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

package plan

import "fmt"

const (
	valDash = "\u2014"
	valSoon = "Coming soon"

	// Plan name canonical identifiers \u2014 use these instead of bare strings.
	PlanFree       = "free"
	PlanStarter    = "starter"
	PlanPro        = "pro"
	PlanEnterprise = "enterprise"
)

const (
	settingsURL                 = "/settings"
	descAIPowered               = "AI-powered"
	descWeeklyEmailAndDashboard = "Weekly email + dashboard"
)

// Plan defines the capabilities and limits for a billing tier.
type Plan struct {
	Name               string
	DisplayName        string
	PriceLabel         string // e.g. "$0/mo*", empty for free
	MaxContributors    int    // per billing period, 0 = unlimited
	RateLimitPerHour   int
	DeepScoring        bool
	AISensing          bool
	BatchAPI           bool
	Webhooks           bool
	HistoryDays        int // score history window in days
	MaxAPIKeys         int
	ComplianceReports  bool
	MaxWatchlists      int    // extra watchlists beyond implicit (0, 1, 3)
	DigestEmail        bool   // whether plan includes email digest
	WatchlistScope     string // "pr", "pr_review", "pr_review_issue"
	EventRetentionDays int    // notification event retention in days
	MaxEventsPerTenant int    // hard cap on stored events per tenant; oldest pruned first
	RiskSummary        string // display value for plans table
	AISensingLabel     string
	APIKeysLabel       string
	AlertsLabel        string
	ComplianceLabel    string
}

// Feature describes one row in the plans comparison table.
type Feature struct {
	ID     string   // HTML anchor id
	Label  string   // first column
	Desc   string   // plain-English tooltip shown on click
	Values []string // one per plan, in display order
	Span   bool     // if true, all values are the same — use colspan
}

var planOrder = []string{PlanFree, PlanStarter, PlanPro}

var plans = map[string]Plan{
	PlanFree: {
		Name:               PlanFree,
		DisplayName:        "Free",
		MaxContributors:    50,
		RateLimitPerHour:   60,
		HistoryDays:        30,
		MaxAPIKeys:         1,
		MaxWatchlists:      0,
		DigestEmail:        false,
		WatchlistScope:     "pr",
		EventRetentionDays: 7,
		MaxEventsPerTenant: 100,
		RiskSummary:        "Metrics-based",
		AISensingLabel:     "Metadata",
		APIKeysLabel:       "1",
		AlertsLabel:        "Dashboard only",
		ComplianceLabel:    valDash,
	},
	PlanStarter: {
		Name:               PlanStarter,
		DisplayName:        "Starter",
		PriceLabel:         "$0/mo*",
		MaxContributors:    200,
		RateLimitPerHour:   300,
		AISensing:          true,
		HistoryDays:        90,
		MaxAPIKeys:         1,
		MaxWatchlists:      1,
		DigestEmail:        true,
		WatchlistScope:     "pr_review",
		EventRetentionDays: 30,
		MaxEventsPerTenant: 1000,
		RiskSummary:        descAIPowered,
		AISensingLabel:     "Metadata + PR authenticity",
		APIKeysLabel:       "1",
		AlertsLabel:        descWeeklyEmailAndDashboard,
		ComplianceLabel:    valDash,
	},
	PlanPro: {
		Name:               PlanPro,
		DisplayName:        "Pro",
		PriceLabel:         "$0/mo*",
		MaxContributors:    2000,
		RateLimitPerHour:   1000,
		DeepScoring:        true,
		AISensing:          true,
		BatchAPI:           true,
		Webhooks:           true,
		ComplianceReports:  true,
		HistoryDays:        365,
		MaxAPIKeys:         10,
		MaxWatchlists:      3,
		DigestEmail:        true,
		WatchlistScope:     "pr_review_issue",
		EventRetentionDays: 90,
		MaxEventsPerTenant: 10000,
		RiskSummary:        descAIPowered,
		AISensingLabel:     "Full Context",
		APIKeysLabel:       "10",
		AlertsLabel:        descWeeklyEmailAndDashboard,
		ComplianceLabel:    "SSDF + EU CRA",
	},
}

// DisplayPlans returns the plans in display order.
func DisplayPlans() []Plan {
	out := make([]Plan, len(planOrder))
	for i, name := range planOrder {
		out[i] = plans[name]
	}
	return out
}

