# Billing runs: implementation plan

> **For agentic workers:** one implementer per task, on disjoint files, none of them
> committing; the coordinator commits. Read the design
> (`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`: D1, D7's
> `too_many_lines`, D10, D11's Billing runs bullet, D13, "Deliveries", "Testing",
> "Readings on the record" 10, the open questions) and delivery D's plan
> (`docs/superpowers/plans/2026-10-27-invoices-energy.md`, whose readings are what the
> wizard and the issue now do) before task 2.

**Goal:** one pull request against `main`, **delivery E** `feat/invoices-billing-runs`
(Invoices 5B): a **billing run** settles, drafts and — when asked — issues one invoice
per customer with energy consumption due, through the wizard's and the issue's own code
paths, under the run's creator, and records per customer what it made, skipped or failed.
A worker, `invoices-billing`, carries the items; `module.Workers` learns the slots it
needs. The **Billing runs** pages make a run, list them and show a run's items. Invoices
phase 5 is then done. Migration `00079_invoices_billing_runs.sql` (the next free on
`main` at `1280f399`).

**Architecture:** everything in `internal/invoices` on the `invoices` schema and in
`apps/invoices/frontend`, plus three seams: `module.Workers` (the slots),
`contracts.DueSuppliesRequest` (the "every customer" read the run needs, reading 2) and
Energy's provider answering it. Energy is reached only through `deps.Consumption`, on
the pool, before any of this module's transactions.

`srv/` is `apps/server/internal/`, `mig/` `srv/db/migrations/`, `inv/` `srv/invoices/`,
`R/` `docs/src/content/docs/en/reference/`, `U/` `docs/src/content/docs/{en,nb}/user/`,
`A/` `docs/src/content/docs/{en,nb}/admin/`, `MB`
`docs/src/content/docs/en/contributing/module-boundaries.md`, `FE/`
`apps/invoices/frontend/src/`.

## Global constraints

- The branch is cut from `main` (`1280f399`); never commit to `main`. If `main` takes
  00079 while the branch is open, renumber in history before rebasing.
- A fresh worktree runs `bun install --frozen-lockfile` before the frontend tests or
  `docs:check`.
- `TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable`.
  The invoices package's tests take about six minutes.
- After an OpenAPI, queries or migration change: `cd apps/server && mise exec -- go
  generate ./...` (twice, never package-scoped), then `mise exec -- bun run gen:client`
  from the root. Generated files are committed.
- Statuses and codes are **plain strings** in OpenAPI, never enums. `openapi/invoices.yaml`
  is hand-ordered: the new paths after `/from-consumption`, the new schemas after the
  consumption ones. Fields of the new operations are required where always sent;
  optional ones are omitempty (absent, never null).
- `x-vantigo-access` lists permissions sorted. Every new operation is exercised by a test
  (`contracttest.RequireCoverage`), and every status a test sees is declared.
- The invoices migration names no other schema.
- Never call `Settle`, `Settlements` or `DueSupplies` inside a transaction that holds
  locks: a run item reads Energy on the pool before the wizard's transaction, through
  the accessors in `contractscalls.go`.
- Commit by pathspec; no `add -A`, `add .`, `stash`, `reset --hard`, `--amend`. While
  parallel implementers' files are mid-edit, the coordinator commits by pathspec with
  `--no-verify` once its own files are formatted, and says so. Subjects:
  `feat(invoices): … (Invoices 5B, task N)`, `feat(invoices-ui): … (Invoices 5B, task
  N)`, `docs(invoices): … (Invoices 5B, task N)`. `Docs-Impact: none — <reason>` only on
  this plan's commit and test-only commits; one such trailer waives `docs:check` for the
  whole branch, so the docs are held to the code by a docs pass regardless.
- Every new test is shown able to fail: run it against the code before the change, or
  break the expectation once.
- **Finishing gates:** `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`; the invoices and energy tests also
  under `TZ=America/New_York`.
- The pull request is pushed and opened as soon as the work is committed, before the
  local gates; the gates then run locally and their findings are reported.

## How this plan reads the design where it leaves a choice open

