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

package tenant

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	dtnet "github.com/thingzio/devtrace/pkg/net"
)

// GitHubAppConfig holds the credentials needed to authenticate as a GitHub App.
type GitHubAppConfig struct {
	AppID      int64
	PrivateKey *rsa.PrivateKey
	BaseURL    string // default: https://api.github.com
}

// InstallationToken is a short-lived token scoped to a single installation.
type InstallationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoadGitHubAppConfig reads GITHUB_APP_ID and GITHUB_APP_KEY_PATH from env.
func LoadGitHubAppConfig() (*GitHubAppConfig, error) {
	appIDStr := os.Getenv("GITHUB_APP_ID")
	if appIDStr == "" {
		return nil, fmt.Errorf("GITHUB_APP_ID not set")
	}
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid GITHUB_APP_ID: %w", err)
	}

	keyPath := os.Getenv("GITHUB_APP_KEY_PATH")
	if keyPath == "" {
		return nil, fmt.Errorf("GITHUB_APP_KEY_PATH not set")
	}
	keyData, err := os.ReadFile(keyPath) //nolint:gosec // path from trusted env var, not user input
	if err != nil {
		return nil, fmt.Errorf("reading private key: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM data in %s", keyPath)
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	return &GitHubAppConfig{
		AppID:      appID,
		PrivateKey: key,
		BaseURL:    os.Getenv("GITHUB_API_BASE_URL"),
	}, nil
}

// CreateAppJWT creates a short-lived JWT signed with the App's private key.
func CreateAppJWT(cfg *GitHubAppConfig) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    strconv.FormatInt(cfg.AppID, 10),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(cfg.PrivateKey)
}

// MintInstallationToken exchanges an App JWT for a short-lived installation token.
func MintInstallationToken(ctx context.Context, cfg *GitHubAppConfig, installationID int64) (*InstallationToken, error) {
	appJWT, err := CreateAppJWT(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating app JWT: %w", err)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := dtnet.GitHubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("minting installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("mint token: unexpected status %d", resp.StatusCode)
	}

	var it InstallationToken
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("decoding installation token: %w", err)
	}
	return &it, nil
}
