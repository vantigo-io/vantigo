# Calendar — design (phase 1)

A new module, `calendar`, researched in
[`../research/2026-10-19-calendar-module.md`](../research/2026-10-19-calendar-module.md):
one view of every time-bound thing the other modules know about one person — the
follow-ups Customers holds for them, their absences in Staff, their tasks in Projects,
their hours in Time, their trips in Expenses — beside the **appointments** they schedule
themselves, with colleagues on them; later the invitations that leave the company, the
feed a phone subscribes to, and the calendars subscribed from elsewhere. The time zones
design ([`2026-10-19-time-zones-design.md`](2026-10-19-time-zones-design.md)) is its
prerequisite and is built: every module judges the business day through
`contracts.BusinessCalendar`, and every user has an effective personal zone the session
answers. The public-holiday package has already given up the name and is
`internal/holidays`.

Like Expenses and Staff the module **depends on nobody but identity**. What it shows
from the other modules it never copies: each enabled module **projects** its own rows
into the calendar at read time, through a new many-provider contract, under its own
permission gate, so a follow-up ticked in Customers is ticked on the calendar with
nothing to synchronise. The one thing the module owns is the appointment.

Phase 1 is three deliveries. **A** is the calendar: the module, the appointments with
recurrence and colleagues, the sources contract with five providers, the views and the
dashboard's agenda. **B** is invitations that leave the company, by mail with native
RSVP buttons and a web answer. **C** is the feed and the subscriptions. A is specified
in full here; B and C take their decisions here and the rest with their own plans.

## Decisions

### D1 — The sources contract: a many-provider slot, each provider its own gatekeeper

```go
// contracts.CalendarSource is a module's view of its own rows as calendar items.
// A module that has time-bound rows about a person fills the slot; the calendar
// module reads every filled one, enabled modules only, and never a module's table.
type CalendarSource interface {
    // CalendarKinds names the kinds this provider may answer, so the calendar can
    // list its sources — for the filter, the legend and the colours — without a
    // query. Stable keys, "<module>.<noun>": "customers.follow-up".
    CalendarKinds() []CalendarKind
    // CalendarItems answers the caller's items in the window. The provider applies
    // its own visibility rule to the principal in ctx and answers nothing — not an
    // error — when the caller lacks its permission; the calendar shows what comes
    // back and never says what was withheld. Called with the request's context,
    // never inside a transaction of the caller's.
    CalendarItems(ctx context.Context, q CalendarQuery) ([]CalendarItem, error)
}

type CalendarKind struct {
    Key    string // "projects.task"
    Module string // "projects"
}

type CalendarQuery struct {
    UserID uuid.UUID
    From   time.Time // the first day, a civil date at midnight UTC, inclusive
    To     time.Time // the last day, the same form, inclusive
    Zone   *time.Location // the viewer's zone, for a provider that buckets an instant
}

type CalendarItem struct {
    Kind     string    // one of the provider's CalendarKinds
    ID       string    // stable within the kind: the row's id
    Title    string
    Subtitle string    // the customer's name, the project's code — one line, may be empty
    AllDay   bool
    // An all-day item spans civil dates, EndDate inclusive, both at midnight UTC.
    StartDate, EndDate time.Time
    // A timed item spans instants, EndsAt exclusive. Zero when AllDay.
    StartsAt, EndsAt time.Time
    Done     bool      // a follow-up done, a task done, an entry approved: rendered struck
    Path     string    // the SPA route of the record, "/customers/42", for the link back
}
```

The slot is `CalendarSources []contracts.CalendarSource` on `module.Deps`, filled from
`CalendarSource func(Deps) contracts.CalendarSource` on `module.Module`, and collected
by `Compose` and `Workers` **from enabled modules only**, the way `Workers` is and
unlike `CustomerReferences` — a disabled module's permissions are not in the catalog,
so its gate could only fail closed, and its tables may be empty. The name is not
`Calendar`: `Deps.Calendar` is the business calendar identity provides, and the two must
not be confused in a reader's eye or in `Compose`'s `soleProvider`. The module reads the
slot through one accessor in `contractscalls.go` that refuses a call inside a
transaction, Staff's `errCallInsideTransaction`.

