package github

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewTokenPoolSingle(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("tok1")
	if pool.Size() != 1 {
		t.Fatalf("size: got %d, want 1", pool.Size())
	}
	if got := pool.Token(); got != "tok1" {
		t.Errorf("token: got %q, want tok1", got)
	}
	if got := pool.Token(); got != "tok1" {
		t.Errorf("single token should always return same: got %q", got)
	}
}

func TestNewTokenPoolMultiple(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")
	if pool.Size() != 3 {
		t.Fatalf("size: got %d, want 3", pool.Size())
	}
	if got := pool.Token(); got != "a" {
		t.Errorf("first: got %q, want a", got)
	}
	if got := pool.Token(); got != "b" {
		t.Errorf("second: got %q, want b", got)
	}
	if got := pool.Token(); got != "c" {
		t.Errorf("third: got %q, want c", got)
	}
	if got := pool.Token(); got != "a" {
		t.Errorf("wrap: got %q, want a", got)
	}
}

func TestNewTokenPoolCommaSeparated(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("tok1,tok2,tok3")
	if pool.Size() != 3 {
		t.Fatalf("size: got %d, want 3", pool.Size())
	}
}

func TestNewTokenPoolEmpty(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("")
	if pool.Size() != 0 {
		t.Fatalf("size: got %d, want 0", pool.Size())
	}
	if got := pool.Token(); got != "" {
		t.Errorf("empty pool should return empty: got %q", got)
	}
}

func TestNewTokenPoolTrimsWhitespace(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool(" tok1 , tok2 ")
	if pool.Size() != 2 {
		t.Fatalf("size: got %d, want 2", pool.Size())
	}
	if got := pool.Token(); got != "tok1" {
		t.Errorf("got %q, want tok1", got)
	}
}

func TestTokenPoolRoundRobin(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b")
	seen := make(map[string]int)
	for range 100 {
		seen[pool.Token()]++
	}
	if seen["a"] != 50 || seen["b"] != 50 {
		t.Errorf("expected 50/50, got a=%d b=%d", seen["a"], seen["b"])
	}
}

func TestTokenPoolExhaustSingle(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")
	pool.Exhaust("b", time.Time{})
	if pool.ActiveCount() != 2 {
		t.Fatalf("active: got %d, want 2", pool.ActiveCount())
	}

	seen := make(map[string]int)
	for range 10 {
		tok := pool.Token()
		if tok == "" {
			t.Fatal("unexpected empty token")
		}
		seen[tok]++
	}
	if seen["b"] != 0 {
		t.Error("exhausted token b should never be returned")
	}
}

func TestTokenPoolExhaustAll(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b")
	pool.Exhaust("a", time.Time{})
	pool.Exhaust("b", time.Time{})
	if pool.ActiveCount() != 0 {
		t.Fatalf("active: got %d, want 0", pool.ActiveCount())
	}
	if got := pool.Token(); got != "" {
		t.Errorf("all exhausted should return empty: got %q", got)
	}
}

func TestTokenPoolExhaustMidRotation(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")
	if got := pool.Token(); got != "a" {
		t.Fatalf("first: got %q, want a", got)
	}
	pool.Exhaust("b", time.Time{})
	if got := pool.Token(); got != "c" {
		t.Errorf("should skip b: got %q, want c", got)
	}
	if got := pool.Token(); got != "a" {
		t.Errorf("wrap should skip b: got %q, want a", got)
	}
}

func TestTokenPoolExhaustUnknown(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a")
	pool.Exhaust("unknown", time.Time{}) // should not panic
	if pool.ActiveCount() != 1 {
		t.Error("unknown exhaust should not affect pool")
	}
}

func TestTokenPoolUsageCounts(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")
	for range 9 {
		pool.Token()
	}
	counts := pool.UsageCounts()
	if len(counts) != 3 || counts[0] != 3 || counts[1] != 3 || counts[2] != 3 {
		t.Errorf("counts: got %v, want [3 3 3]", counts)
	}
}

func TestTokenPoolConcurrentAccess(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok := pool.Token()
			if tok == "" {
				t.Error("unexpected empty token")
			}
		}()
	}
	wg.Wait()
}

func TestTokenPoolNeverReturnsComma(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("ghp_abc123,ghp_def456")
	for range 10 {
		tok := pool.Token()
		for _, c := range tok {
			if c == ',' {
				t.Fatalf("Token() returned comma-separated: %q", tok)
			}
		}
	}
}

