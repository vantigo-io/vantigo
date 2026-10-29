-- +goose Up
-- Outlook state on close (Outlook connection and Microsoft 365 mail design
-- M11, M12, M17, docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery E, docs/superpowers/plans/2026-10-29-communications-inbound-notifications.md):
-- when a conversation on a Microsoft 365 channel with settings.onClose of
-- 'read' or 'move' closes, a row in communications.mailbox_marks queues an
-- at-least-once, idempotent pass over the conversation's imported inbound
-- messages in Outlook (marking each as read or moving it to the configured
-- folder). Re-closing a reopened conversation resets the row to 'pending' so
-- any newer inbound messages on the thread are marked too.
--
-- lease_id is the outbox's 32-character hex form.
CREATE TABLE communications.mailbox_marks (
    conversation_id uuid         PRIMARY KEY REFERENCES communications.conversations (id) ON DELETE CASCADE,
    operation       varchar(20)  NOT NULL,
    folder          varchar(120),
    status          varchar(20)  NOT NULL DEFAULT 'pending',
    attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at timestamptz  NOT NULL,
    queued_at       timestamptz  NOT NULL,
    completed_at    timestamptz,
    last_error      varchar(40),
    lease_id        varchar(32),
    lease_until     timestamptz,
    CONSTRAINT ck_mailbox_marks_operation CHECK (operation IN ('read', 'move')),
    CONSTRAINT ck_mailbox_marks_status CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'failed'))
);
CREATE INDEX ix_mailbox_marks_due
    ON communications.mailbox_marks (next_attempt_at)
    WHERE status IN ('pending', 'retry', 'processing');

-- +goose Down
DROP TABLE communications.mailbox_marks;
