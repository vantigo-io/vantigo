-- The reads (design I27, I29, I30): what lies where, and the movements.

-- name: BalancesAt :many
-- BalancesAt is what lies on the location and, with subtree, below it, per
-- variant and location, summed over the lots (manufacturing phase 2 M55).
SELECT b.variant_id, b.location_id, sum(b.on_hand)::numeric AS on_hand
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE l.id = @id OR (@subtree::boolean AND l.ancestors @> ARRAY[@id::bigint])
GROUP BY b.variant_id, b.location_id, l.full_code
ORDER BY l.full_code, b.variant_id;

-- name: BalancesOfVariants :many
-- BalancesOfVariants is every balance of the variants, per location, summed
-- over the lots (manufacturing phase 2 M55).
SELECT b.variant_id, b.location_id, sum(b.on_hand)::numeric AS on_hand
FROM inventory.balances b
WHERE b.variant_id = ANY(@variant_ids::int[])
GROUP BY b.variant_id, b.location_id
ORDER BY b.variant_id, b.location_id;

-- name: LastLineSnapshots :many
-- LastLineSnapshots is each variant's snapshot on its latest movement line:
-- how a variant the catalog no longer knows is named (§6).
SELECT DISTINCT ON (variant_id) variant_id, sku, product_name, unit
FROM inventory.movement_lines
WHERE variant_id = ANY(@variant_ids::int[])
ORDER BY variant_id, movement_id DESC, line_no DESC;

-- name: GetMovement :one
SELECT * FROM inventory.movements WHERE id = @id;

-- name: MovementLines :many
SELECT * FROM inventory.movement_lines WHERE movement_id = @movement_id ORDER BY line_no;

-- name: ListMovements :many
-- ListMovements is the movements newest first (I30), filtered by site, kind,
-- posting time, variant, lot (M74) and location subtree (each NULL for any), after the
-- cursor's (posted_at, id).
SELECT m.id, m.client_request_id, m.site_id, m.kind, m.reason, m.note, m.supplier_name, m.supplier_reference,
       m.posted_by_user_id, m.posted_by_display, m.posted_at, m.count_id,
       (SELECT count(*) FROM inventory.movement_lines c WHERE c.movement_id = m.id)::int AS line_count
FROM inventory.movements m
WHERE (sqlc.narg(site_id)::bigint IS NULL OR m.site_id = sqlc.narg(site_id)::bigint)
  AND (sqlc.narg(kind)::text IS NULL OR m.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(posted_from)::timestamptz IS NULL OR m.posted_at >= sqlc.narg(posted_from)::timestamptz)
  AND (sqlc.narg(posted_before)::timestamptz IS NULL OR m.posted_at < sqlc.narg(posted_before)::timestamptz)
  AND (sqlc.narg(variant_id)::int IS NULL OR EXISTS (
        SELECT 1 FROM inventory.movement_lines v WHERE v.movement_id = m.id AND v.variant_id = sqlc.narg(variant_id)::int))
  AND (sqlc.narg(lot_id)::bigint IS NULL OR EXISTS (
        SELECT 1 FROM inventory.movement_lines t WHERE t.movement_id = m.id AND t.lot_id = sqlc.narg(lot_id)::bigint))
  AND (sqlc.narg(location_id)::bigint IS NULL OR EXISTS (
        SELECT 1 FROM inventory.movement_lines ml
        JOIN inventory.locations l ON l.id = ml.from_location_id OR l.id = ml.to_location_id
        WHERE ml.movement_id = m.id
          AND (l.id = sqlc.narg(location_id)::bigint OR l.ancestors @> ARRAY[sqlc.narg(location_id)::bigint])))
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (m.posted_at, m.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::bigint))
ORDER BY m.posted_at DESC, m.id DESC
LIMIT @row_limit::int;

-- name: StockTotals :many
-- StockTotals is each of the variants' on hand across every location and
-- what is reserved of it across every site (I33); a variant with neither is
-- absent. With a lot, only that lot's on hand (M74); a reservation names no
-- lot, so none counts.
SELECT coalesce(b.variant_id, r.variant_id)::int AS variant_id, coalesce(b.on_hand, 0)::text AS on_hand,
       coalesce(r.reserved, 0)::text AS reserved
FROM (SELECT variant_id, sum(on_hand) AS on_hand FROM inventory.balances
      WHERE variant_id = ANY(@variant_ids::int[]) AND (sqlc.narg(lot_id)::bigint IS NULL OR lot_id = sqlc.narg(lot_id)::bigint)
      GROUP BY variant_id) b
FULL JOIN (SELECT variant_id, sum(quantity) AS reserved FROM inventory.reservations
           WHERE variant_id = ANY(@variant_ids::int[]) AND sqlc.narg(lot_id)::bigint IS NULL
           GROUP BY variant_id) r ON r.variant_id = b.variant_id
ORDER BY 1;

-- name: ReservedOfVariants :many
-- ReservedOfVariants is what is reserved of each variant, per site.
SELECT variant_id, site_id, sum(quantity)::text AS reserved
FROM inventory.reservations
WHERE variant_id = ANY(@variant_ids::int[])
GROUP BY variant_id, site_id
ORDER BY variant_id, site_id;

-- name: OnHandBySite :many
-- OnHandBySite is each variant's on hand per site, summed over its locations.
SELECT b.variant_id, l.site_id, sum(b.on_hand)::text AS on_hand
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE b.variant_id = ANY(@variant_ids::int[])
GROUP BY b.variant_id, l.site_id
ORDER BY b.variant_id, l.site_id;

-- name: UsableBySite :many
-- UsableBySite is each variant's usable on hand per site (manufacturing phase
-- 2 M63): the balances with no lot, and those of lots with no active hold that
-- are not expired on today. A site where nothing is usable is absent.
SELECT b.variant_id, l.site_id, sum(b.on_hand)::text AS on_hand
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
LEFT JOIN inventory.lots t ON t.id = b.lot_id
WHERE b.variant_id = ANY(@variant_ids::int[])
  AND (b.lot_id IS NULL OR (t.status = 'available' AND (t.expiry IS NULL OR t.expiry >= @today::date)))
GROUP BY b.variant_id, l.site_id
ORDER BY b.variant_id, l.site_id;
