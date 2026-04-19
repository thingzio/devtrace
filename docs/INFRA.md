# DevTrace — Infrastructure & Operations

DevTrace shares the same GCP project, VPC, and Cloud SQL instance as DevPulse. It gets its own Cloud Run services, Artifact Registry repo, scheduler jobs, secrets, and service accounts.

---

## GCP Resources

### Cloud Run

| Resource | Type | Description |
|----------|------|-------------|
| `devtrace-saas-serve` | Service | Web UI + REST API + background workers (auto-scaling 0-10) |

Single binary (`devtrace-site`) handles HTTP serving, background ingestion, and continuous scoring. `TRUST_PROXY=true` required in production — Cloud Run sits behind Google's LB, and the rate limiter needs the real client IP from `X-Forwarded-For`.

### Supporting Resources

| Resource | Type | Description |
|----------|------|-------------|
| `devtrace-saas-images` | Artifact Registry | Container images |
| `devtrace-saas-run` | Service Account | Runtime SA for Cloud Run |
| `github-actions-devtrace-saas` | Service Account | CI/CD deployer via WIF |

### Secrets (Secret Manager)

| Secret | Used by |
|--------|---------|
| `devtrace-saas-github-app-key` | serve (GitHub App auth) |
| `devtrace-saas-oauth-client-secret` | serve (OAuth flow) |
| `devtrace-saas-webhook-secret` | serve (webhook verification) |
| `devtrace-saas-anthropic-api-key` | serve (Claude API) |

All secret names are prefixed with `${var.prefix}` (`devtrace-saas`) to avoid collision with DevPulse secrets.

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
├── main.tf                    # APIs, locals
├── providers.tf               # GCP provider + backend
├── variables.tf               # All input variables with defaults
├── terraform.tfvars           # Variable values
├── cloudrun.tf                # Cloud Run service
├── scheduler.tf               # Cloud Scheduler
├── iam.tf                     # Runtime SA, deployer SA, WIF
├── secrets.tf                 # Secret Manager resources + IAM
├── artifact-registry.tf       # Container image repo
├── database.tf                # DB user, password
├── monitoring.tf              # Cloud Monitoring dashboards
├── dashboard_service.json     # Service dashboard definition
├── dashboard_pipeline.json    # Pipeline dashboard definition
├── outputs.tf                 # Service URL, SA emails, AR repo
└── terraformrc                # Provider mirror config
```

### Terraform Bootstrap Variables

| Variable | Purpose |
|----------|---------|
| `github_oauth_client_id` | OAuth App client ID (persisted in Cloud Run env) |
| `github_app_id` | GitHub App ID (persisted in Cloud Run env) |
| `github_token` | Bootstrap only — container registry auth during first apply. Not stored in state. |

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
| `make lint` | Go vet + golangci-lint + yamllint + tfsec |
| `make qualify` | test-coverage + lint + vulncheck + e2e |
| `make vulncheck` | Scan for known vulnerabilities |
| `make e2e` | End-to-end tests (requires Docker) |
| `make db-up` / `db-down` | Local Postgres lifecycle |
| `make db-connect` | psql shell to local Postgres |
| `make seed` | Create test tenant + API token |
| `make server` | Run devtrace-site locally |
| `make build` / `release` | goreleaser build/release |
| `make tf-init` / `tf-plan` / `tf-apply` | Terraform operations |
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
| `tfsec-on-push.yaml` | Push/PR (infra changes) | Terraform security scanning |
| `yamllint-on-push.yaml` | Push/PR (YAML changes) | YAML linting |

Deployment workflows use Workload Identity Federation for GCP auth — no stored credentials.

### Release Process

Use `make bump-patch`, `make bump-minor`, or `make bump-major` to tag and push. goreleaser v2 compiles binaries, ko builds container images. Cloud Run service updated via `deploy-saas.yaml` or `release-on-tag.yaml`.

---

## Bootstrap Guide

Step-by-step guide to deploy DevTrace from scratch on GCP.

### Prerequisites

- [gcloud CLI](https://cloud.google.com/sdk/docs/install) installed and authenticated
- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.13
- [ko](https://ko.build/install/) for building container images
- [gh CLI](https://cli.github.com/) for GitHub Actions environment setup
- GCP project with billing enabled (shared with DevPulse)
- Domain name with DNS access at your registrar
- GitHub account with org admin access
- `GITHUB_TOKEN` with `write:packages` scope

### 1. Set Environment

```shell
export PROJECT_ID="thingzio"
export REGION="us-west1"
export DOMAIN="devtrace.thingz.io"
```

### 2. Register GitHub OAuth App

Go to https://github.com/settings/applications/new

| Field | Value |
|-------|-------|
| Application name | DevTrace |
| Homepage URL | `https://$DOMAIN` |
| Authorization callback URL | `https://$DOMAIN/auth/github/callback` |

