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
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		var req struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64 KB
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if len(req.Name) > maxTokenNameLen || !tokenNameRE.MatchString(req.Name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 1-64 alphanumeric characters, spaces, dots, hyphens, or underscores"})
			return
		}

		// Enforce MaxAPIKeys plan limit.
		existing, err := tenant.ListAPITokens(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("list tokens for limit check", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check token limit"})
			return
		}
		p, ok := plan.Get(tn.Plan)
		if !ok {
			p = plan.Free()
		}
		if p.MaxAPIKeys > 0 && len(existing) >= p.MaxAPIKeys {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "API key limit reached for your plan"})
			return
		}

		rawToken, err := tenant.CreateAPIToken(r.Context(), db, tn.ID, req.Name)
		if err != nil {
			slog.Error("create token", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create token"})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"token": rawToken, "name": req.Name})
	}
}

func listTokensHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		tokens, err := tenant.ListAPITokens(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("list tokens", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list tokens"})
			return
		}

		writeJSON(w, http.StatusOK, tokens)
	}
}

func revokeTokenHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		tokenID := r.PathValue("id")
		if tokenID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token id required"})
			return
		}

		if err := tenant.RevokeAPIToken(r.Context(), db, tn.ID, tokenID); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
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
