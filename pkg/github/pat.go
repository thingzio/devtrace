package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
	u, _, err := c.api.Users.Get(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("fetch user %s: %w", username, err)
	}
	return mapUser(u), nil
}

// FetchSignals retrieves scoring signals for the given user, optionally scoped to a repo.
func (c *PATClient) FetchSignals(ctx context.Context, username, repo string) (*score.InputSignals, error) {
	u, _, err := c.api.Users.Get(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("fetch user %s: %w", username, err)
	}

	profile := mapUser(u)
	signals := &score.InputSignals{
		AgeDays:     int64(time.Since(profile.CreatedAt).Hours() / 24),
		HasBio:      profile.Bio != "",
		HasCompany:  profile.Company != "",
		HasLocation: profile.Location != "",
		Followers:   profile.Followers,
		Following:   profile.Following,
		PublicRepos: profile.PublicRepos,
		Suspended:   profile.Suspended,
	}

	// Suspended accounts: return early with basic profile data only.
	if profile.Suspended {
		slog.Warn("user is suspended, skipping extended signals", "username", username)
		return signals, nil
	}

	type result struct {
		field string
		err   error
	}

	var (
		mu          sync.Mutex
		mergedPRs   int64
		closedPRs   int64
		recentRepos int64
		forkedRepos int64
		orgMember   bool
		assoc       string
	)

	var wg sync.WaitGroup
	results := make(chan result, 6)

	// 1. Merged PRs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		q := fmt.Sprintf("type:pr author:%s is:merged", username)
		sr, _, e := c.api.Search.Issues(ctx, q, &gh.SearchOptions{
			ListOptions: gh.ListOptions{PerPage: 1},
		})
		if e != nil {
			results <- result{"merged_prs", e}
			return
		}
		mu.Lock()
		mergedPRs = int64(sr.GetTotal())
		mu.Unlock()
	}()

	// 2. Closed (unmerged) PRs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		q := fmt.Sprintf("type:pr author:%s is:unmerged is:closed", username)
		sr, _, e := c.api.Search.Issues(ctx, q, &gh.SearchOptions{
			ListOptions: gh.ListOptions{PerPage: 1},
		})
		if e != nil {
			results <- result{"closed_prs", e}
			return
		}
		mu.Lock()
		closedPRs = int64(sr.GetTotal())
		mu.Unlock()
	}()

	// 3. Recent PRs (last 90 days) — count distinct repos.
	wg.Add(1)
	go func() {
		defer wg.Done()
		since := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
		q := fmt.Sprintf("type:pr author:%s created:>=%s", username, since)
		sr, _, e := c.api.Search.Issues(ctx, q, &gh.SearchOptions{
			ListOptions: gh.ListOptions{PerPage: 100},
		})
		if e != nil {
			results <- result{"recent_prs", e}
			return
		}
		repos := make(map[string]struct{})
		for _, issue := range sr.Issues {
			if url := issue.GetRepositoryURL(); url != "" {
				repos[url] = struct{}{}
			}
		}
		mu.Lock()
		recentRepos = int64(len(repos))
		mu.Unlock()
	}()

	// 4. Forked repos.
	wg.Add(1)
	go func() {
		defer wg.Done()
		opt := &gh.RepositoryListByUserOptions{
			Type:        "owner",
			ListOptions: gh.ListOptions{PerPage: 100},
		}
		var forks int64
		for {
			repos, resp, e := c.api.Repositories.ListByUser(ctx, username, opt)
			if e != nil {
				results <- result{"forked_repos", e}
				return
			}
			for _, r := range repos {
				if r.GetFork() {
					forks++
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		mu.Lock()
		forkedRepos = forks
		mu.Unlock()
	}()

	// 5. Org membership + author association (only when repo is specified).
	if repo != "" {
		parts := strings.SplitN(repo, "/", 2)
		if len(parts) == 2 {
			org := parts[0]

			// Org membership check.
			wg.Add(1)
			go func() {
				defer wg.Done()
				isMember, _, e := c.api.Organizations.IsMember(ctx, org, username)
				if e != nil {
					results <- result{"org_member", e}
					return
				}
				mu.Lock()
				orgMember = isMember
				mu.Unlock()
			}()

			// Author association via recent issue/PR in the repo.
			wg.Add(1)
			go func() {
				defer wg.Done()
				q := fmt.Sprintf("repo:%s author:%s", repo, username)
				sr, _, e := c.api.Search.Issues(ctx, q, &gh.SearchOptions{
					Sort:        "updated",
					ListOptions: gh.ListOptions{PerPage: 1},
				})
				if e != nil {
					results <- result{"author_assoc", e}
					return
				}
				if len(sr.Issues) > 0 {
					mu.Lock()
					assoc = sr.Issues[0].GetAuthorAssociation()
					mu.Unlock()
				}
			}()
		}
	}

	// Wait for all goroutines, then drain result channel.
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		slog.Warn("partial signal fetch failure",
			"field", r.field,
			"username", username,
			"error", r.err,
		)
	}

	signals.PRsMerged = mergedPRs
	signals.PRsClosed = closedPRs
	signals.RecentPRRepoCount = recentRepos
	signals.ForkedRepos = forkedRepos
	signals.OrgMember = orgMember
	signals.AuthorAssociation = assoc

	return signals, nil
}

// mapUser converts a go-github User to a UserProfile.
func mapUser(u *gh.User) *UserProfile {
	p := &UserProfile{
		Username:    u.GetLogin(),
		Name:        u.GetName(),
		Email:       u.GetEmail(),
		AvatarURL:   u.GetAvatarURL(),
		Company:     u.GetCompany(),
		Location:    u.GetLocation(),
		Bio:         u.GetBio(),
		Followers:   int64(u.GetFollowers()),
		Following:   int64(u.GetFollowing()),
		PublicRepos: int64(u.GetPublicRepos()),
		Suspended:   u.SuspendedAt != nil,
	}
	if u.CreatedAt != nil {
		p.CreatedAt = u.CreatedAt.Time
	}
	return p
}
