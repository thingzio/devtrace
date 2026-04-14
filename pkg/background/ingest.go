package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
)

const defaultIngestSec = 600 // 10 minutes

// IngestFunc is the function signature for a single ingest run.
type IngestFunc func(ctx context.Context) error

// StartIngestLoop runs the given function immediately, then on a ticker interval.
// If interval is 0, it reads INGEST_INTERVAL_SEC (default 600s).
// Returns a cancel function to stop the loop.
func StartIngestLoop(ctx context.Context, run IngestFunc, interval time.Duration) func() {
	if interval == 0 {
		interval = time.Duration(config.GetEnvAsInt("INGEST_INTERVAL_SEC", defaultIngestSec)) * time.Second
	}
	slog.Info("starting ingest loop", "interval", interval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		// Run immediately on startup.
		if err := run(ctx); err != nil {
			slog.Error("ingest run failed", "error", err)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("ingest loop stopped")
				return
			case <-ticker.C:
				if err := run(ctx); err != nil {
					slog.Error("ingest run failed", "error", err)
				}
			}
		}
	}()

	return cancel
}
