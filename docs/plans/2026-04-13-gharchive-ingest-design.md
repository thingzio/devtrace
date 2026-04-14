# GH Archive Ingest — Design Document

Date: 2026-04-13

## Context

DevTrace currently scores contributors reactively — on API request or via DevPulse sync. GH Archive enables proactive enrichment by continuously ingesting the GitHub public event firehose, providing behavioral signals computed without GitHub API calls and pre-scoring contributors relevant to tenant-tracked repos.

## Design Decisions

### Cloud Run Job

New binary `devtrace-ingest` runs as a Cloud Run Job scheduled hourly at :20 past (after GH Archive publishes dumps ~:10-:15). Timeout 55 minutes. Processes one hour per execution. Same VPC/Cloud SQL access as the site service.

### Event types

Ingest `PullRequestEvent`, `PullRequestReviewEvent`, and `IssueCommentEvent`. Covers PR activity, review engagement, and community participation. Skip noisy events (Watch, Fork, Push) for now.

### No raw event storage

Process the hourly dump in-memory via streaming (gunzip → NDJSON line-by-line). Aggregate per-contributor hourly summaries and write to `contributor_activity` table. Raw events are always re-available from GH Archive if reprocessing is needed.

### Hourly summary + compute on read

Store one row per contributor per hour with counts (PRs opened/merged/closed, reviews given, issue comments, distinct repos). Behavioral signals (velocity, consistency, burst detection) computed at query time from the summary rows. Compaction reduces old rows: >90 days → weekly, >1 year → monthly.

### Smart scoring queue

Not every discovered contributor needs API scoring. Priority model:

| Priority | Who | When queued |
|----------|-----|-------------|
| P0 | On-demand API/UI request | Never queued — scored synchronously |
| P1 | New contributor in a tenant-tracked repo | Ingest finds PR to tenant repo, contributor has no score |
| P2 | New contributor in any repo | Ingest finds contributor with no existing score |
| P3 | Existing contributor in tenant repo with stale score | Ingest finds activity for already-scored contributor |

Background scorer drains P1 first, then P2, then P3. API rate budget split: ~70% P1/P2, ~30% P3 (configurable). P0 always bypasses the queue.

### Behavioral data as separate dimension

Archive-derived behavioral signals returned as a separate `behavior` field in the API response — not folded into the reputation score. Same principle as AI sensing: independent transparency dimension.

## Data Model

### contributor_activity (new)

Hourly behavioral summaries. Sharding-ready for future AlloyDB migration.

```sql
CREATE TABLE contributor_activity (
    id BIGSERIAL,
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    hour TIMESTAMPTZ NOT NULL,
    prs_opened INTEGER NOT NULL DEFAULT 0,
    prs_merged INTEGER NOT NULL DEFAULT 0,
    prs_closed INTEGER NOT NULL DEFAULT 0,
    reviews_given INTEGER NOT NULL DEFAULT 0,
    issue_comments INTEGER NOT NULL DEFAULT 0,
    distinct_repos INTEGER NOT NULL DEFAULT 0,
    repos JSONB,
    PRIMARY KEY (username, provider, hour)
);
CREATE INDEX idx_activity_hour ON contributor_activity(hour);
```

### scoring_queue (new)

Priority-based queue drained by background scorer.

```sql
CREATE TABLE scoring_queue (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    priority INTEGER NOT NULL DEFAULT 2,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);
CREATE INDEX idx_scoring_queue_priority ON scoring_queue(priority, queued_at);
```

## Ingest Job Flow

```
1. Read cursor from sync_state (key: gharchive_cursor)
2. Determine hour to process (cursor + 1, or current_hour - 1 if no cursor)
3. Download data.gharchive.org/YYYY-MM-DD-H.json.gz
4. Stream gunzip → NDJSON line-by-line
5. Filter: PullRequestEvent, PullRequestReviewEvent, IssueCommentEvent
6. Extract: (actor.login, repo.name, type, action, created_at)
7. Aggregate in-memory: per-contributor hourly summary map
8. Batch upsert into contributor_activity
9. Load tenant-tracked repos set (from github_app_installation)
10. For each contributor:
    - No existing score + touches tenant repo → queue P1
    - No existing score + other repo → queue P2
    - Has score + touches tenant repo + score is stale → queue P3
11. Save cursor to sync_state
```

Memory: ~50MB for aggregation map (~100K contributors per hour). Stream processing, never loads full file.

## Binary + Deployment

- **Entry point:** `cmd/devtrace-ingest/main.go` (same pattern as devtrace-site)
- **Cloud Run Job:** `devtrace-saas-ingest`
- **Scheduler:** `20 * * * *` (hourly at :20 past)
- **Resources:** CPU 1000m, Memory 1Gi, Timeout 3300s (55 min)
- **Single task** (no parallelism — one hour per run)

Env vars:
- `DATABASE_URL` — same as site
- `GHARCHIVE_BASE_URL` — default `https://data.gharchive.org`
- `GHARCHIVE_LOOKBACK_HOURS` — default `1` (catch-up window)

## Background Scorer Update

The existing background scorer gains queue awareness:

```
1. Drain scoring_queue (P1 first, then P2, then P3)
   - For each: FetchSignals → Compute → UpdateReputation → Dequeue
   - Rate budget: ~70% for P1/P2, ~30% for P3
2. Then: GetStaleContributors (existing behavior, unchanged)
```

On-demand requests (P0) always bypass the queue — existing behavior unchanged.

## API Response Extension

Authenticated responses gain a `behavior` field (when data exists):

```json
{
  "behavior": {
    "pr_velocity_30d": 12,
    "pr_velocity_baseline": 8.5,
    "reviews_given_30d": 23,
    "issue_comments_30d": 15,
    "distinct_repos_90d": 7,
    "consistency_score": 0.82,
    "active_since": "2024-01-15T00:00:00Z"
  }
}
```

Computed on read from `contributor_activity`. Separate from reputation score.

## Compaction Strategy

Weekly Cloud Scheduler job (or in-process routine):
- Rows older than 90 days: aggregate into weekly summaries (sum, max distinct repos)
- Rows older than 1 year: aggregate into monthly summaries
- Keeps table bounded at ~50M rows steady state

## Terraform Additions

- `google_cloud_run_v2_job.ingest` — job resource
- `google_cloud_scheduler_job.ingest` — `20 * * * *` trigger
- `.goreleaser.yaml` — add `devtrace-ingest` build + container image
- Service account: reuse `devtrace-saas-run` (same DB access needed)

## Phased Implementation

1. **Phase A:** Ingest job + `contributor_activity` table + cursor management. No queue, no scoring integration. Validates the pipeline.
2. **Phase B:** Scoring queue + background scorer integration. Queue P1/P2/P3, drain via scorer.
3. **Phase C:** Behavioral signals in API response. Compute on read, return in `behavior` field.
4. **Phase D:** Compaction job. Weekly aggregation of old rows.

## Open Questions

- How to handle GH Archive outages (missing hours)? Retry next run or skip?
- Should the ingest job backfill historical data on first run? How far back?
- Behavioral signals weight in scoring model — defer until we have data to validate against?
- AlloyDB migration trigger — what scale/cost threshold justifies the switch?

## References

- `docs/GHARCHIVE.md` — initial design notes and motivation
- `docs/SCOPE.md` — AI sensing tiers and behavioral analysis signals
- GH Archive: https://www.gharchive.org