Why each provider gates itself, as Customer 360's sections do: the rule is the
provider's — Projects' `visible()`, Time's "mine or on a project I manage", Staff's
audience — and it already exists in each module as a query the module owns. The
calendar re-stating it would be a copy that drifts. The calendar passes the request's
context, since `contracts.HasPermission` reads the request from it and answers false
without one; a provider called from a worker (none in phase 1) would answer nothing.

A provider that **fails** fails the whole request (`GET /items` is 503 with a problem
naming no module): absent must never also mean "could not be read", Customer 360's
rule. The five providers of delivery A, each a small file in its module, each with the
module's own query:

| Provider | Kind | The rows | The gate | Rendered | `Path` |
| --- | --- | --- | --- | --- | --- |
| Customers | `customers.follow-up` | manual, active entries with `follow_up_on` in the window, assigned to the caller; on a customer that is not merged away or anonymised | `customers:timeline-view` and `customers:view` | all-day on `follow_up_on`; `Done` when `follow_up_done_at` is set; title the entry's summary, subtitle the customer's name | `/customers/{customerId}` |
| Customers | `customers.interaction` | the caller's own manual entries (`actor_user_id`) of the three interaction kinds with `occurred_on` in the window | the same pair | an all-day item on `occurred_on`, or a point at `occurred_at` when set (`EndsAt` = `StartsAt`, the view draws the default length); title the summary, subtitle the customer | `/customers/{customerId}` |
| Staff | `staff.absence` | the linked person's own absences — requested, approved or registered — overlapping the window | `staff:access` and a link (no link, no items) | all-day over `first_day`..`last_day`; an hour type's one day with the hours in the subtitle; title the type's name in the caller's language, the status when not approved | `/staff/me?tab=absence` |
| Projects | `projects.task` | tasks assigned to the caller with a `due_date` in the window, on a project `projects.visible()` admits (the caller's role, or view-all/manage-all), any status | `projects:access` | all-day on `due_date`; `Done` when `status = 'done'`; title the task's, subtitle the project's code | `/projects/{projectId}/tasks?task={id}` |
| Time | `time.entry` | the caller's own entries with `entry_date` in the window | `time:access` | a block from `start_time` to `end_time` read as wall-clock in the **business** zone when both are set, else all-day; title the project's name or the note's first line, subtitle the hours; `Done` when approved | `/time/day?date={entryDate}` |
| Expenses | `expenses.claim` | the caller's own travel claims whose `departure_at`..`return_at` overlaps the window | `expenses:access` | a block over the two instants; title the purpose, subtitle the destination; `Done` when approved | `/expenses/claims/{id}` |

Each is "mine" by construction — the calendar is a person's — so Time's and Expenses'
"everyone's" keys widen nothing here, and Projects' view-all only widens which of *my*
tasks are visible. The queries that exist are reused where they fit (Time's
`ListWeekEntries` generalised to a range) and added where they do not (Customers'
follow-ups have no range parameter; Projects' `ListMyOpenTasks` is open-only and the
calendar wants the done ones struck). None of the five touches a table it does not own.

Public holidays are not a provider: the calendar reads `holidays.PublicHolidays`
itself, a platform package every module may import, and answers them beside the items.

### D2 — The appointment: one table, instants or dates, the zone it was made in

Migration `00055_calendar_baseline.sql` creates the schema and three tables:

```sql
CREATE SCHEMA calendar;

CREATE TABLE calendar.appointments (
    id               bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    owner_user_id    uuid         NOT NULL,
    title            varchar(200) NOT NULL,
    description      varchar(10000),
    location         varchar(200),
    all_day          boolean      NOT NULL,
    start_date       date,                 -- all-day: the first day
    end_date         date,                 -- all-day: the last day, inclusive
    starts_at        timestamptz,          -- timed: the instant it starts
    ends_at          timestamptz,          -- timed: the instant it ends, exclusive
    time_zone        varchar(64)  NOT NULL, -- the TZID the appointment was made in
    visibility       varchar(10)  NOT NULL, -- 'shared' | 'private'
    transparency     varchar(10)  NOT NULL, -- 'busy' | 'free'
    rrule            varchar(500),          -- RFC 5545 RRULE, no DTSTART, null when single
    series_last_date date,                  -- the last occurrence's day, null when unbounded or single
    customer_id      integer,               -- opaque: the customer it was made from
    project_id       integer,               -- opaque: the project it was made from
    ical_uid         varchar(255) NOT NULL,
    sequence         integer      NOT NULL DEFAULT 0,
    revision         integer      NOT NULL DEFAULT 1,
    created_at       timestamptz  NOT NULL,
    updated_at       timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_appointments_ical_uid ON calendar.appointments (ical_uid);
CREATE INDEX ix_appointments_owner_window ON calendar.appointments (owner_user_id, starts_at, start_date);

CREATE TABLE calendar.appointment_attendees (
    id             bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    appointment_id bigint      NOT NULL REFERENCES calendar.appointments (id) ON DELETE CASCADE,
    user_id        uuid        NOT NULL,
    status         varchar(12) NOT NULL, -- 'needs-action' | 'accepted' | 'declined' | 'tentative'
    responded_at   timestamptz,
    created_at     timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_appointment_attendees ON calendar.appointment_attendees (appointment_id, user_id);
CREATE INDEX ix_appointment_attendees_user ON calendar.appointment_attendees (user_id);

CREATE TABLE calendar.appointment_exceptions (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    appointment_id  bigint      NOT NULL REFERENCES calendar.appointments (id) ON DELETE CASCADE,
    occurrence_date date        NOT NULL, -- the occurrence's day in the appointment's zone, as the series would have made it
    cancelled       boolean     NOT NULL,
    start_date      date,                 -- a moved all-day occurrence
    end_date        date,
    starts_at       timestamptz,          -- a moved timed occurrence
    ends_at         timestamptz,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_appointment_exceptions ON calendar.appointment_exceptions (appointment_id, occurrence_date);
```

The time zones design's D1 is kept to the letter: a **timed** appointment is two UTC
instants, an **all-day** one is two civil dates, and the module's validation — not a
CHECK, by house style — holds that exactly one pair is set, that `end_date >= start_date`
and `ends_at > starts_at`, and that a timed appointment is at most 31 days and an all-day
one at most 366. `time_zone` is the one thing the design asked this module to store:
the **TZID the appointment was made in**, the creator's effective zone at creation —
taken from the request body, where the SPA puts the session's `timeZone`, and held to the
workspace zone's two checks (Go loads it; Postgres agrees). A recurring appointment's
"every Monday 09:00" is 09:00 in that zone across the clock change: the `rrule` expands
in `time_zone` and the occurrences' instants are derived from it (D4), never the other
way round. A later change of the person's own zone changes how they *see* it, not what
was agreed.

`visibility` is iCalendar's `CLASS` (`shared` = PUBLIC, `private` = PRIVATE) and
`transparency` its `TRANSP` (`busy` = OPAQUE, `free` = TRANSPARENT): the two words a
colleague's view (D6) and the feed (C) need, stored in this module's own vocabulary and
translated at the edge. Defaults: `shared`, `busy`. `ical_uid` is `<uuid>@vantigo` from
creation — the feed and the invitations (B, C) need a stable one and a `sequence` that
counts every change of when or where, so both exist from the first row rather than being
backfilled. `customer_id` and `project_id` are opaque ids (module boundaries rule 4), set
when the appointment is made **from a record** — the customer page's "New appointment"
— and answered on the response with the name resolved through `Deps.Directory` and
`Deps.Projects` when those are wired, blank when not; the calendar does not refuse an id
it cannot resolve, as Expenses does with a project, because an appointment is not booked
on anything and the link is a courtesy. `revision` is `If-Match`'s, Staff's convention.

### D3 — Endpoints, two permissions

Two keys, category "Calendar", both delegable:

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `calendar:access` | Use Calendar | the calendar, one's own appointments and answers, the dashboard's agenda. **Every operation requires it.** | no |
| `calendar:view-colleagues` | See colleagues' calendars | a colleague's appointments as busy or free, with the title when shared | no |

No `manage` key: the module has no settings in A, and a colleague's view is the one
thing not everyone should have by default — an installation that wants every employee
to see every other's busy blocks grants the second key to the employee role.

All under `/api/v1/calendar`:

| Operation | Access |
| --- | --- |
| `GET /meta` — `timeZone` (the business zone), `today`, `canViewColleagues`, `sources` | `calendar:access` |
| `GET /items?from&to&timeZone&kinds` — the caller's calendar in a window (D5) | `calendar:access` |
| `GET /appointments/{id}`, `POST /appointments`, `PUT /appointments/{id}`, `DELETE /appointments/{id}` | `calendar:access`, then the owner or an attendee (reads) and the owner (writes), otherwise 404 |
| `PUT /appointments/{id}/occurrences/{date}`, `DELETE /appointments/{id}/occurrences/{date}` — move or cancel one occurrence, and put it back | `calendar:access`, the owner, otherwise 404 |
| `POST /appointments/{id}/response` — `{ status }` from an attendee | `calendar:access`, an attendee, otherwise 404 |
| `GET /colleagues/{userId}/items?from&to&timeZone` (D6) | `calendar:access` + `calendar:view-colleagues` |

The appointment on the wire:

```
Appointment {
  id, title, description?, location?, allDay,
  startDate?, endDate?,            // all-day, civil dates
  startsAt?, endsAt?,              // timed, instants
  timeZone, visibility, transparency,
  recurrence?: { rule, lastDate? },      // the RRULE as stored; lastDate when bounded
  customer?: { id, name? }, project?: { id, code?, name? },
  owner: { id, displayName }, attendees: [{ user: {id, displayName}, status, respondedAt? }],
  myStatus?,                       // the caller's own, when an attendee
  revision, createdAt, updatedAt
}
```

`POST` takes the same shape minus the server's fields, with `attendees: [userId…]` —
active users in the directory, at most 50, the owner never among them — and
`timeZone` required. `PUT` takes `If-Match` and the whole shape; a change of when, where
or of the rule bumps `sequence`, and a change of when **resets every attendee to
`needs-action`** except the owner's own, since what they answered is no longer what is
asked. `DELETE` removes the series with its attendees and exceptions — the module keeps
no cancelled state in A, because nobody outside the company has been told; B, which
tells them, adds `cancelled_at` with the `METHOD:CANCEL` that must follow. A response is
`accepted`, `declined` or `tentative`; `needs-action` is not something a person sends.
An attendee who declined still sees the appointment, struck, and may change their
answer; the owner's status is implicitly accepted and not stored.

The refusals are this module's `CalendarProblem`: field errors on the body (400), 404
for what the caller may not see, 409 `revision_mismatch` on a stale `If-Match`, 409
`occurrence_not_in_series` for a date the rule never produces, and the uniform 403 from
the access layer. Writes take the appointment's row lock (`SELECT … FOR UPDATE`) in one
transaction, Staff's `withLockedTx`, and resolve the directory **before** it opens.

### D4 — Recurrence: an RRULE subset, expanded in the appointment's zone, exceptions by day

`recurrence.rule` is an RFC 5545 `RRULE` value — `FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;
COUNT=10` — without `DTSTART`, which is the appointment's start. The subset accepted:
`FREQ` of `DAILY`, `WEEKLY` or `MONTHLY`; `INTERVAL` 1 to 99; `BYDAY` with plain weekday
names for `WEEKLY`, and with one ordinal (`2TU`, `-1FR`) for `MONTHLY`; `BYMONTHDAY`
with one day for `MONTHLY`; one of `COUNT` (at most 730) or `UNTIL` (a date, at most
five years after the start), or neither. Anything else — `YEARLY`, `BYHOUR`, `BYSETPOS`,
a second `BYDAY` list, `WKST` — is a field error on `recurrence.rule`. The rule is
parsed with `teambition/rrule-go` and **re-serialised** before it is stored, so the row
holds a canonical form, and `series_last_date` is computed from it at write time: the
last occurrence's day for `COUNT` and `UNTIL`, null when the series never ends. An
unbounded series costs nothing: expansion is always windowed (D5).

Expansion is in `time_zone`: `DTSTART` is `starts_at` in that zone (or `start_date` at
midnight in it), the rule yields wall-clock occurrences, and each occurrence's instants
are that wall-clock in that zone converted to UTC — so a 09:00 stays 09:00 through
March and October. The occurrence keeps the appointment's duration. An occurrence is
named by its **date**, `occurrence_date`: the civil day in `time_zone` the series put
it on, which the rule guarantees unique per occurrence (at most one a day in this
subset). That date is the key of an exception and of the two occurrence endpoints.

An **exception** is one of two things, never both: `cancelled`, or a move (`startsAt`/
`endsAt` for a timed series, `startDate`/`endDate` for an all-day one, the pair the
series uses). A moved occurrence keeps its `occurrence_date` as its identity and shows at
its new time; it may move to another day and still answers to the day it came from.
`PUT /appointments/{id}/occurrences/{date}` creates or replaces the exception (409
`occurrence_not_in_series` when the rule never produced `{date}`, judged by expanding
the rule to that day); `DELETE` removes it, and the series' own occurrence returns. A
`PUT` of the appointment that changes the rule or the start **drops every exception**,
with a sentence in the sheet saying so: the days the exceptions named may no longer
exist, and a person re-does the one or two that matter. The iCalendar names for all of
this (`RECURRENCE-ID`, `EXDATE`, an overriding `VEVENT`) are C's, and the model is shaped
so C can write them without a migration.

### D5 — The window: `GET /items`, at most 93 days, the viewer's zone

`from` and `to` are civil dates, inclusive, at most 93 days apart (the month grid's six
weeks and a quarter's list both fit; Staff's limit is the same); `timeZone` is the
viewer's effective zone, which the SPA takes from the session and the server holds to
the two checks, defaulting to the business zone when absent; `kinds` is an optional
comma list to narrow the providers asked (the filter is the client's, but a dashboard
that wants only appointments need not pay for five queries). The answer:

```
CalendarItems {
  from, to, timeZone,
  sources: [{ kind, module }],         // every kind a wired provider declares, enabled modules only
  publicHolidays: [{ date, name }],    // Norway's, from the platform package
  items: [ CalendarItem… ]
}
CalendarItem {
  kind,                                // "calendar.appointment" or a provider's
  id,                                  // the appointment's "1001" or "1001:2026-11-03" for an occurrence; a provider's row id
  title, subtitle?, allDay, startDate?, endDate?, startsAt?, endsAt?, done,
  path?,                               // a provider's record; absent for an appointment, which the sheet opens by id
  appointment?: { id, occurrenceDate?, isOwner, myStatus?, visibility, transparency, recurring }
}
```

Items are built in order: the caller's appointments — owned, or attended with any
status — whose span overlaps the window (a single one by its instants or dates; a series
when it starts no later than `to` and `series_last_date` is null or no earlier than
`from`), expanded in their own zones over the window and with their exceptions applied;
then each provider's `CalendarItems` with the window and the zone, in module order; then
the holidays. The window's edges are judged in `timeZone`: a timed item is in the window
when its instants overlap `[from 00:00, to 24:00)` in that zone, an all-day one when its
dates overlap `[from, to]` — so the 23:30 appointment the research worried about lands
on the viewer's day. The list is sorted by start, all-day first within a day, and not
paged: 93 days of one person's calendar is hundreds of rows, not thousands, and a grid
needs all of them. Providers are asked **concurrently** under one `errgroup`, each on
the pool, since none shares a transaction, with a budget of ten seconds for the lot.

### D6 — A colleague's calendar: busy or free, the title only when shared

`GET /colleagues/{userId}/items` answers the same window shape with only `kind:
"calendar.appointment"` items and no sources: the colleague's appointments — owned, or
attended and accepted — with `transparency = 'busy'`, expanded as D5 does; a `shared` one
with its title and location, a `private` one as **"Busy"** with nothing else and
`visibility: "private"` so the view can draw it differently; `free` ones omitted
altogether, since the point of the view is "when can I book them". The colleague must be
an active user in the directory, otherwise 404; the caller's own id is answered as
their own calendar would be, with the private titles, so the SPA need not special-case
it. No provider is asked: a colleague's follow-ups, tasks and hours are the other
modules' data behind their own doors, and Staff already answers a colleague's absence
days on its own calendar to the audience it chooses. The one audience rule here is the
permission: `calendar:view-colleagues` sees every active colleague this way, with no
manager relation — the calendar has no org chart and does not borrow Staff's, which may
be off. **UNCERTAIN**, and the owner's to overrule: whether the colleague's absences
(dates only, as Staff shows colleagues) should also appear here, through Staff's
provider answering for a different person; left out of A, trivially added.

The SPA's picker lists active users through identity's directory search, as Projects'
assignee picker does.

### D7 — The dashboard: an agenda at the top, the attention list unchanged

`/dashboard` gains a **Today and this week** card at the top of the page when the
module is on and the caller holds `calendar:access`: `GET /items` from today for seven
days in the viewer's zone, rendered by `@vantigo/calendar-ui`'s `AgendaWidget` — today's
items in order with their times, each a link to its record or its appointment, the next
six days collapsed to one line each with a count, a **New appointment** action and a
link to the full calendar. It is the one `WidgetCard` on the page every employee has a
reason to look at, which is why it sits above the manager's figures.

The attention list **keeps its dated items**: a follow-up due and a task due stay there,
and also appear on the agenda. The two say different things — the list says "this needs
you", the agenda says "when" — and removing them from the list is a change in five
modules' `/stats/attention` for a saving of two lines. Decided for A; revisited if the
duplication annoys in use.

The host also gains, on the customer page's **Overview** and the project page's header,
a **New appointment** action that opens the sheet with `customerId` or `projectId` set
(D2) — the "created from a record" every comparable product has — gated on the module
and the permission as the cross-module tabs are. No new tab: an appointment is not a
customer's record to list, it is the person's; the customer page says where it came
from through the appointment's own `customer` field.

### D8 — The app: FullCalendar in `@vantigo/calendar-ui`, four views, a sheet

`apps/calendar/frontend`, package `@vantigo/calendar-ui`, laid out as Expenses' is
(`api/`, `pages/`, `components/`, `lib/`, `i18n.ts`, `test/`). The grid is
**FullCalendar 7** (`@fullcalendar/react`, `core`, `daygrid`, `timegrid`, `list`,
`interaction`; MIT), chosen over a grid of our own because a week view with a time axis,
an all-day row, drag-to-create and resize, the `nb` locale and the accessibility work are
a year of someone's time, and over the paid tiers of the alternatives. **UNCERTAIN**, as
the research said: how its theme sits inside Mantine. The package maps FullCalendar's
CSS variables to Mantine's in one stylesheet, and the first task of the plan proves the
week and month views inside the shell before anything else is built; if the result is
not ours, the fallback is a month grid and an agenda of our own on Mantine's `Table`,
Staff's `MonthTable` generalised, with the week grid deferred.

The page, `/calendar?view=month|week|day|list&date=YYYY-MM-DD&user=<uuid>`:

- **Month**, **Week**, **Day** and **List** (the agenda; the default under phone width,
  where the grids do not fit), with a toolbar of today, previous, next, the view
  switch and the date, every date read and written in the viewer's zone through the
  shell's `civilDateIn` and `instantFromWallClock`.
- A **legend and filter** of the sources `GET /meta` lists, one colour per kind from a
  fixed palette keyed by module, each togglable, the state kept in `localStorage` per
  browser — a convenience, not a setting, so no table.
- **Appointments** drawn with the owner's colour; a `private` one of a colleague as a
  hatched "Busy"; a `free` one outlined; a declined one struck; a recurring one with the
  repeat glyph. Clicking an appointment opens the **sheet**; clicking a projected item
  follows its `path`.
- The **sheet** (a drawer, or a modal under phone width, Staff's `RequestSheet` shape):
  title, all-day switch, the dates or the date-and-time pair in the viewer's zone,
  location, description, visibility, transparency, a recurrence control (none, daily,
  weekly on chosen days, monthly on the day, every *n*, until a date or *n* times —
  rendered to the rule and parsed back from it), attendees picked from the directory
  search, and the customer or project it was made from. For an occurrence of a series
  the sheet asks **This occurrence** or **The series** before a change or a delete, and
  for the series' change says the exceptions will go. For an attendee the sheet is
  read-only with Accept, Tentative and Decline.
- Drag-to-create on the grids opens the sheet with the span filled; drag and resize of an
  own appointment issue the `PUT` (with the same occurrence question for a series);
  projected items are not draggable — moving a task's due date from here needs a write
  contract back into Projects, which is "later".
- The **colleague picker** (shown with `canViewColleagues`) swaps the calendar for the
  colleague's, read-only, with the legend reduced to busy and free.

`AgendaWidget` (D7) is exported for the host. Strings in `i18n.ts`, en and nb; dates
and times through `useI18n().formatters` and the session's zone.

### D9 — What B and C add, decided now so A's model holds

**B — Invitations.** Attendees gain two kinds beside a user: a **contact** of a customer
(`contact_id`, name and address read through `Deps.Directory.Contact` at send time, so a
corrected address is picked up by the next update) and a typed **address** (`email`,
`name`), each with an `rsvp_token_sha256`. The invitation is the research's iMIP layout —
a `text/calendar; method=REQUEST` alternative part the clients key on, plus the `.ics`
attachment — built with `arran4/golang-ical`, `UID` the row's `ical_uid`, `SEQUENCE` the
row's, every attendee on every copy, `ORGANIZER` **and** `From` the installation's
`SMTP_FROM` with the owner's name as `CN`, which is the safe choice the research found,
sent after the commit by the module's own `smtpSend` accessor, Staff's shape. **RSVP is a
web link** in the body — one token per attendee, `POST /api/v1/calendar/rsvp` with the
token in the body on a public route, the pay link's shape, answered by a page on the
public host — and the mail says so, since a native button's reply lands in a mailbox
nobody reads until an inbound route exists (deliberately "later"; the ICS is written so
it can be added). An update resends `REQUEST` with `SEQUENCE+1`; a delete becomes a
cancellation: `cancelled_at` on the row, `METHOD:CANCEL` to everyone outside, the row
kept until the series' last day has passed and then erased by a small worker. A
**colleague** on an appointment is mailed through a new seam, not through an address
crossing the boundary: `contracts.UserDirectory` gains `Notify(ctx, userID, Notice)` —
identity builds and sends a plain mail to the user's address and the calendar never sees
it, the same "mail them, do not hand the value over" line `PermissionHolders` and the
provisioning invitation draw; a colleague gets no ICS and no native buttons, because
their answer is the in-app one and the appointment is already on their Vantigo calendar.
Decided here: the time zones design's D5 said this method would come with this mail.

**C — The feed and the subscriptions.** `calendar.feed_tokens (user_id, variant, token_
sha256, created_at, last_used_at)`, one row per user per variant (`full`, `busy`), the
token 32 random bytes `base64.RawURLEncoding` as the pay link's, shown once and reset from
the calendar's settings. `GET /calendar/feed/{token}.ics` is the module's one anonymous
`GET` with a token in the path — the research's argued exception to the pay link's rule:
the clients allow nothing else; the token is hashed at rest, per user, per variant,
resettable, rate-limited per client address, and the feed carries nothing the person's
own calendar does not (and the busy variant carries nothing at all but spans). The route
joins `security.PayRoutes()` for the public host, with `TestPublicHostGate`. The feed is
windowed (−3/+12 months), writes series as `RRULE` with `EXDATE` and overriding
`VEVENT`s, one `VTIMEZONE` per `TZID`, `REFRESH-INTERVAL` and `X-PUBLISHED-TTL` of an hour,
`ETag` and 304, and the settings page says the phone decides when it refreshes. Projected
items are in the full feed as all-day or timed `VEVENT`s with the module's name in
`CATEGORIES`, read through the same providers under the token's user. **Subscriptions**
(`calendar.subscriptions`: `user_id`, `url`, `name`, `colour`, `busy_only`, `etag`,
`last_modified`, `fetched_at`, `body` of at most 1 MiB, `error`) are polled by a
`calendar-subscriptions` worker every 30 minutes with jitter under an advisory lease,
fetched through `netguard` with no redirects and a size cap, parsed on read over the
window; subscribed items render on the person's calendar and are **never** in their own
feed. The parsed item takes `kind: "calendar.subscription"` and the subscription's name
as its subtitle. C adds `calendar:access`'s one settings screen, **My calendar**: the
two feed addresses and their reset, the subscriptions.

### D10 — Docs and roadmap

- `docs/src/content/docs/en/reference/calendar.md`, new: the schema, the permissions, the
  sources contract with the six kinds and each gate, the appointment and its rules, the
  recurrence subset, the window, a colleague's view, the dashboard's agenda, refusals,
  locks, endpoints; `sources: [apps/server/internal/calendar, apps/calendar/frontend,
  openapi/calendar.yaml]`. `reference/index.md`'s module table gains the row.
- `docs/src/content/docs/{en,nb}/user/calendar.md`, new, `sidebar.order: 35` (after Time):
  the views, what lands on the calendar and from where, an appointment, repeating ones,
  colleagues, the dashboard's agenda, the permissions; `user/index.md`'s app table gains
  the row; `user/getting-started.md`'s dashboard passage names the agenda, in both
  languages.
- `docs/src/content/docs/{en,nb}/admin/index.md`: the module in the list, with the two
  keys and that it needs nothing an installation does not already have.
- `docs/src/content/docs/en/contributing/module-boundaries.md`: `CalendarSources` under
  the many-provider slots and a rule for providers ("the provider is the gatekeeper");
  the checklist's "many-provider slot" example.
- `openapi/calendar.yaml` and `docs/astro.config.mjs`'s `contracts` list: the API
  reference regenerates.
- Each provider module's reference page gains one paragraph, "On the calendar", saying
  what it projects and under which key: `reference/customers.md`, `staff.md`,
  `projects.md`, `time.md`, `expenses.md`.
- `ROADMAP.md`: a `## Calendar` section after Point of sale with phase 1's three
  deliveries, A marked built when it lands, and a *Later* paragraph (two-way Google and
  Microsoft sync, booking pages, rooms and resources, reminders in-app and by mail,
  CalDAV, the inbound `METHOD:REPLY` route, moving a projected item, team calendars and
  delegation); the Platform section's last sentence updated.

## Out of scope

Two-way sync with Google or Microsoft; booking pages; rooms and resources; attachments
on an appointment; in-app or mail reminders (`VALARM` on the feed and in the invite is
C's and B's, for the client to ring); CalDAV; the inbound reply route; dragging a
projected item; team calendars, delegation and a manager's view of reports (Staff's
calendar is that for absence); a colleague's projected items (D6); a `manage`
permission and settings in A; invoices' due dates and billing milestones (a company
view, not a person's; the dashboard's attention list carries them); `YEARLY` and the
rest of the RRULE grammar (D4); an appointment longer than a month; more than one
occurrence a day from one rule; cancelled appointments kept after a delete (B's); the
SCIM `timezone` attribute.

## Testing

**Backend** (`internal/calendar`, through `modtest`, `contracttest.RequireCoverage` on
every operation): the migration applies, rolls back and re-applies; `POST` of an all-day
and a timed appointment is 201 with the stored zone, `ical_uid` and `sequence` 0; both
pairs or neither, an end before the start, a 32-day timed span, a zone Postgres reads
differently, a 51st attendee, an inactive or unknown attendee and the owner as attendee
are each a field error; every rule in the subset round-trips canonically and
`series_last_date` is right for `COUNT`, `UNTIL` and none; `YEARLY`, `BYSETPOS` and a
second `BYDAY` list are refused; a weekly 09:00 Oslo series expanded across the October
clock change keeps 09:00 wall-clock and shifts UTC; an exception cancels an occurrence,
moves one to another day while it answers to its date, is refused for a day the rule
never produces, and is dropped by a `PUT` that changes the rule; `GET /items` returns
owned and attended appointments, expanded, sorted, within a 93-day window and refuses 94;
an appointment at 23:30 Oslo is on the Oslo day for a viewer in `Europe/Oslo` and on the
next day for one in `Asia/Tokyo`; each of the five providers answers its rows for the
caller with the gate on (a fake per module in the harness, and the real provider in the
module's own package: Customers' follow-ups assigned to me and not another's, a done one
struck, a merged-away customer's absent; Staff's own absences and nothing without a link;
Projects' tasks only on visible projects; Time's timed block in the business zone;
Expenses' claim overlapping the edge) and nothing with it off; a provider that errors
makes the request 503; `kinds` narrows which are asked; a colleague's view shows shared
titles, "Busy" for private, nothing for free, 404 for an inactive user, and 403 without
the key; a response from a non-attendee is 404, from an attendee 200 and reflected in
`myStatus`; a `PUT` that moves the time resets the attendees' answers and bumps
`sequence`, one that changes the title does neither; a stale `If-Match` is 409; `DELETE`
cascades; every operation is 403 without `calendar:access`; the module is absent from
`MODULES` and the providers answer nothing to nobody (the slot is collected from enabled
modules only).

**Frontend** (`apps/calendar/frontend`, vitest, `TZ` pinned off Oslo as the others do):
the month, week and list views render the items from a fake `/items` in the session's
zone; the filter hides a kind and remembers it; the sheet creates an all-day and a timed
appointment with the session's `timeZone` in the body; the recurrence control writes and
reads each rule shape; the occurrence question issues the occurrence `PUT` or the series
`PUT`; an attendee's sheet offers the three answers and nothing else; the colleague
picker loads the colleague's items; `AgendaWidget` lists today's items and six collapsed
days; the host's dashboard shows the widget with the module on and the permission held,
not otherwise; `apps.test.ts`'s lists and the catalog test count the module.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
