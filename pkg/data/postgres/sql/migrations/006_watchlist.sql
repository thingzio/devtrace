-- Watchlist and notification events for org/repo monitoring.

-- Add issue tracking columns to contributor activity.
ALTER TABLE devtrace_contributor_activity ADD COLUMN IF NOT EXISTS issues_opened INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devtrace_contributor_activity ADD COLUMN IF NOT EXISTS issues_closed INTEGER NOT NULL DEFAULT 0;

-- Watchlist: tenant-defined orgs/repos to monitor.
CREATE TABLE IF NOT EXISTS devtrace_watchlist (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES devtrace_tenant(id) ON DELETE CASCADE,
    target TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'manual',
    notify_email BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, target)
);

CREATE INDEX IF NOT EXISTS idx_devtrace_watchlist_tenant ON devtrace_watchlist(tenant_id);
CREATE INDEX IF NOT EXISTS idx_devtrace_watchlist_target ON devtrace_watchlist(target);

-- Notification events detected by ingest and scorer pipelines.
CREATE TABLE IF NOT EXISTS devtrace_notification_event (
    id BIGSERIAL PRIMARY KEY,
    watchlist_id UUID NOT NULL REFERENCES devtrace_watchlist(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    username TEXT NOT NULL,
    details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_devtrace_notif_watchlist ON devtrace_notification_event(watchlist_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_devtrace_notif_unsent ON devtrace_notification_event(sent_at) WHERE sent_at IS NULL;

-- Backfill: create implicit watchlist entries for existing active installations.
INSERT INTO devtrace_watchlist (tenant_id, target, source)
SELECT DISTINCT ai.tenant_id, ai.target_login, 'implicit'
FROM devtrace_app_installation ai
WHERE ai.suspended_at IS NULL AND ai.target_login IS NOT NULL
ON CONFLICT (tenant_id, target) DO NOTHING;
