---
title: "Calendar module"
description: "One person's calendar: the appointments the module owns, with recurrence, colleagues, customers' contacts and typed addresses on them and their answers; the invitations mailed to the people outside, the answer by link and the cancellation; the sources contract through which other modules project their rows; the window read in the viewer's zone; a colleague's busy and free; the dashboard's agenda; the permissions and the lock rules."
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

It depends on nobody but identity: it reads users through `contracts.UserDirectory` —
and mails a colleague through its `Notify`, without ever learning their address — and the
business day through `contracts.BusinessCalendar`, which identity always provides, and —
when they are wired — a customer's name, a contact's name and address and a customer's
contacts through `contracts.CustomerDirectory`, and a project's code and name through
`contracts.ProjectDirectory`. It sends the invitations to the people outside the company
through the installation's own mail configuration (`SMTP_FROM`), the way Staff sends its
mails. `MODULES=calendar` alone is a valid installation: a calendar of appointments and
public holidays, with colleagues and typed addresses on them. It provides no
single-provider contract.
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
[locks](#locks) and the [endpoints](#endpoints) — and, for the people outside the
company, the [three kinds of attendee](#three-kinds-of-attendee), the
[invitations](#invitations) mailed to them, [the answer by link](#the-answer-by-link) and
[the cancellation](#cancellation) a delete becomes once they have been told — and [what a
colleague receives](#what-a-colleague-receives) instead.

## The schema

Two migrations: `00055_calendar_baseline.sql` creates the `calendar` schema and its three
tables, and `00056_calendar_invitations.sql` adds the attendees from outside the company
and the cancelled state. Every id is a `bigint` identity starting at 1001. Every foreign identifier — the
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

The baseline's down migration drops the schema with everything in it.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `calendar:access` | Use Calendar | The calendar, one's own appointments and answers, the dashboard's agenda. **Every operation requires it**, but the two anonymous ones of [the answer by link](#the-answer-by-link). | no |
| `calendar:view-colleagues` | See colleagues' calendars | [A colleague's calendar](#a-colleagues-calendar): their appointments as busy or free, with the title when it is shared. | no |

Both are in the category "Calendar" and delegable. The Owner holds them through the
wildcard; anyone else needs a role that grants them. There is no `manage` key: the module
has no settings. A colleague's view is the one thing not everyone should have by
default — an installation that wants every employee to see every other's busy blocks
grants `calendar:view-colleagues` to the employee role. Each operation's rule is in the
contract's `x-vantigo-access`; a caller without it gets the access layer's uniform 403.

`GET /meta` (`getCalendarMeta`) answers `canViewColleagues`, so the app never re-derives
the rule, beside the business `timeZone`, `today` in it, the
[sources](#the-kinds), and `invitationsAvailable` — whether the installation can mail an
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
lower-cased) are kept once, and the limit counts what is left: at most **50** attendees
of all kinds together, and more is one field error on `attendees`.

On a `PUT` the list replaces the old one. An attendee still listed is **kept**, with
their answer, their link and their invitation record — unless [when
changed](#changing-and-deleting), which resets every answer; one left out is
**dropped**, with their answer, and is told ([a `CANCEL`](#when-it-goes) to an outside
one, [a notice](#what-a-colleague-receives) to a colleague); a new one is **added** at
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
company. `PARTSTAT` is the attendee's answer in iCalendar's words (`NEEDS-ACTION`,
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
attendee, one after the other, under a budget of **thirty seconds** for the lot, on a
context detached from the request, so a caller who leaves does not stop the sends; when
the budget runs out, the attendees not yet reached are marked failed:

| On | To whom | Method, `SEQUENCE` |
| --- | --- | --- |
| a create | every outside attendee | `REQUEST`, the row's sequence |
| a `PUT` that bumped `sequence` (when or where changed) | every outside attendee, kept or new | `REQUEST`, the new sequence |
| a `PUT` that did not | the outside attendees **added** | `REQUEST`, the current sequence |
| a `PUT` | each outside attendee **dropped** | `CANCEL` to that one, the current sequence, with that one as the only `ATTENDEE` |
| an occurrence moved, cancelled or put back | every outside attendee | `REQUEST`, the new sequence — the whole series |
| the appointment [cancelled](#cancellation) | every outside attendee, one whose earlier send failed included | `CANCEL`, the new sequence |
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
contact's name or the name typed, else the address's part before the `@` — never the
address; `status` the attendee's
current answer; `cancelled` whether the appointment has been
[cancelled](#cancellation); `language` the organiser's ([the
language](#the-language)), which the page is shown in rather than the browser's. A timed
appointment carries `startsAt` and `endsAt`, an all-day one `startDate` and `endDate`
(inclusive); a series answers its first occurrence. A body that does not decode, or
carries no token, is a 400, and so is a `status` that is not an answer.

The answer takes `status` — `accepted`, `tentative` or `declined` — sets the attendee's
`status` and `respondedAt`, under the appointment's row lock, and answers the page again.
It changes nothing of the appointment itself: neither its `revision`, its `sequence` nor
its `updatedAt`. An answer to a cancelled appointment is a 409
`appointment_cancelled`. The answer may be changed through the same link at any time
until then. After the commit the owner is told through identity — "Ola Hansen
accepted: Kundemøte" ([what a colleague receives](#what-a-colleague-receives)).

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
  `STATUS:CANCELLED` is mailed to every outside attendee — one whose earlier send failed
  included — and recorded on each as a send is ([when it goes](#when-it-goes)), and the
  colleagues are told ([what a colleague receives](#what-a-colleague-receives)).

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
- for a bounded series, `series_last_date`;
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
an answer from an invitee with no name is "An invitee" ("En invitert"). A move, cancel
or restore of one occurrence sends no notice. The body is the same sentence and
"Open it in Vantigo: <link>" ("Åpne den i Vantigo: …"), the link into the app
`APP_URL/calendar?date=<the first day>&appointment=<id>`, and nothing else: the
appointment is on the colleague's calendar already, and their answer is the in-app one.
A colleague gets no ICS and no native buttons, by design. A notice that fails is logged
as a warning, `calendar: a notice could not be sent`, with `appointment_id` and
`user_id`, and never fails the request.

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
any status, and not [cancelled](#cancellation) — whose span overlaps the window: a single one by its instants or dates, a
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

- the colleague's appointments — owned, or attended and **accepted**, and not
  cancelled — with transparency `busy`, expanded as `GET /items` expands them, each with `isOwner` false and no
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
| *(field errors)* | 400 | "Invalid appointment": a body that breaks a rule above — the title, the wrong pair or a missing field, an end before the start, a span too long, a zone either check refuses, a visibility or transparency that is not one, a rule outside the subset, over 500 characters or not producing the start, more than 50 distinct attendees, an attendee whose fields do not match its kind, a colleague who is unknown, inactive or the owner, a contact the directory does not answer or that has no address, a contact named by a writer without `customers:contacts-view`, an address that is not one, a description or location too long — or an `If-Match` that holds no revision (the field error on `If-Match`). "Invalid occurrence": an occurrence `PUT` whose `date` is more than ten years from today, or on an appointment that does not recur (both on `date`), or a body that is both or neither of cancelled and moved (on `cancelled`), or a move that breaks the span rules. "Invalid response": a `status` that is not an answer. "Invalid window": `to` before `from`, over 93 days, `from` or `to` more than ten years from today, or a `timeZone` either check refuses. "Invalid query parameters": a `limit` on `GET /users` outside 1 to 50. |
| `revision_mismatch` | 409 | A `PUT /appointments/{id}` whose `If-Match` revision has moved on. The detail names both revisions. |
| `occurrence_not_in_series` | 409 | `PUT /appointments/{id}/occurrences/{date}` for a date the series' rule never produces. |
| `appointment_cancelled` | 409 | Any write to a [cancelled](#cancellation) appointment — a `PUT`, a `DELETE`, an occurrence's `PUT` or `DELETE`, a colleague's answer, a resend — and an answer by the link (`POST /rsvp`). |
| `invitations_unavailable` | 503 | `POST /appointments/{id}/attendees/{attendeeId}/resend` when the mail driver is not `smtp`. |
| — | 403 | `GET /contacts` without `customers:contacts-view` and `customers:associations-view`. |
| — | 429 | `POST /rsvp/read` over 60, or `POST /rsvp` over 30, per client address in 10 minutes: the limiter's `rate_limited`, with `Retry-After`. |
| — | 503 | `GET /items` when a calendar source failed or ran out of time: "Calendar source unavailable", naming no module. |

**A 404 is bare, with no body**, and says nothing more than "not for you": an unknown
appointment, one the caller neither owns nor attends (a read), one the caller does not
own (a change, a delete, an occurrence, a resend), one the caller does not attend (an
answer), a colleague who is not an active user, an attendee to resend who is not an
outside one on the appointment, a customer the directory does not know (`GET /contacts`),
and a token no attendee holds (the two anonymous operations). The uniform 403 is the access layer's.

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

Every operation requires `calendar:access` but the two `anonymous` ones, which the token
alone admits. The request and response shapes are in the
contract, [`openapi/calendar.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/calendar.yaml),
and in the API reference generated from it.
