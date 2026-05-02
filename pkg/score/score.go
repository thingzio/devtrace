package score

import (
	"math"

	"github.com/thingzio/devtrace/pkg/model"
)

const (
	// Category weights (sum to 1.0).
	provenanceWeight     = 0.15
	ageWeight            = 0.15
	associationWeight    = 0.05
	profileWeight        = 0.05
	proportionWeight     = 0.15
	recencyWeight        = 0.05
	prAcceptWeight       = 0.05
	followerWeight       = 0.05
	repoCountWeight      = 0.10
	consistencyWeight    = 0.06
	reviewParticipWeight = 0.04
	repoDiversityWeight  = 0.04
	burstWeight          = 0.03
	forkOnlyWeight       = 0.03

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
	reviewCountCeil          = 10.0
	repoDiversityCeil        = 8.0
)

// Exported category weights derived from signal constants.
var (
	CategoryProvenanceWeight = provenanceWeight
	CategoryIdentityWeight   = ageWeight + associationWeight + profileWeight
	CategoryEngagementWeight = proportionWeight + recencyWeight + prAcceptWeight
	CategoryCommunityWeight  = followerWeight + repoCountWeight
	CategoryBehavioralWeight = consistencyWeight + reviewParticipWeight + repoDiversityWeight + burstWeight + forkOnlyWeight
)

// Category name constants used as keys in the per-category map.
const (
	CategoryIdentity       = "identity"
	CategoryEngagement     = "engagement"
	CategoryCommunity      = "community"
	CategoryBehavioral     = "behavioral"
	CategoryCodeProvenance = "code_provenance"
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
	HasPublicEmail    bool

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
// When hasRepo is false, repo-dependent signal weights are redistributed across
// available signals so the score reflects what can actually be evaluated.
func Compute(s InputSignals, hasRepo bool, beh *model.Behavior) float64 {
	if s.Suspended {
		return 0
	}

	// Weight scaling factor: without repo context, repo-dependent weights
	// (provenance, proportion, recency, association) are redistributed.
	// Available weight without repo: 0.60 of 1.00. Scale = 1/0.60 ≈ 1.667.
	scale := 1.0
	if !hasRepo {
		scale = 1.0 / 0.60
	}

	var rep float64

	// --- Category 1: Code Provenance (0.15) — repo-dependent ---
	if hasRepo && s.Commits > 0 && s.TotalCommits > 0 {
		verifiedRatio := float64(s.Commits-s.UnverifiedCommits) / float64(s.Commits)
		maturity := logCurve(float64(s.AgeDays), verificationMaturityCeil)
		rep += verifiedRatio * maturity * provenanceWeight
	}

	// --- Category 2: Identity (0.25) ---
	rep += logCurve(float64(s.AgeDays), ageCeilDays) * ageWeight

	// Association is repo-dependent (needs org membership context).
	if hasRepo {
		rep += associationScore(s.AuthorAssociation, s.OrgMember, s.TrustedOrgMember) * associationWeight
	}

	rep += profileScore(s) * profileWeight

	// --- Category 3: Engagement (0.25) ---
	// Proportion and recency are repo-dependent.
	if hasRepo {
		rep += repoEngagementScore(s)
	}

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
	} else if s.Followers > 0 {
		// Followers with zero following implies an infinite ratio — saturate.
		rep += followerWeight
	}

	rep += logCurve(float64(s.PublicRepos), repoCountCeil) * repoCountWeight

	// --- Category 5: Behavioral (0.20) ---
	rep += behavioralScore(s, beh)

	// Scale up when repo-dependent weights were excluded.
	rep *= scale
	if rep > 1.0 {
		rep = 1.0
	}

	return toFixed(rep, 2)
}

