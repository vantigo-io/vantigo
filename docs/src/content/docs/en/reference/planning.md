---
title: "Planning module"
description: "Booking people's time on projects: the one allocation table — a person, a project, a period, a load in percent or hours a week, tentative or confirmed, a title and the planners' note; the Workforce reads it takes from Staff and the project listing it takes from Projects, both before any transaction; the schema; the three permissions, why two are sensitive and why a planner needs staff:view; the settings; the period rules, the default status by the project's, confirm and its mail; the one capacity calculation — capacity, away, available, booked, tentative and free per day and per week, and absence that never shrinks a booking; overbooking as a warning or, by a setting, a refusal of a growing excess; the board, the project's plan with its demand, the person's timeline with its flags, the unbooked list and My planning; requests — a project's need for a person not yet named, in hours a week with competence requirements, open, filled or withdrawn — and the fill that books one, the reopen when its row is deleted; a request's candidates judged for competence, availability and the fit Staff scores; the intake of the planned projects; the competence catalogue read; the dashboard's figures and attention items; the events and their actor; the locks; the personal data held; the refusals, the switch and the endpoints."
sources:
  - apps/server/internal/planning
  - apps/planning/frontend
  - openapi/planning.yaml
  - apps/server/internal/openapi/specs/planning.yaml
  - apps/server/internal/db/migrations/00072_planning_baseline.sql
  - apps/server/internal/db/migrations/00073_planning_requests.sql
---
The Planning module books the company's people on its projects and shows the plan
whole: who is on what, for how long and how much, tentative or confirmed, against the
hours each person actually has. It keeps one booking table — an **allocation** is a
person on a project over a period at a load — and computes every person's capacity once,
from Staff's facts, for every view that reads it. In Norwegian the app is **Bemanning**.
The research is
[`2026-10-26-planning-module.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/research/2026-10-26-planning-module.md)
("N") and the design
[`2026-10-26-planning-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-26-planning-design.md)
(decisions S*n*). It is a vertical-slice module inside the single Vantigo binary
(`apps/server/internal/planning`), owns the `planning` schema in the shared PostgreSQL
database, and serves `openapi/planning.yaml` under `/api/v1/planning`. Its app is
`apps/planning/frontend` (`@vantigo/planning-ui`), the tile **Planning**.

