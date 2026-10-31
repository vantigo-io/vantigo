# Integrations design, delivery B — customer mail through a channel the Owner chose: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** no mail to a *customer* leaves on the installation's `SMTP_*` credentials again.
Communications fills a new single-provider slot, `contracts.OutboundMail`, through which
Invoices sends documents and reminder letters and Calendar sends invitations to outside
attendees, from a channel an Owner picked — or not at all, when the setting is
**Disabled** (null). A Communications SMTP channel with no stored credentials is
**Unavailable** and sends nothing: the fall-back to the host's SMTP in the outbox is gone.
The system mailbox (`Deps.Mail`) keeps its one job, writing to the tenant's own employees.

**Architecture:** `contracts.OutboundMail{Channels, Channel, Send}` with
`OutboundChannel{ID, Address, DisplayName, Provider, ProviderDisplayName, Available}` and
the errors `ErrChannelUnknown`, `ErrChannelUnavailable`, `ErrRecipientRefused` (defined
here, used by delivery C), reusing `ErrMailTooLarge`. `module.Module.OutboundMail` /
`module.Deps.OutboundMail`; Compose and Workers resolve it like `ChannelMailbox`, refusing
two. Communications fills it from its own tables and its own providers: `Send` resolves the
channel, refuses an unavailable one, renders and sends through the channel's provider
(`smtp` through `Deps.SMTPSend` with the channel's sealed credentials, `entra` through
`ChannelMailbox.Send`) with the caller's `Outbound` — From the channel's address, `Reply-To`
and `Message-ID` kept — synchronously, under the caller's context, never in a transaction.
Invoices stores `settings.send_channel uuid NULL`; Calendar a new single-row
`calendar.settings(invitation_channel uuid NULL)` under a new `calendar:manage`
permission. Neither module imports Communications or reads `communications.*` (rule 4).

**Spec:** [`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`](../specs/2026-10-31-integrations-and-configuration-tiers-design.md)
T4, T5, T6, T7 (without hosted), T15 (delivery B); the Outlook connection design M11–M13
for how `ChannelMailbox` was shaped
([`2026-10-29-outlook-connection-and-mail-design.md`](../specs/2026-10-29-outlook-connection-and-mail-design.md)).
**Brief:** [`2026-10-31-integrations-briefs/b-outbound-mail-slot.md`](2026-10-31-integrations-briefs/b-outbound-mail-slot.md).
**Format model:** [`2026-10-29-communications-m365-channels.md`](2026-10-29-communications-m365-channels.md).

**Branch:** `feat/outbound-mail-slot`, cut from `0e553d10` (`main` `5108bb4b` plus the spec
and the four briefs). The pull request opens against `main` after delivery A, if A lands
first, only by Anders' say-so; nothing here depends on A.

**Migrations:** the directory ends at `00095_communications_personal_sending.sql`, so
`00096_invoices_send_channel.sql` and `00097_calendar_settings.sql`. Re-check before the
task that lands each (`ls apps/server/internal/db/migrations | tail`); renumber in-history
if `main` takes a number first.

## Plan readings

1. **The slot's model** (`contracts/channel_mailbox.go`): a doc comment that names the
   rule it lives under, a `// A single-provider slot.` line, errors as package variables
   (`ErrMailTooLarge` at its end, reused). `module.Deps.ChannelMailbox` (`module/module.go:228`),
   `Module.ChannelMailbox` (`:472`), `withChannelMailbox` (`module/workers.go:185`) called by
   Workers (`:98`) and Compose (`module/compose.go:374`) through `soleProvider`, which
   refuses two and keeps a preset value when none declares it; `modtest.WithChannelMailbox`
   (`modtest/modtest.go:386`) fills `setup.channelMailbox` (`:146`), copied into Deps at
   `:681`. `OutboundMail` joins in the same five places, resolved **after
   `PersonalMailbox`**, before `CalendarBusy` (Workers too — Invoices' reminder worker
   sends in worker mode).

