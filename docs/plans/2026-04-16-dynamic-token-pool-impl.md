# Dynamic Token Pool Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the static startup-only token pool with a dynamically refreshing pool that tracks token expiry, refreshes on a timer and webhook signals, and shows stable labels in the admin dashboard.

**Architecture:** `TokenPool` stores `poolEntry` structs (installationID, label, token, expiresAt) instead of raw strings. A single background goroutine owns all pool mutations — triggered by a 1-minute timer (only mints when tokens near expiry) or a channel signal from webhooks. The webhook handler sends a non-blocking signal on installation create/delete/suspend events.

**Tech Stack:** Go, `sync.Mutex`, `time.Ticker`, channels, `pkg/tenant` for minting

---

### Task 1: Refactor TokenPool internals to use poolEntry

**Files:**
- Modify: `pkg/github/tokenpool.go`
- Modify: `pkg/github/tokenpool_test.go`

**Step 1: Write failing tests for poolEntry-based construction and expiry**

Add to `pkg/github/tokenpool_test.go`:

```go
func TestNewTokenPoolFromEntries(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "org-a", Token: "tok-a", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "org-b", Token: "tok-b", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "PAT", Token: "pat-1"},
	}
	pool := NewTokenPoolFromEntries(entries)
	if pool.Size() != 3 {
		t.Fatalf("size: got %d, want 3", pool.Size())
	}
	if got := pool.Token(); got != "tok-a" {
		t.Errorf("first: got %q, want tok-a", got)
	}
}

func TestPoolEntryExpiry(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "expired", Token: "tok-old", ExpiresAt: time.Now().Add(2 * time.Minute)}, // within 5-min buffer
		{Label: "fresh", Token: "tok-new", ExpiresAt: time.Now().Add(time.Hour)},
	}
	pool := NewTokenPoolFromEntries(entries)
	// Should skip expired entry
	got := pool.Token()
	if got != "tok-new" {
		t.Errorf("should skip near-expiry token: got %q, want tok-new", got)
	}
}

func TestPoolMixedPATAndInstallation(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "org-a", Token: "inst-tok", ExpiresAt: time.Now().Add(2 * time.Minute)}, // near expiry
		{Label: "PAT", Token: "pat-tok"}, // zero ExpiresAt, never expires
	}
	pool := NewTokenPoolFromEntries(entries)
	got := pool.Token()
	if got != "pat-tok" {
		t.Errorf("should skip near-expiry, use PAT: got %q, want pat-tok", got)
	}
}

func TestPoolAllExpired(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "a", Token: "tok-a", ExpiresAt: time.Now().Add(time.Minute)},
		{Label: "b", Token: "tok-b", ExpiresAt: time.Now().Add(2 * time.Minute)},
	}
	pool := NewTokenPoolFromEntries(entries)
	if got := pool.Token(); got != "" {
		t.Errorf("all near-expiry should return empty: got %q", got)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestNewTokenPoolFromEntries|TestPoolEntryExpiry|TestPoolMixed|TestPoolAllExpired" -v`
Expected: FAIL — `NewTokenPoolFromEntries` and `PoolEntry` not defined

**Step 3: Implement poolEntry and NewTokenPoolFromEntries**

In `pkg/github/tokenpool.go`, add the `PoolEntry` type and `tokenExpiryBuffer` constant. Refactor internal storage to `[]poolEntry`. Keep `NewTokenPool` working (creates entries with zero `ExpiresAt`).

```go
const tokenExpiryBuffer = 5 * time.Minute

// PoolEntry describes a token source for the pool.
type PoolEntry struct {
	InstallationID int64
	Label          string    // target login (org/user) or "PAT"
	Token          string
	ExpiresAt      time.Time // zero means never expires (PAT)
}

// poolEntry is the internal representation with exhaustion tracking.
type poolEntry struct {
	installationID int64
	label          string
	token          string
	expiresAt      time.Time
}
```

Change `TokenPool` struct:

```go
type TokenPool struct {
	mu          sync.Mutex
	entries     []poolEntry
	counts      []int
	exhausted   []bool
	exhaustedAt []time.Time
	current     int
}
```

Add constructor:

