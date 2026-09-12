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
	"encoding/json"
	"fmt"
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

func ptr[T any](v T) *T { return &v }

// TestMapRepoEdgeCases covers nil sub-fields and unusual flag combos in
// the go-github Repository → Repo conversion. Each case documents an
// invariant we rely on in pkg/profile/repos.go aggregation.
func TestMapRepoEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      *gh.Repository
		want       Repo
		wantPushed bool
	}{
		{
			name: "all fields populated",
			input: &gh.Repository{
				Name: ptr("myrepo"), FullName: ptr("user/myrepo"),
				Description: ptr("A repo"), Language: ptr("Go"),
				StargazersCount: ptr(42), Fork: ptr(false), Archived: ptr(false),
				PushedAt: &gh.Timestamp{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
			want: Repo{
				Name: "myrepo", FullName: "user/myrepo", Description: "A repo",
				Language: "Go", Stars: 42,
			},
			wantPushed: true,
		},
		{
			name:  "fork repository preserved",
			input: &gh.Repository{Name: ptr("forked"), FullName: ptr("user/forked"), Fork: ptr(true), StargazersCount: ptr(0)},
			want:  Repo{Name: "forked", FullName: "user/forked", Fork: true},
		},
		{
			name:  "archived repository preserved",
			input: &gh.Repository{Name: ptr("old"), Archived: ptr(true)},
			want:  Repo{Name: "old", Archived: true},
		},
		{
			name:  "fork AND archived",
			input: &gh.Repository{Name: ptr("oldfork"), Fork: ptr(true), Archived: ptr(true)},
			want:  Repo{Name: "oldfork", Fork: true, Archived: true},
		},
		{
			name:  "missing language defaults to empty",
			input: &gh.Repository{Name: ptr("polyglot"), StargazersCount: ptr(5)},
			want:  Repo{Name: "polyglot", Stars: 5, Language: ""},
		},
		{
			name:  "nil PushedAt yields zero time",
			input: &gh.Repository{Name: ptr("never-pushed"), PushedAt: nil},
			want:  Repo{Name: "never-pushed"},
		},
		{
			name:  "completely empty repository — should not panic",
			input: &gh.Repository{},
			want:  Repo{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapRepo(tc.input)
			if got.Name != tc.want.Name {
				t.Errorf("Name: got %q, want %q", got.Name, tc.want.Name)
			}
			if got.FullName != tc.want.FullName {
				t.Errorf("FullName: got %q, want %q", got.FullName, tc.want.FullName)
			}
			if got.Description != tc.want.Description {
				t.Errorf("Description: got %q, want %q", got.Description, tc.want.Description)
			}
			if got.Language != tc.want.Language {
				t.Errorf("Language: got %q, want %q", got.Language, tc.want.Language)
			}
			if got.Stars != tc.want.Stars {
				t.Errorf("Stars: got %d, want %d", got.Stars, tc.want.Stars)
			}
			if got.Fork != tc.want.Fork {
				t.Errorf("Fork: got %v, want %v", got.Fork, tc.want.Fork)
			}
			if got.Archived != tc.want.Archived {
				t.Errorf("Archived: got %v, want %v", got.Archived, tc.want.Archived)
			}
			if got.PushedAt.IsZero() == tc.wantPushed {
				t.Errorf("PushedAt zero=%v, want zero=%v", got.PushedAt.IsZero(), !tc.wantPushed)
			}
		})
	}
}

// TestFetchUserReposPagination verifies fetchUserRepos walks the
// "Link: rel=next" pagination protocol, accumulates pages correctly,
// and stops cleanly at the last page (no NextPage).
func TestFetchUserReposPagination(t *testing.T) {
	const username = "paginate-user"
	pages := 0
	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"GET /api/v3/users/" + username + "/repos": func(w http.ResponseWriter, r *http.Request) {
			pages++
			page := r.URL.Query().Get("page")
			var (
				start, count, nextPage int
			)
			switch page {
			case "", "1":
				start, count, nextPage = 0, 100, 2
			case "2":
				start, count, nextPage = 100, 100, 3
			case "3":
				start, count, nextPage = 200, 50, 0
			default:
				t.Fatalf("unexpected page %q", page)
			}
			repos := make([]*gh.Repository, count)
			for i := range count {
				n := fmt.Sprintf("repo-%d", start+i)
				repos[i] = &gh.Repository{
					Name: ptr(n), FullName: ptr(username + "/" + n),
					StargazersCount: ptr(start + i),
				}
			}
			if nextPage > 0 {
				// go-github parses Link header URL's page= query param into resp.NextPage.
				w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/users/%s/repos?page=%d>; rel="next"`, srvURL(r), username, nextPage))
			}
			writeJSON(w, repos)
		},
	})
	defer srv.Close()

	repos, err := fetchUserRepos(context.Background(), client, username, 0)
	if err != nil {
		t.Fatalf("fetchUserRepos: %v", err)
	}
	if len(repos) != 250 {
		t.Errorf("expected 250 (100+100+50), got %d", len(repos))
	}
	if pages != 3 {
		t.Errorf("expected 3 page fetches, got %d", pages)
	}
}

// TestFetchUserReposRespectsMaxCap exits the loop once maxRepos is
// reached, even when GitHub indicates more pages exist — protects the
// token budget against runaway pagination on huge accounts.
func TestFetchUserReposRespectsMaxCap(t *testing.T) {
	const username = "cap-user"
	pageHits := 0
	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"GET /api/v3/users/" + username + "/repos": func(w http.ResponseWriter, r *http.Request) {
			pageHits++
			repos := make([]*gh.Repository, 100)
			for i := range 100 {
				n := fmt.Sprintf("r%d", i)
				repos[i] = &gh.Repository{
					Name: ptr(n), FullName: ptr(username + "/" + n),
					StargazersCount: ptr(i),
				}
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/users/%s/repos?page=2>; rel="next"`, srvURL(r), username))
			writeJSON(w, repos)
		},
	})
	defer srv.Close()

	repos, err := fetchUserRepos(context.Background(), client, username, 50)
	if err != nil {
		t.Fatalf("fetchUserRepos: %v", err)
	}
	if len(repos) != 50 {
		t.Errorf("expected exactly 50 repos at maxRepos=50, got %d", len(repos))
	}
	if pageHits != 1 {
		t.Errorf("expected 1 page fetch (cap reached on page 1), got %d", pageHits)
	}
}

