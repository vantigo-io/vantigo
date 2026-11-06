-- name: LockInbound :exec
-- Serialises the transactions that insert into meta.inbound, so rows commit in
-- the order their ids were handed out and a reader that has seen id N has seen
-- every id below it.
SELECT pg_advisory_xact_lock(7340113501);

-- name: InsertInbound :execrows
INSERT INTO meta.inbound (page_id, kind, dedup_key, participant, occurred_at, payload, storage_keys, created_at)
VALUES (@page_id, @kind, @dedup_key, @participant, @occurred_at, @payload::jsonb, @storage_keys::text[], @now)
ON CONFLICT (page_id, dedup_key) DO NOTHING;

-- name: ListPendingInbound :many
SELECT id, page_id, kind, participant, occurred_at, payload, storage_keys
FROM meta.inbound
WHERE page_id = @page_id AND id > @after
ORDER BY id
LIMIT @max_rows;

-- name: NotifiedPages :many
SELECT DISTINCT page_id FROM meta.inbound WHERE acked_at IS NULL AND created_at > @since;

-- name: GetInbound :one
SELECT id, page_id, payload, storage_keys FROM meta.inbound WHERE id = @id AND page_id = @page_id;

-- name: AckInbound :execrows
UPDATE meta.inbound SET acked_at = @now WHERE page_id = @page_id AND id <= @up_to AND acked_at IS NULL;

-- name: ListExpiredInbound :many
-- Acknowledged rows older than the acknowledged retention, and rows never
-- acknowledged older than the longest bound.
SELECT id, storage_keys FROM meta.inbound
WHERE (acked_at IS NOT NULL AND acked_at < @acked_before)
   OR created_at < @created_before
ORDER BY id
LIMIT @max_rows;

-- name: DeleteInboundByIDs :execrows
DELETE FROM meta.inbound WHERE id = ANY(@ids::bigint[]);

-- name: DeleteInboundForPage :many
DELETE FROM meta.inbound WHERE page_id = @page_id RETURNING storage_keys;

-- name: PageExistsForShare :one
-- The Page's row, locked against its removal until the transaction ends.
SELECT page_id FROM meta.pages WHERE page_id = @page_id FOR SHARE;

-- name: StageFile :exec
INSERT INTO meta.pending_files (storage_key, created_at) VALUES (@storage_key, @now)
ON CONFLICT (storage_key) DO UPDATE SET created_at = excluded.created_at;

-- name: UnstageFiles :exec
DELETE FROM meta.pending_files WHERE storage_key = ANY(@keys::text[]);

-- name: ListStaleStagedFiles :many
-- Staged keys older than @before, and whether an inbound row names each.
SELECT p.storage_key,
       EXISTS (SELECT 1 FROM meta.inbound i WHERE i.storage_keys @> ARRAY[p.storage_key]) AS referenced
FROM meta.pending_files p
WHERE p.created_at < @before
ORDER BY p.created_at
LIMIT @max_rows;