```go
func NewTokenPoolFromEntries(entries []PoolEntry) *TokenPool {
	pe := make([]poolEntry, len(entries))
	for i, e := range entries {
		pe[i] = poolEntry{
			installationID: e.InstallationID,
			label:          e.Label,
			token:          e.Token,
			expiresAt:      e.ExpiresAt,
		}
	}
	return &TokenPool{
		entries:     pe,
		counts:      make([]int, len(pe)),
		exhausted:   make([]bool, len(pe)),
		exhaustedAt: make([]time.Time, len(pe)),
	}
}
```

Update `NewTokenPool` to build `[]poolEntry` from strings (label="", expiresAt=zero):

```go
func NewTokenPool(tokens ...string) *TokenPool {
	var list []poolEntry
	for _, t := range tokens {
		for part := range strings.SplitSeq(t, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				list = append(list, poolEntry{token: part})
			}
		}
	}
	return &TokenPool{
		entries:     list,
		counts:      make([]int, len(list)),
		exhausted:   make([]bool, len(list)),
		exhaustedAt: make([]time.Time, len(list)),
	}
}
```

Update `Token()` to check expiry:

```go
func (p *TokenPool) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.entries)
	if n == 0 {
		return ""
	}

	now := time.Now()
	for range n {
		idx := p.current
		p.current = (idx + 1) % n

		// Auto-reset tokens whose rate limit window has passed.
		if p.exhausted[idx] && now.Sub(p.exhaustedAt[idx]) > tokenResetWindow {
			p.exhausted[idx] = false
		}

		// Skip expired installation tokens (within buffer of expiry).
		if !p.entries[idx].expiresAt.IsZero() && now.Add(tokenExpiryBuffer).After(p.entries[idx].expiresAt) {
			continue
		}

		if !p.exhausted[idx] {
			p.counts[idx]++
			return p.entries[idx].token
		}
	}

	return ""
}
```

Update all other methods (`Exhaust`, `ActiveCount`, `Size`, `UsageCounts`, `CheckQuotas`) to use `p.entries[i].token` instead of `p.tokens[i]`.

Update `CheckQuotas` to populate `Label`:

```go
func (p *TokenPool) CheckQuotas(ctx context.Context) []TokenQuota {
	p.mu.Lock()
	entries := make([]poolEntry, len(p.entries))
	copy(entries, p.entries)
	p.mu.Unlock()

	quotas := make([]TokenQuota, len(entries))
	for i, e := range entries {
		quotas[i] = checkTokenRateLimit(ctx, e.token)
		quotas[i].Index = i
		quotas[i].Label = e.label
	}
	return quotas
}
```

Add `Label` to `TokenQuota`:

```go
type TokenQuota struct {
	Index     int
	Label     string
	Limit     int
	Remaining int
	Reset     time.Time
	Error     string
}
```

**Step 4: Run tests to verify they pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestNewTokenPool|TestPoolEntry|TestPoolMixed|TestPoolAll|TestTokenPool|TestAggregateQuota|TestCheckQuotas|TestPoolClient|TestIsRateLimited" -v -race`
Expected: ALL PASS — both old and new tests

**Step 5: Commit**

```bash
git add pkg/github/tokenpool.go pkg/github/tokenpool_test.go
git commit -S -m "Refactor TokenPool to use poolEntry with expiry tracking"
```

---

### Task 2: Add Replace method to TokenPool

**Files:**
- Modify: `pkg/github/tokenpool.go`
- Modify: `pkg/github/tokenpool_test.go`

**Step 1: Write failing tests for Replace**

Add to `pkg/github/tokenpool_test.go`:

