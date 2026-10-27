# Energy tariffs and the metering point's billing facts: implementation plan

> **For agentic workers:** one implementer per task, a reviewer after each; tick the
> boxes as you go. Read the design
> (`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`, D3, D4,
> D11, D13, "Deliveries", "Testing", the open questions) before task 1, and delivery
> A's plan (`docs/superpowers/plans/2026-10-24-energy-spot-prices.md`) for the shape it
> was run in.

**Goal:** one pull request against `main`, **delivery B** `feat/energy-tariffs` (Energy
4B): the supplier's power tariffs and the installation's own grid tariffs, versioned by
date, with their components; the power tariff and the billing cadence on a supply
period; the grid tariff, household use and the electricity-tax class on a metering
point; and the VAT treatment derived from them. Migration
`00076_energy_tariffs.sql` (the next free on `main` at `c1513277`).

**Architecture:** everything in `internal/energy` on the `energy` schema. No contract,
no slot, nothing of Invoices. Nothing here prices anything: settlements (delivery C)
read what B stores. B builds nothing that contradicts the spot-price service design's
S10 (a settlement prices only from `final` spot prices): a tariff's `spot_energy`
component names no price state, and the state stays the settlement's to judge.

`srv/` is `apps/server/internal/`, `mig/` `srv/db/migrations/`, `R/`
`docs/src/content/docs/en/reference/`, `U/` `docs/src/content/docs/{en,nb}/user/`, `A/`
`docs/src/content/docs/{en,nb}/admin/`.

## Global constraints

- The branch is cut from `main`; never commit to `main`. If `main` takes 00076 while
  the branch is open, renumber in history before rebasing (`prs-land-by-rebase`).
- A fresh worktree runs `bun install --frozen-lockfile` before the frontend tests or
  `docs:check`.
- `TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable`.
- After an OpenAPI, queries or migration change: `cd apps/server && mise exec -- go
  generate ./...` (twice, never package-scoped), then `mise exec -- bun run gen:client`
  from the root. Generated files are committed. Kinds, statuses, cadences and classes
  stay **plain strings** in OpenAPI, not enums (delivery A's gotcha: oapi-codegen
  renames existing constants when a new enum reuses a value).
- Frontend fixtures are patched in the same commit as a wire change; docs change with
  the code.
- Commit by pathspec; no `add -A`, `add .`, `stash`, `reset --hard`, `--amend`.
  Subjects: `feat(energy): … (Energy 4B, task N)`, `feat(energy-ui): … (Energy 4B,
  task N)`, `docs(energy): … (Energy 4B, task N)`. `Docs-Impact: none — <reason>` only
  on plan and test-only commits.
- Every new test is shown able to fail: run it against the code before the change, or
  break the expectation once.
- **Finishing gates:** `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`; the energy tests also under
  `TZ=America/New_York`.
- The pull request is pushed and opened as soon as the work is committed, before the
  local gates, so CI starts early; the gates then run locally and their findings are
  reported.

## How this plan reads the design where it leaves a choice open

