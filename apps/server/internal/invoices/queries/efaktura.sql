-- eFaktura and AvtaleGiro (invoices phase 6 design I29-I35, delivery D): the
-- consumer provider's settings row, the consumer lookups, the mandates, the
-- eFaktura transmissions and the AvtaleGiro claims. Every time here is a
-- parameter (@now) from Deps.Clock(), never SQL now(), as transmissions.sql's.

-- name: GetConsumerProviderCredentials :one
-- GetConsumerProviderCredentials reads the one settings row; no row is
-- pgx.ErrNoRows.
SELECT * FROM invoices.consumer_provider_credentials WHERE id = 1;

-- name: LockConsumerProviderCredentials :one
-- LockConsumerProviderCredentials reads the row FOR UPDATE: a PUT that keeps
-- the stored mandate certificate opens and seals it again from this read.
SELECT * FROM invoices.consumer_provider_credentials WHERE id = 1 FOR UPDATE;

-- name: LockConsumerProviderCredentialsForShare :one
-- LockConsumerProviderCredentialsForShare is the send's hold on the row, so a
-- DELETE never leaves a queued eFaktura without its settings.
SELECT * FROM invoices.consumer_provider_credentials WHERE id = 1 FOR SHARE;

-- name: UpsertConsumerProviderCredentials :one
-- UpsertConsumerProviderCredentials writes the row and clears rejected_at.
INSERT INTO invoices.consumer_provider_credentials (id, provider, settings_json, secret_ciphertext, rejected_at, updated_at)
VALUES (1, @provider, @settings_json, @secret_ciphertext, NULL, @now::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    provider = EXCLUDED.provider,
    settings_json = EXCLUDED.settings_json,
    secret_ciphertext = EXCLUDED.secret_ciphertext,
    rejected_at = NULL,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteConsumerProviderCredentials :execrows
DELETE FROM invoices.consumer_provider_credentials WHERE id = 1;

-- name: MarkConsumerProviderRejected :exec
-- MarkConsumerProviderRejected records the provider refusing the submitter,
-- keeping the first moment it did.
UPDATE invoices.consumer_provider_credentials SET rejected_at = @now::timestamptz
WHERE id = 1 AND rejected_at IS NULL;

-- name: ClearConsumerProviderRejected :exec
UPDATE invoices.consumer_provider_credentials SET rejected_at = NULL
WHERE id = 1 AND rejected_at IS NOT NULL;

-- name: EfakturaInFlight :one
-- EfakturaInFlight is how much the settings still serve: eFaktura
-- transmissions queued, submitted or unconfirmed, and claims submitted or
-- asked to cancel, or accepted and not yet due by @today. A DELETE is refused
-- while any is.
SELECT
    (SELECT count(*) FROM invoices.transmissions
     WHERE channel = 'efaktura' AND status IN ('queued', 'submitted', 'unconfirmed'))
  + (SELECT count(*) FROM invoices.avtalegiro_claims
     WHERE status IN ('submitted', 'cancel_requested')
        OR (status = 'accepted' AND due_date >= @today::date)) AS in_flight;

-- name: EfakturaSentPerMonth :many
-- EfakturaSentPerMonth is the eFaktura transmissions queued per calendar
-- month in @zone since @since (I35), cancelled ones left out: what the
-- provider charges for.
SELECT to_char(queued_at AT TIME ZONE @zone::text, 'YYYY-MM')::text AS month, count(*) AS sent
FROM invoices.transmissions
WHERE channel = 'efaktura' AND status <> 'cancelled' AND queued_at >= @since::timestamptz
GROUP BY 1
ORDER BY 1 DESC;

-- name: SetAvtaleGiroEnabled :exec
-- Turning AvtaleGiro on reserves the customer KIDs' space for good.
UPDATE invoices.settings SET avtalegiro_enabled = @enabled, kid_space_reserved = kid_space_reserved OR @enabled
WHERE id = 1;

-- name: AnyIssuedKidInCustomerSpace :one
-- AnyIssuedKidInCustomerSpace is whether an issued invoice's KID already
-- begins with 9, the customer KIDs' space (reading 12).
SELECT EXISTS (
    SELECT 1 FROM invoices.invoices
    WHERE kind = 'invoice' AND status = 'issued' AND kid LIKE '9%'
) AS taken;

-- The consumer lookups (I30).

-- name: GetConsumerLookup :one
SELECT * FROM invoices.consumer_lookups WHERE customer_id = @customer_id;

-- name: UpsertConsumerLookup :one
-- UpsertConsumerLookup stores one answer, due again at @next_check_at.
INSERT INTO invoices.consumer_lookups (customer_id, status, efaktura_identifier, auto_accept, has_agreement, checked_at, next_check_at)
VALUES (@customer_id, @status, sqlc.narg(efaktura_identifier), @auto_accept, @has_agreement, @now::timestamptz, @next_check_at::timestamptz)
ON CONFLICT (customer_id) DO UPDATE SET
    status = EXCLUDED.status,
    efaktura_identifier = EXCLUDED.efaktura_identifier,
    auto_accept = EXCLUDED.auto_accept,
    has_agreement = EXCLUDED.has_agreement,
    checked_at = EXCLUDED.checked_at,
    next_check_at = EXCLUDED.next_check_at
RETURNING *;

-- name: ConsumerLookupsDue :many
-- ConsumerLookupsDue is the customers whose lookup is due by @now, the
-- oldest first, at most @max: the worker's weekly refresh.
SELECT customer_id FROM invoices.consumer_lookups
WHERE next_check_at <= @now::timestamptz
ORDER BY next_check_at, customer_id
LIMIT @max::integer;

-- name: DeleteConsumerLookup :execrows
DELETE FROM invoices.consumer_lookups WHERE customer_id = @customer_id;

-- name: MarkConsumerLookupDue :exec
-- MarkConsumerLookupDue makes a customer's lookup due now: a merge moved
-- another customer's identity onto it.
UPDATE invoices.consumer_lookups SET next_check_at = @now::timestamptz WHERE customer_id = @customer_id;

-- The mandates (I32, reading 10).

-- name: UpsertMandate :one
-- UpsertMandate records a mandate the queue delivered, by its KID. A new one
-- takes @customer_id (NULL when the KID names no customer known); an existing
-- one keeps its customer, which a merge or a person may have set, and is
-- changed only by a later delivery (@queue_position higher than the one it
-- holds). No row is an older delivery, ignored.
INSERT INTO invoices.avtalegiro_mandates (kid, customer_id, status, notify_by_mail, provider_seen_at, queue_position, updated_at)
VALUES (@kid, sqlc.narg(customer_id), @status, @notify_by_mail, @now::timestamptz, @queue_position, @now::timestamptz)
ON CONFLICT (kid) DO UPDATE SET
    customer_id = COALESCE(invoices.avtalegiro_mandates.customer_id, EXCLUDED.customer_id),
    status = EXCLUDED.status,
    notify_by_mail = EXCLUDED.notify_by_mail,
    provider_seen_at = EXCLUDED.provider_seen_at,
    queue_position = EXCLUDED.queue_position,
    updated_at = EXCLUDED.updated_at
WHERE invoices.avtalegiro_mandates.queue_position < EXCLUDED.queue_position
RETURNING *;

-- name: GetMandate :one
SELECT * FROM invoices.avtalegiro_mandates WHERE id = @id;

-- name: MandateByKID :one
SELECT * FROM invoices.avtalegiro_mandates WHERE kid = @kid;

-- name: ActiveMandateOfCustomer :one
-- ActiveMandateOfCustomer is the customer's newest active mandate.
SELECT * FROM invoices.avtalegiro_mandates
WHERE customer_id = @customer_id AND status = 'active'
ORDER BY id DESC LIMIT 1;

-- name: ListMandates :many
SELECT * FROM invoices.avtalegiro_mandates
WHERE (NOT @unresolved::boolean OR customer_id IS NULL)
ORDER BY id DESC
LIMIT 500;

-- name: ResolveMandate :one
-- ResolveMandate assigns an unresolved mandate; no row when it names a
-- customer already.
UPDATE invoices.avtalegiro_mandates
SET customer_id = @customer_id, resolved_by_user_id = @resolved_by_user_id,
    resolution_note = sqlc.narg(resolution_note), updated_at = @now::timestamptz
WHERE id = @id AND customer_id IS NULL
RETURNING *;

-- name: UnassignMandate :one
-- UnassignMandate takes a mandate's customer away again, for a person to
-- assign it right; no row when it names none.
UPDATE invoices.avtalegiro_mandates
SET customer_id = NULL, resolved_by_user_id = NULL, resolution_note = NULL, updated_at = @now::timestamptz
WHERE id = @id AND customer_id IS NOT NULL
RETURNING *;

-- name: MandateHasLiveClaim :one
SELECT EXISTS (
    SELECT 1 FROM invoices.avtalegiro_claims
    WHERE mandate_id = @mandate_id AND status IN ('submitted', 'accepted', 'cancel_requested')
) AS live;

-- name: RepointMandates :execrows
UPDATE invoices.avtalegiro_mandates SET customer_id = @into_id, updated_at = @now::timestamptz
WHERE customer_id = @from_id;

-- name: MandatesOfCustomer :many
SELECT * FROM invoices.avtalegiro_mandates WHERE customer_id = @customer_id ORDER BY id;

-- name: CustomerOfNumber :one
-- CustomerOfNumber resolves a customer number to a customer through this
-- module's own snapshots (reading 10): the newest issued document whose
-- buyer carried that number.
SELECT customer_id FROM invoices.invoices
WHERE status = 'issued' AND buyer_customer_number = @customer_number
ORDER BY id DESC LIMIT 1;

-- The eFaktura transmissions (I31).

-- name: InsertEfakturaTransmission :one
-- InsertEfakturaTransmission queues one eFaktura, due at once. The trigger
-- refuses a draft's and an anonymised customer's; ux_transmissions_active a
-- second live one on the channel.
INSERT INTO invoices.transmissions (
    invoice_id, channel, provider, idempotency_key, pdf_sha256, pdf_object_key, efaktura_identifier,
    shipment_id, payment_type, next_attempt_at, queued_at, created_by_user_id
) VALUES (
    @invoice_id, 'efaktura', 'mps', @idempotency_key, @pdf_sha256, @pdf_object_key::text, @efaktura_identifier::text,
    @shipment_id::uuid, @payment_type::text, @now::timestamptz, @now::timestamptz, @created_by_user_id
)
RETURNING *;

-- name: ActiveEfakturaTransmissionExists :one
SELECT EXISTS (
    SELECT 1 FROM invoices.transmissions
    WHERE invoice_id = @invoice_id AND channel = 'efaktura' AND status IN ('queued', 'submitted', 'delivered', 'unconfirmed')
) AS active;

-- name: ClaimEfakturaTransmission :one
-- ClaimEfakturaTransmission leases the next due eFaktura row: in flight, or
-- delivered without its evidence (reading 3) — one conditional UPDATE over a
-- SKIP LOCKED pick, as ClaimTransmission.
UPDATE invoices.transmissions SET lease_id = @lease_id, lease_until = @lease_until::timestamptz
WHERE id = (
    SELECT t.id FROM invoices.transmissions t
    WHERE t.channel = 'efaktura'
      AND (t.status IN ('queued', 'submitted', 'unconfirmed')
           OR (t.status = 'delivered' AND t.evidence_object_key IS NULL AND t.provider_ref IS NOT NULL))
      AND t.next_attempt_at <= @now::timestamptz
      AND (t.lease_until IS NULL OR t.lease_until < @now::timestamptz)
    ORDER BY t.next_attempt_at, t.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkEfakturaSubmitted :execrows
UPDATE invoices.transmissions SET
    status = 'submitted', provider_ref = @provider_ref, submitted_at = @now::timestamptz,
    next_attempt_at = @next_attempt_at::timestamptz, poll_attempts = 0, last_error = NULL,
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND status = 'queued' AND lease_id = @lease_id;

-- name: MarkEfakturaDeliveredLeased :execrows
-- MarkEfakturaDeliveredLeased records the eFaktura in the consumer's bank,
-- with the network's state; the evidence is stored when the claim, if any,
-- has settled, in this update (@evidence_object_key set) or a later one.
UPDATE invoices.transmissions SET
    status = 'delivered', delivered_at = @now::timestamptz, next_attempt_at = @next_attempt_at::timestamptz,
    provider_status = @provider_status,
    evidence_object_key = sqlc.narg(evidence_object_key), evidence_sha256 = sqlc.narg(evidence_sha256),
    resolution_note = CASE WHEN status = 'unconfirmed' THEN sqlc.narg(machine_note) ELSE resolution_note END,
    poll_attempts = 0, last_error = NULL, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text;

-- name: SetProviderStatusLeased :execrows
-- SetProviderStatusLeased records the network's state on a row short of
-- delivered, under the lease, and reschedules it.
UPDATE invoices.transmissions SET
    provider_status = @provider_status, next_attempt_at = @next_attempt_at::timestamptz,
    poll_attempts = poll_attempts + 1, last_error = NULL, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text AND status <> 'delivered';

-- name: MarkEfakturaFailedLeased :execrows
UPDATE invoices.transmissions SET
    status = 'failed', failed_at = @now::timestamptz, last_error = @last_error, provider_status = @provider_status,
    resolution_note = CASE WHEN status = 'unconfirmed' THEN sqlc.narg(machine_note) ELSE resolution_note END,
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text;

-- name: EfakturaTransmissionsOf :many
SELECT * FROM invoices.transmissions WHERE invoice_id = @invoice_id AND channel = 'efaktura' ORDER BY id DESC;

-- The AvtaleGiro claims (I33, I34).

-- name: InsertClaim :one
INSERT INTO invoices.avtalegiro_claims (invoice_id, transmission_id, mandate_id, amount, due_date, kid, submitted_at)
VALUES (@invoice_id, @transmission_id, @mandate_id, @amount, @due_date, @kid, @now::timestamptz)
RETURNING *;

-- name: ClaimOfTransmission :one
SELECT * FROM invoices.avtalegiro_claims WHERE transmission_id = @transmission_id ORDER BY id DESC LIMIT 1;

-- name: ClaimsOfInvoice :many
SELECT * FROM invoices.avtalegiro_claims WHERE invoice_id = @invoice_id ORDER BY id DESC;

-- name: AcceptClaim :execrows
UPDATE invoices.avtalegiro_claims SET status = 'accepted', accepted_at = @now::timestamptz, attention = @attention
WHERE id = @id AND status = 'submitted';

-- name: SetClaimAttention :execrows
UPDATE invoices.avtalegiro_claims SET attention = true
WHERE id = @id AND status IN ('accepted', 'cancel_requested') AND NOT attention;

-- name: RejectClaim :execrows
-- RejectClaim ends a claim the bank or the payer refused, from any live state.
UPDATE invoices.avtalegiro_claims SET
    status = 'rejected', rejected_at = @now::timestamptz, attention = @attention,
    rejection_code = sqlc.narg(rejection_code), rejection_text = sqlc.narg(rejection_text),
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND status IN ('submitted', 'accepted', 'cancel_requested');

-- name: CancelClaim :execrows
UPDATE invoices.avtalegiro_claims SET status = 'cancelled', cancelled_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND status IN ('submitted', 'accepted', 'cancel_requested');

-- name: RequestClaimCancel :many
-- RequestClaimCancel moves an invoice's live claim to cancel_requested
-- (reading 15), inside the credit note's issue; the worker carries it to
-- the provider. A claim whose eFaktura was never handed over — queued, never
-- attempted — is cancelled here instead: the bank never got it. A paid claim
-- is left as it is.
UPDATE invoices.avtalegiro_claims c SET
    status = CASE WHEN t.status = 'queued' AND t.submit_attempted_at IS NULL THEN 'cancelled' ELSE 'cancel_requested' END,
    cancelled_at = CASE WHEN t.status = 'queued' AND t.submit_attempted_at IS NULL THEN @now::timestamptz ELSE c.cancelled_at END,
    cancel_requested_at = @now::timestamptz, next_attempt_at = @now::timestamptz
FROM invoices.transmissions t
WHERE t.id = c.transmission_id AND c.invoice_id = @invoice_id AND c.status IN ('submitted', 'accepted')
RETURNING c.*;

-- name: ClaimCancelDue :one
-- ClaimCancelDue leases the next claim asked to cancel, with what the call
-- needs of its transmission: the shipment and the transaction.
UPDATE invoices.avtalegiro_claims c SET lease_id = @lease_id, lease_until = @lease_until::timestamptz
FROM invoices.transmissions t
WHERE c.id = (
    SELECT x.id FROM invoices.avtalegiro_claims x
    WHERE x.status = 'cancel_requested' AND x.next_attempt_at <= @now::timestamptz
      AND (x.lease_until IS NULL OR x.lease_until < @now::timestamptz)
    ORDER BY x.next_attempt_at, x.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
) AND t.id = c.transmission_id
RETURNING c.id, c.invoice_id, c.due_date, c.status, c.accepted_at, t.provider_ref, t.idempotency_key, t.shipment_id,
    t.status AS transmission_status, t.submit_attempted_at;

-- name: RefuseClaimCancel :execrows
-- RefuseClaimCancel puts a claim the provider would not cancel back where it
-- was, saying so — accepted when the bank had accepted it, else submitted,
-- for the poll to judge: the payment comes, and its surplus is credit.
UPDATE invoices.avtalegiro_claims SET
    status = CASE WHEN accepted_at IS NULL THEN 'submitted' ELSE 'accepted' END, cancel_refused_at = @now::timestamptz,
    next_attempt_at = NULL, lease_id = NULL, lease_until = NULL
WHERE id = @id AND status = 'cancel_requested' AND lease_id = @lease_id;

-- name: RescheduleClaimCancel :execrows
UPDATE invoices.avtalegiro_claims SET next_attempt_at = @next_attempt_at::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND status = 'cancel_requested' AND lease_id = @lease_id;

-- name: CancelRequestedClaim :execrows
-- CancelRequestedClaim records the provider taking the cancellation.
UPDATE invoices.avtalegiro_claims SET status = 'cancelled', cancelled_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND status = 'cancel_requested' AND lease_id = @lease_id;

-- name: PayClaim :execrows
-- PayClaim marks an accepted claim paid, in the match's transaction.
UPDATE invoices.avtalegiro_claims SET status = 'paid', paid_at = @now::timestamptz
WHERE id = @id AND status IN ('accepted', 'cancel_requested', 'unpaid');

-- name: ClaimsForMatch :many
-- ClaimsForMatch is the mandate rule's candidates (I32): the accepted claims
-- of the customer whose mandate carries @kid, with this amount, due on one
-- of @due_dates (the booking day, or the days a weekend or holiday moved the
-- debit from).
SELECT c.* FROM invoices.avtalegiro_claims c
JOIN invoices.avtalegiro_mandates m ON m.id = c.mandate_id
JOIN invoices.avtalegiro_mandates k ON k.kid = @kid AND k.customer_id = m.customer_id
WHERE c.status IN ('accepted', 'cancel_requested', 'unpaid') AND c.amount = @amount
  AND c.due_date = ANY(@due_dates::date[])
ORDER BY c.due_date DESC, c.id;

-- name: CancelCustomerUnsubmittedClaims :execrows
-- CancelCustomerUnsubmittedClaims cancels, at an erase, the claims whose
-- transmission the erase cancelled before it was attempted.
UPDATE invoices.avtalegiro_claims c SET status = 'cancelled', cancelled_at = @now::timestamptz
FROM invoices.transmissions t
WHERE t.id = c.transmission_id AND t.status = 'cancelled' AND c.status = 'submitted'
  AND c.invoice_id IN (SELECT i.id FROM invoices.invoices i WHERE i.customer_id = @customer_id);

-- name: ClaimsOfCustomer :many
SELECT c.* FROM invoices.avtalegiro_claims c
JOIN invoices.invoices i ON i.id = c.invoice_id
WHERE i.customer_id = @customer_id
ORDER BY c.id;

-- name: RuleAvtaleGiroClaims :many
-- RuleAvtaleGiroClaims is the accepted claim's due date of each invoice the
-- reminder engine judges (reading 13).
SELECT invoice_id, due_date FROM invoices.avtalegiro_claims
WHERE invoice_id = ANY(@ids::bigint[]) AND status = 'accepted'
ORDER BY invoice_id, id;

-- name: ClaimEfakturaHousekeeping :one
-- ClaimEfakturaHousekeeping leases the daily housekeeping — the mandate
-- queue drained and the due lookups refreshed — when a day has passed since
-- the last and no other worker holds it. No row is nothing to do.
UPDATE invoices.consumer_provider_credentials
SET housekeeping_lease_until = @lease_until::timestamptz
WHERE id = 1
  AND (housekeeping_at IS NULL OR housekeeping_at <= @due_before::timestamptz)
  AND (housekeeping_lease_until IS NULL OR housekeeping_lease_until < @now::timestamptz)
RETURNING id;

-- name: FinishEfakturaHousekeeping :exec
UPDATE invoices.consumer_provider_credentials
SET housekeeping_at = @now::timestamptz, housekeeping_lease_until = NULL
WHERE id = 1;

-- name: ReleaseEfakturaHousekeeping :exec
UPDATE invoices.consumer_provider_credentials SET housekeeping_lease_until = NULL WHERE id = 1;

-- name: PayClaimOfInvoice :many
-- PayClaimOfInvoice marks the invoice's live claim on @kid paid: an
-- AvtaleGiro debit registered against it — matched, applied or kept as
-- credit — in that transaction.
UPDATE invoices.avtalegiro_claims SET status = 'paid', paid_at = @now::timestamptz
WHERE invoice_id = @invoice_id AND kid = @kid AND status IN ('accepted', 'cancel_requested', 'unpaid')
RETURNING *;

-- name: HasPayableClaim :one
-- HasPayableClaim reports whether the invoice has a claim on @kid that a
-- debit would pay (PayClaimOfInvoice's).
SELECT EXISTS (SELECT 1 FROM invoices.avtalegiro_claims
               WHERE invoice_id = @invoice_id AND kid = @kid AND status IN ('accepted', 'cancel_requested', 'unpaid'));

-- name: RequestSettledClaimsCancel :many
-- RequestSettledClaimsCancel asks every live claim to cancel whose invoice
-- has no principal open any more — paid by hand, with credit, by a split, a
-- queue apply or a rounding, or written off: gross less what is credited
-- less settled_amount (B-1's moved_amount in it) is 0 or less. A claim the
-- provider refused to cancel once (cancel_refused_at) is past its deadline
-- and stays; a claim whose eFaktura was never handed over is cancelled here,
-- as RequestClaimCancel does.
UPDATE invoices.avtalegiro_claims c SET
    status = CASE WHEN t.status = 'queued' AND t.submit_attempted_at IS NULL THEN 'cancelled' ELSE 'cancel_requested' END,
    cancelled_at = CASE WHEN t.status = 'queued' AND t.submit_attempted_at IS NULL THEN @now::timestamptz ELSE c.cancelled_at END,
    cancel_requested_at = @now::timestamptz, next_attempt_at = @now::timestamptz
FROM invoices.transmissions t, invoices.invoices i
WHERE t.id = c.transmission_id AND i.id = c.invoice_id
  AND c.status IN ('submitted', 'accepted') AND c.cancel_refused_at IS NULL
  AND i.gross_total
      - coalesce((SELECT sum(x.gross_total) FROM invoices.invoices x
                  WHERE x.credits_invoice_id = i.id AND x.kind = 'credit_note' AND x.status = 'issued'), 0)
      - invoices.settled_amount(i.id) <= 0
RETURNING c.*;

-- name: AcceptedClaimsDueBefore :many
-- AcceptedClaimsDueBefore is the accepted claims due before @before: the
-- candidates for unpaid, which the worker judges by business days.
SELECT * FROM invoices.avtalegiro_claims
WHERE status = 'accepted' AND due_date < @before::date
ORDER BY id
LIMIT 500;

-- name: MarkClaimUnpaid :execrows
-- MarkClaimUnpaid ends an accepted claim no debit paid within five business
-- days of its due date: the ordinary reminders take over.
UPDATE invoices.avtalegiro_claims SET status = 'unpaid', unpaid_at = @now::timestamptz
WHERE id = @id AND status = 'accepted';

-- name: SetMandateRequestID :exec
-- SetMandateRequestID records the X-Request-ID of the page about to be asked
-- for, before the call; ClearMandateRequestID forgets it once the page is
-- stored.
UPDATE invoices.consumer_provider_credentials SET mandate_request_id = @request_id WHERE id = 1;

-- name: ClearMandateRequestID :exec
UPDATE invoices.consumer_provider_credentials SET mandate_request_id = NULL WHERE id = 1;
