# Phase 1: API + Scoring Engine — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver a working REST API that scores any GitHub contributor's reputation, running locally with docker-compose Postgres and a PAT-backed GitHub client.

**Architecture:** Single `devtrace-site` binary serving a REST API. PostgreSQL for persistence. Reputer v3.2.0 scoring model copied and adapted. GitHub client abstracted behind an interface (PAT in dev, App installation tokens in prod). Plan-aware response enrichment on `/api/v1/score/{username}`.

**Tech Stack:** Go 1.26, PostgreSQL 16, `net/http` (stdlib router), `github.com/google/go-github/v83`, `github.com/lib/pq`, `slog` structured logging. Copy build toolchain from DevPulse (`thingzio/devpulse`).

**Infra note:** Same GCP project as DevPulse. Terraform references existing VPC, Cloud SQL, and private networking via `data` sources (owned by DevPulse's Terraform — ideally these would live in a shared infra repo, but we reuse DevPulse's as-is). DevTrace gets its own Cloud Run services, service accounts (`devtrace-saas-run`), Artifact Registry repo, secrets, and WIF provider. It connects to the shared Cloud SQL instance as its own DB user (`devtrace`) with read grants on DevPulse tables (`developer`, `event`, `repo_meta`) to reuse existing deep reputation state. DevTrace-owned tables are separate — no writes to DevPulse tables.

---

## Task 1: Project Scaffolding — Build Toolchain

Copy generic build infrastructure from DevPulse. All paths relative to repo root.

**Files:**
- Create: `go.mod`
- Create: `.settings.yaml`
- Create: `.golangci.yaml`
- Create: `.yamllint.yaml`
- Create: `.goreleaser.yaml`
- Create: `Makefile`
- Create: `docker-compose.yaml`
- Create: `.gitignore`

**Step 1: Initialize Go module**

```bash
go mod init github.com/thingzio/devtrace
```

**Step 2: Copy and adapt .settings.yaml**

Copy from `/Users/mchmarny/dev/thingz/devpulse/.settings.yaml`. Same versions — these are tool versions, not app versions.

```yaml
languages:
  go: '1.26'

build_tools:
  goreleaser: 'v2.15.2'
  ko: 'v0.18.0'
  tfsec: 'v1.28.14'

linting:
  golangci_lint: 'v2.11.3'

quality:
  coverage_threshold: '33'
  lint_timeout: '5m'
  test_timeout: '10m'
  build_timeout: '10m0s'
```

**Step 3: Copy linter and YAML configs as-is**

- `.golangci.yaml` — copy from DevPulse, no changes needed (linter rules are project-agnostic)
- `.yamllint.yaml` — copy as-is

**Step 4: Create docker-compose.yaml**

```yaml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_DB: devtrace
      POSTGRES_USER: devtrace
      POSTGRES_PASSWORD: devtrace
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "devtrace"]
      interval: 2s
      timeout: 5s
      retries: 5

volumes:
  pgdata:
```

**Step 5: Create Makefile**

Copy from DevPulse. Rename binary targets:
- `devpulse-site` → `devtrace-site`
- `devpulse-import` → `devtrace-scorer` (Phase 1 only builds `devtrace-site`)
- `devpulse-admin` → `devtrace-admin`
- Database name: `devtrace`
- Remove import-specific targets for now (add in later phases)

Key targets to preserve: `tidy`, `lint`, `test`, `test-coverage`, `vulncheck`, `qualify`, `db-up`, `db-down`, `db-connect`, `server`, `build`, `release`, `bump-*`, `setup`, `tf-init`, `tf-plan`, `tf-apply`.

**Step 6: Create .goreleaser.yaml**

Copy from DevPulse. Phase 1 only builds one binary:

```yaml
builds:
  - id: devtrace-site
    binary: devtrace-site
    dir: ./cmd/devtrace-site
    ldflags:
      - -s -w
        -X main.version={{.Version}}
        -X main.commit={{.ShortCommit}}
        -X main.date={{.CommitDate}}
    goos: [linux]
    goarch: [amd64, arm64]

kos:
  - id: devtrace-site
    build: devtrace-site
    repositories:
      - us-west1-docker.pkg.dev/devpulseio/devtrace-saas-images/devtrace-site
    platforms: [linux/amd64]
    tags: [latest, "{{.Tag}}"]
```

**Step 7: Create .gitignore**

```
.DS_Store
*.exe
/vendor/
dist/
coverage.out
```

**Step 8: Verify scaffolding**

