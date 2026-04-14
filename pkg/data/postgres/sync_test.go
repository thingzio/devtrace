package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func TestSyncState(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const key = "test-sync-cursor"

	// Clean up any leftover state from prior runs.
	_, _ = store.DB().ExecContext(ctx, `DELETE FROM sync_state WHERE key = $1`, key)

	// Should return zero time when key does not exist.
	got, err := store.GetSyncState(ctx, key)
	if err != nil {
		t.Fatalf("get (missing): %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero time, got %v", got)
	}

	// Save and retrieve.
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err = store.SaveSyncState(ctx, key, ts); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = store.GetSyncState(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("round-trip: got %v, want %v", got, ts)
	}

	// Overwrite and verify.
	ts2 := time.Date(2025, 7, 1, 8, 30, 0, 0, time.UTC)
	if err = store.SaveSyncState(ctx, key, ts2); err != nil {
		t.Fatalf("save (overwrite): %v", err)
	}
	got, err = store.GetSyncState(ctx, key)
	if err != nil {
		t.Fatalf("get (overwrite): %v", err)
	}
	if !got.Equal(ts2) {
		t.Fatalf("overwrite: got %v, want %v", got, ts2)
	}
}

func TestSyncDeveloperToDevTrace(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	dev := postgres.DevPulseDeveloper{
		Username:          "sync-test-user",
		FullName:          "Sync Tester",
		Email:             "sync@test.dev",
		Avatar:            "https://example.com/avatar.png",
		Reputation:        0.82,
		ReputationDeep:    true,
		ReputationSignals: `{"commit_frequency":0.9}`,
		UpdatedAt:         time.Now().UTC(),
	}

	if err := store.SyncDeveloperToDevTrace(ctx, dev, "A-", "3.2.0"); err != nil {
		t.Fatalf("sync developer: %v", err)
	}

	// Verify contributor row exists.
	var displayName string
	err := store.DB().QueryRowContext(ctx,
		`SELECT display_name FROM contributor WHERE username = $1 AND provider = 'github'`,
		dev.Username).Scan(&displayName)
	if err != nil {
		t.Fatalf("query contributor: %v", err)
	}
	if displayName != dev.FullName {
		t.Errorf("display_name: got %q, want %q", displayName, dev.FullName)
	}

	// Verify reputation row exists.
	var score float64
	var grade string
	err = store.DB().QueryRowContext(ctx,
		`SELECT score, grade FROM reputation WHERE username = $1 AND provider = 'github'`,
		dev.Username).Scan(&score, &grade)
	if err != nil {
		t.Fatalf("query reputation: %v", err)
	}
	if score != dev.Reputation {
		t.Errorf("score: got %f, want %f", score, dev.Reputation)
	}
	if grade != "A-" {
		t.Errorf("grade: got %q, want %q", grade, "A-")
	}

	// Verify history row exists.
	var histCount int
	err = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reputation_history WHERE username = $1 AND provider = 'github'`,
		dev.Username).Scan(&histCount)
	if err != nil {
		t.Fatalf("query reputation_history: %v", err)
	}
	if histCount < 1 {
		t.Errorf("expected at least 1 history row, got %d", histCount)
	}

	// Idempotent: call again with updated score.
	dev.Reputation = 0.90
	if err = store.SyncDeveloperToDevTrace(ctx, dev, "A", "3.2.0"); err != nil {
		t.Fatalf("sync developer (update): %v", err)
	}

	err = store.DB().QueryRowContext(ctx,
		`SELECT score, grade FROM reputation WHERE username = $1 AND provider = 'github'`,
		dev.Username).Scan(&score, &grade)
	if err != nil {
		t.Fatalf("query reputation (update): %v", err)
	}
	if score != 0.90 {
		t.Errorf("updated score: got %f, want 0.90", score)
	}
	if grade != "A" {
		t.Errorf("updated grade: got %q, want %q", grade, "A")
	}
}

func TestGetDevPulseUpdatedDevelopers(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The developer table belongs to DevPulse and may not exist locally.
	// Check if it exists; skip if not.
	var exists bool
	err := store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'developer')`).Scan(&exists)
	if err != nil {
		t.Fatalf("check developer table: %v", err)
	}
	if !exists {
		t.Skip("developer table does not exist (DevPulse not present locally)")
	}

	// Table exists — verify the query runs without error.
	devs, err := store.GetDevPulseUpdatedDevelopers(ctx, time.Time{}, 10)
	if err != nil {
		t.Fatalf("get devpulse developers: %v", err)
	}
	t.Logf("got %d developers from devpulse", len(devs))
}
