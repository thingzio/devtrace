package github

import (
	"context"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/score"
	"golang.org/x/oauth2"
)

// PATClient implements Client using a personal access token.
type PATClient struct {
	api *gh.Client
}

// NewPATClient returns a Client backed by a personal access token.
// The ctx parameter is accepted for API consistency but is not stored;
// context.Background() is used for the oauth2 HTTP client because the
// returned PATClient outlives any single request context.
func NewPATClient(_ context.Context, token string) *PATClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	return &PATClient{api: gh.NewClient(tc)}
}

// FetchUser retrieves a GitHub user profile.
func (c *PATClient) FetchUser(ctx context.Context, username string) (*UserProfile, error) {
	return fetchUser(ctx, c.api, username)
}

// FetchSignals retrieves scoring signals for the given user, optionally scoped to a repo.
func (c *PATClient) FetchSignals(ctx context.Context, username, repo string, hints *ArchiveHints) (*score.InputSignals, error) {
	return fetchSignals(ctx, c.api, username, repo, hints)
}

// IsOrgMember checks if the user is a member of the given org.
func (c *PATClient) IsOrgMember(ctx context.Context, org, username string) (bool, error) {
	isMember, _, err := c.api.Organizations.IsMember(ctx, org, username)
	if err != nil {
		return false, err
	}
	return isMember, nil
}

// ListUserRepos retrieves the contributor's owned repositories.
func (c *PATClient) ListUserRepos(ctx context.Context, username string, maxRepos int) ([]Repo, error) {
	return fetchUserRepos(ctx, c.api, username, maxRepos)
}

// FetchSecurityCredits queries the contributor's GHSA credits via GraphQL.
func (c *PATClient) FetchSecurityCredits(ctx context.Context, username string, maxCredits int) ([]SecurityAdvisoryCredit, error) {
	return fetchSecurityCredits(ctx, c.api, username, maxCredits)
}
