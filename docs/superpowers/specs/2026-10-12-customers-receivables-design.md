# Customer 360 — design (delivery C: receivables)

Phase 5 of the Customers roadmap ("Customer 360"), delivery **C**: the overview panel
answers what this customer has been invoiced and what they still owe. Delivery A
([`2026-09-23-customers-360-design.md`](2026-09-23-customers-360-design.md)) built the
panel and parked "invoiced revenue and outstanding" until Invoices existed; Invoices now
exists through phase 4B — a gap-free document, a payment ledger, credit notes, a derived
document state, overdue rules, holds and the hand-off to collection — so the figures
are facts the system already keeps, and this delivery only carries them to the customer
page the way delivery A carried projects' and expenses'. Other modules writing to the
customer timeline still wait for the outbox (ROADMAP, Platform).

## Where the figures come from

Invoices keeps no state column. `invoices.document_state(kind, status, gross, credited,
paid, due_date, today)` derives one of `draft`, `issued` (a credit note), `credited`,
`paid`, `overdue`, `partially_paid`, `open` from the document's gross, the issued credit
notes against it and its live payments, judged on **today in Oslo** from the server's
clock (`businessDay`, never `CURRENT_DATE`). **The open amount** is gross − credited −
paid. `GET /invoices/stats/summary` already sums "outstanding" (open, partially paid or
overdue, at the open amount) and "overdue" over the whole installation; this delivery
is the same sum narrowed to one customer, plus what was issued to them.

