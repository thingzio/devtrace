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
	"github.com/thingzio/devtrace/pkg/ingest"
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

// CSS classes for AI sensing interpretation badges.
const (
	cssInterpGreen = "interp-green"
	cssInterpAmber = "interp-amber"
	cssInterpRed   = "interp-red"
)

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
	"velocityLabel": func(v float64) string {
		if v > 5.0 {
			return "suspicious"
		}
		if v > 2.0 {
			return "elevated"
		}
		return "normal"
	},
	"velocityClass": func(v float64) string {
		if v > 5.0 {
			return cssInterpRed
		}
		if v > 2.0 {
			return cssInterpAmber
		}
		return cssInterpGreen
	},
	"hourLabel": func(h int) string {
		if h < 8 {
			return "narrow"
		}
		if h > 16 {
			return "wide"
		}
		return "typical"
	},
	"hourClass": func(h int) string {
		if h < 8 {
			return cssInterpAmber
		}
		if h > 16 {
			return cssInterpAmber
		}
		return cssInterpGreen
	},
	"burstLabel": func(v float64) string {
		if v > 5.0 {
			return "volatile"
		}
		if v > 2.0 {
			return "bursty"
		}
		return "steady"
	},
	"burstClass": func(v float64) string {
		if v > 5.0 {
			return cssInterpRed
		}
		if v > 2.0 {
			return cssInterpAmber
		}
		return cssInterpGreen
	},
	"syntheticClass": func(flags int) string {
		if flags >= 3 {
			return "badge-red"
		}
		if flags >= 1 {
			return "badge-amber"
		}
		return "badge-green"
	},
}

