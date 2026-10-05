-- +goose Up
-- The invoice that invoiced an entry (invoices work design D1): its id and
-- number, opaque — the invoices schema is another module's (rule 4). Written
-- only by the invoices issue through contracts.InvoicedWorkHolder, cleared only
-- by the credit note that returns the entry's line. Both or neither, and only
-- on an invoiced entry; an entry stamped by hand carries invoiced_at without them.
ALTER TABLE time.entries
    ADD COLUMN invoiced_invoice_id bigint,
    ADD COLUMN invoiced_number     bigint,
    ADD CONSTRAINT ck_entries_invoiced_by
        CHECK ((invoiced_invoice_id IS NULL) = (invoiced_number IS NULL)),
    ADD CONSTRAINT ck_entries_invoiced_by_status
        CHECK (invoiced_invoice_id IS NULL OR (status = 'invoiced' AND invoiced_at IS NOT NULL));

-- +goose Down
ALTER TABLE time.entries
    DROP CONSTRAINT ck_entries_invoiced_by_status,
    DROP CONSTRAINT ck_entries_invoiced_by,
    DROP COLUMN invoiced_number,
    DROP COLUMN invoiced_invoice_id;
