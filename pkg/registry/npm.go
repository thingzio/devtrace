// Package registry wraps public package-registry APIs used to detect
// whether a contributor publishes packages under their handle. v1
// covers npm; pypi is a deliberate gap (no public reverse-lookup API).
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// ErrNotFound is returned when the registry has no packages for the
// requested user. Distinguishes "user genuinely publishes nothing"
// (or never registered the handle) from a transport / decode error,
// so callers can cache the empty result without retrying.
var ErrNotFound = errors.New("registry: user not found or has no packages")

// npmBaseURL is the npm registry endpoint. Override via NewNPMClient
// for tests that point at httptest servers.
const npmBaseURL = "https://registry.npmjs.org"

// npmRoleWrite is the npm permission string indicating a publisher
// (write-access) relationship. Read-only roles are filtered out so
// the publisher headline reflects packages the user actually owns
// or co-maintains.
const npmRoleWrite = "write"

// Package is one entry in a publisher's package list as parsed from
// the registry response. Role mirrors the registry's permission
// vocabulary (npm: "write" / "read"; "write" implies maintainer
// authority on the package).
type Package struct {
	Name string
	Role string
	URL  string
}

// NPMClient queries the npm registry for a user's published packages.
type NPMClient struct {
	http    *http.Client
	baseURL string
}

// NewNPMClient returns an NPMClient with the given timeout. Negative
// or zero timeout falls back to a sane default; callers should not
// run unbounded against the registry.
func NewNPMClient(timeout time.Duration) *NPMClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &NPMClient{
		http:    &http.Client{Timeout: timeout},
		baseURL: npmBaseURL,
	}
}

// FetchUserPackages returns the published packages owned by the given
// npm username. The npm endpoint returns a JSON map of package-name
// to permission (npmRoleWrite / "read"); we surface only npmRoleWrite roles
// (the publisher relationship), sorted alphabetically and capped at
// topLimit for display. The full count is preserved in the returned
// total regardless of cap. Returns ErrNotFound on 404 (user has no
// publisher account).
//
// We assume the npm username matches the GitHub username — common
// (Sindre, dominictarr, isaacs, etc.) but not universal. v1
// limitation; we may add owned-repo cross-reference fallback later.
func (c *NPMClient) FetchUserPackages(ctx context.Context, username string, topLimit int) (total int, top []Package, err error) {
	if username == "" {
		return 0, nil, errors.New("registry: username required")
	}
	if topLimit <= 0 {
		topLimit = 5
	}
	u := fmt.Sprintf("%s/-/user/%s/package", c.baseURL, url.PathEscape(username))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("npm: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "devtrace-site/registry-client")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("npm fetch %s: %w", username, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// proceed
	case http.StatusNotFound:
		return 0, nil, ErrNotFound
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, nil, fmt.Errorf("npm fetch %s: status %d: %s",
			username, resp.StatusCode, string(body))
	}

	var raw map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, nil, fmt.Errorf("npm decode %s: %w", username, err)
	}

	// Filter to npmRoleWrite (publisher) roles. Some packages list the user
	// only as a reader; those don't represent a publishing relationship
	// and would inflate the headline count.
	publisherPkgs := make([]string, 0, len(raw))
	for name, role := range raw {
		if role == npmRoleWrite {
			publisherPkgs = append(publisherPkgs, name)
		}
	}
	sort.Strings(publisherPkgs)

	total = len(publisherPkgs)
	if topLimit > total {
		topLimit = total
	}
	top = make([]Package, 0, topLimit)
	for _, name := range publisherPkgs[:topLimit] {
		top = append(top, Package{
			Name: name,
			Role: npmRoleWrite,
			URL:  "https://www.npmjs.com/package/" + name,
		})
	}
	return total, top, nil
}
