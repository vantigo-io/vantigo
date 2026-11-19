-- +goose Up
-- An invoice made from an order starts in the order's brand (brands design B28,
-- delivery E3): brand_source gains 'order', beside 'chosen', 'customer', 'group'
-- and 'default' (00214).
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_brand_source,
    ADD CONSTRAINT ck_invoices_brand_source CHECK (brand_source IS NULL OR brand_source IN ('chosen', 'customer', 'group', 'default', 'order'));

-- +goose Down
UPDATE invoices.invoices SET brand_source = 'default' WHERE brand_source = 'order' AND status = 'draft';
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_brand_source,
    ADD CONSTRAINT ck_invoices_brand_source CHECK (brand_source IS NULL OR brand_source IN ('chosen', 'customer', 'group', 'default'));
