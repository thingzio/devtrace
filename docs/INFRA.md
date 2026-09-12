# DevTrace — Development & Operations

Infrastructure lives in the private [`thingzio/infra`](https://github.com/thingzio/infra)
repository, under `run/devtrace`. It moved there when this repository went
public: the Terraform and the document describing it map production topology —
service and secret names, IAM structure, database wiring — which is worth not
publishing. Unlike a credential, it cannot be rotated out of a public
repository's history.

What remains here is what a contributor needs: the toolchain, the database
migrations, how CI is wired, and how to run the thing locally.

---

## Database

### Migrations

| File | Description |
|------|-------------|
| `001_schema.sql` | Squashed base schema: tenant, contributor, reputation, reputation_history, license_profile, ai_signal, api_token, session, app_installation, usage_record, rate_limit, sync_state, contributor_activity, scoring_queue |
| `002_usage_repo.sql` | Add repo column to usage_record |
| `003_usage_repo_fix.sql` | Re-apply repo column (idempotent fix) |
| `004_reset_backfill_cursor.sql` | Reset backfill cursor for 8MB scanner buffer |
| `005_token_quota_sample.sql` | Token quota sampling table for utilization tracking |

Applied automatically at startup by `store.Migrate(ctx)`. All tables use `devtrace_` prefix.

### Schema Boundary

DevTrace tables are DevTrace-owned. DevPulse tables are read-only. DevTrace reads from DevPulse's `developer` table for initial contributor discovery (via background sync). GH Archive provides independent discovery.

DevTrace uses its own `devtrace_tenant` table, not the shared DevPulse `tenant` table. The `devtrace_schema_version` table tracks migration state independently.

---

## Build & Quality Toolchain

| File | Purpose |
|------|---------|
| `.settings.yaml` | Go version, tool versions, quality thresholds |
| `.goreleaser.yaml` | Multi-binary build + ko container images |
| `.golangci.yaml` | Linter configuration |
| `.yamllint.yaml` | YAML validation |
| `docker-compose.yaml` | Local PostgreSQL 16 |
| `Makefile` | All build, test, lint, deploy targets |

### Key Makefile Targets

| Target | Description |
|--------|-------------|
| `make test` | Unit tests with race detector + coverage |
| `make lint` | Go vet + golangci-lint + yamllint |
| `make qualify` | test-coverage + lint + vulncheck + e2e |
| `make vulncheck` | Scan for known vulnerabilities |
| `make e2e` | End-to-end tests (requires Docker) |
| `make db-up` / `db-down` | Local Postgres lifecycle |
| `make db-connect` | psql shell to local Postgres |
| `make seed` | Create test tenant + API token |
| `make server` | Run devtrace-site locally |
| `make build` / `release` | goreleaser build/release |

| `make setup` | Validate and install local dev tools |
| `make bump-patch` / `bump-minor` / `bump-major` | Version tagging |

---

## CI/CD Workflows

| Workflow | Trigger | What it does |
|----------|---------|-------------|
| `test-on-push.yaml` | Push/PR | Calls reusable test workflow |
| `test-on-call.yaml` | Reusable (workflow_call) | tidy, lint, test with race detector |
| `release-on-tag.yaml` | Version tag (`v*.*.*`) | Test, build, push images, create GitHub release |
| `deploy-saas.yaml` | Manual (workflow_dispatch) | Deploy to Cloud Run |
| `deploy-cloud-run.yaml` | Reusable (workflow_call) | Cloud Run deployment logic |
| `yamllint-on-push.yaml` | Push/PR (YAML changes) | YAML linting |

Deployment workflows use Workload Identity Federation for GCP auth — no stored credentials.

### Release Process

Use `make bump-patch`, `make bump-minor`, or `make bump-major` to tag and push. goreleaser v2 compiles binaries, ko builds container images. Cloud Run service updated via `deploy-saas.yaml` or `release-on-tag.yaml`.

---

---

## Local Testing

### Prerequisites

- Go 1.26+, Docker, `curl`, `jq`
- A GitHub Personal Access Token (PAT) with `read:user` scope

### Quick Start

```bash
make db-up                              # start Postgres
make server                             # run dev server
curl -s localhost:8080/health           # verify
make seed                               # create test tenant + API token
```

For GitHub API features: `GITHUB_TOKEN=ghp_... make server`

### Test Scoring API

```bash
TOKEN="dt_your_token_here"

# Unauthenticated (score + grade only)
curl -s http://localhost:8080/api/v1/score/octocat | jq .

# Authenticated (full signals + risk summary)
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/mchmarny | jq .

# With repo context
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/v1/score/torvalds?repo=torvalds/linux" | jq '.repo_context'

# Bot detection (immediate score 0, no API calls)
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/dependabot%5Bbot%5D | jq .
```

### Run Tests

```bash
make test                               # unit tests (skips DB tests without Postgres)
make lint                               # linting
make qualify                            # test + lint + vulncheck
```

### Known Test Interactions

- **TestNewClientNoKey**: Fails if `DEVTRACE_ANTHROPIC_API_KEY` is set in your shell. Unset it or run tests in a clean env.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable` | PostgreSQL connection URI |
| `PORT` | `8080` | HTTP server port |
| `BASE_URL` | `http://localhost:8080` | Public URL |
| `DEVTRACE_DEBUG` | `false` | Debug logging |
| `TRUST_PROXY` | `false` | Trust X-Forwarded-For (required on Cloud Run) |
| `GITHUB_TOKEN` | — | PAT for GitHub API (dev) |
| `GITHUB_OAUTH_CLIENT_ID` | — | OAuth App client ID |
| `GITHUB_OAUTH_CLIENT_SECRET` | — | OAuth App client secret |
| `GITHUB_APP_ID` | — | GitHub App ID (prod) |
| `GITHUB_APP_KEY_PATH` | — | GitHub App private key PEM path |
| `GITHUB_APP_INSTALLATION_ID` | — | Default installation ID (prod) |
| `GITHUB_WEBHOOK_SECRET` | — | Webhook HMAC secret |
| `ANTHROPIC_API_KEY` | — | Claude API key (enables risk narratives) |
| `ANTHROPIC_MODEL` | `claude-haiku-4-5-20251001` | Claude model for analysis |
| `DEVTRACE_ADMIN_USERS` | — | Comma-separated GitHub usernames for admin access |
| `ENABLE_BACKGROUND_OPS` | `false` | Enable sync + scorer background routines |
| `SCORE_RATE_LIMIT` | `60` | Requests/hour (unauth) |
| `OAUTH_RATE_LIMIT` | `20` | OAuth starts/minute per IP |
| `SCORE_CACHE_TTL_SEC` | `1800` | Score cache TTL (30 min) |
| `SCORER_INTERVAL_SEC` | `3600` | Background scorer interval |
| `SCORER_BATCH_SIZE` | `100` | Scoring queue batch size |
| `SCORER_MIN_QUOTA_PCT` | `30` | Min aggregate token quota % before pausing scorer |
| `SCORER_LOW_STALE_DAYS` | `7` | Rescore low-score contributors after N days |
| `SCORER_HIGH_STALE_DAYS` | `30` | Rescore high-score contributors after N days |
| `DEVPULSE_SYNC_INTERVAL_SEC` | `1800` | DevPulse sync interval |
| `GHARCHIVE_BASE_URL` | (gharchive.org) | Override for testing |
| `GHARCHIVE_LOOKBACK_HOURS` | `1` | Fresh install bootstrap hours |
| `GHARCHIVE_CATCHUP_MAX_HOURS` | `24` | Max hours to process when behind |
| `GHARCHIVE_BACKFILL_DAYS` | `0` | Historical backfill depth (0 = disabled) |
| `GHARCHIVE_BACKFILL_BATCH_SIZE` | `18` | Hours per backfill batch |
| `GHARCHIVE_BACKFILL_WORKERS` | `3` | Concurrent backfill workers |
| `DB_MAX_OPEN_CONNS` | `10` | Postgres pool max open |
| `DB_MAX_IDLE_CONNS` | `5` | Postgres pool max idle |
| `SERVER_SHUTDOWN_TIMEOUT_SEC` | `5` | Graceful shutdown timeout |
| `SEND_API_KEY` | — | Email service API key (enables contact form) |
| `SUPPORT_EMAIL` | — | Support email address for contact form |

---

## Lessons Learned

- **Shared DB**: DevTrace uses `devtrace_tenant` and `devtrace_schema_version` tables to avoid collision with DevPulse.
- **Installation filtering**: Filter GitHub App installations by `app_id` — querying all rows returns DevPulse installations that 404.
- **`--set-env-vars` replaces all**: Use `--update-env-vars` for incremental changes, or manage all env vars in Terraform.
- **OAuth callback URL**: Must exactly match `${BASE_URL}/auth/github/callback`.
- **Webhook cert delay**: Cloud Run domain mappings need time for TLS cert provisioning. Redeliver failed webhooks from GitHub App settings after cert is live.
