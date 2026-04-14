package background

import (
	"context"
	"fmt"
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

// scorerStore defines the store operations needed by the background scorer.
type scorerStore interface {
	DequeueForScoring(ctx context.Context, limit int) ([]postgres.QueueEntry, error)
	RemoveFromQueue(ctx context.Context, username, provider string) error
	GetStaleContributors(ctx context.Context, lowDays, highDays, limit int) ([]postgres.StaleContributor, error)
	UpsertContributor(ctx context.Context, username, provider string) error
	SaveScoreHistory(ctx context.Context, username, provider string, value float64, grade string, deep bool) error
	UpdateReputation(ctx context.Context, username, provider string, value float64, grade, version string, signals *score.InputSignals) error
}

// StartBackgroundScorer rescores stale contributors on a schedule.
// Returns a cancel function to stop the loop.
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client, version string) func() {
	interval := time.Duration(config.GetEnvAsInt("SCORER_INTERVAL_SEC", defaultScorerSec)) * time.Second
	slog.Info("starting background scorer", "interval", interval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		// Run once immediately to drain any pending queue.
		runScorer(ctx, store, gh, version)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("background scorer stopped")
				return
			case <-ticker.C:
				runScorer(ctx, store, gh, version)
			}
		}
	}()

	return cancel
}

func runScorer(ctx context.Context, store scorerStore, gh ghclient.Client, version string) {
	// Phase 1: Drain scoring queue (P1 → P2 → P3).
	drainQueue(ctx, store, gh, version)

	// Phase 2: Rescore stale contributors.
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
		if ctx.Err() != nil {
			return
		}
		if err := scoreContributor(ctx, store, gh, c.Username, c.Provider, version); err != nil {
			slog.Warn("score stale", "username", c.Username, "error", err)
			continue
		}
		scored++
	}

	slog.Info("background scorer complete", "scored", scored, "total", len(stale))
}

func drainQueue(ctx context.Context, store scorerStore, gh ghclient.Client, version string) {
	queued, err := store.DequeueForScoring(ctx, scorerBatchSize)
	if err != nil {
		slog.Error("dequeue for scoring", "error", err)
		return
	}
	if len(queued) == 0 {
		return
	}

	var scored int
	for _, q := range queued {
		if ctx.Err() != nil {
			return
		}
		if err := scoreContributor(ctx, store, gh, q.Username, q.Provider, version); err != nil {
			slog.Warn("score queued", "username", q.Username, "priority", q.Priority, "error", err)
			continue
		}
		_ = store.RemoveFromQueue(ctx, q.Username, q.Provider)
		scored++
	}
	slog.Info("queue scoring complete", "scored", scored, "total", len(queued))
}

// scoreContributor fetches signals, computes a score, and persists the result.
func scoreContributor(ctx context.Context, store scorerStore, gh ghclient.Client,
	username, provider, version string) error {
	signals, err := gh.FetchSignals(ctx, username, "", nil)
	if err != nil {
		return fmt.Errorf("fetch signals: %w", err)
	}

	value := score.Compute(*signals, false) // background scoring has no repo context
	grade := score.Grade(value)

	_ = store.UpsertContributor(ctx, username, provider)

	if err := store.SaveScoreHistory(ctx, username, provider, value, grade, true); err != nil {
		slog.Warn("save history", "username", username, "error", err)
	}

	return store.UpdateReputation(ctx, username, provider, value, grade, version, signals)
}
