package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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

type tenantRow struct {
	*tenant.Tenant
	HasInstall bool
}

func buildTenantRows(ctx context.Context, db *sql.DB, tenants []*tenant.Tenant) []tenantRow {
	rows := make([]tenantRow, len(tenants))
	for i, tn := range tenants {
		rows[i] = tenantRow{Tenant: tn}
		installs, err := tenant.GetActiveInstallations(ctx, db, tn.ID)
		if err == nil && len(installs) > 0 {
			rows[i].HasInstall = true
		}
	}
	return rows
}

type tokenQuotaRow struct {
	Index          int
	Label          string
	InstallationID int64
	Limit          int
	Used           int
	Remaining      int
	Percent        int
	Reset          string
	Error          string
}

type activityBar struct {
	Label   string
	UTC     string // ISO 8601 timestamp for client-side local time conversion
	Count   int
	Percent int
}

func buildBars(times []time.Time, counts []int, format string) []activityBar {
	if len(times) == 0 {
		return nil
	}
	maxCount := 0
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}
	bars := make([]activityBar, len(times))
	for i := range times {
		pct := 0
		if maxCount > 0 {
			pct = (counts[i] * 100) / maxCount
		}
		if pct < 2 {
			pct = 2
		}
		bars[i] = activityBar{
			Label:   times[i].Format(format),
			UTC:     times[i].UTC().Format(time.RFC3339),
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

func adminBaseData(r *http.Request, opts Options) (map[string]any, *tenant.Tenant) {
	tn := middleware.TenantFromContext(r.Context())
	if tn == nil {
		return nil, nil
	}

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

	return data, tn
}

func adminDashboardHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, tn := adminBaseData(r, opts)
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		auditLog("view_dashboard", tn, r.URL.Path, r.RemoteAddr, "")

		if store != nil {
			loadPipelineMetrics(r.Context(), store, data)
		}

		renderTemplate(w, "admin.html", data)
	}
}

func adminTokensHandler(pool *ghclient.TokenPool, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, tn := adminBaseData(r, opts)
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		auditLog("view_tokens", tn, r.URL.Path, r.RemoteAddr, "")

		if pool != nil {
			loadPoolQuotas(r.Context(), pool, data)
		}

		renderTemplate(w, "admin_tokens.html", data)
	}
}

// GET /admin/tokens/quota-history — JSON time-series of token quota samples.
func adminTokenQuotaHistoryHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tn := adminBaseData(r, opts)
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		hours := 24
		if h := r.URL.Query().Get("hours"); h != "" {
			if v, err := strconv.Atoi(h); err == nil && v > 0 && v <= 720 {
				hours = v
			}
		}

		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		samples, err := store.GetTokenQuotaSamples(r.Context(), since)
		if err != nil {
			slog.Error("querying quota history", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, samples)
	}
}

func adminTenantsHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, tn := adminBaseData(r, opts)
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		auditLog("view_tenants", tn, r.URL.Path, r.RemoteAddr, "")

		csrfToken, err := middleware.GenerateCSRFToken()
		if err != nil {
			slog.Error("admin: generate csrf token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		middleware.SetCSRFCookie(w, csrfToken, "/")
		data["CSRFToken"] = csrfToken

		if store != nil {
			loadTenantList(r.Context(), store, data)
		}

		renderTemplate(w, "admin_tenants.html", data)
	}
}

func loadPipelineMetrics(ctx context.Context, store *postgres.Store, data map[string]any) {
	if sc, err := store.HourlyScoringCounts(ctx, 24); err == nil {
		data["ScoringBars"] = hourlyCountBars24(sc)
	}

	if depth, err := store.QueueDepth(ctx); err == nil {
		data["QueueDepth"] = depth
	}

	if stale, err := store.StaleCount(ctx, 7, 30); err == nil {
		data["StaleCount"] = stale
	}

	if tc, err := store.ContributorCount(ctx); err == nil {
		data["ContributorCount"] = tc
	}

	if sc, err := store.ScoredCount(ctx); err == nil {
		data["ScoredCount"] = sc
	}

	if ps, err := store.PipelineStats(ctx); err == nil {
		data["PipelineStats"] = ps
		data["IngestAge"] = timeSince(ps.LastIngest)
		data["ScorerAge"] = timeSince(ps.LastScored)
	}

	if ac, err := store.HourlyActivityCounts(ctx, 24); err == nil {
		data["ActivityBars"] = hourlyCountBars(ac)
	}
}

