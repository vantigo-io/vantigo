# Calendar ↔ Outlook (Microsoft 365) — research

Date: 2026-10-27. For the calendar module's phase 2: putting what Vantigo knows about a
person into their Outlook calendar, reading Outlook's busy time back, and letting an edit
made in Outlook — on a phone — change the appointment in Vantigo. Read with
[the calendar research](2026-10-19-calendar-module.md) (§4 chose the read-only feed for
v1 and put "two-way sync with Google and Microsoft" under *Later*) and
[the Entra research](2026-10-19-entra-module.md).

## 1. What exists today

- **The feed** ([calendar feed design](../specs/2026-10-20-calendar-feed-design.md)): a
  person's calendar address, full or busy-only, which Outlook subscribes to under *Add
  calendar → Subscribe from web*. Read-only; Outlook refreshes it about every three
  hours; it lands as a separate subscribed calendar, so it **does not count as busy** in
  Outlook's free/busy (the Scheduling Assistant, *Find a time*), and each person pastes
  the address themselves.
- **The Entra module**: an app registration in the customer's tenant with client
  secrets, an app-only token cache, the guarded Graph client (`internal/entra/graph.go`,
  no proxy, no redirect, capped bodies, Retry-After honoured) and its fake
  (`internal/entra/graphtest`). It holds an account per Staff person it provisions or
  adopts (`entra.accounts`, keyed by the object id), and Staff holds, per person, the
  identity link (the Vantigo user) and the provider link (the account's object id).

The certain bridge from a Vantigo user to a mailbox is therefore Staff's: the person
whose identity link names the user, and whose provider link names the account Entra
manages. Identity's `oidc_links` hold the token's `sub`, which Graph never answers, and a
SCIM `externalId` is whatever the tenant's attribute mapping sends (Entra's default is
`mailNickname`), so neither is certain. Matching by address is guesswork, which the owner
ruled out.

## 2. Three connection models

| | App-only through the Entra connection | Per-person delegated OAuth | The feed, made easier |
| --- | --- | --- | --- |
| Consent | An admin, once, for the tenant | Each person (often blocked by tenant policy, so an admin anyway) | None |
| Tokens held | The module's client secret (exists) | A sealed refresh token per person, rolling, revocable | None |
| Latency | A worker: seconds for an appointment, minutes for the rest | Same | ~3 h |
| Counts as busy | Yes, in the default calendar | Yes | No |
| Reach | Every mailbox the permission allows — narrowed by Exchange RBAC for Applications | The person's own | — |

**The owner chose app-only through the Entra module** (2026-10-27): the installations
that would want this already connect Entra; one consent, one switch, and a person only
flips a toggle.

## 3. Microsoft Graph facts the design rests on

Read on learn.microsoft.com on 2026-10-27.

- **Permission.** `Calendars.ReadWrite` (application) creates, reads, updates and deletes
  events in every calendar of every mailbox, and creates and deletes secondary calendars
  (`POST /users/{id}/calendars`, `DELETE /users/{id}/calendars/{id}`). It also covers
  `calendarView/delta`, `getSchedule` and the extended-property filter, for which
  `Calendars.Read` is the least privilege. `Calendars.ReadBasic.All` reads without body.
- **Scoping: RBAC for Applications in Exchange Online.** `New-ManagementScope
  -RecipientRestrictionFilter "MemberOfGroup -eq '<group DN>'"` and
  `New-ManagementRoleAssignment -App <service principal> -Role "Application
  Calendars.ReadWrite" -CustomResourceScope <scope>`; `Test-ServicePrincipalAuthorization`
  checks a mailbox. Microsoft 365 groups, mail-enabled security groups and distribution
  lists may scope; nested members do not count. **The grants are a union with Entra's**:
  the tenant-wide `Calendars.ReadWrite` consent in Entra must be *removed* for the scope
  to restrict anything. Changes take 30 minutes to 2 hours. Application access policies
  are what this replaces. An out-of-scope mailbox answers 403 (`ErrorAccessDenied`,
  observed, not documented).
- **An event with no attendees sends nothing.** Invitations are sent "when you create an
  event that includes attendees". Fields: `subject`, `body {contentType, content}`,
  `start`/`end` as `{dateTime, timeZone}`, `isAllDay` (start and end at midnight in one
  zone), `location.displayName`, `showAs` (`free`, `tentative`, `busy`, `oof`,
  `workingElsewhere`), `sensitivity` (`private`), `isReminderOn`, `transactionId`
  (create idempotency, never changeable), `categories` (must name a category the user
  has — not usable blind).
- **Our marker.** A single-value extended property `String {GUID} Name <name>` written
  on create, filterable: `$filter=singleValueExtendedProperties/Any(ep: ep/id eq '…' and
  startswith(ep/value, '…'))` on `/users/{id}/events`, and read back with
  `$expand=singleValueExtendedProperties($filter=id eq '…')`. The filter on a secondary
  calendar's `/events` is not documented — a secondary calendar is ours whole, so it
  needs none.
