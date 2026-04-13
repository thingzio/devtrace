# Phase 4: Backend Operations — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Update Terraform for the new `thingzio` shared infrastructure, add a background sync routine that copies developer scores from DevPulse every 30 minutes, and add a background scorer that rescores stale contributors on a schedule.

**Architecture:** Terraform references `thingzio` shared infra via variables (same pattern as DevPulse). Two background goroutines run inside the `devtrace-site` binary: (1) DevPulse sync copies `developer` rows into DevTrace's `contributor` + `reputation` tables, (2) background scorer re-fetches signals and rescores stale contributors. Both run on configurable intervals.

**Tech Stack:** Terraform (GCP provider), Go goroutines with `time.Ticker`, PostgreSQL cross-table queries within the same `thingz` database on `thingzio-pg` Cloud SQL instance.

---

## Task 1: Update Terraform — Align with thingzio Shared Infra

Rewrite `infra/saas/` to match DevPulse's pattern referencing the new shared infrastructure.

**Files:**
- Rewrite: `infra/saas/main.tf`
- Rewrite: `infra/saas/variables.tf`
- Rewrite: `infra/saas/outputs.tf`
- Rewrite: `infra/saas/cloud-run.tf`
- Rewrite: `infra/saas/iam.tf`
- Rewrite: `infra/saas/artifact-registry.tf`
- Create: `infra/saas/providers.tf`
- Create: `infra/saas/secrets.tf`
- Create: `infra/saas/database.tf`
- Create: `infra/saas/scheduler.tf`

**Step 1: Create providers.tf**

```hcl
terraform {
  required_version = ">= 1.13"
  backend "gcs" {
    bucket = "thingzio-infra-state"
    prefix = "devtrace"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
```

**Step 2: Rewrite variables.tf**

Follow DevPulse pattern exactly. Key variables:

```hcl
variable "project_id" {
  type    = string
  default = "thingzio"
}

variable "region" {
  type    = string
  default = "us-west1"
}

variable "prefix" {
  type    = string
  default = "devtrace-saas"
}

variable "vpc_id" {
  description = "Shared VPC network ID"
  type        = string
  default     = "projects/thingzio/global/networks/thingzio-vpc"
}

variable "subnet_id" {
  description = "Shared VPC subnet ID"
  type        = string
  default     = "projects/thingzio/regions/us-west1/subnetworks/thingzio-subnet"
}

variable "db_instance_name" {
  description = "Shared Cloud SQL instance name"
  type        = string
  default     = "thingzio-pg"
}

variable "db_connection_name" {
  description = "Shared Cloud SQL connection string"
  type        = string
  default     = "thingzio:us-west1:thingzio-pg"
}

variable "db_name" {
  description = "Database name within shared instance"
  type        = string
  default     = "thingz"
}

variable "domain" {
  type    = string
  default = "devtrace.thingz.io"
}

variable "image_tag" {
  type    = string
  default = "latest"
}

variable "admin_invoker_emails" {
  type    = list(string)
  default = []
}
```

**Step 3: Create database.tf**

Reference shared instance, create DevTrace's own DB user:

```hcl
data "google_sql_database_instance" "shared" {
  name    = var.db_instance_name
  project = var.project_id
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  name     = "devtrace"
  instance = data.google_sql_database_instance.shared.name
  password = random_password.db_password.result
}

locals {
  vpc_id        = var.vpc_id
  subnet_id     = var.subnet_id
  db_connection = var.db_connection_name
}
```

**Step 4: Rewrite main.tf**

Enable required APIs only (no resource creation for shared infra):

```hcl
resource "google_project_service" "apis" {
  for_each = toset([
    "run.googleapis.com",
    "artifactregistry.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudscheduler.googleapis.com",
    "sqladmin.googleapis.com",
    "iam.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}
```

**Step 5: Create secrets.tf**

DevTrace needs: GitHub App key, OAuth client secret, webhook secret. Same pattern as DevPulse:

