# Phase 5: GH Archive Ingest — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ingest hourly GH Archive dumps to populate contributor behavioral summaries, queue discovered contributors for API scoring by priority, and extend the background scorer to drain the queue.

**Architecture:** New `devtrace-ingest` Cloud Run Job binary downloads hourly GH Archive dumps, stream-processes NDJSON, aggregates per-contributor hourly summaries into `contributor_activity`, and queues new/relevant contributors for scoring. The existing background scorer is extended to drain the `scoring_queue` by priority before rescoring stale contributors.

**Tech Stack:** Go 1.26, `compress/gzip` + `bufio.Scanner` for streaming, `encoding/json` for NDJSON parsing, PostgreSQL batch inserts. Same build toolchain as `devtrace-site`.

---

## Task 1: Database Migration — New Tables

Add `contributor_activity` and `scoring_queue` tables.

**Files:**
- Create: `pkg/data/postgres/sql/migrations/003_gharchive.sql`

**Step 1: Create migration**

```sql
-- Hourly behavioral summaries from GH Archive.
-- Sharding-ready: BIGSERIAL id + composite PK for AlloyDB compatibility.
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

-- Priority-based scoring queue.
-- P1: new contributor in tenant repo
-- P2: new contributor in any repo
-- P3: stale contributor in tenant repo
CREATE TABLE scoring_queue (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    priority INTEGER NOT NULL DEFAULT 2,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

CREATE INDEX idx_scoring_queue_priority ON scoring_queue(priority, queued_at);
```

**Step 2: Verify migration applies**

```bash
make db-up
go test ./pkg/data/postgres/ -run TestMigrate -v
```

**Step 3: Commit**

```bash
git add pkg/data/postgres/sql/migrations/
git commit -S -m "Add contributor_activity and scoring_queue tables"
```

---

## Task 2: Activity Data Access Layer

Store and query methods for `contributor_activity` and `scoring_queue`.

**Files:**
- Create: `pkg/data/postgres/activity.go`
- Create: `pkg/data/postgres/activity_test.go`
- Create: `pkg/data/postgres/queue.go`
- Create: `pkg/data/postgres/queue_test.go`

### activity.go

```go
package postgres

type HourlySummary struct {
    Username      string
    Provider      string
    Hour          time.Time
    PRsOpened     int
    PRsMerged     int
    PRsClosed     int
    ReviewsGiven  int
    IssueComments int
    DistinctRepos int
    Repos         []string
}

// BatchUpsertActivity inserts or updates hourly summaries.
// ON CONFLICT (username, provider, hour) adds to existing counts.
func (s *Store) BatchUpsertActivity(ctx context.Context, summaries []HourlySummary) (int, error)

// GetBehavioralSignals computes behavioral metrics for a contributor
// from contributor_activity over the given window.
type BehavioralSignals struct {
    PRVelocity30d       int
    PRVelocityBaseline  float64
    ReviewsGiven30d     int
    IssueComments30d    int
    DistinctRepos90d    int
    ConsistencyScore    float64   // active weeks / total weeks in window
    ActiveSince         time.Time
}
func (s *Store) GetBehavioralSignals(ctx context.Context, username, provider string) (*BehavioralSignals, error)
```

`BatchUpsertActivity` uses a multi-row INSERT with ON CONFLICT DO UPDATE to add counts:
```sql
INSERT INTO contributor_activity (username, provider, hour, prs_opened, prs_merged, prs_closed,
    reviews_given, issue_comments, distinct_repos, repos)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
ON CONFLICT (username, provider, hour) DO UPDATE SET
    prs_opened = contributor_activity.prs_opened + EXCLUDED.prs_opened,
    prs_merged = contributor_activity.prs_merged + EXCLUDED.prs_merged,
    prs_closed = contributor_activity.prs_closed + EXCLUDED.prs_closed,
    reviews_given = contributor_activity.reviews_given + EXCLUDED.reviews_given,
    issue_comments = contributor_activity.issue_comments + EXCLUDED.issue_comments,
    distinct_repos = EXCLUDED.distinct_repos,
    repos = EXCLUDED.repos;
```

