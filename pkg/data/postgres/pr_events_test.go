package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func TestPREventsCorrelatesAuthorAcrossActions(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		repo     = "pr-events-rt-test"
		author   = "human-author-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)

	openedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	mergedAt := time.Date(2026, 3, 5, 18, 0, 0, 0, time.UTC)

	// Hour 1: opened by human author
	if _, err := store.BatchUpsertPREvents(ctx, []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 42, Action: "opened",
			Author: author, OccurAt: openedAt},
	}); err != nil {
		t.Fatalf("upsert opened: %v", err)
	}

	// Hour 2: merged by CI bot — no author on this row, but the row
	// already has author set from the opened path. COALESCE preserves it.
	if _, err := store.BatchUpsertPREvents(ctx, []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 42, Action: "merged",
			Author: "", OccurAt: mergedAt},
	}); err != nil {
		t.Fatalf("upsert merged: %v", err)
	}

	// Author-attributed merge count should now reflect this PR.
	got, err := store.GetAuthoredMergedPRCount(ctx, provider, author)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 1 {
		t.Errorf("authored-merged count: got %d, want 1", got)
	}
}

func TestPREventsPreservesAuthorOnLateMerge(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		repo     = "pr-events-late-merge"
		author   = "late-merge-author"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)
	})

	// Multiple hours apart, three actions on the same PR.
	openedAt := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC) // closed without merge first
	mergedAt := time.Date(2026, 1, 25, 0, 0, 0, 0, time.UTC) // then later merged

	for _, ev := range []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 7, Action: "opened",
			Author: author, OccurAt: openedAt},
		{Provider: provider, Repo: repo, Number: 7, Action: "closed",
			Author: "", OccurAt: closedAt},
		{Provider: provider, Repo: repo, Number: 7, Action: "merged",
			Author: "", OccurAt: mergedAt},
	} {
		if _, err := store.BatchUpsertPREvents(ctx, []postgres.PREventRow{ev}); err != nil {
			t.Fatalf("upsert %s: %v", ev.Action, err)
		}
	}

	// Verify all three timestamps + author landed in a single row.
	var (
		gotAuthor                   string
		gotOpen, gotMerge, gotClose time.Time
	)
	row := store.DB().QueryRowContext(ctx, `
		SELECT author, opened_at, merged_at, closed_at
		FROM devtrace_pr_events
		WHERE provider=$1 AND repo=$2 AND pr_number=$3`,
		provider, repo, 7)
	if err := row.Scan(&gotAuthor, &gotOpen, &gotMerge, &gotClose); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotAuthor != author {
		t.Errorf("author: got %q, want %q", gotAuthor, author)
	}
	if !gotOpen.Equal(openedAt) {
		t.Errorf("opened_at: got %v, want %v", gotOpen, openedAt)
	}
	if !gotMerge.Equal(mergedAt) {
		t.Errorf("merged_at: got %v, want %v", gotMerge, mergedAt)
	}
	if !gotClose.Equal(closedAt) {
		t.Errorf("closed_at: got %v, want %v", gotClose, closedAt)
	}
}

func TestPREventsBotOpenerNotAttributed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		repo     = "pr-events-bot-open"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)
	})

	// Aggregator filters bot-opened PRs to empty Author; the storage
	// layer must not synthesize one. Author stays NULL.
	if _, err := store.BatchUpsertPREvents(ctx, []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 99, Action: "opened",
			Author: "", OccurAt: time.Now()},
		{Provider: provider, Repo: repo, Number: 99, Action: "merged",
			Author: "", OccurAt: time.Now()},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// No author was ever set, so author-attributed count for any user is 0.
	got, err := store.GetAuthoredMergedPRCount(ctx, provider, "anyone")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 0 {
		t.Errorf("expected 0 authored merges, got %d", got)
	}
}

