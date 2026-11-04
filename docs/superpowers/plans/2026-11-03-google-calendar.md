# Google Workspace, delivery E — calendar sync on the person's Google connection: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person with a Google account in Staff and a connected Google connection turns
on **Sync with Google Calendar** in the calendar's settings. Their appointments and the
items they chose appear in their Google Calendar (or a *Vantigo* calendar), their busy and
out-of-office time in Google shows in Vantigo as hatched blocks, edits to the copies in
Google come back under the calendar's write-back rules, and a change in Google makes the
sync due at once through a push channel when `PUBLIC_BASE_URL` is set. The calendar module
does not change: it already picks the provider by the person's account (delivery 0).

**Architecture:** `google` fills `contracts.MailboxCalendar` under `Key() = "google"`,
Entra's `mailbox*.go` reshaped for Calendar v3: `srv/google/mailbox.go` (the status, the
footing of a call — the status, the account, the person's token with the `calendar`
feature —, the calendars, `Apply`, `Find`, the error mapping), `mailbox_batch.go` (Google's
`multipart/mixed` batch endpoint, 50 requests a batch), `mailbox_delta.go` (`Delta` with a
sync token and the `g1.` link, `Marked`), `mailbox_writeback.go` (`Versions`, `Events`),
`mailbox_notifications.go` (`Watch`, `Unwatch`, `Notified`, `Connections`) and
`mailbox_notifications_endpoint.go` (the anonymous `POST /google/calendar-notifications`).
Every call runs on D's `personalTokens.source(gid, "calendar", removal)`; `quotaUser` is
the person's Google id on every Calendar call. The rows are `google.calendars`,
`google.calendar_channels` and `google.connection.marker` (00117).

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G30–G32 (on G14, G25–G28), P1–P5; the calendar Outlook sync design's O4, O8–O11; the Outlook design's M9.
**Brief:** [`2026-11-03-google-workspace-briefs/e-calendar.md`](2026-11-03-google-workspace-briefs/e-calendar.md).

## Plan readings

Checked against `0bae1d8c` (`main`, deliveries 0–D merged).

1. **The migration is `00117`.** `main` holds 00114 (baseline), 00115 (accounts) and
   00116 (connections) for Google after main's own 00109–00113; the design's and the
   brief's `00115` is stale. `srv/google/sqlc.yaml` and `srv/db/schema_test.go` follow.
2. **The slot is many-provider already** (delivery 0): `module.Module.MailboxCalendar`
   builds the module's, Compose and Workers collect every enabled module's into
   `Deps.MailboxCalendars`, refusing a duplicate key; the calendar picks
   `mailboxCalendarFor(acct.Provider)` and keeps notices and connections per provider.
   Nothing in the calendar changes.
3. **D's token source** is `personalTokens.source(gid, feature, removal)`
   (`personal_token.go`): `not_connected` without a row, the reconnect's code in
   `reconnect`, `closing` for a closing row unless a removal (or no token at all), the
   account checked on use, `consent_missing` when the feature's scopes are not granted —
   none of these moves the row except the account check and a refused refresh. The
   `calendar` feature is `calendar.events` + `calendar.app.created`. `connectionChanges`
   (`personal_token.go`) is `Connections` already; `ListPersonalChanges` reads
   `ix_google_connections_changed`.
4. **The guarded client** (`client.go`) sends JSON and decodes JSON; the batch endpoint
   answers `multipart/mixed`. `do` gains a raw variant answering the 2xx's headers and
   bytes, so the batch is built and read in `mailbox_batch.go` under the same host list,
   caps, retries and words rule. The Calendar base is `https://www.googleapis.com/calendar/v3`
   and the batch `https://www.googleapis.com/batch/calendar/v3`, both on
   `www.googleapis.com`, already on the fixed list.
5. **The sweep** (`personal_sweep.go`, `DeleteSpentPersonal`) deletes a closing row seven
   days after it closed, or a day after its last use. G28 adds "and holding no calendar
   channel" to the second; the seven days stand (G28's "in any case").