1. **The schema** (`mig/00079_invoices_billing_runs.sql`).
   - `invoices.billing_runs`: `id bigint` identity from 1001, `through date`, `issue
     boolean`, `status varchar(10)` (`queued` | `running` | `done` | `cancelled`, CHECK),
     `customers integer` (the items made, ≥ 0), `created_by_user_id uuid`, `created_at`,
     `started_at`, `finished_at` (timestamptz NULL). `ux_billing_runs_active`: a unique
     index on `((true)) WHERE status IN ('queued', 'running')` — at most one run not
     finished, so two creates racing end in one and a 409.
   - `invoices.billing_run_items`: `id bigint` identity, `run_id` `REFERENCES
     invoices.billing_runs ON DELETE CASCADE`, `customer_id integer` (opaque), `status
     varchar(10)` (`queued` | `drafted` | `issued` | `skipped` | `failed` | `cancelled`,
     CHECK), `invoice_ids bigint[] NOT NULL DEFAULT '{}'` (the documents the item made,
     in order — no foreign key: a draft the run made may be deleted later, and the reads
     answer only the ones that still exist), `code varchar(60) NULL`, `detail text NULL`,
     `attempts integer NOT NULL DEFAULT 0`, `next_attempt_at timestamptz NOT NULL`,
     `lease_id varchar(100) NULL`, `lease_until timestamptz NULL`, `finished_at
     timestamptz NULL`; unique `(run_id, customer_id)`; `ix_billing_run_items_due` on
     `(next_attempt_at, id) WHERE status = 'queued'`; CHECK: `code` set exactly when the
     status is `skipped` or `failed`; `finished_at` set exactly when the status is not
     `queued`.
   - Counts are not stored: the reads count the items by status (one `GROUP BY`), so they
     are never out of step with the items. `customers` is what the run was made with.
   - Down drops both. Added to `inv/sqlc.yaml`'s schema list.

2. **"Every customer DueSupplies names"** (D10). `DueSupplies` takes customer ids, and
   no directory lists every customer. `contracts.DueSuppliesRequest` gains `Every bool`:
   exactly one of `CustomerIDs` and `Every` (`Validate`), `Every` meaning every supply
   period that started before `Through`. Energy's provider answers it with one new query
   (`ListSettlementSupplyPeriodIDsAll`, no migration); `R/energy.md`'s contract section
   says so. The run takes the distinct customers of the answer; `More` (more than 5 000
   supplies) refuses the run, 409 `too_many_customers`, telling the caller to name the
   customers.

3. **Creating a run** `POST /api/v1/invoices/billing-runs`
   (`permission:invoices:access+invoices:create`, and `invoices:issue` when `issue`):
   `{through?, customerIds?, issue}`. In order:
   1. The body (400): `through` a date, today in the business zone when absent;
      `customerIds`, when given, 1 to 5 000, positive, none twice.
   2. `issue` without `invoices:issue` → 403.
   3. 409 `consumption_unavailable` when `deps.Consumption` is nil.
   4. 409 `billing_run_active` (with `runId`) when a run is `queued` or `running`.
   5. The customers: named — the directory's `Customers` in chunks of 2 000, an id it
      does not know a 400 on `customerIds[i]`; else `DueSupplies{Every}` through the
      start of the day after `through` (2), its distinct customers in id order, none a
      409 `no_consumption_due`.
   6. One transaction: the run row and an item per customer, `queued`, due now. The
      unique index refusing (23505) is `billing_run_active` too.
   201 with the run. A named customer with nothing due gets an item, which the worker
   skips `no_consumption_due` — the run says so instead of leaving it out.

