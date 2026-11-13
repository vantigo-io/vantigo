# Accounting phase 1 — deliveries 0, A, B, C, D, E, F: implementation plan

This plan builds the first phase of the Accounting module. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-14-accounting-design.md`, revision 2. Its
  decisions are numbered A*n*, and its §13 (R*n*) amends them and wins.
- **The research:** `docs/superpowers/research/2026-11-14-accounting-module.md`, called
  "R" below.

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/accounting/frontend/src/`.

## How it lands: one stack

On 2026-11-14 the owner asked to *go full auto implement and fix if you meet any
hurdles*. Review happens once the stack is built.

- **The tool:** `gh stack`, used only with its non-interactive flags.
- **Merging:** the owner merges the whole stack with `gh stack merge <top PR> --yes`.
- **Each layer** is one PR. Its diff is that delivery and nothing else, and its body says
  which layer it sits on.

| # | Branch | Delivery | Migrations claimed (main at `2fa35614a`, latest `00177`) |
| --- | --- | --- | --- |
| 1 | `claude/vantigo-accounting-app-aababf` | Research, spec, plan (docs only) | — |
| 2 | `claude/accounting-0-seams` | 0: the sales journal, two events, `srv/brreg` | `00178_invoices_journal` |
| 3 | `claude/accounting-a-ledger` | A: the module and the ledger | `00179_accounting_baseline` |
| 4 | `claude/accounting-b-sales` | B: sales post themselves, write-offs | `00180_invoices_write_offs`, `00181_accounting_sales` |
| 5 | `claude/accounting-c-purchases` | C: suppliers, supplier invoices, expenses post | `00182_expenses_journal`, `00183_accounting_purchases` |
| 6 | `claude/accounting-d-vat` | D: VAT terms and the return | `00184_accounting_vat` |
| 7 | `claude/accounting-e-saft` | E: SAF-T export and import | `00185_accounting_saft` |
| 8 | `claude/accounting-f-bank` | F: bank accounts, statements, reconciliation | `00186_accounting_bank` |

**Migration numbers.** If `main` takes a number first, the owning layer renumbers its
migration in its own history before the stack is rebased (memory:
migration-number-collisions). Check `git ls-tree origin/main` right before merging. The
Folio bank feed, when built, will claim numbers too.

**The docs layer carries no `Docs-Impact` trailer.** It changes only
`docs/superpowers/**`.

**The bank feed (A29) is not in this stack.** It waits for the feed's own delivery A
(`specs/2026-11-13-bank-feed-folio-design.md`) and lands as a layer of its own after it.

## How each delivery is run

The routine used for Orders, Inventory and Manufacturing.

1. **The seam first, by one agent.** The migration, the OpenAPI operations (each with a
   `summary`), `go generate`/sqlc, the contract types, the skeleton and the registries.
   One compiling commit.
2. **Lanes.** Each lane owns disjoint files, and no two agents work in one Go package at
   once: server; the other module's side; `@vantigo/accounting-ui`; the host. The agent
   that runs a delivery does not delegate its lanes.
3. **Docs.** The bilingual user and admin pages and the reference, then
   `go generate ./...` in `apps/server`.
4. **Review.** An opus code review of server and frontend. Fix what it finds in the
   layer it belongs to.
5. **Gates,** in order:
   - `bun install --frozen-lockfile` once in the worktree;
   - `mise run server:check`;
   - `mise run server:test`. On SQLSTATE 53300, rerun the failing packages with `-p 1`;
   - `mise run frontend:check`. Read the log, not the exit line;
   - `mise run docs:check`, and per layer
     `bun run tools/docs/check-coverage.ts --base <parent branch>`.
6. **Commits.** Signed Conventional Commits: `feat(invoices)`, `feat(expenses)`,
   `refactor(customers)` for the seams; `feat(accounting)`, `feat(accounting-ui)`,
   `feat(host)`; `docs(accounting)`;
   `chore(assistant): regenerate the embedded documentation`.
7. **Next layer.** `gh stack add <branch>`, then `gh stack submit --auto`.