- **Free/busy and secondary calendars.** Not documented; Exchange computes free/busy
  from the default Calendar folder, and a secondary calendar does not block time. To be
  confirmed in a test tenant; the design offers both targets and says which blocks time.
- **Batching and throttling.** `$batch` takes at most 20 requests, each judged and
  answered on its own (a 429 per request); the Outlook limits are per app and mailbox:
  10 000 requests per 10 minutes, 4 concurrent, 150 MB uploaded per 5 minutes; one
  mailbox's throttling does not touch another's.
- **Delta (for delivery B and C).** v1.0 has only `GET
  /users/{id}/calendarView/delta?startDateTime=&endDateTime=` — the window fixed in the
  token, `@odata.nextLink` then `@odata.deltaLink`, `@removed` for deletions, occurrences
  and exceptions returned expanded, no `$select`. A non-default calendar's delta is not in
  the v1.0 reference; tracking several calendars means one delta each.
- **Change notifications (delivery D).** Resource `/users/{id}/events`; at most 10 080
  minutes (under seven days) for an Outlook subscription; the `validationToken` handshake
  answered as `text/plain` within 10 seconds; `clientState` on every notification;
  answer 202 within 3 seconds; lifecycle notifications `reauthorizationRequired`,
  `subscriptionRemoved`, `missed` (the last means: run delta). A public HTTPS address is
  required.
- **Concurrency.** `changeKey` and `@odata.etag` change with every edit; that `PATCH`
  honours `If-Match` is not documented — the write-back (C) compares `changeKey`s itself
  rather than relying on a 412.

### Delta, as checked for B (2026-10-27)

Read on learn.microsoft.com ([event-delta](https://learn.microsoft.com/en-us/graph/api/event-delta?view=graph-rest-1.0),
[delta-query-events](https://learn.microsoft.com/en-us/graph/delta-query-events),
[delta-query-overview](https://learn.microsoft.com/en-us/graph/delta-query-overview)).

- **Calendars.** v1.0 lists only `/users/{id}/calendarView/delta`, the primary calendar;
  `/calendars/{id}/calendarView/delta` is shown in beta only. B reads the default
  calendar, which is the one free/busy is computed from.
- **The window** is encoded in the tokens; a moving window means a fresh delta.
- **Token lifetime** for Outlook is not fixed — it depends on a cache — and an expired
  token answers a 4xx with a code such as `syncStateNotFound`; the overview documents
  `410 Gone` with a `Location` for a reset. The remedy is a fresh delta.
- **Page size**: `Prefer: odata.maxpagesize=N` is accepted; no default or maximum is
  documented; pages vary and may be empty with a `nextLink`.
- **Recurrence**: single instances, occurrences and exceptions — no series master.
- **`@removed`**: `{"id": …, "@removed": {"reason": "deleted"}}`, id only; events moved
  outside the range are answered so.
- **Properties**: those of `GET /calendarView` — subject, body and attendees included;
  `$select`, `$expand`, `$filter`, `$orderby` are refused, so **no extended property**.
  `start`/`end` in UTC unless `Prefer: outlook.timezone`; seven fractional digits, no
  offset. `type`, `showAs`, `isAllDay`, `isCancelled`, `sensitivity` are present.
- **`transactionId`** is returned only when the app set it; A's copies never did.
- **Permission**: application `Calendars.Read` is the least privilege, `Calendars.ReadWrite`
  covers it; RBAC for Applications has `Application Calendars.Read`.
- **Throttling**: the Outlook limits per app and mailbox (10 000 requests in 10 minutes,
  4 concurrent); nothing specific to delta.
- **Not documented, for the real tenant**: a cancelled meeting's presence and `showAs`;
  an all-day event's `start` under UTC; the 403 body under RBAC; whether an event's id
  in a calendar view equals its id in `/events` (relied on for single instances).

## 4. What other products do

HubSpot, Pipedrive and Odoo (calendar research §2) push their own meetings into the
connected calendar and read busy time back; every one documents caveats — primary
calendar only, the first occurrence of a series, a field set that round-trips. The
recurring lesson: **write copies without attendees** (or the provider mails its own
invitations beside the product's), **tag what you wrote** and touch nothing else, and
make the product the authority on conflict.

## 5. Recommendations

1. App-only through the Entra module, behind an Owner-visible switch on the Entra page
   that adds `Calendars.ReadWrite` to what the connection test asks for; document RBAC
   for Applications as the way to narrow it, and report a 403 per person.
2. The mailbox from Staff's provider link and nothing else; Entra writes only to an
   account it manages and has confirmed.
3. One-way first (A), Outlook's busy time read back second (B), the write-back third
   (C), webhooks as an accelerator for reachable installations last (D). Delta polling
   works everywhere, behind any firewall.
4. Push occurrences, not series: the recurrence rules do not round-trip, and a moved
   occurrence in Outlook then maps one-to-one onto the calendar's own exception.
5. No reminders on what Vantigo writes, no categories, `private` for a private
   appointment, `oof` for an absence.
