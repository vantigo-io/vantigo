# Energy phase 4 and Invoices phase 5 — Energy consumption billing — research

Research for two roadmap phases that are one piece of work: Energy phase 4, "Invoicing
groundwork (align with Invoices)" (`ROADMAP.md:492-500`), and Invoices phase 5, "Energy
consumption billing" (`ROADMAP.md:1039-1045`). The first promises "an `internal/contracts`
interface (e.g. `contracts.ConsumptionProvider`) returning billable consumption per
customer/metering point/supply period" and a "price dimension: spot prices per price area
(NO1–NO5) and grid tariffs, decided together with the Invoices module"; the second,
"periodic invoices per supply point and period from Energy's figures, tariff and price
lines, batch issuing, the utilities' periodic-invoicing rules". Neither has started: the
energy package, its SQL and its pages hold no price, tariff or billing concept, and Energy
fills no provider slot (§7.1). This note builds on the 1A research
(`docs/superpowers/research/2026-09-26-invoices-module.md`, "1A", §7.7 and the phase 5
sketch at its end) and the phase 3 research
(`docs/superpowers/research/2026-10-05-invoices-work-to-invoices.md`, "R3"), whose
write-back design (`contracts.InvoicedWorkHolder`, module-boundaries rule 10) is the seam
this work rides on.

External sources were read on **2026-10-24**: regulations on Lovdata, Skatteetaten's
handbook, statements and rate pages, regjeringen.no and RME/NVE for the support schemes,
Nord Pool for the market time unit, the public spot-price feed itself for its shape.
Anything not confirmed from a primary source is marked **UNCERTAIN**. Code references are
`file:line` against `main` at `df9bf81d`; `srv/` abbreviates `apps/server/internal/`,
`mig/` `srv/db/migrations/`, `MB` the contributing page
`docs/src/content/docs/en/contributing/module-boundaries.md`, `R/` the reference pages
under `docs/src/content/docs/en/reference/`.

## 1. Why now, and what Vantigo's energy business is

- The Energy reference page says what the module is for: "the metering points an energy
  supplier supplies: where each one is, which meter sits on it, which customer it is
  supplied to and for how long, and the consumption read from it" (`R/energy.md:10-13`).
  The business is a **kraftleverandør** (electricity supplier) in the Norwegian retail
  market, with its own record of metering points (målepunkt, GSRN), meters and supply
  periods — not a grid company (nettselskap), though a supplier that also owns a local
  grid or sub-meters a property bills grid services too, and the roadmap names "grid
  tariffs" as part of the price dimension.
- The Invoices roadmap says phase 5 "unblocks the energy business's core revenue"; the 1A
  research put it last "because it needs batch issue, schedules, mature payment matching,
  and the heaviest volume; Energy has no prices or `ConsumptionProvider` yet"
  (1A §9, phase 5). Phases 1A–4 have since landed: the sales document, EHF, KID, OCR/camt
  payment files, reminders, the payments port, and the work write-back (R3) that this
  design reuses.
- Elhub (Energy phase 5) is not a precondition. Consumption arrives today by manual
  reading and by file (`R/energy.md`, "Importing consumption"); the billing basis is
  whatever current revision the module holds, and § 7-5's rule that invoices rest on
  Elhub's values (§3.4) is met the day the Elhub sync writes them as `Source=Elhub`
  revisions.

## 2. The bookkeeping rules for metered consumption

### 2.1 When the sales document may be issued (bokføringsforskriften)

