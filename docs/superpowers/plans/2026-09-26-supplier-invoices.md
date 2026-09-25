# Supplier invoices (Projects phase 3, delivery C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The invoice a supplier sends for work or goods on a project becomes a thing of its own inside Expenses: a **supplier invoice** (`kind = 'supplier_invoice'`) with the supplier, the supplier's invoice number, the invoice date (the entry date) and the due date, its PDF attached, attested through the module's own flow, priced and re-billed with the outlay's markup, never owed to anybody, visible to the project's financial side, and counted apart as a sub-figure in the project's economy. One migration (`00033`: two columns and the SQL function `expenses.owes_employee`), no new operation, one additive contract field (`contracts.CurrencyExpenses.SupplierInvoices *ExpenseSplit`), additive optional fields on existing schemas only (`ExpensesEntryRequest`, `ExpensesEntryUpdateRequest`, `ExpensesEntryResponse`, `GET /entries`' and `GET /projects`' parameters, `ExpensesProjectSummaryCapabilities`, `ExpensesProjectSummaryCurrency`, `ExpensesProjectSummaryResponse`, `ProjectEconomyExpenses`) and two new schemas (`ExpensesProjectSummarySupplierInvoices`, `ProjectEconomySupplierInvoices`).

**Architecture:** Migration `00033_expenses_supplier_invoices.sql` adds `supplier_invoice_number varchar(100)` and `supplier_due_date date` to `expenses.entries` and the `IMMUTABLE` function `expenses.owes_employee(kind text, paid_by text)`; the thirteen copies of the owes-the-employee predicate in `queries/{reimbursements,stats,claims}.sql` call it, and Go's `owesEmployee` (`authorize.go`) stays its mirror, a test holding the two against each other on every (kind, paid_by) pair. `entries_validation.go` learns the fourth kind (`parseSupplierInvoice` over the outlay's money rules, now shared as `parseAmounts`; supplier and invoice number required, due date on or after the entry date, `paid_by` fixed to `company`, refused in a claim, without a project and without the projects module), `entries.go` gates the recorder on **financial rights** instead of `CanLogTime` (`checkSupplierInvoiceProject`, reading `Project` and the caller's role before any transaction; a cancelled project refused, a completed one accepted) and prices it on the outlay's branch; receipts, the kind-change rule and the approval queue's missing-receipt count learn it (`takesReceipts`), and the submit refuses one without a document (`supplierInvoiceDocumentRefusal`). Visibility widens for this kind only: `accessFor` and the list's SQL predicate admit financial-rights holders on the invoice's project (`financialProjects`, `@supplier_invoices_all`, `@financial_project_ids`). `ProjectExpenseGroups` groups by a `supplier_invoice` flag and the fold carries a per-currency `SupplierInvoices *ExpenseSplit` (nil when none) while every existing figure still means "everything"; the project summary renders it as `supplierInvoices`, answers `capabilities.canRecordSupplierInvoice` and, when true, the `project` as a booking option; `GET /projects?kind=supplier_invoice` is the picker for the Expenses app's own form. Projects' economy shapes the sub-figure into `expenses.supplierInvoices` (own currency, absent when none). The Expenses frontend gains the kind (form, fields, payload, receipts hint, list filter and labels, drawer with number, due date and an "overdue" tag, approval tables, the project page's **Record a supplier invoice** button and the currency cards' "of which supplier invoices" line); the Projects frontend's Costs section gains the same line. en + nb.

**Tech Stack:** Go 1.27 (pgx, sqlc 1.31.1, goose, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-26-supplier-invoices-design.md` (D1–D6 + "Out of scope" + "Testing"). Read it first; it is binding. Research with file:line pointers: `.superpowers/sdd/2026-09-26-supplier-invoices/context-for-design.md` — trust it, but read the code it points at before writing code against it. The shapes this delivery copies: `apps/server/internal/expenses/entries_validation.go` (`parseOutlay`, `optionalText`, `notOnKind`), `expenses/entries.go:143-212` (`checkProject`, the directory reads before any transaction), `expenses/authorize.go:180-213,584-647,730-741` (`managedProjects`, `accessFor`, `owesEmployee`), `expenses/flow.go:785-825` (`receiptRefusal`), `expenses/projectexpenses.go` (the exact fold), `expenses/projectsummary.go`, `projects/economy.go:494-531` + `projects/economy_math.go:394-511` (shaping by absence), `apps/expenses/frontend/src/pages/-expense-form-modal.tsx`, `components/project-expenses-panel.tsx`, `components/entry-details.tsx`, `apps/projects/frontend/src/pages/project-economy.tsx:378-533`.

## Global Constraints

- Branch `feat/project-supplier-invoices` (HEAD is the committed spec, `eb97f087`). Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'` for every `go test`. Port 55432 belongs to another project — never touch it.
- The untracked `go.mod`/`go.sum` in the repo root are not ours: never add, edit or delete them.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. Concurrent agents share ONE index: never commit a path you did not change.
- Commit messages: Conventional Commits scoped `expenses` / `projects` / `contracts` / `expenses-ui` / `projects-ui` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- After any `openapi/*.yaml` or `queries/*.sql` or migration change: `cd apps/server && mise exec -- go generate ./...` — **never package-scoped**; a second run must show no new diff — then `mise exec -- go test -count=1 ./internal/openapi/...`, then from the repo root `mise exec -- bun run gen:client`, then regenerate `openapi/COVERAGE.md` with `cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`. Commit every generated file (`apps/server/internal/openapi/specs/*.yaml`, `internal/<module>/gen/api.gen.go`, every file `git status` shows under `internal/<module>/store/`, each changed `api-schema.d.ts`, and `openapi/COVERAGE.md` if it moved).
- **Frozen corpus:** never edit `openapi/testdata/exchanges/*.jsonl`. Neither expenses nor projects has a corpus file (`portedModules` in `internal/openapi/exchanges_test.go`); both are held to `contracttest.RequireCoverage` in their `main_test.go` instead, which requires every operation to answer 2xx in the module's own tests. This delivery adds no operation. Changes to EXISTING schemas are additive and optional only — never add to an existing `required:` list. New schemas may have required fields. New request fields and parameters are plain optional values validated in Go with this module's wording, never yaml `enum:`/`minimum:`/`maximum:`/`maxLength:`.
- After any migration: add it to the owning module's `sqlc.yaml` `schema:` list (checked by `TestSqlcSchemaListsOnlyTheModulesOwnMigrations`) and pin it in `apps/server/internal/db/schema_test.go`. After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes.
- **No directory or object-store call inside a locked transaction** — the expenses harness enforces it on every test (`lockedContractCalls`, installed from `TestMain`); projects' harness does the same for its providers. Every `Project`/`Role`/`ProjectsForUser`/`BillingLines` read this delivery adds happens in `prepare`, a handler preamble or a list preamble, before `withLockedTx` opens; every attachment read in the submit is this module's own table under the row lock.
- **Exact decimal** via `math/big.Rat` / `pgtype.Numeric` / SQL `numeric`, never float arithmetic on money. The supplier split is summed in SQL and folded in `big.Rat`, rounded once per published figure.
- Money is shaped **absent, not null**, per caller: `billing` stays behind `canSeeBilling`; `supplierInvoices` is absent when a currency has none; the economy's block stays behind financial rights.
- **One refusal rule:** 404 for what the caller may not see (a bare body where the module answers bare), 403 for what is not theirs to do, 400 naming the field and the state. A supplier invoice's recorder gate is a 400 on `projectId` with one sentence for "no such project" and "no financial rights", and the cancelled-project sentence only for a caller who holds the rights.
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style.
- **Frontend rules:** omitted → absent at the api boundary, never trusted deeper in; at least one fixture per resource is a wire literal — the body the server sends; every new UI string in both catalogs (`en` + `nb`) of the package's `src/i18n.ts`, and `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` pass; a Mantine `Select` is a `combobox` in tests; **never assert "the last fetch"** — filter `fetchMock.actualCalls` by method/URL (`sent(fetchMock, "POST")` in the expenses package, `actualCalls.find(...)` in projects); the fetch fake must model every server rule a test leans on (the lesson in `expenses-module-followups.md`: a fake that does not model a rule hides the UI bug); tests run with `mise exec -- bun run --cwd <pkg> test`; run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.
- Every new test must be shown able to fail (remove the guard, see red, restore). Say so in the report.
- **One implementer commits at a time.** If two agents ever share the tree, the second writes and verifies but does not commit; the controller commits by pathspec.

**Parallelism.** Tasks 1 → 2 → 3 → 4 → 5 are sequential (each builds on the previous one's generated code or contract). Task 7 (expenses frontend) needs only Tasks 2 and 3's generated `apps/expenses/frontend/src/api-schema.d.ts`, so it may run beside Tasks 4–6. Task 8 (projects frontend) needs only Task 4's generated `apps/projects/frontend/src/api-schema.d.ts`, so it may run beside Tasks 5–7. Task 6 (docs) touches only `docs/` and `ROADMAP.md` and may run beside Tasks 5, 7 and 8 once Task 4 is in. The trees are disjoint (`apps/server` / `docs`+`ROADMAP.md` / `apps/expenses/frontend` / `apps/projects/frontend`); the one-committer rule above still holds.

---

**How this plan reads the spec where it leaves a choice open.** Each is on the record for the user's verdict (Task 9 Step 4 repeats them):

1. **The predicate is written thirteen times in SQL, not fourteen.** `grep -n "kind = 'outlay'" apps/server/internal/expenses/queries/*.sql` finds fourteen lines, but `claims.sql:214` is `ClaimAttentionCounts`' receipts-missing count (`e.kind = 'outlay' AND NOT EXISTS (… attachments …)`), not the owes-the-employee rule. The thirteen are `reimbursements.sql:26,39,63,77,100,118,153,189,237`, `stats.sql:52,173,182` and `claims.sql:127`. `claims.sql:214` stays as it is: a travel claim never holds a supplier invoice, so its receipt count has nothing to learn. "The count of places drops from fifteen to two" reads "from fourteen to two".
2. **The recorder gate is the caller's financial rights, not the owner's.** `seesProjectFinancials` reads the caller's own permissions and role, so "financial rights on the project" can only be asked of whoever is making the request. `expenses:manage` recording one for a colleague therefore needs financial rights too (the colleague becomes the owner; the right to book this kind is the recorder's). A kept project is not judged again — but only when the row being replaced is itself a supplier invoice: an outlay turned into one is a new booking of this kind, and a supplier invoice turned into an outlay is judged by `CanLogTime` in full.
3. **"Only a cancelled project refuses"** is read literally: planned, active, on-hold and completed all take a supplier invoice. The refusal sentence names the state and is said only to a caller who holds financial rights; everyone else, and an unknown project, gets `This project is not one you can record a supplier invoice on`.
4. **"Refused outright without the projects module"** is a 400 on `kind` (`A supplier invoice is booked on a project`) — the fact is the kind's, and `projectId` is already refused on its own field by decision X2 in that installation.
5. **The document rule's message** is `Expense <id> cannot be submitted yet: Attach the supplier's invoice`, in `entryIds` like every submit refusal; the frontend's hint is the bare `Attach the supplier's invoice`.
6. **`canRecordSupplierInvoice` is optional in the contract and always present in practice** (additive-only rule: `ExpensesProjectSummaryCapabilities.required` is never extended). It is `project.status != "cancelled"`, because the summary answers only a caller who already holds financial rights.
7. **The summary also answers `project`** — an `ExpensesProjectOption` (code, name, currency, active billing lines) — whenever `canRecordSupplierInvoice` is true. D5's button opens the form on a fixed project, and the form needs the project's code, name and lines; `GET /projects` offers only what the caller may *log time* on, which for the finance reader D2 exists for is nothing. Without this field the button could never open.
8. **`GET /projects?kind=supplier_invoice`** is the picker for the Expenses app's own form: the caller's own projects on which they hold financial rights and that are not cancelled. `ProjectsForUser` lists only projects the caller holds a role on, so a `projects:manage-all` holder on no team gets an empty picker there and records from the project page (Point 7). With `userId` naming somebody else it is a 400 on `kind` (the right is the recorder's, Point 2); `kind=outlay`/`mileage` or absent is today's picker; any other value is a 400.
9. **The kind control on the project page.** "Record a supplier invoice" opens the form with the kind fixed (no kind control), and "Record a cost" no longer offers the supplier invoice either — each button books its own kind. The Expenses app's own "New expense" offers it beside Outlay and Mileage when projects are available. Once saved, the kind control stays disabled as today (the server allows outlay ↔ supplier invoice on a draft, D2; the UI keeps its one-kind-per-saved-line rule).
10. **"Subcontractor" is found by name**, case-insensitively, as `Subcontractor` or `Underleverandør` among the active categories; when neither exists nothing is preselected. Category names are free text an administrator may change.
11. **"Overdue"** is shown when the invoice has a due date before today (the reader's own calendar day) and its `billing.invoice` stamp is not there. A caller who cannot see billing sees the tag on the due date alone — which in practice is nobody: the recorder holds financial rights, and so does everyone D4 shows the row to, except the owner-by-`expenses:manage` case.
12. **The payroll-facing figures are untouched by construction**: `paid_by` is stored `company` on every supplier invoice *and* the function answers false for the kind whatever `paid_by` says, so a hand-written row cannot reach payroll either.
13. **The Down migration** turns every supplier invoice into the company-paid outlay it would have been before (`kind = 'outlay'`; `paid_by` is already `company`), then drops the function and the two columns — so a rolled-back installation's inline predicate still owes nobody for those rows.
14. **The "you can see totals, not rows" note** keeps its logic (it is computed from the counts, and supplier-invoice rows now count as rows the reader is shown); its wording learns the widening.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00033_expenses_supplier_invoices.sql`, `internal/expenses/sqlc.yaml`, `internal/db/schema_test.go` | the two columns and `expenses.owes_employee` (Task 1) |
| `apps/server/internal/expenses/queries/reimbursements.sql`, `queries/stats.sql`, `queries/claims.sql` (+ every generated file under `internal/expenses/store/`) | the thirteen predicates call the function (Task 1) |
| `apps/server/internal/expenses/authorize.go`, `entries_validation.go`, `export_test.go`, `owes_employee_test.go` | the Go mirror and its test (Task 1) |
| `openapi/expenses.yaml` (+ generated `internal/openapi/specs/expenses.yaml`, `internal/expenses/gen/api.gen.go`, `apps/expenses/frontend/src/api-schema.d.ts`) | `invoiceNumber`, `dueDate`, the kind's descriptions, `GET /projects?kind`, `canRecordSupplierInvoice`, `project` (Task 2); `supplierInvoices` on the summary currency (Task 3) |
| `apps/server/internal/expenses/entries_validation.go`, `entries.go`, `queries/entries.sql`, `authorize.go`, `billing.go`, `attachments.go`, `approvals.go`, `flow.go`, `responses.go`, `projectoptions.go`, `projectsummary.go` | the kind, its gate, its pricing, its documents, its visibility, its picker and its summary capability (Task 2) |
| `apps/server/internal/expenses/harness_test.go`, `supplier_invoices_test.go` | the kind's tests (Task 2) |
| `apps/server/internal/contracts/expenses.go`, `internal/expenses/queries/projectexpenses.sql`, `projectexpenses.go`, `projectsummary.go`, `projectexpenses_test.go`, `projectsummary_test.go`, `harness_test.go` | `ExpenseSplit`, the grouped flag, the fold, the summary line (Task 3) |
| `openapi/projects.yaml` (+ generated `internal/openapi/specs/projects.yaml`, `internal/projects/gen/api.gen.go`, `apps/projects/frontend/src/api-schema.d.ts`), `apps/server/internal/projects/economy.go`, `economy_math.go`, `harness_test.go`, `economy_expenses_test.go` | the economy's `expenses.supplierInvoices` (Task 4) |
| `apps/server/internal/integration/supplier_invoices_test.go` | projects + expenses composed for real (Task 5) |
| `docs/expenses.md`, `docs/projects.md`, `docs/module-boundaries.md`, `ROADMAP.md` | D6 (Task 6) |
| `apps/expenses/frontend/src/{lib/status.ts,lib/money.ts,lib/search.test.ts,api/projects.ts,api/project-expenses.ts,lib/project-options.ts,pages/-expense-form-modal.tsx,components/entry-details.tsx,components/project-expenses-panel.tsx,test/fixtures.ts,test/server.ts,pages/supplier-invoice.test.tsx,components/project-expenses-panel.test.tsx,i18n.ts}` | D5, the Expenses package (Task 7) |
| `apps/projects/frontend/src/{api/economy.ts,pages/project-economy.tsx,pages/project-economy.test.tsx,i18n.ts}` | D5, the Costs section's line (Task 8) |

---

### Task 1: Who is owed money is one SQL function and its Go mirror (D1, the rule and the columns)

Migration `00033` adds the supplier invoice's two columns and `expenses.owes_employee`; every SQL copy of the owes-the-employee predicate calls the function; Go's `owesEmployee` becomes its mirror and learns the new kind, which owes nobody; a test holds the two together on every (kind, paid_by) pair. Nothing records the new kind yet — Task 2 does — so every existing test stays green unchanged.

**Files:**
- Create: `apps/server/internal/db/migrations/00033_expenses_supplier_invoices.sql`, `apps/server/internal/expenses/owes_employee_test.go`
- Modify: `apps/server/internal/expenses/sqlc.yaml`, `apps/server/internal/db/schema_test.go`, `apps/server/internal/expenses/queries/reimbursements.sql`, `apps/server/internal/expenses/queries/stats.sql`, `apps/server/internal/expenses/queries/claims.sql`, `apps/server/internal/expenses/authorize.go`, `apps/server/internal/expenses/entries_validation.go`, `apps/server/internal/expenses/export_test.go`
- Generated (commit them): `apps/server/internal/expenses/store/models.go` (`ExpensesEntry` gains `SupplierInvoiceNumber *string`, `SupplierDueDate pgtype.Date`), `store/reimbursements.sql.go`, `store/stats.sql.go`, `store/claims.sql.go`, and every other file `git status` shows under `apps/server/internal/expenses/store/` (every `SELECT *`/`RETURNING *`/`sqlc.embed(e)` scan now reads the two columns)
- Read first (do not change): `db/migrations/00012_expenses_baseline.sql:57-101`, `00014_expenses_to_invoice_index.sql` (the Down style), `00008_projects_baseline.sql:76-87` (`projects.visible`, the one SQL function sqlc already resolves in a WHERE), `db/schema_test.go:1779-1815,2222-2330,2454-2480` (`TestTimeWorkTypes_…`, `migrateTo`, `expensesColumns`), `expenses/authorize.go:730-741`, `expenses/reimbursements.go:280-345` (`owesEmployee`'s callers), `expenses/responses.go:533-542`

**Interfaces:**
- Produces SQL: `expenses.entries.supplier_invoice_number varchar(100) NULL`, `expenses.entries.supplier_due_date date NULL`, `expenses.owes_employee(kind text, paid_by text) RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE` — `outlay` → `paid_by IS NOT DISTINCT FROM 'employee'`, `supplier_invoice` → `false`, every other kind → `true`.
- Produces Go: `kindSupplierInvoice = "supplier_invoice"`; `owesEmployee(store.ExpensesEntry) bool` answering false for the new kind; `expenses.OwesEmployee(kind string, paidBy *string) bool` (export_test.go only); `store.ExpensesEntry.SupplierInvoiceNumber *string`, `.SupplierDueDate pgtype.Date`.
- Consumes: nothing new.

- [ ] **Step 1: Pin the migration, see it fail, write it**

In `apps/server/internal/db/schema_test.go`, in `expensesColumns`, replace

```go
		{"meal_dinner_percent", "numeric", "YES"},
	},
	"claims": {
```

with

```go
		{"meal_dinner_percent", "numeric", "YES"},
		// The supplier invoice's own two facts, 00033's (supplier invoices
		// design D1): the supplier's number for the invoice and the day it is
		// due. The entry date is the invoice date, so there is no third.
		{"supplier_invoice_number", "character varying", "YES"},
		{"supplier_due_date", "date", "YES"},
	},
	"claims": {
```

Directly before the comment `// TestCustomersRevision_AppliesWithANonUniqueLegalIdentityIndex proves`, add:

```go
// TestExpensesSupplierInvoices_AppliesAndIsIdempotent proves
// 00033_expenses_supplier_invoices.sql applies, rolls back and re-applies
// cleanly, and pins what the supplier invoices design (D1) rests on: the two
// columns at their widths, and expenses.owes_employee — IMMUTABLE, boolean,
// and the whole truth table of the rule every owes-the-employee query calls
// instead of writing it out. The Down is pinned as well: a supplier invoice
// left behind becomes the company-paid outlay the old inline predicate owes
// nobody for, and the function and the columns are gone.
func TestExpensesSupplierInvoices_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 33) // 00033_expenses_supplier_invoices.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var columns string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(string_agg(column_name || ':' || data_type
		       || CASE WHEN data_type = 'character varying' THEN '(' || character_maximum_length || ')'
		               ELSE '' END
		       || ':' || is_nullable, ',' ORDER BY column_name COLLATE "C"), 'MISSING')
		FROM information_schema.columns
		WHERE table_schema = 'expenses' AND table_name = 'entries'
		  AND column_name IN ('supplier_invoice_number', 'supplier_due_date')`).Scan(&columns); err != nil {
		t.Fatalf("read the supplier invoice columns: %v", err)
	}
	if want := "supplier_due_date:date:YES,supplier_invoice_number:character varying(100):YES"; columns != want {
		t.Errorf("supplier invoice columns = %q, want %q", columns, want)
	}

	var volatility, returns string
	if err := pool.QueryRow(ctx, `
		SELECT p.provolatile::text, pg_catalog.format_type(p.prorettype, NULL)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'expenses' AND p.proname = 'owes_employee'`).Scan(&volatility, &returns); err != nil {
		t.Fatalf("read expenses.owes_employee: %v", err)
	}
	if volatility != "i" || returns != "boolean" {
		t.Errorf("expenses.owes_employee is volatility %q returning %q, want IMMUTABLE (i) returning boolean", volatility, returns)
	}

	employee, company := "employee", "company"
	for _, c := range []struct {
		kind   string
		paidBy *string
		want   bool
	}{
		{"outlay", &employee, true},
		{"outlay", &company, false},
		{"outlay", nil, false},
		{"mileage", nil, true},
		{"per_diem", nil, true},
		{"supplier_invoice", &company, false},
		{"supplier_invoice", &employee, false},
		{"supplier_invoice", nil, false},
	} {
		var got bool
		if err := pool.QueryRow(ctx, `SELECT expenses.owes_employee($1, $2)`, c.kind, c.paidBy).Scan(&got); err != nil {
			t.Fatalf("expenses.owes_employee(%s): %v", c.kind, err)
		}
		if got != c.want {
			payer := "NULL"
			if c.paidBy != nil {
				payer = *c.paidBy
			}
			t.Errorf("expenses.owes_employee(%s, %s) = %v, want %v", c.kind, payer, got, c.want)
		}
	}

	// A supplier invoice recorded before a rollback is still nobody's money
	// after it: the Down makes it the company-paid outlay it would have been.
	if _, err := pool.Exec(ctx, `
		INSERT INTO expenses.entries (user_id, created_by_user_id, kind, entry_date, description,
		    currency, gross_amount, paid_by, supplier, supplier_invoice_number, project_id,
		    created_at, updated_at)
		VALUES ($1, $1, 'supplier_invoice', DATE '2026-03-10', 'Rørleggerarbeid', 'NOK', 1000.00,
		    'company', 'Rør & Varme AS', 'F-1', 1001, now(), now())`, uuid.New()); err != nil {
		t.Fatalf("seed a supplier invoice: %v", err)
	}
	migrateTo(t, url, 32)
	var kind, paidBy string
	if err := pool.QueryRow(ctx, `SELECT kind, paid_by FROM expenses.entries WHERE description = 'Rørleggerarbeid'`).Scan(&kind, &paidBy); err != nil {
		t.Fatalf("read the rolled-back invoice: %v", err)
	}
	if kind != "outlay" || paidBy != "company" {
		t.Errorf("after the rollback the invoice is kind %q paid by %q, want a company-paid outlay", kind, paidBy)
	}
	var functions, left int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		        WHERE n.nspname = 'expenses' AND p.proname = 'owes_employee'),
		       (SELECT count(*) FROM information_schema.columns
		        WHERE table_schema = 'expenses' AND table_name = 'entries'
		          AND column_name IN ('supplier_invoice_number', 'supplier_due_date'))`).Scan(&functions, &left); err != nil {
		t.Fatalf("read what the rollback left: %v", err)
	}
	if functions != 0 || left != 0 {
		t.Errorf("after the rollback %d owes_employee functions and %d supplier columns remain, want none", functions, left)
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestExpensesSupplierInvoices_AppliesAndIsIdempotent|TestExpensesBaseline_PinsTheEntryAndAttachmentColumns' ./internal/db/
```
Expected: FAIL (no migration 33; the column pin lists two columns nobody has added).

Create `apps/server/internal/db/migrations/00033_expenses_supplier_invoices.sql`:

```sql
-- +goose Up
-- Supplier invoices (supplier invoices design D1): the invoice a supplier
-- sends for work or goods on a project, recorded as a fourth kind of expense,
-- kind = 'supplier_invoice'. kind has always been a plain varchar(20) with no
-- CHECK (00012's house style), so the kind itself needs no DDL; what does is
-- the two facts an outlay never had.
--
-- supplier_invoice_number is the supplier's own number for the invoice: free
-- text, at most 100 characters, and not unique — there is no supplier record
-- to make it unique under, only the free-text supplier column. The name keeps
-- clear of invoiced_at / invoice_reference, which are the *outgoing* stamp:
-- the customer's invoice, not the supplier's. supplier_due_date is when the
-- supplier wants paying, on or after the entry date, which on this kind *is*
-- the invoice date. Both are NULL on every other kind; Go refuses them there,
-- and no CHECK ties them to the kind.
ALTER TABLE expenses.entries
    ADD COLUMN supplier_invoice_number varchar(100),
    ADD COLUMN supplier_due_date       date;

-- owes_employee is the one rule for who is owed money back (design D1). It
-- was written out fourteen times — thirteen in SQL, once in Go — and every
-- copy would have called a supplier invoice owed to the employee. Now every
-- query that asks calls this, and Go's owesEmployee (authorize.go) is its
-- mirror, held to it by a test on every kind and payer: an outlay owes its
-- gross only when the employee paid it; a supplier invoice owes nobody,
-- whatever its paid_by says; mileage and a per diem day are always the
-- employee's. "Owes something" still needs gross_amount > 0 beside it: that
-- half is about the amount, and every caller already says it.
--
-- IMMUTABLE and PARALLEL SAFE: it reads nothing but its arguments, so the
-- planner may inline it into every query that calls it — the predicate costs
-- what the inline one did.
-- +goose StatementBegin
CREATE FUNCTION expenses.owes_employee(kind text, paid_by text)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE $1
        WHEN 'outlay' THEN $2 IS NOT DISTINCT FROM 'employee'
        WHEN 'supplier_invoice' THEN false
        ELSE true
    END
$$;
-- +goose StatementEnd

-- +goose Down
-- A supplier invoice becomes the company-paid outlay it would have been
-- before this migration: paid_by is 'company' on every one, so the inline
-- predicate the older queries carry owes nobody for it, exactly as the
-- function did. Its number and due date go with their columns.
UPDATE expenses.entries SET kind = 'outlay' WHERE kind = 'supplier_invoice';
DROP FUNCTION expenses.owes_employee(text, text);
ALTER TABLE expenses.entries
    DROP COLUMN supplier_invoice_number,
    DROP COLUMN supplier_due_date;
```

In `apps/server/internal/expenses/sqlc.yaml`, replace

```yaml
      - ../db/migrations/00014_expenses_to_invoice_index.sql
```

with

```yaml
      - ../db/migrations/00014_expenses_to_invoice_index.sql
      - ../db/migrations/00033_expenses_supplier_invoices.sql
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestExpensesSupplierInvoices_AppliesAndIsIdempotent|TestExpensesBaseline_PinsTheEntryAndAttachmentColumns|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestNoModuleReferencesAnotherModulesSchema' ./internal/db/
```
Expected: PASS.

- [ ] **Step 2: Write the mirror's test and see it fail**

In `apps/server/internal/expenses/export_test.go`, replace

```go
import "context"
```

with

```go
import (
	"context"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// OwesEmployee is owesEmployee asked about a row of this kind and payer with a
// gross above zero, so the test holding it against expenses.owes_employee
// compares the rule and not the arithmetic.
func OwesEmployee(kind string, paidBy *string) bool {
	return owesEmployee(store.ExpensesEntry{
		Kind: kind, PaidBy: paidBy,
		GrossAmount: pgtype.Numeric{Int: big.NewInt(100), Valid: true},
	})
}
```

Create `apps/server/internal/expenses/owes_employee_test.go`:

```go
package expenses_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Who is owed money back is one rule written twice on purpose (supplier
// invoices design D1): expenses.owes_employee, the SQL function migration
// 00033 defines and every owes-the-employee query calls, and owesEmployee, its
// Go mirror, which decides a row's canMarkReimbursed and owedToEmployee. This
// asks both about every kind a row can carry and every payer it can name, and
// fails on any pair they disagree about — the kind this delivery adds, which
// owes nobody whatever its paid_by says, among them.
func TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, company := "employee", "company"
	for _, kind := range []string{"outlay", "mileage", "per_diem", "supplier_invoice"} {
		for _, paidBy := range []*string{nil, &employee, &company} {
			inSQL := modtest.One[bool](t, h.Harness, `SELECT expenses.owes_employee($1::text, $2::text)`, kind, paidBy)
			if inGo := expenses.OwesEmployee(kind, paidBy); inGo != inSQL {
				payer := "nobody"
				if paidBy != nil {
					payer = *paidBy
				}
				t.Errorf("a %s paid by %s: Go says owed %v, SQL says %v", kind, payer, inGo, inSQL)
			}
		}
	}
	if expenses.OwesEmployee("supplier_invoice", &employee) {
		t.Error("a supplier invoice marked paid by the employee owes them something; a supplier invoice owes nobody")
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./...
mise exec -- go test -count=1 -run 'TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction' ./internal/expenses/
```
Expected: FAIL — `a supplier_invoice paid by employee: Go says owed true, SQL says false` (Go's copy still treats every kind but outlay as owed).

- [ ] **Step 3: Fold the thirteen SQL copies into the function, make Go the mirror**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server/internal/expenses/queries
grep -c "NOT (e\?\.\?kind = 'outlay' AND (e\?\.\?paid_by IS NULL OR e\?\.\?paid_by <> 'employee'))" reimbursements.sql stats.sql claims.sql   # 9, 3, 1
sed -i \
  -e "s/NOT (e\.kind = 'outlay' AND (e\.paid_by IS NULL OR e\.paid_by <> 'employee'))/expenses.owes_employee(e.kind, e.paid_by)/g" \
  -e "s/NOT (kind = 'outlay' AND (paid_by IS NULL OR paid_by <> 'employee'))/expenses.owes_employee(kind, paid_by)/g" \
  reimbursements.sql stats.sql claims.sql
grep -n "kind = 'outlay'" reimbursements.sql stats.sql claims.sql   # exactly one line: claims.sql's receipts_missing count
grep -c "expenses.owes_employee(" reimbursements.sql stats.sql claims.sql   # 9, 3, 1
```

In `apps/server/internal/expenses/queries/reimbursements.sql`, replace the file's opening comment

```sql
-- Decision X5's first track: what the employee is owed back. Every query here
-- shares one predicate — an approved expense that owes its owner something —
-- written out in full each time rather than hidden in a view, so the count,
-- the page, the export and the update can never disagree about which expenses
-- a payroll run is about.
--
-- "Owes its owner something" is owedToEmployee (responses.go) in SQL: the
-- gross of an outlay the employee paid, the whole of a mileage line, and
-- nothing at all for an outlay the company paid. A gross that rounds to zero
-- owes nothing either, which is why gross_amount > 0 is part of it.
```

with

```sql
-- Decision X5's first track: what the employee is owed back. Every query here
-- shares one predicate — an approved expense that owes its owner something —
-- written out in every query rather than hidden in a view, so the count, the
-- page, the export and the update can never disagree about which expenses a
-- payroll run is about.
--
-- "Owes its owner something" is two halves. Who is owed is
-- expenses.owes_employee(kind, paid_by), the function migration 00033 defines
-- and owesEmployee (authorize.go) mirrors: the gross of an outlay the employee
-- paid, the whole of a mileage line and of a per diem day, and nothing at all
-- for an outlay the company paid or for a supplier invoice, which the company
-- pays and which is nobody's to be paid back for (supplier invoices design
-- D1). How much is gross_amount > 0: a gross that rounds to zero owes nothing.
```

In `apps/server/internal/expenses/authorize.go`, replace

```go
// owesEmployee reports whether the expense owes its owner anything at all —
// owedToEmployee above zero, decided without doing the arithmetic. It is the
// Go half of the predicate queries/reimbursements.sql applies in SQL, and the
// two must stay one rule: an outlay the company paid for itself owes nothing,
// and neither does a line whose gross rounds to nothing.
func owesEmployee(entry store.ExpensesEntry) bool {
	if entry.Kind == kindOutlay && (entry.PaidBy == nil || *entry.PaidBy != paidByEmployee) {
		return false
	}
	return entry.GrossAmount.Valid && !entry.GrossAmount.NaN &&
		entry.GrossAmount.Int != nil && entry.GrossAmount.Int.Sign() > 0
}
```

with

```go
// owesEmployee reports whether the expense owes its owner anything at all —
// owedToEmployee above zero, decided without doing the arithmetic.
//
// It is the Go mirror of expenses.owes_employee, the SQL function migration
// 00033 defines and every query that asks the question calls — one rule,
// written twice on purpose (supplier invoices design D1), and
// TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction holds the two together
// on every kind and payer. An outlay the company paid for itself owes
// nothing; a supplier invoice owes nobody, whatever its paid_by says, because
// the company pays the supplier; and a line whose gross rounds to nothing owes
// nothing either, which is the half of the question the SQL callers ask
// beside the function (gross_amount > 0).
func owesEmployee(entry store.ExpensesEntry) bool {
	switch entry.Kind {
	case kindSupplierInvoice:
		return false
	case kindOutlay:
		if entry.PaidBy == nil || *entry.PaidBy != paidByEmployee {
			return false
		}
	}
	return entry.GrossAmount.Valid && !entry.GrossAmount.NaN &&
		entry.GrossAmount.Int != nil && entry.GrossAmount.Int.Sign() > 0
}
```

In `apps/server/internal/expenses/entries_validation.go`, replace

```go
// The kinds of money line (decision X3). The per diem day is the one that
// exists only inside a travel claim; the other two stand alone or inside one.
const (
	kindOutlay  = "outlay"
	kindMileage = "mileage"
	kindPerDiem = "per_diem"
)
```

with

```go
// The kinds of money line (decision X3, and supplier invoices design D1). The
// per diem day is the one that exists only inside a travel claim; the supplier
// invoice the one that never does and always sits on a project; the outlay
// and mileage stand alone or inside one.
const (
	kindOutlay          = "outlay"
	kindMileage         = "mileage"
	kindPerDiem         = "per_diem"
	kindSupplierInvoice = "supplier_invoice"
)
```

(`entryKinds` does not list it yet: until Task 2 a request naming it is refused as an unknown kind, as it is today.)

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && git -C ../.. status --short   # the three query files' store/*.sql.go, models.go and every SELECT * reader under store/
mise exec -- go generate ./... && git -C ../.. diff --stat   # a second run moves nothing
mise exec -- gofmt -l internal/expenses internal/db
mise exec -- go test -count=1 -run 'TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction' ./internal/expenses/
```
Expected: PASS. **If sqlc refuses the function in a query** (`function expenses.owes_employee(character varying, character varying) does not exist`, or an unknown return type): change the function's two parameter types from `text` to `character varying` in the migration (the columns are `varchar(20)`), keep the body, change the Down to `DROP FUNCTION expenses.owes_employee(character varying, character varying);` and the schema test's `$1::text, $2::text` casts to `$1::varchar, $2::varchar`, and regenerate — `projects.visible` (00008) is the precedent that sqlc resolves a schema-qualified SQL function in a `WHERE`, and its arguments are typed exactly as the columns passed to it. Report which form it took.

- [ ] **Step 4: Every former predicate site is still exercised, and green**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestExpensesReimbursements_|TestExpensesReimbursed_|TestExpensesReimbursementsExport_|TestExpensesClaimReimbursement_|TestExpensesClaimExport_|TestExpensesClaimStats_|TestExpensesClaims_TotalsArePerCurrencyAndNeverConverted|TestExpensesStats|TestExpensesUnapprove_RefusesWhatHasBeenReimbursed|TestExpensesEntries_TheReimbursedFilter' ./internal/expenses/
mise exec -- go test -count=1 ./internal/expenses/... ./internal/db/... ./internal/integration/...
```
Expected: PASS. Site by site, the tests named above reach every query that carried the predicate: `CountReimbursementGroups`, `ListReimbursementGroups`, `ListReimbursementGroupEntries`, `ListReimbursementGroupClaims` (`TestExpensesReimbursements_AreGroupedPerPersonWithTheirTotals`, `TestExpensesClaimReimbursement_PaysATripAsOneUnit` — a company-paid outlay beside an owed one, and a trip); `ListReimbursementRows` (`TestExpensesReimbursementsExport_IsExactlyTheseBytes`, `…_RefusesWhatItCannotExport`, `TestExpensesClaimExport_WritesOneRowPerLineUnderItsUnit`); `MarkEntriesReimbursed` (`TestExpensesReimbursed_SaysWhyPerExpenseAndMovesNothing`, a company-paid line refused); `MarkClaimsReimbursed` (`TestExpensesClaimReimbursement_ATripThatOwesNothingCannotBeMarked`); `StatsMyUnreimbursed` and `StatsReimbursementWaiting` (`TestExpensesStats_AreTheCallersOwnKeyFigures`, `TestExpensesStatsAttention_IsWhatEachCallerHasToDo`, `TestExpensesClaimStats_CountsUnitsAndSumsWhatATripOwes`); `ClaimTotals` (`TestExpensesClaims_TotalsArePerCurrencyAndNeverConverted`). Task 2's `TestSupplierInvoices_OweNobody` adds the new kind to the standalone half of every one of them.

- [ ] **Step 5: Show it can fail, commit**

Prove the tests can fail, restoring after each: in the migration change `WHEN 'supplier_invoice' THEN false` to `THEN true` (regenerate, re-run Step 1's and Step 2's commands) — the schema test's truth table and the mirror test go red; change `WHEN 'outlay' THEN $2 IS NOT DISTINCT FROM 'employee'` to `THEN true` — the truth table and `TestExpensesReimbursements_AreGroupedPerPersonWithTheirTotals` go red (the company-paid outlay lands in the list); delete the `case kindSupplierInvoice:` arm in `owesEmployee` — the mirror test goes red; delete the Down's `UPDATE` — the rollback assertion goes red with `kind "supplier_invoice"`. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-1.txt <<'MSG'
feat(expenses): who is owed money back is one SQL function and its Go mirror

Migration 00033 adds the supplier invoice's two columns —
supplier_invoice_number varchar(100) and supplier_due_date — and the
IMMUTABLE function expenses.owes_employee(kind, paid_by): an outlay owes
its gross only when the employee paid it, a supplier invoice owes nobody,
mileage and a per diem day are the employee's. The thirteen copies of the
predicate in the reimbursement, stats and claim queries call it, and Go's
owesEmployee is its mirror, held to it by a test on every kind and payer.
The Down turns any supplier invoice into the company-paid outlay the old
inline predicate owes nobody for.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
PATHS="apps/server/internal/db/migrations/00033_expenses_supplier_invoices.sql apps/server/internal/expenses/sqlc.yaml \
 apps/server/internal/db/schema_test.go apps/server/internal/expenses/queries/reimbursements.sql \
 apps/server/internal/expenses/queries/stats.sql apps/server/internal/expenses/queries/claims.sql \
 apps/server/internal/expenses/authorize.go apps/server/internal/expenses/entries_validation.go \
 apps/server/internal/expenses/export_test.go apps/server/internal/expenses/owes_employee_test.go \
 $(git status --short -- apps/server/internal/expenses/store | awk '{print $2}')"
git add $PATHS && git commit -F /tmp/claude-1000/msg-si-1.txt -- $PATHS
git show --stat HEAD && git status --short
```

---
### Task 2: Expenses records, gates, prices, documents and shows a supplier invoice (D1, D2, D3's pricing, D4)

The fourth kind end to end inside Expenses: the request vocabulary and its field rules, the recorder gate on financial rights, the outlay's pricing, receipts and the document rule on submit, the widened visibility, the picker's `kind=supplier_invoice`, and the project summary's `canRecordSupplierInvoice` with its `project`.

**Files:**
- Create: `apps/server/internal/expenses/supplier_invoices_test.go`
- Modify: `openapi/expenses.yaml`, `apps/server/internal/expenses/entries_validation.go`, `apps/server/internal/expenses/entries.go`, `apps/server/internal/expenses/queries/entries.sql`, `apps/server/internal/expenses/authorize.go`, `apps/server/internal/expenses/billing.go`, `apps/server/internal/expenses/attachments.go`, `apps/server/internal/expenses/approvals.go`, `apps/server/internal/expenses/flow.go`, `apps/server/internal/expenses/responses.go`, `apps/server/internal/expenses/projectoptions.go`, `apps/server/internal/expenses/projectsummary.go`, `apps/server/internal/expenses/harness_test.go`, `openapi/COVERAGE.md` (if it moves)
- Generated (commit them): `apps/server/internal/openapi/specs/expenses.yaml`, `apps/server/internal/expenses/gen/api.gen.go`, `apps/server/internal/expenses/store/entries.sql.go`, `apps/expenses/frontend/src/api-schema.d.ts`, and every other file `git status` shows under `apps/server/internal/expenses/store/`
- Read first (do not change): `expenses/entries_validation.go` whole, `expenses/entries.go:110-260,511-700,808-1060,1150-1300`, `expenses/authorize.go:148-335,498-647`, `expenses/billing.go:80-125,150-170,295-380`, `expenses/attachments.go:290-340`, `expenses/flow.go:314-420,785-825`, `expenses/responses.go:300-380`, `expenses/projectoptions.go` whole, `expenses/projectsummary.go` whole, `expenses/harness_test.go:150-445,560-640,1020-1330,1560-1640,1700-1870,1878-2116`, `expenses/reimbursements_test.go:1-60`, `expenses/billing_test.go:15-30`, `expenses/invoiced_test.go:25-35`, `expenses/projectsummary_test.go:280-320`

**Interfaces:**
- Consumes: `kindSupplierInvoice`, `owesEmployee`, the two columns (Task 1).
- Produces Go: `entryKinds` with `kindSupplierInvoice`; `marksUp(kind) bool`, `takesReceipts(kind) bool`; `invoiceNumberMaxLength = 100`; `supplierInvoiceNeedsProject`, `supplierInvoiceNotInClaim`, `supplierInvoicePaidByCompany`; `entryBody.InvoiceNumber *string`, `.DueDate *openapi_types.Date`; `parsedEntry.InvoiceNumber *string`, `.DueDate *time.Time`; `parseAmounts`, `parseSupplierInvoice`; `(*server).checkLine`, `(*server).checkSupplierInvoiceProject`, `projectCancelled = "cancelled"`, `cannotRecordSupplierInvoice`, `supplierInvoiceOnCancelledProject`, `optionalPgDate`; `projectScope{all bool; ids []int32}`, `(*caller).financialProjects`; `supplierInvoiceDocumentRefusal`; `(*server).projectOption`, `(*server).supplierInvoiceProjects`; `projectSummaryResponse(totals, projectCurrency, capabilities gen.ExpensesProjectSummaryCapabilities, project *gen.ExpensesProjectOption)`; `store.InsertEntryParams`/`UpdateEntryParams` gain `SupplierInvoiceNumber *string`, `SupplierDueDate pgtype.Date`; `store.CountEntriesParams`/`ListEntriesParams` gain `SupplierInvoicesAll bool`, `FinancialProjectIds []int32`.
- Wire: `ExpensesEntryRequest`/`ExpensesEntryUpdateRequest` gain optional `invoiceNumber` (string) and `dueDate` (date); `ExpensesEntryResponse` gains optional `invoiceNumber`, `dueDate` (present on a supplier invoice when set); `kind` accepts `supplier_invoice` on both writes and on `GET /entries`; `GET /projects` gains optional `kind`; `ExpensesProjectSummaryCapabilities.canRecordSupplierInvoice` (optional boolean); `ExpensesProjectSummaryResponse.project` (optional `ExpensesProjectOption`). Refusals (400, `Invalid expense`): `kind` "A supplier invoice is booked on a project" (no projects module); `projectId` the same sentence (none named), "This project is not one you can record a supplier invoice on", "This project is cancelled, so it takes no supplier invoices"; `claimId` "A supplier invoice is not a travel claim line"; `paidBy` "A supplier invoice is paid by the company"; `supplier` "A supplier invoice names its supplier"; `invoiceNumber` "A supplier invoice carries the supplier's invoice number" / "An invoice number can be at most 100 characters"; `dueDate` "The due date cannot be before the invoice date"; `categoryId` "A supplier invoice needs a category"; `grossAmount` "A supplier invoice needs an amount"; `invoiceNumber`/`dueDate` on another kind "A <kind> line carries no <field>". Submit (400, `Invalid submission`, `entryIds`): "Expense <id> cannot be submitted yet: Attach the supplier's invoice".

- [ ] **Step 1: The contract**

In `openapi/expenses.yaml`, in `ExpensesEntryRequest`:

Replace

```yaml
            description: 'One money line (design §3.1). The fields a kind does not carry are refused on their own field rather than ignored: an outlay carries a category, a payer, a currency and a gross amount with optional VAT; a mileage line carries a distance, optional places and passengers, and is priced by the server from the dated rate table; a per diem day carries a type and three covered-meal flags, and is priced the same way. claimId records it as a line of a travel claim instead of a standalone expense, which is the only place a per diem day exists.'
```

with

```yaml
            description: 'One money line (design §3.1). The fields a kind does not carry are refused on their own field rather than ignored: an outlay carries a category, a payer, a currency and a gross amount with optional VAT; a supplier invoice carries the outlay''s money, a supplier, the supplier''s invoice number and an optional due date, is always paid by the company, always on a project and never in a travel claim (supplier invoices design D1); a mileage line carries a distance, optional places and passengers, and is priced by the server from the dated rate table; a per diem day carries a type and three covered-meal flags, and is priced the same way. claimId records it as a line of a travel claim instead of a standalone expense, which is the only place a per diem day exists.'
```

Replace

```yaml
                    description: Required on an outlay, and must be an active category. Refused on a mileage line and on a per diem day.
```

with

```yaml
                    description: Required on an outlay and on a supplier invoice, and must be an active category. Refused on a mileage line and on a per diem day.
```

Replace

```yaml
                    description: A three-letter ISO 4217 code. Required on an outlay.
```

with

```yaml
                    description: A three-letter ISO 4217 code. Required on an outlay and on a supplier invoice.
```

Replace

```yaml
                distanceKm:
                    description: Mileage's distance — greater than zero, at most 9999.9, at most one decimal. Refused on an outlay and on a per diem day.
                    format: double
                    type: number
```

with

```yaml
                distanceKm:
                    description: Mileage's distance — greater than zero, at most 9999.9, at most one decimal. Refused on an outlay, a supplier invoice and a per diem day.
                    format: double
                    type: number
                dueDate:
                    description: When the supplier wants paying — a supplier invoice's, optional, on or after entryDate (supplier invoices design D1). Informational only; nothing here records whether the supplier has been paid. Refused on every other kind.
                    format: date
                    type: string
```

Replace

```yaml
                    description: The day the money was spent or the distance driven. Nothing before the period lock may be recorded except by expenses:manage.
```

with

```yaml
                    description: The day the money was spent or the distance driven — on a supplier invoice, the invoice date, the one date the period lock judges. Nothing before the period lock may be recorded except by expenses:manage.
```

Replace

```yaml
                    description: An outlay's amount including VAT — greater than zero, at most 9999999999.99, at most two decimals. Refused on a mileage line and on a per diem day, whose amounts the server computes.
                    format: double
                    type: number
                kind:
                    description: '''outlay'', ''mileage'' or ''per_diem''. A per diem day exists only inside a travel claim, so it is refused without a claimId.'
                    type: string
```

with

```yaml
                    description: An outlay's or a supplier invoice's amount including VAT — greater than zero, at most 9999999999.99, at most two decimals. Refused on a mileage line and on a per diem day, whose amounts the server computes.
                    format: double
                    type: number
                invoiceNumber:
                    description: The supplier's own number for the invoice — required on a supplier invoice, at most 100 characters, trimmed. Free text and not unique, because there is no supplier record to make it unique under. Refused on every other kind.
                    type: string
                kind:
                    description: '''outlay'', ''mileage'', ''per_diem'' or ''supplier_invoice''. A per diem day exists only inside a travel claim, so it is refused without a claimId. A supplier invoice is never in a travel claim, is always on a project, and is refused outright in an installation with no projects module.'
                    type: string
```

Replace

```yaml
                    description: '''employee'' or ''company''. Required on an outlay, refused on a mileage line and on a per diem day — both are always owed to the employee.'
```

with

```yaml
                    description: '''employee'' or ''company''. Required on an outlay, refused on a mileage line and on a per diem day — both are always owed to the employee. A supplier invoice is paid by the company: it may be left out or say ''company'', and ''employee'' is refused.'
```

Replace

```yaml
                    description: The project to book the expense on. The person it concerns must be allowed to book on it — what logging time needs. Refused when this installation has no projects module.
```

with

```yaml
                    description: The project to book the expense on. The person it concerns must be allowed to book on it — what logging time needs. A supplier invoice needs one, and it is judged instead by the recorder's financial rights on the project (the manager role, projects:manage-all, or projects:view-financials on a project they see), on any project that is not cancelled (supplier invoices design D2). Refused when this installation has no projects module.
```

Replace

```yaml
                    description: At most 200 characters. Outlays only.
```

with

```yaml
                    description: At most 200 characters. Optional on an outlay, required on a supplier invoice; refused on a mileage line and on a per diem day.
```

In `ExpensesEntryResponse`, replace

```yaml
                    description: Mileage's distance. Absent on an outlay.
                    format: double
                    type: number
```

with

```yaml
                    description: Mileage's distance. Absent on an outlay.
                    format: double
                    type: number
                dueDate:
                    description: A supplier invoice's due date. Absent on every other kind, and on a supplier invoice that carries none.
                    format: date
                    type: string
```

Replace

```yaml
                    description: An outlay's amount as entered, VAT included; a mileage line's or a per diem day's computed amount.
                    format: double
                    type: number
```

with

```yaml
                    description: An outlay's amount as entered, VAT included; a mileage line's or a per diem day's computed amount.
                    format: double
                    type: number
                invoiceNumber:
                    description: A supplier invoice's number, as the supplier wrote it. Absent on every other kind. Not the outgoing invoice stamp, which is billing.invoice.
                    type: string
```

Replace

```yaml
                    description: What the owner gets back — the gross of an outlay they paid, a mileage line's amount, a per diem day's amount, and nothing at all for an outlay the company paid.
```

with

```yaml
                    description: What the owner gets back — the gross of an outlay they paid, a mileage line's amount, a per diem day's amount, and nothing at all for an outlay the company paid or for a supplier invoice, which the company pays.
```

In `ExpensesEntryUpdateRequest`, replace

```yaml
                distanceKm:
                    format: double
                    type: number
                entryDate:
                    description: Neither the day the entry had nor the day it is given may fall before the period lock, except for expenses:manage.
```

with

```yaml
                distanceKm:
                    format: double
                    type: number
                dueDate:
                    description: 'See the create request: a supplier invoice''s, on or after entryDate. Left out, it is cleared.'
                    format: date
                    type: string
                entryDate:
                    description: Neither the day the entry had nor the day it is given may fall before the period lock, except for expenses:manage.
```

Replace

```yaml
                grossAmount:
                    format: double
                    type: number
                kind:
                    description: 'Changing an outlay that still carries receipts into a line of another kind is refused on this field: a receipt belongs to an outlay, so the change would leave them where nothing can reach them. Remove them first.'
                    type: string
```

with

```yaml
                grossAmount:
                    format: double
                    type: number
                invoiceNumber:
                    description: 'See the create request: required on a supplier invoice, refused on every other kind.'
                    type: string
                kind:
                    description: 'Changing an outlay or a supplier invoice that still carries attachments into a mileage line or a per diem day is refused on this field: only those two kinds carry documents, so the change would leave them where nothing can reach them. Remove them first. A draft may change between outlay and supplier invoice — both carry documents, so nothing strands — and the refusals say what the new kind needs.'
                    type: string
```

In `ExpensesProjectSummaryCapabilities`, replace

```yaml
            required:
                - canRecord
            type: object
```

with

```yaml
                canRecordSupplierInvoice:
                    description: Whether the caller may record a supplier invoice on this project — financial rights on it, which reading the summary already requires, on a project that is not cancelled (supplier invoices design D2). A "Record a supplier invoice" button opens on it. Always answered; optional only because this schema's required list is never extended.
                    type: boolean
            required:
                - canRecord
            type: object
```

In `ExpensesProjectSummaryResponse`, replace

```yaml
                projectCurrency:
                    description: The project's own currency — the entry of `currencies` with this code is the project's;
```

with

```yaml
                project:
                    allOf:
                        - $ref: '#/components/schemas/ExpensesProjectOption'
                    description: The project as a booking option — its code, its name, its currency and its active billing lines — present exactly when capabilities.canRecordSupplierInvoice is true. It is what a "Record a supplier invoice" form is opened on, because the caller that button exists for is often on no project team, and GET /projects, a picker of what the caller may log time on, answers them nothing.
                projectCurrency:
                    description: The project's own currency — the entry of `currencies` with this code is the project's;
```

On `GET /api/v1/expenses/entries`, replace

```yaml
                - description: '''outlay'', ''mileage'' or ''per_diem''.'
```

with

```yaml
                - description: '''outlay'', ''mileage'', ''per_diem'' or ''supplier_invoice''.'
```

On `GET /api/v1/expenses/projects`, replace

```yaml
                - description: 'The person the expense is being recorded for. Naming anybody but the caller needs expenses:manage, the permission that lets one record for somebody else; left out, it is the caller''s own projects.'
```

with

```yaml
                - description: 'Which kind of expense the picker is for. Left out, ''outlay'' or ''mileage'', it is the projects the person may book on — what logging time needs. ''supplier_invoice'' is instead the caller''s own projects on which they hold financial rights and that are not cancelled — what recording a supplier invoice needs (supplier invoices design D2) — and cannot be combined with a userId naming somebody else, because the right to record one is the recorder''s. It lists projects the caller holds a role on; a projects:manage-all holder on no team records from the project page, whose summary answers the project. Any other value is refused on this field.'
                  in: query
                  name: kind
                  schema:
                    type: string
                - description: 'The person the expense is being recorded for. Naming anybody but the caller needs expenses:manage, the permission that lets one record for somebody else; left out, it is the caller''s own projects.'
```

Replace

```yaml
                    description: Bad Request — on userId, for somebody the caller may not ask about or an id nobody active has.
```

with

```yaml
                    description: Bad Request — on userId, for somebody the caller may not ask about or an id nobody active has; on kind, for a kind the picker does not narrow by or supplier_invoice asked for somebody else.
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go test -count=1 ./internal/openapi/...
mise exec -- go build ./... ; echo "build exit $?"
```
Expected: generate and the openapi tests PASS; the build **passes** (every field is optional and additive; `projectSummaryResponse`'s callers do not change until Step 7). `gen.ExpensesEntryRequest`/`ExpensesEntryUpdateRequest` now carry `InvoiceNumber *string` and `DueDate *openapi_types.Date`, `gen.ExpensesEntryResponse` the same two, `gen.GetExpensesProjectsParams` a `Kind *string`, `gen.ExpensesProjectSummaryCapabilities` a `CanRecordSupplierInvoice *bool`, `gen.ExpensesProjectSummaryResponse` a `Project *ExpensesProjectOption`. If oapi-codegen names any of them differently, use its names below and report them.

- [ ] **Step 2: The harness learns the fields, the tests are written, and they fail**

In `apps/server/internal/expenses/harness_test.go`, replace

```go
	Capabilities    entryCapabilitiesJSON   `json:"capabilities"`
}
```

with

```go
	Capabilities    entryCapabilitiesJSON   `json:"capabilities"`
	// The supplier invoice's two fields (supplier invoices design D1),
	// absent on every other kind.
	InvoiceNumber *string `json:"invoiceNumber"`
	DueDate       *string `json:"dueDate"`
}
```

Replace

```go
	ProjectCurrency *string `json:"projectCurrency"`
	Capabilities    struct {
		CanRecord bool `json:"canRecord"`
	} `json:"capabilities"`
}
```

with

```go
	ProjectCurrency *string `json:"projectCurrency"`
	Capabilities    struct {
		CanRecord bool `json:"canRecord"`
		// CanRecordSupplierInvoice is a pointer: optional in the contract,
		// so a test can tell false from not answered.
		CanRecordSupplierInvoice *bool `json:"canRecordSupplierInvoice"`
	} `json:"capabilities"`
	// Project is the booking option the summary answers exactly when
	// canRecordSupplierInvoice is true (supplier invoices design D2).
	Project *projectOptionJSON `json:"project"`
}
```

Create `apps/server/internal/expenses/supplier_invoices_test.go`:

```go
package expenses_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Supplier invoices (docs/superpowers/specs/2026-09-26-supplier-invoices-design.md):
// the invoice a supplier sends for work or goods on a project, recorded as a
// fourth kind of expense. It borrows the outlay's money whole, is paid by the
// company and so owes nobody, is booked by whoever holds the project's
// financial rights rather than by its team, needs its document to be
// submitted, and is visible to the project's financial side.

// subcontractorCategory is the second category 00012 seeds, after Materials —
// the one a supplier invoice is most often booked under.
const subcontractorCategory = 1002

// projectAbandoned is a cancelled project, the one status that takes no
// supplier invoice. The shared fixtures have none, so the tests that need one
// add it.
const projectAbandoned = 1006

func addAbandonedProject(h *harness) {
	customer := int32(1001)
	h.projects.addProject(contracts.ProjectEntry{
		ID: projectAbandoned, Code: "AVLYST01", Name: "Avlyst prosjekt",
		CustomerID: &customer, Status: "cancelled", OpenForWork: false,
		BillingType: "time-and-materials", Currency: ptr("NOK"),
	})
}

// supplierInvoiceBody is a valid supplier invoice on 1001, which tests
// override one field of at a time; a nil override removes the field. It names
// no payer — the request may leave it out, and the invoice is the company's —
// and it is billable, because what a supplier invoiced is usually billed on.
func supplierInvoiceBody(overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{
		"kind":          "supplier_invoice",
		"entryDate":     "2026-03-10",
		"description":   "Rørleggerarbeid, uke 10",
		"categoryId":    subcontractorCategory,
		"supplier":      "Rør & Varme AS",
		"invoiceNumber": "F-20260310",
		"dueDate":       "2026-04-09",
		"currency":      "NOK",
		"grossAmount":   12500.00,
		"vatAmount":     2500.00,
		"projectId":     projectKraftVerket,
		"billable":      true,
	}, overrides)
}

// financeReader is the caller D2 and D4 are written for: they see every
// project and its money, and hold no role on any of them — so they are on no
// team and projects' own CanLogTime would let them book nothing.
func financeReader(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return signIn(t, h, "projects:view-all", "projects:view-financials")
}

// attachInvoice uploads the supplier's invoice to one expense.
func attachInvoice(t *testing.T, c *modtest.Client, entryID int64) {
	t.Helper()
	uploadReceipt(t, c, entryID, "faktura.pdf", "application/pdf", testPDF(64))
}

func TestSupplierInvoices_ARecordedInvoiceIsTheCompanysDraftOnItsProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, financeID := financeReader(t, h)

	e := createEntry(t, finance, supplierInvoiceBody(nil))
	if e.Kind != "supplier_invoice" || e.Status != "draft" || e.Owner.UserId != financeID {
		t.Fatalf("recorded = kind %q status %q owner %v, want the recorder's supplier_invoice draft", e.Kind, e.Status, e.Owner.UserId)
	}
	if e.Supplier == nil || *e.Supplier != "Rør & Varme AS" || e.InvoiceNumber == nil || *e.InvoiceNumber != "F-20260310" ||
		e.DueDate == nil || *e.DueDate != "2026-04-09" {
		t.Errorf("supplier/number/due = %v/%v/%v, want Rør & Varme AS, F-20260310, 2026-04-09", e.Supplier, e.InvoiceNumber, e.DueDate)
	}
	// The company pays a supplier invoice: stored as company-paid whatever the
	// request left out, and owed to nobody.
	if e.PaidBy == nil || *e.PaidBy != "company" || e.OwedToEmployee != 0 || e.Capabilities.CanMarkReimbursed {
		t.Errorf("paidBy %v owed %v canMarkReimbursed %v, want company, 0, false", e.PaidBy, e.OwedToEmployee, e.Capabilities.CanMarkReimbursed)
	}
	if e.NetAmount != 10000 || e.Category == nil || e.Category.Id != subcontractorCategory || e.Project == nil || e.Project.Id != projectKraftVerket {
		t.Errorf("net %v category %v project %v, want 10000 under Subcontractor on %d", e.NetAmount, e.Category, e.Project, projectKraftVerket)
	}
	// Priced as an outlay (D3): the settings' default markup, 0 %, on the net.
	if !e.Billable || e.Billing == nil || e.Billing.BillAmount != 10000 || e.Billing.MarkupPercent == nil || *e.Billing.MarkupPercent != 0 {
		t.Errorf("billable %v billing %+v, want billable at the default markup, 10000 billed", e.Billable, e.Billing)
	}
	// The supplier's number is never the outgoing invoice stamp.
	if e.Billing != nil && e.Billing.Invoice != nil {
		t.Errorf("billing.invoice = %+v on a line nobody has invoiced", e.Billing.Invoice)
	}
	// The list's kind filter takes the new kind.
	if ids := entryIDs(listEntries(t, finance, "?kind=supplier_invoice")); !slices.Equal(ids, []int64{e.Id}) {
		t.Errorf("?kind=supplier_invoice = %v, want [%d]", ids, e.Id)
	}
}

func TestSupplierInvoices_TheBodyIsRefusedFieldByField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	trip := createClaim(t, manager, map[string]any{"projectId": projectKraftVerket})

	cases := []struct {
		name   string
		caller *modtest.Client
		body   map[string]any
		field  string
		want   string
	}{
		{"no supplier", finance, supplierInvoiceBody(map[string]any{"supplier": nil}), "supplier", "A supplier invoice names its supplier"},
		{"a blank supplier", finance, supplierInvoiceBody(map[string]any{"supplier": "   "}), "supplier", "A supplier invoice names its supplier"},
		{"no invoice number", finance, supplierInvoiceBody(map[string]any{"invoiceNumber": nil}), "invoiceNumber", "A supplier invoice carries the supplier's invoice number"},
		{"an invoice number too long", finance, supplierInvoiceBody(map[string]any{"invoiceNumber": strings.Repeat("9", 101)}), "invoiceNumber", "An invoice number can be at most 100 characters"},
		{"no category", finance, supplierInvoiceBody(map[string]any{"categoryId": nil}), "categoryId", "A supplier invoice needs a category"},
		{"no amount", finance, supplierInvoiceBody(map[string]any{"grossAmount": nil, "vatAmount": nil}), "grossAmount", "A supplier invoice needs an amount"},
		{"VAT above the amount", finance, supplierInvoiceBody(map[string]any{"vatAmount": 20000.00}), "vatAmount", "VAT cannot be more than the amount it is part of"},
		{"paid by the employee", finance, supplierInvoiceBody(map[string]any{"paidBy": "employee"}), "paidBy", "A supplier invoice is paid by the company"},
		{"no project", finance, supplierInvoiceBody(map[string]any{"projectId": nil, "billable": nil}), "projectId", "A supplier invoice is booked on a project"},
		{"a due date before the invoice date", finance, supplierInvoiceBody(map[string]any{"dueDate": "2026-03-09"}), "dueDate", "The due date cannot be before the invoice date"},
		{"a distance", finance, supplierInvoiceBody(map[string]any{"distanceKm": 12.0}), "distanceKm", "A supplier invoice line carries no distanceKm"},
		{"passengers", finance, supplierInvoiceBody(map[string]any{"passengers": 1}), "passengers", "A supplier invoice line carries no passengers"},
		{"a per diem type", finance, supplierInvoiceBody(map[string]any{"perDiemType": "day_6_12"}), "perDiemType", "A supplier invoice line carries no perDiemType"},
		{"a covered meal", finance, supplierInvoiceBody(map[string]any{"lunchCovered": true}), "lunchCovered", "A supplier invoice line carries no lunchCovered"},
		{"in a travel claim", manager, supplierInvoiceBody(map[string]any{"claimId": trip.Id}), "claimId", "A supplier invoice is not a travel claim line"},
	}
	for _, c := range cases {
		errs := refusedEntry(t, c.caller, http.MethodPost, entriesPath, c.body)
		if !mentions(errs[c.field], c.want) {
			t.Errorf("%s: %s = %v, want %q", c.name, c.field, errs[c.field], c.want)
		}
	}
	// Every other kind carries neither of the invoice's fields.
	errs := refusedEntry(t, finance, http.MethodPost, entriesPath,
		outlayBody(map[string]any{"invoiceNumber": "F-1", "dueDate": "2026-04-09"}))
	for _, field := range []string{"invoiceNumber", "dueDate"} {
		if !mentions(errs[field], "line carries no "+field) {
			t.Errorf("an outlay with %s: %v, want it refused on the field", field, errs[field])
		}
	}
}

func TestSupplierInvoices_WithoutProjectsTheKindIsRefusedOutright(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	c, _ := signIn(t, h)
	errs := refusedEntry(t, c, http.MethodPost, entriesPath,
		supplierInvoiceBody(map[string]any{"projectId": nil, "billable": nil}))
	if !mentions(errs["kind"], "A supplier invoice is booked on a project") {
		t.Errorf("kind = %v, want the kind refused: a supplier invoice exists only on a project", errs["kind"])
	}
}

func TestSupplierInvoices_TheRecordersFinancialRightsDecide(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addAbandonedProject(h)

	finance, _ := financeReader(t, h)
	createEntry(t, finance, supplierInvoiceBody(nil))
	// An invoice often arrives after the work: a completed project takes one.
	completed := createEntry(t, finance, supplierInvoiceBody(map[string]any{"projectId": projectCompleted}))
	if completed.Project == nil || completed.Project.Id != projectCompleted {
		t.Errorf("project = %+v, want the completed project %d", completed.Project, projectCompleted)
	}
	// The manager role is financial rights; so is projects:manage-all.
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	createEntry(t, manager, supplierInvoiceBody(nil))
	everywhere, _ := signIn(t, h, "projects:manage-all")
	createEntry(t, everywhere, supplierInvoiceBody(nil))

	// A cancelled project refuses, and says so to someone who may know.
	errs := refusedEntry(t, finance, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": projectAbandoned}))
	if !mentions(errs["projectId"], "This project is cancelled, so it takes no supplier invoices") {
		t.Errorf("a cancelled project: projectId = %v, want the cancelled refusal", errs["projectId"])
	}

	// A member of the team may book an outlay here — CanLogTime — and may not
	// record a supplier invoice, and hears what an unknown project gets.
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	createEntry(t, member, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	blind, _ := signIn(t, h, "projects:view-financials") // sees no project at all
	for name, refusal := range map[string]map[string][]string{
		"a member without financial rights": refusedEntry(t, member, http.MethodPost, entriesPath, supplierInvoiceBody(nil)),
		"an unknown project":                refusedEntry(t, member, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": projectUnknown})),
		"view-financials on a project not seen": refusedEntry(t, blind, http.MethodPost, entriesPath, supplierInvoiceBody(nil)),
		"a cancelled project, to a member":      refusedEntry(t, member, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": projectAbandoned})),
	} {
		if !mentions(refusal["projectId"], "This project is not one you can record a supplier invoice on") {
			t.Errorf("%s: projectId = %v, want the one refusal that tells nothing apart", name, refusal["projectId"])
		}
	}
}

func TestSupplierInvoices_TheOwnerAndManageChangeAndDeleteThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	_, colleague := signIn(t, h)

	// Recording one for a colleague still needs expenses:manage, and the
	// financial rights are the recorder's own: the colleague becomes the owner.
	clerk, _ := signIn(t, h, "expenses:manage", "projects:view-all", "projects:view-financials")
	forThem := createEntry(t, clerk, supplierInvoiceBody(map[string]any{"userId": colleague}))
	if forThem.Owner.UserId != colleague {
		t.Errorf("owner = %v, want the colleague %v", forThem.Owner.UserId, colleague)
	}
	errs := refusedEntry(t, finance, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"userId": colleague}))
	if !mentions(errs["userId"], "needs the Manage expenses permission") {
		t.Errorf("recording for a colleague without expenses:manage: userId = %v", errs["userId"])
	}
	manageOnly, _ := signIn(t, h, "expenses:manage")
	errs = refusedEntry(t, manageOnly, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"userId": colleague}))
	if !mentions(errs["projectId"], "This project is not one you can record a supplier invoice on") {
		t.Errorf("expenses:manage without financial rights: projectId = %v", errs["projectId"])
	}

	mine := createEntry(t, finance, supplierInvoiceBody(nil))
	changed := updateEntry(t, finance, mine.Id, supplierInvoiceBody(map[string]any{
		"invoiceNumber": "F-20260311", "revision": mine.Revision}))
	if changed.InvoiceNumber == nil || *changed.InvoiceNumber != "F-20260311" {
		t.Errorf("invoiceNumber after the replace = %v, want F-20260311", changed.InvoiceNumber)
	}
	// expenses:manage changes anyone's draft; the project it keeps is not
	// judged again, and a full replace without dueDate clears it.
	byClerk := updateEntry(t, manageOnly, mine.Id, supplierInvoiceBody(map[string]any{
		"dueDate": nil, "revision": changed.Revision}))
	if byClerk.DueDate != nil || byClerk.Owner.UserId != mine.Owner.UserId {
		t.Errorf("after manage's replace: dueDate %v owner %v, want cleared and still the recorder", byClerk.DueDate, byClerk.Owner.UserId)
	}
	if r := finance.Do(http.MethodDelete, entryPath(mine.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("the owner's delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := manageOnly.Do(http.MethodDelete, entryPath(forThem.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("manage's delete: status %d body %s, want 204", r.Status, r.Body)
	}
}

func TestSupplierInvoices_ADraftChangesBetweenOutlayAndSupplierInvoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	outlay := createEntry(t, manager, outlayBody(map[string]any{"projectId": projectKraftVerket, "paidBy": "company"}))
	attachInvoice(t, manager, outlay.Id)
	current := getEntry(t, manager, outlay.Id)

	// The missing-field refusals say what the new kind needs.
	errs := refusedEntry(t, manager, http.MethodPut, entryPath(outlay.Id), supplierInvoiceBody(map[string]any{
		"supplier": nil, "invoiceNumber": nil, "revision": current.Revision}))
	if !mentions(errs["supplier"], "A supplier invoice names its supplier") ||
		!mentions(errs["invoiceNumber"], "A supplier invoice carries the supplier's invoice number") {
		t.Errorf("an outlay made a supplier invoice without its fields: %v", errs)
	}
	// Both kinds carry documents, so the attachment comes along.
	invoice := updateEntry(t, manager, outlay.Id, supplierInvoiceBody(map[string]any{"revision": current.Revision}))
	if invoice.Kind != "supplier_invoice" || invoice.AttachmentCount != 1 || invoice.PaidBy == nil || *invoice.PaidBy != "company" {
		t.Errorf("after the change: kind %q attachments %d paidBy %v", invoice.Kind, invoice.AttachmentCount, invoice.PaidBy)
	}
	back := updateEntry(t, manager, invoice.Id, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "paidBy": "employee", "revision": invoice.Revision}))
	if back.Kind != "outlay" || back.AttachmentCount != 1 || back.InvoiceNumber != nil || back.DueDate != nil {
		t.Errorf("back to an outlay: kind %q attachments %d number %v due %v", back.Kind, back.AttachmentCount, back.InvoiceNumber, back.DueDate)
	}
	// Mileage carries no documents, so that change is still refused.
	errs = refusedEntry(t, manager, http.MethodPut, entryPath(back.Id), mileageBody(map[string]any{"revision": back.Revision}))
	if !mentions(errs["kind"], "Remove this expense's receipts") {
		t.Errorf("an outlay with an attachment made mileage: kind = %v", errs["kind"])
	}
}

func TestSupplierInvoices_SubmitNeedsTheSuppliersInvoiceAttached(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	e := createEntry(t, finance, supplierInvoiceBody(nil))

	errs := refusedFlow(t, finance, submitPath, flowBody([]int64{e.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d cannot be submitted yet: Attach the supplier's invoice", e.Id)) {
		t.Errorf("entryIds = %v, want the document refusal", errs["entryIds"])
	}
	if got := getEntry(t, finance, e.Id); got.Status != "draft" {
		t.Errorf("status after the refusal = %q, want draft", got.Status)
	}
	attachInvoice(t, finance, e.Id)
	if moved := submitEntries(t, finance, e.Id); len(moved) != 1 || moved[0].Status != "submitted" {
		t.Errorf("submit with the invoice attached = %+v, want it submitted", moved)
	}
}

func TestSupplierInvoices_AreAttestedPricedAndInvoicedLikeAnyCost(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "expenses:approve")
	clerk, _ := signIn(t, h, "expenses:manage")
	submitted := func(number string) entryJSON {
		e := createEntry(t, manager, supplierInvoiceBody(map[string]any{"invoiceNumber": number}))
		attachInvoice(t, manager, e.Id)
		submitEntries(t, manager, e.Id)
		return e
	}

	// The project's manager approves — their own recording included.
	own := submitted("F-1")
	if moved := approveEntries(t, manager, own.Id); moved[0].Status != "approved" {
		t.Errorf("the manager's approval = %q", moved[0].Status)
	}
	other := submitted("F-2")
	approveEntries(t, approver, other.Id)
	sent := submitted("F-3")
	if moved := rejectEntries(t, approver, "Feil prosjekt", sent.Id); moved[0].Status != "rejected" {
		t.Errorf("rejected = %q", moved[0].Status)
	}
	if moved := unapproveEntries(t, clerk, other.Id); moved[0].Status != "draft" {
		t.Errorf("unapproved = %q, want a fresh draft", moved[0].Status)
	}

	// The pricing door: a markup named from the project's side, on the net.
	priced := setBilling(t, manager, own.Id, setBillingBody(getEntry(t, manager, own.Id).Revision,
		map[string]any{"markupPercent": 10.0}))
	if priced.Billing == nil || priced.Billing.BillAmount != 11000 {
		t.Errorf("billing after a 10 %% markup = %+v, want 11000 on a 10000 net", priced.Billing)
	}
	ready := listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=true", projectKraftVerket))
	if ids := entryIDs(ready); !slices.Equal(ids, []int64{own.Id}) {
		t.Errorf("ready to invoice = %v, want [%d]", ids, own.Id)
	}
	invoiced := markInvoiced(t, manager, own.Id, invoicedBody(priced.Revision, map[string]any{"reference": "KF-1001"}))
	if invoiced.Billing == nil || invoiced.Billing.Invoice == nil {
		t.Fatalf("billing after the stamp = %+v, want the invoice", invoiced.Billing)
	}
	errs := refusedFlow(t, manager, unapprovePath, flowBody([]int64{own.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d has been invoiced", own.Id)) {
		t.Errorf("unapproving an invoiced supplier invoice: %v", errs["entryIds"])
	}
}

// A supplier invoice owes nobody (D1): the company pays the supplier. So it is
// absent from every surface the owes-the-employee rule decides — the
// reimbursement list, the payroll CSV, a payroll run, the unreimbursed
// figures and the reimbursement attention item — while an outlay its owner
// paid, approved beside it, is on every one of them.
func TestSupplierInvoices_OweNobody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:manage", "expenses:approve")
	invoice := createEntry(t, manager, supplierInvoiceBody(nil))
	attachInvoice(t, manager, invoice.Id)
	approvedBy(t, manager, manager, invoice.Id)
	owed := createEntry(t, manager, outlayBody(nil))
	approvedBy(t, manager, manager, owed.Id)

	if got := getEntry(t, manager, invoice.Id); got.OwedToEmployee != 0 || got.Capabilities.CanMarkReimbursed {
		t.Errorf("approved invoice: owed %v canMarkReimbursed %v, want 0 and false", got.OwedToEmployee, got.Capabilities.CanMarkReimbursed)
	}
	var listed []int64
	for _, group := range getReimbursements(t, manager, "").Data {
		listed = append(listed, entryIDsOf(group.Entries)...)
	}
	if !slices.Contains(listed, owed.Id) || slices.Contains(listed, invoice.Id) {
		t.Errorf("reimbursement list = %v, want the outlay %d and never the invoice %d", listed, owed.Id, invoice.Id)
	}
	csv := string(exportCSV(t, manager, "").Body)
	if !strings.Contains(csv, "Kabel og kontakter") || strings.Contains(csv, "Rørleggerarbeid") {
		t.Errorf("payroll CSV:\n%s\nwant the outlay's row and no supplier invoice", csv)
	}
	errs := refusedReimbursement(t, manager, reimbursedPath, reimbursedBody([]int64{invoice.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d owes the employee nothing", invoice.Id)) {
		t.Errorf("a payroll run over the invoice: %v", errs["entryIds"])
	}
	if stats := getStats(t, manager); len(stats.Unreimbursed) != 1 || stats.Unreimbursed[0].Amount != 1250 {
		t.Errorf("unreimbursed = %+v, want the outlay's 1250 alone", stats.Unreimbursed)
	}
	if summary := getStatsSummary(t, manager, ""); len(summary.MyUnreimbursed) != 1 || summary.MyUnreimbursed[0].Amount != 1250 {
		t.Errorf("myUnreimbursed = %+v, want the outlay's 1250 alone", summary.MyUnreimbursed)
	}
	waiting := attentionOfType(getAttention(t, manager), "reimbursementWaiting")
	if len(waiting) != 1 || waiting[0].Count == nil || *waiting[0].Count != 1 {
		t.Errorf("reimbursementWaiting = %+v, want one item counting the outlay alone", waiting)
	}
}

// D4: a supplier invoice carries no personal data, so its rows are the
// project's financial side's to see — the list, the project's own list, the
// detail and its document — while an employee's outlay on the same project
// keeps today's visibility.
func TestSupplierInvoices_TheProjectsFinancialSideSeesTheRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	invoice := createEntry(t, manager, supplierInvoiceBody(nil))
	attachInvoice(t, manager, invoice.Id)
	document := getEntry(t, manager, invoice.Id).Attachments[0].Id
	outlay := createEntry(t, member, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	onProject := fmt.Sprintf("?projectId=%d", projectKraftVerket)

	finance, _ := financeReader(t, h)
	viewer, _ := signInAs(t, h, projectKraftVerket, roleViewer, "projects:view-financials")
	for name, c := range map[string]*modtest.Client{"view-financials with view-all": finance, "view-financials through a role": viewer} {
		if ids := entryIDs(listEntries(t, c, onProject)); !slices.Equal(ids, []int64{invoice.Id}) {
			t.Errorf("%s: the project's list = %v, want the invoice %d alone", name, ids, invoice.Id)
		}
		if got := getEntry(t, c, invoice.Id); got.InvoiceNumber == nil || got.AttachmentCount != 1 {
			t.Errorf("%s: the detail = %+v, want the invoice with its document", name, got)
		}
		if r := downloadReceipt(t, c, document); r.Status != http.StatusOK {
			t.Errorf("%s: the document: status %d", name, r.Status)
		}
		if r := c.Do(http.MethodGet, entryPath(outlay.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s: a colleague's outlay: status %d, want the bare 404", name, r.Status)
		}
	}
	// Seeing is not changing: the finance reader is not the invoice's writer.
	forbidden(t, finance, http.MethodDelete, entryPath(invoice.Id), nil)

	plainViewer, _ := signInAs(t, h, projectKraftVerket, roleViewer)
	if ids := entryIDs(listEntries(t, plainViewer, onProject)); len(ids) != 0 {
		t.Errorf("a viewer without financial rights lists %v, want nothing", ids)
	}
	if r := plainViewer.Do(http.MethodGet, entryPath(invoice.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a viewer without financial rights reads the invoice: status %d, want 404", r.Status)
	}
}

func TestSupplierInvoices_ThePickerOffersTheProjectsTheCallerMayRecordOneOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addAbandonedProject(h)
	c, id := signIn(t, h, "projects:view-financials")
	for _, project := range []int32{projectKraftVerket, projectCompleted, projectAbandoned} {
		h.projects.addRole(project, id, roleViewer)
	}

	r := c.Do(http.MethodGet, projectOptionsPath+"?kind=supplier_invoice", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("the supplier invoice picker: status %d body %s", r.Status, r.Body)
	}
	var options []projectOptionJSON
	r.JSON(&options)
	var ids []int32
	for _, option := range options {
		ids = append(ids, option.Id)
	}
	if !slices.Equal(ids, []int32{projectKraftVerket, projectCompleted}) {
		t.Errorf("supplier invoice picker = %v, want %d and %d — never the cancelled %d", ids, projectKraftVerket, projectCompleted, projectAbandoned)
	}
	if len(options) > 0 && (len(options[0].BillingLines) != 1 || options[0].BillingLines[0].Id != lineFixed) {
		t.Errorf("1001's lines = %+v, want the one active line", options[0].BillingLines)
	}
	// The bookable-projects picker offers a viewer nothing: a viewer logs no time.
	if plain := listProjectOptions(t, c); len(plain) != 0 {
		t.Errorf("the ordinary picker = %+v, want nothing for a viewer", plain)
	}
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	r = member.Do(http.MethodGet, projectOptionsPath+"?kind=supplier_invoice", nil)
	var none []projectOptionJSON
	r.JSON(&none)
	if r.Status != http.StatusOK || len(none) != 0 {
		t.Errorf("a member without financial rights: status %d options %+v, want none", r.Status, none)
	}
	for query, want := range map[string]string{
		"?kind=per_diem":                                 "is not a kind this picker narrows by",
		"?kind=supplier_invoice&userId=" + uuid.NewString(): "the right to record one is the recorder's",
	} {
		errs := refused(t, c, http.MethodGet, projectOptionsPath+query, nil, invalidQueryTitle)
		if !mentions(errs["kind"], want) {
			t.Errorf("%s: kind = %v, want %q", query, errs["kind"], want)
		}
	}
}

// The approval queue counts a supplier invoice with no document the way it
// counts an outlay with no receipt. The submit refuses one now, so the row is
// seeded — one submitted before the rule, or written past it, must still be
// flagged to whoever checks it.
func TestSupplierInvoices_TheQueueCountsOneWithNoDocument(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve")
	_, owner := signIn(t, h)
	h.Exec(t, `INSERT INTO expenses.entries
	    (user_id, created_by_user_id, kind, entry_date, description, category_id, supplier,
	     supplier_invoice_number, paid_by, currency, gross_amount, project_id, status,
	     submitted_at, created_at, updated_at)
	    VALUES ($1, $1, 'supplier_invoice', DATE '2026-03-10', 'Rørleggerarbeid', $2, 'Rør & Varme AS',
	            'F-1', 'company', 'NOK', 1000.00, $3, 'submitted', now(), now(), now())`,
		owner, int32(subcontractorCategory), int32(projectKraftVerket))
	page := getApprovals(t, approver, "")
	if len(page.Data) != 1 || page.Data[0].ReceiptsMissing != 1 {
		t.Errorf("approval queue = %+v, want one group missing one document", page.Data)
	}
}

func TestSupplierInvoices_TheSummarySaysWhoMayRecordOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addAbandonedProject(h)
	finance, _ := financeReader(t, h)

	summary := getProjectSummary(t, finance, projectKraftVerket)
	if summary.Capabilities.CanRecord {
		t.Error("canRecord is true for a reader on no team")
	}
	if c := summary.Capabilities.CanRecordSupplierInvoice; c == nil || !*c {
		t.Errorf("canRecordSupplierInvoice = %v, want true: financial rights on an open project", c)
	}
	if p := summary.Project; p == nil || p.Id != projectKraftVerket || p.Code != projectKraftVerketCode ||
		len(p.BillingLines) != 1 || p.BillingLines[0].Id != lineFixed {
		t.Errorf("project = %+v, want 1001 with its one active line", summary.Project)
	}
	if c := getProjectSummary(t, finance, projectCompleted).Capabilities.CanRecordSupplierInvoice; c == nil || !*c {
		t.Errorf("a completed project: canRecordSupplierInvoice = %v, want true", c)
	}
	abandoned := getProjectSummary(t, finance, projectAbandoned)
	if c := abandoned.Capabilities.CanRecordSupplierInvoice; c == nil || *c || abandoned.Project != nil {
		t.Errorf("a cancelled project: canRecordSupplierInvoice %v project %+v, want false and no project", c, abandoned.Project)
	}
}
```

(`entryIDsOf` is `reimbursements_test.go`'s helper over a group's entries; `downloadReceipt`, `getApprovals`, `refused`, `invalidQueryTitle`, `lineFixed`, `projectUnknown`, `projectCompleted` are the harness's.)

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestSupplierInvoices_' ./internal/expenses/
```
Expected: FAIL — every create is refused on `kind` ("'supplier_invoice' is not an expense kind").

- [ ] **Step 3: The kind and its field rules**

In `apps/server/internal/expenses/entries_validation.go`, replace

```go
// entryKinds is every kind a request may carry, in the order design §3.1 names
// them. It is also what the list's kind filter accepts, so the two can never
// drift.
var entryKinds = []string{kindOutlay, kindMileage, kindPerDiem}
```

with

```go
// entryKinds is every kind a request may carry, in the order design §3.1 names
// them and the supplier invoice after them. It is also what the list's kind
// filter accepts, so the two can never drift.
var entryKinds = []string{kindOutlay, kindMileage, kindPerDiem, kindSupplierInvoice}

// marksUp reports whether a billable line of this kind is priced as its net
// plus a markup: an outlay, and a supplier invoice, which borrows the outlay's
// money whole (supplier invoices design D3). Mileage bills per kilometre and a
// per diem day bills nothing.
func marksUp(kind string) bool { return kind == kindOutlay || kind == kindSupplierInvoice }

// takesReceipts reports whether a line of this kind carries documents: an
// outlay its receipts, a supplier invoice the supplier's invoice itself
// (design D2). Mileage and a per diem day carry none.
func takesReceipts(kind string) bool { return kind == kindOutlay || kind == kindSupplierInvoice }

// The supplier invoice's own limit and refusals (supplier invoices design D1).
// One project sentence serves both "no project named" and "no projects module
// at all", because the fact the caller can act on is the same: this kind lives
// on a project and nowhere else.
const (
	invoiceNumberMaxLength       = 100
	supplierInvoiceNeedsProject  = "A supplier invoice is booked on a project"
	supplierInvoiceNotInClaim    = "A supplier invoice is not a travel claim line"
	supplierInvoicePaidByCompany = "A supplier invoice is paid by the company"
)
```

Replace

```go
	BillRatePerKm *float64
	ClaimID       *int64
}
```

with

```go
	BillRatePerKm *float64
	ClaimID       *int64

	// InvoiceNumber and DueDate are the supplier invoice's own two fields
	// (supplier invoices design D1), refused on every other kind.
	InvoiceNumber *string
	DueDate       *openapi_types.Date
}
```

Replace

```go
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
	}
}

