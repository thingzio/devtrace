package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
)

const (
	csrfCookieSecure = "__Host-csrf"
	csrfCookiePlain  = "csrf"
	csrfFormField    = "csrf_token"
	csrfTokenBytes   = 32
)

type csrfContextKey struct{}

var csrfCookieName string

func init() {
	csrfCookieName = csrfCookieNameFor(secure)
}

func csrfCookieNameFor(isSecure bool) string {
	if isSecure {
		return csrfCookieSecure
	}
	return csrfCookiePlain
}

// CSRFCookieName returns the CSRF cookie name for the current environment.
func CSRFCookieName() string {
	return csrfCookieName
}

// GenerateCSRFToken returns a cryptographically random hex-encoded token.
func GenerateCSRFToken() (string, error) {
	b := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating csrf token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SetCSRFCookie writes the CSRF token cookie to the response.
// The path parameter scopes the cookie to the given URL prefix (e.g. "/admin", "/").
//
// HttpOnly is intentionally false — the double-submit CSRF pattern requires
// JavaScript to read the cookie value to inject it as a hidden form field.
// SameSite=Strict provides the security boundary instead of HttpOnly.
func SetCSRFCookie(w http.ResponseWriter, token, path string) {
	// G124 acknowledged: HttpOnly: false is intentional for double-submit
	// CSRF; Secure is env-driven; SameSite=Strict provides the guarantee.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: HttpOnly:false is intentional for double-submit CSRF
		Name:     CSRFCookieName(),
		Value:    token,
		Path:     path,
		Secure:   secure,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	})
}

// InjectCSRF generates a CSRF token, sets it as a cookie at Path="/",
// and stores it in the request context. Apply to all authenticated GET
// routes so that POST forms (sign-out, TOS accept, etc.) have a valid token.
// Client-side JS reads the cookie and injects hidden csrf_token fields.
func InjectCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only inject on GET (the pages that render forms).
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		token, err := GenerateCSRFToken()
		if err != nil {
			slog.Error("csrf: generate token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		SetCSRFCookie(w, token, "/")
		ctx := context.WithValue(r.Context(), csrfContextKey{}, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CSRFTokenFromContext returns the CSRF token stored by InjectCSRF middleware.
func CSRFTokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(csrfContextKey{}).(string); ok {
		return v
	}
	return ""
}

// CSRFTokenFromRequest reads the CSRF token from the cookie.
func CSRFTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(CSRFCookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

// ValidateCSRF is middleware that rejects POST requests where the form field
// csrf_token does not match the csrf cookie (double-submit cookie pattern).
// GET/HEAD/OPTIONS requests pass through unchanged.
func ValidateCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		cookieToken := CSRFTokenFromRequest(r)
		// Limit body before parsing form to prevent memory exhaustion.
		// Handlers may set their own limit; this is a safe ceiling.
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		formToken := r.FormValue(csrfFormField)

		if cookieToken == "" || formToken == "" {
			slog.Warn("csrf: missing token",
				"path", r.URL.Path,
				"remote", r.RemoteAddr,
				"has_cookie", cookieToken != "",
				"has_form", formToken != "",
			)
			http.Error(w, "Forbidden: missing CSRF token", http.StatusForbidden)
			return
		}

		if subtle.ConstantTimeCompare([]byte(cookieToken), []byte(formToken)) != 1 {
			slog.Warn("csrf: token mismatch",
				"path", r.URL.Path,
				"remote", r.RemoteAddr,
			)
			http.Error(w, "Forbidden: invalid CSRF token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
