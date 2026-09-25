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

-- name: CustomersMergedIntoForPersonalData :many
-- CustomersMergedIntoForPersonalData is the export's mergedFrom (design D3):
-- every duplicate merged into this customer, with what its own row still holds
-- — the merge moves what hangs off a duplicate, not the row's own values, and
-- they are the same person's data. FlattenMergedIntoChain keeps every marker
-- one hop long, so this one level is all of them; ix_customers_merged_into
-- finds them. Oldest first.
SELECT id, customer_number, name, status,
       legal_country, legal_id, legal_name, legal_source, legal_type,
       email, phone, website,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE merged_into_customer_id = @customer_id::int
ORDER BY id;

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

-- name: SetCustomerAnonymiseOn :exec
-- SetCustomerAnonymiseOn is the schedule's write (design D4): PUT sets the
-- day, DELETE clears it (NULL). A write to the row like any other, so the
-- revision advances; both callers hold the lock and have decided the write is
-- not a no-op.
UPDATE customers.customers
SET anonymise_on = sqlc.narg(anonymise_on)::date, updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id;

-- name: DropCustomerAnonymiseOn :exec
-- DropCustomerAnonymiseOn calls a schedule off as part of another write to the
-- row — a restore, a change of type (cancelAnonymisationSchedule,
-- anonymisation.go) — whose own UPDATE has already advanced the revision in the
-- same transaction, so this one does not advance it a second time.
UPDATE customers.customers SET anonymise_on = NULL WHERE id = @id;

-- name: CustomerAnonymisedAt :one
-- CustomerAnonymisedAt is the read-only refusal read on the pool
-- (refuseReadOnlyCustomer, merge.go): when a customer was anonymised, NULL when
-- it was not. pgx.ErrNoRows is a customer that does not exist, whose 404 is the
-- caller's to give.
SELECT anonymised_at FROM customers.customers WHERE id = @id;

-- name: DueAnonymisations :many
-- DueAnonymisations is the worker's batch (design D4): every private person
-- whose day has come (UTC), archived and not yet anonymised, oldest day first,
-- at most row_limit. ix_customers_anonymise_on holds exactly the scheduled and
-- not yet anonymised. A merged-away customer scheduled before its merge is
-- here too: it is archived, and it is a person.
SELECT id
FROM customers.customers
WHERE anonymise_on <= @today::date AND anonymised_at IS NULL AND status = 'archived' AND type = 'person'
ORDER BY anonymise_on, id
LIMIT @row_limit::int;

-- name: CustomersMergedInto :many
-- CustomersMergedInto is the rest of a merge chain (design D4: a chain is one
-- person): every customer merged into this one and not yet anonymised.
-- FlattenMergedIntoChain keeps every marker one hop long, so this one level is
-- the whole chain. ix_customers_merged_into finds them.
SELECT id
FROM customers.customers
WHERE merged_into_customer_id = @customer_id::int AND anonymised_at IS NULL
ORDER BY id;

-- name: DeleteAllCustomerAddresses :execrows
-- Every address of an anonymised customer (design D4): an address is where a
-- person lives or is reached, and none of it is bookkeeping.
DELETE FROM customers.customer_addresses WHERE customer_id = @customer_id;

-- name: DetachCustomerContacts :many
-- Every association of an anonymised customer, its role rows cascading with it
-- (design D4), answering the contacts it linked so their orphans can go next.
DELETE FROM customers.customers_contacts WHERE customer_id = @customer_id RETURNING contact_id;

-- name: LockContactsForAnonymisation :exec
-- The detached contacts, locked before the orphan delete below reads whether
-- anything else still links them: an attach key-shares the contact row, so one
-- that has not taken it yet waits here, and one that has is committed — and
-- seen — by the time the lock is granted. Ascending id, every multi-contact
-- writer's order.
SELECT id FROM customers.contacts WHERE id = ANY(@contact_ids::int[]) ORDER BY id FOR UPDATE;

-- name: DeleteOrphanedContacts :execrows
-- A contact the anonymised customer was the only one linked to existed for it
-- alone (design D4), and goes; one another customer still links is somebody
-- else's contact too, and stays.
DELETE FROM customers.contacts c
WHERE c.id = ANY(@contact_ids::int[])
  AND NOT EXISTS (SELECT 1 FROM customers.customers_contacts cc WHERE cc.contact_id = c.id);

