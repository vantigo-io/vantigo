---
title: "Communications module"
description: "Business email from shared mailboxes: the channels' providers — SMTP, and Microsoft 365 shared mailboxes through the Microsoft Entra ID module, created by address or switched from SMTP, with an optional import window and Outlook on-close behavior — the outbox and its delivery through either, the size a Microsoft 365 channel takes; receiving into a Microsoft 365 channel — the mailbox sync, change notifications, receipts, parsing, sanitising, threading, the customer linker, reply and reply-all, and Outlook state on close —; retention and the operator's configuration."
sources:
  - apps/server/internal/communications
  - apps/communications/frontend
  - openapi/communications.yaml
  - apps/server/internal/openapi/specs/communications.yaml
  - apps/server/internal/db/migrations/00091_communications_channel_settings.sql
  - apps/server/internal/db/migrations/00092_communications_inbound.sql
  - apps/server/internal/db/migrations/00094_communications_mailbox_marks.sql
  - apps/server/internal/db/migrations/00095_communications_personal_sending.sql
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
headers — although the erase below deletes all of it with the conversations. A received
message's original (its raw payload) is not exported either; the erase deletes it with the
message. The suppressions, the opt-out addresses, are neither exported nor erased: honouring an
opt-out needs the address, so it is kept on purpose.

