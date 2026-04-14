# DevTrace — Project Scope

Inspect the provenance of any open source contributor at a glance. Trace contribution history, assess license obligations, and surface trust signals before they become risks.

## Relationship to DevPulse

DevPulse answers: **"Is this project healthy?"** — org/repo-level health analytics. DevTrace answers: **"Is this contributor trustworthy?"** — individual contributor provenance and trust scoring.

They are complementary lenses on the same underlying data. DevPulse looks at the project; DevTrace looks at the person.

### Shared Infrastructure

Same GCP project, VPC, and Cloud SQL instance. DevTrace owns its own Cloud Run services, tables, secrets, and service accounts. Clean service boundary at the DB level — DevTrace may read DevPulse tables but never writes to them.

### Migration from Reputer

DevTrace's scoring model originated from `mchmarny/reputer` (v3.2.0) but has been fully internalized. The `reputer` library is no longer a dependency. The scoring model lives in `pkg/score/` and evolves independently. Version is tracked by DevTrace release tags, not a separate model version.

---

## Core Capabilities

### 1. On-Demand Reputation Scoring (REST API)

A public-facing API that returns contributor trust signals for any GitHub username.

**Implemented:**

- 5-category weighted scoring model with 23 signals
- Hybrid scoring: uses GH Archive data when available (1 API call), falls back to GitHub API (3 calls)
- Token pool with round-robin rotation across all tenant installation tokens
- Bot detection: known bots return score 0, grade F immediately — no API calls consumed
- Context-aware responses: global signals always present, repo-scoped signals only with `?repo=`
- Risk summaries: reputation language (no repo) vs review language (with repo)
- Claude-powered risk narratives (Haiku, best-effort with template fallback)
- PR authenticity classification (Claude, Starter+ plans)
- 5-minute response cache with plan-aware filtering

**API access tiers:**

| Tier | What it gets |
|------|-------------|
| Unauthenticated | Score + grade only. IP-based rate limiting. |
| Free | + categories, signals, risk summary, behavioral data |
| Starter | + AI sensing (Tier 1), PR authenticity (Claude) |
| Pro | + license distribution, AI sensing Tier 2/3 (deferred) |

### 2. GH Archive Behavioral Enrichment

Hourly ingest of the GitHub public event firehose, providing API-free behavioral signals.

**Implemented:**

- Hourly Cloud Run Job ingests PullRequestEvent, PullRequestReviewEvent, IssueCommentEvent
- Per-contributor hourly summaries in `contributor_activity` table
- Bot actors filtered at ingest time
- Priority-based scoring queue (P1: new + tenant repo, P2: new + any, P3: stale + tenant)
- Background scorer drains queue, then rescores stale contributors
- Behavioral signals in API: PR velocity, review activity, repo diversity, consistency score
- Activity compaction: rows > 30 days aggregated into weekly buckets (runs daily)
- Configurable lookback (fresh install) and catchup-max (missed window recovery)
- Hybrid scoring path: when archive data exists, skips 3 GitHub Search API calls

### 3. License Obligation Analysis

Aggregate the licenses represented by all projects a contributor has committed to.

**Status: Deferred.** Model types exist (`License`, `LicenseEntry` in `pkg/model/types.go`). Implementation requires enumerating repos with merged PRs and collecting SPDX license IDs.

### 4. AI Agent Co-Development Sensing

Detect and quantify how much of a contributor's work is AI-generated or AI-assisted.

**Implemented (Tier 1 — metadata, near-zero cost):**

- Bot account detection (`pkg/bot/`): `[bot]` suffix + known names (aligned with DevPulse)
- Bot filtering at ingest (aggregator) and scoring (immediate zero-score) time
- PR authenticity classification structure (Claude-powered, Starter+)
- AISensing response field: co-authored commits, bot-associated PRs, tool signatures

**Deferred:**

- **Tier 2 (behavioral heuristics):** Velocity anomalies, time-of-day spread, commit size uniformity, burst-and-vanish. Data exists in `contributor_activity`, computation not yet implemented.
- **Tier 3 (Claude analysis):** Code style consistency, commit coherence, risk narratives for ambiguous cases. Integration point exists (`pkg/claude/`), analysis prompts not yet built.

**Design principle:** AI sensing is a separate transparency dimension — not folded into the reputation score. Reputation measures trust; AI sensing measures provenance.

---

## Scoring Model

Native to DevTrace (`pkg/score/`). Version tracked by DevTrace release tags.

### 5 Categories (weights sum to 1.0)

| Category | Weight | Signals |
|----------|--------|---------|
| Code Provenance | 0.15 | Commit verification ratio × account maturity |
| Identity | 0.25 | Account age, association, profile completeness (bio, company, location, website, verified email) |
| Engagement | 0.25 | Commit proportion, recency, PR acceptance rate |
| Community | 0.15 | Follower/following ratio, repository count |
| Behavioral | 0.20 | Cross-repo burst detection, fork-only ratio |

### Signal Sources

| Source | Signals | API Cost |
|--------|---------|----------|
| GitHub Users.Get | Age, bio, company, location, website, email, followers, following, repos, suspended | 1 call (always) |
| GitHub Search | Merged PRs, closed PRs, recent PR repos | 0 with archive, 3 without |
| GitHub Repos.List | Forked repos (single page, max 100) | 1 call |
| GH Archive | PR velocity, reviews, comments, repo diversity, consistency, cumulative PR counts | 0 calls |
| GitHub Repo Stats | Commits, contributors, recency, org membership, association | 3 calls (repo context only) |

### Global vs Repo-Scoped

- **Global signals** (always present in `signals`): account age, followers, PRs, profile fields, suspended
- **Repo-scoped** (in `repo_context`, only with `?repo=`): org member, commits verified, author association, commit stats

---

## Decisions Log

| # | Decision |
|---|----------|
| Q1 | Shared GCP project/VPC/Cloud SQL. Own Cloud Run services, tables, secrets. |
| Q4 | AI sensing v1: metadata-only (Tier 1). Behavioral heuristics deferred (Tier 2). |
| Q5 | AI sensing is a separate dimension — not folded into reputation score. |
| Q7 | Fully independent API. Own domain (`devtrace.thingz.io`), own auth. |
| Q8 | Scoring code internalized from reputer. No external library dependency. |
| Q9 | Identity key: `(username, provider)`. Provider-agnostic for future git platforms. |
| Q12 | No runtime coupling to DevPulse. Code copied and evolved independently. |
| Q13 | Unauthenticated access: score + grade only, IP rate limited. |
| Q14 | Risk signals: context-aware summaries (reputation vs review language). |
| Q15 | Plans: Free / Starter / Pro. Feature gating via plan-aware response filtering. |

---

## Remaining Work

### Near-term

- **Admin service** — operator visibility into tenants, pipeline health, token pool (Phase 6)
- **GitHub Action** — `thingzio/devtrace-action` for PR comment integration
- **License analysis** — SPDX distribution from merged PRs (Starter+)
- **Quota headers** — `X-RateLimit-*` and `X-Quota-*` in API responses
- **Overage billing** — per-request pricing beyond plan quota

### Completed

- **Deployment and bootstrap** — Cloud Run service + ingest job deployed, Terraform infra, CI/CD pipelines, DNS, secrets, domain mapping, first release (see [BOOTSTRAP.md](BOOTSTRAP.md))

### Future

- Tier 2 AI sensing (behavioral heuristics computed from GH Archive data)
- Tier 3 AI sensing (Claude analysis for ambiguous cases)
- Batch API for bulk contributor scoring
- Webhook notifications on score changes
- CLI tool
