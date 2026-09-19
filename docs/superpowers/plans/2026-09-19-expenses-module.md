# Expenses Module Implementation Plan (PR A: the module and single expenses)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A new `expenses` module in which an employee records outlays (with receipts) and mileage, optionally against a project with a markup, submits them, a manager approves, payroll marks them reimbursed and whoever invoices marks billable lines invoiced — with its own app, settings and admin-managed rates.

**Architecture:** A standalone module in the Time mould: directory `apps/server/internal/expenses` (package `expenses`), schema `expenses`, URL `/api/v1/expenses`, contract `openapi/expenses.yaml`, frontend package `apps/expenses/frontend` (`@vantigo/expenses-ui`). It depends on identity only. Projects is an *optional* read through the existing `contracts.ProjectDirectory` (`deps.Projects`, nil when Projects is disabled) — the module must run, and its whole test suite must have cases, with and without it. Receipts go through `internal/storage`'s object store. One migration, `00012_expenses_baseline.sql`. Travel claims / per diem (PR B) and the Economy integration (PR C) are NOT in this plan — but the schema and contract must not paint them into a corner (`entries.claim_id` exists from day one as a plain nullable bigint; the `per_diem` kind is refused until B).

**Tech Stack:** Go (pgx v5, sqlc, goose, oapi-codegen strict server), PostgreSQL, `internal/storage`, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-19-expenses-design.md` — delivery A: §2 (all decisions), §3.1–3.5, §4 (mileage, outlays, freezing, receipt rule, period lock, currency), §5, §6 (everything except Claims and Per project), §7 (everything except the travel claim page and the project page), §8, §9.

## Global Constraints

- **Pattern module: Time.** Read before writing: `apps/server/internal/time/{module.go,server.go,authorize.go,entries.go,approval.go,settings.go,ratecards.go,stats.go,values.go,errors.go,responses.go,harness_test.go,main_test.go,sqlc.yaml,queries/}`, `internal/db/migrations/00010_time_baseline.sql`, `openapi/time.yaml`, and every place Time is registered: `internal/config` (MODULES allowlist + tests), `.golangci.yml` depguard rules, `internal/openapi` (`Modules`), wherever `businessModules` lives, `apps/server/generate.go` + the oapi-codegen cfg yaml, `internal/db/schema_test.go` (`moduleSchemas`), `internal/module/compose_test.go`, `cmd/vantigo` module list, `deploy/compose` README + env example (schema GRANTs, `MODULES` default), `scripts/smoke-image.sh`. Frontend: `apps/time/frontend` (package layout, api layer, stub server, test setup, i18n) and how the host registers Time (`apps/host/frontend/src/{apps.ts,navigation.ts,routes/time*,routes/dashboard.tsx,catalogs,app-spotlight.tsx}`). For multipart upload and the object store: identity's avatar upload and `internal/storage`, `modtest.WithObjectStore`. **Siblings win over this plan's wording; say so in the report.**
- **No dependency on Projects.** Config must accept `MODULES=customers,expenses`. Depguard: `internal/expenses/**` imports no other module (tests included). No SQL crosses schemas. `deps.Projects == nil` ⇒ `projectsAvailable:false`, every project-related request field is a 400 on that field, `GET /expenses/projects` is 404. Every project-aware behaviour has a test with a fake directory (`modtest.WithProjects`) AND its counterpart without one.
- **No call to another module inside a locked transaction** (`deps.Projects`, `deps.Users`): ask before, decide under the lock. Give this module the harness-wide check from day one — copy the *mechanism* of `internal/projects` (`withProjectLock` marking + checked accessors + the hook in an `_test.go` file) or Time's (`withLockedTx` + failing fakes); do not import either.
- **Conventions (spec X10):** outsiders 404 identical to unknown ids; a caller who can approve nothing gets 403 on batch endpoints; batches all-or-nothing, ≤ 500 ids, per-id explanations; revision-guarded full-replace PUT (409); amounts exact decimal (`math/big.Rat` from numeric text, half-up to 2 places), a third decimal is a 400 on the field; optional fields absent, never null; period lock on every mutating path with `expenses:manage` exempt; self-approval allowed.
- **Exact values:** kinds `outlay | mileage` (`per_diem` refused with a 400 until PR B); `paidBy` `employee | company`; statuses `draft | submitted | approved | rejected`; description 1–500; supplier ≤ 200; places ≤ 200; `distanceKm` > 0, ≤ 9999.9, one decimal; `passengers` 0–8; `grossAmount` > 0, ≤ 9 999 999 999.99; `0 ≤ vatAmount ≤ grossAmount`; `markupPercent` 0–1000 (two decimals); rejection reason 1–1000; references ≤ 100; attachments JPEG/PNG/HEIC/PDF, ≤ 10 MB each, ≤ 10 per entry, sniffed content type must match; rate kinds exactly as spec §3.4; seeds: `mileage` 5.30 NOK and `mileage_passenger` 1.00 NOK, `valid_from` 2026-01-01, source "State rate" (no per diem seeds in this PR; `mileage_customer` unseeded). Permissions: `expenses:access` "Use Expenses", `expenses:approve` "Approve expenses", `expenses:view-all` "View all expenses", `expenses:manage` "Manage expenses" (sensitive) — category "Expenses", delegable, descriptions per spec §5.
- **Toolchain:** `mise exec --` for everything; `golangci-lint` from `apps/server`; `bun run --cwd <dir> test`; exit codes before pipes; `TEST_DATABASE_URL` per the workspace's environment notes. The race detector runs locally now (`CC=<zcc> CGO_ENABLED=1 taskset -c 0-3 go test -race ./internal/expenses/...`, see the environment notes) — run it on the module before each commit. After contract changes: `go generate ./...` + `bun run gen:client`. After host route changes: host `build` + commit `routeTree.gen.ts`.
- **Tests must be able to fail; contract coverage gate; i18n en + nb; calendar dates in UTC; Vitest `testTimeout: 15_000` in the new workspace; commits end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; never `--no-verify`; stage by explicit path.**
- **Branch:** `feat/expenses-module`; never commit to `main`.

## File Structure

```
apps/server/internal/db/migrations/00012_expenses_baseline.sql   entries, attachments, categories, rates, settings (+ seeds)
apps/server/internal/expenses/
  module.go, server.go, authorize.go, errors.go, values.go, responses.go, contractscalls.go (checked accessors)
  meta.go, settings.go, rates.go, categories.go
  entries.go, entries_validation.go, money.go (exact decimal: net, mileage, markup)
  attachments.go
  flow.go (submit/approve/reject/unapprove), approvals.go (queue, rate override)
  reimbursements.go (list, mark, undo, csv), invoiced.go
  stats.go
  queries/*.sql, sqlc.yaml, store/, gen/
  *_test.go (harness_test.go, main_test.go with RequireCoverage, one test file per area, *_concurrency_test.go)
openapi/expenses.yaml
apps/expenses/frontend/            package scaffold like apps/time/frontend
  src/api/*, src/lib/*, src/components/{receipt-dropzone,receipt-viewer,vat-field,status-strip}.tsx
  src/pages/{my-expenses,-expense-form-modal,approvals,reimbursements,settings,-rate-form-modal,-category-form-modal}.tsx
  src/i18n.ts, src/index.ts, src/test/*
apps/host/frontend/src/            moduleKeys, apps.ts, routes/expenses*, dashboard, spotlight, catalogs, permission labels
docs/expenses.md, docs/module-boundaries.md, README/CONTRIBUTING/deploy docs, ROADMAP.md, scripts/smoke-image.sh
```

---

### Task 1: Scaffold, schema, settings, rates, categories, meta

The module exists, is registered everywhere Time is, and serves its configuration.

- Migration per spec §3.1–3.5 (incl. `entries` and `attachments` in full — later tasks add no migration), the seeds, a down migration. No CHECK constraints (house style).
- Registration everywhere listed in Global Constraints; `MODULES` default gains `expenses`; **no** `requires` rule; a config test that `customers,expenses` loads.
- Operations: `GET /meta`; `GET|PUT /settings` (manage; `GET` for access holders returns what they may know: currency, receipt rule, lock date); `GET /rates`, `POST /rates`, `PUT|DELETE /rates/{id}`, `POST /rates/reset` `{kind}` (re-creates the seeded rows of that kind that are missing and removes nothing); `GET|POST /categories`, `PUT /categories/{id}` (name, active, position; a used category cannot be renamed into a duplicate; never deleted).
- `rateFor(kind, date)` — the row with the greatest `valid_from ≤ date`; none → a typed "no rate" result the entry code turns into a field error.
- Harness (`harness_test.go`): `newHarness(t)` with Projects fake, `newHarnessWithoutProjects(t)`, `signIn(t, h, perms…)`, fake object store, the locked-call check installed for every test.

- [ ] **Failing tests first**: module mounts with and without Projects; meta in both; permissions registered with exact metadata; settings read/write + shaping; rates CRUD, uniqueness `(kind, valid_from)`, lookup by date across three rows, seeds present and labelled, reset restores a deleted seed and keeps own rows, percent kinds carry no currency; categories seeded, add/rename/deactivate, duplicate refused; migration up/down/up; config accepts `expenses` alone.
- [ ] Implement; gates (module tests incl. `-race` on 4 CPUs, vet, lint, generate no drift, `internal/config`, `internal/db`, `internal/module`, `internal/openapi` tests).
- [ ] **Commit** `feat(expenses): a new module — settings, admin-managed rates and categories`.

### Task 2: Entries — outlays and mileage, the optional project link

- Contract: `ExpenseEntryRequest` / `UpdateRequest` (+`revision`) / `Response` per spec §3.1 and §6; response `capabilities { canEdit, canDelete, canSubmit, canApprove, canOverrideRate, canMarkInvoiced, canSeeBilling }`; billing fields (`markupPercent`, `billRatePerKm`, `billAmount`) grouped in a `billing` object present only with financial rights on the project; `owedToEmployee`, `netAmount` always for those who see the entry; `attachmentCount`; `project {id, code, name}` when available and set.
- `POST /entries` (owner = caller unless `userId` given, which needs `manage`), `GET|PUT|DELETE /entries/{id}` (edit/delete while `draft|rejected`, owner or `manage`), `GET /entries` (filters per spec; a caller without view rights sees own + managed projects' lines; paged), `GET /projects` (bookable projects with billing lines; 404 without Projects).
- `money.go`: `netOf`, `mileageAmount(km, rate, passengerRate, passengers)`, `outlayBillAmount(net, markup)`, `mileageBillAmount(km, customerRate)`; pure, table-tested incl. half-up cases.
- Project rules: `CanLogTime(owner, project)` decides bookability (one 400 message whatever the reason); `billingLineId` must be an active line of that project; currency: an outlay's currency is what was entered, mileage uses the default currency; billable requires a project; markup defaults from settings when `billable` and none given; customer rate defaults from `mileage_customer` for the date (absent → field required).
- Visibility + shaping per spec §5; the financial-rights rule is computed from the session's permissions + `ProjectDirectory.Role`, as Time does.

- [ ] **Failing tests first**: create/get/update/delete per kind; validation table per field (incl. third decimal, VAT bounds, `per_diem` refused); mileage arithmetic through the API; no rate for the date → 400; with Projects: bookable / not bookable, billing line rules, markup default, bill amounts, `billing` absent for the owner without financial rights and present for the manager, recording for a colleague checks the colleague; WITHOUT Projects: each project field → 400 on that field, `/projects` 404; visibility matrix (owner, colleague 404, project manager, view-all, outsider); list filters + paging; 409 on stale revision; lock discipline (the harness check) on every write.
- [ ] Implement; gates. **Commit** `feat(expenses): outlays and mileage, optionally booked on a project`.

### Task 3: Receipts

`POST /entries/{id}/attachments` (multipart `file`; owner or `manage`; entry `draft|rejected`; type sniffed and matched; size and count limits), `GET /attachments/{id}` (streams with the stored content type, `Content-Disposition: inline; filename*=…`, `Cache-Control: private, no-store`; visibility = the entry's), `DELETE /attachments/{id}` (while editable); entry response lists `attachments [{id, fileName, contentType, sizeBytes}]`; deleting an entry removes its objects (after the row delete commits; a failed object delete is logged, never fails the request); object keys are random, never derived from names.

- [ ] **Failing tests first**: upload each allowed type; refused types (incl. a PNG named `.pdf`), oversize, the 11th file; download by owner / approver / outsider (404); delete rules; upload to a submitted entry → 400; entry delete removes objects (fake store asserts); object store error paths.
- [ ] Implement; gates. **Commit** `feat(expenses): receipts on an expense`.

### Task 4: The flow — submit, approve, reject, unapprove, override

`POST /submit | /approve | /reject | /unapprove` `{entryIds}` (contract already allows `claimIds`, which must be empty until PR B → 400); freezing on submit (rate, amounts, bill amount recomputed one last time and stored; afterwards nothing recomputes); receipt rule; who may approve (spec X9) with project roles warmed BEFORE the transaction; reject needs a reason; unapprove (approver or manage; not once reimbursed or invoiced) → fresh draft; `GET /approvals` (submitted units the caller may approve, grouped per person with totals, receipt indicator, overridden flag; paged in SQL); `PUT /entries/{id}/rate` `{rate, passengerRate?, revision}` for submitted mileage lines by an approver of that line or manage — records `rate_overridden_by_user_id` and `rate_table_value`, recomputes the frozen amounts; period lock everywhere.

- [ ] **Failing tests first**: every allowed and refused move; all-or-nothing with per-id reasons; 500 cap; 403 for a caller who approves nothing; project manager approves own project's lines only; project-less lines need `approve`; freezing (change the rate table after submit → amount unchanged; reject → edit → resubmit refreezes); receipt rule on/off/at the boundary; override audit fields and that the owner cannot; lock blocks each move except for manage; concurrency: two approvers, one wins, the other gets the per-id refusal; queue contents per caller kind.
- [ ] Implement; gates. **Commit** `feat(expenses): submit, approve and reject, with rates frozen on submit`.

### Task 5: After approval — reimbursed, invoiced, the CSV, stats

`GET /reimbursements` (approved, `owedToEmployee > 0`, not reimbursed; grouped per person, totals per currency; `manage`), `POST /reimbursed` `{entryIds, date, reference?}`, `POST /reimbursed/undo`; `GET /reimbursements/export.csv` (same filter or explicit ids; UTF-8 with a BOM, `;`-separated, decimal comma, ISO dates — the form a Norwegian Excel opens correctly, documented in `docs/expenses.md`; fields quoted when they contain `;`, quotes or newlines, and a leading `=`, `+`, `-` or `@` in a text field is prefixed with `'` against formula injection; columns: employee display name, user id, date, kind, description, category, currency, gross, vat, owed, project code); `POST /entries/{id}/invoiced` `{reference?, revision}` and `/invoiced/undo` (financial rights on the project; approved + billable only; unapprove refused while invoiced or reimbursed). Stats: `/stats`, `/stats/summary` (awaiting my approval, my unreimbursed amount per currency), `/stats/timeseries` (approved net per day), `/stats/attention` (`approvalWaiting` entityId = owner user id; `expenseRejected` entityId = entry id, for the owner; `reimbursementWaiting` for manage holders) — same response shapes as Time's stats so the host's dashboard code is reused.

- [ ] **Failing tests first**: owed computation per kind and payer; mark / undo / nothing-owed refusal; CSV bytes for a fixture; invoiced rules and rights; unapprove refusals; each stat and attention item per caller kind.
- [ ] Implement; gates; whole `apps/server` suite under `-race` on 4 CPUs. **Commit** `feat(expenses): reimbursements with a payroll export, invoiced lines, and dashboard stats`.

### Task 6: Frontend — the package, My expenses, the form, receipts

Scaffold `apps/expenses/frontend` like `apps/time/frontend` (workspace registration, tsconfig, eslint, vite config with `testTimeout: 15_000`, test setup with `asyncUtilTimeout`, stub server, i18n). `api/` (query options under `["expenses", …]`, mutations invalidating the prefix, forms send the revision they loaded); `pages/my-expenses.tsx` (status strip, filters in URL search params, list, empty state); `pages/-expense-form-modal.tsx` (kind switch; outlay: date, description, category, supplier, gross, VAT field with 25/15/12/none helper and the net shown, paid-by, receipts; mileage: date, from, to, km, passengers, the computed amount and rate shown; the project block ONLY when `meta.projectsAvailable`: project select from `GET /expenses/projects`, billing line, billable, markup / customer rate — rendered from `capabilities.canSeeBilling` for existing lines); `components/receipt-dropzone.tsx` (drag/drop + `capture="environment"` input, per-file progress, server errors per file), `components/receipt-viewer.tsx` (images inline, PDFs in an `<object>` with a download fallback; keyboard-operable; alt text = file name); rejected reason banner; submit from the list and from the form.

- [ ] **Failing tests first** (stub server, real router, no mocks of package functions): form per kind incl. VAT helper arithmetic display and field errors; project block absent/present by meta; create → list; edit sends loaded revision; upload/remove receipt; receipt rule message on submit; strip totals; filters in the URL; rejected banner.
- [ ] Implement; package `test|typecheck|lint`, root biome / translations / i18n. **Commit** `feat(expenses-ui): my expenses, the expense form and receipts`.

### Task 7: Frontend — approvals, reimbursements, settings

`pages/approvals.tsx` (groups per person; unit rows with project, total, receipt indicator, overridden flag; drawer with lines + receipt thumbnails + viewer; approve / reject with reason; only `canApprove` units selectable; rate override dialog showing table value → new value), `pages/reimbursements.tsx` (per person totals per currency; select; mark reimbursed dialog (date, reference); undo on a "recently reimbursed" filter; **Export CSV** downloading the API's file), `pages/settings.tsx` (general form; rates grouped by kind with source badge "State rate" / "Own rate", add / edit / remove, "Reset to default"; categories add / rename / deactivate / reorder with up-down buttons). Every table and per-row button named after its row (the convention #105 established).

- [ ] **Failing tests first**; implement; gates. **Commit** `feat(expenses-ui): approvals, reimbursements and settings`.

### Task 8: Host integration and docs

`moduleKeys` += `expenses`; app registry entry "Expenses"/"Utlegg" with the four sidebar items (Approvals: sidebar `expenses:approve`, route guard `expenses:access` via `guardPermissions`); routes `/expenses`, `/expenses/approvals`, `/expenses/reimbursements`, `/expenses/settings` with the package's search validators; dashboard card + metric + three attention types (hrefs, en+nb titles, hint hidden at 0); spotlight "New expense" → `/expenses?new=outlay`; permission labels en+nb (extend `admin.test.ts`); `routeTree.gen.ts`. Docs: `docs/expenses.md` (model, rules, permissions, the optional Projects link and what changes without it, rates and overriding them, receipts and where files live, the API table, what PR B and C add), `docs/module-boundaries.md` (a module that depends on nobody; the optional directory read), README / CONTRIBUTING / docs index / deploy docs (`MODULES`, schema GRANTs, object-store note for receipts), `ROADMAP.md`, `scripts/smoke-image.sh` ("expenses demands authentication"), `openapi/COVERAGE.md`.

- [ ] Tests first; implement; host `test|typecheck|lint|build`; root gates. **Commits** `feat(frontend): the Expenses app in the switcher, dashboard and spotlight` and `docs(expenses): module guide, boundaries and roadmap`.

### Task 9: Final gates and PR

- [ ] Whole-branch reviews (backend / frontend+docs, most capable model), one fix wave, re-review.
- [ ] All gates incl. the whole Go suite under `-race` on 4 CPUs, all frontend gates, `mise run smoke`; trailers normalised; push; PR against `main` (NEVER stacked on a feature branch); watch checks; fix on red; never merge. PR B starts only on top of what is merged.
