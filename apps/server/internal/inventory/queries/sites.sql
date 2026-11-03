-- Sites (design I1, I6): the warehouses and stores, each the root of a
-- location tree. Every structural write locks the site row first (§4), so tree
-- changes in one site are serial.

-- name: ListSites :many
-- ListSites is every site in one status, by code, with its root location.
SELECT s.id, s.code, s.name, s.kind, s.address_line1, s.address_line2, s.postal_code, s.city, s.country,
       s.status, s.revision, s.created_at, s.updated_at, r.id AS root_location_id
FROM inventory.sites s
JOIN inventory.locations r ON r.site_id = s.id AND r.parent_id IS NULL
WHERE s.status = @status
ORDER BY s.code;

-- name: GetSite :one
-- GetSite is one site with its root location.
SELECT s.id, s.code, s.name, s.kind, s.address_line1, s.address_line2, s.postal_code, s.city, s.country,
       s.status, s.revision, s.created_at, s.updated_at, r.id AS root_location_id
FROM inventory.sites s
JOIN inventory.locations r ON r.site_id = s.id AND r.parent_id IS NULL
WHERE s.id = @id;

-- name: SitesByIDs :many
-- SitesByIDs is the sites a response names.
SELECT * FROM inventory.sites WHERE id = ANY(@ids::bigint[]);

-- name: LockSite :one
-- LockSite takes the site row FOR UPDATE: the first lock of every structural
-- write in the site (§4).
SELECT * FROM inventory.sites WHERE id = @id FOR UPDATE;

-- name: SiteCodeTaken :one
-- SiteCodeTaken reports whether another site has the code.
SELECT EXISTS (SELECT 1 FROM inventory.sites WHERE code = @code AND id <> @id);

-- name: InsertSite :one
INSERT INTO inventory.sites (code, name, kind, address_line1, address_line2, postal_code, city, country, status, created_at, updated_at)
VALUES (@code, @name, @kind, sqlc.narg(address_line1), sqlc.narg(address_line2), sqlc.narg(postal_code), sqlc.narg(city),
        sqlc.narg(country), 'active', @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateSite :one
-- UpdateSite replaces a site's fields and moves its revision on.
UPDATE inventory.sites
SET code = @code, name = @name, kind = @kind, address_line1 = sqlc.narg(address_line1), address_line2 = sqlc.narg(address_line2),
    postal_code = sqlc.narg(postal_code), city = sqlc.narg(city), country = sqlc.narg(country), status = @status,
    revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SiteHasMovements :one
-- SiteHasMovements reports whether any movement was ever posted in the site:
-- a site with history is archived, never deleted (I6).
SELECT EXISTS (SELECT 1 FROM inventory.movements WHERE site_id = @site_id);

-- name: DeleteSite :exec
DELETE FROM inventory.sites WHERE id = @id;
