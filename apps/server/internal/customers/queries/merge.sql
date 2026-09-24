-- name: CustomerForMerge :one
-- CustomerForMerge is a merge's read of each of its two customers (customers
-- merge design D2, D3), made after both LockCustomer calls so the refusal
-- ladder and the absorbed customer's snapshot see the rows exactly as they
-- will be written: the type, status, marker and revision the ladder checks,
-- whether the absorbed customer was anonymised (customers GDPR design D4 — an
-- anonymised customer is read-only, and a merge would write it), and every
-- column customer.merged's absorbed payload records — the identity, the
-- contact info, the eleven billing columns, the owner and the group.
SELECT id, customer_number, name, type, status, revision, merged_into_customer_id, anonymised_at,
       legal_country, legal_id, legal_name, legal_source, legal_type,
       email, phone, website, owner_user_id, group_id,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE id = @id;

-- name: InsertMergedAssociations :exec
-- InsertMergedAssociations is the first of a merge's three contact statements
-- (design D3): every association of the absorbed customer is copied to the
-- survivor, and a contact the survivor already has keeps the survivor's own
-- row — its title, phone and email override — through ON CONFLICT on the
-- primary key. The absorbed rows are deleted afterwards
-- (DeleteCustomerAssociations), once their roles have been copied too:
-- customer_contact_roles' composite foreign key has no ON UPDATE, so an
-- association cannot simply have its customer_id rewritten under its roles.
INSERT INTO customers.customers_contacts (customer_id, contact_id, title, phone, email)
SELECT @into_customer_id::int, contact_id, title, phone, email
FROM customers.customers_contacts
WHERE customer_id = @from_customer_id::int
ON CONFLICT (customer_id, contact_id) DO NOTHING;

-- name: InsertMergedContactRoles :exec
-- InsertMergedContactRoles unions the roles (design D3) and decides every
-- primary flag in the same statement. A role the survivor already has a
-- primary for keeps it, and every absorbed role row for it arrives
-- non-primary; a role the survivor has nobody in takes the absorbed customer's
-- primary as its own — the absorbed customer has at most one per role
-- (ux_customer_contact_roles_primary), so this can never make two. The NOT
-- EXISTS reads the statement's snapshot, which is the survivor's rows before
-- this insert. A role the survivor's association already holds for a shared
-- contact is kept as the survivor has it (the conflict target is the primary
-- key, deliberately: a primary-index conflict would be a bug to hear about, not
-- one to swallow). created_at travels with the row, so a contact's seniority in
-- a role — what decides who is promoted when a primary later steps down — is
-- the seniority it earned.
INSERT INTO customers.customer_contact_roles (customer_id, contact_id, role, is_primary, created_at)
SELECT @into_customer_id::int, r.contact_id, r.role,
       r.is_primary AND NOT EXISTS (
           SELECT 1 FROM customers.customer_contact_roles p
           WHERE p.customer_id = @into_customer_id::int AND p.role = r.role AND p.is_primary
       ),
       r.created_at
FROM customers.customer_contact_roles r
WHERE r.customer_id = @from_customer_id::int
ON CONFLICT (customer_id, contact_id, role) DO NOTHING;

-- name: DeleteCustomerAssociations :execrows
-- DeleteCustomerAssociations removes every association of a customer, and
-- with them its role rows (their foreign key cascades) — the last of the
-- merge's contact statements, once both copies above have run. Its row count
-- is the merge's customers.contacts: how many associations the absorbed
-- customer had, a contact the survivor already had included.
DELETE FROM customers.customers_contacts WHERE customer_id = @customer_id;

-- name: MoveCustomerAddresses :execrows
-- MoveCustomerAddresses moves every address of the absorbed customer to the
-- survivor (design D3), label and all, demoting the absorbed primary of a type
-- the survivor already has a primary for — the NOT EXISTS reads the
-- statement's snapshot, the survivor's own addresses — and keeping it primary
-- for a type the survivor has none of, so every type still has exactly one
-- (ux_customer_addresses_primary). The 50-address cap guards a write, not a
-- merge (docs/customers.md, Merging duplicates).
UPDATE customers.customer_addresses a
SET customer_id = @into_customer_id::int,
    is_primary = a.is_primary AND NOT EXISTS (
        SELECT 1 FROM customers.customer_addresses p
        WHERE p.customer_id = @into_customer_id::int AND p.type = a.type AND p.is_primary
    ),
    updated_at = @now::timestamptz
WHERE a.customer_id = @from_customer_id::int;

