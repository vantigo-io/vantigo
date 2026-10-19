---
title: Calendar
description: Turning Calendar on and giving people its two permissions; what invitations to people outside the company need — the SMTP driver, a sending domain with SPF, DKIM and DMARC, the public host and its three routes; the cancellations worker and its two settings; how to check that an invitation went, and what can go wrong.
sidebar:
  order: 45
sources:
  - apps/server/internal/calendar/invitations.go
  - apps/server/internal/calendar/rsvp.go
  - apps/server/internal/calendar/cancellations_worker.go
  - apps/server/internal/calendar/module.go
---

Calendar is one view of each person's days: the appointments they make, with colleagues,
customers' contacts and other people on them, beside what the other modules know about
them. This page is the operator's side: turning it on, what the invitations to people
outside the company need, and the worker that tidies up after a cancellation. What the
module does and the rules it checks are in [the reference](/en/reference/calendar/); what
a person does on its screens is in [the user guide](/en/user/calendar/).

## Before you start

- Calendar needs no other business module. `MODULES` unset enables it with every other
  module; an installation that lists `MODULES` adds `calendar` to the list, alone if it
  likes (`MODULES=calendar`). With Customers on as well, an appointment can be made from
  a customer and its contacts invited.
- The migrations create the `calendar` schema whether or not the module is enabled, so
  turning it on later needs no migration. A least-privilege application role needs the
  `calendar` schema granted like every other ([Installation](/en/admin/installation/)).
