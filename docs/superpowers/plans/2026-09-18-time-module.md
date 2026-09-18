# Time Module Implementation Plan (PR B of Time tracking and tasks)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the `time` business module — time entries with snapshotted rates, person rate cards, weekly submission, per-entry approval and a period lock — with the Time app in the switcher, a Time tab on the project page and a dashboard card.

**Architecture:** A new vertical slice like `projects` (Go package `internal/time`, schema `time`, contract `openapi/time.yaml`, package `@vantigo/time-ui`), following `docs/module-boundaries.md`'s "Adding a module" checklist exactly as the projects module did (`docs/projects.md` describes the pattern; `git log --stat 390bfc4` shows every registration point). Time reads projects only through `contracts.ProjectDirectory`, users through `contracts.UserDirectory`, product list prices through the optional `contracts.ProductCatalog`. Time provides no contract yet.

**Tech Stack:** Go (pgx v5, sqlc, goose, oapi-codegen strict server), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-18-time-tracking-and-tasks-design.md` (delivery B: §2 D1–D4, D7–D10, §3.2, §4.2–4.3, §5, §6, §7 Time, §8 Time app, §9). Read `docs/projects.md` for the conventions it inherits.

## Global Constraints

- **Pattern module:** `apps/server/internal/projects` (module.go, server.go, authorize.go, errors.go, values.go, responses.go, handlers, `harness_test.go`, `main_test.go`, `gen/gen_test.go`, `sqlc.yaml`, `queries/`) and `apps/projects/frontend` + `apps/host/frontend/src/routes/projects*`. Registration points for a new module: `internal/openapi/openapi.go` `Modules`, `internal/config/config.go` dependency check (`time requires projects`) + tests, `cmd/vantigo/main.go` `businessModules`, `apps/server/generate.go` (oapi-codegen + sqlc lines), `internal/openapi/gen/cfg-time.yaml`, `.golangci.yml` depguard (a rule set for time AND time in every other module's deny list, two entries each), `internal/db/schema_test.go` `moduleSchemas`, tests enumerating module lists, `tools/openapi/gen-client.ts` target + test, host `package.json` dependency, every other module package's eslint `forbiddenModuleImports`, host `navigation.ts` `moduleKeys`, `apps.ts`, layout route, i18n import, catalogs incl. admin permission labels, dashboard card, spotlight, docs (`docs/time.md`, `docs/module-boundaries.md`, `CONTRIBUTING.md`, `README.md`, `deploy/compose/*` env examples and READMEs that enumerate modules/schemas, `docs/customers-authentication.md` MODULES default).
- **Boundaries:** `internal/time/**` imports no other module (tests use fakes: a fake `ProjectDirectory`, fake `ProductCatalog`; the real `UserDirectory` is composed); no SQL crosses schemas; module packages import neither each other nor the host.
- **Toolchain:** `mise exec --` for go/sqlc/golangci-lint/bun; `golangci-lint run` from `apps/server` only; `bun run --cwd <dir> test`; capture exit codes. `TEST_DATABASE_URL` per the ledger's environment note. After `openapi/time.yaml` changes: `cd apps/server && mise exec -- go generate ./...` and `mise exec -- bun run gen:client`; commit results. After host route changes: `mise exec -- bun run --cwd apps/host/frontend build`, commit `routeTree.gen.ts`. After exported Go signature changes: `mise exec -- go vet ./...`.
- **Contract coverage gate:** every operation in `time.yaml` exercised by a passing test.
- **Exact values:** statuses `draft | submitted | approved | rejected | invoiced`; `rateSource` `line | project | person | none`; hours `> 0`, `<= 24`, two decimals, per-person-per-day total `<= 24`; `startTime`/`endTime` both or neither, same day, end > start, `hours` must equal the difference (to two decimals) or the request is refused on `hours`; note ≤ 2000; rejection reason ≤ 1000, required on reject; week starts on Monday (`weekStart` must be a Monday); permissions `time:access`, `time:approve`, `time:view-all`, `time:manage`; money float64/JSON number; user IDs uuid; project/line/task IDs int32; entry IDs int64.
- **Authorization (spec D7, D8, §4.2, §6):** `time:access` on every operation; owner-only content edits in `draft`/`rejected`; approvers = project managers (role via `ProjectDirectory.Role`) or `time:approve`; `time:view-all` sees everyone; `time:manage` = rates, lock, unapprove, edit past the lock. Bill rate visible to the owner and to callers who may see the project's financials (project manager, `projects:view-financials`, `projects:manage-all` — checked via `contracts.HasPermission` and `Role`); cost rate only to `time:manage`/`time:view-all`. Entries on projects the caller may not see are 404 (bare, identical to unknown).
- **Rates (D3):** resolved at every save while `draft`/`rejected`; frozen from `submitted`: bill (if billable) = line rule (`fixed` amount; `list`/`discount` via `ProductCatalog.ListPrice` in the project currency at the entry date, discount applied) → project `DefaultBillRate` → person bill rate effective on the date → none; cost = person cost rate effective on the date → none. Currency: project's for bill, person card's for cost.
- **i18n:** en + nb for every string; `translations:check` passes.
- **Commits:** Conventional Commits (`feat(time): …`, `feat(time-ui): …`, `feat(frontend): …`) ending with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; never `--no-verify`.
- **Branch:** `feat/time-module` (based on `feat/project-tasks`, PR #100); never commit to `main`.

## File Structure

```
apps/server/internal/db/migrations/00010_time_baseline.sql
openapi/time.yaml
apps/server/internal/openapi/gen/cfg-time.yaml
apps/server/internal/time/
  module.go          permissions, Module(), mount
  server.go          server struct, newServer, helpers (userEntries, project access cache)
  authorize.go       entryAccess(): owner/approver/financial visibility for an entry's project
  values.go          enums, hours/time rules, currency, validation helpers
  rates.go           rate resolution (D3) + person-rate lookup
  entries.go         list/create/update/delete
  weeks.go           my-week rows + submit week
  approval.go        submit/approve/reject/unapprove (batch), approval queue
  people.go          people overview (view-all)
  ratecards.go       person rates CRUD
  settings.go        lock date
  stats.go           dashboard shapes + project summary
  responses.go       row → gen mapping with shaping
  errors.go
  queries/*.sql, store/, gen/, sqlc.yaml, *_test.go, harness_test.go, main_test.go
apps/time/frontend/                       @vantigo/time-ui (mirror apps/projects/frontend)
  src/api/{entries,weeks,approvals,people,rates,settings,stats,projects}.ts  (projects.ts = cross-module HTTP reads with local types)
  src/lib/{status,week,hours}.ts
  src/pages/{my-week,day,approvals,people,settings}.tsx, -entry-form-modal.tsx, -row-picker.tsx
  src/components/{project-time-panel,entry-status-badge,hours-cell}.tsx
apps/host/frontend/src/{navigation.ts,apps.ts,routes/time.tsx,routes/time/*,routes/projects/$projectId.time.tsx,-project-detail-layout.tsx,components/app-spotlight.tsx,routes/dashboard.tsx,i18n.ts,catalogs/*}
docs/time.md, docs/module-boundaries.md, CONTRIBUTING.md, README.md, ROADMAP.md, deploy/compose/*
```

---

## Slice 1 — Backend

### Task 1: Module scaffold, schema, entries create/get/delete

**Files:** the migration, `time.yaml` (entries create/get/delete only), `cfg-time.yaml`, `sqlc.yaml`, `module.go`, `server.go`, `authorize.go`, `values.go`, `rates.go`, `entries.go`, `responses.go`, `errors.go`, `queries/{entries,rates,settings}.sql`, `gen/gen_test.go`, `harness_test.go`, `main_test.go`, `entries_test.go`, `rates_internal_test.go`; every backend registration point listed in Global Constraints (config check + test, openapi.Modules, main.go, generate.go, depguard both directions, schema_test moduleSchemas, module-list tests, `modtest.modulesEnv` comment).

**Migration (complete):**

```sql
-- +goose Up
CREATE SCHEMA time;

-- Every foreign identifier here is opaque: users, projects, billing lines and
-- tasks live in other schemas (docs/module-boundaries.md rule 4).
CREATE TABLE time.entries (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id             uuid          NOT NULL,
    project_id          integer       NOT NULL,
    billing_line_id     integer,
    task_id             integer,
    task_title          varchar(200),
    entry_date          date          NOT NULL,
    hours               numeric(5,2)  NOT NULL,
    start_time          time,
    end_time            time,
    note                varchar(2000),
    billable            boolean       NOT NULL,
    bill_rate           numeric(12,2),
    bill_currency       char(3),
    cost_rate           numeric(12,2),
    cost_currency       char(3),
    rate_source         varchar(20)   NOT NULL,
    status              varchar(20)   NOT NULL DEFAULT 'draft',
    rejection_reason    varchar(1000),
    submitted_at        timestamptz,
    approved_by_user_id uuid,
    approved_at         timestamptz,
    invoiced_at         timestamptz,
    revision            integer       NOT NULL DEFAULT 1,
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL
);
CREATE INDEX ix_entries_user_id_entry_date ON time.entries (user_id, entry_date);
CREATE INDEX ix_entries_project_id_entry_date ON time.entries (project_id, entry_date);
CREATE INDEX ix_entries_status_entry_date ON time.entries (status, entry_date);

CREATE TABLE time.person_rates (
    id         integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    user_id    uuid          NOT NULL,
    valid_from date          NOT NULL,
    bill_rate  numeric(12,2),
    cost_rate  numeric(12,2),
    currency   char(3)       NOT NULL,
    created_at timestamptz   NOT NULL,
    updated_at timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_person_rates_user_id_valid_from ON time.person_rates (user_id, valid_from);

CREATE TABLE time.week_submissions (
    user_id      uuid        NOT NULL,
    week_start   date        NOT NULL,
    submitted_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, week_start)
);

CREATE TABLE time.settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- +goose Down
DROP SCHEMA time CASCADE;
```

**Contract shapes** (copy `projects.yaml`'s conventions for problems, `x-vantigo-access`, pagination):

```
TimeEntryResponse {
  id int64, userId uuid, userDisplayName, projectId int32, projectCode, projectName,
  billingLineId? int32, billingLineCode?, trackableCode? ("KVEM1000-PM"), taskId? int32, taskTitle?,
  entryDate date, hours number, startTime? ("HH:MM"), endTime?, note?, billable bool,
  rateSource (line|project|person|none), status, rejectionReason?, submittedAt?, approvedBy?: {userId, displayName}, approvedAt?, invoicedAt?,
  revision int32, createdAt, updatedAt,
  capabilities: { canEdit bool, canSubmit bool, canApprove bool, canUnapprove bool },
  billing?: { billRate? number, currency? }        // ABSENT unless owner or may-see-financials
  cost?:    { costRate? number, currency? }        // ABSENT unless time:manage / time:view-all
}
TimeEntryRequest { projectId, billingLineId?, taskId?, entryDate, hours, startTime?, endTime?, note?, billable? }   // POST
TimeEntryUpdateRequest = TimeEntryRequest + revision                                                             // PUT (full replace)
```

**Rate resolution (`rates.go`, complete semantics):** `resolveRates(ctx, q, entry, project *ProjectEntry, line *BillingLineEntry, date)`:
- bill: if `!billable` → nil/`none`; else if line != nil: `fixed` → line.FixedAmount in project currency, source `line`; `list`/`discount` → `Products.ListPrice(line.VariantID, *project.Currency, dateAtNoon)` (nil catalog or nil price → fall through), discount applied `amount * (1 - pct/100)` rounded to 2 decimals, source `line`; else if `project.DefaultBillRate != nil && project.Currency != nil` → source `project`; else person bill rate effective on date (latest `valid_from <= date`) → source `person`; else nil, source `none`. Cost: person cost rate effective on date → else nil.
- The snapshot is written on create and on every update while `draft`/`rejected`.

**Authorization (`authorize.go`):** `entryAccess(ctx, entry)`: `isOwner`; `canSeeProject` = `Role != ""` or `projects:view-all`/`projects:manage-all` (via `Deps.Access`); `isApprover` = role manager or `time:approve`; `canSeeBilling` = owner || manager || `projects:view-financials` || `projects:manage-all`; `canSeeCost` = `time:manage` || `time:view-all`; `canEdit` = owner && status in {draft, rejected} && date not locked (or `time:manage`); `canSubmit` = owner && draft; `canApprove` = isApprover && submitted; `canUnapprove` = (isApprover || time:manage) && approved. An entry whose project the caller cannot see and which they do not own → bare 404. Non-owner readers with `time:view-all` see everything; without it, only entries on projects they can see AND (own entries, or approver of that project, or `projects:view-financials`/`manage-all`) — decide: **a project member sees only their own entries; a project manager sees all entries on the project; `time:view-all` sees all.**

**Create rules (§4.2, D1):** `CanLogTime(projectId, caller)` else 400 on `projectId` ("you cannot log time on this project" — do not distinguish unknown from not-allowed beyond that message); line must belong to the project and be active (400 on `billingLineId`); task must belong to the project (400 on `taskId`), title snapshotted; hours rule; day cap (sum of the caller's other entries that day, inside the transaction with an advisory lock keyed on (user, date) or `SELECT … FOR UPDATE` of the day's rows); lock date; billable default from billing type (`non-billable` ⇒ false forced).

- [ ] **Step 1:** scaffold + registrations; `go generate`; copy `gen_test.go`/`main_test.go`; `harness_test.go` with fake `ProjectDirectory` (projects 1001 active NOK with `DefaultBillRate` 900, member/manager/viewer users configurable per test; 1002 active non-billable; 1003 `completed`; 1004 active EUR without default rate; lines 3001 `fixed` 1500 on 1001, 3002 `list` variant 2001 on 1001, 3003 inactive; tasks 5001/5002 on 1001) and fake `ProductCatalog` (variant 2001 list 1600 NOK / 1400 EUR); helpers `signIn` (always adds `time:access`), `createEntry`, `getEntry`, `seedRate(userID, validFrom, bill, cost, currency)` via `h.Exec`, `setLock(date)`.
- [ ] **Step 2: Failing tests** (`entries_test.go`, `rates_internal_test.go`): create by member → 201 with snapshot from the fixed line (1500 NOK, `line`); list line → 1600 NOK via catalog; discount 10% → 1440; no line → project default 900; project 1004 without default → person rate; no person rate → `none`; products off (harness without catalog) + list line → `none`; non-billable project forces `billable:false` and no bill rate; viewer → 400 on `projectId`; completed project → 400; foreign line / inactive line / foreign task → 400 on the field; hours rules table (0, 24.5, -1, 3 decimals, start/end mismatch, crossing midnight, only one of start/end); day cap (23 + 1.5 → 400 on `hours`); two racing creates that together exceed 24 → one 201, one 400; get: owner 200, another member 404, project manager 200 with `billing`, `time:view-all` 200 with `cost`, member's own entry has `billing` but no `cost` (raw JSON); delete draft → 204, delete after submit → 403 (Task 2 covers submit; here set status via `h.Exec`); lock: entry before lock → 400 on `entryDate` for the owner, 201 for `time:manage`; `MODULES=time` without projects → config problem; `taskTitle` snapshot survives fake-directory task removal.
- [ ] **Step 3–4:** implement; run `./internal/time/... ./internal/config/... ./internal/db/... ./internal/module/... ./internal/openapi/... ./internal/modtest/... ./internal/server/... ./cmd/...`, lint, vet; `bun run gen:client` no drift (no frontend target yet).
- [ ] **Step 5: Commit** `feat(time): module scaffold, time entries and rate snapshots`.

### Task 2: Update, list, weeks and submission

**Files:** `entries.go` (+update, list), `weeks.go`, `queries/{entries,weeks}.sql`, `time.yaml`, tests `entries_update_test.go`, `entries_list_test.go`, `weeks_test.go`, `weeks_concurrency_test.go`.

Operations:
- `PUT /time/entries/{id}` (owner, draft/rejected, revision-guarded 409; rejected → draft and reason cleared; rates re-resolved).
- `GET /time/entries?userId&weekStart&projectId&status&page&pageSize` (visibility per authorize; `userId` defaults to self; another user's requires view-all or approver/financial rights on the filtered project).
- `GET /time/weeks/{weekStart}` → `{ weekStart, submittedAt?, hasUnsubmittedChanges bool, rows: [ { projectId, projectCode, projectName, billingLineId?, trackableCode?, taskId?, taskTitle?, days: [ { date, entries: [TimeEntryResponse] } ×7 ] } ], totals: { perDay: [7], week } }` — the caller's own rows (weekStart must be a Monday else 400).
- `POST /time/weeks/{weekStart}/submit` → the week response; moves all the caller's `draft` entries in that week to `submitted`, records/updates `week_submissions`; empty weeks allowed; entries dated before the lock refuse (400) unless none.
- `POST /time/entries/submit` `{ ids }` → `[TimeEntryResponse]` (owner, draft).

- [ ] **Step 1: Failing tests**: update re-resolves rates while draft (change line → new snapshot), stale revision 409, update after submit 403, editing rejected → draft with reason cleared; list filters and visibility (member sees own only; manager sees all on project; view-all sees all; `userId` of another user without rights → 403); week rows grouping and totals; submit week moves only drafts, records submission, `hasUnsubmittedChanges` after adding a later entry; submit ids; concurrency: submit week racing an edit (one wins, the other 409/403 cleanly).
- [ ] **Step 2–4.** **Step 5: Commit** `feat(time): entry updates, listing, the week view and submission`.

### Task 3: Approval, unapprove, queue, people, rates, settings

**Files:** `approval.go`, `people.go`, `ratecards.go`, `settings.go`, SQL, `time.yaml`, tests.

Operations:
- `POST /time/entries/approve` `{ ids }`, `/reject` `{ ids, reason }`, `/unapprove` `{ ids }` → `[TimeEntryResponse]`; each id must be in the right state and the caller an approver for its project (all-or-nothing per request: any refusal → 400 listing the offending ids on `ids`; nothing changes).
- `GET /time/approvals?page&pageSize` → submitted entries the caller may approve, grouped `[ { userId, displayName, weekStart, hours, entries: [...] } ]`.
- `GET /time/people?weeks=4` (`time:view-all`) → per user `[ { userId, displayName, weeks: [ { weekStart, hours, submittedAt?, approvedHours, rejectedCount } ] } ]`.
- `GET/POST /time/rates`, `PUT/DELETE /time/rates/{id}`, `GET /time/rates/users/{userId}` (`time:manage`): `{ userId, validFrom, billRate?, costRate?, currency }`, unique (user, validFrom) → 400 on `validFrom`.
- `GET /time/settings` (access; `{ lockedBefore? }`), `PUT /time/settings` (`manage`; `{ lockedBefore? }` null clears).

- [ ] **Step 1: Failing tests**: approve by project manager / by `time:approve` / by member → 403; approve a draft → 400; reject requires reason; rejected shows in owner's week with reason; unapprove by approver and `time:manage`; invoiced entries (set via `h.Exec`) refuse every transition; the all-or-nothing batch; queue grouping and scoping (manager sees only their projects; `time:approve` sees all); people overview numbers; rates CRUD + effective-date resolution changing a new entry's snapshot but not an existing one; settings lock round-trip and its effect on approve/reject; concurrency: two approvers racing → one 200, one 400.
- [ ] **Step 2–4.** **Step 5: Commit** `feat(time): approval workflow, approval queue, people overview, rate cards and the period lock`.

### Task 4: Stats and project summary

**Files:** `stats.go`, `queries/stats.sql`, `time.yaml`, `stats_test.go`.

- `GET /time/stats/summary`, `/timeseries?metric=hours|billableHours`, `/attention` — copy customers'/projects' parameter names and shapes exactly (the dashboard is generic); summary figures `hoursThisWeek` (caller), `awaitingMyApproval` (approver), deltas like customers'; attention: the caller's weeks older than the current one with unsubmitted entries, and (for approvers) submitted entries waiting > 7 days, links `/time` / `/time/approvals`.
- `GET /time/projects/{projectId}/summary` → `{ hours: { total, byStatus{…}, byLine: [ { billingLineId?, trackableCode?, hours } ], byPerson: [ { userId, displayName, hours } ] }, billing?: { amount, currency } }` — `billing` only for callers who may see the project's financials (sum of approved+submitted hours × bill rate, in the project currency; entries without a rate excluded and counted in `unpricedHours`).

- [ ] **Step 1: Failing tests**: shapes equal customers' (assert field names), scoping, the project summary numbers and shaping (member: no `billing`; manager: `billing`), unknown metric 400.
- [ ] **Step 2–4.** **Step 5: Commit** `feat(time): dashboard statistics and the project hours summary`.

## Slice 2 — Frontend

### Task 5: `@vantigo/time-ui` package, API layer, my week and day view

**Files:** package scaffold (copy `apps/projects/frontend` configuration: package.json `@vantigo/time-ui`, tsconfig, vite/vitest, eslint isolation incl. adding `@vantigo/time-ui` to every other package's list, `src/test/*`, `src/api/request.ts`); `tools/openapi/gen-client.ts` target + test; host `package.json` dep; `src/api/*`, `src/lib/*`, `src/pages/my-week.tsx`, `-row-picker.tsx`, `-entry-form-modal.tsx`, `src/pages/day.tsx`, `components/{entry-status-badge,hours-cell}.tsx`, `i18n.ts`, `index.ts`.

- **My week** (`MyWeekPage`, no props, reads `?week=YYYY-MM-DD` Monday via the router like the projects list page): week navigator (prev/next/today); grid rows "code › line › task" × Mon–Sun with `HoursCell` inputs (blur/enter saves: creates or updates the day's entry for that row — one entry per row per day; empty clears/deletes a draft); row status colours by the entries' statuses; day and week totals; "Add row" via `RowPicker` (project from `GET /api/v1/projects?mine=true` cross-module with local types → billing lines from `GET /api/v1/projects/{id}/billing-lines` → my open tasks from `GET /api/v1/projects/my-tasks` filtered to the project); "From my tasks" shortcut adding rows for assigned tasks; "Submit week" with confirm; banner for unsubmitted changes on a submitted week; rejected entries show the reason on hover; locked days read-only.
- **Day view** (`DayPage` at `?date=`): list of the day's entries with start/end/note, `EntryFormModal` (project/line/task pickers, date, hours OR start/end (derives hours), note, billable toggle when the project allows), delete draft.
- i18n en + nb; tests: grid renders rows/cells, cell edit creates/updates, submit week, banner, picker flow, day form derives hours from start/end, locked day read-only.

- [ ] Steps: scaffold + `gen:client`; failing tests; implement; `test|typecheck|lint`, `translations:check`, `i18n:test`, biome, `frontend:lint`, `frontend:typecheck`. Commit `feat(time-ui): package, api layer, my week grid and day view`.

### Task 6: Approvals, people, settings, project time panel

**Files:** `pages/approvals.tsx`, `pages/people.tsx`, `pages/settings.tsx`, `components/project-time-panel.tsx`, tests.

- Approvals: grouped by person/week, expandable entries, approve/reject (reason modal) per entry and per group, batch select.
- People (`view-all`): table user × last 4 weeks with hours and state chips.
- Settings (`manage`): rates table per user with effective dates (add/edit/delete), lock date picker.
- `ProjectTimePanel({ projectId })`: hours by line/person/status from `/time/projects/{id}/summary`; billing amount when present; link to my week.
- Tests for each; en + nb.

- [ ] Commit `feat(time-ui): approvals, people overview, settings and the project time panel`.

### Task 7: Host integration, docs

**Files:** `navigation.ts` (`moduleKeys` += `"time"`, `searchStrategy` `"time-week"` → `{ week: undefined }`), `apps.ts` (`moduleApp("time", "navigation.time", IconClock, "/time", [My week `/time` (`time:access`), Approvals `/time/approvals` (`time:approve` — project managers without it reach it from the dashboard card; document), People `/time/people` (`time:view-all`), Settings `/time/settings` (`time:manage`)])`, `routes/time.tsx` (`appLayoutOptions("time")`), `routes/time/{index,day,approvals,people,settings}.tsx` with validators, `routes/projects/$projectId.time.tsx` + tab in `projectDetailTabs` gated `{ module: "time", requiredPermissions: ["time:access"] }` rendering `ProjectTimePanel` (inline `ModuleNotEnabledPage` when off), dashboard card + `hours` metric + attention link, spotlight "Log time" (→ `/time`), i18n import, catalogs (navigation, dashboard, project tab, spotlight, admin permission labels + "Time" category) en + nb, `routeTree.gen.ts`; tests (registry, guard, tabs, routes, spotlight). Docs: `docs/time.md` (model, rates chain, state machine, lock, permissions, what Invoices will read), `docs/module-boundaries.md` (schema list, MODULES known set, `time requires projects`, provider notes), `CONTRIBUTING.md`, `README.md`, `deploy/compose/*` (schemas/GRANTs/env examples), `docs/customers-authentication.md` MODULES default, `ROADMAP.md` phase 2 done.

- [ ] Commit `feat(frontend): the Time app in the switcher, project time tab, dashboard and spotlight` and `docs(time): module guide, boundaries and roadmap`.

### Task 8: Final gates and PR

- [ ] `go generate` + `gen:client` no drift; vet; lint; full Go suite pinned to four CPUs; `frontend:lint|typecheck|test|build`; translations; biome; `openapi/COVERAGE.md` regenerated (`go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`); smoke test with a "time demands authentication" line added to `scripts/smoke-image.sh`.
- [ ] Trailer normalisation; push; PR against `main` (based on #100 — say so); watch checks; fix on red; never merge.
