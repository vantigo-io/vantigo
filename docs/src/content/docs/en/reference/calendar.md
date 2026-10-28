---
title: "Calendar module"
description: "One person's calendar: the appointments the module owns, with recurrence, colleagues, customers' contacts and typed addresses on them and their answers; the invitations mailed to the people outside, the answer by link and the cancellation; the sources contract through which other modules project their rows; the window read in the viewer's zone; a colleague's busy and free; the dashboard's agenda; the calendar feed a phone subscribes to, in a full and a busy variant; the calendars from elsewhere a person subscribes to, fetched by a worker and parsed on read; the Outlook sync — a person's setting, the copies of their appointments and chosen items a worker writes to their Microsoft 365 mailbox through the MailboxCalendar slot, the reconcile, the write-back of their own edits, moves and deletes made in Outlook and of the events they make in the Vantigo calendar, the busy time read back from their Outlook calendar and the clean-up; the CalendarBusy contract the module provides to Planning; the permissions and the lock rules."
sources:
  - apps/server/internal/calendar
  - apps/calendar/frontend
  - openapi/calendar.yaml
  - apps/server/internal/openapi/specs/calendar.yaml
  - apps/server/internal/db/migrations/00055_calendar_baseline.sql
  - apps/server/internal/db/migrations/00056_calendar_invitations.sql
  - apps/server/internal/db/migrations/00057_calendar_feed.sql
  - apps/server/internal/db/migrations/00081_calendar_mailbox_sync.sql
  - apps/server/internal/db/migrations/00083_calendar_mailbox_busy.sql
  - apps/server/internal/db/migrations/00084_calendar_mailbox_writeback.sql
  - apps/server/internal/db/migrations/00086_calendar_mailbox_sync_mailbox.sql
  - apps/server/internal/db/migrations/00087_calendar_mailbox_sync_claim.sql
---
The Calendar module is one view of every time-bound thing Vantigo knows about one
person — the follow-ups Customers holds for them, their absences in Staff, their tasks in
Projects, their hours in Time, their trips in Expenses, their bookings in Planning —
beside the **appointments** they
schedule themselves, with colleagues on them. It is a vertical-slice module inside the
single Vantigo binary (`apps/server/internal/calendar`), owns the `calendar` schema in
the shared PostgreSQL database, and serves `openapi/calendar.yaml` under
`/api/v1/calendar`.

