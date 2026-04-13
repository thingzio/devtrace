package postgres_test

import (
	"context"
	"testing"
)

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
