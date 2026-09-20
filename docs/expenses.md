# Expenses module

The Expenses module is outlays, mileage and per diem days, standalone or
gathered into a travel claim, with receipts, an approval flow, two independent
tracks after approval — paying the employee back and invoicing the customer —
dated rates, categories and admin settings. It is a vertical-slice module
inside the single Vantigo binary (`apps/server/internal/expenses`), owns the
`expenses` schema in the shared PostgreSQL database, and serves `openapi/expenses.yaml`
under `/api/v1/expenses`.

It depends on nobody but identity. [Projects](projects.md) is an **optional** read
through `contracts.ProjectDirectory`: `MODULES=customers,expenses` is a valid
installation, and so is `MODULES=expenses` alone.

## Domain model

- **Entry** (`expenses.entries`) — one money line: an **outlay**, a **mileage**
  line or a **per diem day** (`kind`), owned by `userId`, dated `entryDate`, with a
  `description`, a `status`, and the audit stamps every mutating path leaves. The id
  is a `bigint`.
  - An **outlay**: a `categoryId`, an optional `supplier`, who `paidBy` it
    (`employee` or `company`), a `currency`, a `grossAmount`, an optional
    `vatAmount`, and up to ten receipts.
  - A **mileage line**: a `distanceKm`, optional `fromPlace`/`toPlace`, `passengers`
    (0–8), and no amount of its own — the dated rate table prices it, in the
    installation's own currency, and it never carries a receipt.
  - A **per diem day** (`per_diem`, only inside a travel claim): a `perDiemType`
    and three covered-meal flags, priced by the dated table for its own date; see
    "The per diem day" below.
  - The first two can carry a `projectId`, optionally a `billingLineId`, a
    `billable` flag, and — only when billable and only on the project's side — a
    `markupPercent` (outlay) or `billRatePerKm` (mileage) and the resulting
    `billAmount`. A per diem day carries the claim's project and nothing else of
    that list: it is never billed on.
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
  percentages. Eight are seeded at migration time and two deliberately are not;
  the figures, their source and validity, and what the agreement says that this
  module leaves to an approver, are in "The rates, and what they deliberately do
  not model" below.
- **Settings** (`expenses.settings`) — one row: the period lock (`lockedBefore`),
  the installation's `defaultCurrency`, `defaultMarkupPercent` (an
  `expenses:manage`-only figure — see below), the optional `receiptRequiredOver`
  threshold, and the business `timeZone` every date derived from a travel claim's
  instants is taken in (see "Which day is which").

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
- **A per diem day** pays the day rate in force on its date less the percentages of
  every meal somebody else covered — the percentages summed first and applied
  together, so the whole day rounds once — floored at zero, because percentages an
  administrator set to more than a hundred between them must not make a day owe the
  company money.
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

**A line is never a unit; the claim is.** `POST /submit`, `/approve`,
`/reject`, `/unapprove`, `/reimbursed` and `/reimbursed/undo` each take
`{entryIds?, claimIds?}` — standalone expenses, travel claims, or both in one
batch — and refuse a *line* by id with a per-id message pointing at the claim
("Expense 5 belongs to travel claim 7; submit the claim"). The line's five flow
capabilities are false; the claim's are true when the claim may move. Every one
of those batches is all-or-nothing across **both** lists, at least one id
between them is required, and the cap of 500 counts distinct **units** across
the two — an id given twice is one unit, and a trip is one however many lines
it holds. A refusal is keyed by the list that named the id, `entryIds` or
`claimIds`, so a client that ticked both knows which half to put right, and the
answer is `{entries, claims}`, each list in the order its own ids were given.

Pricing, invoicing and a rate override stay the line's own doors, because those
are about this one amount; each of them reads the *claim's* status for the
state it is judged in. So a billable line of an **approved** claim is invoiced
exactly as a billable standalone expense is.

