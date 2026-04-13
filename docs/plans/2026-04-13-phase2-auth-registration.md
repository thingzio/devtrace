# Phase 2: Auth + Registration — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add GitHub OAuth sign-up, GitHub App installation, session management, API token minting, and dual auth paths (session for UI, API token for programmatic access) — so tenants can register and use the scoring API with their own tokens.

**Architecture:** Port DevPulse's proven OAuth/session/tenant patterns. Add API token auth path for CI/CD and CLI. DevTrace has its own GitHub App (distinct from DevPulse's) with its own App ID, private key, webhook secret, and API rate limit quota — ensuring the two services never compete for GitHub API capacity. GitHub App installations provide server-side tokens for GitHub API calls. Webhook handler processes installation events. PAT client swapped for installation-token client behind the same `github.Client` interface.

**Tech Stack:** Go 1.26, `crypto/sha256` (token hashing), `crypto/rsa` + `golang-jwt/jwt/v5` (GitHub App JWT), `crypto/hmac` (webhook verification), `crypto/subtle` (constant-time state comparison). HTTP client from `net/http` stdlib for OAuth token exchange and GitHub API calls.

---

## Task 1: HTTP Client Utilities

Shared HTTP client for OAuth and GitHub App API calls. DevPulse uses `pkg/net/client.go`.

**Files:**
- Create: `pkg/net/client.go`
- Create: `pkg/net/client_test.go`

**Step 1: Write test**

```go
// pkg/net/client_test.go
package net

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubClientDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, nil, "/other", http.StatusFound)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := GitHubClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302, got %d", resp.StatusCode)
	}
}
```

**Step 2: Implement**

```go
// pkg/net/client.go
package net

import (
	"net/http"
	"time"
)

// GitHubClient is a shared HTTP client for GitHub API and OAuth calls.
// Does not follow redirects (OAuth token exchange needs raw response).
var GitHubClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}
```

**Step 3: Run tests, commit**

```bash
go test ./pkg/net/ -v -race
git add pkg/net/
git commit -S -m "Add shared HTTP client for OAuth and GitHub API calls"
```

---

## Task 2: OAuth Package

Port `pkg/oauth/github.go` from DevPulse.

**Files:**
- Create: `pkg/oauth/github.go`
- Create: `pkg/oauth/github_test.go`

**Step 1: Write tests**

Test `BuildAuthURL` (returns URL with correct params + random state), `ExchangeCode` (with httptest mock), `FetchUser` (with httptest mock including email fallback).

```go
// pkg/oauth/github_test.go
package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildAuthURL(t *testing.T) {
	cfg := &Config{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost/callback",
	}
	url, state, err := BuildAuthURL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Error("state should not be empty")
	}
	if len(state) != 32 { // 16 bytes hex-encoded
		t.Errorf("state length: got %d, want 32", len(state))
	}
	if !strings.Contains(url, "client_id=test-client-id") {
		t.Errorf("URL missing client_id: %s", url)
	}
	if !strings.Contains(url, "state="+state) {
		t.Errorf("URL missing state: %s", url)
	}
}

func TestExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
	}))
	defer srv.Close()

	cfg := &Config{TokenURL: srv.URL}
	token, err := ExchangeCode(context.Background(), cfg, "test-code")
	if err != nil {
		t.Fatal(err)
	}
	if token != "test-token" {
		t.Errorf("got %q, want test-token", token)
	}
}

func TestFetchUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			json.NewEncoder(w).Encode(map[string]any{
				"id": 123, "login": "testuser", "email": "test@example.com",
			})
		}
	}))
	defer srv.Close()

	cfg := &Config{UserURL: srv.URL + "/user"}
	user, err := FetchUser(context.Background(), cfg, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if user.Login != "testuser" || user.ID != 123 {
		t.Errorf("unexpected user: %+v", user)
	}
}
```

**Step 2: Implement**

