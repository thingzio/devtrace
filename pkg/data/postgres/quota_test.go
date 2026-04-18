package postgres_test

import (
	"context"
	"testing"
	"time"
)

func TestRecordAndGetTokenQuotaSamples(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Clean up any leftover samples.
	_, _ = store.DB().ExecContext(ctx, `DELETE FROM devtrace_token_quota_sample WHERE installation_id IN (99001, 99002)`)

	// Record some samples.
	if err := store.RecordTokenQuotaSample(ctx, 99001, "org-a", 5000, 100); err != nil {
		t.Fatalf("record sample 1: %v", err)
	}
	if err := store.RecordTokenQuotaSample(ctx, 99002, "org-b", 5000, 200); err != nil {
		t.Fatalf("record sample 2: %v", err)
	}

	// Retrieve samples from the last hour.
	since := time.Now().UTC().Add(-time.Hour)
	samples, err := store.GetTokenQuotaSamples(ctx, since)
	if err != nil {
		t.Fatalf("get samples: %v", err)
	}
	if len(samples) < 2 {
		t.Fatalf("expected at least 2 samples, got %d", len(samples))
	}

	// Verify we can find our samples.
	found := map[int64]bool{}
	for _, s := range samples {
		if s.InstallationID == 99001 || s.InstallationID == 99002 {
			found[s.InstallationID] = true
		}
	}
	if !found[99001] || !found[99002] {
		t.Errorf("missing expected samples, found: %v", found)
	}

	// Verify sample fields.
	for _, s := range samples {
		if s.InstallationID == 99001 {
			if s.Login != "org-a" {
				t.Errorf("login = %q, want %q", s.Login, "org-a")
			}
			if s.QuotaLimit != 5000 {
				t.Errorf("limit = %d, want 5000", s.QuotaLimit)
			}
			if s.QuotaUsed != 100 {
				t.Errorf("used = %d, want 100", s.QuotaUsed)
			}
			if s.SampledAt.IsZero() {
				t.Error("sampled_at should not be zero")
			}
		}
	}
}

func TestGetTokenQuotaSamples_Empty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Query for a future time range — should return nothing.
	since := time.Now().UTC().Add(time.Hour)
	samples, err := store.GetTokenQuotaSamples(ctx, since)
	if err != nil {
		t.Fatalf("get samples: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("expected 0 samples for future range, got %d", len(samples))
	}
}

func TestPurgeTokenQuotaSamples(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert a sample with old timestamp.
	_, err := store.DB().ExecContext(ctx,
		`INSERT INTO devtrace_token_quota_sample (sampled_at, installation_id, login, quota_limit, quota_used)
		 VALUES ($1, 99003, 'org-old', 5000, 500)`,
		time.Now().UTC().Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("insert old sample: %v", err)
	}

	// Purge samples older than 24 hours.
	threshold := time.Now().UTC().Add(-24 * time.Hour)
	purged, err := store.PurgeTokenQuotaSamples(ctx, threshold)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purged < 1 {
		t.Errorf("expected at least 1 purged, got %d", purged)
	}
}
