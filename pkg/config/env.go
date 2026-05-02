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
