# Scorer Observability + Concurrent Batch Processing Design

## Problem

Queue depth is 92k and growing at ~4k usernames/hour. Sequential scoring processes ~600 users/hour/token (Search API bottleneck). With 4 tokens that's ~2,400/hour — falling behind. No metrics to track throughput or diagnose bottlenecks.

## Solution

1. Add structured logging at key points to track throughput, hint usage, and queue drain rate
2. Change `drainQueue` and `rescoreStale` from sequential to concurrent using bounded `errgroup`

## Observability

**Per-cycle summary** (end of each drain/rescore):
```
slog.Info("scorer cycle", "scored", N, "errors", E, "with_hints", H, "without_hints", W, "elapsed", dur)
```

**Periodic stats** (every 5 minutes):
```
slog.Info("scorer stats", "scored_total", total, "scored_5m", recent, "queue_depth", depth, "rate_per_hour", rate)
```

**Per-user timing** (debug level):
```
slog.Debug("scored", "username", u, "duration", dur, "hints", hasHints)
```

Requires a counter struct and `QueueDepth(ctx) (int, error)` on the store.

## Concurrent Batch Processing

Change `drainQueue` from sequential loop to bounded `errgroup`:
- `SCORER_CONCURRENCY` env var, default 3
- `errgroup.SetLimit(concurrency)`
- `atomic.Int32` for scored counter
- Errors don't cancel siblings (return nil from goroutine)
- Same pattern for `rescoreStale`

## Expected Throughput

With 4 tokens, 3 concurrent workers:
- ~1,500-1,800 users/hour/token × 4 = ~6,000-7,200 users/hour
- Net drain: ~2,000-3,200/hour
- Backlog cleared in ~30-45 hours
