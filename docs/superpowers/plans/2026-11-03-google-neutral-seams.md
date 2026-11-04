# Google Workspace, delivery 0 — the neutral seams: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** nothing an Entra installation does changes, except words: the calendar says
"Outlook" from a value rather than a string, and Staff's attention items lose "Entra" from
their names. Underneath, a second mailbox provider can be added without touching
Communications, the calendar, identity, Staff or the host again.

**Architecture:** `MailboxCalendar`, `ChannelMailbox` and `PersonalMailbox` become
many-provider slots on `module.Deps` (`MailboxCalendars`, `ChannelMailboxes`,
`PersonalMailboxes`), collected by one generic keyed collector in the shape of
`withIdentityProvisioners` (a duplicate `Key()` refused, a reserved one refused, presets
first). Each consumer picks its provider by a key it already holds: the calendar by the
person's `DirectoryAccount.Provider`, Communications by `channels.provider` and, for
personal sending, by asking every provider and recording the one it used
(`conversation_messages.send_provider`). Providers answer a `SentMessage` from `Send`, and
Communications threads by the provider's thread id in either direction and by the
Message-ID the provider sent with. The account kind becomes any provider's key. Identity
keys a Google sign-in by `sub`. Directory search becomes `contracts.DirectorySearch`, served
by identity's Owner SSO endpoints and filled by Entra; the host's picker moves into the
host. The host lists connection cards and buttons from one array.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G1–G11 and P1–P5.
**Brief:** [`2026-11-03-google-workspace-briefs/0-neutral-seams.md`](2026-11-03-google-workspace-briefs/0-neutral-seams.md).

## Plan readings