6. **Entra's webhook** (`entra/mailbox_notifications_endpoint.go`) is the template for the
   anonymous endpoint, its rate limit (600 a minute, its own bucket) and its allowlisting
   on the public host (`srv/security/publichost.go`, `PayRoutes`, with
   `publichost_test.go`'s admitted and refused lists).
7. **The calendar's consumer** reads `MailboxChange.ShowAs` (`busy` and `out_of_office`
   hatched, `free` dropped), `Series` (never a copy) and `Removed`; the write-back compares
   `Apply`'s `ChangeKey` with `Versions`' and reads `Events` whole. Google's `etag` moves
   with every write to an event and is the same string in `events.insert`'s, `patch`'s,
   `list`'s and `get`'s answers: it is the change key.
8. **The calendar worker's tests** use `modtest.FakeMailboxCalendar`. A test through the
   calendar's own worker with Google's real provider must compose both modules, which
   only `internal/integration` may (depguard). It composes `calendar` and `google` with a
   fake `Workforce`, the fake Google as the transport and the module rows seeded.

## Decisions taken in planning

- **The footing of a call** is Entra's: the status (anything but a removal needs
  `available`; `DeleteCalendar`, `Find`, `Unwatch` and an all-delete `Apply` run under
  `cleanup` too), then the mailbox — a Directory id that is a trusted, undeleted
  `google.accounts` row (`ErrMailboxUnknown` otherwise) —, then the person's token for
  the `calendar` feature, asked for as a removal for the removals. A removal whose
  connection is closing with no token, or gone, is `ErrMailboxGone` (as Entra's
  `mailboxSource`).
- **`Status`** (G30): `available` while Connection for people is on and the web client is
  ready (D's `personalOffered`); `cleanup` (`switch_off`) while the switch is off, the web
  client is set and a closing row still holds a token; else `unavailable` with
  `switch_off` or the web client's reason (`no_web_client`, `not_tested`,
  `client_refused`). No network.
- **The marker** is `google.connection.marker`, 32 hex digits the migration writes once
  (`replace(gen_random_uuid()::text, '-', '')`). A copy carries
  `extendedProperties.private = {"vantigo": <marker>, "vantigoKey": <key>}`; `Find` and
  the default calendar's `Versions` ask `privateExtendedProperty=vantigo=<marker>`; an
  event whose `vantigo` is another value is `Foreign`.
- **The calendars.** Google allows two calendars of one name and the `calendar.app.created`
  scope reaches only the calendars this client made, so a calendar is the module's when
  `google.calendars` holds `(google_user_id, calendar_id)` — that row is the marker of a
  calendar. `EnsureCalendar`: `known` kept when its row and `calendars.get` both answer;
  else another row of the mailbox whose calendar still answers is adopted; else
  `calendars.insert {summary: name, timeZone}` and the row written. `DeleteCalendar` of a
  calendar no row names is `ErrCalendarNotOurs`; `calendars.delete`, 404 or 410 done, the
  row dropped. `calendars.insert` refused 403 is `ErrMailboxDenied` (U8: the *own* target
  unavailable).
- **The event written** (`Apply`): `summary`, `description`, `location` (an update always
  writes it, so a dropped location clears), a timed event's `start.dateTime`/`end.dateTime`
  in RFC 3339 with `timeZone` the event's IANA zone or `UTC`, an all-day one's
  `start.date`/`end.date` (the end the day after the last), `transparency` `transparent`
  for `free` and `opaque` otherwise, `visibility` `private` or `default`, `reminders
  {useDefault: false}`, and the marker on a create and on an update naming a key. Google
  has no tentative or out-of-office for an event a client writes as a plain event:
  `tentative`, `out_of_office` and `working_elsewhere` are written `opaque` (busy).
