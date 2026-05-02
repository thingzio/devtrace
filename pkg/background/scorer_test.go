package background

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

// --- mock store ---

type mockScorerStore struct {
	mu             sync.Mutex
	queue          []postgres.QueueEntry
	stale          []postgres.StaleContributor
	dequeueErr     error
	staleErr       error
	upserted       []string
	scored         []string
	reputations    []string
	upsertErr      error
	saveHistoryErr error
	updateRepErr   error
	queueDepth     int

	// Grade change tracking
	grades        map[string]string // "user:provider" -> grade
	watchlists    []postgres.WatchlistEntry
	notifications []mockNotif
	bumped        []string
}

type mockNotif struct {
	watchlistID string
	eventType   string
	username    string
	details     map[string]any
}

func (m *mockScorerStore) DequeueForScoring(_ context.Context, limit int) ([]postgres.QueueEntry, error) {
	if m.dequeueErr != nil {
		return nil, m.dequeueErr
	}
	if limit > len(m.queue) {
		limit = len(m.queue)
	}
	// Simulate atomic DELETE ... RETURNING by consuming from queue.
	result := make([]postgres.QueueEntry, limit)
	copy(result, m.queue[:limit])
	m.queue = m.queue[limit:]
	return result, nil
}

func (m *mockScorerStore) GetBehavioralSignals(_ context.Context, _, _ string) (*model.Behavior, error) {
	return nil, nil
}

func (m *mockScorerStore) GetCachedSignals(_ context.Context, _, _ string) (*score.InputSignals, error) {
	return nil, nil
}

func (m *mockScorerStore) GetStaleContributors(_ context.Context, _, _, limit int) ([]postgres.StaleContributor, error) {
	if m.staleErr != nil {
		return nil, m.staleErr
	}
	if limit > len(m.stale) {
		limit = len(m.stale)
	}
	return m.stale[:limit], nil
}

func (m *mockScorerStore) UpsertContributor(_ context.Context, username, _ string) error {
	m.mu.Lock()
	m.upserted = append(m.upserted, username)
	m.mu.Unlock()
	return m.upsertErr
}

func (m *mockScorerStore) SaveScoreHistory(_ context.Context, username, _ string, _ float64, _ string, _ bool) error {
	m.mu.Lock()
	m.scored = append(m.scored, username)
	m.mu.Unlock()
	return m.saveHistoryErr
}

func (m *mockScorerStore) UpdateReputation(_ context.Context, username, _ string, _ float64, _, _ string, _ bool, _ *score.InputSignals) error {
	m.mu.Lock()
	m.reputations = append(m.reputations, username)
	m.mu.Unlock()
	return m.updateRepErr
}

func (m *mockScorerStore) QueueDepth(_ context.Context) (int, error) {
	return m.queueDepth, nil
}

func (m *mockScorerStore) GetCurrentGrade(_ context.Context, username, provider string) (string, error) {
	if m.grades == nil {
		return "", nil
	}
	return m.grades[username+":"+provider], nil
}

func (m *mockScorerStore) GetWatchlistsForContributor(_ context.Context, _, _ string) ([]postgres.WatchlistEntry, error) {
	return m.watchlists, nil
}

func (m *mockScorerStore) InsertNotificationEvent(_ context.Context, watchlistID, eventType, username string, details map[string]any) error {
	m.mu.Lock()
	m.notifications = append(m.notifications, mockNotif{watchlistID, eventType, username, details})
	m.mu.Unlock()
	return nil
}

func (m *mockScorerStore) BumpScoredAt(_ context.Context, username, _ string) error {
	m.mu.Lock()
	m.bumped = append(m.bumped, username)
	m.mu.Unlock()
	return nil
}

// --- mock GitHub client ---

type mockGHClient struct {
	signals *score.InputSignals
	err     error
}

func (m *mockGHClient) FetchSignals(_ context.Context, _, _ string, _ *ghclient.ArchiveHints) (*score.InputSignals, error) {
	return m.signals, m.err
}

func (m *mockGHClient) FetchUser(_ context.Context, _ string) (*ghclient.UserProfile, error) {
	return nil, nil
}