Workers are tested as `module.Workers` builds them, not through the harness's deps.

---

## Layer 1 — the design documents

R, the spec and this plan, committed as `docs(accounting): research, design and plan for
the Accounting module`. Then `gh stack init` adopts this branch as the bottom layer.

## Layer 2 — delivery 0, the seams (A12, A17; R1, R6, R13)

**Contracts** (`srv/contracts/sales_journal.go`, `events_invoices.go`):

- `SalesJournal` with `Documents`, `Facts`, `OpenAt` and their value types (R1).
- Events `invoices.document.issued` and `invoices.payment.removed`.
- The module slot on `module.Module` and `Deps`, resolved in `module.Workers` too.
- Fakes in `modtest`.

**Invoices** (`srv/invoices`):

- `mig/00178_invoices_journal.sql`: `journal_facts` with its deferred constraint
  triggers and the `journal` counter (R1); `line_kind`, `tax_category_id` and
  `project_id` on lines, snapshotted at issue (R6).
- Confirm every issue path takes the `documents` counter (R1).
- The provider, reading issued documents with lines and VAT summaries, payments with
  removals, charge payments and waivers.
- Publishing the two events in the transactions that make the facts.

**Brønnøysund:** move the client from `srv/customers` to `srv/brreg` (client, entity
decoding, config), without Customers' generated types (R13). Customers keeps its registry records, feed worker and behaviour.
Depguard: `srv/brreg` is a platform package every module may import.

**Tests:** each stream's order under concurrent writers; the cursor never skips a row;
events published once per fact; Customers' registry tests unchanged.

**Docs:** `reference/invoices.md` (the contract, the events),
`contributing/platform-packages.md` (`srv/brreg`), `reference/customers.md` where it
names the client.

## Layer 3 — delivery A, the ledger (A1–A11; R3, R5, R10, R12, R13)

**Seam:**

- `mig/00179_accounting_baseline.sql`: settings, counters, accounts, groupings, tax codes
  and rates, vouchers, postings, attachments, open items; the balance, immutability and
  lock triggers.
- `openapi/accounting.yaml`: meta (capabilities, activation state); settings and
  activation; accounts and groupings; tax codes; vouchers (list, read, post, reverse,
  attachments); the lock; open items; every report of A11 with CSV.
- `srv/openapi/gen/cfg-accounting.yaml`, the generate lines, the module skeleton with
  permissions, `accounting requires invoices` in config.
- Every registry AGENTS.md lists, the depguard sets counted by name.

**Lanes:**

1. **Server:** activation (seeding the chart, tax codes and roles in the workspace's
   language); accounts; the posting engine (one function every later delivery calls:
   validate, number, insert, open items); manual journals with tax computation;
   reversal; the lock; opening balances; the reports as SQL.
2. **UI** (`@vantigo/accounting-ui`): Overview; activation; Chart of accounts; Journal
   entry form; Vouchers list and page; Reports (one page, a picker); Settings with the
   lock.
3. **Host:** the routes, `apps.ts`, `i18n`, permission labels, the dashboard card, the
   spotlight, `sessionClients`; the Assistant's link prefixes and `promptVersion`.

**Data:** `srv/accounting/grouping/2026.csv` and `2027.csv` from Skatteetaten's
repository; the default chart as a Go table with English and Norwegian names.

**Docs:** `user/accounting.md` (en and nb): setting up, the chart, journals, reversal,
locking, reports, inviting an accountant. `admin/installation.md` (en and nb): storage
location and the backup test. `reference/accounting.md`. The overview listings,
`ROADMAP.md`, the `deploy/compose` `MODULES` and `GRANT` lines, `README.md`.

## Layer 4 — delivery B, sales (A12–A16; R1, R3, R4, R5, R7, R9)

**Seam:** `mig/00180_invoices_write_offs.sql`; `mig/00181_accounting_sales.sql` (posting
rules, cursors, catch-up requests and leases, unposted); the OpenAPI operations in both contracts; `SalesJournal` gains
the write-off kind.

**Lanes:**

