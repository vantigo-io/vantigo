-- The history (knowledge design K9).

-- name: LatestVersion :one
SELECT version, created_by, created_at FROM knowledge.article_versions
WHERE article_id = @article_id::uuid ORDER BY version DESC LIMIT 1;

-- name: InsertVersion :exec
INSERT INTO knowledge.article_versions (article_id, version, title, content, created_by, created_at)
VALUES (@article_id::uuid, @version::int, @title::text, @content::jsonb, sqlc.narg(created_by)::uuid, @created_at::timestamptz);

-- name: ReplaceVersion :exec
-- ReplaceVersion is a coalesced save: the persons previous version becomes
-- this one.
UPDATE knowledge.article_versions
SET version = @version::int, title = @title::text, content = @content::jsonb, created_at = @created_at::timestamptz
WHERE article_id = @article_id::uuid AND version = @previous::int;

-- name: ListVersions :many
SELECT version, title, created_by, created_at FROM knowledge.article_versions
WHERE article_id = @article_id::uuid ORDER BY version DESC;

-- name: GetVersion :one
SELECT version, title, content, created_by, created_at FROM knowledge.article_versions
WHERE article_id = @article_id::uuid AND version = @version::int;
