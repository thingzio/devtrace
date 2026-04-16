# Scorer Observability + Concurrent Batches Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add throughput metrics logging and concurrent batch processing to the background scorer.

**Architecture:** Add a `scorerStats` counter struct that tracks scored/errors/hints per window. Add `QueueDepth` to the `scorerStore` interface. Change `drainQueue` and `rescoreStale` from sequential to concurrent using `sync.WaitGroup` + channel semaphore. Log periodic stats every 5 minutes.

**Tech Stack:** Go stdlib only (`sync`, `sync/atomic`, `time`, `log/slog`). No new dependencies.

---

### Task 1: Add QueueDepth to scorerStore interface and mock

**Files:**
- Modify: `pkg/background/scorer.go`
- Modify: `pkg/background/scorer_test.go`

**Step 1: Add QueueDepth to the scorerStore interface**

In `pkg/background/scorer.go`, add to the `scorerStore` interface (after `UpdateReputation`):

```go
QueueDepth(ctx context.Context) (int, error)
```

**Step 2: Add QueueDepth to the mock**

In `pkg/background/scorer_test.go`, add a field and method to `mockScorerStore`:

```go
// Add field to struct:
queueDepth int

// Add method:
func (m *mockScorerStore) QueueDepth(_ context.Context) (int, error) {
	return m.queueDepth, nil
}
```