```hcl
resource "google_secret_manager_secret" "github_app_key" {
  secret_id = "${var.prefix}-github-app-key"
  replication { auto {} }
}

resource "google_secret_manager_secret" "oauth_client_secret" {
  secret_id = "${var.prefix}-oauth-client-secret"
  replication { auto {} }
}

resource "google_secret_manager_secret" "webhook_secret" {
  secret_id = "${var.prefix}-webhook-secret"
  replication { auto {} }
}
```

**Step 6: Rewrite iam.tf**

DevTrace service accounts + WIF for GitHub Actions:

```hcl
resource "google_service_account" "run" {
  account_id   = "${var.prefix}-run"
  display_name = "DevTrace Cloud Run"
}

resource "google_service_account" "github_actions" {
  account_id   = "github-actions-${var.prefix}"
  display_name = "GitHub Actions - DevTrace"
}

# Roles for run SA: Cloud SQL client, Secret Manager accessor, logging, monitoring
# WIF: OIDC from GitHub Actions, scoped to thingzio/devtrace
```

**Step 7: Rewrite cloud-run.tf**

Single Cloud Run service `devtrace-saas-serve` with:
- Cloud SQL socket volume (same pattern as DevPulse)
- VPC access via network_interfaces
- Secret environment variables and volume mounts
- `DATABASE_URL` using Unix socket: `host=/cloudsql/${local.db_connection} dbname=${var.db_name} user=devtrace password=... sslmode=disable`

**Step 8: Rewrite artifact-registry.tf**

```hcl
resource "google_artifact_registry_repository" "images" {
  repository_id = "${var.prefix}-images"
  location      = var.region
  format        = "DOCKER"
}
```

**Step 9: Rewrite outputs.tf**

```hcl
output "service_url" {
  value = google_cloud_run_v2_service.site.uri
}
output "service_account_email" {
  value = google_service_account.run.email
}
output "image_repo" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}
```

**Step 10: Verify**

```bash
cd infra/saas && terraform fmt -check && cd ../..
```

**Step 11: Update .goreleaser.yaml**

Update image repo to new project:
```yaml
repositories:
  - us-west1-docker.pkg.dev/thingzio/devtrace-saas-images/devtrace-site
```

**Step 12: Update CI/CD workflows**

Update WIF provider, service account, and region references to `thingzio` project.

**Step 13: Commit**

```bash
git add infra/ .goreleaser.yaml .github/
git commit -S -m "Align Terraform with thingzio shared infrastructure"
```

---

## Task 2: DevPulse Sync — Data Access Layer

Create the database queries for reading DevPulse's `developer` table and upserting into DevTrace tables. Both tables are in the same `thingz` database on `thingzio-pg`.

**Files:**
- Create: `pkg/data/postgres/sync.go`
- Create: `pkg/data/postgres/sync_test.go`

**Step 1: Implement sync queries**

```go
// pkg/data/postgres/sync.go
package postgres

import (
    "context"
    "fmt"
    "time"
)

type DevPulseDeveloper struct {
    Username          string
    FullName          string
    Email             string
    Avatar            string
    Reputation        float64
    ReputationDeep    bool
    ReputationSignals string // raw JSON
    UpdatedAt         time.Time
}

// GetDevPulseUpdatedDevelopers reads developers from DevPulse's developer table
// that have been updated since the given timestamp.
// Both tables are in the same database (thingz on thingzio-pg).
func (s *Store) GetDevPulseUpdatedDevelopers(ctx context.Context, since time.Time, limit int) ([]DevPulseDeveloper, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT username, COALESCE(full_name,''), COALESCE(email,''), COALESCE(avatar,''),
                COALESCE(reputation,0), COALESCE(reputation_deep,0),
                COALESCE(reputation_signals,'{}'), COALESCE(reputation_updated_at,'')
         FROM developer
         WHERE reputation_updated_at > $1
         ORDER BY reputation_updated_at ASC
         LIMIT $2`, since.Format(time.RFC3339), limit)
    if err != nil {
        return nil, fmt.Errorf("query devpulse developers: %w", err)
    }
    defer rows.Close()

    var result []DevPulseDeveloper
    for rows.Next() {
        var d DevPulseDeveloper
        var deepInt int
        var updatedStr string
        if err := rows.Scan(&d.Username, &d.FullName, &d.Email, &d.Avatar,
            &d.Reputation, &deepInt, &d.ReputationSignals, &updatedStr); err != nil {
            return nil, fmt.Errorf("scan devpulse developer: %w", err)
        }
        d.ReputationDeep = deepInt == 1
        if t, err := time.Parse(time.RFC3339, updatedStr); err == nil {
            d.UpdatedAt = t
        }
        result = append(result, d)
    }
    return result, rows.Err()
}

