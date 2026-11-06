-- +goose Up
-- Two repairs to the meta module's queues (code review of the Messenger delivery).
--
-- A webhook body whose signature held and that is valid JSON can still be one
-- Postgres refuses as jsonb (an escaped NUL in a string is "untranslatable").
-- Answering 503 would make Meta retry it for ever, so the bytes are kept in
-- raw instead, in a row that is born failed ('unstorable_json') and is never
-- claimed; retention deletes it with the other raw events. payload is
-- therefore nullable, and one of the two is always set.
--
-- meta.pending_files stages the key of an attachment before it is written to
-- the object store, and is cleared in the transaction that inserts the
-- meta.inbound row that names it. A key that stays staged for a day with no
-- inbound row naming it is a file whose transaction never happened (a crash, a
-- rollback): retention deletes the object. The GIN index is for that "does a
-- row name this key" question.
ALTER TABLE meta.webhook_events
    ALTER COLUMN payload DROP NOT NULL,
    ADD COLUMN raw bytea,
    ADD CONSTRAINT webhook_events_body_check CHECK (payload IS NOT NULL OR raw IS NOT NULL);

CREATE TABLE meta.pending_files (
    storage_key  text         PRIMARY KEY,
    created_at   timestamptz  NOT NULL
);

CREATE INDEX ix_meta_pending_files_created ON meta.pending_files (created_at);
CREATE INDEX ix_meta_inbound_storage_keys ON meta.inbound USING gin (storage_keys);

-- +goose Down
DROP INDEX meta.ix_meta_inbound_storage_keys;
DROP TABLE meta.pending_files;
DELETE FROM meta.webhook_events WHERE payload IS NULL;
ALTER TABLE meta.webhook_events
    DROP CONSTRAINT webhook_events_body_check,
    DROP COLUMN raw,
    ALTER COLUMN payload SET NOT NULL;