func TestAggregateQuota(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		quotas  []TokenQuota
		wantPct int
	}{
		{"empty", nil, 100},
		{"full_quota", []TokenQuota{
			{Limit: 5000, Remaining: 5000, Reset: time.Now().Add(time.Hour)},
		}, 100},
		{"half_used", []TokenQuota{
			{Limit: 5000, Remaining: 2500, Reset: time.Now().Add(time.Hour)},
			{Limit: 5000, Remaining: 2500, Reset: time.Now().Add(time.Hour)},
		}, 50},
		{"all_exhausted", []TokenQuota{
			{Limit: 5000, Remaining: 0, Reset: time.Now().Add(time.Hour)},
		}, 0},
		{"mixed", []TokenQuota{
			{Limit: 5000, Remaining: 1000, Reset: time.Now().Add(time.Hour)},
			{Limit: 10000, Remaining: 8000, Reset: time.Now().Add(2 * time.Hour)},
		}, 60},
		{"all_errored", []TokenQuota{
			{Error: "failed"},
			{Error: "failed"},
		}, 100},
		{"skip_errors", []TokenQuota{
			{Limit: 5000, Remaining: 2500, Reset: time.Now().Add(time.Hour)},
			{Error: "failed"},
		}, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pct, _ := AggregateQuota(tt.quotas)
			if pct != tt.wantPct {
				t.Errorf("pct = %d, want %d", pct, tt.wantPct)
			}
		})
	}
}

func TestAggregateQuotaEarliestReset(t *testing.T) {
	t.Parallel()
	early := time.Now().Add(30 * time.Minute)
	late := time.Now().Add(2 * time.Hour)
	quotas := []TokenQuota{
		{Limit: 5000, Remaining: 2500, Reset: late},
		{Limit: 5000, Remaining: 2500, Reset: early},
	}
	_, reset := AggregateQuota(quotas)
	if !reset.Equal(early) {
		t.Errorf("reset = %v, want %v", reset, early)
	}
}

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

func TestPoolEntryNearExpiry(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "near-expiry", Token: "tok-old", ExpiresAt: time.Now().Add(2 * time.Minute)},
		{Label: "fresh", Token: "tok-new", ExpiresAt: time.Now().Add(time.Hour)},
	}
	pool := NewTokenPoolFromEntries(entries)
	// Near-expiry tokens are still returned (valid until actual expiry),
	// but trigger a refresh signal.
	got := pool.Token()
	if got != "tok-old" {
		t.Errorf("near-expiry token should still be used: got %q, want tok-old", got)
	}
}

func TestPoolNearExpiryTriggersRefresh(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "near-expiry", Token: "tok-old", ExpiresAt: time.Now().Add(2 * time.Minute)},
	}
	pool := NewTokenPoolFromEntries(entries)
	ch := make(chan struct{}, 1)
	pool.SetRefreshCh(ch)

	got := pool.Token()
	if got != "tok-old" {
		t.Errorf("got %q, want tok-old", got)
	}
	select {
	case <-ch:
		// refresh signaled — expected
	default:
		t.Error("expected refresh signal for near-expiry token")
	}
}

func TestPoolMixedPATAndInstallation(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "org-a", Token: "inst-tok", ExpiresAt: time.Now().Add(2 * time.Minute)},
		{Label: "PAT", Token: "pat-tok"},
	}
	pool := NewTokenPoolFromEntries(entries)
	// Near-expiry installation token is still used in round-robin order.
	got := pool.Token()
	if got != "inst-tok" {
		t.Errorf("near-expiry token should still be used: got %q, want inst-tok", got)
	}
}

func TestPoolAllActuallyExpired(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "a", Token: "tok-a", ExpiresAt: time.Now().Add(-time.Minute)},
		{Label: "b", Token: "tok-b", ExpiresAt: time.Now().Add(-2 * time.Minute)},
	}
	pool := NewTokenPoolFromEntries(entries)
	// Actually expired tokens (past ExpiresAt) are still returned — the pool
	// doesn't hard-block; the HTTP client will get a 401 and the refresh
	// goroutine will re-mint.
	got := pool.Token()
	if got == "" {
		t.Error("pool should still return tokens even when expired (refresh handles re-minting)")
	}
}

func TestPoolReplace(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("old-a", "old-b")

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

	if got := pool.Token(); got != "new-x" {
		t.Errorf("first after replace: got %q, want new-x", got)
	}

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
	pool.Exhaust("a", time.Time{})
	pool.Exhaust("b", time.Time{})
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
}

func TestPoolNeedsRefreshExpiringSoon(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "ok", Token: "a", ExpiresAt: time.Now().Add(time.Hour)},
		{Label: "expiring", Token: "b", ExpiresAt: time.Now().Add(3 * time.Minute)},
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

func TestCheckQuotasEmptyPool(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool()
	quotas := pool.CheckQuotas(context.Background())
	if len(quotas) != 0 {
		t.Errorf("expected 0 quotas, got %d", len(quotas))
	}
}

