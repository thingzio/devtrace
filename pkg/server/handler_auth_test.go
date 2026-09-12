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
	"testing"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/oauth"
)

func TestOAuthStartHandler(t *testing.T) {
	cfg := &oauth.Config{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost:8080/auth/github/callback",
		AuthURL:     "https://github.com/login/oauth/authorize",
	}

	handler := oauthStartHandler(cfg)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/github", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}

	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header")
	}

	// Verify state cookie was set
	var foundState bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "oauth_state" {
			foundState = true
			if c.Value == "" {
				t.Error("state cookie value is empty")
			}
			if !c.HttpOnly {
				t.Error("state cookie should be HttpOnly")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Error("state cookie should be SameSiteLax")
			}
		}
	}
	if !foundState {
		t.Error("oauth_state cookie not set")
	}
}

func TestSignoutHandler(t *testing.T) {
	// signoutHandler works without a real DB when no session cookie is present
	handler := signoutHandler(nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/auth/signout", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}

	loc := rec.Header().Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	// Verify session cookie was cleared
	var foundCleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.SessionCookieName() && c.MaxAge < 0 {
			foundCleared = true
		}
	}
	if !foundCleared {
		t.Error("session cookie was not cleared")
	}
}
