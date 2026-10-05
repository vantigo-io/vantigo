# Invoices — work becomes invoices — design (Invoices phase 3)

The fourth delivery of the Invoices module, on `feat/invoices-work-to-invoices`, cut from
`main` after phase 2. Research: `docs/superpowers/research/2026-10-05-invoices-work-to-invoices.md`
("R3": §2 the law on hours, §3 a-konto and the settlement, §4 re-billing, §5 credits,
§6 EHF and personal data, §7 the codebase, §9 the four write-back options, §10 the
sequencing, §11 the decisions it asked for); the 1A research `2026-09-26-invoices-module.md`
and the phase 2 design `2026-10-03-invoices-ehf-peppol-kid-design.md` are its baseline.
Module doc: `docs/src/content/docs/en/reference/invoices.md`. The roadmap's phase is
`ROADMAP.md:817-829`: the write-back "is a third sanctioned cross-module write direction
and gets its own contract design under [module-boundaries] before any code". D1 is it. This revision
follows the design's critical review — one blocker (a zero settlement that could never
be credited) and twenty-three further findings, each taken in its place; the readings it
added or changed say so.

Three modules' roadmaps end here — Time's "What invoicing will read"
(`R/time.md:369-393`), Expenses' "Next: invoicing" (`ROADMAP.md:727-732`), Projects'
milestones (`ROADMAP.md:533-537`). The delivery: **the write-back** (sources stamped with
the invoice's id and number inside the issue's transaction, released by the credit note
that returns their line — rule 10); **the link** `invoices.line_sources`, held from the
draft; **line-level reads** and Expenses' one ready predicate; **the uninvoiced view** and
**the wizard** with five groupings; **the timesheet** inside the PDF; **per-kind VAT
codes**; **a-konto and the final settlement**; **the project dimension**; four modules'
OpenAPI contracts, the frontend and the documentation.

`srv/` is `apps/server/internal/`, `inv/` is `srv/invoices/`, `mig/` is
`srv/db/migrations/`, `MB` is `docs/src/content/docs/en/contributing/module-boundaries.md`,
`R/` is `docs/src/content/docs/en/reference/`. Line numbers are against `d259a4df`. Money
stays as 1A has it: exact decimal inside, JSON numbers on the wire; across a contract,
decimal text, "so no float ever rounds money on its way between modules"
(`srv/contracts/actuals.go:65-68`).

## Decisions

### D1 — The write-back: a holder-style command on the issue's transaction (rule 10)

**The choice.** Option (a) of R3 §9.1: a synchronous command on the issue's own `pgx.Tx`.
It is the only option that keeps the roadmap's "inside the issue transaction"
literally, leaves no crash window between an issued invoice and its stamps, and keeps
each source module's own state authoritative — Time's refusal to reopen an invoiced entry
(`srv/time/approval.go:63-64`), the `Invoiced` buckets of `ProjectActuals`
(`srv/contracts/actuals.go:100-112`) and `ProjectExpenses`, the milestone's frozen amount
(`srv/projects/milestones.go:604-607`) all go on meaning what they say. (b) and (d) open a
commit-to-confirm window and need a worker or the deferred event bus
(`ROADMAP.md:8-21`); (c) leaves the sources' state second-hand and makes every source
aggregate read Invoices while serving, the cycle MB avoids (`MB:201-227`).

**The contract** (`srv/contracts/invoiced_work.go`, new):

```go
// InvoicedWorkHolder is a module whose rows an invoice is built from, and the
// third sanctioned cross-module WRITE (module-boundaries rule 10).
type InvoicedWorkHolder interface {
    // Kinds are the source kinds this holder stamps; they also place it in
    // the cross-module lock order (Projects, then Expenses, then Time).
    Kinds() []WorkSourceKind
    MarkInvoiced(ctx context.Context, tx pgx.Tx, ref InvoiceRef, sources []WorkSource) error
    ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref InvoiceRef, sources []WorkSource) error
}
type WorkSourceKind string // "time.entry", "expenses.entry", "projects.milestone"
type InvoiceRef struct {
    ID, Number      int64
    IssueDate       time.Time
    IssuedAt        time.Time // the issue's own params.Now, every stamp's timestamp
    IssuedBy        uuid.UUID // the stamps' "by"
    IssuedByDisplay string    // the issuer's name, read before the transaction
}
type WorkSource struct {
    Kind      WorkSourceKind
    ID        int64
    Revision  int32  // judged by Time and Projects; display-only for Expenses
    ProjectID int32
    Currency  string
    Amount    string // the billable amount as read for the draft: exact decimal text
    ExpenseKind string // expenses only: the entry's kind, a billing fact the line text depends on
}
// WorkSourceRefusal is the one error a holder answers for a source it will
// not stamp; anything else is a failure, a 500.
type WorkSourceRefusal struct {
    Source WorkSource
    Code   string // source_not_invoiceable | source_changed | source_already_invoiced
    Detail string
}
```

