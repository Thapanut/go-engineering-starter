-- Reverts 0006. Skips tables or columns that are already gone or already reverted.
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN SELECT * FROM (VALUES
        ('public',   'payments',    'amount_minor',     'amount'),
        ('catalog',  'products',    'price_minor',      'price'),
        ('ordering', 'orders',      'amount_minor',     'amount'),
        ('ordering', 'order_lines', 'unit_price_minor', 'unit_price'),
        ('ordering', 'order_lines', 'line_total_minor', 'line_total')
    ) AS c(sch, tbl, col, old)
    LOOP
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = r.sch AND table_name = r.tbl AND column_name = r.col) THEN
            EXECUTE format('ALTER TABLE %I.%I RENAME COLUMN %I TO %I', r.sch, r.tbl, r.col, r.old);
        END IF;
    END LOOP;
    FOR r IN SELECT * FROM (VALUES
        ('public',   'payments', 'ck_payments_amount_minor_non_negative', 'ck_payments_amount_non_negative'),
        ('catalog',  'products', 'ck_products_price_minor_positive',      'ck_products_price_positive'),
        ('ordering', 'orders',   'ck_orders_amount_minor_positive',       'ck_orders_amount_positive')
    ) AS c(sch, tbl, con, old)
    LOOP
        IF EXISTS (SELECT 1 FROM pg_constraint k JOIN pg_namespace n ON n.oid = k.connamespace
                   WHERE n.nspname = r.sch AND k.conname = r.con) THEN
            EXECUTE format('ALTER TABLE %I.%I RENAME CONSTRAINT %I TO %I', r.sch, r.tbl, r.con, r.old);
        END IF;
    END LOOP;
END $$;
