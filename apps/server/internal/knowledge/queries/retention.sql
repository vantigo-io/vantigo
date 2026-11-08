-- The knowledge.retention worker (knowledge design K9–K11, K17).

-- name: PurgeTrash :execrows
-- PurgeTrash deletes for good the trash groups trashed before cutoff.
DELETE FROM knowledge.articles WHERE trash_root = id AND trashed_at < @cutoff::timestamptz;

-- name: PruneVersions :execrows
-- PruneVersions deletes the versions older than cutoff beyond each articles
-- newest keep.
DELETE FROM knowledge.article_versions v
USING (
    SELECT article_id, version, row_number() OVER (PARTITION BY article_id ORDER BY version DESC) AS n
    FROM knowledge.article_versions
) r
WHERE v.article_id = r.article_id AND v.version = r.version
  AND r.n > @keep::int AND v.created_at < @cutoff::timestamptz;

-- name: PruneFavourites :execrows
-- PruneFavourites removes the favourites of articles their person can no
-- longer read.
DELETE FROM knowledge.favourites f
USING knowledge.article_access_default d
WHERE d.article_id = f.article_id
  AND coalesce((SELECT x.permission FROM knowledge.article_access x WHERE x.article_id = f.article_id AND x.user_id = f.user_id), d.permission) = 'none';

-- name: ListHeldUserIDs :many
-- ListHeldUserIDs is the users the module holds anything of, after a given
-- id, in id order: the erasure sweep asks identity which still exist.
SELECT user_id::uuid AS user_id FROM (
    SELECT m.user_id FROM knowledge.article_members m
    UNION SELECT x.user_id FROM knowledge.article_access x
    UNION SELECT f.user_id FROM knowledge.favourites f
    UNION SELECT a.created_by FROM knowledge.articles a WHERE a.created_by IS NOT NULL
    UNION SELECT a.updated_by FROM knowledge.articles a WHERE a.updated_by IS NOT NULL
    UNION SELECT a.trashed_by FROM knowledge.articles a WHERE a.trashed_by IS NOT NULL
    UNION SELECT v.created_by FROM knowledge.article_versions v WHERE v.created_by IS NOT NULL
    UNION SELECT f.created_by FROM knowledge.article_files f WHERE f.created_by IS NOT NULL
    UNION SELECT n.user_id FROM knowledge.mention_notices n
    UNION SELECT u.created_by FROM knowledge.article_updates u WHERE u.created_by IS NOT NULL
) held
WHERE user_id > @after::uuid
ORDER BY user_id
LIMIT @batch_size::int;

-- name: DeleteErasedPrivateArticles :execrows
-- DeleteErasedPrivateArticles deletes the private roots whose only writer
-- was one of the erased users, with their subtrees and versions (K17).
DELETE FROM knowledge.articles a
USING knowledge.article_access_default d
WHERE d.article_id = a.id
  AND a.parent_id IS NULL
  AND d.permission = 'none'
  AND EXISTS (SELECT 1 FROM knowledge.article_access x WHERE x.article_id = a.id AND x.permission = 'write' AND x.user_id = ANY(@users::uuid[]))
  AND (SELECT count(*) FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write') = 1;

-- name: DeleteErasedMembers :exec
-- DeleteErasedMembers deletes the erased users member rows and, since no
-- member row of theirs is left anywhere, every materialised row of theirs.
WITH gone AS (
    DELETE FROM knowledge.article_members WHERE user_id = ANY(@users::uuid[]) RETURNING 1
)
DELETE FROM knowledge.article_access WHERE user_id = ANY(@users::uuid[]);

-- name: DeleteErasedFavourites :exec
DELETE FROM knowledge.favourites WHERE user_id = ANY(@users::uuid[]);

-- name: ForgetErasedAuthors :exec
-- ForgetErasedAuthors sets the erased users authorship to null, shown as a
-- former user (K17); what they wrote stays.
WITH authors AS (
    UPDATE knowledge.articles
    SET created_by = CASE WHEN created_by = ANY(@users::uuid[]) THEN NULL ELSE created_by END,
        updated_by = CASE WHEN updated_by = ANY(@users::uuid[]) THEN NULL ELSE updated_by END,
        trashed_by = CASE WHEN trashed_by = ANY(@users::uuid[]) THEN NULL ELSE trashed_by END
    WHERE created_by = ANY(@users::uuid[]) OR updated_by = ANY(@users::uuid[]) OR trashed_by = ANY(@users::uuid[])
    RETURNING 1
)
UPDATE knowledge.article_versions SET created_by = NULL WHERE created_by = ANY(@users::uuid[]);
