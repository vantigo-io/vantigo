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

*As built:* the token's `Calendars.ReadWrite` does not gate the sync — a grant made with
Exchange RBAC for Applications never shows in the token — so `calendarSync.status`
`permission_missing` (on and connected, the token without it) is advisory, and the
provider is available whenever the switch is on and the connection connected; a mailbox
outside the grant answers 403 per person (`mailbox_denied`). The calendar's
availability `permission_missing` stays in the enum, reserved, and is never answered.

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
`mismatch` or `account_not_managed` never) and answers the oldest provider link. The
calendar takes only an `Enabled` account: a disabled one, a leaver's, is `no_account`.

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

*As built:* `Status` is `available` while the switch is on and the connection connected,
whatever the token carries; `cleanup` (reason `switch_off`) while the switch is off and a
credential still works — the tenant and the client saved, a secret unrevoked and
unexpired, the connection not `failing` `credential_invalid`; `unavailable` otherwise.
**The markers name the installation**: events carry `Name VantigoKey` with
`vantigo:<client id>:<key>`, and the calendars the module makes `Name VantigoCalendar`
with `vantigo:<client id>`. `EnsureCalendar` adopts only a calendar carrying the marker,
trying `Vantigo (2)` to `Vantigo (5)` past ones that do not; `DeleteCalendar` refuses an
unmarked calendar with `ErrCalendarNotOurs`; `Find(ctx, mailbox, calendarID, from)`
answers only this installation's marked events, in any calendar, filtered by
`start/dateTime ge from` when `from` is set. Ids that are a dot segment, bare or
percent-encoded, are refused. Graph's bare `ResourceNotFound` is not read as the mailbox
gone (only `MailboxNotEnabledForRESTAPI`, `ErrorInvalidUser`, `ErrorNonExistentMailbox`
and `Request_ResourceNotFound`). A `$batch` holding a create is sent with no retry of a
5xx; 429s inside a batch are sent again once after the longest `Retry-After`, only when
it is at most 30 seconds. The `$batch` answer is read through 8 MiB. The calendar hands
`Apply` slices of 100 ops, which Entra splits into batches of 20.

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
availability code; `switch_off` is told whatever the provider's state. *Sync now* while
the sync is turned off is 409 `mailbox_sync_off`. The `PUT` is rate limited too,
`calendar-mailbox-sync-write`, 10 per client address in 10 minutes. The card is on
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
appointment, its owner's included. The 1500 cap is hard: past the projected items, the
farthest appointment occurrences are trimmed too.

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
nothing. Each person has three minutes for what the sync asks of its neighbours.
**Reconcile does not delete** a marked event whose key is not wanted: `Find` from the
window's first instant, it adopts a wanted key the map lacks, deletes a second event
carrying a key already mapped (the duplicate an unknown create leaves), and forgets a
mapped event starting in the window that `Find` no longer answers — only when `Find`
answered fewer than 5000. The daily reconcile is derived from `last_synced_at`'s
business day, not scheduled. `Apply` is handed slices of 100, and the first slice with a
failed op stops the sync. `ErrMailboxUnknown` is recorded as `no_account`,
`ErrMailboxUnavailable` as `unavailable`, both retried in an hour. Errors are logged by
their codes alone.

### O8 — The clean-up

When a person turns the toggle off, changes the target, may no longer use Calendar, or
loses their certain account — and when the admin switch is turned off — their copies are
removed: `target = own` deletes the *Vantigo* calendar; `default` deletes each mapped
event (`Apply` deletes, allowed under `cleanup`). Then the map is emptied and the state
is `off` (toggle off) or `unavailable`. A clean-up that cannot reach the mailbox
(`mailbox_gone`, the connection unusable) forgets the map and logs it: the mailbox is
gone or the copies cannot be reached, and a leaver's mailbox is soft-deleted with them.
Turning the switch back on resumes everyone whose toggle is on, from a reconcile.

*As built:* the clean-up's target is decided by the row's `calendar_id`: with one, the
*Vantigo* calendar is deleted when it carries the marker, else the marked copies in it
one by one; without one, every marked event of the default calendar is deleted,
whatever its date (`Find` with no bound), and every mapped one besides. Only
`ErrMailboxGone` and `ErrMailboxUnknown` forget the copies unremoved; a mailbox denied, a
provider unusable or a failure keeps the clean-up owed on the backoff, with the
refusal's code as the row's error.

### O9 — B: Outlook's busy time in Vantigo

Revised for delivery B on 2026-10-27, after a Microsoft Learn check of the delta
(research §3, *Delta, as checked for B*). The busy time is read back on the same
worker, stored as times and nothing else, and shown in three places: the person's own
calendar, a colleague's, and Planning's board.

**O9.1 — The read.** Each sync that worked (O7 step 8 reached with no refusal or
failure) goes on to read the person's **default calendar** with
`GET /users/{id}/calendarView/delta` — the only calendar whose delta v1.0 documents, and
the one Exchange computes free/busy from. The push comes first, so the map already holds
the copies just written. The delta's window is fixed in its token, so the read keeps
a window of its own, wider than the push's: from 30 days before today to **210** days
after, set when a delta starts and stored with its link; a delta is started afresh when
there is no link, when the stored window ends before today + 180 days (about once a
month), and when Graph refuses the link (O9.4). The read takes the pages Graph answers
(`Prefer: odata.maxpagesize=50`, UTC) until the `deltaLink`, at most 40 pages a call; a
call cut short answers the `nextLink`, which is stored, and the worker calls again while
the person's deadline lasts — the next sync takes it up otherwise. The first call of a
fresh delta empties the person's busy rows in the transaction that writes its first
page.

