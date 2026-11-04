# Google Workspace module (design)

Date: 2026-11-03. Research and the owner's answers:
[`docs/superpowers/research/2026-11-03-google-workspace-module.md`](../research/2026-11-03-google-workspace-module.md)
(§8 is binding). Builds on the [Entra design](2026-10-19-entra-design.md), the
[Outlook connection and mail design](2026-10-29-outlook-connection-and-mail-design.md)
and the [integrations design](2026-10-31-integrations-and-configuration-tiers-design.md)
(T2 reserves the place).

A company on Google Workspace gets what a company on Microsoft 365 has: Staff makes and
keeps its accounts and groups in the company's directory, existing users are connected
to their Google sign-in, each person connects their own Google account once, the
calendar syncs to Google Calendar, a licensed mailbox such as `post@` becomes a
Communications channel, and a person can send from their own Gmail address. Entra and
Google may run side by side in one installation (Q2), so the slots that today admit one
mailbox provider admit many first.

Code references are against `main` at `a0141503`. `srv/` abbreviates
`apps/server/internal/`, `host/` `apps/host/frontend/src/`, `mig/`
`srv/db/migrations/`; `U/`, `A/`, `R/`, `C/` the user, administration, reference and
contributing sections of `docs/src/content/docs/en/` (with `nb/` twins for `U/` and
`A/`); `boundaries.md` the module-boundaries page. **D**, **M**, **O** and **T** are the
decisions of the Entra, Outlook connection, calendar Outlook sync and integrations
designs; **Q1–Q7** the owner's answers in the research's §8.

## Readings

What the code holds, checked against `main`, where it differs from the research or adds
to it:

1. **`CalendarBusy` is not a provider slot.** The research (§4.1) lists it with the
   mailbox slots. It is the calendar's own contract, filled by `srv/calendar/module.go:119`
   and read by Planning; Compose resolves it last (`srv/module/compose.go:442-453`). It
   reads the busy rows the calendar keeps from `MailboxCalendar.Delta`. It stays
   single-provider and unchanged; Google's busy time reaches it through the calendar.
2. **The single-provider slots** are resolved by `withMailboxCalendar`,
   `withChannelMailbox`, `withPersonalMailbox` and `withOutboundMail`
   (`srv/module/workers.go:177-235`) through `soleProvider` (`srv/module/compose.go:563`).
   `withIdentityProvisioners` (`compose.go:880-917`) is the many-provider shape to copy:
   a slice, a duplicate `Key()` refused, `reservedProviderKey` (`:925`) guarding
   `identity`, `local` and `external`.
3. **A person's directory account is one.** `Workforce.DirectoryAccount`
   (`srv/staff/workforce_directory_account.go:11-20`) answers the person's provider link,
   "a person with two provider links answers the oldest". Entra's consumers compare its
   `Provider` with `"entra"` (`srv/entra/personal_mailbox.go:139`,
   `srv/entra/outlook.go:398`), and so does the calendar (`srv/calendar/mailbox_sync.go:286,332`).
4. **Communications names `entra` in four accessors**, not two:
   `channelMailbox` (`srv/communications/contractscalls.go:91-101`), `mailBudget`
   (`:140-153`), `syncMailbox` (`:184-195`) and the `outboxMailbox` comparison
   (`:159-168`); the constants are at `:36-41`. `GET /channel-providers`
   (`channel_providers.go:26-56`) appends one provider; its refusal sentences (`:68-79`)
   name Microsoft 365 and Entra. `outboundChannelOf` (`outbound_mail.go:83-106`)
   switches on `providerEntra`.
5. **The thread key** is `inboundMetadata.GraphConversationID`
   (`srv/communications/inbound_import.go:79`, JSON `graphConversationId`), read by
   `FindConversationByGraphConversation` (`queries/inbound.sql:55-65`), which is already
   scoped to the channel, through the index of 00092 (`mig/00092_communications_inbound.sql:62-65`,
   inbound only). The one-channel-per-mailbox index is 00091's
   `ux_channels_entra_mailbox … WHERE provider = 'entra'`.
6. **`normalizeExternalID` does not mangle a Google id.** It GUID-parses only a 32- or
   36-character string (`srv/identity/oidc_claims.go:179-189`, called from
   `scim_input.go:254`); a Google user id is 21 digits and is kept trimmed. What is
   missing is the source: `readExternalAccount` (`oidc_claims.go:264-284`) takes the
   object id from `oid` only, which Google's ID token lacks.
7. **The account kind** is closed at three values: `ValidAccountKind`
   (`srv/contracts/accounts.go:47-55`), identity's `kindOf` (`srv/identity/provisioning.go:111-126`)
   and the provisioning-roles special case (`:527`). Staff already asks the slot whether a
   kind is offered (`srv/staff/account_settings.go:67-81`); the columns are
   `varchar(10)` with no CHECK (`mig/00060_staff_account_kind.sql:39-42`).
8. **Staff's attention items are named for Entra**: `accountAmbiguousInEntra`,
   `accountAddressTakenInEntra`, `accountUnconfirmedInEntra`, `accountPrivilegedInEntra`,
   `managerNotInEntra` (the stored code `manager_not_in_entra`,
   `srv/staff/accounts_provider.go:49`), `entraNotConnected`, `entraFailing`
   (`openapi/staff.yaml`, the attention description and the enum at `:5161`).
9. **Leavers' deletion already exists.** Staff's `deleteExternalAfterDays`
   (`mig/00042_staff_foundation.sql:326`, `srv/staff/account_settings.go:27-60`) deletes a
   provider's account N days after the last employment, for any provider, under
   `staff:manage`. Q5's "delete after N days" is that setting.
10. **Entra has no directory delta for accounts.** Staff's accounts worker reads
    `Account(externalID)` per link on its own cycle (`srv/staff/accounts_worker_provider.go:485`);
    Entra's only deltas are mailbox ones. The research's "reconcile worker in place of
    delta" (§6 B) has nothing to replace.
11. **The host** imports `DirectoryMatches` and `DirectoryPicker` from `@vantigo/entra-ui`
    (`host/routes/workspace/users.tsx:43`, gated `entraOn` at `:157,683,910`),
    `OutlookConnectionCard` on Settings › Connections (`host/routes/settings/connections.tsx:3,53`)
    and `OutlookConnectButton` for the `connectionButton` prop
    (`host/routes/calendar/settings.tsx:13-14`, `host/routes/communications/inbox.tsx:8-9`).
    `NavItem.module` takes one key (`host/apps.ts:607`). `ModuleWithApp` excludes `entra`
    (`host/apps.ts:72`).
12. **The calendar's Outlook vocabulary**: `ShowAsOOF = "oof"` and
    `ShowAsWorkingElsewhere = "workingElsewhere"` (`srv/contracts/mailbox_calendar.go:290-296`),
    stored in `calendar.mailbox_busy.show_as` (`mig/00083_calendar_mailbox_busy.sql:35`);
    the item kind `calendar.outlook` (`srv/calendar/mailbox_busy.go:30`,
    `openapi/calendar.yaml:385-467`, `apps/calendar/frontend/src/lib/colours.ts:3`). The
    `kinds` stored on `calendar.mailbox_sync` are push kinds and never hold it.
13. **Notices and connections are read per slot.** The calendar worker keeps one
    `noticesSince` and one `connectionsSince` (`srv/calendar/mailbox_worker.go:372-470`)
    and marks rows by `lower(mailbox)` alone (00086).
14. **Gmail and the Message-ID.** Communications threads a reply by its In-Reply-To
    against the Message-ID the outbox gave (`<hex@vantigo.invalid>`). Gmail is widely
    reported to replace that header on `messages.send` (U6). Graph keeps it.

## Decisions

### G1 — Eight deliveries, one pull request each

| | Delivery | Module(s) | Migrations |
| --- | --- | --- | --- |
| **0** | Neutral seams: many-provider mailbox slots, Communications and the calendar provider-neutral, the account kind, `sub`, directory search, the host's lists | contracts, module, communications, calendar, identity, staff, entra, host | 00109–00111 |
| **A** | The module: connection, service-account key, guarded client, *Test connection*, four tabs | google, host | 00114 |
| **B** | Accounts, groups, licences, photos through `IdentityProvisioner` | google, entra (address package), staff docs | 00115 |
| **C** | Connect existing users: directory search, matches, announced links, the SSO card | google, host | — |
| **D** | The personal Google connection | google, host | 00116 |
| **E** | Calendar sync on the connection | google | 00117 |
| **F** | Gmail mail channels | google | 00118 |
| **G** | Personal sending through Gmail | google | — |

Each is a pull request against `main` once the one before it has landed; 0 may land
before any Google work starts. The migration numbers are reserved in delivery order and
renumbered at rebase, as always. Each delivery ships its documentation in the same
commit series, `U/` and `A/` in `en` and `nb`. No commit carries `Docs-Impact: none`
unless the whole pull request changes nothing documented (a trailer on any commit waives
the coverage check for the whole range).

**Delivery 0 is one pull request.** Splitting it server-then-host would leave `main`
with identity's new directory endpoints and the calendar's renamed fields unread by the
host, or the host reading endpoints that do not exist. It is large but mechanical; its
commits are ordered so each compiles and passes the gates on its own.

