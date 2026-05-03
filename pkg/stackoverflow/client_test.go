package stackoverflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient(2*time.Second, "")
	c.baseURL = srv.URL
	return c
}

func TestExtractUserID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"with slug", "https://stackoverflow.com/users/22656/jon-skeet", 22656},
		{"without slug", "https://stackoverflow.com/users/22656", 22656},
		{"www prefix", "http://www.stackoverflow.com/users/22656/", 22656},
		{"empty", "", 0},
		{"questions page (false positive guard)", "https://stackoverflow.com/questions/12345", 0},
		{"tags page", "https://stackoverflow.com/tags/go", 0},
		{"non-numeric id", "https://stackoverflow.com/users/abc/foo", 0},
		{"meta site", "https://meta.stackoverflow.com/users/22656", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractUserID(tc.in); got != tc.want {
				t.Errorf("ExtractUserID(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFetchUserOK(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/22656" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		if r.URL.Query().Get("site") != "stackoverflow" {
			t.Errorf("site param missing: %v", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{
			"user_id": 22656,
			"display_name": "Jon Skeet",
			"reputation": 1500000,
			"badge_counts": {"bronze": 9000, "silver": 9000, "gold": 800},
			"link": "https://stackoverflow.com/users/22656/jon-skeet",
			"creation_date": 1222430705,
			"last_access_date": 1700000000
		}]}`))
	})

	got, err := c.FetchUser(context.Background(), 22656)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.UserID != 22656 {
		t.Errorf("UserID: got %d, want 22656", got.UserID)
	}
	if got.DisplayName != "Jon Skeet" {
		t.Errorf("DisplayName: got %q", got.DisplayName)
	}
	if got.Reputation != 1500000 {
		t.Errorf("Reputation: got %d, want 1500000", got.Reputation)
	}
	if got.BadgeGold != 800 || got.BadgeSilver != 9000 || got.BadgeBronze != 9000 {
		t.Errorf("Badges: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be parsed from creation_date")
	}
}

func TestFetchUserEmptyItemsTreatedAsNotFound(t *testing.T) {
	// SE API responds 200 + empty items for missing users.
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	_, err := c.FetchUser(context.Background(), 99999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestFetchUserNotFoundStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := c.FetchUser(context.Background(), 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 must surface as ErrNotFound, got %v", err)
	}
}

func TestFetchUserServerError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	_, err := c.FetchUser(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("500 should not surface as ErrNotFound: %v", err)
	}
}

func TestFetchUserRequiresPositiveID(t *testing.T) {
	c := NewClient(time.Second, "")
	if _, err := c.FetchUser(context.Background(), 0); err == nil {
		t.Error("expected error on zero user id")
	}
	if _, err := c.FetchUser(context.Background(), -1); err == nil {
		t.Error("expected error on negative user id")
	}
}

func TestFetchUserAPIKeyAttached(t *testing.T) {
	var sawKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.URL.Query().Get("key")
		_, _ = w.Write([]byte(`{"items":[{"user_id":1,"display_name":"x","reputation":1}]}`))
	}))
	defer srv.Close()
	c := NewClient(2*time.Second, "test-key-abc")
	c.baseURL = srv.URL
	if _, err := c.FetchUser(context.Background(), 1); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if sawKey != "test-key-abc" {
		t.Errorf("API key not propagated: got %q", sawKey)
	}
}
