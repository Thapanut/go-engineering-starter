# ADR-0005: Split catalog, ordering, and payment into modules of one deployable (modular monolith)

- **Status:** Proposed
- **Date:** 2026-09-29
- **Deciders:** Thapanut L. (architect / owner) — option chosen 2026-09-29; this record awaits review

## Context
The checkout demo (spec [payment-checkout](../../02-specs/payment-checkout.md)) lets the payment service price a cart through a `ProductCatalog` port backed by prices hard-coded in Go. That was a shortcut: in a real system the payment service collects an amount it is given; it does not own products, prices, or orders. Putting a `products` table into the payment database would make the shortcut permanent.

The owner wants to see how a request flows across services (catalog → ordering → payment → 2C2P → Kafka → ordering) with real data in PostgreSQL, without running and operating several deployables yet. The payment side already publishes `payment.status-changed` through a transactional outbox (ADR-0004), which is exactly how an order service would learn that an order is paid.

## Options considered
| Criterion | A. Keep the shortcut (payment prices the cart) | B. Modular monolith: catalog, ordering, payment modules in one binary | C. Three microservices (three binaries, three databases) |
|---|---|---|---|
| Complexity | Lowest | Medium: module boundaries, one consumer | High: service discovery, auth between services, three pipelines |
| Cost / effort | None | Moderate, all in this repo | Large; needs deployment tooling not in scope |
| Scalability | Payment scales with catalog reads | One process; modules cannot scale independently | Each service scales on its own |
| Security / compliance | Payment trusts its own catalog | Ownership per module; cross-module calls in process | mTLS / service tokens needed between services |
| Operability | One service | One service, one database (a schema per module) | Three services, three databases, distributed tracing needed |
| Reversibility | Hard: prices and orders grow inside payment | High: each module talks to others only through ports and Kafka, so it can be extracted into a service by swapping adapters | — |
| Shows the real flow | No | Yes: sync calls through ports, async via Kafka | Yes |

## Decision
Split the code into three modules — **catalog**, **ordering**, and **payment** — built and deployed as one binary, each owning its own tables (PostgreSQL schemas `catalog` and `ordering`; payment keeps its existing tables) and talking to the others only through ports and the Kafka event, because this shows the real service boundaries and flow with data in PostgreSQL while keeping one deployable, and each module can later become a service by replacing its adapters.

### Design rules
- **Each module is its own hexagon** (ADR-0002): `internal/catalog/{domain,port,service}`, `internal/ordering/{domain,port,service}`, and payment in `internal/core` (unchanged path) with their adapters.
- **Shared kernel.** `internal/kernel` holds only what every module must agree on: `Money` (int64 minor units, `ParseDecimal`, `Decimal`) and the generic business errors (`ErrValidation`, `ErrNotFound`, `ErrConflict`) that the HTTP adapter maps to contract codes. Standard library only; payment's `domain` keeps aliases so its code reads unchanged.
- **No shared tables, no cross-module SQL.** A module's repositories touch only its own schema. Each module has its own `TxManager`; there is no transaction across modules.
- **Synchronous calls go through an anti-corruption adapter.** Ordering depends on its own outbound ports (`PriceSource`, `PaymentStarter`). Adapters in `internal/ordering/adapter/...` implement them by calling catalog's and payment's inbound ports in process today, an HTTP client tomorrow. depguard forbids ordering's core from importing other modules, and forbids catalog and payment from importing ordering.
- **Payment collects what it is told.** Payment's `CreatePayment` takes `orderId`, `customerId`, and `amount` from ordering (a trusted caller, in process); the browser can no longer create a payment directly. Payment no longer knows about products.
- **Asynchronous result via Kafka.** Ordering consumes `payments.v1.status-changed` (consumer group `ordering`, at-least-once) and marks the order PAID or PAYMENT_FAILED. It deduplicates on `event_id` in the same transaction as the order update, and checks the event amount against the order total. With `STORE=memory` the in-process publisher delivers to the same handler.
- **The event carries `order_id`**, so consumers need no lookup by invoice number (unreleased contract, changed in v1).

## Consequences
- **Positive:** Prices and orders live in PostgreSQL in the module that owns them. The demo shows a synchronous hop (ordering → catalog, ordering → payment) and an asynchronous one (payment → Kafka → ordering), including eventual consistency: payment is SUCCESS a moment before the order is PAID. Extracting a module later changes adapters and wiring, not core code.
- **Negative / accepted risk:** One process and one database still couple availability and scaling. No distributed transaction: if starting the payment fails after the order is stored, the order stays AWAITING_PAYMENT without a payment (visible, retriable). Without Kafka and with `STORE=postgres` the relay is disabled, so orders never become PAID (by design; the demo says so). More code and migrations than the shortcut. Payment tables stay in the `public` schema until a rename is worth the churn.
- **Revisit when:** a module needs its own release cadence or scaling (e.g. catalog read load ≫ payments), a separate team owns a module, or compliance requires isolating payment data (e.g. PCI scope) — then extract that module into its own service (option C) behind the same ports.
