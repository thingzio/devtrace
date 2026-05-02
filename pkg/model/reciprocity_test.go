package model_test

import (
	"math"
	"testing"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestComputeReciprocity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		la   *model.LifetimeActivity
		want *model.Reciprocity
	}{
		{
			name: "nil receiver returns nil",
			la:   nil,
			want: nil,
		},
		{
			name: "no PRs opened returns nil — undefined ratios",
			la:   &model.LifetimeActivity{ReviewsGiven: 5, IssueComments: 10},
			want: nil,
		},
		{
			name: "balanced contributor",
			la: &model.LifetimeActivity{
				PRsOpened: 10, ReviewsGiven: 8, IssueComments: 4,
				IssuesOpened: 6, IssuesClosed: 3,
			},
			want: &model.Reciprocity{
				ReviewsPerPR:       0.8,
				IssueClosingRate:   0.5,
				IssueCommentsPerPR: 0.4,
			},
		},
		{
			name: "drive-by contributor — opens PRs, never reviews",
			la: &model.LifetimeActivity{
				PRsOpened: 20, ReviewsGiven: 0, IssueComments: 1,
			},
			want: &model.Reciprocity{
				ReviewsPerPR:       0,
				IssueClosingRate:   0,
				IssueCommentsPerPR: 0.05,
			},
		},
		{
			name: "issues opened but never closed",
			la: &model.LifetimeActivity{
				PRsOpened: 1, ReviewsGiven: 0,
				IssuesOpened: 10, IssuesClosed: 0,
			},
			want: &model.Reciprocity{IssueClosingRate: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.la.ComputeReciprocity()
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("nil mismatch: got %v, want %v", got, tt.want)
			}
			if got == nil {
				return
			}
			if !floatEq(got.ReviewsPerPR, tt.want.ReviewsPerPR) {
				t.Errorf("ReviewsPerPR: got %f, want %f", got.ReviewsPerPR, tt.want.ReviewsPerPR)
			}
			if !floatEq(got.IssueClosingRate, tt.want.IssueClosingRate) {
				t.Errorf("IssueClosingRate: got %f, want %f", got.IssueClosingRate, tt.want.IssueClosingRate)
			}
			if !floatEq(got.IssueCommentsPerPR, tt.want.IssueCommentsPerPR) {
				t.Errorf("IssueCommentsPerPR: got %f, want %f", got.IssueCommentsPerPR, tt.want.IssueCommentsPerPR)
			}
		})
	}
}

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
