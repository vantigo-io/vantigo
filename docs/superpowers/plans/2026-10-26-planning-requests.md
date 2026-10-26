# Planning delivery B — requests and intake: implementation plan

Delivery B of the Planning module, from the design
`docs/superpowers/specs/2026-10-26-planning-design.md` ("the spec", decisions S*n*,
its §13 amendments binding) and the research
`docs/superpowers/research/2026-10-26-planning-module.md` ("N"). Branch
`claude/dreamy-ptolemy-9ff41d`, opened against `main` (`ece0ac8e`, delivery A merged as
PR #177) as one pull request; migration **`00073_planning_requests.sql`** (renumbered
in-history if main takes 73 first, see the memory on rebases). `srv/` abbreviates
`apps/server/internal/`, `P/` `srv/planning/`. Delivery A's plan
(`2026-10-26-planning-plan.md`) is the model for this one.

## What B ships

Requests as the placeholder (S16): `planning.requests` and
`planning.request_requirements`, hours a week only, open / filled / withdrawn; fill
creating the allocation under A's write rules and naming the request on the row;
reopen on delete of the filling row; withdraw. Candidates (S17 points 1, 2, 6 and the
sort): competence through `HeldCompetences`, availability through the capacity
calculation, fit through the new `Workforce.PreferenceFits`, the fairness signal. Intake
(S18) over the `planned` projects. The plan's `demand` per week (S11) and its requests;
the `requestsOpen` item and `openRequests` on the stats (S22); the request events and
the project lock (S23); `GET /competences` for the request sheet and the board's
competence control (amendment 38). The app's Requests and Intake pages, the request
sheet, the candidates panel, the plan's demand row; the host's routes, the Planning
tab on the project page, the attention item and the KPI. The docs.

Out of B: non-compete, release and wishes in the matcher (C), terms, the calendar
source.

## The contract (`openapi/planning.yaml`), fixed here so every lane agrees

Written by hand before the lanes started, regenerated (`go generate ./...` in
`apps/server`, `bun run gen:client`). The nine new operations:

| Operation | Op id | Access |
| --- | --- | --- |
| `GET /competences` → `PlanningCompetenceList {competences: [PlanningCompetence]}`; `PlanningCompetence {id, key, nameEn, nameNb, categoryKey, kind, hasLevel, hidden}` — `Workforce.Competences`, empty when Staff answers nothing | `listPlanningCompetences` | +view |
| `GET /intake` → `PlanningIntake {projects: [PlanningIntakeProject]}`; `PlanningIntakeProject {project: PlanningProject, requestedHoursPerWeek, tentativeHoursPerWeek, confirmedHoursPerWeek, requests: [PlanningIntakeRequest]}`; `PlanningIntakeRequest {request, coverage: covered|partly_covered|uncovered, candidates: int, flags: [key_person]}` | `getPlanningIntake` | +view |
| `GET /requests?projectId&status&from&to` → `PlanningRequestList {requests}` (≤ 500, by from date then id; no window lists all) | `listPlanningRequests` | +view |
| `POST /requests` `PlanningRequestInput {projectId, title, fromDate, toDate, hoursPerWeek, note?, requirements?: [{competenceId, minLevel?}]}` → 201 `PlanningRequestWritten {request, warnings}` | `createPlanningRequest` | +manage |
| `GET /requests/{id}` → `PlanningRequest` | `getPlanningRequest` | +view |
| `PUT /requests/{id}` (If-Match) → `PlanningRequestWritten`; a filled or withdrawn request is `request_not_open` | `putPlanningRequest` | +manage |
| `GET /requests/{id}/candidates?includePartial` → `PlanningCandidateList {candidates, includePartial}` | `listPlanningRequestCandidates` | +manage |
| `POST /requests/{id}/fill` `PlanningFillInput {personId, status?}` → 201 `PlanningRequestFilled {request, allocation, warnings}` | `fillPlanningRequest` | +manage |
| `POST /requests/{id}/withdraw` → `PlanningRequest` | `withdrawPlanningRequest` | +manage |

