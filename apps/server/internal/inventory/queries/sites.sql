-- Sites (design I1, I6): the warehouses and stores, each the root of a
-- location tree. Every structural write locks the site row first (§4), so tree
-- changes in one site are serial.

-- name: ListSites :many
-- ListSites is every site in one status, by code, with its root location.
SELECT s.id, s.code, s.name, s.kind, s.address_line1, s.address_line2, s.postal_code, s.city, s.country,
       s.status, s.revision, s.created_at, s.updated_at, s.fulfils_orders, r.id AS root_location_id
FROM inventory.sites s
JOIN inventory.locations r ON r.site_id = s.id AND r.parent_id IS NULL
WHERE s.status = @status
ORDER BY s.code;

-- name: GetSite :one
-- GetSite is one site with its root location.
SELECT s.id, s.code, s.name, s.kind, s.address_line1, s.address_line2, s.postal_code, s.city, s.country,
       s.status, s.revision, s.created_at, s.updated_at, s.fulfils_orders, r.id AS root_location_id
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
INSERT INTO inventory.sites (code, name, kind, address_line1, address_line2, postal_code, city, country, status, fulfils_orders,
                             created_at, updated_at)
VALUES (@code, @name, @kind, sqlc.narg(address_line1), sqlc.narg(address_line2), sqlc.narg(postal_code), sqlc.narg(city),
        sqlc.narg(country), 'active', @fulfils_orders, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateSite :one
-- UpdateSite replaces a site's fields and moves its revision on.
UPDATE inventory.sites
SET code = @code, name = @name, kind = @kind, address_line1 = sqlc.narg(address_line1), address_line2 = sqlc.narg(address_line2),
    postal_code = sqlc.narg(postal_code), city = sqlc.narg(city), country = sqlc.narg(country), status = @status,
    fulfils_orders = @fulfils_orders, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SiteHasMovements :one
-- SiteHasMovements reports whether any movement was ever posted, or any
-- count opened, in the site: a site with history is archived, never deleted
-- (I6).
SELECT EXISTS (
    SELECT 1 FROM inventory.movements m WHERE m.site_id = @site_id::bigint
    UNION ALL
    SELECT 1 FROM inventory.counts c WHERE c.site_id = @site_id::bigint
);

-- name: SiteHasOpenCount :one
-- SiteHasOpenCount reports whether a count is still counting in the site, so
-- archiving it is refused (location_has_open_count).
SELECT EXISTS (SELECT 1 FROM inventory.counts WHERE site_id = @site_id AND status = 'counting');

-- name: SiteFigures :many
-- SiteFigures is, per site, its active locations below the root, its
-- balances and its latest receipt (the movement's own time), in one statement
-- for the whole list.
SELECT s.id,
       (SELECT count(*) FROM inventory.locations l WHERE l.site_id = s.id AND l.status = 'active' AND l.parent_id IS NOT NULL)::int AS location_count,
       (SELECT count(*) FROM inventory.balances b JOIN inventory.locations l ON l.id = b.location_id WHERE l.site_id = s.id)::int AS stock_lines,
       r.posted_at AS last_receipt_at
FROM inventory.sites s
LEFT JOIN inventory.movements r ON r.id = (
    SELECT m.id FROM inventory.movements m WHERE m.site_id = s.id AND m.kind = 'receipt' ORDER BY m.posted_at DESC, m.id DESC LIMIT 1)
WHERE s.id = ANY(@ids::bigint[]);

-- name: DeleteSite :exec
DELETE FROM inventory.sites WHERE id = @id;
