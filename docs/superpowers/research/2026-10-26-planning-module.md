# Planning module — research

The module the Staff research of 2026-10-11 (`2026-10-11-staff-module.md`, "R") and
the roadmap call **Staffing** is named **Planning** (module key `planning`, Norwegian
**Bemanning**) by the owner's decision of 2026-10-26, and starts **without shifts**.
This note settles what the first version is, from the research already on the record,
the products a Norwegian consultancy would otherwise buy, and the codebase as it stands.
The design spec `docs/superpowers/specs/2026-10-26-planning-design.md` starts from §8
and does not reopen it. Code references are against `main` at `9c4f1edd`; `srv/`
abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`, "the Staff
reference" `docs/src/content/docs/en/reference/staff.md`, "the Projects reference"
`docs/src/content/docs/en/reference/projects.md`, and `boundaries.md` the page
`docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. What is decided, and what this note adds

### 1.1 Decisions already on the record

- **One module owning every booking of a person's time** (R §10.9): allocation,
  assignment and shift, with requests, wishes, matching and trades, employee pages
  beside the manager's. The person, the employment, absence and competence remain
  Staff's.
- **Legally agnostic** (R §6.7, the owner's first decision of 2026-10-11): the
  statutory limits are warnings an installation can mark as hard, never built-in
  refusals.
- **The sequence** (R §10.11): Staff phase 1 → Entra → Staff phase 2 → this module.
  The first three are built; Entra phase 2 is in flight and touches nothing here.
- **Staff's side of the seam is built** (competence design S15, the Staff reference
  "The Workforce contract"): `contracts.Workforce` answers the catalogue and who holds
  what valid on a date under the Colleague rule; `PreferenceFit` is shaped in the
  reference and joins the interface with this module's first delivery; the
  availability declaration is data only, hard versus soft is this module's rule;
  customer and project wishes and *do not schedule with* wait for it.
- **2026-10-26, the owner**: the name is **Planning** / **Bemanning**, `planning` in
  every identifier; it stays a module of its own, not a part of Staff or Projects;
  **shifts and shift trading are left out for now**; Projects phase 5's allocations
  move into it so there is one capacity calculation.

### 1.2 What this note settles

1. What the first version books, and what it leaves for later (§3).
2. How the market shapes a booking, demand before a person is named, capacity, and
   matching — and which of it a first version cannot lack (§2, §4).
3. The one thing the products reviewed do not model and the owner's cases need: the
   assignment's notice period and customer terms (§5).
4. What the codebase gives and what it lacks: Projects has no phases and no pipeline
   state yet; Staff's seam answers competence but not capacity; project members are
   identity users while Staff's people are persons (§6).
5. The must-have list and the readings for the owner (§7, §8).

### 1.3 Why the name changed

`staff` and `staffing` differ by three letters in every identifier — `staff:access`
beside `staffing:access`, `@vantigo/staff-ui` beside `@vantigo/staffing-ui` — and the
collision never existed in Norwegian (*Ansatte* against *Bemanning*). *Planning* says
what the module does — books people's time — rather than whose time; *Scheduling* was
the fallback and reads as rostering, which is out; *Resourcing* is the PSA term and
calls people resources, which the rest of the product does not; *Workforce* is Staff's
contract already. The word is free in the repository: nothing names a screen or a
module "planning" today.

## 2. What the products do

Read 2026-10-26 from each product's public pages and help centres (Odoo Planning,
named by the owner, and the resource-planning specialists a consultancy compares it
with). Shift features are noted only where they shape a product's model. "Not stated"
means the pages did not say.

### 2.1 Odoo Planning — shift-shaped

The unit is the **shift**: a resource (empty means an *open shift*), a role, a project,
a sales-order item, a date — the only required field — and an **allocated time**
*derived* from the span and the employee's working schedule, never typed. Two states,
planned (striped) and **published** (full colour): the employee is told only on
publish. Capacity is per-employee working hours; time off and public holidays grey
the person out; the one rule is no two shifts at the same time. **Auto Plan** fills open
shifts under hard constraints (role, no overlap, time off, holidays, working hours,
contract period) and is off for anyone with flexible hours or no role. The employee
sees *My Planning*, can *ask to switch* with a same-role colleague, and may unassign
themselves back to an open shift when allowed. No percentage of a period, no tentative
state, no placeholder beyond the open shift, no availability or utilisation figure, no
request loop, no bench list. Priced per user; Planning is an ordinary app in the
all-apps tiers.

