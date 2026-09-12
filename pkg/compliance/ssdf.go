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

package compliance

import "github.com/thingzio/devtrace/pkg/model"

const (
	StatusPresent = "present"
	StatusAbsent  = "absent"
	StatusNoData  = "no_data"

	msgNeedRepo     = "Requires repository context (?repo=owner/name)"
	msgNoSignals    = "No signal data available"
	msgNeedBehavior = "Requires behavioral data (ingested over time)"

	// GitHub author_association values (subset used here).
	assocOwner        = "OWNER"
	assocMember       = "MEMBER"
	assocCollaborator = "COLLABORATOR"

	// SSDF practice identifiers (NIST SP 800-218).
	practicePS1 = "PS.1"
	practicePS2 = "PS.2"
	practicePS3 = "PS.3"
	practicePW4 = "PW.4"
	practicePW6 = "PW.6"
	practicePW7 = "PW.7"
	practiceRV1 = "RV.1"
	practicePO4 = "PO.4"
)

// PracticeResult maps a contributor's signals to a single SSDF practice.
type PracticeResult struct {
	ID     string // SSDF practice identifier, e.g. "PS.1"
	Name   string // human-readable practice name
	Signal string // what DevTrace evidence was evaluated
	Status string // "present", "absent", or "no_data"
}

// EvaluatePractices checks 8 SSDF practices against available contributor data.
func EvaluatePractices(
	signals *model.Signals,
	repo *model.RepoContext,
	categories map[string]float64,
	behavior *model.Behavior,
	aiSensing *model.AISensing,
) []PracticeResult {
	return []PracticeResult{
		evalPS1(repo),
		evalPS2(repo),
		evalPS3(signals),
		evalPW4(signals),
		evalPW6(behavior),
		evalPW7(behavior),
		evalRV1(aiSensing),
		evalPO4(signals, categories),
	}
}

// PS.1 — Protect code from unauthorized access.
func evalPS1(repo *model.RepoContext) PracticeResult {
	r := PracticeResult{
		ID:   practicePS1,
		Name: "Protect Code from Unauthorized Access",
	}
	if repo == nil {
		r.Signal = msgNeedRepo
		r.Status = StatusNoData
		return r
	}
	r.Signal = "Author association: " + repo.AuthorAssociation
	if repo.OrgMember || repo.TrustedOrgMember {
		r.Signal += " (org member)"
	}
	if repo.OrgMember || repo.AuthorAssociation == assocMember ||
		repo.AuthorAssociation == assocOwner ||
		repo.AuthorAssociation == assocCollaborator {
		r.Status = StatusPresent
	} else {
		r.Status = StatusAbsent
	}
	return r
}

// PS.2 — Verify software release integrity.
func evalPS2(repo *model.RepoContext) PracticeResult {
	r := PracticeResult{
		ID:   practicePS2,
		Name: "Verify Software Release Integrity",
	}
	if repo == nil {
		r.Signal = msgNeedRepo
		r.Status = StatusNoData
		return r
	}
	if repo.CommitsVerified {
		r.Signal = "Commits are cryptographically signed"
		r.Status = StatusPresent
	} else {
		r.Signal = "No signed commits detected"
		r.Status = StatusAbsent
	}
	return r
}

// PS.3 — Archive and provide software provenance.
func evalPS3(signals *model.Signals) PracticeResult {
	r := PracticeResult{
		ID:   practicePS3,
		Name: "Archive and Provide Software Provenance",
	}
	if signals == nil {
		r.Signal = msgNoSignals
		r.Status = StatusNoData
		return r
	}
	fields := 0
	if signals.HasBio {
		fields++
	}
	if signals.HasCompany {
		fields++
	}
	if signals.HasLocation {
		fields++
	}
	if signals.HasWebsite {
		fields++
	}
	if signals.HasPublicEmail {
		fields++
	}
	r.Signal = profileSignalText(fields)
	if fields >= 3 {
		r.Status = StatusPresent
	} else {
		r.Status = StatusAbsent
	}
	return r
}

