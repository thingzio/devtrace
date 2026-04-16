# Admin Dashboard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace API-key admin endpoints with a session-based admin dashboard at `/admin`, adding tenant management, token pool health, scoring metrics, and pipeline health.

**Architecture:** Single server-rendered page behind `RequireAdmin` middleware (GitHub OAuth session + `DEVTRACE_ADMIN_USERS` env var). Non-admins see 404 (not 403). All admin actions audit-logged via `slog.Warn`. Token pool passed to handler; all other data is DB-driven. Removes 3 API endpoints, `DEVTRACE_ADMIN_API_KEY`, and 3 CLI tools.

**Tech Stack:** Go stdlib, `html/template`, `log/slog`, existing `pkg/data/postgres` store, existing `pkg/github.TokenPool`.

**Design doc:** `docs/plans/2026-04-16-admin-dashboard-design.md`

---

### Task 1: RequireAdmin Middleware

**Files:**
- Create: `pkg/middleware/admin.go`
- Create: `pkg/middleware/admin_test.go`

**Step 1: Write the failing tests**

In `pkg/middleware/admin_test.go`:

```go
package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		username string
		want     bool
	}{
		{"in_list", "alice,bob", "alice", true},
		{"not_in_list", "alice,bob", "eve", false},
		{"empty_env", "", "alice", false},
		{"spaces", " alice , bob ", "alice", true},
		{"case_sensitive", "Alice", "alice", false},
		{"single_admin", "alice", "alice", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DEVTRACE_ADMIN_USERS", tt.envVal)
			if got := IsAdmin(tt.username); got != tt.want {
				t.Errorf("IsAdmin(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}

func TestRequireAdmin_NoSession(t *testing.T) {
	handler := RequireAdmin(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
	}))
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRequireAdmin_NotAdmin(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "alice")
	tn := &tenant.Tenant{ID: "t-1", Username: "eve", Status: "active"}
	ctx := WithTenantContext(context.Background(), tn)

	handler := RequireAdmin(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
	}))
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRequireAdmin_IsAdmin(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "alice,bob")
	tn := &tenant.Tenant{ID: "t-1", Username: "alice", Status: "active"}
	ctx := WithTenantContext(context.Background(), tn)

	var called bool
	handler := RequireAdmin(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !called {
		t.Error("handler should have been called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/middleware/ -run TestIsAdmin -v`
Expected: FAIL (function not defined)

**Step 3: Write the implementation**

In `pkg/middleware/admin.go`:

```go
package middleware

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// IsAdmin checks if the given username is in the DEVTRACE_ADMIN_USERS env var.
func IsAdmin(username string) bool {
	raw := os.Getenv("DEVTRACE_ADMIN_USERS")
	if raw == "" {
		return false
	}
	for _, u := range strings.Split(raw, ",") {
		if strings.TrimSpace(u) == username {
			return true
		}
	}
	return false
}

// RequireAdmin wraps RequireAuth and additionally checks the admin user list.
// Non-admins receive 404 (not 403) to avoid revealing the route exists.
func RequireAdmin(db *sql.DB) func(http.Handler) http.Handler {
	sessionAuth := RequireAuth(db, "")
	return func(next http.Handler) http.Handler {
		return sessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tn := TenantFromContext(r.Context())
			if tn == nil || !IsAdmin(tn.Username) {
				slog.Warn("admin access denied",
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
					"username", adminUsername(tn),
				)
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func adminUsername(tn *Tenant) string {
	if tn == nil {
		return "<anonymous>"
	}
	return tn.Username
}
```

Note: `RequireAuth` with empty loginURL will redirect to `""` which won't work for non-session users. Fix: the `RequireAdmin` middleware should handle the nil-tenant case directly instead of relying on `RequireAuth` redirect. Revised approach — don't wrap `RequireAuth`, check session inline:

