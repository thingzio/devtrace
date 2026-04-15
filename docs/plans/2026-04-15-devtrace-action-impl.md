# DevTrace GitHub Action Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build `thingzio/devtrace-action` — a GitHub Action that scores PR contributor trustworthiness via the DevTrace API and posts results as PR comments, with optional check run enforcement.

**Architecture:** TypeScript action compiled to single `dist/index.js` via `ncc`. Calls `GET /api/v1/score/{username}` for each PR commit author, renders a Markdown comment, and optionally creates a check run when `min-score` is set. No runtime dependencies beyond `@actions/core` and `@actions/github`.

**Tech Stack:** TypeScript, Node 20, `@actions/core`, `@actions/github`, `@vercel/ncc`, Jest

**Design doc:** `docs/plans/2026-04-15-devtrace-action-design.md`

---

## Task 1: Repository Scaffold

**Files:**
- Create: `package.json`
- Create: `tsconfig.json`
- Create: `action.yml`
- Create: `.github/workflows/test.yml`
- Create: `LICENSE`

**Step 1: Initialize package.json**

```json
{
  "name": "devtrace-action",
  "version": "1.0.0",
  "description": "Score PR contributor trustworthiness using DevTrace",
  "main": "dist/index.js",
  "scripts": {
    "build": "ncc build src/main.ts -o dist --source-map --license licenses.txt",
    "test": "jest --coverage",
    "lint": "tsc --noEmit"
  },
  "repository": {
    "type": "git",
    "url": "https://github.com/thingzio/devtrace-action"
  },
  "keywords": ["github-action", "devtrace", "supply-chain", "contributor-trust"],
  "author": "thingzio",
  "license": "Apache-2.0",
  "dependencies": {
    "@actions/core": "^1.10.1",
    "@actions/github": "^6.0.0"
  },
  "devDependencies": {
    "@types/jest": "^29.5.12",
    "@types/node": "^20.14.0",
    "@vercel/ncc": "^0.38.1",
    "jest": "^29.7.0",
    "ts-jest": "^29.1.4",
    "typescript": "^5.5.0"
  }
}
```

**Step 2: Create tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "commonjs",
    "lib": ["ES2022"],
    "outDir": "./lib",
    "rootDir": "./src",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "resolveJsonModule": true,
    "declaration": true,
    "declarationMap": true,
    "sourceMap": true
  },
  "include": ["src/**/*"],
  "exclude": ["node_modules", "dist", "__tests__"]
}
```

**Step 3: Create action.yml**

```yaml
name: 'DevTrace PR Check'
description: 'Score PR contributor trustworthiness using DevTrace'
author: 'thingzio'

inputs:
  token:
    description: 'DevTrace API token (dt_ prefix)'
    required: true
  min-score:
    description: 'Minimum score threshold (0.0-1.0). Enables check run with pass/fail.'
    required: false
  repo:
    description: 'Repository for scoring context (owner/repo)'
    required: false
    default: '${{ github.repository }}'
  trusted-orgs:
    description: 'Comma-separated trusted organization slugs'
    required: false
  api-url:
    description: 'DevTrace API base URL'
    required: false
    default: 'https://devtrace.thingz.io'

outputs:
  score:
    description: 'Numeric score (0.0-1.0)'
  grade:
    description: 'Letter grade (A+ through F)'
  risk-summary:
    description: 'Risk summary text'

runs:
  using: 'node20'
  main: 'dist/index.js'

branding:
  icon: 'shield'
  color: 'blue'
```

**Step 4: Create jest.config.js**

```javascript
module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  testMatch: ['**/__tests__/**/*.test.ts'],
  collectCoverageFrom: ['src/**/*.ts', '!src/main.ts'],
  coverageThreshold: {
    global: { branches: 80, functions: 80, lines: 80, statements: 80 }
  }
}
```

**Step 5: Create CI workflow at `.github/workflows/test.yml`**

```yaml
name: Test
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'npm'
      - run: npm ci
      - run: npm run lint
      - run: npm test
      - name: Check dist is up to date
        run: |
          npm run build
          git diff --exit-code dist/ || (echo "dist/ is out of date. Run 'npm run build' and commit." && exit 1)
