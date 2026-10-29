---
title: Calendar
description: Turning Calendar on and giving people its two permissions; what invitations to people outside the company need — the SMTP driver, a sending domain with SPF, DKIM and DMARC, the public host and its routes; the calendar feed on the public host, its rate limit and the proxy's log; the cancellations, subscriptions and mailbox sync workers and their settings, and what Vantigo fetches from the internet; how to check that an invitation went and that the feed answers, and what can go wrong.
sidebar:
  order: 45
sources:
  - apps/server/internal/calendar/invitations.go
  - apps/server/internal/calendar/rsvp.go
  - apps/server/internal/calendar/cancellations_worker.go
  - apps/server/internal/calendar/module.go
  - apps/server/internal/calendar/feed.go
  - apps/server/internal/calendar/feed_tokens.go
  - apps/server/internal/calendar/subscriptions_worker.go
  - apps/server/internal/calendar/subscriptions_fetch.go
  - apps/server/internal/calendar/mailbox_worker.go
---

Calendar is one view of each person's days: the appointments they make, with colleagues,
customers' contacts and other people on them, beside what the other modules know about
them. This page is the operator's side: turning it on, what the invitations to people
outside the company need, the worker that tidies up after a cancellation, the calendar
feed a phone subscribes to, the worker that fetches the calendars people subscribe
to from elsewhere, and the worker that keeps their copies in Outlook. What the
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
- Calendar has no workspace settings of its own: each person's calendar addresses and
  subscriptions are theirs, on their **My calendar** screen. Its two permissions are `calendar:access`, to use
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

### The public host's routes

