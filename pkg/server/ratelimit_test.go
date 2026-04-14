package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
