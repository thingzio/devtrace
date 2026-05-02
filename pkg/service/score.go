package service

import (
	"context"
	"fmt"
	"time"

	"github.com/thingzio/devtrace/pkg/bot"
	"github.com/thingzio/devtrace/pkg/claude"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/score"
)

// BehaviorStore provides behavioral signal data from contributor activity.
// GetLifetimeActivity returns aggregate lifetime counts; nil when no data exists.
type BehaviorStore interface {
	GetBehavioralSignals(ctx context.Context, username, provider string) (*model.Behavior, error)
	GetLifetimeActivity(ctx context.Context, username, provider string) (*model.LifetimeActivity, error)
}

// ScoreService orchestrates signal fetching, scoring, and response enrichment.
type ScoreService struct {
	gh       ghclient.Client
	behStore BehaviorStore // nil-safe; behavioral signals omitted when nil
	cache    *scoreCache
	claude   *claude.Client // nil = fallback to templates
	version  string         // DevTrace build version stamped on every response
}

// NewScoreService returns a ScoreService wired to the given GitHub client.
func NewScoreService(gh ghclient.Client, version string) *ScoreService {
	return &ScoreService{gh: gh, cache: newScoreCache(), version: version}
}

// Close stops background goroutines (e.g., cache eviction).
func (s *ScoreService) Close() {
	if s.cache != nil {
		s.cache.Close()
	}
}

// SetBehaviorStore sets an optional store for behavioral signal enrichment.
func (s *ScoreService) SetBehaviorStore(bs BehaviorStore) {
	s.behStore = bs
}

// SetClaudeClient sets an optional Claude client for AI-powered risk narratives.
func (s *ScoreService) SetClaudeClient(c *claude.Client) {
	s.claude = c
}

