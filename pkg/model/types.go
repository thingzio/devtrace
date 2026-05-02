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
	ScoredAt    time.Time    `json:"scored_at"`
	CachedAt    *time.Time   `json:"cached_at,omitempty"`
	Detail      string       `json:"detail,omitempty"`
}

// Enrichment holds optional profile-decoration data populated when the
// caller requests a detail view (?detail=true). All sub-blocks are
// independently optional and may be absent without affecting the score.
type Enrichment struct {
	LifetimeActivity    *LifetimeActivity  `json:"lifetime_activity,omitempty"`
	TopContributedRepos []RepoContribution `json:"top_contributed_repos,omitempty"`
	LinkedAccounts      []LinkedAccount    `json:"linked_accounts,omitempty"`
	Emails              []string           `json:"emails,omitempty"`
}

// LinkedAccount is a URL that the contributor declared in their public
// profile (bio or blog). v1 confidence is uniformly T4 ("declared link") —
// later phases promote individual links to T1-T3 via cross-platform
// verification (e.g. SSH key fingerprints, Keybase proofs).
type LinkedAccount struct {
	Platform string `json:"platform"`         // e.g. "twitter", "mastodon", "personal_site"
	URL      string `json:"url"`
	Source   string `json:"source"`           // "bio" or "blog"
	Tier     string `json:"tier"`             // T1-T5 confidence; v1 is always "T4"
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
