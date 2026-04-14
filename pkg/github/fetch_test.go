package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/google/go-github/v83/github"
)

// ghAPIServer sets up an httptest server that handles the GitHub API endpoints
// used by fetchSignals and fetchUser. Returns the server and a go-github client
// wired to it.
func ghAPIServer(t *testing.T, handlers map[string]http.HandlerFunc) (*httptest.Server, *gh.Client) {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, handler := range handlers {
		mux.HandleFunc(pattern, handler)
	}
	srv := httptest.NewServer(mux)
	client := gh.NewClient(nil).WithAuthToken("test-token")
	// Point the client at our test server.
	baseURL := srv.URL + "/"
	client, _ = client.WithEnterpriseURLs(baseURL, baseURL)
	return srv, client
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestMapUser(t *testing.T) {
	t.Parallel()

	created := time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)
	ts := &gh.Timestamp{Time: created}
	login := "torvalds"
	name := "Linus Torvalds"
	email := "linus@kernel.org"
	bio := "Creator of Linux and Git"
	company := "Linux Foundation"
	location := "Portland, OR"
	website := "https://kernel.org"
	avatar := "https://avatars.githubusercontent.com/u/1024025"
	followers := 200000
	following := 0
	repos := 7

	u := &gh.User{
		Login:       &login,
		Name:        &name,
		Email:       &email,
		Bio:         &bio,
		Blog:        &website,
		Company:     &company,
		Location:    &location,
		AvatarURL:   &avatar,
		Followers:   &followers,
		Following:   &following,
		PublicRepos: &repos,
		CreatedAt:   ts,
	}

	p := mapUser(u)

	if p.Username != "torvalds" {
		t.Errorf("username: got %q, want %q", p.Username, "torvalds")
	}
	if p.Name != name {
		t.Errorf("name: got %q, want %q", p.Name, name)
	}
	if p.Email != email {
		t.Errorf("email: got %q, want %q", p.Email, email)
	}
	if p.Bio != bio {
		t.Errorf("bio: got %q, want %q", p.Bio, bio)
	}
	if p.Company != company {
		t.Errorf("company: got %q, want %q", p.Company, company)
	}
	if p.Location != location {
		t.Errorf("location: got %q, want %q", p.Location, location)
	}
	if p.AvatarURL != avatar {
		t.Errorf("avatar: got %q, want %q", p.AvatarURL, avatar)
	}
	if p.Website != website {
		t.Errorf("website: got %q, want %q", p.Website, website)
	}
	if p.Followers != 200000 {
		t.Errorf("followers: got %d, want 200000", p.Followers)
	}
	if p.Following != 0 {
		t.Errorf("following: got %d, want 0", p.Following)
	}
	if p.PublicRepos != 7 {
		t.Errorf("public_repos: got %d, want 7", p.PublicRepos)
	}
	if !p.CreatedAt.Equal(created) {
		t.Errorf("created_at: got %v, want %v", p.CreatedAt, created)
	}
	if p.Suspended {
		t.Error("should not be suspended")
	}
}

func TestMapUserSuspended(t *testing.T) {
	t.Parallel()

	login := "badactor"
	suspended := &gh.Timestamp{Time: time.Now()}
	u := &gh.User{
		Login:       &login,
		SuspendedAt: suspended,
	}

	p := mapUser(u)
	if !p.Suspended {
		t.Error("should be suspended")
	}
}

func TestMapUserNilCreatedAt(t *testing.T) {
	t.Parallel()

	login := "nodate"
	u := &gh.User{Login: &login}
	p := mapUser(u)
	if !p.CreatedAt.IsZero() {
		t.Error("created_at should be zero when nil")
	}
}

func TestFetchUserViaAPI(t *testing.T) {
	t.Parallel()

	login := "testuser"
	name := "Test User"
	bio := "Hello"
	followers := 42
	created := gh.Timestamp{Time: time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)}

	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"GET /api/v3/users/testuser": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, gh.User{
				Login:     &login,
				Name:      &name,
				Bio:       &bio,
				Followers: &followers,
				CreatedAt: &created,
			})
		},
	})
	defer srv.Close()

	p, err := fetchUser(context.Background(), client, "testuser")
	if err != nil {
		t.Fatalf("fetchUser: %v", err)
	}
	if p.Username != "testuser" {
		t.Errorf("username: got %q", p.Username)
	}
	if p.Followers != 42 {
		t.Errorf("followers: got %d", p.Followers)
	}
}