Mail received into a Microsoft 365 channel ([Receiving](#receiving)) reaches the export and
the erase **only through a conversation's customer**: the [linker](#the-customer-linker)
sets it when the sender's address belongs to exactly one customer, so a matched sender's
mail is covered; a sender who matches no customer, or several, writes into a conversation
with no customer (or only candidates), which neither the export nor the erase of a person
finds until someone links it.
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
written to; the [receipts](#receipts) of the person's received mail keep its Message-Ids
and Graph's ids — no address, no content — until the message was received longer ago than
the retention window (the [sweep](#retention-cleanup-and-staged-objects); a channel's
newest receipt stays longer), so an erased message is never imported again; and a customer's
`customer.peppol_lookup` entries keep their `smpHost`, derived from the participant id.

## Providers

A channel is an address replies are sent from, and its `provider` says how they leave:
`smtp`, through the channel's own SMTP server, or `entra`, **Microsoft 365** — an
Exchange Online shared mailbox, reached through Microsoft Graph by the [Microsoft Entra
ID module](/en/reference/entra/#mail-channels). There is no other. Mailgun, which this
port never created, has left the contract; a create or update naming any other provider
is a 400 under `smtp` (*SMTP credentials require the smtp provider.*).

**Only a Microsoft 365 channel receives.** Its shared mailbox's Inbox is read by the
`communications-mailbox-sync` worker ([Receiving](#receiving)), which writes each message
as an inbound message of a conversation. An SMTP channel only sends: there is no webhook,
no IMAP and no other inbound path, so a conversation of an SMTP channel holds no inbound
message and cannot be replied to ([Reply and reply-all](#reply-and-reply-all)).

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
only from a mailbox its `Check` resolved and recorded — a shared one. Someone with
`entra:manage` turns it on in the Entra module and an Exchange administrator scopes it ([Mail channels](/en/admin/entra/#mail-channels)).

- **The row.** `provider = 'entra'`; `channels.settings` (migration
  `00091_communications_channel_settings.sql`, `jsonb`, `{}` by default) is
  `{"mailbox": "<object id>", "onClose"?: "read"|"move", "folder"?: "<name>"}`, the
  mailbox's object id in lower case and the optional [Outlook state on close](#outlook-state-on-close);
  and there is **no `channel_credentials` row** — no secret to seal. **One channel per mailbox**:
  `ux_channels_entra_mailbox` on `settings->>'mailbox'` `WHERE provider = 'entra'`, so two
  aliases of one mailbox cannot make two channels; its violation is 409 `mailbox_exists`
  (*Another channel already uses this mailbox.*), apart from `channel_exists`, an address
  another channel holds.
- **The answer.** `ChannelResponse.mailbox` is `{id, onClose: "none"|"read"|"move", folder: string|null}`
  for a Microsoft 365 channel and null otherwise; `settings`, the SMTP shape (`host`,
  `port`, `useSsl`, `username`), is null for it, and `hasCredentials` false.
- **Every call of the slot is made outside a transaction** — before it opens or after it
  commits — through the accessors in `contractscalls.go`, which refuse one made inside
  (a programming error, answered as a 500).
- **The import window.** Creating a Microsoft 365 channel, and switching an SMTP one,
  writes its `communications.channel_sync` row in the same transaction (migration
  `00092_communications_inbound.sql`): `state = 'pending'`, `next_sync_at` the moment of
  the write, and `import_from` set from the request's optional `importWindow` —
  `from_now` (the default, the moment of the write), `last_30_days` (now minus 30 days) or
  `last_90_days` (now minus 90 days). The [mailbox sync](#the-mailbox-sync) reads only
  mail received at or after `import_from`. A channel that
  already existed when `00092` ran got its row from the migration, with `import_from` the
  moment of the upgrade, inactive channels included. The way back to SMTP deletes the row,
  so a later switch starts again from its own window; deactivating a channel keeps it, and the
  worker skips an inactive channel until it is activated again.
- **Its receive state** is `ChannelResponse.sync`, `{state, error, lastSyncedAt}`, null for
  an SMTP channel: `state` is the row's — `pending` until its first sync finishes, then
  `active`, or `failed` after a refusal (below); `error` the last failure's code, cleared
  by a finished sync.

#### Creating one

`POST /channels` with `{type: "email", provider: "entra", address, displayName?,
isDefault?, importWindow?, onClose?, folder?}` and no `smtp` block (one is a 400 under
`smtp`; `importWindow`, `onClose` and `folder` on an `smtp` create are a 400 too).
`importWindow` is `from_now` (default), `last_30_days` or `last_90_days`; `onClose` is
`none` (default), `read` or `move`, and when `onClose` is `move`, `folder` (1 to 120
characters, no `/` or `\`, defaulting to `"Handled in Vantigo"`) is ensured in the shared
mailbox by calling `EnsureFolder` before the transaction. Before the transaction, the
provider's `Status`, then `Check(address)` (and `EnsureFolder` when `onClose` is `move`):

| What the provider answers | Result |
| --- | --- |
| no provider in the slot, or its `Status` not available | 409 `mail_channels_off` |
| a shared mailbox it reaches | 201, the channel **active**; its address is the mailbox's primary SMTP address as `Check` read it, in lower case, so an alias typed becomes the mailbox's own |
| a mailbox it reaches, not shared | 422 `personal_mailbox` — shared mailboxes only; `Check` does not record a personal mailbox, so the provider would refuse to send from it anyway |
| `ErrMailboxDenied` | 422 `mailbox_denied` — outside the application's RBAC scope, or a role missing |
| `ErrMailboxUnknown`, `ErrMailboxGone` | 422 `mailbox_unknown` — no such mailbox, or an address that is not a bare address (a display name, white space, one of `/ \ ? # %`, over 320 bytes) |
| `ErrMailboxUnavailable` | 409 `mail_channels_off` |
| anything else — throttled, Graph not answering, the status unreadable | 503 `provider_unreachable` (*Microsoft 365 did not answer. Try again.*), logged |

`mail_channels_off` says *Microsoft 365 mail channels are not available. An Entra
administrator turns on Mail channels in the Entra settings.* The address a person typed is
never in an error or a log line: Entra names the lookup's path `/v1.0/users/{address}`.

The insert then runs as an SMTP channel's does — the default slot's lock, the first
channel made the default — with `settings` and no credential row. An address another
channel holds is 409 `channel_exists` as for SMTP; a mailbox another channel uses, 409
`mailbox_exists`.

#### Switching provider

`POST /channels/{id}/provider` with `{provider: "entra", importWindow?, onClose?, folder?}` (`channels-manage`) moves an
SMTP channel to Microsoft 365 and keeps its **id, address, conversations and default
flag**. Any other provider in the body is a 400; an unknown channel a bare 404; a
channel already on `entra` 422 `provider_unchanged`. `Status` and `Check` (and
`EnsureFolder` when `onClose` is `move`) run on the
channel's own address before the transaction, with the create's table; a mailbox another
channel uses is 409 `mailbox_exists`. The channel **keeps its own address**, which may be
an alias of the mailbox rather than its primary address: the mail then goes out `From:`
the alias through the shared mailbox — whether Exchange sends it so, rewrites it or
refuses it is **not verified on a real tenant**. Then, under the
default slot's lock and a re-read, the provider and `settings` are written and the
**credential row deleted, in one transaction**: a failure leaves the channel SMTP with its
credential. Answers 200 with the channel. The mailbox `Check` recorded on Entra's side
stays recorded if that transaction fails; it only lets the provider send from a mailbox
an admin checked, and the next `Check` rewrites it.

**Back to SMTP** is `PUT /channels/{id}` with `provider: "smtp"` and a full `smtp` block,
**the password included** — there is no stored one to reuse (*A password is required to
switch a channel to SMTP.*; without a block, *SMTP credentials require a host and valid
port.*). It writes the credential row, sets the provider to `smtp` and `settings` to
`{}`, and **after the commit** calls the provider's `Unwatch` and `Forget` for the old mailbox,
best-effort: a failure is logged and the 200 stands. The channels are read again first,
and `Unwatch` and `Forget` are skipped when another channel has taken the mailbox in between, so that
channel can still send and receive. The channels page has no control for
it; it is the API's. A `PUT` without `provider` keeps the channel's own, so `isActive`,
`isDefault`, `displayName`, `onClose` or `folder` alone on a Microsoft 365 channel need no
credential (when `onClose` is set to `move`, `EnsureFolder` runs before the transaction;
when `isActive` is set to `false`, `Unwatch` runs best-effort after the commit); a `PUT`
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
  stamp set may already have been delivered — re-claimed after its lease expired, or
  claimed back from `retry` after a send that had begun and then failed (a mailbox's
  `Send` that timed out may have been taken by Exchange) — the resend still happens, but
  it is logged as a possible duplicate and counted on the
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
  `Key` is not the channel's provider, is `ErrMailboxUnavailable` and **held** (below) — a
  channel is **never sent through another provider**. The same `Outbound` is rendered as MIME
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
a draft's `Bcc:` header reaches the blind copies as Graph documents for a MIME draft, nor
how Exchange treats a channel switched from SMTP whose address is an alias. The
pull request's real-tenant checklist reads the sent item's `internetMessageId`, the
recipient's raw headers and a blind copy's arrival. If Exchange rewrites the
`Message-Id`, a customer's answer names Exchange's own in `In-Reply-To`, which no message
of Vantigo's has, and [threading](#threading) falls back on Graph's `conversationId` — with
the limit stated there.

**Terminal failures.** A send through the mailbox that fails with one of these codes
**fails the job at once**, after one attempt, rather than at the eighth: no retry can
mend it — `provider_unavailable` aside, which ends a day's hold (below).

| Code | From |
| --- | --- |
| `mailbox_denied` | `ErrMailboxDenied`: the mailbox is outside the application's scope, or a role is missing |
| `mailbox_gone` | `ErrMailboxGone`: the mailbox no longer exists |
| `mailbox_unknown` | `ErrMailboxUnknown`: no `Check` recorded the mailbox on Entra's side (forgotten, say) |
| `too_large` | `ErrMailTooLarge`: over `MaxMessage`, Graph's 413, or Exchange's 400 `ErrorMessageSizeExceeded`, `MessageSizeExceeded` or `ErrorMessageTooBig` (the mailbox's own send limit below Graph's) |
| `bcc_unsupported` | `contracts.ErrBccUnsupported`: the draft did not keep every blind copy, so nothing was sent — a retry would build the same draft |
| `channel_misconfigured` | the channel's `settings` name no mailbox |
| `provider_unavailable` | the provider stayed unavailable for 24 hours after the job was queued (below) |

The job becomes `failed` and its deliveries `submission_failed`, `jobs_failed` counts
it, and `last_error` stays the constant *Outbound delivery failed.* — the module's one
redaction of an outbound error, so nothing Graph said is stored. The code goes beside it
in the failure event's `data_json` (`{"error":"Outbound delivery failed.","code":"mailbox_denied"}`)
and in the log line.

**An unavailable provider holds the job.** `ErrMailboxUnavailable` — the Mail channels
switch off, the connection failing, Entra off, no provider in the slot or one under
another key — spends **no attempt**: the job goes back to `retry` with the claim's attempt
given back and its send marker cleared (nothing was handed over), and is tried again after
the time it has waited so far — at least the backoff below, **at most 5 minutes**. When a
hold begins, each sendable delivery gets one `retrying` event whose data is
`{"error":"Outbound delivery failed.","code":"provider_unavailable"}`; the later held tries
write no event. A try in between that reaches the provider and fails writes the usual
`retrying` events without a code, and the next unavailable try begins a new hold, with its
events again. Held tries are counted on `communications.outbox.jobs_held`, not
`jobs_retried`. **24 hours after the job was queued**, a try that still finds the provider
unavailable fails it: its deliveries `submission_failed` with the code
`provider_unavailable`, counted on `jobs_failed`. An outage shorter than that loses
nothing.

Everything else — throttling, a 5xx, any error the contract does not name — is retried
on the backoff below, like an SMTP failure.

A failed send is rescheduled with an exponential backoff of
`min(3600, 2^min(attempts, 10))` seconds, and `attempts` is incremented when the job
is claimed rather than when it fails. Two consequences of that expression are worth
stating because the arithmetic is easy to read wrongly:

- **The 3600 s cap is unreachable.** The exponent is clamped at 10, so the term never
  exceeds 1024 s and the `min` never binds.
- **With the default `max_attempts = 8`, the largest backoff a live job ever waits is
  128 s.** The sequence is 2, 4, 8, 16, 32, 64, 128 s, and the eighth attempt is
  terminal — so 256, 512 and 1024 s are never scheduled either.

The advisory lease guards the **retention worker only**. Outbox delivery, attachment
cleanup and the mailbox sync take no advisory lock: each claims work with a conditional
update whose `WHERE` re-asserts the predicate its candidate query used, so a claim another
worker already won matches nothing. Every replica runs those three workers every cycle,
by design. Adding an advisory lock to any of them would be a defect, not a hardening.

### Background workers

The module contributes four background workers: outbox delivery, retention, attachment
cleanup and `communications-mailbox-sync`, which reads each Microsoft 365 channel's Inbox
([Receiving](#receiving)). `worker` mode always runs them, `server`
mode never does, and `api` mode runs them when `WORKERS_IN_PROCESS=1` (the default).
A deployment that scales the API horizontally should set `WORKERS_IN_PROCESS=0` on
the API replicas and run one dedicated `worker` container, so extra HTTP replicas do
not multiply the pollers.

### Metrics

Six counters on an OpenTelemetry meter named `Vantigo.Communications`, none with
attributes. Five are the outbox's: `communications.outbox.possible_duplicate_sends`,
`.jobs_completed`, `.jobs_retried`, `.jobs_held` (a try of a Microsoft 365 channel's job
put back while its provider is unavailable, spending no attempt) and `.jobs_failed`.
**Alert on the last one: every increment is undelivered customer email.** The sixth is
the mailbox sync's, `communications.inbound.cycles_failed`: one per cycle whose failure was
written on its channel — a refusal (`mailbox_denied`, `mailbox_gone`, `mailbox_unknown`)
or any other failure to read or import (`sync_failed`) — and not a cycle held for an
unavailable provider, nor one that lost its lease. A rise means a channel's mail is not
arriving. There are deliberately no others — no retention, cleanup, storage, SMTP or AI
metrics, and no histograms.

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
| `COMMUNICATIONS_INBOUND_MAX_BYTES` | The largest received message, in bytes of MIME, the mailbox sync reads; a larger one is recorded `skipped`/`too_large` and never stored (1 MiB – 150 MiB, Graph's attachment ceiling) | `26214400` (25 MiB) |

A received message is a message as any other here: its original (`raw_payload_storage_key`)
and its attachments are queued on the ledger with it, by retention and by an erase alike.
The **receipts' sweep** runs once per retention cycle, after the message batches, in
chunks of `COMMUNICATIONS_RETENTION_BATCH_SIZE`: a [receipt](#receipts) whose message is
gone — deleted by retention or an erase, or never stored, a skipped message — is deleted
once its `received_at` is before the retention window's start, **except each channel's
newest receipt**, a bound on an expired delta's restart point. That is the same cutoff the
[mailbox sync](#the-mailbox-sync) reads nothing before, so a message whose receipt the sweep
deletes is never read again; until then the receipt, with its Message-Id, keeps an erased
or retained-away message from being imported a second time when the delta names it once
more (marked read, say). A receipt whose message still exists stays with it.

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

- **The attachments it takes**: `(budget − 16 384) × 100 / 137`, rounded down to
  100 000 bytes — **2 100 000** of Entra's 3 000 000 — what a message with no body can
  carry once base64 and the headers are counted. `ConversationDetailResponse` answers it as
  `maxAttachmentBytes` beside the budget, `maxMessageBytes` (both null for SMTP), so the
  composer shows *Attachments up to about 2.1 MB in total on this channel* without
  `channels-manage`.
- **Staging** on a conversation whose channel is Microsoft 365: a file whose size × 1.37
  (base64 and a margin) plus 16 KiB (headers and boundaries) exceeds the budget is **413**
  `attachment_too_large`, *The attachment is larger than this channel can send (about 2.1
  MB).*
- **Reply**: the estimate of the whole message — every staged attachment × 1.37, the text
  and the HTML bodies each × 3 (quoted-printable at its worst, every byte `=XX`), plus
  16 KiB — over the budget is **422** `too_large`, *The message and its attachments are
  larger than this channel can send.*, before anything is written.

The provider's `ErrMailTooLarge` at send time stays the last guard, and fails the job
(`too_large`). A larger message would need a draft with upload sessions, which is not
built. An SMTP channel has no budget beyond the staging limit and its server's own.

`GET /api/v1/communications/attachments/{id}/download` requires ConversationsView and
streams a message's attachment — never a staged upload — through the application. A
`clean` one, the composer's, is answered with its declared media type, as before. An
`unscanned` one, received with an inbound message from a stranger, is answered as a
**file to save**: `Content-Disposition: attachment; filename*=UTF-8''…` (the name
percent-encoded, so nothing in it can end the header), and its media type only when that
is passive — PNG, JPEG, GIF, WebP, BMP, TIFF, PDF, plain text, the Word, Excel and
PowerPoint formats, zip — `application/octet-stream` otherwise, so a received `text/html`
or `image/svg+xml` is never served inline from Vantigo's own origin. The detail's
`downloadAvailable` is true for both. As before, any ConversationsView holder may fetch any
downloadable attachment by its id. It never redirects to a
provider URL and never exposes a storage key; the `downloadPath` field in
conversation DTOs is this same-origin endpoint. Attachments are stored under the
`communications` storage scope — see [object storage](/en/admin/object-storage/), which is
**filesystem-only** in this release and fails closed at each operation when
`STORAGE_PROVIDER` is unset.

**There is no attachment scanner.** Nothing transitions an upload from `pending` to
`clean`, so new uploads are created `clean` on the reasoning that there is nothing to
scan. The `scanStatus` and `ready` fields, and every gate that reads them, are kept
exactly as they are, and a scanner can be added later without a contract change. Do not
read a `clean` status as evidence that a file was inspected. A received attachment is
stored `unscanned` (`message_attachments.scan_status` has no CHECK, so the value needed no
migration) and stays so: the name says what is true of it, and the inbox shows it with a
warning.

**The original of a received message** is
`GET /api/v1/communications/messages/{id}/raw` (ConversationsView, as the attachment
download): 200 `message/rfc822` with `Content-Disposition: attachment;
filename="message-{id without dashes}.eml"` — the message whole as the mailbox held it, its
HTML unsanitised and its attachments inside, answered only as a file to save, never
rendered by Vantigo. A message with no original (every one but an inbound message), an
unknown id and an object gone from the store are the same bare 404; any other storage
failure is 503 `attachment_storage_unavailable`. The detail answers each message's
`rawDownloadPath`, this path or null.

## Receiving

A Microsoft 365 channel receives (Outlook connection and Microsoft 365 mail design M14,
M15, M16, M17): mail arriving in its shared mailbox's **Inbox** becomes an inbound message of a
conversation, threaded onto the one it answers or starting one, its sender linked to a
customer when the address belongs to exactly one. Vantigo reads the mailbox with the
application's token through the `ChannelMailbox` slot's two reads, `Delta` and `Raw`
([Entra's inbound reads](/en/reference/entra/#delta-and-raw)), subscribes the Inbox to
change notifications when configured (`Watch`, `Unwatch`, `Notified`), and leaves the
messages in the Inbox untouched until a conversation is closed on a channel configured to
mark or move them ([Outlook state on close](#outlook-state-on-close)). An SMTP channel does not receive.

### The mailbox sync

`communications-mailbox-sync` is a worker of this module. Every **30 seconds** it drains:
it first reads any new mail change notices from the provider, then claims a channel due,
runs one cycle on it, and claims the next — or, when no channel sync is due, claims the
next due [on-close mark job](#outlook-state-on-close) — until none is due.

- **Change notices (`takeNotices`).** Before claiming, the worker asks
  `ChannelMailbox.Notified(since)` from an in-memory cursor initialised to one minute
  before the worker started. Each notice's mailbox id marks its active `entra` channel's
  `channel_sync` row `notified = true` and `next_sync_at = now` when the row is not
  backing off a failure (`attempts = 0` and `state <> 'failed'`), so a notified channel is
  claimed next.
- **The claim.** A `channel_sync` row is due when its channel is an **active** `entra`
  channel, its `state` is not `stopped` (nothing in this release sets `stopped`), its
  `next_sync_at` has come and it holds no lease — or its lease has expired. A row marked
  `notified` comes first (a finished pass clears it), then the
  longest due. The claim is a
  conditional update that re-asserts that predicate and writes a new `lease_id` (32 hex
  digits) and `lease_until` **two minutes** ahead; 0 rows is a lost race, and the worker
  tries the next candidate, ten times at most. There is no advisory lock.
- **Every later write of the cycle** names `channel_id` and `lease_id` together; one that
  changes 0 rows means another worker took the row, and the cycle **stops, writing nothing
  more** (logged at info). The lease is renewed to two minutes from now **before each
  message**.
- **The provider first.** No provider in the slot, one under a key other than `entra`, a
  `Status` that is not available or does not answer — or a read refused as
  `ErrMailboxUnavailable` — **holds** the channel: `next_sync_at` five minutes ahead,
  `error = 'provider_unavailable'`, `attempts` and `state` unchanged, the lease given back.
- **One page a cycle.** The cycle reads one page of the Inbox's delta: with the stored
  `delta_link` when there is one, else afresh from `import_from` (or from `restart_from`,
  below). A page holds at most 50 messages and is **handled whole** — there is no cap per
  cycle below the page. Items removed from the Inbox are ignored (Vantigo keeps what it
  imported). **Nothing received before the floor is read**: the later of `import_from` and
  the retention window's start, now − `COMMUNICATIONS_RETENTION_DAYS` (365 days by
  default) — the delta also answers changes to older messages, and a message retention
  deleted must not come back. An item with no `receivedDateTime` is kept and timed at the
  moment it is read. The rest are taken **oldest first**, ties by Graph's id. An item the channel already has
  a [receipt](#receipts) for is skipped silently; every other is
  [imported](#one-messages-import).
- **The link is kept last.** Once every item of the page has a receipt, the page's link is
  stored and the lease given back. A page that ends the delta stores the **delta link**,
  and the channel becomes `active`, its `error`, `restart_from` and `notified` cleared,
  `attempts` 0 and `last_synced_at` now. When change notifications are active
  (`PUBLIC_BASE_URL` is set and `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` is on) and
  `Watch(mailbox, "inbox")` succeeds after the pass, `next_sync_at` is set **one hour**
  ahead as a safety net; otherwise — notifications off, `PUBLIC_BASE_URL` unset, or
  `Watch` failing (logged at warn) — `next_sync_at` is **five minutes** ahead. Every
  stored page forgets the count of failed tries (below). A page with more to come stores the **next
  page's link** with `next_sync_at` now, so the same drain claims the channel again at
  once. A cycle that stops half-way stores nothing, reads the same page next time, and the
  receipts skip what it already imported.
- **An expired delta.** When Graph no longer takes the stored link
  (`ErrMailboxDeltaExpired`), the link is cleared and the cycle reads afresh in the same
  cycle, from a **restart point**: `import_from` when the channel never finished a pass
  (its pages come in no order, so a receipt says nothing of what is left); else **an hour
  before the earlier** of its last finished sync and its newest receipt's `received_at`,
  never before `import_from`. The point is kept in `channel_sync.restart_from` until a pass
  finishes, so a fresh read that fails is tried again from the same instant. The receipts
  skip the overlap.
- **Failures.** A refusal no retry mends soon — `ErrMailboxDenied`, `ErrMailboxGone`,
  `ErrMailboxUnknown` (the channel's `settings` naming no mailbox counts as this too) —
  sets `state = 'failed'`, `error` to `mailbox_denied`, `mailbox_gone` or `mailbox_unknown`,
  `next_sync_at` **an hour** ahead (an Exchange administrator may fix the mailbox's RBAC
  meanwhile), and spends an attempt. Anything else — throttling, Graph not answering, the
  object store, the database — sets `error = 'sync_failed'`, keeps `state`, spends an
  attempt, and backs off **one minute × 2^attempts** (the attempts spent before this one),
  **at most an hour**. Each is a warning in the log with the channel's id and the code —
  never an address, a subject or Graph's words — and one on
  `communications.inbound.cycles_failed` ([Metrics](#metrics)). A message whose read or
  import failed has no receipt, so it is tried again.
- **Giving a message up.** So that one bad email cannot hold its channel for ever, a
  message whose import fails is given up on — a receipt `failed`/`import_failed`, logged
  at warn, and the rest of the page imports — **at once** when PostgreSQL refuses one of
  its values (SQLSTATE class 22 or 23), and otherwise **on its third failed try**, counted
  on the row across cycles (`channel_sync.failing_item`, `failing_tries`; a try at another
  message starts again from one). A failure of the provider (`Raw`) or of the object store
  is never counted: it is retried for ever, as a lost lease or a stopped worker is.
- **New mail appears** within about five minutes of arriving: the interval after a finished
  sync, plus the 30-second poll.

### One message's import

For each item without a receipt, in this order:

1. **The read.** `Raw` reads the message whole, at most
   `COMMUNICATIONS_INBOUND_MAX_BYTES` (25 MiB by default). Larger is a receipt
   `skipped`/`too_large`, and nothing is stored; a message deleted or moved since the delta
   named it (`ErrMailItemGone`) is `skipped`/`gone`. Any other error ends the cycle as a
   failure, without a receipt, and is never counted toward giving the message up.
2. **The Message-Id** is the message's own header, bracketed — or, when its header block
   does not read or names none, the `internetMessageId` Graph answered.
3. **Our own.** A Message-Id of the outbox's form, `<32 hex digits@vantigo.invalid>`, whose
   digits are the id of a message **of this database**, is a message Vantigo sent delivered
   back to the mailbox (the shared mailbox in Cc, say): `skipped`/`own_message`, nothing
   stored. Another installation's id of the same form is imported as any other. After the
   parse, a message whose `From` is **the channel's own address** (in any case) is
   `skipped`/`own_message` too, whatever its Message-Id — Exchange may rewrite ours.
4. **A copy.** A Message-Id the channel already has an `imported` receipt for is a copy —
   the same mail delivered twice, or moved out and back under another id: a receipt
   `duplicate` whose `message_id` is the original's message, nothing stored.
5. **The parse** ([the parser](#parsing)) and, when the message has HTML,
   [the sanitiser](#sanitising). A message the parser cannot read is still stored, as an
   **unreadable** message (below).
6. **Objects before rows.** The original (`message/rfc822`, at
   `inbound/{channel}/{message}/raw.eml`) and every attachment
   (`inbound/{channel}/{message}/attachments/{attachment}`) are reserved on the cleanup
   ledger and written to the object store **before** the transaction that names them; the
   transaction marks them owned. An import that does not commit queues its objects for
   deletion at once; a crash in between leaves reservations the cleanup worker reclaims
   when they expire.
7. **The linker and the thread** are read before the transaction: the
   [linker](#the-customer-linker) calls the customer directory, a call out of the module,
   which is refused inside a transaction; the [thread](#threading) is re-checked inside it.
8. **One transaction**, in which no provider or directory call can happen: the matched
   conversation locked and re-read — gone or `archived` starts a new one, `closed` is
   reopened to `open` unless the message is an [auto-reply](#auto-replies), `open` stays as
   is —; or a new conversation, `open`, with the message's subject and the linker's one
   customer, if any; the participants — the sender, and each Cc address but the channel's
   own and the sender's — found in the channel **in any case**, or created with the
   address in lower case, and each linked to the conversation; the message,
   `direction = 'inbound'`, its `participant_id` the sender, `occurred_at` Graph's
   `receivedDateTime`, `rfc_message_id` the bracketed Message-Id; its attachments,
   `unscanned`; the conversation's activity — its subject filled if it had none, its
   preview and `last_activity_at` advanced only by a message newer than the last; the
   linker's writes; and **the receipt, last**.
9. **A race.** The receipt's unique indexes decide two workers importing at once: a
   violation of `(channel_id, provider_message_id)` means the other imported this very
   message, and this import is dropped; of `(channel_id, rfc_message_id)` among `imported`
   receipts, that it imported a copy under another id, and this one becomes a `duplicate`
   receipt. Either way its objects go to the ledger.

The message's `channel_metadata_json` is `{from, to[], cc[], inReplyTo, references[],
autoSubmitted, graphConversationId}`, plus `unreadable` and `htmlDropped` when set: the
addresses bare and in lower case, the ids bracketed, references oldest first. Reply and
reply-all read `cc` and `references` back; the detail reads `autoSubmitted` and
`unreadable`.

**The bodies.** `text_body` is the message's text, or, when it has only HTML, text derived
from the sanitised HTML — every received message has a text, which the AI draft reads.
`html_body` is the sanitised HTML. Each is at most 1 MiB: a longer text is cut with `…`; a
longer HTML is **dropped**, the text kept and `htmlDropped` set — the detail answers every
message's bodies at once.

**An unreadable message** — no header block, or neither a sender nor a body — is stored with
the text *Vantigo could not read this message.*, no HTML, no attachment, its original kept,
`unreadable` in its metadata, and a receipt `failed`/`unparseable` pointing at it. It has no
sender, so it starts a conversation of its own, links no customer and is not one a reply
answers. The detail answers `unreadable: true` and its `rawDownloadPath`.

### Receipts

`communications.inbound_receipts` holds one row for every message the worker saw in a
channel, imported or not — which is what makes an import happen once:

| `outcome` | `reason` | `message_id` | When |
| --- | --- | --- | --- |
| `imported` | — | the message | stored and threaded |
| `duplicate` | — | the original's message | a copy of a Message-Id already imported in the channel |
| `skipped` | `too_large` | — | over `COMMUNICATIONS_INBOUND_MAX_BYTES` |
| `skipped` | `gone` | — | deleted or moved before it was read |
| `skipped` | `own_message` | — | a message this database sent, or one from the channel's own address |
| `failed` | `unparseable` | the message | stored as unreadable |
| `failed` | `import_failed` | — | given up on: its import failed each time |

A receipt holds Graph's immutable id (`provider_message_id`, compared exactly — Graph's ids
are case-sensitive), the Message-Id, `received_at` and `created_at`; no address and nothing
of the content. Two unique indexes: `(channel_id, provider_message_id)`, and
`(channel_id, rfc_message_id)` over `imported` receipts only, so a duplicate can carry its
original's Message-Id. The stored Message-Id, of the receipt and of the message, is the
header's, or Graph's `internetMessageId` when the header has none. `message_id` is set to
null when retention or an erase deletes the message; the
[sweep](#retention-cleanup-and-staged-objects) deletes such receipts once the message was
received before the retention window.

### Parsing

`mail.ParseInbound` ([the mail package](/en/contributing/platform-packages/#the-mail-package))
reads the message over Go's `net/mail` and `mime/multipart`: `quoted-printable`, `base64`
(tolerant of line breaks and missing padding), `7bit`, `8bit` and `binary`; headers in RFC
2047; file names in RFC 2231 in any charset, with continuations; and every charset
`golang.org/x/text` knows (`iso-8859-1`, `windows-1252`, `koi8-r`…), an unknown one read as
UTF-8 with its invalid bytes replaced. It walks at most **200 parts**, **10 levels** deep,
and keeps at most **100 attachments**; a damaged part is skipped, not fatal. A part with
no `Content-Type` is `text/plain`. Every NUL is removed from every string it answers
(PostgreSQL refuses one in text and `jsonb`).

- **The bodies** are the parts of type `text/plain` and the first `text/html` that are not
  attachments; several plain parts are joined.
- **An attachment** is every other leaf — one with a file name, a `Content-Disposition:
  attachment`, or any other type. A part with a `Content-ID` and no attachment disposition
  is **inline**, what the HTML's `cid:` images name. A forwarded message
  (`message/rfc822`) is kept whole as an attachment, `message.eml` when it names none, not
  opened; Outlook's `application/ms-tnef` is kept as `winmail.dat`, not decoded.
- **Threading's headers**: `Message-Id` and the first id of `In-Reply-To`, bracketed — an
  id with a control character in it counts as missing; `References` bracketed, distinct,
  oldest first, at most 100 — the first and the newest when a thread holds more; each id
  header is read for at most 400 ids. Subject decoded, trimmed, at most 998 bytes.
- **Addresses** in lower case; one over 320 characters or with a control character is
  dropped, and `To` and `Cc` keep **at most 100** each, the first.

### Sanitising

Received HTML is a stranger's. `mail.SanitizeHTML` parses it with `golang.org/x/net/html`
and, before any policy, rewrites each `img` whose `src` is `cid:` to the download path of
the message's own inline attachment — removing it when the message has no such part —,
removes every other `img` but a base64 `data:` PNG, GIF, JPEG or WebP (so **remote images
and tracking pixels go**), drops `background` and `srcset`, and drops the `href` of a link or
an image map's `area` whose scheme is not `http`, `https` or `mailto`. **No relative URL
survives** but the inline image's own download path, which crosses the policy as a
`vantigo-inline:` placeholder and is written back as
`/api/v1/communications/attachments/{id}/download`. Then bluemonday's UGC policy removes everything
executable — `script`, `style`, `iframe`, `object`, `embed`, `form`, `input`, `meta`,
`base`, `link`, `svg`, `math`, every `on*` attribute — and keeps a short list of
typographic and box style properties (colours, fonts, alignment, line height, margins,
paddings, borders, width, height, vertical alignment, white space), never `url(…)`,
`position` or `expression`, so mail keeps its look. Links get
`rel="nofollow noreferrer noopener"` and `target="_blank"`.

The inbox is the second wall: it shows the HTML in an `<iframe sandbox="">` whose document
carries its own CSP, `default-src 'none'; img-src data:; style-src 'unsafe-inline'`. A
sandboxed frame has an opaque origin and would send no session cookie, so the inbox
fetches the message's own inline images itself — only the attachments the HTML names, of
an image type and size it allows — and puts them into the HTML as `data:` URIs; nothing
in the frame reaches Vantigo's API or another host.

### Auto-replies

A message with `Auto-Submitted` other than `no`, `X-Auto-Response-Suppress`, `Precedence`
`bulk`, `auto_reply` or `list`, or `X-Autoreply` is automatic — an out-of-office answer, a
bounce, a list. It is imported and threaded as any other, answered `autoSubmitted: true`
in the detail, and **does not reopen** a closed conversation.

### Threading

A message is threaded within its channel, never across channels, in this order:

1. The conversation of the message its **`In-Reply-To`** names — a message of **any
   direction**, so a customer's answer to a reply Vantigo sent threads onto its
   conversation: the outbox's Message-Ids are stored bracketed, as received ones are.
2. Else the conversation of a message its **`References`** name, the newest reference
   first.
3. Else the conversation of the newest **inbound** message of the same Graph
   **`conversationId`** received in the last **90 days**.
4. Else a new conversation.

Several messages with one Message-Id answer the newest's conversation. The match is
re-checked in the import's transaction (`archived` starts a new one, `closed` is reopened
unless the message is automatic).

**If Exchange rewrites our Message-Id** on sending (not verified, [Delivery
semantics](#delivery-semantics)), a customer's answer to Vantigo's reply names Exchange's id,
which no message of Vantigo's has, and step 3 threads it when the conversation already
holds an inbound message of the same Outlook conversation — the usual case, a reply to a
customer who wrote first. A customer's first answer to a conversation **Vantigo began**
(`POST /conversations`) has no such inbound message, and starts a new conversation.

### The customer linker

Before the transaction, the sender's address is looked up in the customer directory
(`ContactsByEmail`, through an accessor that refuses a call inside a transaction). The
customers are every matching contact's candidate customers, deduplicated:

- **Exactly one** customer, and the conversation is new or has none: the conversation's
  `customer_id` is set, `customer_association_source = 'automatic'`.
- **Several**, on the same condition: each becomes a candidate
  (`conversation_customer_candidates`), and no customer is set.
- **None**: nothing; the AI suggestion stays available.
- A conversation that **has a customer** — set by hand or by an earlier message — is never
  linked again, so a second sender in one thread does not move it.
- The sender's participant gets the contact when **exactly one contact** matched and it has
  none; several contacts at one customer link the customer, not a contact.
- Customers off (no directory) links nothing; a directory that fails is logged, and the
  message is imported unlinked.

**The `From` is not authenticated.** The linker, the participant and reply's To all take
the message's `From` header as it stands: a mail with a forged `From` naming a customer's
contact links a new conversation to that customer, and a reply — or a reply-all, which
copies the message's Cc list as it stands — goes to the addresses the message names.
Exchange Online's anti-spoofing (SPF, DKIM, DMARC) and Junk filtering are the mitigation:
the worker reads only the **Inbox**, so a message Exchange put in Junk Email is never
imported. Vantigo does not read `Authentication-Results` itself.

### Reply and reply-all

A reply answers the conversation's **latest inbound message with a sender** (by
`occurred_at`, ties by id) — the rules of the .NET module, ported:

- **To** is that message's sender alone. A conversation with no such message — one created
  through `POST /conversations` and never answered, or of an SMTP channel — answers 422
  `recipients_missing`.
- **Reply-all's Cc** is that one message's Cc only — not its other To recipients — each a
  valid bare address, without the channel's own address and the sender, distinct in any
  case, the first 100 (unreachable from a real import, whose `Cc` the parser already
  caps at 100), resolved to the participants the import created (one missing is
  left out). `reply_all` sends to them as Cc, `reply` to the sender alone; the reply's
  deduplication and suppression checks run over both.
- **The threading headers**: the reply's channel metadata carries `inReplyTo`, that
  message's Message-Id, and `references`, its own References then its Message-Id, distinct
  in any case — **the first id and the last 19** when there are more than 20 (RFC 5322's
  advice; .NET kept the oldest 20, which drops the very message answered in a long
  thread). The outbox writes them as `In-Reply-To` and `References`. A message with no
  Message-Id gets no threading headers.
- **The detail's `replyRecipients`** answers the same: `canReply` and `replyTo` the sender,
  `replyAllCc` the Cc participants and `canReplyAll` whether there are any.

Each message of the detail also answers `autoSubmitted`, `unreadable` and
`rawDownloadPath` (always present; optional in the schema only because the recorded
exchanges predate them).

**The AI draft** builds its context from the conversation's 20 newest inbound text bodies,
so it drafts from the customer's own words once a channel receives; the HTML body is never
read.

### Outlook state on close

A Microsoft 365 channel's `settings.onClose` decides what happens in the shared mailbox in
Outlook when a conversation on the channel transitions to `closed`
(`PATCH /api/v1/communications/conversations/{id}` with `status: "closed"`):

- `none` (the default): nothing is changed in Outlook.
- `read`: every inbound message of the conversation is marked as read in Outlook
  (`isRead: true`).
- `move`: every inbound message of the conversation is moved to the channel's configured
  top-level folder (`settings.folder`, `"Handled in Vantigo"` by default), ensured by
  `EnsureFolder`.

In the close's own transaction, when the channel is an `entra` channel whose `onClose` is
`read` or `move`, `UpsertMailboxMark` writes or resets the conversation's row in
`communications.mailbox_marks` (migration `00094_communications_mailbox_marks.sql`,
`conversation_id` primary key `ON DELETE CASCADE` to `conversations(id)`): `operation`
(`read` or `move`), `folder`, `status = 'queued'`, `attempts = 0`, `next_attempt_at` and
`queued_at` now. Reopening and closing the conversation again resets the row to `queued` so
any new inbound messages that arrived in between are marked or moved too.

`communications-mailbox-sync` drains due `mailbox_marks` jobs (`queued` or `retry`,
`next_attempt_at <= now`, no active lease) under a two-minute lease:

- It lists the conversation's `imported` receipts' `provider_message_id`s
  (`ListConversationInboundProviderIDs`), calls `EnsureFolder` once when `operation` is
  `move`, and calls `Mark` for each message outside any transaction, renewing the lease
  before each message.
- `contracts.ErrMailItemGone` (the message was already deleted in Outlook) is treated as
  idempotent success for that message.
- `ErrMailboxUnavailable` holds the job (`HoldMailboxMark`: attempt given back, tried
  again up to five minutes later) until 24 hours after `queued_at`, when it fails as
  `provider_unavailable`.
- `ErrMailboxDenied`, `ErrMailboxGone` and `ErrMailboxUnknown` fail the job immediately;
  any other error retries up to 8 attempts with exponential backoff (`min(3600, 2^attempts)`
  seconds).

### Not verified on a real tenant

What the pull request's real-tenant checklist settles, and this page will record: whether
Exchange keeps our `Message-Id` (and so which [threading](#threading) step answers a
customer's reply); the exact form of the delta's next and delta links (`mailFolders` or
`mailfolders`, the Inbox's name or its id — Entra accepts either); the code of an expired
message delta; whether Graph honours 50 messages a page; whether a message moved into the
Inbox from another folder arrives as created or updated (the worker imports either);
`$value`'s ceiling, whether it ever redirects (the client follows none), and how fast
25 MiB arrives inside the read's 90 seconds.

## Sending from one's own address

People whose own Microsoft 365 Outlook connection is active with the `mail` feature (`Mail.Send` delegated)
can choose **Send from my address** on both new conversations and replies (`sendFromPersonal: true`).

- **Channel continuity**: The conversation always belongs to its channel (`channels.address`), regardless
  of whether an individual message was sent from the channel's shared mailbox or from an author's personal mailbox.
- **`From` and `Reply-To`**: For a message with `send_as = 'personal'` (migration
  `00095_communications_personal_sending.sql`), the `From` header carries the author's personal directory
  email address and display name (`from_address`, `from_display_name`), both recorded when the message is
  composed. The `Reply-To` header is set to the channel's address and display name as they are when the
  outbox sends; `GetConversation` answers it as `replyToAddress`, read from the channel at the time of the
  read, not stored with the message. Customer replies therefore arrive directly in the shared channel
  mailbox and thread into the conversation without reading anyone's personal Inbox.
- **Availability check**: Before beginning a transaction, the handler checks
  `PersonalMailbox.Status(ctx)`. If personal sending is not available (`!Available`), the API returns 409
  `connection_needed`. It also enforces the 3 MB MIME size budget (`contracts.MaxPersonalMessageBytes`, the
  same figure as a channel mailbox's), returning 422 `too_large` if exceeded. A failure to read the
  author's display name is a 500, never a message sent without one.
- **Outbox dispatch & immediate failure**: The outbox dispatches personal messages via
  `PersonalMailbox.Send(contracts.AsUser(ctx, authorUserID), mime, bcc)`. If Microsoft refuses due to an
  expired session, revoked token, or missing consent (`contracts.ErrConnectionNeeded`), the outbox job fails
  immediately as a terminal failure after 1 attempt (`connection_needed`, never silently falling back to the
  channel's address).
- **Retry**: `POST /api/v1/communications/conversations/{id}/messages/{messageId}/retry`
  (`communications:conversations-reply` and `communications:conversations-view`) re-queues an outbound
  message whose outbox job is `failed`: 409 `message_not_failed` otherwise, 422 `channel_inactive` when
  the channel is off. A message sent from the author's own address may be retried by its author alone
  (403 `forbidden`), and only while `PersonalMailbox.Status(ctx).Available` (422
  `personal_sender_unavailable`); a channel message may be retried by anyone the permissions allow. The
  job is re-queued first, conditionally on its status still being `failed` — a retry that loses that
  race writes nothing and answers `queued` all the same —, then its `submission_failed` deliveries go
  back to `queued` with an event each.

## AI draft and customer suggestion

Both AI operations answer 503 `ai_unavailable` unless all three conditions hold:
`COMMUNICATIONS_AI_ENABLED=1`, `COMMUNICATIONS_AI_PROVIDER` is `openai` (the
default), and `COMMUNICATIONS_AI_API_KEY` is set. `COMMUNICATIONS_AI_MODEL` defaults
to `gpt-4o-mini`. A half-set configuration is the documented unavailable state, not a
startup error: no chat client, network client or provider dependency is constructed
at all in that state, and any other provider name leaves the feature unavailable
rather than refusing to start, so a second provider stays additive.
