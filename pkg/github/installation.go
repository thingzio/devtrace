package github

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/score"
	"github.com/thingzio/devtrace/pkg/tenant"
	"golang.org/x/oauth2"
)

// InstallationClient implements Client using GitHub App installation tokens.
// Tokens are cached and auto-refreshed before expiry.
type InstallationClient struct {
	appCfg         *tenant.GitHubAppConfig
	installationID int64

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewInstallationClient returns a Client backed by auto-refreshing installation tokens.
func NewInstallationClient(cfg *tenant.GitHubAppConfig, installationID int64) *InstallationClient {
	return &InstallationClient{
		appCfg:         cfg,
		installationID: installationID,
	}
}

// ghClient returns a go-github client with a valid installation token.
// Mints a new token if the cached one is expired or about to expire (5 min buffer).
func (c *InstallationClient) ghClient(ctx context.Context) (*gh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Add(5*time.Minute).Before(c.expiresAt) {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: c.token})
		return gh.NewClient(oauth2.NewClient(ctx, ts)), nil
	}

	slog.Debug("minting new installation token", "installation_id", c.installationID)

	it, err := tenant.MintInstallationToken(ctx, c.appCfg, c.installationID)
	if err != nil {
		return nil, fmt.Errorf("mint installation token: %w", err)
	}

	c.token = it.Token
	c.expiresAt = it.ExpiresAt

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: c.token})
	return gh.NewClient(oauth2.NewClient(ctx, ts)), nil
}

// FetchUser retrieves a GitHub user profile.
func (c *InstallationClient) FetchUser(ctx context.Context, username string) (*UserProfile, error) {
	api, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}
	return fetchUser(ctx, api, username)
}

// FetchSignals retrieves scoring signals for the given user, optionally scoped to a repo.
func (c *InstallationClient) FetchSignals(ctx context.Context, username, repo string, hints *ArchiveHints) (*score.InputSignals, error) {
	api, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}
	return fetchSignals(ctx, api, username, repo, hints)
}

func (c *InstallationClient) IsOrgMember(ctx context.Context, org, username string) (bool, error) {
	api, err := c.ghClient(ctx)
	if err != nil {
		return false, err
	}
	isMember, _, err := api.Organizations.IsMember(ctx, org, username)
	if err != nil {
		return false, err
	}
	return isMember, nil
}

// ListUserRepos retrieves the contributor's owned repositories.
func (c *InstallationClient) ListUserRepos(ctx context.Context, username string, maxRepos int) ([]Repo, error) {
	api, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}
	return fetchUserRepos(ctx, api, username, maxRepos)
}

// FetchSecurityCredits queries the contributor's GHSA credits via GraphQL.
func (c *InstallationClient) FetchSecurityCredits(ctx context.Context, username string, maxCredits int) ([]SecurityAdvisoryCredit, error) {
	api, err := c.ghClient(ctx)
	if err != nil {
		return nil, err
	}
	return fetchSecurityCredits(ctx, api, username, maxCredits)
}
