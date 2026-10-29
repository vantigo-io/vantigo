# Outlook connection and Microsoft 365 mail, delivery D — Microsoft 365 channels that receive: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** mail arriving in a Microsoft 365 channel's shared mailbox becomes a
Communications conversation. A new worker, `communications-mailbox-sync`, reads each
channel's **Inbox** through Graph's message delta with the application's token, fetches
each new message whole as MIME (`$value`), stores the raw message, parses it, sanitises its
HTML, threads it onto the conversation it answers (or starts one), links the sender to a
customer when exactly one matches, and records a **receipt** for every message it sees so
nothing is imported twice. With inbound messages in the database, **reply, reply-all and
the AI draft come alive** — the code paths that were "permanently negative" since the port
(communications design §1.1) start answering.

**Scope — D only.** Not mail change notifications, Outlook state on close or the import
window (delivery E, `00093`: `Watch`/`Unwatch`/`Notified`, `Mark`, `EnsureFolder`,
`channel_sync.notified` set by a notice), not sending from one's own address (F). The
`ChannelMailbox` contract gains only the two methods D calls: `Delta` and `Raw`. The
`notified` column is created now (spec M14's schema) and read by the claim's ordering, but
nothing sets it until E.

**Architecture:** Entra answers two new reads on its existing guarded Graph client with
the **application** credential, for a mailbox `Check` recorded in `entra.channel_mailboxes`
only: `Delta` (one page of `/users/{oid}/mailFolders/inbox/messages/delta`, immutable ids,
`$select` of four fields) and `Raw` (`/users/{oid}/messages/{id}/$value`, capped).
`internal/mail` gains the inbound half of the platform's mail package: `ParseInbound`
(net/mail + mime/multipart + x/text charsets), `SanitizeHTML` (bluemonday) and
`TextFromHTML`. Communications owns everything else: `channel_sync` and `inbound_receipts`
(`00092`), the worker and its lease, the import transaction, threading, the linker, the
reply recipients, the widened download and the raw download. The frontend renders the
sanitised HTML whenever there is some, with **Show as text**, and inline `cid:` images
resolved to `data:` URIs so the iframe's CSP stays `img-src data:`.

**Branch:** `claude/communications-m365-inbound-d`, at `origin/main` `57b8af8e` (C #197
merged). The spec names `feat/communications-inbound`; this one is the session's. The pull
request opens against `main` (memory: *Stacked PRs per phase*).

**Migration:** `00092_communications_inbound.sql`. Free: the directory ends at
`00091_communications_channel_settings.sql`.

**Dependency:** `github.com/microcosm-cc/bluemonday` (new; pulls `github.com/aymerick/douceur`
and `github.com/gorilla/css`). `golang.org/x/text v0.43.0` and `golang.org/x/net v0.61.0`
are already direct requirements (`apps/server/go.mod:38`, `:41`).

**Spec:** [`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`](../specs/2026-10-29-outlook-connection-and-mail-design.md), M14, M15, M16, *Deliveries* D, *Testing* D, and C's *as built* note.
**Research:** [`…/research/2026-10-29-outlook-connection-and-mail.md`](../research/2026-10-29-outlook-connection-and-mail.md) §2 *Communications*, §3 *Change notifications and delta*, *Shared mailboxes and RBAC for Applications*.
**Format model:** [delivery C's plan](2026-10-29-communications-m365-channels.md).
**Inventory for the ported rules:** [`2026-09-13-communications-inventory.md`](../specs/2026-09-13-communications-inventory.md) §4.3 (reply-all CC), §4.4 (threading metadata).

## Plan readings

All paths are under `apps/server/internal/` unless they start with `apps/`, `docs/`,
`openapi/` or `deploy/`. Line numbers are `main` at `57b8af8e`.

1. **Base.** `main` holds C: `contracts.ChannelMailbox` with seven methods
   (`contracts/channel_mailbox.go:25-67`), Entra's provider (`entra/channel_mailbox.go`),
   `channels.settings` and `ux_channels_entra_mailbox` (`db/migrations/00091_communications_channel_settings.sql:16-18`),
   the outbox's mailbox branch (`communications/outbox.go:492-522`) and the accessors
   with the in-transaction guard (`communications/contractscalls.go:57-175`). The branch
   is cut from `origin/main`; `git log origin/main..` holds only this plan before task 2.

2. **The schema D writes into** (`db/migrations/00006_communications_baseline.sql`).
   - `conversation_messages.direction` already allows `inbound` (`:166`);
     `raw_payload_storage_key varchar(1000)` (`:181`), `channel_metadata_json jsonb`
     (`:173`) and `rfc_message_id varchar(998)` with a **non-unique** index (`:184`, `:190`)
     exist and are unwritten by any inbound path.
   - `message_attachments.scan_status varchar(20) NOT NULL DEFAULT 'clean'` has **no CHECK**
     (`:238`), so `'unscanned'` needs no schema change. `attachment_uploads.scan_status`
     (`:268`) is untouched by D.
   - `participants.contact_id integer` (`:76`), unique `(channel_id, address)` — **case-
     sensitive** (`:83`).
   - `conversations.customer_association_source` CHECK `manual|automatic` (`:108-109`);
     `conversation_customer_candidates (conversation_id, customer_id)` PK (`:135-141`).
   - `channels.provider` has no CHECK (`:34`); a channel is never deleted —
     `participants.channel_id` and `conversations.channel_id` are `ON DELETE RESTRICT`
     (`:70`, `:90`) — so `channel_sync`'s `ON DELETE CASCADE` only matters to tests.
   - `communications/sqlc.yaml:7-9` lists `00006` and `00091`; it gains `00092`
     (`db/schema_test.go` checks each module's list against the migrations, C's reading 3).

3. **The outbox's claim pattern, which the sync worker copies** (`communications/outbox.go`,
   `queries/outbox.sql`).
   - `claim` (`outbox.go:304-372`) computes `now` and `leaseUntil` **once**, selects a
     candidate (`SelectClaimableOutboxJob`, `outbox.sql:7-29`), then a conditional
     `UPDATE` re-asserting the same predicate (`ClaimOutboxJob`, `:44-52`, `:execrows`); 0
     rows is a lost race, try the next. "The conditional update is the lock" — no
     advisory lock (`:31-37`; reference *Background workers*, `reference/communications.md:370-374`).
   - The lease id is `hexN(uuid.New())` (`outbox.go:306`); completion re-reads and
     compares the lease (`:545-549`); `markFailed` does too (`:645`).
   - `ProcessOne` returns true for any outcome so `drain` keeps going (`:247-295`).
   - The worker is a `worker.Worker` (`:198`) with `Name`/`Interval`/`Run` (`:222-245`);
     `workers()` (`communications/module.go:119-121`) lists three, and
     `TestModule_ContributesItsWorkers` (`module_test.go:45-`) is an **exact set**.
   - sqlc 1.31.1 cannot parse `@now::timestamptz - interval '…'`; the outbox computes
     `leaseUntil` in Go and compares `lease_until < @now`. The sync worker does the same.

4. **Objects before rows: the reservation ledger** (`communications/attachments.go:352-400`,
   `objects.go:129-165`). Staging writes a `staged` cleanup record and commits it **before**
   `Put`, then marks it `owned` inside the metadata's transaction
   (`MarkCleanupRecordOwned`, `queries/attachments.sql:108-115`). A crash between leaves a
   staged reservation the cleanup worker reclaims after 10 minutes. `reserveStorageKey` is a
   method on `*server` and takes `uploadID` only for its error text; D lifts its body into
   `objects.go` as `reserveObjectKey(ctx, pool, key, now) error` and keeps
   `(*server).reserveStorageKey` as a one-line caller, so the worker reserves the same way.
   On a rolled-back import (e.g. the receipt's unique violation) the worker calls
   `queueObjectForDeletion` (`objects.go:129`) for each key it put, outside the dead
   transaction, which turns the staged reservation `pending` (`:157-163`). Keys pass
   `isSafeRelativeKey` (`:92-106`); D's keys are
   `inbound/{channel hex}/{message hex}/raw.eml` and
   `inbound/{channel hex}/{message hex}/attachments/{attachment hex}`. The object store is
   `moduleObjectStore` (`server.go:80-99`), scope `communications` (`server.go:17`).

5. **Reply today** (`communications/conversations_reply.go`, `conversations.go`).
   - `resolveReplyRecipients` (`conversations_reply.go:93-102`) reads
     `GetLatestInboundParticipantAddress` (`queries/conversations.sql:298-320`, a `JOIN
     participants`, so an inbound message without a participant is skipped); step 7 answers
     422 `recipients_missing` (`:225-232`).
   - `ccAddresses` is never filled by the handler (`:250-269`, the comment says
     "permanently empty"); `queueReply` (`:278-`) dedups and suppression-checks `primary +
     cc` (`:329-343`), inserts the message **without** `ChannelMetadataJson` (`:368-373`),
     a `to` delivery and one `cc` delivery each (`:380-398`).
   - `replyRecipientsOf()` (`conversations.go:272-332`) is a **constant**, all negative,
     used by the detail (`:398`). Its shape: `replyTo *string`, `replyAllCc []participant`
     (participant objects with `id` and `channelId`, `openapi/communications.yaml:532-573`),
     so a CC address must be a **participant row** to be answered.
   - .NET's rules to port unchanged (inventory §4.3, §4.4): the **To** is exactly one
     address, the latest inbound participant; reply-all's CC is `metadata.Cc` of **that one
     latest inbound message** only, each a valid email, normalised `Trim().ToUpperInvariant()`
     for comparison, without the channel's mailbox address and the latest sender,
     `Distinct(OrdinalIgnoreCase).Take(100)`, then filtered against the primary at send;
     `In-Reply-To` = the latest inbound `rfc_message_id`, `References` = its stored
     references plus it, `Distinct(OrdinalIgnoreCase).Take(20)`.
   - The outbox already reads `{inReplyTo, references[]}` from an outbound message's
     `channel_metadata_json` and writes the headers (`outbox.go:776-787`, `:825-838`); C
     proved them byte for byte through `RenderMIME`. D only has to write them.
   - Outbound `rfc_message_id` is stored **bracketed**: `rfcMessageIDFor` →
     `<hex@vantigo.invalid>` (`conversations_reply.go:80-82`). Inbound Message-Ids are
     therefore stored bracketed too, so `In-Reply-To` matches by plain equality.

6. **The AI context** (`queries/ai.sql:23-38`): the 20 newest **inbound** `text_body`s,
   `html_body` never read. D guarantees a text body on every imported message (M14), so the
   context fills itself with no change to `ai.go`.

7. **Attachments and download** (`queries/attachments.sql`, `attachments.go`).
   - `InsertMessageAttachment` hardcodes `'clean'` (`attachments.sql:148-157`); D adds
     `InsertInboundMessageAttachment` with `scan_status = 'unscanned'` and leaves the
     composer's insert alone.
   - Download reads `GetCleanMessageAttachmentByID` — `scan_status = 'clean'` only
     (`attachments.sql:60-71`; handler `attachments.go:634-654`), and the 200 carries only
     `Body`, `ContentType` (`sanitizedAttachmentContentType`, `:170-180`, the declared media
     type) and `ContentLength`. **No `Content-Disposition`**: a stranger's `text/html` or
     `image/svg+xml` attachment would be served inline from Vantigo's own origin. The global
     CSP (`security/headers.go:33-48`: `script-src 'self' <hash>`) blocks inline script but
     not a script loaded from another `'self'` URL. D therefore answers an **unscanned**
     attachment with `Content-Disposition: attachment; filename*=UTF-8''…` and a content type
     from a passive allow-list (images other than SVG, PDF, plain text, Office types, zip),
     `application/octet-stream` otherwise. The header must be declared on the operation's
     200 in `openapi/communications.yaml:1034-1073` so the strict server carries it.
   - The detail sets `DownloadAvailable: a.ScanStatus == "clean"` (`conversations.go:498`);
     D makes it `clean || unscanned`.

8. **Retention and erase** (`communications/retention.go:201-267`,
   `customer_personal_data.go:133-196`). `deleteMessages` already queues each
   `raw_payload_storage_key` (`ListRetentionRawPayloadKeys`, `queries/retention.sql:61-71`)
   and each attachment's key before deleting the rows; the erase runs the same function
   for a customer's conversations. Nothing new is needed for raw payloads or attachments;
   D adds the receipts' sweep (reading 17) and rewrites the stale "nothing writes this"
   comments.

9. **The linker's contract** (`contracts/directory.go:49-56`, `:199-202`;
   `customers/directory.go:143-156`). `ContactsByEmail(ctx, email) ([]ContactMatch, error)`
   normalises the address itself, takes no principal, answers `{ContactID,
   CandidateCustomerIDs}` per contact, empty when none. `Deps.Directory` is resolved for
   workers too (`module/workers.go:43-48`), and is nil when Customers is off — then the
   linker does nothing. Communications calls `Directory` inline today
   (`contractscalls.go:29-30`); the worker's call goes through a new accessor
   `directoryContactsByEmail` there, so it is refused inside a transaction.

10. **Entra's Graph client** (`entra/graph.go`).
    - Options (`:305-343`): `noRetry`, `noWords`, `eventual`, `largeAnswer`, `deltaPage`
      (sets `Prefer: outlook.timezone="UTC", odata.maxpagesize=50` and reads through 16 MiB,
      `:98-101`), `wholeEvents`, `addressInPath`. `sendAs` (`:473-552`) maps them to headers
      and a read cap; every answer is read through `readCapped` (`:623-633`) and **decoded as
      JSON** (`:511-519`).
    - The call budget is `graphCallBudget = 30 s` on the `http.Client` itself
      (`:74-75`, `:253-261`) — connection, headers and body together.
    - D adds two options: `mailDeltaPage` (Prefer `IdType="ImmutableId",
      odata.maxpagesize=50`, read through `graphMaxBody` 1 MiB — fifty messages of four
      fields are a few KiB) and a raw read, `getRaw(ctx, cred, path, limit) ([]byte, error)`,
      which sends `Prefer: IdType="ImmutableId"`, `Accept: */*`, does **not** JSON-decode,
      reads through `limit`, and runs on a second `http.Client` over the same transport with
      `graphRawBudget = 90 s` (25 MiB in 30 s would need ~7 Mbit/s). `errResponseTooLarge`
      from it becomes `contracts.ErrMailTooLarge`.
    - The 1 MiB `graphMaxBody` cap on a delta page needs no change.

11. **Entra's channel mailboxes** (`entra/channel_mailbox.go`).
    - `ready` (`:146-163`) is Status-then-credential; `Send` refuses a mailbox not in
      `entra.channel_mailboxes` before any call (`:311-320`); `channelRefusal` (`:204-219`)
      maps 403 → `ErrMailboxDenied`, 404 with `mailboxGoneCodes` (`mailbox.go:106-108`) →
      `ErrMailboxGone`, 413/size codes → `ErrMailTooLarge`.
    - `Delta` and `Raw` follow `Send`'s gate exactly: parse the id, require the row
      (`ErrMailboxUnknown` with no call), `ready`, then the call.
    - The calendar's delta (`entra/mailbox_delta.go`) gives the pieces to reuse:
      `mailboxDeltaExpiredCodes` (`:34-36`), `deltaExpired` (`:67-84`, a 410 or a 4xx with
      one of the codes → `contracts.ErrMailboxDeltaExpired`, already declared at
      `contracts/mailbox_calendar.go:474`), and the **link check** `deltaPath` (`:52-63`): a
      stored or answered link is followed only when it is https on Graph's host, no user, no
      fragment, no percent-encoded path, a query, and the exact path of *this* mailbox's
      delta — the request is rebuilt from our own path plus the link's query alone. D writes
      `messageDeltaPath(oid, link)` the same way for
      `/v1.0/users/{oid}/mailFolders/{folder}/messages/delta`, `folder` being `inbox` or one
      `validID` segment (`mailbox.go:333-`), compared case-insensitively (Graph answers
      `mailfolders` and the folder's id rather than `inbox` — unverified, so both pass).
    - `validID` also guards each message id before it goes into `Raw`'s path.

12. **The fake Graph** (`entra/graphtest`). `mail.go` (`:13-42` its routes, `mailPath`
    `:49` matches `sendMail|messages|mailFolders|mailboxSettings`), dispatched from
    `graph.go:845-846`; the calendar delta fake (`mailbox_delta.go:13-29`) answers from a
    change log with `$skiptoken`/`$deltatoken` and `ExpireDelta`. D adds an Inbox to a
    fake user (`AddInboxMessage(userID, InboxMessage{ReceivedAt, MIME, InternetMessageID,
    ConversationID}) string` returning an immutable-looking id, `RemoveInboxMessage`,
    `MarkInboxMessageRead` producing an update in the delta, `ExpireMailDelta(userID,
    status)`), and the routes `GET /users/{id}/mailFolders/inbox/messages/delta` (honouring
    `$select`, `$filter=receivedDateTime ge …`, `Prefer: odata.maxpagesize`, recording the
    `Prefer` it saw so tests assert `IdType="ImmutableId"`; `@odata.nextLink` with
    `$skiptoken`, the last page `@odata.deltaLink`; `@removed` entries; links naming
    `mailfolders/{folder id}` in lower case to exercise reading 11) and
    `GET /users/{id}/messages/{id}/$value` (`message/rfc822`, 404 `ErrorItemNotFound`).
    `MailboxDenied`/`NoMailbox` apply as on every mail route.

13. **The worker's cycle — decided.**
    - **Claim** (`SelectDueChannelSync` + `ClaimChannelSync :execrows`): a row of an
      **active `entra`** channel, `state <> 'stopped'`, `next_sync_at <= @now`, and
      `lease_until IS NULL OR lease_until < @now`, ordered `notified DESC, next_sync_at`;
      the update sets `lease_id`, `lease_until = @lease_until` (now + 2 min, computed in Go)
      re-asserting the predicate. Ten tries, as the outbox (`outboxClaimAttempts`).
    - **Every write is guarded** `WHERE channel_id = @channel_id AND lease_id = @lease_id`
      (`:execrows`); 0 rows → the lease was lost → the cycle stops, writing nothing more.
    - **Renew** (`RenewChannelSyncLease`) before each message: `lease_until = now + 2 min`.
    - **Status** of the slot first (`mailboxStatus`): not available, no slot, or another
      `Key()` → **hold**: `next_sync_at = now + 5 min`, `error = 'provider_unavailable'`,
      `attempts` unchanged, state unchanged (C's outbox hold, M13 as built).
    - **Delta**: `link = channel_sync.delta_link`; empty → `since = import_from`. One page per
      call (reading 15). `ErrMailboxDeltaExpired` → `delta_link = NULL` and restart with
      `since` = the newest receipt's `received_at` for the channel (`NewestInboundReceipt`),
      or `import_from` when none; the receipts dedupe the overlap.
    - **The page**: items `Removed` are ignored (Vantigo keeps the record, M17); items with
      `ReceivedAt < import_from` are ignored (delta answers changes outside its filter —
      Microsoft facts); the rest sorted by `ReceivedAt` ascending, ties by id; at most
      `inboundPerCycle = 50` handled (one page is at most 50). Each id with a receipt
      already (`InboundReceiptExists(channel, provider id)`) is skipped silently.
    - **After the page**, `StoreChannelSyncPage(link, done)`: with `Done`, `delta_link =
      link`, `state = 'active'`, `error = NULL`, `attempts = 0`, `last_synced_at = now`,
      `next_sync_at = now + 5 min`, lease cleared; without, `delta_link = link` (the next
      link — a page's link is only stored once every message of the page has a receipt, so a
      lost lease re-reads the page and the receipts dedupe), `next_sync_at = now`, lease
      cleared — the drain claims it again at once. "At once while more remain" (M14).
    - **Failures**: `ErrMailboxDenied`, `ErrMailboxGone`, `ErrMailboxUnknown` → `state =
      'failed'`, `error` the code (`mailbox_denied`, `mailbox_gone`, `mailbox_unknown`),
      `next_sync_at = now + 1 h` (an admin may fix RBAC), `attempts + 1`. Anything else
      (throttled, Graph not answering, a storage failure, a database error) → `attempts + 1`,
      `next_sync_at = now + min(1 min × 2^attempts, 1 h)`, `error = 'sync_failed'`, state
      unchanged. Each failure is a `Warn` log with the channel id and the code, never an
      address, a subject or Graph's words.
    - **Lease vs budgets**: the lease is 2 min and renewed before each message; `Raw` is
      bounded by `graphRawBudget` 90 s; parse, store and the transaction are local. A
      message that outlives the lease loses it, and its receipt's uniqueness makes the
      overlap harmless (M14).
    - **The drain**: `Run` ticks every `mailboxSyncPollInterval = 30 s`; each tick claims and
      runs channels until none is due or the context ends.

14. **One message's import — decided.** For an item without a receipt:
    1. `Raw(mailbox, id, inboundMaxBytes)`. `ErrMailTooLarge` → receipt `skipped`/`too_large`,
       no message, no object. `contracts.ErrMailItemGone` (deleted between delta and
       `$value`) → receipt `skipped`/`gone`. Other errors end the cycle as a failure
       (reading 13) without a receipt, so the message is tried again.
    2. `own_message`: the `Message-Id` header (read with `net/mail` before the full parse) is
       `<32 hex@vantigo.invalid>`, the hex a uuid, and `MessageExists(id)` finds it in
       `conversation_messages` → receipt `skipped`/`own_message`, nothing stored. Another
       installation's id with the same suffix is imported normally (M14).
    3. `duplicate`: the normalised Message-Id already has an `imported` receipt in the
       channel (`InboundReceiptByRfcID`) → receipt `duplicate` pointing `message_id` at the
       original's message, nothing stored.
    4. `mail.ParseInbound(raw, inboundLimits)` (reading 16). A parse error → the
       **unreadable** path: message stored with `text_body = "Vantigo could not read this
       message."`, no HTML, no attachments, the raw kept, `channel_metadata_json
       {"unreadable": true}` plus whatever headers did parse, receipt `failed` with reason
       `unparseable` and `message_id` set — a new conversation (no threading without
       headers).
    5. `messageID := uuid.New()`; reserve and `Put` the raw (`message/rfc822`) and every
       attachment **before** the transaction (reading 4).
    6. The linker's lookup (reading 19), **before** the transaction (it is a contract call).
    7. The thread (reading 18) — read before the transaction, **re-checked inside it** with
       the conversation row `FOR UPDATE` (status may have changed).
    8. **One transaction** (`db.WithTx`, a context marked by `lockedTxKey` so no slot or
       directory call can happen inside): `InsertInboundReceipt` (the unique index decides
       the race), the participants (sender, and each Cc address that is not the mailbox —
       find-or-create **case-insensitively**, reading 21), the conversation (new, reopened or
       as is), `conversation_participants`, the message (`direction='inbound'`,
       `participant_id` = sender, `subject`, `text_body`, `html_body`, `rfc_message_id`,
       `raw_payload_storage_key`, `channel_metadata_json`, `occurred_at` = Graph's
       `receivedDateTime`, `created_at` = now), the attachments (`unscanned`), every key
       `MarkCleanupRecordOwned`, the conversation's activity (`last_activity_at`,
       `preview_text`, `subject` filled once), and the linker's writes.
    9. A unique violation on `ux_inbound_receipts_provider` → rolled back, counted as already
       imported, the keys queued (`queueObjectForDeletion`); on `ux_inbound_receipts_rfc` →
       the same import becomes a `duplicate` receipt in a second, small transaction.
    - `channel_metadata_json` (M14): `{from, to[], cc[], inReplyTo, references[],
      autoSubmitted, graphConversationId}` plus `unreadable` when set. `to`/`cc` hold bare
      lower-cased addresses; `references` the bracketed ids oldest first, at most 100.
    - The outbox never reads an inbound message's metadata; `outboxChannelMetadata`'s
      comment (`outbox.go:776-783`, "nothing in this port ever writes that column") is
      rewritten.

15. **The contract's two methods — decided** (`contracts/channel_mailbox.go`):
    ```go
    // Delta reads one page of the changes to folder ("inbox") of mailbox, a mailbox
    // Check recorded: link "" starts afresh with the messages received at or after
    // since; any other link is one an earlier page answered. Items are at most
    // MailDeltaPageMax, in no promised order. ErrMailboxDeltaExpired when the provider
    // no longer takes link; the errors of Send otherwise. No message content is read.
    Delta(ctx context.Context, mailbox, folder, link string, since time.Time) (MailDelta, error)
    // Raw answers the message whole, as RFC 5322 bytes, at most max of them:
    // ErrMailTooLarge above, ErrMailItemGone when the message is no longer there.
    Raw(ctx context.Context, mailbox, messageID string, max int64) ([]byte, error)

    type MailDelta struct {
        Items []MailItem
        Link  string // with Done the delta link to keep; else the next page's
        Done  bool
    }
    type MailItem struct {
        ID             string    // the provider's immutable id; opaque
        ReceivedAt     time.Time // UTC
        MessageID      string    // the Internet Message-Id as the provider answers it, "" if none
        ConversationID string    // the provider's thread id; opaque, "" if none
        Removed        bool      // gone from the folder: ID alone is set
    }
    const MailDeltaPageMax = 50
    var ErrMailItemGone = errors.New("contracts: the mail item is gone")
    ```
    - *Spec departures:* (a) `Raw` takes `max` — the cap is Communications' setting
      (`COMMUNICATIONS_INBOUND_MAX_BYTES`), not the provider's — and answers `[]byte`, not
      an `io.ReadCloser`: the guarded client buffers through its cap anyway (reading 10), and
      the caller stores and parses the whole. (b) `ErrMailItemGone` is new. (c) `Delta`
      answers one page, not a whole delta: M14's "at most 50 per cycle" is one page.
    - `ErrMailboxDeltaExpired` is reused from the mailbox calendar.
    - The interface's comment loses "the inbound reads … arrive with the deliveries that
      call them" for the notices only (E's).

16. **The parser and the sanitiser — decided** (`internal/mail`, new files `inbound.go`,
    `sanitize.go`, `html_text.go`; corpus `internal/mail/testdata/mime/`). A platform
    package, like `RenderMIME`, documented in `contributing/platform-packages.md`.
    ```go
    type Inbound struct {
        MessageID, InReplyTo string   // bracketed, "" when absent; the first In-Reply-To id only
        References           []string // bracketed, oldest first, at most 100
        From                 *Address // nil when no From parses
        To, Cc               []Address
        Subject              string   // RFC 2047 decoded, trimmed, ≤ 998
        Date                 time.Time
        AutoSubmitted        bool
        Text, HTML           string   // HTML raw (unsanitised); either may be ""
        Attachments          []InboundAttachment
    }
    type Address struct{ Name, Address string } // Address lower-cased addr-spec
    type InboundAttachment struct {
        FileName, ContentType, ContentID string // ContentID without brackets
        Inline bool
        Content []byte
    }
    type InboundLimits struct{ MaxParts, MaxDepth, MaxAttachments int; MaxBody int64 }
    func ParseInbound(raw []byte, limits InboundLimits) (Inbound, error)
    func SanitizeHTML(html string, inlineURL func(contentID string) (string, bool)) string
    func TextFromHTML(sanitized string) string
    ```
    - **Parse**: `net/mail.ReadMessage`; headers decoded with `mime.WordDecoder` whose
      `CharsetReader` uses `golang.org/x/text/encoding/htmlindex` (so `iso-8859-1`,
      `windows-1252`, `koi8-r`… all decode); `multipart/*` walked with `mime/multipart`
      to `MaxDepth` 10 and `MaxParts` 200; `Content-Transfer-Encoding` `quoted-printable`
      (`mime/quotedprintable`), `base64` (tolerant of line breaks and missing padding),
      `7bit`/`8bit`/`binary`; text parts' `charset` through `htmlindex` (unknown → treated as
      UTF-8 with invalid bytes replaced). The first `text/plain` and the first `text/html`
      that are not attachments are the bodies (`multipart/alternative` picks both;
      `multipart/related` keeps its `cid:` parts as inline attachments). Every other leaf
      with a filename, a `Content-Disposition: attachment`, or a non-text type is an
      attachment; `message/rfc822` is kept as an attachment (`.eml`), not descended;
      `application/ms-tnef` (`winmail.dat`) is kept as an attachment, **not decoded**.
      `MaxAttachments` 100; the rest dropped with a count in the result.
    - **Filenames**: `mime.ParseMediaType` decodes RFC 2231 `filename*` only for `utf-8`
      and `us-ascii` (Go's `decode2231Enc`); an `iso-8859-1''…` value is dropped by it. The
      parser therefore reads the raw `filename*`/`name*` parameters itself as a fallback
      (charset through `htmlindex`, continuations `*0*`, `*1*` joined), then RFC 2047 in a
      quoted `filename=`, then `name=` on `Content-Type`; the result goes through
      Communications' own `safeAttachmentFileName` (`attachments.go:134`) at insert.
    - **Auto-replies** (M14): `Auto-Submitted` present and not `no`, `X-Auto-Response-Suppress`
      present, `Precedence` `bulk|auto_reply|list`, or `X-Autoreply` present →
      `AutoSubmitted`.
    - **Errors**: no header block, or no `From` **and** no body, is an error (the unreadable
      path); a damaged part is skipped, not fatal.
    - **Sanitise** (M14): parse with `golang.org/x/net/html`, then a pre-pass that rewrites
      `img[src^="cid:"]` through `inlineURL` (unknown cid → the `img` removed), removes every
      other `img` whose `src` is not a `data:image/(png|gif|jpeg|webp);base64,…` URI (remote
      images and tracking pixels stripped), and drops `background`/`srcset` attributes; then
      `bluemonday.UGCPolicy()` plus: `AllowURLSchemes("http", "https", "mailto")`,
      `RequireNoReferrerOnLinks(true)`, `AddTargetBlankToFullyQualifiedLinks(true)` (UGC
      already adds `nofollow`; `noopener` comes with `target=_blank`), `AllowDataURIImages()`,
      `img src` matching `^/api/v1/communications/attachments/[0-9a-f-]{36}/download$` or the
      data pattern, `AllowRelativeURLs(true)` for that path only, and a **style allow-list**
      — `AllowStyles("color", "background-color", "font-family", "font-size", "font-weight",
      "font-style", "text-align", "text-decoration", "line-height", "margin*", "padding*",
      "border*", "width", "height", "vertical-align", "white-space").Globally()` — so mail
      keeps its look without `url(…)`, `position` or `expression`. `<style>`, `<script>`,
      `<iframe>`, `<object>`, `<embed>`, `<form>`, `<input>`, `<meta>`, `<base>`, `<link>`,
      `<svg>`, `<math>` and every `on*` attribute go. *Plan decision:* the style allow-list
      is not in the spec (UGC alone strips every `style`, which flattens Outlook's mail).
    - **Text from HTML**: walk the sanitised tree; block elements and `<br>` are line
      breaks, `<li>` `- `, links their text, whitespace collapsed, at most two blank lines.
    - **Body caps** (*plan decision*): `text_body` and sanitised `html_body` at most 1 MiB
      each; a longer text is cut with `…`; a longer HTML is **dropped** (the text stays, and
      `channel_metadata_json.htmlDropped = true`) — the detail answers every message's
      bodies at once (`conversations.go:347-401`).
    - The receipt key normalisation, `<id>` bracketed and trimmed, lives in the parser
      (`NormalizeMessageID`) so Communications and the fake agree.

17. **The migration — decided** (`db/migrations/00092_communications_inbound.sql`):
    ```sql
    -- +goose Up
    -- Receiving (Outlook connection and Microsoft 365 mail design M14; delivery D,
    -- docs/superpowers/plans/2026-10-29-communications-m365-inbound.md): each
    -- Microsoft 365 channel's sync state, read and written by the
    -- communications-mailbox-sync worker under its lease, and a receipt for every
    -- message the worker saw, which makes an import idempotent. The inbound tables
    -- the port dropped (00006's header) come back in this shape, not .NET's.
    CREATE TABLE communications.channel_sync (
        channel_id      uuid        PRIMARY KEY REFERENCES communications.channels (id) ON DELETE CASCADE,
        folder          varchar(40) NOT NULL DEFAULT 'inbox',
        delta_link      text,
        import_from     timestamptz NOT NULL,
        state           varchar(20) NOT NULL,
        error           varchar(40),
        next_sync_at    timestamptz NOT NULL,
        last_synced_at  timestamptz,
        attempts        integer     NOT NULL DEFAULT 0,
        lease_id        varchar(32),
        lease_until     timestamptz,
        notified        boolean     NOT NULL DEFAULT false,
        updated_at      timestamptz NOT NULL,
        CONSTRAINT ck_channel_sync_state CHECK (state IN ('pending', 'active', 'failed', 'stopped'))
    );
    CREATE INDEX ix_channel_sync_due ON communications.channel_sync (next_sync_at);

    CREATE TABLE communications.inbound_receipts (
        id                  uuid          PRIMARY KEY,
        channel_id          uuid          NOT NULL REFERENCES communications.channels (id) ON DELETE CASCADE,
        provider_message_id varchar(400)  NOT NULL,
        rfc_message_id      varchar(998),
        message_id          uuid          REFERENCES communications.conversation_messages (id) ON DELETE SET NULL,
        outcome             varchar(20)   NOT NULL,
        reason              varchar(40),
        received_at         timestamptz   NOT NULL,
        created_at          timestamptz   NOT NULL,
        CONSTRAINT ck_inbound_receipts_outcome CHECK (outcome IN ('imported', 'duplicate', 'skipped', 'failed'))
    );
    CREATE UNIQUE INDEX ux_inbound_receipts_provider
        ON communications.inbound_receipts (channel_id, provider_message_id);
    -- Only imported receipts: a duplicate carries its original's Message-Id.
    CREATE UNIQUE INDEX ux_inbound_receipts_rfc
        ON communications.inbound_receipts (channel_id, rfc_message_id)
        WHERE rfc_message_id IS NOT NULL AND outcome = 'imported';
    -- The restart point of an expired delta, and the receipts' sweep.
    CREATE INDEX ix_inbound_receipts_channel_received
        ON communications.inbound_receipts (channel_id, received_at DESC);
    -- ON DELETE SET NULL from conversation_messages: retention deletes in batches.
    CREATE INDEX ix_inbound_receipts_message_id ON communications.inbound_receipts (message_id);
    -- Threading's fallback by Graph's conversationId (M15).
    CREATE INDEX ix_conversation_messages_graph_conversation
        ON communications.conversation_messages ((channel_metadata_json->>'graphConversationId'))
        WHERE direction = 'inbound';

    -- A channel made under delivery C receives from the day D ships, never its past.
    INSERT INTO communications.channel_sync (channel_id, folder, import_from, state, next_sync_at, updated_at)
    SELECT id, 'inbox', now(), 'pending', now(), now()
    FROM communications.channels WHERE provider = 'entra';

    -- +goose Down
    DROP INDEX communications.ix_conversation_messages_graph_conversation;
    DROP TABLE communications.inbound_receipts;
    DROP TABLE communications.channel_sync;
    ```
    - *Plan departures from M14's DDL*: `lease_id` is `varchar(32)` (the outbox's hex form,
      `outbox.go:306`), not `uuid`; the two CHECKs, `ix_channel_sync_due`, the two receipt
      indexes and the expression index are additions. The backfill writes `pending` for
      **inactive** `entra` channels too; the claim skips them until reactivated (reading 13).
    - `message_attachments.scan_status` gets no CHECK (none exists, reading 2).

18. **Threading — decided** (M15). Read in Go before the transaction, in this order, all
    **within the channel**:
    1. `In-Reply-To` (the first id) → `FindConversationByRfcMessageID(channel, id)`: a
       message of any direction with that `rfc_message_id` (so a reply to *our* outbound
       message threads onto its conversation — our outbound ids are bracketed, reading 5).
    2. Each `References` id **newest to oldest**, the same query.
    3. Graph's `conversationId`: `FindConversationByGraphConversation(channel, id, since =
       now − 90 days)` over inbound messages' `channel_metadata_json->>'graphConversationId'`
       (the expression index).
    4. Otherwise a new conversation: `status='open'`, subject the message's, no customer.
    - A match whose conversation is `archived` → a **new** conversation; `closed` → reopened
      to `open` unless `AutoSubmitted` (M14: an auto-reply does not reopen). `open` as is.
    - Several matches for one id (the index is not unique, `00006:187-190`) → the newest
      message's conversation.
    - If Exchange rewrites our Message-Id on send (C's unverified fact), a customer's reply
      names Exchange's id, which no row has — step 3 then threads it, since the reply sits
      in the same Outlook conversation as the Sent Items copy. Step 3 only finds an
      **inbound** message's conversation: a customer's first reply to a conversation
      Vantigo started is matched by step 3 only if Exchange put the first reply in the same
      `conversationId` as an earlier inbound — not the case for a conversation Vantigo
      began. The reference states this, and the real-tenant checklist measures it.

19. **The linker — decided** (M15). Before the transaction, for the sender's address only:
    `directoryContactsByEmail` (accessor, reading 9). Customers = the union of every
    match's `CandidateCustomerIDs`, deduplicated.
    - Exactly one customer, and the conversation is new or has `customer_id IS NULL` →
      `customer_id` set, `customer_association_source = 'automatic'`.
    - Several customers, same condition → `conversation_customer_candidates` rows (`ON
      CONFLICT DO NOTHING`), no customer set.
    - None → nothing; the AI suggestion stays available.
    - `participants.contact_id` is set when exactly **one contact** matched and the
      participant's `contact_id` is NULL (several contacts at one customer still link the
      customer, not a contact).
    - A conversation with any customer already (manual or automatic) is **never re-linked**
      (M15 says manual; *plan refinement*: automatic too, so a second sender in one thread
      does not flip it). A directory error is logged and linking skipped — the import
      proceeds unlinked.

20. **Reply comes alive — decided** (M15, reading 5).
    - `GetReplyRecipientsSource(conversation)`: the latest inbound message **with a
      participant** (as `GetLatestInboundParticipantAddress` orders), its participant's
      address, its `channel_metadata_json->'cc'` and `rfc_message_id`, `references`, and
      the channel's address.
    - `replyAllCcOf(source)`: the inventory §4.3 rule exactly (each address valid —
      `net/mail.ParseAddress`, a bare addr-spec —, normalised upper-case for comparison,
      minus the channel address and the sender, distinct case-insensitively, first 100),
      resolved to participant rows by `(channel_id, lower(address))` — the import created
      them (reading 14 step 8); one missing is left out.
    - `replyRecipientsOf(ctx, q, conversationID)` becomes query-backed: `canReply = replyTo !=
      nil`, `replyTo` = the sender, `canReplyAll = len(replyAllCc) > 0`.
    - The handler: `reply_all` → `ccAddresses` = the same list's addresses, filtered against
      the primary (`queueReply` already dedups, `:329`). `reply` → none.
    - `queueReply` writes `ChannelMetadataJson = {"inReplyTo": source.rfc, "references":
      threadReferences(source.references, source.rfc)}`. `threadReferences` is .NET's
      `Distinct(OrdinalIgnoreCase)` then — *plan decision, owner question 1* — **the first
      id and the last 19** (RFC 5322 §3.6.4's advice), not .NET's `Take(20)`, which drops the
      very message being answered once a thread has 20 references.
    - The comments that pin "permanently negative" are rewritten in the same commit:
      `conversations_reply.go:20-63`, `:84-92`, `:214-238`, `:250-256`; `conversations.go:272-297`,
      `:334-345`; `queries/conversations.sql:298-314`; `stats.sql:51-56`; `ai.sql` keeps its.

21. **Participants, case — decided.** The composer stores what the user typed; the unique
    index is case-sensitive (reading 2). The import looks up with
    `FindParticipantByChannelAndAddressFold` (`lower(address) = lower(@address)`, newest
    first) and inserts the lower-cased address only when none is found, through the
    existing `InsertParticipant`'s `ON CONFLICT` (`queries/conversations.sql:240-`).

22. **The detail and the downloads — decided** (`openapi/communications.yaml`).
    - `ConversationDetailResponse.messages[]` gains `autoSubmitted: boolean`, `unreadable:
      boolean` and `rawDownloadPath: string | null` (required, `x-omitempty: false`, as C's
      `maxMessageBytes`), read from the message's metadata and `raw_payload_storage_key`.
    - New `GET /api/v1/communications/messages/{id}/raw` (`permission:communications:conversations-view`,
      as the attachment download): 200 `message/rfc822` with `Content-Disposition:
      attachment; filename="message-{hex}.eml"`, 404 when the message has no raw or the
      object is gone, 503 `attachment_storage_unavailable` on a storage failure. The raw
      holds the unsanitised HTML; as a download it is never rendered by Vantigo.
    - The attachment download gains the response header `Content-Disposition` and the
      unscanned branch (reading 7).
    - `ChannelResponse` gains `sync: {state, error, lastSyncedAt} | null` (null for SMTP), so
      the channels page shows whether a Microsoft 365 channel receives. *Plan addition.*
    - Create (`PostCommunicationsChannels`, `channels.go:328-`, the insert at `:385`) and
      the switch (`PostCommunicationsChannelsByIdProvider`, `:983-`, the update at `:1034`)
      insert a `channel_sync` row (`pending`, `import_from = now`, `next_sync_at = now`) in
      their transaction; the way back to SMTP (`PutCommunicationsChannelsById`,
      `SetChannelSmtp` at `:687`) deletes it, so a later switch starts afresh from its own
      day. Deactivation keeps it (the claim skips an inactive channel).

23. **Retention of receipts — decided** (M16). `SweepInboundReceipts`: delete receipts whose
    `message_id IS NULL` and `created_at < @cutoff` (the retention window), **except the
    newest receipt of each channel** (the delta's restart point, reading 13). It runs in
    `CleanupBatch` beside the orphan sweeps (`retention.go:276-`). A receipt whose message
    still exists is kept with it. Raw payloads and attachments already leave through
    `deleteMessages` (reading 8). Export stays keyed by customer and still leaves out raw
    payloads (M16); the reference's *what the erase does not cover* list keeps "a
    conversation not linked to the customer keeps the person's participant address" and
    adds the receipts' Message-Ids.

24. **Configuration** (`config/config.go:1159-1163`). `COMMUNICATIONS_INBOUND_MAX_BYTES`,
    `Config.CommunicationsInboundMaxBytes int64`, default 25 MiB, bounds `[1 MiB, 150 MiB]`
    (Graph's attachment ceiling), loaded in `communicationsAttachments`. Documented in the
    reference's variables table (`reference/communications.md:405-411`) and the env example
    (`deploy/compose/vantigo.env.example:450-460`). Because `admin/authentication.md` lists
    `apps/server/internal/config` among its sources and defers `COMMUNICATIONS_*` to the
    reference (`:1048` en, `:1081` nb), that sentence names the new variable in both
    languages, which also satisfies the coverage check.

25. **The frontend today** (`apps/communications/frontend/src`).
    - `ConversationThread.tsx:159-171`: text when there is any, HTML **only** when there is
      no text, in `<iframe sandbox="" srcDoc={safeHtmlSrcDoc(…)}>`; `safeHtmlSrcDoc`
      (`api/conversations.ts:244-245`) puts a CSP `default-src 'none'; img-src data:` in the
      document. A `sandbox=""` iframe has an opaque origin: its image requests to
      `/api/...` would go without the `SameSite=Strict` session cookie, and its CSP refuses
      them anyway. So inline images are fetched by the **parent** (same origin, the
      session's cookie) for that message's inline attachments only, turned into `data:`
      URIs (images ≤ 2 MiB each of `image/png|gif|jpeg|webp`), and substituted into the
      HTML before `srcDoc`; the CSP stays.
    - Attachments (`:172-195`): a download button only for `downloadAvailable && scanStatus
      === "clean"`, else a badge *Scanning*/*Unavailable*. D adds `unscanned`: the button,
      with a warning icon and the line *"Not scanned. Open it only if you trust the
      sender."*
    - The author line (`:146-154`) shows the participant; D adds an **Auto-reply** badge and,
      for `unreadable`, the text plus **Download original** (`rawDownloadPath`); every
      inbound message gets **Download original** in a small menu.
    - The composer (`components/ConversationComposer.tsx:126-157`) already shows **Reply
      all · N** when `canReplyAll` and *Replying to …* when `replyTo`; D disables **Send
      reply** when `!canReply` with *"There is no message from the customer to reply to
      yet."*
    - Entra's switch label (`apps/entra/frontend/src/i18n.ts:244` en, `:702` nb) still says
      *send through*; D restores M10's *send and receive through* (C's as-built note).

26. **Docs and the map.** `bun run tools/docs/check-coverage.ts --list` (C's reading 19)
    puts `internal/communications`, `apps/communications/frontend`,
    `openapi/communications.yaml` and `00091` under `reference/communications.md`;
    `apps/communications/frontend` under `user/communications.md` (en, nb);
    `internal/entra` and `apps/entra/frontend` under `reference/entra.md`;
    `entra/graph.go` and `entra/channel_mailbox.go` under `admin/entra.md` (en, nb);
    `apps/entra/frontend` under `user/entra.md` (en, nb); `internal/contracts`,
    `internal/modtest`, `internal/module` under `contributing/module-boundaries.md`;
    `internal/mail` under `contributing/platform-packages.md`; `internal/config` under
    `admin/authentication.md` (en, nb); `deploy` under `admin/installation.md` (en, nb).
    The migration joins `reference/communications.md`'s `sources`.

### Microsoft facts, as checked for D

Read on learn.microsoft.com on 2026-10-29.

- **`message: delta` — verified.** `GET /users/{id}/mailFolders/{id}/messages/delta`;
  application permission **`Mail.ReadBasic.All`** least, `Mail.Read`/`Mail.ReadWrite`
  higher — `Application Mail Full Access` (RBAC) carries `Mail.ReadWrite`, so C's commands
  cover it. Query parameters given on the **first** request only, then encoded in the
  links. `$select`, `$top`, `$expand` supported; **`$filter` only `receivedDateTime ge|gt
  {value}`**; **`$orderby` only `receivedDateTime desc`**, "the return order is not
  guaranteed" without it; no `$search`; custom `changeType=created|updated|deleted`.
  "Delta queries for messages can return change events that don't match the filter
  conditions": `@removed` (deleted or moved out) and read/unread changes. Header `Prefer:
  odata.maxpagesize={x}`.
  ([message: delta](https://learn.microsoft.com/en-us/graph/api/message-delta?view=graph-rest-1.0))
  D asks no `$orderby` (desc would read newest first) and sorts each page itself; asks no
  `changeType` (a message moved *into* the Inbox may arrive as an update — unverified), and
  dedupes by receipt.
- **Immutable ids — verified.** `Prefer: IdType="ImmutableId"`, which "only applies to the
  request it is included with" — so every delta page and every `$value` sends it. The id
  does not change on a move between folders of the same mailbox; it changes when the item
  goes to an archive mailbox or is exported and re-imported. Supported in delta responses;
  "the `@odata.nextLink` and `@odata.deltaLink` values … are compatible with both ID
  formats". Ids are **case-sensitive** (the receipt's varchar compares exactly).
  ([immutable ids](https://learn.microsoft.com/en-us/graph/outlook-immutable-id))
- **`$value` — verified (path), partly (rest).** `GET /users/{id}/messages/{id}/$value`
  answers the MIME, Exchange's own `Received:` and `X-MS-Exchange-*` headers included; the
  app needs the permission to get the message (`Mail.Read`, which `Mail.ReadWrite`
  covers). ([MIME of a message](https://learn.microsoft.com/en-us/graph/outlook-get-mime-message))
  **Unverified:** a size ceiling on `$value`, whether it ever redirects (the client follows
  none, `graph.go:253-261`), and how fast 25 MiB arrives inside 90 s.
- **Not verified:** the exact form of the next and delta links for an Inbox delta
  (`mailFolders('inbox')`, `mailfolders/{id}`, case), hence reading 11's tolerant check;
  the code of an expired message delta token (the calendar's list — 410,
  `syncStateNotFound`, `syncStateInvalid`, `resyncRequired`, `ErrorInvalidSyncStateData`
  — is reused); the largest `odata.maxpagesize` honoured for messages (50 is asked; a
  smaller page is fine); whether a message moved into the Inbox arrives as created or
  updated; and, from C, whether Exchange keeps our `Message-ID` (reading 18 decides what
  each answer means). The real-tenant checklist (task 9) settles them.
- **Not re-checked for D:** throttling (research §3: 10 000 requests per 10 minutes per app
  and mailbox, 4 concurrent) — one cycle is one delta page and at most 50 `$value`s.

## Tasks

Parallel lanes after task 2: **Entra** (task 3), **mail** (task 4), then **Communications
server** (task 5, after 4), **frontends** (task 6, after 5's contract commit), **docs**
(task 7, after 3–6). Each executor `git add`s **only its own paths** and regenerates only
its own module's output (`sqlc generate -f internal/<module>/sqlc.yaml`, `go generate
./internal/<module>/...` or the repo's `go generate ./...` only when it touches no other
module's gen, `bun run gen:client` in its own app).

- [ ] **1. This plan.** Committed alone, no `Docs-Impact` trailer:
  `docs(communications): delivery D's plan — Microsoft 365 channels that receive: channel_sync and inbound_receipts (00092) with the backfill, the communications-mailbox-sync worker under a lease, ChannelMailbox.Delta and Raw with immutable ids, mail.ParseInbound and SanitizeHTML on bluemonday, threading, the linker, reply and reply-all live, unscanned attachments, and the pages in both languages`.

- [ ] **2. The foundation (one worker, alone).** Readings 2, 15, 17, 24.
  - [ ] `00092_communications_inbound.sql` (reading 17) with the house-style header;
    `communications/sqlc.yaml` lists it.
  - [ ] Failing tests first: `db/schema_communications_inbound_test.go` (in the pattern of
    `schema_communications_channel_settings_test.go`): both tables, both CHECKs, the provider
    unique refusing a second receipt, the rfc partial unique refusing two `imported` with one
    Message-Id and **allowing** a `duplicate` beside its original, `ON DELETE SET NULL` on
    `message_id`, the **backfill** (an `entra` channel inserted before `goose up 00092` gets
    one `pending` row with `import_from` ≈ now; an SMTP channel none; Down then Up again).
    `modtest/channel_mailbox_test.go`: the fake's new methods.
  - [ ] `contracts/channel_mailbox.go`: `Delta`, `Raw`, `MailDelta`, `MailItem`,
    `MailDeltaPageMax`, `ErrMailItemGone` (reading 15), the interface comment updated.
  - [ ] `modtest.FakeChannelMailbox`: `AddInbox(mailbox string, item MailItem, raw []byte)`,
    `RemoveInbox`, `ExpireDelta(mailbox)`, `SetDeltaPage(n)`, `Raw` honouring `max`, `FailWith`
    for `Delta`/`Raw`, `Calls()` recording them; its links are opaque strings it issues.
  - [ ] Entra compiles against the wider interface: `entra/channel_mailbox.go` gains `Delta`
    and `Raw` answering `errors.New("entra: not built yet")` — **replaced in task 3 of this
    same pull request**, never on `main`.
  - [ ] `config.go`: `COMMUNICATIONS_INBOUND_MAX_BYTES` (reading 24) with `config_test.go`
    cases (default, bounds, a typo refused). `deploy/compose/vantigo.env.example` gains the
    commented line under *Communications*.
  - [ ] `go get github.com/microcosm-cc/bluemonday@latest` (`go.mod`, `go.sum`); `go mod tidy`
    leaves it required by nothing until task 4 — add it in task 4 instead if tidy drops it.
  - [ ] `sqlc generate -f internal/communications/sqlc.yaml`; `go build ./... && go vet ./...`;
    `mise run server:test` for `db`, `modtest`, `config`, `module`.
  - [ ] Commit (`Docs-Impact` trailer naming task 7):
    `feat(communications, entra): receiving's schema and contract — channel_sync and inbound_receipts with the backfill of Microsoft 365 channels (00092); ChannelMailbox.Delta and Raw, MailDelta and ErrMailItemGone; modtest's inbox; COMMUNICATIONS_INBOUND_MAX_BYTES`.

- [ ] **3. Entra: `Delta` and `Raw`, and the fake's Inbox (after 2; beside 4 and 5).**
  Readings 10, 11, 12. Paths: `internal/entra/**` only.
  - [ ] `graphtest` first, with tests (`graphtest/mail_inbox.go`, `mail_inbox_test.go`):
    reading 12's helpers and routes; the delta's `$filter` applied on a fresh query only;
    pages by `odata.maxpagesize`; `@removed` for a removed message; a read change as an
    update; `ExpireMailDelta` → 410 `syncStateNotFound`; `$value` byte for byte; the
    `Prefer` seen recorded; the file header lists the routes.
  - [ ] Failing tests in `channel_mailbox_inbound_test.go`: `Delta` — a fresh read sends
    `$filter=receivedDateTime ge {since, RFC 3339 UTC}`, `$select=id,receivedDateTime,internetMessageId,conversationId`,
    `Prefer: IdType="ImmutableId", odata.maxpagesize=50`; a page cut with its next link and
    `Done` false, the last with its delta link and `Done` true; a stored link followed
    (query only, our path); a link for another mailbox, another host, a fragment or a
    `..` refused unsent; a lower-case `mailfolders/{id}` link accepted; `@removed` →
    `Removed`; an id that is no `validID` left out; expired → `ErrMailboxDeltaExpired`; a
    mailbox `Check` never recorded → `ErrMailboxUnknown`, no call; the switch off →
    `ErrMailboxUnavailable`, no call; 403 → `ErrMailboxDenied`; gone codes →
    `ErrMailboxGone`; throttling passes. `Raw` — the bytes; `Prefer` sent; over `max` →
    `ErrMailTooLarge`; 404 `ErrorItemNotFound` → `ErrMailItemGone`; a bad id refused
    unsent; the same gates. Nothing of a message (subject, address) in any log line
    (a `slog` capture).
  - [ ] `graph.go`: `mailDeltaPage`, `getRaw`, `graphRawBudget` (reading 10);
    `channel_mailbox_inbound.go` (new) with `Delta`, `Raw`, `messageDeltaPath`; the stubs
    of task 2 gone.
  - [ ] `go vet`, `mise run server:test` (entra).
  - [ ] Commit:
    `feat(entra): the channel mailboxes' inbound reads — Delta reads a page of the Inbox's message delta with immutable ids, from receivedDateTime ge on a fresh read, its links checked to be this mailbox's; Raw reads a message's MIME through $value under the caller's cap; refusals mapped as Send's, an expired delta and a message gone; graphtest's Inbox`.

- [ ] **4. `internal/mail`: parse, sanitise, text (after 2; beside 3).** Reading 16. Paths:
  `internal/mail/**`, `go.mod`, `go.sum`.
  - [ ] The corpus first, `testdata/mime/`, each with an expected-JSON twin
    (`*.eml` + `*.golden.json`, updated with `-update`): `plain.eml`, `alternative.eml`,
    `related-cid.eml` (an inline PNG by `cid:`), `rfc2047-subject.eml` (`=?UTF-8?B?…?=` and
    `=?ISO-8859-1?Q?…?=`), `rfc2231-filename.eml` (`filename*=UTF-8''…` and
    `filename*=iso-8859-1''…`, continuations), `latin1.eml`, `windows-1252.eml` (curly quotes,
    `€`), `winmail.eml` (an `application/ms-tnef` part kept as `winmail.dat`),
    `forwarded-rfc822.eml`, `auto-reply.eml` (each of the four headers in a variant),
    `references.eml` (folded `References` of 25 ids), `malformed.eml` (no blank line, broken
    boundary — an error), `no-from.eml`, `deep.eml` (12 levels — the depth limit),
    `html-only.eml`, `outlook.eml` (Exchange's `Received`/`X-MS-*` headers, `$value`'s shape),
    `own-message.eml` (our `@vantigo.invalid` id). Built by hand; no customer's real mail.
  - [ ] Failing tests: `inbound_test.go` over the corpus; `sanitize_test.go` against an
    XSS list — `<script>`, `<img onerror>`, `<a href="javascript:">`, `jAvAsCrIpT:` with
    tabs and entities, `data:text/html` links, `<svg><script>`, `<math>`, `<iframe>`,
    `<object>`, `<embed>`, `<form>`/`<input>`, `<meta http-equiv=refresh>`, `<base href>`,
    `<link rel=stylesheet>`, `style="background:url(…)"`, `style="position:fixed"`,
    `expression(…)`, `srcset`, a remote `<img>` and a 1×1 pixel (both gone), a `cid:` image
    rewritten and an unknown `cid:` gone, a `data:image/png` kept and `data:image/svg+xml`
    gone, conditional comments, unclosed tags, mixed-case tags; links get `rel` with
    `noopener noreferrer nofollow` and `target="_blank"`; allowed styles kept.
    `html_text_test.go`: blocks, `<br>`, lists, entities, whitespace.
  - [ ] `inbound.go`, `sanitize.go`, `html_text.go`; `NormalizeMessageID`.
  - [ ] `go vet`, `mise run server:test` (mail).
  - [ ] Commit:
    `feat(mail): reading inbound mail — ParseInbound over net/mail and mime/multipart with quoted-printable, base64, RFC 2047, RFC 2231 names in any charset and x/text's charsets, attachments, cid parts and auto-reply headers, under limits; SanitizeHTML on bluemonday's UGC policy with cid images rewritten, remote images stripped and safe styles kept; TextFromHTML; a MIME corpus`.

- [ ] **5. Communications: the worker, the import, threading, the linker and reply (after
  2 and 4; beside 3 against the fake).** Readings 3–9, 13, 14, 18–23. Paths:
  `internal/communications/**`, `openapi/communications.yaml`,
  `internal/openapi/specs/communications.yaml` (if generated there).
  - [ ] `openapi/communications.yaml` first (reading 22): the message's three fields;
    `GET /messages/{id}/raw`; the download's `Content-Disposition` header; `ChannelResponse.sync`;
    descriptions of `replyRecipients` no longer "always negative". `go generate`, own module.
    Commit this first (`feat(communications): …the contract…`, `Docs-Impact` trailer) so
    task 6 can start.
  - [ ] Failing tests first (`mailbox_sync_test.go`, `mailbox_sync_concurrency_test.go`,
    `inbound_import_test.go`, `inbound_threading_test.go`, `inbound_linker_test.go`, all
    against `modtest.WithChannelMailbox` and the real parser):
    - the lease: a lost lease writes nothing (the row taken by another lease id mid-cycle →
      no link stored, no state written); two workers overlapping on one channel import each
      message **once** (receipts), the loser's objects queued on the ledger; a held provider
      (status off, no slot, another key) → `next_sync_at + 5 min`, attempts unchanged;
      denied/gone/unknown → `failed` with the code, `+1 h`; another error → backoff; an
      inactive channel never claimed; a `stopped` row never claimed;
    - the **backfill**'s row is claimed and its first `Delta` asks `since = import_from`;
    - a **51-message** inbox: the first cycle imports 50 from a page cut at 50 and stores the
      next link with `next_sync_at = now`; the drain's second claim imports the 51st and
      stores the delta link, `Done`, `active`; oldest first within a page;
    - an **expired delta** restarts from the newest receipt's `received_at`, re-reads the
      overlap and imports nothing twice;
    - receipts: `imported`; `duplicate` beside its original (same Message-Id, another
      provider id); `skipped`/`too_large` (no message, no object); `skipped`/`gone`;
      `skipped`/`own_message` **only for this database's ids** (another installation's
      `@vantigo.invalid` imported); `failed`/`unparseable` with the message *Vantigo could
      not read this message.* and its raw;
    - dedupe by provider id and by Message-Id; a removed item ignored; an item received
      before `import_from` ignored;
    - the stored message: direction, participant, bodies (HTML sanitised, text derived when
      only HTML), `rfc_message_id` bracketed, metadata's keys, `occurred_at` =
      `receivedDateTime`, raw object present; attachments `unscanned`, inline `cid` rewritten
      to their download path; every key owned on the ledger;
    - threading's order: `In-Reply-To` over `References`, references newest first, then
      `graphConversationId` within 90 days and not older, never across channels; a reply to
      our outbound message (its bracketed id); `closed` reopened, `archived` → new, an
      auto-reply **not** reopening and marked;
    - the linker's three outcomes, the contact set for one contact only, no re-link of a
      conversation with a customer, a nil directory, a failing directory;
    - reply, reply-all and the AI context **from a real import**: `canReply`, `replyTo`,
      `replyAllCc` (the mailbox and the sender excluded, 100 cap, case-insensitive
      distinct), a reply's deliveries (one `to`, the `cc`s), its metadata's `inReplyTo`
      and `references` (first + last 19 with 25), the outbox's MIME carrying them (one
      end-to-end through `outbox_mailbox_test.go`'s helpers), the AI context holding the
      inbound text;
    - the download of an `unscanned` attachment (200, `Content-Disposition: attachment`,
      `text/html` served as `application/octet-stream`); a `clean` one unchanged; the raw
      download (200, 404 without raw, 404 object gone);
    - retention and erase reaching raw payloads and inbound attachments (keys on the
      ledger); the receipts' sweep keeping each channel's newest;
    - channel create and switch writing a `pending` `channel_sync`; the way back to SMTP
      deleting it; `ChannelResponse.sync`;
    - no `ChannelMailbox` or `Directory` call inside a transaction (the contract-call hook);
    - `TestModule_ContributesItsWorkers` with `communications-mailbox-sync`.
  - [ ] `queries/mailbox_sync.sql` (new: `SelectDueChannelSync`, `ClaimChannelSync`,
    `RenewChannelSyncLease`, `StoreChannelSyncPage`, `HoldChannelSync`, `FailChannelSync`,
    `ResetChannelSyncDelta`, `InsertChannelSync`, `DeleteChannelSync`, `GetChannelSyncByChannelIDs`),
    `queries/inbound.sql` (new: receipts, `MessageExists`, the threading finds, the fold
    participant lookup, `InsertInboundMessageAttachment`, `ReopenConversation`,
    `SetConversationAutomaticCustomer`, `InsertConversationCandidate`,
    `SetParticipantContact`, `NewestInboundReceipt`, `SweepInboundReceipts`),
    `queries/conversations.sql` (`GetReplyRecipientsSource`, participants by fold),
    `queries/attachments.sql` (`GetDownloadableMessageAttachmentByID`); `sqlc generate -f
    internal/communications/sqlc.yaml`.
  - [ ] Code: `mailbox_sync.go` (the worker, reading 13), `inbound_import.go` (reading 14),
    `inbound_threading.go`, `inbound_linker.go`, `objects.go` (`reserveObjectKey`),
    `contractscalls.go` (`mailboxDelta`, `mailboxRaw`, `directoryContactsByEmail`),
    `conversations.go` (`replyRecipientsOf`, the message fields, `DownloadAvailable`),
    `conversations_reply.go` (cc, metadata), `attachments.go` (download), `messages_raw.go`
    (the raw download), `channels.go` (the sync row; `sync` in the response), `retention.go`
    (the sweep), `module.go` (`workers()`), `metrics.go` (one counter,
    `communications.inbound.cycles_failed`, *plan addition*; `metrics_test.go`), the stale
    comments of reading 20.
  - [ ] `go vet`, `mise run server:test` (communications), `mise run server:check`.
  - [ ] Commit (after the contract commit):
    `feat(communications): Microsoft 365 channels receive — the communications-mailbox-sync worker claims a channel under a renewed lease and reads its Inbox a page at a time; each message stored raw, parsed, sanitised and imported once by its receipt, duplicates, our own sends, the too large and the gone recorded; threading by In-Reply-To, References and Graph's conversation; the sender linked to one customer or to candidates; reply and reply-all from the latest inbound message with its threading headers; unscanned attachments downloadable as attachments; the original message downloadable`.

