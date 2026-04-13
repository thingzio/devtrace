# DevTrace — MVP Definition

Minimum viable product scoped from the user's perspective. API-first architecture — UI, CLI, and GitHub Action are thin clients on top of a single REST API.

---

## Personas

### Day 1: OSS Maintainer

Maintains 1-5 public repos. Gets PRs from unknown contributors. Wants an automated trust signal in the PR workflow — actionable insight that tells them how to handle the PR.

**Journey:**

1. Discovers DevTrace (word of mouth, GitHub Marketplace, blog post)
2. Visits landing page, types a known contributor's username in "try it" box
3. Sees a fully-populated score card (1 free lookup per IP) — convinced there's value
4. Signs up via GitHub OAuth, installs GitHub App on their org
5. Gets an API token from the dashboard
6. Adds DevTrace GitHub Action to their repo
7. Next PR from an unknown contributor — Action posts a comment with score card + risk summary
8. Maintainer uses the signal to decide review depth

**Value moment:** Step 7 — the first PR comment with actionable reputation data.

### Day 2: Enterprise SecOps

Security team at a company with 50+ repos and 200+ contributors. Cares about contributor risk, license exposure, and AI provenance across the portfolio.

**Journey:**

1. Day 1 maintainer within the company evangelizes DevTrace
2. SecOps team signs up for Pro plan, installs GitHub App across the org
3. Uses batch API to score all external contributors across their repos
4. Sets up webhook alerts for contributors below threshold
5. Reviews license exposure — identifies repos with copyleft contributions
6. Monitors AI sensing signals — understands AI tool usage patterns
7. Generates reports for compliance/legal review

**Value moment:** Step 4 — automated monitoring replaces manual contributor vetting.

---

## Architecture

### API is the product

Everything is a client of the REST API:

- **Web UI** — server-rendered, same patterns as DevPulse
- **CLI** — thin wrapper around API calls
- **GitHub Action** — calls API, posts PR comment
- **Future integrations** — all via the same API

### Auth Model

1. **Registration:** GitHub OAuth sign-up → user installs DevTrace GitHub App on their org/repos
2. **Token pool:** Each GitHub App installation gives DevTrace server-side installation tokens for GitHub API calls
3. **API access:** DevTrace mints its own opaque API tokens for users — map internally to tenant + GitHub App installation
4. **No stored credentials:** OAuth identifies users, GitHub App tokens are server-managed and short-lived, DevTrace API tokens are the service's own domain

### GitHub Client Interface

Abstracted behind an interface to support development and production modes:

- **Development:** PAT-backed client (your own token, local docker-compose + Postgres)
- **Production:** GitHub App installation-token-backed client

Single interface, swap implementation via config.

### Unauthenticated Access

- 1 free fully-populated lookup per IP (showcases full capabilities)
- After that, cached-only responses (letter grade + summary, no GitHub API cost)
- If contributor not cached: "Not yet scored — sign up to request"
- IP-based rate limiting (e.g., 10 requests/hour)

---

## API Contract

### Base URL

`https://devtrace.thingz.io/api/v1`

### Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/score/{username}` | None | Cached letter grade + summary (or 1 free full lookup) |
| `GET` | `/score/{username}` | API token | Full score — response depth based on caller's plan |
| `GET` | `/score/{username}?repo={owner/repo}` | API token | Same + repo-contextual signals |
| `GET` | `/tenant` | API token | Tenant info, plan, quota usage |
| `POST` | `/token` | Session (UI) | Mint new API token |
| `DELETE` | `/token/{id}` | Session (UI) | Revoke API token |
| `GET` | `/health` | None | Health check |

### Plan-Aware Response

Single endpoint, progressively richer response based on caller's plan:

| Plan | Response includes |
|------|------------------|
| **Unauth** | grade, value, model_version |
| **Free** | + categories, signals, risk_summary, repo_context |
| **Starter** | + license distribution, AI sensing (Tier 1) |
| **Pro** | + full signal breakdown, AI Tier 2/3, trend data |

### Example: Unauthenticated Response

