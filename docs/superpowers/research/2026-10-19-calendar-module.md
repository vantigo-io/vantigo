# Calendar module — research before a design

Research for a per-employee calendar that gathers every time-bound thing the other
modules know about one person into one day/week/month view, lets the person schedule
appointments with colleagues and external contacts, publishes the calendar to phones
and subscribes to calendars from elsewhere. Three inputs: what the repository already
holds, what comparable products do, and what the standards and the mail clients
demand. Uncertain items are marked **UNCERTAIN**.

## 1. What the repository already holds (2026-10-19)

**There is no calendar code.** No iCal, ICS, RRULE, CalDAV or appointment code exists
in Go, TypeScript, SQL, the OpenAPI contracts or the docs. Two things carry the name
and must be kept apart:

- `apps/server/internal/calendar` was the platform's **Norwegian public-holiday
  calendar** (`PublicHolidays`, `BusinessDay`, `NextBusinessDay`), used by Invoices and
  Staff. A business module named `calendar` would have collided with it as a Go
  package and confused every reader, so it was renamed to `internal/holidays` on
  this branch (see §5).
- `GET /api/v1/staff/calendar` is Staff's **absence calendar**: people as rows, absence
  bars, public holidays, 93 days at most, audience-shaped (`staff:approve`,
  `staff:manage` and `staff:personal-data` see everyone, `staff:view` everyone with the
  type hidden, anyone else their reports and themselves). Its UI is a hand-built month
  table in `apps/staff/frontend/src/pages/absence.tsx`.

**Time-bound data per module, with the person it belongs to and the permission that
guards it.** This is the inventory of what a first aggregation could show.

| Module | Row | When | Whose | Read gate |
| --- | --- | --- | --- | --- |
| Customers | timeline entry (`note`, `interaction.call/meeting/email`, `other`) | `occurred_on` date, optional `occurred_at` instant, **no end** | `actor_user_id` (the writer) | `customers:timeline-view` (sensitive) + `customers:view` |
| Customers | follow-up on a manual entry | `follow_up_on` date, may be in the future; `follow_up_done_at` | `follow_up_assignee_user_id`, NULL = everyone's | same; `GET /customers/follow-ups?assignee=me` exists |
| Customers | the customer | — | `owner_user_id` | `customers:view` |
| Staff | absence | `first_day`..`last_day`, hour-type absences one day | `person_id`, linked to a user through `staff.identity_links` | audience rule above |
| Staff | sick-case milestone | `due_date` | the case's person, and the manager/HR who must act | HR-only |
| Staff | employment, probation and expected end | dates | person | `staff:view` and up |
| Projects | task | `start_date`, `due_date` | `assignee_user_id` | `projects:access`; `ProjectDirectory.OpenTasksForUser` exists but is **not visibility-filtered** |
| Projects | project | `start_date`, `end_date` | roles in `project_roles` (`manager`, …) | `projects:access` |
| Projects | billing milestone | `planned_date` | — | financial rights on the project |
| Time | entry | `entry_date`, optional `start_time`/`end_time` without zone, `hours` | `user_id` | `time:access` own, `time:view-all` everyone |
| Expenses | travel claim | `departure_at`..`return_at` instants | `user_id` | `expenses:access` own, `expenses:view-all` |
| Invoices | invoice | `issue_date`, `due_date` | nobody (only `created_by`/`issued_by`) | `invoices:access` |

What the inventory says: the person's own calendar has **four natural sources today**
— customers timeline entries and follow-ups, own absences, own project tasks, own time
entries — and travel claims as a fifth. Invoice due dates have no person and belong on
a company view, not a personal one. Nothing has an end time except travel; "logged
entries with a customer" are a date and an optional instant, so they render as a
point or an all-day item, never as a block.

