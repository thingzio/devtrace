# DevTrace — Project Scope

Inspect the provenance of any open source contributor at a glance. Trace contribution history, assess license obligations, and surface trust signals before they become risks.

## Relationship to DevPulse

DevPulse answers: **"Is this project healthy?"** — org/repo-level health analytics. DevTrace answers: **"Is this contributor trustworthy?"** — individual contributor provenance and trust scoring.

They are complementary lenses on the same underlying data. DevPulse looks at the project; DevTrace looks at the person.

### Current State

DevPulse already performs contributor reputation scoring via the `github.com/mchmarny/reputer` library (v0.5.1). This includes:

- **Shallow scoring** — local signals (commits in repo, contributor count, recency)  
- **Deep scoring** — full GitHub API signals (\~20 dimensions: account age, followers, orgs, PR history, profile completeness, suspended status, trusted org membership, etc.)  
- Scores stored per-developer with signal breakdowns in `reputation_signals` JSONB

DevTrace elevates this from a DevPulse sub-feature into a standalone service with its own API surface, expanded analysis (licenses, AI co-development), and independent value proposition.

---

## Core Capabilities

### 1\. On-Demand Reputation Scoring (REST API)

A public-facing API that returns contributor trust signals for any GitHub username.

**What exists today (in reputer/devpulse)**:

- Scoring model with 20+ signals, production-tested  
- Two-tier scoring: shallow \== free, and deep \== paid with configurable staleness thresholds  
- Token pool management with rate-limit awareness

**What DevTrace provides**:

- Does not require DevPulse membership (separate service)
- **Account management UI** — tenants sign up (GitHub OAuth), choose plan, monitor quota consumption
- **REST API** — tenants query any contributor by username

**API access tiers**:

- **Unauthenticated** — limited public access for discoverability (e.g., single contributor base score). Abuse prevention via IP-based rate limiting, CAPTCHA on web, and short-lived tokens. Goal: let someone try before signing up.
- **Free tier** — GitHub OAuth sign-up, base scoring (shallow signals + a few easy-to-acquire extras). Generous enough to be useful, limited in contributor count per month.
- **Paid tiers** (Starter / Pro / Enterprise) — deep scoring, historical trends, batch queries, webhook support. Each tier increases the number of unique contributors that can be scored per billing period and the rate limits (per hour/day).

**Trust signals and risk sensing**:

The API doesn't just return a score — it surfaces actionable risk signals. Primary use case: CI/CD integration (e.g., GitHub Action that scores a PR author before merge). The API should detect and flag patterns like:
- Contributor account was recently created
- No prior contribution history to any public repo
- Score below a configurable threshold

The response includes a risk summary alongside the score, enabling automated gates (e.g., "require maintainer review if contributor reputation is unknown").

### 2\. License Obligation Analysis

Aggregate the licenses represented by all projects a contributor has committed to, giving a "license profile" for that contributor.

**What exists today**:

- DevPulse imports `repo_meta.license` (SPDX ID) from GitHub API for tracked repos  
- License is displayed but not analyzed

**What DevTrace adds**:

- For a given contributor, enumerate public repos where they have **merged PRs** (actual code changes, not drive-by fixes)  
- Include contributor's own repos as a secondary signal  
- Collect SPDX license IDs across those repos  
- Produce a license distribution summary with factual annotations  
- Example: "47 repos with merged PRs — 38 Apache-2.0, 6 MIT, 3 GPL-3.0 (contributor has merged PRs in 2 of the GPL-3.0 repos)"  
- No compliance judgments — surface facts, let the consumer assess risk for their context

### 3\. AI Agent Co-Development Sensing

Detect and quantify how much of a contributor's work is AI-generated or AI-assisted.

**Why this matters**: As AI agents increasingly author code under human identities, traditional contribution metrics (commit count, PR count) become unreliable trust signals. A contributor with 500 commits may have personally written 50 of them.

**v1 approach — metadata-only detection** (high precision, low recall):

- **Commit trailers**: `Co-authored-by:` with known AI tool identifiers, `Signed-off-by:` bot signatures  
- **PR attribution**: PRs opened by or associated with known bot accounts (Dependabot, Renovate, GitHub Copilot, Claude Code, etc.)  
- **Commit message patterns**: known AI tool signatures (e.g., "Generated with", tool-specific templates)