// SyncDeveloperToDevTrace upserts a DevPulse developer into DevTrace's
// contributor and reputation tables.
func (s *Store) SyncDeveloperToDevTrace(ctx context.Context, d DevPulseDeveloper, grade string) error {
    // Upsert contributor
    _, err := s.db.ExecContext(ctx,
        `INSERT INTO contributor (username, provider, display_name, email, avatar_url)
         VALUES ($1, 'github', $2, $3, $4)
         ON CONFLICT (username, provider) DO UPDATE SET
           display_name = EXCLUDED.display_name,
           email = EXCLUDED.email,
           avatar_url = EXCLUDED.avatar_url,
           updated_at = NOW()`,
        d.Username, d.FullName, d.Email, d.Avatar)
    if err != nil {
        return fmt.Errorf("upsert contributor %s: %w", d.Username, err)
    }

    // Upsert reputation
    _, err = s.db.ExecContext(ctx,
        `INSERT INTO reputation (username, provider, score, grade, model_version, deep, signals)
         VALUES ($1, 'github', $2, $3, '3.2.0', $4, $5::jsonb)
         ON CONFLICT (username, provider) DO UPDATE SET
           score = EXCLUDED.score,
           grade = EXCLUDED.grade,
           deep = EXCLUDED.deep,
           signals = EXCLUDED.signals,
           scored_at = NOW()`,
        d.Username, d.Reputation, grade, d.ReputationDeep, d.ReputationSignals)
    if err != nil {
        return fmt.Errorf("upsert reputation %s: %w", d.Username, err)
    }

    // Append to history
    _, err = s.db.ExecContext(ctx,
        `INSERT INTO reputation_history (username, provider, score, grade, deep)
         VALUES ($1, 'github', $2, $3, $4)`,
        d.Username, d.Reputation, grade, d.ReputationDeep)
    if err != nil {
        return fmt.Errorf("save history %s: %w", d.Username, err)
    }

    return nil
}

// GetSyncState returns the last sync timestamp. Returns zero time if never synced.
func (s *Store) GetSyncState(ctx context.Context, key string) (time.Time, error) {
    var val string
    err := s.db.QueryRowContext(ctx,
        `SELECT value FROM sync_state WHERE key = $1`, key).Scan(&val)
    if err != nil {
        return time.Time{}, nil // not found = never synced
    }
    t, _ := time.Parse(time.RFC3339, val)
    return t, nil
}

// SaveSyncState stores the last sync timestamp.
func (s *Store) SaveSyncState(ctx context.Context, key string, val time.Time) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT INTO sync_state (key, value) VALUES ($1, $2)
         ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
        key, val.Format(time.RFC3339))
    return err
}
```

**Step 2: Add sync_state table to migrations**

Create `pkg/data/postgres/sql/migrations/002_sync_state.sql`:

```sql
CREATE TABLE IF NOT EXISTS sync_state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

**Step 3: Write integration test** (skip without DB)

Test `GetDevPulseUpdatedDevelopers` — may return empty if no DevPulse data locally, that's OK. Test `SyncDeveloperToDevTrace` with a mock developer. Test `GetSyncState`/`SaveSyncState` round-trip.

**Step 4: Commit**

```bash
git add pkg/data/postgres/
git commit -S -m "Add DevPulse sync data access layer"
```

---

## Task 3: DevPulse Sync — Background Routine

Background goroutine that runs on a configurable interval (default 30 min).

**Files:**
- Create: `pkg/background/sync.go`
- Create: `pkg/background/sync_test.go`

**Step 1: Implement sync routine**

