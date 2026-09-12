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

package server

// Template data-map keys. Shared across handler files to satisfy goconst
// without forcing each handler to redeclare the same string. These are
// the JSON / HTML template field names — keep stable to avoid breaking
// templates.
const (
	tmplTitle        = "Title"
	tmplVersion      = "Version"
	tmplCommit       = "Commit"
	tmplDate         = "Date"
	tmplAdmin        = "Admin"
	tmplHelp         = "Help"
	tmplName         = "name"
	tmplPlanFree     = "free"
	tmplPlanPro      = "pro"
	tmplErrorKey     = "error"
	tmplNavUser      = "NavUser"
	tmplNavAvatar    = "NavAvatar"
	tmplUsername     = "Username"
	tmplGrade        = "Grade"
	tmplGradeClass   = "GradeClass"
	tmplBio          = "bio"
	tmplModelVersion = "ModelVersion"
	tmplValue        = "Value"

	// Lowercase page-data keys used by the dashboard and settings templates.
	// Distinct from tmplUsername ("Username"), which the scorecard uses.
	tmplKeyUsername  = "username"
	tmplKeyPlan      = "plan"
	tmplKeyRateLimit = "rate_limit"

	msgRateLimitExceeded = "rate limit exceeded"

	// authGitHubPath is the OAuth start URL. Hoisted because handlers,
	// middleware, and ratelimit JSON envelopes all need to reference it.
	authGitHubPath = "/auth/github"

	// dashboardPath is the default post-auth landing page.
	dashboardPath = "/dashboard"
)

// flashMessage maps a ?msg= code to display prose. Unknown codes return the
// empty string: the parameter is attacker-controllable, and rendering it
// verbatim would let a crafted link put arbitrary text on a victim's page.
func flashMessage(code string) string {
	return flashMessages[code]
}

var flashMessages = map[string]string{
	"install_app":             "One more step — install the GitHub App to finish setup.",
	"watchlist_added":         "Watchlist added.",
	"watchlist_removed":       "Watchlist removed.",
	"watchlist_updated":       "Watchlist updated.",
	"watchlist_limit":         "Watchlist limit reached for your plan.",
	"watchlist_limit_error":   "Could not check your watchlist limit. Try again.",
	"watchlist_add_error":     "Could not add that watchlist. Try again.",
	"watchlist_update_error":  "Could not update that watchlist. Try again.",
	"watchlist_delete_denied": "That watchlist cannot be deleted.",
	"invalid_target":          "Invalid org or repo name.",
	"invalid_request":         "Invalid request.",
}