**O9.2 — Our own copies are skipped, by the map and one read.** A delta answers no
extended property (`$expand` and `$select` are refused), so the marker cannot be seen
in it. An event is ours when (a) its id is an `event_id` of the person's
`calendar.mailbox_events` — every copy written under `target = default`, since a single
event's instance in a calendar view carries the event's id — or (b) for an event that
is not, would be stored, and is a single instance (an occurrence or exception belongs
to a series, and the push never writes one), the provider's new **`Marked`** read
answers it: a `$batch` of `GET /users/{id}/events/{eid}?$select=id&$expand=
singleValueExtendedProperties($filter=id eq '<marker>')`, twenty to a request, answering
the ids carrying this installation's marker with their keys. (b) is what catches a copy
whose create's outcome was unknown, one forgotten as it slid before the push's window,
and anything left over from a target change. `transactionId` was rejected: A's copies
were written without one, and since Graph de-duplicates creates on it, a copy re-made
after a `Gone` would collide with it. Another installation's copies are not ours: they
are the person's busy time like any other event.

**O9.3 — What is stored.** `calendar.mailbox_busy (user_id, event_id, all_day,
starts_at, ends_at | start_date, end_date (inclusive), show_as, updated_at)`, primary key
`(user_id, event_id)` — never a subject, body, location, organizer or attendee; those
arrive in the delta and are not decoded. An event is stored unless it is ours (O9.2),
`free`, or cancelled; a change to any of those deletes the row, as does `@removed`
(whatever its reason: an event moved out of the window is answered so). `show_as` is
`busy`, `tentative`, `oof` or `workingElsewhere` (an unknown value is `busy`). An all-day
event's dates are its start's and end's calendar dates (the end exclusive in Graph,
stored inclusive), rounded to the nearest midnight should Graph answer them off it. At
most **5000** rows a person; past it a new event is not stored and the fact logged. On
`mailbox_sync`: `busy_link` (the next or delta link), `busy_from`, `busy_to` (the
window it was started over) and `busy_synced_at`.

**O9.4 — The contract.** `contracts.MailboxCalendar` gains two calls, same lock
discipline (rule 14), both refused under `cleanup` with `ErrMailboxUnavailable` (a read
is never needed to remove copies):

```go
Delta(ctx, mailbox string, from, to time.Time, link string) (MailboxDelta, error)
Marked(ctx, mailbox string, eventIDs []string) ([]MailboxEventRef, error) // ≤ 100 ids
type MailboxDelta struct { Changes []MailboxChange; Link string; Done bool }
type MailboxChange struct {
    EventID string; Removed, Series, Cancelled, AllDay bool
    StartDate, EndDate, StartsAt, EndsAt time.Time; ShowAs string
}
var ErrMailboxDeltaExpired // the link is refused: start the delta again
```

`from`/`to` are used only when `link` is `""`. A `link` the provider did not answer for
this mailbox — not Graph's `…/users/{oid}/calendarView/delta?` — is refused, so a stored
link can point it nowhere else. `ErrMailboxDeltaExpired` is Graph's `410 Gone`, or a
4xx whose code is `syncStateNotFound`, `syncStateInvalid`, `resyncRequired` or
`ErrorInvalidSyncStateData`; the worker forgets the link and the rows and starts again,
once a sync. The rest of the refusals are A's.

**O9.5 — The person's own calendar.** A new kind **`calendar.outlook`** (module
`calendar`) on `GET /items`, listed in `GET /meta`'s `sources` while the caller's sync
is on: each stored row in the window as an item with no title and
`outlook: {showAs}`, which the screen labels *Busy in Outlook* / *Opptatt i Outlook*
(*Tentative*, *Out of office*, *Working elsewhere* by `showAs`). Its id is the first 16
hex of the SHA-256 of the event id — Graph's ids never reach the browser.

**O9.6 — The person's own titles: deferred.** Showing a person their own Outlook
titles means a Graph read on the request path — a network call under a person's
request, per window shown, through the provider's rate limit — or storing them, which
B does not. It is left to delivery C, which reads events whole for the write-back
anyway; the item's shape (no title, `outlook.showAs`) leaves room for it.

**O9.7 — A colleague's calendar.** `GET /colleagues/{userId}/items`, behind
`calendar:view-colleagues` as today, answers the colleague's rows of `show_as` `busy` or
`oof` — tentative and working-elsewhere are not busy, as a tentative Vantigo answer is
not shown there today — as `calendar.outlook` items with no title, while the
colleague's sync is on.

**O9.8 — Planning: `contracts.CalendarBusy`, provider-gated.** A new single-provider
slot `CalendarBusy` (`Module.CalendarBusy`, `Deps.CalendarBusy`), filled by the calendar
— the first contract it provides — and read by Planning:

```go
type CalendarBusy interface {
    Busy(ctx context.Context, userIDs []uuid.UUID, from, to time.Time) (map[uuid.UUID]BusyTime, error)
}
type BusyTime struct { Spans []TimeSpan; Days []DateSpan } // merged
```

