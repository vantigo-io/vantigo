# Calendar phase 1 delivery C — the feed and the subscriptions: implementation plan

**Goal:** a calendar address per person in two variants, full and busy-only, behind a
hashed and resettable token — the module's one anonymous `GET`, on the public host's
list — that a phone subscribes to; calendars from elsewhere subscribed to, polled by
the `calendar-subscriptions` worker through `netguard`, parsed on read and shown beside
the person's own, never in their feed; and the **My calendar** settings screen.

**Spec:** [`docs/superpowers/specs/2026-10-20-calendar-feed-design.md`](../specs/2026-10-20-calendar-feed-design.md)
(D1–D10), on [`2026-10-19-calendar-design.md`](../specs/2026-10-19-calendar-design.md) D9.

**Pattern code:** `internal/calendar/ics.go` (the `VEVENT` and `VTIMEZONE` writers —
extend, never duplicate), `attendees.go` (`newRSVPToken`, `tokenSHA256`, the sealed
link, `rsvpBase`), `rsvp.go` and `module.go`'s `limits` (an anonymous operation and its
bucket), `items.go` (the window, `sourceItemsIn`, `projectedItem`, `sortItems`),
`contractscalls.go` (every call out of the module), `cancellations_worker.go` (a worker
under a lease on one connection), `recurrence.go` (rrule-go dressed as floating);
`internal/security/publichost.go` and its test; `internal/peppol/lookup.go`'s
`guardedTransport` and `NewClient` (a guarded client with no redirects);
`internal/netguard`; `internal/contracts/access.go` and `identity/access.go`
(`HasPermissions`, `permitted`); `internal/config/config.go`'s `calendarWorkers`;
`internal/db/schema_calendar_invitations_test.go` (a migration's pin test);
`invoices/ehf/render_test.go` and `calendar/invitation_test.go` (goldens with `-update`);
`apps/calendar/frontend/src/pages/calendar.tsx`, `lib/items.ts`, `lib/colours.ts`,
`components/source-legend.tsx`; `apps/expenses/frontend/src/pages/settings.tsx` and
`apps/host/frontend/src/routes/expenses/settings.tsx` (a module's settings page and its
host route), `apps/host/frontend/src/routes/settings/profile.tsx`, `apps.ts` and
`apps.test.ts` (a nav item).

## The order

Task 2 first; then 3 (the feed), 4 (the subscriptions), 5 (the app) and 6 (docs) in
parallel; 7 last.

## Tasks

- [x] **1. The spec and this plan.**
- [ ] **2. The contract, the migration and the seams.** `openapi/calendar.yaml`:
  `FeedAddress { variant, address, webcalAddress, public, createdAt, lastUsedAt? }`,
  `FeedAddresses { public, addresses: [FeedAddress] }`, `GET /feed`,
  `POST /feed/{variant}`, `DELETE /feed/{variant}`, `GET /feed/{file}` (`anonymous`,
  `text/calendar` 200 with `ETag`, `Cache-Control` and `Content-Disposition` headers, 304
  with `ETag`, 404, 429; `If-None-Match` as a header parameter),
  `CalendarSubscription`, `CalendarSubscriptionWrite`, the five subscription
  operations, `CalendarItem.subscription { id, name, colour, busyOnly }`, the code
  `subscription_limit` on `CalendarProblem`; `go generate`, `bun run gen:client`.
  Migration `00057_calendar_feed.sql` with its pin test in `internal/db`.
  `queries/feed_tokens.sql` (get by user and variant, get by hash, upsert, touch
  last used, delete) and `queries/subscriptions.sql` (list for user, count for user,
  get for user, insert, update, update fetch outcome, set next fetch, list due with
  `FOR UPDATE SKIP LOCKED`, delete); `sqlc generate`. `contracts.Access.UserHolds`
  and `contracts.AsUser` with `HasPermissions`' second rule, identity's `UserHolds`
  over `GetUserByID`, the owner role and `EffectivePermissionForUser`, its test, and
  the canned answer on the test fakes (`module/router_test.go`, `server/server_test.go`,
  `openapi/module_router_test.go`, `communications/module_internal_test.go`);
  `security.PayRoutes()`'s feed route and `TestPublicHostGate`'s cases; `config`'s
  `CalendarSubscriptionsEnabled` and `CalendarSubscriptionsPoll` with the loader's
  test and `vantigo.env.example`; the secrets purpose constant in the module.
- [ ] **3. The feed, tests first.** `feed_tokens.go` (`GET /feed`, `POST` and `DELETE
  /feed/{variant}`: the token, the hash, the sealed address under
  `calendar/feed-address`, `feedBase` beside `rsvpBase`); `feed.go` (`GET /feed/{file}`:
  the suffix, the hash lookup, `last_used_at`, the window −3/+12 in the business zone,
  the person's appointments through `ownItems`' query minus the declined and the
  cancelled, the providers through `sourceItemsIn` under `contracts.AsUser`, the size
  rule, the `ETag` and 304, the headers); `feed_ics.go` (the `VCALENDAR` through `ics.go`'s
  writers generalised: `NAME`, `X-WR-CALNAME`, `REFRESH-INTERVAL`, `X-PUBLISHED-TTL`, one
  `VTIMEZONE` per zone, the full and the busy `VEVENT`s, `CATEGORIES` and `URL` for a
  projected item, `✓ ` for a done one); the `calendar-feed` policy in `module.go`'s
  `limits`; the goldens under `testdata/feed/`; the spec's feed tests in `feed_test.go`.
- [ ] **4. The subscriptions, tests first.** `subscriptions.go` (the five operations,
  the rules at write, the limit, the inline fetch); `subscriptions_fetch.go` (the
  guarded client — `netguard.DialContext`, no proxy, no redirect — the conditional
  request, the 1 MiB limit, the error sentences, the outcome written to the row;
  `Deps`-injectable transport for the tests, the way `modtest.WithTransport` serves
  Customers); `subscriptions_parse.go` (golang-ical, the time rules, `TZID` resolution,
  rrule-go expansion with `EXDATE` and `RECURRENCE-ID`, the caps) and `windowszones.go`
  (the CLDR table); `subscriptions_worker.go` (the five-minute tick, the lease
  "CALSUBS1", the due list, `next_fetch_at` with the jitter and `REFRESH-INTERVAL`,
  registered in `workers()` under its switch); `items.go` gains `subscriptionItems`
  after the providers and `meta.go` the kind when the caller has a subscription; the
  spec's subscription, worker and parse tests.
- [ ] **5. The app, tests first.** `apps/calendar/frontend`: `api/feed.ts`,
  `api/subscriptions.ts`; `pages/settings.tsx` (the two feed cards, the subscriptions
  table and form); `lib/colours.ts`' palette and `styles/fullcalendar.css`'s variables;
  `lib/items.ts` drawing a subscription's item in its colour, *Busy* when busy-only,
  never draggable; the legend's chip; `pages/calendar.tsx` opening nothing on a click;
  `i18n.ts` en + nb; `index.ts` exporting `SettingsPage`. `apps/host/frontend`:
  `routes/calendar/settings.tsx`, `apps.ts`' nav item **My calendar** under
  `calendar:access`, `catalogs/navigation.ts`, `apps.test.ts`.
- [ ] **6. Docs, both languages.** `en/reference/calendar.md` (the schema, **The
  feed**, **Subscriptions**, the endpoints; `AsUser` where the sources contract is
  described), the page describing `contracts.Access` for `UserHolds`,
  `{en,nb}/user/calendar.md`, `{en,nb}/admin/calendar.md`, `{en,nb}/admin/
  authentication.md`'s environment table, `en/contributing/module-boundaries.md`
  and `platform-packages.md`, `ROADMAP.md` (C built).
- [ ] **7. Review, gates and the pull request.** An independent review of `git diff
  main...HEAD -- apps/server openapi` for leaks (a feed carrying anything the
  person's own calendar does not; the busy variant anything but spans; a
  subscription's body republished; the guard letting a private address or a redirect
  through) and a docs pass against the code; `mise run server:check`, `server:test`,
  `frontend:check`, `docs:check`; the PR on `feat/calendar-feed` against `main` with
  CI green.
