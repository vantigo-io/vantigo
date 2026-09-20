# Expenses module

The Expenses module is outlays and mileage, standalone or gathered into a
travel claim, with receipts, an approval flow, two independent tracks after
approval — paying the employee back and invoicing the customer — dated rates,
categories and admin settings. It is a vertical-slice module
inside the single Vantigo binary (`apps/server/internal/expenses`), owns the
`expenses` schema in the shared PostgreSQL database, and serves `openapi/expenses.yaml`
under `/api/v1/expenses`.

It depends on nobody but identity. [Projects](projects.md) is an **optional** read
through `contracts.ProjectDirectory`: `MODULES=customers,expenses` is a valid
installation, and so is `MODULES=expenses` alone. The per diem day is a later
delivery's; this one refuses `kind: "per_diem"` with a message that says so
rather than treating it as an unknown value.

## Domain model

- **Entry** (`expenses.entries`) — one money line: an **outlay** or a **mileage**
  line (`kind`), owned by `userId`, dated `entryDate`, with a `description`, a
  `status`, and the audit stamps every mutating path leaves. The id is a `bigint`.
  - An **outlay**: a `categoryId`, an optional `supplier`, who `paidBy` it
    (`employee` or `company`), a `currency`, a `grossAmount`, an optional
    `vatAmount`, and up to ten receipts.
  - A **mileage line**: a `distanceKm`, optional `fromPlace`/`toPlace`, `passengers`
    (0–8), and no amount of its own — the dated rate table prices it, in the
    installation's own currency, and it never carries a receipt.
  - Both kinds can carry a `projectId`, optionally a `billingLineId`, a `billable`
    flag, and — only when billable and only on the project's side — a
    `markupPercent` (outlay) or `billRatePerKm` (mileage) and the resulting
    `billAmount`.
- **Travel claim** (`expenses.claims`) — the container a trip's expenses sit in:
  a `purpose`, an optional `destination`, `departureAt` and `returnAt` as the
  instants they were entered as, `abroad` with the claim's own `abroadDayRate`
  and `abroadCurrency`, an optional `projectId`, and the same status, decision
  and reimbursement columns an entry carries — because a claim is a unit of
  approval and of payroll in exactly the same way. A trip may be at most 366
  days and hold at most 200 expenses.
- **Attachment** (`expenses.attachments`) — one receipt: the object key the bytes
  live under, the file name, the sniffed content type and size, who uploaded it and
  when. Deleting the entry cascades its receipts.
- **Category** (`expenses.categories`) — what an outlay is booked on (`Materials`,
  `Subcontractor`, `Equipment hire`, `Travel`, `Accommodation`, `Meals`, `Phone and
  internet`, `Other`, seeded in that order). A category is never deleted, only
  deactivated — a line that already carries one keeps it and stays editable, but a
  deactivated category cannot be put on a *different* line. Its `position` is a
  **dense, server-owned 1..n** over every category, not a number the caller's
  request is stored verbatim — the server owns the numbering exactly as Projects
  owns a task's place among its siblings. Moving a category, or inserting one at
  a given place, locks the whole list in its current order, takes the category
  out and puts it back at the requested place (the end, for one past it), and
  renumbers every row in the same transaction; a replace that repeats the place a
  category already holds renumbers nothing.