```json
{
  "username": "octocat",
  "provider": "github",
  "score": {
    "grade": "B+",
    "value": 0.78,
    "model_version": "3.2.0"
  },
  "cached_at": "2026-04-12T10:30:00Z",
  "detail": "Sign up for full signal breakdown -> devtrace.thingz.io"
}
```

### Example: Authenticated Response (Free+)

```json
{
  "username": "octocat",
  "provider": "github",
  "score": {
    "grade": "B+",
    "value": 0.78,
    "model_version": "3.2.0",
    "categories": {
      "code_provenance": 0.82,
      "identity": 0.91,
      "engagement": 0.65,
      "community": 0.73,
      "behavioral": 0.79
    }
  },
  "signals": {
    "account_age_days": 1095,
    "followers": 42,
    "following": 18,
    "public_repos": 23,
    "forked_repos": 5,
    "prs_merged": 87,
    "prs_closed": 12,
    "recent_pr_repo_count": 4,
    "has_bio": true,
    "has_company": true,
    "has_location": true,
    "has_website": false,
    "org_member": false,
    "suspended": false,
    "author_association": "CONTRIBUTOR",
    "commits_verified": true
  },
  "risk_summary": "Established account with consistent contribution history. No prior contributions to this project. Standard review recommended.",
  "repo_context": null,
  "license": null,
  "ai_sensing": null,
  "scored_at": "2026-04-13T08:15:00Z"
}
```

### Example: With Repo Context (`?repo=owner/repo`)

```json
{
  "repo_context": {
    "repo": "owner/repo",
    "commits": 0,
    "total_commits": 1842,
    "total_contributors": 34,
    "last_commit_days": null,
    "org_member": false,
    "author_association": "FIRST_TIME_CONTRIBUTOR",
    "trusted_org_member": false
  }
}
```

### Example: License Distribution (Starter+)

```json
{
  "license": {
    "total_repos_with_merged_prs": 47,
    "own_repos": 23,
    "distribution": [
      {"license": "Apache-2.0", "count": 38, "own": 15, "contributed": 23},
      {"license": "MIT", "count": 6, "own": 5, "contributed": 1},
      {"license": "GPL-3.0", "count": 3, "own": 0, "contributed": 3}
    ]
  }
}
```

### Example: AI Sensing (Starter+, Tier 1)

```json
{
  "ai_sensing": {
    "co_authored_commits": 12,
    "bot_associated_prs": 3,
    "known_tool_signatures": ["dependabot", "copilot"],
    "total_commits_analyzed": 87,
    "ai_associated_ratio": 0.17
  }
}
```

### Rate Limit Headers (all responses)

```
X-RateLimit-Limit: 120
X-RateLimit-Remaining: 98
X-RateLimit-Reset: 1712956800
X-Quota-Limit: 200
X-Quota-Remaining: 143
X-Quota-Reset: 1714521600
```

---

## UI Screens

### 1. Landing Page

- Value prop headline + subtext
- "Try it" search box — type username, get fully-populated score card (1 free lookup per IP)
- After free lookup exhausted: cached-only results with "sign up for more"
- If not cached: "Not yet scored — sign up to request"
- CTA: "Sign up with GitHub" button

### 2. Score Card

- **Unauth:** Letter grade badge, numeric score, "sign up for details" CTA
- **Authed:** Full score with category breakdown, signal details, risk summary
- **Starter+:** License distribution section, AI sensing section
- Trend chart showing score history over time (reuse DevPulse charting solution)
- Optional repo context panel when `?repo=` is provided

### 3. GitHub OAuth + App Installation Flow

- "Sign up with GitHub" -> OAuth consent -> GitHub App installation prompt
- Redirect to dashboard on completion
- Same pattern as DevPulse registration

### 4. Dashboard

- Plan status + quota usage (visual bar: used/remaining)
- API token list (name, created, last used, truncated value)
- "Generate new token" button + revoke action
- Recent scoring activity (last 10 lookups)

### 5. Admin Service (separate Cloud Run service)

- Tenant list + usage summary
- Scoring pipeline status (last run, queue depth, errors)
- GitHub API token pool health (rate limit remaining across installations)
- System metrics (request volume, cache hit rate, latency)

---

## GitHub Action (MVP)

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

**MVP behavior:**

