-- name: LockCustomer :one
-- LockCustomer is every address write's first statement (invoice-ready
-- customer design D3's controller ruling): a bare existence check that also
-- takes FOR NO KEY UPDATE on the customer row, so two address writes against
-- the same customer serialize through this one lock rather than racing each
-- other's primary-flag bookkeeping. NO KEY UPDATE, not the plainer UPDATE a
-- write to the row itself would need, since this never touches a column any
-- foreign key references. pgx.ErrNoRows means the customer does not exist —
-- the addresses.go handlers turn that into a 404 the same way GetCustomer's
-- callers elsewhere in this module do.
SELECT id FROM customers.customers WHERE id = @id FOR NO KEY UPDATE;

-- name: ListCustomerAddresses :many
-- ListCustomerAddresses is GetCustomersByIdAddresses's one query
-- (invoice-ready customer design D3, controller ruling): every address of
-- one customer, ordered by type in the fixed order invoice/postal/delivery/
-- visiting, primary first within a type, then id — never by created_at,
-- which would let a re-labelled or re-ordered address jump around the list.
SELECT id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at
FROM customers.customer_addresses
WHERE customer_id = @customer_id
ORDER BY
    CASE type WHEN 'invoice' THEN 0 WHEN 'postal' THEN 1 WHEN 'delivery' THEN 2 WHEN 'visiting' THEN 3 ELSE 4 END,
    is_primary DESC,
    id;

-- name: CountCustomerAddresses :one
-- CountCustomerAddresses backs the 50-address cap (invoice-ready customer
-- design D3): every type together, checked under the customer lock so two
-- concurrent creates can never both slip in as the 50th and 51st.
SELECT count(*) FROM customers.customer_addresses WHERE customer_id = @customer_id;

-- name: CountCustomerAddressesOfType :one
-- CountCustomerAddressesOfType is PostCustomersByIdAddresses/
-- PutCustomersByIdAddressesByAddressId's "is this the first address of its
-- type" check (invoice-ready customer design D3): the first address of a
-- type is primary whatever the request says. exclude_id is 0 on create
-- (nothing to exclude); on a PUT that changes an address's type, it is that
-- address's own id, since it is not yet counted as a member of the type it
-- is joining.
SELECT count(*) FROM customers.customer_addresses
WHERE customer_id = @customer_id AND type = @type AND id <> @exclude_id;

-- name: PrimaryCustomerAddressOfType :one
-- PrimaryCustomerAddressOfType finds the address to demote when a write
-- makes a different address of the same type primary (invoice-ready
-- customer design D3): at most one row can ever match, the partial unique
-- index's own invariant. pgx.ErrNoRows means the type currently has no
-- primary (the type has no addresses yet), which addresses.go treats as
-- "nothing to demote", not an error.
SELECT id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at
FROM customers.customer_addresses
WHERE customer_id = @customer_id AND type = @type AND is_primary
LIMIT 1;

-- name: OldestCustomerAddressOfType :one
-- OldestCustomerAddressOfType is the address a delete-the-primary or a
-- type-changing PUT promotes in the address's old type (invoice-ready
-- customer design D3): the oldest remaining address of that type, other
-- than the one leaving (exclude_id), ordered by created_at then id.
-- pgx.ErrNoRows means none remain — the type is now empty, so there is
-- nothing to promote, not an error.
SELECT id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at
FROM customers.customer_addresses
WHERE customer_id = @customer_id AND type = @type AND id <> @exclude_id
ORDER BY created_at, id
LIMIT 1;

-- name: SetCustomerAddressPrimary :exec
-- SetCustomerAddressPrimary flips one address's is_primary flag alone
-- (invoice-ready customer design D3): the demote-before-promote step a write
-- elsewhere in the same transaction needs before its own INSERT/UPDATE can
-- safely set a different address of the same type primary, without
-- transiently violating ux_customer_addresses_primary.
UPDATE customers.customer_addresses SET is_primary = @is_primary, updated_at = @updated_at::timestamptz WHERE id = @id;

-- name: InsertCustomerAddress :one
-- InsertCustomerAddress is PostCustomersByIdAddresses's write (invoice-ready
-- customer design D3): created_at and updated_at are the same instant,
-- supplied by the caller from Deps.Clock(), the same convention
-- InsertCustomer follows. is_primary is whatever addresses.go already
-- resolved (first-of-type forced true, or the request's own value) — this
-- statement never decides it itself.
INSERT INTO customers.customer_addresses (
    customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at
) VALUES (
    @customer_id, @type, @label, @line1, @line2, @postal_code, @city, @region, @country, @is_primary, @now::timestamptz, @now::timestamptz
)
RETURNING id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at;

-- name: GetCustomerAddress :one
-- GetCustomerAddress fetches one address, scoped to the customer it must
-- belong to (invoice-ready customer design D3): an address id valid for a
-- different customer answers the same 404 as one that does not exist at
-- all, never leaking whose it actually is.
SELECT id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at
FROM customers.customer_addresses
WHERE id = @id AND customer_id = @customer_id;

-- name: UpdateCustomerAddress :one
-- UpdateCustomerAddress is PutCustomersByIdAddressesByAddressId's write: a
-- full replace of every column (invoice-ready customer design D3, D1's
-- sub-resource PUT shape) except customer_id, created_at, which a PUT never
-- moves. is_primary is whatever addresses.go already resolved, the same
-- convention InsertCustomerAddress follows.
UPDATE customers.customer_addresses
SET type = @type, label = @label, line1 = @line1, line2 = @line2, postal_code = @postal_code,
    city = @city, region = @region, country = @country, is_primary = @is_primary, updated_at = @updated_at::timestamptz
WHERE id = @id AND customer_id = @customer_id
RETURNING id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at;

-- name: CustomerHasInvoiceAddress :one
-- CustomerHasInvoiceAddress is billing_profile.go's own read (invoice-ready
-- customer design D4's no_invoice_address warning): true when the customer
-- has a primary invoice address or, lacking that, a primary postal one —
-- the same resolution order D3 defines for "the invoice address" everywhere
-- else. At most one primary per (customer, type) can ever exist (D3's
-- partial unique index), so this is a plain existence check, never a
-- priority pick between two candidate rows.
SELECT EXISTS (
    SELECT 1 FROM customers.customer_addresses
    WHERE customer_id = @customer_id AND is_primary AND type IN ('invoice', 'postal')
);

-- name: DeleteCustomerAddress :exec
-- DeleteCustomerAddress is DeleteCustomersByIdAddressesByAddressId's write,
-- scoped to the customer the same way GetCustomerAddress is.
DELETE FROM customers.customer_addresses WHERE id = @id AND customer_id = @customer_id;
