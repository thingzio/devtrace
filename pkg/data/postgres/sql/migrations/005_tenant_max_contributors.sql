-- Add max_contributors column to tenant table.
-- The column exists in DevTrace's CREATE TABLE definition (migration 001) but
-- may be missing when the tenant table was originally created by DevPulse.
ALTER TABLE devtrace_tenant ADD COLUMN IF NOT EXISTS max_contributors INTEGER NOT NULL DEFAULT 50;
