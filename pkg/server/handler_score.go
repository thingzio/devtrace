package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/service"
)

func scoreHandler(svc *service.ScoreService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		repo := r.URL.Query().Get("repo")

		plan := ""
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			plan = tn.Plan
		}

		resp, err := svc.Score(r.Context(), username, repo, plan)
		if err != nil {
			slog.Error("scoring failed", "username", username, "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "scoring failed"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("encode response", "error", err)
		}
	}
}