4. **The worker** `invoices-billing` (always on; the reminder worker's shape): every 5 s
   a cycle of at most 5 items, one at a time. A claim is one conditional `UPDATE` over a
   `SKIP LOCKED` pick — the oldest due `queued` item of a run that is `queued` or
   `running`, its lease run out — taking a 10-minute lease; the run moves `queued →
   running` (`started_at`). The item is carried on a context bounded by the lease less a
   minute. Afterwards the run is `done` (`finished_at`) once it has no `queued` item.

5. **The actor** (readings on the record 10). The item's context carries the run's
   creator as its principal (`runActor`: `contracts.WithPrincipal` with the creator's
   id and nothing else), so every `callerID` the code path reads — the draft's
   `created_by_user_id`, the issue's `issued_by_user_id`, the stamps' `IssuedBy` and the
   issuer's display name — is the creator's. The run does not re-judge the creator's
   permissions: they were judged when the run was made.

6. **One code path** (D10: "`fromConsumption` then, when `issue`, `issueLocked` and
   `storeAfterIssue`"). The wizard's handler and the issue's handler are each split into
   a core and a mapping: `fromConsumption(ctx, body) (consumptionOutcome, error)` — the
   draft, a 409 problem, the 400 errors or "the target is gone" — and
   `issueDraft(ctx, id, issueDate) (issueOutcome, error)` — the issued document (its PDF
   stored after the commit), a 409 problem, 404 or 503 `storage_unavailable`. The
   handlers map the outcomes to their responses unchanged; the worker calls the same
   cores. Nothing of either is duplicated.

7. **An item, in order.**
   1. Energy off → `skipped consumption_unavailable`.
   2. `Settle` for the customer through the start of the day after `through`, on the
      pool. The settlements a live document already holds are left out — the run bills
      what is not being invoiced yet; this item's own documents of an earlier attempt are
      among them. Nothing left → `skipped no_consumption_due`.
   3. **The split** (D7, D10): the settlements in the wizard's order (`sortSettlements`:
      GSRN, period, kind, id), grouped by metering point and packed, point by point, into
      documents of at most 500 lines — a settlement counted as `max(1, len(Lines))`, the
      most `energyLines` makes of it. A point whose settlements alone exceed 500 is a
      document of its own, which the wizard refuses (`too_many_lines`).
   4. Per document: `fromConsumption` with the customer, `through` and the document's
      `settlementIds` — so a blocked settlement is the wizard's `settlement_blocked` and
      a yearly supply of a person its `cadence_not_allowed`, by name — and the document's
      id appended to the item at once (`invoice_ids`), lease-checked. Then, when the run
      issues, `issueDraft` on it, without an issue date (today).
   5. Outcomes. A wizard refusal (409 or 400) → `skipped` with its code, and a detail
      naming the metering point when the refusal names a settlement ("Metering point
      <GSRN>: …"); a 400 is code `invalid_request`, its field errors in the detail. An
      issue refusal → `failed` with its code (`storage_unavailable` included); the draft
      stays, listed. A later document is not tried after a refusal. Every document made
      and, when the run issues, issued → `drafted` or `issued`.
   6. An error (not a refusal) counts an attempt: the item goes back to `queued`, due
      after 1 then 2 minutes, its lease cleared; the third error fails it, code
      `billing_error`, a fixed detail, the error logged. Nothing refused is retried.
   Every write of the item names its lease; 0 rows is another actor's move, logged at
   debug.

8. **The reads.** `GET /api/v1/invoices/billing-runs` (`permission:invoices:access`):
   newest first, paged (25, at most 100), each run with its counts by status.
   `GET /api/v1/invoices/billing-runs/{id}`: the run and a page of its items in item
   order, filtered by `status`; each item its customer's name from the directory (absent
   when the directory is off or does not know it), its documents that still exist (id,
   number when issued, status), code, detail, attempts, `finishedAt`. 404 for an
   unknown run.

9. **Cancel** `POST /api/v1/invoices/billing-runs/{id}/cancel`
   (`permission:invoices:access+invoices:create`): a `queued` or `running` run becomes
   `cancelled` (`finished_at` now) and its `queued` items not under a live lease
   `cancelled`; an item being carried finishes and records its outcome. A run already
   `done` or `cancelled` → 409 `billing_run_finished`. 200 with the run.

10. **`module.Workers`** (D10, MB rule 5). The Projects slot and the four billing slots
    (`BillableHours`, `BillableExpenses`, `BillableMilestones`, `Consumption`) are each
    resolved by one helper Compose and Workers both call — `withProjects`, then
    `withBillables` — in Compose's order (after the directory, users, calendar and
    accounts; billable after Projects; Consumption last), keeping a preset value when no
    enabled module provides one. `workers_test.go` covers the resolution (beside the
    other `Workers` slot tests), the preset seam and two providers panicking.

11. **The frontend** (D11). `/invoices/billing-runs`: the list (through, issue or draft
    only, status, the counts, created); **New billing run** (through date, issue or not —
    the issue choice only for a caller who may issue —, optionally the customers by id
    list; the 409s in words); a run's page: its header, counts, **Cancel** while it is
    not finished, the items filtered by status with the customer, outcome, the documents
    as links and the code and detail in words, polling while the run is not finished.
    Navigation under `invoices:create`. i18n en and nb in `FE/i18n.ts`.

