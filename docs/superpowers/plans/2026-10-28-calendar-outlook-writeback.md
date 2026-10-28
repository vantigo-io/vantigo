# Calendar phase 2, delivery C — the write-back from Outlook: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person whose Outlook sync is on edits, moves or deletes one of their own
appointments in Outlook — on a phone — and the appointment changes in Vantigo within a
sync, through its own update path (validation, `SEQUENCE`, the invitations and notices);
an event they make in the separate *Vantigo* calendar becomes an appointment; anything
else they change in a copy is put back, and the Outlook card says what was not taken.

**Architecture:** the `calendar-mailbox-sync` worker gains a write-back step between the
desired set and the push. It asks the provider for the copies' versions (`Versions`:
ids and change keys), reads whole only the moved, new and missing ones (`Events`),
decides per key (O10.6), writes appointments through refactored internal forms of the
module's own `PUT`, occurrence and delete handlers, and keeps each copy's `change_key`.
B's delta is untouched. Branch `feat/calendar-outlook-writeback`, cut from `main` at
`69ca4b09` (delivery B, vantigo-io/vantigo#191, merged).

**Spec:** [`docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`](../specs/2026-10-27-calendar-outlook-sync-design.md), O10 (O10.1–O10.11).
**Research:** [`…/research/2026-10-27-calendar-outlook-sync.md`](../research/2026-10-27-calendar-outlook-sync.md) §3, *The write-back, as checked for C*.

## Plan readings

1. **The contract** (`internal/contracts/mailbox_calendar.go`): `Versions` and `Events`
   exactly as O10.2, with `MailboxVersions`, `MailboxEventVersion`, `MailboxEventState`;
   `MailboxVersionsMax = 5000`; `Events` takes at most `MailboxMarkedMax` ids. Doc
   comments: both refused under cleanup; `Versions("")` answers only this installation's
   marked events of the default calendar from `from` (`to` unused there), a secondary
   calendar every event of its view `from..to` with no `Key`; `Events` reads through the
   calendar's own path, an id not answered is not in that calendar; nothing of an event's
   words is ever logged by a provider. `MailboxOp`'s doc: an update writes `Key` as the
   marker when set. `MailboxEvent` unchanged.
2. **Migration** `00084_calendar_mailbox_writeback.sql`: `calendar.mailbox_events` gains
   `change_key varchar(512)` and `released_at timestamptz`; `calendar.mailbox_sync` gains
   `put_back_reason varchar(20)`, `put_back_key varchar(200)`, `put_back_at timestamptz`.
   No CHECK (house style). Down drops them.
3. **Queries** (`internal/calendar/queries/mailbox_sync.sql`): `UpsertMailboxEvent` takes
   `change_key` (null keeps the stored one: `COALESCE`); new `SetMailboxEventChangeKey`,
   `StaleMailboxEvent` (hash `''`, optional change key), `ReleaseMailboxEvent`;
   `ListMailboxEvents` answers the new columns; `SetMailboxPutBack` and
   `ClearMailboxPutBack`; `GetMailboxSync` and the row type carry the new columns.
4. **The API** (`openapi/calendar.yaml`): `MailboxSync.putBack?` —
   `MailboxSyncPutBack {reason: enum conflict|refused|not_owner|not_editable, at:
   date-time, appointmentId?: int64, occurrenceDate?: date, title?: string}`, described
   as O10.3. `bun run gen:client`.
5. **The fakes.** `modtest.FakeMailboxCalendar`: every event carries a `ChangeKey` that
   moves with each write (Apply's results answer it); `EditEvent(mailbox, id, fn)` (the
   person editing a copy in Outlook — moves the change key and logs the change),
   `TouchEvent` (the change key moves, nothing else: Exchange's noise), `DeleteEvent`,
   `MoveEvent(mailbox, id, toCalendarID)` (a new id, as without immutable ids),
   `SetMeeting(mailbox, id, bool)`, `SetHTMLBody(mailbox, id, bool)`, `PutForeignInCalendar`
   for a person's own event in the *Vantigo* calendar; `Versions` and `Events` as O10.2
   (default: marked from `from`; secondary: every event overlapping); refusals as the
   other reads. `Apply`'s update stores `Key` as the event's key when set.
6. **The Entra stubs** so everything builds: `Versions`/`Events` answering
   `ErrMailboxUnavailable` until task 3a.
7. **The calendar's internal paths** (`internal/calendar`): refactor, no behaviour
   change, so the worker calls the same code the screen does —
   `s.replaceAppointment(ctx, me, id, revision, body)`, `s.writeOccurrence(ctx, me, id,
   date, body)`, `s.deleteAppointment(ctx, me, id)` and `s.createAppointment(ctx, me,
   body, inTx func(txq, row) error)` (the create's extra statement in its transaction, for
   O10.9's map row) — each answering the row and a refusal value the handlers turn into
   their responses; `writeOf(a, atts) gen.AppointmentWrite`, the appointment as a body
   that writes it back unchanged (attendees by kind, recurrence, links, zone). The
   handlers read `callerID` and call them.
8. **The write-back** (`internal/calendar/mailbox_writeback.go`): O10.4–O10.9 as
   `w.writeBack(ctx, call, r, mailbox, calendarID, p, d)` answering what it wrote, the
   ids it confirmed present, whether it completed, and a provider error. Accessors
   `mailboxVersions`, `mailboxEvents` in `contractscalls.go`. `mailbox_worker.go`: the
   step in `syncPerson` (O10.5), the desired set rebuilt after a write, `reconcile`'s
   forget-missing skipped unless the write-back completed and never for an id it
   confirmed present; `apply` stores each result's `ChangeKey`; the diff skips released
   rows (no update, no delete — forgotten when not wanted); `cleanupEvents` forgets a
   released row without deleting its event; `cleanup` clears the put-back. The worker
   runs each appointment write under `contracts.AsUser(withoutRequest{call}, user)` as
   the other reads, with the user as `me`.
9. **The setting's GET** (`mailbox_sync.go`): `putBack` from the row; its `title` read
   from the appointment when the caller owns or attends it.
10. **Entra** (`internal/entra/mailbox_writeback.go`): `Versions` — default: `GET
    /users/{oid}/events?$filter=<the marker> and start/dateTime ge …&$select=id,changeKey,type&$expand=<the marker>&$top=500`;
    secondary: `GET /users/{oid}/calendars/{cid}/calendarView?startDateTime=…&endDateTime=…&$select=id,changeKey,type&$top=500`;
    next links parsed and the path rebuilt (B's lesson: host exact, path compared
    case-insensitively, the query alone kept), at most 5000, a bad id skipped. `Events` —
    `$batch` GETs of twenty on `/users/{oid}/calendar/events/{id}` or
    `/users/{oid}/calendars/{cid}/events/{id}`, `$select=id,changeKey,type,subject,body,location,start,end,isAllDay,showAs,sensitivity,isCancelled,attendees`
    and the marker expanded, the Prefer headers per request; 404 not answered, 429 once,
    403/gone through `mailboxRefusal`; decodes only those fields, the attendees' count
    alone. `eventOf`: an update with `Key` carries the marker. `graphtest` learns the
    filtered list with `changeKey`, a secondary calendar's `calendarView`, the
    calendar-scoped single GET (404 for an event of another calendar) and the person's
    edits (a change key moving).
11. **The frontend** (`apps/calendar/frontend`): the Outlook card's sentence that edits
    to one's own appointments come back (and, under *a separate Vantigo calendar*, that
    new events there become appointments), and the put-back notice by reason, naming the
    appointment when `title` is answered, in `en` and `nb`; tests.

## Tasks

- [ ] **1. The research, the spec's O10 and this plan.** Committed first, no
  `Docs-Impact` trailer.
- [ ] **2. The contract, the migration, the queries, the API, the fakes and the stubs
  (one worker, alone).** Readings 1–6; `sqlc generate` (calendar); `go generate ./...` in
  `apps/server`; `bun run gen:client`; `go build ./... && go vet ./...` and the full
  `server:test`.
- [ ] **3a. Entra (`internal/entra`), after 2.** Reading 10 with tests against
  `graphtest`: both `Versions` forms, paging and a foreign next link refused, the bound,
  `Events` (ours, another installation's, a meeting, an HTML body, a series, 404, 429,
  403, gone, cleanup refused), the update carrying the marker.
- [ ] **3b. The calendar (`internal/calendar`), after 2.** Readings 7–9 with tests
  against the fake: the refactor's handlers unchanged (existing tests), an owner's edit
  of each field taken (and the mails), noise ignored, the baseline, an occurrence moved
  and its text put back, a conflict, an attendee's and a projected copy put back, a
  refusal put back, a delete (deleted, and cancelled with outsiders invited), an
  occurrence's delete cancelling it, a move out of the window and to another calendar,
  an incomplete list, a meeting released, a new event in the *Vantigo* calendar taken
  once, the 20 cap, a failing read not hiding the push, the lock rule, `putBack` on GET.
- [ ] **3c. The frontend, after 2.** Reading 11.
- [ ] **4. Documentation, after 3.**
  - `reference/calendar.md`: *Outlook sync* — the write-back (the versions, the
    decisions, the fields, deletes, released meetings, new events in the Vantigo
    calendar, `putBack`, the person's own titles not shown), the schema's migration
    list and columns.
  - `reference/entra.md`: the mailbox calendar's `Versions` and `Events`, their Graph
    calls and refusals; the update's marker.
  - `contributing/module-boundaries.md`: rule 14 — the write-back reads.
  - `user/calendar.md` (en, nb): editing, moving and deleting in Outlook, the *Vantigo*
    calendar's new events, what is put back and why the card says so.
  - `admin/entra.md` (en, nb): the sync now reads the copies and the Vantigo calendar
    whole (the same permission), and what a delete in Outlook does.
  - `ROADMAP.md`: C done.
- [ ] **5. Review, then verification.** A security review (read-only) of the contract,
  the write-back and the provider, a docs pass against the code, one fix batch; then
  `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`; push; the pull request with the real-tenant checklist.
