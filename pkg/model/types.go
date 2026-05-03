package model

import "time"

// Provider identifies the source platform for contributor data.
type Provider string

// ProviderGitHub is the GitHub provider.
const ProviderGitHub Provider = "github"

// ScoreResponse is the top-level API response for a contributor score.
type ScoreResponse struct {
	Version     string       `json:"version"`
	Username    string       `json:"username"`
	Provider    Provider     `json:"provider"`
	Profile     *Profile     `json:"profile,omitempty"`
	Score       *Score       `json:"score"`
	Signals     *Signals     `json:"signals,omitempty"`
	RiskSummary string       `json:"risk_summary,omitempty"`
	RepoContext *RepoContext `json:"repo_context,omitempty"`
	License     *License     `json:"license,omitempty"`
	AISensing   *AISensing   `json:"ai_sensing,omitempty"`
	Behavior    *Behavior    `json:"behavior,omitempty"`
	Enrichment  *Enrichment  `json:"enrichment,omitempty"`
	ScoringMode string       `json:"scoring_mode"` // "global" or "repo"
	Scope       *ScopeInfo   `json:"scope,omitempty"`
	ScoredAt    time.Time    `json:"scored_at"`
	CachedAt    *time.Time   `json:"cached_at,omitempty"`
	Detail      string       `json:"detail,omitempty"`
}

// ScopeInfo documents which response fields are scoped to the
// requested repo vs the contributor's profile. Lets API consumers
// distinguish "this contributor in NVIDIA/aicr" data from "this
// contributor across all of GitHub" data without inferring scope
// from naming or absence. Both lists are always populated when this
// block is present (RepoScoped is empty when no repo was requested).
type ScopeInfo struct {
	RepoScoped []string `json:"repo_scoped"`
	Global     []string `json:"global"`
}

// Enrichment holds optional profile-decoration data populated when the
// caller requests a detail view (?detail=true). All sub-blocks are
// independently optional and may be absent without affecting the score.
type Enrichment struct {
	LifetimeActivity    *LifetimeActivity  `json:"lifetime_activity,omitempty"`
	TopContributedRepos []RepoContribution `json:"top_contributed_repos,omitempty"`
	LinkedAccounts      []LinkedAccount    `json:"linked_accounts,omitempty"`
	Emails              []string           `json:"emails,omitempty"`
	Reciprocity         *Reciprocity       `json:"reciprocity,omitempty"`
	OwnedRepos          *OwnedRepos        `json:"owned_repos,omitempty"`
	SecurityCredits     *SecurityCredits   `json:"security_credits,omitempty"`
	OSSFScorecard       *OSSFScorecard     `json:"ossf_scorecard,omitempty"`
	Publisher           *Publisher         `json:"publisher,omitempty"`
}

// Publisher aggregates the contributor's published-package presence
// across registries. Strong supply-chain credibility signal: a
// contributor whose GitHub handle owns published npm/PyPI packages
// has user-attested publisher identity that downstream consumers
// rely on. v1 covers npm only; PyPI is a known gap (no public
// reverse-lookup API). A non-nil Publisher with TotalPackages == 0
// is meaningful — it records "we looked, nothing found" — but the
// UI render path treats it as absent.
type Publisher struct {
	NPM           *RegistryProfile `json:"npm,omitempty"`
	PyPI          *RegistryProfile `json:"pypi,omitempty"`
	TotalPackages int              `json:"total_packages"`
}

// RegistryProfile is the per-registry slice of a contributor's
// publisher footprint. Top is capped at the display limit; the full
// list is intentionally not exposed in the API to avoid leaking the
// entire alphabet for prolific publishers.
type RegistryProfile struct {
	PackageCount int       `json:"package_count"`
	Top          []Package `json:"top,omitempty"`
}

// Package is a single published package surfaced in a RegistryProfile.
// Role reflects the publisher's relationship to the package as
// reported by the registry (npm: "write" / "read"; PyPI: "owner" /
// "maintainer"). URL points to the registry's package page.
type Package struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	URL  string `json:"url,omitempty"`
}