func init() {
	simplePages := []string{"admin.html", "landing.html", "scorecard.html", "tos.html", "settings.html", "stub.html", "help.html", "ratelimit.html", "changelog.html"}
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

	installNotify := make(chan struct{}, 1)

	var pool *ghclient.TokenPool
	if pc, ok := ghClient.(*ghclient.PoolClient); ok {
		pool = pc.Pool()

		// Start background token refresh.
		appCfg, _ := tenant.LoadGitHubAppConfig()
		refreshStop := ghclient.StartPoolRefresh(ctx, pool, func(ctx context.Context) ([]ghclient.PoolEntry, error) {
			return mintPoolEntries(ctx, store, appCfg)
		}, installNotify, 0)
		defer refreshStop()
	}

	// Start background operations (disabled by default for local dev).
	if config.GetEnvBool("ENABLE_BACKGROUND_OPS") {
		scorerStop := background.StartBackgroundScorer(ctx, store, ghClient, opts.Version)
		defer scorerStop()

		backfillDays := config.GetEnvAsInt("GHARCHIVE_BACKFILL_DAYS", 0)
		ingestStop := background.StartIngestLoop(ctx, func(ctx context.Context) error {
			if backfillDays > 0 {
				if err := ingest.Backfill(ctx, store, backfillDays); err != nil {
					slog.Error("backfill failed", "error", err)
				}
			}
			return ingest.Run(ctx, store)
		}, 0)
		defer ingestStop()
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

	mux, routerCleanup := makeRouter(store, scoreSvc, pool, oauthCfg, opts, installNotify)
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

	if appErr == nil && store != nil {
		entries, err := mintPoolEntries(ctx, store, appCfg)
		if err == nil && len(entries) > 0 {
			pool := ghclient.NewTokenPoolFromEntries(entries)
			slog.Info("using token pool GitHub client", "tokens", pool.Size())
			return ghclient.NewPoolClient(pool), nil
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

	token := config.GetEnv("GITHUB_TOKEN", "")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN or GitHub App config required")
	}
	slog.Info("using PAT GitHub client")
	return ghclient.NewPATClient(ctx, token), nil
}

// mintPoolEntries loads all active installations, mints tokens, and returns pool entries.
func mintPoolEntries(ctx context.Context, store *postgres.Store, appCfg *tenant.GitHubAppConfig) ([]ghclient.PoolEntry, error) {
	installations, err := tenant.GetAllActiveInstallations(ctx, store.DB(), appCfg.AppID)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}

	var entries []ghclient.PoolEntry
	for _, inst := range installations {
		tok, err := tenant.MintInstallationToken(ctx, appCfg, inst.InstallationID)
		if err != nil {
			slog.Error("skip installation token", "installation_id", inst.InstallationID, "error", err)
			continue
		}
		entries = append(entries, ghclient.PoolEntry{
			InstallationID: inst.InstallationID,
			Label:          inst.TargetLogin,
			Token:          tok.Token,
			ExpiresAt:      tok.ExpiresAt,
		})
		slog.Debug("minted installation token", "installation_id", inst.InstallationID, "org", inst.TargetLogin)
	}

	if pat := config.GetEnv("GITHUB_TOKEN", ""); pat != "" {
		entries = append(entries, ghclient.PoolEntry{
			Label: "PAT",
			Token: pat,
		})
	}

	return entries, nil
}

func makeRouter(store *postgres.Store, scoreSvc *service.ScoreService, pool *ghclient.TokenPool, oauthCfg *oauth.Config, opts Options, installNotify chan<- struct{}) (*http.ServeMux, func()) {
	var db *sql.DB
	if store != nil {
		db = store.DB()
	}
	unauthRL := newIPRateLimiter(
		config.GetEnvAsInt("UNAUTH_RATE_LIMIT", 1),
		config.GetEnvAsInt("UNAUTH_RATE_WINDOW", 60),
	)
	authRL := newIPRateLimiter(1000, 3600) // ceiling; actual limit per plan via allowWithLimit
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
	mux.HandleFunc("GET /changelog", changelogPageHandler(opts))
	mux.HandleFunc("GET /help", helpPageHandler(db, opts))
	mux.HandleFunc("POST /help/contact", helpContactHandler(db, opts))
	mux.Handle("GET /auth/github", oauthRL.wrap(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))

	// Dashboard — requires session
	mux.Handle("GET /dashboard", requireSession(dashboardHandler(store, opts)))

	// Settings + ToS — requires session
	mux.Handle("GET /settings", requireSession(settingsHandler(store, opts)))
	mux.Handle("GET /tos", requireAny(tosPageHandler(db, opts)))
	mux.Handle("POST /tos/accept", requireSession(tosAcceptHandler(store)))

	// Score card page — accepts any auth, rate-limited (HTML 429)
	mux.Handle("GET /score/{username}", requireAny(authAwareRateLimit(unauthRL, authRL, true, opts.Version)(scorecardHandler(store, scoreSvc, opts))))

	// Score API — accepts any auth, rate-limited (JSON 429)
	mux.Handle("GET /api/v1/score/{username}", requireAny(authAwareRateLimit(unauthRL, authRL, false, opts.Version)(scoreHandler(db, store, scoreSvc))))

	// Score history API (trend chart data) — no rate limit, UI-only read
	mux.Handle("GET /api/v1/score/{username}/history", requireAny(historyHandler(store)))

	// Token management — requires session auth (UI only)
	mux.Handle("POST /api/v1/token", requireSession(createTokenHandler(db)))
	mux.Handle("GET /api/v1/token", requireSession(listTokensHandler(db)))
	mux.Handle("DELETE /api/v1/token/{id}", requireSession(revokeTokenHandler(db)))

	// Session
	mux.Handle("POST /auth/signout", requireSession(signoutHandler(db)))

	// GitHub App webhook
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if webhookSecret != "" {
		mux.HandleFunc("POST /webhook/github", webhookHandler(db, webhookSecret, installNotify))
	}

	// Admin — session auth + admin user list, returns 404 for non-admins
	requireAdmin := middleware.RequireAdmin(db)
	mux.Handle("GET /admin", requireAdmin(adminDashboardHandler(store, pool, opts)))
	mux.Handle("GET /admin/", requireAdmin(adminDashboardHandler(store, pool, opts)))
	mux.Handle("POST /admin/tenant/{username}/plan", requireAdmin(adminUpdatePlanFormHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/status", requireAdmin(adminUpdateStatusFormHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/delete", requireAdmin(adminDeleteTenantHandler(db)))

	cleanup := func() {
		unauthRL.close()
		authRL.close()
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
