-- 0001_payments: payments collected through a payment provider (spec 2c2p-payment-webhook)

CREATE TABLE payments (
    id            uuid        PRIMARY KEY,
    invoice_no    text        NOT NULL CONSTRAINT uq_payments_invoice_no UNIQUE, -- transaction reference shared with the provider
    amount        bigint      NOT NULL CONSTRAINT ck_payments_amount_non_negative CHECK (amount >= 0), -- minor units
    currency      char(3)     NOT NULL,
    status        text        NOT NULL CONSTRAINT ck_payments_status CHECK (status IN ('PENDING', 'SUCCESS', 'FAILED')),
    provider_ref  text        NOT NULL DEFAULT '',
    provider_code text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
