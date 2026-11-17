# Purchasing phase 1 — deliveries 0, A, B, C, D: implementation plan

This plan builds the first phase of the Purchasing module. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-purchasing-design.md`, revision 2. Its
  decisions are numbered P*n*, and its §15 (A*n*) amends them and wins.
- **The research:** `docs/superpowers/research/2026-11-17-purchasing-module.md`, called
  "R" below.

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/purchasing/frontend/src/`.

## How it lands: one stack

The owner answered the spec's first question on 2026-11-17 — the books are kept in
Vantigo — and the other six stand at their defaults. Building starts when the owner says
so; nothing in this plan is started by the plan.

- **The tool:** `gh stack`, used only with its non-interactive flags.
- **Merging:** one layer lands per CI round through the merge queue; the next is
  retargeted to `main` by hand, since the repository does not delete branches.
- **Each layer** is one PR. Its diff is that delivery and nothing else, and its body says
  which layer it sits on.

| # | Branch | Delivery | Migrations claimed (main at `64829c4e2`, latest `00188`) |
| --- | --- | --- | --- |
| 1 | `claude/next-feature-planning-c320f7` | Research, spec, plan (docs only) | — |
| 2 | `claude/purchasing-0-seams` | 0: the supplier directory, `Receive`, open purchases, Accounting's events | — |
| 3 | `claude/purchasing-a-orders` | A: the module, supplier items, purchase orders | `00189_purchasing_baseline` |
| 4 | `claude/purchasing-b-receipts` | B: receipts, the remainder, *incoming* | `00190_purchasing_receipts` |
| 5 | `claude/purchasing-c-proposals` | C: proposals | — |
| 6 | `claude/purchasing-d-match` | D: the invoice match | `00191_purchasing_matches` |

Delivery 0 takes no migration: `inventory.movements.kind` has no check constraint, and
`receipt_reversal` fills its `varchar(16)` exactly. Every number is rechecked against
`main` right before each merge.

**Each delivery carries its own demo seed** (the owner, 2026-11-17). A, B, C and D each
add what they make to the demo seed and its tests in their own layer, get their PR and
green CI, and are merged; there is no separate seed layer at the end. Delivery 0 seeds
nothing of Purchasing's — there is no Purchasing yet.

The spec's §9 names one baseline migration. The stack takes one per delivery instead, so
each layer's diff carries its own tables.

**Migration numbers.** If `main` takes a number first, the owning layer renumbers its
migration in its own history before the stack is rebased (memory:
migration-number-collisions). Check `git ls-tree origin/main` right before merging.

**The docs layer carries no `Docs-Impact` trailer.** It changes only
`docs/superpowers/**` and `ROADMAP.md`.

**C and D are independent.** They are stacked in this order only because a stack is a
line; either can be moved under the other.

## How each delivery is run

The routine used for Orders, Inventory, Manufacturing and Accounting.

1. **The seam first, by one agent.** The migration, the OpenAPI operations (each with a
   `summary` and its assistant mode), `go generate`/sqlc, the contract types, the
   skeleton and the registries. One compiling commit.
2. **Lanes.** Each lane owns disjoint files, and no two agents work in one Go package at
   once: server; the other module's side; `@vantigo/purchasing-ui`; the host.
3. **Docs.** The bilingual user pages and the reference, then `go generate ./...` in
   `apps/server`.
4. **Review.** A code review of server and frontend in a separate pass. Fix what it finds
   in the layer it belongs to.
5. **Gates,** in order:
   - `bun install --frozen-lockfile` once in the worktree;
   - `mise run server:check`;
   - `mise run server:test`. On SQLSTATE 53300, rerun the failing packages with `-p 1`;
   - `mise run frontend:check`. Read the log, not the exit line;
   - `mise run docs:check`, and per layer
     `bun run tools/docs/check-coverage.ts --base <parent branch>`.
6. **Commits.** Signed Conventional Commits: `feat(accounting)`, `feat(inventory)`,
   `feat(manufacturing)` for the seams; `feat(purchasing)`, `feat(purchasing-ui)`,
   `feat(host)`; `docs(purchasing)`;
   `chore(assistant): regenerate the embedded documentation`.
7. **Next layer.** `gh stack add <branch>`, then `gh stack submit --auto`.

Workers are tested as `module.Workers` builds them, not through the harness's deps.

---

## Layer 1 — the design documents

R, the spec, this plan and the roadmap's Purchasing section, committed as
`docs(purchasing): research, design and plan for the Purchasing module`.

## Layer 2 — delivery 0, the seams (P1–P7; A2, A3, A5–A8, A12, A13)

**Contracts** (`srv/contracts/`):

- `supplier_directory.go`: `SupplierDirectory` — `Supplier`, `Suppliers`,
  `SearchSuppliers`, `LockSuppliers(tx, ids)` — and `SupplierEntry` (P1, A8).
- `supplier_holders.go`: the holder slot `SupplierReferences` and the many-provider
  `SupplierPersonalData` with its export and erase halves (P10, A8).
