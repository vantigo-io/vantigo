# Calendar feed and subscriptions — design (phase 1, delivery C)

Delivery **C** of the calendar's phase 1, decided in outline by
[`2026-10-19-calendar-design.md`](2026-10-19-calendar-design.md) D9 and detailed here:
a **calendar address** per person that a phone or another calendar program subscribes
to — in two variants, the full calendar and a busy-only one — behind a token that is
hashed at rest and reset from a settings screen; and **subscriptions** the other way,
calendars from elsewhere polled by a worker and shown beside the person's own, never
republished in their feed. The research
([`../research/2026-10-19-calendar-module.md`](../research/2026-10-19-calendar-module.md)
§4) chose a read-only iCalendar feed over CalDAV for v1, and set the rules this
delivery keeps: a token URL, a windowed feed with series as `RRULE`, one `VTIMEZONE` per
zone, `ETag` and 304, polling through `netguard` with a size cap and no redirects, and
`TZID` resolution for the Windows names Outlook feeds carry.

A and B built what C writes from: `ical_uid` and `sequence` on every appointment, an
occurrence named by its day, and B's `ics.go` — the `VEVENT` writer, the `RRULE`,
`EXDATE` and `RECURRENCE-ID` forms, and the `VTIMEZONE` walked from Go's tzdata — which
the feed extends and does not duplicate. What C must decide: the token and its
mitigations, what each variant carries and what neither may, how the feed reads the
providers without a request, the subscription's bounds and the parse rules, and the
settings screen.

## Decisions

### D1 — Two addresses per person, the token hashed, a sealed copy for the screen

Migration `00057_calendar_feed.sql` adds two tables to the `calendar` schema:

```sql
CREATE TABLE calendar.feed_tokens (
    user_id            uuid        NOT NULL,
    variant            varchar(4)  NOT NULL,  -- 'full' | 'busy'
    token_sha256       char(64)    NOT NULL,  -- the token's hex SHA-256; the token itself is never stored
    address_ciphertext text        NOT NULL,  -- the whole address, sealed under calendar/feed-address
    created_at         timestamptz NOT NULL,
    last_used_at       timestamptz,           -- the last request that answered 200 or 304
    PRIMARY KEY (user_id, variant)
);
CREATE UNIQUE INDEX ux_feed_tokens_hash ON calendar.feed_tokens (token_sha256);

CREATE TABLE calendar.subscriptions (
    id             bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    user_id        uuid          NOT NULL,
    name           varchar(100)  NOT NULL,
    url            varchar(2048) NOT NULL,   -- as typed, webcal(s) rewritten to https
    colour         varchar(10)   NOT NULL,   -- one of the palette's names (D9)
    busy_only      boolean       NOT NULL,
    etag           varchar(500),             -- the feed's last ETag, sent back as If-None-Match
    last_modified  varchar(100),             -- the feed's last Last-Modified, sent back as If-Modified-Since
    body           text,                     -- the last calendar fetched, at most 1 MiB; kept through a failed attempt
    fetched_at     timestamptz,              -- the last attempt that answered a calendar or a 304
    attempted_at   timestamptz,              -- the last attempt, whatever it answered
    next_fetch_at  timestamptz,              -- when the worker fetches next; null is now
    error          varchar(500),             -- why the last attempt failed; null after one that did not
    created_at     timestamptz   NOT NULL,
    updated_at     timestamptz   NOT NULL
);
CREATE INDEX ix_subscriptions_user ON calendar.subscriptions (user_id);
CREATE INDEX ix_subscriptions_due  ON calendar.subscriptions (next_fetch_at);
```

A person has at most one address per **variant**: `full`, their calendar as they see it,
and `busy`, its spans and nothing else (D3). The token is the pay link's and the RSVP
link's: 32 random bytes, `base64.RawURLEncoding`, 43 characters, stored as its hex
SHA-256 and found by that hash. The design said the address is shown once; B's precedent
is better and is followed instead: the whole address is also kept **sealed** under the
new secrets purpose `calendar/feed-address`, so the settings screen can show it again —
a person sets up their phone today and their laptop next month, and a secret they must
reset to see is one they paste into a note. The hash is still what a request is matched
by; the sealed copy is opened only for the owner's own screen. `POST /feed/{variant}`
creates the address, or **resets** it when one exists — a new token, the old address
dead from that moment — and answers the address; `GET /feed` answers both variants'
addresses, with `createdAt` and `lastUsedAt`, for the ones that exist; `DELETE
/feed/{variant}` turns one off. All three are `calendar:access`'s and act on the
caller's own rows only.

