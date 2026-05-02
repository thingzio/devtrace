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

// Pool returns the underlying token pool for operational visibility.
func (c *PoolClient) Pool() *TokenPool {
	return c.pool
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
	for fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		if c.pool.ActiveCount() == 0 {
			return nil, fmt.Errorf("all tokens exhausted: %w", fetchErr)
		}
		slog.Warn("token rate limited, retrying", "username", username)
		api, token, err = c.ghClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("retry after rate limit: %w", err)
		}
		profile, fetchErr = fetchUser(ctx, api, username)
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
	for fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		if c.pool.ActiveCount() == 0 {
			return nil, fmt.Errorf("all tokens exhausted: %w", fetchErr)
		}
		slog.Warn("token rate limited, retrying", "username", username)
		api, token, err = c.ghClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("retry after rate limit: %w", err)
		}
		signals, fetchErr = fetchSignals(ctx, api, username, repo, hints)
	}
	return signals, fetchErr
}

// ListUserRepos retrieves the contributor's owned repositories with
// automatic token rotation on rate limit.
//
//nolint:dupl // intentionally parallel to FetchSecurityCredits — extracting a generic retry helper would obscure the per-method log message and return type
func (c *PoolClient) ListUserRepos(ctx context.Context, username string, maxRepos int) ([]Repo, error) {
	api, token, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}

	repos, fetchErr := fetchUserRepos(ctx, api, username, maxRepos)
	for fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		if c.pool.ActiveCount() == 0 {
			return nil, fmt.Errorf("all tokens exhausted: %w", fetchErr)
		}
		slog.Warn("token rate limited, retrying list user repos", "username", username)
		api, token, err = c.ghClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("retry after rate limit: %w", err)
		}
		repos, fetchErr = fetchUserRepos(ctx, api, username, maxRepos)
	}
	return repos, fetchErr
}

// FetchSecurityCredits queries the contributor's GHSA credits via
// GraphQL, with automatic token rotation on rate limit.
//
//nolint:dupl // intentionally parallel to ListUserRepos — see note there
func (c *PoolClient) FetchSecurityCredits(ctx context.Context, username string, maxCredits int) ([]SecurityAdvisoryCredit, error) {
	api, token, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}

	credits, fetchErr := fetchSecurityCredits(ctx, api, username, maxCredits)
	for fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		if c.pool.ActiveCount() == 0 {
			return nil, fmt.Errorf("all tokens exhausted: %w", fetchErr)
		}
		slog.Warn("token rate limited, retrying security credits", "username", username)
		api, token, err = c.ghClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("retry after rate limit: %w", err)
		}
		credits, fetchErr = fetchSecurityCredits(ctx, api, username, maxCredits)
	}
	return credits, fetchErr
}

// IsOrgMember checks if the user is a member of the given org with token rotation.
func (c *PoolClient) IsOrgMember(ctx context.Context, org, username string) (bool, error) {
	api, token, err := c.ghClient(ctx)
	if err != nil {
		return false, err
	}

	isMember, _, fetchErr := api.Organizations.IsMember(ctx, org, username)
	for fetchErr != nil && isRateLimited(fetchErr) {
		c.pool.Exhaust(token)
		if c.pool.ActiveCount() == 0 {
			return false, fmt.Errorf("all tokens exhausted: %w", fetchErr)
		}
		api, token, err = c.ghClient(ctx)
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
