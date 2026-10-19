-- name: GetSsoSettings :one
-- GetSsoSettings is the single sign-on row (Entra design D1) every reader's
-- 20-second cache reads through: the session query's "is SCIM on", sign-in,
-- the SCIM bearer check, the relying party, the status endpoints and the
-- authority mode. No row means the settings were never written and both
-- OIDC and SCIM are off.
SELECT id, version, oidc_enabled, oidc_provider, oidc_authority, oidc_client_id,
       oidc_client_secret_sealed, oidc_client_secret_set_at, oidc_workload_token_file,
       oidc_allowed_domains, oidc_display_name,
       scim_enabled, scim_token_sha256, scim_token_set_at,
       scim_previous_token_sha256, scim_previous_expires_at,
       imported_from_environment, updated_at, updated_by_user_id
FROM identity.sso_settings
WHERE id = 1;

-- name: LockSsoSettings :one
-- LockSsoSettings reads the row holding it, which the caller has just
-- ensured exists (EnsureSsoSettings), so two Owners' saves queue and each
-- compares its If-Match with the version it actually replaces, and two API
-- processes starting at once import the environment once.
SELECT id, version, oidc_enabled, oidc_provider, oidc_authority, oidc_client_id,
       oidc_client_secret_sealed, oidc_client_secret_set_at, oidc_workload_token_file,
       oidc_allowed_domains, oidc_display_name,
       scim_enabled, scim_token_sha256, scim_token_set_at,
       scim_previous_token_sha256, scim_previous_expires_at,
       imported_from_environment, updated_at, updated_by_user_id
FROM identity.sso_settings
WHERE id = 1
FOR UPDATE;

-- name: EnsureSsoSettings :exec
-- EnsureSsoSettings creates the row, version 0 and everything off, when there
-- is none, so a save or the import has a row to lock; an existing row is
-- left as it is.
INSERT INTO identity.sso_settings (id, version, updated_at)
VALUES (1, 0, @now::timestamptz)
ON CONFLICT (id) DO NOTHING;

-- name: SaveSsoSettings :exec
-- SaveSsoSettings writes the whole row the caller holds (LockSsoSettings)
-- and moves its version on, so every reader's cache and the relying party
-- see a different version at their next read. Every settable column is a
-- parameter: a caller changing one block passes the other as it read it.
-- A nil oidc_allowed_domains is stored as the empty list the column holds.
UPDATE identity.sso_settings
SET version = version + 1,
    oidc_enabled = @oidc_enabled,
    oidc_provider = @oidc_provider,
    oidc_authority = @oidc_authority,
    oidc_client_id = @oidc_client_id,
    oidc_client_secret_sealed = @oidc_client_secret_sealed,
    oidc_client_secret_set_at = @oidc_client_secret_set_at,
    oidc_workload_token_file = @oidc_workload_token_file,
    oidc_allowed_domains = coalesce(@oidc_allowed_domains::text[], '{}'),
    oidc_display_name = @oidc_display_name,
    scim_enabled = @scim_enabled,
    scim_token_sha256 = @scim_token_sha256,
    scim_token_set_at = @scim_token_set_at,
    scim_previous_token_sha256 = @scim_previous_token_sha256,
    scim_previous_expires_at = @scim_previous_expires_at,
    imported_from_environment = @imported_from_environment,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by_user_id
WHERE id = 1;

-- name: CountOidcLinksByIssuer :one
-- CountOidcLinksByIssuer counts the people linked to the provider under
-- @issuer, the normalised issuer the links are keyed on: a save that changes
-- the authority, or turns OIDC off, while it is above zero needs the Owner's
-- confirmation (oidc_links_exist), since each of them would be provisioned
-- anew at their next sign-in.
SELECT count(*)::int
FROM identity.oidc_links
WHERE issuer = @issuer;
