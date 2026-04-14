-- Hourly behavioral summaries from GH Archive.
-- Sharding-ready: BIGSERIAL id + composite PK for AlloyDB compatibility.
CREATE TABLE IF NOT EXISTS contributor_activity (
    id BIGSERIAL,
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    hour TIMESTAMPTZ NOT NULL,
    prs_opened INTEGER NOT NULL DEFAULT 0,
    prs_merged INTEGER NOT NULL DEFAULT 0,
    prs_closed INTEGER NOT NULL DEFAULT 0,
    reviews_given INTEGER NOT NULL DEFAULT 0,
    issue_comments INTEGER NOT NULL DEFAULT 0,
    distinct_repos INTEGER NOT NULL DEFAULT 0,
    repos JSONB,
    PRIMARY KEY (username, provider, hour)
);

CREATE INDEX IF NOT EXISTS idx_activity_hour ON contributor_activity(hour);

-- Priority-based scoring queue.
-- P1: new contributor in tenant repo
-- P2: new contributor in any repo
-- P3: stale contributor in tenant repo
CREATE TABLE IF NOT EXISTS scoring_queue (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    priority INTEGER NOT NULL DEFAULT 2,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

CREATE INDEX IF NOT EXISTS idx_scoring_queue_priority ON scoring_queue(priority, queued_at);
