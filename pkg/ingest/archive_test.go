package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseEvent(t *testing.T) {
	tests := []struct {
		name   string
		json   string
		evType string
		action string
		actor  string
	}{
		{
			name:   "PullRequestEvent",
			json:   `{"type":"PullRequestEvent","actor":{"login":"alice"},"repo":{"name":"org/repo"},"payload":{"action":"opened"},"created_at":"2026-01-01T00:00:00Z"}`,
			evType: "PullRequestEvent",
			action: "opened",
			actor:  "alice",
		},
		{
			name:   "PullRequestReviewEvent",
			json:   `{"type":"PullRequestReviewEvent","actor":{"login":"bob"},"repo":{"name":"org/repo"},"payload":{"action":"submitted"},"created_at":"2026-01-01T00:00:00Z"}`,
			evType: "PullRequestReviewEvent",
			action: "submitted",
			actor:  "bob",
		},
		{
			name:   "IssueCommentEvent",
			json:   `{"type":"IssueCommentEvent","actor":{"login":"carol"},"repo":{"name":"org/repo"},"payload":{"action":"created"},"created_at":"2026-01-01T00:00:00Z"}`,
			evType: "IssueCommentEvent",
			action: "created",
			actor:  "carol",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := parseEvent([]byte(tt.json))
			if !ok {
				t.Fatal("expected ok=true")
			}
			if ev.Type != tt.evType {
				t.Errorf("type: got %q, want %q", ev.Type, tt.evType)
			}
			if ev.Action != tt.action {
				t.Errorf("action: got %q, want %q", ev.Action, tt.action)
			}
			if ev.Actor != tt.actor {
				t.Errorf("actor: got %q, want %q", ev.Actor, tt.actor)
			}
			if ev.Repo != "org/repo" {
				t.Errorf("repo: got %q, want %q", ev.Repo, "org/repo")
			}
			want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if !ev.CreatedAt.Equal(want) {
				t.Errorf("created_at: got %v, want %v", ev.CreatedAt, want)
			}
		})
	}
}

func TestParseEventFiltered(t *testing.T) {
	filtered := []string{
		`{"type":"WatchEvent","actor":{"login":"alice"},"repo":{"name":"org/repo"},"payload":{},"created_at":"2026-01-01T00:00:00Z"}`,
		`{"type":"ForkEvent","actor":{"login":"alice"},"repo":{"name":"org/repo"},"payload":{},"created_at":"2026-01-01T00:00:00Z"}`,
	}
	for _, line := range filtered {
		if _, ok := parseEvent([]byte(line)); ok {
			t.Errorf("expected filtered out: %s", line)
		}
	}
}

func TestParseEventInvalid(t *testing.T) {
	if _, ok := parseEvent([]byte(`{invalid json`)); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

func TestArchiveReaderURL(t *testing.T) {
	r := NewArchiveReader("https://example.com")
	got := r.URL(time.Date(2026, 3, 15, 8, 0, 0, 0, time.UTC))
	want := "https://example.com/2026-03-15-8.json.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStream(t *testing.T) {
	lines := []string{
		`{"type":"PullRequestEvent","actor":{"login":"alice"},"repo":{"name":"org/repo"},"payload":{"action":"opened"},"created_at":"2026-01-01T00:00:00Z"}`,
		`{"type":"PullRequestEvent","actor":{"login":"bob"},"repo":{"name":"org/repo"},"payload":{"action":"closed"},"created_at":"2026-01-01T01:00:00Z"}`,
		`{"type":"PullRequestReviewEvent","actor":{"login":"carol"},"repo":{"name":"org/repo"},"payload":{"action":"submitted"},"created_at":"2026-01-01T02:00:00Z"}`,
		`{"type":"IssueCommentEvent","actor":{"login":"dave"},"repo":{"name":"org/repo"},"payload":{"action":"created"},"created_at":"2026-01-01T03:00:00Z"}`,
		`{"type":"WatchEvent","actor":{"login":"eve"},"repo":{"name":"org/repo"},"payload":{"action":"started"},"created_at":"2026-01-01T04:00:00Z"}`,
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	for _, l := range lines {
		fmt.Fprintln(gw, l)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	reader := NewArchiveReader(srv.URL)
	var events []Event
	err := reader.Stream(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), func(ev Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}

	checks := []struct {
		typ, action, actor string
	}{
		{"PullRequestEvent", "opened", "alice"},
		{"PullRequestEvent", "closed", "bob"},
		{"PullRequestReviewEvent", "submitted", "carol"},
		{"IssueCommentEvent", "created", "dave"},
	}
	for i, c := range checks {
		ev := events[i]
		if ev.Type != c.typ {
			t.Errorf("event[%d] type: got %q, want %q", i, ev.Type, c.typ)
		}
		if ev.Action != c.action {
			t.Errorf("event[%d] action: got %q, want %q", i, ev.Action, c.action)
		}
		if ev.Actor != c.actor {
			t.Errorf("event[%d] actor: got %q, want %q", i, ev.Actor, c.actor)
		}
		if ev.Repo != "org/repo" {
			t.Errorf("event[%d] repo: got %q, want %q", i, ev.Repo, "org/repo")
		}
	}
}
