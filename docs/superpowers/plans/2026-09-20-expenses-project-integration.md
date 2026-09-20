# Expenses on the Project Page Implementation Plan (Expenses, PR C)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A project shows what its expenses cost and what they pass on to the customer: an **Expenses tab** on the project page with "Record a cost", a **Costs section**, a margin that includes expenses and a "ready to invoice" that includes billable expense lines on the **Economy tab**, and the portfolio's **ready column** — when both modules are enabled, and nothing at all when one of them is not.

**Architecture:** A second optional provider contract, `contracts.ProjectExpenses`, the mirror of `contracts.ProjectActuals`: Expenses *provides* it, Projects *consumes* it, `module.Compose` resolves it before any mount, and it is nil when `expenses` is disabled. The provider reports facts **per currency** and performs no authorization; Projects decides which currency is the project's, shapes by its own permissions and rounds nothing twice. Expenses' own API gains `GET /projects/{projectId}/summary` and a `toInvoice` filter for the tab, which lives in `@vantigo/expenses-ui` and is mounted by the host on the project page — the pattern the Time tab uses. No app's UI package imports another's.

**Tech Stack:** as PR A/B — Go (pgx v5, sqlc, oapi-codegen strict server), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-19-expenses-design.md` — §1 (delivery C), §2 X1, X2, X5, X7, **X12**, §4 "Currency", §5, §6 row "Per project (C)", §7 "Project page (C)". And `docs/superpowers/specs/2026-09-19-project-economy-design.md` E6 ("a later expenses module becomes a second source for the same view"), E7 (`projects:view-costs`). The modules as built: `docs/expenses.md`, `docs/projects.md`, `docs/module-boundaries.md`.

## Global Constraints

- **Both modules stay optional.** Projects without Expenses looks and answers exactly as today except for one new required boolean `expenseTracking: false` (the twin of `timeTracking`); never zeroes standing in for "not enabled". Expenses without Projects is unchanged (`GET /projects/{projectId}/summary` → 404, like `GET /projects`). Every behaviour is tested in the combinations that exist for it.
- **X12 — expenses never eat the budget.** Nothing from the expenses contract reaches `budgetUsed`, `overBudget`, `usedPercent`, the per-line budget table, the dashboard's budget alerts or `loggedWork`. Expenses appear in: the Costs section, the margin, "ready to invoice".
- **Currency (spec §4):** a line counts towards a project's figures only when its currency is the project's; otherwise it is reported as "in another currency" — per currency, with its own count and amounts — never dropped and never converted. A project without a currency reports every currency that way and computes no margin contribution.
- **The unit's status decides the bucket.** A claim's line is judged through its claim: `COALESCE(claim.status, entry.status)`. Buckets are `approved`, `submitted`, `draft` — `rejected` counts as draft (check what Time's provider does with rejected and mirror it; say so in the contract's doc comment). It must be clear in every UI what is approved and what is not.
- **What an expense costs and bills:** cost = net = gross − VAT (mileage and per diem carry no VAT), whoever paid (employee or company). Bill = `bill_amount` of `billable` lines; a billable line without a bill amount (e.g. mileage with no customer rate) is counted as **unpriced**, never as zero. Per diem is never billable. **Ready to invoice** = unit approved ∧ `billable` ∧ `bill_amount` present ∧ `invoiced_at IS NULL`. **Invoiced** = `invoiced_at` set.
- **Money across the boundary is decimal text**, two decimals, half away from zero, `"0.00"` for nothing, exactly as `contracts.ActualsBucket`; each published figure is rounded once from the unrounded sum and a `Total` is carried — consumers never add buckets. Projects' margin = (value of work + expense bill total) − (labour cost + expense cost total), each side rounded once from exact values (`projects/economy_math.go` helpers).
- **The provider performs no authorization and calls nobody** — in particular never `contracts.ProjectDirectory` while serving (a request-time module cycle). An error means "could not be read", never "there are none". At most `contracts.MaxActualsRequests` project ids per call (reuse the constant; do not invent a second cap); the same id twice is an error.
- **Who sees what, Projects side:** the `expenses` object on the economy response needs `CanSeeFinancials` (as amounts do today); expense *cost* is not labour cost and needs no `projects:view-costs`, but the **margin** still does (it contains labour cost). Outsiders keep their 404. **Expenses side:** `GET /projects/{projectId}/summary` is for whoever has financial rights on the project (`seesProjectFinancials`: manager, `projects:manage-all`, or `projects:view-financials` on a visible project) — aggregates only; anyone else 404. The visibility of individual expenses does **not** widen (spec §5): the tab's list shows what the caller may already see.
- **Contract calls from Projects** go through `projects/contracts.go` accessors wrapped in `noteContractCall` and never run under `withProjectLock` (the harness-wide check fails the test otherwise). Expenses' new reads take no locks.
- **Everything in `docs/expenses.md`, `docs/projects.md` and `docs/module-boundaries.md` still holds**: 404/403/400 rule, absent-not-null, contract-first with the coverage gate, depguard isolation both ways, the cross-schema scan, exact decimals, en + nb with parity, a11y conventions (named tables, row controls named after their row, nothing by colour alone), tests that can fail (say which you saw red / which mutations you ran), the fetch-level fakes must model every server rule the UI relies on.
- **Toolchain:** per the workspace's environment notes — test DB only through `TEST_DATABASE_URL` on 127.0.0.1:55442, race detector on what you touched, a contract change also runs `frontend:typecheck` and `gen:client`, commits end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`, never `--no-verify`, stage by explicit path and commit without a pathspec.
- **Branch:** `feat/expenses-project-integration` (from `main`); the PR opens against `main`.

