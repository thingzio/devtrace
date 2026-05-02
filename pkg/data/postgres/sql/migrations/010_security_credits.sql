-- Migration 010: per-contributor security advisory credits.
-- Caches the contributor's GHSA credits (reporter / fixer / analyst /
-- remediation_developer roles on published GitHub Security Advisories).
-- Driven by lazy fetch at score time with a per-row freshness check.

CREATE TABLE IF NOT EXISTS devtrace_security_credit (
    username      TEXT NOT NULL,
    provider      TEXT NOT NULL DEFAULT 'github',
    advisory_id   TEXT NOT NULL,
    credit_type   TEXT NOT NULL,
    severity      TEXT NOT NULL DEFAULT 'unknown',
    cve_id        TEXT,
    summary       TEXT,
    published_at  TIMESTAMPTZ,
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider, advisory_id, credit_type)
);

-- Refresh worker scans for stale rows by (username, provider, max(fetched_at)).
-- A composite index on this prefix supports both per-user fetch lookups and
-- bulk staleness scans without requiring a separate per-row fetched_at index.
CREATE INDEX IF NOT EXISTS idx_devtrace_security_credit_user_fetched
    ON devtrace_security_credit(username, provider, fetched_at);

-- Severity-aggregate queries scan all credits for a user; covered by the PK
-- prefix (username, provider). No additional index needed.