```go
package middleware

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devtrace/pkg/tenant"
)

// IsAdmin checks if the given username is in the DEVTRACE_ADMIN_USERS env var.
func IsAdmin(username string) bool {
	raw := os.Getenv("DEVTRACE_ADMIN_USERS")
	if raw == "" {
		return false
	}
	for _, u := range strings.Split(raw, ",") {
		if strings.TrimSpace(u) == username {
			return true
		}
	}
	return false
}

// RequireAdmin validates the session and checks the admin user list.
// Returns 404 for unauthenticated users and non-admins (hides route existence).
func RequireAdmin(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil {
				http.NotFound(w, r)
				return
			}
			tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
			if err != nil || tn == nil || !IsAdmin(tn.Username) {
				slog.Warn("admin access denied",
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
					"username", tenantUsername(tn),
				)
				http.NotFound(w, r)
				return
			}
			ctx := WithTenantContext(r.Context(), tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func tenantUsername(tn *tenant.Tenant) string {
	if tn == nil {
		return "<anonymous>"
	}
	return tn.Username
}
```

**Step 4: Run all middleware tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/middleware/ -v`
Expected: PASS

**Step 5: Commit**

```
git add pkg/middleware/admin.go pkg/middleware/admin_test.go
git commit -S -m "Add RequireAdmin middleware with 404 for non-admins"
```

---

### Task 2: Store Methods for Admin Metrics

**Files:**
- Modify: `pkg/data/postgres/queue.go`
- Modify: `pkg/data/postgres/stale.go`
- Modify: `pkg/data/postgres/history.go`
- Modify: `pkg/data/postgres/activity.go`
- Modify: `pkg/data/postgres/queue_test.go`
- Modify: `pkg/data/postgres/stale_test.go`
- Modify: `pkg/data/postgres/history_test.go`
- Modify: `pkg/data/postgres/activity_test.go`

**Step 1: Write failing tests**

Add to `pkg/data/postgres/queue_test.go`:

```go
func TestQueueDepth(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	// Empty queue.
	depth, err := store.QueueDepth(ctx)
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth != 0 {
		t.Errorf("depth = %d, want 0", depth)
	}

	// Enqueue two entries.
	require.NoError(t, store.EnqueueForScoring(ctx, "alice", "github", 1))
	require.NoError(t, store.EnqueueForScoring(ctx, "bob", "github", 2))

	depth, err = store.QueueDepth(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, depth)
}
```

Add to `pkg/data/postgres/stale_test.go`:

```go
func TestStaleCount(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	count, err := store.StaleCount(ctx, 7, 30)
	if err != nil {
		t.Fatalf("StaleCount: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}
```

Add to `pkg/data/postgres/history_test.go`:

```go
func TestScoringMetrics(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	m, err := store.ScoringMetrics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, m.Last1h)
	assert.Equal(t, 0, m.Last24h)
	assert.Equal(t, 0, m.Last72h)
	assert.Equal(t, 0, m.ThisWeek)
}
```

Add to `pkg/data/postgres/activity_test.go`:

```go
func TestPipelineStats(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	ps, err := store.PipelineStats(ctx)
	require.NoError(t, err)
	assert.True(t, ps.LastIngest.IsZero())
	assert.True(t, ps.LastScored.IsZero())
	assert.Equal(t, 0, ps.TotalActivities)
}
```

**Step 2: Run tests to verify they fail**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/data/postgres/ -run "TestQueueDepth|TestStaleCount|TestScoringMetrics|TestPipelineStats" -v -short`
Expected: FAIL (methods not defined)

**Step 3: Write implementations**

In `pkg/data/postgres/queue.go`, add:

```go
// QueueDepth returns the number of entries in the scoring queue.
func (s *Store) QueueDepth(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_scoring_queue`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("queue depth: %w", err)
	}
	return count, nil
}
```

In `pkg/data/postgres/stale.go`, add:

```go
// StaleCount returns the number of contributors whose scores are stale.
func (s *Store) StaleCount(ctx context.Context, lowDays, highDays int) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_reputation
		 WHERE (score < 0.5 AND scored_at < NOW() - MAKE_INTERVAL(days => $1))
		    OR (score >= 0.5 AND scored_at < NOW() - MAKE_INTERVAL(days => $2))`,
		lowDays, highDays).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("stale count: %w", err)
	}
	return count, nil
}
```

