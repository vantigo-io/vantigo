# Work types and overtime multipliers (Projects phase 3, delivery B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A project defines its **work types** once ("Overtid 50 %", "Helg") — a name, a bill multiplier and a cost multiplier, as percentages of the rate — a person picks one when logging time, and Time multiplies whatever rates the chain resolved, snapshotting the multipliers beside the base rates and freezing them on submit. Actuals report hours and value per work type; the Economy tab shows "Hours by work type". Projects stores the rule and multiplies nothing; Time owns every amount. Two migrations (`00031` projects, `00032` time), three new operations (`GET`/`POST /projects/{id}/work-types`, `PUT /projects/{id}/work-types/{workTypeId}`), two directory methods (`WorkType`, `WorkTypes`), one additive contract field (`ProjectActualsEntry.WorkTypes`), additive optional fields on four existing schemas (`TimeEntryRequest`, `TimeEntryUpdateRequest`, `TimeEntryResponse`/`TimeEntryBilling`/`TimeEntryCost`, `ProjectEconomyResponse`).

**Architecture:** `projects.work_types` (migration `00031`, unique on `(project_id, lower(name))`) is served by `internal/projects/work_types.go` — list for anyone who sees the project, create and change for `CanManage`, one plain transaction each (no project-row lock: nothing here depends on the currency, the fixed price or the billing type), the unique index's 23505 answered 409, and a `work-type-added` / `work-type-changed` timeline entry naming the fields. `contracts.ProjectDirectory` gains `WorkType(id)` and `WorkTypes(projectID)` over `contracts.WorkTypeEntry`, implemented in `projects/directory.go` and stubbed in every fake the tree has. Time (migration `00032`: `work_type_id`, `work_type_name`, `bill_multiplier_percent`, `cost_multiplier_percent` on `time.entries`) resolves `workTypeId` in `checkReferences` — before `withLockedTx`, like the project and the line — snapshots it at every save while draft or rejected, keeps `bill_rate`/`cost_rate` the base rates, and answers `workType` plus `multiplierPercent`/`effectiveRate` inside the shaped `billing` and `cost` blocks. `queries/actuals.sql` multiplies where it sums (`hours × rate × (COALESCE(multiplier, 100) × 0.01)`, exact numeric multiplication, rounded once by the existing folding), a second grouped query splits one project's work per type, and `time/actuals.go` folds it on the buckets' currency gate into `ProjectActualsEntry.WorkTypes`. Projects' economy shapes that into `workTypes` (hours for everyone, `billAmount` with financial rights and a currency, `costAmount` with `projects:view-costs` on top, absent without time tracking). The Projects frontend gains a Work types card on the Billing tab, its form modal, the two timeline sentences and the economy's "Hours by work type" table; the Time frontend gains the entry form's Work type select (reading Projects' own `GET /projects/{id}/work-types`, the way it already reads billing lines), a badge after the trackable code in my week, the day view and the approval queue, the "900 × 150 % = 1 350" rate line, and the approval queue's amount computed exactly with the multiplier. en + nb.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-25-project-work-types-design.md` (D1–D6 + "Out of scope" + "Testing"). Read it first; it is binding. Research with file:line pointers: `.superpowers/sdd/2026-09-25-project-costs-multipliers/context-for-design.md` and `.superpowers/sdd/2026-09-25-project-costs-multipliers/context-work-types.md` — trust them, but read the code they point at before writing code against it. The shapes this delivery copies: `apps/server/internal/projects/lines.go` (the three-operation CRUD, the order of the gates, the unique index as the uniqueness rule), `projects/timeline.go:111-229` (the billing-line entries, `diffLines`, `numericChanged`), `projects/directory.go` + `queries/directory.sql` (minimal-column directory rows), `time/entries.go:37-93` (`checkReferences`, where every directory read happens before the transaction), `time/rates.go:209-243` (`exactDecimal`, `roundHalfUpCents`), `time/actuals.go` (the currency-gated folding), `projects/economy.go:176-270` (shaping by absence), `apps/projects/frontend/src/pages/project-billing.tsx` + `-billing-line-form-modal.tsx` (the card and the modal), `apps/time/frontend/src/api/projects.ts` (Time's UI reading Projects' API).

## Global Constraints

- Branch `feat/project-costs-multipliers` (HEAD is the committed spec, `4662fe38`). Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'` for every `go test`. Port 55432 belongs to another project — never touch it.
- The untracked `go.mod`/`go.sum` in the repo root are not ours: never add, edit or delete them.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. Concurrent agents share ONE index: never commit a path you did not change.
- Commit messages: Conventional Commits scoped `projects` / `time` / `contracts` / `projects-ui` / `time-ui` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- After any `openapi/*.yaml` or `queries/*.sql` or migration change: `cd apps/server && mise exec -- go generate ./...` — **never package-scoped**; a second run must show no new diff — then `mise exec -- go test -count=1 ./internal/openapi/...`, then from the repo root `mise exec -- bun run gen:client`, then regenerate `openapi/COVERAGE.md` with `cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`. Commit every generated file (`apps/server/internal/openapi/specs/*.yaml`, `internal/<module>/gen/api.gen.go`, `internal/<module>/store/*.go`, each changed `api-schema.d.ts`, `openapi/COVERAGE.md`).
- **Frozen corpus:** never edit `openapi/testdata/exchanges/*.jsonl` (projects and time have none — `portedModules` in `internal/openapi/exchanges_test.go` — and are held to `contracttest.RequireCoverage` in their `main_test.go` instead, which requires every operation to answer 2xx in the module's own tests). Changes to EXISTING schemas are additive and optional only — never add to an existing `required:` list. New schemas may have required fields. New request fields are plain optional values validated in Go with this module's wording, never yaml `enum:`/`minimum:`/`maximum:`.
- New operations need an `operationId`, `x-vantigo-access: permission:projects:access`, module-test coverage, and the `KnownServeMuxConflicts` pins `TestServeMuxConflictsArePinned` asks for.
- After any migration: add it to the owning module's `sqlc.yaml` `schema:` list (checked by `TestSqlcSchemaListsOnlyTheModulesOwnMigrations`) and pin it in `apps/server/internal/db/schema_test.go`. After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes.
- **No directory call inside a locked transaction** — both modules enforce it in their harnesses (projects: `lockedContractCalls` via `SetContractCallHook`; time: `fakeProjects.locked`/`noteLocked`). Every `WorkType`/`WorkTypes` read happens before `withLockedTx`/`withProjectLock` opens.
- **Projects never computes money.** It stores the two percentages and nothing else; every amount is Time's.
- **Exact decimal** via `math/big.Rat` / `pgtype.Numeric` / SQL `numeric`, never float arithmetic on money. The entry's `effectiveRate` is display only, half-up to cents; summed amounts are multiplied in SQL and rounded once.
- Money is shaped **absent, not null** per caller, everywhere: `billing.multiplierPercent`/`effectiveRate` live inside `billing`, the cost ones inside `cost`, the economy's `billAmount`/`costAmount` are absent without the rights.
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style.
- **Frontend rules:** omitted → null (or filtered) at the api boundary, never trusted deeper in; at least one fixture per resource is a wire literal — the body the server sends; every new UI string in both catalogs (`en` + `nb`) of the package's `src/i18n.ts`, and `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` pass; a Mantine `Select` is a `combobox` in tests; **never assert "the last fetch"** — filter `fetchMock.actualCalls` by method/URL (`sent(fetchMock, "POST")` in the time package, `actualCalls.find(...)` in projects); tests run with `mise exec -- bun run --cwd <pkg> test`; run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.
- Every new test must be shown able to fail (remove the guard, see red, restore). Say so in the report.
- **One implementer commits at a time.** If two agents ever share the tree, the second writes and verifies but does not commit; the controller commits by pathspec.

**Parallelism.** Tasks 1 → 2 → 3 are sequential (Task 2 needs Task 1's contract type, Task 3 needs Task 2's). Task 6 (projects frontend) needs Tasks 1 and 3's generated `api-schema.d.ts` and nothing else, so it may run beside Tasks 4–5; Task 7 (time frontend) needs Task 2's generated `apps/time/frontend/src/api-schema.d.ts` and Task 1's endpoint, so it may run beside Tasks 3–6. The trees are disjoint (`apps/server` / `docs` / `apps/projects/frontend` / `apps/time/frontend`); the one-committer rule above still holds.

---

**How this plan reads the spec where it leaves a choice open.** Each is on the record for the user's verdict (Task 8 Step 4 repeats them):

1. **The duplicate-name 409 is a bare `ProblemDetails`, title "Work type exists".** D1 names it `work_type_exists`; `projects/errors.go` says in so many words that this module "may [not] grow a machine-readable error code of its own" and every refusal here is a bare RFC 7807 problem. So `work_type_exists` is the condition and the helper's name (`workTypeExists`), the wire says it by status and title, and the frontend puts it on the name field on any 409 from the two writes — the only 409 they answer.
2. **The timeline event types are `work-type-added` and `work-type-changed`**, in the module's own kebab vocabulary (`line-added`, `line-changed`), not D1's dotted `project.work_type_added`/`_updated` spelling, which no project event uses. Payload `{workTypeId, name, fields}`; a change of `active` is one of `work-type-changed`'s fields (D1 names two events, so there is no separate deactivated entry); a change that moved nothing writes nothing.
3. **No Time proxy endpoint.** Time's UI already reads Projects' HTTP API for projects, billing lines and tasks (`apps/time/frontend/src/api/projects.ts`: "Time never imports the projects frontend; it calls the projects HTTP API"); the frontend isolation rule is about imports, not HTTP. So the entry form reads `GET /api/v1/projects/{id}/work-types` — which D1 already opens to anyone who sees the project — and Time's contract gains no operation.
4. **The per-type split is named by Projects, from its own table** (controller ruling on the pre-flight review, overruling this plan's first draft). `contracts.WorkTypeActuals` carries `WorkTypeID`, `HoursHundredths`, `BillAmount`, `CostAmount` and no name: an entry's `updated_at` moves on submit, approve and reject, so a "latest snapshot" name would flip back to a pre-rename name whenever an old entry was approved, and would disagree with the Billing card right after a rename. Projects owns `projects.work_types`, reads it in-module (no contract call, not money) and names each row by id, skipping an id it does not know. The provider orders by id; Projects orders the rows by name.
5. **The multiplier enters SQL as `× (COALESCE(pct, 100) × 0.01)`** — multiplication, which PostgreSQL's `numeric` does exactly, where `/ 100` is a division rounded to a scale that shrinks as the value grows.
6. **The project summary's billed amount multiplies too** (`ProjectBillingTotals`, `queries/stats.sql`). D3 names `actuals.sql`; the summary is the other place Time sums bill amounts, and the Time tab and the Economy tab would otherwise disagree about the same hours.
7. **A non-billable entry snapshots both multipliers.** The bill multiplier multiplies nothing there (the amount is filtered on `billable`, and the chain gives no rate); `billing.multiplierPercent` is present whenever a type was picked and the block is visible, `effectiveRate` only when the block also has a rate.
8. **A full replace without `workTypeId` clears the type**, like every other optional field of the PUT — so the week grid's `timeEntryUpdateFrom` carries `workTypeId` along, or typing new hours into a cell would drop an entry's overtime.
9. **The economy's "Hours by work type" table is Task 6's** (it is the projects frontend's), not Task 7's.
10. **My week's rows are per trackable**, not per work type (the row key is unchanged), so a row shows, after its label, a badge for each distinct work type its week's entries were logged as; typing into an empty day of such a row logs ordinary hours (the grid writes a duration; picking a type is the entry form's), and an hours edit on an entry whose type was since retired is refused on `workTypeId`, which the grid reports in its notification.
11. **The approval queue's amount is computed exactly** with the multiplier (`lib/money.ts`, BigInt cents): its float `rate * hours` would show 750.00 for 333.33 × 1.5 h at 150 % where every other surface says 749.99.
12. **The rate line appears only when a multiplier applies** — nothing in Time's UI shows a bare rate today, and D5's line is the multiplied one.
13. **Small ones:** `POST` ignores `active` (a type starts active); deactivating is the edit form's Active switch, the billing line's precedent; the Work types card shows whether or not products is enabled (types do not depend on it); the create form defaults both multipliers to 100 ("as the rate says"); both lists order active first, then by `lower(name)`, then id; no CHECK constraints and no new index (house style; `ix_entries_project_id_entry_date` already serves the per-type read's `project_id`).

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00031_projects_work_types.sql`, `internal/projects/sqlc.yaml`, `internal/db/schema_test.go` | `projects.work_types` (Task 1) |
| `apps/server/internal/projects/queries/work_types.sql`, `queries/directory.sql` (+ generated `store/work_types.sql.go`, `store/directory.sql.go`, `store/models.go`) | the CRUD's and the directory's statements (Task 1) |
| `openapi/projects.yaml` (+ generated `internal/openapi/specs/projects.yaml`, `internal/projects/gen/api.gen.go`, `apps/projects/frontend/src/api-schema.d.ts`), `apps/server/internal/openapi/openapi_test.go`, `openapi/COVERAGE.md` | three operations, two schemas (Task 1); `ProjectEconomyWorkType` + `workTypes` (Task 3) |
| `apps/server/internal/projects/work_types.go`, `errors.go`, `timeline.go`, `directory.go`, `work_types_test.go`, `directory_test.go` | the handlers, the 409, the timeline entries, the directory methods (Task 1) |
| `apps/server/internal/contracts/projects.go` | `WorkTypeEntry`, `WorkType`, `WorkTypes` (Task 1) |
| `apps/server/internal/{time/harness_test.go,time/actuals_test.go,expenses/harness_test.go,expenses/contractscalls_internal_test.go,expenses/projectexpenses_test.go,module/compose_test.go}` | every `ProjectDirectory` fake gains the two methods (Task 1) |
| `apps/server/internal/db/migrations/00032_time_work_types.sql`, `internal/time/sqlc.yaml`, `internal/db/schema_test.go` | the entry's snapshot columns (Task 2) |
| `apps/server/internal/time/queries/entries.sql`, `queries/actuals.sql`, `queries/stats.sql` (+ generated `store/*.go`) | the snapshot written, the sums multiplied, the per-type read (Task 2) |
| `openapi/time.yaml` (+ generated `internal/openapi/specs/time.yaml`, `internal/time/gen/api.gen.go`, `apps/time/frontend/src/api-schema.d.ts`) | `workTypeId`, `workType`, the multiplier fields (Task 2) |
| `apps/server/internal/time/values.go`, `entries.go`, `rates.go`, `responses.go`, `actuals.go`, `harness_test.go`, `actuals_test.go`, `work_types_test.go` | resolution, snapshot, shaping, folding (Task 2) |
| `apps/server/internal/contracts/actuals.go` | `WorkTypeActuals`, `ProjectActualsEntry.WorkTypes` (Task 2) |
| `apps/server/internal/projects/economy.go`, `harness_test.go`, `economy_work_types_test.go`, `economy_expenses_test.go` | the economy's `workTypes` block, named from projects' own table; the golden test's second allowed field (Task 3) |
| `apps/server/internal/integration/work_types_test.go` | projects + time composed for real (Task 4) |
| `docs/projects.md`, `docs/time.md`, `docs/module-boundaries.md`, `ROADMAP.md` | D6 (Task 5) |
| `apps/projects/frontend/src/{api/projects.ts,api/work-types.ts,api/work-types.test.ts,api/economy.ts,pages/project-billing.tsx,pages/project-billing.test.tsx,pages/-work-type-form-modal.tsx,pages/-work-type-form-modal.test.tsx,pages/project-economy.tsx,pages/project-economy.test.tsx,pages/-project-timeline.tsx,pages/-project-timeline.test.tsx,i18n.ts}` | the card, the modal, the timeline, the economy table (Task 6) |
| `apps/time/frontend/src/{api/projects.ts,api/projects.test.ts,api/entries.ts,api/entries.test.ts,lib/money.ts,lib/money.test.ts,components/work-type-badge.tsx,components/rate-line.tsx,pages/-entry-form-modal.tsx,pages/day.tsx,pages/day.test.tsx,pages/approvals.tsx,pages/approvals.test.tsx,pages/my-week.tsx,pages/my-week.test.tsx,test/server.ts,test/fixtures.ts,i18n.ts}` | the select, the badges, the rate line, the exact amount (Task 7) |

---
### Task 1: A project's work types, and the directory that hands them over (D1, D2)

`projects.work_types`, its three operations with the 409 and the timeline entries, and `contracts.ProjectDirectory.WorkType`/`WorkTypes`. Every fake of the directory in the tree learns the two methods, so the tree builds; the time fake's fixtures come in Task 2.

**Files:**
- Create: `apps/server/internal/db/migrations/00031_projects_work_types.sql`, `apps/server/internal/projects/queries/work_types.sql`, `apps/server/internal/projects/work_types.go`, `apps/server/internal/projects/work_types_test.go`
- Modify: `apps/server/internal/projects/sqlc.yaml`, `apps/server/internal/db/schema_test.go`, `apps/server/internal/projects/queries/directory.sql`, `openapi/projects.yaml`, `apps/server/internal/openapi/openapi_test.go`, `apps/server/internal/contracts/projects.go`, `apps/server/internal/projects/directory.go`, `apps/server/internal/projects/errors.go`, `apps/server/internal/projects/timeline.go`, `apps/server/internal/projects/directory_test.go`, `apps/server/internal/time/harness_test.go`, `apps/server/internal/time/actuals_test.go`, `apps/server/internal/expenses/harness_test.go`, `apps/server/internal/expenses/contractscalls_internal_test.go`, `apps/server/internal/expenses/projectexpenses_test.go`, `apps/server/internal/module/compose_test.go`, `openapi/COVERAGE.md`
- Generated (commit them): `apps/server/internal/openapi/specs/projects.yaml`, `apps/server/internal/projects/gen/api.gen.go`, `apps/server/internal/projects/store/work_types.sql.go`, `apps/server/internal/projects/store/directory.sql.go`, `apps/server/internal/projects/store/models.go`, `apps/projects/frontend/src/api-schema.d.ts`
- Read first (do not change): `projects/lines.go` (the gate order), `projects/timeline.go:111-229`, `projects/values.go:262-296,533-614` (`validatePositiveAmount`, `numericChanged`, `numericFromFloat`), `projects/milestones_validation.go:299-306,517-526` (`decimalPlaces`, `floatFromNumeric`), `projects/lines_test.go:1-175` (the helpers this task's tests mirror), `projects/people_test.go:125-140` (`lastPayload`), `projects/projects_update_test.go:86-97,165` (`eventTypes`, `contains`)

**Interfaces:**
- Produces Go: `contracts.WorkTypeEntry{ID, ProjectID int32; Name string; BillMultiplierPercent, CostMultiplierPercent float64; Active bool}`; `contracts.ProjectDirectory.WorkType(ctx, id int32) (*WorkTypeEntry, error)` ((nil, nil) when missing) and `.WorkTypes(ctx, projectID int32) ([]WorkTypeEntry, error)` (every type, active first, by `lower(name)`, then id); `(*server).GetProjectsByIdWorkTypes`, `PostProjectsByIdWorkTypes`, `PutProjectsByIdWorkTypesByWorkTypeId`; `validateWorkType`, `validateMultiplier`, `workTypeResponse`, `workTypeExists(name) apicommon.ProblemDetails`, `recordWorkTypeAdded`, `diffWorkTypes`, `recordWorkTypeUpdated`, `eventWorkTypeAdded = "work-type-added"`, `eventWorkTypeChanged = "work-type-changed"`; `store.ProjectsWorkType`, `store.ListWorkTypes`, `InsertWorkType`, `LockWorkType`, `UpdateWorkType`, `DirectoryWorkType`, `DirectoryWorkTypes`.
- Wire: `GET /api/v1/projects/{id}/work-types` → 200 `WorkTypeResponse[]` (anyone who sees the project), 404; `POST` `WorkTypeRequest` → 201 `WorkTypeResponse`, 400 on `name` / `billMultiplierPercent` / `costMultiplierPercent`, 403, 404, 409 (title "Work type exists"); `PUT …/work-types/{workTypeId}` → 200, 400, 403, 404 (no such type on this project), 409. Timeline `work-type-added` `{workTypeId, name, fields: [name, billMultiplierPercent, costMultiplierPercent]}`, `work-type-changed` `{workTypeId, name, fields}` (`name`, `billMultiplierPercent`, `costMultiplierPercent`, `active` — the ones that moved).
- Consumes: nothing new.

- [ ] **Step 1: Pin the migration, see it fail, write it**

In `apps/server/internal/db/schema_test.go`, directly after `TestProjectsMilestones_AppliesAndIsIdempotent` (it ends before the comment `// TestExpensesBaseline_AppliesAndIsIdempotent proves`), add:

```go
// TestProjectsWorkTypes_AppliesAndIsIdempotent proves
// 00031_projects_work_types.sql applies, rolls back and re-applies cleanly,
// and pins what the work types design (D1) rests on: both percentages at
// numeric(6,2) — greater than zero and at most 1000 with two decimals is Go's
// rule, the column is what holds it — the in-module foreign key to the
// project, and the unique index on the project and the name's lower case,
// whose 23505 is the duplicate-name 409. A plain (project_id, name) index
// would let "Overtid" and "overtid" both in.
func TestProjectsWorkTypes_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 31) // 00031_projects_work_types.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var columns string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(string_agg(column_name || ':' || data_type
		       || CASE WHEN data_type = 'numeric' THEN '(' || numeric_precision || ',' || numeric_scale || ')'
		               WHEN data_type = 'character varying' THEN '(' || character_maximum_length || ')'
		               ELSE '' END
		       || ':' || is_nullable, ',' ORDER BY ordinal_position), 'MISSING')
		FROM information_schema.columns
		WHERE table_schema = 'projects' AND table_name = 'work_types'`).Scan(&columns); err != nil {
		t.Fatalf("read projects.work_types columns: %v", err)
	}
	want := "id:integer:NO,project_id:integer:NO,name:character varying(100):NO," +
		"bill_multiplier_percent:numeric(6,2):NO,cost_multiplier_percent:numeric(6,2):NO," +
		"active:boolean:NO,created_at:timestamp with time zone:NO,updated_at:timestamp with time zone:NO"
	if columns != want {
		t.Errorf("projects.work_types columns = %q, want %q", columns, want)
	}

	var projectKeys int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_class r ON r.oid = c.confrelid
		WHERE n.nspname = 'projects' AND t.relname = 'work_types' AND c.contype = 'f' AND r.relname = 'projects'`).Scan(&projectKeys); err != nil {
		t.Fatalf("count the work types' foreign keys: %v", err)
	}
	if projectKeys != 1 {
		t.Errorf("foreign keys from work_types to projects = %d, want 1", projectKeys)
	}

	def := indexDefinition(t, ctx, pool, "projects", "ux_work_types_project_id_name")
	if !strings.Contains(def, "CREATE UNIQUE INDEX") || !strings.Contains(def, "(project_id, lower((name)::text))") {
		t.Errorf("ux_work_types_project_id_name = %q, want UNIQUE on (project_id, lower(name))", def)
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestProjectsWorkTypes_AppliesAndIsIdempotent' ./internal/db/
```
Expected: FAIL — `down to version 30` / `read projects.work_types columns` (no migration 31 yet).

Create `apps/server/internal/db/migrations/00031_projects_work_types.sql`:

```sql
-- +goose Up
-- A project's work types (work types design D1): a rule defined once per
-- project — "Overtid 50 %", "Helg" — that a time entry may pick, and that
-- multiplies every rate the chain resolves for it, on every billing line of
-- the project and at every step. Projects stores the two percentages and
-- nothing else; Time multiplies them and snapshots them onto the entry.
--
-- numeric(6,2): a percentage greater than zero and at most 1000, with two
-- decimals, validated in Go (validateMultiplier, work_types.go) the way every
-- other decimal here is — no CHECK, house style. 100 is "as the rate says".
-- There is no DELETE, so no ON DELETE either: a type is deactivated, because
-- entries that picked it still name it.
--
-- The name is unique per project without regard to case, on lower(name), so
-- "Overtid" and "overtid" are one type; the handler turns this index's 23505
-- into the 409, as a line's code index is turned into its field error.
CREATE TABLE projects.work_types (
    id                      integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id              integer      NOT NULL REFERENCES projects.projects (id),
    name                    varchar(100) NOT NULL,
    bill_multiplier_percent numeric(6,2) NOT NULL,
    cost_multiplier_percent numeric(6,2) NOT NULL,
    active                  boolean      NOT NULL DEFAULT true,
    created_at              timestamptz  NOT NULL,
    updated_at              timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_work_types_project_id_name ON projects.work_types (project_id, lower(name));

-- +goose Down
DROP TABLE projects.work_types;
```

In `apps/server/internal/projects/sqlc.yaml`, after `      - ../db/migrations/00011_projects_milestones.sql` add the line `      - ../db/migrations/00031_projects_work_types.sql`.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestProjectsWorkTypes_AppliesAndIsIdempotent|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestNoModuleReferencesAnotherModulesSchema' ./internal/db/
```
Expected: PASS.

- [ ] **Step 2: The contract**

In `openapi/projects.yaml`, replace

```yaml
info:
    title: Vantigo Projects API
```

with (the two schemas go last under `components.schemas`, after `TimelineEntryResponse`):

```yaml
        WorkTypeRequest:
            description: "A project's work type as it should stand (work types design D1): a name and two multipliers, each a percentage of the rate the time entry's rate chain resolved — 100 is the rate as it stands, 150 the classic overtime uplift. Projects stores the percentages and multiplies nothing; Time multiplies an entry's rates by them. active is the one field a PUT may leave out; a POST ignores it, and a new type is active."
            properties:
                active:
                    description: PUT only. Absent leaves the type as it stands. There is no DELETE — entries that picked a type still name it — so a type is deactivated, and a deactivated type is offered for no new entry.
                    nullable: true
                    type: boolean
                billMultiplierPercent:
                    description: What an hour of this type bills at, as a percentage of the bill rate the chain resolved. Greater than zero, at most 1000, at most two decimals.
                    format: double
                    type: number
                costMultiplierPercent:
                    description: What an hour of this type costs the company, as a percentage of the person's cost rate. Greater than zero, at most 1000, at most two decimals.
                    format: double
                    type: number
                name:
                    description: Trimmed; 1 to 100 characters, and unique within the project without regard to case — a taken name answers 409.
                    type: string
            required:
                - name
                - billMultiplierPercent
                - costMultiplierPercent
            type: object
        WorkTypeResponse:
            description: One of a project's work types (work types design D1). The multipliers are a rule, not an amount, so everyone who can see the project sees them, the way budgetHours is visible while budgetAmount is shaped away.
            properties:
                active:
                    type: boolean
                billMultiplierPercent:
                    format: double
                    type: number
                costMultiplierPercent:
                    format: double
                    type: number
                createdAt:
                    format: date-time
                    type: string
                id:
                    format: int32
                    type: integer
                name:
                    type: string
                projectId:
                    format: int32
                    type: integer
                updatedAt:
                    format: date-time
                    type: string
            required:
                - id
                - projectId
                - name
                - billMultiplierPercent
                - costMultiplierPercent
                - active
                - createdAt
                - updatedAt
            type: object
info:
    title: Vantigo Projects API
```

Under `paths`, replace

```yaml
    /api/v1/projects/stats:
        get:
```

with

```yaml
    /api/v1/projects/{id}/work-types:
        get:
            description: 'Every work type of the project (work types design D1), deactivated ones included — an entry that picked one still names it — the active ones first, each half by name. Anyone who sees the project reads them: a multiplier is a rule, not an amount.'
            operationId: getProjectsByIdWorkTypes
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                items:
                                    $ref: '#/components/schemas/WorkTypeResponse'
                                type: array
                    description: OK
                "401":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Unauthorized
                "403":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Forbidden
                "404":
                    description: Not Found — the project does not exist, or the caller holds no role on it and no view-all/manage-all permission.
            summary: List a project's work types
            tags:
                - Projects
            x-vantigo-access: permission:projects:access
        post:
            description: 'Adds a work type to the project (work types design D1). Manager only. The write takes no project lock — nothing about a work type depends on the currency, the fixed price or the billing type — and records work-type-added on the project timeline, naming the fields and never their values.'
            operationId: postProjectsByIdWorkTypes
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/WorkTypeRequest'
                required: true
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/WorkTypeResponse'
                    description: Created
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request
                "401":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Unauthorized
                "403":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Forbidden
                "404":
                    description: Not Found — the project does not exist, or the caller holds no role on it and no view-all/manage-all permission.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Conflict — the project already has a work type of that name, compared without regard to case (title "Work type exists").
            summary: Add a work type to a project
            tags:
                - Projects
            x-vantigo-access: permission:projects:access
    /api/v1/projects/{id}/work-types/{workTypeId}:
        put:
            description: 'Changes one of the project''s work types, active included (work types design D1). Manager only. Records work-type-changed naming the fields that moved; a change that moved nothing records nothing. A multiplier changed here moves no entry already submitted: Time froze it with the rates.'
            operationId: putProjectsByIdWorkTypesByWorkTypeId
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
                - in: path
                  name: workTypeId
                  required: true
                  schema:
                    format: int32
                    type: integer
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/WorkTypeRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/WorkTypeResponse'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request
                "401":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Unauthorized
                "403":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Forbidden
                "404":
                    description: Not Found — the project does not exist, the caller holds no role on it and no view-all/manage-all permission, or the project has no such work type.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Conflict — another work type of the project already has that name, compared without regard to case (title "Work type exists").
            summary: Change a project's work type
            tags:
                - Projects
            x-vantigo-access: permission:projects:access
    /api/v1/projects/stats:
        get:
```

In `apps/server/internal/openapi/openapi_test.go`'s `KnownServeMuxConflicts`, add each line directly after the line named:

- after `"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/timeline",` add `"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/work-types",`
- after `"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/timeline",` add `"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/work-types",`
- after `"PUT /api/v1/projects/milestones/{milestoneId}/position ⟷ PUT /api/v1/projects/{id}/roles/{userId}",` add `"PUT /api/v1/projects/milestones/{milestoneId}/position ⟷ PUT /api/v1/projects/{id}/work-types/{workTypeId}",`
- after `"PUT /api/v1/projects/tasks/{taskId}/position ⟷ PUT /api/v1/projects/{id}/roles/{userId}",` add `"PUT /api/v1/projects/tasks/{taskId}/position ⟷ PUT /api/v1/projects/{id}/work-types/{workTypeId}",`

If `TestServeMuxConflictsArePinned` prints a different set, pin exactly what it prints and say so in the report.

- [ ] **Step 3: The contract type and every fake**

In `apps/server/internal/contracts/projects.go`, directly after the `BillingLineEntry` struct's closing brace, add:

```go
// WorkTypeEntry is one of a project's work types as another module may read
// it (work types design D1, D2): a named rule that multiplies whatever rates
// the chain resolved for an entry that picks it. The percentages are a rule,
// not an amount — 150 is "one and a half times the rate" — so they are here
// beside the name for every consumer, not behind a financial-viewer check the
// way Currency and DefaultBillRate are. Projects stores them and multiplies
// nothing; the consumer that owns the rates does.
type WorkTypeEntry struct {
	ID, ProjectID         int32
	Name                  string
	BillMultiplierPercent float64 // > 0, ≤ 1000, two decimals; 100 is the rate as it stands
	CostMultiplierPercent float64 // the same, applied to the cost rate
	Active                bool
}
```

and in the `ProjectDirectory` interface, directly after `CanLogTime`'s declaration (before the interface's closing brace), add:

```go
	// WorkType looks up a work type by ID, active or not: an entry that
	// picked a type before it was deactivated must still be able to name it.
	// It returns (nil, nil) if id does not exist. The caller compares
	// ProjectID with its own project; a type of another project is the
	// caller's refusal to make.
	WorkType(ctx context.Context, id int32) (*WorkTypeEntry, error)
	// WorkTypes lists every work type on projectID, active ones first, each
	// half by name without regard to case, then by ID. A caller offering a
	// choice filters the active ones itself. A project with none, or one that
	// does not exist, answers an empty result.
	WorkTypes(ctx context.Context, projectID int32) ([]WorkTypeEntry, error)
```

Every fake must satisfy the interface again:

`apps/server/internal/time/harness_test.go` — change the `fakeProjects` struct to

```go
type fakeProjects struct {
	mu        sync.Mutex
	projects  map[int32]contracts.ProjectEntry
	lines     map[int32]contracts.BillingLineEntry
	tasks     map[int32]contracts.TaskEntry
	workTypes map[int32]contracts.WorkTypeEntry
	roles     map[roleKey]string
	locked    lockedCalls
}
```

in `newFakeProjects`, directly before `		roles: map[roleKey]string{},` add `		workTypes: map[int32]contracts.WorkTypeEntry{},`; add `"cmp"` and `"strings"` to the import block; and directly before the comment `// customerKraftVerket is the customer every fixture project but the internal` add:

```go
// WorkType is projects' own answer: any type by id, active or not, (nil, nil)
// for one nobody has (work types design D2).
func (f *fakeProjects) WorkType(ctx context.Context, id int32) (*contracts.WorkTypeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "WorkType")
	wt, ok := f.workTypes[id]
	if !ok {
		return nil, nil
	}
	return &wt, nil
}

// WorkTypes is the project's types in the directory's order: active first,
// each half by name without regard to case, then by id.
func (f *fakeProjects) WorkTypes(ctx context.Context, projectID int32) ([]contracts.WorkTypeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "WorkTypes")
	var out []contracts.WorkTypeEntry
	for _, wt := range f.workTypes {
		if wt.ProjectID == projectID {
			out = append(out, wt)
		}
	}
	slices.SortFunc(out, func(a, b contracts.WorkTypeEntry) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}
```

`apps/server/internal/time/actuals_test.go` — replace

```go
func (f *forbiddenProjects) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	f.deny("CanLogTime")
	return false, nil
}
```

with

```go
func (f *forbiddenProjects) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	f.deny("CanLogTime")
	return false, nil
}

func (f *forbiddenProjects) WorkType(context.Context, int32) (*contracts.WorkTypeEntry, error) {
	f.deny("WorkType")
	return nil, nil
}

func (f *forbiddenProjects) WorkTypes(context.Context, int32) ([]contracts.WorkTypeEntry, error) {
	f.deny("WorkTypes")
	return nil, nil
}
```

`apps/server/internal/expenses/projectexpenses_test.go` — the same replacement, the same two methods (its `forbiddenProjects` has the identical `CanLogTime`).

`apps/server/internal/expenses/harness_test.go` — directly before the comment `// fakeObjectStore is storage.ObjectStore in memory: the receipts this module` add:

```go
// WorkType and WorkTypes satisfy the interface and are never asked: work
// types multiply time's rates (work types design D1), and an expense has
// none.
func (f *fakeProjects) WorkType(context.Context, int32) (*contracts.WorkTypeEntry, error) {
	return nil, nil
}

func (f *fakeProjects) WorkTypes(context.Context, int32) ([]contracts.WorkTypeEntry, error) {
	return nil, nil
}
```

`apps/server/internal/expenses/contractscalls_internal_test.go` — replace

```go
func (silentDirectory) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	return false, nil
}
```

with

```go
func (silentDirectory) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	return false, nil
}

func (silentDirectory) WorkType(context.Context, int32) (*contracts.WorkTypeEntry, error) {
	return nil, nil
}

func (silentDirectory) WorkTypes(context.Context, int32) ([]contracts.WorkTypeEntry, error) {
	return nil, nil
}
```

`apps/server/internal/module/compose_test.go` — replace

```go
func (*fakeProjectDirectory) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	return false, nil
}
```

with

```go
func (*fakeProjectDirectory) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	return false, nil
}

func (*fakeProjectDirectory) WorkType(context.Context, int32) (*contracts.WorkTypeEntry, error) {
	return nil, nil
}

func (*fakeProjectDirectory) WorkTypes(context.Context, int32) ([]contracts.WorkTypeEntry, error) {
	return nil, nil
}
```

(`internal/customers/overview_test.go`'s `fakeProjects` embeds the interface and needs nothing.) Then check no implementer was missed:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
grep -rln 'func (.*) CanLogTime(' --include=*.go . | sort
grep -rln 'func (.*) WorkTypes(' --include=*.go . | sort
```
Expected: the two lists name the same files once Step 6 has added `internal/projects/directory.go` to the second.

- [ ] **Step 4: The queries, and generate**

Create `apps/server/internal/projects/queries/work_types.sql`:

```sql
-- name: ListWorkTypes :many
-- ListWorkTypes is every work type of one project (work types design D1),
-- deactivated ones included — an entry that picked one still names it — the
-- active ones first, each half by name without regard to case. The
-- directory's DirectoryWorkTypes answers in the same order, so the Billing
-- tab and Time's picker never disagree about it.
SELECT * FROM projects.work_types
WHERE project_id = @project_id
ORDER BY active DESC, lower(name), id;

-- name: InsertWorkType :one
-- InsertWorkType adds one type to a project. created_at and updated_at are
-- the same instant, supplied by the caller from Deps.Clock(); active takes the
-- column default (true). A name the project already has, in any case, raises
-- 23505 on ux_work_types_project_id_name, which the handler answers 409.
INSERT INTO projects.work_types (
    project_id, name, bill_multiplier_percent, cost_multiplier_percent, created_at, updated_at
) VALUES (
    @project_id, @name, @bill_multiplier_percent, @cost_multiplier_percent, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: LockWorkType :one
-- LockWorkType is one type of one project, locked for the rest of the
-- change's transaction, so the timeline names the fields that moved against
-- the row nobody else can move meanwhile. Scoping by project_id is the 404
-- for a type that exists on somebody else's project.
SELECT * FROM projects.work_types
WHERE id = @id AND project_id = @project_id
FOR UPDATE;

-- name: UpdateWorkType :one
-- UpdateWorkType applies one change under that lock. `active` is nullable
-- because a PUT may leave it out, which leaves the type as it stands —
-- coalesce, not a second statement, the billing line's UpdateBillingLine
-- shape.
UPDATE projects.work_types SET
    name = @name,
    bill_multiplier_percent = @bill_multiplier_percent,
    cost_multiplier_percent = @cost_multiplier_percent,
    active = coalesce(sqlc.narg(active)::boolean, active),
    updated_at = @now::timestamptz
WHERE id = @id AND project_id = @project_id
RETURNING *;
```

Append to `apps/server/internal/projects/queries/directory.sql`:

```sql

-- name: DirectoryWorkType :one
-- DirectoryWorkType is contracts.ProjectDirectory.WorkType's row: one type by
-- id, active or not, whatever project it is on — the consumer compares the
-- project itself (work types design D2).
SELECT id, project_id, name, bill_multiplier_percent, cost_multiplier_percent, active
FROM projects.work_types
WHERE id = @id;

-- name: DirectoryWorkTypes :many
-- DirectoryWorkTypes is contracts.ProjectDirectory.WorkTypes' rows: every type
-- on projectID, in ListWorkTypes' order.
SELECT id, project_id, name, bill_multiplier_percent, cost_multiplier_percent, active
FROM projects.work_types
WHERE project_id = @project_id
ORDER BY active DESC, lower(name), id;
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Expected: the generated files listed under **Files** change, and the build of `internal/projects` now fails — `*server does not implement gen.StrictServerInterface (missing method GetProjectsByIdWorkTypes)` and `*directory does not implement contracts.ProjectDirectory (missing method WorkType)`. That is the red the next step's tests are written against. sqlc should emit `store.ProjectsWorkType{ID, ProjectID int32; Name string; BillMultiplierPercent, CostMultiplierPercent pgtype.Numeric; Active bool; CreatedAt, UpdatedAt time.Time}`, `InsertWorkTypeParams{ProjectID, Name, BillMultiplierPercent, CostMultiplierPercent, Now}`, `LockWorkTypeParams{ID, ProjectID}`, `UpdateWorkTypeParams{Name, BillMultiplierPercent, CostMultiplierPercent, Active *bool, Now, ID, ProjectID}`, `DirectoryWorkTypeRow` and `DirectoryWorkTypesRow`; if a name differs, use sqlc's and say so in the report.

- [ ] **Step 5: Write the failing tests**

Create `apps/server/internal/projects/work_types_test.go`:

```go
package projects_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The three work-type operations (work types design D1). A work type is a
// rule defined once per project — a name and two percentages — that Time
// multiplies an entry's rates by; Projects stores it and multiplies nothing.
// Two things run through every case: the multipliers are visible to everyone
// who sees the project (a rule, not an amount), and only a manager writes.

// workTypeJSON decodes WorkTypeResponse.
type workTypeJSON struct {
	Id                    int32     `json:"id"`
	ProjectId             int32     `json:"projectId"`
	Name                  string    `json:"name"`
	BillMultiplierPercent float64   `json:"billMultiplierPercent"`
	CostMultiplierPercent float64   `json:"costMultiplierPercent"`
	Active                bool      `json:"active"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

func workTypesPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/work-types", projectID)
}

func workTypePath(projectID, workTypeID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/work-types/%d", projectID, workTypeID)
}

// workTypeBody is a valid body — overtime at 150 % on both sides — which
// tests override one field of at a time. A nil override value removes that
// field, the convention createBody and lineBody use.
func workTypeBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name":                  "Overtid 50 %",
		"billMultiplierPercent": 150,
		"costMultiplierPercent": 150,
	}
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
			continue
		}
		body[field] = value
	}
	return body
}

func postWorkType(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, workTypesPath(projectID), workTypeBody(overrides))
}

// createWorkType is postWorkType for a test that expects the type to be added.
func createWorkType(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) workTypeJSON {
	t.Helper()
	r := postWorkType(t, c, projectID, overrides)
	if r.Status != http.StatusCreated {
		t.Fatalf("create work type: status %d body %s, want 201", r.Status, r.Body)
	}
	var wt workTypeJSON
	r.JSON(&wt)
	return wt
}

func putWorkType(t *testing.T, c *modtest.Client, projectID, workTypeID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, workTypePath(projectID, workTypeID), body)
}

// changeWorkType is putWorkType for a test that expects the change applied.
func changeWorkType(t *testing.T, c *modtest.Client, projectID, workTypeID int32, body map[string]any) workTypeJSON {
	t.Helper()
	r := putWorkType(t, c, projectID, workTypeID, body)
	if r.Status != http.StatusOK {
		t.Fatalf("change work type: status %d body %s, want 200", r.Status, r.Body)
	}
	var wt workTypeJSON
	r.JSON(&wt)
	return wt
}

// listWorkTypes reads the project's types and fails the test unless it may.
func listWorkTypes(t *testing.T, c *modtest.Client, projectID int32) []workTypeJSON {
	t.Helper()
	r := c.Do(http.MethodGet, workTypesPath(projectID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list work types: status %d body %s, want 200", r.Status, r.Body)
	}
	var types []workTypeJSON
	r.JSON(&types)
	return types
}

func workTypeNames(types []workTypeJSON) []string {
	names := make([]string, 0, len(types))
	for _, wt := range types {
		names = append(names, wt.Name)
	}
	return names
}

// D1's happy path: a manager adds a type, it answers with the name trimmed
// and the percentages as given, the list carries it, and the timeline names
// it and the fields it was added with — never their values.
func TestPostProjectsByIdWorkTypes_AddsATypeAndRecordsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1000"})

	created := createWorkType(t, c, project.Id, map[string]any{
		"name": "  Overtid 50 %  ", "billMultiplierPercent": 150, "costMultiplierPercent": 137.5,
	})

	if created.Name != "Overtid 50 %" || created.ProjectId != project.Id || created.BillMultiplierPercent != 150 ||
		created.CostMultiplierPercent != 137.5 || !created.Active {
		t.Errorf("created = %+v, want the trimmed name, 150 %% and 137.5 %%, active", created)
	}
	if got := listWorkTypes(t, c, project.Id); len(got) != 1 || got[0].Id != created.Id {
		t.Errorf("list = %+v, want exactly the type just added", got)
	}
	payload := lastPayload(t, h, project.Id, "work-type-added")
	if payload["name"] != "Overtid 50 %" || payload["workTypeId"] != float64(created.Id) {
		t.Errorf("work-type-added payload = %v, want the type's id and name", payload)
	}
	if fields := payloadFields(t, payload); !slices.Equal(fields, []string{"name", "billMultiplierPercent", "costMultiplierPercent"}) {
		t.Errorf("work-type-added fields = %v, want name and both multipliers", fields)
	}
	if text := lastPayloadText(t, h, project.Id, "work-type-added"); strings.Contains(text, "137.5") || strings.Contains(text, "150") {
		t.Errorf("payload %s carries a multiplier; the timeline names fields, never their values", text)
	}
}

// D1's rules, one case each, on the field the form renders them under.
func TestPostProjectsByIdWorkTypes_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1001"})

	for _, tc := range []struct {
		name      string
		overrides map[string]any
		field     string
		message   string
	}{
		{"blank name", map[string]any{"name": "   "}, "name", "A work type name cannot be empty"},
		{"name longer than 100", map[string]any{"name": strings.Repeat("x", 101)}, "name",
			"A work type name cannot be longer than 100 characters, the given value was 101 characters"},
		{"zero bill multiplier", map[string]any{"billMultiplierPercent": 0}, "billMultiplierPercent",
			"The bill multiplier must be greater than zero"},
		{"bill multiplier past 1000", map[string]any{"billMultiplierPercent": 1000.01}, "billMultiplierPercent",
			"The bill multiplier cannot be more than 1000 %"},
		{"bill multiplier with three decimals", map[string]any{"billMultiplierPercent": 150.125}, "billMultiplierPercent",
			"The bill multiplier cannot have more than two decimals"},
		{"negative cost multiplier", map[string]any{"costMultiplierPercent": -10}, "costMultiplierPercent",
			"The cost multiplier must be greater than zero"},
		{"cost multiplier past 1000", map[string]any{"costMultiplierPercent": 1001}, "costMultiplierPercent",
			"The cost multiplier cannot be more than 1000 %"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := postWorkType(t, c, project.Id, tc.overrides)
			var problem validationProblemJSON
			r.JSON(&problem)
			if r.Status != http.StatusBadRequest || problem.Title != "Invalid project" ||
				!slices.Equal(problem.Errors[tc.field], []string{tc.message}) {
				t.Errorf("status %d body %s, want 400 with %q on %s", r.Status, r.Body, tc.message, tc.field)
			}
		})
	}
	// 1000 exactly and two decimals are inside the rule.
	createWorkType(t, c, project.Id, map[string]any{"name": "Maks", "billMultiplierPercent": 1000, "costMultiplierPercent": 0.01})
	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"Maks"}) {
		t.Errorf("names = %v, want only the valid type stored", got)
	}
}

// A name is unique per project without regard to case (D1): the unique index
// on lower(name) refuses the second, on a create and on a rename alike, and
// another project may use the same name.
func TestWorkTypes_ANameTakenInAnyCase_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1002"})
	other := createProject(t, c, map[string]any{"code": "WT1003"})
	createWorkType(t, c, project.Id, nil)

	r := postWorkType(t, c, project.Id, map[string]any{"name": "OVERTID 50 %"})
	var problem problemJSON
	r.JSON(&problem)
	if r.Status != http.StatusConflict || problem.Title != "Work type exists" || problem.Status != http.StatusConflict {
		t.Errorf("duplicate in another case: status %d body %s, want 409 \"Work type exists\"", r.Status, r.Body)
	}
	createWorkType(t, c, other.Id, nil)

	weekend := createWorkType(t, c, project.Id, map[string]any{"name": "Helg", "billMultiplierPercent": 200})
	r = putWorkType(t, c, project.Id, weekend.Id, workTypeBody(map[string]any{"name": "overtid 50 %"}))
	if r.Status != http.StatusConflict {
		t.Errorf("rename onto a taken name: status %d body %s, want 409", r.Status, r.Body)
	}
	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"Helg", "Overtid 50 %"}) {
		t.Errorf("names = %v, want both types as they were", got)
	}
}

// A change names what it moved; deactivating is a change of `active`; a body
// without `active` leaves it as it stands; a change that moved nothing writes
// no entry; and a deactivated type stays listed, after the active ones.
func TestPutProjectsByIdWorkTypes_ChangesDeactivatesAndRecordsWhatMoved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1004"})
	wt := createWorkType(t, c, project.Id, nil)

	changed := changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"costMultiplierPercent": 175}))
	if changed.CostMultiplierPercent != 175 || changed.BillMultiplierPercent != 150 || !changed.Active {
		t.Errorf("changed = %+v, want cost 175 %%, bill 150 %%, still active", changed)
	}
	if fields := payloadFields(t, lastPayload(t, h, project.Id, "work-type-changed")); !slices.Equal(fields, []string{"costMultiplierPercent"}) {
		t.Errorf("fields = %v, want [costMultiplierPercent]", fields)
	}

	deactivated := changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"costMultiplierPercent": 175, "active": false}))
	if deactivated.Active {
		t.Error("Active = true, want the type deactivated")
	}
	if fields := payloadFields(t, lastPayload(t, h, project.Id, "work-type-changed")); !slices.Equal(fields, []string{"active"}) {
		t.Errorf("fields = %v, want [active]", fields)
	}

	before := eventTypes(t, h, project.Id)
	same := changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"costMultiplierPercent": 175}))
	if same.Active {
		t.Error("Active = true after a body without active, want it left as it stood")
	}
	if after := eventTypes(t, h, project.Id); len(after) != len(before) {
		t.Errorf("timeline = %v, want no entry for a change that moved nothing (was %v)", after, before)
	}

	createWorkType(t, c, project.Id, map[string]any{"name": "Helg"})
	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"Helg", "Overtid 50 %"}) {
		t.Errorf("names = %v, want the active type first and the deactivated one kept", got)
	}
}

// The list's order is D2's: active first, each half by name without regard
// to case — the order Time's picker and the Billing tab both show.
func TestGetProjectsByIdWorkTypes_ListsTheActiveOnesFirstByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1005"})
	createWorkType(t, c, project.Id, map[string]any{"name": "Zeta"})
	alfa := createWorkType(t, c, project.Id, map[string]any{"name": "Alfa"})
	createWorkType(t, c, project.Id, map[string]any{"name": "beta"})
	changeWorkType(t, c, project.Id, alfa.Id, workTypeBody(map[string]any{"name": "Alfa", "active": false}))

	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"beta", "Zeta", "Alfa"}) {
		t.Errorf("names = %v, want [beta Zeta Alfa]", got)
	}
}

// Multipliers are visible to everyone who sees the project (D1): a member
// with no financial rights reads them. Writing is the manager's: a member
// and a viewer are the access layer's 403, an outsider the unknown id's 404.
func TestWorkTypes_EveryoneWhoSeesTheProjectReads_OnlyAManagerWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "WT1006"})
	wt := createWorkType(t, manager, project.Id, map[string]any{"costMultiplierPercent": 140})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")
	outsider, _ := signIn(t, h)

	got := listWorkTypes(t, member, project.Id)
	if len(got) != 1 || got[0].BillMultiplierPercent != 150 || got[0].CostMultiplierPercent != 140 {
		t.Errorf("member's list = %+v, want the type with both multipliers", got)
	}
	listWorkTypes(t, viewer, project.Id)

	for name, c := range map[string]*modtest.Client{"member": member, "viewer": viewer} {
		if r := postWorkType(t, c, project.Id, map[string]any{"name": "Helg"}); r.Status != http.StatusForbidden {
			t.Errorf("%s POST: status %d, want 403", name, r.Status)
		}
		if r := putWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"active": false})); r.Status != http.StatusForbidden {
			t.Errorf("%s PUT: status %d, want 403", name, r.Status)
		}
	}
	for label, r := range map[string]*modtest.Response{
		"GET":  outsider.Do(http.MethodGet, workTypesPath(project.Id), nil),
		"POST": postWorkType(t, outsider, project.Id, nil),
		"PUT":  putWorkType(t, outsider, project.Id, wt.Id, workTypeBody(nil)),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider %s: status %d, want 404", label, r.Status)
		}
	}
	if got := workTypeNames(listWorkTypes(t, manager, project.Id)); !slices.Equal(got, []string{"Overtid 50 %"}) {
		t.Errorf("names = %v, want nothing written by a refused request", got)
	}
}

// 404 for an unknown project, an unknown type, and a type that exists on
// another project — the scoped lock's answer.
func TestWorkTypes_UnknownProjectOrType_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1007"})
	other := createProject(t, c, map[string]any{"code": "WT1008"})
	foreign := createWorkType(t, c, other.Id, nil)

	for label, r := range map[string]*modtest.Response{
		"GET an unknown project":  c.Do(http.MethodGet, workTypesPath(999999), nil),
		"POST an unknown project": postWorkType(t, c, 999999, nil),
		"PUT an unknown type":     putWorkType(t, c, project.Id, 999999, workTypeBody(nil)),
		"PUT another's type":      putWorkType(t, c, project.Id, foreign.Id, workTypeBody(nil)),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("%s: status %d body %s, want 404", label, r.Status, r.Body)
		}
	}
}
```

Append to `apps/server/internal/projects/directory_test.go`:

```go
// TestDirectory_WorkType is D2's single read: a type resolves with its
// percentages, a deactivated one still resolves (an entry that picked it must
// still name it), and one nobody has is (nil, nil).
func TestDirectory_WorkType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1100"})
	wt := createWorkType(t, c, project.Id, map[string]any{"name": "Helg", "billMultiplierPercent": 200, "costMultiplierPercent": 162.5})
	dir := newDirectory(t, h)
	ctx := context.Background()

	got, err := dir.WorkType(ctx, wt.Id)
	if err != nil {
		t.Fatalf("WorkType: %v", err)
	}
	want := contracts.WorkTypeEntry{
		ID: wt.Id, ProjectID: project.Id, Name: "Helg", BillMultiplierPercent: 200, CostMultiplierPercent: 162.5, Active: true,
	}
	if got == nil || *got != want {
		t.Errorf("WorkType = %+v, want %+v", got, want)
	}

	changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{
		"name": "Helg", "billMultiplierPercent": 200, "costMultiplierPercent": 162.5, "active": false,
	}))
	if got, err := dir.WorkType(ctx, wt.Id); err != nil || got == nil || got.Active {
		t.Errorf("WorkType(deactivated) = %+v, %v, want it resolved and inactive", got, err)
	}
	if missing, err := dir.WorkType(ctx, 999999); err != nil || missing != nil {
		t.Errorf("WorkType(unknown) = %+v, %v, want (nil, nil)", missing, err)
	}
}

// TestDirectory_WorkTypes is D2's list: every type of the one project, active
// first, each half by name without regard to case; nothing of another
// project's; and an empty answer, not an error, for a project with none.
func TestDirectory_WorkTypes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1101"})
	other := createProject(t, c, map[string]any{"code": "DIR1102"})
	createWorkType(t, c, project.Id, map[string]any{"name": "Zeta"})
	alfa := createWorkType(t, c, project.Id, map[string]any{"name": "Alfa"})
	createWorkType(t, c, project.Id, map[string]any{"name": "beta"})
	changeWorkType(t, c, project.Id, alfa.Id, workTypeBody(map[string]any{"name": "Alfa", "active": false}))
	createWorkType(t, c, other.Id, map[string]any{"name": "Annet"})
	dir := newDirectory(t, h)
	ctx := context.Background()

	got, err := dir.WorkTypes(ctx, project.Id)
	if err != nil {
		t.Fatalf("WorkTypes: %v", err)
	}
	var names []string
	for _, wt := range got {
		names = append(names, wt.Name)
		if wt.ProjectID != project.Id {
			t.Errorf("WorkTypes answered %+v, a type of another project", wt)
		}
	}
	if want := []string{"beta", "Zeta", "Alfa"}; !slices.Equal(names, want) {
		t.Errorf("WorkTypes names = %v, want %v", names, want)
	}
	empty := createProject(t, c, map[string]any{"code": "DIR1103"})
	if none, err := dir.WorkTypes(ctx, empty.Id); err != nil || len(none) != 0 {
		t.Errorf("WorkTypes(none) = %v, %v, want an empty answer", none, err)
	}
}
```

and add `"slices"` to `directory_test.go`'s import block if it is not there.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'WorkType' ./internal/projects/
```
Expected: FAIL — the package does not compile (`missing method GetProjectsByIdWorkTypes`, `missing method WorkType`).

- [ ] **Step 6: Implement**

In `apps/server/internal/projects/errors.go`, append:

```go
// workTypeExistsTitle is the title of the 409 a work type's create or rename
// answers when the project already has a type of that name (work types design
// D1's work_type_exists). It is a conflict rather than a field error because
// the body is valid — it is the project that already holds the name — and it
// carries no code of its own, like every refusal here: the status and the
// title say it.
const workTypeExistsTitle = "Work type exists"

// workTypeExists is that 409's body, naming the name as the caller typed it.
func workTypeExists(name string) apicommon.ProblemDetails {
	return apicommon.ProblemStatus(workTypeExistsTitle,
		fmt.Sprintf("This project already has a work type named '%s'; names are compared without regard to case.", name),
		http.StatusConflict)
}
```

In `apps/server/internal/projects/timeline.go`, add to the event-type `const` block, after `eventMilestoneReopened      = "milestone-reopened"`, a blank line and:

```go
	eventWorkTypeAdded   = "work-type-added"
	eventWorkTypeChanged = "work-type-changed"
```

and directly before the comment `// recordMilestoneEvent writes one of the nine milestone entries (design` add:

```go
// The two work-type entries (work types design D1). Each names the type by
// its id and its name as it now stands, and the fields a change moved — never
// a multiplier's value, the billing lines' rule, so a timeline stays one
// thing for every reader. A change of `active` is one of the fields: D1 has
// no entry of its own for switching a type off.
func recordWorkTypeAdded(ctx context.Context, q *store.Queries, now time.Time, projectID int32, wt store.ProjectsWorkType, by actor) error {
	payload := map[string]any{
		"workTypeId": wt.ID,
		"name":       wt.Name,
		"fields":     []string{"name", "billMultiplierPercent", "costMultiplierPercent"},
	}
	return recordEvent(ctx, q, now, projectID, eventWorkTypeAdded, payload, by)
}

// diffWorkTypes names what one change to a type moved, in the contract's
// camelCase, comparing the percentages the way the response renders them
// (numericChanged), so 150 and 150.00 are one number.
func diffWorkTypes(before, after store.ProjectsWorkType) ([]string, error) {
	var fields []string
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	changed, err := numericChanged(before.BillMultiplierPercent, after.BillMultiplierPercent)
	if err != nil {
		return nil, err
	}
	if changed {
		fields = append(fields, "billMultiplierPercent")
	}
	changed, err = numericChanged(before.CostMultiplierPercent, after.CostMultiplierPercent)
	if err != nil {
		return nil, err
	}
	if changed {
		fields = append(fields, "costMultiplierPercent")
	}
	if before.Active != after.Active {
		fields = append(fields, "active")
	}
	return fields, nil
}

// recordWorkTypeUpdated writes work-type-changed for a change that moved
// something; the caller skips it when diffWorkTypes answered nothing.
func recordWorkTypeUpdated(ctx context.Context, q *store.Queries, now time.Time, projectID int32, wt store.ProjectsWorkType, fields []string, by actor) error {
	payload := map[string]any{"workTypeId": wt.ID, "name": wt.Name, "fields": fields}
	return recordEvent(ctx, q, now, projectID, eventWorkTypeChanged, payload, by)
}
```

Create `apps/server/internal/projects/work_types.go`:

```go
package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a project's work types (work types design D1): a rule defined
// once per project — "Overtid 50 %", "Helg" — that a time entry may pick, and
// that multiplies every rate the chain resolves for it, on every line. A type
// is a name and two percentages and that is all: Time multiplies, snapshots
// and freezes; nothing here multiplies anything.
//
// The three operations are ordered the billing lines' way — the project, the
// caller's access to it (404 before 403), then the body — and differ from
// them in one place on purpose: no project-row lock. The lock guards writes
// whose validity depends on the project's currency, fixed price or billing
// type (docs/projects.md's "Locking"), and a work type depends on none of
// them. Each write is one plain transaction; the name's uniqueness is the
// unique index's, whose 23505 is the 409.
//
// There is no DELETE. Entries that picked a type snapshot its name, but a
// type that was ever picked stays readable in the list, so it is deactivated
// through PUT `active: false` and reactivated the same way.

// errWorkTypeNotFound carries the change's 404 out of its transaction: whether
// the type is on this project is decided under the row lock.
var errWorkTypeNotFound = errors.New("projects: the project has no such work type")

// maxWorkTypeName is the name column's width.
const maxWorkTypeName = 100

// maxMultiplierPercent is D1's ceiling: ten times the rate. numeric(6,2)
// could hold more; the rule is the design's, not the column's.
const maxMultiplierPercent = 1000.0

// parsedWorkType is a body that passed D1's rules, in the shape the queries
// take.
type parsedWorkType struct {
	Name                  string
	BillMultiplierPercent pgtype.Numeric
	CostMultiplierPercent pgtype.Numeric
}

// validateWorkType runs every rule over a body and answers the parsed type and
// the field errors (nil when there are none). Every rule runs regardless of
// the others, so one round trip reports every problem. The error is an
// infrastructure one only: a multiplier that passed the rules and still cannot
// be stored.
func validateWorkType(body gen.WorkTypeRequest) (parsedWorkType, map[string][]string, error) {
	var errs map[string][]string
	name := strings.TrimSpace(body.Name)
	switch n := utf8.RuneCountInString(name); {
	case name == "":
		errs = withFieldError(errs, "name", "A work type name cannot be empty")
	case n > maxWorkTypeName:
		errs = withFieldError(errs, "name", fmt.Sprintf(
			"A work type name cannot be longer than %d characters, the given value was %d characters", maxWorkTypeName, n))
	}
	if msg := validateMultiplier("The bill multiplier", body.BillMultiplierPercent); msg != "" {
		errs = withFieldError(errs, "billMultiplierPercent", msg)
	}
	if msg := validateMultiplier("The cost multiplier", body.CostMultiplierPercent); msg != "" {
		errs = withFieldError(errs, "costMultiplierPercent", msg)
	}
	if len(errs) > 0 {
		return parsedWorkType{}, errs, nil
	}
	bill, err := numericFromFloat(body.BillMultiplierPercent)
	if err != nil {
		return parsedWorkType{}, nil, err
	}
	cost, err := numericFromFloat(body.CostMultiplierPercent)
	if err != nil {
		return parsedWorkType{}, nil, err
	}
	return parsedWorkType{Name: name, BillMultiplierPercent: bill, CostMultiplierPercent: cost}, nil, nil
}

// validateMultiplier is D1's percentage rule: more than nothing, at most
// maxMultiplierPercent, and no more precision than the numeric(6,2) column
// keeps — a third decimal would be silently rounded away, and a rate
// multiplied by something the manager did not type is worse than a refusal
// (the reasoning validatePositiveAmount gives for amounts). An absent field
// decodes as 0 and is refused here as "must be greater than zero".
func validateMultiplier(label string, v float64) string {
	switch {
	case v <= 0:
		return label + " must be greater than zero"
	case v > maxMultiplierPercent:
		return fmt.Sprintf("%s cannot be more than %g %%", label, maxMultiplierPercent)
	case decimalPlaces(v) > 2:
		return label + " cannot have more than two decimals"
	default:
		return ""
	}
}

// workTypeResponse renders one row. The percentages are NOT NULL columns, so
// floatFromNumeric's zero-for-NULL never fires; its error is an unreadable
// decimal, an infrastructure failure.
func workTypeResponse(row store.ProjectsWorkType) (gen.WorkTypeResponse, error) {
	bill, err := floatFromNumeric(row.BillMultiplierPercent)
	if err != nil {
		return gen.WorkTypeResponse{}, err
	}
	cost, err := floatFromNumeric(row.CostMultiplierPercent)
	if err != nil {
		return gen.WorkTypeResponse{}, err
	}
	return gen.WorkTypeResponse{
		Id:                    row.ID,
		ProjectId:             row.ProjectID,
		Name:                  row.Name,
		BillMultiplierPercent: bill,
		CostMultiplierPercent: cost,
		Active:                row.Active,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
	}, nil
}

// GetProjectsByIdWorkTypes List a project's work types
// (GET /api/v1/projects/{id}/work-types)
//
// Anyone who sees the project sees its types and their multipliers: a
// percentage is a rule, not an amount (D1), the reading that keeps budgetHours
// visible while budgetAmount is shaped away. A member logging overtime is
// entitled to know it bills at 150 %.
func (s *server) GetProjectsByIdWorkTypes(ctx context.Context, req gen.GetProjectsByIdWorkTypesRequestObject) (gen.GetProjectsByIdWorkTypesResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdWorkTypes404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdWorkTypes404Response{}, nil
	}

	rows, err := q.ListWorkTypes(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list work types: %w", err)
	}
	out := make([]gen.WorkTypeResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := workTypeResponse(row)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return gen.GetProjectsByIdWorkTypes200JSONResponse(out), nil
}

// PostProjectsByIdWorkTypes Add a work type to a project
// (POST /api/v1/projects/{id}/work-types)
//
// Manager only. A new type is active whatever the body says: `active` is a
// PUT field. Two managers adding the same name both reach the insert; one
// wins and the other's 23505 becomes the 409.
func (s *server) PostProjectsByIdWorkTypes(ctx context.Context, req gen.PostProjectsByIdWorkTypesRequestObject) (gen.PostProjectsByIdWorkTypesResponseObject, error) {
	body := gen.WorkTypeRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdWorkTypes404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdWorkTypes404Response{}, nil
	}
	if !a.CanManage {
		return gen.PostProjectsByIdWorkTypes403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := validateWorkType(body)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PostProjectsByIdWorkTypes400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	// The caller is named before the transaction opens: the user directory is
	// another module, and nothing inside a transaction asks one.
	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.ProjectsWorkType
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		created, err = txq.InsertWorkType(ctx, store.InsertWorkTypeParams{
			ProjectID:             project.ID,
			Name:                  parsed.Name,
			BillMultiplierPercent: parsed.BillMultiplierPercent,
			CostMultiplierPercent: parsed.CostMultiplierPercent,
			Now:                   now,
		})
		if err != nil {
			return err
		}
		return recordWorkTypeAdded(ctx, txq, now, project.ID, created, by)
	})
	switch {
	case db.IsUniqueViolation(err, "ux_work_types_project_id_name"):
		return gen.PostProjectsByIdWorkTypes409ApplicationProblemPlusJSONResponse(workTypeExists(parsed.Name)), nil
	case err != nil:
		return nil, fmt.Errorf("projects: create work type: %w", err)
	}

	resp, err := workTypeResponse(created)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdWorkTypes201JSONResponse(resp), nil
}

// PutProjectsByIdWorkTypesByWorkTypeId Change a project's work type
// (PUT /api/v1/projects/{id}/work-types/{workTypeId})
//
// Manager only; one intention, "this type is now X", `active` included. The
// type is read under its own row lock and changed in the same transaction, so
// the timeline names the fields that moved against the row nobody else could
// move meanwhile. Changing a multiplier moves no entry already submitted:
// Time froze the percentages with the rates (work types design D3).
func (s *server) PutProjectsByIdWorkTypesByWorkTypeId(ctx context.Context, req gen.PutProjectsByIdWorkTypesByWorkTypeIdRequestObject) (gen.PutProjectsByIdWorkTypesByWorkTypeIdResponseObject, error) {
	body := gen.WorkTypeRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId404Response{}, nil
	}
	if !a.CanManage {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := validateWorkType(body)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var changed store.ProjectsWorkType
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		before, err := txq.LockWorkType(ctx, store.LockWorkTypeParams{ID: req.WorkTypeId, ProjectID: project.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errWorkTypeNotFound
		}
		if err != nil {
			return fmt.Errorf("projects: lock work type: %w", err)
		}
		changed, err = txq.UpdateWorkType(ctx, store.UpdateWorkTypeParams{
			ID:                    req.WorkTypeId,
			ProjectID:             project.ID,
			Name:                  parsed.Name,
			BillMultiplierPercent: parsed.BillMultiplierPercent,
			CostMultiplierPercent: parsed.CostMultiplierPercent,
			Active:                body.Active,
			Now:                   now,
		})
		if err != nil {
			return err
		}
		fields, err := diffWorkTypes(before, changed)
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			return nil
		}
		return recordWorkTypeUpdated(ctx, txq, now, project.ID, changed, fields, by)
	})
	switch {
	case errors.Is(err, errWorkTypeNotFound):
		return gen.PutProjectsByIdWorkTypesByWorkTypeId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_work_types_project_id_name"):
		return gen.PutProjectsByIdWorkTypesByWorkTypeId409ApplicationProblemPlusJSONResponse(workTypeExists(parsed.Name)), nil
	case err != nil:
		return nil, fmt.Errorf("projects: change work type: %w", err)
	}

	resp, err := workTypeResponse(changed)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsByIdWorkTypesByWorkTypeId200JSONResponse(resp), nil
}
```

In `apps/server/internal/projects/directory.go`, directly before the comment `// directoryTaskRow is the shape every directory query resolving a task`, add:

```go
// toWorkTypeEntry converts a directory work-type row into the contract's
// WorkTypeEntry. The two queries emit two row types with the same columns, so
// both call sites pass the fields and this is the one mapping.
func toWorkTypeEntry(id, projectID int32, name string, bill, cost pgtype.Numeric, active bool) (contracts.WorkTypeEntry, error) {
	billPercent, err := floatFromNumeric(bill)
	if err != nil {
		return contracts.WorkTypeEntry{}, fmt.Errorf("projects: directory work type: %w", err)
	}
	costPercent, err := floatFromNumeric(cost)
	if err != nil {
		return contracts.WorkTypeEntry{}, fmt.Errorf("projects: directory work type: %w", err)
	}
	return contracts.WorkTypeEntry{
		ID:                    id,
		ProjectID:             projectID,
		Name:                  name,
		BillMultiplierPercent: billPercent,
		CostMultiplierPercent: costPercent,
		Active:                active,
	}, nil
}

// WorkType looks up one work type by id, active or not (work types design D2):
// an entry that picked it before it was switched off must still name it.
func (d *directory) WorkType(ctx context.Context, id int32) (*contracts.WorkTypeEntry, error) {
	row, err := d.q.DirectoryWorkType(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: directory work type: %w", err)
	}
	entry, err := toWorkTypeEntry(row.ID, row.ProjectID, row.Name, row.BillMultiplierPercent, row.CostMultiplierPercent, row.Active)
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// WorkTypes lists every work type on projectID, active first, each half by
// name without regard to case — the Billing tab's order.
func (d *directory) WorkTypes(ctx context.Context, projectID int32) ([]contracts.WorkTypeEntry, error) {
	rows, err := d.q.DirectoryWorkTypes(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("projects: directory work types: %w", err)
	}
	entries := make([]contracts.WorkTypeEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := toWorkTypeEntry(row.ID, row.ProjectID, row.Name, row.BillMultiplierPercent, row.CostMultiplierPercent, row.Active)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
```

- [ ] **Step 7: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
# The pre-commit hook refuses the commit when `gofmt -l apps/server` prints anything, and `-l` alone exits 0:
# write the formatting (the new workTypes field realigns time's fakeProjects literal), then check it is empty.
mise exec -- gofmt -w internal/projects internal/time internal/expenses internal/module internal/contracts internal/db
test -z "$(mise exec -- gofmt -l internal)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/projects/... ./internal/openapi/... ./internal/db/... ./internal/module/... ./internal/time/... ./internal/expenses/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git diff --stat -- openapi/COVERAGE.md
```
Expected: PASS, including the projects coverage gate (`contracttest.RequireCoverage`) with the three new operations answered 2xx; `openapi/COVERAGE.md`'s projects section lists the three new operations (projects has no corpus, so "45 uncovered" is expected).

Prove the tests can fail, restoring after each: change `lower(name)` to `name` in the migration's index (and regenerate, re-run migrations through the test) — `TestWorkTypes_ANameTakenInAnyCase_Returns409` and `TestProjectsWorkTypes_AppliesAndIsIdempotent` go red; delete the `if !a.CanManage` block in `PostProjectsByIdWorkTypes` — the member's POST answers 201 and the access test goes red; drop the `decimalPlaces` case from `validateMultiplier` — the three-decimals case goes red; return `nil` from `diffWorkTypes` — the change test goes red; change `ORDER BY active DESC` to `ORDER BY active` in `DirectoryWorkTypes` — `TestDirectory_WorkTypes` goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-wt-1.txt <<'EOF'
feat(projects): a project defines its work types once, and the directory hands them to Time

A work type is a project-level rule — a name, a bill multiplier and a
cost multiplier, as percentages of the rate — that applies to every
billing line of the project. GET /projects/{id}/work-types answers
anyone who sees the project (a multiplier is a rule, not an amount);
POST and PUT /projects/{id}/work-types/{workTypeId} are the manager's,
validate the name (1–100) and both multipliers (> 0, ≤ 1000, two
decimals), refuse a name the project already has in any case with a
409, and record work-type-added / work-type-changed naming the fields.
There is no DELETE: a type is deactivated. No project lock — nothing
here depends on the currency, the fixed price or the billing type.
contracts.ProjectDirectory gains WorkType and WorkTypes.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/db/migrations/00031_projects_work_types.sql apps/server/internal/projects/sqlc.yaml \
 apps/server/internal/db/schema_test.go apps/server/internal/projects/queries/work_types.sql \
 apps/server/internal/projects/queries/directory.sql apps/server/internal/projects/store/work_types.sql.go \
 apps/server/internal/projects/store/directory.sql.go apps/server/internal/projects/store/models.go \
 openapi/projects.yaml apps/server/internal/openapi/specs/projects.yaml apps/server/internal/projects/gen/api.gen.go \
 apps/projects/frontend/src/api-schema.d.ts apps/server/internal/openapi/openapi_test.go openapi/COVERAGE.md \
 apps/server/internal/contracts/projects.go apps/server/internal/projects/directory.go \
 apps/server/internal/projects/errors.go apps/server/internal/projects/timeline.go \
 apps/server/internal/projects/work_types.go apps/server/internal/projects/work_types_test.go \
 apps/server/internal/projects/directory_test.go apps/server/internal/time/harness_test.go \
 apps/server/internal/time/actuals_test.go apps/server/internal/expenses/harness_test.go \
 apps/server/internal/expenses/contractscalls_internal_test.go apps/server/internal/expenses/projectexpenses_test.go \
 apps/server/internal/module/compose_test.go"
git status --short   # every changed file of this task must be in PATHS, and nothing of anyone else's
git add $PATHS && git commit -F /tmp/claude-1000/msg-wt-1.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 2: Time snapshots the multipliers and multiplies where it sums (D3, D4's provider half)

`workTypeId` on both entry requests, checked and snapshotted before the transaction at every save while draft or rejected, frozen from submit; the base rates stay what the chain resolved; the response carries `workType` and the multiplier inside the shaped blocks; every summed amount multiplies exactly; `ProjectActualsEntry.WorkTypes` splits one project's work per type on the buckets' currency gate.

**Files:**
- Create: `apps/server/internal/db/migrations/00032_time_work_types.sql`, `apps/server/internal/time/work_types_test.go`
- Modify: `apps/server/internal/time/sqlc.yaml`, `apps/server/internal/db/schema_test.go`, `apps/server/internal/time/queries/entries.sql`, `apps/server/internal/time/queries/actuals.sql`, `apps/server/internal/time/queries/stats.sql`, `openapi/time.yaml`, `apps/server/internal/contracts/actuals.go`, `apps/server/internal/time/values.go`, `apps/server/internal/time/entries.go`, `apps/server/internal/time/rates.go`, `apps/server/internal/time/responses.go`, `apps/server/internal/time/actuals.go`, `apps/server/internal/time/harness_test.go`, `apps/server/internal/time/actuals_test.go`, `openapi/COVERAGE.md` (if it moves)
- Generated (commit them): `apps/server/internal/openapi/specs/time.yaml`, `apps/server/internal/time/gen/api.gen.go`, `apps/server/internal/time/store/models.go`, `apps/server/internal/time/store/entries.sql.go`, `apps/server/internal/time/store/actuals.sql.go`, `apps/server/internal/time/store/stats.sql.go`, `apps/server/internal/time/store/approvals.sql.go`, `apps/server/internal/time/store/weeks.sql.go` (their `RETURNING *`/`SELECT *` now carry the four columns) — and every other file `git status` shows under `apps/server/internal/time/store/` after `go generate` — plus `apps/time/frontend/src/api-schema.d.ts`
- Read first (do not change): `time/entries.go:37-190,340-470` (`checkReferences`, the two saves), `time/rates.go` whole, `time/responses.go:88-170` (`entryResponse`), `time/actuals.go:175-300` (`actualsSum.add`, `amountText`), `time/queries/actuals.sql` header, `time/harness_test.go:122-420,470-640` (fixtures, `fakeProjects`, `entryBody`, `updateBody`, `entryJSON`), `time/actuals_test.go:120-175` (`actualsProvider`, `loggedEntry`, `logEntry`, `wantBucket`), `time/stats_test.go:374-420` (`projectSummaryJSON`, `readProjectSummary`), `time/approval_queue_test.go:1-45` (`getApprovals`)

**Interfaces:**
- Consumes: `contracts.WorkTypeEntry`, `ProjectDirectory.WorkType` (Task 1).
- Produces Go: `contracts.WorkTypeActuals{WorkTypeID int32; HoursHundredths int64; BillAmount, CostAmount string}` (no name — Projects names the rows, Task 3), `contracts.ProjectActualsEntry.WorkTypes []WorkTypeActuals`; `parsedEntry.WorkTypeID *int32`, `entryRefs.WorkType *contracts.WorkTypeEntry`, `workTypeNotOnProject`, `workTypeInactive`, `workTypeSnapshot`, `snapshotWorkType(*contracts.WorkTypeEntry) (workTypeSnapshot, error)`, `multiplied(rate, percent float64) float64`, `multiplierOf(rate *float64, stored pgtype.Numeric) (percent, effective *float64, err error)`, `(*actuals).workTypes`; `store.TimeEntry` gains `WorkTypeID *int32`, `WorkTypeName *string`, `BillMultiplierPercent`, `CostMultiplierPercent pgtype.Numeric`; `store.ProjectWorkTypeActualGroups(ctx, projectID int32)`.
- Wire: `TimeEntryRequest`/`TimeEntryUpdateRequest` gain optional `workTypeId` (int32, nullable) — 400 on `workTypeId`: "Work type is not on this project" (unknown, or another project's) / "Work type is no longer active"; `TimeEntryResponse.workType?` `{id, name}` (new required-field schema `TimeEntryWorkType`); `TimeEntryBilling.multiplierPercent?`/`effectiveRate?`, `TimeEntryCost.multiplierPercent?`/`effectiveRate?` — present with a type (the effective rate only when the block has a rate), absent otherwise, shaped with their blocks. The project summary's `billing.amount` multiplies.

- [ ] **Step 1: Pin the migration, see it fail, write it**

In `apps/server/internal/db/schema_test.go`, directly after `TestProjectsWorkTypes_AppliesAndIsIdempotent` (Task 1), add:

```go
// TestTimeWorkTypes_AppliesAndIsIdempotent proves 00032_time_work_types.sql
// applies, rolls back and re-applies cleanly, and pins the entry's work-type
// snapshot (work types design D3): the type's opaque id, its name at the
// name column's width, and the two multipliers at the scale projects stores
// them in — all nullable, NULL being "ordinary hours". COLLATE "C": the
// underscore must sort as a character, not be skipped.
func TestTimeWorkTypes_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 32) // 00032_time_work_types.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var columns string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(string_agg(column_name || ':' || data_type
		       || CASE WHEN data_type = 'numeric' THEN '(' || numeric_precision || ',' || numeric_scale || ')'
		               WHEN data_type = 'character varying' THEN '(' || character_maximum_length || ')'
		               ELSE '' END
		       || ':' || is_nullable, ',' ORDER BY column_name COLLATE "C"), 'MISSING')
		FROM information_schema.columns
		WHERE table_schema = 'time' AND table_name = 'entries'
		  AND column_name IN ('work_type_id', 'work_type_name', 'bill_multiplier_percent', 'cost_multiplier_percent')`).Scan(&columns); err != nil {
		t.Fatalf("read the work-type snapshot columns: %v", err)
	}
	want := "bill_multiplier_percent:numeric(6,2):YES,cost_multiplier_percent:numeric(6,2):YES," +
		"work_type_id:integer:YES,work_type_name:character varying(100):YES"
	if columns != want {
		t.Errorf("snapshot columns = %q, want %q", columns, want)
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestTimeWorkTypes_AppliesAndIsIdempotent' ./internal/db/
```
Expected: FAIL (no migration 32).

Create `apps/server/internal/db/migrations/00032_time_work_types.sql`:

```sql
-- +goose Up
-- The work type an entry was logged as (work types design D3), snapshotted
-- the way its rates are: the project's work-type id, its name as it stood,
-- and its two multipliers — all four NULL when no type was picked, ordinary
-- hours being the absence of a type, not a row. Resolved at every save while
-- the entry is a draft or rejected, frozen from submitted on with the rates.
--
-- bill_rate and cost_rate stay the base rates the chain resolved: the
-- multiplier sits beside them and is applied where amounts are summed
-- (queries/actuals.sql), so nothing is rounded twice and the snapshot still
-- says what the rate was and what multiplied it. numeric(6,2) is the scale
-- projects stores the percentages in, so a snapshot never rounds one.
-- work_type_id is opaque — work types are projects' rows
-- (docs/module-boundaries.md rule 4) — and no CHECK ties the four together,
-- house style: the save writes all four or none.
ALTER TABLE time.entries
    ADD COLUMN work_type_id            integer,
    ADD COLUMN work_type_name          varchar(100),
    ADD COLUMN bill_multiplier_percent numeric(6,2),
    ADD COLUMN cost_multiplier_percent numeric(6,2);

-- +goose Down
ALTER TABLE time.entries
    DROP COLUMN work_type_id,
    DROP COLUMN work_type_name,
    DROP COLUMN bill_multiplier_percent,
    DROP COLUMN cost_multiplier_percent;
```

In `apps/server/internal/time/sqlc.yaml`, after `      - ../db/migrations/00010_time_baseline.sql` add `      - ../db/migrations/00032_time_work_types.sql`.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestTimeWorkTypes_AppliesAndIsIdempotent|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestNoModuleReferencesAnotherModulesSchema' ./internal/db/
```
Expected: PASS.

- [ ] **Step 2: The contract**

In `openapi/time.yaml`:

Replace

```yaml
                    description: The bill rate's currency — the project's for a line or project rate, the person rate card's for a person rate.
                    nullable: true
                    type: string
            type: object
```

with

```yaml
                    description: The bill rate's currency — the project's for a line or project rate, the person rate card's for a person rate.
                    nullable: true
                    type: string
                effectiveRate:
                    description: billRate times multiplierPercent, rounded half up to cents — display only, what one hour of this work type bills at. Nothing stores or sums it; amounts multiply the base rate where they are summed and are rounded once (work types design D3). Absent when no work type was picked or there is no billRate.
                    format: double
                    type: number
                multiplierPercent:
                    description: The picked work type's bill multiplier as it was snapshotted with the rates — frozen from submitted on. billRate stays the base rate the chain resolved. Absent when no work type was picked.
                    format: double
                    type: number
            type: object
```

Replace

```yaml
                currency:
                    description: The person rate card's currency.
                    nullable: true
                    type: string
            type: object
        TimeEntryRejectRequest:
```

with

```yaml
                currency:
                    description: The person rate card's currency.
                    nullable: true
                    type: string
                effectiveRate:
                    description: costRate times multiplierPercent, rounded half up to cents — display only (work types design D3). Absent when no work type was picked or there is no costRate.
                    format: double
                    type: number
                multiplierPercent:
                    description: The picked work type's cost multiplier as it was snapshotted — kept on a non-billable entry too, since overtime costs the company whether or not it bills. Absent when no work type was picked.
                    format: double
                    type: number
            type: object
        TimeEntryRejectRequest:
```

Replace

```yaml
                taskId:
                    description: A task on the project. Its title is snapshotted on the entry, so the entry stays readable after the task is deleted.
                    format: int32
                    nullable: true
                    type: integer
            required:
                - projectId
                - entryDate
                - hours
            type: object
        TimeEntryResponse:
```

with

```yaml
                taskId:
                    description: A task on the project. Its title is snapshotted on the entry, so the entry stays readable after the task is deleted.
                    format: int32
                    nullable: true
                    type: integer
                workTypeId:
                    description: One of the project's active work types (work types design D3). Its name and multipliers are snapshotted with the rates; absent or null is ordinary hours.
                    format: int32
                    nullable: true
                    type: integer
            required:
                - projectId
                - entryDate
                - hours
            type: object
        TimeEntryResponse:
```

Replace

```yaml
                userId:
                    format: uuid
                    type: string
            required:
                - id
                - userId
                - userDisplayName
```

with

```yaml
                userId:
                    format: uuid
                    type: string
                workType:
                    allOf:
                        - $ref: '#/components/schemas/TimeEntryWorkType'
                    description: The work type the entry was logged as, as it was snapshotted. Absent — not null — for ordinary hours. Visible to whoever sees the entry; the multipliers are inside billing and cost.
            required:
                - id
                - userId
                - userDisplayName
```

Replace

```yaml
                taskId:
                    description: A task on the project. Its title is snapshotted on the entry, so the entry stays readable after the task is deleted.
                    format: int32
                    nullable: true
                    type: integer
            required:
                - projectId
                - entryDate
                - hours
                - revision
            type: object
        TimePersonOverview:
```

with

```yaml
                taskId:
                    description: A task on the project. Its title is snapshotted on the entry, so the entry stays readable after the task is deleted.
                    format: int32
                    nullable: true
                    type: integer
                workTypeId:
                    description: One of the project's active work types (work types design D3). A full replace, like every other field — left out, the entry is ordinary hours again.
                    format: int32
                    nullable: true
                    type: integer
            required:
                - projectId
                - entryDate
                - hours
                - revision
            type: object
        TimeEntryWorkType:
            description: The work type an entry was logged as, by the id projects gave it and the name it had when the entry was saved.
            properties:
                id:
                    format: int32
                    type: integer
                name:
                    type: string
            required:
                - id
                - name
            type: object
        TimePersonOverview:
```

- [ ] **Step 3: The queries, the contract field, and generate**

In `apps/server/internal/time/queries/entries.sql`, `InsertEntry` becomes

```sql
-- name: InsertEntry :one
-- InsertEntry creates a draft entry with its rates already resolved and
-- snapshotted (D3), and its work type beside them (work types design D3: all
-- four NULL for ordinary hours). created_at and updated_at are the same
-- instant, supplied by the caller from Deps.Clock(); status and revision take
-- the column defaults ('draft', 1).
INSERT INTO time.entries (
    user_id, project_id, billing_line_id, task_id, task_title, entry_date,
    hours, start_time, end_time, note, billable,
    bill_rate, bill_currency, cost_rate, cost_currency, rate_source,
    work_type_id, work_type_name, bill_multiplier_percent, cost_multiplier_percent,
    created_at, updated_at
) VALUES (
    @user_id, @project_id, @billing_line_id, @task_id, @task_title, @entry_date,
    @hours, @start_time, @end_time, @note, @billable,
    @bill_rate, @bill_currency, @cost_rate, @cost_currency, @rate_source,
    @work_type_id, @work_type_name, @bill_multiplier_percent, @cost_multiplier_percent,
    @now::timestamptz, @now::timestamptz
)
RETURNING *;
```

and in `UpdateEntry`, replace

```sql
    rate_source = @rate_source,
    status = 'draft',
```

with

```sql
    rate_source = @rate_source,
    work_type_id = @work_type_id,
    work_type_name = @work_type_name,
    bill_multiplier_percent = @bill_multiplier_percent,
    cost_multiplier_percent = @cost_multiplier_percent,
    status = 'draft',
```

and add to `UpdateEntry`'s comment, after its first sentence, `The work type is snapshotted again with them (work types design D3).`

In `apps/server/internal/time/queries/actuals.sql`, directly before `-- name: ProjectActualGroups :many`, add the paragraph

```sql
--
-- The amounts are multiplied where they are summed (work types design D3):
-- each entry's hours × its base rate × its work type's multiplier, NULL (no
-- type) counting as 100 %. The multiplier is applied as COALESCE(pct, 100) ×
-- 0.01 — a multiplication, which numeric does exactly — rather than / 100, a
-- division PostgreSQL rounds to a scale that shrinks as the value grows; the
-- one rounding stays Go's, at the end. 333.33 × 1.5 h × 150 % is 749.9925 and
-- is published 749.99, where multiplying the rate first (499.995 → 500.00)
-- would bill 750.00.
```

and replace

```sql
       COALESCE(SUM(hours * bill_rate) FILTER (WHERE billable), 0)::text AS bill_amount,
       COALESCE(SUM(hours * cost_rate), 0)::text AS cost_amount,
```

with

```sql
       COALESCE(SUM(hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01)) FILTER (WHERE billable), 0)::text AS bill_amount,
       COALESCE(SUM(hours * cost_rate * (COALESCE(cost_multiplier_percent, 100) * 0.01)), 0)::text AS cost_amount,
```

and append to the file:

```sql

-- name: ProjectWorkTypeActualGroups :many
-- ProjectWorkTypeActualGroups is one project's logged work per work type
-- (work types design D4), all buckets together: the hours, and what they
-- bill and cost at the base rate times the multiplier each entry snapshotted,
-- grouped per bill and cost currency so Go folds the amounts on the currency
-- rule the buckets use. Entries without a type are not here — ordinary hours
-- are the absence of a type, not one of them. No name: projects owns the
-- type and names it (work types design D4, as ruled on the plan's review).
-- COALESCE on the id only tells sqlc what the WHERE already guarantees.
SELECT COALESCE(work_type_id, 0)::integer AS work_type_id,
       bill_currency,
       cost_currency,
       SUM(hours * 100)::bigint AS hours_hundredths,
       COALESCE(SUM(hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01)) FILTER (WHERE billable), 0)::text AS bill_amount,
       COALESCE(SUM(hours * cost_rate * (COALESCE(cost_multiplier_percent, 100) * 0.01)), 0)::text AS cost_amount
FROM time.entries
WHERE project_id = @project_id AND work_type_id IS NOT NULL
GROUP BY work_type_id, bill_currency, cost_currency
ORDER BY work_type_id;
```

In `apps/server/internal/time/queries/stats.sql`, `ProjectBillingTotals`: replace `-- bill rate each entry snapshotted, summed exactly and rounded to cents, and` with `-- bill rate each entry snapshotted times its work type's bill multiplier (work types design D3), summed exactly and rounded to cents, and`, and replace

```sql
       COALESCE(ROUND(SUM(hours * bill_rate) FILTER (WHERE bill_rate IS NOT NULL), 2), 0)::numeric(14,2) AS amount,
```

with

```sql
       COALESCE(ROUND(SUM(hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01)) FILTER (WHERE bill_rate IS NOT NULL), 2), 0)::numeric(14,2) AS amount,
```

In `apps/server/internal/contracts/actuals.go`, replace

```go
	Lines []LineActuals
}
```

(the end of `ProjectActualsEntry`) with

```go
	Lines []LineActuals
	// WorkTypes is the same project's logged work per work type (work types
	// design D4): all three buckets together, one entry per type at least one
	// entry was logged as, by WorkTypeID ascending. Work logged as no type —
	// ordinary hours — is in no entry. The amounts follow the buckets'
	// currency rule: only work logged in the requested currency is in them,
	// and other-currency work contributes its hours to HoursHundredths and
	// nothing else. Each type is rounded once, on its own.
	// ActualsForProjects does not carry it.
	WorkTypes []WorkTypeActuals
}

// WorkTypeActuals is what was logged as one work type, by the type's id and
// nothing else: the type's name is the module that owns work types' to say
// (projects names the rows from its own table), and the provider asks the
// project directory nothing while serving.
type WorkTypeActuals struct {
	WorkTypeID      int32
	HoursHundredths int64
	// BillAmount and CostAmount are decimal text with two decimals, at the
	// base rate times the multiplier each entry snapshotted, "0.00" when
	// there is nothing — the ActualsBucket shape.
	BillAmount, CostAmount string
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Expected: the generated files listed under **Files** change; the build still passes (nothing reads the new fields yet). sqlc should emit `InsertEntryParams`/`UpdateEntryParams` fields `WorkTypeID *int32`, `WorkTypeName *string`, `BillMultiplierPercent`, `CostMultiplierPercent pgtype.Numeric`, and `ProjectWorkTypeActualGroupsRow{WorkTypeID int32; BillCurrency, CostCurrency *string; HoursHundredths int64; BillAmount, CostAmount string}`, with `ProjectWorkTypeActualGroups(ctx, projectID int32)`; oapi-codegen `gen.TimeEntryRequest.WorkTypeId *int32`, `gen.TimeEntryResponse.WorkType *gen.TimeEntryWorkType`, `gen.TimeEntryBilling.MultiplierPercent`/`EffectiveRate *float64`, the same on `gen.TimeEntryCost`. If a name differs, use the generated one and say so.

- [ ] **Step 4: The fixtures and the decoders**

In `apps/server/internal/time/harness_test.go`, directly after the task `const` block (it ends with `taskSpecificationTitle = "Skriv spesifikasjonen"` and `)`), add:

```go
// The work types the fake directory knows (work types design D1): on 1001,
// overtime billed at 150 % and costed at 140 %, a weekend at 200 % and 150 %,
// and a retired type; on 1004, overtime at 150 % both ways — "a type on
// another project" for 1001, and the EUR case.
const (
	workTypeOvertime = 6001
	workTypeWeekend  = 6002
	workTypeRetired  = 6003
	workTypeEuro     = 6004
	workTypeUnknown  = 6999

	workTypeOvertimeName = "Overtid 50 %"
	workTypeWeekendName  = "Helg"
)
```

in `newFakeProjects`, replace `		workTypes: map[int32]contracts.WorkTypeEntry{},` (Task 1) with

```go
		workTypes: map[int32]contracts.WorkTypeEntry{
			workTypeOvertime: {
				ID: workTypeOvertime, ProjectID: projectKraftVerket, Name: workTypeOvertimeName,
				BillMultiplierPercent: 150, CostMultiplierPercent: 140, Active: true,
			},
			workTypeWeekend: {
				ID: workTypeWeekend, ProjectID: projectKraftVerket, Name: workTypeWeekendName,
				BillMultiplierPercent: 200, CostMultiplierPercent: 150, Active: true,
			},
			workTypeRetired: {
				ID: workTypeRetired, ProjectID: projectKraftVerket, Name: "Gammel overtid",
				BillMultiplierPercent: 150, CostMultiplierPercent: 150, Active: false,
			},
			workTypeEuro: {
				ID: workTypeEuro, ProjectID: projectEuro, Name: "Overtime",
				BillMultiplierPercent: 150, CostMultiplierPercent: 150, Active: true,
			},
		},
```

and directly after `removeTask`'s closing brace add:

```go
// setWorkType changes one work type the way editing it in projects would, so
// a test can prove what a saved entry snapshotted stays put (D3's freeze).
func (f *fakeProjects) setWorkType(id int32, name string, bill, cost float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wt := f.workTypes[id]
	wt.Name, wt.BillMultiplierPercent, wt.CostMultiplierPercent = name, bill, cost
	f.workTypes[id] = wt
}
```

In the same file, add to `entryJSON`, after the `Cost *costJSON` field, `WorkType *workTypeJSON \`json:"workType"\``; replace `billingJSON` and `costJSON` with

```go
type billingJSON struct {
	BillRate          *float64 `json:"billRate"`
	Currency          *string  `json:"currency"`
	MultiplierPercent *float64 `json:"multiplierPercent"`
	EffectiveRate     *float64 `json:"effectiveRate"`
}

type costJSON struct {
	CostRate          *float64 `json:"costRate"`
	Currency          *string  `json:"currency"`
	MultiplierPercent *float64 `json:"multiplierPercent"`
	EffectiveRate     *float64 `json:"effectiveRate"`
}

// workTypeJSON decodes TimeEntryWorkType.
type workTypeJSON struct {
	Id   int32  `json:"id"`
	Name string `json:"name"`
}
```

and in `updateBody` (`maps.Copy(body, overrides)` appears in `entryBody` too, so anchor on the switch above it) replace

```go
		case *string:
			if v != nil {
				body[field] = *v
			}
		}
	}
	maps.Copy(body, overrides)
```

with

```go
		case *string:
			if v != nil {
				body[field] = *v
			}
		}
	}
	// A full replace: an entry logged as a work type carries it along, or the
	// update would make it ordinary hours again.
	if e.WorkType != nil {
		body["workTypeId"] = e.WorkType.Id
	}
	maps.Copy(body, overrides)
```

In `apps/server/internal/time/actuals_test.go`, replace `loggedEntry` and `logEntry` with

```go
// loggedEntry is one row seeded straight into time.entries: the provider
// reads whatever is there, whatever path put it there, and a test about
// currencies and statuses needs combinations no create call can reach
// (invoiced, a cost in another currency than the bill). The four work-type
// fields are the snapshot (work types design D3); nil is ordinary hours.
type loggedEntry struct {
	project        int32
	line           *int32
	date           string
	hours          string
	billable       bool
	billRate       any
	billCurrency   any
	costRate       any
	costCurrency   any
	status         string
	workType       any
	workTypeName   any
	billMultiplier any
	costMultiplier any
}

// logEntry seeds one row, so a test reads as the list of what was logged.
func logEntry(t *testing.T, h *harness, userID uuid.UUID, e loggedEntry) {
	t.Helper()
	source := "none"
	if e.billRate != nil {
		source = "project"
	}
	h.Exec(t, `INSERT INTO time.entries
	    (user_id, project_id, billing_line_id, entry_date, hours, billable,
	     bill_rate, bill_currency, cost_rate, cost_currency, rate_source, status, created_at, updated_at,
	     work_type_id, work_type_name, bill_multiplier_percent, cost_multiplier_percent)
	    VALUES ($1, $2, $3, $4::date, $5::numeric, $6, $7::numeric, $8, $9::numeric, $10, $11, $12, now(), now(),
	            $13::integer, $14, $15::numeric, $16::numeric)`,
		userID, e.project, e.line, e.date, e.hours, e.billable,
		e.billRate, e.billCurrency, e.costRate, e.costCurrency, source, e.status,
		e.workType, e.workTypeName, e.billMultiplier, e.costMultiplier)
}
```

- [ ] **Step 5: Write the failing tests**

Append to `apps/server/internal/time/actuals_test.go`:

```go
// D3 where the money is summed: an entry's amount is hours × base rate ×
// multiplier, exact, rounded once. 333.33 × 1.5 h × 150 % is 749.9925, so
// 749.99 — multiplying the rate first (499.995 → 500.00) would bill 750.00 —
// and the cost the same way on its own multiplier: × 125 % is 624.99375,
// 624.99. The per-type split carries the same figures.
func TestActualsMultipliesWhereItSums(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectEuro, date: workDay, hours: "1.50", billable: true,
		billRate: "333.33", billCurrency: "EUR", costRate: "333.33", costCurrency: "EUR", status: "approved",
		workType: workTypeEuro, workTypeName: "Overtime", billMultiplier: "150.00", costMultiplier: "125.00"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectEuro, Currency: ptr("EUR")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 150, "749.99", "624.99")
	wantBucket(t, "total", got.Totals.Total, 150, "749.99", "624.99")
	want := contracts.WorkTypeActuals{WorkTypeID: workTypeEuro, HoursHundredths: 150, BillAmount: "749.99", CostAmount: "624.99"}
	if len(got.WorkTypes) != 1 || got.WorkTypes[0] != want {
		t.Errorf("work types = %+v, want [%+v]", got.WorkTypes, want)
	}
}

// D4's split: one entry per type anything was logged as, by id, all buckets
// together; ordinary hours in none of them; the amounts on the buckets'
// currency rule, other-currency hours counted in the hours and nowhere else;
// no currency asked, no amounts. ActualsForProjects is unchanged in shape and
// carries the same multiplied totals.
func TestActualsReportsEveryWorkTypeInTheProjectsCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	for _, e := range []loggedEntry{
		{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true, status: "approved",
			billRate: "900.00", billCurrency: "NOK", costRate: "400.00", costCurrency: "NOK",
			workType: workTypeOvertime, workTypeName: workTypeOvertimeName, billMultiplier: "150.00", costMultiplier: "140.00"},
		{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true, status: "submitted",
			billRate: "100.00", billCurrency: "SEK",
			workType: workTypeOvertime, workTypeName: workTypeOvertimeName, billMultiplier: "150.00", costMultiplier: "140.00"},
		{project: projectKraftVerket, date: workDay, hours: "3.00", billable: true, status: "draft",
			billRate: "900.00", billCurrency: "NOK",
			workType: workTypeWeekend, workTypeName: workTypeWeekendName, billMultiplier: "200.00", costMultiplier: "150.00"},
		{project: projectKraftVerket, date: workDay, hours: "4.00", billable: true, status: "approved",
			billRate: "900.00", billCurrency: "NOK"},
	} {
		logEntry(t, h, user, e)
	}

	nok, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	want := []contracts.WorkTypeActuals{
		{WorkTypeID: workTypeOvertime, HoursHundredths: 300, BillAmount: "2700.00", CostAmount: "1120.00"},
		{WorkTypeID: workTypeWeekend, HoursHundredths: 300, BillAmount: "5400.00", CostAmount: "0.00"},
	}
	if !slices.Equal(nok.WorkTypes, want) {
		t.Errorf("work types in NOK = %+v, want %+v", nok.WorkTypes, want)
	}
	// 2 h × 900 × 150 % + 3 h × 900 × 200 % + 4 h × 900; the SEK hour prices nothing here.
	wantBucket(t, "total in NOK", nok.Totals.Total, 1000, "11700.00", "1120.00")

	none, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	if len(none.WorkTypes) != 2 || none.WorkTypes[0].HoursHundredths != 300 ||
		none.WorkTypes[0].BillAmount != "0.00" || none.WorkTypes[0].CostAmount != "0.00" {
		t.Errorf("work types without a currency = %+v, want the hours and no amounts", none.WorkTypes)
	}

	batch, err := p.ActualsForProjects(t.Context(), []contracts.ActualsRequest{{ProjectID: projectKraftVerket, Currency: ptr("NOK")}})
	if err != nil {
		t.Fatalf("actuals for projects: %v", err)
	}
	if batch[projectKraftVerket].Total != nok.Totals.Total {
		t.Errorf("batch total = %+v, want the single read's %+v", batch[projectKraftVerket].Total, nok.Totals.Total)
	}
}
```

and add `"slices"` to `actuals_test.go`'s import block if it is not there.

Create `apps/server/internal/time/work_types_test.go`:

```go
package timetracking_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// An entry and its work type (work types design D3): checked and snapshotted
// at every save while draft or rejected, frozen from submit, the base rates
// left as the chain resolved them, and the multipliers shaped with the blocks
// they multiply. actuals_test.go carries what the sums make of it.

// snapshotOf is an entry's four work-type columns as stored, "-" for NULL.
func snapshotOf(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		SELECT coalesce(work_type_id::text, '-') || '|' || coalesce(work_type_name, '-') || '|' ||
		       coalesce(bill_multiplier_percent::text, '-') || '|' || coalesce(cost_multiplier_percent::text, '-')
		FROM time.entries WHERE id = $1`, id)
}

// D3's snapshot: the type's id and name, its multipliers beside the rates,
// the stored rates the base ones, rateSource untouched — the type multiplies
// whatever step won — and the effective rates for display.
func TestPostTimeEntries_SnapshotsTheWorkTypeBesideTheBaseRates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")

	e := createEntry(t, owner, map[string]any{"workTypeId": workTypeOvertime})

	if e.WorkType == nil || e.WorkType.Id != workTypeOvertime || e.WorkType.Name != workTypeOvertimeName {
		t.Errorf("workType = %+v, want %d %q", e.WorkType, workTypeOvertime, workTypeOvertimeName)
	}
	if e.RateSource != "project" {
		t.Errorf("rateSource = %q, want project: the work type is orthogonal to the chain", e.RateSource)
	}
	if b := e.Billing; b == nil || deref(b.BillRate) != 900.0 || deref(b.MultiplierPercent) != 150.0 || deref(b.EffectiveRate) != 1350.0 {
		t.Errorf("billing = %+v, want 900 at 150 %%, 1350 an hour", e.Billing)
	}
	if c := e.Cost; c == nil || deref(c.CostRate) != 400.0 || deref(c.MultiplierPercent) != 140.0 || deref(c.EffectiveRate) != 560.0 {
		t.Errorf("cost = %+v, want 400 at 140 %%, 560 an hour", e.Cost)
	}
	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid 50 %|150.00|140.00" {
		t.Errorf("snapshot = %q", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT bill_rate::text || '|' || cost_rate::text FROM time.entries WHERE id = $1`, e.Id); got != "900.00|400.00" {
		t.Errorf("stored rates = %q, want the base rates 900.00|400.00", got)
	}
}

// The effective rate is display, rounded half up once: 333.33 at 150 % is
// 499.995, shown 500.00. What the hours are worth is multiplied where it is
// summed (actuals_test.go): 749.99, not 1.5 × 500.00.
func TestPostTimeEntries_TheEffectiveRateIsForDisplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectEuro, roleMember)
	seedRate(t, h, ownerID, "2026-01-01", 333.33, nil, "EUR")

	e := createEntry(t, owner, map[string]any{"projectId": projectEuro, "hours": 1.5, "workTypeId": workTypeEuro})
	if e.RateSource != "person" || e.Billing == nil || deref(e.Billing.BillRate) != 333.33 || deref(e.Billing.EffectiveRate) != 500.0 {
		t.Errorf("entry = %s %+v, want the person's 333.33 shown as 500.00 an hour", e.RateSource, e.Billing)
	}
	got, err := actualsProvider(t, h).Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectEuro, Currency: ptr("EUR")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "draft", got.Totals.Draft, 150, "749.99", "0.00")
}

// D3's refusals, on the field: a type nobody has and one on another project
// are the one message, a retired one its own — on a create and an update.
func TestTimeEntries_RefuseAWorkTypeNotOnTheProjectOrRetired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for id, want := range map[int32]string{
		workTypeEuro:    "Work type is not on this project",
		workTypeUnknown: "Work type is not on this project",
		workTypeRetired: "Work type is no longer active",
	} {
		errs := fieldErrors(t, owner, entryBody(map[string]any{"workTypeId": id}))
		if !slices.Equal(errs["workTypeId"], []string{want}) {
			t.Errorf("workTypeId %d: errors %v, want %q", id, errs, want)
		}
	}

	e := createEntry(t, owner, nil)
	r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"workTypeId": workTypeRetired}))
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || !slices.Equal(problem.Errors["workTypeId"], []string{"Work type is no longer active"}) {
		t.Errorf("update to a retired type: status %d body %s, want 400 on workTypeId", r.Status, r.Body)
	}
}

// Ordinary hours: no workType key, no multiplier in either block, four NULLs;
// and a full replace that leaves workTypeId out takes a type off again.
func TestTimeEntries_WithoutAWorkType_StoreAndAnswerNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")

	e := createEntry(t, owner, nil)
	raw := rawEntry(t, owner, e.Id)
	if _, ok := raw["workType"]; ok {
		t.Errorf("workType = %v, want the key absent", raw["workType"])
	}
	for _, block := range []string{"billing", "cost"} {
		fields, _ := raw[block].(map[string]any)
		for _, key := range []string{"multiplierPercent", "effectiveRate"} {
			if _, ok := fields[key]; ok {
				t.Errorf("%s.%s = %v, want it absent for ordinary hours", block, key, fields[key])
			}
		}
	}
	if got := snapshotOf(t, h, e.Id); got != "-|-|-|-" {
		t.Errorf("snapshot = %q, want four NULLs", got)
	}

	typed := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15", "workTypeId": workTypeOvertime})
	cleared := updateEntry(t, owner, typed, map[string]any{"workTypeId": nil})
	if cleared.WorkType != nil || snapshotOf(t, h, typed.Id) != "-|-|-|-" {
		t.Errorf("after a replace without workTypeId: %+v, snapshot %q, want ordinary hours", cleared.WorkType, snapshotOf(t, h, typed.Id))
	}
}

// D3 on a non-billable entry: it keeps its cost multiplier — overtime costs
// the company whether or not it bills — and bills nothing. Both multipliers
// are snapshotted; the billing block carries the bill one and no effective
// rate, since the chain gives a non-billable entry no rate to multiply; and
// actuals cost the hours multiplied and bill them at 0.00.
func TestWorkTypes_ANonBillableEntryKeepsItsCostMultiplier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")

	e := createEntry(t, owner, map[string]any{"billable": false, "workTypeId": workTypeOvertime})

	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid 50 %|150.00|140.00" {
		t.Errorf("snapshot = %q, want both multipliers kept", got)
	}
	if c := e.Cost; c == nil || deref(c.MultiplierPercent) != 140.0 || deref(c.EffectiveRate) != 560.0 {
		t.Errorf("cost = %+v, want 400 at 140 %%, 560 an hour", e.Cost)
	}
	raw := rawEntry(t, owner, e.Id)
	billing, ok := raw["billing"].(map[string]any)
	if !ok {
		t.Fatalf("billing = %v, want the owner's block", raw["billing"])
	}
	if _, has := billing["effectiveRate"]; has {
		t.Errorf("billing.effectiveRate = %v, want it absent: a non-billable entry has no rate to multiply", billing["effectiveRate"])
	}
	if billing["multiplierPercent"] != 150.0 {
		t.Errorf("billing.multiplierPercent = %v, want the snapshotted 150", billing["multiplierPercent"])
	}

	got, err := actualsProvider(t, h).Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	// 2 h × 400 × 140 % = 1120.00; nothing is billed.
	wantBucket(t, "draft", got.Totals.Draft, 200, "0.00", "1120.00")
}

// D3's freeze: a multiplier changed in projects after submit does not move a
// submitted entry; a rejected entry is a draft again, and its next save
// snapshots the type as it now stands.
func TestWorkTypes_FrozenFromSubmit_AndSnapshottedAgainWhenRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	e := submittedEntry(t, owner, map[string]any{"workTypeId": workTypeOvertime})

	h.projects.setWorkType(workTypeOvertime, "Overtid", 175, 160)
	frozen := getEntry(t, owner, e.Id)
	if frozen.WorkType == nil || frozen.WorkType.Name != workTypeOvertimeName || deref(frozen.Billing.MultiplierPercent) != 150.0 {
		t.Errorf("submitted entry = %+v %+v, want the snapshot it was submitted with", frozen.WorkType, frozen.Billing)
	}
	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid 50 %|150.00|140.00" {
		t.Errorf("snapshot = %q, want it untouched", got)
	}

	rejected := rejectEntries(t, approver, "Wrong day", e.Id)[0]
	resaved := updateEntry(t, owner, rejected, nil)
	if resaved.WorkType == nil || resaved.WorkType.Name != "Overtid" ||
		deref(resaved.Billing.MultiplierPercent) != 175.0 || deref(resaved.Billing.EffectiveRate) != 1575.0 {
		t.Errorf("resaved = %+v %+v, want the type as it now stands: Overtid, 175 %%, 1575", resaved.WorkType, resaved.Billing)
	}
	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid|175.00|160.00" {
		t.Errorf("snapshot = %q, want it taken again", got)
	}
}

// D8's shaping, carried over: the type is on everyone's copy; the bill
// multiplier travels inside billing (owner, project manager), the cost one
// inside cost (time:view-all), each absent where its block is.
func TestWorkTypes_TheMultipliersAreShapedWithTheirBlocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	viewAll, _ := signIn(t, h, "time:view-all")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")
	e := createEntry(t, owner, map[string]any{"workTypeId": workTypeOvertime})

	for name, c := range map[string]*modtest.Client{"owner": owner, "manager": manager, "time:view-all": viewAll} {
		raw := rawEntry(t, c, e.Id)
		workType, _ := raw["workType"].(map[string]any)
		if workType["name"] != workTypeOvertimeName {
			t.Errorf("%s: workType = %v, want it on every copy", name, raw["workType"])
		}
		billing, seesBilling := raw["billing"].(map[string]any)
		cost, seesCost := raw["cost"].(map[string]any)
		switch name {
		case "owner", "manager":
			if !seesBilling || billing["multiplierPercent"] != 150.0 || seesCost {
				t.Errorf("%s: billing %v cost %v, want the bill multiplier and no cost block", name, raw["billing"], raw["cost"])
			}
		case "time:view-all":
			if seesBilling || !seesCost || cost["multiplierPercent"] != 140.0 || cost["effectiveRate"] != 560.0 {
				t.Errorf("%s: billing %v cost %v, want the cost multiplier and no billing block", name, raw["billing"], raw["cost"])
			}
		}
	}
}

// The approval queue renders entries through the same code, so its entries
// carry the type.
func TestGetTimeApprovals_CarriesTheWorkType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	e := submittedEntry(t, owner, map[string]any{"workTypeId": workTypeWeekend})

	var found *entryJSON
	for _, g := range getApprovals(t, approver, "").Data {
		for i := range g.Entries {
			if g.Entries[i].Id == e.Id {
				found = &g.Entries[i]
			}
		}
	}
	if found == nil || found.WorkType == nil || found.WorkType.Id != workTypeWeekend || found.WorkType.Name != workTypeWeekendName {
		t.Errorf("queued entry = %+v, want it carrying %q", found, workTypeWeekendName)
	}
}

// The project summary's billed amount multiplies where it sums too: 2 h at
// 900 × 150 % and 1 h at 900 is 3600.
func TestGetTimeProjectSummary_BillsTheWorkTypesMultiplier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	submittedEntry(t, owner, map[string]any{"hours": 2, "workTypeId": workTypeOvertime})
	submittedEntry(t, owner, map[string]any{"hours": 1, "entryDate": "2026-09-15"})

	s, _ := readProjectSummary(t, manager, projectKraftVerket)
	if s.Billing == nil || s.Billing.Amount != 3600 {
		t.Errorf("billing = %+v, want 3600", s.Billing)
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'WorkType|TestActualsMultipliesWhereItSums|TestActualsReportsEveryWorkTypeInTheProjectsCurrency|TestGetTimeApprovals_CarriesTheWorkType|TestGetTimeProjectSummary_BillsTheWorkTypesMultiplier|TestPostTimeEntries_TheEffectiveRateIsForDisplay' ./internal/time/
```
Expected: FAIL — `workTypeId` is ignored (no `workType` on the entry, snapshot `-|-|-|-`, no 400s), the actuals bill `750.00`/`2700` unmultiplied as `1800.00`, and `WorkTypes` is empty.

- [ ] **Step 6: Implement**

In `apps/server/internal/time/values.go`, directly after the `cannotLogTime` constant add:

```go
// The two work-type refusals (work types design D3), on workTypeId. A type
// nobody has and one on another project are the same message: which ids exist
// on other projects is not the caller's to learn from a refusal.
const (
	workTypeNotOnProject = "Work type is not on this project"
	workTypeInactive     = "Work type is no longer active"
)
```

add `WorkTypeID *int32` as the last field of `parsedEntry`, and `WorkTypeID: body.WorkTypeId,` as the last field of `parseEntry`'s returned literal.

In `apps/server/internal/time/entries.go`:

- `entryRefs` gains a last field `WorkType  *contracts.WorkTypeEntry`, and its comment `the billing line and the task's title (when given).` becomes `the billing line, the task's title and the work type (when given).`
- in `checkReferences`, directly before its final `return refs, errs, nil`, add:

```go
	// The work type is projects' rule, read here with the project and the
	// line — before any transaction opens (work types design D2, D3) — and
	// snapshotted by the save that called this.
	if p.WorkTypeID != nil {
		wt, err := s.deps.Projects.WorkType(ctx, *p.WorkTypeID)
		if err != nil {
			return refs, nil, fmt.Errorf("time: look up the work type: %w", err)
		}
		switch {
		case wt == nil || wt.ProjectID != p.ProjectID:
			errs = withFieldError(errs, "workTypeId", workTypeNotOnProject)
		case !wt.Active:
			errs = withFieldError(errs, "workTypeId", workTypeInactive)
		default:
			refs.WorkType = wt
		}
	}
```

  and add to its doc comment's list, after `whose title is snapshotted (D5)`, `, and the work type is one of the project's and active, whose name and multipliers are snapshotted (work types design D3)`.
- in `PostTimeEntries`, replace

```go
	costRate, err := numericFromFloatPtr(rates.CostRate)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.TimeEntry
```

  with

```go
	costRate, err := numericFromFloatPtr(rates.CostRate)
	if err != nil {
		return nil, err
	}
	workType, err := snapshotWorkType(refs.WorkType)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.TimeEntry
```

- in `PutTimeEntriesById`, replace

```go
	costRate, err := numericFromFloatPtr(rates.CostRate)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var (
```

  with

```go
	costRate, err := numericFromFloatPtr(rates.CostRate)
	if err != nil {
		return nil, err
	}
	workType, err := snapshotWorkType(refs.WorkType)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var (
```

- replace **both** occurrences (the `InsertEntryParams` and the `UpdateEntryParams` literal) of

```go
			RateSource:    rates.Source,
			Now:           now,
```

  with

```go
			RateSource:            rates.Source,
			WorkTypeID:            workType.ID,
			WorkTypeName:          workType.Name,
			BillMultiplierPercent: workType.BillMultiplierPercent,
			CostMultiplierPercent: workType.CostMultiplierPercent,
			Now:                   now,
```

  (then `mise exec -- gofmt -w internal/time` realigns the rest of both literals);
- in `requestFromUpdate`, add `WorkTypeId:    body.WorkTypeId,` after `Billable:      body.Billable,`.

In `apps/server/internal/time/rates.go`, add `"github.com/jackc/pgx/v5/pgtype"` to the imports and append:

```go
// workTypeSnapshot is what an entry stores about the work type it was logged
// as (work types design D3), nil and SQL NULL in every field for ordinary
// hours. It is taken at the same save points the rates are, and so frozen
// with them from submitted on.
type workTypeSnapshot struct {
	ID                    *int32
	Name                  *string
	BillMultiplierPercent pgtype.Numeric
	CostMultiplierPercent pgtype.Numeric
}

// snapshotWorkType turns the directory's answer into the four columns. The
// percentages go in through their shortest decimal text, the same text
// projects stored them from, so the numeric(6,2) columns hold what projects
// holds.
func snapshotWorkType(wt *contracts.WorkTypeEntry) (workTypeSnapshot, error) {
	if wt == nil {
		return workTypeSnapshot{}, nil
	}
	bill, err := numericFromFloatPtr(&wt.BillMultiplierPercent)
	if err != nil {
		return workTypeSnapshot{}, err
	}
	cost, err := numericFromFloatPtr(&wt.CostMultiplierPercent)
	if err != nil {
		return workTypeSnapshot{}, err
	}
	id, name := wt.ID, wt.Name
	return workTypeSnapshot{ID: &id, Name: &name, BillMultiplierPercent: bill, CostMultiplierPercent: cost}, nil
}

// multiplied is a rate under a work type's multiplier, for display (work
// types design D3): rate × percent / 100 in exact decimal, rounded half up to
// cents once. Nothing stores it and nothing sums it — the amounts multiply
// the base rate where the hours are added up (queries/actuals.sql), so an
// amount is rounded once and never from a rounded rate.
func multiplied(rate, percent float64) float64 {
	r := exactDecimal(rate)
	r.Mul(r, exactDecimal(percent))
	r.Quo(r, big.NewRat(100, 1))
	return roundHalfUpCents(r)
}
```

In `apps/server/internal/time/responses.go`, add `"github.com/jackc/pgx/v5/pgtype"` to the imports; in `entryResponse`, directly before `	if a.CanSeeBilling {` add

```go
	// The type is not money: whoever sees the entry sees what it was logged
	// as. Its multipliers are inside the blocks below, shaped with them.
	if row.WorkTypeID != nil && row.WorkTypeName != nil {
		resp.WorkType = &gen.TimeEntryWorkType{Id: *row.WorkTypeID, Name: *row.WorkTypeName}
	}
```

replace

```go
		resp.Billing = &gen.TimeEntryBilling{BillRate: rate, Currency: row.BillCurrency}
```

with

```go
		billing := gen.TimeEntryBilling{BillRate: rate, Currency: row.BillCurrency}
		if billing.MultiplierPercent, billing.EffectiveRate, err = multiplierOf(rate, row.BillMultiplierPercent); err != nil {
			return gen.TimeEntryResponse{}, err
		}
		resp.Billing = &billing
```

replace

```go
		resp.Cost = &gen.TimeEntryCost{CostRate: rate, Currency: row.CostCurrency}
```

with

```go
		cost := gen.TimeEntryCost{CostRate: rate, Currency: row.CostCurrency}
		if cost.MultiplierPercent, cost.EffectiveRate, err = multiplierOf(rate, row.CostMultiplierPercent); err != nil {
			return gen.TimeEntryResponse{}, err
		}
		resp.Cost = &cost
```

and append:

```go
// multiplierOf is one block's work-type half (work types design D3): the
// snapshotted multiplier, nil for ordinary hours, and the rate under it, nil
// too when the block has no rate to multiply.
func multiplierOf(rate *float64, stored pgtype.Numeric) (*float64, *float64, error) {
	percent, err := floatPtrFromNumeric(stored)
	if err != nil || percent == nil {
		return nil, nil, err
	}
	if rate == nil {
		return percent, nil, nil
	}
	effective := multiplied(*rate, *percent)
	return percent, &effective, nil
}
```

In `apps/server/internal/time/actuals.go`, add `"cmp"` to the imports; replace

```go
	if noLine != nil {
		entry.Lines = append(entry.Lines, contracts.LineActuals{Totals: noLine.totals()})
	}
	return entry, nil
}
```

with

```go
	if noLine != nil {
		entry.Lines = append(entry.Lines, contracts.LineActuals{Totals: noLine.totals()})
	}
	workTypes, err := a.workTypes(ctx, req)
	if err != nil {
		return contracts.ProjectActualsEntry{}, err
	}
	entry.WorkTypes = workTypes
	return entry, nil
}

// workTypes is one project's work per work type (work types design D4): the
// hours of every type anything was logged as, and what they bill and cost in
// the requested currency — the buckets' currency rule, applied the same way:
// an amount counts only when its own currency is the one asked for, and work
// in another currency contributes its hours and nothing else. Each type's
// amounts are added up exactly and rounded once. By id: a type's name is
// projects', which names the rows it renders.
func (a *actuals) workTypes(ctx context.Context, req contracts.ActualsRequest) ([]contracts.WorkTypeActuals, error) {
	rows, err := a.q.ProjectWorkTypeActualGroups(ctx, req.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("time: read the project's hours per work type: %w", err)
	}
	type workTypeSum struct {
		hundredths int64
		bill, cost big.Rat
	}
	sums := map[int32]*workTypeSum{}
	var ids []int32
	for _, row := range rows {
		sum := sums[row.WorkTypeID]
		if sum == nil {
			sum = &workTypeSum{}
			sums[row.WorkTypeID] = sum
			ids = append(ids, row.WorkTypeID)
		}
		sum.hundredths += row.HoursHundredths
		if req.Currency == nil {
			continue
		}
		if row.BillCurrency != nil && *row.BillCurrency == *req.Currency {
			amount, err := exactAmount(row.BillAmount)
			if err != nil {
				return nil, err
			}
			sum.bill.Add(&sum.bill, amount)
		}
		if row.CostCurrency != nil && *row.CostCurrency == *req.Currency {
			amount, err := exactAmount(row.CostAmount)
			if err != nil {
				return nil, err
			}
			sum.cost.Add(&sum.cost, amount)
		}
	}

	out := make([]contracts.WorkTypeActuals, 0, len(ids))
	for _, id := range ids {
		sum := sums[id]
		out = append(out, contracts.WorkTypeActuals{
			WorkTypeID:      id,
			HoursHundredths: sum.hundredths,
			BillAmount:      amountText(&sum.bill),
			CostAmount:      amountText(&sum.cost),
		})
	}
	slices.SortFunc(out, func(x, y contracts.WorkTypeActuals) int { return cmp.Compare(x.WorkTypeID, y.WorkTypeID) })
	return out, nil
}
```

- [ ] **Step 7: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/time internal/contracts internal/db
test -z "$(mise exec -- gofmt -l internal)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/time/... ./internal/projects/... ./internal/openapi/... ./internal/db/... ./internal/integration/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git diff --stat -- openapi/COVERAGE.md
```
Expected: PASS — every existing time test unchanged (ordinary hours multiply by 100 %), the integration figures unchanged, the time coverage gate green. `COVERAGE.md` may not move (no new time operation); commit it only if it did.

Prove the tests can fail, restoring after each: change `* 0.01` to `* 0.02` in `ProjectActualGroups`' bill line (regenerate) — the actuals and effective-rate tests go red; multiply the rate before storing it (`BillRate: numericFromFloatPtr(ptr(multiplied(...)))` in the create) — the stored-rates assertion and `749.99` go red with `750.00`; delete the `!wt.Active` case — the retired refusal goes red; in `workTypes`, count a bill amount whatever its currency (`if row.BillCurrency != nil {`) — the SEK hour's 150.00 lands in overtime's NOK value (2850.00) and the per-type case goes red; remove `WorkTypeId` from `requestFromUpdate` — the re-snapshot test goes red; remove the `billing.MultiplierPercent` assignment — the shaping test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-wt-2.txt <<'EOF'
feat(time): an entry snapshots its work type and every amount multiplies where it is summed

TimeEntryRequest and TimeEntryUpdateRequest take an optional
workTypeId. At every save while draft or rejected Time reads the type
through the project directory before its transaction opens, refuses one
not on the entry's project or no longer active, and snapshots its id,
name and two multipliers beside the rates — frozen from submit, with the
stored bill and cost rates still the base rates the chain resolved. The
entry answers workType and, inside the shaped blocks, multiplierPercent
and a display-only effectiveRate. Actuals and the project summary sum
hours × rate × multiplier exactly and round once (333.33 × 1.5 h at
150 % is 749.99), and ProjectActualsEntry gains WorkTypes: hours and
value per type, on the buckets' currency rule.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/db/migrations/00032_time_work_types.sql apps/server/internal/time/sqlc.yaml \
 apps/server/internal/db/schema_test.go apps/server/internal/time/queries/entries.sql \
 apps/server/internal/time/queries/actuals.sql apps/server/internal/time/queries/stats.sql \
 apps/server/internal/time/store/models.go apps/server/internal/time/store/entries.sql.go \
 apps/server/internal/time/store/actuals.sql.go apps/server/internal/time/store/stats.sql.go \
 apps/server/internal/time/store/approvals.sql.go apps/server/internal/time/store/weeks.sql.go \
 openapi/time.yaml apps/server/internal/openapi/specs/time.yaml apps/server/internal/time/gen/api.gen.go \
 apps/time/frontend/src/api-schema.d.ts apps/server/internal/contracts/actuals.go \
 apps/server/internal/time/values.go apps/server/internal/time/entries.go apps/server/internal/time/rates.go \
 apps/server/internal/time/responses.go apps/server/internal/time/actuals.go \
 apps/server/internal/time/harness_test.go apps/server/internal/time/actuals_test.go \
 apps/server/internal/time/work_types_test.go"
git status --short   # add openapi/COVERAGE.md to PATHS if it moved, and every file listed under apps/server/internal/time/store/ that PATHS does not name yet
git add $PATHS && git commit -F /tmp/claude-1000/msg-wt-2.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 3: The economy reports hours and value per work type (D4)

`GET /projects/{id}/economy` gains `workTypes: [{id, name, hours, billAmount?, costAmount?}]` from `ProjectActualsEntry.WorkTypes` — hours for everyone who sees the project, `billAmount` with financial rights and a currency, `costAmount` with `projects:view-costs` on top, the block absent without time tracking and empty when no entry picked a type. Each row is **named from Projects' own `work_types` table** by id (the contract carries no name — controller ruling on the pre-flight review), so a renamed type reads by its new name at once, and an id the project does not know is skipped. Nothing else in the economy moves: budget used, the margin and the portfolio already read the multiplied buckets.

**Files:**
- Create: `apps/server/internal/projects/economy_work_types_test.go`
- Modify: `openapi/projects.yaml`, `apps/server/internal/projects/economy.go`, `apps/server/internal/projects/harness_test.go`, `apps/server/internal/projects/economy_expenses_test.go` (the golden test learns the one new allowed field)
- Generated (commit them): `apps/server/internal/openapi/specs/projects.yaml`, `apps/server/internal/projects/gen/api.gen.go`, `apps/projects/frontend/src/api-schema.d.ts`
- Read first (do not change): `projects/economy.go:64-270` (the handler, `economyResponse`, `seesAmounts`), `projects/authorize.go:115-125` (`canSeeCosts`), `projects/economy_math.go:279,568-590` (`exactHours`, `exactAmount`, `decimalNumber`), `projects/harness_test.go:370-500,1215-1320` (`fakeActuals`, `economyJSON`, `rawEconomy`), `projects/economy_expenses_test.go:45-80` (the golden body and its one allowed new field), `projects/work_types_test.go` (Task 1's `createWorkType`, `changeWorkType`, `workTypeBody`, `workTypeJSON`)

**Interfaces:**
- Consumes: `contracts.WorkTypeActuals{WorkTypeID, HoursHundredths, BillAmount, CostAmount}`, `ProjectActualsEntry.WorkTypes` (Task 2); `store.ListWorkTypes`, `store.ProjectsWorkType` (Task 1).
- Produces Go: `economyWorkTypes(logged []contracts.WorkTypeActuals, known []store.ProjectsWorkType, seesAmounts, seesCosts bool) (*[]gen.ProjectEconomyWorkType, error)`; `economyResponse` gains a last `workTypes []store.ProjectsWorkType` parameter; `(*fakeActuals).setWorkTypes(projectID int32, types ...contracts.WorkTypeActuals)`.
- Wire: new schema `ProjectEconomyWorkType {id, name, hours, billAmount?, costAmount?}`; `ProjectEconomyResponse.workTypes?` (optional — never added to `required:`); rows by name without regard to case, then id.

- [ ] **Step 1: The contract**

In `openapi/projects.yaml`, replace

```yaml
        ProjectFinancials:
```

with

```yaml
        ProjectEconomyWorkType:
            description: "What was logged as one of the project's work types (work types design D4), every bucket together. The amounts are the work's value and cost at the base rates times the type's multipliers, as Time snapshotted them; they are already inside every total of this response, so this is a split, never an addition."
            properties:
                billAmount:
                    description: What the type's hours bill at, in the response's currency. Absent without financial rights on the project, and when the project carries no currency.
                    format: double
                    type: number
                costAmount:
                    description: What the type's hours cost the company. Absent unless billAmount is present and the caller also holds projects:view-costs.
                    format: double
                    type: number
                hours:
                    description: Every hour logged as the type, whatever currency it was priced in. Planning data, visible to everyone who sees the project.
                    format: double
                    type: number
                id:
                    format: int32
                    type: integer
                name:
                    description: The type's name as the project's own work types list has it now — a renamed type reads by its new name.
                    type: string
            required:
                - id
                - name
                - hours
            type: object
        ProjectFinancials:
```

and replace

```yaml
                    type: boolean
            required:
                - timeTracking
                - expenseTracking
                - budget
                - lines
                - overBudget
            type: object
        ProjectEconomyRow:
```

with

```yaml
                    type: boolean
                workTypes:
                    description: One row per work type at least one entry was logged as, by name as the project's work types list has it; ordinary hours are in no row. Absent exactly when timeTracking is false, and an empty list when no entry picked a type.
                    items:
                        $ref: '#/components/schemas/ProjectEconomyWorkType'
                    type: array
            required:
                - timeTracking
                - expenseTracking
                - budget
                - lines
                - overBudget
            type: object
        ProjectEconomyRow:
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Expected: `gen.ProjectEconomyWorkType{Id int32; Name string; Hours float64; BillAmount, CostAmount *float64}` and `gen.ProjectEconomyResponse.WorkTypes *[]ProjectEconomyWorkType` (the `OtherCurrencies` shape); the build passes.

- [ ] **Step 2: The fake, the decoder, the golden test, and the failing tests**

In `apps/server/internal/projects/harness_test.go`, directly after `fakeActuals.set`'s closing brace, add:

```go
// setWorkTypes says what was logged per work type on a project whose totals
// set has already given — the provider's per-type split (work types design
// D4): ids and figures, by id, as the contract promises. The names are this
// module's own, read from projects.work_types when the economy renders them.
func (f *fakeActuals) setWorkTypes(projectID int32, types ...contracts.WorkTypeActuals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.entries[projectID]
	entry.WorkTypes = types
	f.entries[projectID] = entry
}
```

and add to `economyJSON`, after the `Cost *economyCostJSON` field, `WorkTypes []economyWorkTypeJSON \`json:"workTypes"\`` (the `gofmt -w` in Step 4 realigns the struct).

In `apps/server/internal/projects/economy_expenses_test.go`, `TestGetProjectEconomy_WithoutExpensesTheAnswerIsUnchanged`, replace

```go
	delete(raw, "expenseTracking")

	var golden map[string]any
```

with

```go
	delete(raw, "expenseTracking")
	// workTypes (work types design D4) is the second field allowed to be new:
	// with time tracking on and no entry logged as a type it is an empty
	// list, and the golden body predates it.
	if list, ok := raw["workTypes"].([]any); !ok || len(list) != 0 {
		t.Fatalf("workTypes = %v, want an empty list: the fixture logs no work type", raw["workTypes"])
	}
	delete(raw, "workTypes")

	var golden map[string]any
```

and in the comment above `goldenEconomyBody`, replace ``with `expenseTracking` — the one field that is allowed to be new — removed.`` with ``with `expenseTracking` and `workTypes` — the two fields that are allowed to be new — removed.``

Create `apps/server/internal/projects/economy_work_types_test.go`:

```go
package projects_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The economy's work-type split (work types design D4): what the provider
// reports per type, named from this module's own work types and shaped by the
// rules every other figure here follows — hours for everyone who sees the
// project, the value with financial rights and a currency, the cost with
// projects:view-costs on top, absent without time tracking.

// economyWorkTypeJSON decodes ProjectEconomyWorkType. The amounts are
// pointers because absence is the shaping; the assertions whose subject is
// the absence read the raw map instead.
type economyWorkTypeJSON struct {
	Id         int32    `json:"id"`
	Name       string   `json:"name"`
	Hours      float64  `json:"hours"`
	BillAmount *float64 `json:"billAmount"`
	CostAmount *float64 `json:"costAmount"`
}

// workTypeRows is the raw workTypes array, failing the test when there is none.
func workTypeRows(t *testing.T, raw map[string]any) []map[string]any {
	t.Helper()
	list, ok := raw["workTypes"].([]any)
	if !ok {
		t.Fatalf("workTypes = %v, want an array", raw["workTypes"])
	}
	rows := make([]map[string]any, 0, len(list))
	for _, item := range list {
		row, _ := item.(map[string]any)
		rows = append(rows, row)
	}
	return rows
}

// twoTypesLogged gives the project two work types through the real API — so
// the economy has names to read — and has the fake provider report work on
// both, by id as the contract orders them: overtime first, then the weekend.
func twoTypesLogged(t *testing.T, c *modtest.Client, actuals *fakeActuals, projectID int32) (overtime, weekend workTypeJSON) {
	t.Helper()
	overtime = createWorkType(t, c, projectID, nil)
	weekend = createWorkType(t, c, projectID, map[string]any{"name": "Helg", "billMultiplierPercent": 200})
	actuals.set(projectID, loggedTotals(loggedBucket(5.5, "8775.00", "3200.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	actuals.setWorkTypes(projectID,
		contracts.WorkTypeActuals{WorkTypeID: overtime.Id, HoursHundredths: 250, BillAmount: "3375.00", CostAmount: "1400.00"},
		contracts.WorkTypeActuals{WorkTypeID: weekend.Id, HoursHundredths: 300, BillAmount: "5400.00", CostAmount: "1800.00"},
	)
	return overtime, weekend
}

// A member sees each type's name and hours and no amount; the manager
// (financial rights, a currency) the value too; a manager with
// projects:view-costs the cost as well. Rows come by name.
func TestGetProjectsByIdEconomy_WorkTypes_AreShapedPerCaller(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "WTE1000", "currency": "NOK"})
	overtime, weekend := twoTypesLogged(t, manager, actuals, project.Id)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	costs, costsID := signIn(t, h, "projects:view-costs")
	addRole(t, h, project.Id, costsID, "manager")

	for _, row := range workTypeRows(t, rawEconomy(t, member, project.Id)) {
		for _, key := range []string{"billAmount", "costAmount"} {
			if _, ok := row[key]; ok {
				t.Errorf("member's row %v carries %s, want hours alone", row, key)
			}
		}
	}
	memberView := getEconomy(t, member, project.Id)
	if len(memberView.WorkTypes) != 2 ||
		memberView.WorkTypes[0].Id != weekend.Id || memberView.WorkTypes[0].Name != "Helg" || memberView.WorkTypes[0].Hours != 3 ||
		memberView.WorkTypes[1].Id != overtime.Id || memberView.WorkTypes[1].Name != "Overtid 50 %" || memberView.WorkTypes[1].Hours != 2.5 {
		t.Errorf("member's work types = %+v, want Helg 3 h then Overtid 50 %% 2.5 h", memberView.WorkTypes)
	}

	managerView := getEconomy(t, manager, project.Id)
	if managerView.WorkTypes[0].BillAmount == nil || *managerView.WorkTypes[0].BillAmount != 5400 || managerView.WorkTypes[0].CostAmount != nil {
		t.Errorf("manager's Helg = %+v, want the value and no cost", managerView.WorkTypes[0])
	}

	costView := getEconomy(t, costs, project.Id)
	if costView.WorkTypes[1].BillAmount == nil || *costView.WorkTypes[1].BillAmount != 3375 ||
		costView.WorkTypes[1].CostAmount == nil || *costView.WorkTypes[1].CostAmount != 1400 {
		t.Errorf("view-costs' Overtid = %+v, want 3375 and 1400", costView.WorkTypes[1])
	}
}

// The names are this module's own (the controller's ruling on D4): a type
// renamed on the Billing tab reads by its new name at once, a deactivated one
// keeps its row, and an id the project does not know is left out rather than
// shown nameless.
func TestGetProjectsByIdEconomy_WorkTypes_AreNamedFromTheProjectsOwnTypes(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "WTE1004", "currency": "NOK"})
	overtime, weekend := twoTypesLogged(t, manager, actuals, project.Id)
	changeWorkType(t, manager, project.Id, overtime.Id, workTypeBody(map[string]any{"name": "Overtid 100 %"}))
	changeWorkType(t, manager, project.Id, weekend.Id, workTypeBody(map[string]any{"name": "Helg", "billMultiplierPercent": 200, "active": false}))
	actuals.setWorkTypes(project.Id,
		contracts.WorkTypeActuals{WorkTypeID: overtime.Id, HoursHundredths: 250, BillAmount: "3375.00", CostAmount: "1400.00"},
		contracts.WorkTypeActuals{WorkTypeID: weekend.Id, HoursHundredths: 300, BillAmount: "5400.00", CostAmount: "1800.00"},
		contracts.WorkTypeActuals{WorkTypeID: 999999, HoursHundredths: 100, BillAmount: "900.00", CostAmount: "0.00"},
	)

	got := getEconomy(t, manager, project.Id).WorkTypes
	if len(got) != 2 || got[0].Name != "Helg" || got[1].Name != "Overtid 100 %" {
		t.Errorf("work types = %+v, want Helg (deactivated, still named) and the new name Overtid 100 %%, and no unknown id", got)
	}
}

// A project with no currency is an hours-only answer for everybody, cost
// rights or not — an amount in no currency is a number nobody can read.
func TestGetProjectsByIdEconomy_WorkTypes_HoursOnlyWithoutACurrency(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create", "projects:view-costs")
	project := createProject(t, manager, map[string]any{"code": "WTE1001"})
	twoTypesLogged(t, manager, actuals, project.Id)

	rows := workTypeRows(t, rawEconomy(t, manager, project.Id))
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want both types", rows)
	}
	for _, row := range rows {
		if _, ok := row["billAmount"]; ok {
			t.Errorf("row %v carries billAmount on a project with no currency", row)
		}
		if _, ok := row["costAmount"]; ok {
			t.Errorf("row %v carries costAmount on a project with no currency", row)
		}
	}
}

// Without time tracking there is no split to report — absent, not empty; with
// it and no entry logged as a type, the split is an empty list.
func TestGetProjectsByIdEconomy_WorkTypes_AbsentWithoutTimeTrackingEmptyWithNone(t *testing.T) {
	t.Parallel()
	off := newHarness(t)
	offManager, _ := signIn(t, off, "projects:create")
	offProject := createProject(t, offManager, map[string]any{"code": "WTE1002", "currency": "NOK"})
	if raw := rawEconomy(t, offManager, offProject.Id); raw["workTypes"] != nil {
		t.Errorf("workTypes = %v without time tracking, want the key absent", raw["workTypes"])
	}

	actuals := newFakeActuals()
	on := newHarnessWithActuals(t, actuals)
	onManager, _ := signIn(t, on, "projects:create")
	onProject := createProject(t, onManager, map[string]any{"code": "WTE1003", "currency": "NOK"})
	actuals.set(onProject.Id, loggedTotals(loggedBucket(2, "1800.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	raw := rawEconomy(t, onManager, onProject.Id)
	if list, ok := raw["workTypes"].([]any); !ok || len(list) != 0 {
		t.Errorf("workTypes = %v, want an empty list when no entry picked a type", raw["workTypes"])
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'WorkTypes|TestGetProjectEconomy_WithoutExpensesTheAnswerIsUnchanged' ./internal/projects/
```
Expected: FAIL — `workTypes = <nil>, want an array` (the block is never set), and the golden test's `want an empty list`.

- [ ] **Step 3: Implement**

In `apps/server/internal/projects/economy.go`, add `"cmp"`, `"slices"` and `"strings"` to the imports.

In `GetProjectsByIdEconomy`, replace

```go
	estimate, err := q.ProjectTaskEstimateHours(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: sum the project's task estimates: %w", err)
	}
```

with

```go
	estimate, err := q.ProjectTaskEstimateHours(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: sum the project's task estimates: %w", err)
	}
	// The work-type split is named from this module's own table (work types
	// design D4): the provider reports ids and figures, and the name a type
	// has is Projects' to say — a rename reads at once, whatever the entries
	// snapshotted. It is only needed when there is a provider to split.
	var workTypes []store.ProjectsWorkType
	if s.deps.Actuals != nil {
		if workTypes, err = q.ListWorkTypes(ctx, project.ID); err != nil {
			return nil, fmt.Errorf("projects: list the project's work types: %w", err)
		}
	}
```

replace

```go
	resp, err := s.economyResponse(ctx, project, a, lines, milestones, estimate, logged, spent)
```

with

```go
	resp, err := s.economyResponse(ctx, project, a, lines, milestones, estimate, logged, spent, workTypes)
```

replace

```go
	logged *contracts.ProjectActualsEntry,
	spent *contracts.ProjectExpenseTotals,
) (gen.ProjectEconomyResponse, error) {
```

with

```go
	logged *contracts.ProjectActualsEntry,
	spent *contracts.ProjectExpenseTotals,
	workTypes []store.ProjectsWorkType,
) (gen.ProjectEconomyResponse, error) {
```

and replace

```go
	resp.Lines, err = economyLines(lines, logged.Lines, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	return resp, nil
}
```

with

```go
	resp.Lines, err = economyLines(lines, logged.Lines, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	resp.WorkTypes, err = economyWorkTypes(logged.WorkTypes, workTypes, seesAmounts, a.canSeeCosts() && project.Currency != nil)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	return resp, nil
}

// economyWorkTypes is the work-type split (work types design D4) as this
// caller may see it: every type's hours — planning data, the way the buckets'
// hours are — its value with seesAmounts, its cost with the cost rights on
// top. Each row is named from known, the project's own work types; a type the
// provider reports that the project does not have (which should never happen)
// is left out rather than shown without a name. The provider's figures are
// already multiplied and already inside every total above, so nothing here
// adds them to anything; it renders them. It is only reached with time
// tracking on, and answers an empty list, never nil, when no entry picked a
// type: "none" and "cannot say" are different answers. Rows come by name
// without regard to case, then id — the Billing tab's reading order.
func economyWorkTypes(logged []contracts.WorkTypeActuals, known []store.ProjectsWorkType, seesAmounts, seesCosts bool) (*[]gen.ProjectEconomyWorkType, error) {
	names := make(map[int32]string, len(known))
	for _, wt := range known {
		names[wt.ID] = wt.Name
	}
	out := make([]gen.ProjectEconomyWorkType, 0, len(logged))
	for _, wt := range logged {
		name, ok := names[wt.WorkTypeID]
		if !ok {
			continue
		}
		row := gen.ProjectEconomyWorkType{
			Id:    wt.WorkTypeID,
			Name:  name,
			Hours: decimalNumber(exactHours(wt.HoursHundredths)),
		}
		if seesAmounts {
			bill, err := exactAmount(wt.BillAmount)
			if err != nil {
				return nil, err
			}
			value := decimalNumber(bill)
			row.BillAmount = &value
		}
		if seesCosts {
			cost, err := exactAmount(wt.CostAmount)
			if err != nil {
				return nil, err
			}
			value := decimalNumber(cost)
			row.CostAmount = &value
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y gen.ProjectEconomyWorkType) int {
		return cmp.Or(strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)), cmp.Compare(x.Id, y.Id))
	})
	return &out, nil
}
```

and add to the file's opening comment, at the end of the "**Shaping by absence.**" paragraph: `The work-type split follows the same three levels — hours, value, cost — and is named from this module's own work types, not by the provider (work types design D4).`

- [ ] **Step 4: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
# The new WorkTypes field realigns economyJSON in harness_test.go; the hook refuses unformatted files.
mise exec -- gofmt -w internal/projects
test -z "$(mise exec -- gofmt -l internal)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/projects/... ./internal/openapi/... ./internal/integration/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: PASS — the golden test with its second allowed field, every other economy test unchanged (their fake actuals carry no work types, so `workTypes` is `[]`, which nothing else there reads).

Prove the tests can fail, restoring after each: pass `true` for `seesAmounts` in the call — the member's row carries `billAmount`, red; pass `seesAmounts` for `seesCosts` — the manager's Helg carries a cost, red; drop the `if !ok { continue }` — the unknown id shows as a nameless row and the naming test goes red; return `nil` for an empty list — the empty-list and golden tests go red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-wt-3.txt <<'EOF'
feat(projects): a project's economy splits its logged work by work type

GET /projects/{id}/economy gains workTypes: one row per work type an
entry was logged as, named from the project's own work types, with its
hours for everyone who sees the project, its value with financial
rights and a currency, and its cost with projects:view-costs on top —
absent without time tracking, an empty list when no entry picked a
type. The figures come multiplied from Time's actuals; budget used, the
margin and the portfolio are unchanged.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="openapi/projects.yaml apps/server/internal/openapi/specs/projects.yaml apps/server/internal/projects/gen/api.gen.go \
 apps/projects/frontend/src/api-schema.d.ts apps/server/internal/projects/economy.go \
 apps/server/internal/projects/harness_test.go apps/server/internal/projects/economy_work_types_test.go \
 apps/server/internal/projects/economy_expenses_test.go"
git status --short
git add $PATHS && git commit -F /tmp/claude-1000/msg-wt-3.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 4: Projects and Time composed for real (the integration test)

Each module's suite hands the other an imitation. This pins the one promise the delivery makes across the seam: a work type created through the real projects module is picked on a real time entry, and the real economy reads the multiplied value back.

**Files:**
- Create: `apps/server/internal/integration/work_types_test.go`
- Read first (do not change): `integration/harness_test.go` (`newInstallation`, `signInAdmin`, `okJSON`, the path constants, `fakeCustomers`, `customerKraftVerket`), `integration/installations_test.go:138-180` (`projectResponse`), `integration/rates_test.go` (a time entry priced across the seam)

**Interfaces:**
- Consumes: Tasks 1–3's wire.
- Produces: nothing.

- [ ] **Step 1: Write the test**

Create `apps/server/internal/integration/work_types_test.go`:

```go
package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkTypes_APickedTypeMultipliesTheProjectsEconomy is the work types
// design end to end (D1–D4): the real projects module stores a work type, the
// real time module reads it through Compose's project directory — not Time's
// own fake of it — and snapshots it onto an entry, and the real economy reads
// the multiplied value back through the real actuals provider, naming the row
// from projects' own work types (a rename reads at once). The project's
// default bill rate prices the hours and the person's card costs them, so the
// arithmetic is one line per figure:
//
//	value: 2 h × 1000 × 150 % + 1 h × 1000 = 4000
//	cost:  2 h ×  600 × 120 % + 1 h ×  600 = 2040
func TestWorkTypes_APickedTypeMultipliesTheProjectsEconomy(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modProjects, modTime)
	admin, adminID := signInAdmin(t, h)
	rates, _ := h.SignInUser(t, "time:access", "time:manage")

	var project projectResponse
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code":            "KVEM2000",
		"name":            "Kraft-Verket overtid",
		"customerId":      customerKraftVerket,
		"billingType":     "time-and-materials",
		"currency":        "NOK",
		"defaultBillRate": 1000,
	}, &project)
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id),
		map[string]any{"status": "active"}, nil)
	okJSON(t, rates, http.MethodPost, "/api/v1/time/rates",
		map[string]any{"userId": adminID, "validFrom": "2026-01-01", "costRate": 600, "currency": "NOK"}, nil)

	var workType struct {
		Id   int32  `json:"id"`
		Name string `json:"name"`
	}
	okJSON(t, admin, http.MethodPost, fmt.Sprintf("%s/%d/work-types", projectsPath, project.Id),
		map[string]any{"name": "Overtid 50 %", "billMultiplierPercent": 150, "costMultiplierPercent": 120}, &workType)

	var entry struct {
		WorkType *struct {
			Id   int32  `json:"id"`
			Name string `json:"name"`
		} `json:"workType"`
		Billing *struct {
			BillRate          *float64 `json:"billRate"`
			MultiplierPercent *float64 `json:"multiplierPercent"`
			EffectiveRate     *float64 `json:"effectiveRate"`
		} `json:"billing"`
	}
	okJSON(t, admin, http.MethodPost, "/api/v1/time/entries", map[string]any{
		"projectId": project.Id, "entryDate": "2026-09-14", "hours": 2, "workTypeId": workType.Id,
	}, &entry)
	if entry.WorkType == nil || entry.WorkType.Id != workType.Id || entry.WorkType.Name != "Overtid 50 %" {
		t.Errorf("entry's work type = %+v, want the one projects stored", entry.WorkType)
	}
	b := entry.Billing
	if b == nil || b.BillRate == nil || *b.BillRate != 1000 || b.MultiplierPercent == nil || *b.MultiplierPercent != 150 ||
		b.EffectiveRate == nil || *b.EffectiveRate != 1500 {
		t.Errorf("billing = %+v, want the project's 1000 at 150 %%, 1500 an hour", b)
	}
	okJSON(t, admin, http.MethodPost, "/api/v1/time/entries",
		map[string]any{"projectId": project.Id, "entryDate": "2026-09-15", "hours": 1}, nil)

	var economy struct {
		Actuals *struct {
			TotalAmount *float64 `json:"totalAmount"`
		} `json:"actuals"`
		Cost *struct {
			Total float64 `json:"total"`
		} `json:"cost"`
		WorkTypes []struct {
			Id         int32    `json:"id"`
			Name       string   `json:"name"`
			Hours      float64  `json:"hours"`
			BillAmount *float64 `json:"billAmount"`
			CostAmount *float64 `json:"costAmount"`
		} `json:"workTypes"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &economy)
	if economy.Actuals == nil || economy.Actuals.TotalAmount == nil || *economy.Actuals.TotalAmount != 4000 {
		t.Errorf("actuals = %+v, want the value of work 4000", economy.Actuals)
	}
	if economy.Cost == nil || economy.Cost.Total != 2040 {
		t.Errorf("cost = %+v, want 2040", economy.Cost)
	}
	if len(economy.WorkTypes) != 1 {
		t.Fatalf("work types = %+v, want the one type, ordinary hours in none", economy.WorkTypes)
	}
	got := economy.WorkTypes[0]
	if got.Id != workType.Id || got.Name != "Overtid 50 %" || got.Hours != 2 ||
		got.BillAmount == nil || *got.BillAmount != 3000 || got.CostAmount == nil || *got.CostAmount != 1440 {
		t.Errorf("work type row = %+v, want 2 h worth 3000 costing 1440", got)
	}

	// The row is named from projects' own work types (work types design D4,
	// as ruled on the plan's review): a rename reads at once, though the
	// entry snapshotted the old name.
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/work-types/%d", projectsPath, project.Id, workType.Id),
		map[string]any{"name": "Overtid 100 %", "billMultiplierPercent": 150, "costMultiplierPercent": 120}, nil)
	okJSON(t, admin, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &economy)
	if len(economy.WorkTypes) != 1 || economy.WorkTypes[0].Name != "Overtid 100 %" {
		t.Errorf("work types after a rename = %+v, want the one row under its new name", economy.WorkTypes)
	}
}
```

- [ ] **Step 2: Run it, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/integration && test -z "$(mise exec -- gofmt -l internal)"
mise exec -- go test -count=1 -run 'TestWorkTypes_APickedTypeMultipliesTheProjectsEconomy' ./internal/integration/
mise exec -- go test -count=1 ./internal/integration/...
```
Expected: PASS. Prove it can fail: in `time/entries.go` pass `workTypeSnapshot{}` instead of `workType` to the insert — the economy's 4000 reads 3000 and the row is gone, red; restore. Then:

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-wt-4.txt <<'EOF'
test(integration): a work type made in projects multiplies a real time entry and reads back in the economy

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
git add apps/server/internal/integration/work_types_test.go
git commit -F /tmp/claude-1000/msg-wt-4.txt -- apps/server/internal/integration/work_types_test.go
git show --stat HEAD && git status --short
```

---

### Task 5: The docs (D6)

The four documents say what the code now does. Every sentence below is checked against Tasks 1–4's code before it is committed.

**Files:**
- Modify: `docs/projects.md`, `docs/time.md`, `docs/module-boundaries.md`, `ROADMAP.md`

**Interfaces:** none.

- [ ] **Step 1: `docs/projects.md`**

In the Domain model list, replace

```markdown
  [Billing lines and the optional Products dependency](#billing-lines-and-the-optional-products-dependency).
- **Billing milestone**
```

with

```markdown
  [Billing lines and the optional Products dependency](#billing-lines-and-the-optional-products-dependency).
- **Work type** — a name and two multipliers, `billMultiplierPercent` and
  `costMultiplierPercent`, defined once per project and applying to every billing line
  of it; a time entry may pick one, and Time multiplies its rates. Deactivated, never
  deleted — see [Work types](#work-types).
- **Billing milestone**
```

In the Timeline entry bullet, replace `` `milestone-invoice-undone`, `milestone-cancelled`, `milestone-reopened`. There are `` with `` `milestone-invoice-undone`, `milestone-cancelled`, `milestone-reopened`, `work-type-added`, `work-type-changed`. There are `` (if the line breaks fall differently, keep them and add the two types after `milestone-reopened`).

Directly before the line `## Project economy`, add:

```markdown
## Work types

Norwegian overtime is billed at 150 % and paid with an uplift, and until this
delivery the only way to say so was a duplicate billing line — "Consulting
(overtime)" at a separate product price — on every project, for every kind of work. A
**work type** ends that: a project defines its types once ("Overtid 50 %", "Overtid
100 %", "Helg"), a person picks one when logging time, and Time multiplies whatever
rates the chain resolved (see [the rate chain](time.md#the-rate-chain)).

- **A rule, not an amount.** A type is a `name` (1–100 characters, unique in the
  project without regard to case), a `billMultiplierPercent` and a
  `costMultiplierPercent` (each greater than zero, at most 1000, at most two decimals;
  100 is "as the rate says", 150 the classic overtime uplift) and `active`. Projects
  stores the percentages and **multiplies nothing**; every amount is computed in Time.
  For the same reason the multipliers are **visible to everyone who can see the
  project** — the reading that keeps `budgetHours` visible while `budgetAmount` is
  shaped away.
- **Per project, on every line.** A type is defined once per project and applies to
  every billing line and every step of the rate chain — a rule per line would recreate
  the duplication one level down. There is no installation-wide catalog; a shared
  vocabulary is what project templates (phase 4) are for.
- **No default, no delete.** "Ordinary hours" is the absence of a type, not a row.
  Entries snapshot a type's name, but a type that was ever picked stays readable, so a
  type is deactivated (`PUT … active: false`) — a deactivated type is offered for no
  new entry, and a draft that picked it is refused on its next save until it picks
  another.
- **The API.** `GET /projects/{id}/work-types` for anyone who sees the project, active
  types first and each half by name; `POST` and `PUT /projects/{id}/work-types/{workTypeId}`
  for a manager (`CanManage`: the manager role or `projects:manage-all`). A name the
  project already has, in any case, answers **409** with the title "Work type exists"
  — the unique index on `(project_id, lower(name))` decides it, not a read-then-write
  check.
- **The timeline.** `work-type-added` and `work-type-changed`, each carrying the type's
  id, its name and the fields that moved (`name`, `billMultiplierPercent`,
  `costMultiplierPercent`, `active`) — never a value, the billing lines' rule. A
  change that moved nothing writes nothing.
- **No lock.** A work type's write takes **no project-row lock**: the lock guards writes
  whose validity depends on the project's currency, fixed price or billing type (see
  [Locking](#locking)), and a percentage depends on none of them. Each write is one
  plain transaction.
- **Changing a multiplier rewrites no history.** Time freezes the multipliers with the
  rates when an entry is submitted; a type changed afterwards moves only drafts and
  rejected entries, on their next save.

The Billing tab shows a **Work types** card beside the billing lines — name, both
multipliers and status, with Add, edit and deactivate for a manager.

```

Directly before the line `### Shaping — who sees what`, add:

```markdown
### Hours by work type

`workTypes` splits the logged work by the [work type](#work-types) each entry was
logged as: `{id, name, hours, billAmount?, costAmount?}` per type, every bucket
together, by name — ordinary hours are in no row. Time reports ids and figures only;
Projects names each row from its own work types, so a renamed type reads by its new
name at once, whatever the entries snapshotted. The figures come from Time already
multiplied and are already inside every total above, so the list is a split, never an
addition: budget used, the margin and the portfolio read the same multiplied buckets
they always read. `hours` is for everyone who sees the project; `billAmount` needs
financial rights and a currency; `costAmount` needs `projects:view-costs` on top. The
block is absent when `timeTracking` is false and an empty list when no entry picked a
type. Hours priced in another currency than the project's count in `hours` and in no
amount. The Economy tab shows "Hours by work type" under the budget when the list is
not empty.

```

In the Contracts for other modules list, replace

```markdown
  `OpenTasksForUser` and `CanLogTime(projectID, userID)`. **Cancelled projects and
```

with

```markdown
  `OpenTasksForUser`, `CanLogTime(projectID, userID)`, and `WorkType(id)` /
  `WorkTypes(projectID)` — a project's [work types](#work-types) as `WorkTypeEntry` (id,
  project, name, both multipliers, active; `WorkType` answers an inactive type too,
  `WorkTypes` lists active first, by name). **Cancelled projects and
```

and replace

```markdown
  each counted only when logged in the currency Projects asked for. See
```

with

```markdown
  each counted only when logged in the currency Projects asked for, and `WorkTypes` —
  the same work split per work type, for the economy's `workTypes`. See
```

In "What Time tracking should build on", directly before `- Resolve amounts yourself, or leave it to invoicing.`, add:

```markdown
- Work types: `WorkType(id)` for the one an entry picked — check its `ProjectID` and
  `Active` yourself — and snapshot its name and multipliers with the rates, before the
  saving transaction opens. The multipliers are rules, not amounts: surface them to
  whoever sees the entry.
```

In the API table, directly after the `PUT /api/v1/projects/{id}/billing-lines/{lineId}` row, add:

```markdown
| `GET /api/v1/projects/{id}/work-types` | The project's work types, active first, by name, multipliers included. Anyone who sees the project |
| `POST /api/v1/projects/{id}/work-types` | Add a work type; a taken name (any case) answers 409. Manager only |
| `PUT /api/v1/projects/{id}/work-types/{workTypeId}` | Change a work type, `active` included; there is no delete. Manager only |
```

In "## Locking", at the end of its first paragraph (after `… so nothing can deadlock.`), add: `A work type's create and change take no project lock at all: nothing about a percentage depends on the project's currency, fixed price or billing type.`

- [ ] **Step 2: `docs/time.md`**

In the Domain model's Time entry bullet, replace `` `rateSource` that produced them, a `status`, a `rejectionReason`, the`` with `` `rateSource` that produced them, the picked work type's snapshot (`workTypeId`, its name, `billMultiplierPercent` and `costMultiplierPercent`; all empty for ordinary hours), a `status`, a `rejectionReason`, the``.

Directly after the paragraph that begins `` `rateSource` is one of `line`, `project`, `customer`, `person` or `none` `` (and before `## The state machine`), add:

```markdown
### The work type's multiplier

An entry may pick one of its project's [work types](projects.md#work-types) —
`workTypeId` on the create and the update, checked at every save while `draft` or
`rejected`: the type must be one of the entry's project's (else 400 on `workTypeId`,
"Work type is not on this project") and active ("Work type is no longer active"). The
type is read through `contracts.ProjectDirectory.WorkType` with the project and the
line, **before** the saving transaction opens, like every other directory read.

The multiplier is **the last step, applied to whatever the chain resolved**: a
billing line's rule, the project's default, the customer's or the person's — the type
is orthogonal to which step won, and `rateSource` still names the step. It is **kept
beside the rates, never baked into them**: `billRate` and `costRate` stay the base
rates the chain resolved, and the entry snapshots the type's name,
`billMultiplierPercent` and `costMultiplierPercent` next to them — frozen from
`submitted` on with the rates, taken again when a rejected entry is saved. A
non-billable entry keeps its cost multiplier: overtime costs the company whether or
not it bills. Picking none is ordinary hours; an update is a full replace, so leaving
`workTypeId` out makes the entry ordinary hours again.

The entry answers `workType: {id, name}` to whoever sees it, and inside the shaped
blocks `billing.multiplierPercent` + `billing.effectiveRate` and
`cost.multiplierPercent` + `cost.effectiveRate` — absent for ordinary hours, and the
effective rate absent when the block has no rate. The effective rate is
**display only**: rate × multiplier, half up to cents. Amounts are multiplied where
they are summed — hours × base rate × multiplier, exact, rounded once — so 1.5 hours
at 333.33 and 150 % is worth 749.99, not 1.5 × 500.00.
```

Replace the section `## What invoicing will read`'s first bullet

```markdown
- **Approved, billable entries with a bill rate** are the invoiceable set: `hours ×
  billRate` in `billCurrency`, grouped by project and by the **trackable code**
  `<project>-<line>` the billing line gives them.
```

with

```markdown
- **Approved, billable entries with a bill rate** are the invoiceable set: `hours ×
  billRate × billMultiplierPercent / 100` (100 % for ordinary hours) in
  `billCurrency`, multiplied exactly and rounded once per invoice line, grouped by
  project and by the **trackable code** `<project>-<line>` the billing line gives
  them — and, within a line, by work type, since an overtime hour is billed at its
  own price.
```

In "## What Time reports to other modules", directly before the bullet that begins `- **No authorization, ever.**`, add:

```markdown
- **Every amount is multiplied where it is summed.** Each entry's bill and cost amount
  is its hours × its base rate × its work type's multiplier (100 % for none), summed
  exactly and rounded once — in the buckets, per line, and in the project summary's
  billed amount alike.
- **The work per work type.** `ProjectActualsEntry.WorkTypes` is one entry per work
  type anything was logged as — `WorkTypeID`, `HoursHundredths`, `BillAmount`,
  `CostAmount` — every bucket together, by id. No name: a type is Projects', which
  names the rows it renders. Ordinary hours are in none. The amounts follow the
  currency rule above; work in another currency counts in the hours and in no amount.
  `ActualsForProjects` does not carry it.
```

- [ ] **Step 3: `docs/module-boundaries.md` and `ROADMAP.md`**

In `docs/module-boundaries.md`, replace

```markdown
Time consumes four contracts and provides one:
`contracts.ProjectDirectory` (required — hence the config check),
```

with

```markdown
Time consumes four contracts and provides one:
`contracts.ProjectDirectory` (required — hence the config check — including
`WorkType`/`WorkTypes`, a project's work types, which the rate chain's last step
multiplies by and which are read before a save's transaction opens like every other
directory read),
```

In `ROADMAP.md`, replace the heading `### Phase 3 — Budgets, billing milestones and costs (first delivery done)` with `### Phase 3 — Budgets, billing milestones and costs (two deliveries done)`, and replace the paragraph

```markdown
**Next: supplier costs, overtime and work-type multipliers.** Expenses landed
separately — see [Expenses phase 3](#phase-3--expenses-on-the-project-page-done)
— and the rest were out of scope for this delivery and stay the concrete next
steps for project economics. Forecast / estimate-to-complete and
original-vs-revised budgets (tracking a budget's own history rather than
only its current value) are candidates worth deciding on once those land,
not committed work yet.
```

with

```markdown
**Work types and overtime multipliers (done).** Decided in
`docs/superpowers/specs/2026-09-25-project-work-types-design.md`. A project
defines its work types once — a name, a bill multiplier and a cost multiplier,
as percentages of the rate — and they apply to every billing line of it; a
person picks one when logging time. Time multiplies whatever rate the chain
resolved, keeps the base rates and snapshots the multipliers beside them,
freezes them on submit, and multiplies where it sums, exactly; the actuals
contract gains the work per type (ids and figures; Projects names the rows),
and the Economy tab shows "Hours by work type". The Norwegian overtime case no longer needs a duplicate billing line.
See [`docs/projects.md`](docs/projects.md#work-types) and
[`docs/time.md`](docs/time.md#the-work-types-multiplier).

*Unblocks:* overtime billed and costed at its own rate on every project,
without a line per kind of work.

**Next: supplier invoices.** Expenses landed separately — see
[Expenses phase 3](#phase-3--expenses-on-the-project-page-done) — and a
supplier invoice (a company-paid expense kind with supplier, invoice number
and due date, never in a claim, split from expenses in the project's Costs
section) is the next delivery, its own spec. Forecast /
estimate-to-complete and original-vs-revised budgets (tracking a budget's
own history rather than only its current value) are candidates worth
deciding on once that lands, not committed work yet.
```

- [ ] **Step 4: Check the docs against the code, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
grep -n 'work-type-added\|work-type-changed' apps/server/internal/projects/timeline.go docs/projects.md
grep -n 'Work type exists\|Work type is not on this project\|Work type is no longer active' apps/server/internal/projects/errors.go apps/server/internal/time/values.go docs/projects.md docs/time.md
grep -n 'COALESCE(bill_multiplier_percent, 100) \* 0.01' apps/server/internal/time/queries/actuals.sql apps/server/internal/time/queries/stats.sql
grep -n '(#work-types)\|(#hours-by-work-type)\|#the-work-types-multiplier' docs/projects.md docs/time.md ROADMAP.md
grep -n -A6 'type WorkTypeActuals struct' apps/server/internal/contracts/actuals.go   # no Name field: the docs must not promise one
```
Every string the docs quote must be found in the code; every anchor must resolve to a heading (`## Work types` → `#work-types`, `### The work type's multiplier` → `#the-work-types-multiplier`). Then:

```bash
cat > /tmp/claude-1000/msg-wt-5.txt <<'EOF'
docs: work types, their multipliers, and what Time reports per type

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="docs/projects.md docs/time.md docs/module-boundaries.md ROADMAP.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-wt-5.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 6: The Projects frontend — the Work types card, its form, the timeline, the economy table (D5)

May run beside Tasks 4–5 once Tasks 1 and 3 have regenerated `apps/projects/frontend/src/api-schema.d.ts`.

**Files:**
- Create: `apps/projects/frontend/src/api/work-types.ts`, `apps/projects/frontend/src/api/work-types.test.ts`, `apps/projects/frontend/src/pages/-work-type-form-modal.tsx`, `apps/projects/frontend/src/pages/-work-type-form-modal.test.tsx`
- Modify: `apps/projects/frontend/src/api/projects.ts`, `apps/projects/frontend/src/api/economy.ts`, `apps/projects/frontend/src/pages/project-billing.tsx`, `apps/projects/frontend/src/pages/project-billing.test.tsx`, `apps/projects/frontend/src/pages/project-economy.tsx`, `apps/projects/frontend/src/pages/project-economy.test.tsx`, `apps/projects/frontend/src/pages/-project-timeline.tsx`, `apps/projects/frontend/src/pages/-project-timeline.test.tsx`, `apps/projects/frontend/src/i18n.ts`
- Read first (do not change): `pages/project-billing.tsx` (the card shape), `pages/-billing-line-form-modal.tsx` (the modal shape, the 409 and `ApiValidationError` handling), `pages/project-billing.test.tsx:1-70` (`project`, `line`, `stubBilling`), `pages/-billing-line-form-modal.test.tsx:1-100`, `pages/project-economy.tsx:110-305` (`BudgetSection`), `pages/project-economy.test.tsx:1-270` (`economy()`, `asTheServerWouldSend`, `stubEconomy`, `money`), `lib/economy.ts:149-180` (`useEconomyFormat`), `pages/-project-timeline.tsx:30-125`

**Interfaces:**
- Consumes: the generated `WorkTypeResponse`, `WorkTypeRequest`, `ProjectEconomyWorkType`, `ProjectEconomyResponse.workTypes` (Tasks 1, 3).
- Produces TS: `WorkType`, `WorkTypeInput` (api/projects.ts); `workTypesQueryOptions(id)` (key `["projects", "detail", id, "work-types"]`), `createWorkType`, `updateWorkType` (api/work-types.ts); `EconomyWorkType` (api/economy.ts); `WorkTypeFormModal`, `WorkTypeModalState` (pages/-work-type-form-modal.tsx); `WorkTypesCard` and `WorkTypeTable` (file-local).

- [ ] **Step 1: The api layer and its test**

In `apps/projects/frontend/src/api/projects.ts`, directly after the line `export type BillingLineInput = Omit<Schemas["BillingLineRequest"], "pricingMode"> & { pricingMode: PricingMode };`, add:

```ts

/**
 * One of the project's work types (work types design D1): a name and two
 * multipliers, percentages of the rate. A rule, not an amount — the API
 * answers it to everyone who sees the project, though this package shows it
 * only on the Billing tab, behind financial rights. Every field is required
 * on the wire, so nothing needs normalising here.
 */
export type WorkType = Schemas["WorkTypeResponse"];

/** A work type as it should stand. `active` is the one field a PUT may leave out; a POST never sends it. */
export type WorkTypeInput = Schemas["WorkTypeRequest"];
```

In `apps/projects/frontend/src/api/economy.ts`, directly after `export type EconomyLine = Schemas["ProjectEconomyLine"];`, add:

```ts
/**
 * One work type's share of the logged work (work types design D4): hours for
 * everyone, `billAmount` with the project's money, `costAmount` with costs on
 * top — absent, never zero, when the caller may not see them. The list itself
 * is absent without time tracking.
 */
export type EconomyWorkType = Schemas["ProjectEconomyWorkType"];
```

Create `apps/projects/frontend/src/api/work-types.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import type { WorkType, WorkTypeInput } from "./projects";
import { request } from "./request";

export type { WorkType, WorkTypeInput } from "./projects";

/**
 * Every work type of the project, deactivated ones included, the active ones
 * first and each half by name — the order the server answers in. Under the
 * `["projects"]` root, so every write's blanket invalidation reaches it.
 */
export const workTypesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["projects", "detail", id, "work-types"],
    queryFn: ({ signal }) => request<WorkType[]>(`/api/v1/projects/${id}/work-types`, { signal }),
  });

const json = (method: string, input: WorkTypeInput): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(input),
});

/** A name the project already has, in any case, answers 409. */
export const createWorkType = (id: number, input: WorkTypeInput): Promise<WorkType> =>
  request<WorkType>(`/api/v1/projects/${id}/work-types`, json("POST", input));

/** There is no delete: `active: false` retires a type. A taken name answers 409 here too. */
export const updateWorkType = (id: number, workTypeId: number, input: WorkTypeInput): Promise<WorkType> =>
  request<WorkType>(`/api/v1/projects/${id}/work-types/${workTypeId}`, json("PUT", input));
```

Create `apps/projects/frontend/src/api/work-types.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { createWorkType, updateWorkType, type WorkType, workTypesQueryOptions } from "./work-types";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

/** Literally the body the server sends: every field of WorkTypeResponse is required. */
const overtime: WorkType = {
  id: 11,
  projectId: 7,
  name: "Overtid 50 %",
  billMultiplierPercent: 150,
  costMultiplierPercent: 140,
  active: true,
  createdAt: "2026-09-01T08:00:00Z",
  updatedAt: "2026-09-01T08:00:00Z",
};

describe("workTypesQueryOptions", () => {
  it("reads the project's work types under the projects root", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [overtime])));

    const options = workTypesQueryOptions(7);
    expect(await runQuery(options)).toEqual([overtime]);
    expect(options.queryKey).toEqual(["projects", "detail", 7, "work-types"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/work-types", { signal: undefined });
  });
});

