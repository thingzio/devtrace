package watchlist

import (
	"fmt"
	"html"
	"strings"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

const eventTypeScoreChange = "score_change"

// RenderDigest produces HTML and plain-text bodies for a weekly digest email.
// Events are pre-sorted by created_at DESC and capped at the caller's limit.
func RenderDigest(events []postgres.NotificationEvent, baseURL string) (htmlBody, textBody string) {
	var hb, tb strings.Builder

	hb.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"></head>`)
	hb.WriteString(`<body style="font-family:-apple-system,BlinkMacSystemFont,`)
	hb.WriteString(`'Segoe UI',Roboto,sans-serif;color:#24292f;max-width:600px;margin:0 auto;padding:20px;">`)
	hb.WriteString(`<h2 style="margin:0 0 16px 0;font-size:20px;">DevTrace Weekly Digest</h2>`)
	hb.WriteString(`<p style="color:#57606a;margin:0 0 16px 0;">Here's what happened in your watched organizations this week.</p>`)

	tb.WriteString("DevTrace Weekly Digest\n")
	tb.WriteString("======================\n\n")

	hb.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:14px;">`)
	hb.WriteString(`<tr style="border-bottom:2px solid #d0d7de;text-align:left;">`)
	hb.WriteString(`<th style="padding:8px 12px;">Contributor</th>`)
	hb.WriteString(`<th style="padding:8px 12px;">Org</th>`)
	hb.WriteString(`<th style="padding:8px 12px;">Type</th>`)
	hb.WriteString(`<th style="padding:8px 12px;">Activity</th>`)
	hb.WriteString(`</tr>`)

	for _, ev := range events {
		scoreURL := fmt.Sprintf("%s/score/%s", baseURL, ev.Username)
		escapedUser := html.EscapeString(ev.Username)
		escapedTarget := html.EscapeString(ev.Target)

		typeBadge := "New"
		typeColor := "#1a7f37"
		if ev.EventType == eventTypeScoreChange {
			typeBadge = "Grade"
			typeColor = "#9a6700"
		}

		detail := formatDetail(ev)

		hb.WriteString(`<tr style="border-bottom:1px solid #d0d7de;">`)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;"><a href="%s" style="color:#0969da;text-decoration:none;">%s</a></td>`, scoreURL, escapedUser)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;">%s</td>`, escapedTarget)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;"><span style="background:%s;color:#fff;padding:2px 8px;border-radius:12px;font-size:12px;">%s</span></td>`, typeColor, typeBadge)
		fmt.Fprintf(&hb, `<td style="padding:8px 12px;">%s</td>`, html.EscapeString(detail))
		hb.WriteString(`</tr>`)

		fmt.Fprintf(&tb, "- %s (%s) [%s] %s\n  %s\n\n", ev.Username, ev.Target, typeBadge, detail, scoreURL)
	}

	hb.WriteString(`</table>`)

	dashboardURL := baseURL + "/dashboard"
	fmt.Fprintf(&hb,
		`<p style="margin:20px 0;"><a href="%s" `+
			`style="display:inline-block;background:#0969da;color:#fff;`+
			`padding:10px 20px;border-radius:6px;text-decoration:none;font-size:14px;">`+
			`View all activity on your dashboard</a></p>`, dashboardURL)
	fmt.Fprintf(&tb, "View all activity: %s\n\n", dashboardURL)

	settingsURL := baseURL + "/settings"
	fmt.Fprintf(&hb,
		`<hr style="border:none;border-top:1px solid #d0d7de;margin:24px 0;">`+
			`<p style="color:#57606a;font-size:12px;">`+
			`You're receiving this because you have active watchlists. `+
			`<a href="%s" style="color:#0969da;">Manage watchlists</a></p>`,
		settingsURL)
	fmt.Fprintf(&tb, "---\nManage watchlists: %s\n", settingsURL)

	hb.WriteString(`</body></html>`)

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
		return "Grade changed"
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
		return "New activity"
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
