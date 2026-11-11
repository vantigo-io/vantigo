# Manufacturing phase 2 — L1, L2, O, M, T, Q, R, F, G, S (and K): implementation plan

This plan builds phase 2 of Manufacturing. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-12-manufacturing-phase2-design.md`,
  revision 1. Its decisions are M49–M146.
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
| 2 | `claude/manufacturing-p2-l1` | L1: tracking mode, lots, the lot-aware ledger, receipts, moves, adjustments, lots pages, files, lookup, FEFO, expiry, contracts (M49–M68) | `00162_products_variant_tracking`, `00163_inventory_lots` |
| 3 | `claude/manufacturing-p2-l2` | L2: counts, transfers, import with lots; expiring list; lot labels (M69–M74) | `00164_inventory_lot_counts_transfers` |
| 4 | `claude/manufacturing-p2-o` | O: delivery picks per lot, FEFO, the PDFs (M75–M79) | `00165_orders_delivery_lots` |
| 5 | `claude/manufacturing-p2-m` | M: output lots, serials, consumed lots (M80–M86) | `00166_manufacturing_lots` |
| 6 | `claude/manufacturing-p2-t` | T: trace, export, block, `DeliveryTrace`, GS1-128 labels (M87–M94) | — |
| 7 | `claude/manufacturing-p2-q` | Q: checks, results, deviations, quarantine, inspection on receipt (M95–M104) | `00167_manufacturing_quality`, `00168_inventory_variant_rules` |
| 8 | `claude/manufacturing-p2-r` | R: placements' multiple, `Stock.Placements`, projected, the job, suggestions (M105–M111) | `00169_inventory_placement_multiple`, `00170_manufacturing_replenishment` |
| 9 | `claude/manufacturing-p2-f` | F: dependencies, send-ahead, work center groups (M112–M119) | `00171_manufacturing_routing` |
| 10 | `claude/manufacturing-p2-g` | G: by-products, unbuild, split and merge (M120–M128) | `00172_manufacturing_bom_depth` |
| 11 | `claude/manufacturing-p2-s` | S: stations, PIN and badge, the station scope (M129–M143) | `00173_identity_stations`, `00174_manufacturing_stations` |
| 12 | `claude/manufacturing-p2-k` | K (optional): kits (M144–M146) | `00175_manufacturing_kits`, `00176_orders_kits` |

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

## Layer 2 — L1, lots in Products and Inventory (M49–M68)

**Seam:**

- `mig/00162_products_variant_tracking.sql` (M49);
- `mig/00163_inventory_lots.sql`: `lots`, `lot_attachments`, `object_deletions`, the
  balance key swap (drop the primary key, create `ux_balances_key … NULLS NOT DISTINCT`),
  `movement_lines.lot_id` and `expired_override`, the `lot` counter (M52–M55, M62);
- `openapi/products.yaml`: `tracking`, `expiryDays`, `bestBeforeDays` on the variant;
  `tracking_held`, `tracking_requires_goods`;
- `openapi/inventory.yaml`: lots (list, get, put, status, attachments upload, list,
  download, delete), lot fields on receipt, move and adjustment lines, `lots` on the stock
  reads, `lot` on movement lines, `kind: lot` in the lookup, the codes, `meta` flags;
- `srv/contracts`: `VariantEntry` fields (M50), `VariantHoldTrackingChange` (M51),
  `Stock.Lots`, `StockLot`, `LotID`/`AllowExpired` on `IssueLine` and `ConsumeLine`,
  `LotID` on `ProduceLine`, `StockLedger.EnsureLots` and `SetLotStatus`, `NewLot`,
  `LotStatusChange`, the codes, `ProposeLots` (M65, M67);
- every fake: `srv/manufacturing/fakes_ledger_test.go`, Orders' ledger and stock fakes,
  `srv/modtest`.

**Server — Products** (`srv/products`):

- validate and store the fields; `tracking_requires_goods`; the type change to Service
  resetting tracking;
- the variant `PUT` (`variants.go`:175) asking `variantHeld` with the new purpose when the
  tracking changes, answering 409 `tracking_held` with `holdText`;
- fill the fields in every catalog read and `LockVariants`.

**Server — Inventory** (`srv/inventory`):

- `posting.go`: `balanceKey.lotID`, `engineLine.lotID/newLot`, `balanceKeys`, `keyColumns`,
  `lockBalances`, `writeLines`, `writeBalances`; steps 4a and 4b; the M56 table; the lock
  order comment rewritten (M58);
- `queries/ledger.sql`: the lot arrays and `ON CONFLICT (variant_id, location_id,
  lot_id)`; new `queries/lots.sql`;
- receipts, moves, adjustments with lots, new lots, generated numbers, serial expansion
  (M57, M59);
- the lots list and page reads, edit, status with events (M60, M61, M64);
- attachments through `srv/storage` scope `inventory-lots`, the deletion ledger and its
  lease worker (M54);
- expiry and the override with `contracts.HasPermission` (M62);
- stock reads per lot (M63), lookup (M66);
- `ledger.go`: `Issue` and `Produce` carry the lot to the engine; `EnsureLots`,
  `SetLotStatus`; `Stock.Lots`;
- the holder's doc comment naming the third purpose;
- the live topics `inventory.lot:<id>` and `inventory.lots` (Inventory's first: declare
  `Module.LiveTopics`);
- permissions `inventory:lots-manage`, `inventory:expired-override`.

**Other modules' side:** Orders' `PostDelivery` (`srv/orders/deliveries.go`:794) and
Manufacturing's `postProduction` (`srv/manufacturing/productions.go`:501, :594) pass
`LotID: 0`; their tests keep untracked fixtures; a test proves a tracked variant is
refused `lot_required` there until O and M.

**UI:**

- `prd/`: the variant form's *Tracking* select and the two offsets, with the held refusal
  shown;
- `inv/`: the `LineEditor`'s lot column (combobox, *New lot…*, *Serial numbers…*); the
  receipt, move and adjustment pages; the movement page's lot; the lots list and lot page
  (files, status dialog, edit); the variant page's lot table; the lookup's lot match.

**Host:** the product page's stock panel per lot (`host/routes/products/$productId.tsx`);
the Inventory navigation's *Lots* entry and the spotlight action *Find a lot*; the
permission labels; `user/index.md` for the new entry.

**Docs:** §18's L1 row, in en and nb where bilingual.

**Tests:** spec §20, Ledger, Products, Contracts.

## Layer 3 — L2, the rest of Inventory (M69–M74)

**Seam:** `mig/00164_inventory_lot_counts_transfers.sql`; the OpenAPI changes to count
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

## Layer 4 — O, Orders deliveries with lots (M75–M79)

**Seam:** `mig/00165_orders_delivery_lots.sql`; `openapi/orders.yaml`: `lots` on delivery
lines (read and update), `picks_mismatch`.

**Server — Orders** (`srv/orders`): the proposal in `CreateDelivery` from `Stock.Lots`
read on the pool before the transaction, with `contracts.ProposeLots` (M76); editing picks
in `UpdateDelivery` (M77); `PostDelivery` issuing one line per pick with `LotID` and
`AllowExpired`, the refusal mapping (M78); the pick list and packing slip
(`deliverypdf.go`:138–184) with the lot column and lines (M79).

**Other module's side:** none; Inventory's ledger already takes the lot (L1).

**UI** (`ord/`): the delivery page's lot table per tracked line, *Change lot*, *Use
expired lot* behind `canOverrideExpired`; the order page's delivery history with lots.

**Docs:** §18's O row. **Tests:** spec §20, O.

## Layer 5 — M, Manufacturing lot capture (M80–M86)

**Seam:** `mig/00166_manufacturing_lots.sql`; `openapi/manufacturing.yaml`: `lotPattern` on
BoMs, `outputLot`, `outputSerials` and line `lots` on productions and the draft, the MO's
lots read.

**Server** (`srv/manufacturing`): the pattern renderer and its counter (M80); serial output
(M81); `production_line_lots`, the draft's FEFO proposal from `Stock.Lots`,
`splitConsumption` for tracked components, the server-side proposal when rows are omitted
(M82); `EnsureLots` in the lock order (M86); the event payload (M85).

**UI** (`mfg/`): the production dialog's lots and output lot; the shop floor's *Register
production* with the same; the MO page's *Lots* tab; the BoM editor's pattern with a
preview.

**Docs:** §18's M row. **Tests:** spec §20, M.

## Layer 6 — T, traceability, blocking and labels (M87–M94)

**Seam:** `contracts.DeliveryTrace` and its `Deps` slot (`srv/module`), provided by Orders;
`openapi/inventory.yaml`: `GET /trace`, `/trace.csv`, `/trace.pdf`, the block-with-downstream
request, `format=gs1` on labels, `trace_too_large`.

**Server — Inventory:** the graph walk over movement lines with lots, both directions, the
bounds and *already traced* (M88, M89); the delivery read and the customer gate (M90);
CSV and PDF (M91); block with the downstream lots (M92); GS1-128 element strings with
FNC1 (`'ñ'`) and the fallback (M93).

**Server — Orders:** `DeliveryTrace`, provider-gated on `orders:access` from `ctx`, answering
nothing without a request (rule 12).

**UI:** `inv/`: the trace page (a tree with direction and component toggles, collapse of
*already shown* nodes, export buttons); the block dialog with its downstream summary; the
GS1 label option. `mfg/`: *Trace* on the MO's *Lots* tab. `ord/`: lot links on the delivery
page.

**Host:** the spotlight action *Trace a lot*; `user/index.md`.

**Docs:** §18's T row, including `module-boundaries.md` rule 12's new provider.
**Tests:** spec §20, T.

## Layer 7 — Q, quality checks and deviations (M95–M104)

**Seam:** `mig/00167_manufacturing_quality.sql`, `mig/00168_inventory_variant_rules.sql`;
OpenAPI: check templates in the BoM body, work-order checks and results, photos, deviations
(list, get, create, close, CSV), the variant rule in Inventory; the permission
`manufacturing:quality-manage`.

**Server — Manufacturing:** templates and the snapshot (M95, M96); results with the
server's judgement, supersession, photos through `srv/storage` scope
`manufacturing-quality` and `srv/imagemeta` (M97); finish and register gated by the
checks (M98); deviations, automatic on fail, quarantine through
`StockLedger.SetLotStatus` on the same transaction, close releasing (M99); live
`manufacturing.deviations`.

**Server — Inventory:** `variant_rules`, the receipt quarantining lots of such variants
(M100), the *Awaiting inspection* count.

**UI:** `mfg/`: the BoM editor's checks per operation; the shop floor's *Checks* panel with
pass/fail, numeric pad and camera; the MO page's results; the deviations list and page.
`inv/`: the variant page's *Inspect on receipt*; the home page's *Awaiting inspection*.

**Host:** the navigation entry *Deviations*, the permission label, `user/index.md`.

**Docs:** §18's Q row. **Tests:** spec §20, Q.

## Layer 8 — R, replenishment suggestions (M105–M111)

**Seam:** `mig/00169_inventory_placement_multiple.sql`,
`mig/00170_manufacturing_replenishment.sql`; `contracts.Stock.Placements` and
`StockPlacement`; OpenAPI: `multiple` on placements and their import, suggestions (list,
create MOs, dismiss), runs (start, list), rules.

**Server — Inventory:** `multiple` on placements, the import and the low-stock rounding;
`Stock.Placements`.

**Server — Manufacturing:** projected (M107) and the suggestion rule (M108); the lease
worker `manufacturing.replenishment` registered through `module.Workers`, nightly and on
demand, rate-limited (M109); suggestions and their actions, origin `reorder` (M110, M111);
automatic creation behind the rule, off by default.

**UI:** `mfg/`: the suggestions page, selection, *Run now*, the BoM page's automatic rule;
`inv/`: the placement form's *Multiple*.

**Host:** the dashboard card *Suggestions to make*, the navigation entry, `user/index.md`.

**Docs:** §18's R row. **Tests:** spec §20, R (the worker on a pool of one).

## Layer 9 — F, routing upgrades (M112–M119)

**Seam:** `mig/00171_manufacturing_routing.sql` (with the backfill of sequential
dependencies for every BoM and open MO); OpenAPI: `dependsOn` and `sendAheadQuantity` on
operations, `workCenterGroupId`, groups CRUD, *Register quantity*.

**Server:** the graph, its cycle check and the snapshot (M112, M113); `ReadyWorkOrders`
replacing `ReadyNextWorkOrder` (M114); the planner's topological walk and send-ahead offset
(M115, M116); groups, member choice, the board's drag between members, re-snapshotted rates
(M117–M119).

**UI** (`mfg/`): the BoM editor's *Depends on* and send-ahead fields with the graph
preview; the groups pages; the shop floor's *Group work* and *Register quantity*; the
board's member rows.

**Docs:** §18's F row. **Tests:** spec §20, F.

## Layer 10 — G, BoM depth (M120–M128)

**Seam:** `mig/00172_manufacturing_bom_depth.sql` (with the amended `ux_orders_origin_line`);
OpenAPI: BoM outputs, production outputs, unbuilds (list, get, create), split, merge.

**Server:** outputs and their snapshot, production lines for by-products with lots, the cost
split (M120–M122); unbuild through `Produce` with the proportional restore (M123); split and
merge with re-reservation and the re-snapshot (M124–M127); timeline events (M128).

**UI** (`mfg/`): the BoM editor's *By-products*; the production dialog's outputs; the cost
panel's split; *Unbuild* on the MO page and the unbuilds list; *Split* and *Merge* dialogs
(merge from the MO list's selection).

**Host:** the navigation entry *Unbuilds*, `user/index.md`.

**Docs:** §18's G row. **Tests:** spec §20, G.

## Layer 11 — S, shared stations (M129–M143)

**Seam:** `mig/00173_identity_stations.sql`, `mig/00174_manufacturing_stations.sql`;
`openapi/identity.yaml`: stations (register, list, revoke, re-pair), pairing, the station's
own reads, operators list, operator sign-in and sign-out, the user's PIN, badges; the
`x-vantigo-station` extension on Manufacturing's allowed operations (M135);
`contracts.ViaStation` and `Via.StationID`; the lint rule in `srv/openapi/lint.go`.

**Server — Identity** (`srv/identity`): stations and pairing with `hashToken`/`newToken`;
PIN hashing with the password parameters and the dummy-hash path; badges under an HMAC key
from `srv/secrets`; the per-station rate limit (`srv/ratelimit`) and the per-user lockout;
operator sessions with idle and absolute expiry; `Access.Check` resolving the station cookie
and the operator token into a station principal with the intersected permissions; the
audit events (M139); erasure (M141).

**Server — platform:** the router's `station_scope` check in `srv/module/router.go`'s
`wrap` (M135).

**Server — Manufacturing:** station work centers (M131); `station_id` on time logs,
productions, results and deviations (M137).

**UI:** `host/routes/station` (pairing, operator chips, the PIN pad and badge field, the
shop floor for the active operator, the idle warning, the token in memory only);
*Settings → Security → PIN*; the admin's stations page under Workspace and the badge field
on the user page; `mfg/`: the work center page's *Stations*.

**Security review:** an opus security reviewer reads the whole layer — the credential
storage, the timing path, the rate limit and lockout, the scope check and its lint, the
cookie flags, the intersection, the audit — before the PR is opened. Its findings are fixed
in the layer.

**Docs:** §18's S row (`admin/authentication.md` first). **Tests:** spec §20, S, including
the test that walks every contract operation as a station principal.

## Layer 12 — K, kits (optional, M144–M146)

Built only if the stack has room when S is done; otherwise the spec's §12 moves to Later.

**Seam:** `mig/00175_manufacturing_kits.sql`, `mig/00176_orders_kits.sql`;
`contracts.Production.Kits`; OpenAPI: `kind` on BoMs, kit lines on order reads.

**Server:** kit BoMs (M144); Orders' reservation, delivery and packing slip per component
(M145); kit availability (M146).

**UI:** `mfg/`: the BoM kind; `ord/`: the kit's components under its line.

**Docs:** §18's K row. **Tests:** reserving, delivering with lots, a nested kit.

## Gotchas carried from earlier deliveries

- The balance key swap touches every Inventory query that joins `balances` on `(variant_id,
  location_id)`: grep `inventory/queries/*.sql` for `balances` and make each lot-aware or
  sum over lots on purpose.
- `ON CONFLICT` must name the three columns exactly as the `NULLS NOT DISTINCT` index does;
  a test proves the no-lot race still serialises.
- `contracts.VariantEntry` is built in several places in Products; a missed one leaves
  `Tracking` empty and the engine treats it as `none`. Make `""` a failure in the engine.
- Run `go generate ./...` after any docs edit, or lint and tests fail.
- The pre-commit hook checks the whole tree: commit when the lanes are quiet.
- Lease workers and handlers must not hold one pooled connection while waiting for another;
  test on a pool of one.
- A new navigation entry or spotlight action needs `user/index.md` in both languages.
- Docs coverage in CI runs against the parent layer: `bun run
  tools/docs/check-coverage.ts --base <parent>`.
