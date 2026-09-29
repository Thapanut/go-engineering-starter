# Spec: Dead-letter queue and bounded retry for the ordering consumer

- **Status:** APPROVED (owner, 2026-09-30: separate retry topic, DLQ retention 14 days, replay run by an Admin, backoff 1 s / 30 s cap / 5 min stall)
- **Owner (human):** Thapanut L.
- **Related:** ADR-0004, ADR-0005; specs [order-flow-modules](order-flow-modules.md), [payment-events-outbox](payment-events-outbox.md), [outbox-attempt-tracking](outbox-attempt-tracking.md) (split from this one); contract `contracts/asyncapi.yaml` (`consumePaymentStatusChangedForOrders`); tables `ordering.processed_events`, `ordering.orders`
- **Size:** M (consumer DLQ + backoff), S (replay CLI). Split from an L request; outbox attempt tracking is its own spec.

## 1. Goal
No payment event that ordering cannot read is lost or silently skipped, and one bad message can no longer stall a consumer forever. Success metric: every message the consumer does not apply is either in `processed_events` (valid but not applicable) or in the DLQ with the reason; zero messages are only in logs.

## 2. Scope
- In:
  - Classify handler failures as **poison** (the message itself is unusable) or **transient** (a dependency failed).
  - Poison → publish to a DLQ topic with the original key, value, and headers plus diagnostic headers, then commit the offset.
  - Transient → retry with capped exponential backoff and never commit or dead-letter; log each retry and a stall alert.
  - `cmd/dlqreplay`: Admin CLI that republishes DLQ messages to a retry topic that only ordering reads, dry-run by default.
  - The ordering consumer group reads the source topic and the retry topic.
  - AsyncAPI: DLQ and retry channels and messages; Makefile creates both topics in dev.
- **Out of scope:**
  - Changing how valid-but-inapplicable events are handled (unknown order, amount mismatch, conflict with a final order): they stay recorded in `processed_events` and logged (order-flow-modules AC-08–AC-10).
  - Outbox attempts / `last_error` (spec [outbox-attempt-tracking](outbox-attempt-tracking.md)).
  - A DLQ for `STORE=memory` in-process delivery: poison there is logged and skipped as today.
  - Metrics / alerting infrastructure (none exists yet); this spec emits structured logs only.
  - Other consumers of `payments.v1.status-changed`.

## 3. Design notes (from Architect — proposed, confirm on approval)
```mermaid
sequenceDiagram
  autonumber
  participant K as Kafka payments.v1.status-changed
  participant C as Consumer (group "ordering")
  participant H as Handler + ordering tx
  participant D as Kafka ...ordering.dlq
  participant R as Kafka ...ordering.retry
  participant O as Admin (dlqreplay)
  K->>C: FetchMessage (p, n) — group reads source + retry topics
  R->>C: FetchMessage (replayed)
  C->>H: Handle(value)
  alt applied / duplicate / recorded-not-applied
    H-->>C: ok
    C->>K: commit n+1
  else poison (decode or validation)
    H-->>C: ErrPoison(reason)
    C->>D: produce original key+value+headers + dlq-* headers (acks=all)
    alt DLQ write ok
      C->>K: commit n+1
    else DLQ write failed
      C->>C: treat as transient: backoff, retry DLQ write, no commit
    end
  else transient (DB down, timeout)
    H-->>C: error
    C->>C: backoff 1s, 2s, 4s … cap 30s; retry same message; no commit
  end
  O->>D: read (dry-run by default)
  O->>R: republish byte-identical key/value + original headers + dlq-replay-count, commit DLQ offset
```
- **Error classes live in the adapter.** `paymentevents.Handler.Handle` returns `ErrPoison` (wrapping a fixed, payload-free reason) for decode failures and for `kernel.ErrValidation` from the use case, instead of `nil`. Everything else it returns is transient. The core does not change.
- **DLQ topic** `payments.v1.status-changed.ordering.dlq`: one DLQ per consumer group, so another consumer's DLQ never mixes with ordering's. Same key (payment_id), so a payment's poison messages stay ordered. 1 partition is enough (poison is rare).
- **DLQ headers:** originals kept; added `dlq-original-topic`, `dlq-original-partition`, `dlq-original-offset`, `dlq-consumer-group`, `dlq-reason` (fixed text, ≤ 256 chars, never payload content), `dlq-failed-at` (RFC 3339 UTC), `dlq-replay-count` (0 on first dead-letter, +1 each time a replayed message dead-letters again).
- **Backoff:** per message, starting 1 s, doubling, capped at 30 s, with ±20 % jitter; reset after success. Retries continue until success or shutdown (transient errors are never dead-lettered: a DB outage would otherwise move valid payments to the DLQ). A WARN per retry with `attempt`; an ERROR "consumer stalled" once the same message has failed for ≥ 5 min, repeated every 5 min.
- **Ports.** The consumer takes a `deadLetterWriter` interface (adapter-local); `cmd/api` wires a kafka-go writer (`Murmur2Balancer`, `RequireAll`). With no `KAFKA_BROKERS` there is no consumer, so nothing changes for memory mode.
- **Retry topic** `payments.v1.status-changed.ordering.retry` (owner decision): replayed messages go here, not back to the source topic, so the source topic stays writable by the relay alone and no other consumer sees a replay. The `ordering` group reads both topics (`GroupTopics`); one partition.
- **Replay CLI** `cmd/dlqreplay`, run by an Admin (owner decision; in production with the Admin Kafka principal, the only one with produce rights on the retry topic): flags `-brokers` (default `KAFKA_BROKERS`), `-dry-run` (default true), `-limit` (default 100), `-event-id` (only that event), `-wait` (stop after this long without a message, default 10 s: joining the consumer group takes a few seconds). Reads the DLQ in consumer group `ordering-dlq-replay`; for each message prints `event_id`, original topic/partition/offset, reason. Without dry-run it republishes to the retry topic with byte-identical key and value, the original headers (no `dlq-*`) plus `dlq-replay-count` = DLQ value + 1, and commits the DLQ offset after the broker acks. A run stops at the DLQ end offsets read when it starts, so a message dead-lettered again during the run is left for the next run. With `-event-id` it never commits (committing would also skip the messages before it); replaying the same event twice is harmless. It never edits key or value. Idempotency on `event_id` makes replay safe: poison messages were never recorded in `processed_events`, and a message that already was is deduplicated.