func TestInvalidateAuthInstallationBackoff(t *testing.T) {
	t.Parallel()
	pool := NewTokenPoolFromEntries([]PoolEntry{
		{InstallationID: 100, Label: "org-a", Token: "tok-a", ExpiresAt: time.Now().Add(time.Hour)},
		{InstallationID: 200, Label: "org-b", Token: "tok-b", ExpiresAt: time.Now().Add(time.Hour)},
	})

	pool.InvalidateAuth("tok-a")
	if pool.ActiveCount() != 1 {
		t.Fatalf("active: got %d, want 1", pool.ActiveCount())
	}
	for range 5 {
		if got := pool.Token(); got != "tok-b" {
			t.Errorf("got %q, want tok-b (invalidated token must not return)", got)
		}
	}

	events := pool.RecentInvalidations(time.Time{})
	if len(events) != 1 {
		t.Fatalf("events: got %d, want 1", len(events))
	}
	if events[0].Label != "org-a" || events[0].InstallationID != 100 || events[0].Permanent {
		t.Errorf("event: %+v", events[0])
	}
}

func TestInvalidateAuthPATPermanent(t *testing.T) {
	t.Parallel()
	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "PAT", Token: "pat"}, // installationID 0 → permanent
		{InstallationID: 100, Label: "org-a", Token: "tok-a"},
	})

	pool.InvalidateAuth("pat")
	if pool.ActiveCount() != 1 {
		t.Fatalf("active: got %d, want 1", pool.ActiveCount())
	}
	events := pool.RecentInvalidations(time.Time{})
	if len(events) != 1 || !events[0].Permanent {
		t.Fatalf("event should be permanent: %+v", events)
	}
}

func TestInvalidateAuthUnknownToken(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a")
	pool.InvalidateAuth("not-in-pool")
	if pool.ActiveCount() != 1 {
		t.Error("unknown token should not affect pool")
	}
	if len(pool.RecentInvalidations(time.Time{})) != 0 {
		t.Error("no event should be recorded for unknown token")
	}
}

func TestRecentInvalidationsSinceFilter(t *testing.T) {
	t.Parallel()
	pool := NewTokenPoolFromEntries([]PoolEntry{
		{InstallationID: 100, Label: "org-a", Token: "a"},
		{InstallationID: 200, Label: "org-b", Token: "b"},
	})
	pool.InvalidateAuth("a")
	mid := time.Now()
	time.Sleep(2 * time.Millisecond) // ensure second event sorts after `mid`
	pool.InvalidateAuth("b")

	all := pool.RecentInvalidations(time.Time{})
	if len(all) != 2 {
		t.Fatalf("all: got %d, want 2", len(all))
	}
	recent := pool.RecentInvalidations(mid)
	if len(recent) != 1 || recent[0].Label != "org-b" {
		t.Errorf("since filter: got %+v", recent)
	}
}

func TestRecentInvalidationsRingCap(t *testing.T) {
	t.Parallel()
	entries := make([]PoolEntry, invalidationRingCap+5)
	for i := range entries {
		entries[i] = PoolEntry{
			InstallationID: int64(i + 1),
			Label:          "org",
			Token:          string(rune('a'+i%26)) + string(rune('0'+i%10)) + string(rune(i%200)),
		}
	}
	pool := NewTokenPoolFromEntries(entries)
	for _, e := range entries {
		pool.InvalidateAuth(e.Token)
	}
	events := pool.RecentInvalidations(time.Time{})
	if len(events) != invalidationRingCap {
		t.Errorf("ring cap: got %d, want %d", len(events), invalidationRingCap)
	}
	// oldest entries should have rolled off — first surviving installationID
	// is 6 (5 entries dropped from the front).
	if events[0].InstallationID != 6 {
		t.Errorf("oldest surviving InstallationID = %d, want 6", events[0].InstallationID)
	}
}

func TestSignalRefreshNonBlocking(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a")
	ch := make(chan struct{}, 1)
	pool.SetRefreshCh(ch)

	pool.SignalRefresh()
	select {
	case <-ch:
		// expected
	default:
		t.Fatal("expected refresh signal")
	}

	// Fill the channel and signal twice more — must not block, must not
	// double-buffer.
	ch <- struct{}{}
	pool.SignalRefresh()
	pool.SignalRefresh()
	if len(ch) != 1 {
		t.Errorf("channel len = %d, want 1 (signals must coalesce)", len(ch))
	}
}

func TestSignalRefreshNilChannel(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a")
	// No SetRefreshCh — must not panic.
	pool.SignalRefresh()
}