Checked against `f0eb86a2` (the design's branch).

1. **The single-provider resolvers** are `withMailboxCalendar`, `withChannelMailbox` and
   `withPersonalMailbox` (`srv/module/workers.go:177-225`), called by Compose
   (`compose.go:400,408,416`) and Workers (`workers.go:90,98,106`), through `soleProvider`
   (`compose.go:563`). `withIdentityProvisioners` (`compose.go:880-917`) and
   `reservedProviderKey` (`:925`) are the shape to copy; a generic collector over an
   interface with `Key() string` serves all four keyed slots (three mailboxes and
   `DirectorySearch`).
2. **The fakes already take a key**: `FakeMailboxCalendar.WithKey` (`modtest/mailbox_calendar.go:231`)
   and `FakeChannelMailbox.WithKey` (`modtest/channel_mailbox.go:156`), both defaulting to
   `entra`. `FakePersonalMailbox` (`modtest/personal_mailbox.go:57`) has no key yet.
   `modtest.With*` set one value (`modtest.go:389-420`) and become appends.
3. **Communications names `entra`** in `providerEntra` (`contractscalls.go:36-41`) and in
   the accessors `channelMailbox` (`:91-101`), `mailBudget` (`:140-153`), `outboxMailbox`
   (`:159-168`), `syncMailbox` (`:184-195`); `channel_providers.go:39-43` builds one entry;
   `channels.go` compares `providerEntra` at `:246,264,313,336,447,536,585,765,775,777,908,1226,1283,1301,1336-1340`.
4. **The calendar** reads `s.deps.MailboxCalendar` through the accessors at
   `calendar/contractscalls.go:315-490`; `mailbox_sync.go:273` answers `no_provider` and
   `:332` compares `acct.Provider == key`; the worker's cursors are single
   (`mailbox_worker.go:236-248`, used at `:385-418` and `:433-470`).
5. **The account kind** is closed at `contracts/accounts.go:47-55`; identity's
   `provisioning.go:527` special-cases `AccountKindEntra`.
6. **Identity's OIDC claims** switch on `"entra"` at `oidc_claims.go:130`; the Owner SSO
   settings at `sso_settings_owner.go:553,601,680`.
7. **Migrations**: the latest is `00108_inventory_baseline.sql`, on this branch and on
   `main`; 00109–00111 are free.

## Tasks

- [x] **1. Slots (G2)** — `refactor(module): the three mailbox slots hold many providers`.
  `Deps.MailboxCalendars`, `ChannelMailboxes`, `PersonalMailboxes`; the keyed collector in
  Compose and Workers; `MailboxCalendar.DisplayName()`, `PersonalMailbox.Key()` and
  `DisplayName()` (Entra: "Outlook"); `modtest` options append; `FakePersonalMailbox`
  takes a key (default `entra`); consumers read by key (`entra` until tasks 3 and 4);
  compose and workers tests: two providers, a duplicate refused, a reserved key refused.
- [x] **2. `SentMessage` (G5)** — `refactor(module): a provider answers what it knows of a sent message`.
  `ChannelMailbox.Send` and `PersonalMailbox.Send` answer `(contracts.SentMessage, error)`;
  Entra the zero value; the fakes can be told one.
- [x] **3. Communications (G3, G4, G5)** — `feat(communications): channels and personal sending on any mailbox provider`.
  Accessors by provider; `providerEntra` gone; `/channel-providers` lists every provider;
  the switch, `provider_switch_unsupported`; the sentences with `DisplayName()`;
  `group_address`; `outboundChannelOf`; personal sending's selection and `send_provider`;
  `providerThreadId` and `sent_message_id`; threading. Migration
  `00109_communications_provider_neutral.sql`. `ChannelForm.tsx` driven by the list; no
  `"entra"` literal in `apps/communications/frontend/src`; the composer names
  `providerDisplayName`. Tests with two `ChannelMailbox` and two `PersonalMailbox` fakes.
- [x] **4. Calendar (G3, G10)** — `feat(calendar): mailbox sync on any provider, in neutral words`.
  Provider by `DirectoryAccount.Provider`, the availability order,
  `mailbox_sync.provider`, a provider change cleaning up on the old one, cursors per
  provider, rows marked by `(provider, lower(mailbox))`; `out_of_office`,
  `working_elsewhere`; kind `calendar.mailbox`, the `mailbox` field, `provider` on
  `GET /mailbox-sync`. Migration `00110_calendar_mailbox_neutral.sql`. Frontend labels in
  both languages; an old kind in a saved filter dropped. Tests with two fakes.
- [x] **5. Account kind and Staff's items (G6)** — `feat(staff): the account kind and the attention items name no provider`.
  `ValidAccountKind`, `IsProviderKind`; `AccountKindEntra` gone; identity's `kindOf` and
  `:527`; Staff's enums patterned; `ENTRA_KIND` gone; the attention items renamed and per
  provider; `manager_not_in_directory`; migration `00111_staff_provider_neutral.sql`;
  `deleteExternalAfterDays`'s description.
- [x] **6. Identity's `sub` (G7)** — `feat(identity): a Google sign-in is keyed by sub`.
  `readExternalAccount`; tests for an announced link by `sub` and a 21-digit id;
  `DirectoryKey`'s doc.
- [x] **7. Directory search (G8)** — `feat(identity): directory search behind a contract identity serves`.
  `contracts.DirectorySearch` (Compose only); Entra fills it; identity's two endpoints
  and `directory` on the Owner SSO settings; Entra's two endpoints removed; the picker and
  matches moved to `apps/host/frontend/src/components/directory/`; `users.tsx` gated on
  `directory`; `@vantigo/entra-ui` stops exporting them.
- [x] **8. Host lists (G9)** — `feat(host): the connections page and button list every provider`.
  `lib/connections.tsx`; the Connections page renders the list and is not found with no
  listed module on; `connection=<module>&outcome=`; the composite `connectionButton`;
  `NavItem.modules`.
- [x] **9. Documentation** — `docs(…): …` per section, `en` and `nb` for `user/` and
  `admin/`: `reference/{communications,calendar,staff,entra}.md`;
  `user/{calendar,staff,connections,communications,workspace-administration,entra}.md`;
  `admin/sso-scim.md`; `contributing/module-boundaries.md` (rules 14–16, rule 22, the slot
  list, *Adding a module*). Every moved file covered by a page's `sources`.
- [x] **10. Embedded docs** — `go generate ./...` in `apps/server`,
  `chore(assistant): regenerate the embedded documentation`.
- [x] **11. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`.
