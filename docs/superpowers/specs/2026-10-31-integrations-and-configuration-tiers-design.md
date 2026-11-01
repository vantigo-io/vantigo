# Integrations, configuration tiers, outbound mail and AI models — design

The owner's decisions of 2026-10-31, taken in conversation ahead of the hosted
service: every setting Vantigo has belongs to one of two tiers, decided by who owns
it, and the screens follow. **Vantigo owns** what an instance needs to exist and what
Vantigo provisions for it: the database, the app secret, the object store, the system
mailbox at `<tenant>.vantigo.cloud` that writes to the tenant's own employees, and the
AI plan the tenant paid for. These live in the environment, are validated at boot,
and the interface never offers to edit them. **The tenant owns** what it brings and
what writes to its customers in its name: the Entra connection, single sign-on, the
mailboxes and SMTP accounts Communications sends from, and which AI model each job
uses. These live in the database, are edited by an Owner under **Workspace admin**,
and the environment never carries them. A setting lives in exactly one tier, so no
precedence rule exists.

Four things follow, and this design covers them all:

1. **An *Integrations* section of Workspace admin**, the one place the tenant's
   connections to systems elsewhere are managed. Microsoft Entra ID moves there from
   the app switcher, in four tabs; Google Workspace joins it later; the AI models page
   and the mail channels are its other occupants.
2. **Customer mail goes through a channel the Owner chose, never through the system
   mailbox.** Invoices and Calendar write to customers through the system SMTP today,
   and a Communications SMTP channel without credentials falls back to it. Both end. A
   new slot, `contracts.OutboundMail`, carries the two modules' mail through
   Communications' channels, and each module gets a setting: a channel, or disabled.
