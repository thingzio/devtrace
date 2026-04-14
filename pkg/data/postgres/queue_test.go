package postgres_test

import (
	"context"
	"testing"
)

func TestEnqueueAndDequeue(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "queue-test-user"
	const provider = "github"

	// Clean up from prior runs.
	_ = store.RemoveFromQueue(ctx, user, provider)

	// Enqueue at priority 2.
	if err := store.EnqueueForScoring(ctx, user, provider, 2); err != nil {
		t.Fatalf("enqueue P2: %v", err)
	}

	// Enqueue same user at priority 1 — should upgrade.
	if err := store.EnqueueForScoring(ctx, user, provider, 1); err != nil {
		t.Fatalf("enqueue P1: %v", err)
	}

	entries, err := store.DequeueForScoring(ctx, 10)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Username == user && e.Provider == provider {
			found = true
			if e.Priority != 1 {
				t.Errorf("expected priority 1, got %d", e.Priority)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected user in queue")
	}

	// Clean up.
	_ = store.RemoveFromQueue(ctx, user, provider)
}

func TestRemoveFromQueue(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "queue-rm-user"
	const provider = "github"

	// Clean up from prior runs.
	_ = store.RemoveFromQueue(ctx, user, provider)

	if err := store.EnqueueForScoring(ctx, user, provider, 2); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := store.RemoveFromQueue(ctx, user, provider); err != nil {
		t.Fatalf("remove: %v", err)
	}

	entries, err := store.DequeueForScoring(ctx, 100)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	for _, e := range entries {
		if e.Username == user && e.Provider == provider {
			t.Fatal("expected user removed from queue")
		}
	}
}

func TestContributorExists(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "exists-test-user"
	const provider = "github"

	// Clean up from prior runs.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM reputation WHERE username = $1 AND provider = $2`, user, provider)
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM contributor WHERE username = $1 AND provider = $2`, user, provider)

	exists, err := store.ContributorExists(ctx, user, provider)
	if err != nil {
		t.Fatalf("exists (missing): %v", err)
	}
	if exists {
		t.Fatal("expected false for missing contributor")
	}

	// Insert contributor + reputation row.
	_, err = store.DB().ExecContext(ctx,
		`INSERT INTO contributor (username, provider) VALUES ($1, $2)`, user, provider)
	if err != nil {
		t.Fatalf("insert contributor: %v", err)
	}
	_, err = store.DB().ExecContext(ctx,
		`INSERT INTO reputation (username, provider, score, grade, model_version)
		 VALUES ($1, $2, 0.5, 'C', 'test')`, user, provider)
	if err != nil {
		t.Fatalf("insert reputation: %v", err)
	}

	exists, err = store.ContributorExists(ctx, user, provider)
	if err != nil {
		t.Fatalf("exists (present): %v", err)
	}
	if !exists {
		t.Fatal("expected true for existing contributor")
	}

	// Clean up.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM reputation WHERE username = $1 AND provider = $2`, user, provider)
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM contributor WHERE username = $1 AND provider = $2`, user, provider)
}

func TestGetTenantRepos(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Returns empty map when no installations exist (OK for local dev).
	repos, err := store.GetTenantRepos(ctx)
	if err != nil {
		t.Fatalf("get tenant repos: %v", err)
	}
	if repos == nil {
		t.Fatal("expected non-nil map")
	}
	t.Logf("got %d tenant repos", len(repos))
}
