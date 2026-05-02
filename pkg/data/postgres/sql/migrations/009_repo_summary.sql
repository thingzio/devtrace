-- Migration 009: per-contributor repository summary cache.
-- Caches an aggregate view of a contributor's owned repositories so the
-- detail-view enrichment doesn't need to re-fetch the full repo list from
-- the GitHub API on every request. TTL is enforced by the application
-- layer reading fetched_at; a stale row is overwritten in place.

CREATE TABLE IF NOT EXISTS devtrace_repo_summary (
    username      TEXT NOT NULL,
    provider      TEXT NOT NULL DEFAULT 'github',
    total_stars   BIGINT NOT NULL DEFAULT 0,
    total_repos   INTEGER NOT NULL DEFAULT 0,
    top_repos     JSONB,
    languages     JSONB,
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

-- Index supports a future refresh worker that scans rows due for
-- re-fetch (WHERE fetched_at < NOW() - INTERVAL '24 hours' ORDER BY
-- fetched_at). Cheap to add now; avoids a backfill later.
CREATE INDEX IF NOT EXISTS idx_devtrace_repo_summary_fetched_at
    ON devtrace_repo_summary(fetched_at);
