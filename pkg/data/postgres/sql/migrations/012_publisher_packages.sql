-- Migration 012: per-contributor publisher-package cache.
-- Caches the contributor's published-package roster on each supported
-- registry so the per-request enrichment doesn't hit registry APIs on
-- every score request. v1 populates npm only; pypi rows are reserved
-- for a future fetcher (the PyPI website is JS-protected and has no
-- public reverse-lookup API as of this migration).
--
-- Keyed by (provider, username, registry); fetched_at drives a per-row
-- freshness check at the application layer.

CREATE TABLE IF NOT EXISTS devtrace_publisher_packages (
    provider     TEXT NOT NULL DEFAULT 'github',
    username     TEXT NOT NULL,
    registry     TEXT NOT NULL,         -- 'npm', 'pypi' (future)
    package_count INTEGER NOT NULL DEFAULT 0,
    packages     JSONB,                  -- [{name, role, url}], capped to display limit
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, username, registry)
);

CREATE INDEX IF NOT EXISTS idx_devtrace_publisher_packages_fetched_at
    ON devtrace_publisher_packages(fetched_at);
