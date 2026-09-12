// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"os"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	devnet "github.com/thingzio/devtrace/pkg/net"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
	"github.com/thingzio/devtrace/pkg/watchlist"
)

const (
	anonymousUser = "<anonymous>"
	durationNever = "never"
)

const tenantsPageSize = 10

type tenantRow struct {
	*tenant.Tenant
	HasInstall bool
	LastSignIn *time.Time
}

func buildTenantRows(ctx context.Context, db *sql.DB, tenants []*tenant.Tenant) []tenantRow {
	ids := make([]string, len(tenants))
	for i, tn := range tenants {
		ids[i] = tn.ID
	}

	signIns, _ := tenant.GetLastSignIns(ctx, db, ids)
	hasInstall, _ := tenant.HasActiveInstallations(ctx, db, ids)

	rows := make([]tenantRow, len(tenants))
	for i, tn := range tenants {
		rows[i] = tenantRow{Tenant: tn}
		if hasInstall[tn.ID] {
			rows[i].HasInstall = true
		}
		if signIns != nil {
			rows[i].LastSignIn = signIns[tn.ID]
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

type tokenInvalidationRow struct {
	At             string
	Label          string
	InstallationID int64
	Permanent      bool
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
		tmplTitle:     tmplAdmin,
		tmplVersion:   opts.Version,
		tmplCommit:    opts.Commit,
		tmplDate:      opts.Date,
		tmplNavUser:   tn.Username,
		tmplNavAvatar: tn.AvatarURL,
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

		csrfToken, err := middleware.GenerateCSRFToken()
		if err != nil {
			slog.Error("admin: generate csrf token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		middleware.SetCSRFCookie(w, csrfToken, "/")
		data["CSRFToken"] = csrfToken

		if store != nil {
			loadPipelineMetrics(r.Context(), store, data)
		}

		renderTemplate(w, "admin.html", data)
	}
}

func adminSendTestDigestHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		apiKey := os.Getenv("SEND_API_KEY")
		if apiKey == "" {
			http.Redirect(w, r, "/admin?msg=no_email_key", http.StatusFound)
			return
		}

		if tn.Email == "" {
			http.Redirect(w, r, "/admin?msg=no_email", http.StatusFound)
			return
		}

		baseURL := config.GetEnv("BASE_URL", "http://localhost:8080")

		events, err := store.GetUnsentEventsForDigest(r.Context(), tn.ID, 10)
		if err != nil {
			slog.Error("admin: get digest events", "tenant", tn.ID, "error", err)
			http.Redirect(w, r, "/admin?msg=digest_error", http.StatusFound)
			return
		}

		usedReal := len(events) > 0
		if !usedReal {
			events = sampleDigestEvents()
		}

		unsubURL := buildAdminUnsubscribeURL(baseURL, tn.ID)
		htmlBody, textBody := watchlist.RenderDigest(events, baseURL, unsubURL)
		if err := devnet.SendEmail(r.Context(), apiKey, "noreply@thingz.io", tn.Email,
			"DevTrace Weekly Digest (Test)", htmlBody, textBody, "",
			devnet.WithUnsubscribeURL(unsubURL)); err != nil {
			slog.Error("admin: send test digest", "tenant", tn.Username, "error", err)
			http.Redirect(w, r, "/admin?msg=digest_error", http.StatusFound)
			return
		}

		if usedReal {
			if merr := store.MarkAllEventsSent(r.Context(), tn.ID); merr != nil {
				slog.Error("admin: mark events sent", "tenant", tn.ID, "error", merr)
			}
		}

		auditLog("send_test_digest", tn, r.URL.Path, r.RemoteAddr,
			fmt.Sprintf("events=%d real=%v", len(events), usedReal))
		http.Redirect(w, r, "/admin?msg=digest_sent", http.StatusFound)
	}
}

func sampleDigestEvents() []postgres.NotificationEvent {
	now := time.Now().UTC()
	return []postgres.NotificationEvent{
		{
			ID: 0, EventType: postgres.EventTypeNewContributor, Username: "sample-dev",
			Target: "example-org", CreatedAt: now.Add(-2 * time.Hour),
			Details: map[string]any{"prs_opened": float64(3), "prs_merged": float64(1)},
		},
		{
			ID: 0, EventType: postgres.EventTypeScoreChange, Username: "another-dev",
			Target: "example-org/repo", CreatedAt: now.Add(-1 * time.Hour),
			Details: map[string]any{"old_grade": "C", "new_grade": "B"},
		},
	}
}

func buildAdminUnsubscribeURL(baseURL, tenantID string) string {
	secret := watchlist.HMACSecret()
	if secret == "" {
		return ""
	}
	token := watchlist.UnsubscribeToken(secret, tenantID)
	return fmt.Sprintf("%s/digest/unsubscribe?tenant=%s&token=%s", baseURL, tenantID, token)
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

		query := r.URL.Query().Get("q")
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if v, err := strconv.Atoi(p); err == nil && v > 0 {
				page = v
			}
		}
		data["Query"] = query
		data["Page"] = page

		if store != nil {
			loadTenantList(r.Context(), store, data, query, page)
		}

		renderTemplate(w, "admin_tenants.html", data)
	}
}

func adminTenantDetailHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, tn := adminBaseData(r, opts)
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		username := r.PathValue("username")
		if username == "" {
			http.Redirect(w, r, "/admin/tenants", http.StatusFound)
			return
		}

		auditLog("view_tenant_detail", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s", username))

		csrfToken, err := middleware.GenerateCSRFToken()
		if err != nil {
			slog.Error("admin: generate csrf token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		middleware.SetCSRFCookie(w, csrfToken, "/")
		data["CSRFToken"] = csrfToken

		db := store.DB()
		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin/tenants?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}
		data["Target"] = target

		hasInstall := false
		installs, iErr := tenant.GetActiveInstallations(r.Context(), db, target.ID)
		if iErr == nil && len(installs) > 0 {
			hasInstall = true
		}
		data["HasInstall"] = hasInstall

		data["LastSignIn"] = tenant.GetLastSignIn(r.Context(), db, target.ID)

		if lastSent, lsErr := store.LastDigestSentAt(r.Context(), target.ID); lsErr == nil && lastSent != nil {
			data["LastDigestSent"] = lastSent
		}

		recent, rErr := tenant.GetRecentScored(r.Context(), db, target.ID, 10)
		if rErr != nil {
			slog.Error("admin: get recent scored", "username", username, "error", rErr)
		}
		data["RecentScored"] = recent

		renderTemplate(w, "admin_tenant_detail.html", data)
	}
}