```go
// pkg/background/sync.go
package background

import (
    "context"
    "log/slog"
    "time"

    "github.com/thingzio/devtrace/pkg/config"
    "github.com/thingzio/devtrace/pkg/data/postgres"
    "github.com/thingzio/devtrace/pkg/score"
)

const (
    syncStateKey    = "devpulse_sync"
    defaultSyncSec  = 1800 // 30 minutes
    syncBatchSize   = 500
)

// StartDevPulseSync runs a background loop that copies developer scores
// from DevPulse's developer table into DevTrace's contributor + reputation tables.
// Returns a cancel function to stop the loop.
func StartDevPulseSync(ctx context.Context, store *postgres.Store) func() {
    interval := time.Duration(config.GetEnvAsInt("DEVPULSE_SYNC_INTERVAL_SEC", defaultSyncSec)) * time.Second
    slog.Info("starting devpulse sync", "interval", interval)

    ctx, cancel := context.WithCancel(ctx)

    go func() {
        // Run immediately on startup
        runSync(ctx, store)

        ticker := time.NewTicker(interval)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                slog.Info("devpulse sync stopped")
                return
            case <-ticker.C:
                runSync(ctx, store)
            }
        }
    }()

    return cancel
}

func runSync(ctx context.Context, store *postgres.Store) {
    since, err := store.GetSyncState(ctx, syncStateKey)
    if err != nil {
        slog.Error("get sync state", "error", err)
        return
    }

    slog.Debug("running devpulse sync", "since", since)

    devs, err := store.GetDevPulseUpdatedDevelopers(ctx, since, syncBatchSize)
    if err != nil {
        slog.Error("fetch devpulse developers", "error", err)
        return
    }

    if len(devs) == 0 {
        slog.Debug("devpulse sync: no updates")
        return
    }

    var synced int
    var lastUpdated time.Time

    for _, d := range devs {
        grade := score.Grade(d.Reputation)
        if err := store.SyncDeveloperToDevTrace(ctx, d, grade); err != nil {
            slog.Error("sync developer", "username", d.Username, "error", err)
            continue
        }
        synced++
        if d.UpdatedAt.After(lastUpdated) {
            lastUpdated = d.UpdatedAt
        }
    }

    if !lastUpdated.IsZero() {
        if err := store.SaveSyncState(ctx, syncStateKey, lastUpdated); err != nil {
            slog.Error("save sync state", "error", err)
        }
    }

    slog.Info("devpulse sync complete", "synced", synced, "total", len(devs))
}
```

**Step 2: Write test**

Test `runSync` with a mock store (or test the sync state logic with unit tests).

**Step 3: Commit**

```bash
git add pkg/background/
git commit -S -m "Add DevPulse sync background routine (30 min default)"
```

---

## Task 4: Background Scorer — Rescore Stale Contributors

Background goroutine that finds stale contributors in DevTrace's own tables and rescores them via GitHub API.

**Files:**
- Create: `pkg/background/scorer.go`
- Create: `pkg/data/postgres/stale.go`

**Step 1: Add stale contributor query**

```go
// pkg/data/postgres/stale.go
package postgres

import (
    "context"
    "fmt"
    "time"
)

type StaleContributor struct {
    Username string
    Provider string
    Score    float64
    ScoredAt time.Time
}

// GetStaleContributors returns contributors whose scores are older than the
// configured thresholds. Low scores (<0.5) stale after 7 days, high scores
// (>=0.5) stale after 30 days.
func (s *Store) GetStaleContributors(ctx context.Context, lowThresholdDays, highThresholdDays int, limit int) ([]StaleContributor, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT r.username, r.provider, r.score, r.scored_at
         FROM reputation r
         WHERE (r.score < 0.5 AND r.scored_at < NOW() - MAKE_INTERVAL(days => $1))
            OR (r.score >= 0.5 AND r.scored_at < NOW() - MAKE_INTERVAL(days => $2))
         ORDER BY r.scored_at ASC
         LIMIT $3`, lowThresholdDays, highThresholdDays, limit)
    if err != nil {
        return nil, fmt.Errorf("query stale contributors: %w", err)
    }
    defer rows.Close()

    var result []StaleContributor
    for rows.Next() {
        var c StaleContributor
        if err := rows.Scan(&c.Username, &c.Provider, &c.Score, &c.ScoredAt); err != nil {
            return nil, fmt.Errorf("scan stale contributor: %w", err)
        }
        result = append(result, c)
    }
    return result, rows.Err()
}
```

**Step 2: Implement scorer routine**

```go
// pkg/background/scorer.go
package background

