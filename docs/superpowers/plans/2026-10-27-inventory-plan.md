# Inventory phase 1 — deliveries A and B: implementation plan (revision 2)

The plan for the Inventory module's first phase. It draws on two documents:

- **The spec:** `docs/superpowers/specs/2026-10-27-inventory-design.md`, whose decisions
  are numbered I*n*.
- **The research:** `docs/superpowers/research/2026-10-27-inventory-module.md` ("N").

There are two deliveries, each **one pull request opened against `main`**:

- **B is cut from `main` after A has merged.** It is not stacked on A, because a stacked
  delivery was once merged into its base branch.
- PRs land by **rebase**. Migration numbers are claimed when the branch is cut and
  renumbered inside the branch history if `main` takes them first.

`srv/` abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`, and `fe/`
`apps/inventory/frontend/src/`.

| Delivery | Migrations (from `main` at `98617107`, latest `00075`) | Branch |
| --- | --- | --- |
| A | `00076_inventory_baseline.sql` | `claude/inventory-a-…` |
| B | `00077_products_packagings.sql`, `00078_inventory_counts.sql` | `claude/inventory-b-…` |

## Task 0 — Land the research and the spec (before A)

1. Commit N, the spec and this plan as `docs(inventory): research, design and plan for
   the Inventory module`, with `Docs-Impact: none — design documents, not published
   pages`.
   - That trailer must **not** reach a feature branch: anywhere in a branch it waives
     every coverage finding. Commit Task 0 on its own branch and merge it first, or fold
     it into A's docs commit **without** the trailer.
2. The spec's §13 questions were answered by the owner on 2026-10-27 and are recorded
   there.

---

# Delivery A — sites, locations, the ledger, the panel, the lookup

## What A ships

**Inventory, the module:**
- the module `inventory` with its six permission keys (§5);
- sites and locations (I1–I6, I21), with bulk creation and the events log (I31);
- the ledger (I8–I15): adjustments, moves and receipts, idempotent postings, the lock
  order (§4);
- the location contents (I29), product and variant stock (I27), the movements (I30),
  the lookup (I28), and the stats.

**The Products seam:**
- `ProductCatalog` gains barcode, `VariantsOfProduct`, `LookupCode` (SKU and variant
  barcode) and `LockVariants` (I22);
- the `VariantReferences` slot, and the variant delete in a transaction refusing
  `variant_has_stock` (I23);
- the Goods→Service type change refused while any variant holds stock (§13.1).

**The frontend:**
- the app's A pages (spec §7);
- Products' `extraPanels` and its delete dialog;
- the host: tile, routes, product-page composition, dashboard card, spotlight,
  permission labels.

**The docs and the roadmap** (spec §8).

**Out of A:** packagings, counts, labels, the import, the list column, the `GET /stock`
totals endpoint, and `entered` on lines (all B).

## Task A1 — Dossiers

Run three Explore dossiers against `main` at the cut, with the reports saved to the
scratchpad by hand (Explore cannot write):

1. **Server:**
   - the newest module's scaffold (`srv/planning`: `module.go`, `server.go`,
     `authorize.go`, `errors.go`, `strictbody.go`, `events.go`, `stubs.go`, `main_test.go`
     and its harness);
   - `srv/products` (`catalog.go`, `variants.go`, `queries/products.sql`);
   - the many-provider slots in `srv/module/module.go` and `compose.go`;
   - `modtest`'s `With…` options;
   - `srv/invoices/decimal.go`;
   - the `InvoicedWork` contract's transaction-carrying shape.
2. **Apps and host:**
   - `apps/planning/frontend` as the newest app;
   - Staff's `extraTabs` and its host route;
   - `apps/products/frontend/src/pages/products.$productId.tsx` and the delete dialog;
   - `apps/host/frontend/src/{navigation.ts, apps.ts, routes/dashboard.tsx,
     components/app-spotlight.tsx, catalogs/admin.ts}`;
   - the route guard and `guardPermissions`.
3. **Docs:**
   - the Planning reference and user pages as models;
   - the docs check's `sources` rule, with `bun run tools/docs/check-coverage.ts --list`
     run to see which pages A's touched paths pull in (`srv/config` is a source of
     `admin/authentication.md`, `apps/host/frontend` of six user overviews);
   - the install and deploy lists of boundaries step 13;
   - the overview pages that list modules.

## Task A2 — The seam, by hand (sequential, before any lane)

Every lane compiles against this. It is done by the orchestrator, not delegated.

1. **Contracts.**
   - `srv/contracts/catalog.go`:
     - `VariantEntry.Barcode`;
     - `CodeMatch {Variant VariantEntry; Packaging *PackagingEntry}` (with
       `PackagingEntry` declared now, empty of use until B);
     - the four methods of I22 (`VariantsOfProduct`, `LookupCode`, `SearchVariants`,
       `LockVariants`), with doc comments stating the lock and the order; the
       interface's own comment no longer calls the whole port read-only.
   - `srv/contracts/references.go`: `VariantReferenceHolder`, `VariantHold`, and the
     comment of rule 8's kind.
2. **The slot.**
   - `srv/module/module.go`: `VariantReferences func(Deps) contracts.VariantReferenceHolder`,
     plus the collected `Deps.VariantReferenceHolders`.
   - `compose.go`: collected from every module, enabled or not, exactly as
     `CustomerReferences`.
   - `modtest.WithVariantReferenceHolders`.
   - `compose_test.go`'s fakes gain the methods.
3. **The migration.** `mig/00076_inventory_baseline.sql`, holding the tables of spec
   §3.1–3.5 and §3.7 and their indexes, in the house style of `00072`. One root per
   site is a partial unique index; there is no FK from the site to its root.
4. **The contract.**
   - `openapi/inventory.yaml`: every A row of spec §6, plus `InventoryProblem` with
     `code` and `lines`. Every request schema `additionalProperties: false`; operation ids
     as listed.
   - `openapi/products.yaml`: the delete's 409, as Products' bare problem (no code).
   - Write the YAML by script, inserting new schemas **before `info:`**, and never run
     `contract normalize`, which reformats every file.
5. **Generation.**
   - `srv/openapi/gen/cfg-inventory.yaml`;
   - two lines in `apps/server/generate.go`;
   - `srv/inventory/sqlc.yaml` and `queries/{sites,locations,ledger,events,stats}.sql`
     (the queries the lanes need, written now so both server lanes share one generated
     store);
   - `go generate ./...`;
   - `bun run gen:client`, with `tools/openapi/gen-client.ts` gaining the module and its
     test.
6. **Registration** (boundaries steps 1–6):
   - `apps/server/cmd/vantigo/main.go`;
   - `srv/openapi/openapi.go` `Modules`;
   - `srv/config/config.go`: known, plus `inventory requires products`, with
     `config_test.go`;
   - `srv/db/schema_test.go` `moduleSchemas`;
   - `compose_test.go`'s `Modules` literal;
   - `apps/server/.golangci.yml`: inventory's depguard set, and the pair added to every
     other set.
7. **The module skeleton.**
   - `srv/inventory/{module.go, server.go, authorize.go, errors.go (every §4 code),
     strictbody.go, contractscalls.go, events.go, stubs.go}`.
   - `strictbody.go` is copied from Planning's, with Inventory's own `closedSchemas` and
     `closedItems` (the `lines` arrays and their items).
   - `contractscalls.go` holds the pre-transaction accessors, plus a separate
     `lockVariants(ctx, tx, ids)` that is the one call allowed inside a transaction
     (spec I22). The harness's "no contract call under an open transaction" check
     exempts exactly that one.
   - `stubs.go` (new; Planning's was deleted when its lanes finished) answers every
     operation with a typed 503, so the generated strict interface is implemented and
     the server compiles. The lanes replace the stubs file by file and delete
     `stubs.go` last.
   - `module.go` declares the six permissions with the displays of §5, refuses to mount on
     a nil `Products`, and sets `VariantReferences` to a holder answering nil (the ledger
     lane fills it).
   - `main_test.go` and `harness_test.go`: `contracttest.NewForModule("inventory")`, plus
     a `fakeCatalog` in `fakes_test.go` with seeded variants and a `LockVariants` that
     records its calls.
8. **The frontend skeleton.**
   - `apps/inventory/frontend` with `package.json` (`@vantigo/inventory-ui`),
     `eslint.config.js` (the `no-restricted-imports` block, and the package added to
     every other package's deny list), `tsconfig`, `src/index.ts`, `src/i18n.ts`
     (en/nb), the route path constants, and a placeholder home page.
   - The host: `moduleKeys`, an `apps.ts` entry, and `routes/inventory.tsx`.
   - Done so the app lane and the host lane compile against one set of exports.
9. **Gates.** `mise run server:check` and `bun run frontend:check` pass on the seam.
   Commit it locally as a WIP commit (squashed into the feature commits at Task A7).

## Task A3 — Products' side (server and app; in parallel with A4 and A5)

**Files:**
- `srv/products/catalog.go` and `catalog_test.go`;
- `srv/products/queries/products.sql` (`CatalogVariantsOfProduct`, `CatalogLookupCode`,
  `CatalogLockVariants … FOR SHARE ORDER BY id`, `LockVariantForDelete … FOR UPDATE`);
- `srv/products/variants.go` (the delete in a transaction);
- `products.go` (the type change refused while stock exists: lock every variant of the product `FOR UPDATE ORDER BY id`, ask the holders, 409 *Product has stock*);
- `srv/products/variants_test.go`;
- `apps/products/frontend/src/pages/products.$productId.tsx` and its delete dialog
  component, its i18n, and its tests.

**What to build:**
- `LookupCode`: SKU with `lower(sku) = lower($1)`; barcode exact. It answers every match,
  never an error for none.
- `SearchVariants`: SKU, product name and barcode, `ILIKE` as the product list does, at
  most 50, goods only.
- `LockVariants`: `SELECT … FROM product_variants v JOIN products p … WHERE v.id = ANY($1)
  ORDER BY v.id FOR SHARE OF v, p`, on the given `tx`.
- The delete:
  1. `BEGIN`.
  2. Lock the variant `FOR UPDATE`, or 404.
  3. The last-variant 409.
  4. For each `Deps.VariantReferenceHolders`, `VariantHeld(ctx, tx, id)`. The first
     non-nil answer gives a 409 titled *Variant has stock*, with I23's detail built from
     `Quantity`, the variant's own unit and `Locations`. The app recognises it by title
     and builds its own translated message from the numbers it reads from Inventory.
  5. Delete.
  6. Commit.
- Tests:
  - a fake holder holding and not holding;
  - the race: a goroutine holding `FOR SHARE` through `LockVariants` blocks the delete
    until it commits.
- The app:
  - `extraPanels?: ReactNode`, drawn after the variants card;
  - the delete dialog shows the translated *Variant has stock* message, with the
    quantity, and a link to `/inventory/variants/$variantId` when the module is on (the
    host passes `stockHref`).

## Task A4 — Inventory server, lane 1: sites and locations (parallel with A3, A5)

**Files:**
- `srv/inventory/{sites.go, locations.go, codes.go, tree.go, bulk.go, lookup.go
  (location half), events.go}`;
- their `_test.go` files.

**What to build:**
- `codes.go`: normalisation, validation and the full-code join. Pure, table-tested.
- `tree.go`:
  - `ancestors`/`depth` maintenance;
  - the subtree rewrite of `full_code`, `ancestors` and `depth` on a code change or
    re-parent, as one `UPDATE … WHERE id = $1 OR ancestors @> ARRAY[$1]`;
  - collision detection before the write.
- `bulk.go`: expanding up to three levels with padding from the start's width; at most
  1000; all-or-nothing, listing the collisions.
- Every structural write takes the spec's §4 tree lock order: the site row `FOR
  UPDATE`, then the affected locations `ORDER BY id FOR UPDATE`; a create shares its
  parent.
- Sites:
  - creating one creates its root in the same transaction;
  - a code rename runs the subtree rewrite;
  - archive and delete per I6.
- Locations:
  - create, update with If-Match (rename, recode, kind, name, sort, status, parent);
  - archive cascades to the subtree after the stock check, under `FOR UPDATE` on the
    subtree (§4);
  - reactivate only itself, with an active parent;
  - delete only when no movement line names it or a descendant (`EXISTS` over the
    locked subtree);
  - `location_has_open_count` is a no-op until B, with a TODO naming B.
- The lookup's location half: the `LOC\d+` token, then the full code.
- Every change writes `inventory.events`.
- Tests: every refusal in the §4 rows marked I1–I6, I21; the depth limit; a re-parent of
  a subtree with stock leaving the balances untouched.

## Task A5 — Inventory server, lane 2: the ledger and the reads (parallel with A3, A4)

**Files:**
- `srv/inventory/{quantity.go, posting.go, adjustments.go, moves.go, receipts.go,
  balances.go, stock.go, movements.go, lookup.go (variant half; the two halves meet in
  `lookupInventory`, which lane 2 owns, calling lane 1's `lookupLocations`), holder.go,
  stats.go}`;
- their tests.

**What to build:**
- `quantity.go`: I11's conversion of the `float64` the strict handler receives into a
  `big.Rat`, copied from Invoices' `ratFromFloat` and `decimalPlaces`; at most three
  decimals; the bounds. Table-tested, including 0.1 and 99,999,999,999.999.
- `posting.go` is the one engine, `post(ctx, tx, movement, lines, opts)`. The API
  handlers open and commit the transaction; B's count post and import call it inside
  theirs. `opts.maxLines` is 500 for the API and unbounded for internal callers. Steps:
  1. Look up the `clientRequestId` with a plain `SELECT`. A hit compares `request_hash`
     (SHA-256 of the canonical body) and answers the first movement, or
     `client_request_reused`.
  2. Validate the shape of every line, collecting all failures.
  3. `lockVariants` (the catalog, on `tx`).
  4. Claim the key with `INSERT … ON CONFLICT DO NOTHING RETURNING`. A conflict means a
     concurrent attempt committed; read it back and answer it as in step 1.
  5. Locations `FOR SHARE` in id order: exist, active, same site. Nothing is written to
     a location.
  6. `INSERT … ON CONFLICT DO NOTHING` the missing balances at zero, then all balances
     `FOR UPDATE` in (variant, location) order.
  7. Apply the lines in order, checking for negatives after each.
  8. Write the movement and its lines.
  9. Delete the zero balances.

  It answers `lines: [{index, code, detail}]` for every failure.
- The three endpoints map their bodies to engine lines (I15). An adjustment in `set` mode
  reads the locked balance to compute the difference.
- `holder.go`: `VariantHeld` sums the variant's balances on the caller's transaction and
  answers `{quantity (a decimal string), locations}`, or nil.
- Reads:
  - contents with the subtree toggle;
  - product stock, through `VariantsOfProduct`, then balances grouped by site;
  - variant stock;
  - movements with the cursor (`(posted_at, id)` descending);
  - stats.
- Variant refs are resolved through `Variants(ids)` in one call per response, with the
  deleted fallback (spec §6).
- Tests:
  - every I8–I15 refusal;
  - idempotency: same body gives 200 and the same id; a different body gives 409;
  - a receipt racing an archive;
  - two receipts to a new (variant, location) pair, concurrently: both land, no 23505;
  - two postings to one location, concurrently: no deadlock;
  - the rebuild helper (`assertLedgerConsistent(t)`) after every ledger test;
  - `contracttest.RequireCoverage`.

## Task A6a — The app, `@vantigo/inventory-ui` (after A2; parallel with A3–A5)

**Files:** under `fe/`:
- `pages/{home, lookup, site, location, variant, movements, movement, new-adjustment,
  new-move, new-receipt}.tsx`;
- `components/{lookup-field, location-tree, location-picker, variant-picker (on
  `GET /inventory/variants?q`),
  line-editor, product-stock, stock-table, movement-list, bulk-create-dialog,
  location-form, site-form}.tsx`;
- `api.ts` (query options against the generated schema), `i18n.ts` (en and nb), and the
  tests.

**What to build:**
- The line editor:
  - the scan field adds or increments on Enter;
  - `clientRequestId` comes from `crypto.randomUUID()` when the form mounts and is kept
    across a failed submit, and reset only after success;
  - rows are marked from the problem's `lines`.
- `product-stock.tsx` is `<ProductStock productId>` (I27), exported from `index.ts` with
  `<InventoryLookupField>`.
- The tree renders lazily by level and searches codes on the client below 2000 nodes,
  through `?q` on the server above that.
- **nb words:**
  - Lager (the module), lagersted (site), lokasjon (location), hylle, reol, gang, sone,
    salgsflate, bakrom;
  - beholdning (on hand), varemottak (receipt), justering, flytting, varetelling (B);
  - "Finn lokasjon eller vare" (the lookup).
- Tests: the line editor's scan and increment, the problem's mapping, the lookup's
  single-match navigation, and the panel's three states.

## Task A6b — The host (after A2; parallel with A6a)

**Files:**
- `apps/host/frontend/src/{apps.ts, navigation.ts, i18n.ts, catalogs/admin.ts (+ test),
  routes/inventory*.tsx, routes/products/$productId.tsx, routes/dashboard.tsx,
  components/app-spotlight.tsx (+ test)}`.

**What to build:**
- Sidebar entries: Overview, Sites, Movements, and Receipts (the movements list
  filtered by `kind=receipt`, §13.3). Each declares its permissions;
  `guardPermissions` applies where a page opens wider.
- The product route passes `extraPanels={<ProductStock …/>}` and `stockHref` only when
  `enabledModuleKeys().includes("inventory")` and the user holds `inventory:stock-view`.
- The dashboard card (stock lines, receipts this week), gated on the module and
  `stock-view`.
- The spotlight's quick action *Find location or item*.
- `catalogs/admin.ts`: the six keys in en and nb, **equal to `module.go`'s
  descriptions**; a test pins them.

## Task A7 — Docs (after A3–A6; in parallel with the reviews)

Two opus documentation lanes:

1. **The user guides**, en then nb, page for page:
   - `user/inventory.md`: sites, locations, bulk add, the lookup, receive, adjust, move,
     the product's Stock card;
   - `user/products.md`: the Stock card, and why a variant with stock cannot be deleted.
2. **Reference, administration and the rest:**
   - `reference/inventory.md`, written from the code: model, rules, codes, permissions,
     the lock order, §12's kept rules;
   - `reference/products.md`: the catalog methods, the slot, the 409;
   - `admin/installation.md` en and nb (`GRANT`, `MODULES`);
   - `contributing/module-boundaries.md`: the `VariantReferences` rule, the rule for a
     locking read on the caller's transaction (spec I22), and the adding list;
   - `deploy/compose/{vantigo.env.example, README.md}`, `README.md`, `CONTRIBUTING.md`;
   - `docs/astro.config.mjs` `contracts`;
   - the overview pages;
   - `ROADMAP.md` per spec §8.

Every new page's `sources` names its paths.

## Task A8 — Reviews, gates, commits, PR

1. **Reviews.** Two opus reviews in parallel, the server (A3–A5) and the apps (A3's app
   part, A6a, A6b), each followed by a fix pass. Deltas that change docs are sent to the
   docs lanes.

   Things the server review must check:
   - the lock orders of §4 in every path (postings, tree writes, counts), and no write
     to a location row from a posting;
   - no float arithmetic on quantities;
   - every refusal answering all lines;
   - the holder on the caller's transaction;
   - no SQL outside `inventory`.
2. **Gates**, run one at a time (run concurrently, the test suites have overrun the
   600 s timeout):
   - `mise run server:check`;
   - `mise run server:test`;
   - `mise run frontend:check`;
   - `bun run tools/docs/check-coverage.ts --base origin/main`, which reads commits
     only, so commit first or snapshot with `git commit-tree`.
3. **Commits**, in Conventional Commits, each ending with the attribution line:
   - `feat(products): the catalog reads Inventory needs, and a variant with stock cannot
     be deleted (Inventory A)`;
   - `feat(inventory): sites, locations and the stock ledger (Inventory A)`;
   - `feat(inventory-ui): the Inventory app, the product's Stock card and the lookup
     (Inventory A)`;
   - `docs(inventory): …`.
4. **The PR** against `main`. Then bind it, read CI, and update the memory.

---

# Delivery B — packagings, counts, labels, the import, the list column

B is cut from `main` after A has merged. Run Task B1 again, as A1 restricted to what A
built.

## What B ships

- **Products' packagings** (I24): table, endpoints, the variant response, the editor;
  `LookupCode` matching packaging barcodes; `VariantEntry.Packagings`.
- **Entering in packagings** on every Inventory line (I25), and the breakdown (I26).
- **Counts** (I16–I20), with `location_has_open_count` made real, `openCounts` and the
  `countOpenLong` attention item.
- **Location labels** as PDF (I7).
- **The opening-balance import** (I32).
- **`GET /stock` and the product list's column** (I33).
- **Docs.**

## Task B2 — The seam, by hand

1. **Migrations.**
   - `mig/00077_products_packagings.sql` (spec §3.8), which Products' `sqlc.yaml` gains;
   - `mig/00078_inventory_counts.sql` (§3.6, plus `movement_lines.entered` and
     `movements.count_id`).
2. **The contract.**
   - `openapi/products.yaml`: the packaging operations and the `packagings` field on
     variants.
   - `openapi/inventory.yaml`:
     - the B rows of spec §6;
     - `entered` on every line input (`{packagings: [{packagingId, count}], loose}`,
       exclusive with `quantity`);
     - `packagings` on `InventoryVariantRef`.
   - Inserted by script, never normalized.
3. **`contracts.PackagingEntry`** `{ID, Name, Kind, Quantity, Barcode, SortOrder}`;
   `VariantEntry.Packagings`.
4. **Generate**, then stub the new operations.
5. **The app's route constants and sidebar entries** (Counts, Import).
6. **Gates;** a WIP commit.

## Task B3 — Products' packagings (server and app; parallel with B4, B5)

**Server:**
- `srv/products/{packagings.go, packagings_test.go, queries/packagings.sql}`, plus the
  variant response and `catalog.go` (packagings loaded with variants; `LookupCode` adding
  the packaging match).
- The derivation:
  - `quantity = containsCount × contained.quantity`;
  - a change recomputes dependants in the same transaction, in dependency order;
  - cycles and in-use deletes are refused.
- The barcode check under `pg_advisory_xact_lock(hashtext('products:barcodes'))`, taken
  by `variants.go`'s writes too.

**App:**
- The packagings editor: a small table on the variant with name, kind, "contains" (a
  packaging × count, or a quantity in the unit), GTIN and dimensions.
- Shown on the single-variant card and wherever B1's dossier finds multi-variant
  editing.
- Refusals are recognised by their titles (*Packaging cycle*, *Packaging in use*,
  *Barcode taken*), since Products' problems carry no code.
- The product list's `extraColumns` prop.

## Task B4 — Inventory server: packagings on lines, and counts (parallel with B3, B5)

**Files:**
- `srv/inventory/{entered.go, counts.go, count_lines.go, count_post.go}`;
- `posting.go` (accepting `entered`);
- `locations.go` (the real `location_has_open_count`);
- `stats.go`;
- their tests.

**What to build:**
- `entered.go`: resolves `entered` against the packagings `LockVariants` answered, in
  the same transaction, computes the base quantity, and returns the snapshot JSON.
- Counts:
  - open: lock the site row `FOR UPDATE`, run the overlap check on the subtrees of open
    counts, then freeze the expected lines;
  - line upsert, with an `added` line for an unknown pair;
  - post: lock the count row `FOR UPDATE`, then call `post(ctx, tx, …)` (which locks the
    variants, then the balances), flag `moved`, honour `acceptMoved` and `uncounted`,
    write one `count` movement, set the count's `movement_id`;
  - cancel.
- Tests:
  - the overlap check;
  - a movement during counting flagging its line;
  - posting `zero` against `keep`;
  - a first count whose lines are all `added`;
  - the ledger consistency helper.

## Task B5 — Labels, the import, the totals (parallel with B3, B4)

**Files:**
- `srv/inventory/{labels.go, labels_test.go, import.go, import_test.go, totals.go}`.

**Labels:**
- maroto, with the Code 128 code column (boombuler's encoder, as `srv/payments/qrsvg`
  uses its QR), embedded fonts as `srv/invoices/pdf.go`.
- Layouts: Avery L7160 (21 per A4) and L7163 (14 per A4) (§13.2).
- Deterministic output, tested by page count and by the encoder's inputs (the tokens
  handed to the barcode column), not by bytes. boombuler has no decoder.

**The import:**
- `srv/fileimport` reads the file.
- Rows are resolved by full code and by SKU (`LookupCode`).
- The dry run answers per-row outcomes.
- The commit posts **one** movement through `post(ctx, tx, …)` with no line cap.
- The client key is derived from the file's hash and the site, so re-uploading the same
  file is idempotent.

**`GET /stock?variantIds`:** at most 100 ids.

## Task B6a — The app (parallel with B3–B5, after B2)

- The line editor gains packaging steppers (one per packaging plus *loose*), and a scan of
  a packaging GTIN adds one of it.
- `breakdown.ts`: greedy, largest first, tested. It is shown on the panel, the contents
  and the counts.
- Counts:
  - the list;
  - open (scope picker);
  - the counting screen, ordered by location full code: scan a location to jump to it,
    scan an item to focus or add its line, type or step the counted quantity, saved in
    batches of up to 500;
  - review: the differences, the `moved` flags, the choices, post.
- The import page: upload, the dry-run table, commit.
- *Print labels* on the site and location pages (selected locations, or the subtree),
  downloading the PDF.
- `useStockTotals` for the host.

## Task B6b — The host

- The product list passes `extraColumns`.
- The sidebar gains Counts and Import.
- The dashboard's attention item `countOpenLong`.
- The spotlight's *Start count* quick action.

## Task B7 — Docs; Task B8 — Reviews, gates, commits, PR

- **Docs**, as A7:
  - the user guides for packagings (Products) and for counts, labels and the import
    (Inventory), en and nb;
  - the references;
  - ROADMAP: phase 1 marked done, and Products phase 5's packagings marked delivered.
- **Reviews, gates, commits and PR**, as A8. The commits:
  - `feat(products): packagings — boxes and pallets (Inventory B)`;
  - `feat(inventory): counts, packagings on lines, labels and the opening-balance import
    (Inventory B)`;
  - `feat(inventory-ui): …`;
  - `docs(inventory): …`.

---

## After phase 1

In the order the roadmap will list it, each a research and spec round of its own (spec
§11 and §12):

1. **With Orders:**
   - transfers between sites (*sent → in transit → received*);
   - reservations per site, and `contracts.Stock` plus `StockLedger`;
   - the domain-events outbox;
   - an issue movement kind.
2. **With Point of sale:** the store flags, and allowing negative stock per site.
3. **With Manufacturing:**
   - lots, serials and expiry (the Products tracking mode, `lot_id` on balances and
     lines);
   - the `production` kind;
   - FEFO and the trace screens.
4. **Placement**, primary location and min/max, and units of measure beyond packagings
   (Products phase 5).

## Gotchas carried from earlier deliveries

- A fresh worktree has no `node_modules`. Run `bun install --frozen-lockfile` before
  `docs:check` or the frontend tests.
- `go run ./internal/openapi/cmd/contract normalize` reformats every contract. If it was
  run, `git checkout -- openapi/` and re-apply.
- `oapi-codegen` needs nullable fields listed in `required` to emit `null`. Two enums
  sharing a value get type-prefixed constant names.
- The access grammar is all-of. `manage` never implies `view`, so every write lists
  `stock-view` too.
- `Docs-Impact: none` anywhere in a branch waives every coverage finding.
- Products' problems never carry a `code` (`srv/products/errors.go`). Tell its 409s
  apart by title.
- A contract call runs before a transaction opens, except `LockVariants` (spec I22).
- The permission descriptions in `catalogs/admin.ts` must equal `module.go`'s; a test
  pins them.
- Under Node 26, the projects localStorage tests fail locally only. The pre-commit hook
  runs tree-wide.
- In zsh, a gate loop must run `bun run $c` with the subcommand alone in `$c`.
