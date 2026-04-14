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
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/background"
	"github.com/thingzio/devtrace/pkg/claude"
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
	"sub":      func(a, b int) int { return a - b },
	"mul":      func(a, b float64) float64 { return a * b },
	"int":      func(n int64) int { return int(n) },
	"prettify": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
}

func init() {
	simplePages := []string{"landing.html", "scorecard.html", "tos.html", "settings.html", "stub.html"}
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

	if migrateErr := store.Migrate(ctx); migrateErr != nil {
		return fmt.Errorf("run migrations: %w", migrateErr)
	}

	ghClient, err := buildGitHubClient(ctx, store)
	if err != nil {
		return fmt.Errorf("init GitHub client: %w", err)
	}

	// Start background operations (disabled by default for local dev).
	if config.GetEnvBool("ENABLE_BACKGROUND_OPS") {
		syncStop := background.StartDevPulseSync(ctx, store)
		defer syncStop()

		scorerStop := background.StartBackgroundScorer(ctx, store, ghClient, opts.Version)
		defer scorerStop()
	}

	scoreSvc := service.NewScoreService(ghClient, nil, opts.Version)
	if store != nil {
		scoreSvc.SetBehaviorStore(store)
	}

	claudeClient := claude.New()
	if claudeClient != nil {
		slog.Info("claude API enabled", "model", os.Getenv("ANTHROPIC_MODEL"))
		scoreSvc.SetClaudeClient(claudeClient)
	}

	oauthCfg := &oauth.Config{
		ClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURL:  config.GetEnv("BASE_URL", "http://localhost:8080") + "/auth/github/callback",
	}

	mux, routerCleanup := makeRouter(store, scoreSvc, oauthCfg, opts)
	defer routerCleanup()

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

// buildGitHubClient creates a GitHub API client, preferring a token pool
// from all active installations, falling back to a single PAT.
func buildGitHubClient(ctx context.Context, store *postgres.Store) (ghclient.Client, error) {
	appCfg, appErr := tenant.LoadGitHubAppConfig()

	// Try to build a token pool from all active GitHub App installations.
	if appErr == nil && store != nil {
		installations, err := tenant.GetAllActiveInstallations(ctx, store.DB(), appCfg.AppID)
		if err == nil && len(installations) > 0 {
			var tokens []string
			for _, inst := range installations {
				tok, err := tenant.MintInstallationToken(ctx, appCfg, inst.InstallationID)
				if err != nil {
					slog.Error("skip installation token", "installation_id", inst.InstallationID, "error", err)
					continue
				}
				tokens = append(tokens, tok.Token)
				slog.Debug("minted installation token", "installation_id", inst.InstallationID, "org", inst.TargetLogin)
			}

			// Also add GITHUB_TOKEN if set (dev/CI fallback).
			if pat := config.GetEnv("GITHUB_TOKEN", ""); pat != "" {
				tokens = append(tokens, pat)
			}

			if len(tokens) > 0 {
				pool := ghclient.NewTokenPool(tokens...)
				slog.Info("using token pool GitHub client", "tokens", pool.Size())
				return ghclient.NewPoolClient(pool), nil
			}
		}
	}

	// Single installation client (legacy path).
	if appErr == nil {
		instID := int64(config.GetEnvAsInt("GITHUB_APP_INSTALLATION_ID", 0))
		if instID > 0 {
			slog.Info("using GitHub App installation client", "app_id", appCfg.AppID, "installation_id", instID)
			return ghclient.NewInstallationClient(appCfg, instID), nil
		}
	}

	// Fall back to PAT.
	token := config.GetEnv("GITHUB_TOKEN", "")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN or GitHub App config required")
	}
	slog.Info("using PAT GitHub client")
	return ghclient.NewPATClient(ctx, token), nil
}

func makeRouter(store *postgres.Store, scoreSvc *service.ScoreService, oauthCfg *oauth.Config, opts Options) (*http.ServeMux, func()) {
	var db *sql.DB
	if store != nil {
		db = store.DB()
	}
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
	mux.Handle("GET /{$}", requireAny(landingHandler(opts)))
	mux.HandleFunc("GET /health", health.Handler())
	mux.HandleFunc("GET /changelog", stubPageHandler("Changelog"))
	mux.HandleFunc("GET /help", stubPageHandler("Help"))
	mux.Handle("GET /auth/github", oauthRL.wrap(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))

	// Dashboard — requires session
	mux.Handle("GET /dashboard", requireSession(dashboardHandler(store, opts)))

	// Settings + ToS — requires session
	mux.Handle("GET /settings", requireSession(settingsHandler(store, opts)))
	mux.HandleFunc("GET /tos", tosPageHandler())
	mux.Handle("POST /tos/accept", requireSession(tosAcceptHandler(store)))

	// Score card page — accepts any auth
	mux.Handle("GET /score/{username}", scoreRL.wrap(requireAny(scorecardHandler(store, scoreSvc, opts))))

	// Score API — accepts any auth (token, session, or none)
	mux.Handle("GET /api/v1/score/{username}", scoreRL.wrap(requireAny(scoreHandler(db, store, scoreSvc))))

	// Score history API (trend chart data)
	mux.Handle("GET /api/v1/score/{username}/history", scoreRL.wrap(requireAny(historyHandler(store))))

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

	cleanup := func() {
		scoreRL.close()
		oauthRL.close()
	}
	return mux, cleanup
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