func (m *mockGHClient) IsOrgMember(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (m *mockGHClient) ListUserRepos(_ context.Context, _ string, _ int) ([]ghclient.Repo, error) {
	return nil, nil
}

func (m *mockGHClient) FetchSecurityCredits(_ context.Context, _ string, _ int) ([]ghclient.SecurityAdvisoryCredit, error) {
	return nil, nil
}

const testVersion = "v0.0.1-test"

func TestScorerStatsRecord(t *testing.T) {
	t.Parallel()
	s := &scorerStats{}
	s.record(3, 1, 2)
	if s.totalScored.Load() != 3 {
		t.Errorf("totalScored: got %d, want 3", s.totalScored.Load())
	}
	if s.totalErrors.Load() != 1 {
		t.Errorf("totalErrors: got %d, want 1", s.totalErrors.Load())
	}
	if s.totalWithHints.Load() != 2 {
		t.Errorf("totalWithHints: got %d, want 2", s.totalWithHints.Load())
	}
}

func TestScorerStatsWindow(t *testing.T) {
	t.Parallel()
	s := &scorerStats{}
	s.record(10, 2, 5)
	s.record(5, 1, 3)

	scored, errs, hints := s.window()
	if scored != 15 {
		t.Errorf("window scored: got %d, want 15", scored)
	}
	if errs != 3 {
		t.Errorf("window errors: got %d, want 3", errs)
	}
	if hints != 8 {
		t.Errorf("window hints: got %d, want 8", hints)
	}

	s.resetWindow()
	s.record(2, 0, 1)
	scored, _, _ = s.window()
	if scored != 2 {
		t.Errorf("window after reset: got %d, want 2", scored)
	}
}

func TestConstants(t *testing.T) {
	t.Parallel()
	if defaultBatchSize != 100 {
		t.Errorf("defaultBatchSize: got %d, want 100", defaultBatchSize)
	}
	if defaultMinQuotaPct != 30 {
		t.Errorf("defaultMinQuotaPct: got %d, want 30", defaultMinQuotaPct)
	}
	if defaultLowDays != 7 {
		t.Errorf("defaultLowDays: got %d, want 7", defaultLowDays)
	}
	if defaultHighDays != 30 {
		t.Errorf("defaultHighDays: got %d, want 30", defaultHighDays)
	}
}

func TestScoreContributorSuccess(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{}
	gh := &mockGHClient{signals: &score.InputSignals{
		AgeDays: 500, PRsMerged: 10, Followers: 20, PublicRepos: 5,
	}}

	err := scoreContributor(context.Background(), store, gh, "alice", "github", testVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.upserted) != 1 || store.upserted[0] != "alice" {
		t.Errorf("upserted: %v, want [alice]", store.upserted)
	}
	if len(store.scored) != 1 || store.scored[0] != "alice" {
		t.Errorf("scored: %v, want [alice]", store.scored)
	}
	if len(store.reputations) != 1 || store.reputations[0] != "alice" {
		t.Errorf("reputations: %v, want [alice]", store.reputations)
	}
}

func TestScoreContributorFetchError(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{}
	gh := &mockGHClient{err: errors.New("rate limited")}

	err := scoreContributor(context.Background(), store, gh, "bob", "github", testVersion)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(store.upserted) != 0 {
		t.Error("should not have upserted on fetch error")
	}
}

func TestDrainQueueScoresEntries(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "alice", Provider: "github", Priority: 1},
			{Username: "bob", Provider: "github", Priority: 2},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 365, PRsMerged: 5}}

	scored := drainQueue(context.Background(), store, gh, nil, testVersion, 100, 1)
	if scored != 2 {
		t.Errorf("scored = %d, want 2", scored)
	}
	// Queue should be empty after atomic dequeue.
	if len(store.queue) != 0 {
		t.Errorf("queue length = %d, want 0 after dequeue", len(store.queue))
	}
}

func TestDrainQueueEmptyQueue(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{queue: nil}
	gh := &mockGHClient{}
	scored := drainQueue(context.Background(), store, gh, nil, testVersion, 100, 1)
	if scored != 0 {
		t.Error("expected 0 scored from empty queue")
	}
}

