# Continuous Scorer Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace timer-based background scorer with a continuous loop that pauses when aggregate GitHub API token quota drops below 30%.

**Architecture:** Add quota checking to `TokenPool` via the free `GET /rate_limit` API. Rewrite scorer loop to run continuously, checking aggregate quota before each batch. Admin dashboard shows live per-token quota data.

**Tech Stack:** Go stdlib, `net/http` for rate limit API, existing `TokenPool`, existing scorer infrastructure.

**Design doc:** `docs/plans/2026-04-16-continuous-scorer-design.md`

---

### Task 1: TokenPool Quota Checking

**Files:**
- Modify: `pkg/github/tokenpool.go`
- Modify: `pkg/github/tokenpool_test.go`
- Modify: `pkg/net/client.go`

**Step 1: Add quota check HTTP client to pkg/net/client.go**

Read `pkg/net/client.go` first. Add a simple client for quota checks (no redirect restriction):

```go
// QuotaCheckClient is a lightweight HTTP client for GitHub rate limit checks.
var QuotaCheckClient = &http.Client{Timeout: 5 * time.Second}
```

**Step 2: Write failing tests in pkg/github/tokenpool_test.go**

Read existing tests first. Add:

```go
func TestAggregateQuota(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		quotas     []TokenQuota
		wantPct    int
		wantZero   bool // earliestReset.IsZero()
	}{
		{"empty", nil, 100, true},
		{"full_quota", []TokenQuota{
			{Limit: 5000, Remaining: 5000},
		}, 100, false},
		{"half_used", []TokenQuota{
			{Limit: 5000, Remaining: 2500},
			{Limit: 5000, Remaining: 2500},
		}, 50, false},
		{"all_exhausted", []TokenQuota{
			{Limit: 5000, Remaining: 0},
		}, 0, false},
		{"mixed", []TokenQuota{
			{Limit: 5000, Remaining: 1000},
			{Limit: 10000, Remaining: 8000},
		}, 60, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pct, reset := AggregateQuota(tt.quotas)
			if pct != tt.wantPct {
				t.Errorf("pct = %d, want %d", pct, tt.wantPct)
			}
			if tt.wantZero && !reset.IsZero() {
				t.Error("expected zero reset time")
			}
		})
	}
}
```

**Step 3: Run test to verify it fails**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run TestAggregateQuota -v`
Expected: FAIL

**Step 4: Write implementation in pkg/github/tokenpool.go**

Add to `pkg/github/tokenpool.go`:

```go
// TokenQuota holds rate limit info for a single token.
type TokenQuota struct {
	Index     int
	Limit     int
	Remaining int
	Reset     time.Time
	Error     string
}

// CheckQuotas calls the GitHub rate_limit API for each token in the pool.
// This endpoint is free (does not count against quota).
func (p *TokenPool) CheckQuotas(ctx context.Context) []TokenQuota {
	p.mu.Lock()
	tokens := make([]string, len(p.tokens))
	copy(tokens, p.tokens)
	p.mu.Unlock()

	quotas := make([]TokenQuota, len(tokens))
	for i, token := range tokens {
		quotas[i] = checkTokenRateLimit(ctx, token)
		quotas[i].Index = i
	}
	return quotas
}

// AggregateQuota returns the aggregate remaining percentage and earliest reset
// time across all tokens. Returns 100% if quotas is empty.
func AggregateQuota(quotas []TokenQuota) (pctRemaining int, earliestReset time.Time) {
	var totalLimit, totalRemaining int
	for _, q := range quotas {
		if q.Error != "" {
			continue
		}
		totalLimit += q.Limit
		totalRemaining += q.Remaining
		if !q.Reset.IsZero() && (earliestReset.IsZero() || q.Reset.Before(earliestReset)) {
			earliestReset = q.Reset
		}
	}
	if totalLimit == 0 {
		return 100, earliestReset
	}
	return (totalRemaining * 100) / totalLimit, earliestReset
}

func checkTokenRateLimit(ctx context.Context, token string) TokenQuota {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		return TokenQuota{Error: "request error"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := quotaClient.Do(req)
	if err != nil {
		return TokenQuota{Error: "rate limit check failed"}
	}
	defer resp.Body.Close()

	var rl struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return TokenQuota{Error: "decode error"}
	}

	core := rl.Resources.Core
	return TokenQuota{
		Limit:     core.Limit,
		Remaining: core.Remaining,
		Reset:     time.Unix(core.Reset, 0).UTC(),
	}
}

var quotaClient = &http.Client{Timeout: 5 * time.Second}
```

Note: needs `"context"`, `"encoding/json"`, `"net/http"` imports added.

**Step 5: Run all tokenpool tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestAggregateQuota|TestTokenPool|TestPoolClient" -v`
Expected: PASS