**Future extensions** (design for but don't implement in v1):

- **Behavioral analysis**: velocity anomalies, time-of-day patterns, uniform commit sizes  
- **Content analysis**: LLM-based code style analysis, boilerplate detection

The detection interface should accept a contribution record and return an `AISignal` result with a classification and confidence, making it straightforward to plug in additional analyzers behind the same contract.

**Scoring model**: AI co-development is reported as a **separate transparency dimension**, not folded into the reputation score. These are independent lenses — reputation measures trust, AI sensing measures provenance. No shared scoring logic between them.

---

## DevPulse Integration Architecture

**Decision**: Shared infrastructure, independent services.

- Same GCP project, same VPC, same Cloud SQL instance  
- DevTrace gets its own Cloud Run services and related infrastructure (scheduler, secrets, etc.)  
- Shared database — DevTrace owns its own schema/tables within the same PostgreSQL instance  
- No shared application logic between services — clean service boundary at the DB level

**Schema boundary**: DevTrace tables are DevTrace-owned. DevPulse tables are DevPulse-owned. DevTrace may read from DevPulse tables (e.g., `developer`, `event`, `repo_meta`) but never writes to them. DevPulse will eventually read reputation scores from DevTrace tables, replacing its current in-process reputer library calls.

**Migration path**: DevPulse currently calls `reputer/pkg/score.Compute()` inline during import. Over time, DevPulse will switch to querying DevTrace's API or reading DevTrace-owned reputation tables, and the reputer library dependency will be removed.

---

## License Analysis — Integration Options

| Approach | Description | Trade-off |
| :---- | :---- | :---- |
| **GitHub API at query time** | Fetch contributor's repos \+ licenses on demand | Simple, but slow and rate-limit heavy for prolific contributors |
| **Background crawl \+ cache** | Periodically crawl public contributor profiles, cache license maps | Fast queries, but stale data and storage cost |
| **Read from DevPulse \+ extend** | Read `repo_meta.license` from shared DB for known repos; crawl GitHub for repos outside DevPulse's scope | Efficient for overlap, covers gaps, no coupling to DevPulse import pipeline |

Since license data comes from repos where the contributor has merged PRs, the "read from DevPulse \+ extend" approach is the natural fit: use `repo_meta.license` for repos already tracked by DevPulse, crawl GitHub for the rest.

---

## Differentiation in the AI-Agent Era

The core thesis: **contribution count is no longer a proxy for competence or trust.**

When anyone can generate 100 PRs/day with an AI agent, the question shifts from "how much did they contribute?" to:

1. **Authenticity** — What fraction of this person's contributions are genuinely their work?
2. **Judgment** — When they use AI tools, do the results demonstrate good engineering judgment? (merged vs. rejected, review feedback patterns)
3. **Consistency** — Does their contribution pattern show sustained engagement or burst-and-vanish?
4. **License awareness** — Do they respect license boundaries when AI-generated code may originate from copyleft-trained models?
5. **Provenance chain** — Can we trace a contribution back through the toolchain that produced it?

DevTrace becomes the "contributor credit score" — not penalizing AI usage, but distinguishing between a developer who uses AI as a force multiplier (high judgment, consistent engagement) vs. one who rubber-stamps AI output (low review engagement, high rejection rate).

### Detection Layers

AI co-development sensing is structured as a tiered pipeline. Each tier adds cost but also signal. The key design principle: **run cheap tiers on every query, expensive tiers only when justified**.

#### Tier 1 — Metadata (near-zero marginal cost)

Already decided for v1. Extracts signals from data DevTrace is already fetching:

- `Co-authored-by:` trailers with known AI tool identifiers
- `Signed-off-by:` bot signatures
- PR author is a known bot account (Dependabot, Renovate, Copilot, etc.)
- Commit message matches known AI tool templates ("Generated with", "🤖 Created by", etc.)
- GitHub API `author_association` field (bot vs. contributor vs. member)

**Cost**: Zero beyond the GitHub API calls already being made for reputation scoring.

**Limitation**: Only catches explicitly labeled AI contributions. As tools evolve, labels may change or disappear.

#### Tier 2 — Heuristic / Behavioral (low cost, computed from existing data)

Statistical analysis of contribution patterns. No external API calls — computed from GitHub data already in the database.

| Signal | What it detects | How |
|--------|----------------|-----|
| **Velocity anomaly** | Sudden output spikes | Compare rolling 30-day commit/PR rate to 6-month baseline. Flag >3x deviations. |
| **Time-of-day spread** | Inhuman work hours | Contribution timestamps spanning 20+ hours/day consistently |
| **Commit size uniformity** | Mechanical output | Low variance in additions/deletions across commits (humans are messy, agents are uniform) |
| **PR description entropy** | Formulaic descriptions | Measure similarity across PR descriptions — AI-generated tend to follow templates |
| **Review-to-author ratio** | Rubber-stamping | High authored PRs, near-zero reviews given — contributor isn't reading others' code |
| **Rejection rate** | Low judgment | High proportion of PRs closed without merge |
| **Burst-and-vanish** | Drive-by contributions | Intense activity in a short window, then silence — common with agent-driven campaigns |

**Cost**: CPU time for statistical computation. Same economics as DevPulse's existing insights calculations — negligible at scale.

**Limitation**: Heuristics produce false positives. A developer on a deadline looks like an AI. These signals are probabilistic — useful in aggregate, unreliable individually.

#### Tier 3 — LLM Analysis via Claude API (variable cost, high signal)

Use Claude to analyze contribution content where Tier 1+2 signals are ambiguous or where deeper assessment is requested (paid tiers).

**What Claude can assess**:

- **Code style consistency**: Compare a contributor's recent PRs against their historical style. AI-assisted code often introduces style breaks — different naming conventions, commenting patterns, error handling approaches than the contributor's established baseline.
- **PR description authenticity**: Classify PR descriptions as likely human-written vs. AI-generated. AI descriptions tend to be more structured, use bullet points, and explain "what" exhaustively while omitting "why".
- **Commit coherence**: Does the commit message accurately describe the diff? AI agents sometimes produce generic messages that don't match the actual change.
- **Risk narrative**: For high-stakes assessments, produce a natural language summary: "This contributor's last 30 PRs show a style shift starting March 2026 — consistent with AI-assisted development. 8 of 30 PRs carry explicit Co-authored-by tags, but statistical patterns suggest higher AI involvement."

### Claude API Integration Options

| Option | When Claude runs | Cost profile | Best for |
|--------|-----------------|--------------|----------|
| **On-demand only** | User explicitly requests deep analysis | Pay-per-request, fully predictable | Paid tier feature, user controls spend |
| **Threshold-triggered** | Tier 1+2 signals are ambiguous (conflicting or mid-confidence) | Bounded — only fires on edge cases | Improving accuracy without blanket cost |
| **Batch summarization** | Nightly/weekly batch over recently-scored contributors | Bulk pricing, off-peak, predictable | Building cached assessments for popular contributors |
| **Shared-corpus amortization** | First query for a contributor triggers analysis; subsequent queries serve cache | Cost amortized across tenants | Same economics as DevPulse's shared data model |

**Recommended approach**: Combine **on-demand** (paid tier feature) with **shared-corpus amortization** (cache results, serve to all).

When Tenant A requests a deep AI analysis for contributor X, the result is cached. When Tenant B queries the same contributor, they get the cached result at zero incremental cost. This is the same economic model that makes DevPulse cost-effective — scan once, serve to many.

### Claude API — Cost Management

| Lever | How it controls cost |
|-------|---------------------|
| **Model selection by task** | Use Haiku for classification tasks (is this PR description AI-generated? yes/no + confidence). Use Sonnet for narrative risk summaries. Reserve Opus for nothing — overkill for classification. |
| **Batch API** | For non-real-time batch analysis, use Claude's Batch API (50% cost reduction). Nightly runs for popular contributors. |
| **Input minimization** | Don't send full diffs. Send PR metadata + description + commit messages. For style analysis, send representative samples (first 200 lines of 5 recent PRs), not entire codebases. |
| **Staleness windows** | Cache LLM results with configurable TTL (e.g., 7 days for active contributors, 30 days for inactive). Only re-analyze when new contributions arrive AND cache is stale. |
| **Tenant-triggered billing** | LLM analysis is a paid-tier feature. Free tier never triggers Claude API calls. Operator cost is bounded by paying tenant count. |
| **Prompt caching** | Reuse system prompts across requests (Claude supports prompt caching). The classification prompt is identical for every contributor — only the input data changes. |

### Estimated Cost Profile

Assumptions: 1,000 tenants, 10,000 unique contributors scored/month, 20% trigger LLM analysis.

| Component | Volume | Unit cost | Monthly cost |
|-----------|--------|-----------|--------------|
| Tier 1+2 (metadata + heuristics) | 10,000 contributors | ~$0 (GitHub API + CPU) | ~$0 |
| Tier 3 — Haiku classification | 2,000 contributors × 5 PRs each | ~$0.001/request | ~$10 |
| Tier 3 — Sonnet risk narrative | 500 contributors (paid tier deep analysis) | ~$0.01/request | ~$5 |
| Batch API discount | 50% of Tier 3 volume via batch | -50% on batch portion | -$4 |
| **Total LLM cost** | | | **~$11/month** |

With shared-corpus caching, popular contributors (queried by multiple tenants) are analyzed once. At 1,000 tenants the effective per-tenant LLM cost approaches $0.01/month.

### What DevTrace Does NOT Do

To stay cost-effective and self-managed:

- **No full diff analysis** — analyzing entire codebases per contributor is unbounded cost. Metadata, statistics, and representative samples are sufficient.
- **No real-time streaming analysis** — all LLM analysis is async (on-demand with cache, or batch). No blocking API calls that wait for Claude.
- **No training custom models** — use Claude's general capabilities with well-crafted prompts. Avoids model management overhead entirely.
- **No adversarial detection arms race** — DevTrace surfaces transparency signals, not definitive "this is AI" verdicts. The value is in the signal, not in being unbeatable.

---

## Decisions Log

| \# | Question | Decision |
| :---- | :---- | :---- |
| Q1 | Integration architecture | Shared GCP project, VPC, Cloud SQL. Own Cloud Run services. Own tables in shared DB. |
| Q4 | AI sensing v1 scope | Metadata-only (commit trailers, bot signatures). Design interface for future behavioral analysis. |
| Q5 | AI sensing scoring impact | Separate transparency dimension. No shared logic with reputation scoring. |
| Q7 | Public API design | Fully independent service with robust metering. Own domain, own auth. |
| Q8 | Reputer library future | Code copied into devtrace repo (thingz org). `mchmarny/reputer` will be sunset over time, same pattern as `mchmarny/devpulse` → `thingzio/devpulse`. |
| Q11 | Metering model | Two layers: (1) **Tenant-facing** — plan-based quotas with real-time throttling and quota feedback in API responses. (2) **Operator-facing** — usage analytics (requests/month, contributors scored, deep vs. shallow, per-key, per-tenant) for billing and capacity planning. |
| Q12 | DevPulse migration | No migration. DevTrace is independent from day one. Code copied from reputer/devpulse as needed and evolved independently. No shared libraries, no runtime coupling. |
| Q9 | Identity resolution | Business key: `(username, provider)` — e.g., `("mchmarny", "github")`. Provider-agnostic by design to support future git providers (GitLab, Bitbucket, etc.). Email/name tracked as mutable display/query attributes, not identity. |
| Q2 | License scope | Merged PRs (actual code changes) as primary signal. Contributor's own repos as secondary. Drive-by contributions excluded. |
| Q3 | License risk model | Informational with annotations. Show license distribution, surface factual observations (e.g., "3 of 47 repos use GPL-3.0, contributor has merged PRs in 2"). No compliance judgments — let the consumer assess risk for their context. |
| Q13 | Unauthenticated access | Yes — limited public endpoint for discoverability (base score only). Abuse prevention via IP rate limiting, CAPTCHA, short-lived tokens. |
| Q14 | Risk signaling | API returns actionable risk signals alongside scores (new account, no history, below threshold). Primary use case: CI/CD gates via GitHub Action. |
| Q15 | Plan structure | Starter / Pro / Enterprise (mirrors DevPulse). Plans differ in: unique contributors scored per billing period, rate limits, feature access (deep scoring, batch, webhooks). |

---

## Open Questions

### Q6: AI Sensing — Training Data

Do you have access to labeled examples of AI-generated vs. human-written contributions? This matters for when you move beyond metadata-only detection into behavioral analysis — whether that requires a data collection phase first.

### Q10: Monetization Boundary

The thingz.md pricing tiers bundle reputer scores into Pro. With DevTrace as a fully independent service, does it get its own pricing tiers, or remain part of the thingz.io bundle?  
