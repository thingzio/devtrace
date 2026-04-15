# DevTrace — Project Scope

Inspect the provenance of any open source contributor at a glance. Trace contribution history, assess trust signals, and surface risk before it becomes a supply chain incident.

## Competitive Position

DevTrace occupies uncontested space: no existing product provides per-contributor trust scoring with multi-category behavioral decomposition, AI narratives, and self-serve pricing. The market is fragmented across project-level tools (OpenSSF Scorecard), package-level SCA (Socket, Snyk, Endor Labs), internal developer analytics (Arnica, Apiiro), and enterprise SBOM-centric contributor mapping (NetRise Provenance). None score individual external OSS contributors as a standalone product at accessible price points. See [COMP.md](COMP.md) for full analysis.

**Key differentiators to protect:**
1. Public, portable per-contributor trust score (the "credit score for OSS contributors")
2. Self-serve tiered pricing in a market where all contributor-aware tools are enterprise-only ($10K+/yr)
3. AI-powered risk narratives — no competitor offers this at the contributor level
4. 5-category behavioral decomposition — transparent, auditable scoring vs opaque single numbers
5. GH Archive behavioral signals at the contributor level (not project or org level)

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

**Status: Deprioritized.** Model types exist (`License`, `LicenseEntry` in `pkg/model/types.go`). SCA tools (Snyk, Mend, ORT) handle license compliance well — this does not differentiate DevTrace. If implemented, keep lightweight: SPDX distribution from merged PRs, not deep compliance analysis. See [COMP.md — Tier 2](COMP.md#tier-2-package-level-supply-chain-contributor-data-incidental).

### 4. AI Agent Co-Development Sensing

Detect and quantify how much of a contributor's work is AI-generated or AI-assisted.

**Implemented (Tier 1 — metadata, near-zero cost):**

- Bot account detection (`pkg/bot/`): `[bot]` suffix + known names (aligned with DevPulse)
- Bot filtering at ingest (aggregator) and scoring (immediate zero-score) time
- PR authenticity classification structure (Claude-powered, Starter+)
- AISensing response field: co-authored commits, bot-associated PRs, tool signatures

**Implemented (Tier 2 — behavioral heuristics, Pro plan):**

- Velocity anomaly ratio (current vs baseline PR velocity)
- Active hour spread (distinct hours of day active over 90d)
- Burst-vanish score (peak-to-median weekly activity with recency amplifier)
- Synthetic contributor flags (composite: young account, fork-only, empty profile, no reviews, no consistency, unverified commits — inspired by hackerbot-claw incident)
- Behavioral category scoring improved: 5 signals (consistency, review participation, repo diversity, burst rate, fork ratio) replacing 2 crude signals

**Deferred:**

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
| Behavioral | 0.20 | Consistency (0.06), review participation (0.04), repo diversity (0.04), burst rate (0.03), fork ratio (0.03) |

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
| Q16 | License analysis deprioritized — SCA tools cover it better; not a differentiator ([COMP.md](COMP.md)). |
| Q17 | GitHub Action elevated to next-up — primary GTM wedge, only contributor-report (narrow) competes. |
| Q18 | AI sensing Tier 2 elevated — AI code proliferation makes behavioral heuristics time-sensitive. |
| Q19 | Enterprise tier planned — $10K+/yr gap between self-serve and NetRise/Apiiro. |
| Q20 | Compliance evidence is a new capability — NIST SSDF + EU CRA create implicit contributor vetting demand. |

---

## Remaining Work

### Near-term (competitive priority — see [COMP.md](COMP.md))

- **Quota headers** — `X-RateLimit-*` and `X-Quota-*` in API responses
- **Batch API** — bulk contributor scoring. Validates the portfolio-level use case that NetRise Provenance is pursuing at enterprise-only price points.

### Medium-term

- **Admin service** — operator visibility into tenants, pipeline health, token pool. Necessary for operations but not a competitive differentiator.
- **Enterprise tier** — SSO, audit logs, compliance exports, SLA. Addresses the $10K+/yr market gap where NetRise, Apiiro, and Arnica operate ([COMP.md — Tier 1](COMP.md#tier-1-direct--near-competitors-contributor-level-risk)).
- **Compliance evidence** — exportable contributor trust reports aligned with NIST SSDF (SP 800-218) practice groups PS/PO and EU CRA due-diligence obligations. No tool currently serves this for contributor vetting ([COMP.md — Regulatory Pressure](COMP.md#regulatory-pressure)).
- **AI sensing Tier 3** — Claude analysis for ambiguous cases.

### Completed

- **Deployment and bootstrap** — Cloud Run service + ingest job deployed, Terraform infra, CI/CD pipelines, DNS, secrets, domain mapping, first release (see [BOOTSTRAP.md](BOOTSTRAP.md))
- **GitHub Action** — `thingzio/devtrace-action@v1` shipped. PR comment with trust scores, optional `min-score` threshold enforcement via check runs. See [design doc](plans/2026-04-15-devtrace-action-design.md).
- **AI sensing Tier 2** — Behavioral heuristics (velocity anomaly, hour spread, burst-vanish, synthetic contributor flags) computed on-demand. Behavioral scoring category improved from 2 to 5 signals. Pro plan only. See [design doc](plans/2026-04-15-ai-sensing-tier2-design.md).

### Deprioritized

- **License analysis** — SPDX distribution from merged PRs. SCA tools (Snyk, Mend, ORT) handle this well; not a DevTrace differentiator ([COMP.md — Tier 2](COMP.md#tier-2-package-level-supply-chain-contributor-data-incidental)).

### Future

- Webhook notifications on score changes
- CLI tool
- Multi-platform support (GitLab, Bitbucket) — extends the provider-agnostic identity key (`Q9`)
