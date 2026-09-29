-- 0006_money_minor_columns: money columns name their unit (minor units, e.g. satang),
-- matching priceMinor / amountMinor in the API and amount_minor in events.

ALTER TABLE payments RENAME COLUMN amount TO amount_minor;
ALTER TABLE payments RENAME CONSTRAINT ck_payments_amount_non_negative TO ck_payments_amount_minor_non_negative;

ALTER TABLE catalog.products RENAME COLUMN price TO price_minor;
ALTER TABLE catalog.products RENAME CONSTRAINT ck_products_price_positive TO ck_products_price_minor_positive;

ALTER TABLE ordering.orders RENAME COLUMN amount TO amount_minor;
ALTER TABLE ordering.orders RENAME CONSTRAINT ck_orders_amount_positive TO ck_orders_amount_minor_positive;

ALTER TABLE ordering.order_lines RENAME COLUMN unit_price TO unit_price_minor;
ALTER TABLE ordering.order_lines RENAME COLUMN line_total TO line_total_minor;
