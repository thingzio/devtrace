# Tier 2 UI + Admin Plan/Status Management Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Render AI Sensing Tier 2 heuristics on the scorecard for Pro users, and add admin API endpoints for tenant plan and status management.

**Architecture:** Pass `AISensing` to the scorecard template and conditionally render a new section. Add `handler_admin.go` with two endpoints protected by `DEVTRACE_ADMIN_API_KEY` env var. Add tenant `status` column via migration 002, enforce suspension in auth middleware.

**Tech Stack:** Go, HTML templates, CSS, PostgreSQL

**Design doc:** `docs/plans/2026-04-15-tier2-ui-admin-design.md`

---

## Task 1: Add Tenant Status Constants, Field, and Migration

**Files:**
- Modify: `pkg/tenant/tenant.go`
- Create: `pkg/data/postgres/sql/migrations/002_tenant_status.sql`

**Step 1: Add status constants and field to Tenant struct**

In `pkg/tenant/tenant.go`, add constants before the Tenant struct:

```go
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)
```

Add `Status` field to the Tenant struct after `Plan`:

```go
	Status          string
```

**Step 2: Update scanTenant to include Status**

Find the `scanTenant` helper that scans DB rows into a `*Tenant`. Add `&t.Status` to the scan list. The scan order must match the SELECT column order in all queries that use it.

Update ALL `SELECT` queries in `pkg/tenant/tenant.go` (UpsertTenant, GetTenantByID, GetTenantByGitHubID) to include `status` in their column lists.

Also update `pkg/tenant/apitoken.go` (`ValidateAPIToken` joins devtrace_tenant) and `pkg/tenant/session.go` (`ValidateSession` joins devtrace_tenant) — their SELECT lists must include `t.status`.

**Step 3: Create migration**

Create `pkg/data/postgres/sql/migrations/002_tenant_status.sql`:

```sql
-- Add status column to devtrace_tenant for account suspension.
ALTER TABLE devtrace_tenant ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
```

**Step 4: Run migration on startup**

Check how migration 001 is applied. The migration runner in `pkg/data/postgres/` should pick up 002 automatically if it uses ordered file listing. Verify this by reading the migration runner code.

**Step 5: Verify compilation**

Run: `go vet ./...`
Expected: No errors

**Step 6: Commit**

```bash
git add pkg/tenant/tenant.go pkg/tenant/apitoken.go pkg/tenant/session.go pkg/data/postgres/sql/migrations/002_tenant_status.sql
git commit -S -m "Add tenant status column, constants, and migration 002"
```

---

## Task 2: Enforce Suspension in Auth Middleware

**Files:**
- Modify: `pkg/middleware/auth.go`
- Modify: `pkg/tenant/tenant.go` (if needed)

**Step 1: Add suspension check to RequireAPIToken**

In `pkg/middleware/auth.go`, in the `RequireAPIToken` middleware, after `ValidateAPIToken` succeeds and returns a `*Tenant`, add:

```go
			if tn.Status == tenant.StatusSuspended {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "account suspended"})
				return
			}
```

This goes between the `ValidateAPIToken` error check and the `context.WithValue` line.

**Step 2: Add suspension check to session validation**

Find wherever session-based auth loads a tenant (likely `RequireSession` or similar in `auth.go`). Add the same suspension check. For session auth, redirect to a suspended page or show an error rather than returning JSON.

**Step 3: Verify compilation**

Run: `go vet ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add pkg/middleware/auth.go
git commit -S -m "Enforce tenant suspension in API token and session auth middleware"
```

---

## Task 3: Add UpdateTenantPlan and UpdateTenantStatus Queries

**Files:**
- Modify: `pkg/tenant/tenant.go`

**Step 1: Write UpdateTenantPlan**

Add to `pkg/tenant/tenant.go`:

```go
// UpdateTenantPlan updates a tenant's plan and max_contributors.
func UpdateTenantPlan(ctx context.Context, db *sql.DB, tenantID, plan string, maxContributors int) (*Tenant, error) {
	var t Tenant
	err := db.QueryRowContext(ctx,
		`UPDATE devtrace_tenant
		 SET plan = $2, max_contributors = $3, updated_at = NOW()
		 WHERE id = $1
		 RETURNING id, github_id, username, email, avatar_url, name, company, location, bio,
		           plan, status, max_contributors, tos_accepted_at, created_at, updated_at`,
		tenantID, plan, maxContributors,
	).Scan(&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL, &t.Name, &t.Company,
		&t.Location, &t.Bio, &t.Plan, &t.Status, &t.MaxContributors, &t.ToSAcceptedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update tenant plan: %w", err)
	}
	return &t, nil
}
```

