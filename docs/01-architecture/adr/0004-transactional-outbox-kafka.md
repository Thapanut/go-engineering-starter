# ADR-0004: Publish domain events to Kafka through a transactional outbox, using segmentio/kafka-go

- **Status:** Proposed
- **Date:** 2026-09-27
- **Deciders:** Thapanut L. (architect / owner) — options chosen 2026-09-27; this record awaits review

## Context
Other services (orders, notifications, ledger) need to know when a payment reaches SUCCESS or FAILED. The 2C2P webhook spec deferred this ("downstream notifications/events — future: transactional outbox"). The payment state lives in PostgreSQL; the events go to Kafka (already in the architecture context diagram).

Writing to PostgreSQL and Kafka in one request is a dual write: if the DB commits and the Kafka write fails (or the process dies in between), downstream never learns about the payment; if Kafka is written first and the DB rolls back, downstream acts on a payment that did not happen. For money movement, neither is acceptable. The webhook must also stay fast (p95 < 300 ms) and must not fail because Kafka is slow or down.

## Options considered
| Criterion | A. Transactional outbox + polling relay | B. Publish to Kafka directly in the webhook | C. CDC (Debezium) on the outbox table |
|---|---|---|---|
| Complexity | Medium: one table, one relay loop | Low | High: Kafka Connect, Debezium, connector ops |
| Cost / effort | Small, all in this repo | Smallest | New infrastructure to run and secure |
| Scalability | Polling adds light DB load; `SKIP LOCKED` lets every API instance relay | Best latency | Best; reads the WAL, no polling |
| Consistency | Event committed atomically with the state change; at-least-once delivery | Dual write: events can be lost or phantom | Same as A |
| Security / compliance | Events stay in our DB until acked; no new components | Same | New component with DB replication rights |
| Operability | Backlog visible with one SQL query; Kafka outage does not affect webhooks | Kafka outage fails or loses webhooks | Connector lag and failures to monitor |
| Reversibility | High: the relay can later be replaced by CDC reading the same table | — | — |

Kafka client library, for the relay:

| Criterion | segmentio/kafka-go | confluent-kafka-go | IBM/sarama |
|---|---|---|---|
| Build | Pure Go, no cgo | cgo + librdkafka | Pure Go |
| API | Small (`Writer.WriteMessages`) | Rich, librdkafka config | Large, lower level |
| Idempotent / transactional producer | No | Yes | Yes |
| License / maintenance | MIT; v0.4.51 released 2026-04-23 | Apache-2.0 | MIT |

## Decision
Every state change that other services care about writes its event to an `outbox` table **in the same database transaction**. A relay in each API process claims unpublished rows with `SELECT … FOR UPDATE SKIP LOCKED`, publishes them to Kafka with `segmentio/kafka-go` (acks = all), and marks them published in that same transaction. Because this removes the dual write without adding infrastructure, a Kafka outage cannot fail or lose a webhook, and a pure-Go client keeps builds and the distroless image simple.

### Design rules
- **Core stays pure.** The domain raises `domain.PaymentStatusChanged`. The core sees only ports: `OutboxRepository` (in `port.Repositories`, bound to the transaction) and `MessagePublisher`. The relay is a service (`service.OutboxRelay`) that depends on those ports only. depguard forbids `kafka-go` in `internal/core`.
- **The contract lives in an adapter.** `internal/adapter/outbound/events` encodes events into the payload defined in `contracts/asyncapi.yaml`; the Postgres and memory outboxes both use it, so the wire format does not depend on the store.
- **At-least-once.** kafka-go has no idempotent producer, and a crash after the broker ack but before the commit republishes a batch. Consumers deduplicate on `event_id` (also sent as a header). This is stated in the contract.
- **Ordering per key.** The message key is the aggregate id (payment id); `Murmur2Balancer` maps keys to partitions the same way Java clients do.
- **No auto topic creation.** Topics are provisioned with their retention and ACLs.

## Consequences
- **Positive:** No lost or phantom events. The webhook latency and availability do not depend on Kafka. Backlog is observable (`SELECT count(*) FROM outbox WHERE published_at IS NULL`). Unit tests run with the memory outbox and a fake publisher.
- **Negative / accepted risk:** Duplicates are possible and consumers must be idempotent. Publishing happens while the relay holds row locks on the claimed batch (bounded by batch size 100 and a 10 s write timeout). A message Kafka always rejects (e.g. too large) blocks its batch until fixed. Published rows accumulate until a retention job exists. Polling adds up to one poll interval (default 1 s) of latency.
- **Revisit when:** sustained event rate makes polling a measurable DB load (e.g. > 500 events/s), end-to-end event latency must be under the poll interval, or exactly-once is required. Then move to CDC on the same table (option C) or a transactional producer.
