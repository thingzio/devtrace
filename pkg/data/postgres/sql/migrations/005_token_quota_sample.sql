-- Token quota sampling for utilization tracking.
CREATE TABLE IF NOT EXISTS devtrace_token_quota_sample (
    id BIGSERIAL PRIMARY KEY,
    sampled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    installation_id BIGINT NOT NULL,
    login TEXT NOT NULL,
    quota_limit INTEGER NOT NULL,
    quota_used INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_devtrace_quota_sample_time
    ON devtrace_token_quota_sample(sampled_at);