## File Structure

```
apps/server/internal/contracts/expenses.go          ProjectExpenses, ProjectExpenseTotals, CurrencyExpenses, ExpenseBucket
apps/server/internal/module/{module.go,compose.go}  Deps.Expenses, Module.Expenses, soleProvider("project expenses")
apps/server/internal/modtest/modtest.go             WithExpenses
apps/server/internal/expenses/
  projectexpenses.go, queries/projectexpenses.sql   the provider (one grouped query) + the summary handler's source
  projectsummary.go                                  GET /projects/{projectId}/summary
  entries.go, queries/entries.sql (+)               the toInvoice filter
apps/server/internal/projects/
  contracts.go (+)                                   expensesForProjects accessor
  economy.go, economy_math.go, portfolio.go (+)      Costs, margin, ready
openapi/expenses.yaml, openapi/projects.yaml
apps/projects/frontend/src/pages/{project-economy,portfolio}.tsx (+ api, test fake, i18n)
apps/expenses/frontend/src/components/project-expenses-panel.tsx (+ api/project-summary.ts, fake, i18n)
apps/host/frontend/src/routes/projects/{-project-detail-layout.tsx, $projectId.expenses.tsx, -project-expenses-tab.tsx}
docs/{expenses,projects,time,module-boundaries}.md, ROADMAP.md, openapi/COVERAGE.md
```

---

### Task 1: The contract and its provider

`contracts/expenses.go`:

```go
// MaxExpensesProjects is MaxActualsRequests: one cap for the portfolio's two batches.
const MaxExpensesProjects = MaxActualsRequests

type ProjectExpenses interface {
	// ExpensesForProjects reports, per project and per currency, what the
	// project's expenses cost and bill. A project with nothing recorded is absent.
	ExpensesForProjects(ctx context.Context, projectIDs []int32) (map[int32]ProjectExpenseTotals, error)
}

type ProjectExpenseTotals struct {
	Currencies    []CurrencyExpenses // by currency code ascending
	LastEntryDate *string            // YYYY-MM-DD, over every line
}

type CurrencyExpenses struct {
	Currency                          string
	Approved, Submitted, Draft, Total ExpenseBucket
	ReadyCount                        int64  // approved, billable, priced, not invoiced
	ReadyAmount                       string
	InvoicedCount                     int64
	InvoicedAmount                    string
	UnpricedCount                     int64  // billable without a bill amount, any status
}

type ExpenseBucket struct {
	Count      int64  // lines
	CostAmount string // net
	BillAmount string // billable lines' bill amounts
}
```

