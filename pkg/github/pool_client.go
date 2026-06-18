package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/score"
	"golang.org/x/oauth2"
)

// maxPoolRetries caps the number of token rotations triggered by rate-limit
// errors before giving up. A typical pool has 3-5 tokens; cap is generous
// enough to rotate through them all once.
const maxPoolRetries = 6

// poolBackoffBase is the base delay between rate-limit retries. Each retry
// adds an exponential factor with jitter to avoid synchronized thundering-
// herd patterns when several scorer goroutines all hit a rate-limited token
// at the same instant.
const poolBackoffBase = 200 * time.Millisecond

// PoolClient implements Client using a TokenPool for round-robin token
// rotation with bounded retry on rate-limit errors. Each token rotation
// honors the actual reset time GitHub returns; the retry is backed by a
// generic helper so all five Client methods share the same loop.
type PoolClient struct {
	pool *TokenPool

	// clientCache memoizes one *gh.Client per token so the underlying
	// http.Transport (and its TLS connection pool) is reused across
	// requests. Building a fresh oauth2.NewClient + gh.NewClient on every
	// call defeated TLS session reuse and piled up short-lived idle
	// connections under scorer load.
	clientCacheMu sync.Mutex
	clientCache   map[string]*gh.Client
}

// NewPoolClient returns a Client backed by the given token pool.
func NewPoolClient(pool *TokenPool) *PoolClient {
	return &PoolClient{pool: pool, clientCache: make(map[string]*gh.Client)}
}

// Pool returns the underlying token pool for operational visibility.
func (c *PoolClient) Pool() *TokenPool {
	return c.pool
}

func (c *PoolClient) ghClient(ctx context.Context) (*gh.Client, string, error) {
	token := c.pool.Token()
	if token == "" {
		return nil, "", errors.New("no available GitHub tokens")
	}

	c.clientCacheMu.Lock()
	api, ok := c.clientCache[token]
	if !ok {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		api = gh.NewClient(oauth2.NewClient(ctx, ts))
		c.clientCache[token] = api
	}
	c.clientCacheMu.Unlock()
	return api, token, nil
}

// InvalidateTokens drops cached *gh.Client entries for the given tokens.
// Called when the pool replaces entries (e.g., installation tokens
// re-minted) so we don't leak references to no-longer-active tokens.
// Safe to call with tokens that were never cached.
func (c *PoolClient) InvalidateTokens(tokens []string) {
	if len(tokens) == 0 {
		return
	}
	c.clientCacheMu.Lock()
	defer c.clientCacheMu.Unlock()
	for _, t := range tokens {
		delete(c.clientCache, t)
	}
}

// rateLimitReset extracts the absolute reset time from a GitHub rate-limit
// error so callers can plumb it into TokenPool.Exhaust. Falls back to a
// zero time, which TokenPool.Exhaust converts to the default window.
func rateLimitReset(err error) time.Time {
	var rlErr *gh.RateLimitError
	if errors.As(err, &rlErr) {
		if !rlErr.Rate.Reset.Time.IsZero() {
			return rlErr.Rate.Reset.Time
		}
	}
	var abuseErr *gh.AbuseRateLimitError
	if errors.As(err, &abuseErr) && abuseErr.RetryAfter != nil {
		return time.Now().Add(*abuseErr.RetryAfter)
	}
	return time.Time{}
}

// classifyRateLimitFamily classifies a rate-limit error into a stable
// family string for log-based metric labels. The categories map onto
// GitHub's own quota families (core/search/graphql) plus a dedicated
// "abuse" bucket for secondary-limit errors. Used by the Exhaust slog
// line so alert policies can page only on core/abuse and let search/
// graphql churn appear on dashboards without paging.
func classifyRateLimitFamily(err error) string {
	var rlErr *gh.RateLimitError
	if errors.As(err, &rlErr) && rlErr.Response != nil && rlErr.Response.Request != nil {
		req := rlErr.Response.Request
		cat := gh.GetRateLimitCategory(req.Method, req.URL.Path)
		switch cat {
		case gh.SearchCategory, gh.CodeSearchCategory:
			return "search"
		case gh.GraphqlCategory:
			return "graphql"
		case gh.CoreCategory:
			return "core"
		default:
			return "core"
		}
	}
	var abuseErr *gh.AbuseRateLimitError
	if errors.As(err, &abuseErr) {
		return "abuse"
	}
	return "unknown"
}

