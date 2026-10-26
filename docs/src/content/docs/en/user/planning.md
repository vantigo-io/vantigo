---
title: Planning
description: Who works on what — the Plan board of people against weeks with its colours, groupings and filters, the allocation sheet with its warnings and refusals, Requests and the request sheet, filling a request from its candidates, the Intake of planned projects, a project's plan with its demand, a person's timeline with its flags, the Unbooked list, My planning and the mail a person gets, the Planning settings, the dashboard card and its attention items, and the Planning tabs on a person's page in Staff and a project's page in Projects.
sidebar:
  order: 46
sources:
  - apps/planning/frontend
  - apps/host/frontend/src/routes/planning
---

The Planning app is where the planners book people on projects, week by week, and
where everyone sees what they are booked on. A booking is an **allocation**: one person,
one project, a period, a load — hours a week or a percentage of the person's position —
and whether it is **Tentative**, the planners' draft, or **Confirmed**, the plan. The
people come from [Staff](/en/user/staff/), with their employment, their capacity and
their days away; the projects come from [Projects](/en/user/projects/). Planning copies
nothing of either: a name you change in Staff or Projects is the name the plan shows.
The rules behind every screen — the capacity calculation, the warnings, the refusals
and the permission each request is checked against — are in the
[Planning module reference](/en/reference/planning/); this page is about what you do on
each screen.

Planning has allocations, the board, the lists and the person's own view, and
**requests** — what a project needs before anybody is named — with the people who could
fill them and the intake of planned projects. Assignments with their contract terms, and
the person's wishes, come in a later delivery.

## The app and its sidebar

The **Planning** tile is shown to everyone with `planning:access`. The sidebar shows the
areas your permissions give you:

| Area in the sidebar | Who sees it |
| --- | --- |
| **Plan** (the board) | `planning:view` |
| **Requests** | `planning:view` |
| **Intake** | `planning:view` |
| **Unbooked** | `planning:view` |
| **My planning** | everyone with `planning:access` |
| **Planning settings** | `planning:manage` |

