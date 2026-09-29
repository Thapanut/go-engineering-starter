# Changelog

## [Unreleased]

### Added — Ordering consumes payment events (ADR-0005, `docs/02-specs/order-flow-modules.md` step 4)
- `PaymentEventHandler` (`service.PaymentEventService`): one ordering transaction records `event_id` in `ordering.processed_events`, locks the order, checks the amount against the total, and moves it to `PAID` or `PAYMENT_FAILED`; unknown orders and mismatches are recorded and logged, not retried forever.
- Driving adapter `inbound/paymentevents`: decodes `payment.status-changed`; Kafka consumer group `ordering` (commit after success, retry with backoff) when `KAFKA_BROKERS` is set; with `STORE=memory` the in-process `memory.Publisher.Deliver` calls it instead.
- AsyncAPI documents the `ordering` consumer.

### Added — Ordering module (ADR-0005, `docs/02-specs/order-flow-modules.md` step 3)
- `internal/core/ordering`: `Order` (`AWAITING_PAYMENT → PAID | PAYMENT_FAILED`) with lines that snapshot name and unit price, `OrderUseCase`, outbound ports `PriceSource` and `PaymentStarter`, own `TxManager`.
- `POST /v1/orders` (`placeOrder`: items only; priced with the catalog; payment started for the total) and `GET /v1/orders/{orderId}` (`getOrder`, owner only).
- Cross-module adapters `outbound/ordering/catalogclient` and `outbound/ordering/paymentclient` (in-process today); Postgres (`ordering` schema) and memory order stores.
- Migration `0005_ordering` (`orders`, `order_lines`, `processed_events`); depguard: ordering's core may not import catalog or payment.

### Added — Catalog module (ADR-0005, `docs/02-specs/order-flow-modules.md` step 2)
- `internal/core/catalog` (domain, `CatalogUseCase`, `ProductRepository`, service); `GET /v1/products` now served by the catalog from PostgreSQL (`catalog.products`), memory adapter with the same sample rows for `STORE=memory`.
- Migration `0004_catalog` (schema `catalog`); dev seed `migrations/dev/20_seed_catalog.sql` (3 active products, 1 inactive).
- depguard: payment and catalog cores may not import another module.

### Changed — Payment collects what ordering asks for (ADR-0005, `docs/02-specs/order-flow-modules.md`)
- Shared kernel `internal/kernel` (`Money`, `ParseDecimal`, generic errors); payment's `domain` keeps aliases. depguard keeps the kernel standard-library only.
- `CheckoutUseCase.CreatePayment` takes `orderId`, `customerId`, and `amount` from the ordering module; payment no longer prices carts. **Removed (unreleased):** `POST /v1/payments`, `GET /v1/products` from payment, `ProductCatalog`, `domain.PriceOrder`.
- `payment.status-changed` carries `order_id` (unreleased contract, changed in v1).

### Added — Cart checkout and status polling (`docs/02-specs/payment-checkout.md`)
- `GET /v1/products` (`listProducts`): sample catalog priced by the backend (`catalog.Sample`, port `ProductCatalog`).
- `POST /v1/payments` (`createPayment`): takes `orderId` and `items` (no amount); `domain.PriceOrder` prices them; creates a PENDING payment owned by the JWT subject with a server-generated `invoiceNo`, then opens a payment session; returns the priced `lines`, `paymentToken`, and `checkoutUrl`.
- `GET /v1/payments/{invoiceNo}/status` (`getPaymentStatus`): owner-only; another customer's payment is `NOT_FOUND`.
- Ports `CheckoutUseCase`, `PaymentEventsUseCase`, `PaymentGateway`, `ProductCatalog`; `PaymentRepository.GetByInvoiceNo`; `OutboxRepository.ListByKey`; `twoc2p.StubGateway` (stands in for the 2C2P Payment Token API, `.invalid` checkout URLs).
- Migration `0003_payment_checkout` (`payments.order_id`, `payments.customer_id`, backward compatible).
- End-to-end demo at `GET /demo` and `/demo/return` (embedded static HTML, CSP, no secrets) behind `DEMO_UI_ENABLED` (default `false`): catalog and cart, mock 2C2P hosted page, frontend return page polling every 2 s, server-to-server webhook signed in the browser (WebCrypto HS256), webhook replay, and a live debug box (DB status, last webhook, outbox/Kafka event via `GET /v1/demo/payments/{invoiceNo}/events`). `make demo`; `devtoken -demo-url`.
- Flow doc `docs/02-specs/frontend-integration-flow.md` (Mermaid sequence).
- Money naming: `amount` is always the decimal string people read (`"1000.00"`); minor units are `amountMinor` (REST) / `amount_minor` (events). **Changed (unreleased):** `payment.status-changed.amount` is now the decimal string and `amount_minor` holds the integer. `domain.ParseDecimal` / `Money.Decimal` shared by REST, events, and the 2C2P verifier.

