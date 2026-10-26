# Planning delivery A — the plan: implementation plan

Delivery A of the Planning module, from the design
`docs/superpowers/specs/2026-10-26-planning-design.md` ("the spec", decisions S*n*)
and the research `docs/superpowers/research/2026-10-26-planning-module.md` ("N").
Branch `claude/staffing-module-plan-af8b31`, opened against `main` (`9c4f1edd`) as one
pull request; migration **`00072_planning_baseline.sql`** (renumbered if main takes 72
first, see the memory on rebases). `srv/` abbreviates `apps/server/internal/`.

## What A ships

The module `planning` with `planning:access`, `planning:view` (sensitive) and
`planning:manage` (sensitive); the settings (S20); allocations — tentative and
confirmed, hours a week or percent, title, note — with the confirm, change, delete and
unconfirm mails (S1–S5, S19); the capacity calculation (S6) and overbooking in both
settings (S7); the three Workforce reads Staff adds and the widened rule (S8); the
listing Projects adds (S9); the board (S10), the project plan without requests (S11),
the person timeline without C's parts (S12), the unbooked list (S13), My planning
(S19), the events (S23), the four A items and the stats (S22); the app with the board,
project plan, person timeline, unbooked, My planning and settings; the host's tile,
routes, dashboard card and items, spotlight, permission labels; the `extraTabs` slot on
Staff's `PersonPage` and the Planning tab on the person page (§7); the docs (§8).

Out of A: requests, candidates, intake (B); terms, wishes, the calendar source, the
project page tab (C).

## The contract (`openapi/planning.yaml`), fixed here so every lane agrees

Base `/api/v1/planning`; every operation `x-vantigo-access: permission:planning:access`
plus `+planning:view` or `+planning:manage` as the table says; every request schema
`additionalProperties: false`; problems as `PlanningProblem` (apicommon's shape with
`code`); operation ids `<verb>Planning<Noun>`.

| Operation | Op id | Access |
| --- | --- | --- |
| `GET /meta` → `PlanningMeta {canView, canManage, hasPerson, workforceVisible, loadUnits, settings?}` | `getPlanningMeta` | access |
| `GET /settings` → `PlanningSettings` (S20 fields + `revision`) | `getPlanningSettings` | +manage |
| `PUT /settings` (If-Match) | `putPlanningSettings` | +manage |
| `GET /projects?status&q` → `{projects: [PlanningProject]}`; `PlanningProject {id, code, name, status, customer?: {id, name}, startDate?, endDate?}` | `listPlanningProjects` | +view |
| `GET /people?from&to&q&departmentId` → `{people: [PlanningPerson]}`; `PlanningPerson {id, displayName, employeeNumber, department?: {id, name}, employed: [{from, to}]}` | `listPlanningPeople` | +view |
| `GET /board?from&to&groupBy=people|project&departmentId&competenceId&projectId&includeTentative&includePlanned` → `PlanningBoard {from, to, weeks: [{monday}], rows: [PlanningBoardRow], allocations: [PlanningAllocation]}`; `PlanningBoardRow {person?: PlanningPerson, project?: PlanningProject, cells: [PlanningWeekFigures]}`; `PlanningWeekFigures {monday, capacity, available, booked, tentative, free, loadPercent?, away: bool}` | `getPlanningBoard` | +view |
| `GET /projects/{projectId}/plan?from&to` → `PlanningProjectPlan {project, allocations, weeks: [{monday, booked, tentative, demand}]}` | `getPlanningProjectPlan` | +view |
| `GET /people/{personId}/timeline?from&to` → `PlanningTimeline {person, employed, away: [{from,to}], allocations: [PlanningAllocation], flags: [{allocationId, code, date?}]}` | `getPlanningPersonTimeline` | access (self) / view |
| `GET /unbooked` → `{horizon: {from,to}, people: [{person, loadPercent, freeHours, rollOff?}]}` | `listPlanningUnbooked` | +view |
| `GET /allocations?personId&projectId&from&to&status` → `{allocations}` (≤ 500) | `listPlanningAllocations` | +view |
| `POST /allocations` `PlanningAllocationInput {personId, projectId, fromDate, toDate, loadUnit, load, status?, title?, note?}` → `PlanningAllocationWritten {allocation, warnings: [{code, weeks?: [monday], detail?}]}` | `createPlanningAllocation` | +manage |
| `GET /allocations/{id}` → `PlanningAllocation` | `getPlanningAllocation` | +view |
| `PUT /allocations/{id}` (If-Match) → `PlanningAllocationWritten` | `putPlanningAllocation` | +manage |
| `DELETE /allocations/{id}` → 204 | `deletePlanningAllocation` | +manage |
| `POST /allocations/{id}/confirm` → `PlanningAllocationWritten` | `confirmPlanningAllocation` | +manage |
| `GET /me` → `PlanningMe {person, allocations}` (confirmed only) | `getPlanningMe` | access |
| `GET /events?personId&projectId&allocationId` → `{events: [PlanningEvent]}` | `listPlanningEvents` | +view |
| `GET /stats` → `PlanningStats {peoplePlannedThisWeek, loadPercentThisWeek?, tentativeLoadPercentThisWeek?, unbooked, openRequests: 0}` | `getPlanningStats` | +view |
| `GET /stats/attention` → `{items: [PlanningStatsAttentionItem]}`; type enum `unbooked, overbooked, endingWithoutFollowOn, certificatesExpiring` | `getPlanningStatsAttention` | access |

