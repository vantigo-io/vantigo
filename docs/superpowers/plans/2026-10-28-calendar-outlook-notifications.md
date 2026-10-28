# Calendar phase 2, delivery D — change notifications from Outlook: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** on an installation Microsoft can reach, a change in a person's mailbox makes
their Outlook sync due at once — B's busy read and C's write-back within a minute rather
than at the next fifteen-minute poll. A notification is a nudge: it sets a time and
nothing else, and the reads stay the truth.

**Architecture:** Entra holds one Graph subscription per synced mailbox on
`/users/{oid}/events` and serves the anonymous webhook,
`POST /api/v1/entra/mailbox-notifications`, on the public host's allowlist; a valid
notification only stamps its subscription row's `notified_at`. The calendar's worker asks
`Watch` after each sync that worked, `Unwatch` in each clean-up, and `Notified(since)`
once a cycle, making the notified people's rows due. No new cross-module direction: the
calendar calls the provider, as it does today. Branch
`feat/calendar-outlook-notifications`, cut from `main` at `157ddaf1` (delivery C,
vantigo-io/vantigo#192, merged).

**Spec:** [`docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`](../specs/2026-10-27-calendar-outlook-sync-design.md), O11 (O11.1–O11.9).
**Research:** [`…/research/2026-10-27-calendar-outlook-sync.md`](../research/2026-10-27-calendar-outlook-sync.md) §3, *Change notifications, as checked for D*.

## Plan readings

1. **The contract** (`internal/contracts/mailbox_calendar.go`): `Watch`, `Unwatch`,
   `Notified` exactly as O11.3; `MailboxNotice{Mailbox string; At time.Time}`;
   `MailboxNoticesMax = 5000`. Doc comments: `Watch` refused under cleanup, nil when the
   provider offers no notifications or holds creates off; `Unwatch` allowed under cleanup,
   idempotent, a failure answered (never forgotten); `Notified` no network, oldest first;
   the mailbox is the `DirectoryAccount.ExternalID` the other calls take, answered as the
   provider holds it (the calendar compares case-insensitively). The package doc's list of
   what the slot does names notifications.
2. **Migration** `00085_entra_mailbox_subscriptions.sql` (Entra's schema only):
   `entra.mailbox_subscriptions (object_id <the type entra.accounts' object id has>
   PRIMARY KEY, subscription_id varchar(64) UNIQUE NULL, client_state_hash bytea NOT
   NULL, expires_at timestamptz NULL, state varchar(20) NOT NULL DEFAULT 'active',
   notified_at timestamptz NULL, created_at, updated_at)`, an index on `notified_at`;
   `entra.connection` gains `notifications_validating_until`,
   `notifications_unreachable_at`, `last_notification_at` (all `timestamptz NULL`). No
   CHECK (house style). Down drops them. No calendar migration: the calendar's
   `mailbox_sync.mailbox` and `last_synced_at` (the sync's start, `syncPerson`'s `now`)
   are what O11.6 needs.
3. **Queries** (`internal/entra/queries/`, a new `mailbox_subscriptions.sql`, and
   `connection.sql`): get by object id; get by subscription id; upsert on create (id,
   hash, expiry, `active`, `notified_at` kept); set expiry and `active` on renew; set
   state; delete by object id; `MarkMailboxNotified(subscription_id, now)` —
   `notified_at = now`, and `state = $lifecycle_state` when given, `WHERE notified_at IS
   NULL OR notified_at < now - 5 s OR $lifecycle_state IS NOT NULL`; list notified after
   `since` ordered, `LIMIT`; count live rows (`expires_at > now`, state not `deleting`);
   the connection's three columns (open/clear the validation window, set/clear
   unreachable, touch `last_notification_at` when older than a minute). The calendar
   (`internal/calendar/queries/mailbox_sync.sql`): `MarkMailboxSyncNotified(mailbox, at,
   now)` — `next_sync_at = now` where `lower(mailbox) = lower($mailbox)`, `enabled`,
   `state IN ('active','pending')`, `(last_synced_at IS NULL OR last_synced_at < $at)`
   and `(next_sync_at IS NULL OR next_sync_at > now)`.
4. **Config** (`internal/config`): `CalendarMailboxNotificationsEnabled`,
   `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED`, default `true`, beside the other
   `CALENDAR_MAILBOX_*`; its test; `.env.example` (or wherever the others are listed).
5. **The public host** (`internal/security/publichost.go`): `PayRoutes` admits `POST
   /api/v1/entra/mailbox-notifications` exactly; its doc names it (the one anonymous
   operation of a module that is not the payer's or the invitee's: Microsoft's webhook);
   the gate's test.
6. **The API** (`openapi/entra.yaml`):
   - `POST /api/v1/entra/mailbox-notifications`, `x-vantigo-access: anonymous`,
     operationId `postEntraMailboxNotifications`, query `validationToken` (optional
     string), request body optional (`application/json` free-form object, and
     `text/plain` for the handshake); responses 200 `text/plain` (the handshake), 202 no
     body, 400 (`AuthErrorResponse`/the module's problem as other operations), 404 no
     body, 429 with `Retry-After`, 503. Described as O11.5. If the generated strict
     wrapper cannot answer the handshake as `text/plain` or would refuse the body before
     the handler sees it, the operation is served by a hand-written handler registered
     on the module router for that path, still declared here (the contract coverage test
     must stay green) — say which in a comment.
   - `EntraCalendarSync.notifications` — `EntraCalendarSyncNotifications {status: enum
     off|not_configured|active|unreachable, subscriptions: int32, lastNotificationAt?:
     date-time, unreachableAt?: date-time}`, required `status` and `subscriptions`,
     described as O11.7.
   - `bun run gen:client` (and the server's `go generate`).
7. **The fakes.** `modtest.FakeMailboxCalendar`: `Watch`/`Unwatch` record the mailboxes
   watched (a set; `Watched(mailbox) bool`, `WatchCalls`, `UnwatchCalls`), refusals
   injectable per call as the other calls' are (`WatchErr`, `UnwatchErr`); `Notify(mailbox,
   at)` queues a notice; `Notified(since)` answers those after `since`, oldest first;
   status rules as the others (`Watch` refused under cleanup; `Unwatch` allowed).
8. **The Entra stubs** so everything builds after task 2: `Watch` answering nil (no
   notifications), `Unwatch` nil, `Notified` an empty list — until task 3a.
9. **Entra, the provider** (`internal/entra/mailbox_notifications.go`): O11.2–O11.4 —
   `notificationsOn()` (config on, `PUBLIC_BASE_URL` set), the URL
   `PublicBaseURL + "/api/v1/entra/mailbox-notifications"`; `Watch` (the account check
   as every call, `ErrMailboxUnavailable` under cleanup; no network while a live row has
   more than three days left; renew by `PATCH`, 404 → create; create with a fresh
   `crypto/rand` `clientState`, the hash stored before the `POST`, the validation window
   opened for 30 s and cleared after; 201 → id (a GUID, strictly) and expiry stored; a
   400 whose code or message says validation failed → `notifications_unreachable_at = now`
   and nil, no create for anyone for an hour; 409 → the application's subscriptions
   listed (`GET /subscriptions`, next links B's way, at most 10 pages), ours for this
   mailbox deleted, one more create; 403 `ErrMailboxDenied`; gone `ErrMailboxGone`
   through `mailboxRefusal`); `Unwatch` (expired → forget; `DELETE`; 204/404 → forget;
   else `deleting` and the error); `Notified` (the rows after `since`, `LIMIT
   MailboxNoticesMax`). Graph's errors read by code only; nothing of an answer logged.
   `graphtest` learns `POST /subscriptions` (calling a validation hook the test sets, so
   the handshake can be simulated failing), `PATCH` and `DELETE /subscriptions/{id}`,
   `GET /subscriptions`, 409 on a duplicate resource, 403 per mailbox.
10. **Entra, the endpoint** (`internal/entra/mailbox_notifications_endpoint.go`): O11.5 —
    the handshake (the window read from the connection row; the token ≤ 1024 bytes, no
    control character; 200 `text/plain; charset=utf-8`, `X-Content-Type-Options:
    nosniff`, `Cache-Control: no-store`; else 404 empty); a notification (decode through
    the 1 MiB cap, at most 1000 items, each checked — GUID, row, constant-time hash
    compare with `crypto/subtle`, tenant id equal, case-insensitively, to the
    connection's — and marked; a context deadline of 2 s on the writes; 202 / 400 / 503;
    counts at debug, ignored items counted at warning, no ids). The rate limit
    `entra-mailbox-notifications`, 600 per client a minute, in `limits` with its reason.
    `GET /connection`'s `calendarSync.notifications` (O11.7) in `status.go`.
11. **The calendar** (`internal/calendar`): accessors `mailboxWatch`, `mailboxUnwatch`,
    `mailboxNotified` in `contractscalls.go` (the lock rule as the others); in
    `mailbox_worker.go` — `Watch` last in a sync that left the person `active` (error
    logged by code, nothing else changes); `Unwatch` in `cleanup` for every reason but a
    target change (gone/unknown forget; denied, unavailable or failure keep the clean-up
    owed as the other steps); `RunCycle` asks `Notified(since)` before picking the due,
    `since` held on the worker (initially `now - poll`), advanced to the latest `At`
    taken, each notice through `MarkMailboxSyncNotified` in a short statement, a failure
    logged and the cycle going on.
12. **The frontend** (`apps/entra/frontend`): under *Calendar sync* on the Entra page, the
    notifications' state — *Change notifications*: on (with the number of mailboxes and
    the last notification), off, not configured ("set `PUBLIC_BASE_URL`"), unreachable
    ("Microsoft could not reach … since …") — in `en` and `nb`; tests. The calendar's card
    is unchanged.

## Tasks

- [ ] **1. The research, the spec's O11 and this plan.** Committed first, no
  `Docs-Impact` trailer.
- [ ] **2. The contract, the migration, the queries, the config, the public host, the API,
  the fakes and the stubs (one worker, alone).** Readings 1–8; `sqlc generate` (entra,
  calendar); `go generate ./...` in `apps/server`; `bun run gen:client`;
  `go build ./... && go vet ./...` and the full `server:test`.
- [ ] **3a. Entra (`internal/entra`), after 2.** Readings 9–10 with tests against
  `graphtest`: create (the body: resource, change types, both URLs, the expiry under the
  limit, a 43-character clientState never stored plain), nothing while live, renew within
  three days and on `reauthorize`, 404 on renew → create, 409 → listed, ours deleted,
  created; the handshake failing → unreachable, an hour held off; 403, gone, unknown,
  cleanup refused; `Unwatch` (expired forgotten unasked, 204, 404, 403 kept `deleting`);
  `Notified`; the endpoint (the handshake inside and outside the window, a long or
  control-character token, a valid notification marking, the flood coalesced, a wrong
  clientState, an unknown id, a bad GUID, another tenant, a lifecycle of each kind, a body
  too large or not JSON, 1000 items, the answer the same for valid and invalid); the
  status's four states.
- [ ] **3b. The calendar (`internal/calendar`), after 2.** Reading 11 with tests against
  the fake: watched after a sync that worked, not after a refusal or failure, a `Watch`
  error hiding nothing; unwatched by each clean-up (toggle off, access lost, account gone,
  mailbox moved — the old one, the admin switch off) and not by a target change; an
  `Unwatch` denied keeping the clean-up owed, gone forgetting; a notice making the right
  rows due and not one synced after it, nor a row off, nor one of another mailbox; the
  lock rule for the three accessors.
- [ ] **3c. The frontend, after 2.** Reading 12.
- [ ] **4. Documentation, after 3.**
  - `reference/entra.md`: the mailbox calendar's `Watch`, `Unwatch`, `Notified`, their
    Graph calls and refusals; the subscription table and the connection's columns; the
    endpoint (the handshake, the checks, the answers, the limit, the logs); the
    status's `notifications`.
  - `reference/calendar.md`: *Outlook sync* — notifications (the nudge, `Watch` after a
    sync, `Unwatch` in the clean-up, `Notified` each cycle), the config.
  - `contributing/module-boundaries.md`: rule 14 — the three calls.
  - `admin/entra.md` (en, nb): change notifications — what they need (`PUBLIC_BASE_URL`
    reachable by Microsoft over HTTPS, the path on the public host), what the Entra page
    shows, turning them off, what can go wrong (unreachable, RBAC, the poll still runs).
  - `admin/calendar.md` (en, nb): `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED`; a change in
    Outlook arriving within a minute with notifications, the poll otherwise.
  - `admin/installation.md` (en, nb): the variable, and the public host now admitting
    Microsoft's webhook path.
  - `admin/transport-security.md` (en, nb) if it lists the public host's paths.
  - `user/entra.md` (en, nb): the Entra page's new line under *Calendar sync*.
  - `ROADMAP.md`: D done — phase 2 complete.
- [ ] **5. Review, then verification.** A security review (read-only) of the endpoint, the
  provider and the public host, a docs pass against the code, one fix batch; then
  `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`; push; the pull request with the real-tenant checklist.
