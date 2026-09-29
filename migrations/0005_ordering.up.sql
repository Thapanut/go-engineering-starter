-- 0005_ordering: the ordering module's own schema (ADR-0005, spec order-flow-modules)

CREATE SCHEMA IF NOT EXISTS ordering;

CREATE TABLE ordering.orders (
    id          uuid        PRIMARY KEY,
    customer_id text        NOT NULL,                       -- owner (JWT subject)
    status      text        NOT NULL CONSTRAINT ck_orders_status CHECK (status IN ('AWAITING_PAYMENT', 'PAID', 'PAYMENT_FAILED')),
    amount      bigint      NOT NULL CONSTRAINT ck_orders_amount_positive CHECK (amount > 0), -- minor units, sum of the lines
    currency    char(3)     NOT NULL,
    invoice_no  text        CONSTRAINT uq_orders_invoice_no UNIQUE, -- payment's reference; NULL until payment has started
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Snapshot of what was ordered and at which price.
CREATE TABLE ordering.order_lines (
    order_id   uuid    NOT NULL REFERENCES ordering.orders (id),
    line_no    int     NOT NULL,
    product_id text    NOT NULL, -- catalog product id; no FK across modules
    name       text    NOT NULL,
    unit_price bigint  NOT NULL,
    quantity   int     NOT NULL CONSTRAINT ck_order_lines_quantity CHECK (quantity > 0),
    line_total bigint  NOT NULL,
    currency   char(3) NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

-- Payment events already applied, so a redelivered event changes nothing.
CREATE TABLE ordering.processed_events (
    event_id     uuid        PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);
