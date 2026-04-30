package postgres

import (
	"context"
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
	wlID := seedTestWatchlist(t, store, tenantID, "NVIDIA")

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
		name  string
		query string
		want  int
	}{
		{"empty query returns all", "", 3},
		{"username substring", "ali", 1},
		{"username case-insensitive", "carolupper", 1},
		{"target substring", "nvidia", 3},
		{"repo in details", "tensorrt", 1},
		{"no matches", "zzznomatch", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, total, err := store.GetNotificationEvents(ctx, tenantID, tc.query, 100, 0)
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

func seedTestWatchlist(t *testing.T, store *Store, tenantID, target string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := store.DB().QueryRowContext(ctx,
		`INSERT INTO devtrace_watchlist (tenant_id, target, source)
		 VALUES ($1, $2, 'manual')
		 RETURNING id`,
		tenantID, target).Scan(&id)
	if err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}
	// Tenant cascade will clean up watchlist + events.
	return id
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