`PlanningAllocation {id, person: {id, displayName?, employeeNumber?}, project: PlanningProject-lite {id, code?, name?, status?}, fromDate, toDate, loadUnit, load, status, title?, note? (manage only), confirmedAt?, revision, createdAt, updatedAt}`. Refusal codes and statuses are the spec's §4.7; warnings §4.6 (`overbooked`, `booked_while_away`, `outside_project_dates`).

## Tasks

### Task 1 — Dossiers (done 2026-10-26)

Three Explore dossiers: backend, apps, docs. Their findings are folded into the tasks
below.

### Task 2a — The seam: contracts

Files: `srv/contracts/workforce.go`, `srv/contracts/projects.go`,
`srv/module/compose_test.go` (the `fakeWorkforce` gains the methods).

- `Workforce`: `People(ctx, from, to) ([]PersonEntry, error)`, `Capacity(ctx, from, to,
  personIDs) (map[int64]PersonCapacity, error)`, `Self(ctx) (*PersonEntry, error)`;
  types `PersonEntry`, `DateSpan {From, To time.Time}`, `PersonCapacity`,
  `CapacitySegment {From, To time.Time; HoursPerWeek float64}`; the doc comments as the
  spec's S8 words them (the gate, the widened rule, the bound, the derived figures, the
  Self rule, `UserID` for mail alone); `HeldCompetences`' comment updated to the widened
  rule. `ErrWindowOutOfBounds` as a sentinel for the bound.
- `ProjectDirectory`: `ProjectsByStatus(ctx, statuses []string) ([]ProjectEntry,
  error)`, `const MaxDirectoryProjects = 1000`, `ProjectEntry.StartDate, EndDate
  *time.Time`; the comment declaring the read consumer-gated.

### Task 2b — The module's scaffold (after 2a; in parallel with 3a)

Files, all new unless noted: `srv/db/migrations/00072_planning_baseline.sql`
(schema, `settings` seeded, `allocations`, `events`; the house style of 00042: no CHECKs
but the settings' one-row index — the spec's CHECKs are enforced in Go);
`openapi/planning.yaml` (the whole table above); `srv/openapi/gen/cfg-planning.yaml`;
`apps/server/generate.go` (two lines); `srv/planning/sqlc.yaml` + `queries/*.sql`
(settings, allocations by person/project/window, events, locks); `go generate` →
`srv/planning/gen`, `srv/planning/store`; `srv/planning/{module.go, server.go,
authorize.go, errors.go, strictbody.go (copied from staff), locks.go, events.go,
settings.go, contractscalls.go, values.go, capacity.go, window.go, stubs.go}`;
registration: `apps/server/cmd/vantigo/main.go`, `srv/openapi/openapi.go` Modules,
`srv/config/config.go` (+ the two requires) and `config_test.go`, `srv/db/schema_test.go`,
`srv/module/compose_test.go` Modules literal, `apps/server/.golangci.yml` (planning's
rule set + the pair in every other set); `tools/openapi/gen-client.ts` + its test;
`bun run gen:client` → `apps/planning/frontend/src/api-schema.d.ts` (the package dir
exists from this task with only that file; 4a fills it).