func TestPREventsCountOnlyMergedPRs(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		repo     = "pr-events-count-test"
		author   = "selective-author"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)
	})

	now := time.Now()
	rows := []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 1, Action: "opened",
			Author: author, OccurAt: now},
		{Provider: provider, Repo: repo, Number: 1, Action: "merged",
			Author: "", OccurAt: now},
		// PR 2: opened only — still in flight, not merged
		{Provider: provider, Repo: repo, Number: 2, Action: "opened",
			Author: author, OccurAt: now},
		// PR 3: opened then closed without merging
		{Provider: provider, Repo: repo, Number: 3, Action: "opened",
			Author: author, OccurAt: now},
		{Provider: provider, Repo: repo, Number: 3, Action: "closed",
			Author: "", OccurAt: now},
	}
	if _, err := store.BatchUpsertPREvents(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetAuthoredMergedPRCount(ctx, provider, author)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 1 {
		t.Errorf("count: got %d, want 1 (only PR #1 was merged)", got)
	}
}

// TestPREventsStatsAggregates pins the admin-dashboard metrics:
// total rows, last-24h rows, and the three attribution numbers.
// IngestAttributionPct is the load-bearing one: of PRs we should
// have seen the open for (withAuthor + missingOpen), the share we
// did. Excludes bot-opened rows from the denominator so Renovate
// churn doesn't mask real ingest gaps.
func TestPREventsStatsAggregates(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		repo     = "pr-events-stats-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)

	// Capture baseline (other rows from earlier tests may exist).
	baseline, err := store.PREventsStats(ctx)
	if err != nil {
		t.Fatalf("baseline stats: %v", err)
	}

	// Insert six rows exercising every author/opened_at combination:
	//   3 human-authored opens (author + opened_at)
	//   1 bot-opened then merged (no author, opened_at set)
	//   1 merge-only orphan   (no author, no opened_at)
	//   1 close-only orphan   (no author, no opened_at)
	now := time.Now()
	rows := []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 1, Action: "opened", Author: "alice", OccurAt: now},
		{Provider: provider, Repo: repo, Number: 2, Action: "opened", Author: "bob", OccurAt: now},
		{Provider: provider, Repo: repo, Number: 3, Action: "opened", Author: "carol", OccurAt: now},
		{Provider: provider, Repo: repo, Number: 4, Action: "opened", Author: "", OccurAt: now}, // bot-opened
		{Provider: provider, Repo: repo, Number: 4, Action: "merged", Author: "", OccurAt: now},
		{Provider: provider, Repo: repo, Number: 5, Action: "merged", Author: "", OccurAt: now}, // missing-open
		{Provider: provider, Repo: repo, Number: 6, Action: "closed", Author: "", OccurAt: now}, // missing-open
	}
	if _, uerr := store.BatchUpsertPREvents(ctx, rows); uerr != nil {
		t.Fatalf("upsert: %v", uerr)
	}

	got, err := store.PREventsStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	// 6 unique PR numbers added (PR 4 gets both opened+merged on one row).
	if got.TotalRows != baseline.TotalRows+6 {
		t.Errorf("TotalRows: got %d, want %d", got.TotalRows, baseline.TotalRows+6)
	}
	if got.Last24hRows < baseline.Last24hRows+6 {
		t.Errorf("Last24hRows: got %d, want >=%d",
			got.Last24hRows, baseline.Last24hRows+6)
	}
	if got.BotOpenedRows < baseline.BotOpenedRows+1 {
		t.Errorf("BotOpenedRows: got %d, want >=%d",
			got.BotOpenedRows, baseline.BotOpenedRows+1)
	}
	if got.MissingOpenRows < baseline.MissingOpenRows+2 {
		t.Errorf("MissingOpenRows: got %d, want >=%d",
			got.MissingOpenRows, baseline.MissingOpenRows+2)
	}
	// AuthorAttributionPct = withAuthor / total — in (0, 100].
	if got.AuthorAttributionPct <= 0 || got.AuthorAttributionPct > 100 {
		t.Errorf("AuthorAttributionPct out of range: %v", got.AuthorAttributionPct)
	}
	// IngestAttributionPct excludes bot_opened from denominator, so it
	// must be >= AuthorAttributionPct whenever any bot_opened rows exist.
	if got.IngestAttributionPct < got.AuthorAttributionPct-1e-9 {
		t.Errorf("IngestAttributionPct (%.2f) should be >= AuthorAttributionPct (%.2f) "+
			"when bot-opened rows exist", got.IngestAttributionPct, got.AuthorAttributionPct)
	}
	if got.IngestAttributionPct <= 0 || got.IngestAttributionPct > 100 {
		t.Errorf("IngestAttributionPct out of range: %v", got.IngestAttributionPct)
	}
	if got.LastEventAt.IsZero() {
		t.Error("LastEventAt should be non-zero after upsert")
	}
}

