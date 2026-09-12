-- name: InsertContact :one
-- InsertContact creates a contact row (CreateContactEndpoint.cs): every name
-- part and connection detail has already been validated and normalized by
-- the caller; a blank optional arrives here as NULL, never an empty string.
INSERT INTO customers.contacts (first_name, last_name, middle_name, prefix, suffix, phone, email, created_at)
VALUES (@first_name, @last_name, @middle_name, @prefix, @suffix, @phone, @email, @now::timestamptz)
RETURNING id, first_name, last_name, middle_name, prefix, suffix, phone, email, created_at;

-- name: GetContact :one
-- GetContact fetches one contact by id (GetContactEndpoint.cs), unlocked.
SELECT id, first_name, last_name, middle_name, prefix, suffix, phone, email, created_at
FROM customers.contacts
WHERE id = @id;

-- name: GetContactForUpdate :one
-- GetContactForUpdate is the pessimistic row lock customers inventory §4
-- describes (AttachCustomerContactEndpoint.cs:42, DeleteContactEndpoint.cs:22):
-- it serializes an attach against a concurrent delete of the same contact, so
-- one of the two always observes the other's effect instead of racing to an
-- orphaned association or a silently-lost one.
SELECT id, first_name, last_name, middle_name, prefix, suffix, phone, email, created_at
FROM customers.contacts
WHERE id = @id
FOR UPDATE;

-- name: UpdateContact :one
-- UpdateContact is UpdateContactEndpoint's full replace: ContactRequest
-- carries the desired final state of every field, so this always overwrites
-- every optional column rather than preserving what the request omitted.
-- Zero rows affected (id not found) surfaces to the caller as pgx.ErrNoRows,
-- the endpoint's 404.
UPDATE customers.contacts
SET first_name = @first_name, last_name = @last_name, middle_name = @middle_name,
    prefix = @prefix, suffix = @suffix, phone = @phone, email = @email
WHERE id = @id
RETURNING id, first_name, last_name, middle_name, prefix, suffix, phone, email, created_at;

-- name: DeleteContact :exec
-- DeleteContact removes a contact row (DeleteContactEndpoint.cs:38); the FK
-- ON DELETE CASCADE on customers_contacts removes its associations. The
-- "removed" timeline event per association is the caller's responsibility to
-- record first, in the same transaction — this statement carries none of its
-- own.
DELETE FROM customers.contacts WHERE id = @id;

