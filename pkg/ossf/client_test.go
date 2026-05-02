package ossf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const fixtureScorecardJSON = `{
  "date": "2026-04-27",
  "repo": {"name": "github.com/example/repo", "commit": "abc123"},
  "scorecard": {"version": "v5.0.0", "commit": "deadbeef"},
  "score": 7.5,
  "checks": [
    {"name": "Code-Review", "score": 10, "reason": "all changesets reviewed",
     "documentation": {"url": "https://example.com/code-review"}},
    {"name": "Fuzzing", "score": -1, "reason": "project is not fuzzed",
     "documentation": {"url": "https://example.com/fuzzing"}}
  ]
}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(2 * time.Second)
	c.baseURL = srv.URL
	return c
}

func TestFetchOK(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method: got %s, want GET", r.Method)
		}
		if r.URL.Path != "/projects/github.com/example/repo" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureScorecardJSON))
	})

	got, err := c.Fetch(context.Background(), "example", "repo")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Score != 7.5 {
		t.Errorf("Score: got %v, want 7.5", got.Score)
	}
	if got.Commit != "abc123" {
		t.Errorf("Commit: got %q", got.Commit)
	}
	if got.ScorecardVer != "v5.0.0" {
		t.Errorf("ScorecardVer: got %q", got.ScorecardVer)
	}
	if got.Date.Format("2006-01-02") != "2026-04-27" {
		t.Errorf("Date: got %v", got.Date)
	}
	if len(got.Checks) != 2 {
		t.Fatalf("Checks: got %d, want 2", len(got.Checks))
	}
	if got.Checks[0].Name != "Code-Review" || got.Checks[0].Score != 10 {
		t.Errorf("Checks[0]: %+v", got.Checks[0])
	}
	if got.Checks[1].Score != -1 {
		t.Errorf("Checks[1].Score: got %d, want -1 (not-applicable preserved)", got.Checks[1].Score)
	}
}

func TestFetchNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := c.Fetch(context.Background(), "owner", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err: got %v, want ErrNotFound", err)
	}
}

func TestFetchServerError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal explosion"))
	})
	_, err := c.Fetch(context.Background(), "o", "r")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("500 should not surface as ErrNotFound: %v", err)
	}
}

func TestFetchRequiresOwnerAndRepo(t *testing.T) {
	c := NewClient(time.Second)
	if _, err := c.Fetch(context.Background(), "", "repo"); err == nil {
		t.Error("expected error for empty owner")
	}
	if _, err := c.Fetch(context.Background(), "owner", ""); err == nil {
		t.Error("expected error for empty repo")
	}
}

func TestFetchTimeout(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Block longer than the client timeout to force context-deadline.
		select {
		case <-time.After(500 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	})
	c.http.Timeout = 50 * time.Millisecond
	_, err := c.Fetch(context.Background(), "owner", "repo")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