- `capacity.go`: the pure function of S6 over `[]contracts.CapacitySegment`,
  `[]contracts.DateSpan` away, `holidays.PublicHoliday`, and the rows, answering per-day
  and per-week figures; `capacity_test.go` with a table per figure.
- `window.go`: `loadWindow(ctx, from, to, personIDs?)` — the people (`Workforce.People`),
  their capacity (`Workforce.Capacity`), both through `contractscalls.go` accessors before
  any transaction, then the allocations of the window from the store — the one reader 3b
  and 3c share. Also `projectEntries(ctx, ids)` and `projectsByStatus` accessors.
- `authorize.go`: `callerFor` loads View, Manage, Zone, Today, and (lazily) `Self`.
- `stubs.go`: every operation of the contract answering a typed 501-shaped problem so
  the module mounts and `TestEveryModuleLoadsAndValidates` passes; 3b/3c replace them
  file by file and delete `stubs.go` last.
- `main_test.go` + `harness_test.go` with `contracttest.NewForModule("planning")`,
  `modtest.New(t, modtest.WithModule(planning.Module()), modtest.WithProjects(fake),
  modtest.WithWorkforce(fake))` and the package-private `fakeWorkforce` and
  `fakeProjects` (seeded maps; a `lockedCalls` recorder as Time's) — the fakes live in
  `fakes_test.go` so 3b and 3c share them.

### Task 3a — Staff's and Projects' side (after 2a; in parallel with 2b)

Files: `srv/staff/workforce.go`, `srv/staff/workforce_test.go`, `srv/staff/queries/
workforce.sql` (new queries: people employed on or after today within a window with
their employment spans and department today, their linked user where the link gives
Self; terms in force per employment for the window; colleague absence days for the
window — reuse `ColleagueAbsenceDays` and `coalesceDays`), `srv/staff/export_test.go`
if a hook is needed; `srv/projects/directory.go`, `queries/directory.sql`
(`DirectoryProjectsByStatus` with `LIMIT @max_projects`), `directory_test.go`.

- `People`: gate `staff:view`; bound check (from ≥ Monday 13 weeks before today, to ≤
  today + 3 years, else `ErrWindowOutOfBounds`); people in employment on today or a
  later day in the window; `Employed` merged spans clipped to the window; department on
  today or the first employed day in the window; `UserID` from the self-service link
  under `PersonForUser`'s rule (state `linked`, or a worker link not blocked as
  `mismatch` / `account_not_managed`).
- `Capacity`: same gate and bound; segments from `termsInForce` × `FullTimeHoursPerWeek
  × FtePercent / 100`, summed across concurrent employments into non-overlapping
  segments; `Away` from `ColleagueAbsenceDays` → `coalesceDays` → clipped.
- `Self`: `staff:access` and `PersonForUser`; `(nil, nil)` otherwise.
- `HeldCompetences`: the person filter becomes "in employment on `on`" when `on` ≥
  today, else today's rule.
- Tests per the spec §10 (Staff paragraph).

### Task 3b — Allocations and their writes (after 2b; in parallel with 3c)

Files: `srv/planning/allocations.go`, `allocations_validation.go`,
`allocations_test.go`, `notifications.go`, `notifications_test.go`,
`queries/allocations.sql` additions as needed (own file), the matching stub removals.

- `POST`, `PUT` (If-Match), `DELETE`, `confirm`, `GET /allocations/{id}`,
  `GET /allocations`: S1–S7, §4.6–4.8, S23's person lock and lock order; project entry
  and capacity read before the transaction; the default status by project status;
  warnings; the mails through `Users.Notify` as the calendar module composes a Notice
  (`srv/calendar/notices.go`), to `PersonEntry.UserID`, after the commit; events.

