-- +goose Up
-- Invoices phase 1B (payments, delivery and the export design D2-D4, D6): the
-- whole phase's schema at once — payment registrations, the delivery log, the
-- erased-customer marker and the one state function — so no later task of the
-- delivery adds a migration. Every existing table is left as 00034 made it.
-- No column is named with a word PostgreSQL reserves (recipient, never "to");
-- a schema test pins it.

-- A registration of money received against an issued invoice (D2). It is
-- bookkeeping material kept with the document, so it is never deleted and
-- never edited: a mistake is removed — removed_at, removed_by_user_id and
-- removal_reason set together, once, with a reason — and registered again.
CREATE TABLE invoices.payments (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id            bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    paid_on               date          NOT NULL,
    amount                numeric(14,2) NOT NULL,
    currency              char(3)       NOT NULL,
    reference             varchar(100)  NOT NULL DEFAULT '',
    note                  varchar(500)  NOT NULL DEFAULT '',
    registered_by_user_id uuid          NOT NULL,
    registered_at         timestamptz   NOT NULL,
    removed_at            timestamptz,
    removed_by_user_id    uuid,
    removal_reason        varchar(200),
    CONSTRAINT ck_payments_amount CHECK (amount > 0),
    CONSTRAINT ck_payments_removal CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL AND removal_reason IS NULL)
        OR (removed_at IS NOT NULL AND removed_by_user_id IS NOT NULL AND removal_reason IS NOT NULL AND removal_reason <> ''))
);
CREATE INDEX ix_payments_invoice ON invoices.payments (invoice_id);
-- The sums read the live rows only.
CREATE INDEX ix_payments_invoice_live ON invoices.payments (invoice_id) WHERE removed_at IS NULL;

-- One e-mail that handed an issued document over (D4): to whom, what, which
-- bytes, when and by whom. recipient becomes '' when the customer is
-- anonymised (D6), and that is the only write a row ever takes.
CREATE TABLE invoices.deliveries (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id      bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    recipient       varchar(254) NOT NULL,
    subject         varchar(300) NOT NULL,
    message_id      varchar(200) NOT NULL,
    pdf_sha256      char(64)     NOT NULL,
    sent_at         timestamptz  NOT NULL,
    sent_by_user_id uuid         NOT NULL
);
CREATE INDEX ix_deliveries_invoice ON invoices.deliveries (invoice_id);

-- The customers this module has anonymised (D6), written by the erase under a
-- lock on the customer's documents and never removed: anonymisation is never
-- undone. customer_id is opaque, as everywhere in this schema.
CREATE TABLE invoices.erased_customers (
    customer_id integer     PRIMARY KEY,
    erased_at   timestamptz NOT NULL
);

