package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseEventFilter_DefaultsToPageOne(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard/events.json", nil)
	filter, page := parseEventFilter(r)
	if page != 1 {
		t.Errorf("default page = %d, want 1", page)
	}
	if filter.Contributor != "" || filter.Org != "" || filter.Repo != "" || !filter.Since.IsZero() {
		t.Errorf("expected zero-value filter, got %+v", filter)
	}
}

func TestParseEventFilter_CapsLongStrings(t *testing.T) {
	long := strings.Repeat("a", 200)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard/events.json?contributor="+long+"&org="+long+"&repo="+long, nil)
	filter, _ := parseEventFilter(r)

	if len(filter.Contributor) != maxFilterLen {
		t.Errorf("Contributor len = %d, want %d", len(filter.Contributor), maxFilterLen)
	}
	if len(filter.Org) != maxFilterLen {
		t.Errorf("Org len = %d, want %d", len(filter.Org), maxFilterLen)
	}
	if len(filter.Repo) != maxFilterLen {
		t.Errorf("Repo len = %d, want %d", len(filter.Repo), maxFilterLen)
	}
}

func TestParseEventFilter_AcceptsRFC3339Since(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard/events.json?since=2026-04-01T12:00:00Z", nil)
	filter, _ := parseEventFilter(r)
	want := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	if !filter.Since.Equal(want) {
		t.Errorf("Since = %v, want %v", filter.Since, want)
	}
}

func TestParseEventFilter_AcceptsBareDateSince(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard/events.json?since=2026-04-01", nil)
	filter, _ := parseEventFilter(r)
	want := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if !filter.Since.Equal(want) {
		t.Errorf("Since = %v, want %v", filter.Since, want)
	}
}

func TestParseEventFilter_RejectsGarbageSince(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard/events.json?since=notatime", nil)
	filter, _ := parseEventFilter(r)
	if !filter.Since.IsZero() {
		t.Errorf("expected zero Since for invalid input, got %v", filter.Since)
	}
}

func TestParseEventFilter_TrimsWhitespace(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard/events.json?contributor=%20%20alice%20%20", nil)
	filter, _ := parseEventFilter(r)
	if filter.Contributor != "alice" {
		t.Errorf("Contributor = %q, want %q", filter.Contributor, "alice")
	}
}

func TestParseEventFilter_PageDefaultsForInvalid(t *testing.T) {
	for _, qs := range []string{"page=0", "page=-1", "page=abc"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/dashboard/events.json?"+qs, nil)
		_, page := parseEventFilter(r)
		if page != 1 {
			t.Errorf("query %q → page = %d, want 1", qs, page)
		}
	}
}

func TestDashboardEventsHandler_NoTenant401(t *testing.T) {
	h := dashboardEventsHandler(nil)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard/events.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v (body=%q)", err, w.Body.String())
	}
	if body[tmplErrorKey] != "unauthorized" {
		t.Errorf("error = %q, want unauthorized", body[tmplErrorKey])
	}
}