- Calendar has no settings of its own. Its two permissions are `calendar:access`, to use
  it, and `calendar:view-colleagues`, to see colleagues' busy and free times, which a
  workspace that wants everyone to see each other's grants to the employee role
  ([Permissions](/en/reference/calendar/#permissions)).

## Enable the module

1. Add `calendar` to `MODULES` in `vantigo.env`, or leave `MODULES` unset.
2. Restart Vantigo (`docker compose up -d`).
3. Verify: `GET /api/v1/calendar/meta` answers 401 without a session rather than the 404
   a disabled module's path answers.

A disabled module contributes no route, no permission and no worker; its data stays in
the database untouched.

## Invitations to people outside the company

An appointment can have people on it who are not users: a customer's contact, or an
address typed in. Each is sent an invitation by mail, one message per person, in the
layout Gmail, Outlook and Apple Mail show their own **Yes / Maybe / No** buttons for,
with a link to answer by that lands the answer in Vantigo
([invitations](/en/reference/calendar/#invitations)). Colleagues on an appointment are
mailed a short notice through identity instead. Both go through the installation's own
mail configuration; nothing here has a setting of its own.

### Preconditions

1. **The SMTP driver.** `MAIL_DRIVER=smtp` with `SMTP_HOST`, `SMTP_FROM` and the rest of
   [Email delivery](/en/admin/authentication/#email-delivery). Under any other driver an
   outside attendee is stored but never mailed: the sheet shows them as **Not sent**,
   with the note "Invitations by mail are not set up on this installation", the log
   says `calendar: invitations not sent: the mail driver is not smtp`, `GET /api/v1/calendar/meta` answers
   `invitationsAvailable: false`, and **Resend** answers 503 `invitations_unavailable`.
2. **A sending domain with SPF, DKIM and DMARC.** Every invitation is sent **from
   `SMTP_FROM`**, under the organiser's name, and names `SMTP_FROM` as its organiser
   (`ORGANIZER`) — the one arrangement no mail client refuses; the organiser's own
   address is never used. So `SMTP_FROM` must be an address on a domain you control
   whose SPF record names your SMTP server, whose mail the server signs with DKIM, and
   which publishes a DMARC policy the two align with. The reason is Google's: since 2022
   Google Calendar by default adds an invitation to the invitee's calendar **only when
   the sender is known**, and an unauthenticated sender is not — the invitation then
   waits in the mail until the person clicks in it, or lands as spam, and the buttons
   may not show at all. A mail server's own name, or a free-mail address, as `SMTP_FROM`
   will not pass.
3. **A mailbox behind `SMTP_FROM`**, or a sink. When an invitee presses a client's own
   button, the client mails its answer (`METHOD:REPLY`) to the organiser — that is, to
   `SMTP_FROM`. Vantigo does not read that mailbox: the answer that counts is the one
   given through the link, which the invitation asks for. Let the mailbox accept and
   discard, or let someone glance at it; do not let it bounce.
4. **`PUBLIC_BASE_URL`, for people outside your network.** The answer link is
   `PUBLIC_BASE_URL/rsvp?token=…` when `PUBLIC_BASE_URL` is set, and
   `APP_URL` (with `APP_BASE_PATH`)`/rsvp?token=…` otherwise. The page needs no sign-in
   in either case, but without a public host only someone who can reach `APP_URL`
   can open it — enough for invitees on the company's own network, not for a customer.
   Set up the public host as for the Invoices pay page
   ([The public host](/en/admin/payments/#the-public-host)): it is one host for both, and
   the steps there — a fixed `Host` at the proxy, the proxy trusted, the query kept out
   of the proxy's logs — hold for the answer link's token as they do for the pay link's.
   A link is fixed when the person is added to an appointment, so set `PUBLIC_BASE_URL`
   before people start inviting.

### The public host's three routes

On the public host Vantigo serves the answer page beside the pay page, and nothing else
of the calendar:

| Method | Path |
| --- | --- |
| `GET`, `HEAD` | `/rsvp` (the answer page; any query) |
| `POST` | `/api/v1/calendar/rsvp/read` (the page's read; the token in the body) |
| `POST` | `/api/v1/calendar/rsvp` (the answer) |

The two operations are anonymous and rate limited per client address — 60 reads and 30
answers in ten minutes — so behind a proxy, `TRUSTED_PROXY_HOPS` and
`TRUSTED_PROXY_CIDRS` must name it, or every invitee shares the proxy's one bucket
([Forwarded headers and HTTPS](/en/admin/authentication/#forwarded-headers-and-https)).
The link's token is in the query of the page request alone: Vantigo's request log
writes the path and never the query, and the page posts the token rather than putting
it in an API address. Keep the proxy's access log to the path for the whole public host,
as the pay page's steps show.

### Verify

1. **Send yourself an invitation.** Make an appointment for tomorrow and, under
   **Attendees → By email**, add an address of yours at Gmail or Outlook.com — outside
   your own domain, so the check is what a customer sees. Save.
2. **The attendee row.** Open the appointment again: the row shows
   **Invited** and today's date (**Invited 3 Nov 2026**). **Sending failed** means the SMTP server refused or did not answer; see
   below.
3. **The mail.** It arrives from `SMTP_FROM` under your name, titled
   `Invitation: <title> · <day>` (`Invitasjon: …` when your profile's preferred language is Norwegian),
   with the client's own buttons and an `invite.ics`
   attachment. In Gmail, **Show original** should say `SPF: PASS`, `DKIM: PASS` and
   `DMARC: PASS`.
4. **The link.** Open the link in the mail from a phone off the company's network: the
   page shows the appointment without signing in, and its address bar no longer shows
   the token. Answer **Accept**; the attendee row in the sheet shows the answer, and you
   are mailed "… accepted: …".
5. **A cancellation.** Delete the appointment: the invitee is mailed `Cancelled: …`, and
   the link now says the appointment has been cancelled.

### Watching it

A failed send is in two places: on the attendee row, as **Sending failed**, with
**Resend** beside it, which the organiser sees; and in the log, as the warning
`calendar: an invitation could not be sent` with `appointment_id`, `attendee_id`,
`method` and the error, the address in it replaced by `<recipient>`. A contact with no
address is logged at info, `calendar: invitation not sent: the contact has no address
the directory answers`; a colleague's notice that fails, `calendar: a notice could not be
sent`. Nothing is queued and nothing retries by itself: the organiser presses
**Resend** when the cause is fixed. The sends for one save run one after the other after
it is saved, within thirty seconds together — the ones not reached by then are marked
**Sending failed** — and the save itself never fails for the mail.

## The cancellations worker

When an appointment someone outside the company has been sent an invitation to is
deleted, it is not removed: it is **cancelled** — kept, read-only and off everyone's
calendar, so the cancellation can name what it cancels and a link already mailed still
says so ([cancellation](/en/reference/calendar/#cancellation)). The
`calendar-cancellations` worker erases such an appointment once its last day has passed:

- a single appointment, after its end;
- a series with a last day, after that day;
- a series that never ends, ninety days after it was cancelled.

Once erased, its invitees' links say **This link is not valid**.

The worker runs unless `CALENDAR_CANCELLATIONS_ENABLED=0`, every `CALENDAR_WORKERS_POLL`
(default `1h`, at least `1m`) — in worker mode, and in api mode with
`WORKERS_IN_PROCESS=1` ([Background workers and
scaling](/en/admin/installation/#background-workers-and-scaling)). It erases at most 200
appointments a cycle and the rest at the next. Several replicas take turns through an
advisory lock; one erases at a time. Turned off, cancelled appointments are kept until
it is on again — on nobody's calendar, but a row each.

| Variable | Purpose | Default |
| --- | --- | --- |
| `CALENDAR_CANCELLATIONS_ENABLED` | On/off switch for the `calendar-cancellations` worker | `1` |
| `CALENDAR_WORKERS_POLL` | How often the calendar's workers look; a duration of at least `1m` | `1h` |

## Key material

The answer links are sealed at rest under a key derived from `APP_SECRET` (the purpose
`calendar/rsvp-link`), so a **Resend** carries the same link as the first mail. Rotating
`APP_SECRET` leaves every stored link unopenable: a link already mailed still answers —
its token is kept as a plain hash, which needs no key — but an update or a **Resend** to
an attendee added before the rotation fails and shows **Sending failed**; take them off
the appointment and add them again for a new link
([Key material](/en/admin/authentication/#key-material)).

## What can go wrong

- **The invitee sees a plain mail with no buttons.** The client did not recognise the
  invitation: it was forwarded (a forwarded invitation names someone else), or a
  gateway on the way stripped the `text/calendar` part. Ask them to answer by the link,
  which works in any client.
- **The invitation is not on the invitee's Google calendar until they click in the
  mail, or it lands as spam.** `SMTP_FROM`'s domain fails SPF, DKIM or DMARC alignment,
  or is unknown to the invitee. Check **Show original** in Gmail and fix the domain's
  records.
- **Outlook drops the invitation without a trace.** The invitee's address is
  `SMTP_FROM` itself: Outlook discards an invitation whose organiser is the recipient.
  Invite another address.
- **Every attendee shows Not sent.** `MAIL_DRIVER` is not `smtp`; the sheet says so.
  Set it, restart, then **Resend** each, or change the appointment's time or place,
  which mails everyone.
- **Sending failed** on an attendee. The SMTP server refused or did not answer — the log
  has the reason — or the attendee is a contact whose address was removed in Customers,
  or who has been anonymised there (the row then shows **A contact**). Fix the cause and
  press **Resend**; a contact with no address must be given one in Customers first.
- **The link opens nothing for a customer, but works in the office.** `PUBLIC_BASE_URL`
  is unset, so the link is on `APP_URL`, which the customer cannot reach. Set up the
  public host. Links already mailed keep their old address; an attendee taken off the
  appointment and added again is mailed a cancellation, then a new invitation with a
  link on the public host.
- **The answer page is blank, or its requests answer 404, on the public host.** The
  proxy serves it under another path than `APP_BASE_PATH`, or strips the base path
  itself.
- **Invitees are refused with 429.** `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` are
  not set, so every invitee shares the proxy's address and its one bucket.
- **"I answered with the buttons, and the organiser sees no answer."** The buttons mail
  `SMTP_FROM`, which Vantigo does not read. Answer with the link.
- **A cancelled appointment is still there.** Its last day has not passed, or the worker
  is off (`CALENDAR_CANCELLATIONS_ENABLED=0`).
- **`CALENDAR_WORKERS_POLL` refused at start.** It is not a duration, or is under a
  minute.
