// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
// Tokens are cached and auto-refreshed before expiry. Mint operations are
// serialized through a separate mint mutex so concurrent requesters block
// at most one minter — the data mutex (RWMutex) stays free for readers.
type InstallationClient struct {
	appCfg         *tenant.GitHubAppConfig
	installationID int64

	mu        sync.RWMutex
	token     string
	expiresAt time.Time

	// mintMu serializes mint operations without holding the data mutex
	// across the network call. Concurrent goroutines that arrive at a
	// stale token coalesce on this lock; the first to acquire it mints,
	// stores, and releases — peers re-check under read lock and skip
	// minting if the freshly-stored token is now usable.
	mintMu sync.Mutex
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
	if token, ok := c.cachedTokenIfFresh(); ok {
		return c.buildClient(ctx, token), nil
	}

	// Serialize minters; under load, all goroutines that arrived at a
	// stale token funnel here. The first holder mints; subsequent
	// holders re-check and short-circuit.
	c.mintMu.Lock()
	defer c.mintMu.Unlock()

	if token, ok := c.cachedTokenIfFresh(); ok {
		return c.buildClient(ctx, token), nil
	}

	slog.Debug("minting new installation token", "installation_id", c.installationID)

	it, err := tenant.MintInstallationToken(ctx, c.appCfg, c.installationID)
	if err != nil {
		return nil, fmt.Errorf("mint installation token: %w", err)
	}

	c.mu.Lock()
	c.token = it.Token
	c.expiresAt = it.ExpiresAt
	c.mu.Unlock()

	return c.buildClient(ctx, it.Token), nil
}

func (c *InstallationClient) cachedTokenIfFresh() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.token != "" && time.Now().Add(5*time.Minute).Before(c.expiresAt) {
		return c.token, true
	}
	return "", false
}

func (c *InstallationClient) buildClient(ctx context.Context, token string) *gh.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	return gh.NewClient(oauth2.NewClient(ctx, ts))
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