Save the **Client ID** and generate a **Client Secret**.

```shell
export GITHUB_OAUTH_CLIENT_ID="your-client-id"
```

### 3. Register GitHub App

Go to https://github.com/settings/apps/new

| Field | Value |
|-------|-------|
| GitHub App name | Must be globally unique (e.g. `DevTraceThingz`) |
| Homepage URL | `https://$DOMAIN` |
| Webhook URL | `https://$DOMAIN/webhook/github` |
| Webhook secret | `openssl rand -hex 32` |

Permissions:
- **Repository**: Metadata (Read-only), Contents (Read-only), Pull requests (Read-only)
- **Organization**: Members (Read-only) — required for org membership checks and trusted_orgs

Subscribe to events: none required. Installation events are sent automatically.

After creating: note the **App ID**, download the **private key** (.pem), note the **webhook secret**.

```shell
export GITHUB_APP_ID="your-app-id"
```

### 4. First Terraform Apply (creates infra — Cloud Run will fail)

Cloud Run needs images + secrets to start. First apply creates the infra — Cloud Run will error, that's expected.

```shell
cd infra/saas
terraform init
terraform apply \
  -var="github_oauth_client_id=$GITHUB_OAUTH_CLIENT_ID" \
  -var="github_app_id=$GITHUB_APP_ID"
```

### 5. Store Secret Values

```shell
echo -n "YOUR_OAUTH_CLIENT_SECRET" | \
gcloud secrets versions add devtrace-saas-oauth-client-secret \
    --project=$PROJECT_ID --data-file=-

echo -n "YOUR_WEBHOOK_SECRET" | \
gcloud secrets versions add devtrace-saas-webhook-secret \
    --project=$PROJECT_ID --data-file=-

gcloud secrets versions add devtrace-saas-github-app-key \
    --project=$PROJECT_ID --data-file=path/to/devtrace.pem

echo -n "YOUR_ANTHROPIC_API_KEY" | \
gcloud secrets versions add devtrace-saas-anthropic-api-key \
    --project=$PROJECT_ID --data-file=-
```

### 6. Push Bootstrap Images

```shell
gcloud auth configure-docker $REGION-docker.pkg.dev --quiet

AR_REGISTRY=$REGION-docker.pkg.dev/$PROJECT_ID/devtrace-saas-images

KO_DOCKER_REPO=${AR_REGISTRY}/devtrace-site ko build ./cmd/devtrace-site/ --bare --tags latest
```

### 7. Second Terraform Apply (completes Cloud Run)

```shell
cd infra/saas
terraform apply \
  -var="github_oauth_client_id=$GITHUB_OAUTH_CLIENT_ID" \
  -var="github_app_id=$GITHUB_APP_ID"
```

> `deletion_protection = false` during initial setup. Set to `true` after successful verification.

### 8. Configure GitHub Actions

```shell
cd ../..  # back to repo root
./tools/setup-gh-env
```

Creates variables in the GitHub `saas` environment: `WIF_PROVIDER`, `DEPLOYER_SA`, `SERVICE_NAME`, `REGION`, `PROJECT_ID`, `AR_REPO`.

### 9. Configure DNS

Add a CNAME record for `devtrace` pointing to `ghs.googlehosted.com.`:

```shell
gcloud beta run domain-mappings create \
    --service=devtrace-saas-serve \
    --domain=$DOMAIN \
    --project=$PROJECT_ID \
    --region=$REGION
```

### 10. First Release

```shell
make bump-minor
```

### 11. Post-Deploy Configuration

```shell
# Enable XFF trust (required for rate limiting behind Cloud Run LB)
gcloud run services update devtrace-saas-serve \
    --region=$REGION --set-env-vars=TRUST_PROXY=true

# Enable background scoring and DevPulse sync
gcloud run services update devtrace-saas-serve \
    --region=$REGION --set-env-vars=ENABLE_BACKGROUND_OPS=true

# Enable deletion protection after verifying
# Edit infra/saas/cloudrun.tf — set deletion_protection = true
cd infra/saas && terraform apply
```

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
