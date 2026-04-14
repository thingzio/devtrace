package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
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
