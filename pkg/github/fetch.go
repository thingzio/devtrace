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
		AgeDays:        int64(time.Since(profile.CreatedAt).Hours() / 24),
		HasBio:         profile.Bio != "",
		HasCompany:     profile.Company != "",
		HasLocation:    profile.Location != "",
		HasWebsite:     profile.Website != "",
		HasPublicEmail: profile.Email != "",
		Followers:      profile.Followers,
		Following:      profile.Following,
		PublicRepos:    profile.PublicRepos,
		Suspended:      profile.Suspended,
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

	// When archive hints are marked as trusted, use them directly to skip 3 Search API calls.
	// Otherwise call the Search API, falling back to hints when API calls fail.
	if hints != nil && hints.Trusted {
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
			// Primary: ListContributorsStats (rich data but returns 202 for cold repos).
			// Fallback: ListCommits by author (always works, less data).
			wg.Add(1)
			go func() {
				defer wg.Done()

				if fetchContributorStats(ctx, api, org, parts[1], username, &mu,
					&repoCommits, &repoTotal, &repoContribs, &repoLastDays, &repoUnverified) {
					return
				}

				// Fallback: count user's commits via the commits API.
				// PerPage:1 + resp.LastPage gives the total count cheaply.
				slog.Debug("falling back to commits API", "repo", repo)
				_, userResp, e := api.Repositories.ListCommits(ctx, org, parts[1], &gh.CommitsListOptions{
					Author:      username,
					ListOptions: gh.ListOptions{PerPage: 1},
				})
				if e != nil {
					results <- result{"repo_commits_fallback", e}
					return
				}
				userCount := int64(userResp.LastPage)
				if userCount == 0 {
					userCount = 1 // at least the one commit we got
				}

				_, allResp, e2 := api.Repositories.ListCommits(ctx, org, parts[1], &gh.CommitsListOptions{
					ListOptions: gh.ListOptions{PerPage: 1},
				})
				totalCount := userCount
				if e2 == nil && allResp.LastPage > 0 {
					totalCount = int64(allResp.LastPage)
				}

				mu.Lock()
				repoCommits = userCount
				repoTotal = totalCount
				repoContribs = 1
				mu.Unlock()
			}()
		}
	}

	// Wait for all goroutines, then drain result channel.
	go func() {
		wg.Wait()
		close(results)
	}()

	var anyFailed bool
	var searchFamilyFailed int
	var fatalErr error
	for r := range results {
		slog.Warn("partial signal fetch failure",
			"field", r.field,
			"username", username,
			"error", r.err,
		)
		anyFailed = true
		switch r.field {
		case "merged_prs", "closed_prs", "recent_prs":
			searchFamilyFailed++
		}
		// If a search call failed because the token was rate limited,
		// surface that error so the PoolClient retry loop can rotate
		// tokens. Without this, search-API exhaustion silently degrades
		// every score until the search quota window resets.
		if fatalErr == nil && isRateLimited(r.err) {
			fatalErr = r.err
		}
	}

	// All three search calls failed and at least one was rate-limited:
	// bubble it up so the caller (PoolClient) can rotate to a fresh
	// token instead of silently caching zeros for this contributor.
	if searchFamilyFailed == 3 && fatalErr != nil {
		return nil, fatalErr
	}

	// When the Search API failed and archive hints are available, use them as a floor.
	// Partial archive data is better than zero when the API is unreachable.
	if anyFailed && hints != nil && !hints.Trusted {
		if mergedPRs == 0 && hints.PRsMerged > 0 {
			mergedPRs = hints.PRsMerged
		}
		if closedPRs == 0 && hints.PRsClosed > 0 {
			closedPRs = hints.PRsClosed
		}
		if recentRepos == 0 && hints.RecentPRRepoCount > 0 {
			recentRepos = hints.RecentPRRepoCount
		}
	}

	signals.Partial = anyFailed

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

// fetchContributorStats tries ListContributorsStats with retry for 202.
// Returns true if stats were successfully fetched, false if caller should fallback.
func fetchContributorStats(ctx context.Context, api *gh.Client, org, repo, username string,
	mu *sync.Mutex, commits, total *int64, contribs *int, lastDays, unverified *int64) bool {
	var stats []*gh.ContributorStats
	for attempt := range 4 {
		resp, e := func() (*gh.Response, error) {
			s, r, err := api.Repositories.ListContributorsStats(ctx, org, repo)
			stats = s
			return r, err
		}()
		if e != nil {
			return false
		}
		if resp.StatusCode != http.StatusAccepted {
			break
		}
		if attempt == 3 {
			return false
		}
		wait := time.Duration(2<<attempt) * time.Second
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}

	if len(stats) == 0 {
		return false
	}

	var t, uc, ulw, uv int64
	for _, cs := range stats {
		if cs.Author == nil {
			continue
		}
		authorTotal := int64(cs.GetTotal())
		t += authorTotal
		if strings.EqualFold(cs.Author.GetLogin(), username) {
			uc = authorTotal
			for i := len(cs.Weeks) - 1; i >= 0; i-- {
				if cs.Weeks[i].GetCommits() > 0 {
					ulw = cs.Weeks[i].GetWeek().Unix()
					break
				}
			}
		}
	}

	mu.Lock()
	*commits = uc
	*total = t
	*contribs = len(stats)
	*unverified = uv
	if ulw > 0 {
		*lastDays = int64(time.Since(time.Unix(ulw, 0)).Hours() / 24)
	}
	mu.Unlock()
	return true
}