In `pkg/data/postgres/history.go`, add:

```go
// ScoringMetrics holds time-bucketed scoring counts.
type ScoringMetrics struct {
	Last1h   int
	Last24h  int
	Last72h  int
	ThisWeek int
}

// ScoringMetrics returns time-bucketed counts of scores recorded.
func (s *Store) ScoringMetrics(ctx context.Context) (*ScoringMetrics, error) {
	var m ScoringMetrics
	err := s.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*) FILTER (WHERE scored_at > NOW() - INTERVAL '1 hour'),
			COUNT(*) FILTER (WHERE scored_at > NOW() - INTERVAL '24 hours'),
			COUNT(*) FILTER (WHERE scored_at > NOW() - INTERVAL '72 hours'),
			COUNT(*) FILTER (WHERE scored_at > DATE_TRUNC('week', NOW()))
		 FROM devtrace_reputation_history`).Scan(
		&m.Last1h, &m.Last24h, &m.Last72h, &m.ThisWeek)
	if err != nil {
		return nil, fmt.Errorf("scoring metrics: %w", err)
	}
	return &m, nil
}
```

In `pkg/data/postgres/activity.go`, add:

```go
// PipelineStats holds pipeline health metrics inferred from DB timestamps.
type PipelineStats struct {
	LastIngest      time.Time
	LastScored      time.Time
	TotalActivities int
}

// PipelineStats returns pipeline health indicators from existing tables.
func (s *Store) PipelineStats(ctx context.Context) (*PipelineStats, error) {
	var ps PipelineStats
	var lastIngest, lastScored sql.NullTime

	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(hour), '0001-01-01'), COUNT(*)
		 FROM devtrace_contributor_activity`).Scan(&lastIngest, &ps.TotalActivities)
	if err != nil {
		return nil, fmt.Errorf("pipeline stats activity: %w", err)
	}
	if lastIngest.Valid {
		ps.LastIngest = lastIngest.Time
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(scored_at), '0001-01-01')
		 FROM devtrace_reputation_history`).Scan(&lastScored)
	if err != nil {
		return nil, fmt.Errorf("pipeline stats history: %w", err)
	}
	if lastScored.Valid {
		ps.LastScored = lastScored.Time
	}

	return &ps, nil
}
```

**Step 4: Run tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/data/postgres/ -run "TestQueueDepth|TestStaleCount|TestScoringMetrics|TestPipelineStats" -v -short`
Expected: PASS (short mode skips DB tests if guarded by `setupTestDB`)

**Step 5: Commit**

```
git add pkg/data/postgres/queue.go pkg/data/postgres/stale.go pkg/data/postgres/history.go pkg/data/postgres/activity.go
git add pkg/data/postgres/queue_test.go pkg/data/postgres/stale_test.go pkg/data/postgres/history_test.go pkg/data/postgres/activity_test.go
git commit -S -m "Add admin metrics store methods: queue depth, stale count, scoring metrics, pipeline stats"
```

---

### Task 3: TokenPool Getter

**Files:**
- Modify: `pkg/github/pool_client.go`
- Modify: `pkg/github/pool_client_test.go`

**Step 1: Write failing test**

Add to `pkg/github/pool_client_test.go`:

```go
func TestPoolClient_Pool(t *testing.T) {
	pool := NewTokenPool("tok1", "tok2")
	client := NewPoolClient(pool)
	if got := client.Pool(); got != pool {
		t.Error("Pool() should return the same pool instance")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run TestPoolClient_Pool -v`
Expected: FAIL

**Step 3: Write implementation**

Add to `pkg/github/pool_client.go`:

```go
// Pool returns the underlying token pool for operational visibility.
func (c *PoolClient) Pool() *TokenPool {
	return c.pool
}
```

