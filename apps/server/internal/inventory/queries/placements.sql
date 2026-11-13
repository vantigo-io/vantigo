-- Placements (phase 2 design I54–I62): per variant and site, the primary
-- location, the reorder point and the maximum, with the variant's SKU and
-- name snapshotted for the low-stock list's order and search. A placement
-- with nothing set is deleted, never stored (the table's check). The
-- low-stock statement is one statement within the schema, its cost the
-- placements with a reorder point (I58).

-- name: GetPlacement :one
SELECT * FROM inventory.placements WHERE variant_id = @variant_id AND site_id = @site_id;

-- name: LockPlacement :one
-- LockPlacement takes the placement FOR UPDATE, for a change or a removal
-- judged by If-Match (I61).
SELECT * FROM inventory.placements WHERE variant_id = @variant_id AND site_id = @site_id FOR UPDATE;

-- name: InsertPlacement :one
-- InsertPlacement creates a placement (If-None-Match: *, I61). One already
-- there answers no row, which is revision_conflict.
INSERT INTO inventory.placements (variant_id, site_id, primary_location_id, reorder_point, maximum, multiple, sku, product_name,
                                  updated_by_user_id, updated_by_display, updated_at)
VALUES (@variant_id, @site_id, sqlc.narg(primary_location_id), sqlc.narg(reorder_point), sqlc.narg(maximum), sqlc.narg(multiple), @sku,
        @product_name, sqlc.narg(updated_by_user_id), sqlc.narg(updated_by_display), @now::timestamptz)
ON CONFLICT (variant_id, site_id) DO NOTHING
RETURNING *;

-- name: UpdatePlacement :one
-- UpdatePlacement replaces a placement's fields and its snapshot, and
-- moves its revision on (If-Match, I61).
UPDATE inventory.placements
SET primary_location_id = sqlc.narg(primary_location_id), reorder_point = sqlc.narg(reorder_point),
    maximum = sqlc.narg(maximum), multiple = sqlc.narg(multiple), sku = @sku, product_name = @product_name,
    updated_by_user_id = sqlc.narg(updated_by_user_id), updated_by_display = sqlc.narg(updated_by_display),
    revision = revision + 1, updated_at = @now::timestamptz
WHERE variant_id = @variant_id AND site_id = @site_id
RETURNING *;

-- name: DeletePlacement :execrows
DELETE FROM inventory.placements WHERE variant_id = @variant_id AND site_id = @site_id;

-- name: PlacementsOfVariants :many
-- PlacementsOfVariants is the placements of the variants given at every
-- site: the product's and the variant's stock, and I53's batch.
SELECT * FROM inventory.placements
WHERE variant_id = ANY(@variant_ids::int[])
ORDER BY variant_id, site_id;

-- name: PrimaryLocationsAt :many
-- PrimaryLocationsAt is, per variant given that has one, its primary
-- location at the site: the variant search's primaryLocationId (I55).
SELECT variant_id, primary_location_id::bigint AS primary_location_id
FROM inventory.placements
WHERE site_id = @site_id AND variant_id = ANY(@variant_ids::int[]) AND primary_location_id IS NOT NULL
ORDER BY variant_id;

-- name: DeletePrimaryOnlyPlacementsIn :many
-- DeletePrimaryOnlyPlacementsIn removes the placements whose only field is a
-- primary location at one of the locations given or below them: archiving
-- or deleting the location clears it (I55), and a placement with nothing
-- set is never stored. Run before ClearPrimaryLocationsIn; each row
-- answered is a placement.cleared event. A location's delete runs both
-- before the row goes, since the foreign key's set null would leave such a
-- placement empty and fail the table's check.
DELETE FROM inventory.placements p
WHERE p.primary_location_id IN (
        SELECT l.id FROM inventory.locations l
        WHERE l.id = ANY(@location_ids::bigint[]) OR l.ancestors && @location_ids::bigint[])
  AND p.reorder_point IS NULL AND p.maximum IS NULL
RETURNING p.variant_id, p.site_id, p.primary_location_id::bigint AS primary_location_id;

-- name: ClearPrimaryLocationsIn :many
-- ClearPrimaryLocationsIn sets the primary location null on every placement
-- naming one of the locations given or one below them, moving its revision
-- on (I55); each row answered, with the primary location it had, is a
-- placement_cleared event.
UPDATE inventory.placements p
SET primary_location_id = NULL, revision = p.revision + 1, updated_at = @now::timestamptz
FROM inventory.placements prev
WHERE prev.variant_id = p.variant_id AND prev.site_id = p.site_id
  AND p.primary_location_id IN (
        SELECT l.id FROM inventory.locations l
        WHERE l.id = ANY(@location_ids::bigint[]) OR l.ancestors && @location_ids::bigint[])
RETURNING p.variant_id, p.site_id, prev.primary_location_id::bigint AS primary_location_id;

-- name: LowStock :many
-- LowStock is the low-stock list (I56, I58): every placement with a reorder
-- point at an active site whose available (usable on hand over the site's
-- locations — no lot held or expired on today, manufacturing phase 2 M63 —
-- less reserved at the site) plus incoming (in transit to the
-- site) is at or below it — optionally one site, q a part of the SKU or
-- name snapshot, ignoring case — by site code, SKU snapshot and variant id,
-- after the cursor's. Quantities as text, for the rule's arithmetic in
-- decimals.
SELECT p.variant_id, p.site_id, s.code AS site_code, p.sku, p.product_name,
       p.reorder_point::text AS reorder_point, p.maximum, p.multiple,
       oh.on_hand::text AS on_hand, rs.reserved::text AS reserved, it.incoming::text AS incoming
FROM inventory.placements p
JOIN inventory.sites s ON s.id = p.site_id
CROSS JOIN LATERAL (
    SELECT coalesce(sum(b.on_hand), 0)::numeric AS on_hand
    FROM inventory.balances b
    JOIN inventory.locations l ON l.id = b.location_id
    LEFT JOIN inventory.lots t ON t.id = b.lot_id
    WHERE b.variant_id = p.variant_id AND l.site_id = p.site_id
      AND (b.lot_id IS NULL OR (t.status = 'available' AND (t.expiry IS NULL OR t.expiry >= @today::date)))) oh
CROSS JOIN LATERAL (
    SELECT coalesce(sum(r.quantity), 0)::numeric AS reserved
    FROM inventory.reservations r
    WHERE r.variant_id = p.variant_id AND r.site_id = p.site_id) rs
CROSS JOIN LATERAL (
    SELECT coalesce(sum(tl.sent - tl.received - tl.missing - tl.returned), 0)::numeric AS incoming
    FROM inventory.transfer_lines tl
    JOIN inventory.transfers t ON t.id = tl.transfer_id
    WHERE tl.variant_id = p.variant_id AND t.to_site_id = p.site_id
      AND tl.sent > tl.received + tl.missing + tl.returned) it
WHERE p.reorder_point IS NOT NULL
  AND s.status = 'active'
  AND (sqlc.narg(site_id)::bigint IS NULL OR p.site_id = sqlc.narg(site_id)::bigint)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(p.sku), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(p.product_name), lower(sqlc.narg(q)::text)) > 0)
  AND oh.on_hand - rs.reserved + it.incoming <= p.reorder_point
  AND (sqlc.narg(cursor_site_code)::text IS NULL
       OR (s.code, p.sku, p.variant_id) > (sqlc.narg(cursor_site_code)::text, sqlc.narg(cursor_sku)::text, sqlc.narg(cursor_variant_id)::int))
