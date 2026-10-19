# Calendar invitations — design (phase 1, delivery B)

Delivery **B** of the calendar's phase 1, decided in outline by
[`2026-10-19-calendar-design.md`](2026-10-19-calendar-design.md) D9 and detailed here:
an appointment may have people on it who are **not users** — a customer's contact, or an
address typed in — and they are told by mail, in the layout the research found Gmail,
Outlook and Apple Mail key on for their native buttons, with a **web link** to answer by
that lands the answer in Vantigo. A colleague on an appointment is told by mail too,
through identity, without their address crossing the module boundary. A delete of an
appointment someone outside has been told of becomes a **cancellation** that is mailed,
kept and swept.

A built the model so that B adds columns and no concept: `ical_uid` and `sequence` have
existed since the first row, an occurrence has been named by its day, and an attendee's
answer has had its vocabulary. What B must still decide: how an outside attendee is
stored and shown, what the mail is and when it goes, how the answer comes back, how a
colleague is reached, and what a cancellation keeps.

## Decisions

### D1 — Three kinds of attendee on one table

Migration `00056_calendar_invitations.sql`, on `calendar.appointment_attendees`:

```sql
ALTER TABLE calendar.appointment_attendees
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN kind                   varchar(8)   NOT NULL DEFAULT 'user', -- 'user' | 'contact' | 'email'
    ADD COLUMN contact_id             integer,                              -- opaque: Customers' contact
    ADD COLUMN email                  varchar(254),                         -- 'email' kind: lower-cased
    ADD COLUMN name                   varchar(200),                         -- 'email' kind: as typed, may be null
    ADD COLUMN rsvp_token_sha256      char(64),
    ADD COLUMN rsvp_link_ciphertext   text,                                 -- the link, sealed, so a resend carries the same one
    ADD COLUMN invited_at             timestamptz,                          -- the last successful send
    ADD COLUMN invited_sequence       integer,                              -- the sequence that send carried
    ADD COLUMN send_failed_at         timestamptz;                          -- the last failed send since invited_at, cleared by a success
DROP INDEX calendar.ux_appointment_attendees;
CREATE UNIQUE INDEX ux_appointment_attendees_user    ON calendar.appointment_attendees (appointment_id, user_id)    WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX ux_appointment_attendees_contact ON calendar.appointment_attendees (appointment_id, contact_id) WHERE contact_id IS NOT NULL;
CREATE UNIQUE INDEX ux_appointment_attendees_email   ON calendar.appointment_attendees (appointment_id, email)      WHERE kind = 'email';
CREATE UNIQUE INDEX ux_appointment_attendees_rsvp    ON calendar.appointment_attendees (rsvp_token_sha256)          WHERE rsvp_token_sha256 IS NOT NULL;
ALTER TABLE calendar.appointments ADD COLUMN cancelled_at timestamptz;
```

A **user** is as in A. A **contact** names a row of Customers by its opaque id; its name
and address are read through `Deps.Directory.Contact` **at send time and at read time**,
never copied, so a corrected address is used by the next mail and a contact's rename
shows on the appointment. An **email** attendee is an address typed in, held to RFC 5322
by `net/mail.ParseAddress`, lower-cased, with the name as typed. The module's validation
holds that a `user` row has `user_id` and nothing else, a `contact` row `contact_id`, an
`email` row `email`; by house style no CHECK says so. The two outside kinds carry the
RSVP token (D5); a user never does.

On the wire, `AppointmentWrite.attendees` becomes a list of objects rather than ids:

```
AttendeeWrite { kind: user|contact|email, userId?, contactId?, email?, name? }
AppointmentAttendee {
  id, kind,
  user?: { id, displayName },        // user
  contact?: { id, name },            // contact — the name from the directory; the address never on the wire
  email?, name?,                     // email — as typed
  status, respondedAt?,
  invitation?: { sentAt?, sequence?, failedAt? }   // the two outside kinds only
}
```

The rules at write: at most **50** of all kinds together, the owner never among them, a
user active, a contact one the directory answers **and** one the writer may name — the
writer holds `customers:contacts-view`, the sensitive key that guards a contact's address
— else a field error on `attendees[i]`; a contact without an address is refused the same
way, since the point is to mail them; an email attendee's address well-formed; two
attendees naming the same user, contact or address are one. A `PUT` keeps an attendee
who is still listed (their answer, their token and their invitation record with them),
drops one who is not (D4 tells them), and adds the new ones. Customers' anonymisation
reaches a contact through nothing here — the row holds an id and the directory answers
nothing for an erased contact, so the appointment shows "a contact" with no name, and
the mail to them is not sent; the module registers no `CustomerPersonalData` holder,
because it holds no personal data of the contact's.

