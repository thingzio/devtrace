# DevTrace — Plan & Roadmap

Inspect the provenance of any open source contributor at a glance. Trace contribution history, assess trust signals, and surface risk before it becomes a supply chain incident.

---

## Competitive Position

DevTrace occupies uncontested space: no existing product provides per-contributor trust scoring with multi-category behavioral decomposition, AI narratives, and self-serve pricing. The market is fragmented across project-level tools (OpenSSF Scorecard), package-level SCA (Socket, Snyk, Endor Labs), internal developer analytics (Arnica, Apiiro), and enterprise SBOM-centric contributor mapping (NetRise Provenance).

**Key differentiators:**
1. Public, portable per-contributor trust score — the "credit score for OSS contributors"
2. Self-serve tiered pricing in a market where all contributor-aware tools are enterprise-only ($10K+/yr)
3. AI-powered risk narratives that explain *why* a contributor is risky, not just a number
4. Transparent 5-category scoring breakdown — auditable and explainable, not a black box
5. Open source community behavior signals — contribution patterns, review activity, and consistency across the ecosystem
6. PR-time trust checks via GitHub Action — surface risk where developers already work

### Market Context

Software supply chain security: $1.95B (2024) -> $3.27B (2034), 10.9% CAGR. Third-party breaches now account for 30% of all data breaches (Verizon 2025 DBIR, 2x YoY).

### Market Catalysts

- **xz-utils (CVE-2024-3094)**: 2+ year social engineering campaign. Canonical proof that contributor trust is the weakest link.
- **Regulatory pressure**: NIST SSDF (SP 800-218) and EU CRA (2024/2847, full compliance Dec 2027) create implicit demand for contributor vetting evidence.
- **AI-generated code**: 2.7x higher vulnerability density. AI lowers cost of manufacturing fake contributor histories.
- **Maintainer trust erosion**: 66% of maintainers less trusting of contributor PRs (Tidelift 2024). 75% of orgs experienced a supply chain attack (BlackBerry 2024).

---

## Competitive Landscape

### Direct / Near Competitors

| Product | Contributor Scoring? | Pricing | Gap vs DevTrace |
|---------|---------------------|---------|-----------------|
| **NetRise Provenance** | Yes — SBOM-centric portfolio view | Enterprise only | No public per-dev score, no self-serve |
| **OpenRank Protocol** | Yes — graph-based EigenTrust | Free/OSS | Web3-native, no AI narratives, not enterprise SaaS |
| **Arnica.io** | Internal devs only | Free + paid | Not external OSS contributors |
| **Apiiro** | Internal devs only | Enterprise, 50-seat min | Internal teams only |

### Adjacent (Contributor Data Incidental)

| Product | Gap vs DevTrace |
|---------|-----------------|
| **Socket.dev** | Package-level unit, no standalone contributor score |
| **Endor Labs** | Contributor signals buried in package risk |
| **OpenSSF Scorecard** | Project-level, not contributor-level |

### Capability Comparison

| Capability | DevTrace | NetRise | OpenRank | Socket | Arnica | Apiiro |
|------------|----------|---------|----------|--------|--------|--------|
| Per-contributor scoring | **Yes** | Yes | Yes | Partial | Internal | Internal |
| Multi-category weighted score | **Yes (5)** | Unknown | Yes (graph) | Yes (5, pkg) | No | No |
| AI-powered narratives | **Yes** | No | No | Yes (pkg) | No | No |
| Bot detection | **Yes** | No | No | No | No | No |
| Behavioral signals (GH Archive) | **Yes** | No | Yes | No | No | No |
| AI/synthetic contributor detection | **Yes** | No | No | No | Anomaly only | No |
| Self-serve SaaS | **Yes** | No | Web3 | Yes | Yes | No |
| GitHub Action / PR-time scoring | **Yes** | No | No | No | No | No |

### Risks to Monitor

- **NetRise Provenance** expanding toward self-serve
- **Socket.dev** deepening maintainer profiles into standalone scores
- **GitHub** adding native contributor reputation features
- **OpenSSF** launching a contributor-level scorecard extension

---

## What's Shipped

### Core Platform

- 5-category weighted scoring model with 23 signals, hybrid scoring (GH Archive + GitHub API)
- GitHub OAuth + GitHub App installation + session/token management
- Landing page, scorecard, dashboard, settings, ToS, admin dashboard
- Background scorer (queue drain + stale rescoring), DevPulse sync, Claude API integration
- GH Archive hourly ingest, behavioral signals, activity compaction, bot filtering, token pool
- Cloud Run deployment, Terraform infra, CI/CD pipelines, DNS, secrets

### GitHub Action

`thingzio/devtrace-action@v1` — PR comment with trust scores, optional `min-score` threshold enforcement via check runs.

### AI Sensing

- **Tier 1 (metadata)**: Bot detection, commit trailers, tool signatures, PR authenticity classification (Claude, Starter+)
- **Tier 2 (behavioral heuristics, Pro)**: Velocity anomaly ratio, active hour spread, burst-vanish score, synthetic contributor flags. 5 behavioral signals (consistency, review participation, repo diversity, burst rate, fork ratio).

### Admin Dashboard

Session-based at `/admin`, behind GitHub OAuth + `DEVTRACE_ADMIN_USERS`. Tenant management, token pool health, scoring metrics (7-day chart + queue depth), pipeline health.