```go
func TestPoolReplace(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("old-a", "old-b")

	// Use tokens to build up counts
	pool.Token()
	pool.Token()

	newEntries := []PoolEntry{
		{Label: "org-x", Token: "new-x", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "org-y", Token: "new-y", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "org-z", Token: "new-z", ExpiresAt: time.Now().Add(time.Hour)},
	}
	pool.Replace(newEntries)

	if pool.Size() != 3 {
		t.Fatalf("size after replace: got %d, want 3", pool.Size())
	}

	// Cursor should reset — first token is new-x
	if got := pool.Token(); got != "new-x" {
		t.Errorf("first after replace: got %q, want new-x", got)
	}

	// Old tokens should be gone
	counts := pool.UsageCounts()
	if counts[0] != 1 {
		t.Errorf("counts should reset: got %v", counts)
	}
}

func TestPoolReplaceEmpty(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b")
	pool.Replace(nil)
	if pool.Size() != 0 {
		t.Fatalf("size after empty replace: got %d, want 0", pool.Size())
	}
	if got := pool.Token(); got != "" {
		t.Errorf("empty pool should return empty: got %q", got)
	}
}

func TestPoolReplaceClearsExhaustion(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b")
	pool.Exhaust("a")
	pool.Exhaust("b")
	if pool.ActiveCount() != 0 {
		t.Fatal("expected 0 active before replace")
	}

	pool.Replace([]PoolEntry{
		{Label: "new", Token: "fresh", ExpiresAt: time.Now().Add(time.Hour)},
	})
	if pool.ActiveCount() != 1 {
		t.Error("replace should clear exhaustion state")
	}
	if got := pool.Token(); got != "fresh" {
		t.Errorf("got %q, want fresh", got)
	}
}

func TestPoolReplaceConcurrent(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b")
	var wg sync.WaitGroup

	// Concurrent reads while replacing
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.Token()
		}()
	}
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.Replace([]PoolEntry{
				{Label: "x", Token: "tok-x", ExpiresAt: time.Now().Add(time.Hour)},
			})
		}()
	}
	wg.Wait()
	// No race or panic = pass
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestPoolReplace" -v`
Expected: FAIL — `Replace` not defined

**Step 3: Implement Replace**

Add to `pkg/github/tokenpool.go`:

```go
// Replace atomically swaps the pool entries. Resets cursor, counts, and exhaustion state.
func (p *TokenPool) Replace(entries []PoolEntry) {
	pe := make([]poolEntry, len(entries))
	for i, e := range entries {
		pe[i] = poolEntry{
			installationID: e.InstallationID,
			label:          e.Label,
			token:          e.Token,
			expiresAt:      e.ExpiresAt,
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.entries = pe
	p.counts = make([]int, len(pe))
	p.exhausted = make([]bool, len(pe))
	p.exhaustedAt = make([]time.Time, len(pe))
	p.current = 0
}
```

**Step 4: Run tests to verify they pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestPoolReplace" -v -race`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add pkg/github/tokenpool.go pkg/github/tokenpool_test.go
git commit -S -m "Add Replace method to TokenPool for atomic entry swap"
```

---

### Task 3: Add NeedsRefresh and Labels methods

**Files:**
- Modify: `pkg/github/tokenpool.go`
- Modify: `pkg/github/tokenpool_test.go`

**Step 1: Write failing tests**

```go
func TestPoolNeedsRefreshExpiringSoon(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "ok", Token: "a", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "expiring", Token: "b", ExpiresAt: time.Now().Add(3 * time.Minute)}, // within 5-min buffer
	}
	pool := NewTokenPoolFromEntries(entries)
	if !pool.NeedsRefresh() {
		t.Error("should need refresh when token is near expiry")
	}
}

func TestPoolNeedsRefreshAllFresh(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "a", Token: "a", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "b", Token: "b", ExpiresAt: time.Now().Add(time.Hour)},
	}
	pool := NewTokenPoolFromEntries(entries)
	if pool.NeedsRefresh() {
		t.Error("should not need refresh when all tokens are fresh")
	}
}

func TestPoolNeedsRefreshPATOnly(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("pat-token")
	if pool.NeedsRefresh() {
		t.Error("PAT-only pool should never need refresh")
	}
}

func TestPoolNeedsRefreshEmpty(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool()
	if pool.NeedsRefresh() {
		t.Error("empty pool should not need refresh")
	}
}

