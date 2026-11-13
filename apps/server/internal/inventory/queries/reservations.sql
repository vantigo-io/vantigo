-- Reservations (orders design O45): stock promised to another module's
-- document lines, per variant and site, written only through
-- contracts.StockLedger on the caller's transaction (ledger.go). Every write
-- locks the site's balances of its variants first, in (variant, location)
-- order, then the reservations.

-- name: LockSiteBalances :many
-- LockSiteBalances takes FOR UPDATE on the variants' balances at the site's
-- locations, in (variant, location, lot) order with no lot first, as a
-- posting takes them (manufacturing phase 2 M58): two reserves of one variant
-- at one site serialise here, as a posting and a reserve do.
SELECT b.variant_id, b.location_id
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE b.variant_id = ANY(@variant_ids::int[]) AND l.site_id = @site_id
ORDER BY b.variant_id, b.location_id, b.lot_id NULLS FIRST
FOR UPDATE OF b;

-- name: SiteStockOf :many
-- SiteStockOf is, per variant, its usable on hand at the site's locations —
-- no lot, or a lot with no active hold that is not expired on today
-- (manufacturing phase 2 M63) — and what is reserved of it at the site, read
-- after the balances are locked.
SELECT u.variant_id::int AS variant_id,
       coalesce((SELECT sum(b.on_hand) FROM inventory.balances b JOIN inventory.locations l ON l.id = b.location_id
                 LEFT JOIN inventory.lots t ON t.id = b.lot_id
                 WHERE b.variant_id = u.variant_id AND l.site_id = @site_id
                   AND (b.lot_id IS NULL OR (t.status = 'available' AND (t.expiry IS NULL OR t.expiry >= @today::date)))), 0)::text AS on_hand,
       coalesce((SELECT sum(r.quantity) FROM inventory.reservations r
                 WHERE r.variant_id = u.variant_id AND r.site_id = @site_id), 0)::text AS reserved
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id) AS u
ORDER BY 1;

-- name: AddReservations :execrows
-- AddReservations adds each line's quantity to its reservation at the site,
-- creating it at the quantity when the line holds none there. The caller
-- names each line once. A line's reservation is of one variant: one held of
-- another variant is left as it is and not counted, so fewer rows than lines
-- tells the caller to refuse.
INSERT INTO inventory.reservations (variant_id, site_id, quantity, source_module, source_kind, source_id, source_line_id,
                                    created_at, updated_at)
SELECT u.variant_id, @site_id, u.quantity, @source_module, @source_kind, @source_id, u.line_id, @now::timestamptz, @now::timestamptz
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@line_ids::text[]) AS line_id,
             unnest(@quantities::numeric[]) AS quantity) AS u
ORDER BY u.line_id
ON CONFLICT (source_module, source_kind, source_id, source_line_id, site_id)
DO UPDATE SET quantity = inventory.reservations.quantity + excluded.quantity, updated_at = excluded.updated_at
WHERE inventory.reservations.variant_id = excluded.variant_id;

-- name: LockSourceReservations :many
-- LockSourceReservations takes FOR UPDATE on every reservation of the
-- document, in id order.
SELECT * FROM inventory.reservations
WHERE source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY id
FOR UPDATE;

-- name: SourceReservations :many
-- SourceReservations is every reservation of the document. It must stay
-- lock-free: StockLedger.ReservationsFor answers it to a caller's read-only
-- transaction (Orders' SalesOrderLines.Unreserved).
SELECT * FROM inventory.reservations
WHERE source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY id;

-- name: ReservationsOfSources :many
-- ReservationsOfSources is every reservation of the documents given, the
-- arrays read row by row, by id (StockLedger.ReservationsForSources). It
-- must stay lock-free, as SourceReservations.
SELECT r.* FROM inventory.reservations r
JOIN (SELECT DISTINCT unnest(@source_modules::text[]) AS source_module, unnest(@source_kinds::text[]) AS source_kind,
             unnest(@source_ids::text[]) AS source_id) AS s
  ON r.source_module = s.source_module AND r.source_kind = s.source_kind AND r.source_id = s.source_id
ORDER BY r.id;

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

-- name: StockSites :many
-- StockSites is every site that is not archived, by code, with its root
-- location (contracts.Stock's Sites).
SELECT s.id, s.code, s.name, s.fulfils_orders, s.status, r.id AS root_location_id
FROM inventory.sites s
JOIN inventory.locations r ON r.site_id = s.id AND r.parent_id IS NULL
WHERE s.status <> 'archived'
ORDER BY s.code;

-- name: StockSiteLocations :many
-- StockSiteLocations is every location of the site, archived ones too, by
-- full code (contracts.Stock's SiteLocations).
SELECT id, parent_id, kind, full_code, status FROM inventory.locations
WHERE site_id = @site_id
ORDER BY full_code;

-- name: StockLocationsAt :many
-- StockLocationsAt is, per variant, every active location of the site holding
-- more than 0 of it, by full code, summed over its lots (contracts.Stock's
-- Locations).
SELECT b.variant_id, b.location_id, l.full_code, sum(b.on_hand)::text AS on_hand
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE b.variant_id = ANY(@variant_ids::int[]) AND l.site_id = @site_id AND l.status = 'active' AND b.on_hand > 0
GROUP BY b.variant_id, b.location_id, l.full_code
ORDER BY b.variant_id, l.full_code, b.location_id;

-- name: StockLotsAt :many
-- StockLotsAt is, per variant, every lot holding more than 0 at an active
-- location of the site, with its location, in FEFO order: expiry (none last),
-- received, lot id, then the location's full code (contracts.Stock's Lots,
-- manufacturing phase 2 M65).
SELECT b.variant_id, t.id AS lot_id, t.number, t.serial, t.expiry, t.best_before, t.received_at, t.status,
       b.location_id, l.full_code, b.on_hand::text AS on_hand
FROM inventory.balances b
JOIN inventory.lots t ON t.id = b.lot_id
JOIN inventory.locations l ON l.id = b.location_id
WHERE b.variant_id = ANY(@variant_ids::int[]) AND l.site_id = @site_id AND l.status = 'active' AND b.on_hand > 0
ORDER BY b.variant_id, t.expiry NULLS LAST, t.received_at, t.id, l.full_code, b.location_id;