- `supplier_invoices.go`: `SupplierInvoices` — `Invoice`, with each line's net amount,
  and `CreateDraft` — and the optional `SupplierInvoiceMatches` (P6, A1, A9).
- `purchase_supply.go`: `PurchaseSupply` — `OpenByVariants` with an optional site, and
  `OpenAll` (P4, A3).
- `purchase_demand.go`: `PurchaseDemand`, with the unrounded need and the computed time
  (P31, A4). Declared here so C touches no contract.
- `events_accounting.go`: `accounting.supplier_invoice.posted.v1` and `…reversed.v1`
  (P5).
- `stock.go`: `StockLedger.Receive` and `ReverseReceipt`, their refusal codes in
  `StockRefusal` (P3, A5, A6).

**Accounting** (`srv/accounting`):

- The supplier operations leave `setupGate`; `supplier()` and the list tolerate no
  settings row; default account and tax code refused before the setup (P2, A2).
- The SAF-T import plan matches the file's suppliers to existing ones, by organisation
  number then exact name, with the pick in the preview as customers have (A2).
- `accounting:suppliers-manage`, and the supplier `PUT` split by which fields each key
  may write (A12). The admin permission label, in both languages.
- The delete and the anonymisation lock the supplier `FOR UPDATE`, then ask the holders;
  the export asks `SupplierPersonalData` (A8).
- The two events published on the posting's and the reversal's transaction (P5).
- `CreateDraft` through the draft's own write path, so `checkDraft` judges it (A1).
- The badge on the supplier-invoice list and page from `SupplierInvoiceMatches`, absent
  with no provider (P6).

**Inventory** (`srv/inventory`):

- No migration: `movements.kind` has no check, and `receipt_reversal` fits its
  `varchar(16)` exactly (spec A27).
- `Receive` and `ReverseReceipt` in `ledger.go`, through the engine `postMovement`
  uses, on the caller's transaction, idempotent by source; a Discontinued variant
  received (A6); the reversal taking the receipt's own lots whatever their holds and
  releasing the holds it set (A5).
- The movement page links a receipt with a source to its document; the kind
  **Receipt reversal** / **Reversert varemottak** in the list, the filter and the page.
- `OpenAll` read before the low-stock statement and `LowStockCounts`, its rows joined
  in as arrays; *in transit* and *on order* as two figures on the list, the CSV and the
  stock card. With no provider, as today (A3).

**Manufacturing** (`srv/manufacturing`): the projection adds `PurchaseSupply` to
*incoming*; `Stock.Placements` is left alone (A3).

**Module boundaries:** rule 24 widened to *receives*; three new rules from the next free
number (A8); the new slots under rule 5.

**Tests:** spec §12, delivery 0, and the SAF-T import with a supplier made first. A fake
`PurchaseSupply` in `modtest` for Inventory's and Manufacturing's packages.

**Docs:** `reference/accounting.md`, `user/accounting.md` (en, nb) for the register
before the books and the new key; `reference/inventory.md`, `user/inventory.md` (en, nb)
for the reversal kind and the two figures; `reference/manufacturing.md`;
`contributing/module-boundaries.md`; `contributing/platform-packages.md`.

## Layer 3 — delivery A, the module (P8–P23; A11, A16–A19)

**The module, every registry** — the checklist *Adding a module* step by step, and
AGENTS.md's list of the ones that have been missed:

- `businessModules`, `openapi.Modules`, `cfg-purchasing.yaml`, the `//go:generate` lines;
- every depguard set in `.golangci.yml`, the hyphenated ones too;
- `moduleSchemas`, the config's known set, the dependency check (accounting, inventory,
  products), `compose_test.go`, `modulesEnv` cascading to accounting and invoices (A18);
- `everyModule()` in `srv/integration/assistant_test.go`;
- `APP_ROUTE_PREFIXES` in `apps/assistant/frontend/src/lib/links.ts`, `appRoutes` in
  `srv/assistant/prompt.go`, `promptVersion` bumped;
- the host: `moduleKeys`, `apps.ts`, the layout route, `i18n.ts`, the admin permission
  labels, the dashboard, the spotlight, `sessionClients`, `gen-client.ts`;
- `docs/astro.config.mjs`, the deployment lists and the `GRANT` lines.

**Server** (`srv/purchasing`), on Orders' files as the model:

- `mig/00189`: `settings`, `counters`, `supplier_items`, `orders`, `order_lines`,
  `order_changes`, `documents`, `mailings`, `import_jobs`.
- `settings.go`, `supplier_items.go` and its import through `internal/fileimport`
  (P9, P11–P14).
- `orders.go`, `lines.go`, `lifecycle.go` — send, mark as sent, send again, close line,
  close, reopen, cancel with its notice — `confirmation.go`, `copy.go` (P15–P22, A16).
- `pdf.go`, `pdfdocs.go`, `ordersend.go`, `mail_worker.go`: the PDF with
  `SellerProfile`, stored after the commit; a person's send synchronous, the queue for
  retries (P21, A19).
- `holders.go`: the variant holder with its three purposes, the supplier holder, the
  personal-data provider with its deletion ledger and worker (P10, A8, A17).