**Step 4: Run test**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/github/ -run TestPoolClient_Pool -v`
Expected: PASS

**Step 5: Commit**

```
git add pkg/github/pool_client.go pkg/github/pool_client_test.go
git commit -S -m "Add Pool() getter to PoolClient for admin visibility"
```

---

### Task 4: Admin Handlers

**Files:**
- Rewrite: `pkg/server/handler_admin.go`
- Create: `pkg/server/handler_admin_test.go`

**Step 1: Write failing tests**

Create `pkg/server/handler_admin_test.go`:

```go
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestAdminDashboardHandler_NoTenant(t *testing.T) {
	pool := ghclient.NewTokenPool("tok1")
	handler := adminDashboardHandler(nil, pool, Options{Version: "test"})

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// No tenant in context — handler redirects or errors
	if w.Code == http.StatusOK {
		t.Error("expected non-200 for unauthenticated request")
	}
}

func TestAdminDashboardHandler_WithTenant(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "admin-user")
	pool := ghclient.NewTokenPool("tok1", "tok2")
	tn := &tenant.Tenant{ID: "t-1", Username: "admin-user", Status: "active"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	handler := adminDashboardHandler(nil, pool, Options{Version: "test"})

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// With nil store, handler should still render (with zero metrics)
	// or return an error — the point is it doesn't panic.
	if w.Code == 0 {
		t.Error("expected a response code")
	}
}

func TestAdminAuditLog(t *testing.T) {
	// Verify audit log helper doesn't panic with nil tenant
	auditLog("test_action", nil, "/admin", "127.0.0.1", "detail")
}
```

**Step 2: Run to verify failure**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -run TestAdmin -v`
Expected: FAIL

**Step 3: Rewrite `handler_admin.go`**

