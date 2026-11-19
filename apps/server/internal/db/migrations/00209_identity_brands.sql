-- +goose Up
-- Brands (brands design B5): the workspace's look, one row per brand. A
-- version (name, title, colour inputs, generated tokens, logos, support) is a
-- jsonb document validated by a Go type, because a published version and a
-- draft are the same shape and a brand is read whole, never queried by a token.
-- Editing writes draft; published is untouched until a publish. The default
-- brand always exists: the first API mount that finds none inserts it (B30),
-- and the unique partial index below is what makes that race-safe.
CREATE TABLE identity.brands (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    is_default         boolean     NOT NULL DEFAULT false,
    state              text        NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'published', 'archived')),
    published          jsonb,
    published_version  integer     NOT NULL DEFAULT 0 CHECK (published_version >= 0),
    draft              jsonb,
    -- revision is the optimistic-concurrency token (If-Match), bumped by every write.
    revision           integer     NOT NULL DEFAULT 1 CHECK (revision >= 1),
    -- name_key is the lower-cased name of the version a person last saved, kept
    -- equal by the writer, so a name is unique without case.
    name_key           text        NOT NULL,
    -- The one-time import of APP_TITLE, APP_LOGO_URL and APP_SUPPORT_* (B30):
    -- import_started_at is the lease of whoever runs the logo step,
    -- imported_env_at is set when the import has run to completion, and
    -- import_notice carries the reason the logo could not be imported.
    import_started_at  timestamptz,
    imported_env_at    timestamptz,
    import_notice      text,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    updated_by_user_id uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    CONSTRAINT ck_brands_published_state CHECK (state = 'draft' OR published IS NOT NULL)
);

CREATE UNIQUE INDEX ux_brands_default ON identity.brands (is_default) WHERE is_default;
CREATE UNIQUE INDEX ux_brands_name_key ON identity.brands (name_key);

-- The asset registry (B6): every logo ever stored, named by the hash of its
-- bytes. A version's logo slot names a sha256 that exists here; objects are
-- never deleted because a document may snapshot one.
CREATE TABLE identity.brand_logos (
    sha256       char(64)    PRIMARY KEY CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    content_type text        NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg')),
    object_key   text        NOT NULL,
    created_at   timestamptz NOT NULL
);

-- +goose Down
DROP TABLE identity.brand_logos;
DROP TABLE identity.brands;