// Categories returns per-category scores for the given signals.
// When hasRepo is false, repo-dependent categories are omitted (not zero).
func Categories(s InputSignals, hasRepo bool, beh *model.Behavior) map[string]float64 {
	if s.Suspended {
		cats := map[string]float64{
			CategoryIdentity:   0,
			CategoryEngagement: 0,
			CategoryCommunity:  0,
			CategoryBehavioral: 0,
		}
		if hasRepo {
			cats[CategoryCodeProvenance] = 0
		}
		return cats
	}

	scale := 1.0
	if !hasRepo {
		scale = 1.0 / 0.60
	}

	cats := make(map[string]float64, 5)

	// Code Provenance — repo-dependent
	if hasRepo {
		var prov float64
		if s.Commits > 0 && s.TotalCommits > 0 {
			verifiedRatio := float64(s.Commits-s.UnverifiedCommits) / float64(s.Commits)
			maturity := logCurve(float64(s.AgeDays), verificationMaturityCeil)
			prov = verifiedRatio * maturity * provenanceWeight
		}
		cats[CategoryCodeProvenance] = toFixed(prov, 4)
	}

	// Identity
	identity := logCurve(float64(s.AgeDays), ageCeilDays) * ageWeight
	if hasRepo {
		identity += associationScore(s.AuthorAssociation, s.OrgMember, s.TrustedOrgMember) * associationWeight
	}
	identity += profileScore(s) * profileWeight
	cats[CategoryIdentity] = toFixed(identity*scale, 4)

	// Engagement — proportion and recency are repo-dependent
	var engagement float64
	if hasRepo {
		engagement += repoEngagementScore(s)
	}
	// PR accept rate is global (not repo-dependent).
	totalTerminalPRs := s.PRsMerged + s.PRsClosed
	if totalTerminalPRs > 0 {
		mergeRate := float64(s.PRsMerged) / float64(totalTerminalPRs)
		confidence := logCurve(float64(totalTerminalPRs), prCountCeil)
		engagement += mergeRate * confidence * prAcceptWeight
	}
	cats[CategoryEngagement] = toFixed(engagement*scale, 4)

	// Community
	var community float64
	if s.Following > 0 {
		ratio := float64(s.Followers) / float64(s.Following)
		community += logCurve(ratio, followerRatioCeil) * followerWeight
	} else if s.Followers > 0 {
		community += followerWeight
	}
	community += logCurve(float64(s.PublicRepos), repoCountCeil) * repoCountWeight
	cats[CategoryCommunity] = toFixed(community*scale, 4)

	// Behavioral
	cats[CategoryBehavioral] = toFixed(behavioralScore(s, beh)*scale, 4)

	return cats
}

// behavioralScore computes the behavioral category (0.20 weight).
func behavioralScore(s InputSignals, beh *model.Behavior) float64 {
	var score float64

	// Consistency (0.06) — from GH Archive
	if beh != nil {
		score += beh.ConsistencyScore * consistencyWeight
	}

	// Review participation (0.04) — from GH Archive
	if beh != nil {
		score += clampedRatio(float64(beh.ReviewsGiven30d), reviewCountCeil) * reviewParticipWeight
	}

	// Repo diversity (0.04) — from GH Archive
	if beh != nil {
		score += clampedRatio(float64(beh.DistinctRepos90d), repoDiversityCeil) * repoDiversityWeight
	}

	// Burst rate (0.03) — from GitHub API
	if s.RecentPRRepoCount > 0 && s.AgeDays > 0 {
		ageMonths := math.Max(float64(s.AgeDays)/30.0, 1.0)
		burstRate := float64(s.RecentPRRepoCount) / ageMonths
		score += (1.0 - clampedRatio(burstRate, burstCeil)) * burstWeight
	} else {
		score += burstWeight
	}

	// Fork ratio (0.03) — from GitHub API
	if s.PublicRepos > 0 {
		originalRepos := float64(s.PublicRepos - s.ForkedRepos)
		score += clampedRatio(originalRepos, forkOriginalCeil) * forkOnlyWeight
	}

	return score
}

// repoEngagementScore returns the repo-dependent portion of the engagement score
// (proportion + recency). Returns 0 when repo context is not available.
func repoEngagementScore(s InputSignals) float64 {
	var score float64
	if s.Commits > 0 && s.TotalCommits > 0 {
		proportion := float64(s.Commits) / float64(s.TotalCommits)
		propCeil := math.Max(1.0/float64(max(s.TotalContributors, 1)), minProportionCeil)
		confThreshold := float64(max(
			int64(s.TotalContributors)*int64(confCommitsPerContrib),
			int64(minConfidenceCommits),
		))
		confidence := math.Min(float64(s.TotalCommits)/confThreshold, 1.0)
		score += clampedRatio(proportion, propCeil) * confidence * proportionWeight
	}
	numContrib := max(s.TotalContributors, 1)
	halfLifeMult := math.Max(1.0/math.Log(1+float64(numContrib)), minHalfLifeMultiple)
	if halfLifeMult > 1.0 {
		halfLifeMult = 1.0
	}
	halfLife := baseHalfLifeDays * halfLifeMult
	score += expDecay(float64(s.LastCommitDays), halfLife) * recencyWeight
	return score
}

// profileScore returns a [0, 1] score based on profile completeness.
func profileScore(s InputSignals) float64 {
	count := 0
	if s.HasBio {
		count++
	}
	if s.HasCompany {
		count++
	}
	if s.HasLocation {
		count++
	}
	if s.HasWebsite {
		count++
	}
	if s.HasPublicEmail {
		count++
	}
	return float64(count) / 5.0
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
