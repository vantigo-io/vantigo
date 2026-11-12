-- The trace (manufacturing phase 2 M88, M89): the graph of lots is read from
-- the movement lines that carry one — no genealogy table is kept. Each read
-- is by lot (ix_movement_lines_lot) or by movement
-- (ux_movement_lines_movement_line), one per level of the walk.

-- name: TraceLotLines :many
-- TraceLotLines is every line of the lots that enters or leaves stock —
-- a line with only a from or only a to; a move, which has both, changes
-- nothing of a lot's path — with its movement, oldest first.
SELECT ml.lot_id::bigint AS lot_id, ml.movement_id, ml.line_no, ml.variant_id, ml.sku, ml.product_name, ml.unit, ml.quantity,
       (ml.from_location_id IS NOT NULL)::boolean AS outgoing,
       m.kind, m.reason, m.posted_at, m.supplier_name, m.supplier_reference,
       m.source_module, m.source_kind, m.source_id, m.source_line_id
FROM inventory.movement_lines ml
JOIN inventory.movements m ON m.id = ml.movement_id
WHERE ml.lot_id = ANY(@lot_ids::bigint[]) AND (ml.from_location_id IS NULL OR ml.to_location_id IS NULL)
ORDER BY m.posted_at, m.id, ml.line_no;

-- name: TraceMovementLines :many
-- TraceMovementLines is every line of the movements — the productions the
-- walk crosses — with its lot when it has one.
SELECT ml.movement_id, ml.line_no, ml.variant_id, ml.sku, ml.product_name, ml.unit, ml.lot_id, ml.quantity,
       (ml.from_location_id IS NOT NULL)::boolean AS outgoing
FROM inventory.movement_lines ml
WHERE ml.movement_id = ANY(@movement_ids::bigint[])
ORDER BY ml.movement_id, ml.line_no;

-- name: TraceLotsOnHand :many
-- TraceLotsOnHand is what each lot holds now, over every location
-- (ix_balances_lot); a lot holding nothing has no row.
SELECT lot_id::bigint AS lot_id, sum(on_hand)::text AS on_hand
FROM inventory.balances
WHERE lot_id = ANY(@lot_ids::bigint[])
GROUP BY lot_id;

-- name: ActiveSourceHoldsOfKind :many
-- ActiveSourceHoldsOfKind is every active hold of one kind a document put,
-- on any lot, in lot id order: what a block's release with its downstream
-- list releases (M92).
SELECT * FROM inventory.lot_holds
WHERE released_at IS NULL AND kind = @kind
  AND source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY lot_id, id;
