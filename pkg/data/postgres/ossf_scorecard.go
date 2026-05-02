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
	getOSSFScorecardSQL = `
		SELECT score, scorecard_date, COALESCE(commit_sha, ''),
		       COALESCE(scorecard_version, ''), checks, fetched_at
		FROM devtrace_ossf_scorecard
		WHERE provider = $1 AND repo_owner = $2 AND repo_name = $3`

	upsertOSSFScorecardSQL = `
		INSERT INTO devtrace_ossf_scorecard
			(provider, repo_owner, repo_name, score, scorecard_date,
			 commit_sha, scorecard_version, checks, fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (provider, repo_owner, repo_name) DO UPDATE SET
			score             = EXCLUDED.score,
			scorecard_date    = EXCLUDED.scorecard_date,
			commit_sha        = EXCLUDED.commit_sha,
			scorecard_version = EXCLUDED.scorecard_version,
			checks            = EXCLUDED.checks,
			fetched_at        = NOW()`
)

// GetOSSFScorecard returns the cached OSSF Scorecard for the given
// repo. Returns (nil, zero-time, nil) when no row exists. The
// returned fetched_at lets callers compare against config.OSSFTTL.
//
// A non-nil zero-score Scorecard with empty Checks represents the
// "fetched but upstream had no scorecard" sentinel, recorded so we
// don't hammer the OSSF API for repos it has no record of. Callers
// should treat the zero-score sentinel as "no data" — the UI render
// path is gated on len(Checks) > 0.
func (s *Store) GetOSSFScorecard(ctx context.Context, provider, owner, repo string) (*model.OSSFScorecard, time.Time, error) {
	var (
		score       float64
		scoreDateNT sql.NullTime
		commit      string
		scVersion   string
		checksJSON  []byte
		fetchedAt   time.Time
	)
	err := s.db.QueryRowContext(ctx, getOSSFScorecardSQL, provider, owner, repo).Scan(
		&score, &scoreDateNT, &commit, &scVersion, &checksJSON, &fetchedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query ossf scorecard: %w", err)
	}

	out := &model.OSSFScorecard{
		Score:        score,
		Commit:       commit,
		ScorecardVer: scVersion,
	}
	if scoreDateNT.Valid {
		out.Date = scoreDateNT.Time
	}
	if len(checksJSON) > 0 {
		if err := json.Unmarshal(checksJSON, &out.Checks); err != nil {
			return nil, time.Time{}, fmt.Errorf("unmarshal ossf checks: %w", err)
		}
	}
	return out, fetchedAt, nil
}

// SaveOSSFScorecard upserts the scorecard row for a repo. Pass a
// non-nil card with Score=0 and no checks to record a "fetch
// attempted, upstream had nothing" sentinel — same TTL semantics as
// real data, suppressing repeated re-fetching of repos OSSF doesn't
// have a scorecard for.
func (s *Store) SaveOSSFScorecard(ctx context.Context, provider, owner, repo string, card *model.OSSFScorecard) error {
	if card == nil {
		return errors.New("save ossf scorecard: nil card")
	}
	checksJSON, err := json.Marshal(card.Checks)
	if err != nil {
		return fmt.Errorf("marshal ossf checks: %w", err)
	}
	var scoreDate sql.NullTime
	if !card.Date.IsZero() {
		scoreDate = sql.NullTime{Time: card.Date, Valid: true}
	}
	if _, err := s.db.ExecContext(ctx, upsertOSSFScorecardSQL,
		provider, owner, repo,
		card.Score, scoreDate,
		nullableString(card.Commit), nullableString(card.ScorecardVer),
		checksJSON,
	); err != nil {
		return fmt.Errorf("upsert ossf scorecard: %w", err)
	}
	return nil
}
