# Outlook connection and Microsoft 365 mail, delivery C — Microsoft 365 channels that send: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a team's Exchange Online **shared mailbox** (`post@`, `support@`, `faktura@`)
becomes a Communications channel that **sends** through Microsoft Graph with the
application's own token, its reach scoped to the channel mailboxes by **Exchange RBAC for
Applications** (`Application Mail Full Access`, `Application MailboxSettings.Read`), not
by a tenant-wide Entra consent. The Owner turns on **Mail channels** in the Entra module
(a switch beside the Outlook connection) and pastes the PowerShell commands the page
fills in; a channel admin then adds a Microsoft 365 channel by its address, or switches an
existing SMTP channel to Microsoft 365, keeping its id, its conversations and its default
flag. Only shared mailboxes are accepted (owner's Q4). A tenant-wide `Mail.*` grant is a
warning, `tenant_wide`, never a block (Q3). The outbox sends the very MIME it builds for
SMTP — our Message-Id, In-Reply-To and References — through `sendMail`; a message too
large for Graph is refused when it is written, not when it is sent.

**Scope — C only.** Not inbound (delivery D: the sync worker, receipts, MIME parsing,
threading, the linker, reply live, migration `00092`), not mail notifications, Outlook
state on close or the import window (E, `00093`), not sending from one's own address (F).
The `ChannelMailbox` contract gets only the methods C calls; D and E add theirs.

**Architecture:** Entra fills a new single-provider slot, `contracts.ChannelMailbox`
(Key, DisplayName, Status, Check, Forget, MaxMessage, Send), on its guarded Graph client
with the **application** credential; it answers only for a mailbox object id `Check`
resolved and recorded in `entra.channel_mailboxes`. Communications is the authority on
which mailboxes are channels (new **rule 15** in module boundaries): `channels.provider =
'entra'`, `channels.settings = {"mailbox": "<object id>"}` and **no** `channel_credentials`
row. The outbox branches after the `delivery_attempted_at` stamp: SMTP as today, or
`mail.RenderMIME` → `ChannelMailbox.Send`. Communications' frontend never reads Entra's
API: it asks its own `GET /channel-providers`.

**Branch:** `claude/communications-m365-channels-c`, at `origin/main` `3102de20` (A #195 and
B #196 merged). The spec names `feat/communications-m365-channels`; this one is the
session's. The pull request opens against `main` (memory: *Stacked PRs per phase*).

**Migrations:** `00090_entra_mail_channels.sql` (the switch, `entra.channel_mailboxes`) and
`00091_communications_channel_settings.sql` (`channels.settings`, one channel per mailbox).
Both numbers are free: the directory ends at `00089_entra_outlook_connections.sql`.

**Spec:** [`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`](../specs/2026-10-29-outlook-connection-and-mail-design.md), M10–M13, M16, M19, *The owner's answers*, *Deliveries* C, *Testing* C.
**Research:** [`…/research/2026-10-29-outlook-connection-and-mail.md`](../research/2026-10-29-outlook-connection-and-mail.md) §2 *Communications*, §3 *Sending*, *Shared mailboxes and RBAC for Applications*, *Throttling and SMTP AUTH*.
**Format models:** [delivery A's plan](2026-10-29-entra-outlook-connection.md), [delivery B's plan](2026-10-29-calendar-outlook-delegated.md).

## Plan readings

1. **Base and when C opens.** `main` holds A (`00088`, `00089`, `outlook_*.go`) and B (the
   calendar on the person's token, no migration). C touches neither the Outlook
   connection nor the calendar. It shares two places with them: the Entra settings
   (`connection.go`, the `EntraSettingsRequest`) and `unexpectedPermissions` (M19). The
   handoff (`.omc/handoff-outlook-mail.md`) records that the branch was cut from B before
   B merged: **before task 2, `git rebase origin/main`** (B's commits are on `main` and
   drop out) and confirm `git log origin/main..` holds only this plan.

2. **Communications' channels today** (`internal/communications/channels.go`).
   - The file's header (`:32-41`) is the port's *Mailgun-branch decision*: every create and
     update refuses a provider other than `smtp`; `verifyChannel` refuses a non-SMTP
     channel defensively (`:59`, `:724-726`).
   - `validateCreateChannel` (`:200-259`) refuses `body.Mailgun` (`:232-234`) and any
     provider but `smtp` (`:236-253`); `PostCommunicationsChannels` (`:296-384`) seals the
     password (`:311`), takes `lockDefaultChannelSlot` (`:323`), **hardcodes**
     `Provider: "smtp"` (`:338`) and inserts the credential row (`:343-348`), all in one
     transaction; 409 `channel_exists` on `ux_channels_type_address` (`:354-357`).
   - `PutCommunicationsChannelsById` (`:414-648`): the provider **defaults to `smtp`**
     (`:453-456`), so a PUT on a Microsoft 365 channel that carries only `isActive` falls
     into `default:` and answers 400 "Credentials for the selected provider are
     required." (`:499-502`) — C must make the default the channel's own provider. The
     write is re-read under the lock (`:555`) and every value derived from that read.
   - `channelResponseOf` (`:128-155`) decodes `credential_settings_json` as SMTP and fills
     the dead `domain`/`region` with nil.
   - `PostCommunicationsChannelsByIdVerify` (`:774-793`): 200 `{ok:true}` or 422
     `destination_rejected` / `verification_failed`.
   - **There is no DELETE of a channel.** `participants.channel_id` and
     `conversations.channel_id` are `ON DELETE RESTRICT` (`00006:63-72`); a channel is
     deactivated, never deleted. *Spec departure:* M11 has Communications call `Forget`
     "when a channel is deleted or switched back to SMTP". C calls it only on the switch
     back to SMTP (reading 13); deactivation keeps the mailbox (a reactivated channel
     sends again with no new `Check`).

3. **Where the settings live** (migration `00006_communications_baseline.sql`).
   `channels` (`:23-37`) has `provider varchar(20) NOT NULL DEFAULT 'smtp'` with no CHECK
   (`:34`), so `entra` needs no change there. `channel_credentials` (`:50-56`) holds
   `settings_json text NOT NULL` beside `secret_ciphertext text NOT NULL` — a row a
   Microsoft 365 channel cannot fill. The reads join it with a `LEFT JOIN`:
   `ListChannels`, `GetChannelByID` (`queries/channels.sql:149-170`) and
   `GetOutboundMessageForSend` (`queries/outbox.sql:64-86`), whose comment says
   `channel_provider` is "omitted rather than carried unread" (`:78-79`) — C carries it.
   Hence M12's `00091`:
   ```sql
   ALTER TABLE communications.channels ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';
   -- One channel per mailbox: two aliases of one shared mailbox would otherwise make two
   -- channels on one entra.channel_mailboxes row, and Forget of one would orphan the other.
   CREATE UNIQUE INDEX ux_channels_entra_mailbox
       ON communications.channels ((settings->>'mailbox')) WHERE provider = 'entra';
   ```
   *Plan addition:* the unique index is not in the spec; its violation answers 409
   `channel_exists` with "A channel for this mailbox already exists." `communications/sqlc.yaml`
   (`:7`) lists only `00006`; it gains `00091`. `internal/db/schema_test.go:186-200`
   checks each module's sqlc schema list against the migrations, so both sqlc files must
   list their new migration.

4. **The outbox today** (`internal/communications/outbox.go`).
   - `deliver` (`:295-397`): load the message (`:312`), the sendable deliveries
     (`:320-329`), nothing to send → completed (`:333-343`), a non-clean attachment →
     `errAttachmentsUnavailable` (`:351-355`), the suppression re-check (`:360-368`),
     **step 7 the stamp** (`:373-378`), **step 8** the envelope (`:381`), `smtpConfig`
     (`:385`), the send under `outboxSendTimeout` = 30 s (`:389-393`, `:77`), then
     `complete` (`:396`). M13's branch goes between `:384` and `:385`.
   - `envelope` (`:624-687`) builds `mail.Outbound` with `MessageID: hexN(id) +
     "@vantigo.invalid"` (`:630`), To/Cc/**Bcc** from the deliveries (`:647-656`), the
     threading headers from `channel_metadata_json` (`:657-670`, never written before D)
     and the attachments' bytes.
   - `markFailed` (`:491-567`) has **no terminal error**: a job is `failed` only when
     `attempts >= outboxMaxAttempts` (8, `:72`, `:515`). `last_error` is the **constant**
     `outboxFailureMessage` (`:79-86`) — the module's only redaction of outbound errors.
     *Spec refinement:* M13 says the terminal errors fail the job "with the code". C adds
     `markFailed(…, cause, terminal bool)`: a terminal cause writes `failed` /
     `submission_failed` at once; `last_error` **stays the constant**, and the code goes to
     the event's `data_json` (`{"error":"Outbound delivery failed.","code":"mailbox_denied"}`)
     and the log line. The metric is `jobsFailed`.
   - No retry inside the call: the outbox retries on `retryBackoff` (`backoff.go`).

5. **The MIME builder** (`internal/mail/outbound.go`). `Outbound` (`:63-82`) and
   `Outbound.message(from)` (`:134-240`) build a go-mail `*Msg`; `SendOutbound`
   (`:100-118`) dials SMTP. `RenderOutboundForTests` (`export_test.go`) is the only way to
   render without sending. **go-mail v0.8.1's writer omits the `Bcc` header**:
   `msgwriter.go:101-131` writes From, then only `HeaderTo, HeaderCc, HeaderReplyTo`. Over
   SMTP the Bcc recipients still receive the mail through the envelope (`RCPT TO`); over
   Graph's MIME `sendMail` there is no envelope — the recipients are the headers — so a
   Bcc recipient would be **silently dropped**. C adds
   `mail.RenderMIME(from string, out Outbound, opts ...RenderOption) ([]byte, error)` (go-mail's
   `WriteTo`, the same `message`; `WithBcc()` adds the `Bcc:` header go-mail leaves out),
   and the Bcc recipients also travel **beside** the MIME so the provider picks its path
   (readings 9, 12).

6. **Attachments and sizes.** `COMMUNICATIONS_ATTACHMENT_MAX_BYTES` is 10 MiB by default,
   at most 50 MiB (`config/config.go:1161`) — per file. The staging handler refuses a
   larger file with **413** `attachment_too_large`
   (`attachments.go:441`, `:458-472`). Staged rows carry `size_bytes`
   (`queries/attachments.sql:123-132`, `ListStagedAttachmentsForClaim`), which
   `queueReply`'s preflight already reads (`conversations_reply.go:285-299`).
   **Only reply carries attachments**: `CreateConversationRequest` has no `attachmentIds`
   (`openapi/communications.yaml:770-813`; `attachmentIds` appears once, in
   `ReplyRequest`, `:884`). *Spec correction:* M13's "reply and create refuse with 422
   `too_large`" applies to reply alone; create carries no attachments. And reply is
   **still unreachable** in C — `resolveReplyRecipients` answers 422 `recipients_missing`
   with no inbound participant (`conversations_reply.go:93-104`, `:214-233`) until D — so
   C's size check is wired and tested but has no live caller until D. The staging check
   (reading 15) is live from C on.

7. **Communications reaches no neighbour through a guard.** It calls
   `s.deps.Directory` inline (`conversations_create.go:163`, `:259`;
   `conversations_patch.go:141`) and has no `contractscalls.go`, no `inOpenTx`. The other
   modules mark a locked context (`calendar/server.go:45-56`, `entra/contractscalls.go:47`)
   and refuse a contract call inside a transaction (`calendar/contractscalls.go:58-66`).
   C adds `communications/contractscalls.go` with the slot's accessors and the same guard
   (a `lockedTxKey` set by a new `withTx` wrapper used by the channel writers that this
   delivery touches), so rule 15's "nothing is called inside a transaction" is checked,
   not just intended.

8. **The slot's machinery.** `module.Deps.MailboxCalendar` (`module/module.go:206-216`),
   `Module.MailboxCalendar func(Deps) contracts.MailboxCalendar` (`:438-443`),
   `withMailboxCalendar` (`module/workers.go:150-162`) called by Compose
   (`compose.go:362-368`) and Workers (`workers.go:90`), `soleProvider` refusing two,
   `modtest.WithMailboxCalendar` (`modtest/modtest.go:373`), `compose_test.go:2463-2490`.
   C copies each for `ChannelMailbox`: `Deps.ChannelMailbox`, `Module.ChannelMailbox`,
   `withChannelMailbox` (after `withMailboxCalendar`), `modtest.WithChannelMailbox`,
   `TestCompose_ResolvesTheChannelMailboxSlot`. Entra's `Module()` (`entra/module.go:143-150`)
   gains `ChannelMailbox: newChannelMailbox`.

9. **The contract** (`internal/contracts/channel_mailbox.go`, new). The errors are the
   existing ones (`contracts/mailbox_calendar.go:452-475`) plus one:
   ```go
   // ChannelMailbox is a provider's mailboxes as Communications' channels use them
   // (Outlook connection design M11, rule 15): Entra's Exchange Online shared
   // mailboxes today. Communications is the authority on which mailboxes are
   // channels; the provider writes only to a mailbox Check resolved. Key, DisplayName,
   // Status and MaxMessage make no network call; Check, Forget and Send do, and are
   // never called inside a transaction. A single-provider slot.
   type ChannelMailbox interface {
       Key() string          // "entra": a channel's provider value
       DisplayName() string  // "Microsoft 365"
       Status(ctx context.Context) (MailboxStatus, error)
       Check(ctx context.Context, address string) (ChannelMailboxInfo, error)
       Forget(ctx context.Context, mailbox string) error
       MaxMessage() int64
       Send(ctx context.Context, mailbox string, mime []byte, bcc []string) error
   }
   type ChannelMailboxInfo struct {
       Mailbox     string // the object id, lower case
       Address     string // the mailbox's primary SMTP address, lower case
       DisplayName string
       Shared      bool   // userPurpose = shared
   }
   // ErrMailTooLarge: the message is larger than MaxMessage, or the provider refused it
   // as too large (413). Terminal for the message.
   var ErrMailTooLarge = errors.New("contracts: the message is larger than the mailbox provider takes")
   // MailboxTenantWide is MailboxStatus.Reason while available but the provider's grant
   // reaches every mailbox in the tenant (M10): a warning, never a refusal.
   const MailboxTenantWide = "tenant_wide"
   ```
   - *Spec departures:* (a) C declares only the seven methods it calls; `Delta`, `Raw`,
     `Mark`, `EnsureFolder`, `Watch`, `Unwatch`, `Notified` and `MailDelta`/`MailMark`/
     `MailboxNotice` arrive with D and E (an interface grows with its consumer; no stubs).
     (b) `Send` takes `bcc []string` beside the MIME (reading 5): the provider sends with
     `sendMail` when it is empty, and through a draft when not (reading 12; owner question
     1). (c) `Status` reuses `MailboxStatus` and its three states; `available` with Reason
     `tenant_wide` is M10's warning.
   - `Status`: `available` while `mail_channels` is on and the connection `connected`
     (Reason `tenant_wide` when M10 says so); else `unavailable` with Reason `switch_off`
     or the connection's reason. There is no `cleanup` state for mail.

10. **Entra's connection and its permissions** (`internal/entra/connection.go`).
    `requiredPermissions` (`:35-44`), `missingPermissions` (`:87-96`),
    `unexpectedPermissions` (`:98-111`, "The mail roles count until delivery C reports
    them under tenant_wide"), `outlookConnectBody` (`:146-170`, the order `off`,
    `unavailable`, `consent_missing`, `ready`, `untested`), `connectionBody` (`:176-211`),
    `mergeSettings` (`:371-460`, the bool fields' table `:446-460`),
    `PutEntraConnectionSettings` (`:469-543`, the Outlook switch's close-all `:502-508`),
    `PostEntraConnectionTest` (`:557-683`, the `unexpected:` codes `:646-649`).
    - M10's `mailChannels` is computed like `outlookConnect`: `mailChannelsBody(conn,
      status, reason)` — `off`; `unavailable` (not `connected`, with the reason);
      `tenant_wide` when `PermissionsPresent` holds any of `Mail.Read`, `Mail.ReadBasic`,
      `Mail.ReadBasic.All`, `Mail.ReadWrite`, `Mail.Send`; else `ready`.
    - *Spec correction:* M10 also lists `full_access_as_app`. That is an application role
      of the **Office 365 Exchange Online** resource (EWS), never in a Graph token's
      `roles`, which is all the test reads (`graphScope = …/.default` on
      `graph.microsoft.com`, `graph.go:52`). C keeps it in the list for completeness and
      the reference says the test cannot see it.
    - M19 change: `unexpectedPermissions(present, mailChannels bool)` leaves out the five
      `Mail.*` roles **only while the switch is on** (they are then `tenant_wide`'s, "not
      twice"); while off they stay unexpected. Callers `:195`, `:640`.
    - `mailChannels` also answers `servicePrincipalId` (the connection's
      `service_principal_id`, `00063_entra_groups.sql:53`, written by the test from the
      token's `oid`) so the page can fill `New-ServicePrincipal -ObjectId`; absent before
      the first test.
    - The switch: `mailChannels *bool` in `EntraSettingsRequest`, one more row in
      `mergeSettings`' table; `UpdateConnectionSettings` gains the column. Turning it off
      closes nothing (there is no per-person state); the channels' sends then answer
      `ErrMailboxUnavailable` and are retried on the backoff until the 8 attempts end.
    - `openapi/entra.yaml`: `EntraMailChannels {enabled, status (off|ready|tenant_wide|
      unavailable), reason?, servicePrincipalId?}` required on `EntraConnection`
      (`:100-180`); `mailChannels` in `EntraSettingsRequest` (`:708-`);
      `permissionsUnexpected`'s description (`:134`) says the mail roles are reported as
      `tenant_wide` while the switch is on.

11. **`Check`** (Entra, `channel_mailbox.go` new), with the application credential
    (`credentialFor`, `credentials.go:86`) and **no** delegated token:
    1. `GET /users/{address}?$select=id,mail,userPrincipalName,displayName,accountEnabled`.
       Graph's path takes an id or a **userPrincipalName**, not a mail address
       (Microsoft facts, below); on a 404, `GET /users?$filter=mail eq '{address}'&$select=…`
       (the quote doubled) — one answer, else `ErrMailboxUnknown`. An address with a
       leading `$` uses `/users('{address}')`. Escape with `url.PathEscape`.
       Needs `User.Read.All`, which the required `User.ReadWrite.All` covers.
    2. `GET /users/{id}/mailboxSettings/userPurpose` → `{"value":"shared"}`; `Shared` iff
       `shared`. Needs `MailboxSettings.Read` — the RBAC role `Application
       MailboxSettings.Read` **exists** (Microsoft facts), so M10's fallback to
       `accountEnabled` is **not built**; a 403 here is `ErrMailboxDenied` like any other
       (the guide names both roles).
    3. `GET /users/{id}/mailFolders/inbox?$select=id` — proves the read side of `Mail Full
       Access` reaches the mailbox (the scope); `sendMail` itself cannot be probed without
       sending.
    - 403 `ErrorAccessDenied` (any step) → `ErrMailboxDenied`; 404 on step 1 →
      `ErrMailboxUnknown`; `mailboxGoneCodes` (`mailbox.go:100-108`) on steps 2–3 →
      `ErrMailboxGone`; switch off, not configured, no credential → `ErrMailboxUnavailable`;
      `errThrottled` and transient errors pass (a failure to retry).
    - On reach, `UpsertChannelMailbox(object_id, address, checked_at)`. The result is the
      object id in lower case and the `mail` address in lower case. Only shared mailboxes
      are then accepted by Communications (Q4); `Check` itself answers either.
    - Logged at info with the tenant and the object id, never the address's words beyond
      the address itself; no audit event (the channel's create is Communications').

12. **`Send`** (Entra): `mailbox` must be a row in `entra.channel_mailboxes`
    (`ErrMailboxUnknown` otherwise); `len(mime) > MaxMessage()` → `ErrMailTooLarge` before
    any call; `Status` not `available` → `ErrMailboxUnavailable`.
    - `bcc` empty: `POST /users/{oid}/sendMail`, body the MIME **base64-encoded**,
      `Content-Type: text/plain`; 202. A new `graphClient.postRaw(ctx, cred, path,
      contentType, data, into)` beside `putRaw` (`graph.go:362-365`), sent with
      **`noRetry`** (`:311`): a 5xx is not retried inside the call, so a send Exchange may
      have taken is not sent twice by the client (the outbox's own retry stays, at-least-once).
    - `bcc` non-empty: `POST /users/{oid}/messages` with the base64 MIME — rendered **with**
      a `Bcc:` header (`mail.RenderMIME`'s `WithBcc` option), which Graph documents for a
      MIME draft ("the applicable Internet message headers ("To", "CC", "BCC",
      "Subject")") — creating a draft in the shared mailbox (201, its `id` and
      `bccRecipients`); the provider checks the answer's `bccRecipients` hold every
      address in `bcc` (else it deletes the draft and answers a failure, never sending
      without them); then `POST /users/{oid}/messages/{id}/send` (202). A failure after the
      draft exists deletes it best-effort (`DELETE …/messages/{id}`). `Mail.ReadWrite`,
      which `Application Mail Full Access` holds. *Plan decision, owner question 1.*
    - Refusals: 403 → `ErrMailboxDenied`; 404 `mailboxGoneCodes` → `ErrMailboxGone`; 413 →
      `ErrMailTooLarge`; 400 → a failure (a bug to retry, then fail by attempts);
      `errThrottled` → a failure (retried).
    - `MaxMessage()` = **3 000 000** bytes of MIME: base64 makes it 4 000 000 bytes of
      request body, Graph's documented 4 MB (Microsoft facts). The constant is named
      `graphMaxMIMEBytes` and the reference states it.
    - `Forget(mailbox)` deletes the `channel_mailboxes` row; idempotent.

13. **Communications' handlers in C** (M12).
    - **Create** `{type: email, provider: entra, address, displayName?, isDefault?}`: the
      validation (`validateCreateChannel`) accepts `smtp` or the slot's `Key()`; for
      `entra`, an `smtp` block is a 400 (`smtp`: "SMTP credentials require the smtp
      provider."). Then, **before** the transaction, `Status` (not `available` → 409
      `mail_channels_off`) and `Check(address)`:

      | `Check` answers | Result |
      | --- | --- |
      | reach, `Shared` | created **active**, `settings = {"mailbox": oid}`, `address` = Check's address |
      | reach, not `Shared` | 422 `personal_mailbox` |
      | `ErrMailboxDenied` | 422 `mailbox_denied` |
      | `ErrMailboxUnknown`, `ErrMailboxGone` | 422 `mailbox_unknown` |
      | `ErrMailboxUnavailable`, no slot, `Key()` not `entra` | 409 `mail_channels_off` |
      | anything else (throttled, Graph not answering) | 503 `provider_unreachable` |

      *Plan decisions:* the stored address is Check's primary address (an alias typed by
      the admin becomes the mailbox's own); `ErrMailboxGone` reads as unknown to the admin;
      a failure to reach Graph is **503** `provider_unreachable` "Microsoft 365 did not
      answer. Try again." (a new response on the operation). The insert writes `provider =
      'entra'`, `settings`, and no credential row. The unique index's violation is 409
      `channel_exists`.
    - **Switch** `POST /channels/{id}/provider` `{provider: entra}`: 404; 422
      `provider_unchanged` when it is already `entra`; the same `Status`/`Check` table on
      the channel's own address (before the transaction); then, under
      `lockDefaultChannelSlot` and a re-read, `SwitchChannelProvider` (provider, settings,
      address unchanged) and `DeleteChannelCredential` in **one transaction** (M12). The
      id, the conversations and `is_default` stay. Answer 200 `ChannelResponse`.
      *Note:* the `channel_mailboxes` row `Check` wrote stays if the transaction then
      fails; it only allows sending to a mailbox that was checked, and the next `Check`
      rewrites it.
    - **Back to SMTP:** `PUT /channels/{id}` with `provider: smtp` and a full `smtp` block
      (host, port, **password required**) on an `entra` channel writes the credential row
      (the existing upsert), sets `provider = 'smtp'`, `settings = '{}'`, and **after
      commit** calls `Forget(old mailbox)`, best-effort and logged.
    - **PUT** without `provider` keeps the channel's own provider (fixes reading 2's
      default); `provider: entra` on PUT is a 400 (switching to Microsoft 365 is the POST).
    - **Verify** on an `entra` channel runs `Status` and `Check(address)`: 200 `{ok:true}`
      when it reaches a shared mailbox **with the same object id** as stored; else 422 with
      the table's codes, `mailbox_changed` when the object id differs.
    - **Read:** `ChannelResponse` — `settings` stays the SMTP shape, `null` for a Microsoft
      365 channel, `domain`/`region` dropped; new `mailbox: {id} | null`;
      `hasCredentials` false for `entra`. *Spec departure:* M12 says "the DTO's `settings`
      becomes a `oneOf` by provider". The repo has `oneOf` only in `identity.yaml`, and
      oapi-codegen's unions for a strict-server response are clumsy; a nullable sibling
      object says the same with no union.
    - **`GET /api/v1/communications/channel-providers`** (`channels-manage`, new):
      `[{key: "smtp", displayName: "SMTP", available: true}, {key: "entra", displayName:
      "Microsoft 365", available, reason?, warning?: "tenant_wide", maxMessageBytes}]` —
      the second only while a slot is filled. Its own route, not `/channels/providers`,
      which would sit beside `/channels/{id}`.
    - `MailgunChannelCredentialRequest` and the `mailgun` fields leave the contract
      (`openapi/communications.yaml:844-861`, `:756-759`, `:973-976`).
    - A rate limit `communications-channel-check`, 30 a minute, on create, switch and
      verify (each reaches Graph up to three times), as Entra's policies
      (`entra/module.go:79-118`).

14. **Sending** (M13; `outbox.go`).
    - `GetOutboundMessageForSend` selects `ch.provider AS channel_provider, ch.settings AS
      channel_settings`; the query's comment (`queries/outbox.sql:65-79`) is rewritten.
    - After the stamp, `channelProvider == "smtp"` → today's path, unchanged. Otherwise
      `w.sendThroughMailbox(ctx, message, envelope)`: `settings.mailbox` empty → terminal
      `errChannelMisconfigured`; no slot or `Key() != provider` → `ErrMailboxUnavailable`
      (retried; "never sent through another provider", M11); `mail.RenderMIME(address,
      envelope)` — with `WithBcc()` when `envelope.Bcc` is not empty — and `Send(sendCtx, mailbox, mime, envelope.Bcc)` under the same
      `outboxSendTimeout`, through the accessor (reading 7).
    - Terminal (`markFailed(…, true)`) with the code: `ErrMailboxDenied`
      (`mailbox_denied`), `ErrMailboxGone` (`mailbox_gone`), `ErrMailboxUnknown`
      (`mailbox_unknown`), `ErrMailTooLarge` (`too_large`), `errChannelMisconfigured`
      (`channel_misconfigured`). Retried: `ErrMailboxUnavailable` and every other error.
    - 202 is acceptance (`relay_accepted`), as SMTP's 250; the possible-duplicate metric
      and the lease rules are unchanged.

15. **Size when the mail is written** (M13).
    - `mailBudget(channel)`: nil for SMTP; for `entra`, `MaxMessage()` from the slot (no
      network). The estimate of a message: `Σ size_bytes × 1.37 + len(text) + len(html) ×
      1.37 + 16 KiB` (the html part may be base64 or quoted-printable; the 16 KiB is the
      headers and the boundaries). *Plan decision:* the spec's 37 % margin is applied to
      the HTML body as well.
    - **Staging** (`PostCommunicationsConversationsByIdAttachments`): on an `entra`
      conversation, a file whose `size × 1.37 + 16 KiB` exceeds the budget is **413**
      `attachment_too_large` with "The attachment is larger than this channel can send
      (about {n} MB)." — live in C.
    - **Reply** (`queueReply`, after the preflight `:285-299`): the estimate over the budget
      → **422** `too_large`, "The message and its attachments are larger than this channel
      can send." `ReplyRequest`'s operation gains nothing new (422 is declared).
    - `ConversationDetailResponse` gains `maxMessageBytes: integer (int64), nullable` — the
      channel's budget, null for SMTP — so the composer can show the limit beside the
      attachments without `channels-manage`.

16. **The Entra frontend** (`apps/entra/frontend/src`). `components/settings-form.tsx`:
    `mailChannels` in `SettingsValues` (`:45`, `:60`, `:63-90`), a second switch after the
    Outlook connection (`:284-301`) labelled M10's *"Mail channels: let Communications
    send and receive through Exchange Online shared mailboxes"*, a `MailChannelsStatus`
    like `OutlookConnectStatus` (`:345-366`) — `ready` (green), `tenant_wide` (orange, M10's
    sentence), `unavailable` (gray with the reason) — and, while on, the six PowerShell
    commands in a `Code` block with **Copy**, `<client id>` and `<object id>` filled from
    `clientId` and `mailChannels.servicePrincipalId` (a placeholder and a hint "Test the
    connection first" while absent), `post@firma.no` left for the admin. `i18n.ts` en/nb.
    `pages/entra.test.tsx` and `test/server.ts` gain `mailChannels`.

17. **The Communications frontend** (`apps/communications/frontend/src`).
    - `components/ChannelForm.tsx`: the Mailgun branch and its values go (`:10-19`,
      `:35-38`, `:62-75`, `:92`, `:104-114`); the provider select offers **SMTP** and, while
      `channel-providers` answers `entra` `available`, **Microsoft 365**; the Microsoft 365
      form is **Email address** and **Display name** only, with a line "The mailbox must be
      a shared mailbox in the application's scope." Server codes map to field messages:
      `personal_mailbox`, `mailbox_denied` (with a link to `/en/admin/entra/#mail-channels`),
      `mailbox_unknown`, `mail_channels_off`, `channel_exists`, `provider_unreachable`.
    - `pages/admin.channels.tsx`: the provider badge reads the provider's display name; a
      **Switch to Microsoft 365** action on SMTP rows while available, with a confirm
      ("The SMTP settings are deleted. To go back you enter them again."); the
      `tenant_wide` warning above the table while `channel-providers` says so.
    - `components/ConversationComposer.tsx`: with `maxMessageBytes`, "Attachments up to
      {size} in total on this channel" beside the attachments, and the 413/422 codes shown.
    - `catalog.ts` en/nb; `bun run gen:client`; `bun run translations:check`.

18. **Module boundaries** (`docs/.../contributing/module-boundaries.md`): rule 14
    (`:565-`) is the calendar's write; **rule 15** — *Communications writes through
    `ChannelMailbox`* — states M11's discipline: Communications is the authority on which
    mailboxes are channels, the provider writes only to a mailbox `Check` resolved, `Key`,
    `DisplayName`, `Status` and `MaxMessage` make no network call, nothing is called inside
    a transaction, and every write is idempotent or reports its outcome (`Send` is
    at-least-once, as SMTP). The slot list (`:116`, `:248`, `:1136`) gains it.

19. **Docs and the map.** `bun run tools/docs/check-coverage.ts --list` puts
    `internal/communications`, `apps/communications/frontend` and `openapi/communications.yaml`
    under `reference/communications.md`; `apps/communications/frontend` also under
    `user/communications.md` (en, nb); `internal/entra`, `apps/entra/frontend`,
    `openapi/entra.yaml` and Entra's migrations (listed one by one) under
    `reference/entra.md`; `entra/module.go`, `graph.go`, `credentials.go`, `status.go`,
    `mailbox*.go`, `outlook*.go` under `admin/entra.md` (en, nb); `apps/entra/frontend`
    under `user/entra.md` (en, nb); `internal/contracts`, `internal/modtest`,
    `internal/module` under `contributing/module-boundaries.md`; `internal/mail` under
    `contributing/platform-packages.md`. There is **no** `admin/communications.md`; the
    operator's procedure for mail channels is the Entra administration page's. The new
    migrations join the `sources` of `reference/entra.md` (`00090`) and
    `reference/communications.md` (`00091`).

20. **SMTP AUTH's end in Exchange Online** (research §3, *Throttling and SMTP AUTH*): the
    reference says, with the research's caveat (secondary sources for MC786329), that an
    SMTP channel on Exchange Online needs an admin's exception from the end of December
    2026 and that switching it to Microsoft 365 keeps its id and history.

### Microsoft facts, as checked for C

Read on learn.microsoft.com on 2026-10-29.

- **`sendMail` with MIME.** `POST /users/{id | userPrincipalName}/sendMail`;
  `Content-Type: text/plain` for MIME, the headers and the MIME content **base64-encoded**
  in the body; attachments inside the MIME; **202 Accepted**, which "doesn't indicate that
  the request processing has completed"; malformed base64 is 400
  `ErrorMimeContentInvalidBase64String`. Permission `Mail.Send` (application). "This
  method saves the message in the **Sent Items** folder." `saveToSentItems` is a
  parameter of the **JSON** form only; with MIME there is no way to pass it, so a MIME
  send is always saved to Sent Items (stated for the JSON form's default and the method as
  a whole; no MIME-specific switch is documented).
  ([user: sendMail](https://learn.microsoft.com/en-us/graph/api/user-sendmail?view=graph-rest-1.0);
  [Send MIME](https://learn.microsoft.com/en-us/graph/outlook-send-mime-message))
- **The size limit — partly verified.** Microsoft's normative text states 4 MB only for
  S/MIME payloads ("413 Request Entity Too Large",
  [user: create message](https://learn.microsoft.com/en-us/graph/api/user-post-messages?view=graph-rest-1.0));
  a Microsoft Q&A answer gives "around 4 MB" with a 413 for `sendMail`
  ([Q&A](https://learn.microsoft.com/en-in/answers/questions/1824204/graph-api-user-sendmail-request-size-limit)).
  Larger messages need a draft and an upload session (3–150 MB per attachment,
  `Mail.ReadWrite`; [large attachments](https://learn.microsoft.com/en-us/graph/outlook-large-attachments)),
  out of scope (M18). C's `MaxMessage` is 3 000 000 bytes of MIME (4 000 000 after
  base64) and a 413 is `ErrMailTooLarge`. **Unverified:** the exact threshold, and
  whether it counts the base64 body or the decoded MIME.
- **Message-ID, In-Reply-To, References — not verified.** No Microsoft page states
  whether Exchange keeps a supplied `Message-ID` on a MIME `sendMail`; secondary sources
  disagree, and Outlook in online mode is reported to restamp `PR_INTERNET_MESSAGE_ID`.
  C sends ours and the real-tenant checklist reads the sent item's `internetMessageId`
  and the recipient's raw headers. If Exchange rewrites it, D's threading falls back to
  `conversationId` (M13, M15) and the reference says so.
- **Bcc in MIME — not verified, reported not honoured.** No Microsoft page says how a
  MIME `Bcc:` header is treated. Thunderbird's Graph client creates a draft first,
  noting that the MIME carries "all headers except for Bcc" and sets the Bcc recipients
  on the draft ([comm-central send_message.rs](https://searchfox.org/comm-central/source/rust/graph_xpcom/src/client/send_message.rs));
  a plugin's support thread reports Bcc headers in MIME not delivered. And go-mail does
  not write the header at all (reading 5). **For a MIME draft** Graph does document the
  header: `POST /users/{id}/messages` takes "the MIME content with the applicable Internet
  message headers ("To", "CC", "BCC", "Subject")", answers 201 with the message
  (`bccRecipients` among its properties), needs `Mail.ReadWrite`, and limits S/MIME
  payloads to 4 MB (413)
  ([user: create message](https://learn.microsoft.com/en-us/graph/api/user-post-messages?view=graph-rest-1.0)).
  Hence reading 12's draft path for a message with Bcc recipients, and owner question 1.
  **Unverified:** that the draft keeps our Message-ID either.
- **RBAC for Applications — verified.** The supported roles include
  `Application Mail.Send` (`Mail.Send`), `Application MailboxSettings.Read`
  (`MailboxSettings.Read`), `Application Mail.ReadWrite` (no send) and **`Application
  Mail Full Access`** (`Mail.ReadWrite`, `Mail.Send`). Syntax:
  `New-ServicePrincipal -AppId <AppId> -ObjectId <service principal object id> -DisplayName <name>`
  (the **Enterprise applications** page's ids, not App registrations');
  `New-ManagementScope -Name "…" -RecipientRestrictionFilter "CustomAttribute1 -eq '…'"`;
  `New-ManagementRoleAssignment -Role <role> -App <ObjectID, AppID or DisplayName> -CustomResourceScope <scope>`
  (or `-RecipientAdministrativeUnitScope`); `Test-ServicePrincipalAuthorization -Identity <…> [-Resource] <mailbox>`
  answering `InScope` per role, which "excludes permissions that might be granted
  separately in Microsoft Entra ID". The cache takes **30 minutes to 2 hours**; the
  test bypasses it. The grants are **a union** with Entra's: "the union of an unscoped
  `Mail.Read` grant from Microsoft Entra and a resource-scoped `Mail.Read` grant in
  Application RBAC results in no effective resource scoping". Requires Organization
  Management (Exchange) or Exchange Administrator (Entra). "Exclusive management scopes
  don't restrict app access." Group filters (`MemberOfGroup`) count direct members only.
  ([RBAC for Applications](https://learn.microsoft.com/en-us/exchange/permissions-exo/application-rbac))
  **Unverified:** that `CustomAttribute10` on a *shared* mailbox matches the scope —
  it is a recipient property and should; `Test-ServicePrincipalAuthorization -Resource
  post@…` in the checklist proves it.
- **`/users/{address}` — verified.** `GET /users/{id | userPrincipalName}`; application
  permission **`User.Read.All`** least, `User.ReadWrite.All` higher — Vantigo already
  requires `User.ReadWrite.All`, so no new permission. The path takes a UPN, **not** a
  mail address (reading 11's `$filter=mail eq` fallback), and a UPN beginning with `$`
  needs `/users('$x@y')`.
  ([Get user](https://learn.microsoft.com/en-us/graph/api/user-get?view=graph-rest-1.0))
- **`userPurpose` — verified.** `GET /users/{id}/mailboxSettings/userPurpose` answers
  `{"value": "shared"}`; values `user`, `linked`, `shared`, `room`, `equipment`,
  `others`; application permission `MailboxSettings.Read`.
  ([mailboxSettings](https://learn.microsoft.com/en-us/graph/api/resources/mailboxsettings?view=graph-rest-1.0);
  [Get mailboxSettings](https://learn.microsoft.com/en-us/graph/api/user-get-mailboxsettings?view=graph-rest-1.0))
- **Not re-checked for C:** throttling (research §3: 10 000 requests per 10 minutes per
  app and mailbox, 4 concurrent) and the SMTP AUTH schedule (research §3, secondary
  sources).

## Tasks

- [ ] **1. This plan.** Committed alone, no `Docs-Impact` trailer:
  `docs(communications, entra): delivery C's plan — Microsoft 365 channels that send: the Mail channels switch and tenant_wide, the ChannelMailbox slot and rule 15, entra.channel_mailboxes and channels.settings, Check of a shared mailbox, create and switch of provider, sendMail with our MIME and Bcc through a draft, size refused when the mail is written, and the pages in both languages`.

- [ ] **2. The schema, the contract and the slot (one worker, alone; after the rebase of
  reading 1).** Readings 3, 8, 9.
  - [ ] `00090_entra_mail_channels.sql`: `ALTER TABLE entra.connection ADD COLUMN
    mail_channels boolean NOT NULL DEFAULT false;` and
    `CREATE TABLE entra.channel_mailboxes (object_id uuid PRIMARY KEY, address varchar(320)
    NOT NULL, checked_at timestamptz NOT NULL);` with a header comment in the house style
    (`00089`'s) and a `-- +goose Down`. `00091_communications_channel_settings.sql`
    (reading 3) with its Down. Both sqlc files list them.
  - [ ] Failing tests first: `db/schema_entra_mail_channels_test.go` and
    `db/schema_communications_channel_settings_test.go` (the columns, the default `'{}'`,
    the partial unique index refusing a second `entra` row with the same mailbox and
    allowing two SMTP rows), in the pattern of `schema_entra_outlook_test.go`;
    `module/compose_test.go` `TestCompose_ResolvesTheChannelMailboxSlot` (resolved,
    preset kept, two refused); `modtest/channel_mailbox_test.go`.
  - [ ] `contracts/channel_mailbox.go` (reading 9); `module.Deps.ChannelMailbox`,
    `Module.ChannelMailbox`, `withChannelMailbox` in Compose and Workers;
    `modtest.FakeChannelMailbox` — `SetMailbox(address string, info ChannelMailboxInfo)`,
    `SetStatus`, `FailWith(method, err)`, `Sent() []FakeSend{Mailbox, MIME, Bcc}`,
    `Forgotten()`, `Calls()`, `MaxMessage` settable — and `modtest.WithChannelMailbox`.
  - [ ] `sqlc generate` for both modules (no query yet uses the columns; the models
    change); `go build ./... && go vet ./...`; `mise run server:test` for `db`, `module`,
    `modtest`.
  - [ ] Commit (`Docs-Impact` trailer naming task 9):
    `feat(entra, communications): the mail channels' schema and slot — entra.connection.mail_channels and entra.channel_mailboxes (00090), communications.channels.settings with one channel per mailbox (00091); contracts.ChannelMailbox with Key, DisplayName, Status, Check, Forget, MaxMessage and Send, ErrMailTooLarge and tenant_wide; Compose and Workers resolve it; modtest's fake`.

- [ ] **3. Entra: the switch and its status, after 2.** Reading 10.
  - [ ] Failing tests first in `connection_test.go`: the switch saved, audited
    `entra.settings.changed` with `mailChannels`, If-Match kept; `mailChannels.status`
    `off`, `unavailable` (with the reason), `ready`, `tenant_wide` for each of the five
    roles (a `graphtest.FakeGraph{Roles: …}` with `Mail.Send`); `servicePrincipalId` after
    a test and absent before; `permissionsUnexpected` holding `Mail.Send` while the switch
    is off and not while on, the test's `unexpected:Mail.Send` code likewise.
  - [ ] `queries/connection.sql`: `UpdateConnectionSettings` with `mail_channels`;
    `sqlc generate -f internal/entra/sqlc.yaml`; `connection.go` (reading 10);
    `openapi/entra.yaml`; `go generate ./...`; `bun run gen:client`.
  - [ ] `go vet`, `mise run server:test` (entra).
  - [ ] Commit:
    `feat(entra): the Mail channels switch — mail_channels in the settings, mailChannels' status off, unavailable, tenant_wide or ready, with the service principal's object id for the RBAC commands; the Mail.* roles reported as tenant_wide while it is on, unexpected while it is off`.

- [ ] **4. Entra: the `ChannelMailbox` provider and the fake Graph's mail, after 3.**
  Readings 11, 12.
  - [ ] `graphtest` first, with its tests (`graphtest/mail.go`, `mail_test.go`): `User`
    gains `Shared bool` (userPurpose `shared`, else `user`); `userByID` also matches a
    UPN case-insensitively; `listUsers` takes `$filter=mail eq '…'`;
    `GET /users/{id}/mailboxSettings/userPurpose`; `GET /users/{id}/mailFolders/inbox`;
    `POST /users/{id}/sendMail` (`text/plain`, base64 decoded, 400
    `ErrorMimeContentInvalidBase64String` when not, **413** over 4 000 000 bytes of body,
    recorded as `SentMail{UserID, MIME, Bcc}` with `g.Sent()`); `POST /users/{id}/messages`
    (MIME draft; its `Bcc:` header read into `bccRecipients`), `POST …/messages/{id}/send`,
    `DELETE …/messages/{id}`, `g.Drafts()`. `MailboxDenied` answers 403
    `ErrorAccessDenied` on every mail route (the RBAC scope); `NoMailbox` 404
    `MailboxNotEnabledForRESTAPI`. The **application** token reaches the mail routes
    (`scopeRefusal`'s `mailboxPath`, `graph.go:1697`, stays calendar-only); a delegated
    token for another user is 403, as for the calendar. The file's header (`:20-46`)
    lists the routes.
  - [ ] Failing tests in `channel_mailbox_test.go`: `Check`'s four outcomes — a shared
    mailbox (row written, lower-cased id and address), a personal one (`Shared` false), a
    denied one (`ErrMailboxDenied`), an unknown one (`ErrMailboxUnknown`) — and the UPN
    path, the `mail eq` fallback, a `$` address, the switch off (`ErrMailboxUnavailable`,
    no Graph call), a throttle passing through; `Send` — the MIME byte for byte in
    `g.Sent()`, a mailbox `Check` never resolved (`ErrMailboxUnknown`, no call), over
    `MaxMessage` (`ErrMailTooLarge`, no call), a 413, a 403, a gone mailbox, a 5xx **not
    retried** (one call), Bcc through the draft (create with the header, `bccRecipients` checked, send; the draft
    deleted when send fails or a Bcc address is missing), the switch off; `Forget` idempotent; `Status`'s three answers and
    `tenant_wide`; `Key` `entra`, `DisplayName` `Microsoft 365`, `MaxMessage` 3 000 000.
  - [ ] `queries/channel_mailboxes.sql` (`UpsertChannelMailbox`, `GetChannelMailbox`,
    `DeleteChannelMailbox`); `graph.go` `postRaw`; `channel_mailbox.go`;
    `Module().ChannelMailbox`.
  - [ ] `go vet`, `mise run server:test` (entra).
  - [ ] Commit:
    `feat(entra): the channel mailboxes — Check resolves an address by its UPN or mail, reads userPurpose and the Inbox with the application token and records the object id; Send posts our MIME to sendMail once, never retried by the client, and a message with Bcc recipients through a draft; refusals mapped to the contract's errors, ErrMailTooLarge above 3 000 000 bytes of MIME; graphtest's mail routes, scoped by MailboxDenied`.

- [ ] **5. Communications: channels on Microsoft 365, after 2 (may run beside 3–4 against
  the fake).** Readings 2, 7, 13, 15.
  - [ ] `openapi/communications.yaml` first: `CreateChannelRequest` and
    `UpdateChannelRequest` without `mailgun`, `MailgunChannelCredentialRequest` gone;
    `ChannelResponse.settings` without `domain`/`region`, `mailbox`; `POST
    /channels/{id}/provider` (`SwitchChannelProviderRequest {provider}`, 200, 400, 404,
    409, 422, 503); `POST /channels` gains 422 and 503; `POST /channels/{id}/verify`
    unchanged in shape; `GET /channel-providers` (`ChannelProviderResponse[]`);
    `ConversationDetailResponse.maxMessageBytes`; the staging operation's 413 described.
    `go generate ./...`.
  - [ ] Failing tests first (`channels_test.go`, `channels_provider_test.go` new,
    `attachments_test.go`, against `modtest.WithChannelMailbox`): create of a shared
    mailbox (active, provider `entra`, settings, no credential row, address from `Check`,
    the first channel default), `personal_mailbox`, `mailbox_denied`, `mailbox_unknown`,
    `mail_channels_off` (status off; no slot), `provider_unreachable`, `channel_exists` for
    a second channel on the same mailbox, an `smtp` block with `entra` (400); the switch
    (id, conversations and default kept, the credential row **gone in the same
    transaction** — a failure injected after the update leaves both as they were), already
    `entra` (422), the codes; back to SMTP through PUT (credential written, settings
    cleared, `Forget` called after commit, a failing `Forget` logged and the 200 kept);
    PUT `isActive` alone on an `entra` channel (200, no credential needed); verify of an
    `entra` channel (ok, each code, `mailbox_changed`); `GET /channel-providers` with and
    without a slot, `tenant_wide` as `warning`; no slot call inside a transaction (the
    contract-call hook); the rate limit. `channels_concurrency_test.go` gains a switch
    racing a PUT of the default. Staging over the channel's budget (413) and under it;
    reply over it (422 `too_large`, nothing written) — driven through a test-only inbound
    participant, as `conversations_reply_test.go` already seeds one.
  - [ ] `queries/channels.sql`: `InsertChannel` with `settings`, `SwitchChannelProvider`,
    `SetChannelSmtp` (provider and settings back), `DeleteChannelCredential`, the reads
    selecting `c.settings`; `sqlc generate -f internal/communications/sqlc.yaml`;
    `contractscalls.go` (reading 7); `channels.go` and `channels_validation.go` (reading
    13; the header's Mailgun note rewritten); `channel_providers.go`; the budget in
    `attachments.go`, `conversations_reply.go`, `conversations.go` (the detail's field).
  - [ ] `go vet`, `mise run server:test` (communications).
  - [ ] Commit:
    `feat(communications): Microsoft 365 channels — created by a shared mailbox's address through ChannelMailbox.Check, settings in channels.settings and no credential row; an SMTP channel switched in one transaction keeping its id, conversations and default, and back through its SMTP settings; verify runs Check again; personal_mailbox, mailbox_denied, mailbox_unknown and mail_channels_off; GET /channel-providers; Mailgun out of the contract; the channel's size budget refused at staging and at reply`.

- [ ] **6. Communications: the outbox sends through the mailbox, after 4's contract and
  5.** Readings 4, 5, 14.
  - [ ] `mail.RenderMIME` first, with tests in `outbound_test.go`: the bytes parse
    (`net/mail`), `Message-Id: <…@vantigo.invalid>`, `In-Reply-To`, `References` space-
    joined, To/Cc present, **no Bcc header** unless `WithBcc()` (then exactly one, the addresses comma-joined), an inline part's bracketed Content-ID, the
    same parts as SMTP's message for the same `Outbound` (`RenderOutboundForTests`).
    `contributing/platform-packages.md` is task 9's.
  - [ ] Failing tests first (`outbox_mailbox_test.go` new, against the fake): an `entra`
    channel's job sent once through `Send` with the MIME's Message-Id, In-Reply-To and
    References **byte for byte** (a `channel_metadata_json` written by the test), the Bcc
    list beside it, the deliveries `relay_accepted`; the stamp written before `Send`; each
    terminal error → `failed`, `submission_failed`, `last_error` the constant, the code in
    the event's data, `jobsFailed` +1, after **one** attempt; `ErrMailboxUnavailable` and
    an unknown error → `retry` on the backoff; a channel whose provider is not the slot's
    `Key()` and a missing slot → retried, never sent; `settings` without a mailbox →
    terminal `channel_misconfigured`; SMTP jobs unchanged (the existing `outbox_test.go`
    green); no `Send` inside a transaction.
  - [ ] `queries/outbox.sql` (`GetOutboundMessageForSend`); `sqlc generate`; `outbox.go`
    (the branch, `markFailed`'s `terminal`, the event data); `metrics.go` unchanged.
  - [ ] `go vet`, `mise run server:test` (communications, mail).
  - [ ] Commit:
    `feat(communications, mail): the outbox sends Microsoft 365 channels' mail through ChannelMailbox.Send — the same Outbound rendered as MIME with our Message-Id, In-Reply-To and References, Bcc beside it; the mailbox's denial, its absence and a message too large fail the job at once with the code in the event, an unavailable provider is retried; mail.RenderMIME`.

- [ ] **7. The Entra frontend, after 3's contract.** Reading 16.
  - [ ] Failing tests first (`pages/entra.test.tsx`, both languages): the switch saved; each
    status's colour and words; the commands with the client id and the object id filled,
    the placeholder before a test; **Copy**; the `tenant_wide` sentence.
  - [ ] `settings-form.tsx`, `i18n.ts` en/nb, `test/server.ts`; `bun run gen:client`;
    `bun run translations:check`; `mise run frontend:check`.
  - [ ] Commit:
    `feat(entra-ui): the Mail channels switch — its status, the RBAC for Applications commands with the client and the service principal filled in to copy, and the tenant-wide warning; both languages`.

- [ ] **8. The Communications frontend, after 5's contract.** Reading 17.
  - [ ] Failing tests first (`pages/admin.channels.test.tsx`, `pages/inbox.test.tsx`, both
    languages): Microsoft 365 offered only while available; its form; each server code's
    message; the switch with its confirm; the provider badge; the `tenant_wide` alert; the
    composer's limit and the 413/422 messages; Mailgun gone.
  - [ ] `ChannelForm.tsx`, `admin.channels.tsx`, `ConversationComposer.tsx`,
    `api/channels.ts`, `catalog.ts` en/nb; `bun run gen:client`; `bun run translations:check`;
    `mise run frontend:check`.
  - [ ] Commit:
    `feat(communications-ui): Microsoft 365 channels — the provider offered while the mail channels are available, a shared mailbox added by its address, an SMTP channel switched with a confirm, the tenant-wide warning, the composer's size limit on the channel; Mailgun gone; both languages`.

- [ ] **9. Documentation, after 3–8.** English first, then Norwegian bokmål page for page.
  - [ ] `reference/communications.md` (en): `sources` gains
    `apps/server/internal/db/migrations/00091_communications_channel_settings.sql`; the
    description no longer "outbound … across shared mailboxes" alone; *SMTP* (`:106`)
    becomes *Providers* with *SMTP* and **Microsoft 365** — the provider value `entra`, the
    slot, `channels.settings`, no credential row, one channel per mailbox, create and its
    codes, the switch of provider and back, verify, `GET /channel-providers`, the rate
    limit, SMTP AUTH's end in Exchange Online (reading 20); *Delivery semantics* (`:131`)
    — the branch after the stamp, `sendMail` with our MIME, 202 as acceptance, Bcc through
    a draft, the terminal codes and the constant `last_error`, Message-Id kept or not
    (the checklist's finding); *Attachments* (`:212`) — the channel's budget at staging and
    at reply, 3 000 000 bytes, `maxMessageBytes`; *The outbound-only consequence*
    (`:242`) — still outbound-only until D, Sent Items holds the copy.
  - [ ] `reference/entra.md` (en): `sources` gains `00090_entra_mail_channels.sql`;
    *The schema* (`:66`) — `mail_channels`, `entra.channel_mailboxes`; *The permissions
    the module does not use* (`:359`) — the mail roles while the switch is on;
    a new section *Mail channels* after *The mailbox calendar* (`:1604`) — the switch and
    `mailChannels`' statuses, `tenant_wide` and what the test cannot see
    (`full_access_as_app`, RBAC grants), `servicePrincipalId`, `ChannelMailbox`, `Check`'s
    three reads and their permissions, `Send`'s two paths, `MaxMessage`, the refusals,
    `Forget`; *The Graph client* (`:446`) — `postRaw`, `noRetry` on `sendMail`;
    *Testing* (`:2109`) — graphtest's mail routes; *Endpoints* (`:2155`) via the
    regenerated reference.
  - [ ] `contributing/module-boundaries.md` (en): rule 15 and the slot list (reading 18);
    the contract-call guard now in Communications too.
  - [ ] `contributing/platform-packages.md` (en): `mail.RenderMIME`, and why Bcc is not a
    header.
  - [ ] `admin/entra.md` (en, nb): a new section **Mail channels** after *Calendar sync*
    (`:334`) — preconditions (the connection connected; Exchange Administrator or
    Organization Management; Exchange Online PowerShell); the steps in order: turn on
    **Mail channels**, `New-ServicePrincipal` with the page's ids (Enterprise applications,
    not App registrations), `Set-Mailbox <shared> -CustomAttribute10 "Vantigo"` per
    mailbox, `New-ManagementScope`, the two `New-ManagementRoleAssignment`
    (`Application Mail Full Access`, `Application MailboxSettings.Read`), remove any
    tenant-wide `Mail.*` consent in the portal; verify with
    `Test-ServicePrincipalAuthorization -Resource` (`InScope` true for both), then add a
    channel in Communications; what can go wrong — the 30-minute to 2-hour cache,
    `mailbox_denied` (outside the scope, or a role missing), `personal_mailbox`,
    `tenant_wide`, a union with Entra's grants, exclusive scopes not restricting,
    `CustomAttribute10` already used by something else (choose another attribute and
    filter); turning it off — sends retried then failed, nothing deleted. *Step 3*
    (`:104-148`): the mail roles shown as tenant-wide rather than unexpected while the
    switch is on.
  - [ ] `user/entra.md` (en, nb): *4 Set the account rules* (`:147`) — the **Mail channels**
    switch, its statuses, the commands to copy; *The Status card* (`:44`) — the mail roles
    while it is on.
  - [ ] `user/communications.md` (en, nb): *Set up a channel* (`:141`) — the provider is
    **SMTP** or **Microsoft 365**; adding a Microsoft 365 channel (a shared mailbox's
    address and a display name; only shared mailboxes; what each refusal means and who
    fixes it); **Switch to Microsoft 365** on an SMTP channel and what it deletes; going back
    by entering SMTP settings; **Verify** for Microsoft 365; the Mailgun sentence gone;
    *Reply to a customer* (`:85`) — the size limit beside the attachments on a Microsoft
    365 channel; *After you send* (`:128`) — the copy in the shared mailbox's Sent Items.
  - [ ] The spec's *as built* note for C under *Deliveries* (after B's), listing this plan's
    departures: only C's methods on the contract; `Send`'s `bcc` and the draft path;
    `ChannelResponse.mailbox` instead of a `oneOf`; `Forget` on the way back to SMTP only
    (no channel delete exists); `channel-providers`; `maxMessageBytes`; the unique index;
    no `accountEnabled` fallback (the role exists); `full_access_as_app` invisible to the
    test; the size check at staging; `provider_unreachable` and `mailbox_changed`; the
    terminal code in the event, `last_error` constant; the Message-Id and Bcc findings
    from the real tenant.
  - [ ] `ROADMAP.md` (Phase 3, `:2425-2477`): **C — Microsoft 365 channels that send,
    built**, its links; *Next:* D and E.
  - [ ] `mise run docs:check` (after `bun install --frozen-lockfile` in a fresh worktree).
  - [ ] Commit:
    `docs(communications, entra): Microsoft 365 channels — providers, the switch of provider, sending through sendMail and the size budget in the communications reference; the Mail channels switch, tenant_wide and ChannelMailbox in Entra's; rule 15; mail.RenderMIME; the Entra administration page's Mail channels procedure with RBAC for Applications, the Entra and Communications guides, both languages; the spec's as-built note; the roadmap`.

- [ ] **10. Review, then verification and the pull request.** A review (read-only) of:
  no `ChannelMailbox` call inside a transaction (the hook's record, `grep -n
  'ChannelMailbox\.' internal/communications` only in `contractscalls.go`); `Send` only to
  a `channel_mailboxes` row; the switch's single transaction; the terminal/retried split;
  no SMTP behaviour changed; the MIME unchanged for SMTP; a **security review** of
  `Check` (an address typed by an admin reaching Graph: escaping, the `$filter` quote, no
  path injection through `url.PathEscape`), the `tenant_wide` warning's honesty, and the
  frontend never reading Entra's API from Communications. A docs pass against the code.
  One fix batch (`fix(communications, entra): the review's findings — …`,
  `docs(communications, entra): the review's fixes — …`). Then `mise run server:check`,
  `mise run server:test`, `mise run frontend:check`, `mise run docs:check`. Rebase onto
  `main`; the gates again; push (signed commits, memory: *Commits must be signed*); the pull
  request against `main` with the **real-tenant checklist**: the commands run as the page
  shows them; `Test-ServicePrincipalAuthorization -Resource post@…` `InScope` true for both
  roles; a tenant-wide `Mail.Send` showing `tenant_wide` and removed; a shared mailbox added,
  a personal one refused, one outside the scope `mailbox_denied`; an SMTP channel switched;
  a reply sent and its **Message-Id, In-Reply-To and References** read on the recipient's
  copy and on the Sent Items item's `internetMessageId`; a **Bcc** recipient receiving the
  mail and not visible to the others; a message just under and just over 3 MB of MIME.
  Report the two unverified facts' outcome in the PR and the spec's note.

## Gotchas for every task

- The shared test database is the container `vantigo-test-postgres-1`; if Docker's disk
  fills, `docker builder prune -f` and restart it — never touch volumes or images.
- sqlc 1.31.1 cannot parse `@now::timestamptz - interval '…'`: write
  `col + interval '…' < @now`.
- Agents running at once in the worktree `git add` **only their own files**; the pre-commit
  hook runs tree-wide.
- A fresh worktree has no `node_modules`: `bun install --frozen-lockfile` before
  `docs:check` or the frontend tests. Node 26's localStorage and "due today" flakes in
  other packages are not this delivery's.
- Every commit is signed; a force-push to the PR branch is the owner's to run.
- A commit whose docs land in task 9 carries `Docs-Impact: none — the pages land in task
  9's commit of this delivery`; strip those trailers when the docs commit lands in the
  same PR (memory: *Entra phase 2 state*).

## Open questions for the owner

1. **Bcc on a Microsoft 365 channel.** go-mail writes no `Bcc:` header and Graph's MIME
   `sendMail` is reported to ignore one. The plan sends a message with Bcc recipients as a
   MIME **draft** in the shared mailbox (whose `Bcc:` header Graph documents), checks its
   `bccRecipients`, then sends it (two calls; a crash between them can leave a draft,
   deleted best-effort). The alternative is to
   refuse Bcc on Microsoft 365 channels (422 `bcc_unsupported`). Which?
2. **The terminal code.** M13 says terminal "with the code"; the outbox's `last_error` is a
   deliberate constant (`outbox.go:79-86`). The plan keeps it constant and puts the code in
   the event's `data_json`. Agree?
3. **Switching back.** M12 says "no way back short of entering SMTP settings again"; the
   plan makes that a PUT with `provider: smtp` and a full SMTP block (password required).
   Agree, or no way back at all in C?
