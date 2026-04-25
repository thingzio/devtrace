-- Add missing indexes for query performance.

-- reputation_history: scored_at standalone index for time-range queries
-- (HourlyScoringCounts, DailyScoringCounts, PipelineStats MAX, PruneScoreHistory).
CREATE INDEX IF NOT EXISTS idx_devtrace_rep_history_scored_at
    ON devtrace_reputation_history(scored_at);

-- session: tenant_id index for SearchTenants ORDER BY subquery,
-- GetLastSignIn, GetLastSignIns.
CREATE INDEX IF NOT EXISTS idx_devtrace_session_tenant
    ON devtrace_session(tenant_id);

-- tenant: username index for GetTenantByUsername lookups.
CREATE INDEX IF NOT EXISTS idx_devtrace_tenant_username
    ON devtrace_tenant(username);

-- Drop duplicate indexes (pre-squash names that duplicate devtrace_-prefixed versions).
DROP INDEX IF EXISTS idx_activity_hour;
DROP INDEX IF EXISTS idx_reputation_history_lookup;
DROP INDEX IF EXISTS idx_reputation_score;
DROP INDEX IF EXISTS idx_reputation_scored_at;
