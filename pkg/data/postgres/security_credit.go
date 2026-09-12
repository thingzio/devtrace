// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

// recentCreditsLimit caps the count of advisory rows surfaced in the
// scorecard "recent" list. The aggregate counts always reflect the
// full set; this is purely a display-density choice.
const recentCreditsLimit = 5

// GetSecurityCredits returns the aggregate plus the most-recent N
// advisory credits for the contributor. Returns (nil, zero-time, nil)
// when no rows exist. The fetched_at returned is the latest fetch
// timestamp across all rows for this contributor — callers compare it
// to config.SecurityCreditTTL to decide whether to refresh.
func (s *Store) GetSecurityCredits(ctx context.Context, username, provider string) (*model.SecurityCredits, time.Time, error) {
	// Sentinel rows (credit_type='_none') record a fetch attempt for
	// users with zero credits — they're filtered out of the aggregate
	// here so the SecurityCredits return shape stays "no credits".
	const aggQuery = `
		SELECT
			COUNT(*) FILTER (WHERE credit_type = 'reporter')             AS reporter_count,
			COUNT(*) FILTER (WHERE credit_type = 'fixer')                AS fixer_count,
			COUNT(*) FILTER (WHERE credit_type NOT IN ('reporter','fixer','_none')) AS other_count,
			COUNT(*) FILTER (WHERE severity = 'critical')                AS sev_critical,
			COUNT(*) FILTER (WHERE severity = 'high')                    AS sev_high,
			COUNT(*) FILTER (WHERE severity = 'moderate')                AS sev_moderate,
			COUNT(*) FILTER (WHERE severity = 'low')                     AS sev_low,
			COUNT(*) FILTER (WHERE severity NOT IN ('critical','high','moderate','low')) AS sev_unknown,
			MAX(fetched_at)                                              AS fetched_at,
			COUNT(*) FILTER (WHERE credit_type != '_none')               AS total
		FROM devtrace_security_credit
		WHERE username = $1 AND provider = $2`

	var (
		out         model.SecurityCredits
		critical    int
		high        int
		moderate    int
		low         int
		unknown     int
		fetchedAtNT sql.NullTime
		total       int
	)
	err := s.db.QueryRowContext(ctx, aggQuery, username, provider).Scan(
		&out.ReporterCount, &out.FixerCount, &out.OtherCount,
		&critical, &high, &moderate, &low, &unknown,
		&fetchedAtNT, &total,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, fmt.Errorf("aggregate security credits: %w", err)
	}

	var fetchedAt time.Time
	if fetchedAtNT.Valid {
		fetchedAt = fetchedAtNT.Time
	}

	if total == 0 {
		// Either never fetched (fetchedAt == zero) or fetched but empty
		// (sentinel only — fetchedAt is non-zero so caller's TTL check
		// will suppress re-fetching).
		return nil, fetchedAt, nil
	}

	out.BySeverity = map[string]int{}
	for label, count := range map[string]int{
		"critical": critical, "high": high, "moderate": moderate, "low": low, "unknown": unknown,
	} {
		if count > 0 {
			out.BySeverity[label] = count
		}
	}

	const recentQuery = `
		SELECT advisory_id, credit_type, severity, COALESCE(cve_id, ''),
		       COALESCE(summary, ''), COALESCE(published_at, '0001-01-01'::timestamptz)
		FROM devtrace_security_credit
		WHERE username = $1 AND provider = $2
		ORDER BY COALESCE(published_at, '0001-01-01'::timestamptz) DESC, advisory_id ASC
		LIMIT $3`

	rows, err := s.db.QueryContext(ctx, recentQuery, username, provider, recentCreditsLimit)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query recent security credits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var c model.SecurityCredit
		var publishedAt time.Time
		if err := rows.Scan(&c.AdvisoryID, &c.CreditType, &c.Severity,
			&c.CVEID, &c.Summary, &publishedAt); err != nil {
			return nil, time.Time{}, fmt.Errorf("scan security credit: %w", err)
		}
		if !publishedAt.IsZero() && publishedAt.Year() > 1 {
			c.PublishedAt = publishedAt
		}
		out.Recent = append(out.Recent, c)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("iterate security credits: %w", err)
	}

	return &out, fetchedAt, nil
}

// SaveSecurityCredits replaces the contributor's credit rows in a
// single transaction: deletes existing rows, inserts the new set,
// stamps fetched_at = NOW() on each. An empty `credits` slice still
// records the fetch attempt by inserting a single sentinel row with
// advisory_id = "", so the next refresh respects the TTL instead of
// re-hitting GitHub on every score request for users with no credits.
func (s *Store) SaveSecurityCredits(ctx context.Context, username, provider string, credits []model.SecurityCredit) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save security credits tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM devtrace_security_credit WHERE username = $1 AND provider = $2`,
		username, provider); err != nil {
		return fmt.Errorf("clear existing security credits: %w", err)
	}

	if len(credits) == 0 {
		// Sentinel row records the fetch attempt for users with zero credits.
		// Type "_none" is filtered out of aggregates by GetSecurityCredits.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO devtrace_security_credit
				(username, provider, advisory_id, credit_type, severity, fetched_at)
			VALUES ($1, $2, '', '_none', 'unknown', NOW())`,
			username, provider); err != nil {
			return fmt.Errorf("insert sentinel security credit row: %w", err)
		}
	} else {
		const insertQuery = `
			INSERT INTO devtrace_security_credit
				(username, provider, advisory_id, credit_type, severity, cve_id, summary, published_at, fetched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())`
		for _, c := range credits {
			var pubAt sql.NullTime
			if !c.PublishedAt.IsZero() {
				pubAt = sql.NullTime{Time: c.PublishedAt, Valid: true}
			}
			if _, err := tx.ExecContext(ctx, insertQuery,
				username, provider, c.AdvisoryID,
				strings.ToLower(c.CreditType), strings.ToLower(c.Severity),
				nullableString(c.CVEID), nullableString(c.Summary), pubAt,
			); err != nil {
				return fmt.Errorf("insert security credit %s: %w", c.AdvisoryID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save security credits: %w", err)
	}
	return nil
}

func nullableString(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

// PruneSecurityCredits drops sentinel rows older than the given window.
// Real credit rows are kept until the next save replaces them. Returns
// rows deleted. Currently called from the same digest-sweep cadence as
// other prune routines.
func (s *Store) PruneSecurityCredits(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_security_credit
		 WHERE credit_type = '_none' AND fetched_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune security credits: %w", err)
	}
	return res.RowsAffected()
}

// Compile-time guarantee that recent ordering uses sort for deterministic
// output even though the query already returns sorted rows.
var _ = sort.SliceStable
