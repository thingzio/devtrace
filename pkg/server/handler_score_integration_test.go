package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

// mockGHWithOrg extends mockGH with configurable org membership.
type mockGHWithOrg struct {
	mockGH
	memberOf string // IsOrgMember returns true for this org
}

func (m *mockGHWithOrg) IsOrgMember(_ context.Context, org, _ string) (bool, error) {
	return m.memberOf != "" && org == m.memberOf, nil
}

func newTestService(gh ghclient.Client) *service.ScoreService {
	return service.NewScoreService(gh, nil, "v1.2.3-test")
}

func defaultMock() *mockGH {
	return &mockGH{
		profile: &ghclient.UserProfile{
			Username:    "testuser",
			PublicRepos: 10,
			Followers:   50,
			Following:   20,
		},
		signals: &score.InputSignals{
			AgeDays:     500,
			PublicRepos: 10,
			Followers:   50,
			Following:   20,
			PRsMerged:   15,
			HasBio:      true,
			HasCompany:  true,
			HasLocation: true,
		},
	}
}

func scoreRequest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func scoreRequestWithTenant(t *testing.T, handler http.Handler, path string, tn *tenant.Tenant) *httptest.ResponseRecorder {
	t.Helper()
	ctx := middleware.WithTenantContext(context.Background(), tn)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeScore(t *testing.T, rec *httptest.ResponseRecorder) model.ScoreResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.ScoreResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestScoreHandlerVersionInResponse(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	resp := decodeScore(t, scoreRequest(t, mux, "/api/v1/score/testuser"))

	if resp.Version != "v1.2.3-test" {
		t.Errorf("version: got %q, want v1.2.3-test", resp.Version)
	}
}

func TestScoreHandlerBotUsername(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	bots := []string{"dependabot%5Bbot%5D", "renovate%5Bbot%5D"}
	for _, bot := range bots {
		resp := decodeScore(t, scoreRequest(t, mux, "/api/v1/score/"+bot))
		if resp.Score.Value != 0 {
			t.Errorf("%s: score should be 0, got %f", bot, resp.Score.Value)
		}
		if resp.Score.Grade != "F" {
			t.Errorf("%s: grade should be F, got %s", bot, resp.Score.Grade)
		}
		if resp.RiskSummary == "" {
			t.Errorf("%s: risk_summary should not be empty", bot)
		}
	}
}

func TestScoreHandlerRepoContext(t *testing.T) {
	mock := defaultMock()
	mock.signals.Commits = 50
	mock.signals.TotalCommits = 200
	mock.signals.TotalContributors = 5
	mock.signals.LastCommitDays = 3
	mock.signals.OrgMember = true
	mock.signals.AuthorAssociation = "MEMBER"

	svc := newTestService(mock)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tn := &tenant.Tenant{ID: "t", Plan: "free"}
	resp := decodeScore(t, scoreRequestWithTenant(t, mux, "/api/v1/score/testuser?repo=org/repo", tn))

	if resp.RepoContext == nil {
		t.Fatal("expected repo_context when ?repo= is provided")
	}
	if resp.RepoContext.Repo != "org/repo" {
		t.Errorf("repo: got %q, want org/repo", resp.RepoContext.Repo)
	}
	if !resp.RepoContext.OrgMember {
		t.Error("org_member should be true")
	}
	if resp.RepoContext.AuthorAssociation != "MEMBER" {
		t.Errorf("author_association: got %q, want MEMBER", resp.RepoContext.AuthorAssociation)
	}
}

func TestScoreHandlerNoRepoNoRepoContext(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	resp := decodeScore(t, scoreRequest(t, mux, "/api/v1/score/testuser"))

	if resp.RepoContext != nil {
		t.Error("repo_context should be nil when no ?repo= parameter")
	}
}

func TestScoreHandlerTrustedOrgs(t *testing.T) {
	mock := &mockGHWithOrg{
		mockGH:   *defaultMock(),
		memberOf: "trusted-corp",
	}
	mock.signals.Commits = 10
	mock.signals.TotalCommits = 100
	mock.signals.TotalContributors = 3

	svc := newTestService(mock)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tn := &tenant.Tenant{ID: "t", Plan: "free"}
	resp := decodeScore(t, scoreRequestWithTenant(t, mux,
		"/api/v1/score/testuser?repo=org/repo&trusted_orgs=trusted-corp", tn))

	if resp.RepoContext == nil {
		t.Fatal("expected repo_context")
	}
	if !resp.RepoContext.TrustedOrgMember {
		t.Error("trusted_org_member should be true")
	}
}

func TestScoreHandlerTrustedOrgsNoMatch(t *testing.T) {
	mock := &mockGHWithOrg{
		mockGH: *defaultMock(),
	}
	mock.signals.Commits = 10
	mock.signals.TotalCommits = 100
	mock.signals.TotalContributors = 3

	svc := newTestService(mock)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tn := &tenant.Tenant{ID: "t", Plan: "free"}
	resp := decodeScore(t, scoreRequestWithTenant(t, mux,
		"/api/v1/score/testuser?repo=org/repo&trusted_orgs=other-corp", tn))

	if resp.RepoContext == nil {
		t.Fatal("expected repo_context")
	}
	if resp.RepoContext.TrustedOrgMember {
		t.Error("trusted_org_member should be false when not a member")
	}
}

func TestScoreHandlerRiskSummaryContextAware(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tn := &tenant.Tenant{ID: "t", Plan: "free"}
	// Without repo — reputation language, no review language.
	resp := decodeScore(t, scoreRequestWithTenant(t, mux, "/api/v1/score/testuser", tn))
	if resp.RiskSummary == "" {
		t.Fatal("risk_summary should not be empty")
	}
	if containsStr(resp.RiskSummary, "review") {
		t.Errorf("without repo should not use review language: %q", resp.RiskSummary)
	}
}

func TestScoreHandlerUnauthMinimalResponse(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	// No tenant context → unauthenticated response.
	resp := decodeScore(t, scoreRequest(t, mux, "/api/v1/score/testuser"))

	if resp.Score == nil {
		t.Fatal("score should be present")
	}
	// Unauth should not have signals or categories.
	if resp.Signals != nil {
		t.Error("unauth should not have signals")
	}
	if resp.Score.Categories != nil {
		t.Error("unauth should not have categories")
	}
	if resp.Detail == "" {
		t.Error("unauth should have detail/signup message")
	}
}

func TestScoreHandlerAuthenticatedFullResponse(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tn := &tenant.Tenant{ID: "test-tenant", Plan: "free"}
	resp := decodeScore(t, scoreRequestWithTenant(t, mux, "/api/v1/score/testuser", tn))

	if resp.Signals == nil {
		t.Error("free plan should have signals")
	}
	if resp.Score.Categories == nil {
		t.Error("free plan should have categories")
	}
	if resp.RiskSummary == "" {
		t.Error("free plan should have risk_summary")
	}
}

func TestScoreHandlerGlobalSignalsNoNulls(t *testing.T) {
	svc := newTestService(defaultMock())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	tn := &tenant.Tenant{ID: "test-tenant", Plan: "free"}
	rec := scoreRequestWithTenant(t, mux, "/api/v1/score/testuser", tn)

	// Parse raw JSON to check for null fields in signals.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}

	var signals map[string]json.RawMessage
	if err := json.Unmarshal(raw["signals"], &signals); err != nil {
		t.Fatalf("decode signals: %v", err)
	}

	for key, val := range signals {
		if string(val) == "null" {
			t.Errorf("signal %q should not be null (global signals should always have values)", key)
		}
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
