# Manufacturing phase 1 — deliveries 0, A, B, C, D, E: implementation plan

This plan builds the first phase of the Manufacturing module. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-11-manufacturing-design.md`, revision 1. Its
  decisions are numbered M*n*.
- **The research:** `docs/superpowers/research/2026-11-11-manufacturing-module.md`, called
  "R" below.

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/manufacturing/frontend/src/`.

## How it lands: one stack

On 2026-11-11 the owner asked for research, a plan, and then **an implementation in
stacked PRs, without waiting for input**. Review happens once the stack is built.

- **The tool:** `gh stack` v0.3.0, used only with its non-interactive flags.
- **Merging:** the owner merges the whole stack with `gh stack merge <top PR> --yes`.
- **Each layer** is one PR. Its diff is that delivery and nothing else, and its body says
  which layer it sits on.

| # | Branch | Delivery | Migrations claimed (main at `430e73f33`, latest `00155`) |
| --- | --- | --- | --- |
| 1 | `claude/manufacturing-app-plan-e75b0f` | Research, spec, plan (docs only) | — |
| 2 | `claude/manufacturing-0-seams` | 0: `Produce`, `ReserveForProduction`, the `production` kind, standard cost | — |
| 3 | `claude/manufacturing-a-boms` | A: the module, work centers, BoMs, the overview, the holder | `00156_manufacturing_baseline` |
| 4 | `claude/manufacturing-b-orders` | B: MOs, reservations, productions, close, backorders | `00157_manufacturing_orders` |
| 5 | `claude/manufacturing-c-shop-floor` | C: work orders, time logs, the shop floor, blocking | `00158_manufacturing_work_orders` |
| 6 | `claude/manufacturing-d-schedule` | D: planning, the board, load, cost, reports | `00159_manufacturing_schedule` |
| 7 | `claude/manufacturing-e-mto` | E: `SalesOrderLines`, `Production`, MTO, child MOs, `orders.mto-reserve` | `00160_manufacturing_mto` |

**Migration numbers.** If `main` takes a number first, the owning layer renumbers its
migration in its own history before the stack is rebased (memory:
migration-number-collisions). Check `git ls-tree origin/main` right before merging.

**The docs layer carries no `Docs-Impact` trailer.** It changes only
`docs/superpowers/**`. A trailer in a merge-queue group would waive coverage for every
layer in it.

## How each delivery is run

This follows the routine used for Orders and Inventory (memories: orders-module-state,
inventory-module-state).

1. **The seam first, by one agent.** Write the migration, the OpenAPI operations (each
   with a `summary`), `go generate`/sqlc, the contract types, and the skeleton and
   registries. It lands as one compiling commit.
2. **Lanes.** Each lane owns disjoint files, and no two agents work in one Go package at
   once. The lanes are:
   - server;
   - the other module's side;
   - `@vantigo/manufacturing-ui`;
   - the host.
3. **Docs.** Write the bilingual user and admin pages and the reference, then run
   `go generate ./...` in `apps/server`.
4. **Review.** An opus code review covers server and frontend. Fix what it finds.
5. **Gates.** Run, in order:
   - `bun install --frozen-lockfile` once in the worktree;
   - `mise run server:check`;
   - `mise run server:test`. On SQLSTATE 53300, rerun with `-p 1`;
   - `mise run frontend:check`. Read the log, not the exit line;
   - `mise run docs:check`.
6. **Commits.** Signed Conventional Commits:
   - `feat(inventory)` and `feat(products)` for the seams;
   - `feat(manufacturing)`, `feat(manufacturing-ui)` and `feat(host)`;
   - `docs(manufacturing)`;
   - `chore(assistant): regenerate the embedded documentation`.
7. **Next layer.** Cut the next branch from this one with `gh stack add <branch>`. Then
   `gh stack submit --auto` pushes and opens the PRs.

---

## Layer 1 — the design documents

R, the spec and this plan, committed as `docs(manufacturing): research, design and plan for
the Manufacturing module`. Then `gh stack init` adopts this branch as the bottom layer.

## Layer 2 — delivery 0, the seams (M1–M5)

**Contracts** (`srv/contracts/stock.go`, `catalog.go`):

- Add `StockLedger.Produce`, together with `StockProduction`, `ConsumeLine` and
  `ProduceLine`.
- Add `StockLedger.ReserveForProduction`.
- Add `VariantEntry.StandardCost`.
- Update every fake that implements these: `srv/orders` tests and `modtest`.

**Inventory** (`srv/inventory`):

- Add `movementKindProduction` and its OpenAPI enum value.
- Write `Produce` on the posting engine: consume lines are from-only, produce lines are
  to-only, reservations are consumed as in `Issue`, and the call is idempotent by the
  document source.
- Write `ReserveForProduction`, sharing `Reserve`'s body with the site check switched.
- On the frontend, add the kind label and the movement page's source link to
  `/manufacturing/orders/{id}` (M5).

**Products:** fill `StandardCost` in `Variant`, `Variants` and `LockVariants`.

**Tests:** see spec §12, Ledger.

**Docs:**

- `reference/inventory.md`: the production kind, `Produce`, the production reserve, and
  the source link;
- `reference/products.md`: the contract field;
- `user/inventory.md` in en and nb: the Production kind in the movements list.

## Layer 3 — delivery A, the module (M6–M16)

**Seam:**

- `mig/00156_manufacturing_baseline.sql`, with counters, work centers, closures, BoMs,
  operations and lines.
- `openapi/manufacturing.yaml`, covering:
  - work centers: CRUD, archive, closures;
  - BoMs: CRUD with lines and operations in one body, archive, make default, overview;
  - meta, which returns the caller's capabilities.
