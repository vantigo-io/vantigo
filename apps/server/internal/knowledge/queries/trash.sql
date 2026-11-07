-- The trash (knowledge design K10).

-- name: TrashSubtree :many
-- TrashSubtree trashes the article and every article below it that is not
-- trashed already, as one trash group.
WITH RECURSIVE sub(id, depth) AS (
    SELECT @id::uuid, 1
    UNION ALL
    SELECT c.id, sub.depth + 1 FROM knowledge.articles c JOIN sub ON c.parent_id = sub.id
    WHERE c.trashed_at IS NULL AND sub.depth < 64
)
UPDATE knowledge.articles
SET trashed_at = @now::timestamptz, trashed_by = @user_id::uuid, trash_root = @id::uuid
WHERE id IN (SELECT id FROM sub) AND trashed_at IS NULL
RETURNING id;

-- name: ParentTrashed :one
-- ParentTrashed reports whether the articles parent is in the trash.
SELECT coalesce((SELECT p.trashed_at IS NOT NULL FROM knowledge.articles a JOIN knowledge.articles p ON p.id = a.parent_id WHERE a.id = @id::uuid), false)::boolean AS trashed;

-- name: RestoreGroup :execrows
UPDATE knowledge.articles SET trashed_at = NULL, trashed_by = NULL, trash_root = NULL
WHERE trash_root = @root::uuid;

-- name: ListTrash :many
-- ListTrash is the trash groups whose root userID could write, and with
-- manage every one that is not private (K2, K10), newest first.
SELECT a.id, a.title, a.icon, a.trashed_at, a.trashed_by,
       (SELECT count(*) FROM knowledge.articles g WHERE g.trash_root = a.id)::int AS article_count,
       p.id AS parent_id, p.title AS parent_title,
       (p.id IS NOT NULL AND p.trashed_at IS NULL AND coalesce(px.permission, pd.permission, 'none') <> 'none')::boolean AS parent_readable
FROM knowledge.articles a
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
LEFT JOIN knowledge.articles p ON p.id = a.parent_id
LEFT JOIN knowledge.article_access_default pd ON pd.article_id = a.parent_id
LEFT JOIN knowledge.article_access px ON px.article_id = a.parent_id AND px.user_id = @user_id::uuid
WHERE a.trash_root = a.id
  AND (coalesce(x.permission, d.permission) = 'write'
       OR (@manage::boolean AND NOT (d.permission = 'none'
           AND (SELECT count(*) FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write') = 1)))
ORDER BY a.trashed_at DESC, a.id;

-- name: EmptyTrash :execrows
-- EmptyTrash deletes every trash group for good; their subtrees, versions,
-- members and favourites go with them by the foreign keys.
DELETE FROM knowledge.articles WHERE trash_root = id;
