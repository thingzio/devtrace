-- Create DevTrace's own tenant table, independent from DevPulse.
-- Copies structure from the shared tenant table but is DevTrace-scoped.

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

-- Recreate dependent tables with FK to devtrace_tenant.
-- Drop old FKs first (they reference the shared tenant table).

-- api_token
ALTER TABLE api_token DROP CONSTRAINT IF EXISTS api_token_tenant_id_fkey;
ALTER TABLE api_token ADD CONSTRAINT api_token_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

-- session
ALTER TABLE session DROP CONSTRAINT IF EXISTS session_tenant_id_fkey;
ALTER TABLE session ADD CONSTRAINT session_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

-- github_app_installation
ALTER TABLE github_app_installation DROP CONSTRAINT IF EXISTS github_app_installation_tenant_id_fkey;
ALTER TABLE github_app_installation ADD CONSTRAINT github_app_installation_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

-- usage_record
ALTER TABLE usage_record DROP CONSTRAINT IF EXISTS usage_record_tenant_id_fkey;
ALTER TABLE usage_record ADD CONSTRAINT usage_record_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;
