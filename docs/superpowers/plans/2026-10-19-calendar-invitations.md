# Calendar phase 1 delivery B — invitations: implementation plan

**Goal:** an appointment carries attendees from outside the company — a customer's
contact or a typed address — who are mailed an iMIP invitation with a web link to
answer by; a colleague is told through identity's new `Notify` seam; a delete of an
appointment someone outside has been told of becomes a mailed cancellation, kept until
its last day and then swept.

**Spec:** [`docs/superpowers/specs/2026-10-19-calendar-invitations-design.md`](../specs/2026-10-19-calendar-invitations-design.md)
(D1–D8), on [`2026-10-19-calendar-design.md`](../specs/2026-10-19-calendar-design.md) D9.

**Pattern code:** `internal/invoices/paylinks.go`, `paypage.go` and `module.go`'s
`limits` (a sealed link, a hashed token, anonymous POSTs, rate limits);
`internal/security/publichost.go` and its test (the public host's list);
`internal/staff/notifications.go` and `contractscalls.go` (`smtpSend`, `mailAvailable`,
sending after the commit under a budget, the redacted log) and `harness_test.go` (the
`mailbox`); `internal/mail/outbound.go` (the message builder); `internal/identity/
invitations.go` and `links.go` (identity mailing a user); `internal/customers/queries/
contacts.sql` `ListContactAssociationsForCustomer` and `customers/directory.go`;
`internal/staff/retention_worker.go` and `internal/config/config.go`'s `staffWorkers`
(a worker behind a switch); `internal/invoices/ehf/render_test.go` (a golden with
`-update`); `apps/invoices/frontend/src/pages/pay.tsx`, `api/pay.ts`, `apps/host/
frontend/src/routes/pay.tsx`, `lib/public-paths.ts` and `routes/pay.test.tsx` (the
sessionless page and its allowlist test).

## The order

Task 2 first; then 3 (backend), 4 (the app) and 5 (docs) in parallel; 6 last.

## Tasks

- [x] **1. The spec and this plan.**
- [x] **2. The contracts and the seams.** `openapi/calendar.yaml`: `AttendeeWrite`,
  `AppointmentAttendee` with `kind`, `contact`, `email`, `name` and `invitation`,
  `Appointment.cancelledAt`, `CalendarMeta.invitationsAvailable`, `GET /contacts`,
  `POST /appointments/{id}/attendees/{attendeeId}/resend`, the two anonymous `rsvp`
  operations with `RsvpPage`, the codes `appointment_cancelled` on `CalendarProblem`;
  `go generate`, `bun run gen:client`. Migration `00056_calendar_invitations.sql` with
  its pin test; `queries/attendees.sql` rewritten for the kinds (insert per kind, keep,
  the token lookup, the invitation marks), `queries/appointments.sql` (`cancel`, the
  window reads exclude cancelled, the sweeper's `ListCancelledDue` and `Delete`).
  `contracts.UserDirectory.Notify` and `Notice`/`NoticeText`, `UserEntry.
  PreferredLanguage`, identity's implementation and tests; `contracts.
  CustomerDirectory.ContactsForCustomer`, Customers' implementation and test;
  `mail.CalendarPart` on `Outbound` and its test; `security.PayRoutes()`'s three routes
  and `TestPublicHostGate`; `config`'s `CalendarCancellationsEnabled` and
  `CalendarWorkersPoll` with the loader and `vantigo.env.example`; the secrets purpose
  `calendar/rsvp-link`; `modtest`'s fake `Users` recording notices if the harness needs
  one.
- [x] **3. The backend, tests first.** `attendees.go` (the kinds' validation, the
  directory and contact checks, the token and the sealed link, kept/dropped/added);
  `invitations.go` (the ICS with golang-ical — `go get github.com/arran4/golang-ical
  v0.3.8` — the text and HTML bodies in the owner's language, the send table of D3,
  the marks, the budget, the resend endpoint); `notices.go` (the colleague and owner
  notices through `Notify`); `rsvp.go` (the two anonymous handlers, the limits in
  `module.go`); `cancellation.go` (the delete's two paths, the 409 on every write, the
  reads' exclusion) and `cancellations_worker.go` (the sweeper under its lease, in
  `Module.Workers`); `meta.go`'s flag; `contacts.go` (`GET /contacts`); `appointments.
  go` and `occurrences.go` calling the sender after their commits; the golden under
  `testdata/invite/`; the tests of the spec's backend paragraph.
- [x] **4. The app, tests first.** `apps/calendar/frontend`: `api/attendees.ts`,
  `api/contacts.ts`, `api/rsvp.ts`; the sheet's three adders and the attendee rows with
  their invitation state and Resend; the cancelled banner and read-only sheet;
  `pages/rsvp.tsx` on the pay page's shape (token from the query to `sessionStorage`,
  cleared, posted; the states); `i18n.ts` en + nb. `apps/host/frontend`: `routes/rsvp.tsx`,
  `lib/public-paths.ts`, `routes/pay.test.tsx`'s allowlist extended (or a sibling
  `rsvp.test.tsx`).
- [x] **5. Docs, both languages.** `en/reference/calendar.md` (the kinds, Invitations,
  The answer by link, Cancellation and the worker, the endpoints), the `UserDirectory`
  and `CustomerDirectory` additions where those contracts are described,
  `{en,nb}/user/calendar.md`, `{en,nb}/admin/calendar.md` (new) and `admin/index.md`,
  `{en,nb}/admin/authentication.md`'s environment table, `en/contributing/
  module-boundaries.md` and `platform-packages.md`, `ROADMAP.md` (B built).
- [ ] **6. Gates and the pull request.** `mise run server:check`, `server:test`,
  `frontend:check`, `docs:check`; the PR on `feat/calendar-invitations` against `main`
  with CI green.