`GetBehavioralSignals` computes from the last 180 days of `contributor_activity`:
- `pr_velocity_30d`: SUM(prs_opened) WHERE hour > NOW() - 30d
- `pr_velocity_baseline`: SUM(prs_opened) / months in window
- `reviews_given_30d`: SUM(reviews_given) WHERE hour > NOW() - 30d
- `distinct_repos_90d`: COUNT(DISTINCT unnested repo) WHERE hour > NOW() - 90d
- `consistency_score`: COUNT(DISTINCT date_trunc('week', hour)) / total weeks in window
- `active_since`: MIN(hour)

### queue.go

```go
package postgres

type QueueEntry struct {
    Username string
    Provider string
    Priority int
    QueuedAt time.Time
}

// EnqueueForScoring adds a contributor to the scoring queue.
// ON CONFLICT: only update if new priority is higher (lower number).
func (s *Store) EnqueueForScoring(ctx context.Context, username, provider string, priority int) error

// DequeueForScoring returns the next batch of contributors to score,
// ordered by priority (ascending) then queued_at (ascending).
func (s *Store) DequeueForScoring(ctx context.Context, limit int) ([]QueueEntry, error)

// RemoveFromQueue removes a contributor from the queue after scoring.
func (s *Store) RemoveFromQueue(ctx context.Context, username, provider string) error

// ContributorExists checks if a contributor has an existing score.
func (s *Store) ContributorExists(ctx context.Context, username, provider string) (bool, error)

// GetTenantRepos returns the set of repos tracked by any tenant
// (from github_app_installation target_login values).
func (s *Store) GetTenantRepos(ctx context.Context) (map[string]bool, error)
```

`EnqueueForScoring`:
```sql
INSERT INTO scoring_queue (username, provider, priority)
VALUES ($1, $2, $3)
ON CONFLICT (username, provider) DO UPDATE SET
    priority = LEAST(scoring_queue.priority, EXCLUDED.priority),
    queued_at = CASE WHEN EXCLUDED.priority < scoring_queue.priority
                     THEN NOW() ELSE scoring_queue.queued_at END;
```

`DequeueForScoring`:
```sql
SELECT username, provider, priority, queued_at
FROM scoring_queue
ORDER BY priority ASC, queued_at ASC
LIMIT $1;
```

### Tests

Integration tests (skip without DB):
- `TestBatchUpsertActivity` — insert summaries, verify rows, upsert adds counts
- `TestGetBehavioralSignals` — insert test data across dates, verify computed signals
- `TestEnqueueAndDequeue` — enqueue P2, enqueue P1 for same user (should upgrade to P1), dequeue returns P1 first
- `TestContributorExists` — false when missing, true after upsert
- `TestGetTenantRepos` — returns empty when no installations (OK for local dev)

**Commit:**
```bash
git add pkg/data/postgres/
git commit -S -m "Add activity and scoring queue data access layer"
```

---

## Task 3: GH Archive Downloader + Parser

Stream-download and parse GH Archive NDJSON dumps.

**Files:**
- Create: `pkg/ingest/archive.go`
- Create: `pkg/ingest/archive_test.go`

### archive.go

```go
package ingest

import (
    "bufio"
    "compress/gzip"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

const defaultBaseURL = "https://data.gharchive.org"

// Event represents a filtered GH Archive event.
type Event struct {
    Type      string    // PullRequestEvent, PullRequestReviewEvent, IssueCommentEvent
    Action    string    // opened, closed, created, submitted, etc.
    Actor     string    // GitHub username
    Repo      string    // owner/repo
    CreatedAt time.Time
}

// ArchiveReader downloads and streams a GH Archive hourly dump.
type ArchiveReader struct {
    baseURL string
    client  *http.Client
}

func NewArchiveReader(baseURL string) *ArchiveReader {
    if baseURL == "" {
        baseURL = defaultBaseURL
    }
    return &ArchiveReader{
        baseURL: baseURL,
        client:  &http.Client{Timeout: 10 * time.Minute},
    }
}

// URL returns the archive URL for the given hour.
func (r *ArchiveReader) URL(t time.Time) string {
    return fmt.Sprintf("%s/%s.json.gz", r.baseURL, t.UTC().Format("2006-01-02-15"))
}

// Stream downloads the archive for the given hour and calls fn for each
// relevant event. Streams gzip → NDJSON line-by-line (low memory).
func (r *ArchiveReader) Stream(ctx context.Context, hour time.Time, fn func(Event)) error {
    url := r.URL(hour)
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return fmt.Errorf("create request: %w", err)
    }

    resp, err := r.client.Do(req)
    if err != nil {
        return fmt.Errorf("download %s: %w", url, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("archive %s: status %d", url, resp.StatusCode)
    }

    gz, err := gzip.NewReader(resp.Body)
    if err != nil {
        return fmt.Errorf("gzip reader: %w", err)
    }
    defer gz.Close()

    scanner := bufio.NewScanner(gz)
    scanner.Buffer(make([]byte, 0, 1<<20), 1<<20) // 1MB line buffer

    for scanner.Scan() {
        if ctx.Err() != nil {
            return ctx.Err()
        }
        ev, ok := parseEvent(scanner.Bytes())
        if ok {
            fn(ev)
        }
    }

    return scanner.Err()
}
```

