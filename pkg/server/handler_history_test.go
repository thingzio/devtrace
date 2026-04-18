package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHistoryHandlerInvalidUsername(t *testing.T) {
	handler := historyHandler(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/score/{username}/history", handler)

	tests := []struct {
		name     string
		username string
	}{
		{"starts with hyphen", "-invalid"},
		{"ends with hyphen", "invalid-"},
		{"contains underscore", "user_name"},
		{"contains dot", "user.name"},
		{"too long", strings.Repeat("a", 40)},
		{"single hyphen", "-"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := "/api/v1/score/" + tc.username + "/history"
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp["error"] != "invalid username" {
				t.Errorf("expected 'invalid username' error, got %q", resp["error"])
			}
		})
	}
}
