package service

import (
	"context"
	"strings"
	"testing"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/score"
)

// mockClient implements ghclient.Client for testing.
type mockClient struct {
	signals    *score.InputSignals
	profile    *ghclient.UserProfile
	trustedOrg string // IsOrgMember returns true for this org
}

func (m *mockClient) FetchSignals(_ context.Context, _, _ string, _ *ghclient.ArchiveHints) (*score.InputSignals, error) {
	return m.signals, nil
}

func (m *mockClient) IsOrgMember(_ context.Context, org, _ string) (bool, error) {
	return m.trustedOrg != "" && org == m.trustedOrg, nil
}

func (m *mockClient) FetchUser(_ context.Context, _ string) (*ghclient.UserProfile, error) {
	return m.profile, nil
}

func establishedSignals() *score.InputSignals {
	return &score.InputSignals{
		AgeDays:           800,
		AuthorAssociation: "CONTRIBUTOR",
		HasBio:            true,
		HasCompany:        true,
		HasLocation:       true,
		HasWebsite:        true,
		Commits:           120,
		TotalCommits:      500,
		TotalContributors: 10,
		LastCommitDays:    5,
		PRsMerged:         30,
		PRsClosed:         5,
		Followers:         50,
		Following:         20,
		PublicRepos:       15,
		RecentPRRepoCount: 3,
		ForkedRepos:       2,
		UnverifiedCommits: 0,
	}
}

func establishedProfile() *ghclient.UserProfile {
	return &ghclient.UserProfile{
		Username:    "testuser",
		Followers:   50,
		Following:   20,
		PublicRepos: 15,
	}
}

func TestScoreContributor(t *testing.T) {
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Username != "testuser" {
		t.Errorf("username = %q, want %q", resp.Username, "testuser")
	}
	if resp.Provider != "github" {
		t.Errorf("provider = %q, want %q", resp.Provider, "github")
	}
	if resp.Score == nil {
		t.Fatal("score is nil")
	}
	if resp.Score.Value < 0 || resp.Score.Value > 1 {
		t.Errorf("score value %f out of [0,1]", resp.Score.Value)
	}
	if resp.Score.Grade == "" {
		t.Error("grade is empty")
	}
	if resp.Version == "" {
		t.Error("version is empty")
	}
}

