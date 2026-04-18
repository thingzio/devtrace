package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/thingzio/devtrace/pkg/score"
)

// StaleContributor represents a contributor whose score is outdated.
type StaleContributor struct {
	Username string
	Provider string
	Score    float64
	ScoredAt time.Time
}

// GetStaleContributors returns contributors whose scores are older than configured thresholds.
// Low scores (<0.5): stale after lowDays. High scores (>=0.5): stale after highDays.
func (s *Store) GetStaleContributors(ctx context.Context, lowDays, highDays, limit int) ([]StaleContributor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.username, r.provider, r.score, r.scored_at
		 FROM devtrace_reputation r
		 WHERE (r.score < 0.5 AND r.scored_at < NOW() - MAKE_INTERVAL(days => $1))
		    OR (r.score >= 0.5 AND r.scored_at < NOW() - MAKE_INTERVAL(days => $2))
		 ORDER BY r.scored_at ASC
		 LIMIT $3`, lowDays, highDays, limit)
	if err != nil {
		return nil, fmt.Errorf("query stale contributors: %w", err)
	}
	defer rows.Close()

	var result []StaleContributor
	for rows.Next() {
		var c StaleContributor
		if err := rows.Scan(&c.Username, &c.Provider, &c.Score, &c.ScoredAt); err != nil {
			return nil, fmt.Errorf("scan stale: %w", err)
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// StaleCount returns the number of contributors whose scores are stale.
func (s *Store) StaleCount(ctx context.Context, lowDays, highDays int) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_reputation
		 WHERE (score < 0.5 AND scored_at < NOW() - MAKE_INTERVAL(days => $1))
		    OR (score >= 0.5 AND scored_at < NOW() - MAKE_INTERVAL(days => $2))`,
		lowDays, highDays).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("stale count: %w", err)
	}
	return count, nil
}

// GetCachedSignals returns the previously stored signals for a contributor.
// Returns nil when no reputation record exists.
func (s *Store) GetCachedSignals(ctx context.Context, username, provider string) (*score.InputSignals, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT signals FROM devtrace_reputation WHERE username = $1 AND provider = $2`,
		username, provider).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("get cached signals: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var sig score.InputSignals
	if err := json.Unmarshal(raw, &sig); err != nil {
		return nil, fmt.Errorf("unmarshal cached signals: %w", err)
	}
	return &sig, nil
}

// UpdateReputation upserts a contributor's reputation score and cached signals.
func (s *Store) UpdateReputation(ctx context.Context, username, provider string, value float64, grade, version string, deep bool, signals *score.InputSignals) error {
	signalsJSON, err := json.Marshal(signals)
	if err != nil {
		return fmt.Errorf("marshal signals: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO devtrace_reputation (username, provider, score, grade, model_version, deep, signals, scored_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NOW())
		 ON CONFLICT (username, provider) DO UPDATE SET
			score = EXCLUDED.score, grade = EXCLUDED.grade, model_version = EXCLUDED.model_version,
			deep = EXCLUDED.deep, signals = COALESCE(EXCLUDED.signals, devtrace_reputation.signals), scored_at = NOW()`,
		username, provider, value, grade, version, deep, signalsJSON)
	if err != nil {
		return fmt.Errorf("upsert reputation: %w", err)
	}
	return nil
}
