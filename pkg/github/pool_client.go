package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/score"
	"golang.org/x/oauth2"
)

// PoolClient implements Client using a TokenPool for round-robin token
// rotation with automatic retry on rate limit errors.
type PoolClient struct {
	pool *TokenPool
}

// NewPoolClient returns a Client backed by the given token pool.
func NewPoolClient(pool *TokenPool) *PoolClient {
	return &PoolClient{pool: pool}
}

func (c *PoolClient) ghClient(ctx context.Context) (*gh.Client, string, error) {
	token := c.pool.Token()
	if token == "" {
		return nil, "", errors.New("no available GitHub tokens")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return gh.NewClient(tc), token, nil
}

// FetchUser retrieves a GitHub user profile.
func (c *PoolClient) FetchUser(ctx context.Context, username string) (*UserProfile, error) {
	api, token, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}

	profile, fetchErr := fetchUser(ctx, api, username)
	if fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		slog.Warn("token rate limited, retrying", "username", username)
		api, _, err = c.ghClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("retry after rate limit: %w", err)
		}
		return fetchUser(ctx, api, username)
	}
	return profile, fetchErr
}

// FetchSignals retrieves scoring signals with automatic token rotation on rate limit.
func (c *PoolClient) FetchSignals(ctx context.Context, username, repo string, hints *ArchiveHints) (*score.InputSignals, error) {
	api, token, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}

	signals, fetchErr := fetchSignals(ctx, api, username, repo, hints)
	if fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		slog.Warn("token rate limited, retrying", "username", username)
		api, _, err = c.ghClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("retry after rate limit: %w", err)
		}
		return fetchSignals(ctx, api, username, repo, hints)
	}
	return signals, fetchErr
}

// IsOrgMember checks if the user is a member of the given org with token rotation.
func (c *PoolClient) IsOrgMember(ctx context.Context, org, username string) (bool, error) {
	api, token, err := c.ghClient(ctx)
	if err != nil {
		return false, err
	}

	isMember, _, fetchErr := api.Organizations.IsMember(ctx, org, username)
	if fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		api, _, err = c.ghClient(ctx)
		if err != nil {
			return false, fmt.Errorf("retry after rate limit: %w", err)
		}
		isMember, _, fetchErr = api.Organizations.IsMember(ctx, org, username)
	}
	if fetchErr != nil {
		return false, fetchErr
	}
	return isMember, nil
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
