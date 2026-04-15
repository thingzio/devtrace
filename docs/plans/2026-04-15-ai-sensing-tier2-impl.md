# AI Sensing Tier 2 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add behavioral heuristics (velocity anomaly, hour spread, burst-vanish, synthetic risk flags) to the AI sensing response, and wire rich behavioral signals into the scoring model's behavioral category.

**Architecture:** Extend the existing `GetBehavioralSignals` query with `active_hour_spread`, add a `GetBurstVanishScore` query, create a pure-function `ComputeTier2Heuristics` in a new `pkg/score/heuristics.go`, rewrite the behavioral category in `score.Compute` to use 5 signals, and wire everything through the scoring service with Pro-only plan gating.

**Tech Stack:** Go, PostgreSQL, existing scoring infrastructure

**Design doc:** `docs/plans/2026-04-15-ai-sensing-tier2-design.md`

---

## Task 1: Add BehavioralHeuristics Model

**Files:**
- Modify: `pkg/model/types.go`
- Test: `go vet ./pkg/model/...`

**Step 1: Add BehavioralHeuristics struct and wire into AISensing**

Add after the `AuthenticityAssessment` struct (after line 123 of `pkg/model/types.go`):

```go
// BehavioralHeuristics holds Tier 2 AI sensing signals computed from contributor activity.
// Pro plan only.
type BehavioralHeuristics struct {
	VelocityAnomalyRatio float64  `json:"velocity_anomaly_ratio"`
	ActiveHourSpread     int      `json:"active_hour_spread"`
	BurstVanishScore     float64  `json:"burst_vanish_score"`
	SyntheticRiskFlags   int      `json:"synthetic_risk_flags"`
	SyntheticRiskDetails []string `json:"synthetic_risk_details"`
}
```

Add a field to the existing `AISensing` struct (after line 116, after `PRAuthenticity`):

```go
	Behavioral *BehavioralHeuristics `json:"behavioral,omitempty"`
```

**Step 2: Verify it compiles**

Run: `go vet ./pkg/model/...`
Expected: No errors

**Step 3: Commit**

```bash
git add pkg/model/types.go
git commit -S -m "Add BehavioralHeuristics struct for AI Sensing Tier 2"
```

---

## Task 2: Extend GetBehavioralSignals with ActiveHourSpread

**Files:**
- Modify: `pkg/model/types.go` (add field to Behavior)
- Modify: `pkg/data/postgres/activity.go`
- Test: Existing tests still pass

**Step 1: Add ActiveHourSpread to Behavior struct**

In `pkg/model/types.go`, add a new field to the `Behavior` struct (after `TotalPRsClosed` on line 104):

```go
	ActiveHourSpread int `json:"active_hour_spread"`
```

**Step 2: Extend the GetBehavioralSignals query**

In `pkg/data/postgres/activity.go`, modify `GetBehavioralSignals` to add one more column to the SELECT and scan.

Add to the SELECT clause (after `EXTRACT(EPOCH FROM NOW() - MIN(hour)) / 2592000.0`):

```sql
,COALESCE(COUNT(DISTINCT EXTRACT(hour FROM hour)) FILTER (WHERE hour > NOW() - INTERVAL '90 days'), 0)
```

Add a new variable in the var block:

```go
activeHourSpread int
```

Add `&activeHourSpread` to the `.Scan(...)` call.

Set it on the returned struct:

```go
ActiveHourSpread: activeHourSpread,
```

**Step 3: Run existing tests**

Run: `go test ./pkg/data/postgres/... -count=1 -short`
Expected: PASS (or SKIP for integration tests)

Run: `go vet ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add pkg/model/types.go pkg/data/postgres/activity.go
git commit -S -m "Add active hour spread to behavioral signals query"
```

---

## Task 3: Add GetBurstVanishScore Query

**Files:**
- Modify: `pkg/data/postgres/activity.go`
- Test: `go vet ./pkg/data/postgres/...`

**Step 1: Add the BurstVanishResult type and query method**

Add to `pkg/data/postgres/activity.go` after `GetBehavioralSignals`:

