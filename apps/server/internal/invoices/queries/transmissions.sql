-- The EHF transmissions (EHF and KID design D8-D10): the send's insert, the
-- reads the document response and the list make, the invoices-ehf worker's
-- claim and its lease-checked completions, the events worker's idempotent
-- updates, cancel and resolve. Every time here is a parameter (@now) from
-- Deps.Clock(), never SQL now(): the harness clock sits weeks from the
-- database's, and a lease judged by now() would always look expired. The
-- trigger tr_transmissions_immutable lets only the state columns change.
--
-- Every completion of a claim is WHERE id = @id AND lease_id = @lease_id AND
-- status = @status, @status the status the claim saw: a row the events worker
-- moved meanwhile (it takes no lease) answers 0 rows, a no-op, never the
-- trigger's refusal of a change to a final row. A completion that can move an
-- unconfirmed row writes @machine_note as its resolution_note — the machine's
-- resolution, with no user — and leaves the note alone on any other row.

-- name: InsertTransmission :one
-- InsertTransmission queues one transmission (D8), due at once. The trigger
-- refuses a draft's and an anonymised customer's; ux_transmissions_active
-- refuses a second live one for the same document (23505).
INSERT INTO invoices.transmissions (
    invoice_id, provider, idempotency_key, sender_participant, receiver_participant,
    document_type, process_id, ubl_object_key, ubl_sha256, pdf_sha256,
    next_attempt_at, lookup_registered, lookup_can_receive, lookup_at, queued_at, created_by_user_id
) VALUES (
    @invoice_id, @provider, @idempotency_key, @sender_participant, @receiver_participant,
    @document_type, @process_id, @ubl_object_key, @ubl_sha256, @pdf_sha256,
    @now::timestamptz, @lookup_registered, @lookup_can_receive, @lookup_at::timestamptz, @now::timestamptz, @created_by_user_id
)
RETURNING *;

-- name: TransmissionsOf :many
-- TransmissionsOf is every transmission of one document, the newest first.
SELECT * FROM invoices.transmissions WHERE invoice_id = @invoice_id ORDER BY id DESC;

-- name: LatestTransmission :one
-- LatestTransmission is one document's newest transmission: the document's
-- EHF state, and the UBL reuse rule's question (D4). No row is pgx.ErrNoRows.
SELECT * FROM invoices.transmissions WHERE invoice_id = @invoice_id ORDER BY id DESC LIMIT 1;

