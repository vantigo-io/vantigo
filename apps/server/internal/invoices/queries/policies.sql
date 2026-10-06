-- A customer's reminder policy (invoices payments and reminders design D7):
-- no row is normal. The PUT's locks are locks.sql's — the customer's
-- documents FOR SHARE, newest first, then the policy row — and the merge's
-- are the documents, then the policy rows by customer id (D18).

-- name: GetPolicy :one
SELECT * FROM invoices.customer_reminder_policies WHERE customer_id = @customer_id;

-- name: CustomerHasDocuments :one
-- CustomerHasDocuments answers whether the customer has a document here —
-- an issued one or a draft — and whether it is anonymised: the PUT's 404s,
-- read under the documents' share lock.
SELECT
    EXISTS (SELECT 1 FROM invoices.invoices WHERE customer_id = sqlc.arg(customer_id)::integer)::boolean AS has_documents,
    EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = sqlc.arg(customer_id)::integer)::boolean AS erased;

-- name: UpsertPolicy :one
INSERT INTO invoices.customer_reminder_policies (customer_id, mode, note, updated_by_user_id, updated_at)
VALUES (@customer_id, @mode, @note, @updated_by_user_id, @updated_at)
ON CONFLICT (customer_id) DO UPDATE SET
    mode = EXCLUDED.mode, note = EXCLUDED.note,
    updated_by_user_id = EXCLUDED.updated_by_user_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeletePolicy :execrows
-- DeletePolicy removes a customer's policy: a PUT of normal with an empty
-- note, the merge's absorbed row, and the erase.
DELETE FROM invoices.customer_reminder_policies WHERE customer_id = @customer_id;

-- name: PoliciesOf :many
-- PoliciesOf is the policies of the merge's two customers, by customer id.
SELECT * FROM invoices.customer_reminder_policies
WHERE customer_id = ANY(sqlc.arg(customer_ids)::integer[])
ORDER BY customer_id;

-- name: MovePolicy :execrows
-- MovePolicy re-points the absorbed customer's policy to the survivor, which
-- has none (the merge, D7).
UPDATE invoices.customer_reminder_policies SET customer_id = @into_customer_id
WHERE customer_id = @from_customer_id;