Wiring exactly as `Actuals`: `Deps.Expenses`, `Module.Expenses func(Deps) contracts.ProjectExpenses`, `soleProvider(mods, "project expenses", …)` resolved with the other provider slots before any mount, `modtest.WithExpenses`, compose tests (nil when disabled; two providers is an error). Provider in `expenses/projectexpenses.go` holding only the pool: ONE grouped query (`project_id`, `currency`, unit-status bucket via the claims LEFT JOIN, with the sums unrounded as numeric text and the counts), summed with `big.Rat`, rounded once per published figure. Guards: more than the cap → error; duplicate id → error; empty input → empty map without a query. Check the query plan against `ix_entries_project_id_entry_date`; add an index only if the plan at a realistic size needs one (say what you measured).

- [ ] **Failing tests first**: compose (slot nil / set / duplicate provider); provider table — statuses incl. a claim's lines judged through the claim and `rejected` as draft; company-paid and employee-paid both cost; VAT reduces cost; mileage and per diem cost gross; billable outlay with markup; billable mileage without a customer rate → unpriced, not zero; ready vs invoiced; two currencies on one project; rounding (three lines of 0.005 → Total "0.02" while buckets say what they say — mirror Time's test); absent project; cap; duplicate; `LastEntryDate`; the provider makes no contract call (harness check).
- [ ] Implement; docs: `docs/module-boundaries.md` (slot list, optional contracts, per-module table); gates; **Commit** `feat(expenses): what a project's expenses cost and bill, as a contract Projects may read`.

### Task 2: Expenses' own per-project reads

