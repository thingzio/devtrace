package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGenerateCSRFToken(t *testing.T) {
	tok, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok) != csrfTokenBytes*2 {
		t.Fatalf("expected token length %d, got %d", csrfTokenBytes*2, len(tok))
	}

	tok2, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == tok2 {
		t.Fatal("tokens must be unique")
	}
}

func TestValidateCSRF_GETPassthrough(t *testing.T) {
	called := false
	handler := ValidateCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called for GET")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestValidateCSRF_ValidToken(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	called := false
	handler := ValidateCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	form := url.Values{csrfFormField: {token}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/tenant/foo/plan", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName(), Value: token})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called for valid token")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestValidateCSRF_MissingCookie(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	called := false
	handler := ValidateCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	form := url.Values{csrfFormField: {token}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/tenant/foo/plan", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler should not be called without cookie")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestValidateCSRF_MissingFormField(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	called := false
	handler := ValidateCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/tenant/foo/plan", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName(), Value: token})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler should not be called without form field")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestValidateCSRF_TokenMismatch(t *testing.T) {
	token1, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	token2, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	called := false
	handler := ValidateCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	form := url.Values{csrfFormField: {token1}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/tenant/foo/plan", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName(), Value: token2})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler should not be called with mismatched tokens")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFCookieNameFor(t *testing.T) {
	if got := csrfCookieNameFor(true); got != csrfCookieSecure {
		t.Fatalf("expected %q, got %q", csrfCookieSecure, got)
	}
	if got := csrfCookieNameFor(false); got != csrfCookiePlain {
		t.Fatalf("expected %q, got %q", csrfCookiePlain, got)
	}
}
