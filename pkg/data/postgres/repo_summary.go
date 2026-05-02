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

// GetRepoSummary returns the cached summary for the contributor along
// with its fetched_at timestamp. Returns (nil, zero-time, nil) when no
// row exists. Callers compare fetched_at against config.RepoSummaryTTL
// to decide whether to refresh.
func (s *Store) GetRepoSummary(ctx context.Context, username, provider string) (*model.OwnedRepos, time.Time, error) {
	const query = `
		SELECT total_stars, total_repos, top_repos, languages, fetched_at
		FROM devtrace_repo_summary
		WHERE username = $1 AND provider = $2`

	var (
		out       model.OwnedRepos
		topJSON   sql.NullString
		langsJSON sql.NullString
		fetchedAt time.Time
	)
	err := s.db.QueryRowContext(ctx, query, username, provider).Scan(
		&out.TotalStars, &out.TotalRepos, &topJSON, &langsJSON, &fetchedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("get repo summary: %w", err)
	}

	if topJSON.Valid && topJSON.String != "" {
		if err := json.Unmarshal([]byte(topJSON.String), &out.Top); err != nil {
			return nil, time.Time{}, fmt.Errorf("unmarshal top repos: %w", err)
		}
	}
	if langsJSON.Valid && langsJSON.String != "" {
		if err := json.Unmarshal([]byte(langsJSON.String), &out.Languages); err != nil {
			return nil, time.Time{}, fmt.Errorf("unmarshal languages: %w", err)
		}
	}
	return &out, fetchedAt, nil
}

// SaveRepoSummary upserts the summary row with fetched_at = NOW(). A nil
// summary clears optional sub-fields but still records the fetch attempt
// (so a contributor with zero owned repos isn't refetched every request).
func (s *Store) SaveRepoSummary(ctx context.Context, username, provider string, summary *model.OwnedRepos) error {
	var totalStars int64
	var totalRepos int
	var topBytes, langsBytes []byte
	if summary != nil {
		totalStars = summary.TotalStars
		totalRepos = summary.TotalRepos
		var err error
		if len(summary.Top) > 0 {
			if topBytes, err = json.Marshal(summary.Top); err != nil {
				return fmt.Errorf("marshal top repos: %w", err)
			}
		}
		if len(summary.Languages) > 0 {
			if langsBytes, err = json.Marshal(summary.Languages); err != nil {
				return fmt.Errorf("marshal languages: %w", err)
			}
		}
	}

	const query = `
		INSERT INTO devtrace_repo_summary
			(username, provider, total_stars, total_repos, top_repos, languages, fetched_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, NOW())
		ON CONFLICT (username, provider) DO UPDATE SET
			total_stars = EXCLUDED.total_stars,
			total_repos = EXCLUDED.total_repos,
			top_repos   = EXCLUDED.top_repos,
			languages   = EXCLUDED.languages,
			fetched_at  = EXCLUDED.fetched_at`

	_, err := s.db.ExecContext(ctx, query,
		username, provider, totalStars, totalRepos,
		nullableJSON(topBytes), nullableJSON(langsBytes),
	)
	if err != nil {
		return fmt.Errorf("save repo summary: %w", err)
	}
	return nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
