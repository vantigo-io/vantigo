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

-- name: TouchChannelMailbox :exec
-- TouchChannelMailbox records that the mailbox was resolved again as the
-- admin and is still the user Check recorded.
UPDATE google.channel_mailboxes SET checked_at = @checked_at WHERE google_user_id = @google_user_id;

-- name: PersonalMailboxHeld :one
-- PersonalMailboxHeld reports whether the Directory id, or the address (lower
-- case), is a person's own mailbox (P2): an account the module keeps for a
-- person Staff manages, in any state, under that id or that primary address,
-- or a person's own connection under that id.
SELECT (
    EXISTS (SELECT 1 FROM google.accounts a
            WHERE a.user_id = @google_user_id OR lower(a.primary_email) = @address::text)
    OR EXISTS (SELECT 1 FROM google.connections c WHERE c.google_user_id = @google_user_id)
)::boolean AS held;
