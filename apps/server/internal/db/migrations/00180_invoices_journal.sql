-- +goose Up
-- Invoices' side of the sales journal (accounting design A12, R1, R6;
-- plan docs/superpowers/plans/2026-11-14-accounting-plan.md, delivery 0): what
-- an issued line was made from, a log that puts payments, charge payments and
-- their removals in one order, and the event a removal publishes. Nothing here
-- names a ledger; contracts.SalesJournal reads it.

-- What a line was made from (R6), snapshotted at issue by the statement that
-- already snapshots its VAT (SnapshotLine), while the document is still a
-- draft: refuse_issued_child_change (00034) refuses every write after, as it
-- does today. NULL on a draft, and on a line issued before this migration —
-- those fall back to their VAT code. line_kind is the source's own word:
-- hours, expense and milestone for a project's work, settlement for energy's,
-- order_line and subscription_period for an order allocation's, deduction for
-- an a-konto deduction, manual for a line made from nothing, and mixed for a
-- line naming sources of more than one kind. A credit note's line takes its
-- original line's. tax_category_id is Products' tax category, known only for
-- an order line's product; project_id the project every source of the line
-- belongs to. Both are opaque ids, as everywhere in this schema.
ALTER TABLE invoices.lines
    ADD COLUMN line_kind       varchar(20),
    ADD COLUMN tax_category_id integer,
    ADD COLUMN project_id      integer,
    ADD CONSTRAINT ck_lines_line_kind CHECK (line_kind IN (
        'hours', 'expense', 'milestone', 'settlement', 'order_line', 'subscription_period',
        'deduction', 'manual', 'mixed'));

-- The tax category of the product an order allocation bills, taken when the
-- draft is made (Products' catalog, read through contracts.ProductCatalog), so
-- the issue can snapshot it without calling out under its lock. No other kind
-- of source has a product.
ALTER TABLE invoices.line_sources
    ADD COLUMN tax_category_id integer,
    ADD CONSTRAINT ck_line_sources_tax_category
        CHECK (tax_category_id IS NULL OR source_kind = 'orders.allocation');

-- The fact log (R1). A document has its number, which the documents counter
-- hands out under a lock held to commit, so documents are read in number
-- order. Payments and charge payments have no such order: their ids are taken
-- before commit, and a removal is an update. So every registration and every
-- removal writes one row here, and seq orders them.
--
-- seq comes from the counter row 'journal' (00034's counters, beside
-- 'documents'), taken by a DEFERRABLE INITIALLY DEFERRED constraint trigger —
-- at commit, the last thing the transaction does — and held for that instant
-- only. Two transactions therefore take their seqs in the order they commit,
-- and a seq is visible before the next is taken: a reader paging by seq never
-- passes a fact that commits later with a lower one. A bank line paying
-- several invoices takes the counter once per payment, at commit, after every
-- invoice lock it needs, so it cannot deadlock on it. The row is made by the
-- first fact, as 'documents' is by the first issue.
--
-- business_date is a registration's paid_on. A removal's day is the business
-- day its removed_at falls on in the workspace's time zone, which this schema
-- cannot read (it is identity's), so the row keeps removed_at and the reader
-- (contracts.SalesJournal) takes the day. Exactly one of the two is set.
-- Append-only: refuse_journal_fact_change refuses every UPDATE and DELETE.
CREATE TABLE invoices.journal_facts (
    seq           bigint      PRIMARY KEY,
    kind          varchar(30) NOT NULL,
    subject_id    bigint      NOT NULL,
    invoice_id    bigint      NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    business_date date,
    removed_at    timestamptz,
    created_at    timestamptz NOT NULL,
    CONSTRAINT ck_journal_facts_seq CHECK (seq >= 1),
    CONSTRAINT ck_journal_facts_kind
        CHECK (kind IN ('payment', 'payment_removed', 'charge_payment', 'charge_payment_removed')),
    CONSTRAINT ck_journal_facts_day CHECK ((business_date IS NULL) <> (removed_at IS NULL)),
    CONSTRAINT ck_journal_facts_removal
        CHECK ((kind IN ('payment_removed', 'charge_payment_removed')) = (removed_at IS NOT NULL))
);
-- One fact of each kind for a row: a registration once, its removal once.
CREATE UNIQUE INDEX ux_journal_facts_subject ON invoices.journal_facts (kind, subject_id);
CREATE INDEX ix_journal_facts_invoice ON invoices.journal_facts (invoice_id);

-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_journal_fact_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'invoices: a journal fact is never changed' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_journal_facts_immutable
    BEFORE UPDATE OR DELETE ON invoices.journal_facts
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_journal_fact_change();

-- The facts that exist when this migration runs, so a company that turns the
-- ledger on later finds its history in the log: every registration, payments
-- and charge payments together in (registered_at, id) order, then every
-- removal in (removed_at, id) order, numbered from 1. The counter row is made
-- only when there is something to number.
INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, removed_at, created_at)
SELECT row_number() OVER (ORDER BY f.phase, f.at, f.subject_id, f.kind), f.kind, f.subject_id, f.invoice_id,
    f.business_date, f.removed_at, now()
FROM (
    SELECT 1 AS phase, registered_at AS at, 'payment' AS kind, id AS subject_id, invoice_id,
        paid_on AS business_date, NULL::timestamptz AS removed_at
    FROM invoices.payments
    UNION ALL
    SELECT 1, registered_at, 'charge_payment', id, invoice_id, paid_on, NULL
    FROM invoices.charge_payments
    UNION ALL
    SELECT 2, removed_at, 'payment_removed', id, invoice_id, NULL, removed_at
    FROM invoices.payments WHERE removed_at IS NOT NULL
    UNION ALL
    SELECT 2, removed_at, 'charge_payment_removed', id, invoice_id, NULL, removed_at
    FROM invoices.charge_payments WHERE removed_at IS NOT NULL
) AS f;

INSERT INTO invoices.counters (counter_name, next_value)
SELECT 'journal', max(seq) + 1 FROM invoices.journal_facts HAVING count(*) > 0;

-- The trigger behind every fact from now on. TG_ARGV[0] is the row's kind,
-- 'payment' or 'charge_payment'; an INSERT is its registration, an UPDATE that
-- sets removed_at (the only one a WHEN lets through) its removal. It runs at
-- commit, so NEW is the row as the transaction left it.
-- +goose StatementBegin
CREATE FUNCTION invoices.record_journal_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    next_seq bigint;
BEGIN
    INSERT INTO invoices.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    IF TG_OP = 'INSERT' THEN
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, created_at)
        VALUES (next_seq, TG_ARGV[0], NEW.id, NEW.invoice_id, NEW.paid_on, now());
    ELSE
        INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, removed_at, created_at)
        VALUES (next_seq, TG_ARGV[0] || '_removed', NEW.id, NEW.invoice_id, NEW.removed_at, now());
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER tr_payments_journal_registered
    AFTER INSERT ON invoices.payments
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION invoices.record_journal_fact('payment');
CREATE CONSTRAINT TRIGGER tr_payments_journal_removed
    AFTER UPDATE ON invoices.payments
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
    EXECUTE FUNCTION invoices.record_journal_fact('payment');
