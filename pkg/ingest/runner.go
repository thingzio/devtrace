package ingest

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
)

const (
	syncStateKey     = "gharchive_cursor"
	compactStateKey  = "activity_compacted"
	compactInterval  = 24 * time.Hour
	compactOlderThan = 30 * 24 * time.Hour // aggregate rows older than 30 days

	pruneActivityRetention = 120 * 24 * time.Hour // delete activity older than 120 days
	pruneHistoryRetention  = 400 * 24 * time.Hour // delete score history older than 400 days
)

// ingestStore defines the store operations needed by the ingest runner.
type ingestStore interface {
	GetSyncState(ctx context.Context, key string) (time.Time, error)
	SaveSyncState(ctx context.Context, key string, val time.Time) error
	GetTenantRepos(ctx context.Context) (map[string]bool, error)
	BatchUpsertActivity(ctx context.Context, summaries []postgres.HourlySummary) (int, error)
	EnqueueForScoring(ctx context.Context, username, provider string, priority int) error
	ContributorExists(ctx context.Context, username, provider string) (bool, error)
	PurgeNonTenantQueue(ctx context.Context) (int64, error)
	CompactActivity(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneActivity(ctx context.Context, retention time.Duration) (int64, error)
	PruneScoreHistory(ctx context.Context, retention time.Duration) (int64, error)
}

// Run processes one or more hourly GH Archive dumps.
func Run(ctx context.Context, store *postgres.Store) error {
	baseURL := config.GetEnv("GHARCHIVE_BASE_URL", "")
	reader := NewArchiveReader(baseURL)

	cursor, _ := store.GetSyncState(ctx, syncStateKey)
	lookback := config.GetEnvAsInt("GHARCHIVE_LOOKBACK_HOURS", 1)
	catchupMax := config.GetEnvAsInt("GHARCHIVE_CATCHUP_MAX_HOURS", 24)
	hours := computeHours(cursor, lookback, catchupMax)

	if len(hours) == 0 {
		slog.Info("no hours to process")
		return nil
	}

	tenantRepos, _ := store.GetTenantRepos(ctx)
	slog.Info("ingest starting", "hours", len(hours), "tenant_orgs", len(tenantRepos))

	for _, hour := range hours {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := processHour(ctx, store, reader, hour, tenantRepos); err != nil {
			slog.Error("process hour failed", "hour", hour, "error", err)
			continue
		}
		if err := store.SaveSyncState(ctx, syncStateKey, hour); err != nil {
			slog.Error("save cursor", "error", err)
		}
	}

	// Compaction: aggregate old hourly rows into weekly buckets.
	maybeCompact(ctx, store)

	return nil
}

// maybeCompact runs activity compaction if it hasn't run in the last 24 hours.
// Errors are logged but do not fail the ingest run.
func maybeCompact(ctx context.Context, store ingestStore) {
	lastCompact, _ := store.GetSyncState(ctx, compactStateKey)
	if !lastCompact.IsZero() && time.Since(lastCompact) < compactInterval {
		return
	}

	slog.Info("running activity compaction", "older_than", compactOlderThan)
	deleted, err := store.CompactActivity(ctx, compactOlderThan)
	if err != nil {
		slog.Error("compaction failed", "error", err)
		return
	}

	slog.Info("compaction complete", "rows_deleted", deleted)

	// Prune old activity rows beyond retention window.
	pruned, err := store.PruneActivity(ctx, pruneActivityRetention)
	if err != nil {
		slog.Error("prune activity failed", "error", err)
	} else if pruned > 0 {
		slog.Info("pruned old activity", "rows_deleted", pruned)
	}

	// Prune old score history beyond retention window.
	histPruned, err := store.PruneScoreHistory(ctx, pruneHistoryRetention)
	if err != nil {
		slog.Error("prune score history failed", "error", err)
	} else if histPruned > 0 {
		slog.Info("pruned old score history", "rows_deleted", histPruned)
	}

	if err := store.SaveSyncState(ctx, compactStateKey, time.Now().UTC()); err != nil {
		slog.Error("save compaction state", "error", err)
	}
}

// computeHours determines which hourly archive files to process.
//   - No cursor (fresh install): returns up to lookback hours ending at now-1h.
//   - With cursor (catching up): returns up to catchupMax hours from cursor+1h.
func computeHours(cursor time.Time, lookback, catchupMax int) []time.Time {
	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-time.Hour)

	var start time.Time
	var cap int
	if cursor.IsZero() {
		start = lastAvailable.Add(-time.Duration(lookback-1) * time.Hour)
		cap = lookback
	} else {
		start = cursor.Add(time.Hour)
		cap = catchupMax
	}

	if !start.Before(now) {
		return nil
	}

	var hours []time.Time
	for t := start; !t.After(lastAvailable) && len(hours) < cap; t = t.Add(time.Hour) {
		hours = append(hours, t)
	}
	return hours
}

func processHour(ctx context.Context, store ingestStore, reader *ArchiveReader,
	hour time.Time, tenantRepos map[string]bool) error {
	start := time.Now()
	slog.Info("processing archive", "hour", hour.Format("2006-01-02-15"))

	agg := NewAggregator(hour)
	var eventCount int

	err := reader.Stream(ctx, hour, func(ev Event) {
		agg.Add(ev)
		eventCount++
	})
	if err != nil {
		return err
	}

	results := agg.Results()

	pgSummaries := make([]postgres.HourlySummary, 0, len(results))
	for _, s := range results {
		repos := make([]string, 0, len(s.Repos))
		for r := range s.Repos {
			repos = append(repos, r)
		}
		pgSummaries = append(pgSummaries, postgres.HourlySummary{
			Username:      s.Username,
			Provider:      "github",
			Hour:          agg.Hour(),
			PRsOpened:     s.PRsOpened,
			PRsMerged:     s.PRsMerged,
			PRsClosed:     s.PRsClosed,
			ReviewsGiven:  s.ReviewsGiven,
			IssueComments: s.IssueComments,
			DistinctRepos: len(repos),
			Repos:         repos,
		})
	}

	stored, err := store.BatchUpsertActivity(ctx, pgSummaries)
	if err != nil {
		return err
	}

	queued := queueContributors(ctx, store, results, tenantRepos)

	slog.Info("archive hour complete",
		"hour", hour.Format("2006-01-02-15"),
		"mode", "hourly",
		"events", eventCount,
		"contributors", len(results),
		"stored", stored,
		"queued", queued,
		"duration_sec", time.Since(start).Seconds(),
	)

	return nil
}

func queueContributors(ctx context.Context, store ingestStore,
	summaries []Summary, tenantRepos map[string]bool) int {
	var count int
	for _, s := range summaries {
		exists, _ := store.ContributorExists(ctx, s.Username, "github")

		touchesTenant := false
		for repo := range s.Repos {
			owner := repo
			if idx := strings.Index(repo, "/"); idx > 0 {
				owner = repo[:idx]
			}
			if tenantRepos[owner] {
				touchesTenant = true
				break
			}
		}

		if !touchesTenant {
			continue // only queue contributors active in tenant repos
		}

		var priority int
		switch {
		case !exists:
			priority = 1
		default:
			priority = 3
		}

		if err := store.EnqueueForScoring(ctx, s.Username, "github", priority); err != nil {
			slog.Debug("enqueue", "username", s.Username, "error", err)
			continue
		}
		count++
	}
	return count
}
