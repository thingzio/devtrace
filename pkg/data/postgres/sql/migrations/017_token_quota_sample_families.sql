-- Track Search and GraphQL rate-limit families alongside Core REST so the
-- admin "Pool Utilization" dashboard reflects what is actually being
-- exhausted. Existing rows are pre-cutover and stay at 0 — the chart
-- treats zero-limit samples as "no data" for that family.
ALTER TABLE devtrace_token_quota_sample
    ADD COLUMN IF NOT EXISTS search_limit INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS search_used INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS graphql_limit INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS graphql_used INTEGER NOT NULL DEFAULT 0;
