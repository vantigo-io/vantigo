-- Payment attempts (invoices payments and reminders design D16) and the pay
-- page's reads (D15). Every query takes the time as a parameter from
-- Deps.Clock(), never now(). An attempt's create locks its invoice first
-- (lockInvoice), shares the credentials row, and inserts; the result of the
-- provider's call is recorded under the attempt's own lock
-- (lockPaymentAttempt), the invoice untouched.

-- name: PayLinkByToken :one
-- PayLinkByToken is the pay link, live or revoked, whose token hashes to
-- @token_sha256: the pay page's lookup.
SELECT * FROM invoices.pay_links WHERE token_sha256 = @token_sha256;

-- name: LiveReservationsSum :one
-- LiveReservationsSum is what an invoice's live reservations hold: the
-- captures decided and not yet registered or disproved (capturing or
-- capture_unknown, D2, D16).
SELECT coalesce(sum(reserved_amount), 0)::numeric(14,2) AS reserved FROM invoices.payment_attempts
WHERE invoice_id = @invoice_id AND state IN ('capturing', 'capture_unknown');

-- name: LiveAttemptsOfInvoice :one
-- LiveAttemptsOfInvoice counts an invoice's attempts that may still pay it —
-- creating, created, authorized, capturing or capture_unknown — for the cap
-- of five (D15), under the invoice's lock.
SELECT count(*) FROM invoices.payment_attempts
WHERE invoice_id = @invoice_id
  AND state IN ('creating', 'created', 'authorized', 'capturing', 'capture_unknown');

-- name: InsertPaymentAttempt :one
-- InsertPaymentAttempt inserts an attempt as creating, under its invoice's
-- lock, its create key and cancel key persisted before any call;
-- tr_payment_attempts_reference sets its reference from its own id.
INSERT INTO invoices.payment_attempts (
    invoice_id, provider, msn, return_nonce_sha256, flow, pay_link_id, reference, amount,
    create_key, cancel_key, state, created_at, created_by_user_id, expires_at, next_poll_at
) VALUES (
    @invoice_id, 'vipps', @msn, sqlc.narg(return_nonce_sha256), @flow, sqlc.narg(pay_link_id), 'set-by-trigger', @amount,
    @create_key, @cancel_key, 'creating', @now::timestamptz, sqlc.narg(created_by_user_id),
    @expires_at::timestamptz, @next_poll_at::timestamptz
)
RETURNING *;

-- name: MarkAttemptCreated :execrows
-- MarkAttemptCreated records what Vipps answered a create: the attempt
-- created, the address the payer is sent to kept (never logged, never
-- answered on the document), the first poll set. Only from creating: the
-- payments worker may have learned more meanwhile.
UPDATE invoices.payment_attempts
SET state = 'created', redirect_url = @redirect_url, next_poll_at = @next_poll_at::timestamptz, last_error = NULL
WHERE id = @id AND state = 'creating';

-- name: MarkAttemptFailed :execrows
-- MarkAttemptFailed records Vipps' definitive refusal of a create: the
-- attempt failed and finished, nothing left to poll. Only from creating.
UPDATE invoices.payment_attempts
SET state = 'failed', finished_at = @now::timestamptz, next_poll_at = NULL, last_error = @last_error
WHERE id = @id AND state = 'creating';

-- name: PayAttemptByReference :one
-- PayAttemptByReference is a pay-page attempt by its reference, for the
-- return page's status read; on-site attempts have no nonce and are never
-- answered.
SELECT * FROM invoices.payment_attempts WHERE reference = @reference AND flow = 'pay_link';

-- name: PaymentAttemptsOf :many
-- PaymentAttemptsOf is every attempt of an invoice, oldest first, for the
-- document.
SELECT * FROM invoices.payment_attempts WHERE invoice_id = @invoice_id ORDER BY id;
