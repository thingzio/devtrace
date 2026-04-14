# Merged Service Design

## Problem

The current architecture has two separate Cloud Run deployments:
- **devtrace-saas-serve**: HTTP service (scales to zero)
- **devtrace-saas-ingest**: Cloud Run job triggered by Cloud Scheduler (hourly)

Background scoring runs as a goroutine in the serve binary, but Cloud Run scales to zero between requests, so the scorer never fires. The ingest job queues contributors for scoring, but nobody processes the queue.

## Decision

Merge ingest into the serve binary. Run as a single Cloud Run service with `min-instances=1`, `max-instances=1`.

### Why not separate services with PubSub?
- On-demand scoring must be synchronous (sub-3s). Async via PubSub adds latency and complexity.
- Token pool is naturally shared in a single process.

### Why max-instances=1?
- Prevents duplicate background work (ingest, scoring) without leader election.
- Single instance handles hundreds of concurrent HTTP requests — more than enough.
- GitHub API tokens are the real bottleneck, not compute.
- Cost difference is negligible (~$5-10/month for always-on).

## Architecture

Single binary (`devtrace-site`) runs:

1. **HTTP server** — dashboard, API, settings, auth
2. **Ingest loop** — polls GH Archive every 10 min, processes new hours
3. **Background scorer** — dequeues and scores contributors with token budget
4. **DevPulse sync** — existing, unchanged

### Ingest Loop

```
StartIngestLoop(ctx, store)
  ticker every 10 min (INGEST_INTERVAL_SEC)
    calls ingest.Run(ctx, store)
      checks cursor in sync_state (gharchive_cursor)
      skips already-processed hours
      saves cursor after each hour
```

No changes to `ingest.Run()` — already idempotent via cursor tracking.

### Scorer Token Budget

```
Token pool: N tokens x 5,000/hr each (grows with tenants)
Background budget: 70% (SCORER_TOKEN_BUDGET_PCT)
On-demand (P0): always gets remaining 30%

Background priorities:
  P1: freshly queued from ingest
  P2: stale (>7 days since last score)
  P3: stale (>30 days since last score)
```

### Removed Components

- `cmd/devtrace-ingest/` binary
- Cloud Run job (devtrace-saas-ingest)
- Cloud Scheduler trigger

### Changed Components

- `server.go` — starts ingest loop alongside existing background workers
- `pkg/background/` — new `StartIngestLoop` function
- Cloud Run service — `min-instances=1`, `max-instances=1`

## Scaling Notes

- Token pool scales with tenants (each GitHub App install = 5k/hr)
- At 10 tenants: 50k/hr GitHub capacity
- Cloud SQL connections become the ceiling before compute does
- If max-instances=1 ever becomes a bottleneck, add pg advisory lock for background work and remove the cap