func TestPoolLabels(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "org-a", Token: "a"},
		{Label: "PAT", Token: "b"},
		{Label: "org-c", Token: "c"},
	}
	pool := NewTokenPoolFromEntries(entries)
	labels := pool.Labels()
	if len(labels) != 3 || labels[0] != "org-a" || labels[1] != "PAT" || labels[2] != "org-c" {
		t.Errorf("labels: got %v", labels)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestPoolNeedsRefresh|TestPoolLabels" -v`
Expected: FAIL

**Step 3: Implement**

```go
// NeedsRefresh returns true if any installation token is within the expiry buffer.
func (p *TokenPool) NeedsRefresh() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, e := range p.entries {
		if !e.expiresAt.IsZero() && now.Add(tokenExpiryBuffer).After(e.expiresAt) {
			return true
		}
	}
	return false
}

// Labels returns the label for each entry in pool order.
func (p *TokenPool) Labels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	labels := make([]string, len(p.entries))
	for i, e := range p.entries {
		labels[i] = e.label
	}
	return labels
}
```

**Step 4: Run all pool tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestPool|TestNewToken|TestAggregateQuota|TestCheckQuotas|TestIsRateLimited" -v -race`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add pkg/github/tokenpool.go pkg/github/tokenpool_test.go
git commit -S -m "Add NeedsRefresh and Labels methods to TokenPool"
```

---

### Task 4: Implement pool refresh goroutine

**Files:**
- Create: `pkg/github/pool_refresh.go`
- Create: `pkg/github/pool_refresh_test.go`

**Step 1: Write failing tests**

Create `pkg/github/pool_refresh_test.go`:

```go
package github

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshOnTimer(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		called.Add(1)
		return []PoolEntry{
			{Label: "refreshed", Token: "new-tok", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
	}

	// Pool with token about to expire
	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(2 * time.Minute)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

	// Wait for at least one refresh cycle
	time.Sleep(200 * time.Millisecond)

	if called.Load() == 0 {
		t.Error("refresh should have been called for near-expiry token")
	}
	if got := pool.Token(); got != "new-tok" {
		t.Errorf("pool should have new token: got %q", got)
	}
}

func TestRefreshOnNotify(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		called.Add(1)
		return []PoolEntry{
			{Label: "notified", Token: "notify-tok", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
	}

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(time.Hour)}, // not expiring
	})

	notifyCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, notifyCh, time.Hour) // long tick — won't fire
	defer stop()

	// Signal refresh
	notifyCh <- struct{}{}
	time.Sleep(200 * time.Millisecond)

	if called.Load() == 0 {
		t.Error("refresh should have been called on notify")
	}
	if got := pool.Token(); got != "notify-tok" {
		t.Errorf("pool should have notified token: got %q", got)
	}
}

func TestRefreshDedup(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		called.Add(1)
		return []PoolEntry{
			{Label: "x", Token: "tok", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
	}

	pool := NewTokenPool("initial")
	notifyCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, notifyCh, time.Hour)
	defer stop()

	// Rapid signals
	notifyCh <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	notifyCh <- struct{}{}
	time.Sleep(100 * time.Millisecond)

	// Second signal should be deduped (within 30s window)
	if called.Load() > 1 {
		t.Errorf("expected dedup: refresh called %d times", called.Load())
	}
}

func TestRefreshFailureKeepsOldTokens(t *testing.T) {
	t.Parallel()
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		return nil, fmt.Errorf("mint failed")
	}

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(2 * time.Minute)}, // near expiry
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

	time.Sleep(200 * time.Millisecond)

	// Pool should still have old token (not replaced with empty)
	if pool.Size() != 1 {
		t.Errorf("pool should retain old entries on failure: size=%d", pool.Size())
	}
}

func TestRefreshAllMintsFail(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		called.Add(1)
		return nil, fmt.Errorf("all mints failed")
	}

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "a", Token: "tok-a", ExpiresAt: time.Now().Add(time.Minute)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

	time.Sleep(200 * time.Millisecond)

	if called.Load() == 0 {
		t.Error("should have attempted refresh")
	}
	// Old entries preserved
	if pool.Size() != 1 {
		t.Errorf("should keep old entries: size=%d", pool.Size())
	}
}

func TestRefreshEmptyResult(t *testing.T) {
	t.Parallel()
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		return nil, nil // no error but no entries
	}

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(time.Minute)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

	time.Sleep(200 * time.Millisecond)

	// Empty result should NOT replace pool — treated same as failure
	if pool.Size() != 1 {
		t.Errorf("empty result should keep old entries: size=%d", pool.Size())
	}
}

