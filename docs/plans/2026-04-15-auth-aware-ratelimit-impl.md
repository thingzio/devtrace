# Auth-Aware Rate Limiting Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Tighten unauthenticated rate limits to 1 req/min per IP, enforce plan-based limits for authenticated users, and show a clear sign-in CTA when rate-limited.

**Architecture:** New `authAwareRateLimit` middleware wraps existing `ipRateLimiter` with auth context awareness. Two limiter instances (unauth + auth) replace the single `scoreRL`. A new `allowWithLimit` method enables dynamic per-plan thresholds. UI routes render an HTML error page; API routes return JSON 429.

**Tech Stack:** Go stdlib `net/http`, existing `ipRateLimiter`, `html/template`, existing `middleware.TenantFromContext`.

---

### Task 1: Add `allowWithLimit` method to `ipRateLimiter`

**Files:**
- Modify: `pkg/server/ratelimit.go:62-78`
- Test: `pkg/server/ratelimit_test.go`

**Step 1: Write the failing test**

Add to `pkg/server/ratelimit_test.go`:

```go
func TestAllowWithLimit(t *testing.T) {
	rl := newIPRateLimiter(100, 1) // high default, irrelevant for allowWithLimit
	defer close(rl.stop)

	// Dynamic limit of 2 for key "user-a"
	if !rl.allowWithLimit("user-a", 2) {
		t.Fatal("request 1: should be allowed")
	}
	if !rl.allowWithLimit("user-a", 2) {
		t.Fatal("request 2: should be allowed")
	}
	if rl.allowWithLimit("user-a", 2) {
		t.Fatal("request 3: should be denied")
	}

	// Different key should be independent
	if !rl.allowWithLimit("user-b", 1) {
		t.Fatal("user-b request 1: should be allowed")
	}
	if rl.allowWithLimit("user-b", 1) {
		t.Fatal("user-b request 2: should be denied")
	}

	// Higher limit for same key still denied (window hasn't reset)
	if rl.allowWithLimit("user-a", 5) {
		t.Fatal("user-a with higher limit: count is 3, should still track")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/server/ -run TestAllowWithLimit -v`
Expected: FAIL — `allowWithLimit` undefined

**Step 3: Write minimal implementation**

Add to `pkg/server/ratelimit.go` after `allow()` method (after line 78):

```go
// allowWithLimit works like allow but uses the given limit instead of rl.limit.
// This lets callers apply dynamic per-key thresholds (e.g. plan-based limits).
func (rl *ipRateLimiter) allowWithLimit(key string, limit int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, ok := rl.visitors[key]
	if !ok || now.After(v.resetAt) {
		rl.visitors[key] = &visitor{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	v.count++
	return v.count <= limit
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/server/ -run TestAllowWithLimit -v`
Expected: PASS

**Step 5: Run all existing rate limiter tests**

Run: `go test ./pkg/server/ -run TestRateLimiter -v`
Expected: PASS (no regressions)

**Step 6: Commit**

```bash
git add pkg/server/ratelimit.go pkg/server/ratelimit_test.go
git commit -S -m "Add allowWithLimit method for dynamic per-key rate thresholds"
```

---

### Task 2: Create rate limit HTML template

**Files:**
- Create: `pkg/server/templates/ratelimit.html`
- Modify: `pkg/server/server.go:56` (register template in `init()`)

**Step 1: Create the template**

Create `pkg/server/templates/ratelimit.html`:

```html
{{define "content"}}
<div class="landing">
  <section class="hero">
    <h1>Rate limit reached</h1>
    <p class="hero-sub">Free lookups are limited to 1 per minute. Sign in with GitHub for higher limits and full access to all features.</p>
    <div class="hero-cta">
      <a href="/auth/github" class="btn btn-primary">Sign in with GitHub</a>
      <p class="muted" style="margin-top:0.5rem;font-size:0.85rem;">
        <a href="/" style="color:inherit;">Back to home</a>
      </p>
    </div>
  </section>
</div>
{{end}}
```

**Step 2: Register the template in `init()`**

In `pkg/server/server.go`, add `"ratelimit.html"` to the `simplePages` slice on line 56:

Change:
```go
simplePages := []string{"landing.html", "scorecard.html", "tos.html", "settings.html", "stub.html", "help.html"}
```
To:
```go
simplePages := []string{"landing.html", "scorecard.html", "tos.html", "settings.html", "stub.html", "help.html", "ratelimit.html"}
```