ORDER BY s.code, p.sku, p.variant_id
LIMIT @row_limit::int;

-- name: LowStockCounts :many
-- LowStockCounts is LowStock's rule counted per active site, for the
-- dashboard's lowStock items (I62): only sites with at least one.
SELECT p.site_id, s.code AS site_code, s.name AS site_name, count(*)::int AS low
FROM inventory.placements p
JOIN inventory.sites s ON s.id = p.site_id
CROSS JOIN LATERAL (
    SELECT coalesce(sum(b.on_hand), 0)::numeric AS on_hand
    FROM inventory.balances b
    JOIN inventory.locations l ON l.id = b.location_id
    LEFT JOIN inventory.lots t ON t.id = b.lot_id
    WHERE b.variant_id = p.variant_id AND l.site_id = p.site_id
      AND (b.lot_id IS NULL OR (t.status = 'available' AND (t.expiry IS NULL OR t.expiry >= @today::date)))) oh
CROSS JOIN LATERAL (
    SELECT coalesce(sum(r.quantity), 0)::numeric AS reserved
    FROM inventory.reservations r
    WHERE r.variant_id = p.variant_id AND r.site_id = p.site_id) rs
CROSS JOIN LATERAL (
    SELECT coalesce(sum(tl.sent - tl.received - tl.missing - tl.returned), 0)::numeric AS incoming
    FROM inventory.transfer_lines tl
    JOIN inventory.transfers t ON t.id = tl.transfer_id
    WHERE tl.variant_id = p.variant_id AND t.to_site_id = p.site_id
      AND tl.sent > tl.received + tl.missing + tl.returned) it
