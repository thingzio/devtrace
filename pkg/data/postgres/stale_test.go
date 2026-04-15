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

func TestUpdateReputation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "stale-test-user"
	const provider = "github"

	// Create contributor and initial reputation via SyncDeveloperToDevTrace.
	if err := store.UpsertContributor(ctx, user, provider); err != nil {
		t.Fatalf("upsert contributor: %v", err)
	}

	// Insert initial reputation row.
	db := store.DB()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO devtrace_reputation (username, provider, score, grade, model_version, deep)
		 VALUES ($1, $2, 0.40, 'D', '3.2.0', false)
		 ON CONFLICT DO NOTHING`,
		user, provider); err != nil {
		t.Fatalf("insert initial reputation: %v", err)
	}

	// Update with new signals.
	signals := &score.InputSignals{
		AgeDays:      365,
		Commits:      100,
		TotalCommits: 500,
		Followers:    10,
		Following:    5,
		PublicRepos:  8,
	}
	if err := store.UpdateReputation(ctx, user, provider, 0.72, "B", "v0.0.1-test", signals); err != nil {
		t.Fatalf("update reputation: %v", err)
	}

	// Verify the update.
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
