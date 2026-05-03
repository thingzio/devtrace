package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ArchivePublishDelay is the expected lag between an hour ending and
// GH Archive publishing the corresponding file (~2 hours).
const ArchivePublishDelay = 2 * time.Hour

// Default values for env-overridable tunables. Operators may override
// any of these without a redeploy via the corresponding env var.
const (
	defaultRepoSummaryTTL = 24 * time.Hour
	defaultRepoListLimit  = 300
)

// RepoSummaryTTL returns the freshness window for cached owned-repos
// aggregates. Override via DEVTRACE_REPO_SUMMARY_TTL (Go duration
// format, e.g. "12h", "30m"). Beyond this age the next score request
// triggers a re-fetch from the GitHub /users/{u}/repos endpoint.
func RepoSummaryTTL() time.Duration {
	return GetEnvAsDuration("DEVTRACE_REPO_SUMMARY_TTL", defaultRepoSummaryTTL)
}

// RepoListLimit returns the cap on how many of a contributor's
// repositories the owned-repos aggregator pulls from GitHub on a single
// refresh. Override via DEVTRACE_REPO_LIST_LIMIT. Three pages of 100
// covers nearly all real users; high-volume accounts get the
// most-recently-pushed slice.
func RepoListLimit() int {
	return GetEnvAsInt("DEVTRACE_REPO_LIST_LIMIT", defaultRepoListLimit)
}

const (
	// defaultSecurityCreditTTL is the freshness window for cached GHSA
	// security-advisory credits. GHSA advisories don't churn rapidly;
	// a week is a reasonable default. Operators can tune via env var.
	defaultSecurityCreditTTL = 7 * 24 * time.Hour

	// defaultSecurityCreditLimit caps how many GHSA credits we pull
	// per fetch. Even prolific researchers like Tavis Ormandy have
	// hundreds, not thousands; 100 is generous.
	defaultSecurityCreditLimit = 100
)

// SecurityCreditTTL returns the freshness window for cached GHSA
// security-advisory credits. Override via DEVTRACE_SECURITY_CREDIT_TTL.
func SecurityCreditTTL() time.Duration {
	return GetEnvAsDuration("DEVTRACE_SECURITY_CREDIT_TTL", defaultSecurityCreditTTL)
}

// SecurityCreditLimit returns the cap on how many GHSA credits the
// security-advisory fetcher pulls from GitHub on a single refresh.
// Override via DEVTRACE_SECURITY_CREDIT_LIMIT.
func SecurityCreditLimit() int {
	return GetEnvAsInt("DEVTRACE_SECURITY_CREDIT_LIMIT", defaultSecurityCreditLimit)
}

// SecurityCreditsEnabled gates live GHSA-credit fetching. Defaults OFF
// because the v0.21 GraphQL query (User.securityAdvisoryCredits) hits
// a non-existent field and there is no cheap alternative API yet. Flip
// via DEVTRACE_SECURITY_CREDITS_ENABLED=true once a viable fetch path
// exists. The cache lookup and UI render still run when disabled, so
// pre-existing rows surface and re-enabling is a no-op for callers.
func SecurityCreditsEnabled() bool {
	return GetEnvBool("DEVTRACE_SECURITY_CREDITS_ENABLED")
}

const (
	// defaultOSSFTTL is the freshness window for cached OSSF Scorecard
	// rows. The OSSF refreshes scorecards roughly weekly, so a 7-day
	// TTL stays in step with upstream cadence.
	defaultOSSFTTL = 7 * 24 * time.Hour

	// defaultOSSFTimeout bounds a single OSSF API call. The endpoint
	// is usually fast (<1s) but large monorepos can occasionally take
	// longer; 10s is generous without holding score requests open.
	defaultOSSFTimeout = 10 * time.Second
)

// OSSFTTL returns the freshness window for cached OSSF Scorecard
// rows. Override via DEVTRACE_OSSF_TTL.
func OSSFTTL() time.Duration {
	return GetEnvAsDuration("DEVTRACE_OSSF_TTL", defaultOSSFTTL)
}

// OSSFTimeout returns the per-call timeout for the OSSF Scorecard
// HTTP client. Override via DEVTRACE_OSSF_TIMEOUT.
func OSSFTimeout() time.Duration {
	return GetEnvAsDuration("DEVTRACE_OSSF_TIMEOUT", defaultOSSFTimeout)
}

const (
	// defaultPublisherTTL is the freshness window for cached
	// publisher-package rows. Publishers don't churn rapidly; a week
	// keeps registry traffic well below any per-IP rate limits.
	defaultPublisherTTL = 7 * 24 * time.Hour

	// defaultPublisherTimeout bounds a single registry call.
	defaultPublisherTimeout = 10 * time.Second

	// defaultPublisherTopLimit caps how many packages we surface in
	// the API and UI top list. Prolific publishers (Sindre Sorhus
	// has 1200+ npm packages) would otherwise dominate the response
	// payload. Aggregate count is preserved regardless of cap.
	defaultPublisherTopLimit = 5
)

// PublisherTTL returns the freshness window for cached publisher
// profiles. Override via DEVTRACE_PUBLISHER_TTL.
func PublisherTTL() time.Duration {
	return GetEnvAsDuration("DEVTRACE_PUBLISHER_TTL", defaultPublisherTTL)
}

// PublisherTimeout returns the per-call timeout for the publisher
// HTTP clients. Override via DEVTRACE_PUBLISHER_TIMEOUT.
func PublisherTimeout() time.Duration {
	return GetEnvAsDuration("DEVTRACE_PUBLISHER_TIMEOUT", defaultPublisherTimeout)
}

// PublisherTopLimit returns the cap on packages surfaced in the top
// list. Override via DEVTRACE_PUBLISHER_TOP_LIMIT.
func PublisherTopLimit() int {
	return GetEnvAsInt("DEVTRACE_PUBLISHER_TOP_LIMIT", defaultPublisherTopLimit)
}

const (
	// defaultStackOverflowTTL is the freshness window for cached SO
	// profiles. Reputation updates daily but doesn't churn rapidly;
	// a week comfortably stays under SE's 300/day per-IP quota.
	defaultStackOverflowTTL = 7 * 24 * time.Hour

	// defaultStackOverflowTimeout bounds a single SE API call.
	defaultStackOverflowTimeout = 10 * time.Second
)

// StackOverflowTTL returns the freshness window for cached SO
// profiles. Override via DEVTRACE_SO_TTL.
func StackOverflowTTL() time.Duration {
	return GetEnvAsDuration("DEVTRACE_SO_TTL", defaultStackOverflowTTL)
}

// StackOverflowTimeout returns the per-call timeout for the SE
// Data API client. Override via DEVTRACE_SO_TIMEOUT.
func StackOverflowTimeout() time.Duration {
	return GetEnvAsDuration("DEVTRACE_SO_TIMEOUT", defaultStackOverflowTimeout)
}

// StackOverflowAPIKey returns the optional Stack Exchange API key.
// Empty string runs against the unauthenticated 300/day per-IP quota;
// providing a key (DEVTRACE_SO_API_KEY) lifts that to 10000/day.
func StackOverflowAPIKey() string {
	return GetEnv("DEVTRACE_SO_API_KEY", "")
}

// GetEnvAsDuration parses the env var as a Go duration; falls back to
// the default on absent or invalid values.
func GetEnvAsDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func GetEnvAsInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil || i < 1 {
		return fallback
	}
	return i
}

func GetEnvBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "true" || v == "1"
}

func GetEnvAsFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func DebugEnabled() bool {
	return GetEnvBool("DEVTRACE_DEBUG")
}
