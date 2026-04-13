# DevTrace — GH Archive Integration

Design notes for integrating GitHub Archive as a data enrichment layer. This is a future capability, not yet planned for implementation.

## Motivation

DevTrace currently scores contributors reactively — on API request or via DevPulse sync. GH Archive enables proactive enrichment by continuously ingesting the GitHub public event firehose, giving DevTrace signals that the GitHub API alone cannot provide.

Two key benefits:
1. **API-free signals** — contribution patterns, cross-repo activity, behavioral analysis computed entirely from archive data. No GitHub API quota consumed.
2. **Pre-scored contributors** — popular contributors are scored before anyone asks, making lookups instant.

## Data Source

- URL: `https://data.gharchive.org/YYYY-MM-DD-H.json.gz`
- Format: Newline-delimited JSON, gzipped
- Size: ~500MB-1GB gzipped per hour
- Frequency: Hourly dumps

## Relevant Event Types

| Event Type | What it gives us |
|-----------|-----------------|
| `PullRequestEvent` | PR author, repo, action (opened/closed/merged), timestamps |
| `PullRequestReviewEvent` | Who reviews whose code, review engagement |
| `IssueCommentEvent` | Community engagement, discussion participation |
| `PushEvent` | Commit activity, velocity, repo breadth |
| `CreateEvent` | New repos, forks (fork-only detection) |
| `WatchEvent` | Star patterns (less useful for scoring) |

Primary filter for MVP: `PullRequestEvent` only. Expand to other types as value is proven.

## Signals Derived from Archive (No API Calls)

These signals supplement the existing reputer-based model and can be computed entirely from archive data:

**Behavioral (Tier 2 AI sensing):**
- Velocity anomalies — rolling 30-day commit/PR rate vs 6-month baseline
- Time-of-day spread — contribution timestamps across 24h (inhuman = 20+ hours/day consistently)
- Commit size uniformity — low variance in PR sizes suggests mechanical output
- Burst-and-vanish — intense activity in short window, then silence

**Engagement quality:**
- PR acceptance rate across repos — merged / (merged + closed), observed from events
- Review-to-author ratio — PRs reviewed vs PRs authored
- Cross-repo diversity — distinct repos with activity over time
- Issue engagement — comments, not just code

**Consistency:**
- Contribution cadence — weekly/monthly regularity
- Sustained engagement — months of activity vs one-time bursts
- Repo loyalty — repeat contributions to same repos vs drive-by

## Architecture

```
GH Archive hourly dump (Cloud Run Job, scheduled)
  → download + gunzip + stream NDJSON
  → filter relevant event types
  → extract (actor.login, repo, event_type, action, created_at)
  → batch insert into contributor_events table

Enrichment routine (background goroutine or separate job)
  → compute behavioral signals from contributor_events
  → update contributor profiles with archive-derived signals
  → queue high-priority contributors for API-based deep scoring

Scoring pipeline (existing)
  → reads archive-derived signals alongside API signals
  → combined score uses both sources
  → API calls reserved for signals archive can't provide
```

## Priority Queue for Scoring

Not every discovered contributor needs immediate API scoring. Priority tiers:

| Priority | Criteria | API scoring? |
|----------|----------|-------------|
| 1 (immediate) | On-demand API/UI request | Yes, synchronous |
| 2 (high) | Contributor to a tenant-tracked repo | Yes, background |
| 3 (medium) | Active contributor with no existing score | Yes, background (low rate) |
| 4 (low) | Contributor with fresh score, archive update only | No — archive signals only |

On-demand requests always bypass the queue and score immediately.

## Data Model (New Tables)

```sql
-- Raw events from GH Archive (append-only, partitioned by date)
CREATE TABLE contributor_event (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    repo TEXT NOT NULL,
    event_type TEXT NOT NULL,
    action TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Scoring queue
CREATE TABLE scoring_queue (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    priority INTEGER NOT NULL DEFAULT 3,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

-- Archive-derived behavioral signals (computed, not raw)
CREATE TABLE contributor_behavior (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    pr_velocity_30d REAL,
    pr_velocity_baseline REAL,
    time_spread_hours INTEGER,
    review_to_author_ratio REAL,
    distinct_repos_90d INTEGER,
    burst_score REAL,
    consistency_score REAL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);
```

## Scale Considerations

- **Storage:** `contributor_event` will grow fast (~millions of rows/day). Needs partitioning by date and retention policy (e.g., keep 6 months, archive older).
- **Compute:** Behavioral signal computation is CPU-bound but parallelizable. Run as a batch job, not inline.
- **API budget:** Archive-derived scoring consumes zero API calls. Reserve API budget for on-demand and high-priority background scoring.
- **Cold start:** Initial backfill from GH Archive history (available back to 2011) is optional but valuable for bootstrapping contributor profiles.

## Scoring Model Evolution

The current model (reputer v3.2.0) uses 22 signals across 5 categories. Archive integration adds a 6th dimension:

| Category | Source | Weight (current) | Weight (with archive) |
|----------|--------|:-:|:-:|
| Code Provenance | API | 0.15 | 0.12 |
| Identity | API | 0.25 | 0.20 |
| Engagement | API + Archive | 0.25 | 0.20 |
| Community | API + Archive | 0.15 | 0.13 |
| Behavioral | API + Archive | 0.20 | 0.20 |
| Consistency | Archive only | — | 0.15 |

Weight adjustments TBD — requires validation against labeled data.

## Phased Approach

1. **Phase A:** Ingest `PullRequestEvent` only, populate `contributor_event`, compute basic stats (PR count, repo count, velocity). No scoring model changes.
2. **Phase B:** Compute behavioral signals (`contributor_behavior` table). Feed into scoring model as supplementary signals.
3. **Phase C:** Add more event types (reviews, issues, pushes). Richer behavioral analysis.
4. **Phase D:** Tier 2 AI sensing using archive data (velocity anomalies, time patterns, burst detection).

## Open Questions

- **Retention policy:** How long to keep raw events? 6 months? 1 year?
- **Backfill:** How far back to ingest from GH Archive history?
- **Cost:** Cloud Run job + storage for millions of rows/day. Estimate needed.
- **Model validation:** How to validate that archive-derived signals improve scoring accuracy?
- **Privacy:** GH Archive is public data, but aggregating behavioral profiles may have perception implications.
