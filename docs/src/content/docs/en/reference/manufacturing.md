---
title: "Manufacturing module"
description: "How the business makes its goods: the module and the two modules it requires; the five permissions and the cost shaping; work centers — code, site, capacity, time efficiency, setup and cleanup, the rates, the weekly working pattern on a 15-minute grid and the closures, archive, reactivate and delete; bills of materials — the product, the output quantity, lines with scrap, operations at work centers on one site, the client keys that link them, the replace-on-edit under a revision, the default and the alternatives, archive and the promotion of the next default; the lock order — LockVariants, then the BoM graph's advisory lock; the cycle and depth walk; the overview — requirements rounded up, made and bought, availability, M28a's expected minutes and the rolled-up cost; the VariantReferenceHolder; personal data; the refusals, the switch and the endpoints."
sources:
  - apps/server/internal/manufacturing
  - apps/manufacturing/frontend
  - openapi/manufacturing.yaml
  - apps/server/internal/openapi/specs/manufacturing.yaml
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/host/frontend/src/routes/manufacturing
---
The Manufacturing module describes how the business makes its goods: the **work centers**
where the work is done, with their working hours, and the **bills of materials** (BoMs)
that say which components and which **operations** make one variant. In Norwegian the app
is **Produksjon**. The research is
[`2026-11-11-manufacturing-module.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/research/2026-11-11-manufacturing-module.md)
and the design
[`2026-11-11-manufacturing-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-11-manufacturing-design.md)
(decisions M*n*; its §15 amendments, such as M10a, win where they differ). This page
describes delivery A: the module, work centers, bills of materials, the BoM overview and
the holder. Delivery 0, the seams in Inventory and Products this module builds on, is
described in [Inventory](/en/reference/inventory/#production) and
[Products](/en/reference/products/).

## The module

The module is `manufacturing` (M6), its schema `manufacturing` (migration
`00156_manufacturing_baseline.sql`), its API `/api/v1/manufacturing` from
`openapi/manufacturing.yaml` and its frontend `@vantigo/manufacturing-ui`
(`apps/manufacturing/frontend`). It is a vertical-slice module inside the single Vantigo
binary (`apps/server/internal/manufacturing`).

It reads the catalog through `contracts.ProductCatalog` — searching goods variants, and
locking every variant a BoM write names (M10a) — and the sites and stock through
`contracts.Stock`: `Sites` and `SiteLocations` for the site a work center stands at, and
`Available` for the overview. `MODULES` therefore refuses `manufacturing` without
`products` or `inventory` (`manufacturing requires inventory`, naming the one missing).
Orders is an optional partner of a later delivery; nothing in this delivery reads it.

The schema holds `counters`, `work_centers`, `work_center_closures`, `boms`,
`bom_operations` and `bom_lines` (§9). `boms` carries a partial unique index on
`variant_id` where the BoM is the default and active, which makes "one default per
variant" a database fact.

The app's sidebar has, in order (M7), **Overview** (`/manufacturing`), **Bills of
materials** (`/manufacturing/boms`) and **Work centers** (`/manufacturing/work-centers`).
Its Norwegian labels are *Oversikt*, *Stykklister* and *Arbeidsstasjoner*. The Overview
reads `GET /stats`: the active work centers, the blocked ones and the active BoMs.

## Permissions

| Key | Display (en / nb) | Covers | Sensitive | Delegable |
| --- | --- | --- | --- | --- |
| `manufacturing:access` | Use Manufacturing / Bruke Produksjon | See the app; read the meta, the stats, the sites, the variant search, work centers and their closures, BoMs and their overview | no | yes |
| `manufacturing:boms-manage` | Manage bills of materials and work centers / Administrere stykklister og arbeidsstasjoner | Create, update, archive, reactivate and delete work centers; add and delete closures; create, update, archive and reactivate BoMs and make one the default | no | yes |
| `manufacturing:orders-manage` | Manage manufacturing orders / Administrere produksjonsordrer | Declared now; first asked for by delivery B | no | yes |
| `manufacturing:shop-floor` | Work on the shop floor / Arbeide i verkstedet | Declared now; first asked for by delivery C | no | yes |
| `manufacturing:costs-view` | See manufacturing costs / Se produksjonskostnader | Every cost field in a response, and writing a work center's rates | **yes** | **no** |

Every read is checked against `manufacturing:access`; every write against
`manufacturing:access` **and** `manufacturing:boms-manage`, so a role that gives
`boms-manage` without `access` gives nothing. The Assistant calls the writes in its confirm
mode. The stock figures are Manufacturing's own reads of `contracts.Stock` and need no
Inventory key (§8); the variant search is Manufacturing's own (`GET /variants`) and needs
no Products key. `GET /meta` answers the caller's `canManageBoms`, `canSeeCosts`,
`canManageOrders` and `canWorkShopFloor`, and the currency, `NOK`.

### Cost shaping

Without `manufacturing:costs-view` every cost field is **left out** of the response —
absent, not `null` (M38): a work center's `machineCostPerHour` and `labourCostPerHour`, a
variant's `standardCost`, an overview node's `standardCost` and `extendedValue`, an
operation's `cost`, and the overview's `rolledUpCost` and `costIncomplete`. A caller
without the key who sends either rate on a work-center write — a value or `null` — is
refused **403** `costs_view_required`, before any other field is judged. On an update a rate left out keeps the stored value, and `null`
clears it — so a person without the key saves a work center and its rates stay.

## Work centers

A work center (M8; nb *arbeidsstasjon*) is a machine, a cell or a team where an operation
is done.

| Field | Rule |
| --- | --- |
| `code` | 1–16 characters of `[A-Z0-9]` after upper-casing, stored upper-case, unique (`work_center_code_taken`, 409) |
| `name` | 1–200 characters |
| `siteId` | An active Inventory site, read through `Stock.Sites` (`site_unknown`, 400, on `siteId`); the response adds `siteCode` and `siteName`. It does not change while an active BoM has an operation at the work center ([One site](#operations)) |
| `capacity` | Units processed in parallel per cycle; an integer of 1 or more, default 1 |
| `timeEfficiency` | A percentage from 1 to 500, default 100; at 50 the work takes twice as long |
| `setupMinutes`, `cleanupMinutes` | Decimals of 0 or more, default `"0"`, applied once per work order |
| `machineCostPerHour`, `labourCostPerHour` | Exact decimals in NOK, optional and nullable; [cost-shaped](#cost-shaping) |
| `pattern` | The weekly working pattern (M9) |
| `workingState` | `normal` or `blocked`; changed only through block and unblock, never by an edit (M30, a later delivery) |
| `status` | `active` or `archived` |
| `revision` | The optimistic-concurrency token; a stale one is `revision_conflict` (409) |

A work center is ordered by code in its list, which filters by `status` (`active`, the
default, `archived` or `all`), `siteId` and a search `q` on its code or name.

### The weekly working pattern

The pattern (M9) is the work center's calendar: per weekday, `monday` to `sunday`, at most
**two intervals** `{start, end}` as `HH:MM` local times in the workspace zone
(`contracts.BusinessCalendar`).

- Every time is on a **15-minute grid**.
- `start` is before `end`; `24:00` is allowed as an `end`.
- A day's two intervals do not overlap.
- The default is Monday to Friday `[{07:00, 15:00}]`, Saturday and Sunday `[]`.

The capacity of a day is the length of its open intervals. Norwegian public holidays
(`holidays.PublicHoliday`) are closed days, and so are the work center's closures. A
pattern that rotates week by week is later (§13).

### Closures

A closure is a date range `{fromDate, toDate, note}` with `toDate` on or after `fromDate`
(a field error otherwise), listed by `fromDate`. A work center keeps **at most 100**
(`closures_too_many`, 400). Closures are added and deleted, never edited.

### Archive, reactivate, delete

- **Archive** (`POST /work-centers/{id}/archive {revision}`) sets `archived`. An archived
  work center is not offered to a new operation (`work_center_unknown` names it on a BoM
  write); the BoMs whose operations name it keep it. From delivery C, archiving is refused
  while an open work order names it (`work_center_has_open_work`, 409).
- **Reactivate** sets `active` again, and is refused **409** `site_archived` when its site
  is no longer an active Inventory site. An update keeps a site that was archived since;
  only a change of `siteId` must name an active one (`site_unknown`, 400).
- **Delete** (`DELETE /work-centers/{id}?revision=`) is allowed only while no BoM
  operation names the work center; otherwise **409** `work_center_in_use`, and the answer
  is to archive it. It takes the BoM graph's lock before the work center's row, so it
  cannot pass a BoM write that is about to name the work center.

## Bills of materials

A BoM (M10; nb *stykkliste*) tells how to make one variant.

| Field | Rule |
| --- | --- |
| `variantId` | The variant made: goods, judged from `LockVariants` (`variant_not_goods`, `variant_not_found`, 400); fixed at create |
| `code` | Optional, 1–32 characters after trimming, unique among BoMs when given (`bom_code_taken`, 409); an empty or blank code is no code, `null` |
| `outputQuantity` | More than 0, at most three decimals (`quantity_invalid`); lines and operation times are stated per it |
| `isDefault` | Exactly one active BoM per variant is the default (M14a); asked for at create only, and the variant's first active BoM is the default whatever it says |
| `makeToOrder` | False by default; read by delivery E |
| `leadTimeDays` | An integer of 0 or more, default 0; read by later deliveries |
| `note` | At most 2,000 characters |
| `status` | `active` or `archived` |
| `revision` | The concurrency token (`revision_conflict`, 409) |

A response also carries `siteId`: the site of its operations' work centers, or `null`
for a BoM without operations. A variant may have several active BoMs, for alternative
recipes. The list filters by `variantId`, `status` (`active` by default), `q`, `limit`
(default 50) and `offset`, newest change first, and answers the `total`. `q` matches a
BoM's code or note, or its variant's SKU or product name in the catalog (the first 200
catalog matches, inactive products included). A `limit` outside 1–200 is clamped to that
range rather than refused, and a negative `offset` is 0.

### Lines

At most **200** per BoM (M11; `too_many_lines`, 400). Each has:

- a component `variantId`, which must be goods (`variant_not_goods`, `variant_not_found`,
  the line's code);
- a `quantity` per `outputQuantity`, more than 0 with at most three decimals
  (`quantity_invalid`), in the component's own unit;
- a `scrapPercent` from 0 to 99.99, default `"0"`: the expected loss, added to the
  requirement;
- an optional `operationKey`, naming the operation that consumes the component
  (`operation_key_unknown` when no operation in the request has that key); the response
  carries `operationId` and `operationKey`;
- a `sortOrder` and a `note` of at most 2,000 characters.

The same component may appear on two lines.

### Operations

At most **50** per BoM (M12; `too_many_operations`, 400). Each has:

- a `key`, 1–40 characters and unique in the request: the client key lines refer to, so
  that lines and operations are created in one request. The response gives each
  operation its `id` as text for its `key`; an input operation that names an existing
  operation's `id` keeps it;
- a `sequence` — operations run in sequence order; two operations of one BoM with the
  same `sequence`, or the same `id`, are refused as field errors on the second;
- a `name`, 1–200 characters;
- a `workCenterId` for an **active** work center (`work_center_unknown`, 400);
- `setupMinutes`, optional and nullable: when empty, the work center's setup applies;
- `minutesPerCycle`, more than 0: the time of one cycle of the work center's capacity;
- `instructions`, Markdown of at most 20,000 characters.

**One site (M12a).** A BoM whose operations name work centers at more than one site is
refused **400** `work_center_other_site`. The rule holds from the work center's side too:
an update that changes a work center's `siteId` while any **active** BoM has an operation
at it is refused **400** `work_center_other_site` on `siteId`. Archive those BoMs, or move
their operations to another work center, first; an archived BoM does not hold the site.
The update takes the BoM graph's lock before the work center's row, as every BoM write
does before it reads the work centers its operations name, so a site change and a BoM
write cannot pass each other.

### Editing

An update (M13; `PUT /boms/{id}`) **replaces** the BoM's lines and operations in one
request, under its `revision`. `variantId` and `isDefault` are not part of it: the product
is fixed, and the default changes only through make-default and archive. Removing an
operation a line referred to clears that line's operation. An archived BoM is not edited
(`bom_not_active`, 409).

A 400 names the failing fields in `errors`, keyed by camelCase path —
`lines[3].quantity`, `operations[1].workCenterId` — and a line's own refusals in `lines`,
each with its `index`, `code` and `detail`.

### The default, archive and reactivate

- The first active BoM of a variant becomes its **default**, whatever `isDefault` says. A
  create with `isDefault` true makes it the default and unmarks the old one in the same
  transaction.
- **Make default** (`POST /boms/{id}/make-default {revision}`) does the same for an
  existing BoM; it runs the [cycle and depth walk](#the-cycle-and-depth-walk) again
  (M14a), and is refused for an archived BoM (`bom_not_active`, 409).
- **Archive** sets `archived`. Archiving the default **promotes the oldest other active
  BoM** of the variant to the default, or leaves the variant with none (M14a). The
  candidate is walked first ([the cycle and depth walk](#the-cycle-and-depth-walk)): when
  it would now close a cycle or go more than 10 levels deep, it is not promoted and the
  variant is left with no default — the archive itself is not refused, and no younger BoM
  is tried. Make one the default by hand once its components are put right.
- **Reactivate** sets `active` again. It judges the product and the components again
  (`variant_not_found`, `variant_not_goods`), and the operations' work centers again: one
  archived while the BoM was is `work_center_unknown`, and work centers moved meanwhile
  so that they stand at more than one site are `work_center_other_site` (both 400). It
  re-runs the cycle and depth walk, and makes the BoM the default when the variant has no
  active default.

A unique-index violation on the default — two writers promoting at once — is answered
as **409** `revision_conflict` (M14a), and the client reloads.

## Locks

Every BoM write — create, update, archive, reactivate and make default — takes its locks
in one order, on its own transaction:

1. **`ProductCatalog.LockVariants`** first (M10a), with the BoM's variant and every
   component variant id. The goods-or-service judgement comes from what `LockVariants`
   answers, so a Products write that would turn a variant into a service waits for the
   BoM write or is seen by it, which keeps the [holder's](#the-variant-holder) answer
   true (rule 20 of the [module boundaries](/en/contributing/module-boundaries/)).
2. **`pg_advisory_xact_lock(19782, 1)`** (M14a): 19782 is `0x4D46`, "MF", the
   manufacturing class key, and key 1 is **the BoM graph**. It serialises the structural
   writes, so two writes cannot each pass the cycle walk and together close a cycle. (A
   later delivery locks `(19782, workCenterID)` for planning.)
3. Then the module's own rows.

The lock is released when the transaction ends.

## The cycle and depth walk

M14 refuses a BoM when any component, at any depth, is the BoM's own variant. The walk
starts from the BoM's lines and follows each component through **its default active BoM**;
a component with none is a leaf.

- Meeting the BoM's own variant is **400** `bom_cycle`, and the problem's `path` lists the
  cycle as `[{variantId, sku}]`, from the BoM's variant back to itself.
- Walking past **10 levels** is **400** `bom_too_deep`.

The walk runs on create, update, make default and reactivate, and on the candidate an
archive would promote, under the graph's lock.

**The levels above count for a default.** Other BoMs explode through a variant's
default BoM only, so when the BoM written is, or becomes, its variant's default, the
walk also counts the default active BoMs **above** it: the longest chain of variants
whose default BoM consumes the variant, read upward a level at a time. The levels above,
the BoM itself and the levels below together go at most 10 deep; past that is **400**
`bom_too_deep`. It applies to a create that becomes the default (the variant's first
active BoM, or `isDefault` true), an update of the default, a reactivate that becomes the
default, a make default, and the promotion on archive. An alternative that is not the
default counts only the levels below it, since no other BoM's tree passes through it. So
under a chain of ten, a BoM for the bought variant at its bottom is refused, though it is
one level on its own.

## The overview

`GET /boms/{id}/overview?quantity=&siteId=` explodes a BoM (M15, M15a). `quantity`
defaults to the BoM's `outputQuantity`; `siteId`, when given, adds availability.

**The tree.** The root node is the BoM's variant at `quantity`. Each child's requirement
is

```text
quantityRequired = line.quantity × parentQuantity / outputQuantity × (1 + scrapPercent / 100)
```

rounded **up** (ceil) to three decimals (M18), where `parentQuantity` is the parent
node's `quantityRequired` and `outputQuantity` the parent's BoM's. A component with a
**default active BoM** is **made** (`made: true`, `bomId` set) and the walk recurses
through that BoM; one with none is **bought**, a leaf. Each node carries its `depth`, and
a made node below the 10th level is **400** `bom_too_deep` — which the
[walk](#the-cycle-and-depth-walk) keeps from happening on every write; the overview checks
again. More than **2,000 nodes** is **400** `bom_too_large`
(M15a); `nodeCount` answers the size. A `siteId` that is not an active Inventory site is
**400** `site_unknown`.

**Availability.** With `siteId`, every node answers `available` from
`Stock.Available(variantIDs, &siteID)`, with the variant ids deduplicated and batched at
most 1,000 per call (M15a); without it, `available` is `null`.

**Expected minutes (M28a).** Each operation of a made node, and of the root, answers

```text
cycles  = ⌈(quantity / outputQuantity) / capacity⌉
minutes = setup + cleanup + cycles × minutesPerCycle × 100 / timeEfficiency
```

rounded half-up to the minute, where `quantity` is the node's, `setup` the operation's
`setupMinutes` or else the work center's, and `cleanup`, `capacity` and `timeEfficiency`
the work center's.

**Operation cost.** `cost = expectedMinutes / 60 × (machineCostPerHour +
labourCostPerHour)`, a `null` rate counting as 0.

**Standard cost and extended value.** Every node answers its own `standardCost`, from the
catalog (delivery 0, M4), and `extendedValue = quantityRequired × standardCost`, `null`
when it has none.

**The rolled-up cost.** M15 reads "the components at standard cost, using the leaf's
standard cost when a made component has none of its own". The module reads it this way,
walking from the root's children:

- a **bought** node adds its `extendedValue`;
- a **made** node **with** a standard cost of its own adds its `extendedValue`, and the
  walk does not descend into it for cost: its standard cost stands for its subtree,
  operations included;
- a **made** node **without** one is descended into: its children and its own operations
  count.

`rolledUpCost.components` is the sum of those values; `rolledUpCost.operations` is the
cost of the root BoM's operations plus the operations of every made node the walk
descended into; `total = components + operations`. A bought node with no standard cost
counts 0 and sets `costIncomplete: true`. Writing the rolled-up cost back to the
product's standard cost is later (§13).

**Rounding and display.** Quantities are exact decimal strings, rounded half-up to three
decimals except the requirement's ceiling; amounts are computed exactly and rounded
half-up to two decimals at display, in NOK. On the wire a quantity, a time in minutes
and a scrap percentage are the shortest text that says the value — `"34"`, `"1.5"`,
`"0.125"` — while an amount (a rate, a standard cost, an extended value, an operation's
cost and the rolled-up cost) always carries two decimals: `"12.50"`. `expectedMinutes` is
an integer, at most 10^12; an operation whose expected time at the quantity passes it is
**400** `bom_too_large`. Every cost field is [cost-shaped](#cost-shaping).

## The variant holder

The module fills the many-provider slot **`Module.VariantReferences`** with a
`contracts.VariantReferenceHolder` (M16, M16a, rule 20 of the
[module boundaries](/en/contributing/module-boundaries/)). Products asks it inside its own
transaction, after locking the variants:

- for **delete** of a variant; and
- for **type change**, a Goods product turned into a Service, for every variant.

For either purpose, a variant named by one or more **active** BoMs, as their product or a
component, is held: `VariantHold{Kind: open_documents, Documents: n}`, where `n` is the
number of such BoMs. `VariantHold` carries no document list, so the answer is a count
(M16a). An archived BoM holds nothing. Holding on a change to Service departs from Orders'
O30 on purpose: a service can be neither consumed nor produced. Delivery B adds the open
manufacturing orders.

## Personal data

Work centers, closures and BoMs hold no personal data. The module registers **no
`PersonalData` slot** in phase 1 (§9, rule 19 of the
[module boundaries](/en/contributing/module-boundaries/)); the user ids later deliveries
record on time logs, productions and events stay with their display snapshot, as
Inventory's ledger does.

## Refusals

Every refusal is an RFC 9457 problem with this module's `code`; a client keys off the code,
never the words. 404 is a bare body for an id that does not exist. A 400 names the failing
fields in `errors` and a BoM's line refusals in `lines`; `bom_cycle` adds `path`. Request
bodies are strict: an unknown property is a 400.

| Code | Status | Rule |
| --- | --- | --- |
| `revision_conflict` | 409 | A stale `revision`; also a unique-index race on the default (M14a) |
| `work_center_code_taken` | 409 | The code is another work center's (M8) |
| `work_center_in_use` | 409 | Delete of a work center a BoM operation names; archive it instead (M8) |
| `work_center_has_open_work` | 409 | Archive while an open work order names it (M8; from delivery C) |
| `closures_too_many` | 400 | More than 100 closures (M9) |
| `site_unknown` | 400 | `siteId` is not an active Inventory site (M8) |
| `site_archived` | 409 | Reactivating a work center whose site is archived |
| `costs_view_required` | 403 | Writing a rate without `manufacturing:costs-view` (M38) |
| `variant_not_goods` | 400 | The product or a component is not goods, judged from `LockVariants` (M10, M10a); a line's code on a line |
| `variant_not_found` | 400 | An unknown variant; a line's code on a line |
| `bom_code_taken` | 409 | The code is another BoM's (M10) |
| `too_many_lines` | 400 | More than 200 lines (M11) |
| `too_many_operations` | 400 | More than 50 operations (M12) |
| `operation_key_unknown` | 400 | A line names a key no operation in the request has (M12); a line's code |
| `work_center_unknown` | 400 | An operation's work center is missing or archived (M12) |
| `work_center_other_site` | 400 | Operations at more than one site; or changing a work center's `siteId` while an active BoM has an operation at it, on `siteId` (M12a) |
| `bom_cycle` | 400 | A component is, at some depth, the BoM's own variant; `path` names the cycle (M14) |
| `bom_too_deep` | 400 | The walk passes 10 levels, counting the default BoMs above a default, or the overview meets a made node below the 10th (M14) |
| `bom_too_large` | 400 | The overview passes 2,000 nodes (M15a), or an operation's expected time passes 10^12 minutes |
| `bom_not_active` | 409 | Make default or edit of an archived BoM (M14a) |
| `quantity_invalid` | 400 | Not more than 0, or more than three decimals; a line's code on a line |
| `dependency_unavailable` | 503 | The catalog or the stock contract is missing at run time; nothing is changed |

## Enabling and disabling

The smallest installation with Manufacturing is

```text
MODULES=products,inventory,manufacturing
```

Omitting `manufacturing` means the module contributes no route and no permission; its
paths answer the `/api` catch-all 404 and the switcher tile is not shown. The schema is
migrated regardless.

## Endpoints

All under `/api/v1/manufacturing`. Reads need `access`; writes need `access` and
`boms-manage`; 401 and 403 are the platform's. Every write of a work center or a BoM that
takes a `revision` also answers 409 `revision_conflict`.

| Operation | Permission | Answers |
| --- | --- | --- |
| `GET /meta` (`getManufacturingMeta`) | access | 200 — the caller's abilities and the currency |
| `GET /stats` (`getManufacturingStats`) | access | 200 — active and blocked work centers, active BoMs |
| `GET /sites` (`listManufacturingSites`) | access | 200 — Inventory's active sites: `id`, `code`, `name`, `rootLocationId` |
| `GET /variants` (`searchManufacturingVariants`) | access | 200 — goods variants of active products matching `q`, each with `hasBom`; `limit` clamped to 1–50, default 20 |
| `GET /work-centers` (`listManufacturingWorkCenters`) | access | 200 — by code; `status`, `siteId`, `q` |
| `POST /work-centers` (`createManufacturingWorkCenter`) | boms-manage | 201; 400 fields, `site_unknown`; 403 `costs_view_required`; 409 `work_center_code_taken` |
| `GET /work-centers/{id}` (`getManufacturingWorkCenter`) | access | 200; 404 |
| `PUT /work-centers/{id}` (`updateManufacturingWorkCenter`) | boms-manage | 200; 400 fields, `site_unknown`, `work_center_other_site`; 403 `costs_view_required`; 404; 409 `work_center_code_taken`, `revision_conflict` |
| `DELETE /work-centers/{id}` (`deleteManufacturingWorkCenter`) | boms-manage | 204; 404; 409 `work_center_in_use`, `revision_conflict` (`revision` in the query) |
| `POST /work-centers/{id}/archive` (`archiveManufacturingWorkCenter`) | boms-manage | 200; 404; 409; 503 `dependency_unavailable` |
| `POST /work-centers/{id}/reactivate` (`reactivateManufacturingWorkCenter`) | boms-manage | 200; 404; 409 `site_archived`, `revision_conflict` |
| `GET /work-centers/{id}/closures` (`listManufacturingClosures`) | access | 200 — by `fromDate`; 404 |
| `POST /work-centers/{id}/closures` (`createManufacturingClosure`) | boms-manage | 201; 400 dates, `closures_too_many`; 404 |
| `DELETE /work-centers/{id}/closures/{closureId}` (`deleteManufacturingClosure`) | boms-manage | 204; 404 |
| `GET /boms` (`listManufacturingBoms`) | access | 200 — `items` and `total`; `variantId`, `status`, `q`, `limit` clamped to 1–200, `offset` |
| `POST /boms` (`createManufacturingBom`) | boms-manage | 201; 400 fields, lines, `variant_not_goods`, `variant_not_found`, `too_many_lines`, `too_many_operations`, `operation_key_unknown`, `work_center_unknown`, `work_center_other_site`, `bom_cycle`, `bom_too_deep`; 409 `bom_code_taken`, `revision_conflict` |
| `GET /boms/{id}` (`getManufacturingBom`) | access | 200 with operations and lines; 404 |
| `PUT /boms/{id}` (`updateManufacturingBom`) | boms-manage | 200; 400 as create; 404; 409 `bom_code_taken`, `bom_not_active`, `revision_conflict` |
| `POST /boms/{id}/archive` (`archiveManufacturingBom`) | boms-manage | 200; 404; 409 |
| `POST /boms/{id}/reactivate` (`reactivateManufacturingBom`) | boms-manage | 200; 400 `variant_not_goods`, `variant_not_found`, `work_center_unknown`, `work_center_other_site`, `bom_cycle`, `bom_too_deep`; 404; 409 |
| `POST /boms/{id}/make-default` (`makeDefaultManufacturingBom`) | boms-manage | 200; 400 `bom_cycle`, `bom_too_deep`; 404; 409 `bom_not_active`, `revision_conflict` |
| `GET /boms/{id}/overview` (`getManufacturingBomOverview`) | access | 200 [the overview](#the-overview); 400 `quantity_invalid`, `site_unknown`, `bom_too_deep`, `bom_too_large`; 404 |

The full contract, with every schema, is `openapi/manufacturing.yaml`, published in the
generated API reference (`/en/reference/api/manufacturing/`).
