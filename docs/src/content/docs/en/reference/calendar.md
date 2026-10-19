---
title: "Calendar module"
description: "One person's calendar: the appointments the module owns, with recurrence, colleagues and their answers; the sources contract through which other modules project their rows; the window read in the viewer's zone; a colleague's busy and free; the dashboard's agenda; the permissions and the lock rules."
sources:
  - apps/server/internal/calendar
  - apps/calendar/frontend
  - openapi/calendar.yaml
---
The Calendar module is one view of every time-bound thing Vantigo knows about one
person — the follow-ups Customers holds for them, their absences in Staff, their tasks in
Projects, their hours in Time, their trips in Expenses — beside the **appointments** they
schedule themselves, with colleagues on them. It is a vertical-slice module inside the
single Vantigo binary (`apps/server/internal/calendar`), owns the `calendar` schema in
the shared PostgreSQL database, and serves `openapi/calendar.yaml` under
`/api/v1/calendar`.

It depends on nobody but identity: it reads users through `contracts.UserDirectory` and
the business day through `contracts.BusinessCalendar`, which identity always provides,
and — when they are wired — a customer's name through `contracts.CustomerDirectory` and a
project's code and name through `contracts.ProjectDirectory`, for the courtesy link an
appointment made from a record carries. `MODULES=calendar` alone is a valid installation:
a calendar of appointments and public holidays. It provides no single-provider contract.
What it shows from the other modules it never copies: it consumes the many-provider slot
`CalendarSources`, through which each enabled module **projects** its own rows at read
time, under its own permission, so a follow-up ticked in Customers is ticked on the
calendar with nothing to synchronise. The one thing the module owns is the appointment.