Port `pkg/oauth/github.go` from DevPulse line-for-line. Change import from `github.com/thingzio/devpulse/pkg/net` to `github.com/thingzio/devtrace/pkg/net`.

Key functions: `BuildAuthURL`, `ExchangeCode`, `FetchUser`, `fetchPrimaryEmail`, `randomState`.

Config struct with testable URL overrides:
```go
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AuthURL      string // override for testing
	TokenURL     string // override for testing
	UserURL      string // override for testing
	EmailsURL    string // override for testing
}
```

**Step 3: Run tests, commit**

```bash
go test ./pkg/oauth/ -v -race
git add pkg/oauth/
git commit -S -m "Add GitHub OAuth package (BuildAuthURL, ExchangeCode, FetchUser)"
```

---

## Task 3: Tenant Management

Tenant CRUD operations against the `tenant` table created in Phase 1.

**Files:**
- Create: `pkg/tenant/tenant.go`
- Create: `pkg/tenant/tenant_test.go`

**Step 1: Define Tenant struct and SQL**

```go
// pkg/tenant/tenant.go
package tenant

type Tenant struct {
	ID               string
	GitHubID         int64
	Username         string
	Email            string
	AvatarURL        string
	Name             string
	Company          string
	Location         string
	Bio              string
	Plan             string
	MaxContributors  int
	ToSAcceptedAt    *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
```

Key functions:
- `UpsertTenant(ctx, db, githubID, username, email, avatarURL, name, company, location, bio) (*Tenant, error)` — INSERT ON CONFLICT (github_id) DO UPDATE
- `GetTenantByID(ctx, db, id) (*Tenant, error)`
- `GetTenantByGitHubID(ctx, db, githubID) (*Tenant, error)`
- `AcceptToS(ctx, db, tenantID) error`
- `scanTenant(row) (*Tenant, error)` — shared row scanner

The SQL uses `INSERT ... ON CONFLICT (github_id) DO UPDATE SET username=$2, email=$3, ... RETURNING *`.

**Step 2: Write integration test** (skips without DB, same pattern as migrate_test.go)

Test UpsertTenant (create + idempotent update), GetTenantByID, AcceptToS.

**Step 3: Commit**

```bash
git add pkg/tenant/
git commit -S -m "Add tenant management (UpsertTenant, GetTenant, AcceptToS)"
```

---

## Task 4: Session Management

Port `pkg/tenant/session.go` from DevPulse.

**Files:**
- Create: `pkg/tenant/session.go`
- Create: `pkg/tenant/session_test.go`

**Step 1: Implement session functions**

- `CreateSession(ctx, db, tenantID, ttl) (rawToken string, error)` — generates 256-bit random token, stores SHA-256 hash
- `ValidateSession(ctx, db, rawToken) (*Tenant, error)` — hashes token, joins session + tenant, checks expiration
- `DestroySession(ctx, db, rawToken) error` — deletes by hash
- `HashToken(raw string) string` — SHA-256 hex encoding
- `ErrSessionInvalid` sentinel error

**Step 2: Write integration test**

Test full lifecycle: create → validate → destroy → validate returns ErrSessionInvalid.

**Step 3: Commit**

```bash
git add pkg/tenant/session.go pkg/tenant/session_test.go
git commit -S -m "Add session management (create, validate, destroy)"
```

---

## Task 5: API Token Management

DevTrace-specific — API tokens for programmatic access (CI/CD, CLI). No DevPulse equivalent.

**Files:**
- Create: `pkg/tenant/apitoken.go`
- Create: `pkg/tenant/apitoken_test.go`

**Step 1: Implement API token functions**

