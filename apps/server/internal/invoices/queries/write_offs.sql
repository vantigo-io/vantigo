-- Write-offs (accounting design A16 as amended by R4): an issued invoice's
-- open amount given up on a day with a reason, undone with a reason, never
-- edited or deleted.

-- name: LiveWriteOff :one
-- LiveWriteOff is the invoice's standing write-off, read after LockInvoice.
SELECT * FROM invoices.write_offs WHERE invoice_id = @invoice_id AND undone_at IS NULL;

-- name: WriteOffsOf :many
-- WriteOffsOf is every write-off of an invoice, undone ones included, in the
-- order they were made.
SELECT * FROM invoices.write_offs WHERE invoice_id = @invoice_id ORDER BY registered_at, id;

-- name: InsertWriteOff :one
-- InsertWriteOff gives up the open amount. The caller holds the invoice FOR
-- UPDATE and has judged the amount under that lock; ux_write_offs_live keeps
-- one standing write-off per invoice, tr_write_offs_parent an issued invoice.
INSERT INTO invoices.write_offs (
    invoice_id, written_off_on, amount, currency, reason, vat_correction, registered_by_user_id, registered_at
) VALUES (
    @invoice_id, @written_off_on, @amount, @currency, @reason, @vat_correction, @registered_by_user_id, @registered_at::timestamptz
)
RETURNING *;

-- name: UndoWriteOff :execrows
-- UndoWriteOff undoes the standing write-off, once: the three columns set
-- together, of that invoice's write-off only.
UPDATE invoices.write_offs
SET undone_at = @undone_at::timestamptz, undone_by_user_id = @undone_by_user_id::uuid, undo_reason = @undo_reason::text
WHERE id = @id AND invoice_id = @invoice_id AND undone_at IS NULL;

-- name: LiveRecoveries :one
-- LiveRecoveries is what the live recovered payments of a write-off add up to.
SELECT coalesce(sum(amount), 0)::numeric(14,2) AS recovered FROM invoices.payments
WHERE write_off_id = @write_off_id::bigint AND removed_at IS NULL;
