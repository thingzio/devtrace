package plan

import "fmt"

const (
	valDash = "\u2014"
	valSoon = "Coming soon"
)

// Plan defines the capabilities and limits for a billing tier.
type Plan struct {
	Name              string
	DisplayName       string
	PriceLabel        string // e.g. "$0/mo*", empty for free
	MaxContributors   int    // per billing period, 0 = unlimited
	RateLimitPerHour  int
	DeepScoring       bool
	LicenseAnalysis   bool
	AISensing         bool
	BatchAPI          bool
	Webhooks          bool
	HistoryDays       int // score history window in days
	MaxAPIKeys        int
	ComplianceReports bool
	RiskSummary       string // display value for plans table
	AISensingLabel    string
	LicenseLabel      string
	APIKeysLabel      string
	AlertsLabel       string
	ComplianceLabel   string
}

// Feature describes one row in the plans comparison table.
type Feature struct {
	ID     string   // HTML anchor id
	Label  string   // first column
	Values []string // one per plan, in display order
	Span   bool     // if true, all values are the same — use colspan
}

var planOrder = []string{"free", "starter", "pro"}

var plans = map[string]Plan{
	"free": {
		Name:             "free",
		DisplayName:      "Free",
		MaxContributors:  50,
		RateLimitPerHour: 60,
		HistoryDays:      30,
		MaxAPIKeys:       1,
		RiskSummary:      "Metrics-based",
		AISensingLabel:   "Metadata",
		LicenseLabel:     valDash,
		APIKeysLabel:     "1",
		AlertsLabel:      valDash,
		ComplianceLabel:  valDash,
	},
	"starter": {
		Name:             "starter",
		DisplayName:      "Starter",
		PriceLabel:       "$0/mo*",
		MaxContributors:  200,
		RateLimitPerHour: 300,
		AISensing:        true,
		HistoryDays:      90,
		MaxAPIKeys:       1,
		RiskSummary:      "AI-powered",
		AISensingLabel:   "Metadata + PR authenticity",
		LicenseLabel:     valDash,
		APIKeysLabel:     "1",
		AlertsLabel:      valSoon,
		ComplianceLabel:  valDash,
	},
	"pro": {
		Name:              "pro",
		DisplayName:       "Pro",
		PriceLabel:        "$0/mo*",
		MaxContributors:   2000,
		RateLimitPerHour:  1000,
		DeepScoring:       true,
		LicenseAnalysis:   true,
		AISensing:         true,
		BatchAPI:          true,
		Webhooks:          true,
		ComplianceReports: true,
		HistoryDays:       365,
		MaxAPIKeys:        10,
		RiskSummary:       "AI-powered",
		AISensingLabel:    "Full Context",
		LicenseLabel:      "Pro only",
		APIKeysLabel:      "10",
		AlertsLabel:       valSoon,
		ComplianceLabel:   "SSDF + EU CRA",
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
		{ID: "feature-scoring", Label: "Contributor Scoring", Values: []string{"Score + Grade + Signals (available on all plans)"}, Span: true},
		{ID: "feature-risk", Label: "Risk Summary", Values: pluck(dp, func(p Plan) string { return p.RiskSummary })},
		{ID: "feature-ai-sensing", Label: "AI Sensing", Values: pluck(dp, func(p Plan) string { return p.AISensingLabel })},
		{ID: "feature-license", Label: "License Analysis", Values: pluck(dp, func(p Plan) string { return p.LicenseLabel })},
		{ID: "feature-history", Label: "Score History", Values: pluck(dp, func(p Plan) string {
			return fmt.Sprintf("%d days", p.HistoryDays)
		})},
		{ID: "feature-rate-limit", Label: "Rate Limit", Values: pluck(dp, func(p Plan) string {
			return fmt.Sprintf("%d req/hour", p.RateLimitPerHour)
		})},
		{ID: "feature-api-keys", Label: "API Keys", Values: pluck(dp, func(p Plan) string { return p.APIKeysLabel })},
		{ID: "feature-batch", Label: "Batch API", Values: pluck(dp, func(p Plan) string {
			if p.BatchAPI {
				return valSoon
			}
			return valDash
		})},
		{ID: "feature-webhooks", Label: "Webhooks", Values: pluck(dp, func(p Plan) string {
			if p.Webhooks {
				return valSoon
			}
			return valDash
		})},
		{ID: "feature-alerts", Label: "Risk Alerts", Values: pluck(dp, func(p Plan) string { return p.AlertsLabel })},
		{ID: "feature-compliance", Label: "Compliance Reports", Values: pluck(dp, func(p Plan) string { return p.ComplianceLabel })},
	}
}

func pluck(plans []Plan, fn func(Plan) string) []string {
	out := make([]string, len(plans))
	for i, p := range plans {
		out[i] = fn(p)
	}
	return out
}

// Get returns the plan with the given name.
func Get(name string) (Plan, bool) {
	p, ok := plans[name]
	return p, ok
}

// Free returns the free tier plan.
func Free() Plan { return plans["free"] }