- **Dated rate** (`expenses.rates`) — one row per kind and `validFrom` date: the rate
  in force on a day is the row of that kind with the greatest `validFrom` on or
  before it. Ten kinds exist in the schema: `mileage`, `mileage_passenger`,
  `mileage_customer` (money per kilometre), four per-diem kinds and three meal
  percentages. Seeded at migration time, all `validFrom: 2026-01-01` and
  `source: "State rate"`: `mileage` 5.30 NOK and `mileage_passenger` 1.00 NOK
  (verified against Skatteetaten's published rates), and `per_diem_6_12` 397
  NOK, `per_diem_over_12` 736 NOK, `per_diem_overnight_hotel` 1012 NOK,
  `meal_breakfast_percent` 20, `meal_lunch_percent` 30 and
  `meal_dinner_percent` 50 (verified against the state's *Særavtale om dekning
  av utgifter til reise og kost innenlands*, §§ 6 and 9). Two kinds are
  deliberately unseeded: `mileage_customer` — what a customer is charged per
  kilometre — because that is the company's own price, not a public rate, and
  `per_diem_overnight_other`, because the agreement knows one overnight rate
  and a night somewhere that is not a hotel is priced at a figure the company
  sets. A
  row an administrator edits or removes can always be restored with
  `POST /rates/reset`, which puts a kind's shipped rows back exactly as the
  migration wrote them (value, currency, source) without touching a row on a day
  the product does not ship.
- **Settings** (`expenses.settings`) — one row: the period lock (`lockedBefore`),
  the installation's `defaultCurrency`, `defaultMarkupPercent` (an
  `expenses:manage`-only figure — see below), and the optional `receiptRequiredOver`
  threshold.

Every foreign identifier — users, projects, billing lines — is opaque: read through
`contracts`, never through SQL (see [module boundaries](module-boundaries.md) rule 4).

## Money rules

Every amount is handled as an exact decimal (`math/big.Rat`, never a float) from the
JSON text it arrived as through to the numeric column it is stored in, and every
computed figure **rounds once, at the end, half up** — the half rounded away from
zero, so 2.625 becomes 2.63. A markup applied twice, or a sum of two figures each
already rounded, would silently be a cent off from what a person adding the same
numbers by hand gets; this module never does either.

- **Net** is the gross less the VAT (or the gross alone, with no VAT entered),
  rounded once. It is the base a markup applies to.
- **Mileage** pays the kilometres at the rate in force, plus — for each passenger
  carried — the kilometres again at the passenger supplement, the whole sum rounded
  once. A rate with no supplement priced for it (only possible with zero passengers)
  contributes nothing.
- **Markup** (a billable outlay): the net times `(1 + markupPercent / 100)`, rounded
  once — a named markup, or the line's own kept figure, or the installation's
  `defaultMarkupPercent`.
- **Customer rate** (billable mileage): the kilometres times the customer rate per
  kilometre, rounded once — a rate that has nothing to do with what the employee is
  paid. No supplement applies to what a customer is billed; the passenger supplement
  is only ever paid to the employee.
- Every money field accepts at most two decimals (a rate at most two, a distance at
  most one); a third decimal on a money field, or a second on a distance, is a 400
  naming the field rather than a silent round. Every amount column is bounded
  (`numeric(12,2)` for money, `9999.9` km for distance, `1000` % for a markup), and a
  computed figure that would overflow one is a 400 naming the field that drove it —
  never a database error.

## The optional Projects link

`GET /meta` answers `projectsAvailable`, decided once from whether this installation
was started with the `projects` module. Without it:

- every project-shaped field — `projectId`, `billingLineId`, `billable`,
  `markupPercent`, `billRatePerKm` — is refused on its own field, on a create and on
  a replace alike;
- `GET /projects` (the picker) answers a 404 problem naming the missing module,
  rather than an empty list a client might mistake for "no projects";
- billing never appears — `canSeeBilling`, `canSetBilling`, `canMarkInvoiced` and
  `canUndoInvoiced` are all `false`, even on a row that still carries a stored
  `projectId` from before the module was switched off, and the `billing` object is
  absent with them.

**What happens to a stored project id when Projects is switched off.** The columns
are not cleared — a project link is data, not a fact this module owns the right to
delete — but nothing can be judged against a project nobody can ask about, so a
save on such a line keeps five of its six project columns exactly as stored:
`projectId`, `billingLineId`, `billable`, the markup percentage and the customer
rate per kilometre are all carried through untouched rather than refusing the
edit outright. The sixth, what the line bills the customer, is **not** copied —
it is *recomputed* from those same carried figures against whatever new gross or
distance the save is writing, so the figure never goes stale against a rewritten
amount. The same carry-through applies when the projects module stays on but the
*particular* project a line was booked on has since been deleted from the
directory.

