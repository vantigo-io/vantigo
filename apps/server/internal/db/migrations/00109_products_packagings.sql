-- +goose Up
-- Packagings (inventory design I24, delivery B): the boxes, cases and pallets
-- a variant is bought, stored and counted in — a box of 15, a Euro pallet of
-- 40 boxes. Pulled forward from Products phase 5 so Inventory can enter and
-- show stock in them; stock itself stays in the variant's own unit.
--
-- A packaging holds either a quantity in the variant's unit, or a count of
-- another packaging of the same variant (contains_packaging_id and
-- contains_count), in which case quantity is derived — 40 × 15 = 600 — and
-- kept by Products in the transaction that changes what it is built from.
-- That the contained packaging belongs to the same variant, that the chain
-- has no cycle, and the 0–10 per variant are Go's rules, as the rest of this
-- schema's are. A barcode is a GTIN unique across every variant and every
-- packaging; the cross-table half of that is checked under a transaction-
-- scoped advisory lock (products:barcodes), the in-table half here.
CREATE TABLE products.variant_packagings (
    id                    integer       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    variant_id            integer       NOT NULL REFERENCES products.product_variants (id) ON DELETE CASCADE,
    name                  varchar(40)   NOT NULL,
    kind                  varchar(16)   NOT NULL,
    contains_packaging_id integer       REFERENCES products.variant_packagings (id),
    contains_count        integer,
    quantity              numeric(14,3) NOT NULL,
    barcode               varchar(14),
    weight_kg             numeric(10,3),
    length_cm             numeric(10,1),
    width_cm              numeric(10,1),
    height_cm             numeric(10,1),
    sort_order            integer       NOT NULL DEFAULT 0,
    created_at            timestamptz   NOT NULL,
    updated_at            timestamptz   NOT NULL
);
CREATE INDEX ix_variant_packagings_variant ON products.variant_packagings (variant_id, sort_order, id);
CREATE INDEX ix_variant_packagings_contains ON products.variant_packagings (contains_packaging_id) WHERE contains_packaging_id IS NOT NULL;
CREATE UNIQUE INDEX ux_variant_packagings_barcode ON products.variant_packagings (barcode) WHERE barcode IS NOT NULL;

-- +goose Down
DROP TABLE products.variant_packagings;
