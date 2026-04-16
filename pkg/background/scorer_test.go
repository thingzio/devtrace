package background

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

// --- mock store ---

type mockScorerStore struct {
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
	m.removed = append(m.removed, username)
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
	m.upserted = append(m.upserted, username)
	return m.upsertErr
}

func (m *mockScorerStore) SaveScoreHistory(_ context.Context, username, _ string, _ float64, _ string, _ bool) error {
	m.scored = append(m.scored, username)
	return m.saveHistoryErr
}

func (m *mockScorerStore) UpdateReputation(_ context.Context, username, _ string, _ float64, _, _ string, _ *score.InputSignals) error {
	m.reputations = append(m.reputations, username)
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

	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
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
	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
	if scored != 0 {
		t.Error("expected 0 scored from empty queue")
	}
}

func TestDrainQueueDequeueError(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{dequeueErr: errors.New("db down")}
	gh := &mockGHClient{}
	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
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
	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
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
	scored := rescoreStale(context.Background(), store, gh, testVersion, 100)
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

	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
	if scored != 1 {
		t.Errorf("queue scored = %d, want 1", scored)
	}
	staleScored := rescoreStale(context.Background(), store, gh, testVersion, 100)
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
	scored := drainQueue(ctx, store, gh, testVersion, 100)
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
