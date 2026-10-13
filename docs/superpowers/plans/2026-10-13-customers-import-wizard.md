# Customer import from a file — implementation plan

The plan for
[`2026-10-13-customers-import-wizard-design.md`](../specs/2026-10-13-customers-import-wizard-design.md).
Every step names its files; the documentation steps are their own, in both
languages where the section is bilingual.

## 1. Schema and queries

- `apps/server/internal/db/migrations/00045_customers_imports.sql` — `customers.imports`
  and `customers.import_rows` (design D1).
- `apps/server/internal/customers/sqlc.yaml` gains the migration;
  `queries/imports.sql` the import's statements: insert, get, list, the queue's next,
  the mapping save, the state transitions, the chunk of unprocessed rows, the chunk's
  outcome writes, the counts, the rows page, the failed rows, the sweep. The rows'
  `CopyFrom` is pgx, not sqlc.

## 2. Settings

- `apps/server/internal/config/config.go` — `CustomersImportEnabled`,
  `CustomersImportPoll` (5 s), `CustomersImportRetention` (30 days); tests in
  `config_test.go`; `deploy/compose/vantigo.env.example`.

## 3. The contract

- `openapi/customers.yaml` — `postCustomersImport` removed; the ten operations of
  design D7 and their schemas (`CustomerImport`, `CustomerImportMapping`,
  `CustomerImportMappingColumn`, `CustomerImportSampleRow`, `CustomerImportRow`,
  `CustomerImportRowError`, the paged responses); `customers:import` named in each
  rule; the four 409 codes on `CustomerConflictProblem`'s description.
- `go generate ./...` in `apps/server`; `bun run gen:client` at the root.

## 4. The module

- `module.go` — the permission, the worker, the rate limit; `objects.go`'s
  `bodyLimits` the upload's cap.
- `imports_read.go` — the reader (design D2): encoding, separator, header, caps.
- `imports_suggest.go` — the alias table and the three-tier suggestion (D3).
- `imports_mapping.go` — the mapping document, its validation and permissions, the
  layout it makes for `import.go`'s row side (D3, D4).
- `import.go` — the row side kept, fed by the mapping: match by number or identity,
  mode, overlay on update, identity defaults, type synonyms; the request side
  removed.
- `imports.go` — the handlers of D7, the sample and preview, the failed-rows file.
- `import_worker.go` — `customers-import` (D5, D6): lease, nudge, chunks, outcomes
  inside the row's transaction, stop, resume, counts, sweep.
- Tests: `imports_test.go`, `imports_read_test.go`, `imports_suggest_test.go`,
  `import_worker_test.go`; the old `csvimport_*_test.go` ported where the rule
  survives; `module_test.go`'s worker set; `csvexport_test.go`'s round trip through
  the new path.

## 5. The frontend

- `apps/customers/frontend/src/api/imports.ts` (+ test) — the client and the query
  options; `import-export.ts` loses `importCustomers`; `lib/csv.ts` goes.
- `pages/import.index.tsx` — the start page; `pages/import.$importId.tsx` — the
  stepper page; `pages/-import-mapping-step.tsx`, `-import-progress.tsx`,
  `-import-rows-table.tsx` as it divides; tests beside each.
- `pages/customers.index.tsx` — Import becomes a link; the modal and its test go.
- `apps/host/frontend/src/routes/customers/import.index.tsx`,
  `import.$importId.tsx`, `-import-page.tsx` (the capabilities), their tests,
  `-customers-list.tsx`'s `canImport` and its test; `apps.ts` unchanged (a header
  action, not a sidebar entry); the Spotlight action.
- `i18n.ts` en + nb.

## 6. Documentation

- `docs/src/content/docs/en/reference/customers.md` — the section, Permissions, The
  frontend, API, What comes next.
- `docs/src/content/docs/en/user/customers.md` and `nb/user/customers.md` — Export
  and import, the permissions table.
- `docs/src/content/docs/en/admin/installation.md` and `nb/admin/installation.md` —
  the worker paragraph.
- `ROADMAP.md` — phase 6 delivery A2.

## 7. Gates

`mise run server:check`, `mise run server:test`, `mise run frontend:check`,
`mise run docs:check`.
