-- The announced link (Entra phase 2 design L1): an Owner's word that an
-- existing user holds a directory account, matched on arrival by the object
-- id under the issuer (OIDC's oid, SCIM's externalId) or the UPN (SCIM's
-- userName). The caller normalises what it compares: the object id as
-- SCIM's externalId, the UPN as emails are.

-- name: UpsertExpectedLink :exec
-- UpsertExpectedLink records the announcement for the user, or replaces the
-- one they have, every column with it.
INSERT INTO identity.expected_links (user_id, issuer, object_id, user_principal_name, created_at, created_by_user_id)
VALUES (@user_id, @issuer, sqlc.narg(object_id)::varchar, sqlc.narg(user_principal_name)::varchar,
        @now::timestamptz, @created_by_user_id)
ON CONFLICT (user_id) DO UPDATE SET
    issuer = EXCLUDED.issuer,
    object_id = EXCLUDED.object_id,
    user_principal_name = EXCLUDED.user_principal_name,
    created_at = EXCLUDED.created_at,
    created_by_user_id = EXCLUDED.created_by_user_id;

-- name: DeleteExpectedLink :execrows
-- DeleteExpectedLink withdraws or consumes the user's announcement; no row is
-- none to withdraw.
DELETE FROM identity.expected_links WHERE user_id = @user_id;

-- name: GetExpectedLinkByUser :one
SELECT * FROM identity.expected_links WHERE user_id = @user_id;

-- name: GetExpectedLinkByObjectID :one
-- GetExpectedLinkByObjectID is the announcement naming the object id under
-- the issuer: OIDC's match, and SCIM's by externalId (which checks the
-- issuer is the configured one itself).
SELECT * FROM identity.expected_links
WHERE issuer = @issuer AND object_id = @object_id;

-- name: GetExpectedLinkByUPN :one
-- GetExpectedLinkByUPN is the announcement naming the user principal name
-- under the issuer, compared as stored: SCIM's match by userName, and
-- OIDC's by the token's principal name after the object id.
SELECT * FROM identity.expected_links
WHERE issuer = @issuer AND user_principal_name = @user_principal_name;

-- name: GetExpectedLinkTaker :one
-- GetExpectedLinkTaker is another user whose announcement under the issuer
-- names either of the keys an Owner is about to announce for @user_id — the
-- refusal sso_link_taken before the unique indexes would say it. An
-- announcement under another issuer blocks nothing.
SELECT user_id FROM identity.expected_links
WHERE user_id <> @user_id AND issuer = @issuer
  AND ((sqlc.narg(object_id)::varchar IS NOT NULL AND object_id = sqlc.narg(object_id)::varchar)
    OR (sqlc.narg(user_principal_name)::varchar IS NOT NULL AND user_principal_name = sqlc.narg(user_principal_name)::varchar))
LIMIT 1;

-- name: ExpectedAccountNamesKey :one
-- ExpectedAccountNamesKey reports whether an announced account (Staff's,
-- for a user yet to arrive) names either key: the same arrival cannot fulfil
-- both, so an Owner's announcement of it is refused sso_link_taken.
SELECT EXISTS (
    SELECT 1 FROM identity.expected_accounts
    WHERE (sqlc.narg(object_id)::varchar IS NOT NULL AND object_id = sqlc.narg(object_id)::varchar)
       OR (sqlc.narg(user_principal_name)::varchar IS NOT NULL AND user_principal_name = sqlc.narg(user_principal_name)::varchar)
)::boolean AS named;

-- name: CountExpectedLinksByIssuer :one
-- CountExpectedLinksByIssuer is how many announcements wait under the
-- issuer: an authority change or a turn-off counts them with the links.
SELECT count(*) FROM identity.expected_links WHERE issuer = @issuer;