// OSSFScorecard summarizes the OSSF Scorecard project assessment for a
// repository (Open Source Security Foundation, api.securityscorecards.dev).
// Repo-scoped: only populated when the score request includes a repo.
// Aggregate score is 0–10; individual checks are -1 (not applicable / not
// run) through 10. Higher is better. The set of checks returned by the
// upstream API may vary as new checks are added; we surface them all.
type OSSFScorecard struct {
	Score        float64     `json:"score"`
	Date         time.Time   `json:"date"`
	Commit       string      `json:"commit,omitempty"`
	ScorecardVer string      `json:"scorecard_version,omitempty"`
	Checks       []OSSFCheck `json:"checks,omitempty"`
}

// OSSFCheck is a single OSSF Scorecard check result. A score of -1 means
// the check did not apply to this repo (e.g., Fuzzing on a docs-only repo)
// — render distinctly from a literal 0 ("ran, scored zero").
type OSSFCheck struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Reason string `json:"reason,omitempty"`
	DocURL string `json:"doc_url,omitempty"`
}

// SecurityCredits aggregates a contributor's GitHub Security Advisory
// credits — published vulnerability advisories where the contributor is
// listed as reporter, fixer, analyst, or other credited role. A non-empty
// roster is a strong positive signal: GHSA credits are username-keyed by
// GitHub itself (T1 confidence), so this is one of the most authoritative
// pieces of contributor credibility we can surface.
type SecurityCredits struct {
	ReporterCount int              `json:"reporter_count"`
	FixerCount    int              `json:"fixer_count"`
	OtherCount    int              `json:"other_count"`
	BySeverity    map[string]int   `json:"by_severity,omitempty"`
	Recent        []SecurityCredit `json:"recent,omitempty"`
}