A user's busy time over `from..to` (civil dates, inclusive, at most 400 days, at most
500 users): the appointments a colleague's calendar shows as busy (owned or accepted,
`busy`, not cancelled, each occurrence) and the Outlook rows of `busy` or `oof` while the
user's sync is on — timed ones as merged instants, all-day ones as merged date spans.
**Provider-gated** (rule 12): the calendar answers the principal in `ctx` under a
request — their own id for `calendar:access`, anyone else's for
`calendar:view-colleagues` — and nothing, not an error, for an id the caller may not
see or a context with no request; the rule is the calendar's own colleague rule, which
Planning cannot know. Planning's board adds, per person and week, **`busyHours`** (the
merged timed spans inside the week, in the business zone) and **`busyDays`** (the
all-day busy days in it), asked for the people with a `UserID` before anything is shaped
and never inside a transaction. They are shown, not subtracted: a meeting about the
project is often the booking's own time, so available, free and load stay Staff's and
Planning's figures. Absent when the calendar answers nothing (the module off, the
caller without the permission).

**O9.9 — Turning off and the clean-up.** Every clean-up (O8) and every result that
leaves the sync `off` or `unavailable` forgets the person's busy rows and their delta
link and window; a failure on the backoff keeps them. The reads join `mailbox_sync`
and show rows only while `enabled`.

