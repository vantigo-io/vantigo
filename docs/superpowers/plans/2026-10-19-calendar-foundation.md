# Calendar phase 1 delivery A — the calendar: implementation plan

**Goal:** the `calendar` module and `@vantigo/calendar-ui`: appointments with
recurrence and colleagues, the `CalendarSource` contract with five providers
(Customers, Staff, Projects, Time, Expenses), a windowed read of one person's calendar
in their zone, a colleague's busy/free view, month, week, day and list views, and the
dashboard's "Today and this week" agenda.

**Spec:** [`docs/superpowers/specs/2026-10-19-calendar-design.md`](../specs/2026-10-19-calendar-design.md)
(D1–D8, with D9 deciding B and C and D10 the docs).

**Pattern code:** `internal/expenses/module.go`, `server.go`, `authorize.go`,
`contractscalls.go`, `errors.go` (a module that depends on identity alone);
`internal/staff/settings.go` (`parseIfMatch`, `withLockedTx`), `calendar_view.go` (a
93-day window), `notifications.go` (the `smtpSend` accessor, for B); `internal/customers/
overview.go` (sections absent under a gate, a contract failure failing the request);
`internal/module/module.go`, `compose.go`, `workers.go` (the slots and how `Workers` is
collected from enabled modules only); `internal/contracts/references.go` (a many-provider
contract); `internal/identity/businesscalendar.go` (zone validation);
`docs/src/content/docs/en/contributing/module-boundaries.md` §"Adding a module" (steps
1–11); `apps/expenses/frontend` (the package shape, `test/`, `eslint.config.js`);
`apps/staff/frontend/src/components/request-sheet.tsx` (a drawer-or-modal sheet);
`apps/host/frontend/src/routes/dashboard.tsx` (`allowed("x") && <WidgetCard>`),
`apps.ts`, `navigation.ts`, `routes/expenses.tsx`; `cmd/vantigo/seed.go`.

## The order

Task 2 first: everything else reads the contract it writes. Then 3–4 (backend), 5
(providers), 6–7 (frontend) and 8 (docs) in parallel; 9 last.

## Tasks

