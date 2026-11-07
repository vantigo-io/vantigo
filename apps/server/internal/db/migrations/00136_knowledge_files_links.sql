-- +goose Up
-- Knowledge's editor comfort (knowledge design §4, delivery K-B): the files
-- uploaded into an article (K19), the links between articles that backlinks
-- read (K21), and when a person was last told they were mentioned in an
-- article (K20).

-- A file uploaded into an article (K19). Its bytes are in the object store
-- under the module's scope, at object_key, a random name never derived from
-- filename. is_image is a JPEG or a PNG whose metadata was stripped; every
-- other type is an attachment, served as a download. A file belongs to one
-- article: a document may only name its own article's files.
CREATE TABLE knowledge.article_files (
    id           uuid        PRIMARY KEY,
    article_id   uuid        NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    object_key   text        NOT NULL UNIQUE,
    filename     text        NOT NULL,
    content_type text        NOT NULL,
    size         bigint      NOT NULL,
    is_image     boolean     NOT NULL,
    created_by   uuid,
    created_at   timestamptz NOT NULL,
    CONSTRAINT ck_article_files_size CHECK (size >= 0),
    CONSTRAINT ck_article_files_filename CHECK (char_length(filename) BETWEEN 1 AND 255)
);
CREATE INDEX ix_article_files_article ON knowledge.article_files (article_id);

-- The objects whose rows are gone (K19): a file row is deleted — by the
-- retention worker, or with its article — in a transaction the object store
-- cannot join, so the trigger queues its key here and the retention worker
-- removes the object after the commit, then the row.
CREATE TABLE knowledge.file_object_deletions (
    object_key text        PRIMARY KEY,
    queued_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION knowledge.queue_file_object_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO knowledge.file_object_deletions (object_key) VALUES (OLD.object_key)
    ON CONFLICT (object_key) DO NOTHING;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER tr_article_files_queue_object_deletion
AFTER DELETE ON knowledge.article_files
FOR EACH ROW EXECUTE FUNCTION knowledge.queue_file_object_deletion();

-- The links between articles (K21), rewritten from a document's articleLink
-- nodes on every save, to the targets that exist. to_id has no foreign key:
-- a link to an article deleted for good is the editor's "Deleted".
CREATE TABLE knowledge.article_links (
    from_id uuid NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    to_id   uuid NOT NULL,
    PRIMARY KEY (from_id, to_id)
);
CREATE INDEX ix_article_links_to ON knowledge.article_links (to_id);

-- When a person was last told they were mentioned in an article (K20): once
-- per person per article per hour.
CREATE TABLE knowledge.mention_notices (
    article_id  uuid        NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL,
    notified_at timestamptz NOT NULL,
    PRIMARY KEY (article_id, user_id)
);

-- +goose Down
DROP TABLE knowledge.mention_notices;
DROP TABLE knowledge.article_links;
DROP TRIGGER tr_article_files_queue_object_deletion ON knowledge.article_files;
DROP FUNCTION knowledge.queue_file_object_deletion();
DROP TABLE knowledge.file_object_deletions;
DROP TABLE knowledge.article_files;
