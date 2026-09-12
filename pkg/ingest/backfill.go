// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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

// backfillQueueDepthGate caps how full the scoring queue can get before the
// backfill loop pauses. Backfill enqueues many tenant-touching contributors
// per hour; without a gate the queue grows unbounded relative to scorer
// throughput. Pause-and-poll lets the scorer drain before more work piles
// on. Below the gate, backfill runs at full speed.
const (
	backfillQueueDepthGate = 50_000
	backfillQueuePauseWait = 60 * time.Second
)

// nonTenantPurgedOnce gates the PurgeNonTenantQueue cleanup so it runs at
// most once per process. The legacy priority-2 entries it removes are a
// migration artifact; doing the table scan on every backfill tick was
// wasteful with no recurring source of new entries.
var nonTenantPurgedOnce sync.Once

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

	// Purge legacy priority-2 (non-tenant) queue entries. Runs once per
	// process: there is no source of new priority-2 entries, so repeating
	// the table scan on every backfill tick is wasteful.
	nonTenantPurgedOnce.Do(func() {
		if purged, err := store.PurgeNonTenantQueue(ctx); err != nil {
			slog.Error("purge non-tenant queue", "error", err)
		} else if purged > 0 {
			slog.Info("purged non-tenant queue entries", "count", purged)
		}
	})

	cursor, err := store.GetSyncState(ctx, backfillCursorKey)
	if err != nil {
		slog.Warn("get backfill cursor failed, starting from scratch", "error", err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	hours := backfillHours(now, days, cursor)

	if len(hours) == 0 {
		slog.Info("backfill complete, no hours remaining")
		return nil
	}

	baseURL := config.GetEnv("GHARCHIVE_BASE_URL", "")
	reader := NewArchiveReader(baseURL)

	tenantRepos, err := store.GetTenantRepos(ctx)
	if err != nil {
		slog.Warn("get tenant repos failed, proceeding without tenant filter", "error", err)
	}

	total := len(hours)
	var processed int

	slog.Info("backfill starting", "total_hours", total, "days", days)

	for i := 0; i < len(hours); i += backfillBatchSize {
		if ctx.Err() != nil {
			return fmt.Errorf("backfill canceled: %w", ctx.Err())
		}

		// Pause backfill if the scoring queue is over its high-water mark
		// so the scorer can drain. Without this, backfill enqueues faster
		// than the scorer drains and queue depth grows unbounded.
		for {
			depth, err := store.QueueDepth(ctx)
			if err != nil || depth < backfillQueueDepthGate {
				break
			}
			slog.Info("backfill paused, queue depth above gate",
				"depth", depth,
				"gate", backfillQueueDepthGate,
				"wait", backfillQueuePauseWait,
			)
			timer := time.NewTimer(backfillQueuePauseWait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("backfill canceled while paused: %w", ctx.Err())
			case <-timer.C:
			}
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
		select {
		case <-ctx.Done():
			break
		case sem <- struct{}{}: // acquire
		}
		if ctx.Err() != nil {
			break
		}
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
			Provider:      providerGitHub,
			Hour:          agg.Hour(),
			PRsOpened:     s.PRsOpened,
			PRsMerged:     s.PRsMerged,
			PRsClosed:     s.PRsClosed,
			ReviewsGiven:  s.ReviewsGiven,
			IssueComments: s.IssueComments,
			IssuesOpened:  s.IssuesOpened,
			IssuesClosed:  s.IssuesClosed,
			DistinctRepos: len(repos),
			Repos:         repos,
		})
	}

	stored, err := store.BatchUpsertActivity(ctx, pgSummaries)
	if err != nil {
		slog.Warn("backfill upsert failed", "hour", hour.Format("2006-01-02-15"), "error", err)
		return
	}

	queued := queueContributors(ctx, store, results, tenantRepos, nil)
	slog.Debug("backfill hour done",
		"hour", hour.Format("2006-01-02-15"),
		"events", eventCount,
		"stored", stored,
		"queued", queued,
	)
}
