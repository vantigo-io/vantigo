-- name: ListPages :many
SELECT * FROM meta.pages ORDER BY name, page_id;

-- name: GetPage :one
SELECT * FROM meta.pages WHERE page_id = @page_id;

-- name: UpsertPage :one
-- The same Page added again replaces its token and clears a rejection.
INSERT INTO meta.pages (page_id, name, token_sealed, state, last_error, added_at, added_by_user_id, checked_at)
VALUES (@page_id, @name, @token_sealed, 'active', NULL, @now, @added_by, @now)
ON CONFLICT (page_id) DO UPDATE SET
    name         = EXCLUDED.name,
    token_sealed = EXCLUDED.token_sealed,
    state        = 'active',
    last_error   = NULL,
    checked_at   = EXCLUDED.checked_at
RETURNING *;

-- name: DeletePage :execrows
DELETE FROM meta.pages WHERE page_id = @page_id;

-- name: SetPageState :exec
UPDATE meta.pages SET state = @state, last_error = sqlc.narg('last_error'), checked_at = @now
WHERE page_id = @page_id;

-- name: SetPageName :exec
UPDATE meta.pages SET name = @name, checked_at = @now WHERE page_id = @page_id;
