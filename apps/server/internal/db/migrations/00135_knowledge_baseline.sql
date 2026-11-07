-- +goose Up
-- Knowledge, the company's own articles (knowledge design §7, delivery K-A):
-- the articles in their tree (K3), the explicit members (K4), the effective
-- access materialised from both (K5, K16), the coalesced versions (K9) and
-- each person's favourites (K11). K-B adds files and links, K-C the
-- co-editing updates, in migrations of their own.
--
-- A user is identity's, an opaque uuid read through contracts.UserDirectory
-- (docs/src/content/docs/en/contributing/module-boundaries.md rule 4), so no
-- key crosses a schema. An author column is nullable: the erasure of K17 sets
-- it to null, and the interface shows "Former user". No extension is created
-- (K12): both text-search configurations are built into Postgres.
CREATE SCHEMA knowledge;

-- An article, a page in the tree (K3). position is a fractional-index key
-- ordered bytewise among its siblings, so a move never renumbers them; the C
-- collation keeps Postgres' order the server's. internal_permission is the
-- default for everyone (K4): null inherits the parent's, and a root's is never
-- null. version is the concurrency token of K9. A trashed article carries the
-- trash group it went with in trash_root (K10). search is K12's index: the
-- title and the first 200 000 characters of the text, in Norwegian and
-- English at once.
CREATE TABLE knowledge.articles (
    id                  uuid        PRIMARY KEY,
    parent_id           uuid        REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    position            text        COLLATE "C" NOT NULL,
    title               text        NOT NULL DEFAULT '',
    icon                text,
    content             jsonb       NOT NULL,
    content_text        text        NOT NULL DEFAULT '',
    internal_permission text,
    version             integer     NOT NULL DEFAULT 1,
    locked              boolean     NOT NULL DEFAULT false,
    ai_hidden           boolean     NOT NULL DEFAULT false,
    created_by          uuid,
    created_at          timestamptz NOT NULL,
    updated_by          uuid,
    updated_at          timestamptz NOT NULL,
    trashed_at          timestamptz,
    trashed_by          uuid,
    trash_root          uuid        REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    search              tsvector    GENERATED ALWAYS AS (
        setweight(to_tsvector('norwegian', coalesce(title, '')), 'A')
        || setweight(to_tsvector('english', coalesce(title, '')), 'A')
        || setweight(to_tsvector('norwegian', left(coalesce(content_text, ''), 200000)), 'B')
        || setweight(to_tsvector('english', left(coalesce(content_text, ''), 200000)), 'B')
    ) STORED,
    CONSTRAINT ck_articles_not_own_parent CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT ck_articles_internal_permission CHECK (internal_permission IS NULL OR internal_permission IN ('write', 'read', 'none')),
    CONSTRAINT ck_articles_root_permission CHECK (parent_id IS NOT NULL OR internal_permission IS NOT NULL),
    CONSTRAINT ck_articles_title CHECK (char_length(title) <= 200),
    CONSTRAINT ck_articles_icon CHECK (icon IS NULL OR char_length(icon) BETWEEN 1 AND 16),
    CONSTRAINT ck_articles_position CHECK (position <> ''),
    CONSTRAINT ck_articles_version CHECK (version >= 1),
    CONSTRAINT ck_articles_trash CHECK ((trashed_at IS NULL) = (trash_root IS NULL))
);
CREATE INDEX ix_articles_siblings ON knowledge.articles (parent_id, position) WHERE trashed_at IS NULL;
CREATE INDEX ix_articles_search ON knowledge.articles USING gin (search);
CREATE INDEX ix_articles_trash_root ON knowledge.articles (trash_root) WHERE trash_root IS NOT NULL;
CREATE INDEX ix_articles_trashed_at ON knowledge.articles (trashed_at) WHERE trashed_at IS NOT NULL;

-- The explicit members (K4), inherited down the tree: the nearest row for a
-- person on the path to the root decides, so none on a descendant shuts one
-- person out of a subtree.
CREATE TABLE knowledge.article_members (
    article_id uuid NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL,
    permission text NOT NULL,
    PRIMARY KEY (article_id, user_id),
    CONSTRAINT ck_article_members_permission CHECK (permission IN ('write', 'read', 'none'))
);
CREATE INDEX ix_article_members_user ON knowledge.article_members (user_id);

-- The effective default of every article, materialised (K5): the nearest
-- internal_permission on the path to the root. ai_hidden is true when the
-- article or any ancestor is hidden from the Assistant (K16, monotone).
CREATE TABLE knowledge.article_access_default (
    article_id uuid    PRIMARY KEY REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    permission text    NOT NULL,
    ai_hidden  boolean NOT NULL DEFAULT false,
    CONSTRAINT ck_article_access_default_permission CHECK (permission IN ('write', 'read', 'none'))
);

-- Every person's effective access through a membership, materialised (K5):
-- the nearest member row for them on the path to the root. A person without
-- a row here has the article's effective default; none overrides it.
CREATE TABLE knowledge.article_access (
    article_id uuid NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL,
    permission text NOT NULL,
    PRIMARY KEY (article_id, user_id),
    CONSTRAINT ck_article_access_permission CHECK (permission IN ('write', 'read', 'none'))
);
CREATE INDEX ix_article_access_user ON knowledge.article_access (user_id, article_id);

-- The history (K9): one row per save, coalesced — a save by the same person
-- less than 10 minutes after their previous one replaces that row. The
-- knowledge.retention worker prunes rows older than 365 days beyond an
-- article's newest 20.
CREATE TABLE knowledge.article_versions (
    article_id uuid        NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    version    integer     NOT NULL,
    title      text        NOT NULL,
    content    jsonb       NOT NULL,
    created_by uuid,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (article_id, version),
    CONSTRAINT ck_article_versions_version CHECK (version >= 1)
);
CREATE INDEX ix_article_versions_created_at ON knowledge.article_versions (created_at);

-- A person's favourites (K11), ordered by a fractional-index key as the tree
-- is.
CREATE TABLE knowledge.favourites (
    user_id    uuid        NOT NULL,
    article_id uuid        NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    position   text        COLLATE "C" NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, article_id),
    CONSTRAINT ck_favourites_position CHECK (position <> '')
);
CREATE INDEX ix_favourites_user_position ON knowledge.favourites (user_id, position);
CREATE INDEX ix_favourites_article ON knowledge.favourites (article_id);

-- +goose Down
DROP SCHEMA knowledge CASCADE;
