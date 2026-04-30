package postgres

import "testing"

func TestNotificationEventDetailSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ev   NotificationEvent
		want string
	}{
		{
			"score_change with grades",
			NotificationEvent{EventType: "score_change", Details: map[string]any{"old_grade": "C", "new_grade": "B"}},
			"grade C → B",
		},
		{
			"score_change missing grades",
			NotificationEvent{EventType: "score_change", Details: map[string]any{}},
			"grade changed",
		},
		{
			"new_contributor opened single PR",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(1)}},
			"opened 1 PR",
		},
		{
			"new_contributor opened multiple PRs",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(3)}},
			"opened 3 PRs",
		},
		{
			"new_contributor PRs and reviews",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_opened": float64(2), "reviews_given": float64(5)}},
			"opened 2 PRs, 5 reviews",
		},
		{
			"new_contributor merged PRs only",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"prs_merged": float64(1)}},
			"merged 1 PR",
		},
		{
			"new_contributor empty details",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{}},
			"first activity",
		},
		{
			"new_contributor only repos in details",
			NotificationEvent{EventType: "new_contributor", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples"}}},
			"first activity",
		},
		{
			"unknown event type",
			NotificationEvent{EventType: "weird", Details: map[string]any{"foo": "bar"}},
			"weird",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.ev.DetailSummary()
			if got != tc.want {
				t.Errorf("DetailSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNotificationEventRepoSummary(t *testing.T) {
	cases := []struct {
		name        string
		ev          NotificationEvent
		wantSummary string
		wantTooltip string
	}{
		{
			"no details",
			NotificationEvent{Target: "NVIDIA"},
			"",
			"",
		},
		{
			"empty repos array",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{}}},
			"",
			"",
		},
		{
			"single repo strips org prefix",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples"}}},
			"cuda-samples",
			"NVIDIA/cuda-samples",
		},
		{
			"single repo no prefix",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{"cuda-samples"}}},
			"cuda-samples",
			"cuda-samples",
		},
		{
			"single repo target is org/repo",
			NotificationEvent{Target: "NVIDIA/cuda-samples", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples"}}},
			"cuda-samples",
			"NVIDIA/cuda-samples",
		},
		{
			"multiple repos shows first plus count",
			NotificationEvent{Target: "NVIDIA", Details: map[string]any{"repos": []any{"NVIDIA/cuda-samples", "NVIDIA/cccl", "NVIDIA/TensorRT"}}},
			"cuda-samples +2",
			"NVIDIA/cuda-samples, NVIDIA/cccl, NVIDIA/TensorRT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.RepoSummary(); got != tc.wantSummary {
				t.Errorf("RepoSummary = %q, want %q", got, tc.wantSummary)
			}
			if got := tc.ev.RepoTooltip(); got != tc.wantTooltip {
				t.Errorf("RepoTooltip = %q, want %q", got, tc.wantTooltip)
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
