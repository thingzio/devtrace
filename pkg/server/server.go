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
	"runtime/debug"
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

	// Compliance status values (duplicated from compliance package to avoid
	// import in package-level var init).
	statusPresent = "present"
	statusAbsent  = "absent"
)

var templateFuncs = template.FuncMap{
	"comma": func(n int) string {
		if n == 0 {
			return "Unlimited"
		}
		neg := ""
		if n < 0 {
			neg = "-"
			n = -n
		}
		s := fmt.Sprintf("%d", n)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		return neg + s
	},
	"fmtnum": func(v any) string {
		// Accept int / int64 / int32 / etc. so templates that surface
		// counts of varying width (e.g., model.StackOverflow.Reputation
		// is int64; postgres row counts are int) all use the same
		// thousands-separator formatter without per-call conversions.
		var n int64
		switch x := v.(type) {
		case int:
			n = int64(x)
		case int64:
			n = x
		case int32:
			n = int64(x)
		default:
			return fmt.Sprintf("%v", v)
		}
		neg := ""
		if n < 0 {
			neg = "-"
			n = -n
		}
		s := fmt.Sprintf("%d", n)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		return neg + s
	},
	"sub":      func(a, b int) int { return a - b },
	"mul":      func(a, b float64) float64 { return a * b },
	"int":      func(n int64) int { return int(n) },
	"prettify": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
	"flagDesc": func(s string) string {
		switch s {
		case "young_account":
			return "Account created less than 30 days ago"
		case "high_fork_ratio":
			return "Over 80% of repos are forks, suggesting little original work"
		case "empty_profile":
			return "No bio, company, location, or website — minimal identity signal"
		case "no_reviews":
			return "No code reviews in the last 30 days — limited peer interaction"
		case "no_consistency":
			return "No sustained activity pattern detected across weeks"
		case "no_verified_commits":
			return "None of the contributor's commits are cryptographically signed"
		default:
			return ""
		}
	},
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
	"complianceIcon": func(status string) string {
		switch status {
		case statusPresent:
			return "\u2713" // ✓
		case statusAbsent:
			return "\u2717" // ✗
		default:
			return "\u2014" // —
		}
	},
	"complianceClass": func(status string) string {
		switch status {
		case statusPresent:
			return cssInterpGreen
		case statusAbsent:
			return cssInterpAmber
		default:
			return "interp-muted"
		}
	},
}