---

## Delivery 0 — the neutral seams

Nothing here changes what an Entra installation does, except labels that named
Outlook where they now name the provider, and Staff's attention items' names.

### G2 — Three mailbox slots become many-provider

`MailboxCalendar`, `ChannelMailbox` and `PersonalMailbox` become many-provider slots
keyed by `Key()`, in the shape of `IdentityProvisioners`:

- `module.Deps` gains `MailboxCalendars []contracts.MailboxCalendar`,
  `ChannelMailboxes []contracts.ChannelMailbox` and
  `PersonalMailboxes []contracts.PersonalMailbox`, replacing the three singular fields.
  `withMailboxCalendars`, `withChannelMailboxes` and `withPersonalMailboxes`
  (`srv/module/workers.go`) collect them from the enabled modules, presets first,
  refusing a duplicate `Key()` and a reserved one, in Compose and Workers at the places
  the singular ones resolve now. `modtest.WithMailboxCalendar` and its two siblings
  append.
- `module.Module`'s builder fields keep their names and signatures: a module still
  fills one of each.
- `contracts.MailboxCalendar` gains `DisplayName() string` ("Outlook", "Google
  Calendar"), answered with no context and no network, for the calendar's copy (G10).
- `contracts.PersonalMailbox` gains `Key() string` and `DisplayName() string`
  ("Outlook", "Gmail").
- **`OutboundMail` stays single.** Communications fills it and is the only provider.
- **`CalendarBusy` stays single** (reading 1). The research named it in error.
- A consumer finds a provider with one helper per slot in its own `contractscalls.go`,
  `mailboxCalendarFor(key)`, `channelMailboxFor(key)`, `personalMailboxFor(key)`, which
  read the slice and make no network call. No consumer iterates a slice to call out
  except where G3 says.
- The doc comments of the three contracts drop "a single-provider slot" and say how a
  caller picks (G3). `boundaries.md` rules 14–16 say "a provider's" where they say
  "the provider's".

### G3 — How each consumer picks its provider

- **The calendar** picks by the person's account: `Workforce.DirectoryAccount(ctx)`
  answers `Provider`, and `mailboxCalendarFor(acct.Provider)` is the provider. The
  availability order becomes `no_provider` (no `MailboxCalendar` at all), then
  `no_account` (no usable account, or none whose provider is registered), then the
  provider's `Status`. Migration 00110 adds `calendar.mailbox_sync.provider
  varchar(20)`, set with `mailbox` whenever a sync confirms the account, backfilled
  `'entra'` where `mailbox` is set. A sync whose account's provider differs from the row's
  is treated as a changed mailbox (O8): the clean-up runs on the row's provider, then the
  row takes the new one. The worker keeps `noticesSince`, `noticesFull`,
  `connectionsSince` and `connectionsFull` per provider key (maps), asks each provider's
  `Notified` and `Connections` in turn, and marks rows by `(provider, lower(mailbox))`;
  00110 replaces 00086's index with one on `(provider, lower(mailbox)) WHERE enabled`.
- **Communications' channels** pick by `channels.provider`: every accessor takes the
  channel's provider and answers `channelMailboxFor(provider)`, nil when none is filled
  under it (as now, "a channel is never served by a provider under another key").
- **Personal sending** asks every `PersonalMailbox` for `Status` (each reads
  `Workforce.DirectoryAccount` and answers `no_account` when the account is not its
  own, as Entra's does at `personal_mailbox.go:139`). The answer Communications uses is
  the first `Available` one; else the first whose reason is not `no_account`; else
  `no_account`; `unavailable` when there is none. `PersonalMailStatus` gains `Provider`
  (the key) and `ProviderDisplayName`. Migration 00109 adds
  `conversation_messages.send_provider varchar(20)`, written with `from_address` when
  `send_as = 'personal'` (backfilled `'entra'` for existing personal rows); the outbox
  sends through `personalMailboxFor(send_provider)` and answers `connection_needed` when
  it is gone, never another provider (Q7).

### G4 — Communications without `entra`

- `providerEntra` goes. A channel's provider is `smtp` or the `Key()` of a filled
  `ChannelMailbox`; validation asks the slice. Every `row.Provider == providerEntra`
  becomes `row.Provider != providerSMTP` (a mailbox channel) plus the lookup.
  `mailBudget(provider)` answers the named provider's `MaxMessage`.
- **The index.** 00109 drops `ux_channels_entra_mailbox` and creates
  `ux_channels_provider_mailbox ON communications.channels (provider, (settings->>'mailbox'))
  WHERE settings ? 'mailbox'`. The `mailbox_exists` refusal is unchanged.
- **`GET /channel-providers`** lists SMTP, then every filled `ChannelMailbox` in slot
  order, each with its key, `DisplayName()`, `MaxMessage`, status and warning, as the one
  entry is built today. `ChannelProviderResponse` is unchanged in shape.
- **The switch of provider** (`POST /channels/{id}/provider`) takes any listed mailbox
  key; `provider_unchanged` when the channel already has it. Switching between two
  mailbox providers is refused (`provider_switch_unsupported`, 409): the channel goes
  back to SMTP first. A PUT that names a mailbox provider stays refused.
- **The sentences** name the provider: `refusalMailChannelsOff` becomes "Mail channels
  through %s are not available. An administrator turns them on under Integrations.",
  `mailbox_unknown` "No mailbox with this address was found in %s.",
  `provider_unreachable` "%s did not answer. Try again.", each with the provider's
  `DisplayName()`. `personal_mailbox` reads "The address is a person's own mailbox. Only
  a shared mailbox can be a channel." A new refusal, `group_address` (422, "The address
  is a group. A group has no mailbox Vantigo can read; use a licensed mailbox."), is
  defined here and answered first by Google (F).
- **`OutboundChannel`** for a mailbox channel takes `ProviderDisplayName` from the
  provider and `Available` from its status; `outboundChannelOf` switches on SMTP or not.
- **The frontend.** `ChannelForm.tsx` builds the provider options from
  `GET /channel-providers` (`:57-117`): SMTP, then every listed provider by its
  `displayName`, disabled with its reason when not available, then the hosted
  placeholder. Every `"entra"` literal in `apps/communications/frontend/src` becomes a
  test of "not smtp". The composer's "Send from my address" copy names
  `PersonalMailStatus.providerDisplayName`.
- `ChannelMailboxInfo.Shared` is documented as "may be a channel: not a person's own
  mailbox" (Entra: `userPurpose = shared`; Google: G34).
- The OpenAPI descriptions that say "Microsoft 365" or "entra" as the only mailbox
  provider say "a mailbox provider (`entra`, `google`)" instead.

### G5 — What a provider answers about a sent message; threading by the provider's thread

- `ChannelMailbox.Send` and `PersonalMailbox.Send` answer `(contracts.SentMessage,
  error)`: `SentMessage{MessageID, ThreadID string}`, the Message-ID the message left
  with as the provider sent it and the provider's thread id, each `""` when the provider
  does not know it. Entra answers the zero value (Graph keeps the Message-ID and
  `sendMail` answers no body).
- The outbox records a non-empty answer on the message: `ThreadID` as
  `channel_metadata_json->>'providerThreadId'`, and a `MessageID` that differs from the
  message's own as `conversation_messages.sent_message_id varchar(998)` (00109, indexed).
- **The thread key is neutral.** `inboundMetadata.GraphConversationID` becomes
  `ProviderThreadID` (`json:"providerThreadId"`); 00109 rewrites the key in every
  existing row (`channel_metadata_json - 'graphConversationId' || jsonb_build_object('providerThreadId', …)`)
  and replaces 00092's index with `ix_conversation_messages_provider_thread` on
  `(channel_metadata_json->>'providerThreadId')` for both directions.
  `FindConversationByGraphConversation` becomes `FindConversationByProviderThread`, and
  its third step matches a message of either direction.
- **Threading's first two steps** match the In-Reply-To and References ids against
  `rfc_message_id` **or** `sent_message_id`. With Graph nothing changes; with Gmail a
  reply to a message whose Message-ID Gmail replaced still threads (U6).

### G6 — The account kind is a provider's key

- `contracts.AccountKindEntra` goes. `ValidAccountKind(s)` admits `local`, `external`,
  or a provider kind: `^[a-z][a-z0-9]{1,9}$` (the column is `varchar(10)`), which is
  syntax only; `IsProviderKind(s)` is a valid kind other than the two. Whether a
  provider offers the kind stays Staff's question to the slot
  (`account_settings.go:67-81`, unchanged).
- Identity's `kindOf` accepts any valid kind, and the special case at
  `provisioning.go:527` reads `IsProviderKind(kind)`: every provider's account gets the
  provisioning roles once, on its first activation, as Entra's does (D5).
- `openapi/staff.yaml`'s `StaffAccountKind` and the two request enums become a string
  with that pattern, documented "local, external, or the key of a provider in
  `providers`". The Staff frontend's `ENTRA_KIND` goes; `lib/accounts.ts` offers local,
  external and each provider's key with its name from the settings' `providers`.
- **Attention items, neutral.** The seven Entra-named items are renamed:
  `accountAmbiguousInDirectory`, `accountAddressTakenInDirectory`,
  `accountUnconfirmedInDirectory`, `accountPrivilegedInDirectory`,
  `managerNotInDirectory`, and the installation's `providerNotConnected` and
  `providerFailing`, which become one per provider (id `<type>/<provider key>`, title the
  provider's name). The stored code `manager_not_in_entra` becomes
  `manager_not_in_directory`; 00111 rewrites it in `staff.identity_links` (`last_error`
  and `reported_codes`). `secretExpiring` and `secretExpired` become per provider in the
  same way. The descriptions that say "Microsoft Entra ID page" say "the provider's page
  under Integrations". The Staff frontend's labels follow, in both languages.
- `deleteExternalAfterDays`'s description loses "restorable there for thirty days" and
  says "deleted in the provider's directory, where the provider may keep it restorable
  for a while (Entra 30 days, Google 20)".

### G7 — Identity keys a Google account by `sub`

- `readExternalAccount` sets `objectID` from `sub` when the configured provider is
  `google`, from `oid` otherwise. The announced links (`expected_links.go`), the
  announced accounts (`expected_accounts.go`) and `KeyHolders` then match a Google
  account by its Directory id, which equals `sub` (U1).
- `normalizeExternalID` is unchanged (reading 6); a test pins that a 21-digit Google
  id is kept exactly. (A 32-digit numeric string would parse as a GUID; Google's ids are
  21 digits, and the test documents the bound rather than changing the function.)
- `contracts.DirectoryKey` is documented as provider-scoped: `ObjectID` is the
  directory's immutable id (Entra's object id, Google's user id), `UserPrincipalName` the
  sign-in name (Entra's UPN, Google's primary email).

### G8 — Directory search becomes a contract identity serves

A new many-provider slot, `contracts.DirectorySearch`, and identity's endpoints over
it; the host no longer imports a provider's picker.

```go
// DirectorySearch is a directory's accounts as identity offers them to an Owner
// connecting existing users (boundaries rule 22). Many providers, keyed by Key;
// identity asks the one whose Key is the configured OIDC provider. Every call
// reaches the network and is never made inside a transaction.
type DirectorySearch interface {
    Key() string         // "entra", "google": the OIDC provider it serves
    DisplayName() string // "Microsoft Entra ID", "Google Workspace"
    // Users answers up to limit accounts whose name or address starts with
    // search, each with its holder under the configured issuer.
    Users(ctx context.Context, search string, limit int) ([]DirectoryUser, error)
    // Matches answers the exact matches between SSOConnections.Unconnected and
    // the directory, as Entra's matches do today.
    Matches(ctx context.Context) (DirectoryMatches, error)
}
type DirectoryUser struct {
    ObjectID, PrincipalName, DisplayName, Mail, EmployeeNumber string
    Enabled, Guest bool
    Holder *KeyHolder
}
type DirectoryMatches struct {
    Matches                          []DirectoryMatch // UserID, Account, MatchedBy
    Candidates, Ambiguous            int
    Truncated                        bool
}
```

- `MatchedBy` values are `mail`, `principalName` and `employeeNumber`.
- **Identity serves it**: `GET /api/v1/identity/owner/sso/directory/users?search=`
  and `GET /api/v1/identity/owner/sso/directory/matches`, both
  `policy:Owner+OwnerManagement`, rate-limited as Entra's are (60 and 6 a minute). They
  answer 409 `no_directory` when OIDC is off or no provider's `Key()` is the configured
  provider, and 503 `provider_unreachable` on a failure. The wire names are
  identity's link's own, `objectId` and `userPrincipalName`, so a pick fills the
  announced-link form unchanged.
- The Owner SSO settings response gains `directory: {provider, displayName} | null`, so
  the users page knows whether to offer the picker without a probe.
- **Entra fills it** by moving `directory.go`'s handlers behind the contract.
  `GET /entra/directory/users` and `GET /entra/directory/matches` are removed from
  `openapi/entra.yaml`. The calls stay the provider's: it still reads
  `SSOConnections` for the candidates, and identity's gatekeeping is unchanged.
- **The host** moves `DirectoryPicker` and `DirectoryMatches` into
  `host/components/directory/`, calling identity's endpoints; `users.tsx` gates them on
  `directory != null` instead of `entraOn`. `@vantigo/entra-ui` stops exporting them.
- Compose resolves the slice; Workers does not (no worker searches).
- `boundaries.md` gains **rule 22, "Identity searches a provider's directory"**.

### G9 — The host lists providers

- **Settings › Connections** becomes a list: `host/lib/connections.tsx` holds an array
  of `{module, Card, Button}` entries (Entra's `OutlookConnectionCard` and
  `OutlookConnectButton`; Google's join in D). The page renders each enabled module's
  card, each of which renders nothing when its provider offers the person nothing (as
  Entra's does on `offered: false`). It is not found when no entry's module is on.
  `validateConnectionsSearch` keeps `outlook=` and gains a neutral `connection=<module>`
  with `outcome=complete|failed&code=`; Entra keeps redirecting with `outlook=` until
  its own change.
- **The `connectionButton` prop** of the calendar settings and the inbox receives a
  composite that renders every enabled module's button; each renders nothing for a
  person whose account is not its own.
- **`NavItem` gains `modules?: ModuleKey[]`** (shown when any is on); Connections uses
  it.
- `ModuleWithApp` becomes `Exclude<ModuleKey, "entra" | "google">` once `google` joins
  `moduleKeys` (A).

### G10 — The calendar's busy vocabulary is neutral

- `ShowAsOOF` becomes `ShowAsOutOfOffice = "out_of_office"` and
  `ShowAsWorkingElsewhere` becomes `"working_elsewhere"`. Entra maps Graph's values to
  them; 00110 rewrites `calendar.mailbox_busy.show_as`.
- The item kind `calendar.outlook` becomes `calendar.mailbox`; the item's `outlook`
  field becomes `mailbox`, and its `showAs` enum takes the new values.
  `apps/calendar/frontend/src/lib/colours.ts` and `items.ts` follow. A saved filter in a
  browser naming the old kind is dropped on read.
- `GET /calendar/mailbox-sync` gains `provider: {key, displayName} | null`, from the
  person's account's provider (`MailboxCalendar.DisplayName()`). The settings card,
  the legend and the item labels say "Busy in %s", "Sync with %s", using it ("Outlook"
  today, "Google Calendar" after E). The `switch_off` availability description says "an
  administrator has not turned on the connection for people in the provider's settings".
- **What a user sees:** the same labels on an Entra installation ("Outlook"), from a
  value rather than a string. The calendar guide's section anchors stay.

### G11 — Delivery 0's migrations, contract changes, tests and pages

- **00109_communications_provider_neutral.sql** — the index (G4), the thread key and its
  index (G5), `send_provider` (G3), `sent_message_id` and its index (G5).
- **00110_calendar_mailbox_neutral.sql** — `mailbox_sync.provider` and its backfill, the
  index (G3), `show_as` values (G10).
- **00111_staff_provider_neutral.sql** — the code rename (G6).
- **OpenAPI:** identity gains the two directory operations and `directory` on the
  Owner SSO settings; entra loses the two directory operations; staff's kind enums
  become patterned strings and the attention types are renamed; calendar's item kind,
  `mailbox` field, `showAs` values and `provider`; communications' descriptions and
  the `provider_switch_unsupported` and `group_address` codes, and `providerDisplayName`
  on the sending status.
- **Tests:** compose tests for each new slice (two providers resolved, a duplicate key
  refused, a reserved key refused); the calendar worker with two fakes (each person
  served by their own provider, notices and connections per provider, a provider
  change cleaning up on the old one); Communications with two `ChannelMailbox` fakes
  (`/channel-providers` lists both, a channel served only by its own, the switch
  between two refused, the index per provider); personal sending choosing among two;
  threading by `sent_message_id` and by a thread across directions; identity's `sub`
  for Google and a 21-digit id through `normalizeExternalID`; `DirectorySearch` through
  identity with a fake, 409 `no_directory`; Staff's renamed items and the migration of
  the code; the host's composite connections and the picker on identity's endpoints.
  `modtest`'s fakes take a key and default to `entra`.
- **Pages** (`en` and `nb` where bilingual): `R/communications.md` (providers, the
  thread key, the sent message, `group_address`), `R/calendar.md` (the kind, the
  values, the provider), `R/staff.md` (the kind, the items), `R/entra.md` (the
  endpoints moved, `SentMessage`), `U/calendar.md` and `U/staff.md` (the item names and
  the provider's name in labels), `U/connections.md` (a list of the providers'
  cards), `U/communications.md` (the provider list in the form),
  `U/workspace-administration.md` and `U/entra.md` (the picker is the users page's),
  `A/sso-scim.md` (connecting existing users through the configured provider),
  `C/module-boundaries.md` (rules 14–16, rule 22, the slot list, *Adding a module*),
  `C/frontend-architecture.md` if it lists the connections slot.

---

## Delivery A — the module

### G12 — `srv/google`, schema `google`, and the wiring

The module is `google` (package `srv/google`, frontend `@vantigo/google-ui` under
`apps/google/frontend`), wired by *Adding a module* (`C/module-boundaries.md:1534`):
`businessModules` in `cmd/vantigo/main.go` beside `entra.Module()` (`:329`),
`openapi/google.yaml`, `Modules` in `srv/openapi/openapi.go:25`, its codegen config and
generate lines, sqlc, depguard, `moduleSchemas` (`srv/db/schema_test.go:28`), the known
modules and **`google requires staff`** in `srv/config/config.go` beside `:2127`,
`moduleKeys` in `host/navigation.ts`, the catalogs, `sessionClients`. It has no app
tile: its page is under *Integrations* (T2).

```sql
CREATE SCHEMA google;
CREATE TABLE google.connection (
    id                    smallint     PRIMARY KEY CHECK (id = 1),
    credential_kind       varchar(30)  NOT NULL DEFAULT 'service_account_key',
    customer_id           varchar(40),             -- C0…, from customers.get
    primary_domain        varchar(253),
    domains               text[]       NOT NULL DEFAULT '{}',  -- verified, from domains.list
    organisation_name     varchar(256),
    admin_subject         varchar(254),            -- the admin the Admin SDK calls impersonate
    oauth_client_id       varchar(200),            -- the Internal web client (D)
    scopes_granted        text[]       NOT NULL DEFAULT '{}',  -- the last test's answers
    scopes_missing        text[]       NOT NULL DEFAULT '{}',
    scopes_unexpected     text[]       NOT NULL DEFAULT '{}',
    status                varchar(20)  NOT NULL DEFAULT 'unconfigured',
    status_reason         varchar(60),
    last_checked_at       timestamptz,
    version               integer      NOT NULL DEFAULT 1,
    updated_at            timestamptz  NOT NULL,
    updated_by_user_id    uuid,
    CONSTRAINT connection_credential_kind_check CHECK (credential_kind IN ('service_account_key', 'vantigo_hosted')),
    CONSTRAINT connection_status_check CHECK (status IN ('unconfigured', 'pending_delegation', 'connected', 'failing'))
);
CREATE TABLE google.credentials (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind               varchar(30)  NOT NULL,     -- service_account_key | oauth_client_secret
    client_email       varchar(254),              -- the key's service account
    client_id          varchar(40),               -- its numeric client id, for DWD
    private_key_id     varchar(64),
    project_id         varchar(64),
    sealed             bytea        NOT NULL,     -- the private key, or the client secret
    created_at         timestamptz  NOT NULL,
    created_by_user_id uuid,
    last_used_at       timestamptz,
    last_failed_at     timestamptz,
    revoked_at         timestamptz,
    CONSTRAINT credentials_kind_check CHECK (kind IN ('service_account_key', 'oauth_client_secret'))
);
CREATE TABLE google.events (
    id            bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz  NOT NULL,
    actor_user_id uuid,
    actor_system  varchar(40),
    action        varchar(40)  NOT NULL,
    subject_id    varchar(64),                    -- a Google id; never an address
    codes         text[]       NOT NULL DEFAULT '{}',
    CONSTRAINT events_actor_check CHECK ((actor_user_id IS NULL) <> (actor_system IS NULL))
);
INSERT INTO google.connection (id, updated_at) VALUES (1, now());
```

The migration is `00112_google_baseline.sql`. The connection is one row written under
`If-Match` (its `version`), as Entra's.

### G13 — The credential: the tenant's service-account key (Q1)

- **`credential_kind`** is `service_account_key` in v1, the only kind implemented.
  `vantigo_hosted` is admitted by the CHECK and refused by the API (`422
  credential_kind_unsupported`); a later delivery adds a token source for it (key-less
  `signJwt` with Workload Identity Federation) behind the same interface, `tokenSource(scope,
  subject)`, which every caller uses.
- **The upload.** `POST /google/connection/credentials` takes `{json}`, the key file's
  text as the Owner pasted it or the page read it from a chosen file. The server parses
  it: `type` must be `service_account`, `token_uri`
  `https://oauth2.googleapis.com/token`, `private_key` an RSA key in PKCS #8 PEM; the
  file is at most 8 KiB. It seals the private key under **`google/service-account-key`**
  and keeps `client_email`, `client_id`, `private_key_id` and `project_id` in the
  clear. Nothing else of the file is kept. The key is **never answered**; the page shows
  the service account's address, its **numeric client id** (what the admin pastes into
  domain-wide delegation), the key id and when it was added.