-- A payment registration is refused a DELETE, and every UPDATE but two
-- writes, which one statement may make together: the removal — removed_at
-- going from NULL to a value — and the anonymisation's blanking of the note
-- (D6), note going to ''. Whatever else the row holds, as jsonb less the
-- three removal columns and the note, never changes — the 00034
-- refuse_issued_document_change shape, so a column added later is covered;
-- the removal columns change only in the removal, and the note only to ''.
-- The CHECK ck_payments_removal holds the three together.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_payment_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a payment registration is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
           = (to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' - 'note')
       AND (NEW.note = OLD.note OR NEW.note = '')
       AND ((OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
            OR (NEW.note = ''
                AND (OLD.removed_at, OLD.removed_by_user_id, OLD.removal_reason)
                    IS NOT DISTINCT FROM (NEW.removed_at, NEW.removed_by_user_id, NEW.removal_reason))) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a payment registration is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_payments_immutable
    BEFORE UPDATE OR DELETE ON invoices.payments
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_payment_change();

-- A payment belongs to an issued invoice — never a draft, never a credit
-- note. The document is read FOR SHARE, whatever it is, and only then judged
-- (00034's child-row pattern): an insert beside an issue that has not
-- committed yet waits for it. The API refuses first, with its own codes; this
-- is the floor. The read takes the document's current customer too, and the
-- marker is then read in a statement of its own — guard_delivery_insert's
-- shape (D6): a payment registered for a marked customer keeps no staff note,
-- so a registration that waited out an erase does not write one back.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_payment_on_unissued()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_kind     text;
    parent_status   text;
    parent_customer integer;
BEGIN
    SELECT kind, status, customer_id INTO parent_kind, parent_status, parent_customer
    FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
    IF parent_kind IS DISTINCT FROM 'invoice' OR parent_status IS DISTINCT FROM 'issued' THEN
        RAISE EXCEPTION 'invoices: a payment needs an issued invoice' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = parent_customer) THEN
        NEW.note := '';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_payments_parent
    BEFORE INSERT ON invoices.payments
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_payment_on_unissued();

-- A delivery is refused a DELETE, and every UPDATE but the anonymisation's:
-- recipient to '', the rest of the row unchanged.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_delivery_change()
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

CREATE TRIGGER tr_deliveries_immutable
    BEFORE UPDATE OR DELETE ON invoices.deliveries
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_delivery_change();

-- A delivery belongs to an issued document, an invoice or a credit note. The
-- document's status and current customer are read FOR SHARE, so an insert
-- waits for an erase holding the customer's documents FOR UPDATE (D6); and
-- only THEN is the marker read, in a statement of its own: under READ
-- COMMITTED it takes a fresh snapshot, so it sees an erase that committed
-- while this one waited — the inserting statement's own snapshot predates the
-- wait and would not. A marked customer's address is blanked here, so no row
-- written after an erase keeps it. The customer read is the document's
-- current one: a merge in between is covered too.
-- +goose StatementBegin
CREATE FUNCTION invoices.guard_delivery_insert()
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

CREATE TRIGGER tr_deliveries_parent
    BEFORE INSERT ON invoices.deliveries
    FOR EACH ROW EXECUTE FUNCTION invoices.guard_delivery_insert();

-- A document's derived state (D3), the first match winning: a draft; an
-- issued credit note; an invoice its issued credit notes cover (credited > 0,
-- so an invoice of free lines falls to paid); nothing left open; past its due
-- date; partly paid; open. credited is the issued credit notes' gross (0 for
-- a credit note), paid the live payments' sum, and today the Oslo business
-- day from Deps.Clock() — always a parameter, never CURRENT_DATE. The list,
-- the stats and the customer tab filter with it; Go's documentState
-- (internal/invoices/state.go) is its mirror for one response, held to it by
-- a test over every combination. IMMUTABLE and PARALLEL SAFE because it reads
-- nothing but its arguments — expenses.owes_employee's precedent (00033).
-- +goose StatementBegin
CREATE FUNCTION invoices.document_state(kind text, status text, gross numeric, credited numeric, paid numeric, due_date date, today date)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN status = 'draft' THEN 'draft'
        WHEN kind = 'credit_note' THEN 'issued'
        WHEN credited > 0 AND credited >= gross THEN 'credited'
        WHEN gross - credited - paid <= 0 THEN 'paid'
        WHEN due_date < today THEN 'overdue'
        WHEN paid > 0 THEN 'partially_paid'
        ELSE 'open'
    END
$$;
-- +goose StatementEnd

-- +goose Down
-- Everything 00035 added, the tables in reverse (their triggers go with
-- them), then the trigger functions, and document_state last.
DROP TABLE invoices.erased_customers;
DROP TABLE invoices.deliveries;
DROP TABLE invoices.payments;
DROP FUNCTION invoices.guard_delivery_insert();
DROP FUNCTION invoices.refuse_delivery_change();
DROP FUNCTION invoices.refuse_payment_on_unissued();
DROP FUNCTION invoices.refuse_payment_change();
DROP FUNCTION invoices.document_state(text, text, numeric, numeric, numeric, date, date);
