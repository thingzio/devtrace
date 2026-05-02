package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/ossf"
	"github.com/thingzio/devtrace/pkg/score"
)

// mockBehaviorStore implements service.BehaviorStore for testing the
// enrichment pipeline without a real Postgres dependency.
type mockBehaviorStore struct {
	behavior         *model.Behavior
	lifetime         *model.LifetimeActivity
	topRepos         []model.RepoContribution
	repoSummary      *model.OwnedRepos
	repoFetchedAt    time.Time
	saveSummaryErr   error
	savedSummary     *model.OwnedRepos
	saveCallCount    int
	credits          *model.SecurityCredits
	creditsFetchedAt time.Time
	savedCredits     []model.SecurityCredit
	ossfCard         *model.OSSFScorecard
	ossfFetchedAt    time.Time
	savedOSSFCard    *model.OSSFScorecard
}

func (m *mockBehaviorStore) GetBehavioralSignals(_ context.Context, _, _ string) (*model.Behavior, error) {
	return m.behavior, nil
}

func (m *mockBehaviorStore) GetLifetimeActivity(_ context.Context, _, _ string) (*model.LifetimeActivity, error) {
	return m.lifetime, nil
}

func (m *mockBehaviorStore) GetTopContributedRepos(_ context.Context, _, _ string, _ int) ([]model.RepoContribution, error) {
	return m.topRepos, nil
}

func (m *mockBehaviorStore) GetRepoSummary(_ context.Context, _, _ string) (*model.OwnedRepos, time.Time, error) {
	return m.repoSummary, m.repoFetchedAt, nil
}

func (m *mockBehaviorStore) SaveRepoSummary(_ context.Context, _, _ string, summary *model.OwnedRepos) error {
	m.savedSummary = summary
	m.saveCallCount++
	return m.saveSummaryErr
}

func (m *mockBehaviorStore) GetSecurityCredits(_ context.Context, _, _ string) (*model.SecurityCredits, time.Time, error) {
	return m.credits, m.creditsFetchedAt, nil
}

func (m *mockBehaviorStore) SaveSecurityCredits(_ context.Context, _, _ string, credits []model.SecurityCredit) error {
	m.savedCredits = credits
	return nil
}

func (m *mockBehaviorStore) GetOSSFScorecard(_ context.Context, _, _, _ string) (*model.OSSFScorecard, time.Time, error) {
	return m.ossfCard, m.ossfFetchedAt, nil
}

func (m *mockBehaviorStore) SaveOSSFScorecard(_ context.Context, _, _, _ string, card *model.OSSFScorecard) error {
	m.savedOSSFCard = card
	return nil
}

