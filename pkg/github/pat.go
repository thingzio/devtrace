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
func NewPATClient(ctx context.Context, token string) *PATClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return &PATClient{api: gh.NewClient(tc)}
}

// FetchUser retrieves a GitHub user profile.
func (c *PATClient) FetchUser(ctx context.Context, username string) (*UserProfile, error) {
	return fetchUser(ctx, c.api, username)
}

// FetchSignals retrieves scoring signals for the given user, optionally scoped to a repo.
func (c *PATClient) FetchSignals(ctx context.Context, username, repo string) (*score.InputSignals, error) {
	return fetchSignals(ctx, c.api, username, repo)
}
