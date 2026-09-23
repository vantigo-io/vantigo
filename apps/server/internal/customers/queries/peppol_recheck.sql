-- name: AgedPeppolLookups :many
-- AgedPeppolLookups is the re-check worker's first candidate set (registry
-- workers design D6): customers whose stored Peppol answer is older than the
-- re-check age, oldest first.
--
-- The participant equality D6 also requires is NOT here: the participant a
-- customer resolves to today is peppolId or else "0192:"+the organisation
-- number, and only Go knows that rule (lookupParticipant, billing_values.go's
-- derivedPeppolID and its check-digit validation). Duplicating it in SQL would
-- be a second copy free to drift; the worker filters the rows this returns
-- instead, which is why it asks for a batch and may refresh fewer.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference,
       l.participant_id, l.checked_at
FROM customers.customers c
JOIN customers.customer_peppol_lookups l ON l.customer_id = c.id
WHERE c.status <> 'archived'
  AND l.checked_at < @checked_before::timestamptz
ORDER BY l.checked_at, c.id
LIMIT @row_limit::int;

-- name: DeleteCustomerPeppolLookup :exec
-- DeleteCustomerPeppolLookup drops a stored answer the re-check worker found to
-- be about a participant the customer no longer resolves to (design D6's
-- participant rule, Task 4 review). Nothing reads such a row —
-- resolvedPeppolLookup already treats it as never checked, in every response and
-- every warning — and leaving it would keep it in the aged set for good: its
-- checked_at never moves, so it occupies a slot of every cycle's batch forever.
-- Deleting it is also what makes the customer's next lookup a first one, which
-- is exactly what it is for the participant it now resolves to.
DELETE FROM customers.customer_peppol_lookups
WHERE customer_id = @customer_id;

-- name: EhfCustomersWithoutPeppolLookup :many
-- EhfCustomersWithoutPeppolLookup is the second candidate set (design D6): a
-- customer whose invoices are already being sent to Peppol and whose
-- registration has never actually been checked. That combination is the one
-- worth a network call unprompted — everyone else's EHF readiness is a
-- question nobody has asked yet, and this worker does not go looking for it.
--
-- The last predicate is a pre-filter, not the rule (final fix wave I1,
-- CustomersWithoutRegistryRecord's own pattern): a customer set to 'ehf' with
-- no peppolId and no Norwegian organisation number resolves to no participant
-- at all, so the Go loop skips it WITHOUT writing anything — and a row that
-- nothing writes to comes back at the head of the batch every cycle forever.
-- Fifty-one of them would starve the one genuine candidate behind them, in
-- silence. Keeping them out here is the fix; lookupParticipant stays the
-- authority (it also knows the check digit, which SQL cannot judge), so this
-- can only let through more rows than the worker will ask about, never fewer.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference
FROM customers.customers c
LEFT JOIN customers.customer_peppol_lookups l ON l.customer_id = c.id
WHERE l.customer_id IS NULL
  AND c.status <> 'archived'
  AND c.invoice_delivery = 'ehf'
  AND (c.peppol_id IS NOT NULL
       OR (c.type = 'business' AND c.legal_country = 'no' AND c.legal_id ~ '^[0-9]{9}$'))
ORDER BY c.id
LIMIT @row_limit::int;
