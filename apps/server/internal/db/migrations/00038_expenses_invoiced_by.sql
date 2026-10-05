-- +goose Up
-- The invoice that invoiced an expense line (invoices work design D1): its id
-- and number, opaque — the invoices schema is another module's (rule 4).
-- Written only by the invoices issue through contracts.InvoicedWorkHolder,
-- cleared only by the credit note that returns the line. Both or neither, and
-- only beside invoiced_at with no hand-typed reference: a line marked by hand
-- through POST /entries/{id}/invoiced carries invoiced_at and maybe a
-- reference, and never these two.
ALTER TABLE expenses.entries
    ADD COLUMN invoiced_invoice_id bigint,
    ADD COLUMN invoiced_number     bigint,
    ADD CONSTRAINT ck_entries_invoiced_by
        CHECK ((invoiced_invoice_id IS NULL) = (invoiced_number IS NULL)),
    ADD CONSTRAINT ck_entries_invoiced_by_stamp
        CHECK (invoiced_invoice_id IS NULL OR (invoiced_at IS NOT NULL AND invoice_reference IS NULL));

-- The index behind the billable read's supplier_invoice_rebilled (invoices
-- work design D15): for every supplier invoice it asks whether another one
-- with the same number has already been invoiced. Partial on exactly that, so
-- it holds only the invoiced supplier invoices and the lookup is one probe by
-- number; the supplier is compared on the few rows it finds.
CREATE INDEX ix_entries_supplier_invoice_invoiced ON expenses.entries (supplier_invoice_number)
    WHERE kind = 'supplier_invoice' AND invoiced_at IS NOT NULL;

-- ready_to_invoice is the one "ready to invoice" rule (invoices work design D3):
-- the unit approved, billable, not a per diem day, priced. It deliberately
-- leaves out "not invoiced yet" — every caller states invoiced_at IS NULL
-- beside it — so the invoices holder can judge "already invoiced" first. Go's
-- invoicedRefusal (invoiced.go) is its mirror, held to it by a test over every
-- combination. IMMUTABLE and PARALLEL SAFE for owes_employee's reason: it reads
-- nothing but its arguments, and as a one-SELECT SQL function the planner
-- inlines it, so the predicate costs what the inline copies did.
-- +goose StatementBegin
CREATE FUNCTION expenses.ready_to_invoice(unit_status text, billable boolean, kind text, bill_amount numeric)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT $1 = 'approved' AND $2 AND $3 <> 'per_diem' AND $4 IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION expenses.ready_to_invoice(text, boolean, text, numeric);
DROP INDEX expenses.ix_entries_supplier_invoice_invoiced;
ALTER TABLE expenses.entries
    DROP CONSTRAINT ck_entries_invoiced_by_stamp,
    DROP CONSTRAINT ck_entries_invoiced_by,
    DROP COLUMN invoiced_number,
    DROP COLUMN invoiced_invoice_id;
