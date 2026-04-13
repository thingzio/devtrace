package plan

// Plan defines the capabilities and limits for a billing tier.
type Plan struct {
	Name             string
	MaxContributors  int // per billing period, 0 = unlimited
	RateLimitPerHour int
	DeepScoring      bool
	LicenseAnalysis  bool
	AISensing        bool
	BatchAPI         bool
	Webhooks         bool
	TrendMonths      int
	MaxAPIKeys       int
}

var plans = map[string]Plan{
	"free": {
		Name:             "free",
		MaxContributors:  50,
		RateLimitPerHour: 60,
		TrendMonths:      0,
		MaxAPIKeys:       1,
	},
	"starter": {
		Name:             "starter",
		MaxContributors:  200,
		RateLimitPerHour: 120,
		LicenseAnalysis:  true,
		AISensing:        true,
		TrendMonths:      3,
		MaxAPIKeys:       1,
	},
	"pro": {
		Name:             "pro",
		MaxContributors:  2000,
		RateLimitPerHour: 1000,
		DeepScoring:      true,
		LicenseAnalysis:  true,
		AISensing:        true,
		BatchAPI:         true,
		Webhooks:         true,
		TrendMonths:      12,
		MaxAPIKeys:       10,
	},
}

// Get returns the plan with the given name.
func Get(name string) (Plan, bool) {
	p, ok := plans[name]
	return p, ok
}

// Free returns the free tier plan.
func Free() Plan { return plans["free"] }
