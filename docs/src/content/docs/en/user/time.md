---
title: Time
description: Registering hours on projects, submitting your week, approving and locking periods.
sidebar:
  order: 30
sources:
  - apps/time/frontend
---

The Time app is where you log your hours on projects, send a week off for approval, and
— if that is your job — approve other people's time, follow everyone's weeks and close
periods. Its sidebar has up to four areas: **My week** for everyone with `time:access`,
**Approvals** for `time:approve`, **People** for `time:view-all` and **Time settings**
for `time:manage`. The rules behind every screen — the rate chain, the approval states,
the period lock — are in the [Time reference](/en/reference/time/); this page tells you
where to click.

An entry is always one person's hours on one project on one day, optionally on one of
the project's billing lines and one of your tasks. It moves through five statuses:
**Draft**, **Submitted**, **Approved**, **Rejected** and **Invoiced**. You can only
change a draft or a rejected entry; see
[the state machine](/en/reference/time/#the-state-machine).

## Log hours on your week

Open **Time → My week**. The page shows one week, Monday to Sunday, with a row for
each project, line and task you log time on and a column for each day. Use the arrows
to move to the **Previous week** or **Next week**, and **This week** to come back;
the week also lives in the address bar, so a link to any date opens that date's week.

A week with nothing logged says "Nothing logged this week". Add a row first:

- **Add row** opens *Add a row*: pick a **Project** (an active project you hold a role
  on — "You hold no role on an active project." means there is nothing to pick),
  optionally one of its active lines under **Line**, and optionally one of your open
  tasks on it under **Task**. Then **Add**.
- **From my tasks** adds a row for every open task of yours on an active project you
  log time on, in one go. If there is none, the page says "No tasks to add".

A row you add lives only on the page until one of its days has hours. There are no
week templates or copying of entries; **From my tasks** is the shortcut.

### Type hours into a day

Click a row's cell under a day and type the duration as `7.5`, `7,5` or `7:30` — more
than 0 and at most 24 — then press Enter or leave the cell. The entry is saved as a
draft at once; Escape puts the saved value back, and clearing the cell deletes the
entry. A value that is not a duration is refused with "Not a duration" and the cell
goes back to what was saved. The row and day totals, and the **Week total**, follow.

A cell is coloured by its entry's status once the entry is past draft, and hovering
says why a cell cannot be typed into:

- "This day is locked." — the day is before the [period lock](#close-a-period).
- "N entries on this day — change them in the day view." — the grid shows the sum of
  several entries; click the cell to open the day.
- "08:00–16:00 — an entry with a start and an end time is changed in the day view." —
  the same for an entry logged by the clock.
- "Submitted" or "Approved" — the entry is out of your hands until an approver acts.
- "Rejected: …" — the reason the approver gave; see [fixing rejected time](#fix-rejected-time).

The server refuses hours that would take one day over 24, and anything dated before the
period lock; the refusal is shown as "Could not save the hours" with the reason.

### Log time with a start and end, a note, or a work type

Click a day's heading in the grid (or open **Time → My week** and follow the column
heading) to open the **day view**: everything you logged on that day as a list, with
start and end times and notes, a **Day total**, and arrows for the **Previous day**,
**Next day** and **Today**. **Week** takes you back to the grid.

**Add entry** opens *Log time*:

| Field | What to fill in |
| --- | --- |
| **Project** | Required. The active projects you hold a role on. |
| **Line** | One of the project's active billing lines, or "No line". |
| **Task** | One of your open tasks on that project, or "No task". |
| **Work type** | Shown only when the project has an active work type; "Ordinary hours" is the empty choice. Changing the project clears it. |
| **Date** | Required; prefilled with the day you opened. |
| **Start** and **End** | Both or neither. The end must be after the start, on the same day. |
| **Hours** | Required unless you gave a start and an end — then it is "Worked out from the start and end time." and cannot be typed. |
| **Note** | Free text, at most 2000 characters. |
| **Billable** | Shown only on a project that bills; on by default for a new entry. |

**Save** stores the entry and shows "Time saved". A refusal the server ties to a field
— a work type that is no longer active, a day over 24 hours — lands on that field;
anything else shows as "Could not save the time".

**What billable means.** A billable entry is priced by the
[rate chain](/en/reference/time/#the-rate-chain) — the line's rule, the project's
default rate, the customer's or your own rate card — and the rate is frozen when you
submit. A non-billable entry gets no bill rate, only its cost. On a project that does
not bill, every entry is non-billable whatever you choose. A **work type** keeps its
own multiplier beside that rate, shown as a badge after the row's code and, where you
may see the money, as a line like "900 × 150 % = 1,350.00"; see
[the work type's multiplier](/en/reference/time/#the-work-types-multiplier). Work
types are defined on the project, in Projects, not here.

### Edit or delete an entry

In the day view each entry is a card with its hours, status badge, work type, times,
note and — when it was rejected — the reason in red. While the entry is a draft or
rejected and its day is not locked, the pencil (**Edit the entry**) opens *Edit time*
with the same fields as above, and the bin (**Delete the entry**) asks "Delete the
entry?" before removing it: "This cannot be undone." In the grid, typing over a cell
edits the entry and clearing it deletes it, but only for a day with one untimed entry.

## Submit your week

When the week is complete, press **Submit week** at the top of **My week**. The
dialog *Submit the week?* says what happens: "Every draft from Monday to Sunday goes to
approval. A submitted entry can only be changed again if it is rejected." Confirm with
**Submit**.

Every draft in the week becomes **Submitted**, its rates are frozen, and the week shows
a "Submitted …" badge with the date and time. Rejected entries are left alone — they
need an edit first — and an empty week can be submitted too, to say you worked nothing.
A draft dated before the period lock refuses the whole week with "Could not submit the
week", and nothing is submitted.

Log more hours in a submitted week and the page warns "Changed since you submitted it":
those drafts were not part of the submission, so press **Submit week** again to send
them. See [weekly submission](/en/reference/time/#weekly-submission).

**You cannot withdraw a submission yourself.** Once submitted, an entry is out of your
hands until an approver acts: ask them to reject it (which gives it back to you with a
reason) or, once it is approved, to withdraw the approval. Both are described under
[the state machine](/en/reference/time/#the-state-machine).

## Fix rejected time

A rejected entry shows red in the grid, with "Rejected: …" and the approver's reason on
hover and in the day view. Edit it — in the grid or with **Edit the entry** — and it
becomes a draft again; the week then shows "Changed since you submitted it" and you
**Submit week** once more. Deleting it is the other way out.

## Approve other people's time

Open **Time → Approvals**. The sidebar offers it to anyone with `time:approve`, who
approves every project's time. If you manage a project you approve its time through
that role alone: you reach the same page from the dashboard's attention list or at
`/time/approvals`. Someone who approves nothing sees "You approve nobody's time".

The queue is one card per person and week, oldest week first, with the person's name,
the week's Monday and the hours. "Nothing waiting for approval" means the queue is
empty. Entries dated before the period lock are left out, so the page never offers an
action the server would refuse.

- **Show the entries** opens the week: a row per entry with its **Date**, project,
  line and task, work type badge, **Hours**, **Bill amount** (the rate times the hours,
  if you may see the project's money; "—" when there is no rate; "Not billable" for a
  non-billable entry) and **Note**.
- **Approve** or **Reject** on a card acts on the whole week; the same buttons on a row
  act on one entry.
- Tick the checkbox on a card or a row to build a selection across weeks, then
  **Approve N selected** or **Reject N selected**; **Clear the selection** empties it.

**Approving** marks the entries **Approved** and shows "Time approved" with the count.
**Rejecting** opens *Reject the time?* and asks for a **Reason** — required, at most
1000 characters: "The person sees this with every entry you reject." Confirm with
**Reject**.

Every batch is all-or-nothing: if one entry in it can no longer be approved — the lock
moved, someone else acted first — nothing changes, and "Could not approve the time"
lists each entry refused and why.

**What the person sees afterwards.** An approved entry turns green and read-only in
their grid. A rejected one turns red, carries your reason wherever it is shown, and
becomes editable again; once they edit it, it is a draft and they submit the week
again, after which it is back in your queue.

**Withdrawing an approval.** Approved entries are no longer in the queue. In the day
view, an approved entry you are allowed to unapprove shows **Withdraw the approval**;
it goes back to a fresh draft and its week reports unsubmitted changes. The period lock
holds this back for everyone but `time:manage`, and an invoiced entry cannot be
touched at all.

**Invoiced hours.** An approved entry becomes **Invoiced** when an invoice that bills
it is issued in Invoices, and from then on nobody can edit, approve or withdraw it
here. Only a credit note that takes back the invoice line it was billed on returns it
to **Approved**, with its approval as it was; withdrawing the approval is not a way
round that. In the day view such an entry's status
reads *Invoiced by invoice 1042* — a link to the invoice when the Invoices module is on
and you hold `invoices:access`, plain words otherwise — and on **My week** the cell's
tooltip says the same ([invoicing work](/en/user/invoices/#invoicing-work)).

## See everyone's weeks

Open **Time → People** (`time:view-all`). The table has a row per person who logged an
hour or submitted a week in the window, and a column per week, newest last. Each cell
shows the hours and how far the week has come: a **Submitted** badge, "N h approved"
and "N rejected". Choose the window under **Weeks**, from "1 week" to "12 weeks"; four
is the default, and the choice stays in the address bar so a link shows the same
weeks.

"Nobody logged time in these weeks" means the window is empty. Without the permission
the page says "You cannot see everyone's time".

## Close a period

Open **Time → Time settings** (`time:manage`). The **Period lock** card holds one
date, **Locked before**: every day strictly before it is closed, the date itself is
open, and "Leave it empty for no lock at all."

1. Type or pick the date, or press **Clear the lock** in the field to remove it.
2. **Save the lock** is enabled once the date differs from the saved one. It asks
   *Change the lock?* and spells out the effect: "Every day before … closes: nobody but
   a time manager can log, change, submit, approve or reject time dated before it." —
   or "The lock goes away and every day is open again."
3. Confirm with **Save**; the page shows "Lock saved".

From then on **My week** says "Days before … are locked and can no longer be changed."
on a week that starts before the lock, the day view hides **Add entry** on a locked day,
and the approval queue leaves those entries out. Only `time:manage` can still change
time dated before the lock. See [the period lock](/en/reference/time/#the-period-lock).

## Set a person's rates

The **Rate cards** card on the same page lists every person who has a card, with
their cards newest first: **Valid from**, **Bill rate**, **Cost rate** and
**Currency**. "No rate cards yet" means nobody is priced.

- **Add rate** opens *Add a rate card*. Search the **Person** by name — "Search anyone
  who works here, whether or not they have logged an hour yet." — and pick the first
  day the card is in effect under **Valid from**. Give a **Bill rate**, a **Cost
  rate** or both, each more than zero, and a three-letter **Currency** (NOK is
  prefilled). **Save**.
- **Add a rate for …** on a person's group opens the same form with the person
  filled in.
- The pencil (**Edit the rate**) changes a card's date, rates and currency; the person
  cannot be changed.
- The bin (**Delete the rate**) asks "Delete the rate?": "Time already submitted keeps
  the rate it was saved with; a draft is priced again on its next save."

A card prices the person's hours from its date until the next card takes over, and only
while an entry is a draft or rejected; once submitted, the rate is frozen. A card in a
currency the project does not bill in gives no bill rate. See
[the rate chain](/en/reference/time/#the-rate-chain). One person can have only one
card per date; a second is refused on **Valid from**.

## Time elsewhere in Vantigo

A project's page has a **Time** tab with the hours logged on it by status, line and
person — with the billed amount for those who may see the project's money — and a
**Log time** link to My week. The dashboard's Time card shows your hours this week and
what waits for your approval; Spotlight has a **Log time** quick action.

## Who can do what

| Permission | What it opens |
| --- | --- |
| `time:access` | The app and **My week**: your own hours on the projects you are a member or manager of. Everything else needs it too. |
| `time:approve` | **Approvals** in the sidebar, for every project. A project manager approves their own projects without it. |
| `time:view-all` | **People**, everyone's entries, and cost rates. |
| `time:manage` | **Time settings**: rate cards, the period lock, withdrawing approvals, and changing time past the lock. |

The full table, and who may see bill and cost rates, is under
[visibility and permissions](/en/reference/time/#visibility-and-permissions).
