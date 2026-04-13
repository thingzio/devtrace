package plan

import "testing"

func TestGetPlan(t *testing.T) {
	cases := []struct {
		name            string
		wantContrib     int
		wantRate        int
		wantDeepScoring bool
		wantKeys        int
	}{
		{"free", 50, 60, false, 1},
		{"starter", 200, 120, false, 1},
		{"pro", 2000, 1000, true, 10},
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
		})
	}
}

func TestGetPlanUnknown(t *testing.T) {
	_, ok := Get("enterprise")
	if ok {
		t.Error("Get(\"enterprise\") should return false")
	}
}

func TestFree(t *testing.T) {
	p := Free()
	if p.Name != "free" {
		t.Errorf("Free().Name = %q, want \"free\"", p.Name)
	}
}
