-- name: ListCustomerGroups :many
-- ListCustomerGroups is GET /customers/groups (customer groups design D2):
-- the whole vocabulary, name-ascending, each with how many customers belong to
-- it. Unpaged, and the count travels with the list, both for the tag
-- vocabulary's own reasons (queries/tags.sql): the delete confirmation and the
-- group picker read the one list, a vocabulary is tens of rows rather than
-- thousands, and a correlated count per row over an indexed column costs
-- nothing worth a second round trip. id after the name is defensive only:
-- ux_customers_groups_name_lower already makes two equal names impossible, so
-- it never decides an order today, and it is there so the order stays total if
-- that index ever changes.
SELECT g.id, g.name, g.default_payment_terms_days,
       (SELECT count(*) FROM customers.customers c WHERE c.group_id = g.id) AS customer_count
FROM customers.customer_groups g
ORDER BY g.name, g.id;

-- name: InsertCustomerGroup :one
-- InsertCustomerGroup is POST /customers/groups. The duplicate check is this
-- statement's own unique violation rather than a SELECT first: two callers
-- creating 'Retail' at the same moment is exactly the race a check-then-insert
-- loses, and ux_customers_groups_name_lower is the only thing that can decide
-- it. The handler matches that constraint BY NAME (db.IsUniqueViolation), never
-- "some unique violation", so a uuid collision on the primary key stays a 500
-- the caller must hear about.
INSERT INTO customers.customer_groups (id, name, default_payment_terms_days, created_at, updated_at)
VALUES (@id, @name, @default_payment_terms_days, @now::timestamptz, @now::timestamptz)
RETURNING id, name, default_payment_terms_days;

-- name: UpdateCustomerGroupRow :one
-- UpdateCustomerGroupRow is PUT /customers/groups/{groupId}: a FULL REPLACE of
-- both fields, so an omitted or null default_payment_terms_days CLEARS the
-- group's default rather than leaving the one it had — the request says what
-- the group is, not what changed (design D2). Named …Row rather than
-- UpdateCustomerGroup so the generated method does not read as "update a
-- customer's group", which is SetCustomerGroup below.
--
-- No timeline write anywhere near this statement: renaming a group, or moving
-- its default, records nothing on the customers that belong to it (design D2 —
-- the group is the vocabulary, not the customer). That a new default changes
-- every member's effective payment term at once is by design, and the docs say
-- so.
--
-- customer_count comes back from this same statement, for UpdateCustomerTagRow's
-- own reason (final fix wave M3): the body this endpoint answers is what the
-- Manage groups modal keeps on screen, so the count must be the one the list
-- would report, and reading it back separately left a window in which a group
-- deleted just after the update turned a successful write into a 404.
-- pgx.ErrNoRows means the group does not exist.
UPDATE customers.customer_groups
SET name = @name, default_payment_terms_days = @default_payment_terms_days, updated_at = @now::timestamptz
WHERE customers.customer_groups.id = @id
RETURNING customers.customer_groups.id, name, default_payment_terms_days,
          (SELECT count(*) FROM customers.customers c WHERE c.group_id = customers.customer_groups.id) AS customer_count;

-- name: CountCustomerGroupMembers :one
-- CountCustomerGroupMembers is what DELETE /customers/groups/{groupId} answers
-- 409 group_in_use with (design D2): the number goes into the problem detail,
-- so the refusal says what has to be moved rather than only that something
-- does. It is asked BEFORE the delete because the foreign key's RESTRICT
-- cannot say how many rows it protected — the violation names a constraint,
-- not a count.
SELECT count(*) FROM customers.customers WHERE group_id = @group_id;

-- name: DeleteCustomerGroup :execrows
-- DeleteCustomerGroup is DELETE /customers/groups/{groupId} for an EMPTY group.
-- Nothing cascades: the membership column's foreign key is RESTRICT, so a group
-- somebody moved a customer into between the count above and this statement
-- raises a restrict_violation (23001 on PostgreSQL 18+, 23503 before it)
-- instead of quietly detaching its members, and the handler matches both. The
-- affected row count is how the handler tells 204 from 404.
DELETE FROM customers.customer_groups WHERE id = @id;

-- name: GetCustomerGroup :one
-- GetCustomerGroup resolves the one groupId PUT /customers/{id}/group was
-- given, so an unknown one is a field error on groupId rather than a
-- foreign-key violation surfacing as a 500 — the same job CustomerTagsByIDs
-- does for tags, in the singular because this body names exactly one group and
-- never a set. It also supplies the name the event's `after` snapshot stores.
-- pgx.ErrNoRows means no such group.
SELECT id, name, default_payment_terms_days FROM customers.customer_groups WHERE id = @id;