1. Extracts PR author username
2. Calls `GET /api/v1/score/{username}?repo={owner/repo}` with the tenant's token
3. Posts a PR comment with the score card (grade, key signals, risk summary, repo context)

**Post-MVP additions:**

- `threshold` input — set a minimum score
- Sets GitHub check status (pass/warn/fail) based on threshold
- Opt-in blocking (require maintainer override for low scores)

---

## Scoring Model

Based on `mchmarny/reputer` v3.2.0 (copied into DevTrace, evolved independently).

### 5 Categories (weights sum to 1.0)

| Category | Weight | Signals |
|----------|--------|---------|
| Code Provenance | 0.15 | Commit verification ratio x account maturity |
| Identity | 0.25 | Account age, author association, profile completeness |
| Engagement | 0.25 | Commit proportion, recency, PR acceptance rate |
| Community | 0.15 | Follower/following ratio, repository count |
| Behavioral | 0.20 | Cross-repo burst detection, fork-only ratio |

### 22 Signals

Carried over from reputer, extended by DevPulse patterns:

**Identity:** account_age_days, author_association, has_bio, has_company, has_location, has_website

**Engagement:** commits (in repo), total_commits, total_contributors, last_commit_days, prs_merged, prs_closed

**Community:** followers, following, public_repos

**Behavioral:** recent_pr_repo_count, forked_repos

**Code Provenance:** unverified_commits, commits_verified

**Membership:** org_member, trusted_org_member, suspended

### Shallow vs Deep Scoring

- **Shallow:** Local DB signals only (no GitHub API calls). Used for bulk scoring.
- **Deep:** Full GitHub API enrichment (~5-6 concurrent API calls per author). Used for on-demand and scheduled rescoring.
- Both use the same `score.Compute()` model — deep just has more signal data available.

### Tiered Rescoring (from DevPulse)

- Score < 0.5: rescore if stale > 7 days
- Score >= 0.5: rescore if stale > 30 days
- Never deep-scored: always eligible
- Lowest scores rescored first

---

## MVP Cut Line

### In MVP

- Single `/score/{username}` endpoint (plan-aware response)
- GitHub OAuth + GitHub App installation
- API token minting/revocation
- Shallow + deep scoring (reputer v3.2.0 model)
- Tier 1 AI sensing (metadata: commit trailers, bot signatures)
- Basic license distribution (SPDX from merged PRs)
- IP rate limiting (unauth) + token rate limiting (authed)
- Quota enforcement (contributor cap per billing period)
- PR comment GitHub Action
- Landing page with 1 free full lookup
- Score card with trend charts
- Dashboard (plan, quota, tokens)
- Admin service (tenants, pipeline, health)
- PAT-backed GitHub client (dev) swapped to App installation (prod)
- Free / Starter plans

### Deferred

- Batch API
- Webhooks
- Tier 2 AI sensing (behavioral heuristics)
- Tier 3 AI sensing (Claude LLM analysis)
- License annotations and copyleft flagging
- Risk alerts (email/webhook)
- Overage billing and spend caps
- CSV/JSON export
- Check status gating in GitHub Action
- Pro / Enterprise plans
- Historical trend beyond current data window
- Billing/payment UI (use Stripe portal initially)

---

## Build Phases

**Phase 1 — API + Scoring Engine (PAT-backed, local dev)**

Schema, scoring engine (copy reputer), REST API, rate limiting, quota enforcement. Local Postgres via docker-compose, your PAT for GitHub API calls, seeded test tenant.

**Phase 2 — Auth + Registration**

GitHub OAuth flow, GitHub App installation, token minting, swap PAT client for App installation client behind the same interface.

**Phase 3 — UI**

Landing page, score card, dashboard, trend charts. Server-rendered, same patterns as DevPulse.

**Phase 4 — GitHub Action**

Action that calls the API and posts PR comments. Published to GitHub Marketplace.

**Phase 5 — Admin Service**

Separate Cloud Run service for operator visibility. Tenant management, pipeline health, token pool monitoring.

---

## Open Questions

- Exact numeric limits per plan (contributors/month, rate limits)
- Pricing per plan
- Free lookup reset window (24h? weekly?)
- Whether Free plan requires credit card
- CLI scope and distribution (homebrew? go install?)