**Who may book on a project.** The rule is exactly the one Time uses to decide who
may log time on a project (`contracts.ProjectDirectory.CanLogTime`) — judged on the
expense's *owner*, not on whoever is recording it, because `expenses:manage` can
record for a colleague. A project or a billing line the save is *keeping* is not
judged again, so a project that has since been completed, or a billing line since
deactivated, does not strand an existing link; a *changed* one is judged in full.

## The unit an expense belongs to

A travel claim's lines are ordinary rows of `expenses.entries` carrying its
`claim_id`. What makes them lines rather than expenses of their own is that
**everything status-shaped about them is the claim's**: the flow, the period
lock (judged on the day the trip *departed*), the decision, the reimbursement,
the capabilities, whether the receipts may still be touched, and the status a
read of the line shows. The line keeps its own kind, date, description,
amounts, receipts, `billable` and billing figures, and its own `invoiced`
stamp.

One function answers it — `unitOf` in `authorize.go`, which takes an entry and
the claim it names and answers the *unit* the rest of the module judges it by —
so a rule added later cannot forget. A line's own `status` column stays at its
default `draft` and is never read. Everything that used to read an entry's
status now takes a unit: `entryStateRefusal`, `accessFor`, `entryResponse`,
`entryTakesReceipts`, `rateOverrideRefusal`, `invoicedRefusal`, and the two
queries that repeat a status guard in SQL (`OverrideEntryRate` and
`MarkEntryInvoiced`, which read it through the claim with a subquery).

**A line is never a unit.** `POST /submit`, `/approve`, `/reject`,
`/unapprove`, `/reimbursed` and `/reimbursed/undo` refuse a line by id with a
per-id message pointing at the claim ("Expense 5 belongs to travel claim 7;
submit the claim") — all-or-nothing, as every batch here is — and the line's
five flow capabilities are false. Pricing, invoicing and a rate override stay
the line's own doors, because those are about this one amount; each of them
reads the *claim's* status for the state it is judged in.

**A line moves neither in nor out.** A `claimId` on a replace must equal the
one the line already carries; a standalone expense naming one, or a line naming
another claim, is a 400 on `claimId`. The owner and the project are the
claim's: a `userId` naming somebody else is refused, and a `projectId` must be
absent (inherited) or exactly the claim's.

## The lock order inside a claim

Every write takes its locks in one order, and nothing may invent another:

1. the claim's own row (`SELECT … FOR UPDATE`), then
2. its lines, in id order — all of them for a project re-point, or the one line
   the write is about.

So a write on the claim and a write on one of its lines start at the same row
and queue rather than deadlock. Changing a claim's project re-points every line
in that same transaction: the new project's billing lines are read **before**
the transaction opens (nothing inside one calls another module), a line's
billing line is cleared when the new project does not have it, and clearing the
project altogether makes every line non-billable with its billing figures. A
line that has already been invoiced holds the project where it is. The cap of
200 lines is decided under the claim's lock, so two lines racing for the last
slot cannot both take it.

## The flow, and what freezes on submit

```text
        submit            approve
draft ───────────► submitted ───────► approved
  ▲                    │
  │                    │ reject
  │                    ▼
  │                 rejected
  │       edit          │
  └─────────────────────┘
```

- **draft** — the owner's (or `expenses:manage`'s, recording for them) to edit,
  delete, attach receipts to and submit.
- **submitted** — waiting for an approver; no longer its owner's to change.
- **approved** — decided. From here the two independent tracks below run.
- **rejected** — sent back with a reason (required, at most 1000 characters).
  Editing a rejected line makes it a draft again, so it is submitted afresh.

**Submit is where the line freezes.** For a mileage line, the rate, the passenger
supplement and the resulting amount are priced one last time from the dated rate
table in force on the entry's own date, and the result is what the row carries from
then on — a later change to the rate table, an approval, or a settings change never
recomputes it. The same freeze reprices what the line bills the customer, if it is
billable and the project bills at all. Two things are checked at the same moment,
under the entry's own row lock, so nothing can slip past between the read and the
write: the receipt rule (below), and — after this task's fix — that a kind change
racing an upload cannot leave a mileage line holding receipts.