2. **Where the fill reads from.** The provider's `Channels` lists `communications.channels`
   where `type = 'email'` (the mail type) and `is_active`; `Available` = the provider is
   offered now (`smtp` always; `entra` while `ChannelMailbox` is filled under `entra` and
   its `Status` is `MailboxAvailable`, `channel_providers.go:25`) **and** for `smtp` a
   `channel_credentials` row exists. `ListChannels` (`queries/`) already returns
   `credential_settings_json` (`channels.go:455`) — `NULL` is "no credential".
   `ChannelResponse.hasCredentials` exists today; the wire gains `available` beside it.
   `Channels` and `Channel` make **no network call**: `ChannelMailbox.Key` / `Status` are
   the provider's own state (the slot's contract).

3. **The send path to reuse.** `OutboxWorker.smtpConfig` (`outbox.go:931`) builds the
   `config.MailConfig` from `message.CredentialSettingsJson` and the sealed password
   (`channelCredentialPurpose`, `channels.go:50`), falling back to `Config.Mail` (`:938`)
   when there is no credential. The fall-back is the T5 line: it becomes a fixed error
   (`errChannelNoCredentials`, "the channel has no credentials") that the outbox treats as
   **terminal** with code `channel_unavailable` (`outboxTerminalCode`, `:125`), since no
   retry mends it. The entra path is `sendThroughMailbox` (`:500`): `mail.RenderMIME`
   from the channel address (`:521`) then `mailboxSend` (`contractscalls.go:173`). The
   slot's `Send` refactors the credential-to-`MailConfig` step into a function both the
   worker and the slot share (`smtpConfigOf(settingsJSON, ciphertext, secrets, address)`),
   so there is one reading of a credential.

4. **Rule 17's shape** is `contractscalls.go`'s: every call out is an accessor that calls
   `noteContractCall` first; the slot's `Send` is *itself* a call into Communications from
   another module, so Communications' side is the provider, not an accessor, and the
   consumers' accessors do the noting. The provider never opens a transaction around a
   send and refuses to run inside a caller's (it cannot know — the consumers' accessors
   do, as Invoices' `noteContractCall` does today).

5. **Invoices today.** `mailAvailable()` (`meta.go:113`) is `Config.Mail.Driver == "smtp"`;
   its callers are `meta.go:55`, `send.go:152` (`withSendDefaults`), `send.go:230`
   (`PostInvoicesByIdSend`, 503 `mailUnavailable()` at `:218`), `reminder_runs.go:79`
   (`recipientOf` → `warningMailUnavailable`) and `reminder_worker.go:296`
   (`reasonMailUnavailable`, `:102`). The sends are `send.go:319` and
   `reminder_worker.go:324`, both through `s.smtpSend(…, s.deps.Config.Mail, out)`
   (`contractscalls.go:231`); `envelope` (`send.go:377`) sets `DisplayName` and `ReplyTo`
   and a fresh `MessageID`. The settings are one row (`invoices.settings`, `UpdateSettings`
   a full replace, `settings.go:PutInvoicesSettings`); required-nullable fields use the
   `json.RawMessage` presence pattern (`parseEInvoicing`). `sendChannel` is **optional on
   PUT, nullable**: absent keeps the stored value (the energy VAT codes' rule,
   `settings.go:checkEnergyVatCodes`), `null` is Disabled — so every existing client keeps
   working and no fixture changes.

6. **Calendar today.** `mailAvailable()` (`contractscalls.go:230`) is the same driver test;
   callers: `invitations.go:116` (`sendInvitations`, logs "the mail driver is not smtp"),
   `:400` (Resend → 503 `invitationsUnavailable()`), `appointments.go:522` (`reread`),
   `meta.go:55` (`InvitationsAvailable`). The send is `invitations.go:164`; the ICS
   ORGANIZER takes `s.deps.Config.Mail.From` (`:267`, `ics.go:232`) — with a channel it must
   be the **channel's address**, since that is the From. The module has two permissions
   (`module.go:48`), no settings; `GET /meta` is under `calendar:access`.

7. **Docs map** (`docs/src/content/docs/…`), `sources` frontmatter paths declare coverage
   (`bun run tools/docs/check-coverage.ts --list`): `en/reference/communications.md`
   (*Providers* `:122`, *SMTP* `:153`), `en/user/communications.md` (*Set up a channel*
   `:217`), `en/user/invoices.md` (*Reminders* `:1094`, *Sending a document by e-mail*
   `:1373`), `en/reference/invoices.md` (*Sending a document* `:2264`, *The model*),
   `en/user/calendar.md` (*Invite people outside the company* `:124`), `en/admin/calendar.md`
   (*Invitations to people outside the company* `:52`), `en/reference/calendar.md`
   (*Permissions* `:167`, *Invitations* `:647`, *Without mail* `:809`),
   `en/admin/installation.md` (*Email and observability*), `en/contributing/module-boundaries.md`
   (rules, the slot list), `ROADMAP.md`; `nb/` twins for `user/` and `admin/`.

