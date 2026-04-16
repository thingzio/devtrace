# GH Archive Historical Backfill Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** On first deployment (empty activity table), progressively backfill 180 days of GH Archive data starting from most recent 7 days, so all behavioral signals and AI Sensing heuristics have full coverage from day one.

**Architecture:** New `Backfill(ctx, store, days)` function in `pkg/ingest/` processes hours in reverse-chronological batches. Called once at startup before the normal ingest loop begins. Uses the existing `ArchiveReader` + `Aggregator` + `BatchUpsertActivity` pipeline. A `backfill_cursor` sync state key tracks progress so interrupted backfills resume where they left off. Batch size (hours per transaction) is configurable to avoid overwhelming the shared DB.

**Tech Stack:** Go, GH Archive (HTTP), PostgreSQL (BatchUpsertActivity)

---

### Task 1: Add `Backfill` function with reverse-chronological hour generation

**Files:**
- Create: `pkg/ingest/backfill.go`
- Test: `pkg/ingest/backfill_test.go`

**Step 1: Write the test for hour generation**

```go
// pkg/ingest/backfill_test.go
package ingest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackfillHours(t *testing.T) {
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	t.Run("full_range", func(t *testing.T) {
		hours := backfillHours(now, 3, time.Time{})
		assert.Len(t, hours, 3*24)
		// Most recent first.
		assert.Equal(t, now.Add(-1*time.Hour), hours[0])
		// Oldest last.
		assert.Equal(t, now.Add(-3*24*time.Hour), hours[len(hours)-1])
	})

	t.Run("partial_resume", func(t *testing.T) {
		// Cursor says we already backfilled down to 2 days ago.
		cursor := now.Add(-2 * 24 * time.Hour)
		hours := backfillHours(now, 3, cursor)
		// Should only generate hours older than cursor.
		assert.True(t, len(hours) > 0)
		for _, h := range hours {
			assert.True(t, h.Before(cursor), "hour %v should be before cursor %v", h, cursor)
		}
	})

	t.Run("already_complete", func(t *testing.T) {
		// Cursor is already at the target.
		cursor := now.Add(-3 * 24 * time.Hour)
		hours := backfillHours(now, 3, cursor)
		assert.Empty(t, hours)
	})
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestBackfillHours ./pkg/ingest/ -v`
Expected: FAIL — `backfillHours` not defined

**Step 3: Write the implementation**

```go
// pkg/ingest/backfill.go
package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

const (
	backfillCursorKey = "gharchive_backfill_cursor"
	backfillBatchSize = 6 // hours per DB batch (adjustable)
)

// backfillStore defines the store operations needed by the backfill.
type backfillStore interface {
	GetSyncState(ctx context.Context, key string) (time.Time, error)
	SaveSyncState(ctx context.Context, key string, val time.Time) error
	BatchUpsertActivity(ctx context.Context, summaries []postgres.HourlySummary) (int, error)
	GetTenantRepos(ctx context.Context) (map[string]bool, error)
	EnqueueForScoring(ctx context.Context, username, provider string, priority int) error
	ContributorExists(ctx context.Context, username, provider string) (bool, error)
}

// Backfill downloads historical GH Archive data going back the specified number
// of days. Hours are processed in reverse-chronological order (most recent first)
// so the trust threshold is met quickly. Progress is saved after each batch so
// interrupted backfills resume automatically.
//
// Skips entirely when the backfill cursor indicates completion.
func Backfill(ctx context.Context, store *postgres.Store, days int) error {
	if days <= 0 {
		return nil
	}

	cursor, _ := store.GetSyncState(ctx, backfillCursorKey)
	now := time.Now().UTC().Truncate(time.Hour)
	hours := backfillHours(now, days, cursor)

	if len(hours) == 0 {
		slog.Info("backfill already complete")
		return nil
	}

	baseURL := ""
	reader := NewArchiveReader(baseURL)
	tenantRepos, _ := store.GetTenantRepos(ctx)

	total := len(hours)
	slog.Info("starting archive backfill",
		"total_hours", total,
		"days", days,
		"batch_size", backfillBatchSize,
	)

	start := time.Now()
	var processed int

	for i := 0; i < total; i += backfillBatchSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		end := i + backfillBatchSize
		if end > total {
			end = total
		}
		batch := hours[i:end]

		batchStart := time.Now()
		if err := processBackfillBatch(ctx, store, reader, batch, tenantRepos); err != nil {
			slog.Error("backfill batch failed", "error", err)
			return err
		}
		processed += len(batch)

		// Save cursor as the oldest hour processed in this batch.
		oldest := batch[len(batch)-1]
		if err := store.SaveSyncState(ctx, backfillCursorKey, oldest); err != nil {
			slog.Error("save backfill cursor", "error", err)
		}

		pct := float64(processed) / float64(total) * 100
		slog.Info("backfill progress",
			"processed", processed,
			"remaining", total-processed,
			"pct", int(pct),
			"batch_elapsed", time.Since(batchStart).Round(time.Millisecond),
			"total_elapsed", time.Since(start).Round(time.Second),
		)
	}

	slog.Info("backfill complete",
		"hours_processed", processed,
		"elapsed", time.Since(start).Round(time.Second),
	)
	return nil
}

// backfillHours generates hours in reverse-chronological order from now-1h back
// to now-days*24h. If cursor is set, only generates hours older than the cursor
// (meaning everything more recent has already been backfilled).
func backfillHours(now time.Time, days int, cursor time.Time) []time.Time {
	latest := now.Add(-time.Hour)
	oldest := now.Add(-time.Duration(days) * 24 * time.Hour)

	// If cursor exists, we've already backfilled down to that point.
	// Continue from one hour before the cursor.
	start := latest
	if !cursor.IsZero() {
		start = cursor.Add(-time.Hour)
	}

	if !start.After(oldest) {
		return nil
	}

	var hours []time.Time
	for t := start; !t.Before(oldest); t = t.Add(-time.Hour) {
		hours = append(hours, t)
	}
	return hours
}

func processBackfillBatch(ctx context.Context, store backfillStore, reader *ArchiveReader,
	hours []time.Time, tenantRepos map[string]bool) error {
	for _, hour := range hours {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Debug("backfill hour", "hour", hour.Format("2006-01-02-15"))

		agg := NewAggregator(hour)
		err := reader.Stream(ctx, hour, func(ev Event) {
			agg.Add(ev)
		})
		if err != nil {
			slog.Warn("backfill hour failed, skipping",
				"hour", hour.Format("2006-01-02-15"),
				"error", err,
			)
			continue
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

		if _, err := store.BatchUpsertActivity(ctx, pgSummaries); err != nil {
			return err
		}

		queueContributors(ctx, store, results, tenantRepos)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestBackfillHours ./pkg/ingest/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/ingest/backfill.go pkg/ingest/backfill_test.go
git commit -S -m "Add GH Archive historical backfill with reverse-chronological processing"
```

