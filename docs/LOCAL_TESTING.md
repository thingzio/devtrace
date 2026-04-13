# DevTrace — Local Testing Guide

How to run and test DevTrace locally after Phase 1 + Phase 2.

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
GITHUB_TOKEN=ghp_your_pat_here go run ./cmd/devtrace-site/
```

You should see:
```
{"level":"INFO","msg":"starting devtrace-site","version":"v0.0.1-default"}
{"level":"INFO","msg":"using PAT GitHub client"}
{"level":"INFO","msg":"listening","port":"8080"}
```

For debug logging:
```bash
DEVTRACE_DEBUG=true GITHUB_TOKEN=ghp_your_pat_here go run ./cmd/devtrace-site/
```

## 3. Test Health Endpoint

```bash
curl -s http://localhost:8080/health
# Should return 200 (empty body)

curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health
# 200
```

## 4. Test Unauthenticated Scoring

Score a well-known contributor:
```bash
curl -s http://localhost:8080/api/v1/score/octocat | jq .
```

Expected: score with grade, value, model version. No signals (unauth gets minimal response with `detail` field suggesting sign-up).

Score with repo context:
```bash
curl -s "http://localhost:8080/api/v1/score/torvalds?repo=torvalds/linux" | jq .
```

Try a few different users to compare scores:
```bash
# Well-established contributor
curl -s http://localhost:8080/api/v1/score/jessfraz | jq '.score'

# Your own account
curl -s http://localhost:8080/api/v1/score/mchmarny | jq '.score'
```

## 5. Test Rate Limiting

Default: 60 requests per hour. Rapid-fire test:
```bash
for i in $(seq 1 65); do
  code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/v1/score/octocat)
  echo "Request $i: $code"
done
```

After 60 requests you should see `429` responses with:
```bash
curl -s http://localhost:8080/api/v1/score/octocat -w "\n%{http_code}"
# 429
# {"error":"rate limit exceeded"}
```

To test with a higher limit:
```bash
SCORE_RATE_LIMIT=1000 GITHUB_TOKEN=ghp_... go run ./cmd/devtrace-site/
```

## 6. Seed a Test Tenant + API Token

```bash
make seed
```

This creates a test tenant with the `free` plan, accepts ToS, mints an API token, and prints it:

```
Tenant ID:  a1b2c3d4-...
Username:   test-user
Plan:       free
API Token:  dt_8f3a...

Test with:
  curl -s -H 'Authorization: Bearer dt_8f3a...' http://localhost:8080/api/v1/score/octocat | jq .
```

**Note:** The raw token is only shown once. Save it. The database stores only the SHA-256 hash — the raw token cannot be recovered.

Running `make seed` again creates another tenant/token (idempotent on tenant, new token each time).

## 7. Test Authenticated Scoring

With the API token from step 6:
```bash
TOKEN="dt_your_token_here"

# Authenticated request — should get full signals, risk summary, categories
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/octocat | jq .

# Check quota headers
curl -s -D- -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/octocat 2>&1 | grep -i x-quota
# X-Quota-Limit: 50
# X-Quota-Remaining: 49
# X-Quota-Reset: ...
```

Compare authenticated vs unauthenticated:
```bash
# Unauth — minimal response (score + detail only)
curl -s http://localhost:8080/api/v1/score/octocat | jq 'keys'
# ["cached_at","detail","provider","score","scored_at","username"]

# Auth — full response (signals + risk summary, nil fields omitted by omitempty)
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/score/octocat | jq 'keys'
# ["provider","risk_summary","score","scored_at","signals","username"]
```

With repo context:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/v1/score/octocat?repo=octocat/Hello-World" | jq '.repo_context'
```

## 8. Test Token Management

Token management requires session auth (cookie-based). For local testing without the full OAuth flow, seed a session directly:

```sql
-- In psql (make db-connect):
-- Hash of "test-session-token":
INSERT INTO session (id, tenant_id, expires_at)
VALUES (
  -- echo -n "test-session-token" | shasum -a 256 | cut -d' ' -f1
  '4f5e3e1c74e0e0e5e8ef7c79a8f764a4d5c9c7e2b3d6e4c8a1f3d5e7b9c2d4f6',
  'a0000000-0000-0000-0000-000000000001',
  NOW() + INTERVAL '7 days'
);
```