- **`Apply`'s batch**: creates (`POST …/events`), updates (`PATCH …/events/{id}`) and
  deletes (`DELETE …/events/{id}`) in batches of 50, one after another; each part's answer
  judged alone — 2xx done (a create's `id`, the `etag`), 404 or 410 on an update or a
  delete `Gone` (an update answered `status: cancelled` too), 403 `forbidden` or
  `insufficientPermissions` `ErrMailboxDenied`, 429 and 403 `rateLimitExceeded` or
  `userRateLimitExceeded` sent again once after a second, then `errThrottled`. Once one op
  is refused for the mailbox, the rest answer the same, unsent. A batch holding a create is
  sent once (no 5xx retry).
- **The reads' fields** are asked with `fields=`: the delta and the versions never ask
  `summary`, `description`, `location` or an attendee's address; `attendees(self,responseStatus)`
  is the most of attendees any call asks, so a meeting is a count and the person's own
  answer.
- **`ShowAs`** (G30): `transparency=transparent` → `free`; `eventType=outOfOffice` →
  `out_of_office`; `eventType=workingLocation` → `working_elsewhere` when opaque (free
  otherwise, by the first rule); the person's own `responseStatus=declined` → `free`;
  `tentative` → `tentative`; otherwise `busy`.
- **`Delta`** (G31): `link ""` lists `primary` with `singleEvents=true`, `showDeleted=true`,
  `timeMin`, `timeMax`, `maxResults=250`, paged; the last page's `nextSyncToken` is the
  link to keep. A continuing link lists with `syncToken` (and `singleEvents`,
  `showDeleted`). At most 40 pages a call; a cut answer's link carries `p`, the page
  token, and whether the listing was the windowed one. The link is
  `g1.<base64url(json{m, f, t, s, p, w})>`; a link whose `m` is not the mailbox, or that is
  not `g1.`, is refused with a failure, unsent. 410 → `ErrMailboxDeltaExpired`. An event
  outside `from..to` (increments are not windowed, U7), cancelled or deleted is `Removed`.
  A windowed list answering no `nextSyncToken` keeps a link without `s`, and its next
  continuation answers `ErrMailboxDeltaExpired` at once — logged once per mailbox in the
  process. `Series` is an event with `recurringEventId`.
- **`Marked`, `Events`**: `events.get` per id in batches of 50; `Marked` answers the ids
  carrying the marker (404, 410 and a cancelled event are not answered); `Events` answers
  404, 410 and a cancelled event in `Gone`, a 403 of one event skipped, and reads
  `TextBody` as a description carrying no HTML tag (Google's web editor writes HTML).
- **`Versions`**: the default calendar is `Find`'s list with `etag`; a secondary calendar
  is `events.list(timeMin, timeMax, singleEvents=true, fields=items(id,etag,recurringEventId))`;
  at most 5000, `Complete` false past it.
- **Ids**: an event id is 1–1024 of `[A-Za-z0-9_-]` (base32hex, and an instance's
  `_<time>Z`); a calendar id 1–255 of `[A-Za-z0-9_.@+-]`, path-escaped. An id outside is
  refused before any call, and a delta's event with one is left out, counted.
- **Errors** (G30): 401 after D's one fresh token → the row to `reconnect`
  (`sign_in_again`) through D's revision-guarded `reconnect`, `ConnectionNeededError`;
  403 `forbidden`/`insufficientPermissions` → `ErrMailboxDenied`; 404 on a calendar-level
  call → `ErrMailboxUnknown`; a removal with no token → `ErrMailboxGone`; 429 and the
  rate-limit 403s retried once, then a failure. Google's words are never logged: a
  refusal is its status and reason.
- **`quotaUser`** = the Google id on every Calendar URL, the batch's and each part's.
- **Notifications** (G32): `Watch` opens `events.watch` on `primary` (`id` a fresh UUID,
  `token` 32 random bytes in base64url — its SHA-256 alone stored —, `address`
  `PUBLIC_BASE_URL/api/v1/google/calendar-notifications`, `params.ttl` 30 days, the most
  Google is asked for), recorded in `google.calendar_channels` keyed by the Google id with
  `expires_at` Google answered and `granted_seconds`. Nothing while a channel at the
  present address has more than min(3 days, half its granted life) left; else a new one,
  then the old stopped (a refused stop logged and forgotten: the channel lapses, its
  notifications name no row). No `PUBLIC_BASE_URL`, or
  `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` off → nil. `Unwatch`: no row nil;
  `channels.stop {id, resourceId}` on the removal token, 2xx, 404 and no token forget the
  row; anything else keeps it and answers the error. `Notified`: rows notified after
  `since`. `Connections`: `connectionChanges`.
- **The endpoint**: `POST /api/v1/google/calendar-notifications`, `anonymous`, header
  parameters only and no request body read; `X-Goog-Channel-ID` a UUID naming a row,
  `X-Goog-Channel-Token` 43 base64url characters whose SHA-256 equals the row's (constant
  time), `X-Goog-Resource-ID` the row's; `exists` and `not_exists` set `notified_at`,
  `sync` and anything else nothing. **204 always**, a database failure logged by count
  alone. Rate limited 600 a minute per address (`google-calendar-notifications`).
  Allowlisted on the public host beside Entra's.
- **The sweep**: `DeleteSpentPersonal` deletes a closing row after seven days, or after a
  day unused **and** holding no `google.calendar_channels` row.

## Tasks

- [ ] **1. Plan** — this file, `docs(google): the plan for calendar sync (Google E)`.
- [ ] **2. The tables and the contract** — `feat(google): the calendar's tables and the notifications endpoint's contract (Google E)`.
  `00117_google_calendar.sql`, `queries/calendar.sql`, sqlc, `schema_test`; the sweep's
  channel rule; `openapi/google.yaml` (the endpoint); `go generate`; the frontend schema
  (`bun run gen:client`).
- [ ] **3. The provider** — `feat(google): calendar sync through the person's Google connection (Google E)`.
  The client's raw call; `mailbox.go`, `mailbox_batch.go`, `mailbox_delta.go`,
  `mailbox_writeback.go`; `MailboxCalendar` on the module; `googletest/calendar.go`
  (calendars insert/get/delete, events insert/patch/delete/get/list with sync tokens,
  pages, 410, `privateExtendedProperty`, the batch endpoint, `events.watch`,
  `channels.stop`); tests per call.
- [ ] **4. Notifications** — `feat(google): Google Calendar change notifications (Google E)`.
  `mailbox_notifications.go`, the endpoint, its rate limit, `publichost.go`; tests: a
  channel opened and renewed, the forged token and the unknown channel ignored, `sync`
  ignored, a notification marking the row, `Unwatch` forgetting.
- [ ] **5. Through the calendar's worker** — `test(integration): Google's calendar sync through the calendar's own worker (Google E)`.
  A push, a busy read, a write-back taken and one put back, the delta restarted on 410,
  the fallback without a token, a notification making the sync due, a disconnect
  cleaning up, a leaver's copies left.
- [ ] **6. Documentation** — `docs(google): calendar sync with Google Calendar (Google E)`:
  `user/calendar.md`, `admin/calendar.md`, `admin/google-workspace.md`,
  `admin/installation.md` in `en` and `nb`; `reference/calendar.md`, `reference/google.md`.
- [ ] **7. Embedded docs** — `go generate ./...`, `chore(assistant): regenerate the embedded documentation`.
- [ ] **8. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`,
  `bun run tools/docs/check-coverage.ts --base origin/main --head HEAD`, serially.

## Unverified items this delivery depends on

- **U5** (a channel's lifetime): renewed at min(3 days, half the granted life); the poll
  runs regardless.
- **U7** (a windowed first list answers a sync token; increments are unwindowed): the
  provider filters the window itself; with no token every sync re-reads the window.
- **U8** (`calendar.app.created` makes and uses a secondary calendar): a refusal is
  `ErrMailboxDenied`, the *own* target unavailable; *default* is unaffected.
- **U18** (push needs HTTPS only, no domain verification): without notifications the poll
  carries the sync.

All four are checked in the test tenant before the merge (P5); `R/google.md` lists them.