**Step 2: Write UpdateTenantStatus**

```go
// UpdateTenantStatus updates a tenant's status (active or suspended).
func UpdateTenantStatus(ctx context.Context, db *sql.DB, tenantID, status string) (*Tenant, error) {
	if status != StatusActive && status != StatusSuspended {
		return nil, fmt.Errorf("invalid status: %s", status)
	}
	var t Tenant
	err := db.QueryRowContext(ctx,
		`UPDATE devtrace_tenant
		 SET status = $2, updated_at = NOW()
		 WHERE id = $1
		 RETURNING id, github_id, username, email, avatar_url, name, company, location, bio,
		           plan, status, max_contributors, tos_accepted_at, created_at, updated_at`,
		tenantID, status,
	).Scan(&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL, &t.Name, &t.Company,
		&t.Location, &t.Bio, &t.Plan, &t.Status, &t.MaxContributors, &t.ToSAcceptedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update tenant status: %w", err)
	}
	return &t, nil
}
```

**Step 3: Verify compilation**

Run: `go vet ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add pkg/tenant/tenant.go
git commit -S -m "Add UpdateTenantPlan and UpdateTenantStatus queries"
```

---

## Task 4: Admin API Handlers

**Files:**
- Create: `pkg/server/handler_admin.go`
- Modify: `pkg/server/server.go`

**Step 1: Create handler_admin.go**

Create `pkg/server/handler_admin.go`:

```go
package server

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
)

// adminAuth checks the DEVTRACE_ADMIN_API_KEY env var against the Authorization: Bearer header.
// Returns false and writes 401 if auth fails.
func adminAuth(w http.ResponseWriter, r *http.Request) bool {
	key := os.Getenv("DEVTRACE_ADMIN_API_KEY")
	if key == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "admin api not configured"})
		return false
	}
	token := extractBearerToken(r)
	if subtle.ConstantTimeCompare([]byte(token), []byte(key)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin key"})
		return false
	}
	return true
}

func adminUpdatePlanHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		tenantID := r.PathValue("id")
		if tenantID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing tenant id"})
			return
		}

		var req struct {
			Plan string `json:"plan"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		p, ok := plan.Get(req.Plan)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan: must be free, starter, or pro"})
			return
		}

		tn, err := tenant.UpdateTenantPlan(r.Context(), db, tenantID, req.Plan, p.MaxContributors)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update plan"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":        tn.ID,
			"username":         tn.Username,
			"plan":             tn.Plan,
			"max_contributors": tn.MaxContributors,
			"updated_at":       tn.UpdatedAt.Format(time.RFC3339),
		})
	}
}

func adminUpdateStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuth(w, r) {
			return
		}

		tenantID := r.PathValue("id")
		if tenantID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing tenant id"})
			return
		}

		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Status != tenant.StatusActive && req.Status != tenant.StatusSuspended {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid status: must be " + tenant.StatusActive + " or " + tenant.StatusSuspended,
			})
			return
		}

		tn, err := tenant.UpdateTenantStatus(r.Context(), db, tenantID, req.Status)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update status"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id":  tn.ID,
			"username":   tn.Username,
			"status":     tn.Status,
			"updated_at": tn.UpdatedAt.Format(time.RFC3339),
		})
	}
}
```

**Step 2: Register admin routes in server.go**

In `pkg/server/server.go`, add after the webhook handler registration (before the `cleanup` function):

```go
	// Admin API — protected by DEVTRACE_ADMIN_API_KEY env var
	mux.HandleFunc("PUT /api/v1/admin/tenant/{id}/plan", adminUpdatePlanHandler(db))
	mux.HandleFunc("PUT /api/v1/admin/tenant/{id}/status", adminUpdateStatusHandler(db))
```

**Step 3: Verify compilation**

Run: `go vet ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add pkg/server/handler_admin.go pkg/server/server.go
git commit -S -m "Add admin API endpoints for tenant plan and status management"
```

---

## Task 5: Makefile Targets

**Files:**
- Modify: `Makefile`

**Step 1: Add admin targets**

Add before the Cleanup section in the Makefile:

```makefile
# =============================================================================
# Admin
# =============================================================================

