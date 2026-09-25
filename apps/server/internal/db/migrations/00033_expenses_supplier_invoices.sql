-- +goose Up
-- Supplier invoices (supplier invoices design D1): the invoice a supplier
-- sends for work or goods on a project, recorded as a fourth kind of expense,
-- kind = 'supplier_invoice'. kind has always been a plain varchar(20) with no
-- CHECK (00012's house style), so the kind itself needs no DDL; what does is
-- the two facts an outlay never had.
--
-- supplier_invoice_number is the supplier's own number for the invoice: free
-- text, at most 100 characters, and not unique — there is no supplier record
-- to make it unique under, only the free-text supplier column. The name keeps
-- clear of invoiced_at / invoice_reference, which are the *outgoing* stamp:
-- the customer's invoice, not the supplier's. supplier_due_date is when the
-- supplier wants paying, on or after the entry date, which on this kind *is*
-- the invoice date. Both are NULL on every other kind; Go refuses them there,
-- and no CHECK ties them to the kind.
ALTER TABLE expenses.entries
    ADD COLUMN supplier_invoice_number varchar(100),
    ADD COLUMN supplier_due_date       date;

-- owes_employee is the one rule for who is owed money back (design D1). It
-- was written out fourteen times — thirteen in SQL, once in Go — and every
-- copy would have called a supplier invoice owed to the employee. Now every
-- query that asks calls this, and Go's owesEmployee (authorize.go) is its
-- mirror, held to it by a test on every kind and payer: an outlay owes its
-- gross only when the employee paid it; a supplier invoice owes nobody,
-- whatever its paid_by says; mileage and a per diem day are always the
-- employee's. "Owes something" still needs gross_amount > 0 beside it: that
-- half is about the amount, and every caller already says it.
--
-- IMMUTABLE and PARALLEL SAFE: it reads nothing but its arguments, so the
-- planner may inline it into every query that calls it — the predicate costs
-- what the inline one did.
-- +goose StatementBegin
CREATE FUNCTION expenses.owes_employee(kind text, paid_by text)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE $1
        WHEN 'outlay' THEN $2 IS NOT DISTINCT FROM 'employee'
        WHEN 'supplier_invoice' THEN false
        ELSE true
    END
$$;
-- +goose StatementEnd

-- +goose Down
-- A supplier invoice becomes the company-paid outlay it would have been
-- before this migration: paid_by is 'company' on every one, so the inline
-- predicate the older queries carry owes nobody for it, exactly as the
-- function did. Its number and due date go with their columns.
UPDATE expenses.entries SET kind = 'outlay' WHERE kind = 'supplier_invoice';
DROP FUNCTION expenses.owes_employee(text, text);
ALTER TABLE expenses.entries
    DROP COLUMN supplier_invoice_number,
    DROP COLUMN supplier_due_date;
