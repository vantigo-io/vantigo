# Invoices — the sales document (phase 1A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A new module, `invoices`, that issues the Norwegian sales document and nothing more: a seller record and one gap-free number series whose start locks at the first issue; a VAT-code table whose rates are dated periods; a draft that is issued, in one serialised transaction, into an immutable numbered document carrying a buyer snapshot, a seller snapshot and VAT per rate — immutability enforced by database triggers too; a PDF rendered from that snapshot, stored once in the object store and downloaded as stored, and a watermarked preview of a draft; full and partial credit notes in the same series with caps per line and on the headline; an invoice journal with a gap check; the two many-provider customer slots; `Status` and `MergedInto` on the customers contract's `CustomerBillingProfile`; the Invoices app (list, editor with live totals, issue dialog, credit flow, settings, journal) in en + nb; and the docs. One migration (`00034_invoices_baseline.sql`), one new contract (`openapi/invoices.yaml`, 18 operations), one additive change to an existing contract type (`contracts.CustomerBillingProfile`), no change to any existing OpenAPI schema.

**Architecture:** `apps/server/internal/invoices` is a module like expenses: `Module()` (four permissions, `Mount`, and the `CustomerReferences` and `CustomerPersonalData` slots), a strict oapi-codegen server over `openapi/invoices.yaml`, sqlc over its own migration only, `withLockedTx` for every write that locks, and `contractscalls.go` as the one door to the customer directory and the object store — which a test hook proves is never used inside a locked transaction. `config` refuses `MODULES=invoices` without `customers`. The issue (`issue.go`) reads the draft, checks the store and reads the billing profile first; then locks the document, shares the settings row, allocates the number from a counter row (`INSERT … ON CONFLICT … RETURNING next_value − 1`), and only then checks every rule, so a refusal rolls the number back; it writes the lines' VAT snapshots and the VAT summaries before the row itself, because triggers refuse line writes under an issued document. After the commit the PDF (`pdf.go`, maroto v2 with Noto Sans embedded) is rendered from the document's own rows only, hashed, put under `documents/<id>/<number>-<sha256>.pdf` in the `invoices` scope and recorded once (`pdfstore.go`). Credit notes (`credits.go`) copy the original's buyer snapshot and lines, and at issue lock the original after the counter and apply both caps. The journal (`journal.go`) reads one repeatable-read snapshot. Money is `math/big.Rat` throughout, read from JSON numbers through their shortest decimal text. The frontend package `apps/invoices/frontend` (`@vantigo/invoices-ui`) is mounted by the host under `/invoices`, with the host passing `customers:view` (the buyer picker reads the customers list) and the user's name (the "Vår ref." prefill).

**Tech Stack:** Go 1.27 (pgx, sqlc 1.31.1, goose, oapi-codegen v2.8.0 strict server), `github.com/johnfercher/maroto/v2 v2.4.2` over `github.com/phpdave11/gofpdf v1.4.3`, PostgreSQL 18, React + Mantine 9 + TanStack Query/Router, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-26-invoices-foundation-design.md` (D1–D13, Phase 1B, Out of scope, Testing) — binding. Research with file:line pointers: `docs/superpowers/research/2026-09-26-invoices-module.md`. The shapes this delivery copies: `apps/server/internal/expenses/{module.go,server.go,contractscalls.go,settings.go,values.go,money.go,attachments.go,harness_test.go,main_test.go,export_test.go}`, the customers counter (`db/migrations/00003_customers_baseline.sql:114-121`, `projects/queries/counters.sql`), the slots (`projects/customer_references.go`, `projects/customer_personal_data.go` and their tests), `contracts/directory.go`, `customers/directory.go:119-239`, the host's `apps.ts`/`navigation.ts`/`catalogs/admin.ts`/`routes/expenses*`, and `apps/expenses/frontend` for the package skeleton. `srv/` in the spec is `apps/server/internal/`.

## Global Constraints

- Branch `feat/invoices-foundation`; HEAD is the spec commit `d77fc256` (commit this plan on top of it before Task 1). Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable` for every `go test`. Never port 55432 — it belongs to another project.
- Never the untracked root `go.mod`/`go.sum`: they are not ours, never add, edit or delete them. The server's module is `apps/server/go.mod`; `go get` runs there (Task 5).
- Never edit `openapi/testdata/exchanges/*.jsonl`. Invoices has no corpus file; `contracttest.RequireCoverage` in `internal/invoices/main_test.go` gates it — every operation must answer 2xx in the module's own tests. The customers corpus must still validate after the contract change: the change is to the Go struct `contracts.CustomerBillingProfile`, not to `openapi/customers.yaml`.
- Existing schemas change additively: no existing OpenAPI schema is touched at all, `contracts.CustomerBillingProfile` only gains two fields, and no other module's migration changes.
- After any `openapi/*.yaml`, `queries/*.sql`, `sqlc.yaml` or migration change: `cd apps/server && mise exec -- go generate ./...` — **never package-scoped**; a second run must show no new diff. Then, from the repository root, `mise exec -- bun run gen:client`, and regenerate `openapi/COVERAGE.md` with `cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`. Commit every generated file.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec (each task's last step lists every path), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. Concurrent agents share ONE index: never commit a path you did not change.
- Every commit message ends with exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. Conventional Commits, scopes `invoices`, `customers`, `invoices-ui`, `frontend`, `docs`.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`). Run `golangci-lint` from `apps/server`; `0 issues.` beside any stderr is a failed run.
- **No directory or object-store call inside a locked transaction.** Every call goes through `internal/invoices/contractscalls.go`, whose hook the harness installs from `TestMain` and fails any test on (`lockedContractCalls`). The billing profile is read before `withLockedTx` opens; the PDF is stored after it commits.
- **Exact decimal** via `math/big.Rat` / `pgtype.Numeric` / SQL `numeric`: a JSON number becomes a decimal through `strconv.FormatFloat(f, 'f', -1, 64)` and `big.Rat.SetString`, never `SetFloat64`; every rounding is two decimals, the half away from zero (`big.Rat.FloatString`); VAT per rate on the sum of the nets.
- Money is shaped **absent, not null**: `creditedAmount`, `uncreditedAmount` and `creditNotes` exist only on an issued invoice, `credits` only on a credit note, `buyer`/`seller` only once written, `allowedIssueDates` only on a draft, `pdfStored` only on an issued document.
- **One refusal rule:** 404 for what does not exist (a bare body), 403 for what is not the caller's to do (the access layer's body), 400 naming the field (`HttpValidationProblemDetails`, keys like `lines[0].unitPrice`), 409 with the spec's codes (`InvoicesConflictProblem.code`; a stale revision is a 409 naming both revisions with no code), 503 `storage_unavailable`.
- **The Oslo business day** from `Deps.Clock()` (`businessDay` in `values.go`, `Europe/Oslo` a constant, `time/tzdata` embedded), never Postgres' `CURRENT_DATE`, which is UTC in the container.
- **Frontend rules:** at least one fixture per resource is a wire literal — the body the server sends; every new UI string in both catalogs (`en` + `nb`) of `apps/invoices/frontend/src/i18n.ts` (host strings in `apps/host/frontend/src/catalogs/*`), and `mise exec -- bun run translations:check` passes; a Mantine `Select` is a `combobox` in tests; **never assert "the last fetch"** — find the call by method and URL (`sent(fetchMock, "POST")`, `fetchMock.actualCalls.some(...)`); run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing; run each package's tests alone (`mise exec -- bun run --cwd <pkg> test`), never through `bun --filter`.
- Every new test must be shown able to fail (break the guard it pins, see red, restore). Say so in the report.
- **One implementer commits at a time.** If two agents ever share the tree, the second writes and verifies but does not commit; the controller commits by pathspec.

**Parallelism.** Tasks 1 → 8 are sequential: each builds on the previous one's contract, queries and generated code. Task 9 (docs) touches only `docs/`, `ROADMAP.md`, `CONTRIBUTING.md` and `deploy/compose/`, and may run beside Tasks 5–8 once Task 4 is in. Task 10 (frontend: list, editor, issue, credit) needs the generated `apps/invoices/frontend/src/api-schema.d.ts` of Task 6 and may run beside Tasks 7–9; Task 11 (frontend: settings, journal) needs Task 8's and follows Task 10 (it extends the same `i18n.ts`, `index.ts` and `test/fixtures.ts`). The trees are disjoint (`apps/server` / `docs`+`ROADMAP.md`+`CONTRIBUTING.md`+`deploy` / `apps/invoices/frontend`+`apps/host/frontend`); the one-committer rule still holds.

---

**How this plan reads the spec where it leaves a choice open.** Each is on the record for the user's verdict (Task 12 Step 4 repeats them):

1. **409 bodies.** One schema, `InvoicesConflictProblem` — ProblemDetails plus `code`, the customers module's `CustomerConflictProblem` precedent — with three optional fields a refusal may need: `mergedInto` (`customer_merged`), `linePosition` (`vat_code_inactive`, `vat_code_not_valid`, `credit_exceeds_line`) and `allowedIssueDates` (`issue_date_not_allowed`). The 503 `storage_unavailable` uses it too, so the code is in the same place. A stale revision carries no code, as the codebase's revision conflicts do.
2. **"A store is configured"** is `Deps.ObjectStore` set (the test seam) or `Config.StorageProvider` non-empty; with neither the module builds the real fail-closed store, and meta says `storageAvailable: false`.
3. **`GET /meta` also answers `today`** (Oslo). The journal's default range and a new draft's delivery prefill read it; no client computes Oslo's date.
4. **A draft's response carries `allowedIssueDates`**, computed by the same function the issue enforces (`issuedate.go`) against the latest issue date read without a lock; the issue dialog offers exactly those dates and re-derives nothing. The issue re-reads the latest date under the counter lock.
5. **A draft's totals and VAT summaries are computed with the rates in force today** — stored on save for the list, recomputed on every read — and a code with no period covering today counts at 0 % until the issue refuses it (`vat_code_not_valid`). A save accepts any active code.
6. **`PUT /invoices/{id}` on an invoice draft** is a full replace: `paymentTermsDays` is required (400 on it), an omitted reference is cleared, the customer may change (the gates run on the new one); the create-time prefills apply to `POST` only. `invoice_changed` is the issue's alone.
7. **A credit-note draft** keeps everything D8 does not list as changeable — customer, currency, delivery and its place, the three references, each line's VAT code, unit and original line — each attempt a 400 on the field; its note and internal note start empty (a correction's own words). Its live totals and VAT summaries use the original lines' snapshot rates, never today's.
8. **`creditedAmount`, `uncreditedAmount` and `creditNotes` are on an issued invoice only** — a draft invoice cannot have credit notes, and money is absent rather than zero.
9. **The rate operations** (`POST …/rates`, `DELETE …/rates/{rateId}`) take the settings row `FOR UPDATE` before the code, so an issue in flight commits first and "after the latest issue date" is read final — the same mechanism `PUT /settings` uses. `PUT /vat-codes/{id}` changing a category also checks that every existing period's rate fits it (400 on `ehfCategory`); an S code may carry an exemption reason (the CHECK allows it), which is never printed.
10. **The seeded codes have fixed ids 1–9**; the identity starts at 1001. Settings' text columns are `NOT NULL DEFAULT ''`, `''` meaning "not set", and the row is `CHECK (id = 1)`.
11. **The PDF test.** No Go library in the dependency graph reads text back out of an embedded-subset TrueType font, so what a document says is asserted on the renderer's intermediate model (`buildPDFModel`), and the bytes on reproducibility: two renders a second apart are identical, and carry the fixed `/ModDate (D:20260101000000` and `/CreationDate` = `issued_at`. The fixed modification date is `2026-01-01T00:00:00Z`.
12. **`Content-Disposition` is not declared in the contract**: `contracttest` hands kin-openapi only the `Content-Type` header, so a declared required header fails every exchange; the header is set by a `Visit` wrapper, as expenses' receipt download does.
13. **The merge holder takes `Deps.Clock`** besides nothing else (projects' precedent) for a draft's `updated_at`; "needs only the pool" is read as "needs nothing a disabled module's Deps lacks". The export's amounts are exact decimal strings.
14. **The gap check is literal**: `[max(first − 1, series_start) … last]` — the number just before the range's first is checked; numbers before that belong to the previous range's check.
15. **"Third-party notices" and "the release note"** have no file in this repository: the font's licence ships as `apps/server/internal/invoices/fonts/LICENSE` and `docs/invoices.md` names it, and the release note is a paragraph in `deploy/compose/README.md` "Upgrading". `vantigo.env.example` lists `invoices` in its explicit `MODULES`.
16. **The fonts are vendored** from pinned commits (Noto Sans Regular and Bold from `notofonts/notofonts.github.io@28b15b4b`, `OFL.txt` from `notofonts/latin-greek-cyrillic@4bc63d7e`) with their SHA-256 checked; the build needs no network.
17. **sqlc** mis-parses `@name::type + 1` ("syntax error at or near N"), so the counter and the journal use `sqlc.arg(name)::type`.
18. **The frontend is two tasks** (10: list, editor, issue, credit; 11: settings, journal). The host passes `canViewCustomers` and the user's display name through small wrappers (`routes/invoices/-invoice-access.tsx`), the pattern of expenses' `-my-expenses.tsx`.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00034_invoices_baseline.sql` | every 1A table, the checks, the exclusion, the immutability triggers, the seed (Task 1) |
| `apps/server/internal/invoices/{module,server,values,seller,meta}.go`, `sqlc.yaml`, `queries/{settings,counters,vatcodes}.sql` | the module, its store and `GET /meta` (Task 1) |
| `openapi/invoices.yaml`, `apps/server/internal/openapi/gen/cfg-invoices.yaml`, `apps/server/generate.go`, `internal/openapi/openapi.go`, `cmd/vantigo/main.go`, `internal/config/config.go`, `.golangci.yml`, `internal/db/schema_test.go`, `internal/module/compose_test.go` | the module in the platform (Task 1; the contract grows every task) |
| `apps/server/internal/invoices/{errors,decimal,settings,vatcodes}.go` | the seller record, the series start, VAT codes and rate periods (Task 2) |
| `apps/server/internal/contracts/directory.go`, `internal/customers/{directory.go,queries/customers.sql,directory_test.go}` | `Status` and `MergedInto` on the billing profile (Task 3) |
| `apps/server/internal/invoices/{contractscalls,money,issuedate,drafts,responses,list}.go`, `queries/{invoices,lines}.sql` | drafts, the money, the list (Task 3) |
| `apps/server/internal/invoices/issue.go` | the issue (Task 4) |
| `apps/server/internal/invoices/{pdf,pdfstore}.go`, `fonts/*`, `apps/server/go.mod`, `go.sum` | the PDF, store-once, download and preview (Task 5) |
| `apps/server/internal/invoices/credits.go`, `queries/credits.sql` | credit notes (Task 6) |
| `apps/server/internal/invoices/customer_slots.go`, `queries/customers.sql` | the merge holder and personal data (Task 7) |
| `apps/server/internal/invoices/journal.go`, `queries/journal.sql` | the journal (Task 8) |
| `docs/invoices.md`, `docs/{module-boundaries,customers,README}.md`, `ROADMAP.md`, `CONTRIBUTING.md`, `deploy/compose/{README.md,vantigo.env.example}` | D13 (Task 9) |
| `apps/invoices/frontend/**` | `@vantigo/invoices-ui` (skeleton Task 1; list, editor, issue, credit Task 10; settings, journal Task 11) |
| `apps/host/frontend/src/{navigation,apps,i18n}.ts`, `catalogs/{admin,navigation}.ts`, `routes/invoices*` | the app in the host (Tasks 1, 10, 11) |

---

### Task 1: The module skeleton: an empty Invoices app that mounts, generates and passes coverage (D1, the D2–D4 and D9 schema)

The whole phase-1A schema lands now — settings, counters, VAT codes and their rate periods, documents, lines and VAT summaries, the CHECKs, the exclusion constraint and the three immutability triggers — so no later task adds a migration. The contract has one operation, `GET /invoices/meta`, which already answers everything the pages need to start (it reads the seeded settings row, the seeded codes and the counter row). The module is wired into every list the checklist in `docs/module-boundaries.md` "Adding a module" names, and the frontend package and the host's app entry exist with one page that reads meta.

**Files:**
- Create: `apps/host/frontend/src/routes/invoices.tsx`, `apps/host/frontend/src/routes/invoices/index.tsx`, `apps/invoices/frontend/.gitignore`, `apps/invoices/frontend/eslint.config.js`, `apps/invoices/frontend/package.json`, `apps/invoices/frontend/src/api/meta.ts`, `apps/invoices/frontend/src/api/request.ts`, `apps/invoices/frontend/src/i18n.ts`, `apps/invoices/frontend/src/index.ts`, `apps/invoices/frontend/src/pages/invoices.test.tsx`, `apps/invoices/frontend/src/pages/invoices.tsx`, `apps/invoices/frontend/src/test/api.ts`, `apps/invoices/frontend/src/test/fetch.ts`, `apps/invoices/frontend/src/test/render.tsx`, `apps/invoices/frontend/src/test/setup.ts`, `apps/invoices/frontend/tsconfig.app.json`, `apps/invoices/frontend/tsconfig.json`, `apps/invoices/frontend/tsconfig.node.json`, `apps/invoices/frontend/vite.config.ts`, `apps/server/internal/db/migrations/00034_invoices_baseline.sql`, `apps/server/internal/invoices/harness_test.go`, `apps/server/internal/invoices/main_test.go`, `apps/server/internal/invoices/meta.go`, `apps/server/internal/invoices/meta_test.go`, `apps/server/internal/invoices/module.go`, `apps/server/internal/invoices/module_internal_test.go`, `apps/server/internal/invoices/queries/counters.sql`, `apps/server/internal/invoices/queries/settings.sql`, `apps/server/internal/invoices/queries/vatcodes.sql`, `apps/server/internal/invoices/seller.go`, `apps/server/internal/invoices/server.go`, `apps/server/internal/invoices/sqlc.yaml`, `apps/server/internal/invoices/values.go`, `apps/server/internal/openapi/gen/cfg-invoices.yaml`, `openapi/invoices.yaml`
- Modify: `apps/communications/frontend/eslint.config.js`, `apps/customers/frontend/eslint.config.js`, `apps/energy/frontend/eslint.config.js`, `apps/expenses/frontend/eslint.config.js`, `apps/host/frontend/package.json`, `apps/host/frontend/src/apps.test.ts`, `apps/host/frontend/src/apps.ts`, `apps/host/frontend/src/catalogs/admin.test.ts`, `apps/host/frontend/src/catalogs/admin.ts`, `apps/host/frontend/src/catalogs/navigation.ts`, `apps/host/frontend/src/i18n.ts`, `apps/host/frontend/src/navigation.ts`, `apps/products/frontend/eslint.config.js`, `apps/projects/frontend/eslint.config.js`, `apps/server/.golangci.yml`, `apps/server/cmd/vantigo/main.go`, `apps/server/generate.go`, `apps/server/internal/config/config.go`, `apps/server/internal/config/config_test.go`, `apps/server/internal/db/schema_test.go`, `apps/server/internal/module/compose_test.go`, `apps/server/internal/openapi/openapi.go`, `apps/time/frontend/eslint.config.js`, `tools/openapi/gen-client.test.ts`, `tools/openapi/gen-client.ts`
- Generated (commit them; never edit by hand): `apps/host/frontend/src/routeTree.gen.ts`, `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/counters.sql.go`, `apps/server/internal/invoices/store/db.go`, `apps/server/internal/invoices/store/models.go`, `apps/server/internal/invoices/store/settings.sql.go`, `apps/server/internal/invoices/store/vatcodes.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `bun.lock`, `openapi/COVERAGE.md`
- Read first (do not change): `docs/module-boundaries.md:268-318`, `apps/server/internal/expenses/{module.go,server.go,main_test.go,harness_test.go}`, `db/migrations/00005_energy_baseline.sql:11-16,85-100` (the exclusion), `00033_expenses_supplier_invoices.sql` (plpgsql in goose), `internal/db/schema_test.go:24-218,2393-2433`, `internal/config/config.go:1223-1283`, `apps/expenses/frontend/{package.json,eslint.config.js,vite.config.ts,src/api/request.ts,src/test/*}`, `apps/host/frontend/src/{apps.ts,navigation.ts,i18n.ts,catalogs/admin.ts,routes/expenses.tsx}`

**Interfaces:**
- Produces SQL: schema `invoices` with `settings` (one row, id 1), `counters`, `vat_codes` (seeded ids 1–9), `vat_code_rates` (`ex_vat_code_rates_no_overlap`), `invoices`, `lines`, `vat_summaries`; functions `invoices.refuse_issued_document_change()`, `invoices.refuse_issued_child_change()`; triggers `tr_invoices_immutable`, `tr_lines_immutable`, `tr_vat_summaries_immutable` (SQLSTATE `P0001`, message `invoices: issued document is immutable`).
- Produces Go: `invoices.Module()` (Name `invoices`, four permissions, `Mount` refusing a nil `Deps.Directory`); `businessDay(time.Time) time.Time` (Oslo); `sellerMissingFields(store.InvoicesSetting) []string`; `anythingIssued(ctx, *store.Queries) (bool, error)`; the store's `GetSettings`, `CounterNextValue`, `VatCodesInForce`.
- Produces wire: `GET /api/v1/invoices/meta` → `InvoicesMetaResponse {currency, defaultPaymentTermsDays, sellerComplete, missingSellerFields, anythingIssued, seriesStart, storageAvailable, today, vatCodes[], capabilities{canCreate, canIssue, canManage}}`.
- Produces TS: `@vantigo/invoices-ui` with `InvoicesPage`, `invoicesMetaQueryOptions`, `invoicesCatalog`; host `moduleKeys` + `"invoices"`, `moduleApp("invoices", …)`, route `/invoices`.
- Consumes: `contracts.CustomerDirectory` (required, not yet called), `storage.ObjectStore`.

- [ ] **Step 1: Pin the module in the platform's own tests, and see them fail**

`MODULES` gains `invoices`, which needs `customers`; the combined-contract test composes it; the schema tests pin the baseline and the reserved-word rule (D2).

**Replace** in `apps/server/internal/config/config_test.go`:

```go
}

func TestLoad_Modules(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); !slices.Equal(cfg.Modules, []string{"customers", "products", "energy", "communications", "projects", "time", "expenses"}) {
		t.Errorf("Modules = %v, want the default customers,products,energy,communications,projects,time,expenses when MODULES is unset", cfg.Modules)
	}

	cfg := mustLoad(t, with(validEnv(), "MODULES", " Customers ,, ENERGY,products "))
```

**with**:

```go
}

func TestLoad_Modules(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); !slices.Equal(cfg.Modules, []string{"customers", "products", "energy", "communications", "projects", "time", "expenses", "invoices"}) {
		t.Errorf("Modules = %v, want the default customers,products,energy,communications,projects,time,expenses,invoices when MODULES is unset", cfg.Modules)
	}

	cfg := mustLoad(t, with(validEnv(), "MODULES", " Customers ,, ENERGY,products "))
```

**Replace** in `apps/server/internal/config/config_test.go`:

```go
		t.Errorf("Modules = %v, want trimmed, lower-cased entries with empties dropped", cfg.Modules)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "customers,widgets")); !strings.Contains(msg, `"widgets" is not a known module`) || !strings.Contains(msg, "customers, products, energy, communications, projects, time, expenses") {
		t.Errorf("error = %q, want it to name the bad value and the known set", msg)
	}

```

**with**:

```go
		t.Errorf("Modules = %v, want trimmed, lower-cased entries with empties dropped", cfg.Modules)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "customers,widgets")); !strings.Contains(msg, `"widgets" is not a known module`) || !strings.Contains(msg, "customers, products, energy, communications, projects, time, expenses, invoices") {
		t.Errorf("error = %q, want it to name the bad value and the known set", msg)
	}

```

**Replace** in `apps/server/internal/config/config_test.go`:

```go

	if msg := loadError(t, with(validEnv(), "MODULES", "customers,time")); !strings.Contains(msg, "MODULES: time requires projects") {
		t.Errorf("error = %q, want time without projects named", msg)
	}

	cfg = mustLoad(t, with(validEnv(), "MODULES", "customers,energy,communications,projects,time"))
```

**with**:

```go

	if msg := loadError(t, with(validEnv(), "MODULES", "customers,time")); !strings.Contains(msg, "MODULES: time requires projects") {
		t.Errorf("error = %q, want time without projects named", msg)
	}

	// invoices reads its buyer through contracts.CustomerDirectory (invoices
	// foundation design D1), so it needs customers and nothing else.
	if msg := loadError(t, with(validEnv(), "MODULES", "invoices,projects")); !strings.Contains(msg, "MODULES: invoices requires customers") {
		t.Errorf("error = %q, want invoices without customers named", msg)
	}
	cfg = mustLoad(t, with(validEnv(), "MODULES", "customers,invoices"))
	if !slices.Equal(cfg.Modules, []string{"customers", "invoices"}) {
		t.Errorf("Modules = %v, want exactly customers,invoices", cfg.Modules)
	}

	cfg = mustLoad(t, with(validEnv(), "MODULES", "customers,energy,communications,projects,time"))
```

**Replace** in `apps/server/internal/module/compose_test.go`:

```go

	// Every business module contract here must actually mount, so all of
	// them are enabled; identity mounts regardless.
	handler, err := Compose(Deps{Access: access, Config: &config.Config{Modules: []string{"customers", "products", "energy", "communications", "projects", "time", "expenses"}}}, mods...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
```

**with**:

```go

	// Every business module contract here must actually mount, so all of
	// them are enabled; identity mounts regardless.
	handler, err := Compose(Deps{Access: access, Config: &config.Config{Modules: []string{"customers", "products", "energy", "communications", "projects", "time", "expenses", "invoices"}}}, mods...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
```

**Replace** in `apps/server/internal/db/schema_test.go`:

```go
// moduleSchemas are the PostgreSQL schemas owned by one module each. The
// platform schema is deliberately not one of these: internal/ratelimit
// reaches it from outside its own module, by design (Global Constraints).
var moduleSchemas = []string{"identity", "customers", "products", "energy", "communications", "projects", "time", "expenses"}

// schemaOwnedFile is one migration or query file, with the module that owns
// it and its full text.
```

**with**:

```go
// moduleSchemas are the PostgreSQL schemas owned by one module each. The
// platform schema is deliberately not one of these: internal/ratelimit
// reaches it from outside its own module, by design (Global Constraints).
var moduleSchemas = []string{"identity", "customers", "products", "energy", "communications", "projects", "time", "expenses", "invoices"}

// schemaOwnedFile is one migration or query file, with the module that owns
// it and its full text.
```

**Replace** in `apps/server/internal/db/schema_test.go`:

```go
	}
}

// TestCustomersRevision_AppliesWithANonUniqueLegalIdentityIndex proves
// 00016_customers_revision.sql applies, rolls back and re-applies cleanly, and
// pins the one thing about its index that is a design decision rather than a
```

**with**:

```go
	}
}

// TestInvoicesBaseline_AppliesAndIsIdempotent proves
// 00034_invoices_baseline.sql applies, rolls back and re-applies cleanly, and
// pins what the invoices foundation design rests on: the six tables, the one
// settings row and its CHECK, no counter row until something is issued (D2),
// the nine seeded VAT codes with one open 2026 period each — 6 as E and 7 as O
// — and the rules the database holds itself: the rate periods' exclusion (D3),
// the draft/number CHECK and the delivery CHECK (D4), and the three
// immutability triggers (D9).
func TestInvoicesBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 34) // 00034_invoices_baseline.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'invoices' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	gotTables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect tables: %v", err)
	}
	if want := []string{"counters", "invoices", "lines", "settings", "vat_code_rates", "vat_codes", "vat_summaries"}; !equalStrings(gotTables, want) {
		t.Errorf("tables = %v, want %v", gotTables, want)
	}

	var settingsRows, counterRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices.settings WHERE id = 1 AND legal_name = '' AND series_start = 1 AND default_payment_terms_days = 14 AND default_currency = 'NOK'`).Scan(&settingsRows); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if settingsRows != 1 {
		t.Errorf("settings rows = %d, want the one empty row", settingsRows)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invoices.settings (id, updated_at) VALUES (2, now())`); !isCheckViolation(err) {
		t.Errorf("a second settings row: %v, want a check violation from ck_settings_single_row", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices.counters`).Scan(&counterRows); err != nil {
		t.Fatalf("count counters: %v", err)
	}
	if counterRows != 0 {
		t.Errorf("counter rows = %d, want none until something is issued", counterRows)
	}

	codeRows, err := pool.Query(ctx, `
		SELECT c.code || ':' || c.ehf_category || ':' || c.saf_t_code || ':' || r.rate_percent::text || ':' || r.valid_from::text || ':' || coalesce(r.valid_to::text, 'open')
		FROM invoices.vat_codes c JOIN invoices.vat_code_rates r ON r.vat_code_id = c.id
		ORDER BY c.id`)
	if err != nil {
		t.Fatalf("query codes: %v", err)
	}
	gotCodes, err := pgx.CollectRows(codeRows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect codes: %v", err)
	}
	wantCodes := []string{
		"3:S:3:25.00:2026-01-01:open", "31:S:31:15.00:2026-01-01:open", "32:S:32:11.11:2026-01-01:open",
		"33:S:33:12.00:2026-01-01:open", "5:Z:5:0.00:2026-01-01:open", "51:AE:51:0.00:2026-01-01:open",
		"52:G:52:0.00:2026-01-01:open", "6:E:6:0.00:2026-01-01:open", "7:O:7:0.00:2026-01-01:open",
	}
	if !equalStrings(gotCodes, wantCodes) {
		t.Errorf("seeded codes = %v, want %v", gotCodes, wantCodes)
	}

	// The rate periods of one code never overlap, inclusive at both ends.
	if _, err := pool.Exec(ctx, `INSERT INTO invoices.vat_code_rates (vat_code_id, rate_percent, valid_from, created_at) VALUES (1, 26, DATE '2027-01-01', now())`); !isExclusionViolation(err) {
		t.Errorf("an overlapping period: %v, want an exclusion violation", err)
	}
	// A reason is required unless the category is S; the category is UNCL5305's.
	if _, err := pool.Exec(ctx, `INSERT INTO invoices.vat_codes (code, name, saf_t_code, ehf_category, created_at, updated_at) VALUES ('X', 'X', '5', 'Z', now(), now())`); !isCheckViolation(err) {
		t.Errorf("a Z code without a reason: %v, want a check violation", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invoices.vat_codes (code, name, saf_t_code, ehf_category, created_at, updated_at) VALUES ('Y', 'Y', '3', 'Q', now(), now())`); !isCheckViolation(err) {
		t.Errorf("an unknown category: %v, want a check violation", err)
	}
	// The code label is unique ignoring case.
	if _, err := pool.Exec(ctx, `INSERT INTO invoices.vat_codes (code, name, saf_t_code, ehf_category, created_at, updated_at) VALUES ('3', 'Again', '3', 'S', now(), now())`); !isUniqueViolation(err) {
		t.Errorf("a second code 3: %v, want a unique violation", err)
	}

	// A draft has no number; a number is an issued document's; delivery is a
	// day, a period with from <= to, or nothing.
	for _, bad := range []string{
		`INSERT INTO invoices.invoices (kind, status, number, customer_id, created_by_user_id, created_at, updated_at) VALUES ('invoice', 'draft', 7, 1, gen_random_uuid(), now(), now())`,
		`INSERT INTO invoices.invoices (kind, status, customer_id, created_by_user_id, created_at, updated_at) VALUES ('invoice', 'issued', 1, gen_random_uuid(), now(), now())`,
		`INSERT INTO invoices.invoices (kind, customer_id, delivery_date, delivery_from, created_by_user_id, created_at, updated_at) VALUES ('invoice', 1, DATE '2026-09-01', DATE '2026-09-01', gen_random_uuid(), now(), now())`,
		`INSERT INTO invoices.invoices (kind, customer_id, delivery_from, delivery_to, created_by_user_id, created_at, updated_at) VALUES ('invoice', 1, DATE '2026-09-02', DATE '2026-09-01', gen_random_uuid(), now(), now())`,
		`INSERT INTO invoices.invoices (kind, customer_id, created_by_user_id, created_at, updated_at) VALUES ('credit_note', 1, gen_random_uuid(), now(), now())`,
	} {
		if _, err := pool.Exec(ctx, bad); !isCheckViolation(err) {
			t.Errorf("%s: %v, want a check violation", bad, err)
		}
	}

	triggerRows, err := pool.Query(ctx, `
		SELECT c.relname || ':' || t.tgname
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'invoices' AND NOT t.tgisinternal
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("query triggers: %v", err)
	}
	gotTriggers, err := pgx.CollectRows(triggerRows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect triggers: %v", err)
	}
	if want := []string{"invoices:tr_invoices_immutable", "lines:tr_lines_immutable", "vat_summaries:tr_vat_summaries_immutable"}; !equalStrings(gotTriggers, want) {
		t.Errorf("triggers = %v, want %v", gotTriggers, want)
	}
}

// TestInvoicesSchema_NamesNoColumnWithAReservedWord pins the rule of D2: no
// column of the invoices schema is named with a word PostgreSQL reserves
// (catcode R in pg_get_keywords()) — to, from, end, user, order and the rest —
// so no query ever has to quote one, and no generated field is named after a
// keyword.
func TestInvoicesSchema_NamesNoColumnWithAReservedWord(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, 34)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var columns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'invoices'`).Scan(&columns); err != nil {
		t.Fatalf("count columns: %v", err)
	}
	if columns < 100 {
		t.Fatalf("the invoices schema has %d columns, want the whole 1A schema to be checked", columns)
	}
	rows, err := pool.Query(ctx, `
		SELECT c.table_name || '.' || c.column_name
		FROM information_schema.columns c
		JOIN pg_get_keywords() k ON k.word = c.column_name AND k.catcode = 'R'
		WHERE c.table_schema = 'invoices'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("query reserved column names: %v", err)
	}
	reserved, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect reserved column names: %v", err)
	}
	if len(reserved) > 0 {
		t.Errorf("columns named with a reserved word: %v", reserved)
	}
}

// TestCustomersRevision_AppliesWithANonUniqueLegalIdentityIndex proves
// 00016_customers_revision.sql applies, rolls back and re-applies cleanly, and
// pins the one thing about its index that is a design decision rather than a
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestLoad_Modules' ./internal/config/   # FAIL: "invoices" is not a known module
mise exec -- go test -count=1 -run 'TestInvoices|TestSqlcSchema' ./internal/db/   # FAIL: no 00034 migration, no invoices/sqlc.yaml
cd ../..
```

- [ ] **Step 2: The migration, the module's sqlc config and its first queries**

**Create** `apps/server/internal/db/migrations/00034_invoices_baseline.sql`:

```sql
-- +goose Up
-- Invoices, the sales document (invoices foundation design D2-D4, D9): the
-- whole phase 1A schema at once, so no later task of the delivery adds a
-- migration. customer_id is opaque — the customer lives in another schema
-- (docs/module-boundaries.md rule 4) and is read through
-- contracts.CustomerDirectory. Unlike 00012's house style this schema carries
-- CHECK constraints: an issued document is bookkeeping material, and the rules
-- that make it lawful are the database's to hold as well as the module's (D9).
-- No column is named with a word PostgreSQL reserves; a schema test pins it.
CREATE SCHEMA invoices;

-- btree_gist supplies the "=" operator class the rate periods' exclusion
-- needs (D3). 00005 already created it; repeated so this schema stands alone.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- The seller record and the series start (D2): one row, id 1, inserted below
-- with empty values. Every text column is NOT NULL with '' for "not set", so
-- "is the seller complete" is a question about empty strings, never NULLs.
CREATE TABLE invoices.settings (
    id                         smallint     PRIMARY KEY,
    legal_name                 varchar(200) NOT NULL DEFAULT '',
    organisation_number        varchar(9)   NOT NULL DEFAULT '',
    vat_registered             boolean      NOT NULL DEFAULT false,
    in_foretaksregisteret      boolean      NOT NULL DEFAULT false,
    address_line1              varchar(200) NOT NULL DEFAULT '',
    address_line2              varchar(200) NOT NULL DEFAULT '',
    postal_code                varchar(20)  NOT NULL DEFAULT '',
    city                       varchar(100) NOT NULL DEFAULT '',
    country                    char(2)      NOT NULL DEFAULT 'NO',
    bank_account               varchar(11)  NOT NULL DEFAULT '',
    iban                       varchar(34)  NOT NULL DEFAULT '',
    bic                        varchar(11)  NOT NULL DEFAULT '',
    email                      varchar(254) NOT NULL DEFAULT '',
    default_payment_terms_days integer      NOT NULL DEFAULT 14,
    default_currency           char(3)      NOT NULL DEFAULT 'NOK',
    footer_text                varchar(500) NOT NULL DEFAULT '',
    series_start               bigint       NOT NULL DEFAULT 1,
    updated_at                 timestamptz  NOT NULL,
    revision                   integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_settings_series_start CHECK (series_start >= 1),
    CONSTRAINT ck_settings_payment_terms CHECK (default_payment_terms_days BETWEEN 0 AND 365)
);

-- The number series (D2): the counter-row primitive customers uses
-- (00003_customers_baseline.sql), not a SEQUENCE, because a sequence burns a
-- value on rollback. next_value is the next number nobody has taken, as in
-- projects. There is one row, 'documents', and it exists exactly when
-- something has been issued: "anything issued" is "the row exists", never a
-- count(*).
CREATE TABLE invoices.counters (
    counter_name text   PRIMARY KEY,
    next_value   bigint NOT NULL
);

-- The VAT codes (D3): the tenant's label, the SAF-T standard tax code and the
-- UNCL5305 category. The rate is not here: it is a dated period in
-- vat_code_rates, so a rate change is a new period on the same code. A code is
-- never deleted; active = false stops offering it for new lines.
CREATE TABLE invoices.vat_codes (
    id               integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code             varchar(10)  NOT NULL,
    name             varchar(100) NOT NULL,
    saf_t_code       varchar(5)   NOT NULL,
    ehf_category     varchar(2)   NOT NULL,
    exemption_reason varchar(200),
    active           boolean      NOT NULL DEFAULT true,
    created_at       timestamptz  NOT NULL,
    updated_at       timestamptz  NOT NULL,
    revision         integer      NOT NULL DEFAULT 1,
    CONSTRAINT ck_vat_codes_category CHECK (ehf_category IN ('S', 'Z', 'E', 'AE', 'G', 'O', 'K')),
    CONSTRAINT ck_vat_codes_exemption_reason
        CHECK (ehf_category = 'S' OR (exemption_reason IS NOT NULL AND exemption_reason <> ''))
);
CREATE UNIQUE INDEX ux_vat_codes_code_lower ON invoices.vat_codes (lower(code));

-- A code's rates as periods (D3). valid_to NULL is open-ended; no two periods
-- of one code overlap, inclusive at both ends — the energy supply periods'
-- exclusion is the precedent (00005).
CREATE TABLE invoices.vat_code_rates (
    id           integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    vat_code_id  integer      NOT NULL REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    rate_percent numeric(5,2) NOT NULL,
    valid_from   date         NOT NULL,
    valid_to     date,
    created_at   timestamptz  NOT NULL,
    CONSTRAINT ck_vat_code_rates_period CHECK (valid_to IS NULL OR valid_to >= valid_from),
    CONSTRAINT ex_vat_code_rates_no_overlap EXCLUDE USING gist (
        vat_code_id WITH =,
        daterange(valid_from, valid_to, '[]') WITH &&
    )
);

-- The document (D4): a draft until it is issued into an immutable, numbered
-- salgsdokument. kind is 'invoice' or 'credit_note'; a draft has no number and
-- no issue date; an issued one carries both, the buyer snapshot, the seller
-- snapshot and its totals, and from then on the trigger below refuses any
-- change but the merge holder's customer_id and the PDF set once.
CREATE TABLE invoices.invoices (
    id                     bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind                   varchar(20)   NOT NULL,
    status                 varchar(20)   NOT NULL DEFAULT 'draft',
    number                 bigint,
    customer_id            integer       NOT NULL,
    credits_invoice_id     bigint        REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    issue_date             date,
    delivery_date          date,
    delivery_from          date,
    delivery_to            date,
    delivery_address_line1 varchar(200),
    delivery_address_line2 varchar(200),
    delivery_postal_code   varchar(20),
    delivery_city          varchar(100),
    delivery_country       char(2),
    payment_terms_days     integer,
    due_date               date,
    currency               char(3)       NOT NULL DEFAULT 'NOK',
    exchange_rate          numeric(14,6) NOT NULL DEFAULT 1,
    exchange_rate_date     date,
    your_reference         varchar(100)  NOT NULL DEFAULT '',
    our_reference          varchar(100)  NOT NULL DEFAULT '',
    order_reference        varchar(100)  NOT NULL DEFAULT '',
    note                   varchar(1000) NOT NULL DEFAULT '',
    internal_note          varchar(1000) NOT NULL DEFAULT '',
    -- The buyer snapshot (D4), written at issue from the billing profile — on a
    -- credit note, copied from the original when the draft is made.
    buyer_customer_number     bigint,
    buyer_type                varchar(20),
    buyer_name                varchar(200),
    buyer_organisation_number varchar(9),
    buyer_foreign_id          varchar(60),
    buyer_address_line1       varchar(200),
    buyer_address_line2       varchar(200),
    buyer_postal_code         varchar(20),
    buyer_city                varchar(100),
    buyer_region              varchar(100),
    buyer_country             char(2),
    buyer_peppol_id           varchar(100),
    buyer_gln                 varchar(13),
    buyer_language            varchar(2),
    -- The seller snapshot (D4), copied from invoices.settings at issue.
    seller_legal_name            varchar(200),
    seller_organisation_number   varchar(9),
    seller_vat_registered        boolean,
    seller_in_foretaksregisteret boolean,
    seller_address_line1         varchar(200),
    seller_address_line2         varchar(200),
    seller_postal_code           varchar(20),
    seller_city                  varchar(100),
    seller_country               char(2),
    seller_bank_account          varchar(11),
    seller_iban                  varchar(34),
    seller_bic                   varchar(11),
    seller_email                 varchar(254),
    seller_footer_text           varchar(500),
    net_total          numeric(14,2) NOT NULL DEFAULT 0,
    vat_total          numeric(14,2) NOT NULL DEFAULT 0,
    gross_total        numeric(14,2) NOT NULL DEFAULT 0,
    vat_total_nok      numeric(14,2) NOT NULL DEFAULT 0,
    pdf_object_key     varchar(300),
    pdf_sha256         char(64),
    issued_at          timestamptz,
    issued_by_user_id  uuid,
    created_by_user_id uuid          NOT NULL,
    created_at         timestamptz   NOT NULL,
    updated_at         timestamptz   NOT NULL,
    revision           integer       NOT NULL DEFAULT 1,
    CONSTRAINT ck_invoices_kind CHECK (kind IN ('invoice', 'credit_note')),
    CONSTRAINT ck_invoices_status CHECK (status IN ('draft', 'issued')),
    CONSTRAINT ck_invoices_number CHECK ((status = 'draft') = (number IS NULL)),
    CONSTRAINT ck_invoices_issue_date CHECK ((status = 'draft') = (issue_date IS NULL)),
    CONSTRAINT ck_invoices_credits CHECK ((kind = 'credit_note') = (credits_invoice_id IS NOT NULL)),
    CONSTRAINT ck_invoices_delivery CHECK (
        (delivery_date IS NOT NULL AND delivery_from IS NULL AND delivery_to IS NULL)
        OR (delivery_date IS NULL AND delivery_from IS NOT NULL AND delivery_to IS NOT NULL
            AND delivery_from <= delivery_to)
        OR (delivery_date IS NULL AND delivery_from IS NULL AND delivery_to IS NULL)
    ),
    CONSTRAINT ck_invoices_payment_terms CHECK (payment_terms_days IS NULL OR payment_terms_days BETWEEN 0 AND 365),
    CONSTRAINT ck_invoices_exchange_rate CHECK (exchange_rate > 0)
);
CREATE UNIQUE INDEX ux_invoices_number ON invoices.invoices (number);
CREATE INDEX ix_invoices_customer ON invoices.invoices (customer_id);
CREATE INDEX ix_invoices_credits ON invoices.invoices (credits_invoice_id) WHERE credits_invoice_id IS NOT NULL;
CREATE INDEX ix_invoices_issue_date ON invoices.invoices (issue_date) WHERE status = 'issued';

-- A document's lines (D4, D5). line_gross, line_allowance and line_net are
-- computed on every save; the VAT columns are the issue snapshot and NULL on a
-- draft. credits_line_id is, on a credit note's line, the original line it
-- credits.
CREATE TABLE invoices.lines (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id       bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    position         integer       NOT NULL,
    description      varchar(500)  NOT NULL,
    quantity         numeric(12,3) NOT NULL,
    unit             varchar(20)   NOT NULL DEFAULT '',
    unit_price       numeric(14,4) NOT NULL,
    discount_percent numeric(5,2)  NOT NULL DEFAULT 0,
    vat_code_id      integer       NOT NULL REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    credits_line_id  bigint        REFERENCES invoices.lines (id) ON DELETE RESTRICT,
    line_gross       numeric(14,2) NOT NULL,
    line_allowance   numeric(14,2) NOT NULL,
    line_net         numeric(14,2) NOT NULL,
    vat_rate_percent numeric(5,2),
    vat_category     varchar(2),
    saf_t_code       varchar(5),
    exemption_reason varchar(200),
    CONSTRAINT ck_lines_position CHECK (position >= 1),
    CONSTRAINT ck_lines_quantity CHECK (quantity > 0),
    CONSTRAINT ck_lines_unit_price CHECK (unit_price >= 0),
    CONSTRAINT ck_lines_discount CHECK (discount_percent BETWEEN 0 AND 100)
);
CREATE UNIQUE INDEX ux_lines_invoice_position ON invoices.lines (invoice_id, position);
CREATE INDEX ix_lines_vat_code ON invoices.lines (vat_code_id);
CREATE INDEX ix_lines_credits_line ON invoices.lines (credits_line_id) WHERE credits_line_id IS NOT NULL;

-- VAT per (category, rate) of an issued document (D4, D5), a row for each
-- 0 % category too (§ 5-1-5).
CREATE TABLE invoices.vat_summaries (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id       bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    vat_category     varchar(2)    NOT NULL,
    rate_percent     numeric(5,2)  NOT NULL,
    saf_t_code       varchar(5)    NOT NULL,
    exemption_reason varchar(200),
    taxable_amount   numeric(14,2) NOT NULL,
    vat_amount       numeric(14,2) NOT NULL,
    vat_amount_nok   numeric(14,2) NOT NULL
);
CREATE UNIQUE INDEX ux_vat_summaries_invoice_rate ON invoices.vat_summaries (invoice_id, vat_category, rate_percent);

-- Immutability, in SQL too (D9). An issued document is refused a DELETE, and
-- an UPDATE unless the only columns that differ are customer_id (the merge
-- holder, D10) and pdf_object_key / pdf_sha256 going from NULL to a value
-- once. The comparison is of the whole row as jsonb less those three, so a
-- column added later is covered without touching this function.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_issued_document_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF OLD.status <> 'issued' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(NEW) - 'customer_id' - 'pdf_object_key' - 'pdf_sha256')
           IS DISTINCT FROM (to_jsonb(OLD) - 'customer_id' - 'pdf_object_key' - 'pdf_sha256')
       OR (OLD.pdf_object_key IS NOT NULL AND NEW.pdf_object_key IS DISTINCT FROM OLD.pdf_object_key)
       OR (OLD.pdf_sha256 IS NOT NULL AND NEW.pdf_sha256 IS DISTINCT FROM OLD.pdf_sha256) THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_invoices_immutable
    BEFORE UPDATE OR DELETE ON invoices.invoices
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_document_change();

-- A line or a VAT summary under an issued document is refused any write. A
-- parent that is not there is a cascade from deleting a draft (the parent's
-- own trigger has already refused deleting an issued one), and is allowed.
-- An UPDATE is judged against both the old and the new parent, so a row
-- cannot be moved out from under an issued document either.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_issued_child_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') AND EXISTS (
        SELECT 1 FROM invoices.invoices WHERE id = OLD.invoice_id AND status = 'issued'
    ) THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND EXISTS (
        SELECT 1 FROM invoices.invoices WHERE id = NEW.invoice_id AND status = 'issued'
    ) THEN
        RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_lines_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.lines
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();

CREATE TRIGGER tr_vat_summaries_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.vat_summaries
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();

-- The single settings row, with empty values: the seller is incomplete until
-- somebody with invoices:manage fills it in.
INSERT INTO invoices.settings (id, updated_at) VALUES (1, now());

-- The seeded VAT codes (D3), each with one open period from 2026-01-01. The
-- ids are fixed, 1-9, below the identity's 1001, so a later code never
-- collides with them. 6 is E (unntatt, mval. kap. 3); 7 is O, for a seller
-- outside the VAT register.
INSERT INTO invoices.vat_codes (id, code, name, saf_t_code, ehf_category, exemption_reason, active, created_at, updated_at)
OVERRIDING SYSTEM VALUE VALUES
    (1, '3',  'Utgående mva 25 %',        '3',  'S',  NULL, true, now(), now()),
    (2, '31', 'Utgående mva 15 %',        '31', 'S',  NULL, true, now(), now()),
    (3, '32', 'Utgående mva 11,11 %',     '32', 'S',  NULL, true, now(), now()),
    (4, '33', 'Utgående mva 12 %',        '33', 'S',  NULL, true, now(), now()),
    (5, '5',  'Fritatt innenlands 0 %',   '5',  'Z',  'Fritatt for merverdiavgift', true, now(), now()),
    (6, '51', 'Omvendt avgiftsplikt 0 %', '51', 'AE', 'Omvendt avgiftsplikt – Merverdiavgift ikke beregnet', true, now(), now()),
    (7, '52', 'Utførsel 0 %',             '52', 'G',  'Utførsel av varer og tjenester', true, now(), now()),
    (8, '6',  'Utenfor mva-loven 0 %',    '6',  'E',  'Unntatt fra merverdiavgift (mval. kap. 3)', true, now(), now()),
    (9, '7',  'Ingen mva-behandling',     '7',  'O',  'Selger er ikke registrert i Merverdiavgiftsregisteret', true, now(), now())
ON CONFLICT DO NOTHING;

INSERT INTO invoices.vat_code_rates (vat_code_id, rate_percent, valid_from, valid_to, created_at) VALUES
    (1, 25.00, DATE '2026-01-01', NULL, now()),
    (2, 15.00, DATE '2026-01-01', NULL, now()),
    (3, 11.11, DATE '2026-01-01', NULL, now()),
    (4, 12.00, DATE '2026-01-01', NULL, now()),
    (5, 0,     DATE '2026-01-01', NULL, now()),
    (6, 0,     DATE '2026-01-01', NULL, now()),
    (7, 0,     DATE '2026-01-01', NULL, now()),
    (8, 0,     DATE '2026-01-01', NULL, now()),
    (9, 0,     DATE '2026-01-01', NULL, now())
ON CONFLICT DO NOTHING;

-- +goose Down
-- The schema only: btree_gist stays, energy's exclusion needs it.
DROP SCHEMA invoices CASCADE;
```

**Create** `apps/server/internal/invoices/sqlc.yaml`:

```yaml
# sqlc: invoices' typed queries. Schema = this module's own migrations only
# (sqlc reads the -- +goose Up sections); internal/db's schema_test keeps that
# list in step with the migrations directory; queries =
# internal/invoices/queries.
version: "2"
sql:
  - engine: postgresql
    schema:
      - ../db/migrations/00034_invoices_baseline.sql
    queries: queries
    gen:
      go:
        package: store
        out: store
        sql_package: pgx/v5
        emit_pointers_for_null_types: true
        emit_json_tags: false
        overrides:
          - db_type: uuid
            go_type: github.com/google/uuid.UUID
          - db_type: uuid
            nullable: true
            go_type:
              import: github.com/google/uuid
              type: UUID
              pointer: true
          - db_type: timestamptz
            go_type: time.Time
          - db_type: timestamptz
            nullable: true
            go_type:
              type: time.Time
              pointer: true
```

**Create** `apps/server/internal/invoices/queries/counters.sql`:

```sql
-- name: CounterNextValue :one
-- CounterNextValue reads the document counter's next_value without
-- allocating (D2). No row means nothing has ever been issued — "something is
-- issued" is "the counter row exists", never a count(*) — which the caller
-- reads from pgx.ErrNoRows.
SELECT next_value FROM invoices.counters WHERE counter_name = 'documents';
```

**Create** `apps/server/internal/invoices/queries/settings.sql`:

```sql
-- name: GetSettings :one
-- GetSettings reads the installation's single settings row, the seller record
-- and the series start (invoices foundation design D2). The migration writes
-- it, so this always answers a row.
SELECT * FROM invoices.settings WHERE id = 1;
```

**Create** `apps/server/internal/invoices/queries/vatcodes.sql`:

```sql
-- name: VatCodesInForce :many
-- VatCodesInForce is every active VAT code with the rate of the period that
-- covers day (D3): what GET /meta offers a new line. A code with no period
-- covering day is not in force and is left out.
SELECT c.id, c.code, c.name, c.saf_t_code, c.ehf_category, c.exemption_reason, r.rate_percent
FROM invoices.vat_codes c
JOIN invoices.vat_code_rates r ON r.vat_code_id = c.id
WHERE c.active
  AND r.valid_from <= @day::date
  AND (r.valid_to IS NULL OR r.valid_to >= @day::date)
ORDER BY lower(c.code), c.id;
```

- [ ] **Step 3: The contract with only GET /invoices/meta, and its generators**

The contract's schemas and paths grow in every later task by insertions immediately before the lines `info:` and `servers:`; this is the file those insertions land in.

**Create** `openapi/invoices.yaml`:

```yaml
components:
    schemas:
        InvoicesMetaCapabilities:
            description: What the caller may do in the Invoices app, answered by the server so no client re-derives a permission rule.
            properties:
                canCreate:
                    description: invoices:create — create, edit and delete drafts, and preview a draft.
                    type: boolean
                canIssue:
                    description: invoices:issue — issue a draft, and create a credit-note draft.
                    type: boolean
                canManage:
                    description: invoices:manage — the seller record, the series start and the VAT codes.
                    type: boolean
            required:
                - canCreate
                - canIssue
                - canManage
            type: object
        InvoicesMetaResponse:
            description: What every Invoices page needs before it draws anything (invoices foundation design D2).
            properties:
                anythingIssued:
                    description: Whether any document has been issued — the counter row exists. Once true the series start is locked.
                    type: boolean
                capabilities:
                    $ref: '#/components/schemas/InvoicesMetaCapabilities'
                currency:
                    description: The one currency a document may carry in this phase, NOK.
                    type: string
                defaultPaymentTermsDays:
                    description: The seller's default payment terms, which a new draft takes when neither the request nor the customer's billing profile decides.
                    format: int32
                    type: integer
                missingSellerFields:
                    description: The seller fields issuing still needs, by their camelCase names (legalName, organisationNumber, addressLine1, postalCode, city, bankAccount). Empty when the seller is complete.
                    items:
                        type: string
                    type: array
                sellerComplete:
                    description: Whether the seller record is complete enough to issue. Issuing is refused with 409 seller_incomplete until it is.
                    type: boolean
                seriesStart:
                    description: The first number of the one series invoices and credit notes share.
                    format: int64
                    type: integer
                storageAvailable:
                    description: Whether this installation has an object store. Without one nothing can be issued (503 storage_unavailable), because an issued number whose PDF can never be stored is not allowed to exist.
                    type: boolean
                today:
                    description: Today in Europe/Oslo, the business day every date rule of this module is judged on.
                    format: date
                    type: string
                vatCodes:
                    description: The VAT codes a new line may take — active, with a rate period covering today — each with today's rate.
                    items:
                        $ref: '#/components/schemas/InvoicesVatCodeInForce'
                    type: array
            required:
                - currency
                - defaultPaymentTermsDays
                - sellerComplete
                - missingSellerFields
                - anythingIssued
                - seriesStart
                - storageAvailable
                - today
                - vatCodes
                - capabilities
            type: object
        InvoicesVatCodeInForce:
            description: One VAT code as a new line may take it, with the rate in force today. A draft holds only the code; the rate a line carries is resolved at issue for the issue date (D3).
            properties:
                code:
                    type: string
                ehfCategory:
                    description: The UNCL5305 category — S, Z, E, AE, G, O or K.
                    type: string
                exemptionReason:
                    description: The text printed under the VAT summary for a category other than S. Absent for S.
                    type: string
                id:
                    format: int32
                    type: integer
                name:
                    type: string
                ratePercent:
                    format: double
                    type: number
                safTCode:
                    type: string
            required:
                - id
                - code
                - name
                - safTCode
                - ehfCategory
                - ratePercent
            type: object
info:
    title: Vantigo Invoices API
    version: "1"
openapi: 3.0.3
paths:
    /api/v1/invoices/meta:
        get:
            description: What every Invoices page needs in one read — the currency, the default payment terms, whether the seller is complete and what it lacks, whether anything is issued and the series start, whether an object store exists, today in Oslo, the VAT codes in force today and the caller's capabilities.
            operationId: getInvoicesMeta
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesMetaResponse'
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
            summary: Get the Invoices metadata
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
servers:
    - url: /
```

**Create** `apps/server/internal/openapi/gen/cfg-invoices.yaml`:

```yaml
# oapi-codegen: models and the strict net/http server interface for
# openapi/invoices.yaml. References into common.yaml become imports of
# internal/apicommon/gen.
package: gen
output: internal/invoices/gen/api.gen.go
generate:
  models: true
  std-http-server: true
  strict-server: true
import-mapping:
  common.yaml: github.com/vantigo-io/vantigo/server/internal/apicommon/gen
output-options:
  skip-prune: true
```

**Replace** in `apps/server/generate.go`:

```go
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-communications.yaml ../../openapi/communications.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-time.yaml ../../openapi/time.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-expenses.yaml ../../openapi/expenses.yaml

// sqlc's typed query layer. Pinned in mise.toml ("aqua:sqlc-dev/sqlc"), so it
// is already on PATH by the time go generate runs.
```

**with**:

```go
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-communications.yaml ../../openapi/communications.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-time.yaml ../../openapi/time.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-expenses.yaml ../../openapi/expenses.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-invoices.yaml ../../openapi/invoices.yaml

// sqlc's typed query layer. Pinned in mise.toml ("aqua:sqlc-dev/sqlc"), so it
// is already on PATH by the time go generate runs.
```

**Replace** in `apps/server/generate.go`:

```go
//go:generate sqlc generate -f internal/communications/sqlc.yaml
//go:generate sqlc generate -f internal/time/sqlc.yaml
//go:generate sqlc generate -f internal/expenses/sqlc.yaml
```

**with**:

```go
//go:generate sqlc generate -f internal/communications/sqlc.yaml
//go:generate sqlc generate -f internal/time/sqlc.yaml
//go:generate sqlc generate -f internal/expenses/sqlc.yaml
//go:generate sqlc generate -f internal/invoices/sqlc.yaml
```

**Replace** in `apps/server/internal/openapi/openapi.go`:

```go

// Modules are the contract files that carry paths; common.yaml only holds
// shared components.
var Modules = []string{"identity", "customers", "products", "energy", "communications", "projects", "time", "expenses"}

//go:embed specs/*.yaml
var specs embed.FS
```

**with**:

```go

// Modules are the contract files that carry paths; common.yaml only holds
// shared components.
var Modules = []string{"identity", "customers", "products", "energy", "communications", "projects", "time", "expenses", "invoices"}

//go:embed specs/*.yaml
var specs embed.FS
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

This writes `internal/openapi/specs/invoices.yaml`, `internal/invoices/gen/api.gen.go` and `internal/invoices/store/*`. The build now fails on the missing module package — the next steps write it.

- [ ] **Step 4: The module's tests: the harness, the coverage gate, meta and the catalog**

The fake directory is built against `internal/contracts` (depguard keeps `internal/customers` out); the fake store records every `Delete`, which the module must never call. Task 3 gives the fake its fixtures and the locked-call hook.

**Create** `apps/server/internal/invoices/main_test.go`:

```go
package invoices_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every invoices test against
// invoices.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate. Invoices has no frozen corpus file (openapi/testdata/
// exchanges holds none for it): this gate is what proves every operation is
// exercised.
var recorder = contracttest.NewForModule("invoices")

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
```

**Create** `apps/server/internal/invoices/harness_test.go`:

```go
package invoices_test

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// harness is one invoices installation for one test: internal/modtest's shared
// harness composing this module beside identity, every exchange validated
// against invoices.yaml through the package recorder, plus the fake customer
// directory and the fake object store it was composed with.
//
// The directory is a fake built directly against internal/contracts —
// depguard forbids internal/invoices/** from importing internal/customers,
// even in tests. modtest always lists customers in MODULES, so "invoices
// requires customers" holds for every harness here.
type harness struct {
	*modtest.Harness
	customers *fakeCustomers
	// objects is nil for an installation without an object store.
	objects *fakeObjectStore
}

// newHarness is an invoices installation with an object store.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newInvoicesHarness(t, newFakeObjectStore(), opts...)
}

// newHarnessWithoutStore is an installation whose operator configured no
// object store: Deps.ObjectStore stays nil and STORAGE_PROVIDER is unset, so
// the module builds the real, fail-closed store.
func newHarnessWithoutStore(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newInvoicesHarness(t, nil, opts...)
}

func newInvoicesHarness(t *testing.T, objects *fakeObjectStore, opts ...modtest.Option) *harness {
	t.Helper()
	customers := newFakeCustomers()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(invoices.Module()),
		modtest.WithDirectory(customers),
	}
	if objects != nil {
		base = append(base, modtest.WithObjectStore(objects))
	}
	return &harness{Harness: modtest.New(t, append(base, opts...)...), customers: customers, objects: objects}
}

// fakeCustomers is contracts.CustomerDirectory over billing profiles a test
// puts in. It models what the invoices gates read — Status and MergedInto on
// the billing profile (D10) — and is safe for concurrent use, because the
// handlers of one harness read it from many requests at once.
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

func newFakeCustomers() *fakeCustomers {
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{}}
}

func (f *fakeCustomers) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: p.ID, Name: p.Name, Archived: p.Archived}, nil
}

func (f *fakeCustomers) Customers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	out := []contracts.CustomerEntry{}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	for _, id := range slices.Compact(sorted) {
		entry, _ := f.Customer(ctx, id)
		if entry != nil {
			out = append(out, *entry)
		}
	}
	return out, nil
}

func (f *fakeCustomers) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (f *fakeCustomers) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return []contracts.ContactMatch{}, nil
}

func (f *fakeCustomers) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Delete,
// because the module must never call one (D7).
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deletes int
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string][]byte{}}
}

func (s *fakeObjectStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = body
	return nil
}

func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (s *fakeObjectStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.objects, key)
	return nil
}
```

**Create** `apps/server/internal/invoices/meta_test.go`:

```go
package invoices_test

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// metaJSON is GET /meta as a client reads it.
type metaJSON struct {
	Currency                string   `json:"currency"`
	DefaultPaymentTermsDays int32    `json:"defaultPaymentTermsDays"`
	SellerComplete          bool     `json:"sellerComplete"`
	MissingSellerFields     []string `json:"missingSellerFields"`
	AnythingIssued          bool     `json:"anythingIssued"`
	SeriesStart             int64    `json:"seriesStart"`
	StorageAvailable        bool     `json:"storageAvailable"`
	Today                   string   `json:"today"`
	VatCodes                []struct {
		ID              int32   `json:"id"`
		Code            string  `json:"code"`
		Name            string  `json:"name"`
		SafTCode        string  `json:"safTCode"`
		EhfCategory     string  `json:"ehfCategory"`
		ExemptionReason *string `json:"exemptionReason"`
		RatePercent     float64 `json:"ratePercent"`
	} `json:"vatCodes"`
	Capabilities struct {
		CanCreate bool `json:"canCreate"`
		CanIssue  bool `json:"canIssue"`
		CanManage bool `json:"canManage"`
	} `json:"capabilities"`
}

const metaPath = "/api/v1/invoices/meta"

func getMeta(t *testing.T, h *harness, permissions ...string) metaJSON {
	t.Helper()
	res := h.SignIn(t, permissions...).Do(http.MethodGet, metaPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /meta = %d %s, want 200", res.Status, res.Body)
	}
	var meta metaJSON
	res.JSON(&meta)
	return meta
}

// A fresh installation: NOK, fourteen days, a seller with nothing filled in,
// nothing issued, the series starting at 1, and the nine seeded codes at their
// 2026 rates — 6 as E and 7 as O (D3).
func TestMeta_AnswersAFreshInstallation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	meta := getMeta(t, h, "invoices:access")

	if meta.Currency != "NOK" || meta.DefaultPaymentTermsDays != 14 {
		t.Errorf("currency, terms = %q, %d, want NOK, 14", meta.Currency, meta.DefaultPaymentTermsDays)
	}
	wantMissing := []string{"legalName", "organisationNumber", "addressLine1", "postalCode", "city", "bankAccount"}
	if meta.SellerComplete || !slices.Equal(meta.MissingSellerFields, wantMissing) {
		t.Errorf("seller = complete %v missing %v, want incomplete missing %v", meta.SellerComplete, meta.MissingSellerFields, wantMissing)
	}
	if meta.AnythingIssued || meta.SeriesStart != 1 || !meta.StorageAvailable {
		t.Errorf("issued %v, start %d, storage %v; want false, 1, true", meta.AnythingIssued, meta.SeriesStart, meta.StorageAvailable)
	}
	if meta.Today != "2026-09-12" {
		t.Errorf("today = %q, want the harness's 2026-09-12 in Oslo", meta.Today)
	}
	type code struct {
		code, category, safT string
		rate                 float64
	}
	var got []code
	for _, c := range meta.VatCodes {
		got = append(got, code{c.Code, c.EhfCategory, c.SafTCode, c.RatePercent})
	}
	want := []code{
		{"3", "S", "3", 25}, {"31", "S", "31", 15}, {"32", "S", "32", 11.11}, {"33", "S", "33", 12},
		{"5", "Z", "5", 0}, {"51", "AE", "51", 0}, {"52", "G", "52", 0}, {"6", "E", "6", 0}, {"7", "O", "7", 0},
	}
	if !slices.Equal(got, want) {
		t.Errorf("VAT codes = %+v, want %+v", got, want)
	}
	for _, c := range meta.VatCodes {
		if (c.EhfCategory == "S") != (c.ExemptionReason == nil) {
			t.Errorf("code %s: exemption reason %v, want one exactly when the category is not S", c.Code, c.ExemptionReason)
		}
	}
	if meta.Capabilities.CanCreate || meta.Capabilities.CanIssue || meta.Capabilities.CanManage {
		t.Errorf("capabilities = %+v for invoices:access alone, want none", meta.Capabilities)
	}
}

// Each capability is its permission, beside invoices:access.
func TestMeta_CapabilitiesAreThePermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	meta := getMeta(t, h, "invoices:access", "invoices:create", "invoices:issue", "invoices:manage")
	if !meta.Capabilities.CanCreate || !meta.Capabilities.CanIssue || !meta.Capabilities.CanManage {
		t.Errorf("capabilities = %+v, want all three", meta.Capabilities)
	}
	if meta := getMeta(t, h, "invoices:access", "invoices:issue"); meta.Capabilities.CanCreate || !meta.Capabilities.CanIssue || meta.Capabilities.CanManage {
		t.Errorf("capabilities = %+v for invoices:issue, want only canIssue", meta.Capabilities)
	}
	if res := h.SignIn(t, "invoices:create").Do(http.MethodGet, metaPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /meta without invoices:access = %d, want 403", res.Status)
	}
}

// "Today" is the calendar day in Oslo, never UTC's: at 22:30 UTC on 12
// September it is already 00:30 on the 13th there (D1).
func TestMeta_TodayIsTheBusinessDayInOslo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(10*time.Hour + 30*time.Minute)

	if meta := getMeta(t, h, "invoices:access"); meta.Today != "2026-09-13" {
		t.Errorf("today at %s = %q, want 2026-09-13", h.Now().Format(time.RFC3339), meta.Today)
	}
}

// Without an object store nothing can be issued, and meta says so; "anything
// issued" is the counter row, never a count of documents (D2).
func TestMeta_StorageAndTheCounter(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	if meta := getMeta(t, h, "invoices:access"); meta.StorageAvailable {
		t.Error("storageAvailable = true with no object store configured")
	}

	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 2)`)
	if meta := getMeta(t, h, "invoices:access"); !meta.AnythingIssued {
		t.Error("anythingIssued = false with the counter row present")
	}
}
```

**Create** `apps/server/internal/invoices/module_internal_test.go`:

```go
package invoices

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The catalog is what an administrator sees when they build a role, so every
// word of it is pinned (D1): the keys, their display names and descriptions,
// the category, which are sensitive — issuing and the seller record — and that
// all four may be delegated.
func TestPermissions_AreTheCatalogTheDesignNames(t *testing.T) {
	t.Parallel()
	want := []contracts.Permission{
		{
			Key: "invoices:access", Display: "Use Invoices",
			Description: "Use the Invoices app and read every invoice, credit note, PDF and the invoice journal.",
			Category:    "Invoices", Sensitive: false, Delegable: true,
		},
		{
			Key: "invoices:create", Display: "Create invoices",
			Description: "Create, edit and delete invoice drafts, and preview a draft as PDF.",
			Category:    "Invoices", Sensitive: false, Delegable: true,
		},
		{
			Key: "invoices:issue", Display: "Issue invoices",
			Description: "Issue a draft into a numbered document that can never be changed, and create credit notes.",
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
		{
			Key: "invoices:manage", Display: "Manage invoicing",
			Description: "Change the seller record, the number series start, and the VAT codes and their rates.",
			Category:    "Invoices", Sensitive: true, Delegable: true,
		},
	}
	got := Module().Permissions
	if len(got) != len(want) {
		t.Fatalf("permissions = %+v, want the four of D1", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("permission %d = %+v, want %+v", i, got[i], w)
		}
		if err := contracts.ValidatePermission("invoices", got[i]); err != nil {
			t.Errorf("permission %q does not pass the platform's own rules: %v", got[i].Key, err)
		}
	}
}

// Invoices requires customers: mounted without a customer directory it fails
// at composition, naming the dependency, rather than panicking on the first
// request.
func TestMount_RefusesAnInstallationWithoutCustomers(t *testing.T) {
	t.Parallel()
	_, err := Module().Mount(module.Deps{})
	if err == nil || !strings.Contains(err.Error(), "requires the customers module") {
		t.Errorf("Mount without a directory = %v, want the customers requirement named", err)
	}
}
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 ./internal/invoices/ ; cd ../..   # FAIL: the package has no Go files
```

- [ ] **Step 5: The module**

**Create** `apps/server/internal/invoices/module.go`:

```go
// Package invoices is the invoices module: the sales document of Norwegian
// bookkeeping (invoices foundation design, phase 1A). A draft is issued into an
// immutable, numbered document with a buyer snapshot, a seller snapshot and
// VAT per rate, rendered to a PDF that is stored once, correctable only by a
// credit note in the same series, and listed in a journal that proves the
// series has no gaps. It serves openapi/invoices.yaml under /api/v1/invoices/.
//
// It requires customers (config refuses MODULES=invoices without it): the
// buyer, its billing profile and its address come from
// contracts.CustomerDirectory, read before any transaction takes a lock.
// Nothing here imports another module — depguard enforces it, tests included —
// and no SQL of this module crosses a schema.
package invoices

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// permissions is the module's catalog (D1). Every operation requires
// invoices:access, through the grammar permission:invoices:access+invoices:<x>,
// and no built-in role holds any of them: Owner has the wildcard, and everyone
// else is granted invoicing deliberately. Issuing and the seller record are
// sensitive — the one creates bookkeeping material that can never be taken
// back, the other decides what every document says the company is.
var permissions = []contracts.Permission{
	{
		Key: "invoices:access", Display: "Use Invoices",
		Description: "Use the Invoices app and read every invoice, credit note, PDF and the invoice journal.",
		Category:    "Invoices", Sensitive: false, Delegable: true,
	},
	{
		Key: "invoices:create", Display: "Create invoices",
		Description: "Create, edit and delete invoice drafts, and preview a draft as PDF.",
		Category:    "Invoices", Sensitive: false, Delegable: true,
	},
	{
		Key: "invoices:issue", Display: "Issue invoices",
		Description: "Issue a draft into a numbered document that can never be changed, and create credit notes.",
		Category:    "Invoices", Sensitive: true, Delegable: true,
	},
	{
		Key: "invoices:manage", Display: "Manage invoicing",
		Description: "Change the seller record, the number series start, and the VAT codes and their rates.",
		Category:    "Invoices", Sensitive: true, Delegable: true,
	},
}

// Module is invoices as a platform module: its contract mounted under
// /api/v1/invoices/ and its four permissions in the composed catalog.
func Module() module.Module {
	return module.Module{
		Name:        "invoices",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem, when the object
// store cannot be built, and — before any of that — when there is no customer
// directory: config already refuses "invoices" without "customers", so
// reaching here without one is a composition bug, and failing the mount says so
// where a nil directory would only panic on the first request.
func mount(d module.Deps) (http.Handler, error) {
	if d.Directory == nil {
		return nil, errors.New("invoices: no customer directory; the invoices module requires the customers module")
	}
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Catalog: d.Catalog,
	})
	srv, err := newServer(d)
	if err != nil {
		return nil, err
	}
	strict := gen.NewStrictHandlerWithOptions(srv, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
```

**Create** `apps/server/internal/invoices/server.go`:

```go
package invoices

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file.
type server struct {
	deps module.Deps
	// objects is where issued documents' PDFs live (pdfstore.go), scoped to
	// storageScope. No call on it is ever made inside withLockedTx.
	objects storage.ObjectStore
	// storageConfigured is whether objects can store anything at all: an
	// object store a test harness set, or a configured provider. Without one
	// the store answers storage.ErrNotConfigured to everything, and issuing is
	// refused before any number is allocated (D6).
	storageConfigured bool
}

var _ gen.StrictServerInterface = (*server)(nil)

// storageScope is this module's namespace in the object store: a document's
// key documents/<id>/<number>-<sha256>.pdf is physically
// invoices/documents/... (docs/storage.md).
const storageScope = "invoices"

// newServer builds the module's operations over d. It fails only when the
// configured object store cannot be built. An unset provider is not an error:
// the process starts, and issuing and downloading fail closed with a 503 at the
// operation (docs/storage.md), never at startup.
func newServer(d module.Deps) (*server, error) {
	if d.ObjectStore != nil {
		return &server{deps: d, objects: d.ObjectStore, storageConfigured: true}, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("invoices: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("invoices: scope the object store: %w", err)
	}
	return &server{deps: d, objects: scoped, storageConfigured: cfg.StorageProvider != ""}, nil
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}
```

**Create** `apps/server/internal/invoices/values.go`:

```go
package invoices

import (
	"fmt"
	"time"
	// The business time zone is a fixed constant, so the module carries its own
	// copy of it rather than trusting the host's zoneinfo (the runtime image is
	// distroless). cmd/vantigo embeds it too; a second import costs nothing.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// oslo is the business time zone (D1): this is Norwegian bookkeeping, so there
// is no setting. Every "today" of this module is the calendar day of
// Deps.Clock() here, computed in Go — Postgres' CURRENT_DATE is UTC in the
// container and is never used.
var oslo = mustLoadLocation("Europe/Oslo")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("invoices: load %s: %v", name, err))
	}
	return loc
}

// businessDay is the calendar day an instant falls on in Oslo, as the UTC
// midnight every date of this module is compared at — expenses' own
// derivation (entries_validation.go), with the zone fixed.
func businessDay(t time.Time) time.Time {
	t = t.In(oslo)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// pgDate stores a date as the date column wants it.
func pgDate(d time.Time) pgtype.Date { return pgtype.Date{Time: d, Valid: true} }

// wireDate is a date as the contract carries one.
func wireDate(d time.Time) openapi_types.Date { return openapi_types.Date{Time: d} }

// floatFromNumeric reads a NOT NULL numeric column onto the wire as a JSON
// number. Money never goes the other way through a float: amounts enter
// through ratFromFloat's decimal text (money.go).
func floatFromNumeric(n pgtype.Numeric) (float64, error) {
	if !n.Valid {
		return 0, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0, fmt.Errorf("invoices: read a stored decimal: %w", err)
	}
	return f.Float64, nil
}
```

**Create** `apps/server/internal/invoices/seller.go`:

```go
package invoices

import "github.com/vantigo-io/vantigo/server/internal/invoices/store"

// sellerMissingFields are the seller fields issuing still needs (D2), by their
// camelCase wire names, in the order the settings form shows them. § 5-1-2
// requires the name and the organisation number; the address and the bank
// account are a sensible gate, not a legal requirement. Empty means complete.
func sellerMissingFields(row store.InvoicesSetting) []string {
	missing := []string{}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"legalName", row.LegalName},
		{"organisationNumber", row.OrganisationNumber},
		{"addressLine1", row.AddressLine1},
		{"postalCode", row.PostalCode},
		{"city", row.City},
		{"bankAccount", row.BankAccount},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	return missing
}
```

**Create** `apps/server/internal/invoices/meta.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /meta: the one read every Invoices page makes before it
// draws anything (D2). It answers what this installation can do (an object
// store or not), what the seller still lacks, whether the series has started,
// the VAT codes a new line may take today, and what the caller may do — so no
// client re-derives a rule this module owns.

// GetInvoicesMeta Get the Invoices metadata
// (GET /api/v1/invoices/meta)
func (s *server) GetInvoicesMeta(ctx context.Context, _ gen.GetInvoicesMetaRequestObject) (gen.GetInvoicesMetaResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	issued, err := anythingIssued(ctx, q)
	if err != nil {
		return nil, err
	}
	today := businessDay(s.deps.Clock())
	codes, err := q.VatCodesInForce(ctx, pgDate(today))
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes in force: %w", err)
	}
	inForce := make([]gen.InvoicesVatCodeInForce, 0, len(codes))
	for _, c := range codes {
		rate, err := floatFromNumeric(c.RatePercent)
		if err != nil {
			return nil, err
		}
		inForce = append(inForce, gen.InvoicesVatCodeInForce{
			Id: c.ID, Code: c.Code, Name: c.Name, SafTCode: c.SafTCode, EhfCategory: c.EhfCategory,
			ExemptionReason: c.ExemptionReason, RatePercent: rate,
		})
	}
	missing := sellerMissingFields(row)
	return gen.GetInvoicesMeta200JSONResponse(gen.InvoicesMetaResponse{
		Currency:                row.DefaultCurrency,
		DefaultPaymentTermsDays: row.DefaultPaymentTermsDays,
		SellerComplete:          len(missing) == 0,
		MissingSellerFields:     missing,
		AnythingIssued:          issued,
		SeriesStart:             row.SeriesStart,
		StorageAvailable:        s.storageConfigured,
		Today:                   wireDate(today),
		VatCodes:                inForce,
		Capabilities: gen.InvoicesMetaCapabilities{
			CanCreate: s.has(ctx, "invoices:create"),
			CanIssue:  s.has(ctx, "invoices:issue"),
			CanManage: s.has(ctx, "invoices:manage"),
		},
	}), nil
}

// anythingIssued is "the counter row exists" (D2), never a count of documents.
func anythingIssued(ctx context.Context, q *store.Queries) (bool, error) {
	_, err := q.CounterNextValue(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: read the document counter: %w", err)
	}
	return true, nil
}
```

- [ ] **Step 6: Wire it into the platform: the binary, MODULES and depguard**

**Replace** in `apps/server/cmd/vantigo/main.go`:

```go
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/management"
	"github.com/vantigo-io/vantigo/server/internal/module"
```

**with**:

```go
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/management"
	"github.com/vantigo-io/vantigo/server/internal/module"
```

**Replace** in `apps/server/cmd/vantigo/main.go`:

```go
		projects.Module(),
		timetracking.Module(),
		expenses.Module(),
	}
}

```

**with**:

```go
		projects.Module(),
		timetracking.Module(),
		expenses.Module(),
		invoices.Module(),
	}
}

```

**Replace** in `apps/server/internal/config/config.go`:

```go
// set. energy depends on customers, and so do communications and projects:
// enabling any of them without customers is its own problem, naming both.
// time depends on projects the same way (it reads contracts.ProjectDirectory,
// which only projects provides). Identity is always mounted and is never
// listed here.
func modules(p *problems, env map[string]string) []string {
	v := env["MODULES"]
```

**with**:

```go
// set. energy depends on customers, and so do communications and projects:
// enabling any of them without customers is its own problem, naming both.
// time depends on projects the same way (it reads contracts.ProjectDirectory,
// which only projects provides), and invoices on customers (its buyer comes
// from contracts.CustomerDirectory). Identity is always mounted and is never
// listed here.
func modules(p *problems, env map[string]string) []string {
	v := env["MODULES"]
```

**Replace** in `apps/server/internal/config/config.go`:

```go
	}
	if slices.Contains(out, "time") && !slices.Contains(out, "projects") {
		p.add("MODULES", "time requires projects")
	}

	return out
```

**with**:

```go
	}
	if slices.Contains(out, "time") && !slices.Contains(out, "projects") {
		p.add("MODULES", "time requires projects")
	}
	if slices.Contains(out, "invoices") && !slices.Contains(out, "customers") {
		p.add("MODULES", "invoices requires customers")
	}

	return out
```

Every business module's depguard rule set gains the two `internal/invoices` deny entries, the platform's too, and invoices gets its own rule set denying every other module. **Run**, from the repository root:

```bash
python3 - <<'PY'
import re
p = 'apps/server/.golangci.yml'
s = open(p).read()
pat = re.compile(r'(            - pkg: github.com/vantigo-io/vantigo/server/internal/expenses/\n              desc: "([^"]*)"\n)')
count = 0
def add(m):
    global count
    count += 1
    d = m.group(2)
    return (m.group(1)
            + f'            - pkg: github.com/vantigo-io/vantigo/server/internal/invoices$\n              desc: "{d}"\n'
            + f'            - pkg: github.com/vantigo-io/vantigo/server/internal/invoices/\n              desc: "{d}"\n')
s = pat.sub(add, s)
assert count == 8, count  # identity, customers, products, energy, communications, projects, time, platform
anchor = ('            - pkg: github.com/vantigo-io/vantigo/server/internal/time/\n'
          '              desc: "internal/expenses may not import another business module"\n')
assert s.count(anchor) == 1
block = '        invoices:\n          files:\n            - "**/internal/invoices/**"\n          deny:\n'
for m in ['identity', 'customers', 'products', 'energy', 'communications', 'projects', 'time', 'expenses']:
    for suffix in ['$', '/']:
        block += (f'            - pkg: github.com/vantigo-io/vantigo/server/internal/{m}{suffix}\n'
                  '              desc: "internal/invoices may not import another business module"\n')
s = s.replace(anchor, anchor
              + '            - pkg: github.com/vantigo-io/vantigo/server/internal/invoices$\n'
              + '              desc: "internal/expenses may not import another business module"\n'
              + '            - pkg: github.com/vantigo-io/vantigo/server/internal/invoices/\n'
              + '              desc: "internal/expenses may not import another business module"\n'
              + block)
open(p, 'w').write(s)
PY
grep -c 'internal/invoices' apps/server/.golangci.yml   # 35
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go build ./... && mise exec -- go vet ./...
mise exec -- go test -count=1 ./internal/invoices/ ./internal/config/ ./internal/module/ ./internal/openapi/... ./cmd/...
mise exec -- go test -count=1 -run 'TestInvoices|TestSqlcSchema|TestNoModuleReferences' ./internal/db/
mise exec -- golangci-lint run ./...   # 0 issues.
cd ../..
```

- [ ] **Step 7: The frontend package skeleton**

Eight files are the expenses package's own, byte for byte (the tsconfig trio, `.gitignore`, and the test helpers — `setup.ts` imports `setAuthStateClearer`/`setUnauthorizedHandler` from `../api/request`, which this package's `request.ts` exports too). **Run**, from the repository root:

```bash
mkdir -p apps/invoices/frontend/src/test
for f in .gitignore tsconfig.json tsconfig.app.json tsconfig.node.json src/test/api.ts src/test/fetch.ts src/test/render.tsx src/test/setup.ts; do
  cp "apps/expenses/frontend/$f" "apps/invoices/frontend/$f"
done
```

**Create** `apps/invoices/frontend/package.json`:

```json
{
  "name": "@vantigo/invoices-ui",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "lint": "eslint .",
    "test": "vitest run",
    "typecheck": "tsc -b --noEmit"
  },
  "dependencies": {
    "@mantine/core": "catalog:",
    "@mantine/dates": "catalog:",
    "@mantine/form": "catalog:",
    "@mantine/hooks": "catalog:",
    "@mantine/modals": "catalog:",
    "@mantine/notifications": "catalog:",
    "@tabler/icons-react": "catalog:",
    "@tanstack/react-query": "catalog:",
    "@tanstack/react-router": "catalog:",
    "@vantigo/frontend-api-client": "workspace:*",
    "@vantigo/frontend-shell": "workspace:*",
    "dayjs": "catalog:",
    "react": "catalog:",
    "react-dom": "catalog:"
  },
  "devDependencies": {
    "@eslint/js": "catalog:",
    "@testing-library/jest-dom": "catalog:",
    "@testing-library/react": "catalog:",
    "@testing-library/user-event": "catalog:",
    "@types/node": "catalog:",
    "@types/react": "catalog:",
    "@types/react-dom": "catalog:",
    "eslint": "catalog:",
    "eslint-plugin-react-hooks": "catalog:",
    "eslint-plugin-react-refresh": "catalog:",
    "globals": "catalog:",
    "jsdom": "catalog:",
    "typescript": "catalog:",
    "typescript-eslint": "catalog:",
    "vite": "catalog:",
    "vitest": "catalog:"
  },
  "exports": {
    ".": "./src/index.ts",
    "./i18n": "./src/i18n.ts",
    "./api/*": "./src/api/*.ts",
    "./lib/*": "./src/lib/*.ts",
    "./pages/*": "./src/pages/*.tsx",
    "./components/*": "./src/components/*.tsx"
  }
}
```

**Create** `apps/invoices/frontend/eslint.config.js`:

```js
import js from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

const forbiddenModuleImports = [
  "@vantigo/customers-ui",
  "@vantigo/customers-ui/**",
  "@vantigo/expenses-ui",
  "@vantigo/expenses-ui/**",
  "@vantigo/communications-ui",
  "@vantigo/communications-ui/**",
  "@vantigo/energy-ui",
  "@vantigo/energy-ui/**",
  "@vantigo/products-ui",
  "@vantigo/products-ui/**",
  "@vantigo/projects-ui",
  "@vantigo/projects-ui/**",
  "@vantigo/time-ui",
  "@vantigo/time-ui/**",
  "@vantigo/app",
  "@vantigo/app/**",
  "../../../customers/**",
  "../../../expenses/**",
  "../../../communications/**",
  "../../../energy/**",
  "../../../products/**",
  "../../../projects/**",
  "../../../time/**",
  "../../../host/**",
];

export default defineConfig([
  globalIgnores(["dist"]),
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: forbiddenModuleImports,
              message: "Module frontends must not import other module frontends or the host app.",
            },
          ],
        },
      ],
    },
  },
]);
```

**Create** `apps/invoices/frontend/vite.config.ts`:

```ts
/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: the invoice editor mounts Mantine's modals, date inputs and a
// line table driven by a dozen inputs a test; on a four-core CI runner that has
// crossed Vitest's five-second default under load elsewhere in the repo. The
// limit exists to catch a hang, not to race the runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
    // "Today" is the server's Oslo business day (GET /meta), never the
    // browser's. Pinning the suite to a zone that is neither UTC nor Oslo means
    // a test that quietly read the browser's date would fail here.
    env: { TZ: "America/New_York" },
  },
});
```

**Create** `apps/invoices/frontend/src/api/request.ts`:

```ts
import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

export const { request } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/**
 * The host's session handling, kept here as well as handed to the client.
 *
 * Two requests in this package cannot go through `request` — the PDF download
 * and the draft preview, whose bodies are files — and an expired session on
 * either must still sign the person out rather than show them a raw problem.
 * Holding the two callbacks lets `handleUnauthorized` do exactly what the
 * client does.
 */
let onUnauthorized: (() => void | Promise<void>) | undefined;
let clearAuthState: (() => void | Promise<void>) | undefined;

export const setUnauthorizedHandler = (handler: (() => void | Promise<void>) | undefined) => {
  onUnauthorized = handler;
  client.setUnauthorizedHandler(handler);
};

export const setAuthStateClearer = (clearer: (() => void | Promise<void>) | undefined) => {
  clearAuthState = clearer;
  client.setAuthStateClearer(clearer);
};

/** What the shared client does on a 401: clear the session, then tell the host. */
export const handleUnauthorized = async (): Promise<void> => {
  try {
    await clearAuthState?.();
  } finally {
    await onUnauthorized?.();
  }
};

/** Every write in this package sends JSON and carries the session cookie. */
export const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so the blanket
 * invalidation each write does reaches all of them.
 */
export const INVOICES_QUERY_KEY = "invoices";
```

**Create** `apps/invoices/frontend/src/api/meta.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * What every Invoices page needs before it draws anything (D2), answered by
 * the server rather than re-derived here: the currency, the default terms,
 * what the seller still lacks, whether the series has started, whether the
 * installation has an object store, today in Oslo, the VAT codes a new line may
 * take today and the caller's three capabilities.
 */
export type InvoicesMeta = Schemas["InvoicesMetaResponse"];
export type VatCodeInForce = Schemas["InvoicesVatCodeInForce"];

export const invoicesMetaQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<InvoicesMeta>("/api/v1/invoices/meta", { signal }),
  });
```

**Create** `apps/invoices/frontend/src/i18n.ts`:

```ts
import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

export const invoicesCatalog = {
  en: {
    invoices: "Invoices",
    invoicesDescription: "Invoices and credit notes: drafts, and the numbered documents they are issued into.",
    failedToLoadMeta: "Could not load Invoices",
    noInvoices: "No invoices yet",
    noInvoicesDescription: "A draft becomes a numbered invoice the moment it is issued.",
  },
  nb: {
    invoices: "Fakturaer",
    invoicesDescription: "Fakturaer og kreditnotaer: utkast, og de nummererte dokumentene de utstedes som.",
    failedToLoadMeta: "Kunne ikke laste Fakturaer",
    noInvoices: "Ingen fakturaer ennå",
    noInvoicesDescription: "Et utkast blir en nummerert faktura i det øyeblikket det utstedes.",
  },
} satisfies CatalogResources;

registerCatalog("invoices", invoicesCatalog);
```

**Create** `apps/invoices/frontend/src/pages/invoices.tsx`:

```tsx
import { Alert, Stack } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { invoicesMetaQueryOptions } from "../api/meta";
import "../i18n";

/**
 * The Invoices app's home. This delivery's first task mounts the app and
 * nothing more: the list, the editor, settings and the journal come with the
 * operations behind them.
 */
export const InvoicesPage = () => {
  const { t } = useI18n("invoices");
  const meta = useQuery(invoicesMetaQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader title={t("invoices")} description={t("invoicesDescription")} />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {meta.error.message}
        </Alert>
      )}
      {meta.isPending && <ContentSkeleton rows={3} rowHeight={52} />}
      {meta.data && <EmptyState title={t("noInvoices")} description={t("noInvoicesDescription")} />}
    </Stack>
  );
};
```

**Create** `apps/invoices/frontend/src/index.ts`:

```ts
import "./i18n";

export { type InvoicesMeta, invoicesMetaQueryOptions, type VatCodeInForce } from "./api/meta";
export { invoicesCatalog } from "./i18n";
export { InvoicesPage } from "./pages/invoices";
```

**Create** `apps/invoices/frontend/src/pages/invoices.test.tsx`:

```tsx
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { InvoicesMeta } from "../api/meta";
import { jsonResponse, problemResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { InvoicesPage } from "./invoices";

/** GET /meta for a fresh installation, as the server sends it — a wire literal. */
const freshMeta: InvoicesMeta = {
  currency: "NOK",
  defaultPaymentTermsDays: 14,
  sellerComplete: false,
  missingSellerFields: ["legalName", "organisationNumber", "addressLine1", "postalCode", "city", "bankAccount"],
  anythingIssued: false,
  seriesStart: 1,
  storageAvailable: true,
  today: "2026-09-26",
  vatCodes: [{ id: 1, code: "3", name: "Utgående mva 25 %", safTCode: "3", ehfCategory: "S", ratePercent: 25 }],
  capabilities: { canCreate: false, canIssue: false, canManage: false },
};

describe("the Invoices app's home", () => {
  it("mounts, reads meta and says there is nothing yet", async () => {
    const fetchMock = stubFetch((input: RequestInfo | URL) =>
      String(input) === "/api/v1/invoices/meta" ? jsonResponse(200, freshMeta) : new Response(null, { status: 404 }),
    );

    renderWithProviders(<InvoicesPage />);

    expect(await screen.findByText("No invoices yet")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url) === "/api/v1/invoices/meta")).toBe(true);
  });

  it("says when meta could not be loaded", async () => {
    stubFetch(() => problemResponse(500, "Internal Server Error"));

    renderWithProviders(<InvoicesPage />);

    expect(await screen.findByText("Could not load Invoices")).toBeInTheDocument();
  });
});
```

**Replace** in `tools/openapi/gen-client.ts`:

```ts
  { module: "projects", output: "apps/projects/frontend/src/api-schema.d.ts" },
  { module: "time", output: "apps/time/frontend/src/api-schema.d.ts" },
  { module: "expenses", output: "apps/expenses/frontend/src/api-schema.d.ts" },
] as const;

const header =
```

**with**:

```ts
  { module: "projects", output: "apps/projects/frontend/src/api-schema.d.ts" },
  { module: "time", output: "apps/time/frontend/src/api-schema.d.ts" },
  { module: "expenses", output: "apps/expenses/frontend/src/api-schema.d.ts" },
  { module: "invoices", output: "apps/invoices/frontend/src/api-schema.d.ts" },
] as const;

const header =
```

**Replace** in `tools/openapi/gen-client.test.ts`:

```ts
      projects: "apps/projects/frontend/src/api-schema.d.ts",
      time: "apps/time/frontend/src/api-schema.d.ts",
      expenses: "apps/expenses/frontend/src/api-schema.d.ts",
    });
  });

```

**with**:

```ts
      projects: "apps/projects/frontend/src/api-schema.d.ts",
      time: "apps/time/frontend/src/api-schema.d.ts",
      expenses: "apps/expenses/frontend/src/api-schema.d.ts",
      invoices: "apps/invoices/frontend/src/api-schema.d.ts",
    });
  });

```

**Replace** in `tools/openapi/gen-client.test.ts`:

```ts
    const root = await mkdtemp(join(tmpdir(), "gen-client-"));
    roots.push(root);
    const written = await generateClients(root);
    expect(written).toHaveLength(8);
    const customers = await readFile(join(root, "apps/customers/frontend/src/api-schema.d.ts"), "utf8");
    expect(customers).toContain("export interface paths");
    expect(customers).toContain('"/api/v1/customers"');
```

**with**:

```ts
    const root = await mkdtemp(join(tmpdir(), "gen-client-"));
    roots.push(root);
    const written = await generateClients(root);
    expect(written).toHaveLength(9);
    const customers = await readFile(join(root, "apps/customers/frontend/src/api-schema.d.ts"), "utf8");
    expect(customers).toContain("export interface paths");
    expect(customers).toContain('"/api/v1/customers"');
```

Every other module package's `no-restricted-imports` list gains the new package. **Run**, from the repository root:

```bash
for m in communications customers energy expenses products projects time; do
python3 - "apps/$m/frontend/eslint.config.js" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
a, b = '  "@vantigo/app",\n', '  "../../../host/**",\n'
assert s.count(a) == 1 and s.count(b) == 1, p
s = s.replace(a, '  "@vantigo/invoices-ui",\n  "@vantigo/invoices-ui/**",\n' + a).replace(b, '  "../../../invoices/**",\n' + b)
open(p, 'w').write(s)
PY
done
mise exec -- bun install
mise exec -- bun run gen:client   # writes apps/invoices/frontend/src/api-schema.d.ts
```

- [ ] **Step 8: Mount the app in the host**

The module key, the app (after Expenses in the switcher), its layout route and first page, the i18n import, the navigation label, and one admin catalog entry per permission key in both languages — the English pinned to the server's own words.

**Replace** in `apps/host/frontend/package.json`:

```json
    "@vantigo/expenses-ui": "workspace:*",
    "@vantigo/frontend-api-client": "workspace:*",
    "@vantigo/frontend-shell": "workspace:*",
    "@vantigo/products-ui": "workspace:*",
    "@vantigo/projects-ui": "workspace:*",
    "@vantigo/time-ui": "workspace:*",
```

**with**:

```json
    "@vantigo/expenses-ui": "workspace:*",
    "@vantigo/frontend-api-client": "workspace:*",
    "@vantigo/frontend-shell": "workspace:*",
    "@vantigo/invoices-ui": "workspace:*",
    "@vantigo/products-ui": "workspace:*",
    "@vantigo/projects-ui": "workspace:*",
    "@vantigo/time-ui": "workspace:*",
```

**Replace** in `apps/host/frontend/src/navigation.ts`:

```ts
  "customers",
  "energy",
  "expenses",
  "products",
  "projects",
  "time",
```

**with**:

```ts
  "customers",
  "energy",
  "expenses",
  "invoices",
  "products",
  "projects",
  "time",
```

**Replace** in `apps/host/frontend/src/apps.ts`:

```ts
  IconCategory,
  IconChecklist,
  IconClock,
  IconFlag,
  IconInbox,
  IconLayoutDashboard,
```

**with**:

```ts
  IconCategory,
  IconChecklist,
  IconClock,
  IconFileInvoice,
  IconFlag,
  IconInbox,
  IconLayoutDashboard,
```

**Replace** in `apps/host/frontend/src/apps.ts`:

```ts
      requiredPermissions: ["expenses:manage"],
    },
  ]),
  moduleApp("communications", "navigation.communications", IconInbox, "/communications", [
    {
      label: "navigation.inbox",
```

**with**:

```ts
      requiredPermissions: ["expenses:manage"],
    },
  ]),
  moduleApp("invoices", "navigation.invoices", IconFileInvoice, "/invoices", [
    {
      // Invoicing is a finance job, not per-customer (invoices foundation
      // design D1): invoices:access reads every document, so the list is the
      // whole app's entry.
      label: "navigation.invoices",
      to: "/invoices",
      icon: IconFileInvoice,
      requiredPermissions: ["invoices:access"],
    },
  ]),
  moduleApp("communications", "navigation.communications", IconInbox, "/communications", [
    {
      label: "navigation.inbox",
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
  switcherTiles,
} from "./apps";

const allModules = ["communications", "customers", "energy", "expenses", "products", "projects", "time"] as const;

describe("the app registry", () => {
  it("lists Home first, without a module, and every module app once", () => {
```

**with**:

```ts
  switcherTiles,
} from "./apps";

const allModules = [
  "communications",
  "customers",
  "energy",
  "expenses",
  "invoices",
  "products",
  "projects",
  "time",
] as const;

describe("the app registry", () => {
  it("lists Home first, without a module, and every module app once", () => {
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
      "home",
      "customers",
      "projects",
      "time",
      "expenses",
      "communications",
      "products",
      "energy",
    ]);
    for (const app of apps.slice(1)) expect(app.module).toBe(app.key);
```

**with**:

```ts
      "home",
      "customers",
      "projects",
      "time",
      "expenses",
      "invoices",
      "communications",
      "products",
      "energy",
    ]);
    for (const app of apps.slice(1)) expect(app.module).toBe(app.key);
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
      "/expenses/approvals",
      "/expenses/reimbursements",
      "/expenses/settings",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
```

**with**:

```ts
      "/expenses/approvals",
      "/expenses/reimbursements",
      "/expenses/settings",
      "/invoices",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
      "projects",
      "time",
      "expenses",
      "communications",
      "products",
      "energy",
```

**with**:

```ts
      "projects",
      "time",
      "expenses",
      "invoices",
      "communications",
      "products",
      "energy",
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
      ["projects", false],
      ["time", false],
      ["expenses", false],
      ["communications", false],
      ["products", false],
      ["energy", false],
```

**with**:

```ts
      ["projects", false],
      ["time", false],
      ["expenses", false],
      ["invoices", false],
      ["communications", false],
      ["products", false],
      ["energy", false],
```

**Replace** in `apps/host/frontend/src/i18n.ts`:

```ts
import "@vantigo/communications-ui/i18n";
import "@vantigo/customers-ui/i18n";
import "@vantigo/energy-ui/i18n";
import "@vantigo/products-ui/i18n";
import "@vantigo/projects-ui/i18n";
import "@vantigo/time-ui/i18n";
```

**with**:

```ts
import "@vantigo/communications-ui/i18n";
import "@vantigo/customers-ui/i18n";
import "@vantigo/energy-ui/i18n";
import "@vantigo/invoices-ui/i18n";
import "@vantigo/products-ui/i18n";
import "@vantigo/projects-ui/i18n";
import "@vantigo/time-ui/i18n";
```

**Replace** in `apps/host/frontend/src/catalogs/navigation.ts`:

```ts
  "navigation.expenseApprovals": "Expense approvals",
  "navigation.reimbursements": "Reimbursements",
  "navigation.expensesSettings": "Expense settings",
  "navigation.energy": "Energy",
  "navigation.meteringPoints": "Metering points",
  "navigation.meteringPoint": "Metering point",
```

**with**:

```ts
  "navigation.expenseApprovals": "Expense approvals",
  "navigation.reimbursements": "Reimbursements",
  "navigation.expensesSettings": "Expense settings",
  "navigation.invoices": "Invoices",
  "navigation.energy": "Energy",
  "navigation.meteringPoints": "Metering points",
  "navigation.meteringPoint": "Metering point",
```

**Replace** in `apps/host/frontend/src/catalogs/navigation.ts`:

```ts
  "navigation.expenseApprovals": "Godkjenning av utlegg",
  "navigation.reimbursements": "Refusjoner",
  "navigation.expensesSettings": "Utleggsinnstillinger",
  "navigation.energy": "Energi",
  "navigation.meteringPoints": "Målepunkter",
  "navigation.meteringPoint": "Målepunkt",
```

**with**:

```ts
  "navigation.expenseApprovals": "Godkjenning av utlegg",
  "navigation.reimbursements": "Refusjoner",
  "navigation.expensesSettings": "Utleggsinnstillinger",
  "navigation.invoices": "Fakturaer",
  "navigation.energy": "Energi",
  "navigation.meteringPoints": "Målepunkter",
  "navigation.meteringPoint": "Målepunkt",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
    displayNameKey: "admin.permission.expensesManage",
    descriptionKey: "admin.permission.expensesManageDescription",
  },
} as const;

export const getHostPermissionTranslation = (permissionKey: string) =>
```

**with**:

```ts
    displayNameKey: "admin.permission.expensesManage",
    descriptionKey: "admin.permission.expensesManageDescription",
  },
  "invoices:access": {
    moduleKey: "admin.permission.module.invoices",
    categoryKey: "admin.permission.category.invoices",
    displayNameKey: "admin.permission.invoicesAccess",
    descriptionKey: "admin.permission.invoicesAccessDescription",
  },
  "invoices:create": {
    moduleKey: "admin.permission.module.invoices",
    categoryKey: "admin.permission.category.invoices",
    displayNameKey: "admin.permission.invoicesCreate",
    descriptionKey: "admin.permission.invoicesCreateDescription",
  },
  "invoices:issue": {
    moduleKey: "admin.permission.module.invoices",
    categoryKey: "admin.permission.category.invoices",
    displayNameKey: "admin.permission.invoicesIssue",
    descriptionKey: "admin.permission.invoicesIssueDescription",
  },
  "invoices:manage": {
    moduleKey: "admin.permission.module.invoices",
    categoryKey: "admin.permission.category.invoices",
    displayNameKey: "admin.permission.invoicesManage",
    descriptionKey: "admin.permission.invoicesManageDescription",
  },
} as const;

export const getHostPermissionTranslation = (permissionKey: string) =>
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
  "admin.permission.module.projects": "Projects",
  "admin.permission.module.time": "Time",
  "admin.permission.module.expenses": "Expenses",
  "admin.permission.category.administration": "Administration",
  "admin.permission.category.customers": "Customers",
  "admin.permission.category.legalIdentity": "Legal identity",
```

**with**:

```ts
  "admin.permission.module.projects": "Projects",
  "admin.permission.module.time": "Time",
  "admin.permission.module.expenses": "Expenses",
  "admin.permission.module.invoices": "Invoices",
  "admin.permission.category.administration": "Administration",
  "admin.permission.category.customers": "Customers",
  "admin.permission.category.legalIdentity": "Legal identity",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
  "admin.permission.category.projects": "Projects",
  "admin.permission.category.time": "Time",
  "admin.permission.category.expenses": "Expenses",
  "admin.permission.identityManage": "Manage identity",
  "admin.permission.identityManageDescription": "Manage accounts, roles, and access.",
  "admin.permission.customersView": "View customers",
```

**with**:

```ts
  "admin.permission.category.projects": "Projects",
  "admin.permission.category.time": "Time",
  "admin.permission.category.expenses": "Expenses",
  "admin.permission.category.invoices": "Invoices",
  "admin.permission.identityManage": "Manage identity",
  "admin.permission.identityManageDescription": "Manage accounts, roles, and access.",
  "admin.permission.customersView": "View customers",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
  "admin.permission.expensesManage": "Manage expenses",
  "admin.permission.expensesManageDescription":
    "Change expense settings, rates and categories, record expenses for a colleague, mark expenses reimbursed, and work past the period lock.",
  "admin.accountExists": "An account already exists for this email address.",
  "admin.filterUsers": "Filter users by {{label}}",
  "admin.searchUsers": "Search users",
```

**with**:

```ts
  "admin.permission.expensesManage": "Manage expenses",
  "admin.permission.expensesManageDescription":
    "Change expense settings, rates and categories, record expenses for a colleague, mark expenses reimbursed, and work past the period lock.",
  "admin.permission.invoicesAccess": "Use Invoices",
  "admin.permission.invoicesAccessDescription":
    "Use the Invoices app and read every invoice, credit note, PDF and the invoice journal.",
  "admin.permission.invoicesCreate": "Create invoices",
  "admin.permission.invoicesCreateDescription": "Create, edit and delete invoice drafts, and preview a draft as PDF.",
  "admin.permission.invoicesIssue": "Issue invoices",
  "admin.permission.invoicesIssueDescription":
    "Issue a draft into a numbered document that can never be changed, and create credit notes.",
  "admin.permission.invoicesManage": "Manage invoicing",
  "admin.permission.invoicesManageDescription":
    "Change the seller record, the number series start, and the VAT codes and their rates.",
  "admin.accountExists": "An account already exists for this email address.",
  "admin.filterUsers": "Filter users by {{label}}",
  "admin.searchUsers": "Search users",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
  "admin.permission.module.projects": "Prosjekter",
  "admin.permission.module.time": "Timer",
  "admin.permission.module.expenses": "Utlegg",
  "admin.permission.category.administration": "Administrasjon",
  "admin.permission.category.customers": "Kunder",
  "admin.permission.category.legalIdentity": "Juridisk identitet",
```

**with**:

```ts
  "admin.permission.module.projects": "Prosjekter",
  "admin.permission.module.time": "Timer",
  "admin.permission.module.expenses": "Utlegg",
  "admin.permission.module.invoices": "Fakturaer",
  "admin.permission.category.administration": "Administrasjon",
  "admin.permission.category.customers": "Kunder",
  "admin.permission.category.legalIdentity": "Juridisk identitet",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
  "admin.permission.category.projects": "Prosjekter",
  "admin.permission.category.time": "Timer",
  "admin.permission.category.expenses": "Utlegg",
  "admin.permission.identityManage": "Administrer identitet",
  "admin.permission.identityManageDescription": "Administrer kontoer, roller og tilgang.",
  "admin.permission.customersView": "Se kunder",
```

**with**:

```ts
  "admin.permission.category.projects": "Prosjekter",
  "admin.permission.category.time": "Timer",
  "admin.permission.category.expenses": "Utlegg",
  "admin.permission.category.invoices": "Fakturaer",
  "admin.permission.identityManage": "Administrer identitet",
  "admin.permission.identityManageDescription": "Administrer kontoer, roller og tilgang.",
  "admin.permission.customersView": "Se kunder",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.ts`:

```ts
  "admin.permission.expensesManage": "Administrer utlegg",
  "admin.permission.expensesManageDescription":
    "Endre utleggsinnstillinger, satser og kategorier, før utlegg for en kollega, merk utlegg som refundert, og arbeid forbi periodelåsen.",
  "admin.accountExists": "Det finnes allerede en konto for denne e-postadressen.",
  "admin.filterUsers": "Filtrer brukere etter {{label}}",
  "admin.searchUsers": "Søk etter brukere",
```

**with**:

```ts
  "admin.permission.expensesManage": "Administrer utlegg",
  "admin.permission.expensesManageDescription":
    "Endre utleggsinnstillinger, satser og kategorier, før utlegg for en kollega, merk utlegg som refundert, og arbeid forbi periodelåsen.",
  "admin.permission.invoicesAccess": "Bruke Fakturaer",
  "admin.permission.invoicesAccessDescription":
    "Bruke Fakturaer-appen og lese alle fakturaer, kreditnotaer, PDF-er og fakturajournalen.",
  "admin.permission.invoicesCreate": "Lage fakturaer",
  "admin.permission.invoicesCreateDescription":
    "Lage, endre og slette fakturautkast, og forhåndsvise et utkast som PDF.",
  "admin.permission.invoicesIssue": "Utstede fakturaer",
  "admin.permission.invoicesIssueDescription":
    "Utstede et utkast som et nummerert dokument som aldri kan endres, og lage kreditnotaer.",
  "admin.permission.invoicesManage": "Administrere fakturering",
  "admin.permission.invoicesManageDescription":
    "Endre selgeropplysningene, startnummeret for nummerserien, og mva-kodene og satsene deres.",
  "admin.accountExists": "Det finnes allerede en konto for denne e-postadressen.",
  "admin.filterUsers": "Filtrer brukere etter {{label}}",
  "admin.searchUsers": "Søk etter brukere",
```

**Replace** in `apps/host/frontend/src/catalogs/admin.test.ts`:

```ts
// cannot read the server's permission catalog from a unit test.
const expensesPermissionKeys = ["expenses:access", "expenses:approve", "expenses:view-all", "expenses:manage"] as const;

describe("the admin permission catalog", () => {
  it("has a display name and a description, in English and Norwegian, for these six projects permissions", () => {
    for (const key of projectsPermissionKeys) {
```

**with**:

```ts
// cannot read the server's permission catalog from a unit test.
const expensesPermissionKeys = ["expenses:access", "expenses:approve", "expenses:view-all", "expenses:manage"] as const;

// Hand-kept in step with `apps/server/internal/invoices/module.go` (`var
// permissions`), for the same reason.
const invoicesPermissionKeys = ["invoices:access", "invoices:create", "invoices:issue", "invoices:manage"] as const;

describe("the admin permission catalog", () => {
  it("has a display name and a description, in English and Norwegian, for these six projects permissions", () => {
    for (const key of projectsPermissionKeys) {
```

**Replace** in `apps/host/frontend/src/catalogs/admin.test.ts`:

```ts
        expect(catalog[translation.descriptionKey], `${key} description (${lng})`).toBeTruthy();
      }
    }
  });

  // The English text is pinned verbatim against the server's own strings
```

**with**:

```ts
        expect(catalog[translation.descriptionKey], `${key} description (${lng})`).toBeTruthy();
      }
    }
  });

  it("has a display name and a description, in English and Norwegian, for the four invoices permissions", () => {
    for (const key of invoicesPermissionKeys) {
      const translation = hostPermissionTranslationKeys[key as keyof typeof hostPermissionTranslationKeys];
      expect(translation, `no catalog entry for ${key}`).toBeDefined();
      for (const lng of ["en", "nb"] as const) {
        const catalog = adminCatalog[lng] as Record<string, string>;
        expect(catalog[translation.displayNameKey], `${key} display name (${lng})`).toBeTruthy();
        expect(catalog[translation.descriptionKey], `${key} description (${lng})`).toBeTruthy();
      }
    }
  });

  it("matches the server's exact English display names and descriptions for invoices permissions", () => {
    const en = adminCatalog.en as Record<string, string>;
    expect(en["admin.permission.invoicesAccess"]).toBe("Use Invoices");
    expect(en["admin.permission.invoicesAccessDescription"]).toBe(
      "Use the Invoices app and read every invoice, credit note, PDF and the invoice journal.",
    );
    expect(en["admin.permission.invoicesCreate"]).toBe("Create invoices");
    expect(en["admin.permission.invoicesCreateDescription"]).toBe(
      "Create, edit and delete invoice drafts, and preview a draft as PDF.",
    );
    expect(en["admin.permission.invoicesIssue"]).toBe("Issue invoices");
    expect(en["admin.permission.invoicesIssueDescription"]).toBe(
      "Issue a draft into a numbered document that can never be changed, and create credit notes.",
    );
    expect(en["admin.permission.invoicesManage"]).toBe("Manage invoicing");
    expect(en["admin.permission.invoicesManageDescription"]).toBe(
      "Change the seller record, the number series start, and the VAT codes and their rates.",
    );
  });

  // The English text is pinned verbatim against the server's own strings
```

**Create** `apps/host/frontend/src/routes/invoices.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { appLayoutOptions } from "./-app-layout";

export const Route = createFileRoute("/invoices")(appLayoutOptions("invoices"));
```

**Create** `apps/host/frontend/src/routes/invoices/index.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { InvoicesPage } from "@vantigo/invoices-ui/pages/invoices";

export const Route = createFileRoute("/invoices/")({
  component: InvoicesPage,
});
```

**Run**, from the repository root (`bun install` links the host to the package; the host's test run regenerates `routeTree.gen.ts` through the router plugin):

```bash
mise exec -- bun install
mise exec -- bun run --cwd apps/host/frontend test
```

- [ ] **Step 9: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bunx biome check --write apps/invoices/frontend apps/host/frontend/src tools/openapi
```

```bash
mise exec -- bun run --cwd apps/invoices/frontend typecheck && mise exec -- bun run --cwd apps/invoices/frontend lint && mise exec -- bun run --cwd apps/invoices/frontend test
mise exec -- bun run --cwd apps/host/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend lint && mise exec -- bun run --cwd apps/host/frontend test
for m in communications customers energy expenses products projects time; do mise exec -- bun run --cwd apps/$m/frontend lint || echo "LINT FAILED: $m"; done
mise exec -- bun run translations:check && mise exec -- bun run gen:client:test
git diff --stat -- openapi/COVERAGE.md   # the invoices section, one operation
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task1.txt <<'MSG'
feat(invoices): a new module — the schema, GET /meta and an empty app

The Invoices module (invoices foundation design D1): internal/invoices with its
four permissions, mounted from openapi/invoices.yaml, requiring customers
(MODULES refuses it without), in businessModules, openapi.Modules, depguard,
moduleSchemas and the default module set. Migration 00034 creates the whole
phase-1A schema at once — the settings row, the counter, the VAT codes and
their dated rate periods with the exclusion, documents, lines and VAT
summaries with their CHECKs, and the triggers that make an issued document
immutable in SQL too (D9) — and seeds the nine SAF-T output codes, 6 as E
and 7 as O. GET /meta answers what every page needs; RequireCoverage gates
the contract. @vantigo/invoices-ui is the app's package, mounted by the host
with its permission labels in en and nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/communications/frontend/eslint.config.js' 'apps/customers/frontend/eslint.config.js' 'apps/energy/frontend/eslint.config.js' 'apps/expenses/frontend/eslint.config.js' 'apps/host/frontend/package.json' 'apps/host/frontend/src/apps.test.ts' 'apps/host/frontend/src/apps.ts' 'apps/host/frontend/src/catalogs/admin.test.ts' 'apps/host/frontend/src/catalogs/admin.ts' 'apps/host/frontend/src/catalogs/navigation.ts' 'apps/host/frontend/src/i18n.ts' 'apps/host/frontend/src/navigation.ts' 'apps/host/frontend/src/routeTree.gen.ts' 'apps/host/frontend/src/routes/invoices.tsx' 'apps/host/frontend/src/routes/invoices/index.tsx' 'apps/invoices/frontend/.gitignore' 'apps/invoices/frontend/eslint.config.js' 'apps/invoices/frontend/package.json' 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/invoices/frontend/src/api/meta.ts' 'apps/invoices/frontend/src/api/request.ts' 'apps/invoices/frontend/src/i18n.ts' 'apps/invoices/frontend/src/index.ts' 'apps/invoices/frontend/src/pages/invoices.test.tsx' 'apps/invoices/frontend/src/pages/invoices.tsx' 'apps/invoices/frontend/src/test/api.ts' 'apps/invoices/frontend/src/test/fetch.ts' 'apps/invoices/frontend/src/test/render.tsx' 'apps/invoices/frontend/src/test/setup.ts' 'apps/invoices/frontend/tsconfig.app.json' 'apps/invoices/frontend/tsconfig.json' 'apps/invoices/frontend/tsconfig.node.json' 'apps/invoices/frontend/vite.config.ts' 'apps/products/frontend/eslint.config.js' 'apps/projects/frontend/eslint.config.js' 'apps/server/.golangci.yml' 'apps/server/cmd/vantigo/main.go' 'apps/server/generate.go' 'apps/server/internal/config/config.go' 'apps/server/internal/config/config_test.go' 'apps/server/internal/db/migrations/00034_invoices_baseline.sql' 'apps/server/internal/db/schema_test.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/main_test.go' 'apps/server/internal/invoices/meta.go' 'apps/server/internal/invoices/meta_test.go' 'apps/server/internal/invoices/module.go' 'apps/server/internal/invoices/module_internal_test.go' 'apps/server/internal/invoices/queries/counters.sql' 'apps/server/internal/invoices/queries/settings.sql' 'apps/server/internal/invoices/queries/vatcodes.sql' 'apps/server/internal/invoices/seller.go' 'apps/server/internal/invoices/server.go' 'apps/server/internal/invoices/sqlc.yaml' 'apps/server/internal/invoices/store/counters.sql.go' 'apps/server/internal/invoices/store/db.go' 'apps/server/internal/invoices/store/models.go' 'apps/server/internal/invoices/store/settings.sql.go' 'apps/server/internal/invoices/store/vatcodes.sql.go' 'apps/server/internal/invoices/values.go' 'apps/server/internal/module/compose_test.go' 'apps/server/internal/openapi/gen/cfg-invoices.yaml' 'apps/server/internal/openapi/openapi.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'apps/time/frontend/eslint.config.js' 'bun.lock' 'openapi/COVERAGE.md' 'openapi/invoices.yaml' 'tools/openapi/gen-client.test.ts' 'tools/openapi/gen-client.ts'
git commit -F /tmp/claude-1000/msg-invoices-task1.txt -- 'apps/communications/frontend/eslint.config.js' 'apps/customers/frontend/eslint.config.js' 'apps/energy/frontend/eslint.config.js' 'apps/expenses/frontend/eslint.config.js' 'apps/host/frontend/package.json' 'apps/host/frontend/src/apps.test.ts' 'apps/host/frontend/src/apps.ts' 'apps/host/frontend/src/catalogs/admin.test.ts' 'apps/host/frontend/src/catalogs/admin.ts' 'apps/host/frontend/src/catalogs/navigation.ts' 'apps/host/frontend/src/i18n.ts' 'apps/host/frontend/src/navigation.ts' 'apps/host/frontend/src/routeTree.gen.ts' 'apps/host/frontend/src/routes/invoices.tsx' 'apps/host/frontend/src/routes/invoices/index.tsx' 'apps/invoices/frontend/.gitignore' 'apps/invoices/frontend/eslint.config.js' 'apps/invoices/frontend/package.json' 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/invoices/frontend/src/api/meta.ts' 'apps/invoices/frontend/src/api/request.ts' 'apps/invoices/frontend/src/i18n.ts' 'apps/invoices/frontend/src/index.ts' 'apps/invoices/frontend/src/pages/invoices.test.tsx' 'apps/invoices/frontend/src/pages/invoices.tsx' 'apps/invoices/frontend/src/test/api.ts' 'apps/invoices/frontend/src/test/fetch.ts' 'apps/invoices/frontend/src/test/render.tsx' 'apps/invoices/frontend/src/test/setup.ts' 'apps/invoices/frontend/tsconfig.app.json' 'apps/invoices/frontend/tsconfig.json' 'apps/invoices/frontend/tsconfig.node.json' 'apps/invoices/frontend/vite.config.ts' 'apps/products/frontend/eslint.config.js' 'apps/projects/frontend/eslint.config.js' 'apps/server/.golangci.yml' 'apps/server/cmd/vantigo/main.go' 'apps/server/generate.go' 'apps/server/internal/config/config.go' 'apps/server/internal/config/config_test.go' 'apps/server/internal/db/migrations/00034_invoices_baseline.sql' 'apps/server/internal/db/schema_test.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/main_test.go' 'apps/server/internal/invoices/meta.go' 'apps/server/internal/invoices/meta_test.go' 'apps/server/internal/invoices/module.go' 'apps/server/internal/invoices/module_internal_test.go' 'apps/server/internal/invoices/queries/counters.sql' 'apps/server/internal/invoices/queries/settings.sql' 'apps/server/internal/invoices/queries/vatcodes.sql' 'apps/server/internal/invoices/seller.go' 'apps/server/internal/invoices/server.go' 'apps/server/internal/invoices/sqlc.yaml' 'apps/server/internal/invoices/store/counters.sql.go' 'apps/server/internal/invoices/store/db.go' 'apps/server/internal/invoices/store/models.go' 'apps/server/internal/invoices/store/settings.sql.go' 'apps/server/internal/invoices/store/vatcodes.sql.go' 'apps/server/internal/invoices/values.go' 'apps/server/internal/module/compose_test.go' 'apps/server/internal/openapi/gen/cfg-invoices.yaml' 'apps/server/internal/openapi/openapi.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'apps/time/frontend/eslint.config.js' 'bun.lock' 'openapi/COVERAGE.md' 'openapi/invoices.yaml' 'tools/openapi/gen-client.test.ts' 'tools/openapi/gen-client.ts'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 2: The seller record, the series start, and VAT codes with dated rate periods (D2, D3)

`GET/PUT /settings` (full replace with revision; both mod-11 checks, IBAN mod-97, BIC, "Only NOK in this phase"; `series_locked` once the counter row exists, decided after taking the settings row `FOR UPDATE`, which waits behind an issue's `FOR SHARE`), and the VAT codes: list, create with a first open period, replace (the in-use rule on category and SAF-T code), and the rate periods (the rate-change rule and removing the latest future period). `withLockedTx` arrives with its first user.

**Files:**
- Create: `apps/server/internal/invoices/decimal.go`, `apps/server/internal/invoices/errors.go`, `apps/server/internal/invoices/settings.go`, `apps/server/internal/invoices/settings_internal_test.go`, `apps/server/internal/invoices/settings_test.go`, `apps/server/internal/invoices/vatcodes.go`, `apps/server/internal/invoices/vatcodes_test.go`
- Modify: `apps/server/internal/invoices/queries/counters.sql`, `apps/server/internal/invoices/queries/settings.sql`, `apps/server/internal/invoices/queries/vatcodes.sql`, `apps/server/internal/invoices/server.go`, `apps/server/internal/invoices/values.go`, `openapi/invoices.yaml`
- Generated (commit them; never edit by hand): `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/counters.sql.go`, `apps/server/internal/invoices/store/settings.sql.go`, `apps/server/internal/invoices/store/vatcodes.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/expenses/{settings.go,values.go,errors.go,categories.go}`, `apps/server/internal/customers/values.go:116-146` (the mod-11 organisation number), `openapi/customers.yaml:346-370` (`CustomerConflictProblem`), `internal/db/tx.go:113` (`IsUniqueViolation`)

**Interfaces:**
- Produces wire: `InvoicesConflictProblem {code?, mergedInto?, linePosition?, allowedIssueDates?, …ProblemDetails}`; `GET/PUT /settings` (`InvoicesSettingsRequest`, `InvoicesSettingsResponse` with `seriesLocked`, `missingSellerFields`); `GET/POST /vat-codes`, `PUT /vat-codes/{id}`, `POST /vat-codes/{id}/rates`, `DELETE /vat-codes/{id}/rates/{rateId}` (`InvoicesVatCode` with `inUse` and `rates[]`).
- Produces Go: `withLockedTx`, `lockedTxKey`; `conflict(code, title, detail)`, `revisionConflict(what, current, supplied)`, `invalid(title, errs)`, `withFieldError`, `fieldError`, `errRefused`, `ptr`; `ratFromFloat`, `decimalPlaces`, `finite`, `numericFromRat`; `validOrganisationNumber`, `validBankAccount`, `validIBAN`, `maxLength`, `onlyNOK`; `utcDay`; codes `series_locked`, `vat_code_in_use`, `rate_change_in_past`, `rate_period_not_latest`, `rate_period_last`, `rate_period_in_use`.
- Produces SQL: `LockSettings`, `UpdateSettings`, `LatestIssueDate`, the VAT-code queries.

- [ ] **Step 1: The tests: the check digits, the seller record, the series lock, the codes and the rate-change rule**

**Create** `apps/server/internal/invoices/settings_internal_test.go`:

```go
package invoices

import "testing"

// The three check-digit rules of the seller record (D2), against numbers
// whose validity is published: Brønnøysundregistrene's own organisation
// number, DNB's sample account and its IBAN, and a Swedish IBAN.
func TestSellerNumbers_TheCheckDigitRules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		number string
		want   bool
	}{
		{"974760673", true}, {"923609016", true}, {"974760674", false}, {"97476067", false}, {"97476067a", false},
	} {
		if got := validOrganisationNumber(c.number); got != c.want {
			t.Errorf("validOrganisationNumber(%q) = %v, want %v", c.number, got, c.want)
		}
	}
	for _, c := range []struct {
		number string
		want   bool
	}{
		{"86011117947", true}, {"12345678903", true}, {"86011117948", false}, {"8601111794", false},
	} {
		if got := validBankAccount(c.number); got != c.want {
			t.Errorf("validBankAccount(%q) = %v, want %v", c.number, got, c.want)
		}
	}
	for _, c := range []struct {
		iban string
		want bool
	}{
		{"NO9386011117947", true}, {"SE4550000000058398257466", true}, {"GB82WEST12345698765432", true},
		{"NO9386011117948", false}, {"NO93860111179", false}, {"9O9386011117947", false},
	} {
		if got := validIBAN(c.iban); got != c.want {
			t.Errorf("validIBAN(%q) = %v, want %v", c.iban, got, c.want)
		}
	}
}
```

**Create** `apps/server/internal/invoices/settings_test.go`:

```go
package invoices_test

import (
	"net/http"
	"slices"
	"testing"
)

const settingsPath = "/api/v1/invoices/settings"

// settingsJSON is the settings as a client reads them.
type settingsJSON struct {
	LegalName               string   `json:"legalName"`
	OrganisationNumber      string   `json:"organisationNumber"`
	VatRegistered           bool     `json:"vatRegistered"`
	InForetaksregisteret    bool     `json:"inForetaksregisteret"`
	AddressLine1            string   `json:"addressLine1"`
	AddressLine2            string   `json:"addressLine2"`
	PostalCode              string   `json:"postalCode"`
	City                    string   `json:"city"`
	Country                 string   `json:"country"`
	BankAccount             string   `json:"bankAccount"`
	Iban                    string   `json:"iban"`
	Bic                     string   `json:"bic"`
	Email                   string   `json:"email"`
	DefaultPaymentTermsDays int32    `json:"defaultPaymentTermsDays"`
	DefaultCurrency         string   `json:"defaultCurrency"`
	FooterText              string   `json:"footerText"`
	SeriesStart             int64    `json:"seriesStart"`
	SeriesLocked            bool     `json:"seriesLocked"`
	MissingSellerFields     []string `json:"missingSellerFields"`
	Revision                int32    `json:"revision"`
}

// problemJSON is a refusal as a client reads it: the conflict's code, the
// validation's field errors.
type problemJSON struct {
	Title             string              `json:"title"`
	Detail            string              `json:"detail"`
	Code              string              `json:"code"`
	Errors            map[string][]string `json:"errors"`
	MergedInto        *int32              `json:"mergedInto"`
	LinePosition      *int32              `json:"linePosition"`
	AllowedIssueDates []string            `json:"allowedIssueDates"`
}

func problemOf(t *testing.T, res interface{ JSON(any) }) problemJSON {
	t.Helper()
	var p problemJSON
	res.JSON(&p)
	return p
}

// completeSeller is a seller body that passes every rule and is complete: a
// VAT-registered AS in Oslo with Brønnøysundregistrene's own organisation
// number and DNB's sample account.
func completeSeller(revision int32) map[string]any {
	return map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": "faktura@kraft-verket.no", "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1, "revision": revision,
	}
}

// saveSeller replaces the settings with body as an invoices:manage holder.
func saveSeller(t *testing.T, h *harness, body map[string]any) settingsJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s, want 200", res.Status, res.Body)
	}
	var s settingsJSON
	res.JSON(&s)
	return s
}

// A complete seller is stored normalised — the separators dropped, the codes
// upper-cased — and from then on meta calls the seller complete.
func TestSettings_AReplaceStoresTheSellerNormalised(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	saved := saveSeller(t, h, completeSeller(1))

	if saved.OrganisationNumber != "974760673" || saved.BankAccount != "86011117947" ||
		saved.Iban != "NO9386011117947" || saved.Bic != "DNBANOKKXXX" || saved.Country != "NO" {
		t.Errorf("saved = %+v, want the numbers without separators and the codes upper-cased", saved)
	}
	if saved.Revision != 2 || saved.SeriesLocked || len(saved.MissingSellerFields) != 0 {
		t.Errorf("revision %d, locked %v, missing %v; want 2, false, none", saved.Revision, saved.SeriesLocked, saved.MissingSellerFields)
	}
	if meta := getMeta(t, h, "invoices:access"); !meta.SellerComplete {
		t.Errorf("meta after a complete seller = incomplete, missing %v", meta.MissingSellerFields)
	}
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.LegalName != "Kraft-Verket AS" || read.FooterText != "Takk for handelen." {
		t.Errorf("GET /settings = %+v, want what was saved", read)
	}
}

// Each field's rule is a 400 on that field (D2).
func TestSettings_EveryRuleIsA400OnItsField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")

	for _, c := range []struct {
		field string
		value any
	}{
		{"organisationNumber", "974760674"},
		{"organisationNumber", "12345"},
		{"bankAccount", "86011117948"},
		{"iban", "NO9386011117948"},
		{"bic", "DNB"},
		{"email", "not an address"},
		{"country", "Norway"},
		{"defaultPaymentTermsDays", 366},
		{"defaultPaymentTermsDays", -1},
		{"defaultCurrency", "EUR"},
		{"seriesStart", 0},
		{"legalName", string(make([]byte, 201))},
		{"footerText", string(make([]byte, 501))},
	} {
		body := completeSeller(1)
		body[c.field] = c.value
		res := manager.Do(http.MethodPut, settingsPath, body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s = %v: %d %s, want 400", c.field, c.value, res.Status, res.Body)
			continue
		}
		if p := problemOf(t, res); len(p.Errors[c.field]) == 0 {
			t.Errorf("%s = %v: errors %v, want one on %s", c.field, c.value, p.Errors, c.field)
		}
	}
	body := completeSeller(1)
	body["defaultCurrency"] = "EUR"
	if p := problemOf(t, manager.Do(http.MethodPut, settingsPath, body)); !slices.Equal(p.Errors["defaultCurrency"], []string{"Only NOK in this phase"}) {
		t.Errorf("EUR: %v, want exactly \"Only NOK in this phase\"", p.Errors)
	}
}

// Meta names what the seller still lacks, field by field.
func TestSettings_MetaNamesTheMissingFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["organisationNumber"] = ""
	body["city"] = ""
	saveSeller(t, h, body)

	if meta := getMeta(t, h, "invoices:access"); meta.SellerComplete || !slices.Equal(meta.MissingSellerFields, []string{"organisationNumber", "city"}) {
		t.Errorf("meta = complete %v missing %v, want incomplete missing organisationNumber and city", meta.SellerComplete, meta.MissingSellerFields)
	}
}

// A stale revision is a 409 naming both, with no code; a replace needs
// invoices:manage.
func TestSettings_TheRevisionAndThePermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))

	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, completeSeller(1))
	if res.Status != http.StatusConflict {
		t.Fatalf("a stale revision = %d, want 409", res.Status)
	}
	if p := problemOf(t, res); p.Code != "" || p.Detail != "The Invoice settings has revision 2; the supplied revision was 1." {
		t.Errorf("stale revision = %+v, want no code and both revisions named", p)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPut, settingsPath, completeSeller(2)); res.Status != http.StatusForbidden {
		t.Errorf("PUT without invoices:manage = %d, want 403", res.Status)
	}
}

// The series start is the settings' own until something is issued; from the
// counter row on it is refused with series_locked, and every other field stays
// editable (D2).
func TestSettings_TheSeriesStartLocksAtTheFirstIssue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["seriesStart"] = 1000
	if saved := saveSeller(t, h, body); saved.SeriesStart != 1000 {
		t.Fatalf("series start = %d, want 1000 while nothing is issued", saved.SeriesStart)
	}

	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 1001)`)

	body = completeSeller(2)
	body["seriesStart"] = 5000
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, body)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "series_locked" {
		t.Fatalf("a changed start after the first issue = %d %s, want 409 series_locked", res.Status, res.Body)
	}
	body["seriesStart"] = 1000
	body["legalName"] = "Kraft-Verket Norge AS"
	if saved := saveSeller(t, h, body); !saved.SeriesLocked || saved.LegalName != "Kraft-Verket Norge AS" {
		t.Errorf("saved = %+v, want the name changed and the series locked", saved)
	}
}
```

**Create** `apps/server/internal/invoices/vatcodes_test.go`:

```go
package invoices_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const vatCodesPath = "/api/v1/invoices/vat-codes"

// The seeded codes' fixed ids (00034).
const (
	vat25        = 1
	vat15        = 2
	vatZero      = 5
	vatReverse   = 6
	vatExport    = 7
	vatExempt    = 8
	vatOutside   = 9
	vatUnknownID = 9999
)

type vatRateJSON struct {
	ID          int32   `json:"id"`
	RatePercent float64 `json:"ratePercent"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
}

type vatCodeJSON struct {
	ID              int32         `json:"id"`
	Code            string        `json:"code"`
	Name            string        `json:"name"`
	SafTCode        string        `json:"safTCode"`
	EhfCategory     string        `json:"ehfCategory"`
	ExemptionReason *string       `json:"exemptionReason"`
	Active          bool          `json:"active"`
	InUse           bool          `json:"inUse"`
	Revision        int32         `json:"revision"`
	Rates           []vatRateJSON `json:"rates"`
}

func manager(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:manage")
}

func listVatCodes(t *testing.T, h *harness) map[int32]vatCodeJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, vatCodesPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /vat-codes = %d %s", res.Status, res.Body)
	}
	var codes []vatCodeJSON
	res.JSON(&codes)
	out := map[int32]vatCodeJSON{}
	for _, c := range codes {
		out[c.ID] = c
	}
	return out
}

// periods renders a code's periods as from..to, for one comparison.
func periods(c vatCodeJSON) string {
	out := ""
	for _, r := range c.Rates {
		to := "open"
		if r.ValidTo != nil {
			to = *r.ValidTo
		}
		out += fmt.Sprintf("[%v %s..%s]", r.RatePercent, r.ValidFrom, to)
	}
	return out
}

// plantIssuedDocument writes an issued document directly, bypassing the
// handlers — what a date rule or a gap check needs to see — and returns its id.
func plantIssuedDocument(t *testing.T, h *harness, number int64, issueDate string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'issued', $1, 1, $2::date, now(), $3, now(), now())
		RETURNING id`, number, issueDate, uuid.New())
}

// plantDraftLine writes a draft with one line on vatCodeID directly, which is
// all "in use" needs.
func plantDraftLine(t *testing.T, h *harness, vatCodeID int32) {
	t.Helper()
	id := modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, customer_id, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 1, $1, now(), now()) RETURNING id`, uuid.New())
	h.Exec(t, `
		INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id, line_gross, line_allowance, line_net)
		VALUES ($1, 1, 'Konsulenttime', 1, 100, $2, 100, 0, 100)`, id, vatCodeID)
}

// The seed is on GET /vat-codes as it is on meta, each with its one open
// 2026 period, none in use.
func TestVatCodes_TheSeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	codes := listVatCodes(t, h)
	if len(codes) != 9 {
		t.Fatalf("codes = %d, want the nine seeded", len(codes))
	}
	if c := codes[vatExempt]; c.Code != "6" || c.EhfCategory != "E" || periods(c) != "[0 2026-01-01..open]" || c.InUse {
		t.Errorf("code 6 = %+v, want E at 0 from 2026-01-01, not in use", c)
	}
	if c := codes[vatOutside]; c.Code != "7" || c.EhfCategory != "O" || c.ExemptionReason == nil {
		t.Errorf("code 7 = %+v, want O with its reason", c)
	}
	if c := codes[vat25]; periods(c) != "[25 2026-01-01..open]" || c.ExemptionReason != nil {
		t.Errorf("code 3 = %+v, want 25 %% open-ended and no reason", c)
	}
}

// A code is created with its first, open period; the label is unique ignoring
// case; a reason is required unless S; an S rate is above 0, every other 0.
func TestVatCodes_CreateAndItsRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)

	res := m.Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "3H", "name": "Høy sats", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01",
	})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /vat-codes = %d %s, want 201", res.Status, res.Body)
	}
	var created vatCodeJSON
	res.JSON(&created)
	if created.Code != "3H" || !created.Active || periods(created) != "[25 2026-01-01..open]" {
		t.Errorf("created = %+v", created)
	}

	for _, c := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"the same label in another case", map[string]any{"code": "3h", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01"}, "code"},
		{"a Z code without a reason", map[string]any{"code": "Z1", "name": "X", "safTCode": "5", "ehfCategory": "Z", "ratePercent": 0, "validFrom": "2026-01-01"}, "exemptionReason"},
		{"an S code at 0", map[string]any{"code": "S0", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 0, "validFrom": "2026-01-01"}, "ratePercent"},
		{"an S code over 100", map[string]any{"code": "S9", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 100.5, "validFrom": "2026-01-01"}, "ratePercent"},
		{"a Z code with a rate", map[string]any{"code": "Z2", "name": "X", "safTCode": "5", "ehfCategory": "Z", "exemptionReason": "Fritatt", "ratePercent": 5, "validFrom": "2026-01-01"}, "ratePercent"},
		{"three decimals", map[string]any{"code": "S3", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 11.111, "validFrom": "2026-01-01"}, "ratePercent"},
		{"an unknown category", map[string]any{"code": "Q", "name": "X", "safTCode": "3", "ehfCategory": "Q", "ratePercent": 0, "validFrom": "2026-01-01"}, "ehfCategory"},
		{"a code too long", map[string]any{"code": "ABCDEFGHIJK", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01"}, "code"},
	} {
		res := m.Do(http.MethodPost, vatCodesPath, c.body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", c.name, res.Status, res.Body, c.field)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "X", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01",
	}); res.Status != http.StatusForbidden {
		t.Errorf("POST without invoices:manage = %d, want 403", res.Status)
	}
}

// A code in use keeps its category and SAF-T code; its label, name and reason
// stay editable; a deactivated code leaves what meta offers (D3).
func TestVatCodes_TheInUseRuleAndDeactivation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)
	update := func(id int32, body map[string]any) (int, problemJSON, vatCodeJSON) {
		res := m.Do(http.MethodPut, fmt.Sprintf("%s/%d", vatCodesPath, id), body)
		var code vatCodeJSON
		var p problemJSON
		if res.Status == http.StatusOK {
			res.JSON(&code)
		} else if len(res.Body) > 0 {
			res.JSON(&p)
		}
		return res.Status, p, code
	}

	// Not in use: the category may change, when the rates fit it.
	if status, _, code := update(vatZero, map[string]any{"code": "5", "name": "Fritatt", "safTCode": "52", "ehfCategory": "G", "exemptionReason": "Utførsel", "active": true, "revision": 1}); status != http.StatusOK || code.EhfCategory != "G" || code.Revision != 2 {
		t.Fatalf("an unused code's category change = %d %+v, want 200", status, code)
	}
	if status, p, _ := update(vatZero, map[string]any{"code": "5", "name": "Fritatt", "safTCode": "52", "ehfCategory": "S", "active": true, "revision": 2}); status != http.StatusBadRequest || len(p.Errors["ehfCategory"]) == 0 {
		t.Errorf("S over a 0 %% period = %d %+v, want 400 on ehfCategory", status, p)
	}

	plantDraftLine(t, h, vat25)
	if status, p, _ := update(vat25, map[string]any{"code": "3", "name": "Utgående mva 25 %", "safTCode": "31", "ehfCategory": "S", "active": true, "revision": 1}); status != http.StatusConflict || p.Code != "vat_code_in_use" {
		t.Errorf("an in-use code's SAF-T change = %d %+v, want 409 vat_code_in_use", status, p)
	}
	status, _, code := update(vat25, map[string]any{"code": "3A", "name": "Høy sats", "safTCode": "3", "ehfCategory": "S", "active": false, "revision": 1})
	if status != http.StatusOK || code.Code != "3A" || code.Active || !code.InUse {
		t.Fatalf("renaming and deactivating an in-use code = %d %+v, want 200", status, code)
	}
	for _, c := range getMeta(t, h, "invoices:access").VatCodes {
		if c.ID == vat25 {
			t.Error("meta still offers a deactivated code")
		}
	}
	if status, p, _ := update(vat25, map[string]any{"code": "3A", "name": "Høy sats", "safTCode": "3", "ehfCategory": "S", "active": true, "revision": 1}); status != http.StatusConflict || p.Code != "" {
		t.Errorf("a stale revision = %d %+v, want 409 without a code", status, p)
	}
	if status, _, _ := update(vatUnknownID, map[string]any{"code": "Z", "name": "Z", "safTCode": "3", "ehfCategory": "S", "active": true, "revision": 1}); status != http.StatusNotFound {
		t.Errorf("an unknown code = %d, want 404", status)
	}
}

// The rate-change rule (D3): a new period closes the old one the day before;
// it must start after the open one and after the latest issue date; the latest
// future period can be removed and the previous reopened, but not an earlier
// one, not the only one, and not once a document is dated in it.
func TestVatCodes_TheRateChangeRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)
	ratesPath := fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25)

	res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 26, "validFrom": "2027-01-01"})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST rates = %d %s, want 201", res.Status, res.Body)
	}
	var changed vatCodeJSON
	res.JSON(&changed)
	if got := periods(changed); got != "[25 2026-01-01..2026-12-31][26 2027-01-01..open]" {
		t.Errorf("periods = %s, want the old closed the day before the new", got)
	}

	for _, c := range []struct {
		name, validFrom string
	}{{"on the open period's start", "2027-01-01"}, {"before it", "2026-06-01"}} {
		res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 27, "validFrom": c.validFrom})
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["validFrom"]) == 0 {
			t.Errorf("a new period %s = %d %s, want 400 on validFrom", c.name, res.Status, res.Body)
		}
	}
	if res := m.Do(http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vatZero), map[string]any{"ratePercent": 5, "validFrom": "2027-01-01"}); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["ratePercent"]) == 0 {
		t.Errorf("a rate on a Z code = %d %s, want 400 on ratePercent", res.Status, res.Body)
	}

	// Removing: never an earlier period, never the only one.
	first, latest := changed.Rates[0].ID, changed.Rates[1].ID
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, first), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_period_not_latest" {
		t.Errorf("removing the earlier period = %d %s, want 409 rate_period_not_latest", res.Status, res.Body)
	}
	only := listVatCodes(t, h)[vat15].Rates[0].ID
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d/rates/%d", vatCodesPath, vat15, only), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_period_last" {
		t.Errorf("removing the only period = %d %s, want 409 rate_period_last", res.Status, res.Body)
	}
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, 424242), nil); res.Status != http.StatusNotFound {
		t.Errorf("removing a period the code does not have = %d, want 404", res.Status)
	}

	// A document dated in the new period keeps it.
	plantIssuedDocument(t, h, 1, "2027-01-05")
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, latest), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_period_in_use" {
		t.Errorf("removing a period a document is dated in = %d %s, want 409 rate_period_in_use", res.Status, res.Body)
	}
	// And no change may start on or before the latest issue date.
	for _, day := range []string{"2027-01-05", "2027-01-02"} {
		if res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 27, "validFrom": day}); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_change_in_past" {
			t.Errorf("a change from %s = %d %s, want 409 rate_change_in_past", day, res.Status, res.Body)
		}
	}
	if res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 27, "validFrom": "2027-01-06"}); res.Status != http.StatusCreated {
		t.Errorf("a change from the day after = %d %s, want 201", res.Status, res.Body)
	}
}

// Removing the latest future period reopens the one before it.
func TestVatCodes_RemovingTheLatestFuturePeriodReopensThePrevious(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)
	ratesPath := fmt.Sprintf("%s/%d/rates", vatCodesPath, vat15)
	var changed vatCodeJSON
	m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 16, "validFrom": "2027-01-01"}).JSON(&changed)

	res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, changed.Rates[1].ID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("DELETE the latest period = %d %s, want 200", res.Status, res.Body)
	}
	var after vatCodeJSON
	res.JSON(&after)
	if got := periods(after); got != "[15 2026-01-01..open]" {
		t.Errorf("periods after the removal = %s, want the 2026 period open again", got)
	}
}
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 ./internal/invoices/ ; cd ../..   # FAIL: undefined validOrganisationNumber; 404s from the unknown paths
```

- [ ] **Step 2: The contract and the queries**

**Insert** into `openapi/invoices.yaml`, immediately before the line `info:`:

```yaml
        InvoicesConflictProblem:
            description: 'ProblemDetails plus this module''s refusal code (invoices foundation design D2-D8). code names the rule that refused — series_locked, vat_code_in_use, rate_change_in_past, rate_period_not_latest, rate_period_last, rate_period_in_use, invoice_issued, invoice_draft, customer_merged, customer_archived, customer_blocked, customer_missing, invoice_changed, seller_incomplete, no_lines, delivery_date_missing, issue_date_not_allowed, buyer_incomplete, vat_code_inactive, vat_code_not_valid, vat_not_registered, category_o_not_allowed, reverse_charge_needs_org_number, vat_codes_ambiguous, credit_exceeds_line, credit_exceeds_invoice, credit_note_not_creditable, invoice_fully_credited. A revision conflict carries no code; its detail names both revisions.'
            properties:
                allowedIssueDates:
                    description: On issue_date_not_allowed, the dates this document may be issued with today, the earliest first. Absent otherwise.
                    items:
                        format: date
                        type: string
                    type: array
                code:
                    nullable: true
                    type: string
                detail:
                    nullable: true
                    type: string
                instance:
                    nullable: true
                    type: string
                linePosition:
                    description: On a refusal about one line (vat_code_inactive, vat_code_not_valid, credit_exceeds_line), the line's position, 1-based. Absent otherwise.
                    format: int32
                    type: integer
                mergedInto:
                    description: On customer_merged, the customer the draft's customer was merged into. Absent otherwise.
                    format: int32
                    type: integer
                status:
                    format: int32
                    nullable: true
                    type: integer
                title:
                    nullable: true
                    type: string
                type:
                    nullable: true
                    type: string
            type: object
        InvoicesSettingsRequest:
            description: 'PUT /settings'' body, a full replace (D2). Every text field is trimmed; an empty string is "not set". organisationNumber is nine digits with a valid mod-11 check digit; bankAccount eleven digits with a valid mod-11 check digit (spaces and dots are dropped); iban passes mod-97 and bic is 8 or 11 characters, both optional; country is ISO 3166-1 alpha-2; defaultPaymentTermsDays is 0-365; defaultCurrency is NOK and only NOK in this phase; seriesStart is 1 or more and cannot change once anything is issued (409 series_locked). revision is the one the caller read: a stale one is a 409 naming both.'
            properties:
                addressLine1:
                    type: string
                addressLine2:
                    type: string
                bankAccount:
                    type: string
                bic:
                    type: string
                city:
                    type: string
                country:
                    type: string
                defaultCurrency:
                    type: string
                defaultPaymentTermsDays:
                    format: int32
                    type: integer
                email:
                    type: string
                footerText:
                    type: string
                iban:
                    type: string
                inForetaksregisteret:
                    type: boolean
                legalName:
                    type: string
                organisationNumber:
                    type: string
                postalCode:
                    type: string
                revision:
                    format: int32
                    type: integer
                seriesStart:
                    format: int64
                    type: integer
                vatRegistered:
                    type: boolean
            required:
                - legalName
                - organisationNumber
                - vatRegistered
                - inForetaksregisteret
                - addressLine1
                - postalCode
                - city
                - country
                - bankAccount
                - defaultPaymentTermsDays
                - defaultCurrency
                - seriesStart
                - revision
            type: object
        InvoicesSettingsResponse:
            description: The seller record and the series start (D2). A text field that is not set is the empty string. seriesLocked is true once anything is issued; from then on seriesStart cannot change, and every other field still can — issued documents keep their own seller snapshot.
            properties:
                addressLine1:
                    type: string
                addressLine2:
                    type: string
                bankAccount:
                    type: string
                bic:
                    type: string
                city:
                    type: string
                country:
                    type: string
                defaultCurrency:
                    type: string
                defaultPaymentTermsDays:
                    format: int32
                    type: integer
                email:
                    type: string
                footerText:
                    type: string
                iban:
                    type: string
                inForetaksregisteret:
                    type: boolean
                legalName:
                    type: string
                missingSellerFields:
                    description: What issuing still needs, as GET /meta names it.
                    items:
                        type: string
                    type: array
                organisationNumber:
                    type: string
                postalCode:
                    type: string
                revision:
                    format: int32
                    type: integer
                seriesLocked:
                    type: boolean
                seriesStart:
                    format: int64
                    type: integer
                updatedAt:
                    format: date-time
                    type: string
                vatRegistered:
                    type: boolean
            required:
                - legalName
                - organisationNumber
                - vatRegistered
                - inForetaksregisteret
                - addressLine1
                - addressLine2
                - postalCode
                - city
                - country
                - bankAccount
                - iban
                - bic
                - email
                - defaultPaymentTermsDays
                - defaultCurrency
                - footerText
                - seriesStart
                - seriesLocked
                - missingSellerFields
                - revision
                - updatedAt
            type: object
        InvoicesVatCode:
            description: One VAT code with every rate period it has had (D3). inUse is true once any line, draft or issued, carries the code; from then on its category and SAF-T code cannot change (409 vat_code_in_use).
            properties:
                active:
                    type: boolean
                code:
                    type: string
                ehfCategory:
                    type: string
                exemptionReason:
                    description: Absent when the code has none (category S).
                    type: string
                id:
                    format: int32
                    type: integer
                inUse:
                    type: boolean
                name:
                    type: string
                rates:
                    description: Every period, the earliest first. The last is open-ended (validTo absent).
                    items:
                        $ref: '#/components/schemas/InvoicesVatCodeRate'
                    type: array
                revision:
                    format: int32
                    type: integer
                safTCode:
                    type: string
            required:
                - id
                - code
                - name
                - safTCode
                - ehfCategory
                - active
                - inUse
                - revision
                - rates
            type: object
        InvoicesVatCodeCreateRequest:
            description: A code and its first, open period (D3). code is 1-10 characters and unique ignoring case; name 1-100; safTCode 1-5; ehfCategory S, Z, E, AE, G, O or K; exemptionReason up to 200 and required unless the category is S; ratePercent greater than 0 and at most 100 for S, exactly 0 for every other category, with at most two decimals.
            properties:
                code:
                    type: string
                ehfCategory:
                    type: string
                exemptionReason:
                    type: string
                name:
                    type: string
                ratePercent:
                    format: double
                    type: number
                safTCode:
                    type: string
                validFrom:
                    format: date
                    type: string
            required:
                - code
                - name
                - safTCode
                - ehfCategory
                - ratePercent
                - validFrom
            type: object
        InvoicesVatCodeRate:
            description: One rate period of a VAT code, inclusive at both ends.
            properties:
                id:
                    format: int32
                    type: integer
                ratePercent:
                    format: double
                    type: number
                validFrom:
                    format: date
                    type: string
                validTo:
                    description: Absent for the open-ended period.
                    format: date
                    type: string
            required:
                - id
                - ratePercent
                - validFrom
            type: object
        InvoicesVatCodeRateRequest:
            description: 'A rate change (D3): the open period closes the day before validFrom and a new open period starts on it. validFrom must be after the open period''s own start (400 on validFrom) and after the latest issue date of any issued document (409 rate_change_in_past). ratePercent follows the category''s rule.'
            properties:
                ratePercent:
                    format: double
                    type: number
                validFrom:
                    format: date
                    type: string
            required:
                - ratePercent
                - validFrom
            type: object
        InvoicesVatCodeUpdateRequest:
            description: A full replace of a code's own fields (D3). A code is never deleted; active false stops it being offered for new lines. A code in use keeps its ehfCategory and safTCode (409 vat_code_in_use); its code, name and reason stay editable. The rates change only through the rate operations.
            properties:
                active:
                    type: boolean
                code:
                    type: string
                ehfCategory:
                    type: string
                exemptionReason:
                    type: string
                name:
                    type: string
                revision:
                    format: int32
                    type: integer
                safTCode:
                    type: string
            required:
                - code
                - name
                - safTCode
                - ehfCategory
                - active
                - revision
            type: object
```

**Insert** into `openapi/invoices.yaml`, immediately before the line `servers:`:

```yaml
    /api/v1/invoices/settings:
        get:
            description: The seller record and the series start. Every invoices:access holder may read them — they are what every document prints.
            operationId: getInvoicesSettings
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesSettingsResponse'
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
            summary: Get the invoice settings
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
        put:
            description: Replaces the seller record and the series start (D2). It takes the settings row FOR UPDATE, so it waits behind an issue in flight; the series start is refused once the counter row exists (409 series_locked). Every other field stays editable after the first issue — issued documents keep their seller snapshot.
            operationId: putInvoicesSettings
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/InvoicesSettingsRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesSettingsResponse'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — a field did not pass; the errors name it.
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
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — series_locked, or a stale revision (no code; the detail names both revisions).
            summary: Change the invoice settings
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:manage
    /api/v1/invoices/vat-codes:
        get:
            description: Every VAT code, inactive ones included, with every rate period it has had (D3).
            operationId: getInvoicesVatCodes
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                items:
                                    $ref: '#/components/schemas/InvoicesVatCode'
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
            summary: List the VAT codes
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
        post:
            description: Creates a VAT code with its first, open rate period (D3).
            operationId: postInvoicesVatCodes
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/InvoicesVatCodeCreateRequest'
                required: true
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesVatCode'
                    description: Created
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — a field did not pass, or the code already exists (on code).
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
            summary: Create a VAT code
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:manage
    /api/v1/invoices/vat-codes/{id}:
        put:
            description: Replaces a VAT code's own fields (D3). A code in use keeps its category and SAF-T code.
            operationId: putInvoicesVatCodesById
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
                            $ref: '#/components/schemas/InvoicesVatCodeUpdateRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesVatCode'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — a field did not pass, or the code already exists (on code).
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
                    description: Not Found — no VAT code has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — vat_code_in_use, or a stale revision.
            summary: Change a VAT code
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:manage
    /api/v1/invoices/vat-codes/{id}/rates:
        post:
            description: The rate-change rule (D3). The open period closes the day before validFrom and a new open period starts on it. It waits behind any issue in flight, so the latest issue date it reads is final.
            operationId: postInvoicesVatCodesByIdRates
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
                            $ref: '#/components/schemas/InvoicesVatCodeRateRequest'
                required: true
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesVatCode'
                    description: Created — the code with its periods.
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — on ratePercent, or on validFrom when it is not after the open period's start.
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
                    description: Not Found — no VAT code has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — rate_change_in_past, when validFrom is on or before the latest issue date of any issued document.
            summary: Change a VAT code's rate from a date
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:manage
    /api/v1/invoices/vat-codes/{id}/rates/{rateId}:
        delete:
            description: Removes a mistaken future period and reopens the previous one (D3). Only the latest period may go, never the only one, and only while no issued document is dated on or after its start.
            operationId: deleteInvoicesVatCodesByIdRatesByRateId
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
                - in: path
                  name: rateId
                  required: true
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesVatCode'
                    description: OK — the code with the periods it has left.
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
                    description: Not Found — no such code, or no such period on it.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — rate_period_not_latest, rate_period_last or rate_period_in_use.
            summary: Remove a VAT code's latest rate period
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:manage
```

**Append** to the end of `apps/server/internal/invoices/queries/counters.sql`:

```sql

-- name: LatestIssueDate :one
-- LatestIssueDate is the latest issue date of any issued document, NULL when
-- nothing is issued. A rate change must start after it (D3), and an issue may
-- not be dated before it (D6); both read it after the lock that makes it
-- final.
SELECT max(issue_date)::date AS latest FROM invoices.invoices WHERE status = 'issued';
```

**Append** to the end of `apps/server/internal/invoices/queries/settings.sql`:

```sql

-- name: LockSettings :one
-- LockSettings takes the settings row FOR UPDATE: PUT /settings and every rate
-- change wait here behind an issue in flight, which holds the row FOR SHARE
-- until it commits (D2, D3). What they read after it is final.
SELECT * FROM invoices.settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSettings :one
-- UpdateSettings replaces the seller record and the series start, and moves
-- the revision on. The caller holds the row (LockSettings) and has checked the
-- revision and the series lock.
UPDATE invoices.settings SET
    legal_name = @legal_name,
    organisation_number = @organisation_number,
    vat_registered = @vat_registered,
    in_foretaksregisteret = @in_foretaksregisteret,
    address_line1 = @address_line1,
    address_line2 = @address_line2,
    postal_code = @postal_code,
    city = @city,
    country = @country,
    bank_account = @bank_account,
    iban = @iban,
    bic = @bic,
    email = @email,
    default_payment_terms_days = @default_payment_terms_days,
    default_currency = @default_currency,
    footer_text = @footer_text,
    series_start = @series_start,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = 1
RETURNING *;
```

**Append** to the end of `apps/server/internal/invoices/queries/vatcodes.sql`:

```sql

-- name: ListVatCodes :many
-- ListVatCodes is every code, inactive ones included, in the order GET
-- /vat-codes answers them.
SELECT * FROM invoices.vat_codes ORDER BY lower(code), id;

-- name: ListVatCodeRates :many
-- ListVatCodeRates is every period of every code, each code's the earliest
-- first.
SELECT * FROM invoices.vat_code_rates ORDER BY vat_code_id, valid_from;

-- name: VatCodeRates :many
-- VatCodeRates is one code's periods, the earliest first.
SELECT * FROM invoices.vat_code_rates WHERE vat_code_id = @vat_code_id ORDER BY valid_from;

-- name: VatCodesInUse :many
-- VatCodesInUse is every code a line carries, draft or issued (D3's "in use").
SELECT DISTINCT vat_code_id FROM invoices.lines ORDER BY vat_code_id;

-- name: VatCodeInUse :one
-- VatCodeInUse is whether any line, draft or issued, carries the code.
SELECT EXISTS (SELECT 1 FROM invoices.lines WHERE vat_code_id = @vat_code_id)::boolean AS in_use;

-- name: GetVatCode :one
SELECT * FROM invoices.vat_codes WHERE id = @id;

-- name: LockVatCode :one
-- LockVatCode takes the code FOR UPDATE, which also waits for any draft save
-- whose new line references it (the foreign key's KEY SHARE lock), so the
-- in-use check after it sees every committed line.
SELECT * FROM invoices.vat_codes WHERE id = @id FOR UPDATE;

-- name: InsertVatCode :one
INSERT INTO invoices.vat_codes (code, name, saf_t_code, ehf_category, exemption_reason, active, created_at, updated_at)
VALUES (@code, @name, @saf_t_code, @ehf_category, @exemption_reason, true, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateVatCode :one
UPDATE invoices.vat_codes SET
    code = @code,
    name = @name,
    saf_t_code = @saf_t_code,
    ehf_category = @ehf_category,
    exemption_reason = @exemption_reason,
    active = @active,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: InsertVatCodeRate :one
INSERT INTO invoices.vat_code_rates (vat_code_id, rate_percent, valid_from, valid_to, created_at)
VALUES (@vat_code_id, @rate_percent, @valid_from, NULL, @now::timestamptz)
RETURNING *;

-- name: CloseOpenVatCodeRate :exec
-- CloseOpenVatCodeRate ends the code's open period on valid_to (D3's
-- rate-change rule: the day before the new period starts).
UPDATE invoices.vat_code_rates SET valid_to = @valid_to
WHERE vat_code_id = @vat_code_id AND valid_to IS NULL;

-- name: DeleteVatCodeRate :exec
DELETE FROM invoices.vat_code_rates WHERE id = @id;

-- name: ReopenVatCodeRate :exec
-- ReopenVatCodeRate makes a period open-ended again, after the one that
-- followed it was removed.
UPDATE invoices.vat_code_rates SET valid_to = NULL WHERE id = @id;
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 3: The errors, the decimals, the settings and the VAT codes**

**Create** `apps/server/internal/invoices/errors.go`:

```go
package invoices

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
)

// This module's refusals follow the codebase's one rule: 404 for what the
// caller may not see (a bare body), 403 for what is not theirs to do (the
// access layer's own body), 400 naming the field (apicommon.ValidationProblem)
// and 409 for a rule about the state of things — the settings, the series, a
// document — carrying the rule's code (InvoicesConflictProblem), the way the
// customers module's conflicts carry theirs. A client keys off the code, never
// the words.

// The titles this module's validation problems carry.
const (
	invalidSettingsTitle = "Invalid invoice settings"
	invalidVatCodeTitle  = "Invalid VAT code"
	invalidRateTitle     = "Invalid VAT rate"
)

// withFieldError adds one message to a map another rule may already have put
// something in. A nil map is the "nothing failed yet" case, so it is grown
// rather than written to.
func withFieldError(errs map[string][]string, field, message string) map[string][]string {
	if errs == nil {
		errs = map[string][]string{}
	}
	errs[field] = append(errs[field], message)
	return errs
}

// fieldError is the one-field error map, for a rule decided on its own.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}

// conflict is a 409 carrying the rule's code and a sentence for a person.
func conflict(code, title, detail string) gen.InvoicesConflictProblem {
	status := int32(http.StatusConflict)
	return gen.InvoicesConflictProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// revisionConflict is the 409 an update carrying a stale revision answers. It
// names both revisions, so a client can tell "somebody else saved" from "I
// sent the wrong number", and carries no code: it is not a rule of this
// module's but the codebase's (docs/expenses.md).
func revisionConflict(what string, current, supplied int32) gen.InvoicesConflictProblem {
	title := what + " revision conflict"
	detail := fmt.Sprintf("The %s has revision %d; the supplied revision was %d.", what, current, supplied)
	status := int32(http.StatusConflict)
	return gen.InvoicesConflictProblem{Title: &title, Detail: &detail, Status: &status}
}

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(title, errs)
}
```

**Create** `apps/server/internal/invoices/decimal.go`:

```go
package invoices

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is how a number crosses the wire into this module (D5). HTTP
// numbers are number/double in this codebase, so every amount arrives as a
// float64; it becomes an exact decimal through its shortest round-tripping
// text (strconv 'f', -1) and big.Rat.SetString — never SetFloat64, which keeps
// the binary value, so 0.1 would not be 0.1.

// ratFromFloat is a JSON number as the exact decimal it was written as.
func ratFromFloat(v float64) *big.Rat {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok {
		return new(big.Rat)
	}
	return r
}

// decimalPlaces is how many decimals a JSON number was written with, judged on
// the same shortest text ratFromFloat reads.
func decimalPlaces(v float64) int {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok {
		return len(decimals)
	}
	return 0
}

// finite is false for NaN and the infinities, which no JSON body can carry
// but a Go caller could.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// decimalText is v as the exact decimal text a numeric column stores, at
// places decimals, the half rounded away from zero — big.Rat's own rule.
func decimalText(v *big.Rat, places int) string { return v.FloatString(places) }

// numericFromRat stores an exact decimal as a column's value, at places
// decimals — the one road from this module's arithmetic into the database.
func numericFromRat(v *big.Rat, places int) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(decimalText(v, places)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("invoices: %v is not a storable decimal: %w", v, err)
	}
	return n, nil
}
```

**Replace** in `apps/server/internal/invoices/server.go`:

```go
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)
```

**with**:

```go
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)
```

**Replace** in `apps/server/internal/invoices/server.go`:

```go
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}
```

**with**:

```go
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// lockedTxKey marks a context as belonging to a transaction that may hold row
// locks (withLockedTx).
type lockedTxKey struct{}

// withLockedTx runs fn in one READ COMMITTED transaction on the module's pool —
// every write here that takes a row lock goes through it. fn gets a context
// marked as locked and its queries bound to the transaction.
//
// The rule the mark carries: nothing inside fn calls another module or the
// object store (docs/module-boundaries.md, docs/expenses.md). Whatever a
// decision inside fn needs from the customer directory is read before the
// transaction, and a PDF is stored after it has committed.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(locked, store.New(tx))
	})
}
```

**Append** to the end of `apps/server/internal/invoices/values.go`:

```go

// utcDay is a wire date as the UTC midnight every date here is compared at.
func utcDay(d time.Time) time.Time {
	d = d.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}
```

**Create** `apps/server/internal/invoices/settings.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the seller record and the series start (D2): one settings row,
// read by everyone with invoices:access and replaced by invoices:manage. Every
// field but the series start stays editable after the first issue, because an
// issued document keeps the seller snapshot it was issued with.

// The codes and titles of this file's 409s.
const (
	codeSeriesLocked  = "series_locked"
	seriesLockedTitle = "The number series has started"
)

// onlyNOK is the one sentence a currency other than NOK is refused with in
// phase 1 (D5): § 5-1-1 nr. 6 wants VAT in NOK at the invoice date's rate,
// which this phase does not model yet.
const onlyNOK = "Only NOK in this phase"

var (
	countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
	bicPattern     = regexp.MustCompile(`^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
)

// maxLength is the rule for a varchar(n) column: n characters, as Postgres
// counts them.
func maxLength(label string, value string, n int) string {
	if utf8.RuneCountInString(value) > n {
		return fmt.Sprintf("%s holds at most %d characters", label, n)
	}
	return ""
}

// validOrganisationNumber is the Brreg organisasjonsnummer mod-11 check, the
// customers module's validator (values.go validNorwegianOrgNumber) copied here
// because no module imports another: nine digits, the ninth the check digit
// over the first eight with weights 3 2 7 6 5 4 3 2, and a check value of 10
// invalid outright.
func validOrganisationNumber(digits string) bool {
	return mod11(digits, []int{3, 2, 7, 6, 5, 4, 3, 2})
}

// validBankAccount is the Norwegian kontonummer mod-11 check: eleven digits,
// the eleventh the check digit over the first ten with weights 5 4 3 2 7 6 5 4
// 3 2.
func validBankAccount(digits string) bool {
	return mod11(digits, []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2})
}

// mod11 is the shared rule: len(weights)+1 ASCII digits whose last is 11 less
// the weighted sum mod 11 (0 when that is 11), a check value of 10 invalid.
func mod11(digits string, weights []int) bool {
	if len(digits) != len(weights)+1 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	check := 0
	if r := sum % 11; r != 0 {
		check = 11 - r
		if check == 10 {
			return false
		}
	}
	return int(digits[len(weights)]-'0') == check
}

// validIBAN is ISO 13616's check: 15-34 characters, two letters and two
// digits first, and the whole number, rearranged, is 1 mod 97.
func validIBAN(iban string) bool {
	if len(iban) < 15 || len(iban) > 34 {
		return false
	}
	for i, r := range iban {
		switch {
		case i < 2 && (r < 'A' || r > 'Z'):
			return false
		case i >= 2 && i < 4 && (r < '0' || r > '9'):
			return false
		case (r < 'A' || r > 'Z') && (r < '0' || r > '9'):
			return false
		}
	}
	rearranged := iban[4:] + iban[:4]
	remainder := 0
	for _, r := range rearranged {
		value := int(r - '0')
		if r >= 'A' && r <= 'Z' {
			value = int(r-'A') + 10
		}
		if value >= 10 {
			remainder = (remainder*100 + value) % 97
		} else {
			remainder = (remainder*10 + value) % 97
		}
	}
	return remainder == 1
}

// withoutSeparators drops the spaces and dots people write account numbers
// with ("8601 11 17947", "8601.11.17947").
func withoutSeparators(s string) string {
	return strings.NewReplacer(" ", "", ".", "").Replace(s)
}

// parsedSettings is one validated settings body, in the shape the update wants.
type parsedSettings = store.UpdateSettingsParams

// parseSettings runs D2's rules over a settings body, every failure collected.
// An empty text field is "not set" and passes: completeness is issuing's
// question (sellerMissingFields), not saving's.
func parseSettings(body gen.InvoicesSettingsRequest) (parsedSettings, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	optional := func(v *string) string {
		if v == nil {
			return ""
		}
		return strings.TrimSpace(*v)
	}

	p := parsedSettings{
		LegalName:               strings.TrimSpace(body.LegalName),
		OrganisationNumber:      withoutSeparators(strings.TrimSpace(body.OrganisationNumber)),
		VatRegistered:           body.VatRegistered,
		InForetaksregisteret:    body.InForetaksregisteret,
		AddressLine1:            strings.TrimSpace(body.AddressLine1),
		AddressLine2:            optional(body.AddressLine2),
		PostalCode:              strings.TrimSpace(body.PostalCode),
		City:                    strings.TrimSpace(body.City),
		Country:                 strings.ToUpper(strings.TrimSpace(body.Country)),
		BankAccount:             withoutSeparators(strings.TrimSpace(body.BankAccount)),
		Iban:                    strings.ToUpper(strings.ReplaceAll(optional(body.Iban), " ", "")),
		Bic:                     strings.ToUpper(optional(body.Bic)),
		Email:                   optional(body.Email),
		DefaultPaymentTermsDays: body.DefaultPaymentTermsDays,
		DefaultCurrency:         strings.ToUpper(strings.TrimSpace(body.DefaultCurrency)),
		FooterText:              optional(body.FooterText),
		SeriesStart:             body.SeriesStart,
	}

	add("legalName", maxLength("A legal name", p.LegalName, 200))
	if p.OrganisationNumber != "" && !validOrganisationNumber(p.OrganisationNumber) {
		add("organisationNumber", "An organisation number is nine digits with a valid check digit")
	}
	add("addressLine1", maxLength("An address line", p.AddressLine1, 200))
	add("addressLine2", maxLength("An address line", p.AddressLine2, 200))
	add("postalCode", maxLength("A postal code", p.PostalCode, 20))
	add("city", maxLength("A city", p.City, 100))
	if !countryPattern.MatchString(p.Country) {
		add("country", "A country is a two-letter ISO 3166-1 code, such as NO")
	}
	if p.BankAccount != "" && !validBankAccount(p.BankAccount) {
		add("bankAccount", "A bank account number is eleven digits with a valid check digit")
	}
	if p.Iban != "" && !validIBAN(p.Iban) {
		add("iban", "This is not a valid IBAN")
	}
	if p.Bic != "" && !bicPattern.MatchString(p.Bic) {
		add("bic", "A BIC is 8 or 11 letters and digits")
	}
	if p.Email != "" {
		if a, err := mail.ParseAddress(p.Email); err != nil || a.Address != p.Email || len(p.Email) > 254 {
			add("email", "This is not an e-mail address")
		}
	}
	if p.DefaultPaymentTermsDays < 0 || p.DefaultPaymentTermsDays > 365 {
		add("defaultPaymentTermsDays", "Payment terms are between 0 and 365 days")
	}
	if p.DefaultCurrency != "NOK" {
		add("defaultCurrency", onlyNOK)
	}
	add("footerText", maxLength("The footer text", p.FooterText, 500))
	if p.SeriesStart < 1 {
		add("seriesStart", "The series starts at 1 or later")
	}
	return p, errs
}

// settingsResponse renders the settings row for the wire.
func settingsResponse(row store.InvoicesSetting, locked bool) gen.InvoicesSettingsResponse {
	return gen.InvoicesSettingsResponse{
		LegalName: row.LegalName, OrganisationNumber: row.OrganisationNumber,
		VatRegistered: row.VatRegistered, InForetaksregisteret: row.InForetaksregisteret,
		AddressLine1: row.AddressLine1, AddressLine2: row.AddressLine2,
		PostalCode: row.PostalCode, City: row.City, Country: row.Country,
		BankAccount: row.BankAccount, Iban: row.Iban, Bic: row.Bic, Email: row.Email,
		DefaultPaymentTermsDays: row.DefaultPaymentTermsDays, DefaultCurrency: row.DefaultCurrency,
		FooterText: row.FooterText, SeriesStart: row.SeriesStart, SeriesLocked: locked,
		MissingSellerFields: sellerMissingFields(row),
		Revision:            row.Revision, UpdatedAt: row.UpdatedAt,
	}
}

// GetInvoicesSettings Get the invoice settings
// (GET /api/v1/invoices/settings)
func (s *server) GetInvoicesSettings(ctx context.Context, _ gen.GetInvoicesSettingsRequestObject) (gen.GetInvoicesSettingsResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	locked, err := anythingIssued(ctx, q)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSettings200JSONResponse(settingsResponse(row, locked)), nil
}

// errRefused stops a locked transaction whose rule refused the request: the
// transaction rolls back and the handler answers the refusal it recorded.
var errRefused = errors.New("invoices: refused")

// PutInvoicesSettings Change the invoice settings
// (PUT /api/v1/invoices/settings)
//
// The row is taken FOR UPDATE, so a replace waits behind an issue in flight
// (which holds it FOR SHARE) and then sees the counter row that issue made: a
// changed series start is refused from the first issue on, and the settings
// never show a start that was not used (D2).
func (s *server) PutInvoicesSettings(ctx context.Context, req gen.PutInvoicesSettingsRequestObject) (gen.PutInvoicesSettingsResponseObject, error) {
	parsed, errs := parseSettings(*req.Body)
	if len(errs) > 0 {
		return gen.PutInvoicesSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, errs)), nil
	}
	parsed.Now = s.deps.Clock()

	var refusal *gen.InvoicesConflictProblem
	var saved store.InvoicesSetting
	var locked bool
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		if current.Revision != req.Body.Revision {
			refusal = ptr(revisionConflict("Invoice settings", current.Revision, req.Body.Revision))
			return errRefused
		}
		locked, err = anythingIssued(ctx, txq)
		if err != nil {
			return err
		}
		if locked && parsed.SeriesStart != current.SeriesStart {
			refusal = ptr(conflict(codeSeriesLocked, seriesLockedTitle, fmt.Sprintf(
				"The series has started at %d and something is issued from it, so its start can no longer change.",
				current.SeriesStart)))
			return errRefused
		}
		saved, err = txq.UpdateSettings(ctx, parsed)
		if err != nil {
			return fmt.Errorf("invoices: change the settings: %w", err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PutInvoicesSettings409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesSettings200JSONResponse(settingsResponse(saved, locked)), nil
}

// ptr is a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }
```

**Create** `apps/server/internal/invoices/vatcodes.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the VAT codes (D3): the tenant's label, the SAF-T standard tax
// code and the UNCL5305 category of each, with the rate as dated periods. A
// rate change is a new period on the same code, never a new label every open
// draft must be re-coded to; a line carries only the code until it is issued,
// when the period covering the issue date decides its rate.

// The codes and titles of this file's 409s.
const (
	codeVatCodeInUse        = "vat_code_in_use"
	codeRateChangeInPast    = "rate_change_in_past"
	codeRatePeriodNotLatest = "rate_period_not_latest"
	codeRatePeriodLast      = "rate_period_last"
	codeRatePeriodInUse     = "rate_period_in_use"

	vatCodeInUseTitle     = "VAT code in use"
	rateChangeInPastTitle = "Rate change in the past"
	ratePeriodTitle       = "Rate period cannot be removed"
)

// vatCodeIndex is the unique index on lower(code).
const vatCodeIndex = "ux_vat_codes_code_lower"

// categories are UNCL5305's, the ones EHF Billing 3.0 Norway uses.
var categories = map[string]bool{"S": true, "Z": true, "E": true, "AE": true, "G": true, "O": true, "K": true}

var hundredPercent = big.NewRat(100, 1)

// vatCodeFields is a code's own fields, validated.
type vatCodeFields struct {
	code, name, safT, category string
	reason                     *string
}

// parseVatCodeFields runs D3's rules over a code's own fields, every failure
// collected into errs.
func parseVatCodeFields(code, name, safT, category string, reason *string, errs map[string][]string) (vatCodeFields, map[string][]string) {
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	f := vatCodeFields{
		code: strings.TrimSpace(code), name: strings.TrimSpace(name),
		safT: strings.TrimSpace(safT), category: strings.ToUpper(strings.TrimSpace(category)),
	}
	if reason != nil && strings.TrimSpace(*reason) != "" {
		f.reason = ptr(strings.TrimSpace(*reason))
	}
	if f.code == "" {
		add("code", "A VAT code needs a code")
	}
	add("code", maxLength("A code", f.code, 10))
	if f.name == "" {
		add("name", "A VAT code needs a name")
	}
	add("name", maxLength("A name", f.name, 100))
	if f.safT == "" {
		add("safTCode", "A VAT code needs its SAF-T standard tax code")
	}
	add("safTCode", maxLength("A SAF-T code", f.safT, 5))
	if !categories[f.category] {
		add("ehfCategory", "The category is one of S, Z, E, AE, G, O and K")
	}
	if f.reason != nil {
		add("exemptionReason", maxLength("An exemption reason", *f.reason, 200))
	}
	if f.category != "S" && categories[f.category] && f.reason == nil {
		add("exemptionReason", fmt.Sprintf("A code in category %s needs the exemption reason its documents print", f.category))
	}
	return f, errs
}

// parseRate is a rate for a code of category: greater than 0 and at most 100
// with at most two decimals for S, exactly 0 for every other category (D3).
// It answers the rate and the message to report on ratePercent.
func parseRate(category string, v float64) (*big.Rat, string) {
	if !finite(v) {
		return nil, "A rate is a number"
	}
	if decimalPlaces(v) > 2 {
		return nil, "A rate has at most two decimals"
	}
	rate := ratFromFloat(v)
	if category == "S" {
		if rate.Sign() <= 0 || rate.Cmp(hundredPercent) > 0 {
			return nil, "A standard-rated code's rate is greater than 0 and at most 100"
		}
		return rate, ""
	}
	if rate.Sign() != 0 {
		return nil, "Only a code in category S carries a rate; every other category's rate is 0"
	}
	return rate, ""
}

// vatCodeResponse renders one code with its periods.
func vatCodeResponse(c store.InvoicesVatCode, rates []store.InvoicesVatCodeRate, inUse bool) (gen.InvoicesVatCode, error) {
	out := gen.InvoicesVatCode{
		Id: c.ID, Code: c.Code, Name: c.Name, SafTCode: c.SafTCode, EhfCategory: c.EhfCategory,
		ExemptionReason: c.ExemptionReason, Active: c.Active, InUse: inUse, Revision: c.Revision,
		Rates: make([]gen.InvoicesVatCodeRate, 0, len(rates)),
	}
	for _, r := range rates {
		rate, err := floatFromNumeric(r.RatePercent)
		if err != nil {
			return gen.InvoicesVatCode{}, err
		}
		period := gen.InvoicesVatCodeRate{Id: r.ID, RatePercent: rate, ValidFrom: wireDate(r.ValidFrom.Time)}
		if r.ValidTo.Valid {
			period.ValidTo = ptr(wireDate(r.ValidTo.Time))
		}
		out.Rates = append(out.Rates, period)
	}
	return out, nil
}

// readVatCode renders one code as it stands now, on q.
func readVatCode(ctx context.Context, q *store.Queries, c store.InvoicesVatCode) (gen.InvoicesVatCode, error) {
	rates, err := q.VatCodeRates(ctx, c.ID)
	if err != nil {
		return gen.InvoicesVatCode{}, fmt.Errorf("invoices: read VAT code %d's rates: %w", c.ID, err)
	}
	inUse, err := q.VatCodeInUse(ctx, c.ID)
	if err != nil {
		return gen.InvoicesVatCode{}, fmt.Errorf("invoices: is VAT code %d in use: %w", c.ID, err)
	}
	return vatCodeResponse(c, rates, inUse)
}

// GetInvoicesVatCodes List the VAT codes
// (GET /api/v1/invoices/vat-codes)
func (s *server) GetInvoicesVatCodes(ctx context.Context, _ gen.GetInvoicesVatCodesRequestObject) (gen.GetInvoicesVatCodesResponseObject, error) {
	q := store.New(s.deps.Pool)
	codes, err := q.ListVatCodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the VAT codes: %w", err)
	}
	rates, err := q.ListVatCodeRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the VAT rates: %w", err)
	}
	used, err := q.VatCodesInUse(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the VAT codes in use: %w", err)
	}
	byCode := map[int32][]store.InvoicesVatCodeRate{}
	for _, r := range rates {
		byCode[r.VatCodeID] = append(byCode[r.VatCodeID], r)
	}
	inUse := map[int32]bool{}
	for _, id := range used {
		inUse[id] = true
	}
	out := make([]gen.InvoicesVatCode, 0, len(codes))
	for _, c := range codes {
		code, err := vatCodeResponse(c, byCode[c.ID], inUse[c.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return gen.GetInvoicesVatCodes200JSONResponse(out), nil
}

// PostInvoicesVatCodes Create a VAT code
// (POST /api/v1/invoices/vat-codes)
func (s *server) PostInvoicesVatCodes(ctx context.Context, req gen.PostInvoicesVatCodesRequestObject) (gen.PostInvoicesVatCodesResponseObject, error) {
	body := req.Body
	fields, errs := parseVatCodeFields(body.Code, body.Name, body.SafTCode, body.EhfCategory, body.ExemptionReason, nil)
	var rate *big.Rat
	if categories[fields.category] {
		var msg string
		if rate, msg = parseRate(fields.category, body.RatePercent); msg != "" {
			errs = withFieldError(errs, "ratePercent", msg)
		}
	}
	if len(errs) > 0 {
		return gen.PostInvoicesVatCodes400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle, errs)), nil
	}
	ratePercent, err := numericFromRat(rate, 2)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var created gen.InvoicesVatCode
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		code, err := txq.InsertVatCode(ctx, store.InsertVatCodeParams{
			Code: fields.code, Name: fields.name, SafTCode: fields.safT, EhfCategory: fields.category,
			ExemptionReason: fields.reason, Now: now,
		})
		if err != nil {
			return err
		}
		if _, err := txq.InsertVatCodeRate(ctx, store.InsertVatCodeRateParams{
			VatCodeID: code.ID, RatePercent: ratePercent, ValidFrom: pgDate(utcDay(body.ValidFrom.Time)), Now: now,
		}); err != nil {
			return fmt.Errorf("invoices: add VAT code %d's first rate: %w", code.ID, err)
		}
		created, err = readVatCode(ctx, txq, code)
		return err
	})
	if db.IsUniqueViolation(err, vatCodeIndex) {
		return gen.PostInvoicesVatCodes400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle,
			fieldError("code", fmt.Sprintf("A VAT code '%s' already exists", fields.code)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: create a VAT code: %w", err)
	}
	return gen.PostInvoicesVatCodes201JSONResponse(created), nil
}

// PutInvoicesVatCodesById Change a VAT code
// (PUT /api/v1/invoices/vat-codes/{id})
//
// A code in use keeps its category and SAF-T code (D3): what a line issued
// under it reported to the VAT return must not be re-labelled behind it. The
// row is taken FOR UPDATE, which also waits for any draft save adding a line
// on it, so the in-use check sees every committed line.
func (s *server) PutInvoicesVatCodesById(ctx context.Context, req gen.PutInvoicesVatCodesByIdRequestObject) (gen.PutInvoicesVatCodesByIdResponseObject, error) {
	body := req.Body
	fields, errs := parseVatCodeFields(body.Code, body.Name, body.SafTCode, body.EhfCategory, body.ExemptionReason, nil)
	if len(errs) > 0 {
		return gen.PutInvoicesVatCodesById400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle, errs)), nil
	}
	var refusal *gen.InvoicesConflictProblem
	var rateRefusal string
	notFound := false
	var saved gen.InvoicesVatCode
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		current, err := txq.LockVatCode(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock VAT code %d: %w", req.Id, err)
		}
		if current.Revision != body.Revision {
			refusal = ptr(revisionConflict("VAT code", current.Revision, body.Revision))
			return errRefused
		}
		if fields.category != current.EhfCategory || fields.safT != current.SafTCode {
			inUse, err := txq.VatCodeInUse(ctx, current.ID)
			if err != nil {
				return err
			}
			if inUse {
				refusal = ptr(conflict(codeVatCodeInUse, vatCodeInUseTitle,
					"Lines carry this code, so its category and SAF-T code can no longer change. Deactivate it and create a new one instead."))
				return errRefused
			}
		}
		if fields.category != current.EhfCategory {
			// A category change must still fit every period's rate: a code
			// cannot become S at 0 %, nor leave S with a rate.
			rates, err := txq.VatCodeRates(ctx, current.ID)
			if err != nil {
				return err
			}
			for _, r := range rates {
				rate, err := floatFromNumeric(r.RatePercent)
				if err != nil {
					return err
				}
				if _, msg := parseRate(fields.category, rate); msg != "" {
					rateRefusal = "The code's rates do not fit that category: " + msg
					return errRefused
				}
			}
		}
		updated, err := txq.UpdateVatCode(ctx, store.UpdateVatCodeParams{
			ID: current.ID, Code: fields.code, Name: fields.name, SafTCode: fields.safT,
			EhfCategory: fields.category, ExemptionReason: fields.reason, Active: body.Active, Now: s.deps.Clock(),
		})
		if err != nil {
			return err
		}
		saved, err = readVatCode(ctx, txq, updated)
		return err
	})
	switch {
	case notFound:
		return gen.PutInvoicesVatCodesById404Response{}, nil
	case refusal != nil:
		return gen.PutInvoicesVatCodesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	case rateRefusal != "":
		return gen.PutInvoicesVatCodesById400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle,
			fieldError("ehfCategory", rateRefusal))), nil
	case db.IsUniqueViolation(err, vatCodeIndex):
		return gen.PutInvoicesVatCodesById400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle,
			fieldError("code", fmt.Sprintf("A VAT code '%s' already exists", fields.code)))), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: change VAT code %d: %w", req.Id, err)
	}
	return gen.PutInvoicesVatCodesById200JSONResponse(saved), nil
}

// PostInvoicesVatCodesByIdRates Change a VAT code's rate from a date
// (POST /api/v1/invoices/vat-codes/{id}/rates)
//
// The rate-change rule (D3): the open period closes the day before validFrom
// and a new open period starts on it. validFrom must be after the open
// period's own start, and after the latest issue date of any issued document,
// so no issued line ever falls into a period that changed after it was issued.
// The transaction takes the settings row FOR UPDATE first — an issue in flight
// holds it FOR SHARE until it commits — so the latest issue date it reads is
// final.
func (s *server) PostInvoicesVatCodesByIdRates(ctx context.Context, req gen.PostInvoicesVatCodesByIdRatesRequestObject) (gen.PostInvoicesVatCodesByIdRatesResponseObject, error) {
	validFrom := utcDay(req.Body.ValidFrom.Time)
	var refusal *gen.InvoicesConflictProblem
	var errs map[string][]string
	notFound := false
	var saved gen.InvoicesVatCode
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if _, err := txq.LockSettings(ctx); err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		code, err := txq.LockVatCode(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock VAT code %d: %w", req.Id, err)
		}
		rate, msg := parseRate(code.EhfCategory, req.Body.RatePercent)
		if msg != "" {
			errs = fieldError("ratePercent", msg)
			return errRefused
		}
		rates, err := txq.VatCodeRates(ctx, code.ID)
		if err != nil {
			return err
		}
		open := rates[len(rates)-1]
		if !validFrom.After(open.ValidFrom.Time) {
			errs = fieldError("validFrom", fmt.Sprintf("A new rate takes effect after the current one's start, %s",
				open.ValidFrom.Time.Format(time.DateOnly)))
			return errRefused
		}
		latest, err := txq.LatestIssueDate(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the latest issue date: %w", err)
		}
		if latest.Valid && !validFrom.After(latest.Time) {
			refusal = ptr(conflict(codeRateChangeInPast, rateChangeInPastTitle, fmt.Sprintf(
				"A document is already issued on %s, so a rate change takes effect after that day.",
				latest.Time.Format(time.DateOnly))))
			return errRefused
		}
		ratePercent, err := numericFromRat(rate, 2)
		if err != nil {
			return err
		}
		if err := txq.CloseOpenVatCodeRate(ctx, store.CloseOpenVatCodeRateParams{
			VatCodeID: code.ID, ValidTo: pgDate(validFrom.AddDate(0, 0, -1)),
		}); err != nil {
			return fmt.Errorf("invoices: close VAT code %d's open rate: %w", code.ID, err)
		}
		if _, err := txq.InsertVatCodeRate(ctx, store.InsertVatCodeRateParams{
			VatCodeID: code.ID, RatePercent: ratePercent, ValidFrom: pgDate(validFrom), Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("invoices: add VAT code %d's rate: %w", code.ID, err)
		}
		saved, err = readVatCode(ctx, txq, code)
		return err
	})
	switch {
	case notFound:
		return gen.PostInvoicesVatCodesByIdRates404Response{}, nil
	case errs != nil:
		return gen.PostInvoicesVatCodesByIdRates400ApplicationProblemPlusJSONResponse(invalid(invalidRateTitle, errs)), nil
	case refusal != nil:
		return gen.PostInvoicesVatCodesByIdRates409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	return gen.PostInvoicesVatCodesByIdRates201JSONResponse(saved), nil
}

// DeleteInvoicesVatCodesByIdRatesByRateId Remove a VAT code's latest rate period
// (DELETE /api/v1/invoices/vat-codes/{id}/rates/{rateId})
//
// A mistaken future period is removed and the one before it reopened (D3). Only
// the latest may go, never the only one, and only while no issued document is
// dated on or after its start — under the same settings lock a rate change
// takes, for the same reason.
func (s *server) DeleteInvoicesVatCodesByIdRatesByRateId(ctx context.Context, req gen.DeleteInvoicesVatCodesByIdRatesByRateIdRequestObject) (gen.DeleteInvoicesVatCodesByIdRatesByRateIdResponseObject, error) {
	var refusal *gen.InvoicesConflictProblem
	notFound := false
	var saved gen.InvoicesVatCode
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if _, err := txq.LockSettings(ctx); err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		code, err := txq.LockVatCode(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock VAT code %d: %w", req.Id, err)
		}
		rates, err := txq.VatCodeRates(ctx, code.ID)
		if err != nil {
			return err
		}
		at := -1
		for i, r := range rates {
			if r.ID == req.RateId {
				at = i
			}
		}
		switch {
		case at < 0:
			notFound = true
			return errRefused
		case at != len(rates)-1:
			refusal = ptr(conflict(codeRatePeriodNotLatest, ratePeriodTitle,
				"Only the latest rate period can be removed."))
			return errRefused
		case len(rates) == 1:
			refusal = ptr(conflict(codeRatePeriodLast, ratePeriodTitle,
				"A VAT code always has a rate, so its only period cannot be removed."))
			return errRefused
		}
		latest, err := txq.LatestIssueDate(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the latest issue date: %w", err)
		}
		if latest.Valid && !latest.Time.Before(rates[at].ValidFrom.Time) {
			refusal = ptr(conflict(codeRatePeriodInUse, ratePeriodTitle, fmt.Sprintf(
				"A document is issued on %s, on or after this period's start, so the period stays.",
				latest.Time.Format(time.DateOnly))))
			return errRefused
		}
		if err := txq.DeleteVatCodeRate(ctx, rates[at].ID); err != nil {
			return fmt.Errorf("invoices: remove VAT rate %d: %w", rates[at].ID, err)
		}
		if err := txq.ReopenVatCodeRate(ctx, rates[at-1].ID); err != nil {
			return fmt.Errorf("invoices: reopen VAT rate %d: %w", rates[at-1].ID, err)
		}
		saved, err = readVatCode(ctx, txq, code)
		return err
	})
	switch {
	case notFound:
		return gen.DeleteInvoicesVatCodesByIdRatesByRateId404Response{}, nil
	case refusal != nil:
		return gen.DeleteInvoicesVatCodesByIdRatesByRateId409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	return gen.DeleteInvoicesVatCodesByIdRatesByRateId200JSONResponse(saved), nil
}
```

- [ ] **Step 4: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bun run gen:client
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go build ./... && mise exec -- go vet ./internal/invoices/
mise exec -- go test -count=1 ./internal/invoices/ ./internal/openapi/
mise exec -- golangci-lint run ./internal/invoices/...
cd ../..
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task2.txt <<'MSG'
feat(invoices): the seller record, the series start, and VAT codes with dated rates

The settings (invoices foundation design D2): the seller record with both
mod-11 checks, IBAN mod-97, BIC and "Only NOK in this phase", replaced whole
with its revision; the series start refused with series_locked once
anything is issued, the settings row taken FOR UPDATE so a replace waits
behind an issue in flight. The VAT codes (D3): a code and its first open
period, replaced with the in-use rule on category and SAF-T code, the
rate-change rule closing the open period the day before a new one and
refusing a change on or before the latest issue date, and removing the
latest future period.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/decimal.go' 'apps/server/internal/invoices/errors.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/queries/counters.sql' 'apps/server/internal/invoices/queries/settings.sql' 'apps/server/internal/invoices/queries/vatcodes.sql' 'apps/server/internal/invoices/server.go' 'apps/server/internal/invoices/settings.go' 'apps/server/internal/invoices/settings_internal_test.go' 'apps/server/internal/invoices/settings_test.go' 'apps/server/internal/invoices/store/counters.sql.go' 'apps/server/internal/invoices/store/settings.sql.go' 'apps/server/internal/invoices/store/vatcodes.sql.go' 'apps/server/internal/invoices/values.go' 'apps/server/internal/invoices/vatcodes.go' 'apps/server/internal/invoices/vatcodes_test.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git commit -F /tmp/claude-1000/msg-invoices-task2.txt -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/decimal.go' 'apps/server/internal/invoices/errors.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/queries/counters.sql' 'apps/server/internal/invoices/queries/settings.sql' 'apps/server/internal/invoices/queries/vatcodes.sql' 'apps/server/internal/invoices/server.go' 'apps/server/internal/invoices/settings.go' 'apps/server/internal/invoices/settings_internal_test.go' 'apps/server/internal/invoices/settings_test.go' 'apps/server/internal/invoices/store/counters.sql.go' 'apps/server/internal/invoices/store/settings.sql.go' 'apps/server/internal/invoices/store/vatcodes.sql.go' 'apps/server/internal/invoices/values.go' 'apps/server/internal/invoices/vatcodes.go' 'apps/server/internal/invoices/vatcodes_test.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 3: Drafts, the money and the list — and Status and MergedInto on the customers contract (D4, D5, D10's contract change)

The customers change comes first, inside this task: `contracts.CustomerBillingProfile` gains `Status` and `MergedInto`, the customers directory fills them from its billing-profile query, and its test proves both — a disabled and a merged-away customer included. Every other module's fake of `CustomerDirectory` compiles unchanged (the fields are new); the invoices fake models both. Then drafts: create (with the prefills), replace with revision, delete, the customer gates in their order on create and on every save, the delivery and money rules, `GET /invoices/{id}` and the list. `contractscalls.go` becomes the one door to the directory, and the harness fails any test that walks through it under a lock.

**Files:**
- Create: `apps/server/internal/invoices/contractscalls.go`, `apps/server/internal/invoices/drafts.go`, `apps/server/internal/invoices/drafts_test.go`, `apps/server/internal/invoices/export_test.go`, `apps/server/internal/invoices/issuedate.go`, `apps/server/internal/invoices/list.go`, `apps/server/internal/invoices/list_test.go`, `apps/server/internal/invoices/money.go`, `apps/server/internal/invoices/queries/invoices.sql`, `apps/server/internal/invoices/queries/lines.sql`, `apps/server/internal/invoices/responses.go`
- Modify: `apps/server/internal/contracts/directory.go`, `apps/server/internal/customers/directory.go`, `apps/server/internal/customers/directory_test.go`, `apps/server/internal/customers/queries/customers.sql`, `apps/server/internal/invoices/harness_test.go`, `apps/server/internal/invoices/main_test.go`, `apps/server/internal/invoices/queries/vatcodes.sql`, `apps/server/internal/invoices/server.go`, `openapi/invoices.yaml`
- Generated (commit them; never edit by hand): `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/internal/customers/store/customers.sql.go`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/invoices.sql.go`, `apps/server/internal/invoices/store/lines.sql.go`, `apps/server/internal/invoices/store/vatcodes.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/contracts/directory.go:61-174`, `apps/server/internal/customers/directory.go:119-239`, `customers/queries/customers.sql:587-611`, `customers/directory_test.go:1-60,327-360`, `apps/server/internal/expenses/{contractscalls.go,entries.go:1455-1510,money.go}`

**Interfaces:**
- Produces Go contract: `contracts.CustomerBillingProfile.Status string` (`active` | `disabled` | `archived`), `.MergedInto *int32`.
- Produces wire: `POST/GET /invoices`, `GET/PUT/DELETE /invoices/{id}`; `InvoicesInvoiceRequest`, `InvoicesLineRequest`, `InvoicesDeliveryAddress`, `InvoicesInvoiceResponse` (with `warnings`, `allowedIssueDates`, and the credit and snapshot fields later tasks fill), `InvoicesLine`, `InvoicesVatSummary`, `InvoicesBuyer`, `InvoicesSeller`, `InvoicesCreditNoteRef`, `InvoicesCreditsRef`, `InvoicesInvoiceListItem`, `PaginatedResponseOfInvoicesInvoiceListItem`.
- Produces Go: `customerGate(*contracts.CustomerBillingProfile) *gen.InvoicesConflictProblem` (codes `customer_merged` with `mergedInto`, `customer_archived`, `customer_blocked`, `customer_missing`); `invoiceIssued()`; `parseDraft`, `draftLine`, `draftInput`, `vatCodesOn`, `taxedLines`, `checkInvoiceLines`, `checkTotal`, `numerics`, `writeLines`, `saveDraft`; `computeLine`, `summarize`, `round2`, `ratFromNumeric`, `floatFromRat`; `allowedIssueDates(today, deliveryEnd, latest)`, `issuedLate(issueDate, deliveryEnd)`; `invoiceResponse`, `deliveryEndOf`, `buyerResponse`, `sellerResponse`; `customerProfile`, `customerEntries`; `callerID`, `inLockedTx`; test exports `InLockedTx`, `SetContractCallHook`.

- [ ] **Step 1: The customers contract change, tested in customers**

**Replace** in `apps/server/internal/contracts/directory.go`:

```go
	Name           string
	Type           string // "business" | "person"
	Archived       bool
	LegalCountry   string // "" when the customer has no legal identity
	LegalID        string
	LegalName      string
	// InvoiceAddress is the resolved invoice address (D3's rule: primary
	// invoice, else primary postal, else nil) — never a list, since a
	// consumer only ever needs the one address to print.
```

**with**:

```go
	Name           string
	Type           string // "business" | "person"
	Archived       bool
	// Status is the customer's own status — "active", "disabled" or
	// "archived" (an anonymised person is archived) — so one read answers every
	// gate an invoice keeps as well as its snapshot (invoices foundation design
	// D10). "disabled" means blocked for invoicing: Invoices refuses a new
	// invoice to it, never a credit note or a read.
	Status string
	// MergedInto is the customer this one was merged into, nil unless it was
	// merged away — the same answer CustomerEntry.MergedInto gives, so a
	// consumer holding a stale id learns where its references went.
	MergedInto   *int32
	LegalCountry string // "" when the customer has no legal identity
	LegalID      string
	LegalName    string
	// InvoiceAddress is the resolved invoice address (D3's rule: primary
	// invoice, else primary postal, else nil) — never a list, since a
	// consumer only ever needs the one address to print.
```

**Replace** in `apps/server/internal/contracts/directory.go`:

```go
//   - Archived customers still resolve, from every one of Customer,
//     Customers and BillingProfile. A consumer (a supply period, a past
//     invoice) can hold a long-lived reference to a customer that has since
//     been archived, and must still be able to show its name or invoice it
//     again; Archived tells the caller to decorate that reference, not that
//     the lookup failed.
//   - More than one candidate from ContactsByEmail is ambiguous. A contact's
//     email is not unique across customers, and the directory does not
//     guess which customer the caller means: it returns every candidate,
```

**with**:

```go
//   - Archived customers still resolve, from every one of Customer,
//     Customers and BillingProfile. A consumer (a supply period, a past
//     invoice) can hold a long-lived reference to a customer that has since
//     been archived, and must still be able to show its name — so a past
//     invoice can be shown and credited; Archived tells the caller to
//     decorate that reference, not that the lookup failed. Invoices refuses a
//     new invoice to an archived or disabled customer (the billing profile's
//     Status, invoices foundation design D10).
//   - More than one candidate from ContactsByEmail is ambiguous. A contact's
//     email is not unique across customers, and the directory does not
//     guess which customer the caller means: it returns every candidate,
```

**Replace** in `apps/server/internal/customers/queries/customers.sql`:

```sql
-- does everywhere else this module reads it), contact email and the eleven
-- billing columns GetCustomerBillingProfile itself selects, plus
-- customer_number and status — what resolveBillingProfile (directory.go)
-- needs to fill in every field of contracts.CustomerBillingProfile except
-- the resolved invoice address, which is DirectoryInvoiceAddress's own
-- query (queries/addresses.sql), a second round trip rather than a join:
-- at most one row either way, and a join would return no row at all for a
```

**with**:

```sql
-- does everywhere else this module reads it), contact email and the eleven
-- billing columns GetCustomerBillingProfile itself selects, plus
-- customer_number and status — what resolveBillingProfile (directory.go)
-- needs to fill in every field of contracts.CustomerBillingProfile (status
-- and merged_into_customer_id too, for the gates Invoices keeps, invoices
-- foundation design D10) except
-- the resolved invoice address, which is DirectoryInvoiceAddress's own
-- query (queries/addresses.sql), a second round trip rather than a join:
-- at most one row either way, and a join would return no row at all for a
```

**Replace** in `apps/server/internal/customers/queries/customers.sql`:

```sql
-- rather than a second round trip, since the group is this module's own table
-- and a customer in no group must still answer a row.
SELECT c.id, c.customer_number, c.name, c.type, c.status = 'archived' AS archived,
       c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type, c.email,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference, c.default_bill_rate,
```

**with**:

```sql
-- rather than a second round trip, since the group is this module's own table
-- and a customer in no group must still answer a row.
SELECT c.id, c.customer_number, c.name, c.type, c.status = 'archived' AS archived,
       c.status, c.merged_into_customer_id,
       c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type, c.email,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference, c.default_bill_rate,
```

**Replace** in `apps/server/internal/customers/directory.go`:

```go
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, defaultBillRate)
	identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)

	return resolveBillingProfile(row.ID, row.CustomerNumber, row.Name, row.Type, row.Archived,
		identity, row.Email, profile, row.GroupDefaultPaymentTermsDays, invoiceAddress), nil
}

// resolveBillingProfile is contracts.CustomerDirectory.BillingProfile's one
```

**with**:

```go
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, defaultBillRate)
	identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)

	resolved := resolveBillingProfile(row.ID, row.CustomerNumber, row.Name, row.Type, row.Archived,
		identity, row.Email, profile, row.GroupDefaultPaymentTermsDays, invoiceAddress)
	// The two facts every gate Invoices keeps reads (invoices foundation
	// design D10): nothing to resolve, so they pass straight through.
	resolved.Status = row.Status
	resolved.MergedInto = row.MergedIntoCustomerID
	return resolved, nil
}

// resolveBillingProfile is contracts.CustomerDirectory.BillingProfile's one
```

**Replace** in `apps/server/internal/customers/directory_test.go`:

```go

// TestDirectory_BillingProfile_ArchivedResolves proves an archived customer's
// billing profile still resolves, flagged — a past invoice can hold a
// reference to a customer archived since, and must still be able to invoice
// it again.
func TestDirectory_BillingProfile_ArchivedResolves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
```

**with**:

```go

// TestDirectory_BillingProfile_ArchivedResolves proves an archived customer's
// billing profile still resolves, flagged — a past invoice can hold a
// reference to a customer archived since, and must still be shown and
// credited.
func TestDirectory_BillingProfile_ArchivedResolves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
```

**Replace** in `apps/server/internal/customers/directory_test.go`:

```go
	}
	if got == nil || !got.Archived || got.Name != "Nedlagt Handel AS" {
		t.Errorf("BillingProfile = %+v, want an archived Nedlagt Handel AS", got)
	}
}

```

**with**:

```go
	}
	if got == nil || !got.Archived || got.Name != "Nedlagt Handel AS" {
		t.Errorf("BillingProfile = %+v, want an archived Nedlagt Handel AS", got)
	}
}

// TestDirectory_BillingProfile_CarriesStatusAndMergedInto proves the two
// fields invoices foundation design D10 adds to the contract: Status is the
// customer's own for an active, a disabled and an archived customer, and
// MergedInto names the survivor of a merged-away one and is nil otherwise — so
// one read answers every gate Invoices keeps.
func TestDirectory_BillingProfile_CarriesStatusAndMergedInto(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newDirectory(t, h)
	ctx := context.Background()
	for _, status := range []string{"active", "disabled", "archived"} {
		id := insertCustomer(t, h, "Status "+status+" AS", status)
		got, err := dir.BillingProfile(ctx, id)
		if err != nil {
			t.Fatalf("BillingProfile(%s): %v", status, err)
		}
		if got == nil || got.Status != status || got.MergedInto != nil {
			t.Errorf("BillingProfile of a %s customer = %+v, want Status %q and no MergedInto", status, got, status)
		}
	}

	survivor := insertCustomer(t, h, "Overlever AS", "active")
	absorbed := insertCustomer(t, h, "Slått sammen AS", "active")
	h.Exec(t, `UPDATE customers.customers SET status = 'archived', merged_into_customer_id = $1 WHERE id = $2`, survivor, absorbed)
	got, err := dir.BillingProfile(ctx, absorbed)
	if err != nil {
		t.Fatalf("BillingProfile(merged): %v", err)
	}
	if got == nil || got.Status != "archived" || got.MergedInto == nil || *got.MergedInto != survivor {
		t.Errorf("BillingProfile of a merged-away customer = %+v, want archived and MergedInto %d", got, survivor)
	}
}

```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 -run 'TestDirectory' ./internal/customers/ ; cd ../..
```
Break `resolved.Status = row.Status` and see `TestDirectory_BillingProfile_CarriesStatusAndMergedInto` fail; restore.

- [ ] **Step 2: The drafts' tests, the fake's fixtures and the locked-call hook**

**Replace** in `apps/server/internal/invoices/main_test.go`:

```go
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

```

**with**:

```go
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

```

**Replace** in `apps/server/internal/invoices/main_test.go`:

```go
var recorder = contracttest.NewForModule("invoices")

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
```

**with**:

```go
var recorder = contracttest.NewForModule("invoices")

func TestMain(m *testing.M) {
	// The whole suite's locking guarantee, installed once before any test
	// runs: every call this module makes to the directory or the object store
	// is reported here, and one made from inside a locked transaction is
	// recorded for the harness to fail on (newInvoicesHarness).
	invoices.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
	"bytes"
	"context"
	"io"
	"slices"
	"sync"
	"testing"

```

**with**:

```go
	"bytes"
	"context"
	"io"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
	if objects != nil {
		base = append(base, modtest.WithObjectStore(objects))
	}
	return &harness{Harness: modtest.New(t, append(base, opts...)...), customers: customers, objects: objects}
}

// fakeCustomers is contracts.CustomerDirectory over billing profiles a test
```

**with**:

```go
	if objects != nil {
		base = append(base, modtest.WithObjectStore(objects))
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), customers: customers, objects: objects}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its locked transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// lockedContractCalls is every call out of the module — to the customer
// directory, or to the object store — made from inside a transaction that
// holds locks (invoices.InLockedTx). The rule is that none ever is (D6, D7),
// and every harness checks it when its test ends. It is one recorder for the
// package because the hook is a package-level one (TestMain); each harness
// checks only what was recorded while it existed, and the stack beside each
// call names the path that made it.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !invoices.InLockedTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
}

func (l *lockedCalls) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *lockedCalls) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.calls) {
		return nil
	}
	return slices.Clone(l.calls[n:])
}

// fakeCustomers is contracts.CustomerDirectory over billing profiles a test
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

func newFakeCustomers() *fakeCustomers {
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{}}
}

func (f *fakeCustomers) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
```

**with**:

```go

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

// The customers the fake directory knows:
//   - customerAcme, a Norwegian business with an organisation number, an
//     invoice address in Oslo, a buyer reference and 30 days' terms;
//   - customerPerson, a private person with an address, invoiced in English;
//   - customerForeign, a Swedish business with no Norwegian number;
//   - customerNoTerms, a business that decided neither terms nor a reference;
//   - customerDisabled, customerArchived and customerMerged (merged into
//     Acme), the three gates;
//   - customerEuro, whose profile invoices in EUR;
//   - customerNoAddress, a person with no address at all.
const (
	customerAcme      = 2001
	customerPerson    = 2002
	customerForeign   = 2003
	customerNoTerms   = 2004
	customerDisabled  = 2005
	customerArchived  = 2006
	customerMerged    = 2007
	customerEuro      = 2008
	customerNoAddress = 2009
	customerUnknown   = 9999
)

func newFakeCustomers() *fakeCustomers {
	oslo := &contracts.CustomerAddressEntry{Line1: "Kundeveien 2", PostalCode: "0150", City: "Oslo", Country: "NO"}
	thirty := int32(30)
	merged := int32(customerAcme)
	business := func(id int32, number int64, name string) contracts.CustomerBillingProfile {
		return contracts.CustomerBillingProfile{
			ID: id, CustomerNumber: number, Name: name, Type: "business", Status: "active",
			LegalCountry: "NO", LegalID: "923609016", LegalName: name + " Norge", InvoiceAddress: oslo,
		}
	}
	acme := business(customerAcme, 10001, "Acme AS")
	acme.PaymentTermsDays, acme.BuyerReference, acme.PeppolID, acme.GLN = &thirty, "PO-77", "0192:923609016", "7080000000001"
	noTerms := business(customerNoTerms, 10004, "Uten Vilkår AS")
	noTerms.LegalName = ""
	disabled := business(customerDisabled, 10005, "Sperret AS")
	disabled.Status = "disabled"
	archived := business(customerArchived, 10006, "Arkivert AS")
	archived.Status, archived.Archived = "archived", true
	mergedAway := business(customerMerged, 10007, "Slått Sammen AS")
	mergedAway.Status, mergedAway.Archived, mergedAway.MergedInto = "archived", true, &merged
	euro := business(customerEuro, 10008, "Euro AS")
	euro.Currency = "EUR"
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{
		customerAcme: acme,
		customerPerson: {
			ID: customerPerson, CustomerNumber: 10002, Name: "Kari Nordmann", Type: "person", Status: "active",
			Language: "en", InvoiceAddress: &contracts.CustomerAddressEntry{Line1: "Hjemveien 5", PostalCode: "5003", City: "Bergen", Country: "NO"},
		},
		customerForeign: {
			ID: customerForeign, CustomerNumber: 10003, Name: "Svenska AB", Type: "business", Status: "active",
			LegalCountry: "SE", LegalID: "556677889901", LegalName: "Svenska Aktiebolaget AB", Language: "en",
			InvoiceAddress: &contracts.CustomerAddressEntry{Line1: "Storgatan 1", PostalCode: "111 22", City: "Stockholm", Country: "SE"},
		},
		customerNoTerms:  noTerms,
		customerDisabled: disabled,
		customerArchived: archived,
		customerMerged:   mergedAway,
		customerEuro:     euro,
		customerNoAddress: {
			ID: customerNoAddress, CustomerNumber: 10009, Name: "Uten Adresse", Type: "person", Status: "active",
		},
	}}
}

// setStatus moves a customer to status, as the customers module would.
func (f *fakeCustomers) setStatus(id int32, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[id]
	p.Status, p.Archived = status, status == "archived"
	f.profiles[id] = p
}

func (f *fakeCustomers) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: p.ID, Name: p.Name, Archived: p.Archived}, nil
}

func (f *fakeCustomers) Customers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
```

**with**:

```go
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: p.ID, Name: p.Name, Archived: p.Status == "archived", MergedInto: p.MergedInto}, nil
}

func (f *fakeCustomers) Customers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
```

**Create** `apps/server/internal/invoices/export_test.go`:

```go
package invoices

import "context"

// InLockedTx exposes inLockedTx to the external tests, whose contract-call
// hook uses it to tell a call made from inside one of this module's locked
// transactions from one made outside any.
var InLockedTx = inLockedTx

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go). The harness installs it once, from
// TestMain, before any test runs: a package-level hook written while other
// tests are making requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}
```

**Create** `apps/server/internal/invoices/drafts_test.go`:

```go
package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const invoicesPath = "/api/v1/invoices"

func invoicePath(id int64) string { return fmt.Sprintf("%s/%d", invoicesPath, id) }

type lineJSON struct {
	ID              int64    `json:"id"`
	Position        int32    `json:"position"`
	Description     string   `json:"description"`
	Quantity        float64  `json:"quantity"`
	Unit            string   `json:"unit"`
	UnitPrice       float64  `json:"unitPrice"`
	DiscountPercent float64  `json:"discountPercent"`
	VatCodeID       int32    `json:"vatCodeId"`
	CreditsLineID   *int64   `json:"creditsLineId"`
	LineGross       float64  `json:"lineGross"`
	LineAllowance   float64  `json:"lineAllowance"`
	LineNet         float64  `json:"lineNet"`
	VatRatePercent  *float64 `json:"vatRatePercent"`
	VatCategory     *string  `json:"vatCategory"`
	SafTCode        *string  `json:"safTCode"`
	ExemptionReason *string  `json:"exemptionReason"`
}

type summaryJSON struct {
	VatCategory     string  `json:"vatCategory"`
	RatePercent     float64 `json:"ratePercent"`
	SafTCode        string  `json:"safTCode"`
	ExemptionReason *string `json:"exemptionReason"`
	TaxableAmount   float64 `json:"taxableAmount"`
	VatAmount       float64 `json:"vatAmount"`
	VatAmountNok    float64 `json:"vatAmountNok"`
}

type invoiceJSON struct {
	ID                int64         `json:"id"`
	Kind              string        `json:"kind"`
	Status            string        `json:"status"`
	Number            *int64        `json:"number"`
	CustomerID        int32         `json:"customerId"`
	CustomerName      *string       `json:"customerName"`
	IssueDate         *string       `json:"issueDate"`
	DeliveryDate      *string       `json:"deliveryDate"`
	DeliveryFrom      *string       `json:"deliveryFrom"`
	DeliveryTo        *string       `json:"deliveryTo"`
	PaymentTermsDays  *int32        `json:"paymentTermsDays"`
	DueDate           *string       `json:"dueDate"`
	Currency          string        `json:"currency"`
	ExchangeRate      float64       `json:"exchangeRate"`
	ExchangeRateDate  *string       `json:"exchangeRateDate"`
	YourReference     string        `json:"yourReference"`
	OurReference      string        `json:"ourReference"`
	OrderReference    string        `json:"orderReference"`
	Note              string        `json:"note"`
	InternalNote      string        `json:"internalNote"`
	NetTotal          float64       `json:"netTotal"`
	VatTotal          float64       `json:"vatTotal"`
	GrossTotal        float64       `json:"grossTotal"`
	VatTotalNok       float64       `json:"vatTotalNok"`
	Lines             []lineJSON    `json:"lines"`
	VatSummaries      []summaryJSON `json:"vatSummaries"`
	Warnings          []string      `json:"warnings"`
	AllowedIssueDates []string      `json:"allowedIssueDates"`
	PdfStored         *bool         `json:"pdfStored"`
	Revision          int32         `json:"revision"`
	Buyer             *struct {
		CustomerNumber     int64   `json:"customerNumber"`
		Type               string  `json:"type"`
		Name               string  `json:"name"`
		OrganisationNumber *string `json:"organisationNumber"`
		ForeignID          *string `json:"foreignId"`
		AddressLine1       *string `json:"addressLine1"`
		PostalCode         *string `json:"postalCode"`
		City               *string `json:"city"`
		Country            *string `json:"country"`
		PeppolID           *string `json:"peppolId"`
		Gln                *string `json:"gln"`
		Language           string  `json:"language"`
	} `json:"buyer"`
	Seller *struct {
		LegalName            string `json:"legalName"`
		OrganisationNumber   string `json:"organisationNumber"`
		VatRegistered        bool   `json:"vatRegistered"`
		InForetaksregisteret bool   `json:"inForetaksregisteret"`
		BankAccount          string `json:"bankAccount"`
		FooterText           string `json:"footerText"`
	} `json:"seller"`
	CreditedAmount   *float64 `json:"creditedAmount"`
	UncreditedAmount *float64 `json:"uncreditedAmount"`
	CreditNotes      []struct {
		ID         int64   `json:"id"`
		Number     *int64  `json:"number"`
		IssueDate  *string `json:"issueDate"`
		GrossTotal float64 `json:"grossTotal"`
		Status     string  `json:"status"`
	} `json:"creditNotes"`
	Credits *struct {
		ID        int64  `json:"id"`
		Number    int64  `json:"number"`
		IssueDate string `json:"issueDate"`
	} `json:"credits"`
}

// line is one request line on code vatCodeID.
func line(description string, quantity, unitPrice float64, vatCodeID int32) map[string]any {
	return map[string]any{"description": description, "quantity": quantity, "unit": "timer", "unitPrice": unitPrice, "vatCodeId": vatCodeID}
}

// draftBody is a draft for customer with lines, delivered on 2026-09-10.
func draftBody(customer int32, lines ...map[string]any) map[string]any {
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{"customerId": customer, "deliveryDate": "2026-09-10", "lines": lines}
}

func creator(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:create")
}

// createDraft posts body and answers the draft, failing unless it was created.
func createDraft(t *testing.T, h *harness, body map[string]any) invoiceJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodPost, invoicesPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices = %d %s, want 201", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// getInvoice reads one document as an invoices:access holder.
func getInvoice(t *testing.T, h *harness, id int64) invoiceJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// A create fills what it was not told from the billing profile — the buyer
// reference and the terms — and falls back to the settings' terms; what the
// request says wins (D4).
func TestDrafts_CreateFillsWhatItWasNotTold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	acme := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))
	if acme.Kind != "invoice" || acme.Status != "draft" || acme.Number != nil || acme.IssueDate != nil {
		t.Errorf("draft = %+v, want an unnumbered invoice draft", acme)
	}
	if acme.YourReference != "PO-77" || acme.PaymentTermsDays == nil || *acme.PaymentTermsDays != 30 {
		t.Errorf("prefills = %q, %v; want the profile's PO-77 and 30 days", acme.YourReference, acme.PaymentTermsDays)
	}
	if acme.CustomerName == nil || *acme.CustomerName != "Acme AS" || acme.Currency != "NOK" || acme.ExchangeRate != 1 {
		t.Errorf("draft = name %v, %s at %v; want Acme AS in NOK at 1", acme.CustomerName, acme.Currency, acme.ExchangeRate)
	}
	if acme.Buyer != nil || acme.Seller != nil {
		t.Error("an invoice draft carries a snapshot; the issue writes them")
	}
	if read := getInvoice(t, h, acme.ID); read.YourReference != "PO-77" || len(read.Lines) != 1 || read.GrossTotal != 2500 ||
		!slices.Equal(read.AllowedIssueDates, []string{"2026-09-12"}) {
		t.Errorf("GET = %+v, want the draft as created, 2 × 1000 + 25 %%, issuable today", read)
	}

	none := createDraft(t, h, draftBody(customerNoTerms))
	if none.YourReference != "" || none.PaymentTermsDays == nil || *none.PaymentTermsDays != 14 {
		t.Errorf("prefills without a profile = %q, %v; want empty and the settings' 14", none.YourReference, none.PaymentTermsDays)
	}

	body := draftBody(customerAcme)
	body["yourReference"], body["paymentTermsDays"], body["ourReference"] = "", 10, "Ola Nordmann"
	told := createDraft(t, h, body)
	if told.YourReference != "" || *told.PaymentTermsDays != 10 || told.OurReference != "Ola Nordmann" {
		t.Errorf("told = %q, %v, %q; want what the request said", told.YourReference, *told.PaymentTermsDays, told.OurReference)
	}
}

// The customer gates, in their order, on create and on save (D4).
func TestDrafts_TheCustomerGates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	for _, g := range []struct {
		customer int32
		code     string
	}{
		{customerMerged, "customer_merged"},
		{customerArchived, "customer_archived"},
		{customerDisabled, "customer_blocked"},
		{customerUnknown, "customer_missing"},
	} {
		res := c.Do(http.MethodPost, invoicesPath, draftBody(g.customer))
		if res.Status != http.StatusConflict {
			t.Errorf("customer %d = %d %s, want 409 %s", g.customer, res.Status, res.Body, g.code)
			continue
		}
		p := problemOf(t, res)
		if p.Code != g.code {
			t.Errorf("customer %d = %s, want %s", g.customer, p.Code, g.code)
		}
		if g.code == "customer_merged" && (p.MergedInto == nil || *p.MergedInto != customerAcme) {
			t.Errorf("customer_merged mergedInto = %v, want %d", p.MergedInto, customerAcme)
		}
	}

	draft := createDraft(t, h, draftBody(customerAcme))
	h.customers.setStatus(customerAcme, "disabled")
	body := draftBody(customerAcme)
	body["paymentTermsDays"], body["revision"] = 30, draft.Revision
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "customer_blocked" {
		t.Errorf("saving for a customer disabled since = %d %s, want 409 customer_blocked", res.Status, res.Body)
	}
}

// A replace is whole and carries its revision; a delete takes the lines with
// it; an issued document answers invoice_issued to both (D4).
func TestDrafts_ReplaceAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 1000, vat25), line("Reise", 1, 500, vat25)))

	body := draftBody(customerAcme, line("Rådgivning", 3, 1200, vat25))
	body["paymentTermsDays"], body["revision"], body["note"] = 20, draft.Revision, "Takk"
	res := c.Do(http.MethodPut, invoicePath(draft.ID), body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	var replaced invoiceJSON
	res.JSON(&replaced)
	if len(replaced.Lines) != 1 || replaced.Lines[0].Description != "Rådgivning" || replaced.Note != "Takk" ||
		replaced.YourReference != "" || *replaced.PaymentTermsDays != 20 || replaced.Revision != draft.Revision+1 {
		t.Errorf("replaced = %+v, want the one new line, the note, the reference cleared, 20 days, the revision on", replaced)
	}

	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "" {
		t.Errorf("a stale revision = %d %s, want 409 without a code", res.Status, res.Body)
	}
	delete(body, "revision")
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusBadRequest {
		t.Errorf("a replace without a revision = %d, want 400", res.Status)
	}
	body["revision"] = replaced.Revision
	delete(body, "paymentTermsDays")
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["paymentTermsDays"]) == 0 {
		t.Errorf("a replace without terms = %d %s, want 400 on paymentTermsDays", res.Status, res.Body)
	}

	if res := c.Do(http.MethodDelete, invoicePath(draft.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, draft.ID); n != 0 {
		t.Errorf("lines after the delete = %d, want the cascade to take them", n)
	}
	if res := c.Do(http.MethodDelete, invoicePath(draft.ID), nil); res.Status != http.StatusNotFound {
		t.Errorf("DELETE again = %d, want 404", res.Status)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(draft.ID), nil); res.Status != http.StatusNotFound {
		t.Errorf("GET a deleted draft = %d, want 404", res.Status)
	}

	issued := plantIssuedDocument(t, h, 1, "2026-09-01")
	body["revision"] = 1
	body["paymentTermsDays"] = 14
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var send any
		if method == http.MethodPut {
			send = body
		}
		if res := c.Do(method, invoicePath(issued), send); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
			t.Errorf("%s an issued document = %d %s, want 409 invoice_issued", method, res.Status, res.Body)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, invoicesPath, draftBody(customerAcme)); res.Status != http.StatusForbidden {
		t.Errorf("POST without invoices:create = %d, want 403", res.Status)
	}
}

// Delivery is a day, a period with from on or before to, or — on a draft —
// nothing; the place of delivery is whole or absent (D4).
func TestDrafts_Delivery(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	period := draftBody(customerAcme)
	delete(period, "deliveryDate")
	period["deliveryFrom"], period["deliveryTo"] = "2026-08-01", "2026-08-31"
	if inv := createDraft(t, h, period); inv.DeliveryFrom == nil || *inv.DeliveryTo != "2026-08-31" || inv.DeliveryDate != nil {
		t.Errorf("a period = %+v", inv)
	}
	none := draftBody(customerAcme)
	delete(none, "deliveryDate")
	if inv := createDraft(t, h, none); inv.DeliveryDate != nil || inv.DeliveryFrom != nil {
		t.Errorf("no delivery = %+v, want none on a draft", inv)
	}
	for _, bad := range []struct {
		name  string
		edit  func(map[string]any)
		field string
	}{
		{"a day and a period", func(b map[string]any) { b["deliveryFrom"], b["deliveryTo"] = "2026-08-01", "2026-08-31" }, "deliveryDate"},
		{"a period without its end", func(b map[string]any) { delete(b, "deliveryDate"); b["deliveryFrom"] = "2026-08-01" }, "deliveryTo"},
		{"a period that ends before it starts", func(b map[string]any) {
			delete(b, "deliveryDate")
			b["deliveryFrom"], b["deliveryTo"] = "2026-08-31", "2026-08-01"
		}, "deliveryTo"},
		{"a place without a city", func(b map[string]any) {
			b["deliveryAddress"] = map[string]any{"line1": "Byggeplassen", "city": "", "country": "NO"}
		}, "deliveryAddress.city"},
	} {
		body := draftBody(customerAcme)
		bad.edit(body)
		if res := c.Do(http.MethodPost, invoicesPath, body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[bad.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", bad.name, res.Status, res.Body, bad.field)
		}
	}
}

// A draft warns — never refuses — when the customer invoices in another
// currency, and when its delivery is more than a month before today (D4).
func TestDrafts_Warnings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if inv := createDraft(t, h, draftBody(customerEuro)); !slices.Equal(inv.Warnings, []string{"customer_currency_differs"}) {
		t.Errorf("warnings for an EUR customer = %v, want customer_currency_differs", inv.Warnings)
	}
	late := draftBody(customerAcme)
	late["deliveryDate"] = "2026-08-11"
	if inv := createDraft(t, h, late); !slices.Equal(inv.Warnings, []string{"issued_late"}) {
		t.Errorf("warnings for a delivery on 2026-08-11 on 2026-09-12 = %v, want issued_late", inv.Warnings)
	}
	late["deliveryDate"] = "2026-08-12"
	if inv := createDraft(t, h, late); len(inv.Warnings) != 0 {
		t.Errorf("warnings for a delivery exactly a month back = %v, want none", inv.Warnings)
	}
}

// The money rules (D5): a float is read as the decimal it was written as;
// too many decimals, the line and total bounds and the 500-line cap are 400s,
// never a 500; a line is gross less allowance; VAT is per rate on the sum of
// the nets; only NOK.
func TestDrafts_TheMoney(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	// 0.1 × 3 is exactly 0.30.
	if inv := createDraft(t, h, draftBody(customerAcme, line("Småting", 3, 0.1, vat25))); inv.Lines[0].LineGross != 0.3 || inv.NetTotal != 0.3 {
		t.Errorf("3 × 0.1 = %v, want 0.30", inv.Lines[0].LineGross)
	}

	// Three lines of 33.33 at 25 %: per line the VAT would be 3 × 8.33 =
	// 24.99; per rate it is round(99.99 × 25 %) = 25.00, and so is the total.
	third := line("Tredjedel", 1, 33.33, vat25)
	perRate := createDraft(t, h, draftBody(customerAcme, third, third, third))
	if perRate.NetTotal != 99.99 || perRate.VatTotal != 25 || perRate.GrossTotal != 124.99 || perRate.VatTotalNok != 25 {
		t.Errorf("per rate = net %v vat %v gross %v, want 99.99, 25.00, 124.99", perRate.NetTotal, perRate.VatTotal, perRate.GrossTotal)
	}

	// A discount is an allowance of the rounded gross: 3 × 33.33 = 99.99, 10 %
	// of it 9.999 → 10.00, net 89.99.
	discounted := line("Rabattert", 3, 33.33, vat25)
	discounted["discountPercent"] = 10
	if inv := createDraft(t, h, draftBody(customerAcme, discounted)); inv.Lines[0].LineGross != 99.99 || inv.Lines[0].LineAllowance != 10 || inv.Lines[0].LineNet != 89.99 {
		t.Errorf("discounted line = %+v, want 99.99 − 10.00 = 89.99", inv.Lines[0])
	}

	// Three rates, a 0 % category with its own row, no øre rounding.
	mixed := createDraft(t, h, draftBody(customerAcme,
		line("Tjeneste", 1, 100.01, vat25), line("Mat", 2, 100, vat15), line("Fritatt", 1, 50.5, vatZero)))
	type row struct {
		category           string
		rate, taxable, vat float64
	}
	var rows []row
	for _, s := range mixed.VatSummaries {
		rows = append(rows, row{s.VatCategory, s.RatePercent, s.TaxableAmount, s.VatAmount})
	}
	if want := []row{{"S", 25, 100.01, 25}, {"S", 15, 200, 30}, {"Z", 0, 50.5, 0}}; !slices.Equal(rows, want) {
		t.Errorf("summaries = %+v, want %+v", rows, want)
	}
	if mixed.NetTotal != 350.51 || mixed.VatTotal != 55 || mixed.GrossTotal != 405.51 {
		t.Errorf("totals = %v, %v, %v, want 350.51, 55.00, 405.51", mixed.NetTotal, mixed.VatTotal, mixed.GrossTotal)
	}

	for _, bad := range []struct {
		name  string
		line  map[string]any
		field string
	}{
		{"four quantity decimals", map[string]any{"quantity": 1.2345}, "lines[0].quantity"},
		{"five unit price decimals", map[string]any{"unitPrice": 1.23456}, "lines[0].unitPrice"},
		{"three discount decimals", map[string]any{"discountPercent": 1.234}, "lines[0].discountPercent"},
		{"a zero quantity", map[string]any{"quantity": 0}, "lines[0].quantity"},
		{"a negative price", map[string]any{"unitPrice": -1}, "lines[0].unitPrice"},
		{"a discount over 100", map[string]any{"discountPercent": 101}, "lines[0].discountPercent"},
		{"a line amount over the bound", map[string]any{"quantity": 1000, "unitPrice": 1000000}, "lines[0].unitPrice"},
		{"no description", map[string]any{"description": "  "}, "lines[0].description"},
		{"an unknown VAT code", map[string]any{"vatCodeId": vatUnknownID}, "lines[0].vatCodeId"},
	} {
		l := line("Linje", 1, 100, vat25)
		for k, v := range bad.line {
			l[k] = v
		}
		res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, l))
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[bad.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", bad.name, res.Status, res.Body, bad.field)
		}
	}
	res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("Linje", 1, 100, vat25), line("Rabatt", 1, 100, vat25)))
	if res.Status != http.StatusCreated {
		t.Fatalf("two lines = %d", res.Status)
	}
	if p := problemOf(t, res); len(p.Errors["lines[1].unitPrice"]) != 0 {
		t.Errorf("a valid second line refused: %v", p.Errors)
	}

	// The document bound: 101 lines at the line bound, at 25 %.
	var big []map[string]any
	for range 101 {
		big = append(big, line("Stor", 1, 999999999.99, vat25))
	}
	if res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, big...)); res.Status != http.StatusBadRequest ||
		!slices.Equal(problemOf(t, res).Errors["lines"], []string{"The document total is too large"}) {
		t.Errorf("a total over the bound = %d %s, want 400 on lines", res.Status, res.Body)
	}
	var many []map[string]any
	for range 501 {
		many = append(many, line("Mange", 1, 1, vat25))
	}
	if res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, many...)); res.Status != http.StatusBadRequest ||
		!slices.Equal(problemOf(t, res).Errors["lines"], []string{"At most 500 lines"}) {
		t.Errorf("501 lines = %d %s, want 400 \"At most 500 lines\"", res.Status, res.Body)
	}

	// NOK only, on create and on save.
	eur := draftBody(customerAcme)
	eur["currency"] = "EUR"
	if res := c.Do(http.MethodPost, invoicesPath, eur); res.Status != http.StatusBadRequest ||
		!slices.Equal(problemOf(t, res).Errors["currency"], []string{"Only NOK in this phase"}) {
		t.Errorf("EUR on create = %d %s, want 400 on currency", res.Status, res.Body)
	}
	draft := createDraft(t, h, draftBody(customerAcme))
	eur["revision"], eur["paymentTermsDays"] = draft.Revision, 14
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), eur); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["currency"]) == 0 {
		t.Errorf("EUR on save = %d %s, want 400 on currency", res.Status, res.Body)
	}
}

// A deactivated code is refused on a new line; a draft may have no lines
// (the issue refuses that).
func TestDrafts_AnInactiveCodeAndNoLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat15)

	res := creator(t, h).Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("Mat", 1, 100, vat15)))
	if res.Status != http.StatusBadRequest || !strings.Contains(strings.Join(problemOf(t, res).Errors["lines[0].vatCodeId"], " "), "no longer offered") {
		t.Errorf("an inactive code = %d %s, want 400 on lines[0].vatCodeId", res.Status, res.Body)
	}
	if inv := createDraft(t, h, draftBody(customerAcme)); len(inv.Lines) != 0 || inv.GrossTotal != 0 {
		t.Errorf("an empty draft = %+v", inv)
	}
}
```

**Create** `apps/server/internal/invoices/list_test.go`:

```go
package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

type listJSON struct {
	Data []struct {
		ID           int64   `json:"id"`
		Kind         string  `json:"kind"`
		Status       string  `json:"status"`
		Number       *int64  `json:"number"`
		CustomerID   int32   `json:"customerId"`
		CustomerName *string `json:"customerName"`
		IssueDate    *string `json:"issueDate"`
		GrossTotal   float64 `json:"grossTotal"`
	} `json:"data"`
	Pagination struct {
		Page        int32 `json:"page"`
		PageSize    int32 `json:"pageSize"`
		TotalCount  int32 `json:"totalCount"`
		HasNextPage bool  `json:"hasNextPage"`
	} `json:"pagination"`
}

func list(t *testing.T, h *harness, query string) listJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicesPath+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices%s = %d %s", query, res.Status, res.Body)
	}
	var l listJSON
	res.JSON(&l)
	return l
}

func ids(l listJSON) []int64 {
	var out []int64
	for _, d := range l.Data {
		out = append(out, d.ID)
	}
	return out
}

// plantIssuedFor writes an issued document for customer with the buyer name it
// was issued to.
func plantIssuedFor(t *testing.T, h *harness, number int64, issueDate string, customer int32, buyer, kind string) int64 {
	t.Helper()
	var credits *int64
	if kind == "credit_note" {
		credits = ptr(plantIssuedFor(t, h, number+100000, issueDate, customer, buyer, "invoice"))
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ($1, 'issued', $2, $3, $4, $5::date, $6, 100, now(), $7, now(), now())
		RETURNING id`, kind, number, customer, credits, issueDate, buyer, uuid.New())
}

func ptr[T any](v T) *T { return &v }

// Paging is every module list's: {data, pagination}, 25 by default and at
// most 100, drafts first and then by number descending, stable across pages.
func TestList_PagingAndOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var issued []int64
	for n := int64(1); n <= 30; n++ {
		issued = append(issued, plantIssuedFor(t, h, n, "2026-09-01", customerAcme, "Acme AS", "invoice"))
	}
	firstDraft := createDraft(t, h, draftBody(customerAcme)).ID
	secondDraft := createDraft(t, h, draftBody(customerPerson)).ID

	page1 := list(t, h, "")
	if len(page1.Data) != 25 || page1.Pagination.TotalCount != 32 || !page1.Pagination.HasNextPage || page1.Pagination.PageSize != 25 {
		t.Fatalf("page 1 = %d rows, %+v; want 25 of 32", len(page1.Data), page1.Pagination)
	}
	if got := ids(page1)[:3]; !slices.Equal(got, []int64{secondDraft, firstDraft, issued[29]}) {
		t.Errorf("first rows = %v, want the drafts newest first, then number 30", got)
	}
	if page1.Data[0].CustomerName == nil || *page1.Data[0].CustomerName != "Kari Nordmann" {
		t.Errorf("a draft's name = %v, want the customer's current name", page1.Data[0].CustomerName)
	}
	page2 := list(t, h, "?page=2")
	want := []int64{}
	for i := 6; i >= 0; i-- {
		want = append(want, issued[i])
	}
	if !slices.Equal(ids(page2), want) {
		t.Errorf("page 2 = %v, want numbers 7..1", ids(page2))
	}
	if all := list(t, h, "?pageSize=100"); len(all.Data) != 32 {
		t.Errorf("pageSize 100 = %d rows", len(all.Data))
	}
	for _, bad := range []string{"?pageSize=101", "?page=0", "?status=paid", "?kind=receipt", "?from=2026-09-02&to=2026-09-01"} {
		if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicesPath+bad, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET /invoices%s = %d, want 400", bad, res.Status)
		}
	}
}

// Every filter, and search by number and by buyer name — a draft is found by
// its customer, not by search.
func TestList_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	early := plantIssuedFor(t, h, 1, "2026-08-15", customerAcme, "Acme AS", "invoice")
	late := plantIssuedFor(t, h, 2, "2026-09-05", customerPerson, "Kari Nordmann", "invoice")
	credit := plantIssuedFor(t, h, 3, "2026-09-06", customerAcme, "Acme AS", "credit_note")
	draft := createDraft(t, h, draftBody(customerPerson)).ID

	for _, c := range []struct {
		query string
		want  []int64
	}{
		{"?status=draft", []int64{draft}},
		{"?status=issued&kind=credit_note", []int64{credit}},
		{fmt.Sprintf("?customerId=%d", customerPerson), []int64{draft, late}},
		{"?from=2026-09-01&to=2026-09-05", []int64{late}},
		{"?search=2", []int64{late}},
		{"?search=kari", []int64{late}},
		{"?search=ACME&kind=invoice&to=2026-08-31", []int64{early}},
		{"?search=100%25", []int64{}},
	} {
		got := ids(list(t, h, c.query))
		if got == nil {
			got = []int64{}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("GET /invoices%s = %v, want %v", c.query, got, c.want)
		}
	}
}
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 ./internal/invoices/ ; cd ../..   # FAIL: undefined invoices.InLockedTx
```

- [ ] **Step 3: The contract and the queries**

**Insert** into `openapi/invoices.yaml`, immediately before the line `info:`:

```yaml
        InvoicesBuyer:
            description: The buyer snapshot (D4), written at issue from the customer's billing profile and printed from, never re-read. A credit note carries its original's.
            properties:
                addressLine1:
                    type: string
                addressLine2:
                    type: string
                city:
                    type: string
                country:
                    type: string
                customerNumber:
                    format: int64
                    type: integer
                foreignId:
                    description: A foreign business's legal id prefixed with its country (SE556677889901), printed under "VAT/Reg. no.". Never a person's.
                    type: string
                gln:
                    type: string
                language:
                    description: The document's language, en or nb.
                    type: string
                name:
                    type: string
                organisationNumber:
                    description: A Norwegian business's organisation number.
                    type: string
                peppolId:
                    type: string
                postalCode:
                    type: string
                region:
                    type: string
                type:
                    description: business or person.
                    type: string
            required:
                - customerNumber
                - type
                - name
                - language
            type: object
        InvoicesCreditNoteRef:
            description: One credit note of an invoice, a draft (number absent) or issued.
            properties:
                grossTotal:
                    format: double
                    type: number
                id:
                    format: int64
                    type: integer
                issueDate:
                    format: date
                    type: string
                number:
                    format: int64
                    type: integer
                status:
                    type: string
            required:
                - id
                - grossTotal
                - status
            type: object
        InvoicesCreditsRef:
            description: The issued invoice a credit note credits.
            properties:
                id:
                    format: int64
                    type: integer
                issueDate:
                    format: date
                    type: string
                number:
                    format: int64
                    type: integer
            required:
                - id
                - number
                - issueDate
            type: object
        InvoicesDeliveryAddress:
            description: The place of delivery (§ 5-1-1 nr. 4), when it is not the buyer's address. line1, city and country are required; country is ISO 3166-1 alpha-2.
            properties:
                city:
                    type: string
                country:
                    type: string
                line1:
                    type: string
                line2:
                    type: string
                postalCode:
                    type: string
            required:
                - line1
                - city
                - country
            type: object
        InvoicesInvoiceListItem:
            description: One document in the list. customerName is the buyer snapshot's name on an issued document and the customer's current name on a draft, absent when the directory no longer knows it.
            properties:
                creditsInvoiceId:
                    format: int64
                    type: integer
                currency:
                    type: string
                customerId:
                    format: int32
                    type: integer
                customerName:
                    type: string
                dueDate:
                    format: date
                    type: string
                grossTotal:
                    format: double
                    type: number
                id:
                    format: int64
                    type: integer
                issueDate:
                    format: date
                    type: string
                kind:
                    type: string
                number:
                    format: int64
                    type: integer
                status:
                    type: string
            required:
                - id
                - kind
                - status
                - customerId
                - currency
                - grossTotal
            type: object
        InvoicesInvoiceRequest:
            description: 'A draft, created (POST) or replaced whole (PUT, with revision) (D4). customerId is the buyer. currency, when given, is NOK — "Only NOK in this phase". Delivery is deliveryDate alone, deliveryFrom with deliveryTo (from on or before to), or none — none only on a draft; the issue refuses it. references are at most 100 characters each, note and internalNote 1000. lines are at most 500. On create, an omitted yourReference is the billing profile''s buyerReference and an omitted paymentTermsDays the profile''s terms, else the settings'' default; on PUT an invoice''s paymentTermsDays is required and an omitted reference is cleared. On a credit-note draft only what D8 allows may change: lines may be removed, a quantity or a unit price lowered, and a description, the note and the internal note edited.'
            properties:
                currency:
                    type: string
                customerId:
                    format: int32
                    type: integer
                deliveryAddress:
                    $ref: '#/components/schemas/InvoicesDeliveryAddress'
                deliveryDate:
                    format: date
                    type: string
                deliveryFrom:
                    format: date
                    type: string
                deliveryTo:
                    format: date
                    type: string
                internalNote:
                    type: string
                lines:
                    items:
                        $ref: '#/components/schemas/InvoicesLineRequest'
                    type: array
                note:
                    type: string
                orderReference:
                    type: string
                ourReference:
                    type: string
                paymentTermsDays:
                    format: int32
                    type: integer
                revision:
                    description: Required on PUT; the revision the caller read. A stale one is a 409 naming both.
                    format: int32
                    type: integer
                yourReference:
                    type: string
            required:
                - customerId
                - lines
            type: object
        InvoicesInvoiceResponse:
            description: 'One document (D4): every column in camelCase, its lines and its VAT summaries. On a draft the VAT summaries and totals are computed with the rates in force today — the issue resolves them again for the issue date — and allowedIssueDates lists the dates it may be issued with today. warnings are never refusals: customer_currency_differs, issued_late, credit_exceeds_invoice, credit_exceeds_line. An invoice carries creditedAmount (its issued credit notes'' gross), uncreditedAmount and creditNotes; a credit note carries credits.'
            properties:
                allowedIssueDates:
                    items:
                        format: date
                        type: string
                    type: array
                buyer:
                    $ref: '#/components/schemas/InvoicesBuyer'
                createdAt:
                    format: date-time
                    type: string
                creditNotes:
                    items:
                        $ref: '#/components/schemas/InvoicesCreditNoteRef'
                    type: array
                creditedAmount:
                    format: double
                    type: number
                credits:
                    $ref: '#/components/schemas/InvoicesCreditsRef'
                currency:
                    type: string
                customerId:
                    format: int32
                    type: integer
                customerName:
                    type: string
                deliveryAddress:
                    $ref: '#/components/schemas/InvoicesDeliveryAddress'
                deliveryDate:
                    format: date
                    type: string
                deliveryFrom:
                    format: date
                    type: string
                deliveryTo:
                    format: date
                    type: string
                dueDate:
                    format: date
                    type: string
                exchangeRate:
                    format: double
                    type: number
                exchangeRateDate:
                    format: date
                    type: string
                grossTotal:
                    format: double
                    type: number
                id:
                    format: int64
                    type: integer
                internalNote:
                    type: string
                issueDate:
                    format: date
                    type: string
                issuedAt:
                    format: date-time
                    type: string
                issuedByUserId:
                    format: uuid
                    type: string
                kind:
                    description: invoice or credit_note.
                    type: string
                lines:
                    items:
                        $ref: '#/components/schemas/InvoicesLine'
                    type: array
                netTotal:
                    format: double
                    type: number
                note:
                    type: string
                number:
                    format: int64
                    type: integer
                orderReference:
                    type: string
                ourReference:
                    type: string
                paymentTermsDays:
                    format: int32
                    type: integer
                pdfStored:
                    description: On an issued document, whether its PDF is stored. False only when storing it after the issue failed; the next download stores it.
                    type: boolean
                revision:
                    format: int32
                    type: integer
                seller:
                    $ref: '#/components/schemas/InvoicesSeller'
                status:
                    description: draft or issued.
                    type: string
                uncreditedAmount:
                    format: double
                    type: number
                updatedAt:
                    format: date-time
                    type: string
                vatSummaries:
                    items:
                        $ref: '#/components/schemas/InvoicesVatSummary'
                    type: array
                vatTotal:
                    format: double
                    type: number
                vatTotalNok:
                    format: double
                    type: number
                warnings:
                    items:
                        type: string
                    type: array
                yourReference:
                    type: string
            required:
                - id
                - kind
                - status
                - customerId
                - currency
                - exchangeRate
                - yourReference
                - ourReference
                - orderReference
                - note
                - internalNote
                - netTotal
                - vatTotal
                - grossTotal
                - vatTotalNok
                - lines
                - vatSummaries
                - warnings
                - createdAt
                - updatedAt
                - revision
            type: object
        InvoicesLine:
            description: One line (D4, D5). lineGross is quantity × unitPrice rounded to øre, lineAllowance the discount of it rounded, lineNet their difference. The VAT fields are the issue snapshot, absent on a draft.
            properties:
                creditsLineId:
                    description: On a credit note's line, the original line it credits.
                    format: int64
                    type: integer
                description:
                    type: string
                discountPercent:
                    format: double
                    type: number
                exemptionReason:
                    type: string
                id:
                    format: int64
                    type: integer
                lineAllowance:
                    format: double
                    type: number
                lineGross:
                    format: double
                    type: number
                lineNet:
                    format: double
                    type: number
                position:
                    format: int32
                    type: integer
                quantity:
                    format: double
                    type: number
                safTCode:
                    type: string
                unit:
                    type: string
                unitPrice:
                    format: double
                    type: number
                vatCategory:
                    type: string
                vatCodeId:
                    format: int32
                    type: integer
                vatRatePercent:
                    format: double
                    type: number
            required:
                - id
                - position
                - description
                - quantity
                - unit
                - unitPrice
                - discountPercent
                - vatCodeId
                - lineGross
                - lineAllowance
                - lineNet
            type: object
        InvoicesLineRequest:
            description: One line of a draft. description is 1-500 characters; quantity greater than 0 with at most 3 decimals; unitPrice 0 or more with at most 4; discountPercent 0-100 with at most 2 (0 when omitted); unit at most 20. vatCodeId is an active code. creditsLineId is a credit-note draft's own and names the original line the line credits.
            properties:
                creditsLineId:
                    format: int64
                    type: integer
                description:
                    type: string
                discountPercent:
                    format: double
                    type: number
                quantity:
                    format: double
                    type: number
                unit:
                    type: string
                unitPrice:
                    format: double
                    type: number
                vatCodeId:
                    format: int32
                    type: integer
            required:
                - description
                - quantity
                - unitPrice
                - vatCodeId
            type: object
        InvoicesSeller:
            description: The seller snapshot (D4), copied from the settings at issue.
            properties:
                addressLine1:
                    type: string
                addressLine2:
                    type: string
                bankAccount:
                    type: string
                bic:
                    type: string
                city:
                    type: string
                country:
                    type: string
                email:
                    type: string
                footerText:
                    type: string
                iban:
                    type: string
                inForetaksregisteret:
                    type: boolean
                legalName:
                    type: string
                organisationNumber:
                    type: string
                postalCode:
                    type: string
                vatRegistered:
                    type: boolean
            required:
                - legalName
                - organisationNumber
                - vatRegistered
                - inForetaksregisteret
                - addressLine1
                - addressLine2
                - postalCode
                - city
                - country
                - bankAccount
                - iban
                - bic
                - email
                - footerText
            type: object
        InvoicesVatSummary:
            description: VAT per (category, rate) of a document (D5), computed on the sum of the lines' nets, a row for each 0 % category too.
            properties:
                exemptionReason:
                    type: string
                ratePercent:
                    format: double
                    type: number
                safTCode:
                    type: string
                taxableAmount:
                    format: double
                    type: number
                vatAmount:
                    format: double
                    type: number
                vatAmountNok:
                    format: double
                    type: number
                vatCategory:
                    type: string
            required:
                - vatCategory
                - ratePercent
                - safTCode
                - taxableAmount
                - vatAmount
                - vatAmountNok
            type: object
        PaginatedResponseOfInvoicesInvoiceListItem:
            properties:
                data:
                    items:
                        $ref: '#/components/schemas/InvoicesInvoiceListItem'
                    type: array
                pagination:
                    $ref: common.yaml#/components/schemas/PaginationMetadata
            required:
                - data
                - pagination
            type: object
```

**Insert** into `openapi/invoices.yaml`, immediately before the line `servers:`:

```yaml
    /api/v1/invoices:
        get:
            description: 'Invoices and credit notes, drafts first and then by number descending (D4). status is draft or issued, kind invoice or credit_note; from and to are issue dates, inclusive; search matches the number exactly when it is digits, or the buyer''s name ignoring case — a draft has no buyer snapshot, so it is found by customerId, not search. page and pageSize are the codebase''s paging: 25 by default, at most 100.'
            operationId: getInvoices
            parameters:
                - in: query
                  name: status
                  schema:
                    type: string
                - in: query
                  name: kind
                  schema:
                    type: string
                - in: query
                  name: customerId
                  schema:
                    format: int32
                    type: integer
                - in: query
                  name: search
                  schema:
                    type: string
                - in: query
                  name: from
                  schema:
                    format: date
                    type: string
                - in: query
                  name: to
                  schema:
                    format: date
                    type: string
                - in: query
                  name: page
                  schema:
                    format: int32
                    type: integer
                - in: query
                  name: pageSize
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/PaginatedResponseOfInvoicesInvoiceListItem'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Bad Request — paging out of range, an unknown status or kind, or from after to.
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
            summary: List invoices and credit notes
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
        post:
            description: Creates an invoice draft (D4). The buyer's billing profile is read before anything is written, for the prefills and the customer gates.
            operationId: postInvoices
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/InvoicesInvoiceRequest'
                required: true
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesInvoiceResponse'
                    description: Created
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — a field did not pass; the errors name it.
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
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — customer_merged (with mergedInto), customer_archived, customer_blocked or customer_missing.
            summary: Create an invoice draft
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:create
    /api/v1/invoices/{id}:
        delete:
            description: Deletes a draft, its lines with it. An issued document is never deleted (409 invoice_issued).
            operationId: deleteInvoicesById
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            responses:
                "204":
                    description: No Content
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
                    description: Not Found — no document has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — invoice_issued.
            summary: Delete a draft
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:create
        get:
            description: One document with its lines, VAT summaries, warnings and credit links.
            operationId: getInvoicesById
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesInvoiceResponse'
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
                    description: Not Found — no document has that id.
            summary: Get an invoice or a credit note
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
        put:
            description: Replaces a draft whole, with revision (D4). Only a draft is edited (409 invoice_issued otherwise).
            operationId: putInvoicesById
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/InvoicesInvoiceRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesInvoiceResponse'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — a field did not pass; the errors name it.
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
                    description: Not Found — no document has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — invoice_issued, a customer gate, or a stale revision.
            summary: Replace a draft
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:create
```

**Create** `apps/server/internal/invoices/queries/invoices.sql`:

```sql
-- name: GetInvoice :one
SELECT * FROM invoices.invoices WHERE id = @id;

-- name: LockInvoice :one
-- LockInvoice takes a document FOR UPDATE: a draft save, a delete and an issue
-- hold it to their commit. It is always the first lock an issue takes (D6).
SELECT * FROM invoices.invoices WHERE id = @id FOR UPDATE;

-- name: InsertInvoiceDraft :one
-- InsertInvoiceDraft writes an invoice draft (D4). It has no number, no issue
-- date and no snapshots; its totals are the ones computed with today's rates,
-- which the issue computes again for the issue date.
INSERT INTO invoices.invoices (
    kind, customer_id, delivery_date, delivery_from, delivery_to,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city, delivery_country,
    payment_terms_days, currency, your_reference, our_reference, order_reference, note, internal_note,
    net_total, vat_total, gross_total, vat_total_nok, created_by_user_id, created_at, updated_at
) VALUES (
    'invoice', @customer_id, @delivery_date, @delivery_from, @delivery_to,
    @delivery_address_line1, @delivery_address_line2, @delivery_postal_code, @delivery_city, @delivery_country,
    @payment_terms_days, @currency, @your_reference, @our_reference, @order_reference, @note, @internal_note,
    @net_total, @vat_total, @gross_total, @vat_total_nok, @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateDraft :one
-- UpdateDraft replaces a draft's own fields and moves its revision on. The
-- caller holds the row (LockInvoice) and has checked it is still a draft.
UPDATE invoices.invoices SET
    customer_id = @customer_id,
    delivery_date = @delivery_date,
    delivery_from = @delivery_from,
    delivery_to = @delivery_to,
    delivery_address_line1 = @delivery_address_line1,
    delivery_address_line2 = @delivery_address_line2,
    delivery_postal_code = @delivery_postal_code,
    delivery_city = @delivery_city,
    delivery_country = @delivery_country,
    payment_terms_days = @payment_terms_days,
    your_reference = @your_reference,
    our_reference = @our_reference,
    order_reference = @order_reference,
    note = @note,
    internal_note = @internal_note,
    net_total = @net_total,
    vat_total = @vat_total,
    gross_total = @gross_total,
    vat_total_nok = @vat_total_nok,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id AND status = 'draft'
RETURNING *;

-- name: DeleteDraft :execrows
-- DeleteDraft removes a draft and, by the cascade, its lines. An issued
-- document is never matched; the trigger would refuse it anyway (D9).
DELETE FROM invoices.invoices WHERE id = @id AND status = 'draft';

-- name: ListInvoices :many
-- ListInvoices is one page of GET /invoices (D4): drafts first, then by number
-- descending, the id breaking ties so a page never shifts under a reader.
-- search is a number (exact) or a buyer-name pattern; a draft has no buyer
-- snapshot and is found through customer_id instead.
SELECT * FROM invoices.invoices
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR customer_id = sqlc.narg(customer_id)::int)
  AND ((sqlc.narg(search_number)::bigint IS NULL AND sqlc.narg(search_pattern)::text IS NULL)
       OR number = sqlc.narg(search_number)::bigint
       OR buyer_name ILIKE sqlc.narg(search_pattern)::text)
  AND (sqlc.narg(issued_from)::date IS NULL OR issue_date >= sqlc.narg(issued_from)::date)
  AND (sqlc.narg(issued_to)::date IS NULL OR issue_date <= sqlc.narg(issued_to)::date)
ORDER BY number DESC NULLS FIRST, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: CountInvoices :one
-- CountInvoices is ListInvoices' total, over the same filters.
SELECT count(*)::int FROM invoices.invoices
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(customer_id)::int IS NULL OR customer_id = sqlc.narg(customer_id)::int)
  AND ((sqlc.narg(search_number)::bigint IS NULL AND sqlc.narg(search_pattern)::text IS NULL)
       OR number = sqlc.narg(search_number)::bigint
       OR buyer_name ILIKE sqlc.narg(search_pattern)::text)
  AND (sqlc.narg(issued_from)::date IS NULL OR issue_date >= sqlc.narg(issued_from)::date)
  AND (sqlc.narg(issued_to)::date IS NULL OR issue_date <= sqlc.narg(issued_to)::date);
```

**Create** `apps/server/internal/invoices/queries/lines.sql`:

```sql
-- name: Lines :many
-- Lines is one document's lines, in their order.
SELECT * FROM invoices.lines WHERE invoice_id = @invoice_id ORDER BY position;

-- name: DeleteLines :exec
-- DeleteLines clears a draft's lines before a replace writes them again. The
-- trigger refuses it under an issued document (D9).
DELETE FROM invoices.lines WHERE invoice_id = @invoice_id;

-- name: InsertLine :exec
-- InsertLine writes one line of a draft with its computed amounts (D5); the
-- VAT snapshot is the issue's to write.
INSERT INTO invoices.lines (
    invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
    credits_line_id, line_gross, line_allowance, line_net
) VALUES (
    @invoice_id, @position, @description, @quantity, @unit, @unit_price, @discount_percent, @vat_code_id,
    @credits_line_id, @line_gross, @line_allowance, @line_net
);

-- name: VatSummaries :many
-- VatSummaries is an issued document's VAT per (category, rate), the highest
-- rate first.
SELECT * FROM invoices.vat_summaries WHERE invoice_id = @invoice_id ORDER BY rate_percent DESC, vat_category;
```

**Append** to the end of `apps/server/internal/invoices/queries/vatcodes.sql`:

```sql

-- name: VatCodesOnDay :many
-- VatCodesOnDay is every code with the rate of the period covering day, NULL
-- when none covers it: what a draft's lines are checked and totalled against
-- (D3, D4). Inactive codes are in it, so a line can be told "inactive" rather
-- than "unknown".
SELECT c.id, c.code, c.active, c.saf_t_code, c.ehf_category, c.exemption_reason, r.rate_percent
FROM invoices.vat_codes c
LEFT JOIN invoices.vat_code_rates r
    ON r.vat_code_id = c.id AND r.valid_from <= @day::date AND (r.valid_to IS NULL OR r.valid_to >= @day::date)
ORDER BY c.id;
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 4: The directory door, the money, the issue-date rule, drafts, responses and the list**

**Create** `apps/server/internal/invoices/contractscalls.go`:

```go
package invoices

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the whole of this module's reach outside its own schema: the
// customer directory (deps.Directory) and the object store (server.objects).
// Every call is made through one of the thin accessors below and through
// nowhere else, so "what does Invoices ask of its neighbours, and when" has one
// place to read the answer and one place to check it from.
//
// What is checked is the rule of D6 and D7: no directory call and no
// object-store call is ever made inside a transaction holding locks
// (withLockedTx). The directory reads through the same connection pool, and a
// slow store under a row lock is the same hazard by another route.
// noteContractCall pins the rule on every path, at a production cost of one nil
// comparison per call.

// contractCallHook is handed the context and the name of every call out of the
// module. It is nil in production and installed once, before any test runs, by
// this package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call out of the module.
func noteContractCall(ctx context.Context, method string) {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
}

// customerProfile is the billing profile, the one read an invoice's gates and
// its buyer snapshot are made from (D4, D10).
func (s *server) customerProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	noteContractCall(ctx, "Directory.BillingProfile")
	return s.deps.Directory.BillingProfile(ctx, id)
}

// customerEntries names a page of drafts' customers in one round trip. An
// empty batch asks nobody.
func (s *server) customerEntries(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	noteContractCall(ctx, "Directory.Customers")
	return s.deps.Directory.Customers(ctx, ids)
}
```

**Replace** in `apps/server/internal/invoices/server.go`:

```go
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
```

**with**:

```go
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
```

**Replace** in `apps/server/internal/invoices/server.go`:

```go
		return fn(locked, store.New(tx))
	})
}
```

**with**:

```go
		return fn(locked, store.New(tx))
	})
}

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (invoices:access), so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// inLockedTx reports whether ctx is one withLockedTx marked. Nothing in the
// module branches on it; the tests' contract-call hook does
// (contractscalls.go), to prove that no call into another module or the
// object store is ever made while locks are held.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}
```

**Create** `apps/server/internal/invoices/money.go`:

```go
package invoices

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is D5's arithmetic, and nothing else: pure functions over exact
// decimals (math/big.Rat), with no database, no request and no float in them.
// Every rounding is to two decimals, the half away from zero — big.Rat's own
// FloatString rule, and the codebase's (docs/expenses.md).

// The bounds a document's amounts are held to before the database could
// overflow (D5).
var (
	maxLineGross  = mustRat("999999999.99")
	maxGrossTotal = mustRat("99999999999.99")
)

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("invoices: not a decimal: " + s)
	}
	return r
}

// round2 is v at two decimals, the half away from zero.
func round2(v *big.Rat) *big.Rat { return mustRat(v.FloatString(2)) }

// lineAmounts is one line's gross, its discount as an allowance, and its net.
// A line is stored as gross minus allowance rather than as one rounded
// product: EHF expresses a line discount as an allowance, and
// round(qty × price) − round(allowance) can differ from round(qty × price ×
// (1 − d)) by an øre, which PEPPOL-EN16931-R120 checks. Storing the two parts
// makes the PDF and a later EHF agree by construction.
type lineAmounts struct {
	gross, allowance, net *big.Rat
}

// computeLine is D5's line rule: line_gross = round(quantity × unit_price),
// line_allowance = round(line_gross × discount / 100), line_net = gross −
// allowance.
func computeLine(quantity, unitPrice, discountPercent *big.Rat) lineAmounts {
	gross := round2(new(big.Rat).Mul(quantity, unitPrice))
	allowance := round2(new(big.Rat).Quo(new(big.Rat).Mul(gross, discountPercent), big.NewRat(100, 1)))
	return lineAmounts{gross: gross, allowance: allowance, net: new(big.Rat).Sub(gross, allowance)}
}

// taxedLine is a line's net with the VAT treatment it carries.
type taxedLine struct {
	net      *big.Rat
	category string
	rate     *big.Rat
	safT     string
	reason   *string
}

// vatSummary is one (category, rate) row of a document (D4, D5).
type vatSummary struct {
	category string
	rate     *big.Rat
	safT     string
	reason   *string
	taxable  *big.Rat
	vat      *big.Rat
	vatNOK   *big.Rat
}

// documentTotals are a document's four totals.
type documentTotals struct {
	net, vat, gross, vatNOK *big.Rat
}

// summarize groups the lines per (category, rate) and computes VAT per rate on
// the sum of the lines' nets (Peppol BR-CO-17), never per line: taxable =
// Σ line_net, vat = round(taxable × rate / 100), vat_nok = round(vat ×
// exchange rate). A 0 % category gets its row too (§ 5-1-5). There is no øre
// rounding of the total: payment is electronic.
//
// ambiguous is true when two lines share a (category, rate) but carry
// different SAF-T codes, so one summary row could not name its code
// (vat_codes_ambiguous, D6). The rows come highest rate first, then by
// category, so a document always lists them the same way.
func summarize(lines []taxedLine, exchangeRate *big.Rat) (rows []vatSummary, totals documentTotals, ambiguous bool) {
	type key struct{ category, rate string }
	byKey := map[key]*vatSummary{}
	var order []key
	for _, l := range lines {
		k := key{l.category, l.rate.FloatString(2)}
		row, ok := byKey[k]
		if !ok {
			row = &vatSummary{category: l.category, rate: l.rate, safT: l.safT, reason: l.reason, taxable: new(big.Rat)}
			byKey[k] = row
			order = append(order, k)
		} else if row.safT != l.safT {
			ambiguous = true
		}
		row.taxable.Add(row.taxable, l.net)
	}
	totals = documentTotals{net: new(big.Rat), vat: new(big.Rat), gross: new(big.Rat), vatNOK: new(big.Rat)}
	for _, k := range order {
		row := byKey[k]
		row.vat = round2(new(big.Rat).Quo(new(big.Rat).Mul(row.taxable, row.rate), big.NewRat(100, 1)))
		row.vatNOK = round2(new(big.Rat).Mul(row.vat, exchangeRate))
		totals.net.Add(totals.net, row.taxable)
		totals.vat.Add(totals.vat, row.vat)
		totals.vatNOK.Add(totals.vatNOK, row.vatNOK)
		rows = append(rows, *row)
	}
	totals.gross.Add(totals.net, totals.vat)
	sort.SliceStable(rows, func(i, j int) bool {
		if c := rows[i].rate.Cmp(rows[j].rate); c != 0 {
			return c > 0
		}
		return rows[i].category < rows[j].category
	})
	return rows, totals, ambiguous
}

// ratFromNumeric reads a numeric column as the exact decimal it holds — Int ×
// 10^Exp, never through a float.
func ratFromNumeric(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid || n.Int == nil {
		return nil, fmt.Errorf("invoices: read a stored decimal: the column holds no number")
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("invoices: read a stored decimal: the column holds %v", n.InfinityModifier)
	}
	value := new(big.Rat).SetInt(n.Int)
	exp := n.Exp
	if exp < 0 {
		exp = -exp
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
	if n.Exp < 0 {
		return value.Quo(value, scale), nil
	}
	return value.Mul(value, scale), nil
}

// floatFromRat is an exact decimal onto the wire as a JSON number, at places
// decimals. The wire carries doubles; the arithmetic never did.
func floatFromRat(v *big.Rat, places int) float64 {
	f, _ := mustRat(v.FloatString(places)).Float64()
	return f
}
```

**Create** `apps/server/internal/invoices/issuedate.go`:

```go
package invoices

import "time"

// This file is D6's issue-date rule, as a pure function: which dates a
// document may be issued with on a given day.
//
// Lovdata § 5-1-3 third paragraph allows exactly one date other than the
// actual one: a document issued within the first fifteen working days of a
// month may carry the last day of the previous month, provided the goods or
// the service were delivered by then. "Virkedager" is not defined in the
// regulation, and fifteen working days always reach at least the 17th, even
// counting Saturdays; "calendar day ≤ 15" is therefore always within the law,
// needs no holiday calendar, and is stricter than the law — docs/invoices.md
// says so. On top of that no date may be before the latest issue date of any
// issued document, so numbers and dates are both monotone: an extra guard the
// law does not ask for.

// allowedIssueDates are the dates a document delivered by deliveryEnd may be
// issued with today, the earliest first. latest is the latest issue date of
// any issued document; a zero deliveryEnd (no delivery yet) allows only today.
func allowedIssueDates(today, deliveryEnd, latest time.Time) []time.Time {
	var allowed []time.Time
	lastOfPrevious := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if today.Day() <= 15 && !deliveryEnd.IsZero() && !deliveryEnd.After(lastOfPrevious) &&
		(latest.IsZero() || !lastOfPrevious.Before(latest)) {
		allowed = append(allowed, lastOfPrevious)
	}
	if latest.IsZero() || !today.Before(latest) {
		allowed = append(allowed, today)
	}
	return allowed
}

// issuedLate is § 5-2-2's "senest en måned etter levering", as a warning: the
// document is issued more than one month after its delivery ended (D6). It
// never refuses — refusing would leave the sale undocumented.
func issuedLate(issueDate, deliveryEnd time.Time) bool {
	return !deliveryEnd.IsZero() && deliveryEnd.Before(issueDate.AddDate(0, -1, 0))
}
```

**Create** `apps/server/internal/invoices/drafts.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the draft (D4): created, replaced whole and deleted while it is
// a draft, and never again once issued. A draft is not a salgsdokument — it
// has no number, and its totals are computed with today's rates only so the
// person editing it sees them; the issue computes them again for the issue
// date.

// The document's kinds and statuses, as the columns hold them.
const (
	kindInvoice    = "invoice"
	kindCreditNote = "credit_note"
	statusDraft    = "draft"
	statusIssued   = "issued"
)

// The codes and titles of the drafts' 409s.
const (
	codeInvoiceIssued    = "invoice_issued"
	codeCustomerMerged   = "customer_merged"
	codeCustomerArchived = "customer_archived"
	codeCustomerBlocked  = "customer_blocked"
	codeCustomerMissing  = "customer_missing"

	invoiceIssuedTitle  = "The document is issued"
	customerGateTitle   = "The customer cannot be invoiced"
	invalidInvoiceTitle = "Invalid invoice"
)

// maxLines is how many lines one document may carry (D5).
const maxLines = 500

// invoiceIssued is the answer to every edit and delete of an issued document.
func invoiceIssued() gen.InvoicesConflictProblem {
	return conflict(codeInvoiceIssued, invoiceIssuedTitle,
		"An issued document is never changed or deleted. Correct it with a credit note.")
}

// customerGate is D4's customer gates on creating and saving an invoice draft,
// in their order, from the billing profile: merged away (carrying where to),
// archived (an anonymised person is archived), disabled — "blocked for
// invoicing" — and, what cannot happen since customers are never deleted, no
// profile at all. nil when the customer may be invoiced. A credit note never
// passes through here (D8).
func customerGate(p *contracts.CustomerBillingProfile) *gen.InvoicesConflictProblem {
	switch {
	case p == nil:
		return ptr(conflict(codeCustomerMissing, customerGateTitle, "No customer has this id."))
	case p.MergedInto != nil:
		c := conflict(codeCustomerMerged, customerGateTitle, fmt.Sprintf(
			"This customer was merged into customer %d; invoice that one instead.", *p.MergedInto))
		c.MergedInto = p.MergedInto
		return &c
	case p.Status == "archived":
		return ptr(conflict(codeCustomerArchived, customerGateTitle,
			"This customer is archived, and an archived customer is not invoiced."))
	case p.Status == "disabled":
		return ptr(conflict(codeCustomerBlocked, customerGateTitle,
			"This customer is blocked for invoicing."))
	}
	return nil
}

// draftLine is one validated line of a request.
type draftLine struct {
	description, unit             string
	quantity, unitPrice, discount *big.Rat
	vatCodeID                     int32
	creditsLineID                 *int64
	amounts                       lineAmounts
}

// draftInput is one validated draft body.
type draftInput struct {
	customerID                             int32
	paymentTermsDays                       *int32
	deliveryDate, deliveryFrom, deliveryTo pgtype.Date
	address                                *gen.InvoicesDeliveryAddress
	yourReference                          *string
	ourReference, orderReference           string
	note, internalNote                     string
	lines                                  []draftLine
}

var addressCountry = regexp.MustCompile(`^[A-Z]{2}$`)

// amount is one number of a line: finite, at least minimum (above it when
// strict), at most places decimals, and within the column. It answers the
// exact decimal and the message to report, "" when it holds.
func amount(label string, v float64, places int, minimum *big.Rat, strict bool, maximum *big.Rat) (*big.Rat, string) {
	if !finite(v) {
		return nil, label + " is a number"
	}
	if decimalPlaces(v) > places {
		return nil, fmt.Sprintf("%s has at most %d decimals", label, places)
	}
	r := ratFromFloat(v)
	switch c := r.Cmp(minimum); {
	case strict && c <= 0:
		return nil, fmt.Sprintf("%s is greater than %s", label, minimum.FloatString(0))
	case c < 0:
		return nil, fmt.Sprintf("%s is %s or more", label, minimum.FloatString(0))
	}
	if r.Cmp(maximum) > 0 {
		return nil, fmt.Sprintf("%s is at most %s", label, maximum.FloatString(places))
	}
	return r, ""
}

// The columns' own ceilings: quantity numeric(12,3), unit_price
// numeric(14,4), discount_percent 0-100.
var (
	maxQuantity  = mustRat("999999999.999")
	maxUnitPrice = mustRat("9999999999.9999")
	zero         = new(big.Rat)
)

// optionalText is a trimmed optional field, "" when absent.
func optionalText(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

// parseDraft runs D4's and D5's field rules over a draft body, every failure
// collected. currency is the one this installation invoices in (NOK in phase
// 1). The VAT codes and the bounds on computed amounts are checked afterwards,
// against the codes (checkLines).
func parseDraft(body gen.InvoicesInvoiceRequest, currency string) (draftInput, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	in := draftInput{
		customerID:       body.CustomerId,
		paymentTermsDays: body.PaymentTermsDays,
		ourReference:     optionalText(body.OurReference),
		orderReference:   optionalText(body.OrderReference),
		note:             optionalText(body.Note),
		internalNote:     optionalText(body.InternalNote),
	}
	if body.YourReference != nil {
		in.yourReference = ptr(strings.TrimSpace(*body.YourReference))
		add("yourReference", maxLength("A reference", *in.yourReference, 100))
	}
	if body.CustomerId <= 0 {
		add("customerId", "A document needs a customer")
	}
	if body.Currency != nil && strings.ToUpper(strings.TrimSpace(*body.Currency)) != currency {
		add("currency", onlyNOK)
	}
	if t := body.PaymentTermsDays; t != nil && (*t < 0 || *t > 365) {
		add("paymentTermsDays", "Payment terms are between 0 and 365 days")
	}
	add("ourReference", maxLength("A reference", in.ourReference, 100))
	add("orderReference", maxLength("A reference", in.orderReference, 100))
	add("note", maxLength("The note", in.note, 1000))
	add("internalNote", maxLength("The internal note", in.internalNote, 1000))

	// Delivery (§ 5-1-1 nr. 4): a day, a period with from on or before to,
	// or — on a draft only — nothing.
	date := func(d *openapi_types.Date) pgtype.Date {
		if d == nil {
			return pgtype.Date{}
		}
		return pgDate(utcDay(d.Time))
	}
	in.deliveryDate, in.deliveryFrom, in.deliveryTo = date(body.DeliveryDate), date(body.DeliveryFrom), date(body.DeliveryTo)
	switch {
	case in.deliveryDate.Valid && (in.deliveryFrom.Valid || in.deliveryTo.Valid):
		add("deliveryDate", "A delivery is a day or a period, not both")
	case in.deliveryFrom.Valid != in.deliveryTo.Valid:
		add("deliveryTo", "A delivery period has both a first and a last day")
	case in.deliveryFrom.Valid && in.deliveryFrom.Time.After(in.deliveryTo.Time):
		add("deliveryTo", "A delivery period ends on or after the day it starts")
	}
	if a := body.DeliveryAddress; a != nil {
		addr := gen.InvoicesDeliveryAddress{
			Line1: strings.TrimSpace(a.Line1), City: strings.TrimSpace(a.City),
			Country: strings.ToUpper(strings.TrimSpace(a.Country)),
		}
		if v := optionalText(a.Line2); v != "" {
			addr.Line2 = &v
		}
		if v := optionalText(a.PostalCode); v != "" {
			addr.PostalCode = &v
		}
		if addr.Line1 == "" {
			add("deliveryAddress.line1", "A place of delivery needs an address line")
		}
		add("deliveryAddress.line1", maxLength("An address line", addr.Line1, 200))
		add("deliveryAddress.line2", maxLength("An address line", optionalText(addr.Line2), 200))
		add("deliveryAddress.postalCode", maxLength("A postal code", optionalText(addr.PostalCode), 20))
		if addr.City == "" {
			add("deliveryAddress.city", "A place of delivery needs a city")
		}
		add("deliveryAddress.city", maxLength("A city", addr.City, 100))
		if !addressCountry.MatchString(addr.Country) {
			add("deliveryAddress.country", "A country is a two-letter ISO 3166-1 code, such as NO")
		}
		in.address = &addr
	}

	if len(body.Lines) > maxLines {
		add("lines", fmt.Sprintf("At most %d lines", maxLines))
		return in, errs
	}
	for i, l := range body.Lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		line := draftLine{
			description: strings.TrimSpace(l.Description), unit: optionalText(l.Unit),
			vatCodeID: l.VatCodeId, creditsLineID: l.CreditsLineId,
		}
		if line.description == "" {
			add(field("description"), "A line needs a description")
		}
		add(field("description"), maxLength("A description", line.description, 500))
		add(field("unit"), maxLength("A unit", line.unit, 20))
		var msg string
		line.quantity, msg = amount("A quantity", l.Quantity, 3, zero, true, maxQuantity)
		add(field("quantity"), msg)
		line.unitPrice, msg = amount("A unit price", l.UnitPrice, 4, zero, false, maxUnitPrice)
		add(field("unitPrice"), msg)
		discount := 0.0
		if l.DiscountPercent != nil {
			discount = *l.DiscountPercent
		}
		line.discount, msg = amount("A discount", discount, 2, zero, false, big.NewRat(100, 1))
		add(field("discountPercent"), msg)
		if line.quantity != nil && line.unitPrice != nil && line.discount != nil {
			line.amounts = computeLine(line.quantity, line.unitPrice, line.discount)
			if line.amounts.gross.Cmp(maxLineGross) > 0 {
				add(field("unitPrice"), "The line amount is too large")
			}
		}
		in.lines = append(in.lines, line)
	}
	return in, errs
}

// vatCodeOnDay is one code as a draft line is judged against it.
type vatCodeOnDay struct {
	active         bool
	category, safT string
	reason         *string
	rate           *big.Rat // nil when no period covers the day
}

// vatCodesOn is every code with the rate of the period covering day.
func vatCodesOn(ctx context.Context, q *store.Queries, day pgtype.Date) (map[int32]vatCodeOnDay, error) {
	rows, err := q.VatCodesOnDay(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes: %w", err)
	}
	out := make(map[int32]vatCodeOnDay, len(rows))
	for _, r := range rows {
		c := vatCodeOnDay{active: r.Active, category: r.EhfCategory, safT: r.SafTCode, reason: r.ExemptionReason}
		if r.RatePercent.Valid {
			if c.rate, err = ratFromNumeric(r.RatePercent); err != nil {
				return nil, err
			}
		}
		out[r.ID] = c
	}
	return out, nil
}

// taxedLines are the lines as today's rates would tax them: a draft's preview
// (D4). A code with no period covering today counts at 0; the issue refuses it
// (vat_code_not_valid).
func taxedLines(lines []draftLine, codes map[int32]vatCodeOnDay) []taxedLine {
	out := make([]taxedLine, 0, len(lines))
	for _, l := range lines {
		c := codes[l.vatCodeID]
		rate := c.rate
		if rate == nil {
			rate = new(big.Rat)
		}
		out = append(out, taxedLine{net: l.amounts.net, category: c.category, rate: rate, safT: c.safT, reason: c.reason})
	}
	return out
}

// checkInvoiceLines is an invoice draft's VAT codes (known and active, D3) and
// its total bound (D5), added to errs.
func checkInvoiceLines(lines []draftLine, codes map[int32]vatCodeOnDay, errs map[string][]string) map[string][]string {
	for i, l := range lines {
		switch c, ok := codes[l.vatCodeID]; {
		case !ok:
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].vatCodeId", i), "No VAT code has this id")
		case !c.active:
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].vatCodeId", i), "This VAT code is no longer offered for new lines")
		}
		if l.creditsLineID != nil {
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].creditsLineId", i), "Only a credit note's line credits another line")
		}
	}
	return errs
}

// checkTotal is D5's document bound, on the totals the lines make.
func checkTotal(totals documentTotals, errs map[string][]string) map[string][]string {
	if totals.gross.Cmp(maxGrossTotal) > 0 {
		errs = withFieldError(errs, "lines", "The document total is too large")
	}
	return errs
}

// numerics is a document's four totals as the columns take them.
func numerics(t documentTotals) (net, vat, gross, vatNOK pgtype.Numeric, err error) {
	if net, err = numericFromRat(t.net, 2); err != nil {
		return
	}
	if vat, err = numericFromRat(t.vat, 2); err != nil {
		return
	}
	if gross, err = numericFromRat(t.gross, 2); err != nil {
		return
	}
	vatNOK, err = numericFromRat(t.vatNOK, 2)
	return
}

// addressColumns is the delivery address as its five columns.
func addressColumns(a *gen.InvoicesDeliveryAddress) (line1, line2, postal, city, country *string) {
	if a == nil {
		return nil, nil, nil, nil, nil
	}
	return &a.Line1, a.Line2, a.PostalCode, &a.City, &a.Country
}

// writeLines replaces a draft's lines with lines, on txq.
func writeLines(ctx context.Context, txq *store.Queries, invoiceID int64, lines []draftLine) error {
	if err := txq.DeleteLines(ctx, invoiceID); err != nil {
		return fmt.Errorf("invoices: clear draft %d's lines: %w", invoiceID, err)
	}
	for i, l := range lines {
		p := store.InsertLineParams{
			InvoiceID: invoiceID, Position: int32(i + 1), Description: l.description, Unit: l.unit,
			VatCodeID: l.vatCodeID, CreditsLineID: l.creditsLineID,
		}
		var err error
		for _, c := range []struct {
			dst    *pgtype.Numeric
			v      *big.Rat
			places int
		}{
			{&p.Quantity, l.quantity, 3}, {&p.UnitPrice, l.unitPrice, 4}, {&p.DiscountPercent, l.discount, 2},
			{&p.LineGross, l.amounts.gross, 2}, {&p.LineAllowance, l.amounts.allowance, 2}, {&p.LineNet, l.amounts.net, 2},
		} {
			if *c.dst, err = numericFromRat(c.v, c.places); err != nil {
				return err
			}
		}
		if err := txq.InsertLine(ctx, p); err != nil {
			return fmt.Errorf("invoices: write draft %d's line %d: %w", invoiceID, i+1, err)
		}
	}
	return nil
}

// PostInvoices Create an invoice draft
// (POST /api/v1/invoices)
//
// The billing profile is read before anything is written — no lock is held
// across a directory call — for the prefills and the gates: yourReference is
// the profile's buyer reference and paymentTermsDays its terms, else the
// settings' default, whenever the request leaves them out.
func (s *server) PostInvoices(ctx context.Context, req gen.PostInvoicesRequestObject) (gen.PostInvoicesResponseObject, error) {
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	in, errs := parseDraft(*req.Body, settings.DefaultCurrency)
	if len(errs) > 0 {
		return gen.PostInvoices400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	profile, err := s.customerProfile(ctx, in.customerID)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PostInvoices409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if in.yourReference == nil {
		in.yourReference = ptr(profile.BuyerReference)
	}
	if in.paymentTermsDays == nil {
		in.paymentTermsDays = profile.PaymentTermsDays
	}
	if in.paymentTermsDays == nil {
		in.paymentTermsDays = &settings.DefaultPaymentTermsDays
	}

	codes, err := vatCodesOn(ctx, q, pgDate(businessDay(s.deps.Clock())))
	if err != nil {
		return nil, err
	}
	errs = checkInvoiceLines(in.lines, codes, errs)
	_, totals, _ := summarize(taxedLines(in.lines, codes), big.NewRat(1, 1))
	errs = checkTotal(totals, errs)
	if len(errs) > 0 {
		return gen.PostInvoices400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	net, vat, gross, vatNOK, err := numerics(totals)
	if err != nil {
		return nil, err
	}
	line1, line2, postal, city, country := addressColumns(in.address)
	var created store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		created, err = txq.InsertInvoiceDraft(ctx, store.InsertInvoiceDraftParams{
			CustomerID: in.customerID, DeliveryDate: in.deliveryDate, DeliveryFrom: in.deliveryFrom, DeliveryTo: in.deliveryTo,
			DeliveryAddressLine1: line1, DeliveryAddressLine2: line2, DeliveryPostalCode: postal, DeliveryCity: city, DeliveryCountry: country,
			PaymentTermsDays: in.paymentTermsDays, Currency: settings.DefaultCurrency,
			YourReference: *in.yourReference, OurReference: in.ourReference, OrderReference: in.orderReference,
			Note: in.note, InternalNote: in.internalNote,
			NetTotal: net, VatTotal: vat, GrossTotal: gross, VatTotalNok: vatNOK,
			CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: create a draft: %w", err)
		}
		return writeLines(ctx, txq, created.ID, in.lines)
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, created, profile)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoices201JSONResponse(resp), nil
}

// PutInvoicesById Replace a draft
// (PUT /api/v1/invoices/{id})
//
// A full replace with revision. The billing profile is read before the
// transaction, and the gates run on every save: a customer merged away,
// archived or disabled since the draft was made refuses the save too.
func (s *server) PutInvoicesById(ctx context.Context, req gen.PutInvoicesByIdRequestObject) (gen.PutInvoicesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	current, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutInvoicesById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if current.Status != statusDraft {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	}
	if req.Body.Revision == nil {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle,
			fieldError("revision", "A replace carries the revision it was read at"))), nil
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	in, errs := parseDraft(*req.Body, settings.DefaultCurrency)
	if in.paymentTermsDays == nil {
		errs = withFieldError(errs, "paymentTermsDays", "An invoice needs payment terms")
	}
	if len(errs) > 0 {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	profile, err := s.customerProfile(ctx, in.customerID)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	codes, err := vatCodesOn(ctx, q, pgDate(businessDay(s.deps.Clock())))
	if err != nil {
		return nil, err
	}
	errs = checkInvoiceLines(in.lines, codes, errs)
	_, totals, _ := summarize(taxedLines(in.lines, codes), big.NewRat(1, 1))
	errs = checkTotal(totals, errs)
	if len(errs) > 0 {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	saved, refusal, err := s.saveDraft(ctx, req.Id, *req.Body.Revision, in, totals)
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	resp, err := s.invoiceResponse(ctx, q, saved, profile)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesById200JSONResponse(resp), nil
}

// saveDraft is the transaction every draft replace runs: the row taken FOR
// UPDATE, still a draft and at the revision the caller read; then the row and
// its lines replaced.
func (s *server) saveDraft(ctx context.Context, id int64, revision int32, in draftInput, totals documentTotals) (store.InvoicesInvoice, *gen.InvoicesConflictProblem, error) {
	net, vat, gross, vatNOK, err := numerics(totals)
	if err != nil {
		return store.InvoicesInvoice{}, nil, err
	}
	line1, line2, postal, city, country := addressColumns(in.address)
	var refusal *gen.InvoicesConflictProblem
	var saved store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, err := txq.LockInvoice(ctx, id)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", id, err)
		}
		switch {
		case locked.Status != statusDraft:
			refusal = ptr(invoiceIssued())
		case locked.Revision != revision:
			refusal = ptr(revisionConflict("Invoice", locked.Revision, revision))
		}
		if refusal != nil {
			return errRefused
		}
		yourReference := ""
		if in.yourReference != nil {
			yourReference = *in.yourReference
		}
		saved, err = txq.UpdateDraft(ctx, store.UpdateDraftParams{
			ID: id, CustomerID: in.customerID, DeliveryDate: in.deliveryDate, DeliveryFrom: in.deliveryFrom, DeliveryTo: in.deliveryTo,
			DeliveryAddressLine1: line1, DeliveryAddressLine2: line2, DeliveryPostalCode: postal, DeliveryCity: city, DeliveryCountry: country,
			PaymentTermsDays: in.paymentTermsDays, YourReference: yourReference, OurReference: in.ourReference,
			OrderReference: in.orderReference, Note: in.note, InternalNote: in.internalNote,
			NetTotal: net, VatTotal: vat, GrossTotal: gross, VatTotalNok: vatNOK, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: replace draft %d: %w", id, err)
		}
		return writeLines(ctx, txq, id, in.lines)
	})
	if refusal != nil {
		return store.InvoicesInvoice{}, refusal, nil
	}
	return saved, nil, err
}

// DeleteInvoicesById Delete a draft
// (DELETE /api/v1/invoices/{id})
func (s *server) DeleteInvoicesById(ctx context.Context, req gen.DeleteInvoicesByIdRequestObject) (gen.DeleteInvoicesByIdResponseObject, error) {
	var notFound, issued bool
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, err := txq.LockInvoice(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		if locked.Status != statusDraft {
			issued = true
			return errRefused
		}
		_, err = txq.DeleteDraft(ctx, req.Id)
		return err
	})
	switch {
	case notFound:
		return gen.DeleteInvoicesById404Response{}, nil
	case issued:
		return gen.DeleteInvoicesById409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: delete draft %d: %w", req.Id, err)
	}
	return gen.DeleteInvoicesById204Response{}, nil
}

// GetInvoicesById Get an invoice or a credit note
// (GET /api/v1/invoices/{id})
func (s *server) GetInvoicesById(ctx context.Context, req gen.GetInvoicesByIdRequestObject) (gen.GetInvoicesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	var profile *contracts.CustomerBillingProfile
	if inv.Status == statusDraft && inv.Kind == kindInvoice {
		// A draft invoice names its customer as the directory knows it today,
		// and warns when the profile invoices in another currency.
		if profile, err = s.customerProfile(ctx, inv.CustomerID); err != nil {
			return nil, err
		}
	}
	resp, err := s.invoiceResponse(ctx, q, inv, profile)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesById200JSONResponse(resp), nil
}
```

**Create** `apps/server/internal/invoices/responses.go`:

```go
package invoices

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file renders a document for the wire (D4): every column in camelCase,
// its lines, its VAT summaries and its warnings. An issued document is
// rendered from its own rows and snapshots only; a draft's summaries and
// totals are computed afresh with the rates in force today.

// The warnings a document carries (D4, D6, D8). They are never refusals.
const (
	warningCustomerCurrencyDiffers = "customer_currency_differs"
	warningIssuedLate              = "issued_late"
)

// wireDateOf is a date column onto the wire, nil for NULL.
func wireDateOf(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return ptr(wireDate(d.Time))
}

// deliveryEndOf is when a document's delivery ended — its day, or its
// period's last day — and the zero time when it has none.
func deliveryEndOf(inv store.InvoicesInvoice) time.Time {
	switch {
	case inv.DeliveryDate.Valid:
		return inv.DeliveryDate.Time
	case inv.DeliveryTo.Valid:
		return inv.DeliveryTo.Time
	}
	return time.Time{}
}

// lineResponse renders one line.
func lineResponse(l store.InvoicesLine) (gen.InvoicesLine, error) {
	out := gen.InvoicesLine{
		Id: l.ID, Position: l.Position, Description: l.Description, Unit: l.Unit, VatCodeId: l.VatCodeID,
		CreditsLineId: l.CreditsLineID, VatCategory: l.VatCategory, SafTCode: l.SafTCode, ExemptionReason: l.ExemptionReason,
	}
	var err error
	for _, c := range []struct {
		dst *float64
		n   pgtype.Numeric
	}{
		{&out.Quantity, l.Quantity}, {&out.UnitPrice, l.UnitPrice}, {&out.DiscountPercent, l.DiscountPercent},
		{&out.LineGross, l.LineGross}, {&out.LineAllowance, l.LineAllowance}, {&out.LineNet, l.LineNet},
	} {
		if *c.dst, err = floatFromNumeric(c.n); err != nil {
			return gen.InvoicesLine{}, err
		}
	}
	if l.VatRatePercent.Valid {
		rate, err := floatFromNumeric(l.VatRatePercent)
		if err != nil {
			return gen.InvoicesLine{}, err
		}
		out.VatRatePercent = &rate
	}
	return out, nil
}

// summaryResponse renders one computed VAT summary row.
func summaryResponse(r vatSummary) gen.InvoicesVatSummary {
	return gen.InvoicesVatSummary{
		VatCategory: r.category, RatePercent: floatFromRat(r.rate, 2), SafTCode: r.safT, ExemptionReason: r.reason,
		TaxableAmount: floatFromRat(r.taxable, 2), VatAmount: floatFromRat(r.vat, 2), VatAmountNok: floatFromRat(r.vatNOK, 2),
	}
}

// storedSummaryResponse renders one issued document's stored summary row.
func storedSummaryResponse(r store.InvoicesVatSummary) (gen.InvoicesVatSummary, error) {
	out := gen.InvoicesVatSummary{VatCategory: r.VatCategory, SafTCode: r.SafTCode, ExemptionReason: r.ExemptionReason}
	var err error
	for _, c := range []struct {
		dst *float64
		n   pgtype.Numeric
	}{
		{&out.RatePercent, r.RatePercent}, {&out.TaxableAmount, r.TaxableAmount},
		{&out.VatAmount, r.VatAmount}, {&out.VatAmountNok, r.VatAmountNok},
	} {
		if *c.dst, err = floatFromNumeric(c.n); err != nil {
			return gen.InvoicesVatSummary{}, err
		}
	}
	return out, nil
}

// storedDraftLines are a draft's stored lines as the arithmetic reads them.
func storedDraftLines(lines []store.InvoicesLine) ([]draftLine, error) {
	out := make([]draftLine, 0, len(lines))
	for _, l := range lines {
		net, err := ratFromNumeric(l.LineNet)
		if err != nil {
			return nil, err
		}
		out = append(out, draftLine{vatCodeID: l.VatCodeID, amounts: lineAmounts{net: net}})
	}
	return out, nil
}

// buyerResponse is a document's buyer snapshot, nil when it has none yet (an
// invoice draft).
func buyerResponse(inv store.InvoicesInvoice) *gen.InvoicesBuyer {
	if inv.BuyerName == nil {
		return nil
	}
	b := &gen.InvoicesBuyer{
		Name: *inv.BuyerName, OrganisationNumber: inv.BuyerOrganisationNumber, ForeignId: inv.BuyerForeignID,
		AddressLine1: inv.BuyerAddressLine1, AddressLine2: inv.BuyerAddressLine2, PostalCode: inv.BuyerPostalCode,
		City: inv.BuyerCity, Region: inv.BuyerRegion, Country: inv.BuyerCountry, PeppolId: inv.BuyerPeppolID, Gln: inv.BuyerGln,
	}
	if inv.BuyerCustomerNumber != nil {
		b.CustomerNumber = *inv.BuyerCustomerNumber
	}
	if inv.BuyerType != nil {
		b.Type = *inv.BuyerType
	}
	if inv.BuyerLanguage != nil {
		b.Language = *inv.BuyerLanguage
	}
	return b
}

// sellerResponse is an issued document's seller snapshot, nil on a draft.
func sellerResponse(inv store.InvoicesInvoice) *gen.InvoicesSeller {
	if inv.SellerLegalName == nil {
		return nil
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	flag := func(b *bool) bool { return b != nil && *b }
	return &gen.InvoicesSeller{
		LegalName: *inv.SellerLegalName, OrganisationNumber: deref(inv.SellerOrganisationNumber),
		VatRegistered: flag(inv.SellerVatRegistered), InForetaksregisteret: flag(inv.SellerInForetaksregisteret),
		AddressLine1: deref(inv.SellerAddressLine1), AddressLine2: deref(inv.SellerAddressLine2),
		PostalCode: deref(inv.SellerPostalCode), City: deref(inv.SellerCity), Country: deref(inv.SellerCountry),
		BankAccount: deref(inv.SellerBankAccount), Iban: deref(inv.SellerIban), Bic: deref(inv.SellerBic),
		Email: deref(inv.SellerEmail), FooterText: deref(inv.SellerFooterText),
	}
}

// invoiceResponse renders one document. profile is the billing profile the
// caller already read for an invoice draft (for its current name and the
// currency warning), nil otherwise; this function reads no directory itself.
func (s *server) invoiceResponse(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile) (gen.InvoicesInvoiceResponse, error) {
	stored, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	resp := gen.InvoicesInvoiceResponse{
		Id: inv.ID, Kind: inv.Kind, Status: inv.Status, Number: inv.Number, CustomerId: inv.CustomerID,
		IssueDate: wireDateOf(inv.IssueDate), DeliveryDate: wireDateOf(inv.DeliveryDate),
		DeliveryFrom: wireDateOf(inv.DeliveryFrom), DeliveryTo: wireDateOf(inv.DeliveryTo),
		PaymentTermsDays: inv.PaymentTermsDays, DueDate: wireDateOf(inv.DueDate), Currency: inv.Currency,
		ExchangeRateDate: wireDateOf(inv.ExchangeRateDate),
		YourReference:    inv.YourReference, OurReference: inv.OurReference, OrderReference: inv.OrderReference,
		Note: inv.Note, InternalNote: inv.InternalNote, Buyer: buyerResponse(inv), Seller: sellerResponse(inv),
		IssuedAt: inv.IssuedAt, IssuedByUserId: inv.IssuedByUserID,
		CreatedAt: inv.CreatedAt, UpdatedAt: inv.UpdatedAt, Revision: inv.Revision,
		Lines: make([]gen.InvoicesLine, 0, len(stored)), VatSummaries: []gen.InvoicesVatSummary{}, Warnings: []string{},
	}
	if inv.DeliveryAddressLine1 != nil {
		resp.DeliveryAddress = &gen.InvoicesDeliveryAddress{
			Line1: *inv.DeliveryAddressLine1, Line2: inv.DeliveryAddressLine2, PostalCode: inv.DeliveryPostalCode,
		}
		if inv.DeliveryCity != nil {
			resp.DeliveryAddress.City = *inv.DeliveryCity
		}
		if inv.DeliveryCountry != nil {
			resp.DeliveryAddress.Country = *inv.DeliveryCountry
		}
	}
	if resp.ExchangeRate, err = floatFromNumeric(inv.ExchangeRate); err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	for _, l := range stored {
		line, err := lineResponse(l)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		resp.Lines = append(resp.Lines, line)
	}
	switch {
	case inv.BuyerName != nil:
		resp.CustomerName = inv.BuyerName
	case profile != nil:
		resp.CustomerName = &profile.Name
	}

	today := businessDay(s.deps.Clock())
	if inv.Status == statusIssued {
		rows, err := q.VatSummaries(ctx, inv.ID)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read document %d's VAT: %w", inv.ID, err)
		}
		for _, r := range rows {
			row, err := storedSummaryResponse(r)
			if err != nil {
				return gen.InvoicesInvoiceResponse{}, err
			}
			resp.VatSummaries = append(resp.VatSummaries, row)
		}
		for _, c := range []struct {
			dst *float64
			n   pgtype.Numeric
		}{
			{&resp.NetTotal, inv.NetTotal}, {&resp.VatTotal, inv.VatTotal},
			{&resp.GrossTotal, inv.GrossTotal}, {&resp.VatTotalNok, inv.VatTotalNok},
		} {
			if *c.dst, err = floatFromNumeric(c.n); err != nil {
				return gen.InvoicesInvoiceResponse{}, err
			}
		}
		resp.PdfStored = ptr(inv.PdfSha256 != nil)
		if issuedLate(inv.IssueDate.Time, deliveryEndOf(inv)) {
			resp.Warnings = append(resp.Warnings, warningIssuedLate)
		}
		return resp, nil
	}

	codes, err := vatCodesOn(ctx, q, pgDate(today))
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	lines, err := storedDraftLines(stored)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	rows, totals, _ := summarize(taxedLines(lines, codes), big.NewRat(1, 1))
	for _, r := range rows {
		resp.VatSummaries = append(resp.VatSummaries, summaryResponse(r))
	}
	resp.NetTotal, resp.VatTotal = floatFromRat(totals.net, 2), floatFromRat(totals.vat, 2)
	resp.GrossTotal, resp.VatTotalNok = floatFromRat(totals.gross, 2), floatFromRat(totals.vatNOK, 2)
	if profile != nil && profile.Currency != "" && profile.Currency != inv.Currency {
		resp.Warnings = append(resp.Warnings, warningCustomerCurrencyDiffers)
	}
	if issuedLate(today, deliveryEndOf(inv)) {
		resp.Warnings = append(resp.Warnings, warningIssuedLate)
	}
	latest, err := q.LatestIssueDate(ctx)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read the latest issue date: %w", err)
	}
	allowed := []openapi_types.Date{}
	for _, d := range allowedIssueDates(today, deliveryEndOf(inv), latest.Time) {
		allowed = append(allowed, wireDate(d))
	}
	resp.AllowedIssueDates = &allowed
	return resp, nil
}
```

**Create** `apps/server/internal/invoices/list.go`:

```go
package invoices

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /invoices (D4): every module list's page/pageSize
// convention, drafts first and then by number descending. There is no keyset
// cursor: number is NULL on every draft.

// The paging bounds every module's list uses; listMaxPage keeps page ×
// pageSize inside the int32 offset.
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
	listMaxPage         = math.MaxInt32 / listMaxPageSize
)

const invalidQueryTitle = "Invalid query parameters"

// validatePageParams is the paging rule, in the other modules' own words.
func validatePageParams(page, pageSize *int32) []string {
	var errs []string
	switch {
	case page == nil:
	case *page < 1:
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *page))
	case *page > listMaxPage:
		errs = append(errs, fmt.Sprintf("'page' must be at most %d, but was %d.", listMaxPage, *page))
	}
	if pageSize != nil && (*pageSize < 1 || *pageSize > listMaxPageSize) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", listMaxPageSize, *pageSize))
	}
	return errs
}

// pageParams is the validated paging as numbers.
func pageParams(page, pageSize *int32) (int32, int32) {
	p, size := int32(1), int32(listDefaultPageSize)
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		size = *pageSize
	}
	return p, size
}

// likePattern is a case-insensitive substring pattern with LIKE's own
// wildcards escaped.
func likePattern(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s) + "%"
}

// GetInvoices List invoices and credit notes
// (GET /api/v1/invoices)
func (s *server) GetInvoices(ctx context.Context, req gen.GetInvoicesRequestObject) (gen.GetInvoicesResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && *p.Status != statusDraft && *p.Status != statusIssued {
		errs = append(errs, fmt.Sprintf("'status' must be 'draft' or 'issued', but was '%s'.", *p.Status))
	}
	if p.Kind != nil && *p.Kind != kindInvoice && *p.Kind != kindCreditNote {
		errs = append(errs, fmt.Sprintf("'kind' must be 'invoice' or 'credit_note', but was '%s'.", *p.Kind))
	}
	if p.From != nil && p.To != nil && p.From.After(p.To.Time) {
		errs = append(errs, "'from' must be on or before 'to'.")
	}
	if len(errs) > 0 {
		return gen.GetInvoices400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	params := store.ListInvoicesParams{
		Status: p.Status, Kind: p.Kind, CustomerID: p.CustomerId,
		PageOffset: (page - 1) * pageSize, PageSize: pageSize,
	}
	if p.From != nil {
		params.IssuedFrom = pgDate(utcDay(p.From.Time))
	}
	if p.To != nil {
		params.IssuedTo = pgDate(utcDay(p.To.Time))
	}
	if p.Search != nil {
		if term := strings.TrimSpace(*p.Search); term != "" {
			params.SearchPattern = ptr(likePattern(term))
			if n, err := strconv.ParseInt(term, 10, 64); err == nil {
				params.SearchNumber = &n
			}
		}
	}
	q := store.New(s.deps.Pool)
	rows, err := q.ListInvoices(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("invoices: list: %w", err)
	}
	total, err := q.CountInvoices(ctx, store.CountInvoicesParams{
		Status: params.Status, Kind: params.Kind, CustomerID: params.CustomerID,
		SearchNumber: params.SearchNumber, SearchPattern: params.SearchPattern,
		IssuedFrom: params.IssuedFrom, IssuedTo: params.IssuedTo,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: count: %w", err)
	}

	// A draft has no buyer snapshot: the page's drafts are named by their
	// customers' current names, in one directory round trip.
	var draftCustomers []int32
	for _, r := range rows {
		if r.BuyerName == nil {
			draftCustomers = append(draftCustomers, r.CustomerID)
		}
	}
	entries, err := s.customerEntries(ctx, draftCustomers)
	if err != nil {
		return nil, err
	}
	names := make(map[int32]string, len(entries))
	for _, e := range entries {
		names[e.ID] = e.Name
	}

	data := make([]gen.InvoicesInvoiceListItem, 0, len(rows))
	for _, r := range rows {
		gross, err := floatFromNumeric(r.GrossTotal)
		if err != nil {
			return nil, err
		}
		item := gen.InvoicesInvoiceListItem{
			Id: r.ID, Kind: r.Kind, Status: r.Status, Number: r.Number, CustomerId: r.CustomerID,
			IssueDate: wireDateOf(r.IssueDate), DueDate: wireDateOf(r.DueDate), GrossTotal: gross,
			Currency: r.Currency, CreditsInvoiceId: r.CreditsInvoiceID, CustomerName: r.BuyerName,
		}
		if item.CustomerName == nil {
			if name, ok := names[r.CustomerID]; ok {
				item.CustomerName = &name
			}
		}
		data = append(data, item)
	}
	return gen.GetInvoices200JSONResponse(gen.PaginatedResponseOfInvoicesInvoiceListItem{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}
```

- [ ] **Step 5: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bun run gen:client
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go build ./... && mise exec -- go vet ./...
mise exec -- go test -count=1 ./internal/invoices/ ./internal/customers/... ./internal/openapi/ ./internal/integration/
mise exec -- golangci-lint run ./...
cd ../..
git diff --stat -- openapi/testdata/exchanges openapi/customers.yaml   # must print nothing
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task3.txt <<'MSG'
feat(invoices): drafts, the money and the list; Status and MergedInto on the billing profile

The customers contract change (invoices foundation design D10):
CustomerBillingProfile gains Status and MergedInto, filled by the customers
directory, so one read answers every gate an invoice keeps and its snapshot.
The contract comment now says archived customers still resolve so a past
invoice can be shown and credited, and Invoices refuses new ones.

Drafts (D4): created with the prefills from the billing profile read before
anything is written, replaced whole with their revision, deleted; the
customer gates in their order on create and every save; a day or a period
of delivery; only NOK. The money (D5): exact decimals from the JSON number's
text, gross less allowance per line, VAT per rate on the sum of the nets,
the bounds as 400s. GET /invoices/{id} and the list on the codebase's
page/pageSize. Every directory call goes through contractscalls.go, and the
harness fails a test that makes one under a lock.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/contracts/directory.go' 'apps/server/internal/customers/directory.go' 'apps/server/internal/customers/directory_test.go' 'apps/server/internal/customers/queries/customers.sql' 'apps/server/internal/customers/store/customers.sql.go' 'apps/server/internal/invoices/contractscalls.go' 'apps/server/internal/invoices/drafts.go' 'apps/server/internal/invoices/drafts_test.go' 'apps/server/internal/invoices/export_test.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/issuedate.go' 'apps/server/internal/invoices/list.go' 'apps/server/internal/invoices/list_test.go' 'apps/server/internal/invoices/main_test.go' 'apps/server/internal/invoices/money.go' 'apps/server/internal/invoices/queries/invoices.sql' 'apps/server/internal/invoices/queries/lines.sql' 'apps/server/internal/invoices/queries/vatcodes.sql' 'apps/server/internal/invoices/responses.go' 'apps/server/internal/invoices/server.go' 'apps/server/internal/invoices/store/invoices.sql.go' 'apps/server/internal/invoices/store/lines.sql.go' 'apps/server/internal/invoices/store/vatcodes.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git commit -F /tmp/claude-1000/msg-invoices-task3.txt -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/contracts/directory.go' 'apps/server/internal/customers/directory.go' 'apps/server/internal/customers/directory_test.go' 'apps/server/internal/customers/queries/customers.sql' 'apps/server/internal/customers/store/customers.sql.go' 'apps/server/internal/invoices/contractscalls.go' 'apps/server/internal/invoices/drafts.go' 'apps/server/internal/invoices/drafts_test.go' 'apps/server/internal/invoices/export_test.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/issuedate.go' 'apps/server/internal/invoices/list.go' 'apps/server/internal/invoices/list_test.go' 'apps/server/internal/invoices/main_test.go' 'apps/server/internal/invoices/money.go' 'apps/server/internal/invoices/queries/invoices.sql' 'apps/server/internal/invoices/queries/lines.sql' 'apps/server/internal/invoices/queries/vatcodes.sql' 'apps/server/internal/invoices/responses.go' 'apps/server/internal/invoices/server.go' 'apps/server/internal/invoices/store/invoices.sql.go' 'apps/server/internal/invoices/store/lines.sql.go' 'apps/server/internal/invoices/store/vatcodes.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 4: Issue, in one serialised transaction, and immutability proven in SQL (D6, D9)

`POST /invoices/{id}/issue` in the spec's exact order: before the transaction the draft (404, `invoice_issued`), the store (503 `storage_unavailable`, before any number exists) and, for an invoice, the billing profile; inside it the document `FOR UPDATE`, `invoice_changed` when a merge re-pointed it meanwhile, the settings `FOR SHARE`, the number, and only then every check — the issue-date rule with § 5-1-3's fifteen-days exception read after the counter lock — then the line snapshots, the summaries and last the row. The tests race issues, inject a failure after the allocation, race a settings write deterministically behind the lock, and bypass the handlers to prove the triggers.

**Files:**
- Create: `apps/server/internal/invoices/issue.go`, `apps/server/internal/invoices/issue_test.go`
- Modify: `apps/server/internal/invoices/export_test.go`, `apps/server/internal/invoices/harness_test.go`, `apps/server/internal/invoices/queries/counters.sql`, `apps/server/internal/invoices/queries/invoices.sql`, `apps/server/internal/invoices/queries/lines.sql`, `apps/server/internal/invoices/queries/settings.sql`, `openapi/invoices.yaml`
- Generated (commit them; never edit by hand): `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/counters.sql.go`, `apps/server/internal/invoices/store/invoices.sql.go`, `apps/server/internal/invoices/store/lines.sql.go`, `apps/server/internal/invoices/store/settings.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/db/migrations/00034_invoices_baseline.sql` (the triggers), `apps/server/internal/invoices/{drafts.go,money.go,issuedate.go}`, `expenses/flow_concurrency_test.go`

**Interfaces:**
- Produces wire: `POST /invoices/{id}/issue` with `InvoicesIssueRequest {issueDate?}` → 200 `InvoicesInvoiceResponse`; 409 codes `invoice_issued`, `invoice_changed`, `seller_incomplete`, `no_lines`, `delivery_date_missing`, `issue_date_not_allowed` (+`allowedIssueDates`), the customer gates, `buyer_incomplete`, `vat_code_inactive`/`vat_code_not_valid` (+`linePosition`), `vat_not_registered`, `category_o_not_allowed`, `reverse_charge_needs_org_number`, `vat_codes_ambiguous`; 503 `storage_unavailable`.
- Produces SQL: `ShareSettings` (FOR SHARE), `AllocateNumber(series_start)`, `SnapshotLine`, `InsertVatSummary`, `IssueDocument`.
- Produces Go: `buyerSnapshot`, `buyerComplete`, `sellerSnapshot`, `keepBuyer`, `invoiceIssueChecks`, `issuePlan`, `issuedLine`, `cannotIssue`; the test seam `issueAfterAllocation` (export `SetIssueAfterAllocation`).

- [ ] **Step 1: The tests: the issue, the snapshots, the date rule, every refusal, the races, and the triggers**

**Append** to the end of `apps/server/internal/invoices/export_test.go`:

```go

// SetIssueAfterAllocation installs a hook the issue calls inside its
// transaction right after the number is allocated, and answers the function
// that removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetIssueAfterAllocation(hook func(ctx context.Context, invoiceID int64) error) func() {
	issueAfterAllocation = hook
	return func() { issueAfterAllocation = nil }
}
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)
```

**with**:

```go
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
	// onProfile, when set, runs after every BillingProfile read — what
	// happens between the directory read and the issue's transaction.
	onProfile func(id int32)
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go

func (f *fakeCustomers) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Delete,
```

**with**:

```go

func (f *fakeCustomers) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	f.mu.Lock()
	p, ok := f.profiles[id]
	after := f.onProfile
	f.mu.Unlock()
	if after != nil {
		after(id)
	}
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// edit changes one customer's profile, as the customers module would.
func (f *fakeCustomers) edit(id int32, change func(*contracts.CustomerBillingProfile)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[id]
	change(&p)
	f.profiles[id] = p
}

// afterProfileRead sets onProfile.
func (f *fakeCustomers) afterProfileRead(fn func(id int32)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onProfile = fn
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Delete,
```

**Create** `apps/server/internal/invoices/issue_test.go`:

```go
package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func issuePath(id int64) string { return fmt.Sprintf("%s/%d/issue", invoicesPath, id) }

func issuer(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:issue")
}

// issueWith issues id with date ("" for today) and answers the response.
func issueWith(t *testing.T, h *harness, id int64, date string) *modtest.Response {
	t.Helper()
	body := map[string]any{}
	if date != "" {
		body["issueDate"] = date
	}
	return issuer(t, h).Do(http.MethodPost, issuePath(id), body)
}

// issued issues id today and answers the document, failing unless it was.
func issued(t *testing.T, h *harness, id int64) invoiceJSON {
	t.Helper()
	res := issueWith(t, h, id, "")
	if res.Status != http.StatusOK {
		t.Fatalf("issue %d = %d %s, want 200", id, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// readyToIssue is an installation with a complete, VAT-registered seller.
func readyToIssue(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	return h
}

// refusedWith asserts the issue of id is a 409 with code and answers it.
func refusedWith(t *testing.T, h *harness, id int64, date, code string) problemJSON {
	t.Helper()
	res := issueWith(t, h, id, date)
	if res.Status != http.StatusConflict {
		t.Fatalf("issue %d = %d %s, want 409 %s", id, res.Status, res.Body, code)
	}
	p := problemOf(t, res)
	if p.Code != code {
		t.Fatalf("issue %d = %s (%s), want %s", id, p.Code, p.Detail, code)
	}
	return p
}

// counterNext is the counter's next_value, 0 without a row.
func counterNext(t *testing.T, h *harness) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT coalesce((SELECT next_value FROM invoices.counters WHERE counter_name = 'documents'), 0)`)
}

// An invoice issued today: the next number, today's date, the due date from
// its terms, both snapshots, the rates as they stand today, and NOK at 1.
func TestIssue_AnInvoice(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 1200, vat25), line("Kurs", 1, 5000, vatExempt)))

	inv := issued(t, h, draft.ID)
	if inv.Status != "issued" || inv.Number == nil || *inv.Number != 1 || *inv.IssueDate != "2026-09-12" {
		t.Fatalf("issued = %+v, want number 1 on 2026-09-12", inv)
	}
	if *inv.DueDate != "2026-10-12" || *inv.ExchangeRateDate != "2026-09-12" || inv.ExchangeRate != 1 {
		t.Errorf("due %v, rate date %v, rate %v; want 2026-10-12 (30 days), 2026-09-12, 1", *inv.DueDate, *inv.ExchangeRateDate, inv.ExchangeRate)
	}
	if inv.NetTotal != 17000 || inv.VatTotal != 3000 || inv.GrossTotal != 20000 || inv.VatTotalNok != 3000 {
		t.Errorf("totals = %v %v %v %v, want 17000, 3000, 20000, 3000", inv.NetTotal, inv.VatTotal, inv.GrossTotal, inv.VatTotalNok)
	}
	if l := inv.Lines[0]; l.VatRatePercent == nil || *l.VatRatePercent != 25 || *l.VatCategory != "S" || *l.SafTCode != "3" || l.ExemptionReason != nil {
		t.Errorf("line 1 snapshot = %+v, want 25 %% S 3", l)
	}
	if l := inv.Lines[1]; *l.VatCategory != "E" || l.ExemptionReason == nil || *l.ExemptionReason != "Unntatt fra merverdiavgift (mval. kap. 3)" {
		t.Errorf("line 2 snapshot = %+v, want E with its reason", l)
	}
	if len(inv.VatSummaries) != 2 || inv.VatSummaries[1].VatCategory != "E" || inv.VatSummaries[1].TaxableAmount != 5000 {
		t.Errorf("summaries = %+v, want the 25 %% row and the E row", inv.VatSummaries)
	}
	if inv.AllowedIssueDates != nil || inv.PdfStored == nil || len(inv.Warnings) != 0 {
		t.Errorf("issued = allowed %v, pdfStored %v, warnings %v", inv.AllowedIssueDates, inv.PdfStored, inv.Warnings)
	}
	if next := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Mer", 1, 100, vat25))).ID); *next.Number != 2 {
		t.Errorf("the second issue = %d, want 2", *next.Number)
	}
	if res := creator(t, h).Do(http.MethodPost, issuePath(draft.ID), map[string]any{}); res.Status != http.StatusForbidden {
		t.Errorf("issue without invoices:issue = %d, want 403", res.Status)
	}
	if res := issueWith(t, h, draft.ID, ""); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
		t.Errorf("issuing again = %d %s, want 409 invoice_issued", res.Status, res.Body)
	}
	if res := issueWith(t, h, 424242, ""); res.Status != http.StatusNotFound {
		t.Errorf("issuing an unknown id = %d, want 404", res.Status)
	}
}

// Every buyer and seller snapshot column (D4); a later change to the customer
// or to the settings changes nothing on an issued document.
func TestIssue_TheSnapshots(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)

	acme := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	b := acme.Buyer
	if b == nil || b.CustomerNumber != 10001 || b.Type != "business" || b.Name != "Acme AS Norge" ||
		b.OrganisationNumber == nil || *b.OrganisationNumber != "923609016" || b.ForeignID != nil ||
		*b.AddressLine1 != "Kundeveien 2" || *b.PostalCode != "0150" || *b.City != "Oslo" || *b.Country != "NO" ||
		*b.PeppolID != "0192:923609016" || *b.Gln != "7080000000001" || b.Language != "nb" {
		t.Errorf("Acme's snapshot = %+v", b)
	}
	s := acme.Seller
	if s == nil || s.LegalName != "Kraft-Verket AS" || s.OrganisationNumber != "974760673" || !s.VatRegistered ||
		!s.InForetaksregisteret || s.BankAccount != "86011117947" || s.FooterText != "Takk for handelen." {
		t.Errorf("the seller snapshot = %+v", s)
	}
	if acme.CustomerName == nil || *acme.CustomerName != "Acme AS Norge" {
		t.Errorf("an issued document's name = %v, want its snapshot's", acme.CustomerName)
	}

	// The legal name when there is one, else the name; a foreign business's id
	// with its country; English where the profile says so.
	if b := issued(t, h, createDraft(t, h, draftBody(customerNoTerms, line("A", 1, 100, vat25))).ID).Buyer; b.Name != "Uten Vilkår AS" {
		t.Errorf("no legal name: %q, want the name", b.Name)
	}
	foreign := issued(t, h, createDraft(t, h, draftBody(customerForeign, line("A", 1, 100, vatExport))).ID).Buyer
	if foreign.OrganisationNumber != nil || foreign.ForeignID == nil || *foreign.ForeignID != "SE556677889901" || foreign.Language != "en" {
		t.Errorf("a Swedish business = %+v, want no organisation number, SE556677889901, en", foreign)
	}
	if person := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID).Buyer; person.Type != "person" || person.OrganisationNumber != nil || person.ForeignID != nil {
		t.Errorf("a person = %+v, want no identifier", person)
	}

	// Later changes change nothing issued.
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.LegalName, p.InvoiceAddress = "Nytt Navn AS", nil })
	body := completeSeller(2)
	body["legalName"] = "Kraft-Verket Holding AS"
	saveSeller(t, h, body)
	if again := getInvoice(t, h, acme.ID); again.Buyer.Name != "Acme AS Norge" || again.Seller.LegalName != "Kraft-Verket AS" {
		t.Errorf("after the changes = buyer %q seller %q, want both as issued", again.Buyer.Name, again.Seller.LegalName)
	}
}

// The issue-date rule (D6): today, or the last day of the previous month
// while today's calendar day is at most 15 and the delivery ended by then —
// and never before the latest issue date.
func TestIssue_TheIssueDateRule(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t) // 2026-09-12
	august := func() int64 {
		body := draftBody(customerAcme, line("A", 1, 100, vat25))
		body["deliveryDate"] = "2026-08-20"
		return createDraft(t, h, body).ID
	}

	for _, day := range []string{"2026-09-13", "2026-09-05", "2026-09-01"} {
		p := refusedWith(t, h, august(), day, "issue_date_not_allowed")
		if !slices.Equal(p.AllowedIssueDates, []string{"2026-08-31", "2026-09-12"}) {
			t.Errorf("%s: allowed = %v, want 2026-08-31 and 2026-09-12", day, p.AllowedIssueDates)
		}
	}
	h.Advance(3 * 24 * time.Hour) // the 15th
	if inv := getInvoice(t, h, august()); !slices.Equal(inv.AllowedIssueDates, []string{"2026-08-31", "2026-09-15"}) {
		t.Errorf("a draft's allowedIssueDates on the 15th = %v", inv.AllowedIssueDates)
	}
	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	body["deliveryDate"] = "2026-09-01"
	p := refusedWith(t, h, createDraft(t, h, body).ID, "2026-08-31", "issue_date_not_allowed")
	if !slices.Equal(p.AllowedIssueDates, []string{"2026-09-15"}) {
		t.Errorf("delivered in September: allowed = %v, want only today", p.AllowedIssueDates)
	}
	previousMonth := august()
	res := issueWith(t, h, previousMonth, "2026-08-31")
	if res.Status != http.StatusOK {
		t.Fatalf("the last day of August on the 15th = %d %s, want 200", res.Status, res.Body)
	}
	// Once a document is dated in the current month, the previous month's day
	// is refused: dates follow numbers.
	issued(t, h, august())
	refusedWith(t, h, august(), "2026-08-31", "issue_date_not_allowed")
	h.Advance(24 * time.Hour) // the 16th
	p = refusedWith(t, h, august(), "2026-08-31", "issue_date_not_allowed")
	if !slices.Equal(p.AllowedIssueDates, []string{"2026-09-16"}) {
		t.Errorf("on the 16th: allowed = %v, want only today", p.AllowedIssueDates)
	}
}

// issued_late warns when the delivery ended more than a month before the
// issue date; the issue still succeeds (§ 5-2-2).
func TestIssue_IssuedLateWarnsAndIssues(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	delete(body, "deliveryDate")
	body["deliveryFrom"], body["deliveryTo"] = "2026-07-01", "2026-07-31"

	inv := issued(t, h, createDraft(t, h, body).ID)
	if !slices.Equal(inv.Warnings, []string{"issued_late"}) {
		t.Errorf("warnings = %v, want issued_late", inv.Warnings)
	}
}

// Every refusal of D6 step 5, each rolling the number back: the issue that
// finally succeeds gets the series' first number.
func TestIssue_TheRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	draftFor := func(customer int32, lines ...map[string]any) int64 {
		return createDraft(t, h, draftBody(customer, lines...)).ID
	}
	standard := line("A", 1, 100, vat25)

	// The seller: each missing field.
	for _, field := range []string{"legalName", "organisationNumber", "addressLine1", "postalCode", "city", "bankAccount"} {
		body := completeSeller(0)
		body[field] = ""
		var current settingsJSON
		h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&current)
		body["revision"] = current.Revision
		saveSeller(t, h, body)
		if p := refusedWith(t, h, draftFor(customerAcme, standard), "", "seller_incomplete"); !strings.Contains(p.Detail, field) {
			t.Errorf("seller_incomplete without %s: %q, want it named", field, p.Detail)
		}
	}
	var current settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&current)
	saveSeller(t, h, completeSeller(current.Revision))

	refusedWith(t, h, draftFor(customerAcme), "", "no_lines")
	noDelivery := draftBody(customerAcme, standard)
	delete(noDelivery, "deliveryDate")
	refusedWith(t, h, createDraft(t, h, noDelivery).ID, "", "delivery_date_missing")

	// The buyer: a person with no address is refused; a business with an
	// organisation number and no address passes.
	refusedWith(t, h, draftFor(customerNoAddress, standard), "", "buyer_incomplete")
	h.customers.edit(customerNoTerms, func(p *contracts.CustomerBillingProfile) { p.InvoiceAddress = nil })

	// Reverse charge needs the buyer's organisation number.
	reverse := line("Byggetjeneste", 1, 1000, vatReverse)
	refusedWith(t, h, draftFor(customerForeign, reverse), "", "reverse_charge_needs_org_number")

	// A registered seller issues no O line.
	refusedWith(t, h, draftFor(customerAcme, line("Utenfor", 1, 100, vatOutside)), "", "category_o_not_allowed")

	// A code deactivated after the save; a code with no period on the day.
	inactive := draftFor(customerAcme, standard, line("Mat", 1, 100, vat15))
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat15)
	if p := refusedWith(t, h, inactive, "", "vat_code_inactive"); p.LinePosition == nil || *p.LinePosition != 2 {
		t.Errorf("vat_code_inactive line = %v, want 2", p.LinePosition)
	}
	h.Exec(t, `UPDATE invoices.vat_codes SET active = true WHERE id = $1`, vat15)
	var future vatCodeJSON
	manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "25N", "name": "Ny sats", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-10-01",
	}).JSON(&future)
	if p := refusedWith(t, h, draftFor(customerAcme, line("Fremtid", 1, 100, future.ID)), "", "vat_code_not_valid"); p.LinePosition == nil || *p.LinePosition != 1 {
		t.Errorf("vat_code_not_valid line = %v, want 1", p.LinePosition)
	}

	// Two codes at one (category, rate) with different SAF-T codes.
	var twin vatCodeJSON
	manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "3B", "name": "Tvilling", "safTCode": "3B", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01",
	}).JSON(&twin)
	refusedWith(t, h, draftFor(customerAcme, standard, line("B", 1, 100, twin.ID)), "", "vat_codes_ambiguous")

	// The customer gates, again at issue.
	blocked := draftFor(customerAcme, standard)
	h.customers.setStatus(customerAcme, "disabled")
	refusedWith(t, h, blocked, "", "customer_blocked")
	h.customers.setStatus(customerAcme, "active")

	if n := counterNext(t, h); n != 0 {
		t.Errorf("after only refusals the counter = %d, want no row: every refusal rolled its number back", n)
	}
	if inv := issued(t, h, draftFor(customerNoTerms, standard)); *inv.Number != 1 {
		t.Errorf("the first successful issue = %d, want 1", *inv.Number)
	}
	if inv := issued(t, h, draftFor(customerAcme, reverse)); *inv.Number != 2 {
		t.Errorf("reverse charge to a buyer with an organisation number = %d, want issued as 2", *inv.Number)
	}
}

// A seller outside the VAT register issues only O lines (research §2.1).
func TestIssue_ANonRegisteredSeller(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["vatRegistered"] = false
	saveSeller(t, h, body)

	refusedWith(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID, "", "vat_not_registered")
	refusedWith(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vatZero))).ID, "", "vat_not_registered")
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vatOutside))).ID)
	if inv.VatTotal != 0 || inv.Seller.VatRegistered {
		t.Errorf("an O invoice = vat %v, registered %v", inv.VatTotal, inv.Seller.VatRegistered)
	}
}

// A merge that re-points the draft between the directory read and the
// transaction is caught under the lock (invoice_changed).
func TestIssue_AMergeInBetweenIsInvoiceChanged(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.customers.afterProfileRead(func(id int32) {
		if id == customerAcme {
			h.Exec(t, `UPDATE invoices.invoices SET customer_id = $1 WHERE id = $2`, customerNoTerms, draft.ID)
		}
	})

	refusedWith(t, h, draft.ID, "", "invoice_changed")
	if n := counterNext(t, h); n != 0 {
		t.Errorf("counter = %d, want no number taken", n)
	}
}

// No object store: refused before any number is allocated (D6 step 2).
func TestIssue_WithoutAStoreNothingIsAllocated(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	saveSeller(t, h, completeSeller(1))
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))

	res := issueWith(t, h, draft.ID, "")
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Fatalf("issue without a store = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	if n := counterNext(t, h); n != 0 {
		t.Errorf("counter = %d, want no row", n)
	}
}

// The series start: the first issue allocates exactly it, the second one
// more; a rate changed from tomorrow applies to tomorrow's issue and not
// today's.
func TestIssue_TheSeriesStartAndARateChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["seriesStart"] = 1000
	saveSeller(t, h, body)
	res := manager(t, h).Do(http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25), map[string]any{"ratePercent": 26, "validFrom": "2026-09-13"})
	if res.Status != http.StatusCreated {
		t.Fatalf("rate change = %d %s", res.Status, res.Body)
	}
	first := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	second := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))

	today := issued(t, h, first.ID)
	h.Advance(24 * time.Hour)
	tomorrow := issued(t, h, second.ID)
	if *today.Number != 1000 || *tomorrow.Number != 1001 {
		t.Errorf("numbers = %d, %d, want 1000, 1001", *today.Number, *tomorrow.Number)
	}
	if today.VatTotal != 25 || tomorrow.VatTotal != 26 {
		t.Errorf("VAT = %v, %v, want the old 25 today and the new 26 tomorrow", today.VatTotal, tomorrow.VatTotal)
	}
}

// Two issues racing get consecutive numbers, and none is lost.
func TestIssue_RacingIssuesGetConsecutiveNumbers(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	var drafts []int64
	for range 6 {
		drafts = append(drafts, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	}
	var wg sync.WaitGroup
	numbers := make([]int64, len(drafts))
	for i, id := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var inv invoiceJSON
			res := issueWith(t, h, id, "")
			if res.Status != http.StatusOK {
				t.Errorf("racing issue %d = %d %s", id, res.Status, res.Body)
				return
			}
			res.JSON(&inv)
			numbers[i] = *inv.Number
		}()
	}
	wg.Wait()
	slices.Sort(numbers)
	if !slices.Equal(numbers, []int64{1, 2, 3, 4, 5, 6}) {
		t.Errorf("numbers = %v, want 1..6", numbers)
	}
}

// Two racing issues with different dates cannot give a later number an
// earlier date: the one that allocates second reads the first's date.
func TestIssue_RacingDatesStayMonotone(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.Advance(3 * 24 * time.Hour) // the 15th: the last day of August is allowed
	august := func() int64 {
		body := draftBody(customerAcme, line("A", 1, 100, vat25))
		body["deliveryDate"] = "2026-08-20"
		return createDraft(t, h, body).ID
	}
	for round := range 4 {
		a, b := august(), august()
		var wg sync.WaitGroup
		for _, c := range []struct {
			id   int64
			date string
		}{{a, "2026-08-31"}, {b, ""}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := issueWith(t, h, c.id, c.date)
				if res.Status != http.StatusOK && (res.Status != http.StatusConflict || problemOf(t, res).Code != "issue_date_not_allowed") {
					t.Errorf("round %d: issue %d = %d %s", round, c.id, res.Status, res.Body)
				}
			}()
		}
		wg.Wait()
	}
	if n := h.Count(t, `
		SELECT count(*) FROM invoices.invoices a JOIN invoices.invoices b ON a.number < b.number
		WHERE a.status = 'issued' AND b.status = 'issued' AND a.issue_date > b.issue_date`); n != 0 {
		t.Errorf("%d pairs have a later number with an earlier date", n)
	}
}

// A failure after the number is allocated rolls it back: the next issue gets
// it, and the series has no gap.
func TestIssue_AFailureAfterAllocationLeavesNoGap(t *testing.T) {
	h := readyToIssue(t)
	doomed := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		if id == doomed.ID {
			return errors.New("injected")
		}
		return nil
	})
	defer restore()

	res := issuer(t, h).Do(http.MethodPost, issuePath(doomed.ID), map[string]any{}, modtest.SkipContract("an injected failure answers the undeclared 500"))
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("the injected failure = %d, want 500", res.Status)
	}
	if inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25))).ID); *inv.Number != 1 {
		t.Errorf("the next issue = %d, want 1", *inv.Number)
	}
	if inv := getInvoice(t, h, doomed.ID); inv.Status != "draft" {
		t.Errorf("the failed draft = %s, want still a draft", inv.Status)
	}
}

// waitForALockWaiter polls until a session of this installation's database
// waits on a lock.
func waitForALockWaiter(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no session ever waited on a lock")
}

// A settings replace racing the first issue waits behind it and is refused:
// the settings never show a start that was not used (D2).
func TestIssue_ASettingsReplaceRacingTheFirstIssueWaitsAndIsRefused(t *testing.T) {
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	var put *modtest.Response
	done := make(chan struct{})
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		if id != draft.ID {
			return nil
		}
		go func() {
			defer close(done)
			body := completeSeller(2)
			body["seriesStart"] = 5000
			put = h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, body)
		}()
		waitForALockWaiter(t, h)
		return nil
	})
	defer restore()

	inv := issued(t, h, draft.ID)
	<-done
	if *inv.Number != 1 {
		t.Errorf("number = %d, want the start that was in force, 1", *inv.Number)
	}
	if put.Status != http.StatusConflict || problemOf(t, put).Code != "series_locked" {
		t.Errorf("the waiting replace = %d %s, want 409 series_locked", put.Status, put.Body)
	}
}

// A merge re-pointing documents and a settings write racing issues finish,
// every one, without a deadlock: the lock order is the document, the
// settings, the counter.
func TestIssue_NoDeadlockBesideAMergeAndASettingsWrite(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	var drafts []int64
	for range 4 {
		drafts = append(drafts, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	}
	var wg sync.WaitGroup
	for _, id := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := issueWith(t, h, id, "")
			if res.Status != http.StatusOK && (res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_changed") {
				t.Errorf("issue %d = %d %s", id, res.Status, res.Body)
			}
		}()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.Exec(t, `UPDATE invoices.invoices SET customer_id = $1 WHERE customer_id = $2`, customerAcme, customerAcme)
	}()
	go func() {
		defer wg.Done()
		body := completeSeller(2)
		if res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, body); res.Status != http.StatusOK {
			t.Errorf("the settings write = %d %s", res.Status, res.Body)
		}
	}()
	wg.Wait()
}

// Immutability in SQL too (D9): bypassing the handlers, an issued document's
// columns cannot change but for customer_id and the PDF set once; it cannot be
// deleted; its lines and summaries cannot be written. A draft's delete still
// cascades.
func TestIssue_AnIssuedDocumentIsImmutableInSQL(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	ctx := context.Background()
	immutable := func(sql string, args ...any) {
		t.Helper()
		_, err := h.Pool().Exec(ctx, sql, args...)
		if err == nil || !strings.Contains(err.Error(), "invoices: issued document is immutable") || !strings.Contains(err.Error(), "P0001") {
			t.Errorf("%s: %v, want the immutability refusal", sql, err)
		}
	}

	immutable(`UPDATE invoices.invoices SET note = 'endret' WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET revision = revision + 1 WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET number = 99 WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET internal_note = 'x' WHERE id = $1`, inv.ID)
	immutable(`DELETE FROM invoices.invoices WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.lines SET description = 'x' WHERE invoice_id = $1`, inv.ID)
	immutable(`DELETE FROM invoices.lines WHERE invoice_id = $1`, inv.ID)
	immutable(`INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id, line_gross, line_allowance, line_net)
		VALUES ($1, 9, 'x', 1, 1, 1, 1, 0, 1)`, inv.ID)
	immutable(`UPDATE invoices.vat_summaries SET vat_amount = 0 WHERE invoice_id = $1`, inv.ID)
	immutable(`DELETE FROM invoices.vat_summaries WHERE invoice_id = $1`, inv.ID)
	immutable(`INSERT INTO invoices.vat_summaries (invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount, vat_amount_nok)
		VALUES ($1, 'Z', 0, '5', 0, 0, 0)`, inv.ID)

	// What may move: the customer (the merge holder) and the PDF, once.
	h.Exec(t, `UPDATE invoices.invoices SET customer_id = $1 WHERE id = $2`, customerNoTerms, inv.ID)
	h.Exec(t, `UPDATE invoices.invoices SET pdf_object_key = 'documents/x.pdf', pdf_sha256 = repeat('a', 64) WHERE id = $1 AND pdf_sha256 IS NULL`, inv.ID)
	immutable(`UPDATE invoices.invoices SET pdf_sha256 = repeat('b', 64) WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET pdf_object_key = NULL WHERE id = $1`, inv.ID)
	if n := h.Count(t, `SELECT revision FROM invoices.invoices WHERE id = $1`, inv.ID); n != int(inv.Revision) {
		t.Errorf("revision = %d after the allowed updates, want %d unmoved", n, inv.Revision)
	}

	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, draft.ID)
	if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, draft.ID); n != 0 {
		t.Errorf("a deleted draft's lines = %d, want the cascade to take them", n)
	}
}
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 ./internal/invoices/ ; cd ../..   # FAIL: undefined issueAfterAllocation
```

- [ ] **Step 2: The contract and the queries**

**Insert** into `openapi/invoices.yaml`, immediately before the line `info:`:

```yaml
        InvoicesIssueRequest:
            description: 'POST /invoices/{id}/issue''s body (D6). issueDate is today (Oslo) when omitted; the only other date allowed is the last day of the previous month, while today''s calendar day is 15 or less and the delivery ended on or before it — and never a date before the latest issue date of any issued document.'
            properties:
                issueDate:
                    format: date
                    type: string
            type: object
```

**Insert** into `openapi/invoices.yaml`, immediately before the line `servers:`:

```yaml
    /api/v1/invoices/{id}/issue:
        post:
            description: 'Issues a draft — an invoice or a credit note — into the next number of the one series (D6). The billing profile is read first; then one transaction locks the document, shares the settings row, allocates the number, and only then checks every rule, so any refusal rolls the number back with it. The response is the issued document with its warnings (issued_late). The PDF is stored after the commit; pdfStored false means storing it failed and the next download stores it.'
            operationId: postInvoicesByIdIssue
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/InvoicesIssueRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesInvoiceResponse'
                    description: OK — the issued document.
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
                    description: Not Found — no document has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: 'Conflict — invoice_issued, invoice_changed, seller_incomplete, no_lines, delivery_date_missing, issue_date_not_allowed (with allowedIssueDates), customer_merged, customer_archived, customer_blocked, customer_missing, buyer_incomplete, vat_code_inactive and vat_code_not_valid (with linePosition), vat_not_registered, category_o_not_allowed, reverse_charge_needs_org_number, vat_codes_ambiguous, credit_exceeds_line (with linePosition) or credit_exceeds_invoice.'
                "503":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Service Unavailable — storage_unavailable, before any number is allocated. An issued number whose PDF can never be stored is not allowed to exist.
            summary: Issue a draft
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:issue
```

**Append** to the end of `apps/server/internal/invoices/queries/counters.sql`:

```sql

-- name: AllocateNumber :one
-- AllocateNumber takes the next number of the one series (D2): the first
-- allocation is series_start, every later one one more. The lock on the counter row
-- is held until the issue commits, so it is the one thing that serialises
-- two issues, and a rolled-back issue rolls its number back with it.
INSERT INTO invoices.counters (counter_name, next_value)
VALUES ('documents', sqlc.arg(series_start)::bigint + 1)
ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS allocated;
```

**Append** to the end of `apps/server/internal/invoices/queries/invoices.sql`:

```sql

-- name: IssueDocument :one
-- IssueDocument turns a draft into an issued document (D6 step 6), last of the
-- issue's writes: the number, the dates, both snapshots and the totals. From
-- this row's commit on the trigger refuses every change but the merge
-- holder's customer_id and the PDF set once (D9).
UPDATE invoices.invoices SET
    status = 'issued',
    number = @number,
    issue_date = @issue_date,
    due_date = @due_date,
    exchange_rate_date = @issue_date,
    buyer_customer_number = @buyer_customer_number,
    buyer_type = @buyer_type,
    buyer_name = @buyer_name,
    buyer_organisation_number = @buyer_organisation_number,
    buyer_foreign_id = @buyer_foreign_id,
    buyer_address_line1 = @buyer_address_line1,
    buyer_address_line2 = @buyer_address_line2,
    buyer_postal_code = @buyer_postal_code,
    buyer_city = @buyer_city,
    buyer_region = @buyer_region,
    buyer_country = @buyer_country,
    buyer_peppol_id = @buyer_peppol_id,
    buyer_gln = @buyer_gln,
    buyer_language = @buyer_language,
    seller_legal_name = @seller_legal_name,
    seller_organisation_number = @seller_organisation_number,
    seller_vat_registered = @seller_vat_registered,
    seller_in_foretaksregisteret = @seller_in_foretaksregisteret,
    seller_address_line1 = @seller_address_line1,
    seller_address_line2 = @seller_address_line2,
    seller_postal_code = @seller_postal_code,
    seller_city = @seller_city,
    seller_country = @seller_country,
    seller_bank_account = @seller_bank_account,
    seller_iban = @seller_iban,
    seller_bic = @seller_bic,
    seller_email = @seller_email,
    seller_footer_text = @seller_footer_text,
    net_total = @net_total,
    vat_total = @vat_total,
    gross_total = @gross_total,
    vat_total_nok = @vat_total_nok,
    issued_at = @now::timestamptz,
    issued_by_user_id = @issued_by_user_id,
    updated_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id AND status = 'draft'
RETURNING *;
```

**Append** to the end of `apps/server/internal/invoices/queries/lines.sql`:

```sql

-- name: SnapshotLine :exec
-- SnapshotLine writes the VAT a line was issued with (D6 step 6). It runs while
-- the document is still a draft; the trigger refuses it afterwards (D9).
UPDATE invoices.lines SET
    vat_rate_percent = @vat_rate_percent,
    vat_category = @vat_category,
    saf_t_code = @saf_t_code,
    exemption_reason = @exemption_reason
WHERE id = @id;

-- name: InsertVatSummary :exec
INSERT INTO invoices.vat_summaries (
    invoice_id, vat_category, rate_percent, saf_t_code, exemption_reason, taxable_amount, vat_amount, vat_amount_nok
) VALUES (
    @invoice_id, @vat_category, @rate_percent, @saf_t_code, @exemption_reason, @taxable_amount, @vat_amount, @vat_amount_nok
);
```

**Append** to the end of `apps/server/internal/invoices/queries/settings.sql`:

```sql

-- name: ShareSettings :one
-- ShareSettings takes the settings row FOR SHARE: the issue's seller snapshot
-- and series start (D6 step 3). Two issues share it; PUT /settings and a rate
-- change wait for both to commit.
SELECT * FROM invoices.settings WHERE id = 1 FOR SHARE;
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 3: The issue**

**Create** `apps/server/internal/invoices/issue.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the issue (D6): one serialised transaction that turns a draft
// into a numbered, immutable salgsdokument. The order is the design's and it
// matters. Before the transaction: the draft is read, the object store is
// checked, and an invoice's billing profile is read — no lock is held across a
// directory call. In the transaction: the document is locked, the settings row
// shared, the number allocated, and only then is any rule checked, because the
// counter row is the one thing that serialises two issues and every check
// that depends on other documents must run after it. Any refusal rolls the
// whole transaction back, the number with it. The lock order is always the
// document, then the settings row, then the counter, then — for a credit note
// — the original; nothing else takes these in another order.

// The codes and titles of the issue's refusals.
const (
	codeStorageUnavailable      = "storage_unavailable"
	codeInvoiceChanged          = "invoice_changed"
	codeSellerIncomplete        = "seller_incomplete"
	codeNoLines                 = "no_lines"
	codeDeliveryDateMissing     = "delivery_date_missing"
	codeIssueDateNotAllowed     = "issue_date_not_allowed"
	codeBuyerIncomplete         = "buyer_incomplete"
	codeVatCodeInactive         = "vat_code_inactive"
	codeVatCodeNotValid         = "vat_code_not_valid"
	codeVatNotRegistered        = "vat_not_registered"
	codeCategoryONotAllowed     = "category_o_not_allowed"
	codeReverseChargeNeedsOrgNr = "reverse_charge_needs_org_number"
	codeVatCodesAmbiguous       = "vat_codes_ambiguous"
	storageUnavailableTitle     = "Document storage is unavailable"
	cannotIssueTitle            = "The document cannot be issued"
)

// issueAfterAllocation is called inside the issue's transaction right after
// the number is allocated, so a test can make the transaction fail there and
// prove the number rolls back with it. nil in production.
var issueAfterAllocation func(ctx context.Context, invoiceID int64) error

// cannotIssue is one of the issue's 409s.
func cannotIssue(code, detail string) *gen.InvoicesConflictProblem {
	return ptr(conflict(code, cannotIssueTitle, detail))
}

// issuedLine is one line as it will be issued: its net and the VAT treatment
// its snapshot records.
type issuedLine struct {
	id       int64
	position int32
	taxed    taxedLine
}

// issuePlan is what the checks decided the issue writes: the lines' VAT, and
// for an invoice the buyer snapshot (a credit note's draft already carries its
// original's).
type issuePlan struct {
	lines []issuedLine
	buyer *store.IssueDocumentParams
}

// buyerSnapshot is D4's buyer snapshot from the billing profile.
func buyerSnapshot(p *contracts.CustomerBillingProfile) *store.IssueDocumentParams {
	nonEmpty := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	b := &store.IssueDocumentParams{
		BuyerCustomerNumber: &p.CustomerNumber, BuyerType: &p.Type,
		BuyerPeppolID: nonEmpty(p.PeppolID), BuyerGln: nonEmpty(p.GLN),
	}
	name := p.Name
	if p.LegalName != "" {
		name = p.LegalName
	}
	b.BuyerName = &name
	if p.Type == "business" && p.LegalID != "" {
		if p.LegalCountry == "NO" {
			b.BuyerOrganisationNumber = &p.LegalID
		} else if p.LegalCountry != "" {
			b.BuyerForeignID = ptr(p.LegalCountry + p.LegalID)
		}
	}
	if a := p.InvoiceAddress; a != nil {
		b.BuyerAddressLine1, b.BuyerAddressLine2 = nonEmpty(a.Line1), nonEmpty(a.Line2)
		b.BuyerPostalCode, b.BuyerCity = nonEmpty(a.PostalCode), nonEmpty(a.City)
		b.BuyerRegion, b.BuyerCountry = nonEmpty(a.Region), nonEmpty(strings.ToUpper(a.Country))
	}
	language := "nb"
	if p.Language == "en" {
		language = "en"
	}
	b.BuyerLanguage = &language
	return b
}

// buyerComplete is § 5-1-2's buyer: a complete address — line 1, city and
// country, and a postal code too when the country is NO — or an organisation
// number.
func buyerComplete(b *store.IssueDocumentParams) bool {
	if b.BuyerOrganisationNumber != nil {
		return true
	}
	set := func(s *string) bool { return s != nil && *s != "" }
	if !set(b.BuyerAddressLine1) || !set(b.BuyerCity) || !set(b.BuyerCountry) {
		return false
	}
	return *b.BuyerCountry != "NO" || set(b.BuyerPostalCode)
}

// dateList is dates as a person reads them.
func dateList(dates []time.Time) string {
	if len(dates) == 0 {
		return "none"
	}
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		out = append(out, d.Format(time.DateOnly))
	}
	return strings.Join(out, ", ")
}

// invoiceIssueChecks are the checks only an invoice keeps (D6 step 5): the
// customer gates on the profile read before the transaction, the buyer, and
// the VAT codes as they stand on the issue date.
func invoiceIssueChecks(ctx context.Context, txq *store.Queries, profile *contracts.CustomerBillingProfile,
	settings store.InvoicesSetting, issueDate time.Time, lines []store.InvoicesLine,
) (issuePlan, *gen.InvoicesConflictProblem, error) {
	if refusal := customerGate(profile); refusal != nil {
		return issuePlan{}, refusal, nil
	}
	plan := issuePlan{buyer: buyerSnapshot(profile)}
	if !buyerComplete(plan.buyer) {
		return issuePlan{}, cannotIssue(codeBuyerIncomplete,
			"The buyer has neither a complete address nor an organisation number (§ 5-1-2)."), nil
	}
	codes, err := vatCodesOn(ctx, txq, pgDate(issueDate))
	if err != nil {
		return issuePlan{}, nil, err
	}
	for _, l := range lines {
		code := codes[l.VatCodeID]
		if !code.active {
			r := cannotIssue(codeVatCodeInactive, fmt.Sprintf("Line %d's VAT code is no longer offered.", l.Position))
			r.LinePosition = &l.Position
			return issuePlan{}, r, nil
		}
		if code.rate == nil {
			r := cannotIssue(codeVatCodeNotValid, fmt.Sprintf("Line %d's VAT code has no rate on %s.",
				l.Position, issueDate.Format(time.DateOnly)))
			r.LinePosition = &l.Position
			return issuePlan{}, r, nil
		}
		net, err := ratFromNumeric(l.LineNet)
		if err != nil {
			return issuePlan{}, nil, err
		}
		plan.lines = append(plan.lines, issuedLine{id: l.ID, position: l.Position, taxed: taxedLine{
			net: net, category: code.category, rate: code.rate, safT: code.safT, reason: code.reason,
		}})
	}
	for _, l := range plan.lines {
		switch {
		case !settings.VatRegistered && l.taxed.category != "O":
			return issuePlan{}, cannotIssue(codeVatNotRegistered, fmt.Sprintf(
				"The seller is not VAT-registered, so every line is outside the VAT act (category O); line %d is not.", l.position)), nil
		case settings.VatRegistered && l.taxed.category == "O":
			return issuePlan{}, cannotIssue(codeCategoryONotAllowed, fmt.Sprintf(
				"The seller is VAT-registered, so no line may be outside the VAT act (category O); line %d is.", l.position)), nil
		case l.taxed.category == "AE" && plan.buyer.BuyerOrganisationNumber == nil:
			return issuePlan{}, cannotIssue(codeReverseChargeNeedsOrgNr, fmt.Sprintf(
				"Line %d is reverse charge, which needs the buyer's organisation number (§ 5-1-1).", l.position)), nil
		}
	}
	return plan, nil, nil
}

// PostInvoicesByIdIssue Issue a draft
// (POST /api/v1/invoices/{id}/issue)
func (s *server) PostInvoicesByIdIssue(ctx context.Context, req gen.PostInvoicesByIdIssueRequestObject) (gen.PostInvoicesByIdIssueResponseObject, error) {
	q := store.New(s.deps.Pool)
	draft, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdIssue404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if draft.Status != statusDraft {
		return gen.PostInvoicesByIdIssue409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	}
	if !s.storageConfigured {
		status := int32(503)
		code, title := codeStorageUnavailable, storageUnavailableTitle
		detail := "This installation has no object store, so an issued document's PDF could never be stored. Nothing was issued."
		return gen.PostInvoicesByIdIssue503ApplicationProblemPlusJSONResponse(gen.InvoicesConflictProblem{
			Code: &code, Title: &title, Detail: &detail, Status: &status,
		}), nil
	}
	var profile *contracts.CustomerBillingProfile
	if draft.Kind == kindInvoice {
		if profile, err = s.customerProfile(ctx, draft.CustomerID); err != nil {
			return nil, err
		}
	}
	today := businessDay(s.deps.Clock())
	issueDate := today
	if req.Body.IssueDate != nil {
		issueDate = utcDay(req.Body.IssueDate.Time)
	}

	var refusal *gen.InvoicesConflictProblem
	var issued store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		// 1. The document, FOR UPDATE: two issues of one draft queue here.
		locked, err := txq.LockInvoice(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		if locked.Status != statusDraft {
			refusal = ptr(invoiceIssued())
			return errRefused
		}
		// 2. A merge may have re-pointed the draft since the profile was read.
		if locked.Kind == kindInvoice && locked.CustomerID != draft.CustomerID {
			refusal = ptr(conflict(codeInvoiceChanged, cannotIssueTitle, "The invoice changed; try again."))
			return errRefused
		}
		// 3. The settings row FOR SHARE: the seller snapshot and the start.
		settings, err := txq.ShareSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: share the settings: %w", err)
		}
		// 4. The number.
		number, err := txq.AllocateNumber(ctx, settings.SeriesStart)
		if err != nil {
			return fmt.Errorf("invoices: allocate a number: %w", err)
		}
		if issueAfterAllocation != nil {
			if err := issueAfterAllocation(ctx, locked.ID); err != nil {
				return err
			}
		}
		// 5. Only now, the checks.
		lines, err := txq.Lines(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d's lines: %w", locked.ID, err)
		}
		deliveryEnd := deliveryEndOf(locked)
		latest, err := txq.LatestIssueDate(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the latest issue date: %w", err)
		}
		allowed := allowedIssueDates(today, deliveryEnd, latest.Time)
		switch missing := sellerMissingFields(settings); {
		case len(missing) > 0:
			refusal = cannotIssue(codeSellerIncomplete, "The seller record still lacks: "+strings.Join(missing, ", ")+".")
		case len(lines) == 0:
			refusal = cannotIssue(codeNoLines, "A document needs at least one line.")
		case deliveryEnd.IsZero():
			refusal = cannotIssue(codeDeliveryDateMissing, "A document states when it was delivered: a day or a period (§ 5-1-1 nr. 4).")
		case !containsDate(allowed, issueDate):
			refusal = cannotIssue(codeIssueDateNotAllowed, fmt.Sprintf(
				"This document may be issued today with: %s (§ 5-1-3).", dateList(allowed)))
			dates := make([]openapi_types.Date, 0, len(allowed))
			for _, d := range allowed {
				dates = append(dates, wireDate(d))
			}
			refusal.AllowedIssueDates = &dates
		}
		if refusal != nil {
			return errRefused
		}
		var plan issuePlan
		switch locked.Kind {
		case kindInvoice:
			plan, refusal, err = invoiceIssueChecks(ctx, txq, profile, settings, issueDate, lines)
		default:
			return fmt.Errorf("invoices: document %d is a %s, which this module cannot issue", locked.ID, locked.Kind)
		}
		if err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		exchangeRate, err := ratFromNumeric(locked.ExchangeRate)
		if err != nil {
			return err
		}
		taxed := make([]taxedLine, 0, len(plan.lines))
		for _, l := range plan.lines {
			taxed = append(taxed, l.taxed)
		}
		rows, totals, ambiguous := summarize(taxed, exchangeRate)
		if ambiguous {
			refusal = cannotIssue(codeVatCodesAmbiguous,
				"Two lines share a VAT category and rate but carry different SAF-T codes, so one VAT summary row could not name its code.")
			return errRefused
		}

		// 6. The writes: the lines' snapshots, the summaries, and last the
		// row itself — the trigger refuses line writes under an issued one.
		for _, l := range plan.lines {
			rate, err := numericFromRat(l.taxed.rate, 2)
			if err != nil {
				return err
			}
			if err := txq.SnapshotLine(ctx, store.SnapshotLineParams{
				ID: l.id, VatRatePercent: rate, VatCategory: &l.taxed.category, SafTCode: &l.taxed.safT,
				ExemptionReason: l.taxed.reason,
			}); err != nil {
				return fmt.Errorf("invoices: snapshot line %d: %w", l.id, err)
			}
		}
		for _, r := range rows {
			p := store.InsertVatSummaryParams{InvoiceID: locked.ID, VatCategory: r.category, SafTCode: r.safT, ExemptionReason: r.reason}
			for _, c := range []struct {
				dst *pgtype.Numeric
				v   *big.Rat
			}{{&p.RatePercent, r.rate}, {&p.TaxableAmount, r.taxable}, {&p.VatAmount, r.vat}, {&p.VatAmountNok, r.vatNOK}} {
				if *c.dst, err = numericFromRat(c.v, 2); err != nil {
					return err
				}
			}
			if err := txq.InsertVatSummary(ctx, p); err != nil {
				return fmt.Errorf("invoices: write document %d's VAT: %w", locked.ID, err)
			}
		}
		params := store.IssueDocumentParams{}
		if plan.buyer != nil {
			params = *plan.buyer
		}
		params.ID, params.Number, params.IssueDate = locked.ID, &number, pgDate(issueDate)
		if locked.Kind == kindInvoice && locked.PaymentTermsDays != nil {
			params.DueDate = pgDate(issueDate.AddDate(0, 0, int(*locked.PaymentTermsDays)))
		}
		sellerSnapshot(&params, settings)
		if params.NetTotal, params.VatTotal, params.GrossTotal, params.VatTotalNok, err = numerics(totals); err != nil {
			return err
		}
		params.Now, params.IssuedByUserID = s.deps.Clock(), ptr(callerID(ctx))
		if plan.buyer == nil {
			// A credit note keeps the buyer snapshot its draft copied.
			keepBuyer(&params, locked)
		}
		issued, err = txq.IssueDocument(ctx, params)
		if err != nil {
			return fmt.Errorf("invoices: issue document %d: %w", locked.ID, err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdIssue409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, issued, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdIssue200JSONResponse(resp), nil
}

// containsDate reports whether d is one of dates.
func containsDate(dates []time.Time, d time.Time) bool {
	for _, x := range dates {
		if x.Equal(d) {
			return true
		}
	}
	return false
}

// sellerSnapshot copies the settings row into the issue's seller snapshot
// (D4). An optional field that is not set is stored as NULL.
func sellerSnapshot(p *store.IssueDocumentParams, s store.InvoicesSetting) {
	optional := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	p.SellerLegalName, p.SellerOrganisationNumber = &s.LegalName, &s.OrganisationNumber
	p.SellerVatRegistered, p.SellerInForetaksregisteret = &s.VatRegistered, &s.InForetaksregisteret
	p.SellerAddressLine1, p.SellerAddressLine2 = &s.AddressLine1, optional(s.AddressLine2)
	p.SellerPostalCode, p.SellerCity, p.SellerCountry = &s.PostalCode, &s.City, &s.Country
	p.SellerBankAccount, p.SellerIban, p.SellerBic = &s.BankAccount, optional(s.Iban), optional(s.Bic)
	p.SellerEmail, p.SellerFooterText = optional(s.Email), optional(s.FooterText)
}

// keepBuyer carries a document's own buyer snapshot through the issue.
func keepBuyer(p *store.IssueDocumentParams, inv store.InvoicesInvoice) {
	p.BuyerCustomerNumber, p.BuyerType, p.BuyerName = inv.BuyerCustomerNumber, inv.BuyerType, inv.BuyerName
	p.BuyerOrganisationNumber, p.BuyerForeignID = inv.BuyerOrganisationNumber, inv.BuyerForeignID
	p.BuyerAddressLine1, p.BuyerAddressLine2 = inv.BuyerAddressLine1, inv.BuyerAddressLine2
	p.BuyerPostalCode, p.BuyerCity, p.BuyerRegion, p.BuyerCountry = inv.BuyerPostalCode, inv.BuyerCity, inv.BuyerRegion, inv.BuyerCountry
	p.BuyerPeppolID, p.BuyerGln, p.BuyerLanguage = inv.BuyerPeppolID, inv.BuyerGln, inv.BuyerLanguage
}
```

- [ ] **Step 4: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bun run gen:client
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 ./internal/invoices/ ./internal/openapi/
mise exec -- golangci-lint run ./internal/invoices/...
# The race detector, locally, on four CPUs (docs: memory "Local vs CI gotchas"):
printf '#!/bin/sh\nexec mise exec zig@0.15 -- zig cc "$@"\n' > /tmp/claude-1000/zigcc.sh && chmod +x /tmp/claude-1000/zigcc.sh
CC=/tmp/claude-1000/zigcc.sh CGO_ENABLED=1 taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/invoices/
cd ../..
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task4.txt <<'MSG'
feat(invoices): issue a draft into the next number, and prove it immutable in SQL

The issue (invoices foundation design D6): the draft, the object store and
the billing profile read first; then one transaction locks the document,
shares the settings row, allocates the number from the counter row and only
then checks every rule, so a refusal gives the number back. The date is
today or, while the calendar day is at most 15, the last day of the previous
month when the delivery ended by then, and never before the latest issued
document. The lines' VAT snapshots and the summaries are written before the
row, which the triggers require (D9); the tests race issues, a settings
write and a merge, inject a failure after the allocation, and bypass the
handlers to prove an issued document cannot change.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/export_test.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/issue.go' 'apps/server/internal/invoices/issue_test.go' 'apps/server/internal/invoices/queries/counters.sql' 'apps/server/internal/invoices/queries/invoices.sql' 'apps/server/internal/invoices/queries/lines.sql' 'apps/server/internal/invoices/queries/settings.sql' 'apps/server/internal/invoices/store/counters.sql.go' 'apps/server/internal/invoices/store/invoices.sql.go' 'apps/server/internal/invoices/store/lines.sql.go' 'apps/server/internal/invoices/store/settings.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git commit -F /tmp/claude-1000/msg-invoices-task4.txt -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/export_test.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/issue.go' 'apps/server/internal/invoices/issue_test.go' 'apps/server/internal/invoices/queries/counters.sql' 'apps/server/internal/invoices/queries/invoices.sql' 'apps/server/internal/invoices/queries/lines.sql' 'apps/server/internal/invoices/queries/settings.sql' 'apps/server/internal/invoices/store/counters.sql.go' 'apps/server/internal/invoices/store/invoices.sql.go' 'apps/server/internal/invoices/store/lines.sql.go' 'apps/server/internal/invoices/store/settings.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 5: The PDF: rendered from the snapshot, stored once, downloaded as stored, and a watermarked preview (D7)

maroto v2 renders the model `buildPDFModel` builds from an issued document's own rows and snapshots; Noto Sans Regular and Bold are vendored and embedded. `storeOnce` renders, hashes, keys the object by the hash, puts it unless it exists and records it on the row once; the issue calls it after the commit and answers `pdfStored: false` when it fails. The download streams the stored object, verified against its hash, and never renders again once a hash is set; the preview renders a draft on demand with the watermark and stores nothing.

**Files:**
- Create: `apps/server/internal/invoices/fonts/LICENSE`, `apps/server/internal/invoices/fonts/NotoSans-Bold.ttf`, `apps/server/internal/invoices/fonts/NotoSans-Regular.ttf`, `apps/server/internal/invoices/pdf.go`, `apps/server/internal/invoices/pdf_internal_test.go`, `apps/server/internal/invoices/pdfstore.go`, `apps/server/internal/invoices/pdfstore_test.go`
- Modify: `apps/server/go.mod`, `apps/server/internal/invoices/contractscalls.go`, `apps/server/internal/invoices/harness_test.go`, `apps/server/internal/invoices/issue.go`, `apps/server/internal/invoices/queries/invoices.sql`, `openapi/invoices.yaml`
- Generated (commit them; never edit by hand): `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/go.sum`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/invoices.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/expenses/attachments.go:508-613` (the Visit wrapper and the download), `internal/storage/storage.go`, `docs/storage.md`, `$(go env GOMODCACHE)/github.com/phpdave11/gofpdf@v1.4.3/fpdf.go:3795-3840` (`SetDefaultCatalogSort`, `SetDefaultModificationDate`), `$(go env GOMODCACHE)/github.com/johnfercher/maroto/v2@v2.4.2/pkg/{config/builder.go,fontrepository/fontrepository.go}`

**Interfaces:**
- Produces wire: `GET /invoices/{id}/pdf` (200 `application/pdf` with `Content-Disposition: attachment; filename="faktura-<n>.pdf"` / `kreditnota-<n>.pdf` / `invoice-<n>.pdf` / `credit-note-<n>.pdf`; 409 `invoice_draft`; 500; 503 `storage_unavailable`), `GET /invoices/{id}/preview.pdf` (200 `inline; filename="utkast-<id>.pdf"`; 409 `invoice_issued`).
- Produces SQL: `SetDocumentPDF` (`WHERE pdf_sha256 IS NULL`, `:execrows`).
- Produces Go: `pdfDocument`, `pdfModel`, `buildPDFModel`, `renderPDF`, `pdfDocumentOf`, `renderIssued`, `storeOnce`, `storeAfterIssue`, `fileName`, `renderPreview`; `objectPut`, `objectGet`, `objectExists`.

- [ ] **Step 1: The dependency and the font**

maroto v2.4.2 (MIT) is the latest release on the Go proxy (`https://proxy.golang.org/github.com/johnfercher/maroto/v2/@latest` answers `v2.4.2`, 2026-09-10); it renders through `github.com/phpdave11/gofpdf v1.4.3`, whose two process-global defaults the renderer sets. The fonts are vendored so the build needs no network. **Run**, from the repository root:

```bash
cd apps/server && mise exec -- go get github.com/johnfercher/maroto/v2@v2.4.2 github.com/phpdave11/gofpdf@v1.4.3 && cd ../..
mkdir -p apps/server/internal/invoices/fonts
curl -fsSL -o apps/server/internal/invoices/fonts/NotoSans-Regular.ttf https://raw.githubusercontent.com/notofonts/notofonts.github.io/28b15b4b43b7bed62b5cf6e6b0b5ff5846270535/fonts/NotoSans/hinted/ttf/NotoSans-Regular.ttf
curl -fsSL -o apps/server/internal/invoices/fonts/NotoSans-Bold.ttf https://raw.githubusercontent.com/notofonts/notofonts.github.io/28b15b4b43b7bed62b5cf6e6b0b5ff5846270535/fonts/NotoSans/hinted/ttf/NotoSans-Bold.ttf
curl -fsSL -o apps/server/internal/invoices/fonts/LICENSE https://raw.githubusercontent.com/notofonts/latin-greek-cyrillic/4bc63d7ebca1faed49c6c685f380ba0abc2c1941/OFL.txt
cd apps/server/internal/invoices/fonts && sha256sum -c - <<'SUMS' && cd ../../../../..
478c558ea716033cd60c03438f628dfa75694dcf6b5f6d505a2f05fd2b4f3823  NotoSans-Regular.ttf
1df075a380fc7cb898acf64c1f7b3b4dd780de3caa860178bf929de35817a913  NotoSans-Bold.ttf
cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a  LICENSE
SUMS
```
`LICENSE` is the SIL Open Font License 1.1 text ("Copyright 2022 The Noto Project Authors"). `go mod tidy` runs in Step 4, once the code imports the packages; it moves both to direct requires and adds their indirect dependencies (`pdfcpu`, `go-tree`, `golang.org/x/image`, …) to `go.mod` and `go.sum`.

- [ ] **Step 2: The tests: the model, reproducibility, store-once and every failure mode**

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
	f.onProfile = fn
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Delete,
// because the module must never call one (D7).
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deletes int
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)
```

**with**:

```go
	f.onProfile = fn
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Put
// and every Delete — the module must never call one (D7) — and fails a Put or
// a Get on demand.
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    int
	deletes int
	putErr  error
	getErr  error
}

// failPuts makes every Put fail with err, nil to stop.
func (s *fakeObjectStore) failPuts(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putErr = err
}

// failGets makes every Get fail with err, nil to stop.
func (s *fakeObjectStore) failGets(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getErr = err
}

// replace swaps an object's bytes behind the module's back.
func (s *fakeObjectStore) replace(key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = body
}

// lose removes an object behind the module's back.
func (s *fakeObjectStore) lose(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
}

// stored is every key in the store and the number of Puts and Deletes made.
func (s *fakeObjectStore) stored() (keys []string, puts, deletes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.objects {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys, s.puts, s.deletes
}

// object is one object's bytes.
func (s *fakeObjectStore) object(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[key]
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = body
	return nil
}
```

**with**:

```go
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.puts++
	s.objects[key] = body
	return nil
}
```

**Replace** in `apps/server/internal/invoices/harness_test.go`:

```go
func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
```

**with**:

```go
func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	body, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
```

**Create** `apps/server/internal/invoices/pdf_internal_test.go`:

```go
package invoices

import (
	"bytes"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"
)

// This file tests the PDF through its model: no PDF text extractor in this
// module's dependencies reads an embedded-subset font back into words, so what
// a document says is asserted on the words the renderer lays out
// (buildPDFModel), and the bytes only for being reproducible.

func rat(s string) *big.Rat { return mustRat(s) }

// anInvoice is an issued invoice from a VAT-registered AS in Foretaksregisteret
// with a 25 % line and a zero-rated one, to a Norwegian business.
func anInvoice() pdfDocument {
	number, terms := int64(1000), int32(14)
	issued := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	due := issued.AddDate(0, 0, 14)
	delivered := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	zeroReason := "Fritatt for merverdiavgift"
	return pdfDocument{
		kind: kindInvoice, language: "nb", number: &number, issueDate: issued, dueDate: &due, paymentTermsDays: &terms,
		deliveryDate: &delivered, yourReference: "PO-77", ourReference: "Ola Nordmann",
		seller: pdfParty{name: "Kraft-Verket AS", line1: "Storgata 1", postalCode: "0155", city: "Oslo", country: "NO",
			organisationNumber: "974760673", email: "faktura@kraft-verket.no", vatRegistered: true, foretaksregisteret: true},
		buyer: pdfParty{name: "Acme Norge AS", line1: "Kundeveien 2", postalCode: "0150", city: "Oslo", country: "NO",
			organisationNumber: "923609016"},
		bankAccount: "86011117947", iban: "NO9386011117947", bic: "DNBANOKKXXX",
		lines: []pdfLine{
			{description: "Konsulenttime", unit: "timer", quantity: rat("10"), unitPrice: rat("1200"), discount: rat("0"), rate: rat("25"), net: rat("12000")},
			{description: "Eksport", unit: "stk", quantity: rat("1.5"), unitPrice: rat("33.3333"), discount: rat("10"), rate: rat("0"), net: rat("45")},
		},
		summaries: []vatSummary{
			{category: "S", rate: rat("25"), safT: "3", taxable: rat("12000"), vat: rat("3000")},
			{category: "Z", rate: rat("0"), safT: "5", reason: &zeroReason, taxable: rat("45"), vat: rat("0")},
		},
		totals:  documentTotals{net: rat("12045"), vat: rat("3000"), gross: rat("15045")},
		note:    "Takk for handelen.",
		footer:  "Kraft-Verket AS · Storgata 1 · 0155 Oslo",
		created: time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC),
	}
}

func TestPDFModel_AnInvoice(t *testing.T) {
	t.Parallel()
	m := buildPDFModel(anInvoice())

	if m.title != "Faktura" || m.watermark != "" {
		t.Errorf("title %q, watermark %q", m.title, m.watermark)
	}
	if want := []string{"Kraft-Verket AS", "Storgata 1", "0155 Oslo", "Org.nr. 974 760 673 MVA", "Foretaksregisteret", "faktura@kraft-verket.no"}; !slices.Equal(m.seller, want) {
		t.Errorf("seller = %q, want %q", m.seller, want)
	}
	if want := []string{"Acme Norge AS", "Kundeveien 2", "0150 Oslo", "Org.nr. 923 609 016"}; !slices.Equal(m.buyer, want) {
		t.Errorf("buyer = %q, want %q", m.buyer, want)
	}
	if want := [][2]string{
		{"Nummer", "1000"}, {"Fakturadato", "12.09.2026"}, {"Leveringsdato", "10.09.2026"},
		{"Forfallsdato", "26.09.2026"}, {"Betalingsbetingelser", "14 dager"}, {"Deres ref.", "PO-77"}, {"Vår ref.", "Ola Nordmann"},
	}; !slices.Equal(m.meta, want) {
		t.Errorf("meta = %q, want %q", m.meta, want)
	}
	if want := []string{"Eksport", "1,5", "stk", "33,3333", "10", "0", "45,00"}; !slices.Equal(m.lines[1], want) {
		t.Errorf("line 2 = %q, want %q", m.lines[1], want)
	}
	if want := []string{"Konsulenttime", "10", "timer", "1 200,00", "0", "25", "12 000,00"}; !slices.Equal(m.lines[0], want) {
		t.Errorf("line 1 = %q, want %q", m.lines[0], want)
	}
	if want := [][]string{{"S 25 %", "12 000,00", "3 000,00"}, {"Z 0 %", "45,00", "0,00"}}; !slices.Equal(m.vatRows[0], want[0]) || !slices.Equal(m.vatRows[1], want[1]) {
		t.Errorf("VAT rows = %q, want a row per rate, the 0 %% one included", m.vatRows)
	}
	if !slices.Equal(m.reasons, []string{"Fritatt for merverdiavgift"}) {
		t.Errorf("reasons = %q, want the Z row's", m.reasons)
	}
	if m.totals[2] != [2]string{"Å betale", "15 045,00"} {
		t.Errorf("gross = %q, want Å betale 15 045,00", m.totals[2])
	}
	if want := [][2]string{{"Kontonummer", "86011117947"}, {"IBAN", "NO9386011117947"}, {"BIC", "DNBANOKKXXX"}, {"Forfallsdato", "26.09.2026"}}; !slices.Equal(m.payment, want) ||
		m.paymentNote != "Vennligst oppgi fakturanummer ved betaling" {
		t.Errorf("payment = %q %q", m.payment, m.paymentNote)
	}
	if m.note != "Takk for handelen." || m.footer == "" {
		t.Errorf("note %q footer %q", m.note, m.footer)
	}
}

// "MVA" and "Foretaksregisteret" follow the flags (§ 5-1-2); a foreign buyer
// is named by its VAT/Reg. no.; the reverse-charge text is the regulation's
// Norwegian on an English document; a place of delivery prints only when it
// is not the buyer's address.
func TestPDFModel_TheFlagsAndTheLanguage(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.seller.vatRegistered, d.seller.foretaksregisteret = false, false
	if m := buildPDFModel(d); slices.Contains(m.seller, "Foretaksregisteret") || !slices.Contains(m.seller, "Org.nr. 974 760 673") {
		t.Errorf("an unregistered seller = %q, want the number without MVA and no Foretaksregisteret", m.seller)
	}

	en := anInvoice()
	en.language = "en"
	en.buyer = pdfParty{name: "Svenska Aktiebolaget AB", line1: "Storgatan 1", postalCode: "111 22", city: "Stockholm", country: "SE", foreignID: "SE556677889901"}
	reason := "Omvendt avgiftsplikt – Merverdiavgift ikke beregnet"
	en.summaries = append(en.summaries, vatSummary{category: "AE", rate: rat("0"), safT: "51", reason: &reason, taxable: rat("100"), vat: rat("0")})
	en.deliveryPlace = &pdfParty{line1: "Byggeplassen", city: "Bergen", country: "NO"}
	m := buildPDFModel(en)
	if m.title != "Invoice" || !slices.Contains(m.buyer, "VAT/Reg. no. SE556677889901") || !slices.Contains(m.buyer, "SE") {
		t.Errorf("an English invoice to Sweden = %q %q", m.title, m.buyer)
	}
	if !slices.Contains(m.reasons, reverseChargeText) {
		t.Errorf("reasons = %q, want the Norwegian reverse-charge text", m.reasons)
	}
	if !slices.Contains(m.meta, [2]string{"Place of delivery", "Byggeplassen, Bergen"}) {
		t.Errorf("meta = %q, want the place of delivery", m.meta)
	}
	if m.lines[0][3] != "1,200.00" || m.meta[1] != [2]string{"Invoice date", "2026-09-12"} {
		t.Errorf("English numbers and dates = %q %q", m.lines[0][3], m.meta[1])
	}
	same := anInvoice()
	same.deliveryPlace = &pdfParty{line1: "kundeveien 2", postalCode: "0150", city: "OSLO", country: "NO"}
	for _, kv := range buildPDFModel(same).meta {
		if kv[0] == "Leveringssted" {
			t.Error("the buyer's own address printed as the place of delivery")
		}
	}
}

// A credit note names its original and has no due date or payment block; a
// preview carries the watermark and no number.
func TestPDFModel_ACreditNoteAndAPreview(t *testing.T) {
	t.Parallel()
	c := anInvoice()
	c.kind = kindCreditNote
	c.credits = &struct {
		number    int64
		issueDate time.Time
	}{999, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	m := buildPDFModel(c)
	if m.title != "Kreditnota" || m.creditsLine != "Kreditnota til faktura 999 av 01.09.2026" {
		t.Errorf("credit note = %q, %q", m.title, m.creditsLine)
	}
	if len(m.payment) != 0 || m.paymentNote != "" || m.totals[2][0] != "Sum" {
		t.Errorf("a credit note's payment = %q %q, gross %q; want none and Sum", m.payment, m.paymentNote, m.totals[2])
	}
	for _, kv := range m.meta {
		if kv[0] == "Forfallsdato" || kv[0] == "Betalingsbetingelser" {
			t.Errorf("a credit note prints %q", kv[0])
		}
	}

	p := anInvoice()
	p.preview, p.number = true, nil
	pm := buildPDFModel(p)
	if pm.watermark != "UTKAST — ikke et salgsdokument" || pm.meta[0][0] == "Nummer" {
		t.Errorf("preview = watermark %q, first meta %q", pm.watermark, pm.meta[0])
	}
}

// The same document renders to the same bytes, in-process, twice: catalog
// sorting and the fixed modification date are set (D7). The bytes may change
// with an upgrade of maroto, gofpdf or the font; stored PDFs never do.
func TestRenderPDF_IsReproducible(t *testing.T) {
	t.Parallel()
	first, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // a second later, so a wall-clock date would differ
	second, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of one document differ")
	}
	if !bytes.HasPrefix(first, []byte("%PDF-")) {
		t.Errorf("not a PDF: %q", first[:8])
	}
	for _, want := range []string{"/CreationDate (D:20260912103000", "/ModDate (D:20260101000000"} {
		if !strings.Contains(string(first), want) {
			t.Errorf("the PDF has no %s", want)
		}
	}
}
```

**Create** `apps/server/internal/invoices/pdfstore_test.go`:

```go
package invoices_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func pdfPath(id int64) string     { return fmt.Sprintf("%s/%d/pdf", invoicesPath, id) }
func previewPath(id int64) string { return fmt.Sprintf("%s/%d/preview.pdf", invoicesPath, id) }

func download(t *testing.T, h *harness, id int64) *modtest.Response {
	t.Helper()
	return h.SignIn(t, "invoices:access").Do(http.MethodGet, pdfPath(id), nil)
}

// issuedAcme issues one invoice to Acme and answers it.
func issuedAcme(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	return issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25))).ID)
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The issue stores the PDF once, under the scope-relative key the bytes
// name; the download streams exactly those bytes, as an attachment named in
// the document's language; the module never deletes (D7).
func TestPDF_IssueStoresItOnceAndTheDownloadStreamsIt(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issuedAcme(t, h)
	if inv.PdfStored == nil || !*inv.PdfStored {
		t.Fatalf("pdfStored = %v, want true", inv.PdfStored)
	}
	keys, puts, _ := h.objects.stored()
	if len(keys) != 1 || puts != 1 || !regexp.MustCompile(fmt.Sprintf(`^documents/%d/1-[0-9a-f]{64}\.pdf$`, inv.ID)).MatchString(keys[0]) {
		t.Fatalf("stored = %v after %d puts, want one scope-relative documents/<id>/<number>-<sha256>.pdf", keys, puts)
	}
	column := modtest.One[string](t, h.Harness, `SELECT pdf_object_key || ' ' || pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	if !strings.HasPrefix(column, keys[0]+" ") || !strings.HasSuffix(keys[0], "-"+strings.Fields(column)[1]+".pdf") {
		t.Errorf("row = %q, want the key and the hash it names", column)
	}

	res := download(t, h, inv.ID)
	if res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" ||
		res.Header("Content-Disposition") != `attachment; filename="faktura-1.pdf"` {
		t.Fatalf("download = %d %q %q", res.Status, res.Header("Content-Type"), res.Header("Content-Disposition"))
	}
	if sha(res.Body) != strings.Fields(column)[1] || string(res.Body) != string(h.objects.object(keys[0])) {
		t.Error("the download is not the stored object")
	}
	if _, puts, _ := h.objects.stored(); puts != 1 {
		t.Errorf("puts after the download = %d, want no second store", puts)
	}

	english := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 500, vat25))).ID)
	if res := download(t, h, english.ID); res.Header("Content-Disposition") != `attachment; filename="invoice-2.pdf"` {
		t.Errorf("an English document = %q, want invoice-2.pdf", res.Header("Content-Disposition"))
	}
	if _, _, deletes := h.objects.stored(); deletes != 0 {
		t.Errorf("deletes = %d, want none ever", deletes)
	}
}

// A Put failing after the commit does not fail the issue: it answers
// pdfStored false, and the next download stores it once (D6, D7).
func TestPDF_APutFailingAfterTheCommitIsStoredByTheNextDownload(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.objects.failPuts(errors.New("disk full"))
	inv := issuedAcme(t, h)
	if inv.Status != "issued" || inv.PdfStored == nil || *inv.PdfStored {
		t.Fatalf("issue with a failing store = %s, pdfStored %v; want issued and false", inv.Status, inv.PdfStored)
	}
	if !strings.Contains(h.Logs(), "could not be stored") {
		t.Error("the failed store was not logged")
	}
	if res := download(t, h, inv.ID); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("a download while the store still fails = %d %s, want 503", res.Status, res.Body)
	}

	h.objects.failPuts(nil)
	res := download(t, h, inv.ID)
	if res.Status != http.StatusOK {
		t.Fatalf("the next download = %d %s", res.Status, res.Body)
	}
	keys, puts, _ := h.objects.stored()
	if len(keys) != 1 || puts != 1 || sha(res.Body) != sha(h.objects.object(keys[0])) {
		t.Errorf("stored = %v after %d puts, want the one object the download streamed", keys, puts)
	}
	if again := getInvoice(t, h, inv.ID); again.PdfStored == nil || !*again.PdfStored {
		t.Error("the document still says its PDF is not stored")
	}
}

// Two downloads racing for an unstored PDF store one hash and both stream the
// same bytes.
func TestPDF_RacingDownloadsStoreOneHash(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.objects.failPuts(errors.New("offline"))
	inv := issuedAcme(t, h)
	h.objects.failPuts(nil)

	bodies := make([][]byte, 4)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := download(t, h, inv.ID)
			if res.Status != http.StatusOK {
				t.Errorf("racing download = %d %s", res.Status, res.Body)
				return
			}
			bodies[i] = res.Body
		}()
	}
	wg.Wait()
	stored := modtest.One[string](t, h.Harness, `SELECT pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	for i, b := range bodies {
		if sha(b) != stored {
			t.Errorf("download %d streamed %s, want the stored %s", i, sha(b), stored)
		}
	}
}

// A stored object whose bytes no longer match is a 500 and is never rendered
// again; a missing one is a 500; a Get that fails is a 503; a draft has no
// download (D7).
func TestPDF_TheStoredObjectsFailureModes(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issuedAcme(t, h)
	keys, _, _ := h.objects.stored()

	h.objects.failGets(errors.New("timeout"))
	if res := download(t, h, inv.ID); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("a failing Get = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	h.objects.failGets(nil)

	h.objects.replace(keys[0], []byte("%PDF-1.3 something else"))
	if res := download(t, h, inv.ID); res.Status != http.StatusInternalServerError {
		t.Errorf("tampered bytes = %d, want 500", res.Status)
	}
	if _, puts, _ := h.objects.stored(); puts != 1 {
		t.Errorf("puts = %d, want the tampered document never rendered again", puts)
	}
	if !strings.Contains(h.Logs(), "does not match its hash") {
		t.Error("the mismatch was not logged")
	}

	h.objects.lose(keys[0])
	if res := download(t, h, inv.ID); res.Status != http.StatusInternalServerError {
		t.Errorf("a lost object = %d, want 500", res.Status)
	}

	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	if res := download(t, h, draft.ID); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_draft" {
		t.Errorf("a draft's download = %d %s, want 409 invoice_draft", res.Status, res.Body)
	}
	if res := download(t, h, 424242); res.Status != http.StatusNotFound {
		t.Errorf("an unknown document = %d, want 404", res.Status)
	}
}

// Without a store the download is a 503.
func TestPDF_WithoutAStore(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	id := plantIssuedDocument(t, h, 1, "2026-09-01")
	if res := download(t, h, id); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("a download without a store = %d %s, want 503", res.Status, res.Body)
	}
}

// The preview renders a draft on demand and stores nothing; an issued
// document is refused; it needs invoices:create, the download only access.
func TestPDF_ThePreview(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // an incomplete seller previews too
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))

	res := creator(t, h).Do(http.MethodGet, previewPath(draft.ID), nil)
	if res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" || !strings.HasPrefix(string(res.Body), "%PDF-") ||
		res.Header("Content-Disposition") != fmt.Sprintf(`inline; filename="utkast-%d.pdf"`, draft.ID) {
		t.Fatalf("preview = %d %q %q", res.Status, res.Header("Content-Type"), res.Header("Content-Disposition"))
	}
	if keys, puts, _ := h.objects.stored(); len(keys) != 0 || puts != 0 {
		t.Errorf("the preview stored %v", keys)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, previewPath(draft.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("a preview without invoices:create = %d, want 403", res.Status)
	}
	if res := creator(t, h).Do(http.MethodGet, previewPath(424242), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown preview = %d, want 404", res.Status)
	}

	saveSeller(t, h, completeSeller(1))
	inv := issued(t, h, draft.ID)
	if res := creator(t, h).Do(http.MethodGet, previewPath(inv.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
		t.Errorf("previewing an issued document = %d %s, want 409 invoice_issued", res.Status, res.Body)
	}
}
```

- [ ] **Step 3: The contract and the query**

**Insert** into `openapi/invoices.yaml`, immediately before the line `servers:`:

```yaml
    /api/v1/invoices/{id}/pdf:
        get:
            description: 'An issued document''s PDF (D7), always the stored object: rendered and stored once when it has no hash yet, then streamed; otherwise read whole and verified against its SHA-256. It is never re-rendered once stored. Named faktura-<number>.pdf or kreditnota-<number>.pdf, invoice-<number>.pdf or credit-note-<number>.pdf in English.'
            operationId: getInvoicesByIdPdf
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            responses:
                "200":
                    content:
                        application/pdf:
                            schema:
                                format: binary
                                type: string
                    description: OK — the PDF, with a Content-Disposition naming it.
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
                    description: Not Found — no document has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — invoice_draft; a draft is previewed, not downloaded.
                "500":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Internal Server Error — the stored object is gone, or its bytes no longer match the hash. It is never papered over by rendering again.
                "503":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Service Unavailable — storage_unavailable, the object store is not configured or could not be read.
            summary: Download an issued document's PDF
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
    /api/v1/invoices/{id}/preview.pdf:
        get:
            description: 'A draft rendered on demand (D4), never stored: the watermark "UTKAST — ikke et salgsdokument", no number, today as the would-be issue date, the current settings, the customer''s current billing profile for an invoice draft and the copied snapshot for a credit-note draft.'
            operationId: getInvoicesByIdPreviewPdf
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            responses:
                "200":
                    content:
                        application/pdf:
                            schema:
                                format: binary
                                type: string
                    description: OK — the PDF, with a Content-Disposition naming it.
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
                    description: Not Found — no document has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — invoice_issued; an issued document is downloaded, not previewed.
            summary: Preview a draft as PDF
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:create
```

**Append** to the end of `apps/server/internal/invoices/queries/invoices.sql`:

```sql

-- name: SetDocumentPDF :execrows
-- SetDocumentPDF records where an issued document's PDF is stored and the hash
-- of its bytes, once (D7): the first writer wins, and a loser of a race sees
-- no row and streams the winner's object instead. The trigger allows exactly
-- this change, from NULL, and never another (D9).
UPDATE invoices.invoices SET pdf_object_key = @pdf_object_key, pdf_sha256 = @pdf_sha256
WHERE id = @id AND status = 'issued' AND pdf_sha256 IS NULL;
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 4: The renderer, store-once, the download and the preview**

**Replace** in `apps/server/internal/invoices/contractscalls.go`:

```go

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)
```

**with**:

```go

import (
	"context"
	"io"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)
```

**Replace** in `apps/server/internal/invoices/contractscalls.go`:

```go
	noteContractCall(ctx, "Directory.Customers")
	return s.deps.Directory.Customers(ctx, ids)
}
```

**with**:

```go
	noteContractCall(ctx, "Directory.Customers")
	return s.deps.Directory.Customers(ctx, ids)
}

// objectPut, objectGet and objectExists are the object store.
func (s *server) objectPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	noteContractCall(ctx, "ObjectStore.Put")
	return s.objects.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	noteContractCall(ctx, "ObjectStore.Get")
	return s.objects.Get(ctx, key)
}

func (s *server) objectExists(ctx context.Context, key string) (bool, error) {
	noteContractCall(ctx, "ObjectStore.Exists")
	return s.objects.Exists(ctx, key)
}
```

**Create** `apps/server/internal/invoices/pdf.go`:

```go
package invoices

import (
	_ "embed"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"
)

// This file is the PDF (D7): a document's model — every word it prints, in
// the order it prints them — and the renderer that lays the model out with
// maroto v2 (pure Go; the runtime image has no fonts, so the font is embedded).
//
// What makes a stored PDF lawful is store-once (pdfstore.go), not determinism.
// Reproducible bytes are a nice-to-have, and the renderer gets them where the
// libraries let it: gofpdf would write /ModDate as time.Now() and font objects
// in Go map order, and maroto exposes neither, so init sets gofpdf's two
// process-global defaults — catalog sorting on, a fixed modification date —
// as constants, never per-document values. The creation date is the
// document's own issued_at. Rendering is sequential, never concurrent.
//
// There is no PDF text extractor in this module's dependencies that reads an
// embedded-subset font back into words, so the tests assert on the model
// (pdfModel) for what a document says, and on the bytes only for
// reproducibility.

// The font: Noto Sans Regular and Bold (SIL Open Font License 1.1, whose
// text is fonts/LICENSE), for æøå and the en dash in the reverse-charge text.
var (
	//go:embed fonts/NotoSans-Regular.ttf
	notoSansRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	notoSansBold []byte
)

const fontFamily = "noto-sans"

// pdfModificationDate is the fixed /ModDate of every PDF (see above).
var pdfModificationDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func init() {
	gofpdf.SetDefaultCatalogSort(true)
	gofpdf.SetDefaultModificationDate(pdfModificationDate)
}

// reverseChargeText is the regulation's own wording (§ 5-1-1), printed in
// Norwegian whatever the document's language.
const reverseChargeText = "Omvendt avgiftsplikt – Merverdiavgift ikke beregnet"

// pdfLabels are a document's fixed words in one language (§ 5-1-1a allows
// English).
type pdfLabels struct {
	invoice, creditNote, number, issueDate, deliveryDate, deliveryPeriod, deliveryPlace string
	dueDate, terms, termsDays, yourRef, ourRef, orderRef, creditsFor                    string
	description, quantity, unit, unitPrice, discount, vat, amount                       string
	vatBasis, vatAmount, vatCategory, net, vatTotal, gross, toPay                       string
	payment, account, iban, bic, payWithNumber, orgNumber, foreignID, watermark         string
}

var labels = map[string]pdfLabels{
	"nb": {
		invoice: "Faktura", creditNote: "Kreditnota", number: "Nummer", issueDate: "Fakturadato",
		deliveryDate: "Leveringsdato", deliveryPeriod: "Leveringsperiode", deliveryPlace: "Leveringssted",
		dueDate: "Forfallsdato", terms: "Betalingsbetingelser", termsDays: "%d dager",
		yourRef: "Deres ref.", ourRef: "Vår ref.", orderRef: "Ordrereferanse", creditsFor: "Kreditnota til faktura %d av %s",
		description: "Beskrivelse", quantity: "Antall", unit: "Enhet", unitPrice: "Enhetspris", discount: "Rabatt %",
		vat: "MVA %", amount: "Beløp", vatBasis: "Grunnlag", vatAmount: "MVA", vatCategory: "MVA-sats",
		net: "Sum eks. MVA", vatTotal: "MVA", gross: "Sum", toPay: "Å betale",
		payment: "Betaling", account: "Kontonummer", iban: "IBAN", bic: "BIC",
		payWithNumber: "Vennligst oppgi fakturanummer ved betaling", orgNumber: "Org.nr.", foreignID: "VAT/Reg. no.",
		watermark: "UTKAST — ikke et salgsdokument",
	},
	"en": {
		invoice: "Invoice", creditNote: "Credit note", number: "Number", issueDate: "Invoice date",
		deliveryDate: "Delivery date", deliveryPeriod: "Delivery period", deliveryPlace: "Place of delivery",
		dueDate: "Due date", terms: "Payment terms", termsDays: "%d days",
		yourRef: "Your ref.", ourRef: "Our ref.", orderRef: "Order reference", creditsFor: "Credit note for invoice %d of %s",
		description: "Description", quantity: "Quantity", unit: "Unit", unitPrice: "Unit price", discount: "Discount %",
		vat: "VAT %", amount: "Amount", vatBasis: "Basis", vatAmount: "VAT", vatCategory: "VAT rate",
		net: "Total excl. VAT", vatTotal: "VAT", gross: "Total", toPay: "Amount due",
		payment: "Payment", account: "Account number", iban: "IBAN", bic: "BIC",
		payWithNumber: "Please state the invoice number with your payment", orgNumber: "Org. no.", foreignID: "VAT/Reg. no.",
		watermark: "UTKAST — ikke et salgsdokument",
	},
}

// pdfParty is a seller or a buyer as a document prints it.
type pdfParty struct {
	name, line1, line2, postalCode, city, country string
	organisationNumber, foreignID, email          string
	vatRegistered, foretaksregisteret             bool
}

// pdfLine is one line as a document prints it.
type pdfLine struct {
	description, unit                   string
	quantity, unitPrice, discount, rate *big.Rat
	net                                 *big.Rat
}

// pdfDocument is everything a PDF is rendered from — for an issued document,
// only its own rows and snapshots (pdfDocumentOf); for a draft's preview, what
// the draft would be if issued today (preview in pdfstore.go).
type pdfDocument struct {
	kind, language                         string
	number                                 *int64
	issueDate                              time.Time
	dueDate                                *time.Time
	paymentTermsDays                       *int32
	deliveryDate, deliveryFrom, deliveryTo *time.Time
	deliveryPlace                          *pdfParty
	yourReference, ourReference, orderRef  string
	note, footer                           string
	seller, buyer                          pdfParty
	bankAccount, iban, bic                 string
	lines                                  []pdfLine
	summaries                              []vatSummary
	totals                                 documentTotals
	credits                                *struct {
		number    int64
		issueDate time.Time
	}
	created time.Time
	preview bool
}

// pdfModel is every word a document prints, in the order it prints them: what
// the tests read, and all the renderer lays out.
type pdfModel struct {
	watermark    string
	title        string
	seller       []string
	buyer        []string
	meta         [][2]string
	creditsLine  string
	lineHeader   []string
	lines        [][]string
	vatHeader    []string
	vatRows      [][]string
	reasons      []string
	totals       [][2]string
	payment      [][2]string
	paymentNote  string
	note, footer string
	created      time.Time
}

// groupDigits writes an integer part with sep between thousands.
func groupDigits(digits, sep string) string {
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatDecimal is v in the document's language — "1 234,50" in Norwegian,
// "1,234.50" in English — with at least minPlaces and at most maxPlaces
// decimals, trailing zeros beyond minPlaces dropped.
func formatDecimal(v *big.Rat, minPlaces, maxPlaces int, language string) string {
	text := v.FloatString(maxPlaces)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	whole, frac, _ := strings.Cut(text, ".")
	for len(frac) > minPlaces && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	thousands, point := " ", ","
	if language == "en" {
		thousands, point = ",", "."
	}
	out := groupDigits(whole, thousands)
	if frac != "" {
		out += point + frac
	}
	if negative {
		out = "-" + out
	}
	return out
}

func money(v *big.Rat, language string) string { return formatDecimal(v, 2, 2, language) }

// formatDate is a date in the document's language.
func formatDate(d time.Time, language string) string {
	if language == "en" {
		return d.Format(time.DateOnly)
	}
	return d.Format("02.01.2006")
}

// organisationNumber prints nine digits in groups of three.
func organisationNumber(n string) string {
	if len(n) != 9 {
		return n
	}
	return n[0:3] + " " + n[3:6] + " " + n[6:9]
}

// partyLines are a party's address lines.
func partyLines(p pdfParty) []string {
	out := []string{p.name}
	for _, l := range []string{p.line1, p.line2} {
		if l != "" {
			out = append(out, l)
		}
	}
	if place := strings.TrimSpace(p.postalCode + " " + p.city); place != "" {
		out = append(out, place)
	}
	if p.country != "" && p.country != "NO" {
		out = append(out, p.country)
	}
	return out
}

// sameAddress reports whether a place of delivery is the buyer's own address,
// which is then not printed (D4).
func sameAddress(a, b pdfParty) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return norm(a.line1) == norm(b.line1) && norm(a.line2) == norm(b.line2) &&
		norm(a.postalCode) == norm(b.postalCode) && norm(a.city) == norm(b.city) && norm(a.country) == norm(b.country)
}

// buildPDFModel is D7's layout as words.
func buildPDFModel(d pdfDocument) pdfModel {
	lang := d.language
	if _, ok := labels[lang]; !ok {
		lang = "nb"
	}
	l := labels[lang]
	m := pdfModel{title: l.invoice, created: d.created}
	if d.kind == kindCreditNote {
		m.title = l.creditNote
	}
	if d.preview {
		m.watermark = l.watermark
	}

	// The seller: name, address, the organisation number followed by MVA
	// when VAT-registered, Foretaksregisteret when registered there (§ 5-1-2).
	m.seller = partyLines(d.seller)
	orgLine := l.orgNumber + " " + organisationNumber(d.seller.organisationNumber)
	if d.seller.vatRegistered {
		orgLine += " MVA"
	}
	m.seller = append(m.seller, orgLine)
	if d.seller.foretaksregisteret {
		m.seller = append(m.seller, "Foretaksregisteret")
	}
	if d.seller.email != "" {
		m.seller = append(m.seller, d.seller.email)
	}

	// The buyer: name, address, and the organisation number or the foreign id.
	m.buyer = partyLines(d.buyer)
	switch {
	case d.buyer.organisationNumber != "":
		m.buyer = append(m.buyer, l.orgNumber+" "+organisationNumber(d.buyer.organisationNumber))
	case d.buyer.foreignID != "":
		m.buyer = append(m.buyer, l.foreignID+" "+d.buyer.foreignID)
	}

	// The meta block.
	if d.number != nil {
		m.meta = append(m.meta, [2]string{l.number, fmt.Sprint(*d.number)})
	}
	m.meta = append(m.meta, [2]string{l.issueDate, formatDate(d.issueDate, lang)})
	switch {
	case d.deliveryDate != nil:
		m.meta = append(m.meta, [2]string{l.deliveryDate, formatDate(*d.deliveryDate, lang)})
	case d.deliveryFrom != nil && d.deliveryTo != nil:
		m.meta = append(m.meta, [2]string{l.deliveryPeriod, formatDate(*d.deliveryFrom, lang) + " – " + formatDate(*d.deliveryTo, lang)})
	}
	if d.deliveryPlace != nil && !sameAddress(*d.deliveryPlace, d.buyer) {
		place := partyLines(*d.deliveryPlace)
		m.meta = append(m.meta, [2]string{l.deliveryPlace, strings.Join(place[1:], ", ")})
	}
	if d.kind == kindInvoice {
		if d.dueDate != nil {
			m.meta = append(m.meta, [2]string{l.dueDate, formatDate(*d.dueDate, lang)})
		}
		if d.paymentTermsDays != nil {
			m.meta = append(m.meta, [2]string{l.terms, fmt.Sprintf(l.termsDays, *d.paymentTermsDays)})
		}
	}
	for _, ref := range [][2]string{{l.yourRef, d.yourReference}, {l.ourRef, d.ourReference}, {l.orderRef, d.orderRef}} {
		if ref[1] != "" {
			m.meta = append(m.meta, ref)
		}
	}
	if d.credits != nil {
		m.creditsLine = fmt.Sprintf(l.creditsFor, d.credits.number, formatDate(d.credits.issueDate, lang))
	}

	// The lines.
	m.lineHeader = []string{l.description, l.quantity, l.unit, l.unitPrice, l.discount, l.vat, l.amount}
	for _, line := range d.lines {
		m.lines = append(m.lines, []string{
			line.description, formatDecimal(line.quantity, 0, 3, lang), line.unit,
			formatDecimal(line.unitPrice, 2, 4, lang), formatDecimal(line.discount, 0, 2, lang),
			formatDecimal(line.rate, 0, 2, lang), money(line.net, lang),
		})
	}

	// VAT per (category, rate), 0 % categories included, and under it each
	// non-S category's reason — the reverse-charge text in Norwegian always.
	m.vatHeader = []string{l.vatCategory, l.vatBasis, l.vatAmount}
	seen := map[string]bool{}
	for _, r := range d.summaries {
		m.vatRows = append(m.vatRows, []string{
			r.category + " " + formatDecimal(r.rate, 0, 2, lang) + " %", money(r.taxable, lang), money(r.vat, lang),
		})
		if r.category == "S" || r.reason == nil || seen[*r.reason] {
			continue
		}
		seen[*r.reason] = true
		reason := *r.reason
		if r.category == "AE" {
			reason = reverseChargeText
		}
		m.reasons = append(m.reasons, reason)
	}

	grossLabel := l.gross
	if d.kind == kindInvoice {
		grossLabel = l.toPay
	}
	m.totals = [][2]string{
		{l.net, money(d.totals.net, lang)}, {l.vatTotal, money(d.totals.vat, lang)}, {grossLabel, money(d.totals.gross, lang)},
	}

	// The payment block, on an invoice only.
	if d.kind == kindInvoice {
		m.payment = append(m.payment, [2]string{l.account, d.bankAccount})
		if d.iban != "" {
			m.payment = append(m.payment, [2]string{l.iban, d.iban})
		}
		if d.bic != "" {
			m.payment = append(m.payment, [2]string{l.bic, d.bic})
		}
		if d.dueDate != nil {
			m.payment = append(m.payment, [2]string{l.dueDate, formatDate(*d.dueDate, lang)})
		}
		m.paymentNote = l.payWithNumber
	}
	m.note, m.footer = d.note, d.footer
	return m
}

// renderPDF lays a model out on A4 and answers the bytes.
func renderPDF(m pdfModel) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(fontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(fontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("invoices: load the PDF font: %w", err)
	}
	cfg := config.NewBuilder().
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: fontFamily, Size: 9}).
		WithSequentialMode().
		WithLeftMargin(15).WithRightMargin(15).WithTopMargin(15).
		WithCreationDate(m.created).
		WithTitle(m.title, true).
		Build()
	doc := maroto.New(cfg)
	bold := props.Text{Style: fontstyle.Bold}
	right := props.Text{Align: align.Right}
	boldRight := props.Text{Style: fontstyle.Bold, Align: align.Right}

	if m.watermark != "" {
		doc.AddRows(text.NewRow(10, m.watermark, props.Text{Style: fontstyle.Bold, Size: 14, Align: align.Center,
			Color: &props.Color{Red: 200, Green: 30, Blue: 30}}))
	}
	doc.AddRows(text.NewRow(12, m.title, props.Text{Style: fontstyle.Bold, Size: 18}))

	// The parties, side by side.
	rows := max(len(m.seller), len(m.buyer))
	for i := range rows {
		cell := func(lines []string) string {
			if i < len(lines) {
				return lines[i]
			}
			return ""
		}
		style := props.Text{}
		if i == 0 {
			style = bold
		}
		doc.AddRow(4.5, text.NewCol(6, cell(m.seller), style), text.NewCol(6, cell(m.buyer), style))
	}
	doc.AddRows(line.NewRow(4))

	for _, kv := range m.meta {
		doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
	}
	if m.creditsLine != "" {
		doc.AddRows(text.NewRow(6, m.creditsLine, props.Text{Style: fontstyle.Bold, Top: 1}))
	}
	doc.AddRows(line.NewRow(4))

	// The line table: description, quantity, unit, unit price, discount, VAT, net.
	sizes := []int{4, 1, 1, 2, 1, 1, 2}
	tableRow := func(cells []string, header bool) core.Row {
		cols := make([]core.Col, 0, len(cells))
		for i, c := range cells {
			style := props.Text{}
			if i > 0 && i != 2 {
				style.Align = align.Right
			}
			if header {
				style.Style = fontstyle.Bold
			}
			cols = append(cols, text.NewCol(sizes[i], c, style))
		}
		return doc.AddAutoRow(cols...)
	}
	tableRow(m.lineHeader, true)
	for _, l := range m.lines {
		tableRow(l, false)
	}
	doc.AddRows(line.NewRow(4))

	// VAT per rate, then the reasons, then the totals.
	doc.AddRow(4.5, col.New(6), text.NewCol(2, m.vatHeader[0], bold), text.NewCol(2, m.vatHeader[1], boldRight), text.NewCol(2, m.vatHeader[2], boldRight))
	for _, r := range m.vatRows {
		doc.AddRow(4.5, col.New(6), text.NewCol(2, r[0]), text.NewCol(2, r[1], right), text.NewCol(2, r[2], right))
	}
	for _, reason := range m.reasons {
		doc.AddAutoRow(text.NewCol(12, reason, props.Text{Top: 1}))
	}
	doc.AddRows(line.NewRow(4))
	for i, kv := range m.totals {
		style := right
		if i == len(m.totals)-1 {
			style = boldRight
		}
		doc.AddRow(5, col.New(6), text.NewCol(3, kv[0], style), text.NewCol(3, kv[1], style))
	}

	if len(m.payment) > 0 {
		doc.AddRows(line.NewRow(4))
		for _, kv := range m.payment {
			doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
		}
		doc.AddRows(text.NewRow(6, m.paymentNote, props.Text{Top: 1}))
	}
	if m.note != "" {
		doc.AddAutoRow(text.NewCol(12, m.note, props.Text{Top: 3}))
	}
	if m.footer != "" {
		doc.AddAutoRow(text.NewCol(12, m.footer, props.Text{Top: 3, Size: 8}))
	}

	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("invoices: render the PDF: %w", err)
	}
	return out.GetBytes(), nil
}
```

**Create** `apps/server/internal/invoices/pdfstore.go`:

```go
package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is D7's store-once path and the two PDF operations. A PDF that was
// never stored was never downloaded, because every download goes through the
// stored object; once a document's hash is set it is never rendered again, and
// the module never deletes an object in its scope. No lock is held around a
// store call: the render reads the document's own committed rows, the object
// is put, and only then is the row told where it is.

// maxStoredPDF is how much of a stored object a download reads (D7).
const maxStoredPDF = 20 << 20

const codeInvoiceDraft = "invoice_draft"

// pdfDocumentOf is an issued document as its PDF prints it: its own rows and
// snapshots, never the settings, the directory or the VAT tables.
func pdfDocumentOf(inv store.InvoicesInvoice, lines []store.InvoicesLine, sums []store.InvoicesVatSummary, original *store.InvoicesInvoice) (pdfDocument, error) {
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	d := pdfDocument{
		kind: inv.Kind, language: str(inv.BuyerLanguage), number: inv.Number, issueDate: inv.IssueDate.Time,
		paymentTermsDays: inv.PaymentTermsDays, yourReference: inv.YourReference, ourReference: inv.OurReference,
		orderRef: inv.OrderReference, note: inv.Note, footer: str(inv.SellerFooterText),
		seller: pdfParty{
			name: str(inv.SellerLegalName), line1: str(inv.SellerAddressLine1), line2: str(inv.SellerAddressLine2),
			postalCode: str(inv.SellerPostalCode), city: str(inv.SellerCity), country: str(inv.SellerCountry),
			organisationNumber: str(inv.SellerOrganisationNumber), email: str(inv.SellerEmail),
			vatRegistered:      inv.SellerVatRegistered != nil && *inv.SellerVatRegistered,
			foretaksregisteret: inv.SellerInForetaksregisteret != nil && *inv.SellerInForetaksregisteret,
		},
		buyer: pdfParty{
			name: str(inv.BuyerName), line1: str(inv.BuyerAddressLine1), line2: str(inv.BuyerAddressLine2),
			postalCode: str(inv.BuyerPostalCode), city: str(inv.BuyerCity), country: str(inv.BuyerCountry),
			organisationNumber: str(inv.BuyerOrganisationNumber), foreignID: str(inv.BuyerForeignID),
		},
		bankAccount: str(inv.SellerBankAccount), iban: str(inv.SellerIban), bic: str(inv.SellerBic),
	}
	if inv.IssuedAt != nil {
		d.created = *inv.IssuedAt
	}
	if inv.DueDate.Valid {
		d.dueDate = &inv.DueDate.Time
	}
	setDelivery(&d, inv)
	for _, l := range lines {
		pl, err := pdfLineOf(l)
		if err != nil {
			return pdfDocument{}, err
		}
		if l.VatRatePercent.Valid {
			if pl.rate, err = ratFromNumeric(l.VatRatePercent); err != nil {
				return pdfDocument{}, err
			}
		}
		d.lines = append(d.lines, pl)
	}
	for _, s := range sums {
		row := vatSummary{category: s.VatCategory, safT: s.SafTCode, reason: s.ExemptionReason}
		var err error
		if row.rate, err = ratFromNumeric(s.RatePercent); err != nil {
			return pdfDocument{}, err
		}
		if row.taxable, err = ratFromNumeric(s.TaxableAmount); err != nil {
			return pdfDocument{}, err
		}
		if row.vat, err = ratFromNumeric(s.VatAmount); err != nil {
			return pdfDocument{}, err
		}
		d.summaries = append(d.summaries, row)
	}
	var err error
	if d.totals.net, err = ratFromNumeric(inv.NetTotal); err != nil {
		return pdfDocument{}, err
	}
	if d.totals.vat, err = ratFromNumeric(inv.VatTotal); err != nil {
		return pdfDocument{}, err
	}
	if d.totals.gross, err = ratFromNumeric(inv.GrossTotal); err != nil {
		return pdfDocument{}, err
	}
	if original != nil && original.Number != nil {
		d.credits = &struct {
			number    int64
			issueDate time.Time
		}{*original.Number, original.IssueDate.Time}
	}
	return d, nil
}

// setDelivery copies a document's delivery and place of delivery.
func setDelivery(d *pdfDocument, inv store.InvoicesInvoice) {
	if inv.DeliveryDate.Valid {
		d.deliveryDate = &inv.DeliveryDate.Time
	}
	if inv.DeliveryFrom.Valid && inv.DeliveryTo.Valid {
		d.deliveryFrom, d.deliveryTo = &inv.DeliveryFrom.Time, &inv.DeliveryTo.Time
	}
	if inv.DeliveryAddressLine1 != nil {
		str := func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		}
		d.deliveryPlace = &pdfParty{
			line1: *inv.DeliveryAddressLine1, line2: str(inv.DeliveryAddressLine2), postalCode: str(inv.DeliveryPostalCode),
			city: str(inv.DeliveryCity), country: str(inv.DeliveryCountry),
		}
	}
}

// pdfLineOf is one stored line's printed amounts; the rate is the caller's.
func pdfLineOf(l store.InvoicesLine) (pdfLine, error) {
	pl := pdfLine{description: l.Description, unit: l.Unit, rate: new(big.Rat)}
	var err error
	if pl.quantity, err = ratFromNumeric(l.Quantity); err != nil {
		return pdfLine{}, err
	}
	if pl.unitPrice, err = ratFromNumeric(l.UnitPrice); err != nil {
		return pdfLine{}, err
	}
	if pl.discount, err = ratFromNumeric(l.DiscountPercent); err != nil {
		return pdfLine{}, err
	}
	if pl.net, err = ratFromNumeric(l.LineNet); err != nil {
		return pdfLine{}, err
	}
	return pl, nil
}

// renderIssued renders an issued document from its own rows.
func renderIssued(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) ([]byte, error) {
	lines, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	sums, err := q.VatSummaries(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's VAT: %w", inv.ID, err)
	}
	var original *store.InvoicesInvoice
	if inv.CreditsInvoiceID != nil {
		o, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
		}
		original = &o
	}
	doc, err := pdfDocumentOf(inv, lines, sums, original)
	if err != nil {
		return nil, err
	}
	return renderPDF(buildPDFModel(doc))
}

// storedPDF is where an issued document's PDF is, and, when this call stored
// it, the bytes it stored.
type storedPDF struct {
	key, sha256 string
	body        []byte
}

// storeOnce is D7's store-once path: render, hash, key the object by the
// hash, put it unless it is there, and record it on the row only while the
// row has no hash. A loser of a race sees no row updated, re-reads the row and
// answers the winner's key without bytes; its own object is an orphan nothing
// ever serves.
func (s *server) storeOnce(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (storedPDF, error) {
	body, err := renderIssued(ctx, q, inv)
	if err != nil {
		return storedPDF{}, err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	key := fmt.Sprintf("documents/%d/%d-%s.pdf", inv.ID, *inv.Number, hash)
	exists, err := s.objectExists(ctx, key)
	if err != nil {
		return storedPDF{}, err
	}
	if !exists {
		if err := s.objectPut(ctx, key, bytes.NewReader(body), "application/pdf"); err != nil {
			return storedPDF{}, err
		}
	}
	n, err := q.SetDocumentPDF(ctx, store.SetDocumentPDFParams{ID: inv.ID, PdfObjectKey: &key, PdfSha256: &hash})
	if err != nil {
		return storedPDF{}, fmt.Errorf("invoices: record document %d's PDF: %w", inv.ID, err)
	}
	if n == 1 {
		return storedPDF{key: key, sha256: hash, body: body}, nil
	}
	winner, err := q.GetInvoice(ctx, inv.ID)
	if err != nil {
		return storedPDF{}, fmt.Errorf("invoices: re-read document %d: %w", inv.ID, err)
	}
	if winner.PdfObjectKey == nil || winner.PdfSha256 == nil {
		return storedPDF{}, fmt.Errorf("invoices: document %d lost its PDF race to nobody", inv.ID)
	}
	return storedPDF{key: *winner.PdfObjectKey, sha256: *winner.PdfSha256}, nil
}

// storeAfterIssue is the store-once path right after an issue commits. A
// failure is logged at warn and never fails the issue: the number is
// committed, and the next download stores the PDF (D6).
func (s *server) storeAfterIssue(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) store.InvoicesInvoice {
	if _, err := s.storeOnce(ctx, q, inv); err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the PDF of an issued document could not be stored; the next download stores it",
			"invoice_id", inv.ID, "number", *inv.Number, "error", err.Error())
		return inv
	}
	stored, err := q.GetInvoice(ctx, inv.ID)
	if err != nil {
		return inv
	}
	return stored
}

// fileName is the download's name in the document's language (D7).
func fileName(inv store.InvoicesInvoice) string {
	english := inv.BuyerLanguage != nil && *inv.BuyerLanguage == "en"
	switch {
	case inv.Kind == kindCreditNote && english:
		return fmt.Sprintf("credit-note-%d.pdf", *inv.Number)
	case inv.Kind == kindCreditNote:
		return fmt.Sprintf("kreditnota-%d.pdf", *inv.Number)
	case english:
		return fmt.Sprintf("invoice-%d.pdf", *inv.Number)
	}
	return fmt.Sprintf("faktura-%d.pdf", *inv.Number)
}

// storageUnavailable is the 503 a download answers when the store is not
// configured or cannot be read.
func storageUnavailable(detail string) gen.InvoicesConflictProblem {
	c := conflict(codeStorageUnavailable, storageUnavailableTitle, detail)
	c.Status = ptr(int32(http.StatusServiceUnavailable))
	return c
}

// storedDocumentBroken is the 500 a download answers when the stored object is
// gone or its bytes no longer match its hash — an operator problem, logged at
// error and never papered over by rendering again.
func storedDocumentBroken() apicommon.ProblemDetails {
	return apicommon.ProblemStatus("The stored document is damaged",
		"The document's stored PDF is missing or does not match what was stored. It is not rendered again; the operator has been told.",
		http.StatusInternalServerError)
}

// pdfDownload and pdfPreview set the headers a PDF answers with before the
// generated response writes the content type and the status: never cached,
// never sniffed, and named. A document is as private as the invoicing it
// belongs to.
type pdfDownload struct {
	body        gen.GetInvoicesByIdPdf200ApplicationpdfResponse
	disposition string
}

func (r pdfDownload) VisitGetInvoicesByIdPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesByIdPdfResponse(w)
}

type pdfPreview struct {
	body        gen.GetInvoicesByIdPreviewPdf200ApplicationpdfResponse
	disposition string
}

func (r pdfPreview) VisitGetInvoicesByIdPreviewPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesByIdPreviewPdfResponse(w)
}

func pdfHeaders(w http.ResponseWriter, disposition string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", disposition)
}

// GetInvoicesByIdPdf Download an issued document's PDF
// (GET /api/v1/invoices/{id}/pdf)
func (s *server) GetInvoicesByIdPdf(ctx context.Context, req gen.GetInvoicesByIdPdfRequestObject) (gen.GetInvoicesByIdPdfResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesByIdPdf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusIssued {
		return gen.GetInvoicesByIdPdf409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, "The document is a draft",
			"A draft has no document to download yet; preview it instead.")), nil
	}
	if !s.storageConfigured {
		return gen.GetInvoicesByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"This installation has no object store.")), nil
	}
	found := storedPDF{}
	if inv.PdfSha256 == nil {
		if found, err = s.storeOnce(ctx, q, inv); err != nil {
			s.deps.Logger.ErrorContext(ctx, "invoices: a PDF could not be stored on download", "invoice_id", inv.ID, "error", err.Error())
			return gen.GetInvoicesByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable(
				"The document store could not be reached. Try again.")), nil
		}
	} else {
		found = storedPDF{key: *inv.PdfObjectKey, sha256: *inv.PdfSha256}
	}
	body := found.body
	if body == nil {
		rc, err := s.objectGet(ctx, found.key)
		switch {
		case errors.Is(err, storage.ErrNotExist):
			s.deps.Logger.ErrorContext(ctx, "invoices: an issued document's stored PDF is gone", "invoice_id", inv.ID, "key", found.key)
			return gen.GetInvoicesByIdPdf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
		case err != nil:
			s.deps.Logger.WarnContext(ctx, "invoices: the document store could not be read", "invoice_id", inv.ID, "error", err.Error())
			return gen.GetInvoicesByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable(
				"The document store could not be read. Try again.")), nil
		}
		body, err = io.ReadAll(io.LimitReader(rc, maxStoredPDF+1))
		_ = rc.Close()
		if err != nil {
			return gen.GetInvoicesByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable(
				"The document store could not be read. Try again.")), nil
		}
		sum := sha256.Sum256(body)
		if len(body) > maxStoredPDF || hex.EncodeToString(sum[:]) != found.sha256 {
			s.deps.Logger.ErrorContext(ctx, "invoices: an issued document's stored PDF does not match its hash",
				"invoice_id", inv.ID, "key", found.key)
			return gen.GetInvoicesByIdPdf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
		}
	}
	return pdfDownload{
		body:        gen.GetInvoicesByIdPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", fileName(inv)),
	}, nil
}

// GetInvoicesByIdPreviewPdf Preview a draft as PDF
// (GET /api/v1/invoices/{id}/preview.pdf)
//
// A draft as it would be if issued today, rendered on demand and never
// stored: the current settings (even incomplete), the customer's billing
// profile as it is now for an invoice draft and the copied snapshot for a
// credit-note draft, today's rates, the watermark and no number.
func (s *server) GetInvoicesByIdPreviewPdf(ctx context.Context, req gen.GetInvoicesByIdPreviewPdfRequestObject) (gen.GetInvoicesByIdPreviewPdfResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesByIdPreviewPdf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusDraft {
		return gen.GetInvoicesByIdPreviewPdf409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	}
	var profile *contracts.CustomerBillingProfile
	if inv.Kind == kindInvoice {
		if profile, err = s.customerProfile(ctx, inv.CustomerID); err != nil {
			return nil, err
		}
	}
	body, err := s.renderPreview(ctx, q, inv, profile)
	if err != nil {
		return nil, err
	}
	return pdfPreview{
		body:        gen.GetInvoicesByIdPreviewPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("inline; filename=\"utkast-%d.pdf\"", inv.ID),
	}, nil
}

// renderPreview is a draft rendered as if issued today (D4's preview).
func (s *server) renderPreview(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile) ([]byte, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	stored, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	today := businessDay(s.deps.Clock())
	codes, err := vatCodesOn(ctx, q, pgDate(today))
	if err != nil {
		return nil, err
	}
	var params store.IssueDocumentParams
	if profile != nil {
		params = *buyerSnapshot(profile)
	} else {
		keepBuyer(&params, inv)
	}
	sellerSnapshot(&params, settings)
	draft := inv
	draft.Number, draft.IssueDate, draft.IssuedAt = nil, pgDate(today), ptr(s.deps.Clock())
	copyIssueParams(&draft, params)
	if inv.Kind == kindInvoice && inv.PaymentTermsDays != nil {
		draft.DueDate = pgDate(today.AddDate(0, 0, int(*inv.PaymentTermsDays)))
	}
	lines, err := storedDraftLines(stored)
	if err != nil {
		return nil, err
	}
	taxed := taxedLines(lines, codes)
	summaries, totals, _ := summarize(taxed, big.NewRat(1, 1))
	var original *store.InvoicesInvoice
	if inv.CreditsInvoiceID != nil {
		o, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
		if err != nil {
			return nil, err
		}
		original = &o
	}
	doc, err := pdfDocumentOf(draft, stored, nil, original)
	if err != nil {
		return nil, err
	}
	for i := range doc.lines {
		doc.lines[i].rate = taxed[i].rate
	}
	doc.summaries, doc.totals, doc.preview = summaries, totals, true
	return renderPDF(buildPDFModel(doc))
}

// copyIssueParams puts a would-be issue's snapshots on a draft's row, for its
// preview.
func copyIssueParams(inv *store.InvoicesInvoice, p store.IssueDocumentParams) {
	inv.BuyerCustomerNumber, inv.BuyerType, inv.BuyerName = p.BuyerCustomerNumber, p.BuyerType, p.BuyerName
	inv.BuyerOrganisationNumber, inv.BuyerForeignID = p.BuyerOrganisationNumber, p.BuyerForeignID
	inv.BuyerAddressLine1, inv.BuyerAddressLine2 = p.BuyerAddressLine1, p.BuyerAddressLine2
	inv.BuyerPostalCode, inv.BuyerCity, inv.BuyerRegion, inv.BuyerCountry = p.BuyerPostalCode, p.BuyerCity, p.BuyerRegion, p.BuyerCountry
	inv.BuyerPeppolID, inv.BuyerGln, inv.BuyerLanguage = p.BuyerPeppolID, p.BuyerGln, p.BuyerLanguage
	inv.SellerLegalName, inv.SellerOrganisationNumber = p.SellerLegalName, p.SellerOrganisationNumber
	inv.SellerVatRegistered, inv.SellerInForetaksregisteret = p.SellerVatRegistered, p.SellerInForetaksregisteret
	inv.SellerAddressLine1, inv.SellerAddressLine2 = p.SellerAddressLine1, p.SellerAddressLine2
	inv.SellerPostalCode, inv.SellerCity, inv.SellerCountry = p.SellerPostalCode, p.SellerCity, p.SellerCountry
	inv.SellerBankAccount, inv.SellerIban, inv.SellerBic = p.SellerBankAccount, p.SellerIban, p.SellerBic
	inv.SellerEmail, inv.SellerFooterText = p.SellerEmail, p.SellerFooterText
}
```

**Replace** in `apps/server/internal/invoices/issue.go`:

```go
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, issued, nil)
	if err != nil {
		return nil, err
```

**with**:

```go
	if err != nil {
		return nil, err
	}
	// After the commit, the PDF is stored once (D7). A failure there is
	// logged and never fails the issue; pdfStored says so.
	issued = s.storeAfterIssue(ctx, q, issued)
	resp, err := s.invoiceResponse(ctx, q, issued, nil)
	if err != nil {
		return nil, err
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go mod tidy && cd ../..
git diff -- apps/server/go.mod | grep '^[+-]' | grep -v '^+++\|^---'   # + maroto/v2 v2.4.2 and gofpdf v1.4.3 direct; boombuler/barcode to v1.1.0; the new indirects
```

- [ ] **Step 5: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bun run gen:client
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go build ./... && mise exec -- go vet ./...
mise exec -- go test -count=1 ./internal/invoices/ ./internal/openapi/ ./cmd/...
mise exec -- golangci-lint run ./...
cd ../..
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task5.txt <<'MSG'
feat(invoices): the PDF, rendered from the snapshot and stored once

The document's PDF (invoices foundation design D7), in maroto v2 over gofpdf
with Noto Sans embedded (SIL OFL 1.1, fonts/LICENSE): rendered from the
issued document's own rows and snapshots only, hashed, stored once under
documents/<id>/<number>-<sha256>.pdf in the invoices scope and recorded on
the row the first time. The issue stores it after its commit and answers
pdfStored false if that fails; every download streams the stored object,
verified, and never renders a stored document again; a missing or altered
object is a 500, a store that cannot be read a 503. The draft preview
carries the watermark and no number and stores nothing. Catalog sorting and
a fixed modification date make the bytes reproducible in-process.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/go.mod' 'apps/server/go.sum' 'apps/server/internal/invoices/contractscalls.go' 'apps/server/internal/invoices/fonts/LICENSE' 'apps/server/internal/invoices/fonts/NotoSans-Bold.ttf' 'apps/server/internal/invoices/fonts/NotoSans-Regular.ttf' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/issue.go' 'apps/server/internal/invoices/pdf.go' 'apps/server/internal/invoices/pdf_internal_test.go' 'apps/server/internal/invoices/pdfstore.go' 'apps/server/internal/invoices/pdfstore_test.go' 'apps/server/internal/invoices/queries/invoices.sql' 'apps/server/internal/invoices/store/invoices.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git commit -F /tmp/claude-1000/msg-invoices-task5.txt -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/go.mod' 'apps/server/go.sum' 'apps/server/internal/invoices/contractscalls.go' 'apps/server/internal/invoices/fonts/LICENSE' 'apps/server/internal/invoices/fonts/NotoSans-Bold.ttf' 'apps/server/internal/invoices/fonts/NotoSans-Regular.ttf' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/harness_test.go' 'apps/server/internal/invoices/issue.go' 'apps/server/internal/invoices/pdf.go' 'apps/server/internal/invoices/pdf_internal_test.go' 'apps/server/internal/invoices/pdfstore.go' 'apps/server/internal/invoices/pdfstore_test.go' 'apps/server/internal/invoices/queries/invoices.sql' 'apps/server/internal/invoices/store/invoices.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 6: Credit notes: the draft copy, what it may change, and the caps under the original's lock (D8)

`POST /invoices/{id}/credit` copies an issued invoice into a credit-note draft — the buyer snapshot included, no directory read — with every line pointing at the line it credits. A credit draft's `PUT` allows only what D8 lists; its totals use the original lines' snapshot rates. At issue the original is locked after the counter and both caps decide; a credit note skips every gate meant for new invoices. Responses link both ways.

**Files:**
- Create: `apps/server/internal/invoices/credits.go`, `apps/server/internal/invoices/credits_test.go`, `apps/server/internal/invoices/queries/credits.sql`
- Modify: `apps/server/internal/invoices/drafts.go`, `apps/server/internal/invoices/issue.go`, `apps/server/internal/invoices/responses.go`, `openapi/invoices.yaml`
- Generated (commit them; never edit by hand): `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/credits.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/invoices/{issue.go,drafts.go,responses.go}`

**Interfaces:**
- Produces wire: `POST /invoices/{id}/credit` → 201 `InvoicesInvoiceResponse` (a credit-note draft); 409 `invoice_draft`, `credit_note_not_creditable`, `invoice_fully_credited`; at issue 409 `credit_exceeds_line` (+`linePosition`), `credit_exceeds_invoice`; the response's `creditedAmount`, `uncreditedAmount`, `creditNotes`, `credits`; warnings `credit_exceeds_line`, `credit_exceeds_invoice`.
- Produces SQL: `InsertCreditDraft`, `CopyLinesToCredit`, `CreditNotesOf`, `CreditedGross`, `CreditedPerLine`.
- Produces Go: `putCreditDraft`, `creditIssueChecks`, `creditCaps`, `creditTaxedLines`, `creditLinks`, `uncredited`, `originalLines`, `snapshotOf`.

- [ ] **Step 1: The tests: the copy, what a draft may change, both caps, the race**

**Create** `apps/server/internal/invoices/credits_test.go`:

```go
package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func creditPath(id int64) string { return fmt.Sprintf("%s/%d/credit", invoicesPath, id) }

// creditDraft makes a credit-note draft of an issued invoice.
func creditDraft(t *testing.T, h *harness, original int64) invoiceJSON {
	t.Helper()
	res := issuer(t, h).Do(http.MethodPost, creditPath(original), nil)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /credit = %d %s, want 201", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// creditBody is a replace of a credit draft keeping everything but lines.
func creditBody(c invoiceJSON, lines ...map[string]any) map[string]any {
	body := map[string]any{
		"customerId": c.CustomerID, "yourReference": c.YourReference, "ourReference": c.OurReference,
		"orderReference": c.OrderReference, "revision": c.Revision, "lines": lines,
	}
	if c.DeliveryDate != nil {
		body["deliveryDate"] = *c.DeliveryDate
	}
	if lines == nil {
		body["lines"] = []map[string]any{}
	}
	return body
}

// creditLine is a replace line crediting l.
func creditLine(l lineJSON) map[string]any {
	return map[string]any{
		"description": l.Description, "quantity": l.Quantity, "unit": l.Unit, "unitPrice": l.UnitPrice,
		"discountPercent": l.DiscountPercent, "vatCodeId": l.VatCodeID, "creditsLineId": *l.CreditsLineID,
	}
}

func saveCredit(t *testing.T, h *harness, c invoiceJSON, body map[string]any) invoiceJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodPut, invoicePath(c.ID), body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT a credit draft = %d %s", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// A credit-note draft copies what the correction must name, from the
// original, with no directory read; it is issued in the same series, for a
// customer archived since and with a VAT code deactivated since, at the
// original's rates; the two documents link to each other (D8).
func TestCredit_TheDraftCopiesAndIsIssuedInTheSameSeries(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := draftBody(customerAcme, line("Konsulenttime", 10, 1000, vat25), line("Kurs", 1, 2000, vatExempt))
	body["deliveryAddress"] = map[string]any{"line1": "Byggeplassen", "city": "Bergen", "country": "NO"}
	body["ourReference"] = "Ola"
	original := issued(t, h, createDraft(t, h, body).ID)

	h.customers.setStatus(customerAcme, "archived")
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat25)
	reads := 0
	h.customers.afterProfileRead(func(int32) { reads++ })

	c := creditDraft(t, h, original.ID)
	if c.Kind != "credit_note" || c.Status != "draft" || c.PaymentTermsDays != nil || c.Credits == nil || c.Credits.Number != *original.Number {
		t.Fatalf("credit draft = %+v", c)
	}
	if c.Buyer == nil || c.Buyer.Name != original.Buyer.Name || *c.DeliveryDate != *original.DeliveryDate ||
		c.OurReference != "Ola" || c.YourReference != "PO-77" || len(c.Lines) != 2 {
		t.Errorf("credit draft = %+v, want the original's buyer, delivery, references and lines", c)
	}
	if *c.Lines[0].CreditsLineID != original.Lines[0].ID || c.Lines[0].VatCodeID != vat25 {
		t.Errorf("line 1 = %+v, want it pointing at the original's line 1 on its code", c.Lines[0])
	}
	if c.GrossTotal != original.GrossTotal {
		t.Errorf("credit draft gross = %v, want the original's %v at the original's rates", c.GrossTotal, original.GrossTotal)
	}

	issuedCredit := issued(t, h, c.ID)
	if *issuedCredit.Number != 2 || issuedCredit.DueDate != nil || issuedCredit.VatTotal != 2500 || issuedCredit.GrossTotal != 14500 {
		t.Errorf("issued credit = number %d due %v vat %v gross %v, want 2, none, 2500, 14500", *issuedCredit.Number, issuedCredit.DueDate, issuedCredit.VatTotal, issuedCredit.GrossTotal)
	}
	if l := issuedCredit.Lines[0]; *l.VatRatePercent != 25 || *l.VatCategory != "S" {
		t.Errorf("a deactivated code's line = %+v, want the original's 25 %% S", l)
	}
	if reads != 0 {
		t.Errorf("the credit note read the directory %d times, want never", reads)
	}
	after := getInvoice(t, h, original.ID)
	if after.CreditedAmount == nil || *after.CreditedAmount != 14500 || *after.UncreditedAmount != 0 ||
		len(after.CreditNotes) != 1 || after.CreditNotes[0].ID != c.ID || *after.CreditNotes[0].Number != 2 {
		t.Errorf("the original = credited %v uncredited %v notes %+v", after.CreditedAmount, after.UncreditedAmount, after.CreditNotes)
	}
	if res := download(t, h, issuedCredit.ID); res.Header("Content-Disposition") != `attachment; filename="kreditnota-2.pdf"` {
		t.Errorf("a credit note's file = %q", res.Header("Content-Disposition"))
	}
	if res := issuer(t, h).Do(http.MethodPost, creditPath(original.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_fully_credited" {
		t.Errorf("crediting a fully credited invoice = %d %s, want 409 invoice_fully_credited", res.Status, res.Body)
	}
	if res := issuer(t, h).Do(http.MethodPost, creditPath(issuedCredit.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "credit_note_not_creditable" {
		t.Errorf("crediting a credit note = %d %s, want 409 credit_note_not_creditable", res.Status, res.Body)
	}
	draft := createDraft(t, h, draftBody(customerNoTerms, line("A", 1, 100, vat15)))
	if res := issuer(t, h).Do(http.MethodPost, creditPath(draft.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_draft" {
		t.Errorf("crediting a draft = %d %s, want 409 invoice_draft", res.Status, res.Body)
	}
	if res := creator(t, h).Do(http.MethodPost, creditPath(original.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("POST /credit without invoices:issue = %d, want 403", res.Status)
	}
}

// What a credit draft may change, and a 400 on the field for everything else
// (D8).
func TestCredit_WhatADraftMayChange(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	disc := line("Rabattert", 4, 500, vat25)
	disc["discountPercent"] = 10
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 1000, vat25), disc)).ID)
	c := creditDraft(t, h, original.ID)

	lower := creditLine(c.Lines[0])
	lower["quantity"], lower["unitPrice"], lower["description"] = 2, 900, "Prisavslag"
	body := creditBody(c, lower)
	body["note"] = "Retting av pris"
	saved := saveCredit(t, h, c, body)
	if len(saved.Lines) != 1 || saved.Lines[0].LineNet != 1800 || saved.Lines[0].Description != "Prisavslag" || saved.Note != "Retting av pris" || saved.GrossTotal != 2250 {
		t.Errorf("a lowered credit = %+v", saved)
	}

	for _, bad := range []struct {
		name  string
		edit  func(map[string]any, []map[string]any)
		field string
	}{
		{"a raised quantity", func(_ map[string]any, l []map[string]any) { l[0]["quantity"] = 11 }, "lines[0].quantity"},
		{"a raised price", func(_ map[string]any, l []map[string]any) { l[0]["unitPrice"] = 1001 }, "lines[0].unitPrice"},
		{"a lowered discount", func(_ map[string]any, l []map[string]any) { l[1]["discountPercent"] = 5 }, "lines[1].discountPercent"},
		{"another VAT code", func(_ map[string]any, l []map[string]any) { l[0]["vatCodeId"] = vat15 }, "lines[0].vatCodeId"},
		{"a line twice", func(_ map[string]any, l []map[string]any) { l[1]["creditsLineId"] = l[0]["creditsLineId"] }, "lines[1].creditsLineId"},
		{"a new line", func(_ map[string]any, l []map[string]any) { delete(l[1], "creditsLineId") }, "lines[1].creditsLineId"},
		{"another customer", func(b map[string]any, _ []map[string]any) { b["customerId"] = customerNoTerms }, "customerId"},
		{"another currency", func(b map[string]any, _ []map[string]any) { b["currency"] = "EUR" }, "currency"},
		{"payment terms", func(b map[string]any, _ []map[string]any) { b["paymentTermsDays"] = 14 }, "paymentTermsDays"},
		{"another reference", func(b map[string]any, _ []map[string]any) { b["yourReference"] = "Ny" }, "yourReference"},
		{"another delivery", func(b map[string]any, _ []map[string]any) { b["deliveryDate"] = "2026-09-11" }, "deliveryDate"},
	} {
		lines := []map[string]any{creditLine(c.Lines[0]), creditLine(c.Lines[1])}
		body := creditBody(saved, lines...)
		bad.edit(body, lines)
		res := creator(t, h).Do(http.MethodPut, invoicePath(c.ID), body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[bad.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", bad.name, res.Status, res.Body, bad.field)
		}
	}
}

// The per-line cap (D8): line A at 25 % credited in full once, then again,
// is refused although the total still fits, because the 0 % line B was never
// credited; the cap only warns on the save.
func TestCredit_ThePerLineCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25), line("B", 1, 5000, vatZero))).ID)

	first := creditDraft(t, h, original.ID)
	issued(t, h, saveCredit(t, h, first, creditBody(first, creditLine(first.Lines[0]))).ID)

	second := creditDraft(t, h, original.ID)
	saved := saveCredit(t, h, second, creditBody(second, creditLine(second.Lines[0])))
	if !slices.Contains(saved.Warnings, "credit_exceeds_line") {
		t.Errorf("warnings on the save = %v, want credit_exceeds_line", saved.Warnings)
	}
	if saved.GrossTotal > *getInvoice(t, h, original.ID).UncreditedAmount {
		t.Fatal("the fixture's total does not fit, so it proves nothing about the line cap")
	}
	p := refusedWith(t, h, saved.ID, "", "credit_exceeds_line")
	if p.LinePosition == nil || *p.LinePosition != 1 {
		t.Errorf("credit_exceeds_line names line %v, want 1", p.LinePosition)
	}
	if n := counterNext(t, h); n != 3 {
		t.Errorf("counter next = %d, want 3: the refused credit note's number rolled back", n)
	}
}

// The headline cap: a credit note's gross never passes what the invoice has
// left, counting every issued credit note.
func TestCredit_TheHeadlineCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	c := creditDraft(t, h, original.ID)
	// An issued credit note of 1000 with no lines — planted, so the line cap
	// has nothing to count and only the headline decides.
	modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('credit_note', 'issued', 50, $1, $2, '2026-09-12', 1000, now(), $3, now(), now()) RETURNING id`, customerAcme, original.ID, uuid.New())

	if got := getInvoice(t, h, c.ID); !slices.Contains(got.Warnings, "credit_exceeds_invoice") {
		t.Errorf("warnings = %v, want credit_exceeds_invoice", got.Warnings)
	}
	refusedWith(t, h, c.ID, "", "credit_exceeds_invoice")
}

// Two credit notes racing against one original cannot together pass a line's
// cap: the second issue waits on the original's lock and sees the first.
func TestCredit_RacingCreditNotesKeepTheCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	a, b := creditDraft(t, h, original.ID), creditDraft(t, h, original.ID)

	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i, id := range []int64{a.ID, b.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := issueWith(t, h, id, "")
			statuses[i] = res.Status
			if res.Status == http.StatusConflict && problemOf(t, res).Code != "credit_exceeds_line" {
				t.Errorf("the losing issue = %s, want credit_exceeds_line", problemOf(t, res).Code)
			}
		}()
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Errorf("statuses = %v, want one issued and one refused", statuses)
	}
}
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 -run TestCredit ./internal/invoices/ ; cd ../..   # FAIL: POST /credit is 404
```

- [ ] **Step 2: The contract and the queries**

**Insert** into `openapi/invoices.yaml`, immediately before the line `servers:`:

```yaml
    /api/v1/invoices/{id}/credit:
        post:
            description: 'Creates a draft credit note for an issued invoice (D8, § 5-2-7): the customer, currency and rate, the delivery, the references and the buyer snapshot are copied from the original, and every line with its VAT code, pointing at the line it credits. It reads no directory. Several credit-note drafts may exist at once; the caps are decided at issue.'
            operationId: postInvoicesByIdCredit
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int64
                    type: integer
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesInvoiceResponse'
                    description: Created — the credit-note draft.
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
                    description: Not Found — no document has that id.
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/InvoicesConflictProblem'
                    description: Conflict — invoice_draft (the original is not issued), credit_note_not_creditable (a credit note is never credited) or invoice_fully_credited.
            summary: Create a credit-note draft
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access+invoices:issue
```

**Create** `apps/server/internal/invoices/queries/credits.sql`:

```sql
-- name: InsertCreditDraft :one
-- InsertCreditDraft writes a credit-note draft for an issued invoice (D8),
-- copying what the correction must name: the customer, the currency and rate,
-- the delivery and its place, the references, and the buyer snapshot. It reads
-- no directory: the original's snapshot is what the correction names, an
-- anonymised customer's included.
INSERT INTO invoices.invoices (
    kind, customer_id, credits_invoice_id, delivery_date, delivery_from, delivery_to,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city, delivery_country,
    currency, exchange_rate, your_reference, our_reference, order_reference,
    buyer_customer_number, buyer_type, buyer_name, buyer_organisation_number, buyer_foreign_id,
    buyer_address_line1, buyer_address_line2, buyer_postal_code, buyer_city, buyer_region, buyer_country,
    buyer_peppol_id, buyer_gln, buyer_language,
    net_total, vat_total, gross_total, vat_total_nok, created_by_user_id, created_at, updated_at
)
SELECT
    'credit_note', o.customer_id, o.id, o.delivery_date, o.delivery_from, o.delivery_to,
    o.delivery_address_line1, o.delivery_address_line2, o.delivery_postal_code, o.delivery_city, o.delivery_country,
    o.currency, o.exchange_rate, o.your_reference, o.our_reference, o.order_reference,
    o.buyer_customer_number, o.buyer_type, o.buyer_name, o.buyer_organisation_number, o.buyer_foreign_id,
    o.buyer_address_line1, o.buyer_address_line2, o.buyer_postal_code, o.buyer_city, o.buyer_region, o.buyer_country,
    o.buyer_peppol_id, o.buyer_gln, o.buyer_language,
    o.net_total, o.vat_total, o.gross_total, o.vat_total_nok, @created_by_user_id, @now::timestamptz, @now::timestamptz
FROM invoices.invoices o
WHERE o.id = @original_id AND o.kind = 'invoice' AND o.status = 'issued'
RETURNING *;

-- name: CopyLinesToCredit :exec
-- CopyLinesToCredit copies every line of the original onto its credit-note
-- draft, each pointing at the line it credits (D8). The VAT snapshot is the
-- credit note's issue's to write, from the original line's.
INSERT INTO invoices.lines (
    invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
    credits_line_id, line_gross, line_allowance, line_net
)
SELECT sqlc.arg(credit_id)::bigint, src.position, src.description, src.quantity, src.unit, src.unit_price,
       src.discount_percent, src.vat_code_id, src.id, src.line_gross, src.line_allowance, src.line_net
FROM invoices.lines src
WHERE src.invoice_id = sqlc.arg(original_id)::bigint AND src.quantity > 0
ORDER BY src.position;

-- name: CreditNotesOf :many
-- CreditNotesOf is every credit note of an invoice, drafts included, the
-- oldest first.
SELECT id, number, issue_date, gross_total, status FROM invoices.invoices
WHERE credits_invoice_id = @original_id
ORDER BY id;

-- name: CreditedGross :one
-- CreditedGross is what an invoice's issued credit notes credit, gross.
SELECT coalesce(sum(gross_total), 0)::numeric(14,2) AS credited FROM invoices.invoices
WHERE credits_invoice_id = @original_id AND status = 'issued';

-- name: CreditedPerLine :many
-- CreditedPerLine is, per line of an invoice, the quantity and the net its
-- issued credit notes credit: what the per-line cap is judged against (D8).
SELECT l.credits_line_id::bigint AS line_id,
       sum(l.quantity)::numeric(14,3) AS quantity,
       sum(l.line_net)::numeric(14,2) AS net
FROM invoices.lines l
JOIN invoices.invoices c ON c.id = l.invoice_id
WHERE c.credits_invoice_id = @original_id AND c.status = 'issued' AND l.credits_line_id IS NOT NULL
GROUP BY l.credits_line_id;
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 3: Credit notes**

**Create** `apps/server/internal/invoices/credits.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the credit note (D8, § 5-2-7): the correction the law requires,
// in the same series as the invoice it reverses. A credit-note draft is a copy
// of an issued invoice; it may only take lines away and lower amounts, and at
// its issue the caps — per original line, and on the headline — are decided
// under the original's lock. A credit note skips every gate meant for new
// invoices, because an invoice to a customer since archived, anonymised or
// blocked must still be correctable, and it re-looks up no VAT rate: it
// reverses the original's treatment, rate for rate.

// The codes and titles of the credit note's refusals and warnings.
const (
	codeCreditNoteNotCreditable = "credit_note_not_creditable"
	codeInvoiceFullyCredited    = "invoice_fully_credited"
	codeCreditExceedsLine       = "credit_exceeds_line"
	codeCreditExceedsInvoice    = "credit_exceeds_invoice"
	cannotCreditTitle           = "The invoice cannot be credited"
)

// creditedGross is what an invoice's issued credit notes credit, gross.
func creditedGross(ctx context.Context, q *store.Queries, originalID int64) (*big.Rat, error) {
	n, err := q.CreditedGross(ctx, &originalID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what document %d is credited: %w", originalID, err)
	}
	return ratFromNumeric(n)
}

// uncredited is an issued invoice's gross less what its issued credit notes
// credit.
func uncredited(ctx context.Context, q *store.Queries, original store.InvoicesInvoice) (credited, left *big.Rat, err error) {
	credited, err = creditedGross(ctx, q, original.ID)
	if err != nil {
		return nil, nil, err
	}
	gross, err := ratFromNumeric(original.GrossTotal)
	if err != nil {
		return nil, nil, err
	}
	return credited, new(big.Rat).Sub(gross, credited), nil
}

// PostInvoicesByIdCredit Create a credit-note draft
// (POST /api/v1/invoices/{id}/credit)
func (s *server) PostInvoicesByIdCredit(ctx context.Context, req gen.PostInvoicesByIdCreditRequestObject) (gen.PostInvoicesByIdCreditResponseObject, error) {
	q := store.New(s.deps.Pool)
	original, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdCredit404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	switch {
	case original.Status != statusIssued:
		return gen.PostInvoicesByIdCredit409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, cannotCreditTitle,
			"A draft is not a sales document: change or delete it instead.")), nil
	case original.Kind == kindCreditNote:
		return gen.PostInvoicesByIdCredit409ApplicationProblemPlusJSONResponse(conflict(codeCreditNoteNotCreditable, cannotCreditTitle,
			"A credit note is never itself credited.")), nil
	}
	_, left, err := uncredited(ctx, q, original)
	if err != nil {
		return nil, err
	}
	if left.Sign() <= 0 {
		return gen.PostInvoicesByIdCredit409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceFullyCredited, cannotCreditTitle,
			"This invoice is already credited in full.")), nil
	}
	var created store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		created, err = txq.InsertCreditDraft(ctx, store.InsertCreditDraftParams{
			OriginalID: original.ID, CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: create a credit note for %d: %w", original.ID, err)
		}
		return txq.CopyLinesToCredit(ctx, store.CopyLinesToCreditParams{CreditID: created.ID, OriginalID: original.ID})
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, created, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdCredit201JSONResponse(resp), nil
}

// originalLines are an invoice's lines by id.
func originalLines(ctx context.Context, q *store.Queries, originalID int64) (map[int64]store.InvoicesLine, error) {
	lines, err := q.Lines(ctx, originalID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's lines: %w", originalID, err)
	}
	out := make(map[int64]store.InvoicesLine, len(lines))
	for _, l := range lines {
		out[l.ID] = l
	}
	return out, nil
}

// snapshotOf is an issued line's VAT treatment, as a credit of it carries it.
func snapshotOf(l store.InvoicesLine) (taxedLine, error) {
	rate, err := ratFromNumeric(l.VatRatePercent)
	if err != nil {
		return taxedLine{}, err
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return taxedLine{category: deref(l.VatCategory), rate: rate, safT: deref(l.SafTCode), reason: l.ExemptionReason}, nil
}

// creditTaxedLines are a credit note's lines taxed with their original lines'
// snapshots — never the rates in force today (D8).
func creditTaxedLines(lines []draftLine, credits []*int64, originals map[int64]store.InvoicesLine) ([]taxedLine, error) {
	out := make([]taxedLine, 0, len(lines))
	for i, l := range lines {
		var taxed taxedLine
		if credits[i] != nil {
			if o, ok := originals[*credits[i]]; ok {
				var err error
				if taxed, err = snapshotOf(o); err != nil {
					return nil, err
				}
			}
		}
		if taxed.rate == nil {
			taxed.rate = new(big.Rat)
		}
		taxed.net = l.amounts.net
		out = append(out, taxed)
	}
	return out, nil
}

// creditCaps is D8's two caps over a credit note's lines: per original line,
// the quantity and the net credited by the issued credit notes and this one
// may not pass the original's; and the headline, this one's gross may not pass
// what the original has left. It answers the refusal code, the credit note's
// line position the line cap names, and the message; "" when both hold.
func creditCaps(ctx context.Context, q *store.Queries, original store.InvoicesInvoice, lines []store.InvoicesLine, gross *big.Rat) (string, *int32, string, error) {
	originals, err := originalLines(ctx, q, original.ID)
	if err != nil {
		return "", nil, "", err
	}
	rows, err := q.CreditedPerLine(ctx, &original.ID)
	if err != nil {
		return "", nil, "", fmt.Errorf("invoices: read what document %d's lines are credited: %w", original.ID, err)
	}
	type credited struct{ quantity, net *big.Rat }
	already := map[int64]credited{}
	for _, r := range rows {
		qty, err := ratFromNumeric(r.Quantity)
		if err != nil {
			return "", nil, "", err
		}
		net, err := ratFromNumeric(r.Net)
		if err != nil {
			return "", nil, "", err
		}
		already[r.LineID] = credited{qty, net}
	}
	for _, l := range lines {
		if l.CreditsLineID == nil {
			continue
		}
		o, ok := originals[*l.CreditsLineID]
		if !ok {
			continue
		}
		qty, net, err := numericPair(l.Quantity, l.LineNet)
		if err != nil {
			return "", nil, "", err
		}
		maxQty, maxNet, err := numericPair(o.Quantity, o.LineNet)
		if err != nil {
			return "", nil, "", err
		}
		if a, ok := already[o.ID]; ok {
			qty.Add(qty, a.quantity)
			net.Add(net, a.net)
		}
		if qty.Cmp(maxQty) > 0 || net.Cmp(maxNet) > 0 {
			position := l.Position
			return codeCreditExceedsLine, &position, fmt.Sprintf(
				"Line %d credits more of the original's line %d than it had, counting the credit notes already issued.", l.Position, o.Position), nil
		}
	}
	_, left, err := uncredited(ctx, q, original)
	if err != nil {
		return "", nil, "", err
	}
	if gross.Cmp(left) > 0 {
		return codeCreditExceedsInvoice, nil, fmt.Sprintf(
			"This credit note is %s, and the invoice has %s left to credit.", gross.FloatString(2), left.FloatString(2)), nil
	}
	return "", nil, "", nil
}

// numericPair reads two numeric columns.
func numericPair(a, b pgtype.Numeric) (*big.Rat, *big.Rat, error) {
	x, err := ratFromNumeric(a)
	if err != nil {
		return nil, nil, err
	}
	y, err := ratFromNumeric(b)
	if err != nil {
		return nil, nil, err
	}
	return x, y, nil
}

// creditIssueChecks are the checks only a credit note keeps (D6 step 5): the
// original locked FOR UPDATE — the last lock of the issue's order — and both
// caps under it. Its lines are taxed with their original lines' snapshots.
func creditIssueChecks(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice, lines []store.InvoicesLine) (issuePlan, *gen.InvoicesConflictProblem, error) {
	original, err := txq.LockInvoice(ctx, *locked.CreditsInvoiceID)
	if err != nil {
		return issuePlan{}, nil, fmt.Errorf("invoices: lock the original %d: %w", *locked.CreditsInvoiceID, err)
	}
	originals, err := originalLines(ctx, txq, original.ID)
	if err != nil {
		return issuePlan{}, nil, err
	}
	plan := issuePlan{}
	taxed := make([]taxedLine, 0, len(lines))
	for _, l := range lines {
		o, ok := originals[derefID(l.CreditsLineID)]
		if !ok {
			return issuePlan{}, nil, fmt.Errorf("invoices: credit note %d's line %d credits no line of %d", locked.ID, l.ID, original.ID)
		}
		t, err := snapshotOf(o)
		if err != nil {
			return issuePlan{}, nil, err
		}
		if t.net, err = ratFromNumeric(l.LineNet); err != nil {
			return issuePlan{}, nil, err
		}
		plan.lines = append(plan.lines, issuedLine{id: l.ID, position: l.Position, taxed: t})
		taxed = append(taxed, t)
	}
	exchangeRate, err := ratFromNumeric(locked.ExchangeRate)
	if err != nil {
		return issuePlan{}, nil, err
	}
	_, totals, _ := summarize(taxed, exchangeRate)
	code, position, detail, err := creditCaps(ctx, txq, original, lines, totals.gross)
	if err != nil {
		return issuePlan{}, nil, err
	}
	if code != "" {
		r := cannotIssue(code, detail)
		r.LinePosition = position
		return issuePlan{}, r, nil
	}
	return plan, nil, nil
}

func derefID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// putCreditDraft is PUT /invoices/{id} for a credit-note draft (D8): only what
// a correction may change — lines removed, a quantity or a unit price
// lowered, a description, the note and the internal note edited. Everything
// else it copied from its original stays, and each attempt to change it is a
// 400 on the field. The caps only warn here; the issue decides them.
func (s *server) putCreditDraft(ctx context.Context, q *store.Queries, current store.InvoicesInvoice, body gen.InvoicesInvoiceRequest) (gen.PutInvoicesByIdResponseObject, error) {
	in, errs := parseDraft(body, current.Currency)
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }
	if in.customerID != current.CustomerID {
		add("customerId", "A credit note's customer is its original's")
	}
	if in.paymentTermsDays != nil {
		add("paymentTermsDays", "A credit note has no payment terms")
	}
	if !sameDate(in.deliveryDate, current.DeliveryDate) || !sameDate(in.deliveryFrom, current.DeliveryFrom) || !sameDate(in.deliveryTo, current.DeliveryTo) {
		add("deliveryDate", "A credit note keeps its original's delivery")
	}
	line1, line2, postal, city, country := addressColumns(in.address)
	if !sameText(line1, current.DeliveryAddressLine1) || !sameText(line2, current.DeliveryAddressLine2) ||
		!sameText(postal, current.DeliveryPostalCode) || !sameText(city, current.DeliveryCity) || !sameText(country, current.DeliveryCountry) {
		add("deliveryAddress", "A credit note keeps its original's place of delivery")
	}
	yourReference := ""
	if in.yourReference != nil {
		yourReference = *in.yourReference
	}
	for _, ref := range []struct{ field, got, want string }{
		{"yourReference", yourReference, current.YourReference},
		{"ourReference", in.ourReference, current.OurReference},
		{"orderReference", in.orderReference, current.OrderReference},
	} {
		if ref.got != ref.want {
			add(ref.field, "A credit note keeps its original's references")
		}
	}
	originals, err := originalLines(ctx, q, *current.CreditsInvoiceID)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	credits := make([]*int64, 0, len(in.lines))
	for i, l := range in.lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		credits = append(credits, l.creditsLineID)
		if l.creditsLineID == nil {
			add(field("creditsLineId"), "A credit note only credits the original's lines; it adds none")
			continue
		}
		o, ok := originals[*l.creditsLineID]
		switch {
		case !ok:
			add(field("creditsLineId"), "This is not a line of the original")
			continue
		case seen[o.ID]:
			add(field("creditsLineId"), "An original line is credited once per credit note")
			continue
		}
		seen[o.ID] = true
		if l.vatCodeID != o.VatCodeID {
			add(field("vatCodeId"), "A credit note keeps the original line's VAT code")
		}
		if l.unit != o.Unit {
			add(field("unit"), "A credit note keeps the original line's unit")
		}
		if l.quantity == nil || l.unitPrice == nil || l.discount == nil {
			continue
		}
		oQty, oPrice, err := numericPair(o.Quantity, o.UnitPrice)
		if err != nil {
			return nil, err
		}
		oDiscount, err := ratFromNumeric(o.DiscountPercent)
		if err != nil {
			return nil, err
		}
		if l.quantity.Cmp(oQty) > 0 {
			add(field("quantity"), "A credit note may lower a quantity, never raise it")
		}
		if l.unitPrice.Cmp(oPrice) > 0 {
			add(field("unitPrice"), "A credit note may lower a price, never raise it")
		}
		if l.discount.Cmp(oDiscount) < 0 {
			add(field("discountPercent"), "A credit note may not lower a discount")
		}
	}
	if len(errs) > 0 {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	taxed, err := creditTaxedLines(in.lines, credits, originals)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := ratFromNumeric(current.ExchangeRate)
	if err != nil {
		return nil, err
	}
	_, totals, _ := summarize(taxed, exchangeRate)
	saved, refusal, err := s.saveDraft(ctx, current.ID, *body.Revision, in, totals)
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	resp, err := s.invoiceResponse(ctx, q, saved, nil)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesById200JSONResponse(resp), nil
}

func sameDate(a, b pgtype.Date) bool {
	return a.Valid == b.Valid && (!a.Valid || a.Time.Equal(b.Time))
}

func sameText(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// creditLinks are a document's credit links (D4): on an issued invoice, what
// its issued credit notes credit, what it has left, and every credit note,
// drafts included; on a credit note, the invoice it credits. On a
// credit-note draft, the caps as warnings.
func creditLinks(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, resp *gen.InvoicesInvoiceResponse) error {
	if inv.Kind == kindInvoice {
		if inv.Status != statusIssued {
			return nil
		}
		credited, left, err := uncredited(ctx, q, inv)
		if err != nil {
			return err
		}
		resp.CreditedAmount, resp.UncreditedAmount = ptr(floatFromRat(credited, 2)), ptr(floatFromRat(left, 2))
		notes, err := q.CreditNotesOf(ctx, &inv.ID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d's credit notes: %w", inv.ID, err)
		}
		refs := make([]gen.InvoicesCreditNoteRef, 0, len(notes))
		for _, n := range notes {
			gross, err := floatFromNumeric(n.GrossTotal)
			if err != nil {
				return err
			}
			refs = append(refs, gen.InvoicesCreditNoteRef{
				Id: n.ID, Number: n.Number, IssueDate: wireDateOf(n.IssueDate), GrossTotal: gross, Status: n.Status,
			})
		}
		resp.CreditNotes = &refs
		return nil
	}
	original, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
	}
	if original.Number != nil {
		resp.Credits = &gen.InvoicesCreditsRef{Id: original.ID, Number: *original.Number, IssueDate: wireDate(original.IssueDate.Time)}
	}
	if inv.Status != statusDraft {
		return nil
	}
	lines, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return err
	}
	code, _, _, err := creditCaps(ctx, q, original, lines, ratFromFloat(resp.GrossTotal))
	if err != nil {
		return err
	}
	if code != "" {
		resp.Warnings = append(resp.Warnings, code)
	}
	return nil
}
```

**Replace** in `apps/server/internal/invoices/drafts.go`:

```go
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle,
			fieldError("revision", "A replace carries the revision it was read at"))), nil
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
```

**with**:

```go
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle,
			fieldError("revision", "A replace carries the revision it was read at"))), nil
	}
	if current.Kind == kindCreditNote {
		return s.putCreditDraft(ctx, q, current, *req.Body)
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
```

**Replace** in `apps/server/internal/invoices/issue.go`:

```go
		switch locked.Kind {
		case kindInvoice:
			plan, refusal, err = invoiceIssueChecks(ctx, txq, profile, settings, issueDate, lines)
		default:
			return fmt.Errorf("invoices: document %d is a %s, which this module cannot issue", locked.ID, locked.Kind)
		}
```

**with**:

```go
		switch locked.Kind {
		case kindInvoice:
			plan, refusal, err = invoiceIssueChecks(ctx, txq, profile, settings, issueDate, lines)
		case kindCreditNote:
			plan, refusal, err = creditIssueChecks(ctx, txq, locked, lines)
		default:
			return fmt.Errorf("invoices: document %d is a %s, which this module cannot issue", locked.ID, locked.Kind)
		}
```

**Replace** in `apps/server/internal/invoices/responses.go`:

```go
		if issuedLate(inv.IssueDate.Time, deliveryEndOf(inv)) {
			resp.Warnings = append(resp.Warnings, warningIssuedLate)
		}
		return resp, nil
	}

	codes, err := vatCodesOn(ctx, q, pgDate(today))
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	lines, err := storedDraftLines(stored)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	rows, totals, _ := summarize(taxedLines(lines, codes), big.NewRat(1, 1))
	for _, r := range rows {
		resp.VatSummaries = append(resp.VatSummaries, summaryResponse(r))
	}
```

**with**:

```go
		if issuedLate(inv.IssueDate.Time, deliveryEndOf(inv)) {
			resp.Warnings = append(resp.Warnings, warningIssuedLate)
		}
		return resp, creditLinks(ctx, q, inv, &resp)
	}

	lines, err := storedDraftLines(stored)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	var taxed []taxedLine
	if inv.Kind == kindCreditNote {
		// A credit note reverses its original's treatment: its lines are taxed
		// with the original lines' snapshots, never today's rates (D8).
		originals, err := originalLines(ctx, q, *inv.CreditsInvoiceID)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		credits := make([]*int64, 0, len(stored))
		for _, l := range stored {
			credits = append(credits, l.CreditsLineID)
		}
		if taxed, err = creditTaxedLines(lines, credits, originals); err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
	} else {
		codes, err := vatCodesOn(ctx, q, pgDate(today))
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		taxed = taxedLines(lines, codes)
	}
	rows, totals, _ := summarize(taxed, big.NewRat(1, 1))
	for _, r := range rows {
		resp.VatSummaries = append(resp.VatSummaries, summaryResponse(r))
	}
```

**Replace** in `apps/server/internal/invoices/responses.go`:

```go
		allowed = append(allowed, wireDate(d))
	}
	resp.AllowedIssueDates = &allowed
	return resp, nil
}
```

**with**:

```go
		allowed = append(allowed, wireDate(d))
	}
	resp.AllowedIssueDates = &allowed
	return resp, creditLinks(ctx, q, inv, &resp)
}
```

- [ ] **Step 4: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bun run gen:client
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 ./internal/invoices/ ./internal/openapi/
mise exec -- golangci-lint run ./internal/invoices/...
cd ../..
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task6.txt <<'MSG'
feat(invoices): credit notes in the same series, capped per line and on the headline

The correction (invoices foundation design D8, § 5-2-7): an issued invoice
is copied into a credit-note draft — the customer, currency and rate,
delivery, references and the buyer snapshot, with no directory read, and
every line pointing at the line it credits. The draft may only remove
lines, lower a quantity or a price, and edit descriptions and notes; its
totals are the original lines' own rates. At issue the original is locked
after the counter and the caps decide — per original line and against what
the invoice has left — while every gate meant for new invoices is skipped.
The documents link both ways.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/credits.go' 'apps/server/internal/invoices/credits_test.go' 'apps/server/internal/invoices/drafts.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/issue.go' 'apps/server/internal/invoices/queries/credits.sql' 'apps/server/internal/invoices/responses.go' 'apps/server/internal/invoices/store/credits.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git commit -F /tmp/claude-1000/msg-invoices-task6.txt -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/credits.go' 'apps/server/internal/invoices/credits_test.go' 'apps/server/internal/invoices/drafts.go' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/issue.go' 'apps/server/internal/invoices/queries/credits.sql' 'apps/server/internal/invoices/responses.go' 'apps/server/internal/invoices/store/credits.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 7: The two customer slots: the merge holder and the person's data (D10)

`CustomerReferences.RepointCustomer` moves every document of the absorbed customer inside the merge's transaction — drafts' revisions advance, issued documents change only their `customer_id` (the one column the trigger allows) and keep their buyer snapshot. `CustomerPersonalData` exports the person's documents and drafts, internal notes included, and on anonymisation deletes the drafts and keeps the issued documents under bokføringsloven § 13. `contracts.ErasedData` has no reason field and does not grow one (the spec's ruling): the reason is written in `docs/invoices.md` and `docs/customers.md` (Task 9). Both constructors need nothing a disabled module's Deps lacks; `module.Compose` and `module.Workers` collect them from `Module()`.

**Files:**
- Create: `apps/server/internal/invoices/customer_slots.go`, `apps/server/internal/invoices/customer_slots_test.go`, `apps/server/internal/invoices/queries/customers.sql`
- Modify: `apps/server/internal/invoices/module.go`
- Generated (commit them; never edit by hand): `apps/server/internal/invoices/store/customers.sql.go`
- Read first (do not change): `apps/server/internal/contracts/{references.go,personal_data.go}`, `apps/server/internal/projects/{customer_references.go,customer_personal_data.go,customer_references_test.go,customer_personal_data_test.go}`, `internal/module/compose.go:468-498`

**Interfaces:**
- Produces Go: `Module().CustomerReferences` → `*customerReferenceHolder` (`RepointCustomer`, kind `invoices.invoices`); `Module().CustomerPersonalData` → `customerPersonalData` (`ExportCustomerData` → `{documents, drafts}` or nil; `EraseCustomerData` → `invoices.drafts` n, `invoices.documents` 0).
- Produces SQL: `RepointCustomer :execrows`, `CustomerDocuments`, `LinesOf`, `DeleteCustomerDrafts :execrows`.

- [ ] **Step 1: The tests, with the module disabled and the caller's transaction rolled back**

**Create** `apps/server/internal/invoices/customer_slots_test.go`:

```go
package invoices_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// inTx runs fn in a transaction the test owns — the merge's or the
// anonymisation's position — and commits it or rolls it back as told.
func inTx(t *testing.T, h *harness, commit bool, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}

// disabledDeps is what Compose hands a module MODULES leaves out: no
// directory, no contract, no object store — only the platform.
func disabledDeps(h *harness) module.Deps {
	return module.Deps{Pool: h.Pool(), Clock: h.Now, Config: h.Deps().Config}
}

// The merge holder (D10) moves every document of the absorbed customer —
// drafts and issued — leaves the snapshots, reports invoices.invoices, and
// works with the module disabled; a rolled-back merge moved nothing.
func TestCustomerReferences_RepointMovesDraftsAndIssuedDocuments(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	issuedDoc := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	draft := createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25)))
	other := createDraft(t, h, draftBody(customerPerson, line("C", 1, 100, vat25)))
	holder := invoices.Module().CustomerReferences(disabledDeps(h))
	h.Advance(time.Hour)

	var moved []contracts.RepointedReferences
	inTx(t, h, false, func(tx pgx.Tx) {
		var err error
		if moved, err = holder.RepointCustomer(context.Background(), tx, customerAcme, customerNoTerms); err != nil {
			t.Fatalf("RepointCustomer: %v", err)
		}
	})
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1`, customerAcme); n != 2 || moved[0].Count != 2 {
		t.Fatalf("after a rollback = %d still Acme's, reported %+v; want both there and 2 reported", n, moved)
	}

	inTx(t, h, true, func(tx pgx.Tx) {
		var err error
		if moved, err = holder.RepointCustomer(context.Background(), tx, customerAcme, customerNoTerms); err != nil {
			t.Fatalf("RepointCustomer: %v", err)
		}
	})
	if want := []contracts.RepointedReferences{{Kind: "invoices.invoices", Count: 2}}; !slices.Equal(moved, want) {
		t.Errorf("moved = %+v, want %+v", moved, want)
	}
	after := getInvoice(t, h, issuedDoc.ID)
	if after.CustomerID != customerNoTerms || after.Buyer.Name != issuedDoc.Buyer.Name || after.Revision != issuedDoc.Revision {
		t.Errorf("the issued document = customer %d buyer %q revision %d; want moved, its snapshot and revision kept",
			after.CustomerID, after.Buyer.Name, after.Revision)
	}
	if d := getInvoice(t, h, draft.ID); d.CustomerID != customerNoTerms || d.Revision != draft.Revision+1 {
		t.Errorf("the draft = customer %d revision %d; want moved and its revision on", d.CustomerID, d.Revision)
	}
	if o := getInvoice(t, h, other.ID); o.CustomerID != customerPerson {
		t.Error("another customer's draft moved")
	}

	inTx(t, h, true, func(tx pgx.Tx) {
		same, err := holder.RepointCustomer(context.Background(), tx, customerPerson, customerPerson)
		if err != nil || !slices.Equal(same, []contracts.RepointedReferences{{Kind: "invoices.invoices", Count: 0}}) {
			t.Errorf("from == into = %+v, %v; want a zero", same, err)
		}
	})
	if o := getInvoice(t, h, other.ID); o.Revision != other.Revision {
		t.Errorf("from == into wrote the draft: revision %d", o.Revision)
	}
}

// The export (D10): nil when nothing is held; every issued document and draft
// otherwise, the internal notes included.
func TestCustomerPersonalData_Export(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	if none, err := data.ExportCustomerData(context.Background(), customerPerson); err != nil || none != nil {
		t.Fatalf("a customer with nothing = %v, %v; want nil", none, err)
	}
	body := draftBody(customerPerson, line("Konsultasjon", 1.5, 800, vat25))
	body["internalNote"] = "Ringte to ganger"
	issued(t, h, createDraft(t, h, body).ID)
	body["internalNote"] = "Utkast til neste måned"
	createDraft(t, h, body)

	section, err := data.ExportCustomerData(context.Background(), customerPerson)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	raw, _ := json.Marshal(section)
	for _, want := range []string{
		`"documents":[{"number":1,"kind":"invoice","issueDate":"2026-09-12"`, `"grossTotal":"1500.00"`,
		`"buyerName":"Kari Nordmann"`, `"buyerAddress":["Hjemveien 5","5003","Bergen","NO"]`, `"internalNote":"Ringte to ganger"`,
		`"vatRatePercent":"25.00"`, `"drafts":[{"kind":"invoice"`, `"internalNote":"Utkast til neste måned"`, `"quantity":"1.500"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("export %s has no %s", raw, want)
		}
	}
}

// The erase (D10): the person's drafts deleted and counted as
// invoices.drafts, the issued documents kept and reported at 0, a second run
// zeros; a rolled-back anonymisation keeps the drafts.
func TestCustomerPersonalData_EraseDeletesDraftsAndKeepsDocuments(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	doc := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID)
	createDraft(t, h, draftBody(customerPerson, line("B", 1, 100, vat25)))
	createDraft(t, h, draftBody(customerPerson, line("C", 1, 100, vat25)))
	credit := creditDraft(t, h, doc.ID)
	createDraft(t, h, draftBody(customerAcme, line("D", 1, 100, vat25)))
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	erase := func(commit bool) []contracts.ErasedData {
		var erased []contracts.ErasedData
		inTx(t, h, commit, func(tx pgx.Tx) {
			var err error
			if erased, err = data.EraseCustomerData(context.Background(), tx, customerPerson); err != nil {
				t.Fatalf("EraseCustomerData: %v", err)
			}
		})
		return erased
	}

	erase(false)
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1 AND status = 'draft'`, customerPerson); n != 3 {
		t.Fatalf("drafts after a rolled-back erase = %d, want 3", n)
	}
	if got, want := erase(true), []contracts.ErasedData{{Kind: "invoices.drafts", Count: 3}, {Kind: "invoices.documents", Count: 0}}; !slices.Equal(got, want) {
		t.Errorf("erased = %+v, want %+v", got, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE id = $1`, credit.ID); n != 0 {
		t.Error("a credit-note draft survived: a draft of either kind is erased")
	}
	if kept := getInvoice(t, h, doc.ID); kept.Buyer.Name != "Kari Nordmann" {
		t.Errorf("the issued document = %+v, want kept with its snapshot", kept.Buyer)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1`, customerAcme); n != 1 {
		t.Error("another customer's draft was erased")
	}
	if got, want := erase(true), []contracts.ErasedData{{Kind: "invoices.drafts", Count: 0}, {Kind: "invoices.documents", Count: 0}}; !slices.Equal(got, want) {
		t.Errorf("a second erase = %+v, want zeros", got)
	}
}
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable' && mise exec -- go test -count=1 -run 'TestCustomer' ./internal/invoices/ ; cd ../..   # FAIL: a nil CustomerReferences
```

- [ ] **Step 2: The queries**

**Create** `apps/server/internal/invoices/queries/customers.sql`:

```sql
-- name: RepointCustomer :execrows
-- RepointCustomer moves every document of one customer to another (D10), the
-- merge holder's one write, inside the merge's transaction. A draft's revision
-- and updated_at move as any change to it does; an issued document's do not —
-- the trigger allows exactly the customer_id to change on it (D9), and its
-- buyer snapshot, which is what it printed, is untouched.
UPDATE invoices.invoices SET
    customer_id = @into_customer_id,
    revision = CASE WHEN status = 'draft' THEN revision + 1 ELSE revision END,
    updated_at = CASE WHEN status = 'draft' THEN @now::timestamptz ELSE updated_at END
WHERE customer_id = @from_customer_id;

-- name: CustomerDocuments :many
-- CustomerDocuments is every document of one customer, issued ones by number
-- first and then the drafts: a private person's export (D10).
SELECT * FROM invoices.invoices
WHERE customer_id = @customer_id
ORDER BY status DESC, number, id;

-- name: LinesOf :many
-- LinesOf is the lines of several documents at once, in their documents'
-- order.
SELECT * FROM invoices.lines WHERE invoice_id = ANY(@invoice_ids::bigint[]) ORDER BY invoice_id, position;

-- name: DeleteCustomerDrafts :execrows
-- DeleteCustomerDrafts erases a person's drafts on anonymisation (D10): a
-- draft is not a salgsdokument, so nothing keeps it. Issued documents stay —
-- bokføringsloven § 13 keeps them five years after the financial year.
DELETE FROM invoices.invoices WHERE customer_id = @customer_id AND status = 'draft';
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 3: The slots, declared on the module**

**Create** `apps/server/internal/invoices/customer_slots.go`:

```go
package invoices

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the two many-provider slots every module holding customer ids
// implements (D10). Both run whether or not the module is enabled — every
// schema is migrated whatever MODULES says — so their constructors need
// nothing a disabled module's Deps lacks, and neither reads a contract.

// The kinds this module reports, "<module>.<what>" in the API's camelCase.
const (
	kindInvoicesInvoices  = "invoices.invoices"
	kindInvoicesDrafts    = "invoices.drafts"
	kindInvoicesDocuments = "invoices.documents"
)

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder:
// when two customers are merged, every document of the absorbed one — draft
// and issued — names the survivor from then on. An issued document keeps its
// buyer snapshot: the id is not printed, the snapshot is.
type customerReferenceHolder struct {
	clock func() time.Time
}

var _ contracts.CustomerReferenceHolder = (*customerReferenceHolder)(nil)

// newCustomerReferenceHolder is Module's CustomerReferences. It takes the
// clock, as projects' does, for a draft's updated_at.
func newCustomerReferenceHolder(d module.Deps) contracts.CustomerReferenceHolder {
	return &customerReferenceHolder{clock: d.Clock}
}

// RepointCustomer moves every document of from to into, inside the caller's
// transaction. from == into writes nothing and reports zero.
func (h *customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	if from == into {
		return []contracts.RepointedReferences{{Kind: kindInvoicesInvoices, Count: 0}}, nil
	}
	n, err := store.New(tx).RepointCustomer(ctx, store.RepointCustomerParams{
		FromCustomerID: from, IntoCustomerID: into, Now: h.clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: re-point customer %d's documents to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: kindInvoicesInvoices, Count: n}}, nil
}

// customerPersonalData is this module's contracts.CustomerPersonalData: what
// was invoiced to a private person, handed over, and on anonymisation the
// drafts erased while the issued documents stay.
type customerPersonalData struct {
	pool *pgxpool.Pool
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool}
}

type invoicesSection struct {
	Documents []exportedDocument `json:"documents"`
	Drafts    []exportedDocument `json:"drafts"`
}

type exportedDocument struct {
	Number         *int64         `json:"number,omitempty"`
	Kind           string         `json:"kind"`
	IssueDate      *string        `json:"issueDate,omitempty"`
	DeliveryDate   *string        `json:"deliveryDate,omitempty"`
	DeliveryFrom   *string        `json:"deliveryFrom,omitempty"`
	DeliveryTo     *string        `json:"deliveryTo,omitempty"`
	DueDate        *string        `json:"dueDate,omitempty"`
	Currency       string         `json:"currency"`
	NetTotal       string         `json:"netTotal"`
	VatTotal       string         `json:"vatTotal"`
	GrossTotal     string         `json:"grossTotal"`
	BuyerName      *string        `json:"buyerName,omitempty"`
	BuyerAddress   []string       `json:"buyerAddress,omitempty"`
	YourReference  string         `json:"yourReference,omitempty"`
	OurReference   string         `json:"ourReference,omitempty"`
	OrderReference string         `json:"orderReference,omitempty"`
	Note           string         `json:"note,omitempty"`
	InternalNote   string         `json:"internalNote,omitempty"`
	Lines          []exportedLine `json:"lines"`
}

type exportedLine struct {
	Description     string  `json:"description"`
	Quantity        string  `json:"quantity"`
	Unit            string  `json:"unit,omitempty"`
	UnitPrice       string  `json:"unitPrice"`
	DiscountPercent string  `json:"discountPercent"`
	VatRatePercent  *string `json:"vatRatePercent,omitempty"`
	LineNet         string  `json:"lineNet"`
}

// dateText is a date column as the API writes one, or nil.
func dateText(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	return ptr(d.Time.Format(time.DateOnly))
}

// decimalOf is a numeric column as exact decimal text — an export is read by
// people and programs alike, and no float rounds its money.
func decimalOf(n pgtype.Numeric, places int) (string, error) {
	r, err := ratFromNumeric(n)
	if err != nil {
		return "", err
	}
	return r.FloatString(places), nil
}

// ExportCustomerData answers nil for a customer with no document. Otherwise
// every issued document and every draft, with the internal note: the
// customers export treats staff-written notes as data held about the person.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	q := store.New(p.pool)
	docs, err := q.CustomerDocuments(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's documents: %w", customerID, err)
	}
	if len(docs) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	lines, err := q.LinesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's lines: %w", customerID, err)
	}
	byDoc := map[int64][]store.InvoicesLine{}
	for _, l := range lines {
		byDoc[l.InvoiceID] = append(byDoc[l.InvoiceID], l)
	}
	section := invoicesSection{Documents: []exportedDocument{}, Drafts: []exportedDocument{}}
	for _, d := range docs {
		e := exportedDocument{
			Number: d.Number, Kind: d.Kind, IssueDate: dateText(d.IssueDate), DeliveryDate: dateText(d.DeliveryDate),
			DeliveryFrom: dateText(d.DeliveryFrom), DeliveryTo: dateText(d.DeliveryTo), DueDate: dateText(d.DueDate),
			Currency: d.Currency, BuyerName: d.BuyerName, YourReference: d.YourReference, OurReference: d.OurReference,
			OrderReference: d.OrderReference, Note: d.Note, InternalNote: d.InternalNote, Lines: []exportedLine{},
		}
		for _, part := range []*string{d.BuyerAddressLine1, d.BuyerAddressLine2, d.BuyerPostalCode, d.BuyerCity, d.BuyerCountry} {
			if part != nil && *part != "" {
				e.BuyerAddress = append(e.BuyerAddress, *part)
			}
		}
		for _, c := range []struct {
			dst *string
			n   pgtype.Numeric
		}{{&e.NetTotal, d.NetTotal}, {&e.VatTotal, d.VatTotal}, {&e.GrossTotal, d.GrossTotal}} {
			if *c.dst, err = decimalOf(c.n, 2); err != nil {
				return nil, err
			}
		}
		for _, l := range byDoc[d.ID] {
			line := exportedLine{Description: l.Description, Unit: l.Unit}
			for _, c := range []struct {
				dst    *string
				n      pgtype.Numeric
				places int
			}{{&line.Quantity, l.Quantity, 3}, {&line.UnitPrice, l.UnitPrice, 4}, {&line.DiscountPercent, l.DiscountPercent, 2}, {&line.LineNet, l.LineNet, 2}} {
				if *c.dst, err = decimalOf(c.n, c.places); err != nil {
					return nil, err
				}
			}
			if l.VatRatePercent.Valid {
				rate, err := decimalOf(l.VatRatePercent, 2)
				if err != nil {
					return nil, err
				}
				line.VatRatePercent = &rate
			}
			e.Lines = append(e.Lines, line)
		}
		if d.Status == statusIssued {
			section.Documents = append(section.Documents, e)
		} else {
			section.Drafts = append(section.Drafts, e)
		}
	}
	return section, nil
}

// EraseCustomerData deletes the person's drafts and keeps their issued
// documents. A draft is not a salgsdokument, so it has no retention basis and
// GDPR art. 17 applies; an issued document and its buyer snapshot are kept
// under bokføringsloven § 13 — five years after the end of the financial year —
// which is why invoices.documents reports 0. contracts.ErasedData carries no
// reason; docs/invoices.md and the anonymisation table in docs/customers.md
// say it.
func (customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	n, err := store.New(tx).DeleteCustomerDrafts(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: erase customer %d's drafts: %w", customerID, err)
	}
	return []contracts.ErasedData{
		{Kind: kindInvoicesDrafts, Count: n},
		{Kind: kindInvoicesDocuments, Count: 0},
	}, nil
}
```

**Replace** in `apps/server/internal/invoices/module.go`:

```go
}

// Module is invoices as a platform module: its contract mounted under
// /api/v1/invoices/ and its four permissions in the composed catalog.
func Module() module.Module {
	return module.Module{
		Name:        "invoices",
		Permissions: permissions,
		Mount:       mount,
	}
}

```

**with**:

```go
}

// Module is invoices as a platform module: its contract mounted under
// /api/v1/invoices/, its four permissions in the composed catalog, and the two
// slots every module holding customer ids fills (customer_slots.go): the merge
// holder and the personal-data provider.
func Module() module.Module {
	return module.Module{
		Name:                 "invoices",
		Permissions:          permissions,
		Mount:                mount,
		CustomerReferences:   newCustomerReferenceHolder,
		CustomerPersonalData: newCustomerPersonalData,
	}
}

```

- [ ] **Step 4: Verify and commit**

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 ./internal/invoices/ ./internal/module/ ./internal/integration/ ./internal/customers/
mise exec -- golangci-lint run ./internal/invoices/...
cd ../..
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task7.txt <<'MSG'
feat(invoices): the merge holder and the person's data

Both many-provider customer slots (invoices foundation design D10). A merge
re-points every document of the absorbed customer, drafts and issued alike;
an issued document keeps its buyer snapshot and its revision, and the
immutability trigger allows exactly its customer_id to change. A person's
export holds every issued document and draft with its lines and notes;
their anonymisation deletes the drafts (invoices.drafts) and keeps the
issued documents under bokføringsloven § 13 (invoices.documents, 0).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/server/internal/invoices/customer_slots.go' 'apps/server/internal/invoices/customer_slots_test.go' 'apps/server/internal/invoices/module.go' 'apps/server/internal/invoices/queries/customers.sql' 'apps/server/internal/invoices/store/customers.sql.go'
git commit -F /tmp/claude-1000/msg-invoices-task7.txt -- 'apps/server/internal/invoices/customer_slots.go' 'apps/server/internal/invoices/customer_slots_test.go' 'apps/server/internal/invoices/module.go' 'apps/server/internal/invoices/queries/customers.sql' 'apps/server/internal/invoices/store/customers.sql.go'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 8: The journal and its gap check (D11)

`GET /invoices/journal?from&to&page&pageSize`: the issued documents with an issue date in the range in number order, credit notes signed negative in every amount, totals per SAF-T code, category and rate over the whole range, the gap check from the number before the range's first (never below the series start) to its last, at most 1000 listed, and the counter's last number — all from one repeatable-read, read-only snapshot.

**Files:**
- Create: `apps/server/internal/invoices/journal.go`, `apps/server/internal/invoices/journal_test.go`, `apps/server/internal/invoices/queries/journal.sql`
- Modify: `openapi/invoices.yaml`
- Generated (commit them; never edit by hand): `apps/invoices/frontend/src/api-schema.d.ts`, `apps/server/internal/invoices/gen/api.gen.go`, `apps/server/internal/invoices/store/journal.sql.go`, `apps/server/internal/openapi/specs/invoices.yaml`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/invoices/{list.go,queries/counters.sql}`

**Interfaces:**
- Produces wire: `GET /invoices/journal` → `InvoicesJournalResponse {data[InvoicesJournalRow], pagination, totals{byCode[InvoicesJournalCode], netTotal, vatTotal, grossTotal}, gaps[], gapsTruncated, seriesStart, counterLast?}`.
- Produces SQL: `JournalPage`, `JournalCount`, `JournalSummaries`, `JournalTotalsByCode`, `JournalTotals`, `JournalGaps`.

- [ ] **Step 1: The tests: order, signs, whole-range totals, the gaps**

**Create** `apps/server/internal/invoices/journal_test.go`:

```go
package invoices_test

import (
	"net/http"
	"slices"
	"testing"
)

type journalJSON struct {
	Data []struct {
		Number        int64   `json:"number"`
		Kind          string  `json:"kind"`
		IssueDate     string  `json:"issueDate"`
		BuyerName     *string `json:"buyerName"`
		NetTotal      float64 `json:"netTotal"`
		VatTotal      float64 `json:"vatTotal"`
		GrossTotal    float64 `json:"grossTotal"`
		CreditsNumber *int64  `json:"creditsNumber"`
		VatSummaries  []struct {
			SafTCode      string  `json:"safTCode"`
			Category      string  `json:"category"`
			RatePercent   float64 `json:"ratePercent"`
			TaxableAmount float64 `json:"taxableAmount"`
			VatAmount     float64 `json:"vatAmount"`
		} `json:"vatSummaries"`
	} `json:"data"`
	Pagination struct {
		TotalCount int32 `json:"totalCount"`
	} `json:"pagination"`
	Totals struct {
		ByCode []struct {
			SafTCode      string  `json:"safTCode"`
			Category      string  `json:"category"`
			RatePercent   float64 `json:"ratePercent"`
			TaxableAmount float64 `json:"taxableAmount"`
			VatAmount     float64 `json:"vatAmount"`
		} `json:"byCode"`
		NetTotal   float64 `json:"netTotal"`
		VatTotal   float64 `json:"vatTotal"`
		GrossTotal float64 `json:"grossTotal"`
	} `json:"totals"`
	Gaps          []int64 `json:"gaps"`
	GapsTruncated bool    `json:"gapsTruncated"`
	SeriesStart   int64   `json:"seriesStart"`
	CounterLast   *int64  `json:"counterLast"`
}

const journalPath = "/api/v1/invoices/journal"

func journal(t *testing.T, h *harness, query string) journalJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, journalPath+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /journal%s = %d %s", query, res.Status, res.Body)
	}
	var j journalJSON
	res.JSON(&j)
	return j
}

// The journal lists the range's documents in number order, credit notes
// signed negative in the rows and the totals, the totals over the whole range
// and not the page, and no gaps in the normal case (D11).
func TestJournal_TheRangeInNumberOrder(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	first := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 10, 100, vat25))).ID)
	issued(t, h, createDraft(t, h, draftBody(customerPerson, line("B", 1, 200, vatZero))).ID)
	c := creditDraft(t, h, first.ID)
	partial := creditLine(c.Lines[0])
	partial["quantity"] = 4
	issued(t, h, saveCredit(t, h, c, creditBody(c, partial)).ID)

	j := journal(t, h, "?from=2026-09-01&to=2026-09-30")
	var numbers []int64
	for _, d := range j.Data {
		numbers = append(numbers, d.Number)
	}
	if !slices.Equal(numbers, []int64{1, 2, 3}) || j.Pagination.TotalCount != 3 {
		t.Fatalf("numbers = %v of %d, want 1, 2, 3", numbers, j.Pagination.TotalCount)
	}
	credit := j.Data[2]
	if credit.Kind != "credit_note" || credit.NetTotal != -400 || credit.VatTotal != -100 || credit.GrossTotal != -500 ||
		credit.CreditsNumber == nil || *credit.CreditsNumber != 1 || credit.VatSummaries[0].TaxableAmount != -400 {
		t.Errorf("the credit note's row = %+v, want it signed negative and naming invoice 1", credit)
	}
	if j.Totals.NetTotal != 800 || j.Totals.VatTotal != 150 || j.Totals.GrossTotal != 950 {
		t.Errorf("totals = %+v, want 1000 + 200 − 400 net, 250 − 100 VAT", j.Totals)
	}
	if len(j.Totals.ByCode) != 2 || j.Totals.ByCode[0].SafTCode != "3" || j.Totals.ByCode[0].TaxableAmount != 600 || j.Totals.ByCode[0].VatAmount != 150 ||
		j.Totals.ByCode[1].Category != "Z" || j.Totals.ByCode[1].TaxableAmount != 200 {
		t.Errorf("by code = %+v", j.Totals.ByCode)
	}
	if len(j.Gaps) != 0 || j.GapsTruncated || j.SeriesStart != 1 || j.CounterLast == nil || *j.CounterLast != 3 {
		t.Errorf("gaps %v truncated %v start %d counter %v; want none, 1 and 3", j.Gaps, j.GapsTruncated, j.SeriesStart, j.CounterLast)
	}

	paged := journal(t, h, "?from=2026-09-01&to=2026-09-30&pageSize=1&page=2")
	if len(paged.Data) != 1 || paged.Data[0].Number != 2 || paged.Totals.NetTotal != 800 {
		t.Errorf("page 2 of 1 = %+v, want document 2 and the whole range's totals", paged.Data)
	}
	for _, bad := range []string{"?from=2026-09-30&to=2026-09-01", "?to=2026-09-01", "?from=2026-09-01&to=2026-09-30&pageSize=101"} {
		if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, journalPath+bad, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET /journal%s = %d, want 400", bad, res.Status)
		}
	}
}

// A gap planted behind the handlers' backs is reported, at the range's first
// number against the one before it too; a number below the series start never
// is; at most 1000 are listed.
func TestJournal_TheGapCheck(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssuedDocument(t, h, 1, "2026-08-20")
	plantIssuedDocument(t, h, 2, "2026-08-25")
	plantIssuedDocument(t, h, 5, "2026-09-02")
	plantIssuedDocument(t, h, 6, "2026-09-03")
	plantIssuedDocument(t, h, 9, "2026-09-10")

	// September's first is 5, checked against 4 before it; 3 is the range
	// before's to check. Inside, 7 and 8 are missing.
	if j := journal(t, h, "?from=2026-09-01&to=2026-09-30"); !slices.Equal(j.Gaps, []int64{4, 7, 8}) {
		t.Errorf("September's gaps = %v, want 4 before its first, 7 and 8 inside", j.Gaps)
	}
	if j := journal(t, h, "?from=2026-08-01&to=2026-08-31"); len(j.Gaps) != 0 {
		t.Errorf("August's gaps = %v, want none", j.Gaps)
	}
	if j := journal(t, h, "?from=2026-10-01&to=2026-10-31"); len(j.Gaps) != 0 || len(j.Data) != 0 {
		t.Errorf("an empty range = %+v", j)
	}
	plantIssuedDocument(t, h, 1012, "2026-11-02")
	if j := journal(t, h, "?from=2026-09-01&to=2026-11-30"); len(j.Gaps) != 1000 || !j.GapsTruncated || j.Gaps[0] != 4 {
		t.Errorf("a long gap = %d listed, truncated %v, first %v; want 1000, true, 4", len(j.Gaps), j.GapsTruncated, j.Gaps[0])
	}
}

// Numbers below the series start are never gaps, and counterLast is the
// counter's even when it is ahead of every document.
func TestJournal_TheSeriesStartAndTheCounter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["seriesStart"] = 100
	saveSeller(t, h, body)
	plantIssuedDocument(t, h, 100, "2026-09-02")
	plantIssuedDocument(t, h, 101, "2026-09-03")
	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 103)`)

	j := journal(t, h, "?from=2026-09-01&to=2026-09-30")
	if len(j.Gaps) != 0 || j.SeriesStart != 100 {
		t.Errorf("gaps = %v with the series at 100, want none", j.Gaps)
	}
	if j.CounterLast == nil || *j.CounterLast != 102 {
		t.Errorf("counterLast = %v, want 102 — ahead of the highest document, 101", j.CounterLast)
	}
}
```

- [ ] **Step 2: The contract and the queries**

**Insert** into `openapi/invoices.yaml`, immediately before the line `info:`:

```yaml
        InvoicesJournalCode:
            description: One (SAF-T code, category, rate) of the journal's VAT, credit notes signed negative.
            properties:
                category:
                    type: string
                ratePercent:
                    format: double
                    type: number
                safTCode:
                    type: string
                taxableAmount:
                    format: double
                    type: number
                vatAmount:
                    format: double
                    type: number
            required:
                - safTCode
                - category
                - ratePercent
                - taxableAmount
                - vatAmount
            type: object
        InvoicesJournalResponse:
            description: 'The invoice journal (D11) over issue dates from-to: the issued documents in number order, a page at a time; the totals over the whole range, per SAF-T code, category and rate, credit notes signed negative; and the gap check — the numbers from the one before the range''s first (never below the series start) to its last that no issued document holds, at most 1000 listed.'
            properties:
                counterLast:
                    description: The counter's last allocated number; absent when nothing was ever issued. When it is not the highest issued number, a number was allocated without a document.
                    format: int64
                    type: integer
                data:
                    items:
                        $ref: '#/components/schemas/InvoicesJournalRow'
                    type: array
                gaps:
                    items:
                        format: int64
                        type: integer
                    type: array
                gapsTruncated:
                    type: boolean
                pagination:
                    $ref: common.yaml#/components/schemas/PaginationMetadata
                seriesStart:
                    format: int64
                    type: integer
                totals:
                    $ref: '#/components/schemas/InvoicesJournalTotals'
            required:
                - data
                - pagination
                - totals
                - gaps
                - gapsTruncated
                - seriesStart
            type: object
        InvoicesJournalRow:
            description: One issued document in the journal, credit notes signed negative in every amount.
            properties:
                buyerCustomerNumber:
                    format: int64
                    type: integer
                buyerName:
                    type: string
                buyerOrganisationNumber:
                    type: string
                creditsNumber:
                    description: On a credit note, the number of the invoice it credits.
                    format: int64
                    type: integer
                currency:
                    type: string
                deliveryDate:
                    format: date
                    type: string
                deliveryFrom:
                    format: date
                    type: string
                deliveryTo:
                    format: date
                    type: string
                dueDate:
                    format: date
                    type: string
                grossTotal:
                    format: double
                    type: number
                id:
                    format: int64
                    type: integer
                issueDate:
                    format: date
                    type: string
                kind:
                    type: string
                netTotal:
                    format: double
                    type: number
                number:
                    format: int64
                    type: integer
                vatSummaries:
                    items:
                        $ref: '#/components/schemas/InvoicesJournalCode'
                    type: array
                vatTotal:
                    format: double
                    type: number
            required:
                - id
                - number
                - kind
                - issueDate
                - currency
                - netTotal
                - vatTotal
                - grossTotal
                - vatSummaries
            type: object
        InvoicesJournalTotals:
            description: The journal's totals over the whole range, not the page, credit notes signed negative.
            properties:
                byCode:
                    items:
                        $ref: '#/components/schemas/InvoicesJournalCode'
                    type: array
                grossTotal:
                    format: double
                    type: number
                netTotal:
                    format: double
                    type: number
                vatTotal:
                    format: double
                    type: number
            required:
                - byCode
                - netTotal
                - vatTotal
                - grossTotal
            type: object
```

**Insert** into `openapi/invoices.yaml`, immediately before the line `servers:`:

```yaml
    /api/v1/invoices/journal:
        get:
            description: The invoice journal (D11) — what proves complete registration (§ 5-1-3). from and to are issue dates, both required, from on or before to; page and pageSize are the list's.
            operationId: getInvoicesJournal
            parameters:
                - in: query
                  name: from
                  required: true
                  schema:
                    format: date
                    type: string
                - in: query
                  name: to
                  required: true
                  schema:
                    format: date
                    type: string
                - in: query
                  name: page
                  schema:
                    format: int32
                    type: integer
                - in: query
                  name: pageSize
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/InvoicesJournalResponse'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Bad Request — from after to, or paging out of range.
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
            summary: The invoice journal
            tags:
                - Invoices
            x-vantigo-access: permission:invoices:access
```

**Create** `apps/server/internal/invoices/queries/journal.sql`:

```sql
-- name: JournalPage :many
-- JournalPage is one page of the journal (D11): the issued documents with an
-- issue date in the range, in number order, each credit note with the number
-- of the invoice it credits.
SELECT i.id, i.number, i.kind, i.issue_date, i.delivery_date, i.delivery_from, i.delivery_to, i.due_date,
       i.buyer_customer_number, i.buyer_name, i.buyer_organisation_number, i.currency,
       i.net_total, i.vat_total, i.gross_total, o.number AS credits_number
FROM invoices.invoices i
LEFT JOIN invoices.invoices o ON o.id = i.credits_invoice_id
WHERE i.status = 'issued' AND i.issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
ORDER BY i.number
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: JournalCount :one
SELECT count(*)::int FROM invoices.invoices
WHERE status = 'issued' AND issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date;

-- name: JournalSummaries :many
-- JournalSummaries is the VAT of a page's documents in one round trip.
SELECT * FROM invoices.vat_summaries WHERE invoice_id = ANY(sqlc.arg(invoice_ids)::bigint[])
ORDER BY invoice_id, rate_percent DESC, vat_category;

-- name: JournalTotalsByCode :many
-- JournalTotalsByCode is the range's VAT per (SAF-T code, category, rate)
-- over every document in it, not the page — credit notes subtracted: they are
-- stored positive, and a journal that summed them would overstate revenue.
SELECT s.saf_t_code, s.vat_category, s.rate_percent,
       sum(CASE WHEN i.kind = 'credit_note' THEN -s.taxable_amount ELSE s.taxable_amount END)::numeric(14,2) AS taxable_amount,
       sum(CASE WHEN i.kind = 'credit_note' THEN -s.vat_amount ELSE s.vat_amount END)::numeric(14,2) AS vat_amount
FROM invoices.vat_summaries s
JOIN invoices.invoices i ON i.id = s.invoice_id
WHERE i.status = 'issued' AND i.issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
GROUP BY s.saf_t_code, s.vat_category, s.rate_percent
ORDER BY s.rate_percent DESC, s.vat_category, s.saf_t_code;

-- name: JournalTotals :one
-- JournalTotals is the range's net, VAT and gross, credit notes subtracted.
SELECT coalesce(sum(CASE WHEN kind = 'credit_note' THEN -net_total ELSE net_total END), 0)::numeric(14,2) AS net_total,
       coalesce(sum(CASE WHEN kind = 'credit_note' THEN -vat_total ELSE vat_total END), 0)::numeric(14,2) AS vat_total,
       coalesce(sum(CASE WHEN kind = 'credit_note' THEN -gross_total ELSE gross_total END), 0)::numeric(14,2) AS gross_total
FROM invoices.invoices
WHERE status = 'issued' AND issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date;

-- name: JournalGaps :many
-- JournalGaps is the gap check (D11): every number from the one before the
-- range's first — never below the series start — to the range's last that no
-- issued document holds. The first document of the range is thereby checked
-- against the one before it. At most limit are listed; a caller asking for
-- one more than it shows learns there are more.
WITH bounds AS (
    SELECT min(number) AS first_number, max(number) AS last_number FROM invoices.invoices
    WHERE status = 'issued' AND issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
)
SELECT g::bigint AS missing
FROM bounds, generate_series(greatest(bounds.first_number - 1, sqlc.arg(series_start)::bigint), bounds.last_number) AS g
WHERE bounds.first_number IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM invoices.invoices d WHERE d.number = g AND d.status = 'issued')
ORDER BY g
LIMIT sqlc.arg(max_gaps);
```

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && cd ../..
```

- [ ] **Step 3: The journal**

**Create** `apps/server/internal/invoices/journal.go`:

```go
package invoices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the invoice journal (D11): what proves complete registration —
// Skatteetaten wants it visible that "det ikke er brudd i nummerserien". It
// lists the issued documents of a range of issue dates in number order, totals
// them per SAF-T code, category and rate over the whole range, and checks the
// series for gaps. Credit notes are stored positive and signed negative here,
// in every amount.

// maxJournalGaps is how many missing numbers the journal lists (D11).
const maxJournalGaps = 1000

// signed is a document's amount as the journal shows it.
func signed(kind string, n pgtype.Numeric) (float64, error) {
	r, err := ratFromNumeric(n)
	if err != nil {
		return 0, err
	}
	if kind == kindCreditNote {
		r.Neg(r)
	}
	return floatFromRat(r, 2), nil
}

// GetInvoicesJournal The invoice journal
// (GET /api/v1/invoices/journal)
func (s *server) GetInvoicesJournal(ctx context.Context, req gen.GetInvoicesJournalRequestObject) (gen.GetInvoicesJournalResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.From.After(p.To.Time) {
		errs = append(errs, "'from' must be on or before 'to'.")
	}
	if len(errs) > 0 {
		return gen.GetInvoicesJournal400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	from, to := pgDate(utcDay(p.From.Time)), pgDate(utcDay(p.To.Time))

	// One snapshot, so the page, the totals and the gaps agree with each other
	// even while documents are being issued.
	var resp gen.InvoicesJournalResponse
	err := pgx.BeginTxFunc(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := store.New(tx)
		settings, err := q.GetSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the settings: %w", err)
		}
		rows, err := q.JournalPage(ctx, store.JournalPageParams{IssuedFrom: from, IssuedTo: to, PageOffset: (page - 1) * pageSize, PageSize: pageSize})
		if err != nil {
			return fmt.Errorf("invoices: read the journal: %w", err)
		}
		total, err := q.JournalCount(ctx, store.JournalCountParams{IssuedFrom: from, IssuedTo: to})
		if err != nil {
			return fmt.Errorf("invoices: count the journal: %w", err)
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		sums, err := q.JournalSummaries(ctx, ids)
		if err != nil {
			return fmt.Errorf("invoices: read the journal's VAT: %w", err)
		}
		kinds := map[int64]string{}
		for _, r := range rows {
			kinds[r.ID] = r.Kind
		}
		byDoc := map[int64][]gen.InvoicesJournalCode{}
		for _, sm := range sums {
			code := gen.InvoicesJournalCode{SafTCode: sm.SafTCode, Category: sm.VatCategory}
			if code.RatePercent, err = floatFromNumeric(sm.RatePercent); err != nil {
				return err
			}
			if code.TaxableAmount, err = signed(kinds[sm.InvoiceID], sm.TaxableAmount); err != nil {
				return err
			}
			if code.VatAmount, err = signed(kinds[sm.InvoiceID], sm.VatAmount); err != nil {
				return err
			}
			byDoc[sm.InvoiceID] = append(byDoc[sm.InvoiceID], code)
		}

		resp.Data = make([]gen.InvoicesJournalRow, 0, len(rows))
		for _, r := range rows {
			row := gen.InvoicesJournalRow{
				Id: r.ID, Number: *r.Number, Kind: r.Kind, IssueDate: wireDate(r.IssueDate.Time),
				DeliveryDate: wireDateOf(r.DeliveryDate), DeliveryFrom: wireDateOf(r.DeliveryFrom), DeliveryTo: wireDateOf(r.DeliveryTo),
				DueDate: wireDateOf(r.DueDate), BuyerCustomerNumber: r.BuyerCustomerNumber, BuyerName: r.BuyerName,
				BuyerOrganisationNumber: r.BuyerOrganisationNumber, Currency: r.Currency, CreditsNumber: r.CreditsNumber,
				VatSummaries: byDoc[r.ID],
			}
			if row.VatSummaries == nil {
				row.VatSummaries = []gen.InvoicesJournalCode{}
			}
			for _, c := range []struct {
				dst *float64
				n   pgtype.Numeric
			}{{&row.NetTotal, r.NetTotal}, {&row.VatTotal, r.VatTotal}, {&row.GrossTotal, r.GrossTotal}} {
				if *c.dst, err = signed(r.Kind, c.n); err != nil {
					return err
				}
			}
			resp.Data = append(resp.Data, row)
		}
		resp.Pagination = apicommon.Pagination(page, pageSize, total)

		codes, err := q.JournalTotalsByCode(ctx, store.JournalTotalsByCodeParams{IssuedFrom: from, IssuedTo: to})
		if err != nil {
			return fmt.Errorf("invoices: total the journal's VAT: %w", err)
		}
		resp.Totals.ByCode = make([]gen.InvoicesJournalCode, 0, len(codes))
		for _, c := range codes {
			code := gen.InvoicesJournalCode{SafTCode: c.SafTCode, Category: c.VatCategory}
			for _, f := range []struct {
				dst *float64
				n   pgtype.Numeric
			}{{&code.RatePercent, c.RatePercent}, {&code.TaxableAmount, c.TaxableAmount}, {&code.VatAmount, c.VatAmount}} {
				if *f.dst, err = floatFromNumeric(f.n); err != nil {
					return err
				}
			}
			resp.Totals.ByCode = append(resp.Totals.ByCode, code)
		}
		totals, err := q.JournalTotals(ctx, store.JournalTotalsParams{IssuedFrom: from, IssuedTo: to})
		if err != nil {
			return fmt.Errorf("invoices: total the journal: %w", err)
		}
		for _, f := range []struct {
			dst *float64
			n   pgtype.Numeric
		}{{&resp.Totals.NetTotal, totals.NetTotal}, {&resp.Totals.VatTotal, totals.VatTotal}, {&resp.Totals.GrossTotal, totals.GrossTotal}} {
			if *f.dst, err = floatFromNumeric(f.n); err != nil {
				return err
			}
		}

		gaps, err := q.JournalGaps(ctx, store.JournalGapsParams{
			IssuedFrom: from, IssuedTo: to, SeriesStart: settings.SeriesStart, MaxGaps: maxJournalGaps + 1,
		})
		if err != nil {
			return fmt.Errorf("invoices: check the series for gaps: %w", err)
		}
		resp.GapsTruncated = len(gaps) > maxJournalGaps
		if resp.GapsTruncated {
			gaps = gaps[:maxJournalGaps]
		}
		resp.Gaps = gaps
		if resp.Gaps == nil {
			resp.Gaps = []int64{}
		}
		resp.SeriesStart = settings.SeriesStart
		next, err := q.CounterNextValue(ctx)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("invoices: read the document counter: %w", err)
		default:
			resp.CounterLast = ptr(next - 1)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesJournal200JSONResponse(resp), nil
}
```

- [ ] **Step 4: Verify and commit**

**Run**, from the repository root:

```bash
cd apps/server && mise exec -- go generate ./... && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
mise exec -- bun run gen:client
```

```bash
cd apps/server && export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 ./internal/invoices/ ./internal/openapi/
mise exec -- golangci-lint run ./internal/invoices/...
cd ../..
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task8.txt <<'MSG'
feat(invoices): the invoice journal and its gap check

The journal (invoices foundation design D11), what proves complete
registration: the range's issued documents in number order with credit
notes signed negative, totals per SAF-T code over the whole range, and the
gap check from the number before the range's first to its last, never below
the series start; counterLast shows a number taken without a document.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/journal.go' 'apps/server/internal/invoices/journal_test.go' 'apps/server/internal/invoices/queries/journal.sql' 'apps/server/internal/invoices/store/journal.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git commit -F /tmp/claude-1000/msg-invoices-task8.txt -- 'apps/invoices/frontend/src/api-schema.d.ts' 'apps/server/internal/invoices/gen/api.gen.go' 'apps/server/internal/invoices/journal.go' 'apps/server/internal/invoices/journal_test.go' 'apps/server/internal/invoices/queries/journal.sql' 'apps/server/internal/invoices/store/journal.sql.go' 'apps/server/internal/openapi/specs/invoices.yaml' 'openapi/COVERAGE.md' 'openapi/invoices.yaml'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 9: The docs (D13)

`docs/invoices.md` is new: the law in one page (numbering, immutability, credit notes, VAT per rate, the issue-date rule and that "calendar day ≤ 15" is stricter than the law, delivery, NOK only), the model, drafts, issuing with every code, credit notes, the PDF and store-once, the journal, retention (§ 13; the object store's backup is part of it; nothing purged; the 2027 wording unconfirmed), why anonymisation erases drafts only, permissions with the `customers:view` note, every endpoint and its refusals, and the plain warning that phase 1A meets neither the B2G nor the 2027 B2B duty. The other docs gain what the module changed.

**Files:**
- Create: `docs/invoices.md`
- Modify: `CONTRIBUTING.md`, `ROADMAP.md`, `deploy/compose/README.md`, `deploy/compose/vantigo.env.example`, `docs/README.md`, `docs/customers.md`, `docs/module-boundaries.md`
- Read first (do not change): `docs/expenses.md` (the voice), `docs/customers.md:88-120,1436-1450,1595-1610`, `docs/module-boundaries.md:230-270`, `ROADMAP.md:734-800`, `CONTRIBUTING.md:60-95,140-150,225-250,410-470,590-600`, `deploy/compose/README.md:55-70,342-350`, `deploy/compose/vantigo.env.example:185-196`

- [ ] **Step 1: The module's own document**

**Create** `docs/invoices.md`:

```markdown
# Invoices

The Invoices module issues the sales document of Norwegian bookkeeping: a draft
becomes a numbered, immutable invoice or credit note, rendered to a PDF that is stored
once and downloaded as stored, and listed in a journal that proves the number series has
no gaps. This is phase 1A of the module
([design](superpowers/specs/2026-09-26-invoices-foundation-design.md),
[research](superpowers/research/2026-09-26-invoices-module.md)). Vantigo stays a
sub-ledger: there is no general ledger and nothing is posted.

> **Phase 1A alone does not meet the e-invoicing duties.** Invoicing the public sector
> has required EHF since 2019 (FOR-2019-04-01-444), and invoicing Norwegian businesses
> requires an e-invoice from **2027-01-01** (Lov 19. juni 2026 nr. 39). This phase
> issues PDFs a person hands over; EHF over Peppol is phase 2. See
> [What comes next](#what-comes-next).

## The law in one page

Sources: bokføringsforskriften kap. 5
(<https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558/KAPITTEL_5-1>) and EHF
Billing 3.0 Norway (<https://anskaffelser.dev/postaward/g3/spec/current/billing-3.0/norway/>).

- **Numbering (§ 5-1-3).** One machine-assigned series for invoices and credit notes,
  continuous across years, starting at the settings' `seriesStart`. A draft has no
  number; the number is allocated inside the issue transaction from a counter row, not
  a PostgreSQL `SEQUENCE`, so a refused or failed issue gives its number back. The
  start locks at the first issue (409 `series_locked`).
- **Immutability (§ 5-2-9).** An issued document is never changed or deleted — by the
  API (409 `invoice_issued`) and by the database: triggers refuse any change to an
  issued row but the merge's `customer_id` and the PDF columns set once, and any write
  to its lines or VAT summaries (SQLSTATE `P0001`, "invoices: issued document is
  immutable").
- **Correction (§ 5-2-7).** A credit note in the same series reverses the original,
  in full or in part.
- **VAT per rate (§ 5-1-5, Peppol BR-CO-17).** VAT is computed per (category, rate) on
  the sum of the lines' nets, never per line, and every 0 % category gets its own row.
- **The issue date (§ 5-1-3 third paragraph).** Today, or the last day of the previous
  month while today's calendar day is 15 or less and the delivery ended on or before
  it. The regulation says "de femten første virkedager"; fifteen working days always
  reach at least the 17th, so **"calendar day ≤ 15" is stricter than the law** and needs
  no holiday calendar. On top of that no document may be dated before the latest issued
  one, so numbers and dates are both monotone — a guard the law does not ask for.
- **Late issue (§ 5-2-2).** More than a month after delivery the issue still succeeds
  and warns `issued_late`: refusing would leave the sale undocumented.
- **Delivery (§ 5-1-1 nr. 4).** A day or a period is required to issue; a place of
  delivery is optional and printed only when it is not the buyer's address. Whether the
  buyer address is enough as the place of delivery for services is **unconfirmed** — no
  Skatteetaten statement was read on it.
- **The parties (§ 5-1-2).** The seller's name and organisation number, followed by
  "MVA" when VAT-registered and "Foretaksregisteret" when registered there. The buyer's
  name and either a complete address or an organisation number (409 `buyer_incomplete`).
- **NOK only (§ 5-1-1 nr. 6).** VAT must be stated in NOK at the invoice date's rate;
  this phase issues in NOK and nothing else ("Only NOK in this phase"). The exchange
  rate columns exist, fixed at 1, dated at issue.

## The model

| Table | What it holds |
| --- | --- |
| `invoices.settings` | One row: the seller record (legal name, organisation number, VAT registration, Foretaksregisteret, address, bank account, IBAN/BIC, e-mail, footer), the default terms and currency, and `series_start`. |
| `invoices.counters` | The one counter row, `documents`; it exists exactly when something has been issued. |
| `invoices.vat_codes` | The tenant's codes: label, name, SAF-T code, UNCL5305 category, exemption reason, active. |
| `invoices.vat_code_rates` | Each code's rates as dated periods that never overlap (an exclusion constraint). A rate change is a new period, not a new code. |
| `invoices.invoices` | Drafts and issued documents: kind, status, number, customer, delivery, references, notes, the buyer snapshot and the seller snapshot (written at issue), the totals, and the stored PDF's key and SHA-256. |
| `invoices.lines` | Description, quantity (3 decimals), unit, unit price (4), discount (2), VAT code, the computed gross, allowance and net, the credited line on a credit note, and the VAT snapshot written at issue. |
| `invoices.vat_summaries` | An issued document's VAT per (category, rate) with its SAF-T code and reason. |

The seeded codes, each from 2026-01-01: `3` 25 %, `31` 15 %, `32` 11.11 %, `33` 12 %
(all S), `5` Z, `51` AE, `52` G, `6` **E** (unntatt, mval. kap. 3) and `7` **O** (a seller
outside the VAT register) at 0 %.

**Money.** Amounts arrive as JSON numbers and are read as the decimal text they were
written as, never as a binary float. A line's gross is quantity × unit price rounded to
øre, its allowance the discount of that rounded gross, rounded, and its net the
difference — gross less allowance, as EHF expresses a discount. Every rounding is two
decimals, the half away from zero; there is no øre rounding of the total. A line is at
most 999 999 999.99, a document 99 999 999 999.99 gross, and at most 500 lines.

## Drafts

A draft is created for a customer id; its buyer is read through
`contracts.CustomerDirectory.BillingProfile` before anything is written. An omitted
`yourReference` is the profile's buyer reference and omitted terms are the profile's,
else the settings' default. The customer gates, in order: merged away (409
`customer_merged` with `mergedInto`), archived — which includes an anonymised person —
(`customer_archived`), disabled — "blocked for invoicing" — (`customer_blocked`), and no
customer at all (`customer_missing`). They run on create, on every save and again at
issue; never on a credit note, and never on a read. A draft's totals are computed with
the rates in force today; the issue computes them again for the issue date. Warnings
never refuse: `customer_currency_differs`, `issued_late`, `credit_exceeds_invoice`,
`credit_exceeds_line`.

## Issuing

`POST /invoices/{id}/issue` runs one READ COMMITTED transaction in a fixed order:
lock the document, share the settings row, allocate the number, and only then check
every rule — the counter row is what serialises two issues, so every check that
depends on other documents runs after it. The directory is read before the transaction
and the object store is used after it; neither is ever called under a lock. The lock
order is always document → settings → counter → original, and nothing takes them in
another order: `PUT /settings` and the rate operations take only the settings row, the
merge holder updates documents only.

The checks, each a 409 that rolls the number back: `seller_incomplete`, `no_lines`,
`delivery_date_missing`, `issue_date_not_allowed` (with `allowedIssueDates`); for an
invoice the customer gates, `buyer_incomplete`, `vat_code_inactive` and
`vat_code_not_valid` (with `linePosition`), `vat_not_registered` (a seller outside the
register issues only O lines), `category_o_not_allowed` (a registered seller issues no O
line), `reverse_charge_needs_org_number` and `vat_codes_ambiguous`; for a credit note
`credit_exceeds_line` (with `linePosition`) and `credit_exceeds_invoice`. Before the
transaction: `invoice_issued`, and 503 `storage_unavailable` when no object store is
configured — an issued number whose PDF could never be stored is not allowed to exist.
A merge that re-points the draft between the directory read and the lock is
`invoice_changed`.

## Credit notes

`POST /invoices/{id}/credit` makes a credit-note draft of an issued invoice: the
customer, currency, rate, delivery, references and the **buyer snapshot** are copied —
no directory is read, so an anonymised customer's correction names the person the
original named — with every line, its VAT code and the line it credits. A credit draft
may remove lines, lower a quantity or a unit price, and edit a description and the
notes; anything else is a 400 on the field. The caps are decided at issue under the
original's lock: per original line, the quantity and net credited by the issued credit
notes and this one; and this one's gross against what the invoice has left. A credit
note skips every customer gate, the VAT active and validity checks and the registration
rules — it reverses the original's treatment at the original's rates — and keeps the
issue-date rule, `seller_incomplete`, `no_lines`, `delivery_date_missing` and both
caps. It has no due date and no payment block. Refusals: `invoice_draft`,
`credit_note_not_creditable`, `invoice_fully_credited`. The cap is common practice, not
law.

## The PDF

Rendered with maroto v2 (pure Go; the runtime image has no fonts) in **Noto Sans**,
embedded, under the SIL Open Font License 1.1 — its text is
`apps/server/internal/invoices/fonts/LICENSE`. A document prints in its buyer's language
(nb, or en), except the reverse-charge text "Omvendt avgiftsplikt – Merverdiavgift ikke
beregnet", which is the regulation's own wording and printed in Norwegian always.

**Store-once is what makes it lawful, not determinism.** Right after an issue commits,
the PDF is rendered from the document's own rows and snapshots only — never the
settings, the directory or the VAT tables — hashed, and put under
`documents/<id>/<number>-<sha256>.pdf` in the `invoices` scope (physically
`invoices/documents/…`), and the row records the key and hash once. A store failure there
never fails the issue: the response says `pdfStored: false` and the first download stores
it. Every download streams the stored object, verified against its hash; a document whose
hash is set is never rendered again, and the module never deletes an object. A stored
object that is gone or no longer matches its hash is a 500 logged at error — an operator
problem, never papered over. Reproducible bytes are a nice-to-have: catalog sorting and a
fixed modification date are set process-wide and the creation date is the issue instant,
but the bytes may change with a maroto, gofpdf or font upgrade; stored PDFs never do.

`GET /invoices/{id}/preview.pdf` renders a draft on demand with the watermark
"UTKAST — ikke et salgsdokument", no number, today's date, the current settings and the
customer's current profile. It is never stored.

## The journal

`GET /invoices/journal?from&to` lists the issued documents with an issue date in the
range in number order, credit notes **signed negative** in every amount (they are stored
positive), totals per SAF-T code, category and rate over the whole range, and the gap
check: every number from the one before the range's first (never below the series start)
to its last that no issued document holds — at most 1000 listed. `counterLast` is the
counter's last allocated number; when it is not the highest issued number, a number was
allocated without a document. This is what shows "det ikke er brudd i nummerserien".

## Retention and personal data

Sales documentation is kept **five years after the end of the financial year**
(bokføringsloven § 13, <https://lovdata.no/lov/2004-11-19-73/§13>). Nothing is purged in
this phase; a purge is later work. The 2027 wording of § 13 (Lov 2026 nr. 39) was not
read — **unconfirmed**. **The operator's backup of the object store is part of that
retention**: the only storage driver is `fs`, with no WORM, so the PDFs are only as safe
as the volume and its backups ([storage](storage.md)).

The module fills both customer slots ([module boundaries](module-boundaries.md)):

- **Merging customers** (`contracts.CustomerReferenceHolder`) re-points every document of
  the absorbed customer, drafts and issued, reported as `invoices.invoices`. An issued
  document keeps its buyer snapshot — the id is not printed, the snapshot is — and its
  revision.
- **A person's export** (`contracts.CustomerPersonalData`) hands over every issued
  document and every draft with its lines, the buyer snapshot, the references and the
  notes, internal notes included.
- **Anonymisation** deletes the person's drafts, invoice and credit-note drafts alike,
  reported as `invoices.drafts` — a draft is not a sales document and has no retention
  basis, so GDPR art. 17 applies — and keeps every issued document and its buyer
  snapshot under § 13, reported as `invoices.documents` at 0. `contracts.ErasedData`
  carries no reason field; this paragraph is where the reason is written.

## Permissions

No built-in role holds any of these; Owner has the wildcard.

| Key | Sensitive | What it allows |
| --- | --- | --- |
| `invoices:access` | no | Use the app; read every invoice, credit note, PDF and the journal. |
| `invoices:create` | no | Create, edit and delete drafts; preview a draft. |
| `invoices:issue` | yes | Issue a draft; create a credit-note draft. |
| `invoices:manage` | yes | The seller record, the series start, VAT codes and their rates. |

**Creating a draft in the app also needs `customers:view`**: the directory has no
search, so the buyer picker reads the customers module's own list. The API takes a
customer id and checks nothing more; the app hides "New invoice" without it.

## Endpoints

All under `/api/v1/invoices`, every one behind `invoices:access`.

| Operation | Also needs | Refusals |
| --- | --- | --- |
| `GET /meta` | | |
| `GET /settings` | | |
| `PUT /settings` | `invoices:manage` | 400 on the field (both mod-11 checks, IBAN mod-97, BIC, "Only NOK in this phase"); 409 `series_locked`, or a stale revision (no code) |
| `GET /vat-codes` | | |
| `POST /vat-codes` | `invoices:manage` | 400 on the field, a duplicate code on `code` |
| `PUT /vat-codes/{id}` | `invoices:manage` | 404; 400; 409 `vat_code_in_use`, a stale revision |
| `POST /vat-codes/{id}/rates` | `invoices:manage` | 404; 400 on `ratePercent` or `validFrom`; 409 `rate_change_in_past` |
| `DELETE /vat-codes/{id}/rates/{rateId}` | `invoices:manage` | 404; 409 `rate_period_not_latest`, `rate_period_last`, `rate_period_in_use` |
| `GET /` | | 400 paging, status, kind, `from` after `to` |
| `POST /` | `invoices:create` | 400 on the field; 409 the customer gates |
| `GET /{id}` | | 404 |
| `PUT /{id}` | `invoices:create` | 404; 400; 409 `invoice_issued`, the customer gates, a stale revision |
| `DELETE /{id}` | `invoices:create` | 404; 409 `invoice_issued` |
| `POST /{id}/issue` | `invoices:issue` | 404; 409 every code under [Issuing](#issuing); 503 `storage_unavailable` |
| `POST /{id}/credit` | `invoices:issue` | 404; 409 `invoice_draft`, `credit_note_not_creditable`, `invoice_fully_credited` |
| `GET /{id}/pdf` | | 404; 409 `invoice_draft`; 500 a missing or altered stored object; 503 `storage_unavailable` |
| `GET /{id}/preview.pdf` | `invoices:create` | 404; 409 `invoice_issued` |
| `GET /journal` | | 400 `from` after `to`, paging |

## What comes next

- **1B** (next): payments with soft removal and derived states (open, overdue, paid,
  credited), e-mail delivery with the PDF, the accountant's CSV export, the dashboard
  card and stats, the customer page's Invoices tab.
- **2**: EHF over Peppol and KID — what makes B2G and, from 2027, B2B invoicing lawful.
- **3**: hours, expenses and milestones turned into lines, with a write-back contract.
- **4**: payment files and reminders.
- **5**: energy consumption billing.
```

- [ ] **Step 2: The rest of the docs**

**Replace** in `docs/README.md`:

```markdown
  approval state machine, weekly submission, the period lock and permissions.
- [Expenses module](expenses.md) — outlays and mileage, receipts, approval, the
  reimbursed and invoiced tracks, dated rates, the payroll CSV and permissions.
- [Module boundaries](module-boundaries.md) — implementation ownership and module
  conventions.
- [Object storage](storage.md) — the filesystem-only provider, module scopes, key
```

**with**:

```markdown
  approval state machine, weekly submission, the period lock and permissions.
- [Expenses module](expenses.md) — outlays and mileage, receipts, approval, the
  reimbursed and invoiced tracks, dated rates, the payroll CSV and permissions.
- [Invoices module](invoices.md) — the sales document: the seller record, gap-free
  numbering, VAT codes with dated rates, issue and immutability, credit notes, the
  stored PDF, the journal, retention and permissions.
- [Module boundaries](module-boundaries.md) — implementation ownership and module
  conventions.
- [Object storage](storage.md) — the filesystem-only provider, module scopes, key
```

**Replace** in `docs/module-boundaries.md`:

```markdown
edit. See [Expenses](expenses.md) for the model, and for what changes with and
without Projects.

## Adding a module

**Backend**
```

**with**:

```markdown
edit. See [Expenses](expenses.md) for the model, and for what changes with and
without Projects.

**Invoices requires customers.** `MODULES` refuses `invoices` without `customers`
(`internal/config`): the buyer, its billing profile and its invoice address come
from `contracts.CustomerDirectory.BillingProfile`, read before any issue or save
takes a lock and never under one. It reads nothing else — products, projects,
time, expenses and energy are not read in phase 1A — and provides no single-provider
contract. It fills both many-provider slots: as a `CustomerReferenceHolder` it
re-points every document of a merged-away customer, drafts and issued alike
(`invoices.invoices`), the immutability trigger allowing exactly `customer_id` to
change on an issued one; as `CustomerPersonalData` it exports a person's documents
and drafts and, on anonymisation, deletes the drafts (`invoices.drafts`) and keeps
the issued documents under bokføringsloven § 13 (`invoices.documents`, at 0).
For those gates `contracts.CustomerBillingProfile` carries the customer's `Status`
(`active`, `disabled`, `archived`) and `MergedInto`, the one change to the
customers contract Invoices made: `disabled` is "blocked for invoicing", and an
archived or merged-away customer still resolves, so a past invoice can be shown and
credited. See [Invoices](invoices.md).

## Adding a module

**Backend**
```

**Replace** in `docs/customers.md`:

```markdown

`active`, `disabled` and `archived`, case-insensitively accepted and stored lower-case.

- **`disabled` is a label only.** It is accepted, stored, shown and — since this
  branch — filterable, but **nothing in this module or any other currently behaves
  differently because a customer is disabled.** This is deliberate (design decision
  D3): Projects already accepts a project against an archived customer on the
  reasoning that "a project can outlive the relationship that started it", so this
  branch does not invent a blocking rule for `disabled` that would contradict that
  standing decision on a status nobody can invoice against yet. Invoices, when it
  exists, is expected to give `disabled` its meaning — Business Central's "blocked
  for invoicing" is the model in mind.
- **Customers are archived, never deleted.** `DELETE /customers/{id}` sets
  `status: "archived"`; it is idempotent (archiving an already-archived customer
  writes nothing and emits no second event) and requires `customers:delete`.
```

**with**:

```markdown

`active`, `disabled` and `archived`, case-insensitively accepted and stored lower-case.

- **`disabled` means blocked for invoicing.** It is accepted, stored, shown and
  filterable here, and nothing in this module behaves differently because of it; the
  Invoices module gives it its meaning ([Invoices](invoices.md#drafts), Business
  Central's "blocked for invoicing" the model): creating, saving and issuing an
  invoice for a disabled customer is refused with 409 `customer_blocked`. It never
  blocks a credit note, a PDF or a read — an invoice already issued must stay
  correctable. Projects still accepts a project against an archived or disabled
  customer: "a project can outlive the relationship that started it" (design decision
  D3). An **archived** customer — a merged-away and an anonymised one included — is
  refused a new invoice likewise (`customer_archived`, and `customer_merged` naming
  the survivor), and is still read, shown and credited: the billing profile carries
  the customer's `Status` and `MergedInto` for exactly these gates.
- **Customers are archived, never deleted.** `DELETE /customers/{id}` sets
  `status: "archived"`; it is idempotent (archiving an already-archived customer
  writes nothing and emits no second event) and requires `customers:delete`.
```

**Replace** in `docs/customers.md`:

```markdown
| --- | --- |
| projects | `projects.projects.customer_id` (`projects.projects`). Each moved project's revision advances; no project timeline entry is written. Time and expenses reach a customer only through a project, so they hold nothing. |
| energy | `energy.supply_periods.customer_id` (`energy.supplyPeriods`). The overlap constraint is per metering point, so a re-point cannot violate it. Energy has no module doc of its own; this row is its paragraph. Its personal data (rule 9) is the supply periods with the metering point's address and the point's consumption inside each period — monthly kWh sums of the current intervals within the period's dates, each month a calendar month in the point's market zone (hourly for years would make a file nobody reads; energy's consumption endpoints keep the series) — handed over in the export and kept by an anonymisation: the period is the point's history and the readings are needed for settlement. The period keeps pointing at the anonymised customer, so its dates and the point's address — for a private person most likely their home — stay linked to "Anonymised person #1234": for that link this is pseudonymisation, not removal. |
| communications | `conversations.customer_id` (`communications.conversations`), `conversations.suggested_customer_id` (`communications.conversationSuggestions`), and the candidate list (`communications.conversationCandidates`), where a conversation that already lists the survivor keeps it once. |

Any error, a holder's included, rolls back everything: nothing moved, no marker, no
```

**with**:

```markdown
| --- | --- |
| projects | `projects.projects.customer_id` (`projects.projects`). Each moved project's revision advances; no project timeline entry is written. Time and expenses reach a customer only through a project, so they hold nothing. |
| energy | `energy.supply_periods.customer_id` (`energy.supplyPeriods`). The overlap constraint is per metering point, so a re-point cannot violate it. Energy has no module doc of its own; this row is its paragraph. Its personal data (rule 9) is the supply periods with the metering point's address and the point's consumption inside each period — monthly kWh sums of the current intervals within the period's dates, each month a calendar month in the point's market zone (hourly for years would make a file nobody reads; energy's consumption endpoints keep the series) — handed over in the export and kept by an anonymisation: the period is the point's history and the readings are needed for settlement. The period keeps pointing at the anonymised customer, so its dates and the point's address — for a private person most likely their home — stay linked to "Anonymised person #1234": for that link this is pseudonymisation, not removal. |
| invoices | `invoices.invoices.customer_id` (`invoices.invoices`): every document of the absorbed customer, drafts and issued alike. A draft's revision advances; an issued document's does not, and it keeps its buyer snapshot — the id is not printed, the snapshot is. The immutability trigger allows exactly this column to change on an issued document ([Invoices](invoices.md#retention-and-personal-data)). |
| communications | `conversations.customer_id` (`communications.conversations`), `conversations.suggested_customer_id` (`communications.conversationSuggestions`), and the candidate list (`communications.conversationCandidates`), where a conversation that already lists the survivor keeps it once. |

Any error, a holder's included, rolls back everything: nothing moved, no marker, no
```

**Replace** in `docs/customers.md`:

```markdown
| Addresses, Peppol answer, registry record | Deleted. |
| Contacts | Every association detached, its roles with it; a contact linked to no other customer afterwards is deleted — it existed for this person alone. One another customer still links stays, theirs too. |
| Timeline | Every entry **stays**, deleted ones included — its type, its day, its state and its follow-up's day and assignee as they were; an open follow-up is closed, done at the run's instant — the customer takes no more writes, so it could never be marked done and would stay overdue on somebody's Follow-ups page for ever; one already done keeps its own instant — with its content anonymised: a manual entry's summary and note become "[anonymised]" and its source URL goes; a generated entry's summary does too, unless its type's summary is built from nothing personal (`customer.status_changed`, `customer.type_changed`, `customer.contact_info_updated`, `customer.billing_profile_updated`, `customer.peppol_lookup`, `customer.tags_changed`, `customer.group_changed`, `customer.owner_changed` and the three anonymisation events — an allow-list, so an event type added later is anonymised until somebody decides otherwise); in every payload the top-level keys that carry the person — `name`, `customerName`, `identity`, `legalIdentity`, `contactInfo`, `billingProfile`, `before`, `after`, `changes`, `absorbed`, `into`, and a contact's or address's `displayName`, `firstName`, `middleName`, `lastName`, `title`, `phone`, `email`, `label`, `display` — become "[anonymised]", each replaced whole — on every event alike, so a status change's `before` and `after` go too, rather than a per-event list a new event type could slip past — and the rest (`customerId`, ids, dates, counts, statuses) is kept. Revisions the same. The author of each entry (`actorDisplay`) is staff, and stays. One set-based statement per table, in SQL. |
| Other modules | Each `contracts.CustomerPersonalData.EraseCustomerData`, inside the same transaction, in the order the installation composes them: communications deletes the person's conversations, every message and the rows under it through the retention worker's own deletes (a message still waiting in the outbox goes with its job), queues every object key — attachments, raw payloads and staged uploads — on the cleanup ledger for the cleanup worker to delete after commit, and clears a suggestion or candidate row naming them on another conversation; energy keeps the supply periods and the consumption (a period is the metering point's history, the address the point's, the readings are needed for settlement — the period's link to the customer stays, pseudonymised, see the [holder table](#what-moves-what-stays-what-is-recorded)); projects keeps the projects (invoiced work stays, no customer name is stored there). |
| Last | `anonymised_at` is set, the revision advances, and `customer.anonymised` is recorded — after the rewrite, so the one event that keeps its words: `{customerId, erased: [{kind, count}]}`, this module's four kinds first (`customers.addresses`, `customers.contactAssociations`, `customers.contacts`, `customers.timelineEntries`) and then each module's — `communications.conversations`, `communications.messages`, `communications.objects`, `communications.conversationSuggestions`, `communications.conversationCandidates`, `energy.supplyPeriods`, `projects.projects` — a module that kept everything listed at zero; the actor is the system. |

**A failing customer is logged and tried again.** A module's error — a panic
included, logged with its stack, since the runner never restarts a worker that panicked
```

**with**:

```markdown
| Addresses, Peppol answer, registry record | Deleted. |
| Contacts | Every association detached, its roles with it; a contact linked to no other customer afterwards is deleted — it existed for this person alone. One another customer still links stays, theirs too. |
| Timeline | Every entry **stays**, deleted ones included — its type, its day, its state and its follow-up's day and assignee as they were; an open follow-up is closed, done at the run's instant — the customer takes no more writes, so it could never be marked done and would stay overdue on somebody's Follow-ups page for ever; one already done keeps its own instant — with its content anonymised: a manual entry's summary and note become "[anonymised]" and its source URL goes; a generated entry's summary does too, unless its type's summary is built from nothing personal (`customer.status_changed`, `customer.type_changed`, `customer.contact_info_updated`, `customer.billing_profile_updated`, `customer.peppol_lookup`, `customer.tags_changed`, `customer.group_changed`, `customer.owner_changed` and the three anonymisation events — an allow-list, so an event type added later is anonymised until somebody decides otherwise); in every payload the top-level keys that carry the person — `name`, `customerName`, `identity`, `legalIdentity`, `contactInfo`, `billingProfile`, `before`, `after`, `changes`, `absorbed`, `into`, and a contact's or address's `displayName`, `firstName`, `middleName`, `lastName`, `title`, `phone`, `email`, `label`, `display` — become "[anonymised]", each replaced whole — on every event alike, so a status change's `before` and `after` go too, rather than a per-event list a new event type could slip past — and the rest (`customerId`, ids, dates, counts, statuses) is kept. Revisions the same. The author of each entry (`actorDisplay`) is staff, and stays. One set-based statement per table, in SQL. |
| Other modules | Each `contracts.CustomerPersonalData.EraseCustomerData`, inside the same transaction, in the order the installation composes them: communications deletes the person's conversations, every message and the rows under it through the retention worker's own deletes (a message still waiting in the outbox goes with its job), queues every object key — attachments, raw payloads and staged uploads — on the cleanup ledger for the cleanup worker to delete after commit, and clears a suggestion or candidate row naming them on another conversation; energy keeps the supply periods and the consumption (a period is the metering point's history, the address the point's, the readings are needed for settlement — the period's link to the customer stays, pseudonymised, see the [holder table](#what-moves-what-stays-what-is-recorded)); projects keeps the projects (invoiced work stays, no customer name is stored there); invoices deletes the person's drafts, invoice and credit-note drafts alike — a draft is not a sales document and has nothing that keeps it — and keeps every issued document and its buyer snapshot, which bokføringsloven § 13 keeps for five years after the end of the financial year ([Invoices](invoices.md#retention-and-personal-data)). |
| Last | `anonymised_at` is set, the revision advances, and `customer.anonymised` is recorded — after the rewrite, so the one event that keeps its words: `{customerId, erased: [{kind, count}]}`, this module's four kinds first (`customers.addresses`, `customers.contactAssociations`, `customers.contacts`, `customers.timelineEntries`) and then each module's — `communications.conversations`, `communications.messages`, `communications.objects`, `communications.conversationSuggestions`, `communications.conversationCandidates`, `energy.supplyPeriods`, `projects.projects`, `invoices.drafts`, `invoices.documents` — a module that kept everything listed at zero; the actor is the system. |

**A failing customer is logged and tried again.** A module's error — a panic
included, logged with its stack, since the runner never restarts a worker that panicked
```

**Replace** in `ROADMAP.md`:

```markdown
a late phase. Vantigo stays a sub-ledger: it issues, sends, tracks and exports; it keeps
no general ledger.

### Phase 1 — The sales document

The seller record (legal name, organisation number, VAT registration, Foretaksregisteret,
address, bank account), a dated VAT-code table seeded with the SAF-T output codes, one
number series with a start set once and gap-free allocation inside the issue
transaction, and the invoice itself: a draft with lines (description, quantity, unit,
unit price, discount, VAT code), issued into an immutable document with a buyer
snapshot taken from the billing profile, VAT summarised per rate, a deterministic PDF
rendered once and stored, e-mail delivery, full and partial credit notes, manual
payment registration with partial payments and a derived open/overdue status, an
invoice journal proving the series has no gaps, and a CSV export for the accountant.
Currency and exchange rate are modelled from day one even though the UI starts NOK-only.

*Unblocks:* everything below; nothing else is lawful without numbering, immutability
and credit notes.

### Phase 2 — EHF over Peppol, and KID

```

**with**:

```markdown
a late phase. Vantigo stays a sub-ledger: it issues, sends, tracks and exports; it keeps
no general ledger.

### Phase 1A — The sales document (done)

Delivered on `feat/invoices-foundation`
([design](docs/superpowers/specs/2026-09-26-invoices-foundation-design.md),
[`docs/invoices.md`](docs/invoices.md)): the seller record and one gap-free number
series whose start locks at the first issue; VAT codes whose rates are dated periods,
seeded with the SAF-T output codes; drafts issued in one serialised transaction into an
immutable, numbered document with a buyer and a seller snapshot and VAT per rate,
immutability enforced by database triggers too; the issue-date rule with § 5-1-3's
previous-month exception; a PDF in Noto Sans rendered from the snapshot and stored once,
and a watermarked preview; full and partial credit notes in the same series with caps
per line and on the headline; an invoice journal with the gap check; both customer slots
(merge re-points, anonymisation erases drafts only); and `Status` and `MergedInto` on the
customers contract's billing profile, so `disabled` finally means "blocked for invoicing".

### Phase 1B — Payments, delivery and the export (next)

A branch cut from `main` after 1A merges, building only on 1A's tables: manual payment
registration with soft removal and derived states (credited, paid, overdue, partially
paid, open), e-mail delivery with the stored PDF and a Reply-To, the accountant's CSV
export, the dashboard card and stats, the customer page's Invoices tab, and the
customers-plus-invoices integration test.

*Unblocks:* everything below; nothing else is lawful without numbering, immutability
and credit notes. Phase 1A alone does not meet the B2G duty (EHF since 2019) nor the B2B
duty from 2027-01-01 — that is phase 2.

### Phase 2 — EHF over Peppol, and KID

```

**Replace** in `CONTRIBUTING.md`:

```markdown
│   │       ├── projects/            # Projects vertical slice
│   │       ├── time/                # Time vertical slice (package timetracking)
│   │       ├── expenses/            # Expenses vertical slice
│   │       ├── module/              # The platform modules mount through
│   │       ├── contracts/           # Cross-module interfaces, permissions, access rules
│   │       ├── config/              # The environment reference: one struct, one validation pass
```

**with**:

```markdown
│   │       ├── projects/            # Projects vertical slice
│   │       ├── time/                # Time vertical slice (package timetracking)
│   │       ├── expenses/            # Expenses vertical slice
│   │       ├── invoices/            # Invoices vertical slice (the sales document)
│   │       ├── module/              # The platform modules mount through
│   │       ├── contracts/           # Cross-module interfaces, permissions, access rules
│   │       ├── config/              # The environment reference: one struct, one validation pass
```

**Replace** in `CONTRIBUTING.md`:

```markdown
│   ├── energy/frontend/             # @vantigo/energy-ui
│   ├── projects/frontend/           # @vantigo/projects-ui
│   ├── time/frontend/               # @vantigo/time-ui
│   └── expenses/frontend/           # @vantigo/expenses-ui
├── packages/
│   ├── frontend-shell/              # @vantigo/frontend-shell — shared app shell, theme, branding
│   └── frontend-api-client/         # @vantigo/frontend-api-client — generated types and client
```

**with**:

```markdown
│   ├── energy/frontend/             # @vantigo/energy-ui
│   ├── projects/frontend/           # @vantigo/projects-ui
│   ├── time/frontend/               # @vantigo/time-ui
│   ├── expenses/frontend/           # @vantigo/expenses-ui
│   └── invoices/frontend/           # @vantigo/invoices-ui
├── packages/
│   ├── frontend-shell/              # @vantigo/frontend-shell — shared app shell, theme, branding
│   └── frontend-api-client/         # @vantigo/frontend-api-client — generated types and client
```

**Replace** in `CONTRIBUTING.md`:

```markdown
### Database

One PostgreSQL database, one schema per module: `identity`, `customers`, `products`,
`energy`, `communications`, `projects`, `time` and `expenses`. Schemas are hard boundaries:

- **No cross-schema foreign keys or joins.** Reference other modules' data by
  opaque ID only. This is what keeps a future "move this schema to its own
```

**with**:

```markdown
### Database

One PostgreSQL database, one schema per module: `identity`, `customers`, `products`,
`energy`, `communications`, `projects`, `time`, `expenses` and `invoices`. Schemas are hard boundaries:

- **No cross-schema foreign keys or joins.** Reference other modules' data by
  opaque ID only. This is what keeps a future "move this schema to its own
```

**Replace** in `CONTRIBUTING.md`:

```markdown
  each operation's access rule and rate limit from the contract at runtime.
- **Versioned APIs** — Identity lives under `/api/v1/identity`; business modules use
  `/api/v1/customers`, `/api/v1/products`, `/api/v1/energy`,
  `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time` and `/api/v1/expenses`.
- **In-process contracts** — module collaboration uses `internal/contracts`, not
  service-to-service API keys.
- **Form-friendly errors** — validation errors use camelCase JSON field paths.
```

**with**:

```markdown
  each operation's access rule and rate limit from the contract at runtime.
- **Versioned APIs** — Identity lives under `/api/v1/identity`; business modules use
  `/api/v1/customers`, `/api/v1/products`, `/api/v1/energy`,
  `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`, `/api/v1/expenses` and
  `/api/v1/invoices`.
- **In-process contracts** — module collaboration uses `internal/contracts`, not
  service-to-service API keys.
- **Form-friendly errors** — validation errors use camelCase JSON field paths.
```

**Replace** in `CONTRIBUTING.md`:

```markdown
`openapi/<module>.yaml`, and the running server serves the merged contract of the
enabled modules at `GET /api/openapi.json` (session required). Module prefixes are
`/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`, `/api/v1/energy`,
`/api/v1/communications`, `/api/v1/projects`, `/api/v1/time` and `/api/v1/expenses`.
Every other `/api` path answers the catch-all 404 problem.

Errors are RFC 7807 problem responses written by `internal/httpx`; validation errors
carry keys matching the JSON field path:
```

**with**:

```markdown
`openapi/<module>.yaml`, and the running server serves the merged contract of the
enabled modules at `GET /api/openapi.json` (session required). Module prefixes are
`/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`, `/api/v1/energy`,
`/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`, `/api/v1/expenses` and
`/api/v1/invoices`. Every other `/api` path answers the catch-all 404 problem.

Errors are RFC 7807 problem responses written by `internal/httpx`; validation errors
carry keys matching the JSON field path:
```

**Replace** in `CONTRIBUTING.md`:

```markdown
must have been exercised by at least one successful exchange, with no allow-list, so a newly
added operation without a passing test fails the whole package.

### Customers, products, energy, communications, projects, time and expenses

Seven business modules mount on that platform, each serving its own contract and
owning its own schema:

- `internal/customers` → `/api/v1/customers/*` from `openapi/customers.yaml`:
```

**with**:

```markdown
must have been exercised by at least one successful exchange, with no allow-list, so a newly
added operation without a passing test fails the whole package.

### Customers, products, energy, communications, projects, time, expenses and invoices

Eight business modules mount on that platform, each serving its own contract and
owning its own schema:

- `internal/customers` → `/api/v1/customers/*` from `openapi/customers.yaml`:
```

**Replace** in `CONTRIBUTING.md`:

```markdown
  purely optionally: with `projects` off, `GET /meta` answers
  `projectsAvailable: false` and every project-shaped field is refused on its own
  field. See [`docs/expenses.md`](docs/expenses.md).

`MODULES` chooses which of them a deployment serves: a comma-separated list,
parsed once at startup, defaulting to
`customers,products,energy,communications,projects,time,expenses` — every module
this binary can mount. Identity is always mounted and is never
listed. A name the binary does not know fails
startup, naming the name and the known set. `energy`, `communications` and
`projects` read customer data through `contracts.CustomerDirectory`, so any of
them without `customers` fails startup naming both; `time` reads projects through
`contracts.ProjectDirectory`, so `time` without `projects` fails the same way.
`expenses` has no such dependency check — it starts with or without any other
module. A disabled module
contributes no route, no permission and no contract path, and its paths answer
```

**with**:

```markdown
  purely optionally: with `projects` off, `GET /meta` answers
  `projectsAvailable: false` and every project-shaped field is refused on its own
  field. See [`docs/expenses.md`](docs/expenses.md).
- `internal/invoices` → `/api/v1/invoices/*` from `openapi/invoices.yaml`: the sales
  document — the seller record, one gap-free number series, VAT codes with dated
  rates, drafts issued into immutable documents (enforced by database triggers too),
  credit notes, a PDF stored once in the object store, and the invoice journal. It
  consumes `contracts.CustomerDirectory` (customers, **required**) and fills both
  customer slots. See [`docs/invoices.md`](docs/invoices.md).

`MODULES` chooses which of them a deployment serves: a comma-separated list,
parsed once at startup, defaulting to
`customers,products,energy,communications,projects,time,expenses,invoices` — every module
this binary can mount. Identity is always mounted and is never
listed. A name the binary does not know fails
startup, naming the name and the known set. `energy`, `communications` and
`projects` read customer data through `contracts.CustomerDirectory`, so any of
them without `customers` fails startup naming both; `time` reads projects through
`contracts.ProjectDirectory`, so `time` without `projects` fails the same way, and
`invoices` reads its buyer through `contracts.CustomerDirectory`, so `invoices`
without `customers` fails too.
`expenses` has no such dependency check — it starts with or without any other
module. A disabled module
contributes no route, no permission and no contract path, and its paths answer
```

**Replace** in `CONTRIBUTING.md`:

````markdown
bun run --cwd apps/projects/frontend test
bun run --cwd apps/time/frontend test
bun run --cwd apps/expenses/frontend test
```

The SPA is **not** served by the dev server in production: `scripts/build-artifacts.sh`
````

**with**:

````markdown
bun run --cwd apps/projects/frontend test
bun run --cwd apps/time/frontend test
bun run --cwd apps/expenses/frontend test
bun run --cwd apps/invoices/frontend test
```

The SPA is **not** served by the dev server in production: `scripts/build-artifacts.sh`
````

**Replace** in `deploy/compose/README.md`:

```markdown
- **Vantigo** — <http://localhost:8080>
- **API contract** — `GET http://localhost:8080/api/openapi.json` (requires a session)

The Customers, Products, Energy, Communications, Projects, Time and Expenses
modules are enabled by default (`MODULES` in `vantigo.env`). They share one
PostgreSQL database named `vantigo`, with independent `identity`, `customers`,
`products`, `energy`, `communications`, `projects`, `time` and `expenses`
schemas and migration histories.

## First sign-in

```

**with**:

```markdown
- **Vantigo** — <http://localhost:8080>
- **API contract** — `GET http://localhost:8080/api/openapi.json` (requires a session)

The Customers, Products, Energy, Communications, Projects, Time, Expenses and
Invoices modules are enabled by default (`MODULES` in `vantigo.env`). They share
one PostgreSQL database named `vantigo`, with independent `identity`, `customers`,
`products`, `energy`, `communications`, `projects`, `time`, `expenses` and
`invoices` schemas and migration histories.

## First sign-in

```

**Replace** in `deploy/compose/README.md`:

````markdown
docker compose up -d
```

For a complete backup, one-migrator, token rotation, and Owner break-glass runbook,
see [SSO and SCIM operations](../../docs/sso-scim-operations.md).
````

**with**:

````markdown
docker compose up -d
```

**The release with Invoices.** An installation that leaves `MODULES` unset enables
every module this binary knows, so it gets the Invoices app on upgrade. Nobody can use
it until a role grants `invoices:access` (Owner holds every permission already).
Issuing needs an object store (`STORAGE_PROVIDER`): without one the app opens and
every issue answers 503. An installation that lists `MODULES` explicitly gets
Invoices only once `invoices` is added, beside `customers`. See
[Invoices](../../docs/invoices.md).

For a complete backup, one-migrator, token rotation, and Owner break-glass runbook,
see [SSO and SCIM operations](../../docs/sso-scim-operations.md).
````

**Replace** in `deploy/compose/vantigo.env.example`:

```bash
# --- Business modules -----------------------------------------------------------
# Comma list of business modules this deployment enables. identity is always
# mounted and never listed here. Unset enables every module this binary can
# mount: customers, products, energy, communications, projects, time, expenses
# (energy, communications and projects each require customers, and time requires
# projects; projects also uses products when it is enabled, and answers 409 on
# its billing-line endpoints when it is not; expenses requires nothing and reads
# projects only when it happens to be enabled). To run a smaller install, list
# only what you want, for example:
# MODULES=customers,products
MODULES=customers,products,energy,communications,projects,time,expenses

# Whether this container also runs every enabled module's background workers
# (outbox delivery, retention, attachment cleanup) in-process alongside
```

**with**:

```bash
# --- Business modules -----------------------------------------------------------
# Comma list of business modules this deployment enables. identity is always
# mounted and never listed here. Unset enables every module this binary can
# mount: customers, products, energy, communications, projects, time, expenses,
# invoices (energy, communications, projects and invoices each require customers,
# and time requires projects; projects also uses products when it is enabled, and
# answers 409 on its billing-line endpoints when it is not; expenses requires
# nothing and reads projects only when it happens to be enabled). To run a smaller
# install, list only what you want, for example:
# MODULES=customers,products
MODULES=customers,products,energy,communications,projects,time,expenses,invoices

# Whether this container also runs every enabled module's background workers
# (outbox delivery, retention, attachment cleanup) in-process alongside
```

- [ ] **Step 3: Check the docs against the code, and commit**

Every code the Go writes is in `docs/invoices.md`, and every code the docs name is in the Go (the two leftovers are a kind value, a column and an index name):

```bash
grep -ohE '= "[a-z]+(_[a-z]+)+"' apps/server/internal/invoices/*.go | sed 's/= "//;s/"//' | sort -u | while read -r c; do grep -q "\`$c\`" docs/invoices.md || echo "not in the docs: $c"; done
grep -ohE '`[a-z]+(_[a-z]+)+`' docs/invoices.md | tr -d '`' | sort -u | while read -r c; do grep -q "\"$c\"" apps/server/internal/invoices/*.go || echo "not in the code: $c"; done
# expected: "not in the docs: credit_note", "not in the docs: ux_vat_codes_code_lower", "not in the code: customer_id", "not in the code: series_start"
grep -c 'x-vantigo-access' openapi/invoices.yaml   # 18, the rows of the endpoints table
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task9.txt <<'MSG'
docs(invoices): the sales document, and what it changed elsewhere

docs/invoices.md (invoices foundation design D13): the law in one page, the
model, drafts, the issue and its every refusal, credit notes, the stored
PDF, the journal, retention and why anonymisation erases drafts only, the
permissions and the customers:view note, the endpoints, and a plain warning
that this phase meets neither the B2G nor the 2027 B2B e-invoicing duty.
customers.md: disabled means blocked for invoicing; the merge and the
anonymisation tables gain invoices. module-boundaries, ROADMAP (1A done, 1B
next), the docs index, CONTRIBUTING, and the Compose release note that an
installation with MODULES unset gets Invoices on upgrade.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'CONTRIBUTING.md' 'ROADMAP.md' 'deploy/compose/README.md' 'deploy/compose/vantigo.env.example' 'docs/README.md' 'docs/customers.md' 'docs/invoices.md' 'docs/module-boundaries.md'
git commit -F /tmp/claude-1000/msg-invoices-task9.txt -- 'CONTRIBUTING.md' 'ROADMAP.md' 'deploy/compose/README.md' 'deploy/compose/vantigo.env.example' 'docs/README.md' 'docs/customers.md' 'docs/invoices.md' 'docs/module-boundaries.md'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 10: The Invoices app: the list, the draft editor with live totals, the issue dialog and the credit flow (D12)

The list (status and kind chips, customer filter, search, issue-date range, paging, drafts first) with "New invoice" — offered to `canCreate` callers who hold `customers:view`, creating a draft for the picked buyer prefilled with today's delivery and the user's name as "Vår ref." and opening it. The document page: a draft's editor (buyer, delivery day or period with the optional place, references with the "Deres ref." nudge, terms, lines with add, remove and reorder, a VAT code per line from the codes in force today, live totals per rate by D5's exact rule, warnings, Save, Preview, Issue, Delete); the issue dialog offering the server's `allowedIssueDates`; an issued document's page (header, lines, VAT, credit notes, Download PDF, Credit); a credit-note draft linking its original and offering only what D8 allows. The host passes `canViewCustomers` and the display name.

**Files:**
- Create: `apps/host/frontend/src/routes/invoices/$invoiceId.tsx`, `apps/host/frontend/src/routes/invoices/-invoice-access.tsx`, `apps/host/frontend/src/routes/invoices/-invoices-list.test.tsx`, `apps/host/frontend/src/routes/invoices/-invoices-list.tsx`, `apps/invoices/frontend/src/api/customers.ts`, `apps/invoices/frontend/src/api/invoices.ts`, `apps/invoices/frontend/src/components/customer-picker.tsx`, `apps/invoices/frontend/src/components/document-link.tsx`, `apps/invoices/frontend/src/lib/errors.ts`, `apps/invoices/frontend/src/lib/format.ts`, `apps/invoices/frontend/src/lib/money.test.ts`, `apps/invoices/frontend/src/lib/money.ts`, `apps/invoices/frontend/src/lib/routes.ts`, `apps/invoices/frontend/src/pages/-issue-modal.tsx`, `apps/invoices/frontend/src/pages/invoice.test.tsx`, `apps/invoices/frontend/src/pages/invoice.tsx`, `apps/invoices/frontend/src/test/fixtures.ts`, `apps/invoices/frontend/src/test/invoice-route.tsx`, `apps/invoices/frontend/src/test/route-tree.tsx`
- Modify: `apps/host/frontend/src/routes/invoices/index.tsx`, `apps/invoices/frontend/src/i18n.ts`, `apps/invoices/frontend/src/index.ts`, `apps/invoices/frontend/src/pages/invoices.test.tsx`, `apps/invoices/frontend/src/pages/invoices.tsx`
- Generated (commit them; never edit by hand): `apps/host/frontend/src/routeTree.gen.ts`
- Read first (do not change): `apps/expenses/frontend/src/{lib/routes.ts,test/route-tree.tsx,test/claim-route.tsx,pages/my-expenses.tsx}`, `apps/host/frontend/src/routes/expenses/{claims.$claimId.tsx,-my-expenses.tsx,-my-expenses.test.tsx}`, `packages/frontend-api-client/src/index.ts:27-90` (`ApiConflictError.code`/`.problem`)

**Interfaces:**
- Produces TS: `api/invoices.ts` (`invoiceListQueryOptions`, `invoiceQueryOptions`, `createInvoice`, `replaceInvoice`, `deleteInvoice`, `issueInvoice`, `creditInvoice`, `pdfUrl`, `previewUrl`), `api/customers.ts` (`customerSearchQueryOptions`), `lib/money.ts` (`lineAmounts`, `documentTotals` — BigInt, half away from zero), `lib/routes.ts` (`INVOICE_ROUTE_PATH = "/invoices/$invoiceId"`), `lib/errors.ts` (`refusalMessage`, `refusalCode`), `components/{customer-picker,document-link}.tsx`, `pages/{invoices,invoice,-issue-modal}.tsx` (`InvoicesPage {canViewCustomers, userDisplayName?}`, `InvoicePage {invoiceId, canViewCustomers}`); host routes `/invoices/` and `/invoices/$invoiceId` over `useInvoiceAccess()`.

- [ ] **Step 1: The tests: the money against the server's own cases, the list, the editor, the dialog, the credit flow**

**Create** `apps/invoices/frontend/src/lib/money.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { documentTotals, lineAmounts } from "./money";

// The server's own hand-computed cases (drafts_test.go TestDrafts_TheMoney),
// so the editor's live totals and the saved draft cannot disagree.
describe("the editor's money", () => {
  it("reads 0.1 × 3 as exactly 0.30", () => {
    expect(lineAmounts(3, 0.1, 0)).toEqual({ gross: 0.3, allowance: 0, net: 0.3 });
  });

  it("takes a discount as an allowance of the rounded gross", () => {
    expect(lineAmounts(3, 33.33, 10)).toEqual({ gross: 99.99, allowance: 10, net: 89.99 });
  });

  it("computes VAT per rate on the sum of the nets, not per line", () => {
    const third = { net: lineAmounts(1, 33.33, 0).net, category: "S", ratePercent: 25 };
    const totals = documentTotals([third, third, third]);
    // Per line it would be 3 × 8.33 = 24.99.
    expect(totals).toEqual({
      rates: [{ category: "S", ratePercent: 25, taxable: 99.99, vat: 25 }],
      net: 99.99,
      vat: 25,
      gross: 124.99,
    });
  });

  it("gives every rate its row, a 0 % category included, highest rate first", () => {
    const totals = documentTotals([
      { net: 50.5, category: "Z", ratePercent: 0 },
      { net: 200, category: "S", ratePercent: 15 },
      { net: 100.01, category: "S", ratePercent: 25 },
    ]);
    expect(totals.rates.map((r) => [r.category, r.ratePercent, r.taxable, r.vat])).toEqual([
      ["S", 25, 100.01, 25],
      ["S", 15, 200, 30],
      ["Z", 0, 50.5, 0],
    ]);
    expect(totals.gross).toBe(405.51);
  });
});
```

**Create** `apps/invoices/frontend/src/test/fixtures.ts`:

```ts
import type { InvoiceDocument, InvoiceList } from "../api/invoices";
import type { InvoicesMeta } from "../api/meta";

/**
 * GET /meta as the server sends it — a wire literal: a complete seller, the
 * caller may create and issue, and today is 2026-09-12 in Oslo.
 */
export const meta = (overrides: Partial<InvoicesMeta> = {}): InvoicesMeta => ({
  currency: "NOK",
  defaultPaymentTermsDays: 14,
  sellerComplete: true,
  missingSellerFields: [],
  anythingIssued: true,
  seriesStart: 1,
  storageAvailable: true,
  today: "2026-09-12",
  vatCodes: [
    { id: 1, code: "3", name: "Utgående mva 25 %", safTCode: "3", ehfCategory: "S", ratePercent: 25 },
    { id: 2, code: "31", name: "Utgående mva 15 %", safTCode: "31", ehfCategory: "S", ratePercent: 15 },
    {
      id: 5,
      code: "5",
      name: "Fritatt innenlands 0 %",
      safTCode: "5",
      ehfCategory: "Z",
      exemptionReason: "Fritatt for merverdiavgift",
      ratePercent: 0,
    },
  ],
  capabilities: { canCreate: true, canIssue: true, canManage: false },
  ...overrides,
});

/** An invoice draft for Acme with three lines of 33.33 at 25 %, as the server answers it. */
export const draft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => ({
  id: 1001,
  kind: "invoice",
  status: "draft",
  customerId: 2001,
  customerName: "Acme AS",
  deliveryDate: "2026-09-10",
  paymentTermsDays: 30,
  currency: "NOK",
  exchangeRate: 1,
  yourReference: "PO-77",
  ourReference: "Ola Nordmann",
  orderReference: "",
  note: "",
  internalNote: "",
  netTotal: 99.99,
  vatTotal: 25,
  grossTotal: 124.99,
  vatTotalNok: 25,
  lines: [1, 2, 3].map((position) => ({
    id: 5000 + position,
    position,
    description: `Tredjedel ${position}`,
    quantity: 1,
    unit: "timer",
    unitPrice: 33.33,
    discountPercent: 0,
    vatCodeId: 1,
    lineGross: 33.33,
    lineAllowance: 0,
    lineNet: 33.33,
  })),
  vatSummaries: [
    { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 99.99, vatAmount: 25, vatAmountNok: 25 },
  ],
  warnings: [],
  allowedIssueDates: ["2026-09-12"],
  createdAt: "2026-09-12T10:00:00Z",
  updatedAt: "2026-09-12T10:00:00Z",
  revision: 3,
  ...overrides,
});

/** The same invoice, issued as number 1000, nothing credited yet. */
export const issued = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = draft();
  return {
    ...base,
    status: "issued",
    number: 1000,
    issueDate: "2026-09-12",
    dueDate: "2026-10-12",
    exchangeRateDate: "2026-09-12",
    customerName: "Acme Norge AS",
    buyer: {
      customerNumber: 10001,
      type: "business",
      name: "Acme Norge AS",
      organisationNumber: "923609016",
      language: "nb",
    },
    lines: base.lines.map((l) => ({ ...l, vatRatePercent: 25, vatCategory: "S", safTCode: "3" })),
    allowedIssueDates: undefined,
    pdfStored: true,
    creditedAmount: 0,
    uncreditedAmount: 124.99,
    creditNotes: [],
    revision: 4,
    ...overrides,
  };
};

/** A credit-note draft of invoice 1000, crediting its first line. */
export const creditDraft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = draft();
  return {
    ...base,
    id: 1002,
    kind: "credit_note",
    paymentTermsDays: undefined,
    customerName: "Acme Norge AS",
    buyer: {
      customerNumber: 10001,
      type: "business",
      name: "Acme Norge AS",
      organisationNumber: "923609016",
      language: "nb",
    },
    lines: [{ ...base.lines[0], id: 6001, creditsLineId: 5001 }],
    netTotal: 33.33,
    vatTotal: 8.33,
    grossTotal: 41.66,
    vatTotalNok: 8.33,
    vatSummaries: [
      { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 33.33, vatAmount: 8.33, vatAmountNok: 8.33 },
    ],
    credits: { id: 1001, number: 1000, issueDate: "2026-09-12" },
    revision: 1,
    ...overrides,
  };
};

/** A page of the list: a draft, then two issued documents. */
export const listPage = (overrides: Partial<InvoiceList["pagination"]> = {}): InvoiceList => ({
  data: [
    {
      id: 1003,
      kind: "invoice",
      status: "draft",
      customerId: 2002,
      customerName: "Kari Nordmann",
      currency: "NOK",
      grossTotal: 0,
    },
    {
      id: 1002,
      kind: "credit_note",
      status: "issued",
      number: 1001,
      customerId: 2001,
      customerName: "Acme Norge AS",
      issueDate: "2026-09-12",
      currency: "NOK",
      grossTotal: 41.66,
      creditsInvoiceId: 1001,
    },
    {
      id: 1001,
      kind: "invoice",
      status: "issued",
      number: 1000,
      customerId: 2001,
      customerName: "Acme Norge AS",
      issueDate: "2026-09-12",
      dueDate: "2026-10-12",
      currency: "NOK",
      grossTotal: 124.99,
    },
  ],
  pagination: {
    page: 1,
    pageSize: 25,
    totalCount: 3,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
    ...overrides,
  },
});
```

**Create** `apps/invoices/frontend/src/test/invoice-route.tsx`:

```tsx
import { useParams } from "@tanstack/react-router";
import { InvoicePage } from "../pages/invoice";

/**
 * The document page as a route component, the way the host mounts it: the
 * route owns the path parameter and hands the id over as a number.
 */
export const InvoiceRoute = ({ canViewCustomers }: { canViewCustomers: boolean }) => {
  const { invoiceId } = useParams({ strict: false }) as { invoiceId: string };
  return <InvoicePage invoiceId={Number(invoiceId)} canViewCustomers={canViewCustomers} />;
};
```

**Create** `apps/invoices/frontend/src/test/route-tree.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import "../i18n";
import { INVOICE_ROUTE_PATH } from "../lib/routes";
import { InvoicesPage } from "../pages/invoices";
import { InvoiceRoute } from "./invoice-route";

/**
 * Stands in for the host routes this package's pages are mounted by: the
 * same paths, so a page test drives a real router and reads the URL back
 * instead of mocking navigation.
 */
const makeRouteTree = (canViewCustomers: boolean) => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/invoices",
      component: () => <InvoicesPage canViewCustomers={canViewCustomers} userDisplayName="Ola Nordmann" />,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: INVOICE_ROUTE_PATH,
      component: () => <InvoiceRoute canViewCustomers={canViewCustomers} />,
    }),
  ]);
};

/** Mounts the stand-in routes at a URL under the providers the host gives the pages. */
export const renderRoute = (url: string, { canViewCustomers = true }: { canViewCustomers?: boolean } = {}) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(canViewCustomers),
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { router, queryClient };
};
```

**Replace the whole of** `apps/invoices/frontend/src/pages/invoices.test.tsx` with:

```tsx
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse, problemResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { draft, listPage, meta } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

/** The fetch fake: meta, the list (paged by its query), the customers list and a create. */
const server = (options: { canCreate?: boolean; totalPages?: number } = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(
        200,
        meta({ capabilities: { canCreate: options.canCreate ?? true, canIssue: true, canManage: false } }),
      );
    }
    if (url.startsWith("/api/v1/invoices?") || url === "/api/v1/invoices") {
      if (method === "POST") return jsonResponse(201, draft({ id: 1010, lines: [] }));
      return jsonResponse(200, listPage({ totalPages: options.totalPages ?? 1 }));
    }
    if (url.startsWith("/api/v1/customers?")) {
      return jsonResponse(200, { data: [{ id: 2001, name: "Acme AS", customerNumber: 10001, status: "active" }] });
    }
    if (url === "/api/v1/invoices/1010") return jsonResponse(200, draft({ id: 1010, lines: [] }));
    return new Response(null, { status: 404 });
  });

describe("the invoice list", () => {
  it("shows drafts first and names each document's customer", async () => {
    server();
    renderRoute("/invoices");

    const rows = await screen.findAllByRole("row");
    expect(within(rows[1]).getByText("Kari Nordmann")).toBeInTheDocument();
    expect(within(rows[1]).getAllByText("Draft").length).toBeGreaterThan(0);
    expect(within(rows[2]).getByText("1001")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Credit note")).toBeInTheDocument();
    expect(screen.getByText("3 documents")).toBeInTheDocument();
  });

  it("asks the server for the chosen status and the next page", async () => {
    const fetchMock = server({ totalPages: 2 });
    renderRoute("/invoices");
    await screen.findByText("Kari Nordmann");

    await userEvent.click(screen.getByRole("radio", { name: "Draft" }));
    await waitFor(() => expect(fetchMock.actualCalls.some(([url]) => path(url).includes("status=draft"))).toBe(true));
    await userEvent.click(await screen.findByRole("button", { name: "2" }));
    await waitFor(() => expect(fetchMock.actualCalls.some(([url]) => path(url).includes("page=2"))).toBe(true));
  });

  it("offers New invoice only to a caller who may create drafts and pick a buyer", async () => {
    server();
    renderRoute("/invoices", { canViewCustomers: false });
    await screen.findByText("Kari Nordmann");
    expect(screen.queryByRole("button", { name: "New invoice" })).not.toBeInTheDocument();
  });

  it("creates a draft for the picked customer, prefilled with today and our reference, and opens it", async () => {
    const fetchMock = server();
    const { router } = renderRoute("/invoices");

    await userEvent.click(await screen.findByRole("button", { name: "New invoice" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Customer" }));
    await userEvent.click(await screen.findByRole("option", { name: "Acme AS (10001)" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Create the draft" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1010"));
    expect(sent(fetchMock, "POST").body).toEqual({
      customerId: 2001,
      lines: [],
      deliveryDate: "2026-09-12",
      ourReference: "Ola Nordmann",
    });
  });

  it("says when the list could not be loaded", async () => {
    stubFetch((input: RequestInfo | URL) =>
      path(input) === "/api/v1/invoices/meta"
        ? jsonResponse(200, meta())
        : problemResponse(500, "Internal Server Error"),
    );
    renderRoute("/invoices");
    expect(await screen.findByText("Could not load the invoices")).toBeInTheDocument();
  });
});
```

**Create** `apps/invoices/frontend/src/pages/invoice.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceDocument } from "../api/invoices";
import { jsonResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { creditDraft, draft, issued, meta } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

/** The fetch fake over a set of documents by id, answering what each write answers. */
const server = (documents: Record<number, InvoiceDocument>, answers: Record<string, InvoiceDocument> = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    const answer = answers[`${method} ${url}`];
    if (answer) return jsonResponse(method === "POST" && url.endsWith("/credit") ? 201 : 200, answer);
    const match = /^\/api\/v1\/invoices\/(\d+)$/.exec(url);
    if (match && method === "GET" && documents[Number(match[1])]) return jsonResponse(200, documents[Number(match[1])]);
    if (url.startsWith("/api/v1/customers?")) return jsonResponse(200, { data: [] });
    return new Response(null, { status: 404 });
  });

describe("the draft editor", () => {
  it("totals VAT per rate on the sum of the nets, live, the server's way to the øre", async () => {
    server({ 1001: draft() });
    renderRoute("/invoices/1001");

    // Three lines of 33.33 at 25 %: per line 3 × 8.33 = 24.99, per rate 25.00.
    expect(await screen.findByTestId("vat-total")).toHaveTextContent("25.00");
    expect(screen.getByTestId("gross-total")).toHaveTextContent("124.99");

    const quantity = screen.getByRole("textbox", { name: "Line 1 quantity" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "2");
    // 2 × 33.33 + 2 × 33.33 = 133.32, VAT round(33.33) = 33.33.
    expect(screen.getByTestId("net-total")).toHaveTextContent("133.32");
    expect(screen.getByTestId("vat-total")).toHaveTextContent("33.33");
  });

  it("saves the whole draft with its revision", async () => {
    const saved = draft({ revision: 4 });
    const fetchMock = server({ 1001: draft() }, { "PUT /api/v1/invoices/1001": saved });
    renderRoute("/invoices/1001");

    const description = await screen.findByRole("textbox", { name: "Line 2 description" });
    await userEvent.clear(description);
    await userEvent.type(description, "Rådgivning");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/1001"));
    const body = sent(fetchMock, "PUT").body;
    expect(body.revision).toBe(3);
    expect(body.paymentTermsDays).toBe(30);
    expect(body.deliveryDate).toBe("2026-09-10");
    expect(body.lines[1]).toEqual({
      description: "Rådgivning",
      quantity: 1,
      unit: "timer",
      unitPrice: 33.33,
      discountPercent: 0,
      vatCodeId: 1,
    });
  });

  it("nudges when the buyer's reference is empty and shows the draft's warnings", async () => {
    server({ 1001: draft({ yourReference: "", warnings: ["issued_late"] }) });
    renderRoute("/invoices/1001");

    expect(await screen.findByText(/an e-invoice \(EHF\) will need the buyer's reference/)).toBeInTheDocument();
    expect(screen.getByText(/the law asks for the invoice within a month/)).toBeInTheDocument();
  });
});

describe("the issue dialog", () => {
  it("offers the last day of the previous month when the server allows it, and issues with the chosen date", async () => {
    const answer = issued({ warnings: ["issued_late"] });
    const fetchMock = server(
      { 1001: draft({ allowedIssueDates: ["2026-08-31", "2026-09-12"] }) },
      { "POST /api/v1/invoices/1001/issue": answer },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("This assigns the next number and cannot be undone.")).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Sep 12, 2026" })).toBeChecked();
    await userEvent.click(within(dialog).getByRole("radio", { name: "Aug 31, 2026" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Issue" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toEqual({ issueDate: "2026-08-31" }));
    expect(await screen.findByText("Issued as number 1000")).toBeInTheDocument();
    expect(await screen.findByText(/the law asks for the invoice within a month/)).toBeInTheDocument();
  });

  it("offers today alone after the 15th", async () => {
    server({ 1001: draft({ allowedIssueDates: ["2026-09-16"] }) });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("radio")).not.toBeInTheDocument();
    expect(within(dialog).getByText("It is issued today, Sep 16, 2026.")).toBeInTheDocument();
  });
});

describe("an issued document", () => {
  it("downloads its PDF, says when it is not stored yet, and credits into a new draft", async () => {
    const fetchMock = server(
      { 1001: issued({ pdfStored: false }), 1002: creditDraft() },
      { "POST /api/v1/invoices/1001/credit": creditDraft() },
    );
    const { router } = renderRoute("/invoices/1001");

    expect(await screen.findByRole("heading", { name: "Invoice 1000" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Download PDF" })).toHaveAttribute("href", "/api/v1/invoices/1001/pdf");
    expect(screen.getByText(/It is stored the first time it is downloaded/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Credit" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1002"));
    expect(
      fetchMock.actualCalls.some(
        ([url, init]) => path(url) === "/api/v1/invoices/1001/credit" && init?.method === "POST",
      ),
    ).toBe(true);
  });

  it("offers no credit for an invoice credited in full", async () => {
    server({ 1001: issued({ uncreditedAmount: 0, creditedAmount: 124.99 }) });
    renderRoute("/invoices/1001");
    await screen.findByRole("heading", { name: "Invoice 1000" });
    expect(screen.queryByRole("button", { name: "Credit" })).not.toBeInTheDocument();
  });
});

describe("a credit-note draft", () => {
  it("names its original, adds no lines, keeps the VAT codes, and shows the cap warnings", async () => {
    server({ 1002: creditDraft({ warnings: ["credit_exceeds_line"] }), 1001: issued() });
    renderRoute("/invoices/1002");

    expect(await screen.findByText("Credit note for invoice")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "1000" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a line" })).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toBeDisabled();
    expect(screen.getByText(/credits more than the original line had left/)).toBeInTheDocument();
    // At the original line's own 25 %.
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("8.33"));
  });
});
```

```bash
mise exec -- bun run --cwd apps/invoices/frontend test   # FAIL: the modules they import do not exist yet
```

- [ ] **Step 2: The package: the API, the money, the pages and the words**

**Create** `apps/invoices/frontend/src/api/customers.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import { INVOICES_QUERY_KEY, request } from "./request";

/**
 * A customer as the buyer picker needs it: the customers module's own list,
 * read over HTTP because `contracts.CustomerDirectory` has no search (D1). It
 * needs `customers:view`, so the picker is only offered to a caller who holds
 * it; the invoices API itself takes a customer id and asks nothing more.
 *
 * The shape is typed here rather than generated: this package owns only the
 * invoices contract, and these four fields are all it reads of the answer.
 */
export interface CustomerOption {
  id: number;
  name: string;
  customerNumber: number;
  status: string;
}

interface CustomerListAnswer {
  data: CustomerOption[];
}

export const customerSearchQueryOptions = (search: string) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "customers", search],
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ pageSize: "20", status: "active" });
      if (search.trim()) params.set("search", search.trim());
      const answer = await request<CustomerListAnswer>(`/api/v1/customers?${params.toString()}`, { signal });
      return answer.data;
    },
  });
```

**Create** `apps/invoices/frontend/src/api/invoices.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** One invoice or credit note as the server answers it (D4). */
export type InvoiceDocument = Schemas["InvoicesInvoiceResponse"];
export type InvoiceLine = Schemas["InvoicesLine"];
export type InvoiceVatSummary = Schemas["InvoicesVatSummary"];
export type InvoiceListItem = Schemas["InvoicesInvoiceListItem"];
export type InvoiceList = Schemas["PaginatedResponseOfInvoicesInvoiceListItem"];
export type InvoiceInput = Schemas["InvoicesInvoiceRequest"];
export type InvoiceLineInput = Schemas["InvoicesLineRequest"];
export type DeliveryAddress = Schemas["InvoicesDeliveryAddress"];
/** A refusal's body: the rule's code, and the dates or the line it names. */
export type InvoicesConflict = Schemas["InvoicesConflictProblem"];

/** The list's filters (D4). Every one is optional; paging is page/pageSize. */
export interface InvoiceListFilters {
  status?: "draft" | "issued";
  kind?: "invoice" | "credit_note";
  customerId?: number;
  search?: string;
  from?: string;
  to?: string;
  page?: number;
  pageSize?: number;
}

const listQuery = (filters: InvoiceListFilters): string => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const query = params.toString();
  return query ? `?${query}` : "";
};

export const invoiceListQueryOptions = (filters: InvoiceListFilters) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "list", filters],
    queryFn: ({ signal }) => request<InvoiceList>(`/api/v1/invoices${listQuery(filters)}`, { signal }),
  });

export const invoiceQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "document", id],
    queryFn: ({ signal }) => request<InvoiceDocument>(`/api/v1/invoices/${id}`, { signal }),
  });

export const createInvoice = (input: InvoiceInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>("/api/v1/invoices", json("POST", input));

/** A full replace with the revision the draft was read at. */
export const replaceInvoice = (id: number, input: InvoiceInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}`, json("PUT", input));

export const deleteInvoice = (id: number): Promise<void> =>
  request<void>(`/api/v1/invoices/${id}`, { method: "DELETE" });

/** Issues a draft; an omitted date is today in Oslo (D6). */
export const issueInvoice = (id: number, issueDate?: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/issue`, json("POST", issueDate ? { issueDate } : {}));

/** Creates a credit-note draft of an issued invoice (D8). */
export const creditInvoice = (id: number): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/credit`, { method: "POST" });

/** Where an issued document's stored PDF downloads from. */
export const pdfUrl = (id: number): string => appUrl(`/api/v1/invoices/${id}/pdf`);

/** Where a draft's watermarked preview renders. */
export const previewUrl = (id: number): string => appUrl(`/api/v1/invoices/${id}/preview.pdf`);
```

**Create** `apps/invoices/frontend/src/lib/errors.ts`:

```ts
import { ApiValidationError } from "../api/request";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string) => string;

/**
 * The refusal's code, when the error is one of this module's 409s or its 503
 * (D2-D8). The shared client puts a problem's top-level `code` on the error
 * itself, a conflict and a generic error alike.
 */
export const refusalCode = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** A 409's own fields — the dates, the line — from its parsed body. */
export const refusalProblem = (error: unknown): Record<string, unknown> =>
  ((error as { problem?: Record<string, unknown> } | null)?.problem ?? {}) as Record<string, unknown>;

/**
 * What a refusal says to a person. A coded refusal is worded by its code, in
 * the reader's language, never the server's English detail; a validation
 * error says its first field's message; anything else, the error's own.
 */
export const refusalMessage = (error: unknown, t: Translate): string => {
  const code = refusalCode(error);
  if (code) {
    const key = `refusal.${code}`;
    const text = t(key);
    if (text !== key) return text;
  }
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.errors)[0];
    if (first?.[0]) return first[0];
  }
  return error instanceof Error ? error.message : String(error);
};
```

**Create** `apps/invoices/frontend/src/lib/format.ts`:

```ts
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/** A calendar date read and written in UTC, so a day never slides by one. */
const toUtc = (date: string): Date => new Date(`${date}T00:00:00.000Z`);

/**
 * The one way this package writes money, numbers and dates. Money is written
 * in the document's own currency; a calendar date in UTC.
 */
export const useInvoiceFormat = () => {
  const { t, formatters } = useI18n("invoices");
  return {
    t,
    money: (amount: number, currency: string) => formatters.formatCurrency(amount, currency),
    number: (value: number, maxDecimals = 3) => formatters.formatNumber(value, { maximumFractionDigits: maxDecimals }),
    date: (date: string) => formatters.formatDate(toUtc(date), { dateStyle: "medium", timeZone: "UTC" }),
  };
};
```

**Create** `apps/invoices/frontend/src/lib/money.ts`:

```ts
/**
 * D5's arithmetic in the browser, exact: every amount is scaled to an integer
 * (BigInt) at its column's decimals, multiplied, and rounded once to øre with
 * the half away from zero — the server's rule — so the live totals the editor
 * shows are the ones the server will compute, to the øre. The server stays
 * authoritative; this only spares the person a round trip per keystroke.
 */

/** v at `places` decimals as an integer: 12.5 at 2 is 1250n. */
export const scaled = (v: number, places: number): bigint => {
  if (!Number.isFinite(v)) return 0n;
  return BigInt(v.toFixed(places).replace(".", ""));
};

/** n / d rounded to the nearest integer, the half away from zero. */
const divRound = (n: bigint, d: bigint): bigint => {
  const q = n / d;
  const r = n % d;
  const twice = 2n * (r < 0n ? -r : r);
  if (twice >= d) return q + (n < 0n ? -1n : 1n);
  return q;
};

const fromOre = (ore: bigint): number => Number(ore) / 100;

export interface LineAmounts {
  gross: number;
  allowance: number;
  net: number;
}

/** A line's gross, its discount as an allowance of the rounded gross, and its net. */
export const lineAmounts = (quantity: number, unitPrice: number, discountPercent: number): LineAmounts => {
  const gross = divRound(scaled(quantity, 3) * scaled(unitPrice, 4), 100000n);
  const allowance = divRound(gross * scaled(discountPercent, 2), 10000n);
  return { gross: fromOre(gross), allowance: fromOre(allowance), net: fromOre(gross - allowance) };
};

export interface TaxedLine {
  net: number;
  category: string;
  ratePercent: number;
}

export interface RateTotal {
  category: string;
  ratePercent: number;
  taxable: number;
  vat: number;
}

export interface DocumentTotals {
  rates: RateTotal[];
  net: number;
  vat: number;
  gross: number;
}

/**
 * VAT per (category, rate) on the sum of the lines' nets (Peppol BR-CO-17),
 * never per line; the rows highest rate first, then by category — the order
 * the server answers them in.
 */
export const documentTotals = (lines: TaxedLine[]): DocumentTotals => {
  const groups = new Map<string, { category: string; ratePercent: number; taxable: bigint }>();
  for (const line of lines) {
    const key = `${line.category}|${line.ratePercent.toFixed(2)}`;
    const group = groups.get(key) ?? { category: line.category, ratePercent: line.ratePercent, taxable: 0n };
    group.taxable += scaled(line.net, 2);
    groups.set(key, group);
  }
  let net = 0n;
  let vat = 0n;
  const rates: RateTotal[] = [];
  for (const group of groups.values()) {
    const groupVat = divRound(group.taxable * scaled(group.ratePercent, 2), 10000n);
    net += group.taxable;
    vat += groupVat;
    rates.push({
      category: group.category,
      ratePercent: group.ratePercent,
      taxable: fromOre(group.taxable),
      vat: fromOre(groupVat),
    });
  }
  rates.sort((a, b) => b.ratePercent - a.ratePercent || a.category.localeCompare(b.category));
  return { rates, net: fromOre(net), vat: fromOre(vat), gross: fromOre(net + vat) };
};
```

**Create** `apps/invoices/frontend/src/lib/routes.ts`:

```ts
/**
 * The one path a document lives at. The host owns the routes, but the list
 * links to a document and the create and credit flows navigate to the new
 * draft, so the path is written once, here, and the host's route file and this
 * package's test route tree are both built from it.
 */
export const INVOICE_ROUTE_PATH = "/invoices/$invoiceId";

/** What `navigate` and `Link` take to reach one document. */
export const invoiceLinkOptions = (invoiceId: number) => ({
  to: INVOICE_ROUTE_PATH,
  params: { invoiceId: String(invoiceId) },
});

/** The same path as a plain URL, for a link's href. */
export const invoiceHref = (invoiceId: number): string => INVOICE_ROUTE_PATH.replace("$invoiceId", String(invoiceId));
```

**Create** `apps/invoices/frontend/src/components/customer-picker.tsx`:

```tsx
import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { customerSearchQueryOptions } from "../api/customers";
import "../i18n";

export interface CustomerPickerProps {
  value: number | null;
  onChange: (customerId: number | null) => void;
  /** The chosen customer's name, when the draft already has one, so it shows before any search. */
  selectedName?: string;
  label?: string;
  error?: string;
  required?: boolean;
}

/**
 * The buyer picker (D1, D12): the customers module's own list, searched as the
 * person types. Only a caller holding `customers:view` is offered it — the
 * page decides that; the picker assumes it.
 */
export const CustomerPicker = ({ value, onChange, selectedName, label, error, required }: CustomerPickerProps) => {
  const { t } = useI18n("invoices");
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const customers = useQuery(customerSearchQueryOptions(debounced));
  const data = (customers.data ?? []).map((c) => ({ value: String(c.id), label: `${c.name} (${c.customerNumber})` }));
  if (value !== null && selectedName && !data.some((d) => d.value === String(value))) {
    data.unshift({ value: String(value), label: selectedName });
  }
  return (
    <Select
      label={label ?? t("customer")}
      placeholder={t("searchCustomers")}
      searchable
      clearable
      required={required}
      data={data}
      value={value === null ? null : String(value)}
      searchValue={search}
      onSearchChange={setSearch}
      onChange={(next) => onChange(next === null ? null : Number(next))}
      nothingFoundMessage={customers.isFetching ? t("searching") : t("noCustomersFound")}
      filter={({ options }) => options}
      error={error}
    />
  );
};
```

**Create** `apps/invoices/frontend/src/components/document-link.tsx`:

```tsx
import { Anchor } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { invoiceHref, invoiceLinkOptions } from "../lib/routes";

/**
 * A link to one document: a real href, so it opens in a new tab, and the
 * router's own navigation on a plain click.
 */
export const DocumentLink = ({ invoiceId, children }: { invoiceId: number; children: ReactNode }) => {
  const navigate = useNavigate() as (options: unknown) => void;
  return (
    <Anchor
      href={appUrl(invoiceHref(invoiceId))}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
        event.preventDefault();
        navigate(invoiceLinkOptions(invoiceId));
      }}
    >
      {children}
    </Anchor>
  );
};
```

**Create** `apps/invoices/frontend/src/pages/-issue-modal.tsx`:

```tsx
import { Alert, Button, Group, Modal, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type InvoiceDocument, issueInvoice } from "../api/invoices";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface IssueModalProps {
  draft: InvoiceDocument;
  onClose: () => void;
}

/**
 * The issue dialog (D12): it says what issuing does, and offers the dates the
 * server allows this draft today — today, and the last day of the previous
 * month only while D6 allows it. The server answers `allowedIssueDates`; this
 * dialog never re-derives the rule.
 */
export const IssueModal = ({ draft, onClose }: IssueModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const allowed = draft.allowedIssueDates ?? [];
  const [issueDate, setIssueDate] = useState(allowed[allowed.length - 1] ?? "");
  const issue = useMutation({
    mutationFn: () => issueInvoice(draft.id, issueDate || undefined),
    onSuccess: async (issued) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("issuedAs", { number: issued.number }) });
      if (issued.warnings.includes("issued_late")) {
        notifications.show({ color: "yellow", title: t("warning.issued_late"), message: t("issuedLateAfter") });
      }
      onClose();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotIssue"), message: refusalMessage(error, t) }),
  });
  return (
    <Modal opened onClose={onClose} title={draft.kind === "credit_note" ? t("issueCreditNote") : t("issueInvoice")}>
      <Stack>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("issueCannotBeUndone")}
        </Alert>
        {allowed.length > 1 ? (
          <Radio.Group label={t("issueDate")} value={issueDate} onChange={setIssueDate}>
            <Stack gap="xs" mt="xs">
              {allowed.map((d) => (
                <Radio key={d} value={d} label={date(d)} />
              ))}
            </Stack>
          </Radio.Group>
        ) : (
          <Text>{t("issuedToday", { date: allowed[0] ? date(allowed[0]) : "" })}</Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={issue.isPending} onClick={() => issue.mutate()}>
            {t("issue")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
```

**Create** `apps/invoices/frontend/src/pages/invoice.tsx`:

```tsx
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  NumberInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconArrowDown,
  IconArrowUp,
  IconDownload,
  IconEye,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  creditInvoice,
  deleteInvoice,
  type InvoiceDocument,
  type InvoiceInput,
  invoiceQueryOptions,
  pdfUrl,
  previewUrl,
  replaceInvoice,
} from "../api/invoices";
import { invoicesMetaQueryOptions, type VatCodeInForce } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import { CustomerPicker } from "../components/customer-picker";
import { DocumentLink } from "../components/document-link";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { documentTotals, lineAmounts } from "../lib/money";
import { invoiceLinkOptions } from "../lib/routes";
import { IssueModal } from "./-issue-modal";

export interface InvoicePageProps {
  invoiceId: number;
  /** Whether the caller holds `customers:view`, which changing the buyer needs (D1). */
  canViewCustomers: boolean;
}

/**
 * One document (D12): a draft's editor, or an issued document's page. The
 * route owns the id and hands it over as a number.
 */
export const InvoicePage = ({ invoiceId, canViewCustomers }: InvoicePageProps) => {
  const { t } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const document = useQuery(invoiceQueryOptions(invoiceId));
  if (document.isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoice")}>
        {refusalMessage(document.error, t)}
      </Alert>
    );
  }
  if (!document.data || !meta.data) return <ContentSkeleton rows={6} rowHeight={48} />;
  return document.data.status === "draft" ? (
    <DraftEditor
      key={document.data.revision}
      draft={document.data}
      vatCodes={meta.data.vatCodes}
      canCreate={meta.data.capabilities.canCreate}
      canIssue={meta.data.capabilities.canIssue}
      canViewCustomers={canViewCustomers}
    />
  ) : (
    <IssuedDocument document={document.data} canIssue={meta.data.capabilities.canIssue} />
  );
};

/** The heading a document shows: its kind and its number, or "Draft". */
const useHeading = (doc: InvoiceDocument) => {
  const { t } = useInvoiceFormat();
  const kind = doc.kind === "credit_note" ? t("kindCreditNote") : t("kindInvoice");
  return doc.number ? `${kind} ${doc.number}` : `${kind} — ${t("statusDraft")}`;
};

/** A credit note's link to its original: "Credit note for invoice N". */
const CreditsLink = ({ doc }: { doc: InvoiceDocument }) => {
  const { t } = useInvoiceFormat();
  if (!doc.credits) return null;
  return (
    <Text>
      {t("creditsInvoice")} <DocumentLink invoiceId={doc.credits.id}>{doc.credits.number}</DocumentLink>
    </Text>
  );
};

/** A line as the editor holds it. */
interface EditorLine {
  key: string;
  description: string;
  quantity: number | string;
  unit: string;
  unitPrice: number | string;
  discountPercent: number | string;
  vatCodeId: number | null;
  creditsLineId?: number;
}

const numberOf = (v: number | string): number => {
  if (typeof v === "number") return v;
  const parsed = Number(v.replace(",", ".").trim());
  return Number.isFinite(parsed) ? parsed : 0;
};

let lineKeys = 0;
const nextKey = () => `line-${++lineKeys}`;

interface DraftEditorProps {
  draft: InvoiceDocument;
  vatCodes: VatCodeInForce[];
  canCreate: boolean;
  canIssue: boolean;
  canViewCustomers: boolean;
}

/**
 * A draft's editor (D12): the buyer, the delivery — required before issue —
 * with an optional place of delivery, the references with a nudge when
 * "Deres ref." is empty, the terms, the lines with a VAT code each from the
 * codes in force today, live totals per rate by D5's rule, the draft's
 * warnings, and Save, Preview, Issue and Delete. A credit-note draft offers
 * only what D8 allows: removing lines and lowering quantities and prices.
 */
const DraftEditor = ({ draft, vatCodes, canCreate, canIssue, canViewCustomers }: DraftEditorProps) => {
  const { t, money } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const heading = useHeading(draft);
  const credit = draft.kind === "credit_note";
  const original = useQuery({
    ...invoiceQueryOptions(draft.credits?.id ?? 0),
    enabled: credit && Boolean(draft.credits),
  });

  const [customerId, setCustomerId] = useState<number | null>(draft.customerId);
  const [deliveryMode, setDeliveryMode] = useState<"date" | "period">(draft.deliveryFrom ? "period" : "date");
  const [deliveryDate, setDeliveryDate] = useState<string | null>(draft.deliveryDate ?? null);
  const [deliveryFrom, setDeliveryFrom] = useState<string | null>(draft.deliveryFrom ?? null);
  const [deliveryTo, setDeliveryTo] = useState<string | null>(draft.deliveryTo ?? null);
  const [elsewhere, setElsewhere] = useState(Boolean(draft.deliveryAddress));
  const [address, setAddress] = useState({
    line1: draft.deliveryAddress?.line1 ?? "",
    line2: draft.deliveryAddress?.line2 ?? "",
    postalCode: draft.deliveryAddress?.postalCode ?? "",
    city: draft.deliveryAddress?.city ?? "",
    country: draft.deliveryAddress?.country ?? "NO",
  });
  const [yourReference, setYourReference] = useState(draft.yourReference);
  const [ourReference, setOurReference] = useState(draft.ourReference);
  const [orderReference, setOrderReference] = useState(draft.orderReference);
  const [terms, setTerms] = useState<number | string>(draft.paymentTermsDays ?? "");
  const [note, setNote] = useState(draft.note);
  const [internalNote, setInternalNote] = useState(draft.internalNote);
  const [lines, setLines] = useState<EditorLine[]>(
    draft.lines.map((l) => ({
      key: nextKey(),
      description: l.description,
      quantity: l.quantity,
      unit: l.unit,
      unitPrice: l.unitPrice,
      discountPercent: l.discountPercent,
      vatCodeId: l.vatCodeId,
      creditsLineId: l.creditsLineId,
    })),
  );
  const [issuing, setIssuing] = useState(false);
  const [dirty, setDirty] = useState(false);
  const touch =
    <T,>(setter: (v: T) => void) =>
    (v: T) => {
      setter(v);
      setDirty(true);
    };
  const setLine = (key: string, change: Partial<EditorLine>) => {
    setLines((current) => current.map((l) => (l.key === key ? { ...l, ...change } : l)));
    setDirty(true);
  };
  const move = (index: number, by: number) => {
    setLines((current) => {
      const next = [...current];
      const [line] = next.splice(index, 1);
      next.splice(index + by, 0, line);
      return next;
    });
    setDirty(true);
  };

  // Live totals by D5's rule: an invoice's lines at today's rates, a credit
  // note's at its original lines' own.
  const rateOf = (line: EditorLine): { category: string; ratePercent: number } => {
    if (credit) {
      const o = original.data?.lines.find((ol) => ol.id === line.creditsLineId);
      return { category: o?.vatCategory ?? "", ratePercent: o?.vatRatePercent ?? 0 };
    }
    const code = vatCodes.find((c) => c.id === line.vatCodeId);
    return { category: code?.ehfCategory ?? "", ratePercent: code?.ratePercent ?? 0 };
  };
  const amounts = lines.map((l) =>
    lineAmounts(numberOf(l.quantity), numberOf(l.unitPrice), numberOf(l.discountPercent)),
  );
  const totals = documentTotals(lines.map((l, i) => ({ net: amounts[i].net, ...rateOf(l) })));

  const input = (): InvoiceInput => ({
    customerId: customerId ?? draft.customerId,
    revision: draft.revision,
    ...(deliveryMode === "date" && deliveryDate ? { deliveryDate } : {}),
    ...(deliveryMode === "period" && deliveryFrom && deliveryTo ? { deliveryFrom, deliveryTo } : {}),
    ...(elsewhere
      ? {
          deliveryAddress: {
            line1: address.line1,
            line2: address.line2 || undefined,
            postalCode: address.postalCode || undefined,
            city: address.city,
            country: address.country,
          },
        }
      : {}),
    yourReference,
    ourReference,
    orderReference,
    ...(credit || terms === "" ? {} : { paymentTermsDays: numberOf(terms) }),
    note,
    internalNote,
    lines: lines.map((l) => ({
      description: l.description,
      quantity: numberOf(l.quantity),
      unit: l.unit,
      unitPrice: numberOf(l.unitPrice),
      discountPercent: numberOf(l.discountPercent),
      vatCodeId: l.vatCodeId ?? 0,
      ...(l.creditsLineId ? { creditsLineId: l.creditsLineId } : {}),
    })),
  });

  const save = useMutation({
    mutationFn: () => replaceInvoice(draft.id, input()),
    onSuccess: async (saved) => {
      queryClient.setQueryData(invoiceQueryOptions(draft.id).queryKey, saved);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "list"] });
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, t) }),
  });
  const remove = useMutation({
    mutationFn: () => deleteInvoice(draft.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      navigate({ to: "/invoices" });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDelete"), message: refusalMessage(error, t) }),
  });
  const confirmDelete = () =>
    modals.openConfirmModal({
      title: t("deleteDraftTitle"),
      children: <Text size="sm">{t("deleteDraftBody")}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  const vatOptions = vatCodes.map((c) => ({ value: String(c.id), label: `${c.code} — ${c.name}` }));
  const editable = canCreate;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("invoices"), to: "/invoices" }, { label: heading }]}
        title={heading}
        actions={
          <Group>
            {canCreate && (
              <Button
                component="a"
                href={previewUrl(draft.id)}
                target="_blank"
                variant="default"
                leftSection={<IconEye size={16} />}
              >
                {t("preview")}
              </Button>
            )}
            {canCreate && (
              <Button variant="default" color="red" leftSection={<IconTrash size={16} />} onClick={confirmDelete}>
                {t("delete")}
              </Button>
            )}
            {canCreate && (
              <Button variant="default" loading={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>
                {t("save")}
              </Button>
            )}
            {canIssue && (
              <Button disabled={dirty} onClick={() => setIssuing(true)}>
                {t("issue")}
              </Button>
            )}
          </Group>
        }
      />
      <CreditsLink doc={draft} />
      {dirty && canIssue && (
        <Text size="sm" c="dimmed">
          {t("saveBeforeIssue")}
        </Text>
      )}
      {draft.warnings.length > 0 && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("warnings")}>
          <Stack gap={4}>
            {draft.warnings.map((w) => (
              <Text key={w} size="sm">
                {t(`warning.${w}`)}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}
      <Card withBorder>
        <Stack>
          {credit || !canViewCustomers ? (
            <Text>
              <Text span fw={600}>
                {t("customer")}:
              </Text>{" "}
              {draft.customerName ?? t("unknownCustomer")}
            </Text>
          ) : (
            <CustomerPicker
              value={customerId}
              onChange={touch(setCustomerId)}
              selectedName={draft.customerName}
              required
            />
          )}
          <Group align="flex-end">
            <SegmentedControl
              aria-label={t("delivery")}
              disabled={credit || !editable}
              value={deliveryMode}
              onChange={(v) => touch(setDeliveryMode)(v as "date" | "period")}
              data={[
                { value: "date", label: t("deliveryDay") },
                { value: "period", label: t("deliveryPeriod") },
              ]}
            />
            {deliveryMode === "date" ? (
              <DateInput
                label={t("deliveryDate")}
                valueFormat={t("dateInputFormat")}
                disabled={credit}
                value={deliveryDate}
                onChange={touch(setDeliveryDate)}
              />
            ) : (
              <>
                <DateInput
                  label={t("deliveryFrom")}
                  valueFormat={t("dateInputFormat")}
                  disabled={credit}
                  value={deliveryFrom}
                  onChange={touch(setDeliveryFrom)}
                />
                <DateInput
                  label={t("deliveryTo")}
                  valueFormat={t("dateInputFormat")}
                  disabled={credit}
                  value={deliveryTo}
                  onChange={touch(setDeliveryTo)}
                />
              </>
            )}
          </Group>
          {!deliveryDate && !(deliveryFrom && deliveryTo) && (
            <Text size="sm" c="orange">
              {t("deliveryRequiredToIssue")}
            </Text>
          )}
          <Checkbox
            label={t("deliverElsewhere")}
            disabled={credit}
            checked={elsewhere}
            onChange={(e) => touch(setElsewhere)(e.currentTarget.checked)}
          />
          {elsewhere && (
            <SimpleGrid cols={{ base: 1, sm: 3 }}>
              <TextInput
                label={t("addressLine1")}
                disabled={credit}
                value={address.line1}
                onChange={(e) => touch(setAddress)({ ...address, line1: e.currentTarget.value })}
              />
              <TextInput
                label={t("addressLine2")}
                disabled={credit}
                value={address.line2}
                onChange={(e) => touch(setAddress)({ ...address, line2: e.currentTarget.value })}
              />
              <TextInput
                label={t("postalCode")}
                disabled={credit}
                value={address.postalCode}
                onChange={(e) => touch(setAddress)({ ...address, postalCode: e.currentTarget.value })}
              />
              <TextInput
                label={t("city")}
                disabled={credit}
                value={address.city}
                onChange={(e) => touch(setAddress)({ ...address, city: e.currentTarget.value })}
              />
              <TextInput
                label={t("country")}
                disabled={credit}
                value={address.country}
                onChange={(e) => touch(setAddress)({ ...address, country: e.currentTarget.value.toUpperCase() })}
              />
            </SimpleGrid>
          )}
          <SimpleGrid cols={{ base: 1, sm: 4 }}>
            <TextInput
              label={t("yourReference")}
              disabled={credit}
              value={yourReference}
              onChange={(e) => touch(setYourReference)(e.currentTarget.value)}
              description={yourReference === "" ? t("yourReferenceNudge") : undefined}
            />
            <TextInput
              label={t("ourReference")}
              disabled={credit}
              value={ourReference}
              onChange={(e) => touch(setOurReference)(e.currentTarget.value)}
            />
            <TextInput
              label={t("orderReference")}
              disabled={credit}
              value={orderReference}
              onChange={(e) => touch(setOrderReference)(e.currentTarget.value)}
            />
            {!credit && (
              <NumberInput label={t("paymentTermsDays")} min={0} max={365} value={terms} onChange={touch(setTerms)} />
            )}
          </SimpleGrid>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Title order={4}>{t("lines")}</Title>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("description")}</Table.Th>
                <Table.Th>{t("quantity")}</Table.Th>
                <Table.Th>{t("unit")}</Table.Th>
                <Table.Th>{t("unitPrice")}</Table.Th>
                <Table.Th>{t("discountPercent")}</Table.Th>
                <Table.Th>{t("vatCode")}</Table.Th>
                <Table.Th ta="right">{t("lineNet")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lines.map((l, i) => (
                <Table.Tr key={l.key}>
                  <Table.Td>
                    <TextInput
                      aria-label={t("lineDescription", { n: i + 1 })}
                      value={l.description}
                      onChange={(e) => setLine(l.key, { description: e.currentTarget.value })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <NumberInput
                      aria-label={t("lineQuantity", { n: i + 1 })}
                      decimalScale={3}
                      min={0}
                      value={l.quantity}
                      onChange={(v) => setLine(l.key, { quantity: v })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <TextInput
                      aria-label={t("lineUnit", { n: i + 1 })}
                      disabled={credit}
                      value={l.unit}
                      onChange={(e) => setLine(l.key, { unit: e.currentTarget.value })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <NumberInput
                      aria-label={t("lineUnitPrice", { n: i + 1 })}
                      decimalScale={4}
                      min={0}
                      value={l.unitPrice}
                      onChange={(v) => setLine(l.key, { unitPrice: v })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <NumberInput
                      aria-label={t("lineDiscount", { n: i + 1 })}
                      decimalScale={2}
                      min={0}
                      max={100}
                      disabled={credit}
                      value={l.discountPercent}
                      onChange={(v) => setLine(l.key, { discountPercent: v })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <Select
                      aria-label={t("lineVatCode", { n: i + 1 })}
                      disabled={credit}
                      data={vatOptions}
                      value={l.vatCodeId === null ? null : String(l.vatCodeId)}
                      onChange={(v) => setLine(l.key, { vatCodeId: v === null ? null : Number(v) })}
                    />
                  </Table.Td>
                  <Table.Td ta="right">{money(amounts[i].net, draft.currency)}</Table.Td>
                  <Table.Td>
                    <Group gap={4} wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        aria-label={t("moveLineUp", { n: i + 1 })}
                        disabled={i === 0}
                        onClick={() => move(i, -1)}
                      >
                        <IconArrowUp size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        aria-label={t("moveLineDown", { n: i + 1 })}
                        disabled={i === lines.length - 1}
                        onClick={() => move(i, 1)}
                      >
                        <IconArrowDown size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("removeLine", { n: i + 1 })}
                        onClick={() => {
                          setLines((c) => c.filter((x) => x.key !== l.key));
                          setDirty(true);
                        }}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {!credit && (
            <Group>
              <Button
                variant="light"
                leftSection={<IconPlus size={16} />}
                onClick={() => {
                  setLines((c) => [
                    ...c,
                    {
                      key: nextKey(),
                      description: "",
                      quantity: 1,
                      unit: "",
                      unitPrice: 0,
                      discountPercent: 0,
                      vatCodeId: vatCodes[0]?.id ?? null,
                    },
                  ]);
                  setDirty(true);
                }}
              >
                {t("addLine")}
              </Button>
            </Group>
          )}
          <Totals
            currency={draft.currency}
            rates={totals.rates}
            net={totals.net}
            vat={totals.vat}
            gross={totals.gross}
          />
        </Stack>
      </Card>
      <Card withBorder>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Textarea
            label={t("note")}
            description={t("noteHint")}
            value={note}
            onChange={(e) => touch(setNote)(e.currentTarget.value)}
          />
          <Textarea
            label={t("internalNote")}
            description={t("internalNoteHint")}
            value={internalNote}
            onChange={(e) => touch(setInternalNote)(e.currentTarget.value)}
          />
        </SimpleGrid>
      </Card>
      {issuing && <IssueModal draft={draft} onClose={() => setIssuing(false)} />}
    </Stack>
  );
};

interface TotalsProps {
  currency: string;
  rates: { category: string; ratePercent: number; taxable: number; vat: number }[];
  net: number;
  vat: number;
  gross: number;
}

/** The totals per rate, then net, VAT and gross. */
const Totals = ({ currency, rates, net, vat, gross }: TotalsProps) => {
  const { t, money, number } = useInvoiceFormat();
  return (
    <Table withRowBorders={false} data-testid="totals">
      <Table.Tbody>
        {rates.map((r) => (
          <Table.Tr key={`${r.category}-${r.ratePercent}`}>
            <Table.Td>{t("vatAtRate", { category: r.category, rate: number(r.ratePercent, 2) })}</Table.Td>
            <Table.Td ta="right">{money(r.taxable, currency)}</Table.Td>
            <Table.Td ta="right">{money(r.vat, currency)}</Table.Td>
          </Table.Tr>
        ))}
        <Table.Tr>
          <Table.Td fw={600}>{t("netTotal")}</Table.Td>
          <Table.Td />
          <Table.Td ta="right" data-testid="net-total">
            {money(net, currency)}
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Td fw={600}>{t("vatTotal")}</Table.Td>
          <Table.Td />
          <Table.Td ta="right" data-testid="vat-total">
            {money(vat, currency)}
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Td fw={700}>{t("grossTotal")}</Table.Td>
          <Table.Td />
          <Table.Td ta="right" fw={700} data-testid="gross-total">
            {money(gross, currency)}
          </Table.Td>
        </Table.Tr>
      </Table.Tbody>
    </Table>
  );
};

/**
 * An issued document's page (D12): its header, lines, VAT summary and totals,
 * its credit notes, Download PDF, and Credit, which opens the new credit-note
 * draft. A document whose PDF could not be stored at issue says the first
 * download stores it.
 */
const IssuedDocument = ({ document: doc, canIssue }: { document: InvoiceDocument; canIssue: boolean }) => {
  const { t, money, date, number } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const heading = useHeading(doc);
  const credit = useMutation({
    mutationFn: () => creditInvoice(doc.id),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      navigate(invoiceLinkOptions(draft.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCredit"), message: refusalMessage(error, t) }),
  });
  const creditable = doc.kind === "invoice" && canIssue && (doc.uncreditedAmount ?? 0) > 0;
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("invoices"), to: "/invoices" }, { label: heading }]}
        title={heading}
        actions={
          <Group>
            <Button component="a" href={pdfUrl(doc.id)} download leftSection={<IconDownload size={16} />}>
              {t("downloadPdf")}
            </Button>
            {creditable && (
              <Button variant="default" loading={credit.isPending} onClick={() => credit.mutate()}>
                {t("credit")}
              </Button>
            )}
          </Group>
        }
      />
      <CreditsLink doc={doc} />
      {doc.pdfStored === false && <Alert color="yellow">{t("pdfNotStoredYet")}</Alert>}
      {doc.warnings.includes("issued_late") && <Alert color="yellow">{t("warning.issued_late")}</Alert>}
      <Card withBorder>
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <Stack gap={2}>
            <Text fw={600}>{doc.buyer?.name}</Text>
            {doc.buyer?.addressLine1 && <Text size="sm">{doc.buyer.addressLine1}</Text>}
            {(doc.buyer?.postalCode || doc.buyer?.city) && (
              <Text size="sm">{`${doc.buyer?.postalCode ?? ""} ${doc.buyer?.city ?? ""}`.trim()}</Text>
            )}
            {doc.buyer?.organisationNumber && (
              <Text size="sm">{t("orgNumber", { number: doc.buyer.organisationNumber })}</Text>
            )}
          </Stack>
          <Stack gap={2}>
            <Text size="sm">
              {t("issueDate")}: {doc.issueDate ? date(doc.issueDate) : ""}
            </Text>
            {doc.dueDate && (
              <Text size="sm">
                {t("dueDate")}: {date(doc.dueDate)}
              </Text>
            )}
            {doc.deliveryDate && (
              <Text size="sm">
                {t("deliveryDate")}: {date(doc.deliveryDate)}
              </Text>
            )}
            {doc.deliveryFrom && doc.deliveryTo && (
              <Text size="sm">
                {t("deliveryPeriod")}: {date(doc.deliveryFrom)} – {date(doc.deliveryTo)}
              </Text>
            )}
          </Stack>
          <Stack gap={2}>
            {doc.yourReference && (
              <Text size="sm">
                {t("yourReference")}: {doc.yourReference}
              </Text>
            )}
            {doc.ourReference && (
              <Text size="sm">
                {t("ourReference")}: {doc.ourReference}
              </Text>
            )}
            {doc.orderReference && (
              <Text size="sm">
                {t("orderReference")}: {doc.orderReference}
              </Text>
            )}
          </Stack>
        </SimpleGrid>
      </Card>
      <Card withBorder>
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("description")}</Table.Th>
              <Table.Th ta="right">{t("quantity")}</Table.Th>
              <Table.Th>{t("unit")}</Table.Th>
              <Table.Th ta="right">{t("unitPrice")}</Table.Th>
              <Table.Th ta="right">{t("discountPercent")}</Table.Th>
              <Table.Th ta="right">{t("vatPercent")}</Table.Th>
              <Table.Th ta="right">{t("lineNet")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {doc.lines.map((l) => (
              <Table.Tr key={l.id}>
                <Table.Td>{l.description}</Table.Td>
                <Table.Td ta="right">{number(l.quantity)}</Table.Td>
                <Table.Td>{l.unit}</Table.Td>
                <Table.Td ta="right">{money(l.unitPrice, doc.currency)}</Table.Td>
                <Table.Td ta="right">{number(l.discountPercent, 2)}</Table.Td>
                <Table.Td ta="right">{number(l.vatRatePercent ?? 0, 2)}</Table.Td>
                <Table.Td ta="right">{money(l.lineNet, doc.currency)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        <Totals
          currency={doc.currency}
          rates={doc.vatSummaries.map((s) => ({
            category: s.vatCategory,
            ratePercent: s.ratePercent,
            taxable: s.taxableAmount,
            vat: s.vatAmount,
          }))}
          net={doc.netTotal}
          vat={doc.vatTotal}
          gross={doc.grossTotal}
        />
      </Card>
      {doc.kind === "invoice" && (
        <Card withBorder>
          <Stack gap="xs">
            <Title order={4}>{t("creditNotes")}</Title>
            {(doc.creditNotes ?? []).length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("noCreditNotes")}
              </Text>
            ) : (
              (doc.creditNotes ?? []).map((c) => (
                <Group key={c.id} gap="xs">
                  <DocumentLink invoiceId={c.id}>
                    {c.number ? `${t("kindCreditNote")} ${c.number}` : `${t("kindCreditNote")} — ${t("statusDraft")}`}
                  </DocumentLink>
                  {c.status === "draft" && <Badge variant="light">{t("statusDraft")}</Badge>}
                  <Text size="sm">{money(c.grossTotal, doc.currency)}</Text>
                </Group>
              ))
            )}
            <Text size="sm">
              {t("uncreditedAmount")}: {money(doc.uncreditedAmount ?? 0, doc.currency)}
            </Text>
          </Stack>
        </Card>
      )}
    </Stack>
  );
};
```

**Replace the whole of** `apps/invoices/frontend/src/pages/invoices.tsx` with:

```tsx
import {
  Alert,
  Badge,
  Button,
  Group,
  Modal,
  Pagination,
  SegmentedControl,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createInvoice, type InvoiceListFilters, invoiceListQueryOptions } from "../api/invoices";
import { invoicesMetaQueryOptions } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import { CustomerPicker } from "../components/customer-picker";
import { DocumentLink } from "../components/document-link";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";

export interface InvoicesPageProps {
  /** Whether the caller holds `customers:view`, which the buyer picker needs (D1). The host reads it. */
  canViewCustomers: boolean;
  /** The signed-in user's name, the "Vår ref." a new draft is prefilled with (D4). */
  userDisplayName?: string;
}

/**
 * The list (D4, D12): drafts first, then by number descending, with the
 * status and kind chips, a customer filter, a search, an issue-date range and
 * paging. "New invoice" is offered to a caller who may create drafts and pick
 * a buyer.
 */
export const InvoicesPage = ({ canViewCustomers, userDisplayName }: InvoicesPageProps) => {
  const { t, money, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [filters, setFilters] = useState<InvoiceListFilters>({ page: 1 });
  const list = useQuery(invoiceListQueryOptions(filters));
  const [creating, setCreating] = useState(false);
  const set = (next: Partial<InvoiceListFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const canCreate = Boolean(meta.data?.capabilities.canCreate) && canViewCustomers;

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("invoices")}
        description={t("invoicesDescription")}
        actions={
          canCreate && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
              {t("newInvoice")}
            </Button>
          )
        }
      />
      {meta.data && !meta.data.sellerComplete && meta.data.capabilities.canIssue && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("sellerIncompleteTitle")}>
          {t("sellerIncomplete")}
        </Alert>
      )}
      <Group align="flex-end" wrap="wrap">
        <SegmentedControl
          aria-label={t("status")}
          value={filters.status ?? "all"}
          onChange={(v) => set({ status: v === "all" ? undefined : (v as "draft" | "issued") })}
          data={[
            { value: "all", label: t("allStatuses") },
            { value: "draft", label: t("statusDraft") },
            { value: "issued", label: t("statusIssued") },
          ]}
        />
        <SegmentedControl
          aria-label={t("kind")}
          value={filters.kind ?? "all"}
          onChange={(v) => set({ kind: v === "all" ? undefined : (v as "invoice" | "credit_note") })}
          data={[
            { value: "all", label: t("allKinds") },
            { value: "invoice", label: t("kindInvoice") },
            { value: "credit_note", label: t("kindCreditNote") },
          ]}
        />
        {canViewCustomers && (
          <CustomerPicker
            label={t("customerFilter")}
            value={filters.customerId ?? null}
            onChange={(id) => set({ customerId: id ?? undefined })}
          />
        )}
        <TextInput
          label={t("search")}
          placeholder={t("searchPlaceholder")}
          value={filters.search ?? ""}
          onChange={(e) => set({ search: e.currentTarget.value })}
        />
        <DateInput
          label={t("issuedFrom")}
          clearable
          valueFormat={t("dateInputFormat")}
          value={filters.from ?? null}
          onChange={(d) => set({ from: d ?? undefined })}
        />
        <DateInput
          label={t("issuedTo")}
          clearable
          valueFormat={t("dateInputFormat")}
          value={filters.to ?? null}
          onChange={(d) => set({ to: d ?? undefined })}
        />
      </Group>
      {list.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoices")}>
          {refusalMessage(list.error, t)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
      {list.data && list.data.data.length === 0 && (
        <EmptyState title={t("noInvoices")} description={t("noInvoicesDescription")} />
      )}
      {list.data && list.data.data.length > 0 && (
        <>
          <Table highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("number")}</Table.Th>
                <Table.Th>{t("kind")}</Table.Th>
                <Table.Th>{t("customer")}</Table.Th>
                <Table.Th>{t("issueDate")}</Table.Th>
                <Table.Th>{t("dueDate")}</Table.Th>
                <Table.Th ta="right">{t("grossTotal")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {list.data.data.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td>
                    <DocumentLink invoiceId={row.id}>{row.number ?? t("draftNumber")}</DocumentLink>{" "}
                    {row.status === "draft" && <Badge variant="light">{t("statusDraft")}</Badge>}
                  </Table.Td>
                  <Table.Td>{row.kind === "credit_note" ? t("kindCreditNote") : t("kindInvoice")}</Table.Td>
                  <Table.Td>{row.customerName ?? t("unknownCustomer")}</Table.Td>
                  <Table.Td>{row.issueDate ? date(row.issueDate) : t("notAvailable")}</Table.Td>
                  <Table.Td>{row.dueDate ? date(row.dueDate) : t("notAvailable")}</Table.Td>
                  <Table.Td ta="right">{money(row.grossTotal, row.currency)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {list.data.pagination.totalPages > 1 && (
            <Pagination
              total={list.data.pagination.totalPages}
              value={list.data.pagination.page}
              onChange={(page) => set({ page })}
            />
          )}
          <Text size="sm" c="dimmed">
            {t("documentCount", { count: list.data.pagination.totalCount })}
          </Text>
        </>
      )}
      {creating && (
        <NewInvoiceModal
          userDisplayName={userDisplayName}
          deliveryDate={meta.data?.today}
          onClose={() => setCreating(false)}
        />
      )}
    </Stack>
  );
};

interface NewInvoiceModalProps {
  userDisplayName?: string;
  deliveryDate?: string;
  onClose: () => void;
}

/**
 * Picks the buyer and makes the draft. The API never invents a delivery date;
 * the UI prefills today, which the editor lets the person change (D4).
 */
const NewInvoiceModal = ({ userDisplayName, deliveryDate, onClose }: NewInvoiceModalProps) => {
  const { t } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [customerId, setCustomerId] = useState<number | null>(null);
  const create = useMutation({
    mutationFn: () =>
      createInvoice({
        customerId: customerId as number,
        lines: [],
        ...(deliveryDate ? { deliveryDate } : {}),
        ...(userDisplayName ? { ourReference: userDisplayName } : {}),
      }),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      onClose();
      navigate(invoiceLinkOptions(draft.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreate"), message: refusalMessage(error, t) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("newInvoice")}>
      <Stack>
        <CustomerPicker value={customerId} onChange={setCustomerId} required />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={customerId === null} loading={create.isPending} onClick={() => create.mutate()}>
            {t("createDraft")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
```

**Replace the whole of** `apps/invoices/frontend/src/i18n.ts` with:

```ts
import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

export const invoicesCatalog = {
  en: {
    invoices: "Invoices",
    invoicesDescription: "Invoices and credit notes: drafts, and the numbered documents they are issued into.",
    failedToLoadMeta: "Could not load Invoices",
    failedToLoadInvoices: "Could not load the invoices",
    failedToLoadInvoice: "Could not load the document",
    noInvoices: "No invoices yet",
    noInvoicesDescription: "A draft becomes a numbered invoice the moment it is issued.",
    notAvailable: "—",
    cancel: "Cancel",
    save: "Save",
    saved: "Saved",
    delete: "Delete",
    dateInputFormat: "MMM D, YYYY",

    newInvoice: "New invoice",
    createDraft: "Create the draft",
    couldNotCreate: "Could not create the draft",
    couldNotSave: "Could not save the draft",
    couldNotDelete: "Could not delete the draft",
    couldNotIssue: "Could not issue",
    couldNotCredit: "Could not create the credit note",
    sellerIncompleteTitle: "The seller is not complete",
    sellerIncomplete: "Nothing can be issued until the seller record in the settings is complete.",

    status: "Status",
    allStatuses: "Any status",
    statusDraft: "Draft",
    statusIssued: "Issued",
    kind: "Kind",
    allKinds: "Any kind",
    kindInvoice: "Invoice",
    kindCreditNote: "Credit note",
    customer: "Customer",
    customerFilter: "Customer",
    searchCustomers: "Search customers",
    searching: "Searching…",
    noCustomersFound: "No customer matches",
    unknownCustomer: "Unknown customer",
    search: "Search",
    searchPlaceholder: "Number or buyer",
    issuedFrom: "Issued from",
    issuedTo: "Issued to",
    number: "Number",
    draftNumber: "Draft",
    issueDate: "Issue date",
    dueDate: "Due date",
    grossTotal: "Total",
    netTotal: "Total excluding VAT",
    vatTotal: "VAT",
    documentCount_one: "{{count}} document",
    documentCount_other: "{{count}} documents",

    delivery: "Delivery",
    deliveryDay: "A day",
    deliveryPeriod: "Delivery period",
    deliveryDate: "Delivery date",
    deliveryFrom: "Delivered from",
    deliveryTo: "Delivered to",
    deliveryRequiredToIssue: "A delivery date or period is needed before the document can be issued.",
    deliverElsewhere: "Delivered somewhere other than the buyer's address",
    addressLine1: "Address",
    addressLine2: "Address line 2",
    postalCode: "Postal code",
    city: "City",
    country: "Country",
    yourReference: "Your reference",
    yourReferenceNudge: "Empty — an e-invoice (EHF) will need the buyer's reference.",
    ourReference: "Our reference",
    orderReference: "Order reference",
    paymentTermsDays: "Payment terms (days)",
    lines: "Lines",
    description: "Description",
    quantity: "Quantity",
    unit: "Unit",
    unitPrice: "Unit price",
    discountPercent: "Discount %",
    vatCode: "VAT code",
    vatPercent: "VAT %",
    lineNet: "Amount",
    lineDescription: "Line {{n}} description",
    lineQuantity: "Line {{n}} quantity",
    lineUnit: "Line {{n}} unit",
    lineUnitPrice: "Line {{n}} unit price",
    lineDiscount: "Line {{n}} discount",
    lineVatCode: "Line {{n}} VAT code",
    moveLineUp: "Move line {{n}} up",
    moveLineDown: "Move line {{n}} down",
    removeLine: "Remove line {{n}}",
    addLine: "Add a line",
    vatAtRate: "{{category}} {{rate}} %",
    note: "Note",
    noteHint: "Printed on the document.",
    internalNote: "Internal note",
    internalNoteHint: "Never printed.",
    warnings: "Worth a look",
    "warning.customer_currency_differs":
      "The customer is invoiced in another currency; this phase invoices in NOK only.",
    "warning.issued_late": "The delivery ended more than a month ago; the law asks for the invoice within a month.",
    "warning.credit_exceeds_invoice":
      "This credit note is more than the invoice has left to credit; it cannot be issued as it stands.",
    "warning.credit_exceeds_line":
      "A line credits more than the original line had left; it cannot be issued as it stands.",
    preview: "Preview",
    issue: "Issue",
    saveBeforeIssue: "Save the changes before issuing.",
    deleteDraftTitle: "Delete the draft?",
    deleteDraftBody: "The draft and its lines are deleted. Nothing was issued, so no number is lost.",

    issueInvoice: "Issue the invoice",
    issueCreditNote: "Issue the credit note",
    issueCannotBeUndone: "This assigns the next number and cannot be undone.",
    issuedToday: "It is issued today, {{date}}.",
    issuedAs: "Issued as number {{number}}",
    issuedLateAfter: "It is issued; mind the one-month deadline next time.",

    downloadPdf: "Download PDF",
    credit: "Credit",
    creditsInvoice: "Credit note for invoice",
    creditNotes: "Credit notes",
    noCreditNotes: "No credit notes.",
    uncreditedAmount: "Left to credit",
    pdfNotStoredYet:
      "The PDF could not be stored when the document was issued. It is stored the first time it is downloaded.",
    orgNumber: "Org. no. {{number}}",

    "refusal.seller_incomplete": "The seller record is not complete. Fill it in under the settings.",
    "refusal.no_lines": "A document needs at least one line.",
    "refusal.delivery_date_missing": "Give the delivery date or period first.",
    "refusal.issue_date_not_allowed": "That issue date is not allowed today.",
    "refusal.customer_merged": "The customer was merged into another; invoice that one instead.",
    "refusal.customer_archived": "The customer is archived and is not invoiced.",
    "refusal.customer_blocked": "The customer is blocked for invoicing.",
    "refusal.customer_missing": "The customer no longer exists.",
    "refusal.buyer_incomplete": "The buyer has neither a complete address nor an organisation number.",
    "refusal.vat_code_inactive": "A line's VAT code is no longer offered.",
    "refusal.vat_code_not_valid": "A line's VAT code has no rate on the issue date.",
    "refusal.vat_not_registered":
      "The seller is not VAT-registered, so every line must be outside the VAT act (code 7).",
    "refusal.category_o_not_allowed": "The seller is VAT-registered, so no line may be outside the VAT act.",
    "refusal.reverse_charge_needs_org_number": "Reverse charge needs the buyer's organisation number.",
    "refusal.vat_codes_ambiguous": "Two lines share a VAT rate but carry different SAF-T codes.",
    "refusal.credit_exceeds_line": "A line credits more than the original line had left.",
    "refusal.credit_exceeds_invoice": "The credit note is more than the invoice has left to credit.",
    "refusal.invoice_issued": "The document is already issued.",
    "refusal.invoice_changed": "The invoice changed; try again.",
    "refusal.invoice_fully_credited": "The invoice is already credited in full.",
    "refusal.storage_unavailable": "The document store is unavailable, so nothing can be issued.",
  },
  nb: {
    invoices: "Fakturaer",
    invoicesDescription: "Fakturaer og kreditnotaer: utkast, og de nummererte dokumentene de utstedes som.",
    failedToLoadMeta: "Kunne ikke laste Fakturaer",
    failedToLoadInvoices: "Kunne ikke laste fakturaene",
    failedToLoadInvoice: "Kunne ikke laste dokumentet",
    noInvoices: "Ingen fakturaer ennå",
    noInvoicesDescription: "Et utkast blir en nummerert faktura i det øyeblikket det utstedes.",
    notAvailable: "—",
    cancel: "Avbryt",
    save: "Lagre",
    saved: "Lagret",
    delete: "Slett",
    dateInputFormat: "D. MMM YYYY",

    newInvoice: "Ny faktura",
    createDraft: "Lag utkastet",
    couldNotCreate: "Kunne ikke lage utkastet",
    couldNotSave: "Kunne ikke lagre utkastet",
    couldNotDelete: "Kunne ikke slette utkastet",
    couldNotIssue: "Kunne ikke utstede",
    couldNotCredit: "Kunne ikke lage kreditnotaen",
    sellerIncompleteTitle: "Selgeropplysningene er ikke fullstendige",
    sellerIncomplete: "Ingenting kan utstedes før selgeropplysningene i innstillingene er fullstendige.",

    status: "Status",
    allStatuses: "Alle statuser",
    statusDraft: "Utkast",
    statusIssued: "Utstedt",
    kind: "Type",
    allKinds: "Alle typer",
    kindInvoice: "Faktura",
    kindCreditNote: "Kreditnota",
    customer: "Kunde",
    customerFilter: "Kunde",
    searchCustomers: "Søk etter kunder",
    searching: "Søker …",
    noCustomersFound: "Ingen kunde passer",
    unknownCustomer: "Ukjent kunde",
    search: "Søk",
    searchPlaceholder: "Nummer eller kjøper",
    issuedFrom: "Utstedt fra",
    issuedTo: "Utstedt til",
    number: "Nummer",
    draftNumber: "Utkast",
    issueDate: "Fakturadato",
    dueDate: "Forfallsdato",
    grossTotal: "Sum",
    netTotal: "Sum eks. mva",
    vatTotal: "Mva",
    documentCount_one: "{{count}} dokument",
    documentCount_other: "{{count}} dokumenter",

    delivery: "Levering",
    deliveryDay: "En dag",
    deliveryPeriod: "Leveringsperiode",
    deliveryDate: "Leveringsdato",
    deliveryFrom: "Levert fra",
    deliveryTo: "Levert til",
    deliveryRequiredToIssue: "Leveringsdato eller -periode må fylles ut før dokumentet kan utstedes.",
    deliverElsewhere: "Levert et annet sted enn kjøperens adresse",
    addressLine1: "Adresse",
    addressLine2: "Adresselinje 2",
    postalCode: "Postnummer",
    city: "Poststed",
    country: "Land",
    yourReference: "Deres referanse",
    yourReferenceNudge: "Tom — en e-faktura (EHF) vil trenge kjøperens referanse.",
    ourReference: "Vår referanse",
    orderReference: "Ordrereferanse",
    paymentTermsDays: "Betalingsfrist (dager)",
    lines: "Linjer",
    description: "Beskrivelse",
    quantity: "Antall",
    unit: "Enhet",
    unitPrice: "Enhetspris",
    discountPercent: "Rabatt %",
    vatCode: "Mva-kode",
    vatPercent: "Mva %",
    lineNet: "Beløp",
    lineDescription: "Beskrivelse på linje {{n}}",
    lineQuantity: "Antall på linje {{n}}",
    lineUnit: "Enhet på linje {{n}}",
    lineUnitPrice: "Enhetspris på linje {{n}}",
    lineDiscount: "Rabatt på linje {{n}}",
    lineVatCode: "Mva-kode på linje {{n}}",
    moveLineUp: "Flytt linje {{n}} opp",
    moveLineDown: "Flytt linje {{n}} ned",
    removeLine: "Fjern linje {{n}}",
    addLine: "Legg til en linje",
    vatAtRate: "{{category}} {{rate}} %",
    note: "Merknad",
    noteHint: "Skrives ut på dokumentet.",
    internalNote: "Intern merknad",
    internalNoteHint: "Skrives aldri ut.",
    warnings: "Verdt å se på",
    "warning.customer_currency_differs": "Kunden faktureres i en annen valuta; denne fasen fakturerer bare i NOK.",
    "warning.issued_late":
      "Leveringen ble avsluttet for mer enn en måned siden; loven krever fakturaen innen en måned.",
    "warning.credit_exceeds_invoice":
      "Kreditnotaen er på mer enn fakturaen har igjen å kreditere; den kan ikke utstedes slik den er.",
    "warning.credit_exceeds_line":
      "En linje krediterer mer enn den opprinnelige linjen hadde igjen; den kan ikke utstedes slik den er.",
    preview: "Forhåndsvis",
    issue: "Utsted",
    saveBeforeIssue: "Lagre endringene før du utsteder.",
    deleteDraftTitle: "Slette utkastet?",
    deleteDraftBody: "Utkastet og linjene slettes. Ingenting er utstedt, så ingen nummer går tapt.",

    issueInvoice: "Utsted fakturaen",
    issueCreditNote: "Utsted kreditnotaen",
    issueCannotBeUndone: "Dette gir dokumentet neste nummer og kan ikke angres.",
    issuedToday: "Den utstedes i dag, {{date}}.",
    issuedAs: "Utstedt som nummer {{number}}",
    issuedLateAfter: "Den er utstedt; husk fristen på en måned neste gang.",

    downloadPdf: "Last ned PDF",
    credit: "Krediter",
    creditsInvoice: "Kreditnota til faktura",
    creditNotes: "Kreditnotaer",
    noCreditNotes: "Ingen kreditnotaer.",
    uncreditedAmount: "Igjen å kreditere",
    pdfNotStoredYet: "PDF-en kunne ikke lagres da dokumentet ble utstedt. Den lagres første gang den lastes ned.",
    orgNumber: "Org.nr. {{number}}",

    "refusal.seller_incomplete": "Selgeropplysningene er ikke fullstendige. Fyll dem ut i innstillingene.",
    "refusal.no_lines": "Et dokument må ha minst én linje.",
    "refusal.delivery_date_missing": "Oppgi leveringsdato eller -periode først.",
    "refusal.issue_date_not_allowed": "Den fakturadatoen er ikke tillatt i dag.",
    "refusal.customer_merged": "Kunden er slått sammen med en annen; fakturer den i stedet.",
    "refusal.customer_archived": "Kunden er arkivert og faktureres ikke.",
    "refusal.customer_blocked": "Kunden er sperret for fakturering.",
    "refusal.customer_missing": "Kunden finnes ikke lenger.",
    "refusal.buyer_incomplete": "Kjøperen har verken fullstendig adresse eller organisasjonsnummer.",
    "refusal.vat_code_inactive": "En linjes mva-kode tilbys ikke lenger.",
    "refusal.vat_code_not_valid": "En linjes mva-kode har ingen sats på fakturadatoen.",
    "refusal.vat_not_registered": "Selgeren er ikke mva-registrert, så alle linjer må være utenfor mva-loven (kode 7).",
    "refusal.category_o_not_allowed": "Selgeren er mva-registrert, så ingen linje kan være utenfor mva-loven.",
    "refusal.reverse_charge_needs_org_number": "Omvendt avgiftsplikt krever kjøperens organisasjonsnummer.",
    "refusal.vat_codes_ambiguous": "To linjer har samme mva-sats, men ulike SAF-T-koder.",
    "refusal.credit_exceeds_line": "En linje krediterer mer enn den opprinnelige linjen hadde igjen.",
    "refusal.credit_exceeds_invoice": "Kreditnotaen er på mer enn fakturaen har igjen å kreditere.",
    "refusal.invoice_issued": "Dokumentet er allerede utstedt.",
    "refusal.invoice_changed": "Fakturaen er endret; prøv igjen.",
    "refusal.invoice_fully_credited": "Fakturaen er allerede kreditert i sin helhet.",
    "refusal.storage_unavailable": "Dokumentlageret er utilgjengelig, så ingenting kan utstedes.",
  },
} satisfies CatalogResources;

registerCatalog("invoices", invoicesCatalog);
```

**Replace the whole of** `apps/invoices/frontend/src/index.ts` with:

```ts
import "./i18n";

export * from "./api/customers";
export * from "./api/invoices";
export { type InvoicesMeta, invoicesMetaQueryOptions, type VatCodeInForce } from "./api/meta";
export { CustomerPicker, type CustomerPickerProps } from "./components/customer-picker";
export { invoicesCatalog } from "./i18n";
export * from "./lib/errors";
export * from "./lib/format";
export * from "./lib/money";
export * from "./lib/routes";
export { IssueModal, type IssueModalProps } from "./pages/-issue-modal";
export { InvoicePage, type InvoicePageProps } from "./pages/invoice";
export { InvoicesPage, type InvoicesPageProps } from "./pages/invoices";
```

- [ ] **Step 3: The host: the document route and what the pages need from the session**

**Create** `apps/host/frontend/src/routes/invoices/$invoiceId.tsx`:

```tsx
import { createFileRoute, notFound } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { InvoicePage } from "@vantigo/invoices-ui/pages/invoice";
import { useInvoiceAccess } from "./-invoice-access";

/**
 * One invoice or credit note. The path is the package's own
 * `INVOICE_ROUTE_PATH` — the list and the create and credit flows navigate to
 * it. A path that is not a positive whole number never reaches the API: it is
 * a not-found, which the `/invoices` layout's own boundary renders.
 */
export const Route = createFileRoute("/invoices/$invoiceId")({
  params: {
    parse: ({ invoiceId }) => ({ invoiceId: /^\d+$/.test(invoiceId) ? Number(invoiceId) : Number.NaN }),
    stringify: ({ invoiceId }) => ({ invoiceId: String(invoiceId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isSafeInteger(params.invoiceId) || params.invoiceId <= 0) throw notFound();
  },
  component: InvoiceRoute,
});

function InvoiceRoute() {
  const { invoiceId } = Route.useParams();
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <InvoicePage invoiceId={invoiceId} canViewCustomers={access.canViewCustomers} />;
}
```

**Create** `apps/host/frontend/src/routes/invoices/-invoice-access.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * What the Invoices pages need from the host's session: whether the caller
 * may search customers — the buyer picker reads the customers module's list,
 * which needs `customers:view` (invoices foundation design D1) — and the name
 * a new draft's "Vår ref." is prefilled with. Both queries share the root
 * layout's keys, so they read its cache rather than refetching.
 */
export const useInvoiceAccess = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return {
    ready: authorization.isSuccess || authorization.isError,
    canViewCustomers: hasPermissions(authorization.data?.permissions, ["customers:view"]),
    userDisplayName: session.data?.user.displayName,
  };
};
```

**Create** `apps/host/frontend/src/routes/invoices/-invoices-list.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InvoicesList } from "./-invoices-list";
import "../../i18n";

// The list's picker needs customers:view (invoices foundation design D1), which
// the host reads from the caller's effective access; this pins that hand-off.
vi.mock("@vantigo/invoices-ui/pages/invoices", () => ({
  InvoicesPage: ({ canViewCustomers, userDisplayName }: { canViewCustomers: boolean; userDisplayName?: string }) => (
    <div>
      invoices for {userDisplayName} {canViewCustomers ? "with" : "without"} the picker
    </div>
  ),
}));

const { fetchSession, getAuthorizationMe } = vi.hoisted(() => ({ fetchSession: vi.fn(), getAuthorizationMe: vi.fn() }));
vi.mock("../../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/auth")>()),
  fetchSession,
}));
vi.mock("../../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/authorization")>()),
  getAuthorizationMe,
}));

const renderList = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <InvoicesList />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the invoice list's entry point", () => {
  it("offers the buyer picker to a customers:view holder, with the caller's name", async () => {
    fetchSession.mockResolvedValue({ user: { id: "u1", displayName: "Ola Nordmann", email: "ola@example.test" } });
    getAuthorizationMe.mockResolvedValue({ permissions: ["invoices:access", "customers:view"] });
    renderList();
    expect(await screen.findByText("invoices for Ola Nordmann with the picker")).toBeInTheDocument();
  });

  it("withholds it from a caller without customers:view", async () => {
    fetchSession.mockResolvedValue({ user: { id: "u1", displayName: "Ola Nordmann", email: "ola@example.test" } });
    getAuthorizationMe.mockResolvedValue({ permissions: ["invoices:access"] });
    renderList();
    expect(await screen.findByText("invoices for Ola Nordmann without the picker")).toBeInTheDocument();
  });
});
```

**Create** `apps/host/frontend/src/routes/invoices/-invoices-list.tsx`:

```tsx
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { InvoicesPage } from "@vantigo/invoices-ui/pages/invoices";
import "../../i18n";
import { useInvoiceAccess } from "./-invoice-access";

/**
 * The list's entry point: the package page, told whether the caller may pick
 * a buyer and whose name "Vår ref." starts as. It sits beside the route file
 * because a route file may export nothing but its `Route`.
 */
export const InvoicesList = () => {
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <InvoicesPage canViewCustomers={access.canViewCustomers} userDisplayName={access.userDisplayName} />;
};
```

**Replace the whole of** `apps/host/frontend/src/routes/invoices/index.tsx` with:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { InvoicesList } from "./-invoices-list";

export const Route = createFileRoute("/invoices/")({
  component: InvoicesList,
});
```

- [ ] **Step 4: Verify and commit**

**Run**, from the repository root:

```bash
mise exec -- bunx biome check --write apps/invoices/frontend apps/host/frontend/src
mise exec -- bun run --cwd apps/host/frontend test   # also regenerates routeTree.gen.ts
```

```bash
mise exec -- bun run --cwd apps/invoices/frontend typecheck && mise exec -- bun run --cwd apps/invoices/frontend lint && mise exec -- bun run --cwd apps/invoices/frontend test
mise exec -- bun run --cwd apps/host/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task10.txt <<'MSG'
feat(invoices-ui): the list, the draft editor, the issue dialog and the credit flow

The Invoices app (invoices foundation design D12): the list with its chips,
filters and paging; "New invoice" through the customers list for callers
who hold customers:view; the draft editor with live totals per rate by the
server's exact rule; the issue dialog offering the dates the server allows;
an issued document with its PDF and credit notes; and a credit-note draft
that offers only what D8 allows. en and nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/host/frontend/src/routeTree.gen.ts' 'apps/host/frontend/src/routes/invoices/$invoiceId.tsx' 'apps/host/frontend/src/routes/invoices/-invoice-access.tsx' 'apps/host/frontend/src/routes/invoices/-invoices-list.test.tsx' 'apps/host/frontend/src/routes/invoices/-invoices-list.tsx' 'apps/host/frontend/src/routes/invoices/index.tsx' 'apps/invoices/frontend/src/api/customers.ts' 'apps/invoices/frontend/src/api/invoices.ts' 'apps/invoices/frontend/src/components/customer-picker.tsx' 'apps/invoices/frontend/src/components/document-link.tsx' 'apps/invoices/frontend/src/i18n.ts' 'apps/invoices/frontend/src/index.ts' 'apps/invoices/frontend/src/lib/errors.ts' 'apps/invoices/frontend/src/lib/format.ts' 'apps/invoices/frontend/src/lib/money.test.ts' 'apps/invoices/frontend/src/lib/money.ts' 'apps/invoices/frontend/src/lib/routes.ts' 'apps/invoices/frontend/src/pages/-issue-modal.tsx' 'apps/invoices/frontend/src/pages/invoice.test.tsx' 'apps/invoices/frontend/src/pages/invoice.tsx' 'apps/invoices/frontend/src/pages/invoices.test.tsx' 'apps/invoices/frontend/src/pages/invoices.tsx' 'apps/invoices/frontend/src/test/fixtures.ts' 'apps/invoices/frontend/src/test/invoice-route.tsx' 'apps/invoices/frontend/src/test/route-tree.tsx'
git commit -F /tmp/claude-1000/msg-invoices-task10.txt -- 'apps/host/frontend/src/routeTree.gen.ts' 'apps/host/frontend/src/routes/invoices/$invoiceId.tsx' 'apps/host/frontend/src/routes/invoices/-invoice-access.tsx' 'apps/host/frontend/src/routes/invoices/-invoices-list.test.tsx' 'apps/host/frontend/src/routes/invoices/-invoices-list.tsx' 'apps/host/frontend/src/routes/invoices/index.tsx' 'apps/invoices/frontend/src/api/customers.ts' 'apps/invoices/frontend/src/api/invoices.ts' 'apps/invoices/frontend/src/components/customer-picker.tsx' 'apps/invoices/frontend/src/components/document-link.tsx' 'apps/invoices/frontend/src/i18n.ts' 'apps/invoices/frontend/src/index.ts' 'apps/invoices/frontend/src/lib/errors.ts' 'apps/invoices/frontend/src/lib/format.ts' 'apps/invoices/frontend/src/lib/money.test.ts' 'apps/invoices/frontend/src/lib/money.ts' 'apps/invoices/frontend/src/lib/routes.ts' 'apps/invoices/frontend/src/pages/-issue-modal.tsx' 'apps/invoices/frontend/src/pages/invoice.test.tsx' 'apps/invoices/frontend/src/pages/invoice.tsx' 'apps/invoices/frontend/src/pages/invoices.test.tsx' 'apps/invoices/frontend/src/pages/invoices.tsx' 'apps/invoices/frontend/src/test/fixtures.ts' 'apps/invoices/frontend/src/test/invoice-route.tsx' 'apps/invoices/frontend/src/test/route-tree.tsx'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 11: The Invoices app: settings with the completeness checklist and the VAT codes, and the journal (D12)

Settings (`invoices:manage`): the seller record with the checklist from `missingSellerFields`, the series start read-only once `seriesLocked`, and the VAT codes — add, edit (category and SAF-T fixed while in use), deactivate, and the rate periods (add from a date, remove the latest while it lies in the future). The journal: a date range (this month by default), the gap check in words ("No gaps between N and M" or the missing numbers), the counter warning, the per-code totals, the documents with credit notes signed negative, paging. The host gains the two sidebar entries and routes.

**Files:**
- Create: `apps/host/frontend/src/routes/invoices/journal.tsx`, `apps/host/frontend/src/routes/invoices/settings.tsx`, `apps/invoices/frontend/src/api/journal.ts`, `apps/invoices/frontend/src/api/settings.ts`, `apps/invoices/frontend/src/api/vat-codes.ts`, `apps/invoices/frontend/src/pages/journal.test.tsx`, `apps/invoices/frontend/src/pages/journal.tsx`, `apps/invoices/frontend/src/pages/settings.test.tsx`, `apps/invoices/frontend/src/pages/settings.tsx`
- Modify: `apps/host/frontend/src/apps.test.ts`, `apps/host/frontend/src/apps.ts`, `apps/host/frontend/src/catalogs/navigation.ts`, `apps/invoices/frontend/src/i18n.ts`, `apps/invoices/frontend/src/index.ts`, `apps/invoices/frontend/src/test/fixtures.ts`
- Generated (commit them; never edit by hand): `apps/host/frontend/src/routeTree.gen.ts`
- Read first (do not change): `apps/expenses/frontend/src/pages/settings.tsx`, `apps/host/frontend/src/apps.ts` (the Invoices entry from Task 1)

**Interfaces:**
- Produces TS: `api/settings.ts`, `api/vat-codes.ts`, `api/journal.ts`, `pages/settings.tsx` (`SettingsPage`), `pages/journal.tsx` (`JournalPage`); host routes `/invoices/settings`, `/invoices/journal`; sidebar entries `navigation.invoiceJournal` (`invoices:access`) and `navigation.invoiceSettings` (`invoices:manage`).

- [ ] **Step 1: The tests: the checklist, the locked start, the rate periods, the gap message**

**Replace** in `apps/invoices/frontend/src/test/fixtures.ts`:

```ts
import type { InvoiceDocument, InvoiceList } from "../api/invoices";
import type { InvoicesMeta } from "../api/meta";

/**
 * GET /meta as the server sends it — a wire literal: a complete seller, the
```

**with**:

```ts
import type { InvoiceDocument, InvoiceList } from "../api/invoices";
import type { InvoiceJournal } from "../api/journal";
import type { InvoicesMeta } from "../api/meta";
import type { InvoiceSettings } from "../api/settings";
import type { VatCode } from "../api/vat-codes";

/**
 * GET /meta as the server sends it — a wire literal: a complete seller, the
```

**Replace** in `apps/invoices/frontend/src/test/fixtures.ts`:

```ts
    ...overrides,
  },
});
```

**with**:

```ts
    ...overrides,
  },
});

/** The settings as the server sends them: a seller lacking two fields, the series locked. */
export const settings = (overrides: Partial<InvoiceSettings> = {}): InvoiceSettings => ({
  legalName: "Kraft-Verket AS",
  organisationNumber: "974760673",
  vatRegistered: true,
  inForetaksregisteret: true,
  addressLine1: "Storgata 1",
  addressLine2: "",
  postalCode: "",
  city: "Oslo",
  country: "NO",
  bankAccount: "",
  iban: "",
  bic: "",
  email: "faktura@kraft-verket.no",
  defaultPaymentTermsDays: 14,
  defaultCurrency: "NOK",
  footerText: "",
  seriesStart: 1000,
  seriesLocked: true,
  missingSellerFields: ["postalCode", "bankAccount"],
  revision: 5,
  updatedAt: "2026-09-12T10:00:00Z",
  ...overrides,
});

/** Code 3 at 25 % with a change to 26 % from 2027, not yet in force. */
export const vatCodes = (): VatCode[] => [
  {
    id: 1,
    code: "3",
    name: "Utgående mva 25 %",
    safTCode: "3",
    ehfCategory: "S",
    active: true,
    inUse: true,
    revision: 1,
    rates: [
      { id: 1001, ratePercent: 25, validFrom: "2026-01-01", validTo: "2026-12-31" },
      { id: 1002, ratePercent: 26, validFrom: "2027-01-01" },
    ],
  },
  {
    id: 5,
    code: "5",
    name: "Fritatt innenlands 0 %",
    safTCode: "5",
    ehfCategory: "Z",
    exemptionReason: "Fritatt for merverdiavgift",
    active: true,
    inUse: false,
    revision: 1,
    rates: [{ id: 1005, ratePercent: 0, validFrom: "2026-01-01" }],
  },
];

/** A month's journal: numbers 1000 to 1002, a credit note signed negative. */
export const journal = (overrides: Partial<InvoiceJournal> = {}): InvoiceJournal => ({
  data: [
    {
      id: 1,
      number: 1000,
      kind: "invoice",
      issueDate: "2026-09-02",
      currency: "NOK",
      buyerName: "Acme Norge AS",
      netTotal: 1000,
      vatTotal: 250,
      grossTotal: 1250,
      vatSummaries: [],
    },
    {
      id: 2,
      number: 1001,
      kind: "invoice",
      issueDate: "2026-09-05",
      currency: "NOK",
      buyerName: "Kari Nordmann",
      netTotal: 200,
      vatTotal: 0,
      grossTotal: 200,
      vatSummaries: [],
    },
    {
      id: 3,
      number: 1002,
      kind: "credit_note",
      issueDate: "2026-09-10",
      currency: "NOK",
      buyerName: "Acme Norge AS",
      netTotal: -400,
      vatTotal: -100,
      grossTotal: -500,
      creditsNumber: 1000,
      vatSummaries: [],
    },
  ],
  pagination: { page: 1, pageSize: 25, totalCount: 3, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
  totals: {
    byCode: [
      { safTCode: "3", category: "S", ratePercent: 25, taxableAmount: 600, vatAmount: 150 },
      { safTCode: "5", category: "Z", ratePercent: 0, taxableAmount: 200, vatAmount: 0 },
    ],
    netTotal: 800,
    vatTotal: 150,
    grossTotal: 950,
  },
  gaps: [],
  gapsTruncated: false,
  seriesStart: 1000,
  counterLast: 1002,
  ...overrides,
});
```

**Create** `apps/invoices/frontend/src/pages/settings.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { meta, settings, vatCodes } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

const path = (input: RequestInfo | URL) => String(input);

const server = () =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url === "/api/v1/invoices/settings")
      return jsonResponse(200, method === "PUT" ? settings({ revision: 6 }) : settings());
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    if (url.startsWith("/api/v1/invoices/vat-codes/1/rates"))
      return jsonResponse(method === "POST" ? 201 : 200, vatCodes()[0]);
    return new Response(null, { status: 404 });
  });

describe("the invoice settings", () => {
  it("lists what issuing still needs and saves the seller with its revision", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    const checklist = await screen.findByRole("list", { name: "What issuing needs" });
    expect(within(checklist).getByText("Postal code is missing")).toBeInTheDocument();
    expect(within(checklist).getByText("Bank account is missing")).toBeInTheDocument();
    expect(within(checklist).getByText("Legal name")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Postal code" }), "0155");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({
      postalCode: "0155",
      revision: 5,
      seriesStart: 1000,
      defaultCurrency: "NOK",
    });
  });

  it("shows the series start read-only once anything is issued", async () => {
    server();
    renderWithProviders(<SettingsPage />);
    const start = await screen.findByRole("textbox", { name: "The number series starts at" });
    expect(start).toBeDisabled();
    expect(screen.getByText("Locked: documents are issued from this series.")).toBeInTheDocument();
  });

  it("shows a code's rate periods, adds one from a date and removes the latest future one", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    const row = (await screen.findByText("Utgående mva 25 %")).closest("tr") as HTMLElement;
    expect(within(row).getByText("25 %")).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "Rate periods" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Jan 1, 2026 – Dec 31, 2026")).toBeInTheDocument();
    expect(within(dialog).getByText("From Jan 1, 2027")).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: "Remove this period" }));
    await waitFor(() =>
      expect(
        fetchMock.actualCalls.some(
          ([url, init]) => path(url) === "/api/v1/invoices/vat-codes/1/rates/1002" && init?.method === "DELETE",
        ),
      ).toBe(true),
    );
  });
});
```

**Create** `apps/invoices/frontend/src/pages/journal.test.tsx`:

```tsx
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { InvoiceJournal } from "../api/journal";
import { jsonResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { journal, meta } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { JournalPage } from "./journal";

const path = (input: RequestInfo | URL) => String(input);

const server = (answer: InvoiceJournal) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = path(input);
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url.startsWith("/api/v1/invoices/journal?")) return jsonResponse(200, answer);
    return new Response(null, { status: 404 });
  });

describe("the invoice journal", () => {
  it("reads this month by default and says in words that the series has no gaps", async () => {
    const fetchMock = server(journal());
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("No gaps between 1000 and 1002.")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => path(url).includes("from=2026-09-01&to=2026-09-12"))).toBe(true);
    expect(screen.getByText("Credit note for 1000")).toBeInTheDocument();
    expect(screen.getByText(/Net NOK\s?800\.00/)).toBeInTheDocument();
  });

  it("names the missing numbers, and warns when the counter ran ahead of every document", async () => {
    server(journal({ gaps: [1004, 1005], counterLast: 1006 }));
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("Missing numbers: 1004, 1005.")).toBeInTheDocument();
    expect(screen.getByText(/The counter is at 1006 but the highest document is 1002/)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: The pages**

**Create** `apps/invoices/frontend/src/api/journal.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/** The invoice journal over a range of issue dates (D11). */
export type InvoiceJournal = Schemas["InvoicesJournalResponse"];
export type InvoiceJournalRow = Schemas["InvoicesJournalRow"];

export const journalQueryOptions = (from: string, to: string, page: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "journal", from, to, page],
    queryFn: ({ signal }) =>
      request<InvoiceJournal>(`/api/v1/invoices/journal?from=${from}&to=${to}&page=${page}`, { signal }),
  });
```

**Create** `apps/invoices/frontend/src/api/settings.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** The seller record and the series start (D2). */
export type InvoiceSettings = Schemas["InvoicesSettingsResponse"];
export type InvoiceSettingsInput = Schemas["InvoicesSettingsRequest"];

export const invoiceSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<InvoiceSettings>("/api/v1/invoices/settings", { signal }),
  });

/** A full replace with the revision the settings were read at. */
export const updateInvoiceSettings = (input: InvoiceSettingsInput): Promise<InvoiceSettings> =>
  request<InvoiceSettings>("/api/v1/invoices/settings", json("PUT", input));
```

**Create** `apps/invoices/frontend/src/api/vat-codes.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** A VAT code with every rate period it has had (D3). */
export type VatCode = Schemas["InvoicesVatCode"];
export type VatCodeRate = Schemas["InvoicesVatCodeRate"];
export type VatCodeCreateInput = Schemas["InvoicesVatCodeCreateRequest"];
export type VatCodeUpdateInput = Schemas["InvoicesVatCodeUpdateRequest"];

export const vatCodesQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "vat-codes"],
    queryFn: ({ signal }) => request<VatCode[]>("/api/v1/invoices/vat-codes", { signal }),
  });

export const createVatCode = (input: VatCodeCreateInput): Promise<VatCode> =>
  request<VatCode>("/api/v1/invoices/vat-codes", json("POST", input));

export const updateVatCode = (id: number, input: VatCodeUpdateInput): Promise<VatCode> =>
  request<VatCode>(`/api/v1/invoices/vat-codes/${id}`, json("PUT", input));

/** The rate-change rule: the open period closes the day before validFrom. */
export const addVatCodeRate = (id: number, ratePercent: number, validFrom: string): Promise<VatCode> =>
  request<VatCode>(`/api/v1/invoices/vat-codes/${id}/rates`, json("POST", { ratePercent, validFrom }));

/** Removes the latest, mistaken period and reopens the one before it. */
export const deleteVatCodeRate = (id: number, rateId: number): Promise<VatCode> =>
  request<VatCode>(`/api/v1/invoices/vat-codes/${id}/rates/${rateId}`, { method: "DELETE" });
```

**Create** `apps/invoices/frontend/src/pages/settings.tsx`:

```tsx
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  List,
  Modal,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCheck, IconPencil, IconPlus, IconTrash, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { type ChangeEvent, useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import { type InvoiceSettings, invoiceSettingsQueryOptions, updateInvoiceSettings } from "../api/settings";
import {
  addVatCodeRate,
  createVatCode,
  deleteVatCodeRate,
  updateVatCode,
  type VatCode,
  vatCodesQueryOptions,
} from "../api/vat-codes";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/** The seller fields issuing needs, in the form's order (D2). */
const requiredSellerFields = [
  "legalName",
  "organisationNumber",
  "addressLine1",
  "postalCode",
  "city",
  "bankAccount",
] as const;

const categories = ["S", "Z", "E", "AE", "G", "O", "K"];

/**
 * Invoice settings (D12), `invoices:manage`'s — the host guards the route: the
 * seller record with the completeness checklist, the series start read-only
 * once anything is issued, and the VAT codes with their rate periods.
 */
export const SettingsPage = () => {
  const { t } = useInvoiceFormat();
  const settings = useQuery(invoiceSettingsQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("invoiceSettings")} description={t("invoiceSettingsDescription")} />
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {refusalMessage(settings.error, t)}
        </Alert>
      )}
      {settings.isPending && <ContentSkeleton rows={6} rowHeight={48} />}
      {settings.data && <SellerForm key={settings.data.revision} settings={settings.data} />}
      <VatCodesSection />
    </Stack>
  );
};

const SellerForm = ({ settings }: { settings: InvoiceSettings }) => {
  const { t } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [values, setValues] = useState(settings);
  const set = <K extends keyof InvoiceSettings>(key: K, value: InvoiceSettings[K]) =>
    setValues((v) => ({ ...v, [key]: value }));
  const save = useMutation({
    mutationFn: () =>
      updateInvoiceSettings({
        legalName: values.legalName,
        organisationNumber: values.organisationNumber,
        vatRegistered: values.vatRegistered,
        inForetaksregisteret: values.inForetaksregisteret,
        addressLine1: values.addressLine1,
        addressLine2: values.addressLine2,
        postalCode: values.postalCode,
        city: values.city,
        country: values.country,
        bankAccount: values.bankAccount,
        iban: values.iban,
        bic: values.bic,
        email: values.email,
        defaultPaymentTermsDays: values.defaultPaymentTermsDays,
        defaultCurrency: values.defaultCurrency,
        footerText: values.footerText,
        seriesStart: values.seriesStart,
        revision: settings.revision,
      }),
    onSuccess: async (saved) => {
      queryClient.setQueryData(invoiceSettingsQueryOptions().queryKey, saved);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: refusalMessage(error, t) }),
  });
  const text = (
    key:
      | "legalName"
      | "organisationNumber"
      | "addressLine1"
      | "addressLine2"
      | "postalCode"
      | "city"
      | "country"
      | "bankAccount"
      | "iban"
      | "bic"
      | "email",
  ) => ({
    label: t(`field.${key}`),
    value: values[key],
    onChange: (e: ChangeEvent<HTMLInputElement>) => set(key, e.currentTarget.value),
  });
  return (
    <Card withBorder>
      <Stack>
        <Title order={4}>{t("seller")}</Title>
        <List spacing={4} size="sm" aria-label={t("completeness")}>
          {requiredSellerFields.map((field) => {
            const missing = settings.missingSellerFields.includes(field);
            return (
              <List.Item
                key={field}
                icon={missing ? <IconX size={14} color="red" /> : <IconCheck size={14} color="green" />}
              >
                {missing ? t("missingField", { field: t(`field.${field}`) }) : t(`field.${field}`)}
              </List.Item>
            );
          })}
        </List>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput {...text("legalName")} />
          <TextInput {...text("organisationNumber")} />
          <TextInput {...text("addressLine1")} />
          <TextInput {...text("addressLine2")} />
          <TextInput {...text("postalCode")} />
          <TextInput {...text("city")} />
          <TextInput {...text("country")} />
          <TextInput {...text("email")} />
          <TextInput {...text("bankAccount")} />
          <TextInput {...text("iban")} />
          <TextInput {...text("bic")} />
          <NumberInput
            label={t("field.defaultPaymentTermsDays")}
            min={0}
            max={365}
            value={values.defaultPaymentTermsDays}
            onChange={(v) => set("defaultPaymentTermsDays", typeof v === "number" ? v : Number(v) || 0)}
          />
        </SimpleGrid>
        <Group>
          <Checkbox
            label={t("field.vatRegistered")}
            checked={values.vatRegistered}
            onChange={(e) => set("vatRegistered", e.currentTarget.checked)}
          />
          <Checkbox
            label={t("field.inForetaksregisteret")}
            checked={values.inForetaksregisteret}
            onChange={(e) => set("inForetaksregisteret", e.currentTarget.checked)}
          />
        </Group>
        <Textarea
          label={t("field.footerText")}
          value={values.footerText}
          onChange={(e) => set("footerText", e.currentTarget.value)}
        />
        <NumberInput
          label={t("field.seriesStart")}
          description={settings.seriesLocked ? t("seriesLocked") : t("seriesStartHint")}
          disabled={settings.seriesLocked}
          min={1}
          value={values.seriesStart}
          onChange={(v) => set("seriesStart", typeof v === "number" ? v : Number(v) || 1)}
        />
        <Group justify="flex-end">
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

type CodeModal = { mode: "create" } | { mode: "edit"; code: VatCode } | { mode: "rates"; code: VatCode } | null;

const VatCodesSection = () => {
  const { t, number, date } = useInvoiceFormat();
  const codes = useQuery(vatCodesQueryOptions());
  const meta = useQuery(invoicesMetaQueryOptions());
  const [modal, setModal] = useState<CodeModal>(null);
  const today = meta.data?.today ?? "";
  const current = (code: VatCode) => code.rates.find((r) => r.validFrom <= today && (!r.validTo || r.validTo >= today));
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t("vatCodes")}</Title>
          <Button leftSection={<IconPlus size={16} />} variant="light" onClick={() => setModal({ mode: "create" })}>
            {t("addVatCode")}
          </Button>
        </Group>
        {codes.isPending && <ContentSkeleton rows={4} rowHeight={36} />}
        {codes.data && (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("code")}</Table.Th>
                <Table.Th>{t("name")}</Table.Th>
                <Table.Th>{t("category")}</Table.Th>
                <Table.Th>{t("safTCode")}</Table.Th>
                <Table.Th ta="right">{t("rateToday")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {codes.data.map((code) => {
                const rate = current(code);
                return (
                  <Table.Tr key={code.id}>
                    <Table.Td>{code.code}</Table.Td>
                    <Table.Td>
                      {code.name} {!code.active && <Badge color="gray">{t("inactive")}</Badge>}
                    </Table.Td>
                    <Table.Td>{code.ehfCategory}</Table.Td>
                    <Table.Td>{code.safTCode}</Table.Td>
                    <Table.Td ta="right">{rate ? `${number(rate.ratePercent, 2)} %` : t("notAvailable")}</Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        <Button size="xs" variant="subtle" onClick={() => setModal({ mode: "rates", code })}>
                          {t("ratePeriods")}
                        </Button>
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("editVatCode", { code: code.code })}
                          onClick={() => setModal({ mode: "edit", code })}
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
      {modal?.mode === "create" && <VatCodeForm onClose={() => setModal(null)} />}
      {modal?.mode === "edit" && <VatCodeForm code={modal.code} onClose={() => setModal(null)} />}
      {modal?.mode === "rates" && (
        <RatePeriods
          code={codes.data?.find((c) => c.id === modal.code.id) ?? modal.code}
          today={today}
          date={date}
          onClose={() => setModal(null)}
        />
      )}
    </Card>
  );
};

const VatCodeForm = ({ code, onClose }: { code?: VatCode; onClose: () => void }) => {
  const { t } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [values, setValues] = useState({
    code: code?.code ?? "",
    name: code?.name ?? "",
    safTCode: code?.safTCode ?? "",
    ehfCategory: code?.ehfCategory ?? "S",
    exemptionReason: code?.exemptionReason ?? "",
    active: code?.active ?? true,
    ratePercent: 25 as number | string,
    validFrom: "2026-01-01" as string | null,
  });
  const save = useMutation({
    mutationFn: () => {
      const own = {
        code: values.code,
        name: values.name,
        safTCode: values.safTCode,
        ehfCategory: values.ehfCategory,
        ...(values.exemptionReason ? { exemptionReason: values.exemptionReason } : {}),
      };
      return code
        ? updateVatCode(code.id, { ...own, active: values.active, revision: code.revision })
        : createVatCode({ ...own, ratePercent: Number(values.ratePercent) || 0, validFrom: values.validFrom ?? "" });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      onClose();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSaveVatCode"), message: refusalMessage(error, t) }),
  });
  return (
    <Modal opened onClose={onClose} title={code ? t("editVatCode", { code: code.code }) : t("addVatCode")}>
      <Stack>
        <TextInput
          label={t("code")}
          value={values.code}
          onChange={(e) => setValues({ ...values, code: e.currentTarget.value })}
        />
        <TextInput
          label={t("name")}
          value={values.name}
          onChange={(e) => setValues({ ...values, name: e.currentTarget.value })}
        />
        <TextInput
          label={t("safTCode")}
          disabled={code?.inUse}
          value={values.safTCode}
          onChange={(e) => setValues({ ...values, safTCode: e.currentTarget.value })}
        />
        <Select
          label={t("category")}
          disabled={code?.inUse}
          data={categories}
          value={values.ehfCategory}
          onChange={(v) => setValues({ ...values, ehfCategory: v ?? "S" })}
        />
        {code?.inUse && (
          <Text size="sm" c="dimmed">
            {t("vatCodeInUseHint")}
          </Text>
        )}
        <TextInput
          label={t("exemptionReason")}
          value={values.exemptionReason}
          onChange={(e) => setValues({ ...values, exemptionReason: e.currentTarget.value })}
        />
        {code ? (
          <Checkbox
            label={t("activeCode")}
            checked={values.active}
            onChange={(e) => setValues({ ...values, active: e.currentTarget.checked })}
          />
        ) : (
          <Group grow>
            <NumberInput
              label={t("ratePercent")}
              decimalScale={2}
              value={values.ratePercent}
              onChange={(v) => setValues({ ...values, ratePercent: v })}
            />
            <DateInput
              label={t("validFrom")}
              valueFormat={t("dateInputFormat")}
              value={values.validFrom}
              onChange={(v) => setValues({ ...values, validFrom: v })}
            />
          </Group>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/**
 * A code's rate periods (D3): every period, adding one from a date — the open
 * one closes the day before — and removing the latest, while it lies in the
 * future.
 */
const RatePeriods = ({
  code,
  today,
  date,
  onClose,
}: {
  code: VatCode;
  today: string;
  date: (d: string) => string;
  onClose: () => void;
}) => {
  const { t, number } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [ratePercent, setRatePercent] = useState<number | string>(code.ehfCategory === "S" ? 25 : 0);
  const [validFrom, setValidFrom] = useState<string | null>(null);
  const done = async () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const fail = (error: Error) =>
    notifications.show({ color: "red", title: t("couldNotChangeRate"), message: refusalMessage(error, t) });
  const add = useMutation({
    mutationFn: () => addVatCodeRate(code.id, Number(ratePercent) || 0, validFrom ?? ""),
    onSuccess: done,
    onError: fail,
  });
  const remove = useMutation({
    mutationFn: (rateId: number) => deleteVatCodeRate(code.id, rateId),
    onSuccess: done,
    onError: fail,
  });
  const latest = code.rates[code.rates.length - 1];
  return (
    <Modal opened onClose={onClose} title={t("ratePeriodsOf", { code: code.code })}>
      <Stack>
        <Table>
          <Table.Tbody>
            {code.rates.map((r) => (
              <Table.Tr key={r.id}>
                <Table.Td>{`${number(r.ratePercent, 2)} %`}</Table.Td>
                <Table.Td>
                  {r.validTo
                    ? t("periodClosed", { from: date(r.validFrom), to: date(r.validTo) })
                    : t("periodOpen", { from: date(r.validFrom) })}
                </Table.Td>
                <Table.Td>
                  {r.id === latest?.id && code.rates.length > 1 && r.validFrom > today && (
                    <ActionIcon
                      color="red"
                      variant="subtle"
                      aria-label={t("removePeriod")}
                      onClick={() => remove.mutate(r.id)}
                    >
                      <IconTrash size={16} />
                    </ActionIcon>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        <Group grow align="flex-end">
          <NumberInput label={t("newRate")} decimalScale={2} value={ratePercent} onChange={setRatePercent} />
          <DateInput
            label={t("validFrom")}
            valueFormat={t("dateInputFormat")}
            value={validFrom}
            onChange={setValidFrom}
          />
        </Group>
        <Group justify="flex-end">
          <Button disabled={!validFrom} loading={add.isPending} onClick={() => add.mutate()}>
            {t("addPeriod")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
```

**Create** `apps/invoices/frontend/src/pages/journal.tsx`:

```tsx
import { Alert, Group, Pagination, Stack, Table, Text, Title } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { IconAlertCircle, IconAlertTriangle, IconCircleCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { journalQueryOptions } from "../api/journal";
import { invoicesMetaQueryOptions } from "../api/meta";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const firstOfMonth = (day: string) => `${day.slice(0, 7)}-01`;

/**
 * The invoice journal (D11, D12): a range of issue dates, the gap check in
 * words, the totals per SAF-T code over the whole range, and the documents in
 * number order a page at a time — credit notes signed negative.
 */
export const JournalPage = () => {
  const { t, money, date, number } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const today = meta.data?.today ?? "";
  const [from, setFrom] = useState<string | null>(null);
  const [to, setTo] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const rangeFrom = from ?? (today ? firstOfMonth(today) : "");
  const rangeTo = to ?? today;
  const journal = useQuery({
    ...journalQueryOptions(rangeFrom, rangeTo, page),
    enabled: Boolean(rangeFrom && rangeTo),
  });
  const data = journal.data;
  const highest = data?.data.length ? Math.max(...data.data.map((d) => d.number)) : undefined;

  return (
    <Stack gap="lg">
      <PageHeader title={t("journal")} description={t("journalDescription")} />
      <Group>
        <DateInput
          label={t("issuedFrom")}
          valueFormat={t("dateInputFormat")}
          value={rangeFrom || null}
          onChange={(d) => {
            setFrom(d);
            setPage(1);
          }}
        />
        <DateInput
          label={t("issuedTo")}
          valueFormat={t("dateInputFormat")}
          value={rangeTo || null}
          onChange={(d) => {
            setTo(d);
            setPage(1);
          }}
        />
      </Group>
      {journal.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadJournal")}>
          {refusalMessage(journal.error, t)}
        </Alert>
      )}
      {journal.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {data && (
        <>
          {data.gaps.length === 0 ? (
            data.data.length > 0 && (
              <Alert color="green" icon={<IconCircleCheck size={16} />}>
                {t("noGaps", { first: data.data[0].number, last: highest })}
              </Alert>
            )
          ) : (
            <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("gapsFound")}>
              {t("missingNumbers", { numbers: data.gaps.join(", ") })}
              {data.gapsTruncated && ` ${t("gapsTruncated")}`}
            </Alert>
          )}
          {data.counterLast !== undefined &&
            highest !== undefined &&
            data.counterLast !== highest &&
            page === data.pagination.totalPages && (
              <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
                {t("counterAhead", { counter: data.counterLast, highest })}
              </Alert>
            )}
          <Title order={4}>{t("totalsByCode")}</Title>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("safTCode")}</Table.Th>
                <Table.Th>{t("category")}</Table.Th>
                <Table.Th ta="right">{t("ratePercent")}</Table.Th>
                <Table.Th ta="right">{t("taxableAmount")}</Table.Th>
                <Table.Th ta="right">{t("vatTotal")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data.totals.byCode.map((c) => (
                <Table.Tr key={`${c.safTCode}-${c.category}-${c.ratePercent}`}>
                  <Table.Td>{c.safTCode}</Table.Td>
                  <Table.Td>{c.category}</Table.Td>
                  <Table.Td ta="right">{number(c.ratePercent, 2)}</Table.Td>
                  <Table.Td ta="right">{money(c.taxableAmount, "NOK")}</Table.Td>
                  <Table.Td ta="right">{money(c.vatAmount, "NOK")}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          <Text fw={600}>
            {t("journalTotals", {
              net: money(data.totals.netTotal, "NOK"),
              vat: money(data.totals.vatTotal, "NOK"),
              gross: money(data.totals.grossTotal, "NOK"),
            })}
          </Text>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("number")}</Table.Th>
                <Table.Th>{t("kind")}</Table.Th>
                <Table.Th>{t("issueDate")}</Table.Th>
                <Table.Th>{t("customer")}</Table.Th>
                <Table.Th ta="right">{t("netTotal")}</Table.Th>
                <Table.Th ta="right">{t("vatTotal")}</Table.Th>
                <Table.Th ta="right">{t("grossTotal")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data.data.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td>{row.number}</Table.Td>
                  <Table.Td>
                    {row.kind === "credit_note" ? t("creditNoteFor", { number: row.creditsNumber }) : t("kindInvoice")}
                  </Table.Td>
                  <Table.Td>{date(row.issueDate)}</Table.Td>
                  <Table.Td>{row.buyerName}</Table.Td>
                  <Table.Td ta="right">{money(row.netTotal, row.currency)}</Table.Td>
                  <Table.Td ta="right">{money(row.vatTotal, row.currency)}</Table.Td>
                  <Table.Td ta="right">{money(row.grossTotal, row.currency)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {data.pagination.totalPages > 1 && (
            <Pagination total={data.pagination.totalPages} value={page} onChange={setPage} />
          )}
        </>
      )}
    </Stack>
  );
};
```

**Replace** in `apps/invoices/frontend/src/i18n.ts`:

```ts
    pdfNotStoredYet:
      "The PDF could not be stored when the document was issued. It is stored the first time it is downloaded.",
    orgNumber: "Org. no. {{number}}",

    "refusal.seller_incomplete": "The seller record is not complete. Fill it in under the settings.",
    "refusal.no_lines": "A document needs at least one line.",
```

**with**:

```ts
    pdfNotStoredYet:
      "The PDF could not be stored when the document was issued. It is stored the first time it is downloaded.",
    orgNumber: "Org. no. {{number}}",

    invoiceSettings: "Invoice settings",
    invoiceSettingsDescription:
      "Who the company is on every document, where the number series starts, and the VAT codes lines are taxed with.",
    failedToLoadSettings: "Could not load the invoice settings",
    couldNotSaveSettings: "Could not save the settings",
    seller: "The seller",
    completeness: "What issuing needs",
    missingField: "{{field}} is missing",
    "field.legalName": "Legal name",
    "field.organisationNumber": "Organisation number",
    "field.addressLine1": "Address",
    "field.addressLine2": "Address line 2",
    "field.postalCode": "Postal code",
    "field.city": "City",
    "field.country": "Country",
    "field.bankAccount": "Bank account",
    "field.iban": "IBAN",
    "field.bic": "BIC",
    "field.email": "E-mail",
    "field.defaultPaymentTermsDays": "Default payment terms (days)",
    "field.vatRegistered": "Registered for VAT (prints MVA after the number)",
    "field.inForetaksregisteret": "Registered in Foretaksregisteret",
    "field.footerText": "Footer text",
    "field.seriesStart": "The number series starts at",
    seriesStartHint: "The first number the first document gets. It locks the moment anything is issued.",
    seriesLocked: "Locked: documents are issued from this series.",
    vatCodes: "VAT codes",
    addVatCode: "Add a VAT code",
    editVatCode: "Edit VAT code {{code}}",
    couldNotSaveVatCode: "Could not save the VAT code",
    code: "Code",
    name: "Name",
    category: "Category",
    safTCode: "SAF-T code",
    rateToday: "Rate today",
    inactive: "Inactive",
    activeCode: "Offered for new lines",
    exemptionReason: "Exemption reason",
    vatCodeInUseHint: "Lines carry this code, so its category and SAF-T code are fixed.",
    ratePercent: "Rate %",
    validFrom: "Valid from",
    ratePeriods: "Rate periods",
    ratePeriodsOf: "Rate periods of {{code}}",
    periodOpen: "From {{from}}",
    periodClosed: "{{from}} – {{to}}",
    newRate: "New rate %",
    addPeriod: "Change the rate from this date",
    removePeriod: "Remove this period",
    couldNotChangeRate: "Could not change the rate",

    journal: "Invoice journal",
    journalDescription: "Every issued document in number order, and the proof that the number series has no gaps.",
    failedToLoadJournal: "Could not load the journal",
    noGaps: "No gaps between {{first}} and {{last}}.",
    gapsFound: "The number series has gaps",
    missingNumbers: "Missing numbers: {{numbers}}.",
    gapsTruncated: "More are missing than are listed.",
    counterAhead:
      "The counter is at {{counter}} but the highest document is {{highest}}: a number was taken without a document.",
    totalsByCode: "Totals per VAT code",
    taxableAmount: "Basis",
    journalTotals: "Net {{net}} · VAT {{vat}} · Total {{gross}}",
    creditNoteFor: "Credit note for {{number}}",

    "refusal.seller_incomplete": "The seller record is not complete. Fill it in under the settings.",
    "refusal.no_lines": "A document needs at least one line.",
```

**Replace** in `apps/invoices/frontend/src/i18n.ts`:

```ts
    pdfNotStoredYet: "PDF-en kunne ikke lagres da dokumentet ble utstedt. Den lagres første gang den lastes ned.",
    orgNumber: "Org.nr. {{number}}",

    "refusal.seller_incomplete": "Selgeropplysningene er ikke fullstendige. Fyll dem ut i innstillingene.",
    "refusal.no_lines": "Et dokument må ha minst én linje.",
    "refusal.delivery_date_missing": "Oppgi leveringsdato eller -periode først.",
```

**with**:

```ts
    pdfNotStoredYet: "PDF-en kunne ikke lagres da dokumentet ble utstedt. Den lagres første gang den lastes ned.",
    orgNumber: "Org.nr. {{number}}",

    invoiceSettings: "Fakturainnstillinger",
    invoiceSettingsDescription:
      "Hvem selskapet er på hvert dokument, hvor nummerserien starter, og mva-kodene linjene avgiftsbelegges med.",
    failedToLoadSettings: "Kunne ikke laste fakturainnstillingene",
    couldNotSaveSettings: "Kunne ikke lagre innstillingene",
    seller: "Selgeren",
    completeness: "Det som trengs for å utstede",
    missingField: "{{field}} mangler",
    "field.legalName": "Juridisk navn",
    "field.organisationNumber": "Organisasjonsnummer",
    "field.addressLine1": "Adresse",
    "field.addressLine2": "Adresselinje 2",
    "field.postalCode": "Postnummer",
    "field.city": "Poststed",
    "field.country": "Land",
    "field.bankAccount": "Kontonummer",
    "field.iban": "IBAN",
    "field.bic": "BIC",
    "field.email": "E-post",
    "field.defaultPaymentTermsDays": "Standard betalingsfrist (dager)",
    "field.vatRegistered": "Registrert i Merverdiavgiftsregisteret (skriver MVA etter nummeret)",
    "field.inForetaksregisteret": "Registrert i Foretaksregisteret",
    "field.footerText": "Bunntekst",
    "field.seriesStart": "Nummerserien starter på",
    seriesStartHint: "Nummeret det første dokumentet får. Det låses i det noe utstedes.",
    seriesLocked: "Låst: det er utstedt dokumenter fra denne serien.",
    vatCodes: "Mva-koder",
    addVatCode: "Legg til en mva-kode",
    editVatCode: "Rediger mva-kode {{code}}",
    couldNotSaveVatCode: "Kunne ikke lagre mva-koden",
    code: "Kode",
    name: "Navn",
    category: "Kategori",
    safTCode: "SAF-T-kode",
    rateToday: "Sats i dag",
    inactive: "Inaktiv",
    activeCode: "Tilbys på nye linjer",
    exemptionReason: "Fritaksgrunn",
    vatCodeInUseHint: "Linjer bruker denne koden, så kategorien og SAF-T-koden ligger fast.",
    ratePercent: "Sats %",
    validFrom: "Gjelder fra",
    ratePeriods: "Satsperioder",
    ratePeriodsOf: "Satsperioder for {{code}}",
    periodOpen: "Fra {{from}}",
    periodClosed: "{{from}} – {{to}}",
    newRate: "Ny sats %",
    addPeriod: "Endre satsen fra denne datoen",
    removePeriod: "Fjern denne perioden",
    couldNotChangeRate: "Kunne ikke endre satsen",

    journal: "Fakturajournal",
    journalDescription: "Alle utstedte dokumenter i nummerrekkefølge, og beviset på at nummerserien ikke har hull.",
    failedToLoadJournal: "Kunne ikke laste journalen",
    noGaps: "Ingen hull mellom {{first}} og {{last}}.",
    gapsFound: "Nummerserien har hull",
    missingNumbers: "Manglende nummer: {{numbers}}.",
    gapsTruncated: "Det mangler flere enn listen viser.",
    counterAhead:
      "Telleren står på {{counter}}, men det høyeste dokumentet er {{highest}}: et nummer er tatt uten dokument.",
    totalsByCode: "Summer per mva-kode",
    taxableAmount: "Grunnlag",
    journalTotals: "Netto {{net}} · Mva {{vat}} · Sum {{gross}}",
    creditNoteFor: "Kreditnota til {{number}}",

    "refusal.seller_incomplete": "Selgeropplysningene er ikke fullstendige. Fyll dem ut i innstillingene.",
    "refusal.no_lines": "Et dokument må ha minst én linje.",
    "refusal.delivery_date_missing": "Oppgi leveringsdato eller -periode først.",
```

**Replace the whole of** `apps/invoices/frontend/src/index.ts` with:

```ts
import "./i18n";

export * from "./api/customers";
export * from "./api/invoices";
export * from "./api/journal";
export { type InvoicesMeta, invoicesMetaQueryOptions, type VatCodeInForce } from "./api/meta";
export * from "./api/settings";
export * from "./api/vat-codes";
export { CustomerPicker, type CustomerPickerProps } from "./components/customer-picker";
export { invoicesCatalog } from "./i18n";
export * from "./lib/errors";
export * from "./lib/format";
export * from "./lib/money";
export * from "./lib/routes";
export { IssueModal, type IssueModalProps } from "./pages/-issue-modal";
export { InvoicePage, type InvoicePageProps } from "./pages/invoice";
export { InvoicesPage, type InvoicesPageProps } from "./pages/invoices";
export { JournalPage } from "./pages/journal";
export { SettingsPage } from "./pages/settings";
```

- [ ] **Step 3: The host: the two routes and the sidebar**

**Create** `apps/host/frontend/src/routes/invoices/journal.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { JournalPage } from "@vantigo/invoices-ui/pages/journal";

export const Route = createFileRoute("/invoices/journal")({
  component: JournalPage,
});
```

**Create** `apps/host/frontend/src/routes/invoices/settings.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/invoices-ui/pages/settings";

export const Route = createFileRoute("/invoices/settings")({
  component: SettingsPage,
});
```

**Replace** in `apps/host/frontend/src/apps.ts`:

```ts
  IconAddressBook,
  IconAdjustments,
  IconBolt,
  IconBriefcase,
  IconCashBanknote,
  IconCategory,
```

**with**:

```ts
  IconAddressBook,
  IconAdjustments,
  IconBolt,
  IconBook,
  IconBriefcase,
  IconCashBanknote,
  IconCategory,
```

**Replace** in `apps/host/frontend/src/apps.ts`:

```ts
      icon: IconFileInvoice,
      requiredPermissions: ["invoices:access"],
    },
  ]),
  moduleApp("communications", "navigation.communications", IconInbox, "/communications", [
    {
```

**with**:

```ts
      icon: IconFileInvoice,
      requiredPermissions: ["invoices:access"],
    },
    {
      // The journal proves the series has no gaps (D11); everyone who reads
      // invoices reads it.
      label: "navigation.invoiceJournal",
      to: "/invoices/journal",
      icon: IconBook,
      requiredPermissions: ["invoices:access"],
    },
    {
      label: "navigation.invoiceSettings",
      to: "/invoices/settings",
      icon: IconAdjustments,
      requiredPermissions: ["invoices:manage"],
    },
  ]),
  moduleApp("communications", "navigation.communications", IconInbox, "/communications", [
    {
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
      "/expenses/reimbursements",
      "/expenses/settings",
      "/invoices",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
```

**with**:

```ts
      "/expenses/reimbursements",
      "/expenses/settings",
      "/invoices",
      "/invoices/journal",
      "/invoices/settings",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
```

**Replace** in `apps/host/frontend/src/apps.test.ts`:

```ts
      ["navigation.expenseApprovals", "/expenses/approvals", ["expenses:approve"], ["expenses:access"]],
      ["navigation.reimbursements", "/expenses/reimbursements", ["expenses:manage"], undefined],
      ["navigation.expensesSettings", "/expenses/settings", ["expenses:manage"], undefined],
    ]);
  });

```

**with**:

```ts
      ["navigation.expenseApprovals", "/expenses/approvals", ["expenses:approve"], ["expenses:access"]],
      ["navigation.reimbursements", "/expenses/reimbursements", ["expenses:manage"], undefined],
      ["navigation.expensesSettings", "/expenses/settings", ["expenses:manage"], undefined],
    ]);
  });

  // Invoices is three destinations: the list and the journal for anyone who
  // reads invoices, the settings for invoices:manage (invoices foundation
  // design D12).
  it("gives Invoices the list, the journal and the settings", () => {
    const invoices = appForKey("invoices");
    expect(invoices).toMatchObject({ module: "invoices", label: "navigation.invoices", home: "/invoices" });
    expect(invoices.requiredPermissions).toEqual(["invoices:access", "invoices:manage"]);
    const items = invoices.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions])).toEqual([
      ["navigation.invoices", "/invoices", ["invoices:access"]],
      ["navigation.invoiceJournal", "/invoices/journal", ["invoices:access"]],
      ["navigation.invoiceSettings", "/invoices/settings", ["invoices:manage"]],
    ]);
  });

```

**Replace** in `apps/host/frontend/src/catalogs/navigation.ts`:

```ts
  "navigation.reimbursements": "Reimbursements",
  "navigation.expensesSettings": "Expense settings",
  "navigation.invoices": "Invoices",
  "navigation.energy": "Energy",
  "navigation.meteringPoints": "Metering points",
  "navigation.meteringPoint": "Metering point",
```

**with**:

```ts
  "navigation.reimbursements": "Reimbursements",
  "navigation.expensesSettings": "Expense settings",
  "navigation.invoices": "Invoices",
  "navigation.invoiceJournal": "Invoice journal",
  "navigation.invoiceSettings": "Invoice settings",
  "navigation.energy": "Energy",
  "navigation.meteringPoints": "Metering points",
  "navigation.meteringPoint": "Metering point",
```

**Replace** in `apps/host/frontend/src/catalogs/navigation.ts`:

```ts
  "navigation.reimbursements": "Refusjoner",
  "navigation.expensesSettings": "Utleggsinnstillinger",
  "navigation.invoices": "Fakturaer",
  "navigation.energy": "Energi",
  "navigation.meteringPoints": "Målepunkter",
  "navigation.meteringPoint": "Målepunkt",
```

**with**:

```ts
  "navigation.reimbursements": "Refusjoner",
  "navigation.expensesSettings": "Utleggsinnstillinger",
  "navigation.invoices": "Fakturaer",
  "navigation.invoiceJournal": "Fakturajournal",
  "navigation.invoiceSettings": "Fakturainnstillinger",
  "navigation.energy": "Energi",
  "navigation.meteringPoints": "Målepunkter",
  "navigation.meteringPoint": "Målepunkt",
```

- [ ] **Step 4: Verify and commit**

**Run**, from the repository root:

```bash
mise exec -- bunx biome check --write apps/invoices/frontend apps/host/frontend/src
mise exec -- bun run --cwd apps/host/frontend test   # also regenerates routeTree.gen.ts
```

```bash
mise exec -- bun run --cwd apps/invoices/frontend typecheck && mise exec -- bun run --cwd apps/invoices/frontend lint && mise exec -- bun run --cwd apps/invoices/frontend test
mise exec -- bun run --cwd apps/host/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```

Commit:

```bash
cat > /tmp/claude-1000/msg-invoices-task11.txt <<'MSG'
feat(invoices-ui): settings with the VAT codes, and the journal

Invoice settings (invoices foundation design D12): the seller record with
the checklist of what issuing still needs, the series start read-only once
anything is issued, and the VAT codes with their rate periods. The journal:
the gap check in words, the per-code totals and the documents in number
order. The host gains both sidebar entries. en and nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
git add -- 'apps/host/frontend/src/apps.test.ts' 'apps/host/frontend/src/apps.ts' 'apps/host/frontend/src/catalogs/navigation.ts' 'apps/host/frontend/src/routeTree.gen.ts' 'apps/host/frontend/src/routes/invoices/journal.tsx' 'apps/host/frontend/src/routes/invoices/settings.tsx' 'apps/invoices/frontend/src/api/journal.ts' 'apps/invoices/frontend/src/api/settings.ts' 'apps/invoices/frontend/src/api/vat-codes.ts' 'apps/invoices/frontend/src/i18n.ts' 'apps/invoices/frontend/src/index.ts' 'apps/invoices/frontend/src/pages/journal.test.tsx' 'apps/invoices/frontend/src/pages/journal.tsx' 'apps/invoices/frontend/src/pages/settings.test.tsx' 'apps/invoices/frontend/src/pages/settings.tsx' 'apps/invoices/frontend/src/test/fixtures.ts'
git commit -F /tmp/claude-1000/msg-invoices-task11.txt -- 'apps/host/frontend/src/apps.test.ts' 'apps/host/frontend/src/apps.ts' 'apps/host/frontend/src/catalogs/navigation.ts' 'apps/host/frontend/src/routeTree.gen.ts' 'apps/host/frontend/src/routes/invoices/journal.tsx' 'apps/host/frontend/src/routes/invoices/settings.tsx' 'apps/invoices/frontend/src/api/journal.ts' 'apps/invoices/frontend/src/api/settings.ts' 'apps/invoices/frontend/src/api/vat-codes.ts' 'apps/invoices/frontend/src/i18n.ts' 'apps/invoices/frontend/src/index.ts' 'apps/invoices/frontend/src/pages/journal.test.tsx' 'apps/invoices/frontend/src/pages/journal.tsx' 'apps/invoices/frontend/src/pages/settings.test.tsx' 'apps/invoices/frontend/src/pages/settings.tsx' 'apps/invoices/frontend/src/test/fixtures.ts'
git show --stat HEAD && git status --short   # nothing of yours left; go.mod/go.sum at the root untracked as before
```

---

### Task 12: Verify the whole branch and open the PR

- [ ] **Step 1: The whole suite, as CI runs it**

```bash
gh run list --branch main --limit 5   # is main already red? say so in the report if it is
cd apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
test -z "$(mise exec -- gofmt -l internal cmd)" && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- golangci-lint run ./...   # depguard: no module imports another; 0 issues and no stderr
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -60; echo "exit ${PIPESTATUS[0]}"
CC=/tmp/claude-1000/zigcc.sh CGO_ENABLED=1 taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/invoices/ ./internal/customers/
mise exec -- go generate ./... >/dev/null 2>&1; cd ../.. && git status --short   # clean but for the root go.mod/go.sum
mise exec -- bun run gen:client && git status --short                                  # still clean
for p in invoices host expenses customers projects time products energy communications; do
  mise exec -- bun run --cwd apps/$p/frontend typecheck && mise exec -- bun run --cwd apps/$p/frontend lint || echo "FAILED: $p"
done
mise exec -- bun run --cwd apps/invoices/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test && mise exec -- bun run gen:client:test
mise exec -- bunx biome check .
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && cd ../..
git status --short -- openapi/COVERAGE.md   # committed in Tasks 1-8: must print nothing
```
`taskset -c 0-3` because the CI runner has four CPUs. Run the frontend packages one at a time, not through `bun --filter`. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so rather than fixing it here. Parallel packages' logs interleave: read a failure's own `--- FAIL` block.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- apps/server/internal/contracts apps/server/internal/customers apps/server/internal/module
git diff main..HEAD -- openapi/testdata/exchanges openapi/customers.yaml   # must print nothing
git diff main..HEAD -- 'openapi/*.yaml' | grep -c '^+.*operationId'          # 18, all in invoices.yaml
grep -rn '"github.com/vantigo-io/vantigo/server/internal/\(customers\|projects\|expenses\|time\|energy\|products\|communications\|identity\)' apps/server/internal/invoices/   # must print nothing
grep -rn 'CURRENT_DATE\|now()::date\|SetFloat64' apps/server/internal/invoices/   # must print nothing
cd apps/server && mise exec -- go test -count=1 -run 'TestNoModuleReferencesAnotherModulesSchema|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestServeMuxConflictsArePinned|TestInvoices' ./internal/db/ ./internal/openapi/ && cd ../..
```
Check, by eye: the spec commit, the plan commit and eleven task commits, each trailer exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; the root `go.mod`/`go.sum` still untracked; one migration, `00034`; `contracts.CustomerBillingProfile` changed only by the two new fields; every `customerProfile`/`customerEntries`/`object*` call outside `withLockedTx` (the harness's locked-call check has already proven it on every test); the fonts' SHA-256 as Task 5 lists them.

- [ ] **Step 3: Open the PR**

```bash
git push -u origin feat/invoices-foundation
cat > /tmp/claude-1000/pr-invoices-foundation.md <<'MSG'
## Invoices — the sales document (phase 1A)

Vantigo issued nothing. After this it issues the lawful Norwegian sales
document: a draft becomes a numbered, immutable invoice or credit note, its
PDF is stored once and downloaded as stored, and a journal proves the series
has no gaps. Decided in `docs/superpowers/specs/2026-09-26-invoices-foundation-design.md`
(D1–D13); the reference is `docs/invoices.md`.

- **A new module** `invoices` (`internal/invoices`, `openapi/invoices.yaml`,
  `@vantigo/invoices-ui`), requiring customers; four permissions, two of them
  sensitive, held by no built-in role.
- **The seller record and one series** — invoices and credit notes, continuous
  across years — allocated from a counter row inside the issue transaction, so
  a refused issue gives its number back; the start locks at the first issue.
- **VAT codes with dated rate periods**, seeded with the SAF-T output codes
  (6 is E, 7 is O); a rate change is a new period, never before the latest
  issued document.
- **Drafts** with the billing profile's prefills and the customer gates
  (merged, archived, blocked for invoicing); exact decimals, gross less
  allowance per line, VAT per rate on the sum of the nets; NOK only.
- **Issue** in one serialised transaction: document → settings → counter →
  original; every rule checked after the allocation; the issue-date rule with
  § 5-1-3's previous-month exception (calendar day ≤ 15, stricter than the
  law); immutability enforced by triggers too.
- **The PDF** in Noto Sans (SIL OFL), rendered from the snapshot only, stored
  once under a key named by its hash, never re-rendered; a watermarked preview.
- **Credit notes** from the original's snapshot, capped per line and on the
  headline under the original's lock, skipping every gate meant for new invoices.
- **The journal** with the gap check; **both customer slots** (a merge
  re-points, anonymisation erases drafts only and keeps documents under § 13).
- **The customers contract**: `Status` and `MergedInto` on
  `CustomerBillingProfile`, so `disabled` now means blocked for invoicing.

Contract: one new file with 18 operations; no existing OpenAPI schema touched;
no corpus touched (invoices has none; `RequireCoverage` gates it). An
installation with `MODULES` unset gets Invoices on upgrade (release note in
`deploy/compose/README.md`). Phase 1A alone meets neither the B2G nor the 2027
B2B e-invoicing duty — that is phase 2.

Decisions on the record for review: listed in the plan's preamble (one
conflict schema with the code; "store configured"; `today` on meta;
`allowedIssueDates` on a draft; draft totals at today's rates; PUT's rules;
what a credit draft keeps; credit amounts on issued invoices only; the rate
operations take the settings lock; fixed seed ids; the PDF tested through
its model; Content-Disposition set outside the contract; the merge holder's
clock; the literal gap range; no notices file or changelog; vendored fonts;
sqlc.arg; two frontend tasks).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
MSG
gh pr create --base main --head feat/invoices-foundation \
  --title "Invoices — the sales document (phase 1A)" \
  --body-file /tmp/claude-1000/pr-invoices-foundation.md
gh pr checks --watch
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -F body=@/tmp/claude-1000/pr-invoices-foundation.md`. Watch CI to green; a red check is fixed on the branch with a new commit (never `--amend`). Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL and CI's state; each test shown able to fail and what the mutation printed; anything a generator disagreed with this plan about; whether `main` was already red; and the eighteen readings of the spec in the preamble, for the user's verdict.

---

## Self-review

**Spec coverage** — every decision and testing bullet maps to a step:

| Spec | Where |
| --- | --- |
| D1 the module, its four permissions, `invoices requires customers`, the checklist of `docs/module-boundaries.md` | Task 1 (every step); `TestPermissions_AreTheCatalogTheDesignNames`, `TestMount_RefusesAnInstallationWithoutCustomers`, `TestLoad_Modules`; Task 11 Step 3 (the sidebar) |
| D1 the Oslo business day from `Deps.Clock()`, never `CURRENT_DATE` | Task 1 `values.go`; `TestMeta_TodayIsTheBusinessDayInOslo`; Task 12 Step 2's grep |
| D1 `customers:view` for the picker; the API checks none | Task 10 (`-invoice-access.tsx`, `canViewCustomers`); `-invoices-list.test.tsx`; the list test "offers New invoice only to…" |
| D2 the settings row, both mod-11 checks, IBAN/BIC, NOK only, the revision, completeness, the counter, `series_locked` after `FOR UPDATE` | Task 1 migration; Task 2 `settings.go`; `TestSellerNumbers_TheCheckDigitRules`, `TestSettings_*`; Task 4 `TestIssue_ASettingsReplaceRacingTheFirstIssueWaitsAndIsRefused` |
| D2 no reserved column names | `TestInvoicesSchema_NamesNoColumnWithAReservedWord` |
| D2 `GET /meta` | Task 1; `TestMeta_*` |
| D3 codes and dated periods, the exclusion, the seed (6 E, 7 O), rate rules per category, in use, deactivation, the rate-change rule and period removal | Task 1 migration, `TestInvoicesBaseline_AppliesAndIsIdempotent`; Task 2 `vatcodes.go`; `TestVatCodes_*`; Task 4 `TestIssue_TheSeriesStartAndARateChange` (old rate the day before, new after) |
| D4 the tables, delivery CHECK, snapshots, drafts, prefills, the gates in order, validation, warnings, preview, the list, the response | Task 1 migration; Task 3 `drafts.go`, `responses.go`, `list.go`; `TestDrafts_*`, `TestList_*`; Task 4 `TestIssue_TheSnapshots`; Task 5 `TestPDF_ThePreview` |
| D5 the float path, decimals per field, gross − allowance, VAT per rate, bounds, NOK only, `vat_total_nok` | Task 3 `money.go`, `decimal.go`; `TestDrafts_TheMoney`; `TestIssue_AnInvoice`; frontend `lib/money.test.ts` |
| D6 the order before and inside the transaction, every refusal, the issue-date rule and the exception, `issued_late`, the lock order | Task 4 `issue.go`, `issuedate.go`; `TestIssue_*` (the date rule, the refusals, `invoice_changed`, no store, racing numbers, racing dates, a failure after allocation, the settings race, no deadlock) |
| D7 maroto v2, the fixed defaults, the font, store-once, the key, the download's failure modes, the preview, the layout | Task 5; `TestPDFModel_*`, `TestRenderPDF_IsReproducible`, `TestPDF_*` |
| D8 the credit draft, what it may change, both caps under the lock, what it skips, links | Task 6; `TestCredit_*` |
| D9 the triggers | Task 1 migration; `TestInvoicesBaseline_…` (they exist); `TestIssue_AnIssuedDocumentIsImmutableInSQL` |
| D10 the contract change; `RepointCustomer`; the export and the erase | Task 3 Step 1 (`TestDirectory_BillingProfile_CarriesStatusAndMergedInto`); Task 7; `TestCustomerReferences_*`, `TestCustomerPersonalData_*` |
| D11 the journal | Task 8; `TestJournal_*` |
| D12 the frontend | Tasks 1, 10, 11 |
| D13 the docs, checked against the code | Task 9 |
| Out of scope | nothing in Tasks 1–11 adds payments, delivery, EHF, KID, a CSV, a dashboard card, a customer tab, a logo, a purge or a sweeper |

**Placeholder scan.** Every step carries its code, SQL, yaml, test and command, or the exact command that writes a generated file. No "TBD", no "similar to Task N".

**Name consistency.** SQL: `invoices.settings/counters/vat_codes/vat_code_rates/invoices/lines/vat_summaries`, `refuse_issued_document_change`, `refuse_issued_child_change`, `ex_vat_code_rates_no_overlap`, `ux_vat_codes_code_lower`, `ux_invoices_number`. Go: `customerGate`, `invoiceIssued`, `parseDraft`, `computeLine`, `summarize`, `allowedIssueDates`, `issuedLate`, `invoiceIssueChecks`, `creditIssueChecks`, `creditCaps`, `storeOnce`, `buildPDFModel`, `renderPDF`, `customerReferenceHolder`, `customerPersonalData`. Wire: `InvoicesConflictProblem.{code,mergedInto,linePosition,allowedIssueDates}`, every operationId `*Invoices*`. Kinds: `invoices.invoices`, `invoices.drafts`, `invoices.documents`. TS: `INVOICE_ROUTE_PATH`, `lineAmounts`, `documentTotals`, `refusalMessage`, `useInvoiceAccess`.

**The scratch run.** The plan was written from a working implementation and then applied, mechanically, to a fresh copy of the spec commit `d77fc256` (`cp -r` into `/tmp/claude-1000/plan-apply/`, never `git worktree`) by a script (`/tmp/claude-1000/gen/apply_plan.py`) that performs every instruction in order — 89 Create, 94 Replace (each anchor asserted to occur exactly once at the moment it is applied), 11 Append, 6 Replace-the-whole-of, 10 Insert into `openapi/invoices.yaml` and all 24 Run blocks (the `cp`s, `curl` + `sha256sum -c` of the fonts, `go get` and `go mod tidy`, `go generate ./...`, the coverage report, `bun install`, `bun run gen:client`, the depguard and eslint scripts, `biome --write`, the host test run that regenerates `routeTree.gen.ts`). The result was **byte-identical** to the implementation tree (`diff -rq` excluding `node_modules` and `.git`), generated files, `go.mod`/`go.sum`, the fonts, `bun.lock` and `routeTree.gen.ts` included. On that tree, with `TEST_DATABASE_URL` on port 55442: `go generate ./...` (exit 0; a second run and `bun run gen:client` changed nothing), `gofmt -l` empty, `go build ./...` and `go vet ./...` (exit 0), `golangci-lint run ./...` (0 issues), `taskset -c 0-3 go test -count=1` over `internal/invoices/...`, `internal/customers/...`, `internal/db/...`, `internal/openapi/...`, `internal/module/...`, `internal/integration/...`, `internal/config/...` and `cmd/...` (all ok), `@vantigo/invoices-ui` typecheck, lint and tests (5 files, 22 tests), the host's tests (46 files, 324 tests, run by the plan itself) and `translations:check` all passed. On the implementation tree before that: the whole `go test ./...` on four CPUs (every package ok), `go test -race` over `internal/invoices` through `zig cc` on four CPUs (ok), every frontend package's lint, `gen:client:test`, `i18n:test` and `biome check .` (clean).

**Tests shown able to fail** (each mutation run, seen red, restored): the series lock removed → `TestSettings_TheSeriesStartLocksAtTheFirstIssue`; VAT summed per line instead of per rate → `TestDrafts_TheMoney`; the per-line credit cap disabled → `TestCredit_ThePerLineCap` and `TestCredit_RacingCreditNotesKeepTheCap`; the gap range starting at the first number instead of the one before → `TestJournal_TheGapCheck`; the issue-date check removed → `TestIssue_TheIssueDateRule`; a directory call marked as made under a lock → the harness's locked-call check failed `TestDrafts_CreateFillsWhatItWasNotTold`; `resolved.Status` not set → `TestDirectory_BillingProfile_CarriesStatusAndMergedInto`.

**What the scratch run changed in this plan** before it passed: sqlc rejects `@series_start::bigint + 1` ("syntax error at or near N"), so the counter and the journal use `sqlc.arg(...)`; the credit copy's `INSERT … SELECT` needed its source aliased (`column reference "invoice_id" is ambiguous`); a `Content-Disposition` declared as a required response header failed every download's contract validation (contracttest passes kin-openapi only `Content-Type`), so it is set by a `Visit` wrapper; Mantine's `Textarea autosize` throws in jsdom, so the notes are plain `Textarea`s; `<Anchor component={Link} {...options}>` does not type-check against an untyped route, so `DocumentLink` navigates; a VAT-code `Select` is found as a `combobox`; a route helper component moved to `test/invoice-route.tsx` for `react-refresh/only-export-components`; `gofmt` realigned `contracts.CustomerBillingProfile`; staticcheck's De Morgan rewrite in `validIBAN`; the gap test's first expectation was wrong (the literal range checks only the number before the first); an unused helper went; the `i18next` type import was replaced by a local `Translate` type.

The scratch copies were deleted afterwards.
