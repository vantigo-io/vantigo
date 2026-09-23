-- name: ContactRolesForAssociation :many
-- ContactRolesForAssociation is the typed roles of ONE association (typed
-- contact roles design D2, D3): what the attach/update/detach handlers read
-- under the customer row's lock before deciding anything, and what a single
-- association's response answers. The ordering is the design's fixed one,
-- billing/project/decision_maker, so the array a client receives never
-- shuffles between reads; an unknown code (there is none today — the
-- vocabulary is validated in Go — but a widened list is a value change, not a
-- migration) sorts last by name rather than vanishing.
SELECT role, is_primary, created_at
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND contact_id = @contact_id
ORDER BY
    CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END,
    role;

-- name: ContactRolesForCustomer :many
-- ContactRolesForCustomer is every role row of one customer in ONE query
-- (design D3), for GET /customers/{id}/contacts: a per-row read would be one
-- round trip per contact for data that is on the wire either way, the same
-- reasoning CustomerTagsForCustomers states for a page of customers. Ordered
-- by contact so the caller can walk it beside the association list, then by
-- the design's fixed role order.
SELECT contact_id, role, is_primary
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id
ORDER BY
    contact_id,
    CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END,
    role;

-- name: ContactRolesForContact :many
-- ContactRolesForContact is the mirror of the query above for
-- GET /customers/contacts/{id}/customers, and it is also DELETE
-- /customers/contacts/{id}'s own read: the roles a contact about to be
-- deleted holds at each of its customers, which is what decides where a
-- promotion has to follow the cascade. ix_customer_contact_roles_contact
-- (migration 00025) is what makes it a lookup rather than a scan.
SELECT customer_id, role, is_primary
FROM customers.customer_contact_roles
WHERE contact_id = @contact_id
ORDER BY
    customer_id,
    CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END,
    role;

-- name: PrimaryContactRoleHolder :one
-- PrimaryContactRoleHolder finds the contact to demote when a write makes a
-- different contact primary for the same role (design D2) — the twin of
-- addresses' PrimaryCustomerAddressOfType. At most one row can ever match:
-- that is ux_customer_contact_roles_primary's own invariant. pgx.ErrNoRows
-- means the role currently has no holder at all, which contact_roles.go reads
-- as "nothing to demote", not as an error.
SELECT contact_id
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND role = @role AND is_primary
LIMIT 1;

-- name: CountContactRoleHolders :one
-- CountContactRoleHolders is the "is this the FIRST holder of the role" check
-- (design D2: the first holder is its primary whatever the request says) —
-- addresses' CountCustomerAddressesOfType, one table over. exclude_contact_id
-- is the contact doing the writing, always: on an attach it holds nothing yet
-- so excluding it changes no count, and on an update it must not count itself
-- as the incumbent it is about to join.
SELECT count(*)
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND role = @role AND contact_id <> @exclude_contact_id;

-- name: OldestContactRoleHolder :one
-- OldestContactRoleHolder is the contact a lost primary promotes (design D2:
-- the LONGEST-STANDING remaining holder) — addresses'
-- OldestCustomerAddressOfType, with created_at then contact_id as the
-- tie-break so two roles given in the same transaction still promote
-- deterministically. exclude_contact_id is the contact that just gave the role
-- up or was detached. pgx.ErrNoRows means nobody else holds it: the role is
-- simply unheld now, so there is nothing to promote and the "always a primary
-- while anyone holds the role" invariant is vacuous.
SELECT contact_id
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND role = @role AND contact_id <> @exclude_contact_id
ORDER BY created_at, contact_id
LIMIT 1;

-- name: SetContactRolePrimary :exec
-- SetContactRolePrimary flips one role row's is_primary flag alone: the
-- demote-before-promote step every write elsewhere in the same transaction
-- needs before a different contact can safely become primary for the role,
-- without transiently violating ux_customer_contact_roles_primary. Scoped to
-- customer_id and contact_id both, like every other statement in this file:
-- every triple this receives came from a row the same transaction just read
-- under that customer's lock, so the extra predicates change no row this code
-- path touches — they exist so a call-site mistake matches zero rows instead
-- of quietly flipping some other association's flag. created_at is never
-- moved: it is what "longest-standing" means, so a demotion or a promotion
-- must not reset a contact's seniority in the role.
UPDATE customers.customer_contact_roles
SET is_primary = @is_primary
WHERE customer_id = @customer_id AND contact_id = @contact_id AND role = @role;

-- name: InsertContactRole :exec
-- InsertContactRole gives one association one role. is_primary is whatever
-- contact_roles.go already resolved (first-holder forced true, or the
-- request's own value once the role has a holder) — this statement never
-- decides it itself, the same division InsertCustomerAddress keeps. created_at
-- comes from the caller's Deps.Clock(), the convention every insert in this
-- module follows.
INSERT INTO customers.customer_contact_roles (customer_id, contact_id, role, is_primary, created_at)
VALUES (@customer_id, @contact_id, @role, @is_primary, @now::timestamptz);

-- name: DeleteContactRolesNotIn :exec
-- DeleteContactRolesNotIn is the removal half of an update's complete-set
-- replace (design D3: the request names the whole set to hold). It is a
-- subtraction rather than a delete-everything-and-re-insert, and that is
-- load-bearing: re-inserting a role the association already held would reset
-- its created_at, and created_at is what decides who gets promoted when a
-- primary steps down. So the rows that survive keep both their seniority and
-- their flag, and only the roles the request dropped leave.
--
-- `role <> ALL(@keep::text[])` with an empty array is TRUE for every row, so
-- an empty keep list clears the association's roles — which is exactly what
-- `roles: []` means.
DELETE FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND contact_id = @contact_id AND role <> ALL(@keep::text[]);
