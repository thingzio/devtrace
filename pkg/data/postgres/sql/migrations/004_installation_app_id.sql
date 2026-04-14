-- Add app_id to github_app_installation so each service (DevTrace, DevPulse)
-- only queries its own installations from the shared table.
ALTER TABLE github_app_installation ADD COLUMN IF NOT EXISTS app_id BIGINT;
CREATE INDEX IF NOT EXISTS idx_installation_app_id ON github_app_installation(app_id) WHERE app_id IS NOT NULL;