```go
// CreateAPIToken generates a new API token for the tenant.
// Returns the raw token (shown once to user) and the stored record.
func CreateAPIToken(ctx context.Context, db *sql.DB, tenantID, name string) (rawToken string, err error)

// ValidateAPIToken checks an API token and returns the associated tenant.
func ValidateAPIToken(ctx context.Context, db *sql.DB, rawToken string) (*Tenant, error)

// ListAPITokens returns all tokens for a tenant (hashes truncated for display).
func ListAPITokens(ctx context.Context, db *sql.DB, tenantID string) ([]APITokenInfo, error)

// RevokeAPIToken deletes a token by ID (must belong to tenant).
func RevokeAPIToken(ctx context.Context, db *sql.DB, tenantID, tokenID string) error

type APITokenInfo struct {
	ID        string
	Name      string
	Prefix    string // first 8 chars of hash for display
	LastUsed  *time.Time
	CreatedAt time.Time
}
```

Token format: `dt_` prefix + 32 random hex bytes = `dt_<64 hex chars>`. Stored as SHA-256 hash. `dt_` prefix makes tokens easily identifiable and greppable.

ValidateAPIToken also updates `last_used_at` on hit.

**Step 2: Write integration test**

Create token, validate it, list tokens, revoke, validate returns error.

**Step 3: Commit**

```bash
git add pkg/tenant/apitoken.go pkg/tenant/apitoken_test.go
git commit -S -m "Add API token management (create, validate, list, revoke)"
```

---

## Task 6: Auth Middleware

Dual auth: session cookies for UI, API tokens for programmatic access.

**Files:**
- Create: `pkg/middleware/auth.go`
- Create: `pkg/middleware/auth_test.go`

**Step 1: Implement middleware**

```go
// RequireAuth checks session cookie, redirects to loginURL on failure.
// For UI routes.
func RequireAuth(db *sql.DB, loginURL string) func(http.Handler) http.Handler

// RequireAPIToken checks Authorization header (Bearer dt_...), returns 401 on failure.
// For API routes. Injects tenant into context.
func RequireAPIToken(db *sql.DB) func(http.Handler) http.Handler

// RequireAnyAuth checks API token first, then session cookie.
// For endpoints accessible from both UI and API.
func RequireAnyAuth(db *sql.DB, loginURL string) func(http.Handler) http.Handler

// TenantFromContext extracts tenant from request context.
func TenantFromContext(ctx context.Context) *Tenant

// SetSessionCookie / ClearSessionCookie — cookie management.
// Cookie name: __Host-session (HTTPS) or session (HTTP dev).
func SetSessionCookie(w http.ResponseWriter, token string, maxAge int)
func ClearSessionCookie(w http.ResponseWriter)
func SessionCookieName() string
```

RequireAPIToken extracts token from `Authorization: Bearer dt_...` header, calls `tenant.ValidateAPIToken`.

**Step 2: Write unit tests**

- TestRequireAuth: valid session → handler called with tenant in context
- TestRequireAuth: no cookie → redirect to login
- TestRequireAPIToken: valid token → handler called
- TestRequireAPIToken: missing/invalid → 401
- TestTenantFromContext: round-trip inject + extract

Use httptest + mock DB or test helpers.

**Step 3: Commit**

```bash
git add pkg/middleware/
git commit -S -m "Add auth middleware (session, API token, dual auth)"
```

---

## Task 7: OAuth Handlers + Wire Auth into Server

Add OAuth routes and protect existing endpoints.

**Files:**
- Modify: `pkg/server/server.go` — add OAuth routes, auth middleware
- Create: `pkg/server/handler_auth.go` — OAuth start, callback, signout
- Create: `pkg/server/handler_auth_test.go`
- Create: `pkg/server/handler_token.go` — API token CRUD endpoints
- Create: `pkg/server/handler_token_test.go`

**Step 1: Create OAuth handlers**

Port from DevPulse:
- `oauthStartHandler(cfg *oauth.Config)` — builds auth URL, sets state cookie, redirects
- `oauthCallbackHandler(db *sql.DB, cfg *oauth.Config)` — validates state, exchanges code, upserts tenant, creates session, sets cookie, redirects to dashboard (or ToS if not accepted)
- `signoutHandler(db *sql.DB)` — destroys session, clears cookie, redirects to /

