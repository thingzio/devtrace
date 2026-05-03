-- Migration 015: per-contributor cross-VCS T1 fingerprint matches.
-- Caches the result of comparing a contributor's GitHub SSH keys
-- against their public keys on other forges (GitLab, Codeberg,
-- Sourcehut in v1). A non-empty matches array means at least one
-- SSH key fingerprint is shared with another forge — the T1
-- "same private key" cryptographic anchor.
--
-- Sentinel rows (matches='[]') record a fetch attempt for users
-- with no cross-VCS presence so the service-layer TTL check
-- suppresses repeated re-checks against forges they've never used.

CREATE TABLE IF NOT EXISTS devtrace_cross_vcs (
    provider      TEXT NOT NULL DEFAULT 'github',
    username      TEXT NOT NULL,
    total_matched INTEGER NOT NULL DEFAULT 0,
    matches       JSONB,
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, username)
);

CREATE INDEX IF NOT EXISTS idx_devtrace_cross_vcs_fetched_at
    ON devtrace_cross_vcs(fetched_at);
