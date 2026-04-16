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
	pool.Exhaust("b")
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
	pool.Exhaust("a")
	pool.Exhaust("b")
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
	pool.Exhaust("b")
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
	pool.Exhaust("unknown") // should not panic
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

func TestPoolEntryExpiry(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "expired", Token: "tok-old", ExpiresAt: time.Now().Add(2 * time.Minute)},
		{Label: "fresh", Token: "tok-new", ExpiresAt: time.Now().Add(time.Hour)},
	}
	pool := NewTokenPoolFromEntries(entries)
	got := pool.Token()
	if got != "tok-new" {
		t.Errorf("should skip near-expiry token: got %q, want tok-new", got)
	}
}

func TestPoolMixedPATAndInstallation(t *testing.T) {
	t.Parallel()
	entries := []PoolEntry{
		{Label: "org-a", Token: "inst-tok", ExpiresAt: time.Now().Add(2 * time.Minute)},
		{Label: "PAT", Token: "pat-tok"},
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

func TestCheckQuotasEmptyPool(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool()
	quotas := pool.CheckQuotas(context.Background())
	if len(quotas) != 0 {
		t.Errorf("expected 0 quotas, got %d", len(quotas))
	}
}
