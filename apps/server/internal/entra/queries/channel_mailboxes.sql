-- The channel mailboxes (Outlook connection design M11): every mailbox
-- ChannelMailbox.Check resolved, by its object id. Send answers only for a
-- mailbox recorded here; Forget deletes the row when Communications' channel
-- leaves the mailbox. Nothing here is a person's.

-- name: UpsertChannelMailbox :exec
-- UpsertChannelMailbox records a mailbox Check reached, or rewrites its
-- address and when it was checked.
INSERT INTO entra.channel_mailboxes (object_id, address, checked_at)
VALUES (@object_id, @address, @checked_at)
ON CONFLICT (object_id) DO UPDATE SET address = EXCLUDED.address, checked_at = EXCLUDED.checked_at;

-- name: GetChannelMailbox :one
SELECT * FROM entra.channel_mailboxes WHERE object_id = @object_id;

-- name: DeleteChannelMailbox :exec
-- DeleteChannelMailbox forgets a mailbox; a mailbox not recorded is done.
DELETE FROM entra.channel_mailboxes WHERE object_id = @object_id;
