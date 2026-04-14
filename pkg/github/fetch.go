package github

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v83/github"
	"github.com/thingzio/devtrace/pkg/score"
)

// fetchUser retrieves a GitHub user profile using the provided go-github client.
func fetchUser(ctx context.Context, api *gh.Client, username string) (*UserProfile, error) {
	u, _, err := api.Users.Get(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("fetch user %s: %w", username, err)
	}
	return mapUser(u), nil
}

// fetchSignals retrieves scoring signals for the given user, optionally scoped to a repo.
//
//nolint:funlen,gocyclo // concurrent API calls inflate length and cyclomatic complexity; splitting hurts readability
func fetchSignals(ctx context.Context, api *gh.Client, username, repo string, hints *ArchiveHints) (*score.InputSignals, error) {
	u, _, err := api.Users.Get(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("fetch user %s: %w", username, err)
	}

	profile := mapUser(u)
	signals := &score.InputSignals{
		AgeDays:          int64(time.Since(profile.CreatedAt).Hours() / 24),
		HasBio:           profile.Bio != "",
		HasCompany:       profile.Company != "",
		HasLocation:      profile.Location != "",
		HasWebsite:       profile.Website != "",
		HasVerifiedEmail: profile.Email != "",
		Followers:        profile.Followers,
		Following:        profile.Following,
		PublicRepos:      profile.PublicRepos,
		Suspended:        profile.Suspended,
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
		mu             sync.Mutex
		mergedPRs      int64
		closedPRs      int64
		recentRepos    int64
		forkedRepos    int64
		orgMember      bool
		assoc          string
		repoCommits    int64
		repoTotal      int64
		repoContribs   int
		repoLastDays   int64
		repoUnverified int64
	)

	var wg sync.WaitGroup
	results := make(chan result, 6)

	// When archive hints are available, use them instead of calling the GitHub Search API.
	if hints != nil {
		mergedPRs = hints.PRsMerged
		closedPRs = hints.PRsClosed
		recentRepos = hints.RecentPRRepoCount
	} else {
		// 1. Merged PRs.
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := fmt.Sprintf("type:pr author:%s is:merged", username)
			sr, _, e := api.Search.Issues(ctx, q, &gh.SearchOptions{
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
			sr, _, e := api.Search.Issues(ctx, q, &gh.SearchOptions{
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
			sr, _, e := api.Search.Issues(ctx, q, &gh.SearchOptions{
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
	}

	// 4. Forked repos (single page — exact for <100 repos, lower bound for 100+).
	wg.Add(1)
	go func() {
		defer wg.Done()
		repos, _, e := api.Repositories.ListByUser(ctx, username, &gh.RepositoryListByUserOptions{
			Type:        "owner",
			ListOptions: gh.ListOptions{PerPage: 100},
		})
		if e != nil {
			results <- result{"forked_repos", e}
			return
		}
		var forks int64
		for _, r := range repos {
			if r.GetFork() {
				forks++
			}
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
				isMember, _, e := api.Organizations.IsMember(ctx, org, username)
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
				sr, _, e := api.Search.Issues(ctx, q, &gh.SearchOptions{
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

			// Contributor stats for this repo (commits, total, recency).
			wg.Add(1)
			go func() {
				defer wg.Done()
				stats, resp, e := api.Repositories.ListContributorsStats(ctx, org, parts[1])
				if e != nil {
					results <- result{"repo_stats", e}
					return
				}
				// GitHub returns 202 when stats are being computed. Retry once.
				if resp.StatusCode == http.StatusAccepted {
					time.Sleep(2 * time.Second)
					stats, _, e = api.Repositories.ListContributorsStats(ctx, org, parts[1])
					if e != nil {
						results <- result{"repo_stats_retry", e}
						return
					}
				}

				var total int64
				var userCommits int64
				var userLastWeek int64
				var unverified int64

				for _, cs := range stats {
					if cs.Author == nil {
						continue
					}
					authorTotal := int64(cs.GetTotal())
					total += authorTotal
					if strings.EqualFold(cs.Author.GetLogin(), username) {
						userCommits = authorTotal
						// Find last active week (most recent non-zero week).
						for i := len(cs.Weeks) - 1; i >= 0; i-- {
							if cs.Weeks[i].GetCommits() > 0 {
								userLastWeek = cs.Weeks[i].GetWeek().Unix()
								break
							}
						}
					}
				}

				mu.Lock()
				repoCommits = userCommits
				repoTotal = total
				repoContribs = len(stats)
				repoUnverified = unverified
				if userLastWeek > 0 {
					repoLastDays = int64(time.Since(time.Unix(userLastWeek, 0)).Hours() / 24)
				}
				mu.Unlock()
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

	// Repo-contextual stats (only populated when repo is provided).
	if repoCommits > 0 || repoTotal > 0 {
		signals.Commits = repoCommits
		signals.TotalCommits = repoTotal
		signals.TotalContributors = repoContribs
		signals.LastCommitDays = repoLastDays
		signals.UnverifiedCommits = repoUnverified
	}

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
		Website:     u.GetBlog(),
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