```go
// BurstVanishResult holds the raw components for burst-vanish score computation.
type BurstVanishResult struct {
	PeakRatio          float64
	DaysSinceLastActive int
}

// GetBurstVanishScore computes peak-to-median weekly activity ratio over 180 days.
// Returns nil when insufficient data exists (fewer than 2 active weeks).
func (s *Store) GetBurstVanishScore(ctx context.Context, username, provider string) (*BurstVanishResult, error) {
	var peakRatio sql.NullFloat64
	var daysSince sql.NullInt64

	err := s.db.QueryRowContext(ctx,
		`WITH weekly AS (
			SELECT date_trunc('week', hour) AS week,
				SUM(prs_opened + reviews_given + issue_comments) AS activity
			FROM devtrace_contributor_activity
			WHERE username = $1 AND provider = $2
				AND hour > NOW() - INTERVAL '180 days'
			GROUP BY 1
			HAVING SUM(prs_opened + reviews_given + issue_comments) > 0
		)
		SELECT
			CASE WHEN COUNT(*) >= 2
				THEN MAX(activity)::float / GREATEST(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY activity), 1)
				ELSE NULL
			END,
			EXTRACT(days FROM NOW() - MAX(week))::int
		FROM weekly`,
		username, provider,
	).Scan(&peakRatio, &daysSince)
	if err != nil {
		return nil, fmt.Errorf("get burst vanish score: %w", err)
	}

	if !peakRatio.Valid {
		return nil, nil
	}

	return &BurstVanishResult{
		PeakRatio:          peakRatio.Float64,
		DaysSinceLastActive: int(daysSince.Int64),
	}, nil
}
```

**Step 2: Add to BehaviorStore interface**

In `pkg/service/score.go`, extend the `BehaviorStore` interface:

```go
type BehaviorStore interface {
	GetBehavioralSignals(ctx context.Context, username, provider string) (*model.Behavior, error)
	GetBurstVanishScore(ctx context.Context, username, provider string) (*BurstVanishResult, error)
}
```

This requires importing the postgres package type. Instead, define a result type in the interface or use a simpler approach — return `(peakRatio float64, daysSince int, err error)`. However, the cleanest approach is to put the result type in model:

Actually, simpler: add `BurstVanishPeakRatio` and `BurstVanishDaysSince` to the `Behavior` struct and fetch it in a second query inside `GetBehavioralSignals`. This keeps the `BehaviorStore` interface unchanged.

**Revised approach:** Add to `Behavior` struct in `pkg/model/types.go`:

```go
	BurstVanishPeakRatio    float64 `json:"-"`
	BurstVanishDaysSince    int     `json:"-"`
	BurstVanishDataSufficient bool  `json:"-"`
```

The `json:"-"` tags hide these from the API response — they're internal to scoring.

Move the query into `GetBehavioralSignals` as a second query (same pattern as the distinct repos query). This keeps the `BehaviorStore` interface clean.

**Step 2 (revised): Add burst-vanish fields to Behavior and query in GetBehavioralSignals**

In `pkg/model/types.go`, add to `Behavior` struct:

```go
	BurstVanishPeakRatio      float64 `json:"-"`
	BurstVanishDaysSince      int     `json:"-"`
	BurstVanishDataSufficient bool    `json:"-"`
```

In `pkg/data/postgres/activity.go`, add a third query at the end of `GetBehavioralSignals` (after the distinct repos query, before the return):

```go
	// Burst-vanish: peak-to-median weekly activity ratio.
	var peakRatio sql.NullFloat64
	var daysSince sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`WITH weekly AS (
			SELECT date_trunc('week', hour) AS week,
				SUM(prs_opened + reviews_given + issue_comments) AS activity
			FROM devtrace_contributor_activity
			WHERE username = $1 AND provider = $2
				AND hour > NOW() - INTERVAL '180 days'
			GROUP BY 1
			HAVING SUM(prs_opened + reviews_given + issue_comments) > 0
		)
		SELECT
			CASE WHEN COUNT(*) >= 2
				THEN MAX(activity)::float / GREATEST(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY activity), 1)
				ELSE NULL
			END,
			EXTRACT(days FROM NOW() - MAX(week))::int
		FROM weekly`,
		username, provider,
	).Scan(&peakRatio, &daysSince)
	if err != nil {
		return nil, fmt.Errorf("get burst vanish: %w", err)
	}

	if peakRatio.Valid {
		// Set on the result struct before returning
	}
```

