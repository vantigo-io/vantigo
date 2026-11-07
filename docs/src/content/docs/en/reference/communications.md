---
title: "Communications module"
description: "Business email from shared mailboxes: the channels' providers — SMTP, and the shared mailboxes of every mailbox provider (Microsoft 365 through the Microsoft Entra ID module, and Google Workspace's licensed Gmail mailboxes through the Google Workspace module), named by key, created by address or switched from SMTP, with an optional import window and Outlook on-close behavior — the outbox and its delivery through either, the size a mailbox channel takes; the OutboundMail slot, through which Invoices and Calendar send customer mail from a channel an Owner chose, and the end of any fall-back to the installation's SMTP; receiving into a mailbox channel — the mailbox sync, change notifications, receipts, parsing, sanitising, threading, the customer linker, reply and reply-all, and Outlook state on close —; sending from one's own address — Outlook or Gmail — through the provider that answers for the person; Messenger channels — the channel types and the default of each, the ChannelMessenger slot, the messenger sync worker, components, answers and the reply window, sending a message in parts, and the new refusals —; retention and the operator's configuration."
sources:
  - apps/server/internal/communications
  - apps/communications/frontend
  - openapi/communications.yaml
  - apps/server/internal/openapi/specs/communications.yaml
  - apps/server/internal/db/migrations/00091_communications_channel_settings.sql
  - apps/server/internal/db/migrations/00092_communications_inbound.sql
  - apps/server/internal/db/migrations/00094_communications_mailbox_marks.sql
  - apps/server/internal/db/migrations/00095_communications_personal_sending.sql
  - apps/server/internal/db/migrations/00111_communications_provider_neutral.sql
  - apps/server/internal/db/migrations/00130_communications_messaging.sql
  - apps/server/internal/db/migrations/00139_communications_contact_hints.sql
  - apps/server/internal/contracts/contact_channels.go
  - apps/server/internal/contracts/outbound_mail.go
  - apps/server/internal/contracts/channel_mailbox.go
  - apps/server/internal/contracts/channel_messenger.go
  - apps/server/internal/contracts/personal_mailbox.go
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