import (
    "context"
    "log/slog"
    "time"

    "github.com/thingzio/devtrace/pkg/config"
    "github.com/thingzio/devtrace/pkg/data/postgres"
    ghclient "github.com/thingzio/devtrace/pkg/github"
    "github.com/thingzio/devtrace/pkg/score"
)

const (
    defaultScorerSec   = 3600 // 1 hour
    scorerBatchSize    = 100
    defaultLowDays     = 7
    defaultHighDays    = 30
)

// StartBackgroundScorer rescores stale contributors on a schedule.
func StartBackgroundScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client) func() {
    interval := time.Duration(config.GetEnvAsInt("SCORER_INTERVAL_SEC", defaultScorerSec)) * time.Second
    slog.Info("starting background scorer", "interval", interval)

    ctx, cancel := context.WithCancel(ctx)

    go func() {
        // Don't run immediately on startup — let sync populate first
        ticker := time.NewTicker(interval)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                slog.Info("background scorer stopped")
                return
            case <-ticker.C:
                runScorer(ctx, store, gh)
            }
        }
    }()

    return cancel
}

func runScorer(ctx context.Context, store *postgres.Store, gh ghclient.Client) {
    lowDays := config.GetEnvAsInt("SCORER_LOW_STALE_DAYS", defaultLowDays)
    highDays := config.GetEnvAsInt("SCORER_HIGH_STALE_DAYS", defaultHighDays)

    stale, err := store.GetStaleContributors(ctx, lowDays, highDays, scorerBatchSize)
    if err != nil {
        slog.Error("fetch stale contributors", "error", err)
        return
    }

    if len(stale) == 0 {
        slog.Debug("background scorer: no stale contributors")
        return
    }

    var scored int
    for _, c := range stale {
        signals, err := gh.FetchSignals(ctx, c.Username, "")
        if err != nil {
            slog.Warn("scorer fetch signals", "username", c.Username, "error", err)
            continue
        }

        value := score.Compute(*signals)
        grade := score.Grade(value)

        if err := store.SaveScoreHistory(ctx, c.Username, c.Provider, value, grade, true); err != nil {
            slog.Warn("scorer save history", "username", c.Username, "error", err)
        }

        // Update reputation table
        if err := store.UpdateReputation(ctx, c.Username, c.Provider, value, grade, signals); err != nil {
            slog.Warn("scorer update reputation", "username", c.Username, "error", err)
            continue
        }

        scored++
    }

    slog.Info("background scorer complete", "scored", scored, "total", len(stale))
}
```

**Step 3: Add UpdateReputation to store**

```go
// pkg/data/postgres/stale.go (append)
func (s *Store) UpdateReputation(ctx context.Context, username, provider string, value float64, grade string, signals *score.InputSignals) error {
    signalsJSON, _ := json.Marshal(signals)
    _, err := s.db.ExecContext(ctx,
        `UPDATE reputation SET score = $1, grade = $2, deep = true, signals = $3::jsonb, scored_at = NOW()
         WHERE username = $4 AND provider = $5`,
        value, grade, signalsJSON, username, provider)
    return err
}
```

**Step 4: Write tests**

Test `GetStaleContributors` (integration, skip without DB). Test `runScorer` with mock GitHub client.

**Step 5: Commit**

```bash
git add pkg/background/ pkg/data/postgres/
git commit -S -m "Add background scorer for stale contributors"
```

---

## Task 5: Wire Background Routines into Server

Start both background routines from the server's `Run()` function.

**Files:**
- Modify: `pkg/server/server.go`

**Step 1: Start routines in Run()**

After store, GitHub client, and router are initialized, start background routines:

```go
// Start background operations
syncStop := background.StartDevPulseSync(ctx, store)
defer syncStop()

