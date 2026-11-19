-- +goose Up
-- A brand on every invoice (brands design B27, B28, delivery E2).
--
-- brand_id is the brand a document is printed in: set when a draft is made
-- (the customer's default, then its group's, then the workspace default, B28) or
-- when the person switches it for this one document. It has no foreign key: the
-- brand is identity's row, which this module may not reference
-- (internal/db/schema_test.go); it is checked through contracts.Brands.
-- brand_source says where it came from ('chosen', 'customer', 'group',
-- 'default'); brand_skipped is true when a brand higher in the order was
-- archived or unknown when the draft picked its brand, so the editor says so.
-- A document made before this migration has none of them.
--
-- The snapshot is written at issue, beside seller_logo_*: the brand's name and
-- its logo copied into this module's own storage scope, content-addressed as
-- branding/logo-{sha256}.{png|jpg}. The logo columns follow the pattern of
-- ck_invoices_seller_logo. 00034's refuse_issued_document_change compares the
-- whole row, so an issued document's snapshot is immutable with no change to
-- it. A credit note carries its invoice's snapshot.
ALTER TABLE invoices.invoices
    ADD COLUMN brand_id                uuid,
    ADD COLUMN brand_source            varchar(10),
    ADD COLUMN brand_skipped           boolean NOT NULL DEFAULT false,
    ADD COLUMN brand_name              varchar(100),
    ADD COLUMN brand_logo_object_key   varchar(200),
    ADD COLUMN brand_logo_sha256       char(64),
    ADD CONSTRAINT ck_invoices_brand_source CHECK (brand_source IS NULL OR brand_source IN ('chosen', 'customer', 'group', 'default')),
    ADD CONSTRAINT ck_invoices_brand_logo CHECK ((brand_logo_object_key IS NULL) = (brand_logo_sha256 IS NULL));

-- +goose Down
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_brand_logo,
    DROP CONSTRAINT ck_invoices_brand_source,
    DROP COLUMN brand_logo_sha256,
    DROP COLUMN brand_logo_object_key,
    DROP COLUMN brand_name,
    DROP COLUMN brand_skipped,
    DROP COLUMN brand_source,
    DROP COLUMN brand_id;
