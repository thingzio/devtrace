-- Drop duplicate indexes left over from pre-squash migrations and from
-- redundant non-unique indexes that overlap the unique constraint indexes.
-- Validated against prod backup with EXPLAIN ANALYZE: all queries continue
-- to use the surviving devtrace_-prefixed (or _key/_pkey) indexes.

-- devtrace_api_token: token_hash already has UNIQUE constraint
-- (api_token_token_hash_key). The two non-unique idx_*_hash indexes are pure
-- duplicates of that unique index.
DROP INDEX IF EXISTS idx_api_token_hash;
DROP INDEX IF EXISTS idx_devtrace_api_token_hash;

-- devtrace_api_token: idx_api_token_tenant duplicates idx_devtrace_api_token_tenant.
DROP INDEX IF EXISTS idx_api_token_tenant;

-- devtrace_scoring_queue: idx_scoring_queue_priority duplicates idx_devtrace_scoring_queue_priority.
DROP INDEX IF EXISTS idx_scoring_queue_priority;

-- devtrace_usage_record: idx_usage_tenant_period duplicates idx_devtrace_usage_tenant_period.
DROP INDEX IF EXISTS idx_usage_tenant_period;
