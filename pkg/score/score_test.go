package score

import "testing"

func TestComputeSuspended(t *testing.T) {
	s := InputSignals{
		Suspended: true,
		AgeDays:   1000,
		Commits:   500,
	}
	if got := Compute(s); got != 0 {
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
	got := Compute(s)
	if got < 0.5 || got > 1.0 {
		t.Errorf("established contributor: got %f, want [0.5, 1.0]", got)
	}
}

func TestComputeNewAccount(t *testing.T) {
	s := InputSignals{
		AgeDays: 1,
	}
	got := Compute(s)
	if got >= 0.3 {
		t.Errorf("new empty account: got %f, want < 0.3", got)
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
			got := Compute(tc.s)
			if got < 0 || got > 1 {
				t.Errorf("score out of bounds: got %f", got)
			}
		})
	}
}

func TestModelVersion(t *testing.T) {
	if ModelVersion != "3.2.0" {
		t.Errorf("unexpected model version: %s", ModelVersion)
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

func TestCategoriesKeys(t *testing.T) {
	cats := Categories(InputSignals{AgeDays: 365})
	expected := []string{"code_provenance", "identity", "engagement", "community", "behavioral"}
	for _, k := range expected {
		if _, ok := cats[k]; !ok {
			t.Errorf("missing category key: %s", k)
		}
	}
}

func TestCategoriesSuspended(t *testing.T) {
	cats := Categories(InputSignals{Suspended: true, AgeDays: 1000})
	for k, v := range cats {
		if v != 0 {
			t.Errorf("suspended category %s: got %f, want 0", k, v)
		}
	}
}