- **Rotation.** Several keys may be stored; the newest not revoked is used, an older one
  only when the newest is refused (`invalid_grant` on its JWT), which marks it
  `last_failed_at` and is an event. `DELETE /google/connection/credentials/{id}` revokes.
  A new key for another service account is refused while one of the current account's is
  live (`422 client_mismatch`): delegation is granted to a client id, so changing
  account is *Change the service account* (the Owner revokes every key first). Service
  account keys carry no expiry in the file, so no expiry is stored; the guide tells the
  Owner to rotate yearly.
- **The Internal web client** for D is entered in A, so the Owner sets up the project in
  one sitting: `oauth_client_id` on the connection (must end
  `.apps.googleusercontent.com`) and its secret as an `oauth_client_secret` credential,
  sealed under **`google/oauth-client-secret`**, written by
  `PUT /google/connection/oauth-client` and never answered. Google shows a client secret
  only at creation; the page says so. The connection tab shows the two redirect URIs to
  register on that client: D's callback and identity's OIDC callback.
- **Writes** are under `policy:Owner+OwnerManagement` as Entra's are; reads of the
  connection under `google:manage`.

### G14 — The guarded Google client

`srv/google/client.go`, Entra's `graph.go` reshaped:

- **Fixed hosts**: `oauth2.googleapis.com` (token, revoke), `admin.googleapis.com`
  (Directory), `licensing.googleapis.com`, `www.googleapis.com` (Calendar v3, Groups
  Settings v1, the JWKS), `gmail.googleapis.com`, `groupssettings.googleapis.com` (U12),
  and `accounts.google.com` for the OIDC discovery alone. A built URL is checked against
  the list; no redirect is followed.
