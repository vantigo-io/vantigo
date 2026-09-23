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

-- name: EhfCustomersWithoutPeppolLookup :many
-- EhfCustomersWithoutPeppolLookup is the second candidate set (design D6): a
-- customer whose invoices are already being sent to Peppol and whose
-- registration has never actually been checked. That combination is the one
-- worth a network call unprompted — everyone else's EHF readiness is a
-- question nobody has asked yet, and this worker does not go looking for it.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference
FROM customers.customers c
LEFT JOIN customers.customer_peppol_lookups l ON l.customer_id = c.id
WHERE l.customer_id IS NULL
  AND c.status <> 'archived'
  AND c.invoice_delivery = 'ehf'
ORDER BY c.id
LIMIT @row_limit::int;
