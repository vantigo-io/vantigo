---
title: Calendar
description: Your appointments and what the other apps have put in your days, inviting colleagues, customers' contacts and anyone with an address, what an invitee receives and how they answer, cancelling, colleagues' busy times, repeating appointments, the dashboard's agenda, your calendar on your phone and calendars from elsewhere beside your own.
sidebar:
  order: 35
sources:
  - apps/calendar/frontend
  - apps/host/frontend/src/routes/rsvp.tsx
  - apps/host/frontend/src/lib/public-paths.ts
  - apps/host/frontend/src/routes/calendar
  - apps/host/frontend/src/routes/calendar.tsx
---

The Calendar app is one view of your days: the appointments you make yourself, with
colleagues on them, beside what the other apps already know about you — your follow-ups
in Customers, your absence in Staff, your tasks in Projects, your hours in Time, your
trips in Expenses and your confirmed bookings in Planning. Nothing is copied: a follow-up you tick in Customers is ticked on the
calendar at once, because the calendar asks Customers each time it is opened. The rules
behind every step are in the [Calendar module reference](/en/reference/calendar/); this
page is about what you do on each screen.

## Who sees what

| What | Who sees it |
| --- | --- |
| **Calendar** in the sidebar, your own calendar, your appointments and your answers to invitations, and the dashboard's **Today and this week** | everyone with `calendar:access` |
| **A colleague's calendar** | `calendar:access` and `calendar:view-colleagues` |
| **Contacts** under **Attendees**, to invite a customer's contacts | `calendar:access`, `customers:contacts-view` and `customers:associations-view` |
| **My calendar** in the sidebar: your calendar addresses and the calendars you subscribe to from elsewhere | everyone with `calendar:access` |
| The answer page an invitation links to | anyone with the link, without signing in |
| What a calendar address carries | any program given the address, without signing in — so keep it to yourself |

