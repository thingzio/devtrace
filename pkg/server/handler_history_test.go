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
	"strings"
	"testing"
)

func TestHistoryDays(t *testing.T) {
	cases := []struct {
		plan string
		want int
	}{
		{"free", 30},
		{"starter", 90},
		{"pro", 365},
		{"", 30},        // empty defaults to free
		{"unknown", 30}, // unknown defaults to free
	}
	for _, tc := range cases {
		t.Run(tc.plan, func(t *testing.T) {
			if got := historyDays(tc.plan); got != tc.want {
				t.Errorf("historyDays(%q) = %d, want %d", tc.plan, got, tc.want)
			}
		})
	}
}

func TestHistoryHandlerInvalidUsername(t *testing.T) {
	handler := historyHandler(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}/history", handler)

	tests := []struct {
		name     string
		username string
	}{
		{"starts with hyphen", "-invalid"},
		{"ends with hyphen", "invalid-"},
		{"contains underscore", "user_name"},
		{"contains dot", "user.name"},
		{"too long", strings.Repeat("a", 40)},
		{"single hyphen", "-"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := "/api/v1/score/" + tc.username + "/history"
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp["error"] != "invalid username" {
				t.Errorf("expected 'invalid username' error, got %q", resp["error"])
			}
		})
	}
}