```go
package server

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
)

// auditLog records an admin action at Warn level for visibility.
func auditLog(action string, tn *tenant.Tenant, path, remoteAddr, detail string) {
	username := "<anonymous>"
	if tn != nil {
		username = tn.Username
	}
	slog.Warn("admin action",
		"action", action,
		"admin", username,
		"path", path,
		"remote", remoteAddr,
		"detail", detail,
	)
}

func adminDashboardHandler(store *postgres.Store, pool *ghclient.TokenPool, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.NotFound(w, r)
			return
		}

		auditLog("view_dashboard", tn, r.URL.Path, r.RemoteAddr, "")

		data := map[string]any{
			"Title":     "Admin",
			"Version":   opts.Version,
			"Commit":    opts.Commit,
			"Date":      opts.Date,
			"NavUser":   tn.Username,
			"NavAvatar": tn.AvatarURL,
		}

		// Flash messages from form submissions.
		if msg := r.URL.Query().Get("msg"); msg != "" {
			data["FlashMsg"] = msg
			data["FlashUser"] = r.URL.Query().Get("user")
		}

		// Tenant list.
		if store != nil {
			if tenants, err := tenant.ListTenants(r.Context(), store.DB()); err == nil {
				data["Tenants"] = tenants
			} else {
				slog.Error("admin: list tenants", "error", err)
			}
		}

		// Token pool health.
		if pool != nil {
			data["PoolTotal"] = pool.Size()
			data["PoolActive"] = pool.ActiveCount()
			data["PoolExhausted"] = pool.Size() - pool.ActiveCount()
			data["PoolUsage"] = pool.UsageCounts()
		}

		// Scoring metrics.
		if store != nil {
			if m, err := store.ScoringMetrics(r.Context()); err == nil {
				data["ScoringMetrics"] = m
			} else {
				slog.Error("admin: scoring metrics", "error", err)
			}

			depth, _ := store.QueueDepth(r.Context())
			data["QueueDepth"] = depth

			stale, _ := store.StaleCount(r.Context(), 7, 30)
			data["StaleCount"] = stale
		}

		// Pipeline health.
		if store != nil {
			if ps, err := store.PipelineStats(r.Context()); err == nil {
				data["PipelineStats"] = ps
				data["IngestAge"] = timeSince(ps.LastIngest)
				data["ScorerAge"] = timeSince(ps.LastScored)
			} else {
				slog.Error("admin: pipeline stats", "error", err)
			}
		}

		renderTemplate(w, "admin.html", data)
	}
}

func adminUpdatePlanFormHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		username := r.PathValue("username")
		if username == "" {
			http.Redirect(w, r, "/admin?msg=error&user=unknown", http.StatusFound)
			return
		}

		newPlan := r.FormValue("plan")
		p, ok := plan.Get(newPlan)
		if !ok {
			http.Redirect(w, r, "/admin?msg=invalid_plan&user="+username, http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin?msg=not_found&user="+username, http.StatusFound)
			return
		}

		if _, err := tenant.UpdateTenantPlan(r.Context(), db, target.ID, newPlan, p.MaxContributors); err != nil {
			slog.Error("admin: update plan", "username", username, "error", err)
			http.Redirect(w, r, "/admin?msg=error&user="+username, http.StatusFound)
			return
		}

		auditLog("update_plan", tn, r.URL.Path, r.RemoteAddr, "plan="+newPlan)
		http.Redirect(w, r, "/admin?msg=plan_updated&user="+username, http.StatusFound)
	}
}

func adminUpdateStatusFormHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		username := r.PathValue("username")
		if username == "" {
			http.Redirect(w, r, "/admin?msg=error&user=unknown", http.StatusFound)
			return
		}

		newStatus := r.FormValue("status")
		if newStatus != tenant.StatusActive && newStatus != tenant.StatusSuspended {
			http.Redirect(w, r, "/admin?msg=invalid_status&user="+username, http.StatusFound)
			return
		}

		target, err := tenant.GetTenantByUsername(r.Context(), db, username)
		if err != nil {
			http.Redirect(w, r, "/admin?msg=not_found&user="+username, http.StatusFound)
			return
		}

		if _, err := tenant.UpdateTenantStatus(r.Context(), db, target.ID, newStatus); err != nil {
			slog.Error("admin: update status", "username", username, "error", err)
			http.Redirect(w, r, "/admin?msg=error&user="+username, http.StatusFound)
			return
		}

		auditLog("update_status", tn, r.URL.Path, r.RemoteAddr, "status="+newStatus)
		http.Redirect(w, r, "/admin?msg=status_updated&user="+username, http.StatusFound)
	}
}

func timeSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
```

Note: needs `"fmt"` import and `"github.com/thingzio/devtrace/pkg/data/postgres"` import.

**Step 4: Run tests**

Run: `GOFLAGS="-mod=vendor" go test ./pkg/server/ -run TestAdmin -v`
Expected: PASS

**Step 5: Commit**

```
git add pkg/server/handler_admin.go pkg/server/handler_admin_test.go
git commit -S -m "Rewrite admin handlers: session-based with audit logging"
```

---

### Task 5: Admin Template

**Files:**
- Create: `pkg/server/templates/admin.html`
- Modify: `pkg/server/server.go` (register template in `init()`)

**Step 1: Create the admin template**

Create `pkg/server/templates/admin.html` using the `layout.html` wrapper (same as `settings.html`):