**For this module:** the wrong shape for allocations — one cannot say "80 % of Q3",
only stack shifts — and the right one for the shift phase later. Two things worth
keeping: *publish* as the moment the person is told, and per-role fields on a booking.

### 2.2 Float — the allocation, tentative and confirmed

The **allocation** is "the basic scheduling unit": a project (required), a phase and a
task; **hours per day** as the standard unit, a total over the range, or a
**percentage** of capacity; **confirmed** (the default), **tentative** (outline only)
or **completed**; notes; one allocation on several people. Two kinds of unnamed demand:
a *placeholder* person profile with no login and zero capacity, and an *unassigned
role* in a project's team whose allocations move to the person in one action; a
project is *draft, tentative, confirmed, completed or cancelled* and allocations on a
tentative project default to tentative. Capacity is 40 hours Monday–Friday by default,
overridden per person, minus time off and regional holidays (blocked); hours over a
day's capacity are **overtime** in red; utilisation is scheduled over capacity. **Smart
assign** ranks people for an unassigned role by role alignment and availability (role
match, adjacent, possible). Schedule by people or by project, a project plan with
phases and milestones, a people report with an *unassigned* filter and an *unscheduled
hours* metric. Priced per scheduled person (about $7–12 a month), viewers free.
Weakness: a percentage allocation ignores other allocations and takes one person, so
the two units do not compose.

### 2.3 Runn — contracted against effective capacity

The **assignment** is on a project for a person or a placeholder, in **hours a day,
hours a week, FTE or a capacity percentage**, work days × effort with time-off days
excluded; phases, non-billable, repeat, split, transfer. Tentativeness lives on the
**project** (*tentative* or *confirmed*), with a **scenario toggle** that flips every
tentative project's load in or out of availability, utilisation and the reports — the
one-switch answer to "can we take A and B". A **placeholder** is a role row on a
project with a rate and no capacity; *Placeholder demand (FTE)* is a planner mode.
Capacity comes from the person's **contract** (dates, work days, hours a day, cost,
role; one at a time): *contracted* capacity, and *effective* capacity after time off;
utilisation is judged against the effective figure. **Resourcing requests** are raised
on a placeholder (*raised → responded → hire proposed*, with comments); **Find person**
filters on role, skills with a 0–4 level and tags, shows the availability impact, and a
transfer deletes the placeholder unless kept. The *People planner* colours
availability from green (75–100 % free) to overbooked and sorts by most available —
the de facto bench list. A *contributor* sees their own hours and time off read-only.
Priced per resource seat (about $7–11, twenty minimum), placeholders free, requests on
the top tier. Weakness: tentative is project-level only.

### 2.4 Productive — requests with weightless previews

The **booking** is against a service in a budget or a deal, or an absence: **hours a
day, a percentage** of the capacity the cost rate defines, or a **total** spread
evenly; a date range; a **tentative** toggle; notes; repeat; split; a **booking
history**; a recycle bin. A **placeholder** has a name, a role, a cost rate, a
**capacity and a holiday calendar** — the one product where a placeholder has capacity
— capped at five or ten per tier; bookings on a *deal* are the pipeline, and a
*scenario builder* sandboxes services and bookings and applies them as tentative.
Tentative bookings do not count toward scheduled hours and show as a grey overlay;
pending time off does not reduce availability until approved. **Resource requests** on
a budget or deal: a title, a service, a range, an allocation, requirements (job title,
teams, tags, department, a preferred maximum cost, a preferred person), *pending →
resolved, rejected or cancelled*; the resolver sees people **sorted by best fit** with a
fit indicator (4 of 4) and builds **preview bookings that affect no real capacity**
until resolved; one permission resolves. Staff-level users never see tentative
bookings. Priced per user (about $10–29), requests and scenarios on the top tier.

### 2.5 Visma Severa — allocations as the split of an estimate

An **allocation** is a project member's hours on a project or **phase** over a range,
shown also as a share of the phase's estimate; allocations should not exceed the
estimate, and *unallocated* is the remainder — demand is visible per phase without a
placeholder. A **role allocation** is the placeholder, split among named people later.
Capacity is the work contract's daily hours and workdays against a 40-hour week;
absence adds **100 % load** (orange stripes) and holidays reduce the time available;
red at or over 100 %; an orange triangle marks a member whose availability conflicts
with the phase. No request, no matching, no tentative state. Priced per user (about
€25–69, resourcing from the middle tier). Weakness: absence as added load cannot show
a half day.

