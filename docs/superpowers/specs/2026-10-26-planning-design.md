# Planning — allocations, requests and assignments (phase 1)

The first phase of the `planning` module (Norwegian **Bemanning**). Research:
`docs/superpowers/research/2026-10-26-planning-module.md` ("N", cited by section; its
§8 holds the readings the owner is asked to confirm) over the Staff research
`docs/superpowers/research/2026-10-11-staff-module.md` ("R"). Revision 5 adds §13's amendments 65–93 (delivery C); revision 2: revision 1
was the design before review; the review of 2026-10-26 found the capacity read leaking
absence detail Staff withholds from colleagues and "me" resting on a permission an
employee does not hold, and this revision answers both (S6, S8) and the rest of its
findings. Code references are against `main` at `9c4f1edd`; `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`, `boundaries.md` the page
`docs/src/content/docs/en/contributing/module-boundaries.md`, "the Staff reference"
`docs/src/content/docs/en/reference/staff.md`, "the Projects reference"
`docs/src/content/docs/en/reference/projects.md`, and the Staff competence design
`docs/superpowers/specs/2026-10-25-staff-competence-design.md` ("C", its decisions
cited as C S*n*).

## 1. Purpose

Let the company book its people's time on its projects and see the plan whole:

- a planner **allocates** a person to a project over a period, in hours a week or a
  share of their position, **tentative** or **confirmed**, and the person is told when
  it is confirmed;
- the **capacity** of each person — the hours of their position, less the days they
  are away and the public holidays — is computed once from Staff's facts, and every
  view reads it: the **board** of people against weeks, the **project's plan**, the
  **person's timeline**, the **unbooked** list;
- a project's need for a person not yet named is a **request** with competence
  requirements, open until filled or withdrawn; its **candidates** are the people who
  hold the competences, have the hours free and whom nothing bars, sorted by how the
  booking fits what they want — a fit Staff scores, never the wishes themselves;
- **intake** answers, for the projects not yet started, whether each need is covered,
  partly covered or uncovered, and by whom;
- an allocation that is also a contract with the customer carries its **terms** — the
  notice period, when notice was given, the minimum duration, the handover, the
  non-compete — and derives the **earliest release** and the **handover start**, which
  the timeline shows and the matcher honours;
- a person says, without a word of free text, that they **wish** off a project from a
  date or wish for something new, and the planners read it;
