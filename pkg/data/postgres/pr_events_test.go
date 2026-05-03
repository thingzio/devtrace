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
