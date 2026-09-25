# Work types and overtime multipliers — design (Projects phase 3, delivery B)

Phase 3 of the Projects roadmap ("Budgets, billing milestones and costs") named three things
after its first delivery: supplier costs, overtime and work-type multipliers. This delivery
is the multipliers; supplier invoices are delivery C, their own spec. The Norwegian case
the plan describes — overtime billed at 150 % and paid with an uplift — is today a manual
duplicate billing line "Consulting (overtime)" at a separate product price, on every
project, for every kind of work. After this delivery a project defines its **work types**
once ("Overtime 50 %", "Overtime 100 %", "Weekend"), a person picks one when logging, and
Time multiplies whatever rates the chain resolved. Projects keeps storing rules and never
multiplies anything; Time keeps owning the money on an entry and freezing it on submit.

## Decisions

### D1 — A work type is a project-level rule, not a rule on a billing line

The plan said "overtime and work-type multipliers as rules on the billing line". A rule per
line would have to be defined on every line of every project — the same duplication the
plan wanted to end, one level down. A work type is therefore defined **once per project**
and applies to **every** billing line and every rate step of that project:
`projects.work_types` (migration `00031`): `id`, `project_id` (in-module FK), `name`
(1–100 chars, unique per project case-insensitively — a 409 titled "Work type exists": the projects module carries no error codes of its own),
`bill_multiplier_percent numeric(6,2)` and `cost_multiplier_percent numeric(6,2)` (each
> 0 and ≤ 1000, at most two decimals; 100 is "as the rate says"; 150 is the classic overtime
uplift), `active boolean` (deactivate, never delete: entries snapshot the name, but a type
that was ever picked stays readable in the list), `created_at`/`updated_at`. No default
work type exists and picking none means the plain rates — "ordinary hours" is the absence
of a work type, not a row. No installation-wide catalog: each project defines its own (a
project template, phase 4, is where a shared vocabulary would come from).

