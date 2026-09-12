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
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/thingzio/devtrace/pkg/compliance"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

var errMessages = map[string]string{
	"auth_failed":  "Authentication failed. Please try again.",
	"auth_expired": "Your sign-in session expired. Please try again.",
	"rate_limit":   "Too many requests. Please wait a moment and try again.",
}

type pageData struct {
	Title     string
	Version   string
	Commit    string
	Date      string
	Error     string
	NavUser   string
	NavAvatar string
	Plans     []plan.Plan
	Features  []plan.Feature
}

func scorecardHandler(store *postgres.Store, svc *service.ScoreService, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		repo := r.URL.Query().Get("repo")

		// Score card page always shows full data — it's the marketing showcase.
		// Use "free" as minimum to get signals/categories/risk summary.
		// The API endpoint (/api/v1/score) still gates by actual plan.
		scorePlan := tmplPlanFree
		tn := middleware.TenantFromContext(r.Context())
		if tn != nil {
			scorePlan = tn.Plan
		}

		resp, err := svc.Score(r.Context(), username, repo, scorePlan, nil)
		if err != nil {
			slog.Error("scoring for scorecard", "username", username, "error", err)
			errData := map[string]any{
				tmplTitle: username, tmplUsername: username,
				tmplGrade: "?", tmplValue: 0.0, tmplModelVersion: "?", tmplVersion: opts.Version, tmplCommit: opts.Commit, tmplDate: opts.Date,
				tmplGradeClass: "grade-f",
			}
			if tn != nil {
				errData[tmplNavUser] = tn.Username
				errData[tmplNavAvatar] = tn.AvatarURL
			}
			renderTemplate(w, "scorecard.html", errData)
			return
		}

		persistScore(store, username, resp.Score.Value, resp.Score.Grade, resp.Version, r.Context())
		slog.Info("score request", "source", "ui", "username", username)

		gradeClass := "grade-f"
		if g := resp.Score.Grade; len(g) > 0 {
			switch g[0] {
			case 'A':
				gradeClass = "grade-a"
			case 'B':
				gradeClass = "grade-b"
			case 'C':
				gradeClass = "grade-c"
			case 'D':
				gradeClass = "grade-d"
			}
		}

		// Record usage for authenticated users
		if tn != nil {
			if uerr := tenant.RecordUsage(r.Context(), store.DB(), tn.ID, username, "github", "ui", false, repo); uerr != nil {
				slog.Error("scorecard: record usage", "tenant", tn.ID, "error", uerr)
			}
		}

		// Determine current plan for upsell messaging.
		planName := ""
		if tn != nil {
			planName = tn.Plan
		}

		data := map[string]any{
			tmplTitle:        username,
			tmplUsername:     username,
			"Profile":        resp.Profile,
			tmplGrade:        resp.Score.Grade,
			tmplValue:        resp.Score.Value,
			tmplModelVersion: resp.Version,
			tmplVersion:      opts.Version,
			tmplCommit:       opts.Commit,
			tmplDate:         opts.Date,
			"ScoringMode":    resp.ScoringMode,
			tmplGradeClass:   gradeClass,
			"Categories":     resp.Score.Categories,
			"Signals":        resp.Signals,
			"RiskSummary":    resp.RiskSummary,
			"RepoContext":    resp.RepoContext,
			"AISensing":      resp.AISensing,
			"Enrichment":     resp.Enrichment,
			"Upsell":         plan.Upsell(planName),
		}
		if scorePlan == tmplPlanPro {
			data["Compliance"] = compliance.EvaluatePractices(
				resp.Signals, resp.RepoContext, resp.Score.Categories,
				resp.Behavior, resp.AISensing)
		}
		if tn != nil {
			data[tmplNavUser] = tn.Username
			data[tmplNavAvatar] = tn.AvatarURL
		}
		renderTemplate(w, "scorecard.html", data)
	}
}

func dashboardHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, authGitHubPath, http.StatusFound)
			return
		}

		db := store.DB()
		limits, _ := plan.Get(tn.Plan)
		maxContribs := tn.MaxContributors
		if maxContribs == 0 {
			maxContribs = limits.MaxContributors
		}

		used, err := tenant.GetUsageCount(r.Context(), db, tn.ID, tenant.BillingPeriodStart())
		if err != nil {
			slog.Error("dashboard: get usage count", "tenant", tn.ID, "error", err)
		}
		tokens, err := tenant.ListAPITokens(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("dashboard: list api tokens", "tenant", tn.ID, "error", err)
		}
		recent, err := tenant.GetRecentScored(r.Context(), db, tn.ID, 10)
		if err != nil {
			slog.Error("dashboard: get recent scored", "tenant", tn.ID, "error", err)
		}

		pct := 0
		if maxContribs > 0 {
			pct = (used * 100) / maxContribs
			if pct > 100 {
				pct = 100
			}
		}

		filter, eventsPage := parseEventFilter(r)
		eventsResp, events, eerr := fetchEvents(r, store, tn.ID, filter, eventsPage)
		if eerr != nil {
			slog.Error("dashboard: get notification events", "tenant", tn.ID, "error", eerr)
		}

		watchlists, wlErr := store.ListWatchlists(r.Context(), tn.ID)
		if wlErr != nil {
			slog.Error("dashboard: list watchlists", "tenant", tn.ID, "error", wlErr)
		}
		hasWatchlists := len(watchlists) > 0

		t := pageTemplates["home.html"]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "home", map[string]any{
			tmplKeyUsername:      tn.Username,
			tmplName:             tn.Name,
			"company":            tn.Company,
			"location":           tn.Location,
			tmplBio:              tn.Bio,
			"avatar_url":         tn.AvatarURL,
			tmplKeyPlan:          tn.Plan,
			"quota_used":         used,
			"quota_limit":        maxContribs,
			"quota_pct":          pct,
			"tokens":             tokens,
			"recent":             recent,
			"events":             events,
			"has_watchlists":     hasWatchlists,
			"events_filter_user": filter.Contributor,
			"events_filter_org":  filter.Org,
			"events_filter_repo": filter.Repo,
			"events_page":        eventsPage,
			"events_total":       eventsResp.Total,
			"events_total_pages": eventsResp.TotalPages,
			"version":            opts.Version,
			"commit":             opts.Commit,
			"date":               opts.Date,
		}); err != nil {
			slog.Error("render dashboard", "error", err)
		}
	}
}

func settingsHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, authGitHubPath, http.StatusFound)
			return
		}
		db := store.DB()
		limits, _ := plan.Get(tn.Plan)
		lastSignIn := tenant.GetLastSignIn(r.Context(), db, tn.ID)
		lastLogin := "never"
		if lastSignIn != nil {
			lastLogin = lastSignIn.Format("2006-01-02")
		}
		tokens, err := tenant.ListAPITokens(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("settings: list api tokens", "tenant", tn.ID, "error", err)
		}

		hasInstall := false
		if installs, ierr := tenant.GetActiveInstallations(r.Context(), db, tn.ID); ierr == nil && len(installs) > 0 {
			hasInstall = true
		}

		appInstallURL := "https://github.com/apps/DevTraceThingz/installations/new"

		watchlists, wlErr := store.ListWatchlists(r.Context(), tn.ID)
		if wlErr != nil {
			slog.Error("settings: list watchlists", "tenant", tn.ID, "error", wlErr)
		}
		manualCount, _ := store.WatchlistManualCount(r.Context(), tn.ID)
		canAddWatchlist := manualCount < limits.MaxWatchlists

		renderTemplate(w, "settings.html", map[string]any{
			tmplTitle:           "Settings",
			tmplVersion:         opts.Version,
			tmplCommit:          opts.Commit,
			tmplDate:            opts.Date,
			"CSRFToken":         middleware.CSRFTokenFromContext(r.Context()),
			tmplNavUser:         tn.Username,
			tmplNavAvatar:       tn.AvatarURL,
			tmplKeyUsername:     tn.Username,
			tmplName:            tn.Name,
			"email":             tn.Email,
			"company":           tn.Company,
			"location":          tn.Location,
			tmplBio:             tn.Bio,
			"avatar_url":        tn.AvatarURL,
			tmplKeyPlan:         tn.Plan,
			"max_contributors":  limits.MaxContributors,
			tmplKeyRateLimit:    limits.RateLimitPerHour,
			"created_at":        tn.CreatedAt.Format("2006-01-02"),
			"last_login":        lastLogin,
			"tokens":            tokens,
			"has_install":       hasInstall,
			"app_install_url":   appInstallURL,
			"flash_msg":         flashMessage(r.URL.Query().Get("msg")),
			"watchlists":        watchlists,
			"can_add_watchlist": canAddWatchlist,
			"max_watchlists":    limits.MaxWatchlists,
			"digest_email":      limits.DigestEmail,
		})
	}
}

func tosPageHandler(_ *sql.DB, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := pageData{
			Title:   "Terms of Service",
			Version: opts.Version,
			Commit:  opts.Commit,
			Date:    opts.Date,
		}
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			pd.NavUser = tn.Username
			pd.NavAvatar = tn.AvatarURL
		}
		renderTemplate(w, "tos.html", pd)
	}
}

func tosAcceptHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := tenant.AcceptToS(r.Context(), store.DB(), tn.ID); err != nil {
			slog.Error("accept tos", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, dashboardPath, http.StatusFound)
	}
}

func compliancePageHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := pageData{
			Title:   "Compliance",
			Version: opts.Version,
			Commit:  opts.Commit,
			Date:    opts.Date,
		}
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			data.NavUser = tn.Username
			data.NavAvatar = tn.AvatarURL
		}
		renderTemplate(w, "compliance.html", data)
	}
}

func changelogPageHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := pageData{
			Title:   "Changelog",
			Version: opts.Version,
			Commit:  opts.Commit,
			Date:    opts.Date,
		}
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			data.NavUser = tn.Username
			data.NavAvatar = tn.AvatarURL
		}
		renderTemplate(w, "changelog.html", data)
	}
}

func landingHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if middleware.TenantFromContext(r.Context()) != nil {
			http.Redirect(w, r, dashboardPath, http.StatusFound)
			return
		}
		var errMsg string
		if code := r.URL.Query().Get("err"); code != "" {
			if msg, ok := errMessages[code]; ok {
				errMsg = msg
			}
		}
		pd := pageData{
			Title:    "Home",
			Version:  opts.Version,
			Commit:   opts.Commit,
			Date:     opts.Date,
			Error:    errMsg,
			Plans:    plan.DisplayPlans(),
			Features: plan.DisplayFeatures(),
		}
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			pd.NavUser = tn.Username
			pd.NavAvatar = tn.AvatarURL
		}
		renderTemplate(w, "landing.html", pd)
	}
}