`parseEvent` parses a single NDJSON line:
```go
// Raw GH Archive event structure (minimal, only fields we need).
type rawEvent struct {
    Type      string `json:"type"`
    Actor     struct {
        Login string `json:"login"`
    } `json:"actor"`
    Repo struct {
        Name string `json:"name"`
    } `json:"repo"`
    Payload json.RawMessage `json:"payload"`
    CreatedAt string        `json:"created_at"`
}

func parseEvent(line []byte) (Event, bool) {
    var raw rawEvent
    if err := json.Unmarshal(line, &raw); err != nil {
        return Event{}, false
    }

    // Filter event types
    switch raw.Type {
    case "PullRequestEvent", "PullRequestReviewEvent", "IssueCommentEvent":
    default:
        return Event{}, false
    }

    // Extract action from payload
    var payload struct {
        Action string `json:"action"`
    }
    _ = json.Unmarshal(raw.Payload, &payload)

    t, _ := time.Parse(time.RFC3339, raw.CreatedAt)

    return Event{
        Type:      raw.Type,
        Action:    payload.Action,
        Actor:     raw.Actor.Login,
        Repo:      raw.Repo.Name,
        CreatedAt: t,
    }, raw.Actor.Login != "" && raw.Repo.Name != ""
}
```

### Tests

- `TestParseEvent` — test with sample JSON lines for each event type, plus ignored types
- `TestParseEventInvalid` — malformed JSON returns false
- `TestArchiveReaderURL` — verify URL format for a given time
- `TestStream` — use httptest server serving a small gzipped NDJSON fixture, verify events are streamed correctly

Create a test fixture: a small gzipped file with ~5 NDJSON lines (2 PullRequestEvents, 1 PullRequestReviewEvent, 1 IssueCommentEvent, 1 WatchEvent). Verify Stream yields 4 events (WatchEvent filtered).

**Commit:**
```bash
git add pkg/ingest/
git commit -S -m "Add GH Archive downloader and NDJSON stream parser"
```

---

## Task 4: Aggregator — In-Memory Event Summarization

Aggregate streamed events into per-contributor hourly summaries.

**Files:**
- Create: `pkg/ingest/aggregator.go`
- Create: `pkg/ingest/aggregator_test.go`

### aggregator.go

```go
package ingest

import "time"

// Aggregator collects events into per-contributor hourly summaries.
type Aggregator struct {
    hour      time.Time
    summaries map[string]*summary // key: username
}

type summary struct {
    PRsOpened     int
    PRsMerged     int
    PRsClosed     int
    ReviewsGiven  int
    IssueComments int
    Repos         map[string]bool
}

func NewAggregator(hour time.Time) *Aggregator {
    return &Aggregator{
        hour:      hour,
        summaries: make(map[string]*summary),
    }
}

// Add processes a single event.
func (a *Aggregator) Add(ev Event) {
    s, ok := a.summaries[ev.Actor]
    if !ok {
        s = &summary{Repos: make(map[string]bool)}
        a.summaries[ev.Actor] = s
    }
    s.Repos[ev.Repo] = true

    switch ev.Type {
    case "PullRequestEvent":
        switch ev.Action {
        case "opened": s.PRsOpened++
        case "closed": s.PRsClosed++ // note: merged PRs also have action=closed
        }
    case "PullRequestReviewEvent":
        s.ReviewsGiven++
    case "IssueCommentEvent":
        s.IssueComments++
    }
}

// Summaries returns the aggregated hourly summaries.
func (a *Aggregator) Summaries() []HourlySummary {
    // Convert map to slice of HourlySummary (from postgres package type)
}
```

Note: `HourlySummary` is defined in `pkg/data/postgres/activity.go`. To avoid a circular import, define a local summary struct in the ingest package and convert at the boundary. Or define the struct in a shared `pkg/model/` package.

