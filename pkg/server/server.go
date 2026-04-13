package server

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/health"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/oauth"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var pageTemplates map[string]*template.Template

var templateFuncs = template.FuncMap{
	"comma": func(n int) string {
		if n == 0 {
			return "Unlimited"
		}
		if n < 1000 {
			return fmt.Sprintf("%d", n)
		}
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	},
	"mul": func(a, b float64) float64 { return a * b },
	"int": func(n int64) int { return int(n) },
}

func init() {
	simplePages := []string{"landing.html", "scorecard.html", "tos.html", "settings.html"}
	pageTemplates = make(map[string]*template.Template, len(simplePages)+1)
	for _, p := range simplePages {
		pageTemplates[p] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html", "templates/"+p))
	}
	pageTemplates["home.html"] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
		"templates/header.html", "templates/home.html", "templates/footer.html"))
}

func renderTemplate(w http.ResponseWriter, name string, data any) {
	t, ok := pageTemplates[name]
	if !ok {
		slog.Error("template not found", "name", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		slog.Error("render template", "name", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// Options holds server configuration passed from the entry point.
type Options struct {
	Version string
	Commit  string
	Date    string
}

// Run starts the HTTP server and blocks until the context is canceled or a fatal error occurs.
func Run(ctx context.Context, opts Options) error {
	store, err := postgres.NewFromEnv(ctx)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	var ghClient ghclient.Client

	// Try GitHub App installation client first.
	if appCfg, appErr := tenant.LoadGitHubAppConfig(); appErr == nil {
		instID := int64(config.GetEnvAsInt("GITHUB_APP_INSTALLATION_ID", 0))
		if instID > 0 {
			ghClient = ghclient.NewInstallationClient(appCfg, instID)
			slog.Info("using GitHub App installation client",
				"app_id", appCfg.AppID,
				"installation_id", instID,
			)
		}
	}

	// Fall back to PAT.
	if ghClient == nil {
		token := config.GetEnv("GITHUB_TOKEN", "")
		if token == "" {
			return fmt.Errorf("GITHUB_TOKEN or GitHub App config (GITHUB_APP_ID+KEY+INSTALLATION_ID) required")
		}
		ghClient = ghclient.NewPATClient(ctx, token)
		slog.Info("using PAT GitHub client")
	}

	scoreSvc := service.NewScoreService(ghClient, nil)

	db := store.DB()

	oauthCfg := &oauth.Config{
		ClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURL:  config.GetEnv("BASE_URL", "http://localhost:8080") + "/auth/github/callback",
	}

	mux := makeRouter(db, scoreSvc, oauthCfg, opts)

	port := config.GetEnv("PORT", "8080")
	srv := &http.Server{
		Addr:              net.JoinHostPort("", port),
		Handler:           securityHeaders(mux),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10, // 64 KB
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownSec := config.GetEnvAsInt("SERVER_SHUTDOWN_TIMEOUT_SEC", 5)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(shutdownSec)*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	return nil
}

func makeRouter(db *sql.DB, scoreSvc *service.ScoreService, oauthCfg *oauth.Config, opts Options) *http.ServeMux {
	scoreRL := newIPRateLimiter(
		config.GetEnvAsInt("SCORE_RATE_LIMIT", 60),
		3600, // 1 hour window
	)
	oauthRL := newIPRateLimiter(
		config.GetEnvAsInt("OAUTH_RATE_LIMIT", 20),
		60,
	)

	requireAny := middleware.RequireAnyAuth(db)
	requireSession := middleware.RequireAuth(db, "/auth/github")

	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Public
	mux.HandleFunc("GET /{$}", landingHandler(opts))
	mux.HandleFunc("GET /health", health.Handler())
	mux.Handle("GET /auth/github", oauthRL.wrap(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))

	// Score — accepts any auth (token, session, or none)
	mux.Handle("GET /api/v1/score/{username}", scoreRL.wrap(requireAny(scoreHandler(db, scoreSvc))))

	// Token management — requires session auth (UI only)
	mux.Handle("POST /api/v1/token", requireSession(createTokenHandler(db)))
	mux.Handle("GET /api/v1/token", requireSession(listTokensHandler(db)))
	mux.Handle("DELETE /api/v1/token/{id}", requireSession(revokeTokenHandler(db)))

	// Session
	mux.Handle("POST /auth/signout", requireSession(signoutHandler(db)))

	// GitHub App webhook
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if webhookSecret != "" {
		mux.HandleFunc("POST /webhook/github", webhookHandler(db, webhookSecret))
	}

	return mux
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
