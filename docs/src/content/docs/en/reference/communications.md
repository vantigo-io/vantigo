---
title: "Communications module"
description: "Outbound business email from shared mailboxes: the channels' providers — SMTP, and Microsoft 365 shared mailboxes through the Microsoft Entra ID module, created by address or switched from SMTP — the outbox and its delivery through either, the size a Microsoft 365 channel takes, retention and the operator's configuration."
sources:
  - apps/server/internal/communications
  - apps/communications/frontend
  - openapi/communications.yaml
  - apps/server/internal/openapi/specs/communications.yaml
  - apps/server/internal/db/migrations/00091_communications_channel_settings.sql
---
Communications is a module inside the single Vantigo binary, not a separately
deployed service. It runs in the same process as Identity, Customers, Products and
Energy, and owns the `communications` schema in the shared PostgreSQL database. The
production artifact is the single `ghcr.io/vantigo-io/vantigo` image.

## Running

The image is one binary with a dispatch table; the command is the first argument.
The commands that matter here are:

```bash
docker run --rm  ghcr.io/vantigo-io/vantigo migrate   # apply migrations and exit
docker run -d    ghcr.io/vantigo-io/vantigo api       # migrate, then serve (+ workers)
docker run -d    ghcr.io/vantigo-io/vantigo worker    # background workers only
```

`migrate` applies all migrations and exits. **`api` applies pending migrations under
the advisory lock and then serves**, running every enabled module's background
workers in-process when `WORKERS_IN_PROCESS=1` (the default). `server` serves only
and never migrates or runs workers — it is what a fleet of stateless replicas runs.
`seed` is development-only and currently does nothing.

There is no `reset-communications` command. The communications schema has a clean
initial migration lineage, there is no production communications data to preserve,
and migrations are forward-only.

## Enabling the module

`MODULES` is a **positive allowlist**, not a per-module boolean: a comma-separated
list of the business modules this deployment serves, parsed once at startup.

```text
MODULES=customers,products,energy,communications
```

That is also the default when `MODULES` is unset. Identity is always mounted and is
never listed. A name the binary does not know fails startup, naming the name and the
known set.

**Communications requires Customers.** Enabling it without `customers` fails startup
naming both. A disabled module contributes no route, no permission and no contract
path, and its paths answer the `/api` catch-all 404 — but every schema is migrated
regardless of what `MODULES` enables, so enabling the module later needs no
migration.

## In-process customer integration

Communications reads customer data through the in-process
`contracts.CustomerDirectory` interface (`apps/server/internal/contracts`). There is
no Customers service URL, no service-to-service API key and no `X-Vantigo-Api-Key`
variable. The customer directory is the one sanctioned cross-module read: modules
never import each other and never query another module's schema.

Customer and contact link values are opaque domain values. Preserve them exactly; do
not derive meaning from them or use them as authorization credentials.