### Task 3c — The reads (after 2b; in parallel with 3b)

Files: `srv/planning/{meta.go, projects.go, people.go, board.go, plan.go, timeline.go,
unbooked.go, me.go, events_read.go, stats.go, attention.go}` and their tests; the
matching stub removals; `stubs.go` deleted when the last stub goes.

- S9–S13, S19, S22, S23's `GET /events`; the timeline's `beyond_employment` and
  `certificate_expires` flags (`HeldCompetences` on `from`); the self audience through
  `Self` with confirmed rows only; `workforceVisible` on meta.

### Task 4a — The app (`@vantigo/planning-ui`) (after 2b's `api-schema.d.ts`)

Files: `apps/planning/frontend/**` (package.json, tsconfigs, vite config, eslint config
with the deny list naming every other package, README, `src/index.ts`, `src/i18n.ts` +
`i18n.test.ts`, `src/api/{request,meta,settings,projects,people,board,plan,timeline,
unbooked,allocations,me}.ts`, `src/lib/{routes,search,navigation,dates,weeks}.ts`,
`src/components/{allocation-sheet,project-picker,person-picker,load-cell,week-header}.tsx`,
`src/pages/{board,project-plan,person-timeline,unbooked,me,settings}.tsx` with tests,
`src/test/{setup,fetch,server,route-tree,helpers}.ts`).

- Exports the host needs (fixed here): `planningCatalog`, `planningNavItems` /
  `PlanningNavItem` / `visiblePlanningNavItems` (`lib/navigation.ts`), the route path
  constants (`lib/routes.ts`: `BOARD_ROUTE_PATH = "/planning"`, `/planning/projects/$projectId`,
  `/planning/people/$personId`, `/planning/unbooked`, `/planning/me`, `/planning/settings`),
  the search validators (`lib/search.ts`), the pages `BoardPage`, `ProjectPlanPage`,
  `PersonTimelinePage`, `UnbookedPage`, `MyPlanningPage`, `PlanningSettingsPage`, and
  `PersonTimeline` (`components/person-timeline.tsx`, props `{personId: number}`) for
  the Staff tab.
