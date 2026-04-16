package middleware

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devtrace/pkg/tenant"
)

// IsAdmin checks if the given username is in the DEVTRACE_ADMIN_USERS env var.
func IsAdmin(username string) bool {
	raw := os.Getenv("DEVTRACE_ADMIN_USERS")
	if raw == "" {
		return false
	}
	for u := range strings.SplitSeq(raw, ",") {
		if strings.TrimSpace(u) == username {
			return true
		}
	}
	return false
}

// RequireAdmin validates the session and checks the admin user list.
// Returns 404 for unauthenticated users and non-admins (hides route existence).
func RequireAdmin(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil {
				http.NotFound(w, r)
				return
			}
			tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
			if err != nil || tn == nil || !IsAdmin(tn.Username) {
				slog.Warn("admin access denied",
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
					"username", tenantUsername(tn),
				)
				http.NotFound(w, r)
				return
			}
			ctx := WithTenantContext(r.Context(), tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

const anonymousUser = "<anonymous>"

func tenantUsername(tn *tenant.Tenant) string {
	if tn == nil {
		return anonymousUser
	}
	return tn.Username
}
