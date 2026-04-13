package github

import (
	"context"
	"time"

	"github.com/thingzio/devtrace/pkg/score"
)

// Client abstracts GitHub API access.
type Client interface {
	FetchUser(ctx context.Context, username string) (*UserProfile, error)
	FetchSignals(ctx context.Context, username, repo string) (*score.InputSignals, error)
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
	CreatedAt   time.Time
	Suspended   bool
	Followers   int64
	Following   int64
	PublicRepos int64
}