## 4. Contract / schema
- Uses: `consumePaymentStatusChangedForOrders`, `publishPaymentStatusChanged`; tables `ordering.processed_events`, `ordering.orders` (unchanged).
- **Changes (needs approval):**
  - `contracts/asyncapi.yaml`: channels `paymentStatusChangedOrderingDlq` (`payments.v1.status-changed.ordering.dlq`) and `paymentStatusChangedOrderingRetry` (`payments.v1.status-changed.ordering.retry`); message `PaymentStatusChangedDeadLetter` (payload: the original bytes, not validated; headers: the `dlq-*` headers above); operations `deadLetterPaymentStatusChangedForOrders` (send, ordering consumer), `replayDeadLetteredPaymentStatusChanged` (send to the retry channel, `dlqreplay`), `consumeRetriedPaymentStatusChangedForOrders` (receive, group `ordering`). Version stays 0.1.0 (unreleased).
  - `Makefile` `kafka-up`: create the DLQ topic (1 partition, `retention.ms` = 14 days) and the retry topic (1 partition); `make dlq-replay ARGS=…`.
  - New binary `cmd/dlqreplay`. No new env vars for the API; the CLI takes flags (brokers default from `KAFKA_BROKERS`).
  - No DB schema change.

## 5. Acceptance criteria
| ID | Given | When | Then |
|---|---|---|---|
| AC-01 | Consumer running | A message with malformed JSON arrives | It is written to the DLQ with the original key, value, and headers plus `dlq-original-topic/partition/offset`, `dlq-consumer-group=ordering`, `dlq-reason`, `dlq-failed-at`, `dlq-replay-count=0`; then the offset is committed; no order changes |
| AC-02 | Consumer running | A message lacks `event_id`, `amount_minor`, or `currency`, or has a status other than SUCCESS/FAILED | Same as AC-01 with a reason naming the field class, not its value |
| AC-03 | Consumer running | The use case returns `kernel.ErrValidation` | Same as AC-01; `processed_events` has no row for the event |
| AC-04 | Order unknown, amount mismatching, or order already final | The event arrives | Unchanged from order-flow-modules AC-08–AC-10: recorded in `processed_events`, logged, committed, **not** dead-lettered |
| AC-05 | The DLQ write fails (broker down) | A poison message arrives | The offset is not committed; the DLQ write is retried with backoff; after the broker recovers, the message is dead-lettered exactly as AC-01 and then committed |
| AC-06 | The database is down | A valid message arrives | The handler is retried with delays 1, 2, 4, 8, 16, 30, 30 … s (±20 %); the offset is not committed; nothing goes to the DLQ; each retry logs WARN with `attempt` and `event_id` |
| AC-07 | AC-06 continues ≥ 5 min on one message | — | An ERROR "payment event consumer stalled" is logged with `event_id`, partition, offset, and duration, repeated every 5 min |
| AC-08 | AC-06 | The database recovers | The message is applied once, the offset committed, the backoff reset for the next message |
| AC-09 | Shutdown (SIGTERM) during a backoff | — | The consumer stops within 1 s without committing the pending message |
| AC-10 | Any DLQ message | Its `dlq-reason` and logs are inspected | They contain no payload values (no amounts, invoice numbers, provider refs); only ids, topic, partition, offset, and a fixed reason |
| AC-11 | DLQ holds 3 messages | `dlqreplay` runs with default flags | It lists the 3 messages (event_id, original partition/offset, reason) and publishes nothing; DLQ offsets are not committed |
| AC-12 | DLQ holds a message whose cause is fixed | `dlqreplay -dry-run=false` | Each message is published to `payments.v1.status-changed.ordering.retry` with byte-identical key and value, the original headers without `dlq-*`, and `dlq-replay-count` = DLQ value + 1; then the DLQ offset is committed; ordering (reading the retry topic in group `ordering`) applies it once |
| AC-12b | A replayed message is dead-lettered again while `dlqreplay` is still running | — | The run stops at the DLQ end offset it read at start; the new DLQ entry waits for the next run (no replay loop) |
| AC-12a | DLQ holds several messages | `dlqreplay -dry-run=false -event-id <id>` | Only that message is republished; no DLQ offset is committed |
| AC-13 | A replayed message is still poison | It is consumed again from the retry topic | It returns to the DLQ with `dlq-replay-count` = previous + 1 and `dlq-original-topic` = the retry topic |
| AC-14 | A replayed event was already applied | It is consumed again | It is deduplicated on `event_id`; the order does not change |
| AC-15 | `dlqreplay` publishes | — | It writes a JSON audit line per message to stdout: operator (`USER`), `event_id`, source DLQ offset, target topic, time |
| AC-16 | `STORE=memory`, no Kafka | A poison event is delivered in process | It is logged and skipped as today (no DLQ) |
| AC-17 | `make kafka-up` | — | Topics `payments.v1.status-changed.ordering.dlq` (1 partition, `retention.ms=1209600000`) and `payments.v1.status-changed.ordering.retry` (1 partition) exist |

