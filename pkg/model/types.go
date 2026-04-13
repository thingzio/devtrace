package model

import "time"

// Provider identifies the source platform for contributor data.
type Provider string

// ProviderGitHub is the GitHub provider.
const ProviderGitHub Provider = "github"

// ScoreResponse is the top-level API response for a contributor score.
type ScoreResponse struct {
	Username    string       `json:"username"`
	Provider    Provider     `json:"provider"`
	Score       *Score       `json:"score"`
	Signals     *Signals     `json:"signals,omitempty"`
	RiskSummary string       `json:"risk_summary,omitempty"`
	RepoContext *RepoContext `json:"repo_context,omitempty"`
	License     *License     `json:"license,omitempty"`
	AISensing   *AISensing   `json:"ai_sensing,omitempty"`
	ScoredAt    time.Time    `json:"scored_at"`
	CachedAt    *time.Time   `json:"cached_at,omitempty"`
	Detail      string       `json:"detail,omitempty"`
}

// Score holds the computed reputation score and grade.
type Score struct {
	Grade        string             `json:"grade"`
	Value        float64            `json:"value"`
	ModelVersion string             `json:"model_version"`
	Categories   map[string]float64 `json:"categories,omitempty"`
}

// Signals holds the raw profile and activity signals used for scoring.
type Signals struct {
	AccountAgeDays    int64  `json:"account_age_days"`
	Followers         int64  `json:"followers"`
	Following         int64  `json:"following"`
	PublicRepos       int64  `json:"public_repos"`
	ForkedRepos       int64  `json:"forked_repos"`
	PRsMerged         int64  `json:"prs_merged"`
	PRsClosed         int64  `json:"prs_closed"`
	RecentPRRepoCount int64  `json:"recent_pr_repo_count"`
	HasBio            bool   `json:"has_bio"`
	HasCompany        bool   `json:"has_company"`
	HasLocation       bool   `json:"has_location"`
	HasWebsite        bool   `json:"has_website"`
	OrgMember         bool   `json:"org_member"`
	Suspended         bool   `json:"suspended"`
	AuthorAssociation string `json:"author_association"`
	CommitsVerified   bool   `json:"commits_verified"`
}

// RepoContext holds contributor-specific context within a repository.
type RepoContext struct {
	Repo              string `json:"repo"`
	Commits           int64  `json:"commits"`
	TotalCommits      int64  `json:"total_commits"`
	TotalContributors int    `json:"total_contributors"`
	LastCommitDays    *int64 `json:"last_commit_days"`
	OrgMember         bool   `json:"org_member"`
	AuthorAssociation string `json:"author_association"`
	TrustedOrgMember  bool   `json:"trusted_org_member"`
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

// AISensing holds signals about AI-assisted development activity.
type AISensing struct {
	CoAuthoredCommits    int      `json:"co_authored_commits"`
	BotAssociatedPRs     int      `json:"bot_associated_prs"`
	KnownToolSignatures  []string `json:"known_tool_signatures"`
	TotalCommitsAnalyzed int      `json:"total_commits_analyzed"`
	AIAssociatedRatio    float64  `json:"ai_associated_ratio"`
}
