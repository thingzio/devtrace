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
type RefreshFunc func(ctx context.Context) ([]PoolEntry, error)

// StartPoolRefresh runs a background goroutine that refreshes the token pool
// when tokens are near expiry or when signaled via notifyCh.
// Pass tickInterval=0 to use the default (1 minute).
// Returns a stop function that cancels the goroutine and waits for it to exit.
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

	// Handle nil notifyCh by using a channel that never receives.
	if notifyCh == nil {
		notifyCh = make(chan struct{})
	}

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
