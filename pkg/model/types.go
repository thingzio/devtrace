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
	Score       *Score       `json:"score"`
	Signals     *Signals     `json:"signals,omitempty"`
	RiskSummary string       `json:"risk_summary,omitempty"`
	RepoContext *RepoContext `json:"repo_context,omitempty"`
	License     *License     `json:"license,omitempty"`
	AISensing   *AISensing   `json:"ai_sensing,omitempty"`
	Behavior    *Behavior    `json:"behavior,omitempty"`
	ScoringMode string       `json:"scoring_mode"` // "global" or "repo"
	ScoredAt    time.Time    `json:"scored_at"`
	CachedAt    *time.Time   `json:"cached_at,omitempty"`
	Detail      string       `json:"detail,omitempty"`
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
	TotalPRsMerged int `json:"total_prs_merged"`
	TotalPRsClosed int `json:"total_prs_closed"`
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
}

// AuthenticityAssessment is a Claude-powered classification of PR authenticity.
type AuthenticityAssessment struct {
	Classification string  `json:"classification"` // human, ai_assisted, ai_generated, uncertain
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning"`
}
