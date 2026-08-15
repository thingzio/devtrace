-- devtrace_app_installation.tenant_id is a foreign key with no child-side
-- index. Postgres indexes only the referenced (parent) side of a FK, so every
-- tenant delete or key update seq-scanned this table to enforce the constraint
-- while holding a lock on the tenant row. Single column is enough: the FK check
-- is `WHERE tenant_id = $1`, and installations per tenant number in the ones.
CREATE INDEX IF NOT EXISTS idx_devtrace_app_installation_tenant
    ON devtrace_app_installation (tenant_id);