// TestPREventsStatsEmptyTable: stats query handles an empty (or
// near-empty) table without divide-by-zero on either percentage.
func TestPREventsStatsEmptyTable(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Nuke all rows for a clean baseline (avoid contention with parallel
	// tests; this ONE test picks an exclusive cleanup window).
	if _, err := store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_pr_events`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := store.PREventsStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if got.TotalRows != 0 {
		t.Errorf("TotalRows on empty: got %d, want 0", got.TotalRows)
	}
	if got.AuthorAttributionPct != 0 {
		t.Errorf("AuthorAttributionPct on empty: got %v, want 0 (no div-by-zero)",
			got.AuthorAttributionPct)
	}
	if got.IngestAttributionPct != 0 {
		t.Errorf("IngestAttributionPct on empty: got %v, want 0 (no div-by-zero)",
			got.IngestAttributionPct)
	}
	if got.BotOpenedRows != 0 {
		t.Errorf("BotOpenedRows on empty: got %d, want 0", got.BotOpenedRows)
	}
	if got.MissingOpenRows != 0 {
		t.Errorf("MissingOpenRows on empty: got %d, want 0", got.MissingOpenRows)
	}
	if !got.LastEventAt.IsZero() {
		t.Errorf("LastEventAt on empty: got %v, want zero", got.LastEventAt)
	}
}

// TestPREventsStatsIngestAttributionFormula verifies the load-bearing
// metric arithmetic on an exact, isolated dataset: when there's
// one missing-open row and two human-authored rows, ingest attribution
// should be 2/3 (≈66.7%), and bot-opened rows must NOT enter the
// denominator (otherwise Renovate churn would mask real gaps).
func TestPREventsStatsIngestAttributionFormula(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_pr_events`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx, `DELETE FROM devtrace_pr_events`)
	})

	const (
		provider = "github"
		repo     = "ingest-attr-formula"
	)
	now := time.Now()
	rows := []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 1, Action: "opened", Author: "alice", OccurAt: now},
		{Provider: provider, Repo: repo, Number: 2, Action: "opened", Author: "bob", OccurAt: now},
		// PR 3 is bot-opened — must NOT affect IngestAttributionPct.
		{Provider: provider, Repo: repo, Number: 3, Action: "opened", Author: "", OccurAt: now},
		// PR 4 is a missing-open merge — DOES affect IngestAttributionPct.
		{Provider: provider, Repo: repo, Number: 4, Action: "merged", Author: "", OccurAt: now},
	}
	if _, err := store.BatchUpsertPREvents(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.PREventsStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	const want = 2.0 / 3.0 * 100
	if diff := got.IngestAttributionPct - want; diff < -0.01 || diff > 0.01 {
		t.Errorf("IngestAttributionPct: got %.4f, want %.4f (2 authored / (2 authored + 1 missing-open))",
			got.IngestAttributionPct, want)
	}
	if got.BotOpenedRows != 1 {
		t.Errorf("BotOpenedRows: got %d, want 1", got.BotOpenedRows)
	}
	if got.MissingOpenRows != 1 {
		t.Errorf("MissingOpenRows: got %d, want 1", got.MissingOpenRows)
	}
}

func TestPREventsPruneByLastEvent(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		repo     = "pr-events-prune-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_pr_events WHERE provider=$1 AND repo=$2`, provider, repo)
	})

	// Insert a fresh row.
	if _, err := store.BatchUpsertPREvents(ctx, []postgres.PREventRow{
		{Provider: provider, Repo: repo, Number: 100, Action: "opened",
			Author: "u", OccurAt: time.Now()},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Backdate to past the retention horizon.
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE devtrace_pr_events SET last_event_at = NOW() - INTERVAL '200 days'
		 WHERE provider=$1 AND repo=$2 AND pr_number=$3`,
		provider, repo, 100); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	deleted, err := store.PrunePREvents(ctx, 120*24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 row pruned, got %d", deleted)
	}
}
