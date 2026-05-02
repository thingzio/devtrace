-- Migration 011: per-repository OSSF Scorecard cache.
-- Caches the aggregate Scorecard score and individual check results for
-- a repo so the per-request enrichment doesn't hit the OSSF API on every
-- score lookup. Keyed by (provider, owner, repo); fetched_at drives a
-- per-row freshness check at the application layer. The OSSF API
-- refreshes scorecards approximately weekly, so a 7-day TTL aligns with
-- upstream cadence.

CREATE TABLE IF NOT EXISTS devtrace_ossf_scorecard (
    provider          TEXT NOT NULL DEFAULT 'github',
    repo_owner        TEXT NOT NULL,
    repo_name         TEXT NOT NULL,
    score             NUMERIC(3,1) NOT NULL DEFAULT 0,
    scorecard_date    TIMESTAMPTZ,
    commit_sha        TEXT,
    scorecard_version TEXT,
    checks            JSONB,
    fetched_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, repo_owner, repo_name)
);

-- Refresh worker scans for stale rows; covered by a fetched_at index.
CREATE INDEX IF NOT EXISTS idx_devtrace_ossf_scorecard_fetched_at
    ON devtrace_ossf_scorecard(fetched_at);
