package background

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

const (
	defaultBatchSize   = 100
	defaultMinQuotaPct = 30
	defaultConcurrency = 3
	defaultLowDays     = 7
	defaultHighDays    = 30
	emptyQueueSleep    = 30 * time.Second
	resetJitter        = 30 * time.Second

	// defaultTokenReserve is how many pool tokens background scoring leaves
	// for interactive traffic. The search family refills only 30 req/min per
	// token, so without a reserve a batch can starve the API path.
	defaultTokenReserve = 1

	// tokenPauseFallback is how long to wait for the pool to recover when no
	// reset time is known.
	tokenPauseFallback = 30 * time.Second
)

// tokenPause reports how long the scorer should wait before its next batch.
// It pauses while spendable tokens are down to the reserve held for
// interactive traffic, sleeping until the pool actually recovers.
func tokenPause(active, reserve int, earliestReset, now time.Time) (time.Duration, bool) {
	if active > reserve {
		return 0, false
	}
	if wait := earliestReset.Sub(now); wait > 0 {
		return wait, true
	}
	// Reset unknown or already elapsed: wait a fixed window rather than
	// spinning the loop at full speed.
	return tokenPauseFallback, true
}

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
	EnqueueForScoring(ctx context.Context, username, provider string, priority int) error
	GetStaleContributors(ctx context.Context, lowDays, highDays, limit int) ([]postgres.StaleContributor, error)
	GetBehavioralSignals(ctx context.Context, username, provider string) (*model.Behavior, error)
	GetCachedSignals(ctx context.Context, username, provider string) (*score.InputSignals, error)
	UpsertContributor(ctx context.Context, username, provider string) error
	SaveScoreHistory(ctx context.Context, username, provider string, value float64, grade string, deep bool) error
	UpdateReputation(ctx context.Context, username, provider string, value float64, grade, version string, deep bool, signals *score.InputSignals) error
	QueueDepth(ctx context.Context) (int, error)
	GetCurrentGrade(ctx context.Context, username, provider string) (string, error)
	GetWatchlistsForContributor(ctx context.Context, username, provider string) ([]postgres.WatchlistEntry, error)
	InsertNotificationEvent(ctx context.Context, watchlistID, eventType, username string, details map[string]any) error
	BumpScoredAt(ctx context.Context, username, provider string) error
}

// quotaChecker abstracts quota checking for testing.
type quotaChecker interface {
	CheckQuotas(ctx context.Context) []ghclient.TokenQuota
}

// poolState reports live token availability. Where quotaChecker samples
// GitHub's counters over HTTP — too coarse for the search family, which
// refills every 60s and is drained by a single batch in seconds — this is
// the pool's own record of which tokens are spendable right now.
type poolState interface {
	ActiveCount() int
	Size() int
	EarliestReset() time.Time
}

// StartBackgroundScorer runs a continuous scoring loop that pauses when
// aggregate token quota drops below the configured threshold.
// Returns a cancel function to stop the loop.
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client, version string) func() {
	batchSize := config.GetEnvAsInt("SCORER_BATCH_SIZE", defaultBatchSize)
	minQuotaPct := config.GetEnvAsInt("SCORER_MIN_QUOTA_PCT", defaultMinQuotaPct)
	concurrency := config.GetEnvAsInt("SCORER_CONCURRENCY", defaultConcurrency)
	tokenReserve := config.GetEnvAsInt("SCORER_TOKEN_RESERVE", defaultTokenReserve)

	slog.Info("starting continuous scorer",
		"batch_size", batchSize,
		"min_quota_pct", minQuotaPct,
		"concurrency", concurrency,
		"token_reserve", tokenReserve,
	)

	ctx, cancel := context.WithCancel(ctx)

	var qc quotaChecker
	var pool poolState
	if pc, ok := gh.(*ghclient.PoolClient); ok {
		qc = pc.Pool()
		pool = pc.Pool()
	}

	go runContinuousScorer(ctx, store, gh, qc, pool, version, batchSize, minQuotaPct, concurrency, tokenReserve)

	return cancel
}

