# Code Review — 2C2P Idempotent Payment Webhook

**Branch:** `feat/2c2p-webhook-idempotency`
**Reviewed:** 2026-09-27 (updated 2026-09-27 after follow-up refactors, see §5)
**Reviewer:** Claude (AI agent)
**Verdict: PASS** — `make verify` all green, all ACs satisfied, no critical issues.

---

## 1. Business Flow

### 1.1 Happy path (PENDING → SUCCESS)

```
2C2P server
    │
    │  POST /webhooks/2c2p
    │  { "payload": "<JWT signed HS256>" }
    ▼
HTTP adapter  (Fiber, public route — no JWT auth)
    │  c.Body() forwarded raw
    ▼
WebhookService.HandlePaymentNotification(ctx, rawBody)
    │
    ├─ 1. Verifier.Verify(rawBody)
    │       parse JSON envelope → extract JWT
    │       check alg == "HS256" (rejects "none", "HS512", etc.)
    │       HMAC-SHA256(header.payload, secretKey) constant-time compare
    │       decode claims → check merchantID matches config (plain compare; not a secret)
    │       parse amount string → int64 minor units, non-negative ≤2dp (never float)
    │       return PaymentNotification{InvoiceNo, ProviderRef, Amount, Outcome}
    │
    ├─ 2. notification.Validate()
    │       check required fields present, outcome is SUCCESS|FAILED
    │       (amount format/sign is the adapter's job, above; a wrong amount is
    │        caught as PAYMENT_MISMATCH in step 3, not here)
    │       (any error → 400 VALIDATION_ERROR, no DB access)
    │
    └─ 3. TxManager.WithinTx(...)
            BEGIN
            SELECT * FROM payments WHERE invoice_no = ? FOR UPDATE  ← row lock
            Payment.Apply(notification, now)
            │   Amount matches? → else 422 PAYMENT_MISMATCH, ROLLBACK
            │   Status PENDING? → set SUCCESS/FAILED, return PROCESSED
            │   Status terminal, same outcome + same tranRef? → return DUPLICATE
            │   Status terminal, different outcome? → return CONFLICT_IGNORED
            │
            if PROCESSED:
                UPDATE payments SET status=?, provider_ref=?, … WHERE id=? AND status='PENDING'
                (conditional update: second guard if row lock raced)
            COMMIT

    200 OK { "status": "OK", "outcome": "PROCESSED" }
```

### 1.2 Duplicate delivery (idempotency)

```
2C2P retries the same notification (already SUCCESS)
    ▼
SELECT … FOR UPDATE → row found, Status = SUCCESS
Payment.Apply → same outcome + same tranRef → OutcomeDuplicate
No UPDATE issued
COMMIT (empty tx)
    ▼
200 OK { "status": "OK", "outcome": "DUPLICATE" }
```

### 1.3 Concurrent deliveries (race condition)

```
20 goroutines post the same notification simultaneously
    ▼
All 20 hit SELECT … FOR UPDATE
PostgreSQL serializes them: one acquires the lock, the rest wait
First: Status=PENDING → PROCESSED, UPDATE committed
Remaining 19: acquire lock one by one, Status=SUCCESS, same tranRef → DUPLICATE
Result: exactly 1 DB write, 19 duplicates — proven by TestAC10
```

### 1.4 Error paths

| Scenario | Where rejected | HTTP |
|---|---|---|
| JWT bad/missing/forged | Verifier.Verify | 401 INVALID_SIGNATURE |
| alg ≠ HS256 | Verifier.Verify | 401 INVALID_SIGNATURE |
| merchantID mismatch | Verifier.Verify | 401 INVALID_SIGNATURE |
| Required field empty | notification.Validate() | 400 VALIDATION_ERROR |
| Amount bad format / >2 decimal places | parseMinorUnits | 400 VALIDATION_ERROR |
| invoiceNo unknown | GetByInvoiceNoForUpdate | 404 NOT_FOUND |
| Amount/currency mismatch | Payment.Apply | 422 PAYMENT_MISMATCH |
| Conflicting terminal outcome | Payment.Apply | 200 CONFLICT_IGNORED + warn log |

---

## 2. Architecture Conformance (ADR-0002 Hexagonal)

