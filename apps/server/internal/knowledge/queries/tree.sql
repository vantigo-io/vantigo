-- The trees queries (knowledge design K3–K6). "Readable" is always the
-- materialised access of K5: the nearest member row for the person, else the
-- effective default, not none.

-- name: TreeLock :exec
-- TreeLock serialises every write that changes the tree or its access (K5):
-- a move, a change of a default, of ai_hidden or of a member, a create, a
-- trash and a restore. It is held to the end of the transaction.
SELECT pg_advisory_xact_lock(hashtext('knowledge.tree'));

-- name: ArticleMetaFor :one
-- ArticleMetaFor is an articles own fields without its document, with what
-- userID may do with it and what decides its section.
SELECT a.id, a.parent_id, a.position, a.title, a.icon, a.internal_permission, a.version, a.locked,
       a.ai_hidden, a.created_by, a.created_at, a.updated_by, a.updated_at,
       a.trashed_at, a.trashed_by, a.trash_root,
       d.permission AS default_permission, d.ai_hidden AS hidden,
       coalesce(x.permission, d.permission)::text AS permission,
       (SELECT count(*) FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write')::bigint AS writers
FROM knowledge.articles a
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
WHERE a.id = @id::uuid;

-- name: LockArticle :exec
-- LockArticle takes an articles row lock, which serialises its content
-- saves (K9); a content save never takes the trees lock.
SELECT 1 FROM knowledge.articles WHERE id = @id::uuid FOR UPDATE;

-- name: ArticleContent :one
SELECT content, content_text FROM knowledge.articles WHERE id = @id::uuid;

-- name: ArticleDepth :one
-- ArticleDepth is the articles level: 1 for a root.
WITH RECURSIVE up(id, parent_id, depth) AS (
    SELECT a.id, a.parent_id, 1 FROM knowledge.articles a WHERE a.id = @id::uuid
    UNION ALL
    SELECT p.id, p.parent_id, up.depth + 1 FROM knowledge.articles p JOIN up ON p.id = up.parent_id WHERE up.depth < 64
)
SELECT coalesce(max(depth), 0)::int AS depth FROM up;

-- name: SubtreeHeight :one
-- SubtreeHeight is how many levels the articles subtree spans: 1 for a leaf.
WITH RECURSIVE sub(id, depth) AS (
    SELECT @id::uuid, 1
    UNION ALL
    SELECT c.id, sub.depth + 1 FROM knowledge.articles c JOIN sub ON c.parent_id = sub.id WHERE sub.depth < 64
)
SELECT coalesce(max(depth), 0)::int AS height FROM sub;

-- name: IsAncestorOrSelf :one
-- IsAncestorOrSelf reports whether ancestor is of or on the path above of:
-- a move of ancestor under of would make a cycle (K3).
WITH RECURSIVE up(id, parent_id, depth) AS (
    SELECT a.id, a.parent_id, 1 FROM knowledge.articles a WHERE a.id = @of::uuid
    UNION ALL
    SELECT p.id, p.parent_id, up.depth + 1 FROM knowledge.articles p JOIN up ON p.id = up.parent_id WHERE up.depth < 64
)
SELECT EXISTS (SELECT 1 FROM up WHERE up.id = @ancestor::uuid) AS cycle;

-- name: SiblingPosition :one
-- SiblingPosition is the position of after when it is a sibling under
-- parent that is not trashed and userID can read, and empty when it is not
-- one: an article they cannot read is answered as one that does not exist.
SELECT coalesce((
    SELECT a.position FROM knowledge.articles a
    JOIN knowledge.article_access_default d ON d.article_id = a.id
    LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
    WHERE a.id = @after::uuid AND a.parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::uuid AND a.trashed_at IS NULL
      AND coalesce(x.permission, d.permission) <> 'none'
), '')::text AS position;

-- name: NextSiblingPosition :one
-- NextSiblingPosition is the first siblings position after position under
-- parent, but for the article excluded; after an empty position, the first.
SELECT coalesce(min(position), '')::text AS position
FROM knowledge.articles
WHERE parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::uuid
  AND trashed_at IS NULL
  AND id <> @exclude::uuid
  AND position > @after::text;

-- name: LastSiblingPosition :one
SELECT coalesce(max(position), '')::text AS position
FROM knowledge.articles
WHERE parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::uuid
  AND trashed_at IS NULL
  AND id <> @exclude::uuid;

-- name: InsertArticle :exec
INSERT INTO knowledge.articles (id, parent_id, position, title, icon, content, content_text, internal_permission,
                                version, created_by, created_at, updated_by, updated_at)
VALUES (@id::uuid, sqlc.narg(parent_id)::uuid, @position::text, @title::text, sqlc.narg(icon)::text, @content::jsonb,
        @content_text::text, sqlc.narg(internal_permission)::text, 1, @user_id::uuid, @now::timestamptz, @user_id::uuid, @now::timestamptz);

-- name: MoveArticle :exec
UPDATE knowledge.articles
SET parent_id = sqlc.narg(parent_id)::uuid, position = @position::text,
    internal_permission = sqlc.narg(internal_permission)::text
WHERE id = @id::uuid;

-- name: UpdateArticleFields :exec
-- UpdateArticleFields changes an articles own fields; each is changed only
-- when its set_ flag says so.
UPDATE knowledge.articles
SET icon = CASE WHEN @set_icon::boolean THEN sqlc.narg(icon)::text ELSE icon END,
    locked = CASE WHEN @set_locked::boolean THEN @locked::boolean ELSE locked END,
    ai_hidden = CASE WHEN @set_ai_hidden::boolean THEN @ai_hidden::boolean ELSE ai_hidden END,
    updated_by = @user_id::uuid,
    updated_at = @now::timestamptz
WHERE id = @id::uuid;

-- name: SaveContent :one
-- SaveContent writes a saves title and document over the article and moves
-- its version on (K9).
UPDATE knowledge.articles
SET title = @title::text, content = @content::jsonb, content_text = @content_text::text,
    version = version + 1, updated_by = @user_id::uuid, updated_at = @now::timestamptz
WHERE id = @id::uuid
RETURNING version;

-- name: TreeRoots :many
-- TreeRoots is the topmost articles userID can read: readable, not trashed,
-- and a root or under a parent they cannot read (K4).
SELECT a.id, a.parent_id, a.position, a.title, a.icon, a.locked,
       d.permission AS default_permission,
       coalesce(x.permission, d.permission)::text AS permission,
       (SELECT count(*) FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write')::bigint AS writers,
       EXISTS (
           SELECT 1 FROM knowledge.articles c
           JOIN knowledge.article_access_default cd ON cd.article_id = c.id
           LEFT JOIN knowledge.article_access cx ON cx.article_id = c.id AND cx.user_id = @user_id::uuid
           WHERE c.parent_id = a.id AND c.trashed_at IS NULL AND coalesce(cx.permission, cd.permission) <> 'none'
       ) AS has_children
FROM knowledge.articles a
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
LEFT JOIN knowledge.article_access_default pd ON pd.article_id = a.parent_id
LEFT JOIN knowledge.article_access px ON px.article_id = a.parent_id AND px.user_id = @user_id::uuid
WHERE a.trashed_at IS NULL
  AND coalesce(x.permission, d.permission) <> 'none'
  AND (a.parent_id IS NULL OR coalesce(px.permission, pd.permission) = 'none')
ORDER BY a.position, a.id;

-- name: TreeChildren :many
-- TreeChildren is parents children userID can read, in order.
SELECT a.id, a.parent_id, a.position, a.title, a.icon, a.locked,
       d.permission AS default_permission,
       coalesce(x.permission, d.permission)::text AS permission,
       (SELECT count(*) FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write')::bigint AS writers,
       EXISTS (
           SELECT 1 FROM knowledge.articles c
           JOIN knowledge.article_access_default cd ON cd.article_id = c.id
           LEFT JOIN knowledge.article_access cx ON cx.article_id = c.id AND cx.user_id = @user_id::uuid
           WHERE c.parent_id = a.id AND c.trashed_at IS NULL AND coalesce(cx.permission, cd.permission) <> 'none'
       ) AS has_children
FROM knowledge.articles a
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
WHERE a.parent_id = @parent_id::uuid
  AND a.trashed_at IS NULL
  AND coalesce(x.permission, d.permission) <> 'none'
ORDER BY a.position, a.id;

-- name: Ancestors :many
-- Ancestors is the ancestors of every article in ids, nearest first (depth
-- 1 is the parent), with whether userID can read each and whether it is
-- hidden from the Assistant: what a path is cut by (K12, K16).
WITH RECURSIVE up(origin, id, parent_id, title, depth) AS (
    SELECT a.id, p.id, p.parent_id, p.title, 1
    FROM knowledge.articles a JOIN knowledge.articles p ON p.id = a.parent_id
    WHERE a.id = ANY(@ids::uuid[])
    UNION ALL
    SELECT up.origin, p.id, p.parent_id, p.title, up.depth + 1
    FROM up JOIN knowledge.articles p ON p.id = up.parent_id
    WHERE up.depth < 64
)
SELECT up.origin::uuid AS origin, up.id::uuid AS id, up.title::text AS title, up.depth::int AS depth,
       (coalesce(x.permission, d.permission) <> 'none')::boolean AS readable,
       d.ai_hidden AS hidden
FROM up
JOIN knowledge.article_access_default d ON d.article_id = up.id
LEFT JOIN knowledge.article_access x ON x.article_id = up.id AND x.user_id = @user_id::uuid
ORDER BY up.origin, up.depth;