// poolDo runs fn against a fresh client from the pool, retrying on
// rate-limit errors with bounded attempts and exponential backoff. The
// generic return type lets every Client method share the same loop
// without sacrificing static typing.
func poolDo[T any](ctx context.Context, c *PoolClient, op string, fn func(api *gh.Client) (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < maxPoolRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		api, token, err := c.ghClient(ctx)
		if err != nil {
			return zero, err
		}
		result, err := fn(api)
		if err == nil {
			return result, nil
		}
		if isAuthFailure(err) {
			c.pool.InvalidateAuth(token)
			c.pool.SignalRefresh()
			if c.pool.ActiveCount() == 0 {
				return zero, fmt.Errorf("all tokens invalid: %w", err)
			}
			slog.Warn("token auth failure, rotating", "op", op, "attempt", attempt+1)
			continue
		}
		if !isRateLimited(err) {
			return zero, err
		}
		c.pool.Exhaust(token, rateLimitReset(err), classifyRateLimitFamily(err))
		if c.pool.ActiveCount() == 0 {
			return zero, fmt.Errorf("all tokens exhausted: %w", err)
		}
		slog.Warn("token rate limited, retrying", "op", op, "attempt", attempt+1)

		// Exponential backoff with jitter; capped at ~3.2s for attempt 4.
		wait := poolBackoffBase << attempt
		jitter := time.Duration(rand.Int64N(int64(poolBackoffBase))) //nolint:gosec // jitter, not security-sensitive
		timer := time.NewTimer(wait + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, fmt.Errorf("retries exhausted for %s after %d rate-limit rotations", op, maxPoolRetries)
}

// FetchUser retrieves a GitHub user profile.
func (c *PoolClient) FetchUser(ctx context.Context, username string) (*UserProfile, error) {
	return poolDo(ctx, c, "fetch user", func(api *gh.Client) (*UserProfile, error) {
		return fetchUser(ctx, api, username)
	})
}

// FetchSignals retrieves scoring signals with automatic token rotation on rate limit.
func (c *PoolClient) FetchSignals(ctx context.Context, username, repo string, hints *ArchiveHints) (*score.InputSignals, error) {
	return poolDo(ctx, c, "fetch signals", func(api *gh.Client) (*score.InputSignals, error) {
		return fetchSignals(ctx, api, username, repo, hints)
	})
}

// ListUserRepos retrieves the contributor's owned repositories with
// automatic token rotation on rate limit.
func (c *PoolClient) ListUserRepos(ctx context.Context, username string, maxRepos int) ([]Repo, error) {
	return poolDo(ctx, c, "list user repos", func(api *gh.Client) ([]Repo, error) {
		return fetchUserRepos(ctx, api, username, maxRepos)
	})
}

// FetchSecurityCredits queries the contributor's GHSA credits via
// GraphQL, with automatic token rotation on rate limit.
func (c *PoolClient) FetchSecurityCredits(ctx context.Context, username string, maxCredits int) ([]SecurityAdvisoryCredit, error) {
	return poolDo(ctx, c, "fetch security credits", func(api *gh.Client) ([]SecurityAdvisoryCredit, error) {
		return fetchSecurityCredits(ctx, api, username, maxCredits)
	})
}

// IsOrgMember checks if the user is a member of the given org with token rotation.
func (c *PoolClient) IsOrgMember(ctx context.Context, org, username string) (bool, error) {
	return poolDo(ctx, c, "is org member", func(api *gh.Client) (bool, error) {
		isMember, _, err := api.Organizations.IsMember(ctx, org, username)
		return isMember, err
	})
}

// isRateLimited checks if the error is a GitHub rate limit error.
func isRateLimited(err error) bool {
	var rlErr *gh.RateLimitError
	if errors.As(err, &rlErr) {
		return true
	}
	var abuseErr *gh.AbuseRateLimitError
	return errors.As(err, &abuseErr)
}

// isAuthFailure returns true when GitHub rejected the token with 401.
// A 401 means the token itself is bad — revoked PAT, suspended/uninstalled
// GitHub App installation, or rotated App key. Distinct from rate-limit
// errors (which retry on the same token) and from 404/422 (terminal for
// the request but not the token).
func isAuthFailure(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		return ghErr.Response.StatusCode == http.StatusUnauthorized
	}
	return false
}
