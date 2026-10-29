# Outlook connection and Microsoft 365 mail (design)

Date: 2026-10-29. Research:
[`docs/superpowers/research/2026-10-29-outlook-connection-and-mail.md`](../research/2026-10-29-outlook-connection-and-mail.md).
Builds on the [calendar ↔ Outlook design](2026-10-27-calendar-outlook-sync-design.md),
the [Entra design](2026-10-19-entra-design.md) and the
[communications design](2026-09-13-communications-design.md).

A person clicks **Connect Outlook** once, and the features that work in their own mailbox
use that connection: the calendar's Outlook sync now, sending mail from their own address
later. A team's shared mailbox (`post@`, `support@`, `faktura@`) becomes a Communications
channel that sends through Microsoft 365 and, for the first time, **receives**: mail
arriving in the mailbox becomes a conversation, and reply, reply-all and the AI draft
come alive.

## Readings

1. **The owner's decisions** (2026-10-28):
   - personal access is **delegated**, for the calendar too, with a one-click connection
     per person;
   - shared mailboxes stay **application** access, scoped with Exchange RBAC for
     Applications;
   - no installation has calendar sync on, so the application path is **replaced, not
     migrated**;
   - calendar delivery D (#193) is merged, and the connection goes under it.
   - and on 2026-10-29, the answers to Q1–Q4 (at the end): only people with a certain
     account connect; a leaver's copies stay in the disabled mailbox; a tenant-wide or
     unused permission is a warning (M10, M19); shared mailboxes only.
2. **Nothing of a person's is guessed** (the calendar design's O3) still holds. The
   connection proves the person can sign in to *an* account. Staff's provider link still
   decides which account is *theirs*, and the two must agree.
3. **The slots stay the boundary.** Entra owns Graph, the app registration and every
   token. The calendar and Communications ask through contracts named for the thing,
   never the vendor. The calendar does not learn that tokens changed.
4. **A dead token is the person's to fix, never the installation's failure.** It shows as
   *Reconnect Outlook* to that person alone.

## Decisions

### M1 — Six deliveries in two tracks

| | Delivery | Module(s) |
| --- | --- | --- |
| **A** | The Outlook connection: flow, token store, the *Connections* page | Entra, host |
| **B** | The calendar's Outlook sync on the connection; `Calendars.ReadWrite` (application) removed | Entra, Calendar |
| **C** | Mail channels: the switch, the slot, Microsoft 365 channels that send | Entra, Communications |
| **D** | Inbound: the sync worker, receipts, MIME, sanitised HTML, threading, the linker; reply live | Communications, Entra |
| **E** | Mail change notifications, Outlook state on close, the import window | Entra, Communications |
| **F** | *Later.* Sending from one's own address (`Mail.Send`, delegated) | Entra, Communications |

A → B is the connection track, and C → D → E is the mail track. Each delivery is a pull
request against `main` after the previous one in its track lands. The two tracks touch
only the Entra settings and the webhook, so they may run in either order. This document
decides A–D in full, E far enough, and F only so that A does not have to be undone.

---

## Track 1 — the Outlook connection

### M2 — The registration and the admin's part (Entra)

The connection uses **the Entra module's own app registration** and its client secrets,
which are already sealed and picked by `pickCredential`. SSO's registration is not used,
even when it is the same one: SSO's secret belongs to identity.

The admin adds, once:

- under **Authentication**, the platform *Web* with the redirect URI
  `{APP_URL}/api/v1/entra/outlook/callback`;
- under **API permissions** › **Delegated**: `openid`, `profile`, `offline_access`,
  `Calendars.ReadWrite`;
- **Grant admin consent for** the organisation.

The Owner's switch `entra.connection.calendar_sync` is **renamed** `outlook_connect`
in migration `00088_entra_outlook_connect.sql`. The same migration adds
`outlook_connect_at timestamptz` (when the switch was last turned on),
`outlook_last_error varchar(40)` and `outlook_last_error_at timestamptz`. Its label is
**Outlook connection: let people connect their Outlook to Vantigo**. The switch is
unreleased, so the rename breaks nothing. The calendar's own enablement (the person's
toggle) stays the calendar's.

**A and B ship as one release.** Between them, `main` holds a connection that nothing
uses yet. The calendar still runs on the application permission until B. A changes no
calendar behaviour, and keeps `Calendars.ReadWrite` in `missingPermissions` while the
switch is on. B removes it, with the conditional ninth permission (`connection.go:49`,
`:93-105`), the `permission_missing` advisory and the RBAC procedure.

`GET /connection` answers `outlookConnect: {enabled, status, reason?, connected,
reconnect, notifications}` in place of `calendarSync`. `connected` and `reconnect` are
counts of people. `notifications` is today's `EntraCalendarSyncNotifications`
(`openapi/entra.yaml:89-97`), moved unchanged.

