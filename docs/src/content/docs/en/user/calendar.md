---
title: Calendar
description: Your appointments and what the other apps have put in your days, colleagues' busy times, repeating appointments and the dashboard's agenda.
sidebar:
  order: 35
sources:
  - apps/calendar/frontend
---

The Calendar app is one view of your days: the appointments you make yourself, with
colleagues on them, beside what the other apps already know about you — your follow-ups
in Customers, your absence in Staff, your tasks in Projects, your hours in Time and your
trips in Expenses. Nothing is copied: a follow-up you tick in Customers is ticked on the
calendar at once, because the calendar asks Customers each time it is opened. The rules
behind every step are in the [Calendar module reference](/en/reference/calendar/); this
page is about what you do on each screen.

## Who sees what

| What | Who sees it |
| --- | --- |
| **Calendar** in the sidebar, your own calendar, your appointments and your answers to invitations, and the dashboard's **Today and this week** | everyone with `calendar:access` |
| **A colleague's calendar** | `calendar:access` and `calendar:view-colleagues` |

What the other apps put on your calendar follows their own permissions: you see your
follow-ups only if you may read Customers' timeline, your absence only if your user is
linked to your record in Staff, and so on. An app you have no access to, or that your
installation has not switched on, simply adds nothing. The full table is under
[Permissions](/en/reference/calendar/#permissions).

## The calendar page

Open **Calendar** from the app switcher. The page shows one of four views, chosen under
**View** in the toolbar:

- **Month** — the month as a grid of weeks.
- **Week** — seven days with a time axis, and a row along the top for what lasts all day.
- **Day** — one day the same way.
- **List** — a week as an agenda, one line per item. On a phone, where the grids do not
  fit, the calendar opens in **List**.

**Today** brings you back to today, and the arrows (**Previous** and **Next**) move a
month, a week or a day at a time, following the view. Every date and time is shown in
your own time zone, the one under **Time zone** in your profile
([Choose your time zone](/en/user/getting-started/#choose-your-time-zone)), so a meeting
booked at 09:00 in Oslo shows at 08:00 for a colleague who has chosen London. The address
of the page keeps the view and the date, so a link or a refresh opens the same place.

Above the calendar, a row of chips — the legend — lists each kind of item your
installation can show, each in its own colour. Click a chip to hide that kind or show it
again; your browser remembers the choice. What each kind is, and where clicking an item
of it takes you:

| Chip | What it is | Clicking it |
| --- | --- | --- |
| **Appointments** | An appointment you made or are invited to | opens the appointment |
| **Follow-ups** | A customer follow-up assigned to you, on its due day; struck through once done | opens the customer |
| **Customer activity** | A call, meeting or email you logged on a customer's timeline | opens the customer |
| **Tasks** | A task assigned to you, on its due date; struck through once done | opens the task on its project |
| **Hours** | Hours you logged, titled with the project, at their start and end time when you gave them, otherwise all day; struck through once approved | opens the day in Time |
| **Travel** | One of your travel claims, from departure to return; struck through once approved | opens the travel claim |
| **Absence** | Your own absence, requested, approved or registered; one you have requested shows **Requested** under its name until it is decided. The name and that word are in the language set under **Language** on your Staff record | opens **My page** on its **Absence** tab |
| **Public holidays** | Norway's public holidays, shaded on their days | — |

Appointments have a look of their own: one that does not block your time (**Free**) is
drawn outlined, one you declined is struck through, and a repeating one carries a repeat
mark (↻).

## Make an appointment

1. Press **New appointment** at the top of the page, or drag across the time you want on
   **Week** or **Day** (or across days on **Month**) to open the sheet with that span
   already filled.
2. Give it a **Title**.
3. Choose all day or a time. With **All day** on, pick the **First day** and the
   **Last day**; with it off, pick the **Start date** and **Start time** and the
   **End date** and **End time**. Times are read in your own time zone — the sheet says
   which, under the times — and the appointment remembers the zone it was made in.
4. Add a **Location** and a **Description** if you want to.
5. Under **Visibility**, choose **Shared** or **Private**. A colleague who may see your
   calendar sees a shared appointment's title; a private one shows to them only as
   **Busy**.
6. Under **Show me as**, choose **Busy** or **Free**. A free appointment — a reminder to
   yourself, say — is not shown on your calendar to colleagues at all.
7. Under **Attendees**, search for a colleague by name and pick them. Up to 50
   colleagues can be on one appointment. Each sees it on their own calendar and can
   answer it.
8. Press **Save**. The appointment is on your calendar at once, and on your attendees'.

Click one of your appointments to open it again, change it and **Save**, or **Delete**
it. Dragging one of your own appointments to another time or day on **Month**, **Week**
or **Day**, or stretching it from its edge, saves the new time straight away. Items from
the other apps cannot be dragged.

**From a customer or a project.** The **New appointment** action on a customer's
**Overview** or in a project's header opens the same sheet with the customer or the
project already filled in, shown as a badge under the title. The appointment stays
yours — it is not listed on the customer — but it names where it came from: the
customer's or the project's name for anyone who may see that customer or project, and
otherwise only its number (**Customer: no. 42**). The
search in the top bar offers **New appointment** too.

When you change **when** an appointment is — its dates or times, all day or not, or how
it repeats — every attendee's answer goes back to **Not answered**, because what they
answered is no longer what is asked. Changing the title, the description or the location
leaves their answers alone. Answering an invitation changes nothing of the appointment
itself, so the organiser can go on editing while answers come in.

## Repeating appointments

In the sheet, choose under **Repeat** how the appointment repeats:

- **Daily**, **Weekly** on the days you tick, or **Monthly** — either on a day of the
  month (**Day 21**) or on a weekday of the month, such as the second Tuesday or the
  last Friday;
- **Every** one, two or more days, weeks or months, up to 99;
- under **Ends**: **Never**, **On a date** (the **Last day** it may fall on), or
  **After a number** of **Times**.

The appointment's first day is always its first occurrence, and counts toward
**Times**, so it must fit the pattern: a weekly appointment on Mondays cannot start on a
Tuesday — the sheet refuses it under **Repeat** until you move the start to a Monday.

A repeating appointment keeps its time of day in the time zone it was made in, through
the change to and from summer time: a meeting every Monday at 09:00 stays at 09:00.

When you save or delete an occurrence of one of your repeating appointments — in the
sheet, or by dragging it — the calendar asks whether you mean **This occurrence** or
**The series**:

- **This occurrence** moves or cancels that one day and leaves the rest. Only its time
  changes: anything else you edited in the sheet is not saved this way. A moved
  occurrence shows where you moved it, even before the series starts or after it ends.
- **The series** changes or deletes every occurrence; a new time moves them all by the
  same amount. As the question warns, this can remove the changes you made to single
  occurrences: a new start, a switch to or from all day, or a change of how it repeats
  puts them back to the series' pattern, so re-do the ones that matter. A new end time
  alone keeps them.

When you open one of your repeating appointments, the sheet lists its
**Changed occurrences** under **Repeat** — "Cancelled …" or "Moved from … to …" — each
with **Put back**, which undoes that one change and brings the series' own occurrence
back.

## Answer an invitation

An appointment a colleague put you on shows on your calendar as soon as it is made.
Click it to open it: you see what it is, when, the **Organiser** and the other
**Attendees** with their answers, and **Your answer**. Answer with **Accept**,
**Tentative** or **Decline**. You can change your answer at any time. An appointment you
declined stays on your calendar, struck through, so you can still change your mind. Only
the person who made it can change or delete it.

## A colleague's calendar

With `calendar:view-colleagues`, the toolbar shows a colleague picker, which reads
**My calendar** while you see your own. Search for a colleague by name and pick them to
see their calendar instead of yours, read-only:

- their appointments that make them busy, with the title and location when the
  appointment is shared;
- a private one as a hatched **Busy**, with nothing else;
- nothing for their free appointments, and nothing from the other apps — their tasks,
  hours and follow-ups stay behind those apps' own doors.

The legend shrinks to **Appointments** and **Public holidays**, and **New appointment**
is hidden. Clear the picker to come back to your own calendar.

## The dashboard

With Calendar on, the dashboard opens with **Today and this week**: today's public
holidays, appointments and items in order with their times — **All day** for what lasts
the day — each a link to its record or to the appointment on the calendar; below, the
next six days one line each with how many items are on them, which you click to see
them; **New appointment**; and **Open the calendar**. The **Needs attention** list below
keeps its follow-ups and tasks due: it says what needs you, the agenda says when.

## What comes next

Invitations to people outside the company, by mail with an answer they can give from the
message; a calendar address your phone can subscribe to, so your Vantigo calendar shows
in the phone's own calendar app; and calendars from elsewhere, subscribed to and shown
beside your own. None of these is built yet.