---

## API Contract

### Base URL

`https://devtrace.thingz.io/api/v1`

### Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/score/{username}` | Any | Score contributor (plan-aware response) |
| `GET` | `/score/{username}?repo=owner/repo` | Any | Score with repo-specific context |
| `GET` | `/score/{username}/history` | Any | Score trend data |
| `POST` | `/token` | Session | Create API token |
| `GET` | `/token` | Session | List API tokens |
| `DELETE` | `/token/{id}` | Session | Revoke API token |
| `GET` | `/health` | None | Health check |
| `POST` | `/webhook/github` | Signature | GitHub App webhook |

### Access Tiers

| Tier | What it gets |
|------|-------------|
| Unauthenticated | Score + grade only. IP-based rate limiting. |
| Free | + categories, signals, risk summary, behavioral data |
| Starter | + AI sensing (Tier 1), PR authenticity (Claude) |
| Pro | + AI sensing Tier 2 (behavioral heuristics) |

---

## Scoring Model

5 categories, 23 signals. Native to DevTrace (`pkg/score/`). Version tracked by release tags.

| Category | Weight | Signals |
|----------|--------|---------|
| Code Provenance | 0.15 | Commit verification ratio x account maturity |
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

---

## Plans

| Feature | Free | Starter | Pro | Enterprise |
|---------|------|---------|-----|------------|
| Scoring | Score + Grade + Signals | + Claude risk narratives | + Claude risk narratives | + Claude risk narratives |
| AI Sensing | Tier 1 (metadata) | Tier 1 + PR authenticity | Tier 1 + Tier 2 (behavioral) | Tier 1 + Tier 2 |
| Score History | 30 days | 90 days | 365 days | Unlimited |
| Rate Limit | 60 req/hour | 300 req/hour | 1,000 req/hour | Custom |
| API Keys | 1 | Multiple | Multiple | Multiple |
| Batch API | — | — | Yes | Yes (high concurrency) |
| Compliance Reports | — | — | — | NIST SSDF + EU CRA |
| SSO | — | — | — | SAML/OIDC |
| Audit Logs | — | — | — | Full |
| Synthetic Detection | — | — | Flags + details | Flags + details |

---

## Next Steps

### P1 — Near-term

| Item | Target Plan | Competitive Rationale |
|------|------------|----------------------|
| **Quota headers** (`X-RateLimit-*`, `X-Quota-*`) | All | Developer experience |
| **Batch API** — bulk contributor scoring | Pro+ | Validates portfolio-level scoring that NetRise pursues at enterprise-only pricing |

### P2 — Medium-term

| Item | Target Plan | Competitive Rationale |
|------|------------|----------------------|
| **Enterprise tier** — SSO, audit logs, SLA | Enterprise | Addresses $10K+/yr market gap (NetRise, Apiiro, Arnica) |
| **Compliance evidence** — exportable contributor trust reports | Enterprise | Unoccupied space; NIST SSDF + EU CRA create implicit demand |
| **AI sensing Tier 3** — Claude analysis for ambiguous cases | Pro | Integration point exists (`pkg/claude/`), prompts pending |

### P3 — Future

| Item | Target Plan | Notes |
|------|------------|-------|
| Webhooks — score change notifications | Pro | Integration enabler |
| Risk alerts (email/webhook) | Starter+ | Retention feature |
| CSV/JSON export | Starter+ | Utility |
| CLI tool | — | — |
| Multi-platform (GitLab, Bitbucket) | — | Extends provider-agnostic identity key |

### Deprioritized

- **License analysis** — SPDX distribution from merged PRs. SCA tools (Snyk, Mend, ORT) handle this well; not a DevTrace differentiator.
- **Overage billing** — revenue feature, lower priority than core differentiators.

---

## Open Questions

- Exact numeric limits per plan (contributors/month)
- Pricing per plan (Free/Starter/Pro self-serve; Enterprise annual contract)
- Enterprise pricing model — per-seat vs per-request vs flat annual
- Custom scoring policy design for Enterprise (which weights/thresholds are configurable?)
- Compliance report format — PDF vs structured JSON vs both
- Which NIST SSDF practice groups map to DevTrace signals

---

## Decisions Log

| # | Decision |
|---|----------|
| Q1 | Shared GCP project/VPC/Cloud SQL. Own Cloud Run services, tables, secrets. |
| Q5 | AI sensing is a separate dimension — not folded into reputation score. |
| Q7 | Fully independent API. Own domain (`devtrace.thingz.io`), own auth. |
| Q8 | Scoring code internalized from reputer. No external library dependency. |
| Q9 | Identity key: `(username, provider)`. Provider-agnostic for future git platforms. |
| Q13 | Unauthenticated access: score + grade only, IP rate limited. |
| Q15 | Plans: Free / Starter / Pro. Feature gating via plan-aware response filtering. |
| Q16 | License analysis deprioritized — SCA tools cover it better. |
| Q17 | GitHub Action elevated to primary GTM wedge. |
| Q18 | AI sensing Tier 2 elevated — behavioral heuristics are time-sensitive. |
| Q19 | Enterprise tier planned — addresses $10K+/yr gap. |
| Q20 | Compliance evidence — NIST SSDF + EU CRA create implicit demand. |
