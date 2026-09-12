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

package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RecordUsage logs a scoring event for quota tracking.
// source indicates how the score was triggered ("ui" or "api").
// repo is the optional owner/repo context (empty string for global scoring).
func RecordUsage(ctx context.Context, db *sql.DB, tenantID, username, provider, source string, deep bool, repo string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO devtrace_usage_record (tenant_id, username_scored, provider, source, deep, repo) VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, username, provider, source, deep, repo)
	if err != nil {
		return fmt.Errorf("recording usage: %w", err)
	}
	return nil
}

// GetUsageCount returns the number of unique contributors scored by a tenant since the given time.
func GetUsageCount(ctx context.Context, db *sql.DB, tenantID string, since time.Time) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT username_scored) FROM devtrace_usage_record WHERE tenant_id = $1 AND scored_at >= $2`,
		tenantID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting usage count: %w", err)
	}
	return count, nil
}

// BillingPeriodStart returns the start of the current monthly billing period
// (first day of the current month, midnight UTC).
func BillingPeriodStart() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// RecentScored represents a recently scored contributor.
type RecentScored struct {
	Username string
	Provider string
	Source   string
	Deep     bool
	Repo     string
	ScoredAt time.Time
}

// Scope returns a human-readable label for the scoring scope.
func (r RecentScored) Scope() string {
	switch {
	case r.Repo != "" && r.Deep:
		return "Repo+Deep"
	case r.Repo != "":
		return "Repo"
	case r.Deep:
		return "Deep"
	default:
		return "Global"
	}
}

// GetRecentScored returns the most recently scored contributors for a tenant.
func GetRecentScored(ctx context.Context, db *sql.DB, tenantID string, limit int) ([]RecentScored, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT username_scored, provider, source, deep, repo, scored_at FROM (
			SELECT DISTINCT ON (username_scored) username_scored, provider, source, deep, repo, scored_at
			FROM devtrace_usage_record WHERE tenant_id = $1
			ORDER BY username_scored, scored_at DESC
		 ) sub ORDER BY scored_at DESC LIMIT $2`,
		tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent scored: %w", err)
	}
	defer rows.Close()

	var result []RecentScored
	for rows.Next() {
		var r RecentScored
		if err := rows.Scan(&r.Username, &r.Provider, &r.Source, &r.Deep, &r.Repo, &r.ScoredAt); err != nil {
			return nil, fmt.Errorf("scan recent: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// NextBillingPeriodStart returns the start of the next monthly billing period.
func NextBillingPeriodStart() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}