```html
{{define "content"}}
<main class="dashboard-main">
<div class="settings">
  <a href="/dashboard" class="back-link">&larr; Dashboard</a>
  <h1>Admin</h1>

  {{if .FlashMsg}}
  <div class="flash-msg">{{.FlashMsg}}{{if .FlashUser}} ({{.FlashUser}}){{end}}</div>
  {{end}}

  <section class="settings-section">
    <h2>Pipeline Health</h2>
    {{if .PipelineStats}}
    <dl>
      <dt>Last Ingest</dt><dd>{{.IngestAge}}</dd>
      <dt>Last Scored</dt><dd>{{.ScorerAge}}</dd>
      <dt>Total Activity Records</dt><dd>{{.PipelineStats.TotalActivities}}</dd>
    </dl>
    {{else}}<p>No pipeline data available.</p>{{end}}
  </section>

  <section class="settings-section">
    <h2>Scoring Metrics</h2>
    {{if .ScoringMetrics}}
    <dl>
      <dt>Last 1 hour</dt><dd>{{.ScoringMetrics.Last1h}}</dd>
      <dt>Last 24 hours</dt><dd>{{.ScoringMetrics.Last24h}}</dd>
      <dt>Last 72 hours</dt><dd>{{.ScoringMetrics.Last72h}}</dd>
      <dt>This week</dt><dd>{{.ScoringMetrics.ThisWeek}}</dd>
      <dt>Queue depth</dt><dd>{{.QueueDepth}}</dd>
      <dt>Stale contributors</dt><dd>{{.StaleCount}}</dd>
    </dl>
    {{else}}<p>No scoring data available.</p>{{end}}
  </section>

  <section class="settings-section">
    <h2>Token Pool</h2>
    {{if .PoolTotal}}
    <dl>
      <dt>Total</dt><dd>{{.PoolTotal}}</dd>
      <dt>Active</dt><dd>{{.PoolActive}}</dd>
      <dt>Exhausted</dt><dd>{{.PoolExhausted}}</dd>
    </dl>
    {{if .PoolUsage}}
    <table class="data-table">
      <thead><tr><th>Token #</th><th>Calls</th></tr></thead>
      <tbody>
        {{range $i, $count := .PoolUsage}}
        <tr><td>{{$i}}</td><td>{{$count}}</td></tr>
        {{end}}
      </tbody>
    </table>
    {{end}}
    {{else}}<p>No token pool configured.</p>{{end}}
  </section>

  <section class="settings-section">
    <h2>Tenants</h2>
    {{if .Tenants}}
    <table class="data-table">
      <thead>
        <tr>
          <th>Username</th>
          <th>Plan</th>
          <th>Status</th>
          <th>Max Contributors</th>
          <th>Created</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {{range .Tenants}}
        <tr>
          <td><a href="/score/{{.Username}}">{{.Username}}</a></td>
          <td>
            <form method="POST" action="/admin/tenant/{{.Username}}/plan" style="display:inline">
              <select name="plan">
                <option value="free" {{if eq .Plan "free"}}selected{{end}}>free</option>
                <option value="starter" {{if eq .Plan "starter"}}selected{{end}}>starter</option>
                <option value="pro" {{if eq .Plan "pro"}}selected{{end}}>pro</option>
              </select>
              <button type="submit" class="btn btn-sm">Update</button>
            </form>
          </td>
          <td>
            <form method="POST" action="/admin/tenant/{{.Username}}/status" style="display:inline">
              <select name="status">
                <option value="active" {{if eq .Status "active"}}selected{{end}}>active</option>
                <option value="suspended" {{if eq .Status "suspended"}}selected{{end}}>suspended</option>
              </select>
              <button type="submit" class="btn btn-sm">Update</button>
            </form>
          </td>
          <td>{{.MaxContributors}}</td>
          <td>{{.CreatedAt.Format "2006-01-02"}}</td>
          <td><a href="/score/{{.Username}}">Score</a></td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}<p>No tenants found.</p>{{end}}
  </section>
</div>
</main>
{{end}}
```

**Step 2: Register admin template in init()**

In `pkg/server/server.go`, add `"admin.html"` to the `simplePages` slice in `init()`:

```go
simplePages := []string{"admin.html", "landing.html", "scorecard.html", "tos.html", "settings.html", "stub.html", "help.html", "ratelimit.html"}
```

**Step 3: Verify template compiles**

Run: `GOFLAGS="-mod=vendor" go build ./cmd/devtrace-site/`
Expected: BUILD SUCCESS

**Step 4: Commit**

```
git add pkg/server/templates/admin.html pkg/server/server.go
git commit -S -m "Add admin dashboard template and register in init"
```

---

### Task 6: Wire Routes in Router

**Files:**
- Modify: `pkg/server/server.go` (replace admin API routes with new routes, pass pool)

**Step 1: Update `makeRouter` signature and route registration**

