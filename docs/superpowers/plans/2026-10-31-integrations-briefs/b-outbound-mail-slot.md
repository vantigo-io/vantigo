# Brief B — Customer mail through a channel the Owner chose

You are implementing delivery **B** of the design
`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`
(decisions **T4, T5, T6, T7** without the hosted provider, and **T15**). Read that
file first, in full, then this brief. You work alone, from a cold start, in this
repository. Model: Claude Sonnet 5.5.

## The outcome

No mail to a *customer* ever leaves on the installation's `SMTP_*` credentials again.
Communications fills a new single-provider slot, `contracts.OutboundMail`, through
which Invoices sends documents and reminder letters, and Calendar sends invitations to
outside attendees, from a channel an Owner picked — or not at all, when the setting is
**Disabled**. A Communications SMTP channel with no stored credentials is
**Unavailable** and sends nothing: the fall-back to the host's SMTP in the outbox is
gone. The system mailbox (`Deps.Mail`) keeps its one job, writing to the tenant's own
employees.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `docs/src/content/docs/en/contributing/module-boundaries.md`
  (every rule; you are adding rule 17), `en/contributing/platform-packages.md`.
- The spec's *Readings*, T4–T7, T15; the Outlook design M11–M13 for how the
  `ChannelMailbox` slot was shaped and filled
  (`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`).
- `apps/server/internal/contracts/channel_mailbox.go` (the model for your slot's doc
  comment and errors), `apps/server/internal/module/module.go` (`Module`, `Deps`, how
  Compose and Workers resolve a single-provider slot and refuse two),
  `apps/server/internal/server/` (where Compose lives), `apps/server/internal/modtest/`.
- Communications: `channels.go` (PROVIDERS, `verifyChannel`,
  `errChannelMissingCredentials`), `channel_providers.go`, `contractscalls.go`
  (`providerSMTP`, `providerEntra`, the slot calls), `outbox.go` (`smtpConfig`,
  `sender`, the send path for `entra` channels), `queries/`, `openapi/communications.yaml`
  (`ChannelResponse`, `ChannelProviderResponse`).
- Invoices: `send.go` (`PostInvoicesByIdSend`, step 7, `envelope`), `meta.go`
  (`mailAvailable`), `reminder_worker.go` and `reminder_runs.go` (`reasonMailUnavailable`
  and the gate), `contractscalls.go` (`smtpSend`), `settings.go` and `queries/settings.sql`,
  `openapi/invoices.yaml` (`/api/v1/invoices/settings`, its `x-vantigo-access`),
  `apps/invoices/frontend/src/pages/settings.tsx`.
