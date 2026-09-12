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

package score

import "testing"

func TestGradeBoundaries(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{1.00, "A+"},
		{0.93, "A+"},
		{0.92, "A"},
		{0.87, "A"},
		{0.86, "A-"},
		{0.83, "A-"},
		{0.82, "B+"},
		{0.77, "B+"},
		{0.76, "B"},
		{0.73, "B"},
		{0.72, "B-"},
		{0.67, "B-"},
		{0.66, "C+"},
		{0.63, "C+"},
		{0.62, "C"},
		{0.57, "C"},
		{0.56, "C-"},
		{0.53, "C-"},
		{0.52, "D+"},
		{0.47, "D+"},
		{0.46, "D"},
		{0.43, "D"},
		{0.42, "D-"},
		{0.37, "D-"},
		{0.36, "F"},
		{0.00, "F"},
	}

	for _, tc := range cases {
		got := Grade(tc.score)
		if got != tc.want {
			t.Errorf("Grade(%f) = %q, want %q", tc.score, got, tc.want)
		}
	}
}
