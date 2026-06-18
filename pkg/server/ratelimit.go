package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
)

type visitor struct {
	count   int
	resetAt time.Time
}

type ipRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    int
	window   time.Duration
	stop     chan struct{}
}

func newIPRateLimiter(limit int, windowSec int) *ipRateLimiter {
	rl := &ipRateLimiter{
		visitors: make(map[string]*visitor),
		limit:    limit,
		window:   time.Duration(windowSec) * time.Second,
		stop:     make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

func (rl *ipRateLimiter) cleanup() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if now.After(v.resetAt) {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stop:
			return
		}
	}
}

func (rl *ipRateLimiter) close() {
	close(rl.stop)
}

func (rl *ipRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, ok := rl.visitors[ip]
	if !ok || now.After(v.resetAt) {
		rl.visitors[ip] = &visitor{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	v.count++
	return v.count <= rl.limit
}

// allowWithLimit works like allow but uses the given limit instead of rl.limit.
// This lets callers apply dynamic per-key thresholds (e.g. plan-based limits).
func (rl *ipRateLimiter) allowWithLimit(key string, limit int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, ok := rl.visitors[key]
	if !ok || now.After(v.resetAt) {
		rl.visitors[key] = &visitor{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	v.count++
	return v.count <= limit
}

func (rl *ipRateLimiter) retryAfter(ip string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[ip]
	if !ok {
		return 0
	}
	remaining := time.Until(v.resetAt).Seconds()
	if remaining < 1 {
		return 1
	}
	return int(remaining) + 1
}

func (rl *ipRateLimiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !rl.allow(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", rl.retryAfter(ip)))
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{tmplErrorKey: msgRateLimitExceeded})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// burstLimitFor returns the per-60s burst cap derived from a plan's
// hourly cap. Approximately RateLimitPerHour/12 with a floor of 5 so
// the Free tier still gets a usable burst, and a hard cap of 100 to
// match the underlying limiter's ceiling configuration. Tunable from
// the plan only; no separate env var.
func burstLimitFor(perHour int) int {
	burst := perHour / 12
	if burst < 5 {
		burst = 5
	}
	if burst > 100 {
		burst = 100
	}
	return burst
}

// authAwareRateLimit returns middleware that applies different rate limits based
// on authentication status. Unauthenticated requests are limited per-IP using
// unauthRL. Authenticated requests are limited per-tenant using authRL with
// the tenant's plan-based RateLimitPerHour, plus an optional burst limiter
// (burstRL) gated by BURST_LIMIT_ENABLED that caps requests-per-60s so a
// single tenant cannot drain the GitHub Search quota in a 5-minute window.
//
// When htmlMode is true, 429 responses render the ratelimit.html template.
// When false, 429 responses return JSON with Retry-After header. If both
// limiters deny in the same request, the shorter Retry-After is returned
// so the client can recover as soon as the more restrictive window resets.
func authAwareRateLimit(unauthRL, authRL, burstRL *ipRateLimiter, burstEnabled bool, htmlMode bool, version string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tn := middleware.TenantFromContext(r.Context())

			var allowed bool
			var key string
			var burstDenied bool

			if tn == nil {
				// Unauthenticated: limit per IP
				key = extractIP(r)
				allowed = unauthRL.allow(key)
			} else {
				// Authenticated: limit per tenant using plan rate.
				key = tn.ID
				p, ok := plan.Get(tn.Plan)
				if !ok {
					p = plan.Free()
				}
				allowed = authRL.allowWithLimit(key, p.RateLimitPerHour)
				// Burst gate (per-60s window). Evaluated independently so
				// even a tenant well within their hourly cap cannot fire
				// 700 calls in five minutes and drain the Search quota.
				if allowed && burstEnabled && burstRL != nil {
					burstAllowed := burstRL.allowWithLimit(key, burstLimitFor(p.RateLimitPerHour))
					if !burstAllowed {
						allowed = false
						burstDenied = true
					}
				}
			}

			if !allowed {
				tier := "unauth"
				if tn != nil {
					tier = "auth"
				}
				slog.Info("rate limit exceeded", "tier", tier, "path", r.URL.Path, "burst", burstDenied)

				var retryAfter int
				switch {
				case tn == nil:
					retryAfter = unauthRL.retryAfter(key)
				case burstDenied:
					retryAfter = burstRL.retryAfter(key)
				default:
					retryAfter = authRL.retryAfter(key)
				}

				if htmlMode {
					w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
					w.WriteHeader(http.StatusTooManyRequests)
					renderTemplate(w, "ratelimit.html", pageData{Title: "Rate Limit"})
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"version":     version,
					tmplErrorKey:  msgRateLimitExceeded,
					"retry_after": retryAfter,
					"sign_in_url": authGitHubPath,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractIP returns the client IP. Trusts X-Forwarded-For only when
// TRUST_PROXY=true (Cloud Run, load balancer). Otherwise uses RemoteAddr.
func extractIP(r *http.Request) string {
	if trustProxy && r.Header.Get("X-Forwarded-For") != "" {
		if ip := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-For"), ",", 2)[0]); ip != "" {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var trustProxy = os.Getenv("TRUST_PROXY") == "true"