- **The transport** is netguard's dialler (`srv/netguard`), `Deps.HTTPTransport` in
  tests, a 30-second budget per call, one retry on 429 (`Retry-After`, at most 10 s) and
  on a 5xx, responses capped (1 MiB; 16 MiB for a raw message), Google's error text never
  logged — the client answers a code (`reason` from Google's error body) and the HTTP
  status.
- **The token exchange goes through the same client.** DWD tokens come from
  `golang.org/x/oauth2/jwt.Config{Email, PrivateKey, PrivateKeyID, Scopes, Subject,
  TokenURL}` (already a dependency), with the guarded client passed in the context as
  `oauth2.HTTPClient`. Tokens are cached in memory per `(credential id, subject, scope
  set)` and dropped five minutes before expiry.
- **No `google.golang.org/api` dependency.** The REST calls are few and JSON; a hand
  client keeps the host list, the caps and the words rule in one file, as Entra's does.
- **`quotaUser`** is set on every Calendar and Gmail call to the Google id of the
  person or mailbox acted for, so a busy tenant does not spend one user's quota.
- Errors map to the module's own: `errCredentialInvalid` (`invalid_grant` on the key),
  `errNotDelegated` (`unauthorized_client` or `access_denied` for a scope, U14),
  `errAdminRefused` (403 on an Admin SDK call), `errThrottled`, `errNotFound`.

### G15 — Scopes and *Test connection*

The scopes Vantigo asks the admin to delegate are a function of the switches:

| Need | Scope | From |
| --- | --- | --- |
| The customer and its domains | `admin.directory.customer.readonly`, `admin.directory.domain.readonly` | A |
| Accounts, photos, sign-out | `admin.directory.user` | B |
| Groups and members | `admin.directory.group` | B |
| Group visibility | `apps.groups.settings` | B |
| Licences | `apps.licensing` | B |
| Mail channels (switch on) | `gmail.modify`, `gmail.send` | F |

Scopes are written in full (`https://www.googleapis.com/auth/…`) on the page, comma-
separated, ready to paste. No calendar scope is ever delegated (Q3).

- **`POST /google/connection/test`** asks the token endpoint for **one token per scope**
  needed, as `admin_subject`, then calls `customers.get(my_customer)` and
  `domains.list`, and stores `customer_id`, `primary_domain`, `domains`,
  `organisation_name`. It answers `{granted, missing, unexpected, customer, domains}`.
- **Unexpected** scopes are found only by trying: the test also asks for a fixed list of
  scopes Vantigo never uses and an over-wide admin is likely to paste — `https://mail.google.com/`,
  `gmail.readonly`, `calendar`, `calendar.events` (Q3), `admin.directory.user.security`,
  `drive`, `cloud-platform` — and lists any it is granted, the M19 analogue. A Gmail
  scope while *Mail channels* is off is unexpected too.
- **States.** Every needed scope refused → `pending_delegation`, reason
  `not_delegated`: the page says delegation can take up to 24 hours to apply and, with
  multi-party approval, needs a second super admin. Some refused → `connected` with
  `scopes_missing` listed, and the features needing them unavailable. The key refused →
  `failing`, `credential_invalid`. The admin refused (403) → `failing`,
  `admin_refused`. A test is rate-limited with the Owner's bucket (30 a minute).
- Staff's `ProviderInfo` (`Provider(ctx)`) answers from these rows (B).

### G16 — Permissions, screens, events

- **`google:manage`** ("Manage Google Workspace": the connection's status, accounts and
  events, the account rules), Administration, sensitive, not delegable — Entra's
  `entra:manage` twin.
- **The page** is *Google Workspace* under Integrations, `/workspace/google`, four tab
  routes in `host/routes/workspace/google/`: **Status** (the status card, the scopes
  with granted, missing and unexpected, the events), **Connection** (the steps: create
  the project, the service account and its key — the guide's org-policy note —, upload
  the key, delegate the listed scopes, name the admin, test; the Internal web client),
  **Accounts** (B and C), **Calendar and Gmail** (D, E, F). In A, Accounts and
  Calendar and Gmail show what is coming as empty states with one sentence each; they are
  routes from the start so links never move.
- `GET /google/events` (`google:manage`) as Entra's, newest first.
- A `googletest` package fakes the token endpoint (JWT checked: issuer, subject, scope,
  audience, signature against the uploaded key) and every API the deliveries call, each
  added by the delivery that calls it.

**A's operations:** `GET`/`PUT /google/connection`, `POST /google/connection/credentials`,
`DELETE /google/connection/credentials/{id}`, `PUT /google/connection/oauth-client`,
`POST /google/connection/test`, `GET /google/events`.

**A's pages:** new `A/google-workspace.md` (before you start — a Workspace with a super
admin, a Cloud project under the organisation —, the project, the service account, the
key and `iam.disableServiceAccountKeyCreation`, domain-wide delegation and its scopes,
propagation and multi-party approval, the admin to impersonate, the Internal web
client and its redirect URIs, verify, rotate a key, what can go wrong), new
`U/google-workspace.md` (the page, its tabs, who sees what), new `R/google.md`, the
section overviews (`A/index.md`, `U/index.md`, `R/index.md`), `A/configuration.md`
(the workspace tier lists the Google connection), `U/workspace-administration.md`
(Integrations), `C/module-boundaries.md` (the module in the depguard list), `ROADMAP.md`.

