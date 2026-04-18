package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
)

const backfillCursorKey = "gharchive_backfill_cursor"

var (
	backfillBatchSize = config.GetEnvAsInt("GHARCHIVE_BACKFILL_BATCH_SIZE", 18)
	backfillWorkers   = config.GetEnvAsInt("GHARCHIVE_BACKFILL_WORKERS", 3)
)

// Backfill processes historical GH Archive hours in reverse-chronological order
// (most recent first) so that on initial deployment, archive data is populated
// going back the specified number of days.
func Backfill(ctx context.Context, store *postgres.Store, days int) error {
	if days <= 0 {
		return nil
	}

	totalStart := time.Now()

	// Purge queued entries for non-tenant contributors (legacy priority 2).
	if purged, err := store.PurgeNonTenantQueue(ctx); err != nil {
		slog.Error("purge non-tenant queue", "error", err)
	} else if purged > 0 {
		slog.Info("purged non-tenant queue entries", "count", purged)
	}

	cursor, _ := store.GetSyncState(ctx, backfillCursorKey)
	now := time.Now().UTC().Truncate(time.Hour)
	hours := backfillHours(now, days, cursor)

	if len(hours) == 0 {
		slog.Info("backfill complete, no hours remaining")
		return nil
	}

	baseURL := config.GetEnv("GHARCHIVE_BASE_URL", "")
	reader := NewArchiveReader(baseURL)

	tenantRepos, _ := store.GetTenantRepos(ctx)

	total := len(hours)
	var processed int

	slog.Info("backfill starting", "total_hours", total, "days", days)

	for i := 0; i < len(hours); i += backfillBatchSize {
		if ctx.Err() != nil {
			return fmt.Errorf("backfill canceled: %w", ctx.Err())
		}

		end := min(i+backfillBatchSize, len(hours))
		batch := hours[i:end]
		batchStart := time.Now()

		if err := processBackfillBatch(ctx, store, reader, batch, tenantRepos); err != nil {
			return fmt.Errorf("backfill batch: %w", err)
		}

		// Save cursor as the oldest hour in this batch (last element, since reverse-chrono).
		oldest := batch[len(batch)-1]
		if err := store.SaveSyncState(ctx, backfillCursorKey, oldest); err != nil {
			slog.Error("save backfill cursor", "error", err)
		}

		processed += len(batch)
		remaining := total - processed
		pct := float64(processed) / float64(total) * 100

		slog.Info("backfill batch complete",
			"mode", "backfill",
			"processed", processed,
			"remaining", remaining,
			"pct", fmt.Sprintf("%.1f", pct),
			"batch_duration_sec", time.Since(batchStart).Seconds(),
			"total_elapsed", time.Since(totalStart).Round(time.Millisecond),
		)

		// Run maintenance (compaction + pruning) during long backfills.
		maybeCompact(ctx, store)
	}

	slog.Info("backfill complete",
		"total_hours", total,
		"processed", processed,
		"duration_sec", time.Since(totalStart).Seconds(),
	)

	return nil
}

// backfillHours generates hours from now-1h down to now-days*24h in reverse
// chronological order. If cursor is set (non-zero), only generates hours older
// than cursor (starting from cursor-1h). Returns nil if already complete.
func backfillHours(now time.Time, days int, cursor time.Time) []time.Time {
	now = now.UTC().Truncate(time.Hour)
	start := now.Add(-time.Hour) // most recent available
	end := now.Add(-time.Duration(days) * 24 * time.Hour)

	if !cursor.IsZero() {
		start = cursor.Add(-time.Hour)
	}

	if !start.After(end) {
		return nil
	}

	var hours []time.Time
	for t := start; t.After(end) || t.Equal(end); t = t.Add(-time.Hour) {
		hours = append(hours, t)
		if t.Equal(end) {
			break
		}
	}
	return hours
}

// processBackfillBatch processes a slice of hours through the archive pipeline
// using bounded concurrency. Individual hour failures are logged and skipped.
func processBackfillBatch(ctx context.Context, store ingestStore, reader *ArchiveReader,
	hours []time.Time, tenantRepos map[string]bool) error {
	if ctx.Err() != nil {
		return fmt.Errorf("batch canceled: %w", ctx.Err())
	}

	sem := make(chan struct{}, backfillWorkers)
	var wg sync.WaitGroup

	for _, hour := range hours {
		if ctx.Err() != nil {
			break
		}

		sem <- struct{}{} // acquire
		wg.Add(1)

		go func(h time.Time) {
			defer wg.Done()
			defer func() { <-sem }() // release

			processBackfillHour(ctx, store, reader, h, tenantRepos)
		}(hour)
	}

	wg.Wait()

	if ctx.Err() != nil {
		return fmt.Errorf("batch canceled: %w", ctx.Err())
	}
	return nil
}

// processBackfillHour downloads, parses, and upserts a single archive hour.
func processBackfillHour(ctx context.Context, store ingestStore, reader *ArchiveReader,
	hour time.Time, tenantRepos map[string]bool) {
	agg := NewAggregator(hour)
	var eventCount int

	err := reader.Stream(ctx, hour, func(ev Event) {
		agg.Add(ev)
		eventCount++
	})
	if err != nil {
		slog.Warn("backfill hour skipped", "hour", hour.Format("2006-01-02-15"), "error", err)
		return
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
		slog.Warn("backfill upsert failed", "hour", hour.Format("2006-01-02-15"), "error", err)
		return
	}

	queued := queueContributors(ctx, store, results, tenantRepos)
	slog.Debug("backfill hour done",
		"hour", hour.Format("2006-01-02-15"),
		"events", eventCount,
		"stored", stored,
		"queued", queued,
	)
}