On the public host Vantigo serves the answer page beside the pay page, and the
[calendar feed](#the-feed-on-the-public-host), and nothing else of the calendar:

| Method | Path |
| --- | --- |
| `GET`, `HEAD` | `/rsvp` (the answer page; any query) |
| `POST` | `/api/v1/calendar/rsvp/read` (the page's read; the token in the body) |
| `POST` | `/api/v1/calendar/rsvp` (the answer) |
| `GET` | `/api/v1/calendar/feed/{token}.ics` (a calendar address; exactly one segment) |

The two operations are anonymous and rate limited per client address — 60 reads and 30
answers in ten minutes — so behind a proxy, `TRUSTED_PROXY_HOPS` and
`TRUSTED_PROXY_CIDRS` must name it, or every invitee shares the proxy's one bucket
([Forwarded headers and HTTPS](/en/admin/authentication/#forwarded-headers-and-https)).
The link's token is in the query of the page request alone: Vantigo's request log
writes the path and never the query, and the page posts the token rather than putting
it in an API address. Keep the proxy's access log to the path for the whole public host,
as the pay page's steps show. The public host also admits, beside these, one route of the
Microsoft Entra ID module: `POST /api/v1/entra/mailbox-notifications`, the webhook of the
Outlook sync's [change notifications](#the-mailbox-sync-worker).

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
**Resend** when the cause is fixed. The mails of one save — invitations and colleagues'
notices — go one after the other after it is saved, within thirty seconds together; the
invitations not reached by then are marked **Sending failed**, and the save itself never
fails for the mail. An organiser's notice of an answer has ten seconds.

## The feed on the public host

Each person can create a **calendar address** on their **My calendar** screen — a full
one and a busy-only one — that their phone, Google Calendar or Outlook subscribes to
([the feed](/en/reference/calendar/#the-feed)). A calendar program takes one address and
`GET`s it, with nothing of Vantigo's, so this is the one anonymous `GET` in Vantigo with
its token in the path:

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/calendar/feed/{token}.ics` |

It is on the public host's list beside the answer page's routes
([the public host's routes](#the-public-hosts-routes)), under `GET` alone and with exactly
one segment after `/feed/`. A token nobody holds — never issued, reset or turned off — is
a bare 404.

So is the token of a person who may no longer use Calendar: **disabling a user, or
taking `calendar:access` from them, kills every address of theirs at once**, with no
step to take. At every request the feed asks identity whether the address's person may
act — not disabled, locked out or pending, and, while SCIM is configured, not deactivated
by the directory unless an Owner — and holds `calendar:access`, the same checks a
session is held to ([the feed](/en/reference/calendar/#the-one-anonymous-get)). The
address is not deleted: enabled again, with the permission, the person's phone updates
again.

### What it needs

1. **The base.** The address is `PUBLIC_BASE_URL/api/v1/calendar/feed/<token>.ics` when
   `PUBLIC_BASE_URL` is set, and on `APP_URL` (with `APP_BASE_PATH`) otherwise. A phone
   off the company's network needs the public host; a laptop on it does not. Without a
   public host the screen tells the person *"This address works on the company's network
   only."* An address is fixed when it is created, so set `PUBLIC_BASE_URL` before people
   create theirs; one created before needs a **Reset** to move to the public host. The
   note follows the setting, not the address: once `PUBLIC_BASE_URL` is set it is gone
   for everyone, even beside an address still on `APP_URL` — read the address itself.
2. **The proxy, as for the answer page.** The feed is rate limited per client address —
   `calendar-feed`, **120 requests per 10 minutes** — which a phone asking hourly, or an
   office of phones behind one NAT, stays well under. Behind a proxy,
   `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` must name it, or every phone shares the
   proxy's one bucket ([Forwarded headers and
   HTTPS](/en/admin/authentication/#forwarded-headers-and-https)).
3. **The proxy's access log.** The token is the path's last segment, not a query: Vantigo
   logs the path of every request, and so does a proxy. **Keep the proxy's access log on
   the public host to the path without the feed's last segment, or to nothing**, and
   restrict who reads Vantigo's own request log, which writes the path. A token in a log
   is a read of that person's calendar until they reset it.

### Verify the feed

1. **Create an address.** Sign in, open **Calendar → My calendar**, press **Create
   address** under **Full calendar** and **Copy**.
2. **Fetch it** from outside the company's network:

   ```sh
   curl -i 'https://pay.example.com/api/v1/calendar/feed/<token>.ics'
   ```

   It answers 200 with `Content-Type: text/calendar; charset=utf-8`, an `ETag` and
   `Cache-Control: private, no-cache`, and a body that starts `BEGIN:VCALENDAR`.
3. **Fetch it again with the tag:**

   ```sh
   curl -i -H 'If-None-Match: "<the ETag>"' 'https://pay.example.com/api/v1/calendar/feed/<token>.ics'
   ```

   It answers 304 with no body. The screen now shows when the address was last read.
4. **A wrong token** — change one character — answers 404, and so does the address after a
   **Reset**.
5. **The proxy's log** holds no token for these requests.

## The cancellations worker

When an appointment someone outside the company has been sent an invitation to is
deleted, it is not removed: it is **cancelled** — kept, read-only and off everyone's
calendar, so the cancellation can name what it cancels and a link already mailed still
says so ([cancellation](/en/reference/calendar/#cancellation)). The
`calendar-cancellations` worker erases such an appointment once its last day has passed:

- a single appointment, after its end;
- a series with a last day, after that day — or after the end of an occurrence moved
  past it, whichever is later;
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

## The subscriptions worker

A person can subscribe to calendars from elsewhere on their **My calendar** screen — at
most ten each — and see their events beside their own
([subscriptions](/en/reference/calendar/#subscriptions)). The `calendar-subscriptions`
worker fetches them. It runs unless `CALENDAR_SUBSCRIPTIONS_ENABLED=0`, in worker mode,
and in api mode with `WORKERS_IN_PROCESS=1`. It **wakes every five minutes** — not a
setting — and fetches each subscription that is due, at most 200 a cycle, one at a time.
Each is claimed for five minutes while it is fetched, so several replicas share the
subscriptions due and never fetch one twice at once, and no database connection is held
while a calendar is fetched. Each subscription is due
`CALENDAR_SUBSCRIPTIONS_POLL` (default `30m`, at least `5m`) after its last attempt, or —
after a fetch that answered the calendar — later when the calendar itself asks to be read
less often (at most once a day), plus a little jitter. A failed fetch keeps the calendar's last good copy and records the
reason, and the next due fetch tries again, with no backoff. Turned off, nothing is
fetched in the background and every subscription shows its last read; saving one, or
**Refresh now**, still fetches it once.

The worker fetches nothing for a person who may no longer use Calendar — disabled, locked
out, pending, deactivated by the directory, or without `calendar:access`. It asks once
per person per cycle, and puts that person's due subscriptions off by the poll, plus
jitter, leaving their last copy and their reason as they were; they are fetched again
once the person may use Calendar again.

### What Vantigo fetches from the internet

The worker, a save and **Refresh now** fetch **the addresses people subscribe to**, and
nothing else: one `GET` each, `User-Agent: Vantigo-Calendar`, sent straight from the
Vantigo container, through the same address guard as mail and the Peppol lookup:

- the host is resolved and every address checked: **no private, loopback, link-local,
  carrier-grade-NAT or cloud-metadata address** is dialled, and the connection goes to
  the address that was checked, so DNS rebinding cannot turn it inward;
- **no redirect is followed** — a 3xx is a failure the person sees;
- **at most 1 MiB** is read, and the fetch has **20 seconds**;
- there is **no outbound proxy**: an `HTTPS_PROXY` is not used, since a proxy would send
  the request to an address the guard never saw. An installation whose containers reach
  the internet only through a proxy cannot fetch subscriptions;
- **no connection is kept open** between fetches: each one dials afresh, through the
  guard;
- a save, a change and **Refresh now** share one rate limit, `calendar-subscription-fetch`,
  **30 requests per client address per 10 minutes**, answered 429 with `Retry-After` —
  far more than a person managing their own calendars clicks, and far less than a script
  turning the server into a fetcher would need. It is per client address, so behind a
  proxy `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` must name it, or everyone shares
  the proxy's one bucket ([Forwarded headers and
  HTTPS](/en/admin/authentication/#forwarded-headers-and-https)). The worker is not
  limited.

An address a person types is refused at save when its host is an address the guard
forbids, or when it carries a user name or password.

### What the log says

Each failed fetch is logged at warning with the subscription's id and the reason, and
nothing of the address beyond its host; the person sees the same reason on their row. The
reasons are one sentence each:

| Reason | Means |
| --- | --- |
| the address redirects; subscribe to the address it points at | the site answered 3xx; the person must use the address it points at |
| the calendar is larger than 1 MiB | the calendar is too big to keep; the last good copy stays |
| the calendar has more than 10,000 events | the calendar is read on every load of the person's calendar, so one this large is not kept; the last good copy stays |
| the address did not answer a calendar | a 2xx that is not an iCalendar file — a web page, usually |
| the address answered <status> (such as `404 Not Found`) | any other status: 404 for a removed calendar, 401 or 403 for one that needs a sign-in Vantigo does not do |
| the address is not one Vantigo may fetch | the host resolves to an address the guard forbids |
| the address did not answer in time | no answer within the 20 seconds a fetch has, or no connection within 10 or no headers within 15 |
| the address could not be reached | the name did not resolve, the connection was refused, or there is no route out |

Reading a stored calendar, at every load of the person's calendar, is bounded too: per
subscription per read, at most 10,000 events, 200,000 steps of work and one second, and
the 5,000 items nearest the start of the window shown kept. A calendar that reaches a bound shows what was
read by then, and the log says so at info with the subscription's id. Nothing needs
doing; the person can ask whoever publishes the calendar for a smaller one.

| Variable | Purpose | Default |
| --- | --- | --- |
| `CALENDAR_SUBSCRIPTIONS_ENABLED` | On/off switch for the `calendar-subscriptions` worker | `1` |
| `CALENDAR_SUBSCRIPTIONS_POLL` | How often each subscription is fetched; a duration of at least `5m` | `30m` |

## The mailbox sync worker

A person can have Vantigo keep a copy of their calendar in their Microsoft 365 mailbox,
on their **My calendar** screen ([Sync with Outlook](/en/user/calendar/#sync-with-outlook)),
once an Owner has turned on the **Outlook connection** switch in the Microsoft Entra ID module and granted
its permission ([Microsoft Entra ID](/en/admin/entra/#calendar-sync)). The
`calendar-mailbox-sync` worker writes the copies through the Entra module's connection
([the worker](/en/reference/calendar/#the-mailbox-sync-worker)). It runs unless
`CALENDAR_MAILBOX_SYNC_ENABLED=0`, in worker mode, and in api mode with
`WORKERS_IN_PROCESS=1`. It **wakes every minute** — not a setting — and syncs each
person who is due, at most 50 a cycle, one at a time. Each person is claimed for ten
minutes before their sync and given back after it, so several replicas share the people
due and never sync one person twice at once; a replica stopped mid-sync leaves a claim
that lapses, and the person is synced by the next cycle after it. No database connection
is held while Microsoft is asked, so the worker cannot starve a small connection pool.
Each person has three minutes for what their sync asks of Microsoft and the other
modules, so one slow mailbox cannot hold the rest. A person is due `CALENDAR_MAILBOX_SYNC_POLL` (default `15m`, at
least `1m`) after their last sync, and at once when one of their appointments changes,
when they change their setting, or when they press **Sync now** — so an appointment
reaches Outlook within a minute. A failed sync is tried again after 1, 5, 15 and 60
minutes, then hourly, and shows as failed from the fifth in a row; a mailbox the tenant
refuses is tried again in six hours; a connection that is not working, in an hour.

Before each push the worker also **takes back what people changed in Outlook**: an
edit, a move or a delete of a copy of their own appointment changes the appointment
through the same rules — and the same mails to colleagues and to people outside the
company — as a change made in Vantigo, and an event made in a separate *Vantigo*
calendar becomes an appointment ([the write-back](/en/reference/calendar/#the-write-back)).
**A copy deleted in Outlook deletes, or cancels, the appointment in Vantigo** — only
when Microsoft answers that the copy is not found, never when a read fails, and not when
the appointment was changed in Vantigo since the last sync. **More than ten such deletes
of one person's appointments in one sync are none of them taken**: the copies come
back, the person's card says so, and the worker logs a warning with the person's id and
the count. Anything else changed in a copy is put back. It writes at most 20 appointments a person per
sync, on that person's behalf. A read of the copies that fails lets the push go on and
is tried again on the failure's backoff; the person's card then says the last sync
failed.

**Change notifications.** Where Microsoft can reach `PUBLIC_BASE_URL`, a change made in
Outlook does not wait for the poll. After a sync that pushed, the worker has the Entra
module ask Microsoft to notify it of changes in that person's mailbox
([Microsoft Entra ID](/en/admin/entra/#change-notifications)), and each cycle it makes
due the people whose mailboxes were notified, so the change is taken within about a
minute. A notification is a nudge and nothing more: the worker still reads the mailbox,
and the poll runs whether or not any arrive. If asking fails, the worker logs a warning
with the error's code and the sync stands. The subscription is removed with the copies —
the person turning the sync off, losing access, being unlinked or moved to another
mailbox, or the Outlook connection switch being turned off — but kept when only the person's target
calendar changes. A removal that fails, or that cannot be tried because the connection is not
working, stays owed and is tried again on the clean-up's backoff, like a copy that could
not be deleted; one Microsoft refuses outright (permission denied) is logged and left to
lapse, within seven days, so it cannot hold the clean-up back. `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED=0`
turns it all off.

The worker removes a person's copies when they turn the sync off, when they may no
longer use Calendar — disabled, locked out, deactivated by the directory, or without
`calendar:access` — when Staff no longer links them to a Microsoft Entra ID account,
and for everyone when the **Outlook connection** switch is turned off. **Turned off, the worker writes
and removes nothing**: people's copies stay in their mailboxes as they last were, the
copies of a leaver included, until it is on again.

It reaches the network only through the Entra module's Graph client — the two fixed
Microsoft hosts, with the module's own limits — and logs a person by their user id
alone: never their mailbox, an appointment's or an event's title or Graph's words. A failed sync is
logged at warning with the step that failed; a refused mailbox at info with its code.

| Variable | Purpose | Default |
| --- | --- | --- |
| `CALENDAR_MAILBOX_SYNC_ENABLED` | On/off switch for the `calendar-mailbox-sync` worker | `1` |
| `CALENDAR_MAILBOX_SYNC_POLL` | How often each person's copies are synced when nothing made them due sooner; a duration of at least `1m` | `15m` |
| `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` | Whether Microsoft is asked to notify changes in synced mailboxes, so a change made in Outlook is taken within about a minute; needs `PUBLIC_BASE_URL`. `0` is for a public host Microsoft cannot reach | `1` |

## Key material

The answer links are sealed at rest under a key derived from `APP_SECRET` (the purpose
`calendar/rsvp-link`), so a **Resend** carries the same link as the first mail. Rotating
`APP_SECRET` leaves every stored link unopenable: a link already mailed still answers —
its token is kept as a plain hash, which needs no key — but an update or a **Resend** to
an attendee added before the rotation fails and shows **Sending failed**; take them off
the appointment and add them again for a new link
([Key material](/en/admin/authentication/#key-material)).

The calendar addresses are sealed the same way, under the purpose
`calendar/feed-address`, so a person's **My calendar** screen can show their address
again. After a rotation the screen cannot open them and shows no address, but every
address still works — a request is matched by the token's plain hash, which needs no key
— so phones go on updating. A person who needs to see their address again presses
**Create address**, which the screen shows in its place: it resets the address, the old one
stops working, and they give their programs the new one.

The busy-only address's events carry identifiers derived from `APP_SECRET` too (the
purpose `calendar/feed-busy-uid`), so they say nothing of the record behind them. A
rotation changes every one of them: a program subscribed to a busy-only address drops
the blocks that come from the other apps and adds them anew at its next refresh — once,
with nothing lost. Appointments keep their identifiers.

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
- **A phone never updates.** The client decides when it refreshes — Apple Calendar as
  set on the device, Outlook about every three hours, Google Calendar up to a day — so
  wait before suspecting Vantigo. Otherwise: the address was **Reset** or turned off,
  and the phone holds the dead one (a 404 in the proxy's log); or it is on `APP_URL`,
  which the phone cannot reach off the company's network — the screen says
  *"This address works on the company's network only"*; set up the public host, and the
  person resets the address to get one there. A 429 in the log means the proxy is not
  trusted and every phone shares one bucket.
- **Adding a subscription or Refresh now is refused with 429.** Thirty in ten minutes
  from one client address is the limit; behind a proxy that is not trusted
  (`TRUSTED_PROXY_HOPS`, `TRUSTED_PROXY_CIDRS`), everyone shares the proxy's one bucket.
- **A feed answers 503.** One of the modules whose items are in it could not be read;
  the log names it. The feed is never served with something missing.
- **A person's calendar is over 1 MiB.** The feed drops the projected items farthest from
  today, the farthest first, until it fits, logs it at info
  with the person's id, and never drops an appointment. Nothing needs doing.
- **A subscription shows an error.** Read the sentence on the row
  ([what the log says](#what-the-log-says)). "Not one Vantigo may fetch" for a public calendar
  means the host resolves to a private address from the container — split DNS; "could
  not be reached" or "did not answer in time" for every subscription means the containers have no route to the
  internet, or reach it only through a proxy, which the fetch does not use.
- **`CALENDAR_SUBSCRIPTIONS_POLL` refused at start.** It is not a duration, or is under
  five minutes.
- **A person's Outlook card cannot be turned on, or says the sync stopped.** It says why
  in a sentence; what each means for the Entra connection, and what to do, is in
  [Microsoft Entra ID](/en/admin/entra/#what-can-go-wrong).
- **Appointments do not reach Outlook.** The worker is off
  (`CALENDAR_MAILBOX_SYNC_ENABLED=0`), or no process runs workers (worker mode, or
  `WORKERS_IN_PROCESS=1`); the person's card then stays at **Waiting for the first
  sync…**.
- **`CALENDAR_MAILBOX_SYNC_POLL` refused at start.** It is not a duration, or is under a
  minute.
- **Changes made in Outlook arrive only at the poll.** Change notifications are off
  (`CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED=0`), `PUBLIC_BASE_URL` is not set, or Microsoft
  could not reach it; the Entra page says which
  ([Microsoft Entra ID](/en/admin/entra/#change-notifications)). Nothing is lost: the poll
  takes them every `CALENDAR_MAILBOX_SYNC_POLL`.
