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

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNotificationEventDetailSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ev   NotificationEvent
		want string
	}{
		{
			"score_change with grades",
			NotificationEvent{EventType: "score_change", Details: map[string]any{"old_grade": "C", "new_grade": "B"}},
			"grade C → B",
		},
		{
			"score_change missing grades",
			NotificationEvent{EventType: "score_change", Details: map[string]any{}},
			"grade changed",
		},
		{
			"new_contributor opened single PR",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(1)}},
			"opened 1 PR",
		},
		{
			"new_contributor opened multiple PRs",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(3)}},
			"opened 3 PRs",
		},
		{
			"new_contributor PRs and reviews",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(2), "reviews_given": float64(5)}},
			"opened 2 PRs, 5 reviews",
		},
		{
			"new_contributor merged PRs only",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_merged": float64(1)}},
			"merged 1 PR",
		},
		{
			"new_contributor empty details",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{}},
			"first activity",
		},
		{
			"new_contributor only repos in details",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples"}}},
			"first activity",
		},
		{
			"unknown event type",
			NotificationEvent{EventType: "weird", Details: map[string]any{"foo": "bar"}},
			"weird",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.ev.DetailSummary()
			if got != tc.want {
				t.Errorf("DetailSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNotificationEventRepoSummary(t *testing.T) {
	cases := []struct {
		name        string
		ev          NotificationEvent
		wantSummary string
		wantTooltip string
	}{
		{
			"no details",
			NotificationEvent{Target: "NVIDIA"},
			"",
			"",
		},
		{
			"empty repos array",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{}}},
			"",
			"",
		},
		{
			"single repo strips org prefix",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples"}}},
			"cuda-samples",
			"NVIDIA/cuda-samples",
		},
		{
			"single repo no prefix",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{"cuda-samples"}}},
			"cuda-samples",
			"cuda-samples",
		},
		{
			"single repo target is org/repo",
			NotificationEvent{Target: "NVIDIA/cuda-samples", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples"}}},
			"cuda-samples",
			"NVIDIA/cuda-samples",
		},
		{
			"multiple repos shows first plus count",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples", "NVIDIA/cccl", "NVIDIA/TensorRT"}}},
			"cuda-samples +2",
			"NVIDIA/cuda-samples, NVIDIA/cccl, NVIDIA/TensorRT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.RepoSummary(); got != tc.wantSummary {
				t.Errorf("RepoSummary = %q, want %q", got, tc.wantSummary)
			}
			if got := tc.ev.RepoTooltip(); got != tc.wantTooltip {
				t.Errorf("RepoTooltip = %q, want %q", got, tc.wantTooltip)
			}
		})
	}
}

