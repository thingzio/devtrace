# DB Retention & Behavioral Window Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Bound DB growth by reducing behavioral window to 90 days, adding 120-day activity retention, 400-day history retention, and resetting backfill cursor.

**Architecture:** Three pruning methods added to existing Store, wired into the daily maintenance cycle in the ingest runner. Behavioral query window narrowed in GetBehavioralSignals. Backfill cursor reset via migration.

**Tech Stack:** Go, PostgreSQL, existing ingest runner pattern

---

### Task 1: Narrow Behavioral Query Window to 90 Days

**Files:**
- Modify: `pkg/data/postgres/activity.go:143-214`

**Step 1: Update GetBehavioralSignals SQL**

In `pkg/data/postgres/activity.go`, change the main query (line 157):
```
WHERE username = $1 AND provider = $2 AND hour > NOW() - INTERVAL '180 days'
```
to:
```
WHERE username = $1 AND provider = $2 AND hour > NOW() - INTERVAL '90 days'
```

Change the burst-vanish query (line 202):
```
AND hour > NOW() - INTERVAL '180 days'
```
to:
```
AND hour > NOW() - INTERVAL '90 days'
```

Change the consistency calculation (line 184):
```go
totalWeeks := 180.0 / 7.0
```
to:
```go
totalWeeks := 90.0 / 7.0
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS (no test directly asserts 180-day window values)

**Step 3: Commit**

```bash
git add pkg/data/postgres/activity.go
git commit -S -m "Narrow behavioral signals window from 180 to 90 days"
```

---

### Task 2: Add PruneActivity Method

**Files:**
- Modify: `pkg/data/postgres/activity.go` (add method after CompactActivity)
- Modify: `pkg/data/postgres/activity_test.go` (add test)

**Step 1: Write failing test**

Add to `pkg/data/postgres/activity_test.go`:

```go
func TestPruneActivity(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	store := setupTestDB(t)
	ctx := context.Background()
	const user = "prune-test-user"

	// Clean up.
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_contributor_activity WHERE username = $1`, user)
	})

	// Insert rows: some old (150 days), some recent (10 days).
	now := time.Now().UTC().Truncate(time.Hour)
	oldHour := now.Add(-150 * 24 * time.Hour)
	recentHour := now.Add(-10 * 24 * time.Hour)

	summaries := []postgres.HourlySummary{
		{Username: user, Provider: "github", Hour: oldHour, PRsOpened: 1, Repos: []string{"org/repo"}},
		{Username: user, Provider: "github", Hour: recentHour, PRsOpened: 2, Repos: []string{"org/repo"}},
	}
	_, err := store.BatchUpsertActivity(ctx, summaries)
	require.NoError(t, err)

	// Prune with 120-day retention.
	deleted, err := store.PruneActivity(ctx, 120*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "should delete the 150-day-old row")

	// Verify only recent row remains.
	var count int
	err = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_contributor_activity WHERE username = $1`, user).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/data/postgres/ -run TestPruneActivity -v`
Expected: FAIL — `PruneActivity` not defined

**Step 3: Write PruneActivity method**

Add to `pkg/data/postgres/activity.go` after `CompactActivity`:

```go
// PruneActivity deletes all activity rows older than the given retention
// window. Returns rows deleted. Run after CompactActivity to remove both
// hourly and compacted rows beyond the retention limit.
func (s *Store) PruneActivity(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_contributor_activity WHERE hour < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune activity: %w", err)
	}
	return res.RowsAffected()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/data/postgres/ -run TestPruneActivity -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/data/postgres/activity.go pkg/data/postgres/activity_test.go
