package background

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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
	removed        []string
	upserted       []string
	scored         []string
	reputations    []string
	upsertErr      error
	saveHistoryErr error
	updateRepErr   error
	queueDepth     int
}

func (m *mockScorerStore) DequeueForScoring(_ context.Context, limit int) ([]postgres.QueueEntry, error) {
	if m.dequeueErr != nil {
		return nil, m.dequeueErr
	}
	if limit > len(m.queue) {
		limit = len(m.queue)
	}
	return m.queue[:limit], nil
}

func (m *mockScorerStore) RemoveFromQueue(_ context.Context, username, _ string) error {
	m.mu.Lock()
	m.removed = append(m.removed, username)
	m.mu.Unlock()
	return nil
}

func (m *mockScorerStore) GetBehavioralSignals(_ context.Context, _, _ string) (*model.Behavior, error) {
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

func (m *mockScorerStore) UpdateReputation(_ context.Context, username, _ string, _ float64, _, _ string, _ *score.InputSignals) error {
	m.mu.Lock()
	m.reputations = append(m.reputations, username)
	m.mu.Unlock()
	return m.updateRepErr
}

func (m *mockScorerStore) QueueDepth(_ context.Context) (int, error) {
	return m.queueDepth, nil
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

func TestDrainQueueScoresAndRemoves(t *testing.T) {
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
	if len(store.removed) != 2 {
		t.Errorf("removed %d, want 2", len(store.removed))
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
	if len(store.removed) != 0 {
		t.Error("failed scores should not be removed from queue")
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
	if len(store.removed) != 4 {
		t.Errorf("removed %d, want 4", len(store.removed))
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

func TestIsTerminalError404(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("fetch signals: fetch user foo: GET https://api.github.com/users/foo: 404 Not Found []")
	if !isTerminalError(err) {
		t.Error("404 should be terminal")
	}
}

func TestIsTerminalError451(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("fetch signals: 451 Unavailable For Legal Reasons")
	if !isTerminalError(err) {
		t.Error("451 should be terminal")
	}
}

func TestIsTerminalErrorRateLimit(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("all tokens exhausted: rate limit exceeded")
	if isTerminalError(err) {
		t.Error("rate limit should not be terminal")
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

func TestDrainQueueTerminalErrorRemoves(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "deleted-user", Provider: "github", Priority: 2},
			{Username: "good-user", Provider: "github", Priority: 2},
		},
	}
	// Client returns 404 for all users — simulates deleted accounts.
	gh := &mockGHClient{err: fmt.Errorf("fetch user deleted-user: GET https://api.github.com/users/deleted-user: 404 Not Found []")}
	stats := &scorerStats{}

	scored := drainQueue(context.Background(), store, gh, stats, testVersion, 100, 1)
	if scored != 0 {
		t.Errorf("scored = %d, want 0 (all 404)", scored)
	}
	// Both should be removed from queue (terminal error).
	if len(store.removed) != 2 {
		t.Errorf("removed = %d, want 2 (terminal errors should be removed)", len(store.removed))
	}
	// Errors stat should be 0 (terminal errors are skipped, not counted as retryable errors).
	if stats.totalErrors.Load() != 0 {
		t.Errorf("totalErrors = %d, want 0", stats.totalErrors.Load())
	}
}
