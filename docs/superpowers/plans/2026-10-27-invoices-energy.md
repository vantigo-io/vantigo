# Invoices from consumption: implementation plan

> **For agentic workers:** one implementer per task, on disjoint files, none of them
> committing; the coordinator commits. Read the design
> (`docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md`: D7, D8, D9,
> D11's Invoices bullets, D13, "Deliveries", "Testing", "Readings on the record" 8 and 9,
> the open questions U4 and U6), the baseline it extends
> (`docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md`: D1, D2, D3,
> D6, D7, D8, D16), delivery C's plan (`docs/superpowers/plans/2026-10-27-energy-settlements.md`,
> whose readings are what the provider does) and `srv/contracts/consumption.go` before
> task 2.

**Goal:** one pull request against `main`, **delivery D** `feat/invoices-energy`
(Invoices 5A): a customer's settled energy consumption becomes an invoice draft through
the **From consumption** wizard — one line per settlement line, in the buyer's language,
its VAT code by the line's hint — held through `line_sources`, judged fresh on GET,
stamped at issue by Energy's `InvoicedWorkHolder` and released by a credit note; the PDF
gains a block per metering point and, for a person, the complaints footer; EHF carries
the metering point per line. The uninvoiced panel gains a **Consumption** section.
Migration `00078_invoices_energy.sql` (the next free on `main` at `fffc49fe`).

**Architecture:** everything in `internal/invoices` on the `invoices` schema and in
`apps/invoices/frontend`. Energy is reached only through `deps.Consumption`
(`contracts.ConsumptionProvider`, nil when Energy is off) and Energy's holder in
`deps.InvoicedWork`, both on main since delivery C. Billing runs, the worker and
`module.Workers` learning slots are delivery E.

`srv/` is `apps/server/internal/`, `mig/` `srv/db/migrations/`, `inv/` `srv/invoices/`,
`R/` `docs/src/content/docs/en/reference/`, `U/` `docs/src/content/docs/{en,nb}/user/`,
`FE/` `apps/invoices/frontend/src/`.

## Global constraints

- The branch is cut from `main` (`fffc49fe`); never commit to `main`. If `main` takes
  00078 while the branch is open, renumber in history before rebasing
  (`prs-land-by-rebase`).
- A fresh worktree runs `bun install --frozen-lockfile` before the frontend tests or
  `docs:check`.
- `TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable`.
  The invoices package's tests take about six minutes.
- After an OpenAPI, queries or migration change: `cd apps/server && mise exec -- go
  generate ./...` (twice, never package-scoped), then `mise exec -- bun run gen:client`
  from the root. Generated files are committed.
- Kinds, states, units, hints and blockers are **plain strings** in OpenAPI, never enums.
  `openapi/invoices.yaml` is hand-sorted; merge with `yq -I4` and sort only what is
  touched. Invoices has no recorded .NET exchanges; still, every field added to an
  existing response is optional (absent, never null: omitempty), so the frontend's
  fixtures stay valid; fields of the new operations are required where always sent.
- `x-vantigo-access` lists permissions sorted. Every new operation is exercised by a
  test (`contracttest.RequireCoverage`).
- The invoices migration names no other schema and no `energy.` literal: the kind CHECK
  is a shape regex, and the project CHECK names the three work kinds.
