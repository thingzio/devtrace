// Package profile extracts and classifies links and emails declared in a
// contributor's public profile (bio + blog field). Output decorates the
// contributor scorecard but never feeds the trust score directly: declared
// links are T4 confidence (the user said so), and only externally-verified
// matches (T1-T3) earn scoring weight in later phases.
package profile

import (
	"net/mail"
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
// bio and blog fields. Caller is responsible for plan-tier filtering of
// the email list (Free tier omits, Starter+ retains).
func Extract(bio, blog string) ([]model.LinkedAccount, []string) {
	accounts := extractURLs(bio, blog)
	emails := extractEmails(bio, blog)
	return accounts, emails
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

// classifyURL returns the platform name for a URL, or "personal_site" /
// "unknown" when no platform pattern matches.
func classifyURL(url string) string {
	for _, p := range platformPatterns {
		if p.pattern.MatchString(url) {
			return p.name
		}
	}
	// Heuristic fallback: bare-host URLs (e.g. https://example.com) are
	// treated as personal sites; more complex unmatched paths stay "unknown"
	// so we don't overclaim.
	bareHostRE := regexp.MustCompile(`^https?://[\w.-]+/?$`)
	if bareHostRE.MatchString(url) {
		return platformPersonalSite
	}
	return platformUnknown
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
