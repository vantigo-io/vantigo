# Invoices — work becomes invoices (phase 3) Implementation Plan

> **For agentic workers:** implement this plan task by task, one implementer per task and a reviewer after it, never in the same context. Steps use checkbox (`- [ ]`) syntax for tracking. A pre-flight review of this plan precedes Task 1.

**Goal:** Phase 3 of the Invoices module on the branch `feat/invoices-work-to-invoices`: the third sanctioned cross-module write — `contracts.InvoicedWorkHolder` (module-boundaries rule 10) — through which the invoices issue stamps Time's entries, Expenses' lines and Projects' billing milestones with the invoice's id and number inside its own transaction, and the credit note that returns a source's line in full releases it; three line-level read contracts (`BillableHours`, `BillableExpenses`, `BillableMilestones`) and Expenses' one ready predicate as an SQL function; `invoices.line_sources`, a hold from the draft that every save carries; the uninvoiced view and the wizard with five groupings and bilingual line text; per-kind VAT codes; the project dimension (BT-11); release on full return; a-konto and the final settlement with deduction lines, the cap per (a-konto, VAT code) and `BillingReference` 0..n; the timesheet inside the PDF; the race and integration tests; four modules' OpenAPI contracts, the apps and the documentation. Four migrations, one per schema (`00037`–`00040`); three new Invoices operations (`getInvoicesWork`, `postInvoicesFromWork`, and `getInvoicesByIdDeductible` by reading 31).

