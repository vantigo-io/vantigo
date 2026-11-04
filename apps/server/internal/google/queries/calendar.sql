-- Calendar sync on the person's own Google connection (Google Workspace design
-- G30–G32): the secondary calendars the module made, and the push channel
-- each person's primary calendar is watched by.

-- name: GetMarker :one
-- GetMarker is the installation's marker, the vantigo private extended
-- property of every event it writes.
SELECT marker FROM google.connection WHERE id = 1;

-- name: ClosingPersonalHoldsToken :one
-- ClosingPersonalHoldsToken reports whether a closing connection still holds
-- its token: with the switch off, the mailbox provider is in cleanup while one
-- does.
SELECT EXISTS (
    SELECT 1 FROM google.connections WHERE state = 'closing' AND refresh_sealed IS NOT NULL
) AS holds;

-- name: GetCalendar :one
-- GetCalendar is the module's row for the account's calendar, if it made it.
SELECT * FROM google.calendars WHERE google_user_id = @google_user_id AND calendar_id = @calendar_id;

-- name: ListCalendars :many
-- ListCalendars is every calendar the module made in the account, oldest
-- first.
SELECT * FROM google.calendars WHERE google_user_id = @google_user_id ORDER BY created_at, calendar_id;

-- name: InsertCalendar :exec
-- InsertCalendar records a calendar the module made.
INSERT INTO google.calendars (google_user_id, calendar_id, created_at)
VALUES (@google_user_id, @calendar_id, @now::timestamptz)
ON CONFLICT (google_user_id, calendar_id) DO NOTHING;

-- name: DeleteCalendarRow :exec
-- DeleteCalendarRow forgets a calendar the module made, once it is gone.
DELETE FROM google.calendars WHERE google_user_id = @google_user_id AND calendar_id = @calendar_id;

-- name: GetChannel :one
-- GetChannel is the account's push channel, if it has one.
SELECT * FROM google.calendar_channels WHERE google_user_id = @google_user_id;

-- name: UpsertChannel :exec
-- UpsertChannel records the account's push channel, replacing the one it had.
INSERT INTO google.calendar_channels
    (google_user_id, channel_id, resource_id, token_hash, address, expires_at, granted_seconds, notified_at, created_at)
VALUES
    (@google_user_id, @channel_id, @resource_id, @token_hash, @address, @expires_at::timestamptz, @granted_seconds, NULL, @now::timestamptz)
ON CONFLICT (google_user_id) DO UPDATE SET
    channel_id = EXCLUDED.channel_id,
    resource_id = EXCLUDED.resource_id,
    token_hash = EXCLUDED.token_hash,
    address = EXCLUDED.address,
    expires_at = EXCLUDED.expires_at,
    granted_seconds = EXCLUDED.granted_seconds,
    notified_at = NULL,
    created_at = EXCLUDED.created_at;

-- name: DeleteChannel :execrows
-- DeleteChannel forgets the account's channel, only while it is still the one
-- the caller stopped.
DELETE FROM google.calendar_channels WHERE google_user_id = @google_user_id AND channel_id = @channel_id;

-- name: GetChannelByID :one
-- GetChannelByID is the channel a notification names.
SELECT * FROM google.calendar_channels WHERE channel_id = @channel_id;

-- name: MarkChannelNotified :execrows
-- MarkChannelNotified records that Google notified a change on the channel.
UPDATE google.calendar_channels SET notified_at = @now::timestamptz
WHERE channel_id = @channel_id;

-- name: ListChannelsNotified :many
-- ListChannelsNotified is Notified: the accounts whose channel was notified
-- after since, oldest first, at most row_limit.
SELECT google_user_id, notified_at::timestamptz AS notified_at FROM google.calendar_channels
WHERE notified_at > @since::timestamptz
ORDER BY notified_at, google_user_id
LIMIT @row_limit::integer;
