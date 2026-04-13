# DevTrace MVP Design

Date: 2026-04-13

## Context

DevTrace is a contributor reputation scoring service — the "contributor credit score" for open source. It answers "Is this contributor trustworthy?" by combining identity, engagement, community, behavioral, and code provenance signals from GitHub.

This design captures the MVP scope, API contract, UI screens, and build phases validated through collaborative design review. The canonical MVP definition lives in `docs/MVP.md`.

## Key Design Decisions

### API-first architecture

The REST API is the product. UI, CLI, and GitHub Action are thin clients. This means:
- API contract is designed before any client
- All business logic lives behind API endpoints
- Clients are interchangeable and independently shippable

### Single enriched endpoint

`GET /api/v1/score/{username}` returns progressively richer data based on the caller's plan. License distribution and AI sensing are nested in the score response, not separate endpoints. This keeps the API surface minimal and the integration story simple.

### Auth via GitHub App installation

All registered users (including Free plan) install the DevTrace GitHub App. This gives DevTrace server-side installation tokens for GitHub API calls — no stored user credentials, and the token pool scales with the user base. DevTrace mints its own opaque API tokens for programmatic access.

### Unauthenticated tier with abuse prevention

1 free fully-populated lookup per IP to showcase capabilities. After that, cached-only responses. Unauthenticated requests never trigger fresh GitHub API calls (except the 1 free lookup), so abuse costs zero API tokens.

### PAT-backed development

During development, the GitHub client interface is backed by a PAT. Swapped to App installation tokens in production. Same interface, config-driven implementation.

### Separate admin service

Operator visibility (tenant management, pipeline health, token pool monitoring) runs in a separate Cloud Run service, same pattern as DevPulse. Does not impact the core offering.

## Decisions deferred

- Batch API, webhooks, and Pro/Enterprise plans
- Tier 2/3 AI sensing (behavioral + LLM)
- License annotations and compliance flagging
- Overage billing, spend caps, and payment UI
- GitHub Action check status gating (opt-in blocking)

## Implementation phases

1. **API + Scoring Engine** — schema, reputer copy, REST API, rate limiting, quota. PAT-backed, local dev.
2. **Auth + Registration** — GitHub OAuth, App installation, token minting, client swap.
3. **UI** — landing page, score card, dashboard, trend charts.
4. **GitHub Action** — API call + PR comment. Marketplace publish.
5. **Admin Service** — separate service for operator visibility.

## References

- `docs/MVP.md` — canonical MVP definition with full API contract and examples
- `docs/SCOPE.md` — full project scope and vision
- `docs/INFRA.md` — infrastructure reuse plan from DevPulse
- `docs/PLANS.md` — plan tiers and feature matrix
