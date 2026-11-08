# Inventory phase 2 — deliveries A and B: implementation plan

The plan for Inventory's second phase. It draws on:

- **The spec:** `docs/superpowers/specs/2026-11-08-inventory-phase2-design.md`
  (revision 2), whose decisions are I35–I65;
- **the research:** `docs/superpowers/research/2026-11-08-inventory-phase2.md` ("R"),
  with the owner's answers in §9.2 and the sketches in §6–§7;
- **phase 1:** the design `specs/2026-10-27-inventory-design.md` ("D") and its plan
  `plans/2026-10-27-inventory-plan.md`, whose *Gotchas* stand.

Two deliveries, each **one pull request against `main`**. They may be **stacked** (B on
A, so work never waits for a merge); when A merges, B is retargeted to `main` and its
commits are cherry-picked onto a fresh branch from `main` if a rebase would need a
force-push. PRs land by **rebase**. Migration numbers are claimed at the cut and
renumbered inside the branch's history if `main` takes them first — check `main`'s latest
right before merging.

`srv/` abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`, `ui/`
`apps/inventory/frontend/src/`, `host/` `apps/host/frontend/src/`, and *the contract*
`srv/openapi/specs/inventory.yaml`.

| Delivery | Migration (main's latest `00139` at `4dcc14cd3`) | Branch |
| --- | --- | --- |
| A — transfers | the next free at A's cut: `inventory_transfers` (§3.1–3.4) | `claude/inventory-p2-a-transfers` |
| B — placement | the next free at B's cut: `inventory_placements` (§3.5) | `claude/inventory-p2-b-placement` |

## Task 0 — Land the research, the spec and this plan

The three documents are on `claude/inventory-phase2-research`. They describe no
published page and change no source path, so the docs check finds nothing to waive and
the branch needs **no `Docs-Impact` trailer**. Open it as `docs(inventory): research,
design and plan for Inventory phase 2` and merge it before A's PR. A is cut from `main`
at once — the documents are not code A compiles against.

---

# Delivery A — transfers between sites

## What A ships

I35–I53 and I62–I65's A parts:

- the tables `counters`, `transfers`, `transfer_lines`, `transfer_actions`,
  `transfer_action_lines`;
- the kinds `transfer_out`/`transfer_in`; send, receive (in parts, with *close the
  difference*), cancel, send-and-receive, all in I38's order;
- the holder's `in_transit` and Products' refusal text; archiving and deleting refused
  around open transfers;
- `incoming` on site stock, `inTransit` on the totals, the site's open-transfer counts,
  the batch read of stock at sites, the lookup's `TRF-`, the movement's `transfer` link;
- the følgeseddel PDF; the `transferOverdue` attention item, with the endpoint's gate
  moved per item;
- `inventory:transfers-manage`;
- the app's transfers list, draft form, send dialog, receive page, close dialog and
  transfer page; the site's Incoming/Outgoing panels; the movements' new kinds; the
  variant page's incoming and differences; the Stock card's *In transit*;
- the docs of spec §8, A's parts.

## Task A1 — Dossiers

Two Explore dossiers against `main` at the cut, saved to the scratchpad by hand:

1. **Server:**
   - the ledger: `srv/inventory/{posting.go, ledger.go, moves.go, receipts.go, errors.go,
     events.go, holder.go, sites.go, locations.go, stock.go, totals.go, lookup.go,
     stats.go, labels.go}` and `queries/*.sql` — the engine's `post` and `postOptions`, the
     lock order comment (:31-54), `Issue`'s derived key (:550), `postMovement`'s retry;
   - Orders' number series (`srv/orders/settings.go`:25-36, `queries/counters.sql`) and
     delivery PDF (`srv/orders/deliverypdf.go`);
   - Products' `holdText`/`holdRemedy` (`srv/products/variants.go`:371-405) and the
     type-change refusal's path;
   - the attention item's shape in every contract that has one (the shared shape I62
     keeps), and the host's `lib/dashboard-helpers.ts`.
2. **App, host and docs:**
   - `ui/components/{movement-form.tsx, line-editor.tsx, location-picker.tsx,
     product-stock.tsx, movement-list.tsx}`, `ui/pages/{movements.tsx, movement.tsx,
     site.tsx, variant.tsx, count.tsx}` (its virtualiser for a long receive), `ui/lib/
     {routes.ts, navigation.ts, capabilities.ts}`, `ui/i18n.ts`, `ui/assist-targets.json`;
   - the host's inventory routes, `catalogs/{navigation.ts, admin.ts, dashboard.ts}`,
     `routes/dashboard.tsx`;
   - `reference/inventory.md`'s sections and `sources`, `user/inventory.md` en and nb,
     and `bun run tools/docs/check-coverage.ts --list` for the pages A's paths pull in.

## Task A2 — The seam, by hand (sequential, before any lane)

Done by the orchestrator, so every lane compiles against one set of names.

1. **The migration** (spec §3.1–3.4), in the house style of `00125`; the check
   constraints and partial indexes as written; `transfer_lines` `on delete cascade`.
   `transfer_lines.suggestion` is created here, with the table: A's contract carries the
   field (spec §6), A stores and returns it as given, and only B's list writes one.
2. **The contract**, by a parse/stringify script proven byte-identical on a round trip
   first (never `contract normalize`):
   - every A row of spec §6, each with a `summary`; request schemas
     `additionalProperties: false`; nullable fields listed in `required`;
   - `InventoryMovementKind` gains the two kinds; `InventoryMovement.transfer`;
     `InventorySiteStock.incoming`; totals' `inTransit`; `InventorySite`'s two counts;
     `InventoryLookupMatch` kind `transfer`; the attention enum gains `transferOverdue`;
     `getInventoryMeta` gains `canTransfer`;
   - every new code of spec §4 in the problem's code enum.
3. **Contracts:** `contracts.VariantHoldKindInTransit` in `srv/contracts/references.go`.
4. **Generation:** `srv/inventory/queries/transfers.sql` with the queries both server lanes
   need (create, read with lines and actions, list with its filters, lock, the replay
   lookup, the action writes, the totals updates, the in-transit sums for the holder, the
   site stock and the totals, the archive checks, the overdue list, the counter upsert);
   `go generate ./...`; `bun run gen:client`.
5. **The skeleton:** `srv/inventory/transfers.go` with typed 503 stubs for the new
   operations; the permission in `module.go` (key, displays of spec §5); the codes in
   `errors.go`; `closedSchemas`/`closedItems` for the new request bodies.
6. **The frontend skeleton:** the route constants, the sidebar entry, the i18n keys en/nb
   (spec §7's words), placeholder pages; the host's routes and `catalogs/admin.ts`
   permission label (equal to `module.go`'s; a test pins them).
7. **The integration test:** `note.pdf` in `filesOrRedirects`
   (`srv/integration/assistant_test.go`:322).
8. **Gates:** `mise run server:check`, `bun run frontend:check`. A local WIP commit, squashed
   at A8.

## Task A3 — Server lane 1: the transfer document and its actions (parallel with A4)

**Files:** `srv/inventory/transfers.go`, `transfer_actions.go`, their tests; `posting.go`
(the lock-order comment, I38; the source reference's comment at :94-95, which now says
it is set for a transfer's postings, I45; `postOptions` gains the action's per-line
check).

1. Create, read, list, edit, delete of drafts (I36, I37): the number from the counter in
   the creating transaction; SKU/name snapshots on write; If-Match.
2. One `runAction` helper implementing I38's six steps, inside its own
   `db.RetrySerializable`; the movement's `client_request_id` is the action's.
3. **Send** (I39): the derived id `…\x00send`; `from_location_required`; lines resolved and
   snapshotted after `LockVariants`; the `transfer_out` movement; `sent` action lines.
4. **Receive** (I40, I41, I42): the client's id; the outstanding judged in units in
   `postOptions.check`; `transfer_in` at the to site; `received` action lines with their
   locations; *close* writing `missing`; a close-only receive with no movement.
5. **Send and receive** (I43): two actions, two movements, `…\x00send`/`…\x00receive`; a
   mismatching send hash answers `transfer_not_draft`.
6. **Cancel** (I44): `returned` lines, the `transfer_in` back at the from site.
7. Events (I64) for every write.
8. **Tests:** every row of spec §10's *Ledger* and *Concurrency* (a pool of four, the
   harness's `-p 1` rule for the shared test database), and the edge cases of spec §9 that
   are A's — 1–8 — each as a named test; a count over a location sent from and received
   into flags both lines moved (I49).

## Task A4 — Server lane 2: what reads and guards a transfer (parallel with A3)

**Files:** `holder.go`, `sites.go`, `locations.go`, `stock.go`, `totals.go`, `lookup.go`,
`stats.go`, `movements.go`, a new `stock_sites.go`, `transfer_note.go`; their tests.

1. **The holder** (I47): `in_transit` after `stock` and `reserved`, for both purposes.
2. **Archiving and deleting** (I48): the three refusals.
3. **Reads** (I46, I53): `incoming` on site stock with a row for a site holding only
   incoming; `inTransit` on totals keeping a variant wholly in transit; the sites' counts;
   `GET /stock/sites`; the movement's `transfer`.
4. **The lookup** (I52): `TRF-<number>`.
5. **The følgeseddel** (I51): maroto, the module's fonts, Code 128 of the token.
6. **The dashboard** (I62): the gate per item; `transferOverdue` in the shared shape.
7. **Tests:** spec §10's *Holder*, *Archiving* and *Reads* rows; the PDF renders and names
   every line; with the module disabled, the holder still answers `in_transit` (I50).

## Task A5 — Products' side (small; with A4)

**Files:** `srv/products/variants.go`, its tests.

`holdText`/`holdRemedy` gain the `in_transit` branch (I47), for the delete and the change
to Service. The Products integration test refuses both while a transfer holds the
variant, and allows both after its cancel.

## Task A6a — The app (after A2; parallel with A3–A5)

**Files:** `ui/pages/{transfers.tsx, transfer.tsx, new-transfer.tsx,
transfer-receive.tsx}`, `ui/components/{transfer-form.tsx, send-transfer-dialog.tsx,
close-difference-dialog.tsx, transfer-list.tsx, transfer-status.tsx}`; changes to
`site.tsx`, `variant.tsx`, `movement-list.tsx`, `movement.tsx`, `product-stock.tsx`,
`lib/navigation.ts`, `i18n.ts`, `assist-targets.json`; tests beside each.

1. **The list** (R §6.1): filters, search, the overdue mark.
2. **The draft form** (R §6.2): the shared `LineEditor` with the two read-only columns
   from `GET /stock/sites`; the short-line warning; *Save draft*, *Send…*, *Send and
   receive…*, *Delete draft*.
3. **The send dialog** (R §6.3): a source per line, defaulting to the location holding
   most (B adds the primary); the reserved warning; *Print the følgeseddel*.
4. **The receive page** (R §6.4): a scan field (+1 per SKU or barcode; `TRF-…` opens the
   transfer), *Accept all*, *Now* and *To location* per line, the virtualiser for long
   transfers, a client request id made on mount and kept across a retry; after a refusal,
   reload the transfer and keep the scanned counts.
5. **Close the difference** (R §6.4): one reason, an optional note.
6. **The transfer page** (R §6.5): lines, actions, the buttons by status and
   `canTransfer`.
7. **Elsewhere:** the site's Incoming/Outgoing panels; the movements' kinds and link; the
   variant page's *+N incoming* and differences; the Stock card's *In transit*.
8. **Tour targets** (spec §7).

## Task A6b — The host (parallel with A6a)

The mirrored routes, new files in `host/routes/inventory/` beside the existing ones
(`transfers.index.tsx`, `transfers.new.tsx`, `transfers.$transferId.tsx`,
`transfers.$transferId.receive.tsx`, named as the existing files are); the sidebar label in
`catalogs/navigation.ts`; the attention item in `lib/dashboard-helpers.ts` (where
attention items render; `catalogs/dashboard.ts` holds only the tiles); the permission
label in `catalogs/admin.ts`.

## Task A7 — Docs (after A3–A6; in parallel with the reviews)

Two documentation lanes:

1. **The user guide**, en then nb, page for page:
   - `user/inventory.md`: the sidebar's *Transfers*; new sections *Transfer stock between
     sites*, *Receive a transfer*, *Close a difference*, *Cancel a transfer*, *Print the
     følgeseddel*; the Stock card's *In transit*; the dashboard's overdue item; *Who sees
     what* with the new key;
   - `user/products.md`: why a variant on its way between sites cannot be deleted or made
     a service.
2. **Reference and the rest:**
   - `reference/inventory.md`, from the code: the schema, the seventh permission,
     *within one site* (how a transfer keeps it), the kinds, *Transfers* (states, actions,
     the order of I38, idempotency, in transit), reads, locks, refusals, the dashboard,
     *enabling and disabling* (I50), *what comes next*, endpoints; `sources` gains the
     migration;
   - `reference/products.md`: the `in_transit` hold;
   - `contributing/module-boundaries.md`: `contracts/references.go`'s new kind;
   - `reference/assistant.md`: the tour targets;
   - `ROADMAP.md`: transfers are Inventory's, and done.
3. `go generate ./...` in `apps/server` for the Assistant's embedded copy, then
   `mise run docs:check`.

## Task A8 — Reviews, gates, commits, PR

1. **Reviews:** two opus reviews in parallel, the server (A3–A5) and the app (A6), each
   followed by a fix pass; doc deltas go to the docs lanes. The server review checks:
   I38's order in every action; no replay judged after the status; the outstanding in
   units; `inTransit` zero after cancel and close; no SQL outside `inventory`; the holder
   on the caller's transaction; no float arithmetic on quantities.
2. **Gates**, one at a time: `mise run server:check`; `mise run server:test` (rerun a
   package failing with SQLSTATE 53300 alone with `-p 1`); `mise run frontend:check` (read
   its log — the wrapper exits 0); `bun run tools/docs/check-coverage.ts --base
   origin/main` after committing.
3. **Commits** (signed, Conventional, the attribution line):
   - `feat(products): a variant on its way between sites is held (Inventory phase 2 A)`;
   - `feat(inventory): transfers between sites — send, receive, close, cancel (Inventory
     phase 2 A)`;
   - `feat(inventory-ui): transfers — the list, the draft, sending and receiving (Inventory
     phase 2 A)`;
   - `docs(inventory): transfers between sites (Inventory phase 2 A)`.
4. **The PR** against `main`; bind it, read CI, update the memory.

---

# Delivery B — placement and the low-stock list

## What B ships

I54–I61 and I62's `lowStock`:

- `inventory.placements` (the line's `suggestion` column came with A);
- PUT and DELETE of a placement (`If-None-Match: *` / `If-Match`), the placements import;
- the rule, the rounding to the smallest packaging and its sentence, the low-stock list
  and its CSV, `openDrafts`, `elsewhere`;
- the primary location as the default in receipts, transfer sends and receives, the
  variant's rows, and Orders' pick list (`contracts.StockLocation.Primary`);
- `searchInventoryVariants?siteId` with `primaryLocationId`; placements on site stock and
  `GET /stock/sites`;
- the `lowStock` attention item;
- the app's placement dialog, the low-stock page with *Create transfer* and *Export CSV*,
  the import's second kind, the suggestion's sentence on drafted lines;
- the docs of spec §8, B's parts.

## Task B2 — The seam, by hand

The migration (§3.5); the integration test's `filesOrRedirects` gains `low-stock.csv`
and the placements import; the contract's B rows and
changed reads (§6); `contracts.StockLocation.Primary`; `queries/placements.sql` (the
upsert, delete, the low-stock statement of I58 with its cursor, the per-site count, the
`elsewhere` aggregate, the import's batch write); generation; stubs; i18n keys; gates; a
WIP commit.

## Task B3 — Server: placements, the rule, the list (parallel with B4)

**Files:** `srv/inventory/{placements.go, low_stock.go, import_placements.go}`, changes to
`stock.go`, `stock_sites.go`, `variants.go` (the search's `siteId`), `stats.go`,
`locations.go` (archiving clears a primary; deleting clears it explicitly before the
row goes, so the `placement.cleared` event is written — the foreign key's `set null` is
only the backstop), `transfers.go` (lines keep `suggestion`); tests, including spec §9's
edge cases 11–15 by name.

1. Placements: PUT with `If-None-Match: *` or `If-Match`, `placement_empty`,
   `maximum_below_reorder_point`, `placement_location_invalid`; DELETE; events; the SKU
   snapshot.
2. **The rule** (I56) and **the rounding** (I57) as one pure function, tested against spec
   §10's table before the SQL uses its inputs.
3. **The list** (I58): the statement, the cursor, `q` on the snapshot, the snapshot's
   refresh on a changed catalog read, `elsewhere`, `openDrafts`; the CSV (I60).
4. **The import** (I61): dry run, commit, clearing, ≤ 5,000 rows, idempotent by the file.
5. **The dashboard's** `lowStock` (I62).
6. **Defaults:** `primaryLocationId` on the variant search; `primary` on site stock rows.

## Task B4 — Orders' pick list (small; parallel with B3)

**Files:** `srv/inventory/ledger.go` (`Stock.Locations` fills `Primary`),
`srv/orders/deliveries.go` (`deliveryLocations` sorts primary first), `srv/orders/
deliverypdf.go` (the pick list keeps that order); tests on both sides.

## Task B6a — The app (after B2; parallel with B3, B4)

**Files:** `ui/pages/low-stock.tsx`, `ui/components/{placement-dialog.tsx,
low-stock-list.tsx, suggestion-note.tsx}`; changes to `variant.tsx`, `import.tsx`,
`new-transfer.tsx`, `transfer-form.tsx`, `send-transfer-dialog.tsx`,
`transfer-receive.tsx`, `new-receipt.tsx`, `line-editor.tsx` (the default location),
`product-stock.tsx`, `lib/navigation.ts`, `i18n.ts`, `assist-targets.json`; tests.

1. **The placement dialog** (R §7.1) on the variant page's site rows; the rows' *primary*,
   *low* and levels.
2. **The low-stock page** (R §7.3): the site filter, search, selection, *Create transfer to
   {site} from [site]* (hidden without `canTransfer`; asks when a draft is open), *Export
   CSV*.
3. **`suggestion-note`** — the one component that writes the rounding sentence (I57), used
   by the list and by drafted transfer lines, in en and nb, with the *above the maximum*
   clause; tested in both languages.
4. **Defaults:** the primary location in receipts, the send dialog and the receive page.
5. **The import page's** second kind; the Stock card's *reorder ≤ N*.

## Task B6b — The host

`host/routes/inventory/low-stock.tsx`; the sidebar's *Low stock* label; the dashboard's `lowStock` item in `lib/dashboard-helpers.ts`.

## Task B7 — Docs

1. **User guide**, en then nb: `user/inventory.md` — *Set an item's primary location and
   reorder point*, *The low-stock list* (the rule in plain words, the rounding sentence,
   *Create transfer*, *Export CSV*), *Import placements*, the Stock card, the dashboard;
   `user/orders.md` — the pick list's primary location first.
2. **Reference:** `reference/inventory.md` (placement, the rule, the list, the import,
   endpoints, `sources`); `reference/orders.md` (the pick list's order);
   `contributing/module-boundaries.md` (`contracts/stock.go`'s `Primary`);
   `reference/assistant.md`; `ROADMAP.md` (placement done; what stays Later).
3. `go generate ./...`, `mise run docs:check`.

## Task B8 — Reviews, gates, commits, PR

As A8. The server review checks the rule against spec §10's table, the statement's plan
on a seeded tenant (10,000 placements across three sites), no cross-schema join, and the
import's idempotency. Commits:

- `feat(inventory): placement, the low-stock list and the placements import (Inventory
  phase 2 B)`;
- `feat(orders): the pick list puts an item's primary location first (Inventory phase 2
  B)`;
- `feat(inventory-ui): placements and the low-stock list (Inventory phase 2 B)`;
- `docs(inventory): placement and the low-stock list (Inventory phase 2 B)`.

---

## Documentation pages changed, by delivery

| Page | A | B |
| --- | --- | --- |
| `user/inventory.md` (en, nb) | transfers, receiving, differences, the Stock card, the dashboard, who sees what | placement, the low-stock list, the import, the Stock card, the dashboard |
| `user/products.md` (en, nb) | the in-transit refusal | — |
| `user/orders.md` (en, nb) | — | the pick list's order |
| `reference/inventory.md` | transfers and everything they touch | placement and the list |
| `reference/products.md` | the `in_transit` hold | — |
| `reference/orders.md` | — | the pick list's order |
| `reference/assistant.md` | tour targets | tour targets |
| `contributing/module-boundaries.md` | `references.go` | `stock.go` |
| `ROADMAP.md` | transfers done | placement done |

## Gotchas

Phase 1's stand (D's plan, *Gotchas*), and these from the deliveries since:

- A fresh worktree needs `bun install --frozen-lockfile`; a stale one shows as Biome
  rejecting `biome.json` or the i18n check missing a package.
- After any docs edit, `go generate ./...` in `apps/server`, or Go lint and
  `TestEmbeddedDocsAreTheDocumentation` fail.
- The docs check wants `module-boundaries.md` when a contract file changes, and
  `reference/assistant.md` when `assist-targets.json` does.
- A whole-tree `go test` exhausts the shared test Postgres (SQLSTATE 53300); rerun the
  failing packages alone with `-p 1`.
- `frontend:check`'s wrapper exits 0 on failure; read its log.
- Amending and force-pushing are blocked; add follow-up commits, and land a stacked branch
  after its base merges by cherry-picking onto a fresh branch from `main`.
- Commits are SSH-signed through 1Password (`-S`); `filter-branch` drops signatures.
- Right before merging, rebase onto the latest `main` and check its migration numbers.
