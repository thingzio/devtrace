package postgres

import (
	"context"
	"fmt"
	"time"
)

// DailyScoringCount holds one day's scoring count.
type DailyScoringCount = DailyCount

// DailyScoringCounts returns per-day scoring counts for the last N days.
func (s *Store) DailyScoringCounts(ctx context.Context, days int) ([]DailyCount, error) {
	return s.dailyCounts(ctx,
		`SELECT DATE(scored_at) AS day, COUNT(*) AS count
		 FROM devtrace_reputation_history
		 WHERE scored_at > NOW() - MAKE_INTERVAL(days => $1)
		 GROUP BY DATE(scored_at)
		 ORDER BY day ASC`, days, "daily scoring counts")
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
