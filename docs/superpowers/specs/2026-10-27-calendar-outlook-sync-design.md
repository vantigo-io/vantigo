# Calendar — Outlook sync (phase 2, design)

Date: 2026-10-27. Research:
[`docs/superpowers/research/2026-10-27-calendar-outlook-sync.md`](../research/2026-10-27-calendar-outlook-sync.md).
Builds on the [calendar design](2026-10-19-calendar-design.md), the
[feed design](2026-10-20-calendar-feed-design.md) and the
[Entra design](2026-10-19-entra-design.md).

A person who works in Outlook sees in it what Vantigo holds for them — their
appointments, their absences and, when they choose, their tasks, follow-ups and bookings
— and their Outlook busy time shows in Vantigo; moving or editing one of their
appointments in Outlook, on a phone, changes it in Vantigo. The connection is the Entra
module's: an Owner's switch, one admin consent, and a toggle per person.

## Readings

1. **The owner's decisions** (2026-10-27): app-only through the Entra connection; a
   simple per-person toggle in Vantigo, behind an admin switch that also needs the
   permission in Entra; only people whose mailbox is known for certain — no guesswork;
   a person's copies follow their rights (a nightly re-sync and a re-sync on change);
   a leaver's copies are deleted; two-way sync; both phases. On the two-way details: an
   appointment its owner **deletes in Outlook is deleted in Vantigo** (with the
   cancellation that follows); an event created **in the Vantigo calendar** in Outlook
   becomes an appointment; change notifications are **optional, recommended**.
2. **Nothing of a person's is guessed.** The mailbox is the Entra account Staff's
   provider link names for the person whose identity link names the user — the account
   the module made or an Owner confirmed. A user with no Staff person, a person of
   another account kind, or an account the module does not manage is "not available",
   never matched by address.
3. **Vantigo stays the authority.** What it writes is a copy, tagged, without attendees;
   it never touches an event it did not write; an edit made in Outlook is either taken
   in through the appointment's own update path (C) or put back.
4. **Provider-neutral contract, one provider.** The calendar module owns the person's
   setting, the map of what it wrote and the worker; the Entra module owns Graph. They
   meet in a new contract named for the thing, not the vendor, so a Google provider
   could fill it later.

## Decisions

### O1 — Four deliveries

| | Delivery | Direction |
| --- | --- | --- |
| **A** | The switch, the person's setting and the push | Vantigo → Outlook |
| **B** | Outlook's busy time in Vantigo | Outlook → Vantigo, read |
| **C** | The write-back: edits, moves and deletes of one's own appointments, and new events in the Vantigo calendar | Outlook → Vantigo, write |
| **D** | Change notifications, for an installation Microsoft can reach | faster B and C |

Each is a pull request against `main` after the previous one lands. This document
decides A in full and B–D far enough that A does not have to be undone.

### O2 — The admin switch (Entra)

`entra.connection.calendar_sync boolean NOT NULL DEFAULT false`, on the module page's
settings (`PUT /connection/settings`, `entra:manage`, beside the account rules) as
**Calendar sync** — "Let people sync their Vantigo calendar to Outlook". It is not a
required permission of the connection: provisioning keeps working with it off and with
the permission missing. `GET /connection` answers `calendarSync: {enabled, status,
reason?}`:

