-- The connection (Google Workspace design G12): the installation's one row,
-- id 1, seeded by the baseline. The admin to impersonate and the credential
-- kind (PUT /connection) are written under If-Match, under the row's lock
-- (LockConnection), and move version on; so does the Internal web client.
-- What a connection test finds is written with SetConnectionTest, which
-- leaves version alone so a test never turns a person's open form into a 412.

-- name: GetConnection :one
SELECT * FROM google.connection WHERE id = 1;

-- name: LockConnection :one
-- LockConnection takes the row's lock for a write of the settings or the web
-- client, so the If-Match version is judged against the row the write then
-- changes; a key's upload takes it too, so the client_mismatch check and the
-- insert are one step.
SELECT * FROM google.connection WHERE id = 1 FOR UPDATE;

-- name: UpdateConnectionSettings :one
-- UpdateConnectionSettings sets the admin to impersonate and the credential
-- kind. A changed admin (reset) sends the status back to unconfigured and
-- forgets the scopes the last test found, which were another admin's.
UPDATE google.connection SET
    admin_subject = @admin_subject,
    credential_kind = @credential_kind,
    status = CASE WHEN @reset::boolean THEN 'unconfigured' ELSE status END,
    status_reason = CASE WHEN @reset::boolean THEN NULL ELSE status_reason END,
    scopes_granted = CASE WHEN @reset::boolean THEN '{}' ELSE scopes_granted END,
    scopes_missing = CASE WHEN @reset::boolean THEN '{}' ELSE scopes_missing END,
    scopes_unexpected = CASE WHEN @reset::boolean THEN '{}' ELSE scopes_unexpected END,
    last_checked_at = CASE WHEN @reset::boolean THEN NULL ELSE last_checked_at END,
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by_user_id
WHERE id = 1
RETURNING *;

-- name: UpdateOAuthClient :one
-- UpdateOAuthClient sets the Internal web client's id.
UPDATE google.connection SET
    oauth_client_id = @oauth_client_id,
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by_user_id
WHERE id = 1
RETURNING *;

-- name: SetConnectionTest :one
-- SetConnectionTest records what a connection test found: the status, its
-- reason and the scopes, always; the customer, its domains and its name
-- when the organisation answered (null keeps them).
UPDATE google.connection SET
    status = @status,
    status_reason = @status_reason,
    scopes_granted = @scopes_granted,
    scopes_missing = @scopes_missing,
    scopes_unexpected = @scopes_unexpected,
    customer_id = COALESCE(@customer_id, customer_id),
    primary_domain = COALESCE(@primary_domain, primary_domain),
    domains = COALESCE(@domains::text[], domains),
    organisation_name = COALESCE(@organisation_name, organisation_name),
    last_checked_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: SetConnectionFailing :exec
-- SetConnectionFailing records the connection failing for reason outside a
-- test — every key refused on a call — and keeps the rest.
UPDATE google.connection SET status = 'failing', status_reason = @status_reason
WHERE id = 1;
