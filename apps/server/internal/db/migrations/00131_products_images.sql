-- +goose Up
-- Images of a product (messenger design N13, delivery P): the pictures a
-- person shows a customer in a message and, one day, in marketing. The bytes
-- live in the object store under the scope products-images — the normalised
-- file and its 320 px thumbnail, both named here — and everything about them
-- lives in this row.
--
-- At most one image of a product is the primary one, by the partial unique
-- index; the first upload becomes it and deleting it promotes the next by
-- sort_order, both Go's rules. uses says where the image may be shown:
-- 'communications' to a customer in a message, 'marketing' for marketing use
-- that nothing reads yet; the CHECK keeps the two words the only ones.
-- created_by is the uploading user's id, a foreign identifier as every module's.
CREATE TABLE products.product_images (
    id                integer     GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    product_id        integer     NOT NULL REFERENCES products.products (id) ON DELETE CASCADE,
    storage_key       text        NOT NULL,
    thumb_storage_key text        NOT NULL,
    content_type      text        NOT NULL,
    byte_size         integer     NOT NULL,
    width             integer     NOT NULL,
    height            integer     NOT NULL,
    sha256            text        NOT NULL,
    alt_text          varchar(250),
    sort_order        integer     NOT NULL DEFAULT 0,
    is_primary        boolean     NOT NULL DEFAULT false,
    uses              text[]      NOT NULL DEFAULT '{communications}',
    created_at        timestamptz NOT NULL,
    created_by        uuid        NOT NULL,
    CONSTRAINT ck_product_images_uses CHECK (uses <@ ARRAY['communications', 'marketing']::text[])
);
CREATE INDEX ix_product_images_product ON products.product_images (product_id, sort_order, id);
CREATE UNIQUE INDEX ux_product_images_primary ON products.product_images (product_id) WHERE is_primary;

-- +goose Down
DROP TABLE products.product_images;
