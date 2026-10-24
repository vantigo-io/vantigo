-- name: GetOidcLinkedUser :one
-- GetOidcLinkedUser is .NET's FindByLoginAsync(normalizedIssuer, sub)
-- (EA/WorkforceOidcEndpoints.cs:85): the local account a federated identity
-- is linked to, keyed on the normalized issuer and the case-sensitive
-- subject, with what completion decides on: whether the account is
-- unavailable (disabled — a pending user reads as disabled —, or locked out)
-- and whether TOTP is enrolled.
SELECT u.id, (u.is_disabled OR u.pending_since IS NOT NULL)::boolean AS is_disabled, u.lockout_end, u.totp_enabled
FROM identity.oidc_links l
JOIN identity.users u ON u.id = l.user_id
WHERE l.issuer = @issuer AND l.subject = @subject;

-- name: InsertOidcLink :exec
-- InsertOidcLink links a just-provisioned account to its federated identity
-- (.NET's AddLoginAsync, EA/WorkforceOidcEndpoints.cs:235-247). The primary
-- key (issuer, subject) is what makes a concurrent provisioning of the same
-- identity lose.
INSERT INTO identity.oidc_links (issuer, subject, user_id, created_at)
VALUES (@issuer, @subject, @user_id, @now::timestamptz);

-- name: DeleteOidcLinksByUser :execrows
-- DeleteOidcLinksByUser is an Owner's disconnect (Entra phase 2 design L2):
-- every link of the user under the configured issuer goes; a link left by a
-- previously configured provider stays, as it counts for nothing.
DELETE FROM identity.oidc_links WHERE user_id = @user_id AND issuer = @issuer;

-- name: HasOidcLinkUnderIssuer :one
-- HasOidcLinkUnderIssuer reports whether the user is linked under the
-- issuer: an announcement for a linked user is refused, and an arrival that
-- finds its user linked meanwhile fulfils nothing.
SELECT EXISTS (SELECT 1 FROM identity.oidc_links WHERE user_id = @user_id AND issuer = @issuer)::boolean AS linked;