`GET /projects/{projectId}/summary` → `ExpensesProjectSummaryResponse { currencies: [{currency, approved, submitted, draft, total: {count, cost, billAmount}, readyCount, readyAmount, invoicedCount, invoicedAmount, unpricedCount}], lastEntryDate?, capabilities: {canRecord} }` — the same source as the provider (one function, two callers). 404 without Projects, for a project the directory does not know, and for a caller without financial rights on it (one bare 404); the role is asked before any query. `canRecord` = the caller may book on the project (`CanLogTime`). `GET /entries` gains `toInvoice=true` (requires `projectId`, 400 otherwise): unit approved ∧ billable ∧ priced ∧ not invoiced, through the claim — and say in the report whether `ix_entries_to_invoice` (whose predicate reads the entry's own `status`) serves claim lines; if it cannot, replace it in a new migration `00014` with one that does, pinned in `schema_test.go`.

- [ ] **Failing tests first**: the caller matrix (manager, manage-all, view-financials on a visible / an invisible project, plain member, approver, `expenses:manage` without project rights → 404); Projects off → 404; figures equal the provider's for the same fixture; `toInvoice` incl. a claim's approved billable line and excluding a per diem; without `projectId` → 400; contract coverage.
- [ ] Implement; `docs/expenses.md`; gates incl. `frontend:typecheck`; **Commit** `feat(expenses): a project's expenses in sum, and the lines waiting to be invoiced`.

### Task 3: Projects reads them — economy, margin, ready, portfolio

`projects/contracts.go`: `expensesForProjects(ctx, ids)` (noted, never under the lock). `GET /projects/{id}/economy`: `expenseTracking` (required boolean); with `CanSeeFinancials` and the contract present, `expenses: { approved, submitted, draft: {count, cost, amount}, totalCost, totalAmount, readyCount, readyAmount, invoicedCount, invoicedAmount, unpricedCount, otherCurrencies: [{currency, count, cost, amount, readyAmount}], lastEntryDate }` — the main figures only in the project's currency, absent when the project has none; `otherCurrencies` absent when empty. `cost.margin` (still `projects:view-costs`) includes expenses per Global Constraints, and `cost` gains `expenseCost` so the UI can show the two halves without subtracting. A provider error is a 500, as for actuals. Portfolio: a second batch over the same capped id set; row gains `readyExpenseCount`, `readyExpenseAmount`, `readyTotalAmount` (milestones + expenses, rounded once; `readyAmount` keeps meaning milestones); totals' `readyAmounts[]` items gain `expenseAmount` and `totalAmount`, `readyCount` stays milestones and `readyExpenseCount` is added; the `ready` sort orders by the total. The dashboard stats stay untouched (assert it).

- [ ] **Failing tests first** (`fakeExpenses` beside `fakeActuals`: set / fail / asked): the four module combinations; X12 — a project over budget only if expenses were (wrongly) counted stays under; margin arithmetic incl. rounding once; other-currency reporting and a project without a currency; shaping per caller (member, financials without view-costs, with view-costs, outsider 404); provider error → 500; portfolio batch asked once with the capped set, sort by total, totals per currency; no contract call under the project lock.
- [ ] Implement; `docs/projects.md` (the sentence calling `ProjectActuals` "the first Projects consumes" included); gates incl. `frontend:typecheck`; **Commit** `feat(projects): a project's economy counts its expenses — costs, margin and what is ready to invoice`.

### Task 4: Projects UI — the Costs section and the ready column

`@vantigo/projects-ui`: Economy tab — a **Costs** section (expenses: approved / awaiting approval / draft with counts, clearly labelled; total cost; passed on to the customer; unpriced note; "in another currency" note per currency; nothing when `expenseTracking` is false, a plain sentence when true and empty), the headline margin's explanation naming expenses, the invoice plan's "Billable expenses ready to invoice" row with count and amount (and an optional `expensesHref` prop/link the host supplies — the package imports nothing from Expenses). Portfolio: the ready column shows the total with the split beneath ("milestones … · expenses …"), the KPI uses `totalAmount`. en + nb; the fake models `expenseTracking` and both shapes.

- [ ] Tests first; implement; package + host gates; **Commit** `feat(projects-ui): costs, margin and ready-to-invoice with expenses in them`.

### Task 5: Expenses UI on the project page — the tab, "Record a cost", docs

`@vantigo/expenses-ui` exports `ProjectExpensesPanel({ projectId })`: the summary cards per currency (from `/projects/{id}/summary`; hidden on its 404), filter chips All / Ready to invoice (`toInvoice=true`), the list of the project's expenses the caller may see (existing table + drawer incl. mark / undo invoiced and pricing), a sentence when the totals cover more than the list shows, and **Record a cost** — the expense form with the project fixed, kind outlay, "the company paid" preselected — offered when `capabilities.canRecord` (or, without the summary, when the project is among `GET /projects` options). Host: tab "Expenses" / "Utlegg" on the project page gated `module: "expenses"`, `requiredPermissions: ["expenses:access"]`, route `/projects/$projectId/expenses` with the `ModuleNotEnabledPage` fallback, and `expensesHref` passed to the Economy tab. Docs: `docs/expenses.md`, `docs/projects.md`, `docs/time.md` where it lists contracts, `ROADMAP.md` (expenses' three deliveries done; what is next).

- [ ] Tests first; implement; all frontend + host gates; **Commits** `feat(expenses-ui): a project's expenses, and a cost recorded from its page`, `feat(frontend): the project page's Expenses tab`, `docs: expenses on the project page`.

### Task 6: Final reviews, gates, PR

Whole-branch backend review and frontend + docs review on the most capable model (compare each fake with the Go it stands for); one fix wave each; spec amended to what was built; `openapi/COVERAGE.md` regenerated; every gate incl. the whole Go suite under `-race` on four CPUs and `mise run smoke`; uniform trailers; push; PR against `main` listing every decision and parked item; watch CI; never merge.