// TestFetchUserReposPropagatesError verifies that a 500 from GitHub
// on the first page surfaces as an error rather than silently returning
// partial / empty data.
func TestFetchUserReposPropagatesError(t *testing.T) {
	const username = "error-user"
	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"GET /api/v3/users/" + username + "/repos": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		},
	})
	defer srv.Close()

	repos, err := fetchUserRepos(context.Background(), client, username, 50)
	if err == nil {
		t.Fatalf("expected error, got %d repos", len(repos))
	}
	if !contains(err.Error(), "list user repos") {
		t.Errorf("error should reference list user repos: %q", err)
	}
}

// srvURL extracts the request's full server URL prefix; needed because
// go-github parses the Link header against the configured base URL.
func srvURL(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

// TestFetchSecurityCreditsParsesGraphQL verifies the GraphQL response
// shape from User.securityAdvisoryCredits is parsed into the expected
// SecurityAdvisoryCredit slice, including CVE extraction from the
// identifiers array.
func TestFetchSecurityCreditsParsesGraphQL(t *testing.T) {
	const username = "ghsa-user"
	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"POST /api/v3/graphql": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{
				"data": map[string]any{
					"user": map[string]any{
						"securityAdvisoryCredits": map[string]any{
							"nodes": []map[string]any{
								{
									"type": "REPORTER",
									"securityAdvisory": map[string]any{
										"ghsaId":      "GHSA-aaaa-bbbb-cccc",
										"summary":     "Critical RCE",
										"severity":    "CRITICAL",
										"publishedAt": "2024-06-01T00:00:00Z",
										"identifiers": []map[string]any{
											{"type": "GHSA", "value": "GHSA-aaaa-bbbb-cccc"},
											{"type": "CVE", "value": "CVE-2024-001"},
										},
									},
								},
								{
									"type": "FIXER",
									"securityAdvisory": map[string]any{
										"ghsaId":      "GHSA-1111-2222-3333",
										"summary":     "Path traversal",
										"severity":    "HIGH",
										"publishedAt": "2024-08-15T00:00:00Z",
										"identifiers": []map[string]any{
											{"type": "GHSA", "value": "GHSA-1111-2222-3333"},
										},
									},
								},
							},
						},
					},
				},
			})
		},
	})
	defer srv.Close()

	got, err := fetchSecurityCredits(context.Background(), client, username, 0)
	if err != nil {
		t.Fatalf("fetchSecurityCredits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 credits, got %d", len(got))
	}

	// First credit: reporter, critical, with CVE.
	if got[0].AdvisoryID != "GHSA-aaaa-bbbb-cccc" {
		t.Errorf("[0].AdvisoryID: got %q", got[0].AdvisoryID)
	}
	if got[0].CreditType != "reporter" {
		t.Errorf("[0].CreditType: got %q, want lowercased 'reporter'", got[0].CreditType)
	}
	if got[0].Severity != "critical" {
		t.Errorf("[0].Severity: got %q, want lowercased 'critical'", got[0].Severity)
	}
	if got[0].CVEID != "CVE-2024-001" {
		t.Errorf("[0].CVEID: got %q, want CVE-2024-001", got[0].CVEID)
	}
	if got[0].PublishedAt.IsZero() {
		t.Error("[0].PublishedAt should be parsed")
	}

	// Second credit: fixer, no CVE, just GHSA identifier.
	if got[1].CVEID != "" {
		t.Errorf("[1].CVEID: got %q, want empty", got[1].CVEID)
	}
	if got[1].CreditType != "fixer" {
		t.Errorf("[1].CreditType: got %q", got[1].CreditType)
	}
}

// TestFetchSecurityCreditsEmptyUser handles users with no credits —
// the GraphQL response has an empty nodes array.
func TestFetchSecurityCreditsEmptyUser(t *testing.T) {
	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"POST /api/v3/graphql": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{
				"data": map[string]any{
					"user": map[string]any{
						"securityAdvisoryCredits": map[string]any{
							"nodes": []any{},
						},
					},
				},
			})
		},
	})
	defer srv.Close()

	got, err := fetchSecurityCredits(context.Background(), client, "no-creds-user", 0)
	if err != nil {
		t.Fatalf("expected nil error for empty credits, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 credits, got %d", len(got))
	}
}

// TestFetchSecurityCreditsUserNotFound returns nil error for the
// NOT_FOUND GraphQL error class — treats unknown-user as "no credits".
func TestFetchSecurityCreditsUserNotFound(t *testing.T) {
	srv, client := ghAPIServer(t, map[string]http.HandlerFunc{
		"POST /api/v3/graphql": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{
				"data": map[string]any{"user": nil},
				"errors": []map[string]any{
					{"type": "NOT_FOUND", "message": "Could not resolve user"},
				},
			})
		},
	})
	defer srv.Close()

	got, err := fetchSecurityCredits(context.Background(), client, "nonexistent", 0)
	if err != nil {
		t.Fatalf("NOT_FOUND should be treated as no-credits, got error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil credits, got %+v", got)
	}
}
