package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/bot"
	"github.com/thingzio/devtrace/pkg/claude"
	"github.com/thingzio/devtrace/pkg/config"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/plan"
	profilepkg "github.com/thingzio/devtrace/pkg/profile"
	"github.com/thingzio/devtrace/pkg/score"
)

// BehaviorStore provides behavioral signal data from contributor activity.
// GetLifetimeActivity returns aggregate lifetime counts; nil when no data exists.
// GetTopContributedRepos returns the top-N repos ranked by active-hour count.
// GetRepoSummary / SaveRepoSummary cache the contributor's owned-repos
// aggregate for 24h to amortize the cost of GitHub /users/{u}/repos calls.
// GetSecurityCredits / SaveSecurityCredits cache the contributor's GHSA
// advisory credits with a TTL governed by config.SecurityCreditTTL.
type BehaviorStore interface {
	GetBehavioralSignals(ctx context.Context, username, provider string) (*model.Behavior, error)
	GetLifetimeActivity(ctx context.Context, username, provider string) (*model.LifetimeActivity, error)
	GetTopContributedRepos(ctx context.Context, username, provider string, limit int) ([]model.RepoContribution, error)
	GetRepoSummary(ctx context.Context, username, provider string) (*model.OwnedRepos, time.Time, error)
	SaveRepoSummary(ctx context.Context, username, provider string, summary *model.OwnedRepos) error
	GetSecurityCredits(ctx context.Context, username, provider string) (*model.SecurityCredits, time.Time, error)
	SaveSecurityCredits(ctx context.Context, username, provider string, credits []model.SecurityCredit) error
}

