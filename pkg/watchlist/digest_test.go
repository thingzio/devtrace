// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package watchlist

import (
	"strings"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func TestRenderDigestEmpty(t *testing.T) {
	t.Parallel()
	html, text := RenderDigest(nil, "https://example.com", "")
	if !strings.Contains(html, "DevTrace Weekly Digest") {
		t.Error("HTML should contain digest header")
	}
	if !strings.Contains(text, "DevTrace Weekly Digest") {
		t.Error("text should contain digest header")
	}
	if !strings.Contains(html, "https://example.com/dashboard") {
		t.Error("HTML should contain dashboard link")
	}
	if !strings.Contains(html, "https://example.com/settings") {
		t.Error("HTML should contain settings link")
	}
}

func TestRenderDigestNewContributor(t *testing.T) {
	t.Parallel()
	events := []postgres.NotificationEvent{
		{
			ID:        1,
			EventType: "new_contributor",
			Username:  "alice",
			Target:    "kubernetes",
			Details:   map[string]any{"prs_opened": float64(3), "prs_merged": float64(1)},
			CreatedAt: time.Now(),
		},
	}

	html, text := RenderDigest(events, "https://devtrace.example.com", "")

	if !strings.Contains(html, "alice") {
		t.Error("HTML should contain username")
	}
	if !strings.Contains(html, "kubernetes") {
		t.Error("HTML should contain target org")
	}
	if !strings.Contains(html, "New") {
		t.Error("HTML should contain 'New' badge for new_contributor")
	}
	if !strings.Contains(html, "3 PRs opened") {
		t.Error("HTML should contain PR details")
	}
	if !strings.Contains(html, "/score/alice") {
		t.Error("HTML should contain score link")
	}

	if !strings.Contains(text, "alice") {
		t.Error("text should contain username")
	}
	if !strings.Contains(text, "[New]") {
		t.Error("text should contain [New] badge")
	}
}

func TestRenderDigestScoreChange(t *testing.T) {
	t.Parallel()
	events := []postgres.NotificationEvent{
		{
			ID:        2,
			EventType: "score_change",
			Username:  "bob",
			Target:    "myorg",
			Details:   map[string]any{"old_grade": "C", "new_grade": "B"},
			CreatedAt: time.Now(),
		},
	}

	html, text := RenderDigest(events, "https://dt.io", "")

	if !strings.Contains(html, "Grade") {
		t.Error("HTML should contain 'Grade' badge for score_change")
	}
	if !strings.Contains(html, "C → B") || !strings.Contains(html, "C →") {
		t.Error("HTML should contain grade change detail")
	}
	if !strings.Contains(text, "[Grade]") {
		t.Error("text should contain [Grade] badge")
	}
}

func TestRenderDigestMultipleEvents(t *testing.T) {
	t.Parallel()
	events := []postgres.NotificationEvent{
		{ID: 1, EventType: "new_contributor", Username: "alice", Target: "org1", Details: map[string]any{"prs_opened": float64(2)}, CreatedAt: time.Now()},
		{ID: 2, EventType: "score_change", Username: "bob", Target: "org2", Details: map[string]any{"old_grade": "D", "new_grade": "C"}, CreatedAt: time.Now()},
		{ID: 3, EventType: "new_contributor", Username: "carol", Target: "org1", Details: map[string]any{}, CreatedAt: time.Now()},
	}

	html, text := RenderDigest(events, "https://dt.io", "")

	for _, name := range []string{"alice", "bob", "carol"} {
		if !strings.Contains(html, name) {
			t.Errorf("HTML missing username %q", name)
		}
		if !strings.Contains(text, name) {
			t.Errorf("text missing username %q", name)
		}
	}
}

func TestFormatDetailScoreChange(t *testing.T) {
	t.Parallel()
	ev := postgres.NotificationEvent{
		EventType: "score_change",
		Details:   map[string]any{"old_grade": "B", "new_grade": "A"},
	}
	got := formatDetail(ev)
	if got != "B → A" {
		t.Errorf("formatDetail = %q, want %q", got, "B → A")
	}
}

func TestFormatDetailScoreChangeEmpty(t *testing.T) {
	t.Parallel()
	ev := postgres.NotificationEvent{
		EventType: "score_change",
		Details:   map[string]any{},
	}
	got := formatDetail(ev)
	if got != "Grade changed" {
		t.Errorf("formatDetail = %q, want %q", got, "Grade changed")
	}
}

func TestFormatDetailNewContributor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		details map[string]any
		want    string
	}{
		{"prs_only", map[string]any{"prs_opened": float64(5), "prs_merged": float64(2)}, "5 PRs opened, 2 merged"},
		{"reviews", map[string]any{"reviews_given": float64(3)}, "3 reviews"},
		{"issues", map[string]any{"issues_opened": float64(1), "issue_comments": float64(4)}, "1 issues, 4 comments"},
		{"empty", map[string]any{}, "New activity"},
		{"nil", nil, "New activity"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := postgres.NotificationEvent{
				EventType: "new_contributor",
				Details:   tc.details,
			}
			got := formatDetail(ev)
			if got != tc.want {
				t.Errorf("formatDetail = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNumFromDetails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		val    any
		want   int
		wantOK bool
	}{
		{"float64", float64(42), 42, true},
		{"int", 7, 7, true},
		{"int64", int64(99), 99, true},
		{"string", "nope", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := map[string]any{"key": tc.val}
			got, ok := numFromDetails(d, "key")
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("numFromDetails = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}

	t.Run("missing_key", func(t *testing.T) {
		_, ok := numFromDetails(map[string]any{}, "missing")
		if ok {
			t.Error("expected false for missing key")
		}
	})
}

func TestRenderDigestWithUnsubURL(t *testing.T) {
	t.Parallel()
	events := []postgres.NotificationEvent{
		{ID: 1, EventType: "new_contributor", Username: "alice", Target: "org1",
			Details: map[string]any{"prs_opened": float64(1)}, CreatedAt: time.Now()},
	}

	unsubURL := "https://dt.io/digest/unsubscribe?tenant=123&token=abc"
	html, text := RenderDigest(events, "https://dt.io", unsubURL)

	if !strings.Contains(html, unsubURL) {
		t.Error("HTML should contain unsubscribe URL")
	}
	if !strings.Contains(html, "Unsubscribe") {
		t.Error("HTML should contain Unsubscribe link text")
	}
	if strings.Contains(html, "/settings") {
		t.Error("HTML should NOT contain settings link when unsubURL is provided")
	}
	if !strings.Contains(text, unsubURL) {
		t.Error("text should contain unsubscribe URL")
	}
}

func TestUnsubscribeToken(t *testing.T) {
	t.Parallel()
	token := UnsubscribeToken("my-secret", "tenant-123")
	if token == "" {
		t.Fatal("token should not be empty")
	}
	if len(token) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("token length = %d, want 64", len(token))
	}
}

func TestValidateUnsubscribeToken(t *testing.T) {
	t.Parallel()
	secret := "test-secret"
	tenantID := "tenant-456"
	token := UnsubscribeToken(secret, tenantID)

	if !ValidateUnsubscribeToken(secret, tenantID, token) {
		t.Error("valid token should pass validation")
	}
	if ValidateUnsubscribeToken(secret, tenantID, "bad-token") {
		t.Error("invalid token should fail validation")
	}
	if ValidateUnsubscribeToken("wrong-secret", tenantID, token) {
		t.Error("wrong secret should fail validation")
	}
	if ValidateUnsubscribeToken(secret, "wrong-tenant", token) {
		t.Error("wrong tenant should fail validation")
	}
}

func TestHMACSecret(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "test-hmac-key")
	if got := HMACSecret(); got != "test-hmac-key" {
		t.Errorf("HMACSecret = %q, want %q", got, "test-hmac-key")
	}

	t.Setenv("DIGEST_HMAC_SECRET", "")
	if got := HMACSecret(); got != "" {
		t.Errorf("HMACSecret = %q, want empty", got)
	}
}

func TestRenderDigestHTMLEscaping(t *testing.T) {
	t.Parallel()
	events := []postgres.NotificationEvent{
		{
			ID:        1,
			EventType: "new_contributor",
			Username:  "user<script>",
			Target:    "org&evil",
			Details:   map[string]any{"prs_opened": float64(1)},
			CreatedAt: time.Now(),
		},
	}

	htmlBody, _ := RenderDigest(events, "https://dt.io", "")

	// The displayed username and target are HTML-escaped.
	if !strings.Contains(htmlBody, "user&lt;script&gt;") {
		t.Error("HTML should contain escaped username in display text")
	}
	if !strings.Contains(htmlBody, "org&amp;evil") {
		t.Error("HTML should contain escaped target")
	}
}