- the statutory and contractual limits are **warnings an installation can make hard**,
  never built-in refusals (R §6.7, the owner's decision) — the one exception the owner
  named is the non-compete, a hard filter (S17).

Shifts, rostering and trading are a later phase (N §3). The module is its own, reads
Projects through `contracts.ProjectDirectory` and Staff through `contracts.Workforce`,
and is the first to consume two business modules at once. Three deliveries, three pull
requests, **one migration each**, each opened against `main` after the previous landed
(the number assigned when the delivery's first task lands; `00071` is the highest
today):

- **A — The plan**: the module, its permissions and settings; allocations, tentative
  and confirmed, with the confirm mail; the capacity calculation and the three reads
  Staff adds for it; the listing Projects adds; the board, the project plan, the
  person timeline, My planning; the unbooked list; the events; the dashboard card and
  its items; the Planning tab on Staff's person page through a slot Staff's package
  opens; the docs.
- **B — Requests and intake**: requests with requirements; candidates — competence,
  availability, fit (`PreferenceFits` joins the Workforce contract here); fill; the
  intake view; the request items; the Planning tab on the project page.
- **C — Assignments and wishes**: the terms, the earliest release and the handover
  start; the non-compete and the release in the matcher and on writes; wishes, seen and
  resolved, with their mail, and their place in the matcher; the calendar source.

## 2. Decisions

### The booking

**S1 — One booking table (N reading 1).** `planning.allocations`: a **person** (Staff's
person id, opaque), a **project** (Projects' id, opaque), a period `from_date`..
`to_date` of civil dates, a load, a status, a title and a note. No table of
assignments beside it: an allocation that is a contract carries **terms** (S14), and
the interface calls it an *assignment* / *oppdrag*. Nothing of the person or the
project is copied — no name, no code: they are read through the contracts on every
response, so the module stores nothing about a person but ids, dates and figures.

**S2 — The load is stored as typed (N reading 4).** `load_unit` is `percent` (of the
person's position, more than 0 and at most 100, two decimals) or `hours_per_week` (more
than 0 and at most 80, two decimals); `load` the number. Per day the row books
`hours_per_week / 5` on a day with capacity, or `percent × capacity(day)`; nothing on
a day without (S6). A percentage follows the FTE when it changes; hours do not. The
installation's `defaultLoadUnit` (S20) pre-fills the form and nothing else.

**S3 — Tentative or confirmed, on the row.** `status` is `tentative` or `confirmed`. A
tentative row is shown apart (hatched), weighs nothing in the headline figures (load,
unbooked, overbooked, free) and is summed on its own; a confirmed one is the plan. A
row on a project whose status is `planned` defaults to tentative, on any other to
confirmed (N reading 3); the planner may set either on any project. **Confirming** is
its own operation (`POST /allocations/{id}/confirm`), because it is the moment the
person is told (S19) — Odoo's publish (N §2.1); confirming a confirmed row answers it
as it is. A confirmed row set back to tentative is a change through `PUT`, and the
person is told of that too.

**S4 — The period.** `to_date` on or after `from_date` (a field error on `toDate`), at
most 1096 days (three years; `period_too_long`, a field error on `toDate`). The period
must lie inside the person's employment days as Staff answers them for the window (S8):
a day outside is `outside_employment` (409) — a booking of someone not employed is an
error, not a warning, and an open-ended employment bounds nothing. A person Staff does
not answer is a field error on `personId`; a project the directory does not resolve, a
field error on `projectId`. A period outside the project's `startDate`..`endDate`, when
the project has them, is the warning `outside_project_dates`. A project in status
`completed` or `cancelled` takes no new row and no change but deletion
(`project_closed`, 409); its rows stay and read with the project's status.

**S5 — Title and note.** `title` at most 100 characters, the role as the project names
it ("Frontend developer", "Prosjektleder"), optional, read by `planning:view`; `note` at
most 500, optional, **read and written by `planning:manage` alone** — a sentence about
a person drifts (N reading 10), so the note is the planners'. No other free text
anywhere on a person's row.

### Capacity

**S6 — One calculation (N §4).** For a person and a civil day:

| Figure | Rule |
| --- | --- |
| `capacity` | 0 on Saturday, Sunday and a public holiday of the platform calendar (`internal/holidays`); else `hoursPerWeek / 5`, where `hoursPerWeek` is the sum over the person's employments in force that day of the full position's hours × the FTE in force (Staff's terms), as the Workforce contract answers it (S8). |
| `away` | true when the day lies in one of the person's **away spans** — the approved and registered absences of whole or part days, coalesced into spans and clipped to the window exactly as Staff's calendar answers a colleague (the Staff reference "Absences"), with no share, no hours and no type. A day away is wholly away: a 40 % absence, a graded sykmelding and an hour's appointment each count the day as away, because the share is a fact Staff keeps from colleagues (reading 1). |
| `available` | `capacity` when not away, else 0 |
| `booked` | Σ over **confirmed** allocations covering the day of the row's hours that day (S2) |
| `tentative` | the same over tentative rows |
| `free` | `available − booked`, which may be negative |

Per **week** (Monday to Sunday) each figure is the sum of its days; `load %` is
`booked / capacity` over the week, absent when the week's capacity is 0. **Absence
never shrinks a booking** (N §4): a week away under a 100 % allocation is a conflict
the board shows red, not a silent re-plan. The calculation is one pure function
(`capacity.go`) over the segments, the spans, the holidays and the rows; no handler
sums hours of its own.

**S7 — Overbooking is a warning, hard by a setting (N reading 5).** A write that would
make any week's confirmed `booked` exceed its `capacity` (not its `available`: being
away is a conflict of its own) answers the warning `overbooked` naming the weeks, and
is written — unless `overbooking` (S20) is `refuse`, when a write that makes a week's
excess **grow** is `overbooked` (409) and nothing is written; a write that keeps or
shrinks an excess already there is allowed with the warning, so a plan can be mended
one row at a time. A week whose `booked` exceeds `available` but not `capacity` is the
warning `booked_while_away`, never a refusal. Both are judged on confirmed rows alone,
under the person's lock (S23), against capacity read before the transaction.

**S8 — Staff answers capacity as derived figures (N readings 6, 7, 8).**
`contracts.Workforce` gains, in delivery A, three reads. The gate is **`staff:view`**,
as `HeldCompetences`' is — `staff:manage` alone answers nothing — and the **Colleague
rule widens** for `People`, `Capacity` and `HeldCompetences` from "active today" to
**in employment on today or on a later day of the window** (`HeldCompetences`: on `on`,
when `on` is today or later; a past `on` keeps today's rule). So next month's starter
is answered and yesterday's leaver is not, whatever window is asked (N reading 7). The
window is bounded: `from` no earlier than the Monday thirteen weeks before today, `to`
no later than three years after today, else an error; the bound keeps a past window
from listing every leaver.

```go
// People answers the people in employment on today or a later day of
// from..to, as the caller may see them: the register's work group, never a
// private field. UserID is the user the person's self-service link names in a
// state that gives that user Self (the Staff reference "Field groups and
// audiences"), nil otherwise — for mail, and for nothing else. DepartmentID
// and DepartmentName are the person's place today, or on their first day in
// employment inside the window when that is later.
People(ctx context.Context, from, to time.Time) ([]PersonEntry, error)

type PersonEntry struct {
    ID             int64
    DisplayName    string
    EmployeeNumber string
    DepartmentID   *int64
    DepartmentName string
    UserID         *uuid.UUID
    Employed       []DateSpan // the days inside from..to the person is in employment, merged
}

// Capacity answers, per person, the hours a week in force over from..to and
// the days away — the approved and registered absences coalesced into spans
// and clipped to the window, as the calendar answers a colleague: no share, no
// hours, no type, no status. Never a terms row.
Capacity(ctx context.Context, from, to time.Time, personIDs []int64) (map[int64]PersonCapacity, error)

type PersonCapacity struct {
    Segments []CapacitySegment // From, To, HoursPerWeek: consecutive, non-overlapping, inside employment
    Away     []DateSpan        // whole-day spans, coalesced and clipped
}

// Self answers the caller's own person — the one whose self-service link
// names the caller in a state that gives them Self, under Staff's own rule —
// or (nil, nil). It needs staff:access and nothing more: the person's own
// record is theirs, as GET /me is.
Self(ctx context.Context) (*PersonEntry, error)
```

`People` and `Capacity` answer **derived figures**: `HoursPerWeek` is the position's
hours × the FTE, a number a colleague can already compute from the FTE they read and
the scheme's default, accepted as reading 2. The module calls each through an accessor
in its `contractscalls.go`, with the request's context, never inside a transaction, and
never restates Staff's rule: who is Self, who is a colleague and who is in employment
are Staff's answers.

**S9 — Projects answers a listing (N §6.1).** `contracts.ProjectDirectory` gains, in
delivery A, `ProjectsByStatus(ctx, statuses []string) ([]ProjectEntry, error)` — every
project in any of the statuses, by code, at most `contracts.MaxDirectoryProjects`
(1000; a consumer that gets exactly that many must assume there are more) — and
`ProjectEntry` gains `StartDate` and `EndDate` (`*time.Time`, civil dates). The read is
**consumer-gated**, declared so in its doc comment as `ProjectActuals` is: Projects
performs no authorization, and the module's own permission decides who sees the list.
The module surfaces of an entry its id, code, name, customer id and name (through
`contracts.CustomerDirectory`, which Projects requires and so is always there), status
and dates — never `Currency` or `DefaultBillRate`. **A `planning:view` holder thereby
learns every project's existence**, bids in `planned` included, which Projects hides
from a user with no role and no `projects:view-all`; that is what a plan is, and it is
why `planning:view` is sensitive (§5, reading 3).

### The views

**S10 — The board.** `GET /board?from&to&groupBy&departmentId&competenceId&projectId
&includeTentative&includePlanned` answers at most 53 weeks inside S8's bound: the weeks,
the people of the window (S8, filtered by department, or by holding a competence on
`from` through `HeldCompetences`), and per person per week the figures of S6, with the
allocations touching the window for the bars. `groupBy=project` answers the same
figures per project row, the people under it. `includePlanned=false` leaves out every
row on a `planned` project — Runn's scenario switch (N §2.3), the answer to "can we
take A and B" read against "what is firm". `planning:view`.

**S11 — The project's plan.** `GET /projects/{projectId}/plan?from&to`: the entry
(S9), its allocations and its requests in the window, and per week the project's
booked and tentative hours and its **open demand** — the sum of its open requests'
hours a week (S16). `planning:view`.

**S12 — The person's timeline.** `GET /people/{personId}/timeline?from&to`, forward by
default (today to today plus 26 weeks, at most 104 inside S8's bound): the employment
spans and the end when known, the away spans, each allocation — with its earliest
release and handover start once C lands (S15), and its days past the employment's end
flagged `beyond_employment` — the open wishes (C; to `planning:manage` and the person),
and each held **certificate whose `validTo` falls inside an allocation** of the window
(`HeldCompetences` on `from`), flagged `certificate_expires` — the owner's "certificates
against planned work" (R §6.7). `planning:view` reads anyone's; a caller holding
`planning:access` alone reads their own, the person `Self` answers, with its
**confirmed** rows only (S19).

**S13 — The unbooked list.** `GET /unbooked`: every person in employment within the
horizon (today to today plus `unbookedHorizonWeeks`) whose confirmed `booked / available`
over the horizon is under `unbookedThresholdPercent`, with the load, the free hours, and
`rollOff` — the last day of their latest confirmed allocation ending inside the
horizon, absent when they have none — by roll-off then load. A person with no
available hours in the horizon (away throughout, or starting after it) is not listed:
they are not unbooked, they are away. `planning:view`. It is a view, not a digest (N
reading 12).

### Assignments

**S14 — Terms (N §5).** `planning.allocation_terms`, one row to an allocation,
`planning:manage`'s to read and write (`PUT /allocations/{id}/terms` with `If-Match`,
`DELETE`): the notice as a count and a unit (`days`, `weeks`, `months`; both or
neither), `noticeGivenOn`, `minimumUntil`, `replacementNeedsApproval`, `handoverDays` (0
to 90), `nonCompeteUntil` with `nonCompeteCustomerIds` (Customers' ids, opaque, at most
20; the until date required when any id is given; each id read from the customer
directory on write, unknown a field error), and a `reference` of at most 200
characters. Terms may be set on a tentative row and **bind once the row is
confirmed** (reading 8): a non-compete, a release and a handover are in force only from
a confirmed row. A `planning:view` holder reads of the terms the two derived dates
(S15) and nothing else.

**S15 — The derived dates.** On read, never stored: the **earliest release** is the
latest of the row's `to_date`, `minimumUntil`, and `noticeGivenOn` plus the notice
period when notice is given — the `to_date` alone when no term binds; the **handover
start** is the earliest release less `handoverDays` when set, the day a successor
should begin so the two overlap. The timeline shows both; the matcher uses the first
as the earliest start elsewhere and proposes the second as a successor's start (S17).

### Requests and the matcher

**S16 — A request is the placeholder (N reading 2).** `planning.requests`: a project,
a `title` (required, at most 100), a period as S4 shapes it, a load in **hours a week
only** (more than 0, at most 80 — a request has no person whose position a percentage
could be read against; reading 4), a `note` (500, `planning:manage`'s as S5), a status
`open`, `filled` or `withdrawn`, and **requirements** (`planning.request_requirements`,
at most 10): a competence (Staff's id, opaque, read from `Competences` on write —
unknown or archived is a field error on the requirement's `competenceId`) and a
`minLevel` (0 to 4, only on a competence with a level, else a field error). A
requirement is met by a person who **holds** the competence on the request's first day
as `HeldCompetences` answers it — a verified and valid certificate, a declared or
verified skill at or above the level — so a certificate requirement needs no flag of
its own: a certificate not held is not met, and one held but expiring before the
request's last day is the flag `certificate_expires` (S17). An open request is the
project's demand on the board, the plan and the intake; a project in `completed` or
`cancelled` takes none (`project_closed`, 409). **Filling** (`POST /requests/{id}/fill`
with `personId` and an optional `status`) creates the allocation from the request's
project, period, load (as `hours_per_week`) and title under S4–S7 and S17's refusal,
names the request on the row (`request_id`), and sets the request `filled`; **a request
is `filled` exactly while a row names it**: deleting that row reopens the request in
the same transaction (`request_reopened`); changing the row leaves it filled. A fill of
a request not open is `request_not_open` (409). `withdraw` closes an open request; a
withdrawn request is not reopened. `planning:manage` writes; `planning:view` reads.

**S17 — Candidates (`GET /requests/{id}/candidates?includePartial`).** The people in
employment through the whole period (S8), each judged:

1. **Competence**: every requirement met (S16); one held but expiring before the
   request's last day is the flag `certificate_expires`; one not met **excludes** the
   person — unless `includePartial`, when they are listed with `missing` naming the
   keys.
2. **Availability**: `free` over the period at or above the request's hours is `fits`;
   less is `partial` with the free hours; never excluded.
3. **Non-compete** (C): a **non-compete in force** — a confirmed row of the person
   whose terms name the request's project's customer with `nonCompeteUntil` on or after
   the request's first day — **excludes** them: the one hard filter the owner named
   (R §6.7). The same rule **refuses a write**: a `POST`, a `PUT`, a `fill` or a
   `confirm` that would place a confirmed row of the person on such a customer's
   project is `non_compete` (409); a tentative row is written with the warning
   `non_compete`.
4. **Release** (C): a confirmed row of the person whose earliest release (S15) is after
   the request's first day is the flag `release_after_start` with the date, and its
   handover start as `handover_from`; a terms row with `replacementNeedsApproval` on it
   adds `needs_customer_approval`; not excluded — the planner may negotiate.
5. **Wishes** (C, reading 7): an open `new` wish, or an `off` wish on the person's
   current project (below), with `fromDate` on or before the request's first day, is
   the flag `wishes`, the kind and date only.
6. **Fit**: `Workforce.PreferenceFits(ctx, personIDs, Booking{From, To, HoursPerWeek})`
   — one call for every candidate, the booking's hours the request's — answers per
   person a score 0–100 and the kinds lowered, never a value (the Staff reference "The
   matching contract's Staff side", with the shape amended in reading 6). A booking
   field left nil contradicts nothing; `max_hours_per_week` is judged on this booking's
   hours alone (reading 6).

Sorted: `fits` first; then `wishes` first; then fit score descending; then free hours
descending; then the **longest on their current project** first (the fairness signal,
R §6.7) — the **current project** is the project of the person's confirmed row covering
today with the earliest `from_date`, and the measure is the days since that
`from_date`. Each candidate carries its figures, its flags and its reasons, so the
planner sees why. `planning:manage`.

**S18 — Intake (`GET /intake`).** Every project in status `planned` (S9) with its open
requests, each **covered** (filled, or at least one candidate `fits` with no flag and
nothing missing), **partly covered** (candidates only partial or flagged) or
**uncovered**; `key_person` when exactly one candidate covers it (R §6.7 "key-person
risk"); and the project's requested, tentative and confirmed hours a week. `on-hold`
projects are not the pipeline (reading 10). `planning:view` reads it; the candidates
behind a request are `planning:manage`'s (S17).

### The person's side

**S19 — The person sees confirmed rows only.** My planning (`GET /me`) answers the
caller's person (`Self`; `no_person`, 404, when Staff answers none), their confirmed
allocations forward with the two derived dates and the title (never the terms, the note
or the non-compete's customers), and their wishes (C). Tentative rows are the planner's
draft and are not shown (N §2.7: Resource Guru, Productive, Odoo). The person is
**mailed** through `UserDirectory.Notify` to `PersonEntry.UserID`, after the commit and
outside any transaction, when a row of theirs is confirmed, when a confirmed row's
period, load or project changes, when it is deleted, and when it is set back to
tentative — in their own language, naming the project's code and name and the dates,
never the note. A person with no `UserID` is never mailed and that is not an error.

**S20 — Settings.** `planning.settings`, one row with a `revision`, `planning:manage`
(`GET`/`PUT /settings` with `If-Match`), every field left out kept:

| Field | Default | Rule |
| --- | --- | --- |
| `overbooking` | `warn` | `warn` or `refuse` (S7) |
| `defaultLoadUnit` | `percent` | `percent` or `hours_per_week` |
| `unbookedThresholdPercent` | 50 | 1 to 100 |
| `unbookedHorizonWeeks` | 6 | 1 to 52 |
| `requestLeadDays` | 14 | 0 to 365: an open request starting within it is an item (S22) |
| `endingLeadWeeks` | 4 | 1 to 26: a confirmed row ending within it with no follow-on is an item |

**S21 — Wishes (N readings 9, 10).** `planning.wishes`: the person's, written on My
planning (`POST /me/wishes`, `DELETE /me/wishes/{id}`): a `kind` — `off` (wants off a
named project, `projectId` required, one the person has a confirmed row on today or
later; else `wish_no_booking`, 409) or `new` (wants something new, no project) — and a
`fromDate` on or after today. **No free text.** At most five open wishes a person
(`wish_limit`, 409); a second `off` on the same project is `wish_exists` (409).
Planners (`planning:manage`) list them (`GET /wishes`), stamp **seen**
(`POST /wishes/{id}/seen`, as Staff's one-to-one stamps) and **resolve**
(`POST /wishes/{id}/resolve`); the person may withdraw their own. A resolved or
withdrawn wish is **deleted** — the event keeps the kind, the project id and the date,
never more — so the table holds open wishes only. A new wish mails every
`planning:manage` holder (`PermissionHolders`), once, after the commit. The person's
manager does not read wishes in this phase (N reading 9). A wish's events are the
planners' (S23); the person sees their open wishes on My planning and nothing of the
closed ones.

### The record and the rest

**S22 — Attention items**, computed on read (`GET /stats/attention`), each to its
audience, each opening the screen that answers it, each arriving with the delivery that
builds what it counts:

| Key | Audience | When | Delivery |
| --- | --- | --- | --- |
| `unbooked` | `planning:view` | people under the threshold within the horizon (S13) | A |
| `overbooked` | `planning:view` | people with a week in the horizon whose confirmed `booked` exceeds `capacity` | A |
| `endingWithoutFollowOn` | `planning:manage` | confirmed rows ending within `endingLeadWeeks` where the person has no confirmed row starting on or before that end plus seven days | A |
| `certificatesExpiring` | `planning:view` | held certificates expiring inside a confirmed row within the horizon (S12) | A |
| `requestsOpen` | `planning:manage` | open requests starting within `requestLeadDays`, or already started | B |
| `wishesUnseen` | `planning:manage` | wishes without a seen stamp | C |

`GET /stats` answers the figures behind them for the dashboard card: people planned
this week, confirmed load this week with tentative named on a second line, open
requests, unbooked. No worker (N reading 12).

**S23 — Locks and events.** Every write of an allocation takes an advisory
transaction lock on the **person** (`planning`, `person_id`); of a request, on the
**project**; of a wish, on the person. A write that needs both — a fill, a `PUT` that
moves a row to another project — takes the **project's first, then the person's**; a
`PUT` that moves a row to another person takes both persons' in ascending id. Capacity,
the people, the project entry and the competences are read **before** the transaction
(rule: no contract call inside one); the overbooking and non-compete judgements read
the person's confirmed rows under the lock. Every write is one row in
`planning.events` — `allocation_created`, `allocation_changed`,
`allocation_confirmed`, `allocation_unconfirmed`, `allocation_deleted`, `terms_set`,
`terms_cleared`, `request_created`, `request_changed`, `request_filled`,
`request_reopened`, `request_withdrawn`, `wish_created`, `wish_seen`, `wish_resolved`,
`wish_withdrawn` — with the changed fields before and after and the **actor
snapshot** (`{kind, displayName, userId}` of the caller who wrote, as Staff keeps it;
the person's name is never written). `GET /events?personId|projectId|allocationId` is
`planning:view`'s; a wish's events are `planning:manage`'s.

**S24 — The calendar (C).** The module fills `CalendarSource` with one kind,
`planning.allocation`: the caller's own **confirmed** allocations overlapping the
window, the caller's person found through `Self` (which answers under `AsUser` for the
feed, as Staff's own source does), all-day items over `from_date`..`to_date`, titled
with the project's code and name, subtitled with the load, linking to `/planning/me`.
It needs `planning:access` and a person; without either it answers nothing. No
colleague's row is projected.

**S25 — Dependencies and the switch.** `planning requires projects` and `planning
requires staff` are checked at startup in `internal/config` beside `time requires
projects` (N reading 14); Customers comes with Projects. `Deps.Projects`,
`Deps.Workforce` and `Deps.Directory` are therefore never nil in a running `planning`;
the module still refuses `dependency_unavailable` (503) rather than panic if one is.
Calendar is optional the other way: the source is collected only when `calendar` is on.
Nothing of this module is read by another in this phase; the host composes its tabs
from its API (§7).

**S26 — Personal data (reading 9).** The module stores about a person ids, dates,
hours and the kind of a wish — no name, no text, no health fact — and the planners'
names as actors. Staff's personal-data export and erase do not reach it in this phase:
after an erase the rows keep an id Staff no longer resolves and read as *Erased person*,
and an erased person's open wishes, a project id and a date, are deleted by the next
`People`-less read of them — the module deletes an open wish whose person `People` no
longer answers for the planners' list. A many-provider slot through which Staff
collects another module's rows about a person for its export and its erase, shaped on
`CalendarSource`, is the roadmap's entry for phase 2.

## 3. Data model (schema `planning`)

One migration per delivery, each owning only `planning`; no foreign key leaves the
schema (rule 4); `person_id`, `project_id`, `competence_id`, `customer_id`s and user ids
are opaque.

### 3.1 `planning.settings` (A)

`id smallint PK CHECK (id = 1)`, `overbooking varchar(10)`, `default_load_unit
varchar(16)`, `unbooked_threshold_percent smallint`, `unbooked_horizon_weeks smallint`,
`request_lead_days smallint`, `ending_lead_weeks smallint`, `revision integer NOT NULL
DEFAULT 1`, `updated_at`, `updated_by_user_id uuid`. Seeded with S20's defaults.

### 3.2 `planning.allocations` (A)

```
id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY
person_id          bigint        NOT NULL
project_id         integer       NOT NULL
from_date          date          NOT NULL
to_date            date          NOT NULL CHECK (to_date >= from_date)
load_unit          varchar(16)   NOT NULL CHECK (load_unit IN ('percent','hours_per_week'))
load               numeric(5,2)  NOT NULL CHECK (load > 0)
status             varchar(10)   NOT NULL CHECK (status IN ('tentative','confirmed'))
title              varchar(100)
note               varchar(500)
confirmed_at       timestamptz
revision           integer       NOT NULL DEFAULT 1
created_at, created_by_user_id, updated_at, updated_by_user_id
INDEX (person_id, from_date, to_date); INDEX (project_id, from_date, to_date)
```

Delivery B's migration adds `request_id bigint REFERENCES planning.requests(id)` with
a unique partial index where not null: at most one row fills a request, and the request
is `filled` exactly while that row exists (S16, kept by the code in one transaction;
the FK is `ON DELETE RESTRICT`, since the row's deletion is what reopens the request
and must run through the module).

### 3.3 `planning.allocation_terms` (C)

`allocation_id bigint PK REFERENCES planning.allocations(id) ON DELETE CASCADE`,
`notice_count smallint`, `notice_unit varchar(6)`, `notice_given_on date`,
`minimum_until date`, `replacement_needs_approval boolean NOT NULL DEFAULT false`,
`handover_days smallint CHECK (0..90)`, `non_compete_until date`,
`non_compete_customer_ids integer[] NOT NULL DEFAULT '{}'`, `reference varchar(200)`,
`revision integer NOT NULL DEFAULT 1`, `updated_at`, `updated_by_user_id`; a check that
`notice_count` and `notice_unit` are both or neither set, and that the ids are empty
when `non_compete_until` is null.

### 3.4 `planning.requests` and `planning.request_requirements` (B)

`requests`: `id`, `project_id integer NOT NULL`, `title varchar(100) NOT NULL`,
`from_date`, `to_date` (checked as 3.2), `hours_per_week numeric(5,2) NOT NULL CHECK (>
0)`, `status varchar(10)` in `open`, `filled`, `withdrawn`, `note varchar(500)`,
`revision`, the stamps; index `(project_id, status)`. `request_requirements`: `id`,
`request_id` (FK, cascade), `competence_id bigint NOT NULL`, `min_level smallint CHECK
(0..4)`, unique `(request_id, competence_id)`.

### 3.5 `planning.wishes` (C)

`id`, `person_id bigint NOT NULL`, `kind varchar(10)` in `off`, `new`, `project_id
integer` (required when `off`, null when `new`, by check), `from_date date NOT NULL`,
`created_at`, `seen_at timestamptz`, `seen_by_user_id uuid`; unique `(person_id,
project_id)` where `kind = 'off'`. Resolved and withdrawn rows are deleted (S21).

### 3.6 `planning.events` (A)

`id`, `entity_kind varchar(12)` (`allocation`, `request`, `wish`), `entity_id bigint`,
`person_id bigint`, `project_id integer`, `kind varchar(32)`, `payload jsonb`, `actor
jsonb NOT NULL`, `occurred_at timestamptz NOT NULL`; indexes on `(person_id,
occurred_at)`, `(project_id, occurred_at)` and `(entity_kind, entity_id)`.

## 4. Rules

1. **Dates are civil** and compared at midnight UTC; "today" is `BusinessCalendar.Today`
   (the platform's time-zones design), computed once per request.
2. **Capacity is computed in one place** (S6), with a table test per figure.
3. **Contract calls precede the transaction** (S23). A change in Staff between the read
   and the write is accepted: capacity is a warning's input, employment a 409's — and a
   person ended meanwhile is caught by the next read.
4. **A missing person or project is an id with no name**: a person Staff no longer
   answers (erased, left, or not visible to this caller) reads as `{id, displayName:
   null}` and the interface shows *Erased person* or *A colleague*; a project the
   directory does not resolve reads by its id. Nothing is deleted for it but an erased
   person's open wishes (S26).
5. **Rows outlive their inputs.** An employment ended after a row was written leaves
   the row; the timeline flags its days past the end `beyond_employment`. A cancelled
   project keeps its rows, which weigh as they are until the planner deletes them.
6. **Writes carry warnings.** A write's answer is the row and `warnings: [{code,
   weeks|detail}]` — `overbooked`, `booked_while_away`, `outside_project_dates`,
   `non_compete` (on a tentative row) — so the planner sees the consequence without a
   second call. Flags on a read (`certificate_expires`, `beyond_employment`,
   `release_after_start`, `needs_customer_approval`, `wishes`, `key_person`) are
   snake_case like the warnings.
7. **Refusals.** A field's fault is a field error (400) on the field: `toDate`
   (`period_too_long`, or before `fromDate`), `load`, `personId`, `projectId`, a
   requirement's `competenceId` or `minLevel`, a term's customer id. A business rule is
   `{code}` with 409: `outside_employment`, `project_closed`, `overbooked` (under
   `refuse`), `non_compete`, `request_not_open`, `wish_exists`, `wish_limit`,
   `wish_no_booking`. `no_person` is 404, `dependency_unavailable` 503, and a caller
   without the operation's permission gets the access layer's uniform 403.
8. **Revisions.** `PUT` on an allocation, its terms, a request and the settings takes
   `If-Match` with the row's revision, as Staff's terms do; `confirm`, `fill`,
   `withdraw`, `seen` and `resolve` do not.

## 5. Permissions and visibility

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `planning:access` | Use Planning | The app: My planning — the caller's own confirmed bookings and wishes — and, from C, their allocations on the Calendar. **Every operation requires it.** | no |
| `planning:view` | See the plan | The board, every project's plan, every person's timeline with confirmed and tentative rows, titles and the derived dates, the requests and the intake, the unbooked list, the events, the stats — and, with them, every project's code, name, customer, status and dates (S9). | yes |
| `planning:manage` | Plan | Write allocations and their terms, requests and their fill, the settings; read notes, candidates, the terms whole, the wishes; the `manage` items. | yes |

All three are in the category "Planning" and delegable; the Owner holds them through
the wildcard. `planning:view` is sensitive because it reveals the whole project
portfolio, bids included, which Projects otherwise reserves for `projects:view-all`
(reading 3); `planning:manage` because it reads wishes, the person's voice (C S9's
reasoning), and the terms of a customer contract. A planner also needs
**`staff:view`**, since the Workforce reads answer nothing without it (C reading 9):
`GET /meta` answers `workforceVisible: false` when `People` answered nobody for a
caller who may see the plan, and the app explains it, so an empty board is never
mistaken for an empty plan. The audiences:

| Reads | `access` (self) | `view` | `manage` |
| --- | --- | --- | --- |
| own confirmed rows with title and derived dates, own open wishes | ✓ | ✓ | ✓ |
| anyone's rows, tentative included; derived dates; projects | – | ✓ | ✓ |
| notes; terms whole; candidates; wishes; settings | – | – | ✓ |

`GET /meta` answers `canView`, `canManage`, `hasPerson`, `workforceVisible`, the
settings for `manage`, and the load units.

## 6. API (`/api/v1/planning`, contract `openapi/planning.yaml`)

| Operation | Permission | Notes |
| --- | --- | --- |
| `GET /meta` | access | §5 |
| `GET` / `PUT /settings` | manage | S20, `If-Match` |
| `GET /projects?status=&q=` | view | S9, plannable projects by code; `status` defaults to `planned,active,on-hold` |
| `GET /people?from&to&q&departmentId` | view | S8's people of a window, for pickers |
| `GET /board` | view | S10 |
| `GET /projects/{projectId}/plan` | view | S11 |
| `GET /people/{personId}/timeline` | view, or self | S12 |
| `GET /unbooked` | view | S13 |
| `GET /intake` | view | S18 (B) |
| `GET /allocations?personId&projectId&from&to&status` | view | at most 500 rows |
| `POST /allocations` | manage | S1–S7, S17.3; `status` defaults by the project |
| `GET` / `PUT` / `DELETE /allocations/{id}` | view / manage / manage | `PUT` with `If-Match` |
| `POST /allocations/{id}/confirm` | manage | S3, S17.3, S19 |
| `PUT` / `DELETE /allocations/{id}/terms` | manage | S14 (C), `PUT` with `If-Match` |
| `GET` / `POST /requests` | view / manage | S16 (B) |
| `GET` / `PUT /requests/{id}` | view / manage | `PUT` with `If-Match` |
| `POST /requests/{id}/withdraw` | manage | |
| `GET /requests/{id}/candidates?includePartial` | manage | S17 |
| `POST /requests/{id}/fill` | manage | S16 |
| `GET /me` | access | S19 |
| `POST /me/wishes`, `DELETE /me/wishes/{id}` | access | S21 (C) |
| `GET /wishes`, `POST /wishes/{id}/seen`, `POST /wishes/{id}/resolve` | manage | S21 (C) |
| `GET /events` | view (wishes: manage) | S23 |
| `GET /stats`, `GET /stats/attention` | view / access | S22; items shaped by audience |

Every operation declares its rule in `x-vantigo-access`; the generated client and the
contract tests follow from the yaml, as for every module.

## 7. Frontend (`@vantigo/planning-ui`, the host's `routes/planning`)

A new app **Planning** / **Bemanning**, tile gated on `planning:access`; the sidebar
entries gated as the table in §5 says.

- **Board** (`/planning`, `planning:view`): a range picker (twelve weeks from this
  week by default), *People* or *Projects* grouping, filters by department, competence
  and project, *Show tentative* and *Include pipeline* switches (S10). People as rows,
  weeks as columns; a cell shows the load with a colour scale — free, fine, full, over —
  and a hatched share for tentative; a conflict with absence reads red with a tooltip
  naming the days away, never a type or a share. A row expands to its allocations as
  bars. Clicking a cell opens the **allocation sheet**: person, project (a picker over
  `GET /projects`), period, load with its unit, status, title, and for `manage` the
  note; the sheet shows the warnings on save.
- **Project plan** (`/planning/projects/$id`): the project's bars and its open
  requests on one timeline, the demand per week, *New allocation*, *New request*; a
  request opens its **candidates** panel (B) — the list with the fit, the free hours,
  the flags and the missing competences, *Show partial matches*, and *Fill* as
  tentative or confirmed.
- **Person timeline** (`/planning/people/$id`): the forward view of S12 — employment,
  away spans, bars with the earliest release and handover start marked (C), the
  expiring certificate badge, the open wishes for those who may see them (C).
- **Requests** (`/planning/requests`, B): open, filled and withdrawn with filters;
  **Intake** (`/planning/intake`, B): the planned projects with each request's
  coverage and the key-person badge; **Unbooked** (`/planning/unbooked`): the list of
  S13 with the roll-off date.
- **My planning** (`/planning/me`, `planning:access`): my confirmed bookings as a
  list and a strip, each with its dates and load; my wishes with *I wish off this
  project from…* and *I wish for something new from…* (C), and their seen stamp.
  Phone first.
- **Settings** (`/planning/settings`, `planning:manage`): the six fields of S20 with
  the overbooking rule explained.
- **Host**: the tile, the routes and the guard; `moduleKeys` in `navigation.ts`; the
  permission labels in both languages in `catalogs/admin.ts`; the dashboard card
  (planned this week, confirmed load with tentative named, open requests, unbooked) and
  the attention items of S22; the spotlight's navigation and *New allocation*.
  **The project page** gains a **Planning** tab (B) rendered by the host from this
  package's `ProjectPlan` for `planning:view`, the way the customer page composes its
  Projects and Energy tabs in `routes/customers/-customer-detail-layout.tsx`. **The
  Staff person page** is different: its tabs are a closed list inside `@vantigo/staff-ui`
  (`apps/staff/frontend/src/pages/person.tsx:116`, the `PersonTab` type), so delivery A
  opens a **slot** there — `PersonPage` takes `extraTabs`, each a key, a label and a
  render function the host fills — and the host route `routes/staff/people.$id.tsx`
  passes a **Planning** tab from this package's `PersonTimeline` when the module is on
  and the caller holds `planning:view` or is the person. Neither Staff nor Projects
  imports this package; the host does, as it imports every app.
- **i18n**: every string in `en` and `nb`, the catalog imported in the host's
  `i18n.ts`; the parity test as in every package.

## 8. Platform, contracts and documentation changes

- **Contracts** (A): `Workforce.People`, `Capacity` and `Self` (S8) with Staff's
  implementation and tests in `srv/staff/workforce.go` and `workforce_test.go`; the
  widening of the Colleague rule to "in employment on today or a later day" for
  `HeldCompetences` too; `ProjectDirectory.ProjectsByStatus`, `MaxDirectoryProjects`
  and `ProjectEntry`'s dates (S9) with Projects' implementation and the consumer-gated
  declaration. (B): `Workforce.PreferenceFits` as the Staff reference shapes it, amended
  per reading 6, scored by Staff from `staff.person_preferences`. The fakes in
  `modtest` gain each method. **Staff's package** (A): the `extraTabs` slot on
  `PersonPage` (§7).
- **Config and scaffold** (A), `boundaries.md` "Adding a module" step by step:
  `srv/planning` with `Module()`, `businessModules`, `openapi/planning.yaml` and
  `Modules` in `internal/openapi`, `internal/openapi/gen/cfg-planning.yaml` and the two
  `//go:generate` lines, the baseline migration, the depguard set and every deny list,
  `moduleSchemas`, the known set and the default list in `internal/config` with
  `planning requires projects` and `planning requires staff`, `compose_test.go`'s
  literal; `apps/planning/frontend` with the `no-restricted-imports` block and its
  entry in every other package's deny list, the `gen-client.ts` entry and
  `api-schema.d.ts`, `moduleKeys`, `apps.ts`, the layout route, the catalogs, the
  dashboard and the spotlight.
- **Docs**, with each delivery, in the commit series of the code:
  - `reference/planning.md` (new, with `sources`), `reference/index.md`;
  - `user/planning.md` in `en` and `nb` (new), `user/index.md` (the app in the table),
    `user/staff.md` and `user/projects.md` in both languages (the Planning tab),
    `user/workspace-administration.md` where the permissions appear;
  - `admin/installation.md` in both languages (`MODULES`, the `GRANT` lines);
  - `contributing/module-boundaries.md`: the module's paragraph under "Turning a module
    off", the Workforce and ProjectDirectory additions with their classes, the first
    consumer of two business modules, the `extraTabs` slot under rule 7;
  - the Staff reference: "The Workforce contract" (the reads, the widened rule, the
    bound, `PreferenceFits` on the interface in B), "What comes next", and the person
    page's tab; the Projects reference: "Contracts for other modules";
  - `ROADMAP.md`: a **Planning** section (this phase's deliveries, then shifts, the
    forecast, the automatic request, the booking contract Staff reads for holiday
    approval, the personal-data slot of S26); the Staff section's *Staffing* paragraph
    rewritten to point to it; Projects phase 5 marked as moved here;
  - `deploy/compose/vantigo.env.example`, `deploy/compose/README.md`, `README.md`,
    `CONTRIBUTING.md`, `docs/astro.config.mjs`.

## 9. Edge cases

- **Two concurrent employments** sum their hours a week into one capacity; an
  allocation is of the person, not of an employment.
- **An FTE change mid-row**: a percentage row's hours change from that day; an hours
  row's do not, and may now overbook — the board shows it, the next write warns.
- **A row over a weekend or holiday only** books nothing and shows a zero-capacity
  week; the write is allowed with no warning.
- **A part-day absence** counts the day as away (S6): the board shows a conflict a
  manager knows to be half a day; the alternative was a share Staff keeps from
  colleagues.
- **Absence approved after a confirmed row** is a conflict on the board and in the
  timeline, never a change to the row; Staff is not told (phase 2's contract).
- **The person's employment ends** after rows were written: the days past the end are
  flagged; a new row on them is `outside_employment`.
- **A project cancelled** with rows: the rows stay and weigh; no new row, no change
  but deletion; the intake and the board show the status.
- **A filled request whose allocation is deleted** reopens; one whose allocation is
  changed stays filled with the changed row; a withdrawn one never reopens.
- **Two planners book one person at once**: the person lock serialises them; the
  second's overbooking is judged on the first's committed rows.
- **A person with no linked user** is planned like anyone and never mailed; My planning
  answers `no_person` to a user who is nobody's Self, a mismatched link included —
  Staff's rule, through `Self`.
- **An erased person's rows** read with no name and weigh nothing new: `People` does
  not list them, so they are off the board, in the project's plan by id; their open
  wishes are deleted (S26).
- **Midnight in Oslo**: "today" for the horizon, the unbooked list, the bound and the
  wish's date comes from the business calendar; dates in rows are civil and never
  shift.
- **A certificate renewed** moves `validTo`, and the expiring flag disappears on the
  next read; nothing is stored.

## 10. Testing

Backend: the capacity function per figure of S6 — segments, two employments, FTE
change, a whole day and a part day away both wholly away, a holiday, a leap week;
percent against hours rows; overbooked and booked-while-away in both settings, a
shrinking excess allowed under `refuse`; `outside_employment`, `project_closed`,
`period_too_long`, `outside_project_dates`, the field errors; the default status by
project status; confirm's mail through the mail fake in both languages, no mail on a
tentative write, no mail and no error for a person without a user; the board's figures
and filters, `includePlanned`, `groupBy`, the window bound; the timeline's flags and
the self audience's confirmed-only shaping through `Self`; the unbooked list's
threshold, horizon, roll-off, and the person away throughout left out; requests and
requirements, fill creating the row and linking, reopen on delete, withdraw never
reopening, `request_not_open`; candidates — each filter and flag, `includePartial`,
the sort order with ties, `PreferenceFits` through the Workforce fake; intake's three
coverages and `key_person`; terms' validation and the two derived dates under each
combination; the non-compete excluding and refusing, and warning on a tentative row;
wishes — kinds, `wish_limit`, `wish_exists`, `wish_no_booking`, seen, resolve deleting
the row and keeping the event, the planners' mail once, the erased person's open wish
deleted; the calendar source with and without a person and permission, and under
`AsUser`; events' payloads and actor; locks (two writers, the lock order); the
harness-wide check that no contract or mail call happens inside a transaction; the
cross-schema scan; config: `planning` without `projects` and without `staff` refused,
`MODULES=customers,projects,staff,planning` valid; contract coverage. Staff: `People`,
`Capacity` and `Self` per audience — a starter next month answered, yesterday's leaver
not, `staff:manage` alone nothing, a past window refused by the bound, `Self` for a
linked, an unlinked and a mismatched user — the spans coalesced and clipped with never a
share, a type or an hour row; `PreferenceFits` per kind and weight. Projects:
`ProjectsByStatus`, its cap and the dates.

Frontend: the board at twelve weeks with the colour scale and the hatched tentative;
the allocation sheet with warnings; the project plan and the candidates panel; the
person timeline; My planning at phone width with the wish forms; settings; catalog
parity; the dashboard items; the Staff tab through the slot and the project page tab.

## 11. Readings on the record

For the owner's verdict; each is how the design read a decision or a gap. N §8's
fourteen readings stand, with 6 and 8 amended here.

1. **A day away is wholly away** (S6): Staff shows a colleague coalesced whole-day spans
   with no share, no hours and no type, and the capacity read answers exactly that, so a
   graded sykmelding, an hour's svangerskapskontroll and a 40 % leave are each a day
   away and nothing more. The cost is a conservative figure the manager can read
   through; the alternative leaked health. This amends N reading 6.
2. **`HoursPerWeek` is a derived figure a colleague can nearly compute** from the FTE
   they already read and the scheme's default; the module accepts that it may reveal a
   scheme other than the default. The owner should confirm.
3. **`planning:view` is sensitive** because it lists every project (S9), which Projects
   reserves for `projects:view-all`; the alternative, requiring that key of every
   planner, tied the two modules' roles together. The owner should confirm.
4. **Requests are in hours a week only** (S16): a percentage needs a position to be a
   percentage of.
5. **The non-compete is a refusal on a confirmed row** (S17.3), the one hard rule, as
   the owner named it; on a tentative row it is a warning, so a planner can draft.
6. **`PreferenceFits` is a batch** — one call for every candidate — over `time.Time`
   dates at midnight UTC as every contract uses, with `HoursPerWeek *float64`; the Staff
   reference's `civil.Date` and `*int` shape is amended with B. A nil field contradicts
   nothing; `max_hours_per_week` is judged on this booking alone, since the person's
   total is the module's figure, not Staff's.
7. **Wishes reach the matcher as a flag and a sort key** (S17.5), not as a proposal
   engine: R §6.7's "the matcher proposes moves" is met by listing the willing first.
8. **Terms may be set on a tentative row and bind on confirm**; refusing until
   confirmed made a contract negotiated ahead unrecordable.
9. **Personal data: ids only, and the slot waits** (S26). The module writes no name
   and no text about a person; the export and erase slot Staff would collect is phase
   2. The owner should confirm the retention of allocations as planning data (N reading
   13).
10. **The intake reads `planned` projects only**; an `on-hold` project is not a
    pipeline, its rows stand.
11. **The person sees confirmed rows only** (S19), and is mailed on confirm, change and
    deletion — not on a tentative write.
12. **`PreferenceFits` joins the interface in delivery B**, not A, because its consumer
    is the matcher; the Staff reference's "with Staffing's first delivery" is reworded
    in A's docs to "with the delivery that matches".
13. **Overbooking is judged against capacity, not availability** (S7): being away under
    a booking is a conflict, not an over-booking, so the two warnings stay distinct.
14. **The planner needs `staff:view`** and the app says so when the contract answers
    nothing, rather than the module holding a permission of its own that implies it.
15. **Fairness is the fifth sort key**, measured as time on the current project; wishes
    granted and attractive assignments (R §6.7) are not counted in this phase.
16. **No `completed` status on a row** (Float has one): a row whose `to_date` has passed
    is history on read, and nothing is stamped.
17. **The pipeline switch is a query parameter**, not a saved scenario: Runn's scenarios
    cannot be saved either, and a saved what-if is a feature of its own.

## 12. Open questions

1. **Should a project's manager (Projects role) write allocations and requests on
    their own project without `planning:manage`** (N reading 11)? The design says no
    for this phase; a small firm may want yes.
2. **Should Staff's holiday approval see the allocations it conflicts with** in this
    phase, through an optional read contract this module provides? Listed as phase 2;
    it is the owner's "holiday against staffing" case and could be pulled forward.
3. **Should the personal-data slot (S26) come in C rather than phase 2**, so an erase
    reaches the module's rows from the start?

## 13. Amendments at revision 3 (decided while building delivery A, 2026-10-26)

1. The plan's readings 1–5 (`docs/superpowers/plans/2026-10-26-planning-plan.md`) stand
   as amendments.
2. **The person's own timeline is their confirmed rows and nothing else** (S12): the
   Workforce reads that give employment, days away and held certificates need
   `staff:view`, which a person holding `planning:access` alone does not have, and the
   module never restates Staff's rule. The self audience's `employed`, `away` and
   `flags` are empty; Staff's My page already shows the person their employment and
   absence. A caller with `planning:view` reads anyone's timeline whole.
3. **No CHECK constraints in the schema** but the settings' one-row index (plan
   reading 2): §3's CHECKs are the module's validation, answered as field errors.
4. **A missing name is `displayName: null`** (§4.4), not an absent field.
5. **When the person is mailed** (S19): whenever a row of theirs *becomes* confirmed —
   by a `POST` that creates it confirmed, a `PUT` that sets `status` to `confirmed`, or
   `confirm` — when a confirmed row's period, load or project changes, when a confirmed
   row is deleted, and when a confirmed row is set tentative. `confirm` on a confirmed
   row answers it as it is and mails nobody.
6. **`confirm` is a change** (S3, S4, S7): it is refused on a closed project
   (`project_closed`) and judged for overbooking and the non-compete like a `PUT`.
7. **`revision_conflict` is a 409** in §4.7's table, answered by every `If-Match` write
   whose revision is stale, as Staff answers it.
8. **A window outside the bound is a 400** on `from` or `to` (S8, S10, S12), the
   contract's `window_out_of_bounds`.
9. **The unbooked list's order** (S13): people with no `rollOff` first — they are free
   now — then by `rollOff` ascending, then by load ascending, then by display name.
10. **A week is `away` when any of its days is** (S10's cell), so the board can colour
    the week; the days themselves are in the timeline's spans.
11. **Nineteen operations** in A's contract, not eighteen: the plan's table counts
    `GET /allocations/{id}` beside the list.
12. **The Staff reference keeps the `PreferenceFit` shape block until B**, when
    `PreferenceFits` replaces it on the interface (reading 6); the prose already names
    the batch.
13. **`People` and `Capacity` answer only people with an employed day inside the window**
    (S8, built in Staff): a person employed today whose employments miss the window is
    left out rather than listed with nothing, since nobody can be booked outside their
    employment and listing fewer reveals nothing more. `Self` answers a linked person
    whether or not they are employed today, with the department in force today.
14. **The bound's far end is today plus thirty-six months** by Staff's month rule, and
    the near end the Monday of the week thirteen weeks before today; the gate is judged
    before the bound, so a caller without `staff:view` learns nothing from the error.
15. **A `PUT` that moves a confirmed row to another person** mails the old person
    "removed" and the new one "planned" (S19, amendment 5 did not name the case).
16. **Writes judge the Workforce bound by clipping**: Staff is asked about the weeks
    the period touches cut to the bound, so a long row stays editable; days outside the
    bound are not checked for `outside_employment`; a period with no day inside the
    bound is a field error `window_out_of_bounds` on `fromDate` or `toDate`.
17. **`GET /allocations` is a row filter without a bound**, so a past window lists
    history; the people named in it are read over the rows' window cut to the bound.
18. **The status flip is its own event** — `allocation_confirmed` /
    `allocation_unconfirmed` beside `allocation_changed`, which leaves `status` out — and
    a `PUT` that changes nothing writes nothing. The note is recorded in an event as its
    name alone, never its text, since events are `planning:view`'s.
19. **`project_closed` on a `PUT`** is judged on the old and the new project both, so a
    row cannot be moved off a closed project; `DELETE` stays allowed.
20. **Overbooking is judged only when the row is left confirmed**; moving a row off a
    person is never judged for that person, since removing a row only shrinks.
21. **`DELETE` and `confirm` carry no `If-Match`** and re-read the row under the lock;
    a row changed in between starts the operation again, three times at most.
22. **The 53- and 104-week caps** (S10, S12) are plain field errors on `to`;
    `window_out_of_bounds` is Staff's bound alone (amendment 8).
23. **`groupBy=project` on the board** answers a project row (sorted by code) followed
    by one row per person on it: the person row's capacity and available are the
    person's, its booked and tentative count that project's rows alone; the project row
    is the sum. A project the directory does not resolve gets no row; its allocations
    stay in the list.
24. **`beyond_employment`'s date is the employment's last day** and the flag fires only
    when the employment ends before the window's `to`; `certificate_expires`'s date is
    the certificate's `validTo`.
25. **Attention item shapes** (S22): `overbooked` is judged on whole weeks from this
    Monday to the horizon's last Sunday, dated the first such Monday and titled
    "name, week NN"; `certificatesExpiring` is one item per confirmed row and
    certificate, held judged on today; `endingWithoutFollowOn`'s follow-on is a
    confirmed row of the person ending after this one and starting on or before its
    end plus seven days; `unbooked` is dated the roll-off or today.
26. **`GET /people` defaults to today and the twelve weeks after**, as the contract says.
27. **An hour-unit absence is not a day away** (S6, reading 1 corrected by the server
    review): Staff shows a colleague no hour row at all, so the capacity read carries
    none and a medical appointment never shows on the board; whole and part days away
    count the day as away, as before.
28. **Hours a week are rounded to the quarter hour** in the capacity segments, since
    Staff answers them with its own hours rule (`expectedHours`): 37.5 × 33 % reads
    12.5, not 12.375.
29. **A person Staff does not answer is not mailed** (S19): the recipient comes from
    `People` as the planner sees it, so a planner without `staff:view` deleting a
    confirmed row tells nobody, and an erased person's row can still be deleted. The
    reference says mail needs the person answered.
30. **Events are filed under the row's current person and project** (S23): a `PUT`
    that moves a row records its `allocation_changed` against the new person, with the
    old one in the payload's `from`; `GET /events?personId=<old>` does not list it.
31. **The project picker's search runs after the directory's cap** (S9): at a thousand
    projects a search may miss projects past the first thousand by code; the module
    logs when the cap is reached.
32. **A missing Projects or Workforce fails the mount** rather than answering 503
    (S25): both are required at startup by config, so the 503 is a defence that cannot
    be reached in a composed server.
33. **The board's competence filter judges holding on the later of `from` and today**
    (S10), so next month's starter on a board that begins last Monday is kept.
34. **The unbooked horizon is `unbookedHorizonWeeks × 7` days from today** (S13), not
    whole calendar weeks.
35. **A `DELETE` or `confirm` whose row keeps changing** under it three times answers
    `revision_conflict` (409), not an internal error (amendment 21).
36. **`planning:manage` does not imply `planning:view`** (§5): the access rule of an
    operation lists the keys it needs and all must be held, as every module's does, so
    a planner's role carries both — the way an HR role carries `staff:view` beside
    `staff:manage` to list people. The §5 audience table reads "manage" as "view and
    manage".
37. **The board's cell says the week has days away, not which** (§7): the week figure
    carries `away` alone; the days are on the person's timeline. The cell reads red
    only when the week's confirmed `booked` exceeds its `available` (S7's
    `booked_while_away`), not for any booking in a week with a day away.
38. **No competence control on the board in A** (§7): Planning lists no competences
    and may not import Staff's picker (rule 7); the `competenceId` parameter works
    from a link. The control comes with B, which reads the catalogue for requests.
39. **My planning is a list in A**, without the strip §7 names.
40. **`GET /stats/attention` answers a bare array**, as every other module's does, not
    `{items}`; the host's shared fetch stays one shape.

## Amendments at revision 4 (decided while building delivery B, 2026-10-26)

41. The plan's readings 1–9 (`docs/superpowers/plans/2026-10-26-planning-requests.md`)
    stand as amendments.
42. **Staff's contradiction rule, per kind** (S17.6, reading 6): `weekdays` — a weekday
    of the booking not among the preferred; `time_of_day` — a part not among the
    preferred; `location` — the booking's location not among the preferred; `travel` —
    the booking's extent beyond the preferred (none < regional < national <
    international); `remote` — the booking's share other than the preferred;
    `max_consecutive_days` and `max_hours_per_week` — the booking's figure above the
    preferred. A facet left nil or empty contradicts nothing, nor does a stored value
    that does not decode; the score is floored at 0 and `Lowered` is in the catalogue's
    order. B's booking carries the period and the hours a week alone, so only
    `max_hours_per_week` can lower a candidate's score.
43. **Candidates are judged over the period cut to the bound** (amendment 16): the people
    of the clipped window, kept when employed on every day of it; `requiredHours`,
    `freeHours` and the fit's booking over the same days. A period with no day inside
    the bound is the 400 `window_out_of_bounds`, as a write answers it.
44. **A requirement is judged on the later of the request's first day and today**
    (S16 said the first day): Staff answers holding on today or later only, as the
    board's filter reads it (amendment 33).
45. **`certificate_expires` on a candidate fires when every holding that meets the
    requirement is a certificate valid to a day before the request's last**; one that
    lasts the period raises nothing. The date is the latest `validTo` among them.
46. **The fill judges no requirement**: the candidates are advice, and a person missing
    one may be booked under S4–S7.
47. **An out-of-bound request in the intake is judged with no candidates** — `uncovered`
    unless filled — rather than refusing the whole intake, which has no 400.
48. **A percent row in the intake is read on its first day inside the bound** (reading
    4): the bound's first day for a row that started before it; 0 when Staff answers no
    capacity there or no day of the row is inside the bound.
49. **Demand counts business days** (S11, reading 5): Monday to Friday less the public
    holidays of the platform calendar, exactly as an hours row books nothing on one, so
    a week with a holiday shows 4/5 of a request's hours. A candidate's
    `requiredHours`, being the person's, counts their days with capacity.
50. **`withdraw` is allowed on a closed project**: it only removes demand. A create, a
    `PUT` and a fill there are `project_closed`; a `PUT` judges the old project and the
    new, as amendment 19 does for a row.
51. **`fill` and `withdraw` retry as `DELETE` does** (amendments 21, 35): no `If-Match`,
    the request re-read `FOR UPDATE` under the lock, a moved revision starting the
    operation again, three times at most, then `revision_conflict`.
52. **Every status change moves the request's revision on** — fill, reopen, withdraw —
    so a `PUT` read before it is `revision_conflict`; a `PUT` judges the revision before
    the status.
53. **The request's note is recorded in an event by its name alone** (amendment 18), as
    an allocation's is.
54. **The intake lists filled requests too** (S18 said open): a filled request reads
    `covered`; `requestedHoursPerWeek` sums the open ones alone; a withdrawn request is
    not listed.
55. **`key_person` flags an open request only**: a filled request has its person.
56. **No demand on the board in B** (S16 named the board): the plan's `demand` row and
    the intake carry it; the board's cells stay the people's capacity.
57. **The Requests page opens on the open requests**, with *All statuses* in the status
    filter (`status=all` in the URL).
58. **`GET /requests` answers at most 500, oldest first** — by from date then id;
    without a window it lists every request, and its window has no bound, as
    `GET /allocations` (amendment 17).
59. **The Norwegian word for a request is *behov*** (not *forespørsel*), and its open
    status *Åpent*, agreeing with the neuter noun.
60. **The project page's Planning tab comes after Invoicing** (§7), gated on the module
    and on `planning:access` with `planning:view`, rendering the package's own
    `ProjectPlan`.
61. **A filling row deleted on a closed project still reopens its request** (S16 taken
    literally): the request is open on a `completed` or `cancelled` project, takes no
    change and no fill, can only be withdrawn, and until then counts in the plan's
    `demand`, in `requestsOpen` and in `openRequests`.
62. **`requestsOpen` lists an open request whose period has already ended** (S22's
    "already started"), until it is withdrawn.
63. **A fill whose request's project the directory no longer resolves** is a field
    error on `projectId` — the request's project, not a body field; the request writes'
    and the candidates' 400s carry the title "Invalid request".
64. **The `requestsOpen` item and a candidate's current project name a project's code to
    `planning:manage`**, as A's write answers do, under amendment 36's assumption that a
    planner's role carries both keys.

## Amendments at revision 5 (decided while building delivery C, 2026-10-26)

65. The plan's readings 1–9 (`docs/superpowers/plans/2026-10-26-planning-assignments.md`)
    stand as amendments: the derived dates computed whenever terms exist and binding on a
    confirmed row; the terms `PUT`'s optional `If-Match`; a non-compete date and its
    customers each needing the other; the erased person's wishes deleted only when
    `People` answered somebody, as `wish_withdrawn` with `reason: unanswered`; the release
    flag on rows with terms alone; the wisher not mailed about their own wish; the
    calendar subtitle in figures; the non-compete judged on the stored customer ids; the
    `wishesUnseen` item one item with a count.
66. **The non-compete is judged before the overbooking** (S17.3, S7): when both would
    refuse, the 409 is `non_compete`; a `PUT` that leaves a row tentative gets the
    warning, one that leaves it confirmed is refused, and moving a row off a person is
    never judged for that person (amendment 20).
67. **A notice in months follows Staff's month rule** (S15): the day is clamped, so 31
    January plus one month is 28 February, as Staff's own terms count notice.
68. **A terms `PUT` that changes nothing writes nothing** (amendment 18): the revision
    stays and no event is recorded; a first `PUT` of an empty body inserts the row at
    revision 1 and records no event. A terms `PUT` is `project_closed` on a closed
    project; the `DELETE` is allowed there, as a row's deletion is. Both re-read the row
    under the person's lock and retry as a `DELETE` does (amendments 21, 35).
69. **The non-compete's customer ids are stored ascending with duplicates collapsed**;
    the twenty-id limit is judged on the list as sent; `replacementNeedsApproval` is
    recorded in an event only when true; dates are recorded as `YYYY-MM-DD` and the
    customer ids as a list.
70. **`needs_customer_approval` fires only beside a `release_after_start` on the same
    row** (S17.4's "on it"): the customer's approval matters for a row the person must
    be released from early.
71. **The row that fills a request binds nothing on that request's candidates**, as the
    write path skips the row's own id: the person who filled it is not flagged or
    excluded against their own booking.
72. **A candidate's release flags when strictly after the request's first day; a
    non-compete binds when its until date is on or after it** (S17.3, S17.4).
73. **The intake judges the bindings once**, read from the earliest first day of the
    requests it judges, each request against its own project's customer from the
    planned-project entries it already holds (S18, amendment 47).
74. **A wisher who holds `planning:view` without `manage` sees their own wishes on the
    planner's timeline path too** (S21): the person's own wishes are theirs whatever else
    they hold; the timeline asks `Self` once and answers them only when the person is
    the caller.
75. **A wish that is someone else's is the same 404 as an unknown id** on `DELETE
    /me/wishes/{id}`, answered as a problem body without a code; a caller without a
    person gets `no_person`. `seen` and `resolve` answer the bare 404.
76. **Create's order** (S21, §4.7): an unknown property is a 400 first; then `no_person`;
    then the field errors (an unknown `kind` is a field error on `kind`; `fromDate` has
    no upper bound); under the lock `wish_no_booking`, then `wish_limit`, then
    `wish_exists` — a unique violation on the off index answers `wish_exists` too.
77. **Who is mailed about a new wish**: every active `planning:manage` holder, Owners
    included, once each, less the caller's user and the user linked to their person; a
    holder read that fails is logged and mails nobody, the wish still written. The
    subject is the sentence ("NAME wishes off CODE NAME from DATE" / "NAME wishes for
    something new from DATE"), the body the sentence and "Open the wishes: LINK", in
    both languages; an unresolved project reads "project N".
78. **`seen` is idempotent without a transaction** when the read already shows a stamp;
    otherwise it stamps under the person's lock, and a stamp set meanwhile is kept with
    no second event.
79. **A person Staff does not answer between today and the bound's far end counts as
    erased for S26** — a leaver included: their open wishes are deleted on the planners'
    list in one transaction under each person's lock, the caller the actor.
80. **The `wishesUnseen` item** is keyed `wishesUnseen/all`, dated by the oldest unseen
    wish's `created_at`, titled with that person's name (or their id when Staff answers
    nobody), with `count` the number unseen.
81. **The calendar source's title is the project's code and name** ("project N" in
    English when unresolved), the subtitle the load as typed ("22.5 h", "50 %"), items
    oldest first; the builder answers nil when `calendar` is not among the configured
    modules (S25).
82. **The permission descriptions change with C** (§5): `planning:access` reads "Use the
    Planning app: My planning, your own confirmed bookings and wishes, and your bookings
    on the Calendar."; `planning:manage` "Create, change, confirm and delete allocations
    and their terms, create, change, fill and withdraw requests, read the planners'
    notes, the candidates and the wishes, and change the Planning settings." The host's
    catalog is pinned to the server's words by test, so both changed together.
83. **The terms section keeps each save's answer and the sheet stays open** after a
    terms save or clear, so the planner sees the derived dates ("Earliest release DATE ·
    Handover from DATE" in the section's heading, when terms exist); a
    `revision_conflict` closes the sheet as the allocation form does. The form judges
    the terms itself before sending (both-or-neither, the given-on needs a period, the
    ranges, the date and the customers each needing the other, the reference's length);
    a field left empty is left out of the body and so cleared.
84. **The customers picker degrades without `customers:view`** (§7): any error from the
    customers search hides it with a note, and the customers the server named stay
    removable; one the directory did not name reads "Customer N".
85. **The timeline shows an earliest release only when it differs from the row's end**;
    the marks are one-day spans (release, handover) in a lane of their own; the two
    dates are columns of the table.
86. **The Wishes page's words**: *Mark seen* and *Resolve* as row actions named with the
    person and the wish, a *New* badge on an unseen wish, an erased person as plain
    text, a dash for a wish without a project; the kind reads "Off" / "Something new"
    (nb "Av" / "Noe nytt") on the page and "off their project" / "something new" in a
    candidate's flag.
87. **My planning's wish forms**: the off-wish button is disabled without a confirmed
    booking, the new-wish button always shown, the empty state included; the project
    select offers the projects of the bookings shown and cannot be cleared; the date's
    minimum is today; the dialog is a modal on a desktop and a bottom drawer on a phone;
    the seen stamp reads "Seen by a planner DATE" / "Not seen yet".
88. **The sidebar's Wishes entry** (nb *Ønsker*) follows Unbooked and is `planning:manage`'s;
    the `wishesUnseen` item opens it with the sentence "Unseen wishes: N"; the Calendar
    names the kind **Bookings** / **Allokeringer** in indigo.
89. **The terms' events are `planning:manage`'s** (S14, S23), as a wish's are: `terms_set`
    and `terms_cleared` keep the fields before and after — the reference and the
    customer ids included — and `GET /events` never answers them to a `planning:view`
    caller, whatever the filter, since S14 gives that audience the two derived dates and
    nothing else of the terms.
90. **A `PUT` is judged for the non-compete only when it places the row** (S17.3 "would
    place"): the person changes, the project changes, the status goes tentative to
    confirmed, or `fromDate` moves earlier so a non-compete's until date newly lies on
    or after it. Any other `PUT` of a confirmed row on a barred customer — a title, a
    load, a later start, an earlier end — is written and answers the `non_compete`
    warning, so a row the non-compete overtook is not stuck until deleted.
91. **A terms row's insert always records `terms_set`** (S23: every write is one row in
    the record), its `fields` empty when nothing was set; an update records nothing when
    nothing changed (amendment 68).
92. **The `wishes` flag unsets `covers` on purpose** (S18's "no flag"): a candidate who
    wishes for a change is one the planner must weigh, so the request reads
    `partly_covered` until someone is placed; the flag still lists them first (reading 7).
    The S26 deletion's reason reads `unanswered`, since §4.4 tells left and erased apart
    and the planners' list cannot.
93. **A write's answer never fails after its commit for a customer the directory could
    not name**: `applyTerms` logs the failure and answers the id alone (rule 4).

Open question 4, for the owner: **terms follow a row that a `PUT` gives to another
person** — the notice, the minimum date and the non-compete then bind the new person.
The design is silent; the build keeps the row's terms and the planner clears or resets
them. The alternative is to clear the terms when the person changes.
