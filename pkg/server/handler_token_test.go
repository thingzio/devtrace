package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestCreateTokenHandlerNoAuth(t *testing.T) {
	handler := createTokenHandler(nil)

	body := bytes.NewBufferString(`{"name":"ci-token"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/token", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "unauthorized" {
		t.Errorf("expected 'unauthorized' error, got %q", resp["error"])
	}
}

func TestCreateTokenHandlerBadRequest(t *testing.T) {
	tn := &tenant.Tenant{ID: "t-123", Username: "testuser", Plan: "free"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	// Empty name
	handler := createTokenHandler(nil)
	body := bytes.NewBufferString(`{"name":""}`)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/token", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTokensHandlerNoAuth(t *testing.T) {
	handler := listTokensHandler(nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/token", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeTokenHandlerNoAuth(t *testing.T) {
	handler := revokeTokenHandler(nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/token/tok-1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeTokenHandlerMissingID(t *testing.T) {
	tn := &tenant.Tenant{ID: "t-123", Username: "testuser", Plan: "free"}
	ctx := middleware.WithTenantContext(context.Background(), tn)

	handler := revokeTokenHandler(nil)

	// PathValue returns "" when not routed through mux
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/api/v1/token/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
