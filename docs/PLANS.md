# DevTrace — Plan & Feature Breakdown

Initial plan structure. Exact numeric limits and pricing TBD.

## Plans

### Free

No sign-up required. Abuse-protected via IP-based rate limiting.

| Capability | Details |
|-----------|---------|
| Contributor scoring | Base score only (letter grade, e.g., "B+") |
| Response detail | Aggregate score — no signal breakdown, no history |
| Rate limit | Low (e.g., 10 requests/hour per IP) |
| License analysis | Not available |
| AI sensing | Not available |
| Batch API | Not available |
| Webhooks | Not available |
| API keys | Not available |

**Purpose**: Lead magnet. Try before signing up. Enough to see the value, not enough to build on.

**Abuse prevention**: IP-based rate limiting, CAPTCHA challenge after repeated hits.

---

### Starter (Paid, low cost)

GitHub OAuth sign-up. Base scoring with enough volume for individual developers and small teams.

| Capability | Details |
|-----------|---------|
| Contributor scoring | Base scoring (shallow signals + select additional signals) |
| Contributors per month | Included quota (e.g., 200 unique contributors) |
| Rate limit | Moderate (e.g., 120 requests/hour) |
| Response detail | Score + top-level signal summary + confidence |
| License analysis | Basic — license distribution, no annotations |
| AI sensing | Tier 1 (metadata: commit trailers, bot signatures) |
| Historical trends | 3 months |
| Batch API | Not available |
| Webhooks | Not available |
| Risk alerts | Email — configurable score threshold |
| API keys | 1 |
| Export | CSV |
| Overage | Per-request pricing beyond included quota |

**Purpose**: Individual developers, small OSS maintainers, hiring managers, personal due diligence. GitHub Action integration for small projects.

---

### Pro (Paid, higher price)

Everything in Starter plus deep scoring, LLM analysis, batch, and webhooks.

| Capability | Details |
|-----------|---------|
| Contributor scoring | Deep scoring (full ~20 signal breakdown) + LLM-enhanced analysis (Tier 3) |
| Contributors per month | Higher included quota (e.g., 2,000 unique contributors) |
| Rate limit | High (e.g., 1,000 requests/hour) |
| Response detail | Full signals + risk narrative + LLM assessment |
| License analysis | Full — distribution with factual annotations |
| AI sensing | Tier 1 + Tier 2 + Tier 3 (metadata + behavioral + Claude analysis) |
| Historical trends | 12 months |
| Batch API | Yes — bulk contributor scoring |
| Webhooks | Yes — score change notifications |
| Risk alerts | Email + webhook — configurable per contributor |
| API keys | 10 |
| Export | CSV + JSON |
| Overage | Per-request pricing beyond included quota |

**Purpose**: Engineering orgs with CI/CD integration (GitHub Actions), OSPO teams, dependency auditing, supply chain security workflows.

---

## Feature Matrix

| Feature | Free | Starter | Pro |
|---------|------|---------|-----|
| Base score (letter grade) | Yes | Yes | Yes |
| Signal summary | - | Top-level | Full + LLM narrative |
| Deep scoring (~20 signals) | - | - | Yes |
| LLM-enhanced analysis | - | - | Yes |
| License distribution | - | Basic | Full with annotations |
| AI sensing — metadata (Tier 1) | - | Yes | Yes |
| AI sensing — behavioral (Tier 2) | - | - | Yes |
| AI sensing — LLM (Tier 3) | - | - | Yes |
| Historical trends | - | 3 months | 12 months |
| Batch API | - | - | Yes |
| Webhooks | - | - | Yes |
| Risk alerts | - | Email | Email + webhook |
| API keys | - | 1 | 10 |
| Export | - | CSV | CSV + JSON |
| Per-request overage | - | Yes | Yes |

---

## Throttling & Quota Model

### Two axes of limiting

1. **Rate limit** — requests per hour. Enforced per API key. Returns `429 Too Many Requests` with `Retry-After` header.
2. **Contributor cap** — unique contributors scored per billing period (monthly). Enforced per tenant. Returns `403 Forbidden` with quota details when cap reached.

### Per-request overage (Starter and Pro)

When a tenant exceeds their included contributor quota, additional requests are billed per-request rather than hard-blocked. This keeps CI/CD pipelines from breaking mid-month.

- Overage rate TBD (e.g., $0.01/request for Starter, $0.005/request for Pro)
- Configurable spend cap — tenant sets a max monthly overage to prevent surprise bills
- Usage dashboard shows included vs. overage consumption in real time

### API response headers (Starter and Pro)

```
X-RateLimit-Limit: 120
X-RateLimit-Remaining: 98
X-RateLimit-Reset: 1712956800
X-Quota-Limit: 200
X-Quota-Remaining: 143
X-Quota-Reset: 1714521600
X-Quota-Overage: true
```

### Free rate limiting

IP-based with short window. No quota headers — just `429` on abuse.

---

## Metering (Operator-Facing)

Separate from tenant-facing quotas. Internal use for billing, capacity planning, and cost monitoring:

- Requests per tenant per day (by endpoint)
- Unique contributors scored per tenant per billing period
- Included vs. overage request counts
- Deep vs. shallow scoring ratio
- LLM analysis invocations and token usage
- Cache hit rate (contributor scoring, license, AI sensing)
- API key usage distribution

---

## Plan Enforcement

Follows DevPulse pattern: limits stored on tenant record, checked in middleware/handlers.

- **Rate limiting**: middleware layer, checked before handler executes
- **Contributor cap**: checked at scoring time, increments usage counter, switches to overage billing beyond cap
- **Feature gating**: handler checks plan capabilities before executing (e.g., batch endpoint returns 403 for Starter)
- **Data retention**: query layer caps historical lookback based on plan

---

## Open

- Exact numeric limits per plan (contributors/month, rate limits)
- Pricing per plan ($X/month)
- Per-request overage rates
- Annual discount
- Whether Starter requires credit card upfront
- Default spend cap for overage