---

## Delivery B — accounts, groups, licences, photos

### G17 — The provisioner `google`

`srv/google/provisioner.go` fills `IdentityProvisioner` under `Key() = "google"`,
`Provider(ctx)` named "Google Workspace". Staff drives it exactly as it drives Entra
(rule 13); nothing in Staff changes beyond delivery 0.

```sql
CREATE TABLE google.accounts (
    user_id                       varchar(32)  PRIMARY KEY,   -- the Directory id
    employee_number               varchar(16)  NOT NULL,
    primary_email                 varchar(254) NOT NULL,
    state                         varchar(10)  NOT NULL,      -- disabled | enabled | deleted
    pass_sealed                   bytea,                      -- google/pass
    pass_expires_at               timestamptz,
    pass_revealed_at              timestamptz,
    created_by_us                 boolean      NOT NULL,
    licences_assigned             text[]       NOT NULL DEFAULT '{}',  -- productId/skuId
    reported                      text[]       NOT NULL DEFAULT '{}',
    last_error                    varchar(60),
    last_synced_at                timestamptz,
    adoption_confirmed_at         timestamptz,
    adoption_confirmed_by_user_id uuid,
    created_at                    timestamptz  NOT NULL,
    updated_at                    timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_google_accounts_number ON google.accounts (upper(employee_number));
CREATE TABLE google.groups (
    group_id          varchar(32)  PRIMARY KEY,               -- the Directory group id
    staff_group_id    varchar(64)  NOT NULL,
    email             varchar(254) NOT NULL,
    created_by_us     boolean      NOT NULL,
    adoption_confirmed_at timestamptz,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL
);
ALTER TABLE google.connection
    ADD COLUMN address_domain        varchar(253),
    ADD COLUMN address_pattern       varchar(40) NOT NULL DEFAULT 'given.family',
    ADD COLUMN org_unit_path         varchar(400) NOT NULL DEFAULT '/',
    ADD COLUMN pass_lifetime_days    integer     NOT NULL DEFAULT 7,
    ADD COLUMN pass_delivery         varchar(10) NOT NULL DEFAULT 'hr',
    ADD COLUMN leaver_remove_licences boolean    NOT NULL DEFAULT true,
    ADD COLUMN leaver_remove_groups  varchar(10) NOT NULL DEFAULT 'managed',
    ADD COLUMN leaver_sign_out       boolean     NOT NULL DEFAULT true,
    ADD COLUMN skus                  jsonb       NOT NULL DEFAULT '[]';
```

The migration is `00113_google_accounts.sql`; the CHECKs mirror Entra's.

- **Correlation** is on `externalIds[{type: "organization", value}]` = the employee
  number, as Entra correlates on `employeeId`: `users.list(customer=my_customer,
  query="externalId:<number>")`, each answer filtered client-side to an
  `organization`-typed external id equal to the number, ignoring case (U11). Two
  matches → `external_ambiguous`; one Vantigo did not create → `external_unconfirmed`
  until a holder of `google:manage` confirms it on the Accounts tab
  (`POST /google/accounts/{userId}/confirm`); an admin (`isAdmin` or
  `isDelegatedAdmin`) → `external_privileged`.
- **Prepare** creates with `users.insert`: `primaryEmail` by the address rules (G18),
  `name`, a random 32-character password nobody sees, `changePasswordAtNextLogin: true`,
  `suspended: true`, `orgUnitPath`, `externalIds`, `organizations` (title, department,
  cost centre, primary), `phones` (work, mobile), `relations` (manager: the manager's
  primary email, when Staff names a manager with a Google account), `languages`
  (`nb`/`en`). An address taken → the next attempt of the pattern, as Entra.
- **Activate** sets `suspended: false`, a **fresh generated password** (16 characters
  from an unambiguous alphabet) with `changePasswordAtNextLogin: true`, sealed under
  **`google/pass`** with `pass_expires_at = now + pass_lifetime_days`, and assigns the
  licences of the person's groups (`licensing.licenseAssignments.insert`,
  `licence_unavailable:<sku>` reported when none is left). `RevealPass` answers it once,
  to whoever `pass_delivery` names, and forgets it (Q4). After `pass_expires_at` the
  sealed password is dropped; HR's *Run now* (`Flags.Fresh`) sets a new one.
- **Update** patches the profile fields Staff's directory switches turn on — the ones
  Google has: `jobTitle`, `department`, `costCenter` (`organizations`), `businessPhone`,
  `mobilePhone` (`phones`), `officeLocation` (`locations`), `manager` (`relations`),
  `preferredLanguage` (`languages`). Fields Google has no attribute for —
  `employeeHireDate`, `employeeLeaveDateTime`, `employeeType`, `companyName`,
  `employeeOrgData` — are ignored and **not reported**: there is nothing an
  administrator can fix. The primary email is never renamed.
- **Delete** is `users.delete`; Google keeps a deleted user restorable for 20 days.
- **`Account`** reads `users.get` and maps `suspended` to `disabled`, a 404 to
  `deleted`.

### G18 — The address rules are shared

`srv/entra/address.go` (pure functions: folding, the patterns, attempts, the temporary
password alphabet) moves to a platform package, **`srv/directoryaddress`**, with Entra
calling it unchanged (its own commit, a refactor with no behaviour change). Google uses
the same patterns (`given.family`, `g.family`, `given`) and domain rule: the address
domain must be one of `domains`. `C/platform-packages.md` lists the package.

### G19 — Leavers (Q5)

**Deactivate** suspends the user on the leave day (`suspended: true`), then, by the
connection's leaver settings: removes the licences (`leaver_remove_licences`, default on,
because Google bills a suspended user's licence, U16), removes group memberships
(`all`, `managed` — the groups Vantigo created — or `none`), and signs the user out
(`users.signOut`, U19). It closes the person's Google connection with its token
forgotten (D's table, when D has landed). **Delete after N days is Staff's existing
`deleteExternalAfterDays`** (reading 9), which already calls `Delete` for any provider;
Google adds no setting of its own. Archive is not offered.

### G20 — Groups and licences

`GroupTraits` declares three traits:

| Trait | Type | Rule |
| --- | --- | --- |
| `email` | string, required | the group's address; default `<group key>@<address domain>`; its domain one of `domains`; immutable once the group exists (`TraitImmutable`) |
| `visibility` | choice | `domain` — anyone in the organisation sees the group and its members — or `members` — only members; written through Groups Settings (`whoCanViewGroup`, `whoCanViewMembership`, `showInGroupDirectory`) |
| `licences` | list | the SKUs in `connection.skus`, each `productId/skuId` with its name, assigned to every member by Activate and Update |

- `EnsureGroup` is `groups.insert` (or the group by its email, adopted when Vantigo
  created it, `external_unconfirmed` otherwise), then the settings. Owners are members
  with role `OWNER`. `SetMembers` diffs `members.list` against the ids, inserting and
  deleting; a member not found is `MemberNotFound`. `DeleteGroup` is `groups.delete`.
- **The SKUs** are read by `GET /google/connection/skus` (`google:manage`): for each
  product id Vantigo knows (`Google-Apps`, the Workspace editions), `licensing
  listForProduct` answers which SKUs the customer holds, cached on `connection.skus`.

### G21 — Photos

`SetPhoto` decodes the JPEG or PNG Staff holds, scales it to **96×96** (centre crop)
with `golang.org/x/image/draw` as Entra does, encodes JPEG, and writes
`users.photos.update` with Google's web-safe base64 (`+`→`-`, `/`→`_`, `=`→`*`). A
refusal or an undecodable picture reports `photo_not_written` (U13). `ClearPhoto` is
`users.photos.delete`. The guide says the directory holds 96 px.

### G22 — No reconcile worker

Staff's accounts worker reads each link's `Account` on its own cycle (reading 10), which
is what tells Staff an account was suspended or deleted outside Vantigo. The research's
reconcile worker has no consumer; B has no worker. `users.watch` stays on the Later list.

**B's operations:** `GET /google/accounts`, `POST /google/accounts/{userId}/confirm`,
`GET /google/groups`, `POST /google/groups/{groupId}/confirm`,
`PUT /google/connection/settings` (the account rules), `GET /google/connection/skus`.

**B's pages:** `A/google-workspace.md` (the account rules, the address, the password
revealed once and its lifetime, licences, groups and their traits, photos at 96 px, what
a leaver's steps do, delete after N days in Staff's settings, confirming an account or a
group Vantigo did not create, the reason codes), `U/google-workspace.md` (the Accounts
tab), `U/staff.md` (the Google kind, the password in place of a pass),
`A/staff.md` where it names providers, `R/google.md`, `R/staff.md`
(`deleteExternalAfterDays`), `C/platform-packages.md`.

