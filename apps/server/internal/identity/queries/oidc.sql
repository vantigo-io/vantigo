-- name: GetOidcLinkedUser :one
-- GetOidcLinkedUser is .NET's FindByLoginAsync(normalizedIssuer, sub)
-- (EA/WorkforceOidcEndpoints.cs:85): the local account a federated identity
-- is linked to, keyed on the normalized issuer and the case-sensitive
-- subject, with what completion decides on: whether the account is
-- unavailable (disabled, or locked out) and whether TOTP is enrolled.
SELECT u.id, u.is_disabled, u.lockout_end, u.totp_enabled
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