```
cmd/api/main.go  ← composition root only; no business logic
│
├── internal/platform/config     env vars, validated at startup
├── internal/platform/auth       JWT bearer (inbound only, /v1 routes)
├── internal/platform/logger     structured JSON, no PII
│
├── internal/core/domain         Payment, Money, PaymentNotification, errors
│                                stdlib only — no framework imports ✓
├── internal/core/port           WebhookUseCase (inbound), WebhookVerifier,
│                                PaymentRepository, TxManager, Clock (outbound)
├── internal/core/service        WebhookService — depends on ports only ✓
│
├── internal/adapter/inbound/httpapi   Fiber handler, error mapping, middleware
│                                       never imports domain errors directly ✓
│                                       public (signature-authenticated) modules are
│                                       registered via Deps.Public, not a JWT group ✓
│
└── internal/adapter/outbound/
    ├── twoc2p/verifier.go     implements WebhookVerifier
    ├── postgres/payment_repo  implements PaymentRepository via GORM
    ├── postgres/tx_manager    implements TxManager (BEGIN/COMMIT/ROLLBACK)
    ├── memory/store           in-process TxManager + PaymentRepository (tests/dev)
    └── system/system.go       real-time Clock
```

Dependency direction: all imports point inward. No domain package imports a framework. `depguard` enforces this at `make lint`.

---

## 3. Security Review

| Control | Implementation | Status |
|---|---|---|
| Signature algorithm pinned | `h.Alg != "HS256"` rejects "none", "HS512" | ✓ |
| Constant-time HMAC compare | `hmac.Equal(sig, mac.Sum(nil))` | ✓ |
| Merchant ID verified | `c.MerchantID != v.merchantID` (plain compare — merchantID is not secret, only the HMAC signature is) | ✓ |
| Money never uses float | `parseMinorUnits` does string arithmetic, regex-validated | ✓ |
| PII out of logs | `accessLog` logs route template, not path/body/query | ✓ tested by AC-11 |
| Secret from env only | `TWOC2P_SECRET_KEY` ≥ 32 bytes enforced in config.Load() | ✓ |
| Body size limit | `BodyLimit: 16 KiB` in Fiber config | ✓ |
| No DB access on bad sig | tx never opened if Verify fails | ✓ tested by AC-05 |
| Conditional UPDATE guard | `WHERE id=? AND status='PENDING'` — second guard after row lock | ✓ |

One open item from spec:
- `cardNo` field is received in the JWT body but intentionally **not stored** (only `tranRef` and `respCode` are persisted). The masked `411111XXXXXX1111` value is also excluded from logs (verified by `TestWebhookAC11_LogsContainNoPayloadData`).

---

## 4. Test Coverage

### Unit tests (`make test`)

| Package | Coverage |
|---|---|
| `internal/core/service` | **96.0%** |
| `internal/adapter/inbound/httpapi` | **96.0%** |
| `internal/adapter/outbound/twoc2p` | **90.6%** |
| `internal/core/domain` | **94.1%** |

### Acceptance criteria coverage

| AC | Unit test | HTTP test | Integration test |
|---|---|---|---|
| AC-01 PENDING→SUCCESS | TestAC01 | TestWebhookAC01_AC03 | TestIntegration_AC01_AC03 |
| AC-02 PENDING→FAILED | TestAC02 | — | — |
| AC-03 Duplicate ack | TestAC03 | TestWebhookAC01_AC03 | TestIntegration_AC01_AC03 |
| AC-04 Conflict ignored + warn | TestAC04 | — | — |
| AC-05 Bad signature 401 | TestAC05 | TestWebhookAC05 | — |
| AC-06 Wrong merchant | covered by `TestVerifyRejectsUntrustedBodies` | — | — |
| AC-07 Unknown invoice 404 | TestAC07 | TestWebhookAC07_AC08_AC09 | — |
| AC-08 Amount mismatch 422 | TestAC08 | TestWebhookAC07_AC08_AC09 | TestIntegration_AC08 |
| AC-09 Validation error 400 | TestAC09 | TestWebhookAC07_AC08_AC09 | — |
| AC-10 Concurrent → 1 write | TestAC10 | — | TestIntegration_AC10 |
| AC-11 No PII in logs | — | TestWebhookAC11 | — |

All 11 ACs have at least one test. AC-10 concurrency is tested at both in-memory (mutex) and PostgreSQL (row lock) levels.

### `make verify` output (2026-09-27)

```
===== VERIFY SUMMARY =====
build        PASS
test         PASS
integration  PASS
lint         PASS
gosec        PASS
vulns        PASS
secrets      PASS
contract     PASS
RESULT: PASS
```

Contract check: 6 warnings for unused components in `openapi.yaml` (pre-existing stubs — `Money`, `Unauthorized`, `Conflict`, etc.). These are stubs for future features; no action required now.