### Added — kafka-ui for local Kafka
- `kafka-ui` (provectuslabs/kafka-ui) in docker-compose on http://localhost:8081 (`KAFKA_UI_PORT`); `make kafka-ui` starts Kafka and the UI and opens it; `make kafka-down` stops both.
- Local Kafka gets a second listener, `kafka:29092` for containers, next to `localhost:9092` for the host.

### Added — Payment events via transactional outbox (`docs/02-specs/payment-events-outbox.md`, ADR-0004)
- `payment.status-changed` on Kafka topic `payments.v1.status-changed` (contract `contracts/asyncapi.yaml`), keyed by payment id, written to the `outbox` table in the same transaction as the payment transition.
- `service.OutboxRelay`: claims unpublished rows with `FOR UPDATE SKIP LOCKED`, publishes (acks=all), marks them published; at-least-once, consumers deduplicate on `event_id`. Runs as a goroutine in `cmd/api`.
- Ports `OutboxRepository` (in `port.Repositories`) and `MessagePublisher`; adapters: Postgres (GORM), memory (plus in-process `memory.Publisher`), and `kafka` (segmentio/kafka-go, MIT).
- Migration `0002_outbox`; env `KAFKA_BROKERS`, `OUTBOX_POLL_INTERVAL`; depguard forbids `kafka-go` in the core.
- `service.NewWebhookService` now takes a `port.IDGenerator` (event ids).

### Added — 2C2P payment webhook (`docs/02-specs/2c2p-payment-webhook.md`)
- `POST /webhooks/2c2p`: verifies the 2C2P PGW v4 notification (JWT HS256 = HMAC-SHA256, alg pinned, constant-time compare, merchant check) and transitions a payment `PENDING → SUCCESS | FAILED` atomically.
- Idempotent redelivery: row lock + terminal-state rule + conditional update; duplicates return `DUPLICATE` without writing, and conflicting outcomes are ignored and logged for reconciliation.
- Ports `WebhookUseCase`, `PaymentRepository`, `WebhookVerifier`; adapters: GORM (`payments` table, model kept in the adapter), in-memory, and `twoc2p`.
- Migration `0001_payments`; env `TWOC2P_MERCHANT_ID`, `TWOC2P_SECRET_KEY`; `httpapi.PublicModule` for signature-authenticated routes.

### Added
- Hexagonal architecture baseline (ADR-0002): `internal/core` (domain, port, service), HTTP inbound adapter (Fiber v2) with a `Module` extension point, PostgreSQL (GORM) and in-memory outbound adapters, composition root in `cmd/api`.
- Platform: env config, structured JSON logging, JWT bearer auth; middleware for trace id, access log, panic recovery, request timeout, body limit; strict JSON decoding; domain error → contract error mapping.
- `/healthz` and `/readyz` probes, `domain.Money` (int64 minor units), Docker Compose dev database, Dockerfile (distroless, non-root), `cmd/devtoken`.
- ADR-0003: Fiber v2 + GORM, with guardrails.
- Integration test gate in `make verify`; `depguard` rules that enforce hexagonal boundaries; GitHub Actions CI.
