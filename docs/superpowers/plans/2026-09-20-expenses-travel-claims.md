# Travel Claims Implementation Plan (Expenses, PR B)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A travel claim (*reiseregning*): one container — purpose, destination, departure and return, an optional project — holding a trip's per diem days, mileage legs and outlays with their receipts, submitted, approved, reimbursed and exported as ONE unit, beside the single expenses that already exist.

**Architecture:** Everything lands inside the existing `expenses` module. One new migration, `00013_expenses_claims.sql`: the `expenses.claims` table, the foreign key from `entries.claim_id` (the column exists since 00012), the per diem columns on `entries`, and the per diem seed rates. A claim's lines are ordinary entries with a `claim_id`; they have no status of their own — the claim's status is theirs. The flow, the queue, the reimbursement track, the CSV and the stats learn a second kind of unit (`claimIds` exist in the contract since PR A and are refused today). The `per_diem` kind is accepted inside a claim only.

**Tech Stack:** as PR A — Go (pgx v5, sqlc, goose, oapi-codegen strict server), PostgreSQL, `internal/storage`, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-19-expenses-design.md` — §2 X3, X4, X5, X7 (per diem never billable), X8; §3.4 (seeds for delivery B — verified figures), §3.6; §4 "Per diem", "Freezing", "Receipt rule", "Period lock", "Currency"; §6 rows Claims (+ Flow / After approval with `claimIds`); §7 "Travel claim page"; §8; §9. The module as built: `docs/expenses.md`.

## Global Constraints

- **Everything in `docs/expenses.md` still holds** — read it first: one refusal rule (404 invisible / 403 not yours / 400 naming the state), the period lock's reach (not reimbursing, pricing, invoicing), pricing by financial rights only, freeze on submit, no call to another module nor to the object store inside a locked transaction (harness-wide check), exact decimals (big.Rat, half-up, a third decimal is a 400), absent-not-null, batches all-or-nothing ≤ 500 distinct ids with per-id explanations, 403 for a caller who can approve nothing, self-approval allowed, with-and-without-Projects tests for every project-aware behaviour. Pattern files are the module's own: `entries.go`, `entries_validation.go`, `money.go`, `flow.go`, `approvals.go`, `reimbursements.go`, `stats.go`, `authorize.go`, `responses.go`, `attachments.go`, `harness_test.go`; frontend `apps/expenses/frontend/src/{api,lib,components,pages,test}`.
- **A claim's line is an entry with `claim_id`.** Owner, status, decision, reimbursement and period-lock judgement come from the CLAIM; the line keeps its own kind, date, amounts, receipts, `billable` + billing figures (outlay and mileage lines only) and its `invoiced` stamp. A line inside a claim is created, edited and deleted only while the claim is `draft | rejected`, by whoever may edit the claim. A standalone entry cannot be moved into a claim, nor a claim line out of it (400 on `claimId`). Standalone operations (`/submit` etc. with `entryIds`) refuse a claim's line with a per-id message pointing at the claim.
- **Project:** the claim's `projectId` (optional, bookable for the claim's OWNER — `CanLogTime`, grandfathered when unchanged) is every line's project; a line may not name a different one (400). Changing the claim's project re-points its lines in the same transaction and clears billing lines that do not belong to the new project.
- **Per diem** (spec §4): only inside a claim; one line per day; `perDiemType` ∈ `day_6_12 | day_over_12 | overnight_hotel | overnight_other`; three meal flags; amount = day rate × (1 − Σ ticked meal percents / 100), never below 0, half-up to 2 places; the day rate is the rate row for the line's DATE and type (`per_diem_6_12`, `per_diem_over_12`, `per_diem_overnight_hotel`, `per_diem_overnight_other`), or the claim's `abroadDayRate` when the claim is abroad (then the type is informational: `overnight_*` or `day_*`, same percentages); never billable; no VAT; default currency (abroad: the claim's `abroadCurrency`, default the settings' currency). No rate for the date/type → 400 on `perDiemType`. At most one per diem line per claim per date (400 on `entryDate`). Dates must fall within the trip (departure date … return date). Rate override by an approver applies to per diem lines too (`PUT /entries/{id}/rate`, audit as for mileage).
- **Suggestion** (`POST /claims/{id}/per-diem-suggestion`, pure function + thin handler; it SUGGESTS, it writes nothing): from `departureAt`/`returnAt` in the claim's own time zone offset as entered (store and compare wall-clock instants; day boundaries are 24-hour periods from departure, not calendar midnights): the request body is `{overnight: boolean}` (required — whether the traveller slept away is a fact the times alone cannot tell). Duration < 6 h → none. `overnight: false`: 6 h up to and including 12 h → one `day_6_12` on the departure date; over 12 h → one `day_over_12`. `overnight: true`: one `overnight_hotel` line per FULL 24-hour period from departure, plus one for a final part-period when it exceeds 6 h (a trip shorter than 24 h with an overnight is one day); each line dated at the start of its period. Returns `[{entryDate, perDiemType, rate?, amount?}]` with rate/amount when a rate applies.
- **Verified seeds** (migration 00013, `valid_from` 2026-01-01, source "State rate", NOK): `per_diem_6_12` 397.00, `per_diem_over_12` 736.00, `per_diem_overnight_hotel` 1012.00; percent kinds `meal_breakfast_percent` 20, `meal_lunch_percent` 30, `meal_dinner_percent` 50 (no currency). `per_diem_overnight_other` is NOT seeded. The Go seed table (`POST /rates/reset`) and its no-drift test grow with them.
- **Exact values:** `purpose` 1–200, `destination` ≤ 200, `returnAt > departureAt`, trip ≤ 366 days, `abroadDayRate` > 0 two decimals required iff `abroad`, `abroadCurrency` 3 letters; at most 200 lines per claim.
- **Toolchain, race detector on what you touched, contract coverage, tests that can fail, i18n en + nb, UTC calendar dates, `testTimeout` already set, commits ending with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`, never `--no-verify`, stage by explicit path and commit without a pathspec** — per the workspace's environment notes.
- **Branch:** `feat/expenses-travel-claims` (from `main`); the PR opens against `main`.

