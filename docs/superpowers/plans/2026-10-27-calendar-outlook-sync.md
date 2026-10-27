# Calendar phase 2, delivery A — the switch, the person's setting and the push: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person turns on *Outlook* on Calendar → Settings and, within a minute, their
appointments and absences (and whatever else they chose) are in their Outlook calendar,
kept in step as they change — once an Owner has turned on **Calendar sync** on the Entra
page and granted `Calendars.ReadWrite`.

**Architecture:** the calendar module owns the person's setting (`calendar.mailbox_sync`),
the map of what it wrote (`calendar.mailbox_events`) and the `calendar-mailbox-sync`
worker, which builds the desired set from the appointments and the providers' items
under `AsUser` (the feed's read) and diffs it against the map. It writes through a new
single-provider contract, `contracts.MailboxCalendar`, filled by the Entra module over
its existing Graph client and token. The mailbox is the account Staff's provider link
names, read through a new `contracts.Workforce.DirectoryAccount`. Branch
`feat/calendar-outlook-sync`, cut from `main` at `a67178d3`.

**Spec:** [`docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`](../specs/2026-10-27-calendar-outlook-sync-design.md), O2–O8.

## Plan readings

1. **The contract file** is `internal/contracts/mailbox_calendar.go`, exactly O4's
   shape; `MailboxStatus.State` constants `MailboxAvailable`, `MailboxCleanup`,
   `MailboxUnavailable`; the four sentinels; `MailboxOpCreate|Update|Delete`; the
   `ShowAs` constants. Its doc comment states the lock rule (rule 13's) and that the
   provider writes only to accounts it manages.
2. **The slot.** `module.Module.MailboxCalendar func(Deps) contracts.MailboxCalendar`,
   `Deps.MailboxCalendar`; `Compose` resolves it with `soleProvider` after the identity
   provisioners (it reads nothing while built); `Workers` resolves it as well
   (`withMailboxCalendar`, after `withIdentityProvisioners`), because the worker runs in
   worker mode. Compose and Workers tests.
3. **Staff's read.** `contracts.Workforce.DirectoryAccount(ctx)` and
   `contracts.DirectoryAccount`; Staff answers from `staff.identity_links` (the identity
   link naming the principal's user → the person → the link whose `provider <>
   'identity'`, `external_id` set, state `enabled`/`disabled`); no key; the principal
   from `contracts.PrincipalFrom` (a request's or AsUser's). Every existing `Workforce`
   fake gains the method.
4. **The fake.** `modtest.FakeMailboxCalendar`: an in-memory mailbox per external id
   (calendars, events with key, the marker), a settable status, injectable refusals per
   mailbox, a record of every call — what the calendar's tests run against.
5. **Migrations.** `00081_calendar_mailbox_sync.sql` (O5's two tables;
   `ix_mailbox_sync_due (next_sync_at) WHERE enabled OR state = 'stopping'`; no CHECK,
   the module's validation owns the values, as the rest of the calendar schema),
   `00082_entra_calendar_sync.sql` (`entra.connection.calendar_sync`). Both with down
   migrations.
6. **The calendar contract** (`openapi/calendar.yaml`): `getCalendarMailboxSync`,
   `putCalendarMailboxSync` (`If-Match`), `postCalendarMailboxSyncRun`, all
   `calendar:access`; schemas `MailboxSync` (`availability`, `enabled`, `target`,
   `detail`, `kinds`, `availableKinds [{key, module}]`, `state`, `error?`,
   `lastSyncedAt?`, `revision`) and `MailboxSyncWrite`. 409 `mailbox_sync_unavailable`
   with `reason`, 412 on a stale revision (the module's existing revision refusal), 400
   for an unknown kind/target/detail as field errors.
7. **The Entra contract** (`openapi/entra.yaml`): `EntraSettingsRequest.calendarSync`
   (boolean, optional, merged as the others), `EntraConnection.calendarSync {enabled,
   status, reason?}`. `missingPermissions` takes the switch.
8. **The window read is the feed's.** The worker reuses `occurrencesIn` (one event per
   occurrence) over `ListAppointmentsInWindow`, and `sourceItemsIn` under
   `contracts.AsUser(ctx, user)` on a context with no request; `CalendarSources` is
   already in worker-mode `Deps`. The base URL for the link is the RSVP link's
   (`attendees.go`'s rule).
9. **The nudge** is a query `NudgeMailboxSync(user_ids[])` run inside each appointment
   write's transaction, the user ids being the owner and the `user` attendees before
   and after the write.
10. **All-day zones.** Entra maps the IANA `TimeZone` to the Windows name with a reverse
    of the CLDR table; since modules may not share code, Entra keeps its own small table
    (`internal/entra/windowszones.go`, generated from the same source — copy the
    calendar's map and invert it) and falls back to UTC.
11. **Graph.** Entra's `mailbox.go`: `POST /$batch` with `requests[{id, method, url,
    body, headers}]` (the body's `Content-Type: application/json`); per-response status;
    429 inside a batch retried once after its Retry-After, then the op's error. Paths:
    `users/{oid}/events`, `users/{oid}/calendars/{cid}/events`, `users/{oid}/events/{eid}`,
    `users/{oid}/calendars`, `users/{oid}/calendars/{cid}`. `Find`:
    `users/{oid}/events?$filter=singleValueExtendedProperties/Any(ep: ep/id eq '<marker>' and startswith(ep/value, 'vantigo:'))&$expand=singleValueExtendedProperties($filter=id eq '<marker>')&$select=id&$top=100`,
    following `@odata.nextLink`; in a secondary calendar the same without the filter. Keys
    are written prefixed `vantigo:`. The status reads the row and the secrets only.
12. **`graphtest`** learns calendars (create, get, delete), events (create with the
    extended property, patch, delete, list with the marker filter and the expand,
    paging), `$batch`, a per-mailbox 403 and `MailboxNotEnabledForRESTAPI`, and a user
    that does not exist.

## Tasks

- [ ] **1. This plan, the research and the spec.**
- [ ] **2. The contract, the slot, the migrations, the queries and the API (one worker, alone).**
  - `internal/contracts/mailbox_calendar.go` (reading 1); `workforce.go`'s
    `DirectoryAccount` (reading 3).
  - `internal/module`: the slot (reading 2) with Compose and Workers and their tests.
  - `internal/modtest`: `FakeMailboxCalendar` (reading 4) with a test; the
    `Workforce` fakes gain `DirectoryAccount`.
  - Migrations (reading 5); the calendar's `queries/mailbox_sync.sql` (get, upsert,
    claim due, write the result, nudge, list/put/delete map rows, empty the map) and
    Entra's settings query; `sqlc generate` in both modules.
  - `openapi/calendar.yaml` and `openapi/entra.yaml` (readings 6, 7);
    `go generate ./...` in `apps/server`; stub handlers answering 503 so every package
    builds; `bun run gen:client` for the frontends.
  - `go build ./... && go vet ./...` and the full `server:test` pass.
- [ ] **3a. Staff's read (`internal/staff`), after 2.** The query, `DirectoryAccount` on
  the workforce, tests (the principal's own; no person; the link's states; another
  user's never; a request and AsUser).
- [ ] **3b. Entra's provider (`internal/entra`), after 2.** `mailbox.go` (the
  `MailboxCalendar` over the client, readings 10–11), `windowszones.go`, the
  `calendarSync` setting and status in `connection.go`, `missingPermissions` with the
  switch, `Module.MailboxCalendar`; `graphtest` (reading 12) with its tests;
  `mailbox_test.go` and the connection tests.
- [ ] **3c. The calendar (`internal/calendar`), after 2.** `mailbox_sync.go` (the three
  endpoints), `mailbox_push.go` (the desired set and the event shape, O6),
  `mailbox_worker.go` (O7–O8), the accessors in `contractscalls.go`
  (`mailboxStatus`, `mailboxApply`, `mailboxEnsure`, `mailboxDelete`, `mailboxFind`,
  `workforceDirectoryAccount`), the nudge in the appointment writes, the worker in
  `workers` with `CALENDAR_MAILBOX_SYNC_ENABLED` / `CALENDAR_MAILBOX_SYNC_POLL` in
  `internal/config`; tests against `FakeMailboxCalendar`.
- [ ] **4. The frontends, after 2.** `@vantigo/calendar-ui`: `api/mailbox-sync.ts`, the
  *Outlook* card on the settings page, words in `en` and `nb`, tests. `@vantigo/entra-ui`:
  the *Calendar sync* switch in the settings form, its status line and the permission in
  the wizard's list while on, words in `en` and `nb`, tests.
- [ ] **5. Documentation, after 3 and 4.**
  - `reference/calendar.md`: *Outlook sync* — the tables, the endpoints, the worker, the
    event shape, the clean-up; the sources contract's `AsUser` paragraph.
  - `reference/entra.md`: the switch, `calendarSync`, the permission, the mailbox
    provider and its Graph calls and refusals.
  - `reference/staff.md`: `DirectoryAccount` under the workforce contract.
  - `contributing/module-boundaries.md`: the `MailboxCalendar` slot (rule 5), the sixth
    sanctioned write (a new rule 14), rule 12's exception widened.
  - `user/calendar.md` (en, nb): *Sync with Outlook*. `user/entra.md` (en, nb): the
    switch. `admin/entra.md` (en, nb): the permission, consent, RBAC for Applications,
    verifying. `admin/calendar.md` (en, nb): the worker's two settings.
  - `ROADMAP.md`: Calendar phase 2 with A done and B–D listed.
- [ ] **6. Review, then verification.** A security review (read-only) of the contract,
  the worker and the provider, a docs pass against the code, one fix batch; then
  `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`; push; the pull request.
