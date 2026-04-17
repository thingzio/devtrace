package postgres_test

import (
	"context"
	"testing"
	"time"
)

func TestDailyScoringCounts(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Skipf("migrate: %v", err)
	}

	counts, err := store.DailyScoringCounts(ctx, 7)
	if err != nil {
		t.Fatalf("DailyScoringCounts: %v", err)
	}
	_ = counts
}

func TestHourlyScoringCounts(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Skipf("migrate: %v", err)
	}

	counts, err := store.HourlyScoringCounts(ctx, 12)
	if err != nil {
		t.Fatalf("HourlyScoringCounts: %v", err)
	}
	_ = counts
}

func TestSaveAndGetHistory(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "history-test-user"
	const provider = "github"

	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor: %v", err)
	}

	// Idempotent — second call should not error.
	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor (idempotent): %v", err)
	}

	if err := store.SaveScoreHistory(ctx, user, provider, 0.75, "B", false); err != nil {
		t.Fatalf("save history 1: %v", err)
	}
	if err := store.SaveScoreHistory(ctx, user, provider, 0.80, "B+", false); err != nil {
		t.Fatalf("save history 2: %v", err)
	}

	entries, err := store.GetScoreHistory(ctx, user, provider, 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	// Oldest first.
	first := entries[0]
	last := entries[len(entries)-1]
	if !first.ScoredAt.Before(last.ScoredAt) && !first.ScoredAt.Equal(last.ScoredAt) {
		t.Errorf("entries not oldest-first: first=%v last=%v", first.ScoredAt, last.ScoredAt)
	}

	// Verify the last two entries match what we inserted.
	secondLast := entries[len(entries)-2]
	if secondLast.Score != 0.75 || secondLast.Grade != "B" {
		t.Errorf("entry[-2]: got score=%f grade=%s, want 0.75/B", secondLast.Score, secondLast.Grade)
	}
	if last.Score != 0.80 || last.Grade != "B+" {
		t.Errorf("entry[-1]: got score=%f grade=%s, want 0.80/B+", last.Score, last.Grade)
	}

	// Empty result for unknown user.
	empty, err := store.GetScoreHistory(ctx, "nonexistent-user", provider, 10)
	if err != nil {
		t.Fatalf("get history (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 entries for unknown user, got %d", len(empty))
	}
}

func TestPruneScoreHistory(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "prune-hist-user"
	const provider = "github"

	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor: %v", err)
	}

	t.Cleanup(func() {
		db := store.DB()
		_, _ = db.ExecContext(ctx, `DELETE FROM devtrace_reputation_history WHERE username = $1`, user)
		_, _ = db.ExecContext(ctx, `DELETE FROM devtrace_contributor WHERE username = $1`, user)
	})

	// Insert an old row (500 days ago) via raw SQL.
	db := store.DB()
	_, err := db.ExecContext(ctx,
		`INSERT INTO devtrace_reputation_history (username, provider, score, grade, deep, scored_at)
		 VALUES ($1, $2, 0.75, 'B', false, NOW() - INTERVAL '500 days')`,
		user, provider)
	if err != nil {
		t.Fatalf("insert old history row: %v", err)
	}

	// Insert a recent row via the Store method.
	err = store.SaveScoreHistory(ctx, user, provider, 0.80, "B", false)
	if err != nil {
		t.Fatalf("save recent history: %v", err)
	}

	// Prune with 400-day retention — should delete the 500-day-old row.
	deleted, err := store.PruneScoreHistory(ctx, 400*24*time.Hour)
	if err != nil {
		t.Fatalf("prune score history: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 row deleted, got %d", deleted)
	}

	// Only the recent row should remain.
	entries, err := store.GetScoreHistory(ctx, user, provider, 10)
	if err != nil {
		t.Fatalf("get history after prune: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after prune, got %d", len(entries))
	}
}
