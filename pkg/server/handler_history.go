package server

import (
	"net/http"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
)

// historyDays returns the score history window based on the tenant's plan.
func historyDays(planName string) int {
	if p, ok := plan.Get(planName); ok {
		return p.HistoryDays
	}
	return plan.Free().HistoryDays
}

func historyHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" || len(username) > 39 || !usernameRE.MatchString(username) {
			writeError(w, http.StatusBadRequest, "invalid username")
			return
		}

		planName := ""
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			planName = tn.Plan
		}

		entries, err := store.GetScoreHistory(r.Context(), username, "github", historyDays(planName))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch history")
			return
		}
		if entries == nil {
			entries = []postgres.ScoreHistoryEntry{}
		}
		writeJSON(w, http.StatusOK, entries)
	}
}
