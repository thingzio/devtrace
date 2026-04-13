-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Contributor profiles (provider-agnostic identity)
CREATE TABLE contributor (
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

-- Reputation scores
CREATE TABLE reputation (
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

CREATE INDEX idx_reputation_score ON reputation(score);
CREATE INDEX idx_reputation_scored_at ON reputation(scored_at);

-- Reputation history (for trend charts)
CREATE TABLE reputation_history (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    score REAL NOT NULL,
    grade TEXT NOT NULL,
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

CREATE INDEX idx_reputation_history_lookup ON reputation_history(username, provider, scored_at);

-- License profiles
CREATE TABLE license_profile (
    username TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    total_repos_with_merged_prs INTEGER NOT NULL DEFAULT 0,
    own_repos INTEGER NOT NULL DEFAULT 0,
    distribution JSONB,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (username, provider),
    FOREIGN KEY (username, provider) REFERENCES contributor(username, provider) ON DELETE CASCADE
);

-- AI sensing signals
CREATE TABLE ai_signal (
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

-- Tenants (registered users)
CREATE TABLE tenant (
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

-- API tokens (DevTrace-minted)
CREATE TABLE api_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_token_hash ON api_token(token_hash);
CREATE INDEX idx_api_token_tenant ON api_token(tenant_id);

-- Sessions (UI auth)
CREATE TABLE session (
    id TEXT PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- GitHub App installations
CREATE TABLE github_app_installation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    installation_id BIGINT UNIQUE NOT NULL,
    target_type TEXT,
    target_login TEXT,
    permissions JSONB,
    suspended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Usage tracking (quota enforcement)
CREATE TABLE usage_record (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    username_scored TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    deep BOOLEAN NOT NULL DEFAULT FALSE,
    scored_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_tenant_period ON usage_record(tenant_id, scored_at);

-- Rate limit tracking (IP-based for unauth)
CREATE TABLE rate_limit (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    window_start TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