- Never call `Settle` or `Settlements` inside a transaction that holds locks (the
  provider's rule, MB rule 10): every read of the provider is made on the pool before
  `withLockedTx`, as from-work reads the billable reads, and goes through an accessor in
  `contractscalls.go` (`noteContractCall`).
- Money is exact decimal inside (`math/big.Rat`), JSON numbers on the wire, decimal text
  across the contract.
- Commit by pathspec; no `add -A`, `add .`, `stash`, `reset --hard`, `--amend`.
  Subjects: `feat(invoices): … (Invoices 5A, task N)`, `feat(invoices-ui): … (Invoices
  5A, task N)`, `docs(invoices): … (Invoices 5A, task N)`. `Docs-Impact: none —
  <reason>` only on this plan's commit and test-only commits. One such trailer waives
  `docs:check` for the whole branch, so the docs are held to the code by a docs pass
  regardless.
- Every new test is shown able to fail: run it against the code before the change, or
  break the expectation once.
- **Finishing gates:** `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`, `mise run ehf:validate`; the
  invoices and energy tests also under `TZ=America/New_York`.
- The pull request is pushed and opened as soon as the work is committed, before the
  local gates, so CI starts early; the gates then run locally and their findings are
  reported.

## How this plan reads the design where it leaves a choice open

1. **The schema** (`mig/00078_invoices_energy.sql`).
   - `invoices.line_sources`: `project_id` nullable, with `ck_line_sources_project`
     `(project_id IS NOT NULL) = (source_kind IN ('time.entry', 'expenses.entry',
     'projects.milestone'))` (reading on the record 8); `ck_line_sources_kind` becomes
     `source_kind ~ '^[a-z]+\.[a-z_]+$'`; `ck_line_sources_subkind` becomes
     `(source_kind = 'expenses.entry' OR project_id IS NULL) = (source_subkind IS NOT
     NULL)` — a settlement carries `regular` | `correction`; `ck_line_sources_quantity`
     becomes `quantity > 0 OR project_id IS NULL` (a correction's kWh is a difference,
     zero or negative).
   - The settlement's snapshot on its row, for the document's own rows to print from
     (D9: the PDF and the EHF never read Energy): `point_gsrn varchar(18)`,
     `point_address varchar(320)`, `price_area varchar(4)`, `contract_name
     varchar(120)`, `period_from date` (the period's first day; `source_date` is its
     last), `estimated boolean`; `ck_line_sources_point`: all six set exactly when
     `project_id IS NULL`.
   - `line_count integer NOT NULL DEFAULT 1`, `ck_line_sources_line_count CHECK
     (line_count >= 1 AND (line_count = 1 OR project_id IS NULL))` — reading 2.
   - `invoices.lines`: `unit_price numeric(14,4)` becomes `numeric(18,8)` (reading 3);
     `correction boolean NOT NULL DEFAULT false`; `ck_lines_quantity` becomes `quantity
     > 0 OR (quantity < 0 AND (deducts_invoice_id IS NOT NULL OR correction))`;
     `ck_lines_correction CHECK (NOT correction OR (deducts_invoice_id IS NULL AND
     discount_percent = 0))`.
   - `invoices.settings`: `energy_vat_code integer NOT NULL DEFAULT 1` and
     `energy_vat_code_exempt integer NOT NULL DEFAULT 5`, both `REFERENCES
     invoices.vat_codes (id) ON DELETE RESTRICT`.
   - A seeded VAT code (U4, D8): id 10, code `5N`, "Fritatt – kraft til husholdninger i
     Nord-Norge 0 %", SAF-T `5`, category `Z`, reason "Elektrisk kraft til
     husholdningsbruk i Nord-Norge, mval. § 6-6", rate 0 from 2026-01-01 — inserted
     only when neither id 10 nor the code `5N` exists; the exempt default moves to it
     when it was inserted. The line's issue snapshot takes the code's reason as every
     line's does; a Z category carries no reason in EHF (BR-Z-10, the writer's rule), so
     the reason prints on the PDF's VAT summary, and an operator who wants it in EHF
     points the setting at an E code (U4). Two codes of one (category, rate) on one
     document share a summary row whose reason is the first line's, as today.
   - Down reverses it; it fails (honestly) while a settlement row, a correction line or a
     line price with more than four decimals exists (a `DO` block raises for the last),
     and deletes code 10 only through `ON DELETE RESTRICT` — in use, it fails.

