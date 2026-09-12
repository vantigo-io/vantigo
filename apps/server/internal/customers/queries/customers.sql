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
