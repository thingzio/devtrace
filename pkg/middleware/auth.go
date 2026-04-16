package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devtrace/pkg/tenant"
)

type contextKey string

const tenantContextKey contextKey = "tenant"

const (
	cookieSecure = "__Host-session"
	cookiePlain  = "session"
)

var (
	secure     bool
	cookieName string
)

func init() {
	secure = strings.HasPrefix(os.Getenv("BASE_URL"), "https://")
	cookieName = cookieNameFor(secure)
}

func cookieNameFor(isSecure bool) string {
	if isSecure {
		return cookieSecure
	}
	return cookiePlain
}

func SessionCookieName() string {
	return cookieName
}

// RequireAuth validates session cookie, redirects to loginURL on failure. For UI routes.
func RequireAuth(db *sql.DB, loginURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil {
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}
			tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
			if err != nil {
				slog.Debug("invalid session", "error", err)
				ClearSessionCookie(w)
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}
			if tn.Status == tenant.StatusSuspended {
				ClearSessionCookie(w)
				http.Redirect(w, r, loginURL+"?error=suspended", http.StatusFound)
				return
			}
			ctx := context.WithValue(r.Context(), tenantContextKey, tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAPIToken validates Authorization: Bearer header. For API routes.
// Returns 401 JSON on failure.
func RequireAPIToken(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
				return
			}
			tn, err := tenant.ValidateAPIToken(r.Context(), db, token)
			if err != nil {
				slog.Debug("invalid api token", "error", err)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api token"})
				return
			}
			if tn.Status == tenant.StatusSuspended {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "account suspended"})
				return
			}
			ctx := context.WithValue(r.Context(), tenantContextKey, tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAnyAuth tries API token first, then session cookie.
// If neither is present, allows through as unauthenticated (tenant will be nil in context).
// The handler checks TenantFromContext and adjusts response accordingly.
func RequireAnyAuth(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try API token first
			if token := extractBearerToken(r); token != "" {
				if tn, err := tenant.ValidateAPIToken(r.Context(), db, token); err == nil {
					if tn.Status == tenant.StatusSuspended {
						writeJSON(w, http.StatusForbidden, map[string]string{"error": "account suspended"})
						return
					}
					ctx := context.WithValue(r.Context(), tenantContextKey, tn)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Invalid token = 401 (they tried to auth and failed)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api token"})
				return
			}

			// Try session cookie
			if cookie, err := r.Cookie(SessionCookieName()); err == nil {
				if tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value); err == nil {
					if tn.Status == tenant.StatusSuspended {
						writeJSON(w, http.StatusForbidden, map[string]string{"error": "account suspended"})
						return
					}
					ctx := context.WithValue(r.Context(), tenantContextKey, tn)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// No auth - allow through as unauthenticated (handler decides behavior)
			next.ServeHTTP(w, r)
		})
	}
}

func TenantFromContext(ctx context.Context) *tenant.Tenant {
	if t, ok := ctx.Value(tenantContextKey).(*tenant.Tenant); ok {
		return t
	}
	return nil
}

func WithTenantContext(ctx context.Context, tn *tenant.Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, tn)
}

func SetSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName(),
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	SetSessionCookie(w, "", -1)
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

// writeJSON is intentionally duplicated from server.writeJSON because the
// middleware package cannot import server (it would create a circular dependency).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