WHERE p.reorder_point IS NOT NULL
  AND s.status = 'active'
  AND oh.on_hand - rs.reserved + it.incoming <= p.reorder_point
GROUP BY p.site_id, s.code, s.name
ORDER BY s.code;

-- name: AvailableAtActiveSites :many
-- AvailableAtActiveSites is, per variant given, its available (on hand less
-- reserved) at every active site where it lies or is reserved, by site code:
-- the low-stock rows' elsewhere (I58), the row's own site left out by the
-- caller. A site without a placement is answered too.
WITH oh AS (
    SELECT b.variant_id, l.site_id, sum(b.on_hand) AS on_hand
    FROM inventory.balances b
    JOIN inventory.locations l ON l.id = b.location_id
    LEFT JOIN inventory.lots t ON t.id = b.lot_id
    WHERE b.variant_id = ANY(@variant_ids::int[])
      AND (b.lot_id IS NULL OR (t.status = 'available' AND (t.expiry IS NULL OR t.expiry >= @today::date)))
    GROUP BY b.variant_id, l.site_id
), rs AS (
    SELECT r.variant_id, r.site_id, sum(r.quantity) AS reserved
    FROM inventory.reservations r
    WHERE r.variant_id = ANY(@variant_ids::int[])
    GROUP BY r.variant_id, r.site_id
)
SELECT coalesce(oh.variant_id, rs.variant_id)::int AS variant_id, s.id AS site_id, s.code AS site_code, s.name AS site_name,
       (coalesce(oh.on_hand, 0) - coalesce(rs.reserved, 0))::text AS available
FROM oh
FULL JOIN rs ON rs.variant_id = oh.variant_id AND rs.site_id = oh.site_id
JOIN inventory.sites s ON s.id = coalesce(oh.site_id, rs.site_id)
WHERE s.status = 'active'
ORDER BY 1, s.code;

-- name: OpenDraftsTo :many
-- OpenDraftsTo is, per (variant, site) pair given — the arrays read
-- pairwise — the numbers of the draft transfers to the site with a line of
-- the variant (I59's Draft N open).
SELECT DISTINCT u.variant_id::int AS variant_id, u.site_id::bigint AS site_id, t.number
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@site_ids::bigint[]) AS site_id) AS u
JOIN inventory.transfers t ON t.to_site_id = u.site_id AND t.status = 'draft'
JOIN inventory.transfer_lines l ON l.transfer_id = t.id AND l.variant_id = u.variant_id
ORDER BY 1, 2, 3;

-- name: RefreshPlacementSnapshots :exec
-- RefreshPlacementSnapshots writes the catalog's current SKU and name over
-- the snapshots that differ, for the variants given (I58), sorted by variant
-- id: a page's catalog read found them changed. Not a change to the
-- placement: the revision stays.
UPDATE inventory.placements p
SET sku = u.sku, product_name = u.product_name
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@skus::text[]) AS sku,
             unnest(@product_names::text[]) AS product_name) AS u
WHERE p.variant_id = u.variant_id AND (p.sku <> u.sku OR p.product_name <> u.product_name);

-- name: UpsertPlacements :exec
-- UpsertPlacements is the placements import's commit (I61): each row sets
-- all four fields, last wins, bumping the revision of one already there.
-- The arrays are read row by row; a primary location of 0 is none, and a
-- null reorder point or maximum clears it. Rows clearing all three go to
-- DeletePlacementsOf instead.
INSERT INTO inventory.placements (variant_id, site_id, primary_location_id, reorder_point, maximum, multiple, sku, product_name,
                                  updated_by_user_id, updated_by_display, updated_at)
SELECT u.variant_id, u.site_id, nullif(u.primary_location_id, 0), u.reorder_point, u.maximum, u.multiple, u.sku, u.product_name,
       sqlc.narg(updated_by_user_id), sqlc.narg(updated_by_display), @now::timestamptz
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@site_ids::bigint[]) AS site_id,
             unnest(@primary_location_ids::bigint[]) AS primary_location_id,
             unnest(@reorder_points::numeric[]) AS reorder_point, unnest(@maximums::numeric[]) AS maximum,
             unnest(@multiples::numeric[]) AS multiple, unnest(@skus::text[]) AS sku, unnest(@product_names::text[]) AS product_name) AS u