---

## Delivery C — connect existing users

### G23 — Google's directory search

`google` fills `DirectorySearch` (G8):

- **`Users`** is `users.list(customer=my_customer, query="name:'<s>*' OR email:'<s>*'")`
  — two queries if the `OR` is refused, merged by id —, at most 20 answers, each with
  `ObjectID` the Directory id, `PrincipalName` the primary email, `Mail` the primary
  email, `EmployeeNumber` from the organization external id, `Enabled` = not suspended,
  `Guest` false (Google has no guests in the directory), and its `Holder` from
  `SSOConnections.KeyHolders`.
- **`Matches`** reads `SSOConnections.Unconnected(1001)` and proposes, for each
  candidate, the one account whose primary email or an alias (`emails[]`) equals the
  candidate's address, or whose organization external id equals their employee number;
  two accounts, or one account matching two candidates, is ambiguous and not proposed —
  Entra's rule. Users are read once (`users.list` with `projection=basic`, paged at 500)
  and matched in memory; an installation over 10,000 accounts answers `truncated`.
- Both need `admin.directory.user` (B's scope), as `admin_subject`.

### G24 — Announced links and the SSO card

- An announced link for Google carries `objectId` = the Directory id and
  `userPrincipalName` = the primary email. After G7 the sign-in matches `sub` against
  it (U1 gates C).
- **The SSO card prefill.** The Accounts tab's *Sign-in* step renders identity's
  `SsoSettingsCard` with defaults, as Entra's does (`host/routes/workspace/entra/accounts.tsx:12`):
  provider `google`, authority `https://accounts.google.com`, client id the
  connection's `oauth_client_id`, and **allowed domains** the connection's `domains`.
  `SsoDefaults` gains `allowedDomains`. The Owner enters the client secret in the card;
  identity keeps its own sealed copy (no secret crosses modules).
- `hd` for a user on a secondary domain (U4): identity's check accepts any allowed
  domain, and the prefill lists every verified domain, so either answer works.

**C's operations:** none new beyond G8's.

**C's pages:** `A/sso-scim.md` and `A/authentication.md` (Google as the provider,
the domains from the connection, `sub` as the key), `U/google-workspace.md`
(connecting existing users, the matches), `U/workspace-administration.md`,
`R/google.md`.

---

## Delivery D — the personal Google connection

### G25 — The connection and its states

```sql
CREATE TABLE google.connections (
    google_user_id  varchar(32) PRIMARY KEY,        -- the account's sub = Directory id
    user_id         uuid        NOT NULL,           -- identity's user, no FK
    scopes          text[]      NOT NULL,           -- as granted, sorted
    refresh_sealed  bytea,                          -- google/connection-refresh; NULL once revoked
    state           varchar(20) NOT NULL,           -- active | reconnect | closing
    error_code      varchar(40),
    connected_at    timestamptz NOT NULL,
    refreshed_at    timestamptz,
    used_at         timestamptz,
    account_checked_at timestamptz,
    changed_at      timestamptz NOT NULL,
    revision        bigint      NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_google_connections_user ON google.connections (user_id) WHERE state <> 'closing';
CREATE INDEX ix_google_connections_changed ON google.connections (changed_at);
ALTER TABLE google.connection
    ADD COLUMN personal_connection     boolean NOT NULL DEFAULT false,
    ADD COLUMN personal_connection_at  timestamptz,
    ADD COLUMN personal_last_error     varchar(40),
    ADD COLUMN personal_last_error_at  timestamptz;
```

The migration is `00114_google_connections.sql`. Everything M3 says of
`entra.outlook_connections` holds: one live row per person, a `closing` row kept beside a
new live one, `account_taken` for a second Vantigo user, every write revision-guarded and
bumping, the access token cached per `(google_user_id, revision)`, every move written to
`google.events` without a token.

### G26 — The flow

M4's two-step flow with Google's endpoints and the tenant's Internal web client:

- `POST /google/connect` `{feature, returnTo}` (`session`): the switch on, a certain
  account (G27), the client configured; sets the sealed cookie `vantigo.google.connect`
  (purpose **`google/connect-state`**: state, nonce, PKCE verifier, user id, the
  session's MAC under **`google/connect-session`**, the scopes, `returnTo`) and answers
  `authorizeUrl` on `accounts.google.com/o/oauth2/v2/auth` with `access_type=offline`,
  `prompt=consent`, `include_granted_scopes=true`, `login_hint` = the account's primary
  email and `hd` = its domain (a hint; the check is G27's).
- `GET /google/connect/callback` (`anonymous`, on `APP_URL`): opens the cookie,
  compares state, redeems the code at `oauth2.googleapis.com/token` through the guarded
  client with the client secret and the verifier, verifies the ID token with go-oidc
  against `https://accounts.google.com` (iss, aud = the client id, nonce, `hd` in
  `domains`), keeps `sub`, the token's `scope` and the refresh token sealed in
  `vantigo.google.connected` (purpose **`google/connected`**, 5 minutes), and redirects
  to `/settings/connections?connection=google&outcome=complete`.
- `POST /google/complete` (`session`): the connected cookie's user and session MAC
  match; G27's check; the row upserted `active`.
- **Features, not scopes.** `calendar` → `calendar.events` and `calendar.app.created`
  (U8); `mail` → `gmail.send`. Base: `openid email profile`. The stored `scopes` are the
  token response's `scope`, never the request (granular consent can drop one). A
  feature whose scopes are missing is not granted; with no feature left, nothing is
  stored and the callback answers `consent_missing`.
- **`prompt=consent` always.** Google issues a refresh token only on a consent; an
  incremental grant without it may answer none (U9). The Internal app's consent screen
  is one click.
- Errors at the callback: `access_denied`, `domain_mismatch` (an `hd` outside
  `domains`, or none — a consumer account), `expired`, `consent_missing`, `failed`.
  Without a valid state cookie nothing is written.
- `GET /google/connection/me` answers `{offered, reason?, state?, features, account,
  connectedAt, errorCode?}` (always 200; `reason` `switch_off` or `no_account`);
  `DELETE /google/connection/me` closes it. Rate limits as Entra's (30 a minute for
  connect and complete, 60 for the callback).

### G27 — The certain account, checked twice

- **At connect:** `Workforce.DirectoryAccount(ctx)` answers `Provider = "google"`,
  enabled, and its `ExternalID` names a `google.accounts` row Vantigo created or whose
  adoption is confirmed, not deleted. Otherwise 409 `no_account`.
- **At complete:** the ID token's `sub` equals that `ExternalID` (U1). Otherwise 409
  `account_mismatch`, nothing stored.
- **On use** (every token asked for E and G): the row `active` and the account still
  trusted, enabled and undeleted in `google.accounts`. A mismatch closes the row with its
  token forgotten.

### G28 — Refreshes, disconnect, leavers, the sweep

- **Refresh failures** move the row to `reconnect`: `invalid_grant` → `expired` (revoked,
  expired, or a password change revoking Gmail tokens); `invalid_scope` or a refreshed
  `scope` lacking a feature's → `consent_missing` without moving the row when another
  feature still stands; `admin_policy_enforced` and `org_internal` → `blocked`.
  `invalid_client` and `unauthorized_client` are the web client's, not the person's:
  the connection's `personal_last_error`. A 5xx, timeout or 429 is transient.
- **Disconnect** → `closing`; the token serves removals; a closing row is deleted once it
  holds no calendar channel and was unused for a day, and in any case after 7 days; on
  deletion the refresh token is revoked at `oauth2.googleapis.com/revoke`.
- **A leaver** (G19) closes the row with `refresh_sealed = NULL`; their calendar copies
  stay in the suspended account (M7, Q2 of the Outlook design).
- **The switch off** closes every row; everyone connects again when it comes back.
- **`google-sweep`** (hourly, claim-based, no connection held across a call — the 00087
  lesson): deletes spent closing rows, revokes their tokens, and reads `users.get` for
  rows in `reconnect` at most once a day (at most 100 a cycle), closing those whose
  account is suspended or deleted.

### G29 — The Connections card and the button

`@vantigo/google-ui` exports `GoogleConnectionCard` and `GoogleConnectButton`, joined to
the host's list (G9): *Not connected* with **Connect Google**; *Connected as x@…* since a
date, the features (Calendar, Gmail), **Disconnect**; *Reconnect needed* with the
reason's sentence and **Reconnect Google**. The Calendar and Gmail tab holds the switch,
its status (`off`, `unavailable` — the client or the connection not ready —,
`consent_missing`, `ready`), the counts of connected and reconnect-needed people, and
the redirect URI to copy.

**D's operations:** `PUT /google/connection/personal` (the switch, under the Owner
policy as Entra's `PUT /entra/connection/settings` is), `POST /google/connect`,
`GET /google/connect/callback`, `POST /google/complete`, `GET`/`DELETE /google/connection/me`.

**D's pages:** `U/connections.md` (Google's card, reconnect reasons, the failures),
`A/google-workspace.md` (the switch, the web client's consent screen as Internal, the
redirect URI, verify by connecting yourself, what happens to a person's connection, the
sweep), `U/google-workspace.md` (the tab), `A/installation.md` (the callback is on
`APP_URL`), `R/google.md`.

