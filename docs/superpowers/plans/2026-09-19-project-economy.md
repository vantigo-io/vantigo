# Project Economy Implementation Plan (PR B: budget vs actual, portfolio, alerts)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show how a project is doing against its budget — logged time in three buckets (approved / submitted / draft) against the project's and the billing lines' budgets — on the Economy tab, in a cross-project portfolio, and as dashboard signals.

**Architecture:** Projects owns the view; Time supplies the numbers through a new optional provider contract, `contracts.ProjectActuals`, resolved by `module.Compose` exactly like `ProductCatalog` (nil when `time` is disabled). Projects never reads the `time` schema and Time never learns about budgets. Nothing is cached: actuals are read live, alerts are computed when the dashboard loads. One new permission, `projects:view-costs`. No migration.

**Tech Stack:** Go (pgx v5, sqlc, oapi-codegen strict server), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-19-project-economy-design.md` — delivery B: §2 E2–E4, E6–E9; §4; §5 "Delivery B" and "Stats"; §6; §7 (budget bar, per-line table, Time off, portfolio, dashboard); §8; §9. Delivery A (milestones, line budgets) is merged work this builds on — `docs/projects.md` describes it.

## Global Constraints

- **Pattern files:** contract slot: `internal/contracts/products.go`, `internal/module/{module.go,compose.go,compose_test.go}`, `internal/modtest/modtest.go` (`WithProducts`), how `projects` treats a nil `deps.Products`. Time side: `internal/time/{stats.go,directory usage,harness_test.go}`, `queries/stats.sql` (`ProjectHourGroups`, `ProjectBillingTotals` — the currency rule already lives there). Projects side: `internal/projects/{milestones.go,responses.go,authorize.go,stats.go,projects_list.go,harness_test.go}`. Frontend: `apps/projects/frontend/src/{pages/project-economy.tsx,api/milestones.ts,pages/projects.index.tsx}`, `apps/time/frontend/src/components` (the project time panel's bars, for look only — packages may not import each other). Host: `apps/host/frontend/src/{apps.ts,routes/projects/*,routes/dashboard.tsx,catalogs}`. **Siblings win over this plan's wording; say so in the report.**
- **Boundaries:** `internal/projects/**` imports no other module and no SQL crosses schemas — the ONLY way hours reach Projects is `deps.Actuals`. `internal/time/**` implements the contract from its own tables and must not call `deps.Projects` while serving it (Projects passes the currency in). Tests use fakes against `internal/contracts` (`modtest.WithActuals`).
- **No cross-module call under a lock:** `deps.Actuals` (like `deps.Products`/`deps.Users`) is never called inside a transaction that holds `LockProject`. The economy reads take no lock at all.
- **Buckets (E2):** `approved` = entry status approved + invoiced; `submitted` = submitted; `draft` = draft + rejected. Every surface shows the split.
- **Currency rule:** an entry's bill amount counts when the entry's currency equals the request's currency; otherwise its hours are `unpriced`. Same for cost with the cost's currency. With no currency in the request no amounts are summed and `unpriced` = hours without a bill rate. Hours always count. Exact arithmetic: hours as int64 hundredths, amounts as decimal text summed in SQL `numeric`; Projects does its ratios in `math/big.Rat`; JSON numbers only at the edge.
- **Budget used (E8), one function used everywhere:** basis = project `budgetAmount` → else `fixedPriceAmount` on a fixed-price project → else `budgetHours`; amount bases compare the three buckets' bill amount, the hours basis compares hours. A caller without financial rights only ever gets the hours basis (or nothing). `percent` and `approvedPercent` rounded half-up to one decimal. No basis → `budgetUsed` absent; such projects sort last. `overBudget` = percent > 100. Thresholds: warning `80 ≤ percent ≤ 100`, exceeded `> 100`.
- **Shaping:** hours for everyone who sees the project; bill amounts, budget amounts, fixed price, milestone totals need financial rights on the project (the module's existing rule); the `cost` block needs financial rights **and** `projects:view-costs`. Absent, never null or zero. Outsider → bare 404. A failing actuals call → 500 problem, never zeros; `deps.Actuals == nil` → `timeTracking: false` and no actuals.
- **Permission:** `projects:view-costs` — Display "View project costs", Description "See what the work costs the company and the margin, on projects whose financials you can see. On a small project this can reveal a person's cost rate.", Category "Projects", Sensitive true, Delegable true, in no default role (check how default roles are seeded and make sure it is in none).
- **Toolchain, contract coverage gate, tests-must-be-able-to-fail, i18n en + nb, calendar dates in UTC, commits with the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`, never `--no-verify`:** as in the workspace's environment notes.
- **Branch:** `feat/project-economy` (based on `feat/project-billing-milestones`, PR #102); never commit to `main`.

## File Structure

```
apps/server/internal/contracts/actuals.go            ProjectActuals, ActualsRequest, ActualsBucket, ActualsTotals, ProjectActualsEntry, LineActuals
apps/server/internal/module/{module.go,compose.go}   + Actuals slot, Deps.Actuals (resolved after Projects)
apps/server/internal/modtest/modtest.go              + WithActuals
apps/server/internal/time/actuals.go (+_test)        the provider; queries/actuals.sql; module.go registers it
apps/server/internal/projects/
  economy.go (+_test)            GET /projects/{id}/economy
  economy_math.go (+_internal_test)  budgetUsed, percent rounding, bucket sums — pure functions
  portfolio.go (+_test)          GET /projects/economy
  stats.go (+)                   readyMilestones, four attention types
  module.go (+)                  projects:view-costs
  queries/economy.sql            budgets, line budgets, task estimate sum, milestone reads for many projects
openapi/projects.yaml            + economy + portfolio operations, schemas, stats additions
apps/projects/frontend/src/
  api/economy.ts (+test), lib/economy.ts (bar maths, formatting)
  components/budget-bar.tsx (+test)   the three-segment bar with marker and overflow
  pages/project-economy.tsx (+)       budget half above the invoice plan; hours-only view; Time-off note
  pages/portfolio.tsx (+test)         the portfolio page
  i18n.ts (+), index.ts (+)
apps/host/frontend/src/
  routes/projects/economy.tsx, apps.ts (+ sidebar item), -project-detail-layout.tsx (tab for everyone),
  routes/dashboard.tsx (+ metric, four attention types), catalogs (+), routeTree.gen.ts
docs/projects.md, docs/time.md, docs/module-boundaries.md, ROADMAP.md
```

---

### Task 1: The actuals contract, the composition slot and Time's provider

**Files:** per File Structure (contracts, module, modtest, time). Also `internal/module/compose_test.go`, depguard config only if a new rule is needed (it should not be: both sides import `contracts` only).

**Contract** (spec §4, verbatim shapes): `ProjectActuals{Actuals(ctx, ActualsRequest) (ProjectActualsEntry, error); ActualsForProjects(ctx, []ActualsRequest) (map[int32]ActualsTotals, error)}`; `ActualsRequest{ProjectID int32; Currency *string}`; `ActualsBucket{HoursHundredths int64; BillAmount, CostAmount string}` (decimal text, `"0.00"` when nothing); `ActualsTotals{Approved, Submitted, Draft ActualsBucket; UnpricedHoursHundredths int64; LastEntryDate *string}` plus `BillableHoursHundredths`, `NonBillableHoursHundredths int64`; `ProjectActualsEntry{Totals; Lines []LineActuals}`; `LineActuals{BillingLineID *int32; Totals ActualsTotals}`. A project with no entries is present in the batch result with zero totals. Doc comments state: no authorization, the caller owns the currency fact, buckets per E2.

**Composition:** `Module.Actuals func(Deps) contracts.ProjectActuals`, `Deps.Actuals`, resolved in `compose` after `Projects` with `soleProvider(…, "project actuals", …)`; Compose's doc comment and error text updated; `compose_test.go` gains the duplicate-provider and the "nil when time is disabled" cases; `modtest.WithActuals(p)` mirrors `WithProducts` (overrides whatever a module provides).

**Time's provider** (`actuals.go`, `queries/actuals.sql`): one grouped query per call — `GROUP BY project_id, [billing_line_id,] bucket, bill currency, cost currency` with `SUM(hours)`, `SUM(hours*bill_rate)`, `SUM(hours*cost_rate)`, billable split, `MAX(entry_date)`; folding by the request's currency happens in Go over the grouped rows (so a project asked in NOK and entries in SEK yields unpriced hours). Look at how `ProjectBillingTotals`/`projectBilling` already expresses the entry-currency rule and reuse its column names. The provider is built from `Deps.Pool` only and **never touches `deps.Projects`**. Batch input of 0 requests → empty map without a query; cap the batch at 2 000 ids (error beyond — the caller caps too).

- [ ] **Step 1: Failing tests** — provider against real entries: every status lands in its bucket (incl. invoiced → approved, rejected → draft); per-line split incl. the nil line; currency match / mismatch / nil currency for bill and for cost; unpriced hours; billable split; last entry date; exact sums (e.g. 3 × 0.33 h at 333.33); batch with several projects incl. one without entries; no `deps.Projects` call (the harness fake fails the test if consulted). Compose tests. A test that Time registers the provider (`timetracking.Module().Actuals != nil`).
- [ ] **Step 2–4** — red, implement, green (`go test ./internal/contracts/... ./internal/module/... ./internal/modtest/... ./internal/time/...`, once under `taskset -c 0-3`), vet, lint, generate no drift.
- [ ] **Step 5: Commit** `feat(time): report what has been logged on projects through a new actuals contract`.

### Task 2: The project economy endpoint and `projects:view-costs`

**Files:** `economy.go`, `economy_math.go`, tests, `queries/economy.sql`, `module.go`, `responses.go`, `openapi/projects.yaml`, `harness_test.go` (a `fakeActuals` with settable totals, a failure switch, and a call log).

**Contract shapes:**

```
ProjectEconomyResponse {
  timeTracking bool, currency? string,
  budget { hours? number, linesHours? number,                       // planning data
           amount? number, fixedPrice? number, linesAmount? number }, // financial
  actuals? ProjectEconomyActuals,                                     // absent when timeTracking is false
  lines: [ { billingLineId? int32, code? string, active? bool, budgetHours? number, budgetAmount? number (fin),
             actuals? ProjectEconomyActuals, usedPercent? number, remainingHours? number, overBudget bool } ],
             // one row per billing line (active or not) + one row without billingLineId when hours were logged without a line
  taskEstimateHours? number,
  budgetUsed? { basis: "amount" | "fixedPrice" | "hours", percent number, approvedPercent number },
  overBudget bool,
  milestones? { planned, ready, invoiced number, currency?, unplanned?, overPlanned? },   // financial; same maths as the plan totals
  cost? { approved, submitted, draft, total number, margin number }                        // financial + projects:view-costs; margin = bill total − cost total
}
ProjectEconomyActuals { approved, submitted, draft: { hours number, amount? number (fin) },
                        totalHours number, totalAmount? number (fin), unpricedHours number,
                        billableHours number, nonBillableHours number, lastEntryDate? date }
```

`GET /projects/{id}/economy` (`permission:projects:access`): visible to everyone who sees the project; shaping per Global Constraints. Line rows carry `code` from the line itself (no catalog call — this read must not depend on Products). A line's `usedPercent` uses its `budgetAmount` (if the caller sees amounts and it is set) else its `budgetHours`. Task estimate = sum of `estimate_hours` over the project's not-deleted tasks (top-level and subtasks). `ProjectCapabilities` gains `canSeeCosts`.

- [ ] **Step 1: Failing tests** — pure functions first (`economy_math`: each basis, the fall-through order, hours-only for a caller without amounts, rounding at x.x5, no basis, thresholds 79.99 / 80 / 100 / 100.01, big.Rat not float); then the endpoint: shaping per caller kind on raw JSON (member: hours only, no amounts/milestones/cost; manager; viewer + `view-financials`; + `view-costs`; `view-costs` without financial rights sees no cost; outsider 404); three buckets and totals; per-line rows incl. inactive lines and the no-line row; line sums vs project budget; unpriced hours; Time absent (`timeTracking:false`, budgets + milestones still there); provider failing → 500; the currency passed to the provider is the project's; the provider is called exactly once and never inside a transaction; milestone totals equal the plan endpoint's.
- [ ] **Step 2–4**, **Step 5: Commit** `feat(projects): a project's economy — budget against logged time, behind the right permissions`.

### Task 3: The portfolio and the dashboard stats

**Files:** `portfolio.go`, tests, `stats.go`, `queries/economy.sql`, `openapi/projects.yaml`.

```
ProjectEconomyRow { project { id, code, name, status, customer? { id, name } }, currency?,
                    budgetUsed?, overBudget bool,
                    actuals? { approved, submitted, draft: { hours, amount? } , totalHours, totalAmount? }, pendingHours number,  // submitted + draft
                    nextMilestone? { id, name, plannedDate?, status, effectiveAmount?, overdue }, readyCount int32, readyAmount? number }
ProjectEconomyListResponse { data: [ProjectEconomyRow], pagination (the module's list shape),
                             totals { projectCount, overBudgetCount, readyCount int32, readyAmounts: [ { currency, amount } ] } }
```

`GET /projects/economy` (`permission:projects:access`): rows = projects the caller has **financial rights** on (a manager's own projects; all visible ones for `view-financials`; all for `manage-all`), filters `status` (default `active`; `all` allowed), `customerId`, `overBudget=true`, `hasReady=true`, `search` (code/name, the list's existing escaping); sort `budgetUsed` (default, desc, no-basis last), `readyAmount` desc, `nextMilestone` asc (none last), `code` asc; `page`/`pageSize` like the project list. Mechanics: read matching projects (cap 2 000 → 400 problem asking for a narrower filter), ONE `ActualsForProjects`, ONE grouped milestone read, compute/filter/sort/page in Go; totals cover the whole filtered set, not the page. Customer names resolve the way the project list does. "Next milestone" = the earliest-dated open (`planned`/`ready`) milestone, undated ones after dated, then by position.

**Stats:** `GET /stats/summary` gains `readyMilestones` (count the caller may see). `GET /stats/attention` gains `budgetWarning` and `budgetExceeded` (active projects where the caller holds the **manager role**; title = project name; entityId = project id; occurredAt = the last entry date at midnight UTC, else now), `milestoneReady` (callers with financial rights on the project; entityId = `<projectId>/<milestoneId>`; title = milestone name; occurredAt = `ready_at`), `milestoneOverdue` (`planned`, date passed; the project's managers; occurredAt = planned date). Cancelled and completed projects raise nothing. Without Time, budget items are simply absent. The existing overdue-project item is unchanged.

- [ ] **Step 1: Failing tests** — row visibility per caller kind (never a row without financial rights; totals only over visible rows); each filter; each sort incl. no-basis-last and ties by code; paging + totals over the filtered set; the cap; one provider call for the page (call log); Time absent; next-milestone choice; attention items per recipient rule and at the threshold boundaries; `readyMilestones`.
- [ ] **Step 2–4**, **Step 5: Commit** `feat(projects): an economy portfolio across projects, and budget and invoicing signals for the dashboard`.

### Task 4: Frontend — budget bar, the Economy tab's budget half, the portfolio page

**Files:** per File Structure.

- `components/budget-bar.tsx`: one stacked bar — approved solid, submitted lighter, draft hatched (CSS pattern, not colour alone) — a budget marker at 100 %, overflow past it in red, `role="img"` with a full-sentence `aria-label` ("312 of 400 hours: 210 approved, 62 submitted, 40 draft"), and a visible legend with hours and (when present) amount per segment. A `size="sm"` variant for table rows. No chart dependency.
- `pages/project-economy.tsx`: above the invoice plan — headline figures (budget used with its basis named; value of work beside the fixed price; ready to invoice; margin when `cost` is present), the bar, the task-estimate line, the unpriced note, the "lines add up to … of the project's …" sentence, the per-line table (code, budget, mini bar, used %, remaining, over-budget badge; no-line row; inactive lines dimmed). Callers without financial rights now get the hours view (bar in hours, per-line hours) instead of the locked note; the invoice plan section stays gated as it is. `timeTracking:false` → the note "Hours appear here when Time tracking is enabled" in place of the bars. The plan and economy queries both live under `["projects", …]`.
- `pages/portfolio.tsx` (`EconomyPortfolio`, reads the router itself like `projects.index.tsx`): table per the row shape, filter chips (active default / all statuses, over budget, has ready milestones, customer picker as the list page does it), sort control, totals line (ready amounts per currency, over-budget count), URL-held `status`, `customerId`, `overBudget`, `hasReady`, `sort`, `page`, `search`; row → the project's Economy tab; empty state explaining that projects whose financials you can see appear here.
- en + nb for everything; exports `EconomyPortfolio`, the search-param validator the host needs, and whatever the dashboard needs for titles stays in the host.

- [ ] **Step 1: Failing tests** — bar: each segment combination, overflow, marker, aria-label, legend amounts absent without amounts; tab: per permission level (absent not zero), basis naming, per-line rows, Time-off note, hours-only view; portfolio: rows, filters/sort/page in the URL, totals, empty state, row link.
- [ ] **Step 2–4**, **Step 5: Commit** `feat(projects-ui): budget against logged time on the economy tab, and an economy portfolio`.

### Task 5: Host integration and docs

Economy tab shown to everyone who sees the project (drop delivery A's capability gate; keep order Billing → Economy → Time); route `routes/projects/economy.tsx` with the package's search validator; sidebar item "Economy" in the Projects app (`projects:access` — the page's empty state covers callers with nothing to see; say so in the report); dashboard: Projects card metric from `readyMilestones` (hidden at 0), the four attention types with en + nb titles and links to `/projects/<id>/economy` (parse `milestoneReady`'s `<projectId>/<milestoneId>`); permission label/description for `projects:view-costs` in the host's permission catalog (en + nb); `routeTree.gen.ts`. Docs: `docs/projects.md` (Economy: budget used, buckets, shaping table, portfolio, alerts, the actuals dependency and what happens without Time), `docs/time.md` (the provider it implements), `docs/module-boundaries.md` (the new sanctioned read and its direction), `ROADMAP.md` (phase 3 first delivery done; next: expenses, overtime multipliers). Fact-check every sentence against the code.

- [ ] Tests first (tab visible to a member; portfolio route + guard; dashboard hrefs/titles for the four types; metric hidden at 0), implement, host `test|typecheck|lint|build`, root gates.
- [ ] **Commit** `feat(frontend): the economy portfolio, budget signals on the dashboard` and `docs: project economy — what it shows, who sees it, where the hours come from`.

### Task 6: Final gates and PR

- [ ] Final whole-branch review (backend / frontend+docs), one fix wave, re-review.
- [ ] `openapi/COVERAGE.md`; generate + `gen:client` no drift; vet; lint; full Go suite on four CPUs; all frontend gates; `mise run smoke`.
- [ ] Rebase onto `main` once #102 has merged (or note the stacking in the PR); normalise trailers; push; `gh pr create`; watch checks; fix on red; never merge.
