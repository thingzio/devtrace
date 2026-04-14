package background

import (
	"context"
	"errors"
	"testing"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/score"
)

func TestScorerConstants(t *testing.T) {
	t.Parallel()

	if defaultScorerSec != 3600 {
		t.Errorf("defaultScorerSec: got %d, want 3600", defaultScorerSec)
	}
	if scorerBatchSize != 100 {
		t.Errorf("scorerBatchSize: got %d, want 100", scorerBatchSize)
	}
	if defaultLowDays != 7 {
		t.Errorf("defaultLowDays: got %d, want 7", defaultLowDays)
	}
	if defaultHighDays != 30 {
		t.Errorf("defaultHighDays: got %d, want 30", defaultHighDays)
	}
}

// --- mock store ---

type mockScorerStore struct {
	queue          []postgres.QueueEntry
	stale          []postgres.StaleContributor
	dequeueErr     error
	staleErr       error
	removed        []string // track removed usernames
	upserted       []string
	scored         []string
	reputations    []string
	upsertErr      error
	saveHistoryErr error
	updateRepErr   error
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

const testVersion = "v0.0.1-test"

func TestScoreContributorSuccess(t *testing.T) {
	t.Parallel()

	store := &mockScorerStore{}
	gh := &mockGHClient{signals: &score.InputSignals{
		AgeDays:     500,
		PRsMerged:   10,
		Followers:   20,
		PublicRepos: 5,
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

	drainQueue(context.Background(), store, gh, testVersion)

	if len(store.removed) != 2 {
		t.Errorf("removed %d, want 2", len(store.removed))
	}
	if len(store.reputations) != 2 {
		t.Errorf("reputations %d, want 2", len(store.reputations))
	}
}

func TestDrainQueueEmptyQueue(t *testing.T) {
	t.Parallel()

	store := &mockScorerStore{queue: nil}
	gh := &mockGHClient{}

	// Should be a no-op, no panic.
	drainQueue(context.Background(), store, gh, testVersion)

	if len(store.removed) != 0 {
		t.Error("should not remove anything from empty queue")
	}
}

func TestDrainQueueDequeueError(t *testing.T) {
	t.Parallel()

	store := &mockScorerStore{dequeueErr: errors.New("db down")}
	gh := &mockGHClient{}

	// Should not panic on dequeue error.
	drainQueue(context.Background(), store, gh, testVersion)
}

func TestDrainQueuePartialFailure(t *testing.T) {
	t.Parallel()

	store := &mockScorerStore{
		queue: []postgres.QueueEntry{
			{Username: "alice", Provider: "github", Priority: 1},
			{Username: "bob", Provider: "github", Priority: 2},
		},
	}
	// FetchSignals returns error — both will fail.
	gh := &mockGHClient{err: errors.New("api error")}

	drainQueue(context.Background(), store, gh, testVersion)

	if len(store.removed) != 0 {
		t.Error("failed scores should not be removed from queue")
	}
}

func TestRunScorerDrainsQueueThenStale(t *testing.T) {
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

	runScorer(context.Background(), store, gh, testVersion)

	// Both queued and stale should be scored.
	if len(store.reputations) != 2 {
		t.Errorf("reputations: got %d, want 2", len(store.reputations))
	}
	// Queued user should be removed from queue.
	if len(store.removed) != 1 || store.removed[0] != "queued-user" {
		t.Errorf("removed: %v, want [queued-user]", store.removed)
	}
}

func TestRunScorerContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	store := &mockScorerStore{
		stale: []postgres.StaleContributor{
			{Username: "user1", Provider: "github"},
			{Username: "user2", Provider: "github"},
		},
	}
	gh := &mockGHClient{signals: &score.InputSignals{AgeDays: 100}}

	runScorer(ctx, store, gh, testVersion)

	// Should bail early, not score stale contributors.
	if len(store.reputations) > 0 {
		t.Errorf("should not score when context is canceled, scored %d", len(store.reputations))
	}
}
