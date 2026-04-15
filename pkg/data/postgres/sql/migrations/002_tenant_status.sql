-- Add status column to devtrace_tenant for account suspension.
ALTER TABLE devtrace_tenant ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
