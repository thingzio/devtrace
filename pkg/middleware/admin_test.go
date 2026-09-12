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
	"testing"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		username string
		want     bool
	}{
		{"in_list", "alice,bob", "alice", true},
		{"not_in_list", "alice,bob", "eve", false},
		{"empty_env", "", "alice", false},
		{"spaces", " alice , bob ", "alice", true},
		{"case_sensitive", "Alice", "alice", false},
		{"single_admin", "alice", "alice", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DEVTRACE_ADMIN_USERS", tt.envVal)
			if got := IsAdmin(tt.username); got != tt.want {
				t.Errorf("IsAdmin(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}

func TestRequireAdmin_NoSession(t *testing.T) {
	handler := RequireAdmin(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
	}))
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRequireAdmin_NotAdmin(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "alice")
	tn := &tenant.Tenant{ID: "t-1", Username: "eve", Status: "active"}
	ctx := WithTenantContext(context.Background(), tn)

	// We can't easily test the full middleware (it calls ValidateSession which needs a real DB),
	// but we can test IsAdmin is used correctly by testing it directly.
	// The full middleware integration is covered by the RequireAdmin_NoSession test (no cookie = 404).
	if IsAdmin(tn.Username) {
		t.Error("eve should not be admin")
	}

	// Test that without a cookie, we get 404 even with tenant in context
	handler := RequireAdmin(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
	}))
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestTenantUsername(t *testing.T) {
	if got := tenantUsername(nil); got != "<anonymous>" {
		t.Errorf("tenantUsername(nil) = %q, want %q", got, "<anonymous>")
	}
	tn := &tenant.Tenant{Username: "alice"}
	if got := tenantUsername(tn); got != "alice" {
		t.Errorf("tenantUsername(alice) = %q, want %q", got, "alice")
	}
}
