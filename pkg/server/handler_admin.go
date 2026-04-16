package server

import (
	"context"
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

type tokenQuotaRow struct {
	Index     int
	Label     string
	Limit     int
	Used      int
	Remaining int
	Percent   int
	Reset     string
	Error     string
}

type activityBar struct {
	Label   string
	Count   int
	Percent int
}

func buildBars(days []time.Time, counts []int) []activityBar {
	if len(days) == 0 {
		return nil
	}
	maxCount := 0
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}
	bars := make([]activityBar, len(days))
	for i := range days {
		pct := 0
		if maxCount > 0 {
			pct = (counts[i] * 100) / maxCount
		}
		if pct < 2 {
			pct = 2
		}
		bars[i] = activityBar{
			Label:   days[i].Format("01/02"),
			Count:   counts[i],
			Percent: pct,
		}
	}
	return bars
}

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

		if store != nil {
			loadStoreMetrics(r.Context(), store, data)
		}
		if pool != nil {
			loadPoolQuotas(r.Context(), pool, data)
		}

		renderTemplate(w, "admin.html", data)
	}
}

func loadStoreMetrics(ctx context.Context, store *postgres.Store, data map[string]any) {
	tenants, err := tenant.ListTenants(ctx, store.DB())
	if err != nil {
		slog.Error("admin: list tenants", "error", err)
	} else {
		data["Tenants"] = tenants
	}

	if sc, err := store.DailyScoringCounts(ctx, 7); err == nil {
		data["ScoringBars"] = dailyCountBars(sc)
	}

	if depth, err := store.QueueDepth(ctx); err == nil {
		data["QueueDepth"] = depth
	}

	if stale, err := store.StaleCount(ctx, 7, 30); err == nil {
		data["StaleCount"] = stale
	}

	if ps, err := store.PipelineStats(ctx); err == nil {
		data["PipelineStats"] = ps
		data["IngestAge"] = timeSince(ps.LastIngest)
		data["ScorerAge"] = timeSince(ps.LastScored)
	}

	if ac, err := store.DailyActivityCounts(ctx, 7); err == nil {
		data["ActivityBars"] = dailyCountBars(ac)
	}
}

func dailyCountBars(dc []postgres.DailyCount) []activityBar {
	if len(dc) == 0 {
		return nil
	}
	days := make([]time.Time, len(dc))
	counts := make([]int, len(dc))
	for i, d := range dc {
		days[i] = d.Day
		counts[i] = d.Count
	}
	return buildBars(days, counts)
}

func loadPoolQuotas(ctx context.Context, pool *ghclient.TokenPool, data map[string]any) {
	quotas := pool.CheckQuotas(ctx)
	pct, _ := ghclient.AggregateQuota(quotas)
	rows := make([]tokenQuotaRow, len(quotas))
	for i, q := range quotas {
		rows[i] = tokenQuotaRow{
			Index:     q.Index,
			Label:     q.Label,
			Limit:     q.Limit,
			Used:      q.Limit - q.Remaining,
			Remaining: q.Remaining,
			Error:     q.Error,
		}
		if q.Limit > 0 {
			rows[i].Percent = (q.Remaining * 100) / q.Limit
		}
		if !q.Reset.IsZero() {
			rows[i].Reset = q.Reset.Format("15:04:05")
		}
	}
	data["PoolQuotas"] = rows
	data["PoolTotal"] = pool.Size()
	data["PoolActive"] = pool.ActiveCount()
	data["PoolAggregatePct"] = pct
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