```

**Step 6: Create LICENSE (Apache 2.0)**

Use standard Apache 2.0 license text with `Copyright 2026 thingzio`.

**Step 7: Install dependencies and verify**

Run: `npm install`
Run: `npx tsc --noEmit` (should succeed with no source files yet — no error)

**Step 8: Commit**

```bash
git add -A
git commit -S -m "Scaffold devtrace-action repo with package.json, tsconfig, action.yml, CI"
```

---

## Task 2: API Client

**Files:**
- Create: `src/api.ts`
- Create: `__tests__/api.test.ts`

**Step 1: Write the failing test**

```typescript
// __tests__/api.test.ts
import { fetchScore, ScoreResponse, APIError } from '../src/api'

// Mock global fetch
const mockFetch = jest.fn()
global.fetch = mockFetch

beforeEach(() => {
  mockFetch.mockReset()
})

const baseOpts = {
  apiUrl: 'https://devtrace.thingz.io',
  token: 'dt_abc123',
  repo: 'owner/repo',
  trustedOrgs: '',
}

function mockResponse(status: number, body: object): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? 'OK' : 'Error',
    json: async () => body,
  } as Response
}

describe('fetchScore', () => {
  test('returns score response on 200', async () => {
    const body: ScoreResponse = {
      version: 'v0.6.3',
      username: 'octocat',
      provider: 'github',
      score: { grade: 'B+', value: 0.78, categories: { identity: 0.19 } },
      risk_summary: 'Established contributor.',
      scored_at: '2026-04-15T10:00:00Z',
    }
    mockFetch.mockResolvedValueOnce(mockResponse(200, body))

    const result = await fetchScore('octocat', baseOpts)
    expect(result.score.grade).toBe('B+')
    expect(result.score.value).toBe(0.78)
    expect(result.risk_summary).toBe('Established contributor.')

    const url = mockFetch.mock.calls[0][0] as string
    expect(url).toBe(
      'https://devtrace.thingz.io/api/v1/score/octocat?repo=owner%2Frepo'
    )
    const headers = mockFetch.mock.calls[0][1].headers
    expect(headers['Authorization']).toBe('Bearer dt_abc123')
  })

  test('appends trusted_orgs to query string', async () => {
    mockFetch.mockResolvedValueOnce(
      mockResponse(200, {
        version: 'v0.6.3',
        username: 'octocat',
        provider: 'github',
        score: { grade: 'B+', value: 0.78 },
        scored_at: '2026-04-15T10:00:00Z',
      })
    )

    await fetchScore('octocat', { ...baseOpts, trustedOrgs: 'org1,org2' })

    const url = mockFetch.mock.calls[0][0] as string
    expect(url).toContain('trusted_orgs=org1')
    expect(url).toContain('trusted_orgs=org2')
  })

  test('throws APIError on 401', async () => {
    mockFetch.mockResolvedValueOnce(
      mockResponse(401, { error: 'invalid api token' })
    )

    await expect(fetchScore('octocat', baseOpts)).rejects.toThrow(APIError)
    await expect(fetchScore('octocat', baseOpts)).rejects.toThrow(/401/)
  })

  test('throws APIError on 429', async () => {
    mockFetch.mockResolvedValueOnce(
      mockResponse(429, { error: 'rate limited' })
    )

    try {
      await fetchScore('octocat', baseOpts)
      fail('should have thrown')
    } catch (e) {
      expect(e).toBeInstanceOf(APIError)
      expect((e as APIError).status).toBe(429)
    }
  })

  test('throws APIError on 500', async () => {
    mockFetch.mockResolvedValueOnce(
      mockResponse(500, { error: 'internal' })
    )

    try {
      await fetchScore('octocat', baseOpts)
      fail('should have thrown')
    } catch (e) {
      expect(e).toBeInstanceOf(APIError)
      expect((e as APIError).status).toBe(500)
    }
  })

  test('throws on network timeout', async () => {
    mockFetch.mockRejectedValueOnce(new Error('network timeout'))

    await expect(fetchScore('octocat', baseOpts)).rejects.toThrow(
      'network timeout'
    )
  })
})
```

**Step 2: Run test to verify it fails**

Run: `npx jest __tests__/api.test.ts --verbose`
Expected: FAIL — cannot find module `../src/api`

**Step 3: Write implementation**

```typescript
// src/api.ts