func TestDrainQueueDequeueError(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{dequeueErr: errors.New("db down")}
	gh := &mockGHClient{}
	scored := drainQueue(context.Background(), store, gh, nil, testVersion, 100, 1)
	if scored != 0 {
		t.Error("expected 0 scored on error")
	}
}

func TestDrainQueuePartialFailure(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "alice", Provider: "github", Priority: 1},
			{Username: "bob", Provider: "github", Priority: 2},
		},
	}
	gh := &mockGHClient{err: errors.New("api error")}
	scored := drainQueue(context.Background(), store, gh, nil, testVersion, 100, 1)
	if scored != 0 {
		t.Error("expected 0 scored on fetch error")
	}
}

func TestRescoreStale(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		stale: []postgres.StaleContributor{
			{Username: "stale-user", Provider: "github"},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 100}}
	scored := rescoreStale(context.Background(), store, gh, nil, testVersion, 100, 1)
	if scored != 1 {
		t.Errorf("scored = %d, want 1", scored)
	}
}

func TestDrainThenStale(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "queued-user", Provider: "github", Priority: 1},
		},
		stale: []postgres.StaleContributor{
			{Username: "stale-user", Provider: "github"},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 100}}

	scored := drainQueue(context.Background(), store, gh, nil, testVersion, 100, 1)
	if scored != 1 {
		t.Errorf("queue scored = %d, want 1", scored)
	}
	staleScored := rescoreStale(context.Background(), store, gh, nil, testVersion, 100, 1)
	if staleScored != 1 {
		t.Errorf("stale scored = %d, want 1", staleScored)
	}
}

func TestDrainQueueContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "user1", Provider: "github", Priority: 1},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 100}}
	scored := drainQueue(ctx, store, gh, nil, testVersion, 100, 1)
	if scored != 0 {
		t.Errorf("should not score when canceled, scored %d", scored)
	}
}

func TestSleepCtxCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleepCtx(ctx, 10*time.Second)
	if time.Since(start) > time.Second {
		t.Error("sleepCtx should return immediately on canceled context")
	}
}

func TestDrainQueueConcurrent(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "alice", Provider: "github", Priority: 1},
			{Username: "bob", Provider: "github", Priority: 2},
			{Username: "carol", Provider: "github", Priority: 3},
			{Username: "dave", Provider: "github", Priority: 4},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 365, PRsMerged: 5}}
	stats := &scorerStats{}

	scored := drainQueue(context.Background(), store, gh, stats, testVersion, 100, 3)
	if scored != 4 {
		t.Errorf("scored = %d, want 4", scored)
	}
	if len(store.queue) != 0 {
		t.Errorf("queue length = %d, want 0 after dequeue", len(store.queue))
	}
	if stats.totalScored.Load() != 4 {
		t.Errorf("stats.totalScored = %d, want 4", stats.totalScored.Load())
	}
}

func TestDrainQueueConcurrentPartialFailure(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "alice", Provider: "github", Priority: 1},
			{Username: "bob", Provider: "github", Priority: 2},
		},
	}
	gh := &mockGHClient{err: errors.New("api error")}
	stats := &scorerStats{}

	scored := drainQueue(context.Background(), store, gh, stats, testVersion, 100, 3)
	if scored != 0 {
		t.Error("expected 0 scored on fetch error")
	}
	if stats.totalErrors.Load() != 2 {
		t.Errorf("stats.totalErrors = %d, want 2", stats.totalErrors.Load())
	}
}

func TestRescoreStaleConcurrent(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		stale: []postgres.StaleContributor{
			{Username: "stale1", Provider: "github"},
			{Username: "stale2", Provider: "github"},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 100}}
	stats := &scorerStats{}

	scored := rescoreStale(context.Background(), store, gh, stats, testVersion, 100, 3)
	if scored != 2 {
		t.Errorf("scored = %d, want 2", scored)
	}
	if stats.totalScored.Load() != 2 {
		t.Errorf("stats.totalScored = %d, want 2", stats.totalScored.Load())
	}
}

