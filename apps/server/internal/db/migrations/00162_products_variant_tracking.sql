-- +goose Up
-- The tracking mode (manufacturing phase 2 M49): whether a variant's stock is
-- kept in lots, in serials (a lot of at most one), or not at all, and the
-- days a new lot's expiry and best-before date lie after its production or
-- receipt date. Only a goods product's variant may be tracked; a change of the
-- mode is held by stock, reservations and stock in transit (M51). Until layer
-- M the variant write refuses any mode but none (tracking_unavailable).
ALTER TABLE products.product_variants
    ADD COLUMN tracking         text    NOT NULL DEFAULT 'none',
    ADD COLUMN expiry_days      integer,
    ADD COLUMN best_before_days integer,
    ADD CONSTRAINT ck_product_variants_tracking CHECK (tracking IN ('none', 'lot', 'serial')),
    ADD CONSTRAINT ck_product_variants_expiry_days CHECK (expiry_days BETWEEN 1 AND 3650),
    ADD CONSTRAINT ck_product_variants_best_before_days CHECK (best_before_days BETWEEN 1 AND 3650);

-- +goose Down
ALTER TABLE products.product_variants
    DROP COLUMN best_before_days,
    DROP COLUMN expiry_days,
    DROP COLUMN tracking;