export interface ScoreResponse {
  version: string
  username: string
  provider: string
  score: {
    grade: string
    value: number
    categories?: Record<string, number>
  }
  signals?: Record<string, unknown>
  risk_summary?: string
  repo_context?: {
    repo: string
    commits: number
    total_commits: number
    total_contributors: number
    last_commit_days?: number
    org_member: boolean
    commits_verified: boolean
    author_association?: string
    trusted_org_member?: boolean
  }
  behavior?: Record<string, unknown>
  scored_at: string
  cached_at?: string
  detail?: string
}

export class APIError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(`DevTrace API error ${status}: ${message}`)
    this.name = 'APIError'
  }
}

export interface FetchScoreOpts {
  apiUrl: string
  token: string
  repo: string
  trustedOrgs: string
}

export async function fetchScore(
  username: string,
  opts: FetchScoreOpts,
): Promise<ScoreResponse> {
  const url = buildURL(username, opts)

  const resp = await fetch(url, {
    method: 'GET',
    headers: {
      Authorization: `Bearer ${opts.token}`,
      Accept: 'application/json',
    },
    signal: AbortSignal.timeout(30_000),
  })

  if (!resp.ok) {
    const body = await resp.json().catch(() => ({}))
    throw new APIError(resp.status, (body as Record<string, string>).error ?? resp.statusText)
  }

  return (await resp.json()) as ScoreResponse
}

function buildURL(username: string, opts: FetchScoreOpts): string {
  const base = `${opts.apiUrl}/api/v1/score/${encodeURIComponent(username)}`
  const params = new URLSearchParams()

  if (opts.repo) {
    params.set('repo', opts.repo)
  }

  if (opts.trustedOrgs) {
    for (const org of opts.trustedOrgs.split(',')) {
      const trimmed = org.trim()
      if (trimmed) {
        params.append('trusted_orgs', trimmed)
      }
    }
  }

  const qs = params.toString()
  return qs ? `${base}?${qs}` : base
}
```

**Step 4: Run test to verify it passes**

Run: `npx jest __tests__/api.test.ts --verbose`
Expected: All 6 tests PASS

**Step 5: Commit**

```bash
git add src/api.ts __tests__/api.test.ts
git commit -S -m "Add DevTrace API client with error handling and tests"
```

---

## Task 3: Comment Formatter

**Files:**
- Create: `src/comment.ts`
- Create: `__tests__/comment.test.ts`

**Step 1: Write the failing test**

```typescript
// __tests__/comment.test.ts
import { formatComment, COMMENT_MARKER } from '../src/comment'
import { ScoreResponse } from '../src/api'

function makeScore(overrides: Partial<ScoreResponse> = {}): ScoreResponse {
  return {
    version: 'v0.6.3',
    username: 'octocat',
    provider: 'github',
    score: {
      grade: 'B+',
      value: 0.78,
      categories: {
        code_provenance: 0.15,
        identity: 0.19,
        engagement: 0.09,
        community: 0.15,
        behavioral: 0.20,
      },
    },
    risk_summary: 'Established contributor with consistent activity history.',
    scored_at: '2026-04-15T10:00:00Z',
    ...overrides,
  }
}