Constants: `sessionTTL = 7 * 24 * time.Hour`

**Step 2: Create API token handlers**

```go
// POST /api/v1/token — mint new token (requires session auth)
func createTokenHandler(db *sql.DB) http.HandlerFunc

// GET /api/v1/token — list tokens (requires session auth)
func listTokensHandler(db *sql.DB) http.HandlerFunc

// DELETE /api/v1/token/{id} — revoke token (requires session auth)
func revokeTokenHandler(db *sql.DB) http.HandlerFunc
```

**Step 3: Update makeRouter**

```go
func makeRouter(...) *http.ServeMux {
	// Public routes
	mux.HandleFunc("GET /health", health.Handler())
	mux.Handle("GET /auth/github", oauthRL.wrap(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))

	// Auth middleware
	requireSession := middleware.RequireAuth(db, "/auth/github")
	requireToken := middleware.RequireAPIToken(db)
	requireAny := middleware.RequireAnyAuth(db, "/auth/github")

	// API routes (API token auth)
	mux.Handle("GET /api/v1/score/{username}", scoreRL.wrap(requireAny(scoreHandler(scoreSvc))))

	// Token management (session auth only — UI)
	mux.Handle("POST /api/v1/token", requireSession(createTokenHandler(db)))
	mux.Handle("GET /api/v1/token", requireSession(listTokensHandler(db)))
	mux.Handle("DELETE /api/v1/token/{id}", requireSession(revokeTokenHandler(db)))

	// Session routes
	mux.Handle("POST /auth/signout", requireSession(signoutHandler(db)))
}
```

**Step 4: Update scoreHandler for plan-aware auth**

The score handler currently hardcodes `plan = "free"`. Update to:
1. Check `middleware.TenantFromContext(r.Context())`
2. If tenant present: use `tenant.Plan`
3. If nil (unauthenticated): use `""` (unauth response)

**Step 5: Load OAuth config from env in Run()**

```go
oauthCfg := &oauth.Config{
	ClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
	ClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
	RedirectURL:  config.GetEnv("BASE_URL", "http://localhost:8080") + "/auth/github/callback",
}
```

GITHUB_TOKEN remains required for the PAT GitHub client (dev mode). OAuth client ID/secret are for DevTrace's own GitHub OAuth App (distinct from DevPulse's). If empty, OAuth routes return 503 — fine for Phase 2 dev where you test API-only.