- Week arithmetic in `lib/weeks.ts` (the package cannot import time-ui's): `mondayOf`,
  `addDays`, `weeksBetween`, business-zone `today()` via `civilDateIn`.
- The board as `Table.ScrollContainer` + `Table` (Time's people page is the model), the
  allocation sheet as a Mantine `Drawer` (Staff's request sheet).
- Strings in `en` and `nb` (Bemanning; Plan / Planen, Prosjektplan, Tidslinje, Ubemannet
  for unbooked, Min bemanning for My planning, Bemanningsinnstillinger; tentativ /
  bekreftet; "oppdrag" reserved for C).

### Task 4b — The host and the Staff slot (after 4a's exports exist)

Files: `apps/host/frontend/src/{navigation.ts, apps.ts, i18n.ts, routeTree.gen.ts,
routes/planning.tsx, routes/planning/{index,projects.$projectId,people.$personId,
unbooked,me,settings}.tsx, routes/staff/people.$id.tsx, routes/dashboard.tsx,
lib/dashboard-helpers.ts, api/dashboard.ts, catalogs/{admin,navigation,dashboard}.ts,
components/app-spotlight.tsx}` and their tests (`apps.test.ts`, `navigation.test.ts`,
`routes/dashboard.test.ts`, `catalogs/admin.test.ts`, `components/app-spotlight.test.tsx`,
a `routes/planning-routes.test.ts`); `apps/host/frontend/package.json` (the workspace
dep); `apps/staff/frontend/src/pages/person.tsx` (the `extraTabs` prop: `{value: string,
label: ReactNode, render: () => ReactNode}[]`, appended after `account`),
`apps/staff/frontend/src/lib/search.ts` (`validatePersonSearch` keeps an unknown tab
value that matches `^[a-z]+$` instead of dropping it — pin in its test), the deny list
in every other package's `eslint.config.js` (`@vantigo/planning-ui` and
`../../../planning/**`), `tools/openapi/gen-client.test.ts` length 13.

- `routeTree.gen.ts` regenerated with `bunx vite build --outDir /tmp/… ` in the host.
- The dashboard: the card (`"dashboard.planning"`, `"dashboard.managePlanning"`,
  `IconCalendarStats`, `["planning:access"]`), `fetchPlanningStats`, the KPI
  (`peoplePlannedThisWeek`, load), `fetchAttention("planning")`, the four types' hrefs
  and title keys; the enum test reads `openapi/planning.yaml` as it reads staff's.

### Task 5a — Reference, boundaries, roadmap (after 2b; in parallel with 3)

Files: `docs/src/content/docs/en/reference/planning.md` (new; `sources`:
`apps/server/internal/planning`, `apps/planning/frontend`, `openapi/planning.yaml`,
`apps/server/internal/openapi/specs/planning.yaml`, the migration), `reference/index.md`
(the row, and Staff's stale row), `reference/staff.md` ("The Workforce contract":
the three reads, the widened rule, the bound, "with the delivery that matches";
"What comes next"; the person page's Planning tab; the `sources` gain nothing),
`reference/projects.md` ("Contracts for other modules": `ProjectsByStatus`, the dates,
consumer-gated), `contributing/module-boundaries.md` (the module list at 26, rule 4's
schema list incl. the missing `entra`, rule 5's Workforce entry, rule 7's slot note,
rule 12's Workforce paragraph, "Turning a module off": default list, requires, the
Planning paragraph, the Staff paragraph's "which does not exist yet"), `ROADMAP.md`
(the Planning section after Staff, modelled on Entra's opening; the Staff "Later"
Staffing paragraph rewritten; the four other "Staffing" mentions; Projects phase 5
marked moved), `docs/astro.config.mjs`, the spec's §13 amendments.

### Task 5b — User and administration pages, deployment (after 4a; in parallel with 4b)

Files, each in `en` and `nb`: `user/planning.md` (new; `sources`:
`apps/planning/frontend`, `apps/host/frontend/src/routes/planning`), `user/index.md`
(the apps sentence, the card paragraph, the attention items, the table row),
`user/staff.md` (the person page's Planning tab), `user/getting-started.md` (the
tile; fix "Staff has no numbers yet"), `user/workspace-administration.md` (the three
permissions), `admin/installation.md` (default modules, `MODULES` bullet with the
two requires, the schema list, the `GRANT` lines, the API prefixes incl. the missing
calendar and entra, an upgrading paragraph "The release with Planning");
`deploy/compose/vantigo.env.example`, `deploy/compose/README.md`, `README.md`,
`CONTRIBUTING.md`.

### Task 6 — Reviews, gates, commits, PR

- An opus server review of 2a–3c and an opus apps review of 4a–4b against the spec;
  fixers for their findings; the spec's §13 lists what the build changed.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`
  (the Node 26 quirk aside, then the remaining packages by hand), `mise run docs:check`,
  `bun run gen:client` clean, `bun run translations:check`, `bun run i18n:test`.
- Commits: `feat(planning): …` (server, contracts, staff, projects, migration, openapi),
  `feat(planning-ui): …` (the app, the host, the staff slot), `docs(planning): …`
  (every page, the roadmap, the plan). The husky hook checks the whole tree: commit
  per group once the tree is clean.
- `gh pr create --base main`; CI green.

## Readings taken in planning

1. **Fakes live in the module's tests**, as Time's `fakeProjects` does, not in
   `modtest`: `modtest` has none today and a shared fake would need every method's
   rule restated.
2. **The spec's CHECK constraints become Go validation** with field errors, the house
   style of 00042; the settings' one-row index is the exception, as in Staff.
3. **`validatePersonSearch` must stop dropping unknown tabs** for the slot to be
   addressable by URL; it keeps lowercase words and the page falls back to `profile`
   when no tab matches, as it does today.
4. **The planning tab is rendered by the host route**, which passes `extraTabs` to
   `PersonPage`, so `@vantigo/staff-ui` never names `@vantigo/planning-ui`.
5. **`openRequests: 0` is on the stats from A** so the dashboard card's shape does not
   change in B.