**Better approach:** Define `HourlySummary` in `pkg/model/types.go` (already exists) and have both packages import from there. Move the struct from `postgres/activity.go` to `model/types.go`.

### Tests

- `TestAggregatorEmpty` — new aggregator, no events, Summaries returns empty
- `TestAggregatorSingleUser` — add 3 PR events + 1 review for same user, verify counts
- `TestAggregatorMultipleUsers` — add events for 3 users, verify 3 summaries
- `TestAggregatorRepoDedup` — same user, same repo, multiple events: distinct_repos = 1
- `TestAggregatorPRActions` — opened, closed, merged actions counted correctly

**Commit:**
```bash
git add pkg/ingest/ pkg/model/
git commit -S -m "Add event aggregator for per-contributor hourly summaries"
```

---

## Task 5: Ingest Runner — Orchestration

Tie together: download → stream → aggregate → store → queue.

**Files:**
- Create: `pkg/ingest/runner.go`
- Create: `pkg/ingest/runner_test.go`

### runner.go

```go
package ingest

import (
    "context"
    "log/slog"
    "time"

    "github.com/thingzio/devtrace/pkg/config"
    "github.com/thingzio/devtrace/pkg/data/postgres"
)

// Run processes one or more hourly GH Archive dumps.
func Run(ctx context.Context, store *postgres.Store) error {
    baseURL := config.GetEnv("GHARCHIVE_BASE_URL", "")
    reader := NewArchiveReader(baseURL)

    // Determine which hour(s) to process
    cursor, _ := store.GetSyncState(ctx, "gharchive_cursor")
    lookback := config.GetEnvAsInt("GHARCHIVE_LOOKBACK_HOURS", 1)
    hours := computeHours(cursor, lookback)

    // Load tenant repos for priority scoring
    tenantRepos, _ := store.GetTenantRepos(ctx)

    for _, hour := range hours {
        if err := processHour(ctx, store, reader, hour, tenantRepos); err != nil {
            slog.Error("process hour", "hour", hour, "error", err)
            continue // skip failed hour, try next
        }
        // Advance cursor
        _ = store.SaveSyncState(ctx, "gharchive_cursor", hour)
    }

    return nil
}

func computeHours(cursor time.Time, lookback int) []time.Time {
    // If no cursor: process [now - lookback hours .. now - 1 hour]
    // If cursor: process [cursor + 1 hour .. now - 1 hour]
    // Cap at lookback hours max
}

func processHour(ctx context.Context, store *postgres.Store, reader *ArchiveReader,
    hour time.Time, tenantRepos map[string]bool) error {

    slog.Info("processing archive", "hour", hour)

    agg := NewAggregator(hour)
    var eventCount int

    err := reader.Stream(ctx, hour, func(ev Event) {
        agg.Add(ev)
        eventCount++
    })
    if err != nil {
        return err
    }

    summaries := agg.Summaries()
    slog.Info("aggregated", "events", eventCount, "contributors", len(summaries))

    // Store activity summaries
    stored, err := store.BatchUpsertActivity(ctx, summaries)
    if err != nil {
        return err
    }
    slog.Info("stored activity", "rows", stored)

    // Queue contributors for scoring
    queued := queueContributors(ctx, store, summaries, tenantRepos)
    slog.Info("queued for scoring", "count", queued)

    return nil
}

func queueContributors(ctx context.Context, store *postgres.Store,
    summaries []HourlySummary, tenantRepos map[string]bool) int {

    var count int
    for _, s := range summaries {
        exists, _ := store.ContributorExists(ctx, s.Username, s.Provider)
        touchesTenant := false
        for _, repo := range s.Repos {
            if tenantRepos[repo] {
                touchesTenant = true
                break
            }
        }

        priority := 0
        switch {
        case !exists && touchesTenant:
            priority = 1 // P1: new + tenant repo
        case !exists:
            priority = 2 // P2: new + any repo
        case exists && touchesTenant:
            priority = 3 // P3: existing + tenant repo (rescore)
        default:
            continue // existing + non-tenant = skip
        }

        if err := store.EnqueueForScoring(ctx, s.Username, s.Provider, priority); err != nil {
            slog.Debug("enqueue", "username", s.Username, "error", err)
            continue
        }
        count++
    }
    return count
}
```

### Tests

