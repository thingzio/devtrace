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
	"github.com/thingzio/devtrace/pkg/model"
)

// ComputeTier2Heuristics computes AI sensing behavioral heuristics from
// the Behavior struct and InputSignals. Safe to call with nil arguments.
func ComputeTier2Heuristics(beh *model.Behavior, s *InputSignals) *model.BehavioralHeuristics {
	h := &model.BehavioralHeuristics{}

	if beh == nil {
		if s != nil {
			h.SyntheticRiskFlags, h.SyntheticRiskDetails = syntheticRiskFlags(nil, s)
		}
		return h
	}

	// Velocity anomaly: current / baseline.
	if beh.PRVelocityBaseline > 0 {
		h.VelocityAnomalyRatio = toFixed(float64(beh.PRVelocity30d)/beh.PRVelocityBaseline, 2)
	}

	// Active hour spread: passthrough from query.
	h.ActiveHourSpread = beh.ActiveHourSpread

	// Burst-vanish: peak/median ratio amplified by inactivity.
	if beh.BurstVanishDataSufficient {
		multiplier := 1.0
		if beh.BurstVanishDaysSince > 30 {
			multiplier = 1.5
		}
		h.BurstVanishScore = toFixed(beh.BurstVanishPeakRatio*multiplier, 2)
	}

	// Synthetic risk flags.
	if s != nil {
		h.SyntheticRiskFlags, h.SyntheticRiskDetails = syntheticRiskFlags(beh, s)
	}

	return h
}

// syntheticRiskFlags checks for the composite pattern of a synthetic contributor.
func syntheticRiskFlags(beh *model.Behavior, s *InputSignals) (int, []string) {
	var flags []string

	if s.AgeDays < 30 {
		flags = append(flags, "young_account")
	}

	if s.PublicRepos > 0 {
		forkRatio := float64(s.ForkedRepos) / float64(s.PublicRepos)
		if forkRatio > 0.8 {
			flags = append(flags, "high_fork_ratio")
		}
	} else {
		flags = append(flags, "high_fork_ratio")
	}

	if !s.HasBio && !s.HasCompany && !s.HasLocation && !s.HasWebsite {
		flags = append(flags, "empty_profile")
	}

	if beh == nil || beh.ReviewsGiven30d == 0 {
		flags = append(flags, "no_reviews")
	}

	if beh == nil || beh.ConsistencyScore == 0 {
		flags = append(flags, "no_consistency")
	}

	if s.Commits > 0 && s.UnverifiedCommits == s.Commits {
		flags = append(flags, "no_verified_commits")
	}

	return len(flags), flags
}
