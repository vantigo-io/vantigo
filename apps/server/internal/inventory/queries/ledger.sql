-- The ledger (design I8–I15): posted movements, their lines, and the balance
-- per variant and location they keep. The posting engine (posting.go) runs
-- these in the lock order of §4.

-- name: MovementByClientRequest :one
-- MovementByClientRequest is step 1 of a posting: a plain read of the key,
-- before any lock (I14).
SELECT id, request_hash FROM inventory.movements WHERE client_request_id = @client_request_id;

-- name: ClaimMovement :one
-- ClaimMovement writes the movement's header and so claims its key (step 3).
-- A concurrent first attempt holding the key makes this wait for it; one that
-- committed answers no row, and the caller reads it back.
INSERT INTO inventory.movements (client_request_id, request_hash, site_id, kind, reason, note, supplier_name, supplier_reference,
                                 posted_by_user_id, posted_by_display, posted_at, count_id)
VALUES (@client_request_id, @request_hash, @site_id, @kind, sqlc.narg(reason), sqlc.narg(note), sqlc.narg(supplier_name),
        sqlc.narg(supplier_reference), sqlc.narg(posted_by_user_id), sqlc.narg(posted_by_display), @posted_at::timestamptz,
        sqlc.narg(count_id))
ON CONFLICT (client_request_id) DO NOTHING
RETURNING id;

-- name: ShareLocations :many
-- ShareLocations takes FOR SHARE on a posting's locations in id order (step
-- 4). A posting writes nothing to a location.
SELECT * FROM inventory.locations WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR SHARE;

-- name: LockBalances :many
-- LockBalances creates a posting's missing balances at zero and locks every
-- one of them, existing or new, in (variant, location) order, in one
-- statement (step 5, I10). ON CONFLICT DO UPDATE locks the row it meets as
-- FOR UPDATE would, waits for a concurrent insert of the same key to settle,
-- inserts again when a concurrent posting deleted the row at zero, and
-- answers the row's latest committed on_hand. No lock is held across a
-- second attempt, so two postings to a new pair serialise on the row rather
-- than collide on its key, and none waits for another's insert while holding
-- a lock that one needs.
INSERT INTO inventory.balances (variant_id, location_id, on_hand, updated_at)
SELECT u.variant_id, u.location_id, 0, @now::timestamptz
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@location_ids::bigint[]) AS location_id) AS u
ORDER BY u.variant_id, u.location_id
ON CONFLICT (variant_id, location_id) DO UPDATE SET updated_at = inventory.balances.updated_at
RETURNING variant_id, location_id, on_hand;

-- name: SetBalances :exec
-- SetBalances writes the balances a posting computed.
UPDATE inventory.balances b
SET on_hand = u.on_hand, updated_at = @now::timestamptz
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@location_ids::bigint[]) AS location_id,
             unnest(@on_hands::numeric[]) AS on_hand) AS u
WHERE b.variant_id = u.variant_id AND b.location_id = u.location_id;

-- name: DeleteZeroBalances :exec
-- DeleteZeroBalances removes the balances a posting left at zero, so "where
-- does it lie" lists only places with stock (I10).
DELETE FROM inventory.balances b
USING (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@location_ids::bigint[]) AS location_id) AS u
WHERE b.variant_id = u.variant_id AND b.location_id = u.location_id AND b.on_hand = 0;

-- name: InsertMovementLines :exec
-- InsertMovementLines writes a movement's lines with the variant snapshots
-- (I9) and, for a line entered in packagings, the entry as it was at posting
-- (I25). A from or to of 0 is none; an entry of '' is none.
INSERT INTO inventory.movement_lines (movement_id, line_no, variant_id, sku, product_name, unit, from_location_id, to_location_id, quantity, entered)
SELECT @movement_id, u.line_no, u.variant_id, u.sku, u.product_name, u.unit, nullif(u.from_id, 0), nullif(u.to_id, 0), u.quantity,
       nullif(u.entered, '')::jsonb
FROM (SELECT unnest(@line_nos::int[]) AS line_no, unnest(@variant_ids::int[]) AS variant_id, unnest(@skus::text[]) AS sku,
             unnest(@product_names::text[]) AS product_name, unnest(@units::text[]) AS unit,
             unnest(@from_ids::bigint[]) AS from_id, unnest(@to_ids::bigint[]) AS to_id,
             unnest(@quantities::numeric[]) AS quantity, unnest(@entereds::text[]) AS entered) AS u;

-- name: VariantHeld :one
-- VariantHeld is what a variant holds across every location (I23), on the
-- caller's transaction.
SELECT coalesce(sum(on_hand), 0)::text AS quantity, count(*)::int AS locations
FROM inventory.balances WHERE variant_id = @variant_id AND on_hand <> 0;