describe("createWorkType and updateWorkType", () => {
  it("POSTs a new type and PUTs a change, active included", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, overtime)));
    const input = { name: "Overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 140 };

    await createWorkType(7, input);
    await updateWorkType(7, 11, { ...input, active: false });

    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/7/work-types", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/7/work-types/11", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...input, active: false }),
    });
  });

  it("surfaces the 409 a taken name answers", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(409, { title: "Work type exists", status: 409 })));

    const error = await createWorkType(7, { name: "overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 150 }).catch(
      (e: unknown) => e as { status?: number },
    );

    expect(error).toMatchObject({ status: 409 });
  });
});
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/api/work-types.test.ts
```
Expected: PASS (the api layer is thin; its test pins the URL, the key and the bodies).

- [ ] **Step 2: The strings**

In `apps/projects/frontend/src/i18n.ts`, replace

```ts
    project: "Project",
  },
  nb: {
```

with

```ts
    project: "Project",

    workTypes: "Work types",
    workTypesDescription:
      "Overtime and other kinds of hours, each multiplying the rate a time entry would otherwise bill and cost at.",
    addWorkType: "Add work type",
    editWorkType: "Edit work type",
    editWorkTypeFor: "Edit work type {{name}}",
    workTypeName: "Name",
    billMultiplier: "Bill multiplier",
    costMultiplier: "Cost multiplier",
    multiplierHelp: "150 % bills and costs one and a half times the rate; 100 % is the rate as it stands.",
    multiplierValue: "{{percent}} %",
    noWorkTypes: "No work types yet. Hours logged here bill and cost at the plain rate.",
    failedToLoadWorkTypes: "Could not load the work types",
    workTypeSaved: "Work type saved",
    couldNotSaveWorkType: "Could not save the work type",
    workTypeNameRequired: "Give the work type a name",
    workTypeNameTooLong: "A work type name is at most 100 characters",
    multiplierOutOfRange: "More than 0 % and at most 1000 %",
    workTypeNameTaken: "This project already has a work type with that name.",
    timelineWorkTypeAdded: "Work type {{name}} was added.",
    timelineWorkTypeChanged: "Work type {{name}} changed: {{fields}}.",
    hoursByWorkType: "Hours by work type",
    workTypeColumn: "Work type",
    workTypeHoursColumn: "Hours",
    workTypeValueColumn: "Value",
    workTypeCostColumn: "Cost",
  },
  nb: {
```

and replace

```ts
    project: "Prosjekt",
  },
} as const satisfies CatalogResources;
```

with

```ts
    project: "Prosjekt",

    workTypes: "Arbeidstyper",
    workTypesDescription:
      "Overtid og andre slags timer, som hver ganger opp satsen en timeføring ellers ville fakturert og kostet.",
    addWorkType: "Legg til arbeidstype",
    editWorkType: "Rediger arbeidstype",
    editWorkTypeFor: "Rediger arbeidstypen {{name}}",
    workTypeName: "Navn",
    billMultiplier: "Faktureringsfaktor",
    costMultiplier: "Kostnadsfaktor",
    multiplierHelp: "150 % fakturerer og koster halvannen gang satsen; 100 % er satsen slik den er.",
    multiplierValue: "{{percent}} %",
    noWorkTypes: "Ingen arbeidstyper ennå. Timer ført her faktureres og koster etter vanlig sats.",
    failedToLoadWorkTypes: "Kunne ikke laste arbeidstypene",
    workTypeSaved: "Arbeidstypen er lagret",
    couldNotSaveWorkType: "Kunne ikke lagre arbeidstypen",
    workTypeNameRequired: "Gi arbeidstypen et navn",
    workTypeNameTooLong: "Et navn på en arbeidstype kan ha høyst 100 tegn",
    multiplierOutOfRange: "Mer enn 0 % og høyst 1000 %",
    workTypeNameTaken: "Prosjektet har allerede en arbeidstype med det navnet.",
    timelineWorkTypeAdded: "Arbeidstypen {{name}} ble lagt til.",
    timelineWorkTypeChanged: "Arbeidstypen {{name}} ble endret: {{fields}}.",
    hoursByWorkType: "Timer per arbeidstype",
    workTypeColumn: "Arbeidstype",
    workTypeHoursColumn: "Timer",
    workTypeValueColumn: "Verdi",
    workTypeCostColumn: "Kostnad",
  },
} as const satisfies CatalogResources;
```

- [ ] **Step 3: The form modal — failing test first**

Create `apps/projects/frontend/src/pages/-work-type-form-modal.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkType } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { WorkTypeFormModal, type WorkTypeModalState } from "./-work-type-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Literally what the server sends for a type. */
const overtime: WorkType = {
  id: 11,
  projectId: 7,
  name: "Overtid 50 %",
  billMultiplierPercent: 150,
  costMultiplierPercent: 140,
  active: true,
  createdAt: "2026-09-01T08:00:00Z",
  updatedAt: "2026-09-01T08:00:00Z",
};