**How modules talk to each other.** The rules in
`docs/src/content/docs/en/contributing/module-boundaries.md`: one schema per module,
no cross-schema joins, modules never import each other (depguard), synchronous reads
through interfaces in `apps/server/internal/contracts`, asynchronous work through a
transactional outbox. The one precedent for "one screen built from several modules" is
**Customer 360** (`customers/overview.go`, spec
`docs/superpowers/specs/2026-09-23-customers-360-design.md`): the consumer takes the
other modules' contracts as optional `Deps` slots, a section is **absent when its
module is off or the caller lacks the permission**, and the consumer re-applies each
provider's permission gate itself. The host frontend has the other half of the
pattern: a module's tab on another module's page is registered by the host, gated on
the enabled module and its permission, and rendered from the module's own UI package.

There is **no "contribute items to someone else's screen" contract on the server**.
`module.Module` has single-provider slots (`Projects`, `Actuals`, …) and three
many-provider slots (`CustomerReferences`, `CustomerPersonalData`, `InvoicedWork`). A
calendar that wants "any enabled module can offer items" is a new many-provider slot,
say `CalendarSources`, with one interface every provider implements:
`ItemsForUser(ctx, userID, from, to) ([]Item, error)`, each provider doing its own
visibility filtering. Staff has no contract at all today; the reference says
`contracts.StaffDirectory` waits for its first consumer, and this would be it.

**"Enabled" means per deployment, not per workspace.** Vantigo is single-tenant. The
`MODULES` environment variable is a startup allowlist; a disabled module contributes
no routes, permissions or workers, and the SPA learns the list from the runtime config
in `index.html`. "Given that it is enabled" is therefore: the calendar takes every
source contract as optional and shows what is wired, as Customer 360 does.

**People and addresses.** `contracts.UserDirectory` returns `UserEntry{ID, DisplayName,
Active}` **without an email**; only `PermissionHolders(key)` carries one, "for mailing
that holder and nothing else", and the `AccountProvisioning` rule is that the identity
address never crosses into the caller. Staff holds `work_email` on `staff.people`,
linked to the user through `staff.identity_links`. So inviting a colleague by mail
needs a deliberate new seam — either a `UserDirectory` method that mails on the
caller's behalf without handing the address over, or Staff's work address through a
Staff contract. External invitees are customers' contacts (name, email) or a typed
address.

**Time zones.** There is no per-user time zone, only `preferred_language`. The
business time zone is `staff.settings.time_zone` (default `Europe/Oslo`); Expenses has
its own; Invoices hard-codes Oslo; Customers' follow-ups compare against the server's
UTC day. A calendar is the first screen where a wrong zone is visible every day, so
the design has to pick: business zone for everyone in v1, with a per-user override
later.