It depends on two business modules, the first module to consume two at once
([contracts consumed](#contracts-consumed)): Projects, for the projects a person is booked
on, through `contracts.ProjectDirectory`, and Staff, for the people and their hours,
through `contracts.Workforce`. `MODULES` refuses `planning` without either
(`planning requires projects`, `planning requires staff`); Customers, for the customers'
names, comes with Projects, which requires it. It provides no contract in this release.
Calendar is optional the other way: from delivery C the module fills the many-provider
calendar slot with the caller's own confirmed bookings; with A it fills nothing.

This page grows with the module. What is here today, deliveries A and B: the
[contracts it consumes](#contracts-consumed), [the schema](#the-schema), the
[permissions](#permissions), the [settings](#settings), the
[projects and people it lists](#projects-and-people), [allocations](#allocations) —
[the load](#the-load), [tentative or confirmed](#tentative-or-confirmed),
[the period](#the-period), [title and note](#title-and-note) and the
[warnings on a write](#warnings) — [capacity](#capacity),
[overbooking](#overbooking), [the board](#the-board),
[the project's plan](#the-projects-plan), [the person's timeline](#the-persons-timeline),
[the unbooked list](#the-unbooked-list), [requests](#requests) — their
[requirements](#the-requirements), [the fill](#the-fill) and
[the reopen](#the-reopen) — a request's [candidates](#candidates), [the intake](#intake),
[the competence catalogue](#the-competence-catalogue), [My planning](#my-planning) and
[its mails](#the-mails), the dashboard's [figures and attention items](#stats-and-attention),
the [events](#events), the [locks](#locks), the [personal data](#personal-data) it holds,
[how a refusal looks](#refusals-and-revisions), [the switch](#enabling-and-disabling),
[what comes next](#what-comes-next) and the [endpoints](#endpoints). The terms of an
assignment, the person's wishes, the non-compete and the release in the candidates, and
the calendar source come with C.

## Contracts consumed

Every call goes through one accessor in `contractscalls.go`, with the request's
context, **before** any transaction of the module opens — never inside one, neither a
locked one nor a read-only snapshot — and the accessor refuses a call made inside one.
The module never restates a provider's rule: who is Self, who is a colleague and who
is in employment are Staff's answers; which projects exist is Projects'.

**`contracts.Workforce`** (`Deps.Workforce`), Staff's register, which Staff gates itself
([the Workforce contract](/en/reference/staff/#the-workforce-contract), rule 12 of the
[module boundaries](/en/contributing/module-boundaries/)). The module reads six of its
methods:

- **`People(ctx, from, to)`** — the people in employment on today or a later day of the
  window, each with an id, a display name, an employee number, a department today (or
  on their first day in employment inside the window when that is later), the days
  inside the window they are in employment (`Employed`, merged spans), and `UserID`: the
  user their self-service link names in a state that gives that user Self, used to
  [mail the person](#the-mails) and for nothing else.
- **`Capacity(ctx, from, to, personIDs)`** — per person, the hours a week in force over
  the window as consecutive segments (the full position's hours × the FTE, summed over
  concurrent employments, rounded to the quarter hour by Staff's own hours rule, so
  37.5 × 33 % reads 12.5), and the days away as whole-day spans, coalesced and clipped
  to the window, as Staff's calendar answers a colleague.
- **`Self(ctx)`** — the caller's own person under Staff's Self rule, or none. It needs
  `staff:access` alone.
- **`HeldCompetences(ctx, on, personIDs)`** — the competences each person holds valid on
  a date, for [the board's](#the-board) competence filter, the timeline's
  [certificate flag](#the-persons-timeline) and a request's
  [requirements](#candidates) met or missing.
- **`Competences(ctx)`** — Staff's catalogue: every competence not archived, hidden ones
  included, with its key, names, category, kind and whether it has a level. It names a
  request's [requirements](#the-requirements), judges them on a write, and is
  [`GET /competences`](#the-competence-catalogue). It needs `staff:view` or
  `staff:manage` and answers nothing otherwise.
- **`PreferenceFits(ctx, personIDs, booking)`** — per person, how a proposed booking fits
  what they want, as Staff scores it: `{score, lowered}`, 100 less each contradicted
  preference's weight × 10, floored at 0, and the kinds lowered in the catalogue's order,
  never a value ([the Workforce contract](/en/reference/staff/#the-workforce-contract)).
  The module asks it once for a request's [candidates](#candidates).

`People`, `Capacity`, `HeldCompetences` and `PreferenceFits` need **`staff:view`** —
`staff:manage` alone answers nothing — and answer the people **in employment on today or
on a later day of the window asked** (for `HeldCompetences`, on the date asked when it is
today or later; today itself for a past date): next month's starter is answered,
yesterday's leaver is not. The window of `People`, `Capacity` and `PreferenceFits` is
**bounded**: from no earlier than the Monday thirteen weeks before today, to no later
than three years after today; outside it Staff answers `contracts.ErrWindowOutOfBounds`. Every figure is **derived**: an
hours-a-week figure and a day away, **never a share** of a day, an absence type, a
status, an hour row or a terms row. A person Staff does not answer is, to this module,
[an id with no name](#allocations).

**`contracts.ProjectDirectory`** (`Deps.Projects`): `Project` and `Projects` for the
projects of the rows on a response, and **`ProjectsByStatus(ctx, statuses)`** for the
plannable list — every project in any of the statuses, by code then id, at most
`contracts.MaxDirectoryProjects` (1000), with its `StartDate` and `EndDate`. The listing
is **consumer-gated**, as `contracts.ProjectActuals` is: Projects performs no
authorization, and this module's own permission, `planning:view`, decides who sees it
([Projects](/en/reference/projects/#contracts-for-other-modules)). Of an entry the module
surfaces the id, code, name, customer, status and dates — never `Currency` or
`DefaultBillRate`.

**`contracts.CustomerDirectory`** (`Deps.Directory`) for the customers' names on a
project, and **`contracts.UserDirectory`** (`Deps.Users`) for the actors' names and
`Notify`, the person's mail.

## The schema

Two migrations make five tables: `00072_planning_baseline.sql` creates the `planning`
schema with `settings`, `allocations` and `events` (delivery A), and
`00073_planning_requests.sql` adds `requests` and `request_requirements` and the
allocation's `request_id` (B). No table references another schema: `person_id`
(Staff's), `project_id` (Projects'), `competence_id` (Staff's), and every user id are
opaque. **No CHECK constraints** — the module's validation owns the rules and reports
them per field; the one exception is the settings table's one-row index.

- **`settings`** — the installation's one row, written by the migration with the
  [defaults](#settings): `overbooking`, `default_load_unit`,
  `unbooked_threshold_percent`, `unbooked_horizon_weeks`, `request_lead_days`,
  `ending_lead_weeks`, `revision` (1), `updated_at`, `updated_by_user_id`.
- **`allocations`** — `id` (identity), `person_id`, `project_id`, `from_date` and
  `to_date` (civil dates), `load_unit` (`percent` or `hours_per_week`), `load`
  (`numeric(5,2)`), `status` (`tentative` or `confirmed`), `title` (100), `note` (500),
  `confirmed_at`, `revision` (1 on create), and the created and updated stamps with the
  user who wrote; indexed by `(person_id, from_date, to_date)` and
  `(project_id, from_date, to_date)`. **`request_id`** names the request the row
  [fills](#the-fill), null otherwise: a foreign key to `requests` with **`ON DELETE
  RESTRICT`** — a request is filled exactly while its row exists, so a request with a
  filling row cannot be deleted under it — and the unique partial index
  `ux_allocations_request` on `request_id` `WHERE request_id IS NOT NULL`, so at most one
  row fills a request.
- **`requests`** — `id` (identity), `project_id`, `title` (100), `from_date` and
  `to_date`, `hours_per_week` (`numeric(5,2)`), `status` (`open`, `filled` or
  `withdrawn`), `note` (500), `revision` (1 on create), and the created and updated
  stamps with the user who wrote; indexed by `(project_id, status)` and
  `(status, from_date)`. No person: a request names a project and competences, never
  someone.
- **`request_requirements`** — `id`, `request_id` (a foreign key to `requests`,
  `ON DELETE CASCADE`), `competence_id`, `min_level` (`smallint`, null for none); unique
  on `(request_id, competence_id)`.
- **`events`** — `id`, `entity_kind` (`allocation` or `request`), `entity_id`, `person_id`,
  `project_id`, `kind`, `payload` (`jsonb`), `actor` (`jsonb`), `occurred_at`; indexed by
  `(person_id, occurred_at)`, `(project_id, occurred_at)` and `(entity_kind, entity_id)`.

Nothing of a person is stored but ids, dates and figures, and nothing of a project but
its id: names, codes and statuses are read through the contracts on every response.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `planning:access` | Use Planning | The app: [My planning](#my-planning) — the caller's own confirmed bookings — and their own [timeline](#the-persons-timeline). **Every operation requires it.** | no |
| `planning:view` | See the plan | The board, every project's plan with its demand and requests, every person's timeline with confirmed and tentative rows and their titles, the unbooked list, the requests, the intake, the competence catalogue, the events, the stats — and with them every plannable project's code, name, customer, status and dates. | yes |
| `planning:manage` | Plan | Write allocations — create, change, confirm, delete — requests — create, change, withdraw, fill — and the settings; read a request's candidates; read the notes; the `manage` attention items. | yes |

All three are in the category "Planning" and delegable; the Owner holds them through
the wildcard. Each operation's rule is in the contract's `x-vantigo-access`; a caller
without it gets the access layer's uniform 403.

**Why two are sensitive.** `planning:view` reveals the whole project portfolio — bids in
`planned` included — which Projects otherwise reserves for `projects:view-all`: that is
what a plan is (reading 3). `planning:manage` reads the notes, the planners' sentences
about a person; from C it reads the person's wishes and the terms of a customer
contract.

**What `manage` alone names.** The `requestsOpen` item and a candidate's
`currentProject` name a project's code to a `planning:manage` holder, as A's write
answers do, under amendment 36's assumption that a planner's role carries
`planning:view` beside `planning:manage`.

**A planner also needs `staff:view`.** The Workforce reads answer nothing without it,
and the module holds no permission of its own that implies it. `GET /meta` answers
`workforceVisible: false` when `People` answered nobody for a caller who holds
`planning:view`, and the app explains it, so an empty board is never mistaken for an
empty plan.

| Reads | `access` (self) | `view` | `manage` |
| --- | --- | --- | --- |
| own confirmed rows with their title | ✓ | ✓ | ✓ |
| anyone's rows, tentative included; the projects; the requests and the intake | – | ✓ | ✓ |
| notes, an allocation's and a request's; a request's candidates; settings | – | – | ✓ |

**`GET /meta`** answers `canView`, `canManage`, `hasPerson` (whether `Self` answers a
person), `workforceVisible`, the load units, and — for `planning:manage` alone — the
settings, so the app never re-derives a rule.

## Settings

One row, written by the migration. `GET /settings` and `PUT /settings` are
`planning:manage`'s. `PUT` takes `If-Match` with the row's `revision`; every field left
out is kept, and every bad field is a 400 naming it, judged before anything is written.

| Field | Default | Rule |
| --- | --- | --- |
| `overbooking` | `warn` | `warn` or `refuse` ([overbooking](#overbooking)). |
| `defaultLoadUnit` | `percent` | `percent` or `hours_per_week`: pre-fills the allocation form and nothing else. |
| `unbookedThresholdPercent` | 50 | 1 to 100 ([the unbooked list](#the-unbooked-list)). |
| `unbookedHorizonWeeks` | 6 | 1 to 52: the horizon of the unbooked list and of the attention items. |
| `requestLeadDays` | 14 | 0 to 365: an open request starting within it of today, or already started, is the [item](#stats-and-attention) `requestsOpen`. |
| `endingLeadWeeks` | 4 | 1 to 26: a confirmed row ending within it with no follow-on is an [item](#stats-and-attention). |

## Projects and people

The two pickers the allocation form reads, both `planning:view`'s:

- **`GET /projects?status&q`** — the plannable projects by code, through
  `ProjectsByStatus`, at most 1000; `status`, a comma list, defaults to
  `planned,active,on-hold` (a status that is none of the five is a 400), and `q` matches
  the code or the name, case-insensitively, among the 1000 the directory answered — the
  module logs when that cap is reached. Each is `{id, code, name, status, customer?: {id, name},
  startDate?, endDate?}`. A `planning:view` holder thereby learns every such project's
  existence ([why](#permissions)).
- **`GET /people?from&to&q&departmentId`** — the people of a window as `People` answers
  them, the window today and the twelve weeks after it by default, each `{id, displayName, employeeNumber, department?: {id, name}, employed:
  [{from, to}]}`.

## Allocations

`planning.allocations` (S1) is the one booking table: a person (Staff's id), a project
(Projects' id), a period `fromDate`..`toDate`, a load with its unit, a status, a title
and a note. No table of assignments stands beside it: from delivery C, an allocation
that is also a contract with the customer carries its terms.

An allocation answers `{id, person: {id, displayName?, employeeNumber?}, project: {id,
code?, name?, status?}, fromDate, toDate, loadUnit, load, status, title?, note?,
confirmedAt?, requestId?, revision, createdAt, updatedAt}` — `requestId` the request the
row [fills](#the-fill), absent otherwise. **A missing person or project is an id
with no name**: a person Staff no longer answers — erased, left, or not visible to the
caller — reads by its id with `displayName` null, and the app shows *Erased person* or *A colleague*; a
project the directory does not resolve reads by its id. Nothing is deleted for it.

**Rows outlive their inputs.** An employment ended after a row was written leaves the
row — the timeline flags its days past the end `beyond_employment`; a cancelled project
keeps its rows, which weigh as they are until a planner deletes them.

- **`POST /allocations`** (`planning:manage`) creates one from `{personId, projectId,
  fromDate, toDate, loadUnit, load, status?, title?, note?}` and answers
  `{allocation, warnings}` ([warnings](#warnings)).
- **`GET /allocations/{id}`** and **`GET /allocations?personId&projectId&from&to&status`**
  (`planning:view`) read one, or at most 500 rows touching the window, by from date
  then id.
- **`PUT /allocations/{id}`** (`planning:manage`, `If-Match` with the row's revision)
  replaces the same fields — it may move the row to another person or project — and
  answers `{allocation, warnings}`; a stale revision is `revision_conflict` (409). A row
  that fills a request keeps its `requestId` through any `PUT`, and the request stays
  filled (S16).
- **`DELETE /allocations/{id}`** (`planning:manage`) deletes the row: 204. A confirmed
  row's person is told; a row that fills a request [reopens it](#the-reopen).
- **`POST /allocations/{id}/confirm`** (`planning:manage`) confirms it
  ([below](#tentative-or-confirmed)), with no `If-Match`.

Every request schema is closed (`additionalProperties: false`): a field the contract
does not name is a 400 naming it.

### The load

`loadUnit` is `percent` — of the person's position, more than 0 and at most 100 — or
`hours_per_week`, more than 0 and at most 80; `load` is the number, two decimals at
most. Anything else is a field error on `load` (or `loadUnit`). The load is **stored as
typed** (S2): per day a row books `hours_per_week / 5` on a day with capacity, or
`percent × capacity(day)`; nothing on a day without ([capacity](#capacity)). So a
percentage follows the FTE when it changes, and hours do not.

### Tentative or confirmed

`status` is `tentative` or `confirmed` (S3). A **tentative** row is the planner's draft:
shown apart (hatched), it weighs nothing in the headline figures — load, unbooked,
overbooked, free — and is summed on its own; the person never sees it. A **confirmed**
row is the plan.

A `POST` without a `status` takes the project's: **`tentative` on a project in status
`planned`, `confirmed` on any other**. The planner may set either on any project.

**Confirming** is its own operation, because it is the moment the person is told
([the mails](#the-mails)): `POST /allocations/{id}/confirm` sets the row `confirmed`,
stamps `confirmed_at` and records `allocation_confirmed`; confirming a confirmed row
answers it as it is. A closed project refuses it (`project_closed`), and the hours it
adds to the confirmed plan are judged for [overbooking](#overbooking). A confirmed row set back to `tentative` is a change through
`PUT` (`allocation_unconfirmed`), and the person is told of that too.

### The period

- `toDate` on or after `fromDate`, else a field error on `toDate`.
- At most **1096 days** (three years): `period_too_long`, a field error on `toDate`.
- **Inside the person's employment**: every day must lie inside the days Staff answers
  the person in employment for the window (`People`'s `Employed`); a day outside is
  `outside_employment` (409). A booking of someone not employed is an error, not a
  warning; an open-ended employment bounds nothing.
- A person Staff does not answer is a field error on `personId`; a project the
  directory does not resolve, a field error on `projectId`.
- A period outside the project's `startDate`..`endDate`, when the project has them, is
  the warning `outside_project_dates`, and the row is written.
- **A closed project**: a project in status `completed` or `cancelled` takes no new row
  and no change but deletion — `project_closed` (409). Its rows stay and read with the
  project's status.

Dates are civil and compared at midnight UTC; "today" is the business calendar's
(`contracts.BusinessCalendar.Today`), computed once per request. Every read that takes a
window (`from`, `to`) refuses with a 400 a date that does not parse, a `to` before
`from`, and a window outside the Workforce [bound](#contracts-consumed).

### Title and note

`title` (S5), at most 100 characters, optional, is the role as the project names it —
"Frontend developer", "Prosjektleder" — and is read by `planning:view` and by the person.
`note`, at most 500, optional, is **read and written by `planning:manage` alone**: a
`planning:view` holder's allocation carries none, and the person never reads it. No
other free text is on a person's row. A longer one is a field error on its field.

### Warnings

A write's answer is the row and `warnings: [{code, weeks?, detail?}]`, so the planner
sees the consequence without a second call (§4.6). The codes, snake_case like every
flag — a [request's](#requests) write answers `outside_project_dates` alone, and a
[fill](#the-fill) its allocation's:

| Code | When |
| --- | --- |
| `overbooked` | A week's confirmed `booked` exceeds its `capacity` after the write; `weeks` names the Mondays ([overbooking](#overbooking)). |
| `booked_while_away` | A week's confirmed `booked` exceeds its `available` but not its `capacity`. |
| `outside_project_dates` | The period reaches outside the project's start and end dates. |

## Capacity

One calculation (S6), one pure function (`capacity.go`) over Staff's segments and away
spans, the platform's public holidays (`internal/holidays`) and the rows; no handler
sums hours of its own. For a person and a civil day:

| Figure | Rule |
| --- | --- |
| `capacity` | 0 on Saturday, Sunday and a public holiday of the platform calendar; else `hoursPerWeek / 5`, where `hoursPerWeek` is the sum over the person's employments in force that day of the full position's hours × the FTE in force, as `Capacity` answers it. |
| `away` | True when the day lies in one of the person's away spans — the approved and registered absences of whole or part days, coalesced and clipped as Staff's calendar answers a colleague, with no share, no hours and no type. **A day away is wholly away**: a 40 % absence and a graded sykmelding each count the day as away, because the share is a fact Staff keeps from colleagues (reading 1). **An hour-unit absence is not a day away**: Staff shows a colleague no hour row, so a medical appointment never reaches the board. |
| `available` | `capacity` when not away, else 0. |
| `booked` | The sum over **confirmed** allocations covering the day of the row's hours that day ([the load](#the-load)). |
| `tentative` | The same over tentative rows. |
| `free` | `available − booked`, which may be negative. |

Per **week**, Monday to Sunday, each figure is the sum of its days, and `away` is true
when any day of it is; **`load %`** is `booked / capacity` over the week, absent when the
week's capacity is 0. **Absence never shrinks a booking**: a week away under a 100 %
allocation is a conflict the board shows red, not a silent re-plan, and Staff is not
told. A row over a weekend or a holiday only books nothing and shows a zero-capacity
week; two concurrent employments sum into one capacity, since an allocation is of the
person, not of an employment.

## Overbooking

Judged on **confirmed** rows alone (S7), under the person's [lock](#locks), against the
capacity read before the transaction:

- A write that leaves any week's `booked` above its **`capacity`** — not its
  `available`: being away is a conflict of its own — answers the warning `overbooked`
  naming the weeks, and is written.
- Under `overbooking: refuse` ([settings](#settings)), a write that makes a week's excess
  **grow** is `overbooked` (409) and nothing is written. A write that keeps or shrinks
  an excess already there is allowed, with the warning, so a plan can be mended one row
  at a time.
- A week whose `booked` exceeds `available` but not `capacity` is the warning
  `booked_while_away`, never a refusal, in either setting.

A tentative row is never overbooked. A percentage row's hours follow an FTE change from
that day and an hours row's do not, so an hours row may come to overbook without a
write: the board shows it and the next write warns.

## The board

`GET /board?from&to&groupBy&departmentId&competenceId&projectId&includeTentative&includePlanned`
(`planning:view`, S10) answers at most **53 weeks** — more is a 400 — inside the
Workforce [bound](#contracts-consumed), this week and the eleven after it by default:
`{from, to, weeks: [{monday}], rows, allocations}`.

- **Rows**: with `groupBy=people` (the default), the people of the window as `People`
  answers them, each `{person, cells}`; `departmentId` keeps a department's people and
  `competenceId` the people who hold the competence on the later of `from` and today
  (`HeldCompetences`), so next month's starter is kept on a board that began last Monday. With
  `groupBy=project`, the same figures per project, `{project, cells}`, the people under
  it. `projectId` keeps one project's rows and the people on it.
- **Cells**: per row per week `{monday, capacity, available, booked, tentative, free,
  loadPercent?, away}`, the figures of [capacity](#capacity).
- **Allocations**: every row touching the window, for the bars.
- **No demand**: the board answers no open requests; a project's demand is on its
  [plan](#the-projects-plan) and in [the intake](#intake).
- **`includeTentative`** (true by default) sums and lists the tentative rows;
  **`includePlanned=false`** (true by default) leaves out every row on a `planned`
  project — "can we take A and B" read against "what is firm".

The app colours a cell by its load — free, fine, full, over — hatches the tentative
share, and reads a conflict with absence red with the days away named, never a type or
a share.

## The project's plan

`GET /projects/{projectId}/plan?from&to` (`planning:view`, S11), this week and the
eleven after it by default: the project's entry
([projects and people](#projects-and-people)), its allocations in the window, its
**`requests`** — the open and filled ones touching the window, by from date then id, as
[a request](#requests) answers; a withdrawn one is not the plan — and per week
`{monday, booked, tentative, demand}`: the project's confirmed and tentative hours, and
its open demand.

**`demand`** (S11, S16) is, over the project's **open** requests, each one's
`hoursPerWeek` × the **business days** of the week inside its period ÷ 5 — Monday to
Friday less the public holidays of the platform calendar (`internal/holidays`) — the
week cut to the plan's window as `booked` and `tentative` are. A request weighs exactly
as an hours row books: from a Wednesday, 3/5 of its hours in its first week; in a week
with a public holiday, 4/5. A filled request is booked through its row and weighs in
`booked` or `tentative`; a withdrawn one is no demand.

## The person's timeline

`GET /people/{personId}/timeline?from&to` (S12): forward by default, today to today plus
26 weeks, at most 104 weeks inside the Workforce [bound](#contracts-consumed). It answers
`{person, employed, away, allocations, flags}`: the employment spans, the away spans,
each allocation, and the flags `[{allocationId, code, date?}]`, `date` the day the flag
is about:

- **`beyond_employment`** — the row has days past the end of the person's employment;
  `date` is the employment's end.
- **`certificate_expires`** — a certificate the person holds (`HeldCompetences` on
  `from`) whose `validTo` falls inside the allocation: the owner's "certificates against
  planned work"; `date` is the certificate's `validTo`.

A `planning:view` holder reads anyone `People` answers them; one it answers nobody to —
without `staff:view` — reads only their own, as below. A caller holding `planning:access`
alone reads their own — the person `Self` answers — with its **confirmed** rows only, as
[My planning](#my-planning) shows them; anyone else's answers the 404 an unknown id
gets, with an empty body. The earliest release
and the handover start of an assignment, and the person's open wishes, come with
delivery C.

## The unbooked list

`GET /unbooked` (`planning:view`, S13) answers `{horizon: {from, to}, people}`: every
person in employment within the horizon — today to today plus `unbookedHorizonWeeks` —
whose confirmed `booked / available` over the horizon is under
`unbookedThresholdPercent`, each `{person, loadPercent, freeHours, rollOff?}`. `rollOff`
is the last day of their latest confirmed allocation ending inside the horizon, absent
when they have none. Sorted by roll-off, then load.

A person with **no available hours** in the horizon — away throughout, or starting after
it — is **not listed**: they are not unbooked, they are away. It is a view, not a
digest: nothing is mailed about it.

## Requests

`planning.requests` (S16) is a project's need for a person not yet named — the
placeholder a planner fills when someone is found. A request is a project (Projects'
id), a `title`, a period `fromDate`..`toDate`, a load in **hours a week only** — a
request has no person whose position a percentage could be read against — a
[status](#the-statuses), the planners' `note`, and up to ten
[requirements](#the-requirements). It names no person: who fills it is the
allocation's.

A request answers `{id, project: {id, code?, name?, status?}, title, fromDate, toDate,
hoursPerWeek, status, note?, requirements: [{competenceId, key?, nameEn?, nameNb?, kind?,
hasLevel?, minLevel?}], filledBy?: {allocationId, person: {id, displayName?,
employeeNumber?}}, revision, createdAt, updatedAt}`. **A requirement is named from
Staff's catalogue** as the caller may see it; one the catalogue no longer answers —
archived since, or a caller Staff answers no catalogue — reads by its `competenceId`
alone (rule 4). **`filledBy`** is the allocation that fills the request and its person,
named through `People` over the row's window cut to the bound; present on a filled
request alone. **`note`** is `planning:manage`'s alone, as on an allocation.

- **`GET /requests?projectId&status&from&to`** (`planning:view`) lists at most **500**,
  by from date then id — the oldest first: one project's, one status's, touching the
  window (`fromDate` on or before `to`, `toDate` on or after `from`), each filter
  optional. Without a window every request is listed; the window has no
  [bound](#contracts-consumed), so a past one lists history, as `GET /allocations`
  does. A `to` before `from` is a field error on `to`.
- **`GET /requests/{id}`** (`planning:view`) reads one; an unknown id is the bare 404.
- **`POST /requests`** (`planning:manage`) creates one from `{projectId, title,
  fromDate, toDate, hoursPerWeek, note?, requirements?: [{competenceId, minLevel?}]}`,
  `open` at revision 1, and answers 201 `{request, warnings}`.
- **`PUT /requests/{id}`** (`planning:manage`, `If-Match` with the request's revision)
  replaces the same fields whole, the requirements with them — left out means none — and
  may move the request to another project; it answers `{request, warnings}`. A `PUT`
  that changes nothing writes nothing.
- **`POST /requests/{id}/withdraw`** (`planning:manage`) closes an open request for good
  and answers it, with no `If-Match`.
- **`POST /requests/{id}/fill`** books a person on it ([the fill](#the-fill)), and
  **`GET /requests/{id}/candidates`** names who could be booked ([candidates](#candidates)).

Every request schema is closed: a field the contract does not name is a 400 naming it.

### The statuses

| Status | Means |
| --- | --- |
| `open` | Demand: the plan's [`demand`](#the-projects-plan), the intake's requested hours, `openRequests` and the `requestsOpen` [item](#stats-and-attention). The only status that takes a change, a fill or a withdrawal. |
| `filled` | Exactly while an allocation names it (`request_id`); deleting that row opens it again ([the reopen](#the-reopen)). |
| `withdrawn` | Final: never reopened, never filled, on neither the plan nor the intake. |

**A filled or withdrawn request takes no change** (plan reading 2): a `PUT`, a `fill` or
a `withdraw` of it is `request_not_open` (409), so the filling row and the request
cannot drift apart — the booking is changed through the allocation's own `PUT`. A
`PUT` judges the revision before the status, so a stale `If-Match` on a filled request
is `revision_conflict`. **Every status change** — fill, reopen, withdraw — moves the
request's `revision` on, as a change of its fields does, so a `PUT` read before it is
`revision_conflict`.

### The fields

- **`title`** — required, trimmed, at most 100 characters: the role as the project
  names it, and the title of the row that fills it.
- **`fromDate`..`toDate`** — as [an allocation's period](#the-period): `toDate` on or
  after `fromDate`, at most 1096 days (`period_too_long` on `toDate`), and at least one
  day inside the Workforce bound (`window_out_of_bounds` on `fromDate` or `toDate`). A
  period outside the project's `startDate`..`endDate` is the warning
  `outside_project_dates`, and the request is written. No employment rule: a request
  names nobody.
- **`hoursPerWeek`** — more than 0, at most 80, two decimals at most: the allocation's
  hours rule, a field error on `hoursPerWeek`.
- **`note`** — at most 500 characters, optional.
- **`projectId`** — a project the directory does not resolve is a field error on
  `projectId`. **A closed project** — `completed` or `cancelled` — takes no request and
  no change to one, `project_closed` (409), judged on a `PUT` on the old project and the
  new, so a request cannot be moved off a closed project. Its open request may still be
  **withdrawn**: withdrawing only removes demand.

Every bad field is reported at once, before anything is written. A request write's
400 — a create, a `PUT` — and the candidates' 400 carry the title *Invalid request*; a
fill's carries *Invalid allocation*, since what it writes is an allocation, except the
field error on `projectId` for a request whose project the directory no longer
resolves, which is the request's and carries *Invalid request*.

### The requirements

At most **10** — more is a field error on `requirements` — each a competence of Staff's
(`competenceId`, opaque) and an optional `minLevel`:

- **Each competence once**: a repeat is a field error on the later one's
  `requirements[i].competenceId`.
- **Read from `Workforce.Competences`** as the caller may see it, only when the body
  names a requirement: a competence the catalogue does not answer — unknown or archived —
  is a field error on `requirements[i].competenceId`. A **hidden** competence is
  accepted, since it is still held by those who have it; the app offers none for a new
  requirement. A caller Staff answers no catalogue — with neither `staff:view` nor
  `staff:manage` — can name no requirement, every id being unknown: Staff's rule, not
  restated.
- **`minLevel`** 0 to 4, on a competence with a level (`hasLevel`) alone; otherwise a
  field error on `requirements[i].minLevel`.

A requirement is **met** by a person who holds the competence as `HeldCompetences`
answers it — a verified and valid certificate, a declared or verified skill — at or
above `minLevel` when it has one, on the later of the request's first day and today
([candidates](#candidates)).

### The fill

`POST /requests/{id}/fill` (`planning:manage`, S16) from `{personId, status?}` books a
person on an **open** request: it writes the allocation from the request's project,
period, hours a week (`loadUnit` `hours_per_week`, `load` the request's `hoursPerWeek`)
and title, under exactly the rules of [`POST /allocations`](#allocations):

- `status` as given, else by the project's — `tentative` on a `planned` project,
  `confirmed` on any other (plan reading 7);
- the project resolved and open: a request whose project the directory no longer
  resolves is a field error on `projectId` — the request's project, not a field of the
  body — and a closed one `project_closed`;
- the person answered by Staff (a field error on `personId`) and in employment on every
  day of the period inside the bound (`outside_employment`); a period with no day inside
  the bound is a 400 `window_out_of_bounds`;
- [overbooking](#overbooking) judged on the person's confirmed rows when the row is
  confirmed: under `refuse` a growing excess is `overbooked`, nothing is written and
  the request stays open.

**The fill judges no requirement**: the candidates are the planner's advice, and a person
missing one may be booked.

In one transaction, under the project's lock and then the person's ([locks](#locks)):
the request is re-read `FOR UPDATE` and must be at the revision read; the allocation is
inserted with **`request_id`** naming the request and `allocation_created` recorded,
its payload carrying `requestId`; the request is set `filled` and `request_filled`
recorded. After the commit a confirmed row's person is [mailed](#the-mails) as for any
confirmed create. The answer is 201 `{request, allocation, warnings}`: the request
filled with its `filledBy`, the row with its `requestId`, and the row's
[warnings](#warnings).

**No `If-Match`**, as on `withdraw`: a request changed between the read and the lock
starts the operation again, three times at most, then `revision_conflict` (409), as a
`DELETE` does — and a request filled or withdrawn meanwhile is `request_not_open`.

### The reopen

**A request is filled exactly while a row names it.** `DELETE /allocations/{id}` of a
row that fills a request takes the request's project lock, then the person's, re-reads
the row under them, deletes it and records `allocation_deleted`, then sets the request
`open` and records `request_reopened` naming the deleted row — one transaction. The
lock is the request's project's, which a `PUT` of the row may have moved the row off.
**A closed project's row reopens its request too** (S16 taken literally: a request is
filled exactly while a row names it): the request is then open on a `completed` or
`cancelled` project, takes no change and no fill (`project_closed`), and can only be
withdrawn — until it is, it counts in the plan's `demand`, in the `requestsOpen` item and
in `openRequests`.

A `PUT` of the filling row — its period, load, person or project — leaves the request
filled and the row's `requestId` in place. A withdrawn request is never reopened; and
since a filled request cannot be withdrawn, a planner who no longer needs it deletes the
row, then withdraws the request it reopened.

## Candidates

`GET /requests/{id}/candidates?includePartial` (`planning:manage`, S17 points 1, 2 and 6)
answers `{candidates, includePartial}`: the people who could fill the request, each with
the figures, the flags and the reasons behind the judgement, so the planner sees why.
It is answered for a request **in any status** (plan reading 3): a read never refuses
on the status, and a filled request's candidates show who else could have filled it. An
unknown id is the bare 404.

**Who is judged.** The people `People` answers over the request's period **cut to the
Workforce bound** — so a request that started long ago is judged on the days Staff can
answer about — kept when in employment on every day of the period inside the bound. A
period with no day inside it is a 400 `window_out_of_bounds` on `fromDate` or `toDate`,
as a write answers. A caller without `staff:view` is answered nobody.

A candidate answers `{person: {id, displayName, employeeNumber, department?, employed},
availability, freeHours, requiredHours, fit: {score, lowered}, flags: [{code,
competenceKey?, date?}], missing, covers, currentProject?: {project, since, days}}`:

1. **Competence** (S17.1). Each requirement is judged against what the person holds on
   the later of the request's first day and today (`HeldCompetences`), the day Staff can
   answer holding on, as [the board's](#the-board) filter judges it. A requirement not
   met — not held, or held below its `minLevel` — puts its key in **`missing`** (its id
   when the catalogue no longer answers it) and **leaves the person out**, unless
   **`includePartial=true`**, when they are listed with `missing` naming every key they
   lack. A requirement met only by certificates valid to a day **before the request's
   last day** — every holding that meets it expiring — is the flag
   **`certificate_expires`**, with `competenceKey` and `date` the latest `validTo` among
   them; one holding that lasts the period, or a skill, raises nothing.
2. **Availability** (S17.2, plan reading 1), judged on **totals** over the period cut
   to the bound, not week by week. **`requiredHours`** is what the request would book:
   `hoursPerWeek / 5` on each of the person's days with [capacity](#capacity) — nothing
   on a weekend, a public holiday or a day outside employment, and a day away still
   books, since absence never shrinks a booking. **`freeHours`** is the sum of the
   person's `free` over the same days, confirmed rows alone. **`fits`** when
   `freeHours ≥ requiredHours`, else **`partial`**. Availability never excludes: one
   week away does not hide a person free the rest of the period, and both figures are
   answered so the planner sees the margin.
3. **Fit** (S17.6). One `Workforce.PreferenceFits` call for every person kept, with the
   booking's `From` and `To` the period cut to the bound and `HoursPerWeek` the
   request's. The other facets — weekdays, parts of the day, location, consecutive
   days, travel, remote — are left empty and contradict nothing, so today only a
   `max_hours_per_week` preference can lower a score, judged on the request's hours
   alone. **`fit`** is `{score, lowered}` as Staff answers it; a person Staff leaves out
   of the answer scores 100 with nothing lowered.
4. **The current project**, the fairness signal: the project of the person's confirmed
   row covering today with the earliest from date, `since` that date and `days` the
   days from it to today; absent when no confirmed row covers today.

**`covers`** is `availability` `fits` with no flag and nothing missing — the intake's
[covered](#intake).

**The sort** is total: `fits` before `partial`; then fit score descending; then free
hours descending; then the days on the current project descending — the longest on one
project first, none counting 0; then display name; then id.

## Intake

`GET /intake` (`planning:view`, S18) answers `{projects: [{project,
requestedHoursPerWeek, tentativeHoursPerWeek, confirmedHoursPerWeek, requests:
[{request, coverage, candidates, flags}]}]}`: the pipeline — every project in status
**`planned`**, by code, through `ProjectsByStatus` (an `on-hold` project is not the
pipeline), each with its **open and filled** requests by from date then id. A withdrawn
request is not listed. The candidates behind a request are `planning:manage`'s; the
intake answers how many cover it.

**Coverage** is judged over [the candidates](#candidates) **without partial matches**
(plan reading 9): a candidate is a person employed through the period who meets every
requirement.

| Coverage | When |
| --- | --- |
| `covered` | The request is filled, or at least one candidate covers it: fits, no flag, nothing missing. |
| `partly_covered` | There are candidates and none covers: each is `partial` or flagged. |
| `uncovered` | Nobody employed through the period meets every requirement. |

So `uncovered` means that nobody holds the competences, not merely that nobody is
employed. A request with no day inside the Workforce bound is judged on no candidates —
`uncovered` unless filled — rather than refusing the whole intake. **`candidates`** is
how many cover the request, and the flag **`key_person`** marks an **open** request
exactly one person covers: the key-person risk. A filled request has its person and
carries no flag. A caller without `staff:view` is answered nobody, so every open
request reads `uncovered`.

The project's figures, each in hours a week (plan reading 4):

- **`requestedHoursPerWeek`** — the sum of its open requests' `hoursPerWeek`;
- **`tentativeHoursPerWeek`** and **`confirmedHoursPerWeek`** — the sum over **all** its
  allocations of that status of each row's hours a week: an hours row its `load`; a
  percent row its share of the person's hours a week in force on **its first day inside
  the Workforce bound** (the bound's first day for a row that started before it), 0 when
  Staff answers the person no capacity there or no day of the row lies inside the bound.

## The competence catalogue

`GET /competences` (`planning:view`, plan reading 6, amendment 38) answers
`{competences: [{id, key, nameEn, nameNb, categoryKey, kind, hasLevel, hidden}]}`:
Staff's catalogue as `Workforce.Competences` answers the caller — every competence not
archived, `kind` `skill` or `certificate`, hidden ones included and flagged so the
request sheet offers none for a new requirement — for a request's
[requirements](#the-requirements) and [the board's](#the-board) competence filter. It is
**empty, not an error**, when Staff answers the caller nothing (neither `staff:view` nor
`staff:manage`). The module has no competence endpoint of its own beyond this read.

## My planning

`GET /me` (`planning:access`, S19) answers `{person, allocations}`: the caller's person,
as `Self` answers it, and their **confirmed** allocations from today on, each with its
dates, load and title — never a note. Tentative rows are the planner's draft and are not
shown. A caller `Self` answers no person for — linked to nobody, or by a link that does
not give them Self — gets `no_person` (404). The person's wishes come with delivery C.

### The mails

The person is mailed through `contracts.UserDirectory.Notify`, to `People`'s `UserID`,
after the commit and outside any transaction:

- when a row of theirs is **confirmed**;
- when a confirmed row's **period, load or project** changes;
- when a confirmed row is **deleted**;
- when a confirmed row is set back to **tentative**.

Each names the project's code and name and the dates, in the person's own language —
identity picks it — and never the note. A tentative write mails nobody. A person with no
`UserID` is never mailed, and that is not an error; nor is a person `People` does not
answer to the planner — a planner without `staff:view`, or an erased person — since the
mail needs the person answered.

## Stats and attention

Computed on read (S22); the module has no worker.

**`GET /stats`** (`planning:view`) answers the dashboard card's figures:
`peoplePlannedThisWeek` (the people with a confirmed booking this week), the confirmed
`loadPercentThisWeek` — booked over capacity this week — with
`tentativeLoadPercentThisWeek` beside it (each absent when this week's capacity is 0),
`unbooked` (the [unbooked list](#the-unbooked-list)'s count) and `openRequests`, how many
[requests](#requests) are open — every one, since a request names nobody Staff could
hide.

**`GET /stats/attention`** (`planning:access`) answers the items as a bare array, oldest first, in the
shape every module's attention items share — `{id, type, title, occurredAt, entityId,
count?}`, `entityId` the person's id, the allocation's for `endingWithoutFollowOn`, or the
request's for `requestsOpen` — each to its audience; a caller with none of them gets `[]`, never a 403:

| Type | Audience | When |
| --- | --- | --- |
| `unbooked` | `planning:view` | People under the threshold within the horizon ([the unbooked list](#the-unbooked-list)). |
| `overbooked` | `planning:view` | People with a week in the horizon whose confirmed `booked` exceeds `capacity`. |
| `endingWithoutFollowOn` | `planning:manage` | Confirmed rows ending within `endingLeadWeeks` where the person has no confirmed row starting on or before that end plus seven days. |
| `certificatesExpiring` | `planning:view` | Held certificates expiring inside a confirmed row within the horizon ([the timeline's flag](#the-persons-timeline)). |
| `requestsOpen` | `planning:manage` | One per open [request](#requests) whose first day is on or before today plus `requestLeadDays` — starting within the lead, or already started, including one whose period has already ended, until it is withdrawn — dated on that first day at midnight UTC and titled "CODE — title", the project's code when the directory resolves it, else its id. |

Each opens the screen that answers it. `wishesUnseen` comes with C.

## Events

Every write is one row in `planning.events` (S23), in the write's transaction:
`allocation_created`, `allocation_changed`, `allocation_confirmed`,
`allocation_unconfirmed` and `allocation_deleted` about an allocation (`entityKind`
`allocation`), and `request_created`, `request_changed`, `request_filled`,
`request_reopened` and `request_withdrawn` about a request (`request`). The payload
carries the changed fields before and after, by the contract's field names; the
**actor** is the
snapshot `{kind, displayName, userId}` — `kind` `user` or `system` — of the caller who
wrote, as Staff keeps it. **The person's name is never
written** — the row names them by `person_id` alone. A `PUT` that moves a row to another
person files its `allocation_changed` under the new person, the old one in the payload's
`from`, so `GET /events?personId=` of the old person does not list it.

A request's events are filed under its project:

| Kind | Payload | Filed under |
| --- | --- | --- |
| `request_created` | Every field set — `projectId`, `title`, `fromDate`, `toDate`, `hoursPerWeek`, `note`, `requirements` as `[{competenceId, minLevel?}]` ordered by competence — before (none) and after. | The project. |
| `request_changed` | The fields a `PUT` changed, before and after; reordering the requirements changes nothing. | The new project. |
| `request_filled` | `{allocationId, personId}`, beside the row's own `allocation_created`. | The project and the person (plan reading 8). |
| `request_reopened` | `{allocationId}`, the deleted row, beside its `allocation_deleted`. | The project and the deleted row's person. |
| `request_withdrawn` | `status` from `open` to `withdrawn`. | The project. |

**The note is recorded by its name alone**, on an allocation and on a request: a changed
note is in the payload with neither text, since events are `planning:view`'s and the
note `planning:manage`'s (amendment 18). An allocation's recorded fields carry
`requestId` when it fills a request.

`GET /events?personId|projectId|allocationId` is `planning:view`'s: newest first, at
most 500. A request's events are read by its project, and `request_filled` by the
person too.

## Locks

- **No call out of a transaction.** The people, their capacity, the project's entry, the
  competence catalogue and the competences held, the preference fits, the user directory
  and the mail are reached before a transaction opens or
  after it commits, never inside one; each goes through its accessor, which refuses such
  a call, and the tests fail on any made inside one. A change in Staff between the read
  and the write is accepted: capacity is a warning's input, employment a 409's, and a
  person ended meanwhile is caught by the next read.
- **The person.** Every write of an allocation takes a transaction advisory lock on the
  person (`planning`, `person_id`), and the overbooking judgement reads the person's
  confirmed rows under it — so two planners booking one person are judged one after the
  other, the second on the first's committed rows.
- **The project.** Every write of a request takes a transaction advisory lock on its
  project (`planning`, `project_id`), and re-reads the request `FOR UPDATE` under it; a
  `PUT` that moves a request to another project takes both projects' in ascending id.
- **The order.** A write that needs both takes the **project's** lock first, then the
  person's: a [fill](#the-fill) — the request's project, then the person's; the
  `DELETE` of a row that fills a request, which [reopens it](#the-reopen) — the
  request's project, then the row's person; and a `PUT` that moves a row to another
  project. One that moves a row to another person takes both persons' in ascending id.
  One order everywhere keeps two such writes from deadlocking.

## Personal data

The module stores about a person ids, dates and hours — no name, no text about them
but the planners' note, no health fact — and the planners' names as the events' actors
(S26). A request stores nothing of a person: its title and note are about a project's
need, and who fills it is the allocation's `person_id`. Staff's personal-data export and erase do not reach it in this release: after an
erase the rows keep an id Staff no longer resolves, read as *Erased person*, and weigh
nothing new, since `People` no longer lists them — they leave the board and stay in the
project's plan by id. A many-provider slot through which Staff collects another module's
rows about a person for its export and its erase is the roadmap's, for phase 2.

## Refusals and revisions

Every refusal is an RFC 9457 problem, `PlanningProblem`: `title`, `status`, `detail`,
and — when a rule of the design refused — `code`, which is what a client keys off, never
the words. A 400 carries `errors`, the field errors keyed by the request's camelCase
field.

| Code | Status | When |
| --- | --- | --- |
| *(field error)* | 400 | `toDate` before `fromDate`; `load` or `loadUnit` out of its rule; `title` or `note` too long; a request's `title` empty; `hoursPerWeek` out of its rule; more than ten `requirements`; a `requirements[i].competenceId` the catalogue does not answer or named twice; a `requirements[i].minLevel` out of 0 to 4 or on a competence without a level; `personId` Staff does not answer; `projectId` the directory does not resolve; a settings field out of its bounds; a property the schema does not name; a window that does not parse, ends before it starts or lies outside the bound; a board of more than 53 weeks; a project status that is none of the five. |
| `period_too_long` | 400 | A period of more than 1096 days, on `toDate`. |
| `outside_employment` | 409 | A day of the period outside the person's employment. |
| `project_closed` | 409 | A new row, a change or a confirm, a new request or a change to one, or a fill, on a project in status `completed` or `cancelled`. |
| `overbooked` | 409 | Under `overbooking: refuse`, a write that makes a week's confirmed excess over capacity grow. |
| `request_not_open` | 409 | A `PUT`, a `fill` or a `withdraw` of a request that is filled or withdrawn. |
| `revision_conflict` | 409 | The `If-Match` revision of an allocation, a request or the settings has moved on, or the row kept changing under a `DELETE`, a `confirm`, a `fill` or a `withdraw` three times running. |
| `no_person` | 404 | `GET /me` for a caller `Self` answers no person for. |
| `dependency_unavailable` | 503 | `Deps.Projects`, `Deps.Workforce` or `Deps.Directory` is nil — never so in a running `planning`, which `MODULES` refuses without them. |

A caller without the operation's permission gets the access layer's uniform 403. **`PUT`**
on an allocation, on a request and on the settings takes `If-Match` with the row's
revision; `confirm`, `fill` and `withdraw` do not. Delivery C adds `non_compete` and the
wishes' codes.

## Enabling and disabling

`MODULES` is a positive allowlist; the smallest installation with Planning is

```text
MODULES=customers,projects,staff,planning
```

`planning` without `projects` fails configuration with `planning requires projects`,
and without `staff` with `planning requires staff` (S25), checked in `internal/config`
beside `time requires projects`; Customers is there because Projects requires it.
`Deps.Projects`, `Deps.Workforce` and `Deps.Directory` are therefore never nil in a
running `planning`; the module still answers `dependency_unavailable` (503) rather than
panic if one is.

Omitting `planning` means the module contributes no route, no permission and no
contract path; its paths answer the `/api` catch-all 404 and the switcher tile greys
out. Nothing of this module is read by another, so nothing else changes. The `planning`
schema is migrated regardless, so enabling it later needs no migration. Planning
contributes **no background worker**.

## What comes next

- **C — Assignments and wishes**: an allocation's terms — the notice, the minimum
  duration, the handover and the non-compete — with the earliest release and the
  handover start derived from them; the non-compete as the one hard refusal, excluding
  a candidate and refusing a write; the release and the wishes as a candidate's flags,
  the wishes in the sort; the person's wishes, without a word of free text, seen and
  resolved by the planners; and the calendar source.

## Endpoints

All under `/api/v1/planning`; every operation requires `planning:access`, and the table
names what it requires beside it.

| Operation | Permission | |
| --- | --- | --- |
| `GET /meta` | `planning:access` | The caller's capabilities, `hasPerson`, `workforceVisible`, the load units; the settings for `manage`. |
| `GET /settings` | `planning:manage` | The settings with their revision. |
| `PUT /settings` | `planning:manage` | Replace the fields sent, with `If-Match`. |
| `GET /projects` | `planning:view` | The plannable projects by code. |
| `GET /people` | `planning:view` | The people of a window. |
| `GET /board` | `planning:view` | People or projects against weeks, with the figures and the bars. |
| `GET /projects/{projectId}/plan` | `planning:view` | One project's rows, its requests, and its hours and demand per week. |
| `GET /people/{personId}/timeline` | `planning:view`, or the person's own | The person's employment, away spans, rows and flags. |
| `GET /unbooked` | `planning:view` | The people under the threshold within the horizon. |
| `GET /competences` | `planning:view` | Staff's competence catalogue as the caller may see it. |
| `GET /intake` | `planning:view` | The planned projects with their requests' coverage and their hours a week. |
| `GET /requests` | `planning:view` | At most 500 requests by project, status and window. |
| `POST /requests` | `planning:manage` | Create a request; answers it with its warnings. |
| `GET /requests/{id}` | `planning:view` | One request. |
| `PUT /requests/{id}` | `planning:manage` | Change an open request, with `If-Match`; answers it with its warnings. |
| `POST /requests/{id}/withdraw` | `planning:manage` | Withdraw an open request for good. |
| `GET /requests/{id}/candidates` | `planning:manage` | Who could fill the request, judged and sorted. |
| `POST /requests/{id}/fill` | `planning:manage` | Book a person on an open request; answers the request, the row and its warnings. |
| `GET /allocations` | `planning:view` | At most 500 rows by person, project, window and status. |
| `POST /allocations` | `planning:manage` | Create a row; answers it with its warnings. |
| `GET /allocations/{id}` | `planning:view` | One row. |
| `PUT /allocations/{id}` | `planning:manage` | Change a row, with `If-Match`; answers it with its warnings. |
| `DELETE /allocations/{id}` | `planning:manage` | Delete a row. |
| `POST /allocations/{id}/confirm` | `planning:manage` | Confirm a row and tell the person. |
| `GET /me` | `planning:access` | The caller's own confirmed bookings. |
| `GET /events` | `planning:view` | The events of a person, a project or a row. |
| `GET /stats` | `planning:view` | The dashboard card's figures. |
| `GET /stats/attention` | `planning:access` | The attention items, shaped by the caller's audience. |