### D2 — Contacts of a customer, for the picker

`GET /api/v1/calendar/contacts?customerId=` answers `[{ id, name, email }]` for the
contacts of one customer that have an address, through a new
`contracts.CustomerDirectory.ContactsForCustomer(ctx, customerID) ([]ContactEntry,
error)` that Customers implements over its association query. The calendar gates it on
the pair Customers' own list requires, `customers:contacts-view` and
`customers:associations-view`, judged in the module and answered 403 otherwise, and on
`calendar:access` in the contract. The address is on this answer because the caller
holds the sensitive key that guards it in Customers; it is **not** on the appointment
(D1), whose readers may not.

### D3 — The invitation: one mail per outside attendee, the iMIP layout

Built with `arran4/golang-ical` and sent through `mail.Outbound`, which gains one field,
`Calendar *mail.CalendarPart{ Method, Body }`: `internal/mail` adds it as the **third
alternative** after the text and the HTML — `text/calendar; method=<Method>`, which
go-mail completes with `charset=UTF-8` — and attaches the same body as `invite.ics`
(`application/ics`). The message is therefore the research's layout, which the clients
key on:

```
multipart/mixed
├─ multipart/alternative
│  ├─ text/plain
│  ├─ text/html
│  └─ text/calendar; method=REQUEST; charset=UTF-8
└─ application/ics; name="invite.ics"
```

**From and ORGANIZER are the same address**, the installation's `SMTP_FROM`, with the
organiser's display name as the mail's display name and as `CN` — the one arrangement
the research found no client refuses. The owner's address never crosses from identity
and would not be the sender anyway: the installation sends, on the owner's behalf, and a
reply by mail lands with the installation (D5 says what the mail asks instead).

