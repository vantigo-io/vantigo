---
title: Projects
description: Projects per customer, their people, tasks, milestones and economy.
sidebar:
  order: 20
sources:
  - apps/projects/frontend
---

The **Projects** app is where work is organised: a project is a coded piece of work
for one customer (or an internal one), the people who may act on it, its tasks, and
the billing rules the Time, Expenses and Invoices apps read later. Its three areas —
**Projects**, **My tasks** and **Project economy** — all need the `projects:access`
permission. Inside a project, what you may do is decided by your project role
(**Manager**, **Member** or **Viewer**) and a few global permissions, listed in
[Roles and permissions](/en/reference/projects/#roles-and-permissions).

## Find a project

Open **Projects** in the sidebar. The page opens with three counters — **Active**,
**Planned** and **On hold** — and a table with **Code**, **Name**, **Customer**,
**Status**, **Managers** and **Dates**. An internal project shows the badge
**Internal** instead of a customer. Click a code to open the project.

The toolbar narrows the list, and every choice lands in the address bar, so a filtered
list survives a refresh and can be sent as a link: the search box (**Search by code or
name…**), **Status** (**All statuses** when empty), **Project type** (**Customer
projects** or **Internal projects**), **Customer**, searched as you type, and the
switch **My projects**, which keeps only the projects you hold a role in.

You only see the projects you hold a role in, unless `projects:view-all` or
`projects:manage-all` shows you every project; a pasted link to one you may not see
answers **Project not found** rather than a refusal.

## Create a project

Click **New project** on the Projects page (it needs `projects:create`; the Spotlight
quick action **Create project** opens the same form). Fill in:

- **Customer** — search and pick one, or choose **Internal project**, the first
  option. An empty picker is refused with *Choose a customer, or select Internal
  project*: an internal project is always an explicit choice.
- **Project name**, required.
- **Project code** — 2–20 letters and digits, upper-cased as you type, unique across
  the installation. While you type the name the form suggests a code from the
  customer's and project's initials and a running number; click **Use suggestion** to
  take it, or type your own (see
  [The suggestion](/en/reference/projects/#the-suggestion)). A taken code is refused
  on the field.
- **Description**, **Start date** and **End date**; the end cannot be before the start.
- **Billing type**: **Time and materials**, **Fixed price** or **Non-billable**.
  **Internal project** locks it to **Non-billable** — nobody can be invoiced.
- **Budget hours**, the planning budget everyone on the project may see.
- The amounts only managers and holders of `projects:view-financials` or
  `projects:manage-all` can read: **Fixed price amount** (shown and required only for
  a fixed-price project), **Budget amount**, **Default bill rate** and **Currency**
  (three letters, NOK by default, required as soon as any amount is set).

Click **Create**. You become the project's **Manager**, it appears with the status
**Planned**, and the timeline records *Project created as CODE*
(see [Domain model](/en/reference/projects/#domain-model)).

## Read the project page

A project opens on its header — code and name, a status badge, and a link to the
customer or the **Internal** badge — above a row of tabs. The header stays on every
tab; a manager also finds **Edit** and **Change status** there.

| Tab | What it shows | Who sees it |
| --- | --- | --- |
| **Overview** | **Project details** (description, dates, billing type, budget hours, managers) and the **Timeline** of every change, never with an amount | everyone who sees the project |
| **Tasks** | the project's tasks as a list or a board | everyone who sees the project |
| **People** | who is on the project and in which role | everyone who sees the project |
| **Billing** | the financial summary, billing lines and work types | managers, `projects:view-financials`, `projects:manage-all` |
| **Economy** | budget against logged work, costs, and the invoice plan | everyone; amounts only with financial rights |
| **Time** | the hours logged on the project, from the Time app | `time:access`, when Time is enabled |
| **Expenses** | the expenses recorded on the project, from the Expenses app | `expenses:access`, when Expenses is enabled |

## Edit a project

Click **Edit** in the header (managers and `projects:manage-all`). The form is the one
you created the project with; **Save changes** replaces every field with what it
holds. Changing the **Project code** asks *Change the project code?* first, because
timesheets and billing lines quote it and every trackable code changes with it; the
old code stays on the timeline.

The **Currency** cannot be cleared while any amount, fixed-amount line, line budget or
milestone is set, and cannot be changed while a fixed-amount line, a line budget
amount or an open milestone exists; the save is refused on the field until those are
repriced, removed or cancelled (see [Domain model](/en/reference/projects/#domain-model)).
If somebody saved the project while your form was open, the save is refused with
*The project was changed by someone else. Reload it and try again.*

## Change the status, close or cancel a project

Click **Change status** in the header (managers and `projects:manage-all`) and pick
**Planned**, **Active**, **On hold**, **Completed** or **Cancelled**; the current one
is greyed out. Every move is allowed, reopening included, and each is written to the
timeline. Only an **Active** project is open for work: the Time app refuses hours on
any other status, and budget alerts are raised for active projects only. Tasks can
still be edited whatever the status. Projects are never deleted or archived: closing
one means **Completed**, abandoning one means **Cancelled**, and both keep the
project and everything logged against it readable, because other apps refer to them.

## Manage the people on a project

Open the **People** tab. **People on this project** lists each person with their
**Role**; a disabled account carries the badge **Inactive**. A manager (or
`projects:manage-all`) also gets **Add person** — pick a **Person** (search by name or
email among active users not yet on the project) and a **Role** — the role picker on
each row, which changes the role at once, and the remove icon, which asks *Remove
name?* and warns that they lose access while work already recorded stays.

The roles widen in order: a **Viewer** sees the project, its people, tasks and
timeline; a **Member** also writes tasks, checklist items and comments, and may log
time; a **Manager** additionally sees the amounts, edits the project, changes its
status and manages people, lines, work types and milestones. One role per person per
project.

## Work with tasks

Open the **Tasks** tab. The **View** switch shows the tasks as a **List** — the groups
**To do**, **In progress** and **Done**, each a table with **Title**, **Assignee**,
**Due date**, **Estimate**, **Checklist** and **Comments**, subtasks folded under
their parent — or as a **Board** of three columns where subtasks are cards of their
own. The choice is remembered in your browser. Members and managers see **Add task**;
a viewer gets the same views without the actions.

**Add task** opens **New task**: **Title** (required, at most 200 characters),
**Description**, **Status**, **Assignee** (anyone on the project), **Start date**,
**Due date** (not before the start), **Estimate (hours)** (above 0 when set) and
**Parent task**, which makes it a subtask of a top-level task (one level deep).

Click a task to open the **Task** drawer: the pencil edits the title in place and
the bin deletes the task after *Delete title?* — subtasks, checklist and comments go
with it, and it cannot be undone. The details form (**Status**, **Assignee**, **Start
date**, **Due date**, **Estimate (hours)**, **Description**) is saved with **Save
changes**. **Subtasks** has a checkbox per subtask that marks it **Done** or back to
**To do**, and **Add subtask**. **Checklist** is the small steps inside the task
(**Add item**, tick, delete). **Comments** is a thread with **Post comment** and
**Load more**; you edit and delete your own, a manager deletes anyone's. On the board
a card's menu offers **Move to To do**, **Move to In progress** and **Move to Done**;
**Done** stamps the completion, moving back clears it. A task somebody else saved
meanwhile is refused with *The task was changed by someone else. Reload it and try
again.* Tasks follow the project's roles, not a permission, and are never locked by
the project's status — see [Tasks](/en/reference/projects/#tasks).

## See your own tasks

**My tasks** in the sidebar lists every open task assigned to you across the projects
you can see, by due date and then project: **Project**, **Title**, **Status**, **Due
date** and **Estimate**. The code opens the project, the title opens the task in its
drawer. The **Status** picker on a row changes the status directly — and since
**Done** is not open, that is how a task leaves the list. With nothing assigned the
page says **Nothing assigned to you**. The Spotlight quick action **Create task**
lands here: it asks for the **Project** first, then opens the ordinary **New task**
form on it.

## Set up billing lines

Open the **Billing** tab. It needs financial rights on the project — manager,
`projects:view-financials` or `projects:manage-all`; anyone else reads *You cannot
see this project's amounts*. It opens with the **Financial summary** (**Billing
type**, **Fixed price amount**, **Budget amount**, **Default bill rate**,
**Currency**) and the **Billing lines** card, with **Trackable code**, **Product**,
**Unit**, **Pricing rule**, **List price**, **Budget** and **Status**.

A manager clicks **Add billing line** and fills in:

- **Line code** — 1–10 letters and digits, unique within the project; the trackable
  code timesheets quote is `PROJECTCODE-LINECODE`.
- **Product variant** — a service product from the Products app, searched as you
  type. Without a products view permission the form says *You need access to products
  to pick a variant*.
- **Pricing rule**: **List price**, **Fixed amount** (in the project's currency) or
  **Discount** (**Discount (%)**, above 0 and at most 100). A fixed amount needs the
  project to have a currency.
- **Budget hours** and **Budget amount** for the line, optional and above 0; the
  amount also needs a currency.

Lines are never deleted: the pencil on a row opens the line with an **Active** switch;
a line switched off shows **Inactive** and can be switched back on. Without the
Products app the card reads *Billing lines need the products module, which is not
enabled*; when the catalog cannot be read, the lines are listed under a warning and
are unchanged. See
[Billing lines and the optional Products dependency](/en/reference/projects/#billing-lines-and-the-optional-products-dependency).

## Define work types

Below the lines on the **Billing** tab, **Work types** lists overtime and other kinds
of hours, each multiplying the rate a time entry would otherwise bill and cost at:
**Name**, **Bill multiplier**, **Cost multiplier** and **Status**. The card stands
whether or not Products is enabled.

A manager clicks **Add work type**: a **Name** (unique in the project, case ignored,
at most 100 characters), a **Bill multiplier** and a **Cost multiplier** in percent —
150 % is one and a half times the rate, 100 % the rate as it stands; each above 0, at
most 1000, at most two decimals. A taken name is refused with *This project already
has a work type with that name*. Work types are never deleted: the edit form's
**Active** switch deactivates one, so no new time entry can pick it, and reactivates
it later. Changing a multiplier moves nothing already submitted — see
[Work types](/en/reference/projects/#work-types).

## Plan the invoicing with milestones

On the **Economy** tab, the **Invoice plan** is what the project is invoiced in, in
the order the milestones are billed. It is financial data throughout: without
financial rights you read *The invoice plan is for people who may see the project's
financials*. Above the card stand **Planned**, **Ready to invoice** and **Invoiced**;
below it, on a fixed-price project, a note says how much *of the fixed price is not
planned yet* or that *the plan is … more than the fixed price*. Neither stops a save.

Milestones are billed in the project's currency; until the project has one, the card
says so and offers a manager **Edit the project**. A manager (or
`projects:manage-all`) clicks **Add milestone**: **Name**, **Description**, **Planned
date**, and **Priced as** either **An amount** or **A share of the fixed price** —
offered only on a fixed-price project, with roughly what it comes to. A share follows
a later change of the fixed price until the milestone is invoiced.

Each row shows **Milestone**, **Planned date** (with **Overdue** when the date has
passed and the milestone is still planned or ready), **Amount** and **Status**, and a
menu of the moves it allows:

| Action | Who | What happens |
| --- | --- | --- |
| **Edit**, **Move up**, **Move down** | manager | while the milestone is **Planned** or **Ready to invoice** |
| **Mark as ready to invoice** / **Move back to planned** | manager | stamps who and when |
| **Mark as invoiced** | financial rights | asks for an optional **Invoice reference** and **Invoice date**, and freezes the amount |
| **Undo the invoicing** | financial rights | back to ready; reference, date and frozen amount are cleared |
| **Cancel the milestone** | manager | it stays in the plan, struck through and last, and bills nothing |
| **Reopen the milestone** | manager | a cancelled milestone goes back to planned |
| **Delete the milestone** | manager | only while still planned and never moved; otherwise cancel it |

A move is refused when the project no longer supports the milestone — no currency, no
fixed price behind a share, or an amount in a currency the project has left; the
message names the reason (full table:
[Billing milestones and the invoice plan](/en/reference/projects/#billing-milestones-and-the-invoice-plan)).
With Expenses enabled, a line under the table says how many *billable expenses ready
to invoice* there are, with **View the expenses** when you may open the Expenses tab.

## Follow the budget and logged work

The top of the **Economy** tab is **Budget and logged work**. Everyone who sees the
project sees it; the amounts appear only with financial rights and a project
currency, the **Margin** only with `projects:view-costs` on top.

- **Budget used** is a percentage of one basis, in the server's order: **Budget
  amount**, else the **Fixed price** on a fixed-price project, else **Budget hours**
  — the one basis a member sees. With none it says **No budget set**. The red **Over
  budget** badge follows the exact ratio, not the rounded percentage.
- A bar splits the logged work into **Approved**, **Submitted** and **Draft**, the
  three buckets Time's entry statuses fold into; rejected entries are in Draft.
- **Value of work** is what the logged hours bill; the **Margin** is the value of the
  work plus what the expenses bill, less what both cost. Notes say what the figures
  leave out: hours with no rate, hours with no cost, and the tasks' total estimate.
- A table per billing line — **Line**, **Budget**, **Logged**, **Used**,
  **Remaining** — includes inactive lines and a row **No billing line** when something
  was logged without one. **Hours by work type** follows when any entry picked a type.

The hours come from the Time app; without it the section reads *Hours appear here
when Time tracking is enabled*. The expenses come from the Expenses app into a
**Costs** section of its own: **Approved**, **Submitted — awaiting approval** and
**Draft** rows with **Lines**, **Cost** and **Passed on to the customer**, a
**Total**, **Of which supplier invoices** when there are any, and notes for unpriced
expenses and other currencies. Expenses are never measured against the budget, and
the section says so. Definitions are in
[Project economy](/en/reference/projects/#project-economy).

## Compare projects in Project economy

**Project economy** in the sidebar lists every project whose money you may see — the
ones you manage, or all of them with `projects:view-financials` or
`projects:manage-all` — as **Project**, **Customer**, **Status**, **Budget used**,
**Value of work**, **Pending hours**, **Next milestone** and **Ready to invoice**.
Three cards count the **Projects**, those **Over budget**, and what is **Ready to
invoice** per currency, split into milestones and expense lines.

The toolbar has the search box, **Status** (**Active** by default; **All statuses**
lifts it), **Customer**, **Sort by** (**Most of the budget used**, **Most ready to
invoice**, **Next milestone first**, **Project code**), and the switches **Only over
budget** and **Only with something ready to invoice**. A row with expenses ready in
another currency says **More ready in another currency**; the project's own Economy
tab reports them. With nothing to show the page reads *Projects whose financials you
can see appear here* (see [The portfolio](/en/reference/projects/#the-portfolio)).

## Permissions at a glance

| Screen or action | Needs |
| --- | --- |
| The Projects app: Projects, My tasks, Project economy | `projects:access` |
| **New project** | `projects:create` |
| Seeing every project, not only your own | `projects:view-all` or `projects:manage-all` |
| **Edit**, **Change status**, People, billing lines, work types, milestones | the **Manager** role, or `projects:manage-all` |
| Tasks, checklist items and comments | the **Member** or **Manager** role |
| **Billing** tab, the invoice plan and the amounts on **Economy** | the **Manager** role, `projects:view-financials` or `projects:manage-all` |
| **Mark as invoiced** and **Undo the invoicing** | financial rights on the project |
| **Margin** and the cost figures | financial rights and `projects:view-costs` |
| **Time** tab | `time:access`, Time enabled |
| **Expenses** tab | `expenses:access`, Expenses enabled |
