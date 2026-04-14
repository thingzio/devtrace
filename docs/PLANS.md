# DevTrace — Plan & Feature Breakdown

## Plans

### Free

GitHub OAuth sign-up required. Base scoring with generous limits for individual use.

| Capability | Details |
|-----------|---------|
| Contributor scoring | Score + grade + categories + full signal breakdown |
| Risk summary | Template-based (no Claude) |
| Rate limit | 60 requests/hour per IP |
| Behavioral signals | From GH Archive (when available) |
| License analysis | Not available |
| AI sensing | Not available |
| API keys | 1 |

### Starter (Paid)

Everything in Free plus AI-powered analysis and higher limits.

| Capability | Details |
|-----------|---------|
| Contributor scoring | Full signals + Claude risk narratives |
| Risk summary | Claude-powered (Haiku) with template fallback |
| AI sensing | Tier 1 (metadata: bot detection, commit trailers) |
| PR authenticity | Claude-powered classification (Starter+) |
| License analysis | Basic distribution (when implemented) |
| Rate limit | Higher (TBD) |
| API keys | Multiple |

### Pro (Paid)

Everything in Starter plus deep analysis and integration features.

| Capability | Details |
|-----------|---------|
| AI sensing | Tier 1 + Tier 2 (behavioral heuristics, deferred) + Tier 3 (Claude, deferred) |
| License analysis | Full with annotations (deferred) |
| Batch API | Bulk contributor scoring (deferred) |
| Webhooks | Score change notifications (deferred) |
| Risk alerts | Email + webhook (deferred) |

---

## Feature Matrix — Current Implementation

| Feature | Free | Starter | Pro |
|---------|------|---------|-----|
| Score + grade | Yes | Yes | Yes |
| Category breakdown | Yes | Yes | Yes |
| Signal breakdown | Yes | Yes | Yes |
| Risk summary (template) | Yes | Yes | Yes |
| Risk summary (Claude) | - | Yes | Yes |
| Behavioral signals | Yes | Yes | Yes |
| AI sensing (Tier 1) | - | Yes | Yes |
| PR authenticity (Claude) | - | Yes | Yes |
| Repo context signals | Yes | Yes | Yes |
| API keys | 1 | Multiple | Multiple |

### Deferred Features

| Feature | Target Plan | Status |
|---------|------------|--------|
| License distribution | Starter+ | Model exists, implementation pending |
| AI sensing Tier 2 (behavioral) | Pro | Data exists (GH Archive), computation pending |
| AI sensing Tier 3 (Claude analysis) | Pro | Integration point exists, prompts pending |
| Batch API | Pro | Not started |
| Webhooks | Pro | Not started |
| Risk alerts (email/webhook) | Starter+ | Not started |
| Overage billing | Starter+ | Not started |
| Quota response headers | All | Not started |
| CSV/JSON export | Starter+ | Not started |

---

## Throttling & Quota

### Current Implementation

- **Rate limiting**: IP-based, enforced in middleware. Returns `429 Too Many Requests`.
- **Configurable**: `SCORE_RATE_LIMIT` (default 60/hour), `OAUTH_RATE_LIMIT` (default 20/minute)
- **Contributor quota**: Tracked per tenant per billing period via `usage_record` table
- **Plan enforcement**: Plan-aware response filtering in score handler

### Not Yet Implemented

- `X-RateLimit-*` / `X-Quota-*` response headers
- Per-request overage billing beyond quota
- Configurable spend caps
- Per-API-key rate limiting (currently per-IP only)

---

## Open

- Exact numeric limits per plan (contributors/month, rate limits)
- Pricing per plan
- Per-request overage rates
- Whether Free plan requires sign-up (currently yes — GitHub OAuth)
- Spend cap defaults for overage
