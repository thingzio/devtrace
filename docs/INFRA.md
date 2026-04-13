# DevTrace — Infrastructure & Reuse Plan

What to copy from DevPulse (`thingzio/devpulse`), what to adapt, and what to build from scratch.

## Reuse Strategy

DevTrace shares the same GCP project, VPC, and Cloud SQL instance as DevPulse. It gets its own Cloud Run services, Artifact Registry repo, scheduler jobs, secrets, and service accounts. Code is copied and evolved independently — no shared Go libraries at runtime.

---

## Copy As-Is (adapt names/config only)

These components are generic patterns that work unchanged. Copy, rename `devpulse` → `devtrace`, update config values.

### Build & Quality Toolchain

| Source | What it provides |
|--------|-----------------|
| `.settings.yaml` | Single source of truth for Go version, tool versions, quality thresholds |
| `.goreleaser.yaml` | Multi-binary build, container image push to Artifact Registry via `kos` |
| `.golangci.yaml` | Linter configuration (~30 linters) |
| `.yamllint.yaml` | YAML validation rules |
| `.codecov.yaml` | Coverage tracking config |
| `docker-compose.yaml` | Local PostgreSQL 16 for development |

### Makefile

Copy the full Makefile. All quality targets are generic:

- `make tidy` / `make upgrade` — dependency management
- `make test` / `make test-coverage` / `make lint` / `make vulncheck` — quality gates
- `make qualify` — full pipeline (test + lint + vulncheck + e2e)
- `make db-up` / `make db-down` / `make db-connect` — local DB lifecycle
- `make build` / `make release` — goreleaser build/release
- `make bump-{major,minor,patch}` — version management
- `make setup` — install dev tools
- `make tf-init` / `make tf-plan` / `make tf-apply` — Terraform operations

Rename binary targets: `make server` → runs `devtrace-site`, `make import` → runs `devtrace-import` (if applicable).

### CI/CD Workflows

| Workflow | What it does | Adaptation needed |
|----------|-------------|-------------------|
| `test-on-push.yaml` | Trigger tests on push/PR | Change repo references |
| `test-on-call.yaml` | Reusable: unit, lint, integration, tfsec, e2e | Update service names |
| `release-on-tag.yaml` | Tag-triggered build + deploy + GitHub release | Update image names, Cloud Run service names |
| `deploy-saas.yaml` | Manual deploy dispatch | Update service/job names |
| `deploy-cloud-run.yaml` | Reusable Cloud Run deployment | Update resource names |

All workflows use Workload Identity Federation (WIF) for GCP auth — same pattern, new service accounts.

### Tools

| Script | Reusable? |
|--------|-----------|
| `setup-tools` | Yes — installs Go toolchain dependencies |
| `bump` | Yes — version management |
| `setup-gh-env` | Yes — GitHub environment setup |
| `e2e` | Copy structure, rewrite test cases |
| `common` | Yes — shared shell utilities |
| `db-connect` | Yes — adapt query for DevTrace tables |
| `db-snapshot` | Yes — generic |
| `tenant-*` scripts | Copy pattern, adapt for DevTrace tenant model |
| `metrics-review` / `job-detail` | Copy pattern, adapt for DevTrace metrics |

---

## Copy and Adapt (structural changes needed)

These components follow the same pattern but need modification for DevTrace's domain model.

### Go Packages

#### `pkg/config/env.go` — Environment variable management

Copy the typed accessor pattern (`GetEnv`, `GetEnvAsInt`, `GetEnvBool`, `GetEnvAsFloat`). Replace domain-specific getters:

- **Remove**: backfill config, import workers/parallelism, adoption timeout
- **Keep**: database pooling, HTTP transport, shutdown/trigger timeout, debug flag, GCP project
- **Add**: API rate limit tiers, metering config, Claude API config (model, key, batch settings), staleness windows for scoring cache, plan quota defaults

#### `pkg/oauth/github.go` — GitHub OAuth flow

Copy as-is. Same `BuildAuthURL()` / `ExchangeCode()` / `FetchUser()` pattern. DevTrace uses GitHub OAuth independently — own client ID/secret, own callback URL.

#### `pkg/middleware/auth.go` — Authentication middleware