func init() {
	simplePages := []string{
		"admin.html", "admin_tokens.html", "admin_tenants.html", "admin_tenant_detail.html", "admin_metrics.html",
		"scorecard.html", "tos.html", "settings.html",
		"stub.html", "ratelimit.html", "changelog.html", "compliance.html",
	}
	// +3: simplePages + landing + help + home
	pageTemplates = make(map[string]*template.Template, len(simplePages)+3)
	for _, p := range simplePages {
		pageTemplates[p] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html", "templates/"+p))
	}
	// Pages that include the plans table partial.
	for _, p := range []string{"landing.html", "help.html"} {
		pageTemplates[p] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html", "templates/plans_table.html", "templates/"+p))
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
	defer func() {
		// Drain fire-and-forget goroutines (persistScore, etc.) before
		// closing the pool. Without this, a goroutine in flight when
		// the process receives SIGTERM races against Close and panics
		// on "use of closed pool". Bounded wait so a stuck goroutine
		// doesn't block shutdown indefinitely.
		drainCtx, drainCancel := context.WithTimeout(context.Background(),
			time.Duration(config.GetEnvAsInt("BACKGROUND_DRAIN_TIMEOUT_SEC", 10))*time.Second)
		defer drainCancel()
		if waitErr := store.WaitBackground(drainCtx); waitErr != nil {
			slog.Warn("background drain timed out", "error", waitErr)
		}
		_ = store.Close()
	}()

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
		pool.SetRefreshCh(installNotify)

		// Start background token refresh. When tokens are rotated out of
		// the pool, drop their cached *gh.Client from PoolClient so the
		// underlying TLS / connection pool doesn't pin a now-invalid token.
		appCfg, _ := tenant.LoadGitHubAppConfig()
		refreshStop := ghclient.StartPoolRefresh(ctx, pool, func(ctx context.Context) ([]ghclient.PoolEntry, error) {
			return mintPoolEntries(ctx, store, appCfg)
		}, installNotify, 0, pc.InvalidateTokens)
		defer refreshStop()
	}

	// Start background operations (disabled by default for local dev).
	if config.GetEnvBool("ENABLE_BACKGROUND_OPS") {
		scorerStop := background.StartBackgroundScorer(ctx, store, ghClient, opts.Version)
		defer scorerStop()

		quotaStop := background.StartQuotaSampler(ctx, store, pool)
		defer quotaStop()

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

	scoreSvc := service.NewScoreService(ghClient, opts.Version)
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
		Handler:           recoverPanics(securityHeaders(mux)),
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
	// Burst limiter: per-tenant 60s window so a tenant cannot drain the
	// GitHub Search quota inside their hourly cap. Ceiling is generous;
	// per-plan threshold derived in burstLimitFor. Default on; disable
	// with BURST_LIMIT_ENABLED=false.
	burstRL := newIPRateLimiter(100, 60)
	burstEnabled := config.GetEnvBoolDefault("BURST_LIMIT_ENABLED", true)
	oauthRL := newIPRateLimiter(
		config.GetEnvAsInt("OAUTH_RATE_LIMIT", 20),
		60,
	)

	requireAny := middleware.RequireAnyAuth(db)
	requireSession := middleware.RequireAuth(db, authGitHubPath)
	csrf := middleware.InjectCSRF

	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Public
	mux.Handle("GET /{$}", requireAny(landingHandler(opts)))
	mux.HandleFunc("GET /health", health.Handler())
	mux.HandleFunc("GET /changelog", changelogPageHandler(opts))
	mux.HandleFunc("GET /compliance", compliancePageHandler(opts))
	mux.HandleFunc("GET /help", helpPageHandler(db, opts))
	mux.HandleFunc("POST /help/contact", helpContactHandler(db, opts))
	mux.Handle("GET /auth/github", oauthRL.wrap(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))

	// Dashboard — requires session
	mux.Handle("GET /dashboard", requireSession(csrf(dashboardHandler(store, opts))))
	mux.Handle("GET /dashboard/events.json", requireSession(dashboardEventsHandler(store)))

	// Settings + ToS — requires session
	mux.Handle("GET /settings", requireSession(csrf(settingsHandler(store, opts))))
	mux.Handle("GET /tos", requireAny(csrf(tosPageHandler(db, opts))))
	mux.Handle("POST /tos/accept", requireSession(middleware.ValidateCSRF(tosAcceptHandler(store))))

	// Watchlist management — requires session
	mux.Handle("POST /settings/watchlist", requireSession(middleware.ValidateCSRF(addWatchlistHandler(store))))
	mux.Handle("POST /settings/watchlist/{id}/delete", requireSession(middleware.ValidateCSRF(deleteWatchlistHandler(store))))
	mux.Handle("POST /settings/watchlist/{id}/toggle-email", requireSession(middleware.ValidateCSRF(toggleWatchlistEmailHandler(store))))

	// Digest unsubscribe — public, HMAC-validated (no auth required)
	mux.HandleFunc("GET /digest/unsubscribe", digestUnsubscribeHandler(store))

	// Score card page — accepts any auth, rate-limited (HTML 429)
	mux.Handle("GET /score/{username}", requireAny(csrf(authAwareRateLimit(unauthRL, authRL, burstRL, burstEnabled, true, opts.Version)(scorecardHandler(store, scoreSvc, opts)))))

	// Score API — accepts any auth, rate-limited (JSON 429)
	mux.Handle("GET /api/v1/score/{username}", requireAny(authAwareRateLimit(unauthRL, authRL, burstRL, burstEnabled, false, opts.Version)(scoreHandler(db, store, scoreSvc))))

	// Score history API (trend chart data) — rate-limited
	mux.Handle("GET /api/v1/score/{username}/history", requireAny(authAwareRateLimit(unauthRL, authRL, burstRL, burstEnabled, false, opts.Version)(historyHandler(store))))

	// Token management — requires session auth (UI only)
	mux.Handle("POST /api/v1/token", requireSession(createTokenHandler(db)))
	mux.Handle("GET /api/v1/token", requireSession(listTokensHandler(db)))
	mux.Handle("DELETE /api/v1/token/{id}", requireSession(revokeTokenHandler(db)))

	// Session
	mux.Handle("POST /auth/signout", requireSession(middleware.ValidateCSRF(signoutHandler(db))))

	// GitHub App webhook
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if webhookSecret != "" {
		mux.HandleFunc("POST /webhook/github", webhookHandler(db, store, webhookSecret, installNotify))
	}

	// Admin — session auth + admin user list, returns 404 for non-admins
	requireAdmin := middleware.RequireAdmin(db)
	mux.Handle("GET /admin", requireAdmin(adminDashboardHandler(store, opts)))
	mux.Handle("GET /admin/", requireAdmin(adminDashboardHandler(store, opts)))
	mux.Handle("GET /admin/tokens", requireAdmin(adminTokensHandler(pool, opts)))
	if store != nil {
		mux.Handle("GET /admin/tokens/quota-history", requireAdmin(adminTokenQuotaHistoryHandler(store, opts)))
	}
	mux.Handle("GET /admin/tenants", requireAdmin(adminTenantsHandler(store, opts)))
	mux.Handle("GET /admin/tenant/{username}", requireAdmin(adminTenantDetailHandler(store, opts)))
	mcfg := newAdminMetricsConfig()
	mux.Handle("GET /admin/metrics", requireAdmin(adminMetricsHandler(store, mcfg, opts)))
	mux.Handle("POST /admin/digest/test", requireAdmin(middleware.ValidateCSRF(adminSendTestDigestHandler(store))))
	mux.Handle("POST /admin/tenant/{username}/plan", requireAdmin(middleware.ValidateCSRF(adminUpdatePlanFormHandler(db))))
	mux.Handle("POST /admin/tenant/{username}/status", requireAdmin(middleware.ValidateCSRF(adminUpdateStatusFormHandler(db))))
	mux.Handle("POST /admin/tenant/{username}/delete", requireAdmin(middleware.ValidateCSRF(adminDeleteTenantHandler(db))))

	cleanup := func() {
		scoreSvc.Close()
		unauthRL.close()
		authRL.close()
		burstRL.close()
		oauthRL.close()
	}
	return mux, cleanup
}

// recoverPanics is a top-level middleware that turns a handler panic into
// a 500 instead of crashing the process. Without it, a single nil-deref in
// any handler drops every in-flight request, every background worker, and
// every queued scoring job — Cloud Run respawns but the disruption is
// avoidable. Logs the panic with stack so the failure is still actionable.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is the documented escape hatch for
			// handlers that want to abruptly terminate the connection
			// (e.g. websocket hijack); preserve the original panic so
			// the server framework handles it normally. Compare via
			// errors.Is in case anything wrapped it on the way up.
			if errAbort, ok := rec.(error); ok && errors.Is(errAbort, http.ErrAbortHandler) {
				panic(rec)
			}
			slog.Error("handler panic",
				"path", r.URL.Path,
				"method", r.Method,
				"panic", rec,
				"stack", string(debug.Stack()),
			)
			// Best-effort 500. If the response was already partially
			// written, http.Error will fail silently — that's fine; the
			// log entry is the durable record.
			http.Error(w, "internal error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	secure := strings.HasPrefix(os.Getenv("BASE_URL"), "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// CSP3: split style-src into the strict family (locks down <style>
		// blocks and <link rel=stylesheet>) and style-src-attr (allows
		// `style="..."` attributes on elements). Templates render dynamic
		// values into element-level style attributes (progress widths,
		// flexible grids); static styles live in app.css. The
		// attribute-only relaxation removes the broad XSS surface that
		// `unsafe-inline` on style-src would have created.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; "+
				"style-src 'self'; style-src-attr 'unsafe-inline'; "+
				"img-src 'self' https://avatars.githubusercontent.com data:; "+
				"connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if secure {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		next.ServeHTTP(w, r)
	})
}