-- name: CountContacts :one
-- CountContacts is GetContactsEndpoint's total row count. @patterns holds one
-- already-ILIKE-escaped "%term%" glob per whitespace-separated search term
-- (empty when the caller sent none); a contact passes only when every
-- pattern matches at least one of its searchable fields
-- (GetContactsEndpoint.cs:41-56, "every term must match at least one of the
-- searchable fields"). unnest() of an empty or NULL array yields no rows, so
-- NOT EXISTS is trivially true and every contact passes when there is no
-- search at all.
SELECT count(*)
FROM customers.contacts
WHERE NOT EXISTS (
    SELECT 1 FROM unnest(@patterns::text[]) AS p(pattern)
    WHERE NOT (
        first_name ILIKE p.pattern OR
        last_name ILIKE p.pattern OR
        (middle_name IS NOT NULL AND middle_name ILIKE p.pattern) OR
        (prefix IS NOT NULL AND prefix ILIKE p.pattern) OR
        (suffix IS NOT NULL AND suffix ILIKE p.pattern) OR
        (phone IS NOT NULL AND phone ILIKE p.pattern) OR
        (email IS NOT NULL AND email ILIKE p.pattern)
    )
);

-- name: ListContactsByID :many
-- ListContactsByID is GetContacts's sortBy=id path, the same search
-- predicate as CountContacts.
SELECT id, first_name, last_name, middle_name, prefix, suffix, phone, email, created_at
FROM customers.contacts
WHERE NOT EXISTS (
    SELECT 1 FROM unnest(@patterns::text[]) AS p(pattern)
    WHERE NOT (
        first_name ILIKE p.pattern OR
        last_name ILIKE p.pattern OR
        (middle_name IS NOT NULL AND middle_name ILIKE p.pattern) OR
        (prefix IS NOT NULL AND prefix ILIKE p.pattern) OR
        (suffix IS NOT NULL AND suffix ILIKE p.pattern) OR
        (phone IS NOT NULL AND phone ILIKE p.pattern) OR
        (email IS NOT NULL AND email ILIKE p.pattern)
    )
)
ORDER BY
    CASE WHEN NOT @descending::bool THEN id END ASC,
    CASE WHEN @descending::bool THEN id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: ListContactsByName :many
-- ListContactsByName is GetContacts's default sort: first name, then last
-- name, then id as the tie-break (GetContactsEndpoint.cs:147-157).
SELECT id, first_name, last_name, middle_name, prefix, suffix, phone, email, created_at
FROM customers.contacts
WHERE NOT EXISTS (
    SELECT 1 FROM unnest(@patterns::text[]) AS p(pattern)
    WHERE NOT (
        first_name ILIKE p.pattern OR
        last_name ILIKE p.pattern OR
        (middle_name IS NOT NULL AND middle_name ILIKE p.pattern) OR
        (prefix IS NOT NULL AND prefix ILIKE p.pattern) OR
        (suffix IS NOT NULL AND suffix ILIKE p.pattern) OR
        (phone IS NOT NULL AND phone ILIKE p.pattern) OR
        (email IS NOT NULL AND email ILIKE p.pattern)
    )
)
ORDER BY
    CASE WHEN NOT @descending::bool THEN first_name END ASC,
    CASE WHEN @descending::bool THEN first_name END DESC,
    CASE WHEN NOT @descending::bool THEN last_name END ASC,
    CASE WHEN @descending::bool THEN last_name END DESC,
    CASE WHEN NOT @descending::bool THEN id END ASC,
    CASE WHEN @descending::bool THEN id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: AssociationsForContacts :many
-- AssociationsForContacts is GetContactsEndpoint's LoadAssociations
-- (GetContactsEndpoint.cs:85-110): every customer association of the given
-- page of contacts, in one query, so the per-contact customerCount and
-- single-customer projection never fan out per row.
SELECT cc.contact_id, cu.id AS customer_id, cu.customer_number, cu.name AS customer_name
FROM customers.customers_contacts cc
JOIN customers.customers cu ON cu.id = cc.customer_id
WHERE cc.contact_id = ANY(@contact_ids::int[]);

-- name: ListContactAssociationsForCustomer :many
-- ListContactAssociationsForCustomer is GetCustomerContactsEndpoint's
-- listing, sorted by the contact's name (GetCustomerContactsEndpoint.cs:29-36).
SELECT c.id, c.first_name, c.last_name, c.middle_name, c.prefix, c.suffix,
       c.phone AS contact_phone, c.email AS contact_email,
       cc.role, cc.phone AS association_phone, cc.email AS association_email
FROM customers.customers_contacts cc
JOIN customers.contacts c ON c.id = cc.contact_id
WHERE cc.customer_id = @customer_id
ORDER BY c.first_name, c.last_name, c.id;

-- name: ListCustomerAssociationsForContact :many
-- ListCustomerAssociationsForContact is GetContactCustomersEndpoint's
-- listing, sorted by the customer's name (GetContactCustomersEndpoint.cs:29-33).
SELECT cu.id, cu.customer_number, cu.name, cc.role, cc.phone, cc.email
FROM customers.customers_contacts cc
JOIN customers.customers cu ON cu.id = cc.customer_id
WHERE cc.contact_id = @contact_id
ORDER BY cu.name, cu.id;

-- name: AssociationExists :one
-- AssociationExists is AttachCustomerContactEndpoint's already-attached
-- check (:50-51), run only after both the customer and the (now locked)
-- contact are confirmed to exist.
SELECT EXISTS (
    SELECT 1 FROM customers.customers_contacts WHERE customer_id = @customer_id AND contact_id = @contact_id
);

-- name: InsertAssociation :exec
-- InsertAssociation creates the customer-contact row
-- (AttachCustomerContactEndpoint.cs:61-62).
INSERT INTO customers.customers_contacts (customer_id, contact_id, role, phone, email)
VALUES (@customer_id, @contact_id, @role, @phone, @email);

-- name: GetAssociationWithContact :one
-- GetAssociationWithContact is UpdateCustomerContactEndpoint's and
-- DetachCustomerContactEndpoint's shared lookup; the contact's own fields are
-- included since both callers need them for their response or their
-- timeline event.
SELECT cc.role, cc.phone AS association_phone, cc.email AS association_email,
       c.id, c.first_name, c.last_name, c.middle_name, c.prefix, c.suffix,
       c.phone AS contact_phone, c.email AS contact_email, c.created_at
FROM customers.customers_contacts cc
JOIN customers.contacts c ON c.id = cc.contact_id
WHERE cc.customer_id = @customer_id AND cc.contact_id = @contact_id;

-- name: UpdateAssociation :exec
-- UpdateAssociation applies PUT .../contacts/{contactId}'s validated fields
-- (UpdateCustomerContactEndpoint.cs:38-50); whether the caller also records a
-- timeline event is decided by comparing the row this statement replaces
-- against the new values, not by this statement itself.
UPDATE customers.customers_contacts
SET role = @role, phone = @phone, email = @email
WHERE customer_id = @customer_id AND contact_id = @contact_id;

-- name: DeleteAssociation :exec
-- DeleteAssociation is DetachCustomerContactEndpoint's row removal (:31).
DELETE FROM customers.customers_contacts WHERE customer_id = @customer_id AND contact_id = @contact_id;

-- name: ListAssociationsForContact :many
-- ListAssociationsForContact is DeleteContactEndpoint's cascade source
-- (:29-32): every customer this contact is attached to, for the "removed"
-- timeline event DeleteContact records against each one before the contact
-- row (and its associations, via ON DELETE CASCADE) are deleted.
SELECT customer_id, role, phone, email
FROM customers.customers_contacts
WHERE contact_id = @contact_id;
