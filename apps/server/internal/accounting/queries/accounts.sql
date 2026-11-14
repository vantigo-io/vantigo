-- name: InsertAccount :one
INSERT INTO accounting.accounts (number, name, kind, default_tax_code_id, system_role, created_at, updated_at)
VALUES (sqlc.arg(number), sqlc.arg(name), sqlc.arg(kind), sqlc.narg(default_tax_code_id), sqlc.narg(system_role),
    sqlc.arg(now), sqlc.arg(now))
RETURNING id;

-- name: ListAccounts :many
-- ListAccounts is the chart by number, each with whether it has postings.
SELECT a.id, a.number, a.name, a.kind, a.default_tax_code_id, a.active, a.system_role, a.revision,
    EXISTS (SELECT 1 FROM accounting.postings p WHERE p.account_id = a.id) AS has_postings
FROM accounting.accounts a
WHERE (sqlc.narg(active)::boolean IS NULL OR a.active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR a.number LIKE sqlc.narg(q)::text || '%'
       OR a.name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY a.number;

-- name: GetAccount :one
SELECT a.id, a.number, a.name, a.kind, a.default_tax_code_id, a.active, a.system_role, a.revision,
    EXISTS (SELECT 1 FROM accounting.postings p WHERE p.account_id = a.id) AS has_postings
FROM accounting.accounts a WHERE a.id = sqlc.arg(id);

-- name: LockAccount :one
SELECT id, number, kind, system_role, revision FROM accounting.accounts WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: AccountNumberTaken :one
SELECT EXISTS (SELECT 1 FROM accounting.accounts WHERE number = sqlc.arg(number) AND id <> sqlc.arg(id))::boolean;

-- name: UpdateAccount :exec
UPDATE accounting.accounts
SET number = sqlc.arg(number), name = sqlc.arg(name), kind = sqlc.arg(kind),
    default_tax_code_id = sqlc.narg(default_tax_code_id), active = sqlc.arg(active),
    updated_at = sqlc.arg(now), revision = revision + 1
WHERE id = sqlc.arg(id);

-- name: DeleteAccount :exec
DELETE FROM accounting.accounts WHERE id = sqlc.arg(id);

-- name: ClearRole :exec
UPDATE accounting.accounts SET system_role = NULL, updated_at = sqlc.arg(now), revision = revision + 1
WHERE system_role = sqlc.arg(role);

-- name: SetRole :exec
UPDATE accounting.accounts SET system_role = sqlc.arg(role), updated_at = sqlc.arg(now), revision = revision + 1
WHERE id = sqlc.arg(id);

-- name: AccountsForPosting :many
-- AccountsForPosting is what a post checks of each account it names: active,
-- kind and role.
SELECT id, number, name, kind, active, system_role FROM accounting.accounts WHERE id = ANY(sqlc.arg(ids)::int[]);

-- name: RoleAccounts :many
SELECT id, system_role::text AS role FROM accounting.accounts WHERE system_role IS NOT NULL;

-- name: SetDefaultTaxCode :exec
UPDATE accounting.accounts SET default_tax_code_id = sqlc.arg(tax_code_id) WHERE id = sqlc.arg(id);

-- name: ListGroupings :many
SELECT * FROM accounting.account_groupings
WHERE (sqlc.narg(account_id)::int IS NULL OR account_id = sqlc.narg(account_id)::int)
ORDER BY account_id, from_year;

-- name: DeleteGroupings :exec
DELETE FROM accounting.account_groupings WHERE account_id = sqlc.arg(account_id);

-- name: InsertGrouping :exec
INSERT INTO accounting.account_groupings (account_id, from_year, category, code)
VALUES (sqlc.arg(account_id), sqlc.arg(from_year), sqlc.arg(category), sqlc.arg(code));
