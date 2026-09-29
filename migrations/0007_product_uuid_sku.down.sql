-- Reverts 0007: the SKU becomes the text id again. Skips a database without 0007.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'catalog' AND table_name = 'products' AND column_name = 'sku') THEN
        RETURN;
    END IF;

    ALTER TABLE ordering.order_lines ALTER COLUMN product_id TYPE text USING sku;
    ALTER TABLE ordering.order_lines DROP COLUMN sku;

    ALTER TABLE catalog.products DROP CONSTRAINT products_pkey;
    ALTER TABLE catalog.products DROP COLUMN id;
    ALTER TABLE catalog.products DROP CONSTRAINT uq_products_sku;
    ALTER TABLE catalog.products RENAME COLUMN sku TO id;
    ALTER TABLE catalog.products ADD CONSTRAINT products_pkey PRIMARY KEY (id);
END $$;