12. **Documentation** (D13 for E). `R/invoices.md`: **Billing runs** (the tables, the
    create's order and refusals, the worker, an item's order and outcomes, the split, the
    actor, cancel, the reads) and the worker in its workers list; the migration in
    `sources`; "What comes next" without billing runs. `R/energy.md`: `DueSupplies`'
    `Every`. `MB`: rule 5's `Workers` paragraph. `U/invoices.md` en then nb: "Run billing
    for every customer". `A/installation.md` en then nb: the worker. `ROADMAP.md`:
    Invoices phase 5 done.

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `mig/00079_invoices_billing_runs.sql`, `srv/db/schema_invoices_billing_runs_test.go`, `inv/sqlc.yaml` | reading 1 | 2 |
| `inv/queries/billing_runs.sql` | the run's queries | 2 |
| `openapi/invoices.yaml` | readings 3, 8, 9 | 2 |
| `srv/contracts/consumption.go`, `srv/energy/consumption_provider.go`, `srv/energy/queries/settlements.sql` (+ tests) | reading 2 | 2 |
| `srv/module/compose.go`, `workers.go` (+ `workers_test.go`) | reading 10 | 2 |
| `inv/consumption.go`, `inv/issue.go` | reading 6: the cores | 2 |
| `inv/billing_runs.go`, `inv/billing_worker.go`, `inv/module.go` (+ tests) | readings 3–5, 7–9 | 3 |
| `FE/api/billing-runs.ts`, `FE/pages/billing-runs*.tsx`, routes, navigation, `FE/i18n.ts` (+ tests) | reading 11 | 4 |
| `R/invoices.md`, `R/energy.md`, `MB`, `U/invoices.md` ×2, `A/installation.md` ×2, `ROADMAP.md` | reading 12 | 5 |

## Tasks

- [ ] **1. This plan**, committed:
  `docs(invoices): the plan for billing runs (Invoices 5B)`, with
  `Docs-Impact: none — plan only`.

- [ ] **2. The foundation** (readings 1, 2, 6, 10 and the OpenAPI of 3, 8, 9), by the
  coordinator: the migration and its schema test; the queries; `go generate` twice; the
  contract's `Every` and Energy's answer with a test; the module slots with tests; the
  wizard's and the issue's cores, the handlers mapping them; the OpenAPI, the handlers
  stubbed (501) until task 3; `bun run gen:client`.
  **commit** `feat(invoices): billing runs — the schema, the queries, the contract, the slots and the cores (Invoices 5B, task 2)`.

- [ ] **3. The API and the worker** (readings 3–5, 7–9), one implementer:
  `inv/billing_runs.go`, `inv/billing_worker.go`, the worker registered in
  `inv/module.go`; tests on the test database with the fake `ConsumptionProvider`: the
  create's refusals in order, the reads and the filter, cancel, the worker end to end —
  drafted, issued (stamped as the creator), skipped with the point named, failed (an
  issue refusal), split across two documents, the retry and the third error, the lease,
  the run's `done`; the contract-call rule; the permission table.

- [ ] **4. The frontend** (reading 11), one implementer.

- [ ] **5. Documentation** (reading 12), one implementer, English first, then bokmål.
  - Tasks 3–5 run in parallel; the coordinator commits them as
    `feat(invoices): billing runs — the API and the invoices-billing worker (Invoices 5B, task 3)`,
    `feat(invoices-ui): the Billing runs pages (Invoices 5B, task 4)`,
    `docs(invoices): billing runs — the reference, the boundaries, the user guide and the installation in both languages (Invoices 5B, task 5)`.

- [ ] **6. Review, the pull request and the gates.** An opus code review (read-only,
  probe tests through `go test -overlay`) and a docs pass against the code (labels from
  `i18n.ts`, en and nb), in parallel; the findings in one
  `fix(invoices): the review's findings — … (Invoices 5B)`. Push and `gh pr create
  --base main` at once; then the gates and the invoices and energy tests under
  `TZ=America/New_York`, and their findings reported. Do not merge.

## Self-review

- D10's tables: reading 1 (counts derived, `cancelled` for an item cancel reaches,
  `invoice_ids` for a split, `next_attempt_at` for the backoff). Its create: readings 2
  and 3 — the "every customer" read needed a contract seam. Its worker: readings 4–7.
  Its reads and cancel: 8, 9. `Workers`' slots: 10.
- D7's `too_many_lines` and `suggestedSplit`: the run splits by metering point before
  the wizard would refuse (reading 7.3); the wizard's own 409 is unchanged.
- Readings on the record 10: reading 5.
- D11's Billing runs bullet: reading 11. D13: reading 12.
- Out of scope and named: sending after a run (D12, "send all"); a run's own schedule;
  re-running a failed item by hand (a new run for the customer does it).