Set the fields on the returned struct:

```go
	result := &BehavioralSignals{
		// ... existing fields ...
		BurstVanishPeakRatio:      0,
		BurstVanishDaysSince:      0,
		BurstVanishDataSufficient: false,
	}

	if peakRatio.Valid {
		result.BurstVanishPeakRatio = peakRatio.Float64
		result.BurstVanishDaysSince = int(daysSince.Int64)
		result.BurstVanishDataSufficient = true
	}
```

**Step 3: Verify it compiles**

Run: `go vet ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add pkg/model/types.go pkg/data/postgres/activity.go
git commit -S -m "Add burst-vanish peak ratio query to behavioral signals"
```

---

## Task 4: Rewrite Behavioral Category Scoring

**Files:**
- Modify: `pkg/score/score.go`
- Modify: `pkg/score/score_test.go`

**Step 1: Write failing tests for new behavioral scoring**

Add to `pkg/score/score_test.go`:

```go
func TestBehavioralWithBehavior(t *testing.T) {
	s := InputSignals{
		AgeDays:           365,
		PublicRepos:       10,
		ForkedRepos:       2,
		RecentPRRepoCount: 3,
	}
	b := &model.Behavior{
		ConsistencyScore: 0.8,
		ReviewsGiven30d:  5,
		DistinctRepos90d: 4,
	}

	// With behavior data, score should reflect consistency/review/diversity
	withBeh := Compute(s, false, b)
	withoutBeh := Compute(s, false, nil)

	if withBeh <= withoutBeh {
		t.Errorf("behavioral signals should improve score: with=%f, without=%f", withBeh, withoutBeh)
	}
}

func TestBehavioralNilBehaviorFallback(t *testing.T) {
	s := InputSignals{
		AgeDays:           365,
		PublicRepos:       10,
		ForkedRepos:       2,
		RecentPRRepoCount: 3,
	}

	// nil behavior should still produce a valid score (fallback to burst/fork only)
	got := Compute(s, false, nil)
	if got < 0 || got > 1 {
		t.Errorf("nil behavior score out of bounds: %f", got)
	}
}

func TestCategoryWeightsSumStillOne(t *testing.T) {
	sum := CategoryProvenanceWeight + CategoryIdentityWeight +
		CategoryEngagementWeight + CategoryCommunityWeight +
		CategoryBehavioralWeight
	if diff := sum - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("category weights sum to %f, want 1.0", sum)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pkg/score/... -run TestBehavioral -v`
Expected: FAIL — `Compute` takes 2 args, not 3

**Step 3: Update Compute and Categories signatures**

In `pkg/score/score.go`:

1. Update weight constants:

```go
	// Behavioral sub-weights (sum to 0.20).
	consistencyWeight     = 0.06
	reviewParticipWeight  = 0.04
	repoDiversityWeight   = 0.04
	burstWeight           = 0.03
	forkOnlyWeight        = 0.03
```

Remove the old `burstWeight = 0.10` and `forkOnlyWeight = 0.10`.

Add ceilings:

```go
	reviewCountCeil  = 10.0
	repoDiversityCeil = 8.0
```

2. Update `CategoryBehavioralWeight`:

```go
	CategoryBehavioralWeight = consistencyWeight + reviewParticipWeight + repoDiversityWeight + burstWeight + forkOnlyWeight
```

3. Add `*model.Behavior` parameter to `Compute`:

```go
func Compute(s InputSignals, hasRepo bool, beh *model.Behavior) float64 {
```

4. Replace the behavioral section (lines 136-149) with:

```go
	// --- Category 5: Behavioral (0.20) ---
	rep += behavioralScore(s, beh)
```

