# Brief 0 — The neutral seams

You are implementing delivery **0** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G1–G11**).
Read that file first, in full — its *Readings* above all — then this brief. You work
alone, from a cold start, in this repository. Model: Claude Opus.

## The outcome

Nothing an Entra installation does changes, except words: the calendar says "Outlook"
from a value rather than a string, and Staff's attention items lose "Entra" from their
names. Underneath, a second mailbox provider can be added without touching
Communications, the calendar, identity, Staff or the host again:

- `MailboxCalendar`, `ChannelMailbox` and `PersonalMailbox` are many-provider slots
  keyed by `Key()` (G2), and each consumer picks its provider as G3 says;
- Communications names no provider (G4), and a provider answers what it knows of a sent
  message (G5);
- the account kind is any provider's key (G6); identity keys a Google account by `sub`
  (G7);
- directory search is a contract identity serves, and the host imports no provider's
  picker (G8);
- the host lists providers on Settings › Connections and in the `connectionButton` prop
  (G9);
- the calendar's busy vocabulary is neutral (G10).

One pull request (G1), its commits ordered so each compiles and passes the gates.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `docs/src/content/docs/en/contributing/module-boundaries.md`
  (rules 10–17 and 21, *Adding a module*), `en/contributing/frontend-architecture.md`.
- The Outlook connection design's M9, M11–M15, M20
  (`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`).
- Server: `apps/server/internal/module/{module.go,compose.go,workers.go}`
  (`soleProvider` at `compose.go:563`, `withIdentityProvisioners` at `:880`, the
  `with*` helpers at `workers.go:177-235`, CalendarBusy at `compose.go:442-453` — which
  stays as it is); `internal/modtest/{modtest.go,mailbox_calendar.go,channel_mailbox.go}`;
  `internal/contracts/{mailbox_calendar.go,channel_mailbox.go,personal_mailbox.go,accounts.go,sso_connections.go,workforce.go}`.
- Communications: `contractscalls.go` (`:36-41`, `:91-101`, `:140-195`, `:274-294`),
  `channel_providers.go`, `channels.go`, `outbound_mail.go:83-106`, `outbox.go:473,535-565`,
  `inbound_import.go:79`, `inbound_threading.go`, `queries/inbound.sql:55-65`,
  `conversations_create.go:294`, `conversations_reply.go:434`, `conversations_retry.go:52`,
  `conversations.go:698`; migrations 00091, 00092, 00095.
- Calendar: `contractscalls.go:315-490`, `mailbox_sync.go:262-332`,
  `mailbox_worker.go:372-470` and `:543`, `mailbox_busy.go`, `mailbox_push.go:283`,
  `colleagues.go`, `meta.go`; migrations 00081, 00083, 00086.
- Identity: `oidc_claims.go:129-189,255-284`, `scim_input.go:254`, `provisioning.go:111-126,527`,
  `expected_links.go`, `expected_accounts.go`, `sso_connections.go`, `sso_settings_owner.go`.
- Staff: `account_settings.go:67-81`, `accounts_provider.go:49`, `accounts_attention.go`,
  `workforce_directory_account.go`, and `openapi/staff.yaml`'s kind enums and attention
  types.
- Entra: `module.go:150-153`, `directory.go` (the picker and matches handlers you move
  behind the contract), `mailbox.go:113,133`, `channel_mailbox.go`, `mail_send.go`,
  `personal_mailbox.go`.
- Frontend: `apps/host/frontend/src/{apps.ts,navigation.ts}`,
  `routes/workspace/users.tsx`, `routes/settings/connections.tsx`,
  `routes/calendar/settings.tsx`, `routes/communications/inbox.tsx`,
  `routes/workspace/-sso-settings.tsx`; `apps/entra/frontend/src/components/directory-*.tsx`
  and `index.ts`; `apps/communications/frontend/src/components/ChannelForm.tsx`;
  `apps/calendar/frontend/src/{lib/colours.ts,lib/items.ts,pages/settings.tsx,pages/calendar.tsx,i18n.ts}`;
  `apps/staff/frontend/src/lib/accounts.ts`.

## Build (one commit or a few per step, in this order)

1. **Slots (G2).** `Deps.MailboxCalendars`, `ChannelMailboxes`, `PersonalMailboxes`
   slices replacing the singular fields; `with*` collectors refusing duplicate and
   reserved keys, in Compose and Workers; `DisplayName()` on `MailboxCalendar`, `Key()`
   and `DisplayName()` on `PersonalMailbox` (Entra: "Outlook" for both); `modtest`
   options append, fakes take a key (default `entra`). Compose tests: two providers,
   a duplicate refused, a reserved key refused.
2. **`SentMessage` (G5).** `ChannelMailbox.Send` and `PersonalMailbox.Send` answer
   `(contracts.SentMessage, error)`; Entra answers the zero value; fakes can be told a
   value.
3. **Communications (G3, G4, G5).** Accessors by provider; `providerEntra` gone;
   `/channel-providers` lists every filled provider; the switch takes any listed key and
   refuses mailbox→mailbox (`provider_switch_unsupported`); the sentences with
   `DisplayName()`; `group_address` defined; `outboundChannelOf` neutral; personal
   sending asks every provider (the selection rule in G3), records `send_provider`,
   sends through it; the outbox records `providerThreadId` and `sent_message_id`;
   threading by `rfc_message_id` or `sent_message_id`, and by the provider thread in
   either direction. Migration **`00109_communications_provider_neutral.sql`** (index,
   the JSON key rewritten, the thread index, `send_provider` backfilled, `sent_message_id`
   and its index). `ChannelForm.tsx` driven by the list; no `"entra"` literal left in
   `apps/communications/frontend/src`; the composer names `providerDisplayName`.
   Tests with two `ChannelMailbox` fakes and two `PersonalMailbox` fakes.
