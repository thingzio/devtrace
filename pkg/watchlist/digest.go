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
	"fmt"
	"html"
	"strings"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

// Aliases to the canonical postgres event-type constants. Importing
// pkg/data/postgres for these strings would be ergonomically heavy in
// templating contexts, so we re-declare local consts that match.
const (
	eventTypeScoreChange = postgres.EventTypeScoreChange
	detailGradeChanged   = "Grade changed"
	detailNewActivity    = "New activity"
)

// RenderDigest produces HTML and plain-text bodies for a weekly digest email.
// Events are pre-sorted by created_at DESC and capped at the caller's limit.
// unsubURL is the HMAC-signed one-click unsubscribe URL (may be empty if secret is not configured).
func RenderDigest(events []postgres.NotificationEvent, baseURL, unsubURL string) (htmlBody, textBody string) {
	var hb, tb strings.Builder

	// Dark theme matching DevTrace UI: bg=#0c1017, text=#e6edf3, card=#171a22, border=#242836, accent=#4a9eff, muted=#8b949e
	hb.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"></head>`)
	hb.WriteString(`<body style="margin:0;padding:0;background:#0c1017;color:#e6edf3;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">`)
	hb.WriteString(`<div style="max-width:600px;margin:0 auto;padding:24px;">`)
	hb.WriteString(`<div style="text-align:center;margin-bottom:24px;">`)
	hb.WriteString(`<h1 style="color:#e6edf3;font-size:20px;margin:0;">DevTrace Weekly Digest</h1>`)
	hb.WriteString(`<p style="color:#8b949e;font-size:13px;margin:4px 0 0;">Here's what happened in your watched organizations this week.</p>`)
	hb.WriteString(`</div>`)

	tb.WriteString("DevTrace Weekly Digest\n")
	tb.WriteString("======================\n\n")

	hb.WriteString(`<div style="background:#171a22;border:1px solid #242836;border-radius:6px;padding:16px;">`)
	hb.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:13px;color:#e6edf3;">`)
	hb.WriteString(`<tr style="border-bottom:1px solid #242836;text-align:left;">`)
	hb.WriteString(`<th style="padding:8px 12px;color:#8b949e;font-size:12px;">Contributor</th>`)
	hb.WriteString(`<th style="padding:8px 12px;color:#8b949e;font-size:12px;">Org</th>`)
	hb.WriteString(`<th style="padding:8px 12px;color:#8b949e;font-size:12px;">Type</th>`)
	hb.WriteString(`<th style="padding:8px 12px;color:#8b949e;font-size:12px;">Activity</th>`)
	hb.WriteString(`</tr>`)

	for _, ev := range events {
		scoreURL := fmt.Sprintf("%s/score/%s", baseURL, ev.Username)
		escapedUser := html.EscapeString(ev.Username)
		escapedTarget := html.EscapeString(ev.Target)

		typeBadge := "New"
		typeColor := "#3fb950"
		if ev.EventType == eventTypeScoreChange {
			typeBadge = "Grade"
			typeColor = "#d29922"
		}

		detail := formatDetail(ev)

		hb.WriteString(`<tr style="border-bottom:1px solid #242836;">`)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;"><a href="%s" style="color:#4a9eff;text-decoration:none;">%s</a></td>`, scoreURL, escapedUser)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;">%s</td>`, escapedTarget)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;"><span style="background:%s;color:#fff;padding:2px 8px;border-radius:12px;font-size:12px;">%s</span></td>`, typeColor, typeBadge)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;">%s</td>`, html.EscapeString(detail))
		hb.WriteString(`</tr>`)

		fmt.Fprintf(&tb, "- %s (%s) [%s] %s\n  %s\n\n", ev.Username, ev.Target, typeBadge, detail, scoreURL)
	}

	hb.WriteString(`</table></div>`)

	dashboardURL := baseURL + "/dashboard"
	fmt.Fprintf(&hb,
		`<div style="text-align:center;margin-top:24px;">`+
			`<a href="%s" style="display:inline-block;background:#4a9eff;color:#fff;text-decoration:none;`+
			`padding:10px 24px;border-radius:6px;font-size:14px;font-weight:600;">View all activity on your dashboard</a></div>`,
		dashboardURL)
	fmt.Fprintf(&tb, "View all activity: %s\n\n", dashboardURL)

	hb.WriteString(`<div style="text-align:center;margin-top:32px;padding-top:16px;border-top:1px solid #242836;">`)
	hb.WriteString(`<p style="color:#8b949e;font-size:11px;margin:0;">`)
	hb.WriteString(`You're receiving this because you have active watchlists.<br>`)
	if unsubURL != "" {
		fmt.Fprintf(&hb, `<a href="%s" style="color:#8b949e;text-decoration:underline;">Unsubscribe</a>`, unsubURL)
		fmt.Fprintf(&tb, "---\nUnsubscribe: %s\n", unsubURL)
	} else {
		settingsURL := baseURL + "/settings"
		fmt.Fprintf(&hb, `<a href="%s" style="color:#8b949e;text-decoration:underline;">Manage watchlists</a>`, settingsURL)
		fmt.Fprintf(&tb, "---\nManage watchlists: %s\n", settingsURL)
	}
	hb.WriteString(`</p></div>`)

	hb.WriteString(`</div></body></html>`)

	return hb.String(), tb.String()
}

// formatDetail returns a human-readable summary of the event details.
func formatDetail(ev postgres.NotificationEvent) string {
	if ev.EventType == eventTypeScoreChange {
		oldGrade, _ := ev.Details["old_grade"].(string)
		newGrade, _ := ev.Details["new_grade"].(string)
		if oldGrade != "" && newGrade != "" {
			return fmt.Sprintf("%s → %s", oldGrade, newGrade)
		}
		return detailGradeChanged
	}

	var parts []string
	if v, ok := numFromDetails(ev.Details, "prs_opened"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("%d PRs opened", v))
	}
	if v, ok := numFromDetails(ev.Details, "prs_merged"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("%d merged", v))
	}
	if v, ok := numFromDetails(ev.Details, "reviews_given"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("%d reviews", v))
	}
	if v, ok := numFromDetails(ev.Details, "issues_opened"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("%d issues", v))
	}
	if v, ok := numFromDetails(ev.Details, "issue_comments"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("%d comments", v))
	}
	if len(parts) == 0 {
		return detailNewActivity
	}
	return strings.Join(parts, ", ")
}

func numFromDetails(d map[string]any, key string) (int, bool) {
	v, ok := d[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}