5. Add `*model.Behavior` parameter to `Categories`:

```go
func Categories(s InputSignals, hasRepo bool, beh *model.Behavior) map[string]float64 {
```

6. Replace the behavioral section in `Categories` (lines 226-239) with:

```go
	cats["behavioral"] = toFixed(behavioralScore(s, beh)*scale, 4)
```

7. Add the `behavioralScore` helper:

```go
// behavioralScore computes the behavioral category (0.20 weight).
// Uses rich signals from Behavior when available, falls back to burst/fork only.
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
```

**Step 4: Update existing tests**

All existing calls to `Compute(s, hasRepo)` and `Categories(s, hasRepo)` need a third `nil` argument. Update every call in `pkg/score/score_test.go`:

- `Compute(s, false)` → `Compute(s, false, nil)`
- `Compute(s, true)` → `Compute(s, true, nil)`
- `Categories(InputSignals{...}, true)` → `Categories(InputSignals{...}, true, nil)`
- `Categories(InputSignals{...}, false)` → `Categories(InputSignals{...}, false, nil)`

**Step 5: Run all tests**

Run: `go test ./pkg/score/... -v`
Expected: All tests PASS (existing + new)

**Step 6: Commit**

```bash
git add pkg/score/score.go pkg/score/score_test.go
git commit -S -m "Rewrite behavioral category with 5 signals from Behavior struct"
```

---

## Task 5: Fix Callers of Compute and Categories

**Files:**
- Modify: `pkg/service/score.go`
- Modify: `pkg/background/scorer.go`
- Any other callers

**Step 1: Find all callers**

Run: `grep -rn 'score\.Compute\|score\.Categories' pkg/ --include='*.go' | grep -v _test.go`

**Step 2: Update each caller to pass `*model.Behavior`**

In `pkg/service/score.go`, the `Score` method already has `behavior *model.Behavior`:

```go
	value := score.Compute(*signals, hasRepo, behavior)
```

And:

```go
	Categories: score.Categories(*signals, hasRepo, behavior),
```

In `pkg/background/scorer.go`, the `scoreContributor` function already fetches behavioral signals. Pass them through:

Find the `score.Compute` call and add the behavior argument.

**Step 3: Verify it compiles and tests pass**

