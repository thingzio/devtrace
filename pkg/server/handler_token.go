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
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
)

const maxTokenNameLen = 64

var tokenNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _.-]*$`)

func createTokenHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var req struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64 KB
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if len(req.Name) > maxTokenNameLen || !tokenNameRE.MatchString(req.Name) {
			writeError(w, http.StatusBadRequest, "name must be 1-64 alphanumeric characters, spaces, dots, hyphens, or underscores")
			return
		}

		// Enforce MaxAPIKeys plan limit.
		existing, err := tenant.ListAPITokens(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("list tokens for limit check", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to check token limit")
			return
		}
		p, ok := plan.Get(tn.Plan)
		if !ok {
			p = plan.Free()
		}
		if p.MaxAPIKeys > 0 && len(existing) >= p.MaxAPIKeys {
			writeError(w, http.StatusForbidden, "API key limit reached for your plan")
			return
		}

		rawToken, err := tenant.CreateAPIToken(r.Context(), db, tn.ID, req.Name)
		if err != nil {
			slog.Error("create token", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to create token")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"token": rawToken, tmplName: req.Name})
	}
}

func listTokensHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		tokens, err := tenant.ListAPITokens(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("list tokens", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}

		writeJSON(w, http.StatusOK, tokens)
	}
}

func revokeTokenHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		tokenID := r.PathValue("id")
		if tokenID == "" {
			writeError(w, http.StatusBadRequest, "token id required")
			return
		}

		if err := tenant.RevokeAPIToken(r.Context(), db, tn.ID, tokenID); err != nil {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError responds with a JSON error envelope at the given status code.
// Centralizes the {"error": "..."} shape used across handlers.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{tmplErrorKey: msg})
}