Copy session cookie pattern (`RequireAuth()`, `TenantFromContext()`). Adapt:

- **Add**: API key authentication path (for REST API clients, CI/CD integrations)
- **Add**: Rate limiting middleware (per-plan throttling with real-time quota feedback)
- **Add**: Metering middleware (record API usage for operator analytics)
- Session auth for UI, API key auth for programmatic access — both inject tenant context

#### `pkg/tenant/` — Tenant management

Copy `UpsertTenant()` / `GetTenant*()` pattern. Adapt struct:

- **Remove**: MaxRepos, MaxEventsPerWeek (DevPulse-specific)
- **Add**: MaxContributors (per billing period), API key management, usage counters, plan-specific rate limits

#### `pkg/plan/plan.go` — Plan definitions

Copy the structure (plan enum + limits struct). Replace limits:

```
DevPulse: MaxRepos, MaxEventsPerWeek, MaxDataRangeMonths, AILevel, DeepReputation
DevTrace: MaxContributors, RateLimit (per hour/day), DeepScoring, LicenseAnalysis, AISensing, BatchAPI, Webhooks
```

Plan tiers: Free / Starter / Pro / Enterprise (same names, different dimensions).

#### `pkg/health/` — Health check endpoint

Copy the `/health` endpoint handler. The health scoring/grading logic is DevPulse-specific — don't copy that.

#### `pkg/logging/` — Structured logging

Copy as-is. Same structured JSON logging pattern.

#### `pkg/net/client.go` — HTTP client utilities

Copy shared client pattern (timeout config, transport settings, OAuth client factory). Add Claude API client configuration.

#### `cmd/` — Binary entry points

Copy the entry point pattern (parse ldflags, setup logger, handle signals, call `Run()`). DevTrace binaries:

| Binary | Role | Analogous to |
|--------|------|-------------|
| `devtrace-site` | Web UI + REST API server | `devpulse-site` |
| `devtrace-scorer` | Background scoring worker (scheduled) | `devpulse-import` |

Note: DevTrace may not need a separate admin binary initially. Admin functions can be IAM-protected endpoints on the site service.

### Terraform (`infra/saas/`)

Copy the full Terraform structure. Same GCP project but separate resources:

| Resource | DevPulse | DevTrace |
|----------|----------|----------|
| VPC / Subnet | `devpulse-saas-vpc` | **Shared** — same VPC |
| Cloud SQL | Shared instance | **Shared** — own database or own schema |
| Cloud Run (serve) | `devpulse-saas-serve` | `devtrace-saas-serve` |
| Cloud Run (worker) | `devpulse-saas-import` (job) | `devtrace-saas-scorer` (job) |
| Cloud Run (admin) | `devpulse-saas-admin` | Not needed initially |
| Artifact Registry | `devpulse-saas-images` | `devtrace-saas-images` |
| Service Accounts | `devpulse-saas-run`, `devpulse-saas-import` | `devtrace-saas-run`, `devtrace-saas-scorer` |
| Secrets | 5 secrets (oauth, webhook, anthropic, etc.) | Similar set + DevTrace API signing key |
| Scheduler | Import every 2h, daily report | Scoring refresh (TBD cadence), cache cleanup |
| Monitoring | Uptime, latency, DB, import alerts | Uptime, latency, DB, API quota alerts |
| DNS | `devpulse.thingz.io` | `devtrace.thingz.io` |
| WIF (GitHub Actions) | `github-actions-devpulse-saas` | `github-actions-devtrace-saas` |

**Shared resources to reference (not recreate)**:
- VPC and subnet (use `data` sources to reference existing)
- Cloud SQL instance (connect to same instance, own schema/tables)
- Private service networking peering (already established)

---

## Build From Scratch

These are DevTrace-specific and have no DevPulse equivalent.

### API Layer

- **REST API router** — contributor scoring, license analysis, AI sensing endpoints
- **API key authentication** — issue, rotate, revoke keys per tenant
- **Rate limiting** — per-plan throttling with `X-RateLimit-*` response headers
- **Metering** — record every API call for quota enforcement and operator analytics
- **Quota feedback** — real-time remaining/limit in API responses

### Scoring Engine