This page grows with the module. What is here today: the [schema](#the-schema), the
[permissions](#permissions), the [sources contract](#the-sources-contract) and its
[kinds](#the-kinds), the [appointment](#the-appointment) — its
[time](#instants-or-dates), its [zone](#the-zone-it-was-made-in), its
[visibility and transparency](#shared-or-private-busy-or-free), its
[iCalendar identity](#the-icalendar-identity), the [record it was made
from](#made-from-a-record), its [attendees](#attendees-and-their-answers) and
[what a change does](#changing-and-deleting) — [recurrence](#recurrence) and its
[exceptions](#occurrences-and-exceptions), [the window](#the-window),
[a colleague's calendar](#a-colleagues-calendar), [the dashboard's
agenda](#the-dashboards-agenda), [how a refusal looks](#refusals-and-revisions), the
[locks](#locks) and the [endpoints](#endpoints).

## The schema

One migration, `00055_calendar_baseline.sql`, creates the `calendar` schema and its three
tables. Every id is a `bigint` identity starting at 1001. Every foreign identifier — the
owner, an attendee, a customer, a project — is opaque: users, customers and projects
live in other schemas and are read through contracts, never through SQL (see [module
boundaries](/en/contributing/module-boundaries/) rule 4). No table has a CHECK
constraint: the module's own validation owns the rules and reports them per field.

- **`appointments`** — one appointment, single or a series: `owner_user_id`, `title`
  (200), `description` (10 000), `location` (200), `all_day`, the all-day pair
  `start_date`/`end_date` (the last day inclusive) or the timed pair
  `starts_at`/`ends_at` (the end exclusive), `time_zone` (the TZID it was made in),
  `visibility` (`shared` or `private`), `transparency` (`busy` or `free`), `rrule` (null
  when single) and `series_last_date` (null when single or unbounded), the opaque
  `customer_id` and `project_id` it was made from, `ical_uid`, `sequence` (from 0),
  `revision` (from 1) and the two stamps. `ux_appointments_ical_uid` keeps the iCalendar
  UID unique; `ix_appointments_owner_window` on `(owner_user_id, starts_at, start_date)`
  serves the window's read of the appointments a person owns.
- **`appointment_attendees`** — the colleagues on an appointment and their answers:
  `appointment_id` (cascading on delete), `user_id`, `status` (`needs-action`,
  `accepted`, `declined` or `tentative`), `responded_at` and `created_at`.
  `ux_appointment_attendees` holds one row per colleague per appointment;
  `ix_appointment_attendees_user` serves the read of the appointments a person attends.
  The owner is never a row here.
- **`appointment_exceptions`** — one occurrence of a series cancelled or moved:
  `appointment_id` (cascading on delete), `occurrence_date` (the occurrence's day in the
  appointment's zone, as the series made it), `cancelled`, the moved pair — `start_date`
  and `end_date` for an all-day series, `starts_at` and `ends_at` for a timed one — and
  the two stamps. `ux_appointment_exceptions` holds at most one exception per occurrence.

The down migration drops the schema with everything in it.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `calendar:access` | Use Calendar | The calendar, one's own appointments and answers, the dashboard's agenda. **Every operation requires it.** | no |
| `calendar:view-colleagues` | See colleagues' calendars | [A colleague's calendar](#a-colleagues-calendar): their appointments as busy or free, with the title when it is shared. | no |

Both are in the category "Calendar" and delegable. The Owner holds them through the
wildcard; anyone else needs a role that grants them. There is no `manage` key: the module
has no settings. A colleague's view is the one thing not everyone should have by
default — an installation that wants every employee to see every other's busy blocks
grants `calendar:view-colleagues` to the employee role. Each operation's rule is in the
contract's `x-vantigo-access`; a caller without it gets the access layer's uniform 403.

`GET /meta` (`getCalendarMeta`) answers `canViewColleagues`, so the app never re-derives
the rule, beside the business `timeZone`, `today` in it, and the
[sources](#the-kinds).

## The sources contract

`contracts.CalendarSource` is a module's view of its own rows as calendar items:

```go
type CalendarSource interface {
    CalendarKinds() []CalendarKind
    CalendarItems(ctx context.Context, q CalendarQuery) ([]CalendarItem, error)
}

type CalendarKind struct {
    Key    string // "projects.task"
    Module string // "projects"
}

type CalendarQuery struct {
    UserID uuid.UUID
    From   time.Time      // the first day, a civil date at midnight UTC, inclusive
    To     time.Time      // the last day, the same form, inclusive
    Zone   *time.Location // the viewer's zone, for a provider that buckets an instant
}

type CalendarItem struct {
    Kind     string // one of the provider's CalendarKinds
    ID       string // stable within the kind: the row's id
    Title    string
    Subtitle string // the customer's name, the project's code — one line, may be empty
    AllDay   bool
    StartDate, EndDate time.Time // all-day: civil dates at midnight UTC, EndDate inclusive
    StartsAt, EndsAt   time.Time // timed: instants, EndsAt exclusive; zero when AllDay
    Done     bool   // a follow-up done, a task done, an entry approved: rendered struck
    Path     string // the SPA route of the record, for the link back
}
```

`CalendarKinds` names the kinds a provider may answer — stable keys,
`"<module>.<noun>"` — so the calendar can list its sources for the filter, the legend and
the colours without a query. `CalendarItems` answers the caller's items in the window.

**The slot.** A module that has time-bound rows about a person sets
`CalendarSource func(Deps) contracts.CalendarSource` on its `module.Module`; `Compose`
and `Workers` collect the filled ones into `Deps.CalendarSources`, in module order, **from
enabled modules only** — the way `Workers` is collected and unlike `CustomerReferences`.
A disabled module's permissions are not in the catalog, so its gate could only fail
closed, and its tables may be empty. The slot is not called `Calendar`: `Deps.Calendar`
is the business calendar identity provides. The module reads the slot through one
accessor in `contractscalls.go` that refuses a call made inside one of its transactions.

**The provider is the gatekeeper.** Each provider applies its own visibility rule to the
principal in `ctx` — Projects' `visible()`, Staff's link, its own permission keys — and
answers nothing, not an error, when the caller lacks its permission. The calendar shows
what comes back and never says what was withheld; re-stating the rule here would be a
copy that drifts. The calendar passes the request's context, since
`contracts.HasPermission` reads the request from it, and never calls a provider inside a
transaction. Each provider is "mine" by construction — a calendar is a person's — so
Time's and Expenses' "everyone's" keys widen nothing here, and Projects' view-all only
widens which of the caller's *own* tasks are visible. No provider touches a table it does
not own.

**A provider that fails fails the request.** `GET /items` answers 503 with a
`CalendarProblem` titled "Calendar source unavailable", with no `code` and naming no
module (which one failed is logged): absent must never also mean "could not be read",
Customer 360's rule.

### The kinds

Five providers answer six kinds, each from a small file in its own module, each with the
module's own query. The table is in module order, the order `sources` lists them in and
the order their items are added to the window. The calendar's own kind,
`calendar.appointment`, is not a provider's and is never listed in `sources`, neither on
`GET /meta` nor on `GET /items`.

| Kind | The rows | The gate | Rendered | `path` |
| --- | --- | --- | --- | --- |
| `customers.follow-up` | Manual, active timeline entries with a follow-up due in the window, done or not, assigned to the caller, on a customer that is neither merged away nor anonymised ([follow-ups](/en/reference/customers/#follow-ups)) | `customers:timeline-view` and `customers:view` | All-day on the due day; done when the follow-up is ticked; the entry's summary (its event type should the summary be blank), the customer's name below | `/customers/{customerId}` |
| `customers.interaction` | The caller's own (`actor_user_id`) manual, active entries of the three interaction kinds (`interaction.call`, `interaction.meeting`, `interaction.email`) with `occurredOn` in the window, on a customer that is neither merged away nor anonymised | The same pair | All-day on `occurredOn`, or a point at `occurredAt` when set (`endsAt` equal to `startsAt`; the view draws its default length); never done; the summary, the customer below | `/customers/{customerId}` |
| `projects.task` | Tasks assigned to the caller with a due date in the window, any status, on a project `visible()` admits — the caller's role, or `projects:view-all` or `projects:manage-all` ([tasks](/en/reference/projects/#tasks)) | `projects:access` | All-day on the due date; done when the status is `done`; the task's title, the project's code below | `/projects/{projectId}/tasks?task={taskId}` |
| `time.entry` | The caller's own entries dated in the window, any status ([the domain model](/en/reference/time/#domain-model)) | `time:access` | A block from `startTime` to `endTime` read as wall-clock times on `entryDate` in the **business** zone when both are set and the end is after the start, otherwise all-day on `entryDate`; titled with the project's name (through `Deps.Projects`), else the task's title, else the note's first line, else `Time`; the hours below (`7.5 h`) when set; done when approved | `/time/day?date={entryDate}` |
| `expenses.claim` | The caller's own travel claims, any status, whose departure to return overlaps the window taken as two instants — the first day's midnight and the midnight after the last day, in the viewer's zone, half-open, so a trip that returns at the first midnight is not in it ([travel claims](/en/reference/expenses/#the-unit-an-expense-belongs-to)) | `expenses:access` | A block over the two instants; done when approved; the purpose, the destination below when it has one | `/expenses/claims/{claimId}` |
| `staff.absence` | The linked person's own absences — requested, approved or registered — overlapping the window ([absence](/en/reference/staff/#absences)) | `staff:access` and a self-service link (no link, no items) | All-day over the first to the last day (an hour type's is one day); never done; titled with the type's name in the **person's own language** — `staff.people.preferred_language`, English when it is `en`, Norwegian bokmål otherwise — not the viewer's; below it, an hour type's hours (`3 h`) and, for a request not yet decided, `Requested` or `Søkt om` in the same language, joined by ` · ` | `/staff/me?tab=absence` |

**Public holidays are not a provider.** The calendar reads the platform package
`internal/holidays` itself — a package every module may import — and answers Norway's
holidays in the window beside the items, as `publicHolidays: [{date, name}]`.

## The appointment

`GET /appointments/{id}` (`getCalendarAppointmentsById`) answers one appointment to its
owner or an attendee:

```
Appointment {
  id, title, description?, location?, allDay,
  startDate?, endDate?,                  // all-day, civil dates
  startsAt?, endsAt?,                    // timed, instants
  timeZone, visibility, transparency,
  recurrence?: { rule, lastDate? },
  customer?: { id, name? }, project?: { id, code?, name? },
  owner: { id, displayName }, attendees: [{ user: {id, displayName}, status, respondedAt? }],
  myStatus?,                             // the caller's own, when an attendee
  exceptions: [{ occurrenceDate, cancelled, startDate?, endDate?, startsAt?, endsAt? }],
  revision, createdAt, updatedAt
}
```

`exceptions` is a series' cancelled and moved occurrences
([exceptions](#occurrences-and-exceptions)) in date order, each the
`AppointmentOccurrence` its `PUT` answered; it is empty on a single appointment.

`POST /appointments` (`postCalendarAppointments`) creates one owned by the caller and
answers 201 with it; `PUT /appointments/{id}` (`putCalendarAppointmentsById`) replaces it
whole. Both take `AppointmentWrite`: the same shape without the server's fields, with
`attendees` as a list of user ids and the record it was made from as `customerId` and
`projectId`. `title` (1 to 200 characters, trimmed), `allDay` and `timeZone` are
required; `description` (10 000) and `location` (200) are trimmed, and a blank one is
stored as none.

### Instants or dates

A **timed** appointment is two UTC instants, `startsAt` and `endsAt` (exclusive), stored
to the whole second; an **all-day** one is two civil dates, `startDate` and `endDate`
(inclusive). Exactly the pair `allDay` names is set: the other pair is a field error (on
`startsAt` for an all-day appointment, on `startDate` for a timed one), and each missing
field of the right pair is a field error of its own. The end may not come before the
start — `endsAt` after `startsAt`, `endDate` on or after `startDate` — and a timed
appointment lasts at most 31 days (31 × 24 hours), an all-day one covers at most 366
days with the first and the last both counted, so `endDate` is at most 365 days after
`startDate`. Both bounds are refused on the end field (`endsAt`, `endDate`).

### The zone it was made in

`timeZone` is the one zone the module stores: the **TZID the appointment was made in**,
the creator's effective zone at creation. The app sends the session's `timeZone` in the
body; the server holds it to the workspace zone's two checks — an empty name or `Local`,
a name the server cannot load, and one the database reads differently from the server,
are refused on `timeZone` ([the business time zone](/en/admin/authentication/)). A
recurring appointment [expands in it](#expansion-in-the-zone): "every Monday 09:00" is
09:00 in that zone across the clock change. A later change of the person's own zone
changes how they *see* the appointment, not what was agreed. On a `PUT`, `timeZone` is
the zone a series expands in from then on, and a different zone on a series is a
[change of when](#changing-and-deleting); on a single appointment, whose instants or
dates mean the same in any zone, a zone-only change is not. The app sends the writer's
session zone only when it creates an appointment; every update — a save in the sheet and
a drag on the grid alike — sends the appointment's own stored zone back.

### Shared or private, busy or free

`visibility` is iCalendar's `CLASS` in the module's own words — `shared` (PUBLIC) or
`private` (PRIVATE) — and `transparency` its `TRANSP` — `busy` (OPAQUE) or `free`
(TRANSPARENT). They are what [a colleague's calendar](#a-colleagues-calendar) is judged
by: a busy appointment blocks the time there, with its title only when it is shared; a
free one is not shown there. The defaults, when a `POST` leaves them out, are `shared`
and `busy`.

### The iCalendar identity

`ical_uid` is `<uuid>@vantigo`, set at creation and never changed, and `sequence` goes
up by one on each `PUT` that changes when (the dates or instants, `allDay`, the rule or
a series' zone) or where (`location`), and on each move, cancel or restore of an
occurrence; nothing else moves it — not an attendee's answer, not a change of the title
or the attendees. Neither is on the wire
yet: they exist from the
first row so the invitations and the feed that come next can use them without a
backfill.

### Made from a record

`customerId` and `projectId` are set when an appointment is made **from a record** — the
**New appointment** action on a customer's Overview or a project's header. They are opaque
ids. The response answers them as `customer: {id, name}` and `project: {id, code, name}`,
the name resolved through `Deps.Directory` and the code and name through `Deps.Projects`
— but each only to a viewer who may read it in its own module: the customer's name to a
holder of `customers:view`, the project's code and name to a holder of
`projects:view-all` or `projects:manage-all`, or of a role on that project (asked of
Projects' own contract). Anyone else — an attendee without those keys included — gets
the bare id, so an appointment cannot be made to name what its viewer may not see. The
id alone is also the answer when the module is off or the id resolves to nothing. An id
that cannot be resolved is never refused: an appointment is not booked on anything, and
the link is a courtesy.

### Attendees and their answers

`attendees` names the colleagues on the appointment: active users in the directory, at
most 50, the owner never among them, each checked before the transaction opens. An id
given twice is kept once, and the limit counts what is left: more than 50 distinct
colleagues is one field error on `attendees`. The owner, and an unknown or inactive
user, are field errors keyed by the element's place in the request (`attendees[3]`) —
except that on a `PUT` a colleague already on the appointment is kept even if they have
become inactive since, so one who left does not lock the owner out of their own
appointment. On a `PUT` the list replaces the old one: a colleague left
out is removed with their answer, a new one starts at `needs-action`, and one who stays
keeps their answer unless [when changed](#changing-and-deleting). The app's attendee
picker searches them with `GET /users?query&limit` (`getCalendarUsers`): active users in
identity's directory, ordered by display name, each `{id, displayName}` — `query`
narrows by display name, and an absent or blank one lists the first page of active
users; `limit` is 1 to 50 and 20 by default, and a limit out of range is a 400 titled
"Invalid query parameters" with the field error on `limit`. Every holder of
`calendar:access` may search, since an id and a display name are what every appointment
already shows its attendees.

An attendee answers with `POST /appointments/{id}/response`
(`postCalendarAppointmentsByIdResponse`), `{status}` of `accepted`, `declined` or
`tentative`; `needs-action` is not something a person sends, and any other value is a
400 titled "Invalid response" with the field error on `status`. The answer is stamped
`respondedAt`, may be changed at any time, changes nothing of the appointment itself —
its `revision`, `sequence` and `updatedAt` stay, so the owner's `If-Match` still holds —
and is the caller's `myStatus` on every read
of the appointment and on each of its items. Anyone who is not an attendee, the owner
included, gets the 404 an unknown id gets. An attendee who declined still sees the
appointment; the app draws it struck. The owner's own status is implicitly accepted and
never stored.

### Changing and deleting

A `PUT` is the owner's alone and carries `If-Match` with the revision it was read at
([revisions](#refusals-and-revisions)). Every `PUT` moves `revision` on, whatever it
changes. **When** is the dates or the instants (the start or the end), `allDay`, the
rule and, for a series, the zone — a single appointment's zone-only change is not one;
**where** is `location`. What a `PUT` changes decides what follows:

- A change of **when** or of **where** bumps `sequence` by one.
- A change of **when** also resets every attendee's answer to `needs-action`, clearing
  `respondedAt`: what they answered is no longer what is asked.
- A change of the **start**, the rule, `allDay` or a series' zone also drops every
  [exception](#occurrences-and-exceptions) of a series, since the days they named may
  no longer exist. A change of the end alone keeps them.
- A change of **where** alone does neither.
- A change of the title, the description, the visibility, the transparency, the
  attendees or the record it was made from does none of these.

`DELETE /appointments/{id}` (`deleteCalendarAppointmentsById`) removes the appointment —
the whole series — with its attendees and exceptions, by the schema's cascade. The
module keeps no cancelled state: nobody outside the company has been told.

## Recurrence

### The subset

`recurrence.rule` is an RFC 5545 `RRULE` value without `DTSTART`, which is the
appointment's start: `FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;COUNT=10`. The subset accepted:

| Part | Accepted |
| --- | --- |
| `FREQ` | `DAILY`, `WEEKLY` or `MONTHLY` |
| `INTERVAL` | 1 to 99; 1 when left out |
| `BYDAY` | for `WEEKLY`, plain weekdays, each at most once (`MO,WE`); for `MONTHLY`, one weekday with an ordinal of 1 to 5 or −1 to −5 (`2TU`, `-1FR`); never for `DAILY` |
| `BYMONTHDAY` | for `MONTHLY` only, one day, 1 to 31 or −1 to −31 counted from the month's end, and not beside `BYDAY` |
| `COUNT` or `UNTIL` | at most one of them: `COUNT` 1 to 730; `UNTIL` a date — or a UTC date-time, whose day is read in the appointment's zone — no earlier than the start's day and at most five years after it; neither for a series that never ends |

A part other than these six — `BYHOUR`, `BYSETPOS`, `BYMONTH`, `WKST` and the rest — is
refused by name; so is a `FREQ` outside the three (`YEARLY`), a part given twice (a
second `BYDAY`), a `DTSTART`, a rule longer than 500 characters (refused before it is
parsed), and a rule that does not produce the appointment's own first day. Each is a
field error on `recurrence.rule`; a refusal repeats at most 60 characters of the part it
names and never the parser's own text. The parts are read case-insensitively and a
leading `RRULE:` is dropped. The subset produces at most one occurrence a day.

**The start is always the first occurrence**, as RFC 5545 has it, and `COUNT` counts it.
`rrule-go` would count the start only when the rule produces it, so a rule that does not
— `FREQ=WEEKLY;BYDAY=MO` on an appointment that starts on a Tuesday,
`FREQ=MONTHLY;BYMONTHDAY=15` on one that starts on the 3rd — is refused: move the start
to a day the rule repeats on.

An `UNTIL` given as a UTC date-time is read in the appointment's zone to find its day,
and since `UNTIL` is inclusive, an instant exactly at midnight there ends the day
before: `UNTIL=20261231T230000Z` on an `Europe/Oslo` series stores `UNTIL=20261231`.

### Canonical form and the last day

The rule is parsed (`teambition/rrule-go`) and **re-serialised** before it is stored, so
the row holds a canonical form and the response answers it as stored: `FREQ`,
`INTERVAL`, `BYDAY`, `BYMONTHDAY`, then `COUNT` or `UNTIL`, in that order; `INTERVAL`
always written; weekdays Monday first; a positive ordinal without its sign; `UNTIL` as a
date (`UNTIL=20261231`). `FREQ=weekly;byday=we,mo` is stored as
`FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE`. `series_last_date` is computed from it at write
time — the last occurrence's day for `COUNT` and `UNTIL`, answered as
`recurrence.lastDate`, and null when the series never ends. An unbounded series costs
nothing: expansion is always [windowed](#the-window).

### Expansion in the zone

A series expands in its `timeZone`. `DTSTART` is `startsAt` in that zone (or `startDate`
at midnight in it); the rule yields wall-clock occurrences, and each occurrence's
instants are that wall-clock time in that zone converted to UTC — so a 09:00 stays 09:00
through March and October, and its UTC instant moves instead. Every occurrence keeps the
appointment's duration.

### Occurrences and exceptions

An occurrence is named by its **date**: the civil day in the appointment's zone the
series put it on, which the subset makes unique. That date is its identity — in an
item's id (`1001:2026-11-03`), in `appointment.occurrenceDate`, in an exception and in
the two occurrence endpoints.

An **exception** is one of two things, never both: **cancelled**, or a **move** to the
pair the series uses — `startsAt` and `endsAt` for a timed series, `startDate` and
`endDate` for an all-day one. A moved occurrence keeps the date it came from as its
identity and shows at its new time; it may move to another day and still answers to the
day it came from.

- `PUT /appointments/{id}/occurrences/{date}`
  (`putCalendarAppointmentsByIdOccurrencesByDate`) creates or replaces the exception for
  `{date}` and answers it as `AppointmentOccurrence`. It is judged in this order: a
  `date` more than ten years from today is a 400 "Invalid occurrence" on `date`, before
  anything is read; then, under the row lock, an appointment the caller does not own is
  the 404; one that does not
  recur is a 400 titled "Invalid occurrence", with no `code` and the field error on
  `date`; a date the rule never produces — judged by expanding the rule to that day — is
  409 `occurrence_not_in_series`; then the body: `cancelled: true`, or a move by the
  series' pair held to the appointment's own rules (the end after the start, 31 days or
  366), never both and never neither — both or neither is a field error on `cancelled`.
- `DELETE /appointments/{id}/occurrences/{date}`
  (`deleteCalendarAppointmentsByIdOccurrencesByDate`) removes it, and the series' own
  occurrence returns. A date with no exception — on any appointment the caller owns,
  recurring or not — is already that, and answers the same 204 with nothing written.
  In the app, the owner's sheet of a series lists its changed occurrences under
  **Repeat**, each with **Put back**, which calls it.

A move, a cancel and a restore are each a change of when: each moves the appointment's
`revision` **and** its `sequence` on. A moved occurrence is shown wherever it moved to,
even before the series' start or after its last day: the window's read admits a series
by its exceptions as well as by its own span. A `PUT` of the appointment that changes
the **start**, the rule, `allDay` or a series' zone **drops every exception**: the days
they named may no longer exist, and a person re-does the one or two that matter. A
change of the end alone, of the title or of the place drops none. The app says so in the
occurrence question, under **The series**, whatever the change. The model is shaped so
the feed can later write the iCalendar names for all of this (`RECURRENCE-ID`, `EXDATE`,
an overriding `VEVENT`) without a migration.

## The window

`GET /items?from&to&timeZone&kinds` (`getCalendarItems`) is the caller's calendar in a
window:

- `from` and `to` are civil dates, inclusive, covering at most **93 days** with both
  counted — `to` at most 92 days after `from` — so the month grid's six weeks and a
  quarter's list both fit, and Staff's calendar has the same limit. `to` before `from`,
  or a longer window, is a 400 titled "Invalid window" with the field error on `to`;
  each must also lie within ten years of today, or it is a field error on itself.
- `timeZone` is the viewer's effective zone, which the app takes from the session; the
  server holds it to the same two checks (a refusal is a field error on `timeZone`) and
  uses the business zone when it is absent.
- `kinds` is an optional comma-separated list of the kinds to ask for
  (`calendar.appointment,projects.task`), so a caller that wants only appointments does
  not pay for five queries. A provider is asked only when one of its kinds is named, and
  the caller's appointments only when `calendar.appointment` is; a name no one declares
  asks nothing, so a list of only such names answers no items. `kinds` narrows `items`
  alone: `sources` still lists every kind and `publicHolidays` is always answered. The
  filter in the app is the client's own and does not use it.

The answer:

```
CalendarItems {
  from, to, timeZone,
  sources: [{ kind, module }],         // every kind a wired provider declares, enabled modules only, in module order; never calendar.appointment
  publicHolidays: [{ date, name }],
  items: [ CalendarItem… ]
}
CalendarItem {
  kind, id, title, subtitle?, allDay, startDate?, endDate?, startsAt?, endsAt?, done,
  path?,                               // a provider's record; absent on an appointment
  appointment?: { id, occurrenceDate?, isOwner, myStatus?, visibility, transparency, recurring }
}
```

The items are built in order. First the caller's appointments — owned, or attended with
any status — whose span overlaps the window: a single one by its instants or dates, a
series when it starts no later than `to` and its last day is unknown or no earlier than
`from`, or when one of its occurrences was moved into the window from anywhere; each
series is expanded in its own zone over the window, with its exceptions
applied (a cancelled occurrence left out, a moved one at its new time). An appointment's
item has no `path` — the app opens the sheet by `appointment.id` — and `done` is always
false. Then each provider's `CalendarItems`, asked with the window and the zone, in
module order. Then the public holidays.

**The edges are judged in `timeZone`.** A timed item is in the window when its instants
overlap `from` 00:00 to the end of `to` in that zone; an all-day one when its dates
overlap `from`..`to`. So an appointment at 23:30 in Oslo lands on the Oslo day for a
viewer in `Europe/Oslo` and on the next day for one in `Asia/Tokyo`.

The list is **sorted by start, all-day first within a day, and never paged**: 93 days of
one person's calendar is hundreds of rows, not thousands, and a grid needs all of them.
The providers are asked **concurrently**, each on the pool since none shares a
transaction, with a budget of **ten seconds** for the lot; a provider that errors, or
has not answered within it, fails the request with the
[503](#refusals-and-revisions) "Calendar source unavailable", whose detail says nothing
is shown rather than a calendar with something missing.

## A colleague's calendar

`GET /colleagues/{userId}/items?from&to&timeZone` (`getCalendarColleaguesByUserIdItems`)
needs `calendar:view-colleagues` beside `calendar:access`, and answers the same window
shape with only `calendar.appointment` items and empty `sources`:

- the colleague's appointments — owned, or attended and **accepted** — with transparency
  `busy`, expanded as `GET /items` expands them, each with `isOwner` false and no
  `myStatus`;
- a `shared` one with its title, and its location as the `subtitle`;
- a `private` one with an **empty `title`**, no `subtitle` and `visibility: private`:
  the server sends no word for it, and the app labels it **Busy** (**Opptatt**) in the
  viewer's language and draws it hatched;
- `free` ones not at all: the point of the view is "when can I book them".

The colleague must be an active user in the directory; anyone else is a 404. The caller's
own id is answered as `GET /items` answers the caller's appointments — every answer,
free ones and private titles included — though still with no provider's items, empty
`sources` and no directory check, so the app need not treat it apart. A window the
request cannot have is the same 400 "Invalid window" as on `GET /items`.

No provider is asked: a colleague's follow-ups, tasks and hours are the other modules'
data behind their own doors, and Staff already shows a colleague's days away on its own
calendar to the audience it chooses. The one audience rule here is the permission:
`calendar:view-colleagues` sees every active colleague this way, with no manager relation
— the calendar has no org chart of its own and does not borrow Staff's, which may be off.
The app's colleague picker lists active users through the same `GET /users` the
attendee picker searches.

## The dashboard's agenda

With the module on and `calendar:access` held, the dashboard opens with a **Today and
this week** card: `GET /items` from today for seven days in the viewer's zone, rendered by
`@vantigo/calendar-ui`'s `AgendaWidget` — today's public holidays and items in order,
all-day first, with their times, each a link to its record (`path`) or to the calendar
page with its appointment's sheet open; the next six days one line each with a count,
which opens to that day's items on a click; a **New appointment** action
(`/calendar?new=1`) and an **Open the calendar** link. A colleague's private item would
read **Busy**, as on the page. It sits above the manager's figures, since it is the one
card every employee has a reason to look at.

The **Needs attention** list keeps its dated items: a follow-up due and a task due stay
there and also appear on the agenda. The list says "this needs you", the agenda says
"when"; no module's `/stats/attention` changed for the calendar.

## Refusals and revisions

Every refusal of the module's own is an RFC 9457 problem, `CalendarProblem`: `title`,
`status`, `detail` and, when a named rule refused, `code` — what a client keys off, never
the words. A 400 carries `errors`, the field errors keyed by the request's camelCase
field, a nested one by its path and a list element by its place: `startsAt`,
`recurrence.rule`, `attendees[0]`. Its `title` says what was refused — "Invalid
appointment", "Invalid occurrence", "Invalid response", "Invalid window" or "Invalid
query parameters" — so a client can tell them apart without reading the field errors. A
request the server cannot decode — a body that is not JSON, a date parameter that is not
a date, a missing `If-Match` — is a bare 400 with neither `code` nor `errors`.

| Code | Status | When |
| --- | --- | --- |
| *(field errors)* | 400 | "Invalid appointment": a body that breaks a rule above — the title, the wrong pair or a missing field, an end before the start, a span too long, a zone either check refuses, a visibility or transparency that is not one, a rule outside the subset, over 500 characters or not producing the start, more than 50 distinct attendees, an attendee who is unknown, inactive or the owner, a description or location too long — or an `If-Match` that holds no revision (the field error on `If-Match`). "Invalid occurrence": an occurrence `PUT` whose `date` is more than ten years from today, or on an appointment that does not recur (both on `date`), or a body that is both or neither of cancelled and moved (on `cancelled`), or a move that breaks the span rules. "Invalid response": a `status` that is not an answer. "Invalid window": `to` before `from`, over 93 days, `from` or `to` more than ten years from today, or a `timeZone` either check refuses. "Invalid query parameters": a `limit` on `GET /users` outside 1 to 50. |
| `revision_mismatch` | 409 | A `PUT /appointments/{id}` whose `If-Match` revision has moved on. The detail names both revisions. |
| `occurrence_not_in_series` | 409 | `PUT /appointments/{id}/occurrences/{date}` for a date the series' rule never produces. |
| — | 503 | `GET /items` when a calendar source failed or ran out of time: "Calendar source unavailable", naming no module. |

**A 404 is bare, with no body**, and says nothing more than "not for you": an unknown
appointment, one the caller neither owns nor attends (a read), one the caller does not
own (a change, a delete, an occurrence), one the caller does not attend (an answer), and
a colleague who is not an active user. The uniform 403 is the access layer's.

`revision` starts at 1 and moves on with every write of the appointment itself: each
`PUT`, and an exception written or removed. An attendee's answer is not one: it moves
neither `revision`, nor `sequence`, nor `updatedAt`, so the owner's `If-Match` still
holds after it. `PUT /appointments/{id}` requires `If-Match` carrying the revision the
appointment was read at — bare (`3`), quoted (`"3"`) or weakly tagged (`W/"3"`) — and
judges it under the appointment's row lock, so two writes at once queue and the second
is told the first won; the app then reloads the calendar and asks the person to open the
appointment again.

## Locks

- **No call out of a transaction.** The calendar sources, the user directory, the
  customer directory and the project directory are reached before a transaction opens,
  never inside one. Every such call goes through one accessor in `contractscalls.go`,
  which refuses a call made inside one of the module's transactions. So a write resolves
  its attendees through the directory **before** it opens its transaction, and a read
  of the window asks the providers with no transaction of the module's open.
- **The appointment's row.** Every write of an appointment — a change, a delete, an
  exception and its removal, an attendee's answer — runs in one transaction that first
  takes the appointment's row `FOR UPDATE` (`withLockedTx`); the revision, the attendees'
  reset, the dropped exceptions and the occurrence's place in the series are judged
  under it. Nothing locks two appointments.

## Endpoints

All under `/api/v1/calendar`.

| Operation | Access |
| --- | --- |
| `GET /meta` | `calendar:access` |
| `GET /items` | `calendar:access`; each kind under its provider's own gate (left out otherwise) |
| `POST /appointments` | `calendar:access`; the caller is the owner |
| `GET /appointments/{id}` | `calendar:access`, then the owner or an attendee (otherwise 404) |
| `PUT /appointments/{id}`, `DELETE /appointments/{id}` | `calendar:access`, then the owner (otherwise 404) |
| `PUT /appointments/{id}/occurrences/{date}`, `DELETE /appointments/{id}/occurrences/{date}` | `calendar:access`, then the owner (otherwise 404) |
| `POST /appointments/{id}/response` | `calendar:access`, then an attendee — not the owner (otherwise 404) |
| `GET /users` | `calendar:access` |
| `GET /colleagues/{userId}/items` | `calendar:access` and `calendar:view-colleagues`; then an active user (otherwise 404) |

Every operation requires `calendar:access`. The request and response shapes are in the
contract, [`openapi/calendar.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/calendar.yaml),
and in the API reference generated from it.