describe('formatComment', () => {
  test('single author renders inline header', () => {
    const result = formatComment(
      [{ username: 'octocat', response: makeScore() }],
      'https://devtrace.thingz.io',
    )

    expect(result).toContain(COMMENT_MARKER)
    expect(result).toContain('### DevTrace: octocat — B+ (0.78)')
    expect(result).toContain('Established contributor')
    expect(result).toContain('<details>')
    expect(result).toContain('| Identity | 0.19 |')
    expect(result).toContain('https://devtrace.thingz.io/score/octocat')
  })

  test('multiple authors render summary table', () => {
    const results = [
      { username: 'octocat', response: makeScore() },
      {
        username: 'newdev',
        response: makeScore({
          username: 'newdev',
          score: { grade: 'D', value: 0.32, categories: { identity: 0.05 } },
          risk_summary: 'Limited history.',
        }),
      },
    ]

    const result = formatComment(results, 'https://devtrace.thingz.io')

    expect(result).toContain(COMMENT_MARKER)
    expect(result).toContain('### DevTrace PR Check')
    expect(result).toContain('| [octocat]')
    expect(result).toContain('| [newdev]')
    expect(result).toContain('<details><summary>Details: octocat</summary>')
    expect(result).toContain('<details><summary>Details: newdev</summary>')
  })

  test('author with no categories omits details section', () => {
    const score = makeScore()
    score.score.categories = undefined

    const result = formatComment(
      [{ username: 'octocat', response: score }],
      'https://devtrace.thingz.io',
    )

    expect(result).not.toContain('<details>')
  })

  test('failed author renders error row', () => {
    const results = [
      { username: 'octocat', response: makeScore() },
      { username: 'unknown', error: 'No score available' },
    ]

    const result = formatComment(results, 'https://devtrace.thingz.io')

    expect(result).toContain('unknown')
    expect(result).toContain('No score available')
  })
})
```

**Step 2: Run test to verify it fails**

Run: `npx jest __tests__/comment.test.ts --verbose`
Expected: FAIL — cannot find module `../src/comment`

**Step 3: Write implementation**

```typescript
// src/comment.ts
import { ScoreResponse } from './api'

export const COMMENT_MARKER = '<!-- devtrace-score -->'

export interface AuthorResult {
  username: string
  response?: ScoreResponse
  error?: string
}

const CATEGORY_LABELS: Record<string, string> = {
  code_provenance: 'Code Provenance',
  identity: 'Identity',
  engagement: 'Engagement',
  community: 'Community',
  behavioral: 'Behavioral',
}

export function formatComment(
  results: AuthorResult[],
  apiUrl: string,
): string {
  if (results.length === 1 && results[0].response && !results[0].error) {
    return formatSingle(results[0], apiUrl)
  }
  return formatMultiple(results, apiUrl)
}

function formatSingle(result: AuthorResult, apiUrl: string): string {
  const r = result.response!
  const lines: string[] = [
    COMMENT_MARKER,
    `### DevTrace: ${r.username} — ${r.score.grade} (${r.score.value.toFixed(2)})`,
  ]

  if (r.risk_summary) {
    lines.push(`> ${r.risk_summary}`)
  }

  const details = categoryTable(r)
  if (details) {
    lines.push('', `<details><summary>Score breakdown</summary>`, '', details, '', '</details>')
  }

  lines.push(
    '',
    `<sub>Scored by [DevTrace](${apiUrl}) · [view full scorecard](${apiUrl}/score/${r.username})</sub>`,
  )

  return lines.join('\n')
}