| Rule | Text | For energy |
| --- | --- | --- |
| § 5-2-2 | "snarest mulig og senest en måned etter levering" | the main rule, which § 5-2-5 relaxes |
| § 5-2-4 | continuous services within a month after the VAT term (two months) | not needed: § 5-2-5 is the lex specialis |
| **§ 5-2-5** "Strøm, telefoni og lignende" | "For tjenester som leveres basert på måling av faktisk forbruk (strøm, telefoni og lignende), kan det utstedes salgsdokument for lengre perioder, begrenset til en periode på ett år. Salgsdokumentet skal så vidt mulig utstedes innenfor det kalenderår levering finner sted." | a metered supply may be invoiced for up to a year at a time, in arrears, and the invoice for a year's consumption should be issued within that calendar year |
| § 5-2-6 / § 5-2-7 | advance invoicing of "abonnementer, leier, avgifter og lignende", up to a year | Skattedirektoratet 26 Jan 2010: "Avtaler om løpende levering av kraft kommer inn under unntaket i bokføringsforskriften § 5-2-5", and a running supply contract may also be invoiced in advance as a subscription (https://www.skatteetaten.no/rettskilder/type/uttalelser/prinsipputtalelser/forskuddsfakturering-av-strom/) |

Read together with the market regulation (§3): the bookkeeping rule permits long periods,
the market regulation forbids them for consumers — grid services and electricity to a
**consumer** are invoiced in arrears **at least every three months**, on hourly values
where the meter has them. So a design needs a **billing cadence per supply** (monthly by
default, quarterly at most for consumers, up to a year for a business customer that
wants it), and **advance invoicing is left out**: § 7-1b's four-week cap and its price
and volume estimate rules (a futures price, last year's Elhub volume) are a product of
their own, and the roadmap's phrase is "periodic invoices … from Energy's figures".

### 2.2 Content (§ 5-1-1 and GBS)

The sales document's content rules are 1A §2's and unchanged: number, date, the parties,
"ytelsens art og omfang", the delivery time and place, the consideration, the VAT. For a
metered supply "art og omfang" is the quantity in kWh, the price per kWh and the period;
the delivery period is the consumption period, so the document's `delivery_from/to`
(`mig/00034_invoices_baseline.sql`, `ck_invoices_delivery`) carries it, and the line text
names the metering point. Corrections of an issued invoice are credit notes (1A §2.3).

### 2.3 Later corrections of the figures

Metered values are corrected after the fact (a meter swap, an estimate replaced by a
reading, an Elhub correction). The market regulation's § 7-5 (2) says what to do: settle
the difference "i påfølgende fakturaperiode" — on the next invoice, as a correction line,
not by crediting and reissuing (§3.4). Bookkeeping-wise a correction line on a later
invoice is a new sale or a reduction; a negative correction that exceeds the invoice's
other lines makes the document a credit note, which the Invoices module already refuses
to issue as an invoice (`invoice_total_not_positive`, R3 design D7).

## 3. The market regulation: forskrift om kraftomsetning og nettjenester

FOR-1999-03-11-301, last amended by FOR-2025-06-27-1364 in force 2026-01-01
(https://lovdata.no/dokument/SF/forskrift/1999-03-11-301). Chapter 7, "Fakturering av
forbrukere", binds invoices to **consumers** (private households); a business customer is
bound by the contract and the bookkeeping rules only.

### 3.1 § 7-1a and § 7-1b — frequency and basis

- § 7-1a (2): grid services are invoiced "etterskuddsvis minimum hver tredje måned på
  bakgrunn av avlesning", in periods "av tilnærmet lik varighet"; for hourly-metered
  consumers "på grunnlag av faktisk forbruk med timesoppløsning".
- § 7-1a (3): estimated consumption may be invoiced only when a reading would cost the
  grid company "urimelig kostnad eller ulempe", and then "Det skal opplyses på fakturaen
  at forbruket er stipulert."
- § 7-1a (4): a consumer may ask for à-konto invoices between the regular ones.
- § 7-1b (1)–(2): the supplier invoices electricity, and § 7-1a's second to fourth
  paragraphs apply to it.
- § 7-1b (3)–(6): advance invoicing of electricity is allowed with "maksimalt fire uker
  mellom fakturaforfall og tidspunkt for levering", the volume from "tilsvarende periode
  året før, slik dette er registrert i Elhub", the price from a monthly futures contract
  capped at "høyeste stengningspris siste to uker" — the rules Tibber was fined 8 MNOK
  for breaking (NVE decision 202219867-21). Out of scope here (§2.1).

### 3.2 § 7-2 — what a consumer's invoice must show

For electricity (§ 7-2 second paragraph, a–e): (a) clear and easy to understand; (b) the
billing basis "med separate linjer for alle priselementer", the volume, whether the supply
is leveringsplikt, and — for a contract tied to the spot price — a clear statement when
the price is **not** weighted hour by hour against consumption; estimated values stated as
such; (c) the metering point's **elspot area** and that offers can be compared at
strømpris.no; (d) the **name of the contract**, its duration and how price changes are
notified; a price guarantee's duration; (e) the right to complain, with Elklagenemnda's
contact details. For grid services (first paragraph, a–g) also: a graphical comparison
with the same period last year, Enova's website, how to switch supplier, and any reduction
from a renewable production facility.

Consequences for the design: every price element is its own line (spot energy, the
markup, the fixed fee, each grid element, each tax, each support deduction); the metering
point id and price area, the kWh and the period are on the document; the contract's name
is printed; "estimated" is flagged when any interval in the period is `Estimated`; a
spot-price contract is priced hour by hour (so the "not weighted" statement is never
needed); the consumer-only footer (strømpris.no, Elklagenemnda, Enova) is a PDF block
switched on for a person customer.

### 3.3 § 7-3, § 7-4a, § 7-4b — joint invoicing and gjennomfakturering

- "Felles fakturering" is any joint invoicing of grid services and electricity;
  "gjennomfakturering" is the supplier invoicing both, the grid company's invoice going to
  the supplier "som betaler på vegne av sluttbruker" (§ 1-3). Under § 1-4a the grid
  company never sends its invoice to the consumer; it sends the e-invoice and the
  invoicing basis to the supplier.
- § 7-3: the joint invoice follows both sets of rules; both parties publish the original
  grid invoice on their websites, without the grid company's payment information; the
  supplier shows consumption data for this and last year; logos and contact details side
  by side; a paper-invoice fee is allowed.
- § 7-4a: a grid company offering joint invoicing must offer gjennomfakturering to every
  supplier; a supplier that takes it must use it for **all** its consumers in that grid,
  and say so in the supply contract; a change of invoicing method needs a month's notice.
- § 7-4b: the agreement's terms — a bank guarantee capped at a national-average month's
  grid rent per metering point, Ediel exchange formats, payment "minst 20 kalenderdager
  etter fakturautstedelse", the consumer's payment to the supplier discharging the grid
  debt, the supplier paying the grid company whether or not the consumer paid, refunds
  forwarded "senest ved sin førstkommende fakturautstedelse".

Consequence: under gjennomfakturering the grid rent on the supplier's invoice is **the
grid company's figure, passed through** — the supplier does not recompute nettleie from a
tariff sheet; it receives an invoice basis per metering point over Ediel/NBS and
re-presents it. That exchange is a project of its own (an Ediel/NBS adapter, the
guarantee, the payment forwarding) and is out of scope. What the roadmap's "grid tariffs"
can mean today is the **tariff the installation itself charges** when it is the grid
operator for its own points (a local grid, a property's sub-metering, an
områdekonsesjonær), and that is how the design reads it (§9). A supplier that bills only
energy leaves the grid tariff empty.

### 3.4 § 7-5 — the Elhub basis and corrections

"Nettselskap og kraftleverandør skal benytte målte og stipulerte måleverdier i Elhub" for
settlement and invoicing (1); when values are corrected they "gjøre opp mot sluttbrukere
for avvik i avregning og fakturering i påfølgende fakturaperiode" (2). The module's
revision model (a correction supersedes, never overwrites, `R/energy.md`, "Consumption")
keeps the figure an invoice was built on and the figure that replaced it, which is what a
correction line needs.

## 4. Taxes, levies and VAT on an electricity invoice

### 4.1 Avgift på elektrisk kraft (elavgift)

Skatteetaten (https://www.skatteetaten.no/bedrift-og-organisasjon/avgifter/saravgifter/om/elektrisk-kraft/)
and the 2026 rate table (regjeringen.no, avgiftssatser 2026): **7,13 øre/kWh** the whole
year (the winter/summer split of 2025 — 9,79 January–March, 16,93 April–September — is
gone), reduced rate **0,60 øre/kWh** for industry and mining (NACE 05–33 and the named
codes), data-centre and other special cases, and **exemption** for households and public
administration in Finnmark and the designated part of Troms, among others. The 2027
proposal is 7,32 øre. "Sluttkunder betaler elavgiften til nettselskapet sitt.
Nettselskapene rapporterer og betaler til Skatteetaten": the tax is the grid company's to
collect, on the grid invoice. A supplier that does not bill grid services never bills
elavgift; one that is the grid operator does, per kWh, at the point's class.

### 4.2 Enova-avgift

The levy to Energifondet is on the grid invoice too: 1,0 øre/kWh for households, 800 kr a
year per metering point for businesses (Elvia's 2026 tariff sheets; the statutory basis
is forskrift om innbetaling av påslag på nettariffen til Energifondet — **UNCERTAIN** that
the business amount is unchanged for 2026, read from one grid company's sheet).

### 4.3 VAT

- Electricity is a good at the standard rate, 25 % (1A §3). Grid services, elavgift and
  the Enova levy are in the VAT base at 25 %.
- **mval § 6-6**: "Omsetning av elektrisk kraft og energi levert fra alternative
  energikilder til husholdningsbruk i fylkene Finnmark, Troms og Nordland, er fritatt for
  merverdiavgift" (zero-rated, category Z, 0 %), including mixed use "etter samme tariff
  og på samme måler" (MVA-håndboken M-6-6). mvaf § 6-6-1 defines husholdningsbruk: "eneboliger,
  leiligheter, hybler og fritidsboliger, herunder hytter, setrer, koier o.l.", care homes
  and children's institutions but not nursing homes, and "fellesvaskeri, garasjer og andre
  fellesanlegg i eller for boliger"; district heating counts as an alternative source,
  fossil sources do not (§ 6-6-2). The supplier decides the exemption itself before
  issuing (self-declaration).
- So a metering point needs to know whether it serves **household use**, and the invoice
  needs the point's county — derivable from the postal code (8000–9999 is Nordland, Troms
  and Finnmark) with a hand override, since the module stores no county.

### 4.4 The support schemes: strømstøtte and Norgespris

Both are the **grid company's** calculation and payout, under lov 20. juni 2025 nr. 44 and
FOR-2025-09-08-1791 (strømstønad) / FOR-2025-09-08-1790 (Norgespris), both in force
2025-09-15 and repealed 2029-12-31:

- Strømstøtte: 90 % of the hourly elspot price above "terskelverdien 75 øre per kWh
  eksklusiv merverdiavgift", the first 5 000 kWh a month per metering point, computed by
  the grid company from Elhub's hourly or profiled values; paid as a deduction on the grid
  invoice, the excess paid out by the due date.
- Norgespris: a contract **with the grid company** (§ 4–§ 9), "40 øre per kWh, eksklusiv
  merverdiavgift" from 2025-10-01 to 2026-12-31 (§ 10), the hourly "prissikringsverdi"
  being "differansen mellom elspotpris i budområdet" and the reference price (§ 3 l),
  times consumption up to 5 000 kWh (households) or 1 000 kWh (cabins) a month (§ 13),
  netted on the grid invoice — the customer pays the supplier the contract's price as
  before, and receives or pays the difference through the grid company (§ 23). A point
  cannot have both (regjeringen.no Q&A).
- Under **gjennomfakturering** the supplier "skal sørge for at utbetaling til sluttkunden
  skjer gjennom fradrag på faktura", the grid company transfers any excess to the
  supplier, and the supplier's invoice must state participation, consumption and the net
  amount, and that RME publishes the hourly values (§ 23 / strømstønad § 16).

Consequence: for a supplier, these amounts are part of the grid company's invoice basis
(§3.3) and arrive with it; Vantigo never computes them. For an installation that is the
grid operator they would be computed from the spot prices it already holds — a later
delivery, named and left out (§9).

## 5. Prices

### 5.1 The day-ahead price and its resolution

Nord Pool's single day-ahead coupling moved to a **15-minute market time unit on
2025-09-30 (delivery from 2025-10-01)**; intraday added 15-minute products on 2025-03-19
(nordpoolgroup.com, "Transition to 15-minute market time unit"). The public feed
`https://www.hvakosterstrommen.no/api/v1/prices/<YYYY>/<MM-DD>_<area>.json`, sampled on
2026-10-24, still answers **hourly** entries (24 on 2025-09-30 and 2025-10-01, 25 on the
DST night 2025-10-26, 23 on 2026-03-29), each `{NOK_per_kWh, EUR_per_kWh, EXR,
time_start, time_end}` with the local offset in the timestamps. So prices must be stored as
**intervals of any length**, exactly as consumption is (`R/energy.md`, PT1H today, PT15M
later), and a settlement must price an interval by the price interval that contains it —
or, when the two resolutions differ, by splitting the finer one. Official sources with
15-minute data are Nord Pool's data portal (a paid subscription) and the ENTSO-E
transparency platform (free, a security token, XML). The feed is a reasonable default for
a Norwegian supplier's day-ahead prices, with a file import as the fallback and the
auditable path.

### 5.2 Currency

The feed carries NOK and EUR with the exchange rate; Nord Pool prices in EUR. Vantigo
invoices in NOK (R3 D11: a document always takes the settings' currency, work in another
currency is refused); the NOK price per kWh is what a settlement needs, and the EUR figure
is kept for the record.

### 5.3 The supplier's own prices

The contracts a Norwegian supplier sells, and what strømpris.no lists: **spot** (the hourly
area price plus a markup in øre/kWh, plus a monthly fee), **fixed price** (øre/kWh for a
term), **variable** (a price the supplier sets and must notify 30 days ahead — not built;
it is a fixed price with versions), and the regulated **leveringsplikt** (§ 7-2 b asks
whether the supply is one; not built — a flag at most). The spot price "weighted hour by
hour against consumption" (§ 7-2 b) is Σ over intervals of kWh × price, the only honest
computation when hourly values exist; a monthly average price × kWh is what the regulation
requires a warning for.

### 5.4 Grid tariffs since 2022

The "ny nettleiemodell" from 2022-07-01: an **energiledd** in øre/kWh, often split day
(06–22 on weekdays) and night/weekend, and a **kapasitetsledd** in kr/month by tier,
the tier chosen by "gjennomsnittet av de tre timene med høyest strømforbruk, fordelt på
tre ulike dager i løpet av en kalendermåned" (whole clock hours, kWh/h). Tiers are grid
company specific (0–2, 2–5, 5–10, 10–15, 15–20, 20–25, 25–50, 50–75, 75–100, >100 kW is
a common ladder). Businesses often have a fixed monthly element and, above a size, an
effektledd by measured peak. Elavgift and the Enova levy are collected with the grid
rent (§4.1–4.2).

## 6. What comparable systems do

Norwegian supplier systems (Hansen/Enoro CIS, Utilitarian, Elhub-integrated SaaS such as
Fjordkraft's and Tibber's in-house) all separate **avregning** — the computation of what a
supply owes for a period, with its basis frozen — from **fakturering**, the document; a
billing run selects supplies due by cadence, settles each, builds one document per
customer (one line group per metering point), issues the batch, and leaves an exception
list for supplies that could not be settled (no prices yet, missing intervals, a period
not closed). Corrections are new settlements that carry the difference against what was
invoiced. That split is what the design takes: Energy settles, Invoices documents.

## 7. Where the codebase stands

### 7.1 Energy

- Schema (`mig/00005_energy_baseline.sql`, `mig/00049_energy_imports.sql`):
  `metering_points` (`gsrn`, address columns, `price_area` validated as two letters and
  one or two digits, `grid_area` free text, `expected_annual_consumption_kwh`,
  `connection_status`), `meters`, `supply_periods` (`customer_id` opaque, half-open
  `[start, end)`, `status Active|Ended|Cancelled`, the GiST exclusion against overlap),
  `consumption_intervals` partitioned by month (`quantity_kwh numeric(14,3)`,
  `quality Measured|Estimated|Corrected|Manual`, `source Manual|Import|Elhub`,
  `is_current`, `supersedes_id`). No price, tariff, product, contract or invoiced column.
- The market time zone is derived from the price area (`srv/energy/consumption.go:102-117`);
  there is no time-zone column. The frontend hard-codes Europe/Oslo
  (`apps/energy/frontend/src/lib/market-time.ts`).
- The customer-gated aggregate (`srv/energy/queries/consumption.sql:120-152`) is the
  privacy predicate every billable read must keep: an interval counts for a customer only
  when it lies inside one of that customer's non-cancelled supply periods on the point.
  The per-period monthly sum the GDPR export uses
  (`srv/energy/queries/customer_personal_data.sql:29-39`) is the closest existing
  settlement-shaped read. Range filters are `start >= from AND end <= to`: an interval
  straddling a boundary is dropped, which a settlement must not do (it splits or refuses).
- Every supply-period write takes `pg_advisory_xact_lock(0x53555052, meteringPointID)`
  (`srv/energy/supplyperiods.go:80-83`) under `db.RetrySerializable`. The switch produces
  contiguous periods (`:358-402`).
- Contracts: Energy implements `CustomerReferenceHolder` and `CustomerPersonalData`,
  consumes `CustomerDirectory`, provides no single-provider slot (`srv/energy/module.go:73-82`).
  Permissions: nine `energy:*` keys, `energy:import` sensitive (`:34-44`).
- Workers: `energy-import` only when `EnergyImportEnabled` (`:89-94`); the import reads
  files through `internal/fileimport`. The calendar module's guarded fetch client
  (`srv/calendar/subscriptions_fetch.go`: netguard dialler, no proxy, no redirect, 1 MiB
  cap) is the pattern an outbound price fetch copies.
- Docs: `R/energy.md` (sources: the package, the OpenAPI, both migrations — a new
  migration is added there), `en|nb/user/energy.md`; no admin page; `en/reference/index.md`
  carries the module's one-line scope.

### 7.2 Invoices

- The sales document and its lines (`mig/00034_invoices_baseline.sql`): a line has
  `description`, `quantity numeric(12,3)`, `unit`, `unit_price numeric(14,4)`,
  `discount_percent`, `vat_code_id`, the three money columns and the issue snapshot; **no
  line period, kind or product**; the document carries `delivery_from/to`. The row-as-jsonb
  immutability trigger freezes any new column automatically.
- Phase 3's link (`mig/00040_invoices_work.sql:58-86`): `line_sources` with
  `source_kind` **CHECKed to the three kinds**, `source_id`, `source_revision`,
  `source_subkind`, **`project_id NOT NULL`**, `quantity > 0`, `amount numeric(22,8)`,
  `currency`, `state held|invoiced|released`, `source_date`; the partial unique index
  `ux_line_sources_live`; the trigger that allows only `held→invoiced` under a draft and
  `invoiced→released` under an issued parent. The three kinds are also fixed in
  `contracts.InvoicedWorkOrder`, `parseFromWork`/`parseLineSources`
  (`srv/invoices/work.go:517`, `sources.go`) and in the schema test's regex
  (`srv/db/schema_test.go`), which would flag a literal `'energy.…'` in an invoices
  migration as a cross-schema reference unless the kind is named without the schema word.
- The wizard's order (`srv/invoices/work.go:864-874`): parse, the append target, the
  5 000-source cap, settings, the billing profile and `customerGate`, the billable reads
  by id, the projects, `judgeSources`, the VAT codes (`resolveVatCodes`, `:729`, per kind
  from the settings' `work_vat_code_*`, code 9 "O" when not VAT-registered), grouping
  (`grouping.go:212`), the 500-line cap, the delivery period as the sources' span, then
  one `withLockedTx` that inserts the draft, `writeLines`, `insertSources` ordered by
  `(kind, id)`, and maps the unique violation to `source_held_elsewhere`.
- The issue (`srv/invoices/issue.go:255`, `issueLocked` `:362`): before the lock the
  billing profile and `readIssueWork` (`writeback.go:179`, which refuses
  `projects_unavailable` for any draft holding sources when Projects is off); under the
  lock the document, settings, the counter, the checks, the VAT summary, the KID, then
  `markIssueWork` (`:282`) calling the holders in `InvoicedWorkOrder`, then the writes.
  After commit the PDF (`pdfstore.go:328`); EHF only on `send-ehf`; no automatic delivery.
- **No batch issue.** The closest shapes: the quick invoice (draft + lines + `issueLocked`
  in one transaction, `quick.go:83`) and reminder runs (`reminder_runs.go`: up to 500
  items, directory reads first, one transaction per item, per-item skips recorded).
- **Worker mode** resolves only `Directory`, `Users`, `Calendar`, `Accounts` and the
  many-provider slots (`srv/module/workers.go:23-80`): a worker's `Deps` has no
  `Projects` and no `Billable*`, so an issuing worker today would refuse every draft with
  sources. A batch issue either runs on the API side or `Workers` learns the slots it needs.
- Money: exact `big.Rat`, `computeLine` rounds gross and allowance to two decimals
  (`money.go:47`); the VAT summary per (category, rate). Bounds: quantity ≤ 999 999 999,999,
  unit price ≤ 9 999 999 999,9999 — fine for kWh and øre. The EHF unit table already maps
  `kWh` → `KWH` (`R/invoices.md`, "Units").
- VAT codes: nine seeded (`mig/00034:339-362`): id 1 code 3 S 25 %, id 5 code 5 Z 0 %,
  id 8 code 6 E, id 9 code 7 O. No Products link.
- Docs: `R/invoices.md` ("What comes next" lists "5: energy consumption billing"),
  `en|nb/user/invoices.md` with the phase 3 sections as the template.

### 7.3 The platform

- Slots: twelve single-provider slots resolved by `soleProvider` in module order
  (`srv/module/compose.go:275-299` for the `Billable*` three), six many-provider slots;
  `withInvoicedWork` refuses a kind two holders claim (`:689-722`). Adding a slot: a
  `Deps` field, a `Module` field, the `soleProvider` block, `compose_test.go`, a
  `modtest` option, MB rule 5's list.
- Migrations: `mig/000NN_<module>_<name>.sql`, goose, one schema per file
  (`srv/db/schema_test.go` refuses a file touching two), the module's `sqlc.yaml` lists
  its own; next free number **00064**. Regeneration:
  `cd apps/server && mise exec -- go generate ./...` then `mise exec -- bun run gen:client`.
- Customers: `contracts.CustomerBillingProfile` carries `Type "business"|"person"`,
  `Status`, the invoice address, language, delivery, payment terms, Peppol id
  (`srv/contracts/directory.go:74-129`) — the household/consumer distinction and the
  buyer's language the line text needs are already there.

## 8. Patterns to take

- **R3's write-back**, unchanged: a settlement is a row Energy owns, held by a draft
  through `line_sources`, judged and stamped by Energy's `InvoicedWorkHolder` inside the
  issue's transaction, released by the credit note. Phase 5 adds a kind, not a mechanism.
- **The `Billable*` read shape**: a request validated by the contract, a page with `More`,
  amounts as exact decimal text, the provider reading on the pool and never calling a
  directory back.
- **The uninvoiced view and the wizard** (R3 D3): a per-customer card of what is ready,
  a wizard that builds the draft, the per-kind VAT code in the settings, line text in the
  buyer's language.
- **The import pattern** (energy import design): a file through `internal/fileimport`, a
  dry run, a worker; and **the guarded fetch** (calendar subscriptions) for the price feed.
- **Reminder runs** for the batch: a run row, items, one transaction per item, skips
  recorded, a page that shows progress.

## 9. What the design must decide

1. **Who prices.** Energy, in a settlement per supply period and period, answering priced
   lines (quantity, unit, unit price, amount, a VAT hint, the consumption facts); Invoices
   makes documents and never sees an hourly series. The price dimension is Energy's by the
   roadmap, and pricing kWh × hourly price is energy arithmetic.
2. **The settlement as the source row.** A row with an id and a revision, the frozen
   figures, `draft → invoiced → released`, recomputed while draft; the kind
   `energy.settlement` held by `line_sources` like a time entry. Corrections as new
   settlements carrying the difference against the invoiced one (§3.4).
3. **Spot prices** as intervals per price area, imported from a file or fetched daily by a
   worker from a configured feed (default the public feed), the finest resolution the
   source gives; a settlement refuses a period with a price gap.
4. **Tariffs**: the supplier's contract (spot + markup + monthly fee, or a fixed price),
   versioned by validity, named (§ 7-2 d), assigned to a supply period; the installation's
   own grid tariff (energiledd by time of day, kapasitetsledd by top-three-hour average,
   a fixed element, elavgift by the point's class, the Enova levy) assigned to a metering
   point, empty for a supplier that bills only energy. Pass-through of a grid company's
   invoice basis and the support schemes: later, named.
5. **The consumer's invoice content** (§3.2) and the VAT exemption (§4.3): a household
   flag and the county from the postal code, the contract's name on the line, the
   "estimated" flag, the consumer footer on the PDF, a settings VAT code for energy and
   one for the exempt case.
6. **Cadence and the batch**: a billing cadence per supply period (monthly default,
   quarterly, yearly for businesses), a billing run that settles what is due through a
   date, drafts one document per customer, issues them, records skips; run on the API side
   by a person, with the run's work done by a worker that has the slots it needs.
7. **The lines' period.** A line has no period column; the document's delivery period is
   the settlement period, and a document mixing periods (a correction beside the month)
   takes the span, as `widenDelivery` does for work. EHF's per-line period (BT-134/135)
   stays out, as R3 left it.
8. **What stays out**: advance invoicing and à-konto for energy, Ediel/NBS and
   gjennomfakturering's exchange, strømstøtte and Norgespris computation, leveringsplikt,
   the year-on-year consumption graph (grid invoices only, and the supplier is exempt
   under § 7-3 (3) when it shows the figures), eFaktura/AvtaleGiro, Elhub.

## 10. UNCERTAIN

- **U1** The Enova levy's business amount for 2026 (800 kr/year, from one grid company's
  sheet) and whether a supplier that is not a grid operator ever shows it.
- **U2** Whether the public feed will move to 15-minute entries; the storage is
  resolution-free either way.
- **U3** How strictly "av tilnærmet lik varighet" (§ 7-1a (2)) is read when a supply
  starts mid-month: the first period is shorter by nature; the design bills the partial
  month and then the cadence.
- **U4** The exempt case's VAT category on EHF: Z (zero-rated) with an exemption reason
  naming mval § 6-6, as the module's code 5 already is; **UNCERTAIN** whether an EHF
  recipient expects E with a reason instead. The design uses the Z code and lets the
  settings override.
- **U5** Whether § 7-2 (2) b's "separate linjer for alle priselementer" requires the
  markup as its own line when the contract's price is "spot + markup": the design prints
  it separately, which is also what strømpris.no's price model shows.
