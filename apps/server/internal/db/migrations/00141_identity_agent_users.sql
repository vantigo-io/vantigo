-- +goose Up
-- Users of kind agent (virtual employees design V5,
-- docs/superpowers/specs/2026-11-08-virtual-employees-design.md): a user that
-- is an AI agent rather than a person. It is made only through identity's
-- AgentAccounts slot, has an address that never routes
-- (agent-<uuid>@agents.invalid, RFC 2606) and no credential of any kind, and
-- acts only under an agent grant (00142). Existing users are people.
ALTER TABLE identity.users
    ADD COLUMN kind text NOT NULL DEFAULT 'person' CHECK (kind IN ('person', 'agent'));

-- An agent has no password, no TOTP secret and no second factor to enrol.
ALTER TABLE identity.users
    ADD CONSTRAINT users_agent_no_credentials CHECK (
        kind = 'person' OR (password_hash IS NULL AND totp_secret IS NULL AND NOT totp_enabled)
    );

-- What an agent can never be given, drawn again where the data lives so no
-- path that was missed in the code can grant it: a session, a passkey, a link
-- to an identity provider, an invitation, a SCIM mapping, a delegation, the stewardship of a
-- role, and the Owner and SystemAdmin roles. The Owner role passes every
-- permission check, so an agent holding it would bypass every other lock.
-- Each guards the insert alone, the only write these columns ever get: a BEFORE
-- UPDATE trigger on identity.roles would make an edit of a role lock its row
-- FOR UPDATE, which an assignment in flight must not be blocked by.
-- +goose StatementBegin
CREATE FUNCTION identity.refuse_agent_user() RETURNS trigger LANGUAGE plpgsql AS $function$
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

CREATE TRIGGER sessions_refuse_agent BEFORE INSERT ON identity.sessions
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER login_tickets_refuse_agent BEFORE INSERT ON identity.login_tickets
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER passkeys_refuse_agent BEFORE INSERT ON identity.passkeys
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER invitations_refuse_agent BEFORE INSERT ON identity.invitations
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER oidc_links_refuse_agent BEFORE INSERT ON identity.oidc_links
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER scim_user_mappings_refuse_agent BEFORE INSERT ON identity.scim_user_mappings
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('user_id');
CREATE TRIGGER authorization_delegations_refuse_agent BEFORE INSERT ON identity.authorization_delegations
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('grantee_user_id');
CREATE TRIGGER roles_refuse_agent_steward BEFORE INSERT ON identity.roles
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('steward_user_id');

-- +goose StatementBegin
CREATE FUNCTION identity.refuse_agent_privileged_role() RETURNS trigger LANGUAGE plpgsql AS $function$
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

CREATE TRIGGER user_roles_refuse_agent_privileged BEFORE INSERT ON identity.user_roles
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_privileged_role();

-- +goose Down
DROP TRIGGER user_roles_refuse_agent_privileged ON identity.user_roles;
DROP FUNCTION identity.refuse_agent_privileged_role();
DROP TRIGGER roles_refuse_agent_steward ON identity.roles;
DROP TRIGGER authorization_delegations_refuse_agent ON identity.authorization_delegations;
DROP TRIGGER scim_user_mappings_refuse_agent ON identity.scim_user_mappings;
DROP TRIGGER oidc_links_refuse_agent ON identity.oidc_links;
DROP TRIGGER invitations_refuse_agent ON identity.invitations;
DROP TRIGGER passkeys_refuse_agent ON identity.passkeys;
DROP TRIGGER login_tickets_refuse_agent ON identity.login_tickets;
DROP TRIGGER sessions_refuse_agent ON identity.sessions;
DROP FUNCTION identity.refuse_agent_user();
ALTER TABLE identity.users DROP CONSTRAINT users_agent_no_credentials;
ALTER TABLE identity.users DROP COLUMN kind;
