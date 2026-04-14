package server

import (
	"net/http"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
)

// historyDays returns the score history window based on the tenant's plan.
func historyDays(plan string) int {
	switch plan {
	case "pro":
		return 365
	case "starter":
		return 90
	default: // free
		return 30
	}
}

func historyHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username required"})
			return
		}

		plan := ""
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			plan = tn.Plan
		}

		entries, err := store.GetScoreHistory(r.Context(), username, "github", historyDays(plan))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch history"})
			return
		}
		if entries == nil {
			entries = []postgres.ScoreHistoryEntry{}
		}
		writeJSON(w, http.StatusOK, entries)
	}
}
