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
			// Closes / (closes+opens) = 3 / (3+3) = 0.5 — balanced.
			name: "balanced contributor — equal opens and closes",
			la: &model.LifetimeActivity{
				PRsOpened: 10, ReviewsGiven: 8, IssueComments: 4,
				IssuesOpened: 3, IssuesClosed: 3,
			},
			want: &model.Reciprocity{
				ReviewsPerPR:       0.8,
				IssueClosingRate:   0.5,
				IssueCommentsPerPR: 0.4,
			},
		},
		{
			name: "drive-by contributor — opens PRs, never reviews, no issue activity",
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
			// Bounded share: 0 / (0+10) = 0.0 — pure asker.
			name: "issues opened but never closed",
			la: &model.LifetimeActivity{
				PRsOpened: 1, ReviewsGiven: 0,
				IssuesOpened: 10, IssuesClosed: 0,
			},
			want: &model.Reciprocity{IssueClosingRate: 0},
		},
		{
			// Pure maintainer: closes others' issues, never opens any
			// of their own. The pre-fix formula returned 0.0 because the
			// IssuesOpened==0 branch was guarded out, throwing away the
			// strongest possible give-side signal. Bounded share returns 1.0.
			name: "pure giver — closes only, no opens",
			la: &model.LifetimeActivity{
				PRsOpened: 5, ReviewsGiven: 10,
				IssuesOpened: 0, IssuesClosed: 3,
			},
			want: &model.Reciprocity{
				ReviewsPerPR:     2.0,
				IssueClosingRate: 1.0,
			},
		},
		{
			// Maintainer-leaning: many closes, few opens.
			name: "maintainer-leaning — closes >> opens",
			la: &model.LifetimeActivity{
				PRsOpened: 5, ReviewsGiven: 5,
				IssuesOpened: 1, IssuesClosed: 9,
			},
			want: &model.Reciprocity{
				ReviewsPerPR:     1.0,
				IssueClosingRate: 0.9,
			},
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
