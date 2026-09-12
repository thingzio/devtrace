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

package bot

import "strings"

// knownBots is a set of known bot/AI usernames (lowercased).
// Aligned with DevPulse bot filtering list, extended with common CI and
// merge bots that operate under bare usernames (no "[bot]" suffix).
var knownBots = map[string]bool{
	// AI assistants
	"copilot":          true,
	"github-copilot":   true,
	"claude":           true,
	"anthropic-claude": true,
	"coderabbitai":     true,

	// Dependency / security automation
	"dependabot":       true,
	"renovate":         true,
	"greenkeeper":      true,
	"snyk-bot":         true,
	"whitesource-bolt": true,

	// CI / coverage
	"codecov":   true,
	"sonar-bot": true,
	"circleci":  true,
	"jenkins":   true,
	"travisci":  true,

	// Merge / release automation (often configured without [bot] suffix)
	"mergify":              true,
	"kodiak":               true,
	"bulldozer":            true,
	"semantic-release-bot": true,
	"changeset-bot":        true,
	"pre-commit-ci":        true,

	// Project / community automation
	"stale":           true,
	"allcontributors": true,
	"imgbot":          true,
	"imgbotapp":       true,

	// Hosting platforms (PR previews)
	"netlify": true,
	"vercel":  true,

	// Google / corporate
	"googleapis-bot": true,
	"google-cla":     true,
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
