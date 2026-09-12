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

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/tenant"
)

// authedCtx returns a request context that carries an authenticated tenant.
// Used by tests for the handlers that branch on TenantFromContext.
func authedCtx(plan string) context.Context {
	tn := &tenant.Tenant{
		ID:        "t-test",
		Username:  "test-user",
		Name:      "Test User",
		AvatarURL: "https://example.test/avatar.png",
		Plan:      plan,
		Status:    "active",
	}
	return middleware.WithTenantContext(context.Background(), tn)
}

func TestLandingHandler_Anonymous(t *testing.T) {
	h := landingHandler(Options{Version: "vtest"})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "DevTrace") {
		t.Errorf("body missing DevTrace marker: %.200s", w.Body.String())
	}
}

func TestLandingHandler_AuthedRedirectsToDashboard(t *testing.T) {
	h := landingHandler(Options{})
	r := httptest.NewRequestWithContext(authedCtx("free"), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

func TestLandingHandler_KnownErrorCode(t *testing.T) {
	h := landingHandler(Options{})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?err=auth_failed", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Authentication failed") {
		t.Errorf("expected auth_failed error message; body: %.300s", w.Body.String())
	}
}

func TestLandingHandler_UnknownErrorCodeIgnored(t *testing.T) {
	h := landingHandler(Options{})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?err=bogus_code_value", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "bogus_code_value") {
		t.Error("unknown error codes must not be reflected back into HTML (XSS surface)")
	}
}

func TestChangelogPageHandler(t *testing.T) {
	h := changelogPageHandler(Options{Version: "vtest"})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/changelog", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Changelog") {
		t.Errorf("body missing Changelog marker: %.200s", w.Body.String())
	}
}

func TestCompliancePageHandler(t *testing.T) {
	h := compliancePageHandler(Options{Version: "vtest"})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/compliance", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Compliance") {
		t.Errorf("body missing Compliance marker: %.200s", w.Body.String())
	}
}

func TestCompliancePageHandler_AuthedShowsNav(t *testing.T) {
	h := compliancePageHandler(Options{Version: "vtest"})
	r := httptest.NewRequestWithContext(authedCtx("free"), http.MethodGet, "/compliance", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// authed branch sets NavUser; the layout template surfaces it.
	if !strings.Contains(w.Body.String(), "test-user") {
		t.Errorf("authed page should render nav username; body: %.300s", w.Body.String())
	}
}

func TestTosPageHandler(t *testing.T) {
	h := tosPageHandler(nil, Options{Version: "vtest"})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/tos", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "terms") {
		t.Errorf("body missing Terms marker: %.200s", w.Body.String())
	}
}

func TestDashboardHandler_Unauthed(t *testing.T) {
	h := dashboardHandler(nil, Options{})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/auth/github" {
		t.Errorf("Location = %q, want /auth/github", loc)
	}
}

func TestSettingsHandler_Unauthed(t *testing.T) {
	h := settingsHandler(nil, Options{})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
}

func TestTosAcceptHandler_NoTenantUnauthorized(t *testing.T) {
	h := tosAcceptHandler(nil)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/tos/accept", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestScorecardHandler_BlankUsernameRedirects(t *testing.T) {
	h := scorecardHandler(nil, nil, Options{})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/score/", nil)
	// PathValue is empty when not routed via mux pattern; verifies the
	// guard short-circuits before touching nil store/svc.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}