**Step 6: Commit**

```
git add pkg/github/tokenpool.go pkg/github/tokenpool_test.go pkg/net/client.go
git commit -S -m "Add token quota checking and aggregate quota calculation"
```

---

### Task 2: Rewrite Background Scorer to Continuous Loop

**Files:**
- Modify: `pkg/background/scorer.go`
- Modify: `pkg/background/scorer_test.go`

**Step 1: Read existing files**

Read `pkg/background/scorer.go` and `pkg/background/scorer_test.go` to understand current structure.

**Step 2: Rewrite scorer.go**

Replace the entire file with:

```go
package background

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

const (
	defaultBatchSize    = 100
	defaultMinQuotaPct  = 30
	defaultLowDays      = 7
	defaultHighDays     = 30
	emptyQueueSleep     = 30 * time.Second
	resetJitter         = 30 * time.Second
)

// scorerStore defines the store operations needed by the background scorer.
type scorerStore interface {
	DequeueForScoring(ctx context.Context, limit int) ([]postgres.QueueEntry, error)
	RemoveFromQueue(ctx context.Context, username, provider string) error
	GetStaleContributors(ctx context.Context, lowDays, highDays, limit int) ([]postgres.StaleContributor, error)
	GetBehavioralSignals(ctx context.Context, username, provider string) (*model.Behavior, error)
	UpsertContributor(ctx context.Context, username, provider string) error
	SaveScoreHistory(ctx context.Context, username, provider string, value float64, grade string, deep bool) error
	UpdateReputation(ctx context.Context, username, provider string, value float64, grade, version string, signals *score.InputSignals) error
}

// quotaChecker abstracts quota checking for testing.
type quotaChecker interface {
	CheckQuotas(ctx context.Context) []ghclient.TokenQuota
}

// StartBackgroundScorer runs a continuous scoring loop that pauses when
// aggregate token quota drops below the configured threshold.
// Returns a cancel function to stop the loop.
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client, version string) func() {
	batchSize := config.GetEnvAsInt("SCORER_BATCH_SIZE", defaultBatchSize)
	minQuotaPct := config.GetEnvAsInt("SCORER_MIN_QUOTA_PCT", defaultMinQuotaPct)

	slog.Info("starting continuous scorer",
		"batch_size", batchSize,
		"min_quota_pct", minQuotaPct,
	)

	ctx, cancel := context.WithCancel(ctx)

	// Extract quota checker from GitHub client if available.
	var qc quotaChecker
	if pc, ok := gh.(*ghclient.PoolClient); ok {
		qc = pc.Pool()
	}

	go runContinuousScorer(ctx, store, gh, qc, version, batchSize, minQuotaPct)

	return cancel
}

func runContinuousScorer(ctx context.Context, store scorerStore, gh ghclient.Client,
	qc quotaChecker, version string, batchSize, minQuotaPct int) {

	for {
		if ctx.Err() != nil {
			slog.Info("continuous scorer stopped")
			return
		}

		// Check quota before each batch.
		if qc != nil {
			quotas := qc.CheckQuotas(ctx)
			pct, earliestReset := ghclient.AggregateQuota(quotas)
			if pct < minQuotaPct {
				wait := time.Until(earliestReset) + jitter()
				if wait < time.Minute {
					wait = time.Minute
				}
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
		scored := drainQueue(ctx, store, gh, version, batchSize)

		// Phase 2: Rescore stale contributors if queue was empty.
		if scored == 0 {
			staleScored := rescoreStale(ctx, store, gh, version, batchSize)
			if staleScored == 0 {
				// Nothing to do — sleep briefly before checking again.
				sleepCtx(ctx, emptyQueueSleep)
			}
		}
	}
}

func drainQueue(ctx context.Context, store scorerStore, gh ghclient.Client, version string, batchSize int) int {
	queued, err := store.DequeueForScoring(ctx, batchSize)
	if err != nil {
		slog.Error("dequeue for scoring", "error", err)
		return 0
	}
	if len(queued) == 0 {
		return 0
	}

	var scored int
	for _, q := range queued {
		if ctx.Err() != nil {
			return scored
		}
		if err := scoreContributor(ctx, store, gh, q.Username, q.Provider, version); err != nil {
			slog.Warn("score queued", "username", q.Username, "priority", q.Priority, "error", err)
			continue
		}
		_ = store.RemoveFromQueue(ctx, q.Username, q.Provider)
		scored++
	}
	if scored > 0 {
		slog.Info("queue scoring complete", "scored", scored, "total", len(queued))
	}
	return scored
}

func rescoreStale(ctx context.Context, store scorerStore, gh ghclient.Client, version string, batchSize int) int {
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

	var scored int
	for _, c := range stale {
		if ctx.Err() != nil {
			return scored
		}
		if err := scoreContributor(ctx, store, gh, c.Username, c.Provider, version); err != nil {
			slog.Warn("score stale", "username", c.Username, "error", err)
			continue
		}
		scored++
	}
	slog.Info("stale rescoring complete", "scored", scored, "total", len(stale))
	return scored
}

// scoreContributor fetches signals, computes a score, and persists the result.
func scoreContributor(ctx context.Context, store scorerStore, gh ghclient.Client,
	username, provider, version string) error {
	var behavior *model.Behavior
	var hints *ghclient.ArchiveHints
	if beh, err := store.GetBehavioralSignals(ctx, username, provider); err == nil && beh != nil {
		behavior = beh
		hints = &ghclient.ArchiveHints{
			PRsMerged:         int64(beh.TotalPRsMerged),
			PRsClosed:         int64(beh.TotalPRsClosed),
			RecentPRRepoCount: int64(beh.DistinctRepos90d),
		}
	}

	signals, err := gh.FetchSignals(ctx, username, "", hints)
	if err != nil {
		return fmt.Errorf("fetch signals: %w", err)
	}

	value := score.Compute(*signals, false, behavior)
	grade := score.Grade(value)

	_ = store.UpsertContributor(ctx, username, provider)

	if err := store.SaveScoreHistory(ctx, username, provider, value, grade, true); err != nil {
		slog.Warn("save history", "username", username, "error", err)
	}

	return store.UpdateReputation(ctx, username, provider, value, grade, version, signals)
}

func jitter() time.Duration {
	return time.Duration(rand.IntN(int(resetJitter.Seconds()))) * time.Second //nolint:gosec // jitter, not security-sensitive
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
```