The one write in the other direction is a customer merge: communications declares a
`contracts.CustomerReferenceHolder` ([module boundaries rule 8](/en/contributing/module-boundaries/#the-rules)),
and when the customers module merges two customers it re-points, inside the merge's
own transaction, every conversation's `customer_id` and `suggested_customer_id` from
the absorbed customer to the survivor, and the candidate list — where a conversation
that already lists the survivor keeps it once. See
[Merging duplicates](/en/reference/customers/#merging-duplicates).

It also hands over and takes out what it holds about a private person —
`contracts.CustomerPersonalData` ([module boundaries rule 9](/en/contributing/module-boundaries/#the-rules)).
A person's export carries every conversation about them, with each message's
direction, date and body — the text body and the HTML body, each when the message has
it, so an HTML-only message is not left empty — and each attachment's name
(`modules.communications`). It leaves out the addressing metadata — each participant's
address and display name, each delivery's recipient address, a message's channel
headers — although the erase below deletes all of it with the conversations. The
suppressions, the opt-out addresses, are neither exported nor erased: honouring an
opt-out needs the address, so it is kept on purpose.
Their anonymisation deletes those conversations inside the customers module's
transaction, through the retention worker's own deletes: every message and the rows
under it in the order `message_events`' RESTRICT allows, each attachment, raw payload
and staged upload's object queued on the cleanup ledger before the rows naming it go —
the cleanup worker deletes the objects after the transaction commits — and a message
still waiting in the outbox with its job. A conversation that only suggests or lists
the person keeps its own customer and loses the suggestion, its reasoning and the
candidate row. The anonymisation reports `communications.objects` as the number of
object keys it queued — attachments, raw payloads and staged uploads, a key already on
the ledger counted too — not as a number of attachment rows. See
[Personal data and anonymisation](/en/reference/customers/#personal-data-and-anonymisation).

What the erase does not cover, on the record: this module takes no customer lock, so a
message written into one of the person's conversations while the erase runs goes with
the conversation by cascade rather than through the ledger — or trips `message_events`'
RESTRICT, which rolls the customer back and the worker retries next cycle; nothing stops
a conversation being linked to the archived "Anonymised person" afterwards;
`communications.suppressions` can still hold the person's address; a conversation not
linked to the customer (no customer, or another one) keeps the person's participant
address, and the export does not list the addresses the person wrote from or was
written to; and a customer's
`customer.peppol_lookup` entries keep their `smpHost`, derived from the participant id.

## Providers

A channel is an address replies are sent from, and its `provider` says how they leave:
`smtp`, through the channel's own SMTP server, or `entra`, **Microsoft 365** — an
Exchange Online shared mailbox, reached through Microsoft Graph by the [Microsoft Entra
ID module](/en/reference/entra/#mail-channels). There is no other. Mailgun, which this
port never created, has left the contract; a create or update naming any other provider
is a 400 under `smtp` (*SMTP credentials require the smtp provider.*).

**Communications is outbound-only.** There is no inbound mail path of any kind: no
webhook, no IMAP, no inbound synchronisation, and therefore nothing that writes an
inbound message — a Microsoft 365 channel included, for now. See "The outbound-only
consequence" below for what that means for replies, reply-all and the AI draft.

`GET /api/v1/communications/channel-providers` (`channels-manage`) is what a channel can
use, so the channels page reads Communications' own API and never Entra's:

- `{key: "smtp", displayName: "SMTP", available: true}`, always;
- `{key: "entra", displayName, available, reason?, warning?, maxMessageBytes}`, only
  while an enabled module fills the `ChannelMailbox` slot. `displayName` is the
  provider's own, *Microsoft 365*; `available` is its status. Not available, `reason`
  is the provider's — `switch_off`, the connection's reason — or `status_unavailable`
  when the status did not answer. Available, `warning` is `tenant_wide` while the
  application's mail grant reaches every mailbox in the tenant. `maxMessageBytes` is the
  provider's `MaxMessage` ([attachments](#attachments)).

The page offers **Microsoft 365** in the provider select, and **Switch to Microsoft 365**
on an SMTP channel's row, only while the second entry is available, and shows the
`tenant_wide` warning above the table while it says so.

### SMTP

**Per-channel SMTP credentials are configured through the Communications API, not
through the environment.** An SMTP channel's host, port, TLS flag and username are its
`channel_credentials` row's settings, and `channels.settings` stays `{}`. Its password is sealed with AES-256-GCM under a
dedicated purpose by `internal/secrets`, whose key is derived from `APP_SECRET` with
HKDF-SHA256. It is never placed in frontend code, browser storage, URLs, logs or
deployment environment files, and it is never returned by the API.

Two consequences follow from the key derivation: every replica must share the same
`APP_SECRET`, and changing `APP_SECRET` makes every stored channel credential
unrecoverable — they must be re-entered.

The `SMTP_*` environment variables are a **different thing**: they configure
identity's own application mail (invitations and password resets), not the mailboxes
conversations are sent through. See
[identity and authentication](/en/admin/authentication/).

**SMTP AUTH in Exchange Online.** An SMTP channel whose server is Exchange Online signs
in with SMTP AUTH and a password. Microsoft withdrew the removal it had planned for
spring 2026; according to Message Center MC786329, read through secondary sources and
not first-hand, basic SMTP AUTH stays on until the end of December 2026, is then off by
default for existing tenants — an Exchange administrator may turn it back on — and
unavailable for new ones, with a final date in the second half of 2027. Such a channel
needs that exception from January 2027, or a [switch to Microsoft
365](#switching-provider), which keeps its id, its conversations and its default flag.

### Microsoft 365

A Microsoft 365 channel (Outlook connection and Microsoft 365 mail design M12) is an
Exchange Online **shared mailbox**, sent from with the application's own token through
`contracts.ChannelMailbox`, a single-provider slot the Microsoft Entra ID module fills
under the key `entra` ([module boundaries](/en/contributing/module-boundaries/) rule
15). Communications is the authority on which mailboxes are channels; the provider sends
only from a mailbox its `Check` resolved. The Owner turns it on and scopes it in the
Entra module ([Mail channels](/en/admin/entra/#mail-channels)).

- **The row.** `provider = 'entra'`; `channels.settings` (migration
  `00091_communications_channel_settings.sql`, `jsonb`, `{}` by default) is
  `{"mailbox": "<object id>"}`, the mailbox's object id in lower case; and there is
  **no `channel_credentials` row** — no secret to seal. **One channel per mailbox**:
  `ux_channels_entra_mailbox` on `settings->>'mailbox'` `WHERE provider = 'entra'`, so two
  aliases of one mailbox cannot make two channels; its violation is 409 `channel_exists`
  (*A channel for this mailbox already exists.*).
- **The answer.** `ChannelResponse.mailbox` is `{id}` for a Microsoft 365 channel and
  null otherwise; `settings`, the SMTP shape (`host`, `port`, `useSsl`, `username`), is
  null for it, and `hasCredentials` false.
- **Every call of the slot is made outside a transaction** — before it opens or after it
  commits — through the accessors in `contractscalls.go`, which refuse one made inside
  (a programming error, answered as a 500).

#### Creating one

`POST /channels` with `{type: "email", provider: "entra", address, displayName?,
isDefault?}` and no `smtp` block (one is a 400 under `smtp`). Before the transaction, the
provider's `Status`, then `Check(address)`:

| What the provider answers | Result |
| --- | --- |
| no provider in the slot, or its `Status` not available | 409 `mail_channels_off` |
| a shared mailbox it reaches | 201, the channel **active**; its address is the mailbox's primary SMTP address as `Check` read it, in lower case, so an alias typed becomes the mailbox's own |
| a mailbox it reaches, not shared | 422 `personal_mailbox` — shared mailboxes only |
| `ErrMailboxDenied` | 422 `mailbox_denied` — outside the application's RBAC scope, or a role missing |
| `ErrMailboxUnknown`, `ErrMailboxGone` | 422 `mailbox_unknown` — no such mailbox, or an address that is not a bare address (a display name, white space, one of `/ \ ? # %`, over 320 bytes) |
| `ErrMailboxUnavailable` | 409 `mail_channels_off` |
| anything else — throttled, Graph not answering, the status unreadable | 503 `provider_unreachable` (*Microsoft 365 did not answer. Try again.*), logged |

The insert then runs as an SMTP channel's does — the default slot's lock, the first
channel made the default — with `settings` and no credential row. An address another
channel holds is 409 `channel_exists` as for SMTP.

#### Switching provider

`POST /channels/{id}/provider` with `{provider: "entra"}` (`channels-manage`) moves an
SMTP channel to Microsoft 365 and keeps its **id, address, conversations and default
flag**. Any other provider in the body is a 400; an unknown channel a bare 404; a
channel already on `entra` 422 `provider_unchanged`. `Status` and `Check` run on the
channel's own address before the transaction, with the create's table. Then, under the
default slot's lock and a re-read, the provider and `settings` are written and the
**credential row deleted, in one transaction**: a failure leaves the channel SMTP with its
credential. Answers 200 with the channel. The mailbox `Check` recorded on Entra's side
stays recorded if that transaction fails; it only lets the provider send from a mailbox
an admin checked, and the next `Check` rewrites it.

**Back to SMTP** is `PUT /channels/{id}` with `provider: "smtp"` and a full `smtp` block,
**the password included** — there is no stored one to reuse (*A password is required to
switch a channel to SMTP.*; without a block, *SMTP credentials require a host and valid
port.*). It writes the credential row, sets the provider to `smtp` and `settings` to
`{}`, and **after the commit** calls the provider's `Forget` for the old mailbox,
best-effort: a failure is logged and the 200 stands. The channels page has no control for
it; it is the API's. A `PUT` without `provider` keeps the channel's own, so `isActive`,
`isDefault` or `displayName` alone on a Microsoft 365 channel need no credential; a `PUT`
naming `entra` on an SMTP channel is a 400 under `provider` (the switch is the `POST`).
A `PUT` whose channel changed provider between its read and its transaction is a 400
under `provider`, to read the channel again.

**There is no delete of a channel** — conversations and participants restrict it — so a
deactivated Microsoft 365 channel keeps its mailbox, and sends again once activated
with no new `Check`.

#### Verifying

`POST /channels/{id}/verify` on a Microsoft 365 channel runs `Status` and `Check` of its
address again: 200 `{ok: true}` only when it still reaches a shared mailbox **with the
object id the channel holds**; otherwise 422 with the create's codes —
`mail_channels_off`, `personal_mailbox`, `mailbox_denied`, `mailbox_unknown`,
`provider_unreachable` — or `mailbox_changed` when the address now names another mailbox.

#### The rate limit

Creating, switching and verifying a channel each reach Graph up to three times for a
Microsoft 365 one, so the three share one bucket, `communications-channel-check`, **30
a minute per client address** — SMTP channels included. A refused request is the
router's 429.

### Delivery semantics

Outbound email is **at-least-once**. The outbox worker claims a job with a lease,
performs the external send, and only then commits completion. If the process dies
between provider acceptance and the completion commit, the lease expires and another
worker resends the message — a duplicate email is possible after a crash or
deployment in exactly that window, and that trade-off is deliberate: the alternative
(at-most-once) silently loses mail.

Three mechanisms keep this honest:

- Every send uses a **deterministic `Message-Id`** derived from the message's id, so
  a resent message carries the same identity and receiving mail systems can collapse
  duplicates.
- Immediately before the external call, the job is stamped with
  `delivery_attempted_at` in its own committed write. A job re-claimed with that
  stamp set may already have been delivered; the resend still happens, but it is
  logged as a possible duplicate and counted on the
  `communications.outbox.possible_duplicate_sends` metric (meter
  `Vantigo.Communications`) — alert on it rather than discovering duplicates from
  customer reports.
- There is deliberately **no retry around the send call itself**: an ambiguous
  timeout may already have delivered, and the outbox owns retries.

**The send's two paths.** After the stamp, the worker builds the message — a
`mail.Outbound` with the deterministic `Message-Id` (`<hex of the id>@vantigo.invalid`),
`In-Reply-To` and `References` from the message's channel metadata, To, Cc and Bcc from
the deliveries, and the attachments — and branches on the channel's provider, which
`GetOutboundMessageForSend` now reads with its `settings`:

- **`smtp`**: as before, over the channel's credential row.
- **anything else**: through the `ChannelMailbox` slot. A channel whose `settings` name
  no mailbox fails with `channel_misconfigured`; no provider in the slot, or one whose
  `Key` is not the channel's provider, is `ErrMailboxUnavailable` and retried — a channel
  is **never sent through another provider**. The same `Outbound` is rendered as MIME
  by [`mail.RenderMIME`](/en/contributing/platform-packages/#the-mail-package) from the
  channel's address — with a `Bcc:` header only when there are blind copies — and handed
  to `Send` with the blind copies beside it, under the SMTP send's own 30-second budget.
  Entra posts it to Graph's `sendMail` as base64 MIME, or, with blind copies, creates it
  as a MIME draft in the shared mailbox, checks the draft kept every blind copy, and sends
  the draft ([Entra's Send](/en/reference/entra/#send)). Neither is retried by Entra's
  client on a 5xx. Graph's **202 is acceptance**, as SMTP's 250: the deliveries become
  `relay_accepted`, and the at-least-once rule, the stamp and the possible-duplicate
  metric are unchanged. Exchange saves the sent message in the shared mailbox's **Sent
  Items**.

**Not yet verified on a real tenant**: whether Exchange keeps our `Message-Id` on a MIME
`sendMail` (no Microsoft page says so; Outlook is reported to stamp its own), and whether
a draft's `Bcc:` header reaches the blind copies as Graph documents for a MIME draft. The
pull request's real-tenant checklist reads the sent item's `internetMessageId`, the
recipient's raw headers and a blind copy's arrival. If Exchange rewrites the
`Message-Id`, a customer's answer still names Exchange's own in `In-Reply-To`, and
threading has to fall back on Graph's `conversationId` once inbound mail exists.

**Terminal failures.** A send through the mailbox that fails with one of five codes
**fails the job at once**, after one attempt, rather than at the eighth: no retry can
mend it.

| Code | From |
| --- | --- |
| `mailbox_denied` | `ErrMailboxDenied`: the mailbox is outside the application's scope, or a role is missing |
| `mailbox_gone` | `ErrMailboxGone`: the mailbox no longer exists |
| `mailbox_unknown` | `ErrMailboxUnknown`: no `Check` recorded the mailbox on Entra's side (forgotten, say) |
| `too_large` | `ErrMailTooLarge`: over `MaxMessage`, or Graph's 413 |
| `channel_misconfigured` | the channel's `settings` name no mailbox |

The job becomes `failed` and its deliveries `submission_failed`, `jobs_failed` counts
it, and `last_error` stays the constant *Outbound delivery failed.* — the module's one
redaction of an outbound error, so nothing Graph said is stored. The code goes beside it
in the failure event's `data_json` (`{"error":"Outbound delivery failed.","code":"mailbox_denied"}`)
and in the log line. Everything else — `ErrMailboxUnavailable` (the Mail channels switch
off, the connection failing), throttling, a 5xx, a draft that lost a blind copy — is
retried on the backoff below, like an SMTP failure.

A failed send is rescheduled with an exponential backoff of
`min(3600, 2^min(attempts, 10))` seconds, and `attempts` is incremented when the job
is claimed rather than when it fails. Two consequences of that expression are worth
stating because the arithmetic is easy to read wrongly:

- **The 3600 s cap is unreachable.** The exponent is clamped at 10, so the term never
  exceeds 1024 s and the `min` never binds.
- **With the default `max_attempts = 8`, the largest backoff a live job ever waits is
  128 s.** The sequence is 2, 4, 8, 16, 32, 64, 128 s, and the eighth attempt is
  terminal — so 256, 512 and 1024 s are never scheduled either.

The advisory lease guards the **retention worker only**. Outbox delivery and
attachment cleanup take no advisory lock: each claims work with a conditional update
whose `WHERE` re-asserts the predicate its candidate query used, so a claim another
worker already won matches nothing. Every replica runs those two workers every cycle,
by design. Adding an advisory lock to either would be a defect, not a hardening.

### Background workers

The module contributes the only three background workers in the application: outbox
delivery, retention, and attachment cleanup. `worker` mode always runs them, `server`
mode never does, and `api` mode runs them when `WORKERS_IN_PROCESS=1` (the default).
A deployment that scales the API horizontally should set `WORKERS_IN_PROCESS=0` on
the API replicas and run one dedicated `worker` container, so extra HTTP replicas do
not multiply the pollers.

### Metrics

Four counters on an OpenTelemetry meter named `Vantigo.Communications`, all of them
the outbox's: `communications.outbox.possible_duplicate_sends`, `.jobs_completed`,
`.jobs_retried` and `.jobs_failed`. **Alert on the last one: every increment is
undelivered customer email.** There are deliberately no others — no retention,
cleanup, storage, SMTP or AI metrics, and no histograms.

## Retention, cleanup and staged objects

Terminal jobs, their receipts and their attachments follow the configured retention
period; failed terminal jobs without messages are included, while non-terminal work
is retained. Retention queues each object key once, through a durable cleanup record,
before deleting the metadata that identifies its owner — it never calls the object
store itself. The attachment cleanup worker drains those records and retries object
deletion. Staged object reservations have a bounded expiry, so a process crash after
the object write cannot strand the object.

| Variable | Purpose | Default |
| --- | --- | --- |
| `COMMUNICATIONS_RETENTION_DAYS` | How old terminal history must be before deletion (1–36500) | `365` |
| `COMMUNICATIONS_RETENTION_BATCH_SIZE` | Messages one batch may delete (1–1000) | `100` |
| `COMMUNICATIONS_RETENTION_POLL` | How often the retention worker runs a cycle | `1h` |
| `COMMUNICATIONS_ATTACHMENT_MAX_BYTES` | Per-file staging limit (1 B – 50 MiB) | `10485760` (10 MiB) |
| `COMMUNICATIONS_UPLOAD_EXPIRY` | How long a staged attachment stays valid | `24h` |

A value outside the accepted range fails startup rather than being silently clamped,
so a typo is a boot error instead of a retention window nobody asked for. Use a
conservative twelve-month retention unless policy requires less; access controls and
audit requirements remain in force while data is deleted.

## Attachments

Stage authenticated multipart attachments with
`POST /api/v1/communications/conversations/{id}/attachments` using a `file`, optional
`contentId`/`isInline`, and an `Idempotency-Key`. Include returned upload IDs as
`attachmentIds` in the reply request; `replyMode` is `reply` by default or
`reply_all`. Uploads are sendable only once `scanStatus=clean`; while pending, reply
returns 409 `attachments_not_ready`.

Poll staged upload state with
`GET /api/v1/communications/conversations/{conversationId}/attachments/{attachmentId}`.
The uploader must hold ConversationsView and the upload must belong to the supplied
conversation. Other users, other conversations, expired uploads and unknown IDs
return 404 without storage details.

**A Microsoft 365 channel's size.** Graph takes a message posted whole up to about 4 MB
of request body, far below the per-file staging limit, so a Microsoft 365 channel holds
its mail to the provider's `MaxMessage` — **3 000 000 bytes of MIME** for Entra, 4 000 000
once base64-encoded for Graph — and refuses what is too large **when it is written, not
when it is sent**:

- **Staging** on a conversation whose channel is Microsoft 365: a file whose size × 1.37
  (base64 and a margin) plus 16 KiB (headers and boundaries) exceeds it is **413**
  `attachment_too_large`, *The attachment is larger than this channel can send (about 3
  MB).*
- **Reply**: the estimate of the whole message — every staged attachment × 1.37, the text
  body as it is, the HTML body × 1.37, plus 16 KiB — over it is **422** `too_large`, *The
  message and its attachments are larger than this channel can send.*, before anything is
  written.
- `ConversationDetailResponse.maxMessageBytes` is the channel's budget — null for SMTP —
  so the composer shows *Attachments up to about 3 MB in total on this channel* without
  `channels-manage`.

The provider's `ErrMailTooLarge` at send time stays the last guard, and fails the job
(`too_large`). A larger message would need a draft with upload sessions, which is not
built. An SMTP channel has no budget beyond the staging limit and its server's own.

`GET /api/v1/communications/attachments/{id}/download` requires ConversationsView and
streams only clean attachments through the application. It never redirects to a
provider URL and never exposes a storage key; the `downloadPath` field in
conversation DTOs is this same-origin endpoint. Attachments are stored under the
`communications` storage scope — see [object storage](/en/admin/object-storage/), which is
**filesystem-only** in this release and fails closed at each operation when
`STORAGE_PROVIDER` is unset.

**There is no attachment scanner.** Nothing transitions an upload from `pending` to
`clean`, so new uploads are created `clean` on the reasoning that there is nothing to
scan. The `scanStatus` and `ready` fields, and every gate that reads them, are kept
exactly as they are: the frontend is unchanged and a scanner can be added later
without a contract change. Do not read a `clean` status as evidence that a file was
inspected.

## The outbound-only consequence

Reply-all resolves its recipients from the latest **inbound** message's CC, and the
reply path resolves its recipients from that message's participants. With no inbound
path, no such message exists, so these are permanently in their negative state rather
than occasionally empty:

- A conversation created through `POST /conversations` cannot be replied to: reply
  answers 422 `recipients_missing`.
- `canReplyAll` is always false and `replyAllCc` is always empty.
- The AI draft builds its context from inbound messages, so it drafts against an
  empty context and records `product_data=false`.

Two further consequences are worth knowing when reading the code: attachments can be
staged but never sent, and suppression is enforced only by the outbox worker's own
re-check, because reply's suppression check sits behind the recipient resolution that
always fails first.

The endpoints are complete and conform to the contract, so the frontend keeps working
and an inbound provider is purely additive.

A Microsoft 365 channel is outbound-only too, for now: Vantigo sends from the shared
mailbox and reads nothing from it. What it sent is in the mailbox's **Sent Items**, where
the team can see it in Outlook; a customer's answer arrives in the mailbox's Inbox and
stays there, unseen by Vantigo, until receiving is built. The size check at reply is
therefore wired and tested but reached by no conversation yet; the staging check is
live.

## AI draft and customer suggestion

Both AI operations answer 503 `ai_unavailable` unless all three conditions hold:
`COMMUNICATIONS_AI_ENABLED=1`, `COMMUNICATIONS_AI_PROVIDER` is `openai` (the
default), and `COMMUNICATIONS_AI_API_KEY` is set. `COMMUNICATIONS_AI_MODEL` defaults
to `gpt-4o-mini`. A half-set configuration is the documented unavailable state, not a
startup error: no chat client, network client or provider dependency is constructed
at all in that state, and any other provider name leaves the feature unavailable
rather than refusing to start, so a second provider stays additive.
