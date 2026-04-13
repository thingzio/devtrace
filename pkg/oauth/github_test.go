package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thingzio/devtrace/pkg/net"
)

func TestBuildAuthURL(t *testing.T) {
	cfg := &Config{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost/callback",
		AuthURL:     "https://example.com/auth",
	}

	rawURL, state, err := BuildAuthURL(cfg)
	if err != nil {
		t.Fatalf("BuildAuthURL: %v", err)
	}

	if len(state) != 32 {
		t.Errorf("state length = %d, want 32 hex chars", len(state))
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing auth URL: %v", err)
	}

	q := parsed.Query()
	if got := q.Get("client_id"); got != "test-client-id" {
		t.Errorf("client_id = %q, want %q", got, "test-client-id")
	}
	if got := q.Get("redirect_uri"); got != "http://localhost/callback" {
		t.Errorf("redirect_uri = %q, want %q", got, "http://localhost/callback")
	}
	if got := q.Get("scope"); got != oauthScope {
		t.Errorf("scope = %q, want %q", got, oauthScope)
	}
	if got := q.Get("state"); got != state {
		t.Errorf("state param = %q, want %q", got, state)
	}

	// Two calls produce different states.
	_, state2, err := BuildAuthURL(cfg)
	if err != nil {
		t.Fatalf("BuildAuthURL second call: %v", err)
	}
	if state == state2 {
		t.Error("two calls returned identical state values")
	}
}

func TestExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "test-token",
		})
	}))
	defer srv.Close()

	orig := net.GitHubClient
	net.GitHubClient = srv.Client()
	defer func() { net.GitHubClient = orig }()

	cfg := &Config{
		ClientID:     "cid",
		ClientSecret: "csecret",
		TokenURL:     srv.URL,
	}

	ctx := context.Background()
	token, err := ExchangeCode(ctx, cfg, "some-code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token != "test-token" {
		t.Errorf("token = %q, want %q", token, "test-token")
	}
}

func TestExchangeCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "bad_code",
		})
	}))
	defer srv.Close()

	orig := net.GitHubClient
	net.GitHubClient = srv.Client()
	defer func() { net.GitHubClient = orig }()

	cfg := &Config{
		ClientID:     "cid",
		ClientSecret: "csecret",
		TokenURL:     srv.URL,
	}

	ctx := context.Background()
	_, err := ExchangeCode(ctx, cfg, "bad")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bad_code") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "bad_code")
	}
}

func TestFetchUser(t *testing.T) {
	want := GitHubUser{
		ID:        42,
		Login:     "octocat",
		Email:     "octocat@github.com",
		AvatarURL: "https://avatars.githubusercontent.com/u/42",
		Name:      "The Octocat",
		Company:   "GitHub",
		Location:  "San Francisco",
		Bio:       "Just a cat.",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok123")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	orig := net.GitHubClient
	net.GitHubClient = srv.Client()
	defer func() { net.GitHubClient = orig }()

	cfg := &Config{UserURL: srv.URL}

	ctx := context.Background()
	got, err := FetchUser(ctx, cfg, "tok123")
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if got.ID != want.ID || got.Login != want.Login || got.Email != want.Email {
		t.Errorf("user = %+v, want %+v", got, want)
	}
}

func TestFetchUserEmailFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubUser{
			ID:    99,
			Login: "nomail",
		})
	})
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"email": "secondary@example.com", "primary": false, "verified": true},
			{"email": "primary@example.com", "primary": true, "verified": true},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	orig := net.GitHubClient
	net.GitHubClient = srv.Client()
	defer func() { net.GitHubClient = orig }()

	cfg := &Config{
		UserURL:   srv.URL + "/user",
		EmailsURL: srv.URL + "/user/emails",
	}

	ctx := context.Background()
	got, err := FetchUser(ctx, cfg, "tok")
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if got.Email != "primary@example.com" {
		t.Errorf("email = %q, want %q", got.Email, "primary@example.com")
	}
}