4. **Calendar (G3, G10).** Provider by `DirectoryAccount.Provider`, the availability
   order, `mailbox_sync.provider`, a provider change cleaning up on the old one, notices
   and connections cursors per provider, rows marked by `(provider, lower(mailbox))`;
   `ShowAsOutOfOffice`/`working_elsewhere` (Entra maps Graph's values), kind
   `calendar.mailbox`, the item's `mailbox` field, `provider {key, displayName}` on
   `GET /mailbox-sync`. Migration **`00110_calendar_mailbox_neutral.sql`**. Frontend
   labels with the provider's name in both languages; an old kind in a saved filter
   dropped. Tests with two `MailboxCalendar` fakes.
5. **Account kind and Staff's items (G6).** `ValidAccountKind`, `IsProviderKind`,
   `AccountKindEntra` gone; identity's `kindOf` and `:527`; Staff's enums to patterned
   strings; `ENTRA_KIND` gone; the seven attention items renamed and per provider; the
   code `manager_not_in_directory`; migration **`00111_staff_provider_neutral.sql`**;
   `deleteExternalAfterDays`'s description.
6. **Identity's `sub` (G7).** `readExternalAccount` takes `sub` for `google`; tests for
   a Google sign-in matching an announced link by `sub`, and a 21-digit id through
   `normalizeExternalID`. `DirectoryKey`'s doc.
7. **Directory search (G8).** `contracts.DirectorySearch` (the shape in G8) as a
   Compose-only slice; Entra fills it from `directory.go`; identity's
   `GET /identity/owner/sso/directory/users` and `/matches` (Owner policy, 60 and 6 a
   minute, 409 `no_directory`, 503 `provider_unreachable`) and `directory` on the
   Owner SSO settings; `/entra/directory/*` removed from `openapi/entra.yaml`; the
   picker and matches moved to `apps/host/frontend/src/components/directory/` on
   identity's endpoints, `users.tsx` gated on `directory`; `@vantigo/entra-ui` stops
   exporting them. `boundaries.md` rule 22.
8. **Host lists (G9).** `lib/connections.tsx`; the Connections page renders the list
   (Entra's card only today) and is not found when no listed module is on;
   `connection=<module>&outcome=` accepted beside `outlook=`; the composite
   `connectionButton`; `NavItem.modules`.
9. **Docs** (below), then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- `reference/communications.md` (mailbox providers by key, the listing, the switch rule,
  `SentMessage`, the thread key and `sent_message_id`, `group_address`,
  `provider_switch_unsupported`, personal sending's provider), `reference/calendar.md`
  (the kind, the values, the provider, the order of the availability),
  `reference/staff.md` (the kind, the renamed items, the code), `reference/entra.md`
  (the directory search behind identity, `SentMessage`).
- `en/` **and** `nb/`: `user/calendar.md` (labels name the provider), `user/staff.md`
  (the renamed items), `user/connections.md` (a list of providers' cards),
  `user/communications.md` (the provider list), `user/workspace-administration.md` and
  `user/entra.md` (the picker belongs to the users page), `admin/sso-scim.md`
  (connecting existing users through the configured provider).
- `contributing/module-boundaries.md`: rules 14–16 as many-provider, rule 22, the slot
  list, *Adding a module*'s paragraph on many-provider slots.
- Each page's `sources` must cover the files you move (`bun run tools/docs/check-coverage.ts --list`).

## Do not

Add the `google` module or any Google code · change `OutboundMail` or `CalendarBusy`
into many-provider slots · change what Entra sends, reads or marks · add a cross-schema
foreign key · let one module import another · change an Entra endpoint other than the
two directory ones · leave a `Docs-Impact: none` trailer on any commit.

## How to work

- Worktree on `feat/google-neutral-seams` cut from the design's branch (the bottom of the README's stack); `bun install --frozen-lockfile`
  first; `mise run server:db` for the test database.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-neutral-seams.md` in the house
  shape (`> For agentic workers`, **Goal**, **Architecture**, **Spec**, *Plan readings*
  with line numbers you verified, *Tasks* as checkboxes, one commit each). Commit it
  alone as `docs(google): the plan for the neutral seams (Google 0)` with no trailer.
- Migrations take the next free numbers (expected 00109–00111), one schema each
  (`srv/db/schema_test.go`).
- Commits: `refactor(module): …`, `feat(communications): …`, `feat(calendar): …`,
  `feat(staff): …`, `feat(identity): …`, `feat(host): …`, `docs(…): …`,
  `chore(assistant): regenerate the embedded documentation`. Signed; never
  `--no-gpg-sign` or `--no-verify`.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`
  (read its log; the wrapper exits 0 regardless), `mise run docs:check`. If only
  `apps/projects/frontend`'s localStorage tests fail under Node 26, run the affected
  packages directly and say so.
- No merge of `main`; the starting session pushes and opens the pull request.

## Report

The branch and `git log --oneline main..`; the gates' output; the plan's path; the
migration numbers; every place you departed from G2–G10 and why; and a manual check on a
running stack with Entra on: the calendar card still says Outlook, a Microsoft 365
channel still sends and receives, the users page's picker still finds an account.
