# Calendar phase 2, delivery B — Outlook's busy time in Vantigo: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person whose Outlook sync is on sees their Outlook busy time in their Vantigo
calendar, a colleague with `calendar:view-colleagues` sees it as busy, and Planning's
board shows each person's busy hours and days a week — within a sync's poll of the
change in Outlook, with nothing of an event stored but its times and `showAs`.

**Architecture:** the `calendar-mailbox-sync` worker, after the push, reads the person's
default calendar through two new calls on `contracts.MailboxCalendar` — `Delta` (Graph's
`calendarView/delta`) and `Marked` (the marker read for single events the map does not
know) — and keeps `calendar.mailbox_busy` and the delta link on `calendar.mailbox_sync`.
The calendar shows the rows as a new kind `calendar.outlook` on `GET /items` and a
colleague's calendar, and provides its first contract, `contracts.CalendarBusy`
(provider-gated), which Planning's board reads. Branch `feat/calendar-outlook-busy`, cut
from `main` at `5fb7fada` (delivery A, vantigo-io/vantigo#190, merged).

**Spec:** [`docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`](../specs/2026-10-27-calendar-outlook-sync-design.md), O9 (O9.1–O9.9).
**Research:** [`…/research/2026-10-27-calendar-outlook-sync.md`](../research/2026-10-27-calendar-outlook-sync.md) §3, *Delta, as checked for B*.

## Plan readings

1. **The contract** (`internal/contracts/mailbox_calendar.go`): `Delta` and `Marked` on
   `MailboxCalendar` exactly as O9.4; `MailboxDelta`, `MailboxChange` (`Series` true for
   an occurrence or exception; `ShowAs` one of the `ShowAs` constants, `""` unknown);
   `ErrMailboxDeltaExpired`. Doc comments: both refused under cleanup; a link is the
   provider's opaque string, valid only for the mailbox it was answered for; `Marked`
   takes at most 100 ids (`MailboxMarkedMax`), answers only this installation's marked
   ones, an id gone is not answered.
2. **The new contract** (`internal/contracts/calendar_busy.go`): `CalendarBusy.Busy`,
   `BusyTime{Spans []TimeSpan; Days []DateSpan}`, `TimeSpan{StartsAt, EndsAt time.Time}`
   (end exclusive); reuses `DateSpan` (workforce.go). Bounds as constants
   (`CalendarBusyMaxUsers = 500`, `CalendarBusyMaxDays = 400`) and
   `ErrBusyWindowOutOfBounds` for a wider ask. The doc comment says **provider-gated**
   and the rule (O9.8).
3. **The slot.** `module.Module.CalendarBusy func(Deps) contracts.CalendarBusy`,
   `Deps.CalendarBusy`; `Compose` resolves it with `soleProvider` last (after the
   mailbox calendar: the calendar's server reads the rest of `Deps`). Not in `Workers`
   (no worker reads it). Compose test.
4. **The fakes.** `modtest.FakeMailboxCalendar` learns a per-mailbox change log:
   `SeedForeign(mailbox, FakeBusyEvent)` / `MoveForeign` / `RemoveForeign` for the
   person's own events, every create/update/delete of a copy logged too (so the delta
   answers our copies as a real one would); `Delta` with `""` answers every event in the
   window, else the changes since the link's sequence, in pages of a settable size;
   `ExpireDelta(mailbox)` makes the next linked call answer `ErrMailboxDeltaExpired`;
   `Marked` answers from the copies' keys. `modtest.FakeCalendarBusy` for Planning's
   tests (a map, a record of the calls, and a gate flag).
5. **Migration** `00083_calendar_mailbox_busy.sql`: `calendar.mailbox_busy (user_id
   uuid, event_id varchar(500), all_day boolean, starts_at timestamptz, ends_at
   timestamptz, start_date date, end_date date, show_as varchar(20), updated_at
   timestamptz, PRIMARY KEY (user_id, event_id))`, index `(user_id, starts_at)` and
   `(user_id, start_date)`; `mailbox_sync` gains `busy_link text`, `busy_from date`,
   `busy_to date`, `busy_synced_at timestamptz`. No CHECK (house style). Down drops them.
6. **Queries** (`internal/calendar/queries/mailbox_busy.sql`): upsert a row, delete one
   by event id, delete all of a user's, count a user's, list a user's in a window (timed
   overlapping it, all-day overlapping its dates) joined to `mailbox_sync.enabled`, the
   same for a batch of users and a `show_as` set, set and clear the link/window/
   synced-at; and `ListBusyAppointmentsForUsers` (the colleague query's predicate over
   `user_id = ANY(@user_ids)`, answering the user each row is for).
7. **The calendar contract** (`openapi/calendar.yaml`): `CalendarItem.outlook`
   (`CalendarItemOutlook {showAs: enum busy|tentative|oof|workingElsewhere}`); the kind
   `calendar.outlook` in `GET /meta`'s sources while the caller's sync is on; the
   `MailboxSync` schema gains `busySyncedAt?`. **The Planning contract**
   (`openapi/planning.yaml`): `PlanningWeekFigures.busyHours?` (double) and `busyDays?`
   (integer), described as shown-not-subtracted.
8. **The read in the worker** (`internal/calendar/mailbox_busy.go`): after step 7 of
   O7 worked, `w.readBusy(ctx, call, r, mailbox, today)`: start fresh when no link, when
   `busy_to < today + 180`, or after `ErrMailboxDeltaExpired` (once); loop `Delta` while
   not `Done` and the deadline lasts; per page: drop ours by the map, collect the
   candidates for `Marked` (not removed, not series, would be stored, not mapped) in
   slices of 100, then write in one short transaction (a fresh start's first page
   deletes the rows first) the deletes and upserts and the link; `busy_synced_at` at the
   `deltaLink`. A refusal from `Delta`/`Marked` goes through `refusedOrFailed`, any
   other error `failed`. The accessors `mailboxDelta`, `mailboxMarked` in
   `contractscalls.go`.