- [x] **1. The spec and this plan.**
- [ ] **2. The module's skeleton and the contracts.** `openapi/calendar.yaml` (D3's
  operations and D5's shapes, `x-vantigo-access` on each, `CalendarProblem` with its
  codes); `internal/openapi/openapi.go`'s `Modules`; `internal/openapi/gen/
  cfg-calendar.yaml` and `generate.go`'s two lines; `internal/contracts/
  calendar_source.go` (D1's types and interface); `module.Deps.CalendarSources`,
  `module.Module.CalendarSource`, collected in `compose.go` and `workers.go` from
  enabled modules only, with `compose_test.go`; `.golangci.yml` (the `calendar` rule
  set, two deny lines in every other module's and in `platform`'s); `internal/db/
  schema_test.go`'s `moduleSchemas`; `config_test.go`'s known list; `cmd/vantigo/
  main.go`'s `businessModules`; `tools/openapi/gen-client.ts`'s target and its test;
  `docs/astro.config.mjs`'s `contracts`; `internal/calendar/module.go` with the two
  permissions and an empty mount so `go generate ./...` and `bun run gen:client` run
  clean. Migration `00055_calendar_baseline.sql` (D2's three tables) with the pin in
  `internal/db/migrations_test.go`; `internal/calendar/sqlc.yaml` and `queries/
  appointments.sql`, `attendees.sql`, `exceptions.sql`, `items.sql`; `sqlc generate`.
- [ ] **3. The backend, tests first.** `main_test.go` (`contracttest.NewForModule`,
  `RequireCoverage`), `harness_test.go`, `fakes_test.go` (a fake `CalendarSource` per
  kind); `appointments_test.go`, `recurrence_test.go`, `items_test.go`,
  `colleagues_test.go` (the spec's testing section); `server.go`, `authorize.go`
  (`callerFor`: the principal, `canViewColleagues`, the business zone), `contractscalls.go`
  (`sources`, `usersBatch`, `directoryCustomer`, `projectsProject`, each refusing a call
  inside a transaction), `errors.go` (`CalendarProblem`), `values.go` (the bounds, the
  zone checks shared with identity's wording), `recurrence.go` (the subset, canonical
  form, `series_last_date`, expansion in the zone with `teambition/rrule-go`),
  `appointments.go` (CRUD, the response shape, the attendee reset and the `sequence`
  bump, `withLockedTx`), `occurrences.go` (the exception endpoints), `responses.go`,
  `items.go` (the window, the appointments' expansion, the providers under an
  `errgroup`, the holidays, the sort), `colleagues.go`, `meta.go`.
- [ ] **4. The five providers, tests in each module.** `internal/customers/
  calendar_source.go` with `queries/calendar.sql` (follow-ups assigned to the caller in
  the window on a live customer; the caller's interaction entries), gated on the
  timeline pair; `internal/staff/calendar_source.go` (the link, own absences
  overlapping, the type's name by language, `queries/calendar.sql`); `internal/projects/
  calendar_source.go` (tasks assigned with `due_date` in the window under
  `projects.visible()`, `queries/calendar.sql`); `internal/timetracking/
  calendar_source.go` (entries in the range, the block from the wall-clock pair in the
  business zone); `internal/expenses/calendar_source.go` (claims overlapping the
  window); each module's `module.go` fills `CalendarSource`; a `calendar_source_test.go`
  in each, through its own harness.
- [ ] **5. The frontend package, tests first.** `apps/calendar/frontend` (package.json
  `@vantigo/calendar-ui` with the FullCalendar packages, tsconfigs, `vite.config.ts`
  with `TZ` off Oslo, `eslint.config.js`, `src/test/*`); **the spike first**:
  `pages/calendar.tsx` with FullCalendar's week and month views inside the shell and
  `styles/fullcalendar.css` mapping its variables to Mantine's — judged in the browser
  before the rest; `api/request.ts`, `api/meta.ts`, `api/items.ts`,
  `api/appointments.ts`; `lib/routes.ts`, `lib/search.ts`, `lib/recurrence.ts` (rule ↔
  control), `lib/colours.ts`, `lib/filter.ts` (localStorage); `components/
  appointment-sheet.tsx`, `recurrence-control.tsx`, `occurrence-question.tsx`,
  `source-legend.tsx`, `colleague-picker.tsx`, `agenda-widget.tsx`; `i18n.ts` en + nb;
  `index.ts`; tests beside each; `@vantigo/calendar-ui` added to every other package's
  `eslint.config.js` deny list.
- [ ] **6. The host.** `package.json`; `navigation.ts`'s `moduleKeys`; `apps.ts` (the
  app, its one nav item) and `apps.test.ts`; `routes/calendar.tsx` (`appLayoutOptions`)
  and `routes/calendar/index.tsx` with the search validator; `i18n.ts`'s import;
  `catalogs/navigation.ts`, `catalogs/dashboard.ts`, `catalogs/admin.ts` (the two
  permissions); `routes/dashboard.tsx` (`allowed("calendar") && <AgendaWidget>` at the
  top) and its test; `lib/dashboard-helpers.ts` (the module card); the **New
  appointment** action on the customer page's overview and the project page's header,
  gated as the cross-module tabs are; `app-spotlight.tsx`'s quick action.
- [ ] **7. The demo seed.** `seedCalendar` in `cmd/vantigo/seed.go`: a handful of
  appointments per demo user over the demo weeks, one weekly series, one with
  colleagues, one from a customer.
- [ ] **8. Docs, both languages.** `en/reference/calendar.md` (new) and `reference/
  index.md`; `{en,nb}/user/calendar.md` (new), `{en,nb}/user/index.md`,
  `{en,nb}/user/getting-started.md`; `{en,nb}/admin/index.md`;
  `en/contributing/module-boundaries.md` (the slot, the provider rule, the checklist);
  the "On the calendar" paragraph in `en/reference/customers.md`, `staff.md`,
  `projects.md`, `time.md`, `expenses.md`; `ROADMAP.md` (the Calendar section, A built;
  the Platform section's last sentence).
- [ ] **9. Gates and the pull request.** `go generate ./...` and `bun run gen:client`
  clean; `mise run server:check`, `server:test`, `frontend:check`, `docs:check`;
  Conventional Commits; the PR on `feat/calendar-foundation` against `main` with CI
  green.
