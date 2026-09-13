-- Channels: getCommunicationsChannels, postCommunicationsChannels,
-- getCommunicationsChannelsById, putCommunicationsChannelsById and
-- postCommunicationsChannelsByIdVerify (communications inventory §1.1,
-- §2's CreateChannel/UpdateChannel/VerifyChannel bullets, §15.6's
-- per-channel-credential shape). Every channel this port creates is SMTP
-- (task 4's report explains the Mailgun-branch decision), so
-- channel_credentials always has exactly one row per channel, and the two
-- list/get queries below can inner-join it without ever hiding a channel
-- that lacks one.

-- name: AnyChannelExists :one
-- AnyChannelExists backs CreateChannel's "the first channel ever created is
-- forced default" rule (inventory §2's CreateChannel bullet:
-- `request.IsDefault == true || !await db.Channels.AnyAsync(ct)`).
SELECT EXISTS (SELECT 1 FROM communications.channels) AS any_exists;

-- name: ClearDefaultChannels :exec
-- ClearDefaultChannels demotes every existing default channel, run before
-- inserting a new channel that will itself be the default (CreateChannel:
-- "setting a default demotes every other row in the same call").
UPDATE communications.channels SET is_default = false WHERE is_default;

-- name: ClearOtherDefaultChannels :exec
-- ClearOtherDefaultChannels is ClearDefaultChannels for UpdateChannel,
-- excluding the row being updated (it is set to the new default
-- separately, by UpdateChannel below).
--
-- THE MISSING `AND is_default` IS THE POINT, and removing it is what makes
-- two concurrent "make me the default" updates safe (task 14's constraint
-- audit). With the predicate present, two PUTs on different channels of the
-- same type deadlock-free-but-wrong: under READ COMMITTED the loser's
-- statement takes its snapshot before the winner commits, so the winner's
-- row is still is_default = false in that snapshot, does not match
-- `is_default AND id != @id`, and is never visited — not even by
-- EvalPlanQual, which only re-checks rows the statement actually found. The
-- loser therefore demotes nothing, sets its own row to true, and collides
-- with the winner on ux_channels_type_is_default. That 23505 escapes to
-- httpx.WriteError's host-wide fallback as a bare RFC 7807 409 — the wrong
-- vocabulary for this module, AND a status putCommunicationsChannelsById's
-- contract does not declare at all (200/400/401/403/404 only), so it cannot
-- simply be caught and re-shaped the way CreateChannel's own collision is.
--
-- Without the predicate the statement visits every other row, blocks on the
-- winner's lock, and EvalPlanQual re-checks the winner's NEW version against
-- `id != @id`, which still matches — so the loser demotes the winner and
-- the update proceeds to a correct last-writer-wins outcome with no
-- violation to map. The cost is writing is_default = false over rows that
-- already hold it, on a table with a handful of rows; there is no observable
-- behaviour change in the sequential case.
-- TestUpdateChannel_ConcurrentDefaultRaceLeavesExactlyOneDefault pins it.
UPDATE communications.channels SET is_default = false WHERE id != @id;

-- name: InsertChannel :one
-- InsertChannel is CreateChannel's channel-row insert. provider is always
-- 'smtp' in this port (task 4's Mailgun-branch decision: no non-SMTP
-- channel is ever creatable through this API).
INSERT INTO communications.channels (id, type, address, display_name, provider, is_default, is_active, created_at)
VALUES (@id, @type, @address, @display_name, @provider, @is_default, @is_active, @created_at)
RETURNING id, type, address, display_name, provider, is_default, is_active, created_at;

-- name: InsertChannelCredential :exec
-- InsertChannelCredential is CreateChannel's credential-row insert
-- (inventory §15.6): settings_json is the camelCase-serialized SMTP
-- settings (host/port/useSsl/username), secret_ciphertext the
-- internal/secrets-sealed password, never the plaintext.
INSERT INTO communications.channel_credentials (id, channel_id, settings_json, secret_ciphertext, created_at)
VALUES (@id, @channel_id, @settings_json, @secret_ciphertext, @created_at);

-- name: ListChannels :many
-- ListChannels is GetChannelsEndpoint: a bare array (no pagination),
-- ordered by CreatedAt ascending (inventory §1.1's `:18`/`:25` note).
-- credential_settings_json is always non-NULL for a row this port wrote —
-- the LEFT JOIN, rather than an INNER JOIN, only matters for a
-- hand-inserted fixture row with no credential.
SELECT c.id, c.type, c.address, c.display_name, c.provider, c.is_default, c.is_active, c.created_at,
       cc.settings_json AS credential_settings_json
FROM communications.channels c
LEFT JOIN communications.channel_credentials cc ON cc.channel_id = c.id
ORDER BY c.created_at ASC;

-- name: GetChannelByID :one
-- GetChannelByID is GetChannel and also UpdateChannel's/VerifyChannel's
-- existence lookup (the plain channel row is all either needs before its
-- own further validation); the joined settings_json lets GetChannel build
-- its response without a second round trip.
SELECT c.id, c.type, c.address, c.display_name, c.provider, c.is_default, c.is_active, c.created_at,
       cc.settings_json AS credential_settings_json
FROM communications.channels c
LEFT JOIN communications.channel_credentials cc ON cc.channel_id = c.id
WHERE c.id = @id;

-- name: GetChannelCredentialByChannelID :one
-- GetChannelCredentialByChannelID is UpdateChannel's/VerifyChannel's read
-- of the protected credential: settings_json to rebuild the SMTP settings,
-- secret_ciphertext to internal/secrets.Open the password.
SELECT id, channel_id, settings_json, secret_ciphertext, created_at
FROM communications.channel_credentials
WHERE channel_id = @channel_id;

-- name: UpdateChannel :one
-- UpdateChannel applies PutChannelById's validated fields, already resolved
-- in Go against the tri-state displayName rule and the write-once-true
-- isDefault rule (inventory §19.2 items 10-11) — this query only ever
-- receives final values, never partial ones.
UPDATE communications.channels
SET display_name = @display_name, is_active = @is_active, is_default = @is_default
WHERE id = @id
RETURNING id, type, address, display_name, provider, is_default, is_active, created_at;

-- name: UpsertChannelCredential :exec
-- UpsertChannelCredential rewrites the credential row when PutChannelById
-- was given a new smtp block (host/port validated, password resolved —
-- reused from the existing secret when omitted). An UPDATE alone matches
-- zero rows for a channel whose credential row is absent (deleted directly,
-- or — the case fix round 1 found live — any future path that can leave a
-- channel briefly without one), silently discarding the write while the
-- handler still answers 200 with hasCredentials:true. ON CONFLICT
-- (channel_id), backed by ux_channel_credentials_channel_id, makes this
-- write unconditional: insert when absent, replace when present.
INSERT INTO communications.channel_credentials (id, channel_id, settings_json, secret_ciphertext, created_at)
VALUES (@id, @channel_id, @settings_json, @secret_ciphertext, @created_at)
ON CONFLICT (channel_id) DO UPDATE
    SET settings_json = excluded.settings_json, secret_ciphertext = excluded.secret_ciphertext;
