package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

// PipelineStats holds pipeline health metrics inferred from DB timestamps.
type PipelineStats struct {
	LastIngest      time.Time
	LastScored      time.Time
	TotalActivities int
}

// PipelineStats returns pipeline health indicators from existing tables.
func (s *Store) PipelineStats(ctx context.Context) (*PipelineStats, error) {
	var ps PipelineStats
	var lastIngest sql.NullTime

	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(hour), COUNT(*) FROM devtrace_contributor_activity`).Scan(&lastIngest, &ps.TotalActivities)
	if err != nil {
		return nil, fmt.Errorf("pipeline stats activity: %w", err)
	}
	if lastIngest.Valid {
		ps.LastIngest = lastIngest.Time
	}

	var lastScored sql.NullTime
	err = s.db.QueryRowContext(ctx,
		`SELECT MAX(scored_at) FROM devtrace_reputation_history`).Scan(&lastScored)
	if err != nil {
		return nil, fmt.Errorf("pipeline stats history: %w", err)
	}
	if lastScored.Valid {
		ps.LastScored = lastScored.Time
	}

	return &ps, nil
}

// DailyActivityCount holds a single day's total activity count.
type DailyActivityCount = DailyCount

// DailyActivityCounts returns per-day total activity counts for the last N days.
func (s *Store) DailyActivityCounts(ctx context.Context, days int) ([]DailyCount, error) {
	return s.dailyCounts(ctx,
		`SELECT DATE(hour) AS day, COUNT(*) AS count
		 FROM devtrace_contributor_activity
		 WHERE hour > NOW() - MAKE_INTERVAL(days => $1)
		 GROUP BY DATE(hour)
		 ORDER BY day ASC`, days, "daily activity counts")
}

// HourlyActivityCounts returns per-hour total activity counts for the last N hours.
func (s *Store) HourlyActivityCounts(ctx context.Context, hours int) ([]HourlyCount, error) {
	return s.dailyCounts(ctx,
		`SELECT DATE_TRUNC('hour', hour) AS day, COUNT(*) AS count
		 FROM devtrace_contributor_activity
		 WHERE hour > NOW() - MAKE_INTERVAL(hours => $1)
		 GROUP BY DATE_TRUNC('hour', hour)
		 ORDER BY day ASC`, hours, "hourly activity counts")
}

// HourlySummary represents one hour of aggregated contributor activity from GH Archive.
type HourlySummary struct {
	Username      string
	Provider      string
	Hour          time.Time
	PRsOpened     int
	PRsMerged     int
	PRsClosed     int
	ReviewsGiven  int
	IssueComments int
	IssuesOpened  int
	IssuesClosed  int
	DistinctRepos int
	Repos         []string
}

// BehavioralSignals holds derived behavioral metrics for a contributor.
//
// Deprecated: Use model.Behavior directly. Kept as an alias for backward compatibility.
type BehavioralSignals = model.Behavior

// BatchUpsertActivity upserts hourly summaries into devtrace_contributor_activity.
// On conflict, counts are added to existing values. Returns the number of rows upserted.
// All rows are written in a single transaction, so partial progress is impossible;
// on error the entire batch is rolled back and count 0 is returned.
func (s *Store) BatchUpsertActivity(ctx context.Context, summaries []HourlySummary) (int, error) {
	const query = `INSERT INTO devtrace_contributor_activity (username, provider, hour, prs_opened, prs_merged, prs_closed,
		reviews_given, issue_comments, issues_opened, issues_closed, distinct_repos, repos)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb)
	ON CONFLICT (username, provider, hour) DO UPDATE SET
		prs_opened = devtrace_contributor_activity.prs_opened + EXCLUDED.prs_opened,
		prs_merged = devtrace_contributor_activity.prs_merged + EXCLUDED.prs_merged,
		prs_closed = devtrace_contributor_activity.prs_closed + EXCLUDED.prs_closed,
		reviews_given = devtrace_contributor_activity.reviews_given + EXCLUDED.reviews_given,
		issue_comments = devtrace_contributor_activity.issue_comments + EXCLUDED.issue_comments,
		issues_opened = devtrace_contributor_activity.issues_opened + EXCLUDED.issues_opened,
		issues_closed = devtrace_contributor_activity.issues_closed + EXCLUDED.issues_closed,
		distinct_repos = EXCLUDED.distinct_repos,
		repos = EXCLUDED.repos`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin batch upsert tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	for _, h := range summaries {
		reposJSON, err := json.Marshal(h.Repos)
		if err != nil {
			return 0, fmt.Errorf("marshal repos: %w", err)
		}
		_, err = tx.ExecContext(ctx, query,
			h.Username, h.Provider, h.Hour,
			h.PRsOpened, h.PRsMerged, h.PRsClosed,
			h.ReviewsGiven, h.IssueComments,
			h.IssuesOpened, h.IssuesClosed,
			h.DistinctRepos, reposJSON)
		if err != nil {
			return 0, fmt.Errorf("upsert activity row %d: %w", count, err)
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit batch upsert: %w", err)
	}
	return count, nil
}

// GetBehavioralSignals computes behavioral metrics from the last 90 days of devtrace_contributor_activity.
// Returns nil when no data exists for the contributor.
func (s *Store) GetBehavioralSignals(ctx context.Context, username, provider string) (*BehavioralSignals, error) {
	var (
		prVelocity30d    int
		totalPRs         int
		totalPRsMerged   int
		totalPRsClosed   int
		reviewsGiven30d  int
		issueComments30d int
		activeWeeks      int
		activeDays       int
		activeHourSpread int
		minHour          sql.NullTime
		monthsInWindow   sql.NullFloat64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN hour > NOW() - INTERVAL '30 days' THEN prs_opened ELSE 0 END), 0),
			COALESCE(SUM(prs_opened), 0),
			COALESCE(SUM(prs_merged), 0),
			COALESCE(SUM(prs_closed), 0),
			COALESCE(SUM(CASE WHEN hour > NOW() - INTERVAL '30 days' THEN reviews_given ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN hour > NOW() - INTERVAL '30 days' THEN issue_comments ELSE 0 END), 0),
			COUNT(DISTINCT date_trunc('week', hour)),
			COUNT(DISTINCT DATE(hour)),
			MIN(hour),
			EXTRACT(EPOCH FROM NOW() - MIN(hour)) / 2592000.0,
			COALESCE(COUNT(DISTINCT EXTRACT(hour FROM hour)) FILTER (WHERE hour > NOW() - INTERVAL '90 days'), 0)
		FROM devtrace_contributor_activity
		WHERE username = $1 AND provider = $2 AND hour > NOW() - INTERVAL '90 days'`,
		username, provider,
	).Scan(&prVelocity30d, &totalPRs, &totalPRsMerged, &totalPRsClosed,
		&reviewsGiven30d, &issueComments30d,
		&activeWeeks, &activeDays, &minHour, &monthsInWindow, &activeHourSpread)
	if err != nil {
		return nil, fmt.Errorf("get behavioral signals: %w", err)
	}

	// No data for this contributor.
	if !minHour.Valid {
		return nil, nil
	}

	// Distinct repos in last 90 days via JSONB expansion.
	var distinctRepos90d int
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT r)
		 FROM devtrace_contributor_activity, jsonb_array_elements_text(repos) r
		 WHERE username = $1 AND provider = $2 AND hour > NOW() - INTERVAL '90 days'`,
		username, provider,
	).Scan(&distinctRepos90d)
	if err != nil {
		return nil, fmt.Errorf("get distinct repos: %w", err)
	}

	// Total weeks in the 90-day window.
	totalWeeks := 90.0 / 7.0
	consistency := float64(activeWeeks) / totalWeeks
	consistency = math.Min(consistency, 1.0)

	var baseline float64
	if monthsInWindow.Valid && monthsInWindow.Float64 > 0 {
		baseline = float64(totalPRs) / monthsInWindow.Float64
	}

	// Burst-vanish: peak-to-median weekly activity ratio.
	var peakRatio sql.NullFloat64
	var daysSince sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`WITH weekly AS (
			SELECT date_trunc('week', hour) AS week,
				SUM(prs_opened + reviews_given + issue_comments) AS activity
			FROM devtrace_contributor_activity
			WHERE username = $1 AND provider = $2
				AND hour > NOW() - INTERVAL '90 days'
			GROUP BY 1
			HAVING SUM(prs_opened + reviews_given + issue_comments) > 0
		)
		SELECT
			CASE WHEN COUNT(*) >= 2
				THEN MAX(activity)::float / GREATEST(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY activity), 1)
				ELSE NULL
			END,
			EXTRACT(days FROM NOW() - MAX(week))::int
		FROM weekly`,
		username, provider,
	).Scan(&peakRatio, &daysSince)
	if err != nil {
		return nil, fmt.Errorf("get burst vanish: %w", err)
	}

	result := &BehavioralSignals{
		PRVelocity30d:             prVelocity30d,
		PRVelocityBaseline:        baseline,
		ReviewsGiven30d:           reviewsGiven30d,
		IssueComments30d:          issueComments30d,
		DistinctRepos90d:          distinctRepos90d,
		ConsistencyScore:          consistency,
		ActiveSince:               minHour.Time,
		TotalPRsMerged:            totalPRsMerged,
		TotalPRsClosed:            totalPRsClosed,
		ActiveDays:                activeDays,
		ActiveHourSpread:          activeHourSpread,
		BurstVanishPeakRatio:      0,
		BurstVanishDaysSince:      0,
		BurstVanishDataSufficient: false,
	}

	if peakRatio.Valid {
		result.BurstVanishPeakRatio = peakRatio.Float64
		result.BurstVanishDaysSince = int(daysSince.Int64)
		result.BurstVanishDataSufficient = true
	}

	return result, nil
}

// CompactActivity aggregates hourly rows older than the given age into weekly
// buckets (Monday 00:00 UTC), then deletes the originals. Runs in a single
// transaction so a failure leaves data unchanged. Returns rows deleted.
func (s *Store) CompactActivity(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin compact tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Step 1: Insert weekly aggregates from hourly rows older than cutoff.
	// The week bucket is the Monday 00:00 UTC of each row's week.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO devtrace_contributor_activity
			(username, provider, hour, prs_opened, prs_merged, prs_closed,
			 reviews_given, issue_comments, issues_opened, issues_closed, distinct_repos, repos)
		SELECT
			username, provider,
			date_trunc('week', hour) AS week_hour,
			SUM(prs_opened), SUM(prs_merged), SUM(prs_closed),
			SUM(reviews_given), SUM(issue_comments),
			SUM(issues_opened), SUM(issues_closed),
			0,
			'[]'::jsonb
		FROM devtrace_contributor_activity
		WHERE hour < $1
		  AND hour != date_trunc('week', hour)
		GROUP BY username, provider, date_trunc('week', hour)
		ON CONFLICT (username, provider, hour) DO UPDATE SET
			prs_opened = devtrace_contributor_activity.prs_opened + EXCLUDED.prs_opened,
			prs_merged = devtrace_contributor_activity.prs_merged + EXCLUDED.prs_merged,
			prs_closed = devtrace_contributor_activity.prs_closed + EXCLUDED.prs_closed,
			reviews_given = devtrace_contributor_activity.reviews_given + EXCLUDED.reviews_given,
			issue_comments = devtrace_contributor_activity.issue_comments + EXCLUDED.issue_comments,
			issues_opened = devtrace_contributor_activity.issues_opened + EXCLUDED.issues_opened,
			issues_closed = devtrace_contributor_activity.issues_closed + EXCLUDED.issues_closed,
			distinct_repos = 0,
			repos = '[]'::jsonb`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("compact insert: %w", err)
	}

	// Step 2: Delete the original hourly rows (but not the weekly bucket rows we just created).
	res, err := tx.ExecContext(ctx, `
		DELETE FROM devtrace_contributor_activity
		WHERE hour < $1
		  AND hour != date_trunc('week', hour)`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("compact delete: %w", err)
	}

	deleted, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("compact commit: %w", err)
	}

	return deleted, nil
}

// PruneActivity deletes all activity rows older than the given retention
// window. Returns rows deleted. Run after CompactActivity to remove both
// hourly and compacted rows beyond the retention limit.
func (s *Store) PruneActivity(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE hour < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune activity: %w", err)
	}
	return res.RowsAffected()
}