function formatMultiple(results: AuthorResult[], apiUrl: string): string {
  const lines: string[] = [
    COMMENT_MARKER,
    '### DevTrace PR Check',
    '',
    '| Contributor | Grade | Score | Risk Summary |',
    '|------------|-------|-------|--------------|',
  ]

  for (const r of results) {
    if (r.error) {
      lines.push(`| ${r.username} | — | — | ${r.error} |`)
    } else {
      const s = r.response!
      const link = `[${s.username}](${apiUrl}/score/${s.username})`
      const summary = truncate(s.risk_summary ?? '', 60)
      lines.push(`| ${link} | ${s.score.grade} | ${s.score.value.toFixed(2)} | ${summary} |`)
    }
  }

  for (const r of results) {
    if (r.response?.score.categories) {
      const details = categoryTable(r.response)
      if (details) {
        lines.push('', `<details><summary>Details: ${r.response.username}</summary>`, '', details, '', '</details>')
      }
    }
  }

  lines.push('', `<sub>Scored by [DevTrace](${apiUrl})</sub>`)

  return lines.join('\n')
}

function categoryTable(r: ScoreResponse): string | null {
  const cats = r.score.categories
  if (!cats || Object.keys(cats).length === 0) return null

  const rows = Object.entries(cats).map(([key, val]) => {
    const label = CATEGORY_LABELS[key] ?? key
    return `| ${label} | ${val.toFixed(2)} |`
  })

  return ['| Category | Score |', '|----------|-------|', ...rows].join('\n')
}

function truncate(s: string, max: number): string {
  if (s.length <= max) return s
  return s.slice(0, max - 3) + '...'
}
```

**Step 4: Run test to verify it passes**

Run: `npx jest __tests__/comment.test.ts --verbose`
Expected: All 4 tests PASS

**Step 5: Commit**

```bash
git add src/comment.ts __tests__/comment.test.ts
git commit -S -m "Add PR comment formatter with single and multi-author layouts"
```

---

## Task 4: Check Run Creator

**Files:**
- Create: `src/check.ts`
- Create: `__tests__/check.test.ts`

**Step 1: Write the failing test**

```typescript
// __tests__/check.test.ts
import { evaluateThreshold, CheckResult } from '../src/check'
import { ScoreResponse } from '../src/api'
import { AuthorResult } from '../src/comment'

function makeResult(
  username: string,
  grade: string,
  value: number,
): AuthorResult {
  return {
    username,
    response: {
      version: 'v0.6.3',
      username,
      provider: 'github',
      score: { grade, value },
      scored_at: '2026-04-15T10:00:00Z',
    },
  }
}

describe('evaluateThreshold', () => {
  test('all authors above threshold → success', () => {
    const results = [makeResult('octocat', 'B+', 0.78)]
    const check = evaluateThreshold(results, 0.5)

    expect(check.conclusion).toBe('success')
    expect(check.title).toContain('All contributors meet')
  })

  test('one author below threshold → failure', () => {
    const results = [
      makeResult('octocat', 'B+', 0.78),
      makeResult('newdev', 'D', 0.32),
    ]
    const check = evaluateThreshold(results, 0.5)

    expect(check.conclusion).toBe('failure')
    expect(check.title).toContain('1 contributor below')
    expect(check.summary).toContain('newdev')
  })

  test('bot authors (score 0) excluded from threshold', () => {
    const results = [
      makeResult('octocat', 'B+', 0.78),
      makeResult('dependabot[bot]', 'F', 0.0),
    ]
    const check = evaluateThreshold(results, 0.5)

    expect(check.conclusion).toBe('success')
    expect(check.summary).toContain('bot')
  })

  test('all bots → neutral', () => {
    const results = [makeResult('dependabot[bot]', 'F', 0.0)]
    const check = evaluateThreshold(results, 0.5)

    expect(check.conclusion).toBe('neutral')
  })

  test('authors with errors excluded from threshold', () => {
    const results = [
      makeResult('octocat', 'B+', 0.78),
      { username: 'unknown', error: 'No score available' },
    ]
    const check = evaluateThreshold(results, 0.5)

    expect(check.conclusion).toBe('success')
    expect(check.summary).toContain('unknown')
  })
})
```

**Step 2: Run test to verify it fails**

Run: `npx jest __tests__/check.test.ts --verbose`
Expected: FAIL — cannot find module `../src/check`

**Step 3: Write implementation**

```typescript
// src/check.ts
import { AuthorResult } from './comment'

