package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func TestPipelineStats(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ps, err := store.PipelineStats(ctx)
	if err != nil {
		t.Fatalf("pipeline stats: %v", err)
	}
	if ps == nil {
		t.Fatal("expected non-nil pipeline stats")
	}
	if ps.TotalActivities < 0 {
		t.Errorf("expected non-negative total activities, got %d", ps.TotalActivities)
	}
	t.Logf("pipeline stats: ingest=%v scored=%v total=%d", ps.LastIngest, ps.LastScored, ps.TotalActivities)
}

func TestDailyActivityCounts(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Skipf("migrate: %v", err)
	}

	counts, err := store.DailyActivityCounts(ctx, 7)
	if err != nil {
		t.Fatalf("DailyActivityCounts: %v", err)
	}
	// Empty or non-empty depending on test DB state — just verify no error
	_ = counts
}

func TestBatchUpsertActivity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Clean up from prior runs.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE username IN ('act-user-a', 'act-user-b')`)

	hour := time.Date(2025, 8, 1, 10, 0, 0, 0, time.UTC)
	summaries := []postgres.HourlySummary{
		{Username: "act-user-a", Provider: "github", Hour: hour, PRsOpened: 2, PRsMerged: 1, ReviewsGiven: 3, Repos: []string{"org/repo1"}},
		{Username: "act-user-a", Provider: "github", Hour: hour.Add(time.Hour), PRsOpened: 1, IssueComments: 5, Repos: []string{"org/repo2"}},
		{Username: "act-user-b", Provider: "github", Hour: hour, PRsOpened: 4, DistinctRepos: 2, Repos: []string{"org/repo1", "org/repo3"}},
	}

	n, err := store.BatchUpsertActivity(ctx, summaries)
	if err != nil {
		t.Fatalf("batch upsert: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 rows upserted, got %d", n)
	}

	// Verify row count.
	var count int
	err = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_contributor_activity WHERE username IN ('act-user-a', 'act-user-b')`).Scan(&count)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows, got %d", count)
	}

	// Upsert same hour for act-user-a — counts should be added.
	overlap := []postgres.HourlySummary{
		{Username: "act-user-a", Provider: "github", Hour: hour, PRsOpened: 3, PRsMerged: 2, ReviewsGiven: 1, Repos: []string{"org/repo1", "org/repo4"}},
	}
	n, err = store.BatchUpsertActivity(ctx, overlap)
	if err != nil {
		t.Fatalf("upsert overlap: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row upserted, got %d", n)
	}

	// Verify counts were added (original: 2+1+3, overlap: 3+2+1).
	var prsOpened, prsMerged, reviewsGiven int
	err = store.DB().QueryRowContext(ctx,
		`SELECT prs_opened, prs_merged, reviews_given FROM devtrace_contributor_activity
		 WHERE username = 'act-user-a' AND hour = $1`, hour).Scan(&prsOpened, &prsMerged, &reviewsGiven)
	if err != nil {
		t.Fatalf("query merged row: %v", err)
	}
	if prsOpened != 5 {
		t.Errorf("prs_opened: got %d, want 5", prsOpened)
	}
	if prsMerged != 3 {
		t.Errorf("prs_merged: got %d, want 3", prsMerged)
	}
	if reviewsGiven != 4 {
		t.Errorf("reviews_given: got %d, want 4", reviewsGiven)
	}
}

