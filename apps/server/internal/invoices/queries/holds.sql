-- Holds, the hand-off to collection and its export (invoices payments and
-- reminders design D11). Every write runs under the invoice's lock
-- (lockInvoice), then touches its letters (D18: a hold, a lift, a hand-off,
-- its withdrawal — the invoice, then its letters, and on a barring lift the
-- waivers inserted).

-- name: InsertHold :one
-- InsertHold marks an invoice disputed; ux_invoice_holds_live is the floor
-- under two holds of one invoice, which its lock already serialises.
INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
VALUES (@invoice_id, 'disputed', @note, @placed_at, @placed_by_user_id)
RETURNING *;

-- name: LiftHold :one
-- LiftHold lifts an invoice's live hold, once, answering whether the
-- objection was groundless (charges_allowed). The trigger blanks the lift
-- note of an anonymised customer's hold.
UPDATE invoices.invoice_holds
SET lifted_at = @lifted_at::timestamptz, lifted_by_user_id = @lifted_by_user_id::uuid, lift_note = @lift_note::text,
    charges_allowed = @charges_allowed::boolean
WHERE invoice_id = @invoice_id AND lifted_at IS NULL
RETURNING *;

-- name: LatestHoldOf :many
-- LatestHoldOf is an invoice's latest hold, live or lifted — the live one
-- when it has one, since a hold is placed only while none is live — for the
-- document; none when it was never held.
SELECT * FROM invoices.invoice_holds WHERE invoice_id = @invoice_id ORDER BY id DESC LIMIT 1;

-- name: InsertHandoff :one
-- InsertHandoff records the hand-off to a collection agency;
-- ux_collection_handoffs_live is the floor under two live ones.
INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, agency_reference, note, created_at, created_by_user_id)
VALUES (@invoice_id, @handed_on, @agency, @agency_reference, @note, @created_at, @created_by_user_id)
RETURNING *;

-- name: WithdrawHandoff :one
-- WithdrawHandoff withdraws an invoice's live hand-off, once.
UPDATE invoices.collection_handoffs
SET withdrawn_on = @withdrawn_on::date, withdrawn_by_user_id = @withdrawn_by_user_id::uuid, withdrawal_reason = @withdrawal_reason::text
WHERE invoice_id = @invoice_id AND withdrawn_on IS NULL
RETURNING *;

-- name: LatestHandoffOf :many
-- LatestHandoffOf is an invoice's latest hand-off, live or withdrawn, for
-- the document; none when it was never handed off.
SELECT * FROM invoices.collection_handoffs WHERE invoice_id = @invoice_id ORDER BY id DESC LIMIT 1;

-- name: WithdrawLettersInFlight :many
-- WithdrawLettersInFlight withdraws an invoice's letters still in flight —
-- queued, awaiting print or failed — with the module's reason and no user
-- (plan reading 44). Never a printed letter: it may be in the post already,
-- and the posting's re-judge decides it (plan reading 9). Never a letter
-- being sent — queued with its facts written under a live lease (plan
-- reading 45): it is mailed outside any lock and becomes sent. A lease
-- that is NULL is no live lease. It answers the letters it withdrew.
UPDATE invoices.reminders
SET status = 'withdrawn', withdrawn_at = @withdrawn_at::timestamptz, withdrawal_reason = @withdrawal_reason::text
WHERE invoice_id = @invoice_id
  AND status IN ('queued', 'awaiting_print', 'failed')
  AND NOT (status = 'queued' AND sent_on IS NOT NULL AND coalesce(lease_until > @now::timestamptz, false))
RETURNING id;

-- name: PrintedLettersOf :many
-- PrintedLettersOf is what a withdrawal leaves of an invoice's letters: the
-- printed ones, left for the posting, and any being sent (the same
-- predicate as WithdrawLettersInFlight's), by sequence.
SELECT * FROM invoices.reminders
WHERE invoice_id = @invoice_id
  AND (status = 'printed'
       OR (status = 'queued' AND sent_on IS NOT NULL AND coalesce(lease_until > @now::timestamptz, false)))
ORDER BY sequence, id;

-- name: ChargesClaimedOf :many
-- ChargesClaimedOf is every fee and compensation an invoice's sent letters
-- claimed and no waiver released yet, by sequence: what a lift that bars
-- charges waives (D11).
SELECT r.id AS reminder_id, c.kind::text AS kind
FROM invoices.reminders r
CROSS JOIN LATERAL (VALUES ('fee', r.fee), ('compensation', r.compensation)) AS c (kind, amount)
WHERE r.invoice_id = @invoice_id AND r.status = 'sent' AND c.amount > 0
  AND NOT EXISTS (SELECT 1 FROM invoices.charge_waivers w WHERE w.reminder_id = r.id AND w.kind = c.kind)
ORDER BY r.sequence, r.id, c.kind DESC;

-- name: CollectionExportRows :many
-- CollectionExportRows is the collection CSV's invoices (D11): the issued
-- invoices with a live hand-off made from handed_from to handed_to, or the
-- issued invoices among ids — the caller passes one selection — each with
-- its buyer snapshot, its live hand-off and whether a hold is live; in
-- number order. The caller asks for one row more than the file may hold.
SELECT i.id, i.number, i.issue_date, i.due_date, i.delivery_date, i.delivery_from, i.delivery_to, i.kid, i.customer_id,
       i.buyer_customer_number, i.buyer_name, i.buyer_type, i.buyer_organisation_number, i.buyer_foreign_id,
       i.buyer_address_line1, i.buyer_address_line2, i.buyer_postal_code, i.buyer_city, i.buyer_country,
       h.handed_on, h.agency, h.agency_reference,
       EXISTS (SELECT 1 FROM invoices.invoice_holds d WHERE d.invoice_id = i.id AND d.lifted_at IS NULL)::boolean AS disputed
FROM invoices.invoices i
LEFT JOIN invoices.collection_handoffs h ON h.invoice_id = i.id AND h.withdrawn_on IS NULL
WHERE i.kind = 'invoice' AND i.status = 'issued'
  AND ((sqlc.narg(handed_from)::date IS NOT NULL
        AND h.handed_on BETWEEN sqlc.narg(handed_from)::date AND sqlc.narg(handed_to)::date)
       OR i.id = ANY(@ids::bigint[]))
ORDER BY i.number, i.id
LIMIT sqlc.arg('limit');

-- name: ExportDeliveries :many
-- ExportDeliveries is every live delivery of the invoices, with its kind as
-- a person reads it — handed_over or posted for a manual record, email,
-- ehf — and its day or instant (RuleDeliveries' definition): the CSV's
-- Delivered column names the first.
SELECT m.invoice_id, m.kind::text AS kind, NULL::timestamptz AS at, m.delivered_on AS delivered_on
FROM invoices.manual_deliveries m
WHERE m.invoice_id = ANY(@ids::bigint[]) AND m.removed_at IS NULL
UNION ALL
SELECT d.invoice_id, 'email'::text, d.sent_at, NULL::date
FROM invoices.deliveries d
WHERE d.invoice_id = ANY(@ids::bigint[])
UNION ALL
SELECT t.invoice_id, 'ehf'::text, t.delivered_at, NULL::date
FROM invoices.transmissions t
WHERE t.invoice_id = ANY(@ids::bigint[]) AND t.status = 'delivered';