**A line moves neither in nor out.** A `claimId` on a replace must equal the
one the line already carries; a standalone expense naming one, or a line naming
another claim, is a 400 on `claimId`. The owner and the project are the
claim's: a `userId` naming somebody else is refused, and a `projectId` must be
absent (inherited) or exactly the claim's.

**Editing a line of a rejected trip leaves the trip rejected.** A standalone
expense that is put right after a rejection becomes a draft again by being
saved; a claim does not, because the status that was rejected is the claim's and
nothing a line's save touches. The trip stays `rejected` — with its reason, and
with its attention item on the owner's dashboard — until they submit it again,
which is the one act that says "this is ready to look at". Editing is allowed
the whole time (`rejected` is editable exactly as `draft` is), so the only
difference is what the trip *says* about itself in between, and it says the
truthful thing: nobody has been asked to look at it again yet.

## The lock order inside a claim

Every write takes its locks in one order, and nothing may invent another:

1. the claim's own row (`SELECT … FOR UPDATE`), then
2. its lines, in id order — all of them for a project re-point, or the one line
   the write is about.

A batch that names several units keeps the same order, widened: **every named
claim's row in id order, and then every expense row of the batch — the claims'
lines and the named standalone expenses alike — in one ascending pass**. One
statement for the second half is what makes it safe: taking each claim's lines
separately and the named expenses afterwards would let two batches that name
each other's claims cross, one holding claim 9's lines and waiting for a line of
claim 8 while the other does the mirror image.

So a write on the claim and a write on one of its lines start at the same row
and queue rather than deadlock. Changing a claim's project re-points every line
in that same transaction: the new project's billing lines are read **before**
the transaction opens (nothing inside one calls another module), a line's
billing line is cleared when the new project does not have it, and clearing the
project altogether makes every line non-billable with its billing figures. A
claim holding a line that has **already been invoiced** cannot be re-pointed at
all — neither onto another project nor off one — because the line would then
say it belongs somewhere the invoice that went out does not (400 on `projectId`,
naming the line). The cap of 200 lines is decided under the claim's lock, so two
lines racing for the last slot cannot both take it.

**A line's project and owner are always its claim's**, and that is an invariant
the rest of the module reads rather than derives: the list's visibility
predicate matches on a line's own denormalised `user_id` and `project_id`
instead of joining, and a line's `canSeeBilling` is answered against the project
those columns name. Because a save is judged against the claim as it was read
*before* the transaction — judging a project means asking the project directory,
and nothing inside a locked transaction may — the claim is compared again once
its row is held, and a save whose claim moved underneath it is refused on
`claimId` ("read it again and retry") rather than written against a claim that
has changed. A replace of a line needs no such comparison: a re-point bumps
every line's revision, so a racing edit already gets a 409.

## The per diem day

A per diem day is a line of a travel claim and of nothing else — a trip is what
gives a day its rate, its currency and the window its date has to fall in — so a
`kind: "per_diem"` with no `claimId` is a 400 on `kind` ("A per diem belongs to a
travel claim"). It carries a `perDiemType` (`day_6_12`, `day_over_12`,
`overnight_hotel`, `overnight_other`) and the three flags `breakfastCovered`,
`lunchCovered` and `dinnerCovered`, and nothing else: a category, a supplier, a
payer, an amount, a VAT, a currency, the mileage fields, `billable`, a billing
line, a markup and a customer rate per kilometre are each refused **on their own
field**. It is always owed to the employee, never billed on, carries no VAT and
takes no receipt.

**Never priced, by any door.** The entry doors refuse `billable` and
`billingLineId` on their own fields, and so do the three doors on the project's
side of the line: `PUT /entries/{id}/billing`, its picker
`GET /entries/{id}/billing-lines` and `POST /entries/{id}/invoiced` each refuse a
per diem day on `kind`, and `canSetBilling` and `canMarkInvoiced` are false for
one. A day away is paid back out of the company's own pocket; there is no
customer on the other side of it, and no role — not even the project's manager —
that may invent one.

