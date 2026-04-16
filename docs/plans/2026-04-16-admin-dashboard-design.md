# Admin Dashboard Design

Replace API-key-protected admin endpoints and CLI tools with a server-rendered admin dashboard behind GitHub OAuth, gated by an env-var-based admin user list.

## Goals

1. Eliminate `DEVTRACE_ADMIN_API_KEY` attack surface
2. Reuse existing GitHub OAuth for admin authentication
3. Consolidate Phase 8 remaining items: tenant management, token pool health, scoring metrics, pipeline health
4. Remove `tools/tenant-{list,plan,status}` CLI scripts

## Auth Model

- New middleware: `RequireAdmin` wraps `RequireAuth`
- After session validation, checks `tenant.Username` against `DEVTRACE_ADMIN_USERS` env var (comma-separated)
- Empty env var = admin disabled (no one can access `/admin`)
- Non-admins get 404 (hides route existence)

## Routes

All behind `RequireAdmin` middleware:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/admin` | Dashboard (all 4 sections) |
| `POST` | `/admin/tenant/{username}/plan` | Update plan (form) |
| `POST` | `/admin/tenant/{username}/status` | Update status (form) |

No link in main nav. Admins access `/admin` by URL. Regular users who hit the URL get redirected.

## Dashboard Sections

### 1. Tenant Management

Table: Username, Plan, Status, Max Contributors, Created. Each row has:
- Plan dropdown (free/starter/pro) + Update button -> `POST /admin/tenant/{username}/plan`
- Status toggle (active/suspended) + Update button -> `POST /admin/tenant/{username}/status`

Success/error feedback via query param redirect (`/admin?msg=plan_updated&user=foo`).

### 2. Token Pool Health

Data from `TokenPool` methods (passed to handler via same pattern as `store`/`scoreSvc`):
- Total / Active / Exhausted token counts
- Per-token usage counts table (token index, calls, status)

### 3. Scoring Metrics

Time-bucketed counts from `devtrace_reputation_history`:
- Scores in last 1h / 24h / 72h / this week
- Queue depth from `devtrace_scoring_queue`
- Stale contributor count from `devtrace_reputation`

New store method: `ScoringMetrics(ctx) (*ScoringMetrics, error)` — runs count queries.

### 4. Pipeline Health

Inferred from DB timestamps (no in-process coupling):
- Last ingest: `MAX(created_at)` from `devtrace_contributor_activity`
- Last scorer: `MAX(scored_at)` from `devtrace_reputation_history`
- Total activity records: `COUNT(*)` from `devtrace_contributor_activity`

New store method: `PipelineStats(ctx) (*PipelineStats, error)`.

## New Files

- `pkg/middleware/admin.go` — `RequireAdmin` middleware
- `pkg/middleware/admin_test.go` — admin check tests
- `pkg/server/templates/admin.html` — admin dashboard template

## Modified Files

- `pkg/server/handler_admin.go` — full rewrite: session-based handlers replacing API-key handlers
- `pkg/server/handler_admin_test.go` — rewrite for form-based handlers
- `pkg/server/server.go` — replace admin API routes with `/admin` routes, pass `TokenPool` to handler
- `pkg/data/postgres/queue.go` — add `QueueDepth(ctx) (int, error)`
- `pkg/data/postgres/stale.go` — add `StaleCount(ctx, lowDays, highDays) (int, error)`
- `pkg/data/postgres/history.go` — add `ScoringMetrics(ctx) (*ScoringMetrics, error)`
- `pkg/data/postgres/activity.go` — add `PipelineStats(ctx) (*PipelineStats, error)`

## Deleted Files

- `tools/tenant-list`
- `tools/tenant-plan`
- `tools/tenant-status`

`tools/common` is kept (used by other scripts: e2e, bump, setup-tools, setup-gh-env).

## Env Var Changes

- **Add:** `DEVTRACE_ADMIN_USERS` (comma-separated GitHub usernames)
- **Remove:** `DEVTRACE_ADMIN_API_KEY`

## No New Dependencies

No DB migrations. All queries use existing tables. No JS framework. Template uses existing `layout.html` and `app.css`.