const stubWrites = (status = 200) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname.startsWith("/api/v1/projects/7/work-types") && init?.method) {
      return Promise.resolve(
        status === 200 ? jsonResponse(200, overtime) : jsonResponse(status, { title: "Work type exists", status }),
      );
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderModal = (state: WorkTypeModalState | null) => {
  const onClose = vi.fn();
  renderWithProviders(<WorkTypeFormModal projectId={7} state={state} onClose={onClose} />);
  return { onClose };
};

/** The body of the one write with this method — never "the last fetch". */
const written = (fetchMock: ReturnType<typeof stubFetch>, method: string) => {
  const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === method) ?? [];
  return { url: url === undefined ? undefined : String(url), body: init?.body ? JSON.parse(String(init.body)) : undefined };
};

const setNumber = async (label: string, value: string) => {
  const input = screen.getByRole("textbox", { name: label });
  await userEvent.clear(input);
  await userEvent.type(input, value);
};

describe("WorkTypeFormModal", () => {
  it("adds a type with its name trimmed and both multipliers, explaining what a percentage does", async () => {
    const fetchMock = stubWrites();
    const { onClose } = renderModal({ mode: "create" });

    expect(screen.getByText("150 % bills and costs one and a half times the rate; 100 % is the rate as it stands.")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "  Overtid 50 %  ");
    await setNumber("Bill multiplier", "150");
    await setNumber("Cost multiplier", "125");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(written(fetchMock, "POST")).toEqual({
      url: "/api/v1/projects/7/work-types",
      body: { name: "Overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 125 },
    });
  });

  it("shows a taken name on the name field", async () => {
    stubWrites(409);
    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "overtid 50 %");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("This project already has a work type with that name.")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("deactivates a type through its Active switch, sending the rest as it stands", async () => {
    const fetchMock = stubWrites();
    renderModal({ mode: "edit", workType: overtime });

    const dialog = screen.getByRole("dialog", { name: "Edit work type" });
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("Overtid 50 %");
    await userEvent.click(within(dialog).getByRole("switch", { name: "Active" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save changes" }));

    await waitFor(() =>
      expect(written(fetchMock, "PUT")).toEqual({
        url: "/api/v1/projects/7/work-types/11",
        body: { name: "Overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 140, active: false },
      }),
    );
  });

  it("refuses a blank name and a multiplier of nothing without asking the server", async () => {
    const fetchMock = stubWrites();
    renderModal({ mode: "create" });

    await setNumber("Bill multiplier", "0");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("Give the work type a name")).toBeInTheDocument();
    expect(screen.getByText("More than 0 % and at most 1000 %")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });
});
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/pages/-work-type-form-modal.test.tsx
```
Expected: FAIL — `Failed to resolve import "./-work-type-form-modal"`.

Create `apps/projects/frontend/src/pages/-work-type-form-modal.tsx`:

```tsx
import { Button, Group, Modal, NumberInput, Stack, Switch, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ApiError, ApiValidationError } from "../api/request";
import { createWorkType, updateWorkType, type WorkType, type WorkTypeInput } from "../api/work-types";
import "../i18n";

/** Adding a type, or editing the one the caller just read off the card. */
export type WorkTypeModalState = { mode: "create" } | { mode: "edit"; workType: WorkType };

/** The server's rules (work types design D1), mirrored so a typo is caught before a round trip. */
const MAX_NAME = 100;
const MAX_MULTIPLIER = 1000;

interface WorkTypeFormValues {
  name: string;
  billMultiplierPercent: number | string;
  costMultiplierPercent: number | string;
  active: boolean;
}

const percent = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

export interface WorkTypeFormModalProps {
  projectId: number;
  state: WorkTypeModalState | null;
  onClose: () => void;
}

/**
 * Adds or edits one work type (work types design D5). The form is mounted
 * fresh every time the modal opens, so no previous type's values survive a
 * close. The multipliers are entered as percentages — 150, not 1.5 — the way
 * the helper line says and the API stores them.
 */
export const WorkTypeFormModal = ({ projectId, state, onClose }: WorkTypeFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editWorkType") : t("addWorkType")}
      centered
    >
      {state && <WorkTypeForm projectId={projectId} state={state} onClose={onClose} />}
    </Modal>
  );
};

const WorkTypeForm = ({ projectId, state, onClose }: WorkTypeFormModalProps & { state: WorkTypeModalState }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const workType = state.mode === "edit" ? state.workType : undefined;
  const multiplier = (value: number | string) => {
    const parsed = percent(value);
    return parsed === undefined || Number.isNaN(parsed) || parsed <= 0 || parsed > MAX_MULTIPLIER
      ? t("multiplierOutOfRange")
      : null;
  };

  const form = useForm<WorkTypeFormValues>({
    initialValues: {
      name: workType?.name ?? "",
      // 100 % is "as the rate says": a new type multiplies nothing until the
      // manager says by how much.
      billMultiplierPercent: workType?.billMultiplierPercent ?? 100,
      costMultiplierPercent: workType?.costMultiplierPercent ?? 100,
      active: workType?.active ?? true,
    },
    validate: {
      name: (value) => {
        const name = value.trim();
        if (!name) return t("workTypeNameRequired");
        return name.length > MAX_NAME ? t("workTypeNameTooLong") : null;
      },
      billMultiplierPercent: multiplier,
      costMultiplierPercent: multiplier,
    },
  });

  const mutation = useMutation({
    mutationFn: (values: WorkTypeFormValues) => {
      const input: WorkTypeInput = {
        name: values.name.trim(),
        billMultiplierPercent: percent(values.billMultiplierPercent) ?? 0,
        costMultiplierPercent: percent(values.costMultiplierPercent) ?? 0,
        // `active` is the one field a PUT may leave out; creating never sends it.
        ...(workType ? { active: values.active } : {}),
      };
      return workType ? updateWorkType(projectId, workType.id, input) : createWorkType(projectId, input);
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("workTypeSaved"), message: saved.name });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      // The two writes answer 409 for one reason only: the project already
      // has a type of that name (the server's "Work type exists").
      if ((error as ApiError).status === 409) {
        form.setFieldError("name", t("workTypeNameTaken"));
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveWorkType"), message: error.message });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        <TextInput label={t("workTypeName")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <Group grow align="start">
          <NumberInput
            label={t("billMultiplier")}
            suffix=" %"
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("billMultiplierPercent")}
          />
          <NumberInput
            label={t("costMultiplier")}
            suffix=" %"
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("costMultiplierPercent")}
          />
        </Group>
        <Text size="xs" c="dimmed">
          {t("multiplierHelp")}
        </Text>
        {workType && (
          <Switch
            label={t("active")}
            checked={form.values.active}
            onChange={(event) => form.setFieldValue("active", event.currentTarget.checked)}
          />
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {workType ? t("saveChanges") : t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/pages/-work-type-form-modal.test.tsx
```
Expected: PASS. (If Mantine names the NumberInput's role differently in this version, query it with `screen.getByLabelText("Bill multiplier")` instead and say so.)

- [ ] **Step 4: The card on the Billing tab — failing test first**

In `apps/projects/frontend/src/pages/project-billing.test.tsx`, change the import `import type { BillingLine, Project } from "../api/projects";` to `import type { BillingLine, Project, WorkType } from "../api/projects";`, replace `stubBilling` with

```tsx
/** Literally what the server sends for a work type. */
const workType = (overrides: Partial<WorkType> = {}): WorkType => ({
  id: 11,
  projectId: 7,
  name: "Overtid 50 %",
  billMultiplierPercent: 150,
  costMultiplierPercent: 140,
  active: true,
  createdAt: "2026-09-01T08:00:00Z",
  updatedAt: "2026-09-01T08:00:00Z",
  ...overrides,
});

const stubBilling = (row: Project, lines: BillingLine[] = [line()], workTypes: WorkType[] = []) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, row));
    if (url.pathname === "/api/v1/projects/7/billing-lines") return Promise.resolve(jsonResponse(200, lines));
    if (url.pathname === "/api/v1/projects/7/work-types") return Promise.resolve(jsonResponse(200, workTypes));
    if (url.pathname === "/api/v1/products") return Promise.resolve(jsonResponse(200, { data: [] }));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
```

and add, before the final `});` of the `describe("ProjectBilling", …)` block:

```tsx
  describe("the Work types card", () => {
    const types = [workType(), workType({ id: 12, name: "Gammel overtid", costMultiplierPercent: 150, active: false })];

    it("lists each type's name, both multipliers and whether it is active", async () => {
      stubBilling(project(), [line()], types);
      renderWithProviders(<ProjectBilling projectId={7} />);

      const table = await screen.findByRole("table", { name: "Work types" });
      const overtime = within(table).getByText("Overtid 50 %").closest("tr") as HTMLElement;
      expect(overtime).toHaveTextContent("150 %");
      expect(overtime).toHaveTextContent("140 %");
      expect(within(overtime).getByText("Active")).toBeInTheDocument();
      const retired = within(table).getByText("Gammel overtid").closest("tr") as HTMLElement;
      expect(within(retired).getByText("Inactive")).toBeInTheDocument();
    });

    it("offers a manager Add and a named edit button, opening the form", async () => {
      stubBilling(project(), [line()], types);
      renderWithProviders(<ProjectBilling projectId={7} />);

      await userEvent.click(await screen.findByRole("button", { name: "Edit work type Overtid 50 %" }));
      const dialog = await screen.findByRole("dialog", { name: "Edit work type" });
      expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("Overtid 50 %");
      await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

      await userEvent.click(screen.getByRole("button", { name: "Add work type" }));
      expect(await screen.findByRole("dialog", { name: "Add work type" })).toBeInTheDocument();
    });

    it("offers no buttons to someone who sees the amounts but may not manage the project", async () => {
      stubBilling(
        project({
          capabilities: {
            canManage: false,
            canContribute: false,
            canSeeFinancials: true,
            canManageMilestones: false,
            canSeeCosts: false,
          },
        }),
        [line()],
        types,
      );
      renderWithProviders(<ProjectBilling projectId={7} />);

      await screen.findByRole("table", { name: "Work types" });
      expect(screen.queryByRole("button", { name: "Add work type" })).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Edit work type Overtid 50 %" })).not.toBeInTheDocument();
    });

    it("says so when the project has none, even with the products module off", async () => {
      stubBilling(project({ billingLinesAvailable: false }));
      renderWithProviders(<ProjectBilling projectId={7} />);

      expect(
        await screen.findByText("No work types yet. Hours logged here bill and cost at the plain rate."),
      ).toBeInTheDocument();
    });
  });
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/pages/project-billing.test.tsx
```
Expected: FAIL — `Unable to find role="table" and name "Work types"`; every existing case still passes.

In `apps/projects/frontend/src/pages/project-billing.tsx`, add the imports

```tsx
import { workTypesQueryOptions } from "../api/work-types";
import { WorkTypeFormModal, type WorkTypeModalState } from "./-work-type-form-modal";
```

(after `import { BillingLineFormModal, type BillingLineModalState } from "./-billing-line-form-modal";`; biome orders them), replace

```tsx
      {project.billingLinesAvailable ? (
        <BillingLinesCard projectId={projectId} project={project} />
      ) : (
        <Text size="sm" c="dimmed">
          {t("billingLinesUnavailable")}
        </Text>
      )}
    </Stack>
```

with

```tsx
      {project.billingLinesAvailable ? (
        <BillingLinesCard projectId={projectId} project={project} />
      ) : (
        <Text size="sm" c="dimmed">
          {t("billingLinesUnavailable")}
        </Text>
      )}
      {/* Work types need no product: they multiply whatever rate an entry
          resolves, so the card stands whether or not products is enabled. */}
      <WorkTypesCard projectId={projectId} canManage={project.capabilities.canManage} />
    </Stack>
```

and directly before `const LineRow = ({` add:

```tsx
/**
 * The project's work types (work types design D5): each a name and two
 * multipliers, applying to every billing line of the project. A manager adds,
 * edits and deactivates them — there is no delete, because entries that
 * picked a type still name it. The card sits on the Billing tab, which is
 * behind financial rights (D5 accepts that); a member without them reads the
 * multipliers only through the API — Time's entry form — not here.
 */
const WorkTypesCard = ({ projectId, canManage }: { projectId: number; canManage: boolean }) => {
  const { t, formatters } = useI18n("projects");
  const { data: workTypes, isPending, isError, error } = useQuery(workTypesQueryOptions(projectId));
  const [modalState, setModalState] = useState<WorkTypeModalState | null>(null);
  const headingId = useId();
  const multiplier = (value: number) => t("multiplierValue", { percent: formatters.formatNumber(value) });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <Stack gap={2}>
            <Text fw={600} component="h3" id={headingId}>
              {t("workTypes")}
            </Text>
            <Text size="sm" c="dimmed">
              {t("workTypesDescription")}
            </Text>
          </Stack>
          {canManage && (
            <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setModalState({ mode: "create" })}>
              {t("addWorkType")}
            </Button>
          )}
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadWorkTypes")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={2} rowHeight={40} />}
        {workTypes && workTypes.length === 0 && <EmptyState title={t("noWorkTypes")} size="sm" />}

        {workTypes && workTypes.length > 0 && (
          <Table.ScrollContainer minWidth={520}>
            <Table striped highlightOnHover aria-labelledby={headingId}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("workTypeName")}</Table.Th>
                  <Table.Th>{t("billMultiplier")}</Table.Th>
                  <Table.Th>{t("costMultiplier")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  {canManage && <Table.Th>{t("actions")}</Table.Th>}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {workTypes.map((workType) => (
                  <Table.Tr key={workType.id}>
                    <Table.Td>
                      <Text size="sm" fw={600}>
                        {workType.name}
                      </Text>
                    </Table.Td>
                    <Table.Td>{multiplier(workType.billMultiplierPercent)}</Table.Td>
                    <Table.Td>{multiplier(workType.costMultiplierPercent)}</Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={workType.active ? "teal" : "gray"}>
                        {workType.active ? t("active") : t("inactive")}
                      </Badge>
                    </Table.Td>
                    {canManage && (
                      <Table.Td>
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("editWorkTypeFor", { name: workType.name })}
                          onClick={() => setModalState({ mode: "edit", workType })}
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>

      <WorkTypeFormModal projectId={projectId} state={modalState} onClose={() => setModalState(null)} />
    </Card>
  );
};
```

Update the page's doc comment's first sentence to `The Billing tab (design §8.2): the financial summary, the billing lines and the project's work types (work types design D5).`

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/pages/project-billing.test.tsx
```
Expected: PASS.

- [ ] **Step 5: The timeline's two sentences — failing test first**

In `apps/projects/frontend/src/pages/-project-timeline.test.tsx`, add inside `describe("ProjectTimeline", …)`:

```tsx
  it("names a work type and the fields a change of it moved, never its percentages", async () => {
    stubTimeline([
      // Literally what the server writes: an added type names all three fields.
      entry({
        id: 21,
        eventType: "work-type-added",
        payload: { workTypeId: 11, name: "Overtid 50 %", fields: ["name", "billMultiplierPercent", "costMultiplierPercent"] },
      }),
      entry({
        id: 22,
        eventType: "work-type-changed",
        payload: { workTypeId: 11, name: "Overtid 50 %", fields: ["costMultiplierPercent", "active"] },
      }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Work type Overtid 50 % was added.")).toBeInTheDocument();
    expect(screen.getByText("Work type Overtid 50 % changed: Cost multiplier, Active.")).toBeInTheDocument();
  });
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/pages/-project-timeline.test.tsx
```
Expected: FAIL — the raw `work-type-added` is shown.

In `apps/projects/frontend/src/pages/-project-timeline.tsx`, add to `fieldKeys`, after `percent: "percent",`:

```ts
  billMultiplierPercent: "billMultiplier",
  costMultiplierPercent: "costMultiplier",
  active: "active",
```

and directly before `    default:` in `describeEntry` add:

```tsx
    // The work-type events name the type and the fields a change moved,
    // never a percentage (work types design D1).
    case "work-type-added":
      return t("timelineWorkTypeAdded", { name: text(payload.name) });
    case "work-type-changed":
      return t("timelineWorkTypeChanged", { name: text(payload.name), fields: fieldList(t, payload.fields) });
```

- [ ] **Step 6: The economy's "Hours by work type" — failing test first**

In `apps/projects/frontend/src/pages/project-economy.test.tsx`, at the top of `asTheServerWouldSend`'s body (directly after `const block = body.expenses;`), add:

```ts
  // The work-type split (work types design D4): rows only with time
  // tracking, a value only in a currency, a cost only beside a value.
  if (!body.timeTracking && (body.workTypes?.length ?? 0) > 0) {
    throw new Error("Without timeTracking there is no work type split");
  }
  if (body.workTypes?.some((row) => row.billAmount != null) && !body.currency) {
    throw new Error("A work type's value needs the project's currency");
  }
  if (body.workTypes?.some((row) => row.costAmount != null && row.billAmount == null)) {
    throw new Error("A work type's cost comes only beside its value");
  }
```

and append, at the end of the file:

```tsx
describe("Hours by work type", () => {
  const types = [
    { id: 11, name: "Helg", hours: 3, billAmount: 5400, costAmount: 1800 },
    { id: 12, name: "Overtid 50 %", hours: 2.5, billAmount: 3375, costAmount: 1400 },
  ];

  it("lists each type's hours and value, and no cost for a caller without costs", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ workTypes: types.map(({ id, name, hours, billAmount }) => ({ id, name, hours, billAmount })) }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const table = await screen.findByRole("table", { name: "Hours by work type" });
    const weekend = within(table).getByText("Helg").closest("tr") as HTMLElement;
    expect(weekend).toHaveTextContent("3 h");
    expect(weekend).toHaveTextContent(money(5400));
    expect(within(table).getByRole("columnheader", { name: "Value" })).toBeInTheDocument();
    expect(within(table).queryByRole("columnheader", { name: "Cost" })).not.toBeInTheDocument();
  });

  it("adds the cost for a caller who may see costs", async () => {
    stubEconomy(project(), plan([milestone()]), 200, { economy: economy({ workTypes: types }) });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const table = await screen.findByRole("table", { name: "Hours by work type" });
    const overtime = within(table).getByText("Overtid 50 %").closest("tr") as HTMLElement;
    expect(overtime).toHaveTextContent("2.5 h");
    expect(overtime).toHaveTextContent(money(1400));
  });

  it("shows hours alone to a caller the answer gives no amounts", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ workTypes: types.map(({ id, name, hours }) => ({ id, name, hours })) }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const table = await screen.findByRole("table", { name: "Hours by work type" });
    expect(within(table).queryByRole("columnheader", { name: "Value" })).not.toBeInTheDocument();
    expect(within(table).getByText("Helg").closest("tr")).toHaveTextContent("3 h");
  });

  it("shows nothing when no entry picked a type", async () => {
    stubEconomy(project(), plan([milestone()]), 200, { economy: economy({ workTypes: [] }) });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByTestId("budget-headline");
    expect(screen.queryByRole("table", { name: "Hours by work type" })).not.toBeInTheDocument();
  });
});
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/projects/frontend test -- src/pages/project-economy.test.tsx
```
Expected: FAIL — `Unable to find role="table" and name "Hours by work type"`; `TAB_WITHOUT_EXPENSES` and every other case still pass (their fixture carries no `workTypes`).

In `apps/projects/frontend/src/pages/project-economy.tsx`, replace

```tsx
                {economy.lines.map((line) => (
                  <EconomyLineRow key={line.billingLineId ?? "no-line"} line={line} currency={currency} />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </Card>
  );
};
```

with

```tsx
                {economy.lines.map((line) => (
                  <EconomyLineRow key={line.billingLineId ?? "no-line"} line={line} currency={currency} />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}

        <WorkTypeTable economy={economy} currency={currency} />
      </Stack>
    </Card>
  );
};

/**
 * "Hours by work type" (work types design D4): the hours of each type the
 * project's entries were logged as, and — when the answer carries them —
 * what they are worth and what they cost. Ordinary hours are the absence of
 * a type, so they are no row, and the figures are already inside every total
 * above, multiplied where Time summed them: this is a split, never a sum. A
 * column is drawn when the answer carries its figure, the way the cost panel
 * follows the cost block.
 */
const WorkTypeTable = ({ economy, currency }: { economy: Economy; currency?: string }) => {
  const { t } = useI18n("projects");
  const { hours, money } = useEconomyFormat(currency);
  const headingId = useId();
  const workTypes = economy.workTypes ?? [];
  if (workTypes.length === 0) return null;
  const showValue = workTypes.some((row) => row.billAmount != null);
  const showCost = workTypes.some((row) => row.costAmount != null);

  return (
    <Stack gap="xs">
      <Text fw={600} size="sm" id={headingId}>
        {t("hoursByWorkType")}
      </Text>
      <Table.ScrollContainer minWidth={480}>
        <Table striped aria-labelledby={headingId}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("workTypeColumn")}</Table.Th>
              <Table.Th>{t("workTypeHoursColumn")}</Table.Th>
              {showValue && <Table.Th>{t("workTypeValueColumn")}</Table.Th>}
              {showCost && <Table.Th>{t("workTypeCostColumn")}</Table.Th>}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {workTypes.map((row) => (
              <Table.Tr key={row.id}>
                <Table.Td>{row.name}</Table.Td>
                <Table.Td>{hours(row.hours)}</Table.Td>
                {showValue && <Table.Td>{money(row.billAmount)}</Table.Td>}
                {showCost && <Table.Td>{money(row.costAmount)}</Table.Td>}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Stack>
  );
};
```

- [ ] **Step 7: Run everything, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bunx biome check --write apps/projects/frontend/src
mise exec -- bun run --cwd apps/projects/frontend test
mise exec -- bun run --cwd apps/projects/frontend typecheck
mise exec -- bun run --cwd apps/projects/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```
Expected: PASS. Prove the tests can fail, restoring after each: drop the `canManage &&` before the Add button — the no-buttons case goes red; drop the 409 branch in the modal — the taken-name case goes red (a red notification instead of the field text); drop `showCost &&` from the header — the "no cost" case goes red; remove the `work-type-changed` case — the timeline test goes red. Say what each printed.

```bash
cat > /tmp/claude-1000/msg-wt-6.txt <<'EOF'
feat(projects-ui): the Billing tab keeps a project's work types, and the Economy tab splits the hours by them

A Work types card beside the billing lines lists each type's name, bill
and cost multiplier and status; a manager adds, edits and deactivates
them in a form that takes the multipliers as percentages and puts a
taken name on the name field. The timeline reads work-type-added and
work-type-changed, and the Economy tab shows "Hours by work type" under
the budget — hours, and value and cost when the answer carries them.
en + nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/projects/frontend/src/api/projects.ts apps/projects/frontend/src/api/economy.ts \
 apps/projects/frontend/src/api/work-types.ts apps/projects/frontend/src/api/work-types.test.ts \
 apps/projects/frontend/src/pages/-work-type-form-modal.tsx apps/projects/frontend/src/pages/-work-type-form-modal.test.tsx \
 apps/projects/frontend/src/pages/project-billing.tsx apps/projects/frontend/src/pages/project-billing.test.tsx \
 apps/projects/frontend/src/pages/project-economy.tsx apps/projects/frontend/src/pages/project-economy.test.tsx \
 apps/projects/frontend/src/pages/-project-timeline.tsx apps/projects/frontend/src/pages/-project-timeline.test.tsx \
 apps/projects/frontend/src/i18n.ts"
git status --short
git add $PATHS && git commit -F /tmp/claude-1000/msg-wt-6.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 7: The Time frontend — the Work type select, the badges, the rate line, the exact amount (D5)

May run beside Tasks 3–6 once Task 2 has regenerated `apps/time/frontend/src/api-schema.d.ts` (and Task 1 has shipped the endpoint the select reads).

**Files:**
- Create: `apps/time/frontend/src/lib/money.ts`, `apps/time/frontend/src/lib/money.test.ts`, `apps/time/frontend/src/components/work-type-badge.tsx`, `apps/time/frontend/src/components/rate-line.tsx`
- Modify: `apps/time/frontend/src/api/projects.ts`, `apps/time/frontend/src/api/projects.test.ts`, `apps/time/frontend/src/api/entries.ts`, `apps/time/frontend/src/api/entries.test.ts`, `apps/time/frontend/src/pages/-entry-form-modal.tsx`, `apps/time/frontend/src/pages/day.tsx`, `apps/time/frontend/src/pages/day.test.tsx`, `apps/time/frontend/src/pages/approvals.tsx`, `apps/time/frontend/src/pages/approvals.test.tsx`, `apps/time/frontend/src/pages/my-week.tsx`, `apps/time/frontend/src/pages/my-week.test.tsx`, `apps/time/frontend/src/test/server.ts`, `apps/time/frontend/src/test/fixtures.ts`, `apps/time/frontend/src/i18n.ts`
- Read first (do not change): `api/projects.ts` (Time reading Projects' API), `api/entries.ts:100-125` (`timeEntryUpdateFrom`), `pages/-entry-form-modal.tsx` whole, `pages/day.tsx:160-260` (`EntryCard`), `pages/approvals.tsx:294-376` (`EntryRow`), `pages/my-week.tsx:300-345` (`WeekRowView`), `test/server.ts`, `test/fixtures.ts:1-120`, `pages/day.test.tsx:1-100`, `pages/my-week.test.tsx:1-125`, `pages/approvals.test.tsx:1-40`

**Interfaces:**
- Consumes: `GET /api/v1/projects/{id}/work-types` (Task 1); the generated `TimeEntryResponse.workType`, `TimeEntryBilling.multiplierPercent`/`effectiveRate`, `TimeEntryRequest.workTypeId` (Task 2).
- Produces TS: `ProjectWorkType {id, name, active}`, `projectWorkTypesQueryOptions(projectId)` (key `["time", "options", "work-types", projectId]`, active types only); `billedAmount(rate, hours, multiplierPercent?)`; `WorkTypeBadge`, `RateLine`; `timeEntryUpdateFrom` carries `workTypeId`; `TimeServer.workTypes`, `kvemWorkTypes`.

- [ ] **Step 1: The api layer, the exact amount, and their tests — failing first**

Append to `apps/time/frontend/src/api/projects.test.ts`:

```ts
describe("projectWorkTypesQueryOptions", () => {
  it("reads the project's work types from the projects API and keeps the active ones", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        jsonResponse(200, [
          {
            id: 6001,
            projectId: 1001,
            name: "Overtid 50 %",
            billMultiplierPercent: 150,
            costMultiplierPercent: 140,
            active: true,
            createdAt: "2026-09-01T08:00:00Z",
            updatedAt: "2026-09-01T08:00:00Z",
          },
          {
            id: 6003,
            projectId: 1001,
            name: "Gammel overtid",
            billMultiplierPercent: 150,
            costMultiplierPercent: 150,
            active: false,
            createdAt: "2026-01-01T08:00:00Z",
            updatedAt: "2026-06-01T08:00:00Z",
          },
        ]),
      ),
    );

    const options = projectWorkTypesQueryOptions(1001);
    expect(await runQuery(options)).toEqual([{ id: 6001, name: "Overtid 50 %", active: true }]);
    expect(options.queryKey).toEqual(["time", "options", "work-types", 1001]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/1001/work-types");
  });
});
```

and add `projectWorkTypesQueryOptions` to its import from `./projects`.

In `apps/time/frontend/src/api/entries.test.ts`, inside `describe("timeEntryUpdateFrom", …)`, add:

```ts
  it("carries the work type along, so new hours from the grid keep an entry's overtime", () => {
    const overtime = entry({ workType: { id: 6001, name: "Overtid 50 %" } });

    expect(timeEntryUpdateFrom(overtime, { hours: 8 })).toMatchObject({ hours: 8, workTypeId: 6001 });
    expect(timeEntryUpdateFrom(entry(), { hours: 8 })).not.toHaveProperty("workTypeId");
  });
```

Create `apps/time/frontend/src/lib/money.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { billedAmount } from "./money";

describe("billedAmount", () => {
  it("bills hours at the rate times the multiplier, rounded once", () => {
    // 333.33 × 1.5 h × 150 % = 749.9925. Rounding the rate first
    // (499.995 → 500.00) would bill 750.00, which no other surface says.
    expect(billedAmount(333.33, 1.5, 150)).toBe(749.99);
  });

  it("is the plain rate over the hours without a work type", () => {
    expect(billedAmount(1200, 7.5)).toBe(9000);
    expect(billedAmount(1200, 7.5, null)).toBe(9000);
  });

  it("rounds a half cent up", () => {
    // 0.01 × 0.5 h = 0.005
    expect(billedAmount(0.01, 0.5)).toBe(0.01);
  });
});
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/time/frontend test -- src/api src/lib/money.test.ts
```
Expected: FAIL — `projectWorkTypesQueryOptions` is not exported, `./money` does not resolve, and `timeEntryUpdateFrom` drops `workTypeId`.

In `apps/time/frontend/src/api/projects.ts`, directly after the `MyTaskOption` interface, add:

```ts
/**
 * One of a project's work types as the entry form offers it (work types
 * design D5). The projects API answers the whole WorkTypeResponse; the form
 * needs the id, the name and whether it may still be picked.
 */
export interface ProjectWorkType {
  id: number;
  name: string;
  active: boolean;
}
```

and append:

```ts
/**
 * The project's work types an entry may pick: the active ones, in the
 * server's order (by name). Projects lists deactivated types too — entries
 * that picked one still name it — but a retired type is no choice for new
 * work, so it is dropped here. Anyone who can see the project may read them;
 * a multiplier is a rule, not an amount.
 */
export const projectWorkTypesQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["time", "options", "work-types", projectId],
    queryFn: async ({ signal }) => {
      const types = await request<ProjectWorkType[]>(`/api/v1/projects/${projectId}/work-types`, { signal });
      return types
        .filter((type) => type.active)
        .map(({ id, name, active }): ProjectWorkType => ({ id, name, active }));
    },
  });
```

In `apps/time/frontend/src/api/entries.ts`, add `export type TimeEntryWorkType = Schemas["TimeEntryWorkType"];` after `export type TimeEntryApprover = Schemas["TimeEntryApprover"];`, and in `timeEntryUpdateFrom` replace

```ts
  billable: entry.billable,
  revision: entry.revision,
  ...changes,
});
```

with

```ts
  billable: entry.billable,
  revision: entry.revision,
  // An update is a full replace: an entry logged as a work type has to say so
  // again, or the grid's new hours would make it ordinary hours.
  ...(entry.workType ? { workTypeId: entry.workType.id } : {}),
  ...changes,
});
```

Create `apps/time/frontend/src/lib/money.ts`:

```ts
/**
 * What an entry's hours bill at (work types design D3), exactly: hours ×
 * rate × multiplier, rounded half up to cents once — the server's own
 * arithmetic, so the approval queue says what the economy says. Floats never
 * multiply money here: each figure is taken to its hundredths (the scale the
 * server stores all three at) and the product is formed in BigInt.
 */
export const billedAmount = (rate: number, hours: number, multiplierPercent?: number | null): number => {
  const hundredths = (value: number): bigint => BigInt(Math.round(value * 100));
  // cents × hundredths of an hour × hundredths of a percent is the value
  // times 10^8; a cent is 10^6 of those.
  const product = hundredths(rate) * hundredths(hours) * hundredths(multiplierPercent ?? 100);
  const cents = (product + 500_000n) / 1_000_000n;
  return Number(cents) / 100;
};
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/time/frontend test -- src/api src/lib/money.test.ts
```
Expected: PASS.

- [ ] **Step 2: The fixtures, the stub, the strings, the two components**

In `apps/time/frontend/src/test/fixtures.ts`, directly after `kvemLines`, add:

```ts
/**
 * Kverneland's work types, literally as the projects API answers them — a
 * retired one included, which the entry form must not offer.
 */
export const kvemWorkTypes = [
  {
    id: 6001,
    projectId: 1001,
    name: "Overtid 50 %",
    billMultiplierPercent: 150,
    costMultiplierPercent: 140,
    active: true,
    createdAt: "2026-09-01T08:00:00Z",
    updatedAt: "2026-09-01T08:00:00Z",
  },
  {
    id: 6003,
    projectId: 1001,
    name: "Gammel overtid",
    billMultiplierPercent: 150,
    costMultiplierPercent: 150,
    active: false,
    createdAt: "2026-01-01T08:00:00Z",
    updatedAt: "2026-06-01T08:00:00Z",
  },
];
```

In `apps/time/frontend/src/test/server.ts`, add to `TimeServer`, after `lines?: …`:

```ts
  /** Each project's work types as the projects API answers them; a project not named has none. */
  workTypes?: Record<number, unknown[]>;
```

and directly before the final `    return Promise.resolve(new Response(null, { status: 404 }));` add:

```ts
    const types = /^\/api\/v1\/projects\/(\d+)\/work-types$/.exec(path);
    if (types) return Promise.resolve(jsonResponse(200, server.workTypes?.[Number(types[1])] ?? []));
```

In `apps/time/frontend/src/i18n.ts`, replace

```ts
    unpricedHours: "{{hours}} h without a rate",
  },
  nb: {
```

with

```ts
    unpricedHours: "{{hours}} h without a rate",
    workType: "Work type",
    ordinaryHours: "Ordinary hours",
    rateTimesMultiplier: "{{rate}} × {{percent}} % = {{effective}}",
  },
  nb: {
```

and replace

```ts
    unpricedHours: "{{hours}} t uten pris",
  },
} satisfies CatalogResources;
```

with

```ts
    unpricedHours: "{{hours}} t uten pris",
    workType: "Arbeidstype",
    ordinaryHours: "Ordinære timer",
    rateTimesMultiplier: "{{rate}} × {{percent}} % = {{effective}}",
  },
} satisfies CatalogResources;
```

Create `apps/time/frontend/src/components/work-type-badge.tsx`:

```tsx
import { Badge } from "@mantine/core";

/**
 * The work type an entry was logged as (work types design D5), shown after
 * the trackable code: a name, never the multiplier — that is money-adjacent
 * and lives in the billing block's rate line.
 */
export const WorkTypeBadge = ({ name }: { name: string }) => (
  <Badge variant="light" color="grape" size="sm" tt="none" data-testid="work-type-badge">
    {name}
  </Badge>
);
```

Create `apps/time/frontend/src/components/rate-line.tsx`:

```tsx
import { Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { TimeEntryBilling } from "../api/entries";
import "../i18n";

/**
 * "900 × 150 % = 1 350" (work types design D5): the base rate the chain
 * resolved, the work type's bill multiplier, and what an hour bills at. It is
 * drawn only from a billing block that carries all three — the block is there
 * exactly when the caller may see the rate, and the multiplier exactly when a
 * work type was picked — so an entry of ordinary hours shows nothing new.
 */
export const RateLine = ({ billing }: { billing?: TimeEntryBilling | null }) => {
  const { t, formatters } = useI18n("time");
  if (billing?.billRate == null || billing.multiplierPercent == null || billing.effectiveRate == null) return null;
  return (
    <Text size="xs" c="dimmed" data-testid="rate-line">
      {t("rateTimesMultiplier", {
        rate: formatters.formatNumber(billing.billRate),
        percent: formatters.formatNumber(billing.multiplierPercent),
        effective: formatters.formatNumber(billing.effectiveRate),
      })}
    </Text>
  );
};
```

- [ ] **Step 3: The entry form's select, the day view — failing tests first**

In `apps/time/frontend/src/pages/day.test.tsx`, change the fixtures import to `import { devTaskRow, entry, kvemWorkTypes, pmRow, week, weekRow } from "../test/fixtures";` and add inside `describe("DayPage", …)`:

```tsx
  it("offers the project's active work types, forgets the choice when the project changes, and sends it", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek(), workTypes: { 1001: kvemWorkTypes } });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Log time" });

    // No project, no choice; a project without work types, none either.
    expect(within(dialog).queryByRole("combobox", { name: "Work type" })).not.toBeInTheDocument();
    await choose(dialog, "Project", /INTERN/);
    expect(within(dialog).queryByRole("combobox", { name: "Work type" })).not.toBeInTheDocument();

    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.click(await within(dialog).findByRole("combobox", { name: "Work type" }));
    expect(await screen.findByRole("option", { name: "Overtid 50 %" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Gammel overtid" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "Overtid 50 %" }));
    expect(within(dialog).getByRole("combobox", { name: "Work type" })).toHaveValue("Overtid 50 %");

    await choose(dialog, "Project", /INTERN/);
    await choose(dialog, "Project", /KVEM1000/);
    const select = await within(dialog).findByRole("combobox", { name: "Work type" });
    expect(select).toHaveValue("");
    expect(select).toHaveAttribute("placeholder", "Ordinary hours");

    await choose(dialog, "Work type", "Overtid 50 %");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Hours" }), "2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toMatchObject({ projectId: 1001, workTypeId: 6001 }));
  });

  it("shows an entry's work type after its trackable code, its multiplied rate, and keeps the type on an edit", async () => {
    const overtime = entry({
      id: 610,
      entryDate: DAY,
      hours: 2,
      note: "Late deploy",
      workType: { id: 6001, name: "Overtid 50 %" },
      billing: { billRate: 900, currency: "NOK", multiplierPercent: 150, effectiveRate: 1350 },
    });
    const fetchMock = stubTimeApi({ week: week([weekRow(pmRow, [overtime])]), workTypes: { 1001: kvemWorkTypes } });
    renderRoute(`/time/day?date=${DAY}`);

    const card = (await screen.findByText("Late deploy")).closest("[data-entry]") as HTMLElement;
    expect(within(card).getByTestId("work-type-badge")).toHaveTextContent("Overtid 50 %");
    expect(within(card).getByTestId("rate-line")).toHaveTextContent("900 × 150 % = 1,350");

    await userEvent.click(within(card).getByRole("button", { name: "Edit the entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit time" });
    expect(await within(dialog).findByRole("combobox", { name: "Work type" })).toHaveValue("Overtid 50 %");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").body).toMatchObject({ workTypeId: 6001, revision: 2 }));
  });
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/time/frontend test -- src/pages/day.test.tsx
```
Expected: FAIL — no `Work type` combobox, no badge, no rate line; every existing case still passes.

In `apps/time/frontend/src/pages/-entry-form-modal.tsx`:

- extend the `../api/projects` import with `projectWorkTypesQueryOptions`;
- add `workTypeId: string | null;` to `EntryFormValues` after `taskId`;
- add `"workTypeId",` to `formFields` after `"taskId",`;
- in `initialValues`, add `workTypeId: editing.workType ? String(editing.workType.id) : null,` after the editing branch's `taskId` line, and `workTypeId: null,` after the create branch's `taskId: null,`;
- directly after `  const { data: tasks } = useQuery(myOpenTasksQueryOptions());` add

```tsx
  // The project's active work types (work types design D5). Only a project
  // that has one shows the select; ordinary hours are the empty choice.
  const { data: workTypes } = useQuery({
    ...projectWorkTypesQueryOptions(projectId ?? 0),
    enabled: projectId !== null,
  });
```

- directly after the `taskOptions` declaration add

```tsx
  // An entry keeps its own type even once it is retired, so the form shows
  // what it was logged as; the server refuses it on save until another is
  // picked, and that refusal lands on this field.
  const workTypeOptions = withCurrent(
    (workTypes ?? []).map((type) => ({ value: String(type.id), label: type.name })),
    sameProject && editing.workType
      ? { value: String(editing.workType.id), label: editing.workType.name }
      : null,
  );
```

- replace `    form.setValues({ projectId: value, billingLineId: null, taskId: null });` with `    form.setValues({ projectId: value, billingLineId: null, taskId: null, workTypeId: null });`
- in the mutation's `input` literal, directly after `note: values.note.trim() || null,` add `...(values.workTypeId === null ? {} : { workTypeId: Number(values.workTypeId) }),`
- directly after the `</Group>` that closes the line/task selects add

```tsx
        {workTypeOptions.length > 0 && (
          <Select
            label={t("workType")}
            placeholder={t("ordinaryHours")}
            clearable
            data={workTypeOptions}
            {...form.getInputProps("workTypeId")}
          />
        )}
```

- and add to the component's doc comment, after `a project with an optional line and task`, `, and a work type when the project has one`.

In `apps/time/frontend/src/pages/day.tsx`, add the imports `import { RateLine } from "../components/rate-line";` and `import { WorkTypeBadge } from "../components/work-type-badge";`, and in `EntryCard` replace

```tsx
          <Text fw={600} size="sm">
            {label}
          </Text>
          <Text size="xs" c="dimmed">
            {entry.projectName}
          </Text>
          {entry.startTime && entry.endTime && (
```

with

```tsx
          <Group gap={6} wrap="nowrap">
            <Text fw={600} size="sm">
              {label}
            </Text>
            {entry.workType && <WorkTypeBadge name={entry.workType.name} />}
          </Group>
          <Text size="xs" c="dimmed">
            {entry.projectName}
          </Text>
          <RateLine billing={entry.billing} />
          {entry.startTime && entry.endTime && (
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/time/frontend test -- src/pages/day.test.tsx
```
Expected: PASS.

- [ ] **Step 4: The approval queue and my week — failing tests first**

In `apps/time/frontend/src/pages/approvals.test.tsx`, change the fixtures import to `import { approvalGroups, ME, submittedEntry, WEEK } from "../test/fixtures";` and add inside `describe("ApprovalsPage", …)`:

```tsx
  it("shows an entry's work type and bills its hours at the multiplied rate, exactly", async () => {
    stubTimeApi({
      approvals: [
        {
          userId: ME,
          displayName: "Ada Lovelace",
          weekStart: WEEK,
          hours: 1.5,
          entries: [
            submittedEntry({
              id: 720,
              hours: 1.5,
              note: "Night shift",
              workType: { id: 6001, name: "Overtid 50 %" },
              billing: { billRate: 333.33, currency: "NOK", multiplierPercent: 150, effectiveRate: 500 },
            }),
          ],
        },
      ],
    });
    renderRoute("/time/approvals");

    const ada = await open("Ada Lovelace");
    const row = (await within(ada).findByText("Night shift")).closest("tr") as HTMLElement;
    expect(within(row).getByTestId("work-type-badge")).toHaveTextContent("Overtid 50 %");
    // 333.33 × 1.5 h × 150 % is 749.99 — not 1.5 × the 500.00 an hour shows.
    expect(row).toHaveTextContent("749.99");
    expect(within(row).getByTestId("rate-line")).toHaveTextContent("333.33 × 150 % = 500");
  });
```

In `apps/time/frontend/src/pages/my-week.test.tsx`, add inside `describe("MyWeekPage", …)`:

```tsx
  it("names the work types a row's entries were logged as, and keeps one when its hours change", async () => {
    const fetchMock = stubTimeApi({
      week: week([weekRow(pmRow, [entry({ id: 501, entryDate: WEEK, hours: 7.5, workType: { id: 6001, name: "Overtid 50 %" } })])]),
    });
    renderRoute(`/time?week=${WEEK}`);

    const monday = await findCell(PM, "Monday");
    const row = monday.closest("tr") as HTMLElement;
    expect(within(row).getByTestId("work-type-badge")).toHaveTextContent("Overtid 50 %");

    await userEvent.clear(monday);
    await userEvent.type(monday, "8");
    await userEvent.tab();

    await waitFor(() => expect(sent(fetchMock, "PUT").body).toMatchObject({ hours: 8, workTypeId: 6001 }));
  });

  // A row is a trackable, not a work type: typing into an empty day of a row
  // badged "Overtid 50 %" logs ordinary hours (work types design D3 — picking
  // a type is the entry form's, the grid writes a duration).
  it("logs ordinary hours into an empty day of a row whose entries carry a work type", async () => {
    const fetchMock = stubTimeApi({
      week: week([weekRow(pmRow, [entry({ id: 501, entryDate: WEEK, hours: 7.5, workType: { id: 6001, name: "Overtid 50 %" } })])]),
    });
    renderRoute(`/time?week=${WEEK}`);

    await userEvent.type(await findCell(PM, "Wednesday"), "3{Enter}");

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/time/entries",
        body: { projectId: 1001, billingLineId: 3001, entryDate: "2026-09-16", hours: 3 },
      }),
    );
  });

  // An entry whose type was deactivated since is refused on its next save
  // (D3). The grid has no work-type field to put that on, so it says the
  // server's sentence in its notification and puts the saved hours back.
  it("says why when new hours are refused because the entry's work type was retired", async () => {
    stubTimeApi({
      week: week([weekRow(pmRow, [entry({ id: 501, entryDate: WEEK, hours: 7.5, workType: { id: 6003, name: "Gammel overtid" } })])]),
      write: (method) =>
        method === "PUT"
          ? jsonResponse(400, { title: "Invalid time entry", errors: { workTypeId: ["Work type is no longer active"] } })
          : undefined,
    });
    renderRoute(`/time?week=${WEEK}`);

    const monday = await findCell(PM, "Monday");
    await userEvent.clear(monday);
    await userEvent.type(monday, "8{Enter}");

    expect(await screen.findByText("Could not save the hours")).toBeInTheDocument();
    expect(screen.getByText("Work type is no longer active")).toBeInTheDocument();
    await waitFor(() => expect(cell(PM, "Monday")).toHaveValue("7.5"));
  });
```

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/time/frontend test -- src/pages/approvals.test.tsx src/pages/my-week.test.tsx
```
Expected: FAIL — no badge, the queue shows 750.00, no rate line (the grid's PUT already carries `workTypeId` from Step 1, and the two grid cases below the badge case already pass: they pin behaviour this delivery must not change — a grid-created entry is ordinary hours, and a refusal on `workTypeId` reaches the notification through `refusalMessage`'s first-field fallback).

In `apps/time/frontend/src/pages/approvals.tsx`, add the imports `import { RateLine } from "../components/rate-line";`, `import { WorkTypeBadge } from "../components/work-type-badge";` and `import { billedAmount } from "../lib/money";`; replace

```tsx
  // The bill rate is on the entry only for a caller who may see the project's
  // money (D8); what it bills is that rate over the entry's own hours.
  const rate = entry.billing?.billRate;
  const amount =
    rate === undefined || rate === null
      ? undefined
      : entry.billing?.currency
        ? formatters.formatCurrency(rate * entry.hours, entry.billing.currency)
        : formatters.formatNumber(rate * entry.hours, { maximumFractionDigits: 2 });
```

with

```tsx
  // The bill rate is on the entry only for a caller who may see the project's
  // money (D8); what it bills is that rate over the entry's own hours, times
  // the work type's multiplier (work types design D3), exactly and rounded
  // once — the figure the project's economy reports for the same hours.
  const rate = entry.billing?.billRate;
  const value =
    rate === undefined || rate === null ? undefined : billedAmount(rate, entry.hours, entry.billing?.multiplierPercent);
  const amount =
    value === undefined
      ? undefined
      : entry.billing?.currency
        ? formatters.formatCurrency(value, entry.billing.currency)
        : formatters.formatNumber(value, { maximumFractionDigits: 2 });
```

and in `EntryRow` replace

```tsx
        <Stack gap={0}>
          <Text size="sm" fw={600}>
            {label}
          </Text>
          <Text size="xs" c="dimmed">
            {entry.projectName}
          </Text>
        </Stack>
```

with

```tsx
        <Stack gap={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={600}>
              {label}
            </Text>
            {entry.workType && <WorkTypeBadge name={entry.workType.name} />}
          </Group>
          <Text size="xs" c="dimmed">
            {entry.projectName}
          </Text>
          <RateLine billing={entry.billing} />
        </Stack>
```

In `apps/time/frontend/src/pages/my-week.tsx`, add the import `import { WorkTypeBadge } from "../components/work-type-badge";`; directly before `interface WeekRowViewProps {` add

```tsx
/**
 * The work types a row's entries were logged as this week, each once, by
 * name. A row is one trackable (project, line, task) — the key the server
 * groups by — so a work type is a fact about its entries, and the row says
 * which ones it holds (work types design D5).
 */
const workTypesOf = (row: TimeWeekRow): string[] =>
  [...new Set(row.days.flatMap((day) => day.entries.flatMap((entry) => (entry.workType ? [entry.workType.name] : []))))].sort(
    (a, b) => a.localeCompare(b),
  );
```

and in `WeekRowView` replace

```tsx
        <Stack gap={0}>
          <Text size="sm" fw={600}>
            {label}
          </Text>
          <Text size="xs" c="dimmed">
            {row.projectName}
          </Text>
        </Stack>
```

with

```tsx
        <Stack gap={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={600}>
              {label}
            </Text>
            {workTypesOf(row).map((name) => (
              <WorkTypeBadge key={name} name={name} />
            ))}
          </Group>
          <Text size="xs" c="dimmed">
            {row.projectName}
          </Text>
        </Stack>
```

- [ ] **Step 5: Run everything, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bunx biome check --write apps/time/frontend/src
mise exec -- bun run --cwd apps/time/frontend test
mise exec -- bun run --cwd apps/time/frontend typecheck
mise exec -- bun run --cwd apps/time/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```
Expected: PASS — the existing day and my-week cases unchanged (their bodies carry no `workTypeId`, since none was picked).

Prove the tests can fail, restoring after each: remove `workTypeId: null` from `pickProject` — the "forgets the choice" assertion goes red; remove the `.filter((type) => type.active)` — "Gammel overtid" is offered, red; compute the queue's value as `(entry.billing?.effectiveRate ?? rate) * entry.hours` — it shows 750.00 and the exact-amount case goes red; remove the `workTypeId` spread from `timeEntryUpdateFrom` — the grid case goes red; make `WeekCell`'s create spread the row's first entry's `workTypeId` — the ordinary-hours case goes red. Say what each printed.

```bash
cat > /tmp/claude-1000/msg-wt-7.txt <<'EOF'
feat(time-ui): an entry picks its work type, and every list says which it was

The entry form offers the chosen project's active work types — read
from the projects API the way its billing lines are — with "Ordinary
hours" as the empty choice, forgets the choice when the project
changes, and sends workTypeId. My week, the day view and the approval
queue show the type as a badge after the trackable code; where the
billing block is visible the rate line reads "900 × 150 % = 1 350";
the queue bills an entry's hours exactly at the multiplied rate; and a
grid edit keeps an entry's work type. en + nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/time/frontend/src/api/projects.ts apps/time/frontend/src/api/projects.test.ts \
 apps/time/frontend/src/api/entries.ts apps/time/frontend/src/api/entries.test.ts \
 apps/time/frontend/src/lib/money.ts apps/time/frontend/src/lib/money.test.ts \
 apps/time/frontend/src/components/work-type-badge.tsx apps/time/frontend/src/components/rate-line.tsx \
 apps/time/frontend/src/pages/-entry-form-modal.tsx apps/time/frontend/src/pages/day.tsx \
 apps/time/frontend/src/pages/day.test.tsx apps/time/frontend/src/pages/approvals.tsx \
 apps/time/frontend/src/pages/approvals.test.tsx apps/time/frontend/src/pages/my-week.tsx \
 apps/time/frontend/src/pages/my-week.test.tsx apps/time/frontend/src/test/server.ts \
 apps/time/frontend/src/test/fixtures.ts apps/time/frontend/src/i18n.ts"
git status --short
git add $PATHS && git commit -F /tmp/claude-1000/msg-wt-7.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 8: Verify the whole branch and open the PR

- [ ] **Step 1: The whole suite, as CI runs it**

```bash
cd /home/anders/projects/vantigo/vantigo && gh run list --branch main --limit 5   # is main already red? say so in the report if it is
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
test -z "$(mise exec -- gofmt -l internal cmd)" && mise exec -- go vet ./... && mise exec -- go build ./...   # the pre-commit hook runs the same check
mise exec -- golangci-lint run ./...   # depguard: no module imports another; the integration package is the one exception
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -40; echo "exit ${PIPESTATUS[0]}"
mise exec -- go generate ./... >/dev/null 2>&1; cd /home/anders/projects/vantigo/vantigo && git status --short   # clean but for go.mod/go.sum
mise exec -- bun run gen:client && git status --short                                                         # still clean
mise exec -- bun run --cwd apps/projects/frontend test && mise exec -- bun run --cwd apps/time/frontend test
mise exec -- bun run --cwd apps/projects/frontend typecheck && mise exec -- bun run --cwd apps/time/frontend typecheck
mise exec -- bun run --cwd apps/projects/frontend lint && mise exec -- bun run --cwd apps/time/frontend lint
mise exec -- bun run --cwd apps/host/frontend test && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/projects/frontend/src apps/time/frontend/src
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short -- openapi/COVERAGE.md   # committed in Tasks 1–3: must print nothing
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits, and the CI runner has 4; drop it if the suite is green without it. Run the frontend packages one at a time as above, not through `bun --filter`, which times out on CI's fan-out. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so rather than fixing it here. Test logs from parallel packages interleave: read a failure's own `--- FAIL` block, not the lines around it.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/projects.yaml openapi/time.yaml
git diff main..HEAD -- apps/server/internal/contracts apps/server/internal/module
git diff main..HEAD -- openapi/testdata/exchanges   # must print nothing
cd apps/server && mise exec -- go test -count=1 -run 'TestNoModuleReferencesAnotherModulesSchema|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestServeMuxConflictsArePinned' ./internal/db/ ./internal/openapi/ && cd ../..
grep -rn 'work-type\|workType\|WorkType' docs/projects.md docs/time.md docs/module-boundaries.md | head -40   # the docs say what the code does
```
Check, by eye: the spec commit plus seven task commits, each trailer exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; `go.mod`/`go.sum` still untracked; no existing `required:` list changed in either yaml (the diff adds `required:` only inside `WorkTypeRequest`, `WorkTypeResponse`, `ProjectEconomyWorkType` and `TimeEntryWorkType`); two migrations, `00031` (projects) and `00032` (time); no projects file imports time or the reverse; no query names another module's schema; no `float64` multiplied into money anywhere in the diff (`git diff main..HEAD -- apps/server | grep -n '\* \*\|float64(.*) \*'` finds only display code or none); `internal/projects` computes no amount (grep the diff of `internal/projects` for `Mul(` — only none, or the existing economy math).

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/project-costs-multipliers
cat > /tmp/claude-1000/pr-work-types.md <<'EOF'
## Work types and overtime multipliers (Projects phase 3, delivery B)

The Norwegian overtime case — billed at 150 % and paid with an uplift — has been a
duplicate billing line on every project, for every kind of work. After this a project
defines its **work types** once, a person picks one when logging, and Time multiplies
whatever rates the chain resolved. Decided in
`docs/superpowers/specs/2026-09-25-project-work-types-design.md` (D1–D6).

- **A project-level rule** (`projects.work_types`, migration `00031`): a name, a bill
  multiplier and a cost multiplier as percentages, on every billing line of the project.
  `GET /projects/{id}/work-types` for anyone who sees the project (a multiplier is a
  rule, not an amount); `POST` / `PUT …/{workTypeId}` for a manager, a taken name in any
  case answered 409, `work-type-added` / `work-type-changed` on the timeline. No delete —
  a type is deactivated. No project lock: nothing here depends on the currency.
- **The directory hands it over**: `contracts.ProjectDirectory.WorkType` / `WorkTypes`.
- **Time snapshots, never bakes** (migration `00032`): `workTypeId` on both entry
  requests, checked before the transaction, snapshotted beside the base rates and frozen
  on submit; the entry answers `workType` and, inside the shaped blocks,
  `multiplierPercent` and a display-only `effectiveRate`.
- **Multiplied where summed, exactly**: actuals and the project summary sum hours × base
  rate × multiplier and round once — 333.33 × 1.5 h at 150 % is 749.99, not 750.00.
  `ProjectActualsEntry` gains `WorkTypes`, on the buckets' currency rule.
- **The economy** gains `workTypes` — hours for everyone, value with financial rights,
  cost with `projects:view-costs` — shown as "Hours by work type" on the Economy tab.
- **Frontend**: a Work types card on the Billing tab with its form; the entry form's Work
  type select ("Ordinary hours" the empty choice); badges in my week, the day view and
  the approval queue; the "900 × 150 % = 1 350" rate line; the approval queue's amount
  exact with the multiplier; en + nb.
- **Integration**: projects and time composed for real, from a work type created to the
  economy's multiplied value.

Contract: three new projects operations and four new schemas; optional fields added to
`TimeEntryRequest`, `TimeEntryUpdateRequest`, `TimeEntryResponse`, `TimeEntryBilling`,
`TimeEntryCost` and `ProjectEconomyResponse` — no existing `required:` changed, no
corpus touched (neither module has one).

Decisions on the record for review: the 409 is a bare problem titled "Work type exists"
(this module grows no error codes); the timeline types are `work-type-added` /
`work-type-changed`; Time's UI reads Projects' own work-types endpoint, as it reads
billing lines, so Time gained no operation; the per-type actuals carry ids and
figures only and Projects names the rows from its own table; the multiplier enters SQL as `× (pct × 0.01)`; the project summary
multiplies too; a non-billable entry keeps both multipliers; a full replace without
`workTypeId` clears the type (the grid carries it); my week's rows stay per trackable
and badge the types their entries carry; the approval queue's amount is computed
exactly; the rate line shows only with a multiplier.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
gh pr create --base main --head feat/project-costs-multipliers \
  --title "Work types and overtime multipliers (Projects phase 3, delivery B)" \
  --body-file /tmp/claude-1000/pr-work-types.md
gh pr checks --watch
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -F body=@/tmp/claude-1000/pr-work-types.md` (`-F`, which reads the file; `-f` would send the literal string). Watch CI to green; a red check is fixed on the branch with a new commit (never `--amend`), and the report says what it was. Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL and CI's state; each test shown able to fail and what the mutation printed; anything the generated code disagreed with this plan about (sqlc's row and parameter names in Tasks 1 and 2, oapi-codegen's field names, whether `TestServeMuxConflictsArePinned` wanted other pins, Mantine's role for `NumberInput`); and whether `main` was already red. Plus the places this branch decides what the spec left open, for the user's verdict:

- the duplicate-name 409 is a bare `ProblemDetails` titled "Work type exists" — `projects/errors.go` forbids this module an error code — and the UI puts any 409 from the two writes on the name field;
- the timeline types are `work-type-added` / `work-type-changed` (the module's kebab vocabulary), payload `{workTypeId, name, fields}`, `active` one of the fields;
- no Time proxy endpoint: Time's entry form reads `GET /projects/{id}/work-types`, the way it already reads billing lines;
- (controller ruling) `WorkTypeActuals` carries no name; the economy names each row from `projects.work_types` by id and skips an id it does not know;
- the multiplier enters SQL as `× (COALESCE(pct, 100) × 0.01)`, exact multiplication rather than a rounded division;
- the project summary's billed amount multiplies too, so the Time tab and the Economy tab agree;
- a non-billable entry snapshots both multipliers; `effectiveRate` only when the block has a rate;
- a PUT without `workTypeId` clears the type, and the week grid carries it along;
- the "Hours by work type" table is in the projects frontend (Task 6);
- my week's rows stay per trackable and show a badge per distinct type of their entries;
- the approval queue's amount is exact (BigInt cents) with the multiplier;
- the rate line appears only when a multiplier applies;
- `POST` ignores `active`; deactivation is the edit form's switch; the card shows with products off; new types default to 100 %/100 %; lists order active first, then `lower(name)`, then id; no CHECK and no new index.

---

## Self-review

**Spec coverage** — every decision and every testing bullet maps to a step:

| Spec | Where |
| --- | --- |
| D1 table `projects.work_types` (`00031`), name 1–100 unique case-insensitively → 409, multipliers numeric(6,2) > 0 ≤ 1000 two decimals, `active`, no default type, no catalog | Task 1 Steps 1, 5, 6 (`validateWorkType`, `ux_work_types_project_id_name`) |
| D1 GET for anyone who sees the project; POST/PUT for `CanManage`; one transaction; unique index for the 409; timeline naming changed fields; no project lock; multipliers visible to all | Task 1 Steps 2, 6 (`work_types.go`, `timeline.go`); tests `…_EveryoneWhoSeesTheProjectReads_OnlyAManagerWrites`, `…_ANameTakenInAnyCase_Returns409`, `…_ChangesDeactivatesAndRecordsWhatMoved` |
| D2 `WorkTypeEntry`, `WorkType` (nil, nil), `WorkTypes` (active first, by name); no directory call in a locked tx | Task 1 Steps 3, 6; `TestDirectory_WorkType`, `TestDirectory_WorkTypes`; Time reads it in `checkReferences` before `withLockedTx` (Task 2 Step 6), enforced by the harness's `noteLocked` |
| D3 `workTypeId` on both requests; exists / on project / active refusals with the exact messages; snapshot columns (`00032`); frozen from submit; base rates untouched; non-billable keeps cost multiplier; `rateSource` untouched | Task 2 Steps 1–3, 6; tests `…_SnapshotsTheWorkTypeBesideTheBaseRates`, `…_RefuseAWorkTypeNotOnTheProjectOrRetired`, `…_FrozenFromSubmit_AndSnapshottedAgainWhenRejected`, `…_WithoutAWorkType_StoreAndAnswerNone` |
| D3 `actuals.sql` multiplied, `::text`, rounded once; `workType?`, `billing/cost.multiplierPercent?` + `effectiveRate?` shaped with their blocks | Task 2 Steps 2, 3, 6; `TestActualsMultipliesWhereItSums` (333.33 × 1.5 h × 150 % = 749.99), `…_TheEffectiveRateIsForDisplay`, `…_TheMultipliersAreShapedWithTheirBlocks` |
| D4 `WorkTypeActuals` on `ProjectActualsEntry`, all buckets, currency-gated, other-currency hours in hours only, only types with entries; `ActualsForProjects` unchanged; named by Projects from its own table (controller ruling), by name | Task 2 Steps 3, 6 (`TestActualsReportsEveryWorkTypeInTheProjectsCurrency`); Task 3 (`…_AreNamedFromTheProjectsOwnTypes`, the golden test); Task 4 (the rename) |
| D4 economy `workTypes` shaping (no rights, financial, view-costs, no currency, time tracking off, empty) | Task 3; the three `…_WorkTypes_…` tests |
| D5 Billing tab card (list, add, edit, deactivate, 409 on name, helper line, viewer no buttons) | Task 6 Steps 3, 4 |
| D5 entry form select (shown only with active types, reset on project change, "Ordinary hours", sent as `workTypeId`); badges in my week, day, approvals; rate line; economy table; the week grid (keeps a type on an hours edit, creates ordinary hours, reports a retired type's refusal); en + nb | Task 7 Steps 3, 4; Task 6 Step 6; Task 6/7 Step 2 (catalogs) |
| D6 docs: projects.md Work types section, economy block, API list; time.md rate chain step, snapshot fields, freeze, invoicing corrected, per-type list; module-boundaries directory methods; ROADMAP | Task 5 Steps 1–3 |
| Testing: modtest CRUD, validation, 409 in any case, deactivate, canManage vs viewer 403, 404s, member sees multipliers, timeline, directory methods, economy shaping via fake actuals, contract coverage | Tasks 1, 3 (coverage via `RequireCoverage`, Task 1 Step 7) |
| Testing: Time create/update with a type, foreign/inactive refused, none → NULLs, a non-billable entry keeping its cost multiplier, freeze and re-snapshot, blocks shaped, approval queue, actuals exact to the cent, per-type with currency gating, `ActualsForProjects` unchanged | Task 2 Step 5 (`…_ANonBillableEntryKeepsItsCostMultiplier` among them) |
| Testing: integration | Task 4 |
| Testing: frontend card, form, badges, rate line, economy table, both catalogs; docs against the code | Tasks 6, 7, 5 Step 4 |
| Out of scope (no automatic overtime, no payroll, no per-line restriction, no expenses, no catalog, no re-resolve after submit, no billable flip, no supplier invoices, no forecast) | nothing in Tasks 1–7 adds any; `ROADMAP.md` names supplier invoices as next |

**Placeholder scan.** Every step carries its code, SQL, yaml, test and command; the three hedges left are about what a generator emits (sqlc/oapi-codegen names, Mantine's `NumberInput` role, extra ServeMux pins), each with the instruction to use what it emits and report it.

**Name consistency.** Go: `WorkTypeEntry`, `WorkType`/`WorkTypes`, `WorkTypeActuals`, `ProjectActualsEntry.WorkTypes`, `workTypeExists`, `eventWorkTypeAdded`/`eventWorkTypeChanged`, `snapshotWorkType`, `multiplied`, `multiplierOf`, `economyWorkTypes`, `setWorkTypes` (projects fake), `setWorkType` (time fake). SQL: `projects.work_types(bill_multiplier_percent, cost_multiplier_percent)`, `ux_work_types_project_id_name`, `time.entries(work_type_id, work_type_name, bill_multiplier_percent, cost_multiplier_percent)`, `ProjectWorkTypeActualGroups`. Wire: `WorkTypeRequest`/`WorkTypeResponse` (`billMultiplierPercent`, `costMultiplierPercent`, `active`), `workTypeId`, `TimeEntryWorkType`, `multiplierPercent`/`effectiveRate`, `ProjectEconomyWorkType` (`hours`, `billAmount`, `costAmount`), `workTypes`. TS: `WorkType`, `WorkTypeInput`, `workTypesQueryOptions`, `EconomyWorkType`, `ProjectWorkType`, `projectWorkTypesQueryOptions`, `billedAmount`, `WorkTypeBadge`, `RateLine`, `kvemWorkTypes`. Timeline strings `work-type-added`/`work-type-changed` in `timeline.go`, `-project-timeline.tsx` and `docs/projects.md` alike.

**Real paths, numbers and commands.** Checked on the branch before writing: the latest migration is `00030_customers_personal_data.sql`, so `00031`/`00032` are free; every file under **Modify** exists (`ls`), and every edit anchor quoted in a "replace … with …" occurs exactly once in its file (a script counted them; the one that did not — `maps.Copy(body, overrides)`, twice in `time/harness_test.go` — is anchored on the switch above it); `openapi/testdata/exchanges/` holds no `projects.jsonl` or `time.jsonl`; `apps/server/internal/openapi/cmd/contract` exists; `bun run gen:client`, `translations:check` and `i18n:test` are root `package.json` scripts; `test`, `typecheck` and `lint` are both packages' scripts; `/tmp/claude-1000/` exists.