func TestGetNotificationEventsSearch(t *testing.T) {
	store := testStoreInternal(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenantID := seedTestTenant(t, store, "search-tenant")
	wlID := seedTestWatchlist(t, store, tenantID)

	insert := func(username string, repos []string) {
		t.Helper()
		details := map[string]any{}
		if len(repos) > 0 {
			anys := make([]any, len(repos))
			for i, r := range repos {
				anys[i] = r
			}
			details["repos"] = anys
		}
		if err := store.InsertNotificationEvent(ctx, wlID, "new_contributor", username, details); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	insert("alice", []string{"NVIDIA/cuda-samples"})
	insert("bob", []string{"NVIDIA/TensorRT"})
	insert("CarolUpper", []string{"NVIDIA/cccl"})

	cases := []struct {
		name   string
		filter NotificationEventFilter
		want   int
	}{
		{"empty filter returns all", NotificationEventFilter{}, 3},
		{"contributor substring", NotificationEventFilter{Contributor: "ali"}, 1},
		{"contributor case-insensitive", NotificationEventFilter{Contributor: "carolupper"}, 1},
		{"org target substring", NotificationEventFilter{Org: "nvidia"}, 3},
		{"repo in details only", NotificationEventFilter{Repo: "tensorrt"}, 1},
		{"repo matches org-prefixed repo names", NotificationEventFilter{Repo: "nvidia"}, 3},
		{"contributor + repo combine (AND)", NotificationEventFilter{Contributor: "ali", Repo: "tensorrt"}, 0},
		{"org + repo combine (AND)", NotificationEventFilter{Org: "nvidia", Repo: "cccl"}, 1},
		{"no matches", NotificationEventFilter{Contributor: "zzznomatch"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, total, err := store.GetNotificationEvents(ctx, tenantID, tc.filter, 100, 0)
			if err != nil {
				t.Fatalf("get events: %v", err)
			}
			if total != tc.want {
				t.Errorf("total = %d, want %d", total, tc.want)
			}
			if len(events) != tc.want {
				t.Errorf("events = %d, want %d", len(events), tc.want)
			}
		})
	}
}

func TestGetNotificationEventsSince(t *testing.T) {
	store := testStoreInternal(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenantID := seedTestTenant(t, store, "since-tenant")
	wlID := seedTestWatchlist(t, store, tenantID)

	insertAt := func(username string, ageDays int) {
		t.Helper()
		_, err := store.DB().ExecContext(ctx,
			`INSERT INTO devtrace_notification_event (watchlist_id, event_type, username, details, created_at)
			 VALUES ($1, 'new_contributor', $2, '{}'::jsonb, NOW() - $3 * INTERVAL '1 day')`,
			wlID, username, ageDays)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insertAt("today", 0)
	insertAt("recent", 2)
	insertAt("week_ago", 7)
	insertAt("old", 30)

	cutoff := time.Now().Add(-3 * 24 * time.Hour)
	_, total, err := store.GetNotificationEvents(ctx, tenantID, NotificationEventFilter{Since: cutoff}, 100, 0)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if total != 2 {
		t.Errorf("total with since=%s = %d, want 2", cutoff.Format(time.RFC3339), total)
	}
}

// testStoreInternal mirrors the postgres_test.testStore helper for tests
// that need access to unexported package symbols.
func testStoreInternal(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable"
	}
	store, err := New(context.Background(), dsn, DefaultPoolConfig())
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seedTestTenant inserts a tenant with a unique github_id so parallel/repeat
// runs don't collide on the UNIQUE constraint. Cleanup cascades to watchlists
// and notification events via ON DELETE CASCADE.
func seedTestTenant(t *testing.T, store *Store, username string) string {
	t.Helper()
	ctx := context.Background()
	githubID := time.Now().UnixNano()
	var id string
	err := store.DB().QueryRowContext(ctx,
		`INSERT INTO devtrace_tenant (github_id, username, plan, status)
		 VALUES ($1, $2, 'free', 'active')
		 RETURNING id`,
		githubID, username).Scan(&id)
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(context.Background(),
			`DELETE FROM devtrace_tenant WHERE id = $1`, id)
	})
	return id
}

func seedTestWatchlist(t *testing.T, store *Store, tenantID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := store.DB().QueryRowContext(ctx,
		`INSERT INTO devtrace_watchlist (tenant_id, target, source)
		 VALUES ($1, 'NVIDIA', 'manual')
		 RETURNING id`,
		tenantID).Scan(&id)
	if err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}
	// Tenant cascade will clean up watchlist + events.
	return id
}

func TestPruneNotificationEventsAge(t *testing.T) {
	store := testStoreInternal(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenantID := seedTestTenant(t, store, "prune-age-tenant")
	wlID := seedTestWatchlist(t, store, tenantID)

	insertAt := func(username string, ageDays int) {
		t.Helper()
		_, err := store.DB().ExecContext(ctx,
			`INSERT INTO devtrace_notification_event (watchlist_id, event_type, username, details, created_at)
			 VALUES ($1, 'new_contributor', $2, '{}'::jsonb, NOW() - $3 * INTERVAL '1 day')`,
			wlID, username, ageDays)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insertAt("recent", 1)
	insertAt("medium", 10)
	insertAt("old", 100)

	// Starter retention: 30 days. Should delete only "old".
	deleted, err := store.PruneNotificationEvents(ctx, tenantID, 30, 0)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	_, total, err := store.GetNotificationEvents(ctx, tenantID, NotificationEventFilter{}, 100, 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if total != 2 {
		t.Errorf("remaining = %d, want 2", total)
	}
}

func TestPruneNotificationEventsCap(t *testing.T) {
	store := testStoreInternal(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenantID := seedTestTenant(t, store, "prune-cap-tenant")
	wlID := seedTestWatchlist(t, store, tenantID)

	for i := 0; i < 15; i++ {
		_, err := store.DB().ExecContext(ctx,
			`INSERT INTO devtrace_notification_event (watchlist_id, event_type, username, details, created_at)
			 VALUES ($1, 'new_contributor', $2, '{}'::jsonb, NOW() - $3 * INTERVAL '1 minute')`,
			wlID, fmt.Sprintf("user%02d", i), 15-i)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Cap at 10. Retention high (1000) so age pass is a no-op.
	deleted, err := store.PruneNotificationEvents(ctx, tenantID, 1000, 10)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}

	events, total, err := store.GetNotificationEvents(ctx, tenantID, NotificationEventFilter{}, 100, 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if total != 10 {
		t.Errorf("remaining = %d, want 10", total)
	}
	// Newest 10 should be user14..user05 (descending by created_at).
	if events[0].Username != "user14" {
		t.Errorf("newest survivor = %s, want user14", events[0].Username)
	}
	if events[9].Username != "user05" {
		t.Errorf("oldest survivor = %s, want user05", events[9].Username)
	}
}

func TestIntDetail(t *testing.T) {
	t.Parallel()
	d := map[string]any{
		"float": float64(42),
		"int":   7,
		"int64": int64(99),
		"str":   "nope",
	}

	cases := []struct {
		key  string
		want int
	}{
		{"float", 42},
		{"int", 7},
		{"int64", 99},
		{"str", 0},
		{"missing", 0},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := intDetail(d, tc.key)
			if got != tc.want {
				t.Errorf("intDetail(%q) = %d, want %d", tc.key, got, tc.want)
			}
		})
	}
}