func TestGetBehavioralSignals(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "signals-test-user"
	const provider = "github"

	// Clean up from prior runs.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)

	// No data returns nil.
	sig, err := store.GetBehavioralSignals(ctx, user, provider)
	if err != nil {
		t.Fatalf("get signals (empty): %v", err)
	}
	if sig != nil {
		t.Fatal("expected nil for unknown contributor")
	}

	// Insert test data spanning 60 days.
	now := time.Now().UTC().Truncate(time.Hour)
	for i := range 60 {
		h := now.Add(-time.Duration(i) * 24 * time.Hour)
		repos := []string{"org/repo1"}
		if i%3 == 0 {
			repos = append(repos, "org/repo2")
		}
		summary := postgres.HourlySummary{
			Username:      user,
			Provider:      provider,
			Hour:          h,
			PRsOpened:     2,
			ReviewsGiven:  1,
			IssueComments: 1,
			DistinctRepos: len(repos),
			Repos:         repos,
		}
		n, upsertErr := store.BatchUpsertActivity(ctx, []postgres.HourlySummary{summary})
		if upsertErr != nil {
			t.Fatalf("insert test data day %d: %v", i, upsertErr)
		}
		if n != 1 {
			t.Fatalf("expected 1 row, got %d", n)
		}
	}

	sig, err = store.GetBehavioralSignals(ctx, user, provider)
	if err != nil {
		t.Fatalf("get signals: %v", err)
	}
	if sig == nil {
		t.Fatal("expected non-nil signals")
	}

	// 30 days of data with 2 PRs each = 60.
	if sig.PRVelocity30d < 58 || sig.PRVelocity30d > 62 {
		t.Errorf("pr_velocity_30d: got %d, want ~60", sig.PRVelocity30d)
	}
	if sig.ReviewsGiven30d < 28 || sig.ReviewsGiven30d > 32 {
		t.Errorf("reviews_given_30d: got %d, want ~30", sig.ReviewsGiven30d)
	}
	if sig.IssueComments30d < 28 || sig.IssueComments30d > 32 {
		t.Errorf("issue_comments_30d: got %d, want ~30", sig.IssueComments30d)
	}
	if sig.DistinctRepos90d < 2 {
		t.Errorf("distinct_repos_90d: got %d, want >= 2", sig.DistinctRepos90d)
	}
	if sig.ConsistencyScore < 0.2 || sig.ConsistencyScore > 1.0 {
		t.Errorf("consistency_score: got %f, want 0.2..1.0", sig.ConsistencyScore)
	}
	if sig.ActiveSince.IsZero() {
		t.Error("active_since should not be zero")
	}
	if sig.PRVelocityBaseline <= 0 {
		t.Errorf("pr_velocity_baseline: got %f, want > 0", sig.PRVelocityBaseline)
	}
}

func TestCompactActivity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "compact-test-user"

	// Clean up from prior runs.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)

	// Insert hourly rows across 45 days — old enough for 30-day compaction.
	now := time.Now().UTC().Truncate(time.Hour)
	var inserted int
	for day := range 45 {
		for hour := range 3 { // 3 rows per day
			h := now.Add(-time.Duration(day)*24*time.Hour - time.Duration(hour)*time.Hour)
			s := postgres.HourlySummary{
				Username:      user,
				Provider:      "github",
				Hour:          h,
				PRsOpened:     1,
				ReviewsGiven:  1,
				DistinctRepos: 1,
				Repos:         []string{"org/repo1"},
			}
			if _, err := store.BatchUpsertActivity(ctx, []postgres.HourlySummary{s}); err != nil {
				t.Fatalf("insert day %d hour %d: %v", day, hour, err)
			}
			inserted++
		}
	}

	// Count rows before compaction.
	var beforeCount int
	_ = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_contributor_activity WHERE username = $1`, user).Scan(&beforeCount)
	if beforeCount != inserted {
		t.Fatalf("before compact: got %d rows, want %d", beforeCount, inserted)
	}

	// Compact rows older than 30 days.
	deleted, err := store.CompactActivity(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if deleted == 0 {
		t.Fatal("expected some rows deleted")
	}

	// Rows after compaction should be fewer.
	var afterCount int
	_ = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_contributor_activity WHERE username = $1`, user).Scan(&afterCount)
	if afterCount >= beforeCount {
		t.Errorf("after compact: %d rows should be fewer than before: %d", afterCount, beforeCount)
	}

	// Totals should be preserved: sum of prs_opened should equal inserted count.
	var totalPRs int
	_ = store.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(prs_opened), 0) FROM devtrace_contributor_activity WHERE username = $1`, user).Scan(&totalPRs)
	if totalPRs != inserted {
		t.Errorf("total prs_opened after compact: got %d, want %d", totalPRs, inserted)
	}
}
