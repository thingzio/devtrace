package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/thingzio/devtrace/pkg/net"
)

const (
	defaultAuthURL   = "https://github.com/login/oauth/authorize"
	defaultTokenURL  = "https://github.com/login/oauth/access_token" //nolint:gosec // not a credential
	defaultUserURL   = "https://api.github.com/user"
	defaultEmailsURL = "https://api.github.com/user/emails"
	oauthScope       = "read:user user:email"
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AuthURL      string // override for testing
	TokenURL     string // override for testing
	UserURL      string // override for testing
	EmailsURL    string // override for testing
}

type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	Name      string `json:"name"`
	Company   string `json:"company"`
	Location  string `json:"location"`
	Bio       string `json:"bio"`
}

func BuildAuthURL(cfg *Config) (string, string, error) {
	state, err := randomState()
	if err != nil {
		return "", "", fmt.Errorf("generating oauth state: %w", err)
	}
	authURL := cfg.AuthURL
	if authURL == "" {
		authURL = defaultAuthURL
	}
	v := url.Values{
		"client_id":    {cfg.ClientID},
		"redirect_uri": {cfg.RedirectURL},
		"scope":        {oauthScope},
		"state":        {state},
	}
	return authURL + "?" + v.Encode(), state, nil
}

func ExchangeCode(ctx context.Context, cfg *Config, code string) (string, error) {
	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	v := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(v.Encode()))
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := net.GitHubClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchanging code: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("oauth error: %s", result.Error)
	}
	if result.AccessToken == "" {
		return "", errors.New("empty access token")
	}
	return result.AccessToken, nil
}

func FetchUser(ctx context.Context, cfg *Config, token string) (*GitHubUser, error) {
	userURL := cfg.UserURL
	if userURL == "" {
		userURL = defaultUserURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := net.GitHubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user API: status %d", resp.StatusCode)
	}
	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user: %w", err)
	}
	if user.Email == "" {
		emailsURL := cfg.EmailsURL
		if emailsURL == "" {
			emailsURL = defaultEmailsURL
		}
		user.Email = fetchPrimaryEmail(ctx, emailsURL, token)
	}
	return &user, nil
}

func fetchPrimaryEmail(ctx context.Context, emailsURL, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, emailsURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := net.GitHubClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return ""
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}
	return ""
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}