// maxFetchRepos caps the number of repositories the user-repos listing
// will pull from GitHub for a single contributor. Three pages of 100
// covers nearly all real users; high-volume accounts get the most-
// recently-pushed slice.
const maxFetchRepos = 300

// fetchUserRepos returns the contributor's owned repositories, capped
// at maxRepos. Uses Sort="pushed" so most active repos arrive first;
// the aggregation layer applies its own ranking by star count.
func fetchUserRepos(ctx context.Context, api *gh.Client, username string, maxRepos int) ([]Repo, error) {
	if maxRepos <= 0 || maxRepos > maxFetchRepos {
		maxRepos = maxFetchRepos
	}
	const perPage = 100

	out := make([]Repo, 0, maxRepos)
	for page := 1; len(out) < maxRepos; page++ {
		repos, resp, err := api.Repositories.ListByUser(ctx, username, &gh.RepositoryListByUserOptions{
			Type:        "owner",
			Sort:        "pushed",
			ListOptions: gh.ListOptions{PerPage: perPage, Page: page},
		})
		if err != nil {
			return nil, fmt.Errorf("list user repos %s page %d: %w", username, page, err)
		}
		for _, r := range repos {
			out = append(out, mapRepo(r))
			if len(out) >= maxRepos {
				break
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
	}
	return out, nil
}

// mapRepo converts a go-github Repository to the local Repo type.
func mapRepo(r *gh.Repository) Repo {
	out := Repo{
		Name:        r.GetName(),
		FullName:    r.GetFullName(),
		Description: r.GetDescription(),
		Language:    r.GetLanguage(),
		Stars:       r.GetStargazersCount(),
		Fork:        r.GetFork(),
		Archived:    r.GetArchived(),
	}
	if r.PushedAt != nil {
		out.PushedAt = r.PushedAt.Time
	}
	return out
}

// maxFetchSecurityCredits caps the number of GHSA credits pulled per
// fetch. Top contributors with extensive credits are bounded to avoid
// runaway pagination; the cap is generous enough that almost everyone
// gets the full set.
const maxFetchSecurityCredits = 100

// fetchSecurityCredits queries the GitHub GraphQL API for the
// contributor's published-advisory credits via the
// `User.securityAdvisoryCredits` connection. The REST surface does
// not expose this connection, so GraphQL is required.
//
// Returns an empty slice when the user has no credits — that's a
// valid result, not an error. Returns an error only on transport or
// auth failure, so callers can distinguish "no credits" from
// "couldn't ask."
func fetchSecurityCredits(ctx context.Context, api *gh.Client, username string, maxCredits int) ([]SecurityAdvisoryCredit, error) {
	if maxCredits <= 0 || maxCredits > maxFetchSecurityCredits {
		maxCredits = maxFetchSecurityCredits
	}

	const query = `
query($login: String!, $first: Int!) {
  user(login: $login) {
    securityAdvisoryCredits(first: $first) {
      nodes {
        type
        securityAdvisory {
          ghsaId
          summary
          severity
          publishedAt
          identifiers { type value }
        }
      }
    }
  }
}`

	type identifier struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	type advisory struct {
		GhsaID      string       `json:"ghsaId"`
		Summary     string       `json:"summary"`
		Severity    string       `json:"severity"`
		PublishedAt time.Time    `json:"publishedAt"`
		Identifiers []identifier `json:"identifiers"`
	}
	type creditNode struct {
		Type             string   `json:"type"`
		SecurityAdvisory advisory `json:"securityAdvisory"`
	}
	type creditConn struct {
		Nodes []creditNode `json:"nodes"`
	}
	type userResp struct {
		SecurityAdvisoryCredits creditConn `json:"securityAdvisoryCredits"`
	}
	type respData struct {
		User userResp `json:"user"`
	}
	type graphqlResp struct {
		Data   respData         `json:"data"`
		Errors []map[string]any `json:"errors,omitempty"`
	}

	body := struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{
		Query: query,
		Variables: map[string]any{
			"login": username,
			"first": maxCredits,
		},
	}

	var out graphqlResp
	req, err := api.NewRequest(http.MethodPost, "graphql", body)
	if err != nil {
		return nil, fmt.Errorf("build graphql request: %w", err)
	}
	if _, err := api.Do(ctx, req, &out); err != nil {
		return nil, fmt.Errorf("graphql security credits %s: %w", username, err)
	}
	if len(out.Errors) > 0 {
		// "user not found" comes back as a GraphQL error — treat as empty.
		for _, e := range out.Errors {
			if t, _ := e["type"].(string); t == "NOT_FOUND" {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("graphql security credits %s: errors: %v", username, out.Errors)
	}

	credits := make([]SecurityAdvisoryCredit, 0, len(out.Data.User.SecurityAdvisoryCredits.Nodes))
	for _, n := range out.Data.User.SecurityAdvisoryCredits.Nodes {
		c := SecurityAdvisoryCredit{
			AdvisoryID:  n.SecurityAdvisory.GhsaID,
			CreditType:  strings.ToLower(n.Type),
			Severity:    strings.ToLower(n.SecurityAdvisory.Severity),
			Summary:     n.SecurityAdvisory.Summary,
			PublishedAt: n.SecurityAdvisory.PublishedAt,
		}
		for _, id := range n.SecurityAdvisory.Identifiers {
			if strings.EqualFold(id.Type, "CVE") {
				c.CVEID = id.Value
				break
			}
		}
		if c.AdvisoryID == "" {
			continue
		}
		credits = append(credits, c)
	}
	return credits, nil
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
