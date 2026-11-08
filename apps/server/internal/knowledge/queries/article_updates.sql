-- The update log of live co-editing (knowledge design K27). Every write runs
-- under the article's row lock (LockArticle), never the tree's.

-- name: ListUpdatesAfter :many
-- ListUpdatesAfter is the newest seq of the log and the rows after a seq, in
-- one statement and so one snapshot: a row is either in it or newer than its
-- newest. seq and data are null, in the one row answered, when no row follows.
WITH newest AS (
    SELECT coalesce(max(seq), 0)::bigint AS n,
           coalesce((SELECT doc_epoch FROM knowledge.articles WHERE id = @article_id::uuid), 0)::bigint AS epoch
    FROM knowledge.article_updates WHERE article_id = @article_id::uuid
)
SELECT newest.n::bigint AS newest, newest.epoch::bigint AS epoch, u.seq AS seq, u.data AS data
FROM newest
LEFT JOIN knowledge.article_updates u ON u.article_id = @article_id::uuid AND u.seq > @after::bigint
ORDER BY u.seq;

-- name: UpdateSeqRange :one
-- UpdateSeqRange is the log's first and last seq, 0 for an empty log.
SELECT coalesce(min(seq), 0)::bigint AS first_seq, coalesce(max(seq), 0)::bigint AS last_seq
FROM knowledge.article_updates WHERE article_id = @article_id::uuid;

-- name: DocState :one
-- DocState is the log's state on the article row (K27, K32); the caller holds
-- the row lock.
SELECT doc_epoch::bigint AS epoch, version, update_rows::bigint AS update_rows, update_bytes, last_compaction_ask_at, last_append_at
FROM knowledge.articles WHERE id = @id::uuid;

-- name: AddDocCounters :exec
-- AddDocCounters counts an appended row and notes when it was (last_append_at).
UPDATE knowledge.articles SET update_rows = update_rows + @rows::int, update_bytes = update_bytes + @bytes::bigint, last_append_at = @now::timestamptz WHERE id = @id::uuid;

-- name: SetDocCounters :exec
-- SetDocCounters sets the log's size after a seed or a compaction, which note
-- when they were (last_append_at) like an append.
UPDATE knowledge.articles SET update_rows = @rows::int, update_bytes = @bytes::bigint, last_append_at = @now::timestamptz WHERE id = @id::uuid;

-- name: MarkCompactionAsked :exec
UPDATE knowledge.articles SET last_compaction_ask_at = @now::timestamptz WHERE id = @id::uuid;

-- name: ResetDocLog :one
-- ResetDocLog starts a new epoch with an empty log and answers it (K32).
UPDATE knowledge.articles SET doc_epoch = doc_epoch + 1, update_rows = 0, update_bytes = 0, last_append_at = NULL
WHERE id = @id::uuid RETURNING doc_epoch::bigint AS epoch;

-- name: AppendUpdate :one
-- AppendUpdate stores a row at max(seq) + 1 and answers the seq.
INSERT INTO knowledge.article_updates (article_id, seq, data, created_by, created_at)
SELECT @article_id::uuid, coalesce(max(seq), 0) + 1, @data::bytea, @created_by::uuid, @now::timestamptz
FROM knowledge.article_updates WHERE article_id = @article_id::uuid
RETURNING seq;

-- name: SeedIfEmpty :execrows
-- SeedIfEmpty stores the seed as row 1 only when the log is empty (K29).
INSERT INTO knowledge.article_updates (article_id, seq, data, created_by, created_at)
SELECT @article_id::uuid, 1, @data::bytea, @created_by::uuid, @now::timestamptz
WHERE NOT EXISTS (SELECT 1 FROM knowledge.article_updates WHERE article_id = @article_id::uuid);

-- name: CompactUpdates :one
-- CompactUpdates deletes the rows up to and including a seq (K31) and answers
-- how many rows and bytes went; the caller inserts the compacted row in the
-- same transaction.
WITH gone AS (
    DELETE FROM knowledge.article_updates WHERE article_id = @article_id::uuid AND seq <= @up_to::bigint
    RETURNING octet_length(data) AS n
)
SELECT count(*)::bigint AS rows, coalesce(sum(n), 0)::bigint AS bytes FROM gone;

-- name: InsertUpdateAt :exec
-- InsertUpdateAt stores the compacted row at its seq.
INSERT INTO knowledge.article_updates (article_id, seq, data, created_by, created_at)
VALUES (@article_id::uuid, @seq::bigint, @data::bytea, @created_by::uuid, @now::timestamptz);

-- name: GetUpdate :one
SELECT data FROM knowledge.article_updates WHERE article_id = @article_id::uuid AND seq = @seq::bigint;

-- name: DeleteUpdates :execrows
-- DeleteUpdates deletes an article's whole log (K32): the JSON is the truth again.
DELETE FROM knowledge.article_updates WHERE article_id = @article_id::uuid;

-- name: NullUpdateAuthors :exec
-- NullUpdateAuthors sets the erased users authorship of updates to null (K17).
UPDATE knowledge.article_updates SET created_by = NULL WHERE created_by = ANY(@users::uuid[]);
