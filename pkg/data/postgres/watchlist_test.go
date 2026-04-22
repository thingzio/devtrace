package postgres

import "testing"

func TestNotificationEventDetailSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event NotificationEvent
		want  string
	}{
		{
			"score_change",
			NotificationEvent{EventType: "score_change", Details: map[string]any{"old_grade": "C", "new_grade": "B"}},
			"C \u2192 B",
		},
		{
			"score_change_empty",
			NotificationEvent{EventType: "score_change", Details: map[string]any{}},
			"",
		},
		{
			"new_contributor_prs",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(3), "prs_merged": float64(1)}},
			"3 PRs opened, 1 PRs merged",
		},
		{
			"new_contributor_reviews",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"reviews": float64(5)}},
			"5 reviews",
		},
		{
			"new_contributor_empty",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{}},
			"",
		},
		{
			"nil_details",
			NotificationEvent{EventType: "new_contributor"},
			"",
		},
		{
			"unknown_type",
			NotificationEvent{EventType: "unknown", Details: map[string]any{"foo": "bar"}},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.event.DetailSummary()
			if got != tc.want {
				t.Errorf("DetailSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIntDetail(t *testing.T) {
	t.Parallel()
	d := map[string]any{
		"float": float64(42),
		"int":   7,
		"int64": int64(99),
		"str":   "nope",
	}

	cases := []struct {
		key  string
		want int
	}{
		{"float", 42},
		{"int", 7},
		{"int64", 99},
		{"str", 0},
		{"missing", 0},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := intDetail(d, tc.key)
			if got != tc.want {
				t.Errorf("intDetail(%q) = %d, want %d", tc.key, got, tc.want)
			}
		})
	}
}