---

## Delivery E — calendar

### G30 — `MailboxCalendar` for Google

`google` fills `MailboxCalendar` under `Key() = "google"`, `DisplayName() = "Google
Calendar"`, every per-mailbox call on the person's own connection with the `calendar`
feature (Q3; no DWD calendar scope). `mailbox` is the person's Directory id; the calendar
id `""` is `primary`.

| Call | Google |
| --- | --- |
| `Status` | the switch and the connection: `available`, `cleanup` (switch off, closing rows still holding tokens), `unavailable` |
| `EnsureCalendar` | `calendars.insert` named *Vantigo*, recorded in `google.calendars(google_user_id, calendar_id)`; `known` kept when the row and the calendar exist. `ErrMailboxDenied` when the grant lacks `calendar.app.created` (U8): the *own* target is then unavailable with that reason |
| `DeleteCalendar` | `calendars.delete`, the row dropped |
| `Apply` | `events.insert`/`patch`/`delete`, in Google's batch endpoint 50 at a time; a create carries `extendedProperties.private = {"vantigo": <marker>, "vantigoKey": <key>}`; the answer's `etag` is the change key |
| `Find` | `events.list(privateExtendedProperty=vantigo=<marker>, timeMin=from)` |
| `Delta` | G31 |
| `Marked` | `events.get` per id (batched), those carrying the marker |
| `Versions` | default calendar: `Find`'s list with `etag`; the secondary: `events.list(timeMin, timeMax, fields=items(id,etag))`; `Complete` false past `MailboxVersionsMax` |
| `Events` | `events.get` per id (batched); a 404 or 410 is `Gone` |
| `Watch`, `Unwatch`, `Notified` | G32 |
| `Connections` | `google.connections` rows changed after `since` |

- **The marker** is `google.connection.marker`, 32 random hex digits the migration
  writes once, so two installations writing one calendar never take each other's
  copies (Entra's marker is its client id, which Google has no equivalent of per
  installation).
- Times: a timed event writes `dateTime` with `timeZone` (IANA, the business time zone),
  an all-day one `date`; no Windows zone table.
- `ShowAs` reads: `transparency=transparent` → free (not answered as busy);
  `eventType=outOfOffice` → `out_of_office`; `eventType=workingLocation` →
  `working_elsewhere` only when `transparency` is opaque; a declined attendance → free;
  otherwise `busy`, or `tentative` for `responseStatus=tentative`.
- Errors: 401 on the person's token after a refresh → `ConnectionNeededError`; 403
  `forbidden` → `ErrMailboxDenied`; 404 on the calendar → `ErrMailboxUnknown`; a
  suspended or deleted account → `ErrMailboxGone`; 429 and `rateLimitExceeded` retried
  once then a failure.

### G31 — The delta with a sync token

- `link ""`: `events.list(calendarId=primary, singleEvents=true, timeMin=from,
  timeMax=to, showDeleted=true)`, paged; the last page's `nextSyncToken` becomes the
  delta link. A continuing link: `events.list(syncToken=…, singleEvents=true,
  showDeleted=true)`.
- **The link** is the provider's own string, `g1.<base64url(json{m, f, t, s|p})>`: the
  mailbox, the window, and the sync token or the next page's token. A link whose `m` is
  not the mailbox asked is refused with a failure (the contract's rule).
- **410 Gone** → `ErrMailboxDeltaExpired`; the calendar starts again with `""`.
- **The window is enforced by the provider**: increments are not windowed (U7), so an
  event outside `from..to` is answered `Removed` (or not at all when it was never
  answered). If the initial windowed list answers no `nextSyncToken`, the link carries
  no token and the next continuation answers `ErrMailboxDeltaExpired`, so every sync
  re-reads the window — correct, costlier, logged once per mailbox.
- A cancelled instance (`status=cancelled`) is `Removed`.

### G32 — Change notifications

- **`Watch`** opens an `events.watch` channel on `primary`: `id` a fresh UUID,
  `token` 32 random bytes (only its SHA-256 stored), `address`
  `PUBLIC_BASE_URL/api/v1/google/calendar-notifications`, the longest `ttl` Google
  takes. `google.calendar_channels(google_user_id, channel_id, resource_id, token_hash,
  expires_at, notified_at, created_at)` keeps it. A channel is renewed (a new one opened,
  the old stopped) when less than **min(3 days, half its granted life)** remains, since
  the expiry Google grants is not documented (U5). No `PUBLIC_BASE_URL`, or the
  notifications turned off by `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED`, → `Watch`
  answers nil and the poll carries the sync.
- **`POST /api/v1/google/calendar-notifications`** (`anonymous`, allowlisted in
  `srv/security/publichost.go` beside `:76`, 600 a minute per address): headers only;
  `X-Goog-Channel-ID` names a row, `X-Goog-Channel-Token` must hash to it (else 204 and
  nothing), `X-Goog-Resource-State` `sync` is ignored, `exists`/`not_exists` set
  `notified_at`. Always 204 quickly; no body read.
- **`Unwatch`** is `channels.stop(id, resourceId)`, idempotent, allowed under cleanup.
- **`Notified`** answers rows with `notified_at` after `since`.
- Notifications are not 100 % reliable; the calendar's poll stands.

The migration is `00117_google_calendar.sql` (`google.calendars`,
`google.calendar_channels`, `connection.marker`). **E's operations:** the notifications endpoint.
**E's pages:** `U/calendar.md` (Sync with Google Calendar under the neutral section,
Google's busy kinds), `A/calendar.md` (Google as a provider, notifications need
`PUBLIC_BASE_URL`), `A/google-workspace.md` (calendar sync on each person's connection,
the *own* calendar's scope, verifying, change notifications, a leaver's copies),
`A/installation.md` (`PUBLIC_BASE_URL` serves Google's notifications), `R/calendar.md`,
`R/google.md`.

---

## Delivery F — Gmail mail channels

### G33 — The switch and the scopes (Q6, Q7)

`google.connection.mail_channels boolean` (00116). Turning it on adds `gmail.modify`
and `gmail.send` to the scopes the page lists to delegate, and *Test connection* checks
them by a token for a channel mailbox when one exists, else for `admin_subject`. While
it is off, a Gmail scope still granted is **unexpected** (G15). The guide states the
trade-off plainly: domain-wide delegation cannot be limited to some mailboxes, so these
scopes reach every mailbox in the domain; Vantigo asks for them only while the switch is
on, and uses them only for mailboxes that are channels.

### G34 — `ChannelMailbox` for Google

`google` fills `ChannelMailbox` under `Key() = "google"`, `DisplayName() = "Google
Workspace"`, every call impersonating the channel's mailbox through DWD.

- **`Status`**: the switch and the connection; when available, `Reason =
  MailboxTenantWide` always — the channels page's existing warning.
- **`Check(address)`**: `groups.get(address)` found → **`group_address`** refusal (Q6;
  Communications answers it, G4); `users.get(address)` (as `admin_subject`) not found →
  `ErrMailboxUnknown`; suspended → `ErrMailboxGone`; then `gmail users.getProfile` as
  the mailbox — refused (`failedPrecondition`, no Gmail licence) → `ErrMailboxGone`.
  `Shared` is true unless the user is an account Staff manages for a person
  (`google.accounts`), which makes it a person's own mailbox (`personal_mailbox`). An
  alias resolves to its user; `Mailbox` is the Directory id. Recorded in
  `google.channel_mailboxes(google_user_id, address, history_id, checked_at)`.
- **`MaxMessage`** is 25,000,000 bytes of MIME (Gmail's message size limit, U10).
- **`Send`** posts the MIME to `/upload/gmail/v1/users/{id}/messages/send?uploadType=media`
  (`message/rfc822`, which takes up to 35 MB) and answers `SentMessage{ThreadID,
  MessageID}` — the Message-ID read back by `messages.get(format=metadata,
  metadataHeaders=Message-ID)` (G5, U6). A 413 or a size refusal → `ErrMailTooLarge`.
  Blind copies: the MIME is rendered with `Bcc` (Gmail delivers the header's blind copies
  and strips it), so `ErrBccUnsupported` is never answered.
- **`Delta(mailbox, "inbox", link, since)`**: `""` reads `users.getProfile`'s
  `historyId` first, then `messages.list(labelIds=INBOX, q="after:<unix since>")`
  paged; a continuing link is `history.list(startHistoryId, labelId=INBOX,
  historyTypes=messageAdded,labelAdded,labelRemoved,messageDeleted)`. An added message or
  INBOX label is an item; a removed one or a deletion is `Removed`. Each new item is
  completed by `messages.get(format=metadata, metadataHeaders=Message-ID)` for
  `MessageID` and `internalDate` (`ReceivedAt`); `ConversationID` is the `threadId`.
  At most `MailDeltaPageMax` items a page. The link is `g1.<base64url(json{m, h, p})>`.
  A 404 (or 400 on `startHistoryId`) → `ErrMailboxDeltaExpired` (U15).
- **`Raw`** is `messages.get(format=raw)`, decoded, capped at `max`.
- **`Mark`**: read → `messages.modify(removeLabelIds=[UNREAD])`; category → the labels
  named by the categories (created as needed); move → add the label `EnsureFolder`
  answered and remove `INBOX`.
- **`EnsureFolder(name)`** finds or creates a user label of that name (`labels.list`,
  `labels.create`); a race re-reads.
- **`Watch`, `Unwatch`** answer nil and **`Notified`** nothing: v1 polls (Q7). The
  existing `communications-mailbox-sync` worker's poll is the cadence.
- Gmail's quota is charged to the impersonated mailbox (`quotaUser` set too); a
  `messages.send` costs 100 units, so a channel sends at most ~60 a minute.

The migration is `00116_google_mail_channels.sql` (the switch,
`google.channel_mailboxes`). **F's operations:** `PUT /google/connection/mail-channels`.
**F's pages:** `U/communications.md` (adding a Google Workspace channel, a licensed
mailbox, why a group is refused, the 25 MB budget, marks as labels),
`A/google-workspace.md` (Mail channels: the switch, the scopes and what they reach, the
licensed mailbox and its cost, Collaborative Inbox unsupported, verifying, what can go
wrong), `U/google-workspace.md` (the tab), `R/communications.md` (the provider),
`R/google.md`.

---

## Delivery G — personal sending

### G35 — `PersonalMailbox` for Google

`google` fills `PersonalMailbox` under `Key() = "google"`, `DisplayName() = "Gmail"`:

- **`Status`** for the caller: the personal switch on, a certain account (G27), a live
  `active` connection with `gmail.send` in `scopes`; `Address` the account's primary
  email. The reasons are M20's.
- **`Send`**: the person's token (`mail` feature), `messages.send` through the upload
  endpoint, Gmail filing it under the person's Sent; answers `SentMessage` as F's
  `Send` does. The size budget stays `MaxPersonalMessageBytes` (3 MB): the contract's
  constant for every provider, raised for all only by a later change.
- **Q5–Q7 of the Outlook design apply unchanged**: Reply-To the channel, both new
  conversations and replies, an immediate `connection_needed` failure and a retry after
  reconnecting. Communications' side is delivery 0's (G3).

No migration. **G's operations:** none (the `mail` feature joins D's connect).
**G's pages:** `U/communications.md` and `U/connections.md` (Send from my Gmail
address, connecting the mail feature), `A/google-workspace.md`, `R/google.md`,
`R/communications.md`.

