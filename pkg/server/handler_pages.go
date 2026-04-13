package server

import (
	"log/slog"
	"net/http"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/service"
)

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

func scorecardHandler(svc *service.ScoreService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		repo := r.URL.Query().Get("repo")

		plan := ""
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			plan = tn.Plan
		}

		resp, err := svc.Score(r.Context(), username, repo, plan)
		if err != nil {
			slog.Error("scoring for scorecard", "username", username, "error", err)
			renderTemplate(w, "scorecard.html", map[string]any{
				"Title": username, "Username": username,
				"Grade": "?", "Value": 0.0, "ModelVersion": "?",
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

		renderTemplate(w, "scorecard.html", map[string]any{
			"Title":        username,
			"Username":     username,
			"Grade":        resp.Score.Grade,
			"Value":        resp.Score.Value,
			"ModelVersion": resp.Score.ModelVersion,
			"GradeClass":   gradeClass,
			"Categories":   resp.Score.Categories,
			"Signals":      resp.Signals,
			"RiskSummary":  resp.RiskSummary,
			"RepoContext":  resp.RepoContext,
			"Detail":       resp.Detail,
		})
	}
}

func landingHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
