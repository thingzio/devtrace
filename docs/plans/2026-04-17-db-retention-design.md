# DB Retention & Behavioral Window Optimization

## Problem

GH Archive ingestion stores activity for ALL GitHub users (~21K new/day), causing
unbounded DB growth. Current DB at 1.7 GB after 57 days of partial backfill.
Behavioral scoring window (180 days) exceeds what's needed for accurate scoring.

## Design

### 1. Behavioral Query Window: 180 -> 90 days

Reduce `GetBehavioralSignals` window from 180 to 90 days. Only 3 signals use the
91-180 day range (consistency, burst-vanish, PR baseline) with combined weight of
0.09 (9%). The 30-day signals (velocity, reviews, comments) and 90-day signals
(repo diversity, hour spread) are unaffected.

Pro plan deep scoring for dormant developers is handled by GitHub API signals
(account age, total PRs, merge rates) which have no GH Archive dependency.

File: `pkg/data/postgres/activity.go` — `GetBehavioralSignals()`

### 2. Activity Retention: 120 days

New `PruneActivity()` method deletes rows older than 120 days. Runs daily alongside
existing `CompactActivity`. The 120-day window provides 90 days for scoring + 30-day
buffer for compaction overlap.

Combined with existing compaction (hourly -> weekly after 30 days), the activity
table reaches a ceiling of ~314 MB (~3.9M rows).

Files: `pkg/data/postgres/activity.go`, `pkg/ingest/runner.go`

### 3. Reputation History Retention: 400 days

New `PruneScoreHistory()` method deletes rows older than 400 days. Runs daily.
Covers Pro plan chart window (365 entries) with buffer. Ceiling: ~34 MB.

Files: `pkg/data/postgres/history.go`, `pkg/ingest/runner.go`

### 4. Backfill Cursor Reset

Migration to delete `gharchive_backfill_cursor` from `devtrace_sync_state`.
Backfill restarts from newest hour, reprocessing with 8MB scanner buffer.
Upserts are idempotent — safe to reprocess.

File: `pkg/data/postgres/sql/migrations/`

## Growth Projections

| Component | Ceiling | Growth |
|-----------|---------|--------|
| Activity (bounded) | 314 MB | 0 (retention-limited) |
| History (bounded) | 34 MB | 0 (retention-limited) |
| Contributor (linear) | -- | ~1.4 MB/month |
| **Total at Year 1** | **~400 MB** | **~1.4 MB/month** |

## Testing

- Update `TestCompactActivity` for new window
- Add `TestPruneActivity` and `TestPruneScoreHistory`
- Verify behavioral signals with 90-day window
