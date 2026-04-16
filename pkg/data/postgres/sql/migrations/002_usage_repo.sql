-- Add repo column to usage_record for tracking repo-scoped scoring.
ALTER TABLE devtrace_usage_record ADD COLUMN IF NOT EXISTS repo TEXT NOT NULL DEFAULT '';
