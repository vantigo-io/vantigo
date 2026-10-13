-- Pay links (invoices payments and reminders design D15): one live link per
-- issued invoice, the token's SHA-256 only, the URL sealed so the PDF, the
-- cover e-mail and a reminder letter can print it again.

-- name: InsertPayLink :one
-- InsertPayLink makes an invoice's live link. The caller holds the invoice
-- FOR UPDATE — the issue's own document, or an on-demand make's lock — so no
-- second link is made beside it; tr_pay_links_parent refuses a draft's, a
-- credit note's and an anonymised customer's.
INSERT INTO invoices.pay_links (
    invoice_id, token_sha256, url_ciphertext, created_at, created_by_user_id
) VALUES (
    @invoice_id, @token_sha256, @url_ciphertext, @created_at::timestamptz, sqlc.narg(created_by_user_id)
)
RETURNING *;

-- name: LivePayLink :one
-- LivePayLink is an invoice's live link, if it has one.
SELECT * FROM invoices.pay_links WHERE invoice_id = @invoice_id AND revoked_at IS NULL;

-- name: RevokePayLink :execrows
-- RevokePayLink revokes a link once; the caller holds the invoice and then
-- the link (lockPayLink).
UPDATE invoices.pay_links
SET revoked_at = @now::timestamptz, revoked_by_user_id = sqlc.narg(by)
WHERE id = @id AND revoked_at IS NULL;
