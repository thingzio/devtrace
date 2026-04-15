package score

import (
	"testing"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestComputeSuspended(t *testing.T) {
	s := InputSignals{
		Suspended: true,
		AgeDays:   1000,
		Commits:   500,
	}
	if got := Compute(s, false, nil); got != 0 {
		t.Errorf("suspended account: got %f, want 0", got)
	}
}

func TestComputeEstablished(t *testing.T) {
	s := InputSignals{
		AgeDays:           1200,
		AuthorAssociation: "MEMBER",
		HasBio:            true,
		HasCompany:        true,
		HasLocation:       true,
		HasWebsite:        true,
		Commits:           200,
		TotalCommits:      1000,
		TotalContributors: 20,
		LastCommitDays:    5,
		PRsMerged:         50,
		PRsClosed:         5,
		Followers:         100,
		Following:         20,
		PublicRepos:       15,
		RecentPRRepoCount: 3,
		ForkedRepos:       2,
		UnverifiedCommits: 10,
		OrgMember:         true,
	}
	got := Compute(s, true, nil) // established has repo context
	if got < 0.5 || got > 1.0 {
		t.Errorf("established contributor: got %f, want [0.5, 1.0]", got)
	}
}

func TestComputeNewAccount(t *testing.T) {
	s := InputSignals{
		AgeDays: 1,
	}
	got := Compute(s, false, nil)
	if got >= 0.5 {
		t.Errorf("new empty account: got %f, want < 0.5", got)
	}
}

func TestComputeBounds(t *testing.T) {
	cases := []struct {
		name string
		s    InputSignals
	}{
		{"zero", InputSignals{}},
		{"suspended", InputSignals{Suspended: true}},
		{"max_values", InputSignals{
			AgeDays:           10000,
			Commits:           10000,
			TotalCommits:      100000,
			TotalContributors: 500,
			Followers:         50000,
			Following:         100,
			PublicRepos:       200,
			PRsMerged:         5000,
			PRsClosed:         100,
			HasBio:            true,
			HasCompany:        true,
			HasLocation:       true,
			HasWebsite:        true,
			OrgMember:         true,
			AuthorAssociation: "OWNER",
			RecentPRRepoCount: 1,
			ForkedRepos:       0,
			UnverifiedCommits: 0,
		}},
		{"negative_days", InputSignals{AgeDays: -1, LastCommitDays: -1}},
		{"only_forks", InputSignals{
			AgeDays:     365,
			PublicRepos: 10,
			ForkedRepos: 10,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.s, true, nil) // bounds test with full context
			if got < 0 || got > 1 {
				t.Errorf("score out of bounds: got %f", got)
			}
		})
	}
}

func TestCategoryWeightsSum(t *testing.T) {
	sum := CategoryProvenanceWeight + CategoryIdentityWeight +
		CategoryEngagementWeight + CategoryCommunityWeight +
		CategoryBehavioralWeight
	if diff := sum - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("category weights sum to %f, want 1.0", sum)
	}
}

func TestCategoriesKeysWithRepo(t *testing.T) {
	cats := Categories(InputSignals{AgeDays: 365}, true, nil)
	expected := []string{"code_provenance", "identity", "engagement", "community", "behavioral"}
	for _, k := range expected {
		if _, ok := cats[k]; !ok {
			t.Errorf("missing category key: %s", k)
		}
	}
}

func TestCategoriesKeysWithoutRepo(t *testing.T) {
	cats := Categories(InputSignals{AgeDays: 365}, false, nil)
	expected := []string{"identity", "engagement", "community", "behavioral"}
	for _, k := range expected {
		if _, ok := cats[k]; !ok {
			t.Errorf("missing category key: %s", k)
		}
	}
	if _, ok := cats["code_provenance"]; ok {
		t.Error("code_provenance should be omitted without repo context")
	}
}

func TestCategoriesSuspended(t *testing.T) {
	cats := Categories(InputSignals{Suspended: true, AgeDays: 1000}, false, nil)
	for k, v := range cats {
		if v != 0 {
			t.Errorf("suspended category %s: got %f, want 0", k, v)
		}
	}
}

func TestBehavioralWithBehavior(t *testing.T) {
	s := InputSignals{
		AgeDays:           365,
		PublicRepos:       10,
		ForkedRepos:       2,
		RecentPRRepoCount: 3,
	}
	b := &model.Behavior{
		ConsistencyScore: 0.8,
		ReviewsGiven30d:  5,
		DistinctRepos90d: 4,
	}

	withBeh := Compute(s, false, b)
	withoutBeh := Compute(s, false, nil)

	if withBeh <= withoutBeh {
		t.Errorf("behavioral signals should improve score: with=%f, without=%f", withBeh, withoutBeh)
	}
}

func TestBehavioralNilBehaviorFallback(t *testing.T) {
	s := InputSignals{
		AgeDays:           365,
		PublicRepos:       10,
		ForkedRepos:       2,
		RecentPRRepoCount: 3,
	}

	got := Compute(s, false, nil)
	if got < 0 || got > 1 {
		t.Errorf("nil behavior score out of bounds: %f", got)
	}
}
