-- Re-create session table if it was dropped by a shared-DB migration.
CREATE TABLE IF NOT EXISTS session (
    id TEXT PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
