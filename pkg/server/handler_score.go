package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

var (
	usernameRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\[bot\])?$`)
	repoRE     = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)
)

func scoreHandler(db *sql.DB, store *postgres.Store, svc *service.ScoreService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" || len(username) > 39 || !usernameRE.MatchString(username) {
			writeError(w, http.StatusBadRequest, "invalid username")
			return
		}

		repo := r.URL.Query().Get("repo")
		if repo != "" && !repoRE.MatchString(repo) {
			writeError(w, http.StatusBadRequest, "invalid repo format, expected owner/repo")
			return
		}

		trustedOrgs := r.URL.Query()["trusted_orgs"]

		planName := ""
		tn := middleware.TenantFromContext(r.Context())
		if tn != nil {
			planName = tn.Plan
		}

		// Quota check for authenticated tenants.
		if tn != nil && db != nil {
			if exceeded := checkQuota(r.Context(), w, db, tn); exceeded {
				return
			}
		}

		resp, err := svc.Score(r.Context(), username, repo, planName, trustedOrgs)
		if err != nil {
			slog.Error("scoring failed", "username", username, "error", err)
			writeError(w, http.StatusInternalServerError, "scoring failed")
			return
		}

		// Record usage after successful scoring.
		if tn != nil && db != nil {
			provider := "github"
			if err := tenant.RecordUsage(r.Context(), db, tn.ID, username, provider, "api", false, repo); err != nil {
				slog.Error("record usage failed", "tenant", tn.ID, "error", err)
			}
		}

		persistScore(store, username, resp.Score.Value, resp.Score.Grade, resp.Version, r.Context())
		slog.Info("score request", "source", "api", "username", username)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("encode response", "error", err)
		}
	}
}

// checkQuota verifies the tenant hasn't exceeded their contributor quota.
// Returns true if the request should be rejected (quota exceeded or error).
func checkQuota(ctx context.Context, w http.ResponseWriter, db *sql.DB, tn *tenant.Tenant) bool {
	p, ok := plan.Get(tn.Plan)
	if !ok {
		p = plan.Free()
	}

	periodStart := tenant.BillingPeriodStart()
	used, err := tenant.GetUsageCount(ctx, db, tn.ID, periodStart)
	if err != nil {
		slog.Error("quota check failed", "tenant", tn.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "quota check failed")
		return true
	}

	maxC := p.MaxContributors
	if tn.MaxContributors > 0 {
		maxC = tn.MaxContributors
	}

	if maxC > 0 && used >= maxC {
		resetTime := tenant.NextBillingPeriodStart()
		writeJSON(w, http.StatusForbidden, map[string]any{
			tmplErrorKey:  "contributor quota exceeded",
			"quota_limit": maxC,
			"quota_used":  used,
			"quota_reset": resetTime.Format("2006-01-02T15:04:05Z"),
		})
		return true
	}

	remaining := maxC - used
	if remaining < 0 {
		remaining = 0
	}
	resetTS := tenant.NextBillingPeriodStart().Unix()
	w.Header().Set("X-Quota-Limit", strconv.Itoa(maxC))
	w.Header().Set("X-Quota-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-Quota-Reset", strconv.FormatInt(resetTS, 10))
	return false
}

// persistScore saves score data to the contributor and reputation tables in a
// fire-and-forget goroutine. Used by both the API and scorecard handlers.
// Uses WithoutCancel so the write outlives the HTTP request without losing trace baggage.
func persistScore(store *postgres.Store, username string, value float64, grade, version string, reqCtx context.Context) {
	if store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), 10*time.Second)
		defer cancel()
		if err := store.UpsertContributor(ctx, username, "github"); err != nil {
			slog.Error("upsert contributor", "username", username, "error", err)
			return
		}
		if err := store.UpdateReputation(ctx, username, "github", value, grade, version, false, nil); err != nil {
			slog.Error("upsert reputation", "username", username, "error", err)
		}
		if err := store.SaveScoreHistory(ctx, username, "github", value, grade, false); err != nil {
			slog.Error("save score history", "username", username, "error", err)
		}
	}()
}