8. **Frontends.** Communications' channels page (`apps/communications/frontend/src/pages/admin.channels.tsx`)
   shows each channel's provider badge; the **Unavailable** badge joins it. Invoices'
   `pages/settings.tsx` (1238 lines) gets a **Sending** card; Calendar's
   `pages/settings.tsx` (824 lines, the person's own feed addresses) gets the
   **Invitations to people outside the company** card for `calendar:manage` holders — the
   capability is read from the host's permission set the way the page's other gates are.
   The generated `api-schema.d.ts` of each touched package is regenerated by
   `bun run gen:client` in the commit that changes its contract (CI fails when stale).
   Translations are checked by `bun run translations:check` (en + nb keys).

## Tasks

- [ ] **1. This plan.** Committed alone: `docs(plans): delivery B's plan — contracts.OutboundMail, the end of the SMTP fall-back, Invoices' and Calendar's sending channel` with `Docs-Impact: none — plan only`.

- [ ] **2. The slot (contracts, module, modtest).** Readings 1, 4.
  - [ ] Failing tests first: `module/compose_test.go` `TestCompose_ResolvesTheOutboundMailSlot`
    (resolved from the enabled module, a preset kept, two providers refused) and
    `TestWorkers_ResolvesTheOutboundMailSlot`; `modtest/outbound_mail_test.go` for the fake.
  - [ ] `contracts/outbound_mail.go` — the interface exactly as T4 writes it, the doc
    comment in `channel_mailbox.go`'s voice naming rule 17, `OutboundChannel`, the three
    errors; `module.Deps.OutboundMail`, `Module.OutboundMail`, `withOutboundMail` in
    Compose and Workers; `modtest.FakeOutboundMail` (`SetChannels`, `Sent() []FakeOutboundSend{Channel, Out}`, `FailWith`, `Calls`)
    and `modtest.WithOutboundMail`.
  - [ ] `go vet ./...`, tests for `module`, `modtest`, `contracts`.
  - [ ] Commit (`Docs-Impact: none — the slot's reference and rule 17 land with the docs commit of this delivery`):
    `feat(contracts, module): the OutboundMail slot — a channel the Owner chose, as other modules send through it; Compose and Workers resolve it, modtest fakes it`.

- [ ] **3. Communications fills the slot and the fall-back ends.** Readings 2, 3.
  - [ ] Failing tests first (`outbound_mail_test.go`, `outbox_test.go` edits): a channel
    without a credential row fails the outbox send terminally with code
    `channel_unavailable` and the fixed sentence, never reaching `SMTPSend`; `GET /channels`
    and `GET /channels/{id}` answer `available: false` for it; `Channels` lists it
    `Available:false` and a credentialed one `true`; `Send` from an `smtp` channel reaches
    `Deps.SMTPSend` with the channel's credential and From = the channel's address, the
    caller's `Reply-To` and `Message-ID` kept; `Send` from an `entra` channel reaches
    `FakeChannelMailbox.Send` with the MIME; `Send` refuses an unavailable channel
    (`ErrChannelUnavailable`, nothing sent), an unknown id (`ErrChannelUnknown`), an
    inactive channel (unknown to consumers), a message too large for the entra provider
    (`ErrMailTooLarge`); `Channel` of a gone channel; a test that parses the package's
    non-test Go files and finds no `Config.Mail` selector (the compile-time absence).
  - [ ] `outbound_mail.go`: the provider (`newOutboundMail(d)`), `smtpConfigOf` shared with
    the outbox, `Module().OutboundMail`; `outbox.go`'s `smtpConfig` loses the host
    fall-back; `channels.go`'s `channelResponseOf` gains `Available`;
    `openapi/communications.yaml` `ChannelResponse.available` (required boolean);
    `go generate ./...`; `bun run gen:client`.
  - [ ] `mise run server:test` (communications, module, openapi); existing communications
    tests that leaned on the host fall-back are changed to give the channel a credential.
  - [ ] Commit (`Docs-Impact` naming task 8): `feat(communications): fill OutboundMail from the channels and end the host SMTP fall-back — an SMTP channel without credentials is Unavailable and sends nothing; available on a channel`.

- [ ] **4. Invoices send from a chosen channel.** Readings 5. `00096_invoices_send_channel.sql`.
  - [ ] Failing tests first: `db/schema_test.go` green with the migration (one schema);
    `settings_test.go`: GET answers `sendChannel: null`, PUT stores a uuid, `null` clears,
    absent keeps, a malformed id 400 on `sendChannel`; `meta_test.go`: `mailAvailable` true
    only with a channel set and available (fake `OutboundMail`), false for null, for a gone
    channel and for an unavailable one; `send_test.go`: the send goes through
    `OutboundMail.Send(channel, out)` (From is the channel's, the envelope's Reply-To and
    Message-ID reach the fake), the 503 sentence "Sending is disabled in Invoice settings,
    or the chosen channel cannot send." when null / gone / unavailable, 502 when `Send`
    fails, the delivery row only on success, **no SMTPSend call**; the reminder worker
    sends through the slot and `last_error` is `reasonMailUnavailable` when disabled;
    `recipientOf` falls to paper with `mail_unavailable` when disabled.
  - [ ] Migration; `queries/settings.sql` (`UpdateSettings` takes `send_channel`,
    `sqlc.narg`) and `sqlc generate -f internal/invoices/sqlc.yaml`; `settings.go`
    (parse, response); `contractscalls.go` (`outboundChannel`, `outboundSend` accessors
    noting `OutboundMail.Channel` / `OutboundMail.Send`, replacing `smtpSend`); `meta.go`,
    `send.go`, `reminder_worker.go`, `reminder_runs.go`; `openapi/invoices.yaml`
    (`InvoicesSettingsRequest.sendChannel`, `InvoicesSettingsResponse.sendChannel` and
    `sendChannelState`: `chosen | disabled | gone | unavailable`); `go generate ./...`;
    `bun run gen:client`. Existing invoice tests that used `WithSMTPSend` +
    `MAIL_DRIVER=smtp` move to `WithOutboundMail`.
  - [ ] `mise run server:test` (invoices, db, openapi).
  - [ ] Commit (`Docs-Impact` naming task 8): `feat(invoices): send documents and reminder letters from a channel the Owner chose — settings.send_channel (00096), OutboundMail.Send, Disabled when none or when the channel cannot send`.

- [ ] **5. Calendar invites from a chosen channel.** Readings 6. `00097_calendar_settings.sql`.
  - [ ] Failing tests first: `db/schema_test.go`; `invitation_settings_test.go`: GET/PUT
    `/api/v1/calendar/invitation-settings` need `calendar:access`+`calendar:manage` (403
    for access alone, for manage alone), PUT stores / clears, revision conflict 409,
    a malformed id 400; the catalog holds `calendar:manage` (sensitive, delegable, category
    Administration); `invitation_test.go`: invitations are off when null (nothing sent,
    nothing marked, `GET /meta` `invitationsAvailable:false`, Resend 503), sent from the
    channel (From = the channel's, ICS ORGANIZER = the channel's address, Reply-To = the
    organiser), a channel that cannot send or is gone acts as off, a failing `Send` marks
    the attendee failed for Resend; **no SMTPSend call**.
  - [ ] Migration `calendar.settings(id smallint PK CHECK (id = 1), invitation_channel uuid
    NULL, updated_at timestamptz NOT NULL, revision int NOT NULL)` with the row seeded;
    `queries/settings.sql`; `settings.go` (`GetCalendarInvitationSettings`,
    `PutCalendarInvitationSettings`, If-Match style per the module's revision convention);
    `module.go` permission and the doc comment ("There is no manage key" is corrected);
    `contractscalls.go` swaps `smtpSend` for `outboundSend` and `mailAvailable` for
    `invitationChannel`; `invitations.go`, `appointments.go`, `meta.go`;
    `openapi/calendar.yaml` (`CalendarInvitationSettings*`, both operations
    `x-vantigo-access: permission:calendar:access+calendar:manage`); `go generate ./...`;
    `bun run gen:client`.
  - [ ] `mise run server:test` (calendar, db, openapi, identity's catalog tests if they pin counts).
  - [ ] Commit (`Docs-Impact` naming task 8): `feat(calendar): invite outside attendees from a channel the Owner chose — calendar.settings (00097), the calendar:manage permission, GET/PUT invitation-settings, OutboundMail.Send; null is off`.

- [ ] **6. The three screens.** Readings 8.
  - [ ] Failing tests first: Communications' channels page shows **Unavailable** for an
    `available:false` channel (en + nb); Invoices' settings page shows the **Sending**
    card (radio per channel with its provider badge, **Disabled**, "Disabled — the channel
    no longer exists" for a gone channel, saved through the settings PUT with its revision,
    a failing save keeps the choice); Calendar's settings page shows the card to a
    `calendar:manage` holder and hides it from everyone else. The channel list for the
    two radios is the consumer's own API (`sendChannels` on the settings response:
    id, address, displayName, provider, providerDisplayName, available) — Communications'
    API is not called by either (brief item 6).
  - [ ] Components and strings in both languages; `bun run gen:client` already ran with the contracts.
  - [ ] `bun run frontend:lint`, `frontend:typecheck`, `translations:check`, tests for the three packages.
  - [ ] Commit (`Docs-Impact` naming task 8): `feat(communications-ui, invoices-ui, calendar-ui): Unavailable on a channel without credentials; the Sending card in Invoice settings; the Sent-from card in Calendar settings`.

- [ ] **7. Rule 17 and the slot list.** `docs/…/contributing/module-boundaries.md`: rule 17, the
  slot in the slot list. May share task 8's commit if the boundaries page is its only
  contributing change; kept separate so the rule reads in history.
  - [ ] Commit: `docs(boundaries): rule 17 — OutboundMail, a channel named by id, no network in the lists, nothing in a transaction`.

- [ ] **8. The documentation, both languages where bilingual.** Reading 7. Strip the
  `Docs-Impact` trailers' promise by landing every page: reference *Providers* (the slot,
  rule 17, the fall-back's end, the `Unavailable` state); user *Set up a channel*; user
  invoices *Sending a document by e-mail* and *Reminders* (the setting, Disabled, the new
  sentences); reference invoices (the column, the field, the 503 sentence); user and
  admin calendar (the setting, the permission); reference calendar (`calendar:manage`,
  both endpoints, the table); `admin/installation.md` *Email and observability*
  rewritten (the system mailbox writes to employees only; no channel, document or
  invitation ever uses it); `ROADMAP.md`. `bun run docs:check` and `mise run docs:check`.
  - [ ] Commit: `docs(communications, invoices, calendar, installation): customer mail through a channel — providers and the slot, the Sending setting, the invitation setting, the system mailbox for employees only (en, nb)`.

- [ ] **9. Gates.** `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`; any fix in a commit of its own (`fix(...)`/`test(...)`) — never an amend of a published task.

## Gotchas for every task

- Signed commits only (1Password); on a signing failure stop and report, never `--no-gpg-sign` / `--no-verify`.
- The pre-commit hook is tree-wide; `git add` only this task's files.
- sqlc is `sqlc generate -f internal/<module>/sqlc.yaml` from `apps/server`; `go generate ./...`
  refreshes the embedded contract copies and the oapi-codegen interfaces.
- A fresh worktree has no `node_modules`; `bun install --frozen-lockfile` first.
- Only `apps/projects/frontend`'s localStorage tests fail locally under Node 26 and stop
  the loop early; run the affected packages directly and say so.
- Staff's and identity's sends through `Deps.Mail` / `Config.Mail` are employees' and are not touched.

## Open questions for the owner

1. **A gone or unavailable channel and the radio.** A setting naming a channel that is
   inactive reads as "gone" (the channel is not listed to consumers); an active channel
   that cannot send reads as "unavailable" and keeps the choice. Agree?
2. **The ICS organiser.** Invitations now name the channel's address as ORGANIZER (it is
   the From); replies by calendar program go there rather than to the installation's
   `SMTP_FROM`. The RSVP link is unchanged and remains the supported answer.