## File Structure

```
apps/server/internal/db/migrations/00013_expenses_claims.sql    claims, FK entries.claim_id, per diem columns, seeds
apps/server/internal/expenses/
  claims.go, claims_validation.go     CRUD, the project re-point, line rules
  perdiem.go                          amount arithmetic + the suggestion (pure), handler
  entries*.go (+)                     claimId accepted; per_diem kind; lines follow their claim
  flow.go, approvals.go (+)           claim units: submit/approve/reject/unapprove, queue
  reimbursements.go, stats.go (+)     claim units: list, mark, undo, CSV, stats, attention
  authorize.go, responses.go (+)      claim access + capabilities; ExpensesClaimResponse
  queries/claims.sql (+ others), *_test.go
openapi/expenses.yaml                 claim operations + schemas; per diem fields; claimIds live
apps/expenses/frontend/src/
  api/claims.ts, lib/per-diem.ts
  pages/claim.tsx (+ -claim-header-modal, -per-diem-section, line sections reuse the entry form)
  pages/my-expenses.tsx, approvals.tsx, reimbursements.tsx, settings.tsx (+ claims / per diem rates)
apps/host/frontend/src/               route /expenses/claims/$claimId, spotlight "New travel claim", attention hrefs
docs/expenses.md, ROADMAP.md
```

---

### Task 1: Claims, and lines inside them