export interface CheckResult {
  conclusion: 'success' | 'failure' | 'neutral'
  title: string
  summary: string
}

export function evaluateThreshold(
  results: AuthorResult[],
  minScore: number,
): CheckResult {
  const lines: string[] = []
  const failing: string[] = []
  let hasNonBot = false

  for (const r of results) {
    if (r.error) {
      lines.push(`⚠️ ${r.username}: ${r.error} (skipped)`)
      continue
    }

    const s = r.response!
    const isBot = s.score.value === 0 && s.score.grade === 'F'

    if (isBot) {
      lines.push(`⊘ ${s.username}: bot (skipped)`)
      continue
    }

    hasNonBot = true

    if (s.score.value >= minScore) {
      lines.push(`✅ ${s.username}: ${s.score.grade} (${s.score.value.toFixed(2)})`)
    } else {
      lines.push(
        `❌ ${s.username}: ${s.score.grade} (${s.score.value.toFixed(2)}) — below threshold ${minScore.toFixed(2)}`,
      )
      failing.push(s.username)
    }
  }

  const summary = lines.join('\n')

  if (!hasNonBot) {
    return {
      conclusion: 'neutral',
      title: 'All PR authors are bots — threshold check skipped',
      summary,
    }
  }

  if (failing.length > 0) {
    return {
      conclusion: 'failure',
      title: `${failing.length} contributor${failing.length > 1 ? 's' : ''} below minimum score (${minScore.toFixed(2)})`,
      summary,
    }
  }

  return {
    conclusion: 'success',
    title: `All contributors meet minimum score (${minScore.toFixed(2)})`,
    summary,
  }
}
```

**Step 4: Run test to verify it passes**

Run: `npx jest __tests__/check.test.ts --verbose`
Expected: All 5 tests PASS

**Step 5: Commit**

```bash
git add src/check.ts __tests__/check.test.ts
git commit -S -m "Add check run threshold evaluation with bot exclusion"
```

---

## Task 5: Main Entry Point

**Files:**
- Create: `src/main.ts`

This task wires everything together. The main function is integration-heavy (reads GitHub context, calls Octokit, posts comments) so we test it via the unit-tested components and a manual integration test in Task 7.

**Step 1: Write implementation**

```typescript
// src/main.ts
import * as core from '@actions/core'
import * as github from '@actions/github'
import { fetchScore, APIError } from './api'
import { formatComment, AuthorResult, COMMENT_MARKER } from './comment'
import { evaluateThreshold } from './check'

async function run(): Promise<void> {
  try {
    const token = core.getInput('token', { required: true })
    const apiUrl = core.getInput('api-url')
    const repo = core.getInput('repo')
    const trustedOrgs = core.getInput('trusted-orgs')
    const minScoreRaw = core.getInput('min-score')

    // Validate min-score if provided
    let minScore: number | undefined
    if (minScoreRaw) {
      minScore = parseFloat(minScoreRaw)
      if (isNaN(minScore) || minScore < 0 || minScore > 1) {
        core.setFailed('min-score must be between 0.0 and 1.0')
        return
      }
    }

    const context = github.context
    if (!context.payload.pull_request) {
      core.setFailed('This action only runs on pull_request events')
      return
    }

    const prNumber = context.payload.pull_request.number
    const ghToken = process.env.GITHUB_TOKEN ?? ''
    const octokit = github.getOctokit(ghToken)

    // Get unique commit authors for this PR
    const authors = await getAuthors(octokit, context, prNumber)
    if (authors.length === 0) {
      core.warning('No commit authors found on this PR')
      return
    }

    core.info(`Scoring ${authors.length} author(s): ${authors.join(', ')}`)

    // Score each author
    const results: AuthorResult[] = []
    const opts = { apiUrl, token, repo, trustedOrgs }

    for (const username of authors) {
      try {
        const response = await fetchScore(username, opts)
        results.push({ username, response })
      } catch (err) {
        if (err instanceof APIError && err.status === 401) {
          core.setFailed('Invalid DevTrace token')
          return
        }
        const msg =
          err instanceof APIError
            ? `API error ${err.status}`
            : 'No score available'
        core.warning(`Failed to score ${username}: ${msg}`)
        results.push({ username, error: msg })
      }
    }

    // Set outputs from first successful result
    const first = results.find((r) => r.response)
    if (first?.response) {
      core.setOutput('score', first.response.score.value.toString())
      core.setOutput('grade', first.response.score.grade)
      core.setOutput('risk-summary', first.response.risk_summary ?? '')
    }

    // Post or update PR comment
    const body = formatComment(results, apiUrl)
    await upsertComment(octokit, context, prNumber, body)

    // Create check run if min-score is set
    if (minScore !== undefined) {
      const check = evaluateThreshold(results, minScore)
      await createCheckRun(octokit, context, check)

      if (check.conclusion === 'failure') {
        core.setFailed(check.title)
      }
    }
  } catch (err) {
    core.setFailed(err instanceof Error ? err.message : String(err))
  }
}

