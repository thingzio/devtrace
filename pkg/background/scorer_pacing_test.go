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

package background

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/score"
)

// countingGHClient records how many contributors a batch actually attempted.
type countingGHClient struct {
	mockGHClient
	calls atomic.Int32
	err   error
}

func (c *countingGHClient) FetchSignals(_ context.Context, _, _ string, _ *ghclient.ArchiveHints) (*score.InputSignals, error) {
	c.calls.Add(1)
	if c.err != nil {
		return nil, c.err
	}
	return &score.InputSignals{}, nil
}

func queueOf(n int) []postgres.QueueEntry {
	q := make([]postgres.QueueEntry, n)
	for i := range q {
		q[i] = postgres.QueueEntry{Username: fmt.Sprintf("u%d", i), Provider: "github"}
	}
	return q
}

func staleOf(n int) []postgres.StaleContributor {
	s := make([]postgres.StaleContributor, n)
	for i := range s {
		s[i] = postgres.StaleContributor{Username: fmt.Sprintf("u%d", i), Provider: "github"}
	}
	return s
}

// A dry pool fails every contributor identically. Marching the whole batch
// through it burns nothing but log volume and keeps the queue hot, so the
// batch must stop as soon as the pool reports empty.
func TestDrainQueueStopsWhenPoolDry(t *testing.T) {
	store := &mockScorerStore{queue: queueOf(50)}
	ghc := &countingGHClient{err: ghclient.ErrNoTokens}

	scored := drainQueue(context.Background(), store, ghc, &scorerStats{}, testVersion, 50, 3)

	if scored != 0 {
		t.Errorf("scored: got %d, want 0", scored)
	}
	if got := ghc.calls.Load(); got > 10 {
		t.Errorf("attempted %d of 50 contributors against a dry pool; want the batch to abort early", got)
	}
}

// DequeueForScoring consumes rows (DELETE ... RETURNING), so aborting a batch
// must put the unscored work back rather than silently dropping it.
func TestDrainQueueRequeuesUnscoredWhenPoolDry(t *testing.T) {
	store := &mockScorerStore{queue: queueOf(50)}
	ghc := &countingGHClient{err: ghclient.ErrNoTokens}

	drainQueue(context.Background(), store, ghc, &scorerStats{}, testVersion, 50, 3)

	store.mu.Lock()
	requeued := len(store.requeued)
	store.mu.Unlock()
	if requeued != 50 {
		t.Errorf("requeued %d of 50 dequeued contributors; want all unscored work returned to the queue", requeued)
	}
}

func TestRescoreStaleStopsWhenPoolDry(t *testing.T) {
	store := &mockScorerStore{stale: staleOf(50)}
	ghc := &countingGHClient{err: ghclient.ErrNoTokens}

	scored := rescoreStale(context.Background(), store, ghc, &scorerStats{}, testVersion, 50, 3)

	if scored != 0 {
		t.Errorf("scored: got %d, want 0", scored)
	}
	if got := ghc.calls.Load(); got > 10 {
		t.Errorf("attempted %d of 50 contributors against a dry pool; want the batch to abort early", got)
	}
}

// A healthy pool must not trip the abort path.
func TestDrainQueueCompletesWhenPoolHealthy(t *testing.T) {
	store := &mockScorerStore{queue: queueOf(10)}
	ghc := &countingGHClient{}

	scored := drainQueue(context.Background(), store, ghc, &scorerStats{}, testVersion, 10, 3)

	if scored != 10 {
		t.Errorf("scored: got %d, want 10", scored)
	}
	if got := ghc.calls.Load(); got != 10 {
		t.Errorf("attempted: got %d, want 10", got)
	}
}

// --- loop pacing ---

type fakePool struct {
	active   int
	size     int
	earliest time.Time
}

func (f *fakePool) ActiveCount() int         { return f.active }
func (f *fakePool) Size() int                { return f.size }
func (f *fakePool) EarliestReset() time.Time { return f.earliest }

// With the pool at its reserve, the loop must wait for the rate-limit window
// to reset instead of dequeuing work it cannot score. Dequeuing is
// destructive, so spinning here is what kept the queue churning.
func TestScorerWaitsInsteadOfDequeuingWhenPoolAtReserve(t *testing.T) {
	store := &mockScorerStore{queue: queueOf(50)}
	ghc := &countingGHClient{}
	pool := &fakePool{active: 0, size: 7, earliest: time.Now().Add(time.Hour)}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	runContinuousScorer(ctx, store, ghc, nil, pool, testVersion, 50, 30, 3, 1)

	store.mu.Lock()
	calls := store.dequeueCalls
	store.mu.Unlock()
	if calls != 0 {
		t.Errorf("dequeued %d batches while the pool was at reserve; want 0", calls)
	}
	if got := ghc.calls.Load(); got != 0 {
		t.Errorf("scored %d contributors while the pool was at reserve; want 0", got)
	}
}

// --- pacing decision ---

func TestTokenPauseSkippedWhenHeadroomRemains(t *testing.T) {
	t.Parallel()
	now := time.Now()

	wait, pause := tokenPause(5, 1, time.Time{}, now)

	if pause {
		t.Errorf("pause: got true (wait %v), want false with 5 active and reserve 1", wait)
	}
}

// Background work yields the last tokens to interactive traffic, so the
// reserve threshold pauses before the pool is fully drained.
func TestTokenPauseHoldsReserveForInteractive(t *testing.T) {
	t.Parallel()
	now := time.Now()
	reset := now.Add(20 * time.Second)

	wait, pause := tokenPause(1, 1, reset, now)

	if !pause {
		t.Fatal("pause: got false, want true when active equals the reserve")
	}
	if wait != 20*time.Second {
		t.Errorf("wait: got %v, want 20s (until the pool recovers)", wait)
	}
}

func TestTokenPauseFallsBackWhenResetUnknown(t *testing.T) {
	t.Parallel()
	now := time.Now()

	wait, pause := tokenPause(0, 1, time.Time{}, now)

	if !pause {
		t.Fatal("pause: got false, want true with a dry pool")
	}
	if wait != tokenPauseFallback {
		t.Errorf("wait: got %v, want %v", wait, tokenPauseFallback)
	}
}

// A reset already in the past must not produce a zero/negative sleep, which
// would spin the loop at full speed.
func TestTokenPauseNeverReturnsNonPositive(t *testing.T) {
	t.Parallel()
	now := time.Now()

	wait, pause := tokenPause(0, 1, now.Add(-time.Minute), now)

	if !pause {
		t.Fatal("pause: got false, want true with a dry pool")
	}
	if wait <= 0 {
		t.Errorf("wait: got %v, want positive", wait)
	}
}