- Calendar: `invitations.go` (the after-commit budget, `smtpSend`), `contractscalls.go`
  (`smtpSend`, the `Driver == "smtp"` gate), `module.go` (two permissions today),
  `openapi/calendar.yaml`, `apps/calendar/frontend/src/pages/settings.tsx` (the
  person's own feed addresses — your card joins it).
- `apps/server/internal/mail/outbound.go` (`Outbound`, `SendOutbound`) — the type your
  slot carries.
- `apps/server/internal/db/schema_test.go` (one migration touches one schema) and the
  latest migration number in `apps/server/internal/db/migrations/`.
- Docs: `en/reference/communications.md` (*Providers*), `en/user/communications.md`
  (*Set up a channel*), `en/user/invoices.md` (*Sending a document by e-mail*,
  *Reminders*), `en/reference/invoices.md`, `en/user/calendar.md` and `en/admin/calendar.md`
  (*Invitations to people outside the company*), `en/reference/calendar.md`,
  `en/admin/installation.md` (*Email and observability*), and the `nb/` twins.

## Build

1. **The slot** `contracts.OutboundMail` as T4 writes it: `Channels`, `Channel`, `Send`,
   `OutboundChannel{ID, Address, DisplayName, Provider, ProviderDisplayName, Available}`,
   errors `ErrChannelUnknown`, `ErrChannelUnavailable`, `ErrRecipientRefused` (reserved
   for C; defined here), reusing `ErrMailTooLarge`. `module.Module.OutboundMail`,
   `module.Deps.OutboundMail`; Compose and Workers resolve it like `ChannelMailbox`.
2. **Communications fills it**: `Channels` lists active channels of the mail type with
   `Available` = the provider offered now and (for `smtp`) a credential row present;
   `Send` resolves the channel, refuses an unavailable one, and sends through the
   channel's provider with the caller's `Outbound` — From the channel's address,
   `Reply-To` kept, the caller's `Message-ID` kept — synchronously, under the caller's
   context, never inside a transaction (`contractscalls.go` pattern, rule 17).
3. **The fallback ends**: `smtpConfig` no longer reads `Config.Mail`; a channel with no
   credential fails with a fixed sentence, and `GET /channels` and the channels page
   show it **Unavailable** (add the field to `ChannelResponse` if it is not there).
   Remove `Config.Mail` from `srv/communications` entirely and prove it with a test
   that greps nothing — or a compile-time absence.
4. **Invoices**: migration `invoices.settings.send_channel uuid NULL` (no FK, rule 4);
   `InvoiceSettings` gains `sendChannel` (nullable) in `openapi/invoices.yaml` under the
   existing `PUT` rule; `mailAvailable()` becomes "a channel is set and
   `OutboundMail.Channel(it).Available`"; the document send and the reminder worker
   call `OutboundMail.Send(channel, out)` under their existing timeouts;
   `reasonMailUnavailable` and the send's 503 sentence read "Sending is disabled in
   Invoice settings, or the chosen channel cannot send." The settings page gets the
   **Sending** card (radio: each channel with its provider badge; **Disabled**; a gone
   channel shows "Disabled — the channel no longer exists"). EHF untouched.
5. **Calendar**: migration creating single-row `calendar.settings(id smallint PK CHECK
   (id = 1), invitation_channel uuid NULL, updated_at, revision)`; a new permission
   `calendar:manage` ("Manage Calendar settings", category Administration, sensitive,
   delegable) in `module.go`'s catalog; `GET`/`PUT /api/v1/calendar/invitation-settings`
   under `permission:calendar:access+calendar:manage`; the invitations path calls
   `OutboundMail.Send` and treats null as "invitations are off" exactly as a non-smtp
   driver is treated today (nothing sent, nothing marked, the appointment says so);
   the settings page gains the card **Invitations to people outside the company —
   Sent from**, shown to `calendar:manage` holders.
6. **`GET /channel-providers`** is unchanged in shape; the Invoices and Calendar radios
   read provider display names from `OutboundChannel`, not from Communications' API.
7. **Documentation**, same commit series, en and nb where bilingual: reference
   *Providers* (the slot, rule 17, the fallback's end, the `Unavailable` state); user
   *Set up a channel* (an SMTP channel without credentials is unavailable); user
   invoices *Sending a document by e-mail* and *Reminders* (the setting, Disabled, the
   new sentences); reference invoices (the column, the field); user and admin calendar
   (the setting, the permission); reference calendar (`calendar:manage`, the
   endpoints, the table); `admin/installation.md` *Email and observability* rewritten:
   the system mailbox writes to employees only, no channel ever uses it;
   `module-boundaries.md` rule 17 and the slot in the slot list; `ROADMAP.md`.
8. **Tests**: `modtest` gains a fake `OutboundMail`; Communications: a channel without
   credential fails with the sentence and lists Unavailable, `Send` from an `smtp` and
   an `entra` channel, `Send` refusing unavailable and unknown; Invoices: send and
   reminder from a chosen channel, both refusing when disabled or the channel is gone,
   the 503 and `last_error` sentences; Calendar: invitations off when null, sent from a
   channel, the permission on both endpoints, the card hidden without it;
   `schema_test.go` green with the two migrations.

## Do not

Build the hosted provider (delivery C) · queue Invoices' or Calendar's mail · touch
`Deps.Mail`, identity's or Staff's sends · move the Channels page · change
`ChannelMailbox` · add a cross-schema foreign key · let any module import another ·
touch the frontend nav (delivery A).

## How to work

- Create a worktree on a branch `feat/outbound-mail-slot` cut from `main`. Run
  `bun install --frozen-lockfile` first (a fresh worktree has no `node_modules`; without
  it the pre-commit hook runs the wrong Biome). `mise run server:db` for the test
  database if the Go tests need it.
- **Plan first.** Write `docs/superpowers/plans/2026-10-31-outbound-mail-slot.md` in the
  house shape (see `docs/superpowers/plans/2026-10-29-communications-m365-channels.md`:
  `> For agentic workers`, **Goal**, **Architecture**, **Spec**, *Plan readings* with
  verified line numbers, *Tasks* as checkboxes, one commit each). Commit the plan
  alone with `Docs-Impact: none — plan only`.
- Migrations take the next free numbers when their task lands; name them
  `000NN_invoices_send_channel.sql` and `000NN_calendar_settings.sql`. Keep the branch
  rebaseable on `main` (Anders lands PRs by rebase; renumber in-history if `main`
  takes a number first).
- One commit per task, Conventional Commits as the history shows. Commits are
  SSH-signed through 1Password; if signing fails, stop and report — never
  `--no-gpg-sign` or `--no-verify`.
- Gates before done: `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`. The Node 26 quirk: only
  `apps/projects/frontend`'s localStorage tests fail locally and stop the loop early;
  run the affected packages directly if so and say so.
- Do not push, do not open a pull request, do not merge `main` into the branch.

## Report

The branch and `git log --oneline main..`; the gates' output verbatim; the plan's path;
the migration numbers taken; where you departed from the spec and why; and the manual
check for a running stack: create an SMTP channel without a password and see it
Unavailable; set Invoices' Sending to a channel and send a document; set it Disabled
and see the sentence.
