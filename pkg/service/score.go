package service

import (
	"context"
	"fmt"
	"time"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

// ScoreStore is the subset of the data store needed for scoring.
type ScoreStore interface {
	GetCachedScore(ctx context.Context, username, provider string) (*model.ScoreResponse, error)
	SaveScore(ctx context.Context, resp *model.ScoreResponse) error
}

// ScoreService orchestrates signal fetching, scoring, and response enrichment.
type ScoreService struct {
	gh    ghclient.Client
	store ScoreStore // nil-safe for unit tests without DB
}

// NewScoreService returns a ScoreService wired to the given GitHub client and optional store.
func NewScoreService(gh ghclient.Client, store ScoreStore) *ScoreService {
	return &ScoreService{gh: gh, store: store}
}

// Score fetches signals, computes a reputation score, and builds a plan-aware response.
func (s *ScoreService) Score(ctx context.Context, username, repo, plan string) (*model.ScoreResponse, error) {
	signals, err := s.gh.FetchSignals(ctx, username, repo)
	if err != nil {
		return nil, fmt.Errorf("fetch signals: %w", err)
	}

	profile, err := s.gh.FetchUser(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("fetch user: %w", err)
	}

	value := score.Compute(*signals)
	grade := score.Grade(value)

	resp := &model.ScoreResponse{
		Username: username,
		Provider: model.ProviderGitHub,
		Score: &model.Score{
			Grade:        grade,
			Value:        value,
			ModelVersion: score.ModelVersion,
		},
		ScoredAt: time.Now().UTC(),
	}

	// Unauthenticated callers get score only.
	if plan == "" {
		resp.Detail = "Sign up for full signal breakdown -> devtrace.thingz.io"
		return resp, nil
	}

	// Authenticated callers get categories, signals, and risk summary.
	resp.Score.Categories = score.Categories(*signals)
	resp.Signals = signalsFromInput(signals, profile)
	resp.RiskSummary = generateRiskSummary(signals, value)

	if repo != "" {
		resp.RepoContext = repoContextFromSignals(signals, repo)
	}

	// Starter and pro plans get license and AI sensing placeholders.
	if plan == "starter" || plan == "pro" {
		resp.License = nil   // placeholder: analyzer not yet built
		resp.AISensing = nil // placeholder: analyzer not yet built
	}

	return resp, nil
}

// signalsFromInput maps InputSignals and UserProfile to the response Signals type.
func signalsFromInput(s *score.InputSignals, p *ghclient.UserProfile) *model.Signals {
	return &model.Signals{
		AccountAgeDays:    s.AgeDays,
		Followers:         p.Followers,
		Following:         p.Following,
		PublicRepos:       p.PublicRepos,
		ForkedRepos:       s.ForkedRepos,
		PRsMerged:         s.PRsMerged,
		PRsClosed:         s.PRsClosed,
		RecentPRRepoCount: s.RecentPRRepoCount,
		HasBio:            s.HasBio,
		HasCompany:        s.HasCompany,
		HasLocation:       s.HasLocation,
		HasWebsite:        s.HasWebsite,
		OrgMember:         s.OrgMember,
		Suspended:         s.Suspended,
		AuthorAssociation: s.AuthorAssociation,
		CommitsVerified:   s.UnverifiedCommits == 0 && s.Commits > 0,
	}
}

// repoContextFromSignals maps repo-specific fields from InputSignals.
func repoContextFromSignals(s *score.InputSignals, repo string) *model.RepoContext {
	lcd := s.LastCommitDays
	return &model.RepoContext{
		Repo:              repo,
		Commits:           s.Commits,
		TotalCommits:      s.TotalCommits,
		TotalContributors: s.TotalContributors,
		LastCommitDays:    &lcd,
		OrgMember:         s.OrgMember,
		AuthorAssociation: s.AuthorAssociation,
		TrustedOrgMember:  s.TrustedOrgMember,
	}
}

// generateRiskSummary produces a human-readable risk assessment.
func generateRiskSummary(s *score.InputSignals, value float64) string {
	var summary string

	switch {
	case s.Suspended:
		summary = "Account is suspended. Do not merge without manual review."
	case value >= 0.8:
		summary = "Established account with strong contribution history."
	case value >= 0.5:
		summary = "Established account with consistent contribution history."
	case value >= 0.3:
		summary = "Limited contribution history. Careful review recommended."
	default:
		summary = "Minimal public activity. Manual review strongly recommended."
	}

	if s.AgeDays < 90 {
		summary += " Account was created recently."
	}
	if s.PRsMerged == 0 {
		summary += " No merged pull requests on record."
	}
	if s.AuthorAssociation == "FIRST_TIME_CONTRIBUTOR" {
		summary += " First-time contributor to this repository."
	}

	switch {
	case value >= 0.7:
		summary += " Standard review process is sufficient."
	case value >= 0.4:
		summary += " Enhanced review recommended."
	default:
		summary += " Maintainer review required."
	}

	return summary
}
