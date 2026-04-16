# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`devtrace` is a multi-tenant SaaS for contributor trust scoring and risk analysis. Single binary: `devtrace-site` (HTTP server with background workers for ingestion and scoring). PostgreSQL for persistence, GitHub API for contributor data, Claude API for AI-powered risk sensing. Deployed to Cloud Run.

## Build & Test

```shell
make test          # unit tests with race detector
make lint          # go vet + golangci-lint + yamllint + tfsec
make qualify       # test-coverage + lint + govulncheck + e2e
make build         # goreleaser single-target build
make server        # run dev server with DEVTRACE_DEBUG=true
make db-up         # start local Postgres (docker compose)
make seed          # seed test tenant and print API token
```

Tool versions and quality thresholds are centralized in `.settings.yaml` (single source of truth). Go version is pinned in `go.mod` (currently 1.26). The `.golangci.yaml` at the repo root configures linting.

## Non-Negotiable Rules

1. **Read before writing** — Never modify code you haven't read
2. **Tests must pass** — `make test` with race detector; never skip or disable tests
3. **Run `make qualify` often** — Run at every stopping point (after completing a phase, before commits). Fix ALL lint/test failures before proceeding
4. **Use project patterns** — Learn existing code before inventing new approaches
5. **3-strike rule** — After 3 failed fix attempts, stop and reassess

## Git Configuration

- Commit to `main` branch (not `master`)
- Do use `-S` to cryptographically sign the commit
- Do NOT add `Co-Authored-By` lines (organization policy)
- Do not sign-off commits (no `-s` flag) unless the commit can't be cryptographically signed

## Code Conventions

**Context propagation (CRITICAL):**
- Every Store interface method takes `ctx context.Context` as its first parameter
- Every DB call must use `*Context` variants: `ExecContext`, `QueryContext`, `QueryRowContext`, `PrepareContext`, `BeginTx`
- Never use `context.Background()` in production code — always thread the caller's ctx
- HTTP handlers pass `r.Context()` to all Store/tenant calls
- Background workers pass their `ctx` through all layers

**Tenant schema changes:**
- When adding a column to the `tenant` table, you MUST update ALL of these:
  - SQL constants in `pkg/tenant/tenant.go`
  - `scanTenant()` field list in `pkg/tenant/tenant.go`
  - `Tenant` struct in `pkg/tenant/tenant.go`
  - Session validation SQL in `pkg/tenant/session.go`
  - Migration in `pkg/data/postgres/sql/migrations/`

**Error handling:**
- Use `fmt.Errorf("context: %w", err)` for wrapping — never bare `return err`
- Exported sentinel errors: `ErrTokenInvalid`, `ErrSessionInvalid` (in `pkg/tenant/`)

**Logging:**
- JSON-only via `log/slog` (Info, Debug, Warn, Error) — never `fmt.Println`
- `DEVTRACE_DEBUG=true` for debug level

**Database:**
- All SQL constants defined at the top of the file they're used in
- COALESCE pattern for optional filters: `WHERE col = COALESCE(?, col)`
- Transactions with explicit rollback on error
- Upserts via `INSERT ... ON CONFLICT(...) DO UPDATE SET`
- Limit checks inside transactions to prevent TOCTOU races

**HTTP handlers:**
- Return `http.HandlerFunc` closures: `func handler(db *sql.DB) http.HandlerFunc`
- Use `writeJSON(w, status, v)` and `writeError(w, status, msg)` helpers
- All outbound HTTP calls use a `*http.Client` with timeout (10-30s), never `http.DefaultClient`

**Imports:**
- GitHub API via `github.com/google/go-github/v83/github`
- Testing via `github.com/stretchr/testify` (assert + require)
- PostgreSQL via `github.com/lib/pq`
- JWT via `github.com/golang-jwt/jwt/v5`

**Testing:**
- `setupTestDB(t)` helper creates temp Postgres container with all migrations
- All test functions must create `ctx := context.Background()` and pass to Store methods
- Table-driven tests where applicable
- Test both nil DB and empty DB cases for query functions

## Anti-Patterns (Do Not Do)

| Anti-Pattern | Correct Approach |
|---|---|
| Modify code without reading it first | Always `Read` files before `Edit` |
| Skip or disable tests to make CI pass | Fix the actual issue |
| Invent new patterns | Study existing code in same package first |
| Use `fmt.Println` for logging | Use `slog.Info/Debug/Warn/Error` |
| Use `context.Background()` in handlers | Thread `r.Context()` or caller's `ctx` |
| Use `http.DefaultClient` for outbound calls | Use a `*http.Client` with timeout |
| Use `s.db.Exec()` (non-context) | Use `s.db.ExecContext(ctx, ...)` |
| Bare `return err` without wrapping | Use `fmt.Errorf("context: %w", err)` |
| Use `fmt.Sprintf` for SQL with user input | Use parameterized queries (`$1, $2`) |
| Add tenant column without updating all SQL | Update ALL SQL constants + scanTenant + struct |
| Add features not requested | Implement exactly what was asked |
| Create new files when editing suffices | Prefer `Edit` over `Write` |
| Guess at missing parameters | Ask for clarification |
| Continue after 3 failed fix attempts | Stop, reassess approach, explain blockers |