What the other apps put on your calendar follows their own permissions: you see your
follow-ups only if you may read Customers' timeline, your absence only if your user is
linked to your record in Staff, your bookings only if you may use Planning
(`planning:access`) and your user is linked to your record in Staff, and so on. An app you have no access to, or that your
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
| **Bookings** | Your own confirmed bookings in Planning, all day over the booking's period, titled with the project's code and name; hold the pointer over one to read the load. A tentative booking, the planners' draft, is never shown | opens **My planning** in Planning |
| **Subscriptions** | An event of a calendar you subscribe to from elsewhere, in the colour you gave it ([Calendars from elsewhere](#calendars-from-elsewhere)) | — |
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
7. Under **Attendees**, search for a colleague by name under **Colleagues** and pick
   them. Each sees it on their own calendar, can answer it, and is told by mail. You can
   invite people outside the company here too, under **Contacts** and **By email**
   ([Invite people outside the company](#invite-people-outside-the-company)). Up to 50
   people can be on one appointment.
8. Press **Save**. The appointment is on your calendar at once, and on your colleagues'.

Click one of your appointments to open it again, change it and **Save**, or **Delete**
it — once someone outside the company has been invited, a delete becomes a
[cancellation](#delete-an-appointment-you-invited-people-to). Dragging one of your own appointments to another time or day on **Month**, **Week**
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

## Invite people outside the company

An appointment can have people on it who do not use Vantigo: a contact of one of your
customers, or anyone with an email address. They are sent an invitation by mail, which
their own mail program — Gmail, Outlook, Apple Mail — shows as an invitation, and they
answer it with a link in the mail. Their answer shows up on the appointment in Vantigo.

Under **Attendees** in the sheet there are three ways to add someone, all into the same
list:

- **Colleagues** — search for a colleague by name, as before.
- **Contacts** — pick one or more of the customer's contacts. This is shown on an
  appointment made from a customer (its **New appointment** action), and only to
  someone who may see the customer's contacts and their addresses
  (`customers:contacts-view` and `customers:associations-view`). Only contacts with an
  email address are offered, since the point is to mail them.
- **By email** — type an address, and a name if you like, and add it.

Up to 50 people of all three kinds together can be on one appointment. Press **Save**:
each person you added from outside is sent their invitation straight away, one mail
each. Saving never waits for the mail.

Each row in the list shows what kind of attendee it is, their name, and their answer.
A contact or an address also shows how their invitation went:

- **Invited 3 Nov 2026** — the last invitation was sent that day.
- **Not sent** — nothing has been sent. When your installation cannot send mail, every
  contact and address shows this, and the sheet says "Invitations by mail are not set up
  on this installation". The person is kept on the appointment; once mail is set up,
  press **Resend** (or change the time or the place).
- **Sending failed** — the last mail to them did not go out. Press **Resend** to try
  again. If it keeps failing, ask your administrator; for a contact, check that they
  still have an address in Customers.

**Resend** is beside every contact and address on a saved appointment, as long as your
installation can send mail.

A contact Customers has anonymised shows as **A contact**, and nothing more is sent to
them.

### What the person receives

One mail, from your company's own sending address, under your name, titled
**Invitation:** followed by the title and the day, and written in your language — the **Preferred
language** in [your profile](/en/user/getting-started/#edit-your-profile): Norwegian when
it is **Norwegian**, English otherwise, **Automatic** included. It says what the appointment is, when (in the time zone
you made it in), where, that you are organising it, and your description, and asks them
to answer with the link in the mail so you see the answer in Vantigo. The appointment is
attached, so their mail program can show its own answer buttons and add it to their
calendar.

The answer that reaches you is the one given **through the link**. If they use their
mail program's own buttons instead, the appointment is added to their calendar, but the
answer goes to your company's sending address and not to you — the mail tells them so.

They never see your description through the link, nor who else is invited; the mail
itself lists the other people from outside the company, as mail programs expect, but
never your colleagues.

### See their answers

Open the appointment: each row shows **Accepted**, **Tentative**, **Declined** or
**Not answered**. When someone from outside answers through the link, you are also sent a
short mail — "Ola Hansen accepted: Customer meeting", "… declined: …" or "… tentatively
accepted: …" — with a link to the appointment.

### When you change the appointment

- Changing **when** (the dates, the times, all day or not, how it repeats) or
  **where** sends everyone from outside an updated invitation, titled
  **Updated: …**. As for colleagues, their answers go back to **Not answered**, except
  that a change of the place alone keeps them.
- Moving, cancelling or putting back one occurrence of a repeating appointment sends
  everyone the updated series.
- Adding someone sends the invitation to them alone; taking someone off sends them a
  cancellation, if they had been sent an invitation. An address you type that is
  already a contact on the list is kept once, as the contact.
- Changing only the title, the description or who can see it sends nothing.

### Resend an invitation

**Resend** on a contact's or an address's row sends the same invitation again, with the
same link, so an earlier mail still works — titled **Updated: …** if they had been sent
one before. Use it when a send failed, or when the person cannot find the mail. A
notice, "Invitation sent", confirms it went.

## Delete an appointment you invited people to

As long as no invitation has gone out to anyone outside the company, **Delete** removes
the appointment as it always has, and your colleagues on it are told by mail that it was
cancelled. Once someone outside has been sent one, deleting it
**cancels** it instead: each of them who was sent an invitation is mailed
**Cancelled: …**, which their mail program uses to take it off their calendar — someone
whose invitation never went out is sent nothing — and your colleagues on it are told by mail.
The appointment leaves every calendar in Vantigo, but you and your attendees can still
open it from a link: it opens read-only, with the banner **Cancelled on 3 Nov 2026**
and nothing to change. Some time after its last day it is removed for good.

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

An appointment a colleague put you on shows on your calendar as soon as it is made, and
you are sent a short mail about it — "Kari Nordmann invited you: Customer meeting, Tue
3 Nov 09:00" — and again when its time or place changes, when you are taken off it, and
when it is cancelled or deleted (a change to one occurrence of a repeating appointment
sends none). The mail is in your own language and links to the appointment ("Open it in
Vantigo"); you answer in Vantigo.
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
  hours, follow-ups and bookings stay behind those apps' own doors.

The legend shrinks to **Appointments** and **Public holidays**, and **New appointment**
is hidden. Clear the picker to come back to your own calendar.

## The dashboard

With Calendar on, the dashboard opens with **Today and this week**: today's public
holidays, appointments and items in order with their times — **All day** for what lasts
the day — each a link to its record or to the appointment on the calendar; below, the
next six days one line each with how many items are on them, which you click to see
them; **New appointment**; and **Open the calendar**. The **Needs attention** list below
keeps its follow-ups and tasks due: it says what needs you, the agenda says when.

## If you received an invitation

This section is for the person outside the company who got an invitation from someone
using Vantigo.

1. Open the mail, **Invitation: …**. It says what the appointment is, when and where,
   and who is organising it.
2. Click the link in the mail. A page opens in your browser — no account or sign-in is
   needed — with the appointment, the time in its own time zone and, if yours is
   different, in yours, where it is, and who organises it. It is in the language the
   invitation was written in.
3. Under **Will you attend?**, answer **Accept**, **Maybe** or **Decline**. The page
   says "Thank you. Your answer has been sent to the organiser.", and the organiser sees
   it. You can open the link again and change it at any time.

Your mail program's own answer buttons add the appointment to your calendar, but do not
tell the organiser: use the link for that. If the appointment changes, you are sent an
updated invitation with the same link; if it is cancelled, you are sent a cancellation,
and the page says **This appointment has been cancelled**. A page that says **This link
is not valid** means you were taken off the appointment, or it is long past.

## Your calendar on your phone

Vantigo can give you a **calendar address**: a link your phone's calendar app — or Google
Calendar, Outlook, or any other program that subscribes to calendars — reads to show your
Vantigo calendar beside your other calendars. It is read-only there: you change
appointments in Vantigo, and the phone shows the change the next time it refreshes.

Open **Calendar** and choose **My calendar** in its sidebar. Under **Your calendar on your
phone** there are two addresses, each with a sentence saying what it carries:

- **Full calendar** — your calendar as you see it in Vantigo: your appointments, with
  their titles, places and descriptions, and your hours, trips, tasks, follow-ups,
  absence and bookings from the other apps. Each event links back to Vantigo.
- **Busy only** — the times you are busy, each titled **Busy**, and nothing else. Give
  this one to someone who should see when you are free but not what you are doing — a
  receptionist, or your partner's calendar.

For each one you want:

1. Press **Create address**. The address appears in a field, with when it was created
   and when a program last read it (**never read** until one has).
2. Press **Copy**, or **Copy as webcal** for the same address starting `webcal://`, which
   Apple Calendar and Outlook subscribe to with one click.
3. Add it to your calendar program, as below.

The screen shows the address again whenever you come back, so you can add it to your
laptop next month without changing anything.

**Reset** gives the address a new link, and the old one stops working at once — use it if
the address has got into the wrong hands. The screen asks first, since your phone stops
updating until you give it the new address. **Turn off** removes the address altogether,
after asking too; whatever subscribed to it stops updating.

An address works only while its owner may use Calendar. When a colleague leaves and their
account is disabled, or `calendar:access` is taken from them, their addresses stop answering at once, and every phone or program subscribed to
them stops updating — nobody needs to reset anything.

### Add it to your calendar program

- **iPhone or iPad:** **Settings → Calendar → Accounts → Add Account → Other → Add
  Subscribed Calendar**, paste the address, then **Next** and **Save**.
- **Calendar on a Mac:** **File → New Calendar Subscription**, paste the address, and
  **Subscribe**.
- **Google Calendar**, in a browser: beside **Other calendars**, press **+**, then
  **From URL**, paste the address and **Add calendar**. The events can take up to a day
  to appear.
- **Outlook:** **Add calendar → Subscribe from web**, paste the address, give it a name
  and import it.

Your phone decides how often it refreshes: Apple Calendar as you set it, Outlook about
every three hours, Google Calendar up to a day. So a change you make in Vantigo reaches
the phone later, not at once.

When the screen says **This address works on the company's network only**, your
installation has no public address: a phone reads it on the office network or its VPN,
but not out and about. Ask your administrator if you need more.

What is not in it: appointments you declined, cancelled appointments, the calendars you
subscribe to from elsewhere ([below](#calendars-from-elsewhere)) and public holidays —
your phone has its own. Nor does it say who else is on an appointment; open it in
Vantigo for that. The **Busy only** address also leaves out what does not make you busy:
appointments shown as **Free**, the all-day items from the other apps, such as a task
due, a follow-up or a day of absence, and the items that are a moment rather than a
span, such as a customer interaction logged at a time. Its events carry nothing that
says which app or record they came from.

## Calendars from elsewhere

You can show other calendars beside your own: a school's term dates, a club's
matches, your private Google Calendar. They are shown on your calendar page, in the
colour you choose, and to nobody else.

1. Open **Calendar → My calendar**, and under **Calendars from elsewhere** press
   **Add calendar**.
2. Give it a **Name**, and paste its **Address**: the address a calendar program gives
   you to subscribe to — it usually ends in `.ics`, and one starting `webcal://` works too.
3. Pick a **Colour**, and tick **Show as busy only** if you want to see when, not what.
4. Save. Vantigo fetches the calendar at once, and its events are on your calendar page
   the next time it loads. An address Vantigo cannot use is refused on the field, with
   the reason. One that is saved but cannot be fetched is kept: the screen says **Saved,
   but the calendar could not be fetched**, with the reason, which stays on its row.

An event from a calendar you subscribe to is drawn in its colour, with the calendar's
name under its title, and labelled **Busy** when you chose busy only. The legend gains a
**Subscriptions** chip, which hides them all at once. You cannot drag them, and clicking
one opens nothing: they belong to the other calendar. Events the other calendar has
cancelled are not shown.

The table lists each calendar with its colour, its name, the address's site, whether it
is busy only, and when it was last updated (**Updated 10 minutes ago**). Vantigo fetches
each one again about every half hour; **Refresh now** on its row fetches it at once.
**Edit** changes it, and **Remove** takes it off your calendar. You can have at most ten.
Adding, saving a change and **Refresh now** together are limited: Vantigo fetches at
most thirty times in ten minutes from one address — your computer's, or your office
network's — so if it refuses, wait a few minutes and try again.

When a fetch fails, the reason is shown in red under the row, and the calendar goes on
showing what it last read:

- **the address redirects; subscribe to the address it points at** — open the address in
  a browser and use the one it ends up on;
- **the address did not answer a calendar** — it is a web page, not the calendar: look on
  that page for its subscribe or `.ics` address;
- **the address answered 404 Not Found** (or another number) — the calendar has moved or
  been removed, or the address is mistyped;
- **the address is not one Vantigo may fetch** — it is on a private network Vantigo does
  not reach out to;
- **the calendar is larger than 1 MiB**, **the calendar has more than 10,000 events**, or
  **the address did not answer in time** — the calendar is too big, or its site too slow;
  ask whoever publishes it;
- **the address could not be reached** — the site does not answer, or its name does not
  exist; check the address.

These calendars are yours alone: a colleague looking at your calendar does not see them,
and they are never in your own calendar address, so a phone subscribed to your Vantigo
calendar does not get them twice.

## What comes next

Both the calendar address for your phone and calendars from elsewhere are built. What
remains is further off: two-way sync with Google and Microsoft, booking pages, rooms and
resources, reminders in the app and by mail, CalDAV, and team calendars.