An expense may not be submitted, approved, rejected, unapproved or edited on a date
the period lock closes (see below), and the owner (or `expenses:manage`) is who may
submit; anyone approaching this from the wrong side gets the ordinary 403/404 split
described under "Refusal codes".

## The receipt rule

Set once, on the installation's settings, as `receiptRequiredOver`:

- **Off** (absent) — no outlay ever needs a receipt to be submitted.
- **Always** (`0`) — any employee-paid outlay above zero needs one; in practice,
  every one of them.
- **Over an amount** — an employee-paid outlay whose gross *exceeds* the threshold
  needs a receipt; a gross equal to the threshold does not.

The rule only ever applies to an **employee-paid outlay** — a company-paid outlay
and every mileage line are exempt, because mileage takes no receipt at all. It is
judged at submit time, under the entry's own row lock, so a receipt deleted a moment
earlier cannot let a line through, and it is judged **only** at submit — a receipt
may be added or removed freely on a draft or a rejected line regardless of the
threshold.

## Approval

Who may approve or reject a **submitted** expense: `expenses:approve` for anything,
or the manager role on the expense's own project (the same role Time and Projects
use) — nothing else. **`expenses:manage` does not, on its own, let anyone approve or
reject**; it is a separate, deliberately narrower set of powers (below).
**Self-approval is allowed**: an approver who is also the expense's owner may
approve their own line, exactly as Time allows.

`GET /approvals` is the queue: submitted expenses the caller may approve, grouped
one card per person, the person who has waited longest first, **paged by person in
SQL** so a page never splits a group across two pages. A caller who approves
nothing at all — no `expenses:approve`, no project managed — gets the access
layer's 403 rather than an empty page. `/approve`, `/reject` and `/unapprove` are
all-or-nothing batches of up to 500 ids: naming one id twice counts once, and a
batch that cannot move even one of its ids answers a message per refused id rather
than moving the rest.

**Unapprove** additionally accepts `expenses:manage` (`orManage`), takes an approved
line back to a fresh **draft** — clearing the decision and the submission stamp, so
it goes round the loop again — and refuses a line that has already been reimbursed
or invoiced: undoing those has its own door, on each track.

**Rate override.** `PUT /entries/{id}/rate` replaces a *submitted mileage* line's
rate and, optionally, its passenger supplement — an approver's (or
`expenses:manage`'s) correction when the table's own figure is wrong for this one
trip. It is guarded by the revision the line was read at (409 on a stale one) and
records an audit: who overrode it, and — the first time a request replaces it —
what the table's own value had been (`rateOverride.tableValue`,
`rateOverride.passengerTableValue`), so a later reader can see what changed without
a second lookup. Leaving the passenger rate out of a request keeps whatever
supplement the line already carries, so an approver correcting the rate alone does
not silently drop it. The override audit is cleared by an unapprove and by the
line's next submit, both of which make the figure a fresh, unoverridden one again.
Like every other single-line write on a submitted or approved expense, the period
lock applies — with `expenses:manage`'s usual exemption.

## Pricing by the project side

`PUT /entries/{id}/billing` is the door through which a project prices what an
expense bills its customer — a full replace of the billing fields alone, touching
no amount the owner entered, no status and no receipt. It belongs to whoever can
see the project's financial figures on the expense's project (its manager,
`projects:manage-all`, or `projects:view-financials` on a project they can see) —
**not `expenses:manage`**, which has no say in what a project charges its customer,
and not the expense's owner. It is **open in every status the line can still be
priced in — draft, rejected, submitted or approved — and refused only once the
line has been invoiced**. The period lock does not reach it: pricing is bookkeeping
done after a period closes, and an invoice for December goes out in January.

