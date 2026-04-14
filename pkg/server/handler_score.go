package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func scoreHandler(db *sql.DB, store *postgres.Store, svc *service.ScoreService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		repo := r.URL.Query().Get("repo")
		trustedOrgs := r.URL.Query()["trusted_orgs"]

		planName := ""
		tn := middleware.TenantFromContext(r.Context())
		if tn != nil {
			planName = tn.Plan
		}

		// Quota check for authenticated tenants.
		if tn != nil && db != nil {
			p, ok := plan.Get(tn.Plan)
			if !ok {
				p = plan.Free()
			}

			periodStart := tenant.BillingPeriodStart()
			used, err := tenant.GetUsageCount(r.Context(), db, tn.ID, periodStart)
			if err != nil {
				slog.Error("quota check failed", "tenant", tn.ID, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "quota check failed"})
				return
			}

			maxC := p.MaxContributors
			if tn.MaxContributors > 0 {
				maxC = tn.MaxContributors
			}

			if maxC > 0 && used >= maxC {
				resetTime := tenant.NextBillingPeriodStart()
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error":       "contributor quota exceeded",
					"quota_limit": maxC,
					"quota_used":  used,
					"quota_reset": resetTime.Format("2006-01-02T15:04:05Z"),
				})
				return
			}

			// Set quota headers (will be written with the response).
			remaining := maxC - used
			if remaining < 0 {
				remaining = 0
			}
			resetTS := tenant.NextBillingPeriodStart().Unix()
			w.Header().Set("X-Quota-Limit", strconv.Itoa(maxC))
			w.Header().Set("X-Quota-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-Quota-Reset", strconv.FormatInt(resetTS, 10))
		}

		resp, err := svc.Score(r.Context(), username, repo, planName, trustedOrgs)
		if err != nil {
			slog.Error("scoring failed", "username", username, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "scoring failed"})
			return
		}

		// Record usage after successful scoring.
		if tn != nil && db != nil {
			provider := "github"
			if err := tenant.RecordUsage(r.Context(), db, tn.ID, username, provider, false); err != nil {
				slog.Error("record usage failed", "tenant", tn.ID, "error", err)
			}
		}

		// Fire-and-forget: persist score history for trend charts.
		// Uses background context intentionally — request may complete before save finishes.
		if store != nil {
			go func() { //nolint:gosec // intentional: background ctx outlives request
				ctx := context.Background()
				if err := store.UpsertContributor(ctx, username, "github"); err != nil {
					slog.Error("upsert contributor", "username", username, "error", err)
					return
				}
				if err := store.SaveScoreHistory(ctx, username, "github", resp.Score.Value, resp.Score.Grade, false); err != nil {
					slog.Error("save score history", "username", username, "error", err)
				}
			}()
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("encode response", "error", err)
		}
	}
}
