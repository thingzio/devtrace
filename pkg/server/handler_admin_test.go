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