`PlanningRequest {id, project: PlanningProjectRef, title, fromDate, toDate,
hoursPerWeek, status, note? (manage), requirements: [PlanningRequirement {competenceId,
key?, nameEn?, nameNb?, kind?, hasLevel?, minLevel?}], filledBy?: {allocationId,
person: PlanningPersonRef}, revision, createdAt, updatedAt}`.
`PlanningCandidate {person: PlanningPerson, availability: fits|partial, freeHours,
requiredHours, fit: {score, lowered: [kind]}, flags: [{code: certificate_expires,
competenceKey?, date?}], missing: [key], covers: bool, currentProject?: {project:
PlanningProjectRef, since, days}}`. `PlanningAllocation` gains `requestId?`;
`PlanningProjectPlan` gains `requests` (open and filled touching the window) and a real
`demand`; the event `entityKind` gains `request` and `kind` the five request kinds; the
attention type enum gains `requestsOpen`; `PlanningProblem` names `request_not_open`.
The generated flag constants are now type-prefixed
(`gen.PlanningTimelineFlagCodeCertificateExpires`), since two enums share
`certificate_expires`.

## The seam (done before the lanes)

- `srv/contracts/workforce.go`: `PreferenceFits(ctx, personIDs []int64, b Booking)
  (map[int64]Fit, error)`, `Booking{From, To time.Time; Weekdays []int; Parts []string;
  LocationID *int64; HoursPerWeek *float64; ConsecutiveDays *int; Travel, Remote
  string}`, `Fit{Score int; Lowered []string}` (reading 6).
- `srv/staff/workforce_fit.go` + `queries/workforce.sql` `WorkforcePreferences`: the
  rule — `staff:view`, the Colleague rule over the booking's period through
  `windowPeople`, the bound; 100 less weight × 10 per contradicted kind, floored at 0;
  `Lowered` in the catalogue's order. What contradicts, per kind: `weekdays` — a weekday
  of the booking not among the preferred; `time_of_day` — a part not among the
  preferred; `location` — the booking's location not among the preferred;
  `travel` — the booking's extent beyond the preferred (none < regional < national <
  international); `remote` — the booking's share other than the preferred;
  `max_consecutive_days`, `max_hours_per_week` — the booking's figure above the
  preferred. A facet left nil or empty contradicts nothing; a stored value that does
  not decode contradicts nothing.
- The fakes: `srv/module/compose_test.go` (nil), `P/fakes_test.go` (`setFit`,
  `lastBooking`, 100 for a person with no fit set).
- The migration (above), `P/sqlc.yaml`, the contract and the generated code, the
  `P/stubs.go` answering 503 for the nine operations, `plan.go` emitting an empty
  `requests` until the lane fills it.

## Tasks

### Task 1 — Dossiers (done 2026-10-26)

Three Explore dossiers in the scratchpad (server, apps, docs).

### Task 2 — Staff's tests and the fake's rules (in parallel with 3 and 4)

Files: `srv/staff/workforce_fit_test.go` (new), `P/fakes_rules_test.go`.

- `PreferenceFits` per kind and weight (one preference each, the booking contradicting
  and not), the score floor, no preference → 100, `staff:manage` alone → nothing, a
  person not in employment through the window absent, the bound refused, `Lowered`
  ordered; a nil facet contradicting nothing; `max_hours_per_week` judged on the
  booking's hours alone (37 h on a 40 h limit fits, whatever else the person has).
- The fake rules test pins `PreferenceFits` (visible false → nothing; unknown id
  absent; default 100) and `Competences` (the seeded catalogue, nothing when not
  visible).

### Task 3a — Requests, their writes, the fill and the reopen (first server lane)

