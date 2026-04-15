# AI Sensing Tier 2 — Design

> Phase 7. Approved 2026-04-15.

## Overview

Behavioral heuristics that detect whether a contributor's activity looks human vs. AI-generated/automated, plus richer signals wired into the behavioral scoring category. Two deliverables:

1. **Behavioral category improvement** — replace 2 crude signals (burst rate, fork ratio) with 5 richer signals using data already in the `Behavior` struct
2. **Tier 2 heuristics** — velocity anomaly, active hour spread, burst-vanish, synthetic risk flags — computed on-demand, returned in `AISensing.Behavioral` sub-struct

**Competitive rationale:** AI-generated code has 2.7x higher vulnerability density ([COMP.md](../COMP.md#ai-generated-code)). No competitor provides behavioral AI sensing at the contributor level. The hackerbot-claw incident (StepSecurity, 2026) demonstrated that composite profile signals catch synthetic contributors that individual checks miss.

**Design principle preserved:** AI sensing remains a separate transparency dimension — not folded into the reputation score ([SCOPE.md Q5](../SCOPE.md#decisions-log)).

## Behavioral Category Scoring Improvement

### Current State

`pkg/score/score.go` — behavioral category (0.20 weight) uses 2 signals:

| Signal | Weight | Source |
|--------|--------|--------|
| Burst rate | 0.10 | `RecentPRRepoCount` from `InputSignals` |
| Fork ratio | 0.10 | `ForkedRepos` / `PublicRepos` from `InputSignals` |

The `Behavior` struct (fetched from `contributor_activity`) has 9 fields — none feed into the score.

### New State

5 signals, same 0.20 total weight:

| Signal | Weight | Source | Computation |
|--------|--------|--------|-------------|
| Consistency | 0.06 | `Behavior.ConsistencyScore` | Already 0.0-1.0 (active_weeks / 25.7) |
| Review participation | 0.04 | `Behavior.ReviewsGiven30d` | `clampedRatio(ReviewsGiven30d, 10)` |
| Repo diversity | 0.04 | `Behavior.DistinctRepos90d` | `clampedRatio(DistinctRepos90d, 8)` |
| Fork ratio | 0.03 | `InputSignals` (existing) | Same logic, reduced weight |
| Burst rate | 0.03 | `InputSignals` (existing) | Same logic, reduced weight |

**Fallback:** When `Behavior` is nil (no GH Archive data), consistency/review/diversity default to 0. Fork and burst carry their existing defaults. Preserves the "works without GH Archive" property.

**Score impact:** Existing contributor scores will shift. Contributors with consistent activity, code reviews, and cross-project participation will score higher in the behavioral category. This is intentional — the current model undervalues these signals.

## Tier 2 Behavioral Heuristics

Computed on-demand at scoring time. Estimated <5ms additional latency (same `contributor_activity` rows already being queried). No persistence — `devtrace_ai_signal` table stays unused until Tier 3.

### Velocity Anomaly Ratio

Current 30d PR velocity divided by historical baseline.

**Computation:** `Behavior.PRVelocity30d / Behavior.PRVelocityBaseline` (zero cost — both already in `Behavior`).

**Interpretation:**
- ~1.0 = normal
- \>2.0 = notable spike
- \>5.0 = suspicious
- Baseline 0 = insufficient data, omit

### Active Hour Spread

Distinct hours of the day (0-23) the contributor has been active over 90 days.

**Computation:** New clause in existing `GetBehavioralSignals` query:
```sql
COUNT(DISTINCT EXTRACT(hour FROM hour)) FILTER (WHERE hour > NOW() - INTERVAL '90 days')
```

Same rows already being scanned. Returns 0-24.

**Interpretation:**
- 8-16 = typical human
- 1-4 = narrow window (possible automation)
- 20+ = unusually distributed

### Burst-Vanish Score

Peak-to-median weekly activity ratio, amplified by recency gap.

**Computation:** New query:
```sql
WITH weekly AS (
  SELECT date_trunc('week', hour) AS week,
         SUM(prs_opened + reviews_given + issue_comments) AS activity
  FROM devtrace_contributor_activity
  WHERE username = $1 AND provider = $2
    AND hour > NOW() - INTERVAL '180 days'
  GROUP BY 1
)
SELECT
  MAX(activity)::float / GREATEST(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY activity), 1) AS peak_ratio,
  EXTRACT(days FROM NOW() - MAX(week))::int AS days_since_last_active
FROM weekly
```

**Score:** `peak_ratio * (days_since_last_active > 30 ? 1.5 : 1.0)`

**Interpretation:**
- <2.0 = steady
- 2.0-5.0 = some burstiness
- \>5.0 = significant burst-and-vanish

### Synthetic Contributor Pattern

Composite detector inspired by the hackerbot-claw incident. Multiple weak signals that individually might pass but collectively indicate a synthetic or malicious contributor.

**Flags checked** (from `InputSignals` + `Behavior`):

| Flag | Condition | Source |
|------|-----------|--------|
| `young_account` | Account age < 30 days | `InputSignals.AccountAgeDays` |
| `high_fork_ratio` | Fork ratio > 0.8 | `InputSignals.ForkedRepos` / `InputSignals.PublicRepos` |
| `no_verified_commits` | Zero commit verification | `InputSignals.CommitsVerified` (repo context) or inferred |
| `empty_profile` | No bio, company, location, or website | `InputSignals.Has*` fields |
| `no_reviews` | Zero reviews in 30d | `Behavior.ReviewsGiven30d` |
| `no_consistency` | Consistency score = 0 | `Behavior.ConsistencyScore` |

**Output:** Flag count (0-6) + list of flag names.

## API Response Shape

Nested `Behavioral` sub-struct inside `AISensing`:

```json
"ai_sensing": {
  "co_authored_commits": 2,
  "bot_associated_prs": 0,
  "known_tool_signatures": ["dependabot"],
  "total_commits_analyzed": 50,
  "ai_associated_ratio": 0.04,
  "behavioral": {
    "velocity_anomaly_ratio": 1.4,
    "active_hour_spread": 12,
    "burst_vanish_score": 0.85,
    "synthetic_risk_flags": 4,
    "synthetic_risk_details": ["young_account", "high_fork_ratio", "no_verified_commits", "empty_profile"]
  }
}
```

### Plan Gating

| Plan | AISensing Tier 1 | AISensing.Behavioral (Tier 2) |
|------|-----------------|-------------------------------|
| Unauthenticated | Stripped | Stripped |
| Free | Stripped | Stripped |
| Starter | Full | Stripped |
| Pro | Full | Full |

## Data Flow

```
Score request
  │
  ├─ GetBehavioralSignals(username) → *Behavior
  │    └─ Extended: active_hour_spread added to existing query
  │
  ├─ score.Compute(*signals, *behavior, hasRepo)
  │    └─ Behavioral category: 5 signals (consistency, review, diversity, fork, burst)
  │
  ├─ ComputeTier2Heuristics(*Behavior, *InputSignals) → *BehavioralHeuristics
  │    ├─ velocity_anomaly_ratio (from Behavior, zero cost)
  │    ├─ active_hour_spread (from extended Behavior query)
  │    ├─ burst_vanish_score (new query, <5ms)
  │    └─ synthetic_risk_flags (from InputSignals + Behavior, zero cost)
  │
  ├─ enrichForPlan(response, plan)
  │    └─ Strip AISensing.Behavioral for plans below Pro
  │
  └─ Response
```

## Files Touched

| File | Change |
|------|--------|
| `pkg/model/types.go` | Add `BehavioralHeuristics` struct inside `AISensing` |
| `pkg/data/postgres/activity.go` | Extend `GetBehavioralSignals` with `active_hour_spread`; add `GetBurstVanishScore` query |
| `pkg/score/score.go` | Rewrite behavioral category: 5 signals, accept `*Behavior` parameter |
| `pkg/score/heuristics.go` | New: `ComputeTier2Heuristics` + synthetic risk flag logic |
| `pkg/service/score.go` | Wire Tier 2 into scoring path; update plan gating |
| `pkg/score/score_test.go` | Update behavioral category tests for new signals |
| `pkg/score/heuristics_test.go` | New: Tier 2 heuristic tests |

## What Doesn't Change

- GH Archive ingest pipeline (no new data collected)
- Background scorer (still works, gets richer scores automatically)
- Database schema (no new tables or columns)
- API endpoints (same shape, new nested fields)
- Other 4 scoring categories (provenance, identity, engagement, community)
