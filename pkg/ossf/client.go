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

// Package ossf wraps the OpenSSF Scorecard public API
// (api.securityscorecards.dev). The API returns repo-level project
// quality assessments with an aggregate score (0–10) and a list of
// per-check results. The endpoint is unauthenticated and accepts only
// GET requests; we make small, bounded calls behind a short timeout.
//
// Per-check scores: -1 means the check did not apply (e.g., Fuzzing
// on a docs-only repo); 0–10 are real scores. Callers should preserve
// the distinction when surfacing results to users.
package ossf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrNotFound is returned when the OSSF API has no scorecard for the
// requested repo. This is a normal outcome (small or new repos),
// not a transport error — callers should treat it as "no data" rather
// than failure.
var ErrNotFound = errors.New("ossf scorecard: not found")

// baseURL is the OpenSSF Scorecard project lookup endpoint.
const baseURL = "https://api.securityscorecards.dev"

// Scorecard is the parsed response. Only fields we surface are kept;
// the upstream payload includes additional metadata (raw check details,
// remediation hints) that we currently do not display.
type Scorecard struct {
	Score        float64
	Date         time.Time
	Commit       string
	ScorecardVer string
	Checks       []Check
}

// Check is a single check result.
type Check struct {
	Name   string
	Score  int
	Reason string
	DocURL string
}

// Client is the OSSF Scorecard HTTP client.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient returns a Client with the given timeout. A zero or
// negative timeout falls back to a sane default; callers should not
// run unbounded — the OSSF API has been known to return large
// responses for large monorepos.
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: baseURL,
	}
}

// Fetch retrieves the scorecard for the given GitHub-hosted repo.
// owner and repo must both be non-empty. Returns ErrNotFound for a
// 404; any other non-2xx is wrapped as an error with the status code.
func (c *Client) Fetch(ctx context.Context, owner, repo string) (*Scorecard, error) {
	if owner == "" || repo == "" {
		return nil, errors.New("ossf scorecard: owner and repo required")
	}
	u := fmt.Sprintf("%s/projects/github.com/%s/%s",
		c.baseURL,
		url.PathEscape(owner),
		url.PathEscape(repo),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("ossf scorecard: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "devtrace-site/ossf-client")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ossf scorecard %s/%s: %w", owner, repo, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// proceed
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		// Drain a small prefix to surface error context without buffering large bodies.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ossf scorecard %s/%s: status %d: %s",
			owner, repo, resp.StatusCode, string(body))
	}

	var raw struct {
		Date string `json:"date"`
		Repo struct {
			Commit string `json:"commit"`
		} `json:"repo"`
		Scorecard struct {
			Version string `json:"version"`
		} `json:"scorecard"`
		Score  float64 `json:"score"`
		Checks []struct {
			Name          string `json:"name"`
			Score         int    `json:"score"`
			Reason        string `json:"reason"`
			Documentation struct {
				URL string `json:"url"`
			} `json:"documentation"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ossf scorecard %s/%s: decode: %w", owner, repo, err)
	}

	out := &Scorecard{
		Score:        raw.Score,
		Commit:       raw.Repo.Commit,
		ScorecardVer: raw.Scorecard.Version,
		Checks:       make([]Check, 0, len(raw.Checks)),
	}
	if raw.Date != "" {
		// API returns YYYY-MM-DD; tolerate full RFC3339 for forward-compat.
		if t, err := time.Parse("2006-01-02", raw.Date); err == nil {
			out.Date = t
		} else if t, err := time.Parse(time.RFC3339, raw.Date); err == nil {
			out.Date = t
		}
	}
	for _, c := range raw.Checks {
		out.Checks = append(out.Checks, Check{
			Name:   c.Name,
			Score:  c.Score,
			Reason: c.Reason,
			DocURL: c.Documentation.URL,
		})
	}
	return out, nil
}
