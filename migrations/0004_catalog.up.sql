-- 0004_catalog: the catalog module's own schema (ADR-0005, spec order-flow-modules)

CREATE SCHEMA IF NOT EXISTS catalog;

CREATE TABLE catalog.products (
    id         text        PRIMARY KEY,                -- e.g. CERAMIC-MUG
    name       text        NOT NULL,
    price      bigint      NOT NULL CONSTRAINT ck_products_price_positive CHECK (price > 0), -- minor units
    currency   char(3)     NOT NULL,
    active     boolean     NOT NULL DEFAULT true,      -- inactive products are not listed or orderable
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
