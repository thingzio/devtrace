package net

import (
	"net/http"
	"time"
)

// GitHubClient is a shared HTTP client for GitHub API and OAuth calls.
// Does not follow redirects (OAuth token exchange needs raw response).
var GitHubClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// QuotaCheckClient is a lightweight HTTP client for GitHub rate limit checks.
var QuotaCheckClient = &http.Client{Timeout: 5 * time.Second}
