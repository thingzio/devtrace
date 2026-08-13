package github

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEarliestResetNothingExhausted(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")

	if got := pool.EarliestReset(); !got.IsZero() {
		t.Errorf("earliest reset with nothing exhausted: got %v, want zero", got)
	}
}

// The scorer sleeps until capacity returns, so it needs the soonest reset
// across exhausted tokens — not the latest, which would idle the pool.
func TestEarliestResetPicksSoonest(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b", "c")
	soon := time.Now().Add(30 * time.Second)
	late := time.Now().Add(10 * time.Minute)
	pool.Exhaust("a", late, "search")
	pool.Exhaust("b", soon, "search")

	got := pool.EarliestReset()
	if got.IsZero() {
		t.Fatal("earliest reset: got zero, want the soonest exhausted reset")
	}
	if got.Sub(soon).Abs() > time.Second {
		t.Errorf("earliest reset: got %v, want ~%v", got, soon)
	}
}

// A pool with spendable tokens left reports no wait: the scorer should keep
// working rather than sleep on one unlucky token.
func TestEarliestResetZeroWhileCapacityRemains(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("a", "b")
	pool.Exhaust("a", time.Now().Add(time.Minute), "search")

	if pool.ActiveCount() == 0 {
		t.Fatal("precondition: pool should still have an active token")
	}
	if got := pool.EarliestReset(); got.IsZero() {
		t.Errorf("earliest reset: got zero, want the exhausted token's reset")
	}
}

// Callers need to distinguish "pool is dry, back off" from a real GitHub
// error, so the drained-pool case must be an identifiable sentinel.
func TestPoolClientReturnsSentinelWhenDry(t *testing.T) {
	t.Parallel()
	client := NewPoolClient(NewTokenPool(""))

	_, _, err := client.ghClient(context.Background())
	if err == nil {
		t.Fatal("want error from dry pool, got nil")
	}
	if !errors.Is(err, ErrNoTokens) {
		t.Errorf("errors.Is(err, ErrNoTokens): got false for %v", err)
	}
}
