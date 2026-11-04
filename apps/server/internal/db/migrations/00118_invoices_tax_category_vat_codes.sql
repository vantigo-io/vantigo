-- +goose Up
-- Each Products tax category maps to one of Invoices' VAT codes (orders design
-- O7, docs/superpowers/specs/2026-11-05-orders-design.md), edited on Invoices'
-- VAT settings page under invoices:settings and read through
-- contracts.SalesTax.ForTaxCategories (O8). tax_category_id is Products' id,
-- with no foreign key across schemas. A category with no row has no VAT code:
-- there is no default.
CREATE TABLE invoices.tax_category_vat_codes (
    tax_category_id integer     PRIMARY KEY,        -- products' tax category, no FK
    vat_code_id     integer     NOT NULL REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE INDEX ix_tax_category_vat_codes_vat_code_id ON invoices.tax_category_vat_codes (vat_code_id);

-- +goose Down
DROP TABLE invoices.tax_category_vat_codes;