A new many-provider slot, `Deps.InvoicedWork []contracts.InvoicedWorkHolder`, filled from
`Module.InvoicedWork func(Deps) contracts.InvoicedWorkHolder` by Time, Expenses and
Projects, collected by `module.Compose` **from every module given, enabled or not**, as
the customer slots are (`srv/module/compose.go:234-264`), and by `module.Workers` too
(`srv/module/workers.go:19-34`) — no worker issues today, but the EHF workers build a
`*server` from worker-mode `Deps` (phase 2 D9), and a worker-mode server must never see an
empty slot as "nothing to stamp". Invoices fails closed: a draft source whose kind no
holder claims is a 500 and an error log (a composition bug), never a skipped stamp. A
holder's constructor needs only `Deps.Clock`, so a disabled module still stamps and
releases its rows (rules 8 and 9's reason, `MB:176-183`).

**Where the call sits.** In `PostInvoicesByIdIssue` (`inv/issue.go:218-456`), inside
`withLockedTx`, after the existing order — 1 the document `FOR UPDATE` (`:249`), 2 the
merge re-check (`:260-264`), 3 the settings `FOR SHARE` (`:266`), 4 the number (`:271`), 5
every check, the kind's checks (`:323-337`), the VAT summary and the KID (`:338-371`) —
and **before any write of step 6** (`:373-436`): the draft's `line_sources` are read under
the document's lock, grouped by kind, and each holder is called once with its own sources,
in the lock order below, with `InvoiceRef{ID, Number, IssueDate, IssuedAt, IssuedBy,
IssuedByDisplay}` — the issuer's name resolved through `UserDirectory` before the
transaction, as the profile is, so no holder reads a directory for its timeline event. Then
`line_sources.state` moves `held → invoiced` for the document, then the snapshots, the
VAT rows and `IssueDocument` last, as today. A `*WorkSourceRefusal` becomes
`cannotIssue(code, detail)` with `LinePosition` the first line holding that source
(`inv/issue.go:180-184`'s shape) and returns `errRefused`: the transaction rolls back, the
number with it, and nothing any holder wrote survives. For a **credit note**, the
releases (D8) are decided inside `creditIssueChecks` after the original's lock and the
caps (`inv/credits.go:598-659`), and `ReleaseInvoiced` runs at the same place in step 6,
with `InvoiceRef` the **original's** id, number and date (the stamp being taken back);
`IssuedAt`, `IssuedBy` and `IssuedByDisplay` are the credit note's issue's — who took the
stamp back, and when.

**Before the transaction**, beside the profile read (`inv/issue.go:238-243`), the issue
reads the draft's `line_sources` on the pool and, when any, the projects they name
through `ProjectDirectory.Projects` (`srv/contracts/projects.go:101-104`): a project that
no longer bills the draft's customer refuses with 409 `source_customer_changed` (the
line's position) before a number exists. Time's and Expenses' rows know only a project id
(`mig/00010_time_baseline.sql:6-33`, `mig/00012_expenses_baseline.sql:56-101`), so no
holder could judge it under the lock; it is judged where the profile is — Reading 7. With
Projects disabled and the draft holding sources, the issue **fails closed**: 409
`projects_unavailable`, before a number. **Under the lock**, the issue compares the
draft's sources with the set it read before the transaction — any difference (a save
slipped between) is `invoice_changed`, as a merge's is — and re-checks each project's
`BillingType` from that read: hours of a project now fixed-price or non-billable are
`source_not_selectable` (D14).

**What each holder does.** Under Invoices' lock, on `tx`, in its own `queries/`:

| Holder | Locks, in order | Judges (refusal) | Stamps | Release |
| --- | --- | --- | --- | --- |
| Projects (`projects.milestone`) | each distinct project `LockProject` (`FOR NO KEY UPDATE`, `srv/projects/queries/projects.sql:64-80`) by id ascending, then the milestones by id ascending — its own order (`srv/projects/milestones.go:38-48`) | already `invoiced` → `source_already_invoiced`; not `ready` → `source_not_invoiceable`; revision, currency or effective amount (fixed price × percent, judged under the project's lock) differs → `source_changed` — a reorder moves no revision (`srv/projects/queries/milestones.sql:88-97`) | `status = invoiced`, `invoiced_invoice_id`, `invoiced_number`, `invoiced_at`, `invoiced_by_user_id`, `invoice_date` = issue date, `invoiced_amount` frozen, `invoice_reference` NULL, `ever_moved`, revision + 1, and the milestone's `invoiced` timeline event (`recordMilestoneEvent`, `:705-708`) by `IssuedBy`/`IssuedByDisplay` at `IssuedAt` | `invoiced → ready`, the five stamp columns and the two new ones cleared, the percent→amount conversion when the fixed price is gone (`R/projects.md:409-429`), the timeline event |
| Expenses (`expenses.entry`) | the claims of claim lines by id, then every line by id — the module's order (`srv/expenses/claims.go:28-33`) | `invoiced_at` set → `source_already_invoiced`; `expenses.ready_to_invoice(...)` false → `source_not_invoiceable` (per diem, unit not approved, not billable, unpriced — `srv/expenses/invoiced.go:36-61`'s reasons); the **billing facts** — `billable`, `bill_amount`, `project_id`, `currency`, `kind` — differ → `source_changed`. Not the revision: a payroll reimbursement bumps it (`srv/expenses/queries/reimbursements.sql:185`, `:209`) and changes nothing billed | `invoiced_at`, `invoiced_by_user_id` the issuer, `invoiced_invoice_id`, `invoiced_number`, `invoice_reference` NULL, revision + 1 | the four cleared — what `UnmarkEntryInvoiced` clears, plus the two new |
| Time (`time.entry`) | the entries by id (`LockEntries`, the order `srv/time/approval.go:21-27` keeps) | `invoiced` → `source_already_invoiced`; not `approved`, not billable or no bill rate → `source_not_invoiceable`; revision, project, currency or `hours × bill_rate × COALESCE(multiplier, 100)/100` differs → `source_changed` | Time's **first writer** of the state: `status = invoiced`, `invoiced_at`, `invoiced_invoice_id`, `invoiced_number`, revision + 1 | the new move `invoiced → approved`, the three cleared; the approval stamps stay |

Every holder judges in that order — already invoiced first, so a row stamped by hand is
named for what it is rather than as "not invoiceable" — and compares amounts exactly,
decimal text against its own exact figure (Time's has up to six decimals,
`R/time.md:374-379`). The stamps' timestamps are `IssuedAt`, never the holder's clock.
The period lock does not apply to either direction: a stamp is not a time edit
(`R/time.md:390` — the lock is how a month is closed *before* it is invoiced), as
Expenses' stamp already ignores it (`srv/expenses/invoiced.go:18-21`). `ReleaseInvoiced`
**tolerates** a source that no longer carries `ref`'s stamp — it writes nothing for it and
logs a warning — because a credit note must never be blocked (Projects' reason,
`R/projects.md:409-424`); a stamp never moves without Invoices, so the case is a repair,
not a flow.

**The new columns**, each in its module's own migration (rule 4): `time.entries`,
`expenses.entries` and `projects.billing_milestones` gain `invoiced_invoice_id bigint`
and `invoiced_number bigint` (opaque ids, no cross-schema key), with a CHECK that both are
set or neither; Time's existing `invoiced_at` (`mig/00010_time_baseline.sql:29`) and the
two others' stamps (`mig/00012_expenses_baseline.sql:95-97`,
`mig/00011_projects_milestones.sql:36-40`) are reused.

**The manual doors.** Expenses' `POST /expenses/entries/{id}/invoiced` and `…/undo`
(`srv/expenses/invoiced.go:63-111`) and the milestone's `invoiced → ready` move
(`srv/projects/milestones.go:608-732`) answer **409 `invoiced_by_invoices`** (naming the
number) on a row carrying `invoiced_invoice_id`; for a row stamped by hand with a
free-text reference they stay as they are — design E5 keeps the manual step "for
installations that invoice elsewhere" (`docs/superpowers/specs/2026-09-19-project-economy-design.md:50-53`).
A manual mark of a row a draft holds is not refused (the source module does not know the
draft); the issue then answers `source_already_invoiced`. Time gets no endpoint: its
`invoiced → approved` move exists only through `ReleaseInvoiced`, and "`invoiced` has no
way out" (`R/time.md:189`) becomes "only a credit note takes it back".

