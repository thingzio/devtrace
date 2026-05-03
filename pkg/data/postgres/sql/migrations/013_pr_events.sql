-- Migration 013: per-PR merge graph.
-- Records (provider, repo, pr_number) → (author, opened_at,
-- merged_at, closed_at) so that "PRs authored by X that got merged"
-- can be computed by joining the opened event (actor = author) with
-- the merged event (actor often a CI bot in modern OSS workflows).
-- Without this, per-actor aggregation alone produces structural zero
-- for human authors whose PRs are merged by automation.
--
-- Each archive hour observation upserts only the columns relevant
-- to that event's action; COALESCE preserves earlier values so a
-- merge event months after the open event correctly stamps merged_at
-- without clobbering the original author/opened_at.

CREATE TABLE IF NOT EXISTS devtrace_pr_events (
    provider      TEXT NOT NULL DEFAULT 'github',
    repo          TEXT NOT NULL,
    pr_number     INTEGER NOT NULL,
    author        TEXT,
    opened_at     TIMESTAMPTZ,
    merged_at     TIMESTAMPTZ,
    closed_at     TIMESTAMPTZ,
    last_event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, repo, pr_number)
);

-- Per-author "merged PRs" lookups scan WHERE author = $1 AND
-- merged_at IS NOT NULL. The PK covers (provider, repo, pr_number)
-- but does not help author lookups; add a partial index that only
-- covers merged rows so it stays small. Most rows in this table will
-- never have merged_at set (closed-without-merge, still-open).
CREATE INDEX IF NOT EXISTS idx_devtrace_pr_events_author_merged
    ON devtrace_pr_events(author)
    WHERE author IS NOT NULL AND merged_at IS NOT NULL;

-- Pruning scans for old rows by last_event_at.
CREATE INDEX IF NOT EXISTS idx_devtrace_pr_events_last_event
    ON devtrace_pr_events(last_event_at);
