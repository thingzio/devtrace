-- Align shared tables when DevPulse created them first.
-- Each ALTER uses IF NOT EXISTS so this migration is safe to re-apply.

-- tenant: DevTrace needs max_contributors (already in 005, but kept here for completeness).
-- DevPulse columns (max_repos, max_events_per_week, upgrade_requested_at) are
-- ignored by DevTrace — they exist in the physical table but DevTrace doesn't query them.
ALTER TABLE devtrace_tenant ADD COLUMN IF NOT EXISTS max_contributors INTEGER NOT NULL DEFAULT 50;

-- github_app_installation: app_id was added in 004, ensure it exists.
ALTER TABLE github_app_installation ADD COLUMN IF NOT EXISTS app_id BIGINT;

-- DevTrace-only tables that may not exist if DevPulse created the initial schema.
-- These use IF NOT EXISTS so they are safe to re-run.
CREATE TABLE IF NOT EXISTS contributor (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    display_name TEXT,
    email TEXT,
    avatar_url TEXT,
    company TEXT,
    location TEXT,
    bio TEXT,
    account_created_at TIMESTAMPTZ,
    suspended BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

CREATE TABLE IF NOT EXISTS reputation (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    score REAL NOT NULL,
    grade TEXT NOT NULL,
    model_version TEXT NOT NULL,
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    categories JSONB,
    signals JSONB,
    risk_summary TEXT,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_reputation_score ON reputation(score);
CREATE INDEX IF NOT EXISTS idx_reputation_scored_at ON reputation(scored_at);

CREATE TABLE IF NOT EXISTS reputation_history (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    score REAL NOT NULL,
    grade TEXT NOT NULL,
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_reputation_history_lookup ON reputation_history(username, provider, scored_at);

CREATE TABLE IF NOT EXISTS license_profile (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    total_repos_with_merged_prs INTEGER NOT NULL DEFAULT 0,
    own_repos INTEGER NOT NULL DEFAULT 0,
    distribution JSONB,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ai_signal (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    co_authored_commits INTEGER NOT NULL DEFAULT 0,
    bot_associated_prs INTEGER NOT NULL DEFAULT 0,
    known_tool_signatures JSONB,
    total_commits_analyzed INTEGER NOT NULL DEFAULT 0,
    ai_associated_ratio REAL NOT NULL DEFAULT 0,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS api_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_token_hash ON api_token(token_hash);
CREATE INDEX IF NOT EXISTS idx_api_token_tenant ON api_token(tenant_id);

CREATE TABLE IF NOT EXISTS usage_record (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    username_scored TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usage_tenant_period ON usage_record(tenant_id, scored_at);

CREATE TABLE IF NOT EXISTS rate_limit (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    window_start TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS scoring_queue (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    priority INTEGER NOT NULL DEFAULT 2,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

CREATE INDEX IF NOT EXISTS idx_scoring_queue_priority ON scoring_queue(priority, queued_at);

CREATE TABLE IF NOT EXISTS sync_state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