// Score fetches signals, computes a reputation score, and builds a plan-aware response.
// Results are cached to avoid redundant GitHub API calls.
func (s *ScoreService) Score(ctx context.Context, username, repo, plan string, trustedOrgs []string) (*model.ScoreResponse, error) {
	// Bot accounts get a predictable zero-score response — no API calls.
	if bot.IsBot(username) {
		return s.botResponse(username), nil
	}

	// Check cache first. Cached responses contain the full data;
	// plan-aware filtering is applied below before returning.
	if cached := s.cache.get(username, repo); cached != nil {
		return enrichForPlan(cached, plan), nil
	}

	// Fetch behavioral signals once — used for both archive hints and response enrichment.
	var behavior *model.Behavior
	if s.behStore != nil {
		if beh, err := s.behStore.GetBehavioralSignals(ctx, username, string(model.ProviderGitHub)); err == nil && beh != nil {
			behavior = beh
		}
	}

	// Build archive hints from GH Archive data when available.
	// Trusted hints (>= 7 active days) replace Search API calls entirely.
	// Untrusted hints are passed as a fallback floor when API calls fail.
	var hints *ghclient.ArchiveHints
	const minActiveDaysForHints = 7
	if behavior != nil {
		hints = &ghclient.ArchiveHints{
			PRsMerged:         int64(behavior.TotalPRsMerged),
			PRsClosed:         int64(behavior.TotalPRsClosed),
			RecentPRRepoCount: int64(behavior.DistinctRepos90d),
			Trusted:           behavior.ActiveDays >= minActiveDaysForHints,
		}
	}

	signals, err := s.gh.FetchSignals(ctx, username, repo, hints)
	if err != nil {
		return nil, fmt.Errorf("fetch signals: %w", err)
	}

	profile, err := s.gh.FetchUser(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("fetch user: %w", err)
	}

	// Check trusted org membership (caller-provided list).
	if len(trustedOrgs) > 0 && !signals.TrustedOrgMember {
		signals.TrustedOrgMember = s.checkTrustedOrgs(ctx, username, trustedOrgs)
	}

	hasRepo := repo != ""
	value := score.Compute(*signals, hasRepo, behavior)
	grade := score.Grade(value)
	categories := score.Categories(*signals, hasRepo, behavior)
	now := time.Now().UTC()

	// Build the full response (all fields populated).
	full := &model.ScoreResponse{
		Version:  s.version,
		Username: username,
		Provider: model.ProviderGitHub,
		Profile: &model.Profile{
			Name:      profile.Name,
			AvatarURL: profile.AvatarURL,
			Company:   profile.Company,
			Location:  profile.Location,
			Bio:       profile.Bio,
		},
		Score: &model.Score{
			Grade:      grade,
			Value:      value,
			Categories: categories,
		},
		Signals:     signalsFromInput(signals, profile),
		RiskSummary: generateRiskSummary(signals, value, repo != ""),
		ScoringMode: scoringMode(hasRepo),
		ScoredAt:    now,
	}

	if repo != "" {
		full.RepoContext = repoContextFromSignals(signals, repo)
	}

	// Try Claude for richer risk narrative (best-effort, falls back to template).
	if s.claude != nil {
		input := claude.RiskInput{
			Username:       username,
			Score:          value,
			Grade:          grade,
			Categories:     categories,
			AccountAge:     signals.AgeDays,
			PRsMerged:      signals.PRsMerged,
			PRsClosed:      signals.PRsClosed,
			Followers:      signals.Followers,
			PublicRepos:    signals.PublicRepos,
			Suspended:      signals.Suspended,
			HasRepoContext: repo != "",
			RepoContext:    repo,
		}
		if narrative, err := s.claude.GenerateRiskNarrative(ctx, input); err == nil && narrative != "" {
			full.RiskSummary = narrative
		}
	}

	// Attach behavioral signals (already fetched above for hints).
	if behavior != nil {
		full.Behavior = behavior
	}

	// Compute Tier 2 AI sensing heuristics (Pro only, gated in enrichForPlan).
	tier2 := score.ComputeTier2Heuristics(behavior, signals)
	if full.AISensing == nil {
		full.AISensing = &model.AISensing{}
	}
	full.AISensing.Behavioral = tier2

	// Populate enrichment block (profile decoration, surfaced when caller
	// requests detail view). Always cached; handler/plan layer decides exposure.
	if s.behStore != nil {
		if la, err := s.behStore.GetLifetimeActivity(ctx, username, string(model.ProviderGitHub)); err == nil && la != nil {
			full.Enrichment = &model.Enrichment{LifetimeActivity: la}
		}
	}

	// Cache the full response.
	s.cache.set(username, repo, full)

	return enrichForPlan(full, plan), nil
}

