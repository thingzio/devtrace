package postgres_test

import (
	"context"
	"testing"

	"github.com/thingzio/devtrace/pkg/score"
)

func TestGetStaleContributorsEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	stale, err := store.GetStaleContributors(ctx, 7, 30, 100)
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale contributors, got %d", len(stale))
	}
}

func TestStaleCount(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	count, err := store.StaleCount(ctx, 7, 30)
	if err != nil {
		t.Fatalf("stale count: %v", err)
	}
	if count < 0 {
		t.Fatalf("expected non-negative count, got %d", count)
	}
	t.Logf("stale count (7/30): %d", count)
}

func TestUpdateReputationInsert(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "rep-insert-user"
	const provider = "github"

	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor: %v", err)
	}

	// No reputation row exists — UpdateReputation should INSERT.
	if err := store.UpdateReputation(ctx, user, provider, 0.65, "C+", "v0.0.1-test", false, nil); err != nil {
		t.Fatalf("insert reputation: %v", err)
	}

	db := store.DB()
	var s float64
	var g string
	var deep bool
	if err := db.QueryRowContext(ctx,
		`SELECT score, grade, deep FROM devtrace_reputation WHERE username = $1 AND provider = $2`,
		user, provider).Scan(&s, &g, &deep); err != nil {
		t.Fatalf("query inserted reputation: %v", err)
	}
	if s != 0.65 {
		t.Errorf("score: got %f, want 0.65", s)
	}
	if g != "C+" {
		t.Errorf("grade: got %q, want C+", g)
	}
	if deep {
		t.Errorf("deep: got true, want false")
	}
}

func TestUpdateReputationUpsert(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "rep-upsert-user"
	const provider = "github"

	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor: %v", err)
	}

	// First call: inserts with nil signals.
	if err := store.UpdateReputation(ctx, user, provider, 0.40, "D", "v1", false, nil); err != nil {
		t.Fatalf("insert reputation: %v", err)
	}

	// Second call: updates with signals — should overwrite score but COALESCE keeps nil → null.
	signals := &score.InputSignals{
		AgeDays:      365,
		Commits:      100,
		TotalCommits: 500,
		Followers:    10,
		Following:    5,
		PublicRepos:  8,
	}
	if err := store.UpdateReputation(ctx, user, provider, 0.72, "B", "v2", true, signals); err != nil {
		t.Fatalf("update reputation: %v", err)
	}

	db := store.DB()
	var s float64
	var g string
	var deep bool
	if err := db.QueryRowContext(ctx,
		`SELECT score, grade, deep FROM devtrace_reputation WHERE username = $1 AND provider = $2`,
		user, provider).Scan(&s, &g, &deep); err != nil {
		t.Fatalf("query updated reputation: %v", err)
	}
	if s != 0.72 {
		t.Errorf("score: got %f, want 0.72", s)
	}
	if g != "B" {
		t.Errorf("grade: got %q, want B", g)
	}
	if !deep {
		t.Errorf("deep: got false, want true")
	}
}

func TestUpdateReputationPreservesSignals(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "rep-preserve-user"
	const provider = "github"

	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor: %v", err)
	}

	// Background scorer writes with signals.
	signals := &score.InputSignals{AgeDays: 100, Commits: 50}
	if err := store.UpdateReputation(ctx, user, provider, 0.60, "C", "v1", true, signals); err != nil {
		t.Fatalf("insert with signals: %v", err)
	}

	// On-demand score writes with nil signals — should preserve existing signals via COALESCE.
	if err := store.UpdateReputation(ctx, user, provider, 0.70, "B-", "v2", false, nil); err != nil {
		t.Fatalf("update with nil signals: %v", err)
	}

	// Verify signals are preserved.
	cached, err := store.GetCachedSignals(ctx, user, provider)
	if err != nil {
		t.Fatalf("get cached signals: %v", err)
	}
	if cached == nil {
		t.Fatal("expected cached signals, got nil")
	}
	if cached.AgeDays != 100 {
		t.Errorf("AgeDays: got %d, want 100", cached.AgeDays)
	}
}
