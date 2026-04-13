package score

import "math"

// ModelVersion is the current scoring model version (ported from reputer v3.2.0).
const ModelVersion = "3.2.0"

const (
	// Category weights (sum to 1.0).
	provenanceWeight  = 0.15
	ageWeight         = 0.15
	associationWeight = 0.05
	profileWeight     = 0.05
	proportionWeight  = 0.15
	recencyWeight     = 0.05
	prAcceptWeight    = 0.05
	followerWeight    = 0.05
	repoCountWeight   = 0.10
	burstWeight       = 0.10
	forkOnlyWeight    = 0.10

	// Ceilings and parameters.
	ageCeilDays              = 730
	verificationMaturityCeil = 730.0
	followerRatioCeil        = 10.0
	repoCountCeil            = 30.0
	baseHalfLifeDays         = 90.0
	minHalfLifeMultiple      = 0.25
	minProportionCeil        = 0.05
	minConfidenceCommits     = 30
	confCommitsPerContrib    = 10
	prCountCeil              = 20.0
	burstCeil                = 5.0
	forkOriginalCeil         = 5.0
)

// Exported category weights derived from signal constants.
var (
	CategoryProvenanceWeight = provenanceWeight
	CategoryIdentityWeight   = ageWeight + associationWeight + profileWeight
	CategoryEngagementWeight = proportionWeight + recencyWeight + prAcceptWeight
	CategoryCommunityWeight  = followerWeight + repoCountWeight
	CategoryBehavioralWeight = burstWeight + forkOnlyWeight
)

// InputSignals holds the raw inputs to the reputation model.
type InputSignals struct {
	// Identity
	AgeDays           int64
	AuthorAssociation string
	HasBio            bool
	HasCompany        bool
	HasLocation       bool
	HasWebsite        bool

	// Engagement
	Commits           int64
	TotalCommits      int64
	TotalContributors int
	LastCommitDays    int64
	PRsMerged         int64
	PRsClosed         int64

	// Community
	Followers   int64
	Following   int64
	PublicRepos int64

	// Behavioral
	RecentPRRepoCount int64
	ForkedRepos       int64

	// Provenance
	UnverifiedCommits int64

	// Flags
	Suspended        bool
	OrgMember        bool
	TrustedOrgMember bool
}

// Compute returns a reputation score in [0.0, 1.0] using the v3 weighted model.
func Compute(s InputSignals) float64 {
	if s.Suspended {
		return 0
	}

	var rep float64

	// --- Category 1: Code Provenance (0.15) ---
	if s.Commits > 0 && s.TotalCommits > 0 {
		verifiedRatio := float64(s.Commits-s.UnverifiedCommits) / float64(s.Commits)
		maturity := logCurve(float64(s.AgeDays), verificationMaturityCeil)
		rep += verifiedRatio * maturity * provenanceWeight
	}

	// --- Category 2: Identity (0.25) ---
	rep += logCurve(float64(s.AgeDays), ageCeilDays) * ageWeight
	rep += associationScore(s.AuthorAssociation, s.OrgMember, s.TrustedOrgMember) * associationWeight

	profileCount := 0
	if s.HasBio {
		profileCount++
	}
	if s.HasCompany {
		profileCount++
	}
	if s.HasLocation {
		profileCount++
	}
	if s.HasWebsite {
		profileCount++
	}
	rep += float64(profileCount) / 4.0 * profileWeight

	// --- Category 3: Engagement (0.25) ---
	if s.Commits > 0 && s.TotalCommits > 0 {
		proportion := float64(s.Commits) / float64(s.TotalCommits)
		propCeil := math.Max(1.0/float64(max(s.TotalContributors, 1)), minProportionCeil)

		confThreshold := float64(max(
			int64(s.TotalContributors)*int64(confCommitsPerContrib),
			int64(minConfidenceCommits),
		))
		confidence := math.Min(float64(s.TotalCommits)/confThreshold, 1.0)

		rep += clampedRatio(proportion, propCeil) * confidence * proportionWeight
	}

	numContrib := max(s.TotalContributors, 1)
	halfLifeMult := math.Max(1.0/math.Log(1+float64(numContrib)), minHalfLifeMultiple)
	if halfLifeMult > 1.0 {
		halfLifeMult = 1.0
	}
	halfLife := baseHalfLifeDays * halfLifeMult
	rep += expDecay(float64(s.LastCommitDays), halfLife) * recencyWeight

	totalTerminalPRs := s.PRsMerged + s.PRsClosed
	if totalTerminalPRs > 0 {
		mergeRate := float64(s.PRsMerged) / float64(totalTerminalPRs)
		confidence := logCurve(float64(totalTerminalPRs), prCountCeil)
		rep += mergeRate * confidence * prAcceptWeight
	}

	// --- Category 4: Community (0.15) ---
	if s.Following > 0 {
		ratio := float64(s.Followers) / float64(s.Following)
		rep += logCurve(ratio, followerRatioCeil) * followerWeight
	}

	rep += logCurve(float64(s.PublicRepos), repoCountCeil) * repoCountWeight

	// --- Category 5: Behavioral (0.20) ---
	if s.RecentPRRepoCount > 0 && s.AgeDays > 0 {
		ageMonths := math.Max(float64(s.AgeDays)/30.0, 1.0)
		burstRate := float64(s.RecentPRRepoCount) / ageMonths
		rep += (1.0 - clampedRatio(burstRate, burstCeil)) * burstWeight
	} else {
		rep += burstWeight
	}

	totalOwnedRepos := s.PublicRepos
	if totalOwnedRepos > 0 {
		originalRepos := float64(totalOwnedRepos - s.ForkedRepos)
		rep += clampedRatio(originalRepos, forkOriginalCeil) * forkOnlyWeight
	}

	return toFixed(rep, 2)
}