*As built:* see the plan
[`2026-10-27-calendar-outlook-busy.md`](../plans/2026-10-27-calendar-outlook-busy.md).
Entra follows a link only when it holds no `#`, `\` or space and parses as `https` on
Graph's host exactly — no user, no fragment, no percent-encoded path, a query — with the
path `/v1.0/users/{oid}/calendarView/delta` of the mailbox's own object id, compared
whatever its case; it sends a path of its own built from the oid with the link's query
alone, and checks every `nextLink` and `deltaLink` Graph answers the same way; anything
else is a failure, unsent. An event whose id breaks the provider's id rule is skipped
before decoding. `event_id` is `varchar(512)`, the provider's bound, and 00083 widens
00081's `mailbox_events.event_id` and `mailbox_sync.calendar_id` to it as well. `CalendarBusy`
also leaves out a user the directory does not answer as active, and answers nothing
under the `AsUser` mark, even over a request. The busy link is written only while the
sync is `enabled`, first in each page's transaction: a sync turned off mid-read deletes
the person's rows there and stops. A busy read that fails without a refusal leaves the
push recorded — `active`, `last_synced_at` now, error `failed`, the backoff, never the
`failed` state — and Planning's board gives each person sharing a user the figures. A page of the delta is read through a cap of its
own, **16 MiB** (fifty events answered whole, bodies and attendees included), beside the
1 MiB default and the `$batch`'s 8 MiB. An event whose times do not read, or with no id,
is skipped and counted at debug level, never failing the page — which would hold the
delta at it for ever. The 5000 cap applies to **new** events only: a row already stored
is still rewritten or deleted past it. A read whose deadline passes between pages keeps
the next link it reached and is **not** counted as a failure: the sync is `active` and
the next one takes the link up (a call the deadline cuts short fails as any call does). A colleague's calendar gives the `calendar.outlook` blocks
a **legend chip of their own** when any are in view; the caller's own id on that route
still answers `GET /items`' appointments alone, with no Outlook rows. Planning's board
answers **503 `dependency_unavailable`** when the `CalendarBusy` read fails, rather than
a board without the figures; with `groupBy=project` the people's rows carry their own
busy figures and the **project's sum row carries none**. The person's timeline in
Planning is unchanged: the busy figures are on the board alone.

### O10 — C: the write-back

Revised for delivery C on 2026-10-28, after a Microsoft Learn check (research §3, *The
write-back, as checked for C*). An edit, a move or a delete a person makes in Outlook to
a copy of **their own appointment** reaches the appointment through its own update path;
an event they make in the separate *Vantigo* calendar becomes an appointment; anything
else they change in a copy is put back.

**O10.1 — Not the delta: the copies' versions, every sync.** The write-back does not
ride B's delta. A delta answers no extended property, its `changeKey` is not shown in
v1.0's samples, the *Vantigo* calendar has no v1.0 delta (`/calendars/{id}/…/delta` is
beta), `@removed` cannot tell a delete from a move out of range, and B's read follows the
push while the write-back must precede it (O10.5). Instead each sync asks the provider
for the **versions** of what it may have to take back — ids and change keys, nothing of
what the events say — and reads whole only the events whose version moved:

- `target = default`: the events of the default calendar carrying this installation's
  marker that start at or after the window's first instant (A's `Find` filter, which is
  documented on `/users/{id}/events`), `$select=id,changeKey`, with the marker's key.
- `target = own`: every event of the *Vantigo* calendar's `calendarView` over the push's
  window (documented in v1.0), `$select=id,changeKey,type` — the calendar is Vantigo's
  whole, so an event without the marker in it is one the person made there.

A filter on `lastModifiedDateTime` was rejected: no page documents it for events, and the
known issues warn a query parameter may fail silently. B's delta is unchanged: it still
reads the default calendar for the busy rows and still skips the copies.

**O10.2 — The contract.** `contracts.MailboxCalendar` gains two reads, rule 14's lock
discipline, both refused under `cleanup` (`ErrMailboxUnavailable`):

```go
Versions(ctx, mailbox, calendarID string, from, to time.Time) (MailboxVersions, error)
Events(ctx, mailbox, calendarID string, eventIDs []string) ([]MailboxEventState, error) // ≤ MailboxMarkedMax
type MailboxVersions struct { Events []MailboxEventVersion; Complete bool }
type MailboxEventVersion struct { EventID, Key, ChangeKey string; Series bool }
type MailboxEventState struct {
    EventID, Key, ChangeKey string
    Foreign, Series, Cancelled, Meeting, TextBody bool
    Event MailboxEvent
}
```

`Versions` with `calendarID ""` answers O10.1's marked events (`Key` set), with a
secondary calendar every event of the view (`Key` unset: the view is not asked for the
marker); at most 5000, `Complete` false when cut. `Events` reads each id through the
**calendar's own path** (`/users/{oid}/calendar/events/{id}`, or
`/calendars/{cid}/events/{id}`), in `$batch`es of twenty, with `Prefer:
outlook.timezone="UTC", outlook.body-content-type="text"` and the marker expanded: an id
not answered is not in that calendar any more (deleted, or moved to another calendar or
folder); `Key` is this installation's key, `Foreign` another installation's marker,
`Series` an occurrence, exception or series master, `Meeting` an event with attendees
(their count is decoded, never an address), `TextBody` whether the body came as text,
and `Event` the subject, the body, the location's display name, the times (all-day dates
rounded to midnight as B's), `showAs` and `sensitivity` (`private` and `confidential`
are `Private`). Nothing of it is logged or stored but what the appointment's update
writes. `Apply`'s update writes the op's `Key` as the marker when it is set — the same
value a copy already carries — so an event taken in is tagged by the push that rewrites
it. `MailboxResult.ChangeKey` (A's) is now kept.

**O10.3 — What is stored.** `calendar.mailbox_events` gains `change_key` (the version the
last write, or the last look, left) and `released_at` (O10.8). `calendar.mailbox_sync`
gains `put_back_reason`, `put_back_key` and `put_back_at`: the last change in Outlook that
was not taken, which the setting's `GET` answers as `putBack {reason, at, appointmentId?,
occurrenceDate?, title?}` — `title` read live from the appointment, only for one the
person owns or attends. Reasons: `conflict` (Vantigo changed it too, O10.6), `refused`
(the appointment's rules refused the change), `not_owner` (an attendee's copy),
`not_editable` (a projected item's copy, or what an occurrence cannot carry). Cleared by a
clean-up and when the sync is turned off.

**O10.4 — Telling an edit from our own write.** A mapped event whose `ChangeKey` equals
the row's `change_key` is as Vantigo left it. A row with no `change_key` (written before
C, or adopted by a reconcile) takes the version answered as its baseline, unread. A
version that moved is read whole (`Events`) and compared, field by field after
normalising (line endings, trailing space, the body cut at the first "Kept by Vantigo" /
"Holdes av Vantigo" line, times to the second), with the copy the desired set holds for
its key: equal is noise — Exchange's own touch — and only the new `change_key` is kept.

**O10.5 — Order in the sync.** After O7 step 6's desired set is built and the map loaded,
before the reconcile and the push: (1) `Versions`; (2) `Events` for the moved, the new and
the missing, at most 100 ids a sync; (3) the decisions below, at most **20** writes to
appointments a sync, each through the module's own path on the worker's context with no
transaction open across a call; (4) the map's change keys, forgets and stale hashes in one
short transaction; (5) when an appointment was written, the desired set built again. Then
the reconcile, the push and the busy read as before. Whatever is left over — a version
not yet read, a write past the 20 — keeps its old `change_key`, so the next sync sees it
again. A refusal from `Versions` or `Events` stops the sync as A's do
(`refusedOrFailed`); a failure skips the write-back, lets the push and the busy read go
on, and records the sync `active` with error `failed` on the backoff, as a failed busy
read does (O9's as-built) — and the push of that sync may then overwrite an Outlook edit
unread, Vantigo winning unnamed.

**O10.6 — The decisions.** For a mapped key whose version moved, read whole:

| The copy | Vantigo's copy changed since the last write | What happens |
| --- | --- | --- |
| `appointment:<id>`, owned by the mailbox's person, single | no | the changed fields become a `PUT` of the appointment (O10.7) |
| `appointment:<id>:<date>`, owned, an occurrence | no | a change of the times moves the occurrence (`PUT …/occurrences/{date}`); any other change is put back, `not_editable` |
| an owned appointment | yes (the row's hash is not the desired one) | **Vantigo wins**: put back, `conflict` |
| an appointment the person attends | — | put back, `not_owner` |
| a projected item (`<kind>:<id>`) | — | put back, `not_editable` |
| a key the desired set no longer holds | — | nothing: the push deletes or forgets it |

"Put back" clears the row's hash, so the push rewrites the copy whole. A write that
worked clears it too, so the push writes the appointment's own form back (the "Kept by
Vantigo" line, the link) and keeps the `ChangeKey` it answers. A refusal by the
appointment's rules is put back, `refused`. A cancelled appointment, a `Series` or
`Foreign` answer, and an event that turned into a meeting (O10.8) are never written back.

**O10.7 — The fields.** Taken from an owned single appointment's copy, when they differ
from what was written: the subject (the title, trimmed; empty is refused), the body
above the kept line (the description; empty clears it; a body that did not come as text
is not taken), the location, the times and all-day (the appointment's zone kept), `showAs`
(`free` is transparency free, any other value busy) and the sensitivity (private or
confidential is visibility private, anything else shared). Under `detail = busy`, and
for a private appointment's hidden copy, the subject, body and location written were
placeholders, so only the times, `showAs` and the sensitivity are taken. Everything else
of the appointment — its attendees, its rule, its customer and project — is sent back
as it stands, through `readAppointment` (`current` its attendees, so one the directory
no longer answers is kept), with the row's revision as `If-Match`: a revision moved on
in between is a `conflict`. The update's `SEQUENCE`, its REQUESTs to the outside
attendees and its notices to colleagues go as for a `PUT` from the screen. An
occurrence's move is held to the series' kind (a timed series' occurrence stays timed).

**O10.8 — Deletes, and what is not a delete.** A mapped key absent from a `Complete`
`Versions` answer, and starting in the window, is read with `Events`. Not answered — the
event is no longer in **that calendar** — is a delete in Outlook: an owned single
appointment is deleted through the module's delete path, which cancels it, with the
CANCELs, when outsiders were invited (reading 1); an owned occurrence is cancelled
(`PUT …/occurrences/{date}` with `cancelled`); any other key is forgotten and made again
by the push. Answered — it is still there, moved before the window — it is a change
(O10.6). The guards: the map is emptied before anything else by every clean-up, target
change and mailbox move, and the push's own deletes forget their rows, so an absence is
never the module's own doing; an incomplete list infers nothing; and on a sync whose
write-back did not complete, the reconcile does not forget mapped events Find no longer
sees (they could be deletes still unread). An event the person **turned into a meeting**
(`Meeting`: they added attendees) is **released**: `released_at` set, never written,
deleted or taken back again — a write would mail its attendees — and forgotten without
a delete by the push and the clean-up.

**O10.9 — New events in the *Vantigo* calendar.** Under `target = own`, an event of the
calendar that is neither mapped nor marked, single, not cancelled and not a meeting is
read whole and becomes an appointment the person owns — the subject its title
("Untitled" / "Uten tittel" when empty), the body its description (cut to the limit), the
location, the times in the business zone, `showAs` and sensitivity as O10.7 — through
the module's create path, its map row (`appointment:<id>`, the event id, no hash, the
change key) written in the same transaction, so an event taken in is never taken twice
even if the push that tags it fails. A refusal is logged by its code, the event left as
it is and its id remembered for the sync (it is tried again at the next).
A series, a meeting, or another installation's event there is left alone.

**O10.10 — The person's own Outlook titles (O9.6): not shown.** C reads only the copies
and the *Vantigo* calendar's events whole, never the person's own events; showing their
titles would mean storing the subjects of a private calendar or reading Graph on the
request path, and a colleague's view must never see them. The `calendar.outlook` item
stays titleless; the item's shape leaves room for it later.

**O10.11 — Rule 14.** The write-back is the calendar writing its own data from what the
provider read — no new cross-module direction. `Versions` and `Events` keep rule 14's
discipline; what an event says crosses the contract for the copies and the *Vantigo*
calendar only, and only for the write it feeds.

*As built:* see the plan
[`2026-10-28-calendar-outlook-writeback.md`](../plans/2026-10-28-calendar-outlook-writeback.md).
`Versions` reads each page through the 8 MiB cap with `$top=500` and follows a next
link only when it is a page of the very list sent — B's rule generalised: host exact,
no user or fragment, a query, the path `/v1.0` plus the path sent compared unescaped and
whatever its case, percent-encoding only when the path sent carries it — sending its
own path with the link's query alone; on the default calendar an event carrying no key
of this installation's is left out. `Events` reads its `$batch`es through the 16 MiB
cap (a body asked as text may still come as HTML), answers a 404 for the item as not
there, retries a 429 once after at most 30 seconds, and leaves out a 200 for another id
or with times that do not read. The 100 reads go to the moved first, then the missing,
then the new; a missing row past them makes the write-back incomplete. The decisions
run in this order: a meeting is released; a key no longer wanted is left to the push; a
`Series`, `Foreign` or cancelled answer keeps its change key; an unchanged copy is
noise; a projected item is `not_editable`; an appointment gone is left alone;
another's is `not_owner`; a cancelled one is left alone; then the conflict. A write answered 400 is `refused`,
409 `revision_mismatch` `conflict`, any other refusal is made stale for the push. Under
`detail = busy` only the times, `showAs` and the sensitivity are taken (the private
appointment's hidden copy is only ever an attendee's, so `not_owner`); a copy whose
fields all match the appointment is rewritten with nothing written. A write that
worked moves the row's `starts_on` with the appointment. A deleted copy of an
occurrence of an appointment that is no longer a series, or of an appointment gone or
cancelled, is forgotten. A released row is forgotten by the default calendar's
clean-up without a delete; a *Vantigo* calendar is deleted whole only while it holds no
meeting and no released copy (see below). `putBack` is answered by the setting's `GET`,
`PUT` and run alike.
Every update the worker makes names its key, so writes the marker. When the busy read
and the write-back both fail, the busy read's failure is the one logged.

*After the review* (these supersede the text above where they differ). **O10.2:**
`Events` answers `MailboxEventsRead{Events, Gone}` — `Gone` the ids the directory
answered 404 for in that calendar; an id in neither list (an answer that did not
decode, a per-item 403, an id skipped) was not read this time and infers nothing. A
`$batch` whose answer outgrows the 16 MiB cap or does not decode is sent again an id at
a time, an id still failing alone skipped; a 403 of the `$batch` itself is
`ErrMailboxDenied`. `Events` selects `originalStartTimeZone` and reads an all-day
event's dates in that zone (a Windows name through CLDR's table, or an IANA one), the
nearest midnight only when it maps to none; a zone that is neither UTC nor IANA makes
the event unreadable — skipped in `Events`, left out of B's delta too — never read as
UTC. `Find` answers `MailboxEventRef.Meeting` (`$select=id,attendees`, the count alone),
and a gone secondary calendar answers no events. **O10.8:** a delete is taken only for an
id in `Gone`; a missing row neither answered nor `Gone` keeps its change key and makes
the write-back incomplete. Each delete's fate, first match: a key no longer wanted is
left to the push; not an appointment key, an appointment gone, not the person's own,
cancelled, or an occurrence of one no longer a series is forgotten; a row hash `""` is
put back with no record; a row hash not the wanted one (Vantigo changed it since) is a
`conflict`, nothing deleted; else the delete or the cancellation. **The breaker:** more
than ten deletes or cancellations of the person's own appointments in one sync take none
— every one put back (made stale), the put-back recorded `many_deleted` with no key
(`MailboxSyncPutBack.reason` gains it, `appointmentId` absent), the counts logged at
warning. **The meeting rule:** a meeting `Find` answers is never adopted nor deleted
by the reconcile or the clean-up, and a mapped one is held as released for the sync
until the write-back releases its row; under `target = own` the clean-up deletes the
*Vantigo* calendar whole only while it holds no meeting and no released row, and
otherwise deletes the non-meeting copies one by one and leaves the calendar. **O10.6:**
after the noise check, a copy whose row hash is `""` (its last write unknown) is never
taken but put back with no record; an event exactly as the push last wrote it (its hash
in the row's target equals the row's) beside a Vantigo change is no conflict — its
change key kept, the push rewriting it. The kept line is cut at the first line that
*starts with* either language's text, a no-break space read as a space. **O10.9:** new
events are taken only while the person's kinds include `calendar.appointment`. **O10.3:**
`putBack` is answered by the `GET`, the `PUT` (absent once it turns the sync off) and
`POST /mailbox-sync/run`. The `POST` and `PUT` of an appointment read its links again
before the write, so a failing directory writes nothing.

### O11 — D: change notifications

Revised for delivery D on 2026-10-28, after a Microsoft Learn check (research §3, *Change
notifications, as checked for D*). Optional and recommended: on an installation Microsoft
can reach, a change in a person's mailbox makes their sync due at once instead of at the
next poll. **A notification is a nudge and nothing more**: it sets nothing but a time,
and the reads — B's delta, C's `Versions` and `Events` — stay the truth. Nothing a
notification says is trusted beyond "this subscription fired", nor stored.

**O11.1 — When.** Only while `PUBLIC_BASE_URL` is set (Graph needs a public HTTPS address)
and `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` (default `true`) is on — the operator's off
switch for a public host Microsoft cannot reach. Otherwise nothing is subscribed and the
poll alone runs, as before. The poll is unchanged either way (`CALENDAR_MAILBOX_SYNC_POLL`):
a secondary calendar's coverage and an event notification's latency are not documented.

**O11.2 — One subscription per mailbox.** `resource` `/users/{oid}/events` — the only
event resource documented; a calendar-scoped path is not — `changeType`
`created,updated,deleted`, no resource data (no `includeResourceData`, no certificate:
nothing of an event is wanted), `expirationDateTime` now + **10 000 minutes** (under the
10 080 limit), `clientState` a fresh 32 random bytes, base64url (43 characters), and
`notificationUrl` and `lifecycleNotificationUrl` both
`PUBLIC_BASE_URL/api/v1/entra/mailbox-notifications`. Whether the subscription sees the
*Vantigo* calendar is for the real tenant; the poll covers it either way.

**O11.3 — The contract.** `contracts.MailboxCalendar` gains three calls, rule 14's
discipline (never in a transaction or holding a connection, through `contractscalls.go`):

```go
// Watch makes sure the mailbox's changes are notified: nothing when a live
// subscription has more than three days left, a renewal when less (or when the
// directory asked to reauthorize), a create otherwise. nil also when the provider
// offers no notifications on this installation, or holds creates off (O11.7).
// Refused under cleanup.
Watch(ctx context.Context, mailbox string) error
// Unwatch deletes the mailbox's subscription. Idempotent; allowed under cleanup;
// a refusal or failure keeps it owed (answered as an error), never forgotten.
Unwatch(ctx context.Context, mailbox string) error
// Notified answers the mailboxes notified after since, oldest first, at most
// MailboxNoticesMax: no network, the provider's own rows.
Notified(ctx context.Context, since time.Time) ([]MailboxNotice, error)
type MailboxNotice struct { Mailbox string; At time.Time }
```

`Watch`: no row, or one removed → **create** (O11.4); a row whose expiry is within three
days, or marked `reauthorize` → **`PATCH` the expiry** to now + 10 000 minutes (which
reauthorizes too); a 404 there forgets the row and creates. A create answered **409**
(Graph already holds one for that resource and this application — a row lost) lists the
application's subscriptions (`GET /subscriptions`, at most 10 pages), deletes those whose
`resource` names this mailbox's object id and whose `notificationUrl` is exactly this
installation's, and creates once more. A 403 is `ErrMailboxDenied`, a mailbox gone
`ErrMailboxGone`, as for the other calls. `Unwatch`: a row past its expiry is forgotten
with no call (Graph deleted it); else `DELETE /subscriptions/{id}` — 204 or 404 forget
the row; anything else marks it `deleting` and answers the error. Every subscription id
read from Graph or stored is a GUID, parsed strictly; any other is a failure.

**O11.4 — Storage (Entra).** `entra.mailbox_subscriptions`, one row per mailbox:
`object_id` (primary key, the account's), `subscription_id` (Graph's, unique, null while
creating), `client_state_hash` (SHA-256 of the `clientState` — a 256-bit random secret,
so a hash is enough; the plain value is sent once, at create, and never stored),
`expires_at`, `state` (`active`, `reauthorize`, `removed`, `deleting`), `notified_at`,
`created_at`, `updated_at`. `entra.connection` gains `notifications_validating_until`
(O11.5), `notifications_unreachable_at` (O11.7) and `last_notification_at`.

**O11.5 — The endpoint.** `POST /api/v1/entra/mailbox-notifications`, anonymous, added to
the public host's allowlist (and served on the app's host too, as the RSVP operations
are). Rate limited `entra-mailbox-notifications`, 600 per client address a minute (Graph
retries a 429). The body is capped at 1 MiB.

- **The handshake**: a request carrying `validationToken` is answered **200 `text/plain`**
  (`nosniff`, `no-store`) with the decoded token — only while a create is in flight
  (`notifications_validating_until`, set to now + 30 s before each `POST /subscriptions`
  and cleared after it) and only for a token of at most 1024 bytes with no control
  character; otherwise 404 with no body. The answer never waits on anything but that one
  row.
- **A notification**: `{"value": [...]}`, at most 1000 items read. Per item: the
  `subscriptionId` a GUID, a row of it, the `clientState`'s SHA-256 equal to the row's
  (compared in **constant time**), and the `tenantId` the connection's — else the item is
  ignored. A valid item sets the row's `notified_at` (one `UPDATE` per subscription per
  request, skipped when it moved within the last five seconds — a flood only marks a row
  due); a `lifecycleEvent` of `reauthorizationRequired` also sets `reauthorize`,
  `subscriptionRemoved` `removed`; `missed` is a notification like any other.
  `last_notification_at` moves at most once a minute.
- **The answer**: **202** with no body for any body that decodes, whatever it named —
  an unknown subscription, a wrong `clientState` and a valid one are answered alike, so
  the endpoint reveals nothing; 400 for one that does not; **503** when the database
  could not be written within two seconds (Graph retries). Everything is done before
  answering, within the three seconds: a few indexed writes.
- **Logs**: counts and codes at debug; ignored items counted at warning, never an id, a
  `clientState`, a resource or a tenant.

**O11.6 — The calendar's side.** (1) Each sync that reached the push and left the person
`active` calls `Watch(mailbox)` last; its error is logged by its code and changes
nothing — the push and the busy read stand. (2) Every clean-up (O8) — the toggle off, the
target changed, `calendar:access` lost, the account gone or moved, the admin switch off —
calls `Unwatch` on the mailbox it cleans; `ErrMailboxGone` and `ErrMailboxUnknown` forget
it, anything else keeps the clean-up owed on the backoff as the other clean-up steps do.
A target change keeps the subscription (it is the mailbox's, not the calendar's): the
clean-up there skips `Unwatch`. (3) Each cycle, before it picks the due, the worker asks
`Notified(since)` — `since` the latest `At` it has taken, held in memory, the poll
interval ago at start — and sets `next_sync_at = now` on each enabled, `active` or
`pending` row of that mailbox whose last sync started before the notice's `At` and which
is not due already. A failing `Notified` is logged and the cycle goes on.

**O11.7 — What an Owner sees.** `GET /connection`'s `calendarSync` gains
`notifications {status, subscriptions, lastNotificationAt?, unreachableAt?}`: `off`
(`CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` false), `not_configured` (`PUBLIC_BASE_URL`
unset), `unreachable` (a create's handshake failed — Graph answered 400 — within the last
hour: every `Watch` then answers nil without calling, so one unreachable address does not
cost a create per person), `active` otherwise; `subscriptions` the live rows. The Entra
page shows it under *Calendar sync*. The person's Outlook card shows nothing new.

**O11.8 — What is not done.** No rich notifications or encryption; no IP allowlist
(optional in Microsoft's words, and their ranges move — an operator may add one at the
proxy); no delta of the *Vantigo* calendar (none in v1.0). A connection deleted forgets
its rows: Graph's subscriptions lapse within seven days and their notifications are
answered 202, unknown.

**O11.9 — Rule 14.** `Watch`, `Unwatch` and `Notified` are calls of the calendar into the
provider, the direction that exists; the endpoint writes Entra's own rows and never
calls the calendar.

*As built:* the build keeps O11 and adds or differs in these places.
`Unwatch` **skips the account check** — only the provider's status (`available` or
`cleanup`) — so a clean-up can still remove a subscription after the account is gone or
unconfirmed; `Watch` keeps it. A **failed handshake is recognised** by a 400 from
`POST /subscriptions` whose error names a validation request (and is not a mailbox gone):
it sets `notifications_unreachable_at`, forgets the half-made row and answers nil, and a
create that works clears the mark. `Watch` also runs **beside a push that stood but whose
busy read or write-back failed** (`pushedButFailed`), not only after a clean success, and
not past the sync's deadline. **`subscriptionRemoved` beats `reauthorizationRequired`**
when one request carries both for a subscription, and a lifecycle state is written even
inside the five-second coalescing. The validation window is **closed conditionally**
(`validating_until` equal to the value that create opened), so a create started since
keeps its own window, and closed on a context that outlives the create's cancellation.
A 409 lists `GET /subscriptions` through at most ten pages, following `@odata.nextLink`
only within Graph's base, and deletes only a subscription on this mailbox's
`/users/{oid}/events` with exactly this installation's `notificationUrl`, then creates
once more. The stored expiry is the one Graph granted, or the one asked when its answer
is unusable; a subscription id that is no GUID is `tenant_unreachable`. The endpoint
reads the connection once for a request and the rows in one query; a tenant id is
compared case-insensitively. The Entra page shows nothing for the line while the
calendar sync switch is off, and `subscriptions` counts live rows whatever the status.
`Notified` is capped at 5000 notices (`MailboxNoticesMax`) a call, and the worker advances
`since` only as far as the notices it marked.

*As built, after review:* the row stores `notification_url`, and a `Watch` that finds a held
subscription made for another address than the present target deletes it (a 404 done, any
other refusal answered, the row kept) and creates again; a renewal never revives a
`removed` row. A new row state `conflict`: a 409 whose whole subscription list held none of
this installation's is another installation's, and holds that mailbox's `Watch` off for 24
hours (a warning with the tenant only); `Unwatch` forgets such a row and it is not counted
live. A notice never overrides the backoff (only rows with `attempts = 0` are made due).
The worker asks `Notified(since − 30 s)` — the overlap covers late commits and replica
skew — except after a full 5000 page, and marks a cycle's notices in one statement, on an
index of `lower(mailbox)` where enabled (migration `00086`). An `Unwatch` refused as denied
no longer keeps the clean-up owed (logged; the subscription lapses within seven days);
unavailable and failures still do. A failed handshake is recognised from the message
alone, never the `ValidationError` code. The webhook pre-filters items with no database
read (strict GUIDs, a 43-character base64url clientState of 32 bytes), counts the ignored
at debug and at warning at most once a minute a server; a lifecycle event never overwrites
`deleting`, and plain marks go in one update.

### O12 — Out of scope

Per-person delegated OAuth; Google; attendees round-tripping; a whole series edited from
Outlook (occurrences only, O6); categories; reminders; a holiday calendar in Outlook.

## Deliveries

- **A** (`feat/calendar-outlook-sync`): O2–O8. Migrations `00081_calendar_mailbox_sync.sql`
  and `00082_entra_calendar_sync.sql`. Docs: the calendar and Entra reference, the
  calendar and Entra user guides and the Entra and calendar administration pages in both
  languages, Staff's reference (the read), module boundaries (the slot, the sixth
  direction, rule 12's widened exception), the roadmap.
- **B** (`feat/calendar-outlook-busy`): O9. Migration `00083_calendar_mailbox_busy.sql`.
  Docs: the calendar, Entra and Planning reference, module boundaries (the `CalendarBusy`
  slot, rule 12, rule 14's reads), the calendar and Planning user guides in both
  languages, the roadmap.
- **C** (`feat/calendar-outlook-writeback`): O10. Migration `00084_calendar_mailbox_writeback.sql`.
  Plan [`2026-10-28-calendar-outlook-writeback.md`](../plans/2026-10-28-calendar-outlook-writeback.md).
  Docs: the calendar and Entra reference, module boundaries (rule 14's reads), the
  calendar user guide and the Entra administration page in both languages, the roadmap.
- **D** (`feat/calendar-outlook-notifications`): O11. Migration
  `00085_entra_mailbox_subscriptions.sql`. Plan
  [`2026-10-28-calendar-outlook-notifications.md`](../plans/2026-10-28-calendar-outlook-notifications.md).
  Docs: the calendar and Entra reference, module boundaries (rule 14's calls), the Entra
  and calendar administration pages and `admin/installation.md` in both languages, the
  roadmap.

## Testing

A: the contract's fake (`modtest`), the calendar's worker against it — the desired set,
the diff, the nudge, the window's edge, the clean-ups, the refusals, the backoff, the
1500 cap; the endpoints (availability reasons, If-Match, the 409); Staff's read
(the principal's own only; the states); Entra's provider against `graphtest` taught
calendars, events, the marker filter and `$batch` — the batch split, `Gone`, 403, the
`MailboxNotEnabledForRESTAPI`, the all-day zone, the unknown account; the settings PUT
and `calendarSync` statuses; the frontend cards in both languages.
