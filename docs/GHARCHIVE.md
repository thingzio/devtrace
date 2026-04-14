# DevTrace — GH Archive Integration

Hourly ingest of the GitHub public event firehose. Provides API-free behavioral signals and enables the hybrid scoring path that reduces GitHub API calls from 5 to 1.

## Architecture

```
Cloud Scheduler (hourly at :20)
  → Cloud Run Job (devtrace-ingest)
    → download data.gharchive.org/{YYYY-MM-DD-H}.json.gz
    → stream gunzip → NDJSON line-by-line (1MB buffer)
    → filter: PullRequestEvent, PullRequestReviewEvent, IssueCommentEvent
    → skip bot actors (pkg/bot)
    → aggregate per-contributor hourly summaries in memory
    → batch upsert into contributor_activity
    → queue new/relevant contributors for scoring
    → compact old rows (daily, >30 days → weekly buckets)
```

## Event Types

| Event Type | What it captures |
|-----------|-----------------|
| `PullRequestEvent` | PR author, repo, action (opened/closed) |
| `PullRequestReviewEvent` | Review activity, engagement |
| `IssueCommentEvent` | Community participation |

Other event types (PushEvent, CreateEvent, WatchEvent) are filtered out.

## Data Model

### contributor_activity

Per-contributor hourly summaries. Primary key: `(username, provider, hour)`.

| Column | Type | Description |
|--------|------|-------------|
| username | TEXT | GitHub login |
| provider | TEXT | Always `github` |
| hour | TIMESTAMPTZ | Truncated to hour boundary |
| prs_opened | INTEGER | PRs opened in this hour |
| prs_merged | INTEGER | PRs merged |
| prs_closed | INTEGER | PRs closed (unmerged) |
| reviews_given | INTEGER | Reviews submitted |
| issue_comments | INTEGER | Issue comments |
| distinct_repos | INTEGER | Unique repos touched |
| repos | JSONB | List of repo names |

ON CONFLICT: counts are added to existing values (idempotent re-runs).

### scoring_queue

Priority-based queue for background scoring.

| Priority | Criteria |
|----------|----------|
| P1 | New contributor in a tenant-tracked repo |
| P2 | New contributor in any repo |
| P3 | Existing contributor in a tenant-tracked repo (rescore) |

Existing contributors in non-tenant repos are skipped (no queue entry).

ON CONFLICT: priority upgrades to the higher (lower number) value.

## Behavioral Signals

Computed by `GetBehavioralSignals()` from the last 180 days of `contributor_activity`:

| Signal | Computation |
|--------|------------|
| `pr_velocity_30d` | SUM(prs_opened) last 30 days |
| `pr_velocity_baseline` | SUM(prs_opened) / months in window |
| `reviews_given_30d` | SUM(reviews_given) last 30 days |
| `issue_comments_30d` | SUM(issue_comments) last 30 days |
| `distinct_repos_90d` | COUNT(DISTINCT repo) last 90 days (from JSONB expansion) |
| `consistency_score` | Active weeks / total weeks (0.0-1.0) |
| `active_since` | MIN(hour) |
| `total_prs_merged` | SUM(prs_merged) all time |
| `total_prs_closed` | SUM(prs_closed) all time |

## Hybrid Scoring Path

When behavioral signals exist for a contributor, the score service builds `ArchiveHints` from cumulative PR counts and distinct repos. `FetchSignals` accepts these hints and skips 3 GitHub Search API calls (merged PRs, closed PRs, recent repos).

| Scenario | API Calls |
|----------|-----------|
| No archive data, no repo | 3 (profile + search×2 + repos) |
| With archive data, no repo | 1 (profile only) |
| No archive data, with repo | 6 |
| With archive data, with repo | 4 (profile + repos + 3 repo-scoped) |

## Bot Filtering

Bot actors are skipped during aggregation (`pkg/bot/IsBot`). Detection:
1. `[bot]` suffix (GitHub App convention)
2. Known bot names list (aligned with DevPulse: dependabot, renovate, copilot, etc.)

This prevents bot activity from polluting behavioral signals and wasting scoring queue entries.

## Compaction

Runs as part of the hourly ingest job (after import + queue). Checks `sync_state` key `activity_compacted` — skips if last run was < 24 hours ago.

Process (single transaction):
1. Aggregate hourly rows older than 30 days into weekly buckets (Monday 00:00 UTC)
2. Delete original hourly rows
3. Preserve count totals (SUM-based aggregation)

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `GHARCHIVE_BASE_URL` | `https://data.gharchive.org` | Archive URL (override for testing) |
| `GHARCHIVE_LOOKBACK_HOURS` | `1` | Hours to bootstrap on fresh install (no cursor) |
| `GHARCHIVE_CATCHUP_MAX_HOURS` | `24` | Max hours to process when catching up from cursor |

### Cursor Management

Progress tracked via `sync_state` table (key: `gharchive_cursor`). Each successfully processed hour advances the cursor. On failure, the hour is skipped and retried next run.

Fresh install: processes `GHARCHIVE_LOOKBACK_HOURS` ending at now-1h.
With cursor: processes from cursor+1h, capped at `GHARCHIVE_CATCHUP_MAX_HOURS`.

## Infrastructure

| Resource | Description |
|----------|-------------|
| `devtrace-saas-ingest` | Cloud Run v2 Job (1 task, 55min timeout, 1Gi memory) |
| `devtrace-saas-ingest-hourly` | Cloud Scheduler at `:20` past each hour |
| `devtrace-saas-scheduler` | Dedicated invoker service account |

Same VPC, Cloud SQL socket, and runtime service account as the serve service.

## Scale Notes

- One hour of GH Archive yields ~3-10K contributor summaries after filtering
- Ingest completes in ~6 seconds locally (download + parse + store + queue)
- Compaction keeps the table bounded — weekly rows replace 168 hourly rows per contributor
- No raw event storage — only aggregated summaries (much smaller footprint than raw events)

## Future Extensions

- **Additional event types**: PushEvent for commit velocity, CreateEvent for fork detection
- **Tier 2 AI sensing**: Velocity anomalies, time-of-day spread, burst detection computed from activity data
- **Historical backfill**: GH Archive goes back to 2011 — optional bootstrap for contributor profiles
