package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RecordUsage logs a scoring event for quota tracking.
// source indicates how the score was triggered ("ui" or "api").
func RecordUsage(ctx context.Context, db *sql.DB, tenantID, username, provider, source string, deep bool) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO devtrace_usage_record (tenant_id, username_scored, provider, source, deep) VALUES ($1, $2, $3, $4, $5)`,
		tenantID, username, provider, source, deep)
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
	ScoredAt time.Time
}

// GetRecentScored returns the most recently scored contributors for a tenant.
func GetRecentScored(ctx context.Context, db *sql.DB, tenantID string, limit int) ([]RecentScored, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT ON (username_scored) username_scored, provider, source, deep, scored_at
		 FROM devtrace_usage_record WHERE tenant_id = $1
		 ORDER BY username_scored, scored_at DESC`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("get recent scored: %w", err)
	}
	defer rows.Close()

	var all []RecentScored
	for rows.Next() {
		var r RecentScored
		if err := rows.Scan(&r.Username, &r.Provider, &r.Source, &r.Deep, &r.ScoredAt); err != nil {
			return nil, fmt.Errorf("scan recent: %w", err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by scored_at DESC and apply limit in Go since DISTINCT ON
	// requires matching ORDER BY on the first key.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].ScoredAt.After(all[j-1].ScoredAt); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// NextBillingPeriodStart returns the start of the next monthly billing period.
func NextBillingPeriodStart() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}