-- name: AnonymiseTimelineEntries :execrows
-- AnonymiseTimelineEntries rewrites every timeline entry of an anonymised
-- customer in one statement (design D4): every entry stays, dated and typed,
-- with its content anonymised. A manual entry's summary and note become the
-- anonymised text and its source URL goes — a link is content too. A generated
-- entry's summary does as well, unless its event type is one whose summary is
-- built from nothing personal (kept_summary_types, anonymisation.go). Every
-- payload's top-level keys named in personal_keys become the anonymised text,
-- every other key — customerId, ids, dates, counts — is kept as it was;
-- coalesce keeps an empty object an object. Neither updated_at nor
-- current_revision moves: this is not an edit, and the revisions below are
-- rewritten the same way rather than added to.
UPDATE customers.customers_timeline_entries e
SET summary = CASE
        WHEN e.provenance = 'generated' AND e.event_type = ANY(@kept_summary_types::text[]) THEN e.summary
        ELSE @anonymised::text
    END,
    note = CASE WHEN e.note IS NULL THEN NULL ELSE @anonymised::text END,
    source_url = NULL,
    payload_json = CASE
        WHEN jsonb_typeof(e.payload_json) = 'object' THEN coalesce((
            SELECT jsonb_object_agg(p.key, CASE WHEN p.key = ANY(@personal_keys::text[]) THEN to_jsonb(@anonymised::text) ELSE p.value END)
            FROM jsonb_each(e.payload_json) AS p(key, value)), '{}'::jsonb)
        ELSE e.payload_json
    END
WHERE e.customer_id = @customer_id::int;

-- name: AnonymiseTimelineRevisions :exec
-- The same rewrite of every revision of those entries (design D4: "revisions
-- the same"), found through their entry — the revisions' own customer_id is not
-- indexed, and ux_customers_timeline_entries_revisions_entry_revision is.
UPDATE customers.customers_timeline_entries_revisions r
SET summary = CASE
        WHEN r.provenance = 'generated' AND r.event_type = ANY(@kept_summary_types::text[]) THEN r.summary
        ELSE @anonymised::text
    END,
    note = CASE WHEN r.note IS NULL THEN NULL ELSE @anonymised::text END,
    source_url = NULL,
    payload_json = CASE
        WHEN jsonb_typeof(r.payload_json) = 'object' THEN coalesce((
            SELECT jsonb_object_agg(p.key, CASE WHEN p.key = ANY(@personal_keys::text[]) THEN to_jsonb(@anonymised::text) ELSE p.value END)
            FROM jsonb_each(r.payload_json) AS p(key, value)), '{}'::jsonb)
        ELSE r.payload_json
    END
WHERE r.customer_timeline_entry_id IN (
    SELECT id FROM customers.customers_timeline_entries WHERE customer_id = @customer_id::int
);

-- name: AnonymiseAbsorbedSnapshots :exec
-- A merged-away customer anonymised on its own (design D4) takes its snapshot
-- off its survivor's timeline: the customer.merged entry that absorbed it keeps
-- its moved counts and loses the absorbed block and the summary naming it.
-- Nothing else of the survivor's is touched — its own history is its own, and
-- the history that moved to it with the merge is its now.
UPDATE customers.customers_timeline_entries
SET summary = @anonymised::text,
    payload_json = jsonb_set(payload_json, '{absorbed}', to_jsonb(@anonymised::text))
WHERE customer_id = @survivor_id::int AND event_type = 'customer.merged'
  AND jsonb_typeof(payload_json -> 'absorbed') = 'object'
  AND (payload_json -> 'absorbed' ->> 'id')::int = @absorbed_id::int;

-- name: AnonymiseAbsorbedSnapshotRevisions :exec
-- And that entry's revisions, which carry its customer_id (MoveTimelineRevisions).
UPDATE customers.customers_timeline_entries_revisions
SET summary = @anonymised::text,
    payload_json = jsonb_set(payload_json, '{absorbed}', to_jsonb(@anonymised::text))
WHERE customer_id = @survivor_id::int AND event_type = 'customer.merged'
  AND jsonb_typeof(payload_json -> 'absorbed') = 'object'
  AND (payload_json -> 'absorbed' ->> 'id')::int = @absorbed_id::int;

-- name: AnonymiseCustomerRow :exec
-- The row itself, last before the event (design D4): the name becomes the
-- anonymised name — the customer number stays, it is the bookkeeping reference
-- — the legal identity, the contact info and the billing profile's five
-- identifiers are cleared, and the terms, currency, language and delivery
-- methods stay: they say how the customer was invoiced, not who it was. Owner
-- and tags stay: staff and vocabulary. It leaves its group, though, the
-- merged-away customer's way (MarkCustomerMerged): an anonymised customer
-- refuses every write, the group PUT included, so a group it still counted in
-- could never be emptied and deleted (customers_group_id_fkey restricts); the
-- group is vocabulary, so no event needs to say which it was. anonymise_on is
-- the day that was scheduled — for a customer merged into the one scheduled,
-- that customer's day, whatever its own schedule said: the chain is one person
-- and one anonymisation. A write to the row, so the revision advances.
UPDATE customers.customers
SET name = @name::text,
    legal_country = NULL, legal_id = NULL, legal_name = NULL, legal_source = NULL, legal_type = NULL,
    email = NULL, phone = NULL, website = NULL,
    invoice_email = NULL, reminder_email = NULL, peppol_id = NULL, gln = NULL, buyer_reference = NULL,
    group_id = NULL,
    anonymise_on = @anonymise_on::date,
    anonymised_at = @now::timestamptz, updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id;