In `pkg/server/server.go`:

1. Change `makeRouter` to accept `*ghclient.TokenPool`:

```go
func makeRouter(store *postgres.Store, scoreSvc *service.ScoreService, pool *ghclient.TokenPool, oauthCfg *oauth.Config, opts Options) (*http.ServeMux, func()) {
```

2. Replace the admin API routes (lines 363-366) with:

```go
	// Admin — session auth + admin user list, returns 404 for non-admins
	requireAdmin := middleware.RequireAdmin(db)
	mux.Handle("GET /admin", requireAdmin(adminDashboardHandler(store, pool, opts)))
	mux.Handle("POST /admin/tenant/{username}/plan", requireAdmin(adminUpdatePlanFormHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/status", requireAdmin(adminUpdateStatusFormHandler(db)))
```

3. Update the `makeRouter` call in `Run()` to pass the pool. Extract pool from `ghClient`:

```go
	var pool *ghclient.TokenPool
	if pc, ok := ghClient.(*ghclient.PoolClient); ok {
		pool = pc.Pool()
	}

	mux, routerCleanup := makeRouter(store, scoreSvc, pool, oauthCfg, opts)
```

**Step 2: Run build**

Run: `GOFLAGS="-mod=vendor" go build ./cmd/devtrace-site/`
Expected: BUILD SUCCESS

**Step 3: Run all tests**

Run: `GOFLAGS="-mod=vendor" go test -short -count=1 -race ./...`
Expected: PASS

**Step 4: Commit**

```
git add pkg/server/server.go
git commit -S -m "Wire admin dashboard routes, remove API-key admin endpoints"
```

---

### Task 7: Remove Old Admin CLI Tools

**Files:**
- Delete: `tools/tenant-list`
- Delete: `tools/tenant-plan`
- Delete: `tools/tenant-status`

**Step 1: Verify no other scripts source these tools**

Run: `grep -r "tenant-list\|tenant-plan\|tenant-status" tools/ .github/`
Expected: Only self-references in the files being deleted.

**Step 2: Delete the files**

```bash
rm tools/tenant-list tools/tenant-plan tools/tenant-status
```

**Step 3: Commit**

```
git add -A tools/tenant-list tools/tenant-plan tools/tenant-status
git commit -S -m "Remove admin CLI tools replaced by admin dashboard"
```

---

### Task 8: Update Docs

**Files:**
- Modify: `docs/MVP.md` (update Phase 8 status)
- Modify: `.claude/CLAUDE.md` (update env vars, architecture)

**Step 1: Update MVP.md Phase 8**

Replace the Phase 8 section to mark it complete:

```markdown
### Phase 8 — Admin Dashboard ✅

Session-based admin dashboard at `/admin`, behind GitHub OAuth + `DEVTRACE_ADMIN_USERS` env var. Returns 404 for non-admins. All admin actions audit-logged via `slog.Warn`.

**Sections:** Tenant management (plan/status), token pool health, scoring metrics (1h/24h/72h/week counts + queue depth + stale count), pipeline health (last ingest/scorer timestamps + activity count).

**Replaced:** `DEVTRACE_ADMIN_API_KEY`-protected API endpoints and `tools/tenant-{list,plan,status}` CLI scripts — reduced attack surface.
```

**Step 2: Update CLAUDE.md**

- Add `DEVTRACE_ADMIN_USERS` to env vars section
- Remove `DEVTRACE_ADMIN_API_KEY` reference (if present)
- Update architecture section to mention admin dashboard

**Step 3: Commit**

```
git add docs/MVP.md .claude/CLAUDE.md
git commit -S -m "Update docs: Phase 8 complete, admin dashboard"
```

---

### Task 9: Qualify

**Step 1: Run full qualification**

Run: `make qualify`
Expected: All tests pass, lint clean, vulncheck clean.

**Step 2: Fix any issues found**

If lint/test failures, fix and re-run.

**Step 3: Final commit (if any fixes)**

```
git commit -S -m "Fix lint/test issues from qualification"
```
