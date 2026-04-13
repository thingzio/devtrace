package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/score"
)

const (
	defaultScorerSec = 3600 // 1 hour
	scorerBatchSize  = 100
	defaultLowDays   = 7
	defaultHighDays  = 30
)

// StartBackgroundScorer rescores stale contributors on a schedule.
// Returns a cancel function to stop the loop.
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client) func() {
	interval := time.Duration(config.GetEnvAsInt("SCORER_INTERVAL_SEC", defaultScorerSec)) * time.Second
	slog.Info("starting background scorer", "interval", interval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		// Don't run immediately — let sync populate first.
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("background scorer stopped")
				return
			case <-ticker.C:
				runScorer(ctx, store, gh)
			}
		}
	}()

	return cancel
}

func runScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client) {
	lowDays := config.GetEnvAsInt("SCORER_LOW_STALE_DAYS", defaultLowDays)
	highDays := config.GetEnvAsInt("SCORER_HIGH_STALE_DAYS", defaultHighDays)

	stale, err := store.GetStaleContributors(ctx, lowDays, highDays, scorerBatchSize)
	if err != nil {
		slog.Error("fetch stale contributors", "error", err)
		return
	}

	if len(stale) == 0 {
		slog.Debug("background scorer: no stale contributors")
		return
	}

	var scored int
	for _, c := range stale {
		signals, err := gh.FetchSignals(ctx, c.Username, "")
		if err != nil {
			slog.Warn("scorer fetch signals", "username", c.Username, "error", err)
			continue
		}

		value := score.Compute(*signals)
		grade := score.Grade(value)

		if err := store.SaveScoreHistory(ctx, c.Username, c.Provider, value, grade, true); err != nil {
			slog.Warn("scorer save history", "username", c.Username, "error", err)
		}

		if err := store.UpdateReputation(ctx, c.Username, c.Provider, value, grade, signals); err != nil {
			slog.Warn("scorer update reputation", "username", c.Username, "error", err)
			continue
		}

		scored++
	}

	slog.Info("background scorer complete", "scored", scored, "total", len(stale))
}
