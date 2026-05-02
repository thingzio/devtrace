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

func TestGetLifetimeActivity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "lifetime-test-user"
	const provider = "github"

	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)

	// No data returns nil, no error.
	la, err := store.GetLifetimeActivity(ctx, user, provider)
	if err != nil {
		t.Fatalf("get lifetime activity (empty): %v", err)
	}
	if la != nil {
		t.Fatal("expected nil for unknown contributor")
	}

	// Insert across a wide time range to verify "lifetime" is not bounded.
	now := time.Now().UTC().Truncate(time.Hour)
	rows := []postgres.HourlySummary{
		{
			Username: user, Provider: provider, Hour: now.Add(-400 * 24 * time.Hour),
			PRsOpened: 5, PRsMerged: 3, IssuesOpened: 2, IssuesClosed: 1,
			ReviewsGiven: 1, IssueComments: 4, Repos: []string{"org/r1"},
		},
		{
			Username: user, Provider: provider, Hour: now.Add(-100 * 24 * time.Hour),
			PRsOpened: 7, PRsMerged: 4, PRsClosed: 1, IssuesOpened: 3,
			ReviewsGiven: 2, IssueComments: 1, Repos: []string{"org/r2"},
		},
		{
			Username: user, Provider: provider, Hour: now.Add(-1 * 24 * time.Hour),
			PRsOpened: 2, PRsMerged: 1, IssuesClosed: 5,
			ReviewsGiven: 6, IssueComments: 2, Repos: []string{"org/r3"},
		},
	}
	if _, ierr := store.BatchUpsertActivity(ctx, rows); ierr != nil {
		t.Fatalf("insert rows: %v", ierr)
	}

	la, err = store.GetLifetimeActivity(ctx, user, provider)
	if err != nil {
		t.Fatalf("get lifetime activity: %v", err)
	}
	if la == nil {
		t.Fatal("expected non-nil lifetime activity")
	}
	if la.PRsOpened != 14 {
		t.Errorf("prs_opened: got %d, want 14", la.PRsOpened)
	}
	if la.PRsMerged != 8 {
		t.Errorf("prs_merged: got %d, want 8", la.PRsMerged)
	}
	if la.PRsClosed != 1 {
		t.Errorf("prs_closed: got %d, want 1", la.PRsClosed)
	}
	if la.ReviewsGiven != 9 {
		t.Errorf("reviews_given: got %d, want 9", la.ReviewsGiven)
	}
	if la.IssueComments != 7 {
		t.Errorf("issue_comments: got %d, want 7", la.IssueComments)
	}
	if la.IssuesOpened != 5 {
		t.Errorf("issues_opened: got %d, want 5", la.IssuesOpened)
	}
	if la.IssuesClosed != 6 {
		t.Errorf("issues_closed: got %d, want 6", la.IssuesClosed)
	}
	if la.ActiveDays != 3 {
		t.Errorf("active_days: got %d, want 3", la.ActiveDays)
	}
	if la.FirstActive == nil || la.LastActive == nil {
		t.Fatal("first_active and last_active should be populated")
	}
	if !la.FirstActive.Before(*la.LastActive) {
		t.Errorf("first_active %v should be before last_active %v", la.FirstActive, la.LastActive)
	}
}

func TestGetTopContributedRepos(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "topcontrib-test-user"
	const provider = "github"

	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)

	// No data: empty slice, no error.
	got, err := store.GetTopContributedRepos(ctx, user, provider, 5)
	if err != nil {
		t.Fatalf("get (empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}

	// Insert: r1 in 4 hours, r2 in 2 hours, r3 in 1 hour. Expected ranking: r1, r2, r3.
	now := time.Now().UTC().Truncate(time.Hour)
	rows := []postgres.HourlySummary{
		{Username: user, Provider: provider, Hour: now.Add(-1 * time.Hour), PRsOpened: 1, Repos: []string{"o/r1", "o/r2"}},
		{Username: user, Provider: provider, Hour: now.Add(-2 * time.Hour), PRsOpened: 1, Repos: []string{"o/r1"}},
		{Username: user, Provider: provider, Hour: now.Add(-3 * time.Hour), PRsOpened: 1, Repos: []string{"o/r1", "o/r2", "o/r3"}},
		{Username: user, Provider: provider, Hour: now.Add(-4 * time.Hour), PRsOpened: 1, Repos: []string{"o/r1"}},
	}
	if _, ierr := store.BatchUpsertActivity(ctx, rows); ierr != nil {
		t.Fatalf("insert rows: %v", ierr)
	}

	got, err = store.GetTopContributedRepos(ctx, user, provider, 5)
	if err != nil {
		t.Fatalf("get top contributed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 repos, got %d: %+v", len(got), got)
	}
	if got[0].Repo != "o/r1" || got[0].Activities != 4 {
		t.Errorf("rank 1: got %s/%d, want o/r1/4", got[0].Repo, got[0].Activities)
	}
	if got[1].Repo != "o/r2" || got[1].Activities != 2 {
		t.Errorf("rank 2: got %s/%d, want o/r2/2", got[1].Repo, got[1].Activities)
	}
	if got[2].Repo != "o/r3" || got[2].Activities != 1 {
		t.Errorf("rank 3: got %s/%d, want o/r3/1", got[2].Repo, got[2].Activities)
	}
	if got[0].LastContribution.IsZero() {
		t.Error("LastContribution should not be zero")
	}

	// Limit honored.
	got2, err := store.GetTopContributedRepos(ctx, user, provider, 2)
	if err != nil {
		t.Fatalf("get top with limit: %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("expected 2 with limit=2, got %d", len(got2))
	}

	// limit <= 0 falls back to default 5.
	got3, err := store.GetTopContributedRepos(ctx, user, provider, 0)
	if err != nil {
		t.Fatalf("get top with limit=0: %v", err)
	}
	if len(got3) != 3 {
		t.Errorf("expected 3 with default limit, got %d", len(got3))
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

func TestPruneActivity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "prune-test-user"

	// Clean up from prior runs.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)

	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)
	})

	now := time.Now().UTC().Truncate(time.Hour)

	// Insert one row 150 days old and one row 10 days old.
	old := []postgres.HourlySummary{{
		Username:  user,
		Provider:  "github",
		Hour:      now.Add(-150 * 24 * time.Hour),
		PRsOpened: 1,
		Repos:     []string{"org/old-repo"},
	}}
	recent := []postgres.HourlySummary{{
		Username:  user,
		Provider:  "github",
		Hour:      now.Add(-10 * 24 * time.Hour),
		PRsOpened: 1,
		Repos:     []string{"org/new-repo"},
	}}

	n, err := store.BatchUpsertActivity(ctx, old)
	if err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 old row upserted, got %d", n)
	}

	n, err = store.BatchUpsertActivity(ctx, recent)
	if err != nil {
		t.Fatalf("insert recent row: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 recent row upserted, got %d", n)
	}

	// Prune with 120-day retention — should delete the 150-day-old row.
	deleted, err := store.PruneActivity(ctx, 120*24*time.Hour)
	if err != nil {
		t.Fatalf("prune activity: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 row pruned, got %d", deleted)
	}

	// Only the recent row should remain.
	var count int
	err = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_contributor_activity WHERE username = $1`, user).Scan(&count)
	if err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row remaining, got %d", count)
	}
}
