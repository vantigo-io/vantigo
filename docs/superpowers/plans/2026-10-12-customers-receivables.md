# Customer 360 delivery C — receivables: implementation plan

**Goal:** the customer page's overview answers what the customer has been invoiced
and what they still owe, read from Invoices through a new single-provider contract.

**Spec:** [`docs/superpowers/specs/2026-10-12-customers-receivables-design.md`](../specs/2026-10-12-customers-receivables-design.md)
(D1–D4).

**Pattern code:** `internal/contracts/expenses.go` (the contract's voice),
`internal/expenses/projectexpenses.go` and `projectexpenses_test.go` (a provider and
its tests), `internal/module/compose.go:160-262` and `compose_test.go:1000-1090` (a
single-provider slot), `internal/modtest/modtest.go:227-256,455-510` (a `With*`
seam), `internal/customers/overview.go` and `overview_test.go` (the consumer and
its fakes), `internal/invoices/queries/stats.sql` and `overdue.sql` (the state
derivation), `apps/host/frontend/src/routes/customers/-customer-360-panel.tsx`,
`api/customer-overview.ts`, `catalogs/customer.ts` and their tests (the panel).

## Tasks

- [x] **1. The contract and the slot.** `internal/contracts/receivables.go`
  (`CustomerReceivables`, `CustomerReceivableTotals`, `CurrencyReceivables`);
  `module.Deps.Receivables` and `module.Module.Receivables`; the slot resolved in
  `compose.go` after `Expenses` ("customer receivables"), with the three compose
  tests the other slots have (resolved onto every Deps, duplicate refused naming
  both, disabled leaves nil); `modtest.WithReceivables`; `module-boundaries.md`'s
  slot count and list.
- [x] **2. The provider in Invoices.** `queries/receivables.sql` (one grouped query
  over the documents with the two lateral joins; counts of live holds and hand-offs
  over the outstanding set; the last live payment), `sqlc` regenerated;
  `invoices/receivables.go` (`newCustomerReceivables`, the Oslo day from
  `Deps.Clock`, the year bounds, decimal text out); `Module().Receivables`; the
  provider tests of the spec's testing section against the real schema.
- [x] **3. The consumer in Customers.** `openapi/customers.yaml`: `CustomerOverviewInvoices`,
  the `invoices` property, `lastActivity.invoicedOn` and `paymentOn`, the operation
  description; `go generate`; `overview.go`: `overviewInvoices` behind
  `invoices:access` and a non-nil `Deps.Receivables`; the overview tests' shaping
  matrix for the new section (a `fakeReceivables`).
- [x] **4. The panel in the host.** `api/customer-overview.ts` (the wire and the
  normalised shapes); `-customer-360-panel.tsx` (two tiles, the hint shapes, the
  See invoices link, six-tile grid, two more last-activity sources);
  `catalogs/customer.ts` en + nb and the catalog test's count; the panel tests.
- [x] **5. Docs.** `en/reference/customers.md` (the Customer 360 table and paragraph,
  the contracts section, the frontend bullet, the phase 5 text); `en/reference/invoices.md`
  (a "What Invoices reports to other modules" section); `en/user/customers.md` and
  `nb/user/customers.md` (the two tiles, the link, the permission); `ROADMAP.md`
  (phase 5 delivery C, the Later list's two corrections).
- [x] **6. Gates and the pull request.** `mise run server:check`, `server:test`,
  `frontend:check`, `docs:check`; `bun run gen:client`; Conventional Commits; the
  PR on `feat/customers-receivables` with CI green.
