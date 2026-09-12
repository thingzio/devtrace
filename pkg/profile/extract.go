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

// Package profile extracts and classifies links and emails declared in a
// contributor's public profile (bio + blog field). Output decorates the
// contributor scorecard but never feeds the trust score directly: declared
// links are T4 confidence (the user said so), and only externally-verified
// matches (T1-T3) earn scoring weight in later phases.
package profile

import (
	"net/mail"
	neturl "net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/thingzio/devtrace/pkg/model"
)

const (
	confidenceTier = "T4" // declared link; v1 is uniform across all sources

	sourceBio  = "bio"
	sourceBlog = "blog"

	platformPersonalSite = "personal_site"
	platformUnknown      = "unknown"
)

// urlRE matches http(s) URLs; intentionally permissive on path/query characters
// since profiles often contain query strings. Stops at whitespace or markdown
// punctuation likely to be sentence boundaries rather than URL content.
var urlRE = regexp.MustCompile(`https?://[^\s<>"'()\[\]{}]+`)

// emailRE matches RFC-loose emails embedded in free text. Final validation
// goes through net/mail.ParseAddress for correctness.
var emailRE = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

// platformPatterns maps known platforms to URL shape regexes. Order matters:
// the first match wins, so platform-specific patterns must precede generic
// ones (e.g. mastodon's `/@user` shape would otherwise accept twitter URLs).
type platformRule struct {
	name    string
	pattern *regexp.Regexp
}

var platformPatterns = []platformRule{
	{"twitter", regexp.MustCompile(`(?i)^https?://(?:www\.|mobile\.)?(?:twitter|x)\.com/[\w-]+/?$`)},
	{"linkedin", regexp.MustCompile(`(?i)^https?://(?:www\.)?linkedin\.com/in/[\w-]+/?$`)},
	{"bluesky", regexp.MustCompile(`(?i)^https?://(?:www\.)?bsky\.app/profile/[\w.-]+/?$`)},
	{"youtube", regexp.MustCompile(`(?i)^https?://(?:www\.)?youtube\.com/(?:@[\w-]+|c/[\w-]+|user/[\w-]+|channel/[\w-]+)/?$`)},
	{"github", regexp.MustCompile(`(?i)^https?://github\.com/[\w-]+/?$`)},
	{"gitlab", regexp.MustCompile(`(?i)^https?://gitlab\.com/[\w-]+/?$`)},
	{"codeberg", regexp.MustCompile(`(?i)^https?://codeberg\.org/[\w-]+/?$`)},
	{"bitbucket", regexp.MustCompile(`(?i)^https?://bitbucket\.org/[\w-]+/?$`)},
	{"stackoverflow", regexp.MustCompile(`(?i)^https?://(?:www\.)?stackoverflow\.com/users/\d+(?:/[\w-]+)?/?$`)},
	{"dev.to", regexp.MustCompile(`(?i)^https?://(?:www\.)?dev\.to/[\w-]+/?$`)},
	{"medium", regexp.MustCompile(`(?i)^https?://(?:www\.)?medium\.com/@?[\w-]+/?$`)},
	{"keybase", regexp.MustCompile(`(?i)^https?://keybase\.io/[\w-]+/?$`)},
	{"mastodon", regexp.MustCompile(`(?i)^https?://[\w.-]+/@[\w.-]+/?$`)},
}

// Extract returns LinkedAccounts and emails declared in the contributor's
// bio, blog, and dedicated profile email fields. Caller is responsible for
// plan-tier filtering of the email list (Free tier omits, Starter+ retains).
//
// The profileEmail argument is the GitHub profile's `email` field — the
// dedicated contact-email slot, distinct from any address that might also
// appear inside bio text. Production data showed that excluding this field
// missed almost every contributor's public email; including it here is the
// canonical fix.
func Extract(bio, blog, profileEmail string) ([]model.LinkedAccount, []string) {
	accounts := extractURLs(bio, blog)
	emails := extractEmails(bio, blog)
	if profileEmail != "" {
		emails = mergeEmail(emails, profileEmail)
	}
	return accounts, emails
}

