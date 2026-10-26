---
title: Planning
description: Who works on what — the Plan board of people against weeks with its colours, groupings and filters, the allocation sheet with its warnings and refusals, a project's plan, a person's timeline with its flags, the Unbooked list, My planning and the mail a person gets, the Planning settings, the dashboard card and its attention items, and the Planning tab on a person's page in Staff.
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

This is Planning's first delivery: allocations, the board, the lists and the person's
own view. Requests for people a project needs, assignments with their contract terms
and the person's wishes come in later deliveries.

## The app and its sidebar

The **Planning** tile is shown to everyone with `planning:access`. The sidebar shows the
areas your permissions give you:

| Area in the sidebar | Who sees it |
| --- | --- |
| **Plan** (the board) | `planning:view` |
| **Unbooked** | `planning:view` |
| **My planning** | everyone with `planning:access` |
| **Planning settings** | `planning:manage` |

The app opens on **Plan** for those who may see it and on **My planning** for everyone
else. A project's plan and a person's timeline have no sidebar entry: you open them
from the board, the Unbooked list, the dashboard or a person's page in Staff.

The plan is built on the people Staff answers you, so a planner also needs **View the
staff directory** (`staff:view`) from Staff. Without it the board does not pretend the
plan is empty: it says **Staff shows you nobody** and asks you to have an administrator
give you the permission.

The search in the top bar knows the screens above, and offers **New allocation** to a
planner holding `planning:view` and `planning:manage`: it opens the board with the
allocation sheet.

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
  people booked on one project.
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

## A project's plan

A project's plan (`planning:view`) opens from a project's name on the board grouped by
**Projects**. Its header names the project with its status, its customer and its dates.

- **Hours per week** — the project's **Booked** (confirmed) and **Tentative** hours each
  week for twelve weeks from this week, with its allocations as bars under them, named
  by the person.
- **Allocations** — each booking on the project in those weeks: the person (with the
  planners' note under the name, for a planner), the role, the period, the load and the
  status. A planner's actions menu on each row offers **Edit**, **Confirm** (on a
  tentative one) and **Delete**.

A planner books someone on the project with **New allocation** in the header.

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
- **Open request lead (days)** — 0 to 365, by default 14: for the requests that come in
  a later delivery. It does nothing yet.

## The dashboard

**Home**, the dashboard, has a **Planning** card for everyone with `planning:access`;
press it to open Planning. With `planning:view` it carries the
figures: the number of people with a confirmed booking this week, their confirmed
load this week with the tentative load beside it, and how many people are unbooked.

Planning's items join the other apps' in **Needs attention**. The first three are for
`planning:view`, the last for `planning:manage`:

| Item | It means | Opens |
| --- | --- | --- |
| Unbooked | A person is under the unbooked threshold within the horizon — one item per person, dated the day they roll off, or today. | Unbooked |
| Overbooked | A person's confirmed bookings exceed their capacity in a week from this week to the end of the horizon, named with the first such week. | The person's timeline |
| Certificate expiring during a booking | A certificate of the person runs out inside one of their confirmed bookings within the horizon — one item per booking and certificate, dated the day it runs out. | The person's timeline |
| Ending without follow-on | A confirmed booking ends within the weeks the settings name, and the person has no confirmed booking that carries on within seven days of its end. Named with the person and the project's code. | Plan |

An item goes away when what it counts does: a booking made, an overbooking mended, a
certificate renewed in Staff.

## On the Staff person page

When the Planning module is on, a person's page in Staff has a **Planning** tab after
**Account**, for those who hold `planning:access` and either `planning:view` or are
the person themselves. It shows the person's [timeline](#a-persons-timeline): with
`planning:view` all of it, and on your own page with `planning:access` alone, your
confirmed bookings. A planner changes an allocation from it as from the timeline.

## Who sees what

| What | `planning:access` | `planning:view` | `planning:manage` |
| --- | --- | --- | --- |
| The app, **My planning**, your own confirmed bookings and the mail about them | ✓ | ✓ | ✓ |
| **Plan**, every project's plan, every person's timeline with tentative bookings, **Unbooked**, the dashboard figures and items | – | ✓ | ✓ |
| The allocation sheet, the planners' notes, **Planning settings**, the *Ending without follow-on* item | – | – | ✓ |

**See the plan** (`planning:view`) and **Plan** (`planning:manage`) are sensitive: the
plan shows every project's code, name, customer, status and dates — bids included,
which Projects otherwise shows only to those who may see every project. Holding
`planning:manage` without `planning:view` gives the sheet but not the board, so a
planner's role carries both. The full table is under
[Permissions](/en/reference/planning/#permissions).

A person Staff no longer shows you — erased, or not someone you may see — appears as
*Erased person* or *A colleague*; their bookings stay in the plan.