**Mail.** `mail.SendOutbound` (`internal/mail/outbound.go`, go-mail) builds text and
HTML alternatives and attachments. It does not build a `text/calendar; method=REQUEST`
**alternative part**, which is what Gmail and Outlook key on for native RSVP buttons;
that is a small addition (go-mail's `AddAlternativeString`). Staff and Invoices send
from the installation's `SMTP_FROM`; Communications from each channel's own SMTP
credentials. **There is no inbound mail path anywhere** — Communications is
outbound-only by design.

**Workers.** `worker.Worker{Name, Interval, Run}` registered through
`Module.Workers`. The scheduled-refresh-of-an-external-feed shape already exists in
`customers/registry_feed_worker.go` and the advisory-lease shape in
`staff/retention_worker.go`; outbound HTTP goes through `internal/netguard` as Peppol's
`guardedTransport` does.

**Public endpoints.** `x-vantigo-access: anonymous` plus a line in
`security.PayRoutes()` for the public host. The token-link precedent is Invoices' pay
link: `token_sha256` stored, URL sealed, `revoked_at`, per-operation rate limit — and a
deliberate **POST so the token never travels in a URL**. An iCal subscription is a
GET with the token in the path by nature of the clients, so the design must argue the
exception: hashed at rest, one per user, reset from settings, busy-only variant,
rate-limited, no personal data beyond what the feed intentionally carries.

**Frontend.** React 19, Mantine 9.4, TanStack Router and Query, dayjs, `@mantine/dates`
in the catalog. No week grid or agenda component exists in `packages/frontend-shell`;
Time's `my-week.tsx` and Staff's `AbsenceCalendar` are the only calendar-like screens
and neither generalises.

## 2. What comparable products do

| Product | What lands on the calendar | Own calendar or view | External invites | Sync |
| --- | --- | --- | --- | --- |
| HubSpot | meetings, tasks | a **view over the user's connected Google/O365 calendar**; primary calendar only, first occurrence of a recurring event only | sent by the connected calendar, not HubSpot | meetings two-way-ish, tasks one-way |
| Pipedrive | typed activities (call, meeting, task, deadline, lunch…) | own activities calendar | via the user's connected calendar | one-way or two-way, types mapped |
| Odoo | its own `calendar.event`; meetings created from any record's chatter link back to it; booking pages, resources, working hours, recurrence, reminders | **true calendar** | Odoo mails iCalendar invitations and tracks accepted/declined | two-way Google and Outlook |
| Zoho CRM | meetings, calls, tasks | own | via sync | Google two-way; CalDAV only on Zoho Calendar, subscriptions read-only |
| monday / ClickUp | any item with a date column | **views**, not calendars | none | one-way or personal two-way |
| Tripletex, Fiken, Visma | no general calendar | — | — | a gap in the Norwegian SMB field |

Sources: HubSpot calendar integration and dedicated calendar view knowledge-base
pages; Pipedrive "Syncing with Google Calendar"; Odoo 17 Calendar documentation; Zoho
CRM Google Calendar integration and the Zoho Calendar CalDAV beta page; monday
"Calendar sync"; ClickUp "Google Calendar syncing".

The pattern a v1 shares across all of them: a **per-user calendar of typed items
created from a record and linking back to it**, item types distinguished by colour and
filterable, with external invites going out as mail. Two-way sync with Google or
Microsoft is the feature every vendor ships with caveats (primary calendar only,
recurrence not editable, deletions not propagated). Odoo is the closest to what is
asked here: a true event model of its own, invitations by mail with RSVP tracking,
other modules creating events that link back.

## 3. Invitations that render native RSVP buttons

RFC 5545 (iCalendar), RFC 5546 (iTIP) and RFC 6047 (iMIP). The message layout that
works in Gmail, Outlook and Apple Mail:

```
multipart/mixed
├─ multipart/alternative
│  ├─ text/plain
│  ├─ text/html                                   (the body, with a web RSVP link)
│  └─ text/calendar; method=REQUEST; charset=UTF-8 (the ICS, inline)
└─ application/ics; name="invite.ics"             (the same ICS, as an attachment)
```

- The `method=` parameter on the part's Content-Type must equal `METHOD:` in the body.
  Sending the ICS **only as a file attachment** makes Outlook show a plain mail with no
  Accept/Decline (reported against Stalwart and older Kolab); the inline
  `text/calendar` alternative is what the clients key on.
- Required in the VEVENT: a stable globally unique `UID` (`<id>@<installation
  host>`), `DTSTAMP`, `SEQUENCE` (0 on first send, incremented on every update),
  `DTSTART`/`DTEND` in UTC or with `TZID` and a `VTIMEZONE`, `SUMMARY`, `ORGANIZER;CN=…:
  mailto:…`, and one `ATTENDEE;CN=…;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:
  mailto:…` per invitee, every attendee listed on every copy. CRLF and 75-octet folding.
- Gmail renders Yes/Maybe/No only when the recipient's address is an `ATTENDEE` with
  `RSVP=TRUE`; a forwarded invite degrades to "Add to calendar". Since 2022 Google's
  default "add invitations only from known senders" keeps an unknown sender's invite
  off the calendar until the person clicks a button in the mail, so SPF, DKIM and
  DMARC alignment on the sending domain is a practical prerequisite.
- Outlook silently drops an invite whose `ORGANIZER` equals the recipient, and an
  `ORGANIZER` without `mailto:` fails import. `X-MICROSOFT-CDO-INTENDEDSTATUS:BUSY` is
  optional, not required for recognition.
- **Organizer versus From.** Not a hard rule: Cal.com sends from a notifications
  address with the host's real address as `ORGANIZER` and gets Gmail's buttons. Cronofy,
  who run this at scale, document that some clients refuse invites where the sender
  differs from the organizer, and solve it by having the customer forward the organizer
  mailbox to them. Safest is From = Organizer = an address the installation controls.
- Updates: resend `METHOD:REQUEST` with the same `UID`, `SEQUENCE+1`, a new `DTSTAMP`;
  clients ignore a lower or equal sequence. Cancellation: `METHOD:CANCEL`,
  `STATUS:CANCELLED`, `SEQUENCE+1`.
- Replies: Google and Microsoft mail a `METHOD:REPLY` message ("Accepted: …") **to the
  ORGANIZER's mailto address**, carrying the `UID`, the replier's `ATTENDEE;PARTSTAT=
  ACCEPTED|DECLINED|TENTATIVE` and the `SEQUENCE`.

**Receiving replies without an inbound mailbox** is the design's hardest decision,
because Vantigo has none:

- (a) **An inbound route.** `ORGANIZER` is an address the installation controls
  (`calendar+<token>@<domain>`) routed to a webhook — Cloudflare Email Routing to a
  Worker, Postmark inbound, SES receipt rule — which parses the `text/calendar;
  method=REPLY` part, matches on `UID` (and `RECURRENCE-ID`), applies only to the
  `ATTENDEE` matching the sender, ignores stale sequences. This is Cronofy's model.
  It is infrastructure every dedicated deployment would have to set up, against the
  roadmap's stated preference for fewer infrastructure pieces per deployment.
- (b) **A web RSVP link** in the HTML body, one signed token per attendee, answered by
  a public endpoint. Works in every client, needs no mailbox, and is what Calendly's
  "email confirmations" mode and Cal.com effectively rely on (neither parses
  `METHOD:REPLY`; both lean on the host's connected calendar instead).

Recommendation: (b) in v1, with the ICS written so (a) can be added as an optional
operator-configured feature later. The native buttons will still appear and will add
the event to the invitee's calendar; what the buttons' reply does is land in the
organizer's mailbox, where a person can see it, until (a) exists. The invite body says
so plainly ("Answer with the link below so the organizer sees it in Vantigo").

## 4. Publishing to phones, and subscribing from elsewhere

**Read-only iCal feed, not CalDAV, in v1.** CalDAV needs principal discovery,
PROPFIND/REPORT, `calendar-query`, RFC 6578 sync tokens, per-device auth and write
semantics; `emersion/go-webdav` gives a skeleton and nothing else. A read-only feed
over HTTPS and `webcal://` covers "see my Vantigo calendar on my phone".

- Refresh is the client's: Google Calendar "From URL" is documented as up to 12 hours
  and observed at 24–48; Outlook.com/M365 about every 3 hours; Apple Calendar from 5
  minutes to weekly, chosen by the user; Thunderbird about 30 minutes. Emit
  `REFRESH-INTERVAL;VALUE=DURATION:PT1H` (RFC 7986) and `X-PUBLISHED-TTL:PT1H` for the
  clients that honour them, `ETag` and `Last-Modified`, and answer `If-None-Match` with
  304. The settings page must say "your phone decides how often it refreshes".
- Token URL: `/calendar/feed/<token>.ics`, token hashed at rest, a "reset private
  address" action that invalidates the old URL, a **busy-only** variant beside the full
  one. Zoho, Remember The Milk and RT all do exactly this.
- Keep the feed small (Google's import cap is 1 MB): window it (−3/+12 months), emit
  recurring events as `RRULE` rather than expanded, include a `VTIMEZONE` per `TZID`
  used (Outlook desktop needs it) or emit UTC.

**Subscribing to external iCal URLs.** Poll every 15–60 minutes with jitter, honour the
feed's `REFRESH-INTERVAL` as a minimum, send `If-None-Match`/`If-Modified-Since`, cap
body size and redirects, fetch through `netguard`, store the raw ICS and expand on
read over a bounded window. Resolve `TZID` through the embedded `VTIMEZONE` to IANA
(Outlook feeds carry Windows names). Let the person mark a subscription "busy only",
treat subscribed content as read-only, and never republish it in the person's own
Vantigo feed by default (otherwise two subscribers loop each other's calendars).

**Go libraries.** `github.com/arran4/golang-ical` for parse and serialise (typed
RRULE/RDATE/EXDATE/RECURRENCE-ID, `SetMethod`, organizer and attendee helpers; the
de-facto choice, Debian-packaged, author seeking a co-maintainer) with
`github.com/teambition/rrule-go` for expansion (a python-dateutil port). `emersion/go-ical`
is cleaner but has no recurrence and no releases; worth it only with go-webdav's
CalDAV later. Build the iMIP MIME with `mime/multipart` and golden-file test it.

**React.** FullCalendar v7: MIT core, new theme system with Tailwind and shadcn themes,
React 17–19, day/week/month/list, drag-to-create, resize, all-day row, time zones,
`nb` locale, accessibility; Premium only for timeline and resource views. Schedule-X
moved drag-and-drop and resize to a paid tier in v4; react-big-calendar is MIT but
override-heavy to style and slowly maintained. **UNCERTAIN:** how FullCalendar's theme
sits inside Mantine; the alternative is a copy-in grid the design system owns fully.

## 5. What the first version should hold, and what should wait

The four features asked for are the right four. What the inventory and the
comparison add:

**Name.** The module should be `calendar` — it is what every user will call it, in
both languages — so the platform's public-holiday package gave the name up first.
Renaming `apps/server/internal/calendar` to **`internal/holidays`** was mechanical:
five exported functions (`PublicHoliday`, `EasterSunday`, `BusinessDay`,
`NextBusinessDay`, `PublicHolidays`), three importing files
(`invoices/reminderrules/months.go`, `staff/calendar_view.go`, `staff/workdays.go`)
plus two tests, and the pages that name the path: `contributing/platform-packages.md`
(also in its `sources` frontmatter), `contributing/module-boundaries.md`,
`reference/staff.md`, `reference/invoices.md` and `ROADMAP.md`. Dated specs, plans
and research keep the old name as history. It is a pure refactor, `Docs-Impact`
limited to the paths, done on this branch before anything else. The business-day helper
of §6 step 1 then joins it, which is where it belongs: the package becomes "which
days the company works, and which day an instant falls on".

**The dashboard.** `/dashboard` today is KPI cards, a metric chart, an attention list
merged on the client from nine per-module `/stats/attention` calls, recent activity
and setup prompts — a manager's page. The calendar is what makes it every
employee's: a **"Today and this week" agenda** at the top, served by the calendar
module's own range endpoint (already aggregated server-side, one call), registered
by the host and rendered from `@vantigo/calendar-ui` the way the customer page's
tabs are, shown when the module is on and the caller holds `calendar:access`, with
the day's items in order, the next days collapsed, a link into the full view and a
"new appointment" action. The attention list stays for what is not dated (a failed
mail, a stuck import); the dated items it carries today — follow-ups due, tasks due —
are the same items the agenda shows, and the design should say whether they stay in
both places. Docs: `user/getting-started.md` and the dashboard passages in both
languages.

**The item model.** The module's own table holds **appointments** (what the employee
schedules); everything else is **projected** from the sources at read time through the
`CalendarSources` contract, never copied. The projection is what every later module
plugs into, and it is what keeps a follow-up ticked in Customers ticked on the
calendar with no synchronisation. Every projected item carries a link back to its
record, a source kind for colour and filtering, and comes through the provider's own
permission gate.

**v1, beyond the four asked for:**

- All-day and multi-day items (absences, follow-ups, tasks are all dates, not
  instants) and the agenda/list view, which is what a phone-width screen shows.
- Per-source colour and a filter per source that remembers its state.
- Basic recurrence on appointments (daily, weekly, monthly, until/count), stored as an
  `RRULE` with exceptions, because the feed and the invites need it in that shape
  anyway and a weekly customer call is the first thing someone will schedule.
- Private versus shared appointment (`CLASS`) and busy versus free (`TRANSP`), so a
  colleague's calendar can show busy blocks without the title.
- A **colleague's calendar** read as busy/free — the same audience rule Staff uses —
  because scheduling with an internal person is otherwise blind.
- The business time zone for everyone, with the per-user override designed in but not
  necessarily built.
- Reminders in v1 only as the client's `VALARM` on the feed and in the invite; in-app
  or mail reminders can wait.
- The ICS feed in two variants (full, busy-only), a reset action, and an explicit
  sentence about refresh.

**Later:** two-way Google/Microsoft sync (OAuth, webhooks, the conflict rules every
vendor hedges on), booking pages, room and resource booking, attachments on
appointments, team calendars and delegation, CalDAV, the inbound `METHOD:REPLY` route,
in-app and mail reminders, a per-user time zone, dragging a projected item (moving a
task's due date from the calendar needs a write contract back into Projects).

**Decisions a design must take before a plan:**

1. The contract shape: a many-provider `CalendarSources` slot and whether providers
   filter visibility themselves (recommended, matching Customer 360).
2. How a colleague is mailed without the identity address crossing the seam.
3. RSVP by web link in v1, the inbound route as an optional later feature.
4. The two time zones (§6): the installation's business zone made one platform
   setting, and a personal zone per user that follows it unless chosen.
5. The feed token's exception to the "no token in a URL" rule, and its mitigations.
6. Where the public routes join `PayRoutes` (the holiday package is already
   renamed, so `calendar` is free).
7. The dashboard agenda: what it shows, and what the attention list keeps.

## 6. Time zones: two concepts, one plan

The calendar is the first screen on which a wrong zone is visible every day, and the
repository has four different answers to "which day is it" today. They are not one
problem but two, and the plan keeps them apart.

**The business time zone** is the calendar every *bookkeeping* day is judged by: the
day an invoice is issued and falls due, the period lock, the per-diem day, Staff's
"today" and the a-melding's dates, the holiday calendar, the payroll file. It is a
property of the company, never of the person looking: an accountant working from
Spain still issues invoices on the Oslo day. It must be **one setting per
installation**, and today it is three:

| Where | What it holds | Who reads it |
| --- | --- | --- |
| `staff.settings.time_zone` | `Europe/Oslo` by default, validated in Go and Postgres, `GET /staff/meta` answers it and `today` | Staff only |
| `expenses.settings.time_zone` | the same, introduced first (migration `00013`) for the per-diem day | Expenses only, also passed into SQL as `AT TIME ZONE` |
| `invoices/values.go` `oslo` | hard-coded, "Norwegian bookkeeping, so there is no setting" (D1) | Invoices |
| `projects/queries/invoiced.sql` | `AT TIME ZONE 'Europe/Oslo'` literal, twice | the invoiced-work query |
| Customers `civilDate`, Time `today`, Projects `stats.go` | **UTC** calendar day | follow-up overdue/due today, time entry dates, dashboard stats |
| Energy | the price area's **market zone** (`Europe/Stockholm`, …) | consumption buckets — correct as it is, a different concept, left alone |

The two settings can already disagree with each other and with Invoices; nothing
checks. A Norwegian installation never notices because every answer is Oslo or two
hours off Oslo at night. The calendar would notice: a follow-up due "today" in UTC
beside an appointment at 23:30 Oslo on the same evening lands on different days.

**The personal time zone** is what an instant looks like to one person, and when "09:00"
means for an appointment they create. It belongs on the user, beside
`preferred_language` in `identity.users`, and is edited under Personal account
settings. The default is the business zone, as asked — but as **`NULL` meaning "follow
the business zone"**, resolved at read time, rather than a copy taken at creation:
a copy goes stale if the business zone ever changes, and a user created by SCIM or by
Staff's provisioning has nobody to choose for them. `GET /me` answers the effective
zone and whether it is inherited. SCIM's core user schema has a `timezone` attribute,
so an IdP can set it; optional. **UNCERTAIN:** whether other modules need a colleague's
zone through `UserDirectory` — the calendar needs the viewer's (from the session) and,
for an invite mail's text body, the invitee's; the ICS itself carries UTC or a `TZID`
and the client renders it, so the contract method can wait for that mail.

The frontend formats instants with `Intl` in the **browser's** zone today, and
date-only values deliberately in UTC (`format-date-only.ts`, which is right and stays).
Customers' timeline even prints "· 10:00 UTC" after an entry's time. With a personal
zone the shell's formatters take the zone from `/me`, and that suffix goes.

**Appointments and recurrence** need a third thing stored: the `TZID` the appointment
was created in. "Every Monday 09:00" must stay 09:00 Oslo across the clock change, so
the `RRULE` expands in the appointment's zone and the stored UTC instants are derived,
never the source. A personal zone changes how the person *sees* an appointment, not
what was agreed.

### The plan, in order

Each step is its own small change with its own documentation, and the first is the
prerequisite for the calendar.

1. **One business time zone on the platform.** A setting identity's workspace
   administration owns (identity is always on and already holds the installation's
   other settings), reached by every module as a contract beside `Clock` —
   `contracts.BusinessCalendar` with `Zone()`, `Today()` and `Day(instant)` — and a
   `businessDay(t, loc)` helper moved into the platform package `internal/calendar`,
   where the public holidays already are and where Expenses' and Invoices' two private
   copies of the same function consolidate. The migration seeds it from Staff's value
   (the broader definition), noting in the log if Expenses' disagrees. Staff's and
   Expenses' own columns go, their settings pages show the platform value with a link,
   `GET /staff/meta` keeps answering zone and today. Docs: `admin/index.md`,
   `admin/staff.md`, `reference/staff.md`, `reference/expenses.md`, the user pages for
   Staff and Expenses in both languages.
2. **Invoices** drops `oslo` for the contract. D1's reason stands — bookkeeping has one
   calendar — and the calendar is now the installation's rather than Norway's, which
   for every Norwegian installation is the same day. Docs: `reference/invoices.md`.
3. **Projects** passes the zone into `invoiced.sql` as a parameter, the way Expenses'
   claims query already does, and the dashboard's `today` becomes the business day.
   Docs: `reference/projects.md`.
4. **Time** computes `today` on the business day, and documents `start_time`/`end_time`
   as **wall-clock in the business zone** — the time of day at work, which the calendar
   projects as such. Docs: `reference/time.md`.
5. **Customers** replaces `civilDate`'s UTC with the business day for `occurredOn`'s
   validation and the follow-up's overdue/due-today split, and the timeline renders an
   entry's time in the viewer's zone. The reference's "00:30 on Tuesday is still Monday
   for two hours" paragraph is rewritten. Existing rows are untouched. Docs:
   `reference/customers.md`, the Customers user page in both languages.
6. **The personal zone**: the nullable column on `identity.users`, the setting under
   Personal account settings, `/me` answering the effective zone, the shell's
   formatters taking it, and the SCIM `timezone` mapping if wanted. Docs:
   `admin/authentication.md`, `user/getting-started.md` or wherever personal settings
   are described, in both languages.
7. The calendar module builds on 1 and 6.

Steps 2 to 5 are independent of each other and of 6; each is a day's work with its
tests. The only user-visible shift is a day boundary moving from UTC midnight to the
business zone's, which is the correction.
