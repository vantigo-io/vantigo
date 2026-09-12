-- +goose Up
-- Products' schema, single-tenant (every tenant_id from the .NET EF
-- configuration is dropped, along with the composite keys/indexes and the
-- Row-Level Security it was part of; what survives is the rest of each key):
-- product categories (an adjacency-list hierarchy), tax categories, products,
-- their variants, and each variant's prices. See
-- docs/superpowers/specs/2026-09-12-products-inventory.md §3.
CREATE SCHEMA products;

-- Sibling names must be unique including among roots (parent_id IS NULL).
-- NULLS NOT DISTINCT is what makes that hold: plain UNIQUE treats every NULL
-- parent_id as distinct from every other, so two root categories named
-- "Furniture" would otherwise pass silently. Name equality is exact/ordinal
-- (no normalization), so "Furniture" and "furniture" can still coexist.
CREATE TABLE products.product_categories (
    id        integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name      varchar(200) NOT NULL,
    parent_id integer REFERENCES products.product_categories (id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX ux_product_categories_parent_id_name
    ON products.product_categories (parent_id, name) NULLS NOT DISTINCT;
CREATE INDEX ix_product_categories_parent_id ON products.product_categories (parent_id);

CREATE TABLE products.tax_categories (
    id         integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name       varchar(100)  NOT NULL,
    kind       varchar(20)   NOT NULL,
    rate       numeric(5,4)  NOT NULL,
    created_at timestamptz   NOT NULL,
    updated_at timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_tax_categories_name ON products.tax_categories (name);

-- category_id and tax_category_id are both Restrict FKs (products inventory
-- §3/§4): a category or tax category still referenced by a product cannot be
-- deleted. The Go port maps that race to 409 at the API layer (a later
-- task). Unlike a unique-violation-backed conflict, and unlike the default
-- NO ACTION FK (which raises foreign_key_violation, 23503), Postgres's
-- literal RESTRICT keyword raises restrict_violation (23001) instead —
-- confirmed empirically in internal/db/schema_test.go. httpx.WriteError maps
-- neither code to 409 today, the way it maps 23505/23P01 — a later task's
-- decision, not this one's, but it needs the *right* code (23001, not the
-- 23503 the products inventory's own §4 discussion names).
CREATE TABLE products.products (
    id              integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name            varchar(200)  NOT NULL,
    description     varchar(4000),
    category_id     integer REFERENCES products.product_categories (id) ON DELETE RESTRICT,
    type            varchar(20)   NOT NULL,
    status          varchar(20)   NOT NULL,
    tax_category_id integer       NOT NULL REFERENCES products.tax_categories (id) ON DELETE RESTRICT,
    created_at      timestamptz   NOT NULL,
    updated_at      timestamptz   NOT NULL
);
CREATE INDEX ix_products_category_id ON products.products (category_id);
CREATE INDEX ix_products_tax_category_id ON products.products (tax_category_id);

-- sku is unique catalog-wide, not scoped to the product (inventory §2, oddity
-- 10). barcode's uniqueness is a *partial* index — WHERE barcode IS NOT
-- NULL — so any number of variants may leave it unset; only two variants
-- that share the same non-null barcode collide.
CREATE TABLE products.product_variants (
    id             integer       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    product_id     integer       NOT NULL REFERENCES products.products (id) ON DELETE CASCADE,
    sku            varchar(64)   NOT NULL,
    barcode        varchar(14),
    unit           varchar(20)   NOT NULL,
    standard_cost  numeric(12,2),
    weight_kg      numeric(10,3),
    length_cm      numeric(10,1),
    width_cm       numeric(10,1),
    height_cm      numeric(10,1),
    option_values  jsonb,
    created_at     timestamptz   NOT NULL,
    updated_at     timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_product_variants_sku ON products.product_variants (sku);
CREATE UNIQUE INDEX ux_product_variants_barcode ON products.product_variants (barcode) WHERE barcode IS NOT NULL;
CREATE INDEX ix_product_variants_product_id ON products.product_variants (product_id);

-- Deliberately no exclusion (or any other overlap-guarding) constraint here.
-- The .NET module never had one either (products inventory §3/§4/§7 oddity
-- 6): the "campaign price beats the open-ended base price of the same
-- currency, but two prices of the same boundedness may not overlap" rule
-- (ProductPricing.Conflicts) is pure application code with no database-level
-- backstop, unlike Energy's supply_periods GiST exclusion constraint. Do not
-- read this table's lack of one as a missed btree_gist opportunity — it is
-- the .NET behavior being reproduced, not an oversight to fix here.
CREATE TABLE products.product_prices (
    id         integer       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    variant_id integer       NOT NULL REFERENCES products.product_variants (id) ON DELETE CASCADE,
    currency   char(3)       NOT NULL,
    amount     numeric(12,2) NOT NULL,
    valid_from timestamptz,
    valid_to   timestamptz
);
CREATE INDEX ix_product_prices_variant_id_currency ON products.product_prices (variant_id, currency);
CREATE INDEX ix_product_prices_variant_id ON products.product_prices (variant_id);

-- +goose Down
DROP SCHEMA products CASCADE;
