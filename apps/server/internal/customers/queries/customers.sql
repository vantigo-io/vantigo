-- name: NextCounterValue :one
-- NextCounterValue allocates the next value of a named counter
-- (TN/TenantCounterService.cs), gapless per counter: the first call for a
-- counter starts it at 1; every later call increments it by one under the
-- same upsert, so two concurrent callers can never allocate the same value,
-- even if one of them later rolls back.
INSERT INTO customers.counters (counter_name, next_value)
VALUES (@counter_name, 1)
ON CONFLICT (counter_name) DO UPDATE SET next_value = customers.counters.next_value + 1
RETURNING next_value;

-- name: InsertCustomer :one
-- InsertCustomer creates a customer row. created_at and updated_at are the
-- same instant on creation, supplied by the caller from Deps.Clock().
INSERT INTO customers.customers (
    customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
    created_at, updated_at
) VALUES (
    @customer_number, @name, @status, @legal_country, @legal_id, @legal_name, @legal_source, @legal_type,
    @now::timestamptz, @now::timestamptz
)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at;

-- name: GetCustomer :one
-- GetCustomer fetches one customer by id.
SELECT id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
       created_at, updated_at
FROM customers.customers
WHERE id = @id;

-- name: DirectoryCustomer :one
-- DirectoryCustomer is contracts.CustomerDirectory.Customer's row
-- (SV/ApplicationServiceCollectionExtensions.cs:22-28): a customer of any
-- status, archived included, since a consumer holding a historical reference
-- must still be able to name it.
SELECT id, name, status = 'archived' AS archived
FROM customers.customers
WHERE id = @id;

-- name: DirectoryContact :one
-- DirectoryContact is contracts.CustomerDirectory.Contact's row
-- (SV/ApplicationServiceCollectionExtensions.cs:30-32).
SELECT id, first_name, last_name, email
FROM customers.contacts
WHERE id = @id;

-- name: DirectoryContactsByEmail :many
-- DirectoryContactsByEmail is contracts.CustomerDirectory.ContactsByEmail's
-- rows (SV/ApplicationServiceCollectionExtensions.cs:41-104): one row per
-- contact whose canonical email, or any of whose customer-specific
-- association emails, is the given address, together with the customers that
-- match makes candidates. A contact matched on its canonical email offers
-- every customer it is linked to; one matched only through an association
-- offers just the customers whose association carries that address. The email
-- itself never leaves this query, so no caller can learn an address it did not
-- already have.
--
-- .NET compared stored values that were canonical by construction; here both
-- sides are lower-cased and trimmed, so a row stored in another casing still
-- resolves. Neither column is indexed (the .NET schema had no index either),
-- so the functions cost nothing a bare comparison would have saved.
WITH matching AS (
    SELECT c.id,
           coalesce(lower(btrim(c.email)) = @email::text, false) AS canonical
    FROM customers.contacts c
    WHERE lower(btrim(c.email)) = @email::text
       OR EXISTS (
           SELECT 1
           FROM customers.customers_contacts cc
           WHERE cc.contact_id = c.id
             AND lower(btrim(cc.email)) = @email::text
       )
)
SELECT m.id AS contact_id,
       coalesce(
           array_agg(cc.customer_id ORDER BY cc.customer_id) FILTER (WHERE cc.customer_id IS NOT NULL),
           '{}'
       )::integer[] AS candidate_customer_ids
FROM matching m
LEFT JOIN customers.customers_contacts cc
       ON cc.contact_id = m.id
      AND (m.canonical OR lower(btrim(cc.email)) = @email::text)
GROUP BY m.id
ORDER BY m.id;