.PHONY: set-plan
set-plan: ## Changes a tenant's plan (TENANT=uuid PLAN=free|starter|pro)
	@curl -s -X PUT \
		-H "Authorization: Bearer $(DEVTRACE_ADMIN_API_KEY)" \
		-H "Content-Type: application/json" \
		-d '{"plan":"$(PLAN)"}' \
		$(API_URL)/api/v1/admin/tenant/$(TENANT)/plan | jq .

.PHONY: set-status
set-status: ## Changes a tenant's status (TENANT=uuid STATUS=active|suspended)
	@curl -s -X PUT \
		-H "Authorization: Bearer $(DEVTRACE_ADMIN_API_KEY)" \
		-H "Content-Type: application/json" \
		-d '{"status":"$(STATUS)"}' \
		$(API_URL)/api/v1/admin/tenant/$(TENANT)/status | jq .
```

**Step 2: Commit**

```bash
git add Makefile
git commit -S -m "Add set-plan and set-status Makefile targets"
```

---

## Task 6: Pass AISensing to Scorecard Template

**Files:**
- Modify: `pkg/server/handler_pages.go`

**Step 1: Add AISensing to template data**

In `scorecardHandler` in `pkg/server/handler_pages.go`, add to the `data` map (after `"RepoContext"`):

```go
			"AISensing":  resp.AISensing,
```

Also add it to the error data map:

```go
			"AISensing": nil,
```

**Step 2: Verify compilation**

Run: `go vet ./...`
Expected: No errors

**Step 3: Commit**

```bash
git add pkg/server/handler_pages.go
git commit -S -m "Pass AISensing data to scorecard template"
```

---

## Task 7: Scorecard AI Sensing Section (Template + CSS)

**Files:**
- Modify: `pkg/server/templates/scorecard.html`
- Modify: `pkg/server/static/css/app.css`

**Step 1: Add AI Sensing section to scorecard template**

In `scorecard.html`, add after the `{{end}}` of the `{{if .Categories}}` section and before the signals section. The section only renders when `AISensing.Behavioral` is present (Pro plan):

```html
  {{if .AISensing}}{{if .AISensing.Behavioral}}
  <section class="ai-sensing">
    <h2>AI Sensing <span class="plan-badge">Pro</span></h2>
    <div class="metric-cards">
      <div class="metric-card">
        <div class="metric-value">{{printf "%.2f" .AISensing.Behavioral.VelocityAnomalyRatio}}×</div>
        <div class="metric-label">Velocity Anomaly</div>
        <div class="metric-interp {{velocityClass .AISensing.Behavioral.VelocityAnomalyRatio}}">
          {{velocityLabel .AISensing.Behavioral.VelocityAnomalyRatio}}
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-value">{{.AISensing.Behavioral.ActiveHourSpread}}</div>
        <div class="metric-label">Active Hours</div>
        <div class="metric-interp {{hourClass .AISensing.Behavioral.ActiveHourSpread}}">
          {{hourLabel .AISensing.Behavioral.ActiveHourSpread}}
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-value">{{printf "%.2f" .AISensing.Behavioral.BurstVanishScore}}</div>
        <div class="metric-label">Burst-Vanish</div>
        <div class="metric-interp {{burstClass .AISensing.Behavioral.BurstVanishScore}}">
          {{burstLabel .AISensing.Behavioral.BurstVanishScore}}
        </div>
      </div>
    </div>
    <div class="synthetic-risk">
      <span class="synthetic-badge {{syntheticClass .AISensing.Behavioral.SyntheticRiskFlags}}"
            onclick="this.parentElement.querySelector('.synthetic-details').classList.toggle('open')">
        {{.AISensing.Behavioral.SyntheticRiskFlags}} flag{{if ne .AISensing.Behavioral.SyntheticRiskFlags 1}}s{{end}}
      </span>
      <span style="margin-left:0.5rem;color:var(--text-secondary);">Synthetic Risk</span>
      {{if .AISensing.Behavioral.SyntheticRiskDetails}}
      <div class="synthetic-details">
        <ul>
          {{range .AISensing.Behavioral.SyntheticRiskDetails}}
          <li>{{prettify .}}</li>
          {{end}}
        </ul>
      </div>
      {{end}}
    </div>
  </section>
  {{end}}{{end}}
