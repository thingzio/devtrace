package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
)

const (
	anonymousUser = "<anonymous>"
	durationNever = "never"
)

func auditLog(action string, tn *tenant.Tenant, path, remoteAddr, detail string) {
	username := anonymousUser
	if tn != nil {
		username = tn.Username
	}
	slog.Warn("admin action",
		"action", action,
		"admin", username,
		"path", path,
		"remote", remoteAddr,
		"detail", detail,
	)
}

func adminDashboardHandler(store *postgres.Store, pool *ghclient.TokenPool, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		auditLog("view_dashboard", tn, r.URL.Path, r.RemoteAddr, "")

		data := map[string]any{
			"Title":     "Admin",
			"Version":   opts.Version,
			"Commit":    opts.Commit,
			"Date":      opts.Date,
			"NavUser":   tn.Username,
			"NavAvatar": tn.AvatarURL,
		}

		if msg := r.URL.Query().Get("msg"); msg != "" {
			data["FlashMsg"] = msg
		}
		if user := r.URL.Query().Get("user"); user != "" {
			data["FlashUser"] = user
		}

		ctx := r.Context()

		if store != nil {
			tenants, err := tenant.ListTenants(ctx, store.DB())
			if err != nil {
				slog.Error("admin: list tenants", "error", err)
			} else {
				data["Tenants"] = tenants
			}

			metrics, err := store.ScoringMetrics(ctx)
			if err != nil {
				slog.Error("admin: scoring metrics", "error", err)
			} else {
				data["ScoringMetrics"] = metrics
			}

			depth, err := store.QueueDepth(ctx)
			if err != nil {
				slog.Error("admin: queue depth", "error", err)
			} else {
				data["QueueDepth"] = depth
			}

			stale, err := store.StaleCount(ctx, 7, 30)
			if err != nil {
				slog.Error("admin: stale count", "error", err)
			} else {
				data["StaleCount"] = stale
			}

			ps, err := store.PipelineStats(ctx)
			if err != nil {
				slog.Error("admin: pipeline stats", "error", err)
			} else {
				data["PipelineStats"] = ps
				data["IngestAge"] = timeSince(ps.LastIngest)
				data["ScorerAge"] = timeSince(ps.LastScored)
			}
		}

		if pool != nil {
			data["PoolTotal"] = pool.Size()
			data["PoolActive"] = pool.ActiveCount()
			data["PoolExhausted"] = pool.Size() - pool.ActiveCount()
			data["PoolUsage"] = pool.UsageCounts()
		}

		renderTemplate(w, "admin.html", data)
	}
}

func timeSince(t time.Time) string {
	if t.IsZero() {
		return durationNever
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func adminUpdatePlanFormHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		tn := middleware.TenantFromContext(r.Context())

		username := r.PathValue("username")
		newPlan := r.FormValue("plan")

		if username == "" {
			http.Redirect(w, r, "/admin?msg=error", http.StatusFound)
			return
		}

		_, ok := plan.Get(newPlan)
		if !ok {
			http.Redirect(w, r, "/admin?msg=invalid_plan&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		p, _ := plan.Get(newPlan)
		if _, err := tenant.UpdateTenantPlan(r.Context(), db, target.ID, newPlan, p.MaxContributors); err != nil {
			slog.Error("admin: update plan", "username", username, "error", err)
			http.Redirect(w, r, "/admin?msg=error&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		auditLog("update_plan", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s plan=%s", username, newPlan))
		http.Redirect(w, r, "/admin?msg=plan_updated&user="+url.QueryEscape(username), http.StatusFound)
	}
}

func adminUpdateStatusFormHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		tn := middleware.TenantFromContext(r.Context())

		username := r.PathValue("username")
		newStatus := r.FormValue("status")

		if username == "" {
			http.Redirect(w, r, "/admin?msg=error", http.StatusFound)
			return
		}

		if newStatus != tenant.StatusActive && newStatus != tenant.StatusSuspended {
			http.Redirect(w, r, "/admin?msg=invalid_status&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		if _, err := tenant.UpdateTenantStatus(r.Context(), db, target.ID, newStatus); err != nil {
			slog.Error("admin: update status", "username", username, "error", err)
			http.Redirect(w, r, "/admin?msg=error&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		auditLog("update_status", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s status=%s", username, newStatus))
		http.Redirect(w, r, "/admin?msg=status_updated&user="+url.QueryEscape(username), http.StatusFound)
	}
}
