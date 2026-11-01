-- +goose Up
-- Assistant grants (the Assistant design S3,
-- docs/superpowers/specs/2026-11-01-assistant-design.md): what the assistant's
-- principal is. When a person sends the assistant a message, identity issues a
-- grant bound to their current session, the conversation and the run. The
-- dispatcher that calls the API on the person's behalf carries the grant's id
-- on the in-process request's context (never a header or a cookie, so no
-- network input can present it) and Access.Check answers the session's own
-- principal for it, for as long as the grant is current and the session is
-- valid: signing out, the session's expiry, a disabled or locked user, the
-- grant's own expires_at and revoked_at each end it at the next step.
--
-- There is no secret: the id alone is not a credential, because it is only
-- ever read from a context a handler cannot be handed from outside. The
-- conversation and run ids belong to the assistant module and have no foreign
-- key here, as identity's other audit columns have none; the grant records
-- them for Principal.Via, so a module's audit can say which run acted.
--
-- Both foreign keys cascade: a session purged or a user erased takes its
-- grants with it.
CREATE TABLE identity.assistant_grants (
    id              uuid        PRIMARY KEY,
    user_id         uuid        NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    session_id      uuid        NOT NULL REFERENCES identity.sessions (id) ON DELETE CASCADE,
    conversation_id uuid        NOT NULL,
    run_id          uuid        NOT NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz
);
CREATE INDEX ix_assistant_grants_user_id ON identity.assistant_grants (user_id);
CREATE INDEX ix_assistant_grants_session_id ON identity.assistant_grants (session_id);

-- +goose Down
DROP TABLE identity.assistant_grants;
