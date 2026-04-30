package plan

import "testing"

func TestGetPlan(t *testing.T) {
	cases := []struct {
		name            string
		wantContrib     int
		wantRate        int
		wantDeepScoring bool
		wantKeys        int
		wantHistory     int
		wantCompliance  bool
		wantWatchlists  int
		wantDigest      bool
		wantScope       string
	}{
		{"free", 50, 60, false, 1, 30, false, 0, false, "pr"},
		{"starter", 200, 300, false, 1, 90, false, 1, true, "pr_review"},
		{"pro", 2000, 1000, true, 10, 365, true, 3, true, "pr_review_issue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := Get(tc.name)
			if !ok {
				t.Fatalf("Get(%q) returned false", tc.name)
			}
			if p.MaxContributors != tc.wantContrib {
				t.Errorf("MaxContributors = %d, want %d", p.MaxContributors, tc.wantContrib)
			}
			if p.RateLimitPerHour != tc.wantRate {
				t.Errorf("RateLimitPerHour = %d, want %d", p.RateLimitPerHour, tc.wantRate)
			}
			if p.DeepScoring != tc.wantDeepScoring {
				t.Errorf("DeepScoring = %v, want %v", p.DeepScoring, tc.wantDeepScoring)
			}
			if p.MaxAPIKeys != tc.wantKeys {
				t.Errorf("MaxAPIKeys = %d, want %d", p.MaxAPIKeys, tc.wantKeys)
			}
			if p.HistoryDays != tc.wantHistory {
				t.Errorf("HistoryDays = %d, want %d", p.HistoryDays, tc.wantHistory)
			}
			if p.ComplianceReports != tc.wantCompliance {
				t.Errorf("ComplianceReports = %v, want %v", p.ComplianceReports, tc.wantCompliance)
			}
			if p.MaxWatchlists != tc.wantWatchlists {
				t.Errorf("MaxWatchlists = %d, want %d", p.MaxWatchlists, tc.wantWatchlists)
			}
			if p.DigestEmail != tc.wantDigest {
				t.Errorf("DigestEmail = %v, want %v", p.DigestEmail, tc.wantDigest)
			}
			if p.WatchlistScope != tc.wantScope {
				t.Errorf("WatchlistScope = %q, want %q", p.WatchlistScope, tc.wantScope)
			}
		})
	}
}

func TestPlanWatchlistRetention(t *testing.T) {
	cases := []struct {
		plan          string
		retentionDays int
		maxEvents     int
	}{
		{"free", 7, 100},
		{"starter", 30, 1000},
		{"pro", 90, 10000},
	}
	for _, tc := range cases {
		t.Run(tc.plan, func(t *testing.T) {
			p, ok := Get(tc.plan)
			if !ok {
				t.Fatalf("plan %q not found", tc.plan)
			}
			if p.EventRetentionDays != tc.retentionDays {
				t.Errorf("EventRetentionDays = %d, want %d", p.EventRetentionDays, tc.retentionDays)
			}
			if p.MaxEventsPerTenant != tc.maxEvents {
				t.Errorf("MaxEventsPerTenant = %d, want %d", p.MaxEventsPerTenant, tc.maxEvents)
			}
		})
	}
}

func TestGetPlanUnknown(t *testing.T) {
	_, ok := Get("nonexistent")
	if ok {
		t.Error("Get(\"nonexistent\") should return false")
	}
}

func TestFree(t *testing.T) {
	p := Free()
	if p.Name != "free" {
		t.Errorf("Free().Name = %q, want \"free\"", p.Name)
	}
}

func TestDisplayPlansOrder(t *testing.T) {
	dp := DisplayPlans()
	want := []string{"free", "starter", "pro"}
	if len(dp) != len(want) {
		t.Fatalf("DisplayPlans() returned %d plans, want %d", len(dp), len(want))
	}
	for i, name := range want {
		if dp[i].Name != name {
			t.Errorf("DisplayPlans()[%d].Name = %q, want %q", i, dp[i].Name, name)
		}
	}
}

func TestUpsell(t *testing.T) {
	cases := []struct {
		plan    string
		wantNil bool
		wantCTA string
		wantURL string
	}{
		{"", false, "Sign in with GitHub", "/auth/github"},
		{"free", false, "Upgrade to Starter", "/settings"},
		{"starter", false, "Upgrade to Pro", "/settings"},
		{"pro", true, "", ""},
		{"unknown", true, "", ""},
	}
	for _, tc := range cases {
		name := tc.plan
		if name == "" {
			name = "unauthenticated"
		}
		t.Run(name, func(t *testing.T) {
			u := Upsell(tc.plan)
			if tc.wantNil {
				if u != nil {
					t.Fatalf("Upsell(%q) = %+v, want nil", tc.plan, u)
				}
				return
			}
			if u == nil {
				t.Fatalf("Upsell(%q) = nil, want non-nil", tc.plan)
			}
			if u.CTA != tc.wantCTA {
				t.Errorf("CTA = %q, want %q", u.CTA, tc.wantCTA)
			}
			if u.Link != tc.wantURL {
				t.Errorf("Link = %q, want %q", u.Link, tc.wantURL)
			}
			if u.Message == "" {
				t.Error("Message is empty")
			}
		})
	}
}

func TestDisplayFeatures(t *testing.T) {
	features := DisplayFeatures()
	dash := "\u2014"
	soon := "Coming soon"

	checks := []struct {
		id     string
		label  string
		values []string
		span   bool
	}{
		{"feature-scoring", "Contributor Scoring", []string{"Score + Grade + Signals (available on all plans)"}, true},
		{"feature-risk", "Risk Summary", []string{"Metrics-based", "AI-powered", "AI-powered"}, false},
		{"feature-history", "Score History", []string{"30 days", "90 days", "365 days"}, false},
		{"feature-rate-limit", "Rate Limit", []string{"60 req/hour", "300 req/hour", "1000 req/hour"}, false},
		{"feature-api-keys", "API Keys", []string{"1", "1", "10"}, false},
		{"feature-batch", "Batch API", []string{dash, dash, soon}, false},
		{"feature-webhooks", "Webhooks", []string{dash, dash, soon}, false},
		{"feature-alerts", "Risk Alerts", []string{"Dashboard only", "Weekly email + dashboard", "Weekly email + dashboard"}, false},
		{"feature-compliance", "Compliance Reports", []string{dash, dash, "SSDF + EU CRA"}, false},
	}

	byID := make(map[string]Feature, len(features))
	for _, f := range features {
		byID[f.ID] = f
		if f.Desc == "" {
			t.Errorf("feature %q has empty Desc", f.ID)
		}
	}

	for _, tc := range checks {
		t.Run(tc.id, func(t *testing.T) {
			f, ok := byID[tc.id]
			if !ok {
				t.Fatalf("feature %q not found", tc.id)
			}
			if f.Label != tc.label {
				t.Errorf("Label = %q, want %q", f.Label, tc.label)
			}
			if f.Span != tc.span {
				t.Errorf("Span = %v, want %v", f.Span, tc.span)
			}
			if len(f.Values) != len(tc.values) {
				t.Fatalf("Values len = %d, want %d", len(f.Values), len(tc.values))
			}
			for i, want := range tc.values {
				if f.Values[i] != want {
					t.Errorf("Values[%d] = %q, want %q", i, f.Values[i], want)
				}
			}
		})
	}
}
