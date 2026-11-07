-- The materialised access (knowledge design K4, K5, K16). Each recomputation
-- is of one subtree, under the trees lock, from its roots parents
-- materialised rows, which are outside the subtree and therefore current.

-- name: RecomputeDefaults :exec
-- RecomputeDefaults rewrites the effective default of roots subtree: the
-- nearest internal_permission on the path, and ai_hidden when the article or
-- any ancestor is hidden (monotone).
WITH RECURSIVE t(id, permission, ai_hidden, depth) AS (
    SELECT a.id, coalesce(a.internal_permission, pd.permission, 'none')::text, (a.ai_hidden OR coalesce(pd.ai_hidden, false))::boolean, 1
    FROM knowledge.articles a
    LEFT JOIN knowledge.article_access_default pd ON pd.article_id = a.parent_id
    WHERE a.id = @root::uuid
    UNION ALL
    SELECT c.id, coalesce(c.internal_permission, t.permission)::text, (c.ai_hidden OR t.ai_hidden)::boolean, t.depth + 1
    FROM knowledge.articles c JOIN t ON c.parent_id = t.id
    WHERE t.depth < 64
)
INSERT INTO knowledge.article_access_default (article_id, permission, ai_hidden)
SELECT id, permission, ai_hidden FROM t
ON CONFLICT (article_id) DO UPDATE SET permission = EXCLUDED.permission, ai_hidden = EXCLUDED.ai_hidden;

-- name: DeleteSubtreeAccess :exec
WITH RECURSIVE sub(id, depth) AS (
    SELECT @root::uuid, 1
    UNION ALL
    SELECT c.id, sub.depth + 1 FROM knowledge.articles c JOIN sub ON c.parent_id = sub.id WHERE sub.depth < 64
)
DELETE FROM knowledge.article_access WHERE article_id IN (SELECT id FROM sub);

-- name: InsertSubtreeAccess :exec
-- InsertSubtreeAccess writes every persons effective access through a
-- membership in roots subtree: the nearest member row for them on the path.
-- An article with its own default is detached (as in Odoo): the member rows
-- above it stop there, so its path restarts at it. The rows of roots parent
-- count as the farthest, only while nothing on the way down is detached.
WITH RECURSIVE sub(id, path, inherits, depth) AS (
    SELECT a.id, ARRAY[a.id]::uuid[], (a.internal_permission IS NULL)::boolean, 1
    FROM knowledge.articles a WHERE a.id = @root::uuid
    UNION ALL
    SELECT c.id,
           (CASE WHEN c.internal_permission IS NULL THEN sub.path || c.id ELSE ARRAY[c.id] END)::uuid[],
           (sub.inherits AND c.internal_permission IS NULL)::boolean,
           sub.depth + 1
    FROM knowledge.articles c JOIN sub ON c.parent_id = sub.id
    WHERE sub.depth < 64
), cand AS (
    SELECT s.id AS article_id, m.user_id, m.permission, p.ord AS depth
    FROM sub s
    CROSS JOIN LATERAL unnest(s.path) WITH ORDINALITY AS p(aid, ord)
    JOIN knowledge.article_members m ON m.article_id = p.aid
    UNION ALL
    SELECT s.id AS article_id, x.user_id, x.permission, 0::bigint AS depth
    FROM sub s
    JOIN knowledge.articles r ON r.id = @root::uuid
    JOIN knowledge.article_access x ON x.article_id = r.parent_id
    WHERE s.inherits
)
INSERT INTO knowledge.article_access (article_id, user_id, permission)
SELECT DISTINCT ON (article_id, user_id) article_id, user_id, permission
FROM cand
ORDER BY article_id, user_id, depth DESC;

-- name: PathOf :many
-- PathOf is the article and its ancestors, depth 0 the article itself, with
-- their own defaults (empty when inherited) and whether userID can read
-- each.
WITH RECURSIVE up(id, parent_id, title, internal_permission, depth) AS (
    SELECT a.id, a.parent_id, a.title, a.internal_permission, 0
    FROM knowledge.articles a WHERE a.id = @id::uuid
    UNION ALL
    SELECT p.id, p.parent_id, p.title, p.internal_permission, up.depth + 1
    FROM knowledge.articles p JOIN up ON p.id = up.parent_id
    WHERE up.depth < 64
)
SELECT up.id::uuid AS id, up.title::text AS title, coalesce(up.internal_permission, '')::text AS internal_permission, up.depth::int AS depth,
       (coalesce(x.permission, d.permission) <> 'none')::boolean AS readable
FROM up
JOIN knowledge.article_access_default d ON d.article_id = up.id
LEFT JOIN knowledge.article_access x ON x.article_id = up.id AND x.user_id = @user_id::uuid
ORDER BY up.depth;

-- name: MembersOnPath :many
-- MembersOnPath is every member row that applies to the article: its own and
-- its ancestors, up to and including the nearest article with its own
-- default, where member rows stop (detached). Depth 0 is the articles own.
WITH RECURSIVE up(id, parent_id, internal_permission, depth) AS (
    SELECT a.id, a.parent_id, a.internal_permission, 0 FROM knowledge.articles a WHERE a.id = @id::uuid
    UNION ALL
    SELECT p.id, p.parent_id, p.internal_permission, up.depth + 1
    FROM knowledge.articles p JOIN up ON p.id = up.parent_id
    WHERE up.internal_permission IS NULL AND up.depth < 64
)
SELECT m.article_id, m.user_id, m.permission, up.depth::int AS depth
FROM up JOIN knowledge.article_members m ON m.article_id = up.id
ORDER BY up.depth, m.user_id;

-- name: EffectiveMembers :many
-- EffectiveMembers is the articles materialised access through
-- memberships.
SELECT user_id, permission FROM knowledge.article_access WHERE article_id = @id::uuid ORDER BY user_id;

-- name: DeleteMembers :exec
DELETE FROM knowledge.article_members WHERE article_id = @id::uuid;

-- name: UpsertMember :exec
INSERT INTO knowledge.article_members (article_id, user_id, permission)
VALUES (@article_id::uuid, @user_id::uuid, @permission::text)
ON CONFLICT (article_id, user_id) DO UPDATE SET permission = EXCLUDED.permission;

-- name: CopyAccessToMembers :exec
-- CopyAccessToMembers makes the articles inherited access its own member
-- rows, so it keeps who can read it when it leaves its parent.
INSERT INTO knowledge.article_members (article_id, user_id, permission)
SELECT x.article_id, x.user_id, x.permission FROM knowledge.article_access x WHERE x.article_id = @id::uuid
ON CONFLICT (article_id, user_id) DO NOTHING;

-- name: SetInternalPermission :exec
UPDATE knowledge.articles SET internal_permission = sqlc.narg(internal_permission)::text WHERE id = @id::uuid;

-- name: SubtreeWithoutWriter :one
-- SubtreeWithoutWriter reports whether an article in roots subtree that has
-- its own default other than write is left without a writer (last_writer).
WITH RECURSIVE sub(id, depth) AS (
    SELECT @root::uuid, 1
    UNION ALL
    SELECT c.id, sub.depth + 1 FROM knowledge.articles c JOIN sub ON c.parent_id = sub.id WHERE sub.depth < 64
)
SELECT EXISTS (
    SELECT 1 FROM sub JOIN knowledge.articles a ON a.id = sub.id
    WHERE a.internal_permission IS NOT NULL AND a.internal_permission <> 'write'
      AND NOT EXISTS (SELECT 1 FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write')
) AS orphaned;
