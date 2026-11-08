-- +goose Up
-- More roles (virtual employees design, delivery C: V29, V30): wakeups and
-- schedules, runs about a subject that is not a conversation, the modes for
-- starting a conversation and for changes, and the templates' own fields.

-- A role's template: the four new kinds beside the front desk.
ALTER TABLE agents.roles DROP CONSTRAINT roles_template_check;
ALTER TABLE agents.roles ADD CONSTRAINT roles_template_check
    CHECK (template IN ('front_desk', 'sales_followup', 'reminder_replies', 'timesheet_reminders', 'project_digest'));

-- start_mode decides a conversation the agent starts, change_mode the changes a
-- template marks as safe to automate (V29). Both are Review until the Owner
-- loosens them; the threshold is shared with reply_mode. wait_days is the sales
-- follow-up's wait after a quote is sent; recipient_user_id is the person the
-- project digest goes to. The schedule is weekly (schedule_day is the ISO
-- weekday, 1 Monday to 7 Sunday) or monthly (the day of the month, 1 to 28) at
-- schedule_time in the business time zone; schedule_fired_at is the last
-- occurrence it woke the role for, so that each occurrence wakes it once.
ALTER TABLE agents.roles
    ADD COLUMN start_mode        varchar(12) NOT NULL DEFAULT 'review' CHECK (start_mode IN ('review', 'auto_above', 'auto')),
    ADD COLUMN change_mode       varchar(12) NOT NULL DEFAULT 'review' CHECK (change_mode IN ('review', 'auto_above', 'auto')),
    ADD COLUMN wait_days         smallint    NOT NULL DEFAULT 7 CHECK (wait_days BETWEEN 1 AND 60),
    ADD COLUMN recipient_user_id uuid,
    ADD COLUMN schedule_kind     varchar(8)  CHECK (schedule_kind IN ('weekly', 'monthly')),
    ADD COLUMN schedule_day      smallint    CHECK (schedule_day BETWEEN 1 AND 28),
    ADD COLUMN schedule_time     varchar(5)  CHECK (schedule_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    ADD COLUMN schedule_fired_at timestamptz,
    ADD CONSTRAINT roles_schedule_whole CHECK ((schedule_kind IS NULL) = (schedule_day IS NULL) AND (schedule_kind IS NULL) = (schedule_time IS NULL));

-- A wakeup (V29): a moment a role is to look at a subject again, written by the
-- follow_up tool, by the trigger (a quote sent N days ago) and by a role's
-- schedule. subject_type is conversation, quote, schedule or person_week;
-- subject_id is the subject's own id as text. customer_id is the customer the
-- subject belongs to, known from the event that made the wakeup, never from a
-- message. taken_at stamps a wakeup the agents-wakeups worker turned into a run
-- request (or dropped); the row is kept until retention.
CREATE TABLE agents.wakeups (
    id           uuid         PRIMARY KEY,
    role_id      uuid         NOT NULL REFERENCES agents.roles (id) ON DELETE CASCADE,
    subject_type varchar(14)  NOT NULL CHECK (subject_type IN ('conversation', 'quote', 'schedule', 'person_week')),
    subject_id   text         NOT NULL,
    customer_id  integer,
    at           timestamptz  NOT NULL,
    reason       varchar(200) NOT NULL DEFAULT '',
    created_at   timestamptz  NOT NULL,
    taken_at     timestamptz
);
CREATE UNIQUE INDEX ux_wakeups_pending ON agents.wakeups (role_id, subject_type, subject_id) WHERE taken_at IS NULL;
CREATE INDEX ix_wakeups_due ON agents.wakeups (at) WHERE taken_at IS NULL;
CREATE INDEX ix_wakeups_taken ON agents.wakeups (taken_at) WHERE taken_at IS NOT NULL;

-- A run about a subject (V29). A run request and a run now name a subject besides
-- the conversation: subject_type conversation (conversation_id is the
-- conversation, subject_id empty), or another type, whose conversation_id is the
-- nil uuid ("no conversation") and whose subject_id is the subject. Requests are
-- coalesced, and one run is active, per role and subject. note is what the
-- wakeup or the event says (a follow-up's reason), read by the run as data.
ALTER TABLE agents.run_queue
    ADD COLUMN subject_type varchar(14) NOT NULL DEFAULT 'conversation',
    ADD COLUMN subject_id   text        NOT NULL DEFAULT '',
    ADD COLUMN customer_id  integer,
    ADD COLUMN note         varchar(200) NOT NULL DEFAULT '';
ALTER TABLE agents.run_queue DROP CONSTRAINT run_queue_reason_check;
ALTER TABLE agents.run_queue ADD CONSTRAINT run_queue_reason_check CHECK (reason IN ('message', 'assigned', 'retry', 'wakeup', 'event'));
DROP INDEX agents.ux_run_queue_untaken;
CREATE UNIQUE INDEX ux_run_queue_untaken ON agents.run_queue (role_id, conversation_id, subject_id) WHERE taken_at IS NULL;

ALTER TABLE agents.runs
    ADD COLUMN subject_type varchar(14) NOT NULL DEFAULT 'conversation',
    ADD COLUMN subject_id   text        NOT NULL DEFAULT '';
DROP INDEX agents.ux_runs_active;
CREATE UNIQUE INDEX ux_runs_active ON agents.runs (role_id, conversation_id, subject_id) WHERE state IN ('queued', 'running');

-- A review item may be a conversation the agent would start (V29).
ALTER TABLE agents.review_items DROP CONSTRAINT review_items_kind_check;
ALTER TABLE agents.review_items ADD CONSTRAINT review_items_kind_check CHECK (kind IN ('reply', 'change', 'start'));

-- +goose Down
ALTER TABLE agents.review_items DROP CONSTRAINT review_items_kind_check;
DELETE FROM agents.review_items WHERE kind = 'start';
ALTER TABLE agents.review_items ADD CONSTRAINT review_items_kind_check CHECK (kind IN ('reply', 'change'));
DROP INDEX agents.ux_runs_active;
DELETE FROM agents.runs WHERE subject_type <> 'conversation';
CREATE UNIQUE INDEX ux_runs_active ON agents.runs (role_id, conversation_id) WHERE state IN ('queued', 'running');
ALTER TABLE agents.runs DROP COLUMN subject_id, DROP COLUMN subject_type;
DROP INDEX agents.ux_run_queue_untaken;
DELETE FROM agents.run_queue WHERE subject_type <> 'conversation' OR reason IN ('wakeup', 'event');
CREATE UNIQUE INDEX ux_run_queue_untaken ON agents.run_queue (role_id, conversation_id) WHERE taken_at IS NULL;
ALTER TABLE agents.run_queue DROP CONSTRAINT run_queue_reason_check;
ALTER TABLE agents.run_queue ADD CONSTRAINT run_queue_reason_check CHECK (reason IN ('message', 'assigned', 'retry'));
ALTER TABLE agents.run_queue DROP COLUMN note, DROP COLUMN customer_id, DROP COLUMN subject_id, DROP COLUMN subject_type;
DROP TABLE agents.wakeups;
ALTER TABLE agents.roles DROP CONSTRAINT roles_schedule_whole;
ALTER TABLE agents.roles
    DROP COLUMN schedule_fired_at, DROP COLUMN schedule_time, DROP COLUMN schedule_day, DROP COLUMN schedule_kind,
    DROP COLUMN recipient_user_id, DROP COLUMN wait_days, DROP COLUMN change_mode, DROP COLUMN start_mode;
DELETE FROM agents.roles WHERE template <> 'front_desk';
ALTER TABLE agents.roles DROP CONSTRAINT roles_template_check;
ALTER TABLE agents.roles ADD CONSTRAINT roles_template_check CHECK (template IN ('front_desk'));
