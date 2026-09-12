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

import (
	"testing"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestVelocityAnomaly(t *testing.T) {
	cases := []struct {
		name     string
		beh      *model.Behavior
		wantZero bool
	}{
		{"normal velocity", &model.Behavior{PRVelocity30d: 10, PRVelocityBaseline: 8.0}, false},
		{"zero baseline", &model.Behavior{PRVelocity30d: 10, PRVelocityBaseline: 0}, true},
		{"nil behavior", nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := ComputeTier2Heuristics(tc.beh, &InputSignals{AgeDays: 365})
			if tc.wantZero && h.VelocityAnomalyRatio != 0 {
				t.Errorf("got %f, want 0", h.VelocityAnomalyRatio)
			}
			if !tc.wantZero && h.VelocityAnomalyRatio == 0 {
				t.Error("got 0, want non-zero")
			}
		})
	}
}

func TestActiveHourSpread(t *testing.T) {
	h := ComputeTier2Heuristics(
		&model.Behavior{ActiveHourSpread: 14},
		&InputSignals{AgeDays: 365},
	)
	if h.ActiveHourSpread != 14 {
		t.Errorf("got %d, want 14", h.ActiveHourSpread)
	}
}

func TestBurstVanish(t *testing.T) {
	cases := []struct {
		name      string
		beh       *model.Behavior
		wantAbove float64
	}{
		{"steady", &model.Behavior{
			BurstVanishPeakRatio:      1.5,
			BurstVanishDaysSince:      5,
			BurstVanishDataSufficient: true,
		}, 0},
		{"bursty and vanished", &model.Behavior{
			BurstVanishPeakRatio:      8.0,
			BurstVanishDaysSince:      45,
			BurstVanishDataSufficient: true,
		}, 5.0},
		{"insufficient data", &model.Behavior{
			BurstVanishDataSufficient: false,
		}, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := ComputeTier2Heuristics(tc.beh, &InputSignals{AgeDays: 365})
			if tc.wantAbove >= 0 && h.BurstVanishScore <= tc.wantAbove {
				t.Errorf("got %f, want > %f", h.BurstVanishScore, tc.wantAbove)
			}
		})
	}
}

func TestSyntheticRiskFlags(t *testing.T) {
	s := &InputSignals{
		AgeDays:     10,
		PublicRepos: 5,
		ForkedRepos: 5,
	}
	beh := &model.Behavior{
		ConsistencyScore: 0,
		ReviewsGiven30d:  0,
	}

	h := ComputeTier2Heuristics(beh, s)
	if h.SyntheticRiskFlags < 4 {
		t.Errorf("expected at least 4 flags, got %d: %v", h.SyntheticRiskFlags, h.SyntheticRiskDetails)
	}

	// Established contributor should have zero flags
	s2 := &InputSignals{
		AgeDays:     1000,
		PublicRepos: 20,
		ForkedRepos: 3,
		HasBio:      true,
		HasCompany:  true,
	}
	beh2 := &model.Behavior{
		ConsistencyScore: 0.8,
		ReviewsGiven30d:  5,
	}

	h2 := ComputeTier2Heuristics(beh2, s2)
	if h2.SyntheticRiskFlags != 0 {
		t.Errorf("established contributor: got %d flags, want 0: %v", h2.SyntheticRiskFlags, h2.SyntheticRiskDetails)
	}
}

func TestNilInputs(t *testing.T) {
	h := ComputeTier2Heuristics(nil, nil)
	if h == nil {
		t.Fatal("should never return nil")
	}
}
