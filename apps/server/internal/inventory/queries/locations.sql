-- Locations (design I2–I6, I21): the nodes of a site's tree. X's subtree is
-- id = X OR ancestors @> {X}; full_code and ancestors are rewritten for a
-- whole subtree in the transaction that changes a code or a parent.

-- name: GetLocation :one
SELECT * FROM inventory.locations WHERE id = @id;

-- name: GetRootLocation :one
-- GetRootLocation is the site's root, its one location without a parent.
SELECT * FROM inventory.locations WHERE site_id = @site_id AND parent_id IS NULL;

-- name: LocationByFullCode :one
-- LocationByFullCode is the lookup's second match (I28); full codes are
-- stored upper-case, so the caller upper-cases what it looks for.
SELECT * FROM inventory.locations WHERE full_code = @full_code;

-- name: LocationsByIDs :many
SELECT * FROM inventory.locations WHERE id = ANY(@ids::bigint[]);

-- name: ShareLocation :one
-- ShareLocation takes FOR SHARE on a create's parent (§4), so it cannot be
-- archived, moved or deleted under the new child.
SELECT * FROM inventory.locations WHERE id = @id FOR SHARE;

-- name: LockSubtree :many
-- LockSubtree takes FOR UPDATE on the location, every location below it and
-- the locations in also (a re-parent's new parent), in id order (§4).
SELECT * FROM inventory.locations
WHERE id = @id OR ancestors @> ARRAY[@id::bigint] OR id = ANY(@also::bigint[])
ORDER BY id
FOR UPDATE;

-- name: LockSiteLocations :many
-- LockSiteLocations takes FOR UPDATE on every location of a site, in id
-- order: a site's code change, archive or delete touches them all (§4).
SELECT * FROM inventory.locations WHERE site_id = @site_id ORDER BY id FOR UPDATE;

-- name: ListSiteLocations :many
-- ListSiteLocations is a site's tree, flat, by full code, filtered by a text
-- its full code or name contains, ignoring case; one row past the cap of
-- 10,000 is read so the caller can say the list was cut.
SELECT * FROM inventory.locations
WHERE site_id = @site_id AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(q)::text IS NULL OR strpos(lower(full_code), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(name, '')), lower(sqlc.narg(q)::text)) > 0)
ORDER BY full_code
LIMIT 10001;

-- name: ListChildren :many
SELECT * FROM inventory.locations WHERE parent_id = @parent_id ORDER BY sort_order, code;

-- name: SiblingCodeTaken :one
-- SiblingCodeTaken reports whether another child of the parent has the code.
SELECT EXISTS (SELECT 1 FROM inventory.locations WHERE parent_id = @parent_id AND code = @code AND id <> @id);

-- name: TakenFullCodes :many
-- TakenFullCodes is which of the full codes a location outside excluded holds,
-- by full code.
SELECT full_code FROM inventory.locations
WHERE full_code = ANY(@full_codes::text[]) AND NOT (id = ANY(@excluded::bigint[]))
ORDER BY full_code;

-- name: InsertLocation :one
INSERT INTO inventory.locations (site_id, parent_id, kind, code, name, sort_order, status, full_code, ancestors, depth, created_at, updated_at)
VALUES (@site_id, sqlc.narg(parent_id), @kind, @code, sqlc.narg(name), @sort_order, 'active', @full_code, @ancestors::bigint[],
        @depth, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: InsertChildren :many
-- InsertChildren is one level of a bulk create (I5): each new location under
-- its parent, its ancestors and depth taken from the parent's row.
INSERT INTO inventory.locations (site_id, parent_id, kind, code, sort_order, status, full_code, ancestors, depth, created_at, updated_at)
SELECT p.site_id, p.id, u.kind, u.code, 0, 'active', u.full_code, p.ancestors || p.id, p.depth + 1,
       @now::timestamptz, @now::timestamptz
FROM (SELECT unnest(@parent_ids::bigint[]) AS parent_id, unnest(@kinds::text[]) AS kind, unnest(@codes::text[]) AS code,
             unnest(@full_codes::text[]) AS full_code) AS u
JOIN inventory.locations p ON p.id = u.parent_id
ORDER BY u.full_code
RETURNING *;

-- name: UpdateLocation :one
-- UpdateLocation replaces one location's fields, its place in the tree
-- included, and moves its revision on.
UPDATE inventory.locations
SET parent_id = sqlc.narg(parent_id), kind = @kind, code = @code, name = sqlc.narg(name), sort_order = @sort_order,
    status = @status, full_code = @full_code, ancestors = @ancestors::bigint[], depth = @depth,
    revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: RewriteDescendants :exec
-- RewriteDescendants follows a location's new code or parent below it (I4,
-- I21): every descendant's full code re-prefixed, its ancestors re-rooted
-- from the location down, and its depth shifted.
UPDATE inventory.locations
SET full_code = @new_full_code::text || substr(full_code, length(@old_full_code::text) + 1),
    ancestors = @new_ancestors::bigint[] || ARRAY(
        SELECT a.id FROM unnest(ancestors) WITH ORDINALITY AS a (id, n) WHERE a.n > @old_ancestor_count::int ORDER BY a.n),
    depth = depth + @depth_delta::int,
    revision = revision + 1, updated_at = @now::timestamptz