scorerStop := background.StartBackgroundScorer(ctx, store, ghClient)
defer scorerStop()
```

Gate on a config flag so they can be disabled for testing:

```go
if config.GetEnvBool("ENABLE_BACKGROUND_OPS") {
    syncStop := background.StartDevPulseSync(ctx, store)
    defer syncStop()

    scorerStop := background.StartBackgroundScorer(ctx, store, ghClient)
    defer scorerStop()
}
```

Default: disabled (safe for local dev). Enabled in production via env var.

**Step 2: Import the background package**

Add `"github.com/thingzio/devtrace/pkg/background"` to imports.

**Step 3: Update LOCAL_TESTING.md**

Add env vars:
```
ENABLE_BACKGROUND_OPS=true     # Enable sync + scorer (default: false)
DEVPULSE_SYNC_INTERVAL_SEC=1800  # Sync every 30 min (default)
SCORER_INTERVAL_SEC=3600         # Score every 60 min (default)
SCORER_LOW_STALE_DAYS=7          # Rescore low scores after 7 days
SCORER_HIGH_STALE_DAYS=30        # Rescore high scores after 30 days
```

**Step 4: Run all tests, lint**

```bash
go test ./... -v -race
golangci-lint -c .golangci.yaml run --timeout=5m
```

**Step 5: Commit**

```bash
git add pkg/server/ docs/
git commit -S -m "Wire background sync and scorer into server"
```

---

## Task 6: End-to-End Verification

**Step 1: Run full test suite**

```bash
make test
make lint
```

**Step 2: Local test with background ops**

```bash
make db-up
GITHUB_TOKEN=ghp_... ENABLE_BACKGROUND_OPS=true DEVPULSE_SYNC_INTERVAL_SEC=60 go run ./cmd/devtrace-site/
```

Watch logs for:
```
{"level":"INFO","msg":"starting devpulse sync","interval":"1m0s"}
{"level":"INFO","msg":"starting background scorer","interval":"1h0m0s"}
{"level":"DEBUG","msg":"devpulse sync: no updates"}
```

(Sync will find no DevPulse data locally — that's expected. In production with shared DB, it will find developers.)

**Step 3: Test scoring still works**

```bash
curl -s http://localhost:8080/api/v1/score/octocat | jq .score
```

**Step 4: Verify Terraform**

```bash
cd infra/saas && terraform fmt -check && cd ../..
```

**Step 5: Commit any fixes**

```bash
git add -A
git commit -S -m "Phase 4 end-to-end verification fixes"
```

---

## Phase 4 Complete Checklist

- [ ] Terraform aligned: project `thingzio`, VPC `thingzio-vpc`, Cloud SQL `thingzio-pg`, state in `thingzio-infra-state/devtrace`
- [ ] Cloud Run config: SQL socket volumes, VPC access, secret mounts (same pattern as DevPulse)
- [ ] DevPulse sync: reads `developer` table, upserts into `contributor` + `reputation` + `reputation_history`
- [ ] Sync state tracked in `sync_state` table (last synced timestamp)
- [ ] Sync interval configurable via `DEVPULSE_SYNC_INTERVAL_SEC` (default 1800 = 30 min)
- [ ] Background scorer: finds stale contributors, rescores via GitHub API
- [ ] Scorer interval configurable via `SCORER_INTERVAL_SEC` (default 3600 = 1 hour)
- [ ] Stale thresholds configurable: `SCORER_LOW_STALE_DAYS` (7), `SCORER_HIGH_STALE_DAYS` (30)
- [ ] Background ops gated on `ENABLE_BACKGROUND_OPS` env var
- [ ] .goreleaser.yaml + CI/CD updated for `thingzio` project
- [ ] All tests pass, lint clean

---

## What's Next

- **GitHub App setup** — create DevTrace GitHub App, configure locally, test full OAuth + App installation flow
- **Production deploy** — apply Terraform, deploy to Cloud Run, enable background ops
- **Phase 5: Admin service** — separate Cloud Run service for operator visibility
