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

// adminBearerToken extracts the Bearer token from the Authorization header.
func adminBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func adminUpdatePlanHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		tenantID := r.PathValue("id")
		if tenantID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing tenant id"})
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

		tn, err := tenant.UpdateTenantPlan(r.Context(), db, tenantID, req.Plan, p.MaxContributors)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update plan"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":        tn.ID,
			"username":         tn.Username,
			"plan":             tn.Plan,
			"max_contributors": tn.MaxContributors,
			"updated_at":       tn.UpdatedAt.Format(time.RFC3339),
		})
	}
}

func adminUpdateStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		tenantID := r.PathValue("id")
		if tenantID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing tenant id"})
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

		tn, err := tenant.UpdateTenantStatus(r.Context(), db, tenantID, req.Status)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update status"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":  tn.ID,
			"username":   tn.Username,
			"status":     tn.Status,
			"updated_at": tn.UpdatedAt.Format(time.RFC3339),
		})
	}
}