async function getAuthors(
  octokit: ReturnType<typeof github.getOctokit>,
  context: typeof github.context,
  prNumber: number,
): Promise<string[]> {
  const commits = await octokit.rest.pulls.listCommits({
    owner: context.repo.owner,
    repo: context.repo.repo,
    pull_number: prNumber,
    per_page: 100,
  })

  const authors = new Set<string>()
  for (const commit of commits.data) {
    const login = commit.author?.login
    if (login) {
      authors.add(login)
    }
  }
  return [...authors]
}

async function upsertComment(
  octokit: ReturnType<typeof github.getOctokit>,
  context: typeof github.context,
  prNumber: number,
  body: string,
): Promise<void> {
  const { owner, repo } = context.repo

  // Find existing comment
  const comments = await octokit.rest.issues.listComments({
    owner,
    repo,
    issue_number: prNumber,
    per_page: 100,
  })

  const existing = comments.data.find((c) =>
    c.body?.includes(COMMENT_MARKER),
  )

  if (existing) {
    await octokit.rest.issues.updateComment({
      owner,
      repo,
      comment_id: existing.id,
      body,
    })
    core.info(`Updated existing comment #${existing.id}`)
  } else {
    await octokit.rest.issues.createComment({
      owner,
      repo,
      issue_number: prNumber,
      body,
    })
    core.info('Created new PR comment')
  }
}

async function createCheckRun(
  octokit: ReturnType<typeof github.getOctokit>,
  context: typeof github.context,
  check: { conclusion: string; title: string; summary: string },
): Promise<void> {
  await octokit.rest.checks.create({
    owner: context.repo.owner,
    repo: context.repo.repo,
    name: 'DevTrace Score',
    head_sha: context.sha,
    status: 'completed',
    conclusion: check.conclusion as 'success' | 'failure' | 'neutral',
    output: {
      title: check.title,
      summary: check.summary,
    },
  })
  core.info(`Created check run: ${check.conclusion} — ${check.title}`)
}

run()
```

**Step 2: Verify it compiles**

Run: `npx tsc --noEmit`
Expected: No errors

**Step 3: Build dist**

Run: `npm run build`
Expected: `dist/index.js` created

**Step 4: Run all tests**

Run: `npm test`
Expected: All 15 tests PASS

**Step 5: Commit**

```bash
git add src/main.ts dist/
git commit -S -m "Add main entry point wiring API, comment, and check run"
```

---

## Task 6: README

**Files:**
- Create: `README.md`

**Step 1: Write README**

```markdown
# DevTrace GitHub Action

