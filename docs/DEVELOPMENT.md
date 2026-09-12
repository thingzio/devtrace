# Development Guide

How to get DevTrace running locally and work on it day to day.

## Quick Start

```shell
git clone https://github.com/thingzio/devtrace && cd devtrace
make db-up     # local Postgres via docker compose
make seed      # seed a test tenant, prints an API token
make server    # run the dev server on :8080
```

You do **not** need a Google Cloud account or any credential from the
maintainer. If you hit a step that seems to require either, that is a bug —
please [file it](https://github.com/thingzio/devtrace/issues/new/choose).

Verify the checkout is healthy with the same gate CI runs:

```shell
make qualify
```

## Prerequisites

### Required

- **Go** — the version pinned in `go.mod`
- **Docker** — for the local Postgres and the e2e tests
- **make**

### Development tools

Tool versions live in `.settings.yaml`, which is the single source of truth
consumed by the Makefile, CI workflows, and `.golangci.yaml`. `make setup`
installs what is missing.

## Local development

### Database

`make db-up` starts Postgres via `docker compose` and waits for it to accept
connections. Migrations apply automatically on server start.

```shell
make db-up        # start
make db-connect   # psql shell
make db-down      # stop and remove
```

> **Port 5432.** DevPulse and DevRadar bind the same port in their own compose
> stacks. Run one at a time, or the second will fail to bind with a message that
> does not obviously say why.

### Environment

`DATABASE_URL` is the only variable strictly required to boot. `make server`
sets sensible development defaults including `DEVTRACE_DEBUG=true`.

Everything else is optional and degrades gracefully — an unset
`ANTHROPIC_API_KEY` disables AI risk sensing rather than failing, an unset
`SEND_API_KEY` disables the contact form.

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection URI — **required** |
| `BASE_URL` | Public base URL of this instance |
| `DEVTRACE_DEBUG` | `true` for debug-level logging |
| `DEVTRACE_ADMIN_USERS` | Comma-separated GitHub usernames granted `/admin` |
| `GITHUB_OAUTH_CLIENT_ID` / `_SECRET` | GitHub OAuth app, for sign-in |
| `GITHUB_APP_ID` / `GITHUB_APP_KEY_PATH` | GitHub App, for installation tokens |
| `GITHUB_WEBHOOK_SECRET` | HMAC secret for webhook verification |
| `ANTHROPIC_API_KEY` | Optional; enables AI risk sensing |
| `SCORER_BATCH_SIZE` | Scoring queue batch size (default 100) |
| `SCORER_MIN_QUOTA_PCT` | Pause the scorer below this % of aggregate token quota (default 30) |
| `SCORE_CACHE_TTL_SEC` | Score-response cache TTL (default 3600) |
| `GHARCHIVE_BACKFILL_DAYS` | Historical backfill depth (default 0 = disabled) |
| `DEVTRACE_*_TTL` / `DEVTRACE_*_TIMEOUT` | Per-source signal cache freshness and client timeouts |

The complete list, including every signal-source knob, is in the "Environment
Variables" section of [`.claude/CLAUDE.md`](../.claude/CLAUDE.md).

### Registering your own GitHub App

The maintainer's GitHub App (`DevTraceThingz`) cannot be shared — App private
keys are per-instance. To exercise sign-in, installations, or webhooks locally
you need your own.

1. Create a GitHub App at
   <https://github.com/settings/apps/new>.
2. Set the callback URL to `http://localhost:8080/auth/github/callback` and the
   webhook URL to your tunnel (`gh webhook forward`, ngrok, or similar).
3. Grant read access to repository metadata, contents, and pull requests.
4. Generate a private key, download the PEM, and point `GITHUB_APP_KEY_PATH` at it.
5. Set `GITHUB_APP_ID` and `GITHUB_WEBHOOK_SECRET` to match.

Most development does not need any of this. Scoring and ingestion work against
public GitHub data without an App.

## Make targets

`make help` prints all of them. The ones you will use:

### Local development

| Target | What it does |
|---|---|
| `make server` | Run the dev server with debug logging |
| `make db-up` / `db-down` / `db-connect` | Local Postgres lifecycle |
| `make seed` | Seed a test tenant and print an API token |

### Quality

| Target | What it does |
|---|---|
| `make test` | Unit tests, race detector, coverage profile |
| `make test-coverage` | The above plus the threshold check from `.settings.yaml` |
| `make lint` | `go vet`, golangci-lint, yamllint, trivy |
| `make vulncheck` | `govulncheck` |
| `make qualify` | Everything above plus e2e. **This is the CI gate.** |

### Build and release

| Target | What it does |
|---|---|
| `make build` | goreleaser single-target build |
| `make tidy` | `go mod tidy` + `go mod vendor` |
| `make bump-patch` / `bump-minor` / `bump-major` | Tag a release |

## Working on the code

### Dependencies are vendored

After changing `go.mod` or `go.sum`, run `make tidy` and commit `go.mod`,
`go.sum`, and `vendor/` together. CI fails if `vendor/` is out of sync.

### Layout

```
cmd/devtrace-site/   Entrypoint
pkg/server/          HTTP handlers, routing, rate limiting, templates
pkg/ingest/          GH Archive fetching and aggregation
pkg/background/      Ingestion scheduler, continuous scoring
pkg/score/           Scoring heuristics and grade calculation
pkg/bot/             Bot detection
pkg/ossf/            OSSF Scorecard signals
pkg/registry/        Package-registry publisher signals
pkg/stackoverflow/   Stack Exchange profile signals
pkg/forges/          Cross-VCS identity matching
pkg/claude/          Optional AI risk sensing
pkg/compliance/      NIST SSDF practice-to-signal mapping
pkg/tenant/          Tenants, sessions, API tokens, installations
pkg/data/postgres/   Store implementation and migrations
pkg/middleware/      Auth and admin gating
```

### Adding a tenant column

The `tenant` table is read through several hand-written SQL constants. Adding a
column means updating **all** of them, or authentication breaks in a way tests
may not catch:

- the SQL constants in `pkg/tenant/tenant.go`
- `scanTenant()` in `pkg/tenant/tenant.go`
- the `Tenant` struct
- the session validation SQL in `pkg/tenant/session.go`
- a migration in `pkg/data/postgres/sql/migrations/`

### Testing patterns

`testStore(t)` in `pkg/data/postgres/` and `testDB(t)` in `pkg/tenant/` connect
to Postgres and apply all migrations. Tests use the standard `testing` package —
no testify. Every test creates its own `ctx := context.Background()` and threads
it through Store calls.

## Debugging

### Common issues

| Symptom | Cause |
|---|---|
| Bind error on startup | Another service on 5432 — likely a DevPulse or DevRadar compose stack |
| `make qualify` fails at e2e | Docker not running |
| Migrations appear not to apply | Check the version table; `applyMigrations` silently skips files at or below the current high-water mark |
| Scorer does nothing | Aggregate GitHub token quota may be below `SCORER_MIN_QUOTA_PCT` |

### Debug logging

```shell
make server                  # already sets DEVTRACE_DEBUG=true
DEVTRACE_DEBUG=true go run ./cmd/devtrace-site
```

Logs are JSON via `log/slog`.

## Related documentation

- [CONTRIBUTING.md](../CONTRIBUTING.md) — contribution process, DCO, governance
- [SECURITY.md](../SECURITY.md) — vulnerability reporting
- [docs/INFRA.md](INFRA.md) — toolchain, migrations, CI and local testing
- [docs/EMAIL.md](EMAIL.md) — transactional email
- [`.claude/CLAUDE.md`](../.claude/CLAUDE.md) — conventions and the full environment variable list