func ghError(statusCode int) error {
	return &gh.ErrorResponse{
		Response: &http.Response{StatusCode: statusCode},
	}
}

func TestIsTerminalError404(t *testing.T) {
	t.Parallel()
	if !isTerminalError(fmt.Errorf("fetch: %w", ghError(http.StatusNotFound))) {
		t.Error("404 should be terminal")
	}
}

func TestIsTerminalError451(t *testing.T) {
	t.Parallel()
	if !isTerminalError(fmt.Errorf("fetch: %w", ghError(http.StatusUnavailableForLegalReasons))) {
		t.Error("451 should be terminal")
	}
}

func TestIsTerminalError422(t *testing.T) {
	t.Parallel()
	if !isTerminalError(fmt.Errorf("fetch: %w", ghError(http.StatusUnprocessableEntity))) {
		t.Error("422 should be terminal")
	}
}

func TestIsTerminalErrorRateLimit(t *testing.T) {
	t.Parallel()
	if isTerminalError(fmt.Errorf("fetch: %w", ghError(http.StatusForbidden))) {
		t.Error("403 rate limit should not be terminal")
	}
}

func TestIsTerminalErrorGeneric(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("connection timeout")
	if isTerminalError(err) {
		t.Error("generic error should not be terminal")
	}
}

func TestIsTerminalErrorNil(t *testing.T) {
	t.Parallel()
	if isTerminalError(nil) {
		t.Error("nil should not be terminal")
	}
}

func TestDrainQueueTerminalErrorSkipped(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "deleted-user", Provider: "github", Priority: 2},
			{Username: "good-user", Provider: "github", Priority: 2},
		},
	}
	// Client returns 404 for all users — simulates deleted accounts.
	gh := &mockGHClient{err: fmt.Errorf("fetch: %w", ghError(http.StatusNotFound))}
	stats := &scorerStats{}

	scored := drainQueue(context.Background(), store, gh, stats, testVersion, 100, 1)
	if scored != 0 {
		t.Errorf("scored = %d, want 0 (all 404)", scored)
	}
	// Terminal errors are skipped, not counted as retryable errors.
	if stats.totalErrors.Load() != 0 {
		t.Errorf("totalErrors = %d, want 0", stats.totalErrors.Load())
	}
	// Queue should be empty after atomic dequeue (entries already removed).
	if len(store.queue) != 0 {
		t.Errorf("queue length = %d, want 0", len(store.queue))
	}
}

func TestRescoreStaleTerminalTombstoned(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		stale: []postgres.StaleContributor{
			{Username: "deleted-user", Provider: "github"},
			{Username: "good-user", Provider: "github"},
		},
	}
	// Client returns 404 for all users.
	gh := &mockGHClient{err: fmt.Errorf("fetch: %w", ghError(http.StatusNotFound))}
	stats := &scorerStats{}

	scored := rescoreStale(context.Background(), store, gh, stats, testVersion, 100, 1)
	if scored != 0 {
		t.Errorf("scored = %d, want 0 (all terminal)", scored)
	}
	if len(store.bumped) != 2 {
		t.Errorf("bumped = %d, want 2", len(store.bumped))
	}
	if stats.totalErrors.Load() != 0 {
		t.Errorf("totalErrors = %d, want 0 (terminal errors are not retryable)", stats.totalErrors.Load())
	}
}

func TestRescoreStalePartialTerminal(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		stale: []postgres.StaleContributor{
			{Username: "deleted-user", Provider: "github"},
		},
	}
	gh := &mockGHClient{err: fmt.Errorf("fetch: %w", ghError(http.StatusNotFound))}
	stats := &scorerStats{}

	scored := rescoreStale(context.Background(), store, gh, stats, testVersion, 100, 1)
	if scored != 0 {
		t.Errorf("scored = %d, want 0", scored)
	}
	if len(store.bumped) != 1 {
		t.Errorf("bumped = %v, want [deleted-user]", store.bumped)
	}
	if store.bumped[0] != "deleted-user" {
		t.Errorf("bumped[0] = %q, want %q", store.bumped[0], "deleted-user")
	}
}

