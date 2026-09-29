-- 0007_product_uuid_sku: products get a UUID primary key; the old text id becomes
-- the SKU, a merchant code that can change without breaking references.
-- Order lines keep the product's UUID and snapshot its SKU.
--
-- The one-off data step reads catalog.products to convert existing order lines:
-- migrations may span schemas, repositories may not (ADR-0005).

ALTER TABLE catalog.products ADD COLUMN sku text;
UPDATE catalog.products SET sku = id;
ALTER TABLE catalog.products
    ALTER COLUMN sku SET NOT NULL,
    ADD CONSTRAINT uq_products_sku UNIQUE (sku),
    ADD COLUMN uuid_id uuid NOT NULL DEFAULT gen_random_uuid();

ALTER TABLE ordering.order_lines ADD COLUMN sku text;
UPDATE ordering.order_lines l SET sku = l.product_id;
UPDATE ordering.order_lines l SET product_id = p.uuid_id::text
    FROM catalog.products p WHERE p.id = l.product_id;
ALTER TABLE ordering.order_lines
    ALTER COLUMN sku SET NOT NULL,
    ALTER COLUMN product_id TYPE uuid USING product_id::uuid; -- no FK across modules

ALTER TABLE catalog.products DROP CONSTRAINT products_pkey;
ALTER TABLE catalog.products DROP COLUMN id;
ALTER TABLE catalog.products RENAME COLUMN uuid_id TO id;
ALTER TABLE catalog.products ADD CONSTRAINT products_pkey PRIMARY KEY (id);
