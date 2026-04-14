# DevTrace — Infrastructure

DevTrace shares the same GCP project, VPC, and Cloud SQL instance as DevPulse. It gets its own Cloud Run services, Artifact Registry repo, scheduler jobs, secrets, and service accounts.

---

## GCP Resources

### Cloud Run

| Resource | Type | Description |
|----------|------|-------------|
| `devtrace-saas-serve` | Service | Web UI + REST API (auto-scaling 0-10) |
| `devtrace-saas-ingest` | Job | Hourly GH Archive ingest (1 task, 55min timeout) |

`devtrace-saas-serve` requires `TRUST_PROXY=true` in production — Cloud Run sits behind Google's LB, and the rate limiter needs the real client IP from `X-Forwarded-For`.

### Supporting Resources

| Resource | Type | Description |
|----------|------|-------------|
| `devtrace-saas-images` | Artifact Registry | Container images for both binaries |
| `devtrace-saas-run` | Service Account | Runtime SA for both Cloud Run workloads |
| `devtrace-saas-scheduler` | Service Account | Cloud Scheduler invoker for ingest job |
| `github-actions-devtrace-saas` | Service Account | CI/CD deployer via WIF |
| `devtrace-saas-ingest-hourly` | Cloud Scheduler | Triggers ingest at :20 past each hour |

### Secrets (Secret Manager)

| Secret | Used by |
|--------|---------|
| `devtrace-saas-github-app-key` | serve (GitHub App auth) |
| `devtrace-saas-oauth-client-secret` | serve (OAuth flow) |
| `devtrace-saas-webhook-secret` | serve (webhook verification) |
| `devtrace-saas-anthropic-api-key` | serve + ingest (Claude API) |

All secret names are prefixed with `${var.prefix}` (`devtrace-saas`) to avoid collision with DevPulse secrets. Same env var names inside containers (e.g. `ANTHROPIC_API_KEY`), different GCP secret resources.

### Terraform Bootstrap Variables

| Variable | Purpose |
|----------|---------|
| `github_oauth_client_id` | OAuth App client ID (persisted in Cloud Run env) |
| `github_app_id` | GitHub App ID (persisted in Cloud Run env) |
| `github_token` | Bootstrap only — container registry auth during first apply. Not stored in state. |

### Shared Resources (from DevPulse/thingzio project)

| Resource | Current Name | Notes |
|----------|-------------|-------|
| VPC | `thingzio-vpc` | Referenced via `var.vpc_id` |
| Subnet | `thingzio-subnet` | Referenced via `var.subnet_id` |
| Cloud SQL | `thingzio-pg` | Shared instance, own DB user (`devtrace`) |
| Private networking | VPC peering | Already established |

### APIs Enabled

`artifactregistry`, `run`, `secretmanager`, `monitoring`, `iam`, `cloudscheduler`

---

## Terraform Structure

```
infra/saas/
├── main.tf              # APIs, locals
├── providers.tf         # GCP provider + backend
├── variables.tf         # All input variables with defaults
├── cloudrun.tf          # serve service + ingest job
├── scheduler.tf         # Cloud Scheduler + invoker SA
├── iam.tf               # Runtime SA, deployer SA, WIF
├── secrets.tf           # Secret Manager resources + IAM
├── artifact-registry.tf # Container image repo
├── database.tf          # DB user, password
├── outputs.tf           # Service URL, SA emails, AR repo
└── terraformrc          # Provider mirror config
```

---

## Binaries

| Binary | Entry Point | Purpose | Run Target |
|--------|------------|---------|------------|
| `devtrace-site` | `cmd/devtrace-site/` | Web UI + REST API server | `make server` |
| `devtrace-ingest` | `cmd/devtrace-ingest/` | GH Archive hourly ingest | `make ingest` |

Both built by goreleaser with ko for container images. Multi-arch (amd64/arm64) Linux builds. ldflags inject version, commit, date.

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
| `make lint` | Go vet + golangci-lint + yamllint + tfsec |
| `make qualify` | test + lint + vulncheck |
| `make db-up` / `db-down` | Local Postgres lifecycle |
| `make seed` | Create test tenant + API token |
| `make server` | Run devtrace-site locally |
| `make ingest` | Run devtrace-ingest locally |
| `make build` / `release` | goreleaser build/release |
| `make tf-init` / `tf-plan` / `tf-apply` | Terraform operations |

---

## CI/CD Workflows

| Workflow | Trigger | What it does |
|----------|---------|-------------|
| `test-on-push.yaml` | Push/PR | Unit tests, lint, coverage |
| `release-on-tag.yaml` | Version tag | Test → build → push images → create GitHub release |

Both use Workload Identity Federation for GCP auth — no stored credentials.

---

## Database

### Migrations

| File | Tables |
|------|--------|
| `001_initial.sql` | tenant, session, api_token, contributor, reputation, score_history, usage_record, github_app_installation |
| `002_sync_state.sql` | sync_state |
| `003_gharchive.sql` | contributor_activity, scoring_queue |

Applied automatically at startup by `store.Migrate(ctx)`.

### Schema Boundary

DevTrace tables are DevTrace-owned. DevPulse tables are read-only. DevTrace reads from DevPulse's `developer` table for initial contributor discovery (via background sync). GH Archive provides independent discovery.

---

## Shared Infrastructure Migration

DevPulse's Terraform currently owns VPC, Cloud SQL, and private networking. Ideally these move to a shared `thingzio/infra` repo. Current status: works as-is with `data` source references. Migration is low priority — no functional impact.