**Step 3: Update tests**

Read existing `pkg/background/scorer_test.go`. The existing mock types and most tests stay the same. Key changes:

- Remove `TestScorerConstants` (constants changed)
- Update `drainQueue` calls to pass `batchSize` parameter
- `runScorer` no longer exists — replace `TestRunScorerDrainsQueueThenStale` with a test that calls `drainQueue` then `rescoreStale` directly
- Add `TestSleepCtxCanceled` test

The `drainQueue` and `rescoreStale` are the testable units now. The continuous loop itself is tested via context cancellation.

Update `TestDrainQueueScoresAndRemoves`:
```go
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
```

Update `TestDrainQueueEmptyQueue`:
```go
func TestDrainQueueEmptyQueue(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{queue: nil}
	gh := &mockGHClient{}
	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
	if scored != 0 {
		t.Error("expected 0 scored from empty queue")
	}
}
```

Update `TestDrainQueueDequeueError`:
```go
func TestDrainQueueDequeueError(t *testing.T) {
	t.Parallel()
	store := &mockScorerStore{dequeueErr: errors.New("db down")}
	gh := &mockGHClient{}
	scored := drainQueue(context.Background(), store, gh, testVersion, 100)
	if scored != 0 {
		t.Error("expected 0 scored on error")
	}
}
```

Update `TestDrainQueuePartialFailure`:
```go
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
```

Add `TestRescoreStale`:
```go
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
```

Replace `TestRunScorerDrainsQueueThenStale` with:
```go
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
```

Add `TestSleepCtxCanceled`:
```go
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
```

**Step 4: Run tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/background/ -v -count=1`
Expected: PASS

**Step 5: Commit**

```
git add pkg/background/scorer.go pkg/background/scorer_test.go
git commit -S -m "Rewrite scorer as continuous loop with quota-aware throttling"
```

---

### Task 3: Admin Dashboard Token Pool with Live Quota

**Files:**
- Modify: `pkg/server/handler_admin.go`
- Modify: `pkg/server/templates/admin.html`

**Step 1: Update handler to use CheckQuotas**

Read `pkg/server/handler_admin.go`. Replace the token pool section in `adminDashboardHandler`:

```go
		// Token pool health with live quota.
		if pool != nil {
			quotas := pool.CheckQuotas(r.Context())
			pct, _ := ghclient.AggregateQuota(quotas)
			data["PoolQuotas"] = quotas
			data["PoolTotal"] = pool.Size()
			data["PoolActive"] = pool.ActiveCount()
			data["PoolAggregatePct"] = pct
		}