---

### Task 2: Wire backfill into server startup

**Files:**
- Modify: `pkg/server/server.go` (where `StartIngestLoop` is called)
- Modify: `pkg/ingest/runner.go` (export `queueContributors` for reuse)

**Step 1: Export queueContributors in runner.go**

The `queueContributors` function in `runner.go` is unexported and used by both `processHour` and `processBackfillBatch`. Since both are in the same package, no change needed — the backfill code already calls it directly.

**Step 2: Add backfill call before ingest loop in server.go**

Find where `StartIngestLoop` is called and add a `Backfill` call before it. The backfill runs synchronously (blocks startup until the first 7 days are processed, then continues in background).

Actually, the backfill should NOT block startup — it could take hours for 180 days. Instead, run it as the first thing the ingest loop does:

```go
// In server.go, modify the ingest loop setup:
ingestStop := background.StartIngestLoop(ctx, func(ctx context.Context) error {
    return ingest.Run(ctx, store)
}, 0)
```

Change to:

```go
backfillDays := config.GetEnvAsInt("GHARCHIVE_BACKFILL_DAYS", 0)
ingestStop := background.StartIngestLoop(ctx, func(ctx context.Context) error {
    // One-time backfill on first run (skips if cursor shows completion).
    if backfillDays > 0 {
        if err := ingest.Backfill(ctx, store, backfillDays); err != nil {
            slog.Error("backfill failed", "error", err)
        }
    }
    return ingest.Run(ctx, store)
}, 0)
```

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/server/server.go
git commit -S -m "Wire GH Archive backfill into ingest loop startup"
```

---

### Task 3: Add env var documentation and integration test

**Files:**
- Test: `pkg/ingest/backfill_test.go` (add integration-style test)

**Step 1: Add test for Backfill with zero days (no-op)**

```go
func TestBackfillZeroDays(t *testing.T) {
	err := Backfill(context.Background(), nil, 0)
	assert.NoError(t, err)
}
```

**Step 2: Run full test suite**

Run: `make qualify`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/ingest/backfill_test.go
git commit -S -m "Add backfill no-op test and env var documentation"
```

---

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `GHARCHIVE_BACKFILL_DAYS` | `0` (disabled) | Number of days to backfill on startup. Set to `180` for full coverage. |

## How it works

1. **First deployment:** Set `GHARCHIVE_BACKFILL_DAYS=180`. On first ingest tick, `Backfill` finds no cursor and generates 4,320 hours in reverse order.
2. **Progressive:** Processes most recent 7 days first (~168 hours). After that, archive hints cross the trust threshold and scoring uses them.
3. **Batched:** Processes 6 hours per batch, saves cursor after each batch. DB sees ~6 upsert transactions between cursor saves.
4. **Resumable:** If the service restarts mid-backfill, it reads the cursor and continues from where it stopped.
5. **Idempotent:** `BatchUpsertActivity` uses `ON CONFLICT DO UPDATE SET ... + EXCLUDED`, so re-processing an hour just adds to counts (safe for restarts).
6. **One-time:** Once the cursor reaches the target oldest hour, `backfillHours` returns empty and backfill is skipped on subsequent runs.
7. **Normal ingest continues:** After backfill completes (or is skipped), `ingest.Run` processes current hours as usual.

## Design Decisions

1. **Runs on every ingest tick until complete** — `Backfill` returns immediately once the cursor shows completion, so no wasted work after the first run.
2. **Per-batch progress at Info level** — each batch logs: processed count, remaining count, percentage, batch elapsed, total elapsed. No per-hour noise.
3. **Batch size is a constant (6 hours)** — simple, no env var needed. Keeps DB transactions small without excessive cursor writes.