Run: `go vet ./...`
Run: `go test ./pkg/service/... ./pkg/background/... -short`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/service/score.go pkg/background/scorer.go
git commit -S -m "Pass Behavior to Compute and Categories in all callers"
```

---

## Task 6: Implement Tier 2 Heuristics

**Files:**
- Create: `pkg/score/heuristics.go`
- Create: `pkg/score/heuristics_test.go`

**Step 1: Write failing tests**

Create `pkg/score/heuristics_test.go`:

```go
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
		}, -1}, // signals 0
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
	// Max flags: young account, all forks, empty profile, no reviews, no consistency
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
	// Both nil should not panic
	h := ComputeTier2Heuristics(nil, nil)
	if h == nil {
		t.Fatal("should never return nil")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pkg/score/... -run TestVelocity -v`
Expected: FAIL — `ComputeTier2Heuristics` undefined

**Step 3: Write implementation**

Create `pkg/score/heuristics.go`:

```go
package score

import (
	"math"

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

	// Young account (< 30 days)
	if s.AgeDays < 30 {
		flags = append(flags, "young_account")
	}

	// High fork ratio (> 80% forks)
	if s.PublicRepos > 0 {
		forkRatio := float64(s.ForkedRepos) / float64(s.PublicRepos)
		if forkRatio > 0.8 {
			flags = append(flags, "high_fork_ratio")
		}
	} else {
		flags = append(flags, "high_fork_ratio") // no repos at all
	}

	// Empty profile (none of bio, company, location, website)
	if !s.HasBio && !s.HasCompany && !s.HasLocation && !s.HasWebsite {
		flags = append(flags, "empty_profile")
	}

	// No reviews (behavioral signal)
	if beh == nil || beh.ReviewsGiven30d == 0 {
		flags = append(flags, "no_reviews")
	}

	// No consistency (behavioral signal)
	if beh == nil || beh.ConsistencyScore == 0 {
		flags = append(flags, "no_consistency")
	}

	// No verified commits — only checkable with repo context.
	// When UnverifiedCommits == Commits (and Commits > 0), nothing is verified.
	if s.Commits > 0 && s.UnverifiedCommits == s.Commits {
		flags = append(flags, "no_verified_commits")
	}

	return len(flags), flags
}

// Ensure toFixed and math are available (toFixed is in score.go, same package).
var _ = math.Max // suppress unused import lint if needed
```

Remove the `var _ = math.Max` line — `math` import is already present via `toFixed` usage. Actually `toFixed` is in score.go in the same package so no import issue. Remove the `math` import entirely from heuristics.go since we don't use it directly. The only external import is `model`.

Corrected imports:

```go
package score

import (
	"github.com/thingzio/devtrace/pkg/model"
)
```

**Step 4: Run all tests**

Run: `go test ./pkg/score/... -v`
Expected: All tests PASS

**Step 5: Commit**

```bash
git add pkg/score/heuristics.go pkg/score/heuristics_test.go
git commit -S -m "Add Tier 2 heuristics: velocity anomaly, hour spread, burst-vanish, synthetic flags"
```

---

## Task 7: Wire Tier 2 into Scoring Service

**Files:**
- Modify: `pkg/service/score.go`

**Step 1: Compute and attach Tier 2 heuristics in the Score method**

In `pkg/service/score.go`, after the line that attaches behavior (`full.Behavior = behavior`, around line 154), add:

```go
	// Compute Tier 2 AI sensing heuristics (Pro only, gated in enrichForPlan).
	if behavior != nil || signals != nil {
		tier2 := score.ComputeTier2Heuristics(behavior, signals)
		if full.AISensing == nil {
			full.AISensing = &model.AISensing{}
		}
		full.AISensing.Behavioral = tier2
	}
```

**Step 2: Update plan gating in enrichForPlan**

In the `enrichForPlan` function, update plan cases to strip Tier 2 for non-Pro plans.

In the `"free"` case (around line 191), after stripping PRAuthenticity, also strip Behavioral:

```go
	aiCopy.Behavioral = nil
```

In the `"starter"` case (inside `"starter", "pro"`), split them:

```go
	case "starter":
		if resp.AISensing == nil {
			resp.AISensing = &model.AISensing{}
		} else {
			aiCopy := *resp.AISensing
			aiCopy.Behavioral = nil // Tier 2 is Pro only
			resp.AISensing = &aiCopy
		}

	case "pro":
		if resp.AISensing == nil {
			resp.AISensing = &model.AISensing{}
		}
```

**Step 3: Verify it compiles**

Run: `go vet ./...`
Expected: No errors

**Step 4: Run all tests**

Run: `go test ./... -short -count=1`
Expected: All PASS

**Step 5: Commit**

```bash
git add pkg/service/score.go
git commit -S -m "Wire Tier 2 heuristics into scoring service with Pro-only plan gating"
```

---

## Task 8: Update SCOPE.md and Final Verification

**Files:**
- Modify: `docs/SCOPE.md`
- Modify: `docs/MVP.md`

**Step 1: Mark Phase 6 complete and Phase 7 implemented in MVP.md**

Update Phase 6 heading to include ✅ and Phase 7:

```markdown
### Phase 6 — GitHub Action ✅
### Phase 7 — AI Sensing Tier 2 ✅
```

**Step 2: Update SCOPE.md AI Sensing section**

Move Tier 2 from "Elevated priority" to "Implemented" with a summary of what was built.

**Step 3: Run full test suite**

Run: `go test ./... -count=1`
Expected: All PASS

**Step 4: Run linter**

Run: `go vet ./...`
Expected: No issues

**Step 5: Commit**

```bash
git add docs/SCOPE.md docs/MVP.md
git commit -S -m "Mark Phase 6-7 complete, update AI Sensing Tier 2 status in SCOPE.md"
```
