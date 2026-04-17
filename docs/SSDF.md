# NIST SSDF Compliance Mapping — Design

## Problem

NIST SP 800-218 (SSDF) and EU CRA (2024/2847) create implicit demand for contributor vetting evidence in software supply chains. No tool currently maps contributor trust signals to SSDF practices. DevTrace already collects the signals — it just doesn't surface the regulatory relevance.

## Goal

Surface SSDF practice mapping in DevTrace as an informational resource that supports organizational due-diligence obligations. Defensive framing: DevTrace provides additional signals, not compliance determinations.

## Audience

AppSec teams and compliance officers in enterprises who need to demonstrate due diligence around contributor trust as part of a broader SSDF compliance program.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Framing | "Supports due-diligence obligations" | Honest about scope — contributor scoring is one input to compliance, not the whole program |
| Mapping depth | Practice-level + 5 task-level callouts | Defensible and maintainable; concrete enough for auditors without full task-level maintenance burden |
| Delivery | Phased: static page + scorecard now, API + export later | Validates demand before investing in Enterprise API |
| Tone | Defensive — "relevant to", "provides signal for" | Never "satisfies", "meets", or "certifies" |
| Scorecard section visibility | All authenticated plans (Free+) | Educational, not premium; export is the premium feature |

---

## SSDF Practice-to-Signal Mapping

### Coverage: 8 of 20 SSDF practices across 3 of 4 practice groups

| SSDF Practice | Practice Name | DevTrace Evidence | Key Signals |
|---|---|---|---|
| **PS.1** | Protect code from unauthorized access | Contributor authorization level relative to the repo | `author_association`, `org_member`, `trusted_org_member` |
| **PS.2** | Verify software release integrity | Whether the contributor signs their commits | `commits_verified`, code provenance category score |
| **PS.3** | Archive and provide software provenance | Verifiable contributor identity and history | Account age, profile completeness (5 fields), identity category score |
| **PW.4** | Reuse well-secured software | Contributor works across established projects, not just forks | `distinct_repos_90d`, `fork_ratio`, PR acceptance rate |
| **PW.6** | Review human-readable code | Contributor participates in code review | `reviews_given_30d`, review participation score |
| **PW.7** | Test executable code | Contributor consistency suggests disciplined dev practices | `consistency_score`, engagement category score |
| **RV.1** | Identify vulnerabilities | Detect anomalous contributor behavior indicating possible compromise | Velocity anomaly ratio, burst-vanish score, synthetic contributor flags |
| **PO.4** | Security awareness | Contributor maturity and established presence | Account age, community category score, follower/following ratio |

### Task-Level Callouts (5 strongest connections)

| SSDF Task | DevTrace Signal | Why It Maps |
|---|---|---|
| **PS.2.1** — Use code signing | `commits_verified` | Direct evidence: contributor either signs commits or doesn't |
| **PS.3.1** — Track provenance of code components | Identity category (5 signals) | Provenance starts with "who wrote this?" — DevTrace answers that |
| **PS.1.1** — Store code with least-privilege access | `author_association` + `org_member` | Measures whether the contributor has appropriate access level |
| **PW.6.1** — Review code for security | `reviews_given_30d` | Contributors who review code participate in the security review process |
| **RV.1.1** — Gather info about vulnerabilities | Synthetic contributor flags | Detects fabricated contributor patterns (xz-utils style social engineering) |

---

## Phase 1: Static Page + Scorecard Section

### `/compliance` Page

Public, no auth required. Marketing and education asset.

**Structure:**
1. **Header** — "NIST SSDF Due-Diligence Support" with positioning: "DevTrace contributor trust signals provide informational context relevant to 8 of 20 NIST SP 800-218 (SSDF) practices across 3 of 4 practice groups."
2. **Coverage summary** — visual indicator: PS (3/3), PW (3/9), PO (1/5), RV (1/3)
3. **Mapping table** — 8-row practice-level table with expandable task-level callouts for the 5 strongest connections
4. **Disclaimer** — "DevTrace provides contributor-level signals that may be relevant to organizational SSDF due-diligence processes. These signals are informational and do not constitute a compliance determination. Full SSDF compliance requires organizational processes beyond contributor trust scoring."
5. **CTA** — links to sign up (free) and contact for Enterprise

**Implementation:** New `compliance.html` template, same layout system as `/help` and `/changelog`. Static content, no server-side data.

### Scorecard "Regulatory Context" Section

Below existing score categories, above risk summary.

**Display logic:**
- For each of the 8 mapped practices, check if the contributor has relevant signal data
- PS.2 only shows when `commits_verified` is present (repo context)
- Show practice ID + short label + signal status (e.g., "PS.2 — Release Integrity — Commits signed: Yes")
- Practices without applicable data show "No data available" (not pass/fail)
- Link to `/compliance` for full methodology

**Framing:**
- Header tooltip: "These signals may be relevant to organizational due-diligence processes. They are informational and do not constitute a compliance determination."
- No green/red pass/fail — neutral presentation (present/absent/not applicable)
- Language uses "relevant to" and "provides signal for", never "satisfies" or "meets"

**Visibility:** All authenticated plans (Free+).

---

## Phase 2: Enterprise API (deferred)

### API Response Field

Enterprise plan only. New `regulatory_context` object in score response:

```json
{
  "regulatory_context": {
    "framework": "NIST SP 800-218 (SSDF) v1.1",
    "disclaimer": "Informational signals only. Does not constitute a compliance determination.",
    "relevant_practices": [
      {
        "practice": "PS.2",
        "name": "Verify Software Release Integrity",
        "signal": "commits_verified",
        "value": true,
        "relevance": "Contributor signs commits, providing cryptographic evidence of authorship"
      }
    ]
  }
}
```

### Batch Export Endpoint

`GET /api/v1/compliance/report?usernames=a,b,c&format=json`

- Batch contributor scoring with SSDF mapping
- JSON format initially, PDF deferred
- Rate limited separately from scoring endpoint
- Same defensive disclaimer in response

Phase 2 details are intentionally light — design depends on Enterprise tier demand and what compliance officers actually need from the export.

---

## References

- [NIST SP 800-218 (SSDF) v1.1](https://csrc.nist.gov/pubs/sp/800/218/final)
- [SSDF Practice Table](https://csrc.nist.gov/projects/ssdf)
- [EU Cyber Resilience Act (2024/2847)](https://digital-strategy.ec.europa.eu/en/policies/cyber-resilience-act)
