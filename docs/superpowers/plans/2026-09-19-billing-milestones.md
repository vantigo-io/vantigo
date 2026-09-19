# Billing Milestones and Line Budgets Implementation Plan (PR A of Project economy)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add billing milestones (the invoice plan, with a manual planned → ready → invoiced flow) and optional budgets per billing line to the projects module, with an Economy tab on the project page that shows the plan.

**Architecture:** Everything lands inside the existing `projects` module (Go package `internal/projects`, schema `projects`, contract `openapi/projects.yaml`, package `@vantigo/projects-ui`), following the patterns tasks established: load → authorize → bare 404 / 403 → validate → `db.WithTx`; revision-guarded full-replace PUT; per-project advisory lock for ordering; financial shaping by absence; `capabilities` on responses. One migration, `00011_projects_milestones.sql`. No new module, no new permission (that is PR B's).

**Tech Stack:** Go (pgx v5, sqlc, goose, oapi-codegen strict server), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-19-project-economy-design.md` — delivery A: §2 E1, E3 (budget fields only), E5; §3.1–3.3; §5 "Delivery A"; §7 Economy tab items 1 (plan figures) and 4; §8; §9. Budget-vs-actual, the actuals contract, the portfolio, alerts and `projects:view-costs` are PR B and must not be started here.

## Global Constraints

- **Pattern files:** backend `apps/server/internal/projects/{tasks.go,tasks_validation.go,lines.go,lines_validation.go,authorize.go,responses.go,values.go,errors.go,timeline.go,projects.go,harness_test.go,tasks_test.go,tasks_concurrency_test.go,lines_test.go}`; queries `queries/{tasks.sql,lines.sql}`; migration `00009_projects_tasks.sql`. Frontend `apps/projects/frontend/src/{api/tasks.ts,pages/project-billing.tsx,pages/-billing-line-form-modal.tsx,pages/project-tasks.tsx,lib/dates.ts}`; host `apps/host/frontend/src/routes/projects/{$projectId.time.tsx,$projectId.billing.tsx,-project-detail-layout.tsx}`. **Read the siblings before following this plan's wording** — where they disagree, the siblings win and the report says so.
- **Boundaries:** `internal/projects/**` imports no other module (tests included; fakes against `internal/contracts`); no SQL crosses schemas (`*_user_id` columns are opaque uuids). Module frontend packages import neither each other nor the host; pages read the router themselves.
- **Authorization:** "financial rights on the project" is the module's existing rule (`authorize.go`: the project's manager, `projects:manage-all`, or `projects:view-financials` on a project the caller can see). "Manager" below means the project's manager **or** `projects:manage-all`. Outsiders get a bare 404 identical to an unknown id — for milestones as for tasks, by loading the milestone, then its project. A caller who sees the project but has no financial rights gets **403** on milestone reads (the milestone's existence is not secret to them; the amounts are).
- **Exact values:** milestone status `planned | ready | invoiced | cancelled`; name trimmed 1–200; description ≤ 2000; `invoiceReference` ≤ 100 trimmed; exactly one of `amount` / `percent`; `amount > 0`, ≤ 9 999 999 999.99; `0 < percent ≤ 100`, two decimals; `percent` requires the project's fixed price; any milestone requires the project's `currency`; line `budgetHours > 0`, `budgetAmount > 0`, `budgetAmount` requires the project's `currency`. Timeline event types: `milestone-added`, `milestone-removed`, `milestone-ready`, `milestone-planned`, `milestone-invoiced`, `milestone-invoice-undone`, `milestone-cancelled`, `milestone-reopened`. Advisory lock class for milestone ordering: `10` (tasks use `9`), two-argument form, object = project id.
- **Money:** contract amounts are JSON numbers like the rest of `projects.yaml`; the percent → amount calculation is **exact decimal** (`math/big.Rat` from the numeric's text, half-up to two places — `apps/server/internal/time/rates.go`'s `discounted` is the reference; do not import it, projects may not import time). 300 000.00 × 33.33 % = 99 990.00; 100 000.01 × 12.5 % = 12 500.00 (12 500.00125 rounds down); 999.99 × 50 % = 500.00 (499.995 rounds half-up).
- **Toolchain:** `mise exec --` for go/sqlc/golangci-lint/bun; `golangci-lint run` from `apps/server` only; `bun run --cwd <dir> test`; capture exit codes before pipes. Go tests need `TEST_DATABASE_URL` (see the ledger's environment note). After `openapi/projects.yaml` changes: `cd apps/server && mise exec -- go generate ./...` and `mise exec -- bun run gen:client`; commit results. After host route changes: `mise exec -- bun run --cwd apps/host/frontend build` and commit `routeTree.gen.ts`.
- **Contract coverage gate:** every new operation in `projects.yaml` must be exercised by a passing test (`main_test.go`'s `RequireCoverage`).
- **Tests must be able to fail:** for every rule, see the test red before the code exists; say so in the report.
- **i18n:** every user-facing string in `en` and `nb`; `translations:check` passes. Plain calendar dates format with `timeZone: "UTC"`.
- **Commits:** Conventional Commits, each ending with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; never `--no-verify`.
- **Branch:** `feat/project-billing-milestones`; never commit to `main`.

## File Structure

```
apps/server/internal/db/migrations/00011_projects_milestones.sql   billing_milestones, billing_lines.budget_*
apps/server/internal/projects/
  milestones.go              CRUD, position, status handlers
  milestones_validation.go   content rules, the move table, effective amount (exact decimal)
  milestones_test.go, milestones_status_test.go, milestones_concurrency_test.go, milestones_internal_test.go
  lines.go / lines_validation.go / responses.go   + budgetHours, budgetAmount
  projects.go / values.go    + fixed-price and currency guards counting milestones and line budgets
  authorize.go               + canManageMilestones on the project's capabilities
  timeline.go                + the eight milestone event types
  queries/milestones.sql, queries/lines.sql (+), sqlc.yaml (+ 00011)
openapi/projects.yaml        + milestone operations and schemas, line budget fields, capability
apps/projects/frontend/src/
  api/milestones.ts (+test)  queryOptions + mutations, keys under ["projects","milestones",…]
  lib/milestones.ts          status → label key + colour, overdue rule
  pages/project-economy.tsx  the Economy tab (plan half): figures, invoice plan, footer totals
  pages/-milestone-form-modal.tsx, pages/-milestone-invoiced-modal.tsx
  pages/-billing-line-form-modal.tsx (+ budget fields), pages/project-billing.tsx (+ budget columns)
  pages/-project-timeline.tsx (+ the new event types), i18n.ts (+), index.ts (+)
apps/host/frontend/src/
  routes/projects/$projectId.economy.tsx, -project-detail-layout.tsx (+ tab), catalogs (+ tab label), routeTree.gen.ts
docs/projects.md (+ Economy / billing milestones section), ROADMAP.md (phase 3 progress)
```

---

### Task 1: Migration, line budgets and the project guards

**Files:** Create `00011_projects_milestones.sql`; modify `sqlc.yaml`, `queries/lines.sql`, `lines.go`, `lines_validation.go`, `responses.go`, `projects.go`/`values.go` (guards), `openapi/projects.yaml`, tests `lines_test.go`, `projects_update_test.go` (or the file that holds the currency guard's tests), `internal/db` migration tests if they enumerate migrations.

**Migration** (goose up/down, conventions of `00009`): the `projects.billing_milestones` table exactly as spec §3.2 (identity from 1001, `ON DELETE` behaviour like tasks' project FK, index `(project_id, position)`, partial index `(project_id, planned_date) WHERE status IN ('planned','ready')`), and `ALTER TABLE projects.billing_lines ADD COLUMN budget_hours numeric(10,2), ADD COLUMN budget_amount numeric(12,2)`. Down drops both. No CHECK constraints (house style); rules live in Go.

**Line budgets:** `BillingLineRequest`/update gain optional `budgetHours`, `budgetAmount`; `BillingLineResponse` gains `budgetHours` at the top level and `budgetAmount` **inside the line's existing financial shaping** (absent without financial rights — assert on raw JSON). Rules per Global Constraints; `budgetAmount` without a project currency → 400 on `budgetAmount`.

**Guards (spec §3.3):** extend the existing "currency cannot change or clear while amounts exist" check to count (a) any non-cancelled milestone and (b) any line `budget_amount`. Add the fixed-price guard: a project update that clears `fixedPriceAmount`, or changes `billingType` away from fixed price, is refused with a 400 on `fixedPriceAmount` naming the milestones (by name, at most five then "and N more") while milestones with `percent` and status `planned` or `ready` exist. Both decided **inside** the update transaction against locked/just-read rows (the TOCTOU lesson from billing lines). The milestone rows these guards need can be inserted by test helpers with SQL until Task 2 provides the API.

- [ ] **Step 1: Failing tests** — line create/update/read with budgets (stored, returned, `budgetAmount` absent for a member, rules table, currency requirement); currency guard with a milestone and with a line budget amount; fixed-price guard (clear price, change billing type; allowed when the only percent milestones are invoiced or cancelled; changing the price to another value is allowed); migration up/down.
- [ ] **Step 2–4** — see them fail, implement, run (`go test ./internal/projects/... ./internal/db/...`, once under `taskset -c 0-3`), `go vet`, lint, `go generate` + `gen:client` no drift.
- [ ] **Step 5: Commit** `feat(projects): budgets on billing lines and the guards billing milestones need`.

**Interfaces produced:** sqlc models `ProjectsBillingMilestone`; harness helper `insertMilestone(t, h, projectID, overrides)` (SQL-level) for later tasks.

### Task 2: Billing milestones — CRUD, ordering and status

**Files:** Create `milestones.go`, `milestones_validation.go`, `queries/milestones.sql`, the four test files; modify `openapi/projects.yaml`, `responses.go`, `authorize.go`, `timeline.go`, `harness_test.go` (helpers `createMilestone`, `getMilestones`, `moveMilestoneStatus`).

**Contract shapes:**

```
BillingMilestoneResponse { id int32, projectId int32, name, description?, plannedDate? date,
    amount? number, percent? number,          // exactly one, as entered
    effectiveAmount number, currency string,   // what the plan counts (spec §3.2 "Effective amount")
    status, position int32, overdue bool,      // overdue: planned|ready, plannedDate < today (server clock, UTC date)
    readyAt?, readyBy? {userId, displayName, active}, invoicedAt?, invoicedBy? {…},
    invoiceReference?, invoiceDate? date, revision int32, createdAt, updatedAt,
    capabilities { canEdit, canDelete, canMarkReady, canMarkPlanned, canMarkInvoiced, canUndoInvoiced, canCancel, canReopen } }
BillingMilestonePlanResponse { milestones: [BillingMilestoneResponse],
    totals { currency, planned number, ready number, invoiced number, cancelled number,
             fixedPrice? number, unplanned? number, overPlanned? number } }   // planned = planned status only; unplanned/overPlanned vs planned+ready+invoiced
BillingMilestoneRequest       { name, description?, plannedDate?, amount?, percent? }          // POST
BillingMilestoneUpdateRequest = request + revision                                             // PUT, full replace
BillingMilestonePositionRequest { position int32, revision int32 }
BillingMilestoneStatusRequest   { status, revision int32, invoiceReference?, invoiceDate? }
```

Operations (all `permission:projects:access`), access per spec §5 and Global Constraints:
- `GET /projects/{id}/milestones` → plan, `position` order with cancelled last. `POST` → 201, appended.
- `GET|PUT|DELETE /projects/milestones/{milestoneId}`; `PUT …/position` (renumber 1..n under the advisory lock); `POST …/status`.
- `ProjectCapabilities` gains `canManageMilestones` (manager) — the UI's "Add milestone" switch; `canSeeFinancials` already gates the tab's plan.

**Rules:** content edits only while `planned` or `ready` (else 400 on `status`: "An invoiced milestone cannot be edited; undo the invoicing first" / cancelled equivalent); delete only `planned` with `ever_moved = false` (else 400 explaining cancel is the way); moves exactly per the spec's table — any other pair is a 400 naming both statuses; `invoiceReference`/`invoiceDate` accepted only on → invoiced (400 otherwise); → invoiced freezes `invoiced_amount` = effective amount at that instant and stamps `invoiced_at/by`; undo clears all four invoice fields; → ready stamps `ready_at/by`, ready → planned clears them; every move sets `ever_moved`. `invoiced ↔ ready` needs financial rights only (a `projects:view-financials` holder who is no manager may do it; a manager may too); the other moves need manager. Everything revision-guarded (409 with the module's conflict problem). Each write records its timeline event with the milestone's name in the payload (amounts never go in the timeline: members read it). The status write re-reads the milestone `FOR UPDATE` inside the transaction and decides the move against that row. `readyBy`/`invoicedBy` resolve through `deps.Users` in one batch for a plan; a disabled user renders `active:false`.

- [ ] **Step 1: Failing tests** — create with amount, create with percent (effective amounts per the three exact-decimal cases, as an internal test on the pure function **and** through the API); percent refused without a fixed price; milestone refused without a currency; both/neither of amount/percent; limits table; plan totals incl. `unplanned` and `overPlanned` and cancelled excluded; percent milestone follows a fixed-price change, an invoiced one does not; get/update/delete rules; 409 on stale revision for PUT, position and status; the full move matrix (every allowed move with its stamps/clears and timeline entry; every refused pair); who may move what (member 403 on reads, viewer-with-`view-financials` reads and marks invoiced but cannot mark ready, manager everything, outsider bare 404 identical to unknown id); `overdue`; ordering (append, move, renumber, cancelled last in the listing); concurrency: two simultaneous creates get distinct positions, two simultaneous status moves → one wins and one 409.
- [ ] **Step 2–4** — fail, implement, run (pinned once), vet, lint, generate + `gen:client` no drift, contract coverage green.
- [ ] **Step 5: Commit** `feat(projects): billing milestones with an invoice plan and a manual invoiced step`.

### Task 3: Frontend — API layer, Economy tab (plan half), line budgets

**Files:** per File Structure; tests beside each. Mirror `project-billing.tsx` (tab root owns its spacing, skeleton, alert; capabilities drive actions), `-billing-line-form-modal.tsx` (form modal), `api/tasks.ts` (query options + mutations + blanket `["projects"]` invalidation), `lib/dates.ts`.

- `api/milestones.ts`: `milestonePlanQueryOptions(projectId)`, mutations `createMilestone`, `updateMilestone`, `deleteMilestone`, `moveMilestone`, `setMilestoneStatus`; forms send the revision they loaded.
- `lib/milestones.ts`: status → i18n key + colour (`planned` gray, `ready` yellow, `invoiced` green, `cancelled` gray/dimmed) and an `isMilestoneStatus` guard.
- `pages/project-economy.tsx` (`ProjectEconomy({ projectId })`): reads the project (for `capabilities` and the fixed price). With `canSeeFinancials`: three headline figures (planned, ready to invoice, invoiced — with currency), the invoice plan table (name + description tooltip, planned date with an "overdue" badge, amount — `30 % · 300 000 kr` for percent —, status badge, row menu built **only** from the milestone's `capabilities`), reorder via up/down buttons in the row menu (no new dependency; check how tasks reorder and copy it), cancelled rows struck through and last, footer totals with the unplanned / over-planned sentence on fixed-price projects (over-planned as a gentle yellow note, never an error), empty state with "Add milestone" for `canManageMilestones`. Without `canSeeFinancials`: a short note that the invoice plan is for people who can see the project's financials (PR B fills this view with the hours budget). A project without a currency: a note that milestones need a currency, linking to the project form.
- `pages/-milestone-form-modal.tsx`: name, description, planned date, amount-or-percent `SegmentedControl` (percent only when the project has a fixed price, with the computed amount previewed from the server's rule: show `fixedPrice × percent` formatted, labelled "about" — the server's number is the truth after save), server field errors mapped onto fields.
- `pages/-milestone-invoiced-modal.tsx`: optional invoice reference + invoice date, confirm. Undo and cancel use confirm modals naming the milestone.
- Billing line form gains `budgetHours` and `budgetAmount` (amount only with a currency and only rendered when the caller sees financials); the Billing tab table gains a Budget column ("120 h · 96 000 kr", parts as available).
- `-project-timeline.tsx` renders the eight new event types with en/nb texts.
- i18n en + nb for everything; export `ProjectEconomy` from `index.ts`.

- [ ] **Step 1: Failing tests** — plan renders rows, badges, percent formatting, overdue, cancelled last; footer sentences (unplanned, over-planned, neither without a fixed price); row menu follows capabilities (a `view-financials` viewer sees only "Mark as invoiced"/"Undo"; a manager sees all that apply to the status); add/edit modal incl. percent toggle hidden without a fixed price and field errors; mark-invoiced dialog sends reference/date/revision; reorder calls the position endpoint; no-financials and no-currency notes; line form budget fields and the Billing column; timeline texts.
- [ ] **Step 2–4** — implement; `bun run --cwd apps/projects/frontend test|typecheck|lint`, `translations:check`, `i18n:test`, `bunx biome check .`.
- [ ] **Step 5: Commit** `feat(projects-ui): the invoice plan on a new economy tab, and budgets on billing lines`.

### Task 4: Host integration and docs

**Files:** `routes/projects/$projectId.economy.tsx` (renders `ProjectEconomy`; mirror `$projectId.billing.tsx`), `-project-detail-layout.tsx` (Economy tab between Billing and Time, shown to everyone who sees the project — same gating as Billing; check what Billing does and copy it), catalogs en + nb (tab label), `routeTree.gen.ts` regenerated; tests (`project-detail-tabs.test.ts` gains the tab and its order; a route test like the billing one). `docs/projects.md`: a "Billing milestones and the invoice plan" section (model, effective amount and freezing, the move table and who may do what, guards, timeline events, API table) and line budgets in the billing-lines section; amend the "calculates no money" sentence — Projects now computes exactly one thing, a percent of the fixed price. `ROADMAP.md` phase 3: mark billing milestones and line budgets done, budget-vs-actual/portfolio/alerts next.

- [ ] **Step 1–4** — tests first, implement, `bun run --cwd apps/host/frontend test|typecheck|lint|build`, `frontend:lint`, `frontend:typecheck`, translations, biome.
- [ ] **Step 5: Commit** `feat(frontend): the economy tab on the project page` and `docs(projects): billing milestones, line budgets and the roadmap`.

### Task 5: Final gates and PR

- [ ] Final whole-branch review (most capable model), one fix wave, re-review.
- [ ] Regenerate `openapi/COVERAGE.md`; `go generate ./...` and `bun run gen:client` no drift; `go vet`; `golangci-lint`; full Go suite pinned to four CPUs; `frontend:lint|typecheck|test|build`; `translations:check`; `biome check .`; `mise run smoke`.
- [ ] Normalise commit trailers; push; `gh pr create` against `main`; body: summary, decisions made along the way, parked minors, verification; ends with the attribution line. Watch checks with Monitor parsing `gh pr checks` default output; fix on red; never merge.
