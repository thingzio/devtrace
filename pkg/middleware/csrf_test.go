// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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

func TestSetCSRFCookie(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"admin path", "/admin"},
		{"tos path", "/tos"},
		{"root path", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			SetCSRFCookie(rec, token, tt.path)

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected 1 cookie, got %d", len(cookies))
			}
			c := cookies[0]
			if c.Name != CSRFCookieName() {
				t.Fatalf("expected cookie name %q, got %q", CSRFCookieName(), c.Name)
			}
			if c.Value != token {
				t.Fatalf("expected cookie value %q, got %q", token, c.Value)
			}
			if c.Path != tt.path {
				t.Fatalf("expected cookie path %q, got %q", tt.path, c.Path)
			}
		})
	}
}

func TestValidateCSRF_TOSPath(t *testing.T) {
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/tos/accept", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName(), Value: token})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called for valid TOS token")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestInjectCSRF_GET(t *testing.T) {
	var gotToken string
	handler := InjectCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = CSRFTokenFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotToken == "" {
		t.Fatal("expected CSRF token in context")
	}
	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == CSRFCookieName() {
			found = true
			if c.Value != gotToken {
				t.Fatalf("cookie value %q != context token %q", c.Value, gotToken)
			}
			if c.Path != "/" {
				t.Fatalf("expected cookie path %q, got %q", "/", c.Path)
			}
		}
	}
	if !found {
		t.Fatal("CSRF cookie not set")
	}
}

// TestInjectCSRF_ReusesExistingCookie pins the no-rotate-on-GET
// behavior: when the request already carries a valid CSRF cookie,
// InjectCSRF reuses it rather than generating a fresh one. Stale-form
// mismatches (form rendered on page A with token T1, then page B in
// another tab rotates the cookie to T2 before the user submits the
// form) would surface as "invalid CSRF token" 403s on sign-out.
func TestInjectCSRF_ReusesExistingCookie(t *testing.T) {
	existing, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("seed token: %v", err)
	}

	var seenInContext string
	handler := InjectCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenInContext = CSRFTokenFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName(), Value: existing})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if seenInContext != existing {
		t.Errorf("context token: got %q, want existing %q", seenInContext, existing)
	}
	// Reuse means we should NOT be writing a fresh Set-Cookie header.
	for _, c := range rec.Result().Cookies() {
		if c.Name == CSRFCookieName() {
			t.Errorf("expected no Set-Cookie on valid existing token; got %q", c.Value)
		}
	}
}

// TestInjectCSRF_RotatesInvalidCookie confirms that a malformed/stale
// cookie value (wrong length, non-hex, prior schema) is replaced —
// the reuse path must not silently accept garbage.
func TestInjectCSRF_RotatesInvalidCookie(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"too short", "abc123"},
		{"too long", strings.Repeat("a", csrfTokenBytes*2+1)},
		{"non-hex", strings.Repeat("z", csrfTokenBytes*2)},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := InjectCSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if tt.bad != "" {
				req.AddCookie(&http.Cookie{Name: CSRFCookieName(), Value: tt.bad})
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			var fresh string
			for _, c := range rec.Result().Cookies() {
				if c.Name == CSRFCookieName() {
					fresh = c.Value
				}
			}
			if !validCSRFToken(fresh) {
				t.Errorf("expected fresh valid token; got %q", fresh)
			}
		})
	}
}

func TestInjectCSRF_POSTPassthrough(t *testing.T) {
	called := false
	handler := InjectCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if tok := CSRFTokenFromContext(r.Context()); tok != "" {
			t.Fatal("POST should not inject CSRF token")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/auth/signout", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler not called for POST")
	}
}

func TestCSRFTokenFromContext_Empty(t *testing.T) {
	if tok := CSRFTokenFromContext(context.Background()); tok != "" {
		t.Fatalf("expected empty token from bare context, got %q", tok)
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
