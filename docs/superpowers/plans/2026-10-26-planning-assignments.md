# Planning delivery C — assignments and wishes: implementation plan

Delivery C of the Planning module, from the design
`docs/superpowers/specs/2026-10-26-planning-design.md` ("the spec", decisions S*n*,
its §13 amendments 1–64 binding) and the research
`docs/superpowers/research/2026-10-26-planning-module.md` ("N", §5 the terms, §8
readings 9, 10, 13). Branch `claude/nifty-jemison-e121e1`, opened against `main`
(`f37fdf87`, delivery B merged as PR #178) as one pull request; migration
**`00074_planning_assignments.sql`** (renumbered in-history if main takes 74 first,
see the memory on rebases). `srv/` abbreviates `apps/server/internal/`, `P/`
`srv/planning/`. Delivery B's plan (`2026-10-26-planning-requests.md`) is the model for
this one; it closes phase 1.

## What C ships

**Terms** (S14): `planning.allocation_terms`, one row to an allocation, `planning:manage`'s
to read and write (`PUT`/`DELETE /allocations/{id}/terms`), with the two **derived
dates** (S15) — the earliest release and the handover start — on every allocation
response, computed on read and never stored. **The non-compete** (S17.3) as the one hard
filter: a candidate under one is not listed; a `POST`, a `PUT`, a `fill` or a `confirm`
that would place a confirmed row of the person on such a customer's project is the 409
`non_compete`; a tentative row is written with the warning. **The release** (S17.4) and
**the wishes** (S17.5) as a candidate's flags, and *wishes first* as the sort's second
key. **Wishes** (S21): `planning.wishes`, the person's own on My planning (`POST
/me/wishes`, `DELETE /me/wishes/{id}`), the planners' list, seen and resolve, the mail to
every `planning:manage` holder, the `wishesUnseen` item (S22), the wishes on the
timeline, an erased person's open wishes deleted (S26). **The calendar source**
`planning.allocation` (S24), registered only when `calendar` is composed (S25). The six
events (S23). The app's terms section, timeline marks, candidate flags, wish forms and
Wishes page; the host's route, entry, item and the calendar's kind label. The docs.

Out of C (phase 2, the roadmap's *Later*): shifts, the forecast, the automatic request,
the booking contract Staff reads for holiday approval, the personal-data slot (open
question 3 stays open: the owner has not pulled it forward).

## The contract (`openapi/planning.yaml`), fixed here so every lane agrees

Written by hand before the lanes started, regenerated (`go generate ./...` in
`apps/server`, `bun run gen:client`). The seven new operations:

| Operation | Op id | Access |
| --- | --- | --- |
| `PUT /allocations/{id}/terms` `PlanningTermsInput` → 200 `PlanningAllocation` (with `terms` and the derived dates); `If-Match` **optional**: the terms' revision when the row has terms, left out to set terms on a row with none; a mismatch either way is `revision_conflict` | `putPlanningAllocationTerms` | +manage |
| `DELETE /allocations/{id}/terms` → 204; a row with no terms is left as it is | `deletePlanningAllocationTerms` | +manage |
| `POST /me/wishes` `PlanningWishInput {kind, projectId?, fromDate}` → 201 `PlanningWish`; 404 `no_person`; 409 `wish_no_booking`, `wish_limit`, `wish_exists` | `createPlanningMyWish` | access |
| `DELETE /me/wishes/{id}` → 204; 404 for a wish not the caller's | `deletePlanningMyWish` | access |
| `GET /wishes?personId` → `PlanningWishList {wishes}`, open wishes oldest first | `listPlanningWishes` | +manage |
| `POST /wishes/{id}/seen` → 200 `PlanningWish` | `markPlanningWishSeen` | +manage |
| `POST /wishes/{id}/resolve` → 204 | `resolvePlanningWish` | +manage |

`PlanningTerms {noticeCount?, noticeUnit?: days|weeks|months, noticeGivenOn?,
minimumUntil?, replacementNeedsApproval, handoverDays?, nonCompeteUntil?,
nonCompeteCustomers: [PlanningCustomerRef], reference?, revision, createdAt, updatedAt}`;
`PlanningTermsInput` the same with `nonCompeteCustomerIds: [int32] (≤ 20)`, whole on
every `PUT` (a field left out is cleared). `PlanningAllocation` gains `earliestRelease`
(required), `handoverStart?` and `terms?` (manage). `PlanningWish {id, person:
PlanningPersonRef, kind: off|new, project?: PlanningProjectRef, fromDate, createdAt,
seenAt?}`. `PlanningMe` and `PlanningTimeline` gain `wishes` (required). The candidate
flag's `code` gains `release_after_start` (`date` the earliest release, `handoverFrom`,
`allocationId`), `needs_customer_approval` (`allocationId`) and `wishes` (`wishKind`,
`date`); the warning's `code` gains `non_compete` (`allocationId` the binding row, `date`
its `nonCompeteUntil`); the attention type gains `wishesUnseen`; the event's
`entityKind` gains `wish` and `kind` the six; `PlanningProblem` names `non_compete`,
`wish_exists`, `wish_limit`, `wish_no_booking`.

## The seam (done before the lanes)

- The migration (above) and `P/sqlc.yaml`; the contract and the generated code; the
  client's `api-schema.d.ts`; `P/stubs.go` answering 503 for the seven operations;
  `main_test.go`'s `pending` naming them; `errors.go`'s four codes and `warnNonCompete`;
  `events.go`'s `entityWish` and the six kinds; `P/terms.go` with `applyTerms` as a
  no-op setting `earliestRelease` to `toDate`, called by the responses so both server
  lanes build against one name; `me.go` and `timeline.go` answering empty `wishes`.
- The app: `WISHES_ROUTE_PATH`, the nav item `wishes` (`navWishes`, `planning:manage`,
  after Unbooked), a placeholder `WishesPage`, so the host lane builds against them.

## Tasks

### Task 1 — Dossiers (done 2026-10-26)

Four Explore dossiers in the scratchpad (server, calendar seam, app and host, docs);
Explore agents cannot write files, so their inline reports were saved by hand.

### Task 2 — Terms, the derived dates, the non-compete, the candidates (server lane 1)

Files: `P/terms.go` (reads, `applyTerms`, the derived dates), `P/terms_write.go`
(`PUT`, `DELETE`), `P/terms_validation.go`, `P/queries/terms.sql`, `P/allocations.go`,
`P/responses.go`, `P/allocations_write.go` (the non-compete on create, replace,
confirm), `P/allocations_validation.go` (`refuseNonCompete`, the warning),
`P/candidates.go`, `P/intake.go` (if the judgement's inputs change), `P/board.go`,
`P/plan.go`, `P/requests.go`, `P/requests_write.go`, `P/fakes_test.go` (a second
customer, `fakeCustomers.set`), `P/fakes_rules_test.go`, tests `P/terms_test.go`,
`P/allocations_test.go`, `P/candidates_test.go`, `P/intake_test.go`; its two stubs
removed from `P/stubs.go` and its two ids from `pending`.

- **Validation** (S14, rule 7): `noticeCount` 1–365 and `noticeUnit` both or neither
  (a field error on the one missing); `noticeGivenOn` needs a notice period (a field
  error on `noticeGivenOn`); `handoverDays` 0–90; `nonCompeteUntil` required when any
  customer id is given and a field error on `nonCompeteCustomerIds` when given with
  none; at most 20 ids, duplicates collapsed, each read through `customerNames` before
  the transaction, an unknown one a field error on `nonCompeteCustomerIds`;
  `reference` trimmed, at most 200 runes, empty cleared; `replacementNeedsApproval`
  defaults false. The title is `invalidAllocationTitle`.
- **`PUT`**: `parseIfMatch` when the header is present (`badIfMatch` otherwise); the
  allocation read (404); `project_closed` on a closed project (it is a change to the
  row, S4); the customers read; under the **person's lock**: the terms row read `FOR
  UPDATE`; the supplied revision (none when the header is absent) must equal the
  stored (none when no row) else `revision_conflict`; insert or update whole, the
  revision moved on; the event `terms_set` under `entityAllocation` with the changed
  fields (`noticeCount`, `noticeUnit`, `noticeGivenOn`, `minimumUntil`,
  `replacementNeedsApproval`, `handoverDays`, `nonCompeteUntil`,
  `nonCompeteCustomerIds` as a list, `reference`) before and after; nothing recorded
  when nothing changed. Answers the allocation through `allocationResponse` +
  `applyTerms` with `withTerms` true.
- **`DELETE`**: no `If-Match`; 404 when no allocation; allowed on a closed project (as
  a delete is); under the person's lock delete the row when one exists and record
  `terms_cleared` with the fields before; 204 either way.
- **The derived dates** (S15), `derivedDates(row, terms) (release time.Time, handover
  *time.Time)`: release = latest of `to_date`, `minimum_until`, and `notice_given_on`
  plus the period (`days` → `addDays`, `weeks` → ×7, `months` → `AddDate(0, n, 0)`)
  when given; handover = release − `handover_days` when set. Computed from the terms
  **whenever a terms row exists, on a tentative row too**, so the planner sees the
  consequence while drafting; they *bind* — the matcher and the refusals — on confirmed
  rows only (reading 1).
- **`applyTerms(ctx, out []gen.PlanningAllocation, withTerms bool) error`**: reads the
  terms rows of `out`'s ids in one query (no lock), sets `EarliestRelease` and
  `HandoverStart` on every row, and for `withTerms` the `Terms` with the customers named
  through `customerNames` (an unresolved id is `{id}` alone, rule 4). Every call site of
  `allocationResponse`/`allocationsView` that answers the client calls it: the list, the
  get, the write answers, the board, the plan, the timeline (lane 2's file; lane 1 may
  touch the one line), My planning, the fill. Terms are never answered to `view` or
  `self`.
- **The non-compete** (S17.3), `judgeNonCompete(ctx, txq, personID, exceptRowID,
  customerID *int32, from time.Time) (*nonCompete, error)` under the person's lock: the
  person's **confirmed** rows other than `exceptRowID` whose terms have
  `non_compete_until >= from` and name `customerID` (one query joining the terms; nil
  when the project has no customer). On `insertAllocation` (create and fill), on
  `replace` (judged on the new project and person, only when the row is left
  confirmed — moving a row off a person is never judged for them, amendment 20's
  spirit) and on `confirm`: a confirmed row is `refuseNonCompete` (409 `non_compete`,
  detail naming the binding allocation and the date); a tentative one gets the warning
  `non_compete` with `allocationId` and `date`. The warning rides on `overbooking`'s
  answer path (`allocationWarnings` gains the case) so `created` and the fill answer it
  without a new type.
- **Candidates** (S17.3–5): before `judgeCandidates`, load for the kept people their
  confirmed rows with a terms row (`PersonAssignmentsFrom`: rows with terms, `to_date >=
  request.from` or any `non_compete_until >= request.from`) and their open wishes, and
  the request's project's customer; per person: a non-compete naming that customer with
  until ≥ the request's first day **excludes** (no `includePartial` escape); a row with
  terms whose earliest release is after the request's first day flags
  `release_after_start` (`date`, `handoverFrom`, `allocationId`) — one flag per such row;
  a row with terms and `replacementNeedsApproval` flags `needs_customer_approval`
  (`allocationId`); an open `new` wish, or an `off` wish on the person's current project
  (the confirmed row covering today with the earliest `from_date`, as the fairness
  signal reads it), with `fromDate ≤` the request's first day flags `wishes` (`wishKind`,
  `date`), one flag per wish. `judged` gains `wishes bool`; the sort is `fits` first,
  then wishes first, then the rest as B. The intake judges with the same inputs (a
  flag unsets `covers`).
- **Tests**: validation per field; the If-Match rule's four cases; set on a tentative
  row; `project_closed`; the derived dates under each combination (no terms, minimum
  only, notice in each unit, notice given before and after the end, handover, a
  tentative row); the terms whole to manage and absent to view and self; the non-compete
  refusing a confirmed POST, PUT, fill and confirm, warning on a tentative one, not
  firing for an unrelated customer, a project without a customer, an expired date, or a
  tentative binding row; the candidates' exclusion and the three flags, the wishes sort
  with ties, the intake's coverage with a flag; events' payloads; the fakes' rules.

### Task 3 — Wishes, the item, the events' audience, the calendar source (server lane 2)

Files: `P/wishes.go` (reads, the response, the planners' list with S26's deletion),
`P/wishes_write.go` (create, withdraw, seen, resolve, the mail), `P/queries/wishes.sql`,
`P/me.go`, `P/timeline.go`, `P/attention.go`, `P/stats.go` (nothing unless a figure is
wanted), `P/events_read.go` + `P/queries/events.sql` (the wish events to manage alone),
`P/notifications.go` (the wish notice), `P/module.go` (`CalendarSource`),
`P/calendar_source.go`, `P/harness_test.go`, tests `P/wishes_test.go`, `P/me_test.go`,
`P/timeline_test.go`, `P/attention_test.go`, `P/events_read_test.go`,
`P/calendar_source_test.go`, `P/notifications_test.go`; its five stubs removed from
`P/stubs.go` (the file deleted by whichever lane empties it) and its five ids from
`pending`. Lane 2 does not edit `fakes_test.go`, `contractscalls*.go` or `errors.go`.

- **Create** (S21): `callerSelf` (404 `no_person`); `kind` `off` needs `projectId`,
  `new` refuses one (field errors); `fromDate ≥ today` (a field error); the project
  entry read for `off` (unknown → a field error on `projectId`); the holders of
  `planning:manage` read before the transaction; under the **person's lock**: an `off`
  wish needs a confirmed row of the person on that project with `to_date ≥ today`
  (`wish_no_booking`, 409); the open count < 5 (`wish_limit`); no `off` wish on the
  project (`wish_exists`; the unique index is the backstop); insert; the event
  `wish_created` under `entityWish`, filed under the person and the project, payload
  `{kind, projectId?, fromDate}`. After the commit: every holder mailed once through
  `usersNotify`, de-duplicated by id, the wisher's own user left out, in both languages
  ("NAME wishes off CODE NAME from DATE" / "NAME wishes for something new from DATE"),
  linking to `/planning/wishes`; a failure logged, never an error. 201 with the wish.
- **Withdraw** (`DELETE /me/wishes/{id}`): the caller's own (else 404); under the lock
  delete and record `wish_withdrawn` with `{kind, projectId?, fromDate}`.
- **Seen**: 404 when none; under the person's lock stamp `seen_at`/`seen_by_user_id`
  when null and record `wish_seen`; a stamped wish is answered as it is. **Resolve**:
  delete and record `wish_resolved`; 204.
- **The list** (`GET /wishes`): open wishes oldest first (`created_at`, `id`),
  `personId` narrowing; the people named through `workforcePeople` over today..the
  bound's end, the projects through `projectEntries`; **S26**: when `People` answered at
  least one person (the caller sees the workforce), every open wish whose person it did
  not answer is deleted in one transaction under that person's lock with the event
  `wish_withdrawn` and the payload `{kind, projectId?, fromDate, reason: "unanswered"}`
  (reading 4) and left out of the answer.
- **My planning**: `wishes` = the caller's open wishes oldest first, with the project
  named. **The timeline**: `wishes` for `manage` and for the self audience (their own),
  empty to `view` alone.
- **`wishesUnseen`** (S22): under `c.Manage`, one item when any open wish has no seen
  stamp: `count` the number, `entityId` the oldest unseen wish's id, `occurredAt` its
  `created_at`, `title` the oldest wisher's name (or the id when Staff does not answer).
- **Events' audience** (S23): `ListEvents` gains `include_wishes bool`; a caller
  without `manage` never sees `entity_kind = 'wish'` rows, whatever the filter.
- **The calendar source** (S24, S25): `Module().CalendarSource = newCalendarSource`,
  which answers nil unless `slices.Contains(d.Config.Modules, "calendar")`; kind
  `planning.allocation` (module `planning`); `CalendarItems`: nothing without
  `planning:access` (`contracts.HasPermission`, which answers under `AsUser`); the
  person through `workforceSelf` (nil → nothing); the confirmed rows touching the window
  (`PersonConfirmedAllocationsInWindow`); the projects through `projectEntries`;
  all-day items over `from_date`..`to_date`, `ID` the row's id, `Title` the project's
  code and name (`project N` when unresolved), `Subtitle` the load ("50 %", "20 h"),
  `Path` `/planning/me`. Tests: kinds; items for a linked person with the permission,
  under a request context and under `contracts.AsUser`; nothing without the permission,
  without a person, for a tentative row, for a colleague's row; nil when `calendar` is
  not in `Config.Modules` (the harness's `MODULES` lacks it: build the source over a
  copy of `h.Deps()` with `calendar` appended to a cloned `Config.Modules`).

### Task 4a — The app (`@vantigo/planning-ui`) (in parallel with 2 and 3)

Files: `apps/planning/frontend/src/{api/terms.ts, api/wishes.ts, api/me.ts,
api/allocations.ts, api/requests.ts, api/request.ts, api/stats.ts, api/customers.ts,
components/terms-section.tsx, components/customer-picker.tsx,
components/allocation-sheet.tsx, components/candidates-panel.tsx,
components/person-timeline.tsx, components/wish-forms.tsx, pages/me.tsx,
pages/wishes.tsx, lib/routes.ts, lib/navigation.ts, index.ts, i18n.ts, i18n.test.ts,
test/server.ts, test/fixtures.ts, test/route-tree.tsx}` and the page tests.

- **The terms section** on the allocation sheet, under the form in edit mode for
  `manage`: its own form keyed by the terms' revision — notice count and unit, notice
  given on, minimum until, replacement needs approval, handover days, non-compete until
  and customers (a picker over `GET /api/v1/customers?search=` with a minimal local
  shape, as Projects' picker does; the chosen customers shown by name from
  `terms.nonCompeteCustomers`; a planner without `customers:view` sees the names the
  server answered and can remove them), reference; *Save terms* (`PUT` with the terms'
  revision as If-Match, none for a first set) and *Clear terms* (`DELETE`, confirmed);
  field errors on their fields; `revision_conflict` as the sheet handles it. The
  sheet's header shows the derived dates when terms exist.
- **The timeline**: a lane of marks per allocation with terms — *Earliest release* and
  *Handover from* as one-day spans — and the two dates as table columns; an *Open
  wishes* card (kind, project, from, seen) when `wishes` is non-empty.
- **The candidates panel**: the three new flag badges (`candidateFlag_release_after_start`
  with `{{date}}` and the handover in a second badge when given,
  `candidateFlag_needs_customer_approval`, `candidateFlag_wishes` with `{{kind}}` and
  `{{date}}`); the `non_compete` warning on the fill as on the sheet.
- **My planning**: a *My wishes* card — the open wishes with the kind, the project, the
  date and *Seen by a planner DATE* / *Not seen yet*, each with *Withdraw*; two buttons
  opening a small form: *I wish off this project from…* (a select over the projects of
  the confirmed bookings shown, a date) and *I wish for something new from…* (a date);
  the refusals `wish_no_booking`, `wish_limit`, `wish_exists` as rule refusals. Phone
  first.
- **The Wishes page** (`/planning/wishes`, manage): the open wishes oldest first —
  person (linked to the timeline), kind, project (linked to the plan), from, created,
  seen stamp; *Mark seen* and *Resolve* (confirmed); unseen rows marked; an empty state.
- Strings en + nb: Terms / Vilkår, Assignment / Oppdrag, Notice period / Oppsigelsestid,
  Notice given on / Oppsigelse gitt, Minimum until / Bundet til, Replacement needs the
  customer's approval / Erstatning krever kundens godkjenning, Handover days /
  Overlappingsdager, Non-compete until / Karantene til, Customers / Kunder, Reference /
  Referanse, Earliest release / Tidligst ledig, Handover from / Overlapping fra, Wishes /
  Ønsker, wish / ønske, "I wish off this project from…" / "Jeg ønsker meg av dette
  prosjektet fra…", "I wish for something new from…" / "Jeg ønsker noe nytt fra…", Off
  / Av, Something new / Noe nytt, Seen / Sett, Not seen yet / Ikke sett ennå, Mark seen
  / Merk som sett, Resolve / Avslutt, Withdraw / Trekk tilbake, the refusals and the
  warning.

### Task 4b — The host and the calendar app (in parallel with 4a, against the seam's exports)

Files: `apps/host/frontend/src/{apps.ts, catalogs/navigation.ts, catalogs/dashboard.ts,
lib/dashboard-helpers.ts, routes/planning/wishes.tsx, routeTree.gen.ts}`, the tests
(`apps.test.ts`, `routes/planning-routes.test.ts`, `routes/dashboard.test.ts`);
`apps/calendar/frontend/src/{i18n.ts, lib/colours.ts}` and a test.

- The sidebar entry (manage, `IconHeartHandshake` or alike), the route (guarded by the
  nav item's permissions through the module access guard), `routeTree.gen.ts`
  regenerated; the `wishesUnseen` href `/planning/wishes` and the title key
  `dashboard.planningWishesUnseen` ("Unseen wishes: {{count}}" / "Usette ønsker:
  {{count}}"); the calendar's `kind_planning_allocation` ("Bookings" / "Allokeringer")
  and `moduleColours.planning`.

### Task 5 — Docs (after 2–4; in parallel with the reviews)

Files: `docs/src/content/docs/en/reference/planning.md` (`sources` gains the
migration; Terms, The derived dates, Non-compete in the writes and the candidates,
Wishes, The calendar source, Events, Locks, Refusals, Stats and attention, Permissions,
Personal data, Endpoints, "What comes next" now phase 2, the delivery markers),
`en/reference/calendar.md` (the kinds table's seventh row, the intro),
`en/reference/index.md`, `en/contributing/module-boundaries.md` (the Planning
paragraph, rule 12's examples), `{en,nb}/user/planning.md` (the sidebar table, the
allocation sheet's terms, the timeline's marks and wishes, the candidates' flags, My
planning's wishes, the Wishes page, the dashboard item, who sees what, the later-delivery
sentence gone), `{en,nb}/user/index.md`, `{en,nb}/user/calendar.md` (the chip),
`{en,nb}/user/workspace-administration.md` (the permissions' words), `ROADMAP.md` (C
built; phase 1 complete), the spec's amendments 65+.

### Task 6 — Reviews, gates, commits, PR

- An opus apps review (4a, 4b) as soon as the frontend lands and an opus server review
  (2, 3) after the server, each against the spec; fixers; the spec's amendments list
  what the build changed.
- Gates: `mise run server:check`, `mise run server:test`, `bun run format:check`,
  `bun run translations:check`, `bun run i18n:test`, `bun run frontend:lint`,
  `bun run frontend:typecheck`, every frontend package's tests one after another,
  `bun run gen:client && git diff --exit-code -- '**/api-schema.d.ts'`,
  `cd docs && bun run build`, `bun run tools/docs/check-coverage.ts --base origin/main`
  (no `Docs-Impact: none` anywhere on the branch).
- Commits: `feat(planning): …`, `feat(planning-ui): …`, `docs(planning): …`;
  `gh pr create --base main`; CI through the app's PR status.

## Readings taken in planning

1. **The derived dates are computed whenever terms exist, binding on a confirmed row**
   (S14 reading 8, S15): a tentative row with terms answers its earliest release and
   handover start so the planner sees what confirming would bind; the matcher and the
   non-compete read confirmed rows alone.
2. **The terms `PUT`'s `If-Match` is optional**: the terms row has its own revision
   (§4.8), and a row with no terms has none to send; the header left out means "the
   row has no terms", and a header on a row without terms, no header on a row with
   terms, or a stale one are each `revision_conflict`. A terms `PUT` is a change to the
   row and is `project_closed` on a closed project; the `DELETE` is allowed there, as a
   row's deletion is.
3. **A non-compete date with no customer is a field error**, and customers with no
   date too (S14): the term is "toward named customers until a date"; half of it binds
   nothing and would only mislead.
4. **An erased person's open wishes are deleted on the planners' list only when `People`
   answered somebody** (S26): to a planner without `staff:view` Staff answers nobody,
   and taking that for "everyone is erased" would empty the table; the deletion is
   recorded as `wish_withdrawn` with `reason: unanswered`, since the wish was the person's
   and nobody resolved it.
5. **The release flag reads rows with terms alone** (S17.4): every overlapping confirmed
   row has an end after the request's first day, so "a confirmed row whose earliest
   release is after" taken literally would flag everyone busy; the flag is about an
   assignment — a row whose terms bind the person — and is one flag per such row.
6. **The wisher is not mailed about their own wish** (S21): a planner who files a wish
   as a person already knows; every other `planning:manage` holder is mailed once.
7. **The calendar source's subtitle is the load in figures** ("50 %", "20 h"), since the
   feed has no language of the viewer; the title is the project's code and name as the
   mails give it.
8. **The non-compete is judged on the target project's customer against the person's
   confirmed rows' terms**, read under the person's lock through one query joining the
   terms, with the target customer read before the transaction (S23's rule): no project
   entry of another row is needed, since the terms store the customers they name.
9. **The `wishesUnseen` item is one item with a count**, as `unbooked` is one per person:
   the planners' Wishes page is the answer, not a list of names on the dashboard.
