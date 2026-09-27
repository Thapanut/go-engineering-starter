# Spec: Payment events to Kafka via transactional outbox

- **Status:** APPROVED (owner request, 2026-09-27; docs drafted with the code for owner review)
- **Owner (human):** Thapanut L.
- **Related:** ADR-0002, ADR-0004, spec [2c2p-payment-webhook](2c2p-payment-webhook.md), contract `contracts/asyncapi.yaml` (`publishPaymentStatusChanged`), table `outbox`
- **Size:** M

## 1. Goal
Tell other services when a payment reaches SUCCESS or FAILED, with **no lost and no phantom events**, and without making the 2C2P webhook slower or dependent on Kafka. Success metric: every committed transition has exactly one outbox row; outbox backlog drains within seconds while Kafka is up.

## 2. Scope
- In: `payment.status-changed` event on topic `payments.v1.status-changed`; `outbox` table; outbox write in the webhook transaction; relay goroutine in `cmd/api`; Kafka publisher (segmentio/kafka-go); memory outbox and in-process publisher; env `KAFKA_BROKERS`, `OUTBOX_POLL_INTERVAL`.
- **Out of scope:** consumers; outbox retention/cleanup job; Kafka TLS/SASL settings; topic provisioning; dead-lettering of messages Kafka rejects; relay metrics.

## 3. Design notes (from Architect)
Decisions in ADR-0004. Summary:

```mermaid
sequenceDiagram
  participant S as WebhookService
  participant DB as PostgreSQL (one tx)
  participant R as OutboxRelay (goroutine per API instance)
  participant K as Kafka
  S->>DB: BEGIN; lock payment; UPDATE payment; INSERT outbox; COMMIT
  loop every OUTBOX_POLL_INTERVAL (immediately again while batches are full)
    R->>DB: BEGIN; SELECT … FROM outbox WHERE published_at IS NULL FOR UPDATE SKIP LOCKED LIMIT 100
    R->>K: WriteMessages (acks=all, key = payment_id)
    alt all acked
      R->>DB: UPDATE outbox SET published_at; COMMIT
    else broker error
      R->>DB: ROLLBACK (retried next poll)
    end
  end
```

- One event per `PROCESSED` outcome only. `DUPLICATE`, `CONFLICT_IGNORED`, mismatches, and rejected signatures add nothing.
- If the outbox insert fails, the payment update rolls back and the webhook returns 500, so 2C2P retries.
- Relay selection in `cmd/api`: `KAFKA_BROKERS` set → Kafka publisher. Unset with `STORE=memory` → in-process publisher (local demo). Unset with `STORE=postgres` → relay disabled with a warning; events wait in the outbox.

## 4. Contract / schema
- Uses: webhook flow from spec 2c2p-payment-webhook; table `payments`.
- **Changes (approved with this spec):** new AsyncAPI contract `contracts/asyncapi.yaml` (channel `payments.v1.status-changed`, message `payment.status-changed`); new table `outbox` (`migrations/0002_outbox`); new env vars `KAFKA_BROKERS`, `OUTBOX_POLL_INTERVAL`; new dependency `github.com/segmentio/kafka-go` (MIT, see ADR-0004).

## 5. Acceptance criteria
| ID | Given | When | Then |
|---|---|---|---|
| AC-01 | Payment is PENDING | A valid notification is PROCESSED (SUCCESS or FAILED) | One outbox message in the same transaction: topic `payments.v1.status-changed`, key = payment id, payload per contract with `occurred_at` = the payment's `updated_at` |
| AC-02 | Any | The outcome is DUPLICATE or CONFLICT_IGNORED, or the request fails (mismatch, bad signature) | No outbox message |
| AC-03 | Payment is PENDING | Writing the outbox message fails | The payment stays PENDING (rollback) and the error is returned; a retry processes normally |
| AC-04 | Payment is PENDING | 20 identical notifications arrive concurrently | Exactly one outbox message |
| AC-05 | Unpublished messages exist | The relay runs | They are published oldest first and marked published; the next run publishes nothing; an empty outbox makes no broker call |
| AC-06 | Unpublished messages exist | The broker fails | Nothing is marked; the messages are published on a later run |
| AC-07 | The broker acked a batch | Marking it published fails | The batch is published again later with the same `event_id` (at-least-once) |
| AC-08 | More messages than the batch size | The relay runs | Each transaction handles at most one batch; `Run` drains a backlog without waiting for the interval and stops on shutdown |
| AC-09 | Any | The relay logs a failure | The log has no payload, payment id, or invoice number |
| AC-10 | Two relays (two API instances) | Both claim at the same time | No message is claimed by both (`SKIP LOCKED`) |

## 6. Non-functional
- Performance: the webhook adds one indexed insert; no network call to Kafka. Relay batch = 100, write timeout 10 s, 3 attempts.
- Security / PII: the payload carries the invoice number and amount (business data, no card data); protect the topic with ACLs. Logs never include payloads.
- Audit / observability: warn log on relay failure; backlog = `SELECT count(*) FROM outbox WHERE published_at IS NULL`.
- Idempotency / consistency: event committed atomically with the state change; at-least-once delivery; consumers deduplicate on `event_id`.

## 7. Threats & mitigations
| Threat (STRIDE) | Likelihood | Impact | Mitigation | AC |
|---|---|---|---|---|
| T: event lost or phantom (dual write) | M | H | Outbox row in the same tx as the state change | AC-01, AC-03 |
| R: duplicate event makes a consumer act twice | M | H | Stable `event_id` in payload and header; contract requires consumer dedup | AC-07 |
| I: payment data in logs | M | M | Relay logs only the error, never payloads | AC-09 |
| I: unauthorized topic readers | M | M | Topic ACLs (out of scope here; ops) | — |
| D: Kafka outage blocks payments | M | H | Webhook never calls Kafka; backlog waits in the outbox | AC-06 |
| Double publish across instances | M | M | `FOR UPDATE SKIP LOCKED` | AC-10 |

## 8. Open questions
- [ ] Retention: how long to keep published outbox rows, and who runs the cleanup job?
- [ ] Kafka TLS/SASL settings per environment (needs new env vars and ADR-0004 update).
- [ ] Dead-letter handling for a message Kafka permanently rejects (currently blocks its batch; alert on backlog age).
- [ ] Topic provisioning (partitions, replication factor, retention) for `payments.v1.status-changed`.
