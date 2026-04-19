package compliance

import (
	"testing"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestEvaluatePracticesNilInputs(t *testing.T) {
	results := EvaluatePractices(nil, nil, nil, nil, nil)
	if len(results) != 8 {
		t.Fatalf("expected 8 practices, got %d", len(results))
	}
	for _, r := range results {
		if r.ID == "" || r.Name == "" || r.Signal == "" {
			t.Errorf("practice %q has empty field: Name=%q Signal=%q", r.ID, r.Name, r.Signal)
		}
		if r.Status != StatusNoData && r.Status != StatusAbsent {
			t.Errorf("practice %s: expected no_data or absent with nil inputs, got %q", r.ID, r.Status)
		}
	}
}

func TestEvaluatePracticesSignalsOnly(t *testing.T) {
	signals := &model.Signals{
		AccountAgeDays: 2000,
		Followers:      100,
		PublicRepos:    10,
		ForkedRepos:    2,
		HasBio:         true,
		HasCompany:     true,
		HasLocation:    true,
		HasWebsite:     true,
		HasPublicEmail: true,
	}
	results := EvaluatePractices(signals, nil, nil, nil, nil)

	byID := make(map[string]PracticeResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}

	// PS.3 — strong profile should be present.
	if ps3 := byID["PS.3"]; ps3.Status != StatusPresent {
		t.Errorf("PS.3 status = %q, want %q", ps3.Status, StatusPresent)
	}
	// PW.4 — low fork ratio should be present.
	if pw4 := byID["PW.4"]; pw4.Status != StatusPresent {
		t.Errorf("PW.4 status = %q, want %q", pw4.Status, StatusPresent)
	}
	// PO.4 — mature account should be present.
	if po4 := byID["PO.4"]; po4.Status != StatusPresent {
		t.Errorf("PO.4 status = %q, want %q", po4.Status, StatusPresent)
	}
	// PS.1, PS.2 — no repo context = no_data.
	if ps1 := byID["PS.1"]; ps1.Status != StatusNoData {
		t.Errorf("PS.1 status = %q, want %q", ps1.Status, StatusNoData)
	}
	if ps2 := byID["PS.2"]; ps2.Status != StatusNoData {
		t.Errorf("PS.2 status = %q, want %q", ps2.Status, StatusNoData)
	}
}

func TestEvaluatePracticesWithRepoContext(t *testing.T) {
	signals := &model.Signals{AccountAgeDays: 500, Followers: 10, PublicRepos: 5}
	repo := &model.RepoContext{
		OrgMember:         true,
		CommitsVerified:   true,
		AuthorAssociation: "MEMBER",
	}
	results := EvaluatePractices(signals, repo, nil, nil, nil)

	byID := make(map[string]PracticeResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}

	if ps1 := byID["PS.1"]; ps1.Status != StatusPresent {
		t.Errorf("PS.1 status = %q, want %q", ps1.Status, StatusPresent)
	}
	if ps2 := byID["PS.2"]; ps2.Status != StatusPresent {
		t.Errorf("PS.2 status = %q, want %q", ps2.Status, StatusPresent)
	}
}

func TestEvaluatePracticesAbsentCases(t *testing.T) {
	signals := &model.Signals{
		AccountAgeDays: 10,
		Followers:      0,
		PublicRepos:    10,
		ForkedRepos:    9,
	}
	repo := &model.RepoContext{
		CommitsVerified:   false,
		AuthorAssociation: "NONE",
	}
	behavior := &model.Behavior{
		ReviewsGiven30d:  0,
		ConsistencyScore: 0.2,
	}
	aiSensing := &model.AISensing{
		Behavioral: &model.BehavioralHeuristics{
			SyntheticRiskFlags:   4,
			VelocityAnomalyRatio: 6.0,
		},
	}

	results := EvaluatePractices(signals, repo, nil, behavior, aiSensing)

	byID := make(map[string]PracticeResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}

	cases := []struct {
		id   string
		want string
	}{
		{"PS.1", StatusAbsent},
		{"PS.2", StatusAbsent},
		{"PS.3", StatusAbsent},
		{"PW.4", StatusAbsent},
		{"PW.6", StatusAbsent},
		{"PW.7", StatusAbsent},
		{"RV.1", StatusAbsent},
		{"PO.4", StatusAbsent},
	}
	for _, tc := range cases {
		if got := byID[tc.id].Status; got != tc.want {
			t.Errorf("%s status = %q, want %q (signal: %q)", tc.id, got, tc.want, byID[tc.id].Signal)
		}
	}
}

func TestEvaluatePracticesFullPresent(t *testing.T) {
	signals := &model.Signals{
		AccountAgeDays: 2000,
		Followers:      200,
		PublicRepos:    20,
		ForkedRepos:    2,
		HasBio:         true,
		HasCompany:     true,
		HasLocation:    true,
		HasWebsite:     true,
		HasPublicEmail: true,
	}
	repo := &model.RepoContext{
		OrgMember:         true,
		CommitsVerified:   true,
		AuthorAssociation: "MEMBER",
	}
	categories := map[string]float64{"community": 0.5}
	behavior := &model.Behavior{
		ReviewsGiven30d:  15,
		ConsistencyScore: 0.8,
	}
	aiSensing := &model.AISensing{
		Behavioral: &model.BehavioralHeuristics{
			SyntheticRiskFlags:   0,
			VelocityAnomalyRatio: 1.0,
		},
	}

	results := EvaluatePractices(signals, repo, categories, behavior, aiSensing)

	for _, r := range results {
		if r.Status != StatusPresent {
			t.Errorf("%s status = %q, want %q (signal: %q)", r.ID, r.Status, StatusPresent, r.Signal)
		}
	}
}

func TestPracticeIDs(t *testing.T) {
	results := EvaluatePractices(nil, nil, nil, nil, nil)
	wantIDs := []string{"PS.1", "PS.2", "PS.3", "PW.4", "PW.6", "PW.7", "RV.1", "PO.4"}
	if len(results) != len(wantIDs) {
		t.Fatalf("got %d results, want %d", len(results), len(wantIDs))
	}
	for i, want := range wantIDs {
		if results[i].ID != want {
			t.Errorf("results[%d].ID = %q, want %q", i, results[i].ID, want)
		}
	}
}