// Categories returns per-category scores for the given signals.
func Categories(s InputSignals) map[string]float64 {
	if s.Suspended {
		return map[string]float64{
			"code_provenance": 0,
			"identity":        0,
			"engagement":      0,
			"community":       0,
			"behavioral":      0,
		}
	}

	cats := make(map[string]float64, 5)

	// Code Provenance
	var prov float64
	if s.Commits > 0 && s.TotalCommits > 0 {
		verifiedRatio := float64(s.Commits-s.UnverifiedCommits) / float64(s.Commits)
		maturity := logCurve(float64(s.AgeDays), verificationMaturityCeil)
		prov = verifiedRatio * maturity * provenanceWeight
	}
	cats["code_provenance"] = toFixed(prov, 4)

	// Identity
	identity := logCurve(float64(s.AgeDays), ageCeilDays) * ageWeight
	identity += associationScore(s.AuthorAssociation, s.OrgMember, s.TrustedOrgMember) * associationWeight
	profileCount := 0
	if s.HasBio {
		profileCount++
	}
	if s.HasCompany {
		profileCount++
	}
	if s.HasLocation {
		profileCount++
	}
	if s.HasWebsite {
		profileCount++
	}
	identity += float64(profileCount) / 4.0 * profileWeight
	cats["identity"] = toFixed(identity, 4)

	// Engagement
	var engagement float64
	if s.Commits > 0 && s.TotalCommits > 0 {
		proportion := float64(s.Commits) / float64(s.TotalCommits)
		propCeil := math.Max(1.0/float64(max(s.TotalContributors, 1)), minProportionCeil)
		confThreshold := float64(max(
			int64(s.TotalContributors)*int64(confCommitsPerContrib),
			int64(minConfidenceCommits),
		))
		confidence := math.Min(float64(s.TotalCommits)/confThreshold, 1.0)
		engagement += clampedRatio(proportion, propCeil) * confidence * proportionWeight
	}
	numContrib := max(s.TotalContributors, 1)
	halfLifeMult := math.Max(1.0/math.Log(1+float64(numContrib)), minHalfLifeMultiple)
	if halfLifeMult > 1.0 {
		halfLifeMult = 1.0
	}
	halfLife := baseHalfLifeDays * halfLifeMult
	engagement += expDecay(float64(s.LastCommitDays), halfLife) * recencyWeight
	totalTerminalPRs := s.PRsMerged + s.PRsClosed
	if totalTerminalPRs > 0 {
		mergeRate := float64(s.PRsMerged) / float64(totalTerminalPRs)
		confidence := logCurve(float64(totalTerminalPRs), prCountCeil)
		engagement += mergeRate * confidence * prAcceptWeight
	}
	cats["engagement"] = toFixed(engagement, 4)

	// Community
	var community float64
	if s.Following > 0 {
		ratio := float64(s.Followers) / float64(s.Following)
		community += logCurve(ratio, followerRatioCeil) * followerWeight
	}
	community += logCurve(float64(s.PublicRepos), repoCountCeil) * repoCountWeight
	cats["community"] = toFixed(community, 4)

	// Behavioral
	var behavioral float64
	if s.RecentPRRepoCount > 0 && s.AgeDays > 0 {
		ageMonths := math.Max(float64(s.AgeDays)/30.0, 1.0)
		burstRate := float64(s.RecentPRRepoCount) / ageMonths
		behavioral += (1.0 - clampedRatio(burstRate, burstCeil)) * burstWeight
	} else {
		behavioral += burstWeight
	}
	totalOwnedRepos := s.PublicRepos
	if totalOwnedRepos > 0 {
		originalRepos := float64(totalOwnedRepos - s.ForkedRepos)
		behavioral += clampedRatio(originalRepos, forkOriginalCeil) * forkOnlyWeight
	}
	cats["behavioral"] = toFixed(behavioral, 4)

	return cats
}

// associationScore maps GitHub's author_association to a [0, 1] score.
func associationScore(assoc string, orgMember, trustedOrgMember bool) float64 {
	var base float64
	switch assoc {
	case "OWNER", "MEMBER":
		base = 1.0
	case "COLLABORATOR":
		base = 0.8
	case "CONTRIBUTOR":
		base = 0.5
	case "FIRST_TIME_CONTRIBUTOR":
		base = 0.2
	case "NONE":
		base = 0.0
	default:
		if orgMember {
			base = 1.0
		}
	}

	if trustedOrgMember && base < 0.8 {
		base = 0.8
	}

	return base
}

// clampedRatio maps val linearly into [0.0, 1.0] with ceil as the saturation point.
func clampedRatio(val, ceil float64) float64 {
	if ceil <= 0 || val <= 0 {
		return 0
	}
	if val >= ceil {
		return 1
	}
	return val / ceil
}

// logCurve maps val into [0.0, 1.0] with logarithmic diminishing returns.
func logCurve(val, ceil float64) float64 {
	if ceil <= 0 || val <= 0 {
		return 0
	}
	r := math.Log(1+val) / math.Log(1+ceil)
	if r >= 1 {
		return 1
	}
	return r
}

// expDecay models freshness with a half-life.
func expDecay(val, halfLife float64) float64 {
	if halfLife <= 0 {
		return 0
	}
	if val <= 0 {
		return 1
	}
	return math.Exp(-val * math.Ln2 / halfLife)
}

// toFixed truncates a float64 to the given precision.
func toFixed(num float64, precision int) float64 {
	output := math.Pow(10, float64(precision))
	return float64(int(math.Round(num*output))) / output
}
