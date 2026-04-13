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
	signals *score.InputSignals
	profile *ghclient.UserProfile
}

func (m *mockClient) FetchSignals(_ context.Context, _, _ string) (*score.InputSignals, error) {
	return m.signals, nil
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
	}, nil)

	resp, err := svc.Score(context.Background(), "testuser", "", "free")
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
	if resp.Score.ModelVersion == "" {
		t.Error("model version is empty")
	}
}

func TestScoreContributorPlanAware(t *testing.T) {
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}
	svc := NewScoreService(mc, nil)
	ctx := context.Background()

	t.Run("unauth", func(t *testing.T) {
		resp, err := svc.Score(ctx, "testuser", "", "")
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
		resp, err := svc.Score(ctx, "testuser", "", "free")
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
		if resp.AISensing != nil {
			t.Error("free plan should not have ai_sensing")
		}
	})

	t.Run("starter", func(t *testing.T) {
		resp, err := svc.Score(ctx, "testuser", "", "starter")
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
	}, nil)

	resp, err := svc.Score(context.Background(), "testuser", "org/repo", "free")
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

func TestRiskSummarySuspended(t *testing.T) {
	sig := &score.InputSignals{Suspended: true}
	summary := generateRiskSummary(sig, 0)
	if !strings.Contains(summary, "suspended") {
		t.Errorf("suspended summary missing keyword: %q", summary)
	}
	if !strings.Contains(summary, "Do not merge") {
		t.Errorf("suspended summary missing warning: %q", summary)
	}
}

func TestRiskSummaryNewAccount(t *testing.T) {
	sig := &score.InputSignals{
		AgeDays:   30,
		PRsMerged: 1,
	}
	summary := generateRiskSummary(sig, 0.2)
	if !strings.Contains(summary, "recently") {
		t.Errorf("new account summary should mention 'recently': %q", summary)
	}
}
