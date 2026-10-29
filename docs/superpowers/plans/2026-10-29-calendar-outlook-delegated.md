# Outlook connection, delivery B — the calendar on the person's connection: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person who has connected Outlook (delivery A) has their calendar copies
written, read back and watched with **their own delegated token**; the application
permission `Calendars.ReadWrite` and its Exchange RBAC procedure are gone. A person who
has not connected, or whose connection Microsoft refused, is **parked** — their card says
*Connect Outlook* or *Reconnect Outlook*, the worker asks again once a day and never
backs off — and is **woken within a cycle** when they connect. A disconnect, a leaver, an
account closed on use and the switch turned off all reach the calendar's clean-up through
the connection's move to `closing`; a leaver's copies, whose token is forgotten first,
stay in the disabled mailbox (owner's Q2).

**Architecture:** Entra's `mailboxSession` carries a `tokenSource` in place of the
application credential — the person's `delegatedSource` (A's `outlook_token.go`) found by
the mailbox's object id, asked for as a **removal** on the calls the provider already
allows under cleanup — and its markers take the connection's client id. The contract
gains `ErrConnectionNeeded` with a reason, `ConnectionChange` and `Connections(ctx,
since)`, read from `entra.outlook_connections.changed_at` through A's index, no network.
The calendar's worker maps `ErrConnectionNeeded` to a person-level stop (24 h, attempts
unchanged), runs its clean-up when the connection is gone, and reads `Connections` in
`takeNotices` beside `Notified`. Branch `claude/outlook-calendar-delegated`, cut from
delivery A (vantigo-io/vantigo#195, head `e84f5017`). **No migration.**

**Spec:** [`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`](../specs/2026-10-29-outlook-connection-and-mail-design.md), M9, M2's removals, the calendar's part of M5–M7, Q2; A's *as built* note.
**Research:** [`…/research/2026-10-29-outlook-connection-and-mail.md`](../research/2026-10-29-outlook-connection-and-mail.md) §3 (*Refresh tokens*, *Change notifications and delta, delegated*).
**Delivery A's plan:** [`2026-10-29-entra-outlook-connection.md`](2026-10-29-entra-outlook-connection.md), reading 25 (*What A leaves to B*).

## Plan readings

1. **Base, the neighbour PRs and when B opens.** `main` holds #194 (`312ed5e5`, `88f4e679`):
   migration `00087_calendar_mailbox_sync_claim.sql`, `ClaimDueMailboxSync`
   (`calendar/queries/mailbox_sync.sql:41-60`) claiming one row at a time with
   `claimed_until`, `ReleaseMailboxSyncClaim`, and `takeNotices` run before any claim,
   holding no connection (`mailbox_worker.go:267-268`, `:337-384`). A (#195, open)
   holds `00088` and `00089`. B is cut from A's head and **its pull request opens only
   after #195 merges**: then B rebases onto `main` (memory: *PRs land by rebase*;
   *Stacked PRs per phase* — never against A's branch). A and B ship in **one release**
   (M2): A's `main` holds a connection the calendar does not use, and the A-only
   `outlookConnect.status = permission_missing` advisory that B removes. The spec names
   the branch `feat/calendar-outlook-delegated`; this one is the session's.

2. **No migration — confirmed.** Everything B reads or writes already exists:
   - `Connections` reads `changed_at` through `ix_entra_outlook_connections_changed`
     (`00089_entra_outlook_connections.sql:43`), added by A for exactly this (A's plan
     reading 4).
   - The calendar's new codes `not_connected` and `reconnect` fit
     `calendar.mailbox_sync.error varchar(40)` (`00081_calendar_mailbox_sync.sql:38`),
     which has no CHECK (house style); the state stays `unavailable`.
   - Parking writes `next_sync_at` and leaves `attempts` alone; waking writes
     `next_sync_at` — both existing columns (`00081`, `00087`).
   - A connection's reason is not stored by the calendar (reading 16), so no column.
   Were one truly needed, the next free number is `00090`, which the spec gives C
   (`00090_entra_mail_channels.sql`); whichever lands second renumbers in its own
   history.

3. **What A built and left unwired.** `outlook_token.go:36-38` says it: "Nothing calls it
   in production until delivery B wires it into the mailbox calls."
   - `graph.go:371-386` — `tokenSource {token, dropped, challenged, tenant}`;
     `credentialSource` (`:388-399`) is today's application path exactly;
     `getAs`/`doAs`/`sendAs` (`:401-404`, `:420-430`, `:445-519`) take a source, every
     401 handed to `challenged` before the one retry (`:502-511`).
   - `outlookTokens.issue` (`outlook_token.go:220-258`): no row → `not_connected`;
     `reconnect` → its `error_code`; no token, `closing` without `removal`, or **the
     switch off without `removal`** → `closing` (`:239`); an `active` row's account
     checked on use (`checkAccount`, `:265-289`), closing it revision-guarded with its
     token forgotten (`disabled`/`deleted`). A refused refresh of a `closing` row forgets
     its token (`reconnect`, `:383-421`, `ForgetClosingOutlookRefresh`).
   - `delegatedSource` (`:423-462`): `challenged` turns a CAE claims challenge into a
     `reconnect` with `sign_in_again`, no retry.
   - `connectionNeeded{reason}` (`:40-55`) is Entra's own error; "Delivery B wraps it in
     `contracts.ErrConnectionNeeded`".
   - **The daily account read is already A's** (A's *as built*, M6): `entra-outlook-sweep`
     reads `accountEnabled` with the application token for the rows in `reconnect`
     (`outlook_sweep.go:155-221`, `ListOutlookAccountChecks`), closing a disabled one with
     its token forgotten. *Spec refinement:* M6 put that read "on the calendar's long
     re-check"; it runs in Entra's own worker, so the calendar's re-check only has to
     **notice** the close — which `Connections` wakes it for (reading 19).

4. **The contract** (`internal/contracts/mailbox_calendar.go`). Added, nothing else
   changed (M9):
   ```go
   // ErrConnectionNeeded: the person's own connection to the provider cannot serve
   // the call until they connect (again). errors.As to *ConnectionNeededError for why.
   var ErrConnectionNeeded = errors.New("contracts: the person's connection is needed")

   // ConnectionNeededError is ErrConnectionNeeded with its reason.
   type ConnectionNeededError struct{ Reason string }
   func (e *ConnectionNeededError) Error() string
   func (e *ConnectionNeededError) Is(target error) bool { return target == ErrConnectionNeeded }

   // The reasons: the person never connected or their connection is gone, it is
   // closing, or one of the reconnect codes the provider names.
   const (
       ConnectionNotConnected = "not_connected"
       ConnectionClosingReason = "closing"
   )

   // ConnectionChange is one connection that moved: its mailbox, the state it
   // moved to, and when.
   type ConnectionChange struct {
       Mailbox string    // the DirectoryAccount.ExternalID, as the provider holds it
       State   string    // ConnectionActive | ConnectionReconnect | ConnectionClosing
       At      time.Time
   }
   const ConnectionChangesMax = 5000

   // in MailboxCalendar:
   // Connections answers the people's connections that moved after since, oldest
   // first, at most ConnectionChangesMax: the provider's own rows, no network.
   Connections(ctx context.Context, since time.Time) ([]ConnectionChange, error)
   ```
   *Plan decision:* `At` and the cap are **added** to the spec's `{Mailbox, State}`, so
   the worker moves a cursor exactly as it does for `MailboxNotice` (`:167-174`) — a
   full page followed by the next, an overlap for a change committed late. The
   interface's comment (`:38-162`) gains the rule for every per-mailbox call: it may
   answer `ErrConnectionNeeded`; **removals** — `DeleteCalendar`, `Find`, `Unwatch`, and
   `Apply` when every op is a delete, the calls already allowed under `MailboxCleanup` —
   are served by a `closing` connection while it holds its token, and answer
   `ErrMailboxGone` once nothing can remove them (reading 8). `ErrMailboxDenied`'s comment
   (`:394-396`) stops saying "outside the provider's permission scope" only: a mailbox
   Exchange refuses (one without Exchange Online). The errors' block (`:385-408`) lists
   `ErrConnectionNeeded`.

5. **`modtest.FakeMailboxCalendar`** (`modtest/mailbox_calendar.go:92-146`, `:868-946`):
   - `SetConnection(mailbox, reason string)` — every call on that mailbox answers
     `&contracts.ConnectionNeededError{Reason: reason}`, except removals when `reason ==
     "closing"` and the fake was told the token is held (`SetConnectionToken(mailbox,
     held bool)`); a removal with `closing` and no token, or `not_connected`, answers
     `ErrMailboxGone`. `""` clears it. Removals are judged as reading 7's table.
   - `ChangeConnection(mailbox, state string, at time.Time)` records a change;
     `Connections(ctx, since)` answers those after `since`, oldest first, capped, as
     `Notified` (`:894-915`) does; `FailWith("Connections", err)` joins the method list
     (`:257`).
   - `Calls()` records `Connections` like the rest. `mailbox_calendar_test.go` covers
     the three answers and the cap.

6. **Entra's error becomes the contract's.** `connectionNeeded` (`outlook_token.go:40-49`)
   is replaced by `contracts.ConnectionNeededError` — a constructor `needsPerson(reason)
   error`, so every `&connectionNeeded{reason: …}` (`:223`, `:238`, `:240`, `:288`,
   `:410`, `:414`, `:420`) reads the same. `outlookNotConnected`/`outlookNeedsClosing`
   (`:52-55`) become the contract's two constants. The internal tests that
   `errors.As` to `*connectionNeeded` (`outlook_token_internal_test.go`,
   `outlook_token_test.go:312-530`) read `.Reason`. `OutlookToken` in `export_test.go:12-17`
   is unchanged.

7. **The session's token** (`mailbox.go:176-256`).
   - `mailboxSession` (`:178-183`) loses `cred graphCredential` and gains `src
     tokenSource`, `clientID` and `tenantID uuid.UUID` (the connection's, read once in
     `checked`). `checked` (`:216-244`) reads the connection beside the status — `Status`
     (`:127-138`) is split into `state(ctx) (store.EntraConnection, contracts.MailboxStatus,
     error)` and `Status`, which answers the second; shape and logic unchanged (reading 9).
   - `open(ctx, mailbox, cleanupAllowed)` (`:187-196`) becomes `open(ctx, mailbox,
     cleanupAllowed, removal bool)`; `withCredential` (`:199-209`) becomes
     `withToken(ctx, &s, removal)`, setting `s.src = m.tokens.mailboxSource(s.oid, removal)`
     — no network until the first call. `mailboxCalendar` (`:109-112`) gains `tokens
     *outlookTokens`, built in `newMailboxCalendar` (`:118-120`) on the **same**
     `graphClient` (`outlookTokens{c, q: store.New(d.Pool), pool: d.Pool}`).
   - Every `m.client.get/post/del/do(ctx, s.cred, …)` becomes the `…As(ctx, s.src, …)`
     form: `mailbox.go:376, 397, 423, 455, 464, 723, 842`; `mailbox_delta.go:140, 315`;
     `mailbox_writeback.go:135, 400`; `mailbox_notifications.go:208, 234, 297, 390, 411,
     465`. `graph.go` gains `postAs` and `delAs` beside `getAs` (one line each over
     `doAs`). Log lines' `"tenant", s.cred.tenantID` (`mailbox.go:787`,
     `mailbox_delta.go:161, 353`, `mailbox_writeback.go:160, 360, 450`,
     `mailbox_notifications.go:304, 319`) read `s.tenantID`.
   - **The markers take the connection's client id** (M9): `keyPrefix`/`calendarMark`
     (`:248-256`) read `s.clientID`. That is the value `cred.clientID` carried —
     `pickCredential` builds the credential from `conn.ClientID`
     (`credentials.go:93-105`) — so a marker written before B still matches.
   - *Which call is a removal* (the `removal` argument; the spec says "removals" without
     a list):

     | Call | `cleanupAllowed` today | `removal` |
     | --- | --- | --- |
     | `EnsureCalendar` (`:362`) | no | no |
     | `DeleteCalendar` (`:446`) | yes | yes |
     | `Apply` (`:623`) | yes (deletes only under cleanup, `:633-636`) | yes iff every op is `MailboxOpDelete` |
     | `Find` (`:809`) | yes | yes |
     | `Delta`, `Marked` (`mailbox_delta.go:110`, `:267`) | no | no |
     | `Versions`, `Events` (`mailbox_writeback.go:91`, `:329`) | no | no |
     | `Watch` (`mailbox_notifications.go:170`) | no | no |
     | `Unwatch` (`:430`) | yes | yes |

     `Find` serves the clean-up's enumeration (`cleanupEvents`, `meetingsIn` with no
     bound) and the reconcile alike; it is safe as a removal because **a sync's first
     provider call is never one**: `EnsureCalendar` for target `own`
     (`calendar/mailbox_worker.go:472-479`) or `Versions`, always called first in
     `writeBack` (`calendar/mailbox_writeback.go:131`). So a sync whose connection is
     not `active` parks before it reaches `Find`, and removal semantics reach only a
     clean-up.