func TestRefreshContextCancel(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		called.Add(1)
		return []PoolEntry{{Label: "x", Token: "x"}}, nil
	}

	pool := NewTokenPool("a")
	ctx, cancel := context.WithCancel(context.Background())

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	cancel()
	stop()

	before := called.Load()
	time.Sleep(200 * time.Millisecond)
	after := called.Load()

	if after > before {
		t.Error("refresh should stop after context cancel")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestRefresh" -v`
Expected: FAIL — `StartPoolRefresh` not defined

**Step 3: Implement StartPoolRefresh**

Create `pkg/github/pool_refresh.go`:

```go
package github

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultRefreshInterval = time.Minute
	refreshDedup           = 30 * time.Second
)

// RefreshFunc loads current installations and returns pool entries.
// Called by the refresh goroutine — must be safe for concurrent use.
type RefreshFunc func(ctx context.Context) ([]PoolEntry, error)

// StartPoolRefresh runs a background goroutine that refreshes the token pool
// when tokens are near expiry or when signaled via notifyCh.
// Pass tickInterval=0 to use the default (1 minute).
// Returns a stop function that cancels the goroutine.
func StartPoolRefresh(ctx context.Context, pool *TokenPool, refreshFn RefreshFunc,
	notifyCh <-chan struct{}, tickInterval time.Duration) func() {
	if tickInterval == 0 {
		tickInterval = defaultRefreshInterval
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		runRefreshLoop(ctx, pool, refreshFn, notifyCh, tickInterval)
	}()

	return func() {
		cancel()
		<-done
	}
}

func runRefreshLoop(ctx context.Context, pool *TokenPool, refreshFn RefreshFunc,
	notifyCh <-chan struct{}, tickInterval time.Duration) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var lastRefresh time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if pool.NeedsRefresh() {
				doRefresh(ctx, pool, refreshFn, &lastRefresh)
			}
		case <-notifyCh:
			doRefresh(ctx, pool, refreshFn, &lastRefresh)
		}
	}
}

func doRefresh(ctx context.Context, pool *TokenPool, refreshFn RefreshFunc, lastRefresh *time.Time) {
	// Dedup: skip if refreshed recently.
	if time.Since(*lastRefresh) < refreshDedup {
		slog.Debug("pool refresh deduped", "last_refresh", lastRefresh)
		return
	}

	entries, err := refreshFn(ctx)
	if err != nil {
		slog.Error("pool refresh failed", "error", err)
		return
	}
	if len(entries) == 0 {
		slog.Warn("pool refresh returned no entries, keeping existing pool")
		return
	}

	pool.Replace(entries)
	*lastRefresh = time.Now()
	slog.Info("pool refreshed", "entries", len(entries))
}
```

**Step 4: Run tests to verify they pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run "TestRefresh" -v -race -count=1`
Expected: ALL PASS

**Step 5: Run full package tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -v -race`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add pkg/github/pool_refresh.go pkg/github/pool_refresh_test.go
git commit -S -m "Add background pool refresh goroutine with timer and notify channel"
```

---

### Task 5: Wire webhook handler to send notify signal

**Files:**
- Modify: `pkg/server/handler_webhook.go`
- Modify: `pkg/server/server.go` (makeRouter signature)

**Step 1: Write failing test for non-blocking webhook notify**

Add to a new file or existing webhook test. Since the webhook handler test requires DB, write a focused unit test:

Create `pkg/server/handler_webhook_notify_test.go`:

```go
package server

import "testing"

func TestWebhookNotifyNonBlocking(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{}, 1)

	// Fill the channel
	ch <- struct{}{}

	// Second send should not block
	notifyInstallationChange(ch)

	// Should still have exactly one signal
	select {
	case <-ch:
	default:
		t.Error("channel should have had a signal")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -run "TestWebhookNotifyNonBlocking" -v`
Expected: FAIL — `notifyInstallationChange` not defined

**Step 3: Implement webhook notify**

In `pkg/server/handler_webhook.go`, update `webhookHandler` to accept the notify channel and add the helper:

```go
func webhookHandler(db *sql.DB, secret string, installNotify chan<- struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxWebhookBytes = 1 << 20
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBytes))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !verifySignature(body, r.Header.Get("X-Hub-Signature-256"), secret) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		event := r.Header.Get("X-GitHub-Event")
		switch event {
		case "installation":
			if err := handleInstallationEvent(r.Context(), db, body); err != nil {
				slog.Error("installation event", "error", err)
				http.Error(w, "processing failed", http.StatusInternalServerError)
				return
			}
			notifyInstallationChange(installNotify)
		default:
			slog.Debug("ignoring webhook event", "event", event)
		}

		w.WriteHeader(http.StatusOK)
	}
}

