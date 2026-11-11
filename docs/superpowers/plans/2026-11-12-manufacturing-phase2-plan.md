# Manufacturing phase 2 — L1a, L1b, L2, O, M, T, Q, R, F, G, S (and K): implementation plan

This plan builds phase 2 of Manufacturing. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-12-manufacturing-phase2-design.md`,
  revision 2 (after a critic review; its §23 lists the changes). Its decisions are
  M49–M146, with M65a, M65b and M107a.
- **The research:** `docs/superpowers/research/2026-11-12-manufacturing-phase2.md` ("R").
- **Phase 1:** the spec and plan of 2026-11-11, whose routine this plan repeats.

**Abbreviations:** `srv/` is `apps/server/internal/`; `mig/` is `srv/db/migrations/`;
`inv/`, `mfg/`, `ord/`, `prd/`, `host/` are `apps/<module>/frontend/src/`.

## How it lands: one stack

On 2026-11-12 the owner asked for phase 2 **as stacked PRs, without waiting for input**.
Review happens once the stack is built.

- **The base.** The stack sits on phase 1's top layer (`claude/manufacturing-e-mto` and
  its review fixes, here at `c3b9a5a7b`), not on `main` (`949cef025`, latest migration
  `00155`): phase 2 needs phase 1's schema. When phase 1 merges, the bottom layer is
  retargeted to `main` (memory: stacked-prs-per-phase — the repo does not delete merged
  branches, so the retarget is ours).
- **The tool:** `gh stack`, non-interactive flags only. The owner merges with `gh stack
  merge <top PR> --yes`.
- **Each layer** is one PR whose diff is that delivery and nothing else; its body names
  the layer under it.

| # | Branch | Delivery | Migrations claimed (latest on the base `00161`) |
| --- | --- | --- | --- |
| 1 | `claude/manufacturing-p2-plan` | Research, spec, plan (docs only) | — |
| 2 | `claude/manufacturing-p2-l1a` | L1a: tracking mode behind a guard, lots and holds, the lot-aware ledger and engine, usable on hand, expiry with `Today`, FEFO and `AutoLot`, the contracts; receipts, moves, adjustments with lots in the API; callers pass no lot (M49–M53, M55–M59, M62–M65b, M67, M68) | `00162_products_variant_tracking`, `00163_inventory_lots` |
| 3 | `claude/manufacturing-p2-l1b` | L1b: lots list and page, holds endpoints, attachments and their deletion worker, the forms' lot column, stock per lot, the host's stock panel, lookup, Inventory's live topics (M54, M59's forms, M60, M61, M63's screens, M64's endpoints, M66, M68) | `00164_inventory_lot_attachments` |
| 4 | `claude/manufacturing-p2-l2` | L2: counts, transfers, import with lots; expiring list; lot labels (M69–M74) | `00165_inventory_lot_counts_transfers` |
| 5 | `claude/manufacturing-p2-o` | O: delivery picks per lot, FEFO, re-proposal, the PDFs (M75–M79) | `00166_orders_delivery_lots` |
| 6 | `claude/manufacturing-p2-m` | M: output lots, serials, consumed lots; the tracking guard removed and the select shown (M80–M86, M49) | `00167_manufacturing_lots` |
| 7 | `claude/manufacturing-p2-t` | T: trace, export, block, `DeliveryTrace`, GS1-128 labels (M87–M94) | — |
| 8 | `claude/manufacturing-p2-q` | Q: checks, results, deviations, holds, inspection on receipt (M95–M104) | `00168_manufacturing_quality`, `00169_inventory_variant_rules` |
| 9 | `claude/manufacturing-p2-r` | R: placements' multiple, `Stock.Placements`, `SalesOrderLines.Unreserved`, projected, the job, suggestions (M105–M111) | `00170_inventory_placement_multiple`, `00171_manufacturing_replenishment` |
| 10 | `claude/manufacturing-p2-f` | F: dependencies, send-ahead, work center groups (M112–M119) | `00172_manufacturing_routing` |
| 11 | `claude/manufacturing-p2-g` | G: by-products, unbuild, split and merge (M120–M128) | `00173_manufacturing_bom_depth` |
| 12 | `claude/manufacturing-p2-s` | S: stations, PIN and badge, the station scope, the live station path (M129–M143) | `00174_identity_stations`, `00175_manufacturing_stations` |
| 13 | `claude/manufacturing-p2-k` | K (optional): kits as child order lines (M144–M146) | `00176_manufacturing_kits`, `00177_orders_kits` |

**One schema per migration.** Every migration changes one module's schema; a layer that
changes two schemas claims two numbers.

**The interim states.** Each layer is merged on its own. Layer **L1a ships a
server-side guard** in Products' variant write that refuses `tracking` other than `none`
(`tracking_unavailable`, 409), and no *Tracking* select, so no tracked variant exists
while deliveries and productions cannot carry a lot; the ledger's tests create tracked
variants through the store. **Layer M removes the guard** and its test, and adds the
select (spec §1, M49).

**Migration numbers.** Each layer claims its numbers at its cut, in this order. If `main`
(or phase 1's last fixes) takes a number first, the owning layer renumbers its migration
**in its own history** before the stack is rebased (memories: migration-number-collisions,
prs-land-by-rebase). Check `git ls-tree origin/main apps/server/internal/db/migrations/`
right before merging.

**The docs layer carries no `Docs-Impact` trailer.** It changes only `docs/superpowers/**`;
a trailer in a merge-queue group would waive coverage for every layer in it.

## How each delivery is run

The routine of phase 1 (and of Orders and Inventory before it), with its lessons.

1. **One executor per layer, doing the work itself.** The executor writes the code, the
   frontend and the docs; it does not delegate implementation to sub-agents. Read-only
   exploration is allowed.
2. **The seam first**, as one compiling commit: the migration(s), the OpenAPI operations
   (each with a `summary`; writes `x-vantigo-assistant: confirm`, shop-floor actions
   `never`), `go generate` and sqlc, the contract types and every fake that implements
   them, the skeleton handlers.
3. **Server, then the other module's side, then the UI, then the host**, each owning its
   files; no two lanes in one Go package at once.
4. **Workers in tests are built through `module.Workers`**, never by hand, so they get the
   same `Deps` as production (the phase 1 E fix *workers get the product catalog*).
5. **Docs**, per the spec's §18, English first, then Norwegian bokmål page for page; a
   layer that adds navigation or a spotlight action also changes `user/index.md` in both
   languages. Then `go generate ./...` in `apps/server` (memory: docs-edit-needs-go-generate)
   and commit the regenerated embedded docs as `chore(assistant): regenerate the embedded
   documentation`.
6. **Docs coverage against the parent.** CI runs the coverage check against the PR's
   base, which in a stack is the **layer under it**, not `main`. Check locally with
   `bun run tools/docs/check-coverage.ts --base <parent branch>` before pushing.
7. **Review.** An opus code review of the layer's diff (server and frontend); fix what it
   finds in `fix(…)` commits and record the fixes in a `docs(manufacturing): the review
   of …` commit when the docs change. **S also gets an opus security review**, before its
   PR is opened.
8. **Gates**, in order:
   - `bun install --frozen-lockfile` once in the worktree (memory: worktree-needs-bun-install);
   - `mise run server:check`;
   - `mise run server:test`; on SQLSTATE 53300 rerun the failing packages with `-p 1`
     (memory: shared-test-postgres-too-many-clients);
   - `mise run frontend:check` — read the log, its wrapper exits 0 anyway;
   - `mise run docs:check`.
9. **Commits.** Signed Conventional Commits, never `--no-gpg-sign` (memory:
   commits-must-be-signed): `feat(products)`, `feat(inventory)`, `feat(orders)`,
   `feat(manufacturing)`, `feat(identity)`, `feat(<module>-ui)`, `feat(host)`,
   `docs(<module>)`, `chore(assistant)`. A force-push the classifier blocks is handed to
   the owner.
10. **Next layer.** `gh stack add <next branch>` from this one; `gh stack submit --auto`
    pushes and opens the PRs. Before merging, rebase onto the latest base and let CI run
    again (AGENTS.md: registries touched by another PR).

---

## Layer 1 — the design documents

R, the spec and this plan, committed as `docs(manufacturing): research, design and plan
for phase 2`. Then `gh stack init` adopts this branch as the stack's bottom layer, on
phase 1's top.

## Layer 2 — L1a, the lot-aware ledger (M49–M53, M55–M59, M62–M65b, M67, M68)

**Seam:**

- `mig/00162_products_variant_tracking.sql` (M49);
- `mig/00163_inventory_lots.sql`: `lots` (with the derived `status`), `lot_holds`, the
  balance key swap (drop the primary key, create `ux_balances_key … NULLS NOT DISTINCT`),
  `movement_lines.lot_id` and `expired_override`, the `lot` counter (M52, M55, M62, M64);
- `openapi/products.yaml`: `tracking`, `expiryDays`, `bestBeforeDays` on the variant;
  `tracking_held`, `tracking_requires_goods`, `tracking_unavailable`;
- `openapi/inventory.yaml`: lot fields on receipt, move and adjustment lines, `lot` on
  movement lines, the codes, `canOverrideExpired` on `meta`;
- `srv/contracts`: `VariantEntry` fields (M50), `VariantHoldTrackingChange` (M51),
  `Stock.Lots`, `StockLot`; `Today`, `ExpiredOverride`, `Unbuild` on `StockIssue` and
  `StockProduction`; `LotID`, `AutoLot`, `AllowExpired` on `IssueLine` and `ConsumeLine`;
  `LotID` on `ProduceLine`; `Issue` and `Produce` answering `StockPosted`; `today` on
  `Reserve` and `ReserveForProduction`; `StockLedger.EnsureLots`, `HoldLots`,
  `ReleaseLotHolds`; `NewLot`, `LotHold`, `LotActor`; `Index`, `LotID`, `LocationID` on
  `StockLineRefusal`; the codes; `ProposeLots` (M65, M67);
- every fake: `srv/manufacturing/fakes_ledger_test.go`, Orders' ledger and stock fakes,
  `srv/modtest`.

**Server — Products** (`srv/products`):

- validate and store the fields; `tracking_requires_goods`; the type change to Service
  resetting tracking;
- **the guard**: the variant write refuses `tracking` ≠ `none` with 409
  `tracking_unavailable`, in one function with a comment naming layer M as its remover;
- the variant `PUT` (`variants.go`:175) asking `variantHeld` with the new purpose when the
  tracking changes, answering 409 `tracking_held` with `holdText`;
- fill the fields in every catalog read and `LockVariants`.

**Server — Inventory** (`srv/inventory`):

- `posting.go`: `balanceKey.lotID`, `engineLine.lotID/newLot`, `balanceKeys`, `keyColumns`,
  `lockBalances`, `writeLines`, `writeBalances`; the lock order of M58 (the counter, new
  lots, named lots with `FOR NO KEY UPDATE` for a serial increase, then the header, the
  locations, the balances `NULLS FIRST`) and its rewritten comment; the M56 table; the
  serial sum under the lot lock (M53);
- `queries/ledger.sql`: the lot arrays and `ON CONFLICT (variant_id, location_id,
  lot_id)`; `LockSiteBalances` ordered lot `NULLS FIRST`; `SiteStockOf` summing **usable**
  on hand (M63); new `queries/lots.sql` and `queries/lot_holds.sql`;
- receipts, moves, adjustments with lots, new lots, generated numbers, serial expansion,
  dates compared only when stated (M57, M59); each handler reading `Today` and the
  override on the pool before its transaction;
- expiry against the passed `Today` and `ExpiredOverride` — no contract call inside any
  ledger call (M62);
- holds: `HoldLots`, `ReleaseLotHolds`, the derived status rewritten under the lot's
  `FOR NO KEY UPDATE`, the events (M64);
- `AutoLot` picks under the locks and `StockPosted.Picks` (M65a);
- `Stock.Available` usable less reserved; the low-stock list on the same figure (M63);
- `ledger.go`: `Issue` and `Produce` carry the lot to the engine; `Unbuild` posts kind
  `unbuild` and allows held consume lots (used from G); `EnsureLots`; `Stock.Lots`;
- the holder's doc comment naming the third purpose;
- the 10,000-serial reserve test and, if it is slow, the (variant, site) advisory lock
  (M65b); record the outcome in `reference/inventory.md`;
- the permission `inventory:expired-override`.

**Other modules' side:** Orders' `PostDelivery` (`srv/orders/deliveries.go`:794) and
Manufacturing's `postProduction` (`srv/manufacturing/productions.go`:501, :594) pass
`LotID: 0`, read `Today` and `ExpiredOverride` on the pool before their transactions, and
take `StockPosted.MovementID`; every reserve call (Orders' `reserveLines`, Manufacturing's
two-pass reserve) passes `today`. Their tests keep untracked fixtures; a test proves that
no ledger call made inside their transactions makes a contract call.

**UI:** none for lots. `prd/`: the variant form shows no *Tracking* select yet (the guard
stands); the two offsets may be shown, since they are not held.

**Host:** the permission label for `inventory:expired-override`.

**Docs:** §18's L1a row, in en and nb where bilingual (`user/inventory.md`: available now
leaves out held and expired lots).

**Tests:** spec §20: Ledger, Rule 24, Holds, Usable on hand, `AutoLot`, Serial reserve
load, Products (the guard), Contracts.

## Layer 3 — L1b, the lot screens (M54, M59's forms, M60, M61, M63's screens, M64's endpoints, M66)

**Seam:** `mig/00164_inventory_lot_attachments.sql` (`lot_attachments`,
`object_deletions`); `openapi/inventory.yaml`: lots (list, get, put), holds (put,
release), attachments (upload, list, download, delete), `lots` and `held` on the stock
reads, `kind: lot` in the lookup, `hold_not_inventorys`, `canManageLots` on `meta`.

**Server — Inventory:** the lots list and page reads, edit, holds endpoints releasing only
Inventory's own holds (M60, M61, M64); attachments through `srv/storage` scope
`inventory-lots`, the deletion ledger and its lease worker built through `module.Workers`
(M54); stock reads per lot with `held` (M63); lookup (M66); the live topics
`inventory.lot:<id>` and `inventory.lots` (Inventory's first: declare
`Module.LiveTopics`); the permission `inventory:lots-manage`.

**UI** (`inv/`): the `LineEditor`'s lot column (combobox, *New lot…*, *Serial
numbers…*); the receipt, move and adjustment pages; the movement page's lot; the lots list
and lot page (files, holds panel with *Quarantine*, *Block*, *Release*, edit); the
variant page's lot table and *of which on hold*; the lookup's lot match.

**Host:** the product page's stock panel per lot (`host/routes/products/$productId.tsx`);
the Inventory navigation's *Lots* entry and the spotlight action *Find a lot*; the
permission label for `inventory:lots-manage`; `user/index.md` for the new entry.

**Docs:** §18's L1b row. **Tests:** spec §20, L1b. Lots can only be made by tests until
M lifts the guard; the screens are tested against store-made tracked variants.

## Layer 4 — L2, the rest of Inventory (M69–M74)

**Seam:** `mig/00165_inventory_lot_counts_transfers.sql`; the OpenAPI changes to count
lines, transfer lines, the import's columns, `GET /lots/expiring`(+`.csv`), `GET
/lots/labels.pdf`, the attention items.

**Server:** count open per lot, added lines with lots, the post per (location, lot) and
the stale-count rule (M69); transfer send with FEFO proposal and the lot required, receive
and cancel with the same lot, the note PDF (M70); the import's lot columns (M71); the
expiring list and attention items (M72); LOT labels and the `LOT<id>` token in the lookup
(M73); the movements CSV (M74).

**UI** (`inv/`): the count screen grouped by lot; the transfer form's lot column; the
import page's template and preview; the expiring page; *Print labels* on the lot page and
the lots list.

**Host:** the dashboard's two attention items; the navigation entry *Expiring*;
`user/index.md`.

**Docs:** §18's L2 row. **Tests:** spec §20, L2.

## Layer 5 — O, Orders deliveries with lots (M75–M79)

**Seam:** `mig/00166_orders_delivery_lots.sql` (with `chosen`); `openapi/orders.yaml`:
`lots` on delivery lines (read and update), `picks_mismatch`.

**Server — Orders** (`srv/orders`): the proposal in `CreateDelivery` from `Stock.Lots`
read on the pool before the transaction, with `contracts.ProposeLots` (M76); editing picks
in `UpdateDelivery`, setting `chosen` (M77); `PostDelivery` issuing one line per pick with
`LotID` and `AllowExpired`, the refusal mapping by `Index`, and the one re-proposal of a
line whose picks are all proposed (M78, M65a); the pick list and packing slip
(`deliverypdf.go`:138–184) with the lot column and lines (M79).

**Other module's side:** none; Inventory's ledger already takes the lot (L1a).

**UI** (`ord/`): the delivery page's lot table per tracked line, *Change lot*, *Use
expired lot* behind `canOverrideExpired`; the order page's delivery history with lots.

**Docs:** §18's O row. **Tests:** spec §20, O.

## Layer 6 — M, Manufacturing lot capture (M80–M86)

**Seam:** `mig/00167_manufacturing_lots.sql`; `openapi/manufacturing.yaml`: `lotPattern` on
BoMs, `outputLot`, `outputSerials` and line `lots` on productions, the draft's proposed
lots and every site lot per tracked component, the MO's lots read.

**Server** (`srv/manufacturing`): the pattern renderer and its counter (M80); serial output
(M81); `production_line_lots`, the draft's FEFO proposal and lot list from `Stock.Lots`,
`splitConsumption` for tracked components, `AutoLot` when rows are omitted and the
re-proposal of unchanged proposed rows (M82, M65a); `EnsureLots` with
`ProductionDate` always set and `Today` read on the pool (M80); the lock order with steps
7a–7c and the one retry on 40P01 (M86); the event payload (M85).

**Other module's side — Products:** **remove the guard** (`tracking_unavailable`) and its
test; the variant form's *Tracking* select with the held refusal shown (`prd/`).

**UI** (`mfg/`): the production dialog's lots and output lot; the shop floor's *Register
production* with the same; the MO page's *Lots* tab; the BoM editor's pattern with a
preview.

**Docs:** §18's M row (with `user/products.md` and `reference/products.md`). **Tests:** spec §20, M, and Products' guard removal.

## Layer 7 — T, traceability, blocking and labels (M87–M94)

**Seam:** `contracts.DeliveryTrace` and its `Deps` slot (`srv/module`), provided by Orders;
`openapi/inventory.yaml`: `GET /trace`, `/trace.csv`, `/trace.pdf`, the block-with-downstream
request, `format=gs1` on labels, `trace_too_large`.

**Server — Inventory:** the graph walk over movement lines with lots, both directions, the
bounds and *already traced*, `unbuild` movements as unexpanded reverse edges (M88, M89);
the delivery read, `ErrForbidden` handled, and the customer gate (M90); CSV and PDF (M91);
block with the downstream lots as `block` holds, the walk run again inside the
transaction (M92); GS1-128 element strings with
FNC1 (`'ñ'`) and the fallback (M93).

**Server — Orders:** `DeliveryTrace`, provider-gated on `orders:access` from `ctx`, refusing
with `ErrForbidden` a principal without it and a call without a request (rule 12).

**UI:** `inv/`: the trace page (a tree with direction and component toggles, collapse of
*already shown* nodes, export buttons); the block dialog with its downstream summary; the
GS1 label option. `mfg/`: *Trace* on the MO's *Lots* tab. `ord/`: lot links on the delivery
page.

**Host:** the spotlight action *Trace a lot*; `user/index.md`.

**Docs:** §18's T row, including `module-boundaries.md` rule 12's new provider.
**Tests:** spec §20, T.

## Layer 8 — Q, quality checks and deviations (M95–M104)

**Seam:** `mig/00168_manufacturing_quality.sql`, `mig/00169_inventory_variant_rules.sql`;
OpenAPI: check templates in the BoM body, work-order checks and results, photos, deviations
(list, get, create, close, CSV), the variant rule in Inventory; the permission
`manufacturing:quality-manage`.

**Server — Manufacturing:** templates and the snapshot (M95, M96); results with the
server's judgement, supersession, photos through `srv/storage` scope
`manufacturing-quality` and `srv/imagemeta`, recording through `withOrders` with the check
row `FOR UPDATE` (M97); the production gated on the predecessors' checks and the output
work order's first-piece checks, Finish on a work order's own, *precedes* by sequence (F
changes it) (M98); deviations, automatic on fail, `HoldLots` for the lots so far and for
every lot the MO makes while the deviation is open (in `postProduction`'s step 7b), close
calling `ReleaseLotHolds` for its own source (M99); live `manufacturing.deviations`.

**Server — Inventory:** `variant_rules`, the receipt putting an inspection hold on new lots
and on an existing lot it adds to (M100), the *Awaiting inspection* count.

**UI:** `mfg/`: the BoM editor's checks per operation; the shop floor's *Checks* panel with
pass/fail, numeric pad and camera; the MO page's results; the deviations list and page.
`inv/`: the variant page's *Inspect on receipt*; the home page's *Awaiting inspection*.

**Host:** the navigation entry *Deviations*, the permission label, `user/index.md`.

**Docs:** §18's Q row. **Tests:** spec §20, Q.

## Layer 9 — R, replenishment suggestions (M105–M111)

**Seam:** `mig/00170_inventory_placement_multiple.sql`,
`mig/00171_manufacturing_replenishment.sql`; `contracts.Stock.Placements` and
`StockPlacement`; `contracts.SalesOrderLines.Unreserved` and `UnreservedDemand`, with
every fake of `SalesOrderLines`; OpenAPI: `multiple` on placements and their import, suggestions (list,
create MOs, dismiss), runs (start, list), rules.

**Server — Inventory:** `multiple` on placements, the import and the low-stock rounding;
`Stock.Placements` answering usable on hand.

**Server — Orders:** `SalesOrderLines.Unreserved`: open goods lines of sales orders at the
site, open quantity less reserved, > 0, on the pool.

**Server — Manufacturing:** projected with `reorder` drafts as supply, MTO output and its
lines left out, unreserved MO and sales demand (M107, M107a), and the suggestion rule
(M108); the lease worker `manufacturing.replenishment` registered through
`module.Workers`, nightly and on demand, rate-limited (M109); suggestions and their
actions, origin `reorder` (M110, M111); automatic creation behind the rule, off by
default, skipping a variant with an open `reorder` MO.

**UI:** `mfg/`: the suggestions page, selection, *Run now*, the BoM page's automatic rule;
`inv/`: the placement form's *Multiple*.

**Host:** the dashboard card *Suggestions to make*, the navigation entry, `user/index.md`.

**Docs:** §18's R row (with `reference/orders.md`). **Tests:** spec §20, R (the worker on a pool of one).

## Layer 10 — F, routing upgrades (M112–M119)

**Seam:** `mig/00172_manufacturing_routing.sql` (with the backfill of sequential
dependencies for every BoM and open MO); OpenAPI: `dependsOn` and `sendAheadQuantity` on
operations, `workCenterGroupId`, groups CRUD, *Register quantity*.

**Server:** the graph, its cycle check and the snapshot (M112, M113); `ReadyWorkOrders`
replacing `ReadyNextWorkOrder` (M114); the planner's topological walk and send-ahead offset
(M115, M116), with `centersOf` naming successors' centers and group members (M115);
groups, member choice, the board's drag between members, re-snapshotted rates and
expected minutes, the fixed member and `work_order_member_fixed` (M117–M119); Q's check
gate switched from sequence to the graph's predecessors (M98).

**UI** (`mfg/`): the BoM editor's *Depends on* and send-ahead fields with the graph
preview; the groups pages; the shop floor's *Group work* and *Register quantity*; the
board's member rows.

**Docs:** §18's F row. **Tests:** spec §20, F.

## Layer 11 — G, BoM depth (M120–M128)

**Seam:** `mig/00173_manufacturing_bom_depth.sql` (with the amended `ux_orders_origin_line`);
OpenAPI: BoM outputs, production outputs, unbuilds (list, get, create), split, merge.

**Server:** outputs and their snapshot, `output_is_component` and the cycle check over
outputs, produce lines keyed by (variant, line id), the MO's output location, the cost split
with a not-produced by-product (M120–M122); unbuild of a chosen lot through `Produce` with
`Unbuild: true`, restoring only from that lot's productions, held lots allowed and the
restored lots quarantined (M123); split and merge through `withScheduledOrders`, unplanning,
mixed origins refused, re-reservation and the re-snapshot (M124–M127); timeline events
(M128).

**UI** (`mfg/`): the BoM editor's *By-products*; the production dialog's outputs; the cost
panel's split; *Unbuild* on the MO page and the unbuilds list; *Split* and *Merge* dialogs
(merge from the MO list's selection).

**Host:** the navigation entry *Unbuilds*, `user/index.md`.

**Docs:** §18's G row. **Tests:** spec §20, G.

## Layer 12 — S, shared stations (M129–M143)

**Seam:** `mig/00174_identity_stations.sql`, `mig/00175_manufacturing_stations.sql`;
`openapi/identity.yaml`: stations (register, list, revoke, re-pair), pairing, the station's
own reads, operators list, operator sign-in and sign-out, the user's PIN, badges; the
`x-vantigo-station` extension on Manufacturing's allowed operations (M135);
`contracts.ViaStation` and `Via.StationID`; `Access.CheckStation`; the lint rule in
`srv/openapi/lint.go`.

**Server — Identity** (`srv/identity`): stations and pairing with `hashToken`/`newToken`,
pairing signing the session out, the 90-day token, the network allowlist; PIN hashing with
the password parameters over the peppered PIN and the dummy-hash path; badges under an HMAC
key from `srv/secrets`, PIN required with a badge by default; the per-station rate limit
(`srv/ratelimit`) and the per-(station, user) backoff with its alert mail; operator
sessions with idle and absolute expiry, switch out with the cookie alone; `Access.Check`
resolving the station cookie and the operator token into a station principal with the
intersected permissions — only for a rule `wrap` marks as station-allowed, station first,
`station_has_session` when a session cookie is present, the session's station checked;
`Access.CheckStation` for the live socket; the audit events (M139); erasure (M141).

**Server — platform:** the router's `station_scope` check in `srv/module/router.go`'s
`wrap`, passing the station flag to `Access.Check` (M134, M135); the live handler's station
path in `srv/live/handler.go`, its station re-check and per-station limits, and
`live.StationID(ctx)` for `Authorize` (§17).

**Server — Manufacturing:** station work centers and their check in every allowed handler
(M131); the work-center and work-order topics' `Authorize` admitting a station for its
own centers (§17); `station_id` on time logs, productions, results and deviations (M137).

**UI:** `host/routes/station` (pairing, operator chips, the PIN pad and badge field, the
shop floor for the active operator with the station context in place of the user, settings
and images, the station's live socket, the idle warning, the token in memory only);
*Settings → Security → PIN*; the admin's stations page under Workspace and the badge field
on the user page; `mfg/`: the work center page's *Stations*.

**Security review:** an opus security reviewer reads the whole layer — the credential
storage, the timing path, the rate limit and lockout, the scope check and its lint, the
cookie flags, the intersection, the audit — before the PR is opened. Its findings are fixed
in the layer.

**Docs:** §18's S row (`admin/authentication.md` first). **Tests:** spec §20, S, including
the test that walks every contract operation as a station principal and the test that
renders the station page against the real router and fails on any request outside the
allowed set.

## Layer 13 — K, kits (optional, M144–M146)

Built only if the stack has room when S is done; otherwise the spec's §12 moves to Later.

**Seam:** `mig/00176_manufacturing_kits.sql` (`boms.kind`), `mig/00177_orders_kits.sql`
(`lines.parent_line_id`, `lines.per_kit`); `contracts.KitStructure` and its `Deps` slot
(`srv/module`, provided by Manufacturing, consumed by Orders) with the depguard sets;
OpenAPI: `kind` on BoMs, child lines on order reads and writes.

**Server — Manufacturing:** kit BoMs (M144); `KitStructure`, ungated, exploded to three
levels.

**Server — Orders:** a kit line's children copied from `KitStructure` when it is added,
rescaled with its quantity; `reservable` false for a line with children; each child
reserving, delivering and picking lots as any goods line; the packing slip and pick list
(M145); kit availability (M146).

**UI:** `mfg/`: the BoM kind; `ord/`: the kit's child lines under it.

**Docs:** §18's K row. **Tests:** spec §20, K.

## Gotchas carried from earlier deliveries

- The balance key swap touches every Inventory query that joins `balances` on `(variant_id,
  location_id)`: grep `inventory/queries/*.sql` for `balances` and make each lot-aware or
  sum over lots on purpose.
- `ON CONFLICT` must name the three columns exactly as the `NULLS NOT DISTINCT` index does;
  a test proves the no-lot race still serialises.
- No ledger call may make a contract call: `Today` and the expired override come from the
  caller, read on the pool before its transaction (spec M62). Inventory's
  `noteContractCall` hook is the test.
- The `tracking_unavailable` guard lands in L1a and leaves in M; a layer between them that
  needs a tracked variant makes it through the store in its tests.
- `contracts.VariantEntry` is built in several places in Products; a missed one leaves
  `Tracking` empty and the engine treats it as `none`. Make `""` a failure in the engine.
- Run `go generate ./...` after any docs edit, or lint and tests fail.
- The pre-commit hook checks the whole tree: commit when the lanes are quiet.
- Lease workers and handlers must not hold one pooled connection while waiting for another;
  test on a pool of one.
- A new navigation entry or spotlight action needs `user/index.md` in both languages.
- Docs coverage in CI runs against the parent layer: `bun run
  tools/docs/check-coverage.ts --base <parent>`.