| `status` | When |
| --- | --- |
| `off` | the switch is off |
| `ready` | on, the connection `connected`, and at least one person has connected since `outlook_connect_at` |
| `untested` | on and `connected`, nobody connected yet (the guide says: connect yourself to check) |
| `consent_missing` | the last connect's token response lacked a scope that was asked for, so admin consent does not cover it |
| `unavailable` | on, but the connection is not `connected` (`reason` is the connection's) |

A token's `roles` cannot show a delegated grant, so the connection test does not judge
it. The status reads the last connect outcome, which is stored on the connection and
cleared by the next success. Only a callback whose state cookie validated may write it,
so an anonymous caller cannot plant a status. Two failures never reach Vantigo, and the
guide covers them instead:

- a missing redirect URI (AADSTS50011), which Microsoft shows on its own page;
- missing admin consent, where Microsoft either asks the person to consent for themselves
  or shows *Approval required*.

Consent is therefore judged from the token response's `scope` (M4).

### M3 — The person's connection (Entra)

```sql
CREATE TABLE entra.outlook_connections (
    object_id       uuid        PRIMARY KEY,           -- the account's oid
    user_id         uuid        NOT NULL,              -- identity's user, no FK (rule 4)
    tenant_id       uuid        NOT NULL,
    scopes          text[]      NOT NULL,              -- as granted, lower-cased, sorted
    refresh_sealed  text,                              -- entra/outlook-refresh; NULL once revoked
    state           varchar(20) NOT NULL,              -- active | reconnect | closing
    error_code      varchar(40),                       -- reconnect's reason
    connected_at    timestamptz NOT NULL,
    refreshed_at    timestamptz,
    used_at         timestamptz,
    changed_at      timestamptz NOT NULL,              -- the last move between states
    revision        bigint      NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_outlook_connections_user ON entra.outlook_connections (user_id)
    WHERE state <> 'closing';
```

The migration is `00089_entra_outlook_connections.sql`.

- The table is keyed by account, and holds **one live row per person**. A `closing` row
  is kept beside a new live one, so a person who reconnects with a re-pointed account
  does not overwrite the token that the old mailbox's clean-up still needs.
- A second Vantigo user connecting an account that already has a live row is refused
  (`account_taken`). An account whose row is `closing` is taken over by `complete`, which
  makes the row live again for the new user.
- Calls find the row by `object_id`, the mailbox the calendar passes. The person's own
  endpoints find it by `user_id` among the live rows.

- **`active`**: the token works, or has not been tried since it last did.
- **`reconnect`**: a refresh answered something only the person can fix (M5).
  `refresh_sealed` is kept, because a 50076 can clear when the person signs in again and
  then redeems a fresh code. The row is the person's to renew.
- **`closing`**: the person disconnected, or lost their account. The token may only be
  used for removals (M7), and is deleted when nothing owes one.

The access token is cached in memory per row, keyed by `(object_id, revision)`, and
dropped five minutes before expiry, like the application token.

**Every write to a row is guarded by `revision` and bumps it**: a refresh storing the
new token, a move to `reconnect` or `closing`, and `complete`.

- A lost refresh race keeps the winner's token. Both tokens are valid at Microsoft
  (research §3), so nothing breaks.
- A refresh that fails on an old token after `complete` has stored a new one finds the
  revision moved. It changes nothing, so a fresh connection is never turned back into
  *Reconnect*.

Every move between states is written to `entra.events`, never with a token. Entra also
keeps the change itself for consumers: `outlook_connections.changed_at`, read by
`Connections` (M9).

### M4 — The flow

```
Browser                       Vantigo (Entra)                           Microsoft
  │ POST /entra/outlook/connect ──▶ (session; switch on; person has
  │   {returnTo, feature}           a certain account, M6)
  │ ◀── 200 {authorizeUrl}          sets vantigo.entra.connect (sealed, Lax,
  │                                 HttpOnly, 10 min): state, nonce, verifier,
  │                                 user id, session-id MAC, scopes, returnTo
  │ ── navigate ───────────────────────────────────────────────────────▶ /authorize
  │                                                                     (login_hint, domain_hint,
  │                                                                      prompt omitted)
  │ ◀── 302 {APP_URL}/api/v1/entra/outlook/callback?code&state ──────────
  │ GET callback ─────────────────▶ (anonymous) open the cookie, compare state;
  │                                 redeem code + verifier + client secret;
  │                                 verify id_token (go-oidc): iss, aud, nonce,
  │                                 tid = connection tenant; keep oid, scopes,
  │                                 refresh token → seal into vantigo.entra.connected
  │                                 (Lax, 5 min); clear the state cookie
  │ ◀── 302 /settings/connections?outlook=complete
  │ POST /entra/outlook/complete ─▶ (session — Strict cookie present, same origin)
  │                                 open vantigo.entra.connected; user id and
  │                                 session MAC match the session; M6's check;
  │                                 upsert the row active; clear the cookie
  │ ◀── 200 {connection}  → the page returns to returnTo
```

- **Why two steps**: the session cookie is `SameSite=Strict` and is not sent on
  Microsoft's redirect back. The callback proves only that the browser holds the state.
  `complete` proves the session is the same one that started (`MAC(session id)` under
  `entra/outlook-connect-session`, a MAC purpose). This is identity's own pattern.
- **One click**: `login_hint` is the account's principal name from `entra.accounts`, and
  `domain_hint` the tenant's verified domain. With admin consent and an SSO session at
  Microsoft, `/authorize` usually answers without a page.
- **Scopes**: the request names a **feature** (`calendar`, later `mail`), never a
  scope, so no consumer names the vendor's permissions. Entra maps the feature to scopes:
  `openid profile offline_access` plus `Calendars.ReadWrite` for `calendar`. A later
  feature asks only its own scope, and incremental consent keeps the earlier ones. The
  stored `scopes` are the token response's `scope`, never what was asked. A response
  missing a scope that was asked for stores the connection without that feature and
  records `consent_missing` (M2).
- **Errors at the callback**: an `error=` from Microsoft, a failed redemption, or a token
  for another tenant. They answer a 302 to `/settings/connections?outlook=failed&code=<code>`
  with nothing sealed. Codes: `access_denied` (the person cancelled), `tenant_mismatch`,
  `expired` (the cookie or the code), `failed`. **Without a valid state cookie the
  callback writes nothing** and answers `expired`. Only a validated callback may record
  an outcome on the connection.
- **`returnTo`** is checked when it is sealed and again in the browser before
  navigating. It must start with a single `/`, and must not start with `//` or `/\`. It
  must hold no scheme, no backslash and no control character, and be at most 512 bytes.
  Anything else becomes `/settings/connections`. The server does not know the host's
  routes. An unknown path is the host's own 404, and never another origin.
- **CSRF**: `connect` and `complete` are POSTs under the cross-origin protection every
  `/api` request has. The callback is a safe GET that only acts on a state it sealed
  itself.

New contract operations in `openapi/entra.yaml`:

| Operation | Access | Notes |
| --- | --- | --- |
| `POST /entra/outlook/connect` | `session` | 409 `switch_off`, 409 `no_account`, 422 `feature_unknown` |
| `GET /entra/outlook/callback` | `anonymous` | 302 only; rate-limited 60/min per address |
| `POST /entra/outlook/complete` | `session` | 409 `account_mismatch`, 409 `account_taken`, 410 `expired` |
| `GET /entra/outlook/connection` | `session` | the caller's own: `{state, scopes, account, connectedAt, errorCode?}` or 404 |
| `DELETE /entra/outlook/connection` | `session` | → `closing` (M7); 204 |

The callback is on `APP_URL`, not the public host, so `security.PayRoutes` is unchanged.

### M5 — Refresh failures

Every refresh error from `/token` moves the row to `reconnect` with `error_code`. So
does every **401 carrying a claims challenge**: Entra declares the client capability
`cp1` when it redeems and refreshes, so Microsoft sends CAE challenges rather than
waiting for the access token to expire. The call answers
`contracts.ErrConnectionNeeded`, a new sentinel, and nothing retries it until the person
reconnects.

| Microsoft | `error_code` |
| --- | --- |
| `invalid_grant` with 70008, 700082, 50173 | `expired` |
| 70043, 50132 | `sign_in_again` |
| 50076, 50078, 50079 | `mfa` |
| 53000, 53003 | `blocked` (Conditional Access) |
| 65001 | `consent_missing` |
| `interaction_required`, anything else of `invalid_grant` | `sign_in_again` |
| `invalid_client`, `unauthorized_client` | not the person's: the application credential, handled as today (`errCredentialInvalid`) |

A 5xx, a timeout or a 429 from `/token` is transient: the call fails and the row stays
`active`. When an account is disabled or deleted outside Vantigo, the row first moves to
`reconnect`, like any other dead token. M6's check on use then finds the account no
longer enabled or trusted and moves the row to `closing`, so the calendar cleans up
(M9).

Every move into `reconnect` is written to `entra.events`
(`outlook.reconnect_needed`, with the code), never with a token.

### M6 — The certain account, checked twice

- **At `connect`**: the person must have a certain account, which is
  `Workforce.DirectoryAccount` (calendar O3), enabled, from this provider, and trusted in
  `entra.accounts`. Otherwise 409 `no_account`, and the *Connect* button is not offered.
- **At `complete`**: the token's `oid` must equal that account's object id, and `tid` the
  connection's tenant. Otherwise 409 `account_mismatch` ("You signed in as
  *x@…*; Vantigo knows you as *y@…*"), and nothing is stored.
- **On use**: Entra answers a mailbox call only for an object id with an `active` row
  **and** a trusted, undeleted, enabled account (`checked`, `mailbox.go:216`). A mismatch
  found later moves the row to `closing`. That covers an account re-pointed in Staff,
  and an account disabled or deleted in the tenant. For a row in `reconnect`, Entra reads
  the account's `accountEnabled` with the application token (`User.ReadWrite.All`, which
  the connection already holds) at most once a day, on the calendar's long re-check
  (M9). So a person waiting in `reconnect` for good is still found.

### M7 — Disconnect, leavers and closing

- **The person disconnects**: the row goes to `closing`. Consumers see
  `ErrConnectionNeeded` for everything except removals. The calendar's worker sees its
  availability become `not_connected` and runs its own clean-up (calendar O8), which is
  `Apply` deletes, `DeleteCalendar` and `Unwatch`, all allowed on `closing`. Entra
  cannot be told the clean-up ended (there is no reverse call), so it deletes a `closing`
  row once it holds **no subscription and was not used for 24 hours**, and in any case
  **7 days** after closing. Vantigo never calls
  `revokeSignInSessions` for a disconnect, because that would sign the person out
  everywhere.
- **A leaver**: the leaver's steps disable the account and revoke sessions, which ends
  the token before any clean-up could run. The deactivate step therefore moves the row to
  `closing` **and sets `refresh_sealed = NULL`**. The calendar's clean-up then answers
  `ErrMailboxGone` and forgets the copies unremoved, exactly as it does today for a
  mailbox it cannot reach (O8's *as built*).
  **A leaver's copies are therefore left in the disabled mailbox.** The calendar design
  (owner's decision, 2026-10-27) had them deleted. The owner accepted the change on
  2026-10-29 (Q2).
- **The switch is turned off**: every row goes to `closing`, and the calendar cleans up
  everyone. That is the same outcome as today's switch-off, now carried by each person's
  own token. **Everyone must connect again** when the switch comes back on. The Entra
  page says so before it saves, which differs from today, where the person's toggle
  resumed on its own.

### M8 — The host's *Connections* page

A new page, `/settings/connections`, beside **Profile** and **Security** in the settings
area (`apps/host/frontend/src/apps.ts`). The host renders it from the Entra frontend
package through a slot: the host composes, and never imports Entra.

- **Outlook**, when the switch is on and the person has a certain account: *Not
  connected* with **Connect Outlook**; *Connected as y@…* since a date, the features using
  it (from the scopes: *Calendar*), **Disconnect**; or *Reconnect needed* with the reason
  and **Reconnect Outlook**.
- `?outlook=complete` runs `complete` and returns to `returnTo`. `?outlook=failed` shows
  the code's sentence.
- Nothing shows when the switch is off or the person has no certain account.

The calendar's own Outlook card (`apps/calendar/frontend/src/pages/settings.tsx`
`OutlookSection`) shows the same state inline. The calendar does not call Entra:
the card opens a host slot, **`connection.button`**, which takes `{feature: "calendar",
returnTo}`, and which the host fills with the Entra package's button (module boundaries
rule 7). The slot renders nothing when no provider fills it. The person never leaves the
app they are in for more than the redirect.

### M9 — B: the calendar on the connection

**Entra.**

- `mailboxSession` gains a token source. For the calendar's calls, the source is the
  person's connection, looked up by `object_id` (the mailbox the calendar passes). The
  application credential is no longer used for any `/users/{oid}/calendar…`,
  `/events…`, `calendarView/delta` or subscription call on a person's mailbox.
- `keyPrefix` and `calendarMark` take the **connection's** client id, not the
  credential's.
- `Status` (no network call, rule 14) answers `available` when the switch is on and the
  connection `connected`, `cleanup` (`switch_off`) when it is off and a credential still
  works, and `unavailable` otherwise. It is unchanged in shape.
- Per mailbox, a call answers `contracts.ErrConnectionNeeded` with a reason when the row
  is missing, `reconnect`, or `closing` for a non-removal. The calendar maps it to its new
  availability values.
- **Subscriptions** are created and renewed with the person's token. A
  `reauthorizationRequired` renews with it, and if the row is `reconnect` the
  subscription is left to lapse and the state is `reauthorize`. On the next `active`
  (a reconnect), the calendar's worker runs a sync, which runs `Watch` again (D's own
  rule). The webhook and its checks are unchanged. A subscription's `creatorId` is the
  person.
- Removed: `Calendars.ReadWrite` from the conditional permissions, the calendar sync
  section's RBAC procedure in the administration guide, and the `permission_missing`
  advisory status. `ErrMailboxDenied` stays for a mailbox Exchange still refuses (a
  mailbox without Exchange Online, say).

**Calendar.**

- `mailboxAvailability` gains `not_connected` and `reconnect` (with the reason), before
  `no_account` is considered. The reserved `permission_missing` is removed from the enum.
- **Parking.** The worker claims by `next_sync_at` alone. That is a batch on `main`
  (`ClaimDueMailboxSyncs`, `calendar/queries/mailbox_sync.sql:46-51`), and one row at a
  time with `claimed_until` after #194 (`ClaimDueMailboxSync`). Either way it cannot skip
  a person waiting for a connection before claiming them. It treats `ErrConnectionNeeded` as a person-level stop: the state is
  `unavailable` with the code (`not_connected`, `reconnect`), `attempts` is not raised,
  and `next_sync_at` is set **24 hours ahead**. That is the long re-check, which still
  runs the cycle's steps (availability, `DirectoryAccount`, the clean-up owed), so a
  person who lost their account is cleaned up. It is not the backoff, and not the 6-hour
  retry for `mailbox_denied`.
- **Waking.** A parked person is woken by Entra's change, not by polling.
  `MailboxCalendar` gains
  `Connections(ctx, since time.Time) ([]ConnectionChange, error)`. It makes no network
  call and is read from `outlook_connections.changed_at`, the way `Notified` is read
  from the subscriptions. It answers `{Mailbox, State}` for every row that moved since
  `since`. The worker's `takeNotices` step (D's own, before the lease) reads it
  alongside `Notified` and sets `next_sync_at = now` on the matching rows. So a
  `complete`, a reconnect or a move to `closing` reaches the person's row within one
  cycle.
- Turning the person's toggle on without a connection is allowed: the row is `pending`,
  is parked by its first cycle, and the card shows **Connect Outlook**. The `complete`
  wakes it through `Connections`.

**Contract changes** (`contracts/mailbox_calendar.go`): `ErrConnectionNeeded` with its
reason, the `ConnectionChange` type and `Connections`. No other method changes. Module
boundaries rule 14 notes that the provider acts on the person's own delegated connection,
and that removals are allowed on a closing one.

---

## Track 2 — mail channels

### M10 — The switch and the permissions (Entra)

`entra.connection.mail_channels boolean NOT NULL DEFAULT false` (in
`00090_entra_mail_channels.sql`). Its label is **Mail channels: let Communications send
and receive through Exchange Online shared mailboxes**.

While it is on, the guide asks for **application** permissions granted through Exchange
RBAC for Applications, not through Entra:

```powershell
New-ServicePrincipal -AppId <client id> -ObjectId <enterprise app object id> -DisplayName "Vantigo"
Set-Mailbox post@firma.no -CustomAttribute10 "Vantigo"          # per channel mailbox
New-ManagementScope -Name "Vantigo mail channels" -RecipientRestrictionFilter "CustomAttribute10 -eq 'Vantigo'"
New-ManagementRoleAssignment -App <client id> -Role "Application Mail Full Access" -CustomResourceScope "Vantigo mail channels"
New-ManagementRoleAssignment -App <client id> -Role "Application MailboxSettings.Read" -CustomResourceScope "Vantigo mail channels"
Test-ServicePrincipalAuthorization -Identity <client id> -Resource post@firma.no
```

`Application Mail Full Access` is read/write and send in one role.
`MailboxSettings.Read` lets `Check` tell a shared mailbox from a person's (M11). The
Entra page shows these commands with the client id and the object id filled in, ready to
copy. *Check before C that the `MailboxSettings.Read` application role exists in RBAC
for Applications. If it does not, `Check` falls back to `accountEnabled` (M11).*

`GET /connection` answers `mailChannels: {enabled, status, reason?}`:

| `status` | When |
| --- | --- |
| `off` | the switch is off |
| `ready` | on, `connected`, and the last test's token carries **no** `Mail.*` role |
| `tenant_wide` | on, and the token carries any of `Mail.Read`, `Mail.ReadBasic`, `Mail.ReadBasic.All`, `Mail.ReadWrite`, `Mail.Send` or `full_access_as_app` (Exchange): the grant reaches every mailbox in the tenant |
| `unavailable` | on, but the connection is not `connected` |

`tenant_wide` is **a warning, not a block** (the owner's answer to Q3): the channels work, and the Entra page
and the channel admin page show *"Mail access is granted to every mailbox in the tenant.
Limit it with RBAC for Applications and remove the tenant-wide consent."* The test cannot
see an RBAC grant, so `ready` means "not tenant-wide", and the channel check (M12) proves
reach per mailbox.

### M11 — The slot: `contracts.ChannelMailbox`

A single-provider slot that Entra fills when the switch is on. It is named for the thing,
so an IMAP or Gmail provider could fill it later. Every method takes the mailbox id that
the channel stores, and none is called inside a transaction.

- **Which provider.** A channel's `provider` is the slot's `Key()` (`entra`), not a
  vendor name. The contract and the UI label it *Microsoft 365* from the provider's
  `DisplayName()`. A channel whose provider key is not the slot's current `Key()` is
  unavailable, never sent through another provider.
- **A new write direction.** `Send`, `Mark` and `EnsureFolder` write into a mailbox on a
  consumer's behalf, which is a direction no rule sanctions yet. Module boundaries
  therefore gains **rule 15**: Communications writes through `ChannelMailbox`. Under
  rule 15, Communications is the authority on which mailboxes are channels, the provider
  writes only to a mailbox `Check` resolved, `Key` and `Status` make no network call,
  nothing is called inside a transaction, and every write is idempotent or reports its
  outcome. This is rule 14's discipline, stated for this slot.

```go
type ChannelMailbox interface {
    Key() string
    DisplayName() string
    Status(ctx) (MailboxStatus, error)                       // no network call
    Check(ctx, address string) (ChannelMailboxInfo, error)    // resolve and prove reach
    Forget(ctx, mailbox string) error                        // the channel is gone
    MaxMessage() int64                                       // the largest MIME Send takes
    Send(ctx, mailbox string, mime []byte) error             // a whole RFC 5322 message
    Delta(ctx, mailbox, folder, link string, since time.Time) (MailDelta, error)
    Raw(ctx, mailbox, messageID string) (io.ReadCloser, error) // /messages/{id}/$value, capped
    Mark(ctx, mailbox, messageID string, op MailMark) error  // read | category | move
    EnsureFolder(ctx, mailbox, name string) (string, error)
    Watch(ctx, mailbox, folder string) error
    Unwatch(ctx, mailbox, folder string) error
    Notified(ctx, since time.Time) ([]MailboxNotice, error)
}

type ChannelMailboxInfo struct {
    Mailbox, Address, DisplayName string  // Mailbox: the object id
    Shared bool                           // a shared mailbox, not a person's
}
```

- `Check` reads `/users/{address}?$select=id,mail,displayName,accountEnabled`, then
  `/users/{id}/mailboxSettings/userPurpose`, then `/users/{id}/mailFolders/inbox`.
  `Shared` is `userPurpose = shared`. `accountEnabled = false` alone is not enough, since
  a leaver's disabled personal mailbox has it too, and it is used only as the fallback
  M10 names. A 403 is `ErrMailboxDenied` (outside the scope), and a 404 is
  `ErrMailboxUnknown`.
- Entra records nothing about channel mailboxes in `entra.accounts`. **The channel is the
  authority on which mailboxes are reached**, and Entra answers only for an object id that
  `Check` resolved. For that, Entra keeps `entra.channel_mailboxes(object_id PK, address,
  checked_at)` (in `00090`), written by `Check` and deleted by `Forget`, which
  Communications calls when a channel is deleted or switched back to SMTP.
- Errors: `ErrMailboxUnavailable` (switch off, no credential), `ErrMailboxDenied`,
  `ErrMailboxGone`, `ErrMailboxUnknown`, and a new `ErrMailTooLarge` (M13).

### M12 — Microsoft 365 channels (Communications)

- `provider = 'entra'` (the slot's `Key()`). The column has no CHECK, so the value needs
  no migration. **The channel's own settings need one.** `settings_json` today lives on
  `channel_credentials`, beside a `secret_ciphertext NOT NULL` (00006:50-56), and the
  outbox reads it through a LEFT JOIN (`queries/outbox.sql:80-85`). C's migration
  `00091_communications_channel_settings.sql` adds
  `channels.settings jsonb NOT NULL DEFAULT '{}'` for settings that are not a secret's.
  A Microsoft 365 channel holds `{mailbox, onClose?, folder?}` there, and **no
  `channel_credentials` row**, so there is no secret to seal. SMTP channels keep their
  credential row as today. `GetOutboundMessageForSend` selects `ch.provider` and
  `ch.settings`.
- The DTO's `settings` becomes a `oneOf` by provider. The SMTP shape is unchanged, the
  Microsoft 365 shape is `{mailbox}`, and the dead Mailgun request
  (`MailgunChannelCredentialRequest`) leaves the contract.
- **Create** (`communications:channels-manage`): `{type: email, provider: entra,
  address, displayName}`. The handler calls `Check`, then:

  | `Check` answers | Result |
  | --- | --- |
  | reach, `Shared` | created **active**: it can send at once (C), and receives once D ships |
  | reach, not `Shared` | 422 `personal_mailbox`: shared mailboxes only (Q4) |
  | `ErrMailboxDenied` | 422 `mailbox_denied` ("outside the application's scope"; links to the guide) |
  | `ErrMailboxUnknown` | 422 `mailbox_unknown` |
  | `ErrMailboxUnavailable` | 409 `mail_channels_off` |

- **Switch an SMTP channel**: `POST /channels/{id}/provider` with `{provider:
  entra}` keeps the id, the conversations and the default flag. It runs `Check` on
  the channel's address, sets the settings, and deletes the SMTP `channel_credentials` row
  in the same transaction. There is no way back short of entering SMTP settings again.
- **Verify** for a Microsoft 365 channel runs `Check` again.
- `ChannelForm.tsx`: the provider select offers *SMTP* and *Microsoft 365*, with
  Microsoft 365 only while `mailChannels.enabled`. The Microsoft 365 form is address and
  display name, plus the M17 options. The Mailgun branch is deleted.

### M13 — Sending (C)

- `GetOutboundMessageForSend` selects the channel's `provider` and `settings` (M12).
  `deliver` branches **after** the `delivery_attempted_at` stamp and before the send:
  SMTP as today, and Microsoft 365 builds the same `mail.Outbound` into MIME with the
  existing builder and calls `ChannelMailbox.Send`. The deterministic Message-Id,
  In-Reply-To and References are ours because the message is MIME (research §3).
- Graph's 202 is acceptance, the same as SMTP's 250. At-least-once and the possible
  duplicate metric are unchanged.
- `ErrMailboxDenied`, `ErrMailboxGone` and `ErrMailboxUnknown` are terminal for the job
  (`jobs_failed`) with the code. `ErrMailboxUnavailable` and throttling are retried on the
  existing backoff.
- **Size is refused when the mail is written, not when it is sent.** The attachment
  limit allows 10 MiB per file by default and up to 50 MiB (`config/config.go:1161`),
  far above Graph's MIME limit. So for a Microsoft 365 channel, reply and create refuse
  with 422 `too_large` when the attachments' total, plus a 37 % base64 margin, exceeds
  the channel's limit. The provider answers that limit as `ChannelMailbox.MaxMessage()`.
  The composer shows the limit beside the attachments. `ErrMailTooLarge` remains as the
  worker's last guard, and is terminal. Drafts with upload sessions are left out (M18).
  *Before C, check the MIME limit and that Exchange keeps our Message-Id (research §3).
  If Exchange rewrites it, threading falls back to Graph's `conversationId` (M15), and
  the reference says so.*

### M14 — Receiving (D)

**Schema** (`00092_communications_inbound.sql`). The migration also **backfills** a
`channel_sync` row for every Microsoft 365 channel created under C, with `import_from =
now()`, so a channel made before D receives from the day D ships and does not import its
past:

```sql
CREATE TABLE communications.channel_sync (
    channel_id      uuid PRIMARY KEY REFERENCES communications.channels (id) ON DELETE CASCADE,
    folder          varchar(40) NOT NULL DEFAULT 'inbox',
    delta_link      text,
    import_from     timestamptz NOT NULL,
    state           varchar(20) NOT NULL,       -- pending | active | failed | stopped
    error           varchar(40),
    next_sync_at    timestamptz NOT NULL,
    last_synced_at  timestamptz,
    attempts        integer NOT NULL DEFAULT 0,
    lease_id        uuid,                       -- the holder's token
    lease_until     timestamptz,
    notified        boolean NOT NULL DEFAULT false,
    updated_at      timestamptz NOT NULL
);

CREATE TABLE communications.inbound_receipts (
    id                  uuid PRIMARY KEY,
    channel_id          uuid NOT NULL REFERENCES communications.channels (id) ON DELETE CASCADE,
    provider_message_id varchar(400) NOT NULL,
    rfc_message_id      varchar(998),
    message_id          uuid REFERENCES communications.conversation_messages (id) ON DELETE SET NULL,
    outcome             varchar(20) NOT NULL,   -- imported | duplicate | skipped | failed
    reason              varchar(40),
    received_at         timestamptz NOT NULL,
    created_at          timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_inbound_receipts_provider ON communications.inbound_receipts (channel_id, provider_message_id);
CREATE UNIQUE INDEX ux_inbound_receipts_rfc ON communications.inbound_receipts (channel_id, rfc_message_id)
    WHERE rfc_message_id IS NOT NULL AND outcome = 'imported';
```

The Message-Id index covers only `imported` receipts. A `duplicate` receipt carries the
same Message-Id as the one it duplicates, and must not collide with it.

`provider_message_id` is Graph's immutable id: Entra asks with `Prefer:
IdType="ImmutableId"`, so a move inside the mailbox keeps it.

**The worker `communications-mailbox-sync`** claims a due channel by the
conditional-update pattern the outbox uses, with no advisory lock (the reference's rule).

- **The lease.** The claim sets a fresh `lease_id` and `lease_until = now + 2 min`. The
  holder renews it (`lease_until = now + 2 min WHERE lease_id = $mine`) before each
  message. **Every write to `channel_sync` is guarded by `WHERE lease_id = $mine`**. A
  holder whose lease was lost finds its write matching nothing, and stops the cycle
  without storing a `delta_link`. So a stale holder never writes back an older link.
- **A message's import is idempotent by its receipt.** A unique violation on
  `(channel_id, provider_message_id)` rolls back that message's transaction and is
  counted as already imported. Its storage objects are queued on the cleanup ledger. A
  second holder that overlapped therefore does no harm.

The cycle:

1. Calls `Delta` from `delta_link`, or on first run from `import_from` with
   `receivedDateTime ge`. `ErrMailboxDeltaExpired` restarts from the newest receipt's
   `received_at`, and the receipts dedupe the overlap.
2. Takes at most **50 new messages per cycle** per channel, oldest first. For each id it
   does not already have a receipt for:
   1. `Raw` (capped at `COMMUNICATIONS_INBOUND_MAX_BYTES`, default 25 MiB; larger is
      `skipped`/`too_large`, recorded with no message, and the conversation is not
      created);
   2. writes the raw object to storage (`communications` scope) **before** the rows;
   3. parses it with `net/mail` and `mime/multipart` (quoted-printable, base64, RFC 2047
      and RFC 2231 names, `charset` through `golang.org/x/text`);
   4. in one transaction: the receipt, the participant, the conversation (M15), the
      message (`direction='inbound'`, `rfc_message_id`, `raw_payload_storage_key`,
      `channel_metadata_json {from, to, cc, inReplyTo, references, autoSubmitted,
      graphConversationId}`), and the attachments (each written to storage before the
      transaction, and on a rollback queued on the cleanup ledger).
3. Stores the new `delta_link`, `last_synced_at` and the next run: **5 minutes** without
   notifications, **1 hour** with them as the safety net (E), at once while more remain.

A receipt is written for every outcome:

- `duplicate`: an `rfc_message_id` already imported in the channel.
- `skipped` with `too_large`.
- `skipped` with `own_message`: a Message-Id of the form `hex(id)@vantigo.invalid` whose
  id is **one of this database's own** `conversation_messages`. Mail from another
  Vantigo installation, such as a customer who also uses Vantigo, has the same suffix and
  is imported normally.
- `failed`: an unparseable message is kept as raw, with the text "Vantigo could not read
  this message" and the raw available to download.

The channel never stalls on one message.

**HTML**: sanitised on write with `github.com/microcosm-cc/bluemonday`'s UGC policy.
Inline `cid:` images are rewritten to the attachment download path. Remote images are
**stripped** (no tracking pixels), and links get `rel="noopener noreferrer"` and
`target="_blank"`. The frontend's iframe sandbox stays as a second wall.

A text body is taken from `text/plain`, or derived from the sanitised HTML when only
HTML exists, so the AI context and the preview always have text. Because of that, the
thread **renders the HTML body whenever it has one**, and falls back to the text. Today
it shows HTML only when there is no text (`ConversationThread.tsx:164`), which would
hide every inbound HTML mail behind its derived text. The thread also gets a **Show as
text** toggle.

**Attachments from strangers are not scanned.** An inbound attachment is written with
`scan_status = 'unscanned'`, not the column's default `'clean'` (00006:238). `ready`
stays derived from `scan_status = 'clean'`, so download is widened to allow `unscanned`
for inbound, with a warning in the thread. This keeps the reference's statement that
`clean` never means inspected, and makes the inbound case honest. Outbound uploads keep
`clean` as today.

**Auto-replies**: `Auto-Submitted` other than `no`, `X-Auto-Response-Suppress`,
`Precedence: bulk|auto_reply|list` or `X-Autoreply` set `autoSubmitted`. Such a message
**does not reopen** a closed conversation and is marked in the thread. Vantigo sends
nothing automatically, so there is no reply loop to break.

### M15 — Threading, the linker and reply

- **Threading**, in order: the first `In-Reply-To`, then each `References` id from
  newest to oldest, matched to `rfc_message_id` **within the channel**. Then the Graph
  `conversationId` stored in a message's metadata, within the channel and the last 90
  days. Otherwise a new conversation (`status='open'`, subject from the message). An
  inbound message to a `closed` conversation reopens it, and to an `archived` one starts
  a new conversation.
- **The linker** asks `contracts.CustomerDirectory.ContactsByEmail` for contacts by the
  sender's address. The method already exists, normalises the address, and takes no
  principal (`contracts/directory.go:202`, `customers/directory.go:143`), so the worker
  calls it directly with no `AsUser`.
  - One customer: `customer_id` is set, with `customer_association_source='automatic'`,
    and `participants.contact_id` is set.
  - Several customers: `conversation_customer_candidates`, with no customer set.
  - None: nothing, and the AI suggestion stays available.

  A conversation with a manual customer is never re-linked.
- **Reply comes alive** as the design always intended. It ports .NET's rules
  (inventory §4.3–4.4) unchanged:
  - `resolveReplyRecipients`: the latest inbound participant;
  - `replyRecipientsOf`: query-backed;
  - reply-all's CC: the latest inbound message's `cc` without the mailbox and the
    sender, capped at 100;
  - `queueReply` writes `{inReplyTo: latest inbound rfc_message_id, references: its
    references + it, max 20}`.

  The AI draft's context fills itself (`ListInboundMessagesForAiContext`).

### M16 — Personal data and retention

- Inbound messages age out by `created_at` like the rest. Their raw payloads and
  attachments are already queued on the cleanup ledger by `deleteMessages`. Receipts go
  with their channel, or are swept by retention once older than the window and their
  message is gone.
- **Export and erase** stay keyed by customer. The gap the research names (an inbound
  conversation with no customer is invisible to both) is closed for the linker's case,
  because a matched sender is linked. A sender who matches no customer remains outside
  both. The reference states this on the record, as it already does for unlinked
  participants.
- Raw payloads are not exported, matching attachments' bytes today.

### M17 — E: notifications, Outlook state and the import window

- **Notifications**: `Watch(mailbox, 'inbox')` with the application token, on
  `users/{oid}/mailFolders('inbox')/messages`, change type `created`.
  `entra.mailbox_subscriptions` gains `resource varchar(40) NOT NULL DEFAULT 'events'`,
  and its key becomes `(object_id, resource)` (in `00093`). The webhook and its checks are
  shared, and `Notified` answers the resource, so Communications sees only `messages`
  notices. A notice sets `channel_sync.notified`, and the worker takes notified channels
  first.
- **Outlook state on close** (per channel, `channels.settings.onClose`): `none` (default),
  `read`, or `move` to a folder Vantigo creates (`EnsureFolder`, default name *Handled in
  Vantigo*). It runs on a conversation's close, for the inbound messages of that
  conversation, through the outbox-style job (`mailbox_marks`, at-least-once and
  idempotent). Nothing flows back: a message read, moved or deleted in Outlook changes
  nothing in Vantigo, because Vantigo keeps the record.
- **The import window** at create: *From now on* (default), *the last 30 days* or *the
  last 90 days*, written to `channel_sync.import_from`.

### M19 — Permissions Vantigo does not use (Entra, A)

The owner's answer to Q3. The connection test already reads the token's `roles` and
stores them as `PermissionsPresent`. It now also derives `permissionsUnexpected`: every
role in the token that is not one of the following.

- The eight required permissions (`connection.go:35-44`).
- `Calendars.ReadWrite`, only between A and B, while the calendar still uses it.
- The mail roles `tenant_wide` reports. Those are reported there, not twice.

`GET /connection` answers `permissionsUnexpected: string[]` beside
`permissionsMissing`. The test audits an `unexpected:<permission>` code for each. The
Entra page's **Status** card shows: *"The application holds permissions Vantigo does not
use: … They may be left from an earlier setup, or wider than needed. Remove them under
API permissions unless another product uses this registration."*

It is **a warning, never a status**: the connection stays `connected`, and nothing is
refused. After B, a leftover application `Calendars.ReadWrite` consent shows here,
which is the clean-up the administration guide asks for. The delegated permissions of
the Outlook connection (M2) are never in an application token's `roles`, so they never
count as unexpected.

### M18 — Out of scope

- Delta of folders other than the Inbox, and Sent Items written outside Vantigo.
- Drafts and upload sessions for messages above the MIME limit.
- A person's mailbox as a channel, and delegated shared-mailbox access.
- Attachment scanning (the reference's standing gap).
- Supplier invoices from `faktura@`, receipts to Expenses, out-of-office into Planning,
  and mail on the customer's 360. Each is a follow-up that reads conversations, and none
  changes this design.
- **F** (personal sending): `Mail.Send` added to the delegated scopes by M4's incremental
  ask; a composer choice *Send from my address*; the outbox calling a
  sibling slot, `PersonalMailbox.Send`, with the person's token. A reply
  then arrives in the person's own Inbox and is not captured, unless F also takes
  `Mail.ReadBasic` and a subscription filtered by conversation. F decides that.

## The owner's answers (2026-10-29)

- **Q1 — People without a certain account: refused.** M6 stands. Only a person whose
  Staff record names an Entra account the module manages may connect.
- **Q2 — A leaver's calendar copies: left in the disabled mailbox.** M7 stands, and the
  guide states it. The calendar design's 2026-10-27 decision to delete them is superseded
  for delegated access.
- **Q3 — Warn, and widen the warning.** A tenant-wide `Mail.*` grant is a warning (M10).
  So is **any application permission the principal holds that Vantigo does not use**,
  because it points to a setup that is wrong or too wide (M19).
- **Q4 — Shared mailboxes only.** M12 refuses every mailbox that is not shared
  (`personal_mailbox`), with no confirmation to override it.

## Deliveries

- **A** (`feat/entra-outlook-connection`): M2 (minus B's removals), M3–M8, M19. Migrations
  `00088_entra_outlook_connect.sql` and `00089_entra_outlook_connections.sql`. Docs:
  - the Entra reference (the connection, the flow, the states, the endpoints, the events,
    `permissionsUnexpected`);
  - module boundaries (the host's *Connections* page slot and the `connection.button` slot);
  - in both languages: the Entra administration page (*Outlook connection*: the redirect
    URI, the delegated permissions, admin consent, verifying by connecting yourself, the warning for
    permissions Vantigo does not use, what
    can go wrong: a missing redirect URI, missing admin consent, Conditional Access and
    reconnects), and the
    user guide's new page `user/connections.md` (connecting, reconnecting, disconnecting), listed in the user overview;
  - `admin/installation.md` (the redirect URI is on `APP_URL`);
  - the roadmap.

  *As built (delivery A, 2026-10-29;
  [the plan](../plans/2026-10-29-entra-outlook-connection.md)):* where A departs from
  this design —
  - **M2**: the `calendarSync` object goes in A; its `permission_missing` advisory
    moves to `outlookConnect.status` until B — while the switch is on and the
    connection connected, when the last test's token lacks `Calendars.ReadWrite`,
    after `unavailable` and before `consent_missing`, `ready` and `untested` — beside
    `Calendars.ReadWrite` in `permissionsMissing` while the switch is on and step 3's
    *(for calendar sync)* label. While the switch is off only removals get a token. `outlookConnect`
    also answers `redirectUri`, which the Entra page shows to copy. The page asks before
    it saves the switch turned off while anyone is connected or waiting to reconnect.
  - **M3**: `refresh_sealed` is `bytea`, as every sealed column of the module;
    `account_checked_at` and `ix_entra_outlook_connections_changed` are added.
  - **M4**: connect refuses `account_taken` early as well (complete still decides). The
    callback has a code this design's list lacks, `consent_missing`: with one feature, a
    connection without it is none, so nothing is stored and the switch's status reads
    `consent_missing`. A sealed connected cookie over 3,800 bytes answers `failed`
    rather than be dropped by the browser. A complete with another session's or another
    person's cookie answers `410 expired`, as if there were none. Connect and complete
    share a rate limit, `entra-outlook` (30 a minute); the callback has
    `entra-outlook-callback` (60).
  - **M5**: a CAE claims challenge moves the row to `reconnect` with `sign_in_again`;
    `connectionNeeded` is Entra's own error until B adds `contracts.ErrConnectionNeeded`.
  - **M6**: the daily account read runs in A, in `entra-outlook-sweep`, for the rows in
    `reconnect` (at most 100 a cycle); an `active` row's account is checked on use
    against the module's own row, closing it (`deleted` or `disabled`) with its token
    forgotten. Complete reads the switch and the account again under their locks (the
    connection's row `FOR UPDATE`, the account's `FOR SHARE`), so completes are
    serialized. The callback never marks the client secret failed; a new connect
    clears an earlier one's connected cookie.
  - **M7**: a leaver's row closes inside Deactivate's transaction, with the pass, its
    token nulled; `Delete` of a user the tenant no longer holds now marks that user's row
    `deleted` (it had marked nothing, for want of the object id) and closes its
    connection, when the module created it or its adoption is confirmed. A refused
    refresh of a closing row forgets its token; a spent closing row goes once a day
    has passed since both its last use and its close; every close decided on a read
    is revision-guarded. `entra.outlook.closed` carries `leaver`, `deleted`, `disabled`,
    `switch_off` or `repointed`; the module's own system actor is
    `system:entra-outlook`.
  - **M8**: the host does import `@vantigo/entra-ui`, as it composes `EntraPage` today;
    what must not import Entra is the calendar package, which opens the
    `connectionButton` prop. `GET /outlook/connection` is always 200 with `offered` and
    `reason` (`switch_off`, `no_account`), not a 404, so the page and the button can tell
    "not connected" from "nothing to connect". `/settings/connections` answers not found
    while Entra is off; Disconnect is on that page only; a completed connect returns by
    replacing the callback's address in the history. The calendar guide changes in A,
    since A changes the calendar's frontend.
  - **M19**: in A the `Mail.*` roles and `full_access_as_app` count as unexpected; C
    moves them under `tenant_wide`.
- **B** (`feat/calendar-outlook-delegated`): M9, and M2's removals. No migration beyond
  A's. A and B ship in the same release (M2). Docs:
  - the calendar and Entra reference (the token source, `ErrConnectionNeeded`,
    `Connections`, parking and waking, the availability values, subscriptions with the person's token);
  - module boundaries (rule 14's note);
  - in both languages: the calendar user guide (*Connect Outlook* in the Outlook card,
    *Reconnect*), the Entra administration page (the calendar sync section rewritten,
    `Calendars.ReadWrite` application and its RBAC procedure removed, a leaver's copies
    per Q2), and the calendar administration page;
  - the calendar Outlook-sync spec's as-built note, pointing here.

  *As built (delivery B, 2026-10-29, branch `claude/outlook-calendar-delegated`;
  [the plan](../plans/2026-10-29-calendar-outlook-delegated.md)):* where B departs from
  this design —
  - **M2**: what B removes is `outlookConnect.status`'s `permission_missing` (where A put
    it), with `Calendars.ReadWrite` out of `permissionsMissing` and step 3's *(for
    calendar sync)* label; the order is `off`, `unavailable`, `consent_missing`, `ready`,
    `untested`. A leftover application `Calendars.ReadWrite` is now one of
    `permissionsUnexpected` (M19).
  - **M9, the contract**: `ConnectionChange` carries `At` beside `{Mailbox, State}`, and
    `Connections` answers at most `ConnectionChangesMax` (5000) oldest first, so the
    worker moves a cursor as it does for `Notified`. `ErrConnectionNeeded` is answered as
    a `*ConnectionNeededError` with the reason `not_connected`, `closing` or the
    reconnect code. The removals are `DeleteCalendar`, `Find`, `Unwatch`, and `Apply`
    when every op is a delete (always under `cleanup`); a removal with no token **or no
    connection row at all** answers `ErrMailboxGone`, since nothing can ever remove those
    copies. `ErrMailboxDenied` now means a mailbox Exchange refuses, not a permission
    scope. The markers keep the client id, now read from the connection's row — the same
    value — so copies written before B still match.
  - **M9, subscriptions**: `Watch` asks for the person's token before it writes anything;
    `Unwatch` with no token left **forgets the subscription row** (Graph drops a
    subscription whose creator's grant ended), so the sweep deletes a leaver's closing
    row a day after the close rather than seven. A person waiting to reconnect is parked
    before `Watch`, so their subscription lapses with no new code.
  - **M9, the availability**: `not_connected` and `reconnect` are the worker's last
    finding — the caller's row on, `unavailable`, its error one of the two — and come
    **after** `no_account`, a fresh read of Staff, rather than before it. The calendar
    does not store the reason: the card's `connection.button` slot (Entra's) shows it.
    `PUT /mailbox-sync` and **Sync now** are taken while the availability is
    `not_connected` or `reconnect`, and the card refetches every 15 seconds while it is
    one of the two.
  - **M9, parking and waking**: a parked person keeps `attempts` and is re-checked every
    24 hours by the next sync, which runs the cycle's steps (status, access, account)
    before its first provider call. A change from `Connections` **overrides the backoff
    and the parking** (unlike a notice), marking only rows written before the change. A
    connection gone runs the clean-up before parking, only for a person who has had
    copies and was not parked `not_connected` already.
  - **M6**: the daily `accountEnabled` read stays where A built it, in
    `entra-outlook-sweep`; the calendar's re-check only notices the close, which
    `Connections` wakes it for.
  - **M7, Q2**: as designed — a disconnect and the switch off remove the copies on the
    closing token; a leaver's, an account closed on use or by the sweep, are forgotten
    and stay in the disabled mailbox.
- **C** (`feat/communications-m365-channels`): M10–M13. Migration
  `00090_entra_mail_channels.sql` (the switch and `entra.channel_mailboxes`) and
  `00091_communications_channel_settings.sql`. Docs:
  - the Entra reference (the switch, `mailChannels`, `ChannelMailbox`,
    `entra.channel_mailboxes`);
  - the communications reference (the provider, the switch of provider, sending through
    Graph, SMTP AUTH's end in Exchange Online);
  - module boundaries (the `ChannelMailbox` slot and rule 15);
  - in both languages: the Entra administration page (*Mail channels*: the RBAC commands,
    `tenant_wide`), and the communications user guide (adding a Microsoft 365 channel,
    switching from SMTP);
  - the API reference regenerates.

  *As built (delivery C, 2026-10-29, branch `claude/communications-m365-channels-c`;
  [the plan](../plans/2026-10-29-communications-m365-channels.md)):* where C departs from
  this design —
  - **M10**: `mailChannels` also answers `servicePrincipalId` (the connection's service
    principal, read by the test), which the page fills into `New-ServicePrincipal
    -ObjectId`; `tenant_wide` comes after `unavailable`, so it needs the connection
    `connected`. `full_access_as_app` stays in the list but is an *Office 365 Exchange
    Online* role, never in a Graph token's `roles`: the test cannot see it. The
    `Application MailboxSettings.Read` role exists in RBAC for Applications, so there is
    **no `accountEnabled` fallback**; a 403 on `userPurpose` is `ErrMailboxDenied`.
    The switch's label says *send through*, since C only sends. The tenant-wide roles
    include `MailboxSettings.Read`/`ReadWrite`, `MailboxItem.Read.All`,
    `MailboxItem.ImportExport.All` and `MailboxFolder.Read.All`/`ReadWrite.All` besides
    the five `Mail.*`; one warning in both modules adds that anyone who manages channels
    can then send as any shared mailbox. *Ready* claims only what the test measured and
    names `Test-ServicePrincipalAuthorization`.
  - **M11, the contract**: only the seven methods C calls — `Key`, `DisplayName`,
    `Status`, `Check`, `Forget`, `MaxMessage`, `Send`; D and E add theirs. `Send` takes
    `bcc []string` beside the MIME: without blind copies the MIME goes to `sendMail`;
    with them it is created as a **MIME draft** (whose `Bcc:` header Graph documents), its
    `bccRecipients` checked, then sent, the draft deleted best-effort on a failure
    (owner question 1). `mail.RenderMIME` with `WithBcc` writes the header go-mail leaves
    out. `Status` reuses `MailboxStatus`; `available` with the reason `tenant_wide` is
    M10's warning. `Check` resolves by user principal name, then `$filter=mail eq`, and
    refuses an address that is not a bare addr-spec before any call, and keeps the typed
    address out of errors and logs. It **records only a shared mailbox**; a personal one
    is answered, never recorded, so `Send` refuses it. `sendMail`, the draft's create and
    its send are `noRetry`. `MaxMessage` is 3 000 000 bytes of MIME; a 400
    `ErrorMessageSizeExceeded` (or `MessageSizeExceeded`, `ErrorMessageTooBig`) is
    `ErrMailTooLarge` too. A draft that lost a blind copy is a new
    `contracts.ErrBccUnsupported`, terminal. A process that dies between the draft's
    create and its send leaves the draft in the mailbox's Drafts.
  - **M11, `Forget`**: there is no delete of a channel (conversations restrict it), so
    `Forget` runs only on the way back to SMTP, after the commit, best-effort, and is
    skipped when another channel has taken the mailbox meanwhile; a deactivated channel
    keeps its mailbox.
  - **M12**: `ChannelResponse.settings` stays the SMTP shape (`domain` and `region`
    gone), null for Microsoft 365, beside a new `mailbox: {id}` — no `oneOf`. One channel
    per mailbox: the unique index `ux_channels_entra_mailbox` (409 `mailbox_exists`, apart
    from `channel_exists` for an address). The stored address is `Check`'s primary address
    on create; the switch keeps the channel's own, which may be an alias — what Exchange
    does with it is unverified. Create, switch and verify answer 503 `provider_unreachable` when Graph
    does not answer, and verify 422 `mailbox_changed` when the address now names another
    mailbox. The frontend reads `GET /channel-providers`, never Entra's API. The way back
    to SMTP is `PUT /channels/{id}` with `provider: smtp` and a full SMTP block, the
    password included (owner question 3) — the API's alone: the channels page has no
    control for it. A `PUT` without `provider` keeps the channel's own. Create, switch
    and verify share a rate limit, `communications-channel-check` (30 a minute).
    Communications gains `contractscalls.go` and its in-transaction guard.
  - **M13**: the terminal codes are `mailbox_denied`, `mailbox_gone`, `mailbox_unknown`,
    `too_large`, `bcc_unsupported` and `channel_misconfigured`; the job fails after one
    attempt, `last_error` stays the constant, and the code goes in the failure event's
    `data_json` and the log (owner question 2). An **unavailable provider** (the switch
    off, the connection failing, Entra off, no slot or another key) is not retried on the
    attempts: the job is held, its attempt given back, tried again at most 5 minutes
    apart; the hold's start writes one `retrying` event per sendable delivery with the code
    `provider_unavailable`, held tries count on `communications.outbox.jobs_held`, and 24
    hours after queueing the job fails as `provider_unavailable`. A `retry` job claimed
    with its send marker set counts as a possible duplicate. The size check is on
    **reply** (422 `too_large`) and at **staging** (413 `attachment_too_large`), not
    create, which carries no attachments; the bodies count × 3 (quoted-printable at its
    worst). `ConversationDetailResponse` answers `maxMessageBytes` and
    `maxAttachmentBytes`, `(budget − 16 KiB) × 100 / 137` rounded down to 0.1 MB (2.1 MB),
    which the composer and the staging's 413 show. Reply is unreachable until D, so its
    check has no live caller yet.
  - **Not verified on a real tenant** when the pages were written: that Exchange keeps
    our `Message-ID` on a MIME `sendMail` (and on a MIME draft), and that a draft's `Bcc:`
    header reaches the blind copies. The pull request's real-tenant checklist settles
    both, and how Exchange sends from an alias address; the reference states them as
    unverified until then.
- **D** (`feat/communications-inbound`): M14–M16. Migration
  `00092_communications_inbound.sql` (with the backfill), and the dependency `bluemonday`. Docs:
  - the communications reference (the outbound-only section replaced by *Receiving*, the
    worker, receipts, threading, sanitising, the linker, reply and reply-all, the AI
    context, retention and personal data);
  - in both languages: the communications user guide (the inbox receives, reply-all,
    auto-replies, a message Vantigo could not read);
  - the design's §1.1 marked superseded.

  *As built (delivery D, 2026-10-29, branch `claude/communications-m365-inbound-d`;
  [the plan](../plans/2026-10-29-communications-m365-inbound.md)):* where D departs from
  this design —
  - **M11, the contract**: `Delta(ctx, mailbox, folder, link, since)` answers **one
    page**, `MailDelta{Items, Link, Done}`, at most `MailDeltaPageMax` (50) items in no
    promised order; `Raw(ctx, mailbox, messageID, max)` takes **the caller's cap** —
    Communications' `COMMUNICATIONS_INBOUND_MAX_BYTES` (25 MiB, bounds 1–150 MiB), not the
    provider's — and answers **`[]byte`**, not an `io.ReadCloser`: the guarded client
    buffers through its cap anyway, and the caller stores and parses the whole. A new
    `contracts.ErrMailItemGone` (a 404 `ErrorItemNotFound` between the delta and
    `$value`) is terminal for the message; `ErrMailboxDeltaExpired` is the calendar's,
    reused with its codes. Both reads are behind `Send`'s gate (a recorded mailbox, no
    call otherwise). Each page asks `Prefer: IdType="ImmutableId", odata.maxpagesize=50`;
    a fresh one `$filter=receivedDateTime ge` and a `$select` of four fields; `changeType`
    is not asked and no `$orderby` — the worker sorts each page. A page is read through
    16 MiB, not 1 MiB. Every link, stored or answered, must be this mailbox's message
    delta on Graph's host, compared case-insensitively (`mailfolders/{id}` passes), and the
    request is rebuilt from our own path plus the link's query. `Raw` runs on a second
    `http.Client` with `graphRawBudget`, 90 s.
  - **M14, the schema** (`00092`): `lease_id` is `varchar(32)`, the outbox's hex form, not
    `uuid`; the CHECKs on `channel_sync.state` and `inbound_receipts.outcome`,
    `ix_channel_sync_due`, the receipts' `(channel_id, received_at DESC)` and `message_id`
    indexes, and the expression index on `channel_metadata_json->>'graphConversationId'`
    are additions. The rfc unique index covers `imported` receipts only, so a `duplicate`
    carries its original's Message-Id. The backfill writes `pending` for inactive `entra`
    channels too; the claim skips them. `channel_sync.notified` is created and ordered on,
    set by nothing until E; nothing sets `stopped`.
  - **M14, the worker**: **no cap per cycle below the page** — a cycle reads one page
    and handles each of its items, and a page with more to come stores the next link with
    `next_sync_at` now, so the drain claims the channel again at once. A page's link is
    stored only once every item has a receipt; within an import **the receipt is written
    last** in the transaction, so its unique index decides a race and nothing of a lost
    one commits. Timings: a 30 s poll, a 2-minute lease renewed before each message, 5
    minutes after a finished page and for a held provider (no attempt spent), `failed`
    for an hour on `mailbox_denied`/`mailbox_gone`/`mailbox_unknown`, else `sync_failed`
    with 1 min × 2^attempts, at most an hour. One counter,
    `communications.inbound.cycles_failed`, a plan addition. The Message-Id is the
    message's own header, **falling back to Graph's `internetMessageId`** when the header
    block does not read or names none. `own_message` only for an id of this database.
    An unreadable message has no sender: it threads nowhere, links no customer and is not
    one a reply answers. Bodies are capped at 1 MiB each — a longer text cut with `…`, a
    longer HTML dropped (`htmlDropped`) —, and the sanitiser keeps a short style
    allow-list (plan decision, owner question 7), which UGC alone would strip.
  - **M15**: references on a reply are the answered message's own then its Message-Id,
    distinct, **the first and the last 19** when there are more than 20 (owner question
    1, default), not .NET's oldest 20. A conversation with **any** customer, automatic
    included, is never re-linked (owner question 6, default); a new conversation takes the
    one customer at its insert. Threading's third step finds only an inbound message's
    `conversationId`: if Exchange rewrites our Message-Id, a customer's first answer to a
    conversation Vantigo began starts a new conversation. Reply-all is the Cc of the latest
    inbound message only (owner question 2, default); a reply goes to `From`, not
    `Reply-To` (question 5, default).
  - **M15, the contract** (`openapi/communications.yaml`): each message's
    `autoSubmitted`, `unreadable` and `rawDownloadPath`, and `ChannelResponse.sync
    {state, error, lastSyncedAt}` (a plan addition), are always answered but **optional in
    the schema**, since the recorded exchanges predate them. `GET
    /messages/{id}/raw` (`conversations-view`) answers the original as `message/rfc822`
    to save, for every inbound message (owner question 3, default). An unscanned
    attachment downloads with `Content-Disposition: attachment; filename*=UTF-8''…` and a
    passive content type, `application/octet-stream` otherwise. Inline images the message
    carries are shown although unscanned, remote ones stripped (owner question 4,
    default).
  - **M16**: the receipts' sweep runs in every retention batch — a receipt with no
    message older than the window goes, except each channel's newest. Raw payloads and
    inbound attachments already left through `deleteMessages`.
  - **Not verified on a real tenant** when the pages were written: whether Exchange keeps
    our `Message-ID`; the delta links' exact form; an expired message delta's code; the
    page size honoured; a message moved into the Inbox as created or updated; `$value`'s
    ceiling, redirects and speed. The pull request's real-tenant checklist settles them,
    and this note and the reference record the findings.
- **E** (`feat/communications-inbound-notifications`): M17. Migration
  `00093_entra_mailbox_subscriptions_resource.sql`. Docs:
  - the Entra and communications reference;
  - in both languages: the communications user guide (the channel's *When a conversation
    is closed* and the import window), and the Entra administration page (notifications
    for mail);
  - the roadmap.

Migration numbers are those free on 2026-10-29, after #194's `00087`. Each delivery renumbers in its own
history before it rebases.

## Testing

- **A**: the flow against a fake authorization server in `graphtest`:
  - state, nonce and PKCE mismatches, an expired cookie, a code for another tenant, an
    `oid` that is not the person's, an account already taken (and a `closing` one taken over), a token
    response missing a scope (`consent_missing`), a callback without a valid state
    cookie writing nothing, `permissionsUnexpected` from a token with extra roles (and
    none from the eight alone), `returnTo` values (`//x`, `/\x`, a scheme, control
    characters);
  - `complete` with another session or another user;
  - the refresh's race (two redemptions, both tokens valid);
  - every M5 code to its `error_code`, and a CAE claims challenge;
  - disconnect to `closing` to deleted;
  - the leaver's step nulling the token;
  - the *Connections* page and the calendar card in both languages.
- **B**: the calendar's worker against the fake with `ErrConnectionNeeded` (parked 24
  hours, `attempts` unchanged, woken within a cycle by `Connections` after `complete`,
  cleaned up on the long re-check after the account is disabled), the clean-up on `closing` (removals allowed, the rest
  refused), subscriptions renewed with the person's token and left to lapse on
  `reconnect`, and markers on the connection's client id. The existing B–D tests run
  unchanged against a delegated token source.
- **C**: `Check`'s four outcomes; create and switch of provider (the credential row gone
  in the same transaction); the outbox's branch, terminal versus retried errors, the
  MIME's Message-Id and threading headers byte for byte, `ErrMailTooLarge`; and the
  `tenant_wide` status from a token with a `Mail.*` role.
- **D**: the lease (a lost lease writes nothing, an overlapping holder imports
  nothing twice), `own_message` only for this database's ids, a `duplicate` beside its
  original, `unscanned` inbound attachments, the backfill; the parser over a corpus in `testdata/mime/` (multipart/alternative, related
  with `cid:`, RFC 2047 and 2231, ISO-8859-1 and Windows-1252, a winmail.dat, a
  malformed one); the sanitiser against an XSS list; dedupe by provider id and by
  Message-Id; threading's order; the linker's three outcomes; a 51-message cycle; an
  expired delta; reply, reply-all and the AI context from a real inbound; and retention
  and erase reaching raw payloads.
- **E**: a `messages` notice making the channel due and an `events` notice not; `onClose`
  `read` and `move` idempotent on a resend; and the import window's filter.
