# Auth-Aware Rate Limiting Design

## Problem

Unauthenticated UI users currently get 60 requests/hour on score endpoints -- too
generous for anonymous traffic. We want to showcase the full service to entice
sign-ups while limiting free abuse. Authenticated users have plan-based quotas
defined but not enforced at the HTTP layer.

## Goals

- 1 request per minute per IP for unauthenticated users (UI and API)
- Plan-based rate limits for authenticated users (free=60/hr, starter=120/hr, pro=1000/hr)
- Clear UI message when rate-limited, with sign-in CTA
- JSON 429 response for API routes with `Retry-After` header
- Uniform policy across UI and API routes

## Approach: Auth-Aware Wrapper (Approach A)

### New middleware: `authAwareRateLimit`

```
authAwareRateLimit(unauthRL, authRL *ipRateLimiter, htmlMode bool) func(http.Handler) http.Handler
```

Sits after `RequireAnyAuth` in middleware chain. Checks tenant context:

- **nil (unauth):** `unauthRL` keyed by client IP, 1 req/60s
- **non-nil (auth):** `authRL` keyed by tenant ID, plan-based limit per hour

Response on deny:
- `htmlMode=true` -> renders `ratelimit.html` (static message + sign-in button)
- `htmlMode=false` -> JSON 429 with `Retry-After` and `sign_in_url`

### Changes to `ipRateLimiter`

New method: `allowWithLimit(key string, limit int) bool`

Same logic as `allow()` but accepts dynamic limit parameter. Used by auth-aware
middleware to pass `plan.RateLimitPerHour` per request. Existing `allow()` and
`wrap()` unchanged (still used by `oauthRL`).

### Route wiring

Replace single `scoreRL` with two limiters:

```
unauthRL := newIPRateLimiter(1, 60)      // 1 req/60s per IP
authRL   := newIPRateLimiter(1000, 3600)  // ceiling; dynamic per plan
```

Middleware chain: `requireAny -> authAwareRateLimit(unauthRL, authRL, htmlMode) -> handler`

Applied to:
- `GET /score/{username}` (htmlMode=true)
- `GET /api/v1/score/{username}` (htmlMode=false)
- `GET /api/v1/score/{username}/history` (htmlMode=false)

### Config

- `UNAUTH_RATE_LIMIT` (default 1) -- requests per window for unauth
- `UNAUTH_RATE_WINDOW` (default 60) -- window in seconds
- Remove `SCORE_RATE_LIMIT` (internal env var, not a public contract)

### Rate limit error page

New template `ratelimit.html` using `layout.html` base:
- Heading: "Rate limit reached"
- Message: "Free lookups are limited to 1 per minute. Sign in with GitHub for higher limits."
- Primary CTA: "Sign in with GitHub" -> `/auth/github`
- Secondary: "Back to home" -> `/`

### API 429 response

```json
{
  "error": "rate limit exceeded",
  "retry_after": 45,
  "sign_in_url": "/auth/github"
}
```

## Test Plan

- Unit: `allowWithLimit` with dynamic limits
- Unit: `authAwareRateLimit` middleware -- unauth vs auth, HTML vs JSON responses
- Integration: unauth 429 on second rapid request
- Integration: auth user gets plan-based limit
- Integration: auth user bypasses unauth limit
- Existing `ratelimit_test.go` tests pass unchanged
