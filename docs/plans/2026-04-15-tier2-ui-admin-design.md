# Tier 2 UI + Admin Plan/Status Management — Design

> Approved 2026-04-15.

## Overview

Two features:
1. **Scorecard UI** — render AI Sensing Tier 2 heuristics for Pro plan users
2. **Admin API** — plan and status management via protected endpoints

---

## Feature 1: Scorecard AI Sensing Section

New section on the scorecard page, rendered after category bars and before the signals table. Only appears when `AISensing.Behavioral` is present (Pro plan — backend already strips for other plans).

### Layout

```
┌─ AI Sensing (Pro) ────────────────────────────────────┐
│                                                        │
│  Velocity Anomaly    Active Hours    Burst-Vanish      │
│       1.25×              14              0.85          │
│     normal            typical           steady         │
│                                                        │
│  Synthetic Risk: ● 0 flags                             │
│                                                        │
└────────────────────────────────────────────────────────┘
```

### Metric Cards

Three cards in a row, each showing numeric value + interpretation label:

| Metric | Thresholds |
|--------|-----------|
| Velocity Anomaly | <2.0 "normal", 2-5 "elevated", >5 "suspicious" |
| Active Hours | 1-7 "narrow", 8-16 "typical", 17+ "wide" |
| Burst-Vanish | <2.0 "steady", 2-5 "bursty", >5 "volatile" |

### Synthetic Risk Badge

Colored badge: green (0 flags), amber (1-2 flags), red (3+ flags). Clicking expands to show flag names as a list.

### Template Data

Add `"AISensing": resp.AISensing` to the handler's template data map in `handler_pages.go`. Template checks `{{if .AISensing}}{{if .AISensing.Behavioral}}` — no plan logic in template.

### Files

| File | Change |
|------|--------|
| `pkg/server/templates/scorecard.html` | Add AI Sensing section |
| `pkg/server/static/css/app.css` | Add `.ai-sensing`, `.metric-card`, `.synthetic-badge` styles |
| `pkg/server/handler_pages.go` | Pass `AISensing` to template data |

---

## Feature 2: Admin Plan Management API

### Endpoint

`PUT /api/v1/admin/tenant/{id}/plan`

**Auth:** `Authorization: Bearer <DEVTRACE_ADMIN_API_KEY>` (env var, constant-time comparison).

**Request:**
```json
{"plan": "pro"}
```

**Validation:** Must be `free`, `starter`, or `pro`.

**Response (200):**
```json
{
  "tenant_id": "uuid",
  "username": "octocat",
  "plan": "pro",
  "max_contributors": 2000,
  "updated_at": "2026-04-15T..."
}
```

Updates both `plan` and `max_contributors` (from `plan.Get()`) in `devtrace_tenant`.

**Makefile:**
```makefile
set-plan:
	@curl -s -X PUT \
		-H "Authorization: Bearer $(DEVTRACE_ADMIN_API_KEY)" \
		-H "Content-Type: application/json" \
		-d '{"plan":"$(PLAN)"}' \
		$(API_URL)/api/v1/admin/tenant/$(TENANT)/plan | jq .
```

---

## Feature 3: Admin Tenant Status API

### Schema Change

New migration `002_tenant_status.sql`:
```sql
ALTER TABLE devtrace_tenant ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
```

### Status Constants

In `pkg/tenant/tenant.go`:
```go
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)
```

All code references these constants — no string literals for status values.

### Endpoint

`PUT /api/v1/admin/tenant/{id}/status`

**Auth:** Same `DEVTRACE_ADMIN_API_KEY`.

**Request:**
```json
{"status": "suspended"}
```

**Validation:** Must be `StatusActive` or `StatusSuspended`.

**Response (200):**
```json
{
  "tenant_id": "uuid",
  "username": "octocat",
  "status": "suspended",
  "updated_at": "2026-04-15T..."
}
```

### Enforcement

- **API tokens:** In auth middleware (`pkg/middleware/auth.go`), after `ValidateAPIToken` loads the tenant, check `tenant.Status == StatusSuspended`. If so, return `403 {"error": "account suspended"}`.
- **UI sessions:** In session middleware or scorecard handler, check tenant status. Show a "your account has been suspended" banner or redirect.

**Makefile:**
```makefile
set-status:
	@curl -s -X PUT \
		-H "Authorization: Bearer $(DEVTRACE_ADMIN_API_KEY)" \
		-H "Content-Type: application/json" \
		-d '{"status":"$(STATUS)"}' \
		$(API_URL)/api/v1/admin/tenant/$(TENANT)/status | jq .
```

---

## Files Touched (All Features)

| File | Change |
|------|--------|
| `pkg/server/templates/scorecard.html` | AI Sensing section (Pro only) |
| `pkg/server/static/css/app.css` | AI Sensing styles |
| `pkg/server/handler_pages.go` | Pass AISensing to template |
| `pkg/server/handler_admin.go` | New: plan + status handlers |
| `pkg/server/server.go` | Register admin routes |
| `pkg/data/postgres/tenant.go` | `UpdateTenantPlan`, `UpdateTenantStatus` queries |
| `pkg/data/postgres/sql/migrations/002_tenant_status.sql` | Add status column |
| `pkg/tenant/tenant.go` | `Status` field, `StatusActive`/`StatusSuspended` constants |
| `pkg/middleware/auth.go` | Suspend check on token validation |
| `Makefile` | `set-plan`, `set-status` targets |