// notifyInstallationChange sends a non-blocking signal on the notify channel.
func notifyInstallationChange(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
```

**Step 4: Update makeRouter to pass notify channel**

In `pkg/server/server.go`, update `makeRouter` signature:

```go
func makeRouter(store *postgres.Store, scoreSvc *service.ScoreService, pool *ghclient.TokenPool,
	oauthCfg *oauth.Config, opts Options, installNotify chan<- struct{}) (*http.ServeMux, func()) {
```

Update the webhook handler call inside makeRouter:

```go
if webhookSecret != "" {
    mux.HandleFunc("POST /webhook/github", webhookHandler(db, webhookSecret, installNotify))
}
```

Update the `makeRouter` call in `Run()`:

```go
installNotify := make(chan struct{}, 1)
mux, routerCleanup := makeRouter(store, scoreSvc, pool, oauthCfg, opts, installNotify)
```

**Step 5: Run tests to verify they pass**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -run "TestWebhookNotify" -v -race`
Expected: PASS

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -v -race -count=1`
Expected: ALL PASS (verify no compilation errors from signature change)

**Step 6: Commit**

```bash
git add pkg/server/handler_webhook.go pkg/server/handler_webhook_notify_test.go pkg/server/server.go
git commit -S -m "Wire webhook handler to send install notify signal"
```

---

### Task 6: Wire pool refresh into server startup

**Files:**
- Modify: `pkg/server/server.go`

**Step 1: Update buildGitHubClient to return pool entries with expiry**

Change `buildGitHubClient` to build `PoolEntry` slice and return the pool separately so it can be passed to `StartPoolRefresh`:

```go
func buildGitHubClient(ctx context.Context, store *postgres.Store) (ghclient.Client, error) {
	appCfg, appErr := tenant.LoadGitHubAppConfig()

	if appErr == nil && store != nil {
		entries, err := mintPoolEntries(ctx, store, appCfg)
		if err == nil && len(entries) > 0 {
			pool := ghclient.NewTokenPoolFromEntries(entries)
			slog.Info("using token pool GitHub client", "tokens", pool.Size())
			return ghclient.NewPoolClient(pool), nil
		}
	}

	// Single installation client (legacy path).
	if appErr == nil {
		instID := int64(config.GetEnvAsInt("GITHUB_APP_INSTALLATION_ID", 0))
		if instID > 0 {
			slog.Info("using GitHub App installation client", "app_id", appCfg.AppID, "installation_id", instID)
			return ghclient.NewInstallationClient(appCfg, instID), nil
		}
	}

	token := config.GetEnv("GITHUB_TOKEN", "")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN or GitHub App config required")
	}
	slog.Info("using PAT GitHub client")
	return ghclient.NewPATClient(ctx, token), nil
}

// mintPoolEntries loads all active installations, mints tokens, and returns pool entries.
// Also appends GITHUB_TOKEN as a PAT entry if set.
func mintPoolEntries(ctx context.Context, store *postgres.Store, appCfg *tenant.GitHubAppConfig) ([]ghclient.PoolEntry, error) {
	installations, err := tenant.GetAllActiveInstallations(ctx, store.DB(), appCfg.AppID)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}

	var entries []ghclient.PoolEntry
	for _, inst := range installations {
		tok, err := tenant.MintInstallationToken(ctx, appCfg, inst.InstallationID)
		if err != nil {
			slog.Error("skip installation token", "installation_id", inst.InstallationID, "error", err)
			continue
		}
		entries = append(entries, ghclient.PoolEntry{
			InstallationID: inst.InstallationID,
			Label:          inst.TargetLogin,
			Token:          tok.Token,
			ExpiresAt:      tok.ExpiresAt,
		})
		slog.Debug("minted installation token", "installation_id", inst.InstallationID, "org", inst.TargetLogin)
	}

	if pat := config.GetEnv("GITHUB_TOKEN", ""); pat != "" {
		entries = append(entries, ghclient.PoolEntry{
			Label: "PAT",
			Token: pat,
		})
	}

	return entries, nil
}
```

**Step 2: Start pool refresh in Run()**

In `Run()`, after building the GitHub client and before starting background ops, start the refresh goroutine:

```go
installNotify := make(chan struct{}, 1)