WHERE ancestors @> ARRAY[@id::bigint];

-- name: RecodeSite :exec
-- RecodeSite follows a site's new code through its tree (I1, I4): the root's
-- code, and every full code's first segment.
UPDATE inventory.locations
SET code = CASE WHEN parent_id IS NULL THEN @new_code::text ELSE code END,
    full_code = @new_code::text || substr(full_code, length(@old_code::text) + 1),
    revision = revision + 1, updated_at = @now::timestamptz
WHERE site_id = @site_id;

-- name: ArchiveSubtree :exec
-- ArchiveSubtree archives the location and every active location below it (I6).
UPDATE inventory.locations SET status = 'archived', revision = revision + 1, updated_at = @now::timestamptz
WHERE (id = @id OR ancestors @> ARRAY[@id::bigint]) AND status = 'active';

-- name: SetSiteLocationsStatus :exec
-- SetSiteLocationsStatus archives every location of a site, or reactivates
-- its root alone (I6), by the ids given.
UPDATE inventory.locations SET status = @status, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]) AND status <> @status;

-- name: StockIn :one
-- StockIn is what lies on the locations — a subtree, or a site's — summed over
-- every variant, and in how many stock lines: a variant on a location, its
-- lots counted once (I6's location_has_stock).
SELECT coalesce(sum(on_hand), 0)::text AS quantity, count(DISTINCT (variant_id, location_id))::int AS lines
FROM inventory.balances WHERE location_id = ANY(@ids::bigint[]);

-- name: HistoryIn :one
-- HistoryIn reports whether a movement line or a count ever named one of the
-- locations: one that has is archived, never deleted (I6).
SELECT EXISTS (
    SELECT 1 FROM inventory.movement_lines
    WHERE from_location_id = ANY(@ids::bigint[]) OR to_location_id = ANY(@ids::bigint[])
    UNION ALL
    SELECT 1 FROM inventory.counts WHERE scope_location_id = ANY(@ids::bigint[])
    UNION ALL
    SELECT 1 FROM inventory.count_lines WHERE location_id = ANY(@ids::bigint[])
);

-- name: SubtreeHasOpenCount :one
-- SubtreeHasOpenCount reports whether a count still counting covers any of
-- the locations — its scope one of them, or above them (ancestors) — so an
-- archive of them is refused (location_has_open_count).
SELECT EXISTS (
    SELECT 1 FROM inventory.counts
    WHERE status = 'counting' AND scope_location_id = ANY(@ids::bigint[])
);

-- name: ActiveSubtrees :many
-- ActiveSubtrees is the locations and every active location below any of
-- them, by full code: what a sheet of labels prints with subtree (I7).
SELECT * FROM inventory.locations
WHERE id = ANY(@ids::bigint[]) OR (status = 'active' AND ancestors && @ids::bigint[])
ORDER BY full_code
LIMIT @row_limit::int;

-- name: SiteLocationsByFullCodes :many
-- SiteLocationsByFullCodes is the site's locations holding the full codes, as
-- the opening-balance import resolves its rows (I32).
SELECT * FROM inventory.locations WHERE site_id = @site_id AND full_code = ANY(@full_codes::text[]);

-- name: DeleteLocations :exec
-- DeleteLocations removes locations in one statement, so the tree's own key
-- is checked once the whole subtree is gone.
DELETE FROM inventory.locations WHERE id = ANY(@ids::bigint[]);