**Architecture:** the contract lives in `srv/contracts/{invoiced_work.go,billable.go}`; `module.Compose` and `module.Workers` collect the new many-provider slot `Deps.InvoicedWork` from every module given and put the invoiced-work providers' `CustomerReferences` and `CustomerPersonalData` after every other module's (a stable partition), and resolve the three single-provider `Billable*` slots after `Projects`. Each source module implements its holder and its read in its own package over its own `queries/`, building nothing but a logger from `Deps`, so a disabled module still stamps and releases. Inside `srv/invoices`: `sources.go` (the hold carried through saves, the `sources` block, freshness, `refreshSources`), `writeback.go` (the holder calls, `noteTxCommand`), `work.go` (the view and `from-work`), `grouping.go` (D4's lines and text), `deductions.go` (D7), `timesheet.go` (D5). `withLockedTx` hands its callback the `pgx.Tx`; the holders run on it after every check and the number and before any write of the issue's step 6; no directory, store or provider call ever runs under it. The integration package proves the lock order against the real modules on a pool of two.

**Tech Stack:** Go 1.27 (pgx, sqlc 1.31.1, goose, oapi-codegen v2.8.0 strict server, `encoding/xml`, maroto v2.4.2), PostgreSQL 18, Temurin 21 + Saxon-HE 12.7 (the EHF oracle, CI only), React + Mantine + TanStack, vitest, bun, mise, Astro Starlight (docs).

**Spec:** `docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md` (D1–D19, the 38 readings, Testing, Phasing 3A/3B/3C) — binding, as amended by `994529c3` and by this plan's pre-flight (the commit carrying this revision: `numeric(22,8)`, held work listed with `heldBy`, `getInvoicesByIdDeductible`, `releasedSources` and `InvoicesLine.warnings`, the save's source bound, from-work's order, the `NOWAIT` proofs, the Projects release never refusing, `Deps.Logger`, `person_label varchar(200)`; and before it the `issueAfterAllocation(ctx, tx, invoiceID)` proof hook; the race pool of two with every raw lock-holder on its own `pgx.Connect`; a release's `InvoiceRef` carrying the credit note's issue time and issuer; `WorkSource.ExpenseKind` and `line_sources.source_subkind`; "fixed-price or non-billable" under the lock). Research: `docs/superpowers/research/2026-10-05-invoices-work-to-invoices.md` (§7 the codebase, §10 the sequencing). The shapes this copies: rules 8 and 9's holders (`srv/invoices/customer_slots.go:44-69`, `srv/projects/customer_references.go`), the `ehf` block answered from own rows (`srv/invoices/transmissions.go:51-129`), the `vat_code_not_valid` warning/refusal pair (`srv/invoices/responses.go:31-34`, `srv/invoices/issue.go:186-190`), `expenses.owes_employee` (`srv/db/migrations/00033_expenses_supplier_invoices.sql:20-52`), the contract-call recorders (`srv/invoices/harness_test.go:82-165`, `srv/time/harness_test.go:87-101`, `srv/expenses/harness_test.go:98-121`, `srv/projects/harness_test.go:131-156`), `srv/integration/{invoices_test.go:70,figures_test.go:258,merge_test.go}`, and phase 2's plan for this document's own shape.

`srv/` is `apps/server/internal/`, `inv/` is `srv/invoices/`, `mig/` is `srv/db/migrations/`, `MB` is `docs/src/content/docs/en/contributing/module-boundaries.md`, `R/` is `docs/src/content/docs/en/reference/`, `U/` is `docs/src/content/docs/{en,nb}/user/`. Line numbers are against `994529c3` (the code is unchanged since the spec's `d259a4df`).

## Global Constraints

- Branch `feat/invoices-work-to-invoices`, cut from `main` at `361cdff6`; HEAD carries the research, the spec (`f530c72b`, `a9497392`, `994529c3`) and this plan. Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable` for every `go test` (`docker compose -f docker-compose.test.yml up -d --wait`; colima must be running).
- Toolchain through mise only. After any `openapi/*.yaml`, `queries/*.sql`, `sqlc.yaml` or migration change: `cd apps/server && mise exec -- go generate ./...` (never package-scoped), twice; from the root `mise exec -- bun run gen:client`; when an operation is added, `cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`. Commit every generated file.
- **Every server task that changes a module's wire patches that module's frontend fixtures in the same commit** — `apps/invoices/frontend/src/test/fixtures.ts`, `apps/time/frontend/src/test/fixtures.ts`, `apps/expenses/frontend/src/test/fixtures.ts`; Projects has no fixtures file, so any page test that spells a whole milestone — and runs `mise exec -- bun run --cwd apps/<module>/frontend typecheck`.
- Invoices has no corpus; `RequireCoverage` gates every operation in the module's own suite.
- **The documentation changes with the code (AGENTS.md).** Each task updates the pages its change touches in the same commit: `R/<module>.md` for a rule, a column, a contract or an endpoint; `U/<module>.md` in **both** `en/` and `nb/` for anything a screen or the generated client changes (every task that edits `openapi/<module>.yaml` touches `apps/<module>/frontend/src/api-schema.d.ts`, which `U/<module>.md` covers — such a task adds at least its sentence to both language pages); `MB` for anything under `srv/module` or `srv/contracts`. **The gate is per commit:** the branch carries `Docs-Impact: none` trailers, and the checker waives everything in `mergeBase..HEAD` when it sees one, so each task runs `mise exec -- bun run tools/docs/check-coverage.ts --base HEAD~1` right after its commit (and `mise run docs:check` for the build and the en/nb parity); a failure is a follow-up commit, never an amend. Reviewers re-run it. A commit that changes nothing documented carries `Docs-Impact: none — <reason>`.
- No other module's Go changes than these: `srv/contracts`, `srv/module`, `srv/modtest` (Task 1; `WithPoolMaxConns` in Task 13); `srv/time` (Task 2), `srv/expenses` (Task 3), `srv/projects` (Task 4) — and nothing of theirs after their task; `srv/db` (migrations and schema tests); `srv/integration` (Task 13). `srv/customers` is **not** touched: the merge's new order is Compose's partition (`srv/module/compose.go:247-264`), the merge's loop stays (`srv/customers/merge.go:387-393`). `cmd/vantigo` is not touched.
- **The lock rule, restated (D1, reading 2):** *no call that takes its own connection or leaves the process while a transaction holds locks.* A directory read takes a second pool connection and an object-store, SMTP, lookup or provider call leaves the process, so none is ever made inside `withLockedTx` (Invoices) or a source module's locked transaction; every one goes through `contractscalls.go` and `noteContractCall`. A holder's command runs on the caller's `pgx.Tx` and does neither: it is the one call allowed under a lock, reported through `noteTxCommand`, and only under one. Whatever a decision under the lock needs from a directory is read before the transaction and re-checked under it.
- **The cross-module lock order (D1, D7, readings 3–5):** Invoices' own first — **the document** (`LockInvoice`, `FOR UPDATE`) → **the settings row** (`ShareSettings`, `FOR SHARE`) → **the number counter** (`AllocateNumber`) → for a credit note **its original** (`LockInvoice`) — and then the source modules in one fixed order, `contracts.InvoicedWorkOrder`: **Projects** (each distinct project `LockProject`, `FOR NO KEY UPDATE`, by id ascending; then the milestones by id ascending) → **Expenses** (the claims of claim lines by id, then every line by id — `LockClaims`, `LockBatchEntries`, `srv/expenses/queries/flow.sql:18-48`) → **Time** (`LockEntries`, by id, `srv/time/queries/entries.sql:131-139`). No source module ever locks an `invoices` row. A transaction that locks an invoices document and a source row takes the document first — so the customers merge takes the invoices documents (newest first, `inv/queries/customers.sql:1-14`) before any project row, by Compose's partition. **The deducted invoices of a settlement are read under the counter and never row-locked, nor FK-locked by a save or a credit's creation** (reading 4 of the spec, amended at the Task 11 review: `lines.deducts_invoice_id` carries no foreign key). A wizard or a save locks the draft only.
- **`withLockedTx` holds every write that locks**; from Task 7 its callback is `fn(ctx context.Context, tx pgx.Tx, txq *store.Queries) error`, its comment (`inv/server.go:102-109`) carrying the restated rule. The one-statement rule: every save inserts the document's `line_sources` in **one statement ordered by `(source_kind, source_id)`**.
- **The clock rule:** every stamp and every release is timestamped `InvoiceRef.IssuedAt`, the issue's own `s.deps.Clock()` read once and also written as `IssueDocument`'s `Now` — never a holder's clock, never SQL `now()` or `CURRENT_DATE`. "Today" (D12's deadline, the view's `until`) is `businessDay(s.deps.Clock())`.
- **Shared hand-written files have owners** (Parallelism below): a conflict in `MB`, `srv/db/schema_test.go`, `srv/modtest/modtest.go` or the invoices app's `test/fixtures.ts`/`pages/settings.tsx` is resolved by the coordinator keeping both sides, then `mise run docs:check` and the suites; a generated file is regenerated, never merged.
- **Exact decimal:** `big.Rat` and `numeric` inside; decimal text across every contract; JSON numbers on the wire. Amounts are compared **by value** — `big.Rat.Cmp` or SQL numeric equality — never as text (`"125.41250000"` and `"125.4125"` are one amount).
- **One refusal rule:** warnings never refuse; an issue refusal is `cannotIssue(code, detail)` with `LinePosition` (and, for a source, `SourceKind`/`SourceId`) and rolls back the number and every holder's write; a holder answers a source it will not stamp only as `*contracts.WorkSourceRefusal`, anything else is a 500 with an error log.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec; **one committer at a time per tree** (the main tree and each worktree has one index and one committer; unstage your files if you must yield it); keep every touched TS file formatted continuously (the hook runs biome over the repository).
- Every commit message ends with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; a docs-only or test-only commit carries `Docs-Impact: none — <reason>` above it. Scopes `contracts`, `time`, `expenses`, `projects`, `invoices`, `integration`, `invoices-ui`, `time-ui`, `expenses-ui`, `projects-ui`, `frontend` (the host), `docs`.
- Every new test is shown able to fail (its guard removed, its assertion inverted, or its order swapped); the report says how.
- **Finishing gates** before any task is called done: `mise run server:check`, `mise run server:test`, `mise run frontend:check` (for a task touching a frontend), `mise run docs:check`, and from Task 9 on `mise run ehf:validate` whenever a golden changed.

**Parallelism.** Task 1 runs alone. After it, **Tasks 2, 3 and 4 run side by side in worktrees** (each its own module, its own migration number fixed here, its own schema-test file), and Task 5 may join them (invoices schema only, migration `00040`). Task 6 follows 5; Task 7 follows 6 (it changes every `withLockedTx` caller, so nothing runs beside it). **Tasks 8 and 9 run side by side after 7.** Task 10 follows 8 and 9; Task 11 follows 10; Task 12 follows 11 (both edit `pdf.go`, `drafts.go`, `credits.go`'s neighbours and the settings). Task 13 follows 12. **Task 14 may start in a worktree once Task 8 has committed its `apps/invoices/frontend/src/api-schema.d.ts`** (the panel, the wizard, the source badges against Tasks 2–4's wire), rebases onto Tasks 9–12's wire before it commits, and commits after Task 13; Task 15 follows 14; Task 16 last. A worktree task commits on its own branch `wt/task-<n>`; **it is brought onto the tip by rebasing that branch onto `feat/invoices-work-to-invoices` inside its own worktree, by its own committer, and then fast-forwarding the feature branch** (a cherry-pick by the coordinator is the fallback). Wherever a rebase or cherry-pick conflicts on a generated file it is regenerated (`go generate` twice, `gen:client`), never resolved by hand, and there is never a merge commit. **Hand-written files several tasks touch have owners**, and a conflict in one is resolved by the coordinator keeping both sides, then `mise run docs:check`: `MB` (Tasks 1 and 7), `srv/db/schema_test.go` (Tasks 3 and 5), `srv/modtest/modtest.go` (Tasks 1 and 13), `openapi/COVERAGE.md` (Tasks 8 and 11; generated — regenerate), `apps/invoices/frontend/src/test/fixtures.ts` and `pages/settings.tsx` (Tasks 6–12 for the wire, Task 14 for the screens). **After every rebase, cherry-pick or fast-forward:** generate twice with no drift, `mise run server:check`, the touched modules' suites and `srv/db`'s, and `check-coverage.ts --base HEAD~1`. The one-committer rule holds in every tree.

---

**How this plan reads the spec where it leaves a choice open**, each for the user's verdict (Task 16 repeats them):

1. **Four migrations, one per schema, numbered in commit order:** `00037_time_invoiced_by.sql`, `00038_expenses_invoiced_by.sql`, `00039_projects_invoiced_by.sql`, `00040_invoices_work.sql`. A migration's owner is its file name's second segment (`srv/db/schema_test.go:40-47`) and `TestNoModuleReferencesAnotherModulesSchema` (`:120-139`) refuses a file naming another module's schema, so no multi-schema file is possible; the numbers are fixed now so Tasks 2–5 can run in parallel worktrees without colliding, and the invoices one is `00040`, not `00037`, because goose refuses an older version applied after a newer one on any database that ran Tasks 2–4 first.
2. **`00040` carries 3C's schema too** — `timesheet_rows`, `invoices.timesheet`, the two `timesheet_*` settings, `lines.deducts_invoice_id` and the relaxed CHECK — so the invoices schema changes once; Tasks 11 and 12 expose them on the wire and give them behaviour. Until then the columns sit at their defaults and nothing reads them.
3. **`line_sources.amount` is `numeric(22,8)`** (the spec amended to match by this plan's pre-flight): Time's exact amount is `hours numeric(5,2) × bill_rate numeric(12,2) × (bill_multiplier_percent numeric(6,2) × 0.01)` (`srv/time/queries/actuals.sql:57`), which carries up to **eight** decimals (1.25 × 100.33 × 1.5025 = 188.43228125); stored at six, the holder's exact comparison (the spec's reading 31) would answer `source_changed` for every such entry.
4. **Amounts are compared by value**: the holders compare `WorkSource.Amount` with their own figure as `big.Rat` (Time: `hours × bill_rate × COALESCE(multiplier, 100)/100` from the locked row's numerics; Expenses: `bill_amount`; Projects: a new `milestoneEffectiveAmountRat`, the effective amount as a `big.Rat` rounded half up to cents by `percentOfPrice`'s rule, `srv/projects/milestones_validation.go:475-505`, `:666-678` — the one figure behind `BillableMilestone.Amount` (`FloatString(2)`), the holder's comparison and the frozen `invoiced_amount`), never as text.
5. **A holder's constructor takes `Deps.Logger` and nothing else** (the release's tolerance warning); every timestamp it writes is `IssuedAt`, so it needs no clock. A disabled module's `Deps` carries the logger.
6. **`contracts.InvoicedWorkOrder`** is the lock order as data (`[]WorkSourceKind{projects.milestone, expenses.entry, time.entry}`); Invoices calls the holders in it, Compose refuses two holders claiming one kind.
7. **`line_sources.invoice_id` cannot disagree with its line**: a new `uq_lines_id_invoice UNIQUE (id, invoice_id)` on `invoices.lines` lets `line_sources` and `line_releases` reference `(line_id, invoice_id)` / `(credit_line_id, invoice_id)` as composite foreign keys, cascading.
8. **`refuse_issued_line_source_change` is tighter than the spec states:** an INSERT must be `held`; under a draft an UPDATE may only move `held → invoiced` with nothing else changed (the issue's own step, while the parent is still a draft); under an issued parent only `invoiced → released`, nothing else changed; a DELETE under an issued parent is refused.
9. **Warnings stay `[]string` codes** (`inv/responses.go:256`; the spec amended to match): `sources_released`'s identities ride in `releasedSources: [{kind, id}]` on the save's answer; `line_differs_from_sources`, `source_changed` and `source_not_invoiceable` per line in `InvoicesLine.warnings`, and once each in the document's `warnings`.
10. **The `sources` block** is each line's `sources[]` (`InvoicesLineSource {kind, id, projectId, date, quantity, amount, state}`) plus the document's `sources: {count, held, invoiced, released, wouldRelease?}` — all from `line_sources`, never a live read; `wouldRelease` (`[{kind, id}]`) only on a credit-note draft.
11. **Freshness cannot tell "already invoiced" from "no longer invoiceable"**: both are absent from a `Billable*` read by id, so `GET` warns `source_not_invoiceable`; the issue names the exact code. A kind whose provider is nil is not judged on `GET` (no warning) — the issue still fails closed.
12. **The view lists held work, not selectable** (the spec amended to match D18): each such row carries `heldBy {invoiceId, number?, status}` and stays out of the totals; each (project, kind) group carries `heldOnDrafts: [{invoiceId, count}]`.
13. **A milestone's `source_date` is its `ready_at` Oslo business day** (D12 measures it from `ready_at`); an hour's and an expense's is their own date.
14. **The project reference on a save:** the derived `project_id` is the one project all the draft's held rows share after the save; its code comes from the stored `project_reference` when the project is unchanged, else from `ProjectDirectory.Projects` read before the save's transaction (made only when the held rows span two or more projects, the one case a `PUT` can change the derived project); with Projects disabled or the project gone, both columns are NULL (`ck_invoices_project`). The wizard sets both from its own directory read.
15. **On a credit-note draft a line's `deducts_invoice_id` is derived from the original line it credits on every save**; the request's `deductsInvoiceId` on a credit note is a 400.
16. **The source modules' doors answer 409 in a coded problem:** `openapi/expenses.yaml` gains `ExpensesConflictProblem` and `openapi/projects.yaml` `ProjectsConflictProblem` (ProblemDetails + `code`, `invoiceId`, `invoiceNumber`), used by the two expense invoiced operations' and the milestone status move's 409; a revision conflict answers the same schema with no code.
17. **The Expenses holder locks with the reimbursement batch's own queries** — a plain `EntryClaims` read of the lines' `claim_id`s, then `LockClaims`, then `LockBatchEntries` with only the named entry ids — in `MarkInvoiced` **and in `ReleaseInvoiced`, before it writes** (every holder's release locks exactly as its mark does); a locked line whose claim was not locked (it moved between the read and the lock) is `source_changed` on a mark and a warning on a release.
18. **The Projects holder** locks with `LockProject` per distinct project, ascending, then a new `LockMilestonesByIDs` (`ORDER BY id FOR UPDATE`), in both directions; its timeline event is `eventMilestoneInvoiced` / `eventMilestoneInvoiceUndone` (`srv/projects/milestones_validation.go:82-85`) with `actor{IssuedBy, IssuedByDisplay}` and the payload's `invoiceNumber`. **A release always moves `invoiced → ready`**, converts a percent milestone to an amount only when `milestoneMoveRefusal` says `convert`, never refuses (a project since left without a currency still releases) and logs a warning where the manual move would have refused (the spec's D1 table amended to match).
19. **`source_not_selectable` under the issue's lock** (spec as amended) is: hours of a project now `fixed-price` or `non-billable`, and any kind of a project now `non-billable` — the wizard's step 2 rule ("fixed-price hours or non-billable work") applied again. **The re-check is from the pre-read** (amended at the Task 13 review): the billing types are those `ProjectDirectory.Projects` answered before the transaction, and no project is locked for them, so a project re-typed while an issue is in flight does not stop that issue — an accepted window, serially equivalent to the change coming just after the issue (a project's `PUT` has no guard against invoiced work), pinned by `TestWorkRace_ABillingTypeChangeAfterTheIssuesPreReadIsNotSeen`.
20. **The issuer's name is read only when the draft holds sources** (`UserDirectory.User(caller)`, before the transaction, through a new `userEntry` accessor); a user the directory does not know gives `IssuedByDisplay` `""`, and Projects records the event with its own unknown-user wording (`srv/projects/server.go:44-60`). A credit note reads it only when its original holds `invoiced` sources.
21. **The contract-call hook gains a flag:** `contractCallHook func(ctx context.Context, method string, txBound bool)`; `noteTxCommand(ctx, name)` reports `txBound = true`. The harness fails a test on a locked call that is not tx-bound and on a tx-bound call outside a lock.
22. **A new pre-lock seam `SetIssueBeforeLock(hook func(ctx context.Context, invoiceID int64))`** runs after every pre-read of the issue and before `withLockedTx`, so a test can slip a save in (`invoice_changed`) or a project's billing type change (`source_not_selectable`).
23. **`invoiced_invoice_id` is tied to the stamp in each schema**: Time `invoiced_invoice_id IS NULL OR (status = 'invoiced' AND invoiced_at IS NOT NULL)`; Expenses `… OR (invoiced_at IS NOT NULL AND invoice_reference IS NULL)`; Projects `… OR status = 'invoiced'`. A row stamped by hand keeps `invoiced_at`/`status` without an id (tests reach Time's `invoiced` that way, `R/time.md:198-201`).
24. **Each migration's schema test goes in its own file** (`srv/db/schema_{time,expenses,projects,invoices}_work_test.go`, package `db_test`, reusing `applyUpDownUp`, `refusedWith`, `checkViolationOf`), so the parallel worktrees rarely meet in `schema_test.go`; the two edits that must be made there are owned: Task 3 appends `invoiced_invoice_id bigint YES` and `invoiced_number bigint YES` to `expensesColumns["entries"]` (the pinned column list `TestExpensesBaseline_PinsTheEntryAndAttachmentColumns` reads, `:3435`), and Task 5 widens the baseline counts and the reserved-word test (`:3189`) at 40.
25. **Release ref:** `InvoiceRef{ID, Number, IssueDate}` are the original's; `IssuedAt`, `IssuedBy`, `IssuedByDisplay` are the credit note's issue's (spec as amended).
26. **The wizard's `timesheet` field, the document's `timesheet`/`timesheetRows` and the two timesheet settings join the contract in Task 12**, not before (spec reading 37).
27. **A deduction on an invoice has quantity exactly −1**, unit price > 0 and discount 0 (D7's "quantity −1"); a credit-note line of one may have any quantity in `[−1, 0)` — a lower magnitude, as a partial credit of an ordinary line lowers its quantity.
28. **`modtest.WithPoolMaxConns(n)`** (Task 13) builds the harness's pool at `n` connections on the migrated database (`testdb.Migrated`'s URL, `db.WithMaxConns`), for the integration races; every raw lock-holding transaction there is a `pgx.Connect` of its own (spec as amended).
29. **The frontend lands once, in Task 14, after 3C's server tasks.** Each of 3A, 3B and 3C is a working server state whose pages say what exists (the per-task docs rule); the user guide for the new screens lands in Task 15, directly after.
30. **`work_unavailable`** is answered before any directory read; `GET /invoices/work` reads `ProjectsForCustomer` (or `Project`) then the composed `Billable*` providers one after the other, on the pool. **`from-work`'s order** is the spec's D3 as amended at this plan's pre-flight: (1) 400 on the body, duplicates included, and 409 `too_many_sources` past 5 000 counting an append target's held rows — before any read; (2) the settings, the profile, `customerGate`; (3) the three `Billable*` reads by `IDs`; (4) `ProjectDirectory.Projects` of the found sources' projects; (5) the judgments in order — `source_not_for_customer`, `source_not_selectable`, `source_not_invoiceable` (missing, or a kind whose provider is nil), `source_changed`, `mixed_currency`, `currency_not_nok`; (6) the lines.
31. **`GET /invoices/{id}/deductible`** (`getInvoicesByIdDeductible`, `invoices:access+invoices:create`; the spec's D17 amended to name it) — for an invoice draft, the customer's issued invoices with something left to deduct, per VAT code `{invoiceId, number, issueDate, vatCodeId, category, ratePercent, left}`; 404 for an unknown id; 409 `invoice_issued` on an issued document and 409 on a credit-note draft (a credit note deducts nothing). Added in Task 11.
32. **`refreshSources` under the lock:** the held set the refresh read before the transaction must equal the set under the draft's lock, else `invoice_changed`, as at the issue.
33. **A save that drops holds by a customer change** deletes the draft's held rows **and** its lines' `sources` from the request are then refused as unheld (400) — a client changing the customer sends `[]` on every line.
34. **Row locks are proved by `NOWAIT` probes, never by `pg_locks`** (an uncontended row lock never appears there; the spec amended to match): the subject is parked — behind a raw lock on its own `pgx.Connect`, or by a seam — `pg_blocking_pids` shows what it waits on, and a `SELECT … FOR UPDATE NOWAIT` / `FOR NO KEY UPDATE NOWAIT` from another connection of its own shows whether a row is held (succeeds: not held; `55P03`: held).
35. **Each source module's locked flag is proved by a seam**, `SetInvoicedWorkAfterLock(func(ctx context.Context))` in its `export_test.go`, called by the holder right after its locks in both directions; the test asserts the module's own `InLockedTx(ctx)` there, shown able to fail by removing the holder's `context.WithValue`.
36. **`SetFromWorkBeforeInsert(func(ctx context.Context, invoiceID int64))`** runs inside `from-work`'s transaction after `LiveSourcesElsewhere` and before `InsertLineSources`, so a race test parks one wizard there while the other commits.

## File Structure

| File | Responsibility |
| --- | --- |
| `srv/contracts/{invoiced_work.go,billable.go,invoiced_work_test.go}`, `srv/module/{module.go,compose.go,workers.go,compose_test.go,workers_test.go}`, `srv/modtest/modtest.go` | the contract, the slots, the partition, the seams (Task 1) |
| `mig/00037_time_invoiced_by.sql`, `srv/time/{invoiced_work.go,billable.go,responses.go,module.go,sqlc.yaml,queries/invoiced.sql,*_test.go}`, `openapi/time.yaml`, `srv/db/schema_time_work_test.go` | Time (Task 2) |
| `mig/00038_expenses_invoiced_by.sql`, `srv/expenses/{invoiced_work.go,billable.go,invoiced.go,module.go,sqlc.yaml,queries/{invoiced.sql,entries.sql,projectexpenses.sql,reimbursements.sql},*_test.go}`, `openapi/expenses.yaml`, `srv/db/schema_expenses_work_test.go` | Expenses (Task 3) |
| `mig/00039_projects_invoiced_by.sql`, `srv/projects/{invoiced_work.go,billable.go,milestones.go,module.go,sqlc.yaml,queries/{invoiced.sql,milestones.sql},*_test.go}`, `openapi/projects.yaml`, `srv/db/schema_projects_work_test.go` | Projects (Task 4) |
| `mig/00040_invoices_work.sql`, `inv/{sqlc.yaml,queries/{work.sql,lines.sql}}`, `srv/db/schema_invoices_work_test.go` | the invoices schema and its queries (Task 5) |
| `inv/{sources.go,drafts.go,responses.go,contractscalls.go,sources_test.go,harness_test.go}` | the hold through the draft (Task 6) |
| `inv/{server.go,writeback.go,issue.go,contractscalls.go,export_test.go,harness_test.go,writeback_test.go}` + every `withLockedTx` caller | the write-back (Task 7) |
| `inv/{work.go,grouping.go,meta.go,settings.go,work_test.go,grouping_internal_test.go}` | the view and the wizard (Task 8) |
| `inv/{sources.go,credits.go,list.go,csvexport.go,pdf.go,ehfdoc.go,ehf/{doc.go,render.go},ehf/testdata/golden/*}`, `inv/queries/{invoices.sql,credits.sql,export.sql}` | the project dimension (Task 9) |
| `inv/{credits.go,issue.go,writeback.go,responses.go,work.go,releases_test.go}` | release on credit (Task 10) |
| `inv/{deductions.go,drafts.go,credits.go,issue.go,pdf.go,ehfdoc.go,ehf/{doc.go,render.go,precheck.go},ehf/testdata/golden/*,queries/deductions.sql,deductions_test.go}` | a-konto and the settlement (Task 11) |
| `inv/{timesheet.go,drafts.go,work.go,sources.go,pdf.go,settings.go,customer_slots.go,timesheet_test.go}` | the timesheet (Task 12) |
| `inv/{customer_slots.go,customer_slots_test.go}`, `srv/modtest/modtest.go`, `srv/integration/{harness_test.go,work_test.go,work_races_test.go}` | the slots and the integration tests (Task 13) |
| `apps/invoices/frontend/src/**`, `apps/host/frontend/src/routes/{projects,customers}/**`, `apps/{time,expenses,projects}/frontend/src/**` | the apps (Task 14) |
| `docs/src/content/docs/{en,nb}/…`, `ROADMAP.md` | the documentation (Task 15) |

---

### Task 1: The contract, the slots, and rule 10's source-module half (D1, D3's contracts, D19; phase 3A)

**Files:** create `srv/contracts/invoiced_work.go`, `srv/contracts/billable.go`, `srv/contracts/invoiced_work_test.go`; modify `srv/module/module.go` (`Deps` after `CustomerPersonalData` at `:105`; `Module` after `CustomerPersonalData` at `:234`), `srv/module/compose.go` (three `soleProvider` slots after expenses at `:226-231`; the partition over `given` at `:247-264`; `withCustomerPersonalData` at `:552`; a new `withInvoicedWork`), `srv/module/workers.go` (`:25`), `srv/module/compose_test.go`, `srv/module/workers_test.go`, `srv/modtest/modtest.go` (`setup` `:117-135`, options after `:263`, `Deps` `:414-435`), `MB`.

**Interfaces** (`contracts`, DTOs and `pgx.Tx` only — rule 3):

```go
type WorkSourceKind string
const (
    WorkSourceMilestone WorkSourceKind = "projects.milestone"
    WorkSourceExpense   WorkSourceKind = "expenses.entry"
    WorkSourceHours     WorkSourceKind = "time.entry"
)
// InvoicedWorkOrder is the cross-module lock order the issue calls holders in.
var InvoicedWorkOrder = []WorkSourceKind{WorkSourceMilestone, WorkSourceExpense, WorkSourceHours}
type InvoicedWorkHolder interface {
    Kinds() []WorkSourceKind
    MarkInvoiced(ctx context.Context, tx pgx.Tx, ref InvoiceRef, sources []WorkSource) error
    ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref InvoiceRef, sources []WorkSource) error
}
type InvoiceRef struct {
    ID, Number      int64
    IssueDate       time.Time
    IssuedAt        time.Time
    IssuedBy        uuid.UUID
    IssuedByDisplay string
}
type WorkSource struct {
    Kind        WorkSourceKind
    ID          int64
    Revision    int32  // judged by Time and Projects; display-only for Expenses
    ProjectID   int32
    Currency    string
    Amount      string // exact decimal text
    ExpenseKind string // expenses only
}
const (
    SourceNotInvoiceable  = "source_not_invoiceable"
    SourceChanged         = "source_changed"
    SourceAlreadyInvoiced = "source_already_invoiced"
)
type WorkSourceRefusal struct { Source WorkSource; Code, Detail string }
func (r *WorkSourceRefusal) Error() string

// billable.go
const MaxBillableRows = 5000
type BillableRequest struct { ProjectIDs []int32; IDs []int64; Until time.Time }
func (r BillableRequest) Validate() error // exactly one of ProjectIDs (≤ MaxActualsRequests) and IDs (≤ MaxBillableRows); no duplicates
type BillableHour struct {
    ID int64; Revision, ProjectID int32; BillingLineID *int32; UserID uuid.UUID; Date time.Time
    HoursHundredths int64; BillRate, Currency string; BillMultiplierPercent *string
    WorkTypeID *int32; WorkTypeName, TaskTitle, Amount string
}
type BillableExpense struct {
    ID int64; Revision, ProjectID int32; ClaimID *int64; Kind string; Date time.Time
    Description, Supplier, SupplierInvoiceNumber, NetAmount string; MarkupPercent, DistanceKm, BillRatePerKm *string
    BillAmount, Currency string; SupplierInvoiceRebilled bool
}
type BillableMilestone struct {
    ID int64; Revision, ProjectID int32; Name, Description string; PlannedDate *time.Time
    ReadyAt time.Time; Amount, Currency string
}
type BillableHoursPage struct { Hours []BillableHour; More bool }
type BillableExpensesPage struct { Expenses []BillableExpense; More bool }
type BillableMilestonesPage struct { Milestones []BillableMilestone; More bool }
type BillableHours interface { BillableHours(ctx context.Context, req BillableRequest) (BillableHoursPage, error) }
type BillableExpenses interface { BillableExpenses(ctx context.Context, req BillableRequest) (BillableExpensesPage, error) }
type BillableMilestones interface { BillableMilestones(ctx context.Context, req BillableRequest) (BillableMilestonesPage, error) }
```

The doc comments carry the holder's rules (rule 8's, plus: judge already-invoiced → not-invoiceable → changed; compare amounts exactly; stamp at `IssuedAt`; ignore the period lock; release tolerates a source without `ref`'s stamp and logs; never a directory or a clock; runs disabled) and the reads' rules (on the pool, never under a lock, never calling a directory back, `More` past `MaxBillableRows`, `IDs` answering only what is still billable, never Time's `note`). `module.Deps` gains `BillableHours`, `BillableExpenses`, `BillableMilestones` and `InvoicedWork []contracts.InvoicedWorkHolder`; `module.Module` gains `BillableHours func(Deps) contracts.BillableHours`, `BillableExpenses …`, `BillableMilestones …`, `InvoicedWork func(Deps) contracts.InvoicedWorkHolder`. `compose.go`: the three `soleProvider`s ("billable hours", "billable expenses", "billable milestones") after `Expenses`; `func invoicedWorkLast(mods []Module) []Module` (a stable partition: every module whose `InvoicedWork` is nil, in order, then the rest, in order) feeding the `CustomerReferences` loop and `withCustomerPersonalData`; `func withInvoicedWork(deps Deps, mods []Module) (Deps, error)` collecting from every module given, appended to a clone of the preset slice, refusing a kind two holders claim (`module: two modules both stamp "time.entry": a, b`). `Workers` calls both helpers over every module given before enablement drops any. `modtest`: `WithInvoicedWork(holders ...contracts.InvoicedWorkHolder)`, `WithBillableHours(p)`, `WithBillableExpenses(p)`, `WithBillableMilestones(p)` — the `WithActuals` shape (a value set here survives Compose).

- [ ] **Step 1: Tests first.** `TestBillableRequest_Validate` (neither, both, too many, duplicates), `TestWorkSourceRefusal_IsAnError` (`errors.As` through a wrap), `TestCompose_InvoicedWorkIsCollectedFromEveryModuleGiven` (a disabled module's holder present), `TestCompose_RefusesAKindClaimedTwice`, `TestCompose_RefusesTwoProvidersOfEachBillableRead` (three cases, naming both), `TestCompose_BillableReadsComeOnlyFromEnabledModules`, `TestCompose_InvoicedWorkProvidersReferencesComeLast` (fake modules given as `[holderA, plain1, holderB, plain2]`; a merge-shaped walk over `Deps.CustomerReferenceHolders` records `[plain1, plain2, holderA, holderB]` — stable on both sides; the merge takes invoices documents before project rows), `TestCompose_PersonalDataIsPartitionedTheSameWay` (the same four, the same order), `TestWorkers_CarriesInvoicedWorkAndThePartition` (worker-mode `Deps` seen by a module's `Workers` func holds the slot, and its `CustomerPersonalData` in `[plain1, plain2, holderA, holderB]` order), `TestModtest_TheInvoicedWorkAndBillableSeamsSurviveCompose`. Red; implement; green; shown able to fail by dropping the partition (the ordering tests go red) and by collecting from `mods` instead of `given`.
- [ ] **Step 2: Docs — `MB`** (English only): rule 1's "the future Invoices module" (`MB:24`) → Invoices; the module list (`MB:10-12`) gains Time, Expenses and Invoices; rule 3 (`MB:44-46`) names `InvoicedWorkHolder` beside the two holders; rule 4's schemas (`MB:48-49`) gain `invoices`; rule 5 (`MB:56-73`) — nine single-provider slots (the three `Billable*`, resolved after `Projects`), four many-provider (`InvoicedWork`, from every module given), the partition and why; rule 8's last sentence (`MB:92-93`) "… and rule 10 for the third"; **rule 10 inserted after rule 9 with its source-module half** (the contract, what a holder judges, stamps and releases, the holder's rules, the cross-module lock order, the merge partition, the restated lock rule); rule 5's enforcement bullet (`MB:136-141`) names the three `Billable*` and a kind claimed twice; a **Rule 10** enforcement bullet after rule 9's (`MB:150-155`) with the holder half (`pgx.Tx` by shape, `Pool` nil in each holder's test, the module's own locked flag, Compose and Workers collecting the slot and the partition); the default `MODULES` (`MB:167`) gains `invoices`; **the module paragraphs' new sentences, written here once** so Tasks 2 and 3 only verify them — Time's (`MB:245-264`): it provides `BillableHours` beside `ProjectActuals` and fills `InvoicedWork`; Expenses' (`MB:266-283`): it provides `BillableExpenses` beside `ProjectExpenses` and fills `InvoicedWork`. `mise run docs:check`.
- [ ] **Step 3:** lint, `go test ./internal/contracts/... ./internal/module/... ./internal/modtest/...`; **commit** `feat(contracts): invoiced work — the holder contract, the billable reads and their slots, collected with the invoiced-work providers' customer slots last (rule 10)`; then `check-coverage.ts --base HEAD~1`.

### Task 2: Time — the first writer of `invoiced`, its release, and `BillableHours` (D1, D3; phase 3A)

**Files:** create `mig/00037_time_invoiced_by.sql`, `srv/time/{invoiced_work.go,billable.go,invoiced_work_test.go,billable_test.go,queries/invoiced.sql}`, `srv/db/schema_time_work_test.go`; modify `srv/time/sqlc.yaml`, `srv/time/module.go` (`:63-70`, the package comment `:24-26`), `srv/time/responses.go` (the entry's `invoicedBy`), `srv/time/export_test.go` (`SetInvoicedWorkAfterLock`, reading 35), `openapi/time.yaml`, `apps/time/frontend/src/test/fixtures.ts`, generated files; docs `R/time.md`, `en|nb/user/time.md` (`MB`'s Time paragraph is Task 1's; verify it).

**The migration:**

```sql
-- +goose Up
-- The invoice that invoiced an entry (invoices work design D1): its id and
-- number, opaque — the invoices schema is another module's (rule 4). Written
-- only by the invoices issue through contracts.InvoicedWorkHolder, cleared only
-- by the credit note that returns the entry's line. Both or neither, and only
-- on an invoiced entry; an entry stamped by hand carries invoiced_at without them.
ALTER TABLE time.entries
    ADD COLUMN invoiced_invoice_id bigint,
    ADD COLUMN invoiced_number     bigint,
    ADD CONSTRAINT ck_entries_invoiced_by
        CHECK ((invoiced_invoice_id IS NULL) = (invoiced_number IS NULL)),
    ADD CONSTRAINT ck_entries_invoiced_by_status
        CHECK (invoiced_invoice_id IS NULL OR (status = 'invoiced' AND invoiced_at IS NOT NULL));

-- +goose Down
ALTER TABLE time.entries
    DROP CONSTRAINT ck_entries_invoiced_by_status,
    DROP CONSTRAINT ck_entries_invoiced_by,
    DROP COLUMN invoiced_number,
    DROP COLUMN invoiced_invoice_id;
```

**Queries** (`queries/invoiced.sql`): `BillableHoursForProjects` (`project_id = ANY(@project_ids)`, `status = 'approved' AND billable AND bill_rate IS NOT NULL`, `sqlc.narg(until)` on `entry_date`, `ORDER BY project_id, entry_date, id LIMIT @row_limit` with `MaxBillableRows + 1`; the columns of `BillableHour` and `(hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01))::text AS amount` — never `note`); `BillableHoursByIDs` (the same predicate over `id = ANY(@ids)`); `StampEntriesInvoiced` (`status = 'invoiced', invoiced_at = @issued_at, invoiced_invoice_id, invoiced_number, revision = revision + 1, updated_at = @issued_at WHERE id = ANY(@ids) AND status = 'approved' RETURNING id` — the guard is the last line, not the rule); `ReleaseEntriesInvoiced` (`status = 'approved', invoiced_at = NULL, invoiced_invoice_id = NULL, invoiced_number = NULL, revision = revision + 1, updated_at = @released_at WHERE id = ANY(@ids) AND invoiced_invoice_id = @invoice_id RETURNING id` — the approval stamps stay). The holder locks with `LockEntries` (`queries/entries.sql:131-139`).

**Interfaces:** `newInvoicedWorkHolder(d module.Deps) contracts.InvoicedWorkHolder` (keeps `d.Logger` only); `Kinds()` → `{WorkSourceHours}`; `MarkInvoiced` marks `ctx` with `lockedTxKey{}` (`srv/time/server.go:86-96`), locks the ids, then judges each source in id order: `status = 'invoiced'` → `source_already_invoiced`; missing, `status <> 'approved'`, `!billable` or `bill_rate IS NULL` → `source_not_invoiceable`; revision, `project_id`, `bill_currency` or `entryAmount(row)` (`big.Rat`) differing → `source_changed`; the first refusal answers, nothing is written; then `StampEntriesInvoiced` (a count short of the sources is an error). `ReleaseInvoiced` marks `ctx` the same way, locks with `LockEntries` **before it writes**, releases what carries `ref.ID` and logs a warning naming each source that does not. Both call `invoicedWorkAfterLock(ctx)` (the seam) right after the locks. A source of another kind is an error. `newBillableHours(d) contracts.BillableHours` over `d.Pool`. `Module()` gains `BillableHours: newBillableHours, InvoicedWork: newInvoicedWorkHolder`. The period lock is not consulted in either direction.

**Contract:** `TimeEntryResponse.invoicedBy` (optional `TimeInvoicedBy {invoiceId: int64, number: int64}`), present on an entry the invoices issue stamped. No operation added; the `invoiced → approved` move has no endpoint.

- [ ] **Step 1: Schema test first** — `TestTimeInvoicedBy_AppliesAndIsIdempotent` (`applyUpDownUp(t, url, 37)`; both CHECKs refuse; an entry stamped by hand without an id still allowed; Down removes both columns). Red; the migration; `sqlc.yaml`; the queries; generate twice.
- [ ] **Step 2: The holder's tests first** (`timetracking_test`, the holder built from `module.Deps{Logger: …}` with `Pool` nil, real rows through a real transaction on `testdb`'s pool that the test rolls back): `TestInvoicedWork_StampsApprovedEntriesAtIssuedAt` (status, the three columns, revision + 1, `updated_at`), `TestInvoicedWork_JudgesAlreadyInvoicedFirst` (an entry stamped by hand and also unbillable → `source_already_invoiced`), `TestInvoicedWork_RefusesWhatIsNotInvoiceable` (draft, submitted, rejected, not billable, no rate, missing), `TestInvoicedWork_RefusesWhatChanged` (revision, project, currency, an amount differing in the eighth decimal), `TestInvoicedWork_AnEqualAmountInAnotherScaleIsNoChange` (`"125.4125"` against `125.41250000`), `TestInvoicedWork_IgnoresThePeriodLock`, `TestInvoicedWork_WritesNothingOnARefusal`, `TestInvoicedWork_ReleaseReturnsToApproved` (approval stamps kept), `TestInvoicedWork_ReleaseToleratesAMissingStamp` (nothing written, one warning), `TestInvoicedWork_BuiltFromADisabledModulesDeps`, `TestInvoicedWork_MarksItsOwnLockedFlag` (`SetInvoicedWorkAfterLock` asserts `timetracking.InLockedTx(ctx)` on a mark and a release; shown able to fail by removing the holder's `context.WithValue`), `TestInvoicedWork_ReleaseLocksBeforeItWrites` (a raw `FOR UPDATE` on the entry from its own connection parks the release — `pg_blocking_pids`). `TestBillableHours_TheSet` (approved, billable, priced; never the note), `…_More`, `…_ByIDs`, `…_Until`, `…_TheExactAmount`. `TestApproval_AnInvoicedEntryIsStillRefused` (`approval.go:63-64` unchanged), `TestEntries_InvoicedByOnTheWire`. Red; implement; green; shown able to fail by swapping the judge's order and by stamping with the clock.
- [ ] **Step 3: Docs** — `R/time.md`: the state machine (`:175-201`) with `invoiced → approved` ("only a credit note takes it back", `:189`, `:198`), the two columns, `invoicedBy`; "What invoicing will read" (`:369-393`) becomes what Time provides — `BillableHours`, the holder, the stamp and the release, the period lock not applying; `en/user/time.md` + `nb/user/time.md`: an entry invoiced through Invoices says so, and only a credit note returns it to approved; `MB`'s Time paragraph verified against the code (Task 1 wrote it). `mise run docs:check`.
- [ ] **Step 4:** fixtures; typecheck; lint; the suites; **commit** `feat(time): entries invoiced by an invoice — Time's first writer of the state, its release back to approved, and the billable hours read`; the per-commit check.

### Task 3: Expenses — one ready predicate, `BillableExpenses`, the holder and the door (D1, D3, D15; phase 3A)

**Files:** create `mig/00038_expenses_invoiced_by.sql`, `srv/expenses/{invoiced_work.go,billable.go,invoiced_work_test.go,billable_test.go,ready_test.go,queries/invoiced.sql}`, `srv/db/schema_expenses_work_test.go`; modify `srv/expenses/sqlc.yaml`, `srv/expenses/module.go` (`:106-111`), `srv/expenses/invoiced.go` (`:36-61`, `:130-205`), `srv/expenses/queries/{entries.sql,projectexpenses.sql,reimbursements.sql}`, the entry response (`invoicedBy`), `srv/expenses/export_test.go` (`SetInvoicedWorkAfterLock`), `srv/db/schema_test.go` (`expensesColumns["entries"]` gains `invoiced_invoice_id bigint YES` and `invoiced_number bigint YES`, reading 24), `openapi/expenses.yaml`, `apps/expenses/frontend/src/test/fixtures.ts`, generated files; docs `R/expenses.md`, `en|nb/user/expenses.md` (`MB`'s Expenses paragraph is Task 1's; verify it).

**The migration:**

```sql
-- +goose Up
ALTER TABLE expenses.entries
    ADD COLUMN invoiced_invoice_id bigint,
    ADD COLUMN invoiced_number     bigint,
    ADD CONSTRAINT ck_entries_invoiced_by
        CHECK ((invoiced_invoice_id IS NULL) = (invoiced_number IS NULL)),
    ADD CONSTRAINT ck_entries_invoiced_by_stamp
        CHECK (invoiced_invoice_id IS NULL OR (invoiced_at IS NOT NULL AND invoice_reference IS NULL));

-- ready_to_invoice is the one "ready to invoice" rule (invoices work design D3):
-- the unit approved, billable, not a per diem day, priced. It deliberately
-- leaves out "not invoiced yet" — every caller states invoiced_at IS NULL
-- beside it — so the invoices holder can judge "already invoiced" first. Go's
-- invoicedRefusal (invoiced.go) is its mirror, held to it by a test over every
-- combination. IMMUTABLE and PARALLEL SAFE for owes_employee's reason.
-- +goose StatementBegin
CREATE FUNCTION expenses.ready_to_invoice(unit_status text, billable boolean, kind text, bill_amount numeric)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT $1 = 'approved' AND $2 AND $3 <> 'per_diem' AND $4 IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION expenses.ready_to_invoice(text, boolean, text, numeric);
ALTER TABLE expenses.entries
    DROP CONSTRAINT ck_entries_invoiced_by_stamp,
    DROP CONSTRAINT ck_entries_invoiced_by,
    DROP COLUMN invoiced_number,
    DROP COLUMN invoiced_invoice_id;
```

**The predicate's places:** `CountEntries` and `ListEntries` (`queries/entries.sql:179-184`, `:206-211`), `ProjectExpenseGroups`' `ready_count`/`ready_amount` (`queries/projectexpenses.sql:67-76`), `MarkEntryInvoiced` (`queries/reimbursements.sql:273-284`) — each `expenses.ready_to_invoice(COALESCE(c.status, e.status), e.billable, e.kind, e.bill_amount) AND e.invoiced_at IS NULL`; the comment at `entries.sql:151` names the function.

**Queries** (`queries/invoiced.sql`): `BillableExpensesForProjects` / `BillableExpensesByIDs` (the predicate plus `invoiced_at IS NULL` and `project_id = ANY(…)` / `id = ANY(…)`, the claim joined for the unit status, `NetAmount` as `gross_amount - COALESCE(vat_amount, 0)`, `supplier_invoice_rebilled` as `EXISTS` another `kind = 'supplier_invoice'` row with `lower(btrim(supplier))` and `supplier_invoice_number` equal and `invoiced_at IS NOT NULL`, `ORDER BY project_id, entry_date, id LIMIT @row_limit`); `EntryClaims` (`SELECT id, claim_id … WHERE id = ANY(@ids)`, unlocked); `StampEntriesInvoicedByInvoice` (`invoiced_at = @issued_at, invoiced_by_user_id = @issued_by, invoiced_invoice_id, invoiced_number, invoice_reference = NULL, revision + 1, updated_at = @issued_at WHERE id = ANY(@ids) AND invoiced_at IS NULL RETURNING id`); `ReleaseEntriesInvoicedByInvoice` (the five cleared, `revision + 1, updated_at = @released_at WHERE id = ANY(@ids) AND invoiced_invoice_id = @invoice_id RETURNING id`).

**Interfaces:** `newInvoicedWorkHolder(d)`, `Kinds()` → `{WorkSourceExpense}`; `MarkInvoiced` marks `ctx` locked (`srv/expenses/server.go:87-113`), reads the claims (`EntryClaims`), `LockClaims`, `LockBatchEntries{EntryIds: ids}` (reading 17), then judges: `invoiced_at` set → `source_already_invoiced` (detail names the number or the hand reference); `!ready_to_invoice` → `source_not_invoiceable` with `invoicedRefusal`'s message; `bill_amount` (`big.Rat`), `project_id`, `currency`, `kind` ≠ `ExpenseKind` → `source_changed` — **never the revision** (a reimbursement bumps it, `reimbursements.sql:185`, `:209`). `ReleaseInvoiced` marks `ctx` and locks exactly as the mark does (`EntryClaims` → `LockClaims` → `LockBatchEntries{EntryIds}`) before it writes, and tolerates; both call the `SetInvoicedWorkAfterLock` seam after the locks. `newBillableExpenses(d)`. **The doors:** `markInvoiced` answers **409 `invoiced_by_invoices`** (with `invoiceId`, `invoiceNumber`) for a row carrying `invoiced_invoice_id`, before `invoicedRefusal` and again under the lock; a hand-stamped row is unchanged.

**Contract:** `ExpensesEntryResponse.invoicedBy` (optional `{invoiceId, number}`); `ExpensesConflictProblem` (ProblemDetails + `code`, `invoiceId`, `invoiceNumber`) on the 409 of `postExpensesEntriesByIdInvoiced` and `…Undo` (reading 16).

- [ ] **Step 1: Schema test first** — `TestExpensesInvoicedBy_AppliesAndIsIdempotent` (37 → 38 → 37 → 38; the CHECKs; the function's truth table; Down). Red; migration; queries; generate twice.
- [ ] **Step 2: The one predicate first** — `TestReadyToInvoice_TheFunctionAndInvoicedRefusalAgree` (every status × billable × kind × priced, against `invoicedRefusal` with `InvoicedAt` nil), `TestReadyToInvoice_EveryFormerPlaceStillCounts` (the list filter, its count, the project groups' ready count and amount, the manual stamp — each unchanged over a fixture). Rewrite the four places; green; shown able to fail by changing the function alone.
- [ ] **Step 3: The holder and the read** (built from `Deps` with `Pool` nil; a rolled-back real transaction): `TestInvoicedWork_StampsAtIssuedAtWithTheIssuer`, `…_JudgesAlreadyInvoicedFirst` (a hand-stamped per diem → `source_already_invoiced`), `…_RefusesWhatIsNotInvoiceable` (each `invoicedRefusal` reason), `…_UnchangedByAReimbursementsRevisionBump`, `…_ChangedByEachBillingFact` (bill amount, project, currency, kind), `…_LocksClaimThenLines` (a raw `FOR UPDATE` on the claim from its own connection parks the mark — `pg_blocking_pids` — and a `FOR UPDATE NOWAIT` probe of the line meanwhile succeeds), `…_ReleaseLocksAsTheMarkDoes`, `…_ALineMovedBetweenReadAndLockIsChanged`, `…_ReleaseClearsTheFive`, `…_ReleaseTolerates`, `…_IgnoresThePeriodLock`, `…_BuiltFromADisabledModulesDeps`, `…_MarksItsOwnLockedFlag` (the seam asserts `expenses.InLockedTx(ctx)` on both directions; shown able to fail by removing the `WithValue`); `TestBillableExpenses_TheSet`, `…_More`, `…_ByIDs`, `…_SupplierInvoiceRebilled`; **the doors**: `TestInvoiced_AnInvoicesStampIsA409OnMarkAndUndo`, `TestInvoiced_AHandStampIsUnchanged`. Red; implement; green.
- [ ] **Step 4: Docs** — `R/expenses.md`: the invoiced track (`:811-821`) — the two columns, the module stamp, the 409, the function and its places, `BillableExpenses`, `SupplierInvoiceRebilled`; `en|nb/user/expenses.md`: "Invoiced by invoice n", and that such a mark can only be undone by a credit note; `MB`'s Expenses paragraph verified (Task 1 wrote it). `docs:check`.
- [ ] **Step 5:** fixtures; typecheck; **commit** `feat(expenses): one ready-to-invoice predicate, the billable expenses read, and lines invoiced by an invoice — the manual door refusing a module stamp`; the per-commit check.

### Task 4: Projects — `BillableMilestones`, the holder and the door (D1, D3; phase 3A)

**Files:** create `mig/00039_projects_invoiced_by.sql`, `srv/projects/{invoiced_work.go,billable.go,invoiced_work_test.go,billable_test.go,queries/invoiced.sql}`, `srv/db/schema_projects_work_test.go`; modify `srv/projects/sqlc.yaml`, `srv/projects/module.go` (`:51-58`), `srv/projects/milestones.go` (`:608-732`, the move refusal), `srv/projects/queries/milestones.sql` (`LockMilestonesByIDs`), `srv/projects/milestones_validation.go` (`milestoneEffectiveAmountRat` beside `:475`), the milestone response (`invoicedBy`), `srv/projects/export_test.go` (`SetInvoicedWorkAfterLock`), `openapi/projects.yaml`, page tests that spell a milestone, generated files; docs `R/projects.md`, `R/customers.md` (`:1434`: the merge now calls Projects' holder after every module that provides no invoiced work — Projects becomes such a provider in this task), `en|nb/user/projects.md`.

**The migration:**

```sql
-- +goose Up
ALTER TABLE projects.billing_milestones
    ADD COLUMN invoiced_invoice_id bigint,
    ADD COLUMN invoiced_number     bigint,
    ADD CONSTRAINT ck_billing_milestones_invoiced_by
        CHECK ((invoiced_invoice_id IS NULL) = (invoiced_number IS NULL)),
    ADD CONSTRAINT ck_billing_milestones_invoiced_by_status
        CHECK (invoiced_invoice_id IS NULL OR status = 'invoiced');

-- +goose Down
ALTER TABLE projects.billing_milestones
    DROP CONSTRAINT ck_billing_milestones_invoiced_by_status,
    DROP CONSTRAINT ck_billing_milestones_invoiced_by,
    DROP COLUMN invoiced_number,
    DROP COLUMN invoiced_invoice_id;
```

**Queries:** `LockMilestonesByIDs` (`WHERE id = ANY(@ids::integer[]) ORDER BY id FOR UPDATE`); `BillableMilestonesForProjects` / `…ByIDs` (`status = 'ready'`, with the project's `currency` and `fixed_price_amount` read from `projects.projects` in the same schema; `LIMIT @row_limit`); `StampMilestonesInvoicedByInvoice` (one row: `status = 'invoiced', invoiced_at = @issued_at, invoiced_by_user_id = @issued_by, invoice_date = @issue_date, invoiced_amount = @frozen, invoice_reference = NULL, invoiced_invoice_id, invoiced_number, ever_moved = true, revision + 1, updated_at = @issued_at WHERE id = @id AND status = 'ready'`); `ReleaseMilestoneInvoicedByInvoice` (`invoiced → ready`, the five stamps and the two new cleared, `amount`/`amount_currency`/`percent` as the conversion decides, `revision + 1, updated_at = @released_at … WHERE id = @id AND invoiced_invoice_id = @invoice_id`).

**Interfaces:** `newInvoicedWorkHolder(d)` (logger only), `Kinds()` → `{WorkSourceMilestone}`; `MarkInvoiced` marks `ctx` locked (`srv/projects/server.go:133-143`'s key), `LockProject` (`lockProject`) for each distinct `ProjectID` ascending, then `LockMilestonesByIDs`; judges: a milestone missing → `source_not_invoiceable`; `invoiced` → `source_already_invoiced`; not `ready` → `source_not_invoiceable`; revision, currency (the milestone's `amount_currency`, else the project's) or the effective amount by value (`milestoneEffectiveAmountRat(m, project) (*big.Rat, error)` — `milestoneEffectiveAmount`'s rule, `milestones_validation.go:475-505`, as a `big.Rat` rounded half up to cents like `percentOfPrice`, `:666-678`; a percent with no fixed price → `source_not_invoiceable`) → `source_changed`; the same function gives `BillableMilestone.Amount` (`FloatString(2)`) and the frozen `invoiced_amount` — a reorder moves no revision (`queries/milestones.sql:88-97`). Stamps one statement per milestone and `recordMilestoneEvent(ctx, txq, ref.IssuedAt, eventMilestoneInvoiced, moved, actor{ref.IssuedBy, ref.IssuedByDisplay})` with `invoiceNumber` (`timeline.go:293-306`) — no `usersUser` call. `ReleaseInvoiced` marks `ctx` and locks the same way before it writes, **always** moves `invoiced → ready`, converts only when `milestoneMoveRefusal` (`milestones_validation.go:180-201`) answers `convert` (a percent milestone whose fixed price is gone becomes an amount milestone at `invoiced_amount`), never refuses — where the manual move would have refused (a project since left without a currency, a currency changed) it releases anyway and logs a warning — records `eventMilestoneInvoiceUndone` (with `convertedToAmount` when it converted), and tolerates a source without the stamp. Both directions call the `SetInvoicedWorkAfterLock` seam after the locks. `newBillableMilestones(d)`. **The door:** the status move `invoiced → ready` on a milestone carrying `invoiced_invoice_id` answers **409 `invoiced_by_invoices`** (judged under the lock, after the revision); `ready → invoiced` by hand on a held milestone is not refused. `ProjectEntry.BillingType` is already in the directory (`srv/projects/directory.go:66`, `:85`, `:151`, `:180`) — nothing to add for D14.

**Contract:** `BillingMilestoneResponse.invoicedBy` (optional `{invoiceId, number}`); `ProjectsConflictProblem` on `postProjectsMilestonesByMilestoneIdStatus`'s 409.

- [ ] **Step 1: Schema test first** — `TestProjectsInvoicedBy_AppliesAndIsIdempotent`. Red; migration; queries; generate twice.
- [ ] **Step 2: Tests first** (`Pool` nil; rolled-back real tx): `TestInvoicedWork_StampsAReadyMilestoneAndFreezesItsAmount` (every column, `invoice_date` the issue date, the timeline event by `IssuedByDisplay` at `IssuedAt`, no `Users.*` call — the module's hook silent under its own flag), `…_JudgesAlreadyInvoicedFirst`, `…_RefusesWhatIsNotReady`, `…_APercentMilestoneAfterAFixedPriceEditIsChanged`, `…_AReorderIsNoChange`, `…_LocksProjectsThenMilestones` (raw-lock P `FOR NO KEY UPDATE` on a `pgx.Connect` of its own, start the holder, wait until `pg_blocking_pids` shows it blocked on P, then a `FOR UPDATE NOWAIT` probe of the milestone from a third connection succeeds — nothing below the project is locked yet), `…_ReleaseLocksAsTheMarkDoes`, `…_ReleaseConvertsWhenTheFixedPriceIsGone`, `…_ReleaseOnAProjectWithoutCurrencyStillReleases` (a warning logged), `…_ReleaseTolerates`, `…_BuiltFromADisabledModulesDeps`, `…_MarksItsOwnLockedFlag` (the seam asserts `projects.InLockedTx(ctx)`); `TestMilestoneEffectiveAmountRat_HalfUpToCents` (33.33 % of 3 750.30 → `1249.97`, and a half-cent case rounding up, the same figure `percentOfPrice` answers); `TestBillableMilestones_TheSetAndTheEffectiveAmount`, `…_More`, `…_ByIDs`; `TestMilestoneStatus_AnInvoicesStampIsA409`, `…_AHandStampUndoesAsBefore`. Red; implement; green; shown able to fail by locking milestones before projects (the `NOWAIT` probe test) and by recording the event with the clock.
- [ ] **Step 3: Docs** — `R/projects.md` milestones (`:359-503`): the module stamp, the two columns, `invoicedBy`, the 409, the release (always back to ready, the conversion, never refused), `BillableMilestones`; `R/customers.md` (`:1434`): the merge's holder order and why; `en|nb/user/projects.md`: "Invoiced by invoice n" on the invoice plan, and the undo refused for such a milestone. `docs:check`.
- [ ] **Step 4:** typecheck; **commit** `feat(projects): billing milestones invoiced by an invoice — the billable milestones read, the stamp with its timeline event, and the undo refusing a module stamp`; the per-commit check.

### Task 5: The invoices schema and its queries (D2, D5, D6, D7, D8, D9; phases 3B and 3C)

**Files:** create `mig/00040_invoices_work.sql`, `inv/queries/work.sql`, `srv/db/schema_invoices_work_test.go`; modify `inv/sqlc.yaml`, `inv/queries/lines.sql` (`InsertLine` becomes `:one … RETURNING id`, `writeLines`'s caller adjusted with no behaviour change), generated files. No wire change.

**The migration** (each plpgsql block in goose markers; a Down written out as `00036`'s is):

```sql
-- +goose Up
-- A line's id together with its document's, so a child table can name both
-- and never disagree with its line (invoices work design D2, D8).
ALTER TABLE invoices.lines
    ADD CONSTRAINT uq_lines_id_invoice UNIQUE (id, invoice_id),
    ADD COLUMN deducts_invoice_id bigint,  -- no FK: its KEY SHARE would cycle with the merge's newest-first lock (spec, amended reading 4)
    DROP CONSTRAINT ck_lines_quantity,
    ADD CONSTRAINT ck_lines_quantity
        CHECK (quantity > 0 OR (quantity < 0 AND deducts_invoice_id IS NOT NULL)),
    ADD CONSTRAINT ck_lines_deduction_no_discount
        CHECK (deducts_invoice_id IS NULL OR discount_percent = 0);
CREATE INDEX ix_lines_deducts ON invoices.lines (deducts_invoice_id) WHERE deducts_invoice_id IS NOT NULL;

-- The project all of a document's work belongs to (D9): derived by every
-- save, NULL when it spans two or holds none, frozen at issue with the rest.
ALTER TABLE invoices.invoices
    ADD COLUMN project_id        integer,
    ADD COLUMN project_reference varchar(30),
    ADD COLUMN timesheet         boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT ck_invoices_project CHECK ((project_id IS NULL) = (project_reference IS NULL));
CREATE INDEX ix_invoices_project ON invoices.invoices (project_id) WHERE project_id IS NOT NULL;

ALTER TABLE invoices.settings
    ADD COLUMN work_vat_code_hours      integer     NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN work_vat_code_expenses   integer     NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN work_vat_code_milestones integer     NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN timesheet_default        boolean     NOT NULL DEFAULT false,
    ADD COLUMN timesheet_person_label   varchar(10) NOT NULL DEFAULT 'initials',
    ADD CONSTRAINT ck_settings_timesheet_person_label
        CHECK (timesheet_person_label IN ('initials', 'number', 'name'));

-- The work a line bills (D2): held from the draft, invoiced by the issue,
-- released by the credit note that returns the line. Opaque ids — the sources
-- are other modules' rows (rule 4). amount is the source's exact amount:
-- Time's carries up to eight decimals (plan reading 3).
CREATE TABLE invoices.line_sources (
    id              bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    line_id         bigint        NOT NULL,
    invoice_id      bigint        NOT NULL,
    source_kind     varchar(30)   NOT NULL,
    source_id       bigint        NOT NULL,
    source_revision integer       NOT NULL,
    source_subkind  varchar(20),
    project_id      integer       NOT NULL,
    quantity        numeric(12,3) NOT NULL,
    amount          numeric(22,8) NOT NULL,
    currency        char(3)       NOT NULL,
    state           varchar(10)   NOT NULL DEFAULT 'held',
    source_date     date          NOT NULL,
    CONSTRAINT fk_line_sources_line FOREIGN KEY (line_id, invoice_id)
        REFERENCES invoices.lines (id, invoice_id) ON DELETE CASCADE,
    CONSTRAINT ck_line_sources_kind
        CHECK (source_kind IN ('time.entry', 'expenses.entry', 'projects.milestone')),
    CONSTRAINT ck_line_sources_subkind
        CHECK ((source_kind = 'expenses.entry') = (source_subkind IS NOT NULL)),
    CONSTRAINT ck_line_sources_state CHECK (state IN ('held', 'invoiced', 'released')),
    CONSTRAINT ck_line_sources_quantity CHECK (quantity > 0)
);
-- The floor (D2): a source is held by one live draft or invoiced by one
-- unreleased issued line, whatever the interleaving.
CREATE UNIQUE INDEX ux_line_sources_live ON invoices.line_sources (source_kind, source_id)
    WHERE state IN ('held', 'invoiced');
CREATE INDEX ix_line_sources_invoice ON invoices.line_sources (invoice_id);
CREATE INDEX ix_line_sources_line ON invoices.line_sources (line_id);

-- A credit note's release of an original line's source (D8): written by the
-- credit note's issue, under refuse_issued_child_change through invoice_id.
CREATE TABLE invoices.line_releases (
    id             bigint GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id     bigint NOT NULL,
    credit_line_id bigint NOT NULL,
    line_source_id bigint NOT NULL REFERENCES invoices.line_sources (id) ON DELETE RESTRICT,
    CONSTRAINT fk_line_releases_line FOREIGN KEY (credit_line_id, invoice_id)
        REFERENCES invoices.lines (id, invoice_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_line_releases_source ON invoices.line_releases (line_source_id);
CREATE INDEX ix_line_releases_invoice ON invoices.line_releases (invoice_id);

-- The timesheet as printed (D5): a snapshot, never the entry's note.
CREATE TABLE invoices.timesheet_rows (
    id           bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id   bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    position     integer      NOT NULL,
    source_id    bigint       NOT NULL,
    person_label varchar(200) NOT NULL,
    entry_date   date         NOT NULL,
    hours        numeric(5,2) NOT NULL,
    work_type    varchar(100),
    description  varchar(200) NOT NULL,
    CONSTRAINT ck_timesheet_rows_position CHECK (position >= 1),
    CONSTRAINT ck_timesheet_rows_hours CHECK (hours > 0)
);
CREATE UNIQUE INDEX ux_timesheet_rows_invoice_position ON invoices.timesheet_rows (invoice_id, position);

-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_issued_line_source_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_status text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT status INTO parent_status FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
        IF parent_status = 'issued' THEN
            RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
        END IF;
        IF NEW.state <> 'held' THEN
            RAISE EXCEPTION 'invoices: a line source is written held' USING ERRCODE = 'P0001';
        END IF;
        RETURN NEW;
    END IF;
    SELECT status INTO parent_status FROM invoices.invoices WHERE id = OLD.invoice_id FOR SHARE;
    IF TG_OP = 'DELETE' THEN
        IF parent_status = 'issued' THEN
            RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
        END IF;
        RETURN OLD;
    END IF;
    -- UPDATE: one transition per parent state, nothing else changed.
    IF (to_jsonb(NEW) - 'state') IS DISTINCT FROM (to_jsonb(OLD) - 'state') THEN
        RAISE EXCEPTION 'invoices: a line source changes only its state' USING ERRCODE = 'P0001';
    END IF;
    IF parent_status = 'issued' AND OLD.state = 'invoiced' AND NEW.state = 'released' THEN
        RETURN NEW;
    END IF;
    IF parent_status IS DISTINCT FROM 'issued' AND OLD.state = 'held' AND NEW.state = 'invoiced' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_line_sources_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.line_sources
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_line_source_change();
CREATE TRIGGER tr_line_releases_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.line_releases
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();
CREATE TRIGGER tr_timesheet_rows_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.timesheet_rows
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();

-- +goose Down
DROP TABLE invoices.timesheet_rows;
DROP TABLE invoices.line_releases;
DROP TABLE invoices.line_sources;
DROP FUNCTION invoices.refuse_issued_line_source_change();
ALTER TABLE invoices.settings
    DROP CONSTRAINT ck_settings_timesheet_person_label,
    DROP COLUMN timesheet_person_label,
    DROP COLUMN timesheet_default,
    DROP COLUMN work_vat_code_milestones,
    DROP COLUMN work_vat_code_expenses,
    DROP COLUMN work_vat_code_hours;
DROP INDEX invoices.ix_invoices_project;
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_project,
    DROP COLUMN timesheet,
    DROP COLUMN project_reference,
    DROP COLUMN project_id;
-- Restoring the old CHECK fails while a deduction line exists, which is the
-- honest answer: a Down never deletes bookkeeping.
DROP INDEX invoices.ix_lines_deducts;
ALTER TABLE invoices.lines
    DROP CONSTRAINT ck_lines_deduction_no_discount,
    DROP CONSTRAINT ck_lines_quantity,
    ADD CONSTRAINT ck_lines_quantity CHECK (quantity > 0),
    DROP COLUMN deducts_invoice_id,
    DROP CONSTRAINT uq_lines_id_invoice;
```

`refuse_issued_document_change` (`mig/00034_invoices_baseline.sql:256-281`) freezes the three new document columns at issue without a change (jsonb less the three it exempts).

**Queries written now** (`queries/work.sql`; sqlc needs them, the tasks give them callers): `LineSourcesOf` (a document's rows with their line's position, ordered by position, kind, id), `InsertLineSources` (one `INSERT … SELECT … FROM unnest(@line_ids::bigint[], @kinds::text[], @ids::bigint[], @revisions::int[], @subkinds::text[], @project_ids::int[], @quantities::numeric[], @amounts::numeric[], @currencies::text[], @dates::date[]) ORDER BY source_kind, source_id` with `invoice_id` a parameter), `DeleteHeldSourcesOf` (a draft's `held` rows, `RETURNING source_kind, source_id`), `LiveSourcesElsewhere` (rows of the given kinds/ids in `held`/`invoiced` whose `invoice_id <> @invoice_id`, with the document's number and status, the oldest first), `LiveSourcesFor` (every live row of the given kinds/ids — the view's exclusion), `HeldOnDrafts` (per `invoice_id`, `project_id`, `source_kind` the count of `held` rows, for the given projects), `MarkSourcesInvoiced` (`state = 'invoiced' WHERE invoice_id = @id AND state = 'held'`), `InvoicedSourcesOfLines` (`state = 'invoiced'` rows of the given line ids), `ReleaseSources` (`state = 'released' WHERE id = ANY(@ids) AND state = 'invoiced' RETURNING *`), `InsertLineReleases` (unnest), `ReleasesOf` (a credit note's), `ReleasedHistoryOf` (for the given kinds and ids, each `released` row's original invoice number and the credit note that released it, newest first — the re-pull note's source, Task 10), `SetDocumentProject`, `TimesheetRowsOf`, `InsertTimesheetRows` (unnest), `DeleteTimesheetRows`, `PruneTimesheetRows` (`source_id <> ALL(@kept)`); `queries/deductions.sql`'s queries are Task 11's.

- [ ] **Step 1: Schema tests first** — `TestInvoicesWork_AppliesAndIsIdempotent` (`applyUpDownUp(t, url, 40)`: every column, CHECK, index and trigger; Down removes all; `ux_line_sources_live`'s predicate by `indexDefinition`), `TestInvoicesWork_TheLineSourceTrigger` (an insert as `invoiced` refused; under a draft `held → invoiced` allowed and `held → released` refused; a revision changed with the state refused; under an issued parent every INSERT, DELETE and UPDATE refused but `invoiced → released`, and that one only once; a cascade from deleting a draft allowed), `TestInvoicesWork_TheLiveIndex` (two live rows refused; a released one and a live one allowed), `TestInvoicesWork_OverlappingHoldsInOppositeOrderFailOnTheIndexNeverDeadlock` (two raw transactions each `InsertLineSources` an overlapping set given in opposite selection order: exactly one `23505` on `ux_line_sources_live`, never `40P01`; shown able to fail by inserting row by row in the given order), `TestInvoicesWork_LineReleasesAndTimesheetRowsAreFrozenWithTheirDocument`, `TestInvoicesWork_TheCompositeKeysRefuseAMismatchedDocument`, `TestInvoicesWork_TheQuantityCheck` (negative only with `deducts_invoice_id`; no discount on a deduction), `TestInvoicesWork_TheSettingsDefaults`; widen the baseline tests' table, trigger and function counts and the reserved-word test (`schema_test.go:3189`) at 40. Red.
- [ ] **Step 2:** the migration, `sqlc.yaml`, the queries, `InsertLine` returning its id; generate twice; green; the invoices suite unchanged.
- [ ] **Step 3: commit** `feat(invoices): the work schema — line sources held from the draft with their one transition, releases, timesheet rows, deduction lines, the project and the work settings` with `Docs-Impact: none — schema and queries only; every column reaches the wire, with its pages, in Tasks 6–12`; the per-commit check.

### Task 6: The hold carried through the draft — sources on the line, the block, freshness, refresh (D2, D17; phase 3B)

**Files:** create `inv/sources.go`, `inv/sources_test.go`; modify `inv/drafts.go` (`parseDraft` `:145-257` reads `lines[i].sources`; `PostInvoices` `:387-453` refuses any `lines[i].sources` with a 400 — work is added through `from-work`; `PutInvoicesById` `:461-527` requires them when the draft holds any and reads the freshness for `refreshSources`; `saveDraft` `:538-583` grows a 400 return beside its 409 and, with `writeLines` `:351-378`, carries the snapshot), `inv/responses.go` (`lineResponse` `:58-83`, `renderInvoice` `:241-433`), `inv/contractscalls.go` (`billableHours`, `billableExpenses`, `billableMilestones` accessors, each `noteContractCall`), `inv/harness_test.go` (`fakeBillable` implementing the three reads, recorded and locked-checked like `fakeCustomers`), `openapi/invoices.yaml`, `apps/invoices/frontend/src/test/fixtures.ts`, generated files; docs.

**Interfaces:** `type heldSource struct{ kind contracts.WorkSourceKind; id int64; revision int32; subkind *string; projectID int32; quantity, amount *big.Rat; currency string; date time.Time; state string; linePosition int32 }`; `parseLineSources(i int, l gen.InvoicesLineRequest) ([]sourceRef, map[string][]string)`; `carrySources(held []heldSource, lines []draftLine) (rows []heldSource, released []sourceRef, errs map[string][]string)` — every identity must be held by this draft (else 400 "work is added through the uninvoiced view"), none twice, `sources` present on every line when the draft holds any (400 on `lines[i].sources`), `[]` drops; `insertSources(ctx, txq, invoiceID, lineIDs []int64, rows []heldSource) error` (one `InsertLineSources`, ordered); `writeLines` returns the new line ids. `saveDraft` reads `LineSourcesOf` **under the document's lock, before `DeleteLines`**, applies `carrySources` there (the refusal is a 400 answered after the rollback), re-inserts; a customer change deletes every held row (reading 33). `sourcesBlock(rows)`, `lineDiffers(line, rows) bool` (`line_net ≠ round2(Σ amount)`), `freshness(ctx, held) (map[sourceRef]string, error)` reading the composed `Billable*` by `IDs` on the pool and judging **the holders' own facts** — Time and Projects: revision, project, currency and amount by value; Expenses: `bill_amount`, project, currency and `source_subkind`, never the revision — called in `GetInvoicesById` (`:616-654`) **for invoice drafts only** and before `PUT`'s transaction for `refreshSources`, never in the list. `refreshSources` takes each still-billable source's new revision, quantity and amount, drops the rest (`sources_released`), and under the lock requires the held set it read (reading 32). The cap: more than `contracts.MaxBillableRows` identities on a save is a 400 `too_many_sources` on `lines`. A credit-note draft's request naming `sources` on any line is a 400 (D16: no work is added to a credit note); `putCreditDraft` (`inv/credits.go:673-797`) is otherwise untouched here.

**Contract:** `InvoicesSourceRef {kind, id}`; `InvoicesLineSource {kind, id, projectId, date, quantity, amount, state}`; `InvoicesLineRequest.sources?: [InvoicesSourceRef]`; `InvoicesLine.sources?`, `InvoicesLine.warnings?: [string]`; `InvoicesInvoiceRequest.refreshSources?: boolean`; `InvoicesInvoiceResponse.sources?: InvoicesSourcesBlock {count, held, invoiced, released}`, `releasedSources?: [InvoicesSourceRef]`; warnings `line_differs_from_sources`, `sources_released`, `source_changed`, `source_not_invoiceable`; the 400 messages on `lines[i].sources`; `too_many_sources`.

- [ ] **Step 1: Tests first** (held rows planted by SQL on a draft made through the API, the harness's `Exec`; `fakeBillable` for freshness): `TestSources_CarriedAcrossNewLineIds` (snapshot kept: revision, quantity, amount, project, date), `TestSources_MovedBetweenLinesAndDropped`, `TestSources_RequiredOnEveryLineOfASourcedDraft` (absent → 400 on `lines[i].sources`; `[]` drops with `sources_released`), `TestSources_APutNeverAddsAHold` (an unheld identity → 400; one named twice → 400), `TestSources_ADraftWithoutSourcesNeedsNone` (existing clients unchanged), `TestSources_ACustomerChangeReleasesEveryHold` (`releasedSources` naming each), `TestSources_DeletingTheDraftReleases` (the cascade; the index free), `TestSources_TheCap`, `TestSources_LineDiffersFromSourcesWarns` (per line and once on the document), `TestSources_FreshnessOnGet` (changed; gone; no read in the list; none on a credit-note draft or an issued document; nil provider → no warning; no call under a lock), `TestSources_AReimbursementIsNoChangeOnGet` (an expense's revision moved, its billing facts not: no warning), `TestSources_APostWithSourcesIsA400`, `TestSources_RefreshSourcesTakesNewRevisionsAndDrops`, `TestSources_ASaveBetweenRefreshReadAndLockIsInvoiceChanged`, `TestSources_TheBlockIsAnsweredFromOwnRows`. Red; implement; green; shown able to fail by deleting the carry (the first test) and by inserting row by row.
- [ ] **Step 2: Docs** — `R/invoices.md`: a new "Invoicing work" section begun with "The link and its states" (the table, held/invoiced/released, the floor, what a save carries and drops, the warnings, freshness, refresh); the model table's `line_sources`; `en|nb/user/invoices.md`: one paragraph — a draft made from work keeps its work on its lines, and "Refresh work". `docs:check`.
- [ ] **Step 3:** fixtures; typecheck; **commit** `feat(invoices): work held on the draft — sources carried by every save, never added by one, dropped by name, with the block, the freshness warnings and refresh`; the per-commit check.

### Task 7: The write-back at the issue — the holders on the issue's transaction (D1's Invoices side; phase 3B)

**Files:** create `inv/writeback.go`, `inv/writeback_test.go`; modify `inv/server.go` (`withLockedTx` `:102-115`), every `withLockedTx` caller (fifteen, mechanical: `_ pgx.Tx`), `inv/contractscalls.go` (`contractCallHook` with `txBound`; `noteTxCommand`; `projectEntries` → `Projects.Projects`; `userEntry` → `Users.User`), `inv/issue.go` (`:57`, `:218-456`), `inv/export_test.go` (`SetContractCallHook`, `SetIssueAfterAllocation`, new `SetIssueBeforeLock`, `NoteTxCommand` exported for the hook test), `inv/harness_test.go` (`noteContractCall` with the flag; the cleanup `:73-78` failing a locked non-tx call and an unlocked tx command; `fakeHolder` — per kind, recording each call's op, ref, sources and `SELECT pg_current_xact_id()::text` on the `pgx.Tx` it is handed, able to refuse a source or fail; `fakeProjects` for `WithProjects`), `inv/issue_test.go` and `inv/customer_slots_test.go` (the hook's new signature), `openapi/invoices.yaml`, fixtures, generated files; docs `MB`, `R/invoices.md`, `en|nb/user/invoices.md`.

**Interfaces:**

```go
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error
var contractCallHook func(ctx context.Context, method string, txBound bool)
func noteTxCommand(ctx context.Context, name string) // "InvoicedWork.<kind>.Mark|Release"
var issueAfterAllocation func(ctx context.Context, tx pgx.Tx, invoiceID int64) error
var issueBeforeLock func(ctx context.Context, invoiceID int64)
type holders map[contracts.WorkSourceKind]contracts.InvoicedWorkHolder
func (s *server) holders() (holders, error) // from s.deps.InvoicedWork by Kinds()
func (s *server) markWork(ctx context.Context, tx pgx.Tx, h holders, ref contracts.InvoiceRef, rows []heldSource) (*gen.InvoicesConflictProblem, error)
func (s *server) releaseWork(ctx context.Context, tx pgx.Tx, h holders, ref contracts.InvoiceRef, rows []heldSource) error // Task 10's caller
func workSources(rows []heldSource) map[contracts.WorkSourceKind][]contracts.WorkSource
```

**The issue, in order.** Before the transaction, beside the profile (`issue.go:238-243`), for an invoice: `LineSourcesOf` on the pool; when any — every kind claimed by a holder (else an error log and a 500); `s.deps.Projects == nil` → 409 `projects_unavailable`; `projectEntries` of their projects → a project gone or not billing `draft.CustomerID` → 409 `source_customer_changed` (`linePosition`, `sourceKind`, `sourceId`); `userEntry(callerID)` → `IssuedByDisplay` (reading 20); then `issueBeforeLock`. Under the lock, after the KID (`:358-371`) and before step 6 (`:373`): `LineSourcesOf` again — a different set (kind, id, revision, amount, line) → `invoice_changed`; each project's `BillingType` from the pre-read → `source_not_selectable` (reading 19); `now := s.deps.Clock()` once; `markWork` calls each kind's holder **once, in `contracts.InvoicedWorkOrder`**, through `noteTxCommand`, with `InvoiceRef{locked.ID, number, issueDate, now, callerID, display}`; a `*contracts.WorkSourceRefusal` (`errors.As`) → `cannotIssue(code, detail)` with the first line holding the source, `SourceKind`, `SourceId` → `errRefused`; any other error → 500; then `MarkSourcesInvoiced`; then the snapshots, the VAT rows and `IssueDocument` with `params.Now = now`. `issueAfterAllocation(ctx, tx, id)` at `:275`.

**Contract:** `InvoicesConflictProblem` gains `sourceKind`, `sourceId`; its description the codes `source_not_invoiceable`, `source_changed`, `source_already_invoiced`, `source_customer_changed`, `source_not_selectable`, `projects_unavailable` (and `invoice_changed`'s new cause).

- [ ] **Step 1: Tests first** (`modtest.WithInvoicedWork` fakes, `WithProjects`, the fixed clock): `TestIssue_CallsEachHolderOnceInTheLockOrder` (Projects → Expenses → Time, after the number and before any snapshot — a snapshot column still NULL when the fake runs), `TestIssue_TheHoldersRideTheIssuesTransaction` (`issueAfterAllocation(ctx, tx, id)` records `pg_current_xact_id()` through `tx`; every fake's value equals it), `TestIssue_TheRefHasTheNumberTheDateTheIssuerAndIssuedAt`, `TestIssue_ASourceRefusalIsA409WithItsLineAndSource` (each of the three codes; the number rolled back — the next issue takes it; no `line_sources` state moved), `TestIssue_AHolderErrorIsA500AndRollsBack`, `TestIssue_AnUnclaimedKindIsA500AndLogged`, `TestIssue_SourceCustomerChangedBeforeANumber`, `TestIssue_ProjectsUnavailableFailsClosed`, `TestIssue_ASaveSlippedBetweenIsInvoiceChanged` (`SetIssueBeforeLock`), `TestIssue_AProjectTurnedFixedPriceOrNonBillableIsNotSelectable`, `TestIssue_SourcesMoveHeldToInvoiced`, `TestIssue_ADraftWithoutSourcesCallsNoHolderAndReadsNoDirectoryMore`, `TestContractCallHook_FailsALockedCallAndAnUnlockedTxCommand` (the recorder's two lists, driven through an export of `noteTxCommand`), `TestIssue_NoDirectoryCallUnderTheLock` (the harness's cleanup, on every test above). Red; implement; green; shown able to fail by calling the holders after `IssueDocument` (the snapshot test), by opening a second transaction in the fake (the xact test), and by iterating the holders map (the order test).
- [ ] **Step 2: Docs** — `MB`: rule 10's Invoices half (the call's place in the issue — after every check and the number, before the document is written; the refusal and the rollback; `noteTxCommand`; the `pg_current_xact_id()` proof; the races, as Task 13 will prove them), the "Invoices requires customers" paragraph (`MB:285-311`: "phases 1A and 1B" gone; it now reads the project and user directories and the billable reads, optionally, and calls the holders), the "no contract call inside a transaction that holds a lock" sentence (`MB:261-264`) restated, rule 10's "Today's holders" linking `/en/reference/invoices/#invoicing-work`; `R/invoices.md`: "Invoicing work" gains "The write-back" (the order, the refusals, `projects_unavailable`, the lock order), Issuing's step list; `en|nb/user/invoices.md`: the issue's new refusals in words. `docs:check`.
- [ ] **Step 3:** fixtures; typecheck; lint; race on invoices; **commit** `feat(invoices): work marked invoiced inside the issue — the holders on the issue's own transaction, in the cross-module lock order, refused with the line and the source (rule 10)`; the per-commit check.

### Task 8: The uninvoiced view and the wizard (D3, D4, D6, D10–D12, D14, D15; phase 3B)

**Files:** create `inv/work.go`, `inv/grouping.go`, `inv/work_test.go`, `inv/grouping_internal_test.go`; modify `inv/meta.go` (`:23-65`), `inv/settings.go` (the three codes), `inv/contractscalls.go` (`projectEntry`, `projectsForCustomer`, `userEntries`), `inv/export_test.go` (`SetFromWorkBeforeInsert`, reading 36), `inv/queries/settings.sql`, `openapi/invoices.yaml`, `apps/invoices/frontend/src/pages/settings.tsx` (`SellerForm` round-trips the three codes it read) and `test/fixtures.ts`, generated files, `openapi/COVERAGE.md`; docs.

**Interfaces:** `GetInvoicesWork` — exactly one of `customerId`/`projectId` (400); `work_unavailable` (409) when no `Billable*` is composed; the projects (`projectsForCustomer` or `projectEntry` + its customer), the composed reads over them with `Until` (`until` or none), `LiveSourcesFor` and `HeldOnDrafts` from the own schema, `userEntries` for the hours' people; per project (code, name, billing type, currency) → per kind → rows with `selectable` and `reason` (`fixed_price`, `non_billable`, `currency`, `held`) and `heldBy`; totals per currency over the selectable rows; warnings `work_overdue_to_invoice` (per project, the oldest selectable item's date before `businessDay(today).AddDate(0, -1, 0)`), `currency_not_nok` (row), `supplier_invoice_rebilled` (row; and both rows of an uninvoiced duplicate pair in the page), `work_truncated` (any `More`). `PostInvoicesFromWork` — the amended D3 order (reading 30), each a refusal by code: (1) 400 on the body, a source named twice included, and 409 `too_many_sources` when the append target's held rows (`LineSourcesOf`, on the pool) and the new sources pass 5 000 — before any read (`BillableRequest.Validate` refuses past `MaxBillableRows` as the floor); (2) the settings, the profile, `customerGate`; (3) the three `Billable*` by `IDs`; (4) `projectEntries` of the found sources' projects; (5) `source_not_for_customer`, `source_not_selectable`, `source_not_invoiceable` (missing from the read, or a kind whose provider is nil), `source_changed` (hours and milestones by revision; an expense's revision is display-only), `mixed_currency` before `currency_not_nok`; (6) the lines (`groupLines`) with `too_many_lines` + `suggestedGrouping` over 500 with the target's; then one `withLockedTx`: insert the draft (or `LockInvoice` the target, still an invoice draft of this customer at `revision`), `writeLines` (appended after the target's own, theirs re-inserted with their sources carried), `LiveSourcesElsewhere` → 409 `source_held_elsewhere` (`heldBy {invoiceId, number?, status}`, the source), the `fromWorkBeforeInsert` seam, `InsertLineSources` (a `23505` on `ux_line_sources_live` mapped to the same code), the document's project (D9 fields arrive in Task 9; here the rows only). Prefills: `deliveryFrom`/`deliveryTo` the first and last source date unless given, `yourReference` and terms as `PostInvoices` (`drafts.go:404-412`). Each `line_sources` row's quantity and amount by kind: hours `HoursHundredths/100` and the exact `Amount`; mileage its km and `BillAmount`; an outlay, a supplier invoice or a milestone 1 and its amount; `source_subkind` the expense's kind. **The line language** is the profile's (`Language == "en"` → `en`, else `nb` — `buyerSnapshot`'s rule, `issue.go:127-131`). `grouping.go`: `groupLines(language string, grouping string, projects map[int32]contracts.ProjectEntry, hours, expenses, milestones, vatCodes) ([]draftLine, [][]heldSource)` — the key (project, kind) then the grouping's term; hours split by effective rate (`bill_rate × multiplier / 100` rounded half away from zero to four decimals); expenses by (project, expense kind) except `itemised`; one line per milestone; ordered by project code, kind (hours, expenses, milestones), key; D3's texts in `nb` and `en` (`timer`/`hours`, `km`, the period "september 2026" or "1.–15. sep. 2026"; `itemised` hours read "Konsulenttimer, <project>, <date> – <person>[ – <work type>]" / "Consulting hours, <project>, <date> – <person>[ – <work type>]", one entry per line); `suggestCoarser(grouping)` along `itemised → date → person → work_type → project`. Settings: `workVatCodes {hours, expenses, milestones}` on the response and **required** on the `PUT` request, an inactive code a 400 on its field; the wizard's defaults the settings', id 9 for every kind while `vat_registered` is false; a default that has since become inactive, with no override in the request, is a 400 on `vatCodes.<kind>` naming it ("choose another, or change the default in the settings").

**Contract:** `getInvoicesWork` (`GET /invoices/work?customerId=&projectId=&until=`, `invoices:access+invoices:create`; 200 `InvoicesWorkResponse {customerId, projects: [InvoicesWorkProject {id, code, name, billingType, currency?, hours: [InvoicesWorkHour], expenses: [InvoicesWorkExpense], milestones: [InvoicesWorkMilestone], heldOnDrafts, warnings}], totals: [{currency, amount}], users: [{id, displayName}], warnings}`; 400; 409 `work_unavailable`); `postInvoicesFromWork` (`POST /invoices/from-work`, `invoices:access+invoices:create`; body `InvoicesFromWorkRequest {customerId, sources: [{kind, id, revision}], grouping?, vatCodes?, deliveryFrom?, deliveryTo?, invoiceId?, revision?}`; 201 the document; 400; 404 the target; 409 the codes above and the gates). Each row schema carries `selectable`, `reason?`, `heldBy?`, `warnings?`. Meta: `workAvailable`, `work {hours, expenses, milestones}`. Settings: `workVatCodes`. `InvoicesConflictProblem` gains `heldBy`, `suggestedGrouping`.

- [ ] **Step 1: Grouping first** (internal): `TestGroupLines_EachGroupingsLinesAndTexts` (five groupings × nb/en), `TestGroupLines_SplitByEffectiveRateAndTheFourDecimals` (and `line_differs_from_sources` on the fifth-decimal case), `TestGroupLines_TheOrder`, `TestGroupLines_ThePeriodText`, `TestSuggestCoarser`. Red; implement; green.
- [ ] **Step 2: The view and the wizard** (`fakeBillable`, `WithProjects`, `fakeUsers`): `TestWork_TheViewPerCustomerAndPerProject`, `TestWork_HeldWorkIsListedNotSelectable` (`heldBy`, `heldOnDrafts`, out of the totals), `TestWork_BothOrNeitherIdIsA400`, `TestWork_WorkUnavailable`, `TestWork_FixedPriceAndNonBillableAreListedNotSelectable`, `TestWork_TheOverdueWarningAtItsBoundary`, `TestWork_CurrencyNotNok`, `TestWork_SupplierInvoiceRebilledOnBothRowsOfAPair`, `TestWork_Truncated`, `TestWork_NeedsCreate`, `TestFromWork_RefusalsInOrder` (each by removing its guard; `mixed_currency` before `currency_not_nok`), `TestFromWork_AnExpensesRevisionIsDisplayOnly`, `TestFromWork_TooManyLinesSuggestsACoarserGrouping`, `TestFromWork_TooManySources`, `TestFromWork_HeldElsewhereNamesTheFirstDraft`, `TestFromWork_TwoRacingHoldsEndInOne` (`SetFromWorkBeforeInsert` parks request A after its `LiveSourcesElsewhere`; request B, over an overlapping set in the opposite order, runs to 201; A released ends in the `23505` mapped to 409 `source_held_elsewhere` — never a 500), `TestFromWork_TooManySourcesCountsTheTargetsHolds`, `TestFromWork_LineSourcesQuantityAndAmountPerKind`, `TestFromWork_TheLineLanguageIsTheBuyers`, `TestFromWork_AnInactiveDefaultVatCode`, `TestFromWork_Prefills`, `TestFromWork_AppendsAtARevision` (and 409 at a stale one; refused on a credit note or another customer's draft), `TestFromWork_VatCodeDefaultsOverridesAndNotRegistered`, `TestSettings_WorkVatCodes` (defaults, inactive → 400, revision, `invoices:manage`), `TestMeta_WorkAvailable`. Red; implement; green; shown able to fail by dropping the under-lock `LiveSourcesElsewhere` (the index then answers) and by dropping the index mapping (a 500).
- [ ] **Step 3: Docs** — `R/invoices.md`: "Invoicing work" gains the view, the wizard and its refusals, grouping and line text, VAT codes and utlegg (D6: unsupported, no line says "utlegg"), D10's permission, D11, D12, D14, D15; the endpoints table; the settings section; meta; `en|nb/user/invoices.md`: the settings' work VAT codes sentence and one on invoicing work from a customer or a project. `docs:check`.
- [ ] **Step 4:** coverage report; fixtures; typecheck; **commit** `feat(invoices): the uninvoiced view and the wizard — work grouped five ways into bilingual lines, held under the draft's lock, with per-kind VAT codes`; the per-commit check.

### Task 9: The project dimension (D9; phase 3B)

**Files:** modify `inv/sources.go` (derive on every save, reading 14), `inv/work.go` (set at from-work), `inv/drafts.go` (`PUT` reads `ProjectDirectory.Projects` before its transaction when the held rows span projects), `inv/queries/invoices.sql` (`UpdateDraft`, `InsertInvoiceDraft` carry the pair; `ListInvoices`/`CountInvoices` a `project_id` filter), `inv/queries/credits.sql` (`InsertCreditDraft` copies the original's pair), `inv/list.go`, `inv/csvexport.go` (`:26-31`, `Project` last) and `inv/queries/export.sql`, `inv/pdf.go` (`buildPDFModel` `:249`: "Prosjekt"/"Project" in `meta`), `inv/ehf/doc.go` (`ProjectReference string`), `inv/ehf/render.go` (`:60-130`: BT-11 `cac:ProjectReference/cbc:ID` on an invoice after the `AdditionalDocumentReference`; on a credit note a second `cac:AdditionalDocumentReference` with `cbc:ID` the reference and `cbc:DocumentTypeCode` `50`, no attachment), `inv/ehfdoc.go`, `inv/responses.go`, goldens `inv/ehf/testdata/golden/{invoice-project-reference.xml,credit-note-project-reference.xml}`, `inv/ehf/render_test.go`, `inv/ehfdoc_internal_test.go`, `openapi/invoices.yaml`, fixtures; docs (with `docs/src/content/docs/en/contributing/e-invoice-validation.md`'s goldens list and `en|nb/admin/e-invoicing.md`'s EHF mapping).

**Contract:** the document and the list item gain `projectId`, `projectReference`; `GET /invoices` a `projectId` parameter; the CSV header's last column `Project`.

- [ ] **Step 1: Tests first** — `TestProject_DerivedOneTwoNone` (from-work over one project; a `PUT` dropping the other project's lines; none), `TestProject_NeverWrittenByARequest`, `TestProject_SnapshotFrozenAtIssue` (the trigger), `TestProject_ACreditNoteCopiesItsOriginals`, `TestList_TheProjectFilter`, `TestExportCSV_TheProjectColumnLast`, `TestPDF_TheProjectLine` (`pdfModelBuilt`), `TestEHF_ProjectReferenceIsBT11`, `TestEHF_ACreditNoteNamesItsProjectWithCode50` (read back with `encoding/xml`, namespace-qualified), goldens written with `-update` and committed; `TestEHF_Goldens` green without the flag. Red; implement; green; `mise run ehf:validate` green on the two new goldens (BT-11 order after `AdditionalDocumentReference` is the XSD's).
- [ ] **Step 2: Docs** — `R/invoices.md`: the model, the EHF mapping (BT-11, the code-50 reference), the CSV's `Project`, the list filter, the PDF; `contributing/e-invoice-validation.md`: the two new goldens; `en|nb/admin/e-invoicing.md`: the project in the EHF; `en|nb/user/invoices.md`: the project on a document and the list's filter. `docs:check`.
- [ ] **Step 3:** fixtures; typecheck; **commit** `feat(invoices): the project dimension — derived from the work, frozen at issue, BT-11 on an invoice and a code-50 reference on a credit note, the list filter and the CSV column`; the per-commit check.

### Task 10: Release on credit (D8; phase 3B)

**Files:** create `inv/releases_test.go`; modify `inv/credits.go` (`creditIssueChecks` `:603-659` decides the releases: for every credit line whose `total` squared it — `lastReturn` `:250-267` — the original line's `InvoicedSourcesOfLines`; `issuePlan` gains `releases []release`), `inv/issue.go` (before the transaction for a credit note: `LineSourcesOf(original)` on the pool and, when any are `invoiced`, `userEntry`; in step 6: `InsertLineReleases` with the credit note's `invoice_id` and the credit line, `ReleaseSources`, then `releaseWork` in `InvoicedWorkOrder` with `InvoiceRef{original.ID, *original.Number, original.IssueDate, now, callerID, display}` — reading 25 — all before `IssueDocument`), `inv/writeback.go`, `inv/responses.go` (`sources.wouldRelease` on a credit-note draft from `readCreditDraft`'s `squared`; an issued original's rows show `released`), `inv/work.go` (re-pulled released work, found by `ReleasedHistoryOf`: the note suggestion "Erstatter faktura <n>, kreditert med kreditnota <c>" / "Replaces invoice <n>, credited by credit note <c>" when the request has no note); an `invoiced` source whose kind no holder claims is **logged at error and skipped** on a release — the credit note is never blocked (the spec's reading 20), `openapi/invoices.yaml` (`InvoicesSourcesBlock.wouldRelease`), fixtures; docs.

- [ ] **Step 1: Tests first** — `TestRelease_AFullReturnReleases` (the holders called once each in order with the original's ref and the credit note's `IssuedAt`/`IssuedBy`; `line_releases` rows carrying the credit note's `invoice_id`; state `released`; the child trigger refusing a later write on them), `TestRelease_APartialReturnReleasesNothing`, `TestRelease_APriceReductionReleasesNothing`, `TestRelease_TheLastOfTwoPartialReturnsReleases`, `TestRelease_AMilestoneWhole`, `TestRelease_AHolderErrorRollsTheCreditNoteBack`, `TestRelease_AHolderToleratesAMissingStamp` (a fake that writes nothing still lets the credit note issue), `TestRelease_ACreditDraftSaysWhatItWouldRelease`, `TestRelease_TheWorkIsSelectableAgain` (the view and from-work after), `TestRelease_TheNoteSuggestionOnRePull`, `TestRelease_AnUnclaimedKindIsLoggedAndSkipped`, `TestRelease_NoDirectoryCallUnderTheLock`. Red; implement; green; shown able to fail by releasing on `isReturn` instead of `lastReturn`.
- [ ] **Step 2: Docs** — `R/invoices.md`: "Invoicing work" gains "Release on credit"; Credit notes; `en|nb/user/invoices.md`: crediting invoiced work returns it to the uninvoiced view only when its line is returned in full. `docs:check`.
- [ ] **Step 3:** fixtures; typecheck; **commit** `feat(invoices): work released by the credit note that returns its line in full — the release recorded on the credit side and the stamps taken back in its issue`; the per-commit check.

### Task 11: A-konto and the final settlement (D7; phase 3C)

**Files:** create `inv/deductions.go`, `inv/deductions_test.go`, `inv/queries/deductions.sql`; modify `inv/drafts.go` (`parseDraft` accepts a non-zero quantity provisionally; `checkInvoiceLines` `:304-317`: a negative quantity only with `deductsInvoiceId`, exactly −1, price > 0, no discount, exempt from the inactive-code check; `taxedLines` `:289-300` taxes a deduction at its a-konto line's snapshot), `inv/credits.go` (never-raise `:749`, the line cap `:553`, `lastReturn`'s sum `:266` and `isReturn` by magnitude with the same sign; a credit-note line negative exactly when its original is a deduction line; `deducts_invoice_id` derived, reading 15; `credit_total_negative`; `invoice_deducted` per (a-konto, VAT code)), `inv/queries/credits.sql` (`CopyLinesToCredit` `:28-40`: `quantity <> 0`, `deducts_invoice_id` copied), `inv/issue.go` (`invoiceIssueChecks` `:164-214`: deduction lines exempt from the code checks and taxed at the snapshot; the cap refused `deduction_exceeds_invoice` with the line; a deducted document no longer an issued invoice of this customer → 409; `invoice_total_not_positive`), `inv/responses.go` (warnings), `inv/pdf.go` (the deducted invoices under the references; a deduction's "-1" and "-125 000,00" with the unit price positive — `formatDecimal` `:183-203` as it is), `inv/ehf/{doc.go,render.go}` (`BillingReference` 0..n on an invoice, one per distinct deducted invoice, written where the credit note's single one is, `:109-115`; no `PrepaidAmount`), `inv/ehfdoc.go`, `inv/ehf/precheck.go` (the invariants over negative lines: BR-27's positive price, BR-CO-10/13 re-summed), goldens `invoice-final-settlement.xml` (two `BillingReference`s, negative lines) and `credit-note-of-settlement.xml`, `inv/ehf/{render_test.go,precheck_test.go}`, `inv/ehfdoc_internal_test.go`, `inv/export_test.go`, `docs/src/content/docs/en/contributing/e-invoice-validation.md` (the goldens list), `en|nb/admin/e-invoicing.md` (the EHF mapping: BG-3), `openapi/invoices.yaml`, fixtures, `openapi/COVERAGE.md`; docs.

**The save rules beside the cap:** a deduction line carries no `sources` (400 on `lines[i].sources`); its VAT code must be one the deducted a-konto has a line at (400 on `lines[i].vatCodeId`); one deduction line per (a-konto, VAT code) per draft (409 `deduction_duplicated`, with the line's position); `GET /invoices/{id}/deductible` on a credit-note draft is a 409 (reading 31).

**Interfaces:** `type deductible struct{ invoiceID, number int64; issueDate time.Time; vatCodeID int32; taxed taxedLine; left *big.Rat }`; `deductibleLeft(ctx, q *store.Queries, customerID int32, excludeID int64) ([]deductible, error)` — per (a-konto, VAT code): the lines' net at that code − what its issued credit notes credited on those lines − what issued settlements' deduction lines took + what their issued credit notes gave back; `checkDeductions(lines, left) []capBreach`. Queries (`deductions.sql`): `DeductibleNet`, `DeductedNet` (issued settlements' deduction lines per (deducted, code) net of their credit notes), `DeductionSnapshot` (the a-konto's line snapshot per code). At the issue they are read **under the counter, without a row lock** (reading 4).

**Contract:** `InvoicesLineRequest.deductsInvoiceId?`, `InvoicesLine.deductsInvoiceId?`; `getInvoicesByIdDeductible` (reading 31); conflicts `deduction_exceeds_invoice`, `deduction_duplicated`, `invoice_total_not_positive`, `credit_total_negative`, `invoice_deducted`; warnings `deduction_exceeds_invoice`, `invoice_total_not_positive`; the 400s on `lines[i].deductsInvoiceId` and `lines[i].quantity`.

- [ ] **Step 1: Tests first** — `TestDeduction_TheSaveRules` (−1 only with the field; on a credit note's own request a 400; a draft, a credit note, another customer's or itself as the deducted → 400), `TestDeduction_ExemptFromTheInactiveAndNoRateChecks`, `TestDeduction_TaxedAtTheSnapshotAcrossARateChange`, `TestDeduction_TheCapPerCodeAfterACreditAndAnEarlierSettlement` (warned on the draft, refused at the issue with the line), `TestDeduction_AZeroAndANegativeSettlementAreRefused`, `TestDeduction_TheCapIsReadUnderTheCounterNotLocked` (the settlement holds a milestone source; a `fakeHolder` callback parks the issue after the cap read, inside its transaction; a `FOR UPDATE NOWAIT` probe of the a-konto from a `pgx.Connect` of its own succeeds), `TestDeduction_NoSourcesOnADeductionLine`, `TestDeduction_TheCodeMustBeOneTheAKontoHas`, `TestDeduction_OneLinePerAKontoAndCode`, `TestDeductible_ACreditNoteDraftIsA409`, `TestCredit_ASettlementsCreditCopiesTheDeductionNegativeAndRestoresTheCap`, `TestCredit_ANegativeLineAgainstAnOrdinaryLineIsRefused`, `TestCredit_NeverRaiseTheLineCapAndLastReturnByMagnitude` (a sign change refused), `TestCredit_CreditTotalNegative`, `TestCredit_AZeroCreditNoteStillIssues`, `TestCredit_InvoiceDeductedPerVatCode`, `TestPDF_TheDeductionsMinusSigns` (nb and en), `TestEHF_ASettlementHasTwoBillingReferences`, `TestDeductible_TheRead`; the goldens with `-update`; `mise run ehf:validate` green. Red; implement; green; shown able to fail by comparing quantities signed (the magnitude test) and by row-locking the a-konto (the `NOWAIT` probe test).
- [ ] **Step 2: Docs** — `R/invoices.md`: "Invoicing work" gains "A-konto and the final settlement" (the deduction line, the cap, the positive gross, the credit-note rules, BG-3, no `PrepaidAmount`, the lock reading); Credit notes; the PDF; the EHF mapping; the endpoints; `en|nb/user/invoices.md`: "Final settlement" (the editor's step arrives in Task 14; the page describes the rule and the refusals now); `contributing/e-invoice-validation.md`'s goldens list; `en|nb/admin/e-invoicing.md`'s mapping. `docs:check`.
- [ ] **Step 3:** coverage report; fixtures; typecheck; **commit** `feat(invoices): a-konto and the final settlement — deduction lines taxed at the a-konto's snapshot, capped per VAT code under the counter, reversed by a credit note, and BillingReference 0..n`; the per-commit check.

### Task 12: The timesheet (D5; phase 3C)

**Files:** create `inv/timesheet.go`, `inv/timesheet_test.go`; modify `inv/work.go` (the `timesheet` field; the rows at from-work, named through `userEntries`), `inv/drafts.go` (the document's `timesheet` flag on `PUT`; turned on → the held hours read through `BillableHours` by `IDs` and their people through `userEntries`, both before the transaction, and written; off → deleted; every save prunes to the hours still held), `inv/sources.go` (`refreshSources` regenerates the refreshed entries' rows and then **every row's label**, so a refresh never leaves two numbering schemes on one timesheet), `inv/pdf.go` (a `timesheet` block in `pdfModel` `:149` after the totals and payment: its own pages, "Timeliste"/"Timesheet", date, person, work type, description, hours, a total per person and overall, paginated with maroto's `AddRows`; the preview renders it), `inv/settings.go` (`timesheetDefault`, `timesheetPersonLabel`), `inv/customer_slots.go` (each exported document's `timesheet`, the rows as printed; the erase deletes a draft's with the draft by the cascade and keeps an issued one's, `:467`), `inv/export_test.go` (`SetPDFModelBuilt` reports the timesheet rows), `openapi/invoices.yaml`, `apps/invoices/frontend/src/pages/settings.tsx` (round-trip the two fields), fixtures; docs.

**Interfaces:** `personLabels(mode string, names map[uuid.UUID]string, order []uuid.UUID) map[uuid.UUID]string` — `initials` (default; "KN", a second "KN" → "KN2"), `number` ("Person 1", … in order of first appearance on the invoice), `name`; `timesheetRows(hours []contracts.BillableHour, labels, projectNames) []store.InsertTimesheetRowsParams` — description the task title, else the project's name, **never the note**.

**Contract:** `InvoicesFromWorkRequest.timesheet?`; `InvoicesInvoiceRequest.timesheet?`; the document `timesheet`, `timesheetRows: [InvoicesTimesheetRow {position, personLabel, date, hours, workType?, description}]`; settings `timesheetDefault`, `timesheetPersonLabel`.

- [ ] **Step 1: Tests first** — `TestTimesheet_RowsOnlyWithTheFlag`, `…_PrunedOnSave`, `…_WrittenWhenTurnedOnAndDeletedWhenOff`, `…_RegeneratedByRefreshSources` (every label renumbered), `…_EachPersonLabelAndTheInitialsCollision`, `…_NeverTheNote` (a note planted in the fake's source; absent from rows, PDF model and export), `…_FrozenAtIssue`, `TestPDF_TheTimesheetBlock` (`pdfModelBuilt`: the rows and the totals), `TestPDF_TheTimesheetPaginates` (a few hundred rows render without error over several pages), `TestPDF_ThePreviewCarriesIt`, `TestPDFStore_StoreOnceUnchanged` (the key and the hash rule), `TestExport_TheTimesheet`, `TestErase_ADraftsTimesheetGoesAndAnIssuedOneStays`, `TestSettings_TheTimesheetFields` (defaults, the label CHECK as a 400, revision, `invoices:manage`). Red; implement; green.
- [ ] **Step 2: Docs** — `R/invoices.md`: "Invoicing work" gains "The timesheet" (what it holds, never the note, the label, retention five years with the document, the employer's art. 13 notice, art. 17(3)(b) for issued rows, identity's deletions never touching a snapshot); the PDF; Retention and personal data; settings; `en|nb/user/invoices.md`: the settings' timesheet default and label, and the timesheet on an invoice. `docs:check`.
- [ ] **Step 3:** fixtures; typecheck; **commit** `feat(invoices): the timesheet inside the PDF — a snapshot of the held hours by initials, number or name, never the note`; the per-commit check.

### Task 13: The slots and the integration tests (D1's races, D5's slots, the end-to-end story)

**Files:** modify `inv/customer_slots_test.go` (the cascade of a draft's `line_sources` and `timesheet_rows` in the erase; the export's `timesheet` and `projectReference`), `srv/modtest/modtest.go` (`WithPoolMaxConns(n int)`, reading 28), `srv/integration/harness_test.go` (an installation of customers + projects + time + expenses + invoices with the real holders and reads), create `srv/integration/work_test.go`, `srv/integration/work_races_test.go`. (`R/customers.md`'s holder-order sentence is Task 4's.)

- [ ] **Step 1: The slots** — `TestErase_ADraftsHoldsAndTimesheetGoWithIt` (the index free afterwards), `TestExport_TheProjectAndTheTimesheet`. Red if missing; implement; green.
- [ ] **Step 2: The story** — `TestWork_FromApprovedWorkToReleasedWorkEndToEnd` (`invoicesInstallation` with projects, time and expenses, `figures_test.go`'s `buildFixture`): two people's approved hours, a re-billable expense, a ready milestone → `GET /invoices/work` → `from-work` (`project`, timesheet on) → issue → every source stamped with the invoice's id and number by the issue's own commit (`invoicedBy` on each module's wire), `ActualsTotals.Invoiced` and `ProjectExpenses`' invoiced bucket moved → an a-konto of a second milestone, then a settlement holding positive work (a third milestone) at least as large as the deduction, deducting the a-konto → a full credit of the first invoice → its sources released in all three modules → pulled into a new draft with the note suggestion.
- [ ] **Step 3: The races** (`MaxConns = 2` for the two racing writers; each raw lock-holder, every `NOWAIT` probe and every `pg_locks`/`pg_blocking_pids` read on its own `pgx.Connect`; every racing request under a context deadline; each pair held at a lock, both finishing, one outcome winning, the loser's refusal the named one; never `40P01`): `TestWorkRace_IssueAgainstTheExpensesManualMark` (the mark first → the issue `source_already_invoiced`; the issue first → the mark 409 `invoiced_by_invoices`), `…AgainstABatchReimbursement` (`srv/expenses/flow.go:218-226`: no loser — both commit; the stamp unchanged by the revision bump), `…AgainstAMilestoneMove` (the manual `ready → invoiced` first → the issue `source_already_invoiced`; the issue first → the stale-revision 409, the stamp having moved the milestone's revision, then a move at the current revision answers 400 on `status`), `…AgainstAFixedPriceEdit` (`LockProject`; the edit first on a percent milestone → the issue `source_changed`; the issue first → the edit commits against the frozen amount), `…AgainstATimeUnapprove` (the unapprove first → the issue `source_not_invoiceable`; the issue first → the unapprove's "Entry n is invoiced"), `…AgainstACustomerMerge` (the draft's customer's project holds a held milestone; while the merge waits, `pg_blocking_pids(merge) = {issue}` and a `FOR NO KEY UPDATE NOWAIT` probe of the project succeeds — the merge holds no project row; no loser — both commit). Shown able to fail by giving projects a `CustomerReferences` slot ahead of invoices (the merge race deadlocks) and by making a holder read a directory (the pool of two starves).
- [ ] **Step 4: commit** `test(integration): work becomes invoices end to end, and the issue raced against every writer of its sources on a pool of two` (the modtest and slot changes in the same commit) with `Docs-Impact: none — tests and a test seam` — unless Step 1 changed `customer_slots.go`, which then updates `R/invoices.md`'s retention paragraph in the same commit; the per-commit check.

### Task 14: The apps (D18)

**Files:** `apps/invoices/frontend/src/**` — `api/work.ts` (`getWork`, `postFromWork`; every mutation invalidates `[INVOICES_QUERY_KEY]`), `api/deductible.ts`, `components/uninvoiced-work-panel.tsx` (exported from `index.ts`: per project and kind, checkboxes, totals per currency, the non-selectable rows greyed with their reason — fixed price, non-billable, currency, "on draft n" as a link — the warnings), `pages/-from-work-wizard.tsx` (selection and totals, the grouping with the line count it would make, the timesheet flag, the three VAT codes, the period; "Create draft" / "Add to draft n"; every 409 in words), `pages/invoice.tsx` (**every save sends each line's `sources` back as read, and `[]` on every line when the customer changes** — a client that drops the field loses work; each line's sources and warnings, "Refresh work", the timesheet toggle and rows, the project, the "Deduct earlier invoices" step over `getInvoicesByIdDeductible`, on a credit-note draft what its issue would release), `pages/settings.tsx` ("Work to invoice" card), `components/invoice-table.tsx` (the project column and filter), `lib/errors.ts` (every new code), `i18n.ts`, `test/fixtures.ts`; `apps/host/frontend/src/routes/customers/-customer-invoices-tab.tsx` (the panel above `CustomerInvoicesPanel`), `routes/projects/$projectId.invoicing.tsx` + `-project-invoicing-tab.tsx` (gated by `-module-tab-gate.tsx` on `invoices` and `invoices:create`, listed in `projectDetailTabs` beside `visibleProjectDetailTabs`, `-project-detail-layout.tsx:144`), `-project-economy-route.tsx` (`:16`, `:44`: `invoicingHref` as `expensesHref`), the host catalogs (en, nb); `apps/projects/frontend/src/pages/project-economy.tsx` (`invoicingHref` prop; the invoice plan's "Invoiced by invoice n" and the undo hidden on a module-stamped milestone), `apps/expenses/frontend/src/{components/entry-details.tsx,components/project-expenses-panel.tsx,pages/-undo-invoiced.tsx,lib/errors.ts}` (the badge with an optional `invoiceHref`, the undo hidden, `invoiced_by_invoices` in words), `apps/time/frontend/src/{lib/status.ts,…}` (`:2`, `:13`: "Invoiced by invoice n", a link when the host passes `invoiceHref`); each package's catalogs (en + nb) and tests.

- [ ] **Step 1:** fixtures; tests first — the panel (per project and kind, greyed rows and reasons, the held link, totals per currency, the warnings), the wizard (the line count per grouping, defaults, both buttons, every refusal in words), the editor (`InvoiceEditor_SendsEachLinesSourcesBackOnEverySave`, `InvoiceEditor_SendsEmptySourcesWhenTheCustomerChanges`, sources, warnings, refresh, timesheet, project, the deduct step, `wouldRelease`), the settings card, the host's tab gating and the economy link, the three badges with and without an href and the hidden undo, both catalogs everywhere. Red; implement; `biome --write`; tests, typecheck, lint, `translations:check` for every touched package.
- [ ] **Step 2: commit** `feat(invoices-ui): invoicing work — the uninvoiced panel, the wizard, the draft's sources, the settlement step and the work settings; the host's tabs and the source apps' invoiced badges` with `Docs-Impact: none — the user guide for these screens lands in the next commit (user/projects.md describes the Invoicing tab)`; then Task 15 immediately.

### Task 15: The documentation (D19)

**Files:** `R/invoices.md` (the "Invoicing work" section completed and every table: the model, endpoints, permissions — D10, no sixth permission — settings, PDF, EHF BT-11 and BG-3, the CSV's `Project`, retention; "What comes next": the 2028 buyer org-number rule, utlegg, several attachments, construction's § 8-1-2a), `en|nb/user/invoices.md` ("Invoicing work", "Final settlement", the settings card, the editor's sources, the timesheet), `en|nb/user/time.md`, `expenses.md`, `projects.md` ("Invoiced by invoice n", where the manual mark and undo are refused; `projects.md`'s tab table, `:69-77`, gains **Invoicing**), `R/time.md`, `R/expenses.md`, `R/projects.md`, `R/customers.md` (verify Tasks 2–4), `MB` (verify Tasks 1 and 7), `ROADMAP.md` (phase 3 done with what it delivered; the 2028 rule and utlegg in the invoices backlog).

- [ ] **Step 1:** every section D19 lists, English first, then Norwegian; the docs read against the code as 1B's were.
- [ ] **Step 2:** `mise run docs:check`; the per-commit check.
- [ ] **Step 3: commit** `docs(invoices): invoicing work — the reference, the user guide in both languages, and the roadmap`.

### Task 16: Verify the whole branch and open the PR

- [ ] **Step 1:** generate twice and `gen:client` → no drift; gofmt; vet; `mise run server:check`; `go test -count=1 ./...`; race on `invoices`, `time`, `expenses`, `projects`, `integration`; govulncheck; every frontend package's checks (`mise run frontend:check`); `mise run docs:check`; `check-coverage.ts --base 361cdff6` with the trailers' waivers in mind (re-run per commit as each task did); `mise run ehf:validate`; the contract coverage report current; the greps — no `now()`/`CURRENT_DATE` in a new query, no `noteContractCall` reachable from `markWork`/`releaseWork`, no `deps.Pool` in a holder, no source module SQL naming `invoices.`, no `note` selected by `BillableHours`, `numeric(20,6)` nowhere, no `pg_locks` read standing as proof that a row is or is not locked.
- [ ] **Step 2:** the PR in phase 2's shape (What; Decisions — the spec's thirty-eight readings and this plan's thirty-six, the pre-flight's spec amendments first; Things to know — the four migrations, the merge's new holder order, the pool-of-two races, the new read `getInvoicesByIdDeductible`, utlegg unsupported; How it was built; Verification). `gh pr create --base main --head feat/invoices-work-to-invoices`. Watch CI; do not merge.
- [ ] **Step 3: Report.**

## Self-review

**Spec coverage.** D1 → Task 1 (the contract, the slots, the partition, MB's source half), Tasks 2–4 (the three holders, their migrations, the doors), Task 7 (the issue's call, `noteTxCommand`, the proof, the refusals, MB's Invoices half), Task 10 (the release's call), Task 13 (the races, the merge proved by `pg_blocking_pids` and a `NOWAIT` probe); D2 → Task 5 (table, trigger, index, one-statement insert), Task 6 (carried, required, dropped, the block, freshness, refresh), Task 8 (`source_held_elsewhere`, the racing holds); D3 → Task 1 (the contracts), Tasks 2–4 (the providers, Expenses' function), Task 8 (view, wizard, meta); D4 → Task 8; D5 → Task 5 (the table), Task 12; D6 → Task 5 (the columns), Task 8 (the codes, utlegg in the docs); D7 → Task 5 (column, CHECK), Task 11; D8 → Task 5 (`line_releases`), Task 10; D9 → Task 5 (columns), Task 9; D10 → Task 8 (the permission on the two operations), Task 15; D11, D12, D14, D15 → Task 8 (and D14's under-lock re-check, Task 7); D13 → Task 15 (the backlog); D16 → nothing adds it; D17 → each server task's **Contract**; D18 → Task 14; D19 → each task's docs step and Task 15. Every Testing bullet names a test above: D1's holders (Tasks 2–4, each refusal by removing its guard in the spec's order), Invoices (Task 7), the doors (Tasks 3–4), Compose (Task 1), the races (Task 13); D2 (Tasks 5, 6, 8); D3 (Tasks 2–4, 8); D4 (Task 8); D5 (Task 12); D6 (Task 8); D7 (Task 11); D8 (Task 10); D9 (Task 9); D11/D12/D14/D15 (Task 8); the integration story (Task 13); Frontend (Task 14); Docs (Task 15). Phasing: 3A is Tasks 1–4; 3B is Tasks 5 (its 3B half), 6–10 and Task 13's races; 3C is Tasks 11–12; the frontend of both lands in Task 14 (reading 29).

**Name consistency.** Added at the pre-flight: `milestoneEffectiveAmountRat`, `SetInvoicedWorkAfterLock` (time, expenses, projects), `SetFromWorkBeforeInsert`, `NoteTxCommand`, `ReleasedHistoryOf`, `deduction_duplicated`. SQL: `time.entries`/`expenses.entries`/`projects.billing_milestones` `.invoiced_invoice_id`, `.invoiced_number`; `ck_entries_invoiced_by`, `ck_entries_invoiced_by_status`, `ck_entries_invoiced_by_stamp`, `ck_billing_milestones_invoiced_by{,_status}`; `expenses.ready_to_invoice`; `invoices.line_sources` (`source_subkind`, `amount numeric(22,8)`), `ux_line_sources_live`, `refuse_issued_line_source_change`, `tr_line_sources_immutable`, `invoices.line_releases` (`ux_line_releases_source`), `invoices.timesheet_rows`, `uq_lines_id_invoice`, `lines.deducts_invoice_id`, `ck_lines_quantity`, `ck_lines_deduction_no_discount`, `invoices.project_id`/`project_reference`/`timesheet`, `ck_invoices_project`, `settings.work_vat_code_{hours,expenses,milestones}`, `timesheet_default`, `timesheet_person_label`. Go: `contracts.InvoicedWorkHolder`, `InvoiceRef`, `WorkSource` (`ExpenseKind`), `WorkSourceKind`, `WorkSourceRefusal`, `InvoicedWorkOrder`, `BillableRequest`, `BillableHours`/`BillableExpenses`/`BillableMilestones` and their pages, `MaxBillableRows`; `module.Deps.InvoicedWork`, `Module.InvoicedWork`, `invoicedWorkLast`, `withInvoicedWork`; `modtest.WithInvoicedWork`, `WithBillableHours`, `WithBillableExpenses`, `WithBillableMilestones`, `WithPoolMaxConns`; `newInvoicedWorkHolder`, `newBillableHours`/`Expenses`/`Milestones` in each module; Invoices' `withLockedTx(ctx, tx, txq)`, `noteTxCommand`, `issueAfterAllocation(ctx, tx, id)`, `issueBeforeLock`, `markWork`, `releaseWork`, `heldSource`, `carrySources`, `insertSources`, `freshness`, `groupLines`, `suggestCoarser`, `deductibleLeft`, `personLabels`. Wire: `invoicedBy`, `invoiced_by_invoices`, `ExpensesConflictProblem`, `ProjectsConflictProblem`; `getInvoicesWork`, `postInvoicesFromWork`, `getInvoicesByIdDeductible`; `InvoicesSourceRef`, `InvoicesLineSource`, `InvoicesSourcesBlock` (`wouldRelease`), `releasedSources`, `InvoicesLine.warnings`, `refreshSources`, `deductsInvoiceId`, `timesheet`, `timesheetRows`, `InvoicesTimesheetRow`, `projectId`, `projectReference`, `workVatCodes`, `timesheetDefault`, `timesheetPersonLabel`, `workAvailable`, `work`, `InvoicesWorkResponse`, `InvoicesWorkHour`/`Expense`/`Milestone`, `InvoicesFromWorkRequest`, `heldBy`, `heldOnDrafts`, `suggestedGrouping`, `sourceKind`, `sourceId`; the codes of D17 plus `invoice_changed`'s new cause. Kinds `time.entry`, `expenses.entry`, `projects.milestone` everywhere; groupings `project`, `work_type`, `person`, `date`, `itemised`; labels `initials`, `number`, `name`.

**Where the spec could not be planned as first written** — each amended in the spec by the commit that carries this plan's pre-flight revision: `numeric(20,6)` for Time's amounts (reading 3); D3's "less what live documents hold" against D18's "on draft n" (reading 12); D17's missing read for the settlement step (reading 31); warnings carrying identities and positions on a `[]string` field (reading 9); D2's save-side `too_many_sources`, now a body bound only; D3's from-work order (reading 30); the `pg_locks` proofs (reading 34); the Projects release never refusing (reading 18); a holder's constructor needing the logger, not the clock (reading 5); `person_label varchar(200)`.