func TestFetchSignalsBasic(t *testing.T) {
	t.Parallel()

	login := "alice"
	name := "Alice"
	bio := "dev"
	company := "ACME"
	location := "NYC"
	blog := "https://alice.dev"
	followers := 50
	following := 10
	repos := 15
	created := gh.Timestamp{Time: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}

	mergedTotal := 25
	closedTotal := 3
	recentTotal := 5
	repoURL1 := "https://api.github.com/repos/org/repo1"
	repoURL2 := "https://api.github.com/repos/org/repo2"

	isFork := true
	notFork := false

	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		// User profile.
		"GET /api/v3/users/alice": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, gh.User{
				Login:       &login,
				Name:        &name,
				Bio:         &bio,
				Blog:        &blog,
				Company:     &company,
				Location:    &location,
				Followers:   &followers,
				Following:   &following,
				PublicRepos: &repos,
				CreatedAt:   &created,
			})
		},
		// Merged PRs search.
		"GET /api/v3/search/issues": func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("q")
			switch {
			case contains(q, "is:merged"):
				writeJSON(w, gh.IssuesSearchResult{
					Total: &mergedTotal,
				})
			case contains(q, "is:unmerged"):
				writeJSON(w, gh.IssuesSearchResult{
					Total: &closedTotal,
				})
			default:
				// Recent PRs — return issues with RepositoryURL.
				writeJSON(w, gh.IssuesSearchResult{
					Total: &recentTotal,
					Issues: []*gh.Issue{
						{RepositoryURL: &repoURL1},
						{RepositoryURL: &repoURL2},
						{RepositoryURL: &repoURL1},
					},
				})
			}
		},
		// Repos list (for fork detection).
		"GET /api/v3/users/alice/repos": func(w http.ResponseWriter, _ *http.Request) {
			rName1 := "repo1"
			rName2 := "forked-repo"
			writeJSON(w, []*gh.Repository{
				{Name: &rName1, Fork: &notFork},
				{Name: &rName2, Fork: &isFork},
			})
		},
	})
	defer srv.Close()

	signals, err := fetchSignals(context.Background(), client, "alice", "", nil)
	if err != nil {
		t.Fatalf("fetchSignals: %v", err)
	}

	// Identity signals.
	if signals.AgeDays <= 0 {
		t.Errorf("AgeDays should be positive, got %d", signals.AgeDays)
	}
	if !signals.HasBio {
		t.Error("HasBio should be true")
	}
	if !signals.HasCompany {
		t.Error("HasCompany should be true")
	}
	if !signals.HasLocation {
		t.Error("HasLocation should be true")
	}
	if !signals.HasWebsite {
		t.Error("HasWebsite should be true")
	}
	if signals.Followers != 50 {
		t.Errorf("Followers: got %d, want 50", signals.Followers)
	}
	if signals.Following != 10 {
		t.Errorf("Following: got %d, want 10", signals.Following)
	}
	if signals.PublicRepos != 15 {
		t.Errorf("PublicRepos: got %d, want 15", signals.PublicRepos)
	}

	// PR signals.
	if signals.PRsMerged != 25 {
		t.Errorf("PRsMerged: got %d, want 25", signals.PRsMerged)
	}
	if signals.PRsClosed != 3 {
		t.Errorf("PRsClosed: got %d, want 3", signals.PRsClosed)
	}

	// Distinct recent repos.
	if signals.RecentPRRepoCount != 2 {
		t.Errorf("RecentPRRepoCount: got %d, want 2", signals.RecentPRRepoCount)
	}

	// Fork detection.
	if signals.ForkedRepos != 1 {
		t.Errorf("ForkedRepos: got %d, want 1", signals.ForkedRepos)
	}

	// No repo context → no org/assoc/commit signals.
	if signals.OrgMember {
		t.Error("OrgMember should be false without repo context")
	}
	if signals.AuthorAssociation != "" {
		t.Errorf("AuthorAssociation should be empty, got %q", signals.AuthorAssociation)
	}
}

func TestFetchSignalsSuspended(t *testing.T) {
	t.Parallel()

	login := "suspended-user"
	suspended := &gh.Timestamp{Time: time.Now()}

	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"GET /api/v3/users/suspended-user": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, gh.User{
				Login:       &login,
				SuspendedAt: suspended,
			})
		},
	})
	defer srv.Close()

	signals, err := fetchSignals(context.Background(), client, "suspended-user", "", nil)
	if err != nil {
		t.Fatalf("fetchSignals: %v", err)
	}
	if !signals.Suspended {
		t.Error("should be marked suspended")
	}
	// Suspended users return early — PR/repo signals should be zero.
	if signals.PRsMerged != 0 {
		t.Errorf("PRsMerged should be 0 for suspended, got %d", signals.PRsMerged)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
