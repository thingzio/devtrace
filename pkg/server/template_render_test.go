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

import (
	"bytes"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/plan"
)

// truncate is a small helper used by failure-message assembly to keep
// the body excerpt short enough to read in test output.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	var b bytes.Buffer
	b.WriteString(s[:n])
	b.WriteString("…(truncated)")
	return b.String()
}

// TestTemplateConstKeysAreCanonical asserts the const VALUES in
// template_keys.go are the strings we actually want — guards against
// silent typos in the const declarations themselves.
func TestTemplateConstKeysAreCanonical(t *testing.T) {
	checks := map[string]string{
		"tmplTitle":            "Title",
		"tmplVersion":          "Version",
		"tmplCommit":           "Commit",
		"tmplDate":             "Date",
		"tmplAdmin":            "Admin",
		"tmplHelp":             "Help",
		"tmplName":             "name",
		"tmplPlanFree":         "free",
		"tmplPlanPro":          "pro",
		"tmplErrorKey":         "error",
		"tmplNavUser":          "NavUser",
		"tmplNavAvatar":        "NavAvatar",
		"msgRateLimitExceeded": "rate limit exceeded",
	}
	got := map[string]string{
		"tmplTitle":            tmplTitle,
		"tmplVersion":          tmplVersion,
		"tmplCommit":           tmplCommit,
		"tmplDate":             tmplDate,
		"tmplAdmin":            tmplAdmin,
		"tmplHelp":             tmplHelp,
		"tmplName":             tmplName,
		"tmplPlanFree":         tmplPlanFree,
		"tmplPlanPro":          tmplPlanPro,
		"tmplErrorKey":         tmplErrorKey,
		"tmplNavUser":          tmplNavUser,
		"tmplNavAvatar":        tmplNavAvatar,
		"msgRateLimitExceeded": msgRateLimitExceeded,
	}
	for name, want := range checks {
		if got[name] != want {
			t.Errorf("%s: got %q, want %q", name, got[name], want)
		}
	}
}

// TestLayoutFieldsCoveredByConsts verifies every top-level field
// reference in layout.html is satisfied by a template_keys const or by
// a known dynamic field (NavUser, NavAvatar, etc.). Catches the
// regression class where a const value drifts from the template's
// `{{.Field}}` reference, leaving fields silently empty.
func TestLayoutFieldsCoveredByConsts(t *testing.T) {
	layoutBytes, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("read layout.html: %v", err)
	}

	// Match top-level field references: {{.Title}}, {{ .Version }}, etc.
	// Excludes nested references like {{.User.Name}} which are not
	// surfaced through template_keys.go.
	fieldRE := regexp.MustCompile(`\{\{[\s-]*\.([A-Z][A-Za-z0-9]*)\b`)
	matches := fieldRE.FindAllStringSubmatch(string(layoutBytes), -1)

	referenced := make(map[string]bool, len(matches))
	for _, m := range matches {
		referenced[m[1]] = true
	}
	if len(referenced) == 0 {
		t.Fatal("found no field references in layout.html — regex likely broken")
	}

	// Fields satisfied by template_keys.go const VALUES.
	covered := map[string]bool{
		tmplTitle:   true,
		tmplVersion: true,
		tmplCommit:  true,
		tmplDate:    true,
		tmplAdmin:   true,
		tmplHelp:    true,
	}
	// Layout-context fields covered by template_keys.go consts but
	// listed here separately for clarity since they're set by handlers
	// dynamically rather than being product/UI labels.
	allowList := map[string]bool{
		tmplNavUser:   true,
		tmplNavAvatar: true,
	}

	for field := range referenced {
		if covered[field] || allowList[field] {
			continue
		}
		t.Errorf("layout.html references {{.%s}} but no template_keys.go "+
			"const has that value and it isn't in the allow-list. "+
			"Either add a const, add it to allowList, or fix the typo.",
			field)
	}
}

// TestRoutesUsingErrorEnvelope is a placeholder marker — real tests
// covering the writeError JSON shape live in handler_*_test.go and
// rely on the same map[string]string{tmplErrorKey: msg} contract.
func TestRoutesUsingErrorEnvelope(t *testing.T) {
	if tmplErrorKey != "error" {
		t.Errorf("API contract: error envelope key changed unexpectedly: %q", tmplErrorKey)
	}
}

