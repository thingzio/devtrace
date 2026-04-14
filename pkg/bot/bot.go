package bot

import "strings"

// knownBots is a set of known bot/AI usernames (lowercased).
// Aligned with DevPulse bot filtering list, extended with common CI bots.
var knownBots = map[string]bool{
	"copilot":          true,
	"github-copilot":   true,
	"claude":           true,
	"anthropic-claude": true,
	"dependabot":       true,
	"renovate":         true,
	"greenkeeper":      true,
	"snyk-bot":         true,
	"codecov":          true,
	"stale":            true,
	"allcontributors":  true,
	"imgbot":           true,
	"netlify":          true,
	"vercel":           true,
}

// IsBot returns true if the username belongs to a known bot account.
// Detection rules:
//  1. Username ends with "[bot]" (GitHub App convention)
//  2. Username is in the known bots list (case-insensitive)
func IsBot(username string) bool {
	if strings.HasSuffix(username, "[bot]") {
		return true
	}
	return knownBots[strings.ToLower(username)]
}
