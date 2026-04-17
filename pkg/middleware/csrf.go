package middleware

import (
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
func SetCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName(),
		Value:    token,
		Path:     "/admin",
		Secure:   secure,
		HttpOnly: false, // JS must be able to read if needed; form field is the primary mechanism
		SameSite: http.SameSiteStrictMode,
	})
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