// SecurityCredit is a single advisory the contributor is credited on.
// The advisory_id is the GHSA identifier; cve_id is populated when the
// advisory has been assigned a CVE.
type SecurityCredit struct {
	AdvisoryID  string    `json:"advisory_id"`
	CreditType  string    `json:"credit_type"`
	Severity    string    `json:"severity"`
	CVEID       string    `json:"cve_id,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// OwnedRepos summarizes a contributor's own (non-forked) repositories
// from the GitHub user-repos listing. Aggregated and cached for 24h.
type OwnedRepos struct {
	TotalStars int64            `json:"total_stars"`
	TotalRepos int              `json:"total_repos"`
	Top        []OwnedRepo      `json:"top,omitempty"`
	Languages  []LanguageBucket `json:"languages,omitempty"`
}

// OwnedRepo represents a single repository in the top-by-stars list.
type OwnedRepo struct {
	Name        string `json:"name"`
	Stars       int    `json:"stars"`
	Language    string `json:"language,omitempty"`
	Description string `json:"description,omitempty"`
}

// LanguageBucket is a single primary language with its share among the
// contributor's non-fork repos. Share is in [0, 1] and the slice is
// sorted by share descending.
type LanguageBucket struct {
	Language string  `json:"language"`
	Repos    int     `json:"repos"`
	Share    float64 `json:"share"`
}

// Reciprocity describes the contributor's give-vs-take pattern: how much
// they review/comment relative to how much they ask for. Derived from
// LifetimeActivity; nil when the contributor has no PRs to anchor ratios.
type Reciprocity struct {
	// ReviewsPerPR captures whether the contributor gives back: high values
	// indicate maintainer-style behavior, near-zero suggests drive-by PRs.
	ReviewsPerPR float64 `json:"reviews_per_pr"`
	// IssueClosingRate is the share of issue actions that are closes:
	// IssuesClosed / (IssuesClosed + IssuesOpened). Bounded to [0, 1]:
	//   1.0 = pure giver — only ever closes issues, never opens
	//   0.5 = balanced — closes as often as opens
	//   0.0 = pure asker — opens issues, never closes any
	// The previous formulation (closed / opened) was unbounded and lost
	// the "pure giver" case (opened==0) entirely. Field name kept for
	// API stability; semantics are now bounded share.
	IssueClosingRate float64 `json:"issue_closing_rate"`
	// IssueCommentsPerPR captures whether they comment on others work or
	// primarily ship their own.
	IssueCommentsPerPR float64 `json:"issue_comments_per_pr"`
}

// LinkedAccount is a URL that the contributor declared in their public
// profile (bio or blog). v1 confidence is uniformly T4 ("declared link") —
// later phases promote individual links to T1-T3 via cross-platform
// verification (e.g. SSH key fingerprints, Keybase proofs).
type LinkedAccount struct {
	Platform string `json:"platform"` // e.g. "twitter", "mastodon", "personal_site"
	URL      string `json:"url"`
	Source   string `json:"source"` // "bio" or "blog"
	Tier     string `json:"tier"`   // T1-T5 confidence; v1 is always "T4"
}

// RepoContribution summarizes a contributor's footprint in a single repo.
// Activities is the count of distinct active hours referencing this repo;
// granular per-PR/per-review counts are not preserved by the GH Archive
// aggregation, so this is the most precise metric available from current data.
type RepoContribution struct {
	Repo             string    `json:"repo"`
	Activities       int       `json:"activities"`
	LastContribution time.Time `json:"last_contribution"`
}

// LifetimeActivity holds aggregate counts derived from the entire
// devtrace_contributor_activity history (no time filter).
type LifetimeActivity struct {
	PRsOpened     int        `json:"prs_opened"`
	PRsMerged     int        `json:"prs_merged"`
	PRsClosed     int        `json:"prs_closed"`
	ReviewsGiven  int        `json:"reviews_given"`
	IssueComments int        `json:"issue_comments"`
	IssuesOpened  int        `json:"issues_opened"`
	IssuesClosed  int        `json:"issues_closed"`
	ActiveDays    int        `json:"active_days"`
	FirstActive   *time.Time `json:"first_active,omitempty"`
	LastActive    *time.Time `json:"last_active,omitempty"`
}

// ComputeReciprocity returns the contributor's give-vs-take ratios derived
// from lifetime activity counts. Returns nil when there is no PR baseline
// to anchor the ratios against (PRsOpened == 0), since "0 reviews per 0
// PRs" is undefined and would be misleading.
//
// IssueClosingRate is a bounded share: closes / (closes + opens). This
// surfaces the pure-maintainer case (opens==0, closes>0 → 1.0) that the
// previous closes/opens ratio threw away when opens was zero.
func (la *LifetimeActivity) ComputeReciprocity() *Reciprocity {
	if la == nil || la.PRsOpened == 0 {
		return nil
	}
	r := &Reciprocity{
		ReviewsPerPR:       float64(la.ReviewsGiven) / float64(la.PRsOpened),
		IssueCommentsPerPR: float64(la.IssueComments) / float64(la.PRsOpened),
	}
	if total := la.IssuesClosed + la.IssuesOpened; total > 0 {
		r.IssueClosingRate = float64(la.IssuesClosed) / float64(total)
	}
	return r
}

// Profile holds public contributor metadata from GitHub.
type Profile struct {
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Company   string `json:"company,omitempty"`
	Location  string `json:"location,omitempty"`
	Bio       string `json:"bio,omitempty"`
}

// Score holds the computed reputation score and grade.
type Score struct {
	Grade      string             `json:"grade"`
	Value      float64            `json:"value"`
	Categories map[string]float64 `json:"categories,omitempty"`
}

// Signals holds global profile and activity signals (always meaningful without repo context).
type Signals struct {
	AccountAgeDays    int64 `json:"account_age_days"`
	Followers         int64 `json:"followers"`
	Following         int64 `json:"following"`
	PublicRepos       int64 `json:"public_repos"`
	ForkedRepos       int64 `json:"forked_repos"`
	PRsMerged         int64 `json:"prs_merged"`
	PRsClosed         int64 `json:"prs_closed"`
	RecentPRRepoCount int64 `json:"recent_pr_repo_count"`
	HasBio            bool  `json:"has_bio"`
	HasCompany        bool  `json:"has_company"`
	HasLocation       bool  `json:"has_location"`
	HasWebsite        bool  `json:"has_website"`
	HasPublicEmail    bool  `json:"has_public_email"`
	Suspended         bool  `json:"suspended"`
}

// RepoContext holds contributor-specific context within a repository.
// Only present when a repo is provided in the request.
type RepoContext struct {
	Repo              string `json:"repo"`
	Commits           int64  `json:"commits"`
	TotalCommits      int64  `json:"total_commits"`
	TotalContributors int    `json:"total_contributors"`
	LastCommitDays    *int64 `json:"last_commit_days"`
	OrgMember         bool   `json:"org_member"`
	CommitsVerified   bool   `json:"commits_verified"`
	AuthorAssociation string `json:"author_association,omitempty"`
	TrustedOrgMember  bool   `json:"trusted_org_member,omitempty"`
}

// License holds aggregated license information across repositories.
type License struct {
	TotalReposWithMergedPRs int            `json:"total_repos_with_merged_prs"`
	OwnRepos                int            `json:"own_repos"`
	Distribution            []LicenseEntry `json:"distribution"`
}

// LicenseEntry is a single license type with repo counts.
type LicenseEntry struct {
	License     string `json:"license"`
	Count       int    `json:"count"`
	Own         int    `json:"own"`
	Contributed int    `json:"contributed"`
}

// Behavior holds derived behavioral metrics from GH Archive contributor activity.
type Behavior struct {
	PRVelocity30d      int       `json:"pr_velocity_30d"`
	PRVelocityBaseline float64   `json:"pr_velocity_baseline"`
	ReviewsGiven30d    int       `json:"reviews_given_30d"`
	IssueComments30d   int       `json:"issue_comments_30d"`
	DistinctRepos90d   int       `json:"distinct_repos_90d"`
	ConsistencyScore   float64   `json:"consistency_score"`
	ActiveSince        time.Time `json:"active_since,omitempty"`
	// Cumulative counts from GH Archive (used by hybrid scoring to skip GitHub Search API).
	TotalPRsMerged            int     `json:"total_prs_merged"`
	TotalPRsClosed            int     `json:"total_prs_closed"`
	ActiveDays                int     `json:"active_days"`
	ActiveHourSpread          int     `json:"active_hour_spread"`
	BurstVanishPeakRatio      float64 `json:"-"`
	BurstVanishDaysSince      int     `json:"-"`
	BurstVanishDataSufficient bool    `json:"-"`
}

// AISensing holds signals about AI-assisted development activity.
type AISensing struct {
	CoAuthoredCommits    int      `json:"co_authored_commits"`
	BotAssociatedPRs     int      `json:"bot_associated_prs"`
	KnownToolSignatures  []string `json:"known_tool_signatures"`
	TotalCommitsAnalyzed int      `json:"total_commits_analyzed"`
	AIAssociatedRatio    float64  `json:"ai_associated_ratio"`
	// Claude-powered (Starter+, when available)
	PRAuthenticity *AuthenticityAssessment `json:"pr_authenticity,omitempty"`
	// Tier 2 behavioral heuristics (Pro only)
	Behavioral *BehavioralHeuristics `json:"behavioral,omitempty"`
}

// BehavioralHeuristics holds Tier 2 AI sensing signals computed from contributor activity.
type BehavioralHeuristics struct {
	VelocityAnomalyRatio float64  `json:"velocity_anomaly_ratio"`
	ActiveHourSpread     int      `json:"active_hour_spread"`
	BurstVanishScore     float64  `json:"burst_vanish_score"`
	SyntheticRiskFlags   int      `json:"synthetic_risk_flags"`
	SyntheticRiskDetails []string `json:"synthetic_risk_details"`
}

// AuthenticityAssessment is a Claude-powered classification of PR authenticity.
type AuthenticityAssessment struct {
	Classification string  `json:"classification"` // human, ai_assisted, ai_generated, uncertain
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning"`
}
