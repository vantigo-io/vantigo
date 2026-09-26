-- name: LockCustomerDocuments :exec
-- LockCustomerDocuments locks every document of the two customers a merge
-- touches, newest first, before RepointCustomer writes them (D10). A
-- credit note's issue locks the credit note, then its older original; an
-- UPDATE locks in whatever order it scans, which can be the original first —
-- and a merge holding the original while waiting on the credit note, beside
-- an issue holding the credit note while waiting on the original, is a
-- deadlock. Locking id-descending takes them in the issue's order.
SELECT id FROM invoices.invoices
WHERE customer_id IN (sqlc.arg(from_customer_id)::integer, sqlc.arg(into_customer_id)::integer)
ORDER BY id DESC
FOR UPDATE;

-- name: RepointCustomer :execrows
-- RepointCustomer moves every document of one customer to another (D10), the
-- merge holder's one write, inside the merge's transaction. A draft's revision
-- and updated_at move as any change to it does; an issued document's do not —
-- the trigger allows exactly the customer_id to change on it (D9), and its
-- buyer snapshot, which is what it printed, is untouched.
UPDATE invoices.invoices SET
    customer_id = @into_customer_id,
    revision = CASE WHEN status = 'draft' THEN revision + 1 ELSE revision END,
    updated_at = CASE WHEN status = 'draft' THEN @now::timestamptz ELSE updated_at END
WHERE customer_id = @from_customer_id;

-- name: CustomerDocuments :many
-- CustomerDocuments is every document of one customer, issued ones by number
-- first and then the drafts: a private person's export (D10).
SELECT * FROM invoices.invoices
WHERE customer_id = @customer_id
ORDER BY status DESC, number, id;

-- name: LinesOf :many
-- LinesOf is the lines of several documents at once, in their documents'
-- order.
SELECT * FROM invoices.lines WHERE invoice_id = ANY(@invoice_ids::bigint[]) ORDER BY invoice_id, position;

-- name: DeleteCustomerDrafts :execrows
-- DeleteCustomerDrafts erases a person's drafts on anonymisation (D10): a
-- draft is not a salgsdokument, so nothing keeps it. Issued documents stay —
-- bokføringsloven § 13 keeps them five years after the financial year.
DELETE FROM invoices.invoices WHERE customer_id = @customer_id AND status = 'draft';