func TestScoreContributorGradeChange(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		grades: map[string]string{"alice:github": "D"},
		watchlists: []postgres.WatchlistEntry{
			{ID: "wl-1", TenantID: "t-1", Target: "org", Plan: "starter"},
		},
	}
	// Signals that produce a decent score (should be above D).
	gh := &mockGHClient{signals: &score.InputSignals{
		AgeDays: 1000, PRsMerged: 50, Followers: 100, PublicRepos: 20,
	}}

	err := scoreContributor(context.Background(), store, gh, "alice", "github", testVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have recorded a notification event for the grade change.
	if len(store.notifications) == 0 {
		t.Fatal("expected at least 1 notification for grade change")
	}
	n := store.notifications[0]
	if n.eventType != "score_change" {
		t.Errorf("eventType = %q, want %q", n.eventType, "score_change")
	}
	if n.watchlistID != "wl-1" {
		t.Errorf("watchlistID = %q, want %q", n.watchlistID, "wl-1")
	}
	if n.details["old_grade"] != "D" {
		t.Errorf("old_grade = %v, want D", n.details["old_grade"])
	}
}

func TestScoreContributorNoGradeChange(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		// No previous grade → no change event expected.
		grades: map[string]string{},
	}
	gh := &mockGHClient{signals: &score.InputSignals{
		AgeDays: 500, PRsMerged: 10, Followers: 20, PublicRepos: 5,
	}}

	err := scoreContributor(context.Background(), store, gh, "bob", "github", testVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.notifications) != 0 {
		t.Errorf("got %d notifications, want 0 (no previous grade)", len(store.notifications))
	}
}

func TestNotifyGradeChange(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		watchlists: []postgres.WatchlistEntry{
			{ID: "wl-1", TenantID: "t-1", Target: "org1"},
			{ID: "wl-2", TenantID: "t-2", Target: "org2"},
		},
	}

	notifyGradeChange(context.Background(), store, "alice", "github", "C", "B")

	if len(store.notifications) != 2 {
		t.Fatalf("got %d notifications, want 2", len(store.notifications))
	}
	for _, n := range store.notifications {
		if n.eventType != "score_change" {
			t.Errorf("eventType = %q, want score_change", n.eventType)
		}
		if n.details["old_grade"] != "C" {
			t.Errorf("old_grade = %v, want C", n.details["old_grade"])
		}
		if n.details["new_grade"] != "B" {
			t.Errorf("new_grade = %v, want B", n.details["new_grade"])
		}
	}
}

func TestNotifyGradeChangeNoWatchlists(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		watchlists: nil,
	}

	// Should not panic or error with no matching watchlists.
	notifyGradeChange(context.Background(), store, "alice", "github", "D", "C")

	if len(store.notifications) != 0 {
		t.Errorf("got %d notifications, want 0", len(store.notifications))
	}
}

func TestScoreContributorUpdateReputationError(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		updateRepErr: errors.New("db unavailable"),
	}
	gh := &mockGHClient{signals: &score.InputSignals{
		AgeDays: 500, PRsMerged: 10, Followers: 20, PublicRepos: 5,
	}}

	err := scoreContributor(context.Background(), store, gh, "alice", "github", testVersion)
	if err == nil {
		t.Fatal("expected error from UpdateReputation")
	}
	if !strings.Contains(err.Error(), "update reputation") {
		t.Errorf("error should contain 'update reputation' context, got: %v", err)
	}
	// Original error should be wrapped.
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("error should contain wrapped cause, got: %v", err)
	}
}

func TestSleepCtxNormalExpiry(t *testing.T) {
	t.Parallel()
	start := time.Now()
	sleepCtx(context.Background(), 50*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Errorf("sleepCtx returned too early: %v", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("sleepCtx took too long: %v", elapsed)
	}
}

func TestJitterRange(t *testing.T) {
	t.Parallel()
	for range 100 {
		d := jitter()
		if d < 0 || d >= resetJitter {
			t.Errorf("jitter() = %v, want [0, %v)", d, resetJitter)
		}
	}
}