func runContinuousScorer(ctx context.Context, store scorerStore, gh ghclient.Client,
	qc quotaChecker, pool poolState, version string, batchSize, minQuotaPct, concurrency, tokenReserve int) {
	stats := &scorerStats{}

	// Stats reporter runs in its own goroutine so reports fire on a real
	// schedule even when the main loop is mid-batch or in a sleepCtx.
	// (The previous select-with-default pattern silently dropped ticks.)
	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		statsTicker := time.NewTicker(5 * time.Minute)
		defer statsTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-statsTicker.C:
				logScorerStats(ctx, store, stats)
			}
		}
	}()
	defer func() { <-reporterDone }()

	for {
		if ctx.Err() != nil {
			slog.Info("continuous scorer stopped",
				"total_scored", stats.totalScored.Load(),
				"total_errors", stats.totalErrors.Load(),
			)
			return
		}

		// Live pool state first: it costs no API calls and, unlike the
		// sampled check below, it sees the search family's 60s window —
		// which a single batch drains in seconds, long before a sample
		// would ever catch it depleted.
		if pool != nil {
			active := pool.ActiveCount()
			if wait, pause := tokenPause(active, tokenReserve, pool.EarliestReset(), time.Now()); pause {
				logScorerStats(ctx, store, stats)
				slog.Warn("scorer paused: token pool at reserve",
					"active", active,
					"pool_size", pool.Size(),
					"reserve", tokenReserve,
					"resume_in", wait,
				)
				sleepCtx(ctx, wait+jitter())
				continue
			}
		}

		// Check quota before each batch. We gate on whichever family is
		// most depleted: core (REST), search, or GraphQL. Each scoring
		// run burns ~3 search calls and 1 GraphQL call per contributor,
		// so search exhausts long before core under load.
		if qc != nil {
			quotas := qc.CheckQuotas(ctx)
			corePct, coreReset := ghclient.AggregateQuota(quotas)
			searchPct, searchReset := ghclient.AggregateSearchQuota(quotas)
			graphqlPct, graphqlReset := ghclient.AggregateGraphQLQuota(quotas)

			lowest, family, reset := corePct, "core", coreReset
			if searchPct < lowest {
				lowest, family, reset = searchPct, "search", searchReset
			}
			if graphqlPct < lowest {
				lowest, family, reset = graphqlPct, "graphql", graphqlReset
			}

			if lowest < minQuotaPct {
				logScorerStats(ctx, store, stats)
				wait := max(time.Until(reset)+jitter(), time.Minute)
				slog.Warn("scorer quota paused",
					"family", family,
					"aggregate_pct", lowest,
					"core_pct", corePct,
					"search_pct", searchPct,
					"graphql_pct", graphqlPct,
					"threshold_pct", minQuotaPct,
					"resume_in", wait,
				)
				sleepCtx(ctx, wait)
				continue
			}
		}

		// Phase 1: Drain scoring queue.
		scored := drainQueue(ctx, store, gh, stats, version, batchSize, concurrency)

		// Phase 2: Rescore stale contributors if queue was empty.
		if scored == 0 {
			staleScored := rescoreStale(ctx, store, gh, stats, version, batchSize, concurrency)
			if staleScored == 0 {
				sleepCtx(ctx, emptyQueueSleep)
			}
		}
	}
}

func logScorerStats(ctx context.Context, store scorerStore, stats *scorerStats) {
	windowScored, windowErrors, windowHints := stats.window()
	stats.resetWindow()

	depth := -1
	if d, err := store.QueueDepth(ctx); err == nil {
		depth = d
	}

	slog.Info("scorer stats",
		"total_scored", stats.totalScored.Load(),
		"window_scored", windowScored,
		"window_errors", windowErrors,
		"window_with_hints", windowHints,
		"queue_depth", depth,
	)
}