**Step 3: Verify it compiles**

Run: `go build ./pkg/server/`
Expected: success

**Step 4: Commit**

```bash
git add pkg/server/templates/ratelimit.html pkg/server/server.go
git commit -S -m "Add rate limit HTML template with sign-in CTA"
```

---

### Task 3: Implement `authAwareRateLimit` middleware

**Files:**
- Modify: `pkg/server/ratelimit.go` (add new middleware function)
- Test: `pkg/server/ratelimit_test.go` (add middleware tests)

**Step 1: Write the failing tests**

Add to `pkg/server/ratelimit_test.go`:

```go
func TestAuthAwareRateLimitUnauth(t *testing.T) {
	unauthRL := newIPRateLimiter(1, 60)
	defer close(unauthRL.stop)
	authRL := newIPRateLimiter(1000, 3600)
	defer close(authRL.stop)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("json mode blocks second unauth request", func(t *testing.T) {
		handler := authAwareRateLimit(unauthRL, authRL, false)(ok)

		// First request — allowed
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/score/octocat", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request 1: expected 200, got %d", rec.Code)
		}

		// Second request — blocked
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("request 2: expected 429, got %d", rec.Code)
		}

		// Verify JSON response
		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["error"] != "rate limit exceeded" {
			t.Errorf("unexpected error: %v", body["error"])
		}
		if body["sign_in_url"] != "/auth/github" {
			t.Errorf("expected sign_in_url, got: %v", body["sign_in_url"])
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Error("expected Retry-After header")
		}
	})

	t.Run("html mode renders template", func(t *testing.T) {
		// Use a fresh limiter so previous test state doesn't interfere
		unauthRL2 := newIPRateLimiter(1, 60)
		defer close(unauthRL2.stop)
		handler := authAwareRateLimit(unauthRL2, authRL, true)(ok)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/score/octocat", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req) // first — allowed

		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req) // second — blocked
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429, got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("expected text/html, got %q", ct)
		}
		if !strings.Contains(rec.Body.String(), "Rate limit reached") {
			t.Error("expected rate limit page content")
		}
	})
}

func TestAuthAwareRateLimitAuth(t *testing.T) {
	unauthRL := newIPRateLimiter(1, 60)
	defer close(unauthRL.stop)
	authRL := newIPRateLimiter(1000, 3600)
	defer close(authRL.stop)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authAwareRateLimit(unauthRL, authRL, false)(ok)

	tn := &tenant.Tenant{ID: "test-tenant-id", Plan: "free"} // free = 60/hr

	// Authenticated user should bypass unauth limit (>1 req allowed)
	for i := range 3 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/score/octocat", nil)
		req.RemoteAddr = "10.0.0.3:1234"
		ctx := middleware.WithTenantContext(req.Context(), tn)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("auth request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pkg/server/ -run TestAuthAwareRateLimit -v`
Expected: FAIL — `authAwareRateLimit` undefined

**Step 3: Write the middleware implementation**

Add to `pkg/server/ratelimit.go`, add these imports at the top: `"github.com/thingzio/devtrace/pkg/middleware"` and `"github.com/thingzio/devtrace/pkg/plan"`. Then add the function:

```go
// authAwareRateLimit returns middleware that applies different rate limits based
// on authentication status. Unauthenticated requests are limited per-IP using
// unauthRL. Authenticated requests are limited per-tenant using authRL with
// the tenant's plan-based RateLimitPerHour.
//
// When htmlMode is true, 429 responses render the ratelimit.html template.
// When false, 429 responses return JSON with Retry-After header.
func authAwareRateLimit(unauthRL, authRL *ipRateLimiter, htmlMode bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tn := middleware.TenantFromContext(r.Context())

			var allowed bool
			var key string

			if tn == nil {
				// Unauthenticated: limit per IP
				key = extractIP(r)
				allowed = unauthRL.allow(key)
			} else {
				// Authenticated: limit per tenant using plan rate
				key = tn.ID
				p, ok := plan.Get(tn.Plan)
				if !ok {
					p = plan.Free()
				}
				allowed = authRL.allowWithLimit(key, p.RateLimitPerHour)
			}

			if !allowed {
				var retryAfter int
				if tn == nil {
					retryAfter = unauthRL.retryAfter(key)
				} else {
					retryAfter = authRL.retryAfter(key)
				}

				if htmlMode {
					w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
					w.WriteHeader(http.StatusTooManyRequests)
					renderTemplate(w, "ratelimit.html", pageData{Title: "Rate Limit"})
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":       "rate limit exceeded",
					"retry_after": retryAfter,
					"sign_in_url": "/auth/github",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
```