## 6. Non-functional
- Performance: no change on the happy path (one extra branch). Dead-lettering adds one synchronous produce (acks=all) per poison message.
- Security / PII: DLQ reasons and logs never include payload values (AC-10). DLQ data has the same classification as the source topic; same ACLs for reading.
- Audit / observability: WARN per transient retry, ERROR on dead-letter (with `event_id` when decodable, partition, offset, reason) and on stall (AC-07); replay audit lines (AC-15). DLQ size visible in kafka-ui.
- Idempotency / consistency: commit only after the ordering tx or the DLQ write is acknowledged; at-least-once is kept end to end; replay relies on `event_id` dedup.

## 7. Threats & mitigations
Data flow: Kafka topic → consumer (ordering) → PostgreSQL `ordering`; consumer → DLQ topic; operator → `dlqreplay` → original topic. Trust boundaries: broker ↔ API process; operator workstation ↔ broker.

| Threat (STRIDE) | Likelihood | Impact | Mitigation | AC |
|---|---|---|---|---|
| S/T: operator or tool injects an edited event that marks an order PAID | L | H | `dlqreplay` cannot edit key or value (byte-identical republish); it writes only to the retry topic (source topic stays relay-only); only the Admin principal may produce to the retry topic; consumer still checks amount/currency against the order and dedups on `event_id` | AC-12, AC-14 |
| R: nobody can tell who replayed what | M | M | JSON audit line per replayed message with operator, event_id, offsets, time | AC-15 |
| I: payment references leak into logs or DLQ reasons | M | M | Fixed, payload-free reasons; logs carry ids only | AC-10 |
| D: one poison message stalls the partition forever | M | H | Poison goes to the DLQ and is committed | AC-01–AC-03 |
| D: DB outage empties valid payments into the DLQ | M | H | Transient errors are never dead-lettered; bounded backoff until recovery | AC-06, AC-08 |
| D: DLQ broker outage loses poison messages | L | M | No commit until the DLQ write is acked | AC-05 |
| Double-apply after replay (replay / double-spend) | M | H | `event_id` dedup in `processed_events` + final-state rule | AC-14 |
| Reconciliation: dead-lettered payments never confirm their orders | M | H | Stall/dead-letter ERROR logs; replay CLI; open question on alert routing | AC-07, AC-11 |

## 8. Open questions
- [x] DLQ retention: 14 days (owner, 2026-09-30).
- [x] Replay target: separate retry topic read only by ordering (owner, 2026-09-30).
- [x] Who runs `dlqreplay`: an Admin, with the Admin Kafka principal in production (owner, 2026-09-30). Broker ACLs are provisioned outside this repo.
- [x] Backoff 1 s start, 30 s cap, 5 min stall threshold (owner, 2026-09-30).
- [ ] Where do "consumer stalled" and "dead-lettered" ERROR logs page someone (no alerting stack defined yet)?
