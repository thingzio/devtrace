package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

// --- mock store for runner tests ---

type mockIngestStore struct {
	syncStates map[string]time.Time
	tenantOrgs map[string]bool
	existing   map[string]bool // username -> exists
	enqueued   []enqueuedEntry
	upserted   []postgres.HourlySummary
	compacted        bool
	compactAge       time.Duration
	prunedActivity   bool
	pruneActivityAge time.Duration
	prunedHistory    bool
	pruneHistoryAge  time.Duration
}

type enqueuedEntry struct {
	username string
	priority int
}

func newMockIngestStore() *mockIngestStore {
	return &mockIngestStore{
		syncStates: make(map[string]time.Time),
		tenantOrgs: make(map[string]bool),
		existing:   make(map[string]bool),
	}
}

func (m *mockIngestStore) GetSyncState(_ context.Context, key string) (time.Time, error) {
	return m.syncStates[key], nil
}

func (m *mockIngestStore) SaveSyncState(_ context.Context, key string, val time.Time) error {
	m.syncStates[key] = val
	return nil
}

func (m *mockIngestStore) GetTenantRepos(_ context.Context) (map[string]bool, error) {
	return m.tenantOrgs, nil
}

func (m *mockIngestStore) BatchUpsertActivity(_ context.Context, summaries []postgres.HourlySummary) (int, error) {
	m.upserted = append(m.upserted, summaries...)
	return len(summaries), nil
}

func (m *mockIngestStore) EnqueueForScoring(_ context.Context, username, _ string, priority int) error {
	m.enqueued = append(m.enqueued, enqueuedEntry{username, priority})
	return nil
}

func (m *mockIngestStore) ContributorExists(_ context.Context, username, _ string) (bool, error) {
	return m.existing[username], nil
}

func (m *mockIngestStore) PurgeNonTenantQueue(_ context.Context) (int64, error) {
	return 0, nil
}

func (m *mockIngestStore) CompactActivity(_ context.Context, olderThan time.Duration) (int64, error) {
	m.compacted = true
	m.compactAge = olderThan
	return 42, nil
}

func (m *mockIngestStore) PruneActivity(_ context.Context, retention time.Duration) (int64, error) {
	m.prunedActivity = true
	m.pruneActivityAge = retention
	return 10, nil
}

func (m *mockIngestStore) PruneScoreHistory(_ context.Context, retention time.Duration) (int64, error) {
	m.prunedHistory = true
	m.pruneHistoryAge = retention
	return 5, nil
}

// newTestArchiveServer returns an httptest server that serves gzipped NDJSON
// from the provided lines for any request path.
func newTestArchiveServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	for _, l := range lines {
		fmt.Fprintln(gw, l)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(data)
	}))
}

// --- tests ---

func TestQueueContributorsPriority(t *testing.T) {
	t.Parallel()

	store := newMockIngestStore()
	store.tenantOrgs["tenantorg"] = true
	store.existing["existing-user"] = true

	summaries := []Summary{
		// New user touching tenant repo → P1
		{Username: "new-tenant", Repos: map[string]bool{"tenantorg/repo1": true}},
		// New user, non-tenant repo → P2
		{Username: "new-other", Repos: map[string]bool{"someorg/repo": true}},
		// Existing user touching tenant repo → P3
		{Username: "existing-user", Repos: map[string]bool{"tenantorg/repo2": true}},
		// Existing user, non-tenant repo → skip
		{Username: "existing-other", Repos: map[string]bool{"someorg/repo": true}},
	}

	// Mark existing-other as existing too.
	store.existing["existing-other"] = true

	count := queueContributors(context.Background(), store, summaries, store.tenantOrgs)
	if count != 2 {
		t.Fatalf("queued %d, want 2 (only tenant-related contributors)", count)
	}

	// Verify priorities.
	priorities := make(map[string]int)
	for _, e := range store.enqueued {
		priorities[e.username] = e.priority
	}

	if p := priorities["new-tenant"]; p != 1 {
		t.Errorf("new-tenant priority: got %d, want 1", p)
	}
	if _, ok := priorities["new-other"]; ok {
		t.Error("new-other (non-tenant) should not be enqueued")
	}
	if p := priorities["existing-user"]; p != 3 {
		t.Errorf("existing-user priority: got %d, want 3", p)
	}
	if _, ok := priorities["existing-other"]; ok {
		t.Error("existing-other should not be enqueued")
	}
}

func TestMaybeCompactRunsWhenDue(t *testing.T) {
	t.Parallel()

	store := newMockIngestStore()
	// No prior compaction state → should run.

	maybeCompact(context.Background(), store)

	if !store.compacted {
		t.Fatal("compaction should have run")
	}
	if store.compactAge != compactOlderThan {
		t.Errorf("compact age: got %v, want %v", store.compactAge, compactOlderThan)
	}
	if store.syncStates[compactStateKey].IsZero() {
		t.Error("compaction state should be saved")
	}
	if !store.prunedActivity {
		t.Error("activity pruning should have run")
	}
	if store.pruneActivityAge != pruneActivityRetention {
		t.Errorf("prune activity age: got %v, want %v", store.pruneActivityAge, pruneActivityRetention)
	}
	if !store.prunedHistory {
		t.Error("history pruning should have run")
	}
	if store.pruneHistoryAge != pruneHistoryRetention {
		t.Errorf("prune history age: got %v, want %v", store.pruneHistoryAge, pruneHistoryRetention)
	}
}

func TestMaybeCompactSkipsWhenRecent(t *testing.T) {
	t.Parallel()

	store := newMockIngestStore()
	store.syncStates[compactStateKey] = time.Now().UTC() // just ran

	maybeCompact(context.Background(), store)

	if store.compacted {
		t.Error("compaction should be skipped when recently run")
	}
}

func TestProcessHourIntegration(t *testing.T) {
	t.Parallel()

	// Create a test HTTP server with gzipped NDJSON fixture.
	srv := newTestArchiveServer(t, []string{
		`{"type":"PullRequestEvent","actor":{"login":"alice"},"repo":{"name":"org/repo1"},"payload":{"action":"opened"},"created_at":"2025-08-01T10:00:00Z"}`,
		`{"type":"PullRequestReviewEvent","actor":{"login":"bob"},"repo":{"name":"org/repo2"},"payload":{"action":"submitted"},"created_at":"2025-08-01T10:01:00Z"}`,
		`{"type":"IssueCommentEvent","actor":{"login":"alice"},"repo":{"name":"org/repo1"},"payload":{"action":"created"},"created_at":"2025-08-01T10:02:00Z"}`,
	})
	defer srv.Close()

	store := newMockIngestStore()
	reader := NewArchiveReader(srv.URL)
	hour := time.Date(2025, 8, 1, 10, 0, 0, 0, time.UTC)

	err := processHour(context.Background(), store, reader, hour, nil)
	if err != nil {
		t.Fatalf("processHour: %v", err)
	}

	// Should have 2 contributors (alice, bob).
	if len(store.upserted) != 2 {
		t.Errorf("upserted %d summaries, want 2", len(store.upserted))
	}

	// No tenant repos → nothing enqueued.
	if len(store.enqueued) != 0 {
		t.Errorf("enqueued %d, want 0 (no tenant repos)", len(store.enqueued))
	}
}
