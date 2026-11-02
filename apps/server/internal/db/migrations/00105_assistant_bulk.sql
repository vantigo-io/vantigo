-- +goose Up
-- The Assistant makes changes in bulk (the Assistant design S13; delivery C): a
-- frozen selection of items, the one change to make to each, and the job that
-- makes it in the API process, as the person, after a preview, a canary of five
-- and the person's own go-ahead.

-- A pending action is one change (the default) or a bulk plan. A bulk action
-- stays confirmed, with no outcome, for as long as its job lasts, and the job
-- has its own claim, so the sweep that closes a claimed single change whose
-- outcome was never recorded leaves it alone.
ALTER TABLE assistant.pending_actions ADD COLUMN kind varchar(10) NOT NULL DEFAULT 'change'
    CHECK (kind IN ('change', 'bulk'));
DROP INDEX assistant.ix_pending_actions_claimed;
CREATE INDEX ix_pending_actions_claimed ON assistant.pending_actions (decided_at)
    WHERE status = 'confirmed' AND outcome IS NULL AND kind = 'change';

-- A decided bulk card keeps no value either: each sample row keeps its label and
-- the names of its fields, as a decided single card keeps the names of its.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assistant.redact_preview(preview jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN b.base ? 'bulk' AND jsonb_typeof(b.base -> 'bulk') = 'object' THEN
        jsonb_set(b.base, '{bulk,sample}', COALESCE((
            SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
                'label', s -> 'label',
                'route', s -> 'route',
                'fields', COALESCE((SELECT jsonb_agg(jsonb_build_object('name', f -> 'name'))
                                    FROM jsonb_array_elements(COALESCE(s -> 'fields', '[]'::jsonb)) AS f), '[]'::jsonb))))
            FROM jsonb_array_elements(COALESCE(b.base -> 'bulk' -> 'sample', '[]'::jsonb)) AS s), '[]'::jsonb))
        ELSE b.base END
    FROM (SELECT jsonb_set(
            preview - 'explain',
            '{fields}',
            COALESCE((SELECT jsonb_agg(jsonb_build_object('name', f -> 'name'))
                      FROM jsonb_array_elements(COALESCE(preview -> 'fields', '[]'::jsonb)) AS f), '[]'::jsonb)) AS base) AS b
$$;
-- +goose StatementEnd