**What it is worth.** `dayRate × (1 − Σ covered meal percents / 100)`, floored at
zero, rounded once. The day rate is the `per_diem_*` row in force on the line's
own date — or, on a claim `abroad`, the claim's own `abroadDayRate`, in its own
`abroadCurrency`, the type then recorded rather than priced from. The meal
percentages come from the table either way. **A day rate the table does not price
is a 400 on `perDiemType`** — which is what a day of type `overnight_other` gets
until an administrator enters the company's own figure — and **a covered meal the
table prices no deduction for is a 400 on that meal's own flag**, so a rate table
somebody emptied is visible rather than silently generous. A meal nobody covered
needs no percentage and records none.

Every draft save reprices the day and stores what it was priced from: the day
rate in `rate` and the three percentages in `meal_*_percent`. The claim's submit
freezes exactly what that last save wrote, and an approver's `PUT
/entries/{id}/rate` replaces the day rate and works the amount out again **from
the percentages the line was saved with** — never from the table as it stands
today, so correcting a rate can neither drop a breakfast somebody else paid for
nor pick up a percentage that has changed since. `passengerRate` is refused on a
per diem day.

**Its date.** At most one per diem day per claim per date — decided under the
claim's own row lock, so two days racing for one date cannot both take it (400 on
`entryDate`) — and the date must fall between the trip's departure day and its
return day, both included.

**The claim answers for its days.** A per diem day is the first kind whose price
and whose validity come from the *claim* rather than from itself, so
`PUT /claims/{id}` has to answer for the days it already holds — and does, inside
the transaction that already holds the claim's row and its lines:

- narrowing `departureAt` or `returnAt` past a day already recorded is **refused**
  on that end's own field, naming every stranded date. A day somebody recorded is
  theirs; a trip correction quietly taking money off a claim is the one outcome
  nobody would forgive.
- changing `abroad`, `abroadDayRate` or `abroadCurrency` **reprices** every per
  diem day of the claim — the rate, the three percentage snapshots, the amount and
  the currency — and bumps each line's revision, so a client holding one at its old
  revision is told to read it again. A day that could not be priced after the
  change (a trip turned domestic on a date the table prices no day of that type)
  refuses the whole edit, because half a claim repriced is worse than an edit the
  caller can undo.

The same reasoning reaches one door down: `PUT /entries/{id}` on a per diem line
reprices it against the claim the *transaction* holds, not the one it read before
taking the lock, so a claim edit committing in between cannot leave a day at
yesterday's rate.

**Which day is which.** A claim stores two instants, and `timestamptz` keeps the
instant rather than the offset it was typed in, so *which calendar day* a trip
departed on depends on where you are standing. The installation says where: its
**business time zone**, `expenses.settings.timeZone` — an IANA name,
`Europe/Oslo` by default, because that is whose per diem agreement this module
implements — and `businessDay(instant, zone)` is the module's one derivation of
"the day". It is what the period lock is judged on, what `GET /claims`'
`from`/`to` filter applies in SQL (the same stored name, as
`(departure_at AT TIME ZONE …)::date`, so Go and the database can never
disagree), what the within-the-trip rule compares against, and what the
suggestion dates a day by — one rule, so a day the server proposes can never
fall outside the trip the save then judges it against. **"Today"** is today in
that zone too: the payroll run's not-in-the-future rule and the export's file
name.

A departure typed `2026-07-01T00:30+02:00` is therefore a trip that departed on
**1 July**, which is what the calendar on the office wall says; under a UTC rule
it was 30 June — one day early for an hour or two a day, and wrong at exactly
the month boundaries a period lock and a payroll month are about.

Every `expenses:access` holder reads the zone, because a client showing a trip's
days has to label them in it rather than in the browser's; `expenses:manage`
alone writes it, and a name is checked against **both** Go's tzdata and
Postgres' before it is stored — not only that each side knows it, but that the
two **mean the same thing** by it: the UTC offset Postgres puts the name at in
January and in July is compared with Go's, and any difference is a 400 naming a
city zone to use instead. Knowing the name is not enough, because Postgres
resolves its abbreviation table first: `CET` is a fixed +01:00 to it and the
zone with summer time to Go, so `CET`, `EET`, `MET` and `WET` are refused while
`Europe/Oslo`, `UTC` and every other city zone are not. **Changing it moves every trip's
days**, which is why it is a setup decision rather than a preference: an
installation that switches zones after recording per diem days may find some of
them outside their own trip, and only a save of that claim or that line will say
so.

**The suggestion.** `POST /claims/{id}/per-diem-suggestion {overnight}` answers
the days a trip's own times imply, each priced with the table as it stands (or the
claim's own rate abroad) and marked `exists` when the claim already holds a day
for that date. **It writes nothing**, and it filters nothing out: what to do about
a day already recorded is the client's decision. Whoever may *see* the claim may
ask for it. The counting, in one sentence — **a trip of at least six hours earns
a day, and an overnight one earns another for every full 24 hours plus a
remainder longer than six**:

- under six hours the trip earns nothing at all;
- `overnight: false` — one day on the departure: `day_6_12` up to and including
  twelve hours, `day_over_12` beyond. A trip of several days that nobody slept
  away on is still one day;
- `overnight: true` — one `overnight_hotel` per full 24-hour period from the
  departure, plus one more when what is left over runs *strictly* longer than
  six hours, and never fewer than one. The periods are 24 hours from the
  departure *instant*, never calendar midnights. So 6 h 00 and 24 h 00 and
  30 h 00 are one day, 30 h 01 and 31 h are two, 48 h 00 is two and 54 h 01 is
  three.
  The **dates** are consecutive calendar days from the departure's own day
  **in the installation's business time zone**: day *i* is that day plus *i*
  days. That is the same date the period's own start falls on except on the two
  nights a year the clocks move — when the clocks go back, 24 elapsed hours
  after 00:30 is 23:30 the same evening, and dating by the instant would put two
  days on one date, which the one-per-date rule then refuses; when they go
  forward it would skip a date. A calendar day is also what whoever fills the
  form in means by "the second day of the trip". Every date is inside the trip,
  so a day the server proposes can never fall outside the trip the save then
  judges it against.

The six hours reads two ways on purpose: **inclusive** as the threshold a whole
trip has to clear, and **exclusive** for the remainder after a full period. A
short trip somebody slept away on is still a trip; six hours left over at the end
of a longer one is not another day.

`overnight: true` proposes `overnight_hotel` throughout, the type the agreement
prices; a traveller who stayed somewhere else changes it on the line. The client
may mirror the counting for display, but the figures on the page are the server's.
A trip may run 366 days against a cap of 200 lines, so a "record them all" button
has to reckon with the cap itself. The whole suggestion is priced from **one**
read of one rate kind — every day of one suggestion shares a type — rather than a
query a day.

**A per diem day has no description of its own unless its owner wrote one.** The
column takes the empty string: what the day *is* is its `perDiem.type`, which
every reader already has, and a name the server invented would sit in the column
in one language for ever.

## The rates, and what they deliberately do not model

The rate in force on a day is the row of that kind with the greatest `validFrom`
on or before it, and a row carries **no end date**: it stands until a later row
takes over. Every seeded row is written by migration 00013 with `validFrom`
2026-01-01 and `source: "State rate"`:

| Kind | Value | Source |
|---|---|---|
| `mileage` | 5.30 NOK/km | Skatteetaten's published rates, verified 2026-09-19 |
| `mileage_passenger` | 1.00 NOK/km | the same |
| `per_diem_6_12` | 397.00 NOK | *Særavtale om dekning av utgifter til reise og kost innenlands* (regjeringen.no), in force **2026-01-01 to 2027-12-31**, § 6, verified 2026-09-20 |
| `per_diem_over_12` | 736.00 NOK | the same, § 6 |
| `per_diem_overnight_hotel` | 1 012.00 NOK | the same, § 9 |
| `meal_breakfast_percent` | 20 | the same, § 6 |
| `meal_lunch_percent` | 30 | the same, § 6 |
| `meal_dinner_percent` | 50 | the same, § 6 |

**The rates are the administrator's, and nothing keeps them current.** They are
added, edited and removed through `/rates`, and `POST /rates/reset` puts a
kind's shipped rows back exactly as the migration wrote them without touching a
row on a day the product never shipped. Nothing in the product fetches a rate
from anywhere: **when the agreement is renegotiated — it expires 2027-12-31 —
somebody has to enter the new rows by hand, dated from the day they take
effect.** The old rows stay, which is the point: an expense dated last year is
still priced by last year's figure, and a submitted one keeps what it was
frozen with, whatever the table says today.

Two kinds ship **unseeded**, and each is a deliberate blank rather than an
oversight:

- `mileage_customer` — what a customer is charged per kilometre is the company's
  own price, not a public rate.
- `per_diem_overnight_other` — the agreement knows **one** overnight rate, the
  hotel one. A company that pays differently for lodging without cooking
  facilities enters its own figure; until it does, a day of that type cannot be
  priced at all (400 on `perDiemType`), which is louder and safer than pricing
  it at the hotel rate nobody agreed to.

And three things the agreement says that this module deliberately **does not
model**, because each is a fact about a trip that no field here records and an
approver is the one who can judge it:

- the **unreceipted night supplement** (§ 10, 452 NOK) — whether somebody slept
  somewhere that issued no receipt is not something the claim knows;
- the agreement's **distance condition** (a journey over 15 km) — the claim
  stores where a trip went, not how far;
- **rate tables for travel abroad** — a claim abroad carries its own
  `abroadDayRate` and `abroadCurrency`, agreed on the claim itself, so no table
  of countries is shipped or has to be kept up to date.

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

**The unit that moves is a standalone expense or a whole travel claim.** A
claim runs the very same four arrows, its lines carried along: the status, the
submission stamp and the decision are the claim's, and its lines keep their own
`status` column at its default for ever.

**Submit is where the line freezes.** For a mileage line, the rate, the
passenger supplement and the resulting amount are priced one last time from the
dated rate table in force on the entry's own date, and the result is what the
row carries from then on — a later change to the rate table, an approval, or a
settings change never recomputes it. A per diem day is frozen the same way, and
from the same one function every draft save runs: the day rate in force on its
own date (or the claim's own rate abroad), the three meal percentages as they
stood that day, the currency, and the amount the four come to. The same freeze
reprices what the line bills the customer, if it is billable and the project
bills at all. Two things are checked at the same moment, under the entry's own
row lock, so nothing can slip past between the read and the write: the receipt
rule (below), and that a kind change racing an upload cannot leave a mileage
line holding receipts.

**Submitting a claim freezes every one of its lines, in one transaction**, and
refuses the whole trip rather than half of it. Four things stop it, each naming
what is wrong: a claim that holds **no expenses at all** (there is nothing to
freeze and nothing to approve, and `canSubmit` says so before the button is
drawn); a line the tables can no longer price — a mileage rate or a per diem day
rate that has gone, a covered meal the table prices no deduction for; a **per
diem day left outside its own trip**, which only a change of the installation's
business time zone can do, and which the submit is the last place to notice; and
the receipt rule, applied per employee-paid outlay line and naming the line.
After the submit nothing recomputes a line's figures but an approver's rate
override and the project side's pricing — and both `PUT /claims/{id}` and the
statement that reprices a claim's per diem days carry the claim's own status in
SQL as well as in Go, so a regression writes nothing rather than repricing an
approved trip.

An expense may not be submitted, approved, rejected, unapproved or edited on a date
the period lock closes (see below), and the owner (or `expenses:manage`) is who may
submit; **a travel claim is judged on the day it departed**. Anyone approaching
this from the wrong side gets the ordinary 403/404 split described under
"Refusal codes".

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

Who may approve or reject a **submitted** unit: `expenses:approve` for anything,
or the manager role on its own project (the same role Time and Projects use) —
nothing else. A travel claim is judged on the *claim's* project, so a project's
manager approves the trips booked on it and never a project-less one. **`expenses:manage` does not, on its own, let anyone approve or
reject**; it is a separate, deliberately narrower set of powers (below).
**Self-approval is allowed**: an approver who is also the expense's owner may
approve their own line, exactly as Time allows.

`GET /approvals` is the queue: submitted **units** the caller may approve,
grouped one card per person, the person who has waited longest first, **paged by
person in SQL** so a page never splits a group across two pages. A group carries
its `entries` and its `claims` separately: a trip is one row with its purpose,
its window, its line count, its totals per currency and how many of its lines
want a receipt or carry a replaced rate — never a run of loose expenses, which is
what a line of it would look like among the entries. The group's own totals and
its two counts hold both kinds together, and the trip's own page (`GET
/claims/{id}`) is where its lines are. A caller who approves nothing at all — no
`expenses:approve`, no project managed — gets the access layer's 403 rather than
an empty page. `/approve`, `/reject` and `/unapprove` are all-or-nothing batches
of up to 500 distinct units: naming one id twice counts once, and a batch that
cannot move even one of its ids answers a message per refused id rather than
moving the rest.

**Unapprove** additionally accepts `expenses:manage` (`orManage`), takes an
approved unit back to a fresh **draft** — clearing the decision and the
submission stamp, so it goes round the loop again, and for a travel claim every
line's rate-override audit with them — and refuses one that has already been
reimbursed, or a trip holding a line that has been invoiced: undoing those has
its own door, on each track.

**Rate override.** `PUT /entries/{id}/rate` replaces a *submitted mileage line's
or per diem day's* rate and, optionally — on mileage alone — its passenger
supplement, an approver's (or `expenses:manage`'s) correction when the table's own
figure is wrong for this one trip. It is guarded by the revision the line was read at (409 on a stale one) and
records an audit: who overrode it, and — the first time a request replaces it —
what the line **had been priced at** (`rateOverride.tableValue`,
`rateOverride.passengerTableValue`), so a later reader can see what changed without
a second lookup. That figure is usually a row of the dated table; on a per diem day
of a claim abroad it is the claim's own `abroadDayRate`, which is why the field is
"what it was priced at" rather than "what the table said". Leaving the passenger rate out of a request keeps whatever
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
line has been invoiced**, and refused outright on a per diem day, which bills
nobody anything. The period lock does not reach it: pricing is bookkeeping
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
  `POST /reimbursed {entryIds?, claimIds?, date, reference?}` records one payroll
  run over every named **unit** that is approved and owes its owner something (a
  company-paid outlay owes nothing and cannot be marked, and neither can a trip
  whose lines come to nothing); `POST /reimbursed/undo` clears the whole stamp —
  the date, the reference and who made it — putting the units back in the waiting
  list exactly as they were. **A trip is paid as one**, for the sum of what its
  lines owe its owner — every employee-paid outlay, every mileage line and every
  per diem day — and the stamp goes on the claim, which each of its lines then
  reads. `GET /reimbursements` lists what a run would cover (or what has already
  been paid), grouped per person with the same two lists the approval queue
  carries, and paged the same way.
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
step of what the employee submitted and what an approver decided. A **travel
claim** is judged by the day it departed, and its lines with it: a trip that left
inside a closed period cannot be recorded, changed, submitted or decided on,
whatever the dates of the expenses it holds.

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
an outlay takes one at all — neither a mileage line nor a per diem day ever does,
and a save that would turn an outlay with receipts into a line of another kind is
refused (on `kind`) rather than stranding them.

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
- **Columns, in order**: `Unit;Purpose;Employee;User id;Date;Kind;Description;Category;Currency;Gross;VAT;Owed;Project code`.
  The header is English and untranslated on purpose — the file is read by a payroll
  system, not by every employee — and the project column is always present, even in
  an installation with no projects module, so a payroll system need not know which
  modules run.
- **One row per line.** A standalone expense is its own row; a travel claim
  writes one row per expense it holds. `Unit` says which — `expense 2001` or
  `claim 1012` — and `Purpose` carries the trip's own, empty for a standalone
  expense. A payroll system that wants the trip as one figure adds its rows; a
  person reading the file sees what each amount was for.
- **A per diem day** has no description of its own unless its owner wrote one, so
  its `Description` cell carries the per diem type (`day_6_12`, `overnight_hotel`,
  …) and its `Category` cell is empty — it is booked on none.
- Rows are ordered by the person's **display name**, then by the **unit's own
  day** (a standalone expense's entry date; a travel claim's departure day in the
  installation's time zone), then by the unit itself, and inside a unit by the
  line's date and id — a file read by a person, not by the database's own uuid
  order. The unit is part of the key so a trip's lines stand **together** under
  its `Unit` cell: a loose expense dated between two of a trip's days follows the
  whole trip rather than splitting it.
- Headers: `Content-Type: text/csv; charset=utf-8`,
  `Content-Disposition: attachment; filename="expenses-reimbursements-YYYY-MM-DD.csv"`
  (today in the business time zone, which is the day the clerk downloading it
  would write on the folder), `Cache-Control: private, no-store`.
- It takes the list's own filters, or explicit `entryIds` **and** `claimIds`
  instead of them; a selection that is present and names nothing at all is
  refused rather than read as "everything", and an id the export cannot hold is
  named under the list that named it rather than left out silently. One of a
  **claim's lines** named on `entryIds` is refused like every other standalone
  operation's (`Expense N belongs to travel claim M; export the claim`), even
  when the claim is named too: the unit a payroll run pays is the whole trip, so
  a file holds a trip whole or not at all.
- **Capped at 5 000 rows.** The cap counts *rows*, so a trip of forty lines
  costs forty of them. Over the cap is a 400 titled "Too many rows to export",
  with a `detail` asking for a narrower filter and **no `errors` object at all** —
  the file is not paged or truncated; half a payroll file is worse than none.

## Stats and attention

`GET /stats` (the app's own strip), `/stats/summary`, `/stats/timeseries` and
`/stats/attention` follow the same shape every other module's dashboard reads use.
The timeseries' one metric is `netAmount` — the caller's own approved expenses, net,
in the installation's default currency only, for the reason Time's hours are scoped
to the caller: a figure that changed meaning with the reader's permissions would
mean a different thing to every reader of the same card.

**Every figure counts units.** A travel claim is one draft, one submitted, one
approved — whatever it holds — and its lines are never counted beside it: their
own `status` column stays at its default and is never read, so counting them
would report five drafts for a trip whose owner can do nothing with one of them
on its own, while the one thing that *is* actionable would be missing. What the
caller is still owed (`unreimbursed`, `myUnreimbursed`) sums over every unit that
owes them, a claim's lines included, and `awaitingMyApproval` counts a trip
waiting for this approver once. The timeseries is the one figure that is per
*line*: it is money per day, and a trip's lines each fall on their own date.

Three attention types, in `GET /stats/attention`, each counted in units:

| Type | Told to | `entityId` | `count` |
| --- | --- | --- | --- |
| `expenseRejected` | the owner, about their own | the expense id (decimal), or `claim/<id>` for a travel claim | absent |
| `approvalWaiting` | whoever may approve | the **owner's** user id (uuid) | how many units are waiting |
| `reimbursementWaiting` | `expenses:manage` | the literal string `"reimbursements"` | how many units are waiting |

A rejected trip is **one** item, titled by its purpose rather than by any line's
description. Its entity is written `claim/<id>` because the two units number
independently and a bare `12` would not say which page to open.

`title` is a name — an expense's description, a person's display name — never a
finished sentence, **except `reimbursementWaiting`, whose title is a deliberately
untranslated English fallback** ("N expenses and travel claims are waiting to be
reimbursed" — the figure counts units, so the sentence names both) for a
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
| `GET /meta` | What this installation can do, the settings a new expense starts from (the business time zone included), the categories, and the caller's own capabilities |
| `GET /entries` (`userId`, `projectId`, `claimId`, `standalone`, `status`, `kind`, `from`, `to`, `reimbursed`, paging) | The caller's own; a project manager also sees their projects'; view-all/approve/manage see everyone's |
| `GET /entries/{id}` | The owner, the project's manager, or view-all/approve/manage; a bare 404 otherwise |
| `POST /entries` | Record one — your own, or (`userId`) a colleague's, with `expenses:manage`; `claimId` records it as a line of a travel claim |
| `GET /claims` (`userId`, `status`, `from`, `to`, `reimbursed`, paging) | The same visibility rule the entries' list applies, one level up |
| `POST /claims` | Record a trip — your own, or (`userId`) a colleague's, with `expenses:manage` |
| `GET /claims/{id}` | The claim with its lines, its totals per currency and its capabilities |
| `POST /claims/{id}/per-diem-suggestion` | Whoever may see the claim — the days its times imply, priced; it writes nothing |
| `PUT /claims/{id}`, `DELETE /claims/{id}` | Owner or `expenses:manage`, while draft or rejected, not past the lock |
| `PUT /entries/{id}`, `DELETE /entries/{id}` | Owner or `expenses:manage`, while draft or rejected, not past the lock |
| `POST /entries/{id}/attachments`, `DELETE /attachments/{id}` | Same as edit, an outlay only |
| `GET /attachments/{id}` | Whoever may see the expense |
| `POST /submit` | Your own draft and rejected units — expenses, travel claims, or both (or anyone's, `expenses:manage`) |
| `POST /approve`, `/reject` | `expenses:approve`, or the unit's own project's manager |
| `POST /unapprove` | Same, plus `expenses:manage` |
| `GET /approvals` | An approver; the units waiting, grouped per person; 403 for a caller who approves nothing |
| `PUT /entries/{id}/rate` | An approver or `expenses:manage`, on a submitted mileage line or per diem day |
| `PUT /entries/{id}/billing` | Financial rights on the entry's project; never a per diem day |
| `GET /entries/{id}/billing-lines` | Financial rights on the entry's project — the pricing dialog's own picker, not the caller's bookable-projects list |
| `POST /entries/{id}/invoiced`, `.../invoiced/undo` | Financial rights on the entry's project; never a per diem day |
| `GET /reimbursements`, `/reimbursements/export.csv`, `POST /reimbursed`, `/reimbursed/undo` | `expenses:manage`; the unit is an expense or a whole trip |
| `GET /projects` | The caller's own bookable projects (or, `userId`, a colleague's, with `expenses:manage`) |
| `GET /categories` | Anyone in the app |
| `POST /categories`, `PUT /categories/{id}` | `expenses:manage` |
| `GET /rates` | Anyone in the app (the customer rate hidden without `expenses:manage`) |
| `POST /rates`, `PUT /rates/{id}`, `DELETE /rates/{id}`, `POST /rates/reset` | `expenses:manage` |
| `GET /settings` | Anyone in the app (the business time zone included — a client labels a trip's days in it) |
| `PUT /settings` | `expenses:manage` |
| `GET /stats`, `/stats/summary`, `/stats/timeseries`, `/stats/attention` | The caller's own figures, plus their approval queue's size |

That is all 44 operations the contract declares, each exercised by the module's own
coverage gate (below) with no allow-list.

## What comes next

**The project page's own Economy tab, on the cost side**: what a project's
expenses cost and bill, beside the hours Time already reports there.

Travel claims and per diem are done, front to back: the trip's own page at
`/expenses/claims/$claimId`, where the days are suggested and ticked off and
the whole trip is submitted; the approval queue and the payroll list, where a
trip is one selectable row beside the loose expenses and a decision on a mixed
selection is one request; and the settings page, where every rate kind and the
installation's business time zone are managed.

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
