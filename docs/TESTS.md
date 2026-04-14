# DevTrace — Local Testing Guide

## Prerequisites

- Go 1.26+
- Docker (for Postgres)
- A GitHub Personal Access Token (PAT) with `read:user` scope
- `curl` and `jq`

## 1. Start Postgres

```bash
make db-up
```

Verify:
```bash
docker compose ps
# db should be healthy
```

## 2. Run the Server

```bash
make server
```

This starts `devtrace-site` with the local database and debug logging. For GitHub API features, set `GITHUB_TOKEN`:

```bash
GITHUB_TOKEN=ghp_your_pat_here make server
```

You should see:
```
{"level":"INFO","msg":"starting devtrace-site","version":"v0.0.1-default"}
{"level":"INFO","msg":"using PAT GitHub client"}
{"level":"INFO","msg":"listening","addr":":8080"}
```

## 3. Test Health Endpoint

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health
# 200
```

## 4. Test Unauthenticated Scoring

```bash
curl -s http://localhost:8080/api/v1/score/octocat | jq .
```

Expected: score + grade only, no signals. `detail` field suggests sign-up.

```bash
curl -s http://localhost:8080/api/v1/score/torvalds | jq '.score'
```

## 5. Seed a Test Tenant + API Token

```bash
make seed
```

Creates a test tenant (free plan), accepts ToS, mints an API token:

```
Tenant ID:  a1b2c3d4-...
Username:   test-user
Plan:       free
API Token:  dt_8f3a...

Test with:
  curl -s -H 'Authorization: Bearer dt_8f3a...' http://localhost:8080/api/v1/score/octocat | jq .
```

The raw token is shown once. The database stores only the SHA-256 hash.

## 6. Test Authenticated Scoring

```bash
TOKEN="dt_your_token_here"

# Full response: signals + risk summary + categories
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/mchmarny | jq .

# With repo context (adds repo_context with org_member, commits_verified, etc.)
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/v1/score/torvalds?repo=torvalds/linux" | jq '.repo_context'
```

Compare authenticated vs unauthenticated:
```bash
# Unauth — minimal
curl -s http://localhost:8080/api/v1/score/octocat | jq 'keys'
# ["cached_at","detail","provider","score","scored_at","username","version"]

# Auth — full
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/octocat | jq 'keys'
# ["provider","risk_summary","score","scored_at","signals","username","version"]
```

## 7. Test Bot Detection

Bots return score 0 immediately with no GitHub API calls:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/dependabot%5Bbot%5D | jq .
# {"version":"...","username":"dependabot[bot]","score":{"grade":"F","value":0},"risk_summary":"Bot account detected..."}
```

## 8. Test Rate Limiting

Default: 60 requests per hour.

```bash
for i in $(seq 1 65); do
  code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/v1/score/octocat)
  echo "Request $i: $code"
done
# After 60: 429
```

Override for testing:
```bash
SCORE_RATE_LIMIT=1000 GITHUB_TOKEN=ghp_... make server
```

## 9. Test Ingest Pipeline

Run the ingest job locally (requires Postgres):

```bash
DATABASE_URL="postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable" make ingest
```

Expected output:
```
{"level":"INFO","msg":"ingest starting","hours":1,"tenant_orgs":0}
{"level":"INFO","msg":"processing archive","hour":"2026-04-14-07"}
{"level":"INFO","msg":"aggregated","events":9086,"contributors":3912}
{"level":"INFO","msg":"stored activity","rows":3912}
{"level":"INFO","msg":"queued for scoring","count":3912}
{"level":"INFO","msg":"compaction complete","rows_deleted":0}
{"level":"INFO","msg":"ingest complete"}
```

Verify data landed:
```bash
make db-connect
# SELECT COUNT(*) FROM contributor_activity;
# SELECT COUNT(*) FROM scoring_queue;
# SELECT username, prs_opened, reviews_given FROM contributor_activity LIMIT 5;
```

After ingest, behavioral signals appear in API responses:
```bash
# Pick a username from contributor_activity
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/USERNAME | jq '.behavior'
```

## 10. Test Token Management

Token management requires session auth. Seed a session for local testing:

```sql
-- In psql (make db-connect):
INSERT INTO session (id, tenant_id, expires_at)
VALUES (
  -- echo -n "test-session-token" | shasum -a 256 | cut -d' ' -f1
  '4f5e3e1c74e0e0e5e8ef7c79a8f764a4d5c9c7e2b3d6e4c8a1f3d5e7b9c2d4f6',
  'a0000000-0000-0000-0000-000000000001',
  NOW() + INTERVAL '7 days'
);
```

```bash
COOKIE="session=test-session-token"

# List tokens
curl -s -b "$COOKIE" http://localhost:8080/api/v1/token | jq .

# Create token
curl -s -b "$COOKIE" -X POST \
  -H "Content-Type: application/json" \
  -d '{"name":"ci-token"}' \
  http://localhost:8080/api/v1/token | jq .

# Revoke (replace TOKEN_ID)
curl -s -b "$COOKIE" -X DELETE http://localhost:8080/api/v1/token/TOKEN_ID
```

## 11. Run the Full Test Suite

```bash
# Unit tests (always work, skips DB tests without Postgres)
make test

# With local Postgres (also runs integration tests)
DATABASE_URL=postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable \
  go test ./... -v -race

# Lint
make lint

# Full qualify (test + lint + vulncheck)
make qualify
```

## Cleanup

```bash
# Stop Postgres (preserves data)
make db-down

# Full reset (destroys all data, useful for migration testing)
docker compose down -v

rm -f devtrace-site devtrace-ingest
```

## Known Test Interactions

- **TestNewClientNoKey**: This test asserts that the Claude client is nil when no API key is configured. If `DEVTRACE_ANTHROPIC_API_KEY` is set in your shell environment, the test will fail because the client initializes successfully. Unset the variable or run tests in a clean env.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHUB_TOKEN` | — | PAT for GitHub API (dev) |
| `PORT` | `8080` | HTTP server port |
| `DATABASE_URL` | `postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable` | Postgres DSN |
| `DEVTRACE_DEBUG` | `false` | Debug logging |
| `BASE_URL` | `http://localhost:8080` | Public URL |
| `SCORE_RATE_LIMIT` | `60` | Requests/hour (unauth) |
| `OAUTH_RATE_LIMIT` | `20` | OAuth starts/minute per IP |
| `SCORE_CACHE_TTL_SEC` | `300` | Score cache TTL (5 min) |
| `TRUST_PROXY` | `false` | Trust X-Forwarded-For for rate limiting (required on Cloud Run) |
| `ENABLE_BACKGROUND_OPS` | `false` | Enable sync + scorer background routines |
| `SCORER_INTERVAL_SEC` | `3600` | Background scorer interval |
| `SCORER_LOW_STALE_DAYS` | `7` | Rescore low-score contributors after N days |
| `SCORER_HIGH_STALE_DAYS` | `30` | Rescore high-score contributors after N days |
| `DEVPULSE_SYNC_INTERVAL_SEC` | `1800` | DevPulse sync interval |
| `GHARCHIVE_BASE_URL` | (gharchive.org) | Override for testing |
| `GHARCHIVE_LOOKBACK_HOURS` | `1` | Fresh install bootstrap hours |
| `GHARCHIVE_CATCHUP_MAX_HOURS` | `24` | Max hours to process when behind |
| `ANTHROPIC_API_KEY` | — | Claude API key (enables risk narratives) |
| `ANTHROPIC_MODEL` | `claude-haiku-4-5-20251001` | Claude model for analysis |
| `GITHUB_OAUTH_CLIENT_ID` | — | OAuth App client ID |
| `GITHUB_OAUTH_CLIENT_SECRET` | — | OAuth App client secret |
| `GITHUB_APP_ID` | — | GitHub App ID (prod) |
| `GITHUB_APP_KEY_PATH` | — | GitHub App private key PEM path |
| `GITHUB_APP_INSTALLATION_ID` | — | Default installation ID (prod, single) |
| `GITHUB_WEBHOOK_SECRET` | — | Webhook HMAC secret |
| `DB_MAX_OPEN_CONNS` | `10` | Postgres pool max open |
| `DB_MAX_IDLE_CONNS` | `5` | Postgres pool max idle |
| `SERVER_SHUTDOWN_TIMEOUT_SEC` | `5` | Graceful shutdown timeout |