It depends on nobody but identity: it reads users through `contracts.UserDirectory` —
and mails a colleague through its `Notify`, without ever learning their address — and the
business day through `contracts.BusinessCalendar`, which identity always provides, and —
when they are wired — a customer's name, a contact's name and address and a customer's
contacts through `contracts.CustomerDirectory`, and a project's code and name through
`contracts.ProjectDirectory`. It sends the invitations to the people outside the company
through the installation's own mail configuration (`SMTP_FROM`), the way Staff sends its
mails. It fetches the calendars a person subscribes to from the internet through
`internal/netguard`, the platform's guard on outbound addresses. `MODULES=calendar` alone
is a valid installation: a calendar of appointments and public holidays, with colleagues
and typed addresses on them. It provides one single-provider contract,
[`contracts.CalendarBusy`](#the-busy-time-contract): people's busy time, which Planning's
board reads. For the [Outlook sync](#outlook-sync) it also reads, when wired, the
caller's own directory account through `contracts.Workforce` (Staff) and writes and
reads people's mailboxes through `contracts.MailboxCalendar` (Entra).
What it shows from the other modules it never copies: it consumes the many-provider slot
`CalendarSources`, through which each enabled module **projects** its own rows at read
time, under its own permission, so a follow-up ticked in Customers is ticked on the
calendar with nothing to synchronise. The one thing the module owns is the appointment.

Three things go the other way. Each person may have a **calendar address** — the
[feed](#the-feed) — that a phone or another calendar program subscribes to, in a full
and a busy-only variant, behind a token hashed at rest: the module's one anonymous
`GET`. And each person may **subscribe** to calendars from elsewhere —
[subscriptions](#subscriptions) — which a worker fetches and the window shows beside
their own, and which are never in their feed. And each person may have the module keep a
copy of their calendar in their Microsoft 365 mailbox — the [Outlook
sync](#outlook-sync) — which a worker writes through the mailbox provider, which
reads the busy time of their Outlook calendar back, and which takes back into their own
appointments the edits, moves and deletes they make to the copies in Outlook.

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
[locks](#locks) and the [endpoints](#endpoints) — and, for the people outside the
company, the [three kinds of attendee](#three-kinds-of-attendee), the
[invitations](#invitations) mailed to them, [the answer by link](#the-answer-by-link) and
[the cancellation](#cancellation) a delete becomes once they have been told — and [what a
colleague receives](#what-a-colleague-receives) instead; and, beyond the app, [the
feed](#the-feed) — its address, [what each variant carries](#what-the-two-variants-carry),
[the iCalendar object](#the-icalendar-object) and [how it reads the
providers](#reading-the-providers-for-the-feed) — and [subscriptions](#subscriptions), with
[the worker](#the-subscriptions-worker) that fetches them and [how they are
parsed](#parsed-on-read); and [the Outlook sync](#outlook-sync) — [the
contract](#the-contract), [the two tables](#the-two-tables), [the person's
setting](#the-persons-setting), [what is pushed](#what-is-pushed), [the
worker](#the-mailbox-sync-worker), [the write-back](#the-write-back), [the busy
read](#the-busy-read) and [the clean-up](#the-clean-up) — and [the busy time contract](#the-busy-time-contract) the
module provides.

## The schema

Eight migrations: `00055_calendar_baseline.sql` creates the `calendar` schema and its three
tables, `00056_calendar_invitations.sql` adds the attendees from outside the company
and the cancelled state, `00057_calendar_feed.sql` adds the feed addresses and the
subscriptions, `00081_calendar_mailbox_sync.sql` adds the [Outlook
sync's](#the-two-tables) two tables, `00083_calendar_mailbox_busy.sql` adds the
[busy time](#the-busy-read) read back from Outlook and the delta's state, and
`00084_calendar_mailbox_writeback.sql` adds the [write-back's](#the-write-back) columns
— each copy's version and release, and the last change put back, and
`00086_calendar_mailbox_sync_mailbox.sql` indexes `lower(mailbox)` of the rows that are on, for
the [change notifications'](#change-notifications) marks, and
`00087_calendar_mailbox_sync_claim.sql` adds the [worker's](#the-mailbox-sync-worker) claim,
`mailbox_sync.claimed_until`. Every id is a `bigint` identity starting at 1001. Every foreign identifier — the
owner, an attendee, a customer, a project, the holder of an address — is opaque: users, customers and projects
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
  serves the window's read of the appointments a person owns. `cancelled_at` is set when
  a delete became a [cancellation](#cancellation), and null otherwise.
- **`appointment_attendees`** — the people on an appointment and their answers:
  `appointment_id` (cascading on delete), `kind` (`user`, `contact` or `email`), the
  kind's own column — `user_id` (null for the other two), the opaque `contact_id`, or
  `email` (lower-cased) with `name` as typed — `status` (`needs-action`, `accepted`,
  `declined` or `tentative`), `responded_at` and `created_at`; and, for the two outside
  kinds, `rsvp_token_sha256` (the [answer link's](#the-answer-by-link) token, hashed),
  `rsvp_link_ciphertext` (the link, sealed), `invited_at` and `invited_sequence` (the
  last successful send and the `SEQUENCE` it carried) and `send_failed_at` (the last
  failed send since then). One unique index per kind — `ux_appointment_attendees_user`,
  `_contact` and `_email` — holds one row per colleague, contact or address per
  appointment, and `ux_appointment_attendees_rsvp` one row per token;
  `ix_appointment_attendees_user` serves the read of the appointments a person attends.
  The owner is never a row here.
- **`appointment_exceptions`** — one occurrence of a series cancelled or moved:
  `appointment_id` (cascading on delete), `occurrence_date` (the occurrence's day in the
  appointment's zone, as the series made it), `cancelled`, the moved pair — `start_date`
  and `end_date` for an all-day series, `starts_at` and `ends_at` for a timed one — and
  the two stamps. `ux_appointment_exceptions` holds at most one exception per occurrence.
- **`feed_tokens`** — one person's [calendar address](#the-feed) of one variant:
  `user_id`, `variant` (`full` or `busy`), `token_sha256` (the token's hex SHA-256; the
  token itself is never stored), `address_ciphertext` (the whole address, sealed under
  `calendar/feed-address`), `created_at` (the create or the last reset) and
  `last_used_at` (the last request that answered 200 or 304). The primary key
  `(user_id, variant)` holds at most one address per person per variant, and
  `ux_feed_tokens_hash` finds a request's row by its hash and keeps two addresses from
  sharing one.
- **`subscriptions`** — one calendar from elsewhere a person [subscribes
  to](#subscriptions): `user_id`, `name` (100), `url` (2048, as typed, `webcal` and
  `webcals` rewritten to `https`), `colour` (one of the [palette's](#the-rows-and-the-rules-at-write)
  names), `busy_only`, and the worker's record of the last fetch — `etag` (500) and
  `last_modified` (100), the validators sent back as `If-None-Match` and
  `If-Modified-Since`; `body`, the last calendar fetched, at most 1 MiB and kept through
  a failed attempt; `fetched_at`, the last attempt that answered a calendar or a 304;
  `attempted_at`, the last attempt whatever it answered; `next_fetch_at`, when the
  worker fetches next (null is now); and `error` (500), why the last attempt failed,
  null after one that did not — and the two stamps. `ix_subscriptions_user` serves a
  person's list and `ix_subscriptions_due` the worker's due list.
- **`mailbox_sync`** and **`mailbox_events`** — a person's [Outlook sync](#outlook-sync)
  setting with the worker's record of it, and the map of the copies the worker wrote to
  their mailbox ([the two tables](#the-two-tables)).
- **`mailbox_busy`** — the events of a person's Outlook calendar that block their
  time, as the sync worker [read them back](#the-busy-read): times and `show_as`
  alone ([the busy rows](#the-busy-rows)).

The baseline's down migration drops the schema with everything in it; `00057`'s drops
the two tables, so every address stops answering and every subscription is forgotten —
nothing of either was copied anywhere else. `00081`'s drops the Outlook sync's two
tables; the copies already in people's mailboxes stay there, unmapped. `00083`'s drops
`mailbox_busy` and the delta's four columns; the mailbox keeps its events. `00084`'s
drops its five columns: a released copy is then a copy again, which the next push
rewrites whole.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `calendar:access` | Use Calendar | The calendar, one's own appointments and answers, the dashboard's agenda, one's own calendar addresses, subscriptions and Outlook sync. **Every operation requires it**, but the three anonymous ones: the two of [the answer by link](#the-answer-by-link) and [the feed's](#the-feed) `GET`. | no |
| `calendar:view-colleagues` | See colleagues' calendars | [A colleague's calendar](#a-colleagues-calendar): their appointments as busy or free, with the title when it is shared. | no |

Both are in the category "Calendar" and delegable. The Owner holds them through the
wildcard; anyone else needs a role that grants them. There is no `manage` key: the module
has no settings. A colleague's view is the one thing not everyone should have by
default — an installation that wants every employee to see every other's busy blocks
grants `calendar:view-colleagues` to the employee role. Each operation's rule is in the
contract's `x-vantigo-access`; a caller without it gets the access layer's uniform 403.

`GET /meta` (`getCalendarMeta`) answers `canViewColleagues`, so the app never re-derives
the rule, beside the business `timeZone`, `today` in it, the
[sources](#the-kinds) — with `calendar.subscription` among them when the caller has at
least one [subscription](#subscriptions), and `calendar.outlook` last while the caller's
[Outlook sync](#outlook-sync) is on, so the legend has their chips — and
`invitationsAvailable` — whether the installation can mail an
invitation at all ([without mail](#without-mail)).

Inviting a customer's contact is judged by Customers' own keys, not the calendar's: a
contact attendee needs `customers:contacts-view`, the sensitive key that guards a
contact's address, and the [contact picker](#a-customers-contacts) needs it beside
`customers:associations-view`, the pair Customers' own list of a customer's contacts
requires.

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
transaction. The one read with no request behind it is [the feed's](#reading-the-providers-for-the-feed):
once its token has named the person, it asks the providers under `contracts.AsUser`, and
each gate is answered by identity for that person as it would be for their session. The
second is [the Outlook sync worker's](#the-mailbox-sync-worker), where the person's own
standing setting — the row they saved — names the user. Each provider is "mine" by construction — a calendar is a person's — so
Time's and Expenses' "everyone's" keys widen nothing here, and Projects' view-all only
widens which of the caller's *own* tasks are visible. No provider touches a table it does
not own.

**A provider that fails fails the request.** `GET /items` answers 503 with a
`CalendarProblem` titled "Calendar source unavailable", with no `code` and naming no
module (which one failed is logged): absent must never also mean "could not be read",
Customer 360's rule.

### The kinds

Six providers answer seven kinds, each from a small file in its own module, each with the
module's own query. The table is in module order, the order `sources` lists them in and
the order their items are added to the window. The calendar's own kind,
`calendar.appointment`, is not a provider's and is never listed in `sources`, neither on
`GET /meta` nor on `GET /items`. Its other kind, `calendar.subscription` — the events of a
[calendar subscribed to](#parsed-on-read) — is listed in `GET /meta`'s `sources`, with
module `calendar`, when the caller has at least one subscription, and never in
`GET /items`'. Its third, `calendar.outlook` — the [busy time read back from the
caller's Outlook calendar](#the-busy-read) — is listed in `GET /meta`'s `sources`, with
module `calendar`, last, while the caller's Outlook sync is on (`enabled`), and never in
`GET /items`'. Its items have no title and carry `outlook: {showAs}` — `busy`,
`tentative`, `oof` or `workingElsewhere` — which the app labels **Busy in Outlook**,
**Tentative in Outlook**, **Out of office** or **Working elsewhere** (**Opptatt i
Outlook**, **Foreløpig i Outlook**, **Ikke til stede**, **Arbeider et annet sted**) and
draws hatched, in a colour of its own; their id is the first 16 hex digits of the
SHA-256 of the event's id in the mailbox, which never reaches the browser. The person's
own Outlook titles are not shown ([why](#the-persons-own-outlook-titles)).

| Kind | The rows | The gate | Rendered | `path` |
| --- | --- | --- | --- | --- |
| `customers.follow-up` | Manual, active timeline entries with a follow-up due in the window, done or not, assigned to the caller, on a customer that is neither merged away nor anonymised ([follow-ups](/en/reference/customers/#follow-ups)) | `customers:timeline-view` and `customers:view` | All-day on the due day; done when the follow-up is ticked; the entry's summary (its event type should the summary be blank), the customer's name below | `/customers/{customerId}` |
| `customers.interaction` | The caller's own (`actor_user_id`) manual, active entries of the three interaction kinds (`interaction.call`, `interaction.meeting`, `interaction.email`) with `occurredOn` in the window, on a customer that is neither merged away nor anonymised | The same pair | All-day on `occurredOn`, or a point at `occurredAt` when set (`endsAt` equal to `startsAt`; the view draws its default length); never done; the summary, the customer below | `/customers/{customerId}` |
| `projects.task` | Tasks assigned to the caller with a due date in the window, any status, on a project `visible()` admits — the caller's role, or `projects:view-all` or `projects:manage-all` ([tasks](/en/reference/projects/#tasks)) | `projects:access` | All-day on the due date; done when the status is `done`; the task's title, the project's code below | `/projects/{projectId}/tasks?task={taskId}` |
| `time.entry` | The caller's own entries dated in the window, any status ([the domain model](/en/reference/time/#domain-model)) | `time:access` | A block from `startTime` to `endTime` read as wall-clock times on `entryDate` in the **business** zone when both are set and the end is after the start, otherwise all-day on `entryDate`; titled with the project's name (through `Deps.Projects`), else the task's title, else the note's first line, else `Time`; the hours below (`7.5 h`) when set; done when approved | `/time/day?date={entryDate}` |
| `expenses.claim` | The caller's own travel claims, any status, whose departure to return overlaps the window taken as two instants — the first day's midnight and the midnight after the last day, in the viewer's zone, half-open, so a trip that returns at the first midnight is not in it ([travel claims](/en/reference/expenses/#the-unit-an-expense-belongs-to)) | `expenses:access` | A block over the two instants; done when approved; the purpose, the destination below when it has one | `/expenses/claims/{claimId}` |
| `staff.absence` | The linked person's own absences — requested, approved or registered — overlapping the window ([absence](/en/reference/staff/#absences)) | `staff:access` and a self-service link (no link, no items) | All-day over the first to the last day (an hour type's is one day); never done; titled with the type's name in the **person's own language** — `staff.people.preferred_language`, English when it is `en`, Norwegian bokmål otherwise — not the viewer's; below it, an hour type's hours (`3 h`) and, for a request not yet decided, `Requested` or `Søkt om` in the same language, joined by ` · ` | `/staff/me?tab=absence` |
| `planning.allocation` | The linked person's own **confirmed** allocations overlapping the window — never a tentative row, never a colleague's ([My planning](/en/reference/planning/#my-planning), [the calendar source](/en/reference/planning/#the-calendar-source)) | `planning:access` and a person through Staff's `Self` (no person, no items) | All-day over the first to the last day; never done; titled with the project's code and name (`project N` when the directory does not resolve it), the load below as typed (`50 %`, `22.5 h`) | `/planning/me` |

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
  owner: { id, displayName }, attendees: [AppointmentAttendee…],
  myStatus?,                             // the caller's own, when an attendee
  exceptions: [{ occurrenceDate, cancelled, startDate?, endDate?, startsAt?, endsAt? }],
  cancelledAt?,                          // set once a delete became a cancellation
  revision, createdAt, updatedAt
}
```

`attendees` is described under [attendees and their answers](#attendees-and-their-answers);
`cancelledAt` under [cancellation](#cancellation).

`exceptions` is a series' cancelled and moved occurrences
([exceptions](#occurrences-and-exceptions)) in date order, each the
`AppointmentOccurrence` its `PUT` answered; it is empty on a single appointment.

`POST /appointments` (`postCalendarAppointments`) creates one owned by the caller and
answers 201 with it; `PUT /appointments/{id}` (`putCalendarAppointmentsById`) replaces it
whole. Both take `AppointmentWrite`: the same shape without the server's fields, with
`attendees` as a list of `AttendeeWrite` objects ([the kinds](#three-kinds-of-attendee))
and the record it was made from as `customerId` and `projectId`. `title` (1 to 200 characters, trimmed), `allDay` and `timeZone` are
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
occurrence, and on a [cancellation](#cancellation); nothing else moves it — not an
attendee's answer, not a change of the title or the attendees. Neither is on the API's
wire: they are the `UID` and the `SEQUENCE` of every [invitation](#the-vevent) the module
mails, so a mail client files an update or a cancellation over the invitation it already
holds.

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

#### Three kinds of attendee

An appointment's `attendees` are people of three **kinds**, on one table:

| Kind | Who | What the row holds | Answered as |
| --- | --- | --- | --- |
| `user` | a colleague: a user in identity's directory | `user_id` | `user: {id, displayName}` |
| `contact` | a customer's contact, a row of Customers | the opaque `contact_id` alone | `contact: {id, name}` — the name read through the customer directory; **never the address** |
| `email` | an address typed in | `email`, lower-cased, and `name` as typed (it may be empty) | `email`, `name` |

On the wire, a write names each attendee by its kind, and a read answers each with its
answer and, for the two outside kinds, its invitation:

```
AttendeeWrite { kind: user|contact|email, userId?, contactId?, email?, name? }
AppointmentAttendee {
  id, kind,
  user?: { id, displayName },        // user
  contact?: { id, name? },           // contact — the name from the directory, the address never
  email?, name?,                     // email — as typed
  status, respondedAt?,
  invitation?: { sentAt?, sequence?, failedAt? }   // contact and email only
}
```

A **contact** is read through `Deps.Directory.Contact` **each time it is needed** — when
an invitation is sent and when the appointment is read — and never copied: a corrected
address is used by the next mail, and a renamed contact shows renamed. The address is
used for the mail and is on no answer of the appointment, whose readers may not hold the
key that guards it in Customers. The contact's **name** is answered to anyone who may read
the appointment — its owner and its attendees — as an attendee's display name is. A contact Customers has anonymised, or no longer has,
answers nothing: the attendee is answered as `contact: {id}` with no name — the app
shows **A contact** — and no mail is sent to it. The row holds an id and nothing of the
contact's, so Customers' anonymisation needs nothing from this module, and it registers
no `CustomerPersonalData` holder. An **email** attendee is the address as
`net/mail.ParseAddress` reads it (RFC 5322), lower-cased, with the name as typed.

`invitation` is the record of what was mailed ([invitations](#invitations)): `sentAt`
the last successful send, `sequence` the `SEQUENCE` that send carried, and `failedAt`
the last failed send since `sentAt` — cleared by the next success. An outside attendee
with none of the three has never been sent anything: the mail driver is not `smtp`
([without mail](#without-mail)), or nothing has been sent yet. A colleague has no
`invitation`; they are told through identity
([what a colleague receives](#what-a-colleague-receives)).

#### The rules at write

Each attendee is checked before the transaction opens, and a refusal is a field error
keyed by the element's place in the request (`attendees[3]`):

- an element names the field of its kind — `userId` for a `user`, `contactId` for a
  `contact`, `email` (and, if it likes, `name`) for an `email`;
- the owner is never among them;
- a `user` is an active user in the directory — except that on a `PUT` a colleague
  already on the appointment is kept even if they have become inactive since, so one
  who left does not lock the owner out of their own appointment;
- a `contact` is one the customer directory answers, **with an address** — the point is
  to mail them — and one the writer may name: the writer holds
  `customers:contacts-view`, the sensitive key that guards a contact's address. A
  contact already on the appointment is kept by a `PUT` without these checks, whatever
  the directory now says, so an erased contact does not lock the owner out of their
  appointment: it shows no name, and its next send is marked failed;
- an `email` is a **bare** address `net/mail.ParseAddress` reads back unchanged — not
  `Per <per@example.com>`, whose name goes in `name` — of at most 254 characters, and
  its `name` at most 200; a `user` or `contact` field beside it is refused.

Two elements naming the same colleague, the same contact or the same address (compared
lower-cased) are kept once, and a typed address equal to the address of a contact in
the same list is collapsed into the contact, so nobody is invited twice. The limit
counts what is left: at most **50** attendees of all kinds together, and more is one
field error on `attendees`.

On a `PUT` the list replaces the old one. An attendee still listed is **kept**, with
their answer, their link and their invitation record — unless [when
changed](#changing-and-deleting), which resets every answer — and a kept email
attendee's `name`, when the request changes it, is stored; one left out is
**dropped**, with their answer, and is told ([a `CANCEL`](#when-it-goes) to an outside
one who had been invited, [a notice](#what-a-colleague-receives) to a colleague); a new one is **added** at
`needs-action`. A new outside attendee gets its [answer link](#the-answer-by-link) as
its row is inserted.

#### Answering

A colleague answers in the app with `POST /appointments/{id}/response`
(`postCalendarAppointmentsByIdResponse`), `{status}` of `accepted`, `declined` or
`tentative`; `needs-action` is not something a person sends, and any other value is a
400 titled "Invalid response" with the field error on `status`. The answer is stamped
`respondedAt`, may be changed at any time, changes nothing of the appointment itself —
its `revision`, `sequence` and `updatedAt` stay, so the owner's `If-Match` still holds —
and is the caller's `myStatus` on every read
of the appointment and on each of its items. Anyone who is not a colleague on the
appointment, the owner included, gets the 404 an unknown id gets. An attendee who
declined still sees the appointment; the app draws it struck. The owner's own status is
implicitly accepted and never stored, and the owner never answers for anyone.

A contact or an address answers through the link in their invitation
([the answer by link](#the-answer-by-link)), which sets the same `status` and
`respondedAt`.

#### The colleague picker

The app's colleague picker searches with `GET /users?query&limit` (`getCalendarUsers`):
active users in identity's directory, ordered by display name, each `{id, displayName}` —
`query` narrows by display name, and an absent or blank one lists the first page of
active users; `limit` is 1 to 50 and 20 by default, and a limit out of range is a 400
titled "Invalid query parameters" with the field error on `limit`. Every holder of
`calendar:access` may search, since an id and a display name are what every appointment
already shows its attendees.

#### A customer's contacts

The contact picker reads `GET /contacts?customerId=` (`getCalendarContacts`): the
contacts of that customer **that have an address of their own**, ordered by last name,
then first name, each `{id, name, email}`, through `contracts.CustomerDirectory.ContactsForCustomer`, which
Customers answers over its own list of a customer's contacts
([`ContactsForCustomer`](/en/reference/customers/#contactsforcustomer--a-customers-contacts)).
It needs, beside `calendar:access`, the pair Customers' own list requires —
`customers:contacts-view` and `customers:associations-view` — judged in the module, and
answers 403 without them; a missing `customerId` is a 400, and a customer the directory
does not know a bare 404. The address is on this answer, and on no appointment, because
its caller holds the sensitive key that guards it; it is the contact's own address, the
one the invitation is later sent to, never the one a contact may have for a single
customer. The app shows the picker only on an appointment made from a customer, and
only when this answers.

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

A `PUT` also decides who is mailed: [when it goes](#when-it-goes) lists, for each of
these, the invitations the outside attendees are sent, and [what a colleague
receives](#what-a-colleague-receives) the notices.

`DELETE /appointments/{id}` (`deleteCalendarAppointmentsById`) removes the appointment —
the whole series — with its attendees and exceptions, by the schema's cascade, **as long
as nobody outside the company has been sent an invitation to it**. Once someone has, the
delete becomes a [cancellation](#cancellation): the row is kept, cancelled, and they are
told.

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
occurrence question, under **The series**, whatever the change. An invitation writes all
of this in iCalendar's own names — an `EXDATE` per cancelled occurrence and an
overriding `VEVENT` with a `RECURRENCE-ID` per moved one ([a series](#a-series)) — and a
move, a cancel or a restore mails the whole series again to every outside attendee.

## Invitations

An attendee from outside the company — a contact or a typed address — is told of an
appointment by mail: one message per attendee, an iMIP invitation (RFC 6047, carrying an
RFC 5546 `REQUEST` or `CANCEL`), in the layout Gmail, Outlook and Apple Mail key on for
their own answer buttons, with a [web link](#the-answer-by-link) to answer by that lands
the answer in Vantigo. A colleague is never sent one: they are told through identity
([what a colleague receives](#what-a-colleague-receives)).

### The message

The ICS is written with `arran4/golang-ical` and the message is sent through
`mail.Outbound`, whose `Calendar` part (`mail.CalendarPart{Method, Body}`) the mail
package adds as the **third alternative**, after the text and the HTML, and attaches
again as a file — `invite.ics` for a `REQUEST`, `cancel.ics` for a `CANCEL`:

```
multipart/mixed
├─ multipart/alternative
│  ├─ text/plain
│  ├─ text/html
│  └─ text/calendar; method=REQUEST; charset=UTF-8
└─ application/ics; name="invite.ics"
```

The part's `method=` is the ICS's `METHOD` (`REQUEST`, or `CANCEL`). The inline
`text/calendar` alternative is what the clients key on: an invitation sent only as an
attachment shows in Outlook as a plain mail with no buttons.

**From and `ORGANIZER` are the same address**, the installation's `SMTP_FROM`, with the
organiser's display name as the mail's display name and as the `ORGANIZER`'s `CN` — the
one arrangement no client refuses. The owner's own address never crosses from identity,
and would not be the sender anyway: the installation sends, on the owner's behalf. So
the native buttons' reply (`METHOD:REPLY`) goes to `SMTP_FROM`'s mailbox, where nobody
reads it — Vantigo has no inbound mail route — and the mail says so: it asks for the
answer by the link.

The **subject** is `<prefix>: <title> · <day>`, the day the appointment starts on in its
zone (`Tue 3 Nov`, `tir. 3. nov.`). The prefix is `Invitation` (`Invitasjon`) on the
first `REQUEST` to that attendee — one never sent anything (`invited_at` null) — `Updated`
(`Oppdatert`) on any later `REQUEST` to one already invited, a resend included, and
`Cancelled` (`Avlyst`) on a `CANCEL`. The **text and HTML bodies** open with what
happened — "Kari Nordmann invites you to an appointment.", "… has changed an appointment
you are invited to.", "… has cancelled this appointment.", or to a dropped attendee
"… has removed you from this appointment." — then the title; **When**, in the
appointment's zone, named, and "repeating" for a series (its first occurrence);
**Where**; **Organiser**; the description; on a `REQUEST` the sentence *"Answer with the
link below so the organiser sees it in Vantigo:"* and the link; and last *"Vantigo sends
this on behalf of Kari Nordmann. A reply to this mail is not read."* A `CANCEL` carries
no answer sentence. The texts are the module's own, in `words.go`.

### The VEVENT

| Property | Value |
| --- | --- |
| `UID` | the row's `ical_uid` ([the iCalendar identity](#the-icalendar-identity)) |
| `SEQUENCE` | the row's `sequence` |
| `DTSTAMP` | now |
| `DTSTART`, `DTEND` | a single timed appointment: the two instants in UTC; a timed series: wall-clock in its zone with `TZID=<timeZone>` ([a series](#a-series)); an all-day appointment: `VALUE=DATE`, the end exclusive (the day after the last) |
| `SUMMARY`, `LOCATION` | the title, the location |
| `DESCRIPTION` | the description; on a `REQUEST`, then a blank line, the sentence asking for the answer by the link, and the link |
| `URL` | the [answer link](#the-answer-by-link) |
| `STATUS` | `CONFIRMED`, or `CANCELLED` on a cancellation |
| `CLASS` | from `visibility`: `PUBLIC` for `shared`, `PRIVATE` for `private` |
| `TRANSP` | from `transparency`: `OPAQUE` for `busy`, `TRANSPARENT` for `free` |
| `ORGANIZER` | `ORGANIZER;CN=<the owner's display name>:mailto:<SMTP_FROM>` |
| `ATTENDEE` | `ATTENDEE;CN=<name>;ROLE=REQ-PARTICIPANT;PARTSTAT=<status>;RSVP=TRUE:mailto:<address>`, one per **outside** attendee |

**Every outside attendee is on every copy**, as the clients expect, and **no colleague
is**: a colleague's address is identity's and is not put in a file that leaves the
company. `SUMMARY`, `LOCATION` and `DESCRIPTION` are written with their carriage returns
normalised and control characters stripped, so a title cannot break the object's lines.
`PARTSTAT` is the attendee's answer in iCalendar's words (`NEEDS-ACTION`,
`ACCEPTED`, `DECLINED`, `TENTATIVE`). The one exception is the `CANCEL` to an attendee
dropped from the appointment, which names that one alone.

#### A series

A series carries its `RRULE` — the stored rule, in its [canonical
form](#canonical-form-and-the-last-day) — and:

- an **`EXDATE`** per cancelled occurrence: the occurrence's start as the series would
  have made it in its zone;
- one further **`VEVENT`** per moved occurrence, with the same `UID`, `RECURRENCE-ID`
  the occurrence's original start, and the new `DTSTART` and `DTEND`.

A single timed appointment's instants are written in UTC, which is exact. A **timed
series** is not: with an `RRULE`, a UTC `DTSTART` repeats at a fixed UTC time, and a
weekly 09:00 Oslo series would show at 08:00 or 10:00 on the invitee's calendar across
the clock change. So a timed series writes its `DTSTART`, `DTEND`, `EXDATE`s and
`RECURRENCE-ID`s as local wall-clock with `TZID=<the appointment's zone>`, an `UNTIL` in
its rule rewritten as a UTC instant, and the calendar carries one **`VTIMEZONE`** for that
zone, walked from Go's tzdata: one `STANDARD` or `DAYLIGHT` component per transition over
the series' span — three years from its start for a series that never ends, after which
the last offset holds — each with `DTSTART`, `TZOFFSETFROM`, `TZOFFSETTO` and `TZNAME`,
the form every client, Outlook desktop included, reads. An all-day series is dates and
needs none of it.

Clients learn of an exception only from the whole series, so a move, a cancel or a
restore of one occurrence mails the whole series again.

### The language

The bodies and the subject are in the **organiser's language**, read from
`contracts.UserEntry`'s `PreferredLanguage`: Norwegian bokmål when the owner's preference
is `nb`, and English otherwise — including when they chose none (**Automatic** in the
profile). The ICS's properties are the appointment's own words and are not translated,
but for the answer sentence in `DESCRIPTION`. The
[answer page](#the-answer-by-link) is in the same language, since the invitee read the
mail in it.

### When it goes

After the commit, never inside it — through the module's own accessor for the mail seam,
which refuses a call made inside one of its transactions — one message per outside
attendee, one after the other, on a context detached from the request, so a caller who
leaves does not stop the sends. **All the after-commit work of one request** — its
invitations and its colleague notices together — shares one budget of **thirty
seconds**; when it runs out, the attendees not yet reached are marked failed. A change's
`CANCEL`s go before its `REQUEST`s:

| On | To whom | Method, `SEQUENCE` |
| --- | --- | --- |
| a create | every outside attendee | `REQUEST`, the row's sequence |
| a `PUT` that bumped `sequence` (when or where changed) | every outside attendee, kept or new | `REQUEST`, the new sequence |
| a `PUT` that did not | the outside attendees **added** | `REQUEST`, the current sequence |
| a `PUT` | each outside attendee **dropped** who had been invited (`invited_at` set) — unless their address is still on the appointment under another kind | `CANCEL` to that one, the current sequence, with that one as the only `ATTENDEE` |
| an occurrence moved, cancelled or put back | every outside attendee | `REQUEST`, the new sequence — the whole series |
| the appointment [cancelled](#cancellation) | every outside attendee who had been invited (`invited_at` set); one never reached is sent nothing | `CANCEL`, the new sequence |
| `POST /appointments/{id}/attendees/{attendeeId}/resend` | that one attendee | `REQUEST`, the current sequence |

So a change of the title, the description or the visibility alone mails nobody: the
clients would file it, but `SEQUENCE` has not moved, and the next change of when or
where carries it.

### Failure and Resend

A send that succeeds writes `invited_at` and `invited_sequence` — answered as
`invitation.sentAt` and `invitation.sequence` — and clears `send_failed_at`; a `CANCEL`
on a cancellation is recorded the same way. A send that fails writes `send_failed_at`
(`invitation.failedAt`) and is logged as a warning, `calendar: an invitation could not
be sent`, with `appointment_id`, `attendee_id`, `method` and the error, the address in it
replaced with `<recipient>`; the appointment is saved all the same. An attendee whose
contact has no address, or whose contact the directory no longer answers, is skipped,
logged at info (`calendar: invitation not sent: the contact has no address the directory
answers`) and marked the same way. An attendee whose sealed link cannot be opened —
after a change of `APP_SECRET` — fails as a send does. A `CANCEL` to a dropped attendee
is not recorded: their row is gone. **Nothing is queued and nothing retries by
itself**: Communications' outbox is the module for mail that must arrive, and an
invitation the owner can see failed, and send again, is enough here.

`POST /appointments/{id}/attendees/{attendeeId}/resend`
(`postCalendarAppointmentsByIdAttendeesByAttendeeIdResend`) is the owner's **Resend**:
the `REQUEST` again, with the current `SEQUENCE` and the **same link** as before
([the link](#the-token-and-the-link)), to one contact or address on the appointment
(`attendeeId` is the attendee's `id`), marked as any send is. It answers 200 with the
appointment, the attendee's `invitation` as the send left it: a failed send is
`failedAt`, not an error of the request. It is judged in this order: a 404 for an
appointment the caller does not own, an attendee not on it, or a colleague, who is never
mailed an invitation; then a 409 `appointment_cancelled` on a cancelled appointment;
then a 503 `invitations_unavailable` when the mail driver is not `smtp`.

### Without mail

When the installation's mail driver is not `smtp`, outside attendees are stored all the
same, with their links, and nothing is sent and nothing marked: each stays with no
`invitation.sentAt` and no `failedAt`, the log says `calendar: invitations not sent: the
mail driver is not smtp` at info, and `GET /meta` answers `invitationsAvailable: false`,
so the sheet shows every outside attendee as **Not sent** and says why. Switching the driver to `smtp` later sends nothing
by itself; the next change of when or where, or a **Resend**, does.

### The link's base

The answer link is `<base>/rsvp?token=<token>`. `<base>` is `PUBLIC_BASE_URL` when it is
set — the public host, where the page is reachable from the internet — and otherwise
`APP_URL` with `APP_BASE_PATH`, where the page is sessionless all the same and reachable
by whoever can reach the installation. Unlike the Invoices pay link, which exists only
with a public host, an invitation does not require one: an invitee on the company's own
network needs none, and an operator who invites people outside it sets
`PUBLIC_BASE_URL` ([Calendar administration](/en/admin/calendar/)). The link is sealed
when the attendee is added, so a change of `PUBLIC_BASE_URL` reaches only the attendees
added after it.

## The answer by link

An outside attendee answers through a web page in Vantigo, on the Invoices pay link's
pattern: a bearer token in a link, read by an anonymous page that posts it.

### The token and the link

Each outside attendee gets a token as its row is inserted: 32 random bytes,
base64url without padding. The row keeps only its hex SHA-256, in
`rsvp_token_sha256`, for the lookup — **never the token** — and the whole link sealed by
the secrets box under the purpose `calendar/rsvp-link`, in `rsvp_link_ciphertext`, so
every later mail — an update, a resend — carries the **same** link, and a client that
filed the first mail answers through it as well as through the last. The token
travels in the query string, which Vantigo's request log never writes; the page reads
it once, keeps it in `sessionStorage`, clears it from the address bar and **posts** it —
it is never in an API path.

### The two operations

The answer page (`<base>/rsvp`, a public route of the app) calls two operations, both
**anonymous** — no session is needed, and a signed-in caller is answered exactly as
anyone else — each rate limited per client address in a bucket of its own, counted
before anything is read, so an unknown token counts too:

| Operation | Access | Policy | Limit |
| --- | --- | --- | --- |
| `POST /calendar/rsvp/read` `{token}` (`postCalendarRsvpRead`) → `RsvpPage` | `anonymous` | `calendar-rsvp-read` | 60 per 10 minutes |
| `POST /calendar/rsvp` `{token, status}` (`postCalendarRsvp`) → `RsvpPage` | `anonymous` | `calendar-rsvp` | 30 per 10 minutes |

Over a limit is a 429 in the limiter's shape everywhere in Vantigo —
`{"error": {"code": "rate_limited", …}}` with `Retry-After`. Behind a reverse proxy the
client address is the proxy's unless `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` are
set, and every invitee then shares one bucket.

The token is hashed and looked up. **A token nobody holds is a bare 404**: one never
issued, one whose attendee was dropped from the appointment, and one whose appointment
was deleted or erased after its cancellation.

```
RsvpPage {
  title, allDay, startsAt?, endsAt?, startDate?, endDate?, timeZone, location?,
  organiser: { name }, company?, attendee: { name },
  status, cancelled, language
}
```

The page answers what the invitee needs to decide, and nothing more: **no description**
(it may be internal), **no other attendee**, no address of the owner's. `company` is
**reserved and never answered**: no contract the module consumes exposes the
workspace's name, so the page names the organiser alone; `attendee.name` is the
contact's name or the name typed — for an email attendee typed without one, the
address's part before the `@` — never the address; `status` the attendee's
current answer; `cancelled` whether the appointment has been
[cancelled](#cancellation); `language` the organiser's ([the
language](#the-language)), which the page is shown in rather than the browser's. A timed
appointment carries `startsAt` and `endsAt`, an all-day one `startDate` and `endDate`
(inclusive); a series answers its first occurrence. On both operations a body that
does not decode, or whose token is absent or blank, is a 400, and on the answer so is a
`status` that is not an answer.

The answer takes `status` — `accepted`, `tentative` or `declined` — sets the attendee's
`status` and `respondedAt`, under the appointment's row lock, and answers the page again.
It changes nothing of the appointment itself: neither its `revision`, its `sequence` nor
its `updatedAt`. An answer to a cancelled appointment is a 409
`appointment_cancelled`. The answer may be changed through the same link at any time
until then. An answer that repeats the status the attendee already holds writes
nothing — `respondedAt` stays — and tells nobody. Otherwise, after the commit, the owner
is told through identity, within ten seconds — "Ola Hansen accepted: Kundemøte" ([what
a colleague receives](#what-a-colleague-receives)).

**The public host.** `GET /rsvp`, `POST /api/v1/calendar/rsvp/read` and
`POST /api/v1/calendar/rsvp` are on `security.PayRoutes()`, the public host's allowlist
([the public host](/en/admin/payments/#the-public-host)), and nothing else of the
module's API is; the host app's list of public and sessionless paths has `/rsvp`, so
the page asks for no session. The page's same-origin `POST`s pass the platform's
cross-origin protection, and a cross-site one is refused 403.

## Cancellation

`DELETE /appointments/{id}` takes one of two paths, judged under the appointment's row
lock:

- **No outside attendee has ever been sent a `REQUEST`** — `invited_at` is null on every
  contact and address row, or there are none: the row is removed, with its attendees and
  exceptions, as it always was, and each colleague on it is told it was cancelled, so
  none sees it vanish untold.
- **Otherwise the appointment is cancelled**: `cancelled_at` is set and `sequence` bumped
  under the row's lock; after the commit a `CANCEL` with the new `SEQUENCE` and
  `STATUS:CANCELLED` is mailed to every outside attendee who had been invited
  (`invited_at` set) — one never reached is sent nothing, since there is nothing on
  their calendar to take off — and recorded on each as a send is ([when it
  goes](#when-it-goes)), and the colleagues are told ([what a colleague
  receives](#what-a-colleague-receives)).

The delete answers 204 on either path.

A cancelled appointment **stays**, so the `CANCEL` can name what it cancels and a link
already mailed still says so. It is read-only:

- `GET /appointments/{id}` answers it to the owner and the attendees as before, with
  `cancelledAt`; the app opens it with a banner and nothing to change.
- **Every write is a 409 `appointment_cancelled`** — a `PUT`, a `DELETE`, an occurrence's
  `PUT` or `DELETE`, a colleague's answer, a **Resend** and an answer by the link.
- It is absent from `GET /items`, from a colleague's calendar and so from the dashboard's
  agenda.
- A cancelled series keeps its exceptions as they were, so its `CANCEL` names the same
  occurrences the last `REQUEST` did.

### The cancellations worker

The **`calendar-cancellations`** worker erases a cancelled appointment once its last day
has passed — with its attendees and exceptions, by the cascade — so the module does not
keep what nobody can see. The last day is:

- for a single appointment, its end;
- for a bounded series, the later of `series_last_date` and the end of its latest moved
  occurrence, since a move may take an occurrence past the series' last day;
- for a series that never ends, ninety days after `cancelled_at`.

It is contributed through `Workers` behind `CALENDAR_CANCELLATIONS_ENABLED` (default
on), runs every `CALENDAR_WORKERS_POLL` (default an hour, at least a minute), and holds
an advisory lease, `0x43414C43414E4331` ("CALCANC1"), so several replicas take turns.
Each cycle erases at most 200 appointments; what is left is erased at the next. It has
nothing else to do. Once an appointment is erased, its attendees' links are tokens
nobody holds, and the answer page says the link is not valid.

## What a colleague receives

A colleague on an appointment is mailed through identity, so their address never crosses
into the calendar: `contracts.UserDirectory.Notify`.

```go
// Notice is a plain mail to one user, composed by a module in the languages it has,
// delivered by identity in the user's own, and never answering the address.
type Notice struct {
    Texts map[string]NoticeText // keyed "en", "nb"
}
type NoticeText struct{ Subject, Body string }

Notify(ctx context.Context, userID uuid.UUID, n Notice) error
```

Identity picks the user's preferred language, then `en`, then any, and sends a text
mail from `SMTP_FROM` through the installation's mail driver, the way it sends its own
invitations — under `MAIL_DRIVER=log` the notice is written to the log, as identity's own
mail is. A user who is not active (as `PermissionHolders` means it), an unknown one and
one whose only address is a directory's placeholder are not mailed, and that is not an
error; nor is an installation with no mail driver — the notice is simply not sent. A
send that fails is an error naming the user's id and no address, which the calendar
logs and goes on.

The calendar calls it after the commit, never inside a transaction, composing each notice
in English and Norwegian (`words.go`, `notices.go`):

| When | To whom | Subject (English · Norwegian) |
| --- | --- | --- |
| a create, and any `PUT` that adds them | each colleague added | "Kari Nordmann invited you: Kundemøte, Tue 3 Nov 09:00" · "Kari Nordmann inviterte deg: Kundemøte, tir. 3. nov. 09:00" |
| a `PUT` that bumped `sequence` | each colleague kept | "Kari Nordmann changed: …" · "Kari Nordmann endret: …" |
| a [cancellation](#cancellation), and a delete that removed the row | every colleague on it | "Kari Nordmann cancelled: …" · "Kari Nordmann avlyste: …" |
| a `PUT` that drops them | each colleague dropped | "Kari Nordmann removed you: …" · "Kari Nordmann fjernet deg: …" |
| an outside attendee's answer by the link | the owner | "Ola Hansen accepted: Kundemøte", "… declined: …", "… tentatively accepted: …" · "Ola Hansen takket ja: Kundemøte", "… takket nei: …", "… svarte kanskje: …" |

The time is the appointment's start in its own zone, the day alone for an all-day one;
the answer's notice names the invitee as the appointment holds them — a contact
with no name as "a contact" ("en kontakt"), never by any part of its address; an email
attendee typed without a name by the part of the address before the `@`. A move, cancel
or restore of one occurrence sends no notice. The body is the same sentence and
"Open it in Vantigo: <link>" ("Åpne den i Vantigo: …"), the link into the app
`APP_URL/calendar?date=<the first day>&appointment=<id>`, and nothing else: the
appointment is on the colleague's calendar already, and their answer is the in-app one.
A colleague gets no ICS and no native buttons, by design. A notice that fails is logged
as a warning, `calendar: a notice could not be sent`, with `appointment_id` and
`user_id`, and never fails the request. The notices of a write share its thirty-second
budget with its invitations ([when it goes](#when-it-goes)); the owner's notice of an
answer has ten seconds of its own.

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
  not pay for six queries. A provider is asked only when one of its kinds is named, and
  the caller's appointments only when `calendar.appointment` is; a name no one declares
  asks nothing, so a list of only such names answers no items. The caller's
  [subscriptions](#parsed-on-read) are read only when `calendar.subscription` is named,
  or `kinds` is absent, and their [Outlook busy time](#the-busy-read) only when
  `calendar.outlook` is, or `kinds` is absent. `kinds` narrows `items`
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
  path?,                               // a provider's record; absent on an appointment and a subscription's item
  appointment?: { id, occurrenceDate?, isOwner, myStatus?, visibility, transparency, recurring },
  subscription?: { id, name, colour, busyOnly }   // kind calendar.subscription only
  outlook?: { showAs }                            // kind calendar.outlook only; title empty
}
```

The items are built in order. First the caller's appointments — owned, or attended with
any status, and not [cancelled](#cancellation) — whose span overlaps the window: a single one by its instants or dates, a
series when it starts no later than `to` and its last day is unknown or no earlier than
`from`, or when one of its occurrences was moved into the window from anywhere; each
series is expanded in its own zone over the window, with its exceptions
applied (a cancelled occurrence left out, a moved one at its new time). An appointment's
item has no `path` — the app opens the sheet by `appointment.id` — and `done` is always
false. Then each provider's `CalendarItems`, asked with the window and the zone, in
module order. Then the events of the caller's [subscriptions](#parsed-on-read), as last
fetched. Then, while the caller's Outlook sync is on, their [Outlook busy
rows](#the-busy-rows) overlapping the window — a timed one by its instants, an all-day
one by its dates — as `calendar.outlook` items, whatever their `show_as`. Then the
public holidays.

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
shape with only `calendar.appointment` and `calendar.outlook` items and empty `sources`:

- the colleague's appointments — owned, or attended and **accepted**, and not
  cancelled — with transparency `busy`, expanded as `GET /items` expands them, each with `isOwner` false and no
  `myStatus`;
- a `shared` one with its title, and its location as the `subtitle`;
- a `private` one with an **empty `title`**, no `subtitle` and `visibility: private`:
  the server sends no word for it, and the app labels it **Busy** (**Opptatt**) in the
  viewer's language and draws it hatched;
- `free` ones not at all: the point of the view is "when can I book them";
- while the colleague's [Outlook sync](#outlook-sync) is on, the [busy
  rows](#the-busy-rows) it read back from their Outlook calendar whose `show_as` is
  `busy` or `oof`, as `calendar.outlook` items with no title — tentative and working
  elsewhere are not busy, as a tentative answer to an appointment is not shown here.
  The app labels them as on one's own calendar, and gives them a chip of their own in
  the legend when there are any.

The colleague must be an active user in the directory; anyone else is a 404. The caller's
own id is answered as `GET /items` answers the caller's appointments — every answer,
free ones and private titles included — though still with no provider's items, no
Outlook busy rows, empty `sources` and no directory check, so the app need not treat it
apart. A window the
request cannot have is the same 400 "Invalid window" as on `GET /items`.

No provider is asked, and no subscription is read: a colleague's follow-ups, tasks,
hours and bookings are the other modules' data behind their own doors, the calendars they subscribe to
are theirs to read, and Staff already shows a colleague's days away on its own
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

## The feed

A **calendar address** is a person's calendar as an iCalendar file at one URL, for a
phone or another calendar program to subscribe to. It is read-only by nature: the
program fetches it on its own schedule, and nothing is ever written back.

### The address and its two variants

A person has at most one address per **variant** ([`feed_tokens`](#the-schema)):

- **`full`** — their calendar as they see it;
- **`busy`** — its busy spans and nothing else, for a receptionist's or a partner's
  calendar to subscribe to ([what each carries](#what-the-two-variants-carry)).

The address is `<base>/api/v1/calendar/feed/<token>.ics`. The token is the pay link's and
the answer link's: 32 random bytes, base64url without padding, 43 characters. The row
keeps its hex SHA-256, by which a request is matched, and **never the token**; and it
keeps the whole address **sealed** by the secrets box under the purpose
`calendar/feed-address`, opened for its owner's own screen alone, so the screen can show
it again — a person sets up their phone today and their laptop next month, and a secret
they had to reset to see is one they would paste into a note. `<base>` is
`PUBLIC_BASE_URL` when it is set, and `APP_URL` with `APP_BASE_PATH` otherwise —
[the answer link's rule](#the-links-base): a phone off the company's network needs the
public host, a laptop on it does not. The address is sealed whole when it is created, so
a change of `PUBLIC_BASE_URL` reaches the addresses created or reset after it.

Three operations manage the addresses, all `calendar:access`'s, each on the caller's own
rows alone:

- `GET /feed` (`getCalendarFeed`) answers both variants' addresses, for those that exist:

  ```
  FeedAddresses { public, addresses: [FeedAddress…] }   // full before busy; a variant without one absent
  FeedAddress   { variant, address, webcalAddress, createdAt, lastUsedAt? }
  ```

  `public` says whether `PUBLIC_BASE_URL` is set now, so the base is reachable from the
  internet, or not, so it is `APP_URL`, which may answer on the company's network only —
  it is read from the configuration on each request, not from the address, so an address
  created on `APP_URL` before `PUBLIC_BASE_URL` was set still points there while `public`
  is `true`; `webcalAddress` is the same address with `webcal://` in place of `https://`
  (or `http://`), which Apple's and Outlook's programs subscribe to in one click — the
  server serves `https` alone; `createdAt` is the create or the last reset, and
  `lastUsedAt` the last request that answered 200 or 304, cleared by a reset. An address
  whose sealed copy cannot be opened — `APP_SECRET` rotated under it — is logged at
  warning and left out, as if the variant had none.
- `POST /feed/{file}` (`postCalendarFeedByVariant`), `file` the variant, **creates** the
  address, or **resets** the one that exists: a new token, its hash written over the old
  one — so the old address answers 404 from that moment — and the new address sealed. It
  answers 200 with the `FeedAddress`.
- `DELETE /feed/{file}` (`deleteCalendarFeedByVariant`) **turns off** that variant's
  address and answers 204; the address answers 404 from that moment, and the other
  variant's is untouched. A variant the caller has no address of is 404.

**One path for the three.** The two writes take the variant in the segment the feed's
`GET` takes the token in — all three are `/api/v1/calendar/feed/{file}` — since OpenAPI
refuses two paths that differ only in a parameter's name. A `file` that is neither
`full` nor `busy` is the bare 404 on both writes.

### The one anonymous GET

`GET /feed/{file}` (`getCalendarFeedByFile`), `file` the token with the `.ics` suffix, is
the feed itself, and the module's one anonymous `GET`. The pay page's and the answer
page's anonymous operations take their token in a `POST` body, so the token never sits in
an API address or a proxy log; a calendar program allows nothing else — Google, Apple, Outlook and
Thunderbird take one URL and `GET` it, with no header and no body of ours. It is held to
these mitigations:

- **The token is hashed at rest** and found by its hash; a read of the database answers
  no address.
- **It is per person and per variant, and resettable** in one click; a reset kills the
  old address at once, and nothing but the calendar is behind it.
- **It is rate limited per client address**: the policy `calendar-feed`, **120 requests
  per 10 minutes**, counted before anything is read — a phone asks hourly, Google a few
  times a day, and an office of phones behind one NAT is dozens an hour. Over it is the
  limiter's 429 `rate_limited`, with `Retry-After`. Behind a reverse proxy the client
  address is the proxy's unless `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` are set.
- **A token nobody holds is the bare 404**, with no body — one never issued, one reset,
  one turned off — and so is a `file` without the `.ics` suffix.
- **A token whose person may not act is the same bare 404.** Once the token is matched,
  the feed asks identity, through `Access.UserHolds`, whether its person may act at all
  and holds `calendar:access` — before anything else is read. A person who is disabled,
  locked out, pending, deactivated by the directory while SCIM is configured (unless an
  Owner), or who no longer holds `calendar:access`, gets the 404 an unknown token gets,
  so the answer never says the token was right, and `last_used_at` is not written. The
  address is a session's stand-in and outlives nothing a session would not: a colleague
  who leaves, or loses the permission, stops reading at once, with no reset needed. The
  row is not removed, so the same address answers again once the person may act and
  holds the permission again. A failure to ask is an error, never a 404.
- **The feed carries nothing the person's own calendar does not**, and the busy variant
  nothing but spans, so a leaked full address reads that one calendar until it is reset,
  and a leaked busy address the shape of the person's days.
- **The path is logged, the query is not**, as for every request, and the token is the
  path's last segment: the operator keeps the proxy's access log on the public host to
  the path, or to nothing ([Calendar administration](/en/admin/calendar/#the-feed-on-the-public-host)).

The platform router matches a parameter to a whole segment and nothing else, so
`{token}.ics` cannot be a pattern: the handler takes the segment and requires the suffix.
The route is on `security.PayRoutes()`, the public host's allowlist, as
`GET /api/v1/calendar/feed/` with exactly one segment after it — not under `HEAD` or
`POST`, not with a segment more — and no page goes with it: the address is pasted into a
calendar program, not opened in a browser. A signed-in caller is answered alike, by the
token and not the session, and its person's right to act is asked as above. A request
that answers 200 or 304 writes `last_used_at`.

### What the two variants carry

The window is **today −3 months to today +12 months**, today in the business zone
(`contracts.BusinessCalendar`), its edges judged as [the window](#the-window) judges them
with the business zone as the viewer's. A series is written as a rule, so the window does
not bound a series: it decides which series are in. The items are the person's own as
`GET /items` builds them — the appointments they own or are on, and the enabled
providers' items under each provider's own gate ([below](#reading-the-providers-for-the-feed)) —
and **never** a colleague's appointment, a cancelled one, a subscription's event or a
public holiday: every phone has a holiday calendar, and a second copy is noise.

| In the feed | `full` | `busy` |
| --- | --- | --- |
| An appointment the person owns, or is on with any answer but `declined` | its `VEVENT`: title, place, description, class, transparency, link, and a series' rule and exceptions | a `VEVENT` of its time alone, when its transparency is `busy`; a `free` one is left out |
| An appointment the person declined | left out: they said no | left out |
| A [cancelled](#cancellation) appointment | left out | left out |
| A provider's **timed** item (hours with a start and an end, a trip) | a `VEVENT` with the title, the subtitle as `DESCRIPTION`, the module in `CATEGORIES` and the link in `URL`; a done one with `✓ ` before its title, since no client draws a strike | a `VEVENT` of its time alone, under an [opaque `UID`](#the-icalendar-object) |
| A provider's **point** — a timed item whose end is not after its start, such as a customer interaction logged at a time | the same, with no `DTEND` | left out: a point is not a span of busy time |
| A provider's **all-day** item (a follow-up, a task due, an absence, a booking) | the same, as dates (`VALUE=DATE`) | left out: a day with a task on it is not a day the person is busy |
| A [subscription's](#subscriptions) event | never | never |
| A public holiday | never | never |

The busy variant's `VEVENT` is `UID`, `DTSTAMP`, `SEQUENCE`, `DTSTART`, `DTEND`, a
`SUMMARY` of the one word *Busy* — *Opptatt* when the person's preferred language, read through the user
directory, is `nb` — and `TRANSP:OPAQUE`; for a series, its `RRULE`, its `EXDATE`s and its
overriding `VEVENT`s with the same `UID`, since a moved occurrence's time is a span too.
No title, no place, no description, no category, no link, no class, and no `UID` that
names the module or the row: nothing a reader could tell one block from another by.

Neither variant writes `ORGANIZER` or `ATTENDEE`: the copy on a phone is read-only, and
the appointment's sheet is where its owner reads who is on it. The `URL` is the app's own
page: `APP_URL/calendar?date=<the first day>&appointment=<id>` for an appointment, as
[a colleague's notice](#what-a-colleague-receives) carries it, and `APP_URL` with the
provider's `path` for a projected item.

**Size.** Fifteen months of one person's calendar is hundreds of `VEVENT`s; a heavy Time
user's entries are the one thing that could reach the 1 MB a Google Calendar import
takes. So the projected items are ordered by their distance from today, nearest first,
and when the serialised calendar exceeds **1 MiB** the farthest quarter of them is
dropped and the calendar serialised again, until it fits. The appointments are never
dropped; a drop is logged at info with the person's id. The feed is not paged and has no
other limit.

### The iCalendar object

The calendar is written with `arran4/golang-ical` through the writers of
[the invitations' `VEVENT`](#the-vevent) (`ics.go`), generalised where the feed needs
them and never copied:

| Property | Value |
| --- | --- |
| `PRODID`, `VERSION` | the invitations' `PRODID`; `2.0` |
| `METHOD` | none: a published calendar has none, and one makes Outlook treat the file as an invitation |
| `NAME`, `X-WR-CALNAME` | *Vantigo*; for the busy variant *Vantigo (busy)*, or *Vantigo (opptatt)* in `nb` |
| `REFRESH-INTERVAL;VALUE=DURATION`, `X-PUBLISHED-TTL` | `PT1H`: the two spellings of an hour's refresh that the clients honouring one read, Apple's the second |
| `VTIMEZONE` | one per `TZID` used, each over the span of the series written in it ([a series](#a-series)), before the first `VEVENT` |
| `DTSTART`, `DTEND` | a single timed appointment: UTC instants; a timed series: wall-clock with its zone's `TZID`; a timed projected item: UTC instants, and no `DTEND` for a point in time (its end its start); an all-day item: dates, the end exclusive |
| `DTSTAMP` | the request's time, on every `VEVENT` |
| `UID`, `SEQUENCE` | an appointment's `ical_uid` and `sequence`, in both variants — random already; a projected item's `<kind>:<id>@vantigo` and `0` in the full variant, and in the busy one an **opaque** `UID` and `0`: the first 32 hex digits of a MAC, under the secrets box purpose `calendar/feed-busy-uid`, of the person's id and the item's kind and id, followed by `@vantigo`. Every `UID` is stable across requests, so a client updates rather than duplicates; the busy one is different for two people, so two busy feeds cannot be joined on it, and a rotated `APP_SECRET` changes it, so a program then sees the projected blocks anew |

The answer is 200 with `Content-Type: text/calendar; charset=utf-8`,
`Content-Disposition: inline; filename="vantigo.ics"`, `Cache-Control: private, no-cache`
— a program may keep the calendar, and must ask again — and an **`ETag`**: the quoted hex
SHA-256 of the serialised calendar with every `DTSTAMP` line removed, so an unchanged
calendar keeps its tag from one hour to the next while `DTSTAMP` still says when it was
written. An `If-None-Match` naming that tag, or `*`, is **304** with the same `ETag` and
`Cache-Control` and no body — after the token was found and `last_used_at` written. There
is no `Last-Modified`: a provider's items have no one time they changed at, and a tag that
is right is better than a date that is a guess. A provider that fails, or runs out of
time, makes the feed the [503](#refusals-and-revisions) "Calendar source unavailable", as
it makes `GET /items`: a calendar with something missing is never served.

### Reading the providers for the feed

A provider's gate is `contracts.HasPermission(ctx, Access, key)`, which asks
`Access.Check` about the session of the request in `ctx`. The feed's request has no
session — its caller is a calendar program holding a token — so the providers would
answer it nothing; and skipping their gates would show a person their tasks on their
phone after `projects:access` was taken from them. The gate stays the provider's, and
identity still answers it, through one seam of the contracts in two parts:

```go
// Access
// UserHolds reports whether userID — active, not locked out — holds every key:
// an owner holds all, anyone else each through an effective role. It is for a
// read made on a user's behalf where there is no request to check: the
// calendar feed. Any failure is an error, never a false.
UserHolds(ctx context.Context, userID uuid.UUID, keys ...string) (bool, error)

// AsUser is the context of a read on one user's behalf with no request behind
// it: a Principal of that id alone — no session, no roles — and the mark
// HasPermission reads.
func AsUser(ctx context.Context, userID uuid.UUID) context.Context
```

`HasPermissions` (and so `HasPermission`) has two rules, in order: a request in `ctx` is
checked through `Check`, as the router checks it, whatever else `ctx` carries; with no
request but the `AsUser` mark, the keys are asked of `UserHolds` for that user, in one
question. Anything else is false, and so is any failure. Identity answers `UserHolds`
in two steps. First, whether the person **may act at all**, through one query,
`UserMayAct`, which applies the session query's (`GetSessionByTokenHash`) person
predicates — not disabled; not pending, invited and never signed in, so nothing can have
been handed a token on their behalf; and while SCIM is configured not deactivated by the
directory, unless an Owner — and lockout besides, as a session's permission check
applies it. The two queries must agree, so a change to one is a change to the other: a
token outlives nothing a session would not. Then the Owner role holding everything, and
`EffectivePermissionForUser` per key. The rule that holds an Owner to a second factor is
not applied: it is a rule about a session, and there is none. A user who does not exist
may not act and holds nothing, and naming no key is false. The feed asks it once for the
token's person and `calendar:access` ([above](#the-one-anonymous-get)), and the
[subscriptions worker](#the-subscriptions-worker) once per person per cycle.

Once the token has named the person, the feed asks the providers under
`AsUser(ctx, userID)`, on a context that does not carry the anonymous request — whose
session `Check` would judge first, and find none — through the same accessor and under
the same ten-second budget `GET /items` uses, with the person's id as the query's
`UserID`, so the module's own check that the principal is the person the query names
holds as it does there. A provider is asked exactly as it would be for the person's own
session, and answers nothing once they no longer hold its key. A module uses `AsUser`
only for a read whose caller is a token it has already matched to that user, never to
widen a request ([module boundaries](/en/contributing/module-boundaries/) rule 12) —
or for a person's own standing setting: the [Outlook sync worker](#the-mailbox-sync-worker)
asks the providers, and Staff for the person's directory account, under `AsUser` for
the user whose `calendar.mailbox_sync` row it is syncing, once `UserHolds` has said they
still hold `calendar:access`.

## Subscriptions

A person may subscribe to calendars from elsewhere — a school's, a club's, the secret
address of their own Google calendar — which the module fetches, keeps as last fetched
and shows beside their own calendar.

### The rows and the rules at write

Five operations, all `calendar:access`'s, all on the caller's own rows — another
person's subscription is the bare 404 an unknown id gets:

| Operation | Does |
| --- | --- |
| `GET /subscriptions` (`getCalendarSubscriptions`) | the caller's subscriptions, by name |
| `POST /subscriptions` (`postCalendarSubscriptions`) | subscribes, fetches once, and answers 201 with the row |
| `PUT /subscriptions/{id}` (`putCalendarSubscriptionsById`) | replaces one whole and answers 200 with the row |
| `DELETE /subscriptions/{id}` (`deleteCalendarSubscriptionsById`) | removes one, 204; its events are gone from the next read |
| `POST /subscriptions/{id}/refresh` (`postCalendarSubscriptionsByIdRefresh`) | fetches one now — the screen's **Refresh now** — and answers 200 with the row as the fetch left it |

```
CalendarSubscription {
  id, name, url, colour, busyOnly,
  fetchedAt?, attemptedAt?, error?,   // the record of the last fetch
  createdAt, updatedAt
}
CalendarSubscriptionWrite { name, url, colour, busyOnly }
```

The rules at write, each a field error: `name` 1 to 100 characters; `url` at most 2048
characters, absolute, with a scheme of `https`, `http`, `webcal` or `webcals` — the last
two stored as `https`, since they are the same thing in a program's address bar — a host
that is not empty and not an IP literal [the guard](#the-fetch) refuses, and no user
name or password, which would be written to the row and the log; `colour` one of the
palette's twelve names — `blue`, `cyan`, `teal`, `green`, `lime`, `yellow`, `orange`,
`red`, `pink`, `grape`, `violet`, `indigo`, each a `--vantigo-calendar-<name>` variable in
the app, so a subscription's events follow the colour scheme as a module's items do;
`busyOnly` a boolean. The **eleventh** subscription is 409 `subscription_limit`: ten
calendars beside one's own is more than anyone reads, and each is a fetch every half
hour. The count and the insert are one transaction under the person's own lock
([locks](#locks)), so two saves at nine cannot both be the tenth. There is no `revision` and no `If-Match`: the row is the person's own and small,
and the last write wins.

**The inline fetch.** A `POST` fetches the address **once before it answers**, under a
budget of twenty seconds and through [the same fetch](#the-fetch) as the worker, so a
wrong address is seen at once and a right one shows its events at the next load rather
than after the worker's next cycle. The row is created whatever the fetch answers, with
`error` set when it failed: a failed fetch is not an error of the request. A `PUT` that
changes `url` forgets every fetch of the old one — `etag`, `last_modified`, `body`,
`error` and the timestamps — and fetches inline too; one that changes only the rest fetches nothing.
`POST /refresh` fetches under the same budget, and so does a save, whether or not the
worker is on.

**One rate limit for the three.** `POST /subscriptions`, `PUT /subscriptions/{id}` —
whether or not it changes `url`, since the limiter counts before the body is read — and
`POST /subscriptions/{id}/refresh` share one bucket, the policy
`calendar-subscription-fetch`, **30 requests per client address per 10 minutes**, counted
before access is checked: each is an outbound request of up to twenty seconds made on a
click, and nobody saving their own calendars clicks thirty times in ten minutes, while a
script turning the server into a fetcher would. Over it is the limiter's 429
`rate_limited`, with `Retry-After`. Behind a reverse proxy the client address is the
proxy's unless `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` are set, as for the feed.

### The subscriptions worker

The **`calendar-subscriptions`** worker keeps the subscriptions fresh. It is contributed
through `Workers` behind `CALENDAR_SUBSCRIPTIONS_ENABLED` (default on), and
`CALENDAR_SUBSCRIPTIONS_POLL` (default `30m`, at least `5m`) is how often each
subscription is fetched. It **wakes every five minutes** — a constant, not a setting —
and fetches every subscription whose `next_fetch_at` is null or past, the never-fetched
first and then the longest due, at most **200** a cycle, one at a time. It takes no lease:
each subscription is **claimed** in one short statement of its own (`FOR UPDATE SKIP
LOCKED`) that puts its `next_fetch_at` **five minutes** ahead — the fetch's 20 seconds
and room for its record — and gives the connection back to the pool before the owner's
access is asked or the address fetched. The claim keeps two replicas from fetching one
subscription, and they share the due ones; the fetch's record then sets when it is due
next, and a fetch cut short is due again once the claim lapses. No connection is held
while a neighbour or the address is asked. After each attempt
— the worker's, a save's or **Refresh now**'s — it sets

```
next_fetch_at = now + max(poll, min(the calendar's interval, 24 hours)) + jitter
```

the calendar's interval being its `REFRESH-INTERVAL` or Apple's `X-PUBLISHED-TTL`, the
longer when it has both, read from a fetch that answered the calendar; a 304 or a failure
has none and waits the poll alone. The jitter is uniform over `[0, poll/10)`, so two
installations subscribed to the same calendar drift apart, and a feed that asks to be read
daily and answers a calendar is read daily; the five-minute tick is the most a
subscription is late. An attempt that fails sets `error`
and `attempted_at` and leaves `body`, `fetched_at` and the validators as they were, so the
calendar keeps showing the last good read and the screen says both when it last worked
and why it does not now. The next attempt is the next one due, with no backoff: a feed
down for an hour costs two requests. Turned off, the worker fetches nothing and every
subscription shows its last read; a save and **Refresh now** still fetch.

**A person who may no longer use Calendar is not fetched for.** The worker asks
`Access.UserHolds` with `calendar:access` — [the feed's rule](#reading-the-providers-for-the-feed):
disabled, locked out, pending, deactivated by the directory while SCIM is configured
(unless an Owner), or without the permission — once per person per cycle, and that
person's due rows are **put off**: nothing is fetched, `next_fetch_at` moves by the poll
plus jitter, and `body`, `error`, `attempted_at`, `fetched_at` and the validators stay as
they were, since nothing was attempted. So a departed person's calendars are neither
fetched for nobody nor read again every tick, and are fetched at their first due time
after the person may act again. A failure to ask ends the cycle with an error; the next
tick takes it up.

#### The fetch

One `GET` through an `http.Client` of the module's own:

- its dialler is `netguard.DialContext` over the system resolver: the host is resolved,
  every address checked against the platform's one table of forbidden ranges — private,
  loopback, link-local, carrier-grade NAT and cloud metadata — and the connection made to
  the address that was checked, not to a fresh lookup;
- `Proxy` is nil, the Peppol lookup's reasoning: a proxy would send the request to an
  address the guard never saw;
- **no redirect is followed** (`CheckRedirect` answers `http.ErrUseLastResponse`): the
  next address is one neither the guard nor the person saw;
- the request carries `User-Agent: Vantigo-Calendar`, `Accept: text/calendar`, and
  `If-None-Match` and `If-Modified-Since` from the row when it has them;
- the budget is **twenty seconds** in all — within it the dial has ten, the TLS handshake
  ten and the response's headers fifteen — the headers are capped at 64 KiB, and the body
  is read through a limit of **1 MiB** plus one byte;
- the transport **keeps no idle connection** (`DisableKeepAlives`): a subscription is
  fetched once every half hour, so a kept connection is never reused in time and only sits
  open to a stranger's server; every fetch dials afresh, through the guard, and closes
  when it is read.

A 2xx with a calendar writes `body`, the new validators (null when it sent none),
`fetched_at` and `attempted_at`; a 304 writes `fetched_at` and `attempted_at` and keeps the
body and the validators; either clears `error`. Every other outcome is a failure, recorded as
one sentence in `error`:

| Outcome | `error` |
| --- | --- |
| a 3xx | "the address redirects; subscribe to the address it points at" |
| a body over 1 MiB — not kept | "the calendar is larger than 1 MiB" |
| a calendar of more than 10,000 `VEVENT`s — not kept, since it would be parsed on every load | "the calendar has more than 10,000 events" |
| a 2xx whose body is not valid UTF-8, carries a NUL, does not begin — after blank space and a byte-order mark — with `BEGIN:VCALENDAR`, or does not parse | "the address did not answer a calendar" |
| any other status | "the address answered <status>", the code and its standard text (`404 Not Found`), never the server's own reason phrase |
| a dial the guard refused | "the address is not one Vantigo may fetch" |
| no answer within the budget, or a dial, a TLS handshake or the headers past their own bound | "the address did not answer in time" |
| any other failure to connect — a name that does not resolve, a connection refused | "the address could not be reached" |

The error is the sentence, never the response's body or the address the host resolved
to. The log carries the subscription's id and the error at warning, and nothing of the
URL beyond its host.

**A fetch overtaken by a change of address records nothing.** Each of the three records
matches the `url` the fetch was made to as well as the row's id, so a fetch still out
when a `PUT` changed the address writes nothing over the new address's row — its
calendar, its validators and its due time would be the old address's; it is logged at
debug, and the row is answered as it now stands. A row deleted while its fetch was out
is the 404 an unknown id gets on `PUT` and on `POST /refresh`; a `POST` whose new row was
deleted from another tab meanwhile answers 201 with the row as it was inserted, and the
worker passes over it.

### Parsed on read

`GET /items` reads the bodies of the caller's subscriptions after the providers' items
and before the public holidays, parses each with golang-ical, and answers every `VEVENT`
that overlaps the window as a `CalendarItem` of **kind `calendar.subscription`**, module
`calendar`:

- `id` is `<subscription id>:<UID>`, with `:<start>` after it for an occurrence of a rule
  or a moved one — `20261103T080000Z`, or the date for an all-day one; an event without a
  `UID` is `event-<its place in the file>`;
- `title` is the event's `SUMMARY`, `subtitle` the subscription's name, and `done` false;
- there is no `path`: the event's `URL` is the other calendar's and is not followed, so a
  click on one opens nothing;
- `subscription: { id, name, colour, busyOnly }` is what the app draws it by;
- a **busy-only** subscription's item has an empty `title`, which the app labels *Busy*,
  as it does a colleague's private appointment.

No description reaches the wire, in either case. The parse rules:

- **Time.** A `DTSTART` with `VALUE=DATE` is all-day; `DTEND` (exclusive) or `DURATION`
  gives its last day, and neither gives one day. A `DTSTART` ending in `Z` is an instant;
  one with a `TZID` is wall-clock in that zone; one with neither is *floating*, read in
  the request's `timeZone`. `DTEND` or `DURATION` gives the end; neither gives a point
  (`endsAt` equal to `startsAt`, drawn at the default length), and so does an end before
  its start.
- **`TZID`**, resolved in order: Go's tzdata by that name; the `X-LIC-LOCATION` of the
  calendar's `VTIMEZONE` of that `TZID` (libical's and Google's hint), when it names a
  zone Go knows; the CLDR `windowsZones` table — its territory-001 entries, about 140, in
  `windowszones.go` — for a Windows name such as `W. Europe Standard Time`, which Outlook
  writes; the `TZOFFSETTO` of that `VTIMEZONE`'s `STANDARD` component as a fixed offset —
  right in winter and an hour off in summer, which is still better than a day off; and
  UTC when there is nothing. Each fallback is logged at debug, once per subscription per
  read.
- **Recurrence.** `RRULE` is expanded with rrule-go over the window widened back by the
  event's own length and two days (its days and two for an all-day one), since an
  occurrence that began before the window may still cover it, and forward by a day,
  `DTSTART` dressed as floating in the resolved zone and each occurrence put back
  into it — [the module's own way](#expansion-in-the-zone), so a 09:00 stays 09:00 across
  the clock change; `EXDATE`s remove occurrences, one written as a date the occurrence on
  that day in the event's zone; a `VEVENT` with a `RECURRENCE-ID`
  replaces the occurrence at that instant of the master with the same `UID`, wherever it
  moved to. `RDATE` is not read. A rule rrule-go refuses makes the event a single one at
  its start; a `VEVENT` without a `DTSTART` that can be read is not shown at all.
- **Bounds.** The body is what a stranger's server answered, and it is parsed on every
  load, so every part of one read is bounded:
  - at most **10,000** `VEVENT`s are read from one body and the rest ignored, logged at
    debug — the fetch stores no calendar with more, so only a body stored before that
    rule meets it;
  - one **budget of 200,000 steps** per subscription per read, shared by every event and
    every rule: an event read, an item offered and a rule's step each spend one, first
    come first served in the calendar's order, so a rule stepping by the second from
    years ago — or two thousand such rules — stops where the budget does, and what comes
    after contributes nothing;
  - a **deadline of one second** per read, looked at before every rule and every 4,096
    steps, since one rule no date satisfies (the 30th of February) can cost rrule-go a
    tenth of a second inside a single step; a read stops too when its request goes away;
  - at most **1,000** occurrences from one rule, and **5,000** items kept from one
    subscription: past that a bounded heap keeps the 5,000 nearest the window's start,
    whatever order the calendar wrote them in, the farthest given up as nearer ones
    come, so memory is bounded by the cap however many items a calendar offers.

  Once a bound is hit the read answers what it has, and logs it at info with the
  subscription's id — the deadline, the spent budget, the rule's cap or the item cap; a
  read stopped because its request went away is logged at debug.
- **Status.** `STATUS:CANCELLED` is left out. `TRANSP` and `CLASS` are not read: a
  subscription is not a colleague, and the person subscribed to it.
- **Text.** `SUMMARY` is unescaped (`\,`, `\;`, `\n`, `\\`) and cut to one line of 200
  characters; an event without one is titled *(untitled)* in the caller's language. The
  busy-only rule overrides both.
- **A body that does not parse** contributes nothing now, and is the fetch's error at the
  next fetch. It is logged at debug with the subscription's id alone, never the parser's
  error, which may quote the body — a stranger's text.

[A colleague's calendar](#a-colleagues-calendar) reads no subscription, as it asks no
provider.

### Never in the person's own feed

[The feed](#what-the-two-variants-carry) is built from the appointments and the
providers, and a subscription is neither; nothing of a subscription is ever reconciled
into the module's tables. Two people subscribed to each other's feeds would otherwise
copy each other's calendars back and forth for ever — and so a subscription's body never
leaves the installation.

## Outlook sync

A person may have the module keep a **copy of their calendar in their Microsoft 365
mailbox**, so their appointments and the items the other modules project show in Outlook
beside everything else, and — in their main calendar — block the time for colleagues
booking meetings there. The calendar is the authority: it decides what the copies are,
from the appointments and the providers, and a **mailbox provider** writes them and
answers what it did. Before each push the worker [takes back](#the-write-back) what the
person changed in Outlook to a copy of **their own appointment** — an edit, a move, a
delete — through the appointment's own write paths, makes an event they created in the
separate *Vantigo* calendar an appointment, and puts back anything else changed in a
copy. After each push that worked it also [reads the busy time](#the-busy-read) of
the person's Outlook calendar back — the times of the events that block it, and nothing
else of them — which their own calendar, a colleague's and Planning's board show.
The person's own Outlook titles are [not shown](#the-persons-own-outlook-titles).

The provider is the single-provider slot `MailboxCalendar` (`contracts.MailboxCalendar`),
which the [Microsoft Entra ID module](/en/reference/entra/#the-mailbox-calendar) fills;
the person's mailbox is their own account in that directory as Staff names it
(`contracts.Workforce.DirectoryAccount`, [Staff](/en/reference/staff/#the-workforce-contract)).
Nothing of a person's mailbox is guessed: a user with no Staff person, a person of another
account kind or an account the provider does not manage is not available, never matched
by address. It is the sixth sanctioned cross-module write ([module
boundaries](/en/contributing/module-boundaries/) rule 14).

### The contract

```go
type MailboxCalendar interface {
    Key() string                                        // "entra", no context, no network
    Status(ctx context.Context) (MailboxStatus, error)  // no network
    EnsureCalendar(ctx context.Context, mailbox, known, name string) (string, error)
    DeleteCalendar(ctx context.Context, mailbox, calendarID string) error
    Apply(ctx context.Context, mailbox, calendarID string, ops []MailboxOp) ([]MailboxResult, error)
    Find(ctx context.Context, mailbox, calendarID string, from time.Time) ([]MailboxEventRef, error)
    Delta(ctx context.Context, mailbox string, from, to time.Time, link string) (MailboxDelta, error)
    Marked(ctx context.Context, mailbox string, eventIDs []string) ([]MailboxEventRef, error)
    Versions(ctx context.Context, mailbox, calendarID string, from, to time.Time) (MailboxVersions, error)
    Events(ctx context.Context, mailbox, calendarID string, eventIDs []string) (MailboxEventsRead, error)
    Watch(ctx context.Context, mailbox string) error
    Unwatch(ctx context.Context, mailbox string) error
    Notified(ctx context.Context, since time.Time) ([]MailboxNotice, error) // no network
}

type MailboxNotice struct { Mailbox string; At time.Time }

type MailboxEventRef struct { EventID, Key string; Meeting bool }

type MailboxDelta struct { Changes []MailboxChange; Link string; Done bool }
type MailboxChange struct {
    EventID string
    Removed, Series, Cancelled, AllDay bool
    StartDate, EndDate time.Time // all-day: civil dates at midnight UTC, EndDate inclusive
    StartsAt, EndsAt   time.Time // timed: instants
    ShowAs string                // one of the ShowAs values, "" for one the contract does not name
}

type MailboxVersions struct { Events []MailboxEventVersion; Complete bool }
type MailboxEventVersion struct { EventID, Key, ChangeKey string; Series bool }
type MailboxEventsRead struct { Events []MailboxEventState; Gone []string }
type MailboxEventState struct {
    EventID, Key, ChangeKey string
    Foreign, Series, Cancelled, Meeting, TextBody bool
    Event MailboxEvent
}
```

- `mailbox` is the account's `ExternalID`; `calendarID` `""` is the mailbox's default
  calendar. `EnsureCalendar` answers the id of a calendar the provider made, carrying
  its marker — `known` while it still exists and carries it, else one named `name`,
  made anew or adopted when it carries the marker; a calendar of the name that is not
  the provider's is never adopted, and the next name is tried (`Vantigo (2)` up to
  `Vantigo (5)`). `DeleteCalendar` deletes a calendar the provider made and every
  event in it; any other is refused `ErrCalendarNotOurs` and left whole, and one
  already gone is done.
- `Watch`, `Unwatch` and `Notified` are the [change notifications](#change-notifications)'
  three calls. `Watch` makes sure the mailbox's changes are notified — nothing while a
  live subscription has more than three days left, a renewal when less, a create when
  none — and answers nil, calling nothing, where the provider offers no notifications on
  this installation or holds creates off. `Unwatch` deletes the subscription, idempotent
  (a mailbox not watched, or a subscription already gone, is nil), and is allowed under
  `cleanup`; a refusal or failure is returned and the delete stays owed. `Notified`
  answers the mailboxes notified after `since`, oldest first, at most 5000
  (`MailboxNoticesMax`), from the provider's own rows with no network; `Mailbox` is as
  the provider holds the account's external id, so the caller compares it
  case-insensitively. The module's answers are in [the Entra
  reference](/en/reference/entra/#change-notifications).
- `Status` answers `MailboxStatus{State, Reason}`: `available` (the provider may write),
  `cleanup` (it may only remove — `Apply` takes deletes alone, and `DeleteCalendar` and
  `Find` work — with the reason `switch_off`) or `unavailable`.
- `MailboxOp{Kind, EventID, Key, Event}`: a `create` names `Key` and `Event`, an
  `update` `EventID` and `Event`, a `delete` `EventID`. `Key` is written on create as the
  event's marker; an update writes it as the marker too when it is set — the same value
  a copy already carries, so an event [taken in](#new-events-in-the-vantigo-calendar)
  from the *Vantigo* calendar is tagged by the push that rewrites it — and `""` leaves
  the marker as it is. The worker's updates always name it.
- `MailboxEvent{Subject, Body, Location; AllDay; StartDate, EndDate; StartsAt, EndsAt;
  TimeZone; ShowAs; Private}`: the all-day pair civil dates with the end inclusive, the
  timed pair instants, `TimeZone` an IANA zone, `ShowAs` one of `free`, `tentative`,
  `busy`, `oof` and `workingElsewhere`.
- `Apply` answers one `MailboxResult{EventID, Gone, ChangeKey, Err}` per op, in order;
  an update or delete of an event that no longer exists is `Gone` — a delete is then
  done, an update the caller's to re-create. A returned error is the whole call failing,
  with no result.
- `Find` answers `{EventID, Key, Meeting}` for the events of the calendar that carry
  this installation's marker, and no other — never an event of the person's own, nor
  one another installation sharing the tenant wrote — starting at or after `from` unless
  it is zero, at most 5000. `Meeting` is an event with attendees — the person turned the
  copy into a meeting — their count alone decoded, never an address; `Marked` leaves it
  false. A secondary calendar that is gone answers no events.
- `Delta` reads the changes to the mailbox's **default calendar** — the one the
  directory computes free/busy from — a page at a time. `link` `""` starts a delta
  afresh over `from..to`, answering every event overlapping the window; `from` and `to`
  are used only then, the window being fixed in the link from that call on. A link the
  provider answered continues it: the changes since, an event deleted or moved out of
  the window answered `Removed` with its id alone. `Done` with `Link` is the delta link
  to keep; without `Done`, `Link` is a next link to continue with. A link that is not
  this mailbox's delta is refused with a failure, so a stored link can point the
  provider nowhere else. `Series` marks an occurrence or an exception of a recurring
  series, which the push never writes. Nothing of what an event is about — subject,
  body, location, organizer, attendees — is in the contract.
- `Marked` answers, among at most 100 ids (`MailboxMarkedMax`), the events carrying
  this installation's marker with their keys, in the order asked; an event of the
  person's, one another installation wrote, or an id no longer there is not answered.
- `Versions` answers the **versions** of the events the [write-back](#the-write-back)
  may have to take back — ids and change keys, nothing of what the events say. With
  `calendarID` `""` it answers the default calendar's events carrying this
  installation's marker that start at or after `from` (`Find`'s rule), each with its
  `Key`; `to` is not used there. With a secondary calendar — the one `EnsureCalendar`
  made — it answers every event of its view overlapping `from..to`, with no `Key`: the
  view is not asked for the marker, the calendar being the provider's whole. At most
  5000 (`MailboxVersionsMax`); `Complete` is false when the list was cut, and a caller
  then infers nothing from an absence. `Series` marks an occurrence, an exception or a
  series master.
- `Events` reads whole, among at most 100 ids (`MailboxMarkedMax`), the events of the
  calendar, in the order asked, each **through the calendar's own path**, and answers
  `MailboxEventsRead`: `Events`, the events read, and `Gone`, the ids the directory
  answered "not found" for in that calendar — deleted, or moved to another calendar or
  folder; never a mailbox gone, which fails the whole call. An id **in neither list**
  was not read this time — an answer that did not decode, a refusal of that one event —
  and the caller infers nothing from it. `Key` is this installation's key, `Foreign` another installation's marker,
  `Series` as above, `Cancelled` a cancelled meeting, `Meeting` an event with attendees
  (their count alone is decoded, never an address), `TextBody` whether the body came
  as text, and `Event` the subject, the body, the location's display name, the times
  (all-day dates at midnight UTC, as `Delta`'s), `ShowAs` and `Private` (a private or
  confidential sensitivity). What an event says crosses the contract here for the
  copies and the provider's own calendar alone, for the write it feeds, and the
  provider never logs a word of it.
- `Delta`, `Marked`, `Versions` and `Events` are refused under `cleanup` with
  `ErrMailboxUnavailable`: a read is never needed to remove copies.
- The refusals are sentinels: `ErrMailboxUnavailable` (not available, or a non-delete in
  cleanup), `ErrMailboxUnknown` (not an account the provider manages),
  `ErrMailboxDenied` (the directory refused the mailbox: outside the provider's
  permission scope), `ErrMailboxGone` (no such user, or no mailbox) and
  `ErrCalendarNotOurs` (`DeleteCalendar` of a calendar the provider did not make) and
  `ErrMailboxDeltaExpired` (`Delta`'s link is no longer taken: start again with `""`).
  Anything else is a failure to retry.

The module calls the provider only through the accessors in `contractscalls.go`
(`mailboxStatus`, `mailboxEnsure`, `mailboxDeleteCalendar`, `mailboxApply`,
`mailboxFind`, `mailboxDelta`, `mailboxMarked`, `mailboxVersions`, `mailboxEvents`, and
`workforceDirectoryAccount` for Staff's read), which refuse a call
made inside one of its transactions. Without a provider the mailbox accessors answer
`ErrMailboxUnavailable` (`Status` answers `unavailable`), and without Staff
`workforceDirectoryAccount` answers no account.

### The two tables

Migration `00081_calendar_mailbox_sync.sql` adds them, and `00083_calendar_mailbox_busy.sql`
gives `mailbox_sync` the delta's four columns and widens the ids 00081 stored —
`mailbox_events.event_id` and `mailbox_sync.calendar_id` — to 512, the provider's bound
on an id; `00084_calendar_mailbox_writeback.sql` adds the [write-back's](#the-write-back)
five columns; `00086_calendar_mailbox_sync_mailbox.sql` adds the index on `lower(mailbox)`
where the sync is on (the [change notifications'](#change-notifications) marks);
`00087_calendar_mailbox_sync_claim.sql` adds `claimed_until` (the
[worker's](#the-mailbox-sync-worker) claim). No CHECK holds a value to its set, the
module's validation does.

- **`mailbox_sync`** — a person's setting and the worker's record of it, one row per
  user who has ever saved it: `user_id` (the primary key), `enabled` (their toggle),
  `target` (`default`, their main calendar, or `own`, a calendar named *Vantigo*),
  `detail` (`full` or `busy`), `kinds` (`text[]`, the kinds chosen), `mailbox` (200, the
  account's external id last confirmed through Staff — kept for the clean-up, since a
  mailbox that changed or was lost still has copies to remove), `calendar_id` (500, the
  *Vantigo* calendar's id once made), `state` (`pending`, `active`, `stopping` — a
  clean-up owed —, `off`, `unavailable` or `failed`), `error` (40, the last refusal's
  code, null after a sync that worked), `next_sync_at` (null is now), `last_synced_at`,
  `attempts` (failures in a row), `reconcile` (the next sync reads the mailbox's list
  first; true by default), `revision` (from 1, the `PUT`'s `If-Match`), `claimed_until`
  (null, or until when a cycle has claimed the row: no other cycle syncs it before) and
  the two stamps. `ix_mailbox_sync_due` on `next_sync_at`, partial on `enabled OR state =
  'stopping'`, serves the worker's due list. The [busy read's](#the-busy-read) state:
  `busy_link` (the provider's next or delta link, opaque and valid for the person's
  mailbox alone), `busy_from` and `busy_to` (the dates the delta was started over) and
  `busy_synced_at` (when a read last reached the delta link). They are written whatever
  the revision, which the read never moves. The [write-back's](#the-write-back) last
  change put back: `put_back_reason` (20 — `conflict`, `refused`, `not_owner`,
  `not_editable` or `many_deleted`), `put_back_key` (200, the item key of the copy it
  was made to; `''` for `many_deleted`) and
  `put_back_at`, also written whatever the revision, and cleared by a clean-up and by
  turning the sync off. Nothing of what the change said is stored.
- **`mailbox_events`** — the map of what the worker wrote: `(user_id, item_key)` the
  primary key, `event_id` (500, the event's id in the mailbox), `content_hash` (the hex
  SHA-256 of what was written; `''` when the copy is to be rewritten whole),
  `starts_on` (the item's first day, for the window's edge), `change_key` (512, the
  event's version as the last write, or the write-back's last look, left it — null for
  a row written before 00084 or adopted by a reconcile, which takes the next version
  answered as its baseline), `released_at` (set when the person turned the copy into
  a meeting: [never written again](#released-meetings)) and the two stamps. Nothing
  else of the event is stored. A write that answers no change key keeps the one
  stored.

The down migration drops both: the copies already in people's mailboxes stay there,
unmapped.

### The person's setting

Three operations, each under `calendar:access` and on the caller's own row alone:

- **`GET /mailbox-sync`** answers `MailboxSync`: `enabled`, `target`, `detail`, `kinds`,
  `state`, `error` (when set), `lastSyncedAt` (when set), `busySyncedAt` (when the
  [busy read](#the-busy-read) last reached its end; absent before the first and once
  the sync is turned off), `revision`, `availability` and
  `availableKinds` — the kinds the caller may choose, `{key, module}`: the appointment
  kind `calendar.appointment` first, then every kind the [sources](#the-kinds) declare,
  in module order, never `calendar.subscription`. A caller who never saved it is answered
  the defaults at revision 0: off, `default`, `full`, state `off`, and the kinds
  `calendar.appointment` and `staff.absence` (each when offered). **`putBack`**
  (`MailboxSyncPutBack`), when set, is the last change the caller made in Outlook that
  the [write-back](#the-write-back) did not take: `reason` (`conflict`, `refused`,
  `not_owner`, `not_editable` or `many_deleted` — more than ten of the caller's own
  appointments deleted in Outlook in one sync, none taken and every copy put back,
  [below](#deletes-and-what-is-not-a-delete)), `at`, and — when the copy is of an
  appointment, so never for `many_deleted` —
  `appointmentId`, `occurrenceDate` for one occurrence of a series, and `title`, read
  live from the appointment and answered **only when the caller owns or attends it**,
  so the stored key never names what the caller may not read. Absent when nothing was
  put back since the sync was last turned off or cleaned up. The `PUT` and the run
  answer it as the `GET` does — a `PUT` that turns the sync off answers it absent,
  since turning it off forgets it.
- **`PUT /mailbox-sync`** replaces the setting whole — `{enabled, target, detail,
  kinds}` — under `If-Match`, the revision it was read at (bare, quoted or weakly tagged;
  `0` for a caller with no row), rate limited to 10 per client address in 10 minutes
  (`calendar-mailbox-sync-write`), since each write makes the sync due. `target` and `detail` must be one of their values and
  each kind one of `availableKinds`, none twice — each a field error, "Invalid Outlook
  sync" — and the kinds are stored in `availableKinds`' order. **Turning it on, or
  keeping it on, while `availability` is not `available` is 409
  `mailbox_sync_unavailable`, whose `detail` is the availability**; turning it off is
  always taken. The revision is judged under the row's lock (`LockMailboxSync`), and one
  that has moved on is 409 `revision_mismatch`, naming both. The write sets the state —
  `stopping` when a sync that was on is turned off or moved to the other target, or a
  clean-up was owed already; else `pending` when on and `off` when off —, makes the row
  due at once, sets `reconcile` when it is turned on or a clean-up is owed (and keeps it
  set), moves the revision on (the first write makes the row at 1) and leaves the
  worker's record — the mailbox, the calendar, the error, the last sync — as it was.
  A write that turns the sync off also forgets the last change put back.
- **`POST /mailbox-sync/run`** — *Sync now* — makes the caller's row due at once and
  answers the setting; the revision stays. 409 `mailbox_sync_unavailable` while the
  availability is not `available` (the `detail` is the availability), and then 409
  `mailbox_sync_off` while the sync is turned off. Rate limited to 6 a minute per client address
  (`calendar-mailbox-run`).

**The availability** is asked of the neighbours outside any transaction, in this order:

| `availability` | When |
| --- | --- |
| `no_provider` | No enabled module fills the `MailboxCalendar` slot (Entra is off). |
| `switch_off` | The provider's reason is `switch_off`, whatever its state: the Owner's *Calendar sync* switch is off. |
| `unavailable` | The provider's status is anything else but `available`: its connection is not working. |
| `no_account` | `Workforce.DirectoryAccount` answers nothing for the caller, a disabled account (a leaver's), an account of another provider than the slot's `Key`, or one with no external id. |
| `available` | None of the above. |

`permission_missing` stays in the contract's enum but is never answered: the calendar
permission may be granted with Exchange RBAC for Applications, which no token shows, so
the provider never refuses for it, and a mailbox outside the grant is that person's
`mailbox_denied` instead.

### What is pushed

The **window** is from 30 days before today to 180 days after it, in the business time
zone. The **desired set** is built in it, appointments first:

- **Appointments**, when `calendar.appointment` is among the kinds: those the person
  owns or attends with any answer but `declined`, never a cancelled one — the feed's
  read — each **occurrence** its own event with a moved occurrence's own times, keyed
  `appointment:<id>` for a single appointment and `appointment:<id>:<YYYY-MM-DD>` (the
  occurrence's date in the series) for a series.
- **The chosen provider kinds**, read through the [sources](#the-sources-contract) under
  `contracts.AsUser` on a context with no request — [the feed's
  way](#reading-the-providers-for-the-feed), each provider's gate answered for the person
  as for their session — keyed `<kind>:<id>`. A source that fails fails the sync, which
  is retried.
- **Never** a colleague's calendar, a subscription's events — they came from elsewhere,
  perhaps from Outlook itself — or a public holiday.

At most **1500** events per person, a hard cap: past it the projected items farthest
from today are left out first (the feed's order); only when the appointments alone are
more than that are the farthest of their occurrences left out too. Each cut is logged at
info with the person's id and its counts.

**The event.** Each copy is laid out as follows, in the person's preferred language
(Norwegian for `nb`, English otherwise):

| Field | Value |
| --- | --- |
| Subject | The title; a projected item that is done starts `✓ `. **Busy** / **Opptatt** under `detail` `busy`, and for a private appointment the person does not own. |
| Body (text) | The appointment's description, or the item's subtitle, then a blank line and "Kept by Vantigo — change it there:" / "Holdes av Vantigo — endre det der:" with the record's link on the next line. Under `busy`, and for that private appointment, the link alone. |
| Location | The appointment's, under `full` only. |
| Time | An all-day item's days, the last inclusive; a timed one's instants. A point item — no end, or an end not after its start — lasts 30 minutes. `TimeZone` is the business zone's name. |
| Show as | An appointment `tentative` when the person's answer is tentative, else its transparency, `busy` or `free`. `staff.absence` `oof`; `time.entry` and `expenses.claim` `busy`; any other all-day item `free` and any other timed item `busy`. |
| Private | A private appointment, whoever's copy it is. |

There are no attendees, no reminder and no category. The link is the app's — `APP_URL`
and the base path, `/calendar?date=<the occurrence's day>&appointment=<id>` for an
appointment and the item's own path for an item, as [the feed's](#what-the-two-variants-carry)
are — never the public host, which serves only its allowlist; with no `APP_URL` there is
no link. Each copy's **hash** is the SHA-256 of a canonical encoding of every field and
the target, so the same copy hashes the same from one sync to the next and a move to the
other target rewrites everything.

### The mailbox sync worker

The **`calendar-mailbox-sync`** worker writes the copies. It is contributed through
`Workers` behind `CALENDAR_MAILBOX_SYNC_ENABLED` (default on); turned off, nothing is
written to or removed from any mailbox until it is on again. It **wakes every minute** — a
constant — and syncs, one at a time, the rows that are on or owe a clean-up and are due
(`next_sync_at` null or past) — the never-synced first, then the longest due — at most
**50** a cycle, each person once. It takes no lease. Each person is **claimed** in one
short statement of their own (`FOR UPDATE SKIP LOCKED`): the due row whose
`claimed_until` is null or past gets `claimed_until` **ten minutes** ahead — the
person's three minutes below and room for the rows written after them — and the
statement's connection goes back to the pool before anything is asked. The claim is
what keeps two replicas from syncing one person at once, and they share the due rows
between them; it is given back when the person's sync ends, whatever it answered, and
one a stopped replica left lapses. The `PUT`, the appointment writes' nudge and the
[change notifications](#change-notifications) move `next_sync_at` only, never a
claim: a person nudged while their sync runs is due again once it is given back. No
connection is held while the provider, identity, Staff, a source or the pool is asked,
and no transaction is open while a neighbour is: a worker holding one connection while
it waits for another starves a small pool — `pgxpool` opens at most four, or one a
CPU, by default — and the readiness probe with it. Each
person has **three minutes** for everything their sync asks of the neighbours, so one
slow mailbox cannot hold the cycle; the person's own rows are written past it. For each
person:

1. **The provider's status.** `unavailable` calls nothing: the row is `unavailable`
   (error `unavailable`) — a clean-up owed stays `stopping` — and tried again in an hour.
2. **The person's access.** `Access.UserHolds(calendar:access)`. A person who may no
   longer use Calendar, a provider in `cleanup` (the Owner's switch off) or a clean-up owed is
   [cleaned up](#the-clean-up). Then the row is `off` when the toggle is off;
   `unavailable` (error `unavailable`, tried in an hour) when the person or the provider
   cannot go on; and otherwise — a target change — the sync goes on to the new target at
   once.
3. **The account.** `Workforce.DirectoryAccount` under `AsUser`. None, a disabled one
   (a leaver's), another provider's, or one with no external id cleans up what was written to the mailbox
   stored and is `unavailable` with error `no_account`, tried in an hour. A different
   external id than the one stored cleans the old mailbox up first, then starts over on
   the new one. The mailbox is recorded as soon as it is known, whatever the revision.
4. **The *Vantigo* calendar**, for target `own`: `EnsureCalendar` with the stored id; a
   changed id empties the map and makes the sync a reconcile.
5. **The desired set** ([above](#what-is-pushed)).
6. **The write-back** ([below](#the-write-back)), after the map is read: what the person
   changed in Outlook taken into their appointments, and the rest put back. When an
   appointment was written, the desired set is built again, and when the map was
   written, it is read again. A refusal of `Versions` or `Events` stops the sync as
   `Apply`'s do; a failure lets the push and the busy read go on, and is recorded after
   them (the table below).
7. **Reconcile**, when `reconcile` is set (turned on, a clean-up, a refusal, a create
   whose outcome is unknown), at the first sync, and at the first sync of each business
   day (`last_synced_at` falling on an earlier day): `Find` from the window's first
   instant — only events carrying this installation's marker are answered — then
   - an event carrying a key the desired set holds and the map does not is **adopted**
     (mapped with no hash, so it is rewritten) — never one `Find` answers as a
     `Meeting`;
   - a second event carrying a key already mapped — the duplicate an unknown create
     leaves — is **deleted**, never a `Meeting`;
   - a mapped event `Find` answers as a `Meeting` is held as
     [released](#released-meetings) for this sync — neither written nor deleted by its
     push — until the write-back releases its row;
   - a mapped event starting in the window that `Find` no longer answers is
     **forgotten**, to be made anew — only when the list is shorter than 5000, so an
     answer that may be cut forgets nothing, and only when the write-back completed,
     since an absence may then be a delete in Outlook still unread; an event the
     write-back saw there is never forgotten (one taken in from the *Vantigo* calendar
     carries no marker until the push tags it);
   - an event carrying a key the set does not hold is **left alone**: such an event is
     most often a copy that slid before the window and was forgotten on purpose — the
     past stays until a clean-up. An event without the marker — one the person made, in
     the *Vantigo* calendar or their own — is never answered, so never touched.
8. **The diff** against the map: the duplicates' deletes, then a create for each new key
   and an update for each changed hash, in the desired set's order, then a delete for
   each mapped key the set no longer holds that starts inside the window. A mapped key
   that now starts before the window is forgotten, its copy left in the past. A
   [released](#released-meetings) copy is never updated nor deleted: it is forgotten
   once the set no longer holds it. Every update names its key, so it writes the marker.
9. **`Apply`** in slices of 100, the map written after each slice in a short
   transaction, with the change key each write answered; an update answered `Gone`
   becomes a create in a later slice. The first
   op that fails — throttled, or any failure to retry — stops the person's sync after
   its slice; the rest wait for the next sync.
10. **The busy read**, once every op worked ([below](#the-busy-read)). A refusal or
   failure of `Delta` or `Marked` is recorded as `Apply`'s are; a read whose three
   minutes run out between pages keeps its next link and is not a failure (one cut
   inside a call is the call's failure).
11. **Watch** — last, after the result is written, on a sync that pushed and left the
    person `active`: one that worked, and one whose busy read or write-back failed beside
    a push that stood. `Watch` on the mailbox, with the sync's own deadline: past it, it
    is not asked, and the next sync does. Its error is logged at warning by its code and
    changes nothing — the row is written already, and the poll runs either way.
12. **The result**, written only if the revision is still the one the sync read — a `PUT`
   meanwhile has made the row due again, and the sync's outcome is dropped:

| Outcome | `state` | `error` | Next |
| --- | --- | --- | --- |
| Every op done, and the busy read refused nothing | `active` | — | `CALENDAR_MAILBOX_SYNC_POLL` later (default 15 minutes, at least 1); `last_synced_at` now, `attempts` 0 |
| `ErrMailboxDenied` | `unavailable` | `mailbox_denied` | in six hours |
| `ErrMailboxGone` | `unavailable` | `mailbox_gone` | in six hours |
| `ErrMailboxUnknown` | `unavailable` | `no_account` | in an hour |
| `ErrMailboxUnavailable` | `unavailable` | `unavailable` | in an hour |
| The push worked, the busy read or the write-back failed (not a refusal) | `active` | `failed` | 1, 5, 15, 60 minutes, then hourly; `last_synced_at` now, `attempts` counted, never `failed` — the push it rides on works. When both failed, the busy read's is the one logged |
| Any other failure, or a clean-up that met a refusal | unchanged (`pending`, `active` or `stopping`); `failed` from the fifth in a row | `failed` — or the clean-up's refusal, `mailbox_denied` or `unavailable` | 1, 5, 15, 60 minutes, then hourly |

A refusal of one op stops the person's sync after its slice's map is written, and so
does a failure. Each failure is logged at warning with the person's id, the step and a
code; a refusal at info with its code. Nothing of a person's mailbox, their copies'
titles or the provider's words — which carry the mailbox's paths — is logged: an error
is logged by its code alone (`mailbox_denied`, `mailbox_gone`, `mailbox_unknown`,
`unavailable`, `calendar_not_ours`, `deadline`, `canceled` or `failed`).

**Forgetting the busy time.** A result that leaves the sync `off` or `unavailable`
forgets the person's busy rows and their delta — link, window and `busy_synced_at` — in
one short transaction; a failure on the backoff keeps them. The reads join
`mailbox_sync` and answer rows only while it is `enabled`, so nothing left behind is
ever shown.

**The nudge.** Every appointment write — a create, a change, a delete or cancellation, a
colleague's answer, an occurrence moved, cancelled or restored — makes the rows of its
owner and its colleague attendees, before the write and after it, due at once
(`NudgeMailboxSync`), inside the write's own transaction; only rows that are on and not
due already are touched, and the revision is left alone, so a person's open form never
goes stale for it. So an appointment reaches Outlook within a minute. The poll catches
the providers' items and a change of rights; the daily reconcile is the nightly
re-sync.

### The write-back

Step 6 of each sync takes back what the person changed in Outlook
(`mailbox_writeback.go`). It does not ride the busy read's delta: a delta answers no
extended property, the *Vantigo* calendar has no delta in Graph v1.0, a removal cannot
tell a delete from a move out of range, and the write-back must come before the push
while the busy read follows it. Instead every sync asks the provider for the copies'
**versions** and reads whole only the events whose version moved.

**The versions.** `Versions` over the push's window: under target `default` the
default calendar's events carrying this installation's marker from the window's first
instant, each with its key; under target `own` every event of the *Vantigo*
calendar's view over the window, so an event there without the marker is one the
person made. Then each row of the map not [released](#released-meetings):

- **no `change_key`** stored — a row written before `00084`, or adopted by a
  reconcile — takes the version answered as its **baseline**, unread;
- the **same** version as stored is as Vantigo left it: nothing to do;
- a version that **moved** is read whole;
- a row **absent** from a `Complete` answer, starting on or after the window's first
  day, is read whole too — it may be a delete, or a copy moved before the window;
- under target `own`, while the person's kinds include `calendar.appointment`, an
  event **neither mapped nor marked**, and not of a series, is read whole as a [new
  event](#new-events-in-the-vantigo-calendar).

**The reads.** `Events` for those ids, at most **100 a sync** — the moved first, then
the missing, then the new. A missing row past the 100 makes the write-back
incomplete, so nothing is inferred from it. A row `Events` answers is compared; one in
`Gone` — the directory's own "not found" — is a [delete](#deletes-and-what-is-not-a-delete);
one **neither answered nor `Gone`** was not read this time: it keeps its old change key
for the next sync, and when it was a missing row the write-back is incomplete. A moved
version is compared, field by field after normalising — line endings, trailing space
(a no-break space read as a space), the body cut at the first line that *starts with*
*Kept by Vantigo — change it there:* or *Holdes av Vantigo — endre det der:*, times to
the second — with the copy the desired set holds for its key. **Equal is noise**
(Exchange's own touch): only the new change key is kept. So is an event **exactly as
the push last wrote it** (its hash, in the row's target and zone, is the row's) beside
a change made in Vantigo: no conflict, the change key kept, and the push rewrites it.

**The decisions** for a mapped copy whose version moved and that differs:

| The copy | What happens |
| --- | --- |
| The person turned it into a meeting (`Meeting`) | [Released](#released-meetings). |
| A key the desired set no longer holds | Nothing: the push deletes or forgets it. |
| An occurrence, an exception or a series master (`Series`), another installation's (`Foreign`), or a cancelled meeting | Never written back: the change key is kept. |
| A copy whose last write is unknown (the row's hash is `''` — a put-back, a take or an adoption whose push then failed) | **Never taken**: put back with no record, as the push rewrites it whole. |
| A projected item (`<kind>:<id>`) | Put back, `not_editable`. |
| An appointment the person attends, not owns | Put back, `not_owner`. |
| A cancelled appointment, or one gone | Nothing. |
| An owned appointment whose copy Vantigo changed since the last write (the row's hash is not the desired one) | **Vantigo wins**: put back, `conflict`. |
| `appointment:<id>`, owned and single | The changed fields become a `PUT` of the appointment ([below](#the-fields-taken)). |
| `appointment:<id>:<date>`, owned, an occurrence | A change of the times moves the occurrence (`PUT …/occurrences/{date}`); any other change is put back, `not_editable` — after the move, when both changed. |

**Put back** clears the row's hash, keeping the version read, so the push rewrites the
copy whole, and records the change on the person's `mailbox_sync` row (`putBack` on the
[setting's `GET`](#the-persons-setting); the last one wins). A write that worked clears
the hash too, so the push writes the appointment's own form back — the kept line, the
link — and keeps the change key it answers; the row's `starts_on` moves with the
appointment, so one moved before the window is forgotten by the push, not deleted.

**Each write is the screen's own.** The worker writes an appointment through the same
code as `PUT /appointments/{id}`, `PUT /appointments/{id}/occurrences/{date}`,
`DELETE /appointments/{id}` and `POST /appointments` — on the person's behalf
(`contracts.AsUser`, on a context with no request), with the person as the caller and
no transaction open across any call out. So its validation, its revision, `SEQUENCE`,
the updated invitations to the outside attendees and the notices to colleagues go as
for a change made on the screen ([changing and deleting](#changing-and-deleting),
[when it goes](#when-it-goes), [what a colleague receives](#what-a-colleague-receives)).
A write's answer:

- done — counted, the copy made stale as above;
- a 400 (a field the appointment's rules refuse) — put back, `refused`;
- a 409 `revision_mismatch` (the appointment moved on between the read and the write)
  — put back, `conflict`;
- any other refusal — gone, cancelled, no longer a day of the series — the copy made
  stale, left to the push;
- a failure — the write-back stops there.

At most **20 writes** to appointments a sync. Whatever is left over — a version not yet
read, a write past the 20 — keeps its old change key, so the next sync sees it again,
and the write-back is then incomplete. The map's change keys, stale hashes, releases
and forgets, and the put-back, are written in one short transaction after the
decisions.

#### The fields taken

From the copy of an owned single appointment, each field that differs from what was
written:

| In Outlook | In the appointment |
| --- | --- |
| The subject | The title, trimmed. An empty one is put back, `refused`. |
| The body above the kept line | The description; an empty one clears it. A body that did not come as text is never taken. |
| The location | The location; an empty one clears it. |
| The start, the end, all day | The times and `allDay`, to the second; the appointment's zone is kept. |
| Show as | `free` is transparency `free`; any other value `busy`. |
| Sensitivity | `private` or `confidential` is visibility `private`; anything else `shared`. |

Under `detail` `busy` the subject, body and location written were placeholders, so only
the times, show as and the sensitivity are taken; a change to the rest is simply
rewritten by the push. Everything else of the appointment — its attendees (kept as
they stand, so one the directory no longer answers is kept), its rule, its customer
and project, its links — is sent back unchanged, with the revision read as its
`If-Match`. A copy whose fields all match the appointment, though its text differs from
the copy, is rewritten and nothing is written. An occurrence's move is held to the
series' kind: a timed series' occurrence moved to all day, or the other way, is put
back, `not_editable`.

#### Deletes, and what is not a delete

A mapped copy `Events` answers in `Gone` — the directory's own "not found": no longer
in **that calendar**, deleted or moved to another calendar or folder — is a delete in
Outlook. An id `Events` neither answered nor called `Gone` is never one: it is left for
the next sync. Each delete goes to the first of these that holds:

| The copy | What happens |
| --- | --- |
| A key the desired set no longer holds | Nothing: left to the push. |
| Not an appointment's key (a projected item's), an appointment gone, one the person attends but does not own, a cancelled one, or an occurrence of an appointment no longer a series | Forgotten, and made again by the push while it is wanted. |
| The row's hash is `''` (its last write unknown) | Put back with no record: the push re-creates it. |
| The row's hash is not the desired one (the appointment changed in Vantigo since the last write) | **Vantigo wins**: put back, `conflict`; nothing is deleted. |
| An owned single appointment | Deleted through `DELETE /appointments/{id}`'s path, which [cancels it](#cancellation), with the CANCELs, once someone outside was invited. |
| An owned occurrence | Cancelled (`PUT …/occurrences/{date}` with `cancelled`). |

**Many at once.** When more than **10** deletes or cancellations of the person's own
appointments would be taken in one sync — a calendar emptied, a client gone wrong, more
likely than the person's intent — **none is taken**: every one of those copies is put
back (made stale, so the push re-creates it), the put-back recorded `many_deleted` with
no key, and the counts logged at warning with the person's id. The other fates above
are taken as usual.

A copy that `Events` still answers — moved before the window, say — is a change, as
above. The guards: the map is emptied by every clean-up, target change and mailbox
move before anything else, and the push's own deletes forget their rows, so an absence
is never the module's own doing; only the directory's own "not found" deletes; an
incomplete list infers nothing; and on a sync whose write-back did not complete, the
[reconcile](#the-mailbox-sync-worker) forgets no mapped event `Find` no longer sees.

#### Released meetings

A copy the person **turned into a meeting** in Outlook — they added attendees, and
Outlook mailed them — is **released**: `released_at` set, and it is never written,
deleted or taken back again, since a write would mail its attendees. The push leaves it
alone while it is wanted and forgets it without a delete once it is not; the clean-up
forgets it without a delete too. A copy `Find` answers as a meeting is treated as one
before the write-back has read it: the reconcile never adopts nor deletes it, and a
mapped one is held as released for that sync. The appointment in Vantigo is not changed
by the release.

#### New events in the Vantigo calendar

Under target `own`, and only while the person's kinds include `calendar.appointment`,
an event of the *Vantigo* calendar that is neither mapped nor marked, single, not cancelled, not a meeting and not another installation's becomes an
appointment the person owns, through `POST /appointments`' path: the subject its title
(**Untitled** / **Uten tittel**, in the person's language, when empty), the body as
text its description, the location, each cut to its limit, the times in the business
zone, show as and the sensitivity as [above](#the-fields-taken). Its map row —
`appointment:<id>`, the event's id, no hash, the change key — is written in the
create's own transaction, so an event is never taken twice, even when the push that
tags it with the marker fails. A refusal is logged at info by its code alone and the
event left as it is, tried again at the next sync. A series, a meeting or another
installation's event there is left alone.

**When the write-back fails.** A refusal of `Versions` or `Events` (denied, gone,
unavailable) stops the sync as `Apply`'s refusals do. Any other failure lets the push
and the busy read go on, and the sync is recorded `active` with error `failed` on the
backoff. Such a push may then overwrite an Outlook edit unread when Vantigo changed the
same copy — Vantigo winning, unnamed. A run that wrote or put back anything is logged
at info with the person's id and its counts; nothing an event says is ever logged.

#### The person's own Outlook titles

Not shown. The write-back reads whole only the copies and the *Vantigo* calendar's
events, never the person's own events: showing their titles would mean storing the
subjects of a private calendar or reading Graph on the request path, and a colleague's
view must never see them. The `calendar.outlook` item stays titleless; its shape —
no title, `outlook.showAs` — leaves room for it.

### The busy read

Step 10 of each sync whose push worked reads the person's **default calendar** back
through `Delta` — Graph's `calendarView/delta`, the only calendar whose delta is
documented, and the one Exchange computes free/busy from. The push comes first, so the
map already holds the copies just written.

**The window.** A delta's window is fixed in its link, so the read keeps one of its own,
wider than the push's: from **30 days before today to 210 days after** it, midnight to
midnight in the business zone, set when a delta starts and stored with its link
(`busy_from`, `busy_to`). A delta is started afresh when there is no link, when the
stored window ends before today + 180 days — about once a month — and once a sync when
the provider answers `ErrMailboxDeltaExpired`, which first forgets the person's rows
and link. A second expiry in the same sync is a failure.

**The pages.** Each `Delta` call reads at most 40 pages of 50 events; the worker calls
again while the delta is not `Done` and the person's three minutes last. A call cut
short answers the next link, which is stored with each page, so the next sync takes it
up. Running out of time between pages is not a failure; a call the deadline cuts short
is, as any failure of `Delta` or `Marked` — recorded as the busy read's failure below,
which never hides the push that worked. Each page is written in **one short
transaction** after its calls: the link and the window first, written only while the
sync is on (`enabled`) — a sync turned off during the read deletes the person's rows in
that same transaction and the read stops, keeping nothing —, then on a fresh delta's
first page the person's rows emptied, then each change a delete or an upsert, and, at
the delta link, `busy_synced_at`.

**Our own copies are skipped**, by the map and one read, since a delta answers no
extended property and the marker cannot be seen in it. An event is ours when its id is
an `event_id` of the person's `mailbox_events` — every copy written to the default
calendar, a calendar view answering a single event by its own id — or, for one that is
not, is a single event (not `Series`: the push never writes a series) and would be
stored, when `Marked` answers it, asked in slices of 100. That catches a copy whose
create's outcome was unknown, one forgotten as it slid before the push's window, and
one left over from a target change. Graph's `transactionId` was not used: delivery A's
copies were written without one, and since Graph de-duplicates creates on it, a copy
re-made after a `Gone` would collide with its old self. Another installation's copies
are not ours: they are the person's busy time like any other event.

#### The busy rows

**`mailbox_busy`** (migration `00083`) holds one row per event that blocks the person's
time: `(user_id, event_id)` the primary key — `event_id` (512, the provider's bound on
an id) the event's or the
occurrence's id in the mailbox, which never leaves the server — `all_day`, the timed pair
`starts_at`/`ends_at` (the end exclusive) or the all-day pair `start_date`/`end_date`
(the last day inclusive), `show_as` (20) and `updated_at`. `ix_mailbox_busy_starts` on
`(user_id, starts_at)` and `ix_mailbox_busy_dates` on `(user_id, start_date)` serve the
window's reads. **Never a subject, a body, a location, an organizer or an attendee**:
the delta answers them, and the provider decodes none of them.

- An event is stored unless it is ours, `free` or cancelled; a change to any of those
  deletes its row, and so does a removal — whatever its reason: an event moved out of
  the window is answered removed.
- `show_as` is `busy`, `tentative`, `oof` or `workingElsewhere`; a value the contract
  does not name is stored `busy`.
- An all-day event's dates are its start's and end's calendar dates, the end made
  inclusive, each rounded to the nearest midnight should Graph answer it off one. An
  event whose times do not read is left out by the provider, rather than holding the
  delta at its page for ever.
- At most **5000** rows a person. Past it a **new** event is not stored — one already
  stored is still rewritten or deleted — and the fact is logged at warning once a sync,
  with the person's id and the cap.

**What reads them.** The person's own window ([`calendar.outlook`](#the-kinds), every
`show_as`), [a colleague's calendar](#a-colleagues-calendar) (`busy` and `oof`) and the
[busy time contract](#the-busy-time-contract) (`busy` and `oof`) — each only while the
person's sync is `enabled`. Nothing of an event, not even its id, is ever logged.

**The person's own titles are not shown** ([why](#the-persons-own-outlook-titles)).

### Change notifications

Where Microsoft can reach the installation, a change made in a person's mailbox makes
their sync due at once instead of at the next poll. **A notification is a nudge and
nothing more**: it sets one time, and the reads stay the truth. The three calls are
[`Watch`, `Unwatch` and `Notified`](#the-contract); the provider's side — one Graph
subscription per mailbox, the webhook, its checks — is in [the Entra
reference](/en/reference/entra/#change-notifications). It is asked for only while
`PUBLIC_BASE_URL` is set and `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` (default `true`)
is on; otherwise nothing is subscribed. The poll (`CALENDAR_MAILBOX_SYNC_POLL`) is
unchanged either way.

- **`Watch`** is the sync's last step (step 11 [above](#the-mailbox-sync-worker)).
- **`Unwatch`** is a [clean-up](#the-clean-up)'s step, for every reason but a target
  change: the subscription is the mailbox's, not the calendar's.
- **`Notified`** is asked **each cycle**, **before anyone is claimed** — no connection
  is held across the read; every replica asks, a mark being idempotent — with `since` — held in memory by the worker, per process, **initially now
  minus the poll interval** and advanced to the latest `At` it marked — **less 30
  seconds**: the overlap covers a notice whose write committed after a later one was read,
  and replicas whose clocks differ a little. Taking a notice twice is harmless, since the
  person has synced since. After a **full page** of 5000 the next asks from `since`
  exactly, so the next page follows. The cycle's notices are marked **in one statement**
  (`MarkMailboxSyncNotified`, through the index on `lower(mailbox)`, no transaction held
  across the call): per mailbox, compared case-insensitively, the latest notice makes
  due, at now, the rows that are on, `active` or `pending`, **with `attempts` 0** — **a
  notice never overrides the backoff** of a failing sync — whose last sync **started
  before the notice** and that are not due already. The revision is left alone, as the
  nudge does. If the statement fails, `since` stays, the same notices are asked for
  again next cycle and a warning is logged; a `Notified` that fails is logged at warning
  by its code and the cycle goes on. A provider with none asks nothing.

### The clean-up

A person's copies are removed when they turn the toggle off or change the target, when
they may no longer use Calendar, when Staff no longer names their account (or names
another), and when the provider is in `cleanup` — the Owner's switch turned off. The
clean-up works on the mailbox stored on the row, and is decided by its `calendar_id`:

- with one, it deletes the *Vantigo* calendar (`DeleteCalendar`, with everything in it)
  — only a calendar the provider made, and only while it holds **no meeting**: no event
  `Find` answers there as a `Meeting` and no [released](#released-meetings) row. One
  that holds a meeting (logged at info with the counts), and one not the provider's
  (`ErrCalendarNotOurs`, logged at warning), is left: the copies in it are deleted one
  by one instead, never a meeting;
- without one, it deletes from the default calendar **every event carrying this
  installation's marker, whatever its date** (`Find` with no bound), and every mapped
  one besides, 100 to an `Apply`, forgetting each one done or gone — except a
  [released](#released-meetings) copy or any event `Find` answers as a `Meeting`, which
  is forgotten and never deleted. A list as long as
  `Find`'s bound is a failure once its deletes are done, so the rest go on the retry.

**The subscription.** Every clean-up but a target change's also calls `Unwatch` on the
mailbox, **after the copies are removed or forgotten** — they are what the person sees, so
a refused `Unwatch` holds nothing back — and before the clean-up is finished. An unusable
provider (`ErrMailboxUnavailable`) or a failure keeps the clean-up owed whole, retried on the
backoff, the deletes it repeats being done or gone by then; the row stays `stopping`.
`ErrMailboxGone`, `ErrMailboxUnknown` and **`ErrMailboxDenied`** are logged at info and the
clean-up goes on: a subscription the provider may not delete stays `deleting` in its own
rows and lapses within seven days, and its notifications name no sync.

Only a mailbox that is no longer there — `ErrMailboxGone`, or `ErrMailboxUnknown`, an
account the provider no longer manages — has its copies **forgotten** unremoved, logged
at warning with the person's id: a leaver's mailbox is soft-deleted with them. A mailbox
denied, a provider unusable or a failure keeps the map and the clean-up owed, retried on
the backoff (a row that owed it stays `stopping`). Then the map is emptied, the
person's [busy rows](#the-busy-rows) and their delta forgotten, the last change put
back cleared, `calendar_id` cleared and the next sync made a reconcile.
Turning the switch back on resumes everyone whose toggle is on, from a reconcile.

## The busy time contract

The module provides one single-provider contract, `contracts.CalendarBusy`
(`Module.CalendarBusy`, `Deps.CalendarBusy`), which Planning's
[board](/en/reference/planning/#the-board) reads. `Compose` resolves it last of the
single-provider slots, after the mailbox calendar; `Workers` does not, since no worker
reads it. It is nil when the calendar is off.

```go
type CalendarBusy interface {
    Busy(ctx context.Context, userIDs []uuid.UUID, from, to time.Time) (map[uuid.UUID]BusyTime, error)
}
type BusyTime struct {
    Spans []TimeSpan  // timed, merged, ordered, clipped to the window
    Days  []DateSpan  // all-day, merged, ordered, clipped to the window
}
type TimeSpan struct { StartsAt, EndsAt time.Time } // the end exclusive
```

- **The bounds.** `from..to` are civil dates, both inclusive, at most **400** days
  (`CalendarBusyMaxDays`), for at most **500** users (`CalendarBusyMaxUsers`); a wider
  ask, or `to` before `from`, is `ErrBusyWindowOutOfBounds`.
- **Provider-gated** ([module boundaries](/en/contributing/module-boundaries/) rule 12).
  The calendar answers the principal of the request in `ctx` by its own colleague rule,
  which a consumer cannot know: their own id when they hold `calendar:access`, anyone
  else's when they hold `calendar:view-colleagues` (each asked of identity's `Check`
  once a call). An id the caller may not see, or a user the directory does not answer
  as active, is left out of the map — nothing, not an error — and so is every id when
  `ctx` carries no request or no principal, or carries the `AsUser` mark even over a
  request: the read answers a request's own principal alone, never a read on someone's
  behalf. A consumer
  shows what comes back and never says what was withheld; an error is a failure.
- **What counts.** What a [colleague's calendar](#a-colleagues-calendar) shows as busy:
  the appointments the user owns or accepted, transparency `busy`, not cancelled, each
  occurrence in the window (a cancelled occurrence dropped, a moved one at its new
  time), expanded in the business zone; and, while the user's Outlook sync is on, their
  [busy rows](#the-busy-rows) of `show_as` `busy` or `oof`.
- **The merge.** Timed spans are clipped to the window's instants (midnight to midnight
  in the business zone) and all-day spans to its dates, then each sorted and merged
  where they overlap or touch — all-day runs a day apart are one run. A user busy at no
  time in the window is not in the map.

It is called with the request's context and never inside a transaction; its own reads
go through `contractscalls.go` (`accessHas`).

## Refusals and revisions

Every refusal of the module's own is an RFC 9457 problem, `CalendarProblem`: `title`,
`status`, `detail` and, when a named rule refused, `code` — what a client keys off, never
the words. A 400 carries `errors`, the field errors keyed by the request's camelCase
field, a nested one by its path and a list element by its place: `startsAt`,
`recurrence.rule`, `attendees[0]`. Its `title` says what was refused — "Invalid
appointment", "Invalid occurrence", "Invalid response", "Invalid window", "Invalid
query parameters", "Invalid subscription" or "Invalid Outlook sync" — so a client can tell them apart without reading the field errors. A
request the server cannot decode — a body that is not JSON, a date parameter that is not
a date, a missing `If-Match` — is a bare 400 with neither `code` nor `errors`.

| Code | Status | When |
| --- | --- | --- |
| *(field errors)* | 400 | "Invalid appointment": a body that breaks a rule above — the title, the wrong pair or a missing field, an end before the start, a span too long, a zone either check refuses, a visibility or transparency that is not one, a rule outside the subset, over 500 characters or not producing the start, more than 50 distinct attendees, an attendee whose fields do not match its kind, a colleague who is unknown, inactive or the owner, a contact the directory does not answer or that has no address, a contact named by a writer without `customers:contacts-view`, an address that is not one, a description or location too long — or an `If-Match` that holds no revision (the field error on `If-Match`). "Invalid occurrence": an occurrence `PUT` whose `date` is more than ten years from today, or on an appointment that does not recur (both on `date`), or a body that is both or neither of cancelled and moved (on `cancelled`), or a move that breaks the span rules. "Invalid response": a `status` that is not an answer. "Invalid window": `to` before `from`, over 93 days, `from` or `to` more than ten years from today, or a `timeZone` either check refuses. "Invalid query parameters": a `limit` on `GET /users` outside 1 to 50. "Invalid subscription": a subscription's `POST` or `PUT` that breaks [the rules at write](#the-rows-and-the-rules-at-write) — the name, the address, the colour — with the field error on that field. |
| `revision_mismatch` | 409 | A `PUT /appointments/{id}`, or a `PUT /mailbox-sync`, whose `If-Match` revision has moved on. The detail names both revisions. |
| `occurrence_not_in_series` | 409 | `PUT /appointments/{id}/occurrences/{date}` for a date the series' rule never produces. |
| `appointment_cancelled` | 409 | Any write to a [cancelled](#cancellation) appointment — a `PUT`, a `DELETE`, an occurrence's `PUT` or `DELETE`, a colleague's answer, a resend — and an answer by the link (`POST /rsvp`). |
| `invitations_unavailable` | 503 | `POST /appointments/{id}/attendees/{attendeeId}/resend` when the mail driver is not `smtp`. |
| `subscription_limit` | 409 | `POST /subscriptions` when the caller has ten subscriptions already. |
| `mailbox_sync_unavailable` | 409 | `PUT /mailbox-sync` turning the sync on, or keeping it on, while it cannot run, and `POST /mailbox-sync/run` while it cannot run. The `detail` is the [availability](#the-persons-setting) — `switch_off`, `no_provider`, `unavailable` or `no_account`. |
| `mailbox_sync_off` | 409 | `POST /mailbox-sync/run` while the sync is turned off. |
| — | 403 | `GET /contacts` without `customers:contacts-view` and `customers:associations-view`. |
| — | 429 | `POST /rsvp/read` over 60, `POST /rsvp` over 30, the feed's `GET /feed/{file}` over 120, or `POST /subscriptions`, `PUT /subscriptions/{id}` and `POST /subscriptions/{id}/refresh` together over 30 (`calendar-subscription-fetch`), per client address in 10 minutes; `PUT /mailbox-sync` over 10 per client address in 10 minutes (`calendar-mailbox-sync-write`) and `POST /mailbox-sync/run` over 6 per client address in a minute (`calendar-mailbox-run`): the limiter's `rate_limited`, with `Retry-After`. |
| — | 503 | `GET /items`, and the feed's `GET /feed/{file}`, when a calendar source failed or ran out of time: "Calendar source unavailable", naming no module. |

**A 404 is bare, with no body**, and says nothing more than "not for you": an unknown
appointment, one the caller neither owns nor attends (a read), one the caller does not
own (a change, a delete, an occurrence, a resend), one the caller does not attend (an
answer), a colleague who is not an active user, an attendee to resend who is not an
outside one on the appointment, a customer the directory does not know (`GET /contacts`),
a token no attendee holds (the two anonymous operations of the answer by link), a feed
`file` that no address holds, that does not end in `.ics`, or whose address's person may
not act or no longer holds `calendar:access`, a variant that is neither `full` nor `busy`
or that the caller has no address of (the feed's writes), and a subscription that is
unknown, another person's, or deleted while its fetch was out. The uniform 403 is the access layer's.

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
- **No mail inside a transaction.** The invitations, the cancellations and the colleagues'
  and owner's notices are sent **after the commit**, through the same accessors (the mail
  seam and `UserDirectory.Notify`), and each send's mark — `invited_at` and
  `invited_sequence`, or `send_failed_at` — is written after it, on the pool, outside any
  transaction. A failed send never undoes the write that caused it.
- **The appointment's row.** Every write of an appointment — a change, a delete or
  cancellation, an exception and its removal, an attendee's answer in the app or by the
  link — runs in one transaction that first
  takes the appointment's row `FOR UPDATE` (`withLockedTx`); the revision, the attendees'
  reset, the dropped exceptions and the occurrence's place in the series are judged
  under it. Nothing locks two appointments.
- **The person's subscriptions.** A subscription's `POST` counts the person's
  subscriptions and inserts the new one in one transaction that first takes a
  transaction-scoped advisory lock on the person's id, under the table's own prefix
  (`LockSubscriptionsOfUser`), so two saves at nine cannot both see nine and both insert;
  its inline fetch runs after the commit.
- **No fetch inside a transaction.** A subscription's fetch — a save's, **Refresh now**'s
  or the worker's — is made with no transaction of the module's open, and its outcome is
  written to the row after it, so a slow calendar elsewhere never holds a lock here.
- **No mailbox call inside a transaction.** The [Outlook sync](#outlook-sync) reaches the
  mailbox provider and Staff only through the accessors, between short transactions: the
  worker's due list is committed before the first call, the map is written after each
  `Apply`, and the person's `PUT` asks the availability before its transaction opens and
  judges the revision under the row's lock (`LockMailboxSync`). The busy read's
  `Delta` and `Marked` are made with no transaction open, and each page is written
  after them in its own short one. The nudge is the one write of the sync made inside
  another transaction — the appointment's — and calls nobody.

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
| `POST /appointments/{id}/response` | `calendar:access`, then a colleague on it — not the owner (otherwise 404) |
| `POST /appointments/{id}/attendees/{attendeeId}/resend` | `calendar:access`, then the owner (otherwise 404) |
| `GET /users` | `calendar:access` |
| `GET /contacts` | `calendar:access`, then `customers:contacts-view` and `customers:associations-view` (otherwise 403) |
| `GET /colleagues/{userId}/items` | `calendar:access` and `calendar:view-colleagues`; then an active user (otherwise 404) |
| `POST /rsvp/read` | `anonymous`; 60 per 10 minutes per client address |
| `POST /rsvp` | `anonymous`; 30 per 10 minutes per client address |
| `GET /feed` | `calendar:access`; the caller's own addresses |
| `POST /feed/{file}`, `DELETE /feed/{file}` | `calendar:access`; `file` the variant, `full` or `busy` (otherwise 404); the caller's own address |
| `GET /feed/{file}` | `anonymous`; `file` the token with `.ics`; 120 per 10 minutes per client address (`calendar-feed`); then the token's person may act and holds `calendar:access` (otherwise 404) |
| `GET /subscriptions` | `calendar:access`; the caller's own |
| `POST /subscriptions` | `calendar:access`; the caller's own; 30 per 10 minutes per client address, shared with the next two (`calendar-subscription-fetch`) |
| `PUT /subscriptions/{id}` | `calendar:access`, then the caller's own (otherwise 404); in `calendar-subscription-fetch` |
| `DELETE /subscriptions/{id}` | `calendar:access`, then the caller's own (otherwise 404) |
| `POST /subscriptions/{id}/refresh` | `calendar:access`, then the caller's own (otherwise 404); in `calendar-subscription-fetch` |
| `GET /mailbox-sync` | `calendar:access`; the caller's own |
| `PUT /mailbox-sync` | `calendar:access`; the caller's own; `If-Match`; 10 per 10 minutes per client address (`calendar-mailbox-sync-write`) |
| `POST /mailbox-sync/run` | `calendar:access`; the caller's own; 6 a minute per client address (`calendar-mailbox-run`) |

Every operation requires `calendar:access` but the three `anonymous` ones, which the token
alone admits. The request and response shapes are in the
contract, [`openapi/calendar.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/calendar.yaml),
and in the API reference generated from it.