func profileSignalText(n int) string {
	switch {
	case n >= 4:
		return "Strong identity: profile is well-populated"
	case n >= 3:
		return "Moderate identity: most profile fields present"
	case n >= 1:
		return "Weak identity: few profile fields populated"
	default:
		return "Empty profile: no identity fields populated"
	}
}

// PW.4 — Reuse well-secured software.
func evalPW4(signals *model.Signals) PracticeResult {
	r := PracticeResult{
		ID:   practicePW4,
		Name: "Reuse Well-Secured Software",
	}
	if signals == nil {
		r.Signal = msgNoSignals
		r.Status = StatusNoData
		return r
	}
	if signals.PublicRepos == 0 {
		r.Signal = "No public repositories"
		r.Status = StatusAbsent
		return r
	}
	forkPct := float64(signals.ForkedRepos) / float64(signals.PublicRepos) * 100
	if forkPct > 80 {
		r.Signal = "High fork ratio: limited original work"
		r.Status = StatusAbsent
	} else {
		r.Signal = "Original work present across repositories"
		r.Status = StatusPresent
	}
	return r
}

// PW.6 — Review human-readable code.
func evalPW6(behavior *model.Behavior) PracticeResult {
	r := PracticeResult{
		ID:   practicePW6,
		Name: "Review Human-Readable Code",
	}
	if behavior == nil {
		r.Signal = msgNeedBehavior
		r.Status = StatusNoData
		return r
	}
	if behavior.ReviewsGiven30d > 0 {
		r.Signal = "Active reviewer: participates in code review"
		r.Status = StatusPresent
	} else {
		r.Signal = "No code reviews in the last 30 days"
		r.Status = StatusAbsent
	}
	return r
}

// PW.7 — Test executable code (consistency as proxy).
func evalPW7(behavior *model.Behavior) PracticeResult {
	r := PracticeResult{
		ID:   practicePW7,
		Name: "Test Executable Code",
	}
	if behavior == nil {
		r.Signal = msgNeedBehavior
		r.Status = StatusNoData
		return r
	}
	switch {
	case behavior.ConsistencyScore > 0.5:
		r.Signal = "Consistent activity pattern suggests disciplined practices"
		r.Status = StatusPresent
	case behavior.ConsistencyScore > 0:
		r.Signal = "Low consistency: irregular activity pattern"
		r.Status = StatusAbsent
	default:
		r.Signal = "Insufficient activity data for consistency analysis"
		r.Status = StatusNoData
	}
	return r
}

// RV.1 — Identify vulnerabilities (anomaly detection).
func evalRV1(aiSensing *model.AISensing) PracticeResult {
	r := PracticeResult{
		ID:   practiceRV1,
		Name: "Identify Vulnerabilities",
	}
	if aiSensing == nil || aiSensing.Behavioral == nil {
		r.Signal = "Requires Pro-tier behavioral heuristics"
		r.Status = StatusNoData
		return r
	}
	b := aiSensing.Behavioral
	if b.SyntheticRiskFlags >= 3 || b.VelocityAnomalyRatio > 5.0 {
		r.Signal = "Anomalous patterns detected — review recommended"
		r.Status = StatusAbsent
	} else {
		r.Signal = "No anomalous contributor patterns detected"
		r.Status = StatusPresent
	}
	return r
}

// PO.4 — Security awareness (contributor maturity).
func evalPO4(signals *model.Signals, categories map[string]float64) PracticeResult {
	r := PracticeResult{
		ID:   practicePO4,
		Name: "Security Awareness",
	}
	if signals == nil {
		r.Signal = msgNoSignals
		r.Status = StatusNoData
		return r
	}
	mature := signals.AccountAgeDays > 365 && signals.Followers > 5
	if community, ok := categories["community"]; ok && community > 0.3 && mature {
		r.Signal = "Established contributor with community presence"
		r.Status = StatusPresent
	} else if mature {
		r.Signal = "Established account but limited community engagement"
		r.Status = StatusPresent
	} else {
		r.Signal = "Young or low-visibility account"
		r.Status = StatusAbsent
	}
	return r
}