## Design Principles

- Partial failure is the steady state — design for timeouts, bounded retries
- Boring first — default to proven, simple technologies
- Observability is mandatory — structured logging
- Correctness must be reproducible — same inputs, same outputs
- Trust requires verifiable provenance — container images built in CI, govulncheck, dependency pinning

## Decision Framework

When choosing between approaches, prioritize in this order:
1. **Testability** — Can it be unit tested without external dependencies?
2. **Readability** — Can another engineer understand it quickly?
3. **Consistency** — Does it match existing patterns in the codebase?
4. **Simplicity** — Is it the simplest solution that works?
5. **Reversibility** — Can it be easily changed later?

## Architecture

```
cmd/devtrace-site/     HTTP server entrypoint (dashboard, OAuth, webhooks, scoring API)
pkg/server/            HTTP server, handlers, rate limiting, templates
pkg/server/static/     Frontend: CSS, JS, images (embedded via go:embed)
pkg/server/templates/  HTML templates: layout, home, landing, scorecard, settings, help, changelog, admin
pkg/score/             Scoring engine: heuristics, grade calculation
pkg/ingest/            GitHub data ingestion: archive fetching, aggregation, runner
pkg/background/        Background workers: ingestion scheduler, continuous scoring
pkg/service/           Service layer: score caching, orchestration
pkg/bot/               Bot detection and analysis
pkg/claude/            Claude API client for AI-powered risk sensing
pkg/data/              Store interface, shared types
pkg/data/postgres/     PostgreSQL Store (migrations, history, activity, queue, sync)
pkg/github/            GitHub API: client pool, token management, installations, fetching
pkg/tenant/            Tenant CRUD, sessions, API tokens, GitHub App, installations, usage
pkg/middleware/        Auth middleware (session cookie, admin gate)
pkg/oauth/             GitHub OAuth web flow
pkg/model/             Shared domain types (ScoreResponse, Profile, Signals, etc.)
pkg/plan/              Tenant plan definitions and limits
pkg/config/            Environment variable helpers
pkg/logging/           Logger setup
pkg/net/               HTTP client utilities, email validation
pkg/health/            Health check endpoint
infra/saas/            Terraform for GCP infrastructure
tools/                 Dev scripts (version bump, e2e, seed, setup)
```

Single binary (`devtrace-site`) with embedded background workers. The server handles HTTP requests (dashboard, OAuth, webhooks, scoring API, admin dashboard) while background goroutines run ingestion and continuous scoring pipelines. Admin dashboard at `/admin` is gated by `DEVTRACE_ADMIN_USERS` env var — returns 404 for non-admins. Public pages include changelog (`/changelog`) and help (`/help`).

Data flow: GitHub App webhook → tenant repos → background ingest worker → GitHub Archive/API → PostgreSQL → scoring engine → dashboard/API

## Environment Variables

- `DATABASE_URL` — PostgreSQL connection URI (required)
- `GITHUB_OAUTH_CLIENT_ID` — GitHub OAuth App client ID
- `GITHUB_OAUTH_CLIENT_SECRET` — GitHub OAuth App client secret
- `GITHUB_WEBHOOK_SECRET` — GitHub App webhook HMAC secret
- `GITHUB_APP_ID` — GitHub App ID for installation tokens
- `GITHUB_APP_KEY_PATH` — path to GitHub App private key PEM
- `BASE_URL` — public base URL (e.g. https://devtrace.thingz.io)
- `DEVTRACE_API_URL` — API URL (defaults to production: devtrace.thingz.io)
- `DEVTRACE_DEBUG` — set to `true` for debug-level logging
- `DEVTRACE_ADMIN_USERS` — comma-separated GitHub usernames for admin access
- `SCORER_BATCH_SIZE` — scoring queue batch size (default 100)
- `SCORER_MIN_QUOTA_PCT` — minimum aggregate token quota % before pausing scorer (default 30)
- `GHARCHIVE_BACKFILL_DAYS` — historical GH Archive backfill depth in days (default 0 = disabled, set to 180 for full coverage)
- `ANTHROPIC_API_KEY` — optional, enables AI risk sensing

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test-on-push.yaml` | push to main, PRs | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | tidy, lint, test with race detector |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, container image push |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy to Cloud Run |
| `deploy-cloud-run.yaml` | reusable (workflow_call) | Cloud Run deployment logic |

## Release Process

Releases are triggered by version tags. Use `make bump-patch`, `make bump-minor`, or `make bump-major` to tag and push.

- **Build**: goreleaser v2 compiles binaries, ko builds container images
- **Deploy**: Cloud Run service updated via `deploy-saas.yaml` or `release-on-tag.yaml`
