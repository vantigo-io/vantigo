-- Holds on lots (manufacturing phase 2 M64): each owned by the document that
-- put it, the lot's status derived from its active ones and rewritten in the
-- same transaction, under the lot's FOR NO KEY UPDATE.

-- name: ActiveHoldsOfSource :many
-- ActiveHoldsOfSource is the active holds a document put on the lots.
SELECT * FROM inventory.lot_holds
WHERE lot_id = ANY(@lot_ids::bigint[]) AND released_at IS NULL
  AND source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY id;

-- name: InsertLotHold :one
INSERT INTO inventory.lot_holds (lot_id, kind, cause, note, source_module, source_kind, source_id,
                                 created_by_user_id, created_by_display, created_at)
VALUES (@lot_id, @kind, @cause, sqlc.narg(note), @source_module, @source_kind, @source_id,
        sqlc.narg(created_by_user_id), sqlc.narg(created_by_display), @now::timestamptz)
RETURNING id;

-- name: LotsHeldBySource :many
-- LotsHeldBySource is every lot a document holds an active hold on, in id
-- order: what a release of all its holds locks.
SELECT DISTINCT lot_id FROM inventory.lot_holds
WHERE released_at IS NULL AND source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
ORDER BY lot_id;

-- name: ReleaseSourceHolds :many
-- ReleaseSourceHolds releases the document's active holds on the lots: every
-- other source's stay.
UPDATE inventory.lot_holds
SET released_at = @now::timestamptz, released_by_user_id = sqlc.narg(released_by_user_id),
    released_by_display = sqlc.narg(released_by_display)
WHERE lot_id = ANY(@lot_ids::bigint[]) AND released_at IS NULL
  AND source_module = @source_module AND source_kind = @source_kind AND source_id = @source_id
RETURNING id, lot_id, kind, cause;

-- name: DeriveLotStatuses :many
-- DeriveLotStatuses rewrites each lot's status from its active holds:
-- blocked while any is a block, quarantine while any is a quarantine, else
-- available — moving the revision on when it changed.
UPDATE inventory.lots t
SET status = d.status, revision = t.revision + 1, updated_at = @now::timestamptz
FROM (SELECT l.id,
             CASE WHEN EXISTS (SELECT 1 FROM inventory.lot_holds h WHERE h.lot_id = l.id AND h.released_at IS NULL AND h.kind = 'block') THEN 'blocked'
                  WHEN EXISTS (SELECT 1 FROM inventory.lot_holds h WHERE h.lot_id = l.id AND h.released_at IS NULL AND h.kind = 'quarantine') THEN 'quarantine'
                  ELSE 'available' END AS status
      FROM inventory.lots l WHERE l.id = ANY(@ids::bigint[])) AS d
WHERE t.id = d.id AND t.status <> d.status
RETURNING t.id, t.status;

-- name: LotHolds :many
-- LotHolds is every hold of a lot, the active ones first, newest first
-- within each (M61).
SELECT * FROM inventory.lot_holds WHERE lot_id = @lot_id
ORDER BY (released_at IS NULL) DESC, created_at DESC, id DESC;

-- name: GetLotHold :one
-- GetLotHold is one hold of a lot; another lot's is no row.
SELECT * FROM inventory.lot_holds WHERE id = @id AND lot_id = @lot_id;

-- name: ReleaseLotHold :one
-- ReleaseLotHold releases one active hold, with the releaser's note (M64:
-- the lot page's Release); a released one is no row.
UPDATE inventory.lot_holds
SET released_at = @now::timestamptz, released_by_user_id = sqlc.narg(released_by_user_id),
    released_by_display = sqlc.narg(released_by_display), release_note = sqlc.narg(release_note)
WHERE id = @id AND lot_id = @lot_id AND released_at IS NULL
RETURNING *;

-- name: ActiveHoldsOfLots :many
-- ActiveHoldsOfLots is the active holds on the lots, blocks first, oldest
-- first within each: what an unbuild of a held lot carries onto the lots it
-- restores (manufacturing phase 2 M123).
SELECT * FROM inventory.lot_holds
WHERE lot_id = ANY(@lot_ids::bigint[]) AND released_at IS NULL
ORDER BY (kind = 'block') DESC, created_at, id;
