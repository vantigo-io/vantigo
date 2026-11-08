-- +goose Up
-- Knowledge's live co-editing (knowledge design §5, delivery K-C, K27): the
-- update log of an article. The server relays and stores opaque Yjs updates;
-- it never decodes one. seq is per article, assigned as max(seq) + 1 under the
-- article's row lock. Compaction replaces the rows up to a seq with one row at
-- that seq, so a log is contiguous from its first remaining seq. created_by is
-- set null for an erased user by the retention sweep (K17). A log is never
-- needed for safety: a seed always rebuilds from the article's JSON.
CREATE TABLE knowledge.article_updates (
    article_id uuid        NOT NULL REFERENCES knowledge.articles (id) ON DELETE CASCADE,
    seq        bigint      NOT NULL,
    data       bytea       NOT NULL,
    created_by uuid,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (article_id, seq),
    CONSTRAINT ck_article_updates_seq CHECK (seq >= 1)
);

CREATE INDEX ix_article_updates_created_by ON knowledge.article_updates (created_by) WHERE created_by IS NOT NULL;

-- The log's state on the article row, kept under the article's row lock
-- (K27, K32): doc_epoch (from 1; 0 on the wire means "none held") counts the resets of the log, so a client that holds
-- a copy from before one is told (a seq alone is reused after a reset);
-- update_rows and update_bytes are the log's size, so a write never scans the
-- log; last_compaction_ask_at spaces the requests to compact across replicas.
ALTER TABLE knowledge.articles
    ADD COLUMN doc_epoch               integer     NOT NULL DEFAULT 1,
    ADD COLUMN update_rows             integer     NOT NULL DEFAULT 0,
    ADD COLUMN update_bytes            bigint      NOT NULL DEFAULT 0,
    ADD COLUMN last_compaction_ask_at  timestamptz;

-- +goose Down
ALTER TABLE knowledge.articles
    DROP COLUMN last_compaction_ask_at,
    DROP COLUMN update_bytes,
    DROP COLUMN update_rows,
    DROP COLUMN doc_epoch;
DROP TABLE knowledge.article_updates;
