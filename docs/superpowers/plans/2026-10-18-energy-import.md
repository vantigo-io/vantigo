# Energy import from a file — implementation plan

The plan for
[`2026-10-18-energy-import-design.md`](../specs/2026-10-18-energy-import-design.md),
plus the two customers-import fixes a supplier's real export showed were needed before
it can be imported at all. Each stage is its own commit (or a few), passes the gates
on its own, and changes the documentation that describes it in the same commit.

## 0. Customers import: two fixes

- **Match by organisation number by default.** `imports_suggest.go`'s `suggestMapping`:
  when `legalId` is suggested, the default is `createOrUpdate` matched by `legalId`,
  whether or not a customer-number column is also suggested; customer number is the
  default match only when no `legalId` column is. A supplier's `KUNDENR` is the old
  system's number, which nobody in Vantigo has.
- **A repeated customer is skipped, not failed.** A file listing one customer per
  metering point repeats the customer. In the duplicate pass (`importDuplicateRows`),
  a row whose identity repeats an earlier row's **and whose mapped cells are all equal
  to that row's** gets outcome `skipped` with the message *"Same customer as row N."*
  instead of an error; a repeated identity with any differing mapped cell is still the
  row's error, as today. Migration `00048_customers_import_skipped.sql` widens
  `ck_import_rows_outcome` with `skipped` and adds `customers.imports.skipped`; the
  contract gains `skipped` on `CustomerImport` and on the rows filter; a skipped row
  never counts as failed and is not in the failed-rows file. Frontend: the counts and
  the rows filter show **Skipped**.
- Tests: suggestion defaults (orgnr alone, number alone, both); identical repeats
  skipped in check and run, a differing repeat failed, `allowDuplicateIdentity`
  unchanged.
- Docs: `reference/customers.md`, `{en,nb}/user/customers.md`.

## 1. `internal/fileimport`

- Move `imports_read.go`'s reader (encoding, separator, quoting, unguard, caps, the
  file type) and `headerKey` into `apps/server/internal/fileimport`, exported; the
  customers module calls it. Tests move with it. No behaviour change.
- `contributing/module-boundaries.md` names the package in rule 1.

## 2. `CustomersByLegalID`

- `contracts.CustomerDirectory.CustomersByLegalID(ctx, country, ids)`; the customers
  module implements it on the existing query; every fake directory in tests gains it.
- `reference/customers.md`'s directory section.

## 3. The metering-point import, server

- Migration `00049_energy_imports.sql` (design D3).
- `config`: `ENERGY_IMPORT_ENABLED`, `ENERGY_IMPORT_POLL`, `ENERGY_IMPORT_RETENTION`;
  `deploy/compose/vantigo.env.example`.
- `openapi/energy.yaml`: the operations of design D7, `withoutCustomer` on the list,
  `energy:import`; `go generate`, `bun run gen:client`.
- `energy/imports*.go`, `energy/import.go`, `energy/import_worker.go`: read, suggest
  (with the installation-alias precedence and the totals line), mapping and its
  permissions, the row side (point and link, D4), the worker with check, run and
  attach, `confirmSwitches`.
- Tests through `modtest`, including the supplier file's shape as a fixture with
  synthetic data.
- Docs: new `reference/energy.md`; `{en,nb}/admin/installation.md`.

## 4. The metering-point import, frontend

- `apps/energy/frontend`: the import client, the start page, the stepper page with
  the switch alert and the confirmation checkbox, the attach panel, the list's
  Import button and Without customer filter; host routes; i18n en + nb; tests.
- Docs: `{en,nb}/user/energy.md`.

## 5. The consumption import

- `openapi/energy.yaml`: `postEnergyMeteringPointsByIdConsumptionImport`; source
  `Import`.
- `energy/consumption_import.go` and tests (timestamps, DST, resolution, all or
  nothing, unchanged/replacing/new, wrong GSRN).
- Frontend: the Import readings modal and its test.
- Docs: `{en,nb}/user/energy.md`, `reference/energy.md`.

## 6. Finish

- `ROADMAP.md`.
- Gates: `mise run server:check`, `server:test` (and the energy tests under a non-UTC
  zone, as CI), `frontend:check`, `docs:check`; the generated code and API types
  committed.
- Pull request.
