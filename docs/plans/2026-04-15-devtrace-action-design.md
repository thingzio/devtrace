# DevTrace GitHub Action — Design

> Phase 6. Approved 2026-04-15.

## Overview

`thingzio/devtrace-action` — a GitHub Action that scores PR contributor trustworthiness using the DevTrace API and posts results as a PR comment. Optionally enforces a minimum score threshold via GitHub Check Runs.

**Competitive rationale:** Only `contributor-report` (narrow GH Action with no persistent scoring, API, or AI narratives) competes. See [COMP.md](../COMP.md#tier-6-emerging--academic).

## Repository

New repo: `github.com/thingzio/devtrace-action` (separate from `thingzio/devtrace`).

```
thingzio/devtrace-action/
├── action.yml              # Action metadata (inputs, outputs, branding)
├── src/
│   ├── main.ts             # Entry point
│   ├── api.ts              # DevTrace API client
│   ├── comment.ts          # PR comment formatting + find/update logic
│   └── check.ts            # Check run creation (when min-score set)
├── dist/
│   └── index.js            # Compiled bundle (ncc output, committed)
├── __tests__/
│   ├── api.test.ts
│   ├── comment.test.ts
│   └── check.test.ts
├── package.json
├── tsconfig.json
├── .github/
│   └── workflows/
│       ├── test.yml        # CI: lint + test on push/PR
│       └── release.yml     # Tag → rebuild dist, create GH release
├── LICENSE
└── README.md
```

## Dependencies

- `@actions/core` — inputs, outputs, logging, failure
- `@actions/github` — Octokit for PR comments and check runs
- `@vercel/ncc` (dev) — compile to single `dist/index.js`

No other runtime dependencies. API calls use Node built-in `fetch` (Node 18+ on all GitHub runners).

## Action Interface

### Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | Yes | — | DevTrace API token (`dt_` prefix) |
| `min-score` | No | — | Threshold 0.0-1.0; enables check run with pass/fail |
| `repo` | No | `${{ github.repository }}` | Repository for scoring context (owner/repo) |
| `trusted-orgs` | No | — | Comma-separated trusted organization slugs |
| `api-url` | No | `https://devtrace.thingz.io` | DevTrace API base URL |

### Outputs

| Output | Description |
|--------|-------------|
| `score` | Numeric score (0.0-1.0) |
| `grade` | Letter grade (A+ through F) |
| `risk-summary` | Risk summary text |

### Permissions

Consuming workflows need:
- `pull-requests: write` — to post/update comments
- `checks: write` — only when `min-score` is set

### Branding

- Icon: `shield`
- Color: `blue`

## Core Logic Flow

```
PR event (opened/synchronize)
  │
  ├─ Extract PR author + commit authors from GitHub context
  │   └─ Deduplicate by username
  │
  ├─ For each author: call DevTrace API
  │    GET {api-url}/api/v1/score/{author}?repo={repo}&trusted_orgs={orgs}
  │    Authorization: Bearer {token}
  │
  ├─ On API error (per author)
  │    ├─ 401 → core.setFailed("Invalid DevTrace token"), abort
  │    ├─ 429 / 5xx / timeout → core.warning(), skip author
  │    └─ 404 → include as "No score available", skip in threshold
  │
  ├─ On success
  │    ├─ Set outputs: score, grade, risk-summary
  │    │
  │    ├─ Find existing comment (marker: <!-- devtrace-score -->)
  │    │   ├─ Found → update comment body
  │    │   └─ Not found → create new comment
  │    │
  │    └─ If min-score is set
  │        ├─ Validate: float 0.0-1.0 (else core.setFailed)
  │        ├─ Collect non-bot authors (score > 0)
  │        ├─ All >= min-score → check run conclusion: "success"
  │        ├─ Any < min-score → check run conclusion: "failure"
  │        └─ All bots → check run conclusion: "neutral"
  │
  └─ Done
```

### Bot Handling

Delegated entirely to the API. The server returns score 0 / grade F / "Bot account detected" for bots. The action renders whatever the API returns. No client-side bot detection logic.

### Multiple Authors

A PR can have the opener plus additional commit authors. Each is scored individually via separate API calls. All scores render in a single comment. For threshold checks, bot authors (score 0) are excluded.

## Comment Format

### Single Author

```markdown
<!-- devtrace-score -->
### DevTrace: octocat — B+ (0.78)
> Established contributor with consistent activity history.

<details><summary>Score breakdown</summary>

| Category | Score |
|----------|-------|
| Code Provenance | 0.15 |
| Identity | 0.19 |
| Engagement | 0.09 |
| Community | 0.15 |
| Behavioral | 0.20 |

</details>

<sub>Scored by [DevTrace](https://devtrace.thingz.io) · [view full scorecard](https://devtrace.thingz.io/score/octocat)</sub>
```

### Multiple Authors

```markdown
<!-- devtrace-score -->
### DevTrace PR Check

| Contributor | Grade | Score | Risk Summary |
|------------|-------|-------|--------------|
| [octocat](https://devtrace.thingz.io/score/octocat) | B+ | 0.78 | Established contributor... |
| [newdev](https://devtrace.thingz.io/score/newdev) | D | 0.32 | Limited contribution history... |

<details><summary>Details: octocat</summary>

| Category | Score |
|----------|-------|
| Code Provenance | 0.15 |
| Identity | 0.19 |
| Engagement | 0.09 |
| Community | 0.15 |
| Behavioral | 0.20 |

</details>

<details><summary>Details: newdev</summary>

| Category | Score |
|----------|-------|
| ... | ... |

</details>

<sub>Scored by [DevTrace](https://devtrace.thingz.io)</sub>
```

Header adapts: single author gets inline grade, multiple authors get summary table. Scorecard links go to DevTrace UI.

## Check Run (when `min-score` set)

**Check run name:** `DevTrace Score`

**Output example:**
```
Title: "All contributors meet minimum score (0.60)"
   or: "1 contributor below minimum score (0.60)"

Summary:
  ✅ octocat: B+ (0.78)
  ❌ newdev: D (0.32) — below threshold 0.60
  ⊘ dependabot[bot]: bot (skipped)
```

Teams add `DevTrace Score` as a required check in repo branch protection settings to enforce contributor trust gates on merges.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| API returns 401 (bad token) | `core.setFailed("Invalid DevTrace token")` — config error, fail hard |
| API returns 429 (rate limited) | `core.warning("Rate limited")`, exit neutral |
| API returns 5xx / timeout (30s) | `core.warning("DevTrace API unavailable")`, exit neutral |
| API returns 404 (unknown user) | Include as "No score available", skip in threshold check |
| PR has 0 commits (empty PR) | `core.warning("No commits found")`, exit neutral |
| `min-score` invalid (not 0.0-1.0) | `core.setFailed("min-score must be between 0.0 and 1.0")` |
| Multiple API calls, partial failure | Score what you can, warn on failures, threshold only checks successful scores |

**Principle:** Config errors (bad token, bad inputs) fail hard. Runtime issues (API down, rate limits) warn and exit neutral. The action never breaks CI due to transient DevTrace issues.

## Usage Example

### Basic (comment only)

```yaml
name: DevTrace PR Check
on:
  pull_request:
    types: [opened, synchronize]

permissions:
  pull-requests: write

jobs:
  score:
    runs-on: ubuntu-latest
    steps:
      - uses: thingzio/devtrace-action@v1
        with:
          token: ${{ secrets.DEVTRACE_TOKEN }}
```

### With enforcement

```yaml
name: DevTrace PR Check
on:
  pull_request:
    types: [opened, synchronize]

permissions:
  pull-requests: write
  checks: write

jobs:
  score:
    runs-on: ubuntu-latest
    steps:
      - uses: thingzio/devtrace-action@v1
        with:
          token: ${{ secrets.DEVTRACE_TOKEN }}
          min-score: '0.5'
          trusted-orgs: 'my-org,partner-org'
```

### With downstream logic

```yaml
steps:
  - uses: thingzio/devtrace-action@v1
    id: devtrace
    with:
      token: ${{ secrets.DEVTRACE_TOKEN }}
  - if: steps.devtrace.outputs.grade == 'F'
    run: echo "::warning::Low trust contributor"
```