3. **Vantigo hosted mail**, a fourth channel provider beside Microsoft 365, SMTP and
   disabled, reached through a shared service of ours in the pattern of
   `vantigo-spotprices`: Mailgun behind it, consumption tracked, test tenants held to a
   list of addresses. The same service is a mail driver for the system tier.
   **Deferred on 2026-11-01** (the owner's answer 7): the service needs its own
   research before it is built. Until then the provider is *shown* in the channel form,
   greyed and marked as not yet available, and nothing behind it exists (T8–T10 stand
   as the design to pick up).
4. **AI as a platform provider with a model per task.** The key and the allowed models
   come from the environment and are shown read-only; each module declares the AI
   tasks it performs, and the Owner picks a model per task on one page.

Code references are against `main` at `5108bb4b`; `srv/` abbreviates
`apps/server/internal/`, `host/` `apps/host/frontend/src/`, `mig/`
`srv/db/migrations/`; `U/`, `A/`, `R/` the user, administration and reference
sections of `docs/src/content/docs/en/` (and `nb/` where the section is bilingual);
`boundaries.md` the module-boundaries page; **D** the Entra design's decisions, **M**
the Outlook connection design's, **S** the spot-price service design's.

## Readings

What exists, and what this design changes:

- **The areas.** `host/apps.ts:521–557` defines three areas beside the apps:
  `settings` (the person's own: Profile, Security, and *Connections* when the Entra
  module is on, M8), `workspace` (Overview, Users, Invitations, Roles & access, the
  first three `ownerOnly`) and `admin` (System admin, one page, the maintenance
  controls). An area's `NavItem` may carry `ownerOnly`, `requiredPermissions`,
  `capability` and `module` (`host/navigation.ts:22`), so a section item gated by a
  module and a permission is already expressible: Settings › Connections is one.
  `host/routes/workspace.tsx` admits Owners and authorization managers; each child
  keeps its own gate.
- **The Entra page.** One route, `/entra`, with a switcher tile, behind
  `entra:manage` (`host/apps.ts:489`). `EntraPage` (`apps/entra/frontend/src/pages/entra.tsx`,
  D15) renders the *Status* card, steps 1–6 (register, secret, permissions and *Test
  connection*, account rules, sign-in, provisioning — 5 and 6 are identity's
  `SsoSettingsCard` handed in by `host/routes/entra/index.tsx`), then the *Outlook
  connection* switch (M2), *Calendar sync* (M9), *Mail channels* (M10) and the
  *Groups* card. `stepsDone` (`lib/steps.ts`) marks 1–4 from the connection.
- **Who sends what through the system mailbox.** `mail.Sender` (`srv/mail/mail.go:30`)
  is `Deps.Mail`, built from `MAIL_DRIVER` (`smtp` | `log`, `srv/config/config.go:1385`)
  and `SMTP_*`. Identity's invitations and recovery, Staff's notices and Calendar's
  colleague notices write to *employees* through it — correctly. But three things
  write to *customers* through the same configuration: Invoices' document send
  (`srv/invoices/send.go:319`, `s.deps.Config.Mail`), the reminder letters
  (`srv/invoices/reminder_worker.go:324`; `reasonMailUnavailable` at `:102` names
  `MAIL_DRIVER`), and Calendar's invitations to outside attendees
  (`srv/calendar/invitations.go:164`, From `SMTP_FROM`). Each module gates on
  `Config.Mail.Driver == "smtp"` (`srv/invoices/meta.go:113`,
  `srv/calendar/contractscalls.go:231`).
- **Communications' channels.** `communications.channels.provider` is `smtp` or
  `entra` (`srv/communications/channels.go:33`, M12); an SMTP channel's password is
  sealed on `channel_credentials`. `GET /channel-providers`
  (`srv/communications/channel_providers.go`) lists SMTP always and Microsoft 365 while
  the `ChannelMailbox` slot is filled, each with `available`, `displayName` and
  `maxMessageBytes`; the channels page reads the list and names providers by it. The
  outbox's `smtpConfig` (`srv/communications/outbox.go:931`) **falls back to the host's
  SMTP when the channel has no credential**, the one path tenant mail can take on
  Vantigo's credentials.
- **The AI feature.** `COMMUNICATIONS_AI_ENABLED`, `_PROVIDER`, `_MODEL`, `_API_KEY`
  (`srv/config/config.go:1183`) build one `aiChatClient` for one module
  (`srv/communications/ai_client.go:112`); nil is the unavailable state and both
  operations answer 503. The interaction log (`ai_interactions`) records digests,
  timings and tokens, never text — the privacy posture this design keeps.
- **A shared service of ours.** `ENERGY_SPOT_PRICE_SERVICE_URL` and `_KEY`
  (`srv/config/config.go:947`, S3) name `vantigo-spotprices`; the module's client is
  guarded (S7: private-address exception only over `http`, host pinned, no redirects,
  bearer key never logged); `vantigo-infra` writes the two settings into the tenant's
  `.env` (S9).
- **The environment imported once.** Identity's SSO settings moved from `OIDC_*` and
  `SCIM_*` into `identity.sso_settings` at the first mount that found the row never
  written, and the variables are ignored since (`srv/identity/sso_settings.go:29`,
  D1). The pattern this design reuses for the AI model.
- **Composition.** `module.Module` declares `Permissions []contracts.Permission` and
  Compose builds `Deps.Catalog`, the composed catalog identity's *Roles & access*
  shows (`srv/module/module.go:38`). Single-provider slots (`ChannelMailbox`, rule 15)
  and many-provider slots (`IdentityProvisioners`, rule 13) are resolved into
  `module.Deps` before any mount; `Deps.HTTPTransport` is the seam every outbound
  client takes.

## Decisions

### T1 — Two tiers, by owner; a setting lives in one

| Tier | Owner | Home | Examples |
| --- | --- | --- | --- |
| **Instance** | Vantigo (the operator) | environment, validated at boot, never edited in the interface | `DATABASE_URL`, `APP_SECRET`, `APP_URL`, `MODULES`, `STORAGE_*`, the system mailbox (`MAIL_DRIVER`, `SMTP_*`, T9's service), `AI_*` (T11), `TRUSTED_PROXY_*`, every worker's poll, `ENERGY_SPOT_PRICE_SERVICE_*` |
| **Workspace** | the tenant's Owner | the database, edited under Workspace admin, never read from the environment | `identity.sso_settings`, Entra's connection and rules, Communications' channels, Invoices' and Calendar's sending channel (T6), the model per AI task (T12) |

- **No precedence.** The two tiers never hold the same setting, so nothing resolves
  between them. The one historical overlap, `OIDC_*`/`SCIM_*` against
  `identity.sso_settings`, is a one-time import (D1) and stays so; T12 adds the second
  and last import, `COMMUNICATIONS_AI_MODEL`.
- **Instance settings may be *shown*, never edited.** A workspace page may state an
  instance fact that bears on it — *AI models* shows the provider and the allowed
  models (T13), a hosted channel shows its sending domain and its test list (T8) —
  as a fact, with no field. A secret (a key, a password, a token) is never shown at
  all, in any form, redacted or not; the page says only that one is configured.
- **Capability gating follows the instance tier.** A workspace page's *options* are
  what the instance makes possible: a hosted channel is offered only when T9's service
  is configured, the AI page exists only when `AI_PROVIDER` is set. The Owner chooses
  among what Vantigo provisioned; Vantigo provisions and never chooses for the tenant.
- The tiers are written down in a new administration page, `A/configuration.md`
  (T15), which every later setting is added to.

### T2 — *Integrations*: a section of Workspace admin

A second sidebar section in the `workspace` area, after the existing one:

```
Workspace admin
  Overview · Users · Invitations · Roles & access      (as today)
Integrations                                           (nb: Integrasjoner)
  Microsoft Entra ID     /workspace/entra        module: entra, entra:manage
  AI models              /workspace/ai           ownerOnly, when AI_PROVIDER is set (T13)
```

- **Gating.** The Entra item carries `module: "entra"` and
  `requiredPermissions: ["entra:manage"]`, exactly as Settings › Connections carries
  its module (`host/apps.ts:532`) and the Entra tile its permission (`:494`). The area
  guard (`host/routes/workspace.tsx`) admits, beside Owners and authorization managers,
  a holder of `entra:manage`, so an Entra manager who is not an Owner sees
  *Integrations* alone — the shape a delegated role administrator already has with
  *Roles & access*. `visibleNavSections` hides a section whose items all hid.
- **The switcher loses the Entra tile.** `moduleApp("entra", …)` leaves `apps`; the
  Entra module stays a module (`MODULES`, its permission, its API, its schema all
  unchanged) that owns no app. `AppKey` therefore no longer includes `entra`; the
  places that resolve an app by module key (`appForKey`, the spotlight, the
  dashboard's tiles) treat a module without an app as not listed.
- **Mail channels stay where they are.** Communications' *Channels* admin page is the
  tenant's outbound mail configuration and already sits in the Communications app
  behind `channels-manage`. It is not moved: a channel is a Communications object,
  and the people who manage channels are not always Owners. The *Integrations*
  section does not duplicate it; the Entra *Outlook and mail* tab and T13's page link
  to it where a provider is turned on.
- **Google Workspace**, when it comes, is a sibling module filling the same slots
  (`IdentityProvisioners`, `CalendarSources`, `ChannelMailbox`, `PersonalMailbox`) and
  takes the item under Entra's with the same four tabs. Nothing here is built for one
  provider: the tab structure (T3) is the template.
- **Not `/admin`.** System admin is the control plane for the person who runs the
  installation (`host/routes/admin/index.tsx`): the maintenance controls, later T15's
  read-only configuration view. A tenant's connections are the Owner's and belong to
  Workspace admin.

### T3 — The Entra page in four tabs

`/workspace/entra` is a layout route with four child routes, rendered as tabs under
one `PageHeader`; the Entra frontend package exports one component per tab, and the
host mounts them with identity's cards handed in as today.

| Tab | Route | Contents (today's cards, regrouped) |
| --- | --- | --- |
| **Status** | `/workspace/entra` | the *Status* card (D15), the account counts and events, the groups kept, and one line per switch — Outlook connection, calendar sync, mail channels — on or off with a link to its tab |
| **Connection** | `/workspace/entra/connection` | steps 1–3: the app registration, the client secrets (rotation lives here for the connection's life), the permissions and *Test connection* |
| **Accounts** | `/workspace/entra/accounts` | step 4 the account rules, step 5 sign-in and step 6 provisioning (identity's `SsoSettingsCard`, pre-filled as now), the *Groups* card |
| **Outlook and mail** | `/workspace/entra/outlook` | the *Outlook connection* switch (M2), *Calendar sync* (M9), *Mail channels* (M10) with its link to Communications' *Channels* |

- **Onboarding without an onboarding page.** The index route redirects to
  *Connection* while step 1 is not done (`stepsDone(connection).register` false); once
  it is, *Status* opens and shows "*n* of 6 steps done — continue in Connection /
  Accounts" until all six are. The step numbers stay on the cards, so the
  administration guide's "step 3" still names one thing.
- **`/entra` and `/entra/` redirect** to `/workspace/entra` for good: Staff settings,
  the Communications UI and both languages' documentation link into the page by site
  path, and nothing of theirs changes in the same release.
- **The *Connections* page is untouched**: Settings › Connections remains the person's
  own (M8). The two pages are the two sides of one line — *my* accounts elsewhere
  under Settings, *the organisation's* under Workspace admin.
- **The `entra:manage` permission is unchanged.** Owner-only steps stay the Owner's
  inside the tabs (`OwnerOnly` as today); the tab bar hides nothing by role.

### T4 — The slot: `contracts.OutboundMail`

A single-provider slot Communications fills, through which a module sends one
message to a customer from a channel the Owner chose. It is the answer to "how does
Invoices send from the tenant's address" under the rule that modules never import
each other.

```go
// OutboundMail is Communications' channels as other modules send through them.
// A consumer stores a channel id it chose from Channels and sends with it;
// Communications resolves the channel's provider and sends, synchronously,
// under the caller's context. Nothing is queued on the consumer's behalf, and
// nothing is called inside a transaction (rule 17).
type OutboundMail interface {
    // Channels lists the active channels a consumer may send from: id,
    // address, display name, provider key and display name, and whether the
    // channel can send now (its provider available, its credential present).
    Channels(ctx context.Context) ([]OutboundChannel, error)
    // Channel answers one channel, or ErrChannelUnknown when it is gone.
    Channel(ctx context.Context, id uuid.UUID) (OutboundChannel, error)
    // Send sends out from the channel. The Message-ID is the consumer's and is
    // the idempotency key a provider honours (T8). Errors: ErrChannelUnknown,
    // ErrChannelUnavailable (its provider is off or its credential missing),
    // ErrMailTooLarge, ErrRecipientRefused (T8's test list), or a failure to
    // retry.
    Send(ctx context.Context, channel uuid.UUID, out mail.Outbound) error
}
```

- **Fill and resolve.** `module.Module.OutboundMail func(Deps) contracts.OutboundMail`;
  Compose and Workers resolve the enabled module's into `Deps.OutboundMail`, refusing
  two, nil when Communications is off. Consumers treat nil as "no sending" — the
  *disabled* state of T6 whether or not the Owner chose it.
- **Direct, not queued.** `Send` is synchronous because every consumer today already
  keeps its own evidence and its own retry: Invoices records the send on the
  document and the reminder's attempt (`reminder_worker.go`), Calendar marks the
  attendee for *Resend*. Communications' outbox stays its own conversations'. A
  consumer wanting a queue later gets one of its own.
- **Which address a mail comes From** is the channel's; the consumer's `From` on the
  `Outbound` is ignored and `Reply-To` is kept (the seller's mailbox for an invoice,
  the organiser for an invitation, as today).
- **Rule 17** joins `boundaries.md`: Communications is the authority on channels; a
  consumer names one by id and never reads `communications.*`; `Channels` and
  `Channel` make no network call; `Send` is idempotent on the Message-ID or reports
  its outcome; nothing is called inside a transaction.

### T5 — The fallback ends

`OutboxWorker.smtpConfig` no longer reads `Config.Mail`. A channel with no credential
row fails its send with "the channel has no credentials" and the channel shows
**Unavailable** in the channels list and in `OutboundMail.Channels`; verification
(`verifyChannel`) already refuses it. Nothing in `srv/communications` imports
`Config.Mail` after this. The Communications reference's *Providers* section and the
installation guide's *Email and observability* paragraph say that no channel ever
sends on the system mailbox's credentials.

### T6 — Invoices and Calendar choose a channel, or none

- **Invoices.** `invoices.settings` gains `send_channel uuid NULL` (a channel's id, no
  foreign key across schemas, rule 4). *Invoice settings* gets **Sending** —
  *Documents and reminders are sent from*: a radio of the channels
  `OutboundMail.Channels` answers, with their provider badge, and **Disabled**. Null
  is disabled. `mailAvailable()` becomes "a channel is chosen and can send now";
  `reasonMailUnavailable` reads "Sending is disabled in Invoice settings, or the
  chosen channel cannot send." The document send and the reminder worker call
  `OutboundMail.Send` with the chosen channel under the same timeouts they have; the
  EHF path is untouched.
- **Calendar.** A new single-row `calendar.settings` with `invitation_channel uuid NULL`
  (its first workspace setting; the module has none today — its *Calendar settings*
  page is the person's own feed addresses, read under `calendar:access`). That page
  gains a card, **Invitations to people outside the company** — *Sent from*: the same
  radio, shown to holders of a new permission `calendar:manage` ("Manage Calendar
  settings", category Administration, sensitive, delegable), which the card's `GET`
  and `PUT /api/v1/calendar/invitation-settings` check. Null is disabled: nothing is
  sent, nothing is marked, the appointment says invitations are off, as it does today
  when the driver is not `smtp`. The after-commit budget and the one-after-the-other
  sends stay (`invitations.go`); only the accessor changes, from
  `smtpSend(Config.Mail)` to `OutboundMail.Send(channel)`.
- **A channel that goes away.** `Channel` answers `ErrChannelUnknown`; the setting is
  shown as *Disabled — the channel no longer exists* and the module behaves as
  disabled until an Owner chooses again. Communications never writes a consumer's
  settings.
- **Permissions.** Invoices' setting is written under the rule its settings `PUT`
  already has (`invoices:access` and `invoices:manage` together); Calendar's under the
  new `calendar:manage`. The channel list a consumer shows is names and addresses,
  which `OutboundMail.Channels` answers to any module without a Communications
  permission — the way `Products` answers a catalog to Projects.

### T7 — Four providers for the tenant's mail

What an Owner may send to customers from, after this design:

| Provider | Key | Credential | Where it is set up |
| --- | --- | --- | --- |
| Microsoft 365 mailbox | `entra` | the tenant's admin consent (M10) | Entra › Outlook and mail, then a channel |
| Vantigo hosted mail | `hosted` | the instance's service key (T9) — Vantigo's | a channel; the domain and the test list are facts the channel shows. **Deferred:** shown greyed in the channel form with "Not yet available", not offered, until T8–T10 are built |
| SMTP credentials | `smtp` | entered in the channel form, sealed in the database | a channel |
| Disabled | — | — | the consumer's setting (T6), or no channel |

Google Workspace adds a fifth key later through the same `ChannelMailbox` slot.
`GET /channel-providers` lists what is available now, so the channels page, the
Invoices and Calendar radios, and the Entra *Outlook and mail* tab all read one truth.

### T8 — Vantigo hosted mail: the channel provider

> **T8, T9 and T10 are deferred** (the owner, 2026-11-01): building a mail service of
> our own needs a research pass of its own — Mailgun's EU region and DNS automation for
> per-tenant domains, where the service runs, the allowances, how events reach a
> tenant — before any of this is built. What ships meanwhile is only the greyed entry
> in the channel form (delivery C below). The three decisions stay as written, as the
> design to pick up.

A third provider in `srv/communications`, present when `MAIL_SERVICE_URL` is set
(T9), implemented on the platform client of T9 the way the Microsoft 365 provider is
implemented on the `ChannelMailbox` slot. A hosted channel has no credential row and
no mailbox id; its `address` is the From address.

- **What the channel shows** (`GET /channel-providers` and the channel itself):
  `displayName` "Vantigo hosted mail" (nb "Vantigo-driftet e-post"); `available`;
  `maxMessageBytes` from the service; and, new on the channel for this provider,
  `sendingDomain` (the domain the tenant may send as), `mode` (`production` |
  `test`), `testRecipients` (the addresses a test tenant may write to), and
  `usage` (`sent`, `allowance`, `periodEnd`). All read from the service, none
  editable here. The channel's address must be under `sendingDomain`; the form
  says so and refuses another.
- **Test mode is loud.** A send to an address outside `testRecipients` is refused by
  the service with 403 and a code; the outbox records the message as failed with
  "this instance is a test tenant and may send only to its listed addresses", the
  channels page shows the mode and the list on the channel card, and
  `OutboundMail.Send` answers `ErrRecipientRefused` so Invoices and Calendar say the
  same thing in their own words. Nothing is dropped silently, and the mode is never
  changed from Vantigo-the-application.
- **Quota.** A 429 from the service leaves the message queued with the service's
  `Retry-After` as the next attempt and "the plan's allowance for this period is
  used" on the channel; `OutboundMail.Send` answers a failure to retry. The usage
  figures are read with a short cache (one minute) so the card is cheap.
- **Idempotency.** Every send carries the message's RFC 5322 `Message-ID` as the
  idempotency key; the service answers the first outcome again for a repeat, so the
  outbox's retry neither sends nor bills twice.
- **Delivery events.** The service relays Mailgun's bounces and complaints to
  `POST /api/v1/communications/hosted-mail-events`, on the public host's list beside
  Entra's `mailbox-notifications`, authenticated by an HMAC-SHA256 of the body under
  the service key with a timestamp and a five-minute window; a message named by its
  Message-ID is marked failed with the reason, and a hard bounce blocks the address
  the way *Block an address* does (the user guide's existing section), marked as the
  service's. Rate limit `communications-hosted-events`, 600 a minute.
- **Inbound later.** The service's contract reserves receiving (T10's `inbound`); this
  design sends only.

### T9 — The platform client, and the system mailbox on the same service

A new platform package, `srv/hostedmail`, beside `mail`: the client for the service,
with no SQL and no module import, used by two callers.

- **Configuration.** `MAIL_SERVICE_URL` (the service's base URL, same shape and
  validation as `ENERGY_SPOT_PRICE_SERVICE_URL`) and `MAIL_SERVICE_KEY` (the tenant's
  bearer key, required with the URL, never logged, redacted in `redact.go`). Both are
  instance tier.
- **The guard** is S7's: the private-address exception only over `http`, the host
  pinned by a round-tripper, no proxy, no redirect, a 1 MiB cap on answers, keep-alives
  on, `Authorization: Bearer`, `User-Agent: vantigo/<version>`, 30 s per send. The
  failure sentences name the service and never quote a body or the key.
- **The system mailbox may use it.** `MAIL_DRIVER=vantigo` is a third driver in
  `mail.New` beside `smtp` and `log`: `Deps.Mail` sends identity's invitations and
  resets, Staff's notices and Calendar's colleague notices through the service, From
  `SMTP_FROM` (kept as the system address's name; `SMTP_HOST` and the rest are not
  required under this driver). A new instance then needs one service key and no SMTP
  credentials; the service applies the same test list to system mail, so a test
  tenant cannot invite an address outside it. `mailAvailable`-style checks in
  identity that read `Driver == "smtp"` become `Driver != "log"`.
- **Two callers, one client.** Communications' hosted provider (T8) and the `vantigo`
  mail driver share the client and its budget. The service tells them apart by the
  `X-Vantigo-Purpose` header (`system` | `channel`), which the usage figures break
  down by.

### T10 — The service's contract (`vantigo-mail`, a separate repository)

The service is ours, in the mould of `vantigo-spotprices`: one process in front of
Mailgun, one row per tenant, a bearer key per tenant, a domain per tenant. Its
contract is `openapi/mail-service.yaml` in its own repository; what Vantigo reads of
it is fixed here so the two can be built apart.

- `GET /api/v1/tenant` — the tenant behind the key: `mode` (`production` | `test`),
  `sendingDomain`, `testRecipients[]`, `maxMessageBytes`, `usage {sent, allowance,
  periodEnd}`, `inbound: false` (reserved).
- `POST /api/v1/messages` — a whole RFC 5322 message as the body
  (`message/rfc822`), `Idempotency-Key: <Message-ID>`, `X-Vantigo-Purpose`. Answers
  `202 {id}`; `403 recipient_not_allowed {recipients[]}` in test mode; `413
  too_large`; `429` with `Retry-After`; `401` on a bad key. The same
  `Idempotency-Key` within 7 days answers the first outcome.
- `POST <APP_URL>/api/v1/communications/hosted-mail-events` — what the service calls:
  `{messageId, event: bounced | complained | delivered, reason, at}`, signed as T8
  says. The service retries for a day and gives up.
- **Per-tenant domains.** The service provisions `<tenant>.vantigo.cloud` in Mailgun
  for every tenant at creation, with SPF, DKIM and DMARC under our zone, so one
  tenant's reputation is its own. A tenant's *own* domain is Q1.
- **The companion change in `vantigo-infra`**: `vantigo add` writes `MAIL_SERVICE_URL`
  and `MAIL_SERVICE_KEY` into the tenant's `.env` and may set `MAIL_DRIVER=vantigo`;
  `vantigo mail key` rotates gap-free (write, restart, revoke); `vantigo mail mode`
  flips a tenant between test and production in the service and nowhere else; the
  smoke test sends an invitation on a test tenant to a listed address and asserts
  one outside it is refused.

### T11 — AI as a platform provider

A new platform package, `srv/ai`, beside `mail` and `payments`: the one client for
the configured provider, the one place the key lives, no SQL, no module import.
Communications' `ai_client.go` moves into it and loses its module name.

- **Configuration, instance tier:** `AI_PROVIDER` (`openai` today; `anthropic` is the
  next, Q4), `AI_API_KEY`, `AI_BASE_URL` (optional, for a compatible endpoint),
  `AI_MODELS` — the comma-separated list of models the plan allows, the first the
  default. Unset `AI_PROVIDER` is the unavailable state: no client, every AI operation
  answers 503 `ai_unavailable` as today, the page of T13 is absent. The four
  `COMMUNICATIONS_AI_*` variables are read for one release as aliases (`_ENABLED` and
  `_PROVIDER` and `_API_KEY` map onto `AI_*`; `_MODEL` is imported by T12) and the
  log names them as "renamed; set AI_* instead".
- **The client**: `ai.Client.Complete(ctx, task string, req ai.Request) (ai.Response,
  error)`. It resolves the task's model through a resolver T12 provides, refuses a
  model outside `AI_MODELS`, calls the provider under the guard every outbound client
  has (no redirects, `Deps.HTTPTransport`, a budget), and answers the typed errors
  `ai_client.go` has today (transport, status, decode, empty) so the consumers'
  interaction rows keep their short labels. The key never leaves the package.
- **`Deps.AI`**: Compose builds one client and puts it on every module's `Deps`; nil
  when unconfigured. Communications' two operations call it with their task keys and
  keep their `ai_interactions` rows, permissions and asymmetries exactly as ported.

### T12 — Tasks declared by modules, a model chosen per task

Each module declares the AI jobs it performs the way it declares permissions, and
the Owner's choice of model per job is one table identity keeps.

```go
// contracts.AITask is one job a module does with a model.
type AITask struct {
    Key         string // "communications.compose-reply"
    Module      string // "communications"
    Display     string // "Draft a reply"
    Description string // what the model is asked to do, and with what data
    Default     string // the model the module recommends; "" means the plan's default
}
```

- **`module.Module.AITasks []contracts.AITask`**; Compose collects them from the enabled
  modules into `Deps.AITasks`, the way it composes `Deps.Catalog` from `Permissions`.
  A disabled module's tasks vanish with its permissions. Communications declares
  `communications.compose-reply` (the AI draft), `communications.customer-suggestion`,
  and, for the future reading of incoming mail, `communications.interpret-inbound` when
  that feature lands — a task is declared by the delivery that performs it.
- **The table**: `identity.ai_task_models(task_key text PK, model text NOT NULL,
  updated_at, updated_by)`. Identity owns it because identity is the always-mounted
  module that already keeps the workspace's own settings (`system_settings`,
  `sso_settings`) and because the page is an Owner's. Identity fills a new
  single-provider slot, `contracts.AIModels{ ModelFor(ctx, task) (string, error) }`,
  read through a short cache (the `sso_settings` pattern), and `ai.Client` takes it as
  its resolver: a row's model if one exists and is still allowed, else the task's
  `Default` if allowed, else `AI_MODELS[0]`.
- **The import**: at the first mount that finds the table empty while
  `COMMUNICATIONS_AI_MODEL` is set, identity writes that model for every
  `communications.*` task and logs "imported from the environment; the variable is now
  ignored" (D1's shape).
- **Endpoints** on identity's API, Owner-only: `GET /api/v1/ai/settings` — the provider,
  the allowed models, `keyConfigured: true`, and the tasks grouped by module with
  each task's `model`, `default`, `source` (`chosen` | `default` | `plan`); `PUT
  /api/v1/ai/tasks/{key}` `{model}` under `If-Match`, refusing a model outside the
  allowed list (422 `model_not_allowed`) and an unknown task (404). Audited
  `ai-task-model.changed` with before and after.

### T13 — The *AI models* page

`/workspace/ai`, under *Integrations*, Owner-only, present while `AI_PROVIDER` is set
(the item hides and the route answers not-found otherwise).

- **Facts, read-only:** the provider; the models the plan allows; "An API key is
  configured by Vantigo" — never the key, never a field for one. The page header's
  description says the plan is Vantigo's and the choices are the Owner's.
- **Tasks, grouped by module**, each a row: the display name and description, a
  select of the allowed models, and a *Default* or *Plan default* badge when no
  choice was made. One allowed model makes every select locked with the model shown.
  A module that is off is not listed.
- **Links** from each consuming feature to this page: the AI draft's 503 sentence in
  Communications gains "an Owner chooses models under Workspace admin › AI models".

### T14 — Out of scope, named

More than one AI provider at once (a provider-prefixed model id can carry it later) ·
a fallback model per task · an Owner's own AI key (the key is instance tier; a
self-hosted operator sets `AI_API_KEY`) · the hosted service receiving mail (T10
reserves it) · moving Communications' *Channels* page under *Integrations* · Google
Workspace (its own research) · the SSO card's move (Q2) · a queue for Invoices' and
Calendar's mail · a generated configuration reference (Q5).

### T15 — Documentation, as its own deliverable in each delivery

The map, by delivery (A–D below), in both languages where the section is bilingual:

- **New pages:** `A/configuration.md` — the two tiers, the rule, every instance
  setting in a table (name, default, what it decides, since which release), and for
  each workspace setting the page that edits it; listed in `A/index.md` under *Where to
  start*. `U/workspace-administration.md` gains **Integrations** (what the section is,
  who sees it, the two items) and loses nothing.
- **A:** `U/entra.md` (the four tabs, the redirect, where the page is now),
  `A/entra.md` (every "the Entra page" becomes the tab it means), `R/entra.md` (the
  routes), `U/workspace-administration.md`, `A/configuration.md` (the tiers),
  `boundaries.md` (the host's *Integrations* section as a slot modules may take an
  item in), `ROADMAP.md`.
- **B:** `R/communications.md` (*Providers*: the slot, rule 17, the end of the
  fallback), `U/communications.md` (*Set up a channel*: an SMTP channel without
  credentials is unavailable), `U/invoices.md` (*Sending a document by e-mail* and
  *Reminders*: the Sending setting, disabled), `R/invoices.md`, `U/calendar.md` and
  `A/calendar.md` (*Invitations to people outside the company*: the setting),
  `R/calendar.md`, `A/installation.md` (*Email and observability* rewritten: the
  system mailbox writes to employees only), `boundaries.md` (rule 17), `ROADMAP.md`.
- **C:** `A/configuration.md` (`MAIL_SERVICE_*`, `MAIL_DRIVER=vantigo`),
  `A/installation.md`, `A/transport-security.md` (the private-address exception now
  has two callers), `U/communications.md` (*Add a Vantigo hosted channel*: the domain,
  the test list, the usage, what each refusal means), `R/communications.md` (the
  provider, the events webhook, the codes), a new `A/hosted-mail.md` (what Vantigo
  provisions, the test mode, moving to production, rotating the key, what can go
  wrong), `ROADMAP.md`.
- **D:** `A/configuration.md` (`AI_*`, the renamed variables, the import),
  `A/authentication.md` (the AI settings leave the Communications paragraph),
  `U/workspace-administration.md` (*AI models*), `R/communications.md` (*AI draft and
  customer suggestion*: the task keys, `Deps.AI`), a new `R/platform.md` section or
  `contributing/platform-packages.md` paragraph for `ai` and `hostedmail`,
  `boundaries.md` (the `AIModels` slot, `AITasks`), `ROADMAP.md`.
- `Docs-Impact: none` only on plan and test-only commits.

## The owner's answers (2026-10-31)

1. **Integrations or admin?** Workspace admin, as a section: `/admin` is the operator's
   control plane; the tenant's connections are the Owner's.
2. **One "integrations" module?** No. Entra stays a module; Google Workspace is a
   sibling filling the same slots; AI is a platform provider like mail.
3. **Env against database?** Not precedence: two tiers by owner, and a setting lives
   in one. The instance tier is what Vantigo provisions; the workspace tier is what the
   tenant brings.
4. **Customer mail** never on the system mailbox: Microsoft 365, Vantigo hosted mail,
   SMTP entered in the interface, or disabled. Google later.
5. **AI**: configured in the environment, shown read-only, the key never shown; a model
   per module and per task within a module.
6. **The hosted service**: Mailgun behind a middleware of ours, consumption tracked,
   test tenants held to listed addresses.
7. **(2026-11-01) The hosted service is deferred.** It needs thorough thought before it
   is built. For now the channel form shows *Vantigo hosted mail* greyed with "Not yet
   available"; nothing is built behind it, and no `MAIL_SERVICE_*` setting exists.

## Deliveries

Each a pull request against `main` after the previous lands, with one migration per
schema it touches, numbers assigned when the first task lands.

- **A — Integrations and the Entra tabs** (`feat/workspace-integrations`): T2, T3, the
  `A/configuration.md` page with T1's tiers. Frontend and docs; no Go change beyond
  none. Tests: the nav's visibility for an Owner, an Entra manager, an authorization
  manager; the redirects; each tab's content against today's page tests.
- **B — Outbound mail through channels** (`feat/outbound-mail-slot`): T4, T5, T6, T7's
  table as it stands without hosted. Migrations: `invoices.settings.send_channel`,
  `calendar.settings`; the `calendar:manage` permission joins the catalog. Tests: the fallback's absence (a channel without a credential
  fails with the sentence), Invoices and Calendar sending from a chosen channel and
  refusing when disabled or the channel is gone, the slot's errors mapped to each
  consumer's words.
- **C — the greyed entry** (`feat/hosted-mail-placeholder`): frontend only, after B.
  The channel form's provider choice shows *Vantigo hosted mail* (nb *Vantigo-driftet
  e-post*) disabled, with the hint "Not yet available" (nb "Ikke tilgjengelig ennå"),
  below Microsoft 365 and SMTP; nothing is sent to the API, no provider key exists
  server-side, and `GET /channel-providers` is unchanged. Docs: `U/communications.md`
  *Set up a channel* names the entry and says it is not yet offered, en and nb.
  Tests: the entry renders disabled and cannot be chosen.
- **C′ — Vantigo hosted mail, deferred**: T8, T9, T10's consumer side, after the
  service has had its own research and design and its first release, with the
  `vantigo-infra` change. Tests against a fake service (the `graphtest` pattern):
  test-mode refusal, idempotent repeat, 429 with `Retry-After`, the events webhook's
  signature and window, `MAIL_DRIVER=vantigo` for an invitation. C′ replaces C's
  greyed entry with the real provider.
- **D — AI models** (`feat/ai-platform`): T11, T12, T13. Migration:
  `identity.ai_task_models`. Tests: the import, a disallowed model refused, the
  resolver's three-step fallback, Communications' two operations unchanged on the wire
  through `Deps.AI`, the page hidden without `AI_PROVIDER`.

A and B are independent and may be worked in parallel; C depends on B (it edits the
same channel form); C′ on C and on the service; D on nothing but A's section. Each
delivery's plan lists T15's pages as its own step.

## Testing

- **Frontend** (A, D): the nav and guard tests in `host/apps.test.ts` and
  `route-guards.test.ts` gain the Integrations cases; the Entra package's page tests
  are split by tab, each asserting the cards it took; the AI page's locked state with
  one model and its absence without a provider.
- **Go** (B, C, D): the module test harnesses (`modtest`) gain a fake
  `OutboundMail`, a fake hosted service and a fake `AIModels`; the existing SMTP seam
  (`Deps.SMTPSend`) stays for channel verification only. `srv/db/schema_test.go`'s
  one-migration-per-schema rule holds. Communications' AI tests keep driving the real
  client through a fake transport (the blind-seam argument in `ai_client.go` stands;
  the client only moved).
- **The tagged test** against a real tenant (Entra) is not touched by A; C gains a
  tagged test against the staging mail service.

## Open questions

1. **A tenant's own sending domain** on hosted mail (SPF, DKIM and DMARC records the
   Owner adds, verified on the channel card). Lean: after C, as its own delivery; the
   subdomain is the default forever.
2. **The SSO card.** Overview keeps the read-only summary; the editable card could
   become *Integrations › Sign-in* when Google needs to embed it too. Lean: decide with
   the Google Workspace research, not here.
3. **Is "Disabled" per module or the absence of channels?** This design makes it per
   module (null setting), so an Owner with three channels may still send invoices on
   paper only. Confirmed by the owner's "three options"; recorded in case it is
   revisited.
4. **The second AI provider.** `anthropic` is the obvious next; the `ai` package's
   request and response types are written provider-neutral so adding it is one file.
5. **A generated configuration reference.** `config.go` is the one place variables are
   read, so `A/configuration.md`'s table could be generated at docs build time like
   the API reference. Lean: write it by hand in A, generate when the table has been
   wrong once.
6. **Rate limits for the hosted service** per tenant are the service's to set; whether
   Vantigo surfaces the plan's figures beyond `usage` (price, overage) is a product
   question for the SaaS launch.