**Step 4: Add required imports to test file**

Add to `pkg/server/ratelimit_test.go` imports:

```go
"strings"

"github.com/thingzio/devtrace/pkg/middleware"
"github.com/thingzio/devtrace/pkg/tenant"
```

**Step 5: Run tests to verify they pass**

Run: `go test ./pkg/server/ -run TestAuthAwareRateLimit -v`
Expected: PASS

**Step 6: Run all rate limiter tests**

Run: `go test ./pkg/server/ -run "TestRateLimiter|TestAllowWithLimit|TestAuthAwareRateLimit|TestExtractIP" -v`
Expected: all PASS

**Step 7: Commit**

```bash
git add pkg/server/ratelimit.go pkg/server/ratelimit_test.go
git commit -S -m "Add auth-aware rate limit middleware with plan-based thresholds"
```

---

### Task 4: Wire new middleware into routes

**Files:**
- Modify: `pkg/server/server.go:230-297` (`makeRouter` function)

**Step 1: Replace `scoreRL` with new limiters**

In `pkg/server/server.go`, replace lines 235-238:

```go
scoreRL := newIPRateLimiter(
	config.GetEnvAsInt("SCORE_RATE_LIMIT", 60),
	3600, // 1 hour window
)
```

With:

```go
unauthRL := newIPRateLimiter(
	config.GetEnvAsInt("UNAUTH_RATE_LIMIT", 1),
	config.GetEnvAsInt("UNAUTH_RATE_WINDOW", 60),
)
authRL := newIPRateLimiter(1000, 3600) // ceiling; actual limit per plan via allowWithLimit
```

**Step 2: Update route registrations**

Replace lines 270-276:

```go
// Score card page — accepts any auth
mux.Handle("GET /score/{username}", scoreRL.wrap(requireAny(scorecardHandler(store, scoreSvc, opts))))

// Score API — accepts any auth (token, session, or none)
mux.Handle("GET /api/v1/score/{username}", scoreRL.wrap(requireAny(scoreHandler(db, store, scoreSvc))))

// Score history API (trend chart data)
mux.Handle("GET /api/v1/score/{username}/history", scoreRL.wrap(requireAny(historyHandler(store))))
```

With:

```go
// Score card page — accepts any auth, rate-limited (HTML 429)
mux.Handle("GET /score/{username}", requireAny(authAwareRateLimit(unauthRL, authRL, true)(scorecardHandler(store, scoreSvc, opts))))

// Score API — accepts any auth, rate-limited (JSON 429)
mux.Handle("GET /api/v1/score/{username}", requireAny(authAwareRateLimit(unauthRL, authRL, false)(scoreHandler(db, store, scoreSvc))))

// Score history API (trend chart data)
mux.Handle("GET /api/v1/score/{username}/history", requireAny(authAwareRateLimit(unauthRL, authRL, false)(historyHandler(store))))
```

**Step 3: Update cleanup function**

Replace lines 292-295:

```go
cleanup := func() {
	scoreRL.close()
	oauthRL.close()
}
```

With:

```go
cleanup := func() {
	unauthRL.close()
	authRL.close()
	oauthRL.close()
}
```

**Step 4: Verify it compiles**

Run: `go build ./...`
Expected: success (no compilation errors)

**Step 5: Run full test suite**

Run: `go test ./...`
Expected: all PASS

**Step 6: Commit**

```bash
git add pkg/server/server.go
git commit -S -m "Wire auth-aware rate limiting into score routes"
```

---

### Task 5: Final validation

**Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: all PASS

**Step 2: Run linter if configured**

Run: `golangci-lint run ./...`
Expected: no new issues

**Step 3: Verify the application starts**

Run: `go build -o /dev/null ./cmd/devtrace/` (or whatever the entry point is)
Expected: builds successfully

**Step 4: Commit (if any lint fixes needed)**

Only if Step 2 required changes.