```

Remove the old `PoolExhausted` and `PoolUsage` lines.

**Step 2: Update template**

Read `pkg/server/templates/admin.html`. Replace the Token Pool section:

```html
  <section class="settings-section">
    <h2>Token Pool</h2>
    {{if .PoolQuotas}}
    <dl>
      <dt>Tokens</dt><dd>{{.PoolTotal}} total, {{.PoolActive}} active</dd>
      <dt>Aggregate Quota</dt><dd>{{.PoolAggregatePct}}% remaining</dd>
    </dl>
    <table class="data-table">
      <thead>
        <tr>
          <th>#</th>
          <th>Limit</th>
          <th>Used</th>
          <th>Remaining</th>
          <th>%</th>
          <th>Reset</th>
        </tr>
      </thead>
      <tbody>
        {{range .PoolQuotas}}
        <tr>
          {{if .Error}}
          <td>{{.Index}}</td>
          <td colspan="5">{{.Error}}</td>
          {{else}}
          <td>{{.Index}}</td>
          <td>{{.Limit}}</td>
          <td>{{sub .Limit .Remaining}}</td>
          <td>{{.Remaining}}</td>
          <td>{{if .Limit}}{{mul (mul 1.0 .Remaining) (mul 1.0 (int (int64 100)))}}{{else}}0{{end}}</td>
          <td>{{.Reset.Format "15:04:05"}}</td>
          {{end}}
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}<p>No token pool configured.</p>{{end}}
  </section>
```

Wait — the percentage math in templates is hard. Better to pre-compute in the handler. Add a wrapper struct:

```go
type tokenQuotaRow struct {
	Index     int
	Limit     int
	Used      int
	Remaining int
	Percent   int
	Reset     string
	Error     string
}
```

In the handler, convert quotas to rows:

```go
		if pool != nil {
			quotas := pool.CheckQuotas(r.Context())
			pct, _ := ghclient.AggregateQuota(quotas)
			rows := make([]tokenQuotaRow, len(quotas))
			for i, q := range quotas {
				rows[i] = tokenQuotaRow{
					Index: q.Index,
					Limit: q.Limit,
					Used:  q.Limit - q.Remaining,
					Remaining: q.Remaining,
					Error: q.Error,
				}
				if q.Limit > 0 {
					rows[i].Percent = (q.Remaining * 100) / q.Limit
				}
				if !q.Reset.IsZero() {
					rows[i].Reset = q.Reset.Format("15:04:05")
				}
			}
			data["PoolQuotas"] = rows
			data["PoolTotal"] = pool.Size()
			data["PoolActive"] = pool.ActiveCount()
			data["PoolAggregatePct"] = pct
		}
```

Simpler template:

```html
  <section class="settings-section">
    <h2>Token Pool</h2>
    {{if .PoolQuotas}}
    <dl>
      <dt>Tokens</dt><dd>{{.PoolTotal}} total, {{.PoolActive}} active</dd>
      <dt>Aggregate Quota</dt><dd>{{.PoolAggregatePct}}% remaining</dd>
    </dl>
    <table class="data-table">
      <thead>
        <tr><th>#</th><th>Limit</th><th>Used</th><th>Remaining</th><th>%</th><th>Reset</th></tr>
      </thead>
      <tbody>
        {{range .PoolQuotas}}
        <tr>
          {{if .Error}}
          <td>{{.Index}}</td><td colspan="5">{{.Error}}</td>
          {{else}}
          <td>{{.Index}}</td><td>{{.Limit}}</td><td>{{.Used}}</td><td>{{.Remaining}}</td><td>{{.Percent}}%</td><td>{{.Reset}}</td>
          {{end}}
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}<p>No token pool configured.</p>{{end}}
  </section>
```

**Step 3: Run tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -run TestAdmin -v -count=1`
Expected: PASS

**Step 4: Commit**

```
git add pkg/server/handler_admin.go pkg/server/templates/admin.html
git commit -S -m "Show live token quota in admin dashboard"
```

---

### Task 4: Update Docs and Clean Up

**Files:**
- Modify: `docs/MVP.md`
- Modify: `.claude/CLAUDE.md`

**Step 1: Update MVP.md**

Add a note under Phase 8 about the continuous scorer improvement.

**Step 2: Update CLAUDE.md env vars**

- Add `SCORER_BATCH_SIZE` and `SCORER_MIN_QUOTA_PCT`
- Remove `SCORER_INTERVAL_SEC` reference if present

**Step 3: Commit**

```
git add docs/MVP.md .claude/CLAUDE.md
git commit -S -m "Update docs: continuous scorer with quota-aware throttling"
```

---

### Task 5: Qualify

**Step 1: Run full qualification**

Run: `make qualify`
Expected: All tests pass, lint clean.

**Step 2: Fix any issues**

**Step 3: Commit and push**

```
git push
```
