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
-- LiveAttemptsOfInvoice counts the attempts that hold a place under the cap
-- of five (D15), under the invoice's lock: of the pay link @pay_link_id on
-- the pay-link flow — so a revoke and a new link restore service — or of
-- the invoice when it is NULL (on site). An attempt counts while it may
-- still pay: authorized, capturing or capture_unknown, and creating or
-- created until its request expires — the payments worker still resolves
-- an expired one, but it no longer holds the payer back.
SELECT count(*) FROM invoices.payment_attempts
WHERE invoice_id = @invoice_id
  AND (sqlc.narg(pay_link_id)::bigint IS NULL OR pay_link_id = sqlc.narg(pay_link_id)::bigint)
  AND (state IN ('authorized', 'capturing', 'capture_unknown')
       OR (state IN ('creating', 'created') AND expires_at > @now::timestamptz));

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

-- name: GetPaymentAttempt :one
-- GetPaymentAttempt is one attempt by id, read on the pool before an
-- abandon judges it; no row is pgx.ErrNoRows.
SELECT * FROM invoices.payment_attempts WHERE id = @id;

-- name: LeaseAttemptForAbandon :execrows
-- LeaseAttemptForAbandon takes an abandon's lease on an attempt it may
-- abandon, while no claim's lease is live: one statement, its row lock held
-- for it alone, as the worker's claim (m11). While the lease lives no worker
-- claims the attempt, so nothing is sent to Vipps between the abandon's read
-- and its write. Nothing leased when a claim holds it or it moved — a
-- create handed back to the worker since the abandon read it parked among
-- them.
UPDATE invoices.payment_attempts
SET lease_id = @lease_id::text, lease_until = @lease_until::timestamptz
WHERE id = @id AND state = @state::text AND capture_attempts = @capture_attempts
  AND (state NOT IN ('creating', 'created') OR next_poll_at IS NULL)
  AND (lease_until IS NULL OR lease_until <= @now::timestamptz);

-- name: ReleaseAttemptLease :execrows
-- ReleaseAttemptLease gives an abandon's lease back, nothing else changed.
UPDATE invoices.payment_attempts
SET lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text;

-- name: UnparkAttempt :execrows
-- UnparkAttempt hands an attempt parked for a person back to the payments
-- worker, due at @now: a create Vipps turns out to know, or a release the
-- person made in the Vipps portal. Its lease, the abandon's or none live,
-- ends; its failures in a row start again.
UPDATE invoices.payment_attempts
SET next_poll_at = @now::timestamptz, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND next_poll_at IS NULL
  AND (lease_id = @lease_id::text OR lease_until IS NULL OR lease_until <= @now::timestamptz);

-- name: AbandonPaymentAttempt :one
-- AbandonPaymentAttempt is an abandon's write (D16), under the attempt's own
-- lock: abandoned, the reservation released, who, when and why recorded,
-- never polled again. The caller has judged the state, the captures sent and
-- the lease under that lock.
UPDATE invoices.payment_attempts
SET state = 'abandoned', reserved_amount = NULL, abandoned_by_user_id = @abandoned_by_user_id,
    abandoned_at = @now::timestamptz, abandon_reason = @abandon_reason::text, finished_at = @now::timestamptz,
    next_poll_at = NULL, registration_failed_at = NULL, lease_id = NULL, lease_until = NULL
WHERE id = @id AND state IN ('creating', 'created', 'capturing', 'capture_unknown')
RETURNING *;
