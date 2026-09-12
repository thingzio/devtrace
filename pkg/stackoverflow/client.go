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

// Package stackoverflow wraps the public Stack Exchange Data API
// (api.stackexchange.com) to surface a contributor's Stack Overflow
// reputation as a cross-platform credibility signal. Discovery flow:
// the contributor's GitHub profile (bio or blog) declares a
// stackoverflow.com/users/{id}/{slug} link → we extract the user ID
// → the SE API returns reputation, badges, and account metadata.
//
// SE rate limits: 300 requests/day per IP unauthenticated, 10000/day
// when a registered `key` is provided. Weekly cache keeps us comfortably
// under either ceiling.
package stackoverflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// ErrNotFound is returned when the SE API has no user with the
// requested ID. Distinguishes "user does not exist" from a transport
// or decode error so callers can cache the empty result.
var ErrNotFound = errors.New("stackoverflow: user not found")

// baseURL is the SE Data API endpoint. Override via NewClient for
// tests pointing at httptest servers.
const baseURL = "https://api.stackexchange.com/2.3"

// soSite is the Stack Exchange site slug for Stack Overflow.
const soSite = "stackoverflow"

// soURLRe matches /users/{id}/{slug}? on stackoverflow.com — the
// canonical shape produced by the SE Data API and used in profile
// links. Captures the numeric ID. Must match a strict prefix because
// other paths under stackoverflow.com (questions, tags) are common
// and would false-positive for a profile.
var soURLRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?stackoverflow\.com/users/(\d+)(?:/[\w-]+)?/?$`)

// ExtractUserID returns the Stack Overflow user ID encoded in the
// given URL, or 0 if the URL is not a recognizable SO profile link.
// Used by the service layer to convert a contributor's classified
// stackoverflow LinkedAccount into an API lookup.
func ExtractUserID(rawURL string) int64 {
	m := soURLRe.FindStringSubmatch(rawURL)
	if len(m) != 2 {
		return 0
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// Profile is the parsed SE user response. Only fields we surface
// are kept; the SE API returns considerably more (collectives,
// per-period reputation deltas, accept rate) that we currently do
// not display.
type Profile struct {
	UserID       int64
	DisplayName  string
	Reputation   int64
	BadgeBronze  int
	BadgeSilver  int
	BadgeGold    int
	URL          string
	CreatedAt    time.Time
	LastAccessAt time.Time
}

// Client is the Stack Overflow / Stack Exchange API client.
type Client struct {
	http    *http.Client
	baseURL string
	apiKey  string // optional; lifts the per-IP quota from 300/day to 10000/day
}

// NewClient returns a Client with the given timeout and optional
// API key. A zero or negative timeout falls back to a sane default;
// an empty apiKey runs against the unauthenticated quota.
func NewClient(timeout time.Duration, apiKey string) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: baseURL,
		apiKey:  apiKey,
	}
}

// FetchUser retrieves the SE user identified by the numeric ID on
// the Stack Overflow site. Returns ErrNotFound when the API returns
// an empty `items` list (the SE API responds 200 even for missing
// users; the empty list is the not-found signal).
func (c *Client) FetchUser(ctx context.Context, userID int64) (*Profile, error) {
	if userID <= 0 {
		return nil, errors.New("stackoverflow: invalid user id")
	}
	q := url.Values{}
	q.Set("site", soSite)
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}
	u := fmt.Sprintf("%s/users/%d?%s", c.baseURL, userID, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("stackoverflow: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", "devtrace-site/stackoverflow-client")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stackoverflow fetch %d: %w", userID, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// proceed
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("stackoverflow fetch %d: status %d: %s",
			userID, resp.StatusCode, string(body))
	}

	var raw struct {
		Items []struct {
			UserID      int64  `json:"user_id"`
			DisplayName string `json:"display_name"`
			Reputation  int64  `json:"reputation"`
			BadgeCounts struct {
				Bronze int `json:"bronze"`
				Silver int `json:"silver"`
				Gold   int `json:"gold"`
			} `json:"badge_counts"`
			Link           string `json:"link"`
			CreationDate   int64  `json:"creation_date"`
			LastAccessDate int64  `json:"last_access_date"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("stackoverflow decode %d: %w", userID, err)
	}
	if len(raw.Items) == 0 {
		return nil, ErrNotFound
	}
	u0 := raw.Items[0]
	out := &Profile{
		UserID:      u0.UserID,
		DisplayName: u0.DisplayName,
		Reputation:  u0.Reputation,
		BadgeBronze: u0.BadgeCounts.Bronze,
		BadgeSilver: u0.BadgeCounts.Silver,
		BadgeGold:   u0.BadgeCounts.Gold,
		URL:         u0.Link,
	}
	if u0.CreationDate > 0 {
		out.CreatedAt = time.Unix(u0.CreationDate, 0).UTC()
	}
	if u0.LastAccessDate > 0 {
		out.LastAccessAt = time.Unix(u0.LastAccessDate, 0).UTC()
	}
	return out, nil
}
