-- This file is every statement customers GDPR design D3 and D4 add: the
-- export's reads, the anonymisation decoration, the schedule's writes and the
-- anonymisation worker's. They are one file because each is meaningful only to
-- a person's data leaving or being taken out, and one file is one place to read
-- what an anonymisation writes — merge.sql's reasoning.

-- name: AnonymisationForCustomers :many
-- AnonymisationForCustomers is the anonymisation of a whole page of customers
-- in ONE query (design D4, SafeCustomerResponse.anonymisation), the merge
-- marker's shape (owner.go): a batched read beside the row rather than two
-- columns on the five customer row types. A customer never scheduled has no
-- row; one anonymised keeps its anonymise_on, so the one predicate covers both.
SELECT id AS customer_id, anonymise_on, anonymised_at
FROM customers.customers
WHERE id = ANY(@customer_ids::int[]) AND anonymise_on IS NOT NULL;

-- name: CustomerForPersonalData :one
-- CustomerForPersonalData is the export's read of the customer row (design D3)
-- and the scheduling's pool read before its lock (D4): the type and status the
-- refusals check, the schedule, and every column the file hands over — the
-- identity, the contact info and the billing profile's eleven columns.
SELECT id, customer_number, name, type, status, revision, merged_into_customer_id, anonymise_on, anonymised_at,
       legal_country, legal_id, legal_name, legal_source, legal_type,
       email, phone, website, owner_user_id, group_id, created_at, updated_at,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE id = @id;

-- name: ListTimelineEntriesForExport :many
-- ListTimelineEntriesForExport is every timeline entry of one customer for its
-- export (design D3): deleted ones included — a soft-deleted note is still held
-- — oldest first, the order a person reads their own history in. Revisions are
-- not part of the file.
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id, follow_up_done_at
FROM customers.customers_timeline_entries
WHERE customer_id = @customer_id
ORDER BY occurred_on, occurred_at NULLS FIRST, id;
