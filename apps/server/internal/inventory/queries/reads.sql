-- The reads (design I27, I29, I30): what lies where, and the movements.

-- name: BalancesAt :many
-- BalancesAt is what lies on the location and, with subtree, below it.
SELECT b.variant_id, b.location_id, b.on_hand
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE l.id = @id OR (@subtree::boolean AND l.ancestors @> ARRAY[@id::bigint])
ORDER BY l.full_code, b.variant_id;

-- name: BalancesOfVariants :many
-- BalancesOfVariants is every balance of the variants.
SELECT b.variant_id, b.location_id, b.on_hand
FROM inventory.balances b
WHERE b.variant_id = ANY(@variant_ids::int[])
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
-- posting time, variant and location subtree (each NULL for any), after the
-- cursor's (posted_at, id).
SELECT m.id, m.client_request_id, m.site_id, m.kind, m.reason, m.note, m.supplier_name, m.supplier_reference,
       m.posted_by_user_id, m.posted_by_display, m.posted_at,
       (SELECT count(*) FROM inventory.movement_lines c WHERE c.movement_id = m.id)::int AS line_count
FROM inventory.movements m
WHERE (sqlc.narg(site_id)::bigint IS NULL OR m.site_id = sqlc.narg(site_id)::bigint)
  AND (sqlc.narg(kind)::text IS NULL OR m.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(posted_from)::timestamptz IS NULL OR m.posted_at >= sqlc.narg(posted_from)::timestamptz)
  AND (sqlc.narg(posted_before)::timestamptz IS NULL OR m.posted_at < sqlc.narg(posted_before)::timestamptz)
  AND (sqlc.narg(variant_id)::int IS NULL OR EXISTS (
        SELECT 1 FROM inventory.movement_lines v WHERE v.movement_id = m.id AND v.variant_id = sqlc.narg(variant_id)::int))
  AND (sqlc.narg(location_id)::bigint IS NULL OR EXISTS (
        SELECT 1 FROM inventory.movement_lines ml
        JOIN inventory.locations l ON l.id = ml.from_location_id OR l.id = ml.to_location_id
        WHERE ml.movement_id = m.id
          AND (l.id = sqlc.narg(location_id)::bigint OR l.ancestors @> ARRAY[sqlc.narg(location_id)::bigint])))
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (m.posted_at, m.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::bigint))
ORDER BY m.posted_at DESC, m.id DESC
LIMIT @row_limit::int;
