-- DevTrace schema (squashed). All tables use devtrace_ prefix.
-- All statements use IF NOT EXISTS for idempotent re-application.

-- Drop old schema version tracking if present (from pre-squash migrations).
DROP TABLE IF EXISTS devtrace_schema_version CASCADE;

-- Schema version tracking.
CREATE TABLE IF NOT EXISTS devtrace_schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Tenants.
CREATE TABLE IF NOT EXISTS devtrace_tenant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id BIGINT UNIQUE NOT NULL,
    username TEXT NOT NULL,
    email TEXT,
    avatar_url TEXT,
    name TEXT,
    company TEXT,
    location TEXT,
    bio TEXT,
    plan TEXT NOT NULL DEFAULT 'free',
    max_contributors INTEGER NOT NULL DEFAULT 50,
    tos_accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Contributor profiles (provider-agnostic identity).
CREATE TABLE IF NOT EXISTS devtrace_contributor (
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

-- Reputation scores.
CREATE TABLE IF NOT EXISTS devtrace_reputation (
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
    FOREIGN KEY (username, provider) REFERENCES devtrace_contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_devtrace_reputation_score ON devtrace_reputation(score);
CREATE INDEX IF NOT EXISTS idx_devtrace_reputation_scored_at ON devtrace_reputation(scored_at);

-- Reputation history (trend charts).
CREATE TABLE IF NOT EXISTS devtrace_reputation_history (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    score REAL NOT NULL,
    grade TEXT NOT NULL,
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (username, provider) REFERENCES devtrace_contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_devtrace_rep_history_lookup ON devtrace_reputation_history(username, provider, scored_at);

-- License profiles.
CREATE TABLE IF NOT EXISTS devtrace_license_profile (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    total_repos_with_merged_prs INTEGER NOT NULL DEFAULT 0,
    own_repos INTEGER NOT NULL DEFAULT 0,
    distribution JSONB,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES devtrace_contributor(username, provider) ON DELETE CASCADE
);

-- AI sensing signals.
CREATE TABLE IF NOT EXISTS devtrace_ai_signal (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    co_authored_commits INTEGER NOT NULL DEFAULT 0,
    bot_associated_prs INTEGER NOT NULL DEFAULT 0,
    known_tool_signatures JSONB,
    total_commits_analyzed INTEGER NOT NULL DEFAULT 0,
    ai_associated_ratio REAL NOT NULL DEFAULT 0,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES devtrace_contributor(username, provider) ON DELETE CASCADE
);

-- API tokens.
CREATE TABLE IF NOT EXISTS devtrace_api_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_devtrace_api_token_hash ON devtrace_api_token(token_hash);
CREATE INDEX IF NOT EXISTS idx_devtrace_api_token_tenant ON devtrace_api_token(tenant_id);

-- Sessions (UI auth).
CREATE TABLE IF NOT EXISTS devtrace_session (
    id TEXT PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- GitHub App installations.
CREATE TABLE IF NOT EXISTS devtrace_app_installation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    installation_id BIGINT UNIQUE NOT NULL,
    app_id BIGINT,
    target_type TEXT,
    target_login TEXT,
    permissions JSONB,
    suspended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_devtrace_install_app_id ON devtrace_app_installation(app_id) WHERE app_id IS NOT NULL;

-- Usage tracking (quota enforcement).
CREATE TABLE IF NOT EXISTS devtrace_usage_record (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    username_scored TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    source TEXT NOT NULL DEFAULT 'api',
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_devtrace_usage_tenant_period ON devtrace_usage_record(tenant_id, scored_at);

-- Rate limit tracking (IP-based for unauth).
CREATE TABLE IF NOT EXISTS devtrace_rate_limit (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    window_start TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Sync state tracking (key-value for background sync cursors).
CREATE TABLE IF NOT EXISTS devtrace_sync_state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Hourly behavioral summaries from GH Archive.
CREATE TABLE IF NOT EXISTS devtrace_contributor_activity (
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

CREATE INDEX IF NOT EXISTS idx_devtrace_activity_hour ON devtrace_contributor_activity(hour);

-- Priority-based scoring queue.
CREATE TABLE IF NOT EXISTS devtrace_scoring_queue (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    priority INTEGER NOT NULL DEFAULT 2,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider)
);

CREATE INDEX IF NOT EXISTS idx_devtrace_scoring_queue_priority ON devtrace_scoring_queue(priority, queued_at);