- `events.go`: `purchasing.order.sent.v1`, `…closed.v1`.
- `clientRequestId` on *Send* (A11); `LockSuppliers` on create and send (A8).

**Frontend** (`fe/`): the order list and page, the editor, **Suppliers** with the
register's entry and the supplier items, **Settings**; `assistTargets` for *New purchase
order*.

**Host:** routes, the sidebar, the dashboard card, the spotlight, and the product page's
**Suppliers** card.

**Docs:** `user/purchasing.md` (en, nb) and `reference/purchasing.md`, new, listed in
their overviews; `user/products.md` (en, nb) for the card; `user/assistant.md` if the
route list there names apps.

**ROADMAP.md:** Products phase 5 keeps the unit conversions and loses the supplier
records; the bank feed's line names Accounting's phase 2 for KID payments (R §9.2, §9.3).

## Layer 4 — delivery B, receipts (P24–P30; A5–A7, A10, A11, A21)

- `mig/00190`: `receipts`, `receipt_lines`, the receipt series.
- `receive.go`: the pool read, `LockVariants`, the order lock and recheck, `Receive`,
  the whole transaction retried three times (A7); `acceptOver`; `clientRequestId`.
- `undo.go`: `ReverseReceipt`, `receipt_invoiced` judged against invoiced quantities —
  nothing is invoiced before D, and the check is written now (A10).
- `supply.go`: the `PurchaseSupply` provider, from sent orders' open lines.
- The receive screen: no prices, a scanned variant or packaging code, a GS1 code's lot
  and expiry. Find where Inventory parses GS1 (`srv/inventory/gs1.go`, and its
  frontend's reader) and move the part Purchasing needs to a shared place (A21).
- Events `purchasing.receipt.posted.v1` and `…undone.v1`; Manufacturing's handler
  requesting a run for the site on a sent order and a posted receipt (A4).
- `assistTargets` for *Receive*.

**Docs:** the receiving sections of `user/purchasing.md` (en, nb) and the reference;
`user/inventory.md` (en, nb) and `reference/inventory.md` for *on order*;
`user/manufacturing.md` (en, nb) and `reference/manufacturing.md` for *incoming*.

**The public site**, opened alongside this layer, in `vantigo-landing`:
`web/src/apps/purchasing.ts` in English and Norwegian from the two user guide pages, the
two catalogs, `mise run apps:check`; a line each on the Inventory and Manufacturing
pages. The two pull requests link each other.

## Layer 5 — delivery C, proposals (P31–P35; A4, A11)

- **Inventory:** the `PurchaseDemand` provider — the low-stock rows with the need before
  packaging rounding — composed only where Manufacturing is not.
- **Manufacturing:** the provider — its stored *buy* rows with the run's time.
- **Purchasing:** `proposals.go` — one source by precedence, less its own drafts and
  what was sent after the computed time, rounded once to the supplier item;
  **Create orders** with `clientRequestId`, one draft per supplier and site.
- **Host:** **Order…** on the low-stock row and on a buy suggestion.

**Docs:** the proposals section of `user/purchasing.md` (en, nb) and the reference;
`user/inventory.md` and `user/manufacturing.md` (en, nb) for **Order…**.

## Layer 6 — delivery D, the match (P36–P41; A1, A9–A11, A14, A15, A20)

- `mig/00191`: `invoice_matches`, keyed by the supplier invoice's line.
- `match.go`: the suggestion by reference; allocate, *Match all received*, unmatch;
  `credit_exceeds_invoiced`.
- `register_invoice.go`: the dialog's fields, `CreateDraft`, 200 lines at a time,
  `supplier_account_missing`, `clientRequestId`; needs `accounting:post` too (A1).
- `handlers.go`: posted fixes a match, reversed releases it, a copy takes its original's
  matches as provisional; each idempotent by the event's id (A15).
- The differences, from the posted line's net over the matched quantity; the last price
  with its two guards (A9).
- `summary.go`: the `SupplierInvoiceMatches` provider.
- Received not invoiced, as a list and a CSV, as of a day (P41).
- **Host:** the **Purchase order** panel on Accounting's supplier-invoice page; an
  invoice's lines shown only to a holder of `accounting:read` (A20).

**Docs:** the invoice sections of `user/purchasing.md` (en, nb) and the reference;
`user/accounting.md` (en, nb) and `reference/accounting.md` for the panel, the badge and
the draft made from an order. The landing page gains the match.

## The demo seed, in every delivery

There is no seed layer. Each of A to D puts its own part in the demo seed, with its
tests, in its own layer, through the module's operations as Go calls, after Inventory's
and before the books', and says so in `docs(seed)`: A the supplier items and a handful
of orders in each state, B receipts, C a proposal's source, D one matched invoice.

## After the stack

- **A browser check** of the whole loop: low stock → proposal → order sent → received →
  invoice registered and posted.
- **Accounting's phase 2** is next: inbound EHF landing as a draft with the order
  reference in `reference`, then payment files. It needs receiving on the access-point
  port, and its own research.
- **The owner's seven remaining questions** (spec §14, 2 to 8), any of which may still be
  overturned before the layer it touches is built.