-- name: MoveTimelineEntries :one
-- MoveTimelineEntries moves every timeline entry of the absorbed customer to
-- the survivor (design D3), deleted ones and follow-ups included, and answers
-- how many of them were active — the count a person reading the summary will
-- find on the page. Payloads are not rewritten: payload_json.customerId says
-- which customer an event happened to at the time. Neither updated_at nor
-- current_revision moves: this is not an edit, and the entry's revision
-- history must not claim one.
WITH moved AS (
    UPDATE customers.customers_timeline_entries
    SET customer_id = @into_customer_id::int
    WHERE customer_id = @from_customer_id::int
    RETURNING state
)
SELECT count(*) FILTER (WHERE state = 'active')::bigint AS active_count FROM moved;

-- name: MoveTimelineRevisions :exec
-- MoveTimelineRevisions rewrites the revisions' own customer_id to match their
-- entry's (00003 mirrors it column for column) — to the ENTRY's, read in this
-- statement, not to the survivor's id: a revision follows its entry wherever
-- MoveTimelineEntries left it. Every writer of an entry takes the customer's
-- lock first and so waits for the merge, but should one ever slip in between
-- the two statements, the worst it can do is leave an entry, with every one of
-- its revisions, behind — never split a revision from its entry. The column is
-- not indexed, so this reads the revisions table; a merge is rare, and an
-- index every timeline write would pay for is not worth it.
UPDATE customers.customers_timeline_entries_revisions r
SET customer_id = e.customer_id
FROM customers.customers_timeline_entries e
WHERE e.id = r.customer_timeline_entry_id
  AND r.customer_id = @from_customer_id::int
  AND e.customer_id <> r.customer_id;

-- name: MergeCustomerTags :one
-- MergeCustomerTags unions the tags (design D3): the absorbed customer's links
-- are deleted and re-inserted for the survivor in one statement, a tag the
-- survivor already carries kept once by the primary key. moved_count is how
-- many tags the absorbed customer had; added_count how many were new to the
-- survivor.
WITH gone AS (
    DELETE FROM customers.customer_tags
    WHERE customer_id = @from_customer_id::int
    RETURNING tag_id
), kept AS (
    INSERT INTO customers.customer_tags (customer_id, tag_id)
    SELECT @into_customer_id::int, tag_id FROM gone
    ON CONFLICT (customer_id, tag_id) DO NOTHING
    RETURNING tag_id
)
SELECT (SELECT count(*) FROM gone)::bigint AS moved_count, (SELECT count(*) FROM kept)::bigint AS added_count;

-- name: BumpCustomerRevision :execrows
-- BumpCustomerRevision is the survivor's own write in a merge (design D3): its
-- fields do not change, but what hangs off it did, and every write to the row
-- bumps revision (customers foundation design D5). Guarded like every other
-- revision-bearing write: the handler compared expected_revision under the
-- lock already, and the WHERE repeats it.
UPDATE customers.customers
SET updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int);

-- name: MarkCustomerMerged :exec
-- MarkCustomerMerged is the absorbed customer's end (design D3): archived —
-- already, or now — with the marker naming the survivor, and its revision
-- advanced. Its name, number, identity, contact info and billing profile stay
-- as they were, so its page still reads. It leaves its group, though: a
-- merged-away customer refuses every write, the group PUT included, so a group
-- it still counted in could never be emptied and deleted (customers_group_id_fkey
-- restricts). customer.merged's absorbed.groupId records which group it was.
UPDATE customers.customers
SET status = 'archived', merged_into_customer_id = @into_customer_id::int, group_id = NULL,
    updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id;

-- name: FlattenMergedIntoChain :exec
-- FlattenMergedIntoChain keeps every marker one hop long (customers merge
-- design D3): a customer merged into the one being absorbed now names the
-- survivor, so A merged into B and B later into C leaves A pointing at C, not
-- at B — the customer whose records are really there. Each re-pointed row is
-- written, so its revision advances as any write to the row does. These rows
-- are not locked first: they are merged away already, and every writer of a
-- merged-away customer refuses once it holds the lock, so the UPDATE's own row
-- lock is all this needs.
UPDATE customers.customers
SET merged_into_customer_id = @into_customer_id::int,
    updated_at = @now::timestamptz, revision = revision + 1
WHERE merged_into_customer_id = @from_customer_id::int;

-- name: MergedIntoForCustomers :many
-- MergedIntoForCustomers is the merge marker of a whole page of customers in
-- ONE query (design D3, SafeCustomerResponse.mergedInto), the decoration's
-- shape (owner.go): a batched read beside the row rather than a column on the
-- five customer row types. A customer that was not merged away has no row.
SELECT c.id AS customer_id, t.id, t.customer_number, t.name
FROM customers.customers c
JOIN customers.customers t ON t.id = c.merged_into_customer_id
WHERE c.id = ANY(@customer_ids::int[]);