Score PR contributor trustworthiness using [DevTrace](https://devtrace.thingz.io). Posts a trust score comment on pull requests and optionally enforces a minimum score threshold.

## Quick Start

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
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        with:
          token: ${{ secrets.DEVTRACE_TOKEN }}
```

## Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | Yes | — | DevTrace API token (`dt_` prefix). Get one at [devtrace.thingz.io](https://devtrace.thingz.io). |
| `min-score` | No | — | Minimum score (0.0-1.0). Enables a GitHub Check Run that fails if any contributor scores below threshold. |
| `repo` | No | Current repo | Repository context for scoring (`owner/repo`). |
| `trusted-orgs` | No | — | Comma-separated GitHub org slugs to mark as trusted. |
| `api-url` | No | `https://devtrace.thingz.io` | DevTrace API base URL. |

## Outputs

| Output | Description |
|--------|-------------|
| `score` | Numeric score (0.0-1.0) |
| `grade` | Letter grade (A+ through F) |
| `risk-summary` | Risk summary text |

## Examples

### Enforce minimum score

```yaml
- uses: thingzio/devtrace-action@v1
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  with:
    token: ${{ secrets.DEVTRACE_TOKEN }}
    min-score: '0.5'
```

Add `DevTrace Score` as a required check in your branch protection settings to block merges from low-trust contributors.

### With trusted organizations

```yaml
- uses: thingzio/devtrace-action@v1
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  with:
    token: ${{ secrets.DEVTRACE_TOKEN }}
    trusted-orgs: 'my-org,partner-org'
    min-score: '0.4'
```

### Use outputs in downstream steps

```yaml
- uses: thingzio/devtrace-action@v1
  id: devtrace
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  with:
    token: ${{ secrets.DEVTRACE_TOKEN }}
- if: steps.devtrace.outputs.grade == 'F'
  run: echo "::warning::Low trust contributor"
```

## How It Works

1. Extracts all commit authors from the pull request
2. Scores each author via the [DevTrace API](https://devtrace.thingz.io)
3. Posts (or updates) a single PR comment with scores and risk summaries
4. If `min-score` is set, creates a GitHub Check Run with pass/fail status

Bot authors (score 0, grade F) are automatically excluded from threshold checks.

## Permissions

| Permission | When |
|-----------|------|
| `pull-requests: write` | Always (to post comments) |
| `checks: write` | When `min-score` is set (to create check runs) |

The action uses `GITHUB_TOKEN` for GitHub API calls (comments, check runs) and your DevTrace `token` for scoring API calls.

## License

Apache-2.0
```

**Step 2: Commit**

```bash
git add README.md
git commit -S -m "Add README with usage examples and input reference"
```

---

## Task 7: Release Workflow

**Files:**
- Create: `.github/workflows/release.yml`

**Step 1: Write release workflow**

```yaml
# .github/workflows/release.yml
name: Release
on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'npm'
      - run: npm ci
      - run: npm run build
      - name: Verify dist is clean
        run: git diff --exit-code dist/
      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          generate_release_notes: true
      - name: Update major version tag
        run: |
          VERSION=${GITHUB_REF#refs/tags/}
          MAJOR=$(echo "$VERSION" | cut -d. -f1)
          git tag -f "$MAJOR"
          git push origin "$MAJOR" --force
```

The `Update major version tag` step keeps `v1` pointing to the latest `v1.x.x` release, so `uses: thingzio/devtrace-action@v1` always gets the latest patch.

**Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -S -m "Add release workflow with major version tag update"
```

---

## Task 8: Final Verification

**Step 1: Run full test suite**

Run: `npm test`
Expected: All tests PASS, coverage ≥ 80% on `api.ts`, `comment.ts`, `check.ts`

**Step 2: Run lint**

Run: `npm run lint`
Expected: No errors

**Step 3: Build dist**

Run: `npm run build`
Expected: `dist/index.js` is up to date

**Step 4: Verify repo structure**

Run: `ls -la`
Expected: All files from design doc present

**Step 5: Tag initial release**

```bash
git tag -a v1.0.0 -m "Initial release"
git push origin main --tags
```