`GET /entries/{id}/billing-lines` is the picker for that dialog, and deliberately
not `GET /projects` again: the project picker answers what the *caller* may book
an expense on (a member-or-manager right on a project still open for work), while
pricing belongs to whoever may see the project's *money* — its manager,
`projects:manage-all`, or `projects:view-financials` — which is a different right
held by a different person. Keying the picker on `GET /projects` would offer a
finance person on no project team nothing at all, and would still offer a line on
a project that has since been completed; this operation is keyed on the *expense*
instead and judged by exactly the rule `PUT /entries/{id}/billing` is judged by,
so the dialog can never offer a line the save then refuses.

## The two tracks after approval

Once approved, an expense can move down either or both of two independent tracks,
in any order — reimbursed, invoiced, both, or neither:

- **Reimbursed** (`expenses:manage` only) — what the employee is paid back.
  `POST /reimbursed {entryIds, date, reference?}` records one payroll run over
  every named expense that is approved and owes its owner something (a
  company-paid outlay owes nothing and cannot be marked); `POST /reimbursed/undo`
  clears the whole stamp — the date, the reference and who made it — putting the
  expenses back in the waiting list exactly as they were. `GET /reimbursements`
  lists what a run would cover (or what has already been paid), grouped per
  person and paged the same way the approval queue is.
- **Invoiced** (financial rights on the project, not `expenses:manage`) — what has
  been billed to the customer. `POST /entries/{id}/invoiced {reference?, revision}`
  marks one **billable, priced** approved line invoiced; `.../invoiced/undo` takes
  it back. A billable line that carries no `billAmount` yet — a mileage line saved
  while no customer rate was in force — cannot be marked until it is priced.

**Neither track consults the period lock.** Both are bookkeeping done *after* a
period closes, and the people who do each — `expenses:manage` for payroll,
financial rights for invoicing — are exactly the two the lock has never held back.
A December expense is reimbursed and invoiced in January without anyone needing to
touch the lock date.

## The period lock

`expenses:manage` sets one date, `lockedBefore`; every day strictly before it is
closed, the date itself is open, and everyone reads it (the client greys out days
nobody but `expenses:manage` may touch).

**What it protects:** creating, editing, deleting and submitting an expense;
approving, rejecting and unapproving one; and overriding a mileage rate — every
step of what the employee submitted and what an approver decided.

**What it deliberately does not protect:** reimbursing (and its undo), pricing
from the project's side (`PUT /billing`), and invoicing (and its undo). Those three
are bookkeeping done once a period has closed, made by the two roles — payroll and
project finance — the lock was never meant to hold back; a lock that reached them
would shut the books on the very people whose job starts when the books close.

## Permissions

| Permission | Meaning |
| --- | --- |
| `expenses:access` | Use the Expenses app and record and submit your own expenses. Every operation requires it |
| `expenses:approve` | Approve or reject anyone's expenses, including those with no project |
| `expenses:view-all` | See everyone's expenses (sensitive) |
| `expenses:manage` | Change expense settings, rates and categories, record expenses for a colleague, mark expenses reimbursed, and work past the period lock (sensitive) |

`expenses:view-all` and `expenses:manage` are both sensitive: the first opens every
colleague's outlays — what they bought, from whom and for how much, which is
personal in a way a time entry is not — and the second reaches the installation's
money settings and can move a payroll run.

## Visibility and shaping

Who sees an expense at all: its owner, always; the manager of its project, whoever
recorded it; and `expenses:view-all`, `expenses:approve` and `expenses:manage`, who
see everyone's. A travel claim follows exactly that rule, and a line is visible
if and only if its claim is — a line carries its claim's owner and its claim's
project, so the same predicate decides both, in SQL as in Go. Anyone else gets a **bare 404**, byte-identical to the answer an
unknown id gets — the list applies the same predicate in SQL, so it never holds a
row a single read of it would 404 for.