1. **Server, Accounting:** the catch-up worker and the event handlers; posting of
   documents, payments, removals, charges and write-offs; posting rules; *Needs
   attention* with retry; the opening form's open invoices.
2. **Server, Invoices:** the write-off and its undo; the invoice's state and screens'
   data.
3. **UI:** Posting rules; Needs attention; the Customers report with open items;
   source links from a voucher to its invoice. In `@vantigo/invoices-ui`: *Write off* on
   an invoice, and a link from an invoice to its voucher when Accounting is on.
4. **Host:** the dashboard's attention count.

**Tests:** the table tests of spec §10; the integration test from an issued invoice to a
balanced voucher; a late handler; a failing fact.

**Docs:** `user/accounting.md` and `user/invoices.md` (en and nb);
`reference/accounting.md` with every rule and its version; `reference/invoices.md`.

## Layer 5 — delivery C, purchases (A17–A20; R2, R8)

**Seam:** `mig/00182_expenses_journal.sql`; `mig/00183_accounting_purchases.sql`
(suppliers, supplier invoices and lines); `contracts.ExpenseJournal` and the two events;
the OpenAPI operations.

**Lanes:**

1. **Server, Accounting:** suppliers with the Brønnøysund lookup and the personal-data
   slot; supplier invoices (draft, post, duplicate guard, reverse and copy, reverse
   charge); payments; posting of expenses with the VAT inference.
2. **Server, Expenses:** the provider and the events.
3. **UI:** Suppliers; Supplier invoices list and form with attachment; payment dialog;
   the Suppliers report; expense-category rules. In `@vantigo/expenses-ui`: the pointer
   to Accounting on the supplier-invoice kind.

**Docs:** `user/accounting.md`, `user/expenses.md` (en and nb); the two references.

## Layer 6 — delivery D, VAT (A21–A23; R4, R10)

**Seam:** `mig/00184_accounting_vat.sql` (terms); the OpenAPI operations; the
`mvamelding` XSDs and examples under `srv/accounting/mva/testdata`.

**Lanes:**

1. **Server:** the specification; terms from settings; close and reopen with the
   settlement voucher and the lock; the XML writer; *filed* registration.
2. **UI:** VAT page with the terms, the return laid out as Skatteetaten's, drill-down to
   postings, close, download, mark as filed.

**Tests:** the XML against the XSD; a term with every code kind; close refuses with
attention items; reopen restores.

**Docs:** `user/accounting.md` (en and nb); the reference.

## Layer 7 — delivery E, SAF-T (A24–A25)

**Seam:** `mig/00185_accounting_saft.sql` (exports, imports); the OpenAPI operations; the
1.30 and 1.40 XSDs under `srv/accounting/saft/testdata`.

**Lanes:**

1. **Server:** the streaming writer and its worker; the reader for 1.30 and 1.40; the
   import preview and the write.
2. **UI:** Export page with the grouping warning and the downloads; the import wizard in
   activation.

**Tests:** the export against the XSD and a golden file; export then import on an empty
database gives the same balances; a file from the repository's examples.

**Docs:** `user/accounting.md` (en and nb): the file for an audit, coming from another
system; the reference.

## Layer 8 — delivery F, bank (A26–A28; R5, R11)

**Seam:** `mig/00186_accounting_bank.sql`; the OpenAPI operations; camt.053 fixtures.

**Lanes:**

1. **Server:** bank accounts; the camt.053 and CSV readers with the duplicate guard;
   suggestions; match, code, undo; the balance foot.
2. **UI:** Bank accounts; the import; the reconciliation screen.

**Docs:** `user/accounting.md` (en and nb); the reference.

**The public site:** with this layer, the pull request in `vantigo-io/vantigo-landing`
for `vantigo.io/apps/accounting`, written from the user guide, linked from this PR.

## After the stack

- The bank feed part of F (A29), after the Folio feed's delivery A.
- A browser check of activation, a journal, a supplier invoice, a VAT term and a
  reconciliation.
- Phase 2 starts with the registrations of R §3.4.
