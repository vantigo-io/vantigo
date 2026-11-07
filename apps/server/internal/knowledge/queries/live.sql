-- name: CanReadArticle :one
-- Whether userID may read the article (K4) on the materialised access
-- (K5): the nearest member row for them, else the effective default, and
-- never a trashed article (K10).
SELECT EXISTS (
    SELECT 1
    FROM knowledge.articles a
    JOIN knowledge.article_access_default d ON d.article_id = a.id
    LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = sqlc.arg(user_id)::uuid
    WHERE a.id = sqlc.arg(article_id)::uuid
      AND a.trashed_at IS NULL
      AND coalesce(x.permission, d.permission) <> 'none'
) AS readable;
