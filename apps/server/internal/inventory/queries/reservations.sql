-- Reservations (orders design O45): stock promised to another module's
-- document lines, per variant and site, written only through
-- contracts.StockLedger on the caller's transaction (ledger.go). Every write
-- locks the site's balances of its variants first, in (variant, location)
-- order, then the reservations.

-- name: LockSiteBalances :many
-- LockSiteBalances takes FOR UPDATE on the variants' balances at the site's
-- locations, in (variant, location) order: two reserves of one variant at
-- one site serialise here, as a posting and a reserve do.
SELECT b.variant_id, b.location_id
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE b.variant_id = ANY(@variant_ids::int[]) AND l.site_id = @site_id
ORDER BY b.variant_id, b.location_id
FOR UPDATE OF b;

-- name: SiteStockOf :many
-- SiteStockOf is, per variant, its on hand at the site's locations and what
-- is reserved of it at the site, read after the balances are locked.
SELECT u.variant_id::int AS variant_id,
       coalesce((SELECT sum(b.on_hand) FROM inventory.balances b JOIN inventory.locations l ON l.id = b.location_id
                 WHERE b.variant_id = u.variant_id AND l.site_id = @site_id), 0)::text AS on_hand,
       coalesce((SELECT sum(r.quantity) FROM inventory.reservations r
                 WHERE r.variant_id = u.variant_id AND r.site_id = @site_id), 0)::text AS reserved
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id) AS u
ORDER BY 1;

-- name: AddReservations :exec
-- AddReservations adds each line's quantity to its reservation at the site,
-- creating it at the quantity when the line holds none there. The caller
-- names each line once.
INSERT INTO inventory.reservations (variant_id, site_id, quantity, source_module, source_kind, source_id, source_line_id,
                                    created_at, updated_at)
SELECT u.variant_id, @site_id, u.quantity, @source_module, @source_kind, @source_id, u.line_id, @now::timestamptz, @now::timestamptz
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@line_ids::text[]) AS line_id,
             unnest(@quantities::numeric[]) AS quantity) AS u
ORDER BY u.line_id
ON CONFLICT (source_module, source_kind, source_id, source_line_id, site_id)
DO UPDATE SET quantity = inventory.reservations.quantity + excluded.quantity, updated_at = excluded.updated_at;

-- name: LockSourceReservations :many
-- LockSourceReservations takes FOR UPDATE on every reservation of the
-- document, in id order.
SELECT * FROM inventory.reservations
WHERE source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY id
FOR UPDATE;

-- name: SourceReservations :many
-- SourceReservations is every reservation of the document.
SELECT * FROM inventory.reservations
WHERE source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY id;

-- name: SetReservations :exec
-- SetReservations writes the quantities a release or an issue left.
UPDATE inventory.reservations r
SET quantity = u.quantity, updated_at = @now::timestamptz
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@quantities::numeric[]) AS quantity) AS u
WHERE r.id = u.id;

-- name: DeleteReservations :exec
-- DeleteReservations removes the reservations a release or an issue left at
-- zero.
DELETE FROM inventory.reservations WHERE id = ANY(@ids::bigint[]);