ON CONFLICT (variant_id, site_id) DO UPDATE
SET primary_location_id = excluded.primary_location_id, reorder_point = excluded.reorder_point,
    maximum = excluded.maximum, multiple = excluded.multiple, sku = excluded.sku, product_name = excluded.product_name,
    updated_by_user_id = excluded.updated_by_user_id, updated_by_display = excluded.updated_by_display,
    revision = inventory.placements.revision + 1, updated_at = excluded.updated_at;

-- name: DeletePlacementsOf :many
-- DeletePlacementsOf removes the placements of the (variant, site) pairs
-- given, read pairwise: the import's rows clearing all three fields (I61),
-- given sorted by (variant, site). Each row answered is one that was there,
-- a placement_cleared event the import writes.
DELETE FROM inventory.placements p
USING (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@site_ids::bigint[]) AS site_id) AS u
WHERE p.variant_id = u.variant_id AND p.site_id = u.site_id
RETURNING p.variant_id, p.site_id;

-- name: PlacementImportSeen :one
-- PlacementImportSeen is the event of a placements file of this SHA-256
-- committed before (I61's idempotency by the file), whose payload carries
-- what that commit set and cleared: entity kind placement, kind
-- placements_imported, entity id the hash's first eight bytes as a bigint
-- (the entity index's key) and the full hash in its payload's sha256. No
-- row when the file is new.
SELECT payload FROM inventory.events
WHERE entity_kind = 'placement' AND entity_id = @hash_key::bigint AND kind = 'placements_imported'
  AND payload ->> 'sha256' = @sha256::text
LIMIT 1;

-- name: LockPlacementImports :exec
-- LockPlacementImports serialises the placements imports (I61): a
-- transaction-scoped advisory lock on one constant key, taken first, so two
-- commits never write the same placements in different orders, and the
-- same file committed twice at once is written once.
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: SitesByCodes :many
-- SitesByCodes is the sites the placements import's rows name by code (I61).
SELECT * FROM inventory.sites WHERE code = ANY(@codes::text[]);

-- name: LocationsByFullCodes :many
-- LocationsByFullCodes is the locations the placements import's rows name by
-- full code, at any site (I61); a full code is unique in the deployment.
SELECT * FROM inventory.locations WHERE full_code = ANY(@full_codes::text[]);

-- name: StockPlacementsAt :many
-- StockPlacementsAt is contracts.Stock.Placements (manufacturing phase 2
-- M106): every placement with a reorder point at the site, if it is active,
-- with LowStock's figures — usable on hand on today, reserved and incoming —
-- by variant id, at most row_limit.
SELECT p.variant_id, p.reorder_point::text AS reorder_point, p.maximum, p.multiple,
       oh.on_hand::text AS on_hand, rs.reserved::text AS reserved, it.incoming::text AS incoming
FROM inventory.placements p
JOIN inventory.sites s ON s.id = p.site_id
CROSS JOIN LATERAL (
    SELECT coalesce(sum(b.on_hand), 0)::numeric AS on_hand
    FROM inventory.balances b
    JOIN inventory.locations l ON l.id = b.location_id
    LEFT JOIN inventory.lots t ON t.id = b.lot_id
    WHERE b.variant_id = p.variant_id AND l.site_id = p.site_id
      AND (b.lot_id IS NULL OR (t.status = 'available' AND (t.expiry IS NULL OR t.expiry >= @today::date)))) oh
CROSS JOIN LATERAL (
    SELECT coalesce(sum(r.quantity), 0)::numeric AS reserved
    FROM inventory.reservations r
    WHERE r.variant_id = p.variant_id AND r.site_id = p.site_id) rs
CROSS JOIN LATERAL (
    SELECT coalesce(sum(tl.sent - tl.received - tl.missing - tl.returned), 0)::numeric AS incoming
    FROM inventory.transfer_lines tl
    JOIN inventory.transfers t ON t.id = tl.transfer_id
    WHERE tl.variant_id = p.variant_id AND t.to_site_id = p.site_id
      AND tl.sent > tl.received + tl.missing + tl.returned) it
WHERE p.site_id = @site_id AND p.reorder_point IS NOT NULL AND s.status = 'active'
ORDER BY p.variant_id
LIMIT @row_limit::int;
