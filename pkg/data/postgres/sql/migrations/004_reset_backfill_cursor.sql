-- Reset backfill cursor so skipped hours (due to 1MB scanner buffer)
-- are reprocessed with the new 8MB buffer.
DELETE FROM devtrace_sync_state WHERE key = 'gharchive_backfill_cursor';