git commit -S -m "Add PruneActivity to delete rows beyond 120-day retention"
```

---

### Task 3: Add PruneScoreHistory Method

**Files:**
- Modify: `pkg/data/postgres/history.go` (add method)
- Modify: `pkg/data/postgres/history_test.go` or create if needed

**Step 1: Check if history_test.go exists**

Run: `ls pkg/data/postgres/history_test.go`
If missing, create it. If exists, add to it.

**Step 2: Write failing test**

```go
func TestPruneScoreHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	store := setupTestDB(t)
	ctx := context.Background()
	const user = "prune-hist-user"

	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_reputation_history WHERE username = $1`, user)
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_contributor WHERE username = $1`, user)
	})

	// Ensure contributor row exists (FK).
	require.NoError(t, store.UpsertContributor(ctx, user, "github"))

	// Insert old and recent history rows.
	_, err := store.DB().ExecContext(ctx,
		`INSERT INTO devtrace_reputation_history (username, provider, score, grade, deep, scored_at)
		 VALUES ($1, 'github', 0.75, 'B', false, NOW() - INTERVAL '500 days')`, user)
	require.NoError(t, err)

	require.NoError(t, store.SaveScoreHistory(ctx, user, "github", 0.80, "B", false))

	// Prune with 400-day retention.
	deleted, err := store.PruneScoreHistory(ctx, 400*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "should delete the 500-day-old row")

	// Verify only recent row remains.
	entries, err := store.GetScoreHistory(ctx, user, "github", 100)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./pkg/data/postgres/ -run TestPruneScoreHistory -v`
Expected: FAIL — `PruneScoreHistory` not defined

**Step 4: Write PruneScoreHistory method**

Add to `pkg/data/postgres/history.go`:

```go
// PruneScoreHistory deletes reputation history rows older than the given
// retention window. Returns rows deleted.
func (s *Store) PruneScoreHistory(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_reputation_history WHERE scored_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune score history: %w", err)
	}
	return res.RowsAffected()
}
```

**Step 5: Run test to verify it passes**

Run: `go test ./pkg/data/postgres/ -run TestPruneScoreHistory -v`
Expected: PASS

**Step 6: Commit**

```bash
git add pkg/data/postgres/history.go pkg/data/postgres/history_test.go
git commit -S -m "Add PruneScoreHistory to delete rows beyond 400-day retention"
```

---

### Task 4: Wire Pruning Into Ingest Runner

**Files:**
- Modify: `pkg/ingest/runner.go:13-30,63-88`
- Modify: `pkg/ingest/runner_internal_test.go:18-76,145-175`

**Step 1: Add interface methods and constants**

In `pkg/ingest/runner.go`, add to the constants block (line 13):
```go
pruneActivityRetention = 120 * 24 * time.Hour  // delete activity older than 120 days
pruneHistoryRetention  = 400 * 24 * time.Hour  // delete score history older than 400 days
```

Add to `ingestStore` interface (line 21):
```go
PruneActivity(ctx context.Context, retention time.Duration) (int64, error)
PruneScoreHistory(ctx context.Context, retention time.Duration) (int64, error)
```

**Step 2: Add pruning to maybeCompact**

Rename `maybeCompact` to `maybeMaintain` (or keep name, add pruning after compaction). Add after the compaction log line (after line 84):

```go
// Prune old activity rows beyond retention window.
pruned, err := store.PruneActivity(ctx, pruneActivityRetention)
if err != nil {
	slog.Error("prune activity failed", "error", err)
} else if pruned > 0 {
	slog.Info("pruned old activity", "rows_deleted", pruned)
}

// Prune old score history beyond retention window.
histPruned, err := store.PruneScoreHistory(ctx, pruneHistoryRetention)
if err != nil {
	slog.Error("prune score history failed", "error", err)
} else if histPruned > 0 {
	slog.Info("pruned old score history", "rows_deleted", histPruned)
}
```

**Step 3: Update mock store in tests**

In `pkg/ingest/runner_internal_test.go`, add to `mockIngestStore` struct:
```go
prunedActivity  bool
pruneActivityAge time.Duration
prunedHistory   bool
pruneHistoryAge time.Duration
```

Add mock methods:
```go
func (m *mockIngestStore) PruneActivity(_ context.Context, retention time.Duration) (int64, error) {
	m.prunedActivity = true
	m.pruneActivityAge = retention
	return 10, nil
}

func (m *mockIngestStore) PruneScoreHistory(_ context.Context, retention time.Duration) (int64, error) {
	m.prunedHistory = true
	m.pruneHistoryAge = retention
	return 5, nil
}
```

**Step 4: Update TestMaybeCompactRunsWhenDue**

Add assertions to verify pruning ran with correct retention values:
```go
if !store.prunedActivity {
	t.Error("activity pruning should have run")
}
if store.pruneActivityAge != pruneActivityRetention {
	t.Errorf("prune activity age: got %v, want %v", store.pruneActivityAge, pruneActivityRetention)
}
if !store.prunedHistory {
	t.Error("history pruning should have run")
}
if store.pruneHistoryAge != pruneHistoryRetention {
	t.Errorf("prune history age: got %v, want %v", store.pruneHistoryAge, pruneHistoryRetention)
}
```

**Step 5: Run tests**

Run: `go test ./pkg/ingest/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add pkg/ingest/runner.go pkg/ingest/runner_internal_test.go
git commit -S -m "Wire activity and history pruning into daily maintenance cycle"
```

---

### Task 5: Reset Backfill Cursor via Migration

**Files:**
- Create: `pkg/data/postgres/sql/migrations/004_reset_backfill_cursor.sql`

**Step 1: Write migration**

```sql
-- Reset backfill cursor so skipped hours (due to 1MB scanner buffer)
-- are reprocessed with the new 8MB buffer.
DELETE FROM devtrace_sync_state WHERE key = 'gharchive_backfill_cursor';
```

**Step 2: Verify migration numbering**

Run: `ls pkg/data/postgres/sql/migrations/`
Expected: 001, 002, 003 exist — 004 is next.

**Step 3: Run full qualify**

Run: `make qualify`
Expected: All tests pass, no lint issues.

**Step 4: Commit**

```bash
git add pkg/data/postgres/sql/migrations/004_reset_backfill_cursor.sql
git commit -S -m "Reset backfill cursor to reprocess skipped hours with 8MB buffer"
```

---

### Task 6: Final Validation

**Step 1: Run full qualify**

Run: `make qualify`
Expected: All tests pass, lint clean, e2e passes.

**Step 2: Verify no regressions**

Check that behavioral signals test, compact test, and all existing tests pass.

**Step 3: Single commit if any fixups needed, then push**

```bash
git push origin main
```
