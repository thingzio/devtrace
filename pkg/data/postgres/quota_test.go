// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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

	// Record some samples. Pass per-family limits so the test exercises
	// both the legacy core columns and the search/graphql additions.
	if err := store.RecordTokenQuotaSample(ctx, 99001, "org-a",
		5000, 100,
		30, 5,
		5000, 50,
	); err != nil {
		t.Fatalf("record sample 1: %v", err)
	}
	if err := store.RecordTokenQuotaSample(ctx, 99002, "org-b",
		5000, 200,
		30, 25,
		5000, 4500,
	); err != nil {
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
			if s.SearchLimit != 30 {
				t.Errorf("search_limit = %d, want 30", s.SearchLimit)
			}
			if s.SearchUsed != 5 {
				t.Errorf("search_used = %d, want 5", s.SearchUsed)
			}
			if s.GraphQLLimit != 5000 {
				t.Errorf("graphql_limit = %d, want 5000", s.GraphQLLimit)
			}
			if s.GraphQLUsed != 50 {
				t.Errorf("graphql_used = %d, want 50", s.GraphQLUsed)
			}
			if s.SampledAt.IsZero() {
				t.Error("sampled_at should not be zero")
			}
		}
	}
}

// TestRecordTokenQuotaSampleLegacy verifies that callers passing zero for
// the search/graphql families (or pre-cutover rows inserted with the old
// 5-column shape) read back cleanly with zero limits/used. The chart
// treats a zero limit as "no data" for that family.
func TestRecordTokenQuotaSampleLegacy(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, _ = store.DB().ExecContext(ctx, `DELETE FROM devtrace_token_quota_sample WHERE installation_id = 99099`)

	// Simulate a pre-cutover row written with the old 5-column statement.
	if _, err := store.DB().ExecContext(ctx,
		`INSERT INTO devtrace_token_quota_sample (installation_id, login, quota_limit, quota_used)
		 VALUES ($1, $2, $3, $4)`,
		int64(99099), "org-legacy", 5000, 42); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}

	since := time.Now().UTC().Add(-time.Hour)
	samples, err := store.GetTokenQuotaSamples(ctx, since)
	if err != nil {
		t.Fatalf("get samples: %v", err)
	}

	found := false
	for _, s := range samples {
		if s.InstallationID != 99099 {
			continue
		}
		found = true
		if s.QuotaUsed != 42 {
			t.Errorf("used = %d, want 42", s.QuotaUsed)
		}
		if s.SearchLimit != 0 || s.SearchUsed != 0 || s.GraphQLLimit != 0 || s.GraphQLUsed != 0 {
			t.Errorf("legacy row should report zero search/graphql, got search=%d/%d graphql=%d/%d",
				s.SearchUsed, s.SearchLimit, s.GraphQLUsed, s.GraphQLLimit)
		}
	}
	if !found {
		t.Fatal("legacy sample not returned")
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
