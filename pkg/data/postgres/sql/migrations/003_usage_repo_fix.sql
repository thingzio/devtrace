-- Re-apply repo column (002 may have been recorded without taking effect).
ALTER TABLE devtrace_usage_record ADD COLUMN IF NOT EXISTS repo TEXT NOT NULL DEFAULT '';
