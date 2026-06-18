package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gh "github.com/google/go-github/v83/github"
	"golang.org/x/oauth2"
)

func TestPoolClientImplementsInterface(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("test-token")
	var _ Client = NewPoolClient(pool)
}

func TestPoolClientPool(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("tok1", "tok2")
	client := NewPoolClient(pool)
	if got := client.Pool(); got != pool {
		t.Error("Pool() should return the same pool instance")
	}
}

func TestPoolClientEmptyPool(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("")
	client := NewPoolClient(pool)

	_, err := client.FetchUser(t.Context(), "testuser")
	if err == nil {
		t.Error("expected error with empty pool")
	}

	_, err = client.FetchSignals(t.Context(), "testuser", "", nil)
	if err == nil {
		t.Error("expected error with empty pool")
	}
}

func TestIsRateLimited(t *testing.T) {
	t.Parallel()

	if isRateLimited(nil) {
		t.Error("nil should not be rate limited")
	}

	if isRateLimited(errForTest("some error")) {
		t.Error("generic error should not be rate limited")
	}
}

func TestClassifyRateLimitFamily(t *testing.T) {
	t.Parallel()

	// Build a *gh.RateLimitError pinned to a given (method, path) so the
	// classifier exercises gh.GetRateLimitCategory the same way it would
	// in production.
	withURL := func(method, path string) error {
		req, _ := http.NewRequestWithContext(context.Background(), method, "https://api.github.com"+path, nil)
		return &gh.RateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden, Request: req}}
	}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil_err", nil, "unknown"},
		{"plain_err", errForTest("boom"), "unknown"},
		{"core_users", withURL(http.MethodGet, "/users/octocat"), "core"},
		{"core_repos", withURL(http.MethodGet, "/repos/owner/repo"), "core"},
		{"search_issues", withURL(http.MethodGet, "/search/issues"), "search"},
		{"search_code", withURL(http.MethodGet, "/search/code"), "search"},
		{"graphql", withURL(http.MethodPost, "/graphql"), "graphql"},
		{"abuse", &gh.AbuseRateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden}}, "abuse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRateLimitFamily(tc.err); got != tc.want {
				t.Errorf("classifyRateLimitFamily(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsAuthFailure(t *testing.T) {
	t.Parallel()

	if isAuthFailure(nil) {
		t.Error("nil should not be auth failure")
	}
	if isAuthFailure(errForTest("some error")) {
		t.Error("plain error should not be auth failure")
	}

	want401 := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusUnauthorized}}
	if !isAuthFailure(want401) {
		t.Error("401 should be auth failure")
	}

	notFound := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}
	if isAuthFailure(notFound) {
		t.Error("404 should not be auth failure")
	}

	rate := &gh.RateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden}}
	if isAuthFailure(rate) {
		t.Error("rate limit should not be auth failure")
	}
}

// TestPoolClientAuthFailureRotates verifies that a 401 from the first
// token triggers invalidation + rotation to the next token, and that the
// request ultimately succeeds against the good token. This is the
// regression test for the production incident on 2026-06-09 where a
// poisoned token wedged every scoring request for hours.
func TestPoolClientAuthFailureRotates(t *testing.T) {
	t.Parallel()

	var badHits, goodHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch auth {
		case "Bearer bad-token":
			badHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Bad credentials"})
		case "Bearer good-token":
			goodHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"login":      "torvalds",
				"name":       "Linus",
				"created_at": "2020-01-01T00:00:00Z",
			})
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{InstallationID: 1, Label: "bad-org", Token: "bad-token"},
		{InstallationID: 2, Label: "good-org", Token: "good-token"},
	})
	client := NewPoolClient(pool)

	// Pre-seed the gh.Client cache so both tokens hit our test server
	// instead of api.github.com. oauth2 injects the Bearer header; the
	// enterprise URL override sends the request to httptest.
	client.clientCache["bad-token"] = newTestGHClient(srv.URL, "bad-token")
	client.clientCache["good-token"] = newTestGHClient(srv.URL, "good-token")

	profile, err := client.FetchUser(context.Background(), "torvalds")
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if profile.Username != "torvalds" {
		t.Errorf("username: got %q, want torvalds", profile.Username)
	}

	if badHits.Load() != 1 {
		t.Errorf("bad token hit count = %d, want 1", badHits.Load())
	}
	if goodHits.Load() != 1 {
		t.Errorf("good token hit count = %d, want 1", goodHits.Load())
	}

	// The bad token should now be invalidated.
	if pool.ActiveCount() != 1 {
		t.Errorf("ActiveCount after rotation = %d, want 1", pool.ActiveCount())
	}
	events := pool.RecentInvalidations(time.Time{})
	if len(events) != 1 || events[0].Label != "bad-org" {
		t.Errorf("invalidation event: %+v", events)
	}
}

// TestPoolClientAllAuthFailures verifies the bail-out when every token is
// invalidated — we must return an error rather than spinning the retry
// loop or returning a zero result.
func TestPoolClientAllAuthFailures(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	pool := NewTokenPoolFromEntries([]PoolEntry{
		{InstallationID: 1, Label: "a", Token: "a"},
		{InstallationID: 2, Label: "b", Token: "b"},
	})
	client := NewPoolClient(pool)
	client.clientCache["a"] = newTestGHClient(srv.URL, "a")
	client.clientCache["b"] = newTestGHClient(srv.URL, "b")

	_, err := client.FetchUser(context.Background(), "torvalds")
	if err == nil {
		t.Fatal("expected error when all tokens invalid")
	}
	if pool.ActiveCount() != 0 {
		t.Errorf("ActiveCount = %d, want 0", pool.ActiveCount())
	}
}

func newTestGHClient(baseURL, token string) *gh.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	hc := oauth2.NewClient(context.Background(), ts)
	c := gh.NewClient(hc)
	url := baseURL + "/"
	c, _ = c.WithEnterpriseURLs(url, url)
	return c
}

type errForTest string

func (e errForTest) Error() string { return string(e) }