- `srv/openapi/gen/cfg-manufacturing.yaml` and the generate lines.
- The module skeleton, with permissions (§8) and the config requirement on products and
  inventory.
- Every registry AGENTS.md lists. Count the depguard sets by name, including the
  hyphenated ones.

**Lanes:**

1. **Server:** work centers with patterns and closures; BoMs with replace-in-one-request,
   the default swap, the cycle and depth walk, and the overview; the
   `VariantReferenceHolder`.
2. **UI** (`@vantigo/manufacturing-ui`):
   - the work centers list and form, with the weekly-pattern editor;
   - the BoMs list and editor, with lines and operations and client keys;
   - the BoM overview tree;
   - the Overview page shell.
3. **Host:** the routes, `apps.ts`, `i18n`, permission labels, the dashboard card, the
   spotlight and `sessionClients`.

**Docs:**

- `user/manufacturing.md` (en and nb) for work centers and BoMs;
- `admin/manufacturing.md` (en and nb);
- `reference/manufacturing.md`;
- the overview listings in each section;
- `ROADMAP.md`;
- the `deploy/compose` `MODULES` and `GRANT` lines.

## Layer 4 — delivery B, manufacturing orders (M17–M26)

**Seam:**

- `mig/00157_manufacturing_orders.sql`;
- the OpenAPI operations for MOs: list, create, get, update, confirm, reserve now, add
  line and edit line, productions, close, cancel, and events;
- the live topics.

**Lanes:**

1. **Server:**
   - the snapshot at confirm;
   - reservations through `ReserveForProduction`;
   - availability through `Stock.Available`;
   - productions through `Produce`, with the proportional prefill endpoint
     (`GET …/production-draft?quantity=`) and the deviation warning;
   - close with the backorder, and cancel;
   - the timeline;
   - live notifications.
2. **UI:**
   - the MO list with its filters and the live list;
   - the new-MO form;
   - the order page, with components, availability, the production dialog, the close
     dialog and the history.

**Docs:** add the MO tasks to the user guide and the MO rules to the reference.

## Layer 5 — delivery C, work orders and the shop floor (M27–M33)

**Seam:**

- `mig/00158_manufacturing_work_orders.sql`;
- the OpenAPI operations:
  - work orders: list per work center, get, start, pause, finish (with an optional
    production), scrap;
  - time logs: list, correct, add;
  - block and unblock;
  - my work;
- the live topics, with presence.

The snapshot at confirm, built in B, now also creates the work orders.

**Lanes:**

1. **Server:**
   - the state machine;
   - one open log per user;
   - block converting logs;
   - finish on the last work order posting a production in the same transaction;
   - corrections.
2. **UI:**
   - the shop floor, with its work center picker, queue, work order view, timers,
     presence, and the scrap and register dialogs, laid out for a tablet;
   - my work;
   - the MO page's work orders tab with time logs.

**Docs:** the shop-floor tasks, written for operators; the admin page's section on
tablets.

## Layer 6 — delivery D, the schedule and cost (M34–M42)

**Seam:**

- `mig/00159_manufacturing_schedule.sql`, which adds the slots, and on orders the
  `requested_start` column and the cost columns if B did not add them;
- the OpenAPI operations: plan, plan all, unplan, reschedule, the board read, the load
  read, and the reports with CSV.

**Lanes:**

1. **Server:**
   - the calendar engine: patterns, holidays, closures, the workspace zone and DST;
   - first-free-slot planning with per-center advisory locks;
   - rescheduling;
   - estimate and actual, with the union of logs;
   - the reports;
   - shaping out cost fields without `costs-view`.
2. **UI:**
   - the board, with day columns, bars, load, drag and the unscheduled list;
   - the cost panel on the MO page;
   - the reports pages.

**Docs:** the schedule and cost tasks; the planning algorithm in the reference.

## Layer 7 — delivery E, make-to-order (M43–M48)

**Seam:**

- `mig/00160_manufacturing_mto.sql`;
- `contracts.SalesOrderLines`, provided by Orders;
- `contracts.Production`, provided by Manufacturing;
- the event `manufacturing.order.done`;
- the `Deps` slots for both contracts.

**Lanes:**

1. **Server, Manufacturing:**
   - the `manufacturing.mto` handler;
   - the cancelled-origin flag;
   - creating child MOs;
   - publishing `order.done`;
   - providing `Production`.
2. **Server, Orders:**
   - providing `SalesOrderLines`;
   - the `orders.mto-reserve` handler;
   - production status in the order response, read through `contracts.Production` and
     gated on the caller holding `manufacturing:access`.
3. **UI:**
   - the Orders line actions and status, in `@vantigo/orders-ui`;
   - the sub-assembly dialog and the parent/child links in Manufacturing.

**Docs:**

- the MTO tasks in `user/manufacturing.md` and `user/orders.md` (en and nb);
- `reference/orders.md`;
- `module-boundaries.md`, for the two contracts.

## Gotchas carried from earlier deliveries

- New modules are missed in `everyModule()`, `APP_ROUTE_PREFIXES`, `appRoutes` with its
  `promptVersion` bump, and the hyphenated depguard sets.
- Run `go generate ./...` after any edit under docs.
- Commits must be signed. Never use filter-branch. A force-push the classifier blocks is
  handed to the owner.
- The pre-commit hook checks the whole tree, so commit when the lanes are quiet.
- Leased workers and event handlers must not hold one pooled connection while waiting on
  another. Use a pool of one in tests.