Migration 00013 in full (claims per spec §3.6 + `abroad_currency`; FK `entries.claim_id → claims` `ON DELETE CASCADE`; entries gain `per_diem_type varchar(30)`, `breakfast_covered`, `lunch_covered`, `dinner_covered boolean not null default false`, `meal_breakfast_percent`, `meal_lunch_percent`, `meal_dinner_percent numeric(5,2)` (the percents used, frozen with the rate); index `(user_id, departure_at)` on claims and partial indexes mirroring 00012's for `submitted` and for approved-not-reimbursed claims; the seeds; down migration; schema-pinning test extended). Operations: `GET /claims` (owner / visible, filters status, from, to, userId, reimbursed; paged), `POST /claims`, `GET /claims/{id}` (the claim with its lines through the one entry renderer, totals per currency: gross, owed, billable bill amount for those who may see it; `capabilities`), `PUT /claims/{id}` (revision-guarded full replace of the header; project re-point), `DELETE /claims/{id}` (draft | rejected; removes lines and, after commit, their objects). Entries: `claimId` accepted on create for `outlay` and `mileage`; line rules per Global Constraints; `GET /entries` gains `claimId` and `standalone` filters and by default EXCLUDES claim lines from "my expenses" lists only when `standalone=true` is asked (keep the default backwards compatible; the frontend passes it). Visibility of a claim = the entry rule applied to the claim (owner; manager of its project; view-all / approve / manage); a line is visible iff its claim is.

- [ ] **Failing tests first**: migration up/down/up + schema pin; claim CRUD + validation table; visibility matrix + bare 404; project rules with and without Projects (incl. re-point and the Projects-off carry-through of a stored project); lines: create/edit/delete inside a draft claim, refused inside a submitted one (400 naming the claim's status), refused project mismatch, moving in/out refused, receipts on a claim's outlay line follow the claim's state, standalone flow operations refuse a claim's line; claim delete removes objects; 200-line cap; lock discipline.
- [ ] Implement; gates; **Commit** `feat(expenses): travel claims — a container for a trip's expenses`.

### Task 2: Per diem

The `per_diem` kind (inside a claim only), its validation and arithmetic (`perdiem.go`, pure, table-tested: every type × meal combination incl. all three meals → 0.00, half-up cases, abroad), rate lookup by date and type, the one-per-date and within-the-trip rules, the suggestion endpoint, rate override on per diem lines, the seeds in the Go seed table + reset, `GET /rates` shows the new kinds to access holders. Entry responses gain `perDiem {type, breakfastCovered, lunchCovered, dinnerCovered, dayRate, mealPercents {breakfast, lunch, dinner}}`.

- [ ] **Failing tests first**: arithmetic table; suggestion table (5 h 59 → none; 6 h → one 6–12; 12 h 00 → 6–12; 12 h 01 → over 12; overnight 26 h → one overnight day (2 h remainder ignored); 31 h → two; 48 h 00 → two; 54 h 01 → three; abroad uses the claim's rate; no rate → line without rate/amount); per diem refused standalone, refused billable, refused VAT, duplicate date, outside the trip, `overnight_other` without a rate; override + audit; seeds no-drift + reset.
- [ ] Implement; gates; **Commit** `feat(expenses): per diem by the day, with meal deductions, from rates you manage`.

### Task 3: The flow, reimbursement, export and stats for claims

`claimIds` live on `/submit`, `/approve`, `/reject`, `/unapprove`, `/reimbursed`, `/reimbursed/undo` (mixed batches with `entryIds` allowed; the 500 cap counts distinct units). Submit freezes EVERY line (mileage + per diem rates and percents, bill amounts) and applies the receipt rule per employee-paid outlay line, naming the line in the refusal; a claim with no lines cannot be submitted; the period lock judges the claim by its DEPARTURE date. Approve: `expenses:approve`, or a manager of the claim's project. Unapprove refused when the claim is reimbursed or any line is invoiced. Queue groups per person and lists units of both kinds (a claim as one row with its totals, line count, receipts missing, overridden rates). Reimbursement: a claim is owed the sum of its employee-paid outlays (gross), mileage and per diem; list / mark / undo / CSV (one row per LINE, with two new leading columns `unit` = `claim 1012` | `expense 2001` and `purpose`; golden-bytes test updated) treat it as one unit. Invoiced stays per line (works on a claim's billable lines once the claim is approved). Stats/attention count units; `expenseRejected` entityId for a claim is `claim/<id>`.

- [ ] **Failing tests first**: every move for a claim; mixed batches all-or-nothing; freeze across line kinds (rate table change after submit moves nothing; reject → edit → resubmit refreezes); receipt rule per line; empty claim; lock by departure date; who approves; unapprove refusals; queue contents; owed per claim; mark/undo; CSV golden; invoiced on a claim line; stats + attention; concurrency (two approvers on one claim; submit racing a line edit).
- [ ] Implement; gates incl. the whole suite under `-race`; **Commit** `feat(expenses): a travel claim is submitted, approved and reimbursed as one`.

### Task 4: Frontend — the travel claim page

`api/claims.ts`; `pages/claim.tsx` (`ClaimPage({ claimId })`): header card (purpose, destination, domestic/abroad + day rate + currency, departure/return with date-time pickers, optional project when `projectsAvailable`), three sections — **Per diem** ("Suggest days" with an overnight switch → preview → "Add these days"; each day: date, type select, three meal checkboxes, the amount; remove), **Mileage** and **Outlays** (reuse the expense form modal with `claimId`, the project block replaced by the claim's project + the line's Billable switch), totals per currency (what I get back; for those who may see it, what goes to the customer), status banner / rejection reason / decision, one **Submit claim** button, read-only when not editable. My expenses: a "New travel claim" action, claims listed beside single expenses (a unified list: decide one sort by date) with status, totals and line count; list passes `standalone=true` for entries. Mobile-friendly.

- [ ] **Failing tests first** (fetch stub extended with claims + suggestion): create claim → page; suggestion flow incl. abroad; meal ticks change the amount shown after save; add/edit/remove mileage and outlay lines with receipts; project block absent without Projects; submit incl. per-line refusals; read-only states; the unified list.
- [ ] Implement; gates; **Commit** `feat(expenses-ui): the travel claim page`.

### Task 5: Frontend — approvals, reimbursements, settings; host; docs

Approvals and Reimbursements handle claim units (row opens the claim read-only in a drawer with all lines, receipts, per-line rate override incl. per diem, per-line pricing; batch with mixed ids). Settings: per diem rate groups with the seeded rows + the unseeded "overnight, other" group with a hint; meal percentages. Host: route `/expenses/claims/$claimId`, spotlight "New travel claim", attention href for `claim/<id>`; en + nb. Docs: `docs/expenses.md` (claims, per diem rules + the suggestion's exact day counting, the verified seeds with their source and validity, what is not modelled: night supplement, the 15 km condition; CSV columns), ROADMAP.

- [ ] Tests first; implement; all frontend + host gates; **Commits** `feat(expenses-ui): claims in approvals, reimbursements and settings`, `feat(frontend): the travel claim route and spotlight action`, `docs(expenses): travel claims and per diem`.

### Task 6: Final gates and PR

- [ ] Whole-branch reviews (backend / frontend+docs), one fix wave, re-review; all gates incl. the whole Go suite under `-race` on 4 CPUs, `mise run smoke`, `openapi/COVERAGE.md`; trailers; push; PR against `main`; watch checks; never merge. Delivery C starts only after this one is merged.
