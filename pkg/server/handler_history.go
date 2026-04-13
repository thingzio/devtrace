package server

import (
	"net/http"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func historyHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username required"})
			return
		}
		entries, err := store.GetScoreHistory(r.Context(), username, "github", 90)
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
