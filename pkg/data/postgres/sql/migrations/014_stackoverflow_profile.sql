-- Migration 014: per-contributor Stack Overflow profile cache.
-- Caches the Stack Exchange API result for a contributor's SO account
-- (discovered via a stackoverflow.com link declared on their GitHub
-- profile). Keyed by (provider, username); fetched_at drives the
-- per-row freshness check. SE has a 300/day per-IP quota for
-- unauthenticated requests; weekly cache stays well under it.
--
-- A non-nil zero-rep row records a "we looked, found nothing"
-- sentinel so re-fetching is suppressed for users without a SO link.

CREATE TABLE IF NOT EXISTS devtrace_stackoverflow_profile (
    provider       TEXT NOT NULL DEFAULT 'github',
    username       TEXT NOT NULL,
    so_user_id     BIGINT NOT NULL DEFAULT 0,
    display_name   TEXT,
    reputation     BIGINT NOT NULL DEFAULT 0,
    badge_bronze   INTEGER NOT NULL DEFAULT 0,
    badge_silver   INTEGER NOT NULL DEFAULT 0,
    badge_gold     INTEGER NOT NULL DEFAULT 0,
    url            TEXT,
    so_created_at  TIMESTAMPTZ,
    last_access_at TIMESTAMPTZ,
    fetched_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, username)
);

CREATE INDEX IF NOT EXISTS idx_devtrace_stackoverflow_fetched_at
    ON devtrace_stackoverflow_profile(fetched_at);