1. **The schema** (`mig/00076_energy_tariffs.sql`). `energy.tariffs (id integer
   identity from 1001, kind varchar(10) CHECK power|grid, name varchar(120), description
   varchar(500) NULL, status varchar(10) CHECK active|archived DEFAULT active,
   created_at, updated_at)`. `energy.tariff_versions (id, tariff_id → tariffs ON DELETE
   CASCADE, valid_from date, valid_to date NULL, CHECK valid_to > valid_from, EXCLUDE
   USING gist (tariff_id WITH =, daterange(valid_from, valid_to, '[)') WITH &&))`.
   `energy.tariff_components (id, version_id → tariff_versions ON DELETE CASCADE,
   position smallint, kind varchar(20) CHECK the nine kinds, name varchar(80), rate,
   rate_night, rate_reduced, rate_business numeric(12,6) NULL, day_from, day_to
   smallint NULL, weekdays_only bool DEFAULT false, tiers jsonb NULL, UNIQUE (version_id,
   position))`. The database keeps the shapes a CHECK can state cheaply
   (`jsonb_typeof(tiers) = 'array'`, `0 <= day_from < day_to <= 24`, the rates `>= 0`);
   the tier shape and the kind rules are Go's (reading 3).
   `supply_periods` gains `tariff_id integer NULL REFERENCES energy.tariffs` (RESTRICT)
   and `cadence varchar(10) NOT NULL DEFAULT 'monthly'` CHECK monthly|quarterly|yearly;
   `metering_points` gains `grid_tariff_id integer NULL REFERENCES energy.tariffs`
   (RESTRICT), `household boolean NOT NULL DEFAULT false` and `electricity_tax_class
   varchar(10) NOT NULL DEFAULT 'ordinary'` CHECK ordinary|reduced|exempt. Indexes on
   both foreign keys (the in-use read and the delete's RESTRICT check).
   `schema_energy_tariffs_test.go` runs up/down/up and asserts the CHECKs and the
   exclusion; `schema_test.go`'s energy `wantTables` gains the three tables.
2. **A version's dates are half-open**, `[valid_from, valid_to)`, civil days in the
   point's market time zone (as every other range in the module): `valid_to` is the
   first day the version no longer applies, `null` open-ended. Versions may leave gaps
   (a day no version covers prices nothing: delivery C's `no_tariff`); they may not
   overlap, which Go checks first (400 on the later version) and the exclusion backs.
   A tariff has at least one version, and a version at least one component.
3. **Validation** (`srv/energy/tariffs_validate.go`, pure, table-tested), every error
   keyed by its position in the body — `versions[1].components[2].rate` — so the 400
   names the component's position:
   - `kind` is `power` or `grid`; `name` 1–120 characters trimmed; `description` ≤ 500.
   - Kinds a power tariff may hold: `spot_energy`, `fixed_energy`, `markup`,
     `monthly_fee`. A grid tariff: `grid_energy`, `grid_capacity`, `grid_fixed`,
     `electricity_tax`, `enova_levy`, `monthly_fee`. Any other kind in a version is
     that component's error ("A grid tariff cannot hold spot_energy.").
   - A power version has **exactly one** of `spot_energy` and `fixed_energy` (the
     error on the version: `versions[i].components`). At most one each of
     `grid_energy`, `grid_capacity`, `electricity_tax` and `enova_levy` per version;
     `markup`, `monthly_fee` and `grid_fixed` may repeat (a supplier may list two
     markups).
   - Each kind's fields; a field the kind does not use must be absent (`null`), so a
     body says only what will price:

     | Kind | Required | Optional |
     | --- | --- | --- |
     | `spot_energy` | — | — |
     | `fixed_energy`, `markup` | `rate` (NOK/kWh) | — |
     | `monthly_fee`, `grid_fixed` | `rate` (NOK/month) | — |
     | `grid_energy` | `rate` (day, NOK/kWh) | `rateNight` with `dayFrom`, `dayTo` (whole hours, `0 ≤ dayFrom < dayTo ≤ 24`) and `weekdaysOnly`; all or none |
     | `grid_capacity` | `tiers` | — |
     | `electricity_tax` | `rate` (ordinary), `rateReduced` | — |
     | `enova_levy` | `rate` (household, NOK/kWh), `rateBusiness` (NOK/year) | — |

   - Rates are `0 ≤ rate < 1 000 000`, rounded to six decimals as stored (numeric(12,6)).
   - **Tiers**: 1–50 entries, the first `fromKw` 0, `fromKw` strictly ascending,
     `0 ≤ fromKw < 100 000` (three decimals), `0 ≤ perMonth < 1 000 000` (two decimals).
     Errors keyed `…tiers[k].fromKw`.
   - The component's `name` is 1–80 characters trimmed: the line's text.
4. **Endpoints and their order.** `GET /tariffs` (`energy:tariffs-view`): every tariff,
   no paging (a business holds a handful), ordered active first, then kind, then name;
   `?kind=` and `?status=` narrow it; each row a `TariffSummary` with the version count,
   the version valid today (Europe/Oslo) and the usage counts. `POST /tariffs`
   (`energy:tariffs-manage`): validate (400) → one transaction → 201 with the full
   `Tariff`. `GET /tariffs/{id}`: 404 or the `Tariff` (header, versions in `valid_from`
   order with their components in position order, the usage). `PUT /tariffs/{id}`:
   validate (400) → the tariff locked `FOR UPDATE` (404) → `kind` must equal the
   stored kind (400 on `kind`: "A tariff's kind cannot be changed.") → the versions and
   components replaced in the transaction (delete and insert: no row outside the
   tariff points at a version or a component) → 200. An archived tariff may still be
   edited: an archived tariff still prices the periods it covers, so a wrong figure
   in it must stay correctable. `POST /tariffs/{id}/archive`: 404, else `status =
   archived` (idempotent) → 200. **`DELETE /tariffs/{id}`** (`energy:tariffs-manage`,
   the design's "cannot be deleted" made concrete): 404; 409 "Tariff in use" (a ProblemDetails: Energy's problems carry no machine code) when any
   supply period (of any status) or metering point names it — checked under the row
   lock, the foreign key's RESTRICT the backstop (its 23001 is the same 409) — else 204.
   There is no un-archive in B; the PUT does not touch `status`.
5. **Assignment.** A supply period's `tariffId` must name an **active power** tariff, a
   metering point's `gridTariffId` an **active grid** tariff, else 400 on the field
   ("Tariff 1004 is a grid tariff." / "… is archived." / "… does not exist."). An
   archived tariff already assigned stays assigned: the metering point's PUT accepts
   the `gridTariffId` it already holds even when that tariff is archived, so editing
   the address of a point does not force a tariff change. The assignment is judged
   inside the write's transaction: `shareTariff` reads the tariff `FOR SHARE` before
   the write's other locks, so an archive or a delete in flight (both `FOR UPDATE`) is
   waited for and seen, and `tariffRefusal(id, t, wantKind, current *int32)` judges it;
   both writes use them. A foreign-key violation on the tariff, the backstop, is the
   field's "… does not exist."
6. **The supply period.** `CreateSupplyPeriodRequest` and `SwitchSupplyPeriodRequest`
   gain `tariffId?` (absent: none) and `cadence?` (absent: `monthly`); a cadence that is
   none of the three is 400 on `cadence`. The checks run after the existing ones, in
   the order the field appears (tariff, then cadence), in one 400: the cadence's word
   is read before the transaction, and the tariff is judged first inside it, under
   `FOR SHARE` (reading 5), the refusal answered as the outcome with nothing written;
   the create's overlap pre-check follows in the same transaction. A switch
   does **not** inherit the ended period's tariff: the new customer's contract is the
   body's. The import's create and switch write no tariff and `monthly` (the file
   carries no contract), and its update of a matched point keeps the point's grid
   tariff, household and tax class; its own pages say so. The shared writes `insertSupplyPeriod`
   and `switchSupplyPeriod` take a `supplyTerms{TariffID *int32; Cadence string}`.
   **`PUT /metering-points/{id}/supply-periods/{periodId}`** (`energy:supply-periods-manage`,
   new): `{tariffId, cadence}` changes the terms of an existing period — without it
   every period created before B would stay without a tariff. 404 for an unknown
   (period, point); 400 for a `Cancelled` period ("A cancelled period cannot be
   changed.") and for the fields as above; under the point's supply-period lock,
   taken after the tariff's `FOR SHARE`, the period read again under it so the
   `Cancelled` check and the tariff it holds are judged as it is written.
   The terms PUT accepts the tariff the period already holds even when archived
   (reading 5). `SupplyPeriodResponse` gains `tariffId`, `tariffName` and `cadence`:
   `cadence` always sent, `tariffId` and `tariffName` sent when the period has a
   tariff and **left out** of the JSON when it has none (not `null`); all three
   **optional in the schema**: the recorded .NET exchanges
   (`openapi/testdata/exchanges/energy.jsonl`) are frozen and predate them, and
   every recorded response must still validate.
7. **The metering point.** `PUT /metering-points/{id}` stays a full replace: the body
   gains `gridTariffId?`, `household?` and `electricityTaxClass?`, and absent means
   none, `false` and `ordinary` — as an absent `gridArea` means none today. The edit
   form sends the three, so editing the address keeps them. `POST /metering-points`
   accepts the same three. `MeteringPointResponse` gains `gridTariffId`,
   `gridTariffName`, `household`, `electricityTaxClass` and `vatTreatment` (the last
   three always sent, `gridTariffId` and `gridTariffName` **left out** when the point
   has no grid tariff; all optional in the schema for the same reason),
   in every place it is built (the list, the item, the customer's points). Tariff names
   are read for a page of points with one query (`ListTariffNamesByIDs`). The POST
   and the PUT judge the grid tariff inside the write's transaction under `FOR SHARE`
   (reading 5), with the GSRN pre-check and the write after it; the PUT reads the
   point there `FOR NO KEY UPDATE`, after the tariff, and judges the tariff against
   the one that row holds.
8. **The VAT treatment** (`srv/energy/vat.go`): `vatTreatment(household bool,
   countryCode, postalCode string) string` answers `exempt_household_north` when
   `household`, `countryCode` is `NO` (case-insensitive) and the trimmed postal code is
   exactly four digits between `8000` and `9999`; otherwise `standard`. One function,
   one table test (the bounds 7999/8000/9999, a five-digit code, letters, `SE`, not a
   household). It is a hint, not a VAT code (D8): Invoices maps it in delivery D.
9. **Cadence and the consumer.** Energy accepts `yearly` for any customer (D4, U6): the
   refusal for a person is the invoicing wizard's in delivery D. The Supply-period
   dialog says so beside the field.
10. **Permissions.** `energy:tariffs-view` ("View energy tariffs", "View power and grid
    tariffs, their versions and their components.") and `energy:tariffs-manage`
    ("Manage energy tariffs", "Create, edit, archive and delete power and grid
    tariffs."), not sensitive, delegable, category Energy, in the catalog's key
    order. Assigning a tariff needs only the write's own permission
    (`energy:supply-periods-manage`, `energy:metering-points-manage`); the dialogs list
    tariffs only when the caller holds `energy:tariffs-view`, and otherwise show the
    assigned name read-only.
11. **The frontend.** A **Tariffs** page (`/energy/tariffs`, `energy:tariffs-view`) in
    the Energy navigation: the list (name, kind, status, the version valid today, in
    use), **New tariff** and, per row, the editor in a drawer when the caller holds
    `energy:tariffs-manage` — the header, the versions (add, remove, dates), each
    version's components (add by kind, remove, reorder by up/down, each kind's fields
    per reading 3's table, the tiers as an editable table) — **Archive** and **Delete**
    (Delete disabled with the reason when in use). The server's 400 keys are shown on
    the field they name. The metering point's page gains a **Billing** card: the grid
    tariff, household use, the electricity-tax class and the derived VAT treatment, with
    the point's edit form carrying the three fields. The supply-period table shows the
    tariff and the cadence; the start and switch dialogs take both, and a **Change
    terms** action opens the new PUT. The host's navigation, route and permission
    labels (`catalogs/admin.ts`) learn the two keys; i18n en and nb.

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `mig/00076_energy_tariffs.sql`, `srv/db/schema_energy_tariffs_test.go`, `srv/db/schema_test.go` | the tables and the new columns | 2 |
| `srv/energy/queries/tariffs.sql`, `meteringpoints.sql`, `supplyperiods.sql`, `sqlc.yaml` | the tariff reads and writes; the new columns | 2 |
| `openapi/energy.yaml` (+ the server's copy) | the six tariff operations, the supply-period PUT, the new fields | 2 |
| `srv/energy/module.go` (+ test) | the two permissions | 2 |
| `srv/energy/vat.go`, `tariff_assign.go` | the shared helpers: VAT, assignment, names, `supplyTerms` | 2 |
| `srv/energy/tariffs_validate.go` (+ test) | reading 3 | 3 |
| `srv/energy/tariffs.go` (+ test) | the six handlers | 3 |
| `srv/energy/meteringpoints.go`, `supplyperiods.go`, `customers.go`, `import.go` (+ tests) | the new fields, the supply-period PUT | 3 |
| `apps/energy/frontend/src/api/tariffs.ts`, `pages/tariffs.tsx`, `pages/-tariff-editor.tsx` (+ tests) | the Tariffs page | 4 |
| `apps/energy/frontend/src/pages/metering-points.$meteringPointId.tsx`, `-metering-point-form-modal.tsx`, `-supply-period-modal.tsx`, `api/energy.ts`, `i18n.ts` (+ tests) | the point's page, the dialogs | 4 |
| `apps/host/frontend/src/apps.ts`, `catalogs/navigation.ts`, `catalogs/admin.ts`, `routes/energy/tariffs.tsx`, `-tariffs-page.tsx` (+ test) | the host's wiring | 4 |
| `R/energy.md`, `U/energy.md` ×2, `ROADMAP.md` | the pages | 5 |

## Tasks

- [ ] **1. This plan**, committed:
  `docs(energy): the plan for tariffs and the metering point's billing facts (Energy 4B)`,
  with `Docs-Impact: none — plan only`.

- [ ] **2. The schema, the queries, the contract, the permissions and the shared helpers**
  (readings 1, 5, 7, 8, 10), by the coordinator.
  - The migration and its schema test. Queries: `InsertTariff`, `UpdateTariffHeader`,
    `GetTariff`, `GetTariffForUpdate`, `ListTariffs`, `ArchiveTariff`, `DeleteTariff`,
    `InsertTariffVersion`, `DeleteTariffVersions`, `ListTariffVersions`,
    `InsertTariffComponent`, `ListTariffComponents`, `TariffUsage`,
    `ListTariffNamesByIDs`; the metering-point and supply-period queries with the new
    columns, and `UpdateSupplyPeriodTerms`.
  - OpenAPI: the operations of readings 4 and 6, the schemas, the new fields. Plain
    strings throughout. The new handlers stubbed (501) until task 3.
  - `vat.go` with its test; `tariff_assign.go` (`supplyTerms`, `checkTariffAssignable`,
    `tariffNames`); the existing handlers compile with the new columns and answer the
    new response fields.
  - **commit** `feat(energy): tariffs — the schema, the queries, the contract and the permissions (Energy 4B, task 2)`.

- [ ] **3. The tariff handlers, their validation and the billing facts on the existing
  endpoints** (readings 2–7, 9).
  - Tests first: the validation table (every row of reading 3's table, each kind
    refused in the other tariff, exactly one energy kind, the duplicates, the tiers,
    the positions in the keys); the handlers against the test database (create, read,
    list with filters, replace the versions, kind immutable, overlapping versions,
    archive idempotent, delete unused 204, delete in use 409 by a period and by a
    point, the archived tariff still editable); the assignment (active power on a
    period, grid on a point, archived refused, the archived one already held kept);
    the supply-period terms PUT; the authorization table rows for the new operations.
  - **commit** `feat(energy): tariffs — versions and components, archive and delete, a supply period's tariff and cadence, a metering point's grid tariff, household use and tax class (Energy 4B, task 3)`.

- [ ] **4. The Tariffs page, the Billing card and the dialogs** (reading 11).
  - Tests: the list (filters, the badges), the editor (each kind's fields, adding and
    removing versions and components, the tiers, a 400's keys on their fields),
    archive and delete, the Billing card and its VAT treatment, the dialogs' new
    fields and the terms dialog, the host route's permission gate.
  - i18n en and nb; `bun run translations:check`.
  - **commit** `feat(energy-ui): the Tariffs page, the metering point's billing facts and a supply period's tariff and cadence (Energy 4B, task 4)`.

- [ ] **5. Documentation** (D13 for B). `R/energy.md`: a **Tariffs** section (the model,
  the kinds table with each kind's fields, the validation, versions, archive and
  delete, the endpoints and their permissions), **The metering point's billing facts**
  (household, the tax class, the VAT treatment's rule), the supply period's tariff
  and cadence (and the terms PUT) in "Supply periods", the permission table, the
  migration in `sources`. `U/energy.md` en then nb: **Tariffs** (create, versions, the
  components by kind, archive, delete), "Edit a metering point" and "Read a metering
  point's page" with the Billing card, "Start a supply period" with the tariff and the
  cadence, and **Change a supply period's terms**; Permissions. `ROADMAP.md`: Energy
  phase 4 "Delivery B (done)".
  - `mise run docs:check`.
  - **commit** `docs(energy): tariffs and the billing facts — the reference and the user guide in both languages (Energy 4B, task 5)`.

- [ ] **6. Review, the pull request and the gates.** An opus code review (read-only,
  probe tests through `go test -overlay`) and a docs pass against the code, in
  parallel. The findings go in one `fix(energy): the review's findings — … (Energy 4B)`.
  Push and `gh pr create --base main` at once; then the four gates and the energy tests
  under `TZ=America/New_York`, and their findings reported. Do not merge.

## Self-review

- D3 → readings 1–5, tasks 2–4; D4 → readings 6–9, tasks 2–4; D11's Tariffs page, the
  point's page and the supply-period dialogs → task 4; D13 → task 5; the design's
  Testing bullets for B ("the tariff validation", "the exemption derivation", "the
  migrations' applyUpDownUp schema tests", "each new page and modal") → tasks 2–4.
- Beyond the design's words, named: `DELETE /tariffs/{id}` (reading 4: "cannot be
  deleted, only archived" needs a delete to refuse) and `PUT …/supply-periods/{periodId}`
  (reading 6: periods created before B need a way to their tariff). Both stay within
  D3/D4's model and add no column.
- Names used the same everywhere: `energy.tariffs`, `energy.tariff_versions`,
  `energy.tariff_components`, `tariff_id`, `grid_tariff_id`, `household`,
  `electricity_tax_class`, `cadence`, `vatTreatment`, `exempt_household_north`,
  `standard`, `energy:tariffs-view`, `energy:tariffs-manage`, "Tariff in use".
- Not here: settlements, `contracts.ConsumptionProvider`, anything of Invoices (C–E).
  Nothing in B reads a spot price.
