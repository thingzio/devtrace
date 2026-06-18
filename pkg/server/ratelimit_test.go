package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestRateLimiter(t *testing.T) {
	rl := newIPRateLimiter(2, 1) // 2 requests per 1 second
	defer close(rl.stop)

	handler := rl.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests from same IP should succeed.
	for i := range 2 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// 3rd request from same IP should be rate limited.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 3: expected 429, got %d", rec.Code)
	}

	// Verify JSON body.
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body["error"] != "rate limit exceeded" {
		t.Errorf("expected 'rate limit exceeded', got %q", body["error"])
	}

	// Verify Content-Type.
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}

	// Verify Retry-After header is present.
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("expected Retry-After header on 429 response")
	}

	// Request from a different IP should succeed.
	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "5.6.7.8:5678"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("different IP: expected 200, got %d", rec.Code)
	}
}

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

	// Count persists across calls — user-a count is now 4, limit 5 allows it
	if !rl.allowWithLimit("user-a", 5) {
		t.Fatal("user-a with higher limit: count is 4, should be allowed under limit 5")
	}
	// But original limit still enforced
	if rl.allowWithLimit("user-a", 2) {
		t.Fatal("user-a back to limit 2: count is 5, should be denied")
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "X-Forwarded-For takes precedence",
			remoteAddr: "10.0.0.1:9999",
			xff:        "203.0.113.50, 70.41.3.18",
			want:       "203.0.113.50",
		},
		{
			name:       "single XFF value",
			remoteAddr: "10.0.0.1:9999",
			xff:        "203.0.113.50",
			want:       "203.0.113.50",
		},
		{
			name:       "falls back to RemoteAddr",
			remoteAddr: "192.168.1.1:4321",
			xff:        "",
			want:       "192.168.1.1",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "192.168.1.1",
			xff:        "",
			want:       "192.168.1.1",
		},
	}

	// Enable proxy trust for XFF tests.
	origTrust := trustProxy
	trustProxy = true
	defer func() { trustProxy = origTrust }()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := extractIP(req)
			if got != tc.want {
				t.Errorf("extractIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractIPNoTrustProxy(t *testing.T) {
	origTrust := trustProxy
	trustProxy = false
	defer func() { trustProxy = origTrust }()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	got := extractIP(req)
	if got != "10.0.0.1" {
		t.Errorf("without TRUST_PROXY, XFF should be ignored: got %q, want 10.0.0.1", got)
	}
}

func TestAuthAwareRateLimitUnauth(t *testing.T) {
	unauthRL := newIPRateLimiter(1, 60)
	defer close(unauthRL.stop)
	authRL := newIPRateLimiter(1000, 3600)
	defer close(authRL.stop)
	burstRL := newIPRateLimiter(100, 60)
	defer close(burstRL.stop)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("json mode blocks second unauth request", func(t *testing.T) {
		handler := authAwareRateLimit(unauthRL, authRL, burstRL, true, false, "test")(ok)

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
		handler := authAwareRateLimit(unauthRL2, authRL, burstRL, true, true, "test")(ok)

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
	burstRL := newIPRateLimiter(100, 60)
	defer close(burstRL.stop)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authAwareRateLimit(unauthRL, authRL, burstRL, true, false, "test")(ok)

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

// TestBurstLimitFor validates the per-plan burst derivation. The mapping
// must keep Free usable (≥5/min) while preventing a paid plan from
// concentrating its full hourly cap into a sub-minute burst.
func TestBurstLimitFor(t *testing.T) {
	tests := []struct {
		name    string
		perHour int
		want    int
	}{
		{"free_60", 60, 5},        // 60/12 = 5, floor exactly
		{"starter_300", 300, 25},  // 300/12 = 25
		{"pro_1000", 1000, 83},    // 1000/12 = 83
		{"huge_2000", 2000, 100},  // 2000/12 = 166, capped at 100
		{"tiny_1", 1, 5},          // 1/12 = 0, floor lifts to 5
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := burstLimitFor(tc.perHour); got != tc.want {
				t.Errorf("burstLimitFor(%d) = %d, want %d", tc.perHour, got, tc.want)
			}
		})
	}
}

// TestAuthAwareRateLimitBurst verifies the burst limiter denies a Free
// tenant after 5 requests within the same 60s window even though the
// hourly cap is 60. The 6th request must come back as 429 from the
// burst gate, not the hourly gate.
func TestAuthAwareRateLimitBurst(t *testing.T) {
	unauthRL := newIPRateLimiter(1, 60)
	defer close(unauthRL.stop)
	authRL := newIPRateLimiter(1000, 3600)
	defer close(authRL.stop)
	burstRL := newIPRateLimiter(100, 60)
	defer close(burstRL.stop)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authAwareRateLimit(unauthRL, authRL, burstRL, true, false, "test")(ok)

	tn := &tenant.Tenant{ID: "burst-tenant", Plan: "free"} // free = 60/hr → burst 5/min

	mkReq := func() *http.Request {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/score/octocat", nil)
		req.RemoteAddr = "10.0.0.99:1234"
		return req.WithContext(middleware.WithTenantContext(req.Context(), tn))
	}

	for i := range 5 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, mkReq())
		if rec.Code != http.StatusOK {
			t.Fatalf("burst request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// 6th request inside the same 60s window must be burst-denied.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, mkReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request: expected 429, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("expected Retry-After header on burst-denied response")
	}
}

// TestAuthAwareRateLimitBurstDisabled confirms that turning off the
// burst gate (burstEnabled=false) lets a tenant burn through their full
// hourly budget without being throttled per-minute. Acts as a kill
// switch for the env-var rollback path.
func TestAuthAwareRateLimitBurstDisabled(t *testing.T) {
	unauthRL := newIPRateLimiter(1, 60)
	defer close(unauthRL.stop)
	authRL := newIPRateLimiter(1000, 3600)
	defer close(authRL.stop)
	burstRL := newIPRateLimiter(100, 60)
	defer close(burstRL.stop)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authAwareRateLimit(unauthRL, authRL, burstRL, false, false, "test")(ok)

	tn := &tenant.Tenant{ID: "burst-off-tenant", Plan: "free"}

	mkReq := func() *http.Request {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/score/octocat", nil)
		req.RemoteAddr = "10.0.0.100:1234"
		return req.WithContext(middleware.WithTenantContext(req.Context(), tn))
	}

	// 10 requests in a burst — all should succeed because burst is off
	// and the hourly cap is 60.
	for i := range 10 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, mkReq())
		if rec.Code != http.StatusOK {
			t.Fatalf("burst-off request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
}
