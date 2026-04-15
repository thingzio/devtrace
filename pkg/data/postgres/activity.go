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
		reviews_given, issue_comments, distinct_repos, repos)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
	ON CONFLICT (username, provider, hour) DO UPDATE SET
		prs_opened = devtrace_contributor_activity.prs_opened + EXCLUDED.prs_opened,
		prs_merged = devtrace_contributor_activity.prs_merged + EXCLUDED.prs_merged,
		prs_closed = devtrace_contributor_activity.prs_closed + EXCLUDED.prs_closed,
		reviews_given = devtrace_contributor_activity.reviews_given + EXCLUDED.reviews_given,
		issue_comments = devtrace_contributor_activity.issue_comments + EXCLUDED.issue_comments,
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
			h.ReviewsGiven, h.IssueComments, h.DistinctRepos,
			reposJSON)
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

// GetBehavioralSignals computes behavioral metrics from the last 180 days of devtrace_contributor_activity.
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
			MIN(hour),
			EXTRACT(EPOCH FROM NOW() - MIN(hour)) / 2592000.0
		FROM devtrace_contributor_activity
		WHERE username = $1 AND provider = $2 AND hour > NOW() - INTERVAL '180 days'`,
		username, provider,
	).Scan(&prVelocity30d, &totalPRs, &totalPRsMerged, &totalPRsClosed,
		&reviewsGiven30d, &issueComments30d,
		&activeWeeks, &minHour, &monthsInWindow)
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

	// Total weeks in the 180-day window.
	totalWeeks := 180.0 / 7.0
	consistency := float64(activeWeeks) / totalWeeks
	consistency = math.Min(consistency, 1.0)

	var baseline float64
	if monthsInWindow.Valid && monthsInWindow.Float64 > 0 {
		baseline = float64(totalPRs) / monthsInWindow.Float64
	}

	return &BehavioralSignals{
		PRVelocity30d:      prVelocity30d,
		PRVelocityBaseline: baseline,
		ReviewsGiven30d:    reviewsGiven30d,
		IssueComments30d:   issueComments30d,
		DistinctRepos90d:   distinctRepos90d,
		ConsistencyScore:   consistency,
		ActiveSince:        minHour.Time,
		TotalPRsMerged:     totalPRsMerged,
		TotalPRsClosed:     totalPRsClosed,
	}, nil
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
			 reviews_given, issue_comments, distinct_repos, repos)
		SELECT
			username, provider,
			date_trunc('week', hour) AS week_hour,
			SUM(prs_opened), SUM(prs_merged), SUM(prs_closed),
			SUM(reviews_given), SUM(issue_comments),
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
