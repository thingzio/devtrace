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
)

func TestContactEnabled(t *testing.T) {
	cases := []struct {
		name     string
		apiKey   string
		support  string
		wantTrue bool
	}{
		{name: "both set", apiKey: "k", support: "s@example.com", wantTrue: true},
		{name: "key missing", apiKey: "", support: "s@example.com", wantTrue: false},
		{name: "support missing", apiKey: "k", support: "", wantTrue: false},
		{name: "both missing", apiKey: "", support: "", wantTrue: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SEND_API_KEY", tc.apiKey)
			t.Setenv("SUPPORT_EMAIL", tc.support)
			if got := contactEnabled(); got != tc.wantTrue {
				t.Errorf("contactEnabled() = %v, want %v", got, tc.wantTrue)
			}
		})
	}
}

func TestHelpPageHandler_AnonymousRenders(t *testing.T) {
	// Ensure contact-form path is the disabled branch — keeps the test
	// hermetic regardless of environment.
	t.Setenv("SEND_API_KEY", "")
	t.Setenv("SUPPORT_EMAIL", "")

	h := helpPageHandler(nil, Options{Version: "vtest"})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/help", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "help") {
		t.Errorf("body missing help marker: %.300s", w.Body.String())
	}
}

func TestHelpContactHandler_NoTenantUnauthorized(t *testing.T) {
	h := helpContactHandler(nil, Options{})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/help/contact", strings.NewReader("message=hi"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestTryGetTenant_NoCookie(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/help", nil)
	if got := tryGetTenant(r, nil); got != nil {
		t.Errorf("expected nil tenant when cookie absent, got %+v", got)
	}
}
