package background

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

const (
	defaultBatchSize   = 100
	defaultMinQuotaPct = 30
	defaultLowDays     = 7
	defaultHighDays    = 30
	emptyQueueSleep    = 30 * time.Second
	resetJitter        = 30 * time.Second
)

// scorerStats tracks scoring throughput for periodic logging.
type scorerStats struct {
	totalScored    atomic.Int64
	totalErrors    atomic.Int64
	totalWithHints atomic.Int64
	windowScored   atomic.Int64
	windowErrors   atomic.Int64
	windowHints    atomic.Int64
}

func (s *scorerStats) record(scored, errors, withHints int) {
	s.totalScored.Add(int64(scored))
	s.totalErrors.Add(int64(errors))
	s.totalWithHints.Add(int64(withHints))
	s.windowScored.Add(int64(scored))
	s.windowErrors.Add(int64(errors))
	s.windowHints.Add(int64(withHints))
}

func (s *scorerStats) window() (scored, errors, hints int64) {
	return s.windowScored.Load(), s.windowErrors.Load(), s.windowHints.Load()
}

func (s *scorerStats) resetWindow() {
	s.windowScored.Store(0)
	s.windowErrors.Store(0)
	s.windowHints.Store(0)
}

// scorerStore defines the store operations needed by the background scorer.
type scorerStore interface {
	DequeueForScoring(ctx context.Context, limit int) ([]postgres.QueueEntry, error)
	RemoveFromQueue(ctx context.Context, username, provider string) error
	GetStaleContributors(ctx context.Context, lowDays, highDays, limit int) ([]postgres.StaleContributor, error)
	GetBehavioralSignals(ctx context.Context, username, provider string) (*model.Behavior, error)
	UpsertContributor(ctx context.Context, username, provider string) error
	SaveScoreHistory(ctx context.Context, username, provider string, value float64, grade string, deep bool) error
	UpdateReputation(ctx context.Context, username, provider string, value float64, grade, version string, signals *score.InputSignals) error
	QueueDepth(ctx context.Context) (int, error)
}

// quotaChecker abstracts quota checking for testing.
type quotaChecker interface {
	CheckQuotas(ctx context.Context) []ghclient.TokenQuota
}

// StartBackgroundScorer runs a continuous scoring loop that pauses when
// aggregate token quota drops below the configured threshold.
// Returns a cancel function to stop the loop.
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client, version string) func() {
	batchSize := config.GetEnvAsInt("SCORER_BATCH_SIZE", defaultBatchSize)
	minQuotaPct := config.GetEnvAsInt("SCORER_MIN_QUOTA_PCT", defaultMinQuotaPct)

	slog.Info("starting continuous scorer",
		"batch_size", batchSize,
		"min_quota_pct", minQuotaPct,
	)

	ctx, cancel := context.WithCancel(ctx)

	var qc quotaChecker
	if pc, ok := gh.(*ghclient.PoolClient); ok {
		qc = pc.Pool()
	}

	go runContinuousScorer(ctx, store, gh, qc, version, batchSize, minQuotaPct)

	return cancel
}

func runContinuousScorer(ctx context.Context, store scorerStore, gh ghclient.Client,
	qc quotaChecker, version string, batchSize, minQuotaPct int) {
	for {
		if ctx.Err() != nil {
			slog.Info("continuous scorer stopped")
			return
		}

		// Check quota before each batch.
		if qc != nil {
			quotas := qc.CheckQuotas(ctx)
			pct, earliestReset := ghclient.AggregateQuota(quotas)
			if pct < minQuotaPct {
				wait := max(time.Until(earliestReset)+jitter(), time.Minute)
				slog.Warn("scorer pausing: quota below threshold",
					"aggregate_pct", pct,
					"threshold_pct", minQuotaPct,
					"resume_in", wait,
				)
				sleepCtx(ctx, wait)
				continue
			}
		}

		// Phase 1: Drain scoring queue.
		scored := drainQueue(ctx, store, gh, version, batchSize)

		// Phase 2: Rescore stale contributors if queue was empty.
		if scored == 0 {
			staleScored := rescoreStale(ctx, store, gh, version, batchSize)
			if staleScored == 0 {
				sleepCtx(ctx, emptyQueueSleep)
			}
		}
	}
}

func drainQueue(ctx context.Context, store scorerStore, gh ghclient.Client, version string, batchSize int) int {
	queued, err := store.DequeueForScoring(ctx, batchSize)
	if err != nil {
		slog.Error("dequeue for scoring", "error", err)
		return 0
	}
	if len(queued) == 0 {
		return 0
	}

	var scored int
	for _, q := range queued {
		if ctx.Err() != nil {
			return scored
		}
		if err := scoreContributor(ctx, store, gh, q.Username, q.Provider, version); err != nil {
			slog.Warn("score queued", "username", q.Username, "priority", q.Priority, "error", err)
			continue
		}
		_ = store.RemoveFromQueue(ctx, q.Username, q.Provider)
		scored++
	}
	if scored > 0 {
		slog.Info("queue scoring complete", "scored", scored, "total", len(queued))
	}
	return scored
}

func rescoreStale(ctx context.Context, store scorerStore, gh ghclient.Client, version string, batchSize int) int {
	lowDays := config.GetEnvAsInt("SCORER_LOW_STALE_DAYS", defaultLowDays)
	highDays := config.GetEnvAsInt("SCORER_HIGH_STALE_DAYS", defaultHighDays)

	stale, err := store.GetStaleContributors(ctx, lowDays, highDays, batchSize)
	if err != nil {
		slog.Error("fetch stale contributors", "error", err)
		return 0
	}
	if len(stale) == 0 {
		return 0
	}

	var scored int
	for _, c := range stale {
		if ctx.Err() != nil {
			return scored
		}
		if err := scoreContributor(ctx, store, gh, c.Username, c.Provider, version); err != nil {
			slog.Warn("score stale", "username", c.Username, "error", err)
			continue
		}
		scored++
	}
	slog.Info("stale rescoring complete", "scored", scored, "total", len(stale))
	return scored
}

// scoreContributor fetches signals, computes a score, and persists the result.
func scoreContributor(ctx context.Context, store scorerStore, gh ghclient.Client,
	username, provider, version string) error {
	var behavior *model.Behavior
	var hints *ghclient.ArchiveHints
	if beh, err := store.GetBehavioralSignals(ctx, username, provider); err == nil && beh != nil {
		behavior = beh
		hints = &ghclient.ArchiveHints{
			PRsMerged:         int64(beh.TotalPRsMerged),
			PRsClosed:         int64(beh.TotalPRsClosed),
			RecentPRRepoCount: int64(beh.DistinctRepos90d),
		}
	}

	signals, err := gh.FetchSignals(ctx, username, "", hints)
	if err != nil {
		return fmt.Errorf("fetch signals: %w", err)
	}

	value := score.Compute(*signals, false, behavior)
	grade := score.Grade(value)

	_ = store.UpsertContributor(ctx, username, provider)

	if err := store.SaveScoreHistory(ctx, username, provider, value, grade, true); err != nil {
		slog.Warn("save history", "username", username, "error", err)
	}

	return store.UpdateReputation(ctx, username, provider, value, grade, version, signals)
}

func jitter() time.Duration {
	return time.Duration(rand.IntN(int(resetJitter.Seconds()))) * time.Second //nolint:gosec // jitter, not security-sensitive
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
