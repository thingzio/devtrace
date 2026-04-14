package server

import (
	"log/slog"
	"net/http"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func stubPageHandler(title string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		renderTemplate(w, "stub.html", pageData{Title: title})
	}
}

var errMessages = map[string]string{
	"auth_failed":  "Authentication failed. Please try again.",
	"auth_expired": "Your sign-in session expired. Please try again.",
	"rate_limit":   "Too many requests. Please wait a moment and try again.",
}

type pageData struct {
	Title   string
	Version string
	Commit  string
	Date    string
	Error   string
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
		plan := "free"
		tn := middleware.TenantFromContext(r.Context())
		if tn != nil {
			plan = tn.Plan
		}

		resp, err := svc.Score(r.Context(), username, repo, plan, nil)
		if err != nil {
			slog.Error("scoring for scorecard", "username", username, "error", err)
			renderTemplate(w, "scorecard.html", map[string]any{
				"Title": username, "Username": username,
				"Grade": "?", "Value": 0.0, "ModelVersion": "?", "Version": opts.Version,
				"GradeClass": "grade-f",
			})
			return
		}

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
			if uerr := tenant.RecordUsage(r.Context(), store.DB(), tn.ID, username, "github", "ui", false); uerr != nil {
				slog.Error("scorecard: record usage", "tenant", tn.ID, "error", uerr)
			}
		}

		// Show sign-up CTA for unauthenticated visitors
		showSignUp := tn == nil

		renderTemplate(w, "scorecard.html", map[string]any{
			"Title":        username,
			"Username":     username,
			"Profile":      resp.Profile,
			"Grade":        resp.Score.Grade,
			"Value":        resp.Score.Value,
			"ModelVersion": resp.Version,
			"Version":      opts.Version,
			"ScoringMode":  resp.ScoringMode,
			"GradeClass":   gradeClass,
			"Categories":   resp.Score.Categories,
			"Signals":      resp.Signals,
			"RiskSummary":  resp.RiskSummary,
			"RepoContext":  resp.RepoContext,
			"ShowSignUp":   showSignUp,
		})
	}
}

func dashboardHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
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

		t := pageTemplates["home.html"]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "home", map[string]any{
			"username":    tn.Username,
			"name":        tn.Name,
			"company":     tn.Company,
			"location":    tn.Location,
			"bio":         tn.Bio,
			"avatar_url":  tn.AvatarURL,
			"plan":        tn.Plan,
			"quota_used":  used,
			"quota_limit": maxContribs,
			"quota_pct":   pct,
			"tokens":      tokens,
			"recent":      recent,
			"version":     opts.Version,
		}); err != nil {
			slog.Error("render dashboard", "error", err)
		}
	}
}

func settingsHandler(store *postgres.Store, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
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

		renderTemplate(w, "settings.html", map[string]any{
			"Title":            "Settings",
			"Version":          opts.Version,
			"username":         tn.Username,
			"name":             tn.Name,
			"email":            tn.Email,
			"company":          tn.Company,
			"location":         tn.Location,
			"bio":              tn.Bio,
			"avatar_url":       tn.AvatarURL,
			"plan":             tn.Plan,
			"max_contributors": limits.MaxContributors,
			"rate_limit":       limits.RateLimitPerHour,
			"created_at":       tn.CreatedAt.Format("2006-01-02"),
			"last_login":       lastLogin,
			"tokens":           tokens,
		})
	}
}

func tosPageHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, "tos.html", pageData{Title: "Terms of Service"})
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
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	}
}

func landingHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if middleware.TenantFromContext(r.Context()) != nil {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		var errMsg string
		if code := r.URL.Query().Get("err"); code != "" {
			if msg, ok := errMessages[code]; ok {
				errMsg = msg
			}
		}
		renderTemplate(w, "landing.html", pageData{
			Title:   "Home",
			Version: opts.Version,
			Error:   errMsg,
		})
	}
}
