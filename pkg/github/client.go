package github

import (
	"context"
	"time"

	"github.com/thingzio/devtrace/pkg/score"
)

// ArchiveHints provides pre-computed signals from GH Archive data,
// allowing fetchSignals to skip redundant GitHub Search API calls.
// When nil, all signals are fetched from the GitHub API.
// When Trusted is true, hints replace API calls. Otherwise the Search API
// is attempted first and hints serve as a fallback when API calls fail.
type ArchiveHints struct {
	PRsMerged         int64
	PRsClosed         int64
	RecentPRRepoCount int64
	Trusted           bool
}

// Client abstracts GitHub API access.
type Client interface {
	FetchUser(ctx context.Context, username string) (*UserProfile, error)
	FetchSignals(ctx context.Context, username, repo string, hints *ArchiveHints) (*score.InputSignals, error)
	IsOrgMember(ctx context.Context, org, username string) (bool, error)
	// ListUserRepos returns up to maxRepos owned repositories. Caller is
	// responsible for any forks/archived filtering. The order is determined
	// by the GitHub API (currently "pushed" descending). maxRepos is
	// clamped to a sane upper bound by the implementation.
	ListUserRepos(ctx context.Context, username string, maxRepos int) ([]Repo, error)
}

// Repo holds a subset of GitHub repository metadata used by enrichment
// aggregation. Only fields actually consumed by the aggregator are
// included; extending this struct is cheap.
type Repo struct {
	Name        string
	FullName    string
	Description string
	Language    string
	Stars       int
	Fork        bool
	Archived    bool
	PushedAt    time.Time
}

// UserProfile holds GitHub user metadata.
type UserProfile struct {
	Username    string
	Name        string
	Email       string
	AvatarURL   string
	Company     string
	Location    string
	Bio         string
	Website     string
	CreatedAt   time.Time
	Suspended   bool
	Followers   int64
	Following   int64
	PublicRepos int64
}