8. **The token's errors as the contract answers them** — `mailboxSource` (in
   `mailbox.go`) wraps `delegatedSource`, overriding `token` and `challenged`:
   - `ConnectionNeededError` passes as it is (it `Is` `ErrConnectionNeeded`), **except
     for a removal whose reason is `closing` or `not_connected`**: then
     `fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)`. `closing` reaches a removal
     only when the row holds no token (`issue`, `:239`): a leaver or a deleted account
     (forgotten at the close, A's M7), an account closed on use, or a closing token
     Microsoft refused (`:393-398`). `not_connected` is no row: the sweep deleted it, or
     the person never connected. *Plan decision:* the spec's M7 names `ErrMailboxGone`
     for the leaver; the no-row case gets the same, since nothing can ever remove those
     copies — the calendar then forgets them (`calendar/mailbox_worker.go:878-881`), its
     O8 rule for a mailbox it cannot reach.
   - `errNotConfigured`/`errNoCredential` from the refresh's `credentialFor`
     (`outlook_token.go:295-298`) → `ErrMailboxUnavailable`, as `withCredential` maps
     them today (`mailbox.go:201-203`). `errCredentialInvalid` and transient errors pass:
     a failure to retry.
   - The refresh still calls `credentialFor`, which keeps the application token's cache
     warm (`credentials.go:104-113`); that token is used for **no** mailbox call. The
     application credential stays for the account check, the sweep and provisioning.

9. **`Status` is unchanged** (M9): `mailboxStatusOf` (`mailbox.go:154-174`) keeps its
   three cases. Only its comment changes: `:140-153` explains `available` by the RBAC
   advisory; it now says the switch on and the connection connected make the provider
   available, and **each person's call** answers `ErrConnectionNeeded` when their own
   connection cannot serve it. Under `cleanup` (switch off), `issue` already serves
   removals alone (`outlook_token.go:217-219`, `:239`), matching `Apply`'s gate — and
   the switch-off closed every live row keeping its token (`closeAllOutlook`,
   `outlook_close.go:66-81`), so the clean-up of everyone runs on each person's token.
   The file's header (`:23-56`) says the calls run on the person's delegated token,
   never the application's.

10. **Subscriptions on the person's token** (M9; `mailbox_notifications.go`).
    - `Watch` (`:170-223`), `renew` (`:227-261`), `create` (`:271-350`) and
      `clearConflict` (`:378-420`) run with `s.src` from `withToken(…, false)`.
      `GET /subscriptions` with a delegated token lists this application's subscriptions
      for the signed-in user, which is what `clearConflict` needs. A subscription's
      `creatorId` is the person (research §3); a renewal is assumed to need their token
      (research §3: "not stated, so assume it").
    - `reauthorizationRequired` is recorded as today (`mailbox_notifications_endpoint.go:
      51-54`, `:239-241`): the row `reauthorize`. A parked person's sync stops before
      `watch` (`calendar/mailbox_worker.go:579` — `watch` only after a sync that
      pushed), so **the subscription is left to lapse** with no new code. On a reconnect,
      `Connections` wakes the row (reading 19), the sync works and `Watch` renews a held
      `reauthorize` row (the PATCH reauthorizes) or creates one — D's own rule. The
      webhook, `lifecycleNotificationUrl` (`:291`) and the checks are unchanged.
    - `Unwatch` (`:430-476`) takes `withToken(…, true)`. *Plan decision:* when its token
      is gone for good (reading 8's `ErrMailboxGone`), it **forgets the subscription row
      and answers nil** — Graph drops a subscription whose creator's grant ended, and
      its notifications would name no row. Without this, a leaver's
      `entra.mailbox_subscriptions` row would stay for ever and the sweep's `NOT EXISTS`
      (`queries/outlook_connections.sql:152-163`) would hold their `closing` row the full
      seven days.

11. **The fake Graph** (`graphtest`), so the existing tests prove the token:
    - A delegated token reaches **only its own user's** `/users/{id}/…` mailbox routes,
      `$batch` sub-requests included (`graph.go:658` → the route; `mailbox.go:245`):
      another user's answers 403 `ErrorAccessDenied`.
    - *Plan decision:* from B on, an **application** token on a mailbox route —
      `/users/{id}/calendar…`, `/events…`, `calendarView/delta` — or on
      `/subscriptions` answers 403 `ErrorAccessDenied`, so every mailbox test that still
      reached Graph on the application path fails. `GET /users/{id}?$select=…` (the
      sweep's account read, provisioning) stays application.
    - `Subscription` (`subscriptions.go`) records `CreatorID`, the delegated token's
      `oid`; `createSubscription` (`:237`) refuses a delegated token for another user's
      resource (403), as Graph does.
    - `graphtest/mailbox_test.go`, `subscriptions_test.go`: the three refusals and the
      creator.

12. **Entra's tests.** `newMailboxHarness` (`mailbox_test.go:57-65`) stops adding
    `Calendars.ReadWrite` to the roles; it builds A's `outlookFixture`
    (`outlook_connect_test.go:74-91`, with `modtest.WithWorkforce`) and `mailboxUser`
    (`:67-78`) becomes `person` + `connectAs` (`:93-110`, `:189-198`), so each mailbox
    the tests use has an `active` connection made through the real flow. The B–D tests
    (`mailbox_test.go`, `mailbox_delta_test.go`, `mailbox_writeback_test.go`,
    `mailbox_notifications_test.go`) then run **unchanged in what they assert**; a test
    that reads `g.Calls()` for a token type sees the delegated one. New tests are
    listed in task 3.

13. **M2's removals in Entra** (`connection.go`).
    - `permissionCalendarsReadWrite` (`:46-49`) goes. `missingPermissions(present,
      outlookConnect bool)` (`:89-104`) becomes `missingPermissions(present)` — the
      eight, nothing conditional; callers `:201`, `:587`, `:654`.
    - `unexpectedPermissions` (`:106-120`) drops its exception: an application
      `Calendars.ReadWrite` left from an earlier setup is now **unexpected** (M19: "After
      B, a leftover application `Calendars.ReadWrite` consent shows here").
    - `outlookConnectBody` (`:145-185`) loses its `permission_missing` case (`:176-177`);
      the order becomes `off`, `unavailable`, `consent_missing`, `ready`, `untested` —
      the spec's M2 table. *Spec correction:* M2 calls it "the `permission_missing`
      advisory" of `calendarSync`; as built by A it is a value of
      `outlookConnect.status` (A's *as built*, M2), which is what B removes.
    - `openapi/entra.yaml`: `permission_missing` out of `EntraOutlookConnect.status`'s enum
      (`:552`) and its description (`:529`); `permissionsMissing`'s description (`:126`)
      loses "and Calendars.ReadWrite while the Outlook connection switch is on";
      `permissionsUnexpected`'s says the eight alone. `go generate ./...`;
      `bun run gen:client`.
    - `connection_test.go`: the four statuses, `Calendars.ReadWrite` never missing and now
      unexpected, the test's `unexpected:Calendars.ReadWrite` code.

14. **M2's removals in the Entra frontend** (`apps/entra/frontend/src`):
    `api/connection.ts:36-46` — `CALENDAR_PERMISSION` goes, `permissionsToAdd` answers
    `requiredPermissions`; `components/step-card.tsx:17`, `:154-157` — the *(for calendar
    sync)* label; `lib/steps.ts:1-20` — step 3 done when `permissionsMissing` is empty;
    `pages/entra.tsx:260-266` — the step's comment; `components/settings-form.tsx:351` —
    the `permission_missing` colour; `i18n.ts` en `:155-156` (the "nine permissions"
    sentence back to eight, `permissionForCalendarSync` removed), `:223` (the switch's
    description loses "Calendar sync still needs the Calendars.ReadWrite application
    permission."), `:227-228` (`outlookConnectStatus_permission_missing`), nb `:602-603`,
    `:670`, `:674-675`; `pages/entra.test.tsx:425-456`, `:585-586` and `test/server.ts`.
    `bun run translations:check`.

15. **`Connections` in Entra** (`mailbox_notifications.go` beside `Notified`, `:480-492`,
    or a new `mailbox_connections.go`). A query in `queries/outlook_connections.sql`:
    ```sql
    -- name: ListOutlookConnectionChanges :many
    SELECT object_id, state, changed_at FROM entra.outlook_connections
    WHERE changed_at > @since::timestamptz
    ORDER BY changed_at, object_id
    LIMIT @row_limit;
    ```
    through `ix_entra_outlook_connections_changed`; `Mailbox` the object id in lower case
    (as `Notified`). What moves `changed_at` (`queries/outlook_connections.sql`): `Insert`
    (`:27`), `Take` (`:36-54`), `Close` (`:56-72`), `CloseAll` (`:93-102`),
    `MarkReconnect` (`:117-127`). What does not: `StoreOutlookRefresh`, the two
    `Forget…Refresh`, `TouchOutlookUsed`, `MarkOutlookAccountChecked` — a token forgotten
    on a row already `closing` is no new state, and the calendar was woken at the close.
    A row the sweep deletes is not answered: it was `closing` for at least a day first.
    No `Status` check: the rows are answered whatever the switch. `mailbox_test.go`: the
    order, `since` exclusive, the cap, a refresh moving nothing.

16. **The calendar's availability** (`calendar/mailbox_sync.go:24-36`, `:251-302`).
    `mailboxAvailability` is a request's read with no network; the calendar cannot ask
    a person's connection per request (the contract has no such read, and M9 adds
    none). So `not_connected` and `reconnect` are **the worker's last finding**: the
    caller's row `enabled`, `state = unavailable`, `error` one of the two. The order:
    `no_provider`, `switch_off`, `unavailable` (the provider's status), `no_account`,
    then the row's `not_connected`/`reconnect`, else `available`.
    - *Spec departure:* M9 puts the two "before `no_account` is considered". `no_account`
      is a fresh read of Staff and a connection needs a certain account (M6: connect
      answers `no_account`), so `no_account` is the truer answer and comes first.
    - *Spec refinement:* M9 says `reconnect` "with the reason". The card's button is the
      Entra package's (`connection.button` slot, A's reading 24), which reads the
      person's own connection and shows *Reconnect needed* with the reason; the calendar
      neither stores nor shows it.
    - *Plan decision:* `PutCalendarMailboxSync` (`:149-158`) and
      `PostCalendarMailboxSyncRun` (`:222-233`) accept `available`, `not_connected` and
      `reconnect`: a parked person may change their setting, and **Sync now** right after
      connecting asks at once instead of waiting a cycle.
    - `permission_missing` leaves the enum and the file's comment (`:33-36`).

17. **Parking** (`calendar/mailbox_worker.go`).
    - `mailboxRetryConnection = 24 * time.Hour` beside `mailboxRetryUnavailable` and
      `mailboxRetryRefused` (`:175-179`); codes `mailboxErrNotConnected = "not_connected"`,
      `mailboxErrReconnect = "reconnect"` (`:194-200`).
    - `mailboxRefusal` (`:1032-1035`), `mailboxRefused` (`:1064-1067`) and
      `mailboxErrorCode` (`:1120-1139`) know `ErrConnectionNeeded` (the log code:
      `not_connected`/`reconnect`). A reason `not_connected` or `closing` is
      `not_connected`; any other is `reconnect`.
    - `parked(ctx, call, r, now, err)`: state `unavailable` (`stopping` kept when a
      clean-up is owed, as `failed` keeps it, `:1103-1111`), the code, `next_sync_at = now
      + 24 h`, **`attempts: r.Attempts`** (not raised), `reconcile: true`; logged at info
      by the code. `refusedOrFailed` (`:1041-1061`) gains `call` and sends
      `ErrConnectionNeeded` to `parked` first; `failed` (`:1091-1116`) does too, for one
      that came out of a clean-up. Every step's error already reaches one of the two:
      `mailboxEnsure`, the write-back's refusal (`:520-522`), `Find`, `Apply`'s whole-call
      error (`apply`, `:780-785`, through `mailboxRefusal`), the busy read (`:562-564`).
    - **The long re-check** is the next sync 24 h later, which runs the cycle's steps in
      order before any provider call (`syncPerson`, `:399-479`): the status (switch off →
      clean-up of everyone), `calendar:access` (lost → clean-up), `DirectoryAccount`
      (none → clean-up, `no_account`; another mailbox → clean-up of the old), then the
      first provider call, which parks again or syncs. It is not the backoff (`:182-184`)
      and not the 6-hour `mailbox_denied` retry.

18. **The clean-up on a connection gone** (M7, O8).
    - `parked` runs `cleanup(ctx, call, &r, now, true)` (`:852-912`) **before** it writes,
      when the reason is `not_connected` or `closing`, the row was not already parked
      `not_connected` (its `error`), and the person has had copies (`last_synced_at`, a
      `calendar_id` or a map row) — *plan decision*: a person who never synced owes no
      clean-up, and a parked one already did theirs. A clean-up that fails is `failed`
      (the backoff), and the next sync parks again and retries it; one answered
      `ErrConnectionNeeded` (a reconnect met during a clean-up) is parked with the
      clean-up owed.
    - **A disconnect** (`closing`, token kept): the sync's first call (non-removal) answers
      `closing` → the clean-up's `Find`, `Apply` deletes, `DeleteCalendar` and `Unwatch`
      run on the person's token (removals) → the map emptied → parked `not_connected`.
    - **A leaver** (A: Deactivate closes with `@forget`, `lifecycle_deactivate.go`): the
      calendar is woken by the `closing` change; `calendar:access` or the account is
      usually gone first, so `syncPerson` goes straight to the clean-up; its first
      removal finds no token → `ErrMailboxGone` → the copies **forgotten unremoved** and
      logged (`:878-881`), `Unwatch` forgetting the subscription row (reading 10). The
      copies stay in the disabled mailbox (Q2).
    - **An account closed on use** or by the sweep's daily read: the same as the leaver.
    - **The switch off**: `Status` is `cleanup`, so every person's sync takes the
      `cannotGoOn` clean-up (`:421-445`) on their own `closing` token. Back on, every row
      parks `not_connected` at its next sync (its connection is `closing`) until the
      person connects again (M7).
    - **A reconnect** keeps the map and the copies (stale until the person reconnects; the
      token cannot remove them anyway). A person who turns their toggle off while waiting
      to reconnect is parked `stopping`: the clean-up runs when they reconnect, or is
      forgotten once the sweep closes a disabled account.

19. **Waking** (`takeNotices`, `:324-384`). After `Notified`, the same step asks
    `mailboxConnections(ctx, since)` — a new accessor in `contractscalls.go` beside
    `mailboxNotified` (`:413-425`), `noteContractCall(ctx, "MailboxCalendar.Connections")`
    — with its own cursor on the worker (`connectionsSince`, `connectionsFull`, under
    `noticesMu`; `:207-217`), the same 30-second overlap and full-page rule, and marks in
    one statement:
    ```sql
    -- name: MarkMailboxSyncConnected :execrows
    UPDATE calendar.mailbox_sync s SET next_sync_at = @now::timestamptz
    FROM (SELECT lower(c.mailbox) AS mailbox, max(c.at) AS at
          FROM (SELECT unnest(@mailboxes::text[]) AS mailbox, unnest(@ats::timestamptz[]) AS at) c
          GROUP BY lower(c.mailbox)) AS change
    WHERE lower(s.mailbox) = change.mailbox
      AND (s.enabled OR s.state = 'stopping')
      AND s.updated_at < change.at
      AND (s.next_sync_at IS NULL OR s.next_sync_at > @now::timestamptz);
    ```
    *Plan decision:* unlike a notice (`MarkMailboxSyncNotified`, `:190-211`), a change
    **does override the backoff and the parking** (no `attempts = 0`, no state filter): it
    changes what the next sync can do, and changes are per person and rare. `updated_at <
    at` keeps a change taken twice from marking a row written since. A failing
    `Connections` is logged by its code and the cycle goes on, as `Notified`'s
    (`:351-355`). Still before any claim, holding no connection (#194's rule).

20. **The toggle on without a connection** (M9): a person with no parked row is
    `available` at request time, so the PUT writes `pending`; the first cycle parks it
    `not_connected` (no clean-up: never synced); the card says so and the slot's
    **Connect Outlook** is right above; `complete` moves the row `active`, `Connections`
    wakes the sync within a cycle.

21. **The calendar frontend** (`apps/calendar/frontend/src`).
    - `pages/settings.tsx` `OutlookSection` (`:279-370`): `canRun = availability ∈
      {available, not_connected, reconnect}` replaces `available` for the toggle (`:327`,
      `:351`) and **Sync now**; for the two new values the alert is orange, not gray, and
      names the button above it. *Plan decision:* while the availability is one of the
      two, the `mailboxSyncQueryOptions` query refetches every 15 seconds, so the card
      turns to *Waiting for the first sync…* within the cycle after a connect without a
      reload. The `connectionButton` slot is A's, unchanged (`:344`).
    - `i18n.ts`: `outlook.availability_not_connected` ("Connect Outlook above to start the
      sync." / "Koble til Outlook ovenfor for å starte synkroniseringen."),
      `outlook.availability_reconnect` ("Microsoft wants you to sign in again: choose
      Reconnect Outlook above." / "Microsoft vil at du logger på igjen: velg Koble til
      Outlook på nytt ovenfor."), `outlook.error_not_connected`, `outlook.error_reconnect`
      (the same words); `availability_permission_missing` removed (en `:313-314`, nb
      `:667-668`); `availability_switch_off` names the *Outlook connection* (en `:311`, nb
      `:665`).
    - `pages/settings.test.tsx:269`: the `permission_missing` row goes; rows for the two
      values in both languages, the toggle enabled while parked, the refetch.

22. **The calendar contract** (`openapi/calendar.yaml:691-722`): `MailboxSync.availability`
    — `permission_missing` out, `not_connected` and `reconnect` in; `error` gains the
    same two; the description (`:692`) rewritten — `switch_off` "an Owner has not turned
    on the Outlook connection in the Entra module", the two new values ("the caller has
    not connected Outlook, or their connection closed"; "Microsoft refused the caller's
    connection; they reconnect"), the RBAC sentence gone. `go generate ./...`;
    `bun run gen:client`.

23. **Docs.** `docs:check`'s map puts `internal/contracts` and `internal/modtest` under
    `contributing/module-boundaries.md`; `apps/entra/frontend` under `user/entra.md` and
    `user/connections.md` (en, nb); `apps/calendar/frontend` under `user/calendar.md` (en,
    nb); `internal/entra` and `internal/calendar` under their references. The pages and
    their changes are task 6's list; `admin/installation.md` needs none (no new worker,
    setting or route).

## Tasks

- [ ] **1. This plan.** Committed alone, no `Docs-Impact` trailer:
  `docs(calendar, entra): delivery B's plan — the calendar on the person's connection: ErrConnectionNeeded and Connections in the contract, the mailbox calls on the delegated token with removals on a closing one, subscriptions the person's, parking a day and waking by Connections, the clean-up when the connection is gone, Calendars.ReadWrite and its RBAC procedure removed`.

- [ ] **2. The contract and Entra's `Connections` (one worker, alone).** Readings 4–6, 15.
  - [ ] Failing tests first: `modtest/mailbox_calendar_test.go` (the fake's three
    answers, the removal rule, `Connections` ordered and capped); `entra/mailbox_test.go`
    — `Connections` over A's flow (a connect, a disconnect, a reconnect each answered
    once, in order; `since` exclusive; a refresh moving nothing; the cap).
  - [ ] `contracts/mailbox_calendar.go` (reading 4), the fake (reading 5),
    `ListOutlookConnectionChanges` and `sqlc generate -f internal/entra/sqlc.yaml`,
    `mailboxCalendar.Connections`; `connectionNeeded` replaced by the contract's error
    (reading 6) with its tests adjusted.
  - [ ] `go build ./... && go vet ./...`, `mise run server:test`.
  - [ ] Commit (no trailer; the docs land in task 6):
    `feat(calendar, entra): the connection's contract — ErrConnectionNeeded with its reason, ConnectionChange and Connections, read from entra.outlook_connections.changed_at with no network; removals served by a closing connection while it holds its token, ErrMailboxGone once none can; Entra's connectionNeeded becomes the contract's; modtest's fake learns both`.

- [ ] **3. Entra: the mailbox on the person's token, and M2's removals, after 2.**
  Readings 7–14.
  - [ ] The fake Graph first (reading 11) with its tests.
  - [ ] Failing tests: `newMailboxHarness`/`mailboxUser` on real connections (reading 12),
    every existing mailbox test green against the delegated source and none reaching
    Graph with the application token. New, in `mailbox_connection_test.go`: each call
    answering `ErrConnectionNeeded` with `not_connected` (no row), `closing` (disconnected,
    non-removal), the reconnect codes (`mfa` after `RefuseRefresh`, `sign_in_again` after
    `ChallengeNext`); removals on a disconnected connection deleting with its token
    (`Apply` of deletes, `DeleteCalendar`, `Find`, `Unwatch`) while an `Apply` with a
    create is refused whole; a leaver's (Deactivate) and a refused closing token's
    removals answering `ErrMailboxGone`, `Unwatch` forgetting the row and the sweep then
    deleting the closing row after a day; the switch off — `Status` `cleanup`, removals
    on every closed row, nothing else; markers read from the connection's client id
    (`Find` sees a copy written before B); a subscription created and renewed with the
    person's token (`CreatorID`), a `reauthorizationRequired` on a row in `reconnect`
    left alone by a parked person and renewed by the `Watch` after a reconnect;
    `errNoCredential` from the refresh answered `ErrMailboxUnavailable`.
    `connection_test.go` (reading 13); `access_test.go` unchanged.
  - [ ] Implement readings 7–10 and 13; `go generate ./...`; `bun run gen:client`.
  - [ ] The Entra frontend's removals (reading 14), tests first; `mise run frontend:check`.
  - [ ] `go vet`, `mise run server:test`.
  - [ ] Commits:
    `feat(entra): the mailbox calendar on the person's own connection — every call through their delegated token, found by the mailbox's object id, removals allowed on a closing connection while it holds its token and ErrMailboxGone once none can; the markers on the connection's client id; subscriptions created, renewed and deleted with the person's token, one whose token is gone forgotten; graphtest refusing the application token on a mailbox and a person's token on another's`
    and
    `feat(entra, entra-ui): Calendars.ReadWrite (application) removed — the eight permissions alone, a leftover grant now a permission Vantigo does not use, outlookConnect's permission_missing gone, step 3's ninth permission and its label; both languages`.

- [ ] **4. The calendar: parking, waking and the clean-up, after 2 (may run beside 3
  against the fake).** Readings 16–20, 22.
  - [ ] Failing tests: `mailbox_connection_test.go` (new, against `FakeMailboxCalendar`):
    a sync answered `not_connected` parked 24 h with `attempts` unchanged and the card's
    availability `not_connected`; `reconnect` likewise, the map kept; a parked person
    claimed again only after 24 h, and woken at the next cycle by a `ChangeConnection`
    after `complete`; a change taken twice marking nothing the second time; a change
    overriding a backoff; a failing `Connections` logged and the cycle going on; a
    disconnect (`closing`, token held) cleaning up with removals then parked, and not
    cleaning up again on the next re-check; a leaver (`closing`, no token) forgetting the
    copies unremoved; the long re-check cleaning up a person whose account was disabled;
    the toggle turned on without a connection (pending → parked, no clean-up); the toggle
    turned off while waiting to reconnect (parked `stopping`, cleaned up once the change
    arrives); the switch off cleaning up everyone; PUT and Sync now accepted while
    parked. `mailbox_sync_test.go`: the availability's order and values, `no_account`
    before the row's codes. The B–D worker tests run unchanged.
  - [ ] `openapi/calendar.yaml` (reading 22), `go generate ./...`; the query (reading 19),
    `sqlc generate`; the accessor; the worker (readings 17–19); `mailbox_sync.go`
    (reading 16).
  - [ ] `go vet`, `mise run server:test`.
  - [ ] Commit:
    `feat(calendar): the Outlook sync on the person's connection — ErrConnectionNeeded a person-level stop, parked a day with attempts unchanged as not_connected or reconnect; woken within a cycle by Connections beside the notices; the clean-up run when the connection is gone, a leaver's copies forgotten in their disabled mailbox; turned on without a connection, it waits for one; PUT and Sync now taken while parked; permission_missing gone from the availability`.

- [ ] **5. The calendar frontend, after 4's contract.** Reading 21.
  - [ ] Failing tests first (`pages/settings.test.tsx`, both languages), then the code;
    `bun run gen:client`, `bun run translations:check`.
  - [ ] `mise run frontend:check`.
  - [ ] Commit:
    `feat(calendar-ui): the Outlook card waits for the connection — Connect Outlook or Reconnect Outlook named above the toggle, which stays usable, the card refreshed until the first sync; permission_missing's sentence gone; both languages`.

- [ ] **6. Documentation, after 3–5.** English first, then Norwegian bokmål page for page.
  - [ ] `reference/entra.md` (en): *Permissions* (`:190`, `:332-366`) — the eight, no
    conditional ninth, `Calendars.ReadWrite` among those not used; *The Outlook
    connection* (`:1314`) — `outlookConnect`'s status table without `permission_missing`;
    *The delegated token* (`:1507`) — used by every mailbox call, removals on a closing
    connection, `ErrConnectionNeeded` and its reasons, `ErrMailboxGone` when no token is
    left; *entra-outlook-sweep* (`:1571`) — a subscription row forgotten by `Unwatch` when
    the token is gone; *The mailbox calendar* (`:1589`) — the token source, the removal
    table, the markers on the connection's client id, `Connections` and what moves
    `changed_at`, the application permission gone; *Change notifications* (`:1838`) — the
    person's token, `creatorId`, `reauthorizationRequired` left to lapse while they wait
    to reconnect.
  - [ ] `reference/calendar.md` (en): *Outlook sync* (`:1600`) — on the person's
    connection; the availability table (`:1862-1876`) with `not_connected` and
    `reconnect` as the worker's last finding after `no_account`, the `permission_missing`
    paragraph gone; *The mailbox sync worker* (`:1920`) — parking (24 h, attempts
    unchanged, the long re-check's steps), waking by `Connections` in `takeNotices` and
    its query; *The clean-up* (`:2330`) — run when the connection is gone, a leaver's
    copies forgotten; *Change notifications* (`:2299`) — the subscription lapses while a
    person waits.
  - [ ] `contributing/module-boundaries.md` (en): rule 14 (`:565-600`) — the provider acts
    on **the person's own delegated connection**; removals allowed on a closing one;
    `ErrConnectionNeeded` among the sentinels with its reason; `Connections` beside
    `Notified`, no network; `ErrMailboxDenied` no longer an RBAC scope.
  - [ ] `user/calendar.md` (en, nb): *Sync with Outlook* (`:438-470`) — connect Outlook
    first, with the card's button; the toggle works before you connect and waits; the
    two sentences; *Reconnect*; disconnecting removes the copies; the "does not use the
    connection yet" sentence (`:465-467`) gone.
  - [ ] `user/connections.md` (en, nb): what uses the connection — the calendar's Outlook
    sync (`:14`); disconnecting removes your Outlook copies (`:63`); a reconnect pauses
    the sync.
  - [ ] `user/entra.md` (en, nb): step 3's eight permissions, no *(for calendar sync)*
    (`:130`); the Outlook connection's paragraph (`:210-236`) without the application
    permission, RBAC and the `permission_missing` line.
  - [ ] `admin/entra.md` (en, nb): step 3 (`:104-148`) — the ninth permission's paragraph
    gone, the unexpected-permissions paragraph naming an application `Calendars.ReadWrite`
    left from an earlier setup (remove it, and any `Application Calendars.ReadWrite` role
    assignment with `Remove-ManagementRoleAssignment`); *What happens to a person's
    connection* (`:297-316`) — a disconnect removes their copies, a leaver's stay in the
    disabled mailbox (Q2); *Calendar sync* (`:331-451`) **rewritten** — on each person's
    Outlook connection, preconditions (the Outlook connection on and set up; each person
    connects), turning it on (nothing to grant beyond the connection's delegated
    `Calendars.ReadWrite`), verifying with a connected person, *Limit it to some
    mailboxes* replaced by a short *Which mailboxes* paragraph (only the people who
    connect; no RBAC), the switch off closing everyone and everyone connecting again;
    *Change notifications* (`:453-541`) — no extra permission, the person's consent covers
    them; the RBAC bullet becomes people not connected or waiting to reconnect.
  - [ ] `admin/calendar.md` (en, nb): *The mailbox sync worker* (`:334-405`) — parked
    people, a day between re-checks, woken by a connect; what the log says for
    `not_connected`/`reconnect`.
  - [ ] `docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`: an *as built*
    note at O2 (the application permission and RBAC) and O8 (a leaver's copies) pointing
    to the Outlook connection design's M7, M9 and Q2.
  - [ ] The Outlook connection spec's *as built* note for B, listing this plan's
    departures.
  - [ ] `ROADMAP.md` (Phase 3, `:2425-`): B built, the calendar on the connection.
  - [ ] `mise run docs:check`.
  - [ ] Commit:
    `docs(calendar, entra): the calendar on the person's connection — the token source, removals, ErrConnectionNeeded, Connections and the subscriptions in the references; parking, waking and the clean-up in the calendar's; rule 14's note; the calendar, Connections and Entra guides, the Entra and calendar administration pages without Calendars.ReadWrite and its RBAC procedure, a leaver's copies left in their mailbox, both languages; the sync design's and the connection design's as-built notes; the roadmap`.

- [ ] **7. Review, then verification and the pull request.** A review (read-only) of the
  token wiring — no mailbox call left on `credentialSource` (`grep -n 's.cred' internal/entra`
  empty), the removal table, `ErrMailboxGone` only where nothing can remove, the
  revision guards untouched — and of the worker's parking (no backoff, no lost clean-up,
  the change cursor); a docs pass against the code; one fix batch (`fix(calendar, entra):
  the review's findings — …`, `docs(calendar, entra): the review's fixes — …`). Then
  `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check` (after `bun install --frozen-lockfile` in a fresh worktree).
  **Wait for #195 to merge**; rebase onto `main`; the gates again; push; the pull request
  against `main` with the real-tenant checklist: the application `Calendars.ReadWrite`
  and its RBAC assignment removed and the test listing none missing; a connected person
  synced, busy time read, an Outlook edit taken, a notification arriving; a person not
  connected parked and woken by connecting; a Conditional Access refusal turning the card
  to *Reconnect*; a disconnect removing the copies; a leaver's copies left; the switch off
  removing everyone's — and a note that A and B are released together.
