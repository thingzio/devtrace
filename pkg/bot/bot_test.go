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

package bot_test

import (
	"testing"

	"github.com/thingzio/devtrace/pkg/bot"
)

func TestIsBot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		username string
		want     bool
	}{
		// [bot] suffix — GitHub App convention.
		{"dependabot[bot]", true},
		{"openshift-merge-bot[bot]", true},
		{"renovate[bot]", true},
		{"codecov[bot]", true},
		{"some-custom-app[bot]", true},

		// Known bot names (case-insensitive).
		{"copilot", true},
		{"Copilot", true},
		{"COPILOT", true},
		{"github-copilot", true},
		{"claude", true},
		{"anthropic-claude", true},
		{"dependabot", true},
		{"renovate", true},
		{"greenkeeper", true},
		{"snyk-bot", true},
		{"imgbot", true},

		// Expanded merge / CI / automation bots without [bot] suffix.
		{"mergify", true},
		{"kodiak", true},
		{"bulldozer", true},
		{"semantic-release-bot", true},
		{"changeset-bot", true},
		{"pre-commit-ci", true},
		{"coderabbitai", true},
		{"sonar-bot", true},
		{"circleci", true},
		{"jenkins", true},
		{"travisci", true},
		{"googleapis-bot", true},
		{"google-cla", true},
		{"whitesource-bolt", true},
		{"imgbotapp", true},

		// Real humans.
		{"torvalds", false},
		{"octocat", false},
		{"alice", false},
		{"bot-lover", false},       // contains "bot" but not a suffix
		{"robot", false},           // contains "bot" but not in list
		{"mybot", false},           // not in known list
		{"dependabot-user", false}, // different from "dependabot"
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			t.Parallel()
			if got := bot.IsBot(tt.username); got != tt.want {
				t.Errorf("IsBot(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}
