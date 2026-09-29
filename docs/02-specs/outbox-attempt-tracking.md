# Spec: Outbox publish attempts, last error, and parking permanent failures

- **Status:** APPROVED (owner, 2026-09-30: track attempts; park permanent failures as FAILED after 10 attempts; ADR-0004 amended)
- **Owner (human):** Thapanut L.
- **Related:** ADR-0004 (amendment 1), specs [payment-events-outbox](payment-events-outbox.md), [ordering-consumer-dlq](ordering-consumer-dlq.md) (split from the same request); table `outbox`; contract `publishPaymentStatusChanged` (wire format unchanged)
- **Size:** S–M

## 1. Goal
Make an outbox message that keeps failing to publish visible — how many times it was tried and why it last failed — and stop a message the broker will never accept from blocking the others forever. Success metric: for any unpublished message, one SQL query shows its status, attempts, and last error; a permanently rejected message stops being retried after 10 attempts while every other message keeps flowing.

## 2. Scope
- In: `status` (`PENDING` / `PUBLISHED` / `FAILED`), `attempts`, `last_error`, `last_attempt_at` on `outbox`; per-message publish results from the Kafka adapter; the relay marks the messages that were published and records a failed attempt for the others in the same transaction; a message whose failure is **permanent** is parked as `FAILED` once it reaches 10 attempts; ERROR logs at the threshold and when parking.
- **Out of scope:** parking on transient failures (broker down, timeouts: retried forever, as before); automatic re-queueing of `FAILED` messages (an Admin re-queues with SQL, see §3); a retention job for published messages; metrics.

## 3. Design notes (from Architect)
- **Per-message results.** Kafka can accept part of a batch (`kafka-go` returns `WriteErrors`, one entry per message). The publisher port reports this with `port.PublishError{Failed map[id]error}`; any other error means the whole batch failed. The adapter wraps broker errors that retrying cannot fix with `port.ErrPermanentPublish`: non-retriable Kafka error codes (`Error.Temporary() == false`, e.g. `MESSAGE_TOO_LARGE`, `INVALID_TOPIC_EXCEPTION`, `INVALID_RECORD`) and the client-side "message too large" check. For the latter the adapter still writes the other messages of the batch.
- **One transaction.** The relay claims, publishes, and in the same transaction marks the published messages `PUBLISHED` and records a failed attempt for the others, then commits (previously a failure rolled the whole batch back). If the transaction itself fails, nothing is recorded and the batch is retried (at-least-once, duplicates possible as before).
- **Parking rule.** A failed attempt sets `attempts = attempts + 1`, `last_error` (error text, ≤ 512 chars, never payload), `last_attempt_at`. If the failure is permanent and `attempts ≥ 10`, `status = FAILED`: the relay no longer claims it, and one ERROR "outbox message parked" is logged with `event_id`, `topic`, `attempts`, `last_error`. Transient failures are never parked; an ERROR "outbox message not publishable" is logged when `attempts` reaches 10 and every 10 after.
- **Re-queue (Admin).** After fixing the cause: `UPDATE outbox SET status = 'PENDING', attempts = 0 WHERE id = '<event_id>' AND status = 'FAILED';` The event id is unchanged, so consumers deduplicate as usual.
- **Port changes.** `OutboxRepository.MarkPublished` sets `status = PUBLISHED`; new `RecordFailedAttempts(ctx, []FailedAttempt, at, maxAttempts) ([]OutboxAttempt, error)` returns the new attempt counts and which messages were parked, so the relay can log. `OutboxRecord` gains `Status`. `ClaimPending` claims `status = PENDING` only.
- **Demo.** `getDemoPaymentEvents` returns `status` for each outbox record, and the demo page treats a `FAILED` event as settled (stops polling and says the event was parked).

## 4. Contract / schema
- Uses: table `outbox`, ports `OutboxRepository` / `MessagePublisher`, `publishPaymentStatusChanged` (wire format unchanged), `getDemoPaymentEvents`.
- **Changes (approved with this spec):**
  - Migration `0008_outbox_status_attempts`: add `status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PUBLISHED','FAILED'))`, `attempts int NOT NULL DEFAULT 0`, `last_error text`, `last_attempt_at timestamptz`; backfill `status = 'PUBLISHED'` where `published_at IS NOT NULL`; replace the partial index `ix_outbox_unpublished` with `ix_outbox_pending ON (created_at, id) WHERE status = 'PENDING'`. Down reverses it.
  - `contracts/openapi.yaml` (demo-only schema, unreleased): `DemoPaymentEvents.events[].status` (`PENDING` / `PUBLISHED` / `FAILED`).
  - `docker-compose.yml` init list and the integration-test migration list include 0008. No env var changes.

