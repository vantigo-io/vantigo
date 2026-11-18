-- +goose Up
-- Accounting's side of the customer's credit (invoices phase 6 design I16;
-- plan docs/superpowers/plans/2026-11-17-invoices-phase6-plan.md, delivery
-- B-1, step B4 and reading 5). Invoices' side is 00197.
--
-- A customer's credit is held on one open item of the customer's, on 1500,
-- with no document: a flag on a customer item, since an item's kind is the
-- party (ck_open_items_party). At most one per customer. It is never the
-- unallocated item, and has no document, number or original amount. The
-- opening records on it the AsOfSeq of the CreditsAt page it read the
-- customer's balance on, as it does on a document's item (R14).
ALTER TABLE accounting.open_items
    ADD COLUMN customer_credit boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT ck_open_items_unallocated,
    DROP CONSTRAINT ck_open_items_opening_seq,
    ADD CONSTRAINT ck_open_items_unallocated CHECK (
        NOT unallocated OR (document_number IS NULL AND source_document_id IS NULL AND original_amount = 0
            AND NOT customer_credit)),
    ADD CONSTRAINT ck_open_items_customer_credit CHECK (
        NOT customer_credit OR (kind = 'customer' AND document_number IS NULL AND source_document_id IS NULL
            AND original_amount = 0)),
    ADD CONSTRAINT ck_open_items_opening_seq CHECK (
        opening_as_of_seq IS NULL OR source_document_id IS NOT NULL OR customer_credit);
CREATE UNIQUE INDEX ux_open_items_customer_credit ON accounting.open_items (customer_id) WHERE customer_credit;

-- +goose Down
-- A credit item with postings is bookkeeping: the Down refuses while one
-- exists, posted or not.
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM accounting.open_items WHERE customer_credit) THEN
        RAISE EXCEPTION 'accounting: a customer credit item exists' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

DROP INDEX accounting.ux_open_items_customer_credit;
ALTER TABLE accounting.open_items
    DROP CONSTRAINT ck_open_items_opening_seq,
    DROP CONSTRAINT ck_open_items_customer_credit,
    DROP CONSTRAINT ck_open_items_unallocated,
    ADD CONSTRAINT ck_open_items_unallocated CHECK (
        NOT unallocated OR (document_number IS NULL AND source_document_id IS NULL AND original_amount = 0)),
    ADD CONSTRAINT ck_open_items_opening_seq CHECK (opening_as_of_seq IS NULL OR source_document_id IS NOT NULL),
    DROP COLUMN customer_credit;
