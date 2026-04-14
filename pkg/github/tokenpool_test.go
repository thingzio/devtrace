package github

import (
	"sync"
	"testing"
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
