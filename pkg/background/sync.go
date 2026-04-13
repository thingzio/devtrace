package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/score"
)

const (
	syncStateKey   = "devpulse_sync"
	defaultSyncSec = 1800 // 30 minutes
	syncBatchSize  = 500
)

// StartDevPulseSync runs a background loop that copies developer scores
// from DevPulse's developer table into DevTrace's tables.
// Returns a cancel function to stop the loop.
func StartDevPulseSync(ctx context.Context, store *postgres.Store) func() {
	interval := time.Duration(config.GetEnvAsInt("DEVPULSE_SYNC_INTERVAL_SEC", defaultSyncSec)) * time.Second
	slog.Info("starting devpulse sync", "interval", interval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		runSync(ctx, store)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("devpulse sync stopped")
				return
			case <-ticker.C:
				runSync(ctx, store)
			}
		}
	}()

	return cancel
}

func runSync(ctx context.Context, store *postgres.Store) {
	since, err := store.GetSyncState(ctx, syncStateKey)
	if err != nil {
		slog.Error("get sync state", "error", err)
		return
	}

	slog.Debug("running devpulse sync", "since", since)

	devs, err := store.GetDevPulseUpdatedDevelopers(ctx, since, syncBatchSize)
	if err != nil {
		slog.Error("fetch devpulse developers", "error", err)
		return
	}

	if len(devs) == 0 {
		slog.Debug("devpulse sync: no updates")
		return
	}

	var synced int
	var lastUpdated time.Time

	for _, d := range devs {
		grade := score.Grade(d.Reputation)
		if err := store.SyncDeveloperToDevTrace(ctx, d, grade); err != nil {
			slog.Error("sync developer", "username", d.Username, "error", err)
			continue
		}
		synced++
		if d.UpdatedAt.After(lastUpdated) {
			lastUpdated = d.UpdatedAt
		}
	}

	if !lastUpdated.IsZero() {
		if err := store.SaveSyncState(ctx, syncStateKey, lastUpdated); err != nil {
			slog.Error("save sync state", "error", err)
		}
	}

	slog.Info("devpulse sync complete", "synced", synced, "total", len(devs))
}
