package plan

import "testing"

func TestGetPlan(t *testing.T) {
	cases := []struct {
		name            string
		wantContrib     int
		wantRate        int
		wantDeepScoring bool
		wantKeys        int
		wantCompliance  bool
	}{
		{"free", 50, 60, false, 1, false},
		{"starter", 200, 300, false, 1, false},
		{"pro", 2000, 1000, true, 10, true},
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
			if p.ComplianceReports != tc.wantCompliance {
				t.Errorf("ComplianceReports = %v, want %v", p.ComplianceReports, tc.wantCompliance)
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

func TestDisplayFeaturesComplianceRow(t *testing.T) {
	features := DisplayFeatures()
	for _, f := range features {
		if f.ID != "feature-compliance" {
			continue
		}
		if f.Label != "Compliance Reports" {
			t.Errorf("Label = %q, want %q", f.Label, "Compliance Reports")
		}
		// Values order: free, starter, pro
		if len(f.Values) != 3 {
			t.Fatalf("expected 3 values, got %d", len(f.Values))
		}
		dash := "\u2014"
		wantVals := []string{dash, dash, "SSDF + EU CRA"}
		for i, want := range wantVals {
			if f.Values[i] != want {
				t.Errorf("Values[%d] = %q, want %q", i, f.Values[i], want)
			}
		}
		return
	}
	t.Fatal("feature-compliance row not found in DisplayFeatures()")
}
