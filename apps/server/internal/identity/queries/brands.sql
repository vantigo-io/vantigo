-- name: InsertDefaultBrand :one
-- InsertDefaultBrand creates the default brand, published, from the built-in
-- values plus the environment's (brand_import.go). The unique partial index on
-- is_default makes it race-safe across replicas: the replica whose insert
-- returns a row owns the import, the others get no row and read the existing
-- one. import_started_at is set at once when the logo step is still to run.
INSERT INTO identity.brands (is_default, state, published, published_version, name_key,
                             import_started_at, imported_env_at, created_at, updated_at)
VALUES (true, 'published', @published::jsonb, 1, @name_key::text,
        sqlc.narg('import_started_at')::timestamptz, sqlc.narg('imported_env_at')::timestamptz,
        @now::timestamptz, @now::timestamptz)
ON CONFLICT (is_default) WHERE is_default DO NOTHING
RETURNING id;

-- name: GetDefaultBrand :one
SELECT id, is_default, state, published, published_version, draft, revision, name_key,
       import_started_at, imported_env_at, import_notice, created_at, updated_at, updated_by_user_id
FROM identity.brands
WHERE is_default;

-- name: GetBrand :one
SELECT id, is_default, state, published, published_version, draft, revision, name_key,
       import_started_at, imported_env_at, import_notice, created_at, updated_at, updated_by_user_id
FROM identity.brands
WHERE id = @id;

-- name: LockBrand :one
SELECT id, is_default, state, published, published_version, draft, revision, name_key,
       import_started_at, imported_env_at, import_notice, created_at, updated_at, updated_by_user_id
FROM identity.brands
WHERE id = @id
FOR UPDATE;

-- name: ListBrands :many
SELECT id, is_default, state, published, published_version, draft, revision, name_key,
       import_started_at, imported_env_at, import_notice, created_at, updated_at, updated_by_user_id
FROM identity.brands
ORDER BY is_default DESC, name_key;

-- name: SaveBrandDraft :exec
-- SaveBrandDraft writes the pending version (and the name key it carries) and
-- moves the revision; the published version is untouched.
UPDATE identity.brands
SET draft = @draft::jsonb, name_key = @name_key::text, revision = revision + 1,
    updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE id = @id;

-- name: PublishBrand :exec
-- PublishBrand makes the draft the published version, version + 1.
UPDATE identity.brands
SET published = @published::jsonb, published_version = published_version + 1, draft = NULL,
    state = 'published', name_key = @name_key::text, revision = revision + 1,
    updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE id = @id;

-- name: DiscardBrandDraft :exec
UPDATE identity.brands
SET draft = NULL, name_key = @name_key::text, revision = revision + 1,
    updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE id = @id;

-- name: ClaimBrandImport :one
-- ClaimBrandImport takes the lease on the default brand's logo import when it
-- has not run to completion and nobody holds the lease: it returns the id to
-- the one caller that wins, so a restart resumes an import a dead replica began.
UPDATE identity.brands
SET import_started_at = @now::timestamptz
WHERE is_default AND imported_env_at IS NULL
  AND (import_started_at IS NULL OR import_started_at < @stale_before::timestamptz)
RETURNING id;

-- name: CompleteBrandImport :exec
UPDATE identity.brands
SET imported_env_at = @now::timestamptz, import_notice = sqlc.narg('notice')
WHERE id = @id AND imported_env_at IS NULL;

-- name: FailBrandImport :exec
-- FailBrandImport records why the logo could not be imported and leaves the
-- import incomplete (imported_env_at stays NULL): the environment's title and
-- logo stay honoured until an admin publishes a logo of their own
-- (CompleteBrandImport), and the next start tries the fetch again.
UPDATE identity.brands SET import_notice = @notice::text
WHERE id = @id AND imported_env_at IS NULL;

-- name: BrandingImported :one
-- BrandingImported is whether the default brand exists and its import of the
-- environment has run to completion (the env is honoured until it has).
SELECT EXISTS (SELECT 1 FROM identity.brands WHERE is_default AND imported_env_at IS NOT NULL) AS imported;

-- name: InsertBrandLogo :exec
INSERT INTO identity.brand_logos (sha256, content_type, object_key, created_at)
VALUES (@sha256::text, @content_type::text, @object_key::text, @now::timestamptz)
ON CONFLICT (sha256) DO NOTHING;

-- name: GetBrandLogo :one
SELECT sha256, content_type, object_key, created_at FROM identity.brand_logos WHERE sha256 = @sha256::text;

-- name: BrandLogoIsPublished :one
-- BrandLogoIsPublished is whether a published brand's published version names
-- the asset (B15): the anonymous route answers nothing else, so it is not a
-- way to read an unpublished draft's logo.
SELECT EXISTS (
    SELECT 1 FROM identity.brands b, jsonb_each(b.published -> 'logos') AS l
    WHERE b.state = 'published' AND l.value ->> 'sha256' = @sha256::text
) AS published;

-- name: SetImportedBrandLogo :exec
-- SetImportedBrandLogo puts the logo imported from APP_LOGO_URL into the
-- default brand's published version, and into its draft when there is one, in
-- one write (B30), moving the version because what the stylesheet and the
-- payload serve has changed.
UPDATE identity.brands
SET published = @published::jsonb, draft = sqlc.narg('draft')::jsonb,
    published_version = published_version + 1, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id;