// enrichForPlan returns a deep copy of the response filtered for the caller's plan.
// Deep-copying pointer fields prevents downstream mutations from corrupting the cache.
func enrichForPlan(full *model.ScoreResponse, plan string) *model.ScoreResponse {
	resp := *full

	// Deep copy pointer fields so the caller cannot mutate cached data.
	if full.Score != nil {
		scoreCopy := *full.Score
		if full.Score.Categories != nil {
			scoreCopy.Categories = make(map[string]float64, len(full.Score.Categories))
			for k, v := range full.Score.Categories {
				scoreCopy.Categories[k] = v
			}
		}
		resp.Score = &scoreCopy
	}
	if full.Profile != nil {
		profileCopy := *full.Profile
		resp.Profile = &profileCopy
	}
	if full.Signals != nil {
		signalsCopy := *full.Signals
		resp.Signals = &signalsCopy
	}
	if full.RepoContext != nil {
		rcCopy := *full.RepoContext
		resp.RepoContext = &rcCopy
	}
	if full.AISensing != nil {
		aiCopy := *full.AISensing
		resp.AISensing = &aiCopy
	}
	if full.Behavior != nil {
		behCopy := *full.Behavior
		resp.Behavior = &behCopy
	}
	if full.Enrichment != nil {
		enrCopy := *full.Enrichment
		if full.Enrichment.LifetimeActivity != nil {
			laCopy := *full.Enrichment.LifetimeActivity
			enrCopy.LifetimeActivity = &laCopy
		}
		resp.Enrichment = &enrCopy
	}

	switch plan {
	case "": // unauthenticated — score only
		resp.Score = &model.Score{
			Grade: full.Score.Grade,
			Value: full.Score.Value,
		}
		resp.Profile = nil
		resp.Signals = nil
		resp.RiskSummary = ""
		resp.RepoContext = nil
		resp.License = nil
		resp.AISensing = nil
		resp.Behavior = nil
		resp.Enrichment = nil
		resp.Detail = "Sign up for full signal breakdown -> devtrace.thingz.io"
		now := time.Now().UTC()
		resp.CachedAt = &now

	case "free":
		// Free gets categories, signals, risk summary, behavior, AI sensing Tier 1 (metadata).
		resp.License = nil
		if resp.AISensing != nil {
			resp.AISensing.PRAuthenticity = nil // Starter+ only
			resp.AISensing.Behavioral = nil     // Pro only
		} else {
			resp.AISensing = &model.AISensing{}
		}

	case "starter":
		// Starter gets Tier 1 AI sensing (metadata + PR authenticity). No Tier 2.
		resp.License = nil
		if resp.AISensing != nil {
			resp.AISensing.Behavioral = nil // Tier 2 is Pro only
		} else {
			resp.AISensing = &model.AISensing{}
		}

	case "pro":
		// Pro gets everything including Tier 2 behavioral heuristics.
		if resp.AISensing == nil {
			resp.AISensing = &model.AISensing{}
		}
	}

	return &resp
}

// checkTrustedOrgs checks if the user is a member of any caller-provided trusted org.
// Best-effort: returns false on any error (doesn't block scoring).
func (s *ScoreService) checkTrustedOrgs(ctx context.Context, username string, orgs []string) bool {
	for _, org := range orgs {
		if org == "" {
			continue
		}
		if isMember, err := s.gh.IsOrgMember(ctx, org, username); err == nil && isMember {
			return true
		}
	}
	return false
}

func scoringMode(hasRepo bool) string {
	if hasRepo {
		return "repo"
	}
	return "global"
}

// signalsFromInput maps InputSignals and UserProfile to global response signals.
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
		HasPublicEmail:    s.HasPublicEmail,
		Suspended:         s.Suspended,
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
		CommitsVerified:   s.UnverifiedCommits == 0 && s.Commits > 0,
		AuthorAssociation: s.AuthorAssociation,
		TrustedOrgMember:  s.TrustedOrgMember,
	}
}

// botResponse returns a zero-score response for bot accounts.
func (s *ScoreService) botResponse(username string) *model.ScoreResponse {
	return &model.ScoreResponse{
		Version:  s.version,
		Username: username,
		Provider: model.ProviderGitHub,
		Score: &model.Score{
			Grade: "F",
			Value: 0,
		},
		RiskSummary: "Bot account detected. Scoring is not applicable.",
		ScoringMode: "bot",
		ScoredAt:    time.Now().UTC(),
	}
}

// generateRiskSummary produces a human-readable risk assessment.
// When repo is provided, uses review-oriented language. Without repo,
// focuses on contributor reputation.
func generateRiskSummary(s *score.InputSignals, value float64, hasRepo bool) string {
	var summary string

	switch {
	case s.Suspended:
		if hasRepo {
			summary = "Account is suspended. Do not merge without manual review."
		} else {
			summary = "Account is suspended."
		}
	case value >= 0.8:
		summary = "Well-established contributor with strong activity history."
	case value >= 0.5:
		summary = "Established contributor with consistent activity history."
	case value >= 0.3:
		summary = "Limited contribution history."
	default:
		summary = "Minimal public activity."
	}

	if s.AgeDays < 90 {
		summary += " Account was created recently."
	}
	if s.PRsMerged == 0 {
		summary += " No merged pull requests on record."
	}

	if hasRepo {
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
	}

	return summary
}
