package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getTestServer(t *testing.T, handler http.Handler) *http.Response {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	return resp
}

func TestSecurityHeaders(t *testing.T) {
	t.Setenv("BASE_URL", "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := getTestServer(t, securityHeaders(inner))
	defer resp.Body.Close()

	exact := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for h, want := range exact {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}

	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "default-src 'self'") {
		t.Errorf("CSP should start with \"default-src 'self'\", got %q", csp)
	}

	if pp := resp.Header.Get("Permissions-Policy"); pp == "" {
		t.Error("Permissions-Policy header missing")
	}
}

func TestSecurityHeadersNoHSTSWithoutHTTPS(t *testing.T) {
	t.Setenv("BASE_URL", "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := getTestServer(t, securityHeaders(inner))
	defer resp.Body.Close()

	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set without HTTPS, got %q", hsts)
	}
}

func TestSecurityHeadersNoHSTSWithHTTP(t *testing.T) {
	t.Setenv("BASE_URL", "http://localhost:8080")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := getTestServer(t, securityHeaders(inner))
	defer resp.Body.Close()

	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set for http BASE_URL, got %q", hsts)
	}
}