The app opens on **Plan** for those who may see it and on **My planning** for everyone
else. A project's plan and a person's timeline have no sidebar entry: you open a
project's plan from the board, Requests or Intake, or as the
[Planning tab](#on-the-project-page) on the project's page in Projects, and a person's
timeline from the board, the Unbooked list, the dashboard or the person's page in Staff.

The plan is built on the people Staff answers you, so a planner also needs **View the
staff directory** (`staff:view`) from Staff. Without it the board does not pretend the
plan is empty: it says **Staff shows you nobody** and asks you to have an administrator
give you the permission.

The search in the top bar knows the screens above, and offers **New allocation** and
**New request** to a planner holding `planning:view` and `planning:manage`: the first
opens the board with the allocation sheet, the second Requests with the request sheet.

## The plan

**Plan** (`planning:view`) shows who is booked on what: one row per person in employment
in the weeks shown, one column per week, numbered (**W42**) with the current week
marked.

Each cell is the person's week:

- The big figure is the **load** — the week's confirmed hours as a percentage of the
  person's capacity — and under it the hours booked, with the tentative hours after a
  plus when there are any. The capacity is the person's position as Staff has it —
  the full-time hours times the FTE, five days a week — less weekends and public
  holidays. **No capacity** means the week has none: the person is not employed in it,
  or it is all holidays.
- The colour says how full the week is: grey is **free** (under 50 %), green **fine**
  (50 to 90 %), blue **full** (90 to 100 %) and orange **over** (more than the
  capacity).
- A hatched strip along the bottom is the **tentative** share. Tentative hours count in
  no figure — not the load, not the free hours — they are only shown apart.
- A cell turns **red** when the person is booked in a week where they are away on one
  of its days. Planning never shrinks a booking because of absence: a week away under a
  booking is a conflict for you to resolve, not a silent change of plan. The board
  never says what kind of absence it is, or how much of the day.

Hold the pointer over a cell to read its figures: the hours booked of the capacity, the
tentative hours, the free hours, and **Away part of the week** or **Booked in a week
with days away**.

Above the grid:

- The arrows move back or forward by as many weeks as are shown, and **This week**
  comes back. The board goes back thirteen weeks at most, and three years ahead.
- **Weeks shown** — 4, 8, 12 (the default) or 26 weeks.
- **Rows** — **People**, or **Projects**: each project with a booking in the weeks
  shown, with its hours summed, followed by a row for each person booked on it, whose
  booked hours are those on that project.
- **Department** narrows the board to the people of one department; **Project** to the
  people booked on one project; **Competence** to the people who hold one competence
  from Staff's catalogue — a skill at any level, or a certificate verified and valid —
  on the first day shown, or today when the board starts in the past.
- **Show tentative** (on) draws the tentative hours; turn it off to see the confirmed
  plan alone.
- **Include pipeline** (on) counts the bookings on projects in the status **Planned** —
  the bids. Turn it off to read the plan of what is firm: can we take on that bid, or
  that one?

Every choice is in the page's address, so a link shows the same board.

Press the arrow before a name to open the row: its allocations show as bars over the
weeks, each named by its project (or, under **Projects**, by its person). Press the name
itself to open the person's [timeline](#a-persons-timeline), or the project's
[plan](#a-projects-plan).

A planner (`planning:manage`) can press a cell to book that person, or that project,
for that week, press a bar to change its allocation, and press **New allocation** at
the top of the page.

## Allocating someone

The allocation sheet opens at the side — from the bottom on a phone — with **New
allocation** or **Change allocation** at the top. It is `planning:manage`'s; nobody else
is offered it.

1. **Person** — type a name or an employee number. Only people Staff answers you are
   offered.
2. **Project** — type a code or a name. Projects that are **Planned**, **Active** or
   **On hold** are offered.
3. **Period** — the first and the last day, both included.
4. **Load** — choose **Percent of position** (more than 0 and at most 100) or **Hours per
   week** (more than 0 and at most 80), and type the number. A percentage follows the
   person's position: when their FTE changes, the hours follow. Hours a week stay what
   you typed. The sheet starts with the unit the [settings](#planning-settings) name.
5. **Status** — **Tentative** or **Confirmed**. A booking on a project in the status
   **Planned** starts tentative, on any other confirmed, until you choose yourself.
   *Tentative* is the planners' draft: shown apart, counted in no headline figure and
   not shown to the person. *Confirmed* is the plan: the person sees it under **My
   planning** and is told by email.
6. **Role** (optional, at most 100 characters) — the role as the project names it, such
   as *Frontend developer*. Everyone who sees the plan reads it, and so does the person.
7. **Note** (optional, at most 500 characters) — for the planners alone. It is never
   shown to the person and never mailed, and the history records that it changed, never its text.

Press **Save**. When nothing needs saying, the sheet closes with *The allocation is
saved*.

### Warnings

Some consequences do not stop the save; the sheet says **Saved — but look at this** and
lists them:

- *More is booked than the person's capacity in* … — the weeks where the person's
  confirmed bookings now exceed their capacity.
- *The person is booked while away in* … — the weeks where the bookings fit the
  capacity but not the days the person is at work.
- *The period goes beyond the project's start or end date.*

Only confirmed bookings are weighed: a tentative one never overbooks anyone.

### Refusals

Some things are not saved at all. The sheet says **Could not save** with the reason, or
marks the field:

- *A day of the period lies outside the person's employment.* — nobody can be booked
  before they start or after they leave.
- *The project is completed or cancelled: it takes no new booking and no change.* — the
  bookings already on it stay, and can be deleted.
- *At most three years (1096 days)* on **Period**.
- *This would book more than the person's capacity, and the settings refuse
  overbooking.* — only when **Overbooking** is set to **Refuse** in the
  [settings](#planning-settings). A change that keeps or shrinks an overbooking already
  there is still saved, with the warning, so a plan can be mended one booking at a
  time.
- *Somebody else saved this; it has been reloaded* — another planner changed the same
  allocation first. Look at what is there now and make your change again.

### Change, confirm or delete an allocation

Open an allocation from its bar on the board, on a project's plan or on a person's
timeline, or with **Edit** in the actions menu of a project's allocation list. Change
what you need and press **Save**.

- **Confirm** (on a tentative allocation, in the sheet or the actions menu) makes it
  part of the plan. It is judged as any change: a closed project refuses it, and an
  overbooking warns — or is refused, when the settings say so.
- To set a confirmed allocation back to tentative, choose **Tentative** and save.
- **Delete** asks *Delete this allocation?* and removes the booking from the plan.

The person is **mailed**, in their own language, whenever a booking of theirs becomes
confirmed, when the period, the load or the project of a confirmed booking changes,
when a confirmed booking is set back to tentative, and when it is deleted. The mail
names the project's code and name and the dates — with the load, when the person is
planned or the booking changed — and links to **My planning**; it never carries the
note. Moving a confirmed booking to another person
tells the first that they are no longer planned on it, and the second that they are. A
tentative booking is never mailed about, and a person without a user is never mailed.

## Requests

A **request** is what a project needs before anybody is named: a title such as *Backend
developer*, a period, the hours a week, and the competences the person must hold. It is
**Open** until somebody fills it, **Filled** while a booking fills it, or **Withdrawn**.
An open request is the project's demand: it shows on the project's plan as the
**Demand** row and in the [intake](#intake).

**Requests** (`planning:view`) lists them, by first day: the title with its
requirements under it, the project — press it to open the project's
[plan](#a-projects-plan) — the period, the hours per week and the status, with
*Filled by* and the person's name on a filled one. The list opens on the **Open** requests;
**Status** switches to the filled or the withdrawn ones, or to **All statuses**, and
**Project** narrows it to one project. Both are in the page's address, so a link shows
the same list. A list is at most 500 requests, the oldest first — the page says so when
it is cut.

A planner (`planning:manage`) presses a request's title to open it, **Candidates** to
[fill it](#filling-a-request), and **Withdraw** on an open one, and files a new one
with **New request** at the top of the page.

### The request sheet

The request sheet opens at the side — from the bottom on a phone — with **New request**
or **Change request** at the top. It is `planning:manage`'s.

1. **Project** — type a code or a name. Projects that are **Planned**, **Active** or
   **On hold** are offered.
2. **Title** — what the project needs, at most 100 characters.
3. **Period** — the first and the last day, both included, at most three years.
4. **Hours per week** — more than 0 and at most 80. A request is always in hours a week:
   it has no person whose position a percentage could be read against.
5. **Requirements** (optional, at most 10) — press **Add requirement** and pick a
   competence from Staff's catalogue, skills and certificates apart. Each competence
   can be named once. On a competence with levels, **Lowest level** asks for at least
   *aware*, *basic*, *independent*, *experienced* or *expert*; left at **Any level**,
   holding it is enough. A person meets a requirement when Staff has them holding the
   competence on the request's first day — a skill at or above the level, a certificate
   verified and valid. Remove one with its bin.
6. **Note** (optional, at most 500 characters) — for the planners alone.

Press **Save**. When nothing needs saying, the sheet closes with *The request is saved*;
when the period goes beyond the project's start or end date, it is saved and the sheet
says so under **Saved — but look at this**.

The sheet refuses what the allocation sheet refuses for a project: a project completed
or cancelled takes no new request and no change, and *Somebody else saved this; it has
been reloaded* means another planner changed the request first.

A filled or withdrawn request opens in the sheet to be read, not changed: *A filled or
withdrawn request takes no change.* Change the allocation that fills it instead, or file
a new request.

### Withdraw a request

**Withdraw** on an open request asks *Withdraw this request?* and closes it: it stops
counting as demand, and it is never reopened. A filled request cannot be withdrawn; to
undo a fill, delete the booking that fills it, and the request opens again.

## Filling a request

**Candidates**, on a request in Requests, in Intake or on the project's plan, opens
the people who could fill it at the side (`planning:manage`). The top line repeats the
request's project, period and hours a week.

A candidate is a person Staff answers you who is employed through the whole period and
holds every requirement. Each card shows:

- the name, with the employee number and the department;
- **Has the time** when the person's free hours over the period cover what the request
  would book, or **Partly free** when they do not — and under it, *28 h free of 74 h
  needed*. Free hours are the person's capacity over the period less their days away
  and their confirmed bookings; tentative ones count for nothing, as on the board. What
  the request needs is its hours a week spread over the person's working days, so a
  public holiday needs nothing;
- **Fit**, a score out of 100 for how well the booking matches the preferences the
  person keeps in Staff ([What I want](/en/user/staff/#what-i-want)). Each preference
  the request goes against lowers it, the more so the more it matters to the person,
  and *Lowered by* names the kinds — never what the person wrote. A request names only
  its hours a week, so today it is **Most hours a week** that can lower it;
- **Covers it** when the person has the time, holds every requirement and carries no
  flag;
- *Certificate expires* with the day and the certificate, when a required certificate
  runs out before the request's last day;
- *On PROJECT for N days* — the project of the person's confirmed booking today, and
  how long they have been on it.

The people with the time come first, then by fit, then by free hours, and between equals
the one longest on their current project first, so the planner can let someone move on.

**Show partial matches** also lists the people who miss a requirement, with *Missing*
and the competences in red. When nobody fits, the panel says **Nobody fits this
request**.

To fill the request:

1. Choose **Fill as** — **Tentative** or **Confirmed**. It starts tentative on a project
   in the status **Planned**, confirmed on any other.
2. Press **Fill** on the person's card.

Filling books the person on the project for the request's period at its hours a week,
with the request's title as the role, and marks the request **Filled**. The booking is
an allocation like any other: it is weighed as the allocation sheet weighs it, warns —
*Filled — but look at this* — or is refused under **Could not fill the request** for the
same reasons, and when it is confirmed the person is mailed. Change the booking as any
allocation, and the request stays filled. **Delete** the booking and the request is open
again.

A filled or withdrawn request still shows its candidates — who else could have filled
it — but offers no **Fill**.

## Intake

**Intake** (`planning:view`) is the pipeline: every project in the status **Planned**,
each with its customer and dates — press its name to open the project's plan — and
three figures in hours a week: **Requested** by its open requests, **Tentative** and
**Booked** (confirmed) by its allocations. Under them are its open and filled requests,
each with its period, its hours a week and its **Coverage**:

- **Covered** — the request is filled, or at least one person covers it: has the time,
  holds every requirement and carries no flag.
- **Partly covered** — people who hold every requirement exist, but none covers it: they
  are short of time, or a required certificate runs out during the period.
- **Uncovered** — nobody employed through the whole period holds every requirement.

**Key person** marks an open request exactly one person covers: if that person is taken,
nobody else can. On an open request, *Candidates who cover it* gives their number. A
planner presses **Candidates** to fill it from here.

A project **On hold** is not in the pipeline, and a withdrawn request is not listed.
When no project is planned, the page says **No planned project**.

## A project's plan

A project's plan (`planning:view`) opens from a project's name on the board grouped by
**Projects**, in Requests or in Intake. Its header names the project with its status,
its customer and its dates.

- **Hours per week** — the project's **Booked** (confirmed) and **Tentative** hours each
  week for twelve weeks from this week, and its **Demand**: the hours its open requests
  still ask for. A request counts its hours a week in every week it covers, and a week
  it covers in part by its working days — a request from a Wednesday counts three
  fifths of its hours in its first week, and a public holiday counts nothing, exactly
  as a booking in hours a week books nothing on one. Under the figures, the allocations are bars named by the
  person, and the open requests orange bars named by their title.
- **Allocations** — each booking on the project in those weeks: the person (with the
  planners' note under the name, for a planner), the role, the period, the load and the
  status. A planner's actions menu on each row offers **Edit**, **Confirm** (on a
  tentative one) and **Delete**.
- **Requests** — the project's open and filled requests in those weeks: the title with
  its requirements (and the planners' note, for a planner), the period, the hours per
  week and the status, with who fills a filled one. A planner presses **Candidates** to
  [fill one](#filling-a-request), and the actions menu offers **Edit** on an open one —
  **Open** on a filled or withdrawn one, which shows it read-only — and **Withdraw** on
  an open one.

A planner books someone on the project with **New allocation**, and files what it needs
with **New request**, which opens the [request sheet](#the-request-sheet) with the
project and the weeks shown as its period.

## A person's timeline

A person's timeline opens from their name on the board or on the Unbooked list, from
a dashboard item, or as the **Planning** tab on their page in Staff. It looks forward:
today and the 26 weeks after.

- **Employment** — the person's employment as a band, with its end when one is known.
- **Days away** — the spans the person is away, from Staff's approved and registered
  absence, with no type and no share: a day partly away counts as away.
- **Allocations** — each booking as a bar, and below the bars a list with the project and
  the role, the period, the load, the status and the **Flags**:
  - **After the employment ends** (with the last day) — the booking runs past the end of
    the person's employment. A booking stays when an employment ends after it was made;
    this flag is how you find it.
  - **Certificate expires** (with the day) — one of the person's certificates in Staff
    runs out during the booking. Renew it in Staff, or plan for it.

A planner opens a bar to change its allocation. With `planning:view` you see anyone's
timeline whole, tentative bookings included. Your own timeline, with `planning:access`
alone, shows your confirmed bookings and nothing else; your employment and your absence
are on [My page](/en/user/staff/#my-page) in Staff.

## Unbooked

**Unbooked** (`planning:view`) lists the people with too little confirmed work ahead:
everyone in employment whose confirmed load over the coming weeks is under the
threshold. The threshold (50 % by default) and how many weeks ahead the list looks (six
by default) are in the [settings](#planning-settings), and the page's description names
both with the dates.

Each row has the person (open their timeline from the name) with their employee
number, the department, the **Load** over those weeks, the **Free hours** and **Rolls
off** — the last day of their latest confirmed booking that ends in those weeks, or
**Free now** when they have none. Those free now come first, then by the day they roll
off, then by load. Someone away throughout those weeks, or starting after them, is not
listed: they are not unbooked, they are away. When nobody is under the threshold, the
page says **Everybody is booked**.

## My planning

**My planning** is every user's own view, with `planning:access`: what you are booked on
from today, as the planners have confirmed it — one card per booking with the project,
the load, your role and the dates. Tentative bookings are the planners' draft and are
not shown here, and neither is any planner's note. When a planner confirms, changes or
removes a booking of yours, you are told by email, and the mail links here.

If the page says **No person is linked to you**, Staff has no person record linked to
your user, so there is nobody to show bookings for. Ask HR to link your user to your
record.

## Planning settings

**Planning settings** (`planning:manage`) has six fields. Press **Save settings** when
you are done; if another planner saved first, the page reloads and you make your change
again.

- **Overbooking** — **Warn** (the default): a booking that takes a week past the
  person's capacity is saved, and the planner is warned about the weeks. **Refuse**: a
  booking that makes a week's overbooking grow is refused, while a change that keeps or
  shrinks one already there is saved with the warning.
- **Load unit for a new allocation** — **Percent of position** (the default) or **Hours
  per week**: the unit the sheet starts with. Every allocation can be typed in either.
- **Unbooked under (%)** — 1 to 100, by default 50: a person whose confirmed load over
  the horizon is under this is on the Unbooked list.
- **Unbooked horizon (weeks)** — 1 to 52, by default 6: how many weeks ahead the
  Unbooked list and the dashboard's unbooked, overbooked and certificate items look.
- **Ending without follow-on (weeks)** — 1 to 26, by default 4: a confirmed booking
  ending this soon, with nothing confirmed after it, is a dashboard item.
- **Open request lead (days)** — 0 to 365, by default 14: an open request starting this
  soon, or already started, is a dashboard item for the planners.

## The dashboard

**Home**, the dashboard, has a **Planning** card for everyone with `planning:access`;
press it to open Planning. With `planning:view` it carries the
figures: the number of people with a confirmed booking this week, their confirmed
load this week with the tentative load beside it, how many requests are open, and how
many people are unbooked.

Planning's items join the other apps' in **Needs attention**. The first three are for
`planning:view`, the last two for `planning:manage`:

| Item | It means | Opens |
| --- | --- | --- |
| Unbooked | A person is under the unbooked threshold within the horizon — one item per person, dated the day they roll off, or today. | Unbooked |
| Overbooked | A person's confirmed bookings exceed their capacity in a week from this week to the end of the horizon, named with the first such week. | The person's timeline |
| Certificate expiring during a booking | A certificate of the person runs out inside one of their confirmed bookings within the horizon — one item per booking and certificate, dated the day it runs out. | The person's timeline |
| Ending without follow-on | A confirmed booking ends within the weeks the settings name, and the person has no confirmed booking that carries on within seven days of its end. Named with the person and the project's code. | Plan |
| Open request | An open request starts within the days the settings name, or has already started — one item per request, named with the project's code and the request's title, dated its first day. | Requests, filtered to the open ones |

An item goes away when what it counts does: a booking made, an overbooking mended, a
certificate renewed in Staff, a request filled or withdrawn.

## On the Staff person page

When the Planning module is on, a person's page in Staff has a **Planning** tab after
**Account**, for those who hold `planning:access` and either `planning:view` or are
the person themselves. It shows the person's [timeline](#a-persons-timeline): with
`planning:view` all of it, and on your own page with `planning:access` alone, your
confirmed bookings. A planner changes an allocation from it as from the timeline.

## On the project page

When the Planning module is on, a project's page in Projects has a **Planning** tab
after **Invoicing**, for those who hold `planning:access` and `planning:view`. It shows
the project's [plan](#a-projects-plan) — the hours per week with the demand, the
allocations and the requests — under the project page's own header, and a planner
works from it as from the plan: **New allocation**, **New request**, **Candidates**,
**Withdraw**. Holding `planning:manage` alone does not show the tab.

## Who sees what

| What | `planning:access` | `planning:view` | `planning:manage` |
| --- | --- | --- | --- |
| The app, **My planning**, your own confirmed bookings and the mail about them | ✓ | ✓ | ✓ |
| **Plan**, every project's plan and the project page's **Planning** tab, every person's timeline with tentative bookings, **Requests**, **Intake**, **Unbooked**, the dashboard figures and items | – | ✓ | ✓ |
| The allocation sheet, the request sheet, **Candidates** and **Fill**, **Withdraw**, the planners' notes, **Planning settings**, the *Ending without follow-on* and *Open request* items | – | – | ✓ |

**See the plan** (`planning:view`) and **Plan** (`planning:manage`) are sensitive: the
plan shows every project's code, name, customer, status and dates — bids included,
which Projects otherwise shows only to those who may see every project. Holding
`planning:manage` without `planning:view` gives the sheets but not the board, Requests
or Intake, so a planner's role carries both. The full table is under
[Permissions](/en/reference/planning/#permissions).

A person Staff no longer shows you — erased, or not someone you may see — appears as
*Erased person* or *A colleague*; their bookings stay in the plan.