`GET /projects/{id}/work-types` for anyone who can see the project; `POST` and
`PUT /projects/{id}/work-types/{workTypeId}` for `CanManage` (the manager role or
`projects:manage-all`). The write validates the body, inserts or updates in one
transaction relying on the unique index for the 409, and records a project timeline entry
(`work-type-added` / `work-type-changed`, the module's own event naming, listing the fields
that changed — the billing lines' precedent). No project-row lock: nothing here depends on the currency,
the fixed price or the billing type. **Multipliers are visible to everyone who can see the
project**: they are a rule ("150 %"), not an amount — the same reading that keeps
`budgetHours` visible while `budgetAmount` is shaped away. Projects records the
percentages and nothing else; every amount is computed in Time.

### D2 — The directory hands the rule to Time

`contracts.ProjectDirectory` gains two methods and one type, read-only:

```go
type WorkTypeEntry struct {
    ID, ProjectID          int32
    Name                   string
    BillMultiplierPercent  float64 // > 0, ≤ 1000, two decimals
    CostMultiplierPercent  float64
    Active                 bool
}
WorkType(ctx, id int32) (*WorkTypeEntry, error)          // (nil, nil) when missing
WorkTypes(ctx, projectID int32) ([]WorkTypeEntry, error) // every type, active first, by name
```

The rule that a directory is never called inside a locked transaction stands: Time reads
the work type with the project and the line, before its transaction opens.

### D3 — Time snapshots the multipliers and multiplies at read time

`TimeEntryRequest`/`TimeEntryUpdateRequest` gain `workTypeId?` (int32). On every save
while `draft` or `rejected` Time checks it — the type must exist, belong to the entry's
project and be active (400 on `workTypeId`: "Work type is not on this project" / "Work type
is no longer active") — and snapshots onto the entry (migration `00032`, `time.entries`):
`work_type_id`, `work_type_name` (100), `bill_multiplier_percent numeric(6,2)`,
`cost_multiplier_percent numeric(6,2)`, all NULL when no type is picked. From `submitted`
on they are frozen with the rates (D3 of the time design). The stored `bill_rate` and
`cost_rate` stay the **base** rates the chain resolved — the multiplier is kept beside them,
never baked in, so nothing is rounded twice and the snapshot still says what the rate was
and what multiplied it. A non-billable entry keeps its cost multiplier (overtime costs the
company whether or not it bills). `rateSource` is untouched: the work type is orthogonal to
which step won.

Amounts multiply where they are summed, exactly: `actuals.sql` becomes
`SUM(hours × bill_rate × COALESCE(bill_multiplier_percent, 100) × 0.01 (exact))` and the same for
cost, still `::text` decimal and rounded once at the end by the existing folding. The
entry response gains `workType?: {id, name}` (whoever sees the entry) and, inside the shaped
blocks, `billing.multiplierPercent?` + `billing.effectiveRate?` (rate × multiplier, half-up
to cents, display only) and `cost.multiplierPercent?` + `cost.effectiveRate?` — absent when
no type is picked, shaped with their blocks. "What invoicing will read" becomes hours ×
bill rate × multiplier percent × 0.01 (exact), stated in the docs.

### D4 — Actuals report hours and value per work type

`contracts.ProjectActualsEntry` gains an additive `WorkTypes []WorkTypeActuals`:
`{WorkTypeID int32; HoursHundredths int64; BillAmount, CostAmount string}` — no name: Projects
names each row from its own `work_types` table, so a rename shows at once and a snapshot's
old name never leaks through — all buckets together, the project's currency only (the same
currency gate as the buckets; other-currency hours count in `HoursHundredths` and nowhere
else), ordered by id, only types with at least one entry, entries without a type not listed. `ActualsForProjects`
(the portfolio's batch read) is unchanged. `GET /projects/{id}/economy` gains
`workTypes: [{id, name, hours, billAmount?, costAmount?}]` — `billAmount` with financial
rights and a currency, `costAmount` with `projects:view-costs` on top, the block absent when
time tracking is off and empty when no entry carries a type. The Economy tab shows "Hours by
work type" under the budget section when the list is non-empty: name, hours, value (when
present), cost (when present). Nothing changes in budget used, the portfolio, the alerts or
the margin — the margin already sums multiplied amounts through the buckets.

### D5 — The frontend

Projects, Billing tab: a **Work types** card beside the billing lines (the tab is already
gated on financial rights; the manager role has them): a table of name, bill multiplier,
cost multiplier, status; **Add work type** / edit / deactivate for `canManage`; the 409 on a
duplicate name shown on the name field; multipliers entered as percentages with a helper
line ("150 % bills and costs one and a half times the rate"). Time, entry form: a **Work
type** select shown when the chosen project has an active work type, cleared when the
project changes, "Ordinary hours" as the empty choice; my week, the day view and the
approval queue show the type as a small badge after the trackable code; where the billing
block is visible, the rate line reads "900 × 150 % = 1 350". en + nb.

### D6 — Docs

`docs/projects.md`: a **Work types** section (what they are, the rule-not-amount reading,
the API, the timeline entries, why no lock, why per project), the economy section's
`workTypes` block, the API list; `docs/time.md`: the rate chain paragraph gains the
multiplier step, the entry's snapshot fields, the freeze rule restated, "what invoicing will
read" corrected, "what Time reports" gains the per-type list; `docs/module-boundaries.md`:
the directory's two new methods in its slot description (no rule changes); `ROADMAP.md`
phase 3: delivery B done, "supplier invoices" the next delivery.

## Out of scope

Automatic overtime from a norm or a time bank (phase 5, capacity); payroll wage types
(lønnsarter) and any pay export; restricting work types per billing line; work types on
expenses; an installation-wide work-type catalog or project templates (phase 4);
re-resolving submitted entries when a multiplier changes; a work type changing an entry's
billable flag; supplier invoices (delivery C); forecast and revised budgets (not
committed).

## Testing

Projects through `modtest`: the CRUD (validation of name and both multipliers, the
duplicate 409 case-insensitively, deactivate, `canManage` vs viewer 403, 404s), the
response visible to a plain member with multipliers, the timeline entries, the directory's
two methods (missing → nil, inactive returned, active-first order), `GET …/economy`'s
`workTypes` block through the fake actuals with every shaping case (no rights, financial
rights, view-costs, no currency, time tracking off, empty), contract coverage. Time through
`modtest` with the fake directory extended: create/update with a type (snapshot fields,
base rates untouched, a foreign or inactive type refused, none → NULLs), the freeze (a
multiplier changed after submit does not move the entry; a rejected entry re-snapshots),
the response blocks shaped per D8 with `multiplierPercent`/`effectiveRate`, the approval
queue carrying the type, actuals: multiplied sums exact to the cent against hand-computed
values (150 % of 333.33 × 1.5 h), the per-type list with currency gating and other-currency
hours, `ActualsForProjects` unchanged. Integration: projects + time composed for real — a
work type created on a project is picked on an entry and shows in the project's economy
with the multiplied value. Frontend: the card (list, add, edit, deactivate, the duplicate
error, a viewer sees no buttons), the entry form select (shown only with active types,
reset on project change, sent as `workTypeId`), the badges, the rate line, the economy
table, both catalogs. Docs against the code.
