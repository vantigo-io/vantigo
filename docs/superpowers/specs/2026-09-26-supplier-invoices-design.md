# Supplier invoices — design (Projects phase 3, delivery C)

Phase 3 of the Projects roadmap named "supplier costs" beside overtime multipliers.
Delivery B was the multipliers. Today a project's non-hours cost is what somebody put on an
expense — a company-paid outlay with a free-text supplier and the "Subcontractor" category —
not what a supplier invoiced. This delivery makes the invoice a supplier sends for work or
goods on a project a thing of its own inside Expenses: a **supplier invoice** with the
supplier, the invoice number, the invoice date and the due date, its PDF attached, going
through the same attestation flow as every other cost, priced and re-billed with the same
markup, and counted apart in the project's economy. No accounts payable, no payment state,
no vendor register: recording, approving, re-billing and reporting — nothing more.

## Decisions

### D1 — A fourth kind in Expenses, with the outlay's money and one rule for who is owed

`kind = 'supplier_invoice'` on `expenses.entries` (the module's kinds are a plain string on
the wire and in the column; adding one is additive). It borrows the outlay's money rules —
`categoryId` required, `currency` any ISO code, `grossAmount` > 0, optional `vatAmount`
0..gross, net = gross − VAT as cost and markup base — and adds (migration `00033`):
`supplier` **required** (≤ 200, the existing column), `supplier_invoice_number varchar(100)`
**required** (trimmed, ≤ 100; free text, since no supplier record exists to make it unique
under), `supplier_due_date date` optional, on or after the entry date. **The entry date is the
invoice date** — one date, the one the period lock judges — the docs say so. `paid_by` is
stored as `company` always (the request may omit it; `employee` is refused: "A supplier
invoice is paid by the company"). Mileage and per-diem fields are refused as on an outlay.
**Never in a claim** (400 on `claimId`: "A supplier invoice is not a travel claim line") and
**always on a project** (400 on `projectId`: "A supplier invoice is booked on a project";
without the projects module the kind is refused outright with the same sentence). Column
names avoid the outgoing stamp's `invoice_reference`/`invoiced_at`.

Who is owed money is today one predicate written fourteen times — thirteen in SQL, once in
Go (claims.sql's receipts-missing count reads `kind = 'outlay'` too, but asks a different
question) — and every copy would call a supplier invoice "owed to the employee". This delivery
folds the SQL copies into **one function**, `expenses.owes_employee(kind, paid_by)`
(`IMMUTABLE`, in the migration), used by every query that had the predicate, and keeps the
Go `owesEmployee` as its mirror with a comment naming the function — "one rule, written
twice on purpose" stays true, and the count of places drops from fourteen to two. A supplier
invoice owes nobody: it never reaches the reimbursement list, the payroll CSV, the
unreimbursed stats or the reimbursement attention item.

### D2 — Recorded by whoever holds the project's financial rights, attested like any cost

Booking a supplier invoice needs **financial rights on the project** (the manager role,
`projects:manage-all`, or `projects:view-financials` on a project the caller sees) rather
than `CanLogTime`: the people who receive and re-bill supplier invoices are the project's
financial side, not necessarily its team, and the project may be **completed** (an invoice
often arrives after the work) — only a cancelled project refuses. The recorder is the
entry's owner as today (it lists under their expenses, they edit and submit it; recording
for a colleague still needs `expenses:manage`). The flow is the module's: draft →
submitted → approved | rejected, approved by `expenses:approve` or the project's manager,
self-approval allowed, unapprove refused once invoiced. **The invoice document is required
on submit**: at least one attachment (the receipts mechanism, which learns the kind — PDF
is already accepted), refused with "Attach the supplier's invoice" the way a missing receipt
is. Kind may change on a draft between outlay and supplier invoice (both take receipts, so
nothing strands); the missing-field 400s say what the new kind needs.

### D3 — Priced and re-billed as an outlay; counted apart

Billing is the outlay's: billable needs a billable project; markup named → stored →
settings default; bill = net × (1 + markup %); the pricing door, the manual invoiced stamp
and "ready to invoice" apply unchanged. `ProjectExpenses` gains an additive per-currency
sub-figure: `CurrencyExpenses.SupplierInvoices *ExpenseSplit{Approved, Submitted, Draft,
Total ExpenseBucket}` — the part of each bucket that is supplier invoices, nil when the
currency has none — while every existing figure keeps meaning "everything". Projects'
economy `expenses` block gains optional `supplierInvoices {approved, submitted, draft, total:
{count, cost, amount}}` in the project's currency (absent when none), the Costs section shows
it as "of which supplier invoices" beneath the totals, and the Expenses tab's currency cards
show the same line. The margin, budget used, the portfolio and the alerts are unchanged (the
margin already counts all expense cost; expenses never eat the budget — X12).