The address is `<base>/api/v1/calendar/feed/<token>.ics`, where `<base>` is
`PUBLIC_BASE_URL` when set and `APP_URL` with `APP_BASE_PATH` otherwise — B's rule for
the answer link: a phone off the company's network needs the public host, a laptop on it
does not, and the screen says which it has. The screen also offers the same address
with `webcal://` in place of `https://`, which Apple's and Outlook's programs subscribe
to with one click; the server serves `https` only.

### D2 — `GET /feed/{token}.ics`: the module's one anonymous `GET`, and why it may be

Every other anonymous operation in Vantigo is a `POST` with its token in the body, so
the token never sits in an API address, a proxy log or a browser history. A calendar
client allows nothing else: Google, Apple, Outlook and Thunderbird take one URL and
`GET` it, with no header and no body of ours. This is the argued exception the research
named, and it is held to its mitigations:

- **The token is hashed at rest** and found by its hash; a database read answers no
  address.
- **It is per person, per variant, and resettable** from the screen in one click; a
  reset kills the old address at once (the row's hash changes), and nothing but the
  calendar is behind it.
- **It is rate limited per client address**: `calendar-feed`, 120 requests per 10
  minutes — a phone asks hourly, Google a few times a day, and an office of phones
  behind one NAT is dozens of requests an hour; a token guessed at is 43 characters of
  256 bits, and the limit makes the guess slower still.
- **A token nobody holds is the bare 404**, with no body and no timing that differs from
  a held one's beyond the database read.
- **The feed carries nothing the person's own calendar does not** (D3), and the busy
  variant nothing but spans, so the worst a leaked full address gives is a read of that
  one calendar until the person resets it, and a leaked busy address the shape of their
  days.
- **The path is logged, the query is not**, as for every request, and the token is the
  path's last segment — so the operator's page says what B's does: keep the proxy's
  access log to the path on the public host, or to nothing.

The path on the wire is `/api/v1/calendar/feed/{file}`, `{file}` the token with the
`.ics` suffix as **one segment**: the platform router matches a parameter to a whole
segment and nothing else, so `{token}.ics` cannot be a pattern, and the handler takes
the segment and requires the suffix (404 without it). The route joins
`security.PayRoutes()` as `GET /api/v1/calendar/feed/` with `OneSegment`, the pay
attempts' shape, and `TestPublicHostGate` admits it and refuses it with a segment too
many, under `POST`, and without a segment. No page accompanies it: the address is pasted
into a calendar program, not opened in a browser. The operation's access rule is
`anonymous`; a signed-in caller is answered alike, by the token and not the session. A
request that answers 200 or 304 writes `last_used_at`.

### D3 — What the two feeds carry, and what neither may

The window is **today −3 months to today +12 months**, today in the business zone
(`contracts.BusinessCalendar`), the edges judged as `GET /items` judges them (calendar
design D5) with the business zone as the viewer's; a series is written as a rule, so it
is not the window that bounds a series but the window that decides which series are
in. Items are the person's own as `GET /items` builds them — the appointments they own
or are on, the enabled providers' items for them under each provider's gate (D5) — and
**never** a colleague's, a cancelled appointment's, a subscription's (D8) or the public
holidays' (every phone has a holiday calendar; a second copy is noise).

| In the feed | `full` | `busy` |
| --- | --- | --- |
| An appointment the person owns, or is on with any answer but `declined` | its `VEVENT`: title, place, description, class, transparency, link, the series' rule and exceptions | a `VEVENT` of its time alone, when `transparency` is `busy`; a `free` one is left out |
| An appointment the person declined | left out: they said no, and the phone need not carry it | left out |
| A cancelled appointment (B's D6) | left out | left out |
| A provider's **timed** item (a time entry with hours, a trip) | a `VEVENT` with the title, the subtitle as description, the module in `CATEGORIES` and the link; a done one with `✓ ` before the title, since no client draws a strike | a `VEVENT` of its time alone |
| A provider's **all-day** item (a follow-up, a task due, an absence day) | the same, `VALUE=DATE` | left out: a day with a task on it is not a day the person is busy |
| A subscription's item (D8) | never | never |
| A public holiday | never | never |

The busy variant's `VEVENT` is `UID`, `DTSTAMP`, `DTSTART`, `DTEND`, `SUMMARY` of the one
word *Busy* (*Opptatt* when the person's preferred language is `nb`, read through the
user directory), `TRANSP:OPAQUE`, and for a series its `RRULE`, `EXDATE`s and overriding
`VEVENT`s with the same `UID` — the time of a moved occurrence is a span too. No title,
no place, no description, no category, no link, no class: nothing a reader could tell
one block from another by, which is the point of a busy feed a receptionist or a
partner's calendar subscribes to.

Neither variant writes `ORGANIZER` or `ATTENDEE`: a phone's copy is read-only, and an
address in a file on a phone is an address the module need not put there — the
appointment's sheet is where the owner reads who is on it. The link (`URL`) is the app's
own page, as a colleague's notice carries it (B's D4): `APP_URL/calendar?date=…&
appointment=…` for an appointment, `APP_URL` with the provider's `path` for a projected
item.

**Size.** The research's cap is Google's import limit, 1 MB. Fifteen months of one
person's calendar is hundreds of `VEVENT`s, well under it; a heavy Time user's entries
are the one thing that could reach it. The feed is therefore built with the projected
items ordered by their distance from today, nearest first, and when the serialised
calendar exceeds **1 MiB** the farthest quarter of them is dropped and it is serialised
again, until it fits; the appointments are never dropped, and a drop is logged at info
with the person's id. The feed is not paged and has no other limit.

### D4 — The iCalendar object: B's writer extended, an hour's refresh, `ETag` and 304

`feed.go` builds the `VCALENDAR` with golang-ical through the writers of `ics.go` —
`setWhen`, `icsRule`, `originalStart`, `vtimezone`, `zoneSpan`, `safeCN` — which are
generalised where C needs them (an `icsInput` without a method, attendees, an organiser
or a link; a `describe` that writes `CATEGORIES` and `URL` for a projected item) and
never copied. The calendar carries `PRODID` (B's), `VERSION:2.0`, no `METHOD` (a
published calendar has none; a `METHOD` makes Outlook treat the file as an invitation),
`NAME` and `X-WR-CALNAME` of *Vantigo* (*Vantigo (busy)* / *Vantigo (opptatt)* for the
busy variant), `REFRESH-INTERVAL;VALUE=DURATION:PT1H` and `X-PUBLISHED-TTL:PT1H` — the two
spellings the clients that honour an interval read, Apple's the second — and **one
`VTIMEZONE` per `TZID` used**, each over the span of the series written in it, before
the first `VEVENT`. A single timed appointment is UTC instants, as B writes it; a timed
series is wall-clock with its zone's `TZID`, for B's reason; an all-day one is dates.
`DTSTAMP` is the request's time on every `VEVENT`. `SEQUENCE` is the row's; a projected
item's is 0 and its `UID` `<kind>:<id>@vantigo`, stable across requests so a client
updates rather than duplicates.

The response is `text/calendar; charset=utf-8`, `Content-Disposition: inline;
filename="vantigo.ics"`, `Cache-Control: private, no-cache` (a client may keep it, and
must ask again), and an **`ETag`**: the quoted hex SHA-256 of the serialised calendar
with every `DTSTAMP` line removed, so an unchanged calendar has the same tag from one
hour to the next while `DTSTAMP` says when it was written. `If-None-Match` naming that
tag (or `*`) is answered **304** with the same `ETag` and no body, after the token was
found and `last_used_at` written. No `Last-Modified`: a provider's items have no one
time they changed at, and a tag that is right is better than a date that is a guess.

### D5 — The feed reads the providers for a person who made no request

A provider's gate is `contracts.HasPermission(ctx, Access, key)`, which reads the
request from the context and asks `Access.Check` about its session; without a request
it answers false, so a feed that handed the providers a bare context would get nothing
from them, and one that skipped their gates — a flag on `CalendarQuery` the providers
honoured — would show a person their tasks on their phone after `projects:access` was
taken from them. The gate must stay the provider's, and identity must still be the one
to answer it. So the contracts gain one seam, in two parts:

```go
// Access
// UserHolds reports whether userID — active, not locked out — holds every key:
// an owner holds all, anyone else each through an effective role. It is for a
// read made on a user's behalf where there is no request to check: the
// calendar feed. Any failure is an error, never a false.
UserHolds(ctx context.Context, userID uuid.UUID, keys ...string) (bool, error)

// AsUser is the context of a read on one user's behalf with no request behind
// it: a Principal of that id alone — no session, no roles — and the mark
// HasPermission reads. A module uses it for a background read whose caller
// is a token it has already matched to the user, never to widen a request.
func AsUser(ctx context.Context, userID uuid.UUID) context.Context
```

`HasPermissions` keeps its first rule — a request in the context is checked through
`Check`, as today — and gains a second: no request but the `AsUser` mark, and the keys
are asked of `UserHolds` for that id; anything else is false, as before. Identity
implements `UserHolds` over the same tables `permitted` reads — the user's `is_disabled`
and `lockout_end`, the owner role, `EffectivePermissionForUser` per key — and the
MFA-for-owners rule `businessSatisfied` enforces on a session is not applied: it is a
rule about a session, and there is none. The feed handler, once the token has named
the user, builds `AsUser(ctx, userID)` and asks `sourceItemsIn` with it, through the
same accessor and under the same budget `GET /items` uses; `sourceItems`' check that the
principal is the user the query names holds as it did. The module's test fakes
(`fakeSource`) gate through `HasPermission` as the real providers do, so the feed's test
proves the gate is honoured: a provider's kind is in the feed with the key held and
absent with it revoked, with no change to the provider.

`contracts.Access`'s other implementations — the router's and the server's test fakes —
gain the method with a canned answer.

### D6 — Subscriptions: a person's own, at most ten, and the address rules

`GET /subscriptions`, `POST /subscriptions`, `PUT /subscriptions/{id}`, `DELETE
/subscriptions/{id}` and `POST /subscriptions/{id}/refresh`, all `calendar:access`'s,
all on the caller's own rows — another person's is the 404 an unknown id gets. On the
wire:

```
CalendarSubscription {
  id, name, url, colour, busyOnly,
  fetchedAt?, attemptedAt?, error?,   // the worker's record (D7)
  createdAt, updatedAt
}
CalendarSubscriptionWrite { name, url, colour, busyOnly }
```

The rules at write, each a field error: `name` 1 to 100 characters; `url` at most 2048,
absolute, with a scheme of `https`, `http`, `webcal` or `webcals` (the last two
rewritten to `https` before it is stored, since they are the same thing in a program's
address bar), a host that is not empty, not an IP literal `netguard.Disallowed` refuses,
and no user-info — the password in a `https://user:pass@host/` address would be written
to the row and the log; `colour` one of the palette's names (D9); `busyOnly` a boolean.
The **eleventh** subscription is 409 `subscription_limit`: ten calendars beside one's
own is more than anyone reads, and each is a fetch every half hour. There is no
revision and no `If-Match`: the row is the person's own, small, and last-write-wins is
what they expect of it.

A `POST` **fetches once inline**, under a budget of twenty seconds, before it answers,
so a wrong address is seen at once and a right one shows its items at the next load
rather than after the worker's next cycle; the row is created whatever the fetch
answers, its `error` set when it failed, and the response is 201 with the row as it
stands. A `PUT` that changes the `url` clears `etag`, `last_modified`, `body` and the
timestamps and fetches inline too; one that changes the rest fetches nothing.
`POST /refresh` fetches now under the same budget and answers 200 with the row — the
screen's **Refresh now**. `DELETE` removes the row; its items are gone from the next
read.

### D7 — The `calendar-subscriptions` worker: every half hour with jitter, through the guard

`CALENDAR_SUBSCRIPTIONS_ENABLED` (default on) hands the worker to the runner;
`CALENDAR_SUBSCRIPTIONS_POLL` (default `30m`, at least `5m`) is how often each
subscription is fetched. The worker **wakes every five minutes** — a constant, not a
knob — takes its advisory lease (`0x43414C5355425331`, "CALSUBS1") on one pooled
connection as the cancellations worker does, and fetches every subscription whose
`next_fetch_at` is null or past, oldest first, at most 200 a cycle, one at a time. After
each attempt it sets `next_fetch_at = now + max(poll, the feed's REFRESH-INTERVAL, capped
at 24 hours) + jitter`, the jitter uniform over `[0, poll/10)` — so two installations
subscribed to the same calendar drift apart, and a feed that asks to be read daily is
— and the lag a five-minute tick adds is the most a subscription is late. An attempt
that fails sets `error` and `attempted_at` and leaves `body`, `fetched_at` and the
validators as they were, so the calendar keeps showing the last good read and the
screen says both when it last worked and why it does not now; the next attempt is the
next due, with no backoff — a feed down for an hour costs two requests.

**The fetch** is one `GET` through an `http.Client` of the module's own: a transport
whose dialler is `netguard.DialContext` over the system resolver — the host is resolved,
every address checked against the one table of forbidden ranges, and the connection
made to the address that was checked — with `Proxy` nil (the Peppol lookup's reasoning:
a proxy would send the request to an address the guard never saw), and
`CheckRedirect` answering `http.ErrUseLastResponse`: **no redirect is followed**, since
the next address is one the guard and the person never saw, and a 3xx is the failure
*"the address redirects; subscribe to the address it points at"*. The request carries
`User-Agent: Vantigo-Calendar`, `Accept: text/calendar`, and `If-None-Match` and
`If-Modified-Since` from the row when it has them. The budget is twenty seconds, the
response's headers are capped at 64 KiB, and the body is read through a limit of **1
MiB plus one byte**: a longer body is the failure *"the calendar is larger than 1 MiB"*
and is not kept. A 304 writes `fetched_at` alone. A 2xx whose body does not begin, after
blank lines, with `BEGIN:VCALENDAR`, or that golang-ical cannot parse, is *"the address
did not answer a calendar"*; any other status is *"the address answered <status>"*; a
refused dial is *"the address is not one Vantigo may fetch"*; a timeout *"the address
did not answer in time"*. The error is the sentence, never the response's body or the
resolved address. The log carries the subscription's id and the error at warning, and
nothing of the URL beyond its host.

### D8 — Parsed on read: what a subscribed calendar contributes, and what it never does

`GET /items` reads the caller's subscriptions' bodies after the providers' items and
before the holidays, parses each with golang-ical, and answers every `VEVENT` that
overlaps the window as a `CalendarItem` of **kind `calendar.subscription`**, module
`calendar`, with `subscription: { id, name, colour, busyOnly }` beside it so the app
draws it in the subscription's colour and labels it. The item's `id` is
`<subscription id>:<UID>`, with `:<start>` after it for an occurrence of a rule
(`20261103T080000Z`, or the date for an all-day one). `title` is `SUMMARY`; `subtitle`
is the subscription's name; `path` is absent (`URL` in the event is not followed — it is
the other calendar's, and a click on one of these opens nothing); `done` is false. A
**busy-only** subscription answers each item with an empty `title`, which the app
labels *Busy* as it does a colleague's private appointment, and no description reaches
the wire in either case. A colleague's calendar (calendar design D6) asks no
subscription, as it asks no provider. `GET /meta`'s `sources` lists the kind when the
caller has at least one subscription, so the legend has its chip.

The parse rules, each with a test:

- **Time.** `DTSTART` with `VALUE=DATE` is an all-day item; `DTEND` (exclusive) or
  `DURATION` gives its last day, and neither gives one day. A `DTSTART` ending in `Z` is
  an instant; one with a `TZID` is wall-clock in that zone (below); one with neither is
  *floating* and read in the viewer's zone of the request. `DTEND` or `DURATION` gives
  the end; neither gives a point (`endsAt` = `startsAt`, drawn at the default length).
  An end before its start is a point.
- **`TZID` resolution**, in order: Go's tzdata by that name; the `VTIMEZONE` of that
  `TZID` carrying `X-LIC-LOCATION` (libical's and Google's hint) naming a zone Go knows;
  the CLDR `windowsZones` table (the territory-001 entries, about 140, in
  `windowszones.go`) for a Windows name such as `W. Europe Standard Time`; the
  `VTIMEZONE`'s `STANDARD` component's `TZOFFSETTO` as a fixed offset, right in winter
  and an hour off in summer, which is still better than a day off; and UTC when there
  is nothing — each fallback counted in the log at debug once per subscription per read.
- **Recurrence.** `RRULE` is expanded with rrule-go over the window widened by a year
  back (a series' occurrences are no longer than its longest event), `DTSTART` dressed as
  floating in the resolved zone and each occurrence put back into it, the calendar
  module's own way (A's D4); `EXDATE`s remove occurrences; a `VEVENT` with
  `RECURRENCE-ID` replaces the occurrence at that instant of the master with the same
  `UID`, wherever it moved to; `RDATE` is not read (Outlook and Google do not write it);
  a rule rrule-go refuses, or without a `DTSTART`, makes the event a single one at its
  start. At most **1,000** occurrences are taken from one rule and **5,000** items from
  one subscription in one read, nearest the window's start first, and the rest are
  dropped with a log line.
- **Status.** `STATUS:CANCELLED` is left out. `TRANSP` is not read: a subscription is
  not a colleague, and its items are drawn as what they are. `CLASS` is not read either:
  the person subscribed to it, so they may see it.
- **Text.** `SUMMARY` is unescaped (`\,`, `\;`, `\n`, `\\`) and cut to 200 characters,
  one line; a `VEVENT` without one is titled *(untitled)* in the person's language — the
  busy-only rule above overrides both.
- **Bounds.** A body that fails to parse is the error of D7 at the next fetch and
  contributes nothing now; a body over 1 MiB cannot be stored (D7).

**Never in the person's own feed.** The feed (D3) is built from the appointments and
the providers, and the subscriptions are neither; nothing is ever reconciled into
the module's tables from a subscription. This is the research's loop rule — two people
subscribed to each other's feeds would otherwise copy each other's calendars back and
forth for ever — and it also means a subscription's body never leaves the installation.

### D9 — The app: **My calendar** under `/calendar/settings`, and one more kind

- **The settings page**, `apps/calendar/frontend/src/pages/settings.tsx`, mounted by
  the host at `/calendar/settings` beside `/expenses/settings` and listed in the
  Calendar app's navigation as **My calendar** (*Min kalender*) under `calendar:access`.
  Its shape is the profile page's: a header, then cards.
  - **Your calendar on your phone.** Two cards, **Full calendar** and **Busy only**, each
    saying what it carries in one sentence; for a variant without an address, a
    **Create address** button; for one with, the address in a read-only field with
    **Copy**, a second **Copy as webcal** for the `webcal://` form, *Created 3 Nov 2026 ·
    last read 2 hours ago* (or *never read*), a **Reset** that asks first ("Your phone
    will stop updating until you give it the new address") and a **Turn off** that asks
    too. One sentence under both, from the research: *"Your phone decides how often it
    refreshes: Apple Calendar as you set it, Outlook about every three hours, Google
    Calendar up to a day."* And, when the address is on `APP_URL` rather than a public
    host (the server says which in `GET /feed`'s `public: false`), *"This address works
    on the company's network only."*
  - **Calendars from elsewhere.** A table of the subscriptions — a swatch in its colour,
    the name, the address shortened to its host, busy only or not, *Updated 10 minutes
    ago*, and the error in red when there is one — with **Refresh now**, **Edit** and
    **Remove** per row and **Add calendar** above it; the form is name, address (with
    the hint *"The address a calendar program gives you to subscribe to — it usually ends
    in .ics"*), colour (a swatch picker over the palette) and **Show as busy only**. A
    refused address shows the server's sentence on the field; an `error` after a save
    shows it under the row with the row kept, since the save succeeded.
- **The palette**: the twelve Mantine hues `blue, cyan, teal, green, lime, yellow,
  orange, red, pink, grape, violet, indigo`, each a `--vantigo-calendar-<name>` variable
  in `styles/fullcalendar.css` beside the module colours A defined (which keep their
  names and values), so a subscription's items and the legend's swatches follow the
  colour scheme as the modules' do.
- **The calendar page**: the legend gains the chip **Subscriptions** when `/meta` lists
  the kind, in grey with a swatch of its own, hiding every subscription's items at
  once; an item of the kind is drawn in its subscription's colour (`item.subscription.
  colour`), labelled *Busy* when `busyOnly` and its title is blank, carries the
  subscription's name as its subtitle in the list view and the tooltip, is never
  draggable, and opens nothing when clicked. `AgendaWidget` lists them as it lists a
  provider's items, without a link.
- **Strings** in `i18n.ts`, en and nb; the host's `navigation` catalog gains
  `navigation.myCalendar`.

### D10 — Config, docs and the roadmap

- `config`: `CalendarSubscriptionsEnabled` (`CALENDAR_SUBSCRIPTIONS_ENABLED`, default
  on) and `CalendarSubscriptionsPoll` (`CALENDAR_SUBSCRIPTIONS_POLL`, default `30m`, at
  least `5m`), loaded beside the cancellations worker's; `vantigo.env.example` gains the
  two under the calendar heading.
- `secrets`: the purpose `calendar/feed-address` in `platform-packages.md`'s table.
- `en/reference/calendar.md`: the two tables under the schema; **The feed** (the
  address, the token and its mitigations, the two variants' table, the window and the
  size rule, the object and its headers, the `ETag` rule, the `AsUser` read); **Subscriptions**
  (the rows and the rules at write, the worker and its fetch, the parse rules, the
  kind, never in the feed); the endpoints table; `reference/identity.md` or wherever
  `contracts.Access` is described, for `UserHolds`.
- `{en,nb}/user/calendar.md`: **Your calendar on your phone** (the two addresses, how to
  add one on an iPhone, in Google Calendar and in Outlook, what "busy only" is for,
  reset), **Calendars from elsewhere** (add, colour, busy only, refresh, what is not
  shown), the **My calendar** screen; the *What comes next* passage rewritten.
- `{en,nb}/admin/calendar.md`: the feed on the public host (the route, the rate limit,
  the proxy's log), the subscriptions worker and its two variables, what Vantigo fetches
  from the internet and through what guard, what can go wrong; `{en,nb}/admin/
  authentication.md`'s environment table; `admin/index.md`'s bullet if it lists the
  workers.
- `en/contributing/module-boundaries.md`: the public-route rule's example gains the
  feed's `GET`; the sources-contract paragraph gains `AsUser` ("a provider's gate is
  asked for a user with no request behind them only through `contracts.AsUser`, and
  only by a module that has matched a token to that user"); `platform-packages.md` for
  the secrets purpose and, if netguard is described there, the calendar as its third
  client.
- `ROADMAP.md`: C built, with the decisions taken while building it; the *Later* line
  unchanged.

## Out of scope

CalDAV; writing back from a phone (the feed is read-only by nature); `VALARM` in the
feed (the client's own default reminder applies; a reminder of ours is the roadmap's);
a feed per colleague or for a team; a feed of the public holidays; `RDATE` in a
subscription; a subscription's attachments, attendees or alarms; following a redirect;
a proxy for outbound fetches; a subscription shared with colleagues or subscribed on a
customer's behalf; showing a subscription's items to a colleague (D6's rule); a
subscription's `URL` as a link; reconciling a subscription into appointments; a
`Last-Modified` header on the feed; an address that is not reset in one click (a
scheduled rotation); more than ten subscriptions; a feed larger than 1 MiB.

## Testing

**Backend** (`internal/calendar`, through the harness; `feed_test.go`,
`subscriptions_test.go`, `subscriptions_worker_test.go`, `subscriptions_parse_test.go`,
a golden `testdata/feed/*.ics` with `-update`; `contracttest.RequireCoverage` on every
operation): the migration applies, rolls back and re-applies with the two tables and
the two indexes pinned; `POST /feed/full` answers an address under `APP_URL` (and under
`PUBLIC_BASE_URL` with `public: true` when set) whose token is not in the database and
whose hash is; `GET /feed` answers it again, with `lastUsedAt` after a read; a second
`POST` resets it and the old address is 404; `DELETE` turns it off and the address is
404; the feed `GET` is 404 for a token nobody holds, for one without `.ics`, and 429 on
the 121st request from one address; the full feed of a person with a single timed
appointment, an all-day one, a weekly Oslo series with a cancelled and a moved
occurrence, a declined appointment, a cancelled one, a free one, a timed and an all-day
provider item and a done one matches the golden byte for byte — `NAME`,
`REFRESH-INTERVAL`, `X-PUBLISHED-TTL`, one `VTIMEZONE`, no `METHOD`, no `ORGANIZER`, no
`ATTENDEE`, the declined and the cancelled absent, `CATEGORIES` and `URL` on the
provider's items, `✓ ` on the done one; the busy feed of the same person matches its
golden — spans alone, *Busy*, the free one and the all-day provider items absent, and
*Opptatt* for a person whose language is `nb`; a provider's kind is in the feed with its
gate's key held and absent with it revoked, the provider itself unchanged; a provider
that fails makes the feed 503; a subscription's items are never in the feed; the `ETag`
is the same across two requests a minute apart and `If-None-Match` is 304 with it, and
differs after a title change; an appointment of a colleague's is never in the feed; a
feed over 1 MiB of provider items is served under it with the farthest dropped (a fake
provider answering thousands). **Subscriptions**: a `POST` with an `https` address that
an `httptest` server (through a transport the harness injects, since `netguard` refuses
loopback) answers fetches once and answers 201 with `fetchedAt` and no `error`, and the
calendar's next `GET /items` carries the items with the kind, the colour and the name;
`webcal://` is stored as `https://`; `http://10.0.0.1/x`, `https://user:pw@host/`, a
relative address, an `ftp` scheme, a 101-character name and a colour off the palette
are each a field error; the eleventh is 409 `subscription_limit`; another person's row
is 404 on every operation; a server answering 301 is the redirect error and nothing is
followed; one answering a 2 MiB body is the size error and `body` is untouched; one
answering HTML is the not-a-calendar error; one answering 304 to the stored `ETag`
writes `fetchedAt` alone; a `PUT` with a new `url` clears the validators and the body
and fetches; **the worker** fetches what is due and not what is not, under the lease,
sets `next_fetch_at` past the poll and under the poll plus a tenth, honours a feed's
`REFRESH-INTERVAL` of a day, keeps the body through a failed attempt and records the
error, and skips the cycle when another holds the lease; `netguard` refuses a host that
resolves to a private address (the resolver injected) with the dial error sentence.
**Parsing**: a date event, a UTC event, a `TZID=Europe/Oslo` event across the clock
change, a floating event in the viewer's zone, a `DURATION`, a `W. Europe Standard
Time` event resolved to Berlin, an unknown `TZID` with an `X-LIC-LOCATION`, an unknown
one with a `VTIMEZONE` `STANDARD` offset, a weekly `RRULE` with `EXDATE` and a
`RECURRENCE-ID` move, `STATUS:CANCELLED` left out, an escaped `SUMMARY`, no `SUMMARY`, a
busy-only subscription answering blank titles, and the 1,000-occurrence cap. **Identity**:
`UserHolds` is true for an owner, true for a user whose role holds the key, false for
one whose role does not, false for a disabled and a locked-out user, and `HasPermission`
under `AsUser` follows it while a bare context is still false. **Security**:
`TestPublicHostGate` admits `GET /api/v1/calendar/feed/x.ics` and refuses it under
`POST`, with a second segment and with none. **Config**: the two variables load with
their defaults and refuse `4m`.

**Frontend** (`apps/calendar/frontend`, vitest): the settings page shows **Create
address** for a missing variant and the address with Copy, Reset and Turn off for one
present, posts the reset after confirming, shows the network-only sentence when
`public` is false; the subscriptions table lists rows with the error in red, the add
form posts the write and shows a field error from the server on the address, Refresh now
posts and re-renders the row; the calendar page draws a subscription's item in its
colour with the name as subtitle, *Busy* for a busy-only one, and the legend's chip
hides them; the host's route and nav item exist under `calendar:access`
(`apps.test.ts`).

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