- `TestComputeHours` — no cursor returns lookback hours; with cursor returns gap hours
- `TestQueueContributors` — mock store, verify P1/P2/P3 assignments based on exists + tenant repo

**Commit:**
```bash
git add pkg/ingest/
git commit -S -m "Add ingest runner orchestrating download, aggregate, store, queue"
```

---

## Task 6: Ingest Binary Entry Point

New `cmd/devtrace-ingest/main.go`.

**Files:**
- Create: `cmd/devtrace-ingest/main.go`
- Modify: `.goreleaser.yaml` — add devtrace-ingest build
- Modify: `Makefile` — add `ingest` target

### main.go

Same pattern as devtrace-site: ldflags, signal handling, structured logging.

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/thingzio/devtrace/pkg/data/postgres"
    "github.com/thingzio/devtrace/pkg/ingest"
    "github.com/thingzio/devtrace/pkg/logging"
)

var (
    version = "v0.0.1-default"
    commit  = ""
    date    = ""
)

func main() {
    logging.SetupLogger()
    slog.Info("starting devtrace-ingest", "version", version, "commit", commit, "date", date)

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    store, err := postgres.NewFromEnv(ctx)
    if err != nil {
        slog.Error("init store", "error", err)
        os.Exit(1)
    }
    defer store.Close()

    if err := store.Migrate(ctx); err != nil {
        slog.Error("migrate", "error", err)
        os.Exit(1)
    }

    if err := ingest.Run(ctx, store); err != nil {
        slog.Error("ingest failed", "error", err)
        os.Exit(1)
    }

    slog.Info("ingest complete")
}
```

### .goreleaser.yaml

Add second build:
```yaml
  - id: devtrace-ingest
    binary: devtrace-ingest
    dir: ./cmd/devtrace-ingest
    ldflags: [same as devtrace-site]
    goos: [linux]
    goarch: [amd64, arm64]
```

Add second ko entry for container image.

### Makefile

Add target:
```makefile
.PHONY: ingest
ingest: ## Runs devtrace-ingest locally
	go run ./cmd/devtrace-ingest/
```

### .gitignore

Add `devtrace-ingest` binary.

**Commit:**
```bash
git add cmd/devtrace-ingest/ .goreleaser.yaml Makefile .gitignore
git commit -S -m "Add devtrace-ingest binary entry point"
```

---

## Task 7: Extend Background Scorer — Queue Awareness

Update the existing background scorer to drain `scoring_queue` before rescoring stale contributors.

**Files:**
- Modify: `pkg/background/scorer.go`

### Updated runScorer flow

```go
func runScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client) {
    // Phase 1: Drain scoring queue (P1 → P2 → P3)
    queued, err := store.DequeueForScoring(ctx, scorerBatchSize)
    if err != nil {
        slog.Error("dequeue for scoring", "error", err)
    } else {
        var scored int
        for _, q := range queued {
            if err := scoreContributor(ctx, store, gh, q.Username, q.Provider); err != nil {
                slog.Warn("score queued contributor", "username", q.Username, "error", err)
                continue
            }
            _ = store.RemoveFromQueue(ctx, q.Username, q.Provider)
            scored++
        }
        if scored > 0 {
            slog.Info("queue scoring complete", "scored", scored, "total", len(queued))
        }
    }

    // Phase 2: Rescore stale contributors (existing behavior)
    lowDays := config.GetEnvAsInt("SCORER_LOW_STALE_DAYS", defaultLowDays)
    highDays := config.GetEnvAsInt("SCORER_HIGH_STALE_DAYS", defaultHighDays)
    stale, err := store.GetStaleContributors(ctx, lowDays, highDays, scorerBatchSize)
    // ... existing stale scoring logic
}

