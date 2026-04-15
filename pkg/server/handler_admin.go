package server

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
)

// adminAuth checks the ADMIN_API_KEY env var against the Authorization: Bearer header.
func adminAuth(w http.ResponseWriter, r *http.Request) bool {
	key := os.Getenv("ADMIN_API_KEY")
	if key == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "admin api not configured"})
		return false
	}
	token := adminBearerToken(r)
	if subtle.ConstantTimeCompare([]byte(token), []byte(key)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin key"})
		return false
	}
	return true
}

func adminBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func adminListTenantsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		tenants, err := tenant.ListTenants(r.Context(), db)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list tenants"})
			return
		}

		type tenantRow struct {
			ID              string `json:"tenant_id"`
			Username        string `json:"username"`
			Plan            string `json:"plan"`
			Status          string `json:"status"`
			MaxContributors int    `json:"max_contributors"`
			CreatedAt       string `json:"created_at"`
		}

		rows := make([]tenantRow, 0, len(tenants))
		for _, t := range tenants {
			rows = append(rows, tenantRow{
				ID:              t.ID,
				Username:        t.Username,
				Plan:            t.Plan,
				Status:          t.Status,
				MaxContributors: t.MaxContributors,
				CreatedAt:       t.CreatedAt.Format(time.RFC3339),
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{"tenants": rows})
	}
}

func adminUpdatePlanHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		username := r.PathValue("username")
		if username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing username"})
			return
		}

		tn, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}

		var req struct {
			Plan string `json:"plan"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		p, ok := plan.Get(req.Plan)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan: must be free, starter, or pro"})
			return
		}

		updated, err := tenant.UpdateTenantPlan(r.Context(), db, tn.ID, req.Plan, p.MaxContributors)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update plan"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":        updated.ID,
			"username":         updated.Username,
			"plan":             updated.Plan,
			"max_contributors": updated.MaxContributors,
			"updated_at":       updated.UpdatedAt.Format(time.RFC3339),
		})
	}
}

func adminUpdateStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		username := r.PathValue("username")
		if username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing username"})
			return
		}

		tn, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}

		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Status != tenant.StatusActive && req.Status != tenant.StatusSuspended {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid status: must be " + tenant.StatusActive + " or " + tenant.StatusSuspended,
			})
			return
		}

		updated, err := tenant.UpdateTenantStatus(r.Context(), db, tn.ID, req.Status)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update status"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":  updated.ID,
			"username":   updated.Username,
			"status":     updated.Status,
			"updated_at": updated.UpdatedAt.Format(time.RFC3339),
		})
	}
}