Run: `make tidy` (will fail until we have Go source files — that's expected, just verify Makefile parses)

**Step 9: Commit**

```bash
git add -A
git commit -S -m "Add project scaffolding from DevPulse patterns"
```

---

## Task 2: Core Packages — Config, Logging, Health

Foundation packages that every other package depends on.

**Files:**
- Create: `pkg/config/env.go`
- Create: `pkg/config/env_test.go`
- Create: `pkg/logging/cli.go`
- Create: `pkg/logging/cli_test.go`
- Create: `pkg/health/health.go`

**Step 1: Write test for config helpers**

```go
// pkg/config/env_test.go
package config

import (
    "os"
    "testing"
)

func TestGetEnv(t *testing.T) {
    t.Setenv("TEST_KEY", "value")
    if got := GetEnv("TEST_KEY", "default"); got != "value" {
        t.Errorf("got %q, want %q", got, "value")
    }
    if got := GetEnv("MISSING_KEY", "default"); got != "default" {
        t.Errorf("got %q, want %q", got, "default")
    }
}

func TestGetEnvAsInt(t *testing.T) {
    t.Setenv("TEST_INT", "42")
    if got := GetEnvAsInt("TEST_INT", 0); got != 42 {
        t.Errorf("got %d, want 42", got)
    }
    if got := GetEnvAsInt("MISSING_INT", 10); got != 10 {
        t.Errorf("got %d, want 10", got)
    }
}

func TestGetEnvBool(t *testing.T) {
    t.Setenv("TEST_BOOL", "true")
    if !GetEnvBool("TEST_BOOL") {
        t.Error("expected true")
    }
    if GetEnvBool("MISSING_BOOL") {
        t.Error("expected false")
    }
}

func TestGetEnvAsFloat(t *testing.T) {
    t.Setenv("TEST_FLOAT", "0.75")
    if got := GetEnvAsFloat("TEST_FLOAT", 0.5); got != 0.75 {
        t.Errorf("got %f, want 0.75", got)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/config/ -v`
Expected: FAIL — package doesn't exist yet

**Step 3: Implement config helpers**

Copy `GetEnv`, `GetEnvAsInt`, `GetEnvBool`, `GetEnvAsFloat` from DevPulse `pkg/config/env.go`. Add DevTrace-specific helpers:

```go
// pkg/config/env.go
package config

import (
    "os"
    "strconv"
    "strings"
)

func GetEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}

func GetEnvAsInt(key string, fallback int) int {
    v := os.Getenv(key)
    if v == "" {
        return fallback
    }
    i, err := strconv.Atoi(v)
    if err != nil || i < 1 {
        return fallback
    }
    return i
}

func GetEnvBool(key string) bool {
    v := strings.ToLower(os.Getenv(key))
    return v == "true" || v == "1"
}

func GetEnvAsFloat(key string, fallback float64) float64 {
    v := os.Getenv(key)
    if v == "" {
        return fallback
    }
    f, err := strconv.ParseFloat(v, 64)
    if err != nil {
        return fallback
    }
    return f
}

func DebugEnabled() bool {
    return GetEnvBool("DEVTRACE_DEBUG")
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/config/ -v`
Expected: PASS

**Step 5: Create logging package**

Copy from DevPulse `pkg/logging/cli.go`:

```go
// pkg/logging/cli.go
package logging

import (
    "log/slog"
    "os"

    "github.com/thingzio/devtrace/pkg/config"
)

func SetupLogger() {
    level := slog.LevelInfo
    if config.DebugEnabled() {
        level = slog.LevelDebug
    }
    handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
    slog.SetDefault(slog.New(handler))
}
```

**Step 6: Write and run logging test**

```go
// pkg/logging/cli_test.go
package logging

import "testing"

func TestSetupLogger(t *testing.T) {
    SetupLogger() // should not panic
}

func TestSetupLoggerDebug(t *testing.T) {
    t.Setenv("DEVTRACE_DEBUG", "true")
    SetupLogger() // should not panic
}
```

Run: `go test ./pkg/logging/ -v`
Expected: PASS

**Step 7: Create health endpoint handler**

Simple health check (LB probe only, no scoring logic — that's DevPulse-specific):

```go
// pkg/health/health.go
package health

import "net/http"

func Handler() http.HandlerFunc {
    return func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
    }
}
```

**Step 8: Run all tests**

Run: `go test ./... -v -race`
Expected: PASS

**Step 9: Commit**

```bash
git add pkg/config/ pkg/logging/ pkg/health/
git commit -S -m "Add config, logging, and health packages"
```

---

## Task 3: Database Schema + Store

DevTrace-owned tables in PostgreSQL. Migration runner pattern from DevPulse.

**Files:**
- Create: `pkg/data/postgres/sql/migrations/001_initial.sql`
- Create: `pkg/data/postgres/postgres.go`
- Create: `pkg/data/postgres/postgres_test.go`
- Create: `pkg/data/postgres/migrate.go`
- Create: `pkg/data/postgres/migrate_test.go`

**Step 1: Design and create initial migration**

```sql
-- pkg/data/postgres/sql/migrations/001_initial.sql

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Contributor profiles (provider-agnostic identity)
CREATE TABLE contributor (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    display_name TEXT,
    email TEXT,
    avatar_url TEXT,
    company TEXT,
    location TEXT,
    bio TEXT,
    account_created_at TIMESTAMPTZ,
    suspended BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

-- Reputation scores
CREATE TABLE reputation (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    score REAL NOT NULL,
    grade TEXT NOT NULL,
    model_version TEXT NOT NULL,
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    categories JSONB,
    signals JSONB,
    risk_summary TEXT,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX idx_reputation_score ON reputation(score);
CREATE INDEX idx_reputation_scored_at ON reputation(scored_at);

-- Reputation history (for trend charts)
CREATE TABLE reputation_history (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    score REAL NOT NULL,
    grade TEXT NOT NULL,
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX idx_reputation_history_lookup ON reputation_history(username, provider, scored_at);

-- License profiles
CREATE TABLE license_profile (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    total_repos_with_merged_prs INTEGER NOT NULL DEFAULT 0,
    own_repos INTEGER NOT NULL DEFAULT 0,
    distribution JSONB,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

-- AI sensing signals
CREATE TABLE ai_signal (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    co_authored_commits INTEGER NOT NULL DEFAULT 0,
    bot_associated_prs INTEGER NOT NULL DEFAULT 0,
    known_tool_signatures JSONB,
    total_commits_analyzed INTEGER NOT NULL DEFAULT 0,
    ai_associated_ratio REAL NOT NULL DEFAULT 0,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

-- Tenants (registered users)
CREATE TABLE tenant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id BIGINT UNIQUE NOT NULL,
    username TEXT NOT NULL,
    email TEXT,
    avatar_url TEXT,
    name TEXT,
    company TEXT,
    location TEXT,
    bio TEXT,
    plan TEXT NOT NULL DEFAULT 'free',
    max_contributors INTEGER NOT NULL DEFAULT 50,
    tos_accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- API tokens (DevTrace-minted)
CREATE TABLE api_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_token_hash ON api_token(token_hash);
CREATE INDEX idx_api_token_tenant ON api_token(tenant_id);

-- Sessions (UI auth)
CREATE TABLE session (
    id TEXT PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- GitHub App installations
CREATE TABLE github_app_installation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    installation_id BIGINT UNIQUE NOT NULL,
    target_type TEXT,
    target_login TEXT,
    permissions JSONB,
    suspended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Usage tracking (quota enforcement)
CREATE TABLE usage_record (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    username_scored TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_tenant_period ON usage_record(tenant_id, scored_at);

-- Rate limit tracking (IP-based for unauth)
CREATE TABLE rate_limit (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    window_start TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Cross-schema read access to DevPulse tables (shared Cloud SQL instance).
-- DevTrace reads but NEVER writes to these tables.
-- In local dev, these tables won't exist (DevTrace works standalone).
-- In prod, the devtrace DB user gets SELECT grants on the devpulse schema:
--   GRANT SELECT ON devpulse.developer TO devtrace;
--   GRANT SELECT ON devpulse.event TO devtrace;
--   GRANT SELECT ON devpulse.repo_meta TO devtrace;
-- This allows DevTrace to reuse existing deep reputation scores and
-- contributor data already collected by DevPulse's import pipeline.
```

**Step 2: Create store with connection pool**

```go
// pkg/data/postgres/postgres.go
package postgres

import (
    "database/sql"
    "fmt"
    "time"

    _ "github.com/lib/pq"

    "github.com/thingzio/devtrace/pkg/config"
)

type Store struct {
    db   *sql.DB
}

type PoolConfig struct {
    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
    ConnMaxIdleTime time.Duration
}

func DefaultPoolConfig() PoolConfig {
    return PoolConfig{
        MaxOpenConns:    config.GetEnvAsInt("DB_MAX_OPEN_CONNS", 10),
        MaxIdleConns:    config.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5),
        ConnMaxLifetime: 30 * time.Minute,
        ConnMaxIdleTime: 5 * time.Minute,
    }
}

func New(dsn string, cfg PoolConfig) (*Store, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, fmt.Errorf("open db: %w", err)
    }

    db.SetMaxOpenConns(cfg.MaxOpenConns)
    db.SetMaxIdleConns(cfg.MaxIdleConns)
    db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
    db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

    if err := db.Ping(); err != nil {
        return nil, fmt.Errorf("ping db: %w", err)
    }

    return &Store{db: db}, nil
}

func NewFromEnv() (*Store, error) {
    dsn := config.GetEnv("DATABASE_URL", "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable")
    return New(dsn, DefaultPoolConfig())
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }
```

**Step 3: Create migration runner**

```go
// pkg/data/postgres/migrate.go
package postgres

import (
    "context"
    "embed"
    "fmt"
    "log/slog"
    "sort"
    "strconv"
    "strings"
)

//go:embed sql/migrations/*.sql
var migrationsFS embed.FS

func (s *Store) Migrate(ctx context.Context) error {
    entries, err := migrationsFS.ReadDir("sql/migrations")
    if err != nil {
        return fmt.Errorf("read migrations dir: %w", err)
    }

    sort.Slice(entries, func(i, j int) bool {
        return entries[i].Name() < entries[j].Name()
    })

    for _, entry := range entries {
        name := entry.Name()
        if !strings.HasSuffix(name, ".sql") {
            continue
        }

        version, err := strconv.Atoi(strings.Split(name, "_")[0])
        if err != nil {
            return fmt.Errorf("parse migration version %q: %w", name, err)
        }

        applied, err := s.migrationApplied(ctx, version)
        if err != nil {
            return fmt.Errorf("check migration %d: %w", version, err)
        }
        if applied {
            continue
        }

        data, err := migrationsFS.ReadFile("sql/migrations/" + name)
        if err != nil {
            return fmt.Errorf("read migration %q: %w", name, err)
        }

        slog.Info("applying migration", "version", version, "file", name)

        if _, err := s.db.ExecContext(ctx, string(data)); err != nil {
            return fmt.Errorf("apply migration %q: %w", name, err)
        }

        if _, err := s.db.ExecContext(ctx,
            "INSERT INTO schema_version (version) VALUES ($1) ON CONFLICT DO NOTHING", version); err != nil {
            return fmt.Errorf("record migration %d: %w", version, err)
        }
    }

    return nil
}

func (s *Store) migrationApplied(ctx context.Context, version int) (bool, error) {
    // schema_version table may not exist yet (first run)
    var exists bool
    err := s.db.QueryRowContext(ctx,
        "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_version')").Scan(&exists)
    if err != nil {
        return false, err
    }
    if !exists {
        return false, nil
    }

    var count int
    err = s.db.QueryRowContext(ctx,
        "SELECT COUNT(*) FROM schema_version WHERE version = $1", version).Scan(&count)
    return count > 0, err
}
```

**Step 4: Write integration test**

```go
// pkg/data/postgres/postgres_test.go
package postgres_test

import (
    "context"
    "os"
    "testing"

    "github.com/thingzio/devtrace/pkg/data/postgres"
)

func testStore(t *testing.T) *postgres.Store {
    t.Helper()
    dsn := os.Getenv("DATABASE_URL")
    if dsn == "" {
        dsn = "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable"
    }
    store, err := postgres.New(dsn, postgres.DefaultPoolConfig())
    if err != nil {
        t.Skipf("skipping integration test: %v", err)
    }
    t.Cleanup(func() { store.Close() })
    return store
}
```

```go
// pkg/data/postgres/migrate_test.go
package postgres_test

import (
    "context"
    "testing"
)

func TestMigrate(t *testing.T) {
    store := testStore(t)
    ctx := context.Background()
    if err := store.Migrate(ctx); err != nil {
        t.Fatalf("migrate: %v", err)
    }
    // Run again to verify idempotency
    if err := store.Migrate(ctx); err != nil {
        t.Fatalf("migrate (idempotent): %v", err)
    }
}
```

**Step 5: Run tests**

Run: `make db-up && go test ./pkg/data/postgres/ -v -run TestMigrate`
Expected: PASS (migrations apply, idempotent re-run succeeds)

**Step 6: Commit**

```bash
git add pkg/data/
git commit -S -m "Add database schema, store, and migration runner"
```

---

## Task 4: Domain Types + Scoring Model

Copy reputer scoring model, define DevTrace domain types.

**Files:**
- Create: `pkg/score/score.go`
- Create: `pkg/score/score_test.go`
- Create: `pkg/score/grade.go`
- Create: `pkg/score/grade_test.go`
- Create: `pkg/model/types.go`
- Create: `pkg/plan/plan.go`
- Create: `pkg/plan/plan_test.go`

**Step 1: Define domain types**

```go
// pkg/model/types.go
package model

import "time"

type Provider string

const ProviderGitHub Provider = "github"

type ContributorID struct {
    Username string   `json:"username"`
    Provider Provider `json:"provider"`
}

type ScoreResponse struct {
    Username  string        `json:"username"`
    Provider  Provider      `json:"provider"`
    Score     *Score        `json:"score"`
    Signals   *Signals      `json:"signals,omitempty"`
    RiskSummary string      `json:"risk_summary,omitempty"`
    RepoContext *RepoContext `json:"repo_context,omitempty"`
    License   *License      `json:"license,omitempty"`
    AISensing *AISensing    `json:"ai_sensing,omitempty"`
    ScoredAt  time.Time     `json:"scored_at"`
    CachedAt  *time.Time    `json:"cached_at,omitempty"`
    Detail    string        `json:"detail,omitempty"`
}

type Score struct {
    Grade        string             `json:"grade"`
    Value        float64            `json:"value"`
    ModelVersion string             `json:"model_version"`
    Categories   map[string]float64 `json:"categories,omitempty"`
}

type Signals struct {
    AccountAgeDays      int64  `json:"account_age_days"`
    Followers           int64  `json:"followers"`
    Following           int64  `json:"following"`
    PublicRepos         int64  `json:"public_repos"`
    ForkedRepos         int64  `json:"forked_repos"`
    PRsMerged           int64  `json:"prs_merged"`
    PRsClosed           int64  `json:"prs_closed"`
    RecentPRRepoCount   int64  `json:"recent_pr_repo_count"`
    HasBio              bool   `json:"has_bio"`
    HasCompany          bool   `json:"has_company"`
    HasLocation         bool   `json:"has_location"`
    HasWebsite          bool   `json:"has_website"`
    OrgMember           bool   `json:"org_member"`
    Suspended           bool   `json:"suspended"`
    AuthorAssociation   string `json:"author_association"`
    CommitsVerified     bool   `json:"commits_verified"`
}

type RepoContext struct {
    Repo               string  `json:"repo"`
    Commits            int64   `json:"commits"`
    TotalCommits       int64   `json:"total_commits"`
    TotalContributors  int     `json:"total_contributors"`
    LastCommitDays     *int64  `json:"last_commit_days"`
    OrgMember          bool    `json:"org_member"`
    AuthorAssociation  string  `json:"author_association"`
    TrustedOrgMember   bool    `json:"trusted_org_member"`
}

type License struct {
    TotalReposWithMergedPRs int                `json:"total_repos_with_merged_prs"`
    OwnRepos                int                `json:"own_repos"`
    Distribution            []LicenseEntry     `json:"distribution"`
}

type LicenseEntry struct {
    License     string `json:"license"`
    Count       int    `json:"count"`
    Own         int    `json:"own"`
    Contributed int    `json:"contributed"`
}

type AISensing struct {
    CoAuthoredCommits    int      `json:"co_authored_commits"`
    BotAssociatedPRs     int      `json:"bot_associated_prs"`
    KnownToolSignatures  []string `json:"known_tool_signatures"`
    TotalCommitsAnalyzed int      `json:"total_commits_analyzed"`
    AIAssociatedRatio    float64  `json:"ai_associated_ratio"`
}
```

**Step 2: Write scoring tests**

Copy the reputer v3.2.0 scoring model from `/Users/mchmarny/dev/thingz/devpulse/vendor/github.com/mchmarny/reputer/pkg/score/score.go`. Adapt into DevTrace's own package.

```go
// pkg/score/score_test.go
package score

import "testing"

func TestComputeSuspended(t *testing.T) {
    s := InputSignals{Suspended: true, AgeDays: 365, PublicRepos: 10}
    if got := Compute(s); got != 0 {
        t.Errorf("suspended account: got %f, want 0", got)
    }
}

func TestComputeEstablished(t *testing.T) {
    s := InputSignals{
        AgeDays: 1095, Followers: 42, Following: 18, PublicRepos: 23,
        HasBio: true, HasCompany: true, HasLocation: true, HasWebsite: true,
        PRsMerged: 87, PRsClosed: 12, Commits: 50, TotalCommits: 200,
        TotalContributors: 10, LastCommitDays: 5,
        AuthorAssociation: "CONTRIBUTOR",
    }
    got := Compute(s)
    if got < 0.5 || got > 1.0 {
        t.Errorf("established contributor: got %f, want 0.5-1.0", got)
    }
}

func TestComputeNewAccount(t *testing.T) {
    s := InputSignals{AgeDays: 7, PublicRepos: 0}
    got := Compute(s)
    if got > 0.3 {
        t.Errorf("new empty account: got %f, want < 0.3", got)
    }
}

func TestComputeBounds(t *testing.T) {
    // Score should always be in [0.0, 1.0]
    cases := []InputSignals{
        {}, // zero values
        {AgeDays: 100000, Followers: 100000, PublicRepos: 100000, PRsMerged: 100000}, // extreme values
    }
    for i, s := range cases {
        got := Compute(s)
        if got < 0 || got > 1 {
            t.Errorf("case %d: got %f, want [0, 1]", i, got)
        }
    }
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./pkg/score/ -v`
Expected: FAIL

**Step 4: Implement scoring model**

Copy the 5-category weighted model from reputer v3.2.0 (`score.Compute` function and all helper functions). The `InputSignals` struct maps to reputer's `Signals` struct. Key implementation:

```go
// pkg/score/score.go
package score

import "math"

const ModelVersion = "3.2.0"

// Category weights (sum to 1.0)
const (
    WeightProvenance  = 0.15
    WeightIdentity    = 0.25
    WeightEngagement  = 0.25
    WeightCommunity   = 0.15
    WeightBehavioral  = 0.20
)

type InputSignals struct {
    // Identity
    AgeDays           int64
    AuthorAssociation string
    HasBio            bool
    HasCompany        bool
    HasLocation       bool
    HasWebsite        bool

    // Engagement
    Commits           int64
    TotalCommits      int64
    TotalContributors int
    LastCommitDays    int64
    PRsMerged         int64
    PRsClosed         int64

    // Community
    Followers  int64
    Following  int64
    PublicRepos int64

    // Behavioral
    RecentPRRepoCount int64
    ForkedRepos       int64

    // Provenance
    UnverifiedCommits int64

    // Flags
    Suspended        bool
    OrgMember        bool
    TrustedOrgMember bool
}

func Compute(s InputSignals) float64 {
    if s.Suspended {
        return 0
    }

    provenance := computeProvenance(s)
    identity := computeIdentity(s)
    engagement := computeEngagement(s)
    community := computeCommunity(s)
    behavioral := computeBehavioral(s)

    total := provenance*WeightProvenance +
        identity*WeightIdentity +
        engagement*WeightEngagement +
        community*WeightCommunity +
        behavioral*WeightBehavioral

    return clamp(total, 0, 1)
}

func Categories(s InputSignals) map[string]float64 {
    return map[string]float64{
        "code_provenance": clamp(computeProvenance(s), 0, 1),
        "identity":        clamp(computeIdentity(s), 0, 1),
        "engagement":      clamp(computeEngagement(s), 0, 1),
        "community":       clamp(computeCommunity(s), 0, 1),
        "behavioral":      clamp(computeBehavioral(s), 0, 1),
    }
}

// Copy exact computation functions from reputer v3.2.0:
// computeProvenance, computeIdentity, computeEngagement,
// computeCommunity, computeBehavioral, and all helper
// functions (logCurve, clamp, maturityFactor, etc.)

func logCurve(value, ceiling float64) float64 {
    if ceiling <= 0 {
        return 0
    }
    return math.Min(math.Log1p(value)/math.Log1p(ceiling), 1.0)
}

func clamp(v, min, max float64) float64 {
    if v < min {
        return min
    }
    if v > max {
        return max
    }
    return v
}

// ... (full implementation copied from reputer, adapted to use InputSignals)
```

Note: copy the exact mathematical formulas from reputer's `score.go` — the log curves, ceilings, and weight distributions. This is a faithful port, not a reimplementation.

**Step 5: Run test to verify it passes**

Run: `go test ./pkg/score/ -v`
Expected: PASS

**Step 6: Write grade tests and implementation**

```go
// pkg/score/grade_test.go
package score

import "testing"

func TestGrade(t *testing.T) {
    cases := []struct{ score float64; want string }{
        {0.95, "A+"}, {0.90, "A"}, {0.85, "A-"},
        {0.80, "B+"}, {0.75, "B"}, {0.70, "B-"},
        {0.65, "C+"}, {0.60, "C"}, {0.55, "C-"},
        {0.50, "D+"}, {0.45, "D"}, {0.40, "D-"},
        {0.30, "F"}, {0.0, "F"},
    }
    for _, tc := range cases {
        if got := Grade(tc.score); got != tc.want {
            t.Errorf("Grade(%f) = %q, want %q", tc.score, got, tc.want)
        }
    }
}
```

```go
// pkg/score/grade.go
package score

func Grade(score float64) string {
    switch {
    case score >= 0.93: return "A+"
    case score >= 0.87: return "A"
    case score >= 0.83: return "A-"
    case score >= 0.77: return "B+"
    case score >= 0.73: return "B"
    case score >= 0.67: return "B-"
    case score >= 0.63: return "C+"
    case score >= 0.57: return "C"
    case score >= 0.53: return "C-"
    case score >= 0.47: return "D+"
    case score >= 0.43: return "D"
    case score >= 0.37: return "D-"
    default:            return "F"
    }
}
```

**Step 7: Implement plan definitions**

```go
// pkg/plan/plan.go
package plan

type Plan struct {
    Name             string
    MaxContributors  int  // per billing period, 0 = unlimited
    RateLimitPerHour int
    DeepScoring      bool
    LicenseAnalysis  bool
    AISensing        bool // Tier 1
    BatchAPI         bool
    Webhooks         bool
    TrendMonths      int
    MaxAPIKeys       int
}

var plans = map[string]Plan{
    "free": {
        Name: "free", MaxContributors: 50, RateLimitPerHour: 60,
        DeepScoring: false, LicenseAnalysis: false, AISensing: false,
        TrendMonths: 0, MaxAPIKeys: 1,
    },
    "starter": {
        Name: "starter", MaxContributors: 200, RateLimitPerHour: 120,
        DeepScoring: false, LicenseAnalysis: true, AISensing: true,
        TrendMonths: 3, MaxAPIKeys: 1,
    },
    "pro": {
        Name: "pro", MaxContributors: 2000, RateLimitPerHour: 1000,
        DeepScoring: true, LicenseAnalysis: true, AISensing: true,
        BatchAPI: true, Webhooks: true, TrendMonths: 12, MaxAPIKeys: 10,
    },
}

func Get(name string) (Plan, bool) {
    p, ok := plans[name]
    return p, ok
}

func Free() Plan { return plans["free"] }
```

```go
// pkg/plan/plan_test.go
package plan

import "testing"

func TestGetPlan(t *testing.T) {
    p, ok := Get("starter")
    if !ok {
        t.Fatal("starter plan not found")
    }
    if p.MaxContributors != 200 {
        t.Errorf("got %d, want 200", p.MaxContributors)
    }
    if !p.LicenseAnalysis {
        t.Error("starter should include license analysis")
    }
}

func TestGetPlanUnknown(t *testing.T) {
    _, ok := Get("nonexistent")
    if ok {
        t.Error("expected not found")
    }
}
```

**Step 8: Run all tests**

Run: `go test ./... -v -race`
Expected: PASS

**Step 9: Commit**

```bash
git add pkg/model/ pkg/score/ pkg/plan/
git commit -S -m "Add domain types, scoring model (reputer v3.2.0), and plan definitions"
```

---

## Task 5: GitHub Client Interface

Abstracted GitHub client — PAT-backed for dev, App installation for prod.

**Files:**
- Create: `pkg/github/client.go`
- Create: `pkg/github/client_test.go`
- Create: `pkg/github/fetch.go`
- Create: `pkg/github/fetch_test.go`

**Step 1: Define the interface**

```go
// pkg/github/client.go
package github

import (
    "context"

    "github.com/thingzio/devtrace/pkg/score"
)

// Client abstracts GitHub API access. PAT-backed in dev, App installation in prod.
type Client interface {
    // FetchUser returns basic profile for a GitHub user.
    FetchUser(ctx context.Context, username string) (*UserProfile, error)

    // FetchSignals returns full scoring signals for a contributor.
    // If repo is non-empty, includes repo-contextual signals.
    FetchSignals(ctx context.Context, username, repo string) (*score.InputSignals, error)
}

type UserProfile struct {
    Username         string
    Name             string
    Email            string
    AvatarURL        string
    Company          string
    Location         string
    Bio              string
    CreatedAt        string // RFC3339
    Suspended        bool
    Followers        int64
    Following        int64
    PublicRepos      int64
}
```

**Step 2: Implement PAT-backed client**

```go
// pkg/github/fetch.go
package github

import (
    "context"
    "fmt"
    "log/slog"
    "strings"
    "time"

    gh "github.com/google/go-github/v83/github"
    "golang.org/x/oauth2"

    "github.com/thingzio/devtrace/pkg/score"
)

type PATClient struct {
    client *gh.Client
}

func NewPATClient(token string) *PATClient {
    ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
    tc := oauth2.NewClient(context.Background(), ts)
    return &PATClient{client: gh.NewClient(tc)}
}

func (c *PATClient) FetchUser(ctx context.Context, username string) (*UserProfile, error) {
    u, _, err := c.client.Users.Get(ctx, username)
    if err != nil {
        return nil, fmt.Errorf("fetch user %s: %w", username, err)
    }
    return &UserProfile{
        Username:    u.GetLogin(),
        Name:        u.GetName(),
        Email:       u.GetEmail(),
        AvatarURL:   u.GetAvatarURL(),
        Company:     u.GetCompany(),
        Location:    u.GetLocation(),
        Bio:         u.GetBio(),
        CreatedAt:   u.GetCreatedAt().Format(time.RFC3339),
        Suspended:   u.GetSuspended(),
        Followers:   int64(u.GetFollowers()),
        Following:   int64(u.GetFollowing()),
        PublicRepos: int64(u.GetPublicRepos()),
    }, nil
}

func (c *PATClient) FetchSignals(ctx context.Context, username, repo string) (*score.InputSignals, error) {
    u, _, err := c.client.Users.Get(ctx, username)
    if err != nil {
        return nil, fmt.Errorf("fetch user %s: %w", username, err)
    }

    sig := &score.InputSignals{
        Suspended:   u.GetSuspended(),
        Followers:   int64(u.GetFollowers()),
        Following:   int64(u.GetFollowing()),
        PublicRepos: int64(u.GetPublicRepos()),
        HasBio:      u.GetBio() != "",
        HasCompany:  u.GetCompany() != "",
        HasLocation: u.GetLocation() != "",
        HasWebsite:  u.GetBlog() != "",
    }

    if u.GetSuspended() {
        return sig, nil // No point fetching more for suspended accounts
    }

    created := u.GetCreatedAt().Time
    sig.AgeDays = int64(time.Since(created).Hours() / 24)

    // Fetch merged PRs, closed PRs, recent PR repos, forked repos concurrently
    // Pattern matches DevPulse's gatherFullSignals
    if err := c.fetchExtendedSignals(ctx, username, sig); err != nil {
        slog.Warn("partial signal fetch", "username", username, "error", err)
        // Continue with partial data — better than failing entirely
    }

    // Repo-contextual signals if repo specified
    if repo != "" {
        if err := c.fetchRepoSignals(ctx, username, repo, sig); err != nil {
            slog.Warn("repo signal fetch", "username", username, "repo", repo, "error", err)
        }
    }

    return sig, nil
}

func (c *PATClient) fetchExtendedSignals(ctx context.Context, username string, sig *score.InputSignals) error {
    // Merged PRs
    mergedResult, _, err := c.client.Search.Issues(ctx, fmt.Sprintf("type:pr author:%s is:merged", username), nil)
    if err == nil {
        sig.PRsMerged = int64(mergedResult.GetTotal())
    }

    // Closed (unmerged) PRs
    closedResult, _, err := c.client.Search.Issues(ctx, fmt.Sprintf("type:pr author:%s is:unmerged is:closed", username), nil)
    if err == nil {
        sig.PRsClosed = int64(closedResult.GetTotal())
    }

    // Recent PR repo diversity (last 90 days)
    cutoff := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
    recentResult, _, err := c.client.Search.Issues(ctx, fmt.Sprintf("type:pr author:%s created:>=%s", username, cutoff), &gh.SearchOptions{
        ListOptions: gh.ListOptions{PerPage: 100},
    })
    if err == nil {
        repos := make(map[string]struct{})
        for _, issue := range recentResult.Issues {
            if url := issue.GetRepositoryURL(); url != "" {
                repos[url] = struct{}{}
            }
        }
        sig.RecentPRRepoCount = int64(len(repos))
    }

    // Forked repos
    opts := &gh.RepositoryListByUserOptions{
        ListOptions: gh.ListOptions{PerPage: 100},
        Type:        "owner",
    }
    userRepos, _, err := c.client.Repositories.ListByUser(ctx, username, opts)
    if err == nil {
        for _, r := range userRepos {
            if r.GetFork() {
                sig.ForkedRepos++
            }
        }
    }

    return nil
}

func (c *PATClient) fetchRepoSignals(ctx context.Context, username, repo string, sig *score.InputSignals) error {
    parts := strings.SplitN(repo, "/", 2)
    if len(parts) != 2 {
        return fmt.Errorf("invalid repo format %q, expected owner/repo", repo)
    }
    owner, name := parts[0], parts[1]

    // Check org membership
    isMember, _, err := c.client.Organizations.IsMember(ctx, owner, username)
    if err == nil {
        sig.OrgMember = isMember
        sig.TrustedOrgMember = isMember
    }

    // Get contributor stats for this repo
    // AuthorAssociation comes from a PR or issue by this user in the repo
    issueResult, _, err := c.client.Search.Issues(ctx,
        fmt.Sprintf("repo:%s/%s author:%s", owner, name, username),
        &gh.SearchOptions{ListOptions: gh.ListOptions{PerPage: 1}})
    if err == nil && len(issueResult.Issues) > 0 {
        sig.AuthorAssociation = issueResult.Issues[0].GetAuthorAssociation()
    }

    return nil
}
```

**Step 3: Write unit test with interface mock**

```go
// pkg/github/client_test.go
package github

import (
    "context"
    "testing"

    "github.com/thingzio/devtrace/pkg/score"
)

// MockClient for testing consumers of the Client interface
type MockClient struct {
    UserProfile *UserProfile
    Signals     *score.InputSignals
    Err         error
}

func (m *MockClient) FetchUser(_ context.Context, _ string) (*UserProfile, error) {
    return m.UserProfile, m.Err
}

func (m *MockClient) FetchSignals(_ context.Context, _, _ string) (*score.InputSignals, error) {
    return m.Signals, m.Err
}

func TestMockClientImplementsInterface(t *testing.T) {
    var _ Client = &MockClient{}
    var _ Client = &PATClient{}
}
```

**Step 4: Run tests**

Run: `go test ./pkg/github/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/github/
git commit -S -m "Add GitHub client interface with PAT-backed implementation"
```

---

## Task 6: Score Service (orchestration layer)

Ties together GitHub client, scoring model, and database.

**Files:**
- Create: `pkg/service/score.go`
- Create: `pkg/service/score_test.go`

**Step 1: Write test for scoring service**

```go
// pkg/service/score_test.go
package service

import (
    "context"
    "testing"

    ghclient "github.com/thingzio/devtrace/pkg/github"
    "github.com/thingzio/devtrace/pkg/score"
)

func TestScoreContributor(t *testing.T) {
    mock := &ghclient.MockClient{
        UserProfile: &ghclient.UserProfile{
            Username: "testuser", Name: "Test User",
            Followers: 42, Following: 18, PublicRepos: 23,
        },
        Signals: &score.InputSignals{
            AgeDays: 1095, Followers: 42, Following: 18, PublicRepos: 23,
            HasBio: true, HasCompany: true, PRsMerged: 87,
        },
    }

    svc := NewScoreService(mock, nil) // nil store for unit test
    resp, err := svc.Score(context.Background(), "testuser", "", "free")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if resp.Username != "testuser" {
        t.Errorf("got %q, want testuser", resp.Username)
    }
    if resp.Score == nil {
        t.Fatal("score is nil")
    }
    if resp.Score.Value < 0 || resp.Score.Value > 1 {
        t.Errorf("score out of range: %f", resp.Score.Value)
    }
    if resp.Score.Grade == "" {
        t.Error("grade is empty")
    }
    if resp.Score.ModelVersion != score.ModelVersion {
        t.Errorf("model version: got %q, want %q", resp.Score.ModelVersion, score.ModelVersion)
    }
}

func TestScoreContributorPlanAware(t *testing.T) {
    mock := &ghclient.MockClient{
        UserProfile: &ghclient.UserProfile{Username: "testuser"},
        Signals:     &score.InputSignals{AgeDays: 365, PublicRepos: 10},
    }

    svc := NewScoreService(mock, nil)

    // Free plan: should have signals and categories
    resp, _ := svc.Score(context.Background(), "testuser", "", "free")
    if resp.Signals == nil {
        t.Error("free plan should include signals")
    }
    if resp.License != nil {
        t.Error("free plan should not include license")
    }

    // Unauth: should only have score
    resp, _ = svc.Score(context.Background(), "testuser", "", "")
    if resp.Signals != nil {
        t.Error("unauth should not include signals")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/service/ -v`
Expected: FAIL

**Step 3: Implement score service**

```go
// pkg/service/score.go
package service

import (
    "context"
    "fmt"
    "time"

    ghclient "github.com/thingzio/devtrace/pkg/github"
    "github.com/thingzio/devtrace/pkg/model"
    "github.com/thingzio/devtrace/pkg/score"
)

type ScoreService struct {
    gh    ghclient.Client
    store ScoreStore // nil-safe for unit tests without DB
}

// ScoreStore is the subset of the data store needed for scoring.
type ScoreStore interface {
    GetCachedScore(ctx context.Context, username, provider string) (*model.ScoreResponse, error)
    SaveScore(ctx context.Context, resp *model.ScoreResponse) error
}

func NewScoreService(gh ghclient.Client, store ScoreStore) *ScoreService {
    return &ScoreService{gh: gh, store: store}
}

func (s *ScoreService) Score(ctx context.Context, username, repo, plan string) (*model.ScoreResponse, error) {
    signals, err := s.gh.FetchSignals(ctx, username, repo)
    if err != nil {
        return nil, fmt.Errorf("fetch signals for %s: %w", username, err)
    }

    profile, err := s.gh.FetchUser(ctx, username)
    if err != nil {
        return nil, fmt.Errorf("fetch user %s: %w", username, err)
    }

    value := score.Compute(*signals)
    grade := score.Grade(value)
    now := time.Now().UTC()

    resp := &model.ScoreResponse{
        Username: username,
        Provider: model.ProviderGitHub,
        Score: &model.Score{
            Grade:        grade,
            Value:        value,
            ModelVersion: score.ModelVersion,
        },
        ScoredAt: now,
    }

    // Plan-aware enrichment
    switch plan {
    case "": // unauthenticated — score only
        resp.Detail = "Sign up for full signal breakdown -> devtrace.thingz.io"

    case "free", "starter", "pro":
        resp.Score.Categories = score.Categories(*signals)
        resp.Signals = signalsFromInput(signals, profile)
        resp.RiskSummary = generateRiskSummary(signals, value, repo)

        if repo != "" {
            resp.RepoContext = repoContextFromSignals(signals, repo)
        }

        // Starter+ features
        if plan == "starter" || plan == "pro" {
            // License and AI sensing populated by separate calls (Phase 1: nil placeholders)
            // These will be populated when license/AI analyzers are built
        }
    }

    return resp, nil
}

func signalsFromInput(s *score.InputSignals, p *ghclient.UserProfile) *model.Signals {
    return &model.Signals{
        AccountAgeDays:    s.AgeDays,
        Followers:         s.Followers,
        Following:         s.Following,
        PublicRepos:       s.PublicRepos,
        ForkedRepos:       s.ForkedRepos,
        PRsMerged:         s.PRsMerged,
        PRsClosed:         s.PRsClosed,
        RecentPRRepoCount: s.RecentPRRepoCount,
        HasBio:            s.HasBio,
        HasCompany:        s.HasCompany,
        HasLocation:       s.HasLocation,
        HasWebsite:        s.HasWebsite,
        OrgMember:         s.OrgMember,
        Suspended:         s.Suspended,
        AuthorAssociation: s.AuthorAssociation,
        CommitsVerified:   s.UnverifiedCommits == 0 && s.Commits > 0,
    }
}

func repoContextFromSignals(s *score.InputSignals, repo string) *model.RepoContext {
    rc := &model.RepoContext{
        Repo:              repo,
        Commits:           s.Commits,
        TotalCommits:      s.TotalCommits,
        TotalContributors: s.TotalContributors,
        OrgMember:         s.OrgMember,
        AuthorAssociation: s.AuthorAssociation,
        TrustedOrgMember:  s.TrustedOrgMember,
    }
    if s.LastCommitDays > 0 {
        d := s.LastCommitDays
        rc.LastCommitDays = &d
    }
    return rc
}

func generateRiskSummary(s *score.InputSignals, value float64, repo string) string {
    if s.Suspended {
        return "Account is suspended. Do not merge without manual review."
    }

    var risk string
    switch {
    case value >= 0.8:
        risk = "Established account with strong contribution history."
    case value >= 0.5:
        risk = "Established account with consistent contribution history."
    case value >= 0.3:
        risk = "Limited contribution history. Careful review recommended."
    default:
        risk = "Minimal public activity. Manual review strongly recommended."
    }

    if s.AgeDays < 90 {
        risk += " Account created recently."
    }
    if s.PRsMerged == 0 {
        risk += " No prior merged PRs."
    }
    if repo != "" && s.AuthorAssociation == "FIRST_TIME_CONTRIBUTOR" {
        risk += " First contribution to this project."
    }

    switch {
    case value >= 0.7:
        risk += " Standard review recommended."
    case value >= 0.4:
        risk += " Enhanced review recommended."
    default:
        risk += " Maintainer review required."
    }

    return risk
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/service/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/service/ pkg/model/
git commit -S -m "Add score service with plan-aware response enrichment"
```

---

## Task 7: HTTP Server + API Router

Wire everything into a running HTTP server with the score endpoint.

**Files:**
- Create: `cmd/devtrace-site/main.go`
- Create: `pkg/server/server.go`
- Create: `pkg/server/handler_score.go`
- Create: `pkg/server/handler_score_test.go`
- Create: `pkg/server/ratelimit.go`

**Step 1: Create main.go entry point**

```go
// cmd/devtrace-site/main.go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/thingzio/devtrace/pkg/logging"
    "github.com/thingzio/devtrace/pkg/server"
)

var (
    version = "v0.0.1-default"
    commit  = ""
    date    = ""
)

func main() {
    logging.SetupLogger()
    slog.Info("starting devtrace-site", "version", version, "commit", commit, "date", date)

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    err := server.Run(ctx, server.Options{Version: version, Commit: commit, Date: date})
    stop()

    if err != nil {
        slog.Error("fatal error", "error", err)
        os.Exit(1)
    }
}
```

**Step 2: Create server with router**

```go
// pkg/server/server.go
package server

import (
    "context"
    "fmt"
    "log/slog"
    "net/http"
    "time"

    "github.com/thingzio/devtrace/pkg/config"
    "github.com/thingzio/devtrace/pkg/data/postgres"
    ghclient "github.com/thingzio/devtrace/pkg/github"
    "github.com/thingzio/devtrace/pkg/health"
    "github.com/thingzio/devtrace/pkg/service"
)

type Options struct {
    Version string
    Commit  string
    Date    string
}

func Run(ctx context.Context, opts Options) error {
    store, err := postgres.NewFromEnv()
    if err != nil {
        return fmt.Errorf("init store: %w", err)
    }
    defer store.Close()

    if err := store.Migrate(ctx); err != nil {
        return fmt.Errorf("migrate: %w", err)
    }

    token := config.GetEnv("GITHUB_TOKEN", "")
    if token == "" {
        return fmt.Errorf("GITHUB_TOKEN is required")
    }

    gh := ghclient.NewPATClient(token)
    scoreSvc := service.NewScoreService(gh, nil) // TODO: wire store

    mux := makeRouter(scoreSvc, opts)

    port := config.GetEnv("PORT", "8080")
    srv := &http.Server{
        Addr:              "0.0.0.0:" + port,
        Handler:           securityHeaders(mux),
        ReadTimeout:       30 * time.Second,
        ReadHeaderTimeout: 5 * time.Second,
        WriteTimeout:      60 * time.Second,
        IdleTimeout:       120 * time.Second,
        MaxHeaderBytes:    1 << 16, // 64KB
    }

    errCh := make(chan error, 1)
    go func() {
        slog.Info("listening", "port", port)
        errCh <- srv.ListenAndServe()
    }()

    select {
    case err := <-errCh:
        return fmt.Errorf("server: %w", err)
    case <-ctx.Done():
        slog.Info("shutting down")
    }

    shutdownTimeout := config.GetEnvAsInt("SERVER_SHUTDOWN_TIMEOUT_SEC", 5)
    shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(shutdownTimeout)*time.Second)
    defer cancel()

    return srv.Shutdown(shutdownCtx)
}

func makeRouter(scoreSvc *service.ScoreService, opts Options) *http.ServeMux {
    mux := http.NewServeMux()

    // Public
    mux.HandleFunc("GET /health", health.Handler())
    mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(scoreSvc))

    return mux
}

func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        next.ServeHTTP(w, r)
    })
}
```

**Step 3: Create score handler**

```go
// pkg/server/handler_score.go
package server

import (
    "encoding/json"
    "log/slog"
    "net/http"

    "github.com/thingzio/devtrace/pkg/service"
)

func scoreHandler(svc *service.ScoreService) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        username := r.PathValue("username")
        if username == "" {
            http.Error(w, `{"error":"username required"}`, http.StatusBadRequest)
            return
        }

        repo := r.URL.Query().Get("repo")

        // TODO: extract plan from API token auth (Phase 2)
        // For now, treat all requests as "free" plan
        plan := "free"

        resp, err := svc.Score(r.Context(), username, repo, plan)
        if err != nil {
            slog.Error("score failed", "username", username, "error", err)
            http.Error(w, `{"error":"scoring failed"}`, http.StatusInternalServerError)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(resp); err != nil {
            slog.Error("encode response", "error", err)
        }
    }
}
```

**Step 4: Write handler test**

```go
// pkg/server/handler_score_test.go
package server

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    ghclient "github.com/thingzio/devtrace/pkg/github"
    "github.com/thingzio/devtrace/pkg/model"
    "github.com/thingzio/devtrace/pkg/score"
    "github.com/thingzio/devtrace/pkg/service"
)

func TestScoreHandler(t *testing.T) {
    mock := &ghclient.MockClient{
        UserProfile: &ghclient.UserProfile{
            Username: "testuser", Followers: 10, PublicRepos: 5,
        },
        Signals: &score.InputSignals{
            AgeDays: 365, Followers: 10, PublicRepos: 5, PRsMerged: 20,
        },
    }
    svc := service.NewScoreService(mock, nil)
    handler := scoreHandler(svc)

    mux := http.NewServeMux()
    mux.HandleFunc("GET /api/v1/score/{username}", handler)

    req := httptest.NewRequest("GET", "/api/v1/score/testuser", nil)
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, req)

    if w.Code != http.StatusOK {
        t.Fatalf("status: got %d, want 200", w.Code)
    }

    var resp model.ScoreResponse
    if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
        t.Fatalf("decode: %v", err)
    }
    if resp.Username != "testuser" {
        t.Errorf("username: got %q, want testuser", resp.Username)
    }
    if resp.Score == nil {
        t.Fatal("score is nil")
    }
    if resp.Score.Grade == "" {
        t.Error("grade is empty")
    }
    if resp.Signals == nil {
        t.Error("signals should be present for free plan")
    }
}

func TestScoreHandlerMissingUsername(t *testing.T) {
    svc := service.NewScoreService(&ghclient.MockClient{}, nil)
    handler := scoreHandler(svc)

    mux := http.NewServeMux()
    mux.HandleFunc("GET /api/v1/score/{username}", handler)

    req := httptest.NewRequest("GET", "/api/v1/score/", nil)
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, req)

    // ServeMux won't match /api/v1/score/ without a username segment
    if w.Code == http.StatusOK {
        t.Error("expected non-200 for missing username")
    }
}
```

**Step 5: Run tests**

Run: `go test ./pkg/server/ -v -race`
Expected: PASS

Run: `go test ./... -v -race`
Expected: PASS (all tests)

**Step 6: Verify local server starts**

Run: `make db-up && GITHUB_TOKEN=$GITHUB_TOKEN go run ./cmd/devtrace-site/`

Test: `curl -s http://localhost:8080/health` → 200 OK
Test: `curl -s http://localhost:8080/api/v1/score/octocat | jq .` → JSON score response

**Step 7: Commit**

```bash
git add cmd/ pkg/server/
git commit -S -m "Add HTTP server with /api/v1/score/{username} endpoint"
```

---

## Task 8: Rate Limiting

IP-based rate limiting for the score endpoint.

**Files:**
- Create: `pkg/server/ratelimit.go`
- Create: `pkg/server/ratelimit_test.go`

**Step 1: Write rate limiter tests**

```go
// pkg/server/ratelimit_test.go
package server

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestRateLimiter(t *testing.T) {
    rl := newIPRateLimiter(2, 1) // 2 requests per 1 second
    handler := rl.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    // First 2 requests should succeed
    for i := 0; i < 2; i++ {
        req := httptest.NewRequest("GET", "/", nil)
        req.RemoteAddr = "192.0.2.1:1234"
        w := httptest.NewRecorder()
        handler.ServeHTTP(w, req)
        if w.Code != http.StatusOK {
            t.Errorf("request %d: got %d, want 200", i, w.Code)
        }
    }

    // 3rd request should be rate limited
    req := httptest.NewRequest("GET", "/", nil)
    req.RemoteAddr = "192.0.2.1:1234"
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusTooManyRequests {
        t.Errorf("request 3: got %d, want 429", w.Code)
    }

    // Different IP should succeed
    req2 := httptest.NewRequest("GET", "/", nil)
    req2.RemoteAddr = "192.0.2.2:1234"
    w2 := httptest.NewRecorder()
    handler.ServeHTTP(w2, req2)
    if w2.Code != http.StatusOK {
        t.Errorf("different IP: got %d, want 200", w2.Code)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/server/ -v -run TestRateLimiter`
Expected: FAIL

**Step 3: Implement in-memory IP rate limiter**

```go
// pkg/server/ratelimit.go
package server

import (
    "net"
    "net/http"
    "sync"
    "time"
)

type ipRateLimiter struct {
    mu       sync.Mutex
    visitors map[string]*visitor
    limit    int
    window   time.Duration
}

type visitor struct {
    count    int
    resetAt  time.Time
}

func newIPRateLimiter(limit int, windowSec int) *ipRateLimiter {
    rl := &ipRateLimiter{
        visitors: make(map[string]*visitor),
        limit:    limit,
        window:   time.Duration(windowSec) * time.Second,
    }
    go rl.cleanup()
    return rl
}

func (rl *ipRateLimiter) wrap(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ip := extractIP(r)
        if !rl.allow(ip) {
            w.Header().Set("Retry-After", "60")
            http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}

func (rl *ipRateLimiter) allow(ip string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    now := time.Now()
    v, ok := rl.visitors[ip]
    if !ok || now.After(v.resetAt) {
        rl.visitors[ip] = &visitor{count: 1, resetAt: now.Add(rl.window)}
        return true
    }
    v.count++
    return v.count <= rl.limit
}

func (rl *ipRateLimiter) cleanup() {
    for {
        time.Sleep(rl.window)
        rl.mu.Lock()
        now := time.Now()
        for ip, v := range rl.visitors {
            if now.After(v.resetAt) {
                delete(rl.visitors, ip)
            }
        }
        rl.mu.Unlock()
    }
}

func extractIP(r *http.Request) string {
    // Prefer X-Forwarded-For (behind LB/proxy)
    if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
        return xff
    }
    ip, _, _ := net.SplitHostPort(r.RemoteAddr)
    return ip
}
```

**Step 4: Wire rate limiter into router**

Update `makeRouter` in `server.go`:

```go
func makeRouter(scoreSvc *service.ScoreService, opts Options) *http.ServeMux {
    mux := http.NewServeMux()

    scoreRL := newIPRateLimiter(
        config.GetEnvAsInt("SCORE_RATE_LIMIT", 60), // requests per window
        3600, // 1 hour window
    )

    mux.HandleFunc("GET /health", health.Handler())
    mux.Handle("GET /api/v1/score/{username}", scoreRL.wrap(http.HandlerFunc(scoreHandler(scoreSvc))))

    return mux
}
```

**Step 5: Run tests**

Run: `go test ./pkg/server/ -v -race`
Expected: PASS

Run: `go test ./... -v -race`
Expected: PASS

**Step 6: Commit**

```bash
git add pkg/server/ratelimit.go pkg/server/ratelimit_test.go pkg/server/server.go
git commit -S -m "Add IP-based rate limiting for score endpoint"
```

---

## Task 9: End-to-End Verification

Verify the full local flow works.

**Step 1: Start local environment**

```bash
make db-up
```

**Step 2: Run all tests**

```bash
make test
```
Expected: PASS

**Step 3: Run linter**

```bash
make lint
```
Expected: PASS (fix any issues)

**Step 4: Start server and test manually**

```bash
GITHUB_TOKEN=$GITHUB_TOKEN go run ./cmd/devtrace-site/
```

In another terminal:
```bash
# Health check
curl -s http://localhost:8080/health
# → 200 OK

# Score a contributor
curl -s http://localhost:8080/api/v1/score/octocat | jq .
# → JSON with score, grade, signals, risk_summary

# Score with repo context
curl -s "http://localhost:8080/api/v1/score/octocat?repo=octocat/Hello-World" | jq .
# → JSON with repo_context populated

# Rate limit test
for i in $(seq 1 65); do curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/v1/score/octocat; done
# → First 60 return 200, then 429
```

**Step 5: Run full qualify pipeline**

```bash
make qualify
```
Expected: PASS

**Step 6: Commit any fixes**

```bash
git add -A
git commit -S -m "Fix lint and test issues from end-to-end verification"
```

---

## Task 10: Terraform — Shared Infrastructure References

Reference existing DevPulse GCP resources. Create DevTrace-specific resources.

**Files:**
- Create: `infra/saas/main.tf`
- Create: `infra/saas/variables.tf`
- Create: `infra/saas/outputs.tf`
- Create: `infra/saas/cloud-run.tf`
- Create: `infra/saas/iam.tf`
- Create: `infra/saas/dns.tf`
- Create: `infra/saas/artifact-registry.tf`

**Step 1: Set up Terraform with shared resource references**

```hcl
# infra/saas/main.tf
terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
  backend "gcs" {
    bucket = "devpulseio-terraform"
    prefix = "devtrace-saas"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Reference existing shared resources (DO NOT recreate)
data "google_compute_network" "vpc" {
  name = "devpulse-saas-vpc"
}

data "google_compute_subnetwork" "subnet" {
  name   = "devpulse-saas-subnet"
  region = var.region
}

data "google_sql_database_instance" "db" {
  name = var.cloud_sql_instance_name
}

# DevTrace-owned database within shared instance
resource "google_sql_database" "devtrace" {
  name     = "devtrace"
  instance = data.google_sql_database_instance.db.name
}

# Own DB user — distinct from DevPulse's user
resource "google_sql_user" "devtrace" {
  name     = "devtrace"
  instance = data.google_sql_database_instance.db.name
  password = var.db_password
}

# Cross-database read grants (applied via provisioner or manually):
# GRANT SELECT ON ALL TABLES IN SCHEMA public TO devtrace;
# Applied on the devpulse database to let the devtrace user
# read developer, event, and repo_meta tables.
# DevTrace NEVER writes to DevPulse tables.
```

```hcl
# infra/saas/variables.tf
variable "project_id" {
  type    = string
  default = "devpulseio"
}

variable "region" {
  type    = string
  default = "us-west1"
}

variable "cloud_sql_instance_name" {
  type        = string
  description = "Shared Cloud SQL instance name"
}

variable "db_password" {
  type      = string
  sensitive = true
}

variable "domain" {
  type    = string
  default = "devtrace.thingz.io"
}
```

**Step 2: Create Cloud Run service**

```hcl
# infra/saas/cloud-run.tf
resource "google_cloud_run_v2_service" "site" {
  name     = "devtrace-saas-serve"
  location = var.region

  template {
    service_account = google_service_account.run.email

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/devtrace-saas-images/devtrace-site:latest"

      env {
        name  = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.db_url.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "GITHUB_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.github_token.secret_id
            version = "latest"
          }
        }
      }
    }

    vpc_access {
      network_interfaces {
        network    = data.google_compute_network.vpc.id
        subnetwork = data.google_compute_subnetwork.subnet.id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }
  }
}
```

**Step 3: Create IAM, Artifact Registry, secrets**

```hcl
# infra/saas/iam.tf
resource "google_service_account" "run" {
  account_id   = "devtrace-saas-run"
  display_name = "DevTrace Cloud Run"
}

# WIF for GitHub Actions
resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = "github-actions" # existing pool
  workload_identity_pool_provider_id = "devtrace-saas"
  display_name                       = "GitHub Actions - DevTrace"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
  }

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}
```

```hcl
# infra/saas/artifact-registry.tf
resource "google_artifact_registry_repository" "images" {
  repository_id = "devtrace-saas-images"
  location      = var.region
  format        = "DOCKER"
}
```

**Step 4: Validate terraform**

```bash
make tf-init && make tf-plan
```

Note: Plan will show new resources (Cloud Run, service accounts, Artifact Registry, secrets) but NO changes to existing VPC, Cloud SQL, or networking.

**Step 5: Commit**

```bash
git add infra/
git commit -S -m "Add Terraform config referencing shared GCP infra"
```

---

## Task 11: CI/CD Workflows

Copy from DevPulse, adapt for DevTrace.

**Files:**
- Create: `.github/workflows/test-on-push.yaml`
- Create: `.github/workflows/release-on-tag.yaml`

**Step 1: Create test workflow**

Copy from DevPulse `test-on-push.yaml`. Update:
- Repo references → `thingzio/devtrace`
- Service names → `devtrace-*`
- WIF service account → `devtrace-saas-run`

**Step 2: Create release workflow**

Copy from DevPulse `release-on-tag.yaml`. Update:
- Image names → `devtrace-saas-images/devtrace-site`
- Cloud Run service → `devtrace-saas-serve`

**Step 3: Commit**

```bash
git add .github/
git commit -S -m "Add CI/CD workflows adapted from DevPulse"
```

---

## Phase 1 Complete Checklist

After all tasks, verify:

- [ ] `make db-up` starts local Postgres
- [ ] `make test` passes all unit tests
- [ ] `make lint` passes
- [ ] `make vulncheck` passes
- [ ] `make qualify` passes full pipeline
- [ ] `go run ./cmd/devtrace-site/` starts server with PAT
- [ ] `GET /health` returns 200
- [ ] `GET /api/v1/score/{username}` returns scored JSON
- [ ] `GET /api/v1/score/{username}?repo=owner/repo` returns repo context
- [ ] Rate limiting returns 429 after threshold
- [ ] `make tf-plan` shows only new resources, no changes to shared infra
- [ ] All commits signed (`-S`)

---

## What's Next (Phase 2)

Phase 2 adds auth: GitHub OAuth, GitHub App installation, token minting, session management, and swapping the PAT client for App installation tokens. Plan and build separately.