var pool *ghclient.TokenPool
if pc, ok := ghClient.(*ghclient.PoolClient); ok {
    pool = pc.Pool()

    // Build refresh function that re-mints all installation tokens.
    appCfg, _ := tenant.LoadGitHubAppConfig() // already succeeded above
    refreshStop := ghclient.StartPoolRefresh(ctx, pool, func(ctx context.Context) ([]ghclient.PoolEntry, error) {
        return mintPoolEntries(ctx, store, appCfg)
    }, installNotify, 0)
    defer refreshStop()
}
```

Remove the old `var pool` block that was after `scoreSvc` creation. The `pool` variable is now set above and passed to `makeRouter`.

**Step 3: Verify compilation**

Run: `GOFLAGS="-mod=vendor" go build ./cmd/devtrace-site/`
Expected: SUCCESS

**Step 4: Run full test suite**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ ./pkg/server/ -v -race`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add pkg/server/server.go
git commit -S -m "Wire pool refresh goroutine into server startup with webhook notify"
```

---

### Task 7: Update admin dashboard to show labels

**Files:**
- Modify: `pkg/server/handler_admin.go`
- Modify: `pkg/server/templates/admin.html`

**Step 1: Add Label to tokenQuotaRow**

In `pkg/server/handler_admin.go`, update the struct:

```go
type tokenQuotaRow struct {
	Index     int
	Label     string
	Limit     int
	Used      int
	Remaining int
	Percent   int
	Reset     string
	Error     string
}
```

Update `loadPoolQuotas` to populate `Label`:

```go
func loadPoolQuotas(ctx context.Context, pool *ghclient.TokenPool, data map[string]any) {
	quotas := pool.CheckQuotas(ctx)
	pct, _ := ghclient.AggregateQuota(quotas)
	rows := make([]tokenQuotaRow, len(quotas))
	for i, q := range quotas {
		rows[i] = tokenQuotaRow{
			Index:     q.Index,
			Label:     q.Label,
			Limit:     q.Limit,
			Used:      q.Limit - q.Remaining,
			Remaining: q.Remaining,
			Error:     q.Error,
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

**Step 2: Update admin template**

In `pkg/server/templates/admin.html`, change the table header and rows to show Label instead of Index:

Change the table header from:
```html
<tr><th>#</th><th>Limit</th><th>Used</th><th>Remaining</th><th>%</th><th>Reset</th></tr>
```
To:
```html
<tr><th>Source</th><th>Limit</th><th>Used</th><th>Remaining</th><th>%</th><th>Reset</th></tr>
```

Change the error row from:
```html
<td>{{.Index}}</td><td colspan="5">{{.Error}}</td>
```
To:
```html
<td>{{if .Label}}{{.Label}}{{else}}#{{.Index}}{{end}}</td><td colspan="5">{{.Error}}</td>
```

Change the data row `<td>{{.Index}}</td>` to:
```html
<td>{{if .Label}}{{.Label}}{{else}}#{{.Index}}{{end}}</td>
```

**Step 3: Verify compilation**

Run: `GOFLAGS="-mod=vendor" go build ./cmd/devtrace-site/`
Expected: SUCCESS

**Step 4: Run tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -v -race`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add pkg/server/handler_admin.go pkg/server/templates/admin.html
git commit -S -m "Show token source label in admin dashboard instead of index"
```

---

### Task 8: Run full qualification

**Step 1: Run make qualify**

Run: `make qualify`
Expected: ALL PASS — tests, lint, govulncheck, e2e

**Step 2: Fix any issues**

If lint or tests fail, fix and re-run until clean.

**Step 3: Final commit if any fixes needed**

```bash
git add -A
git commit -S -m "Fix lint/test issues from dynamic token pool"
```

---

Plan complete and saved to `docs/plans/2026-04-16-dynamic-token-pool-impl.md`. Two execution options:

**1. Subagent-Driven (this session)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** — Open new session with executing-plans, batch execution with checkpoints

Which approach?