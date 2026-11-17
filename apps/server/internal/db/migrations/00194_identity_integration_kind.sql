-- +goose Up
-- Users of kind integration (API access design K1-K3, K44, K47,
-- docs/superpowers/specs/2026-11-17-api-access-design.md): an external
-- application's account, made only through identity's integrations service,
-- that never signs in and acts only with an API key on the request.
ALTER TABLE identity.users DROP CONSTRAINT users_kind_check;
ALTER TABLE identity.users
    ADD CONSTRAINT users_kind_check CHECK (kind IN ('person', 'agent', 'integration'));

-- The constraint already reads kind = 'person' OR (no credential); only its
-- name said agent.
ALTER TABLE identity.users RENAME CONSTRAINT users_agent_no_credentials TO users_nonperson_no_credentials;

-- 00141's guards, widened from agents to every user who is not a person: an
-- integration can never have a session, a login ticket, a passkey, an
-- invitation, an identity provider link, a SCIM mapping, a delegation, the
-- stewardship of a role, or the Owner or SystemAdmin role. The trigger names
-- stay; the functions they call are replaced. 00142's agent-grant guard keeps
-- kind = 'agent': an integration is not an agent.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.refuse_agent_user() RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    target uuid := (to_jsonb(NEW) ->> TG_ARGV[0])::uuid;
BEGIN
    IF target IS NOT NULL AND EXISTS (SELECT 1 FROM identity.users WHERE id = target AND kind <> 'person') THEN
        RAISE EXCEPTION 'identity.%.% may not name a user who is not a person', TG_TABLE_NAME, TG_ARGV[0]
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.refuse_agent_privileged_role() RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM identity.users u, identity.roles r
        WHERE u.id = NEW.user_id AND u.kind <> 'person'
          AND r.id = NEW.role_id AND r.name IN ('Owner', 'SystemAdmin')
    ) THEN
        RAISE EXCEPTION 'identity.user_roles: a user who is not a person may not hold the Owner or SystemAdmin role'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- The rest of what only a person has, guarded where the data lives too, so a
-- refactor that forgets the code's check cannot give one to an agent or an
-- integration: a shared station's operator session, PIN and badge
-- (manufacturing phase 2 M134), an assistant grant, an announced link to the
-- workforce provider and the account it replaces, a password reset token and
-- recovery codes.
CREATE TRIGGER station_operator_sessions_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.station_operator_sessions
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER user_pins_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.user_pins
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER user_badges_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.user_badges
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER assistant_grants_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.assistant_grants
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER expected_links_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.expected_links
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER expected_accounts_refuse_agent BEFORE INSERT OR UPDATE OF replace_user_id ON identity.expected_accounts
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('replace_user_id');
CREATE TRIGGER password_reset_tokens_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.password_reset_tokens
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER recovery_codes_refuse_agent BEFORE INSERT OR UPDATE OF user_id ON identity.recovery_codes
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');

-- An integration's roles have one writer, identity's integrations service,
-- which sets vantigo.integration_roles to the integration's id on its own
-- transaction (set_config(..., true), so it never outlives the transaction
-- on a pooled connection) (K3, K47). Without it a steward of a role could
-- widen a key they hold through Roles & access. An integration holds no
-- access-group membership in this phase at all. Deletes are not guarded:
-- taking a role away only narrows what a key can do.
-- +goose StatementBegin
CREATE FUNCTION identity.refuse_integration_role_write() RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.users WHERE id = NEW.user_id AND kind = 'integration')
       AND (TG_TABLE_NAME <> 'user_roles'
            OR coalesce(current_setting('vantigo.integration_roles', true), '') <> NEW.user_id::text) THEN
        RAISE EXCEPTION 'identity.%: an integration''s roles are changed only through its own screen', TG_TABLE_NAME
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER user_roles_refuse_integration_write BEFORE INSERT OR UPDATE ON identity.user_roles
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_integration_role_write();
CREATE TRIGGER access_group_memberships_refuse_integration_write BEFORE INSERT OR UPDATE ON identity.access_group_memberships
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_integration_role_write();

-- +goose Down
-- +goose StatementBegin
DO $do$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.users WHERE kind = 'integration') THEN
        RAISE EXCEPTION 'identity.users holds integrations; archive and delete them before rolling back 00194';
    END IF;
END;
$do$;
-- +goose StatementEnd

DROP TRIGGER access_group_memberships_refuse_integration_write ON identity.access_group_memberships;
DROP TRIGGER user_roles_refuse_integration_write ON identity.user_roles;
DROP FUNCTION identity.refuse_integration_role_write();
DROP TRIGGER recovery_codes_refuse_agent ON identity.recovery_codes;
DROP TRIGGER password_reset_tokens_refuse_agent ON identity.password_reset_tokens;
DROP TRIGGER expected_accounts_refuse_agent ON identity.expected_accounts;
DROP TRIGGER expected_links_refuse_agent ON identity.expected_links;
DROP TRIGGER assistant_grants_refuse_agent ON identity.assistant_grants;
DROP TRIGGER user_badges_refuse_agent ON identity.user_badges;
DROP TRIGGER user_pins_refuse_agent ON identity.user_pins;
DROP TRIGGER station_operator_sessions_refuse_agent ON identity.station_operator_sessions;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.refuse_agent_privileged_role() RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM identity.users u, identity.roles r
        WHERE u.id = NEW.user_id AND u.kind = 'agent'
          AND r.id = NEW.role_id AND r.name IN ('Owner', 'SystemAdmin')
    ) THEN
        RAISE EXCEPTION 'identity.user_roles: an agent user may not hold the Owner or SystemAdmin role'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.refuse_agent_user() RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    target uuid := (to_jsonb(NEW) ->> TG_ARGV[0])::uuid;
BEGIN
    IF target IS NOT NULL AND EXISTS (SELECT 1 FROM identity.users WHERE id = target AND kind = 'agent') THEN
        RAISE EXCEPTION 'identity.%.% may not name an agent user', TG_TABLE_NAME, TG_ARGV[0]
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

ALTER TABLE identity.users RENAME CONSTRAINT users_nonperson_no_credentials TO users_agent_no_credentials;
ALTER TABLE identity.users DROP CONSTRAINT users_kind_check;
ALTER TABLE identity.users
    ADD CONSTRAINT users_kind_check CHECK (kind IN ('person', 'agent'));