Files: `P/requests.go` (reads and the response), `P/requests_write.go` (create, put,
withdraw, fill), `P/requests_validation.go`, `P/competences.go`
(`GET /competences`), `P/contractscalls.go` (accessors `workforceCompetences`,
`workforceFits`; the three accessor tests in `contractscalls_internal_test.go` and
`silentWorkforce`), `P/queries/requests.sql` (new; `go generate`), `P/queries/
allocations_write.sql` (`InsertAllocation` gains `request_id`),
`P/allocations.go` (`allocationResponse` sets `RequestId`), `P/allocations_write.go`
(the delete hook; `recordedValues` gains `requestId`), `P/events.go` (the kinds and
`entityRequest`), `P/locks.go` (the comment), `P/fakes_test.go` (`competences` field,
`setCompetences`), `P/stubs.go` (its stubs removed), tests `P/requests_test.go`,
`P/competences_test.go`, `P/main_test.go` (`pending` while building, empty at the end).

- **Validation**: title required, trimmed, ≤ 100 runes; `hoursPerWeek` through
  `loadOfInput(loadUnitHours, v)` (> 0, ≤ 80, two decimals) as a field error on
  `hoursPerWeek`; the period through `periodErrors` and `boundErrors`; note ≤ 500;
  requirements ≤ 10 (`requirements`), each competence once (the later index), each read
  from `Workforce.Competences` — unknown or archived (not in the catalogue) a field error
  on `requirements[i].competenceId`, `minLevel` 0–4 only when `hasLevel` else a field
  error on `requirements[i].minLevel`; a hidden competence is accepted (it is still
  held). A caller whom Staff answers no catalogue cannot name a requirement (every id
  unknown) — Staff's rule, not restated.
- **Create**: project entry before the transaction (unknown → field error on
  `projectId`; closed → `project_closed`), the warning `outside_project_dates`; under
  `withProjectLock`: insert the request `open`, its requirements, the event
  `request_created` with the recorded values (`projectId`, `title`, `fromDate`,
  `toDate`, `hoursPerWeek`, `note` as its name alone, `requirements` as
  `[{competenceId, minLevel}]`).
- **Put**: If-Match; the request read outside, the revision checked, `request_not_open`
  when not open (amendment: a filled or withdrawn request takes no change);
  `project_closed` on the old and the new project; under the project locks (old and
  new, ascending): `LockedRequest` FOR UPDATE, revision again, update, requirements
  replaced, `request_changed` with the changed fields; a put that changes nothing writes
  nothing.
- **Withdraw**: `request_not_open` unless open; under the project lock sets
  `withdrawn`, `request_withdrawn`; no If-Match (§4.8).
- **Fill**: `request_not_open` unless open (re-judged under the lock); the allocation's
  fields are the request's project, period, hours a week (`hours_per_week`), title;
  `status` as given or by the project's status; then exactly A's create flow (project
  entry, `readStaff`, `personIn`, `employs`, settings, overbooking under the person's
  lock) with the locks `lockSet{projects: {project}, persons: {person}}` — project
  first, then person — and in the same transaction: insert the allocation with
  `request_id`, set the request `filled`, record `allocation_created` and
  `request_filled` (payload `{allocationId, personId}`, filed under the project and the
  person); after the commit the "planned" mail when confirmed. Answer
  `PlanningRequestFilled` with the allocation's warnings.
- **Reopen**: `s.remove` takes the project lock too when `current.RequestID != nil`
  (`lockSet{projects, persons}`), re-reads the row under the lock, and when the row
  names a request sets it `open` and records `request_reopened` (`{allocationId}`)
  beside `allocation_deleted`; `retryMoved` unchanged. A `PUT` on the row leaves the
  request filled (S16) and keeps `request_id`.
- **Reads**: `GET /requests` (filters; a window matches rows touching it), `GET
  /requests/{id}`; `requestResponse(row, requirements, catalogue, project, filledBy,
  withNote)` names each requirement from the catalogue when it still answers it (rule
  4) and `filledBy` from the filling allocation and `People` over the row's window cut
  to the bound; the note for `manage` alone.
- **Competences**: `GET /competences` answers `Workforce.Competences` through the
  accessor, empty when nothing.
