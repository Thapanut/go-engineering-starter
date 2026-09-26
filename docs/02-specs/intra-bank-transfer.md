# Spec: Intra-bank transfer & account inquiry

- **Status:** APPROVED (reference implementation, approved by owner 2026-09-27)
- **Owner (human):** Thapanut L.
- **Related:** ADR-0002, contracts `getAccount`, `createTransfer`, `getTransfer`, problem-statement §1–3
- **Size:** M

## 1. Goal
An authenticated customer can check their own account's balance and move money from an account they own to any active account in the bank exactly once, even when the request is retried. Success metric: 0 duplicate debits.

## 2. Scope
- In: `GET /v1/accounts/{accountId}`, `POST /v1/transfers`, `GET /v1/transfers/{transferId}`, PostgreSQL + in-memory adapters, audit log, dev JWT tooling.
- **Out of scope:** interbank, FX, fees, limits, notifications, Kafka events, account opening.

## 3. Design notes (from Architect)
- Hexagonal per ADR-0002. Business rules live in `internal/core`.
- Money is `int64` minor units (satang); only `THB` is supported.
- One DB transaction per transfer: lock both accounts `FOR UPDATE` in ascending id order, update balances, insert transfer, insert audit.
- Idempotency scope is `(customer, Idempotency-Key)`. Fingerprint = SHA-256 of from, to, amount, currency, reference.
- Accounts the caller does not own are reported as **not found**, never forbidden, so account ids cannot be enumerated.

## 4. Contract / schema
- Uses: `getHealth`, `getAccount`, `createTransfer`, `getTransfer` in `contracts/openapi.yaml`
- Tables: `accounts`, `transfers`, `audit_log` (`migrations/0001_init.up.sql`)
- **Changes (approved with this spec):** new operations and tables above.

## 5. Acceptance criteria
| ID | Given | When | Then |
|---|---|---|---|
| AC-01 | Customer owns account A (1,000.00 THB), account B active | POST transfer A→B 250.00 with a new key | 201; A = 750.00, B +250.00; transfer status `COMPLETED` |
| AC-02 | AC-01 has completed | Same key, same body again | 200 with the **same** transfer id; balances unchanged |
| AC-03 | A key was used | Same key, different amount/destination | 422 `IDEMPOTENCY_KEY_REUSED`; no balance change |
| AC-04 | A has 100.00 | Transfer 100.01 | 422 `INSUFFICIENT_FUNDS`; no balance change, no transfer row |
| AC-05 | Any | Missing/invalid key, amount ≤ 0, from = to, bad id format, unsupported currency, reference > 140 chars, unknown JSON field | 400 `VALIDATION_ERROR` |
| AC-06 | No/invalid/expired bearer token | Any `/v1` call | 401 `UNAUTHORIZED` |
| AC-07 | Account A belongs to another customer | GET account A, or transfer from A | 404 `ACCOUNT_NOT_FOUND` (no ownership leak) |
| AC-08 | Destination does not exist | Transfer | 404 `ACCOUNT_NOT_FOUND` |
| AC-09 | Source or destination is `FROZEN`/`CLOSED` | Transfer | 422 `ACCOUNT_INACTIVE` |
| AC-10 | Currency of amount ≠ account currency | Transfer | 422 `CURRENCY_MISMATCH` |
| AC-11 | A transfer succeeds | Inspect audit log | Exactly one `TRANSFER_CREATED` entry with actor, trace id, before/after balances, in the same tx |
| AC-12 | A has 100.00, 50 concurrent transfers of 10.00 with distinct keys | All complete | Exactly 10 succeed; A = 0.00; sum of balances unchanged |
| AC-13 | 20 concurrent requests with the **same** key and body | All complete | Exactly one debit; every success response has the same transfer id |
| AC-14 | Transfer T created by customer X | Customer Y GETs T | 404 `TRANSFER_NOT_FOUND`; X gets 200 |
| AC-15 | Any error | Response | Body matches `Error` schema with `traceId`; no internal details; `X-Trace-Id` header set |
| AC-16 | Any request | Logs | JSON log line with route template, status, latency, trace id; no body, token, or account id |

## 6. Non-functional
- Performance: p95 < 300 ms; request context timeout 5 s; HTTP server read/write timeouts.
- Security / PII: JWT on all `/v1`; body ≤ 16 KiB; strict JSON decoding; ids are opaque UUIDs, not account numbers.
- Audit / observability: AC-11, AC-16.
- Idempotency / consistency: AC-02, AC-03, AC-12, AC-13; `CHECK (balance >= 0)` as a DB safety net.

## 7. Threats & mitigations
| Threat (STRIDE) | Likelihood | Impact | Mitigation | AC |
|---|---|---|---|---|
| S: caller forges identity | M | H | JWT signature + exp + issuer validation | AC-06 |
| T: client tampers amount on retry | M | H | Request fingerprint bound to key | AC-03 |
| R: customer denies transfer | M | M | Audit row with actor, trace id, before/after | AC-11 |
| I: probing other customers' accounts | M | H | Ownership check returns 404 | AC-07, AC-14 |
| I: PII in logs/errors | M | H | Route-template logging, generic 500s | AC-15, AC-16 |
| D: large bodies / slow clients | M | M | Body limit, server timeouts, request timeout | §6 |
| E: debit an account you don't own | M | H | Ownership check inside the locked tx | AC-07 |
| Double spend / replay | H | H | Idempotency key + unique index | AC-02, AC-13 |
| Race on balance | H | H | Row locks in id order, DB check constraint | AC-12 |

## 8. Open questions
- [ ] Per-transaction and daily limits (business owner to define).
- [ ] Idempotency key retention period (currently forever).
- [ ] Should replay return 200 or 201? (Implemented: 200 + `Idempotent-Replayed: true`.)
- [ ] Production IdP: RS256/JWKS instead of the dev HS256 secret.