// scoreContributor is shared by both queue and stale paths.
func scoreContributor(ctx context.Context, store *postgres.Store, gh ghclient.Client,
    username, provider string) error {
    signals, err := gh.FetchSignals(ctx, username, "")
    if err != nil {
        return err
    }
    value := score.Compute(*signals)
    grade := score.Grade(value)
    _ = store.UpsertContributor(ctx, username, provider)
    _ = store.SaveScoreHistory(ctx, username, provider, value, grade, true)
    return store.UpdateReputation(ctx, username, provider, value, grade, signals)
}
```

Extract the common scoring logic into `scoreContributor` to avoid duplication.

**Commit:**
```bash
git add pkg/background/
git commit -S -m "Extend background scorer to drain scoring queue by priority"
```

---

## Task 8: Behavioral Signals in API Response

Add `behavior` field to the score API response for authenticated users.

**Files:**
- Modify: `pkg/model/types.go` — add Behavior struct
- Modify: `pkg/service/score.go` — query behavioral signals
- Modify: `pkg/server/handler_score.go` — pass store to service (if not already)

### model/types.go addition

```go
type Behavior struct {
    PRVelocity30d      int       `json:"pr_velocity_30d"`
    PRVelocityBaseline float64   `json:"pr_velocity_baseline"`
    ReviewsGiven30d    int       `json:"reviews_given_30d"`
    IssueComments30d   int       `json:"issue_comments_30d"`
    DistinctRepos90d   int       `json:"distinct_repos_90d"`
    ConsistencyScore   float64   `json:"consistency_score"`
    ActiveSince        time.Time `json:"active_since,omitempty"`
}
```

Add `Behavior *Behavior` field to `ScoreResponse` (with `omitempty` JSON tag).

### service/score.go update

After computing the score, if store is not nil and plan is authenticated:
```go
if store != nil && plan != "" {
    if beh, err := store.GetBehavioralSignals(ctx, username, "github"); err == nil && beh != nil {
        resp.Behavior = mapBehavior(beh)
    }
}
```

This is a best-effort enrichment — if no behavioral data exists, the field is omitted.

**Commit:**
```bash
git add pkg/model/ pkg/service/ pkg/server/
git commit -S -m "Add behavioral signals to API response from contributor_activity"
```

---

## Task 9: Terraform + CI/CD for Ingest Job

Add Cloud Run Job and Cloud Scheduler for the ingest pipeline.

**Files:**
- Modify: `infra/saas/cloudrun.tf` — add ingest job
- Modify: `infra/saas/main.tf` — enable cloudscheduler API
- Create: `infra/saas/scheduler.tf` — hourly trigger
- Modify: `.github/workflows/release-on-tag.yaml` — build + push ingest image

### cloudrun.tf addition

```hcl
resource "google_cloud_run_v2_job" "ingest" {
  name     = "${var.prefix}-ingest"
  location = var.region

  template {
    parallelism = 1
    task_count  = 1

    template {
      timeout         = "3300s"
      service_account = google_service_account.run.email

      containers {
        image = "${local.image_repo}/devtrace-ingest:${var.image_tag}"
        resources {
          limits = { cpu = "1000m", memory = "1Gi" }
        }
        # Same DATABASE_URL, VPC, Cloud SQL socket as site service
      }
    }
  }
}
```

### scheduler.tf

```hcl
resource "google_cloud_scheduler_job" "ingest" {
  name     = "${var.prefix}-ingest-hourly"
  schedule = "20 * * * *"
  # ... triggers Cloud Run job
}
```

**Commit:**
```bash
git add infra/ .github/
git commit -S -m "Add Terraform and CI/CD for ingest Cloud Run Job"
```

---

## Task 10: End-to-End Verification

**Step 1: Run full test suite**

```bash
make test
make lint
```

**Step 2: Build both binaries**

```bash
go build ./cmd/devtrace-site/
go build ./cmd/devtrace-ingest/
```

**Step 3: Test ingest locally**

```bash
make db-up
make ingest
# Should download previous hour's archive, process events, store summaries
# Logs: "processing archive", "aggregated", "stored activity", "queued for scoring"
```

**Step 4: Verify behavioral signals appear in API**

```bash
GITHUB_TOKEN=... go run ./cmd/devtrace-site/
# After ingest has run:
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/score/torvalds | jq '.behavior'
```

**Step 5: Verify Terraform**

```bash
cd infra/saas && terraform fmt -check && cd ../..
```

**Step 6: Commit any fixes**

```bash
git add -A
git commit -S -m "Phase 5 end-to-end verification fixes"
```

---

## Phase 5 Complete Checklist

- [ ] Migration: `contributor_activity` and `scoring_queue` tables
- [ ] Activity data layer: BatchUpsertActivity, GetBehavioralSignals
- [ ] Queue data layer: EnqueueForScoring, DequeueForScoring, RemoveFromQueue
- [ ] GH Archive reader: stream download, gunzip, NDJSON parsing
- [ ] Event filter: PullRequestEvent, PullRequestReviewEvent, IssueCommentEvent
- [ ] Aggregator: per-contributor hourly summaries in memory
- [ ] Ingest runner: download → stream → aggregate → store → queue
- [ ] Priority queue: P1 (new + tenant repo), P2 (new + any), P3 (stale + tenant)
- [ ] Ingest binary: `devtrace-ingest` with goreleaser + Makefile
- [ ] Background scorer: drains queue before rescoring stale
- [ ] Behavioral signals: `behavior` field in API response
- [ ] Terraform: Cloud Run Job + Scheduler for hourly ingest
- [ ] All tests pass, lint clean

---

---

## Claude API Integration Points (Tier 3 — Future)

The behavioral data from GH Archive combined with API-sourced signals creates rich context for Claude-powered analysis. These are the places where LLM analysis adds insight beyond what metrics alone can show. Stub these as interfaces/extension points during implementation.

### 1. Risk Narrative Generation

**Input:** Full `ScoreResponse` + `BehavioralSignals` for a contributor.

**What Claude adds:** A natural language risk assessment that synthesizes multiple weak signals into a coherent narrative. Example: "This contributor's PR velocity tripled in March 2026 while their review-to-author ratio dropped to zero — consistent with AI-assisted bulk contributions without code review engagement."

**Where to stub:** `pkg/service/score.go` — after computing score + behavioral signals, optionally call a `RiskNarrative(resp) string` function. Return empty string if Claude not configured.

### 2. PR Description Authenticity

**Input:** PR titles and descriptions from GH Archive events (available in the payload).

**What Claude adds:** Classify PR descriptions as likely human-written vs AI-generated. AI descriptions tend to be more structured, use bullet points, and explain "what" exhaustively while omitting "why." This feeds into the AI sensing dimension.

**Where to stub:** `pkg/ingest/aggregator.go` — extract PR title/description from event payload during aggregation. Store as optional fields in `contributor_activity`. The analysis itself runs as a batch job (Tier 3, Claude Haiku for classification).

### 3. Contribution Pattern Anomaly Detection

**Input:** 180 days of `contributor_activity` hourly summaries for a contributor.

**What Claude adds:** Detect non-obvious behavioral shifts. A sudden change in contribution style, timing, or repo breadth may indicate account compromise, organizational changes, or transition to AI-assisted development. Metrics can detect velocity changes; Claude can assess whether the pattern is concerning.

**Where to stub:** `pkg/data/postgres/activity.go` → `GetBehavioralSignals` already computes velocity and consistency. Add an `AnomalyContext` struct that packages the raw time-series data for Claude analysis. The analysis endpoint calls Claude only when Tier 2 heuristics flag ambiguity.

### 4. Cross-Contributor Correlation

**Input:** Activity patterns for all contributors to a specific repo.

**What Claude adds:** Identify coordinated behavior — multiple accounts with similar timing, PR patterns, or description templates. This is relevant for supply chain security: detecting sybil-style contribution campaigns.

**Where to stub:** This is a repo-level analysis, not contributor-level. Add a future `pkg/analysis/` package with an interface for repo-level risk assessment. The ingest pipeline already captures per-repo contributor activity.

### 5. License Risk Interpretation

**Input:** A contributor's license distribution (from license analysis) + their behavioral profile.

**What Claude adds:** Contextual risk assessment. A contributor who actively contributes to GPL-3.0 repos and then submits code to your Apache-2.0 project may warrant attention — not because of a violation, but because of provenance ambiguity. Claude can articulate the nuance that a license distribution table alone cannot.

**Where to stub:** `pkg/service/score.go` — when license data and behavioral data are both present, an optional `LicenseRiskAssessment` function can synthesize them via Claude.

### Cost Control

All Claude integration follows the design from `docs/SCOPE.md`:
- **Haiku** for classification tasks (PR description, yes/no + confidence)
- **Sonnet** for narrative generation (risk summaries, anomaly explanations)
- **Batch API** (50% cost reduction) for non-real-time analysis
- **Cache results** with configurable TTL (7 days active, 30 days inactive)
- **Free/Starter tiers never trigger Claude** — Pro/Enterprise only

---

## What's Next

- **Phase 5 follow-up:** Compaction job for `contributor_activity` (weekly aggregation of old rows)
- **Phase 6:** Admin service for operator visibility
- **GitHub Action:** `thingzio/devtrace-action` (separate repo)
- **Claude API integration:** Implement Tier 3 analysis behind the stubbed interfaces
