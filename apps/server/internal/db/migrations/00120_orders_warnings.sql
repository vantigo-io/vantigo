-- +goose Up
-- Orders' sales warnings (orders design O33, delivery A2): one warning per
-- customer and one per product, kept by Orders itself — a warning is a selling
-- rule, and neither Customers nor Products needs it. A warning is info (shown
-- when the customer or product is put on a quote or an order) or block (also
-- refusing send and confirm, unless an orders:settings holder overrides it
-- with a reason). The subject's name is snapshotted when the warning is
-- written, so the settings can list the warnings without reading Customers or
-- Products; the customer erase deletes a customer's warning, name and all.
--
-- The lines gain the product a catalog line's variant belongs to, so the
-- warnings a document carries and the analysis by product are read from
-- Orders' own tables (O35), and the name of the packaging a line was entered
-- in, so the PDF prints "3 × Eske à 15 (45 stk)" after the packaging is
-- renamed or gone (O15). Both are snapshots, as the SKU and the name are.
CREATE TABLE orders.customer_warnings (
    customer_id        integer      PRIMARY KEY,
    level              varchar(10)  NOT NULL,
    text               varchar(500) NOT NULL,
    subject_name       varchar(255) NOT NULL DEFAULT '',
    updated_by_user_id uuid,
    updated_by_display varchar(200),
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL,
    CONSTRAINT ck_customer_warnings_level CHECK (level IN ('info', 'block')),
    CONSTRAINT ck_customer_warnings_text CHECK (length(btrim(text)) > 0)
);

CREATE TABLE orders.product_warnings (
    product_id         integer      PRIMARY KEY,
    level              varchar(10)  NOT NULL,
    text               varchar(500) NOT NULL,
    subject_name       varchar(255) NOT NULL DEFAULT '',
    updated_by_user_id uuid,
    updated_by_display varchar(200),
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL,
    CONSTRAINT ck_product_warnings_level CHECK (level IN ('info', 'block')),
    CONSTRAINT ck_product_warnings_text CHECK (length(btrim(text)) > 0)
);

ALTER TABLE orders.lines
    ADD COLUMN product_id     integer,
    ADD COLUMN packaging_name varchar(40);
CREATE INDEX ix_lines_product ON orders.lines (product_id) WHERE product_id IS NOT NULL;

-- +goose Down
DROP INDEX orders.ix_lines_product;
ALTER TABLE orders.lines DROP COLUMN packaging_name, DROP COLUMN product_id;
DROP TABLE orders.product_warnings;
DROP TABLE orders.customer_warnings;