- **Tests**: field errors (each), `project_closed`, `request_not_open` on fill, put and
  withdraw, fill creating the row with `requestId` and the request `filled`, fill under
  `refuse` overbooking refused and nothing written (the request stays open), delete
  reopening with both events in one transaction, a put on the row leaving it filled,
  withdraw never reopening, the list's filters, the note's audience, the catalogue
  read, permissions (view alone 403 on writes and candidates), events' payloads, the
  locked-call check.

### Task 3b — Candidates, intake, demand, the item and the stat (second server lane, after 3a)

Files: `P/candidates.go`, `P/intake.go`, `P/plan.go` (demand and requests),
`P/attention.go` + `P/stats.go` (`requestsOpen`, `openRequests`), `P/queries/
requests.sql` additions (own section), their tests (`candidates_test.go`,
`intake_test.go`, `plan_test.go`, `attention_test.go`, `stats_test.go`), `P/stubs.go`
deleted.

- **Candidates** (`s.candidatesOf(ctx, c, req, includePartial)`, shared with intake):
  the people of `loadWindow` over the request's period cut to the bound (a period with
  no day inside is a 400 `window_out_of_bounds` as writes answer it), kept when
  `employedThroughout`; `workforceHeld` on the later of the request's first day and
  today; per person: `missing` (requirements not met: not held, or held below
  `minLevel`), excluded unless `includePartial`; `certificate_expires` for a
  requirement's certificate held with `validTo` before the request's last day;
  `requiredHours` = Σ over the period's days with capacity of `hoursPerWeek / 5`,
  `freeHours` = Σ `free` of the person's confirmed figures over the period (both totals,
  reading 1 below), `fits` when `freeHours ≥ requiredHours`; `fit` from one
  `workforceFits` call over every kept person with `Booking{From, To, HoursPerWeek}` (a
  person absent from the map scores 100 with nothing lowered); `currentProject` from
  the confirmed rows covering today (one query over the kept people), the earliest
  `from_date`'s project, `days` = today − from; `covers` = fits ∧ no flag ∧ nothing
  missing. Sort: fits first; fit score desc; free hours desc; current-project days
  desc (none = 0); display name; id.