| `status` | When |
| --- | --- |
| `off` | the switch is off |
| `ready` | on, the connection `connected`, and the last test's token carried `Calendars.ReadWrite` |
| `permission_missing` | on, but the last test did not see `Calendars.ReadWrite` (the wizard says to add it, consent, and test again) |
| `unavailable` | on, but the connection is not `connected` (`reason` is the connection's) |

`permissionsMissing` lists `Calendars.ReadWrite` while the switch is on and the token
lacks it. The permission is never asked for while the switch is off. The administration
guide describes narrowing it with **RBAC for Applications** to a group of mailboxes (and
that Entra's tenant-wide consent must then be removed); a mailbox outside the scope is
answered 403 per person (`mailbox_denied`, O7), never as the connection failing.

*As built:* `permission_missing` is answered only while the connection is
`connected`; on but not connected is `unavailable` with the connection's reason, as the
table says.

### O3 — The certain mailbox (Staff)

`contracts.Workforce` gains `DirectoryAccount(ctx) (*DirectoryAccount, error)`: the
caller's own account in an external directory — the provider link of the person whose
identity link names the principal in `ctx` (a request, or `AsUser`), with an
`external_id` and a state of `enabled` or `disabled`. `DirectoryAccount{Provider,
ExternalID string; Enabled bool}`; `(nil, nil)` for a principal with no person, no
provider link, no external id, or a `deleted`, `preparing`, `failed` or `blocked` link.
It needs no Staff key — it is the caller's own account id, nothing more — and answers
only the principal's own. It is the third read under Staff's own principal (beside
`Self`), provider-gated as rule 12 says.

The Entra side checks again: it writes only to an object id that is a row of
`entra.accounts` the module made or whose adoption is confirmed, and not `deleted`
(`ErrMailboxUnknown` otherwise).

*As built:* `DirectoryAccount` performs no permission check at all — the principal's own
account id, under Staff's own principal — rather than being gated by a key; it finds the
person under `PersonForUser`'s predicate (a disabled identity link included, one blocked
`mismatch` or `account_not_managed` never) and answers the oldest provider link. Its
`Enabled` is not read by the calendar today.

### O4 — The contract: `contracts.MailboxCalendar`

A single-provider slot `MailboxCalendar` (`Module.MailboxCalendar`, `Deps.MailboxCalendar`),
filled by Entra, consumed by the calendar, resolved by `Compose` and by `Workers` (the
worker runs in worker mode). It is the **sixth sanctioned cross-module write**, calendar
→ a provider's directory, held to rule 13's discipline: the provider runs on its own
pool and reaches the network, so the calendar never calls it inside a transaction or
holding a connection — only through `contractscalls.go` accessors that refuse such a
call — and every call is idempotent or answers what it did per operation.

```go
type MailboxCalendar interface {
    Key() string                                  // "entra", no context, no network
    Status(ctx context.Context) (MailboxStatus, error) // no network
    EnsureCalendar(ctx context.Context, mailbox, known, name string) (string, error)
    DeleteCalendar(ctx context.Context, mailbox, calendarID string) error
    Apply(ctx context.Context, mailbox, calendarID string, ops []MailboxOp) ([]MailboxResult, error)
    Find(ctx context.Context, mailbox, calendarID string) ([]MailboxEventRef, error)
}
```

- `mailbox` is the account's `ExternalID` (O3); `calendarID` `""` is the default
  calendar. `EnsureCalendar` answers the id of the calendar named `name` that the module
  made — `known` when it still exists, else a new one.
- `MailboxStatus{State, Reason}`: `available` (O2's `ready`), `cleanup` (the switch off
  or the permission missing, a credential working: `Apply` takes deletes only and
  `DeleteCalendar` works, so a person's copies can still be removed), `unavailable`.
- `MailboxOp{Kind: create|update|delete, EventID, Key string, Event *MailboxEvent}`;
  `MailboxEvent{Subject, Body, Location string; AllDay bool; StartDate, EndDate
  time.Time (civil, EndDate inclusive); StartsAt, EndsAt time.Time; TimeZone string
  (IANA); ShowAs string (free|tentative|busy|oof|workingElsewhere); Private bool}`.
  `Key` is written on create as the event's marker and never changed.
- `MailboxResult{EventID string; Gone bool; ChangeKey string; Err error}`, one per op, in
  order: an update or delete of an event that no longer exists is `Gone` (a delete is
  then done; an update is the caller's to re-create).
- `Find` answers the events carrying the marker — in a secondary calendar, every event
  in it — `{EventID, Key}`, bounded at 5000.
- Refusals, as sentinels: `ErrMailboxUnavailable` (not `available`, or `cleanup` for a
  non-delete), `ErrMailboxUnknown` (O3's check), `ErrMailboxDenied` (403: outside an
  RBAC scope), `ErrMailboxGone` (no such user, or no mailbox:
  `MailboxNotEnabledForRESTAPI`). Anything else is a failure to retry; a throttled
  request is retried by the client within the call, then a failure.

The Entra side: `$batch` of at most 20 per request, sequential batches per mailbox (the
4-concurrent limit), the marker `String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name
VantigoKey`, `isReminderOn: false`, timed events in UTC, all-day events at midnight in
the Windows name of `TimeZone` (the CLDR table the calendar already holds, mirrored) —
UTC when none — and `sensitivity: private` when `Private`.

*As built:* `Status` is `cleanup` only while a credential still works — the tenant and
the client saved, a secret unrevoked and unexpired, and the connection not `failing`
`credential_invalid` — and `permission_missing` only while the connection is
`connected`; the switch off with no working credential is `unavailable` (reason
`switch_off`), so the calendar answers that person `unavailable`. Graph's bare
`ResourceNotFound` is not read as the mailbox gone (only `MailboxNotEnabledForRESTAPI`,
`ErrorInvalidUser`, `ErrorNonExistentMailbox` and `Request_ResourceNotFound`), so an
event or a calendar gone is never mistaken for its mailbox. A `$batch` holding a create
is sent with no retry of a 5xx; 429s inside a batch are sent again once after the
longest `Retry-After`. The `$batch` answer is read through 8 MiB, the client's other
answers through 1 MiB. The calendar hands `Apply` slices of 100 ops, which Entra splits
into batches of 20.

### O5 — The person's setting (Calendar)

`calendar.mailbox_sync`, one row per user who has ever turned it on:

| Column | |
| --- | --- |
| `user_id` | primary key |
| `enabled` | the person's toggle |
| `target` | `default` (their main calendar — blocks time for colleagues) or `own` (a calendar named *Vantigo*) |
| `detail` | `full` or `busy` (titles become *Busy* / *Opptatt*, the feed's busy rule) |
| `kinds` | `text[]`: `calendar.appointment` and the provider kinds chosen |
| `mailbox` | the `ExternalID` last confirmed by O3 — kept for the clean-up alone |
| `calendar_id` | the *Vantigo* calendar's id when `target = own` |
| `state` | `pending`, `active`, `stopping`, `off`, `unavailable`, `failed` |
| `error` | the last refusal's code (`no_account`, `mailbox_denied`, `mailbox_gone`, `unavailable`, `failed`) |
| `next_sync_at`, `last_synced_at`, `attempts`, `reconcile` | the worker's |
| `revision`, `created_at`, `updated_at` | |

`calendar.mailbox_events` is the map: `(user_id, item_key)` primary key, `event_id`,
`content_hash`, `starts_on` (the item's first day, for the window), `created_at`,
`updated_at`. Nothing else of the event is stored.

Three operations under `calendar:access`:

- `GET /mailbox-sync` — the person's setting and state, and `availability`: `available`,
  or why not — `switch_off`, `no_provider` (Entra off), `unavailable`,
  `permission_missing`, `no_account` (O3 answered nothing) — and the kinds they may
  choose (the appointment kind and the provider kinds the sources list today).
- `PUT /mailbox-sync` — `{enabled, target, detail, kinds}` with `If-Match` the revision;
  turning it on while unavailable is refused 409 with the reason; turning it off, or
  changing the target, starts the clean-up (O8).
- `POST /mailbox-sync/run` — "Sync now": `next_sync_at = now`, rate-limited
  (`calendar-mailbox-run`, 6 per client a minute).

The defaults when first turned on: `target = default`, `detail = full`, `kinds =
[calendar.appointment, staff.absence]`. The card is on **Calendar → Settings**, after
the calendar addresses: *Outlook* — the toggle, *Where* (main calendar / a separate
*Vantigo* calendar, with the sentence that only the main one shows them busy to
colleagues), *What* (the kinds), *Titles* (full / *Busy* only), the state and the last
sync, *Sync now*, and, when unavailable, why, in the person's words.

*As built:* a stale revision is 409 `revision_mismatch` — the module's existing refusal
for `If-Match` — not 412. `mailbox_sync_unavailable` is a 409 whose `detail` carries the
availability code (`off` for a *Sync now* while the sync is off). The card is on
**Calendar → My calendar**, the screen's name. A `PUT` that keeps the sync on while it
cannot run is refused too; turning it off is always taken.

### O6 — What is pushed, and how it looks

The window is **30 days back to 180 days ahead** of today in the business zone. In it:

- **Appointments** the person owns or attends with any answer but declined, not
  cancelled — each **occurrence** its own event, keyed
  `appointment:<id>:<occurrence date>` (a single one `appointment:<id>`), with the
  moved occurrence's own times. A tentative answer is `showAs: tentative`; otherwise
  the appointment's transparency (`busy`/`free`); `private` visibility is
  `sensitivity: private`.
- **The chosen provider kinds**, read through `CalendarSources` under `AsUser` (the
  feed's way, rule 12's exception widened to "a token *or a person's own standing
  setting* names the user"), keyed `<kind>:<id>`. `staff.absence` is `oof`;
  `time.entry` and `expenses.claim` `busy`; every other all-day item `free`; a point
  (`endsAt` equal to `startsAt`) lasts 30 minutes.
- Never a colleague's calendar, a subscription's events (they came from elsewhere, maybe
  Outlook) or a public holiday.

Subject: the title (with *Busy* / *Opptatt* under `detail = busy`, and for a private
appointment's attendee who is not its owner — the feed's rule). Body (text): the
subtitle, then a line "Kept by Vantigo — change it there:" / "Holdes av Vantigo — endre
det der:" and the record's link on the installation's base URL; under `busy`, the link
alone. Location only under `full`. No reminder, no category, no attendees. The language
is the person's preferred language.

At most **1500** events per person; past it the farthest projected items are left out
(the feed's order), never an appointment.

*As built:* the body of an appointment's copy is its description (an item's is its
subtitle), and the link is the app's — `APP_URL` and the base path — as the feed's and
the notices' are, not the installation's public host, which serves only its allowlist.
A done item's subject starts `✓ `. The private flag is set on every copy of a private
appointment, its owner's included.

### O7 — The worker: `calendar-mailbox-sync`

One cycle every minute under an advisory lease (the subscriptions worker's shape), at
most 50 people a cycle, the due first (`next_sync_at` null or past). For each, holding
no transaction or connection while calling out:

1. `Access.UserHolds(user, calendar:access)`; false → O8's clean-up.
2. `MailboxCalendar.Status`; `unavailable` → state `unavailable` (kept, retried hourly);
   `cleanup` → O8's clean-up, state `unavailable` after.
3. `Workforce.DirectoryAccount` under `AsUser`; nil, or a provider other than the
   slot's key → `no_account` and O8's clean-up of what was written (with the stored
   mailbox), state `unavailable`. A different `ExternalID` than the stored one → clean
   up the old mailbox first, then start over on the new.
4. `target = own`: `EnsureCalendar` (stores a changed id, forgets the map when it
   changed).
5. When `reconcile` is set (first sync, a create whose outcome is unknown, daily):
   `Find`, and drop or adopt (`Key` known → map it; unknown → delete).
6. The desired set (O6), each with its hash (SHA-256 of the canonical `MailboxEvent`
   and the target); against the map: new keys are creates, changed hashes updates,
   mapped keys missing from the set and starting inside the window deletes; mapped keys
   that now start before the window are forgotten, never deleted (the past stays).
7. `Apply` in slices; the map written after each slice in a short transaction; a `Gone`
   update becomes a create in the next slice.
8. `last_synced_at`, `next_sync_at = now + CALENDAR_MAILBOX_SYNC_POLL` (default 15
   minutes), `attempts = 0`, state `active`. A refusal sets the state and `error`
   (`mailbox_denied`, `mailbox_gone`: retried in six hours); a failure backs off
   (1, 5, 15, 60 minutes, then hourly) and is `failed` after five.

**The nudge**: every appointment write (create, update, delete, an answer, a move or
cancel of an occurrence, a cancellation) sets `next_sync_at = now` on the
`mailbox_sync` rows of its owner and its colleague attendees, in the same transaction,
so an appointment reaches Outlook within a minute. The nightly re-sync of reading 1 is
the daily `reconcile` plus the poll; a change of roles is seen at the next poll.
`CALENDAR_MAILBOX_SYNC_ENABLED` (default `true`) turns the worker off, as the other
calendar workers.

*As built:* the steps run Status first, then `UserHolds`: an unavailable provider calls
nothing. **Reconcile does not delete** a marked event whose key is not wanted: it adopts
a wanted key the map lacks, deletes a second event carrying a key already mapped (the
duplicate an unknown create leaves), and forgets a mapped event `Find` no longer
answers — only when `Find` answered fewer than 5000, so a cut list forgets nothing. An
unwanted marked event is most often a copy that slid before the window, which the past
keeps. The daily reconcile is derived from `last_synced_at`'s business day (a first sync
on a later day reconciles), not scheduled. `Apply` is handed slices of 100. A refusal
of `ErrMailboxUnknown` is recorded as `no_account`, and `ErrMailboxUnavailable` as
`unavailable`, both retried in an hour.

### O8 — The clean-up

When a person turns the toggle off, changes the target, may no longer use Calendar, or
loses their certain account — and when the admin switch is turned off — their copies are
removed: `target = own` deletes the *Vantigo* calendar; `default` deletes each mapped
event (`Apply` deletes, allowed under `cleanup`). Then the map is emptied and the state
is `off` (toggle off) or `unavailable`. A clean-up that cannot reach the mailbox
(`mailbox_gone`, the connection unusable) forgets the map and logs it: the mailbox is
gone or the copies cannot be reached, and a leaver's mailbox is soft-deleted with them.
Turning the switch back on resumes everyone whose toggle is on, from a reconcile.

*As built:* the clean-up's target is decided by the row's `calendar_id`, not its
`target`: with a *Vantigo* calendar made it is deleted whole, otherwise each mapped event
of the default calendar. A failure to retry keeps the clean-up owed (`stopping`).

### O9 — B: Outlook's busy time in Vantigo (outline)

A delta per person over the same window (`/users/{id}/calendarView/delta`), on the same
worker; events carrying our marker are skipped; the rest stored as `calendar.mailbox_busy
(user_id, event_id, starts_at, ends_at | dates, show_as)` — never subject or body. A new
kind `calendar.outlook` beside the person's own items and in `a colleague's calendar` as
busy; Planning reads it as availability through a new `CalendarSource`-like busy read.
`Calendars.ReadWrite` already covers it.

### O10 — C: the write-back (outline)

The same delta sees changes to events carrying our marker. For an
`appointment:<id>[:<date>]` key whose owner is the mailbox's person: a changed subject,
body above the "Kept by Vantigo" line, location, times, all-day, `showAs`
(busy/free) or sensitivity is a `PUT` of the appointment — or of the occurrence — through
the appointment's own update path, with its validation, its `SEQUENCE` and its mails; a
deleted event is a delete in Vantigo, a cancellation when outsiders were told (reading 1).
An attendee's copy, or a projected item's, is put back at the next push. Conflict: when
both sides changed between two reads, Vantigo wins and the person's state names the
overwritten change. An event created in the *Vantigo* calendar (`target = own`) becomes
an appointment the person owns, then is tagged. The worker remembers each event's
`changeKey` so reading back its own write is not a change.

### O11 — D: change notifications (outline)

Optional, recommended: with `PUBLIC_BASE_URL` reachable, a subscription per person on
`/users/{id}/events` (renewed before its seven days, lifecycle notifications handled),
whose notification only sets `next_sync_at = now` — the delta remains the truth. The
anonymous endpoint joins the public host's list.

### O12 — Out of scope

Per-person delegated OAuth; Google; attendees round-tripping; a whole series edited from
Outlook (occurrences only, O6); categories; reminders; a holiday calendar in Outlook.

## Deliveries

- **A** (`feat/calendar-outlook-sync`): O2–O8. Migrations `00081_calendar_mailbox_sync.sql`
  and `00082_entra_calendar_sync.sql`. Docs: the calendar and Entra reference, the
  calendar and Entra user guides and the Entra and calendar administration pages in both
  languages, Staff's reference (the read), module boundaries (the slot, the sixth
  direction, rule 12's widened exception), the roadmap.
- **B**: O9. **C**: O10. **D**: O11.

## Testing

A: the contract's fake (`modtest`), the calendar's worker against it — the desired set,
the diff, the nudge, the window's edge, the clean-ups, the refusals, the backoff, the
1500 cap; the endpoints (availability reasons, If-Match, the 409); Staff's read
(the principal's own only; the states); Entra's provider against `graphtest` taught
calendars, events, the marker filter and `$batch` — the batch split, `Gone`, 403, the
`MailboxNotEnabledForRESTAPI`, the all-day zone, the unknown account; the settings PUT
and `calendarSync` statuses; the frontend cards in both languages.
