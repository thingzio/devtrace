package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RecordUsage logs a scoring event for quota tracking.
func RecordUsage(ctx context.Context, db *sql.DB, tenantID, username, provider string, deep bool) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO usage_record (tenant_id, username_scored, provider, deep) VALUES ($1, $2, $3, $4)`,
		tenantID, username, provider, deep)
	if err != nil {
		return fmt.Errorf("recording usage: %w", err)
	}
	return nil
}

// GetUsageCount returns the number of unique contributors scored by a tenant since the given time.
func GetUsageCount(ctx context.Context, db *sql.DB, tenantID string, since time.Time) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT username_scored) FROM usage_record WHERE tenant_id = $1 AND scored_at >= $2`,
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
	ScoredAt time.Time
}

// GetRecentScored returns the most recently scored contributors for a tenant.
func GetRecentScored(ctx context.Context, db *sql.DB, tenantID string, limit int) ([]RecentScored, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT username_scored, MAX(scored_at) as last_scored
		 FROM usage_record WHERE tenant_id = $1
		 GROUP BY username_scored
		 ORDER BY last_scored DESC LIMIT $2`,
		tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent scored: %w", err)
	}
	defer rows.Close()

	var result []RecentScored
	for rows.Next() {
		var r RecentScored
		if err := rows.Scan(&r.Username, &r.ScoredAt); err != nil {
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
