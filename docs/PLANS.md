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
| AI sensing | Tier 1 (metadata: bot detection, commit trailers, tool signatures) |
| Score history | 30 days |
| License analysis | Not available |
| API keys | 1 |

### Starter (Paid)

Everything in Free plus AI-powered analysis and higher limits.

| Capability | Details |
|-----------|---------|
| Contributor scoring | Full signals + Claude risk narratives |
| Risk summary | Claude-powered (Haiku) with template fallback |
| AI sensing | Tier 1 (metadata) + PR authenticity (Claude-powered) |
| License analysis | Basic distribution (when implemented) |
| Score history | 90 days |
| Rate limit | 300 requests/hour |
| API keys | Multiple |

### Pro (Paid)

Everything in Starter plus deep analysis and integration features.

| Capability | Details |
|-----------|---------|
| AI sensing | Tier 1 + Tier 2 (behavioral heuristics) + Tier 3 (Claude, deferred) |
| Batch API | Bulk contributor scoring — portfolio-level risk assessment |
| Webhooks | Score change notifications (deferred) |
| Risk alerts | Email + webhook (deferred) |
| Score history | 365 days |
| Rate limit | 1,000 requests/hour |
| API keys | Multiple |

### Enterprise (Paid — addresses [$10K+/yr market gap](COMP.md#strategic-whitespace))

Everything in Pro plus compliance, governance, and organizational controls. Competes with NetRise Provenance, Apiiro, and Arnica at the enterprise level where no self-serve contributor trust tool exists today.

| Capability | Details |
|-----------|---------|
| SSO | SAML/OIDC integration |
| Audit logs | Full API access and scoring audit trail |
| Compliance reports | Exportable contributor trust evidence for [NIST SSDF](https://csrc.nist.gov/pubs/sp/800/218/final) (PS/PO) and [EU CRA](https://digital-strategy.ec.europa.eu/en/policies/cyber-resilience-act) due-diligence |
| Custom scoring policies | Configurable category weights and thresholds |
| Batch API | Bulk scoring with higher concurrency |
| Dedicated support | SLA-backed response times |
| Score history | Unlimited |
| Rate limit | Custom |

---

## Feature Matrix — Current Implementation

| Feature | Free | Starter | Pro | Enterprise |
|---------|------|---------|-----|------------|
| Contributor Scoring | Score + Grade + Signals | Score + Grade + Signals | Score + Grade + Signals | Score + Grade + Signals |
| Risk Summary | Metrics-based | AI-powered | AI-powered | AI-powered |
| AI Sensing | Tier 1 (metadata) | Tier 1 + PR authenticity | Tier 1 + Tier 2 (behavioral) | Tier 1 + Tier 2 (behavioral) |
| Score History | 30 days | 90 days | 365 days | Unlimited |
| Rate Limit | 60 req/hour | 300 req/hour | 1,000 req/hour | Custom |
| API Keys | 1 | Multiple | Multiple | Multiple |
| Batch API | — | — | Yes | Yes (high concurrency) |
| Webhooks | — | — | Yes | Yes |
| Risk Alerts | — | Email | Email + Webhook | Email + Webhook |
| Compliance Reports | — | — | — | [NIST SSDF](https://csrc.nist.gov/pubs/sp/800/218/final) + [EU CRA](https://digital-strategy.ec.europa.eu/en/policies/cyber-resilience-act) |
| SSO | — | — | — | SAML/OIDC |
| Audit Logs | — | — | — | Full |
| Synthetic Contributor Detection | — | — | Flags + details | Flags + details |
| Custom Scoring Policies | — | — | — | Yes |

### Deferred Features (prioritized per [COMP.md](COMP.md))

| Priority | Feature | Target Plan | Status | Competitive Rationale |
|----------|---------|------------|--------|----------------------|
| ~~P1~~ | ~~AI sensing Tier 2 (behavioral)~~ | ~~Pro~~ | **Implemented** — velocity anomaly, hour spread, burst-vanish, synthetic flags | — |
| **P1** | Batch API | Pro+ | Not started | Validates portfolio-level scoring that [NetRise Provenance](COMP.md#tier-1-direct--near-competitors-contributor-level-risk) pursues at enterprise-only pricing |
| **P2** | Compliance reports | Enterprise | Not started | Unoccupied space; [NIST SSDF](https://csrc.nist.gov/pubs/sp/800/218/final) + [EU CRA](https://digital-strategy.ec.europa.eu/en/policies/cyber-resilience-act) create implicit demand ([COMP.md](COMP.md#regulatory-pressure)) |
| **P2** | SSO (SAML/OIDC) | Enterprise | Not started | Table stakes for enterprise tier |
| **P2** | Audit logs | Enterprise | Not started | Table stakes for enterprise tier |
| **P2** | Quota response headers | All | Not started | Developer experience |
| **P3** | AI sensing Tier 3 (Claude analysis) | Pro | Integration point exists, prompts pending | Deepens differentiation |
| **P3** | Webhooks | Pro | Not started | Integration enabler |
| **P3** | Risk alerts (email/webhook) | Starter+ | Not started | Retention feature |
| **P3** | CSV/JSON export | Starter+ | Not started | Utility |
| **P3** | Overage billing | Starter+ | Not started | Revenue feature |
| **P4** | License distribution | Starter+ | Model exists, deprioritized | SCA tools (Snyk, Mend, ORT) cover this better; not a differentiator ([COMP.md](COMP.md#tier-2-package-level-supply-chain-contributor-data-incidental)) |

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
- Pricing per plan (Free/Starter/Pro self-serve; Enterprise annual contract)
- Per-request overage rates
- Whether Free plan requires sign-up (currently yes — GitHub OAuth)
- Spend cap defaults for overage
- Enterprise pricing model — per-seat vs per-request vs flat annual ([COMP.md](COMP.md) shows competitors range from $10K-$50K+/yr)
- Custom scoring policy design for Enterprise (which weights/thresholds are configurable?)
- Compliance report format — PDF vs structured JSON vs both
- Which [NIST SSDF](https://csrc.nist.gov/pubs/sp/800/218/final) practice groups map to DevTrace signals