| Field | Who sees it |
| --- | --- |
| `billing` (markup, customer rate, bill amount, invoice) | financial rights on the entry's project **only** — not the owner, not `expenses:view-all`, not `expenses:manage` |
| `reimbursement.at` / `.date` / `.by` | everyone who may see the expense — being told the money went out is not a privilege |
| `reimbursement.reference` | the owner, and `expenses:view-all` / `expenses:approve` / `expenses:manage` — **not** a project manager, who sees the line because of its project and has no business in the company's payroll batches |
| `decision` (status, at, by, reason) | everyone who may see the expense |
| `rateOverride` | everyone who may see the expense — what they are paid was decided by a person, not the table, and they are entitled to know |
| `attachments` / `attachmentCount` | everyone who may see the expense, and downloading one follows the same rule listing it does |
| `settings.defaultMarkupPercent` / `meta.defaultMarkupPercent` | `expenses:manage` only, from one function so the two reads can never disagree |
| `mileage_customer` rate rows | `expenses:manage` only |

## Receipts

A receipt is JPEG, PNG, HEIC or PDF, at most 10 MiB (10,485,760 bytes, "10 MB" the
friendly figure the contract itself uses), and an outlay carries at most ten. Only
an outlay takes one at all — a mileage line never does, and a save that
would turn an outlay with receipts into a mileage line is refused (on `kind`) rather
than stranding them.

**The type is decided by the bytes, not by what the client called the file.** The
upload is sniffed from its own leading bytes (HEIC by its ISO base-media brand,
which `net/http`'s own sniffer does not recognise; everything else by
`http.DetectContentType`), and a declared content type or a file extension may only
*agree* with what was sniffed or say nothing at all — a PNG named `.pdf`, or a PDF
sent as `image/png`, is refused, while an invoice saved as "Faktura nr. 12345" (no
extension) or a phone sending `application/octet-stream` are both accepted, because
neither contradicts anything.

