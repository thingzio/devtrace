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

// Package forges fetches public SSH keys from code-hosting platforms
// other than GitHub and produces SHA-256 fingerprints for cross-platform
// identity matching. v1 covers GitLab, Codeberg, and Sourcehut — the
// forges that publish keys at well-known unauthenticated endpoints.
//
// Bitbucket is intentionally out of scope: it has no public `.keys`
// endpoint as of this implementation. Adding it would require either
// API authentication or HTML scraping, both of which are higher-cost
// than the cryptographic signal warrants for v1.
//
// GPG fingerprint matching is also deferred. The `.gpg` endpoints
// exist on most forges but parsing OpenPGP packets requires a heavier
// library; SSH fingerprints alone produce the same T1 confidence
// signal for the (much larger) population of contributors who use
// SSH for git operations.
package forges

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Forge identifies a supported code-hosting platform. The constants
// double as JSON tags surfaced in the API response and as label
// strings in the UI; keep them stable across releases.
type Forge string

const (
	GitHub    Forge = "github"
	GitLab    Forge = "gitlab"
	Codeberg  Forge = "codeberg"
	Sourcehut Forge = "sourcehut"
)

// SupportedForges returns the non-GitHub forges the cross-VCS
// matcher walks. GitHub is the anchor (we always fetch its keys to
// compare against), so it is excluded from this list.
func SupportedForges() []Forge {
	return []Forge{GitLab, Codeberg, Sourcehut}
}

// ErrNotFound indicates the forge has no user with the requested
// handle (404, or 302 redirect to login on GitLab when the user
// doesn't exist). Distinguishes "user genuinely absent" from a
// transport error so callers can cache the empty result.
var ErrNotFound = errors.New("forges: user not found")

// keysURL returns the public SSH-keys URL for the given forge and
// username. Returns "" for unsupported forges; the caller should
// treat that as a programming error (see SupportedForges).
func keysURL(forge Forge, username string) string {
	u := url.PathEscape(username)
	switch forge {
	case GitHub:
		return "https://github.com/" + u + ".keys"
	case GitLab:
		return "https://gitlab.com/" + u + ".keys"
	case Codeberg:
		return "https://codeberg.org/" + u + ".keys"
	case Sourcehut:
		// Sourcehut prefixes user accounts with `~`. We expect the
		// caller to pass the bare handle without the tilde.
		return "https://meta.sr.ht/~" + u + ".keys"
	}
	return ""
}

// ProfileURL returns the human-facing profile URL for the given
// forge and username. Used by the UI to link out to the matched
// forge profile.
func ProfileURL(forge Forge, username string) string {
	u := url.PathEscape(username)
	switch forge {
	case GitHub:
		return "https://github.com/" + u
	case GitLab:
		return "https://gitlab.com/" + u
	case Codeberg:
		return "https://codeberg.org/" + u
	case Sourcehut:
		return "https://sr.ht/~" + u
	}
	return ""
}

// Client fetches public keys from the supported forges.
type Client struct {
	http        *http.Client
	urlOverride map[Forge]string // tests can swap out the per-forge base URL
}

// NewClient returns a Client with the given timeout. A zero or
// negative timeout falls back to a sane default.
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		// CheckRedirect: forge keys URLs sometimes redirect (GitLab
		// 302s missing users to /users/sign_in). We treat any
		// redirect as not-found rather than following — chasing the
		// redirect into a login page would parse junk as keys.
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// FetchSSHFingerprints returns the SHA-256 fingerprints of every
// public SSH key the user has published on the given forge. Each
// fingerprint is OpenSSH-formatted: "SHA256:<base64-no-padding>".
//
// Returns ErrNotFound on 404 or any 3xx response (see CheckRedirect
// note in NewClient). Empty result with nil error means the user
// exists but has published no keys — a real and meaningful state.
func (c *Client) FetchSSHFingerprints(ctx context.Context, forge Forge, username string) ([]string, error) {
	if username == "" {
		return nil, errors.New("forges: username required")
	}
	base := keysURL(forge, username)
	if base != "" && c.urlOverride != nil {
		if v, ok := c.urlOverride[forge]; ok {
			base = v + "/" + url.PathEscape(username) + ".keys"
		}
	}
	if base == "" {
		return nil, fmt.Errorf("forges: unsupported forge %q", forge)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return nil, fmt.Errorf("forges: build request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "devtrace-site/forges-client")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forges %s/%s: %w", forge, username, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		// proceed
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// Redirects out of the keys endpoint mean "user not found"
		// on GitLab and possibly other forges. See CheckRedirect.
		return nil, ErrNotFound
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("forges %s/%s: status %d: %s",
			forge, username, resp.StatusCode, string(body))
	}

	// Cap response size — even a prolific user with hundreds of
	// keys won't approach this. Defends against accidental redirect
	// to a giant HTML login page.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("forges %s/%s: read: %w", forge, username, err)
	}
	return parseSSHKeys(string(body)), nil
}

// parseSSHKeys extracts SHA-256 fingerprints from a public-keys
// response body. Each line is one key in the OpenSSH format:
//
//	<algorithm> <base64-payload> [comment]
//
// Lines that don't decode are silently skipped — forge responses
// occasionally include trailing whitespace or junk; we can't fail
// the whole user's match because of one malformed line.
func parseSSHKeys(body string) []string {
	out := make([]string, 0, 8)
	seen := make(map[string]bool)
	for _, line := range strings.Split(body, "\n") {
		fp := lineFingerprint(line)
		if fp == "" || seen[fp] {
			continue
		}
		seen[fp] = true
		out = append(out, fp)
	}
	return out
}

// lineFingerprint extracts a SHA-256 OpenSSH fingerprint from one
// line of a keys response. Returns "" for malformed input. The
// fingerprint format matches `ssh-keygen -lf` output: the SHA-256
// of the raw key bytes, base64-encoded without padding, prefixed
// with "SHA256:".
func lineFingerprint(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ""
	}
	keyBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(keyBytes)
	enc := base64.RawStdEncoding.EncodeToString(sum[:])
	return "SHA256:" + enc
}
