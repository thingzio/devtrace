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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestCreateTokenHandlerNoAuth(t *testing.T) {
	handler := createTokenHandler(nil)

	body := bytes.NewBufferString(`{"name":"ci-token"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/token", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "unauthorized" {
		t.Errorf("expected 'unauthorized' error, got %q", resp["error"])
	}
}

func TestCreateTokenHandlerBadRequest(t *testing.T) {
	tn := &tenant.Tenant{ID: "t-123", Username: "testuser", Plan: "free"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	// Empty name
	handler := createTokenHandler(nil)
	body := bytes.NewBufferString(`{"name":""}`)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/token", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTokensHandlerNoAuth(t *testing.T) {
	handler := listTokensHandler(nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/token", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeTokenHandlerNoAuth(t *testing.T) {
	handler := revokeTokenHandler(nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/token/tok-1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateTokenHandlerInvalidName(t *testing.T) {
	tn := &tenant.Tenant{ID: "t-123", Username: "testuser", Plan: "free"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	handler := createTokenHandler(nil)

	tests := []struct {
		name string
		body string
	}{
		{"xss in name", `{"name":"<script>alert(1)</script>"}`},
		{"semicolon", `{"name":"drop; table"}`},
		{"too long", `{"name":"` + strings.Repeat("a", 65) + `"}`},
		{"starts with space", `{"name":" leading"}`},
		{"starts with dot", `{"name":".hidden"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/token", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp["error"] == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}

func TestRevokeTokenHandlerMissingID(t *testing.T) {
	tn := &tenant.Tenant{ID: "t-123", Username: "testuser", Plan: "free"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	handler := revokeTokenHandler(nil)

	// PathValue returns "" when not routed through mux
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/api/v1/token/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
