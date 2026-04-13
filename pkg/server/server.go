package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
)

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

	token := config.GetEnv("GITHUB_TOKEN", "")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN is required")
	}

	gh := ghclient.NewPATClient(ctx, token)
	scoreSvc := service.NewScoreService(gh, nil)

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

func makeRouter(db *sql.DB, scoreSvc *service.ScoreService, oauthCfg *oauth.Config, _ Options) *http.ServeMux {
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

	// Public
	mux.HandleFunc("GET /health", health.Handler())
	mux.Handle("GET /auth/github", oauthRL.wrap(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))

	// Score — accepts any auth (token, session, or none)
	mux.Handle("GET /api/v1/score/{username}", scoreRL.wrap(requireAny(scoreHandler(scoreSvc))))

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