CREATE CONSTRAINT TRIGGER tr_charge_payments_journal_registered
    AFTER INSERT ON invoices.charge_payments
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION invoices.record_journal_fact('charge_payment');
CREATE CONSTRAINT TRIGGER tr_charge_payments_journal_removed
    AFTER UPDATE ON invoices.charge_payments
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
    EXECUTE FUNCTION invoices.record_journal_fact('charge_payment');

-- invoices.payment.removed, published by the removal itself as 00150's
-- invoices.payment.received is by the registration: two write paths remove a
-- payment (a person, and a reversal the bank reports), each holding the
-- invoice FOR UPDATE. The payload is contracts.PaymentRemoved: ids, the
-- amount, the day it was paid on and the moment it was removed, no reason and
-- no note. The business day of removedAt is the reader's to take, for the
-- journal facts' reason above.
-- +goose StatementBegin
CREATE FUNCTION invoices.publish_payment_removed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    inv invoices.invoices%ROWTYPE;
BEGIN
    SELECT * INTO inv FROM invoices.invoices WHERE id = NEW.invoice_id;
    INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
    VALUES ('invoices.payment.removed', 1, 'invoices', 'invoices.payment', NEW.id::text,
            jsonb_build_object(
                'paymentId', NEW.id, 'invoiceId', NEW.invoice_id, 'number', coalesce(inv.number, 0),
                'customerId', inv.customer_id, 'amount', to_char(NEW.amount, 'FM999999999990.00'), 'currency', NEW.currency,
                'paidOn', to_char(NEW.paid_on, 'YYYY-MM-DD'), 'source', NEW.source, 'removedAt', NEW.removed_at),
            NEW.removed_at);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER tr_payments_removed AFTER UPDATE ON invoices.payments
    FOR EACH ROW WHEN (OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL)
    EXECUTE FUNCTION invoices.publish_payment_removed();

-- +goose Down
DROP TRIGGER tr_payments_removed ON invoices.payments;
DROP FUNCTION invoices.publish_payment_removed();
DROP TRIGGER tr_charge_payments_journal_removed ON invoices.charge_payments;
DROP TRIGGER tr_charge_payments_journal_registered ON invoices.charge_payments;
DROP TRIGGER tr_payments_journal_removed ON invoices.payments;
DROP TRIGGER tr_payments_journal_registered ON invoices.payments;
DROP FUNCTION invoices.record_journal_fact();
DROP TABLE invoices.journal_facts;
DROP FUNCTION invoices.refuse_journal_fact_change();
DELETE FROM invoices.counters WHERE counter_name = 'journal';
ALTER TABLE invoices.line_sources
    DROP CONSTRAINT ck_line_sources_tax_category,
    DROP COLUMN tax_category_id;
-- The snapshots are dropped with their columns; an issued line's other
-- columns are untouched, so refuse_issued_child_change has nothing to refuse.
ALTER TABLE invoices.lines
    DROP CONSTRAINT ck_lines_line_kind,
    DROP COLUMN project_id,
    DROP COLUMN tax_category_id,
    DROP COLUMN line_kind;
