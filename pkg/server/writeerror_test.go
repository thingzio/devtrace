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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thingzio/devtrace/pkg/service"
)

// TestWriteErrorShape pins the JSON envelope contract used by the
// scoring API and middleware. Clients depend on this exact shape:
//   - Status code as set by caller
//   - Content-Type: application/json
//   - Body: {"error": "<message>"}
//
// Any drift here breaks API consumers (the GitHub Action, downstream
// integrations, error reporters), so the contract is asserted directly
// rather than relied on by inspection.
func TestWriteErrorShape(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		message string
	}{
		{"bad request", http.StatusBadRequest, "invalid username"},
		{"unauthorized", http.StatusUnauthorized, "missing token"},
		{"forbidden", http.StatusForbidden, "quota exceeded"},
		{"not found", http.StatusNotFound, "token not found"},
		{"server error", http.StatusInternalServerError, "scoring failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeError(rec, tc.status, tc.message)

			if rec.Code != tc.status {
				t.Errorf("status: got %d, want %d", rec.Code, tc.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type: got %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if got := body[tmplErrorKey]; got != tc.message {
				t.Errorf("body[%q]: got %q, want %q", tmplErrorKey, got, tc.message)
			}
			if len(body) != 1 {
				t.Errorf("expected exactly 1 key (%q), got %d: %v",
					tmplErrorKey, len(body), body)
			}
		})
	}
}

// TestScoreHandlerErrorResponses asserts the scoring handler produces
// the correct error envelope for each invalid-input branch. Catches
// regressions where someone bypasses writeError and leaks the raw
// http.Error text body.
func TestScoreHandlerErrorResponses(t *testing.T) {
	mock := &mockGH{
		profile: nil, signals: nil, // unused; we don't reach the fetch path
	}
	svc := service.NewScoreService(mock, "v0.0.1-test")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tests := []struct {
		name       string
		url        string
		wantStatus int
		wantError  string
	}{
		{"bad username chars", "/api/v1/score/-leading-dash", http.StatusBadRequest, "invalid username"},
		{"bad repo format", "/api/v1/score/octocat?repo=not-a-repo", http.StatusBadRequest, "invalid repo format, expected owner/repo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type: got %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("body is not JSON: %v (body=%s)", err, rec.Body.String())
			}
			got, ok := body[tmplErrorKey]
			if !ok {
				t.Fatalf("response missing %q key: %v", tmplErrorKey, body)
			}
			if got != tc.wantError {
				t.Errorf("error message: got %q, want %q", got, tc.wantError)
			}
		})
	}
}