2. **One settlement, several lines.** `ux_line_sources_live` holds a source once, and a
   settlement makes one line per settlement line, so a settlement's one row sits on its
   **first** line and `line_count` says how many lines in a row, from there, bill it.
   - On the wire every one of those lines lists the source (`InvoicesLineSource` per
     line, as now), and a save names it on each of them; they must be consecutive (a 400
     on `lines[i].sources`: "A settlement's lines stay together, one after another"). A
     save that keeps fewer of its lines keeps the source with a smaller count; one that
     names it on none releases it, as work is released.
   - `lineDiffers` for the kind compares the sum of its lines' nets with the source's
     amount, and warns on each of its lines; `withFreshness` marks each of them.
   - `sameIssueWork` compares the position and the count.
   - A credit note releases the settlement when it returns **every** one of its lines in
     full (each line's `lastReturn`); one line returned leaves it invoiced. Its
     `line_releases` row names the credit line returning the first line.

3. **The lines** (D7), built by one pure function, `energyLines` (`inv/energy_lines.go`),
   used by the wizard and by a save's refresh:
   - One line per settlement line, in the settlement's order; settlements ordered by
     GSRN, then period start, regular before correction, then id.
   - **Text** in the buyer's language (`lineLanguage(profile.Language)`), a word table
     beside `lineLanguages`: `"<component><qualifier>, <GSRN> <address>, <period>"` with
     the component's own name (`SettlementLine.Text`), the qualifier ` (dag)`/` (natt)`
     — `(day)`/`(night)` — for a band, and ` (<month year>)` for a capacity line when
     the settlement spans more than one month; the address as Energy answers it
     ("street, postal city"); `periodText` over the period's first and last civil day in
     `Europe/Oslo` (the market zone; the provider's bounds are its midnights). A
     correction: `"Korreksjon <component><qualifier>, <GSRN> <address>, <period>: <±q>
     <unit>, tidligere fakturert <n>"` — `"Correction …, previously invoiced <n>"` — the
     quantity part left out when the difference is zero, the number the original's from
     this module's own rows (the settlement `CorrectsID` names, invoiced on a line source
     here; left out when none is found). Cut to 500 characters.
   - **Unit** word by unit: `kWh` → `kWh`; `month` → `mnd`/`month`; `year` → `år`/`year`;
     `pcs` → `stk`/`pcs` — each one `ehf.UnitCode` maps (KWH, MON, ANN, C62).
   - **A regular line**: the quantity (three decimals) and the settlement's unit price
     when `round2(q × p) = amount`; else `round8(amount ÷ q)` when that hits; else the
     settlement's price, the line then differing (warned, `line_differs_from_sources`).
     Readings on the record 3 — the amount wins — is why `unit_price` takes eight
     decimals: at four, a 1 500 kWh line misses its amount by up to 7 øre nine times in
     ten, and EHF's PEPPOL-EN16931-R120 holds a line's net to quantity × price. A line of
     quantity 0 and amount 0 is left out; a settlement left with no line keeps its first
     as `1 stk × 0.00`, so its source always sits on a line.
   - **A correction line** (`correction = true`): quantity `−1` when the amount is
     negative, else `1`; unit `stk`/`pcs`; unit price `|amount|`; the difference in kWh
     (or months) in the text. Readings on the record 9: the deduction line's shape, and
     honest when only a rate changed — the settlement line's quantity is then 0 and its
     quantity × unit price is not its amount, which no line of kWh could say. The
     document must still total above zero (`invoice_total_not_positive`).
   - **VAT code** by hint: `standard` → the energy code, `exempt_household_north` → the
     exempt code; code 9 (O) for both while the seller is not VAT registered. Discount 0.
   - **The source row**: kind `energy.settlement`, the id and revision, subkind the
     settlement's kind, no project (0 in `heldSource`), quantity the settlement's kWh,
     amount its amount, currency, `source_date` the period's last day, the snapshot,
     `line_count` its lines.

4. **The views.** Two reads for the panel and the wizard, both
   `permission:invoices:access+invoices:create`, both 409 `consumption_unavailable`
   when `deps.Consumption` is nil, both `customerId` required and `through` (a date)
   optional — today in the business zone when absent; the provider's `Through` is the
   start of the day after it in `Europe/Oslo`:
   - `GET /api/v1/invoices/consumption` — `DueSupplies` for the customer, which writes
     nothing: `{customerId, through, supplies[], warnings[]}`, each supply its ids,
     GSRN, address, price area, contract name, cadence, `billedThrough?`, `duePeriods`,
     `blocked?`; `consumption_truncated` when `More`.
   - `GET /api/v1/invoices/consumption/settlements` — `Settle` for the customer (Energy
     writes its own drafts, the provider's command; Invoices writes nothing):
     `{customerId, through, settlements[], warnings[]}`, each settlement its figures,
     lines (with `band?`, `month?`, `vatHint`), `blocked?`/`blockedDetail?`,
     `selectable`, `reason?` (`held` | `blocked` | `cadence`) and `heldBy?` from this
     module's own rows (`LiveSourcesFor`).

5. **The wizard** `POST /api/v1/invoices/from-consumption`
   (`permission:invoices:access+invoices:create`): `{customerId, through?,
   settlementIds?, invoiceId?, revision?, vatCodes?: {energy?, energyExempt?}, note?}`.
   In order, before any transaction:
   1. The body (400): a customer, ids positive and none twice and at most
      `MaxBillableRows`, `invoiceId` and `revision` together, the note ≤ 1 000.
   2. An append's target as from-work's (404, `invoice_issued`, a credit note or another
      customer a 400) and `too_many_sources` with its held rows.
   3. `consumption_unavailable` when `deps.Consumption` is nil.
   4. The settings, the billing profile, `customerGate`.
   5. `DueSupplies` (the cadences), then `Settle` for the customer through `through` —
      or `Settlements(settlementIds)`; `More` is `too_many_sources`.
   6. The judgments, each over every settlement before the next: a named one of another
      customer `source_not_for_customer`; a named one missing, invoiced or released
      `source_not_invoiceable`; a named blocked one `settlement_blocked` (its
      `sourceId`, the blocker in the detail with `BlockedDetail`); `cadence_not_allowed`
      for a person and a yearly supply (the cadence from `DueSupplies`; a settlement whose
      supply it does not list — a correction of a supply billed through — is yearly when
      its period is longer than three months; U6); a currency other than NOK
      `currency_not_nok`. **Without ids**, the blocked ones, the ones another live
      document holds and the ones of a yearly supply of a person are left out instead of
      refused, and nothing left is 409 `no_consumption_due`.
   7. The VAT codes (`resolveVatCodes`' way: an override known and active, a default
      active, a 400 on `vatCodes.energy` / `vatCodes.energyExempt`, judged only when a
      line carries that hint).
   8. The lines (`energyLines`); `too_many_lines` past 500 with the target's; the column
      bounds (`checkWorkLines`).
   9. The delivery period the settlements' span, widened on an append (`widenDelivery`);
      the note the request's, else `rePullNote` over the settlements.
   Then one `withLockedTx` as from-work's: insert the draft (or lock the target at its
   revision), write the lines (a target's own first, its work carried), `liveElsewhere`,
   `insertSources`, the derived project (settlement rows carry none and are left out of
   `oneProject`), the timesheet as the target has it. 201 with the draft, 200 with the
   target. A settlement another document holds is `source_held_elsewhere` as today.

6. **Freshness and refresh** (D7). `billableNow` reads the kind through `Settlements`
   when `deps.Consumption` is set (a kind whose module is off is not judged, as now):
   found, in state draft, not blocked, else `source_not_invoiceable`; another revision,
   currency or amount `source_changed`. `refreshSources: true` re-reads and rebuilds each
   held settlement's lines from its current lines with `energyLines` in the draft's
   buyer language at the settings' codes, in place of the lines the request gave for it
   (their number may change); a settlement no longer invoiceable is dropped as work is.

7. **The issue** (D7). `readIssueWork` requires `deps.Projects` only when a held row
   carries a project; reads the held settlements through `Settlements` before the
   transaction and refuses `source_customer_changed` when one now belongs to another
   customer; `notSelectable` skips the kind; `markWork` hands Energy's holder the
   `WorkSource`s (`ProjectID` 0, no expense kind) last, in `InvoicedWorkOrder`. A save
   accepts a negative quantity on a line that names a held correction settlement (the
   line's `correction` derived on every save, never taken from a request) as on a
   deduction line; a credit of a correction line is negative as a deduction's credit
   is, and carries `correction`.

8. **The settings** gain `energyVatCode` and `energyVatCodeExempt` (the response always,
   the request optional — absent keeps the stored one), each a known, active code; the
   meta's VAT codes list code 10.

9. **The document** (D9). The PDF, from the document's own rows: above the first line
   of each metering point's lines a block — GSRN, address, price area, contract name,
   the period (its settlements' span), the kWh (Σ regular kWh, and the corrections'
   difference beside it when any), "Forbruket er delvis stipulert" / "The consumption
   is partly estimated" when a settlement is estimated — repeated above each run when a
   save interleaved the point's lines; the footer "Prisene på strøm kan sammenlignes på
   strømpris.no. Klager kan rettes til Elklagenemnda, elklagenemnda.no" (en: "Electricity
   prices can be compared at strømpris.no. Complaints can be made to Elklagenemnda,
   elklagenemnda.no") for a person buyer of a document holding settlements. A unit price
   prints with its own decimals, at least two and at most eight. EHF: each line of a
   settlement carries `cac:Item/cac:AdditionalItemProperty` (`cbc:Name` "Målepunkt-ID",
   `cbc:Value` the GSRN) after `cac:ClassifiedTaxCategory`; the document's period as
   `cac:InvoicePeriod` as work's; `cbc:PriceAmount` with its own decimals, at least four
   (so every existing golden stays byte-equal); KWH/MON/ANN/C62 from the unit words. A
   correction line's −1 is `cbc:InvoicedQuantity` −1 as a deduction's.

10. **The frontend** (D11).
    - The uninvoiced panel gains a **Consumption** section (`GET /consumption`): per
      supply the GSRN, address, cadence, billed through, due periods, a blocker in words;
      a **From consumption** button opening the wizard; hidden when the endpoint answers
      `consumption_unavailable`.
    - The **From consumption** wizard: a through date, the settlements of
      `GET /consumption/settlements` with their lines, amounts, blockers and holders,
      selectable ones checked, the two VAT codes, an optional append target, then
      `POST /from-consumption` and the draft.
    - The draft editor shows a settlement source as the point and the period; carries
      `line_count` lines' sources on save; lets a correction line keep its negative
      quantity and an energy line its eight-decimal price; the settings page gains the two
      energy VAT codes.
    - i18n en and nb in `FE/i18n.ts`; `bun run translations:check`.

11. **Documentation** (D13 for D). `R/invoices.md`: **Invoicing consumption** (the
    views, the wizard's order and refusals, the lines and their text, a settlement over
    several lines, correction lines, the unit price's eight decimals, the VAT codes and
    the seeded code, freshness, refresh, the issue, the credit note, the document), the
    settings' two codes, the migration in `sources`, "What comes next" (billing runs).
    `U/invoices.md` en then nb: "Invoice a customer's consumption" and the panel's
    section, the settings' codes. `R/energy.md`: the consumer of the contract now
    exists. `ROADMAP.md`: Invoices phase 5 delivery D done.

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `mig/00078_invoices_energy.sql`, `srv/db/schema_invoices_energy_test.go` | reading 1 | 2 |
| `inv/queries/work.sql`, `settings.sql`, `lines.sql` | the new columns | 2 |
| `openapi/invoices.yaml` | readings 4, 5, 8; the line source's new fields | 2 |
| `inv/contractscalls.go` | `dueSupplies`, `settle`, `settlementsByID` accessors | 2 |
| `inv/sources.go`, `drafts.go` (`writeLines`, `draftLine`), `energy_lines.go` (+ test) | the shared shapes: `heldSource`'s count and snapshot, `settlementSource`, `energyLines`, the kinds a line names | 2 |
| `inv/consumption.go` (+ `consumption_test.go`) | readings 4, 5 | 3 |
| `inv/sources.go`, `drafts.go`, `writeback.go`, `releases.go`, `credits.go`, `issue.go`, `settings.go` (+ tests) | readings 2, 6, 7, 8 | 4 |
| `inv/pdf.go`, `pdfstore.go`, `ehfdoc.go`, `inv/ehf/*` (+ goldens) | reading 9 | 5 |
| `FE/api/consumption.ts`, `components/uninvoiced-work-panel.tsx`, `pages/-from-consumption-wizard.tsx`, the draft editor and settings page, `i18n.ts` (+ tests) | reading 10 | 6 |
| `R/invoices.md`, `R/energy.md`, `U/invoices.md` ×2, `ROADMAP.md` | reading 11 | 7 |

## Tasks

- [ ] **1. This plan**, committed:
  `docs(invoices): the plan for invoices from consumption (Invoices 5A)`, with
  `Docs-Impact: none — plan only`.

- [ ] **2. The schema, the queries, the contract and the shared shapes** (readings 1–3,
  the OpenAPI of 4, 5, 8), by the coordinator.
  - The migration and its schema test (`applyUpDownUp`, the CHECKs refusing what they
    refuse); the queries; `go generate` twice; the compile through a nullable
    `project_id`.
  - OpenAPI: the three operations and their schemas, the settings' two fields, the line
    source's optional `projectId` and settlement fields; the handlers stubbed (501)
    until tasks 3–4; `bun run gen:client`.
  - The accessors; `heldSource.lineCount` and `point`; `settlementSource`; `energyLines`
    with its table test (both languages, a band, a capacity month, a correction of each
    sign and of a rate alone, the unit price search, a zero line, the VAT hints);
    `draftLine.correction`; `writeLines` writing it and the price at eight decimals.
  - **commit** `feat(invoices): invoices from consumption — the schema, the queries, the contract and the lines (Invoices 5A, task 2)`.

- [ ] **3. The views and the wizard** (readings 4, 5), one implementer:
  `inv/consumption.go`, tests on the test database with a fake `ConsumptionProvider`:
  each refusal in order, both languages, the VAT codes and their 400s, the omissions
  without ids, an append, `too_many_lines`, a held settlement, the contract-call rule
  (no provider call under a lock), the permission table.

- [ ] **4. Saves, freshness, the issue, the credit note and the settings** (readings 2,
  6–8), one implementer: the carry over a span, the refresh rebuilding a settlement's
  lines, the freshness verdicts, `readIssueWork` without Projects for energy alone, the
  stamp through a fake holder last in order, the customer check, a correction line's
  negative net issued, the credit releasing only on every line returned, the settings'
  codes; `writeback_test.go`/`releases_test.go` learn the fourth kind.

- [ ] **5. The document** (reading 9), one implementer: the PDF block and footer (golden
  in both languages), the unit price's decimals, EHF's item property and units (golden,
  `mise run ehf:validate`), existing goldens unchanged.

- [ ] **6. The frontend** (reading 10), one implementer: the API client, the panel's
  section, the wizard, the editor's and the settings' changes, fixtures patched, tests,
  `bun run translations:check`.

- [ ] **7. Documentation** (reading 11), one implementer, English first, then bokmål.
  - Tasks 3–7 run in parallel; the coordinator commits them as
    `feat(invoices): invoices from consumption — the views and the wizard (Invoices 5A, task 3)`,
    `feat(invoices): invoices from consumption — saves, freshness, the issue and the credit note (Invoices 5A, task 4)`,
    `feat(invoices): invoices from consumption — the metering point on the PDF and in EHF (Invoices 5A, task 5)`,
    `feat(invoices-ui): the Consumption section and the From consumption wizard (Invoices 5A, task 6)`,
    `docs(invoices): invoicing consumption — the reference and the user guide in both languages (Invoices 5A, task 7)`.

- [ ] **8. Review, the pull request and the gates.** An opus code review (read-only,
  probe tests through `go test -overlay`) and a docs pass against the code (labels from
  `i18n.ts`, en and nb), in parallel; the findings in one
  `fix(invoices): the review's findings — … (Invoices 5A)`. Push and `gh pr create
  --base main` at once; then the gates and the invoices and energy tests under
  `TZ=America/New_York`, and their findings reported. Do not merge.

## Self-review

- D7's link: the nullable project with its CHECK, the shape regex, the subkind, the
  wizard in its order, freshness, refresh, the issue, the credit note — readings 1, 2,
  5–7. D7 left one source over several lines unresolved (`ux_line_sources_live` holds a
  source once): reading 2. D7's negative quantity: reading 3's correction shape and
  reading 1's `correction` column, since the CHECK cannot see `line_sources`.
- D8: reading 3's VAT by hint, reading 1's seeded code and settings, reading 8.
- D9: reading 9. D11's Invoices bullets but billing runs: reading 10. D13: reading 11.
- Out of scope and named: billing runs, the worker, `suggestedSplit`, `module.Workers`
  (E); per-line EHF periods (R3 D16); a reason per line within one VAT summary row.
