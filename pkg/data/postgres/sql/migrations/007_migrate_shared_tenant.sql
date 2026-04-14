-- Migrate tenant data from shared DevPulse tenant table to devtrace_tenant.
-- Only runs on existing deployments where the shared 'tenant' table exists.
-- No-ops on fresh installs (no shared tenant table, no data to migrate).

-- Ensure devtrace_tenant exists (skipped on fresh installs where 001 created it,
-- needed on existing deployments where 001 was the old schema).
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

DO $$
BEGIN
    -- Only migrate if the shared tenant table exists (existing deployment).
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'tenant' AND table_schema = 'public') THEN

        -- Copy tenants referenced by DevTrace tables into devtrace_tenant.
        INSERT INTO devtrace_tenant (id, github_id, username, email, avatar_url, name, company, location, bio, plan, max_contributors, tos_accepted_at, created_at, updated_at)
        SELECT id, github_id, username, email, avatar_url,
               COALESCE(name,''), COALESCE(company,''), COALESCE(location,''),
               COALESCE(bio,''), 'free', 50, tos_accepted_at, created_at, updated_at
        FROM tenant
        WHERE id IN (
            SELECT DISTINCT tenant_id FROM session
            UNION SELECT DISTINCT tenant_id FROM api_token
            UNION SELECT DISTINCT tenant_id FROM github_app_installation
            UNION SELECT DISTINCT tenant_id FROM usage_record
        )
        ON CONFLICT (id) DO NOTHING;

        -- Re-point FK constraints from shared tenant to devtrace_tenant.
        ALTER TABLE api_token DROP CONSTRAINT IF EXISTS api_token_tenant_id_fkey;
        ALTER TABLE api_token ADD CONSTRAINT api_token_tenant_id_fkey
            FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

        ALTER TABLE session DROP CONSTRAINT IF EXISTS session_tenant_id_fkey;
        ALTER TABLE session ADD CONSTRAINT session_tenant_id_fkey
            FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

        ALTER TABLE github_app_installation DROP CONSTRAINT IF EXISTS github_app_installation_tenant_id_fkey;
        ALTER TABLE github_app_installation ADD CONSTRAINT github_app_installation_tenant_id_fkey
            FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

        ALTER TABLE usage_record DROP CONSTRAINT IF EXISTS usage_record_tenant_id_fkey;
        ALTER TABLE usage_record ADD CONSTRAINT usage_record_tenant_id_fkey
            FOREIGN KEY (tenant_id) REFERENCES devtrace_tenant(id) ON DELETE CASCADE;

    END IF;
END $$;
