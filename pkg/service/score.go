package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/bot"
	"github.com/thingzio/devtrace/pkg/claude"
	"github.com/thingzio/devtrace/pkg/config"
	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
	"github.com/thingzio/devtrace/pkg/ossf"
	"github.com/thingzio/devtrace/pkg/plan"
	profilepkg "github.com/thingzio/devtrace/pkg/profile"
	"github.com/thingzio/devtrace/pkg/registry"
	"github.com/thingzio/devtrace/pkg/score"
	"github.com/thingzio/devtrace/pkg/stackoverflow"
)

// OSSFFetcher is the subset of the OSSF Scorecard client used by the
// score service. Defined here so tests can inject a mock without
// spinning up an httptest server.
type OSSFFetcher interface {
	Fetch(ctx context.Context, owner, repo string) (*ossf.Scorecard, error)
}

// NPMFetcher is the subset of the npm registry client used by the
// score service. Defined here so tests can inject a mock without
// spinning up an httptest server. Returns (total, top, err) where
// total is the unbounded count and top is the display-capped list.
type NPMFetcher interface {
	FetchUserPackages(ctx context.Context, username string, topLimit int) (int, []registry.Package, error)
}

// StackOverflowFetcher is the subset of the SE Data API client used
// by the score service. Defined here so tests can inject a mock
// without spinning up an httptest server.
type StackOverflowFetcher interface {
	FetchUser(ctx context.Context, userID int64) (*stackoverflow.Profile, error)
}

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
	GetOSSFScorecard(ctx context.Context, provider, owner, repo string) (*model.OSSFScorecard, time.Time, error)
	SaveOSSFScorecard(ctx context.Context, provider, owner, repo string, card *model.OSSFScorecard) error
	GetPublisherProfile(ctx context.Context, provider, username, registry string) (*model.RegistryProfile, time.Time, error)
	SavePublisherProfile(ctx context.Context, provider, username, registry string, profile *model.RegistryProfile) error
	GetStackOverflowProfile(ctx context.Context, provider, username string) (*model.StackOverflow, time.Time, error)
	SaveStackOverflowProfile(ctx context.Context, provider, username string, profile *model.StackOverflow) error
}

// topContributedRepoLimit caps the number of repos surfaced in enrichment.
// This is a UI/display limit; operational tunables (TTLs, API caps) live
// in pkg/config.
const topContributedRepoLimit = 5

// logKeyRepo is the slog attribute key for owner/repo identifiers.
// Extracted as a constant so goconst is satisfied across the multiple
// log sites in the OSSF enrichment helper.
const logKeyRepo = "repo"

// ScoreService orchestrates signal fetching, scoring, and response enrichment.
type ScoreService struct {
	gh       ghclient.Client
	behStore BehaviorStore        // nil-safe; behavioral signals omitted when nil
	ossf     OSSFFetcher          // nil-safe; repo OSSF Scorecard enrichment omitted when nil
	npm      NPMFetcher           // nil-safe; npm publisher enrichment omitted when nil
	so       StackOverflowFetcher // nil-safe; SO enrichment omitted when nil
	cache    *scoreCache
	claude   *claude.Client // nil = fallback to templates
	version  string         // DevTrace build version stamped on every response
}