9. **Forgetting** (O9.9): `cleanup()` and `write()` of a state `off` or `unavailable`
   delete the busy rows and clear the link, window and synced-at.
10. **The reads.** `items.go`: the `calendar.outlook` items from the rows (the kind
    filter honoured); `colleagues.go`: the colleague's `busy`/`oof` rows; `meta.go`: the
    kind while the caller's sync is on. `busy.go`: the `CalendarBusy` provider —
    `contracts.HasPermission` for `calendar:access` (own id) and
    `calendar:view-colleagues` (others), no request → nothing, the bounds, the
    appointments expanded by `occurrencesIn` in the business zone, the merge.
    `module.go`: `CalendarBusy: newCalendarBusy`.
11. **Planning** (`internal/planning`): an accessor `calendarBusy` in
    `contractscalls.go` (nil slot → nothing); the board asks it once, before shaping,
    for the people with a `UserID` over the board's window, and fills `busyHours`
    (merged spans clipped to each week in the business zone, rounded to 0.1) and
    `busyDays`; absent for a person the calendar answered nothing for.
12. **Entra** (`internal/entra/mailbox.go` or a new `mailbox_delta.go`): `Delta` —
    `open(…, false)`; a fresh call `GET /users/{oid}/calendarView/delta?startDateTime=
    …Z&endDateTime=…Z`, a linked call the link after checking it starts with
    `graphAPIBase + "/users/{oid}/calendarView/delta?"`; `Prefer: outlook.timezone="UTC",
    odata.maxpagesize=50`; at most 40 pages; decodes only `id`, `@removed`, `type`,
    `isAllDay`, `isCancelled`, `showAs`, `start`, `end` (the body's cap checked against a
    page of 50 events with bodies); expired → `ErrMailboxDeltaExpired`; other refusals
    through `mailboxRefusal`. `Marked` — `$batch` GETs of 20, 404 not answered, the key
    cut from this installation's prefix. Graph's words never logged. `graphtest` learns
    `calendarView/delta` (a change log per mailbox, the window, `@removed`, paging by
    `$skiptoken`/`$deltatoken`, a settable expiry answering 410 or `syncStateNotFound`)
    and the single-event GET with the marker expand inside `$batch`.

## Tasks

- [ ] **1. The research, the spec's O9 and this plan.** Committed first.
- [ ] **2. The contracts, the slot, the migration, the queries, the API and the fakes (one worker, alone).**
  Readings 1–7; `sqlc generate` (calendar); `go generate ./...` in `apps/server`; stub
  `Delta`/`Marked` in Entra answering `ErrMailboxUnavailable` and a stub `CalendarBusy`
  in the calendar answering nothing, so every package builds; `bun run gen:client`;
  `go build ./... && go vet ./...` and the full `server:test`.
- [ ] **3a. Entra (`internal/entra`), after 2.** Reading 12 with tests against
  `graphtest`: fresh and linked, paging, the page bound, `@removed`, series, all-day
  rounding, expired (410 and the codes), a foreign link refused, cleanup refused, 403,
  gone, `Marked` (ours, another installation's, none, 404).
- [ ] **3b. The calendar (`internal/calendar`), after 2.** Readings 8–10 with tests
  against the fake: ours skipped by the map and by `Marked`, free/cancelled/removed,
  the fresh start and its emptying, the month's restart, the expired link, the page
  loop and the deadline, the 5000 cap, the forgetting, the items, the colleague, meta,
  `CalendarBusy`'s gate and bounds and merge, the lock rule.
- [ ] **3c. Planning (`internal/planning`), after 2.** Reading 11 with tests against
  `FakeCalendarBusy`: the week figures, a person without a user, nothing answered,
  the call outside any transaction.
- [ ] **4. The frontends, after 2.** `@vantigo/calendar-ui`: the `calendar.outlook`
  kind's colour and label, the item's label by `showAs`, the colleague's busy blocks,
  the Outlook card's sentence on busy time, words in `en` and `nb`, tests.
  `@vantigo/planning-ui`: the busy hours and days in the board cell's detail, words in
  `en` and `nb`, tests.
- [ ] **5. Documentation, after 3 and 4.**
  - `reference/calendar.md`: *Outlook sync* — the busy read, `mailbox_busy`, the
    columns, `calendar.outlook`, the colleague's view, the `CalendarBusy` contract the
    module now provides; the schema's migration list.
  - `reference/entra.md`: the mailbox calendar's `Delta` and `Marked`, their Graph calls
    and refusals.
  - `reference/planning.md`: `busyHours`, `busyDays`, the read of `CalendarBusy`.
  - `contributing/module-boundaries.md`: rule 5's slot list (`CalendarBusy`), rule 12
    (provider-gated `CalendarBusy`), rule 14 (the reads).
  - `user/calendar.md` (en, nb): Outlook busy time in one's own and a colleague's
    calendar. `user/planning.md` (en, nb): the busy hours on the board.
  - `admin/entra.md` (en, nb): the sync now reads the default calendar too
    (`Calendars.ReadWrite` covers it; nothing to add).
  - `ROADMAP.md`: B done.
- [ ] **6. Review, then verification.** A security review (read-only) of the contract,
  the worker's read and the provider, a docs pass against the code, one fix batch; then
  `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`; push; the pull request with the real-tenant checklist.