**Step 3: Verify tests pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/background/ -v -race`
Expected: ALL PASS

**Step 4: Commit**

```bash
git add pkg/background/scorer.go pkg/background/scorer_test.go
git commit -S -m "Add QueueDepth to scorerStore interface"
```

---

### Task 2: Add scorerStats counter and periodic logging

**Files:**
- Modify: `pkg/background/scorer.go`
- Modify: `pkg/background/scorer_test.go`

**Step 1: Write failing test for scorerStats**

Add to `pkg/background/scorer_test.go`:

```go
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

	// Reset and verify window returns delta
	s.resetWindow()
	s.record(2, 0, 1)
	scored, _, _ = s.window()
	if scored != 2 {
		t.Errorf("window after reset: got %d, want 2", scored)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/background/ -run "TestScorerStats" -v`
Expected: FAIL — `scorerStats` not defined

**Step 3: Implement scorerStats**

Add to `pkg/background/scorer.go` (after the constants block):

```go
// scorerStats tracks scoring throughput for periodic logging.
type scorerStats struct {
	totalScored    atomic.Int64
	totalErrors    atomic.Int64
	totalWithHints atomic.Int64
	// Window tracking for rate calculation.
	windowScored atomic.Int64
	windowErrors atomic.Int64
	windowHints  atomic.Int64
}

func (s *scorerStats) record(scored, errors, withHints int) {
	s.totalScored.Add(int64(scored))
	s.totalErrors.Add(int64(errors))
	s.totalWithHints.Add(int64(withHints))
	s.windowScored.Add(int64(scored))
	s.windowErrors.Add(int64(errors))
	s.windowHints.Add(int64(withHints))
}

func (s *scorerStats) window() (scored, errors, hints int64) {
	return s.windowScored.Load(), s.windowErrors.Load(), s.windowHints.Load()
}

func (s *scorerStats) resetWindow() {
	s.windowScored.Store(0)
	s.windowErrors.Store(0)
	s.windowHints.Store(0)
}
```

Add `"sync/atomic"` to the imports.

**Step 4: Run tests to verify they pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/background/ -run "TestScorerStats" -v -race`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/background/scorer.go pkg/background/scorer_test.go
git commit -S -m "Add scorerStats counter for throughput tracking"
```

---

### Task 3: Wire stats into scoring loop with periodic logging

**Files:**
- Modify: `pkg/background/scorer.go`

**Step 1: Add stats and periodic logging to runContinuousScorer**

Update `runContinuousScorer` signature to accept and use stats. Add a stats logging ticker.

The full updated function:

```go
func runContinuousScorer(ctx context.Context, store scorerStore, gh ghclient.Client,
	qc quotaChecker, version string, batchSize, minQuotaPct, concurrency int) {

	stats := &scorerStats{}
	statsTicker := time.NewTicker(5 * time.Minute)
	defer statsTicker.Stop()

	for {
		if ctx.Err() != nil {
			slog.Info("continuous scorer stopped",
				"total_scored", stats.totalScored.Load(),
				"total_errors", stats.totalErrors.Load(),
			)
			return
		}

		// Emit periodic stats.
		select {
		case <-statsTicker.C:
			logScorerStats(ctx, store, stats)
		default:
		}

		// Check quota before each batch.
		if qc != nil {
			quotas := qc.CheckQuotas(ctx)
			pct, earliestReset := ghclient.AggregateQuota(quotas)
			if pct < minQuotaPct {
				logScorerStats(ctx, store, stats)
				wait := max(time.Until(earliestReset)+jitter(), time.Minute)
				slog.Warn("scorer pausing: quota below threshold",
					"aggregate_pct", pct,
					"threshold_pct", minQuotaPct,
					"resume_in", wait,
				)
				sleepCtx(ctx, wait)
				continue
			}
		}

		// Phase 1: Drain scoring queue.
		scored := drainQueue(ctx, store, gh, stats, version, batchSize, concurrency)

		// Phase 2: Rescore stale contributors if queue was empty.
		if scored == 0 {
			staleScored := rescoreStale(ctx, store, gh, stats, version, batchSize, concurrency)
			if staleScored == 0 {
				sleepCtx(ctx, emptyQueueSleep)
			}
		}
	}
}

func logScorerStats(ctx context.Context, store scorerStore, stats *scorerStats) {
	windowScored, windowErrors, windowHints := stats.window()
	stats.resetWindow()

	depth := -1
	if d, err := store.QueueDepth(ctx); err == nil {
		depth = d
	}

	slog.Info("scorer stats",
		"total_scored", stats.totalScored.Load(),
		"window_scored", windowScored,
		"window_errors", windowErrors,
		"window_with_hints", windowHints,
		"queue_depth", depth,
	)
}
```

**Step 2: Update StartBackgroundScorer to read SCORER_CONCURRENCY**

```go
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client, version string) func() {
	batchSize := config.GetEnvAsInt("SCORER_BATCH_SIZE", defaultBatchSize)
	minQuotaPct := config.GetEnvAsInt("SCORER_MIN_QUOTA_PCT", defaultMinQuotaPct)
	concurrency := config.GetEnvAsInt("SCORER_CONCURRENCY", defaultConcurrency)

	slog.Info("starting continuous scorer",
		"batch_size", batchSize,
		"min_quota_pct", minQuotaPct,
		"concurrency", concurrency,
	)

	ctx, cancel := context.WithCancel(ctx)

	var qc quotaChecker
	if pc, ok := gh.(*ghclient.PoolClient); ok {
		qc = pc.Pool()
	}

	go runContinuousScorer(ctx, store, gh, qc, version, batchSize, minQuotaPct, concurrency)

	return cancel
}
```

Add constant:

```go
defaultConcurrency = 3
```

**Step 3: Verify compilation**

Run: `GOFLAGS="-mod=vendor" go build ./cmd/devtrace-site/`
Expected: FAIL — `drainQueue` and `rescoreStale` signatures changed. That's expected, we fix them in Task 4.

Note: This task intentionally breaks compilation. Task 4 immediately follows to update `drainQueue` and `rescoreStale`.

**Step 4: Do NOT commit yet — proceed to Task 4**

---

### Task 4: Make drainQueue and rescoreStale concurrent with stats

**Files:**
- Modify: `pkg/background/scorer.go`
- Modify: `pkg/background/scorer_test.go`

**Step 1: Write tests for concurrent drainQueue**

Add to `pkg/background/scorer_test.go`:

```go
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
```

**Step 2: Update drainQueue to be concurrent with stats**

Replace the existing `drainQueue` function:

```go
func drainQueue(ctx context.Context, store scorerStore, gh ghclient.Client,
	stats *scorerStats, version string, batchSize, concurrency int) int {
	queued, err := store.DequeueForScoring(ctx, batchSize)
	if err != nil {
		slog.Error("dequeue for scoring", "error", err)
		return 0
	}
	if len(queued) == 0 {
		return 0
	}

	start := time.Now()
	var scored, errors, hints atomic.Int32
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, q := range queued {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{} // acquire
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release

			hasHints := false
			if beh, err := store.GetBehavioralSignals(ctx, q.Username, q.Provider); err == nil && beh != nil {
				hasHints = true
			}

			if err := scoreContributor(ctx, store, gh, q.Username, q.Provider, version); err != nil {
				slog.Warn("score queued", "username", q.Username, "priority", q.Priority, "error", err)
				errors.Add(1)
				return
			}
			_ = store.RemoveFromQueue(ctx, q.Username, q.Provider)
			scored.Add(1)
			if hasHints {
				hints.Add(1)
			}
		}()
	}
	wg.Wait()

	s, e, h := int(scored.Load()), int(errors.Load()), int(hints.Load())
	if stats != nil {
		stats.record(s, e, h)
	}
	if s > 0 {
		slog.Info("queue scoring complete",
			"scored", s,
			"errors", e,
			"with_hints", h,
			"total", len(queued),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
	}
	return s
}
```

**Step 3: Update rescoreStale similarly**

```go
func rescoreStale(ctx context.Context, store scorerStore, gh ghclient.Client,
	stats *scorerStats, version string, batchSize, concurrency int) int {
	lowDays := config.GetEnvAsInt("SCORER_LOW_STALE_DAYS", defaultLowDays)
	highDays := config.GetEnvAsInt("SCORER_HIGH_STALE_DAYS", defaultHighDays)

	stale, err := store.GetStaleContributors(ctx, lowDays, highDays, batchSize)
	if err != nil {
		slog.Error("fetch stale contributors", "error", err)
		return 0
	}
	if len(stale) == 0 {
		return 0
	}

	start := time.Now()
	var scored, errors atomic.Int32
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, c := range stale {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{} // acquire
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release

			if err := scoreContributor(ctx, store, gh, c.Username, c.Provider, version); err != nil {
				slog.Warn("score stale", "username", c.Username, "error", err)
				errors.Add(1)
				return
			}
			scored.Add(1)
		}()
	}
	wg.Wait()

	s, e := int(scored.Load()), int(errors.Load())
	if stats != nil {
		stats.record(s, e, 0)
	}
	slog.Info("stale rescoring complete",
		"scored", s,
		"errors", e,
		"total", len(stale),
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
	return s
}
```

**Step 4: Update existing test calls to match new signatures**

All existing calls to `drainQueue` and `rescoreStale` in tests need the new `stats` and `concurrency` params. Update each call:

- `drainQueue(ctx, store, gh, testVersion, 100)` → `drainQueue(ctx, store, gh, nil, testVersion, 100, 1)`
- `rescoreStale(ctx, store, gh, testVersion, 100)` → `rescoreStale(ctx, store, gh, nil, testVersion, 100, 1)`

Passing `nil` stats and concurrency=1 preserves existing test behavior.

**Step 5: Add `"sync"` and `"sync/atomic"` to imports if not already present**

**Step 6: Verify all tests pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/background/ -v -race -count=1`
Expected: ALL PASS

**Step 7: Verify full compilation**

Run: `GOFLAGS="-mod=vendor" go build ./cmd/devtrace-site/`
Expected: SUCCESS

**Step 8: Commit Tasks 3 + 4 together**

```bash
git add pkg/background/scorer.go pkg/background/scorer_test.go
git commit -S -m "Add concurrent batch scoring with throughput metrics logging"
```

---

### Task 5: Run full qualification

**Step 1: Run make qualify**

Run: `make qualify`
Expected: ALL PASS

**Step 2: Fix any lint/test issues**

**Step 3: Commit fixes if needed**

```bash
git add -A
git commit -S -m "Fix lint/test issues from scorer concurrency"
```
