-- Lots (manufacturing phase 2 M52–M58): a numbered quantity of one variant,
-- created by the posting that first names it (M57), never deleted. The
-- posting engine (posting.go) and the ledger's EnsureLots (lots.go) take the
-- lot counter, insert new lots and lock the lots they name in the lock order
-- of M58: counter, new lots in (variant, lower(number)) order, the lots named
-- in id order — all before the movement's header, the locations and the
-- balances.

-- name: AllocateLotNumbers :one
-- AllocateLotNumbers takes n numbers of the lot series under the counter
-- row's lock (step 3 of M58) and answers the first: L<first> … L<first+n-1>.
-- The lock is held until the posting ends, so it serialises the postings that
-- generate a number.
INSERT INTO inventory.counters (counter_name, next_value)
VALUES ('lot', 1001 + @n::bigint)
ON CONFLICT (counter_name) DO UPDATE SET next_value = inventory.counters.next_value + @n::bigint
RETURNING (next_value - @n::bigint)::bigint AS first;

-- name: InsertLot :one
-- InsertLot creates a lot unless the variant has one of the number, ignoring
-- case (step 4): a conflict answers no row — a concurrent insert of the same
-- number is waited for — and the caller reads the lot back by number.
INSERT INTO inventory.lots (variant_id, number, serial, supplier_lot, production_date, best_before, expiry, received_at,
                            origin_module, origin_kind, origin_id, note, sku, product_name,
                            created_by_user_id, created_by_display, created_at, updated_at)
VALUES (@variant_id, @number, @serial, sqlc.narg(supplier_lot), sqlc.narg(production_date), sqlc.narg(best_before), sqlc.narg(expiry),
        @now::timestamptz, sqlc.narg(origin_module), sqlc.narg(origin_kind), sqlc.narg(origin_id), sqlc.narg(note), @sku, @product_name,
        sqlc.narg(created_by_user_id), sqlc.narg(created_by_display), @now::timestamptz, @now::timestamptz)
ON CONFLICT (variant_id, lower(number)) DO NOTHING
RETURNING id;

-- name: LotByNumber :one
-- LotByNumber is the variant's lot of the number, ignoring case.
SELECT * FROM inventory.lots WHERE variant_id = @variant_id AND lower(number) = lower(@number::text);

-- name: ShareLots :many
-- ShareLots takes FOR SHARE on lots, in id order (step 5).
SELECT * FROM inventory.lots WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR SHARE;

-- name: LockLots :many
-- LockLots takes FOR NO KEY UPDATE on lots, in id order (step 5): a serial
-- lot a posting increases, or a lot whose holds change (M53, M64).
SELECT * FROM inventory.lots WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR NO KEY UPDATE;

-- name: LotsByIDs :many
-- LotsByIDs is a plain read of lots.
SELECT * FROM inventory.lots WHERE id = ANY(@ids::bigint[]) ORDER BY id;

-- name: SetLotOrigin :exec
-- SetLotOrigin names the movement that created the lots, once its header is
-- claimed.
UPDATE inventory.lots SET origin_module = @origin_module, origin_kind = @origin_kind, origin_id = @origin_id
WHERE id = ANY(@ids::bigint[]);

-- name: DeleteLots :exec
-- DeleteLots removes lots a posting created and then found to be a replay of
-- a movement posted meanwhile: nothing refers to them.
DELETE FROM inventory.lots WHERE id = ANY(@ids::bigint[]);

-- name: LotTotals :many
-- LotTotals is each lot's on hand over every location, read after the
-- posting's balances are locked (the serial rule, M53).
SELECT lot_id::bigint AS lot_id, sum(on_hand)::text AS on_hand
FROM inventory.balances WHERE lot_id = ANY(@ids::bigint[])
GROUP BY lot_id;

-- name: LotCandidates :many
-- LotCandidates is every lot holding more than 0 of each (variant, location)
-- given, in FEFO order — expiry (none last), received, lot id — with what it
-- holds, its expiry and its status: the lots an AutoLot line may take (M65a),
-- read plainly on the posting's transaction before the lots are locked, so
-- the posting locks only those that look to cover it.
SELECT b.variant_id, b.location_id, b.lot_id::bigint AS lot_id, b.on_hand::text AS on_hand, t.expiry, t.status
FROM inventory.balances b
JOIN (SELECT DISTINCT unnest(@variant_ids::int[]) AS variant_id, unnest(@location_ids::bigint[]) AS location_id) AS u
  ON u.variant_id = b.variant_id AND u.location_id = b.location_id
JOIN inventory.lots t ON t.id = b.lot_id
WHERE b.on_hand > 0
ORDER BY b.variant_id, b.location_id, t.expiry NULLS LAST, t.received_at, b.lot_id;

-- name: LotsOfMovementLines :many
-- LotsOfMovementLines is the lot of every line of a movement that has one,
-- with its number and expiry, for the movement's answer.
SELECT ml.line_no, t.id, t.number, t.expiry
FROM inventory.movement_lines ml
JOIN inventory.lots t ON t.id = ml.lot_id
WHERE ml.movement_id = @movement_id
ORDER BY ml.line_no;
