# Frontend integration flow: order → catalog → payment → 2C2P → webhook → Kafka → order

- **Related:** specs [order-flow-modules](order-flow-modules.md), [payment-checkout](payment-checkout.md), [2c2p-payment-webhook](2c2p-payment-webhook.md), [payment-events-outbox](payment-events-outbox.md); ADR-0004, ADR-0005; contracts `listProducts`, `placeOrder`, `getOrder`, `getPaymentStatus`, `getDemoPaymentEvents`, `receive2c2pPaymentNotification`, `publishPaymentStatusChanged`, `consumePaymentStatusChangedForOrders`
- **Run it:** `make demo` (add `STORE=memory` to skip Docker), then open the printed URL. For Kafka: `make kafka-up`, then `make demo KAFKA_BROKERS=localhost:9092` and `make kafka-consume` in another terminal.

## Sequence

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser (shop + return page)
  box one deployable, three modules (ADR-0005)
    participant O as Ordering
    participant C as Catalog
    participant PM as Payment
  end
  participant DB as PostgreSQL (schemas catalog / ordering / public)
  participant P as 2C2P hosted page (mock modal)
  participant K as Kafka

  B->>C: GET /v1/products
  C->>DB: SELECT catalog.products WHERE active
  C-->>B: products with price / priceMinor
  B->>O: POST /v1/orders {items[productId, quantity]} (no prices)
  O->>C: FindProducts(ids) [sync, via PriceSource → catalogclient]
  O->>O: price lines, total (int64 satang)
  O->>DB: INSERT ordering.orders AWAITING_PAYMENT + order_lines (ordering tx)
  O->>PM: CreatePayment(orderId, customer, total) [sync, via PaymentStarter → paymentclient]
  PM->>DB: INSERT payments PENDING (payment tx), open 2C2P session (stub)
  O->>DB: UPDATE ordering.orders SET invoice_no
  O-->>B: 201 {orderId, lines, amount, payment: {invoiceNo, checkoutUrl}}
  B->>P: open checkoutUrl, pay (card + 3-D Secure OTP)

  par Callback 1: frontend return (browser redirect, not trusted)
    P-->>B: redirect to /demo/return?orderId=…&invoiceNo=…
    loop every 2 s until the order is final
      B->>O: GET /v1/orders/{orderId} → AWAITING_PAYMENT
      B->>PM: GET /v1/payments/{invoiceNo}/status → PENDING, later SUCCESS
    end
  and Callback 2: server-to-server webhook (source of truth)
    P->>PM: POST /webhooks/2c2p {"payload": JWT HS256}
    PM->>DB: BEGIN; lock payment; UPDATE → SUCCESS; INSERT outbox; COMMIT
    PM-->>P: 200 {outcome: PROCESSED | DUPLICATE | CONFLICT_IGNORED}
  end

  PM->>K: relay publishes payment.status-changed {order_id, status, amount}
  K->>O: consumer group "ordering"
  O->>DB: BEGIN; INSERT processed_events(event_id); lock order; check amount; UPDATE → PAID; COMMIT
  O-->>K: commit offset
  B->>O: next poll → PAID → "Payment Successful! Order Confirmed", stop polling
```

## Notes
- **The browser never prices the order.** It sends product ids and quantities; ordering prices them with the catalog, and payment takes the amount only from ordering (there is no public payment-create route), so a tampered request cannot lower the amount.
- **Two kinds of hops.** Ordering calls catalog and payment synchronously through its own ports (in process today, HTTP tomorrow). Payment tells ordering the result asynchronously through Kafka, so payment never depends on ordering.
- **Eventual consistency is visible.** The page shows *Payment Received — confirming your order…* while the payment is SUCCESS and the event is on its way; the order becomes PAID when ordering consumes it (≈ the relay poll interval).
- **Source of truth for the browser is the status endpoint**, not the 2C2P redirect back to the shop. The redirect can be lost (closed tab, network), the webhook is retried by 2C2P, and the status endpoint reads what the webhook committed.
- **Kafka is on the order's path, not the payment's.** The payment row and its outbox event commit in one transaction, so the payment is SUCCESS as soon as the webhook commits even if Kafka is down; the order waits for the event. With `STORE=postgres` and no `KAFKA_BROKERS` the relay is disabled and orders stay AWAITING_PAYMENT (the page says so). With `STORE=memory` the event is delivered in process.
- **Polling stops when the order is final** (PAID / PAYMENT_FAILED never change) and its payment event is published.
- **Idempotency is visible in the demo.** The debug box's *Replay last webhook* resends the identical signed body: the backend answers `DUPLICATE`, writes nothing, and emits no second event.
- **The debug box reads the backend, not the browser's guesses.** `[Order Service]` from `GET /v1/orders/{orderId}`, `[DB Status]` from the payment status endpoint, `[Last Webhook Received]` from the webhook response, and `[Kafka Outbox Event]` from the demo events endpoint (outbox row, `publishedAt`, and whether the relay targets Kafka).
- **In the demo the browser also plays 2C2P.** The modal is the hosted payment page; the webhook is sent 3 s after the redirect to show that the two callbacks are independent. It signs the webhook with the sandbox secret it received in the URL fragment from `make demo`. The backend never sends the secret to a client, and `/demo` is served only with `DEMO_UI_ENABLED=true`.
- **Future option:** replace polling with server-sent events fed by a Kafka consumer. That needs its own spec (fan-out across API instances, reconnects); polling every 2 s on one indexed read is enough at the current scale.