-- What a finished job keeps of the plan: the operations, the names of what was
-- set and which item field fed which parameter, never the values or the filters.
-- +goose StatementBegin
CREATE FUNCTION assistant.redact_bulk_template(template jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_build_object(
        'operationId', template -> 'operationId',
        'bind', COALESCE(template -> 'bind', '{}'::jsonb),
        'fields', COALESCE(template -> 'fields', '[]'::jsonb))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION assistant.redact_bulk_selector(selector jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_build_object(
        'operationId', selector -> 'operationId',
        'query', COALESCE((SELECT jsonb_agg(k) FROM jsonb_object_keys(COALESCE(selector -> 'query', '{}'::jsonb)) AS k), '[]'::jsonb))
$$;
-- +goose StatementEnd

-- A bulk job (S13), private like everything else here: every query filters by
-- user_id, and it goes with its conversation (retention and the erasure sweep
-- delete conversations, and the cascade takes the job and its items).
--
-- state: previewed (the card waits for Start), canary (the first items run),
-- awaiting_continue (the canary is done, or the grant was about to lapse: the
-- person decides), running, stopping (a stop was asked and takes effect between
-- chunks), done, stopped, failed, expired (nobody went on within 24 hours).
--
-- selector and template hold the person's filters and values until the job
-- ends; the end redacts them. total counts the frozen items; processed =
-- succeeded + failed + skipped. claimed_by and claimed_until are the claim of
-- the one API replica running the job: a lapsed claim is taken over by the next
-- replica that sees it, from the next pending item. grant_id is the assistant
-- grant the job's calls carry (no secret; identity holds the row) and
-- grant_expires_at when it lapses. pause_reason says why a job waits for
-- Continue ("grant_expiring") and error_code why it ended other than done
-- (cancelled, stopped_by_person, narrowed, session_ended, internal).
CREATE TABLE assistant.bulk_jobs (
    id                uuid         PRIMARY KEY,
    pending_action_id uuid         NOT NULL UNIQUE REFERENCES assistant.pending_actions (id) ON DELETE CASCADE,
    run_id            uuid         NOT NULL REFERENCES assistant.runs (id) ON DELETE CASCADE,
    conversation_id   uuid         NOT NULL REFERENCES assistant.conversations (id) ON DELETE CASCADE,
    user_id           uuid         NOT NULL,
    operation_id      text         NOT NULL,
    selector          jsonb        NOT NULL,
    template          jsonb        NOT NULL,
    state             varchar(20)  NOT NULL DEFAULT 'previewed'
                      CHECK (state IN ('previewed', 'canary', 'awaiting_continue', 'running', 'stopping', 'done', 'stopped', 'failed', 'expired')),
    total             integer      NOT NULL CHECK (total >= 0),
    processed         integer      NOT NULL DEFAULT 0,
    succeeded         integer      NOT NULL DEFAULT 0,
    failed            integer      NOT NULL DEFAULT 0,
    skipped           integer      NOT NULL DEFAULT 0,
    stop_requested    boolean      NOT NULL DEFAULT false,
    grant_id          uuid,
    grant_expires_at  timestamptz,
    claimed_by        text,
    claimed_until     timestamptz,
    pause_reason      varchar(40),
    error_code        varchar(60),
    created_at        timestamptz  NOT NULL,
    started_at        timestamptz,
    ended_at          timestamptz,
    updated_at        timestamptz  NOT NULL
);
CREATE INDEX ix_bulk_jobs_conversation ON assistant.bulk_jobs (conversation_id);
CREATE INDEX ix_bulk_jobs_running ON assistant.bulk_jobs (claimed_until) WHERE state IN ('canary', 'running', 'stopping');
CREATE INDEX ix_bulk_jobs_waiting ON assistant.bulk_jobs (updated_at) WHERE state IN ('previewed', 'awaiting_continue');

-- The frozen items, one row each, in the order the selector read them. item
-- holds the values the item feeds the operation (bound) and its label for the
-- list; status is pending, succeeded, failed or skipped. A failed item keeps the
-- router's status and the problem's code and title, never a response body.
CREATE TABLE assistant.bulk_job_items (
    job_id        uuid         NOT NULL REFERENCES assistant.bulk_jobs (id) ON DELETE CASCADE,
    seq           integer      NOT NULL,
    item          jsonb        NOT NULL,
    status        varchar(10)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed', 'skipped')),
    http_status   integer,
    problem_code  varchar(120),
    problem_title varchar(300),
    done_at       timestamptz,
    PRIMARY KEY (job_id, seq)
);
CREATE INDEX ix_bulk_job_items_failed ON assistant.bulk_job_items (job_id, seq) WHERE status = 'failed';

-- +goose Down
DROP TABLE assistant.bulk_job_items;
DROP TABLE assistant.bulk_jobs;
DROP FUNCTION assistant.redact_bulk_selector(jsonb);
DROP FUNCTION assistant.redact_bulk_template(jsonb);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assistant.redact_preview(preview jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_set(
        preview - 'explain',
        '{fields}',
        COALESCE((SELECT jsonb_agg(jsonb_build_object('name', f -> 'name'))
                  FROM jsonb_array_elements(COALESCE(preview -> 'fields', '[]'::jsonb)) AS f), '[]'::jsonb))
$$;
-- +goose StatementEnd
DROP INDEX assistant.ix_pending_actions_claimed;
CREATE INDEX ix_pending_actions_claimed ON assistant.pending_actions (decided_at) WHERE status = 'confirmed' AND outcome IS NULL;
DELETE FROM assistant.pending_actions WHERE kind = 'bulk';
ALTER TABLE assistant.pending_actions DROP COLUMN kind;