Charges — reminder fees, compensation and interest — are kept apart from the principal
everywhere in Invoices ("the payments, the open amount and the state never read these
tables"), and the stats leave them out. So does this delivery.

## Decisions

### D1 — A single-provider contract, `contracts.CustomerReceivables`, implemented by Invoices

Customers must not import Invoices (depguard) nor read its schema (`schema_test.go`), and
no Invoices endpoint answers a per-customer summary. So the figures cross the boundary
the way projects' and expenses' do: a read-only in-process port in `internal/contracts`
(`receivables.go`), a **`Receivables func(Deps) contracts.CustomerReceivables`** hook on
`module.Module` that Invoices declares, a **`Deps.Receivables`** slot that Compose
resolves with the other single-provider slots before any Mount runs (`soleProvider`,
"customer receivables"), and `modtest.WithReceivables` for the fake. It is nil when
Invoices is off, which a consumer reads as "invoicing is off", never as "nothing owed".

```go
type CustomerReceivables interface {
    // ReceivablesForCustomer reports what one customer has been invoiced and
    // what they owe, judged on the provider's own today. A customer with no
    // documents answers the zero value, never an error and never nil.
    ReceivablesForCustomer(ctx context.Context, customerID int32) (CustomerReceivableTotals, error)
}

type CustomerReceivableTotals struct {
    // Currencies is one entry per currency any issued document of the
    // customer is in, by ISO 4217 code ascending; a currency with nothing is
    // absent. There is no total across currencies, and a consumer must not
    // build one.
    Currencies []CurrencyReceivables
    // OutstandingCount and OverdueCount are the issued invoices whose state
    // is open, partially paid or overdue, and of those the overdue ones —
    // across every currency, because a count is not money.
    OutstandingCount, OverdueCount int
    // OnHoldCount and AtCollectionCount are the outstanding invoices with a
    // live hold, and with a live hand-off to a collection agency.
    OnHoldCount, AtCollectionCount int
    // OldestOverdueDueOn is the earliest due date among the overdue
    // invoices, YYYY-MM-DD; nil when none is overdue.
    OldestOverdueDueOn *string
    // LastIssuedOn is the latest issue date of any issued document, invoice
    // or credit note; LastPaymentOn the latest paid-on date of any live
    // payment. Each nil when there is none.
    LastIssuedOn, LastPaymentOn *string
    // AsOf is the Oslo day the states were judged on, YYYY-MM-DD.
    AsOf string
}

type CurrencyReceivables struct {
    Currency string
    // IssuedThisYear and CreditedThisYear are the gross of the invoices and
    // of the credit notes issued in the Oslo calendar year AsOf falls in;
    // IssuedLastYear and CreditedLastYear the year before. Decimal text,
    // exact, as every money figure crosses a contract.
    IssuedThisYear, CreditedThisYear, IssuedLastYear, CreditedLastYear string
    // Outstanding is the open amounts of the outstanding invoices, summed;
    // Overdue the overdue ones' — charges never included.
    Outstanding, Overdue string
}
```

Rules the contract states, in the other contracts' words:

- **The provider owns "today".** Invoices judges every state on the Oslo business day
  of its own clock, and a consumer that passed a day of its own could show a state the
  Invoices app does not; `AsOf` says which day it was, so a test can pin it and a
  consumer can say "as of".
- **A draft is never counted**: it is not a salgsdokument. A credit note counts in
  `Credited*` and in `LastIssuedOn`, never in outstanding. A merged-away customer's
  documents were re-pointed to the survivor and answer there; an anonymised person's
  documents stay — bookkeeping keeps them — and still answer, which is why the
  anonymised customer's page keeps showing its figures.
- **No authorization, no callback.** The provider serves from its own tables alone and
  never asks the customer directory anything; shaping to the caller is the consumer's
  job. It holds no transaction: the panel's figures need not be one snapshot, and
  saying so keeps the provider to plain reads.
- **A failure is a failure**, never zeros: "could not be read" must not read as "owes
  nothing".
- **Per currency from day one**, though Invoices accepts only NOK in this phase, so the
  day EUR arrives the contract is unchanged.

The provider is one aggregate query in `queries/receivables.sql` over
`invoices.invoices` with the list's own two lateral joins (credited, paid), `FILTER`ed
per figure and grouped by currency, plus counts of live holds and hand-offs over the
outstanding set, and `max(paid_on)` over live payments. The year bounds are Oslo
calendar years from `AsOf`; issue dates are already Oslo dates.

A batch method (`ReceivablesForCustomers`) is deliberately not in this delivery: the one
caller reads one customer. The day the customers list wants an **Outstanding** column
it is an additive method, not a changed one.

### D2 — The response: an `invoices` section, shaped by Invoices' own key

`CustomerOverviewResponse` gains **`invoices?`**, present when the Invoices module is on
and the caller holds **`invoices:access`** — the key that already reads every invoice,
its open amount and the stats, and the key the customer page's Invoices tab sits
behind, so a figure here never names what the tab would refuse. No new permission key;
money follows the one gate, because `invoices:access` reads amounts already.

```
invoices: {
  outstandingCount, overdueCount, onHoldCount, atCollectionCount,
  outstandingAmounts: [{currency, amount}],      // CustomerOverviewAmount, per currency
  overdueAmounts:     [{currency, amount}],
  invoicedThisYearAmounts: [{currency, amount}], // issued − credited, this Oslo calendar year
  invoicedLastYearAmounts: [{currency, amount}], // the same for the year before
  oldestOverdueDueOn?, lastIssuedOn?, lastPaymentOn?,
  asOf
}
```

`invoicedThisYear` is **net of credit notes by issue date** — a credit note issued this
January against December's invoice lowers this year, as the stats' period figures count
it — and the description says so. A currency with nothing in a list is left out, as the
other sections do; a customer with no documents answers the section with zero counts
and empty lists, not absence — absence means "not for you or not installed", exactly as
delivery A decided. Amounts cross from decimal text to a JSON number once, at the end,
in `math/big.Rat` as `overviewAmounts` already does.

`lastActivity` gains **`invoicedOn`** (`LastIssuedOn`) and **`paymentOn`**
(`LastPaymentOn`), each absent when unknown or when the section is. The panel's
"latest of" then spans five sources, with the tie order timeline, work, expense,
invoiced, payment.

Everything else in the endpoint stands: no transaction, each contract asked at most
once, a failing contract fails the request, 404 for an unknown customer, an archived
or anonymised customer answers normally.

### D3 — The panel: two tiles and a link

The host's **Customer 360** panel gains two `KpiCard` tiles after **Expenses ready to
invoice** and before **Last activity**:

- **Outstanding** — the outstanding amounts per currency (the "—" dash when none);
  hint: "{n} invoices · {m} overdue, oldest due {date}" when anything is overdue, "{n}
  invoices" otherwise, and when a hold or a hand-off is live the hint adds "{k} on
  hold" / "{k} at collection" — a relationship owner should learn here, not from the
  agency, that their customer is at collection.
- **Invoiced this year** — the net amounts per currency; hint: "last year {amounts}".

A tile is rendered only when `invoices` is present. With six tiles the grid grows to
three per row at `md` and all six at `lg` (`tileColumns` learns the two breakpoints;
the four-tile layout is unchanged at `md`). Under the tiles, when the section is
present, a **See invoices** anchor to `/customers/$customerId/invoices` — the tab is
there on the same gate, so the link never leads nowhere. **Last activity** names the
two new sources ("Invoice issued", "Payment received"). Strings in
`catalogs/customer.ts`, en + nb. The host's `api/customer-overview.ts` boundary turns
the new optionals into `null` as it does the others. No new host props.

### D4 — Docs and roadmap

- `docs/src/content/docs/en/reference/customers.md`: the **Customer 360** table gains the
  `invoices` row and `lastActivity` its two dates; a paragraph on what outstanding,
  overdue and invoiced-this-year mean here (by reference to the invoices page's state
  and open-amount rules), that charges are left out, and that the provider owns today;
  a `contracts.CustomerReceivables` section beside `contracts.CustomerDirectory`.
- `docs/src/content/docs/en/reference/invoices.md`: a short **What Invoices reports to
  other modules** section — the contract, the query, the Oslo-day rule — and the
  **Endpoints** / **What comes next** text unchanged.
- `docs/src/content/docs/{en,nb}/user/customers.md`: the Customer 360 paragraph names the
  two tiles, the link and the permission (`invoices:access`).
- `docs/src/content/docs/en/contributing/module-boundaries.md`: the single-provider
  slots become ten, and the list names `contracts.CustomerReceivables` (invoices).
- `ROADMAP.md`: phase 5 **Delivery C (done)**; "still ahead" shrinks to the timeline
  writers. The **Later** list drops "per-customer dunning settings" (delivered in
  Invoices 4B as the reminder policy on the Invoices tab) and gains the 2028 buyer
  organisation-number fact (bokføringsforskriften § 5-1-2: whether a buyer is
  bokføringspliktig, which the directory does not hold yet — Invoices' Later entry
  points here). "Credit limit and credit hold (needs receivables to mean anything)" now
  has its prerequisite.

## Out of scope

Other modules writing to the customer timeline (outbox); credit limit and credit hold
(Later; this is their prerequisite); charges in any figure; a period parameter or
timeseries (the stats endpoint has the installation's, the Invoices tab has the
documents); currency conversion; a batch contract method and an Outstanding column on
the customers list; an invoices table on the panel (the tab lists them); a new
permission key; refunds due (a figure the dashboard's attention already shows).

## Testing

**Contracts**: the new file compiles against the fakes; the contract's corpus still
validates.

**Invoices** (the provider, against the real schema through `modtest`): a customer with
nothing answers zeros and empty currencies; drafts never count; an issued invoice is
outstanding and open; a partial payment lowers the open amount and keeps the count; a
full payment and a full credit note each take the invoice out; an overdue invoice is
overdue on the Oslo day — due yesterday Oslo while still today UTC is overdue, and
`AsOf` says the Oslo day; the oldest overdue due date; a live hold and a live hand-off
counted, a lifted hold and a withdrawn hand-off not; issued and credited split by Oslo
calendar year on both sides of 1 January; `LastIssuedOn` across invoices and credit
notes; `LastPaymentOn` ignoring removed registrations; a merged-away customer answers
zeros and the survivor everything; an anonymised customer still answers its documents;
two customers' documents never mix.

**Customers** (the consumer, with `WithReceivables` fakes): the shaping matrix — module
off → section absent; without `invoices:access` → absent; with it → present including
for an archived and an anonymised customer; figures pass through per currency with
nothing added across currencies; zero figures answer the section, not absence;
`lastActivity` takes the two new dates and the latest-of order; a failing contract
fails the request; the other sections unchanged.

**Host**: the two tiles present only with the section; the hint's four shapes (plain,
overdue with oldest due, on hold, at collection); the "—" for no amounts; the
See invoices link; six-tile grid; Last activity from each new source; both catalogs
complete.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