---

## G36 — Later, not planned

Gmail push through Pub/Sub (a topic in the customer's project, an authenticated push
endpoint, a daily re-watch); `users.watch`; credential kind `vantigo_hosted` with
key-less `signJwt` and an *Admin Only Install* listing; the Data Transfer API for a
leaver's Drive and Calendar; the Reports API; an admin role on the service account in
place of `admin_subject` (U3); a channel `MaxMessage` and personal budget above 3 MB for
Entra.

## Unverified items, and how each delivery is safe before they are settled

The owner settles these in the test tenant before the delivery that depends on them. Each
delivery's code is written so a wrong guess fails visibly and safely.

| # | Item | Needed by | Safe until settled because |
| --- | --- | --- | --- |
| U1 | The ID token's `sub` equals the Directory user id | C, D | A and B use Directory ids only. C's links and D's check compare them; a mismatch refuses (`account_mismatch`, a link never matched) — nothing is linked wrongly. The test tenant must settle it before C merges |
| U2 | New organisations enforce `iam.disableServiceAccountKeyCreation` | A | Documentation only: the guide covers lifting it for the project |
| U3 | A custom admin role on the service account works without `sub` | later | v1 requires `admin_subject` |
| U4 | `hd` for a user on a secondary domain | C | Every verified domain is prefilled as allowed |
| U5 | `events.watch` channel lifetime | E | Renewed at min(3 days, half the granted life); the poll runs regardless |
| U6 | Gmail replaces a supplied Message-ID on `messages.send` | F, G | `SentMessage.MessageID` is read back and threading matches it as well as ours (G5); if Gmail keeps ours, both are equal |
| U7 | A windowed initial `events.list` answers a sync token; increments are unwindowed | E | The provider filters the window; with no token every sync re-reads the window |
| U8 | `calendar.app.created` lets a person's token create and use a secondary calendar | E | The *own* target answers `ErrMailboxDenied` and is shown unavailable; *default* is unaffected |
| U9 | Incremental consent with `include_granted_scopes` and `prompt=consent` answers the union in `scope` | D, G | Only the answered `scope` is stored; a missing feature is `consent_missing`, never assumed |
| U10 | Gmail's limits: 25 MB a message, 35 MB through the upload endpoint | F | A refusal is `ErrMailTooLarge`, terminal for the message, never a retry loop |
| U11 | `query=externalId:` matches exactly and across types | B | Results are filtered client-side to an `organization` id equal to the number |
| U12 | Groups Settings' host | B | Both hosts are on the fixed list; the test tenant fixes which is called |
| U13 | `users.photos.update` takes a 96 px JPEG in the `*`-padded alphabet | B | A refusal reports `photo_not_written`, as Entra's |
| U14 | A DWD scope not delegated answers `unauthorized_client` | A | `access_denied` is treated the same |
| U15 | `history.list` with a stale id answers 404 | F | 404 and an `invalid` 400 on `startHistoryId` both restart |
| U16 | A suspended user's licence is still billed; removing it is allowed | B | `leaver_remove_licences` defaults on and can be turned off |
| U17 | The error a DWD call answers for a suspended or unlicensed mailbox | F | Every such shape maps to `ErrMailboxGone`, never a retry |
| U18 | Calendar push needs only HTTPS, no domain verification | E | Without notifications the poll carries the sync |
| U19 | `users.signOut` is available to the delegated scope | B | A refusal is reported, the suspension already done |

## Testing

- **Unit and integration**, per delivery, against `googletest` (the token endpoint
  checking the JWT and the scope; Directory, Licensing, Groups Settings, Calendar with
  sync tokens, 410 and batch; Gmail with history, raw, labels and upload; the OIDC
  discovery and JWKS for D), on the test database, as Entra's are against `graphtest`.
- **Contract tests**: `contracttest.RequireCoverage` for every operation of
  `openapi/google.yaml`; the slots' consumers tested with two fakes (delivery 0) so a
  second provider is proven before Google exists.
- **The pool lesson**: each worker and each slot call tested with a pool of one (as the
  lease-worker fix did), proving no connection is held across a Google call.
- **Never words**: tests that Google's error text and every address stay out of logs,
  events and problems.
- **The tagged test** (`//go:build google_tenant`), like Entra's `tenant_test.go`, runs
  each delivery's flows against the owner's test tenant with a key from the environment,
  and is where U1–U19 are settled and recorded in `R/google.md`.
- **Frontend**: each tab, the connection card and button, the channel form with three
  providers, the calendar labels with a provider name, in both languages.

## Provisional answers (2026-11-03), open to the owner's override

Taken so the stack is not held; each is the design's default and cheap to reverse.

- **P1 — Delete after N days** stays Staff's existing `deleteExternalAfterDays` under
  `staff:manage`, for every provider (G19). It is not made Owner-only.
- **P2 — The channel's mailbox** is refused when it is a group address or the mailbox of a
  person Staff manages; any other licensed user mailbox is accepted (G34).
- **P3 — Staff's attention item types** are renamed to neutral names in delivery 0, the
  Staff API changing with its frontend in the same pull request (G6).
- **P4 — Limits:** a Gmail channel message up to 25 MB; a personal send stays at 3 MB for
  both providers (G34, G35).
- **P5 — Unverified items before merge, not before build.** C and D are built on the
  design's defensive paths before U1 and U9 are settled in the owner's tenant; their pull
  requests say that the check in the tenant comes before the merge.