### 2.6 Kantata, Resource Guru, Hub Planner, Silverbucket, Teamdeck

- **Kantata** allocates at project level in a percentage of the workweek or hours,
  **soft** (striped, tentative) or **hard** (solid); *unnamed resources* from account
  roles take **soft allocations only** — hard needs a name; **resource requests** with
  required fields and a chosen approver who picks the person and approves or rejects
  with a reason. Weakness: allocations, scheduled hours and estimates are three hour
  concepts to reconcile.
- **Resource Guru** books hours a day, total hours or a time; **tentative** bookings
  consume no availability and are seen by the booker alone; a booking over the day's
  availability is a **clash** with three outcomes — the **waiting list** (saved,
  weightless, red stripes), *with overtime* (availability extended and flagged) or
  without; a booking approval workflow on the top plan. Priced per person ($5–12).
- **Hub Planner** (now Milient) books in a percentage or hours, keeps **unassigned
  work** as a demand pool, and lets a requester ask for a named person **or a
  skill group**, with approvers per resource or group and an audit trail; demand
  categories consume paid licences.
- **Silverbucket** fills an unassigned role through a request with a **match score
  0–100** over availability, competencies and role specification.
- **Teamdeck** is a calendar of bookings with availability, vacations and holidays, and
  a scheduled-against-timesheet report, at about $2–6 a seat.

### 2.7 What every one of them has

- A **person × project × date range** booking with **both units**, hours (a day or a
  week) and a percentage of capacity, interchangeable on the row.
- A **tentative** dimension — tentative/confirmed, soft/hard — excluded from the
  headline capacity figure but visible, and a project state that sets the default.
- **Capacity from the contract** (hours and working days), **reduced by approved time
  off and public holidays**, with a red over-booked state; Runn's *contracted* against
  *effective* is the cleanest pair.
- A **placeholder** that is scheduled like a person and whose bookings move to the
  person in one action.
- A **people timeline and a project timeline** over the same rows; **utilisation or
  availability per period**, sortable by most available — nobody ships a named bench
  list with a threshold and a horizon; Runn's sort and Float's unscheduled hours are
  the closest.
- The employee sees **their own confirmed bookings only**: Resource Guru hides
  tentative ones, Productive hides them from staff users, Odoo tells on publish.
- Repeat, split and notes.

### 2.8 What one or two have

