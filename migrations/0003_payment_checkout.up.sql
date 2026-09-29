-- 0003_payment_checkout: who started a payment and for which order (spec payment-checkout)

ALTER TABLE payments
    ADD COLUMN order_id    text NOT NULL DEFAULT '', -- caller's order reference
    ADD COLUMN customer_id text NOT NULL DEFAULT ''; -- owner (JWT subject); '' for rows created before 0003
