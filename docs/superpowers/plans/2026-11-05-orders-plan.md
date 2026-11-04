# Orders phase 1 — deliveries 0, A, A2, B, S, C, D: implementation plan

This plan builds the Orders module's first phase from two documents:

- **The spec:** `docs/superpowers/specs/2026-11-05-orders-design.md` (revision 2). Its
  decisions are O*n*.
- **The research:** `docs/superpowers/research/2026-11-04-orders-module.md` ("R").

**Abbreviations:**
- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/orders/frontend/src/`.

## How it lands: one stack

The owner asked on 2026-11-05 for **stacked PRs, so the work never waits for a merge**.

- **The tool.** The stack is managed with `gh stack` (github/gh-stack v0.3.0), always
  with its non-interactive flags: `view --json`, `submit --auto`, `add <branch>`.
- **Merging.** The owner merges with `gh stack merge <top PR> --yes`, which queues the
  whole stack bottom-up through the merge queue.
- **One layer per delivery, bottom to top.**
  - Each layer is one PR whose diff is that delivery alone.
  - Each layer's body says what it sits on.

| # | Branch | Delivery | Migrations claimed (main at `0bae1d8c`, latest `00116`) |
| --- | --- | --- | --- |
| 1 | `claude/orders-app-planning-29bde0` | Research, spec, plan (docs only) | — |
| 2 | `claude/orders-0-platform` | 0: outbox, catalog, VAT mapping, `SalesTax`, `docmath`, customer contract | `00117_platform_events`, `00118_invoices_tax_category_vat_codes` |
| 3 | `claude/orders-a-quotes-orders` | A: the module, quotes, orders, PDFs, mail, holders, timeline, customer tab | `00119_orders_baseline` |
| 4 | `claude/orders-a2-selling` | A2: warnings, variant grid, packagings on lines, analysis | `00120_orders_warnings` |
| 5 | `claude/orders-b-invoicing` | B: allocations, `BillableOrders`, the holder, the wizard, the billing run | `00121_orders_allocations` |
| 6 | `claude/orders-s-subscriptions` | S: subscriptions | `00122_orders_subscriptions` |
| 7 | `claude/orders-c-deliveries` | C: `Stock`, `StockLedger`, reservations, deliveries | `00123_inventory_reservations`, `00124_orders_deliveries` |
| 8 | `claude/orders-d-shipping` | D: `srv/shipping`, Bring, shipments, the shipping line | `00125_orders_shipping` |

**Migration numbers.** If `main` takes a claimed number first, the stack renumbers it
inside the owning layer's history and then `gh stack rebase` runs (R:
prs-land-by-rebase).

**The docs layer carries no `Docs-Impact` trailer.** It changes only
`docs/superpowers/**`, which no page's `sources` names, so the coverage check passes
without one. A trailer anywhere in a merge-queue group would waive every layer's
coverage findings.

## How each delivery is run

This repeats Inventory's tested routine (memory: inventory-module-state).

1. **The seam, by hand, first.** The migration, the OpenAPI contract with every
   operation's `summary`, `make generate` / sqlc, the contract types in `srv/contracts`,
   the module skeleton and the registries. One commit, so parallel lanes build on a
   compiling tree.
2. **Lanes in parallel.** Each lane owns disjoint files and is never two agents in one
   Go package:
   - server;
   - the other module's side (Products, Invoices, Inventory, Customers);
   - `@vantigo/orders-ui`;
   - the host.
3. **Docs lane.** The bilingual user and admin pages, the reference, and
   module-boundaries. Then `go generate ./...` in `apps/server`, because the Assistant
   embeds the docs.
4. **Reviews.** An opus code review of server and frontend. Fix what it finds.
5. **Gates:**
   - `mise run server:check`;
   - `mise run server:test`. On SQLSTATE 53300, rerun the failing packages with
     `-p 1`, because the test Postgres is shared;
   - `mise run frontend:check`. Read the log, not the exit line;
   - `mise run docs:check`.
   - A fresh worktree needs `bun install --frozen-lockfile` first.
6. **Commits.**
   - Conventional Commits, signed. Never `--no-gpg-sign`, never filter-branch.
   - Grouped as `feat(<module>)` per module, then `docs(orders)`, then
     `chore(assistant): regenerate the embedded documentation`.
7. **Then:** `gh stack add <next branch>`, and the next delivery starts at once.
   `gh stack submit --auto --open` pushes and opens the PRs. CI is watched, and red is
   fixed in the layer that owns it, followed by `gh stack rebase --upstack` and
   `gh stack push`.

**Force-push.** Updating a lower layer rewrites the branches above it. `gh stack push`
force-pushes those with a lease. If the auto-mode classifier blocks it, the exact
command is handed to the owner.

---

## Layer 1 — the design documents

Commit R, the spec and this plan as `docs(orders): research, design and plan for the
Orders module`. Then `gh stack init` adopts this branch as the bottom.

## Layer 2 — delivery 0, platform and seams

**Seam (by hand):**
- `mig/00117_platform_events.sql`: `platform.events` and `platform.event_deliveries`
  (O1, O3).
- `mig/00118_invoices_tax_category_vat_codes.sql` (O7).
- Contract changes:
  - `contracts/catalog.go`: `Money.Amount string`, `VariantEntry.TaxCategoryID`,
    `Description`, `SearchVariants(ctx, q, VariantSearch)` (O6);
  - `contracts/references.go`: `VariantHold.Kind` and `Documents`, and the purpose
    argument (O30);
  - `contracts/directory.go`: `CustomerAddressEntry.ID`, `DeliveryAddresses`,
    `Contacts` and the options argument (O10);
  - the new `contracts/sales_tax.go` (O8);
  - the new `contracts/events.go`: the `Event` interface (O1).
- `module.Module.EventHandlers` and `Deps.SalesTax` (O2, O8).

**Lanes:**

1. **Platform** (`srv/events`, `srv/module/compose.go`, `srv/management`):
   - `Publish`, the dispatcher worker and its lease and backoff, ordering per
     aggregate, dead and skip, pruning;
   - the management endpoints (O4);
   - Compose's duplicate-handler refusal;
   - the tests listed in spec §12 for 0, including a pool of one.
2. **Products** (`srv/products`):
   - the catalog changes, with `LockVariants` filling the new fields;
   - `ListPrice` returning exact text;
   - the type filter;
   - `VariantHold`'s kind and the purpose;
   - migrate the callers: `srv/projects` (billing lines) and `srv/time/rates.go`
     (rate chain), each to exact decimals;
   - Inventory's `VariantReferenceHolder` adapted to the new signature, as
     `Kind = stock`.
3. **Invoices** (`srv/invoices`, `srv/docmath`):
   - move `computeLine` and `summarize` verbatim to `srv/docmath`, with Invoices
     calling it and its tests unchanged;
   - the mapping table, its settings endpoints and screen in `@vantigo/invoices-ui`;
   - `SalesTax` (`VATCodes`, `ForTaxCategories`, `Seller`). Find where the PDF reads
     the seller (spec Q2).
4. **Customers** (`srv/customers`): the billing profile's delivery addresses with ids,
   the contacts, and the options argument (O10).
5. **Config** (`srv/config`): `orders` requires products, customers and invoices.
   Registered now, so A only adds the module (O9).

**Docs:**
- `contributing/module-boundaries.md`: rule 23, rule 10's amendment, the slot list, and
  the provider-command pattern (O37);
- `reference/products.md`, `reference/invoices.md` and `reference/customers.md`;
- `user/invoices.md` and `admin/invoices.md` in `en` and `nb`, for the VAT mapping;
- the operations page, for the events worker and the management calls;
- ROADMAP.md: the outbox is built, and the "reserves stock" sentence is corrected.

## Layer 3 — delivery A, quotes and orders

**Seam:**
- `mig/00119_orders_baseline.sql` with:
  - `settings`, `counters`, `quotes`, `orders`, `lines`, `templates`,
    `order_changes`, `documents`, `mailings`, `erasures`;
  - `events`, Orders' own audit log in Inventory's shape, if the reference needs it.
- `openapi/orders.yaml`:
  - quotes: CRUD, send, mark-sent, revise, accept, decline, cancel, copy, apply
    template, PDF, pro forma;
  - orders: CRUD, confirm, hold, release, lock, unlock, cancel, cancel-remaining,
    mark-delivered, undo, changes, PDF, send;
  - templates, settings;
  - the customer summary (O31a).
  - Lists take `customerId`, `status`, `salespersonId` and `q`.
- The module skeleton, and every registry in AGENTS.md "Adding a module":
  - `everyModule()`;
  - `APP_ROUTE_PREFIXES` and `appRoutes`, with a `promptVersion` bump;
  - depguard (count the sets, including the hyphenated ones);
  - `modulesEnv`;
  - `openapi.go` `Modules`;
  - `schema_test.go`, `compose_test.go`, `gen-client.ts`;
  - `astro.config.mjs`;
  - `deploy/compose`;
  - the host's `moduleKeys`, `apps.ts`, the layout route, `i18n.ts`, the admin
    permission labels, the dashboard tile and the spotlight;
  - `sessionClients`;
  - README.md and CONTRIBUTING.md.

**Lanes:**

1. **Server, quotes and lines** (`srv/orders`: lines, pricing, `docmath` use,
   templates, quotes, PDFs, mail and the `orders.mail` worker). The rules are
   O12–O25. Every line write calls `LockVariants` first.
2. **Server, orders.** Its files are kept apart from lane 1's, in the same package, so
   **this lane runs after lane 1** (one agent per package). It covers:
   - confirm, hold, lock, cancel, the edit rules, the change log, manual marking;
   - the create command;
   - the holders: customer reference, personal data, variant;
   - `orders.retention` and `orders.quote_expiry`;
   - the event publications;
   - the summary endpoint.
3. **Customers** (`srv/customers`): the handler `customers.timeline_orders` (O31).
4. **`@vantigo/orders-ui`:**
   - the lists, the quote and order editors with the line grid, send dialogs, the
     accept dialog, templates and settings;
   - `CustomerOrdersPanel`;
   - `assistTargets` with its registry test.
5. **Host:**
   - the registrations;
   - `$customerId.orders.tsx`, `-customer-orders-tab.tsx`, the tab entry and
     `activeCustomerDetailTab`, with tests mirroring `customer-invoices-tab.test.tsx`
     (O31a).

**Docs:**
- `user/orders.md` and `admin/orders.md` in `en` and `nb`;
- `reference/orders.md`;
- `reference/customers.md`: the timeline and the tab;
- `user/customers.md`, in both languages: the Orders tab;
- the overview pages that list modules.

## Layer 4 — delivery A2

- `mig/00120_orders_warnings.sql`.
- **Server:**
  - warnings with block and override (O33);
  - the analysis endpoints with CSV (O35);
  - packaging entry on lines (O15).
- **App:**
  - warning banners, warning editors in settings;
  - the variant grid (O34);
  - the analysis pages with one chart each, following the dataviz rules.
- **Docs:** user, admin and reference.

## Layer 5 — delivery B

- `mig/00121_orders_allocations.sql`, plus Invoices' source kind constant in Go.
- **Contracts:**
  - `contracts/billable_orders.go` (O37);
  - `WorkSourceOrderAllocation`, appended to `InvoicedWorkOrder` (O38).
- **Lanes:**
  - **Orders server:** allocations, the provider, the holder, the sweep, *Release draft
    allocations*, the edit and cancel rules against draft allocations.
  - **Invoices server and app:**
    - the *From orders* wizard with partial invoicing and samlefaktura;
    - `ReleaseAllocations` after a draft's delete or a line's removal (spec Q6);
    - the billing run's orders source;
    - checking Projects-off behaviour (Q5).
  - **Orders app:** the *Invoice* button, the order's invoices list, to-invoice figures,
    and the customer panel's third sum.
- **Tests:** the property test on line equality, and concurrent `Allocate`.
- **Docs:**
  - `user/invoices.md`, `admin/invoices.md` and `reference/invoices.md`;
  - Orders' pages;
  - module-boundaries for the new slot.

## Layer 6 — delivery S

- `mig/00122_orders_subscriptions.sql`: `subscriptions`, `subscription_skips`, dated
  lines, and the unique live period index.
- **Server:**
  - the period calendar;
  - pause, resume and end;
  - changes from the next period;
  - the provider listing due periods, and `Allocate` by subscription through a date;
  - `billed_through` derivation;
  - accept-as-subscription.
- **App:** the subscription list and editor, and the panel's subscription list.
- **Invoices:** the wizard and billing-run labels for periods, and the header period.
- **Docs:** the subscription sections.

## Layer 7 — delivery C

- `mig/00123_inventory_reservations.sql`: `reservations`, the `issue` kind, and
  `sites.fulfils_orders`.
- `mig/00124_orders_deliveries.sql`: deliveries, delivery lines, and `site_id` on
  orders.
- **Contracts:** `contracts/stock.go`, with `Stock`, `StockLedger` and `StockRefusal`
  (O45). MB rule 24.
- **Lanes:**
  - **Inventory server:** reservations, `Reserve`, `Release`, `Issue`, the reads; the
    ledger composed whatever `MODULES` says; the product page's *Reserved* and
    *Available*.
  - **Orders server:**
    - reserve at confirm, and *Confirm without reserving*;
    - edits adjusting reservations;
    - deliveries with pick list and packing slip;
    - the *on delivery* policy.
  - **Apps:** Orders' deliveries pages; Inventory's site flag and available figures.
- **Docs:** Inventory's user and reference pages, Orders' pages, and module-boundaries.

## Layer 8 — delivery D, shipping

D waits for R §2.9, the carrier research. The research is running. Its findings revise
O49–O53 before D's seam is cut.

- `mig/00125_orders_shipping.sql`: carrier accounts, shipments, packages, documents
  and events.
- **`srv/shipping`:** the port, `shipping/bring` (from Bring's documented API), and
  `shipping/bring/bringtest`.
  - An opt-in test against Bring's test mode behind an environment variable, as
    `vipps_test_env_test.go` does.
- **Orders server:**
  - shipments: rates, book with an idempotency key, cancel;
  - the label and waybill store;
  - the tracking worker;
  - the shipping line with *Estimate*;
  - the shipping notice through `orders.mail`.
- **Host:** the Integrations item *Frakt / Shipping* at `/workspace/shipping` (O51).
- **Config:** `SHIPPING_CARRIERS` (instance tier).
- **Docs:**
  - `admin/shipping.md` in `en` and `nb`;
  - Orders' pages;
  - a contributing page on the port;
  - `admin/configuration.md`.

## Gotchas carried from earlier deliveries

- Every OpenAPI operation needs a `summary`. The Assistant's mode is derived: reads are
  `auto`, writes are `confirm`, files are `never`.
- House status codes:
  - `400` for field and line codes;
  - `409 version_conflict` for a stale version;
  - `422` only for a carrier refusal.
- A docs edit needs `go generate ./...` in `apps/server`. The docs check also wants
  module-boundaries.md for any `contracts/*.go` change, and `reference/assistant.md`
  when assist targets change.
- The integration test's `filesOrRedirects` must list file operations (PDFs, CSV).
- A whole-tree `go test` can exhaust the shared Postgres, so run packages alone.
- Amending is blocked, so add follow-up commits.
- The pre-commit hook checks the whole tree, so commit only when every lane's files are
  clean.