// mergeEmail validates and inserts an email into the deduped list,
// preserving alphabetical order so output is deterministic across calls.
func mergeEmail(emails []string, candidate string) []string {
	e := strings.ToLower(strings.TrimSpace(candidate))
	if e == "" {
		return emails
	}
	if _, err := mail.ParseAddress(e); err != nil {
		return emails
	}
	for _, existing := range emails {
		if existing == e {
			return emails
		}
	}
	emails = append(emails, e)
	sort.Strings(emails)
	return emails
}

// extractURLs collects URLs from bio (free text) and the dedicated blog
// field. URLs from blog are not re-found in bio — both sources contribute
// independently. Duplicates across sources are folded; bio source wins
// when the same URL appears in both.
func extractURLs(bio, blog string) []model.LinkedAccount {
	seen := make(map[string]model.LinkedAccount)

	for _, u := range urlRE.FindAllString(bio, -1) {
		key, acct := buildAccount(u, sourceBio)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; !ok {
			seen[key] = acct
		}
	}

	if blog != "" {
		// The blog field may itself be a bare URL or have a leading scheme.
		blogURL := blog
		if !strings.HasPrefix(strings.ToLower(blogURL), "http://") &&
			!strings.HasPrefix(strings.ToLower(blogURL), "https://") {
			blogURL = "https://" + blogURL
		}
		key, acct := buildAccount(blogURL, sourceBlog)
		if key != "" {
			if _, ok := seen[key]; !ok {
				seen[key] = acct
			}
		}
	}

	out := make([]model.LinkedAccount, 0, len(seen))
	for _, a := range seen {
		out = append(out, a)
	}
	// Stable order: platform asc, url asc — keeps responses deterministic
	// across calls for the same input.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return out[i].URL < out[j].URL
	})
	return out
}

func buildAccount(rawURL, source string) (string, model.LinkedAccount) {
	url := strings.TrimRight(rawURL, ".,;:)]}")
	url = strings.TrimSpace(url)
	if url == "" {
		return "", model.LinkedAccount{}
	}
	platform := classifyURL(url)
	return strings.ToLower(url), model.LinkedAccount{
		Platform: platform,
		URL:      url,
		Source:   source,
		Tier:     confidenceTier,
	}
}

// knownPlatformHosts is the set of hostnames whose URLs we know how to
// recognize via platformPatterns. When a URL's host is in this set but
// the pattern didn't match (unusual URL shape, e.g. a github repo URL
// rather than a user URL), classify as "unknown" rather than overclaim
// "personal_site". When the host is NOT in this set, the URL is on
// some other domain and is most likely a personal site or blog.
var knownPlatformHosts = map[string]bool{
	"twitter.com":       true,
	"x.com":             true,
	"linkedin.com":      true,
	"bsky.app":          true,
	"youtube.com":       true,
	"github.com":        true,
	"gitlab.com":        true,
	"codeberg.org":      true,
	"bitbucket.org":     true,
	"stackoverflow.com": true,
	"dev.to":            true,
	"medium.com":        true,
	"keybase.io":        true,
}

// classifyURL returns the platform name for a URL. URLs matching a
// platform pattern get that platform's name. URLs on a known-platform
// host but with an unusual shape get "unknown" (we know the host but
// can't normalize). Everything else with a parseable host is treated
// as "personal_site".
func classifyURL(rawURL string) string {
	for _, p := range platformPatterns {
		if p.pattern.MatchString(rawURL) {
			return p.name
		}
	}
	u, err := neturl.Parse(rawURL)
	if err != nil || u.Host == "" {
		return platformUnknown
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "mobile.")
	host = strings.TrimPrefix(host, "m.")
	if knownPlatformHosts[host] {
		return platformUnknown
	}
	return platformPersonalSite
}

// extractEmails collects valid emails from bio and the dedicated blog
// field, deduped and sorted. net/mail.ParseAddress is the final validator
// to discard regex false positives (e.g. version numbers like "1.2@3.4").
func extractEmails(bio, blog string) []string {
	seen := make(map[string]struct{})
	for _, candidate := range emailRE.FindAllString(bio+" "+blog, -1) {
		if _, err := mail.ParseAddress(candidate); err != nil {
			continue
		}
		seen[strings.ToLower(candidate)] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
