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

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(2 * time.Minute)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

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
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(time.Hour)},
	})

	notifyCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, notifyCh, time.Hour)
	defer stop()

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

	notifyCh <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	notifyCh <- struct{}{}
	time.Sleep(100 * time.Millisecond)

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
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(2 * time.Minute)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

	time.Sleep(200 * time.Millisecond)

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
	if pool.Size() != 1 {
		t.Errorf("should keep old entries: size=%d", pool.Size())
	}
}

func TestRefreshEmptyResult(t *testing.T) {
	t.Parallel()
	refreshFn := func(_ context.Context) ([]PoolEntry, error) {
		return nil, nil
	}

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{Label: "old", Token: "old-tok", ExpiresAt: time.Now().Add(time.Minute)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartPoolRefresh(ctx, pool, refreshFn, nil, 50*time.Millisecond)
	defer stop()

	time.Sleep(200 * time.Millisecond)

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