- [ ] **6. The frontends (after 5's contract commit).** Reading 25. Paths:
  `apps/communications/frontend/**`, `apps/entra/frontend/src/i18n.ts` and its test.
  - [ ] Failing tests first (`pages/inbox.test.tsx`, both languages): an inbound HTML message
    rendered in the iframe **even with a text body**; **Show as text** toggling; an inline
    image fetched and substituted as `data:` (and no request for a remote one — none is in
    the HTML); the **Auto-reply** badge; an unreadable message's sentence and **Download
    original**; an `unscanned` attachment's button and warning; **Send reply** disabled
    without `canReply`; reply-all's selector from a real-shaped `replyRecipients`.
    `pages/admin.channels.test.tsx`: the receive status column (*Receiving*, *Waiting for
    first sync*, *Failed: …* with the code's sentence, last synced). `apps/entra`'s page
    test: the switch's new label.
  - [ ] `ConversationThread.tsx`, `ConversationComposer.tsx`, `api/conversations.ts`
    (`inlineImages`, the raw path), `pages/admin.channels.tsx`, `catalog.ts` en/nb;
    Entra's `i18n.ts:244`, `:702` (*"Mail channels: let Communications send and receive
    through Exchange Online shared mailboxes"*, *"E-postkanaler: la Kommunikasjon sende og
    motta gjennom delte postbokser i Exchange Online"*); `bun run gen:client` in
    communications; `bun run translations:check`; `mise run frontend:check`.
  - [ ] Commit:
    `feat(communications-ui, entra-ui): receiving in the inbox — inbound HTML shown with Show as text, inline images from the message itself, auto-replies marked, an unreadable message's original to download, unscanned attachments with a warning, reply only when there is a message to answer; each channel's receive status; the Mail channels switch sends and receives; both languages`.

- [ ] **7. Documentation (after 3–6).** English first, then Norwegian bokmål page for page.
  - [ ] `reference/communications.md` (en): `sources` gains `00092_communications_inbound.sql`;
    the description; *Providers › Microsoft 365* — receives from the day it is added or
    switched (`channel_sync`, `import_from`), the sync row deleted on the way back to SMTP;
    **Receiving** replacing *The outbound-only consequence* (`:473-499`): the worker, its
    lease and the rules of reading 13, receipts and their outcomes, the cap
    `COMMUNICATIONS_INBOUND_MAX_BYTES`, parsing (what is an attachment, `winmail.dat` and
    forwarded messages kept whole), sanitising (the policy, remote images stripped, the
    styles kept, the iframe's sandbox and CSP as the second wall), unreadable messages,
    auto-replies, threading (the order, the 90 days, what happens if Exchange rewrites our
    Message-Id — the real-tenant finding), the linker, and the `ChannelResponse.sync`;
    *Reply and reply-all* (the ported rules, first + last 19 references); *Attachments*
    (`:418-471`) — `unscanned`, downloaded as attachments, `clean` still never means
    inspected; the raw download; *Background workers* (`:377-384`) four workers;
    *Metrics* (`:386-393`) the new counter; *Retention* (`:395-416`) the variable, the
    receipts' sweep; *personal data* (`:77-106`) the receipts, the linker closing the gap
    for a matched sender, an unmatched sender outside export and erase (M16).
  - [ ] `reference/entra.md` (en): *Mail channels* (`:2046-2200`) — `Delta` and `Raw`
    after *Send*: the paths, immutable ids, `$select`, the `$filter`, the link check, the
    caps and `graphRawBudget`, the refusals, `ErrMailItemGone`; the switch's label (`:2066`)
    and "In this release a channel only sends" (`:2069`) rewritten; *Testing* (`:2288-`)
    the fake's Inbox.
  - [ ] `contributing/module-boundaries.md` (en): rule 15 (`:665-697`) — `Delta` and `Raw`
    are reads under the same gate (a recorded mailbox only, no transaction), and the
    interface "grows with" the notices (E) only; the linker's `Directory` call through
    an accessor.
  - [ ] `contributing/platform-packages.md` (en): `mail.ParseInbound`, `SanitizeHTML`,
    `TextFromHTML`, the limits, bluemonday.
  - [ ] `admin/entra.md` (en, nb): *Mail channels* (`:549`, `:569`, `:620`) — receives now;
    the role already covers reading; the switch's label; what can go wrong (mail not
    appearing: the RBAC cache, the channel's receive status on the Channels page,
    `mailbox_denied`).
  - [ ] `user/entra.md` (en, nb): the switch's label (`:172`, `:276`) and "a channel sends
    only" (`:272`).
  - [ ] `user/communications.md` (en, nb): the intro; *Read a conversation* (`:45-`) —
    mail arriving in a Microsoft 365 channel's shared mailbox appears in the Inbox within
    about five minutes, HTML shown with **Show as text**, auto-replies marked, *Vantigo
    could not read this message* and **Download original**, unscanned attachments and the
    warning; *Link a conversation to a customer* (`:68-`) — linked automatically when the
    sender's address belongs to one customer, candidates when several; *Reply to a
    customer* (`:85-117`) — the "Vantigo does not receive mail yet" refusal replaced, reply
    all; *Set up a channel* — a Microsoft 365 channel receives from the day it is added,
    not its past, and its receive status.
  - [ ] `admin/authentication.md` (en, nb): the `COMMUNICATIONS_*` sentence (`:1048` en,
    `:1081` nb) names `COMMUNICATIONS_INBOUND_MAX_BYTES`.
  - [ ] `admin/installation.md` (en, nb): *Background workers and scaling* (`:272-`) gains
    `communications-mailbox-sync`.
  - [ ] The communications design's §1.1 (`docs/superpowers/specs/2026-09-13-communications-design.md:24-61`):
    a *Superseded (2026-10-29)* paragraph pointing to the Outlook design's M14–M15 and this plan.
  - [ ] The spec's *as built* note for D under *Deliveries* (after C's): reading 15's
    departures; `lease_id varchar(32)`; the added indexes and CHECKs; `ChannelResponse.sync`;
    the raw download; the style allow-list; the body caps; first + last 19 references;
    no re-link of an automatic customer; `changeType` not asked; the real-tenant findings.
  - [ ] `ROADMAP.md` (Phase 3, `:2425-`): **D — Microsoft 365 channels that receive, built**,
    its links; *Next:* E.
  - [ ] `bun install --frozen-lockfile` (a fresh worktree), `mise run docs:check`.
  - [ ] Commit:
    `docs(communications, entra): receiving — the worker, receipts, parsing, sanitising, threading, the linker, reply and reply-all, unscanned attachments and the original's download in the communications reference; Delta and Raw in Entra's; rule 15's reads; mail.ParseInbound; the inbound cap; the communications guide, the Entra pages, installation and authentication in both languages; §1.1 superseded; the spec's as-built note; the roadmap`.

- [ ] **8. Review, then verification.** A review (read-only) of: no slot or directory call
  inside a transaction (`grep -n 'ChannelMailbox\.\|Directory\.' internal/communications`
  only in `contractscalls.go`); every `channel_sync` write lease-guarded; a link stored
  only after its page's receipts; objects before rows and the ledger on every rollback;
  `own_message` only for our ids; the receipts' unique indexes deciding every race. A
  **security review**: the sanitiser (bypasses of the XSS list, CSS, `data:` URIs), the
  iframe and the parent's inline-image fetch (only the message's own inline attachments,
  only image types), the unscanned download (`Content-Disposition`, the type allow-list),
  the raw download, the delta link check (no request off this mailbox's delta), the
  parser's limits (zip-bomb-like nesting, huge headers, 25 MiB of base64), nothing of a
  message in logs or errors. A docs pass against the code. One fix batch
  (`fix(communications, entra, mail): the review's findings — …`,
  `docs(communications, entra): the review's fixes — …`). Then `mise run server:check`,
  `mise run server:test`, `mise run frontend:check`, `mise run docs:check`; strip the
  `Docs-Impact` trailers of commits whose docs landed in task 7 (memory: *Entra phase 2
  state*; signed rebase with `--exec 'git commit --amend --no-edit -S'`).

- [ ] **9. The pull request.** Rebase onto `main`; the gates again; push (signed; a
  force-push is the owner's); the pull request against `main` with the **real-tenant
  checklist**: a C channel's backfilled row syncing from the day of the upgrade and not
  before; a mail to `post@` in the Inbox within five minutes, with an inline image, an
  attachment (`unscanned`), Norwegian characters in subject and filename; a reply from
  Vantigo and the customer's answer threading back — and on the recipient's copy and the
  Sent Items item, **our Message-Id kept or not** (C's open fact; reading 18 decides what
  that means); reply-all with two Cc; an out-of-office answering without reopening a
  closed conversation; a 26 MB message `skipped`/`too_large`; a message moved into the
  Inbox from another folder (created or updated?); a delta link's exact form in the
  stored row; an expired delta (wait or force) restarting cleanly; RBAC removed from the
  mailbox → `failed`/`mailbox_denied` on the Channels page. Report each finding in the PR
  and the spec's note.

## Gotchas for every task

- The shared test database is the container `vantigo-test-postgres-1`; if Docker's disk
  fills, `docker builder prune -f` and restart it — never touch volumes or images.
- sqlc 1.31.1 cannot parse `@now::timestamptz - interval '…'`: write
  `col + interval '…' < @now`, or compute the instant in Go and compare (the outbox's way).
  `FOR UPDATE OF` and `SKIP LOCKED` parse; the sync worker does not need them (reading 13).
- Agents running at once in the worktree `git add` **only their own files**; the
  pre-commit hook runs tree-wide, so a sibling's half-written file can fail your commit —
  wait and retry rather than fixing theirs.
- Regenerate only your module: `sqlc generate -f internal/<module>/sqlc.yaml`; a repo-wide
  `go generate ./...` while another lane edits its contract rewrites their gen.
- A fresh worktree has no `node_modules`: `bun install --frozen-lockfile` before
  `docs:check` or the frontend tests. Node 26's localStorage and "due today" flakes in
  other packages are not this delivery's.
- Every commit is signed; a force-push to the PR branch is the owner's to run.
- A commit whose docs land in task 7 carries `Docs-Impact: none — the pages land in task
  7's commit of this delivery`; strip those trailers when the docs commit lands in the
  same PR (memory: *Entra phase 2 state*).
- Graph ids are case-sensitive; Message-Ids are compared exactly after trimming and
  bracketing; email addresses case-insensitively. Do not lower-case an id.
- Go's `mime.ParseMediaType` silently drops an RFC 2231 parameter in a charset other than
  UTF-8 or US-ASCII (reading 16) — test the Latin-1 filename explicitly.
- The `sandbox=""` iframe cannot load `/api/...` images (opaque origin, `SameSite=Strict`,
  its own CSP): never "fix" a broken inline image by loosening the sandbox or the CSP.
- The corpus is built by hand; no real customer mail goes into `testdata/`.

## Open questions for the owner

Each has a default the work proceeds on; the PR lists them for confirmation.

1. **References on a reply.** .NET keeps `Distinct().Take(20)` — the **oldest** 20 — which
   drops the very message being answered once a thread holds 20 references, and breaks
   threading in long threads. *Default:* the first id and the last 19 (RFC 5322's advice).
   Or port .NET unchanged?
2. **Reply-all's recipients.** .NET (and M15) takes the latest inbound message's **Cc**
   only; the other **To** recipients of that message (besides the shared mailbox) are not
   included, unlike Outlook's reply-all. *Default:* .NET's rule, Cc only. Or To and Cc?
3. **The original message's download.** *Default:* every inbound message offers
   **Download original** (`.eml`, the unsanitised HTML inside) to anyone with
   `conversations-view`, not only the unreadable ones. Or only when Vantigo could not read
   it?
4. **Inline images from strangers.** *Default:* images the message itself carries (`cid:`
   parts, `data:` URIs) are shown in the thread although unscanned; remote images are
   stripped (M14). Or show inline images only after a click?
5. **Reply-To.** *Default:* a reply goes to the sender (`From`), as .NET did; a
   `Reply-To` header is kept in the raw only. Or honour `Reply-To` as the reply's To?
6. **Re-linking.** M15 says a conversation with a **manual** customer is never re-linked.
   *Default:* one with an **automatic** customer is not re-linked either, so a second sender
   in the thread does not move it. Agree?
7. **Styles in mail HTML.** UGC alone removes every `style` attribute, which flattens most
   Outlook mail. *Default:* a short allow-list of typographic and box properties, never
   `url(…)` or positioning (reading 16). Or UGC unchanged?
