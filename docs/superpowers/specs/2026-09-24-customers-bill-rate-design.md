# Customer default bill rate — design (phase 5, delivery B)

Phase 5 of the Customers roadmap ("Customer 360"), delivery **B**: a customer default
bill rate, slotted into the chain Projects and Time already resolve rates through —
**billing line → project → customer → person**. A customer that has negotiated an
hourly rate gets it applied to every project of theirs that does not price its own
hours, without anyone re-typing it per project. Nothing else about rates changes.

## Decisions

### D1 — The rate is the eleventh field of the billing profile, quoted in the profile's currency

`customers.customers.default_bill_rate numeric(12,2) NULL` (migration `00028`, the
`00019` ALTER shape) — the module's first money column, at the scale every rate in the
chain already has (`projects.default_bill_rate`, `time.*.bill_rate`). It is a field of
the **billing profile**: read with `customers:view`, written with
`customers:billing-manage` through the existing full-replace `PUT
/customers/{id}/billing-profile`, never on `SafeCustomerResponse`, part of the
`customer.billing_profile_updated` event's `before`/`after`/`changes` (payload version
stays 1 — an added optional field is not a new shape).

Its currency is the profile's own **`currency`** — one currency per customer, not a
second pair the way a project carries `defaultBillRate`/`currency` (a project is billed
in one currency; so is a customer). Validation, in this module's wording and keyed by
field: greater than zero and at most two decimals (`maxAmount12`, projects' own rule
mirrored, since depguard forbids sharing it); **a rate needs the currency** — a body
with `defaultBillRate` and no `currency` is a 400 on `defaultBillRate` ("A default bill
rate needs the billing profile's currency to be quoted in"); the PUT is a full replace,
so clearing the currency while sending the rate is the same error. Omitted or null
clears it.

### D2 — The directory exposes it, own value only

`contracts.CustomerBillingProfile.DefaultBillRate *float64` — the customer's own value,
nil when never set, quoted in `Currency`; **no group tier** in this delivery (the
roadmap's chain names line → project → customer → person, no group; a group default
bill rate slots in later beside `default_payment_terms_days` if wanted, the
`PaymentTermsDays` three-tier shape). `resolveBillingProfile`'s signature is untouched:
the field passes through like `Currency` and `GLN`, and the docs' resolution table gets
its row.

### D3 — Time's chain gains step 3: the customer

`time/rates.go`'s `resolveRates` switch gains a case between `sourceProject` and
`sourcePerson`:

- **When**: the entry is billable, no line rate, no usable project default, the project
  has a customer (`ProjectEntry.CustomerID != nil`), there is a directory
  (`deps.Directory != nil` — Time's first read of the customer directory; unlike
  expenses' read of projects this one is never absent in a real installation, since
  time requires projects and projects requires customers, so the nil check is a
  defensive floor, not a mode), and `Directory.BillingProfile(customerID)` answers a
  profile with `DefaultBillRate != nil` and a `Currency`.
- **The currency rule is the person card's, verbatim**: the rate applies when the
  project has no currency (the entry takes the customer's) or the project's currency
  equals the customer's; a customer quoted in SEK is no use to a project billed in NOK,
  and nothing converts — the chain falls through to the person card.
- `rateSource` = **`customer`**; `bill_rate`/`bill_currency` snapshot as every other
  source. A directory error is an error (the `ListPrice` precedent); a missing customer
  ((nil, nil)) is simply "no rate here". An archived customer's rate **applies**:
  archived customers still resolve, and a project of theirs may still be worked on.
- The directory is asked at most once per resolve, only when the chain reaches step 3
  (never for a non-billable entry, a line-priced one, or a project with its own
  default). Rates still resolve at every save while draft/rejected and freeze on submit,
  so a changed customer rate reaches drafts on their next save and nothing already
  submitted.

Wire: `TimeEntryResponse.rateSource`'s description names `customer` (a plain string,
no enum; no corpus for time). The time frontend's hand-narrowed `RateSource` union
gains `"customer"`.

### D4 — The frontend

The customers **Billing modal** (`-customer-billing-modal.tsx`) gains a **Default bill
rate** `NumberInput` (two decimals, empty = cleared) after the currency field; the
**Billing card** shows the row "Default bill rate — 1 250.00 NOK per hour" (or "—")
after `currency`, in the contract's order. The server's field errors map onto the form as
the other fields' do (the currency-required error lands on the rate field). Both
catalogs. No host change (`canManageBilling` already gates the modal).

### D5 — Docs

`docs/time.md` "The rate chain": step 3 inserted (person → 4, none → 5), the
"Currency is never converted" rule gains the customer bullet, the `rateSource` sentence
names `customer`, the prose at ~395 that enumerates the fallback; `docs/projects.md`'s
chain summary widens to `→ customer default → person default`; `docs/customers.md`:
the billing profile table's eleventh row and its "ten columns (00019)" sentence, the
directory resolution table's `DefaultBillRate` row, a phase 5 delivery B paragraph in
"what comes next"; `ROADMAP.md` phase 5 "still ahead" loses the bill rate.

## Out of scope

A group-level default bill rate; re-resolving submitted entries; currency conversion;
a rate history; showing `rateSource` in Time's UI (it shows none today); the Products
list-price step (unchanged).

## Testing

Customers through `modtest`: PUT round trip (set, shown in GET and PUT answers), clear
by omission/null, `0`/negative/three decimals/over max → 400 keyed `defaultBillRate`, a
rate without currency → 400 keyed `defaultBillRate`, the event's `changes` names it, the
two "every field" tests extended, the directory (`BillingProfile`) carries it and
`resolveBillingProfile` passes it through (nil stays nil, value stays value). Time:
`rates_internal_test.go`'s table gains the customer cases (customer default when no
project default; project default wins over it; currency mismatch falls to the person
card; a currency-less project takes the customer's currency; a nil directory (a floor
that cannot fire — Time requires Projects, which requires Customers) → person;
project without a customer → person; missing customer → person; a directory
error → error), with a fake `CustomerDirectory` in Time's test package; an
`entries_test.go` end-to-end case asserting `rateSource: "customer"` and the snapshot;
the harness gains `modtest.WithDirectory`. Frontend: the modal's round trip (the PUT
body carries `defaultBillRate`; clearing sends null/omits as the sibling numeric field
does), the card row (value / "—"), the server field error shown on the field, both
catalogs. Docs reviewed against the code.