// DisplayFeatures returns the feature comparison rows for the plans table.
func DisplayFeatures() []Feature {
	dp := DisplayPlans()
	return []Feature{
		{
			ID: "feature-scoring", Label: "Contributor Scoring",
			Desc:   "Analyze any GitHub contributor across 22 signals in 5 categories. Every plan returns a numeric score, letter grade, and full signal breakdown.",
			Values: []string{"Score + Grade + Signals (available on all plans)"}, Span: true,
		},
		{
			ID: "feature-risk", Label: "Risk Summary",
			Desc:   "A short narrative explaining the contributor's reputation, highlighting strengths and areas of concern. Free plans use metrics-based summaries; paid plans use AI-powered analysis.",
			Values: pluck(dp, func(p Plan) string { return p.RiskSummary }),
		},
		{
			ID: "feature-ai-sensing", Label: "AI Sensing",
			Desc: "Detects AI-generated contributions by analyzing commit co-authorship, bot-associated PRs, and tool signatures. " +
				"Higher tiers add PR authenticity classification and behavioral heuristics.",
			Values: pluck(dp, func(p Plan) string { return p.AISensingLabel }),
		},
		{
			ID: "feature-history", Label: "Score History",
			Desc:   "Track how a contributor's score changes over time. The history window determines how far back trend data is retained for each scored contributor.",
			Values: pluck(dp, func(p Plan) string { return fmt.Sprintf("%d days", p.HistoryDays) }),
		},
		{
			ID: "feature-rate-limit", Label: "Rate Limit",
			Desc:   "Maximum number of API requests allowed per hour. Applies to both the scoring API and the score history endpoint. Exceeding the limit returns HTTP 429 with a Retry-After header.",
			Values: pluck(dp, func(p Plan) string { return fmt.Sprintf("%d req/hour", p.RateLimitPerHour) }),
		},
		{
			ID: "feature-api-keys", Label: "API Keys",
			Desc:   "Bearer tokens for programmatic API access. Create and revoke tokens in Settings. Each token counts against the same plan quota.",
			Values: pluck(dp, func(p Plan) string { return p.APIKeysLabel }),
		},
		{
			ID: "feature-batch", Label: "Batch API",
			Desc: "Score multiple contributors in a single API call. Useful for CI/CD pipelines and bulk audits of project contributors.",
			Values: pluck(dp, func(p Plan) string {
				if p.BatchAPI {
					return valSoon
				}
				return valDash
			}),
		},
		{
			ID: "feature-webhooks", Label: "Webhooks",
			Desc: "Receive real-time HTTP callbacks when a contributor's score changes significantly. Configure endpoints in Settings to integrate with your existing tooling.",
			Values: pluck(dp, func(p Plan) string {
				if p.Webhooks {
					return valSoon
				}
				return valDash
			}),
		},
		{
			ID: "feature-alerts", Label: "Risk Alerts",
			Desc:   "Get notified when a contributor's score drops below a threshold or when new risk flags appear. Alerts can be delivered via webhook or email.",
			Values: pluck(dp, func(p Plan) string { return p.AlertsLabel }),
		},
		{
			ID: "feature-compliance", Label: "Compliance Reports",
			Desc:   "Generate reports aligned with NIST SSDF (SP 800-218) and EU Cyber Resilience Act requirements. Documents contributor provenance and trust signals for audit and compliance workflows.",
			Values: pluck(dp, func(p Plan) string { return p.ComplianceLabel }),
		},
	}
}

func pluck(plans []Plan, fn func(Plan) string) []string {
	out := make([]string, len(plans))
	for i, p := range plans {
		out[i] = fn(p)
	}
	return out
}

// UpsellInfo describes a plan upgrade prompt shown on the scorecard.
type UpsellInfo struct {
	Message string // what the user gains by upgrading
	CTA     string // button label
	Link    string // button href
}

// Upsell returns upgrade messaging for the given plan tier.
// Returns nil when no upsell applies (pro or highest tier).
func Upsell(currentPlan string) *UpsellInfo {
	switch currentPlan {
	case "":
		return &UpsellInfo{
			Message: "Sign in to see full signal breakdown, category scores, and risk summaries.",
			CTA:     "Sign in with GitHub",
			Link:    "/auth/github",
		}
	case PlanFree:
		return &UpsellInfo{
			Message: "Upgrade to Starter for AI-powered risk summaries and PR authenticity analysis.",
			CTA:     "Upgrade to Starter",
			Link:    settingsURL,
		}
	case PlanStarter:
		return &UpsellInfo{
			Message: "Upgrade to Pro for behavioral heuristics, compliance reports, " +
				"and 365-day score history.",
			CTA:  "Upgrade to Pro",
			Link: settingsURL,
		}
	default:
		return nil
	}
}

// Get returns the plan with the given name.
func Get(name string) (Plan, bool) {
	p, ok := plans[name]
	return p, ok
}

// Free returns the free tier plan.
func Free() Plan { return plans[PlanFree] }