## 5. Acceptance criteria
| ID | Given | When | Then |
|---|---|---|---|
| AC-01 | 3 pending messages | The whole batch fails (broker down) | In one transaction each gets `attempts` +1, `last_error`, `last_attempt_at`; all stay `PENDING`; none is marked published |
| AC-02 | AC-01 | The next publish succeeds | They become `PUBLISHED` with `published_at`; `attempts` and `last_error` keep their last values |
| AC-03 | A batch of 3 where Kafka rejects only the 2nd | The relay publishes it | The 1st and 3rd are `PUBLISHED`; the 2nd has `attempts` +1 and stays `PENDING`; the transaction commits |
| AC-04 | A message rejected permanently (e.g. too large) with `attempts = 9` | It fails again | `attempts = 10`, `status = FAILED`; one ERROR "outbox message parked" with `event_id`, `topic`, `attempts`, `last_error`, no payload; it is never claimed again |
| AC-05 | A message failing transiently with `attempts = 9` | It fails again | `attempts = 10`, still `PENDING`, ERROR "outbox message not publishable"; it keeps being retried every poll |
| AC-06 | A permanently rejected message in a batch | The relay publishes the batch | The other messages of the batch are still published (not blocked) |
| AC-07 | A broker error longer than 512 chars | It is recorded | `last_error` is truncated to 512 chars and contains no payload bytes |
| AC-08 | Recording the outcome fails (DB down) | A batch was published | The transaction rolls back; nothing is marked; the batch is retried (duplicates are deduplicated downstream) |
| AC-09 | `STORE=memory` | A batch fails | The memory outbox records attempts and parks exactly like PostgreSQL |
| AC-10 | A `FAILED` message | An Admin runs the re-queue SQL from §3 | The relay publishes it on the next poll with the same event id |
| AC-11 | Migration 0008 on a database with published and unpublished rows | Up, then down, then up | Published rows are `PUBLISHED`, unpublished `PENDING`; down removes the columns and restores `ix_outbox_unpublished` |
| AC-12 | Demo enabled | `GET /v1/demo/payments/{invoiceNo}/events` | Each event has `status`; the demo page stops polling on `FAILED` |

## 6. Non-functional
- Performance: no extra statement on full success; one UPDATE per failed batch; the claim uses the new partial index.
- Security / PII: `last_error` never includes payload (AC-07).
- Audit / observability: `SELECT id, topic, status, attempts, last_error, last_attempt_at FROM outbox WHERE status <> 'PUBLISHED' ORDER BY created_at` shows the backlog, why, and what is parked; ERROR logs at the threshold and on parking.
- Idempotency / consistency: at-least-once is kept for every message that is not parked. A parked message is not lost: it stays in the outbox with its payload until an Admin re-queues it (ADR-0004 amendment 1).

## 7. Threats & mitigations
| Threat (STRIDE) | Likelihood | Impact | Mitigation | AC |
|---|---|---|---|---|
| I: broker errors echo message content into the DB or logs | L | M | Truncate; record error text only, never payload | AC-07 |
| D: one unpublishable message blocks its batch forever | M | H | Per-message results; the others are published; permanent failures parked after 10 attempts | AC-03, AC-04, AC-06 |
| D/reconciliation: a broker outage parks valid events and orders never confirm | M | H | Only permanent failures are parked; transient ones retried forever | AC-05 |
| Reconciliation: a parked payment event is forgotten | M | H | ERROR on parking; parked rows stay queryable with payload; Admin re-queue | AC-04, AC-10 |

## 8. Open questions
- [x] Park permanently failing messages? Yes, as `FAILED` after 10 attempts (owner, 2026-09-30; ADR-0004 amendment 1).
- [x] Threshold: 10 attempts (owner, 2026-09-30).
- [ ] Where does the "parked" ERROR page someone (no alerting stack yet)?