- **Reputation scoring** — copy scoring model from `mchmarny/reputer`, evolve independently
- **Scoring cache** — staleness windows, cache invalidation on new contributions
- **Shallow vs. deep scoring pipeline** — free tier gets shallow, paid gets deep

### License Analysis

- **Contributor PR enumeration** — GitHub search API for merged PRs by author
- **License aggregation** — collect SPDX IDs, produce distribution summary
- **License cache** — per-contributor license profile with TTL

### AI Co-Development Sensing

- **Tier 1 analyzers** — metadata extraction (commit trailers, bot signatures, message patterns)
- **`AISignal` interface** — pluggable analyzer contract for future Tier 2/3
- **Claude API integration** — Tier 3 analysis (on-demand, batch, prompt caching)

### Data Model

- **DevTrace-owned tables** — contributor profiles, reputation scores, license profiles, AI signals, API keys, usage records, scoring cache
- **Migration framework** — copy DevPulse's migration runner pattern, own migration files

### Background Worker

- **Scoring refresh** — re-score stale contributors (configurable staleness per tier)
- **License crawl** — background enumeration for contributors outside DevPulse's repo set
- **Cache cleanup** — evict expired scoring/license/AI analysis results

### Account Management UI

- **Tenant dashboard** — plan status, quota consumption, API key management
- **Contributor search** — query any contributor, view score/license/AI signals
- **Alert configuration** — set reputation thresholds for notifications

---

## Shared Infrastructure Migration Plan

### Problem

DevPulse's Terraform currently owns project-wide shared resources (VPC, Cloud SQL, private networking). Any change to DevPulse infra could inadvertently break DevTrace or future services. The resource names (`devpulse-saas-vpc`, `devpulse-saas-pg`) reinforce the false impression that these are DevPulse-specific.

### Decision

Extract shared infrastructure ownership into a new `thingzio/infra` repo. Leave resource names as-is — GCP does not support renaming VPCs or Cloud SQL instances, and recreating them carries significant downtime risk for DevPulse in production.

### What moves to `thingzio/infra`

| Resource | Current Name | Current Owner |
|----------|-------------|---------------|
| VPC | `devpulse-saas-vpc` | `thingzio/devpulse` infra/saas/ |
| Subnet | `devpulse-saas-subnet` | `thingzio/devpulse` infra/saas/ |
| Cloud SQL instance | `devpulse-saas-pg` | `thingzio/devpulse` infra/saas/ |
| Private service networking | VPC peering to Cloud SQL | `thingzio/devpulse` infra/saas/ |

### What stays in each service repo

| Resource | Owner |
|----------|-------|
| Cloud Run services, service accounts, secrets, scheduler, monitoring | Each service's own repo |
| Artifact Registry repos | Each service's own repo |
| WIF providers | Each service's own repo |
| DNS records | Each service's own repo |
| DB users, schemas, grants | Each service's own repo |

### Migration Steps

1. **Create `thingzio/infra` repo** with Terraform config for shared resources
2. **`terraform import`** existing VPC, subnet, Cloud SQL, and private networking into the new state — no resource recreation, just ownership transfer
3. **Remove** these resource definitions from `thingzio/devpulse` infra/saas/, replace with `data` sources referencing the resources (same as DevTrace already does)
4. **Configure remote state** — both DevPulse and DevTrace read shared infra outputs (VPC ID, subnet ID, Cloud SQL instance name, connection name) from `thingzio/infra`'s Terraform remote state
5. **Verify** `terraform plan` in all three repos shows no changes (pure ownership transfer)
6. **Add CI protection** — `thingzio/infra` gets its own PR review workflow; changes to shared resources require explicit approval

### Timing

Execute before Phase 2 (auth + registration) deploys DevTrace to production. During Phase 1, DevTrace is local-only so there is no production dependency. This is the ideal window — DevPulse is the only consumer, making the migration low-risk.

### Why not rename resources?

- GCP does not support renaming VPCs or Cloud SQL instances
- Recreation requires: new resource → data migration → connection string updates → cutover → delete old
- DevPulse is in production — downtime and migration risk are not justified for a cosmetic change
- The resource names are just labels; Terraform ownership in `thingzio/infra` is what provides the safety boundary
