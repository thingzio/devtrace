package postgres

import (
	"context"
	"fmt"
	"time"
)

// ScoringMetrics holds time-bucketed scoring counts for the admin dashboard.
type ScoringMetrics struct {
	Last1h   int
	Last24h  int
	Last72h  int
	ThisWeek int
}

// ScoringMetrics returns time-bucketed counts of scores recorded.
func (s *Store) ScoringMetrics(ctx context.Context) (*ScoringMetrics, error) {
	var m ScoringMetrics
	err := s.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*) FILTER (WHERE scored_at > NOW() - INTERVAL '1 hour'),
			COUNT(*) FILTER (WHERE scored_at > NOW() - INTERVAL '24 hours'),
			COUNT(*) FILTER (WHERE scored_at > NOW() - INTERVAL '72 hours'),
			COUNT(*) FILTER (WHERE scored_at > DATE_TRUNC('week', NOW()))
		 FROM devtrace_reputation_history`).Scan(
		&m.Last1h, &m.Last24h, &m.Last72h, &m.ThisWeek)
	if err != nil {
		return nil, fmt.Errorf("scoring metrics: %w", err)
	}
	return &m, nil
}

// ScoreHistoryEntry represents a single point on a score trend chart.
type ScoreHistoryEntry struct {
	Score    float64   `json:"score"`
	Grade    string    `json:"grade"`
	ScoredAt time.Time `json:"scored_at"`
}

// GetScoreHistory returns up to limit history entries for a contributor,
// ordered oldest-first (suitable for left-to-right chart rendering).
func (s *Store) GetScoreHistory(ctx context.Context, username, provider string, limit int) ([]ScoreHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT score, grade, scored_at FROM devtrace_reputation_history
		 WHERE username = $1 AND provider = $2
		 ORDER BY scored_at DESC LIMIT $3`, username, provider, limit)
	if err != nil {
		return nil, fmt.Errorf("get score history: %w", err)
	}
	defer rows.Close()

	var result []ScoreHistoryEntry
	for rows.Next() {
		var e ScoreHistoryEntry
		if err := rows.Scan(&e.Score, &e.Grade, &e.ScoredAt); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}

	// Reverse so oldest first (left-to-right on chart).
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// SaveScoreHistory inserts a new reputation history entry.
// The contributor row must already exist (FK constraint).
func (s *Store) SaveScoreHistory(ctx context.Context, username, provider string, score float64, grade string, deep bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_reputation_history (username, provider, score, grade, deep) VALUES ($1, $2, $3, $4, $5)`,
		username, provider, score, grade, deep)
	if err != nil {
		return fmt.Errorf("save score history: %w", err)
	}
	return nil
}

// UpsertContributor ensures a contributor row exists for the given identity.
// Uses ON CONFLICT DO NOTHING so it is idempotent.
func (s *Store) UpsertContributor(ctx context.Context, username, provider string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_contributor (username, provider) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		username, provider)
	if err != nil {
		return fmt.Errorf("upsert contributor: %w", err)
	}
	return nil
}