```

**Step 2: Register template functions**

Find where template functions are registered (where `prettify` and `mul` are defined — likely in `pkg/server/templates.go` or similar). Add these functions:

```go
"velocityLabel": func(v float64) string {
    if v > 5.0 { return "suspicious" }
    if v > 2.0 { return "elevated" }
    return "normal"
},
"velocityClass": func(v float64) string {
    if v > 5.0 { return "interp-red" }
    if v > 2.0 { return "interp-amber" }
    return "interp-green"
},
"hourLabel": func(h int) string {
    if h < 8 { return "narrow" }
    if h > 16 { return "wide" }
    return "typical"
},
"hourClass": func(h int) string {
    if h < 8 { return "interp-amber" }
    if h > 16 { return "interp-amber" }
    return "interp-green"
},
"burstLabel": func(v float64) string {
    if v > 5.0 { return "volatile" }
    if v > 2.0 { return "bursty" }
    return "steady"
},
"burstClass": func(v float64) string {
    if v > 5.0 { return "interp-red" }
    if v > 2.0 { return "interp-amber" }
    return "interp-green"
},
"syntheticClass": func(flags int) string {
    if flags >= 3 { return "badge-red" }
    if flags >= 1 { return "badge-amber" }
    return "badge-green"
},
```

**Step 3: Add CSS styles**

Add to `pkg/server/static/css/app.css`:

```css
/* AI Sensing Section */
.ai-sensing {
  margin-top: 1.5rem;
}

.ai-sensing h2 {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.plan-badge {
  font-size: 0.65rem;
  font-weight: 600;
  padding: 0.15rem 0.5rem;
  border-radius: 9999px;
  background: var(--accent-color, #2f81f7);
  color: #fff;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.metric-cards {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
  margin-top: 1rem;
}

.metric-card {
  background: var(--card-bg, #161b22);
  border: 1px solid var(--border-color, #30363d);
  border-radius: 0.5rem;
  padding: 1rem;
  text-align: center;
}

.metric-value {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--text-primary, #e6edf3);
}

.metric-label {
  font-size: 0.75rem;
  color: var(--text-secondary, #8b949e);
  margin-top: 0.25rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.metric-interp {
  font-size: 0.8rem;
  font-weight: 500;
  margin-top: 0.5rem;
  padding: 0.15rem 0.5rem;
  border-radius: 0.25rem;
  display: inline-block;
}

.interp-green { color: #3fb950; background: rgba(63, 185, 80, 0.1); }
.interp-amber { color: #d29922; background: rgba(210, 153, 34, 0.1); }
.interp-red   { color: #f85149; background: rgba(248, 81, 73, 0.1); }

.synthetic-risk {
  margin-top: 1rem;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
}

.synthetic-badge {
  display: inline-block;
  font-size: 0.8rem;
  font-weight: 600;
  padding: 0.25rem 0.75rem;
  border-radius: 9999px;
  cursor: pointer;
}

.badge-green  { color: #3fb950; background: rgba(63, 185, 80, 0.15); }
.badge-amber  { color: #d29922; background: rgba(210, 153, 34, 0.15); }
.badge-red    { color: #f85149; background: rgba(248, 81, 73, 0.15); }

.synthetic-details {
  display: none;
  width: 100%;
  margin-top: 0.5rem;
}

.synthetic-details.open {
  display: block;
}

.synthetic-details ul {
  list-style: none;
  padding: 0;
  margin: 0;
}

.synthetic-details li {
  font-size: 0.8rem;
  color: var(--text-secondary, #8b949e);
  padding: 0.25rem 0;
}

.synthetic-details li::before {
  content: "⚠ ";
}

@media (max-width: 600px) {
  .metric-cards {
    grid-template-columns: 1fr;
  }
}
```

**Step 4: Verify it compiles and renders**

Run: `go vet ./...`
Run: `make server` (check scorecard renders without errors)

**Step 5: Commit**

```bash
git add pkg/server/templates/scorecard.html pkg/server/static/css/app.css pkg/server/templates.go
git commit -S -m "Add AI Sensing section to scorecard with metric cards and synthetic risk badge"
```

---

## Task 8: Final Verification

**Step 1: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All PASS

**Step 2: Run linter**

Run: `make lint`
Expected: 0 issues

**Step 3: Verify admin endpoints manually**

Run local server: `make server`

Test plan update:
```bash
curl -s -X PUT -H "Authorization: Bearer test-admin-key" -H "Content-Type: application/json" \
  -d '{"plan":"pro"}' http://localhost:8080/api/v1/admin/tenant/TEST_ID/plan
```

Test status update:
```bash
curl -s -X PUT -H "Authorization: Bearer test-admin-key" -H "Content-Type: application/json" \
  -d '{"status":"suspended"}' http://localhost:8080/api/v1/admin/tenant/TEST_ID/status
```

**Step 4: Commit any remaining fixes**

```bash
git add -A
git commit -S -m "Final verification fixes"
```
