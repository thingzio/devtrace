# Bootstrap Guide

Step-by-step guide to deploy DevTrace from scratch on GCP. Steps are ordered to minimize friction — each step depends only on previous steps.

## Prerequisites

- [gcloud CLI](https://cloud.google.com/sdk/docs/install) installed and authenticated
- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.13
- [ko](https://ko.build/install/) for building container images
- [gh CLI](https://cli.github.com/) for GitHub Actions environment setup
- GCP project with billing enabled (shared with DevPulse)
- Domain name with DNS access at your registrar
- GitHub account with org admin access
- `GITHUB_TOKEN` with `write:packages` scope

## 1. Set Environment

```shell
export PROJECT_ID="thingzio"
export REGION="us-west1"
export DOMAIN="devtrace.thingz.io"
```

## 2. Terraform State Bucket

DevTrace uses the shared state bucket `gs://thingzio-infra-state` with prefix `devtrace`. This bucket is created by the `thingzio/infra` repo — no action needed here.

## 3. Register GitHub OAuth App

Go to https://github.com/settings/applications/new

| Field | Value |
|-------|-------|
| Application name | DevTrace |
| Homepage URL | `https://$DOMAIN` |
| Authorization callback URL | `https://$DOMAIN/auth/github/callback` |

Save the **Client ID** (short, like `Ov23li...`) and generate a **Client Secret** (40-char hex).

```shell
export GITHUB_OAUTH_CLIENT_ID="your-client-id"
```

## 4. Register GitHub App

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

Subscribe to events: none required. Installation events (create/delete/suspend) are sent automatically for all GitHub Apps — no checkbox needed.

After creating: note the **App ID**, download the **private key** (.pem), note the **webhook secret**.

```shell
export GITHUB_APP_ID="your-app-id"
```

## 5. Push Bootstrap Images

Terraform needs images to exist before creating Cloud Run resources. Push manually (one-time):

```shell
# Authenticate to Artifact Registry
gcloud auth configure-docker $REGION-docker.pkg.dev --quiet

# Set the AR registry path
AR_REGISTRY=$REGION-docker.pkg.dev/$PROJECT_ID/devtrace-saas-images

# Build and push
KO_DOCKER_REPO=${AR_REGISTRY}/devtrace-site ko build ./cmd/devtrace-site/ --bare --tags latest
KO_DOCKER_REPO=${AR_REGISTRY}/devtrace-ingest ko build ./cmd/devtrace-ingest/ --bare --tags latest
```

## 6. Create Secrets (Empty)

Terraform creates Secret Manager resources, but Cloud Run fails if secrets have no versions. Create initial versions **before** `terraform apply`:

```shell
# Terraform must run first to create the secret containers.
# If bootstrapping fresh, run terraform apply once (it will fail on Cloud Run),
# then add secret versions, then apply again.
# If the secrets already exist (e.g. from a previous deploy), add versions directly.

# GitHub OAuth client secret
echo -n "YOUR_OAUTH_CLIENT_SECRET" | \
gcloud secrets versions add devtrace-saas-oauth-client-secret \
    --project=$PROJECT_ID --data-file=-

# GitHub webhook secret
echo -n "YOUR_WEBHOOK_SECRET" | \
gcloud secrets versions add devtrace-saas-webhook-secret \
    --project=$PROJECT_ID --data-file=-

# GitHub App private key
gcloud secrets versions add devtrace-saas-github-app-key \
    --project=$PROJECT_ID --data-file=path/to/devtrace.pem

# Anthropic API key (enables Claude risk narratives)
echo -n "YOUR_ANTHROPIC_API_KEY" | \
gcloud secrets versions add devtrace-saas-anthropic-api-key \
    --project=$PROJECT_ID --data-file=-
```

> **Note:** Secret Manager resources are created by Terraform, but you must add versions (values) manually. Terraform creates empty secret containers — Cloud Run fails if it references a secret with no versions.

## 7. Run Terraform

```shell
cd infra/saas
terraform init
terraform apply \
  -var="github_oauth_client_id=$GITHUB_OAUTH_CLIENT_ID" \
  -var="github_app_id=$GITHUB_APP_ID"
```

This creates:
- DB user (`devtrace`) in shared Cloud SQL instance
- Secret Manager secrets (4: oauth, webhook, app key, anthropic)
- Service accounts (runtime, scheduler invoker, deployer)
- Artifact Registry standard repo (`devtrace-saas-images`)
- Cloud Run service (`devtrace-saas-serve`) + job (`devtrace-saas-ingest`)
- Cloud Scheduler (hourly at :20)
- WIF for GitHub Actions

VPC, Cloud SQL instance, and networking are owned by `thingzio/infra`.

> **First apply note:** `deletion_protection = false` is set in `cloudrun.tf`. Set to `true` after successful deploy.

Note the outputs:
```shell
terraform output
```

## 8. Configure GitHub Actions

Populate the `saas` environment variables from Terraform outputs:

```shell
cd ../..  # back to repo root
./tools/setup-gh-env
```

This creates 7 variables in the GitHub `saas` environment:
`WIF_PROVIDER`, `DEPLOYER_SA`, `SERVICE_NAME`, `JOB_NAME`, `REGION`, `PROJECT_ID`, `AR_REPO`

## 9. Configure DNS

Add a CNAME record for `devtrace` pointing to `ghs.googlehosted.com.` and create a Cloud Run domain mapping:

```shell
gcloud beta run domain-mappings create \
    --service=devtrace-saas-serve \
    --domain=$DOMAIN \
    --project=$PROJECT_ID \
    --region=$REGION
```

## 10. First Release

```shell
make bump-minor
```

This triggers the release pipeline:
1. Tests (unit, lint, integration, tfsec, e2e)
2. Builds `devtrace-site` and `devtrace-ingest` images via goreleaser + ko
3. Pushes to Artifact Registry
4. Deploys to Cloud Run (service + ingest job)
5. Publishes GitHub release

## 11. Verify

```shell
# Check service URL
gcloud run services describe devtrace-saas-serve \
    --region=$REGION --format='value(status.url)'

# Open in browser
open https://$DOMAIN

# Health check
curl -s https://$DOMAIN/health

# After signing in, install the GitHub App on your org:
# https://github.com/apps/DevTraceThingz
# This grants API access for scoring contributors in your repos.

# Trigger manual ingest (verify GH Archive pipeline)
gcloud run jobs execute devtrace-saas-ingest \
    --region=$REGION --project=$PROJECT_ID

# Check ingest logs
gcloud logging read 'resource.type="cloud_run_job" AND jsonPayload.msg=~"ingest"' \
    --project=$PROJECT_ID --limit=10 \
    --format='table(timestamp, jsonPayload.msg)'

# Test the API
TOKEN="your-api-token"
curl -s -H "Authorization: Bearer $TOKEN" \
    https://$DOMAIN/api/v1/score/octocat | jq .
```

## Post-Deploy

### Enable TRUST_PROXY

Cloud Run sits behind Google's load balancer. Enable XFF trust:

```shell
gcloud run services update devtrace-saas-serve \
    --region=$REGION \
    --set-env-vars=TRUST_PROXY=true
```

### Enable Background Operations

Background scoring and DevPulse sync are disabled by default:

```shell
gcloud run services update devtrace-saas-serve \
    --region=$REGION \
    --set-env-vars=ENABLE_BACKGROUND_OPS=true
```

### Configure Ingest Lookback

For first deploy, backfill 24 hours of GH Archive data:

```shell
gcloud run jobs update devtrace-saas-ingest \
    --region=$REGION \
    --set-env-vars=GHARCHIVE_LOOKBACK_HOURS=24
```

After initial backfill, set back to 1 (default) — the catchup mechanism handles missed windows automatically (up to 24 hours).

### Enable Deletion Protection

After verifying everything works:
```shell
# Edit infra/saas/cloudrun.tf — set deletion_protection = true
cd infra/saas && terraform apply
```

### Rotate Secrets

If any secrets were exposed during setup:
1. Regenerate in GitHub (OAuth App settings / GitHub App settings)
2. Update in Secret Manager: `gcloud secrets versions add <secret-name> --data-file=-`
3. Redeploy: `make bump-patch`
