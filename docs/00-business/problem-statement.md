# Problem Statement  _(Human-owned)_

> **Reference example.** This describes the sample domain shipped with the starter (internal fund transfer).
> Replace it with your real problem when you start a new project from this template.

## 1. Business problem
Customers need to move money between accounts held at the same bank, instantly and safely. Mobile networks are unreliable, so clients retry. A retry must **never** debit twice. Every movement must be traceable for audit and dispute handling.

## 2. Users & stakeholders
| Role | Need | Pain today |
|---|---|---|
| Retail customer | Transfer from own account to any account in the bank | Duplicate debits when the app retries on timeout |
| Operations / dispute team | Know who moved what, when, and the balances before and after | Evidence is spread across logs |
| Audit / compliance | Immutable trail; no PII in logs | Logs contain account numbers |

## 3. Success metrics
| Metric | Baseline | Target | How measured |
|---|---|---|---|
| Duplicate debits caused by retries | > 0 per month | 0 | Reconciliation report |
| Transfer API latency p95 | — | < 300 ms | APM |
| Transfers with a complete audit entry | — | 100 % | Audit table vs transfers table |

## 4. Constraints
- Budget / deadline: reference implementation, small team
- Compliance (PDPA, BOT IT risk, audit): no real customer data outside production; audit trail for every state change; PII masked in logs
- Legacy systems to integrate: none in this example (core banking is out of scope)
- Team skills: Go, PostgreSQL

## 5. Out of scope
Interbank transfer (PromptPay/ITMX), FX, scheduled transfers, fees, transaction limits, notifications, statement history.