func drainQueue(ctx context.Context, store scorerStore, gh ghclient.Client,
	stats *scorerStats, version string, batchSize, concurrency int) int {
	queued, err := store.DequeueForScoring(ctx, batchSize)
	if err != nil {
		slog.Error("dequeue for scoring", "error", err)
		return 0
	}
	if len(queued) == 0 {
		return 0
	}

	start := time.Now()
	var scored, errCount, skipped, hints atomic.Int32
	var dry atomic.Bool
	// Each goroutine writes only its own index, so no synchronization is
	// needed to decide afterwards which entries still owe a score.
	settled := make([]bool, len(queued))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, q := range queued {
		// Explicit pre-check before the select: when the ctx is already
		// canceled, Go's select non-deterministically picks between
		// ctx.Done() and a ready sem send, so spawning would be a coin
		// flip. The pre-check makes cancellation deterministic.
		if ctx.Err() != nil {
			break
		}
		// The pool is dry: every remaining entry would fail identically.
		// Stop and let the caller wait for the rate-limit window to reset.
		if dry.Load() {
			break
		}
		select {
		case <-ctx.Done():
			break
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			hasHints := false
			if beh, berr := store.GetBehavioralSignals(ctx, q.Username, q.Provider); berr == nil && beh != nil {
				hasHints = true
			}

			if serr := scoreContributor(ctx, store, gh, q.Username, q.Provider, version); serr != nil {
				if errors.Is(serr, ghclient.ErrNoTokens) {
					dry.Store(true)
					errCount.Add(1)
					return
				}
				if isTerminalError(serr) {
					slog.Debug("skipped terminal error", "username", q.Username, "error", serr)
					skipped.Add(1)
				} else {
					slog.Warn("score queued", "username", q.Username, "priority", q.Priority, "error", serr)
					errCount.Add(1)
				}
				settled[i] = true
				return
			}
			settled[i] = true
			scored.Add(1)
			if hasHints {
				hints.Add(1)
			}
		}()
	}
	wg.Wait()

	// DequeueForScoring consumed these rows, so anything left unscored by a
	// dry pool has to go back on the queue or the work is lost.
	if dry.Load() {
		requeueUnscored(ctx, store, queued, settled)
	}

	s, e, sk, h := int(scored.Load()), int(errCount.Load()), int(skipped.Load()), int(hints.Load())
	if stats != nil {
		stats.record(s, e, h)
	}
	if s > 0 || sk > 0 {
		slog.Info("queue scoring complete",
			"scored", s,
			"errors", e,
			"skipped", sk,
			"with_hints", h,
			"total", len(queued),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
	}
	return s
}

// requeueUnscored puts back work a dry pool prevented us from scoring.
// settled[i] marks entries that reached a decision (scored, skipped, or a
// real error) and so must not be re-queued.
func requeueUnscored(ctx context.Context, store scorerStore, queued []postgres.QueueEntry, settled []bool) {
	requeued := 0
	for i, q := range queued {
		if settled[i] {
			continue
		}
		if err := store.EnqueueForScoring(ctx, q.Username, q.Provider, q.Priority); err != nil {
			slog.Warn("requeue after dry pool", "username", q.Username, "error", err)
			continue
		}
		requeued++
	}
	slog.Warn("queue batch aborted: no available GitHub tokens",
		"requeued", requeued,
		"total", len(queued),
	)
}

func rescoreStale(ctx context.Context, store scorerStore, gh ghclient.Client,
	stats *scorerStats, version string, batchSize, concurrency int) int {
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

	start := time.Now()
	var scored, errCount, tombstoned atomic.Int32
	var dry atomic.Bool
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, c := range stale {
		if ctx.Err() != nil {
			break
		}
		// Stale contributors are re-read every pass, so abandoning the rest
		// of the batch loses nothing: they stay stale and are picked up once
		// the pool recovers.
		if dry.Load() {
			break
		}
		select {
		case <-ctx.Done():
			break
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if serr := scoreContributor(ctx, store, gh, c.Username, c.Provider, version); serr != nil {
				if errors.Is(serr, ghclient.ErrNoTokens) {
					dry.Store(true)
					errCount.Add(1)
					return
				}
				if isTerminalError(serr) {
					if berr := store.BumpScoredAt(ctx, c.Username, c.Provider); berr != nil {
						slog.Warn("bump scored_at", "username", c.Username, "error", berr)
					}
					tombstoned.Add(1)
					slog.Info("stale contributor tombstoned", "username", c.Username, "error", serr)
					return
				}
				slog.Warn("score stale", "username", c.Username, "error", serr)
				errCount.Add(1)
				return
			}
			scored.Add(1)
		}()
	}
	wg.Wait()

	s, e, tb := int(scored.Load()), int(errCount.Load()), int(tombstoned.Load())
	if stats != nil {
		stats.record(s, e, 0)
	}
	slog.Info("stale rescoring complete",
		"scored", s,
		"errors", e,
		"tombstoned", tb,
		"total", len(stale),
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
	return s
}

// isTerminalError returns true for errors that will never succeed on retry.
// Uses typed github.ErrorResponse when available, with string fallback.
func isTerminalError(err error) bool {
	if err == nil {
		return false
	}
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		switch ghErr.Response.StatusCode {
		case http.StatusNotFound, // 404: user deleted or renamed
			http.StatusUnavailableForLegalReasons, // 451: DMCA block
			http.StatusUnprocessableEntity:        // 422: invalid user
			return true
		}
	}
	return false
}

// scoreContributor fetches signals, computes a score, and persists the result.
// After scoring, detects grade changes and writes watchlist notification events.
func scoreContributor(ctx context.Context, store scorerStore, gh ghclient.Client,
	username, provider, version string) error {
	// Capture old grade before scoring for change detection.
	oldGrade, _ := store.GetCurrentGrade(ctx, username, provider)

	var behavior *model.Behavior
	var hints *ghclient.ArchiveHints
	if beh, err := store.GetBehavioralSignals(ctx, username, provider); err == nil && beh != nil {
		behavior = beh

		merged := int64(beh.TotalPRsMerged)
		closed := int64(beh.TotalPRsClosed)
		recentRepos := int64(beh.DistinctRepos90d)

		// Use previously cached reputation signals as a floor so that
		// contributors scored before the archive ingest keep their
		// correct PR counts even when archive coverage is incomplete.
		if cached, cerr := store.GetCachedSignals(ctx, username, provider); cerr == nil && cached != nil {
			merged = max(merged, cached.PRsMerged)
			closed = max(closed, cached.PRsClosed)
			recentRepos = max(recentRepos, cached.RecentPRRepoCount)
		}

		// Background scorer always trusts hints to avoid burning
		// the scarce Search API quota (30 req/min). The interactive path
		// in service/score.go uses the ActiveDays threshold instead.
		hints = &ghclient.ArchiveHints{
			PRsMerged:         merged,
			PRsClosed:         closed,
			RecentPRRepoCount: recentRepos,
			Trusted:           true,
		}
	}

	signals, err := gh.FetchSignals(ctx, username, "", hints)
	if err != nil {
		return fmt.Errorf("fetch signals: %w", err)
	}

	value := score.Compute(*signals, false, behavior)
	grade := score.Grade(value)

	// Partial fetches mean one or more sub-calls (search/contrib-stats/
	// repos) failed — the score is best-effort. Persist with deep=false
	// so the next ingest tick sees a stale row and re-scores when the
	// upstream is healthy again.
	deep := !signals.Partial

	if err := store.UpsertContributor(ctx, username, provider); err != nil {
		slog.Warn("upsert contributor", "username", username, "error", err)
	}

	if err := store.SaveScoreHistory(ctx, username, provider, value, grade, deep); err != nil {
		slog.Warn("save history", "username", username, "error", err)
	}

	if err := store.UpdateReputation(ctx, username, provider, value, grade, version, deep, signals); err != nil {
		return fmt.Errorf("update reputation: %w", err)
	}

	// Detect grade change and notify matching watchlists.
	if oldGrade != "" && oldGrade != grade {
		notifyGradeChange(ctx, store, username, provider, oldGrade, grade)
	}

	return nil
}

// notifyGradeChange writes score_change notification events for all watchlists
// that match the contributor's recent activity repos.
func notifyGradeChange(ctx context.Context, store scorerStore, username, provider, oldGrade, newGrade string) {
	watchlists, err := store.GetWatchlistsForContributor(ctx, username, provider)
	if err != nil {
		slog.Debug("get watchlists for grade change", "username", username, "error", err)
		return
	}
	details := map[string]any{
		"old_grade": oldGrade,
		"new_grade": newGrade,
	}
	for _, wl := range watchlists {
		if err := store.InsertNotificationEvent(ctx, wl.ID, postgres.EventTypeScoreChange, username, details); err != nil {
			slog.Debug("write grade change event", "username", username, "watchlist", wl.ID, "error", err)
		}
	}
	if len(watchlists) > 0 {
		slog.Info("grade change notified", "username", username, "old", oldGrade, "new", newGrade, "watchlists", len(watchlists))
	}
}

func jitter() time.Duration {
	return time.Duration(rand.IntN(int(resetJitter.Seconds()))) * time.Second //nolint:gosec // jitter, not security-sensitive
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