-- name: LatestPersonResolvedFailedTransmission :one
-- LatestPersonResolvedFailedTransmission is the newest of one document's
-- transmissions that was unconfirmed and that a person resolved as failed,
-- among all of the document's rows: its bytes may have reached the receiver,
-- so every later send carries them (D4's reuse rule) — a reused send that is
-- later cancelled leaves the next send carrying them still. No row is
-- pgx.ErrNoRows: the send renders fresh.
SELECT * FROM invoices.transmissions
WHERE invoice_id = @invoice_id AND status = 'failed' AND resolved_by_user_id IS NOT NULL
ORDER BY id DESC LIMIT 1;

-- name: ActiveTransmissionExists :one
-- ActiveTransmissionExists is whether a live transmission blocks a new send:
-- the predicate of ux_transmissions_active.
SELECT EXISTS (
    SELECT 1 FROM invoices.transmissions
    WHERE invoice_id = @invoice_id AND status IN ('queued', 'submitted', 'delivered', 'unconfirmed')
) AS active;

-- name: ClaimTransmission :one
-- ClaimTransmission leases the next due row (D9): in flight, or delivered with
-- a reference and no evidence yet — ix_transmissions_due's predicate — due by
-- @now, and not leased by a worker whose lease still runs. One conditional
-- UPDATE over a SKIP LOCKED pick, so two workers never take one row. No row
-- is pgx.ErrNoRows: nothing is due. @skip_queued leaves every queued row
-- alone, unleased, short of the 48-hour age cap: an installation whose
-- Peppol lookup is disabled still probes and stores evidence and still caps
-- a queued row by its age (a cap makes no call), but never submits (D9).
UPDATE invoices.transmissions SET lease_id = @lease_id, lease_until = @lease_until::timestamptz
WHERE id = (
    SELECT t.id FROM invoices.transmissions t
    WHERE ((t.status = 'queued' AND (NOT @skip_queued::boolean OR t.queued_at + interval '48 hours' < @now::timestamptz))
           OR t.status IN ('submitted', 'unconfirmed')
           OR (t.status = 'delivered' AND t.evidence_object_key IS NULL AND t.provider_ref IS NOT NULL))
      AND t.next_attempt_at <= @now::timestamptz
      AND (t.lease_until IS NULL OR t.lease_until < @now::timestamptz)
    ORDER BY t.next_attempt_at, t.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: GetTransmission :one
-- GetTransmission re-reads one row by id.
SELECT * FROM invoices.transmissions WHERE id = @id;

-- name: MarkSubmitAttempted :execrows
-- MarkSubmitAttempted stamps the crash marker immediately before the provider
-- is called, auto-committed, under the claim's lease (D9).
UPDATE invoices.transmissions SET submit_attempted_at = @now::timestamptz
WHERE id = @id AND lease_id = @lease_id AND status = 'queued';

-- name: RestoreSubmitAttempted :execrows
-- RestoreSubmitAttempted puts the crash marker back to its pre-claim value
-- (NULL or the earlier stamp) when the outcome proves the provider did not
-- take the document: a 401, a 403, a 429 or a refusal before any call.
UPDATE invoices.transmissions SET submit_attempted_at = sqlc.narg(previous)::timestamptz
WHERE id = @id AND lease_id = @lease_id AND status = 'queued';

-- name: MarkSubmitted :execrows
-- MarkSubmitted records the provider's acceptance with its reference, the
-- first probe due at @next_attempt_at, and releases the lease.
UPDATE invoices.transmissions SET
    status = 'submitted', provider_ref = @provider_ref, submitted_at = @now::timestamptz,
    next_attempt_at = @next_attempt_at::timestamptz, poll_attempts = 0, last_error = NULL,
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND status = 'queued' AND lease_id = @lease_id;

-- name: MarkSubmittedWithoutRef :execrows
-- MarkSubmittedWithoutRef is the 422 on a retry (D9): the submission may have
-- gone through, so the row waits for the queue drain to resolve it by its
-- idempotency key, rescheduled at @next_attempt_at and never probed.
UPDATE invoices.transmissions SET
    status = 'submitted', submitted_at = @now::timestamptz, next_attempt_at = @next_attempt_at::timestamptz,
    last_error = sqlc.narg(last_error), lease_id = NULL, lease_until = NULL
WHERE id = @id AND status = 'queued' AND lease_id = @lease_id;

-- name: MarkFailedLeased :execrows
-- MarkFailedLeased ends a claimed row (queued, submitted or unconfirmed) as
-- failed with its (redacted) reason.
UPDATE invoices.transmissions SET
    status = 'failed', failed_at = @now::timestamptz, last_error = @last_error,
    resolution_note = CASE WHEN status = 'unconfirmed' THEN sqlc.narg(machine_note) ELSE resolution_note END,
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text;

-- name: MarkUnconfirmedLeased :execrows
-- MarkUnconfirmedLeased hands a claimed row to a person (D9): the cap by age
-- on a queued row whose marker is set, or seven days submitted. The worker
-- keeps probing it at @next_attempt_at, or never when @park ('infinity':
-- the row waits for a person).
UPDATE invoices.transmissions SET
    status = 'unconfirmed',
    next_attempt_at = CASE WHEN @park::boolean THEN 'infinity'::timestamptz ELSE @next_attempt_at::timestamptz END,
    last_error = sqlc.narg(last_error),
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text;

-- name: RescheduleLeased :execrows
-- RescheduleLeased puts a claimed row back for later: the backoff after a
-- transport failure (submit_attempts + 1), the probe cadence (poll_attempts
-- + 1), or an hour out after a refused key (neither counted). @park puts it
-- at 'infinity' instead: an unconfirmed row the machine stops probing.
UPDATE invoices.transmissions SET
    next_attempt_at = CASE WHEN @park::boolean THEN 'infinity'::timestamptz ELSE @next_attempt_at::timestamptz END,
    submit_attempts = submit_attempts + @submit_attempts_delta::integer,
    poll_attempts = poll_attempts + @poll_attempts_delta::integer,
    last_error = sqlc.narg(last_error),
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text;

-- name: RefreshTransmissionLookup :execrows
-- RefreshTransmissionLookup records a queued row's fresh receiver lookup,
-- made in its claim when the old one is older than a day (D9).
UPDATE invoices.transmissions SET
    lookup_registered = @lookup_registered, lookup_can_receive = @lookup_can_receive, lookup_at = @now::timestamptz
WHERE id = @id AND lease_id = @lease_id AND status = 'queued';

-- name: MarkDeliveredLeased :execrows
-- MarkDeliveredLeased records delivery a probe found (the evidence answered)
-- on a submitted or unconfirmed row, with the evidence due at once in the
-- next claim. resolved_by_user_id stays NULL: the machine resolved it.
UPDATE invoices.transmissions SET
    status = 'delivered', delivered_at = @now::timestamptz, next_attempt_at = @now::timestamptz,
    resolution_note = CASE WHEN status = 'unconfirmed' THEN sqlc.narg(machine_note) ELSE resolution_note END,
    poll_attempts = 0, last_error = NULL, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = @status::text;

-- name: SetEvidenceLeased :execrows
-- SetEvidenceLeased stores where a delivered row's evidence is, once; from
-- then on the row is final.
UPDATE invoices.transmissions SET
    evidence_object_key = @evidence_object_key, evidence_sha256 = @evidence_sha256,
    last_error = NULL, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id AND status = 'delivered' AND evidence_object_key IS NULL;

-- name: ApplyEventDelivered :execrows
-- ApplyEventDelivered applies a "succeeded" event from the provider's queue
-- (D9), idempotently and without a lease: the row is matched by its provider
-- reference, or by its idempotency key when it never learned the reference,
-- which it then takes. 0 rows is an event for a row already final, or not
-- ours. An empty reference never matches and is never stored.
UPDATE invoices.transmissions SET
    status = 'delivered', delivered_at = @now::timestamptz, next_attempt_at = @now::timestamptz,
    provider_ref = COALESCE(provider_ref, NULLIF(sqlc.narg(provider_ref)::text, '')),
    resolution_note = CASE WHEN status = 'unconfirmed' THEN sqlc.narg(machine_note) ELSE resolution_note END,
    poll_attempts = 0, last_error = NULL
WHERE (idempotency_key = @idempotency_key
       OR (provider_ref = sqlc.narg(provider_ref)::text AND provider_ref IS NOT NULL AND sqlc.narg(provider_ref)::text <> ''))
  AND status IN ('queued', 'submitted', 'unconfirmed');

-- name: ApplyEventFailed :execrows
-- ApplyEventFailed applies a "failed" or "no_action_taken" event the same way.
UPDATE invoices.transmissions SET
    status = 'failed', failed_at = @now::timestamptz, last_error = @last_error,
    provider_ref = COALESCE(provider_ref, NULLIF(sqlc.narg(provider_ref)::text, '')),
    resolution_note = CASE WHEN status = 'unconfirmed' THEN sqlc.narg(machine_note) ELSE resolution_note END
WHERE (idempotency_key = @idempotency_key
       OR (provider_ref = sqlc.narg(provider_ref)::text AND provider_ref IS NOT NULL AND sqlc.narg(provider_ref)::text <> ''))
  AND status IN ('queued', 'submitted', 'unconfirmed');

-- name: AnyAwaitingEvents :one
-- AnyAwaitingEvents is whether the events worker has anything to drain for:
-- a submitted or unconfirmed row, or a queued one whose marker is set.
SELECT EXISTS (
    SELECT 1 FROM invoices.transmissions
    WHERE status IN ('submitted', 'unconfirmed') OR (status = 'queued' AND submit_attempted_at IS NOT NULL)
) AS awaiting;

-- name: QueuedTransmissionsDue :one
-- QueuedTransmissionsDue is how many queued rows are due by @now: what an
-- installation whose Peppol lookup is disabled leaves alone, said once per
-- cycle.
SELECT count(*) FROM invoices.transmissions WHERE status = 'queued' AND next_attempt_at <= @now::timestamptz;

-- name: CancelUnattemptedTransmission :execrows
-- CancelUnattemptedTransmission cancels a queued row that was never attempted
-- and is not leased (D9). 0 rows is 409 transmission_not_cancellable.
UPDATE invoices.transmissions SET status = 'cancelled', cancelled_at = @now::timestamptz
WHERE id = @id AND invoice_id = @invoice_id AND status = 'queued' AND submit_attempted_at IS NULL
  AND (lease_until IS NULL OR lease_until < @now::timestamptz);

-- name: ResolveTransmission :execrows
-- ResolveTransmission is a person's verdict on an unconfirmed row (D9):
-- delivered or failed, who and why. 0 rows is 409
-- transmission_not_resolvable.
UPDATE invoices.transmissions SET
    status = @outcome::text,
    delivered_at = CASE WHEN @outcome::text = 'delivered' THEN @now::timestamptz ELSE delivered_at END,
    failed_at = CASE WHEN @outcome::text = 'failed' THEN @now::timestamptz ELSE failed_at END,
    next_attempt_at = @now::timestamptz,
    resolved_by_user_id = @resolved_by_user_id, resolution_note = @resolution_note,
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND invoice_id = @invoice_id AND status = 'unconfirmed'
  AND @outcome::text IN ('delivered', 'failed');

-- name: ActiveTransmissionsCount :one
-- ActiveTransmissionsCount is how many transmissions the credentials still
-- serve (D7): a DELETE or a provider switch is refused while any is queued,
-- submitted or unconfirmed.
SELECT count(*) FROM invoices.transmissions WHERE status IN ('queued', 'submitted', 'unconfirmed');

-- name: CancelCustomerUnattemptedTransmissions :execrows
-- CancelCustomerUnattemptedTransmissions cancels, at an erase, every queued
-- transmission of the customer's documents that was never attempted, leased
-- or not (D12): a worker holding one stamps its marker only on a row still
-- queued (MarkSubmitAttempted), so it finds the row cancelled and never
-- posts it.
UPDATE invoices.transmissions SET status = 'cancelled', cancelled_at = @now::timestamptz
WHERE invoice_id IN (SELECT i.id FROM invoices.invoices i WHERE i.customer_id = @customer_id)
  AND status = 'queued' AND submit_attempted_at IS NULL;

-- name: TransmissionsOfDocuments :many
-- TransmissionsOfDocuments is the newest transmission of each of a page of
-- documents: the list's ehfStatus (D10).
SELECT DISTINCT ON (invoice_id) *
FROM invoices.transmissions
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, id DESC;

-- name: EveryTransmissionOfDocuments :many
-- EveryTransmissionOfDocuments is every transmission of a set of documents,
-- each document's oldest first: a person's export (D12).
SELECT * FROM invoices.transmissions
WHERE invoice_id = ANY(@invoice_ids::bigint[])
ORDER BY invoice_id, id;
