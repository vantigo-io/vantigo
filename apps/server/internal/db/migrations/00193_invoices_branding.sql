-- +goose Up
-- Invoices' branding (invoices phase 6 design I1-I6, delivery A): the logo and
-- the phone on the seller record and its snapshot, and a personal message on
-- a delivery.

-- The logo (I1) is a setting: the object's key under the module's storage
-- scope, content-addressed as branding/logo-{sha256}.{png|jpg}, the hash of
-- its bytes and its type, all three or none. Removing the logo clears them
-- and never the object, which a document's snapshot may name. The phone (I4)
-- is optional, '' for none, as the other text columns are (00034).
ALTER TABLE invoices.settings
    ADD COLUMN logo_object_key   varchar(200),
    ADD COLUMN logo_sha256       char(64),
    ADD COLUMN logo_content_type varchar(20),
    ADD COLUMN phone             varchar(30) NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_settings_logo CHECK (
        (logo_object_key IS NULL AND logo_sha256 IS NULL AND logo_content_type IS NULL)
        OR (logo_object_key IS NOT NULL AND logo_sha256 IS NOT NULL
            AND logo_content_type IN ('image/png', 'image/jpeg')));

-- The seller snapshot (I2, I4) gains the logo and the phone, written at issue
-- from the settings. 00034's refuse_issued_document_change compares the whole
-- row, so an issued document's are immutable with no change to it. A
-- document issued before this migration has neither.
ALTER TABLE invoices.invoices
    ADD COLUMN seller_logo_object_key varchar(200),
    ADD COLUMN seller_logo_sha256     char(64),
    ADD COLUMN seller_phone           varchar(30),
    ADD CONSTRAINT ck_invoices_seller_logo CHECK ((seller_logo_object_key IS NULL) = (seller_logo_sha256 IS NULL));

-- The personal message a document was sent with (I6), '' for none. It is
-- blanked when the customer is anonymised, as the recipient is.
ALTER TABLE invoices.deliveries
    ADD COLUMN message varchar(2000) NOT NULL DEFAULT '';

-- A delivery is refused a DELETE, and every UPDATE but the anonymisation's:
-- recipient and message to '', the rest of the row unchanged (00035's rule,
-- widened by the message).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_delivery_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a delivery is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.recipient = '' AND NEW.message = ''
       AND (to_jsonb(OLD) - 'recipient' - 'message') = (to_jsonb(NEW) - 'recipient' - 'message') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a delivery is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- 00035's guard_delivery_insert, which now blanks the message of a marked
-- customer's delivery as well as its recipient: the same wait on the
-- document, then the marker read in a statement of its own.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.guard_delivery_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_status   text;
    parent_customer integer;
BEGIN
    SELECT status, customer_id INTO parent_status, parent_customer
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: a delivery needs an issued document' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = parent_customer) THEN
        NEW.recipient := '';
        NEW.message := '';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.guard_delivery_insert()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_status   text;
    parent_customer integer;
BEGIN
    SELECT status, customer_id INTO parent_status, parent_customer
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: a delivery needs an issued document' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = parent_customer) THEN
        NEW.recipient := '';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices.refuse_delivery_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a delivery is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.recipient = ''
       AND (to_jsonb(OLD) - 'recipient') = (to_jsonb(NEW) - 'recipient') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a delivery is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

-- The message column goes with the function that now names it: the restored
-- functions above never read it.
ALTER TABLE invoices.deliveries DROP COLUMN message;
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_seller_logo,
    DROP COLUMN seller_phone,
    DROP COLUMN seller_logo_sha256,
    DROP COLUMN seller_logo_object_key;
ALTER TABLE invoices.settings
    DROP CONSTRAINT ck_settings_logo,
    DROP COLUMN phone,
    DROP COLUMN logo_content_type,
    DROP COLUMN logo_sha256,
    DROP COLUMN logo_object_key;
