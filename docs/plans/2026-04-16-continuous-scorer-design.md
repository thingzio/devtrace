# Continuous Scorer with Quota-Aware Throttling

Replace the timer-based background scorer with a continuous loop that pauses only when aggregate GitHub API token quota drops below a configurable threshold.

## Problem

Background scorer runs on a 1-hour timer, processing 100 entries per cycle. With 95K entries in the scoring queue, this takes ~40 days to drain. The scorer sleeps most of the time while tokens sit idle.

## Design

### Continuous scorer loop

Replace `StartBackgroundScorer`'s ticker with a tight loop:

1. Dequeue batch (size from `SCORER_BATCH_SIZE`, default 100)
2. If queue empty → sleep 30s, retry
3. Check aggregate token pool quota via free `GET /rate_limit` API
4. If aggregate remaining < `SCORER_MIN_QUOTA_PCT` (default 30%) → sleep until earliest token reset + 30s jitter, retry
5. Score the batch (each contributor scored and written individually, no batch transaction)
6. Loop immediately (no sleep between batches)
7. Individual scoring errors logged and skipped, don't break the loop
8. Context cancellation stops the loop gracefully

### TokenPool quota awareness

Add `CheckQuotas(ctx) ([]TokenQuota, error)` to `TokenPool`:
- Iterates all tokens in the pool
- Calls `GET https://api.github.com/rate_limit` per token (free, no quota cost)
- Returns per-token: limit, remaining, reset time

Add `AggregateQuota(quotas) (pctRemaining int, earliestReset time.Time)`:
- Sums limit and remaining across all tokens
- Returns aggregate remaining percentage and earliest reset time

### Admin dashboard integration

Token Pool section reuses `CheckQuotas` to display live quota data per token: table with token index, limit, used, remaining, %, reset time. Replaces current call-count display.

## Env vars

- `SCORER_BATCH_SIZE` — entries per batch, default 100
- `SCORER_MIN_QUOTA_PCT` — minimum aggregate remaining %, default 30
- **Remove:** `SCORER_INTERVAL_SEC` (no longer timer-based)

## Files changed

- `pkg/github/tokenpool.go` — add `TokenQuota` struct, `CheckQuotas()`, `AggregateQuota()`
- `pkg/github/tokenpool_test.go` — tests for quota aggregation
- `pkg/background/scorer.go` — rewrite to continuous loop with quota checking
- `pkg/background/scorer_test.go` — update tests
- `pkg/server/handler_admin.go` — use `CheckQuotas` for token pool section
- `pkg/server/templates/admin.html` — update token pool with quota table

## No contention risk

Each contributor is scored and written individually (3 small writes: upsert, history insert, reputation update). No batch transaction. On-demand scoring hits different rows. PostgreSQL MVCC handles concurrent access.