// bodyOfUpdate is a replace request as one entryBody.
```

with

```go
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
		InvoiceNumber: b.InvoiceNumber, DueDate: b.DueDate,
	}
}

// bodyOfUpdate is a replace request as one entryBody.
```

Replace

```go
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
	}
}

// parsedEntry is one validated body
```

with

```go
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
		InvoiceNumber: b.InvoiceNumber, DueDate: b.DueDate,
	}
}

// parsedEntry is one validated body
```

Replace

```go
	MarkupPercent *big.Rat
	BillRatePerKm *big.Rat
}
```

with

```go
	MarkupPercent *big.Rat
	BillRatePerKm *big.Rat

	// InvoiceNumber and DueDate are a supplier invoice's, nil on every other
	// kind. DueDate is a calendar date at UTC midnight, like Date.
	InvoiceNumber *string
	DueDate       *time.Time
}
```

Replace

```go
	if body.EntryDate.IsZero() {
		add("entryDate", "A date is required")
```

with

```go
	if p.Kind != kindSupplierInvoice {
		if body.InvoiceNumber != nil {
			add("invoiceNumber", notOnKind("invoiceNumber", p.Kind))
		}
		if body.DueDate != nil {
			add("dueDate", notOnKind("dueDate", p.Kind))
		}
	}

	if body.EntryDate.IsZero() {
		add("entryDate", "A date is required")
```

Replace

```go
	switch p.Kind {
	case kindOutlay:
		parseOutlay(&p, body, add)
```

with

```go
	switch p.Kind {
	case kindSupplierInvoice:
		parseSupplierInvoice(&p, body, projectsOn, inClaim, add)
	case kindOutlay:
		parseOutlay(&p, body, add)
```

Replace the whole of `parseOutlay` — from `// parseOutlay is the outlay half of design §3.1: what somebody paid, to whom,` through its closing `refuseMileageFields(body, kindOutlay, add)` and `}` — with:

```go
// parseOutlay is the outlay half of design §3.1: what somebody paid, to whom,
// in what currency, with the VAT they can read off the receipt. The mileage
// fields are refused rather than ignored, so a client that sent the wrong
// shape hears about it.
func parseOutlay(p *parsedEntry, body entryBody, add func(field, msg string)) {
	parseAmounts(p, body, "An outlay", add)
	if msg := optionalText(body.Supplier, "A supplier", supplierMaxLength, &p.Supplier); msg != "" {
		add("supplier", msg)
	}
	switch paidBy := strings.TrimSpace(derefString(body.PaidBy)); {
	case paidBy == "":
		add("paidBy", "An outlay says who paid it")
	case !slices.Contains(paidByValues, paidBy):
		add("paidBy", fmt.Sprintf("'%s' is not a payer; must be one of %s", paidBy, strings.Join(paidByValues, ", ")))
	default:
		p.PaidBy = &paidBy
	}
	refuseMileageFields(body, kindOutlay, add)
}

// parseAmounts is the money an outlay and a supplier invoice share (supplier
// invoices design D1): a category, a currency in any ISO code, a gross above
// zero and an optional VAT from zero to that gross. what names the kind in the
// two sentences that say something is missing.
func parseAmounts(p *parsedEntry, body entryBody, what string, add func(field, msg string)) {
	if body.CategoryID == nil {
		add("categoryId", what+" needs a category")
	} else {
		p.CategoryID = body.CategoryID
	}

	currency, msg := validateCurrency(derefString(body.Currency))
	add("currency", msg)
	if msg == "" {
		p.Currency = currency
	}

	if body.GrossAmount == nil {
		add("grossAmount", what+" needs an amount")
	} else if msg := validateAboveZero("An amount", *body.GrossAmount, maxMoney); msg != "" {
		add("grossAmount", msg)
	} else {
		p.Gross = ratFromFloat(*body.GrossAmount)
	}
	if body.VatAmount != nil {
		if msg := validateDecimal("VAT", *body.VatAmount, 0, maxMoney); msg != "" {
			add("vatAmount", msg)
		} else if body.GrossAmount != nil && *body.VatAmount > *body.GrossAmount {
			add("vatAmount", "VAT cannot be more than the amount it is part of")
		} else {
			p.Vat = ratFromFloat(*body.VatAmount)
		}
	}
}

// parseSupplierInvoice is the supplier invoice (supplier invoices design D1):
// the outlay's money, a supplier and the supplier's invoice number that are
// both required, an optional due date on or after the entry date — which on
// this kind *is* the invoice date, the one the period lock judges — and a
// payer that is always the company. It never sits in a travel claim and
// always sits on a project, and in an installation without the projects
// module there is no such kind at all. Whether the recorder may book it on
// the project is asked afterwards, of the directory, outside any transaction
// (checkSupplierInvoiceProject).
func parseSupplierInvoice(p *parsedEntry, body entryBody, projectsOn, inClaim bool, add func(field, msg string)) {
	switch {
	case !projectsOn:
		add("kind", supplierInvoiceNeedsProject)
	case body.ProjectID == nil:
		add("projectId", supplierInvoiceNeedsProject)
	}
	if inClaim {
		add("claimId", supplierInvoiceNotInClaim)
	}
	parseAmounts(p, body, "A supplier invoice", add)
	if msg := optionalText(body.Supplier, "A supplier", supplierMaxLength, &p.Supplier); msg != "" {
		add("supplier", msg)
	} else if p.Supplier == nil {
		add("supplier", "A supplier invoice names its supplier")
	}
	if msg := optionalText(body.InvoiceNumber, "An invoice number", invoiceNumberMaxLength, &p.InvoiceNumber); msg != "" {
		add("invoiceNumber", msg)
	} else if p.InvoiceNumber == nil {
		add("invoiceNumber", "A supplier invoice carries the supplier's invoice number")
	}
	if body.DueDate != nil {
		due := utcDay(body.DueDate.Time)
		if !p.Date.IsZero() && due.Before(p.Date) {
			add("dueDate", "The due date cannot be before the invoice date")
		} else {
			p.DueDate = &due
		}
	}
	// The company pays the supplier, so the payer is not the caller's to say:
	// left out or 'company' it is stored as company-paid, which is what the
	// owes-the-employee rule and every payroll surface read.
	switch paidBy := strings.TrimSpace(derefString(body.PaidBy)); paidBy {
	case "", paidByCompany:
		company := paidByCompany
		p.PaidBy = &company
	case paidByEmployee:
		add("paidBy", supplierInvoicePaidByCompany)
	default:
		add("paidBy", fmt.Sprintf("'%s' is not a payer; a supplier invoice is paid by the company", paidBy))
	}
	refuseMileageFields(body, kindSupplierInvoice, add)
}
```

Replace

```go
		} else if p.Kind != kindOutlay || !requested {
			add("markupPercent", "A markup belongs to a billable outlay")
```

with

```go
		} else if !marksUp(p.Kind) || !requested {
			add("markupPercent", "A markup belongs to a billable outlay or supplier invoice")
```

Replace

```go
// kindLabel names a kind the way a refusal says it out loud. Only the per diem
// day's stored value is not already a word.
func kindLabel(kind string) string {
	if kind == kindPerDiem {
		return "per diem"
	}
	return kind
}
```

with

```go
// kindLabel names a kind the way a refusal says it out loud. The per diem
// day's and the supplier invoice's stored values are not already words.
func kindLabel(kind string) string {
	switch kind {
	case kindPerDiem:
		return "per diem"
	case kindSupplierInvoice:
		return "supplier invoice"
	}
	return kind
}
```

- [ ] **Step 4: The recorder gate, the pricing and the two columns**

In `apps/server/internal/expenses/entries.go`, replace

```go
	keptProject := inherited ||
		(current != nil && current.ProjectID != nil && *current.ProjectID == *p.ProjectID)
```

with

```go
	// A supplier invoice's project was judged by its recorder's financial
	// rights, not by the owner's CanLogTime, so turning one into an outlay
	// is a new booking of this kind and is judged in full.
	keptProject := inherited ||
		(current != nil && current.Kind != kindSupplierInvoice &&
			current.ProjectID != nil && *current.ProjectID == *p.ProjectID)
```

Replace

```go
	keptLine := keptProject && p.LineID != nil && current != nil &&
		current.BillingLineID != nil && *current.BillingLineID == *p.LineID
	if p.LineID != nil && !keptLine {
		line, err := s.projectsBillingLine(ctx, *p.ProjectID, *p.LineID)
		if err != nil {
			return nil, false, fmt.Errorf("expenses: look up the billing line: %w", err)
		}
		switch {
		case line == nil:
			add("billingLineId", fmt.Sprintf("Billing line %d is not one of this project's", *p.LineID))
		case !line.Active:
			add("billingLineId", fmt.Sprintf("Billing line %d is inactive", *p.LineID))
		}
	}
	return project, false, nil
}
```

with

```go
	if err := s.checkLine(ctx, p, current, keptProject, add); err != nil {
		return nil, false, err
	}
	return project, false, nil
}

// checkLine is the billing-line half of both project checks: a line the save
// keeps on a project it keeps is not judged again, and any other must be one
// of the project's active lines.
func (s *server) checkLine(ctx context.Context, p parsedEntry, current *store.ExpensesEntry, keptProject bool,
	add func(field, msg string),
) error {
	keptLine := keptProject && p.LineID != nil && current != nil &&
		current.BillingLineID != nil && *current.BillingLineID == *p.LineID
	if p.LineID == nil || keptLine {
		return nil
	}
	line, err := s.projectsBillingLine(ctx, *p.ProjectID, *p.LineID)
	if err != nil {
		return fmt.Errorf("expenses: look up the billing line: %w", err)
	}
	switch {
	case line == nil:
		add("billingLineId", fmt.Sprintf("Billing line %d is not one of this project's", *p.LineID))
	case !line.Active:
		add("billingLineId", fmt.Sprintf("Billing line %d is inactive", *p.LineID))
	}
	return nil
}

// projectCancelled is the one project status that takes no supplier invoice
// (supplier invoices design D2). An invoice often arrives after the work, so
// a completed project takes one, and so does every other status.
const projectCancelled = "cancelled"

// The two projectId sentences a supplier invoice's recorder can hear. The
// first is the one answer for "no such project" and "no financial rights on
// it", so it tells nobody which project ids exist or whose money they are;
// the second is said only to someone who holds the rights, for whom the
// project's status is no secret.
const (
	cannotRecordSupplierInvoice       = "This project is not one you can record a supplier invoice on"
	supplierInvoiceOnCancelledProject = "This project is cancelled, so it takes no supplier invoices"
)

// checkSupplierInvoiceProject is checkProject for a supplier invoice
// (supplier invoices design D2). Who may book one is not the owner's
// CanLogTime but the *recorder's* financial rights on the project — the
// manager role, projects:manage-all, or projects:view-financials on a project
// they see — because the people who receive and re-bill supplier invoices are
// the project's financial side, not necessarily its team. The rights can only
// be the caller's: seesProjectFinancials reads the permissions of whoever is
// asking, so expenses:manage recording one for a colleague needs them too.
//
// A project the invoice already carries is not judged again, the way
// checkProject grandfathers one — but only when the row being replaced is
// itself a supplier invoice: an outlay turned into one is a new booking of
// this kind and is judged in full. The project and the caller's role are read
// here, before any transaction, and the role lands in the cache the response
// shaping reads.
func (s *server) checkSupplierInvoiceProject(ctx context.Context, c *caller, p parsedEntry,
	current *store.ExpensesEntry, add func(field, msg string),
) (*contracts.ProjectEntry, bool, error) {
	if p.ProjectID == nil || !s.projectsAvailable() {
		return nil, false, nil
	}
	kept := current != nil && current.Kind == kindSupplierInvoice &&
		current.ProjectID != nil && *current.ProjectID == *p.ProjectID
	project, err := s.projectsProject(ctx, *p.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("expenses: look up the project: %w", err)
	}
	if project == nil {
		if kept {
			return nil, true, nil
		}
		add("projectId", cannotRecordSupplierInvoice)
		return nil, false, nil
	}
	if !kept {
		role, err := c.role(ctx, s, project.ID)
		if err != nil {
			return nil, false, err
		}
		if !c.seesProjectFinancials(role) {
			add("projectId", cannotRecordSupplierInvoice)
			return nil, false, nil
		}
		if project.Status == projectCancelled {
			add("projectId", supplierInvoiceOnCancelledProject)
			return nil, false, nil
		}
	}
	if err := s.checkLine(ctx, p, current, kept, add); err != nil {
		return nil, false, err
	}
	return project, false, nil
}
```

Replace

```go
	inheritedProject := claim != nil && sameProject(parsed.ProjectID, claim.ProjectID)
	project, projectLost, err := s.checkProject(ctx, owner, parsed, current, inheritedProject, add)
	if err != nil {
		return prepared{}, err
	}
```

with

```go
	inheritedProject := claim != nil && sameProject(parsed.ProjectID, claim.ProjectID)
	var project *contracts.ProjectEntry
	var projectLost bool
	if parsed.Kind == kindSupplierInvoice {
		project, projectLost, err = s.checkSupplierInvoiceProject(ctx, c, parsed, current, add)
	} else {
		project, projectLost, err = s.checkProject(ctx, owner, parsed, current, inheritedProject, add)
	}
	if err != nil {
		return prepared{}, err
	}
```

Replace

```go
	switch p.Kind {
	case kindOutlay:
		v.MarkupPercent = p.MarkupPercent
```

with

```go
	switch p.Kind {
	case kindOutlay, kindSupplierInvoice:
		v.MarkupPercent = p.MarkupPercent
```

Replace

```go
	switch p.Parsed.Kind {
	case kindOutlay:
		markup, err := ratPtrFromNumeric(row.MarkupPercent)
```

with

```go
	switch p.Parsed.Kind {
	case kindOutlay, kindSupplierInvoice:
		markup, err := ratPtrFromNumeric(row.MarkupPercent)
```

Replace

```go
		Now:           s.deps.Clock(),
	}

	var created store.ExpensesEntry
```

with

```go
		Now:           s.deps.Clock(),

		SupplierInvoiceNumber: p.Parsed.InvoiceNumber,
		SupplierDueDate:       optionalPgDate(p.Parsed.DueDate),
	}

	var created store.ExpensesEntry
```

Replace

```go
			Now:           s.deps.Clock(),
		}
		if p.CarryProject {
```

with

```go
			Now:           s.deps.Clock(),

			SupplierInvoiceNumber: p.Parsed.InvoiceNumber,
			SupplierDueDate:       optionalPgDate(p.Parsed.DueDate),
		}
		if p.CarryProject {
```

Replace

```go
// optionalDate is a date filter as the column wants it, invalid (no filter)
```

with

```go
// optionalPgDate is an optional calendar date as a nullable date column wants
// it: invalid (NULL) when there is none.
func optionalPgDate(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgDate(*d)
}

// optionalDate is a date filter as the column wants it, invalid (no filter)
```

and, in the same file's imports, replace

```go
	"strings"

	"github.com/google/uuid"
```

with

```go
	"strings"
	"time"

	"github.com/google/uuid"
```

In `apps/server/internal/expenses/queries/entries.sql`, replace

```sql
    project_id, billing_line_id, billable, markup_percent, bill_rate_per_km, bill_amount,
    created_at, updated_at
) VALUES (
```

with

```sql
    project_id, billing_line_id, billable, markup_percent, bill_rate_per_km, bill_amount,
    supplier_invoice_number, supplier_due_date,
    created_at, updated_at
) VALUES (
```

Replace

```sql
    @project_id, @billing_line_id, @billable, @markup_percent, @bill_rate_per_km, @bill_amount,
    @now::timestamptz, @now::timestamptz
```

with

```sql
    @project_id, @billing_line_id, @billable, @markup_percent, @bill_rate_per_km, @bill_amount,
    @supplier_invoice_number, @supplier_due_date,
    @now::timestamptz, @now::timestamptz
```

Replace

```sql
    bill_amount = @bill_amount,
    status = 'draft',
```

with

```sql
    bill_amount = @bill_amount,
    supplier_invoice_number = @supplier_invoice_number,
    supplier_due_date = @supplier_due_date,
    status = 'draft',
```

In `apps/server/internal/expenses/billing.go`, replace

```go
	switch in.Kind {
	case kindOutlay:
```

with

```go
	switch in.Kind {
	case kindOutlay, kindSupplierInvoice:
```

Replace

```go
		} else if row.Kind != kindOutlay || !body.Billable {
			add("markupPercent", "A markup belongs to a billable outlay")
```

with

```go
		} else if !marksUp(row.Kind) || !body.Billable {
			add("markupPercent", "A markup belongs to a billable outlay or supplier invoice")
```

In `apps/server/internal/expenses/responses.go`, replace

```go
	if vat != nil {
		resp.VatAmount = ptrTo(floatOfRat(vat))
	}
	if row.Kind == kindMileage {
```

with

```go
	if vat != nil {
		resp.VatAmount = ptrTo(floatOfRat(vat))
	}
	// The supplier invoice's own two fields (supplier invoices design D1),
	// shown to everyone who may see the line: they are the supplier's, not
	// the project's money.
	if row.Kind == kindSupplierInvoice {
		resp.InvoiceNumber = row.SupplierInvoiceNumber
		if row.SupplierDueDate.Valid {
			resp.DueDate = &openapi_types.Date{Time: row.SupplierDueDate.Time}
		}
	}
	if row.Kind == kindMileage {
```

Replace

```go
// owedToEmployee is what the owner gets back (design §3.1): the gross of an
// outlay they paid themselves, a mileage line's whole amount, and nothing at
// all for an outlay the company paid. owesEmployee (authorize.go) is the same
// rule as a yes or no, and the reimbursement queries are that rule in SQL.
```

with

```go
// owedToEmployee is what the owner gets back (design §3.1): the gross of an
// outlay they paid themselves, a mileage line's whole amount, and nothing at
// all for an outlay the company paid or for a supplier invoice, which the
// company pays. owesEmployee (authorize.go) is the same rule as a yes or no,
// and expenses.owes_employee is that rule in SQL.
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- gofmt -w internal/expenses && mise exec -- go build ./...
mise exec -- go test -count=1 -run 'TestSupplierInvoices_ARecordedInvoice|TestSupplierInvoices_TheBodyIsRefused|TestSupplierInvoices_WithoutProjects|TestSupplierInvoices_TheRecordersFinancialRights|TestSupplierInvoices_TheOwnerAndManage|TestSupplierInvoices_AreAttested' ./internal/expenses/
```
Expected: the recording, refusal, gate and owner tests PASS; `…_AreAttested…` still FAILS on the submit (no attachment can be uploaded yet: `Only an outlay carries a receipt`).

- [ ] **Step 5: Documents — receipts, the kind change, the queue, the submit rule**

In `apps/server/internal/expenses/attachments.go`, replace

```go
	if entry.Kind != kindOutlay {
		return "Only an outlay carries a receipt; a mileage line and a per diem day have none"
	}
```

with

```go
	if !takesReceipts(entry.Kind) {
		return "Only an outlay carries a receipt, and a supplier invoice its invoice; a mileage line and a per diem day have none"
	}
```

Replace

```go
// receiptsStranded is the message a save carries when it would change the kind
// of an expense that still holds receipts. It is the other side of
// entryTakesReceipts: a receipt belongs to an outlay and to nothing else, so a
// line that stopped being one would leave its receipts where no door of this
```

with

```go
// receiptsStranded is the message a save carries when it would change the kind
// of an expense that still holds receipts. It is the other side of
// entryTakesReceipts: a receipt belongs to an outlay or a supplier invoice and
// to nothing else — the two may become each other, and the documents follow —
// so a line that stopped being either would leave its receipts where no door of this
```

Replace

```go
	if current.Kind != kindOutlay || kind == kindOutlay {
		return false, nil
	}
```

with

```go
	if !takesReceipts(current.Kind) || takesReceipts(kind) {
		return false, nil
	}
```

In `apps/server/internal/expenses/approvals.go`, replace

```go
		// A receipt is what an approver checks an outlay against; mileage and a
		// per diem day take none and are never counted as missing one.
		if row.Kind == kindOutlay && entries[i].AttachmentCount == 0 {
```

with

```go
		// A receipt is what an approver checks an outlay against, and the
		// supplier's invoice what they check a supplier invoice against;
		// mileage and a per diem day take none and are never counted as
		// missing one.
		if takesReceipts(row.Kind) && entries[i].AttachmentCount == 0 {
```

In `apps/server/internal/expenses/flow.go`, replace

```go
			if msg == "" {
				msg, err = receiptRefusal(ctx, txq, c, row)
				if err != nil {
					return err
				}
			}
```

with

```go
			if msg == "" {
				msg, err = receiptRefusal(ctx, txq, c, row)
				if err != nil {
					return err
				}
			}
			if msg == "" {
				msg, err = supplierInvoiceDocumentRefusal(ctx, txq, row)
				if err != nil {
					return err
				}
			}
```

Replace

```go
// decision is one move a batch makes over expenses that are already approved
```

with

```go
// supplierInvoiceDocumentRefusal is the supplier invoice's own document rule
// (supplier invoices design D2): whatever the receipt threshold says, a
// supplier invoice is submitted only with the supplier's invoice attached — at
// least one attachment, through the receipts mechanism. It is judged where the
// receipt rule is, under the entry's own row lock and on the count this
// transaction reads, so an attachment deleted a moment ago cannot let it
// through. A travel claim never holds one, so the claim's submit never asks.
func supplierInvoiceDocumentRefusal(ctx context.Context, txq *store.Queries, row store.ExpensesEntry) (string, error) {
	if row.Kind != kindSupplierInvoice {
		return "", nil
	}
	count, err := txq.CountAttachmentsForEntry(ctx, row.ID)
	if err != nil {
		return "", fmt.Errorf("expenses: count a supplier invoice's attachments: %w", err)
	}
	if count > 0 {
		return "", nil
	}
	return fmt.Sprintf("Expense %d cannot be submitted yet: Attach the supplier's invoice", row.ID), nil
}

// decision is one move a batch makes over expenses that are already approved
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -w internal/expenses && mise exec -- go build ./...
mise exec -- go test -count=1 -run 'TestSupplierInvoices_ADraftChanges|TestSupplierInvoices_SubmitNeeds|TestSupplierInvoices_AreAttested|TestSupplierInvoices_OweNobody|TestSupplierInvoices_TheQueueCounts' ./internal/expenses/
```
Expected: PASS.

- [ ] **Step 6: The project's financial side sees the rows (D4)**

In `apps/server/internal/expenses/authorize.go`, replace

```go
// warmRoles reads c's role on every one of projectIDs into the cache, so a
```

with

```go
// projectScope is a set of projects as a list's query filters on it: every
// project there is (all), or the ones named.
type projectScope struct {
	all bool
	ids []int32
}

// financialProjects is the projects whose supplier invoices the caller sees as
// rows (supplier invoices design D4): the ones they hold financial rights on.
// projects:manage-all is financial rights everywhere, and
// projects:view-financials is financial rights on every project the caller
// sees — with projects:view-all, every project — so both are all, and no
// project is asked about. Otherwise it is the caller's own projects on which
// seesProjectFinancials holds, read through role so the answers are cached
// beside the ones accessFor shapes the rows with: the list filtered on these
// ids and the read of any one of them can never disagree. Without
// view-financials the only financial right is the manager role, whose projects
// managedProjects already puts in the list whole, so nothing is asked. Without
// the projects module there are none.
//
// It is managedProjects' N+1 again, for the same reason and at the same price:
// ProjectsForUser answers no roles. A caller who sees every expense never asks.
func (c *caller) financialProjects(ctx context.Context, s *server) (projectScope, error) {
	scope := projectScope{ids: []int32{}}
	if !s.projectsAvailable() {
		return scope, nil
	}
	if c.ProjectsManageAll || (c.ProjectsFinancials && c.ProjectsViewAll) {
		scope.all = true
		return scope, nil
	}
	if !c.ProjectsFinancials {
		return scope, nil
	}
	projects, err := s.projectsForUser(ctx, c.UserID)
	if err != nil {
		return projectScope{}, fmt.Errorf("expenses: list the caller's projects: %w", err)
	}
	for _, p := range projects {
		role, err := c.role(ctx, s, p.ID)
		if err != nil {
			return projectScope{}, err
		}
		if c.seesProjectFinancials(role) {
			scope.ids = append(scope.ids, p.ID)
		}
	}
	return scope, nil
}

// warmRoles reads c's role on every one of projectIDs into the cache, so a
```

Replace

```go
// CanSee is design §5's visibility: its owner, a manager of its project, and
// expenses:view-all, expenses:approve and expenses:manage; anyone else gets
// the bare 404 an unknown id gets.
```

with

```go
// CanSee is design §5's visibility: its owner, a manager of its project, and
// expenses:view-all, expenses:approve and expenses:manage — and, for a
// supplier invoice alone, everyone with financial rights on its project
// (supplier invoices design D4: it carries no personal data, and it is the
// project's financial side that receives and re-bills it); anyone else gets
// the bare 404 an unknown id gets.
```

Replace

```go
	a.CanSee = a.IsOwner || a.IsManager || c.seesEveryone()
	// Every billing answer is the projects module's, so none of them is true
```

with

```go
	a.CanSee = a.IsOwner || a.IsManager || c.seesEveryone() ||
		(entry.Kind == kindSupplierInvoice && c.ProjectsOn && entry.ProjectID != nil && c.seesProjectFinancials(role))
	// Every billing answer is the projects module's, so none of them is true
```

In `apps/server/internal/expenses/queries/entries.sql`, replace

```sql
-- makes a line visible exactly when its claim is.
--
-- The status and the reimbursed filters
```

with

```sql
-- makes a line visible exactly when its claim is.
--
-- A supplier invoice is visible to one set more (supplier invoices design D4):
-- everyone with financial rights on its project — supplier_invoices_all for
-- projects:manage-all, and for projects:view-financials with
-- projects:view-all; otherwise the ids financialProjects (authorize.go)
-- resolved through the project directory before the query, the way
-- managed_project_ids is. It carries no personal data; an employee's outlay
-- on the same project keeps the rule above.
--
-- The status and the reimbursed filters
```

Replace

```sql
SELECT count(*) FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE (@see_all::boolean
       OR e.user_id = @caller_id::uuid
       OR (e.project_id IS NOT NULL AND e.project_id = ANY(@managed_project_ids::integer[])))
```

with

```sql
SELECT count(*) FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE (@see_all::boolean
       OR e.user_id = @caller_id::uuid
       OR (e.project_id IS NOT NULL AND e.project_id = ANY(@managed_project_ids::integer[]))
       OR (e.kind = 'supplier_invoice' AND e.project_id IS NOT NULL
           AND (@supplier_invoices_all::boolean OR e.project_id = ANY(@financial_project_ids::integer[]))))
```

Replace

```sql
SELECT e.* FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE (@see_all::boolean
       OR e.user_id = @caller_id::uuid
       OR (e.project_id IS NOT NULL AND e.project_id = ANY(@managed_project_ids::integer[])))
```

with

```sql
SELECT e.* FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE (@see_all::boolean
       OR e.user_id = @caller_id::uuid
       OR (e.project_id IS NOT NULL AND e.project_id = ANY(@managed_project_ids::integer[]))
       OR (e.kind = 'supplier_invoice' AND e.project_id IS NOT NULL
           AND (@supplier_invoices_all::boolean OR e.project_id = ANY(@financial_project_ids::integer[]))))
```

In `apps/server/internal/expenses/entries.go`, replace

```go
// and expenses:manage, the caller's own, and everything on the projects the
// caller manages. So the total is the number of expenses the caller may see,
// every page is full but the last, and the list never holds an expense its own
// read answers 404 for.
```

with

```go
// and expenses:manage, the caller's own, everything on the projects the
// caller manages, and the supplier invoices on the projects whose money they
// may see (supplier invoices design D4). So the total is the number of
// expenses the caller may see, every page is full but the last, and the list
// never holds an expense its own read answers 404 for.
```

Replace

```go
	managed := []int32{}
	if !c.seesEveryone() {
		if managed, err = c.managedProjects(ctx, s); err != nil {
			return nil, err
		}
	}
```

with

```go
	managed := []int32{}
	financial := projectScope{ids: []int32{}}
	if !c.seesEveryone() {
		if managed, err = c.managedProjects(ctx, s); err != nil {
			return nil, err
		}
		if financial, err = c.financialProjects(ctx, s); err != nil {
			return nil, err
		}
	}
```

Replace

```go
		ToInvoice:         p.ToInvoice,
	}
```

with

```go
		ToInvoice:         p.ToInvoice,

		SupplierInvoicesAll: financial.all,
		FinancialProjectIds: financial.ids,
	}
```

Replace

```go
		ToInvoice:         filter.ToInvoice,
		PageSize:          pageSize,
```

with

```go
		ToInvoice:         filter.ToInvoice,
		PageSize:          pageSize,

		SupplierInvoicesAll: filter.SupplierInvoicesAll,
		FinancialProjectIds: filter.FinancialProjectIds,
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- gofmt -w internal/expenses && mise exec -- go build ./...
mise exec -- go test -count=1 -run 'TestSupplierInvoices_TheProjectsFinancialSide|TestExpensesEntries|TestExpensesClaims_VisibilityIsTheEntryRuleAppliedToTheClaim' ./internal/expenses/
```
Expected: PASS — the visibility test, and every existing list and visibility test unchanged (`entries_visibility_test.go` among them).

- [ ] **Step 7: The picker's kind and the summary's capability**

Replace the whole of `apps/server/internal/expenses/projectoptions.go` with:

```go
package expenses

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is GET /projects: the projects the caller may book an expense on,
// with the billing lines they may book it against, so the expense form needs
// no code of the projects module's own (design §6, "Project options"). It is
// the one operation that does not exist at all without that module — decision
// X2's "there is no project field anywhere" taken to its conclusion.
//
// kind=supplier_invoice is the same picker for the one kind that is not booked
// on CanLogTime (supplier invoices design D2): the projects the caller holds
// financial rights on and that are not cancelled.

// GetExpensesProjects List the projects an expense may be booked on
// (GET /api/v1/expenses/projects)
//
// The directory is asked for the person's projects, then for each one whether
// they may book on it (the rule a create is held to, so the picker can never
// offer a project the save would refuse), and then for its billing lines. That
// is one call per project on each of two counts: contracts.ProjectDirectory
// offers no bulk form of either, and widening the contract for a picker is a
// worse trade than three small reads of an in-process module. A caller holds
// few enough projects for it.
//
// The person is the caller unless userId names somebody else, which is the
// same rule and the same field the create answers: a save is judged on what
// the expense's *owner* may book on (checkProject), so an administrator
// recording for a colleague has to be offered the colleague's projects or the
// picker would offer what the save then refuses. Naming anybody else needs
// expenses:manage, which is what lets one record for somebody else at all.
//
// kind=supplier_invoice is judged on the *caller*, as the save of one is
// (checkSupplierInvoiceProject), so naming somebody else beside it is refused
// on kind rather than quietly answered with the caller's projects.
func (s *server) GetExpensesProjects(ctx context.Context, req gen.GetExpensesProjectsRequestObject) (gen.GetExpensesProjectsResponseObject, error) {
	if !s.projectsAvailable() {
		return gen.GetExpensesProjects404ApplicationProblemPlusJSONResponse(projectsNotInstalled()), nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	if kind := filterValue(req.Params.Kind); kind != nil {
		switch *kind {
		case kindOutlay, kindMileage:
			// The picker these two kinds have always had.
		case kindSupplierInvoice:
			if id := req.Params.UserId; id != nil && *id != c.UserID {
				return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(invalidQueryFields(fieldError("kind",
					"A supplier invoice is booked on the recorder's own financial rights: the right to record one is the recorder's, so its projects are never somebody else's"))), nil
			}
			return s.supplierInvoiceProjects(ctx, c)
		default:
			return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(invalidQueryFields(fieldError("kind",
				fmt.Sprintf("'%s' is not a kind this picker narrows by; it takes outlay, mileage or supplier_invoice", *kind)))), nil
		}
	}
	userID := c.UserID
	if id := req.Params.UserId; id != nil && *id != c.UserID {
		if !c.Manage {
			return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(invalidQueryFields(fieldError(
				"userId", "Listing somebody else's projects needs the Manage expenses permission"))), nil
		}
		user, err := s.usersUser(ctx, *id)
		if err != nil {
			return nil, fmt.Errorf("expenses: look up the person the picker is for: %w", err)
		}
		if user == nil || !user.Active {
			return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(
				invalidQueryFields(fieldError("userId", "Nobody active has that user id"))), nil
		}
		userID = user.ID
	}
	projects, err := s.projectsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the person's projects: %w", err)
	}

	options := make([]gen.ExpensesProjectOption, 0, len(projects))
	for _, project := range projects {
		allowed, err := s.projectsCanLogTime(ctx, project.ID, userID)
		if err != nil {
			return nil, fmt.Errorf("expenses: check the person may book on a project: %w", err)
		}
		if !allowed {
			continue
		}
		option, err := s.projectOption(ctx, project)
		if err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return gen.GetExpensesProjects200JSONResponse(options), nil
}

// supplierInvoiceProjects is the picker for a supplier invoice: the caller's
// own projects on which seesProjectFinancials holds and that are not
// cancelled — exactly the projects checkSupplierInvoiceProject would let this
// caller record one on, so the picker never offers what the save refuses. It
// can only list projects the caller holds a role on, because that is what
// ProjectsForUser answers; a projects:manage-all holder on no team records
// from the project page instead, whose summary hands the project over.
func (s *server) supplierInvoiceProjects(ctx context.Context, c *caller) (gen.GetExpensesProjectsResponseObject, error) {
	projects, err := s.projectsForUser(ctx, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the caller's projects: %w", err)
	}
	options := make([]gen.ExpensesProjectOption, 0, len(projects))
	for _, project := range projects {
		if project.Status == projectCancelled {
			continue
		}
		role, err := c.role(ctx, s, project.ID)
		if err != nil {
			return nil, err
		}
		if !c.seesProjectFinancials(role) {
			continue
		}
		option, err := s.projectOption(ctx, project)
		if err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return gen.GetExpensesProjects200JSONResponse(options), nil
}

// projectOption is one project as a booking picker offers it: its code, its
// name, its currency and its active billing lines — an inactive one is
// refused on a save, so offering it would be offering a mistake.
func (s *server) projectOption(ctx context.Context, project contracts.ProjectEntry) (gen.ExpensesProjectOption, error) {
	lines, err := s.projectsBillingLines(ctx, project.ID)
	if err != nil {
		return gen.ExpensesProjectOption{}, fmt.Errorf("expenses: list a project's billing lines: %w", err)
	}
	offered := make([]gen.ExpensesProjectOptionBillingLine, 0, len(lines))
	for _, line := range lines {
		if line.Active {
			offered = append(offered, gen.ExpensesProjectOptionBillingLine{Id: line.ID, Code: line.Code})
		}
	}
	return gen.ExpensesProjectOption{
		Id: project.ID, Code: project.Code, Name: project.Name,
		Currency: project.Currency, BillingLines: offered,
	}, nil
}
```

In `apps/server/internal/expenses/projectsummary.go`, replace

```go
	canRecord, err := s.projectsCanLogTime(ctx, req.ProjectId, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("expenses: check the caller may book on the project: %w", err)
	}
```

with

```go
	canRecord, err := s.projectsCanLogTime(ctx, req.ProjectId, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("expenses: check the caller may book on the project: %w", err)
	}
	// A supplier invoice is recorded by whoever holds the project's financial
	// rights — which this caller does, or the summary would already have
	// answered 404 — on any project but a cancelled one (supplier invoices
	// design D2). The project comes with it as a booking option, because the
	// caller the button exists for is often on no project team, and
	// GET /projects, a picker of what the caller may log time on, offers them
	// nothing to open the form with.
	canRecordSupplierInvoice := project.Status != projectCancelled
	var option *gen.ExpensesProjectOption
	if canRecordSupplierInvoice {
		one, err := s.projectOption(ctx, *project)
		if err != nil {
			return nil, err
		}
		option = &one
	}
```

Replace

```go
	response, err := projectSummaryResponse(totals[req.ProjectId], project.Currency, canRecord)
```

with

```go
	response, err := projectSummaryResponse(totals[req.ProjectId], project.Currency,
		gen.ExpensesProjectSummaryCapabilities{CanRecord: canRecord, CanRecordSupplierInvoice: &canRecordSupplierInvoice},
		option)
```

Replace

```go
func projectSummaryResponse(totals contracts.ProjectExpenseTotals, projectCurrency *string,
	canRecord bool,
) (gen.ExpensesProjectSummaryResponse, error) {
```

with

```go
func projectSummaryResponse(totals contracts.ProjectExpenseTotals, projectCurrency *string,
	capabilities gen.ExpensesProjectSummaryCapabilities, project *gen.ExpensesProjectOption,
) (gen.ExpensesProjectSummaryResponse, error) {
```

Replace

```go
		Capabilities:    gen.ExpensesProjectSummaryCapabilities{CanRecord: canRecord},
	}
```

with

```go
		Capabilities:    capabilities,
		Project:         project,
	}
```

- [ ] **Step 8: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go generate ./... && mise exec -- gofmt -w internal/expenses
test -z "$(mise exec -- gofmt -l internal)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/expenses/... ./internal/openapi/... ./internal/db/... ./internal/integration/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git diff --stat -- openapi/COVERAGE.md
```
Expected: PASS — every `TestSupplierInvoices_…` test, every existing expenses test unchanged, and the module's coverage gate (`RequireCoverage` in `main_test.go`, no new operation). `COVERAGE.md` may not move; commit it only if it did.

Prove the tests can fail, restoring after each: drop `kindSupplierInvoice` from `entryKinds` — every supplier-invoice test goes red on `kind`; in `parseSupplierInvoice` store `body.PaidBy` as given — `…_TheBodyIsRefused…` goes red on `paidBy=employee` and `…_OweNobody` stays green only because the function answers false for the kind (then also swap the function's `THEN false` for `THEN true` in a scratch migration to see `…_OweNobody` go red on all five surfaces); replace `!c.seesProjectFinancials(role)` with `false` in `checkSupplierInvoiceProject` — the member and "not seen" cases go red; delete the `project.Status == projectCancelled` refusal — the cancelled case goes red; delete the `supplierInvoiceDocumentRefusal` call — `…_SubmitNeeds…` goes red; delete the supplier-invoice disjunct in `accessFor` — the detail, the document and the forbidden-delete of `…_FinancialSide…` go red (and the SQL one — the list assertions); use `row.Kind == kindOutlay` in `approvals.go` again — `…_TheQueueCounts…` goes red; `case kindOutlay:` alone in `resolveValues` — the recorded invoice bills nothing and `…_ARecordedInvoice…` goes red; drop the `projectCancelled` skip in `supplierInvoiceProjects` — the picker test goes red with 1006. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-2.txt <<'MSG'
feat(expenses): a supplier invoice is recorded, attested, priced and shown to the project's financial side

A fourth kind, supplier_invoice: the outlay's money with a supplier and
the supplier's invoice number required and an optional due date on or
after the invoice date, always paid by the company, never in a travel
claim, always on a project and refused without the projects module.
Whoever holds financial rights on the project records one — a completed
project included, a cancelled one refused — and it is priced with the
outlay's markup, carries the supplier's invoice through the receipts
mechanism, and is submitted only with it attached. Its rows are visible
to the project's financial side. GET /projects?kind=supplier_invoice is
its picker, and the project summary answers canRecordSupplierInvoice and
the project to record it on.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
PATHS="openapi/expenses.yaml apps/server/internal/openapi/specs/expenses.yaml apps/server/internal/expenses/gen/api.gen.go \
 apps/expenses/frontend/src/api-schema.d.ts apps/server/internal/expenses/entries_validation.go \
 apps/server/internal/expenses/entries.go apps/server/internal/expenses/queries/entries.sql \
 apps/server/internal/expenses/authorize.go apps/server/internal/expenses/billing.go \
 apps/server/internal/expenses/attachments.go apps/server/internal/expenses/approvals.go \
 apps/server/internal/expenses/flow.go apps/server/internal/expenses/responses.go \
 apps/server/internal/expenses/projectoptions.go apps/server/internal/expenses/projectsummary.go \
 apps/server/internal/expenses/harness_test.go apps/server/internal/expenses/supplier_invoices_test.go \
 $(git status --short -- apps/server/internal/expenses/store | awk '{print $2}')"
git status --short   # add openapi/COVERAGE.md to PATHS if it moved
git add $PATHS && git commit -F /tmp/claude-1000/msg-si-2.txt -- $PATHS
git show --stat HEAD && git status --short
```

---
### Task 3: The contract carries the supplier invoices as a sub-figure (D3, the provider half)

`contracts.ExpenseSplit` and `CurrencyExpenses.SupplierInvoices` (additive, nil when a currency has none); `ProjectExpenseGroups` groups by a supplier-invoice flag; the fold sums the split beside the buckets in exact decimals; the project summary renders it as `supplierInvoices` on each currency. Every existing figure keeps meaning "everything".

**Files:**
- Create: `apps/server/internal/expenses/supplier_invoice_figures_test.go`
- Modify: `apps/server/internal/contracts/expenses.go`, `apps/server/internal/expenses/queries/projectexpenses.sql`, `apps/server/internal/expenses/projectexpenses.go`, `apps/server/internal/expenses/projectsummary.go`, `openapi/expenses.yaml`, `apps/server/internal/expenses/harness_test.go`, `openapi/COVERAGE.md` (if it moves)
- Generated (commit them): `apps/server/internal/expenses/store/projectexpenses.sql.go`, `apps/server/internal/openapi/specs/expenses.yaml`, `apps/server/internal/expenses/gen/api.gen.go`, `apps/expenses/frontend/src/api-schema.d.ts`
- Read first (do not change): `contracts/expenses.go` whole, `expenses/projectexpenses.go` whole, `expenses/queries/projectexpenses.sql` whole, `expenses/projectsummary.go:89-178`, `expenses/projectexpenses_test.go:115-230` (`recordedExpense`, `recordExpense`, `projectExpensesOf`, `currencyOf`, `wantBucket`), `expenses/harness_test.go:1080-1160` (`summaryCurrencyJSON`, `rawProjectSummary`, `summaryCurrency`), `projects/harness_test.go:505-640` (the one other `ProjectExpenses` fake), `customers/overview_test.go:515-600` (a consumer's fakes)

**Interfaces:**
- Consumes: `kindSupplierInvoice` (Task 1), `projectSummaryResponse`'s new signature (Task 2).
- Produces Go: `contracts.ExpenseSplit{Approved, Submitted, Draft, Total ExpenseBucket}`; `contracts.CurrencyExpenses.SupplierInvoices *ExpenseSplit`; `store.ProjectExpenseGroupsRow.SupplierInvoice bool`; `splitSum`, `pickBucket`, `sumOf`, `(*splitSum).split() *contracts.ExpenseSplit`.
- Wire: new schema `ExpensesProjectSummarySupplierInvoices {approved, submitted, draft, total: ExpensesProjectSummaryBucket}` (all four required); `ExpensesProjectSummaryCurrency.supplierInvoices` (optional; absent when the currency has none).
- No fake of `ProjectExpenses` needs to change to compile — the field is an additive pointer, and every fake in the tree (`projects/harness_test.go`'s `fakeExpenses`, customers' `overview_test.go` literals) builds `CurrencyExpenses` by field name. Projects' fake gains its helper for the new field in Task 4.

- [ ] **Step 1: The contract field, and a test that fails**

In `apps/server/internal/contracts/expenses.go`, replace

```go
	UnpricedCount int64
}
```

with

```go
	UnpricedCount int64
	// SupplierInvoices is the part of this currency's three buckets and their
	// total that is supplier invoices (supplier invoices design D3) — nil when
	// the currency holds none. It is a sub-figure, never a split of the
	// figures above: Approved, Submitted, Draft and Total keep meaning every
	// line, supplier invoices included, so a consumer that knows nothing of
	// this field reads exactly what it always read. Its buckets follow the
	// same rules as those — the unit's status, rejected as draft, cost the
	// net, bill the billable lines' stored amounts, each rounded once on its
	// own — and its Total is rounded once from the unrounded whole, as Total
	// is.
	SupplierInvoices *ExpenseSplit
}
```

Replace

```go
	BillAmount string
}
```

with

```go
	BillAmount string
}

// ExpenseSplit is one kind's share of a currency's buckets: the same three
// buckets and the across-bucket total, over the lines of that kind alone. It
// is only ever the supplier invoices' (CurrencyExpenses.SupplierInvoices).
type ExpenseSplit struct {
	Approved, Submitted, Draft, Total ExpenseBucket
}
```

In `apps/server/internal/expenses/harness_test.go`, replace

```go
		UnpricedCount  int32             `json:"unpricedCount"`
	}
)
```

with

```go
		UnpricedCount  int32             `json:"unpricedCount"`
		// SupplierInvoices is the part of the buckets that is supplier
		// invoices, absent when the currency has none.
		SupplierInvoices *summarySupplierInvoicesJSON `json:"supplierInvoices"`
	}
	summarySupplierInvoicesJSON struct {
		Approved  summaryBucketJSON `json:"approved"`
		Submitted summaryBucketJSON `json:"submitted"`
		Draft     summaryBucketJSON `json:"draft"`
		Total     summaryBucketJSON `json:"total"`
	}
)
```

Create `apps/server/internal/expenses/supplier_invoice_figures_test.go`:

```go
package expenses_test

import (
	"testing"
)

// What a project's supplier invoices cost and bill, as a sub-figure of what
// all its expenses do (supplier invoices design D3): per currency, in the
// three buckets the unit's status puts a line in and their total, nil when a
// currency has none — while every existing figure keeps meaning everything.

func TestProjectExpenses_SupplierInvoicesAreASubFigureOfEveryBucket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	for _, e := range []recordedExpense{
		{project: projectKraftVerket, gross: "100.00", paidBy: "employee", status: "draft"},
		{project: projectKraftVerket, kind: "supplier_invoice", gross: "1250.00", vat: "250.00", paidBy: "company",
			billable: true, billAmount: "1100.00", status: "approved"},
		{project: projectKraftVerket, kind: "supplier_invoice", gross: "500.00", paidBy: "company", status: "submitted"},
		// Rejected is back with its recorder, which is where a draft is.
		{project: projectKraftVerket, kind: "supplier_invoice", gross: "200.00", paidBy: "company", status: "rejected"},
		{project: projectKraftVerket, currency: "EUR", gross: "90.00", paidBy: "company", status: "approved"},
	} {
		recordExpense(t, h, user, e)
	}

	totals := projectExpensesOf(t, p, projectKraftVerket)
	nok := currencyOf(t, totals, "NOK")
	// Every existing figure is still everything.
	wantBucket(t, "NOK approved", nok.Approved, 1, "1000.00", "1100.00")
	wantBucket(t, "NOK submitted", nok.Submitted, 1, "500.00", "0.00")
	wantBucket(t, "NOK draft", nok.Draft, 2, "300.00", "0.00")
	wantBucket(t, "NOK total", nok.Total, 4, "1800.00", "1100.00")

	si := nok.SupplierInvoices
	if si == nil {
		t.Fatal("NOK carries no supplier invoice figure; three supplier invoices were recorded in it")
	}
	wantBucket(t, "NOK supplier invoices approved", si.Approved, 1, "1000.00", "1100.00")
	wantBucket(t, "NOK supplier invoices submitted", si.Submitted, 1, "500.00", "0.00")
	wantBucket(t, "NOK supplier invoices draft", si.Draft, 1, "200.00", "0.00")
	wantBucket(t, "NOK supplier invoices total", si.Total, 3, "1700.00", "1100.00")

	// A currency with none carries no sub-figure at all, rather than zeroes.
	if eur := currencyOf(t, totals, "EUR"); eur.SupplierInvoices != nil {
		t.Errorf("EUR supplier invoices = %+v, want nil: nothing in EUR is a supplier invoice", eur.SupplierInvoices)
	}
}

func TestExpensesProjectSummary_SupplierInvoicesHaveTheirOwnLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, financeID := financeReader(t, h)
	recordExpense(t, h, financeID, recordedExpense{project: projectKraftVerket, kind: "supplier_invoice",
		gross: "1250.00", vat: "250.00", paidBy: "company", status: "approved"})
	recordExpense(t, h, financeID, recordedExpense{project: projectKraftVerket, gross: "100.00", paidBy: "employee"})
	recordExpense(t, h, financeID, recordedExpense{project: projectEuro, currency: "EUR", gross: "90.00", paidBy: "employee"})

	nok := summaryCurrency(t, getProjectSummary(t, finance, projectKraftVerket), "NOK")
	if nok.Total.Count != 2 || nok.Total.Cost != 1100 {
		t.Errorf("NOK total = %+v, want both lines, 1100 cost: the totals are everything", nok.Total)
	}
	si := nok.SupplierInvoices
	if si == nil || si.Approved.Count != 1 || si.Approved.Cost != 1000 || si.Draft.Count != 0 || si.Total.Count != 1 || si.Total.Cost != 1000 {
		t.Errorf("NOK supplierInvoices = %+v, want the one approved invoice costing 1000", si)
	}

	raw := rawProjectSummary(t, finance, projectEuro)
	currencies, _ := raw["currencies"].([]any)
	if len(currencies) != 1 {
		t.Fatalf("EUR project currencies = %v, want one", raw["currencies"])
	}
	if _, present := currencies[0].(map[string]any)["supplierInvoices"]; present {
		t.Errorf("a currency with no supplier invoice answers supplierInvoices: %v", currencies[0])
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestProjectExpenses_SupplierInvoicesAreASubFigureOfEveryBucket|TestExpensesProjectSummary_SupplierInvoicesHaveTheirOwnLine' ./internal/expenses/
```
Expected: FAIL — `NOK carries no supplier invoice figure` and `NOK supplierInvoices = <nil>`.

- [ ] **Step 2: The grouped flag and the fold**

In `apps/server/internal/expenses/queries/projectexpenses.sql`, replace

```sql
-- after the groups have been added up in exact decimals.
```

with

```sql
-- after the groups have been added up in exact decimals.
--
-- supplier_invoice is the one kind the answer tells apart (supplier invoices
-- design D3): the groups are split by it so the fold can carry the supplier
-- invoices' share of each bucket beside the buckets. It splits groups and
-- changes no sum — every bucket the fold publishes is still every line.
```

Replace

```sql
       END AS bucket,
       count(*) AS line_count,
```

with

```sql
       END AS bucket,
       (e.kind = 'supplier_invoice')::boolean AS supplier_invoice,
       count(*) AS line_count,
```

Replace

```sql
GROUP BY e.project_id,
         e.currency,
         CASE COALESCE(c.status, e.status)
```

with

```sql
GROUP BY e.project_id,
         e.currency,
         (e.kind = 'supplier_invoice'),
         CASE COALESCE(c.status, e.status)
```

In `apps/server/internal/expenses/projectexpenses.go`, replace

```go
// currencyExpenseSum is one currency's figures while they are being added up:
// the three buckets, and the four figures that span them.
type currencyExpenseSum struct {
	approved, submitted, draft expenseBucketSum
	readyCount                 int64
	ready                      big.Rat
	invoicedCount              int64
	invoiced                   big.Rat
	unpricedCount              int64
}
```

with

```go
// currencyExpenseSum is one currency's figures while they are being added up:
// the three buckets, the four figures that span them, and the supplier
// invoices' share of the buckets (supplier invoices design D3), summed beside
// them rather than instead of them — every bucket above keeps meaning every
// line.
type currencyExpenseSum struct {
	approved, submitted, draft expenseBucketSum
	readyCount                 int64
	ready                      big.Rat
	invoicedCount              int64
	invoiced                   big.Rat
	unpricedCount              int64
	supplier                   splitSum
}

// splitSum is one kind's share of the three buckets while it is being added up.
type splitSum struct {
	approved, submitted, draft expenseBucketSum
}
```

Replace

```go
	bucket := &currency.draft
	switch row.Bucket {
	case statusApproved:
		bucket = &currency.approved
	case statusSubmitted:
		bucket = &currency.submitted
	}
	bucket.count += row.LineCount

	cost, err := exactDecimal(row.CostAmount)
	if err != nil {
		return err
	}
	bucket.cost.Add(&bucket.cost, cost)

	bill, err := exactDecimal(row.BillAmount)
	if err != nil {
		return err
	}
	bucket.bill.Add(&bucket.bill, bill)
```

with

```go
	cost, err := exactDecimal(row.CostAmount)
	if err != nil {
		return err
	}
	bill, err := exactDecimal(row.BillAmount)
	if err != nil {
		return err
	}
	// A supplier invoice's group lands in its bucket like any other and, a
	// second time, in the supplier invoices' own share of that bucket.
	into := []*expenseBucketSum{pickBucket(row.Bucket, &currency.approved, &currency.submitted, &currency.draft)}
	if row.SupplierInvoice {
		into = append(into, pickBucket(row.Bucket, &currency.supplier.approved, &currency.supplier.submitted, &currency.supplier.draft))
	}
	for _, bucket := range into {
		bucket.count += row.LineCount
		bucket.cost.Add(&bucket.cost, cost)
		bucket.bill.Add(&bucket.bill, bill)
	}
```

Replace

```go
		UnpricedCount:  c.unpricedCount,
	}
}
```

with

```go
		UnpricedCount:  c.unpricedCount,

		SupplierInvoices: c.supplier.split(),
	}
}
```

Replace

```go
func (c *currencyExpenseSum) total() contracts.ExpenseBucket {
	var whole expenseBucketSum
	for _, bucket := range []*expenseBucketSum{&c.approved, &c.submitted, &c.draft} {
		whole.count += bucket.count
		whole.cost.Add(&whole.cost, &bucket.cost)
		whole.bill.Add(&whole.bill, &bucket.bill)
	}
	return whole.bucket()
}
```

with

```go
func (c *currencyExpenseSum) total() contracts.ExpenseBucket {
	whole := sumOf(&c.approved, &c.submitted, &c.draft)
	return whole.bucket()
}

// split renders the supplier invoices' share for the contract, nil when there
// is none — "absent when none" is the provider's answer, so every consumer's
// shaping by absence starts here. Its Total is the three buckets as one
// unrounded sum, rounded once, for the reason total gives.
func (s *splitSum) split() *contracts.ExpenseSplit {
	whole := sumOf(&s.approved, &s.submitted, &s.draft)
	if whole.count == 0 {
		return nil
	}
	return &contracts.ExpenseSplit{
		Approved:  s.approved.bucket(),
		Submitted: s.submitted.bucket(),
		Draft:     s.draft.bucket(),
		Total:     whole.bucket(),
	}
}

// sumOf is buckets added up exactly, before anything is rounded.
func sumOf(buckets ...*expenseBucketSum) *expenseBucketSum {
	var whole expenseBucketSum
	for _, bucket := range buckets {
		whole.count += bucket.count
		whole.cost.Add(&whole.cost, &bucket.cost)
		whole.bill.Add(&whole.bill, &bucket.bill)
	}
	return &whole
}

// pickBucket is the bucket a group's unit status puts it in; anything that is
// neither approved nor submitted — a draft or a rejected unit — is a draft.
func pickBucket(status string, approved, submitted, draft *expenseBucketSum) *expenseBucketSum {
	switch status {
	case statusApproved:
		return approved
	case statusSubmitted:
		return submitted
	}
	return draft
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- gofmt -w internal/expenses internal/contracts && mise exec -- go build ./...
mise exec -- go test -count=1 -run 'TestProjectExpenses' ./internal/expenses/
```
Expected: PASS — the new test and every existing `ProjectExpenses` test (their totals unchanged). If sqlc names the flag's field other than `SupplierInvoice`, use its name and report it.

- [ ] **Step 3: The summary's own line**

In `openapi/expenses.yaml`, in `ExpensesProjectSummaryCurrency`, replace

```yaml
                submitted:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                total:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                unpricedCount:
```

with

```yaml
                submitted:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                supplierInvoices:
                    allOf:
                        - $ref: '#/components/schemas/ExpensesProjectSummarySupplierInvoices'
                    description: The part of the buckets and the total above that is supplier invoices (supplier invoices design D3). Absent when this currency holds none. It is a line of its own beneath the totals, never a split of them — every bucket above still counts every line.
                total:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                unpricedCount:
```

Replace

```yaml
        ExpensesRateOverrideRequest:
```

with

```yaml
        ExpensesProjectSummarySupplierInvoices:
            description: What a project's supplier invoices in one currency cost and bill, in the same three buckets as everything else and their total — the total rounded once from the unrounded whole, never the three added up. Each bucket is already inside the currency's own bucket of that status.
            properties:
                approved:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                draft:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                submitted:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
                total:
                    $ref: '#/components/schemas/ExpensesProjectSummaryBucket'
            required:
                - approved
                - submitted
                - draft
                - total
            type: object
        ExpensesRateOverrideRequest:
```

In `apps/server/internal/expenses/projectsummary.go`, replace

```go
		if published.InvoicedAmount, err = summaryAmount(currency.InvoicedAmount); err != nil {
			return gen.ExpensesProjectSummaryResponse{}, err
		}
		currencies = append(currencies, published)
```

with

```go
		if published.InvoicedAmount, err = summaryAmount(currency.InvoicedAmount); err != nil {
			return gen.ExpensesProjectSummaryResponse{}, err
		}
		// The supplier invoices' own line (supplier invoices design D3),
		// absent when the currency holds none — the contract's nil.
		if split := currency.SupplierInvoices; split != nil {
			line := gen.ExpensesProjectSummarySupplierInvoices{}
			for _, bucket := range []struct {
				from contracts.ExpenseBucket
				into *gen.ExpensesProjectSummaryBucket
			}{
				{split.Approved, &line.Approved},
				{split.Submitted, &line.Submitted},
				{split.Draft, &line.Draft},
				{split.Total, &line.Total},
			} {
				if *bucket.into, err = summaryBucket(bucket.from); err != nil {
					return gen.ExpensesProjectSummaryResponse{}, err
				}
			}
			published.SupplierInvoices = &line
		}
		currencies = append(currencies, published)
```

- [ ] **Step 4: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go generate ./... && mise exec -- gofmt -w internal/expenses
test -z "$(mise exec -- gofmt -l internal)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/expenses/... ./internal/projects/... ./internal/customers/... ./internal/openapi/... ./internal/integration/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git diff --stat -- openapi/COVERAGE.md
```
Expected: PASS — projects' and customers' suites unchanged (their fakes set no split, so their consumers see nil), the integration figures unchanged.

Prove the tests can fail, restoring after each: drop `(e.kind = 'supplier_invoice')` from the `GROUP BY` (regenerate) — sqlc or PostgreSQL refuses the ungrouped column, and with the column dropped too the sub-figure goes nil and both tests go red; add the supplier group to its share only (`into := []*expenseBucketSum{}` when `row.SupplierInvoice`) — the "every existing figure is still everything" assertions go red with 0-count approved; return a zeroed split instead of nil in `split` — the EUR assertion and the absent-key assertion go red; render `split.Approved` into `line.Total` — the summary test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-3.txt <<'MSG'
feat(expenses): what a project's supplier invoices cost is counted apart, inside every figure

contracts.CurrencyExpenses gains SupplierInvoices, an ExpenseSplit of the
three buckets and their total over the supplier invoices alone — nil when
a currency has none — while every existing figure keeps meaning every
line. ProjectExpenseGroups splits its groups by the kind and the fold sums
the share beside the buckets in exact decimals, each figure rounded once.
The project summary answers it as supplierInvoices on each currency.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
PATHS="apps/server/internal/contracts/expenses.go apps/server/internal/expenses/queries/projectexpenses.sql \
 apps/server/internal/expenses/store/projectexpenses.sql.go apps/server/internal/expenses/projectexpenses.go \
 apps/server/internal/expenses/projectsummary.go openapi/expenses.yaml apps/server/internal/openapi/specs/expenses.yaml \
 apps/server/internal/expenses/gen/api.gen.go apps/expenses/frontend/src/api-schema.d.ts \
 apps/server/internal/expenses/harness_test.go apps/server/internal/expenses/supplier_invoice_figures_test.go"
git status --short   # add openapi/COVERAGE.md if it moved
git add $PATHS && git commit -F /tmp/claude-1000/msg-si-3.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 4: The economy's expenses block reports the supplier invoices (D3, the consumer half)

Projects shapes the contract's sub-figure into `expenses.supplierInvoices` — the project's own currency only, the block's own bucket shape, absent when none — and changes nothing else in the answer.

**Files:**
- Modify: `openapi/projects.yaml`, `apps/server/internal/projects/economy.go`, `apps/server/internal/projects/economy_math.go`, `apps/server/internal/projects/harness_test.go`, `apps/server/internal/projects/economy_expenses_test.go`, `openapi/COVERAGE.md` (if it moves)
- Generated (commit them): `apps/server/internal/openapi/specs/projects.yaml`, `apps/server/internal/projects/gen/api.gen.go`, `apps/projects/frontend/src/api-schema.d.ts`
- Read first (do not change): `projects/economy.go:150-290,480-531`, `projects/economy_math.go:370-570`, `projects/economy_expenses_test.go` whole (the golden test and `TestGetProjectEconomy_RefusesAnExpenseTotalThatContradictsItsBuckets`), `projects/harness_test.go:505-660,1240-1380`

**Interfaces:**
- Consumes: `contracts.ExpenseSplit`, `CurrencyExpenses.SupplierInvoices` (Task 3).
- Produces Go: `expenseSplit{Approved, Submitted, Draft, Total expenseSum}`, `currencyExpenses.SupplierInvoices *expenseSplit`; test helpers `(*fakeExpenses).setSupplierInvoices(projectID int32, currency string, split contracts.ExpenseSplit)`, `spentSplit(approved, submitted, draft contracts.ExpenseBucket) contracts.ExpenseSplit`, `economySupplierInvoicesJSON`.
- Wire: new schema `ProjectEconomySupplierInvoices {approved, submitted, draft, total: ProjectEconomyExpenseBucket}` (all required); `ProjectEconomyExpenses.supplierInvoices` (optional, not nullable; absent when the project's own currency has none or the project carries no currency). A provider whose split contradicts itself (its total count is not its buckets' counts, or more than the currency's lines) is a 500, as a contradicting total already is.

- [ ] **Step 1: The contract**

In `openapi/projects.yaml`, in `ProjectEconomyExpenses`, replace

```yaml
                    description: What is waiting for a decision. Absent when the project carries no currency.
```

with

```yaml
                    description: What is waiting for a decision. Absent when the project carries no currency.
                supplierInvoices:
                    allOf:
                        - $ref: '#/components/schemas/ProjectEconomySupplierInvoices'
                    description: The part of the three buckets and the totals that is supplier invoices, in the project's own currency — shown as "of which supplier invoices" beneath the totals (supplier invoices design D3). Absent when none has been recorded in the project's currency, and on a project that carries no currency. It is a sub-figure, never a split of the block's figures, so none of those changes meaning and the margin is unchanged.
```

Replace

```yaml
        ProjectEconomyTotals:
```

with

```yaml
        ProjectEconomySupplierInvoices:
            description: What the project's supplier invoices in its own currency cost the company and will charge the customer, in the same three buckets as every expense and the across-bucket total, which is rounded once from the unrounded whole rather than the buckets added up. Each bucket is already inside the expenses block's bucket of that status.
            properties:
                approved:
                    $ref: '#/components/schemas/ProjectEconomyExpenseBucket'
                draft:
                    $ref: '#/components/schemas/ProjectEconomyExpenseBucket'
                submitted:
                    $ref: '#/components/schemas/ProjectEconomyExpenseBucket'
                total:
                    $ref: '#/components/schemas/ProjectEconomyExpenseBucket'
            required:
                - approved
                - submitted
                - draft
                - total
            type: object
        ProjectEconomyTotals:
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go test -count=1 ./internal/openapi/... && mise exec -- go build ./...
```
Expected: PASS; `gen.ProjectEconomyExpenses` gains `SupplierInvoices *ProjectEconomySupplierInvoices`.

- [ ] **Step 2: The fake learns the field; the tests are written and fail**

In `apps/server/internal/projects/harness_test.go`, replace

```go
func (f *fakeExpenses) set(projectID int32, totals contracts.ProjectExpenseTotals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[projectID] = totals
}
```

with

```go
func (f *fakeExpenses) set(projectID int32, totals contracts.ProjectExpenseTotals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[projectID] = totals
}

// setSupplierInvoices says which part of one currency's figures, already set
// for projectID, is supplier invoices (supplier invoices design D3) — the
// sub-figure the real provider carries beside the buckets and leaves nil when
// a currency has none. set comes first; this only adds the split to it.
func (f *fakeExpenses) setSupplierInvoices(projectID int32, currency string, split contracts.ExpenseSplit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	totals := f.entries[projectID]
	currencies := slices.Clone(totals.Currencies)
	for i := range currencies {
		if currencies[i].Currency == currency {
			one := split
			currencies[i].SupplierInvoices = &one
		}
	}
	totals.Currencies = currencies
	f.entries[projectID] = totals
}

// spentSplit is a supplier-invoice split with Total derived from its three
// buckets, the way spentInCurrency derives a currency's.
func spentSplit(approved, submitted, draft contracts.ExpenseBucket) contracts.ExpenseSplit {
	return contracts.ExpenseSplit{
		Approved: approved, Submitted: submitted, Draft: draft,
		Total: contracts.ExpenseBucket{
			Count:      approved.Count + submitted.Count + draft.Count,
			CostAmount: addedAmounts(approved.CostAmount, submitted.CostAmount, draft.CostAmount),
			BillAmount: addedAmounts(approved.BillAmount, submitted.BillAmount, draft.BillAmount),
		},
	}
}
```

Replace

```go
	OtherCurrencies []expenseCurrencyJSON `json:"otherCurrencies"`
	LastEntryDate   *string               `json:"lastEntryDate"`
}
```

with

```go
	OtherCurrencies []expenseCurrencyJSON `json:"otherCurrencies"`
	LastEntryDate   *string               `json:"lastEntryDate"`
	// SupplierInvoices is the part of the buckets that is supplier invoices,
	// absent when there are none.
	SupplierInvoices *economySupplierInvoicesJSON `json:"supplierInvoices"`
}

// economySupplierInvoicesJSON decodes ProjectEconomySupplierInvoices.
type economySupplierInvoicesJSON struct {
	Approved  expenseBucketJSON `json:"approved"`
	Submitted expenseBucketJSON `json:"submitted"`
	Draft     expenseBucketJSON `json:"draft"`
	Total     expenseBucketJSON `json:"total"`
}
```

Append to `apps/server/internal/projects/economy_expenses_test.go`:

```go
// Supplier invoices are a sub-figure of the expenses block (supplier invoices
// design D3): the part of each bucket that is supplier invoices, in the
// project's own currency, shaped exactly as the block's buckets are — and
// nothing else in the answer moves, because the buckets, the totals and the
// margin already count every expense.
func TestGetProjectEconomy_SupplierInvoicesAreASubFigureOfTheExpenses(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOSUP1000")
	expenses.set(project.Id, recordedExpenses("2026-09-19", spentNOK(
		spentInCurrency("NOK",
			spentBucket(3, "1200.00", "1500.00"),
			spentBucket(2, "800.00", "900.00"),
			spentBucket(1, "300.00", "0.00")),
		2, "1100.00", 1, "400.00", 0)))
	before := rawEconomy(t, manager, project.Id)

	expenses.setSupplierInvoices(project.Id, "NOK", spentSplit(
		spentBucket(1, "1000.00", "1100.00"), spentBucket(1, "500.00", "600.00"), spentBucket(0, "0.00", "0.00")))

	si := getEconomy(t, manager, project.Id).Expenses.SupplierInvoices
	if si == nil {
		t.Fatal("expenses.supplierInvoices is absent; the provider reported two supplier invoices in NOK")
	}
	for name, got := range map[string]struct{ got, want expenseBucketJSON }{
		"approved":  {si.Approved, expenseBucketJSON{Count: 1, Cost: 1000, Amount: 1100}},
		"submitted": {si.Submitted, expenseBucketJSON{Count: 1, Cost: 500, Amount: 600}},
		"draft":     {si.Draft, expenseBucketJSON{Count: 0, Cost: 0, Amount: 0}},
		"total":     {si.Total, expenseBucketJSON{Count: 2, Cost: 1500, Amount: 1700}},
	} {
		if got.got != got.want {
			t.Errorf("supplierInvoices.%s = %+v, want %+v", name, got.got, got.want)
		}
	}

	after := rawEconomy(t, manager, project.Id)
	block, _ := after["expenses"].(map[string]any)
	delete(block, "supplierInvoices")
	if !reflect.DeepEqual(before, after) {
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(after)
		t.Errorf("the rest of the economy moved with the sub-figure.\nbefore %s\nafter  %s", b, a)
	}
}

// Absent when none: a project whose own currency holds no supplier invoice
// answers no key — not zeroes — even when another currency does, because the
// sub-figure is the project's own currency's alone, like every figure beside
// it.
func TestGetProjectEconomy_WithoutSupplierInvoicesInItsCurrencyTheKeyIsAbsent(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOSUP2000")
	eur := spentInCurrency("EUR", spentBucket(1, "90.00", "100.00"), spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	split := spentSplit(spentBucket(1, "90.00", "100.00"), spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	eur.SupplierInvoices = &split
	expenses.set(project.Id, recordedExpenses("2026-09-19",
		spentNOK(spentInCurrency("NOK", spentBucket(1, "100.00", "0.00"), spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")),
			0, "0.00", 0, "0.00", 0),
		eur))

	block, _ := rawEconomy(t, manager, project.Id)["expenses"].(map[string]any)
	if _, present := block["supplierInvoices"]; present {
		t.Errorf("expenses.supplierInvoices = %v, want it absent: nothing in NOK is a supplier invoice", block["supplierInvoices"])
	}
}

// A split whose own total disagrees with its buckets, or that claims more
// lines than the currency has, is the provider contradicting itself: a 500,
// exactly as a contradicting total is, rather than a sub-figure nobody can
// trust.
func TestGetProjectEconomy_RefusesASupplierSplitThatContradictsItself(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOSUP3000")
	nok := spentInCurrency("NOK", spentBucket(2, "100.00", "120.00"), spentBucket(1, "50.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	split := spentSplit(spentBucket(2, "100.00", "120.00"), spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	split.Total.Count = 7 // two in the buckets, seven in the total, three lines in the currency
	nok.SupplierInvoices = &split
	expenses.set(project.Id, recordedExpenses("2026-09-19", nok))

	r := readEconomy(t, manager, project.Id,
		modtest.SkipContract("a provider contradicting itself is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500", r.Status, r.Body)
	}
}
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestGetProjectEconomy_SupplierInvoices|TestGetProjectEconomy_WithoutSupplierInvoices|TestGetProjectEconomy_RefusesASupplierSplit' ./internal/projects/
```
Expected: FAIL — the sub-figure is absent and the contradicting split answers 200.

- [ ] **Step 3: The shaping**

In `apps/server/internal/projects/economy_math.go`, replace

```go
type currencyExpenses struct {
	Currency                          string
	Approved, Submitted, Draft, Total expenseSum
```

with

```go
type currencyExpenses struct {
	Currency                          string
	Approved, Submitted, Draft, Total expenseSum
	// SupplierInvoices is the supplier invoices' share of the buckets and the
	// total (supplier invoices design D3), nil when the provider reported none.
	SupplierInvoices *expenseSplit
```

Replace

```go
// expenseFigures is one project's expenses split the way every surface here
```

with

```go
// expenseSplit is one kind's share of a currency's buckets, as exact decimals:
// the three buckets and the provider's own across-bucket total.
type expenseSplit struct {
	Approved, Submitted, Draft, Total expenseSum
}

// expenseFigures is one project's expenses split the way every surface here
```

Replace

```go
	out.ReadyAmount, out.InvoicedAmount = ready, invoiced
	return out, nil
}
```

with

```go
	out.ReadyAmount, out.InvoicedAmount = ready, invoiced
	if c.SupplierInvoices != nil {
		split, err := expenseSplitOf(c.Currency, *c.SupplierInvoices, c.Total.Count)
		if err != nil {
			return currencyExpenses{}, err
		}
		out.SupplierInvoices = split
	}
	return out, nil
}

// expenseSplitOf converts the supplier invoices' share of one currency. The
// counts are exact, so the provider's split contradicts itself — and is
// refused, as currencyExpensesOf refuses a contradicting total — when its
// total is not its buckets added up, or when it claims more lines than the
// currency holds at all: a share bigger than the whole is not a share.
func expenseSplitOf(currency string, s contracts.ExpenseSplit, lines int64) (*expenseSplit, error) {
	if buckets := s.Approved.Count + s.Submitted.Count + s.Draft.Count; s.Total.Count != buckets || s.Total.Count > lines {
		return nil, fmt.Errorf(
			"projects: the expenses provider reports %d %s supplier invoices in total, %d across their buckets and %d lines in the currency",
			s.Total.Count, currency, buckets, lines)
	}
	out := &expenseSplit{}
	for _, pair := range []struct {
		from contracts.ExpenseBucket
		to   *expenseSum
	}{
		{s.Approved, &out.Approved},
		{s.Submitted, &out.Submitted},
		{s.Draft, &out.Draft},
		{s.Total, &out.Total},
	} {
		cost, err := exactAmount(pair.from.CostAmount)
		if err != nil {
			return nil, err
		}
		bill, err := exactAmount(pair.from.BillAmount)
		if err != nil {
			return nil, err
		}
		pair.to.Count, pair.to.Cost, pair.to.Bill = pair.from.Count, cost, bill
	}
	return out, nil
}
```

In `apps/server/internal/projects/economy.go`, replace

```go
		out.UnpricedCount = &unpricedCount
	}
```

with

```go
		out.UnpricedCount = &unpricedCount
		// The supplier invoices' share (supplier invoices design D3), in the
		// same currency and the same bucket shape, and absent when there is
		// none: a sub-figure of the block, never a second block, so nothing
		// above changes meaning and the margin is untouched.
		if split := own.SupplierInvoices; split != nil {
			out.SupplierInvoices = &gen.ProjectEconomySupplierInvoices{
				Approved: *bucket(split.Approved), Submitted: *bucket(split.Submitted),
				Draft: *bucket(split.Draft), Total: *bucket(split.Total),
			}
		}
	}
```

- [ ] **Step 4: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/projects
test -z "$(mise exec -- gofmt -l internal)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 -run 'TestGetProjectEconomy_WithoutExpensesTheAnswerIsUnchanged|TestGetProjectsEconomy_WithoutExpensesTheAnswerIsUnchanged' ./internal/projects/   # the two golden bodies, unchanged
mise exec -- go test -count=1 ./internal/projects/... ./internal/openapi/... ./internal/integration/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git diff --stat -- openapi/COVERAGE.md
```
Expected: PASS — the three new tests, the golden economy body of an installation without expenses (`TestGetProjectEconomy_WithoutExpensesTheAnswerIsUnchanged`, byte for byte: no key appears without the contract) and the portfolio's golden body, every other projects test unchanged, the coverage gate green. The portfolio's golden test is `TestGetProjectsEconomy_WithoutExpensesTheAnswerIsUnchanged` (`portfolio_expenses_test.go`).

Prove the tests can fail, restoring after each: delete the `if split := own.SupplierInvoices` block — the sub-figure test goes red; shape it outside `if own := f.Own` from the first currency found — the absent-key test goes red with EUR's figures; delete the `s.Total.Count > lines` clause and set `split.Total.Count = 4` with buckets of 4 in the contradiction test — it goes red with a 200; render `split.Approved` as `Total` — the total assertion goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-4.txt <<'MSG'
feat(projects): the economy's expenses block says how much of it is supplier invoices

ProjectEconomyExpenses gains supplierInvoices, the supplier invoices'
share of the three buckets and the totals in the project's own currency,
shaped as the block's own buckets and absent when there are none. It is a
sub-figure: every existing figure, the margin, the budget and the
portfolio are unchanged. A provider whose split contradicts itself is a
500, as a contradicting total already is.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
PATHS="openapi/projects.yaml apps/server/internal/openapi/specs/projects.yaml apps/server/internal/projects/gen/api.gen.go \
 apps/projects/frontend/src/api-schema.d.ts apps/server/internal/projects/economy.go \
 apps/server/internal/projects/economy_math.go apps/server/internal/projects/harness_test.go \
 apps/server/internal/projects/economy_expenses_test.go"
git status --short   # add openapi/COVERAGE.md if it moved
git add $PATHS && git commit -F /tmp/claude-1000/msg-si-4.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 5: Projects and Expenses composed for real (the integration test)

A finance reader — `projects:view-financials` with `projects:view-all`, on no team — records a supplier invoice on a **completed** project through the real Expenses module, attaches the invoice, and the real Projects economy, reading the real provider through `Compose`, shows it in the sub-figure: in `draft` when recorded, in `approved` once an approver has approved it.

**Files:**
- Create: `apps/server/internal/integration/supplier_invoices_test.go`
- Read first (do not change): `integration/harness_test.go` whole (`newInstallation`, `moduleNamed`, `recorder`, `fakeCustomers`, `signInAdmin`, `okJSON`, the path constants), `integration/installations_test.go:150-185` (`createProject`), `integration/work_types_test.go` (the shape of a single-test file), `modtest/modtest.go:289-299` (`WithObjectStore`), `storage/fs.go:50-75` (`NewFS`)

**Interfaces:**
- Consumes: everything from Tasks 1–4, over HTTP only.
- Produces: `TestSupplierInvoices_AFinanceReaderRecordsOneOnACompletedProject`, `subcontractorCategory` (this package's own constant).

- [ ] **Step 1: Write the test**

Create `apps/server/internal/integration/supplier_invoices_test.go`:

```go
package integration_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// subcontractorCategory is the second category 00012 seeds, after
// materialsCategory.
const subcontractorCategory = 1002

// TestSupplierInvoices_AFinanceReaderRecordsOneOnACompletedProject is the
// supplier invoices design end to end (D1–D4): the real expenses module takes
// a supplier invoice from somebody who holds the project's financial rights
// and no role on it — judged through Compose's real project directory, on a
// project already completed, which CanLogTime would refuse — and the real
// economy reads it back through the real provider as the expenses block's
// supplier-invoice sub-figure, the net as its cost and, at the default 0 %
// markup, as what it bills:
//
//	12 500 gross − 2 500 VAT = 10 000 cost, 10 000 billed
func TestSupplierInvoices_AFinanceReaderRecordsOneOnACompletedProject(t *testing.T) {
	t.Parallel()
	// newInstallation composes no object store, and the supplier's invoice
	// goes through the receipts door, so this installation is composed with
	// the real filesystem store, rooted in a directory it creates itself
	// (restrictively permissioned — an existing temp directory would not be).
	store, err := storage.NewFS(filepath.Join(t.TempDir(), "objects"), false, false)
	if err != nil {
		t.Fatalf("open a receipt store: %v", err)
	}
	h := modtest.New(t,
		modtest.WithRecorder(recorder),
		modtest.WithDirectory(fakeCustomers{}),
		modtest.WithModule(moduleNamed(t, modProjects)),
		modtest.WithModule(moduleNamed(t, modExpenses)),
		modtest.WithObjectStore(store),
	)
	admin, _ := signInAdmin(t, h)
	project := createProject(t, admin)
	// The work is done; the subcontractor's invoice arrives afterwards.
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id),
		map[string]any{"status": "completed"}, nil)

	finance, financeID := h.SignInUser(t, "projects:access", "projects:view-all", "projects:view-financials", "expenses:access")
	var invoice struct {
		Id             int64   `json:"id"`
		Kind           string  `json:"kind"`
		Status         string  `json:"status"`
		OwedToEmployee float64 `json:"owedToEmployee"`
		Owner          struct {
			UserId string `json:"userId"`
		} `json:"owner"`
	}
	okJSON(t, finance, http.MethodPost, expensesEntries, map[string]any{
		"kind": "supplier_invoice", "entryDate": "2026-09-22", "description": "Rørleggerarbeid, uke 38",
		"categoryId": subcontractorCategory, "supplier": "Rør & Varme AS", "invoiceNumber": "F-20260922",
		"dueDate": "2026-10-22", "currency": "NOK", "grossAmount": 12500, "vatAmount": 2500,
		"projectId": project.Id, "billable": true,
	}, &invoice)
	if invoice.Kind != "supplier_invoice" || invoice.Status != "draft" || invoice.OwedToEmployee != 0 ||
		invoice.Owner.UserId != financeID.String() {
		t.Fatalf("recorded = %+v, want the finance reader's draft supplier invoice, owed to nobody", invoice)
	}

	type splitBucket struct {
		Count  int32   `json:"count"`
		Cost   float64 `json:"cost"`
		Amount float64 `json:"amount"`
	}
	type economy struct {
		Expenses *struct {
			TotalCost        float64 `json:"totalCost"`
			SupplierInvoices *struct {
				Approved splitBucket `json:"approved"`
				Draft    splitBucket `json:"draft"`
				Total    splitBucket `json:"total"`
			} `json:"supplierInvoices"`
		} `json:"expenses"`
	}
	read := func() economy {
		var e economy
		okJSON(t, finance, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &e)
		if e.Expenses == nil || e.Expenses.SupplierInvoices == nil {
			t.Fatalf("economy expenses = %+v, want the supplier invoice sub-figure", e.Expenses)
		}
		return e
	}
	want := splitBucket{Count: 1, Cost: 10000, Amount: 10000}
	if got := read(); got.Expenses.SupplierInvoices.Draft != want || got.Expenses.SupplierInvoices.Total != want ||
		got.Expenses.TotalCost != 10000 {
		t.Errorf("recorded: supplierInvoices = %+v, totalCost %v, want the invoice in draft costing 10000",
			got.Expenses.SupplierInvoices, got.Expenses.TotalCost)
	}

	// The supplier's invoice goes through the receipts door, the submit
	// accepts it, and an approver's approval moves it in the economy too.
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="faktura.pdf"`},
		"Content-Type":        {"application/pdf"},
	})
	if err != nil {
		t.Fatalf("build the upload: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")); err != nil {
		t.Fatalf("write the upload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the upload: %v", err)
	}
	r := finance.Do(http.MethodPost, fmt.Sprintf("%s/%d/attachments", expensesEntries, invoice.Id), nil,
		modtest.RawBody(w.FormDataContentType(), form.Bytes()))
	if r.Status != http.StatusCreated {
		t.Fatalf("attach the invoice: status %d body %s, want 201", r.Status, r.Body)
	}
	okJSON(t, finance, http.MethodPost, expensesSubmit, map[string]any{"entryIds": []int64{invoice.Id}}, nil)
	okJSON(t, admin, http.MethodPost, expensesApprove, map[string]any{"entryIds": []int64{invoice.Id}}, nil)

	if got := read(); got.Expenses.SupplierInvoices.Approved != want || got.Expenses.SupplierInvoices.Draft != (splitBucket{}) {
		t.Errorf("approved: supplierInvoices = %+v, want the invoice in approved and none in draft", got.Expenses.SupplierInvoices)
	}
}
```

- [ ] **Step 2: Run it, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal/integration
mise exec -- go test -count=1 -run 'TestSupplierInvoices_AFinanceReaderRecordsOneOnACompletedProject' ./internal/integration/
mise exec -- go test -count=1 ./internal/integration/...
```
Expected: PASS.

Prove it can fail, restoring after each: in `checkSupplierInvoiceProject` call `s.projectsCanLogTime` instead of `seesProjectFinancials` — the create is refused on `projectId` (completed project, no role); in `economy.go` delete the supplier-invoice block — `want the supplier invoice sub-figure`; in `pickBucket` return `draft` for everything — the approved assertion goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-5.txt <<'MSG'
test(integration): a finance reader's supplier invoice on a completed project reaches the economy

The real expenses module takes a supplier invoice from somebody holding
the project's financial rights and no role on it, on a project already
completed, through Compose's real project directory; the real economy
reads it back through the real provider as the expenses block's
supplier-invoice sub-figure, first in draft and, once approved, in
approved.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
PATHS="apps/server/internal/integration/supplier_invoices_test.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-si-5.txt -- $PATHS
git show --stat HEAD && git status --short
```

---
### Task 6: The docs (D6)

`docs/expenses.md` learns the fourth kind and the function; `docs/projects.md` the economy's `supplierInvoices`; `docs/module-boundaries.md` the contract's new sub-figure; `ROADMAP.md` phase 3 C done. Every sentence is checked against the code of Tasks 1–5 (Step 5). This task touches no code and may run beside Tasks 7 and 8.

**Files:**
- Modify: `docs/expenses.md`, `docs/projects.md`, `docs/module-boundaries.md`, `ROADMAP.md`
- Read first (do not change): `docs/expenses.md:1-160,573-590,692-845,879-942,1061-1124`, `docs/projects.md:644-690,766-780,1360-1385`, `docs/module-boundaries.md:190-220`, `ROADMAP.md:520-600,686-720`, and the code of Tasks 1–5

- [ ] **Step 1: `docs/expenses.md` — the kind, the rule, the one date**

Replace

```markdown
The Expenses module is outlays, mileage and per diem days, standalone or
```

with

```markdown
The Expenses module is outlays, supplier invoices, mileage and per diem days, standalone or
```

Replace

```markdown
  **owed to the employee** means) · [The optional Projects link](#the-optional-projects-link)
```

with

```markdown
  **owed to the employee** means) · [The optional Projects link](#the-optional-projects-link) ·
  [The supplier invoice](#the-supplier-invoice)
```

Replace

```markdown
- **Entry** (`expenses.entries`) — one money line: an **outlay**, a **mileage**
  line or a **per diem day** (`kind`), owned by `userId`, dated `entryDate`, with a
```

with

```markdown
- **Entry** (`expenses.entries`) — one money line: an **outlay**, a **supplier
  invoice**, a **mileage** line or a **per diem day** (`kind`), owned by `userId`, dated `entryDate`, with a
```

Replace

```markdown
    `vatAmount`, and up to ten receipts.
```

with

```markdown
    `vatAmount`, and up to ten receipts.
  - A **supplier invoice** (`supplier_invoice`, never inside a travel claim,
    always on a project): the outlay's money — `categoryId`, `currency`,
    `grossAmount`, optional `vatAmount` — plus a required `supplier`, the
    supplier's `invoiceNumber` and an optional `dueDate`; the company always pays
    it, and its PDF is attached as a receipt is. See "The supplier invoice" below.
```

Replace

```markdown
  - The first two can carry a `projectId`, optionally a `billingLineId`, a
```

with

```markdown
  - An outlay and a mileage line can carry a `projectId` (a supplier invoice
    always does), optionally a `billingLineId`, a
```

Replace

```markdown
- a line whose gross rounds to nothing owes nothing, so it can never be put on
```

with

```markdown
- a **supplier invoice** owes **nobody**: the company pays the supplier, so it
  never reaches the reimbursement list, the payroll CSV, the unreimbursed figures
  or the reimbursement attention item — whatever its `paidBy` says, and it always
  says `company`;
- a line whose gross rounds to nothing owes nothing, so it can never be put on
```

Replace

```markdown
One rule, written twice on purpose and kept in step: `owesEmployee` in
`authorize.go` decides whether a single unit may be marked reimbursed, and
`ListReimbursementRows` applies the same predicate in SQL to build the list. A
```

with

```markdown
**The SQL function and its Go mirror.** Who is owed is one rule written twice on
purpose: `expenses.owes_employee(kind, paid_by)`, an `IMMUTABLE` SQL function
(migration `00033`) that every query asking the question calls — the
reimbursement list, the payroll CSV, a payroll run, the claim totals, the
unreimbursed figures and the reimbursement attention item — and `owesEmployee`
in `authorize.go`, its Go mirror, which decides a single row's
`canMarkReimbursed` and `owedToEmployee`. A test asks both about every kind and
every payer and fails on any pair they disagree about. "Owes something" also
needs a gross above zero, which the SQL callers say beside the function. A
```

Directly before `## The unit an expense belongs to`, add:

```markdown
## The supplier invoice

The invoice a supplier sends for work or goods on a project is a kind of its own,
`supplier_invoice` (supplier invoices design D1–D4). It is recording, attesting,
re-billing and reporting — **not accounts payable**: there is no paid/unpaid state,
no vendor register, no inbound e-invoice and no payment export. Paying the supplier
happens outside Vantigo.

- **Its fields.** The outlay's money — a `categoryId` (the form preselects
  *Subcontractor* when that category exists), a `currency` in any ISO code, a
  `grossAmount` above zero and an optional `vatAmount` from zero to the gross; the
  net, gross less VAT, is its cost and its markup's base — plus a **required**
  `supplier` (at most 200 characters), a **required** `invoiceNumber` (the
  supplier's own, trimmed, at most 100 characters, free text and not unique, since
  no supplier record exists to make it unique under) and an optional `dueDate`, on
  or after the entry date. Mileage and per diem fields are refused, as on an
  outlay; `invoiceNumber` and `dueDate` are refused on every other kind. The
  columns are `supplier_invoice_number` and `supplier_due_date`, named clear of
  the *outgoing* stamp `invoiced_at`/`invoice_reference`.
- **One date.** The entry date **is the invoice date** — the one the period lock
  judges. There is no second date column.
- **Company-paid, always.** `paidBy` may be left out or say `company`, and is
  stored `company`; `employee` is refused ("A supplier invoice is paid by the
  company"). It owes nobody (see "What owed to the employee is").
- **Never in a claim, always on a project.** A `claimId` is refused ("A supplier
  invoice is not a travel claim line"), so is a missing `projectId` ("A supplier
  invoice is booked on a project"), and in an installation without the projects
  module the kind itself is refused with the same sentence.
- **Who may record one.** Whoever holds **financial rights on the project** — its
  manager, `projects:manage-all`, or `projects:view-financials` on a project they
  see — rather than projects' `CanLogTime`: the people who receive and re-bill
  supplier invoices are the project's financial side, not necessarily its team.
  The project may be **completed** — an invoice often arrives after the work — and
  only a **cancelled** project refuses. The right is the recorder's: recording one
  for a colleague still needs `expenses:manage`, and financial rights on the
  project as well. The recorder is the owner, as for any expense: it lists under
  their expenses, and they edit and submit it. A project the invoice already
  carries is not judged again; an outlay turned into a supplier invoice is.
- **The flow is the module's.** Draft → submitted → approved | rejected, approved
  by `expenses:approve` or the project's manager, self-approval allowed, unapprove
  refused once invoiced. **The supplier's invoice is required on submit**: at least
  one attachment, through the receipts mechanism, or the submit answers "Expense
  *id* cannot be submitted yet: Attach the supplier's invoice". A draft may change
  between outlay and supplier invoice — both carry documents, so nothing strands.
- **Priced as an outlay.** Billable needs a billable project; the markup is the
  one named, the one the line keeps, or the settings' default; what it bills is
  net × (1 + markup %). The pricing door, the manual invoiced stamp and "ready to
  invoice" apply unchanged.
- **Visible to the project's financial side.** It carries no personal data, so
  beside its owner, the project's manager and `expenses:view-all`/`approve`/
  `manage`, **everyone with financial rights on its project** sees it — in the
  list, the project's list, a single read and its document. An employee's outlay
  on the same project keeps the ordinary rule.
- **Counted apart.** Every figure of a project's expenses still counts it; the
  supplier invoices' own share is reported beside them as `supplierInvoices` — see
  [What a project's expenses come to](#what-a-projects-expenses-come-to).
- **Its picker.** `GET /projects?kind=supplier_invoice` is the caller's own
  projects on which they hold financial rights and that are not cancelled — the
  projects the save would accept from them. A `projects:manage-all` holder on no
  team records from the project page, whose summary hands the project over.

```

Replace

```markdown
The rule only ever applies to an **employee-paid outlay**
```

with

```markdown
A **supplier invoice** has a rule of its own that no threshold moves: it is never
submitted without the supplier's invoice attached (see
[The supplier invoice](#the-supplier-invoice)), judged the same way — at submit
time, under the entry's own row lock, on the count that transaction reads.

The rule only ever applies to an **employee-paid outlay**
```

- [ ] **Step 2: `docs/expenses.md` — the figures, the page, visibility, receipts, the API, what comes next**

Replace

```markdown
| `capabilities.canRecord` | whether *this caller* may book a cost on the project (projects' own `CanLogTime`), so a "Record a cost" button never offers what the save would refuse |
```

with

```markdown
| `capabilities.canRecord` | whether *this caller* may book a cost on the project (projects' own `CanLogTime`), so a "Record a cost" button never offers what the save would refuse |
| `supplierInvoices` (per currency) | the part of `approved` / `submitted` / `draft` / `total` that is supplier invoices, each bucket already inside the one above it — a line of its own, never a split of them. Absent when the currency holds none |
| `capabilities.canRecordSupplierInvoice` | whether *this caller* may record a supplier invoice on the project: financial rights (which reading the summary already needs) on a project that is not cancelled |
| `project` | the project as a booking option — code, name, currency, active billing lines — present exactly when `canRecordSupplierInvoice` is, so "Record a supplier invoice" can open for a finance reader `GET /projects` offers nothing |
```

Replace

```markdown
| `projects:view-financials` without managing it | every figure | **their own expenses on the project, and nothing else** — which for most such callers is nothing at all, and the tab then says so in words: "You can see this project's totals, but not the individual expenses behind them" |
```

with

```markdown
| `projects:view-financials` without managing it | every figure | **the project's supplier invoices, and their own expenses on it** — a supplier invoice is the project's financial side's to see, a colleague's outlay is not; where there are neither, the tab says so in words: "You can see this project's totals, but not the individual expenses behind them" |
```

Replace

```markdown
in the client re-derives it from a permission.
```

with

```markdown
in the client re-derives it from a permission.

**Record a supplier invoice** stands beside it when `capabilities.canRecordSupplierInvoice`
is true. It opens the same form fixed to this project and to the supplier-invoice
kind — the supplier, the invoice number, the invoice date, the due date, the
category (Subcontractor preselected when it exists), gross and VAT, billable on to
start with, a sentence that the company pays it, and the dropzone for the
supplier's invoice. The project comes from the summary's own `project`, because the
reader this button exists for is often on no project team. Each card's totals
carry an **"of which supplier invoices"** row beneath the total, and the list shows
the kind like any other.
```

Replace

```markdown
Who sees an expense at all: its owner, always; the manager of its project, whoever
recorded it; and `expenses:view-all`, `expenses:approve` and `expenses:manage`, who
see everyone's.
```

with

```markdown
Who sees an expense at all: its owner, always; the manager of its project, whoever
recorded it; and `expenses:view-all`, `expenses:approve` and `expenses:manage`, who
see everyone's. A **supplier invoice** is seen by one set more: everyone with
financial rights on its project — `projects:manage-all`, or
`projects:view-financials` on a project they see — because it carries no personal
data and it is the project's financial side that receives and re-bills it.
```

Replace

```markdown
friendly figure the contract itself uses), and an outlay carries at most ten. Only
an outlay takes one at all — neither a mileage line nor a per diem day ever does,
and a save that would turn an outlay with receipts into a line of another kind is
refused (on `kind`) rather than stranding them.
```

with

```markdown
friendly figure the contract itself uses), and an outlay or a supplier invoice
carries at most ten. Only those two take one at all — the supplier invoice's is the
supplier's own invoice, and it is required on submit — neither a mileage line nor a
per diem day ever does, and a save that would turn a line with receipts into one of
those two is refused (on `kind`) rather than stranding them. An outlay and a
supplier invoice may become each other freely: the receipts follow.
```

Replace

```markdown
| `GET /entries` (`userId`, `projectId`, `claimId`, `standalone`, `status`, `kind`, `from`, `to`, `reimbursed`, `toInvoice`, paging) | The caller's own; a project manager also sees their projects'; view-all/approve/manage see everyone's. `toInvoice=true` additionally needs financial rights on the `projectId` named — 403 otherwise |
```

with

```markdown
| `GET /entries` (`userId`, `projectId`, `claimId`, `standalone`, `status`, `kind` — `supplier_invoice` included, `from`, `to`, `reimbursed`, `toInvoice`, paging) | The caller's own; a project manager also sees their projects'; financial rights on a project show its supplier invoices; view-all/approve/manage see everyone's. `toInvoice=true` additionally needs financial rights on the `projectId` named — 403 otherwise |
```

Replace

```markdown
| `POST /entries/{id}/attachments`, `DELETE /attachments/{id}` | Same as edit, an outlay only |
```

with

```markdown
| `POST /entries/{id}/attachments`, `DELETE /attachments/{id}` | Same as edit, an outlay or a supplier invoice only |
```

Replace

```markdown
| `GET /projects` | The caller's own bookable projects (or, `userId`, a colleague's, with `expenses:manage`) |
```

with

```markdown
| `GET /projects` (`userId`, `kind`) | The caller's own bookable projects (or, `userId`, a colleague's, with `expenses:manage`); `kind=supplier_invoice` is the caller's own projects they hold financial rights on that are not cancelled |
```

Replace

```markdown
**Supplier costs** are the other named gap: a project's non-hours cost today is what
somebody put on an expense, not what a supplier invoiced — see
[ROADMAP.md](../ROADMAP.md#projects).
```

with

```markdown
**Supplier invoices** are done: a supplier's invoice is recorded as what it is —
[the supplier invoice](#the-supplier-invoice) — attested, re-billed and counted
apart in the project's economy. What is deliberately still not here is accounts
payable: a paid/unpaid state, a supplier register, inbound e-invoices and a
payment export are purchasing's, when purchasing comes — see
[ROADMAP.md](../ROADMAP.md#projects).
```

Run

```bash
cd /home/anders/projects/vantigo/vantigo
grep -n "one money line\|The SQL function and its Go mirror\|## The supplier invoice\|canRecordSupplierInvoice\|Record a supplier invoice\|Supplier invoices\*\* are done" docs/expenses.md
grep -n "^| \`GET /entries\` \|^| \`POST /entries/{id}/attachments\`\|^| \`GET /projects\` \|^| \`projects:view-financials\` without" docs/expenses.md   # each row once
```

- [ ] **Step 3: `docs/projects.md` and `docs/module-boundaries.md`**

In `docs/projects.md`, replace

```markdown
- **`lastEntryDate`** — the most recently dated line **across every currency**, so
```

with

```markdown
- **`supplierInvoices`** — the part of the three buckets and the totals that is
  **supplier invoices**, in the project's own currency, `{approved, submitted,
  draft, total}` each `{count, cost, amount}` exactly as the block's own buckets,
  each already inside the bucket of that status above it. It is a sub-figure,
  never a split: every figure above still counts every line, the margin already
  counts all expense cost and nothing reaches the budget. Absent when the
  project's currency holds none (and on a project that carries no currency). A
  provider whose split contradicts itself — its total count not its buckets', or
  more lines than the currency has — is a 500, as a contradicting total is.
- **`lastEntryDate`** — the most recently dated line **across every currency**, so
```

Replace

```markdown
included, needs financial rights and *not* `projects:view-costs`: what a receipt
cost the company is what somebody paid a supplier, and nothing in it can be divided
```

with

```markdown
included, needs financial rights and *not* `projects:view-costs`: what an expense
cost the company is what it paid a supplier — on a receipt somebody handed in, or on
the supplier's own invoice — and nothing in it can be divided
```

Replace

```markdown
passed on to the customer, over a total the server sends rather than the three rows
added up.
```

with

```markdown
passed on to the customer, over a total the server sends rather than the three rows
added up, and beneath the total an **"of which supplier invoices"** row — the
supplier invoices' own count, cost and amount, when there are any.
```

In `docs/module-boundaries.md`, replace

```markdown
`contracts.ProjectExpenses` is the third optional contract and the mirror of the
second: **Expenses provides it, Projects optionally consumes it** — what a
project's expenses cost and bill, beside the hours Time already reports. It is the
```

with

```markdown
`contracts.ProjectExpenses` is the third optional contract and the mirror of the
second: **Expenses provides it, Projects optionally consumes it** — what a
project's expenses cost and bill, beside the hours Time already reports, and, per
currency, the part of it that is supplier invoices (`CurrencyExpenses.SupplierInvoices`,
an `ExpenseSplit` of the same buckets, nil when there are none: a sub-figure, never a
split, so every existing figure still means everything). It is the
```

- [ ] **Step 4: `ROADMAP.md`**

Replace

```markdown
### Phase 3 — Budgets, billing milestones and costs (two deliveries done)
```

with

```markdown
### Phase 3 — Budgets, billing milestones and costs (done)
```

Replace

```markdown
**Next: supplier invoices.** Expenses landed separately — see
[Expenses phase 3](#phase-3--expenses-on-the-project-page-done) — and a
supplier invoice (a company-paid expense kind with supplier, invoice number
and due date, never in a claim, split from expenses in the project's Costs
section) is the next delivery, its own spec. Forecast /
estimate-to-complete and original-vs-revised budgets (tracking a budget's
own history rather than only its current value) are candidates worth
deciding on once that lands, not committed work yet.
```

with

```markdown
**Supplier invoices (done).** Decided in
`docs/superpowers/specs/2026-09-26-supplier-invoices-design.md`. The invoice a
supplier sends for work or goods on a project is a fourth kind in Expenses:
the supplier, the supplier's invoice number, the invoice date (the entry date)
and the due date, its PDF attached and required on submit, attested through
the module's own flow, priced and re-billed with the outlay's markup, and
company-paid — so it owes nobody, now that who is owed money is one SQL
function (`expenses.owes_employee`) and its Go mirror rather than fourteen
copies. It is recorded by whoever holds the project's financial rights, on a
completed project too, and visible to them as rows. The expenses contract
carries it as a per-currency sub-figure, and the Economy tab's Costs section
and the Expenses tab's cards show "of which supplier invoices". Accounts
payable, a supplier register and inbound e-invoices stay out of scope. See
[`docs/expenses.md`](docs/expenses.md#the-supplier-invoice).

*Unblocks:* a project's non-hours cost that is what suppliers invoiced, not
only what somebody put on an expense.

Forecast / estimate-to-complete and original-vs-revised budgets (tracking a
budget's own history rather than only its current value) are candidates worth
deciding on next, not committed work yet.
```

Replace

```markdown
has said of an hour since phase 2. Supplier costs are the other gap already
named, under [Projects](#projects).
```

with

```markdown
has said of an hour since phase 2. Supplier costs are no longer a gap: a
supplier's invoice is its own kind — see [Projects](#projects).
```

- [ ] **Step 5: Check the docs against the code, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
grep -rn "supplier invoice\|supplier_invoice\|supplierInvoices\|owes_employee" docs/expenses.md docs/projects.md docs/module-boundaries.md ROADMAP.md | head -60
grep -n "owes_employee\|kindSupplierInvoice\|supplierInvoiceNeedsProject\|cannotRecordSupplierInvoice\|Attach the supplier's invoice" \
  apps/server/internal/expenses/*.go apps/server/internal/db/migrations/00033_expenses_supplier_invoices.sql | head -30
```
Check, by eye, every sentence in Steps 1–4 against the code: the refusal sentences are the constants' words; "financial rights" is `seesProjectFinancials`; "only a cancelled project refuses" is `projectCancelled`; the visibility widening is `accessFor` and the list's predicate; "fourteen copies" is thirteen in SQL and one in Go (plan preamble, Point 1); the API table still says "all 45 operations" (no operation was added). Fix any sentence the code contradicts, in the doc.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-6.txt <<'MSG'
docs: supplier invoices — the kind, the one rule for who is owed, the sub-figure

docs/expenses.md gains the supplier invoice (its fields, the one date,
company-paid, never in a claim, always on a project, the required
document, who may record it and who sees it), the SQL function and its Go
mirror, the summary's new figures and the project page's button;
docs/projects.md the economy's supplierInvoices and the Costs section's
line; docs/module-boundaries.md the contract's sub-figure; ROADMAP.md
phase 3 done and supplier costs no longer a gap.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
PATHS="docs/expenses.md docs/projects.md docs/module-boundaries.md ROADMAP.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-si-6.txt -- $PATHS
git show --stat HEAD && git status --short
```

---
### Task 7: The Expenses frontend — the kind, its form, its drawer, the project page's button and line (D5)

`@vantigo/expenses-ui` learns the fourth kind: the vocabulary and labels, the form's kind control (outside claims, only with projects, not on "Record a cost"), its fields and payload, the receipts block's hint, the picker for the kind, the drawer's number, due date and "overdue" tag, the list's filter and labels, the approval tables (through the labels), the project page's **Record a supplier invoice** button behind `canRecordSupplierInvoice` with the summary's own `project`, and the currency cards' "of which supplier invoices" row. The fetch fake learns every server rule the tests lean on. en + nb.

**Files:**
- Create: `apps/expenses/frontend/src/pages/supplier-invoice.test.tsx`
- Modify: `apps/expenses/frontend/src/lib/status.ts`, `lib/money.ts`, `lib/search.test.ts`, `api/projects.ts`, `api/project-expenses.ts`, `lib/project-options.ts`, `pages/-expense-form-modal.tsx`, `components/entry-details.tsx`, `components/project-expenses-panel.tsx`, `components/project-expenses-panel.test.tsx`, `test/fixtures.ts`, `test/server.ts`, `i18n.ts`
- Read first (do not change): `pages/-expense-form-modal.tsx` whole, `components/entry-details.tsx` whole, `components/project-expenses-panel.tsx` whole, `pages/-approval-tables.tsx:130-222`, `pages/my-expenses.tsx:225-280,495-540`, `lib/search.ts`, `test/server.ts:1-300,360-400,500-610,700-780,1100-1170,1400-1560`, `test/fixtures.ts` whole, `test/api.ts`, `pages/expense-form-modal.test.tsx:1-160`, `components/project-expenses-panel.test.tsx:1-80,240-380`, `apps/expenses/frontend/src/api-schema.d.ts` (the generated `ExpensesEntryRequest`, `ExpensesEntryResponse`, `ExpensesProjectSummaryCapabilities`, `ExpensesProjectSummaryCurrency`, `ExpensesProjectSummaryResponse`, `ExpensesProjectSummarySupplierInvoices` from Tasks 2 and 3)

**Interfaces:**
- Consumes: the regenerated `api-schema.d.ts` (Tasks 2, 3); `GET /projects?kind=supplier_invoice`; the summary's `capabilities.canRecordSupplierInvoice`, `project`, `currencies[].supplierInvoices`.
- Produces TS: `ExpenseKind` gains `"supplier_invoice"`; `standaloneExpenseKinds` gains it; `entersAnAmount(kind)`, `takesReceipts(kind)`; `INVOICE_NUMBER_MAX_LENGTH = 100`; `ProjectPicker = "supplier_invoice"`, `expenseProjectsQueryOptions(kind?)`, `useProjectOptions(stored, enabled, kind?)`; `ProjectExpensesSupplierInvoices`; fixtures `supplierInvoice()`, `categoriesWithSubcontractor`; fake options `supplierInvoiceProjects`, `canRecordSupplierInvoice`, `summaryProject`.
- i18n keys (both catalogs): `kindSupplierInvoice`, `invoiceNumber`, `invoiceDate`, `dueDate`, `overdue`, `supplierRequired`, `invoiceNumberRequired`, `invoiceNumberTooLong`, `dueDateBeforeInvoiceDate`, `supplierInvoiceNeedsProject`, `noSupplierInvoiceProjects`, `companyPaysSupplierInvoices`, `supplierInvoiceDocument`, `attachSupplierInvoice`, `recordASupplierInvoice`, `ofWhichSupplierInvoices`; `totalsCoverMoreThanTheList` reworded.

- [ ] **Step 1: The vocabulary, the catalogs, the api layer, the fixtures and the fake**

In `apps/expenses/frontend/src/lib/status.ts`, replace

```ts
/** Every kind of money line the module records. */
export const expenseKinds = ["outlay", "mileage", "per_diem"] as const;
```

with

```ts
/** Every kind of money line the module records. */
export const expenseKinds = ["outlay", "mileage", "per_diem", "supplier_invoice"] as const;
```

Replace

```ts
/**
 * The kinds an expense of its own can be. A per diem day exists only inside a
 * travel claim, so it is never among "My expenses"' standalone list and is not
 * offered as a filter there — the trip it belongs to is the unit instead.
 */
export const standaloneExpenseKinds = ["outlay", "mileage"] as const;
```

with

```ts
/**
 * The kinds an expense of its own can be. A per diem day exists only inside a
 * travel claim, so it is never among "My expenses"' standalone list and is not
 * offered as a filter there — the trip it belongs to is the unit instead. A
 * supplier invoice is never inside a trip, so it is always one of these.
 */
export const standaloneExpenseKinds = ["outlay", "mileage", "supplier_invoice"] as const;

/**
 * Whether a kind is entered as an amount — a category, a gross and a VAT —
 * rather than priced by the server: an outlay, and a supplier invoice, which
 * borrows the outlay's money whole (supplier invoices design D1).
 */
export const entersAnAmount = (kind: ExpenseKind): boolean => kind === "outlay" || kind === "supplier_invoice";

/** Whether a kind carries documents: an outlay its receipts, a supplier invoice the supplier's invoice. */
export const takesReceipts = (kind: ExpenseKind): boolean => kind === "outlay" || kind === "supplier_invoice";
```

Replace

```ts
/** The `expenses` catalog key naming a kind. */
export const expenseKindLabelKey = (kind: ExpenseKind): string =>
  kind === "mileage" ? "kindMileage" : kind === "per_diem" ? "kindPerDiem" : "kindOutlay";
```

with

```ts
const kindLabelKeys: Record<ExpenseKind, string> = {
  outlay: "kindOutlay",
  mileage: "kindMileage",
  per_diem: "kindPerDiem",
  supplier_invoice: "kindSupplierInvoice",
};

/** The `expenses` catalog key naming a kind. */
export const expenseKindLabelKey = (kind: ExpenseKind): string => kindLabelKeys[kind];
```

In `apps/expenses/frontend/src/lib/money.ts`, replace

```ts
export const PLACE_MAX_LENGTH = 200;
```

with

```ts
export const PLACE_MAX_LENGTH = 200;

/** What the contract allows in a supplier's invoice number. */
export const INVOICE_NUMBER_MAX_LENGTH = 100;
```

In `apps/expenses/frontend/src/i18n.ts`, replace

```ts
    kindPerDiem: "Per diem",
```

with

```ts
    kindPerDiem: "Per diem",
    kindSupplierInvoice: "Supplier invoice",
    invoiceNumber: "Invoice number",
    invoiceDate: "Invoice date",
    dueDate: "Due date",
    overdue: "Overdue",
    supplierRequired: "Name the supplier",
    invoiceNumberRequired: "Give the supplier's invoice number",
    invoiceNumberTooLong: "An invoice number holds at most 100 characters",
    dueDateBeforeInvoiceDate: "The due date cannot be before the invoice date",
    supplierInvoiceNeedsProject: "A supplier invoice is booked on a project",
    noSupplierInvoiceProjects:
      "There is no open project you are on whose money you may see. Record a supplier invoice from the project's own page instead.",
    companyPaysSupplierInvoices: "The company pays a supplier invoice — nobody is paid anything back for it.",
    supplierInvoiceDocument: "The supplier's invoice",
    attachSupplierInvoice: "Attach the supplier's invoice",
    recordASupplierInvoice: "Record a supplier invoice",
    ofWhichSupplierInvoices: "Of which supplier invoices",
```

Replace

```ts
    kindPerDiem: "Diett",
```

with

```ts
    kindPerDiem: "Diett",
    kindSupplierInvoice: "Leverandørfaktura",
    invoiceNumber: "Fakturanummer",
    invoiceDate: "Fakturadato",
    dueDate: "Forfallsdato",
    overdue: "Forfalt",
    supplierRequired: "Oppgi leverandøren",
    invoiceNumberRequired: "Oppgi leverandørens fakturanummer",
    invoiceNumberTooLong: "Et fakturanummer kan ha høyst 100 tegn",
    dueDateBeforeInvoiceDate: "Forfallsdatoen kan ikke være før fakturadatoen",
    supplierInvoiceNeedsProject: "En leverandørfaktura føres på et prosjekt",
    noSupplierInvoiceProjects:
      "Du er ikke på noe åpent prosjekt der du kan se økonomien. Før leverandørfakturaen fra prosjektets egen side i stedet.",
    companyPaysSupplierInvoices: "Firmaet betaler en leverandørfaktura — ingen får noe tilbakebetalt for den.",
    supplierInvoiceDocument: "Leverandørens faktura",
    attachSupplierInvoice: "Legg ved leverandørens faktura",
    recordASupplierInvoice: "Før en leverandørfaktura",
    ofWhichSupplierInvoices: "Herav leverandørfakturaer",
```

Replace

```ts
      "The totals cover every expense on the project. The list shows the ones you may open: your own, and all of them if you manage the project or may view all expenses.",
```

with

```ts
      "The totals cover every expense on the project. The list shows the ones you may open: your own, the supplier invoices if you may see the project's money, and all of them if you manage the project or may view all expenses.",
```

Replace

```ts
      "Summene dekker alle utleggene på prosjektet. Listen viser dem du kan åpne: dine egne — og alle sammen hvis du leder prosjektet eller kan se alle utlegg.",
```

with

```ts
      "Summene dekker alle utleggene på prosjektet. Listen viser dem du kan åpne: dine egne, leverandørfakturaene hvis du kan se prosjektets økonomi — og alle sammen hvis du leder prosjektet eller kan se alle utlegg.",
```

In `apps/expenses/frontend/src/api/projects.ts`, replace

```ts
export const expenseProjectsQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "projects"],
    queryFn: ({ signal }) => request<ExpenseProjectOption[]>("/api/v1/expenses/projects", { signal }),
  });
```

with

```ts
export const expenseProjectsQueryOptions = (kind?: ProjectPicker) =>
  queryOptions({
    queryKey: kind ? [EXPENSES_QUERY_KEY, "projects", kind] : [EXPENSES_QUERY_KEY, "projects"],
    queryFn: ({ signal }) =>
      request<ExpenseProjectOption[]>(
        kind ? `/api/v1/expenses/projects?kind=${kind}` : "/api/v1/expenses/projects",
        { signal },
      ),
  });

/**
 * Which picker. Left out, the projects the caller may book on — what logging
 * time needs. `supplier_invoice` is the projects they hold financial rights on
 * and that are not cancelled, which is what recording a supplier invoice needs
 * (supplier invoices design D2) and a different list altogether: a finance
 * reader logs time on nothing and may still record one.
 */
export type ProjectPicker = "supplier_invoice";
```

In `apps/expenses/frontend/src/lib/project-options.ts`, replace

```ts
import { type ExpenseProjectOption, expenseProjectsQueryOptions } from "../api/projects";
```

with

```ts
import { type ExpenseProjectOption, expenseProjectsQueryOptions, type ProjectPicker } from "../api/projects";
```

Replace

```ts
export const useProjectOptions = (stored: StoredProject | undefined, enabled: boolean): ProjectOptions => {
  const { t } = useI18n("expenses");
  const { data: projects } = useQuery({ ...expenseProjectsQueryOptions(), enabled });
```

with

```ts
export const useProjectOptions = (
  stored: StoredProject | undefined,
  enabled: boolean,
  kind?: ProjectPicker,
): ProjectOptions => {
  const { t } = useI18n("expenses");
  const { data: projects } = useQuery({ ...expenseProjectsQueryOptions(kind), enabled });
```

In `apps/expenses/frontend/src/api/project-expenses.ts`, replace

```ts
export type ProjectExpensesCapabilities = Schemas["ExpensesProjectSummaryCapabilities"];
```

with

```ts
export type ProjectExpensesCapabilities = Schemas["ExpensesProjectSummaryCapabilities"];

/**
 * The part of one currency's buckets that is supplier invoices (supplier
 * invoices design D3) — a line of its own beneath the total, never a split of
 * it. Absent when the currency holds none.
 */
export type ProjectExpensesSupplierInvoices = Schemas["ExpensesProjectSummarySupplierInvoices"];
```

In `apps/expenses/frontend/src/test/fixtures.ts`, replace

```ts
export const attachment = (overrides: Partial<ExpenseAttachment> = {}): ExpenseAttachment => ({
```

with

```ts
/**
 * A supplier invoice as the server answers one — a wire literal: the caller's
 * own draft on the panel's project, company-paid and owed to nobody, with the
 * supplier's number and its due date, under Subcontractor. Billing is not on
 * it: an owner's capabilities here cannot see it.
 */
export const supplierInvoice = (overrides: Partial<StoredExpense> = {}): StoredExpense => ({
  id: 551,
  kind: "supplier_invoice",
  entryDate: DAY,
  description: "Rørleggerarbeid, uke 38",
  supplier: "Rør & Varme AS",
  invoiceNumber: "F-20260918",
  dueDate: "2026-10-18",
  category: { id: 14, name: "Subcontractor" },
  currency: "NOK",
  paidBy: "company",
  grossAmount: 12500,
  vatAmount: 2500,
  netAmount: 10000,
  owedToEmployee: 0,
  billable: true,
  project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
  status: "draft",
  attachmentCount: 0,
  attachments: [],
  owner: owner(),
  revision: 1,
  createdAt: "2026-09-18T08:00:00Z",
  updatedAt: "2026-09-18T08:00:00Z",
  capabilities: ownDraftCapabilities,
  ...overrides,
});

export const attachment = (overrides: Partial<ExpenseAttachment> = {}): ExpenseAttachment => ({
```

Replace

```ts
  { id: 13, name: "Old category", active: false, position: 3 },
];
```

with

```ts
  { id: 13, name: "Old category", active: false, position: 3 },
];

/** The categories with Subcontractor among them, which a new supplier invoice starts under. */
export const categoriesWithSubcontractor: ExpensesMeta["categories"] = [
  ...categories,
  { id: 14, name: "Subcontractor", active: true, position: 4 },
];
```

In `apps/expenses/frontend/src/test/server.ts`, replace

```ts
  projects?: Read<ExpenseProjectOption[]>;
```

with

```ts
  projects?: Read<ExpenseProjectOption[]>;
  /**
   * What `GET /projects?kind=supplier_invoice` answers — the projects the
   * caller holds financial rights on, a picker of its own (supplier invoices
   * design D2). Left out, none.
   */
  supplierInvoiceProjects?: Read<ExpenseProjectOption[]>;
```

Replace

```ts
  /** What the derived summary reports as `capabilities.canRecord`; false by default. */
  canRecord?: boolean;
```

with

```ts
  /** What the derived summary reports as `capabilities.canRecord`; false by default. */
  canRecord?: boolean;
  /**
   * What the derived summary reports as `capabilities.canRecordSupplierInvoice`;
   * false by default. True, the summary also answers `project` — `summaryProject`,
   * or the first of `projects` — exactly as the server answers the project
   * whenever the capability is true.
   */
  canRecordSupplierInvoice?: boolean;
  summaryProject?: ExpenseProjectOption;
```

Replace

```ts
  const gross = input.grossAmount ?? 0;
  const vat = input.vatAmount ?? 0;
  return {
    currency: input.currency ?? currency,
    supplier: input.supplier,
    paidBy: input.paidBy as Expense["paidBy"],
```

with

```ts
  const gross = input.grossAmount ?? 0;
  const vat = input.vatAmount ?? 0;
  if (input.kind === "supplier_invoice") {
    return {
      currency: input.currency ?? currency,
      supplier: input.supplier,
      invoiceNumber: input.invoiceNumber,
      dueDate: input.dueDate,
      // The company pays a supplier invoice, and the server stores it so
      // whatever the request left out.
      paidBy: "company",
      grossAmount: gross,
      vatAmount: vat === 0 ? undefined : vat,
      netAmount: round2(gross - vat),
      owedToEmployee: 0,
    };
  }
  return {
    currency: input.currency ?? currency,
    supplier: input.supplier,
    invoiceNumber: undefined,
    dueDate: undefined,
    paidBy: input.paidBy as Expense["paidBy"],
```

Replace

```ts
  const projectsOf = (): ExpenseProjectOption[] => (server.projects instanceof Response ? [] : (server.projects ?? []));
```

with

```ts
  const projectsOf = (): ExpenseProjectOption[] => (server.projects instanceof Response ? [] : (server.projects ?? []));

  /** The projects the caller may record a supplier invoice on, as `GET /projects?kind=supplier_invoice` answers them. */
  const supplierInvoiceProjectsOf = (): ExpenseProjectOption[] =>
    server.supplierInvoiceProjects instanceof Response ? [] : (server.supplierInvoiceProjects ?? []);

  /** A project an expense names, as the server renders it on the entry: from whichever picker knew it. */
  const projectRefOf = (id: number) => {
    const known = [...projectsOf(), ...supplierInvoiceProjectsOf(), ...(server.summaryProject ? [server.summaryProject] : [])].find(
      (one) => one.id === id,
    );
    return { id, code: known?.code ?? "KVEM1000", name: known?.name ?? "Kverneland web" };
  };
```

Replace

```ts
    if (path === "/api/v1/expenses/projects") return Promise.resolve(answer(server.projects, []));
```

with

```ts
    if (path === "/api/v1/expenses/projects") {
      // `kind=supplier_invoice` is a picker of its own — the projects whose
      // money the caller may see — and never the bookable list.
      const picker = url.searchParams.get("kind") === "supplier_invoice" ? server.supplierInvoiceProjects : server.projects;
      return Promise.resolve(answer(picker, []));
    }
```

Replace

```ts
      for (const bucket of [figures[bucketOf(entry)], figures.total]) {
        bucket.count += 1;
        bucket.cost = round2(bucket.cost + entry.netAmount);
        bucket.billAmount = round2(bucket.billAmount + bills);
      }
```

with

```ts
      for (const bucket of [figures[bucketOf(entry)], figures.total]) {
        bucket.count += 1;
        bucket.cost = round2(bucket.cost + entry.netAmount);
        bucket.billAmount = round2(bucket.billAmount + bills);
      }
      // The supplier invoices' own share, beside the buckets and never
      // instead of them — absent from a currency that holds none.
      if (entry.kind === "supplier_invoice") {
        if (!figures.supplierInvoices) {
          figures.supplierInvoices = { approved: empty(), submitted: empty(), draft: empty(), total: empty() };
        }
        const share = figures.supplierInvoices;
        for (const bucket of [share[bucketOf(entry)], share.total]) {
          bucket.count += 1;
          bucket.cost = round2(bucket.cost + entry.netAmount);
          bucket.billAmount = round2(bucket.billAmount + bills);
        }
      }
```

Replace

```ts
      capabilities: { canRecord: server.canRecord ?? false },
    };
  };
```

with

```ts
      capabilities: {
        canRecord: server.canRecord ?? false,
        canRecordSupplierInvoice: server.canRecordSupplierInvoice ?? false,
      },
      ...(server.canRecordSupplierInvoice ? { project: server.summaryProject ?? projectsOf()[0] } : {}),
    };
  };
```

Replace

```ts
      receiptsMissing: held.filter((one) => one.kind === "outlay" && one.attachmentCount === 0).length,
```

with

```ts
      receiptsMissing: held.filter(
        (one) => (one.kind === "outlay" || one.kind === "supplier_invoice") && one.attachmentCount === 0,
      ).length,
```

Replace

```ts
      const refusal = missing("Invalid submission", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const send = (unit: Expense | Claim) => {
```

with

```ts
      const refusal = missing("Invalid submission", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      // The supplier invoice's document rule, as the server judges it at
      // submit: never without the supplier's invoice attached.
      const undocumented = ids
        .map((id) => find(id))
        .filter((entry) => entry?.kind === "supplier_invoice" && entry.attachmentCount === 0);
      if (undocumented.length > 0) {
        return Promise.resolve(
          problem(400, "Invalid submission", {
            entryIds: undocumented.map(
              (entry) => `Expense ${entry?.id} cannot be submitted yet: Attach the supplier's invoice`,
            ),
          }),
        );
      }
      const send = (unit: Expense | Claim) => {
```

Replace

```ts
      let perDiem: Partial<Expense> | undefined;
```

with

```ts
      // The supplier invoice's own rules, as parseSupplierInvoice holds them:
      // a fake that took one without a project or a number would let a form
      // that forgets either pass every test.
      if (input.kind === "supplier_invoice") {
        const refused: Record<string, string[]> = {};
        if (input.projectId === undefined) refused.projectId = ["A supplier invoice is booked on a project"];
        if (claim) refused.claimId = ["A supplier invoice is not a travel claim line"];
        if (!input.supplier?.trim()) refused.supplier = ["A supplier invoice names its supplier"];
        if (!input.invoiceNumber?.trim()) {
          refused.invoiceNumber = ["A supplier invoice carries the supplier's invoice number"];
        }
        if (input.paidBy === "employee") refused.paidBy = ["A supplier invoice is paid by the company"];
        if (input.dueDate && input.dueDate < input.entryDate) {
          refused.dueDate = ["The due date cannot be before the invoice date"];
        }
        if (Object.keys(refused).length > 0) return Promise.resolve(problem(400, "Invalid expense", refused));
      }
      let perDiem: Partial<Expense> | undefined;
```

Replace

```ts
        ...(perDiem ?? priced(input, ratesOf(), metaOf().defaultCurrency)),
      } as StoredExpense;
```

with

```ts
        ...(perDiem ?? priced(input, ratesOf(), metaOf().defaultCurrency)),
        // A supplier invoice is always on its project, and the entry names it.
        ...(input.kind === "supplier_invoice" && input.projectId !== undefined
          ? { project: projectRefOf(input.projectId) }
          : {}),
      } as StoredExpense;
```

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/expenses/frontend typecheck
```
Expected: PASS (nothing uses the new kind yet; the `Record<ExpenseKind, string>` is complete).

- [ ] **Step 2: Write the tests and see them fail**

Create `apps/expenses/frontend/src/pages/supplier-invoice.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { EntryDetails } from "../components/entry-details";
import { sent } from "../test/api";
import {
  APPROVER,
  capabilities,
  categoriesWithSubcontractor,
  meta,
  outlay,
  projectOptions,
  supplierInvoice,
} from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { renderRoute } from "../test/route-tree";
import { type ExpensesServer, stubExpensesApi } from "../test/server";

/** Mounts My expenses over the given server and opens the create form. */
const openNew = async (server: ExpensesServer) => {
  const fetchMock = stubExpensesApi(server);
  renderRoute("/expenses");
  await screen.findByRole("table", { name: "My expenses" });
  await userEvent.click(screen.getByRole("button", { name: "New expense" }));
  return { dialog: await screen.findByRole("dialog", { name: "New expense" }), fetchMock };
};

/** Mounts My expenses over the given server and opens one expense for editing. */
const openEdit = async (server: ExpensesServer, description: string) => {
  const fetchMock = stubExpensesApi(server);
  renderRoute("/expenses");
  const row = (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;
  await userEvent.click(within(row).getByRole("button", { name: `Edit ${description}` }));
  return { dialog: await screen.findByRole("dialog", { name: "Edit the expense" }), fetchMock };
};

const choose = async (dialog: HTMLElement, label: string, option: RegExp | string) => {
  await userEvent.click(within(dialog).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

const withSubcontractor = meta({ categories: categoriesWithSubcontractor });

describe("a supplier invoice in the expense form", () => {
  it("is offered beside outlay and mileage only where there are projects", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: meta({ projectsAvailable: false }) });

    expect(within(dialog).getByRole("radio", { name: "Outlay" })).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Mileage" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio", { name: "Supplier invoice" })).not.toBeInTheDocument();
  });

  it("asks for the supplier's fields, starts under Subcontractor and billable, and names no payer", async () => {
    const { dialog, fetchMock } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      supplierInvoiceProjects: projectOptions,
    });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));

    expect(within(dialog).getByRole("textbox", { name: "Invoice date" })).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Invoice number" })).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Due date" })).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Category" })).toHaveValue("Subcontractor");
    expect(within(dialog).queryByRole("radio", { name: "The company paid" })).not.toBeInTheDocument();
    expect(within(dialog).getByTestId("company-pays")).toHaveTextContent("The company pays a supplier invoice");
    // Its picker is the projects whose money the caller may see, not the bookable ones.
    await within(dialog).findByRole("combobox", { name: "Project" });
    await choose(dialog, "Project", /KVEM1000/);
    expect(within(dialog).getByRole("switch", { name: "Billable" })).toBeChecked();
    expect(
      fetchMock.actualCalls.some(([url]) => String(url).endsWith("/api/v1/expenses/projects?kind=supplier_invoice")),
    ).toBe(true);
  });

  it("sends the supplier's own fields and no payer, and asks for the invoice once it is a draft", async () => {
    const { dialog, fetchMock } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      supplierInvoiceProjects: projectOptions,
    });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));
    await within(dialog).findByRole("combobox", { name: "Project" });
    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Rørleggerarbeid");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Supplier" }), "Rør & Varme AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Invoice number" }), "F-20260918");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "12500");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    const body = sent(fetchMock, "POST").body;
    expect(body).toMatchObject({
      kind: "supplier_invoice",
      supplier: "Rør & Varme AS",
      invoiceNumber: "F-20260918",
      categoryId: 14,
      currency: "NOK",
      grossAmount: 12500,
      projectId: 1001,
      billable: true,
    });
    expect(body).not.toHaveProperty("paidBy");
    expect(body).not.toHaveProperty("dueDate");
    // A new one stays open as a draft, because its document can only be
    // attached to something that exists — and it says the document is needed.
    expect(await within(dialog).findByTestId("attach-supplier-invoice")).toHaveTextContent(
      "Attach the supplier's invoice",
    );
    expect(within(dialog).getByText("The supplier's invoice")).toBeInTheDocument();
  });

  it("refuses to save without the supplier, the invoice number and the project", async () => {
    const { dialog, fetchMock } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      supplierInvoiceProjects: projectOptions,
    });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Rørleggerarbeid");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "12500");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    expect(await within(dialog).findByText("Name the supplier")).toBeInTheDocument();
    expect(within(dialog).getByText("Give the supplier's invoice number")).toBeInTheDocument();
    expect(within(dialog).getByText("A supplier invoice is booked on a project")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("carries its number and due date through an edit, still naming no payer", async () => {
    const { dialog, fetchMock } = await openEdit(
      { entries: [supplierInvoice()], meta: withSubcontractor, supplierInvoiceProjects: projectOptions },
      "Rørleggerarbeid, uke 38",
    );

    const number = within(dialog).getByRole("textbox", { name: "Invoice number" });
    expect(number).toHaveValue("F-20260918");
    await userEvent.clear(number);
    await userEvent.type(number, "F-20260919");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/551"));
    const body = sent(fetchMock, "PUT").body;
    expect(body).toMatchObject({
      kind: "supplier_invoice",
      invoiceNumber: "F-20260919",
      dueDate: "2026-10-18",
      projectId: 1001,
      revision: 1,
    });
    expect(body).not.toHaveProperty("paidBy");
  });
});

describe("a supplier invoice read out", () => {
  it("names the supplier's number and due date, and says when it is past due and not invoiced", () => {
    renderWithProviders(<EntryDetails expense={supplierInvoice({ dueDate: "2026-01-02" })} />);

    expect(screen.getByText("Supplier invoice")).toBeInTheDocument();
    expect(screen.getByText("Invoice date")).toBeInTheDocument();
    expect(screen.getByText("F-20260918")).toBeInTheDocument();
    expect(screen.getByText("Due date")).toBeInTheDocument();
    expect(screen.getByText("Overdue")).toBeInTheDocument();
    expect(screen.getByText("The supplier's invoice")).toBeInTheDocument();
  });

  it("says nothing of overdue before the due date, or once the line is invoiced", () => {
    const { unmount } = renderWithProviders(<EntryDetails expense={supplierInvoice({ dueDate: "2999-12-31" })} />);
    expect(screen.queryByText("Overdue")).not.toBeInTheDocument();
    unmount();

    renderWithProviders(
      <EntryDetails
        expense={supplierInvoice({
          dueDate: "2026-01-02",
          capabilities: capabilities({ canSeeBilling: true }),
          billing: { billAmount: 10000, invoice: { at: "2026-02-01T10:00:00Z", by: APPROVER } },
        })}
      />,
    );
    expect(screen.queryByText("Overdue")).not.toBeInTheDocument();
  });
});

describe("a supplier invoice in My expenses", () => {
  it("is listed under its own kind, paid by the company, and is a kind the list filters by", async () => {
    stubExpensesApi({ entries: [outlay(), supplierInvoice()] });
    const { router } = renderRoute("/expenses");

    const row = (await screen.findByText("Rørleggerarbeid, uke 38")).closest("[data-expense]") as HTMLElement;
    expect(within(row).getByText("Supplier invoice")).toBeInTheDocument();
    expect(within(row).getByText("The company paid")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    await userEvent.click(await screen.findByRole("option", { name: "Supplier invoice" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ kind: "supplier_invoice" }));
    await waitFor(() => expect(screen.queryByText("Taxi to the airport")).not.toBeInTheDocument());
    expect(screen.getByText("Rørleggerarbeid, uke 38")).toBeInTheDocument();
  });
});
```

In `apps/expenses/frontend/src/lib/search.test.ts`, replace

```ts
  it("drops what a hand-edited link invented rather than sending the API a 400", () => {
```

with

```ts
  it("keeps a supplier invoice filter, the one kind that is never inside a trip", () => {
    expect(validateMyExpensesSearch({ kind: "supplier_invoice" }).kind).toBe("supplier_invoice");
  });

  it("drops what a hand-edited link invented rather than sending the API a 400", () => {
```

In `apps/expenses/frontend/src/components/project-expenses-panel.test.tsx`, replace

```ts
import {
  capabilities,
  claim,
  meta,
```

with

```ts
import {
  capabilities,
  categoriesWithSubcontractor,
  claim,
  meta,
```

and append at the end of the file:

```tsx
describe("ProjectExpensesPanel — supplier invoices", () => {
  it("offers 'Record a supplier invoice' to the project's financial side, fixed to the project and the kind", async () => {
    // A finance reader: may see the money, on no team — so no "Record a cost".
    const fetchMock = stubExpensesApi({
      entries: [],
      meta: meta({ categories: categoriesWithSubcontractor }),
      canRecordSupplierInvoice: true,
      summaryProject: projectOptions[0],
      projectCurrency: "NOK",
    });
    panel();

    await userEvent.click(await screen.findByRole("button", { name: "Record a supplier invoice" }));
    expect(screen.queryByRole("button", { name: "Record a cost" })).not.toBeInTheDocument();
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    // The button named its kind, so there is no kind to choose.
    expect(within(dialog).queryByRole("radio", { name: "Outlay" })).not.toBeInTheDocument();
    expect(within(dialog).getByText("Booked on KVEM1000 · Kverneland web")).toBeInTheDocument();
    expect(within(dialog).getByRole("switch", { name: "Billable" })).toBeChecked();
    expect(within(dialog).getByRole("combobox", { name: "Category" })).toHaveValue("Subcontractor");

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Rørleggerarbeid");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Supplier" }), "Rør & Varme AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Invoice number" }), "F-1");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "12500");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    expect(sent(fetchMock, "POST").body).toMatchObject({
      kind: "supplier_invoice",
      projectId: PROJECT,
      billable: true,
      categoryId: 14,
    });
  });

  it("offers no supplier invoice without the capability, and 'Record a cost' offers no such kind", async () => {
    stubExpensesApi({ entries: [], projects: projectOptions, canRecord: true, canRecordSupplierInvoice: false });
    panel();

    await userEvent.click(await screen.findByRole("button", { name: "Record a cost" }));
    expect(screen.queryByRole("button", { name: "Record a supplier invoice" })).not.toBeInTheDocument();
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    expect(within(dialog).getByRole("radio", { name: "Outlay" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio", { name: "Supplier invoice" })).not.toBeInTheDocument();
  });

  it("writes the supplier invoices' share beneath a currency's total, and nothing where there is none", async () => {
    stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        projectCurrency: "NOK",
        currencies: [
          summaryCurrency({
            currency: "EUR",
            approved: summaryBucket({ count: 1, cost: 90, billAmount: 100 }),
            total: summaryBucket({ count: 1, cost: 90, billAmount: 100 }),
          }),
          summaryCurrency({
            approved: summaryBucket({ count: 3, cost: 14000, billAmount: 15400 }),
            total: summaryBucket({ count: 3, cost: 14000, billAmount: 15400 }),
            supplierInvoices: {
              approved: summaryBucket({ count: 1, cost: 10000, billAmount: 11000 }),
              submitted: summaryBucket(),
              draft: summaryBucket(),
              total: summaryBucket({ count: 1, cost: 10000, billAmount: 11000 }),
            },
          }),
        ],
      }),
    });
    panel();

    const share = await screen.findByTestId("project-expense-supplier-invoices-NOK");
    expect(share).toHaveTextContent("Of which supplier invoices");
    expect(share).toHaveTextContent("1 expense");
    expect(share).toHaveTextContent("10,000.00");
    expect(share).toHaveTextContent("11,000.00");
    // The total above it is still every line.
    expect(screen.getByTestId("project-expense-currency-NOK")).toHaveTextContent("14,000.00");
    expect(screen.queryByTestId("project-expense-supplier-invoices-EUR")).not.toBeInTheDocument();
  });
});
```

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/expenses/frontend test src/pages/supplier-invoice.test.tsx src/components/project-expenses-panel.test.tsx src/lib/search.test.ts
```
Expected: FAIL — no "Supplier invoice" radio, no invoice fields, no button, no share row (the search test already passes: `standaloneExpenseKinds` has the kind).

- [ ] **Step 3: The form**

In `apps/expenses/frontend/src/pages/-expense-form-modal.tsx`, replace

```ts
import { expensesMetaQueryOptions } from "../api/meta";
```

with

```ts
import { type ExpensesMeta, expensesMetaQueryOptions } from "../api/meta";
```

Replace

```ts
import {
  DESCRIPTION_MAX_LENGTH,
  MAX_DISTANCE_KM,
```

with

```ts
import {
  DESCRIPTION_MAX_LENGTH,
  INVOICE_NUMBER_MAX_LENGTH,
  MAX_DISTANCE_KM,
```

Replace

```ts
import type { ExpenseKind, PaidBy } from "../lib/status";
```

with

```ts
import { type ExpenseKind, entersAnAmount, type PaidBy, takesReceipts } from "../lib/status";
```

Replace

```ts
 * line is picked from that project's lines. It is still an expense of its
 * own, so it can be saved and submitted in one go.
 */
```

with

```ts
 * line is picked from that project's lines. It is still an expense of its
 * own, so it can be saved and submitted in one go.
 *
 * A `kind` of `supplier_invoice` with a `project` is **a supplier invoice
 * recorded from that project's page** (supplier invoices design D5): the kind
 * is fixed as well as the project — the button said which — and the project
 * is the summary's own option, because the finance reader it is for is often
 * on no project team.
 */
```

Replace

```ts
  supplier: string;
  grossAmount: number | string;
```

with

```ts
  supplier: string;
  invoiceNumber: string;
  dueDate: string | null;
  grossAmount: number | string;
```

Replace

```ts
  "supplier",
  "grossAmount",
```

with

```ts
  "supplier",
  "invoiceNumber",
  "dueDate",
  "grossAmount",
```

Replace

```ts
/**
 * One expense, whichever kind it is. The kind switches the *payload* and not
```

with

```ts
/**
 * The category a new supplier invoice starts under: Subcontractor, when the
 * installation has an active one by that name (in English or Norwegian —
 * categories are free text an administrator may rename), and nothing
 * otherwise.
 */
const SUBCONTRACTOR_NAMES = ["subcontractor", "underleverandør"];
const subcontractorOf = (categories: ExpensesMeta["categories"] | undefined): string | undefined => {
  const found = categories?.find(
    (category) => category.active && SUBCONTRACTOR_NAMES.includes(category.name.trim().toLowerCase()),
  );
  return found ? String(found.id) : undefined;
};

/**
 * One expense, whichever kind it is. The kind switches the *payload* and not
```

Replace

```ts
  const { projects, options: projectOptions } = useProjectOptions(opened?.project, meta?.projectsAvailable === true);
  const { data: rates } = useQuery(expenseRatesQueryOptions());
```

with

```ts
  const { data: rates } = useQuery(expenseRatesQueryOptions());
```

Replace

```ts
  const form = useForm<ExpenseFormValues>({
    initialValues: {
      kind: opened?.kind ?? (state.mode === "create" ? (state.kind ?? "outlay") : "outlay"),
```

with

```ts
  const startingKind: ExpenseKind = opened?.kind ?? (state.mode === "create" ? (state.kind ?? "outlay") : "outlay");
  /**
   * A supplier invoice opened from a project's page books that kind on that
   * project and nothing else: the button said so, so the form does not offer
   * to make it something the button did not promise.
   */
  const kindFixed = state.mode === "create" && state.kind === "supplier_invoice" && fixedProject !== undefined;

  const form = useForm<ExpenseFormValues>({
    initialValues: {
      kind: startingKind,
```

Replace

```ts
      categoryId: opened?.category ? String(opened.category.id) : null,
      supplier: opened?.supplier ?? "",
      grossAmount: opened && opened.kind === "outlay" ? opened.grossAmount : "",
```

with

```ts
      categoryId: opened?.category
        ? String(opened.category.id)
        : startingKind === "supplier_invoice"
          ? (subcontractorOf(meta?.categories) ?? null)
          : null,
      supplier: opened?.supplier ?? "",
      invoiceNumber: opened?.invoiceNumber ?? "",
      dueDate: opened?.dueDate ?? null,
      grossAmount: opened && entersAnAmount(opened.kind) ? opened.grossAmount : "",
```

Replace

```ts
      billable: opened?.billable ?? false,
    },
```

with

```ts
      // What a supplier invoiced is usually billed on, so a new one starts billable.
      billable: opened?.billable ?? startingKind === "supplier_invoice",
    },
```

Replace

```ts
      categoryId: (value, values) => (values.kind === "outlay" && !value ? t("categoryRequired") : null),
      supplier: (value) => (value.trim().length > PLACE_MAX_LENGTH ? t("supplierTooLong") : null),
```

with

```ts
      categoryId: (value, values) => (entersAnAmount(values.kind) && !value ? t("categoryRequired") : null),
      supplier: (value, values) => {
        const supplier = value.trim();
        if (!supplier && values.kind === "supplier_invoice") return t("supplierRequired");
        return supplier.length > PLACE_MAX_LENGTH ? t("supplierTooLong") : null;
      },
      invoiceNumber: (value, values) => {
        if (values.kind !== "supplier_invoice") return null;
        const number = value.trim();
        if (!number) return t("invoiceNumberRequired");
        return number.length > INVOICE_NUMBER_MAX_LENGTH ? t("invoiceNumberTooLong") : null;
      },
      dueDate: (value, values) =>
        values.kind === "supplier_invoice" && value && values.entryDate && value < values.entryDate
          ? t("dueDateBeforeInvoiceDate")
          : null,
      projectId: (value, values) =>
        values.kind === "supplier_invoice" && !value ? t("supplierInvoiceNeedsProject") : null,
```

Replace

```ts
      grossAmount: (value, values) => {
        if (values.kind !== "outlay") return null;
        const gross = numeric(value);
```

with

```ts
      grossAmount: (value, values) => {
        if (!entersAnAmount(values.kind)) return null;
        const gross = numeric(value);
```

Replace

```ts
      vatAmount: (value, values) => {
        if (values.kind !== "outlay") return null;
```

with

```ts
      vatAmount: (value, values) => {
        if (!entersAnAmount(values.kind)) return null;
```

Replace

```ts
  const values = form.values;
  const gross = numeric(values.grossAmount) ?? 0;
```

with

```ts
  /**
   * Subcontractor is preselected once the categories are known. `/meta` may
   * answer after the form mounted — its initial values were fixed then — so
   * a new supplier invoice that started without it is given it when it
   * arrives, once, adjusted during render the way React documents.
   */
  const [preselected, setPreselected] = useState(opened !== undefined || startingKind !== "supplier_invoice");
  if (!preselected && meta) {
    setPreselected(true);
    const subcontractor = subcontractorOf(meta.categories);
    if (subcontractor && form.values.categoryId === null) form.setFieldValue("categoryId", subcontractor);
  }

  const values = form.values;
  // A supplier invoice has a picker of its own: the projects whose money the
  // caller may see, which is who may record one — not the ones they log on.
  const { projects, options: projectOptions } = useProjectOptions(
    opened?.project,
    meta?.projectsAvailable === true,
    values.kind === "supplier_invoice" ? "supplier_invoice" : undefined,
  );
  const gross = numeric(values.grossAmount) ?? 0;
```

Replace

```ts
    gross > meta.receiptRequiredOver &&
    attachments.length === 0;
```

with

```ts
    gross > meta.receiptRequiredOver &&
    attachments.length === 0;
  /** A supplier invoice is never submitted without the supplier's invoice attached (design D2). */
  const needsInvoiceDocument = values.kind === "supplier_invoice" && attachments.length === 0;
```

Replace

```ts
    const vat = numeric(values.vatAmount);
    return {
      ...shared,
      currency,
      categoryId: Number(values.categoryId),
      paidBy: values.paidBy,
      grossAmount: gross,
      ...(vat !== undefined ? { vatAmount: vat } : {}),
      ...(values.supplier.trim() ? { supplier: values.supplier.trim() } : {}),
    };
  };
```

with

```ts
    const vat = numeric(values.vatAmount);
    const money = {
      ...shared,
      currency,
      categoryId: Number(values.categoryId),
      grossAmount: gross,
      ...(vat !== undefined ? { vatAmount: vat } : {}),
      ...(values.supplier.trim() ? { supplier: values.supplier.trim() } : {}),
    };
    // A supplier invoice names no payer — the company pays it, and the server
    // stores it so — and carries the supplier's number and its due date.
    if (values.kind === "supplier_invoice") {
      return {
        ...money,
        invoiceNumber: values.invoiceNumber.trim(),
        ...(values.dueDate ? { dueDate: values.dueDate } : {}),
      };
    }
    return { ...money, paidBy: values.paidBy };
  };
```

Replace

```ts
      // A new outlay stays open once it is a draft: its receipts can only be
      // attached to something that exists, and asking somebody to reopen the
      // form they just filled in to add them would be a poor trade.
      const keepOpen = !submitted && saved === undefined && stored.kind === "outlay";
```

with

```ts
      // A new outlay or supplier invoice stays open once it is a draft: its
      // documents can only be attached to something that exists, and asking
      // somebody to reopen the form they just filled in to add them would be a
      // poor trade.
      const keepOpen = !submitted && saved === undefined && takesReceipts(stored.kind);
```

Replace

```tsx
        {claim === undefined && (
          <Input.Wrapper label={t("kind")} labelElement="div">
            <SegmentedControl
              fullWidth
              mt={4}
              disabled={saved !== undefined}
              aria-label={t("kind")}
              value={values.kind}
              onChange={(next) => form.setFieldValue("kind", next as ExpenseKind)}
              data={[
                { value: "outlay", label: t("kindOutlay") },
                { value: "mileage", label: t("kindMileage") },
              ]}
            />
          </Input.Wrapper>
        )}
```

with

```tsx
        {claim === undefined && !kindFixed && (
          <Input.Wrapper label={t("kind")} labelElement="div">
            <SegmentedControl
              fullWidth
              mt={4}
              disabled={saved !== undefined}
              aria-label={t("kind")}
              value={values.kind}
              onChange={(next) => {
                const kind = next as ExpenseKind;
                form.setFieldValue("kind", kind);
                // A new supplier invoice starts billable and under
                // Subcontractor when there is one; a category already chosen
                // stays.
                if (kind === "supplier_invoice" && saved === undefined) {
                  form.setFieldValue("billable", true);
                  const subcontractor = subcontractorOf(meta.categories);
                  if (!values.categoryId && subcontractor) form.setFieldValue("categoryId", subcontractor);
                }
              }}
              data={[
                { value: "outlay", label: t("kindOutlay") },
                { value: "mileage", label: t("kindMileage") },
                // Only where projects exist — the kind lives on a project —
                // and not on a project page's "Record a cost", which has a
                // button of its own for it. One already saved keeps its label.
                ...((meta.projectsAvailable && fixedProject === undefined) || values.kind === "supplier_invoice"
                  ? [{ value: "supplier_invoice", label: t("kindSupplierInvoice") }]
                  : []),
              ]}
            />
          </Input.Wrapper>
        )}
```

Replace

```tsx
          <DateInput
            label={t("date")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            minDate={lockedBefore}
```

with

```tsx
          <DateInput
            label={values.kind === "supplier_invoice" ? t("invoiceDate") : t("date")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            minDate={lockedBefore}
```

Replace

```tsx
        {values.kind === "outlay" ? (
          <Stack>
            <Group grow align="start">
              <Select
                label={t("category")}
                placeholder={t("chooseCategory")}
                withAsterisk
                searchable
                data={categoryOptions}
                {...form.getInputProps("categoryId")}
              />
              <TextInput label={t("supplier")} {...form.getInputProps("supplier")} />
            </Group>
```

with

```tsx
        {entersAnAmount(values.kind) ? (
          <Stack>
            {values.kind === "supplier_invoice" && (
              <Group grow align="start">
                <TextInput label={t("invoiceNumber")} withAsterisk {...form.getInputProps("invoiceNumber")} />
                <DateInput
                  label={t("dueDate")}
                  valueFormat={t("dateInputFormat")}
                  clearable
                  minDate={values.entryDate ?? undefined}
                  {...form.getInputProps("dueDate")}
                />
              </Group>
            )}
            <Group grow align="start">
              <Select
                label={t("category")}
                placeholder={t("chooseCategory")}
                withAsterisk
                searchable
                data={categoryOptions}
                {...form.getInputProps("categoryId")}
              />
              <TextInput
                label={t("supplier")}
                withAsterisk={values.kind === "supplier_invoice"}
                {...form.getInputProps("supplier")}
              />
            </Group>
```

Replace

```tsx
            <Input.Wrapper label={t("paidBy")} labelElement="div">
              <SegmentedControl
                mt={4}
                aria-label={t("paidBy")}
                value={values.paidBy}
                onChange={(next) => form.setFieldValue("paidBy", next as PaidBy)}
                data={[
                  { value: "employee", label: t("paidByEmployee") },
                  { value: "company", label: t("paidByCompany") },
                ]}
              />
            </Input.Wrapper>
```

with

```tsx
            {values.kind === "supplier_invoice" ? (
              // Nobody is paid back for a supplier invoice: the company pays
              // the supplier and the server stores it so, and a control here
              // would offer a choice the save refuses.
              <Text size="sm" c="dimmed" data-testid="company-pays">
                {t("companyPaysSupplierInvoices")}
              </Text>
            ) : (
              <Input.Wrapper label={t("paidBy")} labelElement="div">
                <SegmentedControl
                  mt={4}
                  aria-label={t("paidBy")}
                  value={values.paidBy}
                  onChange={(next) => form.setFieldValue("paidBy", next as PaidBy)}
                  data={[
                    { value: "employee", label: t("paidByEmployee") },
                    { value: "company", label: t("paidByCompany") },
                  ]}
                />
              </Input.Wrapper>
            )}
```

Replace

```tsx
            {projectOptions.length === 0 && !values.projectId ? (
              <Text size="sm" c="dimmed">
                {t("noBookableProjects")}
              </Text>
            ) : (
```

with

```tsx
            {projectOptions.length === 0 && !values.projectId ? (
              <Input.Wrapper error={form.errors.projectId}>
                <Text size="sm" c="dimmed">
                  {values.kind === "supplier_invoice" ? t("noSupplierInvoiceProjects") : t("noBookableProjects")}
                </Text>
              </Input.Wrapper>
            ) : (
```

Replace

```tsx
                  <Select
                    label={t("project")}
                    placeholder={t("chooseProject")}
                    clearable
                    searchable
```

with

```tsx
                  <Select
                    label={t("project")}
                    placeholder={t("chooseProject")}
                    withAsterisk={values.kind === "supplier_invoice"}
                    clearable={values.kind !== "supplier_invoice"}
                    searchable
```

Replace

```tsx
        {values.kind === "outlay" && (
          <>
            <Divider />
            <Stack gap="xs">
              <Title order={6}>{t("receipts")}</Title>
              {needsReceipt && (
```

with

```tsx
        {takesReceipts(values.kind) && (
          <>
            <Divider />
            <Stack gap="xs">
              <Title order={6}>
                {values.kind === "supplier_invoice" ? t("supplierInvoiceDocument") : t("receipts")}
              </Title>
              {needsInvoiceDocument && (
                <Text size="sm" c="orange" data-testid="attach-supplier-invoice">
                  {t("attachSupplierInvoice")}
                </Text>
              )}
              {needsReceipt && (
```

- [ ] **Step 4: The drawer's details, the project page's button and line**

In `apps/expenses/frontend/src/components/entry-details.tsx`, replace

```ts
import { expenseKindLabelKey } from "../lib/status";
```

with

```ts
import { today } from "../lib/dates";
import { expenseKindLabelKey, takesReceipts } from "../lib/status";
```

Replace

```ts
  const perDiem = expense.kind === "per_diem" ? expense.perDiem : undefined;
```

with

```ts
  const perDiem = expense.kind === "per_diem" ? expense.perDiem : undefined;
  const supplierInvoice = expense.kind === "supplier_invoice";
  /**
   * Past its due date and not yet invoiced on — a line somebody should look
   * at. Informational only: nothing here records whether the supplier has
   * been paid (supplier invoices design D5). A reader who may not see billing
   * cannot see the invoice stamp either, so the due date alone decides for
   * them.
   */
  const overdue =
    supplierInvoice &&
    expense.dueDate !== undefined &&
    expense.dueDate < today() &&
    expense.billing?.invoice === undefined;
```

Replace

```tsx
        {expense.rateOverride && <Badge color="orange">{t("rateOverridden")}</Badge>}
      </Group>
```

with

```tsx
        {expense.rateOverride && <Badge color="orange">{t("rateOverridden")}</Badge>}
        {overdue && <Badge color="red">{t("overdue")}</Badge>}
      </Group>
```

Replace

```tsx
        <Field label={t("date")}>{format.date(expense.entryDate)}</Field>
```

with

```tsx
        <Field label={supplierInvoice ? t("invoiceDate") : t("date")}>{format.date(expense.entryDate)}</Field>
```

Replace

```tsx
            <Field label={t("supplier")}>{expense.supplier ?? t("notAvailable")}</Field>
```

with

```tsx
            <Field label={t("supplier")}>{expense.supplier ?? t("notAvailable")}</Field>
            {supplierInvoice && (
              <>
                <Field label={t("invoiceNumber")}>{expense.invoiceNumber ?? t("notAvailable")}</Field>
                <Field label={t("dueDate")}>
                  {expense.dueDate === undefined ? t("notAvailable") : format.date(expense.dueDate)}
                </Field>
              </>
            )}
```

Replace

```tsx
      {withReceipts && expense.kind === "outlay" && (
        <Stack gap={4}>
          <Title order={6}>{t("receipts")}</Title>
```

with

```tsx
      {withReceipts && takesReceipts(expense.kind) && (
        <Stack gap={4}>
          <Title order={6}>{supplierInvoice ? t("supplierInvoiceDocument") : t("receipts")}</Title>
```

In `apps/expenses/frontend/src/components/project-expenses-panel.tsx`, replace

```ts
import { IconAlertCircle, IconPlus, IconReceipt } from "@tabler/icons-react";
```

with

```ts
import { IconAlertCircle, IconFileInvoice, IconPlus, IconReceipt } from "@tabler/icons-react";
```

Replace

```ts
  const mayRecord = totals ? totals.capabilities.canRecord : summaryMissing;
  const showRecord = mayRecord && bookable !== undefined;
```

with

```ts
  const mayRecord = totals ? totals.capabilities.canRecord : summaryMissing;
  const showRecord = mayRecord && bookable !== undefined;
  /**
   * Who may record a supplier invoice, and on what. The project's financial
   * side books one and is often on no project team, so both come off the
   * summary — its capability and its own `project` — because `GET /projects`
   * offers such a reader nothing to open the form with (supplier invoices
   * design D2, D5).
   */
  const supplierInvoiceProject = totals?.capabilities.canRecordSupplierInvoice ? totals.project : undefined;
```

Replace

```tsx
            {showRecord && (
              <Button
                h={40}
                leftSection={<IconPlus size={16} />}
                onClick={() => setRecording({ mode: "create", project: bookable })}
              >
                {t("recordACost")}
              </Button>
            )}
```

with

```tsx
            <Group gap="sm">
              {showRecord && (
                <Button
                  h={40}
                  leftSection={<IconPlus size={16} />}
                  onClick={() => setRecording({ mode: "create", project: bookable })}
                >
                  {t("recordACost")}
                </Button>
              )}
              {supplierInvoiceProject && (
                <Button
                  h={40}
                  variant="light"
                  leftSection={<IconFileInvoice size={16} />}
                  onClick={() =>
                    setRecording({ mode: "create", kind: "supplier_invoice", project: supplierInvoiceProject })
                  }
                >
                  {t("recordASupplierInvoice")}
                </Button>
              )}
            </Group>
```

Replace

```tsx
            <Table.Tfoot>{bucket(t("bucketTotal"), figures.total)}</Table.Tfoot>
```

with

```tsx
            <Table.Tfoot>
              {bucket(t("bucketTotal"), figures.total)}
              {/* The supplier invoices' share of the total above it — a line
                  of its own, never a split: the total is still every line. */}
              {figures.supplierInvoices && (
                <Table.Tr data-testid={`project-expense-supplier-invoices-${currency}`}>
                  <Table.Td>{t("ofWhichSupplierInvoices")}</Table.Td>
                  <Table.Td ta="right">{count(figures.supplierInvoices.total.count)}</Table.Td>
                  <Table.Td ta="right">{format.money(figures.supplierInvoices.total.cost, currency)}</Table.Td>
                  <Table.Td ta="right">{format.money(figures.supplierInvoices.total.billAmount, currency)}</Table.Td>
                </Table.Tr>
              )}
            </Table.Tfoot>
```

- [ ] **Step 5: Run everything, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
F="apps/expenses/frontend/src/lib/status.ts apps/expenses/frontend/src/lib/money.ts apps/expenses/frontend/src/lib/search.test.ts \
 apps/expenses/frontend/src/api/projects.ts apps/expenses/frontend/src/api/project-expenses.ts \
 apps/expenses/frontend/src/lib/project-options.ts apps/expenses/frontend/src/pages/-expense-form-modal.tsx \
 apps/expenses/frontend/src/components/entry-details.tsx apps/expenses/frontend/src/components/project-expenses-panel.tsx \
 apps/expenses/frontend/src/components/project-expenses-panel.test.tsx apps/expenses/frontend/src/test/fixtures.ts \
 apps/expenses/frontend/src/test/server.ts apps/expenses/frontend/src/pages/supplier-invoice.test.tsx \
 apps/expenses/frontend/src/i18n.ts"
mise exec -- bunx biome check --write $F
mise exec -- bun run --cwd apps/expenses/frontend test
mise exec -- bun run --cwd apps/expenses/frontend typecheck && mise exec -- bun run --cwd apps/expenses/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bun run --cwd apps/host/frontend test && mise exec -- bun run --cwd apps/host/frontend typecheck
```
Expected: PASS — the new tests and every existing expenses test (the form's outlay and mileage tests, the panel's "Record a cost" tests, the approval tables through `expenseKindLabelKey`). If Mantine's `DateInput` is not a `textbox` by its label in this version, query it as the existing claim test does (`getByRole("textbox", { name: "Date" })` works today, so it is) and report otherwise.

Prove the tests can fail, restoring after each: drop the `supplier_invoice` option from the kind control's `data` — the form tests go red; send `paidBy` for every kind (`return { ...money, paidBy: values.paidBy }` unconditionally) — the two payload tests go red on `not.toHaveProperty("paidBy")`; remove the `projectId` validator — the refusal test goes red (a POST is sent and the fake answers 400 on `projectId`, which is not the client sentence); use `useProjectOptions(opened?.project, …)` without the kind — the picker assertion goes red; drop `kindFixed` from the control's condition — the panel's "no kind to choose" assertion goes red; remove `expense.billing?.invoice === undefined` from `overdue` — the invoiced case goes red; render the share row unconditionally with `figures.total` — the EUR assertion goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-7.txt <<'MSG'
feat(expenses-ui): a supplier invoice is recorded from My expenses or the project page and read out in full

The expense form offers Supplier invoice beside Outlay and Mileage where
projects exist: the supplier and the invoice number required, the
invoice date and a due date, Subcontractor preselected, billable to
start with, no payer control but a line saying the company pays, and the
supplier's invoice asked for once the draft exists. Its project picker is
the projects whose money the caller may see. The project page offers
Record a supplier invoice behind canRecordSupplierInvoice, fixed to the
project and the kind, and each currency card carries an "of which
supplier invoices" row. The drawer shows the number, the due date and an
overdue tag; the list filters by the kind. en + nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add $F && git commit -F /tmp/claude-1000/msg-si-7.txt -- $F
git show --stat HEAD && git status --short
```

---

### Task 8: The Projects frontend — the Costs section's "of which supplier invoices" line (D5)

The Economy tab's Costs section (`@vantigo/projects-ui`) writes the supplier invoices' share beneath the totals row when the block carries one. The Expenses tab's currency cards are the expenses package's and were Task 7's.

**Files:**
- Modify: `apps/projects/frontend/src/api/economy.ts`, `apps/projects/frontend/src/pages/project-economy.tsx`, `apps/projects/frontend/src/pages/project-economy.test.tsx`, `apps/projects/frontend/src/i18n.ts`
- Read first (do not change): `pages/project-economy.tsx:378-533`, `pages/project-economy.test.tsx:1-311,1141-1360`, `apps/projects/frontend/src/api-schema.d.ts` (the generated `ProjectEconomyExpenses.supplierInvoices`, `ProjectEconomySupplierInvoices`, Task 4)

**Interfaces:**
- Consumes: `expenses.supplierInvoices` (Task 4).
- Produces TS: `EconomySupplierInvoices`; i18n key `expensesOfWhichSupplierInvoices` in both catalogs; the row `data-testid="expense-supplier-invoices"`.

- [ ] **Step 1: The test, failing**

In `apps/projects/frontend/src/pages/project-economy.test.tsx`, replace

```ts
    if (block.otherCurrencies?.length === 0) throw new Error("otherCurrencies is absent when it is empty");
```

with

```ts
    if (block.supplierInvoices && block.approved === undefined) {
      throw new Error("supplierInvoices is the project's own currency's share, so it comes only with those figures");
    }
    if (block.otherCurrencies?.length === 0) throw new Error("otherCurrencies is absent when it is empty");
```

Replace

```ts
  it("says out loud that the expenses are not measured against the budget", async () => {
```

with

```ts
  it("writes the supplier invoices' share beneath the totals, and keeps the totals everything", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: tracked(
        expenses({
          supplierInvoices: {
            approved: bucket(1, 4000, 4400),
            submitted: bucket(0, 0, 0),
            draft: bucket(0, 0, 0),
            total: bucket(1, 4000, 4400),
          },
        }),
      ),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const share = await screen.findByTestId("expense-supplier-invoices");
    expect(share).toHaveTextContent("Of which supplier invoices");
    expect(share).toHaveTextContent("1");
    expect(share).toHaveTextContent(money(4000));
    expect(share).toHaveTextContent(money(4400));
    const total = within(screen.getByTestId("project-expenses")).getByText("Total").closest("tr") as HTMLElement;
    expect(total).toHaveTextContent(money(5500));
    expect(total).toHaveTextContent(money(6000));
  });

  it("draws no supplier-invoice row when the block carries none", async () => {
    stubEconomy(project(), plan([milestone()]), 200, { economy: tracked(expenses()) });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByTestId("project-expenses");
    expect(screen.queryByTestId("expense-supplier-invoices")).not.toBeInTheDocument();
  });

  it("says out loud that the expenses are not measured against the budget", async () => {
```

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/projects/frontend test src/pages/project-economy.test.tsx
```
Expected: FAIL — no `expense-supplier-invoices` row.

- [ ] **Step 2: The line**

In `apps/projects/frontend/src/api/economy.ts`, replace

```ts
export type EconomyExpenseCurrency = Schemas["ProjectEconomyExpenseCurrency"];
```

with

```ts
export type EconomyExpenseCurrency = Schemas["ProjectEconomyExpenseCurrency"];
/**
 * The part of the expenses block that is supplier invoices, in the project's
 * own currency — a line beneath the totals, never a split of them. Absent
 * when there are none (supplier invoices design D3).
 */
export type EconomySupplierInvoices = Schemas["ProjectEconomySupplierInvoices"];
```

In `apps/projects/frontend/src/pages/project-economy.tsx`, replace

```tsx
                        <Text size="sm" fw={600}>
                          {money(expenses.totalAmount)}
                        </Text>
                      </Table.Td>
                    </Table.Tr>
```

with

```tsx
                        <Text size="sm" fw={600}>
                          {money(expenses.totalAmount)}
                        </Text>
                      </Table.Td>
                    </Table.Tr>
                    {/* The supplier invoices' share of the totals above — the
                        server's own figures, never derived here, and a line of
                        its own: the totals are still every expense. */}
                    {expenses.supplierInvoices && (
                      <Table.Tr data-testid="expense-supplier-invoices">
                        <Table.Td>
                          <Text size="sm">{t("expensesOfWhichSupplierInvoices")}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{formatters.formatNumber(expenses.supplierInvoices.total.count)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{money(expenses.supplierInvoices.total.cost)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{money(expenses.supplierInvoices.total.amount)}</Text>
                        </Table.Td>
                      </Table.Tr>
                    )}
```

In `apps/projects/frontend/src/i18n.ts`, replace

```ts
    expenseTotal: "Total",
```

with

```ts
    expenseTotal: "Total",
    expensesOfWhichSupplierInvoices: "Of which supplier invoices",
```

Replace

```ts
    expenseTotal: "Totalt",
```

with

```ts
    expenseTotal: "Totalt",
    expensesOfWhichSupplierInvoices: "Herav leverandørfakturaer",
```

- [ ] **Step 3: Run everything, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
F="apps/projects/frontend/src/api/economy.ts apps/projects/frontend/src/pages/project-economy.tsx \
 apps/projects/frontend/src/pages/project-economy.test.tsx apps/projects/frontend/src/i18n.ts"
mise exec -- bunx biome check --write $F
mise exec -- bun run --cwd apps/projects/frontend test
mise exec -- bun run --cwd apps/projects/frontend typecheck && mise exec -- bun run --cwd apps/projects/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```
Expected: PASS — the two new tests, and `TAB_WITHOUT_EXPENSES` (the word-for-word Economy tab of an installation without expenses) unchanged.

Prove the tests can fail, restoring after each: render the row unconditionally from `expenses.totalCost` — the "no row" test goes red; render `expenses.supplierInvoices.approved` — unchanged here (approved equals total in the fixture), so change the fixture's `total` to `bucket(2, 4500, 5000)` and see the share test go red, then restore both. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-si-8.txt <<'MSG'
feat(projects-ui): the Costs section says how much of the expenses is supplier invoices

Beneath the totals row the Economy tab's Costs section writes "of which
supplier invoices" — the count, what they cost and what they pass on —
from the server's own sub-figure, when the block carries one. The totals
above it still count every expense. en + nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add $F && git commit -F /tmp/claude-1000/msg-si-8.txt -- $F
git show --stat HEAD && git status --short
```

---
### Task 9: Verify the whole branch and open the PR

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
mise exec -- bun run --cwd apps/expenses/frontend test && mise exec -- bun run --cwd apps/projects/frontend test
mise exec -- bun run --cwd apps/expenses/frontend typecheck && mise exec -- bun run --cwd apps/projects/frontend typecheck
mise exec -- bun run --cwd apps/expenses/frontend lint && mise exec -- bun run --cwd apps/projects/frontend lint
mise exec -- bun run --cwd apps/host/frontend test && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/expenses/frontend/src apps/projects/frontend/src
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short -- openapi/COVERAGE.md   # committed in Tasks 2–4 if it moved: must print nothing
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits, and the CI runner has 4; drop it if the suite is green without it. Run the frontend packages one at a time as above, not through `bun --filter`, which times out on CI's fan-out. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so rather than fixing it here. Test logs from parallel packages interleave: read a failure's own `--- FAIL` block, not the lines around it.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/expenses.yaml openapi/projects.yaml
git diff main..HEAD -- apps/server/internal/contracts apps/server/internal/module
git diff main..HEAD -- openapi/testdata/exchanges   # must print nothing
grep -rn "kind = 'outlay' AND (\(e\.\)\?paid_by IS NULL" apps/server/internal/expenses/queries/   # must print nothing: every copy calls the function
grep -rn "kindOutlay" apps/server/internal/expenses/*.go | grep -v _test   # each remaining one is outlay-only on purpose (the receipt threshold, parseOutlay, the switch arms that list both kinds)
cd apps/server && mise exec -- go test -count=1 -run 'TestNoModuleReferencesAnotherModulesSchema|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestServeMuxConflictsArePinned' ./internal/db/ ./internal/openapi/ && cd ../..
grep -rn "supplier invoice\|supplier_invoice\|supplierInvoices\|owes_employee" docs/expenses.md docs/projects.md docs/module-boundaries.md ROADMAP.md | head -40   # the docs say what the code does
```
Check, by eye: the spec commit plus eight task commits, each trailer exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; `go.mod`/`go.sum` still untracked; no existing `required:` list changed in either yaml (the diff adds `required:` only inside `ExpensesProjectSummarySupplierInvoices` and `ProjectEconomySupplierInvoices`); one migration, `00033` (expenses); no expenses file imports projects or the reverse; no query names another module's schema; no `float64` multiplied into money anywhere in the diff; no new operation (`git diff main..HEAD -- openapi/*.yaml | grep -c operationId` prints 0); every directory call the diff adds (`checkSupplierInvoiceProject`, `financialProjects`, `supplierInvoiceProjects`, `projectOption` from the summary) runs before any `withLockedTx`, which the harness's locked-call check has already proven on every test.

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/project-supplier-invoices
cat > /tmp/claude-1000/pr-supplier-invoices.md <<'MSG'
## Supplier invoices (Projects phase 3, delivery C)

A project's non-hours cost was whatever somebody put on an expense — a
company-paid outlay with a free-text supplier. After this the invoice a
supplier sends for work or goods on a project is a thing of its own inside
Expenses. Decided in `docs/superpowers/specs/2026-09-26-supplier-invoices-design.md`
(D1–D6).

- **A fourth kind** (`supplier_invoice`, migration `00033`): the outlay's money,
  a required supplier and supplier's invoice number, an optional due date on or
  after the invoice date (the entry date — one date, the one the period lock
  judges), always company-paid, never in a travel claim, always on a project,
  refused without the projects module.
- **One rule for who is owed**: the owes-the-employee predicate, written out
  thirteen times in SQL, is now `expenses.owes_employee(kind, paid_by)`, an
  `IMMUTABLE` function every such query calls; Go's `owesEmployee` is its mirror,
  tested against it on every kind and payer. A supplier invoice owes nobody: it
  never reaches the reimbursement list, the payroll CSV, a payroll run, the
  unreimbursed figures or the reimbursement attention.
- **Recorded by the project's financial side**: financial rights on the project
  rather than `CanLogTime` — on a completed project too, never a cancelled one —
  and attested like any cost; the supplier's invoice is required on submit, through
  the receipts mechanism.
- **Priced as an outlay, counted apart**: the outlay's markup; the expenses
  contract gains `CurrencyExpenses.SupplierInvoices` (a sub-figure, nil when none;
  every existing figure still means everything); the project summary and the
  economy's `expenses` block report it as `supplierInvoices`.
- **Visible to the project's financial side**: its rows, detail and document, to
  everyone with financial rights on its project; employees' outlays keep today's
  visibility.
- **Frontend**: the kind in the expense form (fields, Subcontractor preselected,
  billable on, "the company pays", "Attach the supplier's invoice"), its own
  project picker, the drawer's number, due date and overdue tag, the list filter
  and labels, **Record a supplier invoice** on the project page, "of which
  supplier invoices" on the Expenses tab's cards and in the Economy tab's Costs
  section; en + nb.
- **Integration**: projects and expenses composed for real — a finance reader
  records one on a completed project and the economy shows it.

Contract: no new operation; optional fields on `ExpensesEntryRequest`,
`ExpensesEntryUpdateRequest`, `ExpensesEntryResponse`, `GET /entries` and
`GET /projects` (`kind`), `ExpensesProjectSummaryCapabilities`,
`ExpensesProjectSummaryCurrency`, `ExpensesProjectSummaryResponse` and
`ProjectEconomyExpenses`; two new schemas. No existing `required:` changed, no
corpus touched (neither module has one).

Decisions on the record for review: thirteen SQL copies of the predicate, not
fourteen (`claims.sql:214` is a receipt count); the recorder gate is the caller's
financial rights, so `expenses:manage` recording for a colleague needs them too,
and an outlay turned into a supplier invoice (or back) is judged afresh; only
`cancelled` refuses; without projects the kind is refused on `kind`; the
summary's `canRecordSupplierInvoice` is optional in the contract and its
`project` booking option exists so the button can open for a reader on no team;
`GET /projects?kind=supplier_invoice` is the Expenses app's picker; "Record a
cost" no longer offers the kind and "Record a supplier invoice" fixes it;
Subcontractor found by name; "overdue" is past due and not invoiced; the Down
migration turns supplier invoices into company-paid outlays.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
MSG
gh pr create --base main --head feat/project-supplier-invoices \
  --title "Supplier invoices (Projects phase 3, delivery C)" \
  --body-file /tmp/claude-1000/pr-supplier-invoices.md
gh pr checks --watch
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -F body=@/tmp/claude-1000/pr-supplier-invoices.md` (`-F`, which reads the file; `-f` would send the literal string). Watch CI to green; a red check is fixed on the branch with a new commit (never `--amend`), and the report says what it was. Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL and CI's state; each test shown able to fail and what the mutation printed; anything the generators disagreed with this plan about (sqlc and the function's argument types — Task 1 Step 3's fallback — sqlc's name for the `supplier_invoice` flag, oapi-codegen's field names, Mantine's role for `DateInput`); and whether `main` was already red. Plus the places this branch decides what the spec left open, for the user's verdict — the fourteen numbered in the plan's preamble:

- thirteen SQL copies, not fourteen: `claims.sql:214` is the claim queue's receipt count and stays;
- the recorder gate is the *caller's* financial rights (`expenses:manage` for a colleague needs them too); a kept project is not re-judged only when the row already was a supplier invoice;
- only `cancelled` refuses; planned, active, on-hold and completed take one;
- without the projects module the refusal is on `kind`;
- the submit's document refusal reads "Expense *id* cannot be submitted yet: Attach the supplier's invoice";
- `canRecordSupplierInvoice` is optional in the contract (never extend `required:`) and always answered;
- the summary answers `project` (an `ExpensesProjectOption`) when the capability is true, because `GET /projects` offers a finance reader nothing;
- `GET /projects?kind=supplier_invoice` is the Expenses app's picker (role-holding projects only; `userId` for somebody else refused on `kind`);
- "Record a supplier invoice" fixes the kind; "Record a cost" no longer offers it; the saved kind stays locked in the UI as today;
- Subcontractor is found by name (`Subcontractor` / `Underleverandør`), nothing preselected otherwise;
- "overdue" = due date before today and no invoice stamp visible;
- `paid_by` stored `company` *and* the function answers false for the kind whatever it says;
- the Down migration turns supplier invoices into company-paid outlays before dropping the columns and the function;
- the "totals, not rows" note keeps its logic and learns the widening in its wording.

---

## Self-review

**Spec coverage** — every decision and every testing bullet maps to a step:

| Spec | Where |
| --- | --- |
| D1 `kind = 'supplier_invoice'`, the outlay's money rules (category, any ISO currency, gross > 0, VAT 0..gross, net as cost and markup base) | Task 2 Step 3 (`parseAmounts`, `parseSupplierInvoice`), Step 4 (`resolveValues`); tests `…_ARecordedInvoice…`, `…_TheBodyIsRefusedFieldByField` |
| D1 migration `00033`: `supplier` required ≤ 200, `supplier_invoice_number varchar(100)` required trimmed, `supplier_due_date` optional ≥ entry date; the entry date is the invoice date | Task 1 Step 1 (columns, pinned twice in `schema_test.go`); Task 2 Steps 1, 3, 4 (validation, persistence, `entryDate` description); Task 6 Step 1 ("One date") |
| D1 `paid_by` stored `company`, `employee` refused with the exact sentence; mileage and per diem fields refused | Task 2 Step 3; `…_TheBodyIsRefusedFieldByField` (paid by the employee, distance, passengers, per diem type, covered meal) |
| D1 never in a claim (400 on `claimId`), always on a project (400 on `projectId`), refused outright without projects | Task 2 Step 3; `…_TheBodyIsRefusedFieldByField` (in a travel claim, no project), `…_WithoutProjectsTheKindIsRefusedOutright` |
| D1 column names avoid `invoice_reference`/`invoiced_at` | Task 1 Step 1 (names and the migration's comment); `…_ARecordedInvoice…` asserts `billing.invoice` absent |
| D1 one function `expenses.owes_employee` (`IMMUTABLE`) in the migration, every predicate site calls it, Go mirror with a comment naming it | Task 1 Steps 1, 3 (the `sed` over thirteen sites, the `grep` proving none is left), `authorize.go`'s comment |
| D1 a supplier invoice owes nobody: reimbursement list, payroll CSV, unreimbursed stats, reimbursement attention | Task 2 `TestSupplierInvoices_OweNobody` (plus the payroll run and `myUnreimbursed`) |
| D2 financial rights (manager, `projects:manage-all`, `projects:view-financials` on a seen project) rather than `CanLogTime`; completed accepted, cancelled refused | Task 2 Step 4 (`checkSupplierInvoiceProject`); `…_TheRecordersFinancialRightsDecide` |
| D2 recorder is the owner; recording for a colleague needs `expenses:manage` | Task 2 Step 4; `…_TheOwnerAndManageChangeAndDeleteThem` |
| D2 flow: approve by `expenses:approve` or the project's manager, self-approval, reject, unapprove refused once invoiced | `…_AreAttestedPricedAndInvoicedLikeAnyCost` |
| D2 the invoice document is required on submit ("Attach the supplier's invoice"); receipts learn the kind (PDF) | Task 2 Step 5 (`supplierInvoiceDocumentRefusal`, `entryTakesReceipts`, `approvals.go`); `…_SubmitNeeds…`, `…_TheQueueCountsOneWithNoDocument` |
| D2 kind may change on a draft outlay ↔ supplier invoice; missing-field 400s say what the new kind needs | Task 2 Step 5 (`changeStrandsReceipts`), Step 4 (`keptProject` excludes a supplier invoice); `…_ADraftChangesBetweenOutlayAndSupplierInvoice` |
| D3 billing is the outlay's (billable needs a billable project; markup named → stored → default; bill = net × (1 + markup %); pricing door, invoiced stamp, ready to invoice) | Task 2 Step 4 (`resolveValues`, `carriedBillAmount`, `resolveBilling`, `marksUp` in the pricing door); `…_ARecordedInvoice…`, `…_AreAttested…` |
| D3 `CurrencyExpenses.SupplierInvoices *ExpenseSplit{Approved, Submitted, Draft, Total}`, nil when none, existing figures still everything | Task 3 Steps 1–2; `TestProjectExpenses_SupplierInvoicesAreASubFigureOfEveryBucket` |
| D3 economy `expenses.supplierInvoices {approved, submitted, draft, total: {count, cost, amount}}`, own currency, absent when none; margin, budget, portfolio, alerts unchanged | Task 4; `TestGetProjectEconomy_SupplierInvoicesAreASubFigureOfTheExpenses` (the rest of the answer byte-equal), `…_WithoutSupplierInvoicesInItsCurrencyTheKeyIsAbsent`, the two golden tests |
| D3 the Costs section's and the Expenses tab's "of which supplier invoices" | Task 8; Task 7 Step 4 (the currency card) |
| D4 visible to financial-rights holders on its project (list, detail, panel); outlays unchanged; the "totals, not rows" note no longer applies to these rows | Task 2 Step 6 (`accessFor`, the list's SQL, `financialProjects`); `…_TheProjectsFinancialSideSeesTheRows`; Task 7 Step 1 (the note's wording) |
| D5 kind control (outside claims, only with projects), the fields, Subcontractor preselected, billable on, no paid-by control with a line, receipts with "Attach the supplier's invoice" | Task 7 Step 3; `supplier-invoice.test.tsx` "a supplier invoice in the expense form" |
| D5 list filter, labels, drawer (number, due date, overdue), approval tables | Task 7 Steps 1, 4 (`standaloneExpenseKinds`, `expenseKindLabelKey`, `EntryDetails`); the "read out" and "My expenses" tests; the approval tables read `expenseKindLabelKey` and `attachmentCount` unchanged |
| D5 project page: **Record a supplier invoice** beside **Record a cost**, behind `canRecordSupplierInvoice`; the panel's list shows the kind | Task 2 Step 7 (the capability and `project`), Task 7 Step 4; the panel tests |
| D5 en + nb ("Leverandørfaktura") | Task 7 Step 1, Task 8 Step 2; `translations:check`, `i18n:test` |
| D6 docs: expenses (kinds, the function and its mirror, API table, receipts, what comes next), projects (the block, the Costs section, "what a receipt cost" reworded), module boundaries (the sub-figure), ROADMAP (phase 3 C done, what remains, supplier costs no longer a gap) | Task 6 Steps 1–4, checked against the code in Step 5 |
| Testing: validation (every required field; `paidBy=employee`, a claim, no project, no projects module, due date before entry date, mileage/per-diem fields) | `…_TheBodyIsRefusedFieldByField`, `…_WithoutProjectsTheKindIsRefusedOutright` |
| Testing: create/update/delete as owner and as `expenses:manage`; kind change outlay ↔ supplier invoice | `…_TheOwnerAndManageChangeAndDeleteThem`, `…_ADraftChangesBetweenOutlayAndSupplierInvoice` |
| Testing: the recorder gate (financial rights without a role; completed accepted; cancelled refused; a member without financial rights refused) | `…_TheRecordersFinancialRightsDecide` |
| Testing: submit refused without an attachment, accepted with one | `…_SubmitNeedsTheSuppliersInvoiceAttached` |
| Testing: the flow (approve by the manager and by `expenses:approve`, reject, unapprove, invoiced stamp, pricing door, ready to invoice) | `…_AreAttestedPricedAndInvoicedLikeAnyCost` |
| Testing: owes nobody on every surface; the function pinned by the schema test; every former predicate site exercised; the Go mirror against the function on every pair | `…_OweNobody`; `TestExpensesSupplierInvoices_AppliesAndIsIdempotent`; Task 1 Step 4 (the named tests, site by site); `TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction` |
| Testing: visibility to a `projects:view-financials` holder (list, detail, panel) and unchanged for outlays | `…_TheProjectsFinancialSideSeesTheRows` (the project's list is the panel's list) |
| Testing: `ProjectExpenses` sub-figure per currency (nil when none, buckets matching, totals unchanged); the summary's capability and its own line; contract coverage | `TestProjectExpenses_SupplierInvoicesAreASubFigureOfEveryBucket`, `TestExpensesProjectSummary_SupplierInvoicesHaveTheirOwnLine`, `…_TheSummarySaysWhoMayRecordOne`; `RequireCoverage` in every module run |
| Testing: projects shaping through the fake, absent/present, the golden test | Task 4 Steps 2, 4 |
| Testing: integration | Task 5 |
| Testing: frontend expenses (kind control and fields, the required-document hint, the payload, the button and its gate, the list filter and labels, the drawer's number/due date/overdue, both catalogs); projects (the Costs line); docs against the code | Task 7 Steps 2, 5; Task 8; Task 6 Step 5 |
| Out of scope (accounts payable, a paid state, a vendor register, inbound e-invoices, OCR, VAT codes, due-date reminders or attention, a supplier-invoice export, purchase orders, a supplier budget, a change to "budget used") | nothing in Tasks 1–8 adds any: no status value, no table, no attention type, no CSV column; `budgetUsed` code is untouched (the golden tests hold it) |

**Placeholder scan.** Every step carries its code, SQL, yaml, test and command. The hedges left are about what a generator emits — sqlc's handling of the function's argument types (with the exact fallback in Task 1 Step 3), sqlc's name for the `supplier_invoice` flag, oapi-codegen's field names, Mantine's role for `DateInput` — each with the instruction to use what it emits and report it.

**Name consistency.** Go: `kindSupplierInvoice`, `marksUp`, `takesReceipts`, `parseAmounts`, `parseSupplierInvoice`, `invoiceNumberMaxLength`, `supplierInvoiceNeedsProject`, `supplierInvoiceNotInClaim`, `supplierInvoicePaidByCompany`, `checkLine`, `checkSupplierInvoiceProject`, `projectCancelled`, `cannotRecordSupplierInvoice`, `supplierInvoiceOnCancelledProject`, `optionalPgDate`, `projectScope`, `financialProjects`, `supplierInvoiceDocumentRefusal`, `projectOption`, `supplierInvoiceProjects`, `owesEmployee`/`OwesEmployee`, `splitSum`, `sumOf`, `pickBucket`, `split`, `contracts.ExpenseSplit`, `CurrencyExpenses.SupplierInvoices`, `expenseSplit`, `expenseSplitOf`, `setSupplierInvoices`, `spentSplit`. SQL: `expenses.owes_employee(kind, paid_by)`, `supplier_invoice_number`, `supplier_due_date`, `@supplier_invoices_all`, `@financial_project_ids`, `supplier_invoice` (the grouped flag). Wire: `invoiceNumber`, `dueDate`, `kind=supplier_invoice`, `canRecordSupplierInvoice`, `project`, `supplierInvoices`, `ExpensesProjectSummarySupplierInvoices`, `ProjectEconomySupplierInvoices`. TS: `ExpenseKind` `"supplier_invoice"`, `entersAnAmount`, `takesReceipts`, `INVOICE_NUMBER_MAX_LENGTH`, `ProjectPicker`, `expenseProjectsQueryOptions(kind)`, `useProjectOptions(…, kind)`, `ProjectExpensesSupplierInvoices`, `EconomySupplierInvoices`, `supplierInvoice()`, `categoriesWithSubcontractor`, `supplierInvoiceProjects`, `canRecordSupplierInvoice`, `summaryProject`. Test ids: `company-pays`, `attach-supplier-invoice`, `project-expense-supplier-invoices-<CUR>`, `expense-supplier-invoices`. The refusal sentences are the same words in `entries_validation.go`/`entries.go`/`flow.go`, the Go tests, the fetch fake, the i18n catalogs (where the client says them itself) and `docs/expenses.md`.

**Real paths, numbers and commands.** Checked on the branch before writing: the latest migration is `00032_time_work_types.sql`, so `00033` is free; `apps/server/internal/expenses/sqlc.yaml` lists 00012–00014; every file under **Modify** exists (`ls`), every file under **Create** does not; `openapi/testdata/exchanges/` holds no `expenses.jsonl` or `projects.jsonl`; `apps/server/internal/openapi/cmd/contract` exists; `bun run gen:client`, `translations:check` and `i18n:test` are root `package.json` scripts; `test`, `typecheck` and `lint` are both packages' scripts; `/tmp/claude-1000/` exists; the seeded settings row has no receipt threshold and a 0 % default markup (00012), so the tests' outlays need no receipt and a supplier invoice bills its net; the second seeded category is `Subcontractor`, id 1002; `projects.visible` (00008) is the precedent for a schema-qualified SQL function in an sqlc query. **Every edit anchor was checked by a script** (`/tmp/claude-1000/anchorcheck.py`) that applies the plan's replacements in order to copies of the files — Task 1's `sed` included — and asserts each anchor occurs exactly once in its file at the moment it is applied: 186 anchors, none missing, none ambiguous. The same script then wrote the result — every replacement, every **Create**, the appends and the two whole-block replacements — into a scratch worktree of the spec commit, where `go generate ./...`, `gofmt`, `go vet`, `golangci-lint` (0 issues), the expenses, projects, customers, openapi, db, module and integration suites, `bun run gen:client`, both frontend packages' typecheck, lint and tests (expenses 234 tests, projects' economy 73) and `translations:check`/`i18n:test` all passed; that run is how the integration test's object store (Task 5) and the `time` import (Task 2 Step 4) came to be in this plan. `biome check --write` reformatted three of the touched frontend files there, which Task 7 Step 5 does as a matter of course. The two whole-block replacements it does not apply by text (`parseOutlay`, from its doc comment to `refuseMileageFields(body, kindOutlay, add)`; and `projectoptions.go`, replaced whole) were checked by hand: both bounds occur once.