// mockClient implements ghclient.Client for testing.
type mockClient struct {
	signals    *score.InputSignals
	profile    *ghclient.UserProfile
	repos      []ghclient.Repo
	credits    []ghclient.SecurityAdvisoryCredit
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

func (m *mockClient) ListUserRepos(_ context.Context, _ string, _ int) ([]ghclient.Repo, error) {
	return m.repos, nil
}

func (m *mockClient) FetchSecurityCredits(_ context.Context, _ string, _ int) ([]ghclient.SecurityAdvisoryCredit, error) {
	return m.credits, nil
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

func TestScoreEnrichmentLifetimeActivity(t *testing.T) {
	first := time.Now().UTC().Add(-400 * 24 * time.Hour)
	last := time.Now().UTC().Add(-1 * 24 * time.Hour)
	store := &mockBehaviorStore{
		lifetime: &model.LifetimeActivity{
			PRsOpened:    14,
			PRsMerged:    8,
			ReviewsGiven: 9,
			IssuesOpened: 5,
			ActiveDays:   3,
			FirstActive:  &first,
			LastActive:   &last,
		},
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Enrichment == nil {
		t.Fatal("expected enrichment block")
	}
	if resp.Enrichment.LifetimeActivity == nil {
		t.Fatal("expected lifetime activity in enrichment")
	}
	got := resp.Enrichment.LifetimeActivity
	if got.PRsOpened != 14 {
		t.Errorf("PRsOpened: got %d, want 14", got.PRsOpened)
	}
	if got.ReviewsGiven != 9 {
		t.Errorf("ReviewsGiven: got %d, want 9", got.ReviewsGiven)
	}
	if got.FirstActive == nil || got.LastActive == nil {
		t.Error("expected populated FirstActive and LastActive")
	}
	// Reciprocity travels with LifetimeActivity automatically.
	if resp.Enrichment.Reciprocity == nil {
		t.Fatal("expected reciprocity to be populated when lifetime activity present")
	}
	if resp.Enrichment.Reciprocity.ReviewsPerPR == 0 {
		t.Errorf("expected non-zero ReviewsPerPR (9 reviews / 14 PRs)")
	}
}

func TestScoreEnrichmentReciprocityAbsentWithoutPRs(t *testing.T) {
	store := &mockBehaviorStore{
		lifetime: &model.LifetimeActivity{
			PRsOpened: 0, ReviewsGiven: 5, IssueComments: 3,
		},
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.LifetimeActivity == nil {
		t.Fatal("expected enrichment with lifetime")
	}
	if resp.Enrichment.Reciprocity != nil {
		t.Errorf("expected nil reciprocity when PRsOpened==0, got %+v", resp.Enrichment.Reciprocity)
	}
}

func TestScoreEnrichmentTopContributedRepos(t *testing.T) {
	store := &mockBehaviorStore{
		topRepos: []model.RepoContribution{
			{Repo: "o/r1", Activities: 10, LastContribution: time.Now().UTC()},
			{Repo: "o/r2", Activities: 4, LastContribution: time.Now().UTC()},
		},
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Enrichment == nil {
		t.Fatal("expected enrichment block")
	}
	if len(resp.Enrichment.TopContributedRepos) != 2 {
		t.Fatalf("expected 2 top repos, got %d", len(resp.Enrichment.TopContributedRepos))
	}
	if resp.Enrichment.TopContributedRepos[0].Repo != "o/r1" {
		t.Errorf("top repo: got %s, want o/r1", resp.Enrichment.TopContributedRepos[0].Repo)
	}
}

func TestScoreEnrichmentLinkedAccountsAndEmails(t *testing.T) {
	mock := &mockClient{
		signals: establishedSignals(),
		profile: &ghclient.UserProfile{
			Username: "testuser",
			Bio:      "Find me on https://x.com/testuser or email me at test@example.com",
			Website:  "https://example.dev",
		},
	}
	store := &mockBehaviorStore{}

	tests := []struct {
		plan       string
		wantLinks  bool
		wantEmails bool
	}{
		{plan: "free", wantLinks: true, wantEmails: false},
		{plan: "starter", wantLinks: true, wantEmails: true},
		{plan: "pro", wantLinks: true, wantEmails: true},
	}
	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			svc := NewScoreService(mock, "v0.0.1-test")
			svc.SetBehaviorStore(store)
			resp, err := svc.Score(context.Background(), "testuser", "", tc.plan, nil)
			if err != nil {
				t.Fatalf("score: %v", err)
			}
			if resp.Enrichment == nil {
				t.Fatal("expected enrichment block")
			}
			if tc.wantLinks && len(resp.Enrichment.LinkedAccounts) == 0 {
				t.Errorf("plan=%s: expected linked accounts, got 0", tc.plan)
			}
			if tc.wantEmails && len(resp.Enrichment.Emails) == 0 {
				t.Errorf("plan=%s: expected emails, got 0", tc.plan)
			}
			if !tc.wantEmails && len(resp.Enrichment.Emails) > 0 {
				t.Errorf("plan=%s: emails leaked: %v", tc.plan, resp.Enrichment.Emails)
			}
		})
	}
}

func TestScoreEnrichmentOwnedReposCacheHit(t *testing.T) {
	cached := &model.OwnedRepos{
		TotalStars: 500, TotalRepos: 4,
		Top: []model.OwnedRepo{{Name: "u/a", Stars: 300, Language: "Go"}},
	}
	store := &mockBehaviorStore{
		repoSummary:   cached,
		repoFetchedAt: time.Now().Add(-1 * time.Hour), // fresh
	}
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
		// repos populated to detect erroneous fetch
		repos: []ghclient.Repo{{FullName: "u/wrong", Stars: 9999}},
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.OwnedRepos == nil {
		t.Fatal("expected owned repos from cache")
	}
	if resp.Enrichment.OwnedRepos.TotalStars != 500 {
		t.Errorf("got %d stars, want 500 (cache should win)", resp.Enrichment.OwnedRepos.TotalStars)
	}
	if store.savedSummary != nil {
		t.Errorf("fresh cache should not trigger save, got %+v", store.savedSummary)
	}
}

func TestScoreEnrichmentOwnedReposCacheStaleRefreshes(t *testing.T) {
	store := &mockBehaviorStore{
		repoSummary:   &model.OwnedRepos{TotalStars: 1, TotalRepos: 1},
		repoFetchedAt: time.Now().Add(-48 * time.Hour), // stale
	}
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
		repos: []ghclient.Repo{
			{FullName: "u/r1", Stars: 1000, Language: "Go"},
			{FullName: "u/r2", Stars: 500, Language: "Go"},
		},
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.OwnedRepos == nil {
		t.Fatal("expected owned repos after refresh")
	}
	if resp.Enrichment.OwnedRepos.TotalStars != 1500 {
		t.Errorf("got %d stars, want 1500 (refreshed from gh client)", resp.Enrichment.OwnedRepos.TotalStars)
	}
	if store.savedSummary == nil {
		t.Fatal("stale refresh should call SaveRepoSummary")
	}
}

func TestScoreEnrichmentOwnedReposFetchErrorFallsBackToCache(t *testing.T) {
	stale := &model.OwnedRepos{TotalStars: 99, TotalRepos: 2}
	store := &mockBehaviorStore{
		repoSummary:   stale,
		repoFetchedAt: time.Now().Add(-48 * time.Hour),
	}
	// errClient returns an error from ListUserRepos.
	ec := &errorRepoClient{base: &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}}
	svc := NewScoreService(ec, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.OwnedRepos == nil {
		t.Fatal("expected fallback to stale cache on fetch error")
	}
	if resp.Enrichment.OwnedRepos.TotalStars != 99 {
		t.Errorf("got %d, want 99 (stale cache served on fetch failure)", resp.Enrichment.OwnedRepos.TotalStars)
	}
}

// errorRepoClient wraps mockClient but returns an error from ListUserRepos.
type errorRepoClient struct {
	base *mockClient
}

func (c *errorRepoClient) FetchSignals(ctx context.Context, u, r string, h *ghclient.ArchiveHints) (*score.InputSignals, error) {
	return c.base.FetchSignals(ctx, u, r, h)
}

func (c *errorRepoClient) FetchUser(ctx context.Context, u string) (*ghclient.UserProfile, error) {
	return c.base.FetchUser(ctx, u)
}

func (c *errorRepoClient) IsOrgMember(ctx context.Context, o, u string) (bool, error) {
	return c.base.IsOrgMember(ctx, o, u)
}

func (c *errorRepoClient) ListUserRepos(_ context.Context, _ string, _ int) ([]ghclient.Repo, error) {
	return nil, errors.New("upstream unavailable")
}

func (c *errorRepoClient) FetchSecurityCredits(_ context.Context, _ string, _ int) ([]ghclient.SecurityAdvisoryCredit, error) {
	return c.base.FetchSecurityCredits(context.Background(), "", 0)
}

// TestSecurityCreditsCacheHit returns the cached aggregate without
// hitting GitHub when the cached row is fresh.
func TestSecurityCreditsCacheHit(t *testing.T) {
	cached := &model.SecurityCredits{
		ReporterCount: 3, FixerCount: 1,
		BySeverity: map[string]int{"high": 4},
	}
	store := &mockBehaviorStore{
		credits:          cached,
		creditsFetchedAt: time.Now().Add(-1 * time.Hour),
	}
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
		// If we erroneously fetch, this canned response would surface.
		credits: []ghclient.SecurityAdvisoryCredit{
			{AdvisoryID: "GHSA-fresh-fetch", CreditType: "fixer", Severity: "low"},
		},
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.SecurityCredits == nil {
		t.Fatal("expected security credits in enrichment")
	}
	if resp.Enrichment.SecurityCredits.ReporterCount != 3 {
		t.Errorf("got reporter=%d, want 3 (cache hit, not fresh fetch)",
			resp.Enrichment.SecurityCredits.ReporterCount)
	}
	if store.savedCredits != nil {
		t.Errorf("fresh cache should not save: got %d new credits", len(store.savedCredits))
	}
}

// TestSecurityCreditsStaleRefresh re-fetches from GitHub when the
// cached row is older than config.SecurityCreditTTL.
//
// Live fetching is gated behind DEVTRACE_SECURITY_CREDITS_ENABLED — see
// SecurityCreditsEnabled in pkg/config — so this test enables the flag
// to exercise the refresh path. Production runs with the flag off until
// a viable user→credits API exists.
func TestSecurityCreditsStaleRefresh(t *testing.T) {
	t.Setenv("DEVTRACE_SECURITY_CREDITS_ENABLED", "true")
	store := &mockBehaviorStore{
		credits:          &model.SecurityCredits{ReporterCount: 1},
		creditsFetchedAt: time.Now().Add(-30 * 24 * time.Hour), // way past TTL
	}
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
		credits: []ghclient.SecurityAdvisoryCredit{
			{
				AdvisoryID: "GHSA-fresh-1234", CreditType: "reporter",
				Severity: "high", CVEID: "CVE-2024-100",
				PublishedAt: time.Now(),
			},
		},
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	_, err := svc.Score(context.Background(), "testuser", "", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if len(store.savedCredits) != 1 {
		t.Fatalf("expected 1 saved credit after refresh, got %d", len(store.savedCredits))
	}
	if store.savedCredits[0].AdvisoryID != "GHSA-fresh-1234" {
		t.Errorf("saved advisory: got %q, want GHSA-fresh-1234", store.savedCredits[0].AdvisoryID)
	}
}

// TestSecurityCreditsDisabledSuppressesFetch ensures the gate flag
// short-circuits the refresh path when the cache is stale, returning
// the (stale-or-nil) cached value without calling GitHub. This is the
// production default — see SecurityCreditsEnabled in pkg/config — set
// after v0.21 shipped a query against a non-existent GraphQL field.
func TestSecurityCreditsDisabledSuppressesFetch(t *testing.T) {
	// Flag is off by default; just confirm refresh is suppressed.
	store := &mockBehaviorStore{
		credits:          nil,
		creditsFetchedAt: time.Time{}, // no cache → would normally fetch
	}
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
		credits: []ghclient.SecurityAdvisoryCredit{
			{AdvisoryID: "GHSA-should-not-fetch", CreditType: "reporter", Severity: "high"},
		},
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment != nil && resp.Enrichment.SecurityCredits != nil {
		t.Errorf("expected nil SecurityCredits with flag off, got %+v", resp.Enrichment.SecurityCredits)
	}
	if store.savedCredits != nil {
		t.Errorf("expected no save with flag off, got %d", len(store.savedCredits))
	}
}

// TestSecurityCreditsStrippedForFree confirms Free-tier callers don't
// see the SecurityCredits block — it's a Starter+ upgrade hook.
func TestSecurityCreditsStrippedForFree(t *testing.T) {
	store := &mockBehaviorStore{
		credits: &model.SecurityCredits{
			ReporterCount: 5, FixerCount: 2,
		},
		creditsFetchedAt: time.Now().Add(-1 * time.Hour),
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	tests := []struct {
		plan    string
		wantSet bool
	}{
		{"free", false},
		{"starter", true},
		{"pro", true},
	}
	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			resp, err := svc.Score(context.Background(), "testuser", "", tc.plan, nil)
			if err != nil {
				t.Fatalf("score: %v", err)
			}
			if resp.Enrichment == nil {
				t.Fatal("expected enrichment block")
			}
			has := resp.Enrichment.SecurityCredits != nil
			if has != tc.wantSet {
				t.Errorf("plan=%s SecurityCredits present=%v, want %v", tc.plan, has, tc.wantSet)
			}
		})
	}
}

// mockOSSF implements service.OSSFFetcher for testing the scorecard
// enrichment path without an httptest server.
type mockOSSF struct {
	card  *ossf.Scorecard
	err   error
	calls int
}

func (m *mockOSSF) Fetch(_ context.Context, _, _ string) (*ossf.Scorecard, error) {
	m.calls++
	return m.card, m.err
}

// TestOSSFEnrichmentCacheHit: fresh cached row → no upstream call,
// returned card matches cached.
func TestOSSFEnrichmentCacheHit(t *testing.T) {
	cached := &model.OSSFScorecard{
		Score: 7.5,
		Date:  time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Checks: []model.OSSFCheck{
			{Name: "Code-Review", Score: 10},
		},
	}
	store := &mockBehaviorStore{
		ossfCard:      cached,
		ossfFetchedAt: time.Now().Add(-1 * time.Hour),
	}
	mc := &mockOSSF{
		card: &ossf.Scorecard{Score: 9.9}, // different — would surface if fetched
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)
	svc.SetOSSFFetcher(mc)

	resp, err := svc.Score(context.Background(), "testuser", "owner/repo", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.OSSFScorecard == nil {
		t.Fatal("expected OSSF scorecard in enrichment")
	}
	if resp.Enrichment.OSSFScorecard.Score != 7.5 {
		t.Errorf("score: got %v, want 7.5 (cache hit)", resp.Enrichment.OSSFScorecard.Score)
	}
	if mc.calls != 0 {
		t.Errorf("expected 0 upstream fetches on cache hit, got %d", mc.calls)
	}
}

// TestOSSFEnrichmentStaleRefresh: stale row → upstream fetch + save.
func TestOSSFEnrichmentStaleRefresh(t *testing.T) {
	store := &mockBehaviorStore{
		ossfCard: &model.OSSFScorecard{
			Score:  3.0,
			Checks: []model.OSSFCheck{{Name: "License", Score: 0}},
		},
		ossfFetchedAt: time.Now().Add(-30 * 24 * time.Hour), // way past TTL
	}
	mc := &mockOSSF{
		card: &ossf.Scorecard{
			Score:        9.0,
			Date:         time.Now(),
			Commit:       "deadbeef",
			ScorecardVer: "v5.0.0",
			Checks: []ossf.Check{
				{Name: "License", Score: 10},
				{Name: "Maintained", Score: 8},
			},
		},
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)
	svc.SetOSSFFetcher(mc)

	resp, err := svc.Score(context.Background(), "testuser", "owner/repo", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment == nil || resp.Enrichment.OSSFScorecard == nil {
		t.Fatal("expected refreshed OSSF scorecard")
	}
	if resp.Enrichment.OSSFScorecard.Score != 9.0 {
		t.Errorf("score after refresh: got %v, want 9.0", resp.Enrichment.OSSFScorecard.Score)
	}
	if mc.calls != 1 {
		t.Errorf("expected 1 upstream fetch on stale refresh, got %d", mc.calls)
	}
	if store.savedOSSFCard == nil || len(store.savedOSSFCard.Checks) != 2 {
		t.Errorf("expected save with 2 checks, got %+v", store.savedOSSFCard)
	}
}

// TestOSSFEnrichmentNotFoundSavesSentinel: 404 from upstream writes a
// zero-score sentinel so subsequent score requests for the same repo
// don't re-hit the OSSF API for the full TTL window.
func TestOSSFEnrichmentNotFoundSavesSentinel(t *testing.T) {
	store := &mockBehaviorStore{}
	mc := &mockOSSF{err: ossf.ErrNotFound}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)
	svc.SetOSSFFetcher(mc)

	resp, err := svc.Score(context.Background(), "testuser", "owner/missing", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment != nil && resp.Enrichment.OSSFScorecard != nil {
		t.Errorf("expected nil OSSF on not-found, got %+v", resp.Enrichment.OSSFScorecard)
	}
	if store.savedOSSFCard == nil {
		t.Fatal("expected sentinel save on not-found")
	}
	if store.savedOSSFCard.Score != 0 || len(store.savedOSSFCard.Checks) != 0 {
		t.Errorf("sentinel shape: score=%v checks=%d", store.savedOSSFCard.Score, len(store.savedOSSFCard.Checks))
	}
}

// TestOSSFEnrichmentSentinelSuppressesFetch: a previously-saved
// sentinel (zero score, no checks) within the TTL must NOT trigger a
// re-fetch and must surface as nil to the caller.
func TestOSSFEnrichmentSentinelSuppressesFetch(t *testing.T) {
	store := &mockBehaviorStore{
		ossfCard:      &model.OSSFScorecard{}, // sentinel
		ossfFetchedAt: time.Now().Add(-1 * time.Hour),
	}
	mc := &mockOSSF{
		card: &ossf.Scorecard{Score: 9.0, Checks: []ossf.Check{{Name: "X", Score: 9}}},
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)
	svc.SetOSSFFetcher(mc)

	resp, err := svc.Score(context.Background(), "testuser", "owner/repo", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment != nil && resp.Enrichment.OSSFScorecard != nil {
		t.Error("sentinel should surface as nil to caller")
	}
	if mc.calls != 0 {
		t.Errorf("sentinel must not trigger refetch, got %d calls", mc.calls)
	}
}

// TestOSSFEnrichmentSkipsWithoutRepo: no repo argument → no upstream
// call and no enrichment block populated, even when fetcher would
// return data.
func TestOSSFEnrichmentSkipsWithoutRepo(t *testing.T) {
	store := &mockBehaviorStore{}
	mc := &mockOSSF{card: &ossf.Scorecard{Score: 9.0, Checks: []ossf.Check{{Name: "X"}}}}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)
	svc.SetOSSFFetcher(mc)

	resp, err := svc.Score(context.Background(), "testuser", "", "starter", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if resp.Enrichment != nil && resp.Enrichment.OSSFScorecard != nil {
		t.Error("OSSF should be nil without repo")
	}
	if mc.calls != 0 {
		t.Errorf("expected 0 fetches without repo, got %d", mc.calls)
	}
}

// TestOSSFStrippedForFree: Free-tier callers don't see the OSSF block.
func TestOSSFStrippedForFree(t *testing.T) {
	store := &mockBehaviorStore{
		ossfCard: &model.OSSFScorecard{
			Score:  9.0,
			Checks: []model.OSSFCheck{{Name: "Code-Review", Score: 10}},
		},
		ossfFetchedAt: time.Now().Add(-1 * time.Hour),
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)
	svc.SetOSSFFetcher(&mockOSSF{})

	tests := []struct {
		plan    string
		wantSet bool
	}{
		{"free", false},
		{"starter", true},
		{"pro", true},
	}
	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			resp, err := svc.Score(context.Background(), "testuser", "owner/repo", tc.plan, nil)
			if err != nil {
				t.Fatalf("score: %v", err)
			}
			if resp.Enrichment == nil {
				t.Fatal("expected enrichment block")
			}
			has := resp.Enrichment.OSSFScorecard != nil
			if has != tc.wantSet {
				t.Errorf("plan=%s OSSFScorecard present=%v, want %v", tc.plan, has, tc.wantSet)
			}
		})
	}
}

// TestBuildScopeInfoMarksRepoFields ensures the response's Scope
// block lists repo_context and enrichment.ossf_scorecard as repo-scoped
// when a repo is provided, and as empty when not. Always-global fields
// stay in the Global list either way. This is the API-side contract
// for "which fields are repo vs profile" that consumers parse.
func TestBuildScopeInfoMarksRepoFields(t *testing.T) {
	withRepo := buildScopeInfo(true)
	if withRepo == nil {
		t.Fatal("buildScopeInfo(true) returned nil")
	}
	if !sliceContains(withRepo.RepoScoped, "repo_context") {
		t.Errorf("repo_context missing from RepoScoped: %v", withRepo.RepoScoped)
	}
	if !sliceContains(withRepo.RepoScoped, "enrichment.ossf_scorecard") {
		t.Errorf("enrichment.ossf_scorecard missing from RepoScoped: %v", withRepo.RepoScoped)
	}
	if !sliceContains(withRepo.Global, "enrichment.lifetime_activity") {
		t.Errorf("enrichment.lifetime_activity missing from Global: %v", withRepo.Global)
	}
	// Repo-scoped paths must NOT appear in Global, and vice versa.
	for _, p := range withRepo.RepoScoped {
		if sliceContains(withRepo.Global, p) {
			t.Errorf("path %q appears in both lists", p)
		}
	}

	noRepo := buildScopeInfo(false)
	if len(noRepo.RepoScoped) != 0 {
		t.Errorf("RepoScoped should be empty without repo, got %v", noRepo.RepoScoped)
	}
	if !sliceContains(noRepo.Global, "enrichment.lifetime_activity") {
		t.Errorf("Global list should not depend on hasRepo: %v", noRepo.Global)
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// TestSplitOwnerRepo covers the parser used by the OSSF helper to
// reject malformed repo arguments before making any upstream call.
func TestSplitOwnerRepo(t *testing.T) {
	tests := []struct {
		in        string
		wantOwner string
		wantRepo  string
		wantOK    bool
	}{
		{"owner/repo", "owner", "repo", true},
		{"  owner/repo  ", "owner", "repo", true},
		{"owner/", "", "", false},
		{"/repo", "", "", false},
		{"owner", "", "", false},
		{"owner/sub/repo", "", "", false},
		{"", "", "", false},
		{" / ", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			o, r, ok := splitOwnerRepo(tc.in)
			if ok != tc.wantOK || o != tc.wantOwner || r != tc.wantRepo {
				t.Errorf("got %q/%q/%v, want %q/%q/%v",
					o, r, ok, tc.wantOwner, tc.wantRepo, tc.wantOK)
			}
		})
	}
}

// TestOwnedReposCacheTTLBoundary pins the exact-at-TTL behavior: a
// summary fetched 1ns before the TTL is fresh; one fetched at exactly
// the TTL is stale (forces a refresh). Catches off-by-one regressions
// in the freshness comparison.
func TestOwnedReposCacheTTLBoundary(t *testing.T) {
	tests := []struct {
		name          string
		fetchedAtAge  time.Duration
		wantRefresh   bool
		cachedStars   int64
		newFetchStars int64
	}{
		{
			name:          "just inside TTL — uses cache, no refresh",
			fetchedAtAge:  config.RepoSummaryTTL() - time.Second,
			wantRefresh:   false,
			cachedStars:   100,
			newFetchStars: 999, // wouldn't be used
		},
		{
			name:          "exactly at TTL — refresh",
			fetchedAtAge:  config.RepoSummaryTTL(),
			wantRefresh:   true,
			cachedStars:   100,
			newFetchStars: 200,
		},
		{
			name:          "just past TTL — refresh",
			fetchedAtAge:  config.RepoSummaryTTL() + time.Second,
			wantRefresh:   true,
			cachedStars:   100,
			newFetchStars: 200,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockBehaviorStore{
				repoSummary: &model.OwnedRepos{
					TotalStars: tc.cachedStars, TotalRepos: 1,
				},
				repoFetchedAt: time.Now().Add(-tc.fetchedAtAge),
			}
			mc := &mockClient{
				signals: establishedSignals(),
				profile: establishedProfile(),
				repos: []ghclient.Repo{
					{FullName: "u/r", Stars: int(tc.newFetchStars), Language: "Go"},
				},
			}
			svc := NewScoreService(mc, "v0.0.1-test")
			svc.SetBehaviorStore(store)

			resp, err := svc.Score(context.Background(), "boundary-user", "", "free", nil)
			if err != nil {
				t.Fatalf("score: %v", err)
			}
			if resp.Enrichment == nil || resp.Enrichment.OwnedRepos == nil {
				t.Fatal("expected owned repos")
			}
			gotStars := resp.Enrichment.OwnedRepos.TotalStars

			refreshed := store.savedSummary != nil
			if refreshed != tc.wantRefresh {
				t.Errorf("refresh: got %v, want %v (saved=%+v)",
					refreshed, tc.wantRefresh, store.savedSummary)
			}
			if tc.wantRefresh {
				if gotStars != tc.newFetchStars {
					t.Errorf("expected refreshed stars=%d, got %d", tc.newFetchStars, gotStars)
				}
			} else {
				if gotStars != tc.cachedStars {
					t.Errorf("expected cached stars=%d, got %d", tc.cachedStars, gotStars)
				}
			}
		})
	}
}

// TestOwnedReposCacheStillSavesEmptyResult ensures that contributors
// with zero owned non-fork repos get a row written so the refresh
// worker doesn't repeatedly re-fetch them on every score request.
// Bug-class: the AggregateRepos function returns nil for empty/all-
// fork inputs; if we naively skip the save when summary is nil, the
// next request goes through the same fetch loop indefinitely.
func TestOwnedReposCacheStillSavesEmptyResult(t *testing.T) {
	store := &mockBehaviorStore{} // no cached summary, no fetched_at
	mc := &mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
		repos: []ghclient.Repo{
			{FullName: "u/fork", Fork: true, Stars: 100},
		},
	}
	svc := NewScoreService(mc, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	_, err := svc.Score(context.Background(), "fork-only-user", "", "free", nil)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	// AggregateRepos returns nil for fork-only input; SaveRepoSummary
	// should still be called (with nil) so the empty-record sentinel
	// suppresses repeated fetches.
	saveCalls := store.saveCallCount
	if saveCalls != 1 {
		t.Errorf("expected 1 SaveRepoSummary call to record empty result, got %d", saveCalls)
	}
}

func TestScoreEnrichmentBlocksIndependent(t *testing.T) {
	// Lifetime present, top repos absent — Enrichment populated with only lifetime.
	store := &mockBehaviorStore{
		lifetime: &model.LifetimeActivity{PRsOpened: 1},
		topRepos: nil,
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Enrichment == nil {
		t.Fatal("expected enrichment block when at least one sub-block has data")
	}
	if resp.Enrichment.LifetimeActivity == nil {
		t.Error("expected LifetimeActivity")
	}
	if len(resp.Enrichment.TopContributedRepos) != 0 {
		t.Errorf("expected empty TopContributedRepos, got %d", len(resp.Enrichment.TopContributedRepos))
	}
}

func TestScoreEnrichmentNilOnNoData(t *testing.T) {
	store := &mockBehaviorStore{lifetime: nil}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	resp, err := svc.Score(context.Background(), "testuser", "", "free", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Enrichment != nil {
		t.Errorf("expected nil enrichment when store returns nil, got %+v", resp.Enrichment)
	}
}

func TestScoreEnrichmentStrippedForUnauthenticated(t *testing.T) {
	store := &mockBehaviorStore{
		lifetime: &model.LifetimeActivity{PRsOpened: 1},
	}
	svc := NewScoreService(&mockClient{
		signals: establishedSignals(),
		profile: establishedProfile(),
	}, "v0.0.1-test")
	svc.SetBehaviorStore(store)

	// Empty plan = unauthenticated; enrichment should be stripped per enrichForPlan.
	resp, err := svc.Score(context.Background(), "testuser", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Enrichment != nil {
		t.Errorf("unauthenticated callers should not see enrichment, got %+v", resp.Enrichment)
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
