-- Channels: getCommunicationsChannels, postCommunicationsChannels,
-- getCommunicationsChannelsById, putCommunicationsChannelsById and
-- postCommunicationsChannelsByIdVerify (communications inventory §1.1,
-- §2's CreateChannel/UpdateChannel/VerifyChannel bullets, §15.6's
-- per-channel-credential shape), and delivery C's mailbox channels
-- (Outlook connection design M12, Google Workspace design G4): an SMTP
-- channel has exactly one channel_credentials row and settings '{}'; a
-- mailbox channel (provider a mail channels provider's Key) has no
-- credential row and settings {"mailbox": "<mailbox id>"}. The list/get
-- queries below LEFT JOIN the credential so neither kind is hidden.

-- name: AnyChannelExists :one
-- AnyChannelExists backs CreateChannel's "the first channel ever created is
-- forced default" rule (inventory §2's CreateChannel bullet:
-- `request.IsDefault == true || !await db.Channels.AnyAsync(ct)`). It counts
-- the channels of one type: the first Messenger channel is the Messenger
-- default whatever e-mail channels exist (Messenger design N1, divergence 16).
SELECT EXISTS (SELECT 1 FROM communications.channels WHERE type = @type) AS any_exists;

-- name: LockDefaultChannelSlot :exec
-- LockDefaultChannelSlot serialises every writer that can change which
-- channel of one type is the default. BOTH write paths take it as the first
-- statement of their transaction — CreateChannel and UpdateChannel — and it
-- is released automatically at commit or rollback.
--
-- It exists because ux_channels_type_is_default has THREE writer pairs and
-- statement rewriting can only fix two of them. PUT-vs-PUT is fixed by
-- ClearOtherDefaultChannels' predicate below. **PUT-vs-POST is not, and
-- cannot be: the row POST is about to insert does not exist yet, so there is
-- nothing for PUT's demote to visit, block on, or re-check.** PUT takes its
-- snapshot, blocks on whatever row POST already locked, never sees POST's new
-- row, sets its own row true, and collides. No `WHERE` clause can see a row
-- that has not been written, and no `SELECT ... FOR UPDATE` can lock one.
--
-- The two alternatives were rejected deliberately:
--   * Catching the 23505 and answering 409 would require adding 409 to
--     putCommunicationsChannelsById, which declares 200/400/401/403/404. That
--     changes the wire contract and diverges from .NET.
--   * Row locking cannot reach a row that does not exist (above).
-- Serialising preserves the contract exactly and keeps the port faithful:
-- callers still see last-writer-wins, never a conflict.
--
-- The key is the two-int32 overload, namespaced by a class constant the way
-- energy's lockSupplyPeriods namespaces its own, so it shares no key space
-- with retention's single-bigint COMMRET1 lease or internal/db's migration
-- lock.
--
-- **ONE GLOBAL SLOT, not one per channel type, and that is deliberate.**
-- The demote statements below are filtered by the channel's type since
-- Messenger (design N1, divergence 16), so a lock per type would now cover
-- each one's write set. It stays one slot for the whole installation all the
-- same: channel writes are administrative and rare, a single key costs
-- nothing, and one lock serialises every writer whichever type it writes, so
-- no writer ever has to be proven disjoint from another. (Before Messenger
-- both demotes were type-unfiltered, faithfully — .NET's own are —, so a
-- lock per type would have been under-scoped: two writers of different types
-- would not have serialised, yet each demoted the other's row.)
--
-- **THIS LOCK REQUIRES READ COMMITTED, and both handlers rely on it.** Every
-- transaction taking it uses pgx.TxOptions{} (the pool default, READ
-- COMMITTED) and nothing overrides that anywhere in this module. The whole
-- protocol is "serialise, then read fresh": under READ COMMITTED each
-- statement takes its own snapshot, so the re-read that follows this lock
-- sees whatever the previous holder committed. Under REPEATABLE READ or
-- SERIALIZABLE the transaction's snapshot is fixed by its first statement —
-- which is THIS ONE, taken before the lock is granted — so the re-read would
-- return the same stale row the caller already had, and the pairs this lock
-- closes would reopen while still looking serialised. Raising the isolation
-- level here would be a silent regression; a writer that needs a higher
-- level must re-establish its snapshot after acquiring the lock.
SELECT pg_advisory_xact_lock(@lock_class::int, 0);

-- name: ClearDefaultChannels :exec
-- ClearDefaultChannels demotes the existing default channel of one type, run
-- before inserting a new channel of that type that will itself be the default
-- (CreateChannel: "setting a default demotes every other row of its type in
-- the same call"). A channel of another type keeps its default (design N1,
-- divergence 16): the first Messenger default must not demote the e-mail
-- default Invoices and Calendar send through.
UPDATE communications.channels SET is_default = false WHERE is_default AND type = @type;

-- name: ClearOtherDefaultChannels :exec
-- ClearOtherDefaultChannels is ClearDefaultChannels for UpdateChannel,
-- excluding the row being updated (it is set to the new default
-- separately, by UpdateChannel below), and limited to the channels of its
-- type (@type is the updated channel's own).
--
-- THE MISSING `AND is_default` IS THE POINT for the PUT-vs-PUT pair, and
-- that pair ONLY. With the predicate present, two PUTs on different channels
-- of the same type are deadlock-free-but-wrong: under READ COMMITTED the
-- loser's statement takes its snapshot before the winner commits, so the
-- winner's row is still is_default = false in that snapshot, does not match
-- `is_default AND id != @id`, and is never visited — not even by
-- EvalPlanQual, which only re-checks rows the statement actually found. The
-- loser therefore demotes nothing, sets its own row to true, and collides
-- with the winner on ux_channels_type_is_default. That 23505 escapes to
-- httpx.WriteError's host-wide fallback as a bare RFC 7807 409 — the wrong
-- vocabulary for this module, AND a status putCommunicationsChannelsById's
-- contract does not declare at all (200/400/401/403/404 only), so it cannot
-- simply be caught and re-shaped the way CreateChannel's own collision is.
--
-- Without the predicate the statement visits every other row of the type,
-- blocks on the winner's lock, and EvalPlanQual re-checks the winner's NEW
-- version against `id != @id AND type = @type`, which still matches — so the loser demotes the winner and the
-- update proceeds to a correct last-writer-wins outcome.
--
-- **SCOPE, precisely: this closes PUT-vs-PUT and nothing else.** It does NOT
-- close PUT-vs-POST — a row that does not exist yet cannot be visited or
-- blocked on, whatever this predicate says — and an earlier version of this
-- comment claimed "no violation at all", which was true only of the pair it
-- had tested. LockDefaultChannelSlot above is what makes the remaining pairs
-- safe; this predicate and that lock are both required, for different pairs.
--
-- COST, stated because it is not free: dropping the term widens the write set
-- from "the current default" to "every other channel row of the type", so one
-- PUT now row-locks every other channel of its type for the rest of its
-- transaction. On this
-- table that is a handful of rows and no sequential behaviour changes, but it
-- is a lock-order surface: at scale, writers acquiring these rows in
-- different orders is how a lock-order inversion (40P01, surfacing as a 500)
-- would appear. LockDefaultChannelSlot also constrains this — every writer
-- for one type now passes through the advisory lock first, so they cannot
-- interleave their row acquisitions in conflicting orders.
-- TestUpdateChannel_ConcurrentDefaultRaceLeavesExactlyOneDefault pins the
-- PUT-vs-PUT pair; TestChannels_ConcurrentUpdateAndCreateDefaultRace pins
-- PUT-vs-POST.
UPDATE communications.channels SET is_default = false WHERE id != @id AND type = @type;

-- name: InsertChannel :one
-- InsertChannel is CreateChannel's channel-row insert: provider 'smtp' with
-- settings '{}' (its credential row follows), or a mail channels provider's
-- Key with the mailbox Check resolved and no credential row.
-- ux_channels_provider_mailbox refuses a second channel on one mailbox of
-- one provider.
INSERT INTO communications.channels (id, type, address, display_name, provider, is_default, is_active, created_at, settings)
VALUES (@id, @type, @address, @display_name, @provider, @is_default, @is_active, @created_at, @settings)
RETURNING id, type, address, display_name, provider, is_default, is_active, created_at, settings;

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
-- credential_settings_json is non-NULL for every SMTP channel this port
-- wrote and NULL for every mailbox one.
SELECT c.id, c.type, c.address, c.display_name, c.provider, c.is_default, c.is_active, c.created_at,
       c.settings, cc.settings_json AS credential_settings_json
FROM communications.channels c
LEFT JOIN communications.channel_credentials cc ON cc.channel_id = c.id
ORDER BY c.created_at ASC;

-- name: GetChannelByID :one
-- GetChannelByID is GetChannel and also UpdateChannel's/VerifyChannel's
-- existence lookup (the plain channel row is all either needs before its
-- own further validation); the joined settings_json lets GetChannel build
-- its response without a second round trip.
SELECT c.id, c.type, c.address, c.display_name, c.provider, c.is_default, c.is_active, c.created_at,
       c.settings, cc.settings_json AS credential_settings_json
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
SET display_name = @display_name, is_active = @is_active, is_default = @is_default, settings = @settings
WHERE id = @id
RETURNING id, type, address, display_name, provider, is_default, is_active, created_at, settings;

-- name: GetChannelSettingsByID :one
-- GetChannelSettingsByID reads a channel's provider and settings, for
-- conversations_patch.go's on-close mark enqueue (M17).
SELECT provider, settings
FROM communications.channels
WHERE id = @id;

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

-- name: SwitchChannelProvider :one
-- SwitchChannelProvider is the switch of an SMTP channel to a mailbox provider
-- (POST /channels/{id}/provider, M12): the provider and the mailbox, the id,
-- address, default flag and conversations untouched. DeleteChannelCredential
-- runs in the same transaction.
UPDATE communications.channels
SET provider = @provider, settings = @settings
WHERE id = @id
RETURNING id, type, address, display_name, provider, is_default, is_active, created_at, settings;

-- name: SetChannelSmtp :exec
-- SetChannelSmtp turns a mailbox channel back into an SMTP one (a PUT
-- with provider smtp and a full smtp block): provider 'smtp', the mailbox
-- cleared. The credential row is written by UpsertChannelCredential in the
-- same transaction.
UPDATE communications.channels
SET provider = 'smtp', settings = '{}'
WHERE id = @id;

-- name: DeleteChannelCredential :exec
-- DeleteChannelCredential drops an SMTP channel's credential row as it is
-- switched to a mailbox provider, in SwitchChannelProvider's transaction.
DELETE FROM communications.channel_credentials WHERE channel_id = @channel_id;

-- name: GetChannelProviderForConversation :one
-- GetChannelProviderForConversation is the provider and type of a
-- conversation's channel, for its size budget (M13): the staging handler's and
-- the conversation detail's maxMessageBytes.
SELECT ch.type, ch.provider
FROM communications.conversations c
JOIN communications.channels ch ON ch.id = c.channel_id
WHERE c.id = @id;

-- name: MailboxHasChannel :one
-- MailboxHasChannel is whether a channel on provider names the mailbox in
-- its settings: read after a channel's switch back to SMTP has committed, so
-- its Forget is skipped when another channel took the mailbox in between.
SELECT EXISTS (
    SELECT 1 FROM communications.channels
    WHERE provider = @provider::text AND settings->>'mailbox' = @mailbox::text
);
