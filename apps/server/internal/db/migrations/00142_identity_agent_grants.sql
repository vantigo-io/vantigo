-- +goose Up
-- Agent grants (virtual employees design V6,
-- docs/superpowers/specs/2026-11-08-virtual-employees-design.md): what the
-- principal of an agent's run is. The agents module asks identity for a grant
-- for one run of one agent; the dispatcher that calls the API for the agent
-- carries the grant's id on the in-process request's context (never a header
-- or a cookie, so no network input can present it) and Access.Check answers
-- the agent user's own principal for it while the grant is current and the
-- agent is active. Unlike an assistant grant (00102) it is bound to no
-- session: an agent has none.
--
-- There is no secret: the id alone is not a credential, because it is only
-- ever read from a context a handler cannot be handed from outside. A grant
-- lives at most ten minutes, twice a run's ceiling, and is never renewed; a
-- reviewer's approval gets a new one.
--
-- agent_id and run_id belong to the agents module and have no foreign key
-- here; customer_id is the run's customer scope (V9b), customers' id, and has
-- none either. approved_by_user_id is the reviewer whose approval the grant
-- carries, null for a run's own. user_id cascades: an erased user takes its
-- grants with it.
CREATE TABLE identity.agent_grants (
    id                  uuid        PRIMARY KEY,
    user_id             uuid        NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    agent_id            uuid        NOT NULL,
    run_id              uuid        NOT NULL,
    customer_id         integer,
    approved_by_user_id uuid,
    created_at          timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL,
    revoked_at          timestamptz,
    CONSTRAINT agent_grants_ttl CHECK (expires_at > created_at AND expires_at <= created_at + interval '10 minutes')
);
CREATE INDEX ix_agent_grants_user_id ON identity.agent_grants (user_id);

-- A grant is for a user of kind agent, whatever code issues it.
-- +goose StatementBegin
CREATE FUNCTION identity.require_agent_user() RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM identity.users WHERE id = NEW.user_id AND kind = 'agent') THEN
        RAISE EXCEPTION 'identity.agent_grants.user_id must name an agent user'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER agent_grants_require_agent BEFORE INSERT ON identity.agent_grants
    FOR EACH ROW EXECUTE FUNCTION identity.require_agent_user();

-- +goose Down
DROP TABLE identity.agent_grants;
DROP FUNCTION identity.require_agent_user();