// TestScorecardRendersEnrichmentSections renders the scorecard with a
// realistic Enrichment payload and asserts each section actually
// surfaces in the HTML. Catches regressions where a template field
// reference drifts from the model struct field name (Go templates fail
// silently for missing fields when rendering against any/interface).
func TestScorecardRendersEnrichmentSections(t *testing.T) {
	first := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	enrichment := &model.Enrichment{
		LifetimeActivity: &model.LifetimeActivity{
			PRsOpened: 79, PRsMerged: 50, PRsClosed: 5,
			ReviewsGiven: 143, IssueComments: 106,
			IssuesOpened: 12, IssuesClosed: 8, ActiveDays: 87,
			FirstActive: &first, LastActive: &last,
		},
		Reciprocity: &model.Reciprocity{
			ReviewsPerPR:       1.81,
			IssueClosingRate:   0.67,
			IssueCommentsPerPR: 1.34,
		},
		TopContributedRepos: []model.RepoContribution{
			{Repo: "kubernetes/kubernetes", Activities: 42, LastContribution: last},
			{Repo: "moby/moby", Activities: 18, LastContribution: last},
		},
		LinkedAccounts: []model.LinkedAccount{
			{Platform: "personal_site", URL: "https://example.dev/blog", Source: "blog", Tier: "T4"},
			{Platform: "twitter", URL: "https://x.com/jane", Source: "bio", Tier: "T4"},
		},
		Emails: []string{"jane@example.com"},
		SecurityCredits: &model.SecurityCredits{
			ReporterCount: 5, FixerCount: 2, OtherCount: 0,
			BySeverity: map[string]int{"critical": 2, "high": 3, "moderate": 1, "low": 1},
			Recent: []model.SecurityCredit{
				{
					AdvisoryID: "GHSA-aaaa-bbbb-cccc",
					CreditType: "reporter", Severity: "critical",
					CVEID: "CVE-2024-001", Summary: "Critical RCE in foo",
					PublishedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		OwnedRepos: &model.OwnedRepos{
			TotalStars: 8534, TotalRepos: 76,
			Top: []model.OwnedRepo{
				{Name: "user/best-repo", Stars: 2400, Language: "Go", Description: "A useful Go thing"},
				{Name: "user/other-repo", Stars: 1100, Language: "Python"},
			},
			Languages: []model.LanguageBucket{
				{Language: "Go", Repos: 32, Share: 0.42},
				{Language: "Shell", Repos: 24, Share: 0.32},
			},
		},
		OSSFScorecard: &model.OSSFScorecard{
			Score:        7.5,
			Date:         time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
			ScorecardVer: "v5.0.0",
			Checks: []model.OSSFCheck{
				{Name: "Code-Review", Score: 10, Reason: "all changesets reviewed", DocURL: "https://example.com/cr"},
				{Name: "Fuzzing", Score: -1, Reason: "project is not fuzzed", DocURL: "https://example.com/fz"},
				{Name: "License", Score: 9, Reason: "license file detected"},
			},
		},
		Publisher: &model.Publisher{
			TotalPackages: 42,
			NPM: &model.RegistryProfile{
				PackageCount: 42,
				Top: []model.Package{
					{Name: "alpha-pkg", Role: "write", URL: "https://www.npmjs.com/package/alpha-pkg"},
					{Name: "beta-pkg", Role: "write", URL: "https://www.npmjs.com/package/beta-pkg"},
				},
			},
		},
		StackOverflow: func() *model.StackOverflow {
			created := time.Date(2008, 9, 26, 12, 0, 0, 0, time.UTC)
			return &model.StackOverflow{
				UserID:      22656,
				DisplayName: "Jon Skeet",
				Reputation:  1500000,
				BadgeBronze: 9000,
				BadgeSilver: 9000,
				BadgeGold:   800,
				URL:         "https://stackoverflow.com/users/22656/jon-skeet",
				CreatedAt:   &created,
			}
		}(),
		CrossVCS: &model.CrossVCS{
			TotalMatched: 2,
			Matches: []model.ForgeMatch{
				{Forge: "gitlab", URL: "https://gitlab.com/jane", KeyCount: 3, MatchedKeys: 2},
				{Forge: "codeberg", URL: "https://codeberg.org/jane", KeyCount: 1, MatchedKeys: 1},
			},
		},
	}

	data := scorecardTestData(enrichment)
	rec := httptest.NewRecorder()
	renderTemplate(rec, "scorecard.html", data)

	if rec.Code != 200 {
		t.Fatalf("render failed: status %d, body: %s", rec.Code, truncate(rec.Body.String(), 500))
	}
	body := rec.Body.String()

	mustContain := []string{
		// Activity stat tiles (PRs Opened, Reviews Given, Issues Opened,
		// Issues Closed, Issue Comments, Active Days). PRs Merged tile
		// is intentionally absent; see template comment in scorecard.html.
		">Activity ", ">79<", ">143<", ">12<", ">8<", ">106<", ">87<",
		"Issues Opened", "Issues Closed",
		"Tracked Jan 2026", "Apr 2026",
		// Scope badges
		"scope-badge-global", "scope-badge-repo",
		// Reciprocity
		"Reciprocity", "1.81", "67%", "1.34",
		// Top contributed repos
		"Top Contributing To", "kubernetes/kubernetes", "42 active hours",
		"https://github.com/kubernetes/kubernetes",
		// Owned repos
		"Owned Repositories", "76 non-fork", "8534 total star",
		"user/best-repo", "2400", "A useful Go thing",
		"Language Footprint", ">Go<", ">Shell<",
		// Linked accounts + emails
		"Linked Accounts",
		"personal_site", "https://example.dev/blog",
		"twitter", "https://x.com/jane",
		"Public email", "jane@example.com",
		// Security credits
		"Security Credits", "GHSA-aaaa-bbbb-cccc",
		"CVE-2024-001", "Critical RCE in foo",
		">5<", ">2<", // ReporterCount, FixerCount tile values
		"2 critical", "3 high",
		// OSSF Scorecard
		"OSSF Scorecard", "7.5", "v5.0.0", "Apr 27, 2026",
		"Code-Review", "all changesets reviewed",
		"Fuzzing", "N/A", // -1 score renders as N/A
		"License", "license file detected",
		"https://example.com/cr",
		// Publisher (npm v1)
		"Package Publisher", "npm", "42 packages",
		"alpha-pkg", "beta-pkg",
		"https://www.npmjs.com/package/alpha-pkg",
		// Stack Overflow
		"Stack Overflow", "Jon Skeet",
		"Sep 2008", // CreatedAt formatted as "Member Since"
		"https://stackoverflow.com/users/22656/jon-skeet",
		// Cross-VCS T1 fingerprint matches
		"Cross-Platform Identity",
		">gitlab<", ">codeberg<",
		"https://gitlab.com/jane", "https://codeberg.org/jane",
		"2 of 3 keys matches GitHub",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("scorecard missing %q\nbody (first 1500 chars): %s",
				want, truncate(body, 1500))
		}
	}
	// PRs Merged tile is intentionally NOT rendered — the per-user
	// merge-click count is a misleading signal in CI-merge workflows.
	for _, mustNotContain := range []string{"PRs Merged"} {
		if strings.Contains(body, mustNotContain) {
			t.Errorf("scorecard rendered %q despite intentional removal", mustNotContain)
		}
	}
}

// TestScorecardWithoutEnrichmentRenders asserts scorecard still works
// when Enrichment is nil (sparse contributor, suspended account, etc.).
func TestScorecardWithoutEnrichmentRenders(t *testing.T) {
	data := scorecardTestData(nil)
	rec := httptest.NewRecorder()
	renderTemplate(rec, "scorecard.html", data)
	if rec.Code != 200 {
		t.Fatalf("render failed: status %d, body: %s", rec.Code, truncate(rec.Body.String(), 500))
	}
	body := rec.Body.String()
	// The enrichment headings must NOT appear when no data is provided.
	mustNotRender := []string{
		">Activity ", "Reciprocity", "Owned Repositories", "Linked Accounts",
		"Security Credits", "OSSF Scorecard", "Package Publisher",
		"Stack Overflow", "Cross-Platform Identity",
	}
	for _, mustNotContain := range mustNotRender {
		if strings.Contains(body, mustNotContain) {
			t.Errorf("scorecard rendered %q despite nil Enrichment", mustNotContain)
		}
	}
}

// TestLandingHasNoPlanVocabulary renders the landing page and asserts the
// service is presented as free and best-effort.
//
// DevTrace has no plans, tiers, or pricing. This test guards against tier
// vocabulary reappearing in user-facing copy.
func TestLandingHasNoPlanVocabulary(t *testing.T) {
	data := pageData{
		Title:    "Home",
		Version:  "v1.0.0",
		Commit:   "abc1234",
		Date:     "2026-05-10",
		Plans:    plan.DisplayPlans(),
		Features: plan.DisplayFeatures(),
	}

	rec := httptest.NewRecorder()
	renderTemplate(rec, "landing.html", data)
	if rec.Code != 200 {
		t.Fatalf("render failed: status %d, body: %s", rec.Code, truncate(rec.Body.String(), 500))
	}
	body := rec.Body.String()

	mustContain := []string{
		"no plans, no pricing, and no subscriptions",
		"best-effort basis",
		"Apache License 2.0",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("rendered landing missing %q", want)
		}
	}

	// Tier vocabulary and pricing scaffolding must not reappear.
	mustNotContain := []string{
		"beta preview",
		"Starter",
		"Enterprise",
		"plan-highlight",
		"plans-table",
		`id="plan-free"`,
		`id="plan-pro"`,
	}
	for _, gone := range mustNotContain {
		if strings.Contains(body, gone) {
			t.Errorf("rendered landing still contains %q", gone)
		}
	}
}

func scorecardTestData(enrichment *model.Enrichment) map[string]any {
	return map[string]any{
		tmplTitle:        "testuser",
		"Username":       "testuser",
		"Profile":        &model.Profile{Name: "Test User"},
		"Grade":          "A",
		tmplValue:        0.9,
		tmplModelVersion: "v3",
		tmplVersion:      "v1.0.0",
		tmplCommit:       "abc1234",
		tmplDate:         "2026-05-02",
		"ScoringMode":    "global",
		"GradeClass":     "grade-a",
		"Categories": map[string]float64{
			"identity": 0.2, "engagement": 0.15, "community": 0.1, "behavioral": 0.45,
		},
		"Signals":     &model.Signals{AccountAgeDays: 365, Followers: 100},
		"RiskSummary": "Test summary",
		"AISensing":   nil,
		"Enrichment":  enrichment,
		"Upsell":      nil,
	}
}
