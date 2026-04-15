# DevTrace — Implementation Status

API-first architecture. UI, CLI, and GitHub Action are thin clients on top of a single REST API.

---

## Build Phases

### Phase 1 — API + Scoring Engine ✅

Schema, scoring engine, REST API, rate limiting, quota enforcement. Local Postgres via docker-compose, PAT for GitHub API calls, seeded test tenant.

### Phase 2 — Auth + Registration ✅

GitHub OAuth flow, GitHub App installation, token minting, session management. PAT client swapped for App installation client behind the same interface.

### Phase 3 — UI ✅

Landing page, score card, dashboard, settings, ToS acceptance. Server-rendered templates.

### Phase 4 — Backend Operations ✅

Background scorer (queue drain + stale rescoring), DevPulse sync, Claude API integration.

### Phase 5 — GH Archive Ingest ✅

Hourly Cloud Run Job, event aggregation, behavioral signals, activity compaction, hybrid scoring path, bot filtering, token pool.

### Phase 5.5 — Deployment & Bootstrap ✅

Terraform infra (Cloud Run, Artifact Registry, secrets, IAM, WIF), CI/CD pipelines (test-on-push, release-on-tag), DNS domain mapping, first production release. See [BOOTSTRAP.md](BOOTSTRAP.md) for the full runbook.

### Phase 6 — GitHub Action ✅

`thingzio/devtrace-action` — calls API, posts PR comment with score card. Published to GitHub Marketplace.