A request lifecycle with an approver and **previews weightless until resolved**
(Productive, Kantata, Hub Planner; Runn by comments); **fit scoring** against a
requirement list (Productive, Silverbucket, Float's three labels); a **pipeline toggle**
that flips tentative work in and out of every number (Runn; Productive through deals);
**demand by role in FTE** (Runn); a **waiting list** and reversible overtime (Resource
Guru); the **contract as a capacity record** with a "no contract" state (Runn);
allocations as the **split of an estimate** with the remainder shown (Severa,
Kantata); a **booking history** (Productive, Resource Guru, Hub Planner); partial
transfer around time off (Runn); placeholders with real capacity (Productive).

### 2.9 What nobody has

**Notice periods, a notice-given date, an earliest release, a customer's minimum
duration, replacement approval, handover overlap or non-compete.** The nearest things
are Runn's contract end date, Float's and Resource Guru's warning when a booking falls
outside the project's dates, and Runn's leaver flow — move the person's work to a
placeholder, then find a person. An assignment with customer terms (§5) would be
unique in this set, and it is the owner's third case.

### 2.10 What this module takes from them

The specialists' model, not Odoo's: a share of a period on a project, tentative beside
confirmed on the **row** (Runn's project-level-only is the weakness to avoid), the
placeholder as a row in the capacity ledger, and the capacity arithmetic — contracted,
effective, booked, free — as the central view. From Productive: requests whose
candidates are sorted by fit and whose proposals weigh nothing until confirmed. From
Runn: the pipeline switch for intake and the contract as the capacity's source, which
here is Staff's employment. From Float: the project state setting the row's default and
the fixed end date warned about when time off lands inside it. From Odoo: the moment
the person is told is a state change. Left out on purpose: the waiting list (a
tentative row does the work), placeholders with capacity of their own (demand is a
request, never a fictitious person), and licences per placeholder.

## 3. What the first version books

R §6.7 named three kinds of booking: an **allocation** (a share of a period on a
project), an **assignment** (a contracted placement with its own notice and customer
terms) and a **shift** (a start and a stop on a date for a position or location). With
shifts out, two remain, and they are closer than R made them: an assignment is an
allocation that is also a contract. Every assignment has a person, a project and a
period; what it adds is the terms. Modelling it as a second person × project × period
table would duplicate the booking and make the timeline sum two tables.

**Recommendation (reading 1):** one booking table, `allocations`, and **contract
terms** as an optional one-to-one attachment. An allocation with terms is what the
interface calls an *assignment* (*oppdrag*); the capacity calculation, the timeline and
the board see one kind of row. The terms carry the notice period, when notice was
given, the minimum duration, whether the customer must approve a replacement, the
handover overlap and the non-compete; from them the row derives its **earliest
release** (§5).

**Demand before a person is named** is one entity too. R separated a *placeholder* (a
role on a pipeline project with requirements, for intake) from a *staffing request* (a
project wants competence X at level Y from a date, open until filled); the products
(§2) show them to be the same row at different ages — an unnamed booking with
requirements that either gets a person or does not. **One entity, `requests`** (*behov*),
with a project, a period, a load, competence requirements and a status `open`,
`filled` or `withdrawn`. Open requests are the demand the intake view measures
candidates against and the list the matcher works from; a filled one keeps the
allocation that filled it.

**What stays out of the first version**, and where it goes:

- Shifts, rostering, shift templates, trades and the phone-first rota view: a later
  phase of this module, where the capacity calculation already subtracts them.
- The revenue forecast from planned hours (planned × bill rate): with Projects, once
  allocations carry a billing line; the roadmap entry under Projects moves here.
- Automatic requests from a foreseeable drop (approved parental leave, a reduced
  position, an employment end): wants absence as events; the item is listed, the
  automation is phase 2.
- Access following the assignment (groups, mailboxes): Staff's groups have a rule
  engine on Staff's facts; a rule on a booking is a contract from this module to Staff
  and a phase of its own.
- Holiday approval seeing the allocations it conflicts with: a read contract Staff
  consumes optionally, mirrored on `contracts.Timesheets`, in phase 2.
- External capacity (freelancers, subcontractors) beyond what Staff registers: Staff
  already holds a `freelancer` relationship type and the hired-out forms, so a person
  of that kind is booked like anyone; a *company* as capacity is later.
- Rate cards, cost and margin on the plan: later, with the forecast.

## 4. The capacity calculation — the one rule everything hangs on

Every product in §2 computes the same thing and names it differently: what a person
can work in a period, minus what is already booked. The first version computes it one
way, in one place, and every view reads it:

```
capacity(person, day)  = 0 on Saturday, Sunday and a public holiday (internal/holidays)
                       = hoursPerWeek(person, day) / 5 otherwise
hoursPerWeek           = the full position's hours × the FTE in force that day, summed
                         over the person's employments in force (Staff's terms rows)
available(person, day) = 0 when the day lies in one of the person's away spans, else capacity
away spans             = the approved and registered absences coalesced into whole-day
                         spans and clipped to the window, as Staff's calendar shows a
                         colleague: no share, no hours, no type
booked(person, day)    = Σ over confirmed allocations covering the day of
                           hoursPerWeek-load / 5, or percent-load × capacity
tentative              = the same over tentative ones, shown apart and never a refusal
free                   = available − booked
load %                 = booked / capacity, over the days of the week that have capacity
```

Three consequences:

1. **The unit is hours per week or a percentage of the position**, the planner's
   choice per row, stored as typed. A percentage follows the contract when the FTE
   changes; hours do not. Both products shapes (§2) exist because both are needed: a
   consultant is "80 % on A" while a specialist is "8 hours a week on B".
2. **Absence does not shrink a booking**; it shrinks what is available, so a week of
   ferie under a 100 % allocation shows as a conflict the planner sees, not as a
   silent re-plan. This is Float's and Runn's behaviour and the owner's "holiday
   against staffing" case (R §6.7).
3. **Overbooking is a warning that an installation can make a refusal** — the
   legally-agnostic rule applied to the one limit a consultancy's plan has. The
   statutory working-time caps (aml. kap. 10) bind hours worked, which Time records,
   not hours planned; they return with shifts.

The inputs Staff owns — the hours in force, the FTE, the days away — are not on
`contracts.Workforce` today (§6.3). The contract gains a **capacity read** that answers
them as derived figures per person and day range, never the terms rows, the absence
types or an absence's share, so a planner learns "available 30 of 37.5 hours this week"
and not why. A day partly away counts as a day away: the share — a graded sykmelding's
degree, an hour's appointment — is a fact Staff shows a manager and keeps from a
colleague, and the planner is a colleague.

## 5. The assignment's notice period and customer terms

None of the products in §2 model a placement as a contract (they confirm this: a
booking has dates and a status, nothing about notice). The owner's third case (R §6.7)
rests on it: a consultant on a customer project is under an *oppdragsavtale* with an
*oppsigelsestid* of one to three months, and cannot start elsewhere before the earliest
day the customer must release them. The terms worth columns from the start, seen in
Norwegian consultant contracts:

| Term | Shape | Used by |
| --- | --- | --- |
| Notice period | a count and a unit (`days`, `weeks`, `months`), as Staff's terms carry notice | the earliest release |
| Notice given on | a date, set when either side gives notice | the earliest release |
| Minimum until | a date before which the customer need not release | the earliest release |
| Replacement needs the customer's approval | yes or no | the matcher's proposal, marked *needs customer approval* |
| Handover overlap | a count of days | the proposed successor's start, overlapping the leaver's end |
| Non-compete | until a date, toward named customers | a hard filter in the matcher: no booking on those customers' projects before the date |
| Reference | the contract's document or number, text | the planner |

**Earliest release** = the latest of the booking's end as planned, the minimum-until
date, and notice given on plus the notice period when notice is given — or simply the
planned end when no term binds it. It is derived on read, never stored, and it is the
date the timeline shows and the matcher uses as the earliest start elsewhere.

**Non-compete needs customers.** Projects require Customers, so a `planning`
installation always has `contracts.CustomerDirectory`; the term stores opaque customer
ids and the matcher compares them to the project's customer through
`contracts.ProjectDirectory`. The customer's name for the planner comes from the
directory. No new dependency arrives: the module reads Projects, and Customers comes
with it.

## 6. Where the codebase stands

### 6.1 Projects: no phases, no pipeline state, members are users

The Projects reference's domain model (`projects.md` "Domain model"): a project has a
`status` of `planned`, `active`, `on-hold`, `completed` or `cancelled`, every
transition allowed, `startDate`/`endDate`, `budgetHours`, roles per identity user
(`manager`, `member`, `viewer`), billing lines, work types, billing milestones and
tasks. **Phase 4 (delivery milestones, a timeline) and phase 5 (capacity planning) are
unbuilt** (ROADMAP.md "Projects"). So:

- **Demand per phase** (R §6.7 case 1) has no phase to hang on. A request carries its
  own period instead; when delivery milestones arrive a request may name one.
- **The pipeline state** (bid, tender, likely) does not exist. `planned` is the one
  status before `active`, and `OpenForWork` is false for it. The first version treats
  a `planned` project as the pipeline: its allocations default to tentative, its
  requests are the intake's demand. A finer pipeline is Projects' to add.
- **Project members are identity users**; Staff's people are persons, linked to a
  user through the self-service link when they have one (the Staff reference "The
  self-service link"). A booking is of a **person**, since capacity, absence and
  competence are the person's, and a person need not have an account. The module
  needs the person's user for two things only — "my bookings" and mail — and gets it
  from the Workforce contract (§6.3).
- **The directory lists no projects for a planner.** `contracts.ProjectDirectory`
  (`srv/contracts/projects.go:80`) answers `Project`, `Projects(ids)`,
  `ProjectsForUser`, `ProjectsForCustomer` and `ProjectByCode`; nothing lists every
  project in a status. Time's authorization reads `ProjectsForUser`
  (`srv/time/authorize.go:92`), the caller's own. A planner books on projects they hold
  no role on, so the directory gains a listing by status, and the module's project
  picker and board read it.
- **What a planner may see of a project** is a question the directory half answers:
  `ProjectEntry` carries the code, name, customer, status and the financial
  `Currency` and `DefaultBillRate` the module must not surface (the entry's own
  comment). Projects' "outsiders get 404" rule is Projects'; the module shows code and
  name to its own permission holders, as Time shows them on a timesheet.

### 6.2 Staff: the register, the hours, the absence and the manager rule

- A person's **employment** has a start, an end and dated **terms**; the row in force
  carries `ftePercent`, the working-hours scheme and `fullTimeHoursPerWeek` (the
  Staff reference "Terms", "The hours rule"); a colleague reads the dates, the type
  and the FTE, a manager every field but pay.
- **Absence** rows carry a person, a type, first and last day or one day's hours, a
  percentage, a status and a sykmelding's degree; a colleague is shown none of the rows:
  the approved and registered days are **coalesced into whole-day spans**, clipped to
  the window, with no id, no type, no status, no share and no hour row ("Absences", the
  audience table; "The calendar"). So *which days* a person is away is a colleague-visible
  fact; *how much of a day*, and why, is not.
- **The one manager rule** decides who is a person's manager today ("Managers and the
  org view"); it is Staff's and never restated. A wish read by "the person's
  manager" needs Staff to answer the question; the first version gives wishes to the
  planners and the person, and asks Staff later (reading 9).
- **The self-service link** makes a user a person's Self — unless the accounts worker
  blocked the link as someone else's (`mismatch`, `account_not_managed`), when the user
  gets nothing ("Field groups and audiences"). Who is Self is Staff's rule, so "me"
  must be Staff's answer, not a match on a user id the module does (reading 8).
- **The Colleague rule** on the Workforce contract answers people active today only
  (competence design reading 9: "a person starting next month is Staffing's
  question"). Planning months ahead needs next month's starter (reading 7).
- Staff keeps **events with an actor snapshot**, **per-person advisory locks**, a
  typed one-row **settings** table, computed-on-read **attention items**, mails
  through `UserDirectory.Notify` after commit and outside any transaction, and a
  `CalendarSource` of its own rows (`srv/staff/calendar_source.go`). Each is the
  pattern the module copies.

### 6.3 The seam: what `contracts.Workforce` answers, and what it must add

Today (`srv/contracts/workforce.go`): `Competences(ctx)` and `HeldCompetences(ctx, on,
personIDs)`, Staff-gated on `staff:view` and the person active today. The module needs
four more answers, each a derived fact and none a Staff table:

| Read | Answers | Why |
| --- | --- | --- |
| `People(ctx, from, to)` | the people in employment on today or a later day of the window: id, display name, employee number, department, the linked user id when the link gives Self, and their employment days inside the window | the board's rows, the mails |
| `Capacity(ctx, from, to, personIDs)` | per person: segments of hours per week in force, and the days away as whole-day spans | §4 |
| `Self(ctx)` | the caller's own person under Staff's Self rule, for `staff:access` | "me", the calendar source |
| `HeldCompetences` | as today, for a person in employment on the day asked | the matcher's filter |
| `PreferenceFits(ctx, personIDs, booking)` | the fit per person, shaped in the Staff reference, as a batch | the matcher's sort |

The gate stays Staff's (rule 12): `staff:view` for the colleague reads, `staff:access`
for `Self`. The widening from "active today" to "in employment on today or a later day
of the window asked", with the window bounded, is the one change to the Colleague rule
and is reading 7.

### 6.4 Patterns to reuse, and the scaffold

The scaffold is `boundaries.md` "Adding a module", step by step; Staff is the nearest
copy (`srv/staff/module.go`, `apps/staff/frontend`, the host's `routes/staff`). From
Staff: `authorize.go` (one function loads the caller's permissions once), `events.go`,
`locks.go`, `attention.go`, `settings.go`, `notifications.go`, `calendar_source.go`,
`contractscalls.go` (one accessor per contract call, never inside a transaction),
`strictbody.go`. From Time: how a module reads `ProjectDirectory` before its own
transaction opens. From the host: the customer page composing other modules' tabs is
how the Staff person page and the project page gain a **Planning** tab without either
module depending on the other.

`mig/00071` is the highest migration; the module's baseline takes the next free
number when its first task lands. `MODULES` gains `planning requires projects` and
`planning requires staff` in `internal/config`, beside `time requires projects`.

## 7. The must-have list

From §2's near-universal features, the owner's cases and §6's gaps, the first version
**must** have:

1. Allocations of a person on a project over a period, in hours per week or a
   percentage, tentative or confirmed, with a title and a short note.
2. The capacity calculation of §4, from Staff's hours, FTE and absence, with public
   holidays, and overbooking as a warning an installation can make hard.
3. The board: people as rows, weeks as columns, load per cell, tentative shown apart,
   filtered by department and by competence; and the same by project.
4. The project's plan and the person's timeline — the latter forward-looking with the
   employment's end, days away, allocations with their ends and earliest release, and
   a certificate expiring inside a booking.
5. Requests with competence requirements, open until filled or withdrawn, and the
   candidates for one: held competence at the level, valid through the period;
   available load; not excluded by a non-compete; sorted by fit, free load and time on
   their current project.
6. Intake over `planned` projects: each request covered, partly covered or uncovered,
   with the candidates behind it.
7. The unbooked list under a threshold within a horizon, with the roll-off date, and
   the attention items: unbooked, request open past its lead, overbooked, a booking
   ending with no follow-on.
8. Assignment terms with the derived earliest release; the move wish from the person;
   the matcher honouring notice and non-compete.
9. My planning: a person's own bookings and wishes, and their allocations on the
   Calendar app.
10. Three permissions, the dashboard card, events with the actor, and the reference,
    user and administration pages in both languages.

**Should**, in the first version if it fits a delivery: the handover overlap proposed
with a move; key-person risk on the intake view (a requirement one person meets).
**Later** is §3's list.

## 8. Readings for the owner

Each is how this note read a decision or a gap; the spec takes the recommendation
unless the owner says otherwise.

1. **One booking table with optional contract terms**, not an assignments table
   beside allocations (§3). Recommended.
2. **Requests are the placeholders.** One entity for an unnamed need, whether on a
   pipeline project or a running one (§3). Recommended.
3. **A `planned` project is the pipeline** until Projects adds a finer state; its
   allocations default to tentative (§6.1). Recommended.
4. **Load is stored as typed**, hours per week or a percentage, and converted per day
   through the contract in force (§4). Recommended.
5. **Overbooking is a warning, hard by a setting** — the legally-agnostic rule's one
   application before shifts (§4). Recommended.
6. **Capacity reaches the planner as derived figures** — hours a week in force, and
   the days away as whole-day spans with no share — never terms rows, absence types or
   an absence's percentage or degree (§4, §6.3). A day partly away counts as away.
   Recommended.
7. **The Colleague rule widens to "in employment on today or a later day of the window
   asked"** for the people, capacity and competence reads, so a planner books next
   month's starter and never lists yesterday's leaver; the window is bounded (thirteen
   weeks back, three years ahead) so a past window cannot list leavers; the directory
   itself is untouched. Recommended; the owner should confirm, since a `staff:view`
   holder learns a starter's name and start date earlier than the directory tells them.
8. **"Me" is Staff's answer** — a `Self` read under Staff's own Self rule, for
   `staff:access` — never a match the module does on a user id; and the contract
   answers a person's linked user id to a `staff:view` holder **for mail alone**, since
   `UserDirectory.Notify` takes a user. Recommended, narrowly: the id and nothing else
   of the link.
9. **Wishes are read by planners and the person, not the manager**, in the first
   version: the manager rule is Staff's, and asking it through the contract is a
   later read. Recommended as a first step; the manager's view joins when Staff answers
   "manages today".
10. **No free text on a wish**, as on a preference: a project, a date, and whether the
    person wants off it or wants more. A reason drifts into health in a sentence.
    Recommended.
11. **Planners hold `planning:manage`; a project's manager does not book by that role
    alone** in the first version. The products reviewed let project managers request,
    not book; a request is what a project manager files here. Recommended; to revisit
    if small firms find it a detour.
12. **No worker in the first version.** Attention items are computed on read; mails
    are sent after a write; the unbooked list is a view, not a digest. Recommended; a
    weekly digest is a later entry.
13. **An erased person's bookings keep their id** and show as an erased person, as an
    invoice keeps an erased customer's id; the wishes, the only rows that are the
    person's voice, are deleted with the person through no new slot, because they are
    removed when resolved and hold no text. The owner should confirm the retention of
    allocations is acceptable as planning data.
14. **Planning requires Projects and Staff** at startup, not optionally: a plan without
    projects has nothing to book on and without people nobody to book. Recommended.
