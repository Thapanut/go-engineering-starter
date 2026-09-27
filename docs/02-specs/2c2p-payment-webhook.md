# Spec: 2C2P payment webhook (idempotent)

- **Status:** APPROVED (owner request, 2026-09-27)
- **Owner (human):** Thapanut L.
- **Related:** ADR-0002, ADR-0003, contract `receive2c2pPaymentNotification`, table `payments`
- **Size:** M

## 1. Goal
Record the final outcome of a card/QR payment when 2C2P calls our backend URL. Each payment is transitioned **exactly once**, even though 2C2P retries and may deliver the same notification many times. Success metric: 0 payments with double-applied outcomes; webhook p95 < 300 ms (fast ack).

## 2. Scope
- In: `POST /webhooks/2c2p`, HMAC-SHA256 (JWT HS256) verification, `PENDING → SUCCESS | FAILED` transition, idempotent duplicate handling, PostgreSQL (GORM) and in-memory adapters.
- **Out of scope:** creating payments (payment-token request), refunds, reconciliation job, downstream notifications/events (see [payment-events-outbox](payment-events-outbox.md)), 2C2P inquiry API.

## 3. Design notes (from Architect)
- 2C2P PGW v4 backend notification body: `{"payload":"<JWT>"}`; the JWT is signed **HS256 with the merchant Secret Key** ([2C2P docs](https://developer.2c2p.com/docs/api-payment-response-backend)).
- The **webhook verifier is an outbound port** (`WebhookVerifier`). Its 2C2P adapter verifies the HMAC with a constant-time compare and translates the provider payload into a provider-agnostic `domain.PaymentNotification`. The core never sees 2C2P field names.
- Transaction reference = `invoiceNo` (our payment reference sent to 2C2P). `tranRef` is stored as the provider reference.
- `amount` arrives as a decimal (e.g. `"230.87"`). It is parsed from its string form to `int64` satang and **never via float**.
- One DB transaction: `SELECT … FOR UPDATE` the payment by `invoice_no`, apply the domain transition, then a conditional `UPDATE … WHERE status = 'PENDING'`.
- Terminal states are final. A later notification never changes them.

```mermaid
sequenceDiagram
  participant P as 2C2P
  participant H as HTTP adapter
  participant S as WebhookService
  participant V as WebhookVerifier (2C2P adapter)
  participant DB as payments (one tx)
  P->>H: POST /webhooks/2c2p {"payload": JWT}
  H->>S: HandlePaymentNotification(raw body)
  S->>V: Verify(raw) → PaymentNotification | ErrInvalidSignature
  S->>DB: BEGIN; SELECT … WHERE invoice_no FOR UPDATE
  alt PENDING
    S->>DB: UPDATE status WHERE status='PENDING'; COMMIT
    H-->>P: 200 PROCESSED
  else already terminal (same outcome)
    H-->>P: 200 DUPLICATE (no write)
  else already terminal (different outcome)
    H-->>P: 200 CONFLICT_IGNORED (no write, warn log for reconciliation)
  end
```

## 4. Contract / schema
- Uses: `receive2c2pPaymentNotification` (`POST /webhooks/2c2p`), table `payments` (`migrations/0001_payments`)
- **Changes (approved with this spec):** the new operation, the error codes `INVALID_SIGNATURE` and `PAYMENT_MISMATCH`, and the new table.

## 5. Acceptance criteria
| ID | Given | When | Then |
|---|---|---|---|
| AC-01 | Payment `INV1` is PENDING, 230.87 THB | A validly signed notification arrives with respCode `0000`, amount 230.87 THB | 200 `PROCESSED`; payment is SUCCESS with tranRef and respCode stored |
| AC-02 | Payment is PENDING | A valid notification arrives with a non-success respCode | 200 `PROCESSED`; payment is FAILED |
| AC-03 | AC-01 already processed | The same notification is delivered again | 200 `DUPLICATE`; no write (status, updated_at, and provider ref unchanged) |
| AC-04 | Payment is SUCCESS | A valid notification says FAILED (or vice versa) | 200 `CONFLICT_IGNORED`; no write; warning logged |
| AC-05 | Any | The signature is wrong, alg ≠ HS256, the JWT is malformed, or it is signed with another key | 401 `INVALID_SIGNATURE`; no DB access |
| AC-06 | Valid signature | merchantID ≠ our merchant | 401 `INVALID_SIGNATURE` |
| AC-07 | Valid signature | invoiceNo is unknown | 404 `NOT_FOUND` (2C2P retries; ops alerted) |
| AC-08 | Payment is PENDING 230.87 THB | The notification has a different amount or currency | 422 `PAYMENT_MISMATCH`; no write |
| AC-09 | Valid signature | Amount is not a non-negative decimal with ≤ 2 places, or a required field is missing | 400 `VALIDATION_ERROR` |
| AC-10 | Payment is PENDING | 20 identical notifications arrive concurrently | Exactly one `PROCESSED`; the rest are `DUPLICATE`; one transition in the DB |
| AC-11 | Any | A webhook request is logged | The log has the route template and trace id; no payload, card number, or invoice number |

## 6. Non-functional
- Performance: one indexed row lock + one update; p95 < 300 ms. Downstream work must be async (outbox, see payment-events-outbox).
- Security / PII: the secret key comes only from env (`TWOC2P_SECRET_KEY`); constant-time HMAC compare; `cardNo` (masked by 2C2P) is **not** stored; body ≤ 16 KiB.
- Audit / observability: warn log on `CONFLICT_IGNORED` with trace id and payment id (not the invoice no).
- Idempotency / consistency: row lock + conditional update + terminal-state rule (AC-03, AC-04, AC-10).

## 7. Threats & mitigations
| Threat (STRIDE) | Likelihood | Impact | Mitigation | AC |
|---|---|---|---|---|
| S: forged webhook marks an unpaid order SUCCESS | H | H | HMAC-SHA256 verify, alg pinned to HS256, constant-time compare | AC-05 |
| S: notification for another merchant replayed at us | L | H | merchantID must match config | AC-06 |
| T: amount tampered / wrong order paid | M | H | Amount + currency must equal the stored payment | AC-08 |
| R: dispute over outcome | M | M | Store tranRef, respCode, and updated_at | AC-01 |
| I: PII in logs | M | M | Route-template logging only | AC-11 |
| D: retry storm / large bodies | M | M | Fast ack, body limit, duplicates short-circuit | AC-03 |
| Replay / double processing | H | H | Terminal-state rule + row lock + conditional update | AC-03, AC-10 |

## 8. Open questions
- [ ] **UNCONFIRMED:** which 2C2P `respCode`s mean *pending*, not failed (e.g. QR/offline channels)? Currently only `0000` = SUCCESS and every other code = FAILED. Confirm against the merchant's 2C2P response-code list before production.
- [ ] Should `CONFLICT_IGNORED` return non-2xx to force a 2C2P retry, or open a reconciliation ticket? (Currently ack + warn.)
- [ ] Is IP allow-listing of 2C2P callback addresses required at the gateway?
