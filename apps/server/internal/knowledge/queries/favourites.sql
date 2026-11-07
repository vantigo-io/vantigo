-- Favourites (knowledge design K11).

-- name: ListFavourites :many
-- ListFavourites is userIDs favourites they can still read, in their order.
SELECT a.id, a.parent_id, a.position, a.title, a.icon, a.locked,
       d.permission AS default_permission,
       coalesce(x.permission, d.permission)::text AS permission,
       (SELECT count(*) FROM knowledge.article_access w WHERE w.article_id = a.id AND w.permission = 'write')::bigint AS writers,
       EXISTS (
           SELECT 1 FROM knowledge.articles c
           JOIN knowledge.article_access_default cd ON cd.article_id = c.id
           LEFT JOIN knowledge.article_access cx ON cx.article_id = c.id AND cx.user_id = @user_id::uuid
           WHERE c.parent_id = a.id AND c.trashed_at IS NULL AND coalesce(cx.permission, cd.permission) <> 'none'
       ) AS has_children,
       (a.parent_id IS NOT NULL AND coalesce(px.permission, pd.permission, 'none') <> 'none')::boolean AS parent_readable
FROM knowledge.favourites f
JOIN knowledge.articles a ON a.id = f.article_id
JOIN knowledge.article_access_default d ON d.article_id = a.id
LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
LEFT JOIN knowledge.article_access_default pd ON pd.article_id = a.parent_id
LEFT JOIN knowledge.article_access px ON px.article_id = a.parent_id AND px.user_id = @user_id::uuid
WHERE f.user_id = @user_id::uuid
  AND a.trashed_at IS NULL
  AND coalesce(x.permission, d.permission) <> 'none'
ORDER BY f.position, f.article_id;

-- name: FavouritePosition :one
-- FavouritePosition is the position of the favourite of userID, empty when
-- it is not one.
SELECT coalesce((SELECT position FROM knowledge.favourites WHERE user_id = @user_id::uuid AND article_id = @article_id::uuid), '')::text AS position;

-- name: LastFavouritePosition :one
SELECT coalesce(max(position), '')::text AS position FROM knowledge.favourites
WHERE user_id = @user_id::uuid AND article_id <> @exclude::uuid;

-- name: NextFavouritePosition :one
SELECT coalesce(min(position), '')::text AS position FROM knowledge.favourites
WHERE user_id = @user_id::uuid AND article_id <> @exclude::uuid AND position > @after::text;

-- name: InsertFavourite :exec
INSERT INTO knowledge.favourites (user_id, article_id, position, created_at)
VALUES (@user_id::uuid, @article_id::uuid, @position::text, @now::timestamptz)
ON CONFLICT (user_id, article_id) DO NOTHING;

-- name: MoveFavourite :exec
UPDATE knowledge.favourites SET position = @position::text
WHERE user_id = @user_id::uuid AND article_id = @article_id::uuid;

-- name: DeleteFavourite :execrows
DELETE FROM knowledge.favourites WHERE user_id = @user_id::uuid AND article_id = @article_id::uuid;