---

## 5. Code Quality Observations

### Strengths

- **Idempotency is layered**: row lock (serialises concurrent access) + domain `IsTerminal()` check (guards logic) + conditional `UPDATE WHERE status='PENDING'` (guards DB). Three independent guards.
- **No float money**: `parseMinorUnits` uses regex + string arithmetic. Handles both `"230.87"` (string JSON) and `230.87` (number JSON).
- **Error mapping is centralised**: `toAPIError()` in `httpapi/errors.go` is the single place domain errors become HTTP codes. Adding a new feature only requires one new `case`.
- **Memory store is a real implementation**: copy-on-write semantics with mutex. Unit tests exercise the same domain logic as integration tests — no mocks for the core.
- **`Sign()` helper in the twoc2p package**: makes test bodies trivially constructible and keeps signing logic auditable in one place.

### Minor observations (no action needed)

1. **`AC-06` not named as its own test**: it is covered inside `TestVerifyRejectsUntrustedBodies` as the `"other merchant"` and `"missing merchantID"` sub-cases. Clear enough, just not labelled AC-06 explicitly.
2. **`cardNo` present in verifier test payload but never extracted**: it is intentionally absent from the `claims` struct in `verifier.go`. The field is silently ignored during JSON unmarshal, which is correct and documented in the spec.

### Follow-up refactors applied after this review

None of these change behaviour; all were cleanups requested in review follow-up and are covered by the same test suite (`make verify` re-run below).

1. **`httpapi.PublicModule` registered explicitly, not via type assertion.** `NewApp` used to probe every `Module` with `m.(PublicModule)`, which forced `WebhookModule` to implement an empty `Register(fiber.Router) {}` just to satisfy `Module`. Public (signature-authenticated) modules are now passed through `Deps.Public []PublicModule` instead, so `WebhookModule` only implements `PublicModule` and the empty method is gone.
2. **`merchantID` compared with `!=`, not `hmac.Equal`.** `merchantID` travels in the clear in the request and isn't a secret — only the JWT's HMAC signature needs constant-time comparison, which is unchanged (`hmac.Equal(sig, mac.Sum(nil))`).
3. **`twoc2p.Validator` renamed to `twoc2p.Verifier`** (and `port.WebhookSignatureValidator` to `port.WebhookVerifier`): the type verifies the HMAC *and* decodes the payload, not just validates a format, so "Verifier" matches what it does. File renamed `validator.go` → `verifier.go`.
4. **Test-only helpers removed from `memory.Store`.** `Payment()` and `OutcomeWrites()` (plus an internal write counter) were production-code surface that existed only for assertions. Tests now read payments through `PaymentRepository.GetByInvoiceNoForUpdate` like a real caller would, and count committed writes with a small decorator (`countingPayments`) defined in the test package itself.
5. **Redundant `amount < 0` check dropped from `PaymentNotification.Validate()`.** The 2C2P adapter already rejects any amount that isn't a non-negative decimal with ≤2 places (`parseMinorUnits`, tested by `TestParseMinorUnits`), and an amount that differs from the stored payment is caught by `Payment.Apply` as `ErrPaymentMismatch` (422) regardless. The domain check could never change an outcome, so it was dead weight — see the updated flow diagram in §1.1.

`make verify` after all five refactors:

```
===== VERIFY SUMMARY =====
build        PASS
test         PASS
integration  PASS
lint         PASS
gosec        PASS
vulns        PASS
secrets      PASS
contract     PASS
RESULT: PASS
```

---

## 6. Open Questions (from spec, still unresolved)

1. **Which 2C2P `respCode`s mean *pending* (not failed)?** Currently only `"0000"` = SUCCESS; every other code = FAILED. Must confirm against the merchant's response-code list before production (especially QR / offline channels).
2. **Should `CONFLICT_IGNORED` return non-2xx to force a 2C2P retry, or open a reconciliation ticket?** Currently ack with 200 + warn log. Needs product decision.
3. **IP allow-listing** of 2C2P callback IPs at the gateway — not implemented here (gateway/infra concern).

---

## 7. Summary

The implementation is complete, correct, and secure. Business logic is confined to the domain and service layers. The signature verification is hardened against algorithm confusion attacks. Idempotency is guaranteed by three independent guards. All 11 acceptance criteria are tested. `make verify` is fully green.

The three open questions above must be resolved before production traffic, but none blocks a code review approval.