Then test with the session cookie:
```bash
COOKIE="session=test-session-token"

# List tokens
curl -s -b "$COOKIE" http://localhost:8080/api/v1/token | jq .

# Create a new token
curl -s -b "$COOKIE" -X POST \
  -H "Content-Type: application/json" \
  -d '{"name":"my-ci-token"}' \
  http://localhost:8080/api/v1/token | jq .
# Returns: {"token":"dt_...", "name":"my-ci-token"}

# List again — should show the new token
curl -s -b "$COOKIE" http://localhost:8080/api/v1/token | jq .

# Revoke (replace TOKEN_ID with actual UUID from list)
curl -s -b "$COOKIE" -X DELETE \
  http://localhost:8080/api/v1/token/TOKEN_ID
# Returns: 204 No Content
```

## 9. Test Invalid Auth

```bash
# Bad API token — should get 401
curl -s -H "Authorization: Bearer dt_invalid_token" \
  http://localhost:8080/api/v1/score/octocat
# {"error":"invalid api token"}

# Bad session cookie — token endpoints redirect to OAuth
curl -s -b "session=invalid" -o /dev/null -w "%{http_code}" \
  http://localhost:8080/api/v1/token
# 302 (redirect to /auth/github)
```

## 10. Test Webhook (optional)

If you have a webhook secret configured:
```bash
SECRET="your-webhook-secret"
PAYLOAD='{"action":"created","installation":{"id":12345,"account":{"login":"test-org","type":"Organization"}},"sender":{"id":12345}}'
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$SECRET" | cut -d' ' -f2)

curl -s -X POST http://localhost:8080/webhook/github \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: installation" \
  -H "X-Hub-Signature-256: sha256=$SIG" \
  -d "$PAYLOAD"
```

Without webhook secret configured, the endpoint won't be registered (returns 404).

## 11. Run the Full Test Suite

```bash
# Unit tests (always work)
make test

# With local Postgres (runs integration tests too)
DATABASE_URL=postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable \
  go test ./... -v -race

# Lint
make lint

# Full qualify (test + lint + vulncheck)
make qualify
```

## Cleanup

```bash
# Stop Postgres
make db-down

# Remove test data (if Postgres still running)
make db-connect
# Then: DELETE FROM tenant WHERE username = 'test-user';

# Remove built binary
rm -f devtrace-site
```

## Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GITHUB_TOKEN` | Yes (dev) | — | PAT for GitHub API calls |
| `PORT` | No | `8080` | HTTP server port |
| `DATABASE_URL` | No | `postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable` | Postgres connection |
| `DEVTRACE_DEBUG` | No | `false` | Enable debug logging |
| `SCORE_CACHE_TTL_SEC` | No | `300` | Score cache TTL in seconds (5 min default) |
| `SCORE_RATE_LIMIT` | No | `60` | Requests per hour (unauth) |
| `OAUTH_RATE_LIMIT` | No | `20` | OAuth starts per minute per IP |
| `BASE_URL` | No | `http://localhost:8080` | Public URL (affects cookie security) |
| `GITHUB_OAUTH_CLIENT_ID` | No | — | OAuth App client ID |
| `GITHUB_OAUTH_CLIENT_SECRET` | No | — | OAuth App client secret |
| `GITHUB_APP_ID` | No | — | GitHub App ID (prod) |
| `GITHUB_APP_KEY_PATH` | No | — | Path to GitHub App private key PEM |
| `GITHUB_APP_INSTALLATION_ID` | No | — | Default installation ID (prod) |
| `GITHUB_WEBHOOK_SECRET` | No | — | Webhook HMAC secret |
| `SERVER_SHUTDOWN_TIMEOUT_SEC` | No | `5` | Graceful shutdown timeout |
| `DB_MAX_OPEN_CONNS` | No | `10` | Postgres pool max open |
| `DB_MAX_IDLE_CONNS` | No | `5` | Postgres pool max idle |