**Rule 10, as the module-boundaries page carries it** (inserted after rule 9, `MB:94-113`;
the "How they are enforced" bullets after rule 9's, `MB:150-155`):

> 10. **Work marked invoiced, inside the issue.** The third sanctioned cross-module
>     direction, made for invoicing work: `contracts.InvoicedWorkHolder`. A module whose
>     rows an invoice is built from — Time's entries, Expenses' lines, Projects' billing
>     milestones — declares `Module.InvoicedWork`, and Invoices calls the holders
>     **inside its issue's transaction**, which already holds the document, the settings
>     row, the number counter and, for a credit note, its original locked: after every
>     check and the number, before the document is written. `MarkInvoiced` judges each
>     source as it stands under the holder's own lock — still approved, ready or
>     billable, at the revision and amount the draft took it at, not already invoiced —
>     and stamps it with the invoice's id, number and date; `ReleaseInvoiced` takes the
>     stamp back in the issue of the credit note that returns the source's line in full.
>     A source a holder will not stamp is answered as a `*contracts.WorkSourceRefusal`,
>     and the issue refuses with its code and the line's position — the number, and
>     every holder's write, roll back with it. A holder keeps rule 8's rules: it runs its
>     own SQL on its own schema from its own package, on the caller's `pgx.Tx`; it never
>     begins or ends a transaction and never reads a directory or any other contract;
>     its release tolerates a source that no longer carries the stamp; and it runs
>     whether or not its module is enabled. **The cross-module lock order** is Invoices'
>     own first — the document, the settings row, the counter, then a credit note's
>     original — and then the source modules in one fixed order: Projects (the project
>     rows, then their milestones), Expenses (the claims, then the lines, by id), Time
>     (the entries, by id). No source module ever locks a row of `invoices`, and a
>     transaction that locks both an invoices document and a source module's row takes
>     the document first — which is why the customers merge and anonymisation call the
>     holders of the modules that provide invoiced work after every other module's,
>     Invoices' included.
>     The rule every locked transaction keeps is restated for it: **no call that takes
>     its own connection or leaves the process while a transaction holds locks**. A
>     directory read takes a second connection from the pool and an object-store call
>     leaves the process, so neither is ever made under a lock; a holder's command runs
>     on the caller's transaction and does neither, which is what rules 8, 9 and 10 have
>     in common. Today's holders are time, expenses and projects
>     ([Invoicing work](/en/reference/invoices/#invoicing-work)).
>
> - **Rule 10**: by shape and by test, as rules 8 and 9. `MarkInvoiced` and
>   `ReleaseInvoiced` are handed the caller's `pgx.Tx`; their SQL is in each module's
>   `queries/`, so rule 4's scan covers it; depguard keeps Invoices and the three
>   source modules apart, tests included. `module.Compose` and `module.Workers` collect
>   the slot from every module given, and both put the invoiced-work providers'
>   `CustomerReferences` and `CustomerPersonalData` after every other module's. Each
>   holder's package test builds it from `Deps` with `Pool` nil — so it can only write
>   through the transaction — and stamps and releases real rows through one it rolls
>   back first; each holder marks `ctx` with its own module's locked-transaction flag,
>   so that module's own contract-call hook catches a directory read inside it.
>   Invoices' tests run with `modtest.WithInvoicedWork` fakes that run
>   `SELECT pg_current_xact_id()` on the `pgx.Tx` they are handed; `issueAfterAllocation`
>   becomes `func(ctx context.Context, tx pgx.Tx, invoiceID int64) error` and the test
>   hook records `pg_current_xact_id()` through that `tx`, so the two values must be
>   equal and the test proves the holder rode the issue's own transaction;
>   the harness fails a test when anything but a transaction-bound command
>   (`noteTxCommand`) is called under a lock. The integration package, on a pool of
>   `MaxConns = 2` serving only the two racing writers — each raw lock-holding transaction
>   sits on its own `pgx.Connect` connection outside the pool, so any call that takes a
>   second pool connection under a lock starves and fails the test — races an issue
>   against each writer of the same rows — the expense's
>   manual stamp and a batch reimbursement, a milestone move and a project's fixed-price
>   edit, a time unapprove, and a customer merge — and requires both to finish.

**The lock rule in code.** `withLockedTx` (`inv/server.go:98-115`) hands its callback the
`pgx.Tx` (`fn(ctx, tx, txq)`), its comment (`:106-109`) carrying the restated rule.
`contractscalls.go` gains `noteTxCommand(ctx, name)` beside `noteContractCall`
(`inv/contractscalls.go:39-44`); the holder calls go through one accessor reporting
`InvoicedWork.<kind>.Mark|Release` by it. The test hook records both with a flag
(`inv/harness_test.go:120-158`), and the harness's cleanup (`:73-78`) fails on a locked
`noteContractCall`, as now, and on a `noteTxCommand` made outside a locked transaction.

**The merge, and why it cannot cycle.** A merge locks both customer rows
(`srv/customers/merge.go:347-358`), then calls the holders in Compose's order (`:387-393`),
the module order — projects before invoices (`apps/server/cmd/vantigo/main.go:289-300`).
Projects' holder UPDATEs the absorbed customer's projects
(`srv/projects/queries/customer_references.sql:1-13`), the row lock `LockProject` takes;
Invoices' then locks both customers' documents newest first
(`inv/queries/customers.sql:1-14`). An issue holds its document D, then wants project P:
the merge would hold P and wait for D. **Resolution: Compose collects the
`CustomerReferences` of modules that declare `InvoicedWork` after every other module's**
(a stable partition of `srv/module/compose.go:247-255`), so a merge takes the invoices
documents before any project row, the issue's order; `CustomerPersonalData` is
partitioned the same way (`withCustomerPersonalData`, `:552`, so `Workers` sees it too),
so no later erase that writes a source row can invert it. Nothing else the issue locks before
D's holders is a merge's: the settings and the counter are Invoices' alone, a credit
note's original is older than the credit note (the merge's newest-first scan has not
reached it while it waits on D), and Time's and Expenses' rows are no holder's. Taking
the source locks before the document instead would hold project rows across the
counter's queue. The module order stays (it also orders the combined OpenAPI document,
`srv/module/compose.go:314`); today's anonymisation would need nothing, Projects' erase
writing nothing (`srv/projects/customer_personal_data.go:71-77`). The race test reads `pg_locks`
to show a merge waiting on D holds no project row.

**Codes.** On `InvoicesConflictProblem` (`cannotIssue`): `source_not_invoiceable`,
`source_changed`, `source_already_invoiced`, `source_customer_changed`,
`source_not_selectable`, each with `linePosition`, `sourceKind` and `sourceId`;
`invoice_changed` (the source set moved under the lock) and `projects_unavailable`. On Expenses' and Projects' conflict
problems: `invoiced_by_invoices` with `invoiceId` and `invoiceNumber`.

### D2 — `invoices.line_sources`, held from the draft

`invoices.line_sources`: `id bigint`, `line_id` (FK `invoices.lines`, `ON DELETE
CASCADE`), `invoice_id` (denormalised for the per-document reads and the triggers),
`source_kind varchar(30)`, `source_id bigint`, `source_revision int`, `source_subkind
varchar(20)` (an expense's kind; NULL for the other kinds), `project_id
integer` (opaque), `quantity numeric(12,3)` (hours, km, 1), `amount numeric(20,6)` (the
source's exact amount — Time's carries up to six decimals, `R/time.md:374-379`),
`currency char(3)`, `state varchar(10)` (`held` | `invoiced` | `released`), `source_date
date` (the work's date, for the period and D12). **The floor** is a partial unique index
`ux_line_sources_live (source_kind, source_id) WHERE state IN ('held','invoiced')`: a
source is held by one live draft or invoiced by one unreleased issued line, whatever the
interleaving.

**Frozen at issue, with one transition.** A trigger of its own,
`refuse_issued_line_source_change`, reads the parent `FOR SHARE` as
`refuse_issued_child_change` does (`mig/00034_invoices_baseline.sql:298-329`) and, under
an issued parent, refuses every INSERT, DELETE and UPDATE but one: `state` from
`invoiced` to `released`, nothing else changed — the row's counterpart of
`refuse_issued_document_change`'s `customer_id` exception (`:256-281`). The release is
recorded on the credit side (D8); the state change is what lets the index forget the
row, so the work can be pulled again.

**Written by the wizard and carried by every save.** `POST /invoices/from-work` writes
the rows with the lines (D3). Because `writeLines` deletes every line and inserts new ids
(`inv/drafts.go:350-378`), the request's line gains `sources[]` — **identities only**,
`{kind, id}` — and the save carries the server's own snapshot (revision, quantity,
amount, project, date) across the re-insert, read under the document's lock before
`DeleteLines`. **When the draft holds sources, `sources` is required on every line**: an
absent one is a 400 on `lines[i].sources` (a client that does not know the field cannot
drop work by omission), `[]` means the line carries none. A `PUT` may move a source
between lines or drop it; it can never add one: an entry the draft does not already hold
is a 400 on `lines[i].sources` ("work is added through the uninvoiced view"), naming one
source twice is a 400 too. A client never states a revision or an amount, and a `PUT`
takes no new hold. Every save inserts the document's `line_sources` in **one statement
ordered by `(source_kind, source_id)`**, so two transactions inserting overlapping holds
wait on the index in the same order and one fails with the unique violation instead of
both deadlocking (`40P01`, a 500). A document holds at most **5 000 sources**
(`too_many_sources`, 409 at the wizard, 400 at a save) — the providers' page size.

**Held, and released.** A held row goes when its line is removed (or the source is
dropped from every line), when the draft is deleted (the cascade), or when a `PUT` changes
the draft's customer — then every held row of the draft is deleted. Every save that drops
a hold, by any of these routes, warns `sources_released` naming the dropped identities
(`{kind, id}`), so a client that lost work by mistake can see what. A merge re-pointing the draft (`inv/customer_slots.go:50-69`) moves
the projects with it and releases nothing.

**One source, one live document**, enforced under lock: where a hold is added — the
wizard's insert and its append to an existing draft (D3) — the transaction locks the
draft, then reads the rows of these sources in state `held` or `invoiced` belonging to
any other document, and refuses with 409 **`source_held_elsewhere`** carrying
`heldBy: {invoiceId, number?, status}` and the source; the index's violation, when two
holds race, maps to the same code. At the issue the index already proves the draft's own
holds exclusive; the holders judge the source module's side (D1).

**A hand-edited line keeps its sources.** The invoice may bill less or more than the work
(a write-down, a rounding): the line's net differing from `round2(Σ amount)` of its
sources is the warning **`line_differs_from_sources`** (with the line's position), never a
refusal. Deduction lines (D7) carry no sources.

**Staying fresh.** The `sources` block is answered from these rows (the `ehf` block's
rule, `inv/transmissions.go:51-129`). `GET` of a draft with sources — the read that
already calls the directory (`inv/drafts.go:616-654`) — also reads them by id through
D3's contracts and warns `source_changed` / `source_not_invoiceable` per line, which the
issue refuses (the `vat_code_not_valid` pair, `inv/responses.go:31-34`,
`inv/issue.go:186-190`). A `PUT` with `refreshSources: true` re-reads them before its
transaction and takes the new revisions and amounts, dropping (`sources_released`) what
is no longer invoiceable, and regenerates the timesheet rows (D5) of the refreshed entries.

### D3 — The uninvoiced view, the line-level reads and the wizard

**Three read contracts** (`srv/contracts/billable.go`), single-provider slots resolved
after `Projects` (`srv/module/compose.go:201-231`), optional for Invoices (nil when the
module is off), read **on the pool, never under a lock**, served from the provider's own
tables without calling any directory back; each page says `More` past 5 000 rows:

```go
type BillableRequest struct {
    ProjectIDs []int32  // at most MaxActualsRequests; or
    IDs        []int64  // exactly these, still billable (a held source's freshness)
    Until      time.Time // work dated on or before; zero = no bound
}
type BillableHours interface      { BillableHours(ctx, BillableRequest) (BillableHoursPage, error) }
type BillableExpenses interface   { BillableExpenses(ctx, BillableRequest) (BillableExpensesPage, error) }
type BillableMilestones interface { BillableMilestones(ctx, BillableRequest) (BillableMilestonesPage, error) }
```

- **`BillableHour`** (Time): `ID`, `Revision`, `ProjectID`, `BillingLineID`, `UserID`,
  `Date`, `HoursHundredths`, `BillRate`, `Currency`, `BillMultiplierPercent`,
  `WorkTypeID`, `WorkTypeName`, `TaskTitle`, `Amount` (exact, `R/time.md:374-379`). The
  set is `status = 'approved' AND billable AND bill_rate IS NOT NULL` (`R/time.md:374`).
  Never the `note` (D5).
- **`BillableExpense`** (Expenses): `ID`, `Revision`, `ProjectID`, `ClaimID`, `Kind`,
  `Date`, `Description`, `Supplier`, `SupplierInvoiceNumber`, `NetAmount`,
  `MarkupPercent`, `DistanceKm`, `BillRatePerKm`, `BillAmount`, `Currency`,
  `SupplierInvoiceRebilled` (D15). The set is **`expenses.ready_to_invoice(...)`** —
  approved, billable, not per diem, priced, **without** the `invoiced_at` term, which
  each caller states beside it, so the holder can judge "already invoiced" first (D1) —
  an IMMUTABLE SQL function on `expenses.owes_employee`'s precedent
  (`mig/00033_expenses_supplier_invoices.sql:20-52`) replacing the predicate's copies
  (`srv/expenses/queries/entries.sql:179-184`, `:206-211`,
  `projectexpenses.sql:67-76`, `reimbursements.sql:273-284`), `invoicedRefusal`
  (`srv/expenses/invoiced.go:36-61`) held to it by a test.
- **`BillableMilestone`** (Projects): `ID`, `Revision`, `ProjectID`, `Name`,
  `Description`, `PlannedDate`, `ReadyAt`, `Amount` (the effective amount, fixed price ×
  percent resolved by Projects), `Currency`. The set is `status = 'ready'`.

**`GET /invoices/work?customerId=|projectId=&until=`** (`invoices:access+invoices:create`;
exactly one of the two ids, else 400). For a customer: `ProjectsForCustomer`
(`srv/contracts/projects.go:100`), then the three reads over those projects; for a
project: `Project`, its customer, the three reads. Then, from its own schema, **less what
live documents hold** (`line_sources` in `held` or `invoiced`). The answer: per project
(code, name, billing type, currency), per kind, the rows with their selectability and
`heldOnDrafts: [{invoiceId, count}]`; per currency the totals; the users named through
`UserDirectory.Users` (`srv/contracts/users.go:12-16`); warnings
`work_overdue_to_invoice` (D12, per project), `currency_not_nok` (D11, per row),
`supplier_invoice_rebilled` (D15, per row), `work_truncated` (a `More`), and 409
`work_unavailable` when none of the three providers is composed. `GET /invoices/meta`
gains `workAvailable` (any provider present) and `work: {hours, expenses, milestones}`.

**`POST /invoices/from-work`** (`invoices:access+invoices:create`). Body: `customerId`,
`sources: [{kind, id, revision}]`, `grouping` (D4, default `project`), `timesheet`
(default the setting, D5; from 3C), `vatCodes: {hours, expenses, milestones}` (default the
settings, D6), optional `deliveryFrom`/`deliveryTo`, optional `invoiceId` + `revision`
to **append** to an existing invoice draft. In order, before any transaction:

1. 400 on the body; the settings; the profile and `customerGate` (`inv/drafts.go:56-79`).
2. The sources' projects (`ProjectDirectory.Projects`): one billing another customer →
   409 `source_not_for_customer`; fixed-price hours or non-billable work → 409
   `source_not_selectable` (D14).
3. The sources by id: missing → 409 `source_not_invoiceable`; another revision than the
   body's → `source_changed` for hours and milestones (an expense's revision is
   display-only, D1: its billing facts are taken as read now); then `mixed_currency` or
   `currency_not_nok` (D11).
4. The lines (D4) — over 500 with the target's → 409 `too_many_lines` with
   `suggestedGrouping` — and the timesheet rows (D5), named through `UserDirectory`.

Then one `withLockedTx`: insert the draft (or lock the target `FOR UPDATE`, still a draft
of this customer, at `revision`), write the lines through `writeLines`, insert the
`line_sources` as `held` under D2's check, the timesheet rows, the document's project
(D9). Prefills: `deliveryFrom`/`deliveryTo` the first and last source date, so a later
line period would sit inside the header's by construction (R3 §2.2); `yourReference`
and the terms as `PostInvoices` takes them (`inv/drafts.go:404-412`). 201 with the
document. **Line text** is in the buyer's language (`buyer_language`, `inv/issue.go:127-131`'s
rule applied to the profile):

| Kind | nb | en | quantity, unit, price |
| --- | --- | --- | --- |
| hours | "Konsulenttimer, <project>, <period>" (+ " – <work type>" / " – <person>" / " – <date>" by grouping) | "Consulting hours, <project>, <period>" | Σ hours, `timer` / `hours` (both `HUR`, `inv/ehf/units.go:15`), the effective rate |
| outlay | "Viderefakturerte kostnader, <project>, <period>" | "Re-billed costs, …" | 1, "", Σ bill amounts (itemised: the expense's description) |
| mileage | "Kjøregodtgjørelse, <project>, <period>" | "Mileage, …" | itemised: km, `km` (`KMT`), the rate per km; grouped: 1, "", Σ |
| supplier invoice | "Viderefakturert leverandørfaktura <supplier> <number>" | "Re-billed supplier invoice …" | 1, "", the bill amount |
| milestone | the milestone's name | the milestone's name | 1, "", the effective amount |

The markup is Expenses' own, already in the bill amount (`srv/expenses/money.go:49-60`).
The period is "september 2026" for one month, "1.–15. sep. 2026" otherwise.

### D4 — Grouping and the line text

`grouping` ∈ `project` | `work_type` | `person` | `date` | `itemised`. The key is always
(project, kind) first; `work_type` adds the work type, `person` the user, `date` the day,
`itemised` the source itself — one line per hour entry, expense or milestone. Expenses
group by (project, expense kind) under every grouping but `itemised`; a milestone is
always its own line. **A line has one unit price**, so within a key hours split further by
effective rate (`bill_rate × multiplier / 100`): two people at different rates on one
project are two lines under `project` — "within a line, by work type, since an overtime
hour is billed at its own price" (`R/time.md:380-381`). The effective rate is rounded
half away from zero to the column's four decimals (`unit_price numeric(14,4)`); a line's
net then differs from Time's exact amount only when a multiplier yields a fifth decimal,
and `line_differs_from_sources` says so. Lines are ordered project code, kind (hours,
expenses, milestones), then the key. The 500-line cap (`inv/drafts.go:47-48`) refuses
with `too_many_lines` naming the next coarser grouping that fits (`itemised` → `date` →
`person` → `work_type` → `project`).

### D5 — The timesheet

Optional per invoice: the flag (default `timesheet_default`, off) is
`invoices.invoices.timesheet boolean`, frozen at issue like every added column
(`mig/00034_invoices_baseline.sql:256-281`). When it is on, the draft
carries **`invoices.timesheet_rows`**: `invoice_id` (FK, cascade), `position`,
`source_id` (the time entry), `person_label varchar(100)`, `entry_date`, `hours
numeric(5,2)`, `work_type varchar(100)`, `description varchar(200)` (the task title,
else the project's name) — **never the note**, which may hold health data
(`mig/00010_time_baseline.sql:17`; R3 §6.3) — under `refuse_issued_child_change`. Written
with the draft; on every save pruned to the hours the draft still holds; a `PUT` turning
the flag on reads the held hours through `BillableHours` by id before its transaction and
writes them, off deletes them. **The person label** by `timesheet_person_label`, applied
when the rows are written (a snapshot): `initials` (default — the display name's initials,
"KN", a second "KN" becoming "KN2"), `number` ("Person 1", "Person 2" in order of first
appearance on the invoice — Vantigo stores no employee number), or `name`.

**Rendered inside the same PDF**: a new `pdfModel` block (`inv/pdf.go:149`) after the
totals and payment block, on its own pages ("Timeliste" / "Timesheet": date, person, work
type, description, hours, a total per person and overall), paginated by maroto's
`AddRows` (R3 §7.6); the draft preview renders it too. The store-once key, the stored
PDF's hash and the single EHF attachment (`inv/ehf/render.go:116-130`) are unchanged:
the timesheet is part of the attached PDF, which is what § 10's "referanse" needs (R3
§2.3). Kept with the document — five years after the financial year, the conservative
reading of U3. In the customer's personal-data export each document gains `timesheet`
(the rows as printed), since the customer received them; the erase deletes a draft's with
the draft and keeps an issued document's (`inv/customer_slots.go:467`).

**The employees' data.** A timesheet discloses employees' work to a customer; the basis is
the employer's (GDPR art. 6(1)(f) or (b)), and art. 13's notice to the employees is the
employer's to give — the reference and the user guide say so and point at it. The
minimised label, `initials`, is the default (art. 25(2)); `name` is an opt-in on the
settings. Issued rows are kept as part of the sales document under bokføringsloven § 13,
the erasure exception of art. 17(3)(b); deleting or disabling a user in identity never
touches a snapshot. The `timesheet` body field, the document's flag and the
`timesheet_*` settings all arrive with 3C.

### D6 — VAT codes and markup

`invoices.settings` gains `work_vat_code_hours`, `work_vat_code_expenses`,
`work_vat_code_milestones` (`integer NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes`
— id 1 is the seeded "Utgående mva 25 %", code `3`,
`mig/00034_invoices_baseline.sql:339-350`), `timesheet_default boolean NOT NULL DEFAULT
false` and `timesheet_person_label varchar(10) NOT NULL DEFAULT 'initials'` (CHECK
`initials|number|name`), on the single row, `PUT /invoices/settings` with its revision
under `invoices:manage` (`inv/settings.go:21-26`); an inactive code is a 400 on its field.
While the seller is not VAT-registered (`vat_registered` false) the wizard pre-fills id 9,
the seeded category-O code `7` (`mig/00034_invoices_baseline.sql:349`), for every kind
instead, since the issue refuses any other category then (`inv/issue.go:202-204`).
The wizard pre-fills them, the request may override per kind, and a line's code is then
editable like any. **Every re-billed expense takes the chosen code** — the main supply's
rate, never the receipt's (mval. § 4-2 (1), R3 §4.2); Expenses' VAT amount
(`srv/expenses/money.go:23-30`) never reaches the line. **Utlegg** (mval. § 4-1 (2) a) is
**not supported** in phase 3: the reference says so and no line text says "utlegg".
Markup is Expenses' `markup_percent`, in its bill amount; Invoices adds none.

### D7 — A-konto and the final settlement

**An a-konto invoice is an ordinary invoice** — any lines, typically a ready milestone —
in the same series (§ 5-1-3), VAT in its term (mval. § 15-9 (1)). **A final settlement** adds **deduction lines**: `deducts_invoice_id bigint REFERENCES
invoices.invoices` frozen on the line, `quantity` −1, `unit_price` the amount deducted
(> 0, so BR-27's positive price holds), no discount, `vat_code_id` the a-konto line's
code, the text "Tidligere fakturert a konto, faktura <n>" / "Previously invoiced on
account, invoice <n>", one line per (a-konto, VAT code). On the line request:
`deductsInvoiceId`; the quantity may be −1 only with it, and it is refused on a
credit-note draft's own request. A deduction line is **exempt from the today's-code checks**
— inactive (`inv/drafts.go:309-310`) and no rate on the issue date
(`inv/issue.go:180-190`) — because it is taxed at its a-konto line's snapshot (below);
`taxedLines` (`inv/drafts.go:289-300`) takes that snapshot for it on every read and save. The editor's "Deduct earlier invoices" step lists the
customer's issued invoices with something left to deduct and proposes the lines.

**The cap** — per (a-konto, VAT code), the a-konto's lines' net at that code, less what
its issued credit notes credited on those lines, less what issued settlements' deduction
lines took and their credit notes did not give back — is warned on the draft
(`deduction_exceeds_invoice`) and **refused at the issue** with the same code and the
line's position. A deducted document must be an issued invoice of the same customer,
not a credit note and not the settlement itself (400 on the field at save, 409 at issue
if it changed). The deduction is **taxed at the a-konto line's snapshot** (category,
rate), as a credit line is (`inv/credits.go:120-133`), never today's rate of the code.
**A settlement's gross must be positive**, warned on the draft and refused at the issue
with `invoice_total_not_positive`. A zero settlement could never be corrected: a
credit note is refused once nothing is left to credit (`invoice_fully_credited`,
`inv/credits.go:79-86`; the headline cap, `:560-567`), so neither it nor the a-konto it
deducted (`invoice_deducted`, below) could ever be credited. A fixed price billed fully on
account ends with its last a-konto, not a zero settlement; a negative one is a credit in
substance (`document_state` would call it paid, `mig/00035_invoices_payments_delivery.sql:190-201`).

**The lock order** is the issue's — the settlement, the settings, the counter — and
**the deducted invoices are read under the counter, not row-locked** (Reading 4): every
write that changes what an a-konto has left to deduct is itself an issue (a credit note,
another settlement), and the counter serialises every issue (`inv/queries/counters.sql:15-19`).

**The CHECK** `ck_lines_quantity` (`mig/00034_invoices_baseline.sql:223`) becomes
`quantity > 0 OR (quantity < 0 AND deducts_invoice_id IS NOT NULL)`; `amount()`'s
minimum (`inv/drafts.go:238`) is lifted for such a line only. **Credit notes learn
negative lines**: `CopyLinesToCredit` copies `quantity <> 0` and `deducts_invoice_id`
(`inv/queries/credits.sql:28-40`), so a credit of a settlement reverses its deductions,
and the a-konto's undeducted amount grows back. A credit-note line may carry a negative
quantity **exactly when the line it credits is a deduction line** — decided once the
original is read (`putCreditDraft`, `inv/credits.go:668+`, and the issue's credit book),
never in `parseDraft`. Every comparison of a credit line to its original is then **by
magnitude and requires the same sign**: the never-raise rule (`inv/credits.go:749`), the
line cap (`:553`) and `lastReturn`'s quantity sum (`:266`). A credit note whose gross is
**negative** is refused (`credit_total_negative`); a zero one — a "Frakt 0,-" line —
stays allowed, as in 1B. **Crediting a deducted a-konto** is judged per (a-konto, VAT
code): a credit taking more at a code than no issued settlement deducted there is refused,
`invoice_deducted`, naming the settlements — credit the settlement first.

**EHF**: `cac:BillingReference` 0..n on an invoice, one per distinct deducted invoice (its
number and issue date), written where the credit note's single one is today
(`inv/ehf/render.go:109-115`); the negative line amounts flow into the VAT rows and the
totals unchanged, so BR-CO-10 and BR-CO-13 hold; `PrepaidAmount` is not written (R3 §3.3:
it lowers the payable, not the VAT base, and these a-kontos were VAT invoices). The PDF
lists the deducted invoices under the references and prints a deduction as
`formatDecimal` already prints a negative (`inv/pdf.go:183-203`): a leading minus on the
quantity and the line amount ("-1", "-125 000,00"; "-125,000.00" in English), the unit
price positive.

### D8 — Partial-credit release

A source is released only when **its line is fully returned** — `creditBook.lastReturn`
(`inv/credits.go:244-267`), which `total` already computes per line as `squared`
(`:382-389`) — never on a price reduction (`isReturn`, `:229-242`). In
`creditIssueChecks`, for every credit line whose original line's last unit this note
returns, the original line's `line_sources` in state `invoiced` are released: step 6
inserts **`invoices.line_releases`** (`invoice_id bigint NOT NULL` — the credit note,
which `refuse_issued_child_change` reads to find the parent,
`mig/00034_invoices_baseline.sql:298-329`; `credit_line_id` FK `invoices.lines`,
cascade; `line_source_id` FK `invoices.line_sources`; under that trigger) and moves those rows to `released` (D2's one transition), and the holders'
`ReleaseInvoiced` runs (D1). The credit note's lines are final by then — no save follows
an issue — so `credit_line_id` is stable. A grouped line credited in part releases
nothing; its work stays invoiced until the rest is returned. A milestone is released
whole. A credit-note draft's `sources` block marks what its issue would release. A new
invoice may pull the released work again and carries **no mandatory reference** to the
credit note (R3 §5); the wizard suggests the note "Erstatter faktura <n>, kreditert
med kreditnota <c>" when it re-pulls released work.

### D9 — The project dimension

`project_id` per `line_sources` row (D2). On the document: nullable `project_id integer`
and `project_reference varchar(30)` (the project's code, a snapshot: the PDF and the EHF
render from the document's own rows, never the directory — `inv/pdfstore.go:37-41`),
**derived, never written by a request**: set by every save to the one project all the
draft's sources share, NULL when they span two or there are none; frozen at issue; a
credit note copies its original's. The EHF writes `cac:ProjectReference/cbc:ID` (BT-11;
R080, one per document) on an invoice when set, after the `AdditionalDocumentReference`
(the UBL order the XSD layer enforces); a credit note, whose syntax has no
`ProjectReference`, writes a second `cac:AdditionalDocumentReference` with
`cbc:DocumentTypeCode` 50 and no attachment (BIS §11.3.7). The PDF prints "Prosjekt" /
"Project". `GET /invoices` gains a `projectId` filter; the CSV gains `Project` (the
reference) **last** (`inv/csvexport.go:26-31`); the document and list item gain
`projectId` and `projectReference`.

### D10 — Authority

No new permission. The view and the wizard are `invoices:access+invoices:create`: whoever
builds the invoice sees the hours, persons and rates it will state, as any issued PDF
shows them to `invoices:access`. The stamp and the release run under `invoices:issue`
through the contract, **not** the source modules' own rights (Projects' financial rights,
Time's approver, Expenses' project rights) — the contract and their docs say so.
`ProjectEntry.Currency` and `DefaultBillRate` surface only under `invoices:create`, the
financial gate their comment asks for (`srv/contracts/projects.go:13-15`).

### D11 — Currencies

The view groups by currency. The module issues in NOK only (`R/invoices.md:74-75`), so
a non-NOK source is listed with `currency_not_nok` and is not selectable. The wizard
judges a selection's currencies before anything else about money: more than one →
409 `mixed_currency`; one, not NOK → 409 `currency_not_nok`. Mileage is always in the
installation's currency (`srv/expenses/entries_validation.go:330-343`).

### D12 — The deadline warning

`work_overdue_to_invoice` per project on the view when the oldest selectable item's
work date — an hour's or an expense's date, a milestone's `ready_at` — is more than one
calendar month before today (`Clock()`'s business day): § 5-2-2's "senest en måned etter
levering", the discrete rule. The continuous-service rule (§ 5-2-4, one month after the
two-month VAT term) is not applied: nothing tells Vantigo which projects are continuous.
A warning, never a refusal.

### D13 — The 2028 buyer org-number rule is out of phase 3

FOR-2026-09-29-1933's § 5-1-2 from 2028-01-01 tightens `buyerComplete`
(`inv/issue.go:135-147`), but the directory knows no "bokføringspliktig" fact. It goes
to the invoices backlog and to "What comes next".

### D14 — Fixed-price projects' hours

Projects decides the billing model: `ProjectEntry.BillingType`
(`srv/contracts/projects.go:22`), `fixed-price` | `time-and-materials` | `non-billable`.
On a fixed-price project the hours are **listed as information** (the hours against the
plan) and are not selectable; its ready milestones are the invoiceable items. Time keeps
defaulting such hours billable (`srv/time/values.go:260-271`). Work on a `non-billable`
project — possible when a project changed model after the work was approved — is listed
the same way.

### D15 — Duplicate supplier invoices

`BillableExpense.SupplierInvoiceRebilled` is true when another supplier-invoice entry
with the same supplier (trimmed, case-folded) and `supplier_invoice_number` is already
invoiced; the view warns `supplier_invoice_rebilled` on the row, and on both rows of an
uninvoiced pair. A warning: Expenses allows the number twice (`mig/00033`; 1A item 22).

### D16 — Out of scope, named

Construction's § 8-1-2a progress rules, ten-year timelists and retention money;
`PrepaidAmount` and the VAT-free payment request (not a salgsdokument; U5); several EHF
attachments — a separate timesheet file, forwarded receipts (U7; Storecove's PDF
regeneration, `ROADMAP.md:809-811`); utlegg outside the VAT base (U4); line-level
`InvoicePeriod` and `AccountingCost`, BT-12 and BT-18/BT-128; mixed-currency invoices;
the 2028 rule (D13); the event bus; a sixth permission; a product or billing-line VAT
default; adding work to a credit note.

### D17 — The contracts

**`openapi/invoices.yaml`**: `getInvoicesWork`, `postInvoicesFromWork`; schemas
`InvoicesWorkResponse`, `InvoicesWorkHour`, `…Expense`, `…Milestone`,
`InvoicesFromWorkRequest`, `InvoicesLineSource` (`kind`, `id`, `projectId`, `quantity`,
`amount`, `state`), `InvoicesTimesheetRow`; the line request and response gain
`sources[]` and `deductsInvoiceId`; the request `timesheet`, `refreshSources`; the
document `sources` (per line, `held`/`invoiced`/`released`, `wouldRelease` on a
credit-note draft), `timesheet`, `timesheetRows`, `projectId`, `projectReference`; the
list item the last two and `GET /invoices` a `projectId` parameter; the settings five
fields; meta `workAvailable`, `work`. Conflicts: `source_not_invoiceable`,
`source_changed`, `source_already_invoiced`, `source_customer_changed`,
`source_held_elsewhere` (+ `heldBy`), `source_not_for_customer`, `source_not_selectable`,
`mixed_currency`, `currency_not_nok`, `too_many_lines` (+ `suggestedGrouping`),
`work_unavailable`, `too_many_sources`, `projects_unavailable`,
`deduction_exceeds_invoice`, `invoice_total_not_positive`,
`credit_total_negative`, `invoice_deducted`. Warnings: `line_differs_from_sources`,
`sources_released` (+ the dropped identities), `invoice_total_not_positive`,
`source_changed`, `source_not_invoiceable`,
`deduction_exceeds_invoice`, `work_overdue_to_invoice`, `currency_not_nok`,
`supplier_invoice_rebilled`, `work_truncated`.

**The source modules** change only where the wire does: `openapi/expenses.yaml` — the
entry gains `invoicedBy: {invoiceId, number}`, the two invoiced operations a 409
`invoiced_by_invoices`; `openapi/projects.yaml` — the milestone `invoicedBy`, the status
move the 409; `openapi/time.yaml` — the entry `invoicedBy` (the release move has no
endpoint).

### D18 — Frontend

- **The uninvoiced panel** (`apps/invoices/frontend`, `UninvoicedWorkPanel`): per project
  and kind, rows with checkboxes, totals per currency, the non-selectable rows greyed with
  their reason (fixed price, currency, "on draft n" as a link), the warnings. The host
  mounts it on the customer's Invoices tab above `CustomerInvoicesPanel`
  (`apps/host/frontend/src/routes/customers/-customer-invoices-tab.tsx`) and on a new
  project tab "Invoicing" (`$projectId.invoicing.tsx`, gated by `-module-tab-gate.tsx` on
  `invoices` and `invoices:create`, in `visibleProjectDetailTabs`,
  `-project-detail-layout.tsx:144`); Projects' Economy tab links to it by a host-passed
  `invoicingHref`, as `expensesHref` is (`-project-economy-route.tsx:16`, `:44`).
- **The wizard**, a modal from the panel: the selection and its totals, the grouping with
  the line count it would make, the timesheet flag, the three VAT codes, the period;
  "Create draft" or "Add to draft n"; every 409 in words.
- **The draft editor** (`apps/invoices/frontend/src/pages/invoice.tsx`): each line's
  sources, the difference and freshness warnings with "Refresh work", the timesheet
  toggle and rows, the project; a **"Deduct earlier invoices"** step showing what each
  a-konto has left per VAT code; on a credit-note draft, what its issue would release.
- **The source modules' badges**: "Invoiced by invoice n", a link where the host passes
  an `invoiceHref` (Time's status badge, `apps/time/frontend/src/lib/status.ts:2`, `:13`;
  Expenses' project panel and entry; Projects' invoice plan); the manual undo hidden on
  a module-stamped row, the 409 in words if reached.
- **Settings**: a "Work to invoice" card (the three codes, the timesheet default and
  label). en + nb throughout.

### D19 — Documentation, as its own deliverable

- `MB`: rule 10 and its enforcement bullet (D1); rule 3's non-DTO sentence (`MB:44-46`)
  names `InvoicedWorkHolder`; rule 5 — nine single-provider slots (the three
  `Billable*`), four many-provider (`InvoicedWork`), and the holder-order partition; rule
  8's "rule 9 is that design for the second" gains "and rule 10 for the third"
  (`MB:92-93`); "Invoices requires customers" (`MB:285-311`) — it now reads the project
  directory and the billable contracts, optionally, and calls the holders; the "no
  contract call inside a transaction that holds a lock" sentence (`MB:261-264`) takes the
  restated rule; Time's and Expenses' contract counts (`MB:245-283`: each now provides its
  `Billable*` beside its aggregate and fills `InvoicedWork`); rule 5's enforcement bullet (`MB:136-141`) names the three `Billable*`
  among the duplicates Compose refuses; rule 1's "the future Invoices module" (`MB:24`);
  and R3's stale facts: the module list (`MB:10-12`), rule 4's schemas (`MB:48-49`), the
  default `MODULES` (`MB:167`), "phases 1A and 1B" (`MB:290`). Rule 10's Time, Expenses
  and Projects sentences land with 3A; its Invoices-side sentences (the call's place in
  the issue, `noteTxCommand`, the races) with 3B.
- `R/invoices.md`: a new "Invoicing work" section (view, wizard, grouping and text, the
  link and its states, the write-back and its refusals, release, timesheet, VAT codes and
  utlegg, a-konto and settlement, the project); the model, endpoints, permissions,
  settings, PDF, EHF (BT-11, BG-3), the CSV's `Project`, retention; "What comes next".
- `R/time.md`: the state machine (`:175-201`) with `invoiced → approved`; "What invoicing
  will read" (`:369-393`) as what Time now provides. `R/expenses.md`: the invoiced track
  (`:811-821`), the predicate function, `BillableExpenses`, the 409. `R/projects.md`:
  milestones (`:359-503`), the module stamp, the 409, `BillableMilestones`.
  `R/customers.md`: the merge's holder order (`:1434`).
- `en|nb/user/invoices.md`: "Invoicing work", "Final settlement", the settings card, the
  editor's sources; `en|nb/user/time.md`, `expenses.md`, `projects.md`: "Invoiced by
  invoice n" and where the manual mark and undo are refused; `en|nb/user/projects.md`'s
  tab table (`:75`) gains the **Invoicing** tab. `apps/host/frontend` is a source only of
  `user/getting-started.md`, `user/index.md` and `user/workspace-administration.md`, none
  of which describes a project's tabs, so the host commit carries `Docs-Impact` naming
  `user/projects.md` as the page that says it.
- `ROADMAP.md`: phase 3 done, the 2028 item in the backlog. No admin page: no setting or
  environment variable is added.

## Readings on the record

1. The write-back is R3 §9.1 (a); (b), (c) and (d) are not taken, for D1's reasons.
2. The lock rule reads "no call that takes its own connection or leaves the process
   while a transaction holds locks"; `noteTxCommand` is the one call the harness allows
   under a lock, and only under one.
3. **The merge's lock order**: Compose collects the `CustomerReferences` of modules that
   provide `InvoicedWork` after every other module's, so a merge takes invoices documents
   before project rows, as an issue does; `CustomerPersonalData` is partitioned the same
   way, so a future erase writing a source row cannot invert it. The module order stays.
4. **A settlement does not row-lock the invoices it deducts** (not "by id ascending"):
   the counter serialises every writer of what the cap reads, and a row lock would cycle
   with the merge's newest-first document lock whenever an a-konto is newer than the
   settlement draft.
5. The holder declares `Kinds()`: Invoices orders the calls (Projects → Expenses → Time)
   itself, and Compose's module order (projects, time, expenses) is not that order.
6. `WorkSource` carries the amount (a percent milestone's moves with the fixed price, not
   its revision), the project and the currency; `InvoiceRef` carries the issuer, the
   issuer's name and the issue's own timestamp, all resolved before the lock, so no
   holder reads a directory or a clock of its own for a stamp or a timeline event.
7. A source's project still billing the customer is judged before the lock, as the
   profile is; a project re-pointed in between is the race the profile already accepts.
8. A `PUT` echoes source identities, never adds a hold, never states a revision or an
   amount; work is added through `from-work`, to a new draft or appended to one. When the
   draft holds sources, `sources` is required on every line (`[]` drops), and every
   dropped hold is named in `sources_released`.
9. Uniqueness is a partial unique index over `held` and `invoiced`, judged in words under
   the draft's lock first; an issued row's one permitted change is `invoiced → released`.
10. A released hold on a draft is deleted; `released` is an issued row's state.
11. Hours split by effective rate within a key; the rate is rounded to four decimals,
    and a difference is a warning.
12. A re-billed cost's line text never says "utlegg"; utlegg is unsupported and every
    re-billed expense takes the chosen code.
13. The timesheet stores rows only when on; the label defaults to initials; `number` is
    the person's number on the invoice, Vantigo storing no employee number; never the
    note; kept five years (U3).
14. The deadline warning is the discrete rule: a calendar month after the work's date.
15. `invoices:create` sees per-person hours and rates; no sixth permission.
16. The deduction cap is per (a-konto, VAT code), net of credits and other settlements;
    a deduction is taxed at the a-konto line's snapshot, quantity −1, price positive, and
    is exempt from today's-code checks. **A settlement's gross must be positive**
    (`invoice_total_not_positive`): a zero settlement could never be credited, nor the
    a-konto it deducted; a fixed price billed fully on account ends with its last
    a-konto. (Amended after the critical review; it allowed zero.)
17. The quantity CHECK is relaxed for deduction lines — an invoice's and a credit note's
    copy of one — not "on invoices only": a credit of a settlement reverses them.
18. An a-konto is credited, per VAT code, only as far as no settlement deducted it. A
    credit-note line is negative exactly when the line it credits is a deduction line,
    decided once the original is read; every comparison with the original is by
    magnitude and of the same sign. A credit note's gross may not be **negative**
    (`credit_total_negative`); zero stays allowed, as in 1B. (Amended after the review,
    which narrowed "not positive".)
19. Release on a line's full return only; a milestone whole; no mandatory reference
    from the new invoice to the credit note.
20. A holder's release tolerates a source without the stamp; no source module blocks a
    credit note.
21. Fixed-price and non-billable projects are known by `ProjectEntry.BillingType`.
22. `mixed_currency` is judged before `currency_not_nok`, so both can answer.
23. The document snapshots the project's code; a credit note names its project in an
    `AdditionalDocumentReference` with code 50.
24. `module.Workers` collects `InvoicedWork` though no worker issues today.
25. The manual doors answer 409 `invoiced_by_invoices` only on a row with an invoice id;
    Time's release move has no endpoint.
26. `work_unavailable` only when no billable provider is composed.
27. The default VAT code of all three kinds is id 1, the seeded 25 % code `3`; while the
    seller is not VAT-registered the wizard pre-fills id 9, the category-O code `7`.
28. Freshness is read on `GET` of a draft and on `refreshSources`, never in the list.
29. The 2028 buyer org-number rule is the backlog's.
30. Every holder judges "already invoiced" first, then "not invoiceable", then
    "changed"; `expenses.ready_to_invoice` leaves the `invoiced_at` term to its callers.
31. Amounts cross the contract as exact decimal text and are kept as `numeric(20,6)` in
    `line_sources`, compared exactly — Time's carry up to six decimals.
32. Expenses judges `source_changed` by the billing facts (`billable`, `bill_amount`,
    `project_id`, `currency`, `kind`), never the revision, which a reimbursement moves;
    Time and Projects judge the revision too (a milestone reorder moves none).
33. `line_sources` are inserted in one statement ordered by `(source_kind, source_id)`,
    so racing holds fail on the index instead of deadlocking; a document holds at most
    5 000 sources.
34. With Projects disabled, an issue of a draft holding sources fails closed, 409
    `projects_unavailable` (chosen at the review).
35. Under the lock the issue compares the source set with the one read before it
    (`invoice_changed`) and re-checks each project's billing type.
36. `line_releases` carries the credit note's `invoice_id`, so the child trigger guards
    it as it guards lines.
37. The timesheet — the body field, the flag and the settings — is 3C's; `name` is an
    opt-in, the employer gives the art. 13 notice, issued rows are kept under art.
    17(3)(b), and identity's deletions never touch a snapshot.
38. The PDF prints a deduction with `formatDecimal`'s leading minus on the quantity and
    the amount, the unit price positive.

## Testing

Through the invoices harness, plus `modtest.WithInvoicedWork` (a fake holder per kind
recording each call and the `pg_current_xact_id()` of the `pgx.Tx` it was handed, which the
`issueAfterAllocation(ctx, tx, invoiceID)` hook records too; able to refuse or fail),
`WithBillableHours`, `WithBillableExpenses`, `WithBillableMilestones`, the existing
`WithProjects` (`srv/modtest/modtest.go:206`) and the fixed clock.

- **D1, the holders**: each module's package test builds its holder from `Deps` with
  `Pool` nil and stamps and releases real rows through a real transaction it rolls back
  first; every refusal by removing its guard, in the order already invoiced → not
  invoiceable → changed (a hand-stamped row named `source_already_invoiced`; a percent
  milestone after a fixed-price edit; Expenses unchanged by a reimbursement's revision
  bump and changed by each billing fact; a milestone reorder not a change); exact amount
  comparison at six decimals; the columns, `IssuedAt`, Projects' timeline event with
  `IssuedByDisplay` and no directory call (the module's own locked flag set, its hook
  silent); the release's prior state, the percent→amount conversion, the tolerance; the
  period lock ignored; a holder built from a disabled module's `Deps`. **Invoices**: one
  call per holder in Projects → Expenses → Time order, after the number and before any
  snapshot, each on the issue's transaction (equal `pg_current_xact_id()`); a refusal →
  409 with code, position and source, the number rolled back (`SetIssueAfterAllocation`),
  no state moved; a holder's error → 500, all rolled back; an unclaimed kind → 500;
  `source_customer_changed` and `projects_unavailable` before any number; a save slipped
  between the pre-read and the lock → `invoice_changed`; a project turned fixed-price →
  `source_not_selectable`; the harness failing a locked `noteContractCall` and an
  unlocked `noteTxCommand`. **The
  doors**: `invoiced_by_invoices` on stamped rows, unchanged on hand-stamped ones.
  **Compose**: the partition; `Workers` carries the slot.
- **D1, the races** (`srv/integration`, real modules, a pool of `MaxConns = 2` for the
  two racing writers only — each raw lock-holding transaction on its own `pgx.Connect`
  connection outside the pool — each pair held at a lock, both finishing, one outcome winning, the
  loser's refusal the documented one): an issue against the expense's manual mark and a
  batch reimbursement (`srv/expenses/flow.go:218-226`), a milestone `ready → invoiced`
  and a project's fixed-price edit (`LockProject`), a time unapprove; and **against a
  customer merge** of the draft's customer whose project
  holds a held milestone — the merge seen in `pg_locks` waiting on the document while
  holding no project row, both committing, no `40P01`.
- **D2**: held rows carried across new line ids, moved, dropped; `sources` absent on a
  line of a sourced draft → 400, `[]` drops; an unheld or duplicate source refused;
  delete and customer change release, `sources_released` naming each; the 5 000 cap;
  `source_held_elsewhere` naming the first draft, and two racing holds → one, the index
  violation mapped — also with the two drafts inserting overlapping sets in opposite
  selection order, which must end in one unique violation and never `40P01`; an issued row refusing all but `invoiced → released`; the
  difference and freshness warnings; `refreshSources`.
- **D3**: each provider's set, `More`, `IDs`; the expenses predicate against
  `invoicedRefusal` over every combination and in each former place; the view per
  customer and project, less held work, `heldOnDrafts`, its warnings, 400 on both or
  neither id, `work_unavailable`; from-work's refusals in order, each by removing its
  guard; the prefills; append at a revision; `invoices:create`.
- **D4**: each grouping's lines and texts in both languages; the split by rate and the
  rounding's warning; the order; `too_many_lines` and its suggestion.
- **D5**: rows only with the flag, pruned on save, written when turned on, regenerated
  by `refreshSources`; each label
  and the initials' collision; no note anywhere; the PDF model's block (`pdfModelBuilt`),
  pagination, the preview; store-once unchanged; the export; the erase.
- **D6**: the settings' defaults, validation, revision and permission; the wizard's
  defaults and overrides; code id 9 pre-filled for a seller not VAT-registered.
- **D7**: a deduction's save rules; exempt from the inactive and no-rate code checks
  and taxed at the snapshot across a rate change; the cap per code after a credit and an
  earlier settlement, warned and refused; a zero and a negative settlement refused
  (`invoice_total_not_positive`); a credit of a settlement copying the deduction as a
  negative line and restoring the cap; a negative credit line refused against an
  ordinary line and accepted against a deduction; never-raise, the line cap and
  `lastReturn` by magnitude with a sign change refused; `credit_total_negative`, and a
  zero credit note still issued; `invoice_deducted` per VAT code; the PDF's minus signs
  in both languages; an EHF golden with two `BillingReference`s and a negative line
  through `mise run ehf:validate`.
- **D8**: a full return releases (the original's ref, `line_releases` with the credit
  note's `invoice_id`, refused by the child trigger after its issue, `released`); a
  partial return and a price reduction do not; the last of two partial returns does;
  the work selectable again; the note suggestion.
- **D9**: the derived project (one, two, none), its snapshot, frozen; BT-11 and the
  code-50 reference in goldens; the filter; the CSV's last column.
- **D11, D12, D14, D15**: each warning and refusal at its boundary.
- **The integration test** (`invoicesInstallation` with projects, time and expenses,
  `figures_test.go`'s `buildFixture`): approve two people's hours and a re-billable
  expense, ready a milestone → view → wizard (`project`, timesheet on from 3C) → issue → every
  source stamped with id and number by the issue's own commit, `ActualsTotals.Invoiced`
  and `ProjectExpenses` moved → an a-konto of a second milestone, then a settlement
  deducting it → a full credit of the first invoice → its sources released in all three
  modules → pulled into a new draft.
- **Frontend**: the panel, the wizard and its refusals, the editor's sources, warnings
  and deduction step, the badges with and without an href, both catalogs.
- **Docs**: D19's pages against the code; `mise run docs:check`.

## Phasing

As R3 §10, each phase a working state with its docs:

- **3A — the contract and the reads.** Rule 10's source-module sentences in MB; `contracts.InvoicedWorkHolder` and
  the slot in `module`, Compose's partition and `Workers`, `modtest.WithInvoicedWork`;
  `expenses.ready_to_invoice` and its places; the three `Billable*` contracts and
  providers; the three holders with their migrations, Time's first writer and release
  move, the manual doors' 409; the source modules' `invoicedBy` on the wire; their docs.
- **3B — the wizard and the write-back.** Rule 10's Invoices-side sentences;
  `line_sources` and its trigger, `line_releases`; `withLockedTx`'s `pgx.Tx` and
  `noteTxCommand`; the issue's and the credit note's calls; `inv/work.go` (the view,
  from-work without `timesheet`, the freshness reads); grouping and line text; the three
  VAT-code settings; the project dimension; the panel, the wizard, the
  editor's sources, the badges; the race and integration tests; Invoices' docs.
- **3C — the timesheet and a-konto.** The `timesheet` field, flag and settings,
  `timesheet_rows` and the PDF block; deduction
  lines, the CHECK, the cap, the credit-note changes, BG-3; the editor's settlement step;
  the docs' remaining sections and `ROADMAP.md`.

## Open questions

R3's UNCERTAIN items (§11), and what this design does meanwhile:

- **U1** a period as "tidspunkt": the wizard always sets one. **U2** hours as one total
  line: `project` grouping states hours, rate and period; the timesheet is offered.
- **U3** the timesheet's retention: five years, with the document. **U4** utlegg on the
  same document: unsupported (D6). **U5** a VAT-free payment request in the series: not
  built (D16).
- **U6** the deduction line's wording: "Tidligere fakturert a konto, faktura <n>".
  **U7** Storecove's attachment limits: one attachment, the PDF (D5).
- **U8** bokføringsloven § 10 from 2027-01-01: the timesheet sits inside the referenced
  PDF; re-read before 2027. **U9** the GBS 10 revision: no rule here depends on it.
  **U10** GBS 1 from the tracked-changes PDF: release on full return and negative
  deduction lines follow its §1.1 and §2.2 as read.
- **U11** diett and kilometergodtgjørelse at the main supply's rate: the expenses code
  pre-fills, changeable per invoice.
- Also: a reference from the new invoice to the credit note as a rule (a suggestion
  here, D8); the "separate supply" edge of re-billed goods (a per-line code, D6).
