package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

const (
	getCrossVCSSQL = `
		SELECT total_matched, matches, fetched_at
		FROM devtrace_cross_vcs
		WHERE provider = $1 AND username = $2`

	upsertCrossVCSSQL = `
		INSERT INTO devtrace_cross_vcs
			(provider, username, total_matched, matches, fetched_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (provider, username) DO UPDATE SET
			total_matched = EXCLUDED.total_matched,
			matches       = EXCLUDED.matches,
			fetched_at    = NOW()`
)

// GetCrossVCS returns the cached cross-VCS match summary for a user.
// Returns (nil, zero-time, nil) when no row exists.
//
// A row with TotalMatched=0 and an empty Matches slice is the
// sentinel for "we looked, no shared keys found"; the row keeps a
// non-zero fetched_at so the service-layer TTL check suppresses
// re-asking against forges the contributor doesn't use. The UI
// render path treats len(Matches)==0 as "no cross-VCS data" and
// omits the section.
func (s *Store) GetCrossVCS(ctx context.Context, provider, username string) (*model.CrossVCS, time.Time, error) {
	var (
		out         model.CrossVCS
		matchesJSON []byte
		fetchedAt   time.Time
	)
	err := s.db.QueryRowContext(ctx, getCrossVCSSQL, provider, username).Scan(
		&out.TotalMatched, &matchesJSON, &fetchedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query cross_vcs %s: %w", username, err)
	}
	if len(matchesJSON) > 0 {
		if err := json.Unmarshal(matchesJSON, &out.Matches); err != nil {
			return nil, time.Time{}, fmt.Errorf("unmarshal cross_vcs matches: %w", err)
		}
	}
	return &out, fetchedAt, nil
}

// SaveCrossVCS upserts the cross-VCS row. Pass a non-nil summary
// with TotalMatched=0 and an empty Matches slice to record a "no
// shared keys" sentinel — same TTL semantics as a real match,
// suppressing repeated re-fetching for users without a cross-VCS
// presence.
func (s *Store) SaveCrossVCS(ctx context.Context, provider, username string, summary *model.CrossVCS) error {
	if summary == nil {
		return errors.New("save cross_vcs: nil summary")
	}
	matches := summary.Matches
	if matches == nil {
		matches = []model.ForgeMatch{}
	}
	matchesJSON, err := json.Marshal(matches)
	if err != nil {
		return fmt.Errorf("marshal cross_vcs matches: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, upsertCrossVCSSQL,
		provider, username, summary.TotalMatched, matchesJSON,
	); err != nil {
		return fmt.Errorf("upsert cross_vcs %s: %w", username, err)
	}
	return nil
}
