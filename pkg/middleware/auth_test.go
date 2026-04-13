package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid", "Bearer dt_abc123", "dt_abc123"},
		{"empty", "", ""},
		{"no_prefix", "dt_abc123", ""},
		{"basic_auth", "Basic dXNlcjpwYXNz", ""},
		{"bearer_lowercase", "bearer dt_abc", ""},
		{"bearer_no_space", "Bearerdt_abc", ""},
		{"bearer_empty_token", "Bearer ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			if got := extractBearerToken(r); got != tt.want {
				t.Errorf("extractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionCookieName(t *testing.T) {
	// In tests BASE_URL is not set, so secure=false, cookie name = "session"
	if got := SessionCookieName(); got != "session" {
		t.Errorf("SessionCookieName() = %q, want %q", got, "session")
	}
}

func TestCookieNameFor(t *testing.T) {
	if got := cookieNameFor(true); got != "__Host-session" {
		t.Errorf("cookieNameFor(true) = %q, want %q", got, "__Host-session")
	}
	if got := cookieNameFor(false); got != "session" {
		t.Errorf("cookieNameFor(false) = %q, want %q", got, "session")
	}
}

func TestSetAndClearCookie(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		w := httptest.NewRecorder()
		SetSessionCookie(w, "tok123", 3600)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected 1 cookie, got %d", len(cookies))
		}
		c := cookies[0]
		if c.Name != "session" {
			t.Errorf("cookie name = %q, want %q", c.Name, "session")
		}
		if c.Value != "tok123" {
			t.Errorf("cookie value = %q, want %q", c.Value, "tok123")
		}
		if c.MaxAge != 3600 {
			t.Errorf("cookie MaxAge = %d, want %d", c.MaxAge, 3600)
		}
		if c.Path != "/" {
			t.Errorf("cookie Path = %q, want %q", c.Path, "/")
		}
		if !c.HttpOnly {
			t.Error("cookie should be HttpOnly")
		}
	})

	t.Run("clear", func(t *testing.T) {
		w := httptest.NewRecorder()
		ClearSessionCookie(w)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected 1 cookie, got %d", len(cookies))
		}
		c := cookies[0]
		if c.MaxAge != -1 {
			t.Errorf("cookie MaxAge = %d, want %d", c.MaxAge, -1)
		}
		if c.Value != "" {
			t.Errorf("cookie value = %q, want empty", c.Value)
		}
	})
}

func TestTenantFromContext(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		tn := &tenant.Tenant{ID: "t-1", Username: "alice"}
		ctx := WithTenantContext(context.Background(), tn)
		got := TenantFromContext(ctx)
		if got == nil {
			t.Fatal("expected tenant, got nil")
		}
		if got.ID != "t-1" {
			t.Errorf("tenant ID = %q, want %q", got.ID, "t-1")
		}
		if got.Username != "alice" {
			t.Errorf("tenant Username = %q, want %q", got.Username, "alice")
		}
	})

	t.Run("absent", func(t *testing.T) {
		got := TenantFromContext(context.Background())
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

func TestRequireAPITokenMissingHeader(t *testing.T) {
	// nil db is safe because middleware returns 401 before touching DB
	handler := RequireAPIToken(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/score", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body["error"] != "missing or invalid authorization header" {
		t.Errorf("error = %q, want %q", body["error"], "missing or invalid authorization header")
	}
}

func TestRequireAnyAuthNoAuth(t *testing.T) {
	var called bool
	handler := RequireAnyAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		tn := TenantFromContext(r.Context())
		if tn != nil {
			t.Errorf("expected nil tenant, got %+v", tn)
		}
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/score", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("handler should have been called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