Mail received into a mailbox channel ([Receiving](#receiving)) reaches the export and
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
`smtp`, through the channel's own SMTP server, or the **key of a mailbox provider** — a
shared mailbox reached through the `contracts.ChannelMailbox` filled under that key
(Google Workspace design G4). Communications names no mailbox provider: a channel's
provider is `smtp` or the `Key()` of a filled `ChannelMailbox`, and validation asks the
slot which keys are filled. Two modules fill it: the [Microsoft Entra ID
module](/en/reference/entra/#mail-channels), under `entra` (**Microsoft 365**, an Exchange
Online shared mailbox reached through Microsoft Graph), and the [Google Workspace
module](/en/reference/google/#mail-channels), under `google` (**Google Workspace**, a
licensed Gmail mailbox reached through the service account's domain-wide delegation).
Mailbox providers are called a *mailbox channel*'s
below. Mailgun, which this port never created, has left the contract; a create or update
naming a provider that is neither `smtp` nor a filled key is a 400 under `smtp` (*SMTP
credentials require the smtp provider.*).

A channel also has a **type**, `email` or `messenger` ([Messenger channels](#messenger-channels)):
an `email` channel's provider is `smtp` or a mailbox provider's key, a `messenger` channel's the
key of a `contracts.ChannelMessenger`, and everything this section says of providers is of
`email` channels unless it says otherwise.

**Only a mailbox channel receives.** Its shared mailbox's Inbox is read by the
`communications-mailbox-sync` worker ([Receiving](#receiving)), which writes each message
as an inbound message of a conversation. An SMTP channel only sends: there is no webhook,
no IMAP and no other inbound path, so a conversation of an SMTP channel holds no inbound
message and cannot be replied to ([Reply and reply-all](#reply-and-reply-all)).

`GET /api/v1/communications/channel-providers` (`channels-manage`) is what a channel can
use, so the channels page reads Communications' own API and never a provider's:

- `{key: "smtp", displayName: "SMTP", available: true}`, always first;
- then **every filled `ChannelMailbox`, in slot order** (the enabled modules' order),
  each `{key, displayName, available, reason?, warning?, maxMessageBytes}`. `key` is the
  provider's `Key()` (`entra`), `displayName` its `DisplayName()` (*Microsoft 365*);
  `available` is its status. Not available, `reason` is the provider's — `switch_off`,
  the connection's reason — or `status_unavailable` when the status did not answer.
  Available, `warning` is `tenant_wide` while the provider's grant reaches every mailbox
  in the tenant — always for Google Workspace, whose domain-wide delegation cannot be
  limited to some mailboxes. `maxMessageBytes` is that provider's `MaxMessage`
  ([attachments](#attachments)).

The page's provider select lists SMTP, then every listed provider by its `displayName`
— one not available disabled, with its `reason` — and then *Vantigo hosted mail*,
greyed. An SMTP channel's row has a **Switch to {provider}** for each listed provider
that is available. The page shows a `tenant_wide` warning above the table for each entry
that says so, in that provider's words: Microsoft 365's names RBAC for Applications and
the tenant-wide consent, Google Workspace's the domain-wide delegation. The note under
the address, and the page's words for `personal_mailbox`, `mailbox_denied` and a sync
failed `mailbox_denied`, are the provider's own too; `mailbox_denied`'s help link goes to
the provider's mail channels section of the administration guide
(`/admin/entra/#mail-channels`, `/admin/google-workspace/#mail-channels`).

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

**A channel with no stored credentials is Unavailable and sends nothing** (integrations
design T5). Every channel this API creates gets its credential row in the same
transaction, so the state is reached only when the row has been deleted directly. There
is **no fall-back to the installation's SMTP**: `smtpConfig` reads the channel's row and
nothing else, and nothing in the module reads `Config.Mail` (a test parses the package to
prove it). `ChannelResponse.available` is false for such a channel, the channels page
shows it **Unavailable**, `OutboundMail.Channels` lists it with `available` false, and an
outbox job on it fails at once with the code `channel_unavailable` and the sentence *the
channel has no credentials* in the log (below); no retry can mend it.

The `SMTP_*` environment variables are a **different thing**: they configure the system
mailbox (`Deps.Mail`), which writes to the tenant's own employees — identity's
invitations and password resets, Staff's notices, Calendar's colleague notices —, and
which **no channel, document or invitation ever uses**. See
[identity and authentication](/en/admin/authentication/) and
[installation](/en/admin/installation/).

**SMTP AUTH in Exchange Online.** An SMTP channel whose server is Exchange Online signs
in with SMTP AUTH and a password. Microsoft withdrew the removal it had planned for
spring 2026; according to Message Center MC786329, read through secondary sources and
not first-hand, basic SMTP AUTH stays on until the end of December 2026, is then off by
default for existing tenants — an Exchange administrator may turn it back on — and
unavailable for new ones, with a final date in the second half of 2027. Such a channel
needs that exception from January 2027, or a [switch to a mailbox
provider](#switching-provider), which keeps its id, its conversations and its default flag.

### Mailbox channels

A mailbox channel (Outlook connection and Microsoft 365 mail design M12, Google Workspace
design G2–G4) is a **shared mailbox**, sent from with the application's own credential
through `contracts.ChannelMailbox`, a **many-provider slot keyed by `Key()`**
(`Deps.ChannelMailboxes`, [module boundaries](/en/contributing/module-boundaries/) rule
15). A channel is served **only by the provider filled under its own `provider`**, and
by none when no provider is filled under it: never by a provider under another key.
Each accessor in `contractscalls.go` takes the channel's provider and reads the slice
with `channelMailboxFor(provider)`, which calls nobody. Communications is the authority
on which mailboxes are channels; a provider sends only from a mailbox its `Check`
resolved and recorded — a shared one. For Microsoft 365, someone with `entra:manage`
turns it on in the Entra module and an Exchange administrator scopes it ([Mail
channels](/en/admin/entra/#mail-channels)); for Google Workspace, an Owner turns on
Mail channels in the Google module and a super admin delegates the two Gmail scopes
([Mail channels](/en/admin/google-workspace/#mail-channels)). Google has no shared mailbox:
its "shared" mailbox is a licensed user that is no person's account in Staff.

- **The row.** `provider` is the provider's key; `channels.settings` (migration
  `00091_communications_channel_settings.sql`, `jsonb`, `{}` by default) is
  `{"mailbox": "<mailbox id>", "onClose"?: "read"|"move", "folder"?: "<name>"}`, the
  provider's id of the mailbox (Entra's object id, in lower case; Google's Directory user
  id, digits) and the optional
  [state on close](#outlook-state-on-close); and there is **no `channel_credentials`
  row** — no secret to seal. **One channel per mailbox, per provider**:
  `ux_channels_provider_mailbox` on `(provider, settings->>'mailbox')` `WHERE settings ?
  'mailbox'` (migration `00111_communications_provider_neutral.sql`, replacing 00091's
  `ux_channels_entra_mailbox`), so two aliases of one mailbox cannot make two channels,
  while the same id under two providers names two mailboxes; its violation is 409
  `mailbox_exists` (*Another channel already uses this mailbox.*), apart from
  `channel_exists`, an address another channel holds.
- **The answer.** `ChannelResponse.mailbox` is `{id, onClose: "none"|"read"|"move", folder: string|null}`
  for a mailbox channel and null otherwise; `settings`, the SMTP shape (`host`,
  `port`, `useSsl`, `username`), is null for it, and `hasCredentials` false.
  `available` is whether a channel can send now: an SMTP channel has stored credentials; a
  mailbox channel has a mailbox and its provider is offered (a provider filled under the
  channel's key, and its `Status` available — each filled provider's `Status` is read once
  per list). The list, the read and the update answer it with the
  provider's state read at that moment (outside any transaction); a create or a switch
  answers it from the row alone, the provider having just been asked.
- **Every call of the slot is made outside a transaction** — before it opens or after it
  commits — through the accessors in `contractscalls.go`, which refuse one made inside
  (a programming error, answered as a 500).
- **The import window.** Creating a mailbox channel, and switching an SMTP one,
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

`POST /channels` with `{type: "email", provider: "<a listed key>", address, displayName?,
isDefault?, importWindow?, onClose?, folder?}` and no `smtp` block (one is a 400 under
`smtp`; `importWindow`, `onClose` and `folder` on an `smtp` create are a 400 too).
`importWindow` is `from_now` (default), `last_30_days` or `last_90_days`; `onClose` is
`none` (default), `read` or `move`, and when `onClose` is `move`, `folder` (1 to 120
characters, no `/` or `\`, defaulting to `"Handled in Vantigo"`) is ensured in the shared
mailbox by calling `EnsureFolder` before the transaction. A provider key no module fills
is 409 `mail_channels_off`, named by its key, and nothing reaches a provider. Before the
transaction, the named provider's `Status`, then `Check(address)` (and `EnsureFolder` when
`onClose` is `move`):

| What the provider answers | Result |
| --- | --- |
| its `Status` not available | 409 `mail_channels_off` |
| a shared mailbox it reaches | 201, the channel **active**; its address is the mailbox's primary SMTP address as `Check` read it, in lower case, so an alias typed becomes the mailbox's own (Google's too: an alias resolves to its user) |
| a mailbox it reaches, not shared | 422 `personal_mailbox` (*The address is a person's own mailbox in {provider}. Only a mailbox that is no person's own can be a channel.*) — `Check` does not record a personal mailbox, so the provider would refuse to send from it anyway |
| `ErrMailboxGroup` | 422 `group_address` (*The address is a group. A group has no mailbox Vantigo can read; use a licensed mailbox.*) — a provider whose groups have addresses of their own answers it: Google for a Google Group's address, a Collaborative Inbox included; Entra never does |
| `ErrMailboxDenied` | 422 `mailbox_denied` (*{provider} does not let Vantigo use this mailbox. An administrator gives the access as the mail channels section of the {provider} administration guide describes.*) — outside the application's RBAC scope, or a role missing; for Google, a 403 on the mailbox that is not the delegation |
| `ErrMailboxUnknown`, `ErrMailboxGone` | 422 `mailbox_unknown` — no such mailbox, or an address that is not a bare address (a display name, white space, one of `/ \ ? # %`, over 320 bytes) |
| `ErrMailboxUnavailable` | 409 `mail_channels_off` |
| anything else — throttled, the provider not answering, the status unreadable | 503 `provider_unreachable` (*{provider} did not answer. Try again.*), logged |

The sentences name the provider by its `DisplayName()`: `mail_channels_off` says *Mail
channels through {provider} are not available. An administrator turns them on under
Integrations.*, `mailbox_unknown` *No mailbox with this address was found in
{provider}.* A channel whose provider is no longer filled (verify) is named by its key.
The address a person typed is never in an error or a log line: Entra names the lookup's
path `/v1.0/users/{address}`, Google its `/admin/directory/v1/groups/{group}` and
`/admin/directory/v1/users/{user}`.

The insert then runs as an SMTP channel's does — the default slot's lock, the first
channel made the default — with `settings` and no credential row. An address another
channel holds is 409 `channel_exists` as for SMTP; a mailbox another channel uses, 409
`mailbox_exists`.

#### Switching provider

`POST /channels/{id}/provider` with `{provider: "<a listed key>", importWindow?, onClose?, folder?}` (`channels-manage`) moves an
SMTP channel to a mailbox provider and keeps its **id, address, conversations and default
flag**. `smtp`, or no provider, in the body is a 400; an unknown channel a bare
404; a channel already on that provider 422 `provider_unchanged` (*The channel already
uses {provider}.*). **A channel on another mailbox provider is refused**, 409
`provider_switch_unsupported` (*The channel uses another mailbox provider. Switch it back
to SMTP first, then to the new provider.*): the channel goes back to SMTP first, so its
old mailbox is forgotten on its own provider before a new one is checked. Both are
checked again under the lock. `Status` and `Check` (and
`EnsureFolder` when `onClose` is `move`) run on the
channel's own address before the transaction, with the create's table; a mailbox another
channel uses is 409 `mailbox_exists`. The channel **keeps its own address**, which may be
an alias of the mailbox rather than its primary address: the mail then goes out `From:`
the alias through the shared mailbox — whether Exchange sends it so, rewrites it or
refuses it is **not verified on a real tenant**. Then, under the
default slot's lock and a re-read, the provider and `settings` are written and the
**credential row deleted, in one transaction**: a failure leaves the channel SMTP with its
credential. Answers 200 with the channel. The mailbox `Check` recorded on the provider's side
stays recorded if that transaction fails; it only lets the provider send from a mailbox
an admin checked, and the next `Check` rewrites it.

**Back to SMTP** is `PUT /channels/{id}` with `provider: "smtp"` and a full `smtp` block,
**the password included** — there is no stored one to reuse (*A password is required to
switch a channel to SMTP.*; without a block, *SMTP credentials require a host and valid
port.*). It writes the credential row, sets the provider to `smtp` and `settings` to
`{}`, and **after the commit** calls the channel's own provider's `Unwatch` and `Forget` for the old mailbox,
best-effort: a failure is logged and the 200 stands. The channels are read again first,
and `Unwatch` and `Forget` are skipped when another channel on that provider has taken the mailbox in between, so that
channel can still send and receive. The channels page has no control for
it; it is the API's. A `PUT` without `provider` keeps the channel's own, so `isActive`,
`isDefault`, `displayName`, `onClose` or `folder` alone on a mailbox channel need no
credential (when `onClose` is set to `move`, `EnsureFolder` runs before the transaction;
when `isActive` is set to `false`, `Unwatch` runs best-effort after the commit); a `PUT`
naming a mailbox provider other than the channel's own is a 400 under `provider` (the switch is the `POST`).
A `PUT` whose channel changed provider between its read and its transaction is a 400
under `provider`, to read the channel again.

**There is no delete of a channel** — conversations and participants restrict it — so a
deactivated mailbox channel keeps its mailbox, and sends again once activated
with no new `Check`.

#### Verifying

`POST /channels/{id}/verify` on a mailbox channel runs its own provider's `Status` and
`Check` of its address again: 200 `{ok: true}` only when it still reaches a shared mailbox
**with the id the channel holds**; otherwise 422 with the create's codes —
`mail_channels_off` (also when no provider is filled under the channel's key any more),
`personal_mailbox`, `group_address`, `mailbox_denied`, `mailbox_unknown`,
`provider_unreachable` — or `mailbox_changed` when the address now names another mailbox.

#### The rate limit

Creating, switching and verifying a channel each reach the provider up to three times for a
mailbox channel, so the three share one bucket, `communications-channel-check`, **30
a minute per client address** — SMTP channels included. A refused request is the
router's 429.

### Customer mail from other modules

Invoices sends documents and reminder letters, and Calendar its invitations to outside
attendees, **through a channel an Owner chose** — never on the installation's `SMTP_*`
credentials (integrations design T4–T6). The seam is `contracts.OutboundMail`, a
single-provider slot Communications fills through `Module.OutboundMail` and Compose and
Workers resolve into `Deps.OutboundMail`, refusing two ([module
boundaries](/en/contributing/module-boundaries/) rule 17). It is nil when Communications
is off, which a consumer reads as *sending is disabled*.

| Call | What it does |
| --- | --- |
| `Channels(ctx)` | The active channels of the mail type, oldest first, each as `{id, address, displayName, provider, providerDisplayName, available}`. No network call, and no Communications permission: names and addresses only. |
| `Channel(ctx, id)` | One channel, or `ErrChannelUnknown` when no active channel of the mail type has the id. |
| `Send(ctx, id, out)` | Sends `out` from the channel, synchronously, under the caller's context. |

`available` is the channel's own state: an SMTP channel has stored credentials; a
mailbox channel has a mailbox and the provider filled under its key is offered.
`providerDisplayName` is that provider's `DisplayName()`, its key while none is filled —
except `entra`, the one provider channels had before there were many, which is then
*Microsoft 365* (the admin's channel list names it so too). `Send` refuses a channel
that cannot send before it builds or dials anything.

- **From and the rest.** The message is sent From the **channel's address**; the caller's
  `DisplayName` is the name beside it, and its `Reply-To`, `Message-ID` and attachments are
  kept. An SMTP channel sends through the same seam the outbox uses (`Deps.SMTPSend`,
  else `mail.SendOutbound`) over its own sealed credentials; a mailbox channel
  renders the message as MIME from the channel's address and hands it to the `Send` of
  the `ChannelMailbox` filled under its provider.
- **Errors.** `ErrChannelUnknown`; `ErrChannelUnavailable` — the provider is off, the
  mailbox is outside the provider's scope or gone (`ErrMailboxUnavailable`, `Unknown`,
  `Denied`, `Gone`), or an SMTP channel has no credentials (the wrapped sentence is *the
  channel has no credentials*); `ErrMailTooLarge`, from the mailbox provider's
  limit; `ErrRecipientRefused`, defined with the slot for a provider that holds a tenant
  to a list of addresses and answered by none yet; and any other error is a failure to
  retry — at least once, as SMTP.
- **Synchronous, never queued.** The outbox stays Communications' own conversations';
  every consumer keeps its evidence and its retry (Invoices the delivery row and the
  letter's attempts, Calendar the attendee's *Resend*). `Send` is made outside any
  transaction, and the consumers' accessors refuse it inside one.
- **A channel that goes away.** A consumer stores a channel's id with no foreign key
  across schemas; deactivating a channel makes `Channel` answer `ErrChannelUnknown`, and
  the consumer shows its setting as *gone* and behaves as disabled until an Owner chooses
  again. Communications never writes a consumer's settings.

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
- **anything else**: through the `ChannelMailbox` filled under the channel's provider. A
  channel whose `settings` name no mailbox fails with `channel_misconfigured`; no provider
  filled under the channel's key is `ErrMailboxUnavailable` and **held** (below) — a
  channel is **never sent through another provider**. The same `Outbound` is rendered as MIME
  by [`mail.RenderMIME`](/en/contributing/platform-packages/#the-mail-package) from the
  channel's address — with a `Bcc:` header only when there are blind copies — and handed
  to `Send` with the blind copies beside it, under a timeout scaled to the provider's
  `MaxMessage`: the SMTP send's 30 seconds and 2 more for each megabyte, at most three
  minutes — 36 seconds for Entra's 3 MB, 80 for Gmail's 25 MB. Before such a send the
  job's lease is moved on to twice the timeout (`ExtendOutboxJobLease`, guarded by the
  lease id like the stamp), so the send never outlives the lease protecting it.
  Entra posts it to Graph's `sendMail` as base64 MIME, or, with blind copies, creates it
  as a MIME draft in the shared mailbox, checks the draft kept every blind copy, and sends
  the draft ([Entra's Send](/en/reference/entra/#send)). Google uploads the MIME whole to
  Gmail's `messages.send` as the mailbox, blind copies in its `Bcc:` header, which Gmail
  delivers and strips ([Google's Send](/en/reference/google/#send)). None is retried by
  the provider's client on a 5xx. Graph's **202 is acceptance**, as SMTP's 250: the deliveries become
  `relay_accepted`, and the at-least-once rule, the stamp and the possible-duplicate
  metric are unchanged. Exchange saves the sent message in the shared mailbox's **Sent
  Items**, Gmail in the mailbox's **Sent**.

**What the provider answers of a sent message** (Google Workspace design G5). A
provider's `Send` — a channel's and a [personal one](#sending-from-ones-own-address) —
answers `contracts.SentMessage{MessageID, ThreadID}`: the Message-ID the message left
with as the provider sent it, and the provider's thread id, each empty when the provider
does not know it. Entra answers the zero value (Graph's `sendMail` answers no body);
Google answers Gmail's `threadId` and the Message-ID Gmail sent the message with, read back
after the send, which may not be ours (U6 in the Google Workspace design). The
outbox records a non-empty answer on the message in the completion's own transaction:
`ThreadID` as `channel_metadata_json->>'providerThreadId'`, and a `MessageID` that differs
from the message's own (bracketed, as `rfc_message_id` is) as
`conversation_messages.sent_message_id` (`varchar(998)`, indexed, migration 00111). Both
are what [threading](#threading) reads, so a reply to a message whose Message-ID the
provider replaced still threads. SMTP answers nothing beyond its 250.

**Not yet verified on a real tenant**: whether Exchange keeps our `Message-Id` on a MIME
`sendMail` (no Microsoft page says so; Outlook is reported to stamp its own), and whether
a draft's `Bcc:` header reaches the blind copies as Graph documents for a MIME draft, nor
how Exchange treats a channel switched from SMTP whose address is an alias. The
pull request's real-tenant checklist reads the sent item's `internetMessageId`, the
recipient's raw headers and a blind copy's arrival. If Exchange rewrites the
`Message-Id`, a customer's answer names Exchange's own in `In-Reply-To`, which no message
of Vantigo's has, and [threading](#threading) falls back on the provider's thread — with
the limit stated there.

**Terminal failures.** A send through the mailbox that fails with one of these codes
**fails the job at once**, after one attempt, rather than at the eighth: no retry can
mend it — `provider_unavailable` aside, which ends a day's hold (below).

| Code | From |
| --- | --- |
| `mailbox_denied` | `ErrMailboxDenied`: the mailbox is outside the application's scope, or a role is missing |
| `mailbox_gone` | `ErrMailboxGone`: the mailbox no longer exists — for Google, also a user suspended or without Gmail since |
| `mailbox_unknown` | `ErrMailboxUnknown`: no `Check` recorded the mailbox on the provider's side (forgotten, say) |
| `too_large` | `ErrMailTooLarge`: over `MaxMessage`, Graph's or Gmail's 413, or Exchange's 400 `ErrorMessageSizeExceeded`, `MessageSizeExceeded` or `ErrorMessageTooBig` (the mailbox's own send limit below Graph's) |
| `bcc_unsupported` | `contracts.ErrBccUnsupported`: the draft did not keep every blind copy, so nothing was sent — a retry would build the same draft |
| `channel_misconfigured` | the channel's `settings` name no mailbox |
| `channel_unavailable` | an SMTP channel with no credential row: nothing to send with, and no fall-back to the installation's SMTP |
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

A retention cycle runs on **one connection**, the one its lease holds: the staged-upload
expiry, every batch's transaction and the receipts' sweep. It never asks the pool for a
second connection while it holds the first — as many lease workers doing that as the pool
has connections deadlock it, and readiness with them — and it calls no other module.

### Background workers

The module contributes five background workers: outbox delivery, retention, attachment
cleanup, `communications-mailbox-sync`, which reads each mailbox channel's Inbox
([Receiving](#receiving)), and `communications-messenger-sync`, which reads each Messenger
channel's inbox of events ([The messenger sync worker](#the-messenger-sync-worker)).
`worker` mode always runs them, `server`
mode never does, and `api` mode runs them when `WORKERS_IN_PROCESS=1` (the default).
A deployment that scales the API horizontally should set `WORKERS_IN_PROCESS=0` on
the API replicas and run one dedicated `worker` container, so extra HTTP replicas do
not multiply the pollers.

### Metrics

Six counters on an OpenTelemetry meter named `Vantigo.Communications`, none with
attributes. Five are the outbox's: `communications.outbox.possible_duplicate_sends`,
`.jobs_completed`, `.jobs_retried`, `.jobs_held` (a try of a mailbox channel's job
put back while its provider is unavailable, spending no attempt) and `.jobs_failed`.
**Alert on the last one: every increment is undelivered customer email.** The sixth is
the mailbox sync's and the messenger sync's, `communications.inbound.cycles_failed`: one per cycle whose failure was
written on its channel — a refusal (`mailbox_denied`, `mailbox_gone`, `mailbox_unknown`)
or any other failure to read or import (`sync_failed`) — and not a cycle held for an
unavailable provider, nor one that lost its lease. A rise means a channel's mail is not
arriving. There are deliberately no others — no retention, cleanup, storage, SMTP or AI
metrics, and no histograms.

## Live updates

The Inbox and an open conversation update by themselves through the platform's
[live updates](/en/contributing/platform-packages/#live-updates). The module registers two
topics through `Module.LiveTopics` (`internal/communications/live.go`) and notifies them from
the writes below. Events carry identifiers only; a screen that hears one refetches through
the ordinary endpoints, which check the permission as they always do.

| Topic | Authorised by | Events | Presence |
| --- | --- | --- | --- |
| `communications.inbox` | `communications:conversations-view` | `conversation.changed`, with the conversation's id, or without one after a bulk change | not allowed |
| `communications.conversation:<uuid>` | `communications:conversations-view`, and the conversation exists | `conversation.changed` (status, assignment, tags, customer link and suggestions), `message.added`, `message.changed` (a delivery's outcome), each with the id of what changed (empty when several messages changed) | allowed; the thread announces `viewing` and `replying` |

A key that is not a UUID, a conversation that does not exist and one the person may not see
are all `forbidden`, so a subscription tells nothing about which conversations exist.

### Where the module notifies

A write notifies on its own transaction, so the event is heard only when the transaction
commits. Each write notifies the inbox and the conversation's topic, except where stated.

| Trigger | Where (`internal/communications/`) | Event |
| --- | --- | --- |
| Mail received into a mailbox channel | `inbound_import.go` | `message.added` |
| A Messenger message received | `messenger_import.go`, `importMessage` | `message.added` |
| A Messenger delivery or read mark applied | `messenger_import.go`, the watermarks | `message.changed` |
| A conversation started from the Inbox | `conversations_create.go` | `message.added` |
| A reply queued | `conversations_reply.go` | `message.added` |
| A failed delivery retried | `conversations_retry.go` | `message.changed` |
| An internal note added | `conversations_notes.go`, on the pool | `message.added` |
| Status, assignment or another field changed | `conversations_patch.go` | `conversation.changed` |
| A tag added or removed | `tags.go`, on the pool | `conversation.changed` |
| An AI customer suggestion saved on the conversation | `ai.go`, on the pool | `conversation.changed` |
| The outbox claims, completes, fails or cancels a job | `outbox.go`: `claim`, `complete`, `markFailed`, `cancelSuppressed` | `message.changed` |
| Customers merge, so conversations are re-pointed | `customer_references.go` | `conversation.changed` on the inbox, once, without an id |
| A customer's data erased | `customer_personal_data.go`, on Customers' transaction | the same, once |
| Retention deletes closed history | `retention.go` | the same, once per batch |

Marking a conversation read notifies nothing: the read state is the caller's own, and the reader's client refreshes its own list. A write on the pool, outside a transaction, logs a failure to send and goes on: the write is
made, and the screen's fallback refresh repairs a lost event. A bulk path sends **one**
event without an id and never one per row, because every notifying commit takes a
cluster-wide lock. A change not in this table, such as the linker choosing a customer
automatically or a mailbox sync giving up, is seen at the screen's next refresh. A staged attachment's scan status has no event either, so the composer keeps its own 2 second poll whether or not the connection is live.

A screen polls every 60 seconds (the list, and the open conversation) only while its topic is not live. See
[Live updates and your proxy](/en/admin/installation/#live-updates-and-your-proxy) for the
operator's side.

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

**A mailbox channel's size.** Graph takes a message posted whole up to about 4 MB
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

A **Google Workspace** channel's budget is Gmail's 25 MB, **25 000 000 bytes of MIME**
(the upload endpoint takes up to 35 MB, U10), so its attachments are **18 200 000** bytes
and the composer says about 18.2 MB; the same staging and reply checks apply.

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

A mailbox channel receives (Outlook connection and Microsoft 365 mail design M14,
M15, M16, M17): mail arriving in its shared mailbox's **Inbox** becomes an inbound message of a
conversation, threaded onto the one it answers or starting one, its sender linked to a
customer when the address belongs to exactly one. Vantigo reads the mailbox with the
application's credential through the two reads of the `ChannelMailbox` filled under its provider, `Delta` and `Raw`
([Entra's inbound reads](/en/reference/entra/#delta-and-raw), [Google's](/en/reference/google/#delta-and-raw)), subscribes the Inbox to
change notifications when configured (`Watch`, `Unwatch`, `Notified`), and leaves the
messages in the Inbox untouched until a conversation is closed on a channel configured to
mark or move them ([Outlook state on close](#outlook-state-on-close)). An SMTP channel does not receive.

### The mailbox sync

`communications-mailbox-sync` is a worker of this module. Every **30 seconds** it drains:
it first reads any new mail change notices from the provider, then claims a channel due,
runs one cycle on it, and claims the next — or, when no channel sync is due, claims the
next due [on-close mark job](#outlook-state-on-close) — until none is due.

- **Change notices (`takeNotices`).** Before claiming, the worker asks every filled
  provider's `ChannelMailbox.Notified(since)`, each from **its own** in-memory cursor
  (kept by the provider's key) initialised to one minute before the worker started. Each
  notice's mailbox id marks the `channel_sync` row of the active channel on that provider
  with that mailbox `notified = true` and `next_sync_at = now` when the row is not
  backing off a failure (`attempts = 0` and `state <> 'failed'`), so a notified channel is
  claimed next.
- **The claim.** A `channel_sync` row is due when its channel is an **active** mailbox
  channel (any provider but `smtp`), its `state` is not `stopped` (nothing in this release sets `stopped`), its
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
- **The provider first.** No provider filled under the channel's own key, a
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
  ahead as a safety net; otherwise — notifications off, `PUBLIC_BASE_URL` unset, `Watch`
  answering `contracts.ErrWatchUnsupported` (a provider that offers no notifications,
  Google's; quietly), or `Watch` failing otherwise (logged at warn) — `next_sync_at` is
  **five minutes** ahead. Every
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
autoSubmitted, providerThreadId}`, plus `unreadable` and `htmlDropped` when set: the
provider's thread id (`MailItem.ConversationID`, Graph's `conversationId`) under a
neutral key — migration 00111 rewrote `graphConversationId` to `providerThreadId` in
every existing row —, the
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
   direction**, by its own Message-Id (`rfc_message_id`) **or the one its provider sent it
   with** (`sent_message_id`), so a customer's answer to a reply Vantigo sent threads onto
   its conversation even when the provider replaced the Message-ID: the outbox's
   Message-Ids are stored bracketed, as received ones are.
2. Else the conversation of a message its **`References`** name, the newest reference
   first, matched the same way.
3. Else the conversation of the newest message **of either direction** in the same
   **provider thread** (`providerThreadId`: Graph's `conversationId` or Gmail's `threadId` on a received message,
   the `ThreadID` a provider answered on a sent one) in the last **90 days**, through
   `ix_conversation_messages_provider_thread` (00111, replacing 00092's inbound-only
   index).
4. Else a new conversation.

Several messages with one Message-Id answer the newest's conversation. The match is
re-checked in the import's transaction (`archived` starts a new one, `closed` is reopened
unless the message is automatic).

**If Exchange rewrites our Message-Id** on sending (not verified, [Delivery
semantics](#delivery-semantics)), a customer's answer to Vantigo's reply names Exchange's id,
which no message of Vantigo's has, and step 3 threads it when the conversation already
holds a message of the same provider thread — an inbound one of the same Outlook
conversation, the usual case, a reply to a customer who wrote first, or a sent one whose
provider answered its thread. Entra answers none, so a customer's first answer to a
conversation **Vantigo began** (`POST /conversations`) through Microsoft 365 has no such
message, and starts a new conversation. Google answers its thread and the Message-ID Gmail
sent with, so a Gmail channel's conversation threads by step 1 or step 3 either way.

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

A mailbox channel's `settings.onClose` decides what happens in the shared mailbox in
Outlook when a conversation on the channel transitions to `closed`
(`PATCH /api/v1/communications/conversations/{id}` with `status: "closed"`):

- `none` (the default): nothing is changed in Outlook.
- `read`: every inbound message of the conversation is marked as read in Outlook
  (`isRead: true`).
- `move`: every inbound message of the conversation is moved to the channel's configured
  top-level folder (`settings.folder`, `"Handled in Vantigo"` by default), ensured by
  `EnsureFolder`.

On a Google Workspace channel the same settings act on Gmail's labels: `read` removes the
`UNREAD` label; `move` adds the user label named by `settings.folder`, created by
`EnsureFolder` if missing, and removes `INBOX` ([Google's marks](/en/reference/google/#marks)).

In the close's own transaction, when the channel is a mailbox channel whose `onClose` is
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

## Messenger channels

Communications has a second kind of channel beside e-mail: **Messenger**, a chat through a
Facebook Page the business owns (Messenger design, delivery A: the groundwork, proven
against a fake provider). Nothing in Communications names Facebook or Meta: a Messenger
channel is served by whatever fills the `contracts.ChannelMessenger` slot.

### Types, providers and the default of each

`channels.type` is `email` or `messenger`; `POST /channels` takes either, and any other
type is a 400 under `type` (*Only the email and messenger channels are currently
supported.*). The address rule depends on the type: an `email` channel's address stays
RFC 5322, a `messenger` channel's is the **Page id**, 1 to 30 digits (a 400 under `address`,
*A valid Facebook Page id is required.*). A `messenger` channel takes no `smtp` block and no
mailbox options.

**The provider has to serve the type.** A `messenger` channel's `provider` is the key of a
filled `ChannelMessenger`; an `email` channel's stays `smtp` or the key of a filled
`ChannelMailbox`. Naming one that serves the other type — a messenger key for an e-mail
channel, `smtp` (the default when none is named) or a mailbox key for a Messenger one — is
**422 `provider_type_mismatch`**, on create, on `POST /channels/{id}/provider` and, as a 400
under `provider`, on `PUT /channels/{id}`; a Messenger channel's provider cannot be changed
or switched at all. A key filled in neither slot is no mismatch: it is a provider that is off,
and a create answers **409 `messenger_off`** (*Messenger channels
through … are not available.*). `module.Compose` and `module.Workers` refuse a key filled in
both slots, so a key names one kind of provider. Dispatch is by the channel's type and the slot
that fills its key: `isMailboxProvider` (anything but SMTP) is asked only of an `email`
channel's provider.

**Creating one** (`channels-manage`) checks the provider before any transaction opens,
as a mailbox channel does: the provider filled and its `Status` available (else 409
`messenger_off`), then `Check(address)` — a Page it has connected, and that is available. An
unknown Page is **422 `endpoint_unknown`**, an unavailable one **422 `endpoint_unavailable`**,
and a provider that does not answer **503 `provider_unreachable`**. The channel is stored
with the address `Check` answered, `settings` `{}`, no credential row and — when no
`displayName` was given — the Page's name as its display name, and a `messenger_sync` row at
cursor 0 is made in the same transaction. `POST /channels/{id}/verify` runs the same check
again, every refusal a 422.

**The default is the default of its type (design divergence 16).** `ClearDefaultChannels`,
`ClearOtherDefaultChannels` and `AnyChannelExists` were type-unfiltered, so the first
Messenger default would have demoted the e-mail default Invoices and Calendar send through.
All three now filter by `type`: the first channel of a type is its default, making a channel
default demotes only the others of its type, and a PUT with `isDefault` does the same.
The global advisory lock of divergences 14 and 15 stays, one slot for every writer whichever
type it writes. Conversation create with no channel named still uses the default
`email` channel, and with a Messenger `channelId` and no `participantId` is **422 `channel_cannot_start`**: the
person writes first. A request that names a `participantId` is the exception, see
[Contacts on Messenger](#contacts-on-messenger).

`GET /channel-providers` lists, after SMTP and the mailbox providers, **every filled
messenger provider**: `{key, displayName, type: "messenger", available, reason?, endpoints}`,
`endpoints` each Page the provider has connected, `{address, displayName, available,
reason?}`; every other entry carries `type: "email"` and `endpoints: null`. `GET /channels`
answers a Messenger channel's `available` as the provider offered **and** its Page available,
and its `sync` as its `messenger_sync` row's state, the code of its last failure and when it
last finished.

### The ChannelMessenger slot

`contracts.ChannelMessenger` is a many-provider slot keyed by `Key()`, filled through
`module.Module.ChannelMessenger` and collected into `Deps.ChannelMessengers` by `Compose` and
`Workers`, after the channel mailboxes (`modtest.WithChannelMessenger` presets one). Its
methods are `Status`, `Capabilities`, `Endpoints`, `Check`, `Send`, `Notified`, `Pending`,
`OpenAttachment`, `Ack` and `Profile`; every one but `Key`, `DisplayName`, `Status` and
`Capabilities` is a provider call, made through an accessor in `contractscalls.go` that
refuses it inside a transaction of the module (`errCallInsideTransaction`), as the mailbox
slot's are. **The cursor** is the provider's own insertion id, a bigserial and never a
timestamp, because webhooks arrive out of order; an event's time only orders the display.
**Attachments** cross the boundary through `OpenAttachment`, never as a storage key: the
import copies the bytes into Communications' own storage before its transaction, and after
`Ack` the provider keeps its own copy for 7 days (acknowledging deletes nothing; the provider's retention does), so a read that failed after the acknowledgement can be made again. An event is a `message` (text, attachments, an
optional answer, the participant's platform id, a time and the platform's message id; an
**echo** is a `message` marked `echo`), a `delivered` or a `read`, each of the last two
carrying a watermark. The errors are `ErrMessengerUnavailable`, `ErrMessengerEndpointUnknown`,
`ErrRecipientUnreachable`, `ErrReplyWindowClosed`, `ErrHumanAgentNotPermitted`,
`ErrMessengerRejected` and the wrapper `MessengerTemporary`.

### The messenger sync worker

`communications-messenger-sync` is a worker of its own, not a rename of the mailbox sync
(whose queries now also filter on `type = 'email'`, so a Messenger channel is never claimed by
the mailbox cycle and has no `channel_sync` row). Its state is `communications.messenger_sync`:
the `channel_id`, the `cursor`, `notified`, `next_sync_at`, `lease_id` and `lease_until`, a
`state` of `active`, `failed` or `stopped`, `last_error` and `attempts`.

**The cycle.** Every 2 seconds the worker asks each provider for `Notified(since)` and marks
the active channels on the Pages named notified and due; a channel is due every 60 seconds
regardless. It claims one due channel by a conditional update on its lease (two minutes,
renewed before each event; the notice is taken with the claim, so one that arrives during
the cycle makes the channel due at once at its end), reads `Pending(cursor, 100)` and, for
each event, outside any transaction opens each attachment through `OpenAttachment` into
storage — an object reserved first, as staging does, so a rollback leaves a
`attachment_cleanup_records` row — and reads the participant's `Profile` when the participant
is new or its name is older than 30 days (`participant_profiles` holds when). One
transaction then writes the receipt, which dedupes by `(channel_id, provider_message_id)`,
the participant, the threading, the message, its attachments and **the cursor's advance, which
the lease guards**: an import by a worker that lost its lease rolls back. The cycle ends with
`Ack(cursor)` — called every cycle, idempotent, so one whose `Ack` failed acknowledges the
next time. A `Pending` or `Ack` failure backs the channel off exactly as the mailbox sync
does (a minute, doubled per attempt, at most an hour; `sync_failed`), an unavailable provider
holds it five minutes without spending an attempt (`provider_unavailable`), and an event
Postgres refuses is given up on with a failed `import_failed` receipt while the cursor goes
on.

**Threading by participant.** A Messenger participant's `address` is the platform's id for the
person, matched exactly, and its `display_name` is the profile's. An inbound message joins the
newest **open** conversation of the channel the participant is in; otherwise it starts one,
with **no subject** — the conversation's title is the participant's name. A closed
conversation stays closed. The customer link is manual: Messenger gives no e-mail address,
so the e-mail linker does not run, except on an address shared in answer to *Ask for e-mail*,
which feeds it as a **suggestion** (the customers its contacts belong to become
`conversation_customer_candidates` of a conversation with no customer, and the one contact
becomes the participant's `suggested_contact_id`), never a link: `participants.contact_id` is
only ever set by an explicit act, see [Contacts on Messenger](#contacts-on-messenger).

**Echoes and watermarks.** An echo whose platform id is in `messenger_parts` is a message of
ours: its receipt is `skipped` (`own_message`) and nothing is imported. Any other echo is a
message sent from Meta Business Suite or another app on the Page: it is imported as an
**outbound** message with no participant and `channel_metadata_json.echo`, in the thread of
the participant it was sent to. An echo can arrive before the part that caused it has its
id recorded, so while the channel has a part whose `Send` began in the last minute and has no
`sent_at`, an echo that matches no part holds the cycle at it (the cursor stays, the channel
is due again in 5 seconds). A `delivered` or `read` watermark is a platform time. It moves every delivery of an
outbound message in the participant's conversations of the channel whose **newest part** the
platform stamped at or before it — the part's `platform_at`, which `Send` answers as
`MessengerSent.At`, else the time its `Send` began — from `relay_accepted` (and, for `read`,
`delivered`) to `delivered` or `read`, writing a message event for each. It is **not** compared
with `accepted_at`, which is written after the last part and so always later than the
platform's stamp. The newest watermark of each kind a participant has had is kept in
`messenger_watermarks`, and the outbox applies it again after a message's completion: one that
arrived while the message was still being sent found its deliveries `sending` and moved
nothing, and is not lost. The effect is monotonic and so idempotent, and goes through no
receipt, whose `outcome` CHECK is shaped by the mailbox.

### Components, answers and the window

**Components** are what a message carries beside its text (`conversation_messages.components`,
and `message_attachments.role`, `file` or `card`, default `file`): `answerOptions` (1 to 13,
the order given; a title of 1 to 20 characters), `shareEmail` (Messenger's `user_email` quick
reply) and `cards` (a carousel: a title of 1 to 80 characters, a subtitle of at most 80, up
to 3 choices of 1 to 20 characters each, and the card's image, an attachment of role `card`).
An `id` is `[a-z0-9-]{1,40}`, unique within its message, so the cards of a product each carry their own *Buy*, `buy-<card
index>` (and a second choice `second-<card index>`); the payload the platform carries is
`v1:<messageId>:<id>`. The limits are the provider's `Capabilities`, which the conversation
detail answers as `componentLimits`. The provider's `Capabilities` also carry `MaxAttachmentBytes`, answered in `componentLimits`
as `maxAttachmentBytes`, and `PublicMedia`, whether the provider fetches a card's image from
the public host. A card of a reply names `productImageId`, a product image; it is copied
(see [A product in a message](#a-product-in-a-message)) and the stored card answers
`attachmentId`, the copy. A client-sent `attachmentId` is ignored.

**Answers.** A tapped answer option or card choice comes back as a `message` event with
`answer: {messageId, choiceId}` and the title the customer saw as its text. The inbound
message stores the answer in `channel_metadata_json.answer` and the detail answers it as
`answerTo: {messageId, choiceId, title}`; the thread shows it as a chip under its question,
which marks the chosen option. A payload the server cannot place — the message is not an
outbound message of this conversation — becomes plain text and is logged at info. An e-mail
address shared in answer to *Ask for e-mail* carries `choiceId` `share-email` and is placed
under the newest message of the conversation that asked.

**The window** is open for 24 hours after the participant's newest inbound message — an
echo is outbound and does not count — and a reply made at the very instant it closes is
inside it. When the provider's `Capabilities` give a longer `HumanAgentWindow` (Meta's Human
Agent permission), the window runs to 7 days and a reply after the first 24 hours is sent
tagged. The conversation detail answers `replyWindow: {state: open | human_agent | closed,
closesAt}`, `null` for an e-mail conversation. A reply to a closed window is **422
`reply_window_closed`**, checked after the idempotency replay, so a reply that was accepted
inside the window is still answered when it is replayed outside it. The outbox checks the
window again at send time: a job that missed it fails terminally as `reply_window_closed`,
and does not retry.

### Reply on a Messenger conversation

`POST /conversations/{id}/reply` takes the same steps with the e-mail-only inputs refused or
ignored. After the channel's `channel_inactive` check, and before the idempotency replay:

| Refusal | When |
| --- | --- |
| 422 `not_supported_on_channel` | `sendFromPersonal` is true, or `replyMode` is `reply_all` |
| 400 `invalid_request`, `textBody` | the text is blank (the HTML is ignored) |
| 422 `text_too_long` | the text is longer than the provider's `MaxText` characters (2000) |
| 422 `components_unsupported` | there are components and the provider takes none — and on every e-mail channel |
| 422 `components_invalid`, with `fields` | a limit is broken: `answerOptions` (empty, over the limit), `answerOptions[i].title` or `.id`, `shareEmail` where the provider has none, `cards` (empty, over the limit), `cards[i].title`, `.subtitle`, `.choices`, `cards[i].productImageId` (missing, or an image of another product than the first card's) |
| 422 `product_image_unavailable` | a card's image is not in the catalog, or is no longer marked `communications` |
| 422 `public_url_missing` | the message has cards, the provider fetches images from the public host and the instance has none (`PUBLIC_BASE_URL` unset) |

The subject is ignored (a chat has none), `mailEstimate` is not run, and no Cc exists. The
person answered is the conversation's participant (the one whose message came last), and one
`to` delivery to the participant's platform id is written. `replyFingerprint` includes the
components: a replay with other components under the same `Idempotency-Key` is the existing
409 `idempotency_key_reused`, and a reply with none keeps the fingerprint it had. Suppressions
stay e-mail only; a person who blocks the Page shows as the terminal failure
`recipient_unreachable`.

### Sending in parts

The outbox has a messenger branch beside SMTP and the mailbox (`outbox_messenger.go`).
Messenger takes one attachment per message, so a message is sent as **ordered parts**: the
text, if any; each `file` attachment, in a stable order (when attached, then by id — the order the parts
are counted in, so a job claimed again counts them alike); then the
carousel, built from the `card` attachments and the stored cards: for a provider whose
`Capabilities.PublicMedia` is true each card carries `ImageURL`, the public media address
of its copy (and no bytes); for any other it carries the bytes. A carousel the instance can
no longer give an address — `PUBLIC_BASE_URL` was unset after the reply was accepted — fails
terminally as `public_url_missing`. The answer options and the
request for an e-mail address ride on the last part. A `card` attachment is never a part of
its own.

**Progress per part** is `communications.messenger_parts`, keyed by `(message_id,
part_index)` with `ON DELETE CASCADE` from the message: `attempted_at`, `platform_message_id`
and `sent_at`. Each is written in its own statement, outside any transaction —
`attempted_at` immediately before `Send`, the platform's id and `sent_at` immediately after.
The job-level marker `delivery_attempted_at` is not stamped on this path. **A job claimed
again skips the parts already sent.** A part with `attempted_at` and no `sent_at` is the
per-part possible duplicate: it is logged, counted in
`communications.outbox.possible_duplicate_sends`, and sent again. **The lease** is renewed
by a conditional update on `lease_id` before every part after the first of a run; a renewal
that finds the lease taken stops the job without sending or writing a failure, and the new
holder carries on from the parts recorded. Each `Send` has the `outboxSendTimeout`
(30 seconds). When every part is sent, the e-mail path's completion runs: the job is
`completed`, every delivery `relay_accepted` with a `relay_accepted` event.

The provider's errors map onto the outbox's outcomes:

| Error | Outcome |
| --- | --- |
| `ErrRecipientUnreachable` | terminal, `recipient_unreachable` |
| `ErrReplyWindowClosed` | terminal, `reply_window_closed` |
| `ErrHumanAgentNotPermitted` | terminal, `human_agent_not_permitted` |
| `ErrMessengerRejected` | terminal, `rejected` |
| `ErrMessengerUnavailable`, or no provider filled | held: back to retry without spending an attempt, failed as `provider_unavailable` 24 hours after it was queued |
| `MessengerTemporary`, or any error the contract does not name | retried on the backoff, up to 8 attempts |

A terminal code is carried by the failure event and shown as the delivery's error; the
job's `last_error` stays the constant `Outbound delivery failed.`.

### A product in a message

A message of cards shows one product (Messenger design N16). The composer's picker reads
Products' own API with the user's permissions; the server only sees the `productImageId`
of each card.

**The copy, at reply time.** After the refusals and the idempotency replay, before the reply
transaction, for each card Communications:

1. opens the image through `ProductCatalog.OpenProductImage` and looks its product up with
   `ProductImages(productID, "communications")`; an image that is not there, or whose
   content type is not `image/png` or `image/jpeg`, is 422 `product_image_unavailable`, and images of two products are 422 `components_invalid` under
   `cards[i].productImageId`. Like the rest of the catalog these calls check no user
   permission, and being calls out of the module they are never made inside a transaction;
2. writes the `attachment_cleanup_records` row for the new object (`cards/<conversation>/<id>`)
   in its own commit, then stores the bytes (at most 8 MiB) in Communications' storage;
3. the reply transaction creates a `card` attachment for each, marks the object `owned`, and
   keeps the product id in the message's `channel_metadata_json.productId`.

A reply refused after the copy — a staged attachment that is not ready, a suppression, a
failed commit — queues the copies for deletion, so no object is left owned by nothing. The
message keeps what was sent, even when the product's images change or are deleted later, and
the card's image is downloaded through the ordinary attachment download (the thread lists it
for the carousel, never as a file). `replyFingerprint` includes each card's `productImageId`.

**A Buy answer.** The conversation detail resolves an answer to a choice of a card of a
message that kept a product id: `answerTo.product` is `{productId, name, attachmentId}`,
the title and the image of the very card the choice stands on, since each card's choice id is
its own. The frontend shows it as *Wants to buy: …* for a `buy-<n>` choice and, where Orders is present and the user may create orders, a *Create order*
link to `/orders/orders/new?customerId=…&productId=…`. No order is made automatically.

**The public media route.** Meta's generic template takes an `image_url` that Meta fetches,
and storage has no public address by design. `GET` and `HEAD`
`/api/v1/communications/public-media/{token}` is anonymous, on the public host
(`security.PayRoutes()`, as exactly one segment under GET and HEAD) and rate limited to 600
requests per client address in 10 minutes (`communications-public-media`).

- **The token** is `<attachmentId>.<expiresAt>.<mac>`: the attachment's UUID, the expiry as
  Unix seconds, and the unpadded base64url of the secrets Box's `MAC` under the purpose
  `communications/public-media` over `<attachmentId>.<expiresAt>`, compared in constant time.
  The outbox mints it when it sends the carousel — a retry mints a new one — and the address
  is `PUBLIC_BASE_URL` followed by the route and the token. It holds for 7 days
  (`publicMediaTokenLifetime`). **That expiry is pending a live check** (the design's delivery
  0) of whether Meta keeps a template's image once it has accepted the message; if it does
  not, a conversation scrolled back to after a week shows the carousel from Vantigo's own
  copy, but Meta's own client may not, and the constant is the one thing to change.
- **What it serves**: only a `card` attachment of an *outbound* message on a *Messenger*
  channel whose provider's `Capabilities.PublicMedia` is true, answered with the image's own
  content type, `Content-Length`, `Referrer-Policy: no-referrer`,
  `Cache-Control: private, max-age=86400` and `X-Content-Type-Options: nosniff`. HEAD answers
  the same headers without reading the object, but looks for it, so a missing one is a 404 for
  both.
- **Every refusal is the same bare 404**, never a 401: an expired, malformed, wrongly signed
  or foreign token, a `file` attachment, an inbound one, a provider that does not ask for
  public media, an object that is gone. So the route cannot be used to probe.
- **Logging**: each fetch is logged by Communications with the attachment id and never the
  token. The request log, which records every path as it is, does include it, as for
  [Calendar's feed](/en/reference/calendar/); the proxy's access log should record the path
  without the token, or not at all.

### Participants and personal data

A Messenger participant row holds the platform's id for the person and its name, as an e-mail
participant holds an address. **Communications' erasure of a customer's personal data covers
its own copies — the conversations, their messages, attachments and parts — but the
participant rows outlive it until retention's orphan sweep (`DeleteOrphanParticipants`), the
same as an e-mail participant does today.** Retention deletes messages by its window as for
e-mail, `messenger_parts` going with them, and the receipts' sweep deletes a receipt whose
message is gone.

### Contacts on Messenger

A Messenger participant can carry the Messenger identity of a contact of Customers (Messenger
design N19-N23, delivery D). **The link is `communications.participants.contact_id`**, a
reference to Customers resolved through `contracts.CustomerDirectory` and never a foreign key;
Customers stores nothing of it, since the person's page-scoped id (PSID) belongs to a Page. A
contact may be linked on several channels, one participant each, and a participant is linked to
at most one contact. A participant is never reassigned on insert (the rule of divergence 13);
linking is an explicit act of its own.

**Merge and erasure.** A customer merge moves no `contact_id`: contacts are shared between
customers and keep their ids when customers merge (Customers moves the *associations*), so
nothing here needs repointing, and the conversation's own customer is moved by the existing
`communications.conversations` holder. Erasing a customer erases its conversations as before, and
the participant rows outlive them until the orphan sweep. **The contacts its anonymisation
deletes** (the ones only that customer had) are given to the module through the optional
`contracts.ContactDataEraser`, which `customerPersonalData` implements and Customers calls right
after `EraseCustomerData`, in the same transaction: `EraseContactData(ctx, tx, contactIDs)`
unlinks every participant linked to them, clears the suggestions and referral notices naming
them and deletes the hint rows left empty — **whether or not any conversation of the person had
a customer**, which is why the conversations cannot be the way in. It reports
`communications.participantContacts`, the participants it unlinked. A contact deleted by hand in
Customers (`DELETE /customers/contacts/{id}`) is not announced, and leaves `contact_id`
pointing at nothing: **a `contact_id` that no longer resolves in `CustomerDirectory` is no link
anywhere.** The thread answers `contactId: null` for it, `ContactChannels` is only ever asked
about contacts that exist, and a ref **replaces** it (below).

**Linking by hand.** `PUT /participants/{id}/contact` with `{contactId, conversationId?}` links;
`DELETE` clears. Both need `communications:conversations-manage`. `PUT` is for a participant of a
**messenger** channel (422 `not_supported_on_channel` for an e-mail one, whose contact is the
linker's). The contact must exist in `CustomerDirectory` (422 `contact_unknown`, also when
Customers is off; **a caller with `conversations-manage` can thereby learn whether a contact id
exists, without Customers' view permission, and nothing else of it** — the reasoning being that
linking needs exactly that answer, and ids are not secrets). The customers are **found by the
server**: it lists the customers of every conversation the participant is in, and the contact has
to be one of the contacts of **each** (422 `contact_not_of_customer`), whatever the request
says. `conversationId`, when given, only has to be a conversation the participant is in (422
`participant_invalid`, the module's code for "not of this participant", also used by create);
it decides nothing else. A participant that does not exist is 404. Setting the contact the
participant has is 200 and `DELETE` is 204 whether or not there was a link. Both lock the
participant's row, replace the link whatever it was and delete the participant's hints.

**Hints.** `communications.participant_contact_hints` (migration `00139`) keeps, per
participant, what is only suggested or noted, so that it cannot be mistaken for the link:
`suggested_contact_id`, the one contact the address shared in answer to *Ask for e-mail*
names (written only while the participant has no contact), and `referral_contact_id`, below.
The conversation detail answers `messengerLink: {participantId, contactId, suggestedContactId,
referralContactId}` for a Messenger conversation (null for e-mail). `suggestedContactId` is
answered only while there is no contact, and `referralContactId` only when it differs from it. The block also has `canManage`, whether the caller holds `conversations-manage`, so the thread offers linking only to someone who may.

**The ref token.** `POST /channels/{id}/contact-links` with `{contactId}` answers `{url,
expiresAt}`, needs `communications:conversations-manage`, and refuses a channel that is not
Messenger (422 `not_supported_on_channel`), is inactive (422 `channel_inactive`) or a contact
that does not exist (422 `contact_unknown`). The url is `https://m.me/<page id>?ref=<token>`. The
token is `v1.<contactId>.<channelId>.<expiresAt>.<mac>`: the channel id in 32-character hex form,
the expiry in Unix seconds 90 days ahead, and the MAC the secrets Box's `MAC` under the purpose
`communications/messenger-ref` over the four fields before it, base64url without padding. It
holds no personal data beyond the contact id, uses only characters Meta accepts in a `ref`, and
is about 100 characters (Meta's limit is 2083). Verification checks the shape, the MAC in
constant time, the expiry and that the channel is the one the token names.

**Referrals.** The provider passes a referral as `MessengerEvent.Ref`: on a `message` event (the
referral of a Get Started postback, or of the person's first message) or on an event of kind
`referral`, which has no text and shows nothing. The messenger sync verifies the ref before the
import's transaction, asking the customer directory whether the contact exists (a directory
that fails makes the event be tried again, never dropped), and applies it **in the import's
transaction**: a participant with no contact is linked and its suggestion cleared; one with
another contact is left alone and gets `referral_contact_id`; one already linked to that very
contact changes nothing. **A ref never overrides a link** — but a link whose contact no longer resolves in Customers is none, and the ref replaces it (the sync resolves the participant's current contact before the transaction, and the replacing update only matches the stale id it read). A ref that fails its MAC, has
expired, names another channel, is not ours or names a contact that is gone is ignored and
logged at info without its value, and the message is imported as if it had none. A referral
event for a person Communications does not know yet **makes the participant** (its profile read first, outside the transaction, as a message's is) and applies the ref, so that the link is not lost to a first message that may not come; a Get Started postback with no title is such an event too. A participant with no conversation is swept by retention like any other.

**Conversation create with `participantId`.** `POST /conversations` takes `participantId` as
an alternative to `to` and `recipients`, for a Messenger participant only, with `textBody` as
the first message; `subject` is not used and no longer required by the contract (an e-mail
conversation still gets 400 without one). The refusals are 400 for a blank or over-long text,
422 `participant_invalid` (no such participant, or not of `channelId`), 422
`not_supported_on_channel` (an e-mail participant), 422 `channel_invalid` (inactive channel or
no provider), 422 `customer_invalid`, and **422 `reply_window_closed`** when the window of the
participant is closed. The window counts from the participant's newest inbound message in *any*
of its conversations (`LatestInboundOccurredAt` now reads the participant's messages, which
changes nothing for a conversation that has its own), so a conversation started from a customer
has the person's window. The conversation is the participant's newest open one, else a new one
with no subject, made with its participant link in one transaction **that first locks the participant's row**, so two creates racing for a person who has no open conversation share the one the first makes. A reused conversation takes the request's `customerId` only while it has none; a customer it has is kept and the request's is ignored; the first message then goes
through `POST /conversations/{id}/reply` itself (limits, components checks, the window at send
time, the outbox), with the request's `Idempotency-Key`. A refusal by the reply takes a
conversation made for it back. A replay of an accepted key is 200 with the original ids; the
same key with another text, **or for another participant** (the reply's fingerprint holds the conversation, so the participant is checked against it), is 409.

**`contracts.ContactChannels`.** The read Customers calls for its contact rows:
`ForContacts(ctx, contactIDs []int32) (map[int32][]ContactChannelEntry, error)`, a
sole-provider slot (`Module.ContactChannels`, `Deps.ContactChannels`, `modtest.WithContactChannels`),
nil without Communications, consumer-gated and never called inside a transaction. For each
contact it answers **one entry per active Messenger channel**, so that a contact that cannot
be linked yet can still be given its Messenger link: `{channelId, type, name, linked,
participantId, lastInboundAt, replyWindowClosesAt}`, the last three only while `linked`.
`replyWindowClosesAt` is computed by Communications from the provider's `Capabilities`
(including the Human Agent extension) and is absent once the window has closed, so a consumer
counts no hours. If two participants of a channel are linked to one contact, the one who wrote
last is the entry. **It never carries the PSID**, or any other address.

## Sending from one's own address

People whose own connection to a personal mailbox provider is active with the `mail`
feature — the Microsoft 365 Outlook connection (`Mail.Send` delegated, provider `entra`,
*Outlook*), or the Google connection (`gmail.send` on the person's own consent, provider
`google`, *Gmail*; [personal sending](/en/reference/google/#personal-sending)) — can
choose **Send from my address** on both new conversations and replies
(`sendFromPersonal: true`), on a channel of any provider.

- **The provider** (Google Workspace design G3). `contracts.PersonalMailbox` is a
  many-provider slot keyed by `Key()` (`Deps.PersonalMailboxes`). Communications asks
  **every** provider's `Status` for the caller — each answers `no_account` for a caller
  whose directory account is not its own — and uses the **first available** answer; else
  the first whose reason is not `no_account`; else `no_account`; `unavailable` when no
  provider is filled. The answer carries the provider's `Key` and `DisplayName`
  (`PersonalMailStatus.Provider`, `ProviderDisplayName`). `GET /personal-sender`, and a
  conversation's `personalSender`, answer `providerDisplayName` (*Outlook*), which the
  composer's **Send from my {provider} address** names. The chosen key is recorded as
  `conversation_messages.send_provider` (`varchar(20)`, migration 00111, backfilled
  `'entra'` for every earlier personal message), written with `from_address`; the
  outbox sends through `personalMailboxFor(send_provider)` and answers
  `connection_needed` when no provider is filled under it any more — **never through
  another provider**, even one that could send. A message with no `send_provider` —
  queued by a replica older than the column, during a rolling upgrade — goes through the
  only provider filled, or, with several, the one the author's status above names (asked
  as the author); the retry below resolves it the same way.

- **Channel continuity**: The conversation always belongs to its channel (`channels.address`), regardless
  of whether an individual message was sent from the channel's shared mailbox or from an author's personal mailbox.
- **`From` and `Reply-To`**: For a message with `send_as = 'personal'` (migration
  `00095_communications_personal_sending.sql`), the `From` header carries the author's personal directory
  email address and display name (`from_address`, `from_display_name`), both recorded when the message is
  composed. The `Reply-To` header is set to the channel's address and display name as they are when the
  outbox sends; `GetConversation` answers it as `replyToAddress`, read from the channel at the time of the
  read, not stored with the message. Customer replies therefore arrive directly in the shared channel
  mailbox and thread into the conversation without reading anyone's personal Inbox.
- **Availability check**: Before beginning a transaction, the handler reads the
  providers' status as above. If personal sending is not available (`!Available`), the API returns 409
  `connection_needed`, its sentence naming the provider that answered. It also enforces the 3 MB MIME size budget (`contracts.MaxPersonalMessageBytes`, the
  same figure as a channel mailbox's), returning 422 `too_large` if exceeded. A failure to read the
  author's display name is a 500, never a message sent without one.
- **Outbox dispatch & immediate failure**: The outbox dispatches personal messages via the
  recorded provider's `PersonalMailbox.Send(contracts.AsUser(ctx, authorUserID), mime, bcc)`, and records
  what it answers as [a channel's send does](#delivery-semantics). Entra answers nothing (Graph keeps
  the Message-ID); Google answers the person's Gmail thread, recorded as `providerThreadId` but never met
  by threading, which looks within the channel, and no Message-ID — `gmail.send` cannot read it back —,
  so a customer's answer to a reply threads by its `References`, and an answer to a new conversation by
  its `In-Reply-To` only while Gmail keeps the Message-ID the outbox gave (U6). If the provider refuses due to an
  expired session, revoked token, or missing consent (`contracts.ErrConnectionNeeded`), the outbox job fails
  immediately as a terminal failure after 1 attempt (`connection_needed`, never silently falling back to the
  channel's address).
- **Retry**: `POST /api/v1/communications/conversations/{id}/messages/{messageId}/retry`
  (`communications:conversations-reply` and `communications:conversations-view`) re-queues an outbound
  message whose outbox job is `failed`: 409 `message_not_failed` otherwise, 422 `channel_inactive` when
  the channel is off. A message sent from the author's own address may be retried by its author alone
  (403 `forbidden`), and only while the provider the message was recorded with answers
  `Status(ctx).Available` (422 `personal_sender_unavailable`); a channel message may be retried by anyone the permissions allow. The
  job is re-queued first, conditionally on its status still being `failed` — a retry that loses that
  race writes nothing and answers `queued` all the same —, then its `submission_failed` deliveries go
  back to `queued` with an event each.

## AI draft and customer suggestion

Two operations ask a model for help: `POST /api/v1/communications/conversations/{id}/ai/draft`
and `…/ai/customer-suggestion`. Each is an **AI task** this module declares
(`Module.AITasks`, [the AI platform](/en/contributing/module-boundaries/#the-ai-platform)), and
each calls the platform's AI client, `Deps.AI`, with the task's key and a prompt. The module
holds no key, names no model and builds no client of its own.

| Task key | Shown to an Owner as | The model is asked |
| --- | --- | --- |
| `communications.compose-reply` | *Draft a reply* | for an editable reply, from the conversation's subject and its newest 20 incoming messages, in the tone and with the instruction the agent gave |
| `communications.customer-suggestion` | *Suggest the customer* | to name one of the candidate customers a conversation could be from, from its newest 20 incoming messages and the candidates' numbers, with a confidence |

A task is declared by the delivery that performs it, so there is no task for reading incoming
mail yet.

### Availability

Both operations answer 503 `ai_unavailable` when `Deps.AI` is nil, which it is unless
`AI_PROVIDER` (`openai`, or `openai-compatible` for a model on the installation's own network), `AI_MODELS` and
either `AI_API_KEY` or `AI_BASE_URL` are set ([Configuration](/en/admin/configuration/#ai)). The message is "Communications AI is not
available. An Owner chooses models under Workspace admin › AI models." — the second sentence
points to the page where the model of each task is chosen once AI is set up. A half-set
configuration is refused at startup; an unset one is the unavailable state, in which no client,
network client or provider dependency is constructed at all. No interaction row is written for
a 503.

The four `COMMUNICATIONS_AI_*` variables that configured the feature before are read for one
more release as aliases of the `AI_*` settings, with the rules they always had: enabled needs
the flag, the provider `openai` (any other value leaves AI unavailable rather than failing the
start) and a key, and `COMMUNICATIONS_AI_MODEL` (default `gpt-4o-mini`) is the one allowed model
until `AI_MODELS` is set. `COMMUNICATIONS_AI_MODEL` is imported once, into the model of both
tasks, by identity at its first API mount that finds no choice written
([the import](/en/admin/configuration/#the-import-of-communications_ai_model)).

### The model, and the interaction row

The model a task uses is the Owner's choice for it, among `AI_MODELS`: the client asks identity's
`AIModels` slot at the moment of the call, so a change applies to the next request in the
process that saved it and within 20 seconds elsewhere. A model outside `AI_MODELS` is refused
before any request is made.

Every call that reaches the platform client writes one `communications.ai_interactions` row, and
the row names what served it: `provider` is the client's, and `model` is the one the client
resolved for the task — also when the call then failed. A call the module refuses before the
provider (the protected-customer and too-few-candidates guards) records the model the task
*would* have used, or an empty one when the choice cannot be read. `error_summary` is the short
type name of the client's error, never its message: `aiTransportError` (no response, including
a timeout of the client's 30 seconds), `aiStatusError` (a non-2xx status), `aiDecodeError`,
`aiEmptyResponseError`, `aiModelUnavailableError` (the choice could not be read) or
`aiModelNotAllowedError`. A caller that disconnects mid-call is not an outage: nothing is recorded.
The row holds digests, timings and token counts and never a prompt, a context or an answer.

### What did not change

The wire and the rules around the call are as they were before the client moved: the two
operations require different permission pairs (`communications:conversations-reply` with
`…-view` for the draft, `…-manage` with `…-view` for the suggestion); their 404 shapes differ
(a coded body for the draft, none for the suggestion); and a provider failure is a 422
`ai_failed` for the draft but a 200 with outcome `failed` and null suggestion fields for the
suggestion. A suggestion below 0.70 confidence is returned and not saved.

## The Assistant

The Assistant calls this module's operations through the same API as the interface, as the
person it acts for ([module boundaries rule 18](/en/contributing/module-boundaries/)), and
each operation's mode is derived from its access rule. Two operations are marked
`x-vantigo-assistant: never` by hand, because their bodies carry an SMTP password that would
pass through the model and its provider: `POST /channels` and `PUT /channels/{id}`. Everything
behind `communications:conversations-view` is `never` already, that permission being sensitive.
