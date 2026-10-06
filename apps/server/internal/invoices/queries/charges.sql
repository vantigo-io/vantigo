-- The charges of an invoice (invoices payments and reminders design D9):
-- what its sent letters claimed, the waivers that released part of it and
-- the charge payments that paid it. Kept apart from the principal: the
-- payments, the open amount and the state never read these tables.

-- name: SentLettersOf :many
-- SentLettersOf is an invoice's sent letters, by sequence: only a sent
-- letter claims a charge (D9), and the latest of them claims the interest.
SELECT * FROM invoices.reminders
WHERE invoice_id = @invoice_id AND status = 'sent'
ORDER BY sequence, id;

-- name: WaiversOf :many
-- WaiversOf is every charge waiver of an invoice, the first first. A waiver
-- is never removed, so every row counts.
SELECT * FROM invoices.charge_waivers
WHERE invoice_id = @invoice_id
ORDER BY id;

-- name: ChargePaymentsOf :many
-- ChargePaymentsOf is every charge payment of an invoice, removed ones
-- included — the record is the point, as the payments' — in the order the
-- money arrived. The formula is fed the live ones only.
SELECT * FROM invoices.charge_payments
WHERE invoice_id = @invoice_id
ORDER BY paid_on, id;

-- name: InsertChargePayment :one
-- InsertChargePayment records money received against an invoice's charges
-- (D9): by hand (source manual, no bank line) or from a bank line by the
-- match or the queue's apply. The caller holds the invoice FOR UPDATE and has
-- judged the charges outstanding under that lock; tr_charge_payments_parent
-- refuses a draft's or a credit note's anyway.
INSERT INTO invoices.charge_payments (
    invoice_id, paid_on, amount, currency, source, bank_transaction_id, reference, note,
    registered_by_user_id, registered_at
) VALUES (
    @invoice_id, @paid_on, @amount, @currency, @source, sqlc.narg(bank_transaction_id), @reference, @note,
    @registered_by_user_id, @registered_at::timestamptz
)
RETURNING *;

-- name: GetChargePayment :one
-- GetChargePayment is one charge payment of one invoice, read after the
-- invoice's lock so two racing removals see each other.
SELECT * FROM invoices.charge_payments WHERE id = @id AND invoice_id = @invoice_id;

-- name: RemoveChargePayment :execrows
-- RemoveChargePayment is the soft removal, once: the three columns set
-- together, of that invoice's charge payment only. One already removed
-- matches no row; tr_charge_payments_immutable would refuse it anyway.
UPDATE invoices.charge_payments
SET removed_at = @removed_at::timestamptz, removed_by_user_id = @removed_by_user_id::uuid, removal_reason = @removal_reason::text
WHERE id = @id AND invoice_id = @invoice_id AND removed_at IS NULL;

-- name: InsertWaiver :one
-- InsertWaiver releases a charge a letter claimed (D9): a fee or the
-- compensation whole, interest as an amount with the day it runs through.
-- The caller holds the invoice FOR UPDATE and has judged the charge under
-- that lock; the composite foreign key keeps the letter the invoice's own,
-- and ux_charge_waivers_letter_kind one fee or compensation waiver a letter.
INSERT INTO invoices.charge_waivers (
    invoice_id, reminder_id, kind, amount, interest_through, reason, note, waived_by_user_id, waived_at
) VALUES (
    @invoice_id, @reminder_id, @kind, @amount, sqlc.narg(interest_through), @reason, @note,
    @waived_by_user_id, @waived_at::timestamptz
)
RETURNING *;

-- name: ReminderOf :one
-- ReminderOf is one letter of one invoice, in any status: a waiver names a
-- letter, and one that is not the invoice's is not found.
SELECT * FROM invoices.reminders WHERE id = @id AND invoice_id = @invoice_id;