### D4 — Visible to the project's financial side

A supplier invoice carries no personal data, so its rows are visible to **everyone with
financial rights on its project**, not only the owner, the project's manager and
`expenses:view-all/approve/manage`: the list, the detail and the project panel show them to a
`projects:view-financials` holder, and the Expenses tab's "you can see totals, not rows"
note no longer applies to these rows. Employee outlays keep today's visibility.

### D5 — The frontend

Expenses app: the kind control offers **Supplier invoice** beside Outlay and Mileage (outside
claims, only when projects are available); its fields — supplier (required), invoice number
(required), invoice date (the entry date), due date, category (the UI preselects
"Subcontractor" when that category exists), gross, VAT, no paid-by control (a line says the
company paid), receipts dropzone with "Attach the supplier's invoice"; the project block
requires a project and defaults **billable on**; the list's kind filter, the kind labels, the
drawer (invoice number, due date, "overdue" tag when the due date has passed and the line is
not yet invoiced — informational, no payment state), the approval tables. Project page:
**Record a supplier invoice** beside **Record a cost**, behind a new summary capability
`canRecordSupplierInvoice` (financial rights, project not cancelled); the panel's list shows
the kind. Economy tab: the "of which supplier invoices" line. en + nb ("Leverandørfaktura").

### D6 — Docs

`docs/expenses.md`: the kinds section gains the fourth kind (fields, rules, the one date,
company-paid, never in a claim, always on a project, the required document, who may record,
the visibility widening), the "one rule, written twice" paragraph becomes "the SQL function
and its Go mirror", the API table, the receipts section, the "what comes next" paragraph;
`docs/projects.md`: the expenses block's `supplierInvoices`, the Costs section, "what a
receipt cost the company" reworded; `docs/module-boundaries.md`: the contract's new
sub-figure in the third optional contract's description; `ROADMAP.md`: phase 3 C done, what
remains uncommitted (forecast, revised budgets), and "supplier costs" no longer a gap in the
Expenses section.

## Out of scope

Accounts payable and a paid/unpaid state; a vendor or supplier register (customer-is-also-
supplier stays with purchasing); inbound e-invoices (EHF/Peppol inbound); OCR; VAT codes or
per-rate VAT lines (gross + one VAT amount, as the module has always had); due-date
reminders or an attention item; a supplier-invoice export (the payroll CSV never carried
company-paid lines and does not start now); purchase orders and commitments; a supplier
cost budget or a materials/subcontractor budget split; changing what "budget used" measures.

## Testing

Expenses through `modtest`: the kind's validation (every required field, the refusals for
`paidBy=employee`, a claim, no project, no projects module, the due date before the entry
date, mileage/per-diem fields), create/update/delete as owner and as `expenses:manage`,
kind change outlay ↔ supplier invoice on a draft, the recorder gate (financial rights
without a role; a completed project accepted; a cancelled one refused; a member without
financial rights refused), submit refused without an attachment and accepted with one, the
flow (approve by the project manager and by `expenses:approve`, reject, unapprove, invoiced
stamp, pricing door, ready to invoice), **owes nobody**: absent from the reimbursement list,
the payroll CSV, the unreimbursed stats and the reimbursement attention, with the SQL
function pinned by the schema test and every former predicate site exercised by an
existing or new test, the Go mirror tested against the function on every (kind, paid_by)
pair; visibility to a `projects:view-financials` holder (rows on the list, the detail, the
panel) and unchanged for outlays; `ProjectExpenses` sub-figure per currency (nil when none,
buckets matching, totals unchanged), the project summary's `canRecordSupplierInvoice` and
its own line; contract coverage. Projects: the economy block's `supplierInvoices` shaping
through the fake, absent/present cases, the golden test. Integration: projects + expenses
composed for real — a supplier invoice recorded by a `view-financials` holder on a completed
project shows in the economy's sub-figure. Frontend (expenses): the kind control and fields,
the required-document hint, the payload, the project-page button and its gate, the list
filter and labels, the drawer's number/due date/overdue tag, both catalogs; (projects): the
Costs section line. Docs against the code.