**Elevated from Phase 7** based on competitive analysis ([COMP.md](COMP.md)): this is the primary GTM wedge. Only `contributor-report` (narrow GH Action with no persistent scoring, API, or AI narratives) competes. Every supply chain attack in [COMP.md — Supply Chain Attacks](COMP.md#high-profile-supply-chain-attacks-contributor-trust-failures) could have been surfaced at PR time.

### Phase 7 — AI Sensing Tier 2 ✅

Behavioral heuristics computed from GH Archive data: velocity anomalies, time-of-day spread, commit size uniformity, burst-and-vanish patterns.

**Elevated from deferred** based on competitive analysis ([COMP.md — AI-Generated Code](COMP.md#ai-generated-code)): AI-generated code has 2.7x higher vulnerability density, and AI lowers the cost of manufacturing fake contributor histories. No competitor provides behavioral AI sensing at the contributor level. Data already exists in `contributor_activity`.

### Phase 8 — Admin Service

Operator visibility: tenant management, pipeline health, token pool monitoring, scoring metrics. Necessary for operations but not a competitive differentiator — moved after GTM-critical phases.

### Phase 9 — Enterprise & Compliance

Enterprise tier and compliance evidence capabilities:
- **Enterprise plan**: SSO, audit logs, compliance exports, SLA, custom scoring policies
- **Compliance reports**: Exportable contributor trust evidence aligned with NIST SSDF (SP 800-218) and EU CRA (2024/2847) due-diligence obligations
- **Batch API**: Bulk contributor scoring for portfolio-level risk assessment

Addresses the $10K+/yr market gap where NetRise Provenance, Apiiro, and Arnica operate. See [COMP.md — Strategic Whitespace](COMP.md#strategic-whitespace) and [COMP.md — Regulatory Pressure](COMP.md#regulatory-pressure).

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

### UI Routes

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/` | None | Landing page |
| `GET` | `/score/{username}` | Any | Score card page |
| `GET` | `/dashboard` | Session | Tenant dashboard |
| `GET` | `/settings` | Session | Account settings |
| `GET` | `/auth/github` | None | OAuth start |
| `GET` | `/auth/github/callback` | None | OAuth callback |
| `POST` | `/auth/signout` | Session | Sign out |
| `GET` | `/tos` | None | Terms of Service |
| `POST` | `/tos/accept` | Session | Accept ToS |

### Response: Unauthenticated

```json
{
  "version": "v0.5.0",
  "username": "octocat",
  "provider": "github",
  "score": {
    "grade": "B+",
    "value": 0.78
  },
  "scored_at": "2026-04-14T10:30:00Z",
  "cached_at": "2026-04-14T10:25:00Z",
  "detail": "Sign up for full signal breakdown -> devtrace.thingz.io"
}
```

### Response: Authenticated (Free+)

```json
{
  "version": "v0.5.0",
  "username": "mchmarny",
  "provider": "github",
  "score": {
    "grade": "C+",
    "value": 0.63,
    "categories": {
      "code_provenance": 0.0,
      "identity": 0.19,
      "engagement": 0.09,
      "community": 0.15,
      "behavioral": 0.20
    }
  },
  "signals": {
    "account_age_days": 5944,
    "followers": 296,
    "following": 10,
    "public_repos": 158,
    "forked_repos": 9,
    "prs_merged": 304,
    "prs_closed": 47,
    "recent_pr_repo_count": 6,
    "has_bio": true,
    "has_company": true,
    "has_location": true,
    "has_website": true,
    "has_verified_email": false,
    "suspended": false
  },
  "risk_summary": "Established contributor with consistent activity history.",
  "behavior": {
    "pr_velocity_30d": 12,
    "pr_velocity_baseline": 8.5,
    "reviews_given_30d": 4,
    "issue_comments_30d": 7,
    "distinct_repos_90d": 6,
    "consistency_score": 0.82,
    "active_since": "2025-01-15T00:00:00Z",
    "total_prs_merged": 304,
    "total_prs_closed": 47
  },
  "scored_at": "2026-04-14T09:44:42Z"
}
```

### Response: With Repo Context

```json
{
  "repo_context": {
    "repo": "org/repo",
    "commits": 50,
    "total_commits": 1842,
    "total_contributors": 34,
    "last_commit_days": 3,
    "org_member": true,
    "commits_verified": true,
    "author_association": "MEMBER",
    "trusted_org_member": false
  },
  "risk_summary": "Well-established contributor with strong activity history. Standard review process is sufficient."
}
```

Note: `signals` contains only global fields — no nulls. Repo-scoped fields (`org_member`, `commits_verified`, `author_association`) live in `repo_context` and only appear when `?repo=` is provided.

### Response: Bot Account

```json
{
  "version": "v0.5.0",
  "username": "dependabot[bot]",
  "provider": "github",
  "score": {
    "grade": "F",
    "value": 0
  },
  "risk_summary": "Bot account detected. Scoring is not applicable.",
  "scored_at": "2026-04-14T10:00:00Z"
}
```

---

## Auth Model

1. **Registration:** GitHub OAuth → GitHub App installation prompt → dashboard
2. **Token pool:** Each installation gives DevTrace server-side tokens for GitHub API. All active installations pooled with round-robin rotation.
3. **API access:** DevTrace mints its own opaque API tokens (`dt_` prefix). Map to tenant + plan.
4. **No stored credentials:** OAuth identifies users, installation tokens are server-managed and short-lived, API tokens are DevTrace's own domain.

---

## Scoring Model

5 categories, 23 signals. See [SCOPE.md](SCOPE.md) for full breakdown.

Key properties:
- Hybrid scoring: 1 API call with GH Archive data, 3 without, +3 with repo context
- Bot accounts return immediately (score 0, no API calls)
- Profile scoring includes 5 identity fields (bio, company, location, website, verified email)
- Version stamped from DevTrace build tags, not a separate model version

---

## GitHub Action (Phase 6)

```yaml
# .github/workflows/devtrace.yaml
name: DevTrace PR Check
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  score:
    runs-on: ubuntu-latest
    steps:
      - uses: thingzio/devtrace-action@v1
        with:
          token: ${{ secrets.DEVTRACE_TOKEN }}
          repo: ${{ github.repository }}
```

**MVP behavior:** Extract PR author → call score API with repo context → post PR comment with score card.

**Post-MVP:** `threshold` input, GitHub check status (pass/warn/fail), opt-in blocking.

**Competitive rationale:** The only similar tool is `contributor-report` (GH Action with ~11 objective metrics, no persistent scoring, no API, no AI). DevTrace Action brings persistent scoring, AI narratives, and behavioral signals to the PR review workflow. See [COMP.md — Tier 6](COMP.md#tier-6-emerging--academic).

---

## Remaining Work

See [SCOPE.md — Remaining Work](SCOPE.md#remaining-work) for the prioritized list (updated per [COMP.md](COMP.md) competitive analysis).
