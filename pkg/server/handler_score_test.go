package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
	"github.com/thingzio/devtrace/pkg/service"
	"github.com/thingzio/devtrace/pkg/tenant"
)

type mockGH struct {
	profile *ghclient.UserProfile
	signals *score.InputSignals
	err     error
}

func (m *mockGH) FetchUser(_ context.Context, _ string) (*ghclient.UserProfile, error) {
	return m.profile, m.err
}

func (m *mockGH) FetchSignals(_ context.Context, _, _ string, _ *ghclient.ArchiveHints) (*score.InputSignals, error) {
	return m.signals, m.err
}

func (m *mockGH) IsOrgMember(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (m *mockGH) ListUserRepos(_ context.Context, _ string, _ int) ([]ghclient.Repo, error) {
	return nil, nil
}

func (m *mockGH) FetchSecurityCredits(_ context.Context, _ string, _ int) ([]ghclient.SecurityAdvisoryCredit, error) {
	return nil, nil
}

func TestScoreHandler(t *testing.T) {
	mock := &mockGH{
		profile: &ghclient.UserProfile{
			Username:    "testuser",
			PublicRepos: 10,
			Followers:   50,
			Following:   20,
		},
		signals: &score.InputSignals{
			AgeDays:     365,
			PublicRepos: 10,
			Followers:   50,
			Following:   20,
			PRsMerged:   5,
			HasBio:      true,
			HasCompany:  true,
		},
	}

	svc := service.NewScoreService(mock, "v0.0.1-test")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/score/testuser", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var resp model.ScoreResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Username != "testuser" {
		t.Errorf("expected username testuser, got %q", resp.Username)
	}
	if resp.Score == nil {
		t.Fatal("expected score to be present")
	}
	if resp.Score.Grade == "" {
		t.Error("expected grade to be non-empty")
	}
	if resp.Score.Value < 0 || resp.Score.Value > 1 {
		t.Errorf("expected score in [0,1], got %f", resp.Score.Value)
	}
}

// stubBehaviorStore is a test-only BehaviorStore that returns canned values.
type stubBehaviorStore struct {
	behavior      *model.Behavior
	lifetime      *model.LifetimeActivity
	topRepos      []model.RepoContribution
	repoSummary   *model.OwnedRepos
	repoFetchedAt time.Time
}

func (s *stubBehaviorStore) GetBehavioralSignals(_ context.Context, _, _ string) (*model.Behavior, error) {
	return s.behavior, nil
}

func (s *stubBehaviorStore) GetLifetimeActivity(_ context.Context, _, _ string) (*model.LifetimeActivity, error) {
	return s.lifetime, nil
}

func (s *stubBehaviorStore) GetTopContributedRepos(_ context.Context, _, _ string, _ int) ([]model.RepoContribution, error) {
	return s.topRepos, nil
}

func (s *stubBehaviorStore) GetRepoSummary(_ context.Context, _, _ string) (*model.OwnedRepos, time.Time, error) {
	return s.repoSummary, s.repoFetchedAt, nil
}

func (s *stubBehaviorStore) SaveRepoSummary(_ context.Context, _, _ string, _ *model.OwnedRepos) error {
	return nil
}

func (s *stubBehaviorStore) GetSecurityCredits(_ context.Context, _, _ string) (*model.SecurityCredits, time.Time, error) {
	return nil, time.Time{}, nil
}

func (s *stubBehaviorStore) SaveSecurityCredits(_ context.Context, _, _ string, _ []model.SecurityCredit) error {
	return nil
}

func (s *stubBehaviorStore) GetOSSFScorecard(_ context.Context, _, _, _ string) (*model.OSSFScorecard, time.Time, error) {
	return nil, time.Time{}, nil
}

func (s *stubBehaviorStore) SaveOSSFScorecard(_ context.Context, _, _, _ string, _ *model.OSSFScorecard) error {
	return nil
}

func (s *stubBehaviorStore) GetPublisherProfile(_ context.Context, _, _, _ string) (*model.RegistryProfile, time.Time, error) {
	return nil, time.Time{}, nil
}

func (s *stubBehaviorStore) SavePublisherProfile(_ context.Context, _, _, _ string, _ *model.RegistryProfile) error {
	return nil
}

func (s *stubBehaviorStore) GetStackOverflowProfile(_ context.Context, _, _ string) (*model.StackOverflow, time.Time, error) {
	return nil, time.Time{}, nil
}

func (s *stubBehaviorStore) SaveStackOverflowProfile(_ context.Context, _, _ string, _ *model.StackOverflow) error {
	return nil
}

func TestScoreHandlerEnrichmentByPlan(t *testing.T) {
	mock := &mockGH{
		profile: &ghclient.UserProfile{Username: "testuser", PublicRepos: 5, Followers: 10},
		signals: &score.InputSignals{AgeDays: 200, PublicRepos: 5, Followers: 10, PRsMerged: 2},
	}
	svc := service.NewScoreService(mock, "v0.0.1-test")
	svc.SetBehaviorStore(&stubBehaviorStore{
		lifetime: &model.LifetimeActivity{PRsOpened: 12, ReviewsGiven: 7, ActiveDays: 4},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}", scoreHandler(nil, nil, svc))

	cases := []struct {
		name           string
		tenant         *tenant.Tenant
		wantEnrichment bool
	}{
		{"unauth strips enrichment", nil, false},
		{"free authed returns enrichment", &tenant.Tenant{ID: "t1", Plan: "free"}, true},
		{"starter authed returns enrichment", &tenant.Tenant{ID: "t2", Plan: "starter"}, true},
		{"pro authed returns enrichment", &tenant.Tenant{ID: "t3", Plan: "pro"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.tenant != nil {
				ctx = middleware.WithTenantContext(ctx, tc.tenant)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/score/testuser", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var resp model.ScoreResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			switch {
			case tc.wantEnrichment && resp.Enrichment == nil:
				t.Fatal("expected enrichment block for authed caller")
			case tc.wantEnrichment && resp.Enrichment.LifetimeActivity == nil:
				t.Error("expected lifetime activity in enrichment")
			case !tc.wantEnrichment && resp.Enrichment != nil:
				t.Errorf("unauth caller got enrichment: %+v", resp.Enrichment)
			}
		})
	}
}

func TestScoreHandlerSecurityHeaders(t *testing.T) {
	mock := &mockGH{
		profile: &ghclient.UserProfile{Username: "u"},
		signals: &score.InputSignals{AgeDays: 100},
	}

	svc := service.NewScoreService(mock, "v0.0.1-test")
	mux, cleanup := makeRouter(nil, svc, nil, nil, Options{}, nil)
	defer cleanup()
	handler := securityHeaders(mux)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/score/u", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	tests := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, want := range tests {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s: expected %q, got %q", header, want, got)
		}
	}
}
