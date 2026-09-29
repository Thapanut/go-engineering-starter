# Frontend integration flow: checkout → 2C2P → webhook → outbox/Kafka → polling

- **Related:** specs [payment-checkout](payment-checkout.md), [2c2p-payment-webhook](2c2p-payment-webhook.md), [payment-events-outbox](payment-events-outbox.md); ADR-0004; contracts `listProducts`, `createPayment`, `getPaymentStatus`, `getDemoPaymentEvents`, `receive2c2pPaymentNotification`, `publishPaymentStatusChanged`
- **Run it:** `make demo` (add `STORE=memory` to skip Docker), then open the printed URL. For Kafka: `make kafka-up`, then `make demo KAFKA_BROKERS=localhost:9092` and `make kafka-consume` in another terminal.

## Sequence

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser (shop + return page)
  participant API as Backend (Fiber, /v1 JWT)
  participant CS as CheckoutService
  participant CAT as ProductCatalog
  participant DB as PostgreSQL
  participant GW as PaymentGateway (2C2P, stub today)
  participant P as 2C2P hosted page (mock modal)
  participant WS as WebhookService
  participant R as OutboxRelay
  participant K as Kafka

  B->>API: GET /v1/products
  API-->>B: products with price / priceMinor
  B->>API: POST /v1/payments {orderId, items[productId, quantity]} (no amount)
  API->>CS: CreatePayment(customer = JWT sub)
  CS->>CAT: FindProducts(ids)
  CS->>CS: PriceOrder: price × quantity, total (int64 satang)
  CS->>DB: INSERT payments (PENDING, invoiceNo, amount, order_id, customer_id)
  CS->>GW: CreateSession(payment) (outside the DB tx)
  GW-->>CS: paymentToken, checkoutUrl
  API-->>B: 201 {invoiceNo, lines, amount, amountMinor, paymentToken, checkoutUrl}
  B->>P: open checkoutUrl, pay (card + 3-D Secure OTP)

  par Callback 1: frontend return (browser redirect, not trusted)
    P-->>B: redirect to /demo/return?invoiceNo=…
    loop every 2 s until SUCCESS or FAILED
      B->>API: GET /v1/payments/{invoiceNo}/status
      API->>DB: SELECT by invoice_no (owner must match, else 404)
      API-->>B: 200 {status: PENDING} → "Payment Pending / Verifying…"
    end
  and Callback 2: server-to-server webhook (source of truth)
    P->>API: POST /webhooks/2c2p {"payload": JWT HS256}
    API->>WS: HandlePaymentNotification(raw body)
    WS->>WS: verify HMAC (alg pinned, constant time), merchantID
    WS->>DB: BEGIN; SELECT … FOR UPDATE; UPDATE payment WHERE status = 'PENDING'; INSERT outbox; COMMIT
    API-->>P: 200 {outcome: PROCESSED | DUPLICATE | CONFLICT_IGNORED}
  end

  R->>DB: claim unpublished outbox rows (FOR UPDATE SKIP LOCKED)
  R->>K: payments.v1.status-changed (key = payment id, acks = all)
  R->>DB: mark published

  B->>API: GET /v1/payments/{invoiceNo}/status (next poll)
  API-->>B: 200 {status: SUCCESS} → "Payment Successful! Order Confirmed", stop polling
  opt demo only (DEMO_UI_ENABLED=true)
    B->>API: GET /v1/demo/payments/{invoiceNo}/events
    API-->>B: outbox rows with publishedAt, publisher = kafka | in-process | disabled
  end
```

## Notes
- **The browser never prices the order.** It sends product ids and quantities; the backend prices them from the catalog, so a tampered request cannot lower the amount.
- **Source of truth for the browser is the status endpoint**, not the 2C2P redirect back to the shop. The redirect can be lost (closed tab, network), the webhook is retried by 2C2P, and the status endpoint reads what the webhook committed.
- **Kafka is off the browser's path.** The payment row and its outbox event commit in one transaction, so polling sees SUCCESS as soon as the webhook commits, whether or not Kafka is up. Other services (fulfilment, notifications) consume the event; the browser does not.
- **Polling stops on a final status.** SUCCESS and FAILED never change again (webhook spec AC-03, AC-04). The demo page keeps polling the debug endpoint until the event is also published (or the relay is disabled), then stops.
- **Idempotency is visible in the demo.** The debug box's *Replay last webhook* resends the identical signed body: the backend answers `DUPLICATE`, writes nothing, and emits no second event.
- **The debug box reads the backend, not the browser's guesses.** `[DB Status]` comes from polling, `[Last Webhook Received]` from the webhook response, and `[Kafka Outbox Event]` from the demo events endpoint (outbox row, `publishedAt`, and whether the relay targets Kafka).
- **In the demo the browser also plays 2C2P.** The modal is the hosted payment page; the webhook is sent 3 s after the redirect to show that the two callbacks are independent. It signs the webhook with the sandbox secret it received in the URL fragment from `make demo`. The backend never sends the secret to a client, and `/demo` is served only with `DEMO_UI_ENABLED=true`.
- **Future option:** replace polling with server-sent events fed by a Kafka consumer. That needs its own spec (fan-out across API instances, reconnects); polling every 2 s on one indexed read is enough at the current scale.