- **Intake**: `projectsByStatus(["planned"])` by code, their open and filled requests;
  per request: `covered` when filled or ≥ 1 candidate covers, `partly_covered` when
  candidates exist but none covers, `uncovered` when none (the judgement runs with
  `includePartial` false: a candidate is a person who meets every requirement, as
  S17.1 excludes anyone missing one, so "uncovered" means nobody employed through the
  period holds the competences);
  `candidates` = how many cover; `key_person` when exactly one covers and the request
  is open. The project's `requestedHoursPerWeek` = Σ open requests' hours;
  `tentativeHoursPerWeek` / `confirmedHoursPerWeek` = Σ over its rows by status of the
  row's hours a week — `load` for an hours row, `load % × hoursPerWeekOn(segments,
  first day)` for a percent row (0 when Staff answers no capacity). Withdrawn requests
  are not listed.
- **Demand** (S11): per week, Σ over the project's open requests of
  `hoursPerWeek × (Mon–Fri days of the week inside the request's period) / 5`; the
  plan lists the project's open and filled requests touching the window, through
  `requestResponse`.
- **The item**: under `c.Manage`, one `requestsOpen` item per open request with
  `from_date ≤ today + requestLeadDays`, `entityId` the request's id, title
  "CODE — title" (the code when the directory resolves it, else the id), `occurredAt`
  the from date at midnight UTC. **The stat**: `openRequests` = count of open requests.
- **Tests**: each filter and flag, `includePartial`, the sort with ties (two people
  equal on fit and free hours ordered by current-project days), `PreferenceFits`'
  booking asserted through `lastBooking`, a person Staff does not answer absent, the
  three coverages and `key_person`, the project figures, demand for a partial week,
  the item within and beyond the lead, the stat.

### Task 4a — The app (`@vantigo/planning-ui`) (in parallel with 3)

Files: `apps/planning/frontend/src/{api/requests.ts, api/intake.ts,
api/competences.ts, api/stats.ts, api/request.ts, lib/routes.ts, lib/navigation.ts,
lib/search.ts, components/request-sheet.tsx, components/candidates-panel.tsx,
components/competence-picker.tsx, components/project-plan.tsx, pages/requests.tsx,
pages/intake.tsx, pages/project-plan.tsx, pages/board.tsx, index.ts, i18n.ts,
i18n.test.ts, test/*}` and the page tests.

- Exports the host needs (fixed here): `REQUESTS_ROUTE_PATH = "/planning/requests"`,
  `INTAKE_ROUTE_PATH = "/planning/intake"`, `RequestsPage`, `IntakePage`,
  `validateRequestsSearch` (status, projectId), the nav keys `requests` and `intake`
  (both `planning:view`, after `board`), and `ProjectPlan` (`components/project-plan.tsx`,
  props `{projectId: number}`) — the page `ProjectPlanPage` becomes a thin route wrapper
  around it, and the host's project tab renders it.
- The request sheet (a Drawer as the allocation sheet): title, project (the picker),
  period, hours a week, requirements from `GET /competences` through the package's own
  competence picker (rule 7: no import of `@vantigo/staff-ui`), each with a level when
  the competence has one, the note for `manage`; field errors land on their fields;
  `request_not_open` and `project_closed` as refusals. The competence picker also
  serves the board's filter (amendment 38): a select over the catalogue setting
  `competenceId` in the search.
- The candidates panel on a request: fit score, free hours of required, the flags
  (*Certificate expires DATE*), missing competences, *Show partial matches*, *Fill* as
  tentative or confirmed; the fill's warnings shown as the sheet shows them.
- Requests page: the list with status (open / filled / withdrawn), project, period,
  hours, requirements; filters by status and project; *New request*; a row opens the
  sheet, *Withdraw* on an open row, *Candidates* opens the panel.
- Intake page: the planned projects, each with its figures and its requests with the
  coverage badge (covered / partly covered / uncovered) and the key-person badge; a
  request's candidates from here for `manage`.
- Project plan: a **Demand** row under Booked and Tentative; a **Requests** card listing
  the open and filled ones with *Candidates* and *Withdraw*; *New request* beside *New
  allocation*.
- Strings en + nb: Requests / Behov, Intake / Inntak, Fill / Fyll, Withdraw / Trekk
  tilbake, covered / dekket, partly covered / delvis dekket, uncovered / udekket,
  Candidates / Kandidater, Show partial matches / Vis delvise treff, Demand /
  Behov (hours row: "Etterspurt"), Key person / Nøkkelperson, open / åpen, filled /
  fylt, withdrawn / trukket tilbake, the `requestsOpen` item title, the setting's
  description no longer "does nothing yet".

### Task 4b — The host (in parallel with 4a, against the exports above)

Files: `apps/host/frontend/src/{apps.ts, catalogs/navigation.ts, routes/planning/
requests.tsx, routes/planning/intake.tsx, routes/projects/$projectId.planning.tsx,
routes/projects/-project-planning-tab.tsx, routes/projects/-project-detail-layout.tsx,
catalogs/project.ts, lib/dashboard-helpers.ts, catalogs/dashboard.ts,
components/app-spotlight.tsx, routeTree.gen.ts}` and the tests
(`routes/planning-routes.test.ts`, `apps.test.ts`, `routes/projects/
project-detail-tabs.test.ts`, `routes/projects/project-planning-tab.test.tsx`,
`routes/dashboard.test.ts`, `components/app-spotlight.test.tsx`).

- The two sidebar entries (view) and routes; the project tab `{value: "planning",
  labelKey: "project.planningTab", to: "/projects/$projectId/planning", module:
  "planning", allPermissions: ["planning:access", "planning:view"]}` after Invoicing,
  gated through `ModuleTabGate` as Time's is, rendering `ProjectPlan`; the
  `requestsOpen` href (`/planning/requests`) and title key; the KPI's open requests
  line; the spotlight's *New request* (manage) opening Requests with the sheet
  (`?new=1`); `routeTree.gen.ts` regenerated.

### Task 5 — Docs (after 3 and 4; in parallel with the reviews)

Files: `docs/src/content/docs/en/reference/planning.md` (`sources` gains the
migration; Requests, Candidates, Intake sections; the plan's demand and requests; the
events, locks, refusals, stats and attention, permissions, endpoints, "What comes
next", the delivery markers), `en/reference/staff.md` ("The Workforce contract":
`PreferenceFits` on the interface replacing the shape block, the contradiction rule per
kind; "What comes next"), `en/reference/projects.md` (the tab sentence),
`en/contributing/module-boundaries.md` (rule 5's and rule 12's Workforce entries, the
Staff and Planning paragraphs), `{en,nb}/user/planning.md` (Requests, Intake, filling a
request, the project page tab, the sidebar table, the dashboard, who sees what, the
setting), `{en,nb}/user/index.md`, `{en,nb}/user/projects.md` (the tab row and the
permissions row), `{en,nb}/user/workspace-administration.md` (the permissions'
words), `ROADMAP.md` (B built), the spec's §13 (the amendments B takes).

### Task 6 — Reviews, gates, commits, PR

- An opus server review (2, 3a, 3b) and an opus apps review (4a, 4b) against the spec;
  fixers; the spec's §13 lists what the build changed.
- Gates: `mise run server:check`, `mise run server:test`, `bun run format:check`,
  `bun run translations:check`, `bun run i18n:test`, `bun run frontend:lint`,
  `bun run frontend:typecheck`, every frontend package's tests one after another,
  `bun run gen:client && git diff --exit-code -- '**/api-schema.d.ts'`,
  `cd docs && bun run build`, `bun run tools/docs/check-coverage.ts` (no
  `Docs-Impact: none` anywhere on the branch).
- Commits: `feat(planning): …`, `feat(planning-ui): …`, `docs(planning): …`;
  `gh pr create --base main`; CI through the app's PR status.

## Readings taken in planning

1. **Availability is judged on totals** (S17.2): `freeHours`, the person's free hours
   over the period, against `requiredHours`, what the request would book over it; a
   week-by-week judgement would make one week away a `partial` and hide a person who
   is free the rest of the year. Both figures are answered so the planner sees the
   margin.
2. **A filled or withdrawn request takes no change** (`PUT` answers
   `request_not_open`), so the filling row and the request cannot drift apart; the row
   is changed through its own `PUT`.
3. **Candidates are answered for a request in any status**: a read never refuses on
   the status, and a filled request's candidates show who else could have.
4. **The intake's hours a week per project** are the sum over its rows of each row's
   hours a week — an hours row its load, a percent row its share of the person's hours
   a week in force on the row's first day — since the pipeline wants one figure per
   project, not a window.
5. **Demand prorates a partial week** by weekdays: a request from Wednesday weighs
   3/5 of its hours in its first week, matching how an hours row books.
6. **`GET /competences` is `planning:view`'s** and answers Staff's catalogue as the
   caller may see it, hidden ones flagged so the picker offers none for a new
   requirement; the module adds no competence endpoint of its own beyond this read.
7. **The fill's status defaults by the project** as `POST /allocations` does, so a
   request on a planned project fills tentative unless the planner says confirmed.
8. **`request_filled` is filed under the project and the person**, so
   `GET /events?personId` shows the person was placed through a request;
   `request_reopened` under the project and the person of the deleted row.
9. **Intake judges coverage without partial matches** (S18 read with S17.1): a person
   missing a requirement is no candidate, so a request is `uncovered` when nobody
   employed through its period holds its competences, and `partly_covered` when
   someone does but nobody has the hours or is unflagged. Judging with partial matches
   included would have made "uncovered" mean only "nobody is employed", which is not
   what an intake asks.
