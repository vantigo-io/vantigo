-- +goose Up
-- A brand on every quote and order (brands design B27, B28, delivery E3).
--
-- brand_id is the brand a document is printed in: set when a draft is made (the
-- customer's default, then its group's, then the workspace default, B28) or
-- when the person switches it for this one document. It has no foreign key: the
-- brand is identity's row, which this module may not reference
-- (internal/db/schema_test.go); it is checked through contracts.Brands.
-- brand_source says where it came from ('chosen', 'customer', 'group',
-- 'default'); brand_skipped is true when a brand higher in the order was
-- archived or unknown when the draft picked its brand, so the editor says so.
-- A document made before this migration has none of them.
--
-- The snapshot is written when a quote is sent and when an order is confirmed:
-- the brand's name, and its logo copied into this module's own storage scope,
-- content-addressed as branding/logo-{sha256}.{png|jpg}, so a sent document
-- prints the same later and does not depend on identity's scope. The logo
-- columns follow the pattern of ck_invoices_brand_logo (00214). A quote sent
-- again after a revision is snapshotted again; the revise opens the brand.
ALTER TABLE orders.quotes
    ADD COLUMN brand_id              uuid,
    ADD COLUMN brand_source          varchar(10),
    ADD COLUMN brand_skipped         boolean NOT NULL DEFAULT false,
    ADD COLUMN brand_name            varchar(100),
    ADD COLUMN brand_logo_object_key varchar(200),
    ADD COLUMN brand_logo_sha256     char(64),
    ADD CONSTRAINT ck_quotes_brand_source CHECK (brand_source IS NULL OR brand_source IN ('chosen', 'customer', 'group', 'default')),
    ADD CONSTRAINT ck_quotes_brand_logo CHECK ((brand_logo_object_key IS NULL) = (brand_logo_sha256 IS NULL));

ALTER TABLE orders.orders
    ADD COLUMN brand_id              uuid,
    ADD COLUMN brand_source          varchar(10),
    ADD COLUMN brand_skipped         boolean NOT NULL DEFAULT false,
    ADD COLUMN brand_name            varchar(100),
    ADD COLUMN brand_logo_object_key varchar(200),
    ADD COLUMN brand_logo_sha256     char(64),
    ADD CONSTRAINT ck_orders_brand_source CHECK (brand_source IS NULL OR brand_source IN ('chosen', 'customer', 'group', 'default')),
    ADD CONSTRAINT ck_orders_brand_logo CHECK ((brand_logo_object_key IS NULL) = (brand_logo_sha256 IS NULL));

-- A subscription made from a quote keeps the quote's brand, which its invoices
-- start in (DueOrder.BrandID). No snapshot: a subscription is not printed.
ALTER TABLE orders.subscriptions
    ADD COLUMN brand_id uuid;

-- A sent quote's and a confirmed order's brand never changes. A quote's brand
-- columns may be written while it is a draft, by the send that freezes it and
-- by the revise that opens it again; an order's while it is a draft, which
-- includes the confirm.
-- +goose StatementBegin
CREATE FUNCTION orders.refuse_quote_brand_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF OLD.status <> 'draft' AND NEW.status <> 'draft'
       AND (OLD.brand_id, OLD.brand_source, OLD.brand_skipped, OLD.brand_name, OLD.brand_logo_object_key, OLD.brand_logo_sha256)
           IS DISTINCT FROM
           (NEW.brand_id, NEW.brand_source, NEW.brand_skipped, NEW.brand_name, NEW.brand_logo_object_key, NEW.brand_logo_sha256) THEN
        RAISE EXCEPTION 'orders: a sent quote keeps its brand' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_quotes_brand_immutable
    BEFORE UPDATE ON orders.quotes
    FOR EACH ROW EXECUTE FUNCTION orders.refuse_quote_brand_change();

-- +goose StatementBegin
CREATE FUNCTION orders.refuse_order_brand_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF OLD.status <> 'draft'
       AND (OLD.brand_id, OLD.brand_source, OLD.brand_skipped, OLD.brand_name, OLD.brand_logo_object_key, OLD.brand_logo_sha256)
           IS DISTINCT FROM
           (NEW.brand_id, NEW.brand_source, NEW.brand_skipped, NEW.brand_name, NEW.brand_logo_object_key, NEW.brand_logo_sha256) THEN
        RAISE EXCEPTION 'orders: a confirmed order keeps its brand' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_orders_brand_immutable
    BEFORE UPDATE ON orders.orders
    FOR EACH ROW EXECUTE FUNCTION orders.refuse_order_brand_change();

-- +goose Down
DROP TRIGGER tr_orders_brand_immutable ON orders.orders;
DROP FUNCTION orders.refuse_order_brand_change();
DROP TRIGGER tr_quotes_brand_immutable ON orders.quotes;
DROP FUNCTION orders.refuse_quote_brand_change();
ALTER TABLE orders.subscriptions DROP COLUMN brand_id;
ALTER TABLE orders.orders
    DROP CONSTRAINT ck_orders_brand_logo,
    DROP CONSTRAINT ck_orders_brand_source,
    DROP COLUMN brand_logo_sha256,
    DROP COLUMN brand_logo_object_key,
    DROP COLUMN brand_name,
    DROP COLUMN brand_skipped,
    DROP COLUMN brand_source,
    DROP COLUMN brand_id;
ALTER TABLE orders.quotes
    DROP CONSTRAINT ck_quotes_brand_logo,
    DROP CONSTRAINT ck_quotes_brand_source,
    DROP COLUMN brand_logo_sha256,
    DROP COLUMN brand_logo_object_key,
    DROP COLUMN brand_name,
    DROP COLUMN brand_skipped,
    DROP COLUMN brand_source,
    DROP COLUMN brand_id;