func loadTenantList(ctx context.Context, store *postgres.Store, data map[string]any) {
	tenants, err := tenant.ListTenants(ctx, store.DB())
	if err != nil {
		slog.Error("admin: list tenants", "error", err)
		return
	}
	data["Tenants"] = buildTenantRows(ctx, store.DB(), tenants)
}

func hourlyCountBars(hc []postgres.HourlyCount) []activityBar {
	if len(hc) == 0 {
		return nil
	}
	hours := make([]time.Time, len(hc))
	counts := make([]int, len(hc))
	for i, h := range hc {
		hours[i] = h.Day
		counts[i] = h.Count
	}
	return buildBars(hours, counts, "3pm")
}

// hourlyCountBars24 returns exactly 24 bars, filling gaps with zeros.
func hourlyCountBars24(hc []postgres.HourlyCount) []activityBar {
	now := time.Now().UTC().Truncate(time.Hour)
	lookup := make(map[string]int, len(hc))
	for _, h := range hc {
		lookup[h.Day.UTC().Truncate(time.Hour).Format("2006010215")] = h.Count
	}

	hours := make([]time.Time, 24)
	counts := make([]int, 24)
	for i := range 24 {
		t := now.Add(-time.Duration(23-i) * time.Hour)
		hours[i] = t
		counts[i] = lookup[t.Format("2006010215")]
	}
	return buildBars(hours, counts, "3pm")
}

func loadPoolQuotas(ctx context.Context, pool *ghclient.TokenPool, data map[string]any) {
	quotas := pool.CheckQuotas(ctx)
	pct, _ := ghclient.AggregateQuota(quotas)
	rows := make([]tokenQuotaRow, len(quotas))
	var totalLimit, totalUsed int
	for i, q := range quotas {
		used := q.Limit - q.Remaining
		rows[i] = tokenQuotaRow{
			Index:          q.Index,
			Label:          q.Label,
			InstallationID: q.InstallationID,
			Limit:          q.Limit,
			Used:           used,
			Remaining:      q.Remaining,
			Error:          q.Error,
		}
		if q.Error == "" {
			totalLimit += q.Limit
			totalUsed += used
		}
		if q.Limit > 0 {
			rows[i].Percent = (q.Remaining * 100) / q.Limit
		}
		if !q.Reset.IsZero() {
			rows[i].Reset = q.Reset.Format(time.RFC3339)
		}
	}
	utilPct := 0
	if totalLimit > 0 {
		utilPct = (totalUsed * 100) / totalLimit
	}
	data["PoolQuotas"] = rows
	data["PoolTotal"] = pool.Size()
	data["PoolActive"] = pool.ActiveCount()
	data["PoolAggregatePct"] = pct
	data["TotalLimit"] = totalLimit
	data["TotalUsed"] = totalUsed
	data["TotalAvailable"] = totalLimit - totalUsed
	data["UtilizationPct"] = utilPct
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
			http.Redirect(w, r, "/admin/tenants?msg=error", http.StatusFound)
			return
		}

		_, ok := plan.Get(newPlan)
		if !ok {
			http.Redirect(w, r, "/admin/tenants?msg=invalid_plan&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin/tenants?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		p, _ := plan.Get(newPlan)
		if _, err := tenant.UpdateTenantPlan(r.Context(), db, target.ID, newPlan, p.MaxContributors); err != nil {
			slog.Error("admin: update plan", "username", username, "error", err)
			http.Redirect(w, r, "/admin/tenants?msg=error&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		auditLog("update_plan", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s plan=%s", username, newPlan))
		http.Redirect(w, r, "/admin/tenants?msg=plan_updated&user="+url.QueryEscape(username), http.StatusFound)
	}
}

func adminUpdateStatusFormHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		tn := middleware.TenantFromContext(r.Context())

		username := r.PathValue("username")
		newStatus := r.FormValue("status")

		if username == "" {
			http.Redirect(w, r, "/admin/tenants?msg=error", http.StatusFound)
			return
		}

		if newStatus != tenant.StatusActive && newStatus != tenant.StatusSuspended {
			http.Redirect(w, r, "/admin/tenants?msg=invalid_status&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin/tenants?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		if _, err := tenant.UpdateTenantStatus(r.Context(), db, target.ID, newStatus); err != nil {
			slog.Error("admin: update status", "username", username, "error", err)
			http.Redirect(w, r, "/admin/tenants?msg=error&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		auditLog("update_status", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s status=%s", username, newStatus))
		http.Redirect(w, r, "/admin/tenants?msg=status_updated&user="+url.QueryEscape(username), http.StatusFound)
	}
}

type dayOption struct {
	Value  int
	Label  string
	Active bool
}

func adminMetricsHandler(store *postgres.Store, mcfg *adminMetricsConfig, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, tn := adminBaseData(r, opts)
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		auditLog("view_metrics", tn, r.URL.Path, r.RemoteAddr, "")

		if mcfg == nil {
			data["MetricsDisabled"] = true
			renderTemplate(w, "admin_metrics.html", data)
			return
		}

		days := defaultMetricDays
		if d := r.URL.Query().Get("days"); d != "" {
			if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= maxMetricDays {
				days = v
			}
		}

		options := make([]dayOption, len(metricDayOptions))
		for i, d := range metricDayOptions {
			label := fmt.Sprintf("%dd", d)
			options[i] = dayOption{Value: d, Label: label, Active: d == days}
		}
		data["DayOptions"] = options

		gcpCtx, gcpCancel := context.WithTimeout(r.Context(), handlerTimeout)
		defer gcpCancel()

		token, err := gcpMetadataToken(gcpCtx)
		if err != nil {
			slog.Error("admin metrics: gcp token", "error", err)
			data["AnalysisError"] = "Failed to obtain GCP access token"
			renderTemplate(w, "admin_metrics.html", data)
			return
		}

		gcpMetrics := collectGCPMetrics(gcpCtx, mcfg, token, days)
		dbMetrics := collectDBMetrics(r.Context(), store, store.DB(), days)
		allMetrics := gcpMetrics + dbMetrics

		data["RawMetrics"] = allMetrics

		switch {
		case gcpCtx.Err() != nil:
			data["AnalysisError"] = "Metrics collection timed out; analysis skipped"
		case mcfg.anthropicKey == "":
			data["AnalysisError"] = "Anthropic API key not configured"
		default:
			analysis, aErr := analyzeAdminMetrics(r.Context(), mcfg, allMetrics)
			if aErr != nil {
				slog.Error("admin metrics: analysis", "error", aErr)
				data["AnalysisError"] = aErr.Error()
			} else {
				data["Analysis"] = analysis
			}
		}

		renderTemplate(w, "admin_metrics.html", data)
	}
}

func adminDeleteTenantHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		tn := middleware.TenantFromContext(r.Context())

		username := r.PathValue("username")
		if username == "" {
			http.Redirect(w, r, "/admin/tenants?msg=error", http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin/tenants?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		if err := tenant.DeleteTenant(r.Context(), db, target.ID); err != nil {
			slog.Error("admin: delete tenant", "username", username, "error", err)
			http.Redirect(w, r, "/admin/tenants?msg=error&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		auditLog("delete_tenant", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s id=%s", username, target.ID))
		http.Redirect(w, r, "/admin/tenants?msg=tenant_deleted&user="+url.QueryEscape(username), http.StatusFound)
	}
}