func TestScoreContributorPlanAware(t *testing.T) {
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	ctx := context.Background()

	t.Run("unauth", func(t *testing.T) {
		resp, err := svc.Score(ctx, "testuser", "", "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Signals != nil {
			t.Error("unauth should not have signals")
		}
		if resp.Detail == "" {
			t.Error("unauth should have detail message")
		}
		if resp.Score.Categories != nil {
			t.Error("unauth should not have categories")
		}
	})

	t.Run("free", func(t *testing.T) {
		resp, err := svc.Score(ctx, "testuser", "", "free", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Signals == nil {
			t.Error("free plan should have signals")
		}
		if resp.Score.Categories == nil {
			t.Error("free plan should have categories")
		}
		if resp.License != nil {
			t.Error("free plan should not have license")
		}
		if resp.AISensing == nil {
			t.Error("free plan should have ai_sensing (Tier 1)")
		}
	})

	t.Run("starter", func(t *testing.T) {
		resp, err := svc.Score(ctx, "testuser", "", "starter", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Signals == nil {
			t.Error("starter plan should have signals")
		}
		// License and AISensing are nil placeholders for now; just verify no panic.
	})
}

func TestScoreContributorWithRepo(t *testing.T) {
	sig := establishedSignals()
	sig.Commits = 50
	sig.TotalCommits = 200
	sig.TotalContributors = 5
	sig.LastCommitDays = 3
	sig.AuthorAssociation = "MEMBER"
	sig.OrgMember = true

	svc := NewScoreService(&mockClient{
		signals: sig,
		profile: establishedProfile(),
	}, "v0.0.1-test")

	resp, err := svc.Score(context.Background(), "testuser", "org/repo", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.RepoContext == nil {
		t.Fatal("repo_context should be populated when repo is provided")
	}
	if resp.RepoContext.Repo != "org/repo" {
		t.Errorf("repo = %q, want %q", resp.RepoContext.Repo, "org/repo")
	}
	if resp.RepoContext.Commits != 50 {
		t.Errorf("commits = %d, want 50", resp.RepoContext.Commits)
	}
	if !resp.RepoContext.OrgMember {
		t.Error("org_member should be true")
	}
}

func TestRiskSummarySuspendedWithRepo(t *testing.T) {
	sig := &score.InputSignals{Suspended: true}
	summary := generateRiskSummary(sig, 0, true)
	if !strings.Contains(summary, "suspended") {
		t.Errorf("suspended summary missing keyword: %q", summary)
	}
	if !strings.Contains(summary, "Do not merge") {
		t.Errorf("suspended summary with repo missing warning: %q", summary)
	}
}

func TestRiskSummarySuspendedWithoutRepo(t *testing.T) {
	sig := &score.InputSignals{Suspended: true}
	summary := generateRiskSummary(sig, 0, false)
	if !strings.Contains(summary, "suspended") {
		t.Errorf("suspended summary missing keyword: %q", summary)
	}
	if strings.Contains(summary, "Do not merge") {
		t.Errorf("suspended summary without repo should not have review language: %q", summary)
	}
}

func TestRiskSummaryWithRepoReviewLanguage(t *testing.T) {
	sig := &score.InputSignals{AgeDays: 500, PRsMerged: 5}
	summary := generateRiskSummary(sig, 0.5, true)
	if !strings.Contains(summary, "review") {
		t.Errorf("with-repo summary should contain review language: %q", summary)
	}
}

func TestRiskSummaryWithoutRepoNoReviewLanguage(t *testing.T) {
	sig := &score.InputSignals{AgeDays: 500, PRsMerged: 5}
	summary := generateRiskSummary(sig, 0.5, false)
	if strings.Contains(summary, "review") {
		t.Errorf("without-repo summary should not contain review language: %q", summary)
	}
}

func TestScoreBotReturnsZero(t *testing.T) {
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")

	bots := []string{"dependabot[bot]", "renovate[bot]", "copilot", "github-copilot", "custom-app[bot]"}
	for _, botName := range bots {
		resp, err := svc.Score(context.Background(), botName, "", "free", nil)
		if err != nil {
			t.Fatalf("Score(%q): unexpected error: %v", botName, err)
		}
		if resp.Score.Value != 0 {
			t.Errorf("Score(%q).Value = %f, want 0", botName, resp.Score.Value)
		}
		if resp.Score.Grade != "F" {
			t.Errorf("Score(%q).Grade = %q, want F", botName, resp.Score.Grade)
		}
		if resp.RiskSummary == "" {
			t.Errorf("Score(%q).RiskSummary should not be empty", botName)
		}
		if resp.Signals != nil {
			t.Errorf("Score(%q) should not have signals", botName)
		}
	}
}

func TestScoreNonBotNotFiltered(t *testing.T) {
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Score.Value == 0 {
		t.Error("non-bot should have non-zero score with established signals")
	}
}

func TestScoreTrustedOrgsMatch(t *testing.T) {
	svc := NewScoreService(&mockClient{
		signals:    establishedSignals(),
		profile:    establishedProfile(),
		trustedOrg: "trusted-org",
	}, "v0.0.1-test")

	resp, err := svc.Score(context.Background(), "testuser", "org/repo", "free", []string{"trusted-org"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.RepoContext == nil {
		t.Fatal("expected repo_context")
	}
	if !resp.RepoContext.TrustedOrgMember {
		t.Error("trusted_org_member should be true when user is member of trusted org")
	}
}

func TestScoreTrustedOrgsNoMatch(t *testing.T) {
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")

	resp, err := svc.Score(context.Background(), "testuser", "org/repo", "free", []string{"other-org"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.RepoContext == nil {
		t.Fatal("expected repo_context")
	}
	if resp.RepoContext.TrustedOrgMember {
		t.Error("trusted_org_member should be false when user is not a member")
	}
}

func TestScoreTrustedOrgsNilNoOp(t *testing.T) {
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")

	// nil trusted orgs — should work fine (no org checks).
	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Score.Value == 0 {
		t.Error("should have non-zero score")
	}
}

func TestRiskSummaryNewAccount(t *testing.T) {
	sig := &score.InputSignals{
		AgeDays:   30,
		PRsMerged: 1,
	}
	summary := generateRiskSummary(sig, 0.2, false)
	if !strings.Contains(summary, "recently") {
		t.Errorf("new account summary should mention 'recently': %q", summary)
	}
}
