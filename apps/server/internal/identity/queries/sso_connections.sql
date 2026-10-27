-- Connecting existing users to the workforce provider (Entra phase 2,
-- delivery B; contracts.SSOConnections): the users not yet connected under
-- the configured issuer, and who already holds a directory account's keys.
-- The caller normalises what it compares: the issuer as the authority, the
-- object id as SCIM's externalId, the UPN as emails are.

-- name: ListConnectCandidates :many
-- ListConnectCandidates is every user who is not pending, holds no OIDC link
-- under @oidc_issuer and no announced link under it, @caller excluded, in
-- ListManagedUsers' order: the Owners first, then by display name
-- ordinally, then by id.
SELECT u.id, u.email, u.employee_number, u.is_disabled
FROM identity.users u
WHERE u.pending_since IS NULL
  AND u.id <> @caller
  AND NOT EXISTS (
      SELECT 1 FROM identity.oidc_links l
      WHERE l.user_id = u.id AND l.issuer = @oidc_issuer::text
  )
  AND NOT EXISTS (
      SELECT 1 FROM identity.expected_links el
      WHERE el.user_id = u.id AND el.issuer = @oidc_issuer::text
  )
ORDER BY EXISTS (
             SELECT 1
             FROM identity.user_roles ur
             JOIN identity.roles r ON r.id = ur.role_id
             WHERE ur.user_id = u.id AND r.name = 'Owner'
         ) DESC,
         u.display_name COLLATE "C", u.id;

-- name: ListKeyHolders :many
-- ListKeyHolders is every holder of an object id in @object_ids or a UPN in
-- @upns (both normalised): an announced account naming either, under
-- @issuer or under none (kind account, no user); an announced link under
-- @issuer (kind expected, with its user); a SCIM mapping whose externalId is
-- one of the object ids or whose userName, normalised as emails are, is one
-- of the UPNs (kind mapped, with its user). object_id and
-- user_principal_name are the holder's keys as stored, the UPN normalised.
-- The announced account comes first so that the union's columns read as
-- nullable: its user is none and its keys either may be.
SELECT 'account'::text AS kind, NULL::uuid AS user_id,
       ea.object_id, ea.user_principal_name
FROM identity.expected_accounts ea
WHERE (ea.issuer IS NULL OR ea.issuer = @issuer::text)
  AND (ea.object_id = ANY(@object_ids::text[]) OR ea.user_principal_name = ANY(@upns::text[]))
UNION ALL
SELECT 'expected'::text, el.user_id, el.object_id, el.user_principal_name
FROM identity.expected_links el
WHERE el.issuer = @issuer::text
  AND (el.object_id = ANY(@object_ids::text[]) OR el.user_principal_name = ANY(@upns::text[]))
UNION ALL
SELECT 'mapped'::text, m.user_id, m.external_id, upper(btrim(m.user_name))
FROM identity.scim_user_mappings m
WHERE m.external_id = ANY(@object_ids::text[]) OR upper(btrim(m.user_name)) = ANY(@upns::text[]);