-- name: CustomerGroupsForCustomers :many
-- CustomerGroupsForCustomers is the group of a whole page of customers in ONE
-- query (design D3), never one query per row: the list answers 25 customers and
-- a per-row read would be 25 round trips for data that is on the wire either
-- way. The single-customer reads (GET /customers/{id} and every sub-resource
-- PUT that answers SafeCustomerResponse) call it with a one-element array
-- rather than having a query of their own, so there is one shape of
-- group-on-a-response and one place it can be wrong — exactly how
-- CustomerTagsForCustomers is used.
--
-- An INNER JOIN, not a LEFT one: a customer in no group contributes no row and
-- the decoration's map simply has no entry for it, which is what "absent when
-- none, never null" means on the wire.
SELECT c.id AS customer_id, g.id, g.name
FROM customers.customers c
JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = ANY(@customer_ids::int[])
ORDER BY c.id;

-- name: CustomerGroupMembership :one
-- CustomerGroupMembership is one customer's group, joined: the id, the name and
-- the default, all NULL when the customer belongs to no group. Two callers, and
-- they want the same three values for different reasons — PUT
-- /customers/{id}/group needs them for its no-op check (same group, nil-safe)
-- and for the event's `before` snapshot, and GET/PUT
-- /customers/{id}/billing-profile needs them for groupDefault (design D4). One
-- query rather than two so the two can never disagree about what a customer
-- inherits.
--
-- Why this exists at all, rather than group_id being selected by GetCustomer:
-- customerRowFrom (customers.go) is shared by five sqlc row types whose queries
-- do not select it, so adding the column there means a sixth positional
-- parameter at five call sites that have nothing to pass. This is one indexed
-- lookup on a primary key, and it keeps the membership out of a struct that
-- exists to make SafeCustomerResponse's projection know one shape.
--
-- pgx.ErrNoRows means the CUSTOMER does not exist (the outer FROM is
-- customers.customers), which is the 404 both callers answer.
--
-- group_id is read from the CUSTOMER's own column rather than from g.id, and
-- that is not a stylistic choice: c.group_id is nullable on the table, so sqlc
-- types it *uuid.UUID without having to infer anything from the outer join,
-- while g.id is NOT NULL on customer_groups and would depend on that inference
-- entirely. The two are the same value by the join's own condition.
--
-- revision is the row's revision as THIS read saw it, and both callers use it
-- for the same reason. Each one reads the customer row first and then this, as
-- two unlocked statements. A write landing between them would pair the first
-- read's revision with the second read's group, so each compares the two
-- revisions before trusting the pair. PUT /customers/{id}/group reads both
-- again, or answers the revision conflict when the caller sent a revision
-- (group_membership.go). Without the comparison it could answer a no-op 200
-- reporting a revision the group it shows never had. The billing profile's
-- billingProfileSnapshot (billing_profile.go) re-reads the profile once, so
-- the revision it answers belongs with the groupDefault beside it.
SELECT c.group_id, g.name AS group_name, g.default_payment_terms_days, c.revision
FROM customers.customers c
LEFT JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = @id;

-- name: SetCustomerGroup :one
-- SetCustomerGroup is PUT /customers/{id}/group's write (design D3): the
-- group_id column only — name, status, type, the legal identity, the contact
-- info, the owner and the billing profile are untouched, since this
-- sub-resource never writes them. Guarded and revision-bumping exactly like
-- UpdateCustomerOwner (queries/customers.sql): sqlc.narg(expected_revision) is
-- PutCustomerGroupRequest's optional revision, and the handler skips calling
-- this entirely when the group did not actually change, so a resubmit of the
-- current group writes nothing and bumps nothing (customers foundation design
-- D5's no-op rule).
--
-- @group_id is NULL to take the customer out of every group, which is a real
-- change like any other: it bumps the revision and records
-- customer.group_changed. The RETURNING list is UpdateCustomerOwner's, column
-- for column, because fromSetCustomerGroupRow feeds the same customerRow —
-- group_id itself is deliberately NOT among them: the response's `group` comes
-- from the decoration's batched query after the transaction commits, like the
-- tags', so this list stays the one shape customerRowFrom knows.
UPDATE customers.customers
SET group_id = @group_id,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;
