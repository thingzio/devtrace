package postgres

import (
	"context"
	"fmt"
	"time"
)

// QueueEntry represents a pending scoring request.
type QueueEntry struct {
	Username string
	Provider string
	Priority int
	QueuedAt time.Time
}

// EnqueueForScoring adds a contributor to the scoring queue.
// On conflict, keeps the highest priority (lowest number) and updates queued_at
// only when priority is upgraded.
func (s *Store) EnqueueForScoring(ctx context.Context, username, provider string, priority int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_scoring_queue (username, provider, priority)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (username, provider) DO UPDATE SET
			priority = LEAST(devtrace_scoring_queue.priority, EXCLUDED.priority),
			queued_at = CASE WHEN EXCLUDED.priority < devtrace_scoring_queue.priority THEN NOW() ELSE devtrace_scoring_queue.queued_at END`,
		username, provider, priority)
	if err != nil {
		return fmt.Errorf("enqueue for scoring: %w", err)
	}
	return nil
}

// DequeueForScoring returns the highest-priority entries from the scoring queue.
// Priority 1 is highest. Entries are not removed; call RemoveFromQueue after processing.
func (s *Store) DequeueForScoring(ctx context.Context, limit int) ([]QueueEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT username, provider, priority, queued_at
		 FROM devtrace_scoring_queue
		 ORDER BY priority ASC, queued_at ASC
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("dequeue for scoring: %w", err)
	}
	defer rows.Close()

	var result []QueueEntry
	for rows.Next() {
		var e QueueEntry
		if err := rows.Scan(&e.Username, &e.Provider, &e.Priority, &e.QueuedAt); err != nil {
			return nil, fmt.Errorf("scan queue entry: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// RemoveFromQueue deletes a contributor from the scoring queue after processing.
func (s *Store) RemoveFromQueue(ctx context.Context, username, provider string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_scoring_queue WHERE username = $1 AND provider = $2`,
		username, provider)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}
	return nil
}

// ContributorExists checks whether a reputation row exists for a contributor.
func (s *Store) ContributorExists(ctx context.Context, username, provider string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM devtrace_reputation WHERE username = $1 AND provider = $2)`,
		username, provider).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("contributor exists: %w", err)
	}
	return exists, nil
}

// QueueDepth returns the number of entries in the scoring queue.
func (s *Store) QueueDepth(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_scoring_queue`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("queue depth: %w", err)
	}
	return count, nil
}

// ContributorCount returns the total number of known contributors.
func (s *Store) ContributorCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_contributor`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("contributor count: %w", err)
	}
	return count, nil
}

// ScoredCount returns the number of contributors with a reputation score.
func (s *Store) ScoredCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_reputation`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("scored count: %w", err)
	}
	return count, nil
}

// GetTenantRepos returns the set of org/user logins with active GitHub App installations.
// The ingest job uses this to determine if a repo owner is a tenant.
func (s *Store) GetTenantRepos(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_login FROM devtrace_app_installation WHERE suspended_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("get tenant repos: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var login string
		if err := rows.Scan(&login); err != nil {
			return nil, fmt.Errorf("scan tenant login: %w", err)
		}
		result[login] = true
	}
	return result, rows.Err()
}