Every receipt is stored under a **fresh random key**, `receipts/<entryId>/<uuid>`
relative to this module's own object-store scope (`expenses`), so the physical key
is `expenses/receipts/<entryId>/<uuid>` — never derived from the file name, so no
upload can collide with another or be guessed. See
[Object storage](storage.md#module-scopes-and-the-physical-key) for the scope
mechanism. Receipts are **served only through the API**, `GET /attachments/{id}`,
which streams the bytes with the type they were sniffed as, never cached, never
sniffed again by the browser (`X-Content-Type-Options: nosniff`), and under a
sandboxing content-security-policy (`default-src 'none'; sandbox`) that keeps a
malicious PDF's own scripting from ever touching this app's origin — on the 404 and
the 503 as well as on a successful read, because a receipt is as private as the
expense it belongs to.

Uploads are rate-limited to **600 per hour per client address** — generous enough
for a whole office behind one NAT catching up on a month of receipts after a trip,
and still far below what it would take to fill a volume.

**Cleanup guarantees.** The bytes and the row cannot be written in one transaction,
so the object always goes first: if the transaction that would record it refuses or
fails, the object is removed again; deleting a receipt (or the expense it is on)
removes the row first and the object once that has committed. Either way, a failure
leaves an object nothing points at rather than a row pointing at nothing. **The one
remaining way an object can be orphaned** is the process dying in the narrow window
between writing the object and that compensating cleanup or commit completing —
there is no background sweeper for it today, so a stray object from that exact
failure mode outlives the request that caused it.

## The payroll CSV

`GET /reimbursements/export.csv` — the same expenses the list holds, or exactly the
ids named instead of the filters (an absent selection exports the filtered list; a
selection that is present but empty is refused rather than silently exporting
everything). Format, byte for byte:

- **UTF-8 with a byte order mark** (`EF BB BF`), fields separated by `;`, **decimal
  comma**, dates `YYYY-MM-DD`, **CRLF** line ends including after the last row, one
  header row.
- **Quoting**: RFC 4180's, with `;` as the separator — a cell holding `;`, `"`, a
  carriage return or a line feed is wrapped in `"` with its own quotes doubled.
- **Formula-injection guard**: a cell beginning with `=`, `+`, `-`, `@`, a tab or a
  carriage return is prefixed with `'`, applied *before* the quoting decision, on
  every text column — so a description, a display name, a category or a project
  code that happens to start with one of those characters cannot execute when a
  colleague opens the file in a spreadsheet.
- **Columns, in order**: `Employee;User id;Date;Kind;Description;Category;Currency;Gross;VAT;Owed;Project code`.
  The header is English and untranslated on purpose — the file is read by a payroll
  system, not by every employee — and the project column is always present, even in
  an installation with no projects module, so a payroll system need not know which
  modules run.
- Rows are ordered by the person's **display name**, then entry date, then id — a
  file read by a person, not by the database's own uuid order.
- Headers: `Content-Type: text/csv; charset=utf-8`,
  `Content-Disposition: attachment; filename="expenses-reimbursements-YYYY-MM-DD.csv"`
  (today, UTC), `Cache-Control: private, no-store`.
- **Capped at 5 000 rows.** Over the cap is a 400 titled "Too many rows to export",
  with a `detail` asking for a narrower filter and **no `errors` object at all** —
  the file is not paged or truncated; half a payroll file is worse than none.

## Stats and attention

`GET /stats` (the app's own strip), `/stats/summary`, `/stats/timeseries` and
`/stats/attention` follow the same shape every other module's dashboard reads use.
The timeseries' one metric is `netAmount` — the caller's own approved expenses, net,
in the installation's default currency only, for the reason Time's hours are scoped
to the caller: a figure that changed meaning with the reader's permissions would
mean a different thing to every reader of the same card.

Three attention types, in `GET /stats/attention`:

| Type | Told to | `entityId` | `count` |
| --- | --- | --- | --- |
| `expenseRejected` | the owner, about their own | the expense id (decimal) | absent |
| `approvalWaiting` | whoever may approve | the **owner's** user id (uuid) | how many are waiting |
| `reimbursementWaiting` | `expenses:manage` | the literal string `"reimbursements"` | how many are waiting |

`title` is a name — an expense's description, a person's display name — never a
finished sentence, **except `reimbursementWaiting`, whose title is a deliberately
untranslated English fallback** ("N expenses are waiting to be reimbursed") for a
client that does not know the type; a translating client renders its own sentence
from `type` and `count` instead, which is exactly what `count` exists for.

## Refusal codes

One rule runs through every door in this module: **an id the caller may not see is
a bare 404**, byte-identical to an id that does not exist, so neither its
existence nor what it belongs to leaks. **Something the caller may see but may not
change is a 403** — the access layer's own body, so a handler-level denial looks
exactly like a router-level one. **What the expense *is* right now — its status, or
a date the period lock closes — is a 400 naming the field**, because that is a fact
about the expense the caller can act on, where a bare 403 would leave them
guessing. Every single-row write that is guarded by a revision answers a 409 naming
both the current and the supplied revision when they disagree.

## The locking rule

**No call into another module, and no object-store call, is ever made inside a
transaction that holds a row or advisory lock.** The project and user directories,
and the receipt object store, all read through the process's own connection pool or
its own I/O; a transaction holding locks while waiting on either would starve every
other writer under load. Whatever a decision inside a lock needs from a neighbour is
read and cached *before* the transaction opens, and whatever a response needs is
resolved *after* it commits. This is enforced mechanically across the module's whole
test suite: a hook installed once before any test runs records every cross-module
and every object-store call together with whether it happened inside such a
transaction, and fails the test if one ever did.

## API

Every operation is under `/api/v1/expenses`, authenticated with the shared identity
session cookie, and every one requires `expenses:access` on top of what the table
says.

| Endpoint | Access |
| --- | --- |
| `GET /meta` | What this installation can do, the settings a new expense starts from, the categories, and the caller's own capabilities |
| `GET /entries` (`userId`, `projectId`, `claimId`, `standalone`, `status`, `kind`, `from`, `to`, `reimbursed`, paging) | The caller's own; a project manager also sees their projects'; view-all/approve/manage see everyone's |
| `GET /entries/{id}` | The owner, the project's manager, or view-all/approve/manage; a bare 404 otherwise |
| `POST /entries` | Record one — your own, or (`userId`) a colleague's, with `expenses:manage`; `claimId` records it as a line of a travel claim |
| `GET /claims` (`userId`, `status`, `from`, `to`, `reimbursed`, paging) | The same visibility rule the entries' list applies, one level up |
| `POST /claims` | Record a trip — your own, or (`userId`) a colleague's, with `expenses:manage` |
| `GET /claims/{id}` | The claim with its lines, its totals per currency and its capabilities |
| `PUT /claims/{id}`, `DELETE /claims/{id}` | Owner or `expenses:manage`, while draft or rejected, not past the lock |
| `PUT /entries/{id}`, `DELETE /entries/{id}` | Owner or `expenses:manage`, while draft or rejected, not past the lock |
| `POST /entries/{id}/attachments`, `DELETE /attachments/{id}` | Same as edit, an outlay only |
| `GET /attachments/{id}` | Whoever may see the expense |
| `POST /submit` | Your own drafts and rejected lines (or anyone's, `expenses:manage`) |
| `POST /approve`, `/reject` | `expenses:approve`, or the project's own manager |
| `POST /unapprove` | Same, plus `expenses:manage` |
| `GET /approvals` | An approver; 403 for a caller who approves nothing |
| `PUT /entries/{id}/rate` | An approver or `expenses:manage`, on a submitted mileage line |
| `PUT /entries/{id}/billing` | Financial rights on the entry's project |
| `GET /entries/{id}/billing-lines` | Financial rights on the entry's project — the pricing dialog's own picker, not the caller's bookable-projects list |
| `POST /entries/{id}/invoiced`, `.../invoiced/undo` | Financial rights on the entry's project |
| `GET /reimbursements`, `/reimbursements/export.csv`, `POST /reimbursed`, `/reimbursed/undo` | `expenses:manage` |
| `GET /projects` | The caller's own bookable projects (or, `userId`, a colleague's, with `expenses:manage`) |
| `GET /categories` | Anyone in the app |
| `POST /categories`, `PUT /categories/{id}` | `expenses:manage` |
| `GET /rates` | Anyone in the app (the customer rate hidden without `expenses:manage`) |
| `POST /rates`, `PUT /rates/{id}`, `DELETE /rates/{id}`, `POST /rates/reset` | `expenses:manage` |
| `GET /settings` | Anyone in the app |
| `PUT /settings` | `expenses:manage` |
| `GET /stats`, `/stats/summary`, `/stats/timeseries`, `/stats/attention` | The caller's own figures, plus their approval queue's size |

That is all 43 operations the contract declares, each exercised by the module's own
coverage gate (below) with no allow-list.

## What comes next

The travel claim's own flow — submitting, approving, unapproving and paying
back a whole trip, and the claim units in the approval queue, the reimbursement
list, the payroll CSV and the dashboard reads — and the **per diem day**: the
`per_diem` kind, its meal deductions against the rates this delivery seeds, and
the suggestion that proposes a trip's days from its departure and return. Then
**the project page's own Economy tab, on the cost side**: what a project's
expenses cost and bill, beside the hours Time already reports there.

## Development

The backend is `apps/server/internal/expenses`, with typed queries generated by
sqlc and the server interface generated by oapi-codegen from `openapi/expenses.yaml`.
The UI lives in `apps/expenses/frontend` (`@vantigo/expenses-ui`) and is composed by
the host SPA in `apps/host/frontend`.

The Go package's tests run every HTTP exchange through a contract-validating client
and gate on operation coverage: every operation in `openapi/expenses.yaml` must have
been exercised by at least one successful exchange, with no allow-list. Projects is a
fake in those tests — depguard forbids importing another module even in tests —
while the user directory is the real one, composed.

```bash
bun run --cwd apps/expenses/frontend test
cd apps/server && go test ./internal/expenses/...
```