// topContributedRepoLimit caps the number of repos surfaced in enrichment.
// This is a UI/display limit; operational tunables (TTLs, API caps) live
// in pkg/config.
const topContributedRepoLimit = 5

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
func (s *ScoreService) Score(ctx context.Context, username, repo, planName string, trustedOrgs []string) (*model.ScoreResponse, error) {
	// Bot accounts get a predictable zero-score response — no API calls.
	if bot.IsBot(username) {
		return s.botResponse(username), nil
	}

	// Check cache first. Cached responses contain the full data;
	// plan-aware filtering is applied below before returning.
	if cached := s.cache.get(username, repo); cached != nil {
		return enrichForPlan(cached, planName), nil
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
	full.Enrichment = s.buildEnrichment(ctx, username, profile)

	// Cache the full response.
	s.cache.set(username, repo, full)

	return enrichForPlan(full, planName), nil
}

// enrichForPlan returns a deep copy of the response filtered for the caller's plan.
// Deep-copying pointer fields prevents downstream mutations from corrupting the cache.
func enrichForPlan(full *model.ScoreResponse, planName string) *model.ScoreResponse {
	respPtr := deepCopyResponse(full)
	resp := *respPtr

	switch planName {
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

	case plan.PlanFree:
		// Free gets categories, signals, risk summary, behavior, AI sensing Tier 1 (metadata).
		resp.License = nil
		if resp.AISensing != nil {
			resp.AISensing.PRAuthenticity = nil // Starter+ only
			resp.AISensing.Behavioral = nil     // Pro only
		} else {
			resp.AISensing = &model.AISensing{}
		}
		// Email extraction and security credits are Starter+. Emails are
		// gated to deter scraping the API as a contact list; security
		// credits are gated as a paid-tier upgrade hook (the strongest
		// "rescues bug-bounty researcher" signal).
		if resp.Enrichment != nil {
			resp.Enrichment.Emails = nil
			resp.Enrichment.SecurityCredits = nil
		}

	case plan.PlanStarter:
		// Starter gets Tier 1 AI sensing (metadata + PR authenticity). No Tier 2.
		resp.License = nil
		if resp.AISensing != nil {
			resp.AISensing.Behavioral = nil // Tier 2 is Pro only
		} else {
			resp.AISensing = &model.AISensing{}
		}

	case plan.PlanPro:
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

// deepCopyResponse returns a copy of full with every pointer / slice /
// map field independently allocated so that downstream mutation cannot
// corrupt the cached source. Split out from enrichForPlan to keep that
// function within the cyclomatic-complexity budget.
func deepCopyResponse(full *model.ScoreResponse) *model.ScoreResponse {
	resp := *full
	if full.Score != nil {
		sc := *full.Score
		if full.Score.Categories != nil {
			sc.Categories = make(map[string]float64, len(full.Score.Categories))
			for k, v := range full.Score.Categories {
				sc.Categories[k] = v
			}
		}
		resp.Score = &sc
	}
	if full.Profile != nil {
		p := *full.Profile
		resp.Profile = &p
	}
	if full.Signals != nil {
		s := *full.Signals
		resp.Signals = &s
	}
	if full.RepoContext != nil {
		rc := *full.RepoContext
		resp.RepoContext = &rc
	}
	if full.AISensing != nil {
		ai := *full.AISensing
		resp.AISensing = &ai
	}
	if full.Behavior != nil {
		b := *full.Behavior
		resp.Behavior = &b
	}
	if full.Enrichment != nil {
		resp.Enrichment = deepCopyEnrichment(full.Enrichment)
	}
	return &resp
}

// deepCopyEnrichment clones the Enrichment block including its nested
// pointer / slice fields. Caller must guard against nil input.
func deepCopyEnrichment(src *model.Enrichment) *model.Enrichment {
	dst := *src
	if src.LifetimeActivity != nil {
		la := *src.LifetimeActivity
		dst.LifetimeActivity = &la
	}
	if src.Reciprocity != nil {
		r := *src.Reciprocity
		dst.Reciprocity = &r
	}
	if src.OwnedRepos != nil {
		or := *src.OwnedRepos
		if src.OwnedRepos.Top != nil {
			or.Top = append([]model.OwnedRepo(nil), src.OwnedRepos.Top...)
		}
		if src.OwnedRepos.Languages != nil {
			or.Languages = append([]model.LanguageBucket(nil), src.OwnedRepos.Languages...)
		}
		dst.OwnedRepos = &or
	}
	if src.TopContributedRepos != nil {
		dst.TopContributedRepos = append([]model.RepoContribution(nil), src.TopContributedRepos...)
	}
	if src.LinkedAccounts != nil {
		dst.LinkedAccounts = append([]model.LinkedAccount(nil), src.LinkedAccounts...)
	}
	if src.Emails != nil {
		dst.Emails = append([]string(nil), src.Emails...)
	}
	if src.SecurityCredits != nil {
		sc := *src.SecurityCredits
		if src.SecurityCredits.BySeverity != nil {
			sc.BySeverity = make(map[string]int, len(src.SecurityCredits.BySeverity))
			for k, v := range src.SecurityCredits.BySeverity {
				sc.BySeverity[k] = v
			}
		}
		if src.SecurityCredits.Recent != nil {
			sc.Recent = append([]model.SecurityCredit(nil), src.SecurityCredits.Recent...)
		}
		dst.SecurityCredits = &sc
	}
	return &dst
}

// buildEnrichment populates the optional Enrichment block from the
// behavior store and the freshly-fetched profile. Each sub-block is
// independent — a failure or absent data in one does not suppress others.
// Returns nil when no sub-block has data, so callers can rely on
// omitempty serialization.
func (s *ScoreService) buildEnrichment(ctx context.Context, username string, profile *ghclient.UserProfile) *model.Enrichment {
	enr := &model.Enrichment{}
	populated := false

	if s.behStore != nil {
		provider := string(model.ProviderGitHub)
		if la, err := s.behStore.GetLifetimeActivity(ctx, username, provider); err == nil && la != nil {
			enr.LifetimeActivity = la
			populated = true
			// Reciprocity is derived purely from lifetime counts — no extra
			// query — so it travels with LifetimeActivity automatically.
			if r := la.ComputeReciprocity(); r != nil {
				enr.Reciprocity = r
			}
		}
		if repos, err := s.behStore.GetTopContributedRepos(ctx, username, provider, topContributedRepoLimit); err == nil && len(repos) > 0 {
			enr.TopContributedRepos = repos
			populated = true
		}
	}

	if profile != nil {
		accts, emails := profilepkg.Extract(profile.Bio, profile.Website, profile.Email)
		if len(accts) > 0 {
			enr.LinkedAccounts = accts
			populated = true
		}
		if len(emails) > 0 {
			enr.Emails = emails
			populated = true
		}
	}

	if owned := s.ownedReposEnrichment(ctx, username); owned != nil {
		enr.OwnedRepos = owned
		populated = true
	}

	if creds := s.securityCreditsEnrichment(ctx, username); creds != nil {
		enr.SecurityCredits = creds
		populated = true
	}

	if !populated {
		return nil
	}
	return enr
}

// securityCreditsEnrichment returns the cached GHSA credits aggregate.
//
// Live fetching is gated by config.SecurityCreditsEnabled, which defaults
// to OFF. v0.21.0 shipped a GraphQL query against `User.securityAdvisoryCredits`
// — a field that does not exist on GitHub's User type (confirmed by
// production logs returning `undefinedField` for every user). There is
// no other cheap path: the REST `/advisories` endpoint silently ignores
// `credit_user`, and iterating `Query.securityAdvisories` to filter
// client-side is too expensive for a synchronous score request.
//
// Storage, model, and UI render path are kept in place so that flipping
// the flag back on (when GitHub exposes a viable API, or we add a
// background indexer) is a one-line change. The cache lookup runs even
// when disabled so any pre-existing rows surface, but the fetch path is
// short-circuited.
func (s *ScoreService) securityCreditsEnrichment(ctx context.Context, username string) *model.SecurityCredits {
	if s.behStore == nil {
		return nil
	}
	provider := string(model.ProviderGitHub)

	cached, fetchedAt, err := s.behStore.GetSecurityCredits(ctx, username, provider)
	if err == nil && !fetchedAt.IsZero() && time.Since(fetchedAt) < config.SecurityCreditTTL() {
		return cached
	}

	if !config.SecurityCreditsEnabled() {
		return cached
	}

	credits, ferr := s.gh.FetchSecurityCredits(ctx, username, config.SecurityCreditLimit())
	if ferr != nil {
		slog.Warn("fetch security credits",
			"username", username,
			"error", ferr,
			"had_cache", cached != nil,
		)
		return cached
	}
	slog.Debug("fetched security credits",
		"username", username,
		"count", len(credits),
	)

	modelCredits := make([]model.SecurityCredit, 0, len(credits))
	for _, c := range credits {
		sev := c.Severity
		if sev == "" {
			sev = "unknown"
		}
		modelCredits = append(modelCredits, model.SecurityCredit{
			AdvisoryID:  c.AdvisoryID,
			CreditType:  c.CreditType,
			Severity:    sev,
			CVEID:       c.CVEID,
			Summary:     c.Summary,
			PublishedAt: c.PublishedAt,
		})
	}
	if err := s.behStore.SaveSecurityCredits(ctx, username, provider, modelCredits); err != nil {
		_ = err
	}

	// Re-read so the aggregate (counts + bySeverity + recent slice) is
	// computed from the canonical query rather than duplicated here.
	fresh, _, _ := s.behStore.GetSecurityCredits(ctx, username, provider)
	return fresh
}

// ownedReposEnrichment returns the cached OwnedRepos aggregate, refreshing
// from GitHub when the cache is missing or older than config.RepoSummaryTTL.
// The refresh path tolerates errors silently — a stale-or-missing summary
// is strictly better than failing the whole score request.
func (s *ScoreService) ownedReposEnrichment(ctx context.Context, username string) *model.OwnedRepos {
	if s.behStore == nil {
		return nil
	}
	provider := string(model.ProviderGitHub)

	cached, fetchedAt, err := s.behStore.GetRepoSummary(ctx, username, provider)
	if err == nil && cached != nil && time.Since(fetchedAt) < config.RepoSummaryTTL() {
		return cached
	}

	repos, ferr := s.gh.ListUserRepos(ctx, username, config.RepoListLimit())
	if ferr != nil {
		// On fetch failure return whatever we have cached even if stale.
		return cached
	}
	summary := profilepkg.AggregateRepos(repos)
	if err := s.behStore.SaveRepoSummary(ctx, username, provider, summary); err != nil {
		// Failure to persist is non-fatal — return the freshly computed
		// summary so the caller sees current data.
		_ = err
	}
	return summary
}
