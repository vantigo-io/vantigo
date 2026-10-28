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

### The write-back, as checked for C (2026-10-28)

Read on learn.microsoft.com ([event-update](https://learn.microsoft.com/en-us/graph/api/event-update?view=graph-rest-1.0),
[event-get](https://learn.microsoft.com/en-us/graph/api/event-get?view=graph-rest-1.0),
[event resource](https://learn.microsoft.com/en-us/graph/api/resources/event?view=graph-rest-1.0),
[event-delta v1.0](https://learn.microsoft.com/en-us/graph/api/event-delta?view=graph-rest-1.0) and
[beta](https://learn.microsoft.com/en-us/graph/api/event-delta?view=graph-rest-beta),
[calendar-list-calendarview](https://learn.microsoft.com/en-us/graph/api/calendar-list-calendarview?view=graph-rest-1.0),
[user-list-events](https://learn.microsoft.com/en-us/graph/api/user-list-events?view=graph-rest-1.0),
[singlevaluelegacyextendedproperty-get](https://learn.microsoft.com/en-us/graph/api/singlevaluelegacyextendedproperty-get?view=graph-rest-1.0),
[outlook-immutable-id](https://learn.microsoft.com/en-us/graph/outlook-immutable-id),
[known-issues](https://learn.microsoft.com/en-us/graph/known-issues)).

- **A secondary calendar has no v1.0 delta.** v1.0 lists `/users/{id}/calendarView/delta`
  of the primary calendar only; `/calendars/{id}/calendarView/delta` and
  `/calendars/{id}/events/delta` are beta. The concept page's wording that a view may come
  from "some other specified calendar" contradicts the reference; the reference wins.
- **`GET /users/{id}/calendars/{id}/calendarView`** is v1.0, pages by `@odata.nextLink`,
  `$top` 1–1000; it supports "some" query parameters, unnamed, and occurrences come
  expanded. `createdDateTime` and `lastModifiedDateTime` cannot be `$select`ed there.
- **A filter on `lastModifiedDateTime`** of events is not documented anywhere; the known
  issues warn that query parameters may fail silently. Not relied on.
- **`changeKey`** "identifies the version … every time the event is changed, ChangeKey
  changes". The v1.0 delta samples are trimmed and show `@odata.etag` only; the beta
  calendarView/delta sample shows `changeKey` beside it. `$select=id,changeKey` on a list
  is ordinary OData.
- **`PATCH`** answers `200 OK` with the updated event (so its `changeKey`); `If-Match`
  and a 412 are not documented for events — the write-back compares change keys itself.
- **`@removed`** has the one reason `deleted`, and is answered also for an event that
  left the window: a delete and a move out of range look the same.
- **A GET after a delete** — 404, or the item found in *Deleted Items* — is not
  documented. A `permanentDelete` action exists, which suggests a plain delete is
  recoverable.
- **Ids** change when an item moves to another folder or calendar unless every request
  sends `Prefer: IdType="ImmutableId"`; an edit in place is implied, not stated, to keep
  the id. `iCalUId` is per occurrence and read-only.
- **Body**: `Prefer: outlook.body-content-type="text"` is documented on GET and list
  (answered by `Preference-Applied`), not on delta; the same pages also say bodies come
  "in only HTML format". The answer's `body.contentType` says which it was.
- **Organizer**: `isOrganizer` is true when the calendar's owner organizes; an event with
  no attendees is not a draft and sends no invitation; updates mail attendees only when
  there are attendees. `hideAttendees`, `allowNewTimeProposals` and `responseRequested`
  matter only for meetings.
- **Recurrence**: occurrences and exceptions carry `seriesMasterId` and `originalStart`;
  how a deleted occurrence shows in a delta is not documented. Vantigo writes single
  events only, so a series in Outlook is always the person's own.
- **Not documented, for the real tenant**: a GET by id of an event the person deleted
  (404, or found in Deleted Items) through the calendar-scoped path; `changeKey` in a
  calendarView list; `isOrganizer` on an app-created event; that a PATCH of an event with
  no attendees mails nobody; whether an edit in Outlook keeps the id; the body answered
  as text.

### Change notifications, as checked for D (2026-10-28)

Read on learn.microsoft.com ([change-notifications-overview](https://learn.microsoft.com/en-us/graph/change-notifications-overview),
[outlook-change-notifications-overview](https://learn.microsoft.com/en-us/graph/outlook-change-notifications-overview),
[change-notifications-delivery-webhooks](https://learn.microsoft.com/en-us/graph/change-notifications-delivery-webhooks),
[change-notifications-lifecycle-events](https://learn.microsoft.com/en-us/graph/change-notifications-lifecycle-events),
[subscription resource](https://learn.microsoft.com/en-us/graph/api/resources/subscription?view=graph-rest-1.0),
[subscription-post-subscriptions](https://learn.microsoft.com/en-us/graph/api/subscription-post-subscriptions?view=graph-rest-1.0),
[subscription-update](https://learn.microsoft.com/en-us/graph/api/subscription-update?view=graph-rest-1.0),
[application-rbac](https://learn.microsoft.com/en-us/exchange/permissions-exo/application-rbac)).

- **The resource.** For events only `/me/events` and `/users/{id}/events` are documented
  ("changes to all events in a user's mailbox"); `/users/{id}/calendars/{id}/events` and
  `/users/{id}/calendar/events` are **not** listed for events. Whether `/users/{id}/events`
  sees a **secondary calendar**'s events is not stated — "all events in a user's mailbox"
  implies it. Use the object id, never the UPN.
- **Create.** `POST /subscriptions` with `changeType` (`created,updated,deleted`),
  `notificationUrl`, `resource`, `expirationDateTime`, and `clientState` (optional in the
  reference, "required" on the webhooks page — always sent; at most **128** characters).
  201 on success; the same `changeType` and `resource` again is **409 Conflict**
  ("Subscription Id … already exists for the requested combination"). The create
  validates the URL synchronously: a failed handshake is a **400**. Least privilege for
  events is `Calendars.Read`; a page warns that subscriptions "don't support write access
  permissions when only read access permissions are needed" — whether
  `Calendars.ReadWrite` alone is accepted is ambiguous.
- **Expiry.** Outlook events: at most **10 080 minutes** (under seven days) without
  resource data, 1440 with; a request beyond the maximum "will fail" in future; under 45
  minutes is raised to 45. Renewal: `PATCH /subscriptions/{id}` with
  `expirationDateTime`, 200 with the subscription; an expired or unknown one is **404** —
  create a new one. `DELETE /subscriptions/{id}` answers 204. Graph deletes an expired
  subscription itself.
- **The handshake.** `POST {notificationUrl}?validationToken=<opaque>` with
  `Content-Type: text/plain`; answer **200 `text/plain`** with the **URL-decoded** token
  within **10 seconds** (an encoded token fails); treat it as opaque and escape it. The
  `lifecycleNotificationUrl` is validated the same way. A query string on the URL is
  echoed back on delivery.
- **Delivery.** A 2xx within **3 seconds**; otherwise retries for up to four hours with
  backoff (10-second timeout). More than 10 % of answers over 3 s in ten minutes delays
  new notifications ten minutes; more than 15 % over 10 s **drops** them for ten minutes,
  unrecoverable. Recommended: queue, answer 202; a 5xx when it could not be queued. The
  body is `{"value": [...]}`, items of several subscriptions mixed: `subscriptionId`,
  `subscriptionExpirationDateTime`, `clientState`, `changeType`, `resource`, `tenantId`,
  `resourceData {@odata.type, @odata.id, @odata.etag, id}`. A batch's maximum size is not
  documented. Compare `clientState` with the stored value; a mismatch is invalid. Event
  latency is documented as "unknown".
- **Lifecycle.** `lifecycleNotificationUrl` (HTTPS, set **at create only**, may equal
  `notificationUrl`); for Outlook events `reauthorizationRequired`, `subscriptionRemoved`
  and `missed`, items `{subscriptionId, subscriptionExpirationDateTime, tenantId,
  clientState, lifecycleEvent}`, batched. Answer 202 and check `clientState`.
  `reauthorizationRequired`: `POST /subscriptions/{id}/reauthorize` or a `PATCH` of the
  expiry (not both within ten minutes); unanswered, delivery pauses and changes in the gap
  are lost. `subscriptionRemoved`: recreate (it may fail while access is denied) and
  resync. `missed`: resync. The pages recommend delta for every gap.
- **Basic notifications carry no `validationTokens`** (that array is the rich
  notifications'); no encryption certificate is needed. Microsoft's addresses are among
  Microsoft 365's "additional endpoints"; a firewall *may* admit only them — optional.
- **Limits.** 1000 active subscriptions per mailbox for all applications; a quota breach
  is 403 with the limit in its message. Per-tenant and per-app limits, and throttling of
  creates, are not documented beyond Graph's 429s. `notificationUrl` must be public HTTPS
  (TLS 1.2).
- **Not documented, for the real tenant**: a secondary calendar's events under
  `/users/{id}/events`; whether a subscription respects RBAC for Applications' scope (and
  the create's answer outside it — 403 expected); what a scope change, a mailbox move, a
  leaver or a removed licence does to a live subscription (`subscriptionRemoved`, or
  silence); whether `Calendars.ReadWrite` alone is accepted for the create; an event
  notification's `resource` shape; the batch's maximum size.

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