The `VEVENT`: `UID` the row's `ical_uid`; `SEQUENCE` the row's; `DTSTAMP` now;
`DTSTART`/`DTEND` as UTC instants, or `VALUE=DATE` with the exclusive end for an all-day
one; `SUMMARY`, `LOCATION`, `DESCRIPTION` (the description, then a blank line, then the
RSVP sentence and the link); `URL` the RSVP link; `STATUS:CONFIRMED` (`CANCELLED` on a
cancellation); `CLASS` from `visibility`; `TRANSP` from `transparency`;
`ORGANIZER;CN=<owner>:mailto:<SMTP_FROM>`; one `ATTENDEE;CN=<name>;ROLE=REQ-PARTICIPANT;
PARTSTAT=<status>;RSVP=TRUE:mailto:<address>` per **outside** attendee — every outside
attendee on every copy, as the clients expect, and no user: a colleague's address is
identity's and is not put in a file that leaves the company. A series carries `RRULE`
(the stored rule, canonical), an `EXDATE` per cancelled occurrence (the occurrence's
start, in the series' zone, as the series would have made it), and one further `VEVENT`
per moved occurrence with the same `UID`, `RECURRENCE-ID` the original start, and the new
`DTSTART`/`DTEND` — written with the library's generic property since it has no helper,
and covered by a golden test. Instants are written in UTC throughout, so no `VTIMEZONE`
is needed; C, which must write `TZID` for the feed, adds it there.

The **text and HTML bodies** are in the organiser's language — `contracts.UserEntry`
gains `PreferredLanguage` (`"en"`, `"nb"` or `""`), a fact of the user that is not
sensitive and that the directory already holds — with the title, when (in the
appointment's zone, named), where, who is organising and on whose behalf the
installation writes, the description, and the sentence the research asked for:
*"Answer with the link below so the organiser sees it in Vantigo"*, then the link. The
subject is `Invitation: <title> · <day>` (`Invitasjon: …`), `Updated: …` on a resend and
`Cancelled: …` on a cancellation.

**When it goes.** After the commit, never inside it, through the module's own `smtpSend`
accessor (Staff's shape), one message per outside attendee, in order, under a budget of
thirty seconds for the lot, on a context detached from the request:

| On | To whom | Method, sequence |
| --- | --- | --- |
| create | every outside attendee | `REQUEST`, the row's sequence |
| a `PUT` that bumped `sequence` (when or where changed) | every outside attendee, kept or new | `REQUEST`, the new sequence |
| a `PUT` that did not | the outside attendees **added** | `REQUEST`, the current sequence |
| a `PUT` | each outside attendee **dropped** | `CANCEL` to that one, the current sequence, with that one as the only `ATTENDEE` |
| an occurrence moved, cancelled or put back | every outside attendee | `REQUEST`, the new sequence (the whole series again, which is how the clients learn of an exception) |
| the appointment cancelled (D6) | every outside attendee | `CANCEL`, the new sequence |
| `POST /appointments/{id}/attendees/{attendeeId}/resend` | that one, by the owner | `REQUEST`, the current sequence |

A send that succeeds writes `invited_at` and `invited_sequence` and clears
`send_failed_at`; one that fails writes `send_failed_at`, logs with the address redacted
(Staff's regex), and leaves the row for the owner's **Resend**. Nothing is queued and
nothing retries by itself: Communications' outbox is the module for mail that must
arrive, and an invitation the owner can see failed and resend is enough here. When the
mail driver is not `smtp`, outside attendees are stored and marked not sent, and
`GET /meta` says `invitationsAvailable: false` so the sheet can say why. An attendee
whose contact has no address, or whose contact the directory no longer answers, is
skipped with `send_failed_at`.

The link's base is `PUBLIC_BASE_URL` when set — the public host, where the page is
reachable by anyone — and `APP_URL` with the base path otherwise, where the page is
sessionless all the same; the pay link's precedent requires the public one, but an
invitation to a customer who may well be on the company's network need not.

### D4 — A colleague is mailed through identity: `UserDirectory.Notify`

```go
// Notice is a plain mail to one user, composed by a module in the languages it has,
// delivered by identity in the user's own, and never answering the address.
type Notice struct {
    Texts map[string]NoticeText // keyed "en", "nb"; identity picks the user's preferred language, then "en", then any
}
type NoticeText struct{ Subject, Body string }

// Notify mails the user through the installation's mail driver. An inactive or unknown
// user is not mailed and is not an error; a driver that is not configured is not an
// error either (the notice is simply not sent). A send that fails is an error that
// names no address.
Notify(ctx context.Context, userID uuid.UUID, n Notice) error
```

Identity implements it over `Deps.Mail` (`mail.Message`, text only, `SMTP_FROM`), the
way it sends its own invitations. The calendar calls it after the commit for every
**user** attendee: on create ("Kari Nordmann invited you: Kundemøte, Tue 3 Nov 09:00"),
on a `PUT` that bumped the sequence ("… changed …"), on a cancellation, and for one who
is dropped; and for the **owner** when an outside attendee answers through the link (D5)
— "Ola Hansen accepted: Kundemøte". The body carries the link into the app
(`APP_URL/calendar?date=…&appointment=…`), and nothing else: the appointment is on the
colleague's calendar already, and their answer is the in-app one. No ICS and no native
buttons for a colleague, by design.

This is the seam the time zones design's D5 reserved; the directory's
`PermissionHolders` keeps its `Email` for Staff's one use, and no new method hands an
address over.

### D5 — The answer by web link, on the pay link's pattern

Each outside attendee gets a token at creation: 32 random bytes, `base64.RawURLEncoding`,
stored as its hex SHA-256 in `rsvp_token_sha256`, the link sealed in
`rsvp_link_ciphertext` under the purpose `calendar/rsvp-link` (the secrets box gains the
purpose), so a resend carries the **same** link and a client that filed the first mail
answers through the second. The link is `<base>/rsvp?token=<token>`: the token in the
query, as the pay link's is, read once by the page, kept in `sessionStorage` and cleared
from the address bar, and **posted** — never a `GET` with the token in an API path. Two
anonymous operations, rate-limited per client address, on the public host's list:

| Operation | Access | Limit |
| --- | --- | --- |
| `POST /api/v1/calendar/rsvp/read` `{ token }` → `RsvpPage` | `anonymous` | 60 / 10 min |
| `POST /api/v1/calendar/rsvp` `{ token, status }` → `RsvpPage` | `anonymous` | 30 / 10 min |

`RsvpPage { title, allDay, startsAt?, endsAt?, startDate?, endDate?, timeZone, location?,
organiser: { name }, company, attendee: { name }, status, cancelled, language }` — what
the invitee needs to decide and nothing more: no description (it may be internal), no
other attendee, no owner address. `company` is the workspace's name from identity's
system settings if it has one, else absent. 404 with no body for a token nobody holds; an
answer to a cancelled appointment is 409 `appointment_cancelled`. The answer sets the
attendee's `status` and `responded_at`, and the owner is told (D4). `security.PayRoutes()`
gains `GET /rsvp`, the two `POST`s, and `TestPublicHostGate` the three; the host's
`publicPaths` and `sessionlessPaths` gain `/rsvp`.

What the native buttons do is unchanged from the research: they add the event to the
invitee's calendar, and their `METHOD:REPLY` goes to `SMTP_FROM`'s mailbox, where nobody
reads it until an inbound route exists. The mail says so, which is why the link is in the
body and in `URL`.

### D6 — A delete becomes a cancellation once someone outside has been told

`DELETE /appointments/{id}`: when **no outside attendee has ever been sent** a `REQUEST`
(`invited_at` null on every outside row, or there are none), the row is removed as in A.
Otherwise the appointment is **cancelled**: `cancelled_at` set, `sequence` bumped,
`CANCEL` mailed to every outside attendee and the colleagues notified; the row stays,
answered by `GET` with `cancelledAt` and read-only — every write is 409
`appointment_cancelled` — and absent from `GET /items` and the colleague view. The sheet
opens it with a banner and nothing to change. A cancelled series' exceptions stay as they
were, so the `CANCEL` can name what it cancels.

The **`calendar-cancellations` worker** (`CALENDAR_CANCELLATIONS_ENABLED`, default on;
`CALENDAR_WORKERS_POLL`, default an hour, at least a minute) erases cancelled rows whose
last day has passed — a single appointment's end, a bounded series' `series_last_date`,
and for an unbounded series ninety days after `cancelled_at` — under an advisory lease
(`0x43414C43414E4331`, "CALCANC1"), at most 200 a cycle, Staff's shape with nothing else
to do. The RSVP page for a token of an erased attendee is 404, as for one that never
existed.

### D7 — The app

- **The sheet's attendees** become three adders over one list: **Colleagues** (A's
  picker), **Contacts** (shown when the appointment is made from a customer and `GET
  /contacts` answers; a `MultiSelect` of the customer's contacts with an address) and
  **By email** (an address and a name). Each row shows its kind's icon, the name, the
  answer, and for an outside attendee "Invited 3 Nov" / "Not sent" / "Sending failed"
  with **Resend** for the owner; a tooltip explains "Not sent" when `meta.
  invitationsAvailable` is false. A contact with no name (anonymised) shows "A contact".
- **A cancelled appointment** opens read-only with the banner "Cancelled on 3 Nov"; the
  grid does not draw it.
- **The RSVP page**, `/rsvp`, in the host from `@vantigo/calendar-ui/pages/rsvp`, the pay
  page's shape: the title, when in the appointment's zone (and in the browser's when it
  differs), where, who is organising, the invitee's name, the three answers with the
  current one marked, a thank-you line after answering, "This appointment has been
  cancelled" when it has, "This link is not valid" on a 404. In the appointment's
  language (`RsvpPage.language`), not the browser's: the invitee got the mail in it.
- **Meta**: `invitationsAvailable`.

### D8 — Docs and roadmap

- `en/reference/calendar.md`: the attendee kinds and their rules, the contacts
  endpoint, **Invitations** (the mail's layout and properties, the table of when it goes,
  failures and resend), **The answer by link** (the two operations, the token, the
  limits), **Cancellation** and the worker, `Notify` under the contracts the module
  consumes, the new refusal codes, the endpoints table; `reference/identity.md` or
  `admin/authentication.md` wherever `UserDirectory` is described, for `Notify` and
  `PreferredLanguage`; `reference/customers.md` for `ContactsForCustomer`.
- `{en,nb}/user/calendar.md`: inviting a contact or an address, what the invitee
  receives and how they answer, resend, what a cancellation does; the RSVP page from
  the invitee's side in a short section.
- `{en,nb}/admin/calendar.md`, new, `sidebar.order: 45`: what invitations need (`MAIL_DRIVER=smtp`,
  `SMTP_FROM` on a domain with SPF, DKIM and DMARC, since Gmail files an unknown sender's
  invite away; `PUBLIC_BASE_URL` for a link that works outside the network), the public
  host's three routes, the worker's two variables, what to watch (a failed send is on the
  attendee row and in the log); `{en,nb}/admin/index.md`'s bullet; `admin/authentication.md`'s
  environment table in both languages; `deploy/compose/vantigo.env.example`.
- `en/contributing/module-boundaries.md`: `Notify` beside `PermissionHolders` under the
  "mail them, do not hand the value over" rule; `ContactsForCustomer`; the public-route
  rule's example gains the RSVP routes; `platform-packages.md`'s secrets table gains
  `calendar/rsvp-link`, and the mail package's `CalendarPart`.
- `ROADMAP.md`: B built, with the decisions taken while building it.

## Out of scope

The inbound `METHOD:REPLY` route (the ICS is written so it can be added); a queue or a
retry for invitation mail (the owner resends); `VTIMEZONE` (C's, for the feed); an
attendee's `DELEGATED` or `COUNTER`; optional attendees (`ROLE=OPT-PARTICIPANT`); a
per-attendee note; a reminder (`VALARM`) in the invite — the client's own default
reminder applies; a copy of the invitation to the owner; a reply to the invitee when the
owner changes the time beyond the updated `REQUEST`; inviting a customer's contact
without `customers:contacts-view`; a `CustomerPersonalData` holder (D1); the owner
answering for an invitee; moving an appointment to another owner.

## Testing

**Backend** (`internal/calendar`, the harness's `mailbox` of `mail.Outbound` as Staff's,
`invitation_test.go`, `rsvp_test.go`, `cancellation_test.go`, a golden `testdata/invite/
*.ics` with `-update`): a create with a contact and an email attendee sends two
messages, each with the three alternatives and the attachment, From and `ORGANIZER`
`SMTP_FROM` with the owner's name, every outside attendee as `ATTENDEE` and no user, the
text in the owner's language with the link; a series' ICS carries `RRULE`, an `EXDATE`
and a `RECURRENCE-ID` event, byte-for-byte against the golden; a title change sends
nothing, a time change sends `REQUEST` with `SEQUENCE` 1 to both, adding a third sends
to the third alone, dropping one sends `CANCEL` to that one; a failed send marks
`send_failed_at` and the resend clears it; no `smtp` driver stores the attendees unsent
and `meta` says so; a contact without an address, an unknown contact, a malformed
address, the owner, a 51st and a writer without `customers:contacts-view` are each
refused; the contact's name comes from the directory and its address is never on the
wire; `GET /contacts` answers the customer's contacts with addresses under the pair and
403 without; the token is not stored and the link resolves by hash; `rsvp/read` answers
the page for a live token, 404 for a wrong one, 429 on the 61st read; `rsvp` sets the
status and `respondedAt`, notifies the owner through a recording `Users`, and is 409 on
a cancelled appointment; a delete with nobody invited removes the row, one with an
invitee cancels it, mails `CANCEL` with the bumped sequence, hides it from `/items` and
the colleague view, makes every write 409 and `GET` answer `cancelledAt`; the worker
erases a cancelled single after its end and a cancelled unbounded series after ninety
days, not before, under the lease; `Notify` is called for each user attendee on create,
on a time change, on a cancellation, and for the owner on an answer, never with an
address. **Identity**: `Notify` sends to an active user in their language, falls back
to English, sends nothing for an inactive or unknown user, and errors without the
address when the driver fails. **Customers**: `ContactsForCustomer` answers the
customer's contacts with addresses, ordered by name. **Mail**: `CalendarPart` renders
as the third alternative with `method=` and as the attachment. **Security**:
`TestPublicHostGate` admits the three routes and refuses `/rsvp/x`.

**Frontend**: the sheet adds a contact and an email attendee and sends `kind` on the
wire; a row shows "Invited", "Not sent" and "Sending failed" with Resend; a cancelled
appointment is read-only with the banner; the RSVP page reads the token from the query,
clears it, posts it, shows the appointment in its language, answers, shows the
thank-you, the cancelled and the invalid states, and never puts the token in a URL;
`pay.test.tsx`'s allowlist test extended to `/rsvp`.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
