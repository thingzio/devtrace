# Phase 3: UI — Design Document

Date: 2026-04-13

## Context

DevTrace Phase 1 (API + scoring) and Phase 2 (auth + registration) are complete. The API is functional with 13 packages, 107+ tests, lint clean. This phase adds the web UI as a thin client on top of the existing API.

## Design Decisions

### Visual system

Copy DevPulse's CSS, fonts (Lato), color palette, and layout patterns. DevTrace looks like a sibling product under thingz.io. Custom CSS, no framework. Same external CDN dependencies: Chart.js 4.x (trend charts), jQuery 3.x (minimal DOM usage).

### Template architecture

Go `html/template` with embedded FS (`//go:embed`). Same two-layout pattern as DevPulse:

- **layout.html** — base wrapper for public pages (landing, tos, score card)
- **header.html + footer.html** — dashboard wrapper for authenticated pages

Templates:

| File | Purpose |
|------|---------|
| `layout.html` | HTML head, meta tags, CSS link, wraps `{{template "content" .}}` |
| `header.html` | Dashboard nav (avatar, username, sign out link, nav items) |
| `footer.html` | Dashboard scripts (Chart.js, jQuery, app.js), theme toggle |
| `landing.html` | Marketing hero + "try it" search box + feature cards |
| `scorecard.html` | Contributor score result (unauth: grade only, authed: full signals + chart) |
| `home.html` | Dashboard home (quota, tokens, recently scored contributors) |
| `settings.html` | Profile, plan details, theme toggle |
| `tos.html` | Terms of service + accept button |

Static assets: `static/css/app.css`, `static/js/app.js`, `static/img/` (favicon, og:image).

### Screens

**Landing page** (`/`)
- Hero headline + subtext
- "Try it" search box — JS calls `GET /api/v1/score/{username}` (unauth), renders inline grade badge
- "Sign in with GitHub" CTA
- Feature cards (Reputation Scoring, License Analysis, AI Sensing)

**Score card** (`/score/{username}`)
- Server-rendered full page for direct linking/sharing
- Unauth: letter grade badge, numeric score, "sign up" CTA
- Authed: category breakdown bars, signal details table, risk summary, repo context panel, trend chart (Chart.js line chart from history endpoint)

**Dashboard** (`/dashboard`)
- Welcome header (avatar + username)
- Plan status card (plan name, quota bar)
- API tokens section (list, generate, revoke)
- Recently scored contributors (from `usage_record`, deduplicated, click through to score card)

**Settings** (`/settings`)
- Profile (username, email, avatar — read-only)
- Plan details (limits, "upgrade" placeholder)
- Theme toggle (light/dark/system via localStorage)

**ToS** (`/tos`)
- Terms text + "Accept" button → POST → redirect to dashboard

### Routes

| Method | Path | Auth | Handler | Template |
|--------|------|------|---------|----------|
| `GET` | `/` | None | `landingHandler` | `landing.html` via `layout.html` |
| `GET` | `/score/{username}` | None | `scorecardHandler` | `scorecard.html` via `layout.html` |
| `GET` | `/dashboard` | Session | `dashboardHandler` | `home.html` via `header.html`/`footer.html` |
| `GET` | `/settings` | Session | `settingsHandler` | `settings.html` via `header.html`/`footer.html` |
| `GET` | `/tos` | Session | `tosPageHandler` | `tos.html` via `layout.html` |
| `POST` | `/tos/accept` | Session | `tosAcceptHandler` | redirect |
| `GET` | `/static/*` | None | `http.FileServer` | embedded FS |

### New API endpoint

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/score/{username}/history` | API token | Score history for trend chart |

Reads from `reputation_history` table (already in schema).

### Data flow

- Landing "try it": JS fetch → inline render
- Dashboard token management: JS POST/GET/DELETE → modal/list refresh
- Score card page: server renders using ScoreService directly
- Trend chart: JS fetch history endpoint → Chart.js line chart

### Not in MVP

- Explicit contributor tracking/watchlist management (emerges naturally from API usage)
- Pricing table
- PDF export
- Alert configuration
- Billing/payment UI

## References

- `docs/MVP.md` — canonical MVP definition
- DevPulse templates at `/Users/mchmarny/dev/thingz/devpulse/pkg/server/templates/`
- DevPulse CSS at `/Users/mchmarny/dev/thingz/devpulse/pkg/server/static/css/app.css`
