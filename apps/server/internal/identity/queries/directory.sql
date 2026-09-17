-- name: DirectoryUser :one
SELECT id, display_name, is_disabled FROM identity.users WHERE id = $1;

-- name: DirectoryUsers :many
SELECT id, display_name, is_disabled FROM identity.users WHERE id = ANY($1::uuid[]);

-- name: DirectorySearchUsers :many
SELECT id, display_name, is_disabled FROM identity.users
WHERE NOT is_disabled AND display_name ILIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
ORDER BY display_name, id
LIMIT sqlc.arg(max_rows);