func loadPipelineMetrics(ctx context.Context, store *postgres.Store, data map[string]any) {
	var mu sync.Mutex
	var wg sync.WaitGroup

	set := func(k string, v any) {
		mu.Lock()
		data[k] = v
		mu.Unlock()
	}

	wg.Add(8)

	go func() {
		defer wg.Done()
		if sc, err := store.HourlyScoringCounts(ctx, 24); err == nil {
			set("ScoringBars", hourlyCountBars24(sc))
		}
	}()

	go func() {
		defer wg.Done()
		if depth, err := store.QueueDepth(ctx); err == nil {
			set("QueueDepth", depth)
		}
	}()

	go func() {
		defer wg.Done()
		if stale, err := store.StaleCount(ctx, 7, 30); err == nil {
			set("StaleCount", stale)
		}
	}()

	go func() {
		defer wg.Done()
		if tc, err := store.ContributorCount(ctx); err == nil {
			set("ContributorCount", tc)
		}
	}()

	go func() {
		defer wg.Done()
		if sc, err := store.ScoredCount(ctx); err == nil {
			set("ScoredCount", sc)
		}
	}()

	go func() {
		defer wg.Done()
		if ps, err := store.PipelineStats(ctx); err == nil {
			mu.Lock()
			data["PipelineStats"] = ps
			data["IngestAge"] = timeSince(ps.LastIngest)
			data["ScorerAge"] = timeSince(ps.LastScored)
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		if ac, err := store.HourlyActivityCounts(ctx, 24); err == nil {
			set("ActivityBars", hourlyCountBars(ac))
		}
	}()

	go func() {
		defer wg.Done()
		if ps, err := store.PREventsStats(ctx); err == nil {
			mu.Lock()
			data["PREventsStats"] = ps
			data["PREventsLastEventAge"] = timeSince(ps.LastEventAt)
			mu.Unlock()
		}
	}()

	wg.Wait()
}

func loadTenantList(ctx context.Context, store *postgres.Store, data map[string]any, query string, page int) {
	offset := (page - 1) * tenantsPageSize
	tenants, total, err := tenant.SearchTenants(ctx, store.DB(), query, tenantsPageSize, offset)
	if err != nil {
		slog.Error("admin: search tenants", "error", err)
		return
	}
	totalPages := (total + tenantsPageSize - 1) / tenantsPageSize
	if totalPages < 1 {
		totalPages = 1
	}

	data["Tenants"] = buildTenantRows(ctx, store.DB(), tenants)
	data["Total"] = total
	data["TotalPages"] = totalPages
	data["HasPrev"] = page > 1
	data["HasNext"] = page < totalPages
	data["PrevPage"] = page - 1
	data["NextPage"] = page + 1
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

	loadPoolInvalidations(pool, data)
}

// loadPoolInvalidations exposes recent 401-driven token invalidations to
// the admin tokens template. Returned newest first so the table reads
// chronologically from the top.
func loadPoolInvalidations(pool *ghclient.TokenPool, data map[string]any) {
	cutoff := time.Now().Add(-1 * time.Hour)
	events := pool.RecentInvalidations(time.Time{})
	rows := make([]tokenInvalidationRow, 0, len(events))
	lastHour := 0
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.At.After(cutoff) {
			lastHour++
		}
		rows = append(rows, tokenInvalidationRow{
			At:             ev.At.UTC().Format(time.RFC3339),
			Label:          ev.Label,
			InstallationID: ev.InstallationID,
			Permanent:      ev.Permanent,
		})
	}
	data["TokenInvalidations"] = rows
	data["TokenInvalidationsLastHour"] = lastHour
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

		detailURL := "/admin/tenant/" + url.PathEscape(username)

		_, ok := plan.Get(newPlan)
		if !ok {
			http.Redirect(w, r, detailURL+"?msg=invalid_plan", http.StatusFound)
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
			http.Redirect(w, r, detailURL+"?msg=error", http.StatusFound)
			return
		}

		auditLog("update_plan", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s plan=%s", username, newPlan))
		http.Redirect(w, r, "/admin/tenant/"+url.PathEscape(username)+"?msg=plan_updated", http.StatusFound)
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

		detailURL := "/admin/tenant/" + url.PathEscape(username)

		if newStatus != tenant.StatusActive && newStatus != tenant.StatusSuspended {
			http.Redirect(w, r, detailURL+"?msg=invalid_status", http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin/tenants?msg=not_found&user="+url.QueryEscape(username), http.StatusFound)
			return
		}

		if _, err := tenant.UpdateTenantStatus(r.Context(), db, target.ID, newStatus); err != nil {
			slog.Error("admin: update status", "username", username, "error", err)
			http.Redirect(w, r, detailURL+"?msg=error", http.StatusFound)
			return
		}

		auditLog("update_status", tn, r.URL.Path, r.RemoteAddr, fmt.Sprintf("user=%s status=%s", username, newStatus))
		http.Redirect(w, r, "/admin/tenant/"+url.PathEscape(username)+"?msg=status_updated", http.StatusFound)
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
