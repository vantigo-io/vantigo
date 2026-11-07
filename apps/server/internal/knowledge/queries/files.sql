-- Files, links and mention notices (knowledge design K19–K21, delivery K-B).

-- name: InsertFile :exec
INSERT INTO knowledge.article_files (id, article_id, object_key, filename, content_type, size, is_image, created_by, created_at)
VALUES (@id::uuid, @article_id::uuid, @object_key::text, @filename::text, @content_type::text, @size::bigint, @is_image::boolean, sqlc.narg(created_by)::uuid, @created_at::timestamptz);

-- name: GetFile :one
SELECT id, article_id, object_key, filename, content_type, size, is_image FROM knowledge.article_files WHERE id = @id::uuid;

-- name: ArticleFileIDs :many
-- ArticleFileIDs is which of ids are files of the article, locked FOR
-- SHARE against the retention worker's delete until the save commits.
SELECT id FROM knowledge.article_files WHERE article_id = @article_id::uuid AND id = ANY(@ids::uuid[]) FOR SHARE;

-- name: FilesOfArticles :many
SELECT id, article_id, object_key, filename, content_type, size, is_image FROM knowledge.article_files
WHERE article_id = ANY(@article_ids::uuid[]) ORDER BY created_at, id;

-- name: DeleteLinksFrom :exec
DELETE FROM knowledge.article_links WHERE from_id = @from_id::uuid;

-- name: InsertLinks :exec
-- InsertLinks records from's links to the targets among to that exist.
INSERT INTO knowledge.article_links (from_id, to_id)
SELECT @from_id::uuid, a.id FROM knowledge.articles a
WHERE a.id = ANY(@to_ids::uuid[]) AND a.id <> @from_id::uuid
ON CONFLICT DO NOTHING;

-- name: Backlinks :many
-- Backlinks is the articles linking to to that userID can read, by title.
SELECT a.id, a.title, a.icon
FROM knowledge.article_links l
JOIN knowledge.articles a ON a.id = l.from_id
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
WHERE l.to_id = @to_id::uuid
  AND a.trashed_at IS NULL
  AND coalesce(x.permission, d.permission) <> 'none'
ORDER BY lower(a.title), a.id;

-- name: ArticlesFor :many
-- ArticlesFor is the articles among ids that exist, with whether userID
-- can read each: not trashed and access not none (K4, K10).
SELECT a.id, a.title, a.icon,
       (a.trashed_at IS NULL AND coalesce(x.permission, d.permission) <> 'none')::boolean AS readable
FROM knowledge.articles a
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
WHERE a.id = ANY(@ids::uuid[]);

-- name: ReadersAmong :many
-- ReadersAmong is the users among users who can read the article.
SELECT u::uuid AS user_id
FROM unnest(@users::uuid[]) AS u
JOIN knowledge.articles a ON a.id = @article_id::uuid
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = u
WHERE a.trashed_at IS NULL AND coalesce(x.permission, d.permission) <> 'none';

-- name: ClaimMentionNotices :many
-- ClaimMentionNotices records that users are told now of a mention in the
-- article, and answers those not told since cutoff: once per person per
-- article per hour (K20).
INSERT INTO knowledge.mention_notices (article_id, user_id, notified_at)
SELECT @article_id::uuid, u, @now::timestamptz FROM unnest(@users::uuid[]) AS u
ON CONFLICT (article_id, user_id) DO UPDATE SET notified_at = EXCLUDED.notified_at
WHERE knowledge.mention_notices.notified_at <= @cutoff::timestamptz
RETURNING user_id;

-- name: SubtreeForExport :many
-- SubtreeForExport is root and the articles below it that userID can read,
-- not trashed, through readable parents only, parents before children.
WITH RECURSIVE sub(id, depth) AS (
    SELECT @root::uuid, 0
    UNION ALL
    SELECT c.id, sub.depth + 1
    FROM knowledge.articles c
    JOIN sub ON c.parent_id = sub.id
    JOIN knowledge.article_access_default d ON d.article_id = c.id
    LEFT JOIN knowledge.article_access x ON x.article_id = c.id AND x.user_id = @user_id::uuid
    WHERE c.trashed_at IS NULL AND coalesce(x.permission, d.permission) <> 'none' AND sub.depth < 64
)
SELECT a.id, a.parent_id, a.title, a.icon, a.content, sub.depth::int AS depth
FROM sub JOIN knowledge.articles a ON a.id = sub.id
ORDER BY sub.depth, a.parent_id, a.position;

-- name: PruneUnreferencedFiles :execrows
-- PruneUnreferencedFiles deletes the files uploaded before cutoff that
-- neither their article's content nor any of its versions names (K19); the
-- trigger queues their objects.
DELETE FROM knowledge.article_files f
WHERE f.created_at < @cutoff::timestamptz
  AND NOT EXISTS (SELECT 1 FROM knowledge.articles a WHERE a.id = f.article_id AND strpos(lower(a.content::text), f.id::text) > 0)
  AND NOT EXISTS (SELECT 1 FROM knowledge.article_versions v WHERE v.article_id = f.article_id AND strpos(lower(v.content::text), f.id::text) > 0);

-- name: ListObjectDeletions :many
SELECT object_key FROM knowledge.file_object_deletions ORDER BY queued_at, object_key LIMIT @batch_size::int;

-- name: DeleteObjectDeletions :exec
DELETE FROM knowledge.file_object_deletions WHERE object_key = ANY(@object_keys::text[]);

-- name: DeleteErasedMentionNotices :exec
DELETE FROM knowledge.mention_notices WHERE user_id = ANY(@users::uuid[]);

-- name: ForgetErasedFileAuthors :exec
UPDATE knowledge.article_files SET created_by = NULL WHERE created_by = ANY(@users::uuid[]);