// NewScoreService returns a ScoreService wired to the given GitHub client.
// The OSSF Scorecard, npm publisher, and Stack Overflow fetchers are
// initialized to defaults; pass SetOSSFFetcher / SetNPMFetcher /
// SetStackOverflowFetcher to override (e.g., a test mock or to
// disable entirely).
func NewScoreService(gh ghclient.Client, version string) *ScoreService {
	return &ScoreService{
		gh:      gh,
		ossf:    ossf.NewClient(config.OSSFTimeout()),
		npm:     registry.NewNPMClient(config.PublisherTimeout()),
		so:      stackoverflow.NewClient(config.StackOverflowTimeout(), config.StackOverflowAPIKey()),
		cache:   newScoreCache(),
		version: version,
	}
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

// SetOSSFFetcher overrides the default OSSF Scorecard client. Pass nil
// to disable OSSF enrichment entirely; useful for tests and for
// environments where the OSSF API is unreachable.
func (s *ScoreService) SetOSSFFetcher(f OSSFFetcher) {
	s.ossf = f
}

// SetNPMFetcher overrides the default npm registry client. Pass nil
// to disable npm-publisher enrichment entirely; useful for tests
// and for environments where the npm registry is unreachable.
func (s *ScoreService) SetNPMFetcher(f NPMFetcher) {
	s.npm = f
}

// SetStackOverflowFetcher overrides the default Stack Exchange API
// client. Pass nil to disable SO enrichment entirely.
func (s *ScoreService) SetStackOverflowFetcher(f StackOverflowFetcher) {
	s.so = f
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
		Scope:       buildScopeInfo(hasRepo),
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
	full.Enrichment = s.buildEnrichment(ctx, username, repo, profile)

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
		resp.Scope = nil
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
		// Email extraction, security credits, OSSF Scorecard,
		// publisher detection, and Stack Overflow are Starter+.
		// Emails are gated to deter scraping; the others are
		// paid-tier upgrade hooks (repo-quality, credibility,
		// supply-chain, and cross-platform reputation signals).
		if resp.Enrichment != nil {
			resp.Enrichment.Emails = nil
			resp.Enrichment.SecurityCredits = nil
			resp.Enrichment.OSSFScorecard = nil
			resp.Enrichment.Publisher = nil
			resp.Enrichment.StackOverflow = nil
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
		return "repo" //nolint:goconst // distinct semantic from logKeyRepo (slog key); inlining a constant just to share spelling would obscure intent
	}
	return "global"
}

// buildScopeInfo enumerates which response fields are scoped to the
// requested repo vs the contributor's profile. Always returns a
// non-nil ScopeInfo so consumers can rely on its presence; RepoScoped
// is empty when no repo was provided. The lists use JSON-path strings
// rooted at the response (e.g. "enrichment.ossf_scorecard") so they
// remain stable as the response struct evolves.
func buildScopeInfo(hasRepo bool) *model.ScopeInfo {
	global := []string{
		"profile",
		"signals",
		"risk_summary",
		"behavior",
		"ai_sensing",
		"enrichment.lifetime_activity",
		"enrichment.reciprocity",
		"enrichment.top_contributed_repos",
		"enrichment.linked_accounts",
		"enrichment.emails",
		"enrichment.owned_repos",
		"enrichment.security_credits",
		"enrichment.publisher",
		"enrichment.stack_overflow",
	}
	repoScoped := []string{}
	if hasRepo {
		repoScoped = []string{
			"repo_context",
			"enrichment.ossf_scorecard",
		}
	}
	return &model.ScopeInfo{
		RepoScoped: repoScoped,
		Global:     global,
	}
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
	dst.OwnedRepos = copyOwnedRepos(src.OwnedRepos)
	if src.TopContributedRepos != nil {
		dst.TopContributedRepos = append([]model.RepoContribution(nil), src.TopContributedRepos...)
	}
	if src.LinkedAccounts != nil {
		dst.LinkedAccounts = append([]model.LinkedAccount(nil), src.LinkedAccounts...)
	}
	if src.Emails != nil {
		dst.Emails = append([]string(nil), src.Emails...)
	}
	dst.SecurityCredits = copySecurityCredits(src.SecurityCredits)
	dst.OSSFScorecard = copyOSSFScorecard(src.OSSFScorecard)
	dst.Publisher = copyPublisher(src.Publisher)
	dst.StackOverflow = copyStackOverflow(src.StackOverflow)
	return &dst
}

func copyOwnedRepos(src *model.OwnedRepos) *model.OwnedRepos {
	if src == nil {
		return nil
	}
	or := *src
	if src.Top != nil {
		or.Top = append([]model.OwnedRepo(nil), src.Top...)
	}
	if src.Languages != nil {
		or.Languages = append([]model.LanguageBucket(nil), src.Languages...)
	}
	return &or
}

func copySecurityCredits(src *model.SecurityCredits) *model.SecurityCredits {
	if src == nil {
		return nil
	}
	sc := *src
	if src.BySeverity != nil {
		sc.BySeverity = make(map[string]int, len(src.BySeverity))
		for k, v := range src.BySeverity {
			sc.BySeverity[k] = v
		}
	}
	if src.Recent != nil {
		sc.Recent = append([]model.SecurityCredit(nil), src.Recent...)
	}
	return &sc
}

func copyOSSFScorecard(src *model.OSSFScorecard) *model.OSSFScorecard {
	if src == nil {
		return nil
	}
	o := *src
	if src.Checks != nil {
		o.Checks = append([]model.OSSFCheck(nil), src.Checks...)
	}
	return &o
}

func copyPublisher(src *model.Publisher) *model.Publisher {
	if src == nil {
		return nil
	}
	p := *src
	p.NPM = copyRegistryProfile(src.NPM)
	p.PyPI = copyRegistryProfile(src.PyPI)
	return &p
}

func copyRegistryProfile(src *model.RegistryProfile) *model.RegistryProfile {
	if src == nil {
		return nil
	}
	rp := *src
	if src.Top != nil {
		rp.Top = append([]model.Package(nil), src.Top...)
	}
	return &rp
}

func copyStackOverflow(src *model.StackOverflow) *model.StackOverflow {
	if src == nil {
		return nil
	}
	so := *src
	if src.CreatedAt != nil {
		t := *src.CreatedAt
		so.CreatedAt = &t
	}
	if src.LastAccessAt != nil {
		t := *src.LastAccessAt
		so.LastAccessAt = &t
	}
	return &so
}

// buildEnrichment populates the optional Enrichment block from the
// behavior store and the freshly-fetched profile. Each sub-block is
// independent — a failure or absent data in one does not suppress others.
// Returns nil when no sub-block has data, so callers can rely on
// omitempty serialization. The repo argument scopes any repo-level
// enrichment (e.g., OSSF Scorecard); empty means user-only.
func (s *ScoreService) buildEnrichment(ctx context.Context, username, repo string, profile *ghclient.UserProfile) *model.Enrichment {
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

	if card := s.ossfScorecardEnrichment(ctx, repo); card != nil {
		enr.OSSFScorecard = card
		populated = true
	}

	if pub := s.publisherEnrichment(ctx, username); pub != nil {
		enr.Publisher = pub
		populated = true
	}

	if so := s.stackOverflowEnrichment(ctx, username, enr.LinkedAccounts); so != nil {
		enr.StackOverflow = so
		populated = true
	}

	if !populated {
		return nil
	}
	return enr
}

// stackOverflowEnrichment surfaces the contributor's Stack Overflow
// profile when their GitHub bio/blog declares a stackoverflow.com
// link. Discovery is two-stage:
//
//  1. The profile pkg has already classified declared links into
//     LinkedAccounts (we get them passed in here, not re-extracted).
//  2. We extract the SO numeric user_id from the first SO-platform
//     link, look up the cached SO profile, and refresh from the SE
//     Data API when stale.
//
// A user without an SO link declared on their GitHub profile gets a
// zero-rep sentinel saved on first lookup so the TTL check suppresses
// re-asking. UI render path treats UserID==0 as "no SO data".
func (s *ScoreService) stackOverflowEnrichment(ctx context.Context, username string, linked []model.LinkedAccount) *model.StackOverflow {
	if s.behStore == nil || s.so == nil {
		return nil
	}
	provider := string(model.ProviderGitHub)

	cached, fetchedAt, err := s.behStore.GetStackOverflowProfile(ctx, provider, username)
	if err == nil && !fetchedAt.IsZero() && time.Since(fetchedAt) < config.StackOverflowTTL() {
		return materializeSO(cached)
	}

	soURL, soUserID := pickStackOverflowLink(linked)
	if soUserID == 0 {
		// No SO link declared on profile. Save sentinel so the TTL
		// check stops us from re-scanning LinkedAccounts every score
		// request — the answer won't change without a profile update.
		empty := &model.StackOverflow{}
		if err := s.behStore.SaveStackOverflowProfile(ctx, provider, username, empty); err != nil {
			slog.Warn("save stackoverflow sentinel", "username", username, "error", err)
		}
		return nil
	}

	prof, ferr := s.so.FetchUser(ctx, soUserID)
	switch {
	case errors.Is(ferr, stackoverflow.ErrNotFound):
		// SO link points to a user the SE API doesn't recognize
		// (deleted account, typo). Sentinel-save so we don't keep
		// asking; UI shows nothing.
		empty := &model.StackOverflow{}
		if err := s.behStore.SaveStackOverflowProfile(ctx, provider, username, empty); err != nil {
			slog.Warn("save stackoverflow not-found sentinel", "username", username, "error", err)
		}
		return nil
	case ferr != nil:
		slog.Warn("fetch stackoverflow profile",
			"username", username,
			"so_user_id", soUserID,
			"error", ferr,
			"had_cache", cached != nil,
		)
		return materializeSO(cached)
	}

	out := &model.StackOverflow{
		UserID:      prof.UserID,
		DisplayName: prof.DisplayName,
		Reputation:  prof.Reputation,
		BadgeBronze: prof.BadgeBronze,
		BadgeSilver: prof.BadgeSilver,
		BadgeGold:   prof.BadgeGold,
		URL:         prof.URL,
	}
	if !prof.CreatedAt.IsZero() {
		t := prof.CreatedAt
		out.CreatedAt = &t
	}
	if !prof.LastAccessAt.IsZero() {
		t := prof.LastAccessAt
		out.LastAccessAt = &t
	}
	// SE Link sometimes lacks the URL we expect; preserve the
	// originating profile link as a fallback for the UI.
	if out.URL == "" && soURL != "" {
		out.URL = soURL
	}
	if err := s.behStore.SaveStackOverflowProfile(ctx, provider, username, out); err != nil {
		slog.Warn("save stackoverflow profile", "username", username, "error", err)
	}
	return out
}

// materializeSO converts a cached SO row into a renderable result.
// Zero-rep / zero-id sentinels collapse to nil so the UI render
// path's `{{with .StackOverflow}}` falls through cleanly.
func materializeSO(card *model.StackOverflow) *model.StackOverflow {
	if card == nil || card.UserID == 0 {
		return nil
	}
	return card
}

// pickStackOverflowLink returns the first stackoverflow URL +
// extracted user ID from the contributor's classified linked
// accounts. Returns (url, 0) if no SO link is found or the URL
// can't be parsed for an ID.
func pickStackOverflowLink(linked []model.LinkedAccount) (string, int64) {
	for _, a := range linked {
		if a.Platform != "stackoverflow" {
			continue
		}
		if id := stackoverflow.ExtractUserID(a.URL); id > 0 {
			return a.URL, id
		}
	}
	return "", 0
}

// publisherEnrichment returns the cached publisher profile aggregate
// (currently npm only), refreshing from the registry when the row is
// missing or older than config.PublisherTTL. Returns nil when the
// behavior store / npm fetcher is unconfigured, or when the
// contributor publishes nothing — the UI render path treats a
// zero-package profile as absent so empty tiles don't appear.
//
// PyPI is reserved as a future registry; the schema and model
// already accommodate it. v1 covers npm only because PyPI has no
// public reverse-lookup API.
func (s *ScoreService) publisherEnrichment(ctx context.Context, username string) *model.Publisher {
	if s.behStore == nil || s.npm == nil {
		return nil
	}
	provider := string(model.ProviderGitHub)

	npmProfile := s.npmPublisherProfile(ctx, provider, username)
	if npmProfile == nil || npmProfile.PackageCount == 0 {
		return nil
	}
	return &model.Publisher{
		NPM:           npmProfile,
		TotalPackages: npmProfile.PackageCount,
	}
}

// npmPublisherProfile fetches (or returns cached) the npm publisher
// profile. A non-nil zero-count return is the sentinel for "we
// looked, found nothing" so the publisherEnrichment caller can
// short-circuit and the cache TTL suppresses repeated re-fetching.
func (s *ScoreService) npmPublisherProfile(ctx context.Context, provider, username string) *model.RegistryProfile {
	const registryName = "npm"
	cached, fetchedAt, err := s.behStore.GetPublisherProfile(ctx, provider, username, registryName)
	if err == nil && !fetchedAt.IsZero() && time.Since(fetchedAt) < config.PublisherTTL() {
		return cached
	}

	total, top, ferr := s.npm.FetchUserPackages(ctx, username, config.PublisherTopLimit())
	switch {
	case errors.Is(ferr, registry.ErrNotFound):
		// Sentinel: zero count, no packages. Suppresses re-fetching
		// for users without an npm publisher account.
		empty := &model.RegistryProfile{}
		if err := s.behStore.SavePublisherProfile(ctx, provider, username, registryName, empty); err != nil {
			slog.Warn("save npm sentinel", "username", username, "error", err)
		}
		return empty
	case ferr != nil:
		slog.Warn("fetch npm publisher",
			"username", username,
			"error", ferr,
			"had_cache", cached != nil,
		)
		return cached
	}

	out := &model.RegistryProfile{
		PackageCount: total,
		Top:          make([]model.Package, 0, len(top)),
	}
	for _, p := range top {
		out.Top = append(out.Top, model.Package{
			Name: p.Name,
			Role: p.Role,
			URL:  p.URL,
		})
	}
	if err := s.behStore.SavePublisherProfile(ctx, provider, username, registryName, out); err != nil {
		slog.Warn("save npm publisher", "username", username, "error", err)
	}
	return out
}

// ossfScorecardEnrichment returns the cached OSSF Scorecard for the
// requested repo, refreshing from the OpenSSF API when the row is
// missing or older than config.OSSFTTL. Returns nil when no repo is
// scoped, when the OSSF fetcher / behavior store is unconfigured, or
// when the upstream has no scorecard for this repo (a sentinel row
// records that fact so we don't re-ask on every score request).
func (s *ScoreService) ossfScorecardEnrichment(ctx context.Context, repo string) *model.OSSFScorecard {
	if repo == "" || s.behStore == nil || s.ossf == nil {
		return nil
	}
	owner, name, ok := splitOwnerRepo(repo)
	if !ok {
		return nil
	}
	provider := string(model.ProviderGitHub)

	cached, fetchedAt, err := s.behStore.GetOSSFScorecard(ctx, provider, owner, name)
	if err == nil && !fetchedAt.IsZero() && time.Since(fetchedAt) < config.OSSFTTL() {
		return materializeOSSF(cached)
	}

	card, ferr := s.ossf.Fetch(ctx, owner, name)
	switch {
	case errors.Is(ferr, ossf.ErrNotFound):
		// Save a sentinel so we don't re-ask for repos OSSF has nothing on.
		if err := s.behStore.SaveOSSFScorecard(ctx, provider, owner, name,
			&model.OSSFScorecard{}); err != nil {
			slog.Warn("save ossf sentinel", logKeyRepo, repo, "error", err)
		}
		return nil
	case ferr != nil:
		slog.Warn("fetch ossf scorecard",
			logKeyRepo, repo,
			"error", ferr,
			"had_cache", cached != nil,
		)
		return materializeOSSF(cached)
	}

	out := &model.OSSFScorecard{
		Score:        card.Score,
		Date:         card.Date,
		Commit:       card.Commit,
		ScorecardVer: card.ScorecardVer,
		Checks:       make([]model.OSSFCheck, 0, len(card.Checks)),
	}
	for _, c := range card.Checks {
		out.Checks = append(out.Checks, model.OSSFCheck{
			Name:   c.Name,
			Score:  c.Score,
			Reason: c.Reason,
			DocURL: c.DocURL,
		})
	}
	if err := s.behStore.SaveOSSFScorecard(ctx, provider, owner, name, out); err != nil {
		slog.Warn("save ossf scorecard", logKeyRepo, repo, "error", err)
	}
	return out
}

// materializeOSSF turns a cached row into a renderable card. The
// "fetched but upstream had nothing" sentinel (zero score, no checks)
// becomes nil here so the UI render path's `{{with .OSSFScorecard}}`
// falls through cleanly.
func materializeOSSF(card *model.OSSFScorecard) *model.OSSFScorecard {
	if card == nil || len(card.Checks) == 0 {
		return nil
	}
	return card
}

// splitOwnerRepo parses an "owner/repo" string. Returns ok=false on
// any other shape (empty, no slash, multiple slashes, blank parts) so
// the caller skips OSSF without surfacing a confusing error.
func splitOwnerRepo(repo string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(repo), "/", 3)
	if len(parts) != 2 {
		return "", "", false
	}
	owner := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	if owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
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
