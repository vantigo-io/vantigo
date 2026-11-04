-- Gmail mail channels (Google Workspace design G33, G34): the Owner's switch
-- and the mailboxes ChannelMailbox.Check resolved.

-- name: SetMailChannels :one
-- SetMailChannels sets the Mail channels switch. It leaves version alone, as
-- the If-Match'd settings are not written.
UPDATE google.connection SET mail_channels = @enabled WHERE id = 1
RETURNING *;

-- name: GetChannelMailbox :one
-- GetChannelMailbox is the mailbox Check recorded under the Directory id.
SELECT * FROM google.channel_mailboxes WHERE google_user_id = @google_user_id;

-- name: FirstChannelMailbox :one
-- FirstChannelMailbox is the mailbox checked longest ago: the subject Test
-- connection asks the Gmail scopes' tokens as.
SELECT * FROM google.channel_mailboxes ORDER BY checked_at, google_user_id LIMIT 1;

-- name: CountChannelMailboxes :one
-- CountChannelMailboxes is how many mailboxes Check recorded.
SELECT count(*) FROM google.channel_mailboxes;

-- name: UpsertChannelMailbox :exec
-- UpsertChannelMailbox records a mailbox Check resolved, or refreshes its
-- address and when it was checked.
INSERT INTO google.channel_mailboxes (google_user_id, address, checked_at)
VALUES (@google_user_id, @address, @checked_at)
ON CONFLICT (google_user_id) DO UPDATE SET address = EXCLUDED.address, checked_at = EXCLUDED.checked_at;

-- name: DeleteChannelMailbox :exec
-- DeleteChannelMailbox forgets a mailbox; nothing is sent through it again
-- until another Check.
DELETE FROM google.channel_mailboxes WHERE google_user_id = @google_user_id;

-- name: AccountExists :one
-- AccountExists reports whether the Directory id is an account the module
-- keeps for a person Staff manages, in any state: its mailbox is that
-- person's own (P2).
SELECT EXISTS (SELECT 1 FROM google.accounts WHERE user_id = @user_id) AS held;
