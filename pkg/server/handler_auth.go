package server

import (
	"crypto/subtle"
	"database/sql"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/oauth"
	"github.com/thingzio/devtrace/pkg/tenant"
)

const (
	sessionTTL       = 7 * 24 * time.Hour
	oauthStateCookie = "oauth_state"
)

func oauthStartHandler(cfg *oauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authURL, state, err := oauth.BuildAuthURL(cfg)
		if err != nil {
			slog.Error("building oauth URL", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124 false positive: middleware.IsSecure() is dynamic
			Name:     oauthStateCookie,
			Value:    state,
			Path:     "/",
			MaxAge:   600,
			Secure:   middleware.IsSecure(),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func oauthCallbackHandler(db *sql.DB, cfg *oauth.Config) http.HandlerFunc {
	clearAndRedirect := func(w http.ResponseWriter, r *http.Request, msg string) {
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124 false positive: middleware.IsSecure() is dynamic
			Name: oauthStateCookie, Value: "", MaxAge: -1, Path: "/",
			HttpOnly: true, Secure: middleware.IsSecure(), SameSite: http.SameSiteLaxMode,
		})
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Validate state
		stateCookie, err := r.Cookie(oauthStateCookie)
		if err != nil || subtle.ConstantTimeCompare(
			[]byte(stateCookie.Value),
			[]byte(r.URL.Query().Get("state")),
		) != 1 {
			slog.Warn("oauth state mismatch")
			clearAndRedirect(w, r, "auth_expired")
			return
		}
		// Clear state cookie.
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124 false positive: middleware.IsSecure() is dynamic
			Name: oauthStateCookie, Value: "", MaxAge: -1, Path: "/",
			HttpOnly: true, Secure: middleware.IsSecure(), SameSite: http.SameSiteLaxMode,
		})

		// 2. Exchange code for token
		code := r.URL.Query().Get("code")
		accessToken, err := oauth.ExchangeCode(r.Context(), cfg, code)
		if err != nil {
			// Handle already-consumed code (browser double-click)
			if sessionCookie, cookieErr := r.Cookie(middleware.SessionCookieName()); cookieErr == nil {
				if _, valErr := tenant.ValidateSession(r.Context(), db, sessionCookie.Value); valErr == nil {
					http.Redirect(w, r, "/dashboard", http.StatusFound)
					return
				}
			}
			slog.Error("oauth exchange", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

		// 3. Fetch user profile
		user, err := oauth.FetchUser(r.Context(), cfg, accessToken)
		if err != nil {
			slog.Error("fetch user", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

		// 4. Upsert tenant
		tn, err := tenant.UpsertTenant(r.Context(), db, user.ID, user.Login, user.Email, user.AvatarURL, user.Name, user.Company, user.Location, user.Bio)
		if err != nil {
			slog.Error("upsert tenant", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}
		slog.Info("user signed in", "username", tn.Username, "tenant_id", tn.ID)

		// 5. Create session
		sessionToken, err := tenant.CreateSession(r.Context(), db, tn.ID, sessionTTL)
		if err != nil {
			slog.Error("create session", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}
		middleware.SetSessionCookie(w, sessionToken, int(sessionTTL.Seconds()))

		// 6. Redirect
		if tn.ToSAcceptedAt == nil {
			http.Redirect(w, r, "/tos", http.StatusFound)
			return
		}

		// Nudge users without an app installation to settings.
		installs, _ := tenant.GetActiveInstallations(r.Context(), db, tn.ID)
		if len(installs) == 0 {
			http.Redirect(w, r, "/settings?msg=install_app", http.StatusFound)
			return
		}

		http.Redirect(w, r, "/dashboard", http.StatusFound)
	}
}

func signoutHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(middleware.SessionCookieName()); err == nil {
			if derr := tenant.DestroySession(r.Context(), db, cookie.Value); derr != nil {
				slog.Debug("destroy session", "error", derr)
			}
		}
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}
