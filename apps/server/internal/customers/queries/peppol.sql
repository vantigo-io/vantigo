-- name: GetCustomerPeppolLookup :one
-- GetCustomerPeppolLookup is the stored answer GET/PUT .../billing-profile
-- read to decide whether to include peppolLookup (can-this-customer-
-- receive-EHF design D3): unlocked, since these read paths never write it.
-- pgx.ErrNoRows means "never checked", not an error.
SELECT customer_id, participant_id, status, can_receive_invoice, can_receive_credit_note, smp_host, checked_at
FROM customers.customer_peppol_lookups
WHERE customer_id = @customer_id;

-- name: GetCustomerPeppolLookupForUpdate :one
-- GetCustomerPeppolLookupForUpdate is POST .../peppol-lookup's own read,
-- inside the transaction that also upserts the row (design D3): the FOR
-- UPDATE lock, the same shape GetContactForUpdate gives contacts.go,
-- serializes two concurrent lookups for the same customer so neither writes
-- its event from a before-state the other has already overtaken.
-- pgx.ErrNoRows means "first lookup ever", not an error — the handler
-- treats it as "everything changed".
SELECT customer_id, participant_id, status, can_receive_invoice, can_receive_credit_note, smp_host, checked_at
FROM customers.customer_peppol_lookups
WHERE customer_id = @customer_id
FOR UPDATE;

-- name: UpsertCustomerPeppolLookup :exec
-- UpsertCustomerPeppolLookup writes POST .../peppol-lookup's answer
-- (design D3): one row per customer, so a re-check always overwrites the
-- last one rather than growing a history. Never touches
-- customers.customers, so a lookup can never bump the customer row's own
-- revision.
INSERT INTO customers.customer_peppol_lookups
    (customer_id, participant_id, status, can_receive_invoice, can_receive_credit_note, smp_host, checked_at)
VALUES (@customer_id, @participant_id, @status, @can_receive_invoice, @can_receive_credit_note, @smp_host, @checked_at::timestamptz)
ON CONFLICT (customer_id) DO UPDATE SET
    participant_id          = EXCLUDED.participant_id,
    status                  = EXCLUDED.status,
    can_receive_invoice     = EXCLUDED.can_receive_invoice,
    can_receive_credit_note = EXCLUDED.can_receive_credit_note,
    smp_host                = EXCLUDED.smp_host,
    checked_at              = EXCLUDED.checked_at;
