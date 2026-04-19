package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func adminCtx() context.Context {
	tn := &tenant.Tenant{ID: "t-1", Username: "admin-user", Status: "active"}
	return middleware.WithTenantContext(context.Background(), tn)
}

func TestAdminDashboardHandler_NoTenant(t *testing.T) {
	handler := adminDashboardHandler(nil, Options{Version: "test"})

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAdminDashboardHandler_WithTenant(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "admin-user")
	tn := &tenant.Tenant{ID: "t-1", Username: "admin-user", Status: "active"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	handler := adminDashboardHandler(nil, Options{Version: "test"})

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// With nil store, template rendering may fail but handler should not panic
	// and should attempt to render (status 200 or 500, not 404)
	if w.Code == http.StatusNotFound {
		t.Error("authenticated admin should not get 404")
	}
}

func TestAdminTokensHandler_NoTenant(t *testing.T) {
	pool := ghclient.NewTokenPool("tok1")
	handler := adminTokensHandler(pool, Options{Version: "test"})

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/tokens", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAdminTenantsHandler_NoTenant(t *testing.T) {
	handler := adminTenantsHandler(nil, Options{Version: "test"})

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/tenants", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestTimeSince(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "never"},
		{"recent", time.Now().Add(-30 * time.Second), "just now"},
		{"minutes", time.Now().Add(-15 * time.Minute), "15m ago"},
		{"hours", time.Now().Add(-3 * time.Hour), "3h ago"},
		{"days", time.Now().Add(-48 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timeSince(tt.t); got != tt.want {
				t.Errorf("timeSince() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuditLog(t *testing.T) {
	// Verify it doesn't panic with nil tenant
	auditLog("test_action", nil, "/admin", "127.0.0.1", "detail")

	// Verify with real tenant
	tn := &tenant.Tenant{Username: "alice"}
	auditLog("test_action", tn, "/admin", "127.0.0.1", "detail")
}

func TestAdminTokensHandler_WithTenant(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "admin-user")

	pool := ghclient.NewTokenPool() // empty pool, no HTTP calls
	handler := adminTokensHandler(pool, Options{Version: "test"})

	r := httptest.NewRequestWithContext(adminCtx(), http.MethodGet, "/admin/tokens", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code == http.StatusNotFound {
		t.Error("authenticated admin should not get 404")
	}
}

func TestAdminTokensHandler_NilPool(t *testing.T) {
	t.Setenv("DEVTRACE_ADMIN_USERS", "admin-user")

	handler := adminTokensHandler(nil, Options{Version: "test"})

	r := httptest.NewRequestWithContext(adminCtx(), http.MethodGet, "/admin/tokens", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code == http.StatusNotFound {
		t.Error("authenticated admin should not get 404 even with nil pool")
	}
}

func TestAdminTokenQuotaHistoryHandler_NoTenant(t *testing.T) {
	handler := adminTokenQuotaHistoryHandler(nil, Options{Version: "test"})

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/tokens/quota-history", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestLoadPoolQuotas_EmptyPool(t *testing.T) {
	pool := ghclient.NewTokenPool() // empty
	data := make(map[string]any)
	loadPoolQuotas(context.Background(), pool, data)

	if data["PoolTotal"] != 0 {
		t.Errorf("PoolTotal = %v, want 0", data["PoolTotal"])
	}
	if data["TotalLimit"] != 0 {
		t.Errorf("TotalLimit = %v, want 0", data["TotalLimit"])
	}
	if data["TotalUsed"] != 0 {
		t.Errorf("TotalUsed = %v, want 0", data["TotalUsed"])
	}
	if data["TotalAvailable"] != 0 {
		t.Errorf("TotalAvailable = %v, want 0", data["TotalAvailable"])
	}
	if data["UtilizationPct"] != 0 {
		t.Errorf("UtilizationPct = %v, want 0", data["UtilizationPct"])
	}
}

func TestLoadNoInstallTenants_NilDB(t *testing.T) {
	// Should not panic with nil db — loadNoInstallTenants is guarded by
	// a nil check in the handler, but test the function directly is safe
	// to call only with a real db. Verify the handler path instead.
	t.Setenv("DEVTRACE_ADMIN_USERS", "admin-user")

	handler := adminTokensHandler(nil, Options{Version: "test"})
	r := httptest.NewRequestWithContext(adminCtx(), http.MethodGet, "/admin/tokens", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// Should not panic, and not 404.
	if w.Code == http.StatusNotFound {
		t.Error("should not return 404 for authenticated admin")
	}
}

func TestTokenQuotaRow_Fields(t *testing.T) {
	t.Parallel()
	row := tokenQuotaRow{
		Index:          0,
		Label:          "org-a",
		InstallationID: 12345,
		Limit:          5000,
		Used:           1000,
		Remaining:      4000,
		Percent:        80,
		Reset:          "2026-04-18T12:00:00Z",
	}
	if row.InstallationID != 12345 {
		t.Errorf("InstallationID = %d, want 12345", row.InstallationID)
	}
	if row.Used != 1000 {
		t.Errorf("Used = %d, want 1000", row.Used)
	}
}