**DevTrace GitHub App setup** (one-time, before production):
- Create a new GitHub App under the thingz org (separate from DevPulse's app)
- Own App ID, private key, webhook secret, OAuth client ID/secret
- Own API rate limit quota (5,000 req/hour per installation, not shared with DevPulse)
- Env vars: `GITHUB_APP_ID`, `GITHUB_APP_KEY_PATH`, `GITHUB_WEBHOOK_SECRET`, `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`

**Step 6: Write tests, commit**

Test OAuth start handler (redirects to GitHub), callback handler (with mocked token exchange), signout, token CRUD.

```bash
git add pkg/server/
git commit -S -m "Add OAuth handlers, API token endpoints, wire auth into router"
```

---

## Task 8: GitHub App Installation Handling

Webhook handler for installation events. GitHub App JWT and installation token minting.

**Files:**
- Create: `pkg/tenant/githubapp.go`
- Create: `pkg/tenant/githubapp_test.go`
- Create: `pkg/tenant/installation.go`
- Create: `pkg/tenant/installation_test.go`
- Create: `pkg/server/handler_webhook.go`
- Create: `pkg/server/handler_webhook_test.go`

**Step 1: GitHub App config and JWT**

Port from DevPulse `pkg/tenant/githubapp.go`:

```go
type GitHubAppConfig struct {
	AppID      int64
	PrivateKey *rsa.PrivateKey
	BaseURL    string
}

func LoadGitHubAppConfig() (*GitHubAppConfig, error)
// Reads GITHUB_APP_ID and GITHUB_APP_KEY_PATH from env.

func CreateAppJWT(cfg *GitHubAppConfig) (string, error)
// RS256 JWT with iss=AppID, iat=now-60s, exp=now+10m.

func MintInstallationToken(ctx context.Context, cfg *GitHubAppConfig, installationID int64) (*InstallationToken, error)
// Creates App JWT, POSTs to /app/installations/{id}/access_tokens.
```

Dependency: `go get github.com/golang-jwt/jwt/v5`

**Step 2: Installation storage**

```go
// pkg/tenant/installation.go
func SaveInstallation(ctx, db, tenantID, installationID, targetType, targetLogin string) error
func ListInstallations(ctx, db, tenantID string) ([]Installation, error)
func SuspendInstallation(ctx, db, installationID int64) error
func GetActiveInstallations(ctx, db, tenantID string) ([]ActiveInstallation, error)
```

**Step 3: Webhook handler**

```go
// pkg/server/handler_webhook.go
func webhookHandler(db *sql.DB, secret string) http.HandlerFunc
```

1. Verify `X-Hub-Signature-256` with HMAC-SHA256
2. Dispatch by `X-GitHub-Event` header:
   - `installation` → handleInstallationEvent (save or suspend)
   - Ignore others for now

**Step 4: Wire webhook into router**

```go
webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
if webhookSecret != "" {
	mux.HandleFunc("POST /webhook/github", webhookHandler(db, webhookSecret))
}
```

**Step 5: Write tests, commit**

- Test JWT creation (valid signature, correct claims)
- Test webhook signature verification (valid + invalid)
- Test installation save/list/suspend

```bash
go get github.com/golang-jwt/jwt/v5
git add pkg/tenant/githubapp.go pkg/tenant/githubapp_test.go pkg/tenant/installation.go pkg/tenant/installation_test.go pkg/server/handler_webhook.go pkg/server/handler_webhook_test.go go.mod go.sum
git commit -S -m "Add GitHub App JWT, installation management, webhook handler"
```

---

## Task 9: Installation-Token GitHub Client

Swap from PAT to GitHub App installation tokens for production.

**Files:**
- Create: `pkg/github/installation.go`
- Create: `pkg/github/installation_test.go`
- Modify: `pkg/server/server.go` — select client based on config

**Step 1: Implement InstallationClient**

```go
// pkg/github/installation.go
type InstallationClient struct {
	appCfg         *tenant.GitHubAppConfig
	installationID int64
	mu             sync.Mutex
	token          string
	expiresAt      time.Time
}

func NewInstallationClient(cfg *tenant.GitHubAppConfig, installationID int64) *InstallationClient

func (c *InstallationClient) FetchUser(ctx, username) (*UserProfile, error)
func (c *InstallationClient) FetchSignals(ctx, username, repo) (*score.InputSignals, error)
```

The client lazily mints a token on first use and caches it until 5 minutes before expiry. Uses the same go-github API calls as PATClient but with the installation token.

To avoid duplication, extract shared fetching logic into internal helper functions that accept an `*github.Client` parameter. Both PATClient and InstallationClient create their own `*github.Client` and delegate to shared helpers.

**Step 2: Update server.go to select client**

```go
// In Run():
var gh ghclient.Client
if appCfg, err := tenant.LoadGitHubAppConfig(); err == nil {
	// Production: use installation token
	// For now, use first active installation. Phase 3 will add per-tenant routing.
	instID := config.GetEnvAsInt("GITHUB_APP_INSTALLATION_ID", 0)
	if instID > 0 {
		gh = ghclient.NewInstallationClient(appCfg, int64(instID))
	}
}
if gh == nil {
	// Dev fallback: PAT
	token := config.GetEnv("GITHUB_TOKEN", "")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN or GITHUB_APP_ID+KEY required")
	}
	gh = ghclient.NewPATClient(token)
}
```

**Step 3: Write tests, commit**

Test that InstallationClient implements Client interface. Test token caching (mock the mint function).

```bash
git add pkg/github/ pkg/server/server.go
git commit -S -m "Add installation-token GitHub client with auto-refresh"
```

---

## Task 10: Quota Enforcement

Track and enforce contributor scoring quotas per tenant plan.

**Files:**
- Create: `pkg/tenant/usage.go`
- Create: `pkg/tenant/usage_test.go`
- Modify: `pkg/service/score.go` — check quota before scoring
- Modify: `pkg/server/handler_score.go` — add rate limit headers

**Step 1: Implement usage tracking**

```go
// pkg/tenant/usage.go
func RecordUsage(ctx, db, tenantID, username, provider string, deep bool) error
func GetUsageCount(ctx, db, tenantID string, since time.Time) (int, error)
func GetQuotaRemaining(ctx, db, tenantID string, maxContributors int) (remaining int, err error)
```

Uses the `usage_record` table from Phase 1 schema.

**Step 2: Update score handler to add headers**

```go
// After scoring:
w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", plan.RateLimitPerHour))
w.Header().Set("X-RateLimit-Remaining", ...)
w.Header().Set("X-Quota-Limit", fmt.Sprintf("%d", plan.MaxContributors))
w.Header().Set("X-Quota-Remaining", fmt.Sprintf("%d", remaining))
```

Return 403 with quota details when contributor cap reached.

**Step 3: Write tests, commit**

```bash
git add pkg/tenant/usage.go pkg/tenant/usage_test.go pkg/service/ pkg/server/
git commit -S -m "Add quota enforcement and rate limit headers"
```

---

## Task 11: End-to-End Verification

**Step 1: Run full test suite**

```bash
go test ./... -v -race
```

**Step 2: Run linter**

```bash
golangci-lint -c .golangci.yaml run --timeout=5m
```

**Step 3: Manual flow test**

1. Start local Postgres: `make db-up`
2. Start server with PAT: `GITHUB_TOKEN=$GITHUB_TOKEN go run ./cmd/devtrace-site/`
3. Test unauthenticated score: `curl http://localhost:8080/api/v1/score/octocat`
4. Seed a test tenant + API token in the DB
5. Test authenticated score: `curl -H "Authorization: Bearer dt_..." http://localhost:8080/api/v1/score/octocat`
6. Verify response includes full signals, risk summary, rate limit headers

**Step 4: Commit any fixes**

```bash
git add -A
git commit -S -m "Fix issues from Phase 2 end-to-end verification"
```

---

## Phase 2 Complete Checklist

- [ ] OAuth package: BuildAuthURL, ExchangeCode, FetchUser
- [ ] Tenant CRUD: UpsertTenant, GetTenantByID, AcceptToS
- [ ] Session management: Create, Validate, Destroy with SHA-256 hashing
- [ ] API token management: Create (dt_ prefix), Validate, List, Revoke
- [ ] Auth middleware: session (UI), API token (programmatic), dual auth
- [ ] OAuth handlers: start, callback, signout
- [ ] Token endpoints: POST/GET/DELETE /api/v1/token
- [ ] Score endpoint: plan-aware based on authenticated tenant
- [ ] GitHub App: JWT creation, installation token minting
- [ ] Installation storage: save, list, suspend
- [ ] Webhook handler: signature verification, installation events
- [ ] Installation-token GitHub client with auto-refresh
- [ ] Quota enforcement with rate limit/quota headers
- [ ] All tests pass, lint clean

---

## What's Next (Phase 3)

Phase 3: UI — landing page, score card, dashboard, trend charts. Server-rendered HTML with embedded templates, same pattern as DevPulse.
