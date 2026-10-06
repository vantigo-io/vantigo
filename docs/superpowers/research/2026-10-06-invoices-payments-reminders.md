# Invoices phase 4 — Payments and reminders — research

Research for Invoices phase 4, "Payments and reminders" (`ROADMAP.md:851-870`): OCR giro
and camt.054 imports matched on KID with an exception queue, an overdue list and reminder
runs under the 14-day rules and a dated rates table, the B2B standard compensation,
per-customer reminder settings, an inkasso hand-off export, a `PaymentProvider` port with
Vipps MobilePay ePayment first, and the quick invoice. Four studies feed it: the
Norwegian rules on late interest, reminders and collection (§2); the bank payment files
(§3); Vipps MobilePay ePayment (§4); and the codebase's seams (§5). It builds on the
phase 2 research (`docs/superpowers/research/2026-10-03-invoices-ehf-peppol-kid.md`,
"P2"), which settled the KID itself, and does not repeat the invoice-content rules of
phases 1–3. Every external source was read on **2026-10-06** unless another date is
given. Statutes and regulations were read on Lovdata; Finanstilsynet, Skatteetaten and
Finansdepartementet pages are administrative practice; Finansklagenemnda and Sivilombudet
are nemnd practice; vendor, bank-customer and advisory pages are secondary sources only.
Anything not confirmed from a primary source is marked **UNCERTAIN**. Code references are
`file:line` against `feat/invoices-payments-reminders` at `0ba2840e` (identical to
`main`); `srv/` abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`, `fe/`
`apps/invoices/frontend/src/`, `host/` `apps/host/frontend/src/`, and `invoices.md` the
reference page `docs/src/content/docs/en/reference/invoices.md`.

## 1. Why now, and what changes the roadmap's assumptions

### 1.1 What phase 4 is for

Phase 1B registers payments by hand and derives `overdue` from the due date; phase 2 put
a KID on every invoice issued under a bank agreement; phase 3 made work into invoices.
Phase 4 closes the loop on money in: the bank tells Vantigo what was paid, Vantigo tells
the customer what is late, and a Vipps request lets a customer pay on the spot or from a
link. The roadmap's own line: "receivables without a spreadsheet; Point of sale's payment
adapters" (`ROADMAP.md:870`).

### 1.2 Seven findings that reshape the plan

1. **A new inkassolov is adopted but not in force.** LOV-2026-05-22-19 repeals the
   inkassoloven of 1988 from a date "Kongen bestemmer"; none was set on 2026-10-06. Finans
   Norge (secondary) says the authorities have signalled **2027-01-01**. Under it only
   inkassoforetak send the statutory inkassovarsel (new § 20) and betalingsoppfordring
   (§ 21); a creditor's own fee-bearing letters become "gebyrbelagte kravbrev" under a
   forskrift still to come (§ 19), and the fee for a creditor's own inkassovarsel and
   betalingsoppfordring is not continued (Prop. 3 L (2025–2026) 13.4.4). Every reminder
   rule that rests on today's inkassolov and inkassoforskrift has a known end and an
   unknown successor. **The reminder rules must be dated data**, so the regime can change
   on the in-force date without a code change (§2.1).
2. **The § 3a compensation is not a share of the inkassosats.** Forsinkelsesrenteloven
   § 3a fixes it at **40 euro**, converted to NOK by regulation every half-year with the
   interest rate: **430 NOK from 2026-07-01**. It is a separate dated value, B2B only, and
   it offsets the reminder fees rather than adding to them (§2.3).
3. **A Vipps payment request lives 10 minutes.** Long-living ePayments are restricted in
   Norway and capped at 24 hours, with the customer present. An invoice with a 14-day term
   therefore cannot carry a Vipps link. It needs **a stable pay page on the installation**
   that creates the ePayment when the customer opens it — a public HTTPS endpoint that
   Vantigo does not have today (§4.2, §5.7).
4. **A payment needs a source and an optional user.** `invoices.payments.
   registered_by_user_id` is `NOT NULL` (`mig/00035_invoices_payments_delivery.sql:21`).
   A bank import has an uploading user; a Vipps poll or webhook has none (§5.1).
5. **Payments have no external reference.** No unique bank or provider key exists, and
   1B left the idempotency key out on purpose (`invoices.md:2125`). Re-importing a file
   would register its payments twice (§3.7, §5.1).
6. **Overpayment is refused today.** `payment_exceeds_open` and `invoice_settled` are 409s
   (`srv/invoices/payments.go:161-171`). An imported overpayment or a second payment of a
   settled invoice cannot become a payment; it must go to an exception queue unless the
   spec changes that rule (§5.1, §6 item 2).
7. **A reminder cannot be a document kind or take a number.** `ck_invoices_kind` allows
   `invoice` and `credit_note` only (`mig/00034_invoices_baseline.sql:170`), and the one
   counter is the gap-free salgsdokument series (`srv/invoices/queries/counters.sql:15-22`).
   A reminder needs its own table, PDF key and delivery log (§5.3).

One thing is already in place: the customer side holds `reminder_email` and
`reminder_delivery` (`mig/00019_customers_billing_profile.sql:10,15`), resolved through
the directory's billing profile. Where to send a reminder is known; the reminder *policy*
has no home (§5.5).

## 2. The law

Source ids in brackets refer to §8.1. **LAW** is statute or regulation; **PREP**
preparatory works; **ADMIN** Finanstilsynet, Skatteetaten or Finansdepartementet;
**NEMND** FinKN or Sivilombudet; **PRACTICE** vendors and other secondary sources.

### 2.1 Two regimes: the inkassolov of 1988 and the one adopted in 2026

- **LOV-2026-05-22-19** (new inkassoloven), kunngjort 22.05.2026, "Ikrafttredelse: Kongen
  bestemmer". It repeals LOV-1988-05-13-26 from its in-force date (§ 55). Lovdata's header
  on the current law says it "oppheves ved lov 22 mai 2026 nr. 19 (i kraft fra den tid
  Kongen bestemmer)". **LAW** [NYINKL, INKL]
- **In-force date UNCERTAIN.** Finans Norge says the authorities signalled **2027-01-01**,
  with the regulations to go on hearing "før eller etter sommeren" (**PRACTICE**). No hearing
  on a new inkassoforskrift was found on regjeringen.no.
- What changes for a creditor collecting its own invoices (egeninkasso):
  1. **Only inkassoforetak send the statutory inkassovarsel.** New § 20 is addressed to
     "inkassoforetaket"; § 2 second paragraph applies chapter 1, § 6, chapter 3 part I
     (§§ 14–19), § 54 and chapter 9 to everyone and the rest to inkassoforetak only. **LAW**.
     Prop. 3 L 12.5.5 suggests creditors "orientere om at kravet vil bli sendt til et
     inkassoforetak dersom skyldneren ikke betaler". **PREP**
  2. **No fee for a creditor's own inkassovarsel or betalingsoppfordring** (Prop. 3 L
     13.4.4). **PREP**
  3. **Standardised egeninkasso fees continue, set in a new forskrift.** New § 19 lets the
     King set amounts with "ulike regler for egeninkasso og fremmedinkasso" and regulate
     "form og innhold i gebyrbelagte kravbrev og om når kravbrevene kan eller skal sendes
     ut". **LAW**. Level, timing and content are left to "senere forskriftsarbeid". **PREP**.
     The amounts and whether the 14-day timing survives are **UNCERTAIN**.
  4. **Rules on every creditor (new §§ 14–18):** good collection practice (§ 14); polite,
     accurate, clear, discreet communication, and electronic notices if sent "på en
     betryggende måte" (§ 15 first and fourth paragraphs); on request, information about
     the claim and the **dekningsrekkefølge** (§ 16); answers "snarest mulig", preferably
     within a week (§ 17 second paragraph); **no recovery costs once the debtor has
     objected**, unless the objection is obviously groundless (§ 18 third paragraph); no
     costs for handling objections, none on claims incurred as a minor, and a consumer's
     first bill sent after the due date may carry only the pre-due fee (§ 18 fourth to
     sixth paragraphs). **LAW**
  5. The contact-hour limit (Mon–Fri 08–21, Sat 09–15, not holidays; § 15 second paragraph)
     binds inkassoforetak only. **LAW**
  6. Forsinkelsesrenteloven § 4 b is amended so a consumer pays no fees beyond
     "erstatningsbeløpene som kan kreves etter inkassoloven § 18 eller forskrift gitt med
     hjemmel i inkassoloven § 19" (new § 56 nr. 1). **LAW**
- Everything in §§2.4–2.7 below that rests on the current inkassolov (INKL) and
  inkassoforskrift (INKF) is current law with a known end.

### 2.2 Late interest (forsinkelsesrenteloven, FRL)

- **Start — § 2.** "Renten løper fra forfallsdag når denne er fastsatt i forveien, og
  ellers fra 30 dager etter at fordringshaveren har sendt skyldneren skriftlig påkrav med
  oppfordring om å betale." An invoice's due date is fixed in advance, so interest runs
  from it with no reminder. Without an agreed date, the demand may be electronic only
  "dersom skyldneren uttrykkelig har godtatt dette". No interest where the creditor caused
  the delay (§ 2 second paragraph). **LAW**
- **UNCERTAIN:** whether the due date itself or the day after is the first interest day.
  The text says "løper fra forfallsdag"; calculators commonly start the day after.
- **Rate — § 3.** Set each half-year as Norges Bank's policy rate on 1 January and 1 July
  "tillagt minst åtte prosentpoeng"; the guidelines aim at exactly 8 pp [RETN]. A change
  applies to existing claims from its effective date ("også for krav hvor
  fordringshaveren har krav på forsinkelsesrente før ikrafttredelsen"), so **a calculation
  over 1 January or 1 July must split at the boundary**. Delegated to Finanstilsynet from
  2025-06-18 [DELEG]. A lower consumer rate is permitted but **none has been set**: one
  rate for B2B and B2C. **LAW**

| Effective from | Rate p.a. | § 3a compensation | Regulation | Kunngjort |
|---|---|---|---|---|
| 2023-07-01 | 11.75 % | 460 NOK | FOR-2023-06-22-1075 | 23.06.2023 |
| 2024-01-01 | 12.50 % | 470 NOK | FOR-2023-12-14-2043 | 15.12.2023 |
| 2024-07-01 | 12.50 % | 460 NOK | FOR-2024-06-26-1320 | 27.06.2024 |
| 2025-01-01 | 12.50 % | 470 NOK | FOR-2024-12-19-3279 | 19.12.2024 |
| 2025-07-01 | 12.25 % | 460 NOK | FOR-2025-06-23-1321 | 26.06.2025 |
| 2026-01-01 | 12.00 % | 460 NOK | FOR-2025-12-18-2658 | 18.12.2025 |
| **2026-07-01** | **12.25 %** | **430 NOK** | **FOR-2026-06-25-1372** | 26.06.2026 |

- All read on Lovdata. Each regulation repeals the previous one. The footnote under FRL
  §§ 3/3a cites the 2026-01-01 regulation as "nr. 2568"; the regulation is **nr. 2658**,
  and 2568 returns nothing — a Lovdata footnote typo. The next value is due by
  **2027-01-01**, typically kunngjort in the second half of December.
- The implied policy rates (4.50 % to 2025 H1, 4.25 %, 4.00 %, 4.25 %) are derived;
  Norges Bank's decisions were not read. **UNCERTAIN**
- **Simple interest.** "En fast prosent årlig rente"; FRL provides no compounding. **LAW
  (by absence)**. The day-count convention (actual/365 is common practice) and interest
  on interest are **UNCERTAIN**. Whether interest runs on the purregebyr or the § 3a
  compensation is **UNCERTAIN**: they are separate claims due on demand (Prop. 150 L
  ch. 7, jf. gjeldsbrevloven § 5). Fees and interest must be **shown separately from the
  principal**, never folded into it [SOM; Finanstilsynet's 2020 letter quoted in
  Prop. 3 L 12.5.5]. **NEMND / ADMIN**
- **Consumers (§ 4).** Interest "kan lempes" on reasonable grounds (a); no fees or averaged
  loss items beyond inkassoloven §§ 17–20 (b); §§ 2, 3 and 4 are mandatory in the
  consumer's favour (c), so a higher agreed rate is not allowed outside the "løpende
  rente" case of § 3 second paragraph; **§§ 2 a, 2 b, 3 a and 4 a do not apply** (d). **LAW**
- **Businesses.** § 1 yields to agreement, so B2B parties may agree another rate; but a
  term excluding late interest is "alltid … urimelig", and one excluding the § 3a
  compensation "presumeres … urimelig" (§ 4 a, avtaleloven § 36). A public-authority
  debtor cannot agree a rate below the statutory one (§ 2 b fourth paragraph c). **LAW**
- **B2B payment terms (§§ 2 a, 2 b).** At most 60 days unless expressly agreed; an
  acceptance procedure at most 30 days. Business to public authority: at most 30 days
  after receipt, 60 only if expressly agreed and objectively justified. A validation on
  invoice terms, not on reminders, and not consumer law (§ 4 d). **LAW**

### 2.3 The § 3a compensation

- "Når fordringshaveren kan kreve forsinkelsesrente etter § 2, kan fordringshaveren også
  kreve en kompensasjon av skyldneren for inndrivelseskostnader tilsvarende 40 euro." Set
  in NOK by regulation with the rate (second paragraph); Norges Bank recommends the figure
  from the average NOK/EUR rate for November (H1) and May (H2) [RETN]. It cannot be
  contracted away (third paragraph). **LAW / ADMIN**
- **Amount:** EUR 40 in NOK, not a share of the inkassosats — **430 NOK from 2026-07-01**
  (table in §2.2).
- **From the due date, no reminder needed:** "Det er ikkje naudsynt med ei særskild
  betalingsoppfordring utover dette, til dømes purring" (Prop. 150 L ch. 12). It falls
  due "ved påkrav" unless the contract sets a date (ch. 7.5). **PREP**
- **B2B only** (FRL § 4 d). **LAW**
- **Offsets the reminder fees.** If the creditor claims the compensation, it is deducted
  from the debtor's liability for out-of-court collection costs (INKF § 2-6 first
  paragraph); if inkasso costs are claimed first, they are deducted from the compensation
  (second paragraph). § 1-5 applies § 2-6 to the chapter 1 fees (purring, inkassovarsel,
  betalingsoppfordring). In net terms the creditor recovers the larger of the two, not
  the sum. **LAW (reading of §§ 1-5, 2-6)**
- **UNCERTAIN:** per invoice or per claim bundle (commonly per invoice, **PRACTICE**); and
  which half-year's NOK figure applies — the one in force on the due date or on the date
  claimed.

### 2.4 The inkassosats and the fees derived from it

- The inkassosats is the base for every standardised fee and cap in INKF ("Inkassosatsen
  er kr 750", §§ 1-1 third paragraph, 2-1 first paragraph). It is set by the King in
  Council as an amendment to INKF, on the Justis- og beredskapsdepartementet's proposal —
  not by Finanstilsynet, which publishes it [FT-IS]. It changes irregularly: **700 from
  2019-01-01 to 2025-12-31, 750 from 2026-01-01**. **LAW**
- **Fees — INKF § 1-2:**

| Letter | Condition | Fee |
|---|---|---|
| Written **purring** | sent **at the earliest 14 days after the due date**; states the claim's amount and what it concerns | **1/20 × inkassosats** |
| **Inkassovarsel** (INKL § 9) | sent **at the earliest 14 days after the due date** | **1/20 × inkassosats** |
| **Betalingsoppfordring** sent by the creditor | meets INKL § 10; sent after the debtor missed a **≥14-day deadline** set in a purring or inkassovarsel | **3/20 × inkassosats** |

- "Ved beregningen av erstatningen anvendes inkassosatsen på det tidspunktet varselet
  sendes. **Beløpet avrundes til nærmeste krone.**" (fourth paragraph; the rounding
  sentence added from 2026-01-01 [IS2026]). 750/20 = 37.50 → **38 NOK**; 3 × 750/20 =
  112.50 → **113 NOK**; Finanstilsynet confirms 38 and 113 [FT-IS, FT-OV], so .50 rounds
  up. **LAW / ADMIN**
- The fractions are 1/20 and 3/20 since **2020-10-01** for claims falling due after that
  date [IF2020]; before, 1/10 and 3/10. FinKN 2023-845 (27.11.2023) still says "en
  tidel" — a misstatement; the forskrift controls. A "1/10" figure, as in the law study's
  brief, is the pre-2020 value.

| Effective from | Inkassosats | Purring / inkassovarsel (1/20, rounded) | Own betalingsoppfordring (3/20, rounded) | Source |
|---|---|---|---|---|
| 2019-01-01 | 700 | 35 | 105 | FOR-2018-12-20-2050 (+ FOR-2020-06-19-1248 fractions from 2020-10-01) |
| **2026-01-01** | **750** | **38** | **113** | FOR-2025-12-19-2709; FT-IS |

- Store the sats and compute the fees. Before 2026 the forskrift had no rounding sentence;
  1/20 of 700 is exactly 35, so it made no difference.
- **Chapter 2 caps** (the most a debtor pays in total out-of-court costs): § 2-2 (0.25–7.2
  × sats by claim band; consumer) and § 2-3 (double, if the betalingsoppfordring deadline
  was missed by more than 28 days), both ×1.5 for non-consumers (§ 2-1), plus VAT where
  over half the claim is from a non-VAT-liable business. They use the sats **when the
  principal is paid**, are **not rounded** [FT-IS], and band on principal plus pre-due
  interest. Relevant only if Vantigo shows a debtor the maximum exposure or collects up to
  the cap under § 1-1 second paragraph. **LAW**

### 2.5 The 14-day rules, the two-fee limit, the six-month reset

- **Scope.** INKL covers "inndriving av forfalte pengekrav", including a creditor's own
  claims (§ 8). INKF chapter 1 makes **no consumer/business distinction** for purring,
  inkassovarsel and betalingsoppfordring: a business can be charged the purregebyr. The
  1.5× uplift applies only to the chapter 2 caps and to § 1-4. INKL § 3: §§ 9–11, § 17
  first paragraph and § 19 may be varied by agreement, but not to a consumer's detriment,
  so B2B terms may vary the regime. **LAW**
- **No fee-bearing letter before due date + 14 days** (§ 1-2). A letter sent earlier is
  allowed but carries no fee. **LAW (reading)**
- **Two fees at most — § 1-3.** For one claim: two purringer + one betalingsoppfordring,
  **or** one purring + one inkassovarsel + one betalingsoppfordring. **LAW**
- **The second fee** (a second purring, or an inkassovarsel after a purring) only if the
  debtor **missed a payment deadline of at least 14 days set in the first purring**. A
  payment counts as on time if the order was handed to post or bank before the deadline
  (§ 1-2 third paragraph, applied by § 1-3). **LAW**
- **Six-month reset.** After six months since the last fee-bearing letter, fees for new
  letters may be charged again (§ 1-3 second paragraph). **LAW**
- Upshot: at most two 1/20 fees before a betalingsoppfordring. Whether one letter that is
  both purring and inkassovarsel counts once is **UNCERTAIN**.
- **Disputed claims.** Costs, fees included, "kan ikke kreves erstattet dersom skyldneren
  hadde innsigelser som det var rimelig grunn til å få vurdert før inndrivingen ble satt i
  verk" (INKL § 17 second paragraph). No costs if the creditor breached god inkassoskikk
  (§ 17 fourth paragraph). The new § 18 tightens this to any objection not obviously
  groundless. **LAW**
- **Electronic delivery.** INKL § 3 a allows electronic notices sent "på en betryggende
  måte"; no forskrift defines it. The new law keeps the rule (§ 15). No consent
  requirement for reminders; contrast FRL § 2, where an interest-starting påkrav needs the
  debtor's express acceptance to be electronic. FinKN accepted an eFaktura inkassovarsel
  (2023-845) and rejected an SMS invoice sent without agreement as not validly delivered
  (2017-492). Whether plain e-mail is always "betryggende" is **UNCERTAIN**. **LAW / NEMND**

### 2.6 The inkassovarsel and the betalingsoppfordring

- **INKL § 9.** Before an inkassator may start, the creditor **or** the inkassator must,
  **after the due date**, have sent a **written** notice that inkasso will be started,
  with a payment deadline **at least 14 days from sending** that has passed unpaid. Payment
  is on time "dersom betalingsoppdraget er mottatt av bank innen fristens utløp". **LAW**
- Nemnd glosses:
  - it must be "klart og utvetydig" and not misleading [FINKN-2025-240];
  - it must not suggest non-payment necessarily brings costs, which "tilslører" the right
    to avoid costs by objecting in time (§ 17 second paragraph) [FINKN-2025-240];
  - a heading like "BETALINGSVARSEL" without a clear statement that the claim goes to
    inkasso is not a § 9 varsel, and fees taken on it must be refunded [SOM];
  - a payment on the last day of a deadline is on time; acting before that day breaches
    god inkassoskikk [FINKN-2025-240];
  - a varsel requires a **due** claim; an invoice not validly delivered does not fall due
    [FINKN-SMS];
  - a purring is **not required** before an inkassovarsel ("Purring er ikke omtalt i
    inkassoloven") [FINKN-2023-845]. **NEMND**
- Costs and the right to object are mandatory content only in the § 10
  betalingsoppfordring and the **new** § 20; under current law, stating costs in a varsel
  is optional but must not mislead. If the debtor pays within the varsel deadline, no
  inkassator fee can be claimed (§ 17 third paragraph). **LAW**
- **Betalingsoppfordring — INKL § 10 (and § 19 b, INKF § 1-2 third paragraph).** Sent by
  the inkassator or by the creditor itself (fee 3/20) after the varsel deadline; a
  deadline of **at least 14 days** to pay or object; doubts about the claim assessed first.
  It must state (a) the creditor's name, (b) what the claim concerns, (c) the amount with
  **principal and additional claims shown separately**, (d) **the interest rate and the
  date interest runs from**, (e) that non-payment can bring more costs and legal
  collection, (f) the right to nemnd (byrå only). Notice of legal collection may come in a
  later notice with its own ≥14-day deadline. **LAW**
- **Under the new law** only inkassoforetak send the varsel (§ 20) and the
  betalingsoppfordring (§ 21). The new § 20 adds the label "inkassovarsel", invoice number
  and date, the original creditor if assigned, estimated costs, and information on
  payment difficulties, objections, complaints and nemnd (byrå only); its fourth paragraph
  applies domstolloven § 148 first paragraph (the start day is not counted), not § 149.
  **LAW (future)**

| Field | Purring with fee (INKF § 1-2) | Inkassovarsel (INKL § 9) | Own betalingsoppfordring (INKL § 10) |
|---|---|---|---|
| Amount of the claim | **required** | (deadline amount; must not mislead) | required, principal and additions **separately** |
| What the claim concerns (invoice reference) | **required** | — (practice) | required |
| Payment deadline | ≥14 days if a second fee is to follow (§ 1-3) | **required, ≥14 days from sending** | **required, ≥14 days** |
| "Inkasso will be started" | — | **required, clear and unambiguous** | — (legal collection, § 10 e) |
| Interest rate and from-date | — | — | **required** (§ 10 d) |
| Creditor name | — | — | required (§ 10 a) |
| KID | not required by law (**PRACTICE**) | not required | not required |

### 2.7 Due dates, payment, partial payments

- **Weekend or holiday due dates.** No statutory rule moves a contractual due date;
  domstolloven § 149 extends procedural deadlines only, and whether it applies by analogy
  is **UNCERTAIN**. Finansavtaleloven (FAL) § 4-5 second paragraph: an order on a
  non-business day is received the next business day, so for a consumer paying on a
  Saturday due date the deadline is "avbrutt" only on Monday (§ 2-2 (2)(a)). A lenient
  "next business day" rule is the safe reading. **LAW / UNCERTAIN**
- **When a payment is made.** FAL § 2-2 (1): when credited to the payee's provider. § 2-2
  (2)(a): a consumer's deadline is interrupted when the payer's bank has the order. For
  non-consumers chapter 2 yields to agreement or custom (§ 1-9 (2)). INKL § 9 (order
  received by the bank) and INKF §§ 1-2, 1-3 (handed to post or bank) set their own
  timeliness tests. The creditor sees the booking date; a run should allow a grace period
  before treating a deadline as missed. **LAW**
- **Partial payments.** No statutory allocation order for ordinary invoices. FAL § 2-9 (3)
  (earliest instalment first unless the debtor designates) applies to credit agreements,
  and whether an ordinary invoice term is a "kredittavtale" (§ 1-7 includes
  "betalingsutsettelse") is **UNCERTAIN**. The working group's rule (principal first) was
  **not adopted** (Prop. 3 L ch. 18). The new § 16 second paragraph lets the debtor learn
  the dekningsrekkefølge applied, so Vantigo's order must be recorded and explainable. The
  customary costs → interest → principal order is **PRACTICE**, not law. A partial payment
  on a receivable with VAT and non-VAT parts reduces both proportionally (Av 26/83 in
  MVAH ch. 4). **ADMIN**

### 2.8 Consumer specifics

- FRL § 4 b: beyond interest, a consumer may be charged only what INKL §§ 17–20 allow.
- FRL § 4 d: no § 3a compensation and no §§ 2 a/2 b/4 a rules against consumers.
- INKL § 18: a fee for the first bill only to the extent it could have been charged before
  the due date; FAL § 2-4 (1) caps a consumer invoice or payment fee at the payee's actual
  cost.
- INKL § 3: §§ 9–11, 17 first paragraph and 19 cannot be varied to a consumer's
  detriment; § 22 fourth paragraph: a consumer cannot waive the right to nemnd.
- The § 10 betalingsoppfordring applies to all debtors; what is consumer-specific is the
  cost limits.
- Fees charged too early: refund on an invalid varsel [SOM]; acting before the last day
  breaches god inkassoskikk [FINKN-2025-240]; a varsel on a claim not validly due breaches
  § 9 [FINKN-SMS]; a fee on a letter sent under 14 days after due date fails § 1-2's
  condition (clear as text, no FinKN decision read — **UNCERTAIN** as case law).
- New law (future): § 14 third paragraph requires reasonable adaptation where the creditor
  knows of serious illness, institutional stay or special payment difficulties; § 18
  fifth paragraph bars costs on claims incurred as a minor. Both bind every creditor.
  **LAW**

### 2.9 Bookkeeping and VAT of a reminder

- **VAT.** Merverdiavgiftsloven § 4-1 (2): outside the base are "b. lovbestemt inkasso- og
  purregebyr" and "c. forsinkelsesrente etter forsinkelsesrenteloven". **LAW**; MVAH ch. 4
  repeats it (Av 8/94). **ADMIN**. The § 3a compensation is not listed; as damages it is
  very likely outside scope, but no Skatteetaten statement was found — **UNCERTAIN**. A fee
  above the statutory amount, or a contractual fakturagebyr, needs its own VAT assessment
  (**UNCERTAIN**). An inkassobyrå invoices its creditor with VAT for its fee (MVAH ch. 3).
- **Is a reminder a salgsdokument?** Bokføringsforskriften § 5-1-1 covers documentation of
  sales of goods and services; a purregebyr, late interest and the § 3a compensation are
  not consideration for a supply. No primary statement says a reminder is "not a
  salgsdokument" — **UNCERTAIN** as a statement, an inference from § 5-1-1. Any booked
  amount must be documented (bokføringsloven § 10, amended from 2027-01-01 by lov 19 juni
  2026 nr. 39, text not read); the sent reminder or a system record of it is that
  documentation. Whether fee and interest income is recognised on issue or on payment is
  **UNCERTAIN** (many systems book on payment, **PRACTICE**). Either a dated claim record
  or an interest/fee note satisfies the law, provided it is documented and shown apart
  from the principal.
- **Bad-debt VAT relief.** FMVA § 4-7-1 first paragraph b: a receivable counts as finally
  lost if "ikke … innfridd seks måneder etter forfall, til tross for **minst tre
  purringskrav med normale purringsintervaller**"; alternative (a) is failed inkasso.
  **LAW via ADMIN** (MVAH ch. 4). Vantigo's reminder history is the evidence for it.
  Skatteklagenemnda accepted three bundled reminders in one case.

### 2.10 The hand-off to an inkassobyrå

- **What the byrå needs**, derived from what it must state or answer: today, the § 10
  content and proof of a valid § 9 varsel and its passed deadline; under the new law also
  the invoice number and date, the original creditor, and on request (§ 16) the case and
  invoice numbers, delivery time, principal, interest calculation, accrued costs, **part
  payments**, **dekningsrekkefølge**, the balance, and steps taken. **LAW**
- **Principal separate from fees and interest.** Finanstilsynet's 2020 letter (in
  Prop. 3 L 12.5.5) found creditor-added "gebyr og renter … har blitt inkludert i
  hovedstolen", causing double fees and wrong salær bands. **ADMIN (via PREP)**
- Implied minimum set (a reading, not a statutory list): debtor name, address, org.nr for
  a business, national ID only if lawfully held; consumer/business flag; invoice number,
  invoice date, due date, delivery date or period, description; principal outstanding;
  part payments with dates; interest rate(s), from-date and amount; each reminder sent
  (type, date, deadline, fee); the varsel date, deadline and content or a copy; any
  objection; a copy of the invoice.
- **No industry-standard hand-off format was found.** Kredinor pulls invoices hourly by API
  from PowerOffice GO and offers its own "InkassoAPI" and SFTP (**PRACTICE**). Intrum,
  Sergel, Lowell and PRA formats were not confirmed. **UNCERTAIN**
- **After hand-off** the creditor still owns the receivable (INKL § 2; new § 5); the debtor
  may pay the byrå with discharging effect, objections to the byrå count as raised to the
  creditor, and the byrå's payment plans bind the creditor (§ 13; new § 27). The byrå pays
  out "snarest" (§ 16; new § 26 third paragraph: principal no later than 14 days after it
  reaches the client account) and gives a written statement (§ 15; new § 26 sixth
  paragraph). **LAW**. Reading: the receivable stays open, remittances arrive from the
  byrå (net of its fee where agreed), and direct payments to the creditor after hand-off
  must be reported to the byrå (**UNCERTAIN / PRACTICE**; no statutory duty found).

### 2.11 Rules a reminder run must enforce, and the seed values

| # | Rule | Source | B2B / B2C | Computed from |
|---|---|---|---|---|
| R1 | Late interest from the due date (agreed) or 30 days after a written demand | FRL § 2 | both | due date; demand date |
| R2 | Rate per day = the half-year rate in force that day; split at 1 Jan / 1 Jul, old claims too | FRL § 3 | both (no lower consumer rate set) | rates table |
| R3 | Simple interest on principal; fees and interest shown apart from principal | FRL § 3; SOM; FT 2020 | both | — (compounding **UNCERTAIN**) |
| R4 | B2B agreed rate allowed; B2C cannot exceed the statutory rate | FRL §§ 1, 4 c | B2B choice / B2C fixed | customer terms |
| R5 | § 3a compensation (EUR 40 in NOK) from the due date without a reminder | FRL § 3a; Prop. 150 L | **B2B only** | compensation table |
| R6 | § 3a and inkasso costs/fees offset; they do not stack | INKF §§ 1-5, 2-6 | B2B | amounts claimed |
| R7 | No fee-bearing purring or inkassovarsel before due date + 14 days | INKF § 1-2 | both | due date |
| R8 | Fee = round(sats/20) per purring/varsel; sats on the send date; .50 rounds up | INKF § 1-2; FT-IS | both | inkassosats table |
| R9 | At most 2 × 1/20 fees per claim, then one 3/20 betalingsoppfordring | INKF § 1-3 | both | letter history |
| R10 | A second 1/20 fee only if a ≥14-day deadline in the first purring was missed | INKF § 1-3 | both | first purring deadline |
| R11 | Fee counter resets 6 months after the last fee-bearing letter | INKF § 1-3 | both | last fee letter date |
| R12 | Inkassovarsel only after due date; written; says clearly that inkasso follows; deadline ≥14 days from sending | INKL § 9; SOM; FinKN | both (B2B variable by contract) | send date |
| R13 | Purring with fee states the amount and what the claim concerns | INKF § 1-2 | both | invoice |
| R14 | Own betalingsoppfordring (3/20) only after a missed ≥14-day deadline; § 10 content; ≥14-day deadline | INKF § 1-2; INKL § 10 | both | prior deadline |
| R15 | On time if the order reached the bank (varsel) or post/bank (fees) by the deadline; last day counts | INKL § 9; INKF § 1-2; FinKN 2025-240 | both | payer's order date (availability **UNCERTAIN**, see §3.2) |
| R16 | No fees or costs on a claim with a reasonable objection; stop the run on dispute | INKL § 17; new § 18 | both | dispute flag |
| R17 | Consumer: no fees beyond INKL §§ 17–20; no § 3a; first-bill fee ≤ actual cost | FRL § 4; INKL § 18; FAL § 2-4 | **B2C** | consumer flag |
| R18 | Electronic reminders if "betryggende"; an interest-starting påkrav by e-mail needs express consent | INKL § 3 a; FRL § 2 | both | channel; consent |
| R19 | B2B payment term ≤60 days unless expressly agreed; public debtor ≤30 days | FRL §§ 2 a, 2 b | B2B / B2G | invoice terms |
| R20 | **Future (date not set):** no statutory inkassovarsel by the creditor, no fee for its varsel or betalingsoppfordring; egeninkasso fees per a new forskrift | NYINKL §§ 2, 19, 20; Prop. 3 L 13.4.4 | both | in-force date (**UNCERTAIN**) |

**Late interest and § 3a compensation** (Lovdata):

| valid_from | valid_to | late_interest_pct | b2b_compensation_nok | regulation |
|---|---|---|---|---|
| 2023-07-01 | 2023-12-31 | 11.75 | 460 | FOR-2023-06-22-1075 (optional, for invoices overdue since 2023) |
| 2024-01-01 | 2024-06-30 | 12.50 | 470 | FOR-2023-12-14-2043 |
| 2024-07-01 | 2024-12-31 | 12.50 | 460 | FOR-2024-06-26-1320 |
| 2025-01-01 | 2025-06-30 | 12.50 | 470 | FOR-2024-12-19-3279 |
| 2025-07-01 | 2025-12-31 | 12.25 | 460 | FOR-2025-06-23-1321 |
| 2026-01-01 | 2026-06-30 | 12.00 | 460 | FOR-2025-12-18-2658 |
| 2026-07-01 | 2026-12-31 | 12.25 | 430 | FOR-2026-06-25-1372 |

**Inkassosats:**

| valid_from | valid_to | inkassosats_nok | purring/varsel fee | own betalingsoppfordring | regulation |
|---|---|---|---|---|---|
| 2019-01-01 | 2025-12-31 | 700 | 35 | 105 | FOR-2018-12-20-2050; fractions FOR-2020-06-19-1248 (claims due after 2020-10-01) |
| 2026-01-01 | — | 750 | 38 | 113 | FOR-2025-12-19-2709 |

## 3. Bank payment files

"Primary" here is the format's issuer or the bank; vendor help pages, blogs and
third-party code are secondary. Several primary documents are older than two years (the
OCR spec v4.0, 2018; the MPS manual, 2021; the MPS camt guide, March 2021; Nordea
eGateway MIG, 2020) — they are the current published versions.

### 3.1 OCR giro

- **Still offered in 2026.** DNB retires Cremul/eGiro EDIFACT on **30 June 2026** and
  moves all account-information and incoming-payment files to ISO 20022 XML, but "OCR-
  formatet fra Mastercard Payments Services vil fortsatt være tilgjengelig, men gir ikke
  fullverdig automatisk oppdatering" (DNB ERP newsletter, 2025). No OCR end date was
  found (**UNCERTAIN** beyond DNB's statement). Nordea and SpareBank 1 offer OCR (Nordea
  suggests it above 50 invoices a month; SpareBank 1 above 10 payments a month, data kept
  3 months).
- **Only valid-KID payments.** An OCR file holds payments with a valid KID (and, by
  option, card information transactions). Wrong or missing KIDs come as paper/PDF "melding
  om kreditering" (L710) or in an eGiro/camt.054 file. An OCR-only importer sees only
  unknown-but-valid KIDs, over- and underpayments and credit notes with KID among the
  exception cases.
- **Direkte remittering** (transaction type 12) is being retired: DNB says "sanert i alle
  norske banker med frist 31. oktober 2026"; Aritma and Vitec (secondary) say 31.12.2025.
  **UNCERTAIN**; only makes type 12 rare.

**Structure** (spec §2): fixed-width **80-character records**; alphanumeric fields left-
justified blank-filled, numeric right-justified zero-filled. One forsendelse holds one or
more oppdrag; record identity is the first four fields.

```
NY000010                     start transmission     (once)
  NY090020                   start assignment        (per assignment)
    NY09tt30                 amount item 1 (beløpspost 1)  } one transaction
    NY09tt31                 amount item 2 (beløpspost 2)  }
    NY09tt32                 amount item 3 — only tt = 20, 21
  NY090088                   end assignment
NY000089                     end transmission       (once)
```

Start transmission `NY000010`:

| Pos | Len | Field | Content |
|---|---|---|---|
| 1-2 | 2 | Formatkode | `NY` |
| 3-4 | 2 | Tjenestekode | `00` |
| 5-6 | 2 | Forsendelsestype | `00` |
| 7-8 | 2 | Recordtype | `10` |
| 9-16 | 8 | Dataavsender | MPS id, always `00008080` |
| 17-23 | 7 | **Forsendelsesnummer** | serial number generated by MPS |
| 24-31 | 8 | Datamottaker | recipient's kundeenhet-ID (DNB online bank OCR = 118125) |
| 32-80 | 49 | Filler | zeros |

Start assignment `NY090020`:

| Pos | Len | Field | Content |
|---|---|---|---|
| 1-8 | 8 | `NY090020` | service 09 = OCR giro, oppdragstype 00, record 20 |
| 9-17 | 9 | **Avtale-ID** | agreement id for the account, assigned by MPS |
| 18-24 | 7 | Oppdragsnummer | serial per assignment in the transmission |
| 25-35 | 11 | **Oppdragskonto** | payee's bank account, 11-digit BBAN |
| 36-80 | 45 | Filler | zeros |

Amount item 1 `NY09tt30`:

| Pos | Len | Field | Content |
|---|---|---|---|
| 1-4 | 4 | `NY09` | |
| 5-6 | 2 | **Transaksjonstype** | see below |
| 7-8 | 2 | Recordtype | `30` |
| 9-15 | 7 | **Transaksjonsnummer** | serial per transaction within the assignment (links items 1/2/3) |
| 16-21 | 6 | **Oppgjørsdato** | settlement date, DDMMYY |
| 22-23 | 2 | Sentral-ID | first two digits of the bank data-centre number |
| 24-25 | 2 | Dagkode | day of month processed, 01-31 |
| 26 | 1 | Delavregningsnummer | partial-settlement number (0 for types 18-21) |
| 27-31 | 5 | Løpenummer | identifies the **bank-statement lump sum**, not the transaction |
| 32 | 1 | **Fortegn** | `-` for a negative amount (credit note with KID) for payees who accept them; else `0` |
| 33-49 | 17 | **Beløp** | amount in **øre** |
| 50-74 | 25 | **KID** | right-justified, blank-padded, incl. check digit; "Bokstaver kan ikke benyttes"; **blank for types 20/21** |
| 75-76 | 2 | Filler/kortutsteder | card issuer for types 18-21, else zeros |
| 77-80 | 4 | Filler | zeros |

Amount item 2 `NY09tt31`:

| Pos | Len | Field | Content |
|---|---|---|---|
| 1-8 | 8 | `NY09tt31` | |
| 9-15 | 7 | Transaksjonsnummer | same as item 1 |
| 16-25 | 10 | Blankettnummer | giro form number; `979` + last 7 of archive ref (Postbanken); else zeros |
| 26-34 | 9 | **Avtale-ID / Arkivreferanse** | type 12: payer's MPS agreement id; types 11, 13: archive reference from input; form-based: MPS archive reference; 18-21: BAX number + session |
| 35-41 | 7 | Filler | zeros |
| 42-47 | 6 | **Oppdragsdato** | date the order was delivered to the bank, DDMMYY |
| 48-58 | 11 | **Debetkonto** | payer's account when known, else zeros; always zeros for 18-21 |
| 59-80 | 22 | Filler | zeros |

- Item 3 `NY09tt32` (only tt = 20, 21): pos 9-15 transaction number, **16-55 free text**
  (40), 56-80 filler.
- End assignment `NY090088`: 9-16 number of transactions; 17-24 number of records
  **including** the assignment's start and end; 25-41 sum (øre) of item-1 amounts; 42-47
  date generated; 48-53 first and 54-59 last oppgjørsdato; 60-80 filler.
- End transmission `NY000089`: 9-16 transactions; 17-24 records **including** all start
  and end records; 25-41 sum (øre); 42-47 date generated; 48-80 filler.
- Sort order: ascending by oppgjørsdato.

**Transaction types** (pos 5-6): 10 giro debited to account; 11 standing order; 12
direkte remittering; 13 BTG (Bedrifts Terminal Giro); 14 SkrankeGiro; 15 AvtaleGiro; 16
TeleGiro; 17 giro paid in cash; 18 reversal with KID; 19 purchase with KID; 20 reversal
with free text (has item 3); 21 purchase with free text (has item 3). Types 18-21 are card
terminal/online information transactions needing a separate agreement; Vantigo has no
terminal and can reject or ignore them. Which online-bank channel maps to 10, 13 or 16 is
**UNCERTAIN** (immaterial to matching).

**Reversals and negatives.** Ordinary giro (10-17) has no reversal record; a correction
appears only on the statement (**UNCERTAIN** how banks report a recalled giro). Reversal
amounts of types 18/20 are **added** to the control sum ("Skal ikke trekkes fra"); whether
their Fortegn is `-` is **UNCERTAIN**. A credit note with KID carries Fortegn `-`, and the
control sum is the **net** (the spec's §3 example: 20 positive and 3 negative amounts,
sum 1 563 000). Vantigo issues credit notes **without** KID (P2 §6.4), so a negative KID
line is an exception.

**Example** (constructed; every line 80 characters, totals consistent). Agreement
001234567 on account 12345678903, KID length 7 MOD10 (invoice 1001 → `0010017`, 1002 →
`0010025`), two payments of NOK 1 250,00 and NOK 499,50 settled 06.10.26:

```
NY000010000080800000123000123450000000000000000000000000000000000000000000000000
NY090020001234567000000112345678903000000000000000000000000000000000000000000000
NY09103000000010610260106100001000000000000125000                  0010017000000
NY091031000000100000000001234567890000000051026987654321090000000000000000000000
NY09163000000020610260106100001000000000000049950                  0010025000000
NY091631000000200000000002345678900000000061026123405678940000000000000000000000
NY090088000000020000000600000000000174950061026061026061026000000000000000000000
NY000089000000020000000800000000000174950061026000000000000000000000000000000000
```

The spec's own §3 example has its KID padding collapsed by the PDF and cannot be used
verbatim as a fixture. The spec's MOD10 example `12345678 → 2` reproduces.

**Checks an importer runs:** every record exactly 80 characters after stripping CR/LF,
starting `NY`; grammar `10 (20 (30 31 [32])+ 88)+ 89`, items 2/3 sharing item 1's
transaction number and type, item 3 iff type 20 or 21; per assignment, the transaction
count, record count (incl. 20 and 88), signed amount sum and min/max oppgjørsdato equal
the `88` fields; per transmission, the same against `89`; service 09 (skip other
services, an open choice); `Oppdragskonto` is a seller account with a KID agreement
(optionally the Avtale-ID too); KID digits, length and check digit per the agreement;
DDMMYY with a century window (the spec has no century); file-level dedupe (§3.7).
Character encoding is **UNCERTAIN** (ISO-8859-1 likely; decode item-3 text leniently).

**Delivery** (manual §3.1): files at 08:00, 12:30, 15:00 and 17:30 for up to four
settlements a day; held for retrieval **25 working days**, copies kept **90 working days**
and re-orderable; payments over NOK 25 million settle as their own lump sum.

### 3.2 camt.054 for incoming payments

- **camt.054 carries all incoming payments**, with and without KID: MPS "Innbetaling
  Total", DNB "Total payment" (with/without KID plus bank-internal; ERP only) and "eGiro"
  (with/without KID, no internal transfers; retrievable in the online bank). With both
  OCR and Innbetaling Total agreed, "KID transaksjoner vil leveres i både OCRgiro fil og
  Innbetaling Total fil" (MPS manual §9).
- **Versions.** Norwegian banks deliver **camt.054.001.02** (ISO 2009) — DNB MIG v2.0
  (files dated 2026-03-10), MPS guide (March 2021), Nordea CAAR v1.9 (2022), Nordea
  eGateway v1.5 (2020-11-30), a Danske Bank Norway example (2023-07-31).
  **camt.054.001.08** (Bits MIG 2.0, in force **19 Nov 2023**; "For bankens kunder er det
  ikke påkrevet å gå over til den nye versjonen") is supported by DNB alongside .02 with
  "ikke satt noen sluttdato"; DNB's 2019 camt MIGs are not yet published, and the Bits
  portal needs a login (not read).
- Accept both namespaces (`urn:iso:std:iso:20022:tech:xsd:camt.054.001.02` and `…001.08`)
  and reject others. Differences that matter:

| Item | .02 | .08 |
|---|---|---|
| Entry status | `Ntry/Sts` = `BOOK` | `Ntry/Sts/Cd` = `BOOK` |
| Debtor name | `RltdPties/Dbtr/Nm` | `RltdPties/Dbtr/Pty/Nm` |
| Tx amount | `TxDtls/AmtDtls/TxAmt/Amt` | also `TxDtls/Amt` + `TxDtls/CdtDbtInd` |
| KID | `RmtInf/Strd/CdtrRefInf/{Tp/CdOrPrtry/Cd, Ref}` | same |

**The KID path:** `Ntry/NtryDtls/TxDtls/RmtInf/Strd/CdtrRefInf/Ref` with
`CdtrRefInf/Tp/CdOrPrtry/Cd = SCOR` (no `Issr`; `Issr = ISO` means an ISO 11649 RF
reference). Not proprietary: MPS ("SCOR = KID", Ref "25 characters max"), DNB ("In
Norway: Used for … KID, ISO11649"), Nordea and Danske agree. For Norway a payment carries
**structured or unstructured remittance, not both** (DNB MIG; MPS guide 2.214).

| Need | Path | Notes |
|---|---|---|
| File id | `GrpHdr/MsgId`, `GrpHdr/CreDtTm` | MsgId unique per recipient: MPS "Unique reference for each camt.054 message"; Nordea ≥90 days (CAAR) / one year (eGateway); DNB "pre-agreed period" |
| Notification | `Ntfctn/Id`, `Ntfctn/CreDtTm`, `Ntfctn/CpyDplctInd` | MPS: `COPY` for a re-ordered message |
| Account | `Ntfctn/Acct/Id/IBAN` or `…/Othr/Id` (+`SchmeNm/Cd=BBAN`), `Acct/Ccy` | IBAN or BBAN per agreement |
| Entry (lump sum) | `Ntry/NtryRef`, `Ntry/Amt@Ccy`, `Ntry/CdtDbtInd`, `Ntry/RvslInd`, `Ntry/Sts`, `Ntry/BookgDt/Dt`, `Ntry/ValDt/Dt`, `Ntry/AcctSvcrRef` | NtryRef unique **only within one notification**; MPS: BookgDt = settlement date, ValDt always the same; Entry Amt = the statement amount |
| Entry type | `Ntry/BkTxCd/Domn/{Cd,Fmly/Cd,Fmly/SubFmlyCd}`, `Ntry/BkTxCd/Prtry/{Cd,Issr}` | MPS/DNB: **230 KID**, 232 Autogiro, 233 with/without message, 234 standard giro, 240 structured (KID, invoice and credit note mixed); `Issr = NETS`. Nordea KID: `PMNT/RCDT/VCOM`. Danske: `PMNT/NTAV/NTAV` + `Prtry/Cd = "NO230 …"`, `Issr = DBA` |
| Batch | `Ntry/NtryDtls/Btch/NbOfTxs` | one entry = one lump sum with many `TxDtls` |
| Transaction | `Ntry/NtryDtls/TxDtls` (1..n) | |
| Tx refs | `TxDtls/Refs/AcctSvcrRef`, `…/EndToEndId`, `…/InstrId`, `…/TxId` | MPS: AcctSvcrRef = NICS archive reference; Nordea CAAR "always reported"; Nordea eGateway "No bank reference will be provided from Norway"; Danske: absent. EndToEndId often `NOTPROVIDED` |
| Tx amount | `TxDtls/AmtDtls/TxAmt/Amt@Ccy` | always present; may be 0.00 |
| Debtor | `TxDtls/RltdPties/Dbtr/Nm`, `…/DbtrAcct/Id/{IBAN \| Othr/Id}` | "reported where available" |
| **KID** | `TxDtls/RmtInf/Strd/CdtrRefInf/Tp/CdOrPrtry/Cd = SCOR` and `…/CdtrRefInf/Ref` | |
| Remitted amount | `RmtInf/Strd/RfrdDocAmt/RmtdAmt` | |
| Invoice no. / credit note | `RmtInf/Strd/RfrdDocInf/{Tp/CdOrPrtry/Cd (CINV\|CREN), Nb}`, `RfrdDocAmt/CdtNoteAmt` | sumpost 240; Tripletex identifies refunds by "CREN" |
| Free text | `TxDtls/RmtInf/Ustrd` (0..n, Max140) | MPS: lines of 80, combined max 1750 |
| Settlement date | `TxDtls/RltdDts/IntrBkSttlmDt` / `AccptncDtTm` | optional |

- **An invalid KID in camt.** Sumpost 230 is "Payments with correct KID"; free text or no
  KID lands under 233/234. Whether a check-digit-invalid KID can appear in
  `CdtrRefInf/Ref` is **UNCERTAIN** (MPS rejects an unregistered length/modulus, but a
  brevgiro with a wrong KID still goes through, probably as 234). Re-validate every KID.
- **Reversals.** `RvslInd`: MPS "Not used"; Nordea Finland only; DNB: a DBIT with
  `RvslInd` true means the original was a credit, or a plain DBIT `PMNT/ICDT-RCDT/RRTN`
  "Tilbakeførsel". No field links a reversed KID payment to its original for Norway
  (**UNCERTAIN**); queue it.
- **Debits.** MPS Innbetaling Total carries debits too; credit-only agreements are common.
  Match only `CdtDbtInd = CRDT`; route or ignore DBIT (an open choice).

**Example** (camt.054.001.02, shape from the Danske Norway example and the MPS guide,
values invented; KID `0010017` = invoice 1001):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.054.001.02">
  <BkToCstmrDbtCdtNtfctn>
    <GrpHdr>
      <MsgId>NO20261006-000123</MsgId>
      <CreDtTm>2026-10-06T17:35:00+02:00</CreDtTm>
    </GrpHdr>
    <Ntfctn>
      <Id>20261006173500NOK8903</Id>
      <CreDtTm>2026-10-06T17:35:00+02:00</CreDtTm>
      <Acct>
        <Id><Othr><Id>12345678903</Id><SchmeNm><Cd>BBAN</Cd></SchmeNm></Othr></Id>
        <Ccy>NOK</Ccy>
      </Acct>
      <Ntry>
        <NtryRef>2610061-1-1</NtryRef>
        <Amt Ccy="NOK">1250.00</Amt>
        <CdtDbtInd>CRDT</CdtDbtInd>
        <Sts>BOOK</Sts>
        <BookgDt><Dt>2026-10-06</Dt></BookgDt>
        <ValDt><Dt>2026-10-06</Dt></ValDt>
        <AcctSvcrRef>90000001</AcctSvcrRef>
        <BkTxCd>
          <Domn><Cd>PMNT</Cd><Fmly><Cd>RCDT</Cd><SubFmlyCd>VCOM</SubFmlyCd></Fmly></Domn>
          <Prtry><Cd>230</Cd><Issr>NETS</Issr></Prtry>
        </BkTxCd>
        <NtryDtls>
          <Btch><NbOfTxs>1</NbOfTxs></Btch>
          <TxDtls>
            <Refs>
              <AcctSvcrRef>123456789</AcctSvcrRef>
              <EndToEndId>NOTPROVIDED</EndToEndId>
            </Refs>
            <AmtDtls><TxAmt><Amt Ccy="NOK">1250.00</Amt></TxAmt></AmtDtls>
            <RltdPties>
              <Dbtr><Nm>Kunde AS</Nm></Dbtr>
              <DbtrAcct><Id><Othr><Id>98765432109</Id></Othr></Id></DbtrAcct>
            </RltdPties>
            <RmtInf>
              <Strd>
                <RfrdDocAmt><RmtdAmt Ccy="NOK">1250.00</RmtdAmt></RfrdDocAmt>
                <CdtrRefInf>
                  <Tp><CdOrPrtry><Cd>SCOR</Cd></CdOrPrtry></Tp>
                  <Ref>0010017</Ref>
                </CdtrRefInf>
              </Strd>
            </RmtInf>
            <RltdDts><IntrBkSttlmDt>2026-10-06</IntrBkSttlmDt></RltdDts>
          </TxDtls>
        </NtryDtls>
      </Ntry>
    </Ntfctn>
  </BkToCstmrDbtCdtNtfctn>
</Document>
```

Real-bank quirks to test: Danske puts the same entry `AcctSvcrRef` (`" 90000000"`, with a
leading space) on two entries and has no transaction-level `AcctSvcrRef`, and its KID
example uses `PMNT/NTAV/NTAV` with `Prtry/Cd = "NO230 08012345678"`. **Key on the
presence of `SCOR`, not on BkTxCd values.** The verbatim Danske file is a good parser
fixture, but its redistribution licence is not stated (**UNCERTAIN**): write our own
fixtures or keep it out of the repo.

### 3.3 camt.053 is not enough

From 30 June 2026 DNB delivers account information only as camt.053. With a payment
agreement the statement shows **lump sums**: "you do not see the name of the sender on
the bank statement, only total entries. Information about who has deposited is located in
the file that you either retrieve in the online bank or directly in your ERP system" (DNB
ERP page). DNB's camt.053 MIG has `CdtrRefInf` and the 230/234/240 codes, but whether DNB
populates per-KID details for agreement lump sums is **UNCERTAIN**. KID matching needs
**camt.054 or OCR**; camt.053 is for bank reconciliation (the roadmap's later bank work).
camt.054 is sufficient for matching invoices; it does not reconcile non-customer
movements.

### 3.4 Getting the files

- **The agreement.** An OCR/KID agreement ("Fakturere med KID", "innbetalingsavtale"),
  registered with MPS stating KID length(s) and modulus (P2 §6.2). DNB: online bank under
  "Filoverføring – Fakturere med KID" or digitally from the ERP; Nordea: a signed form. For
  all incoming payments, an **eGiro / camt.054C** agreement. **Tvungen KID** is a separate
  option rejecting payments without or with invalid KID, except brevgiro (manual §6.5;
  active in 2-3 working days at DNB).
- **Manual download** fits phase 4 (bank APIs are later). DNB: OCR and eGiro files in the
  corporate online bank, re-orderable under "File transfer – Order files"; kundeenhet-ID
  for OCR to the online bank **118125**, for eGiro **041616**. Nordea: "hente fil i
  nettbanken". SpareBank 1: "Meny → Filer → Hent filer". Whether Nordea and SpareBank 1
  online banks offer camt.054 as a manual download is **UNCERTAIN**; Handelsbanken Norway
  was not researched.
- **Direct integration** (DNB Connect / FileGateway, or MPS delivery to the ERP's own
  kundeenhet-ID) is out of scope for phase 4.

| Bank | Agreement | Establishment | Monthly | Per transaction |
|---|---|---|---|---|
| DNB | Invoice with KID (OCR) | 300 | 90 | 2,25 per KID payment; OCR file in online bank 0,00; re-order 100 |
| DNB | Payment Agreement (ERP) / DNB Payment Pluss | 300 | 90 | 2,25 with KID; 2,50 without |
| Nordea | OCR | 600 | 90 | 2,50 per KID; list 701 A+B 100 + postage |
| Nordea | Cremul | 600 | 80 | 2,50 with KID; 2,75 without |

SpareBank 1 prices vary by bank and were not looked up (**UNCERTAIN**).

### 3.5 KID matching and the exception cases

A KID is per invoice by design (P2 §6.4; §5.2 below), so a valid known KID identifies one
invoice. The bank study's proposed handling:

| # | Case | Detection | Suggested handling |
|---|---|---|---|
| a | Wrong KID (fails check digit / wrong length) | `kid` check against the agreement | Queue: "invalid KID" (never in OCR; camt may carry it in `Ustrd`/`Strd`) |
| b | Unknown KID (valid digits, no invoice) | no issued invoice with that KID | Queue: "unknown KID" (PowerOffice blocks the whole file; a per-line queue is better). Also a KID from another agreement or a pre-Vantigo system |
| c | KID of a credited invoice | fully credited, open = 0 | Queue: "invoice credited" — usually a refund case |
| d | Exact payment | amount = open | Auto-post (source = import, linked to the bank line) |
| e | Underpayment | amount < open | Auto-post as a partial payment (Fiken and Tripletex do); a small-difference write-off is an open choice, not automatic |
| f | Overpayment | amount > open | Today's `payments.go` refuses it → **queue** ("overpaid by X"); actions as in Fiken: refund, keep as income, round off under a threshold, set off against the next invoice |
| g | Already paid | open = 0, not credited | Queue: "invoice already paid" (double payment) → refund |
| h | Two payments for one invoice | second line, same KID | The first posts; the second follows e/f/g on the remaining open amount |
| i | One payment for several invoices | no single KID; text or a sumpost 240 `RfrdDocInf` CINV list | Queue: "split" across invoices (1B left this out, `invoices.md:2124-2125`) |
| j | No KID, text only (`Ustrd`) | camt only | Queue with suggestions (invoice number in text, a known debtor account, amount = one invoice's open amount); suggest, never auto-post (open choice) |
| k | Negative KID line / credit note | OCR Fortegn `-`, camt `CREN`/`CdtNoteAmt` | Queue |
| l | Reversal | camt DBIT + `RvslInd`; OCR 18/20 | Queue; if tied to a posted import payment, offer to reverse it |
| m | Currency ≠ NOK, or an account without an agreement | header checks | Reject the file or notification before any posting |
| n | Non-customer credit (interest, own transfer, Vipps settlement) | camt only, no KID, BkTxCd | Queue → "not a customer payment" |

How other Norwegian products shape the queue (secondary, help pages): **Tripletex** splits
wrong/missing-KID payments into "Bilagsmottak" or auto-books them to a placeholder customer
under "Andre poster", with a setting "Innkommende betaling uten KID-nummer"; **Fiken**
enumerates "Betalte for mye" (refund as a negative payment, other income 3900, under
10 kr øreavrunding, set off), "Betalte samme faktura to ganger", partial and combined
payments, and a credit note deducted before payment; **DNB Regnskap** posts unmatched
credits to an interim account under "Innbetalt ingen match" and has rules that auto-route
recurring non-customer credits such as Vipps (a search engine attributed this page to
Fiken; it is DNB Regnskap); **PowerOffice Go** stops the whole OCR import on an unknown
KID (a counter-example); Visma eAccounting's pages were unreachable. The bank study's
proposal for Vantigo: queue rows `open → resolved`, resolutions `matched`, `split`,
`not_customer_payment`, `refund_due`, `reversed`, each keeping the raw bank line (KID or
text, amount, date, debtor name and account, file and line reference) and a reason code
from the table.

### 3.6 Dedupe keys and pre-posting checks

- **Same file twice.** OCR: (`Dataavsender` 9-16, `Forsendelsesnummer` 17-23,
  `Datamottaker` 24-31) of `NY000010`; whether a re-ordered copy keeps its
  forsendelsesnummer is **UNCERTAIN** (likely yes). camt: `GrpHdr/MsgId` + `CreDtTm`, and
  `Ntfctn/Id`; a re-order carries `CpyDplctInd = COPY` and probably a new MsgId, so
  file-level dedupe is not enough. Hash the bytes as a second guard in both.
- **Same transaction in two files** (both OCR and camt agreed, a COPY re-order,
  overlapping downloads). No cross-bank transaction id is guaranteed: the OCR archive
  reference is MPS's only for form/BTG/standing-order types (for type 12 it is the payer's
  agreement id), and the transaction number is per assignment; camt's
  `TxDtls/Refs/AcctSvcrRef` is absent at Nordea eGateway Norway and Danske, the entry
  `AcctSvcrRef` may repeat, and `NtryRef` is unique only per notification. Proposal (an
  open choice): a fingerprint of receiving account, booking/settlement date, amount in
  øre, KID or normalised text, debtor account if present, archive reference if present;
  an exact match is a duplicate, a match on all but the archive reference a "possible
  duplicate" for the queue. Whether OCR's archive reference equals camt's
  `TxDtls/AcctSvcrRef` for the same payment is **UNCERTAIN** (9 digits vs Max35Text). The
  simplest safe policy: **one import source per seller account** (OCR or camt), chosen in
  settings.
- **Pre-posting checks, all-or-nothing per file:** (1) format validity (camt against the
  XSD for its namespace; OCR grammar); (2) control totals (OCR end records; camt Σ
  `TxDtls` = `Ntry/Amt` per entry, `Btch/NbOfTxs` = count, `TxsSummry` when present); (3)
  the account is a seller account with a KID agreement (normalise a NO IBAN to BBAN); (4)
  every currency NOK; (5) only `BOOK`, only `CRDT` for matching; (6) sane dates; (7)
  re-validate each KID with package `kid`.

### 3.7 Parsing: hand-written; schemas and licence

| Package | Covers | Licence | Status |
|---|---|---|---|
| github.com/moov-io/iso20022 | generated ISO 20022 models incl. camt | Apache-2.0 | v0.2.1, Jan 2022; low activity |
| github.com/moov-io/fednow20022 `gen/camt_054_001_08` | camt.054.001.08 types | Apache-2.0 | Feb 2026 |
| github.com/mbanq/iso20022-go `ISO20022/camt_054_001_08` | camt.054.001.08 | not checked — **UNCERTAIN** | v0.1.7 Feb 2026 (search snippet) |
| github.com/anyfin/ocrline | fixed-width struct tags; **no OCR record model** | MIT | v0.3.0, 2026-03-24, 0 importers |
| python-netsgiro (not Go) | full OCR giro + AvtaleGiro parser/builder | Apache-2.0 | usable as a test oracle |

- Neither format needs a dependency: OCR is about eight fixed-width record shapes, and
  camt.054 about 30 element paths through `encoding/xml`. **Hand-written parsing is the
  normal choice.** `encoding/xml` does not validate against an XSD; `xmllint --schema` can
  be a test-time oracle, and runtime XSD validation would need cgo/libxml2 (an open
  choice, not recommended at runtime).
- **XSDs** from the ISO 20022 archive: camt.054.001.02
  (https://www.iso20022.org/message/12746/download, set V02 archived 1 March 2009),
  camt.054.001.08 (https://www.iso20022.org/message/12776/download, set V08 archived
  19 February 2019); camt.053 .02 and .08 at /12706 and /12736. Current is camt.054.001.14.
  The downloads returned a "SWIFT site off-line" page to curl; fetch them in a browser.
- **Licence.** The iso20022.org terms: material "intended to be used and reproduced freely
  by all interested users under the ISO 20022 Intellectual Property Right Policy";
  replications must say they are not the official site and point to it. The IPR policy
  grants "a non-exclusive, royalty-free license to use the published information".
  **Vendoring the two XSDs into `testdata/` is permitted** with a NOTICE line naming
  https://www.iso20022.org/. Whether the XSD files carry their own copyright header was
  not inspected — **UNCERTAIN**, check when vendoring.

## 4. Vipps MobilePay ePayment

Primary sources are Vipps MobilePay's developer pages (published as Markdown and indexed
in `llms.txt`) and its OpenAPI specs (ePayment spec `info.version: 1.8.5`). The per-API
GitHub repos return 404 and `vippsas/vipps-developers` was archived on 2026-02-16; the
docs site is canonical. The commercial pages (pricing, Betalingslenker, the help centre)
were read through a fetch summarizer.

### 4.1 Product choice

- **ePayment API** (`POST /epayment/v1/payments`) is the current one-time-payment API for
  Vipps (NO) and MobilePay (DK/FI): online, in person, one-time QR, personal QR and
  freestanding card. **This is the product to build on.**
- **eCom API v2** is legacy: "All new integrations: Use the ePayment API", Norway only.
  Its changelog says "There is currently no scheduled date for discontinuing the eCom
  API"; the reserve-capture page calls it "(Vipps only and deprecated)". Not for new work
  either way.
- **No invoice API exists.** The site index lists ePayment, Recurring, Login, QR, Order
  Management, Userinfo, Webhooks, Management, Report, Sales, Donations, PSP and legacy APIs.
  "Invoice" appears only in the long-living page (Finland's retired *MobilePay Invoice*),
  one example `paymentDescription`, and Vipps' own fee invoices in the Report API.
- **"Vipps eFaktura" is a consumer feature, not a merchant API.** A person who turns on
  "Se eFaktura i Vipps" sees the banks' eFaktura B2C invoices in the app. The issuer sends
  ordinary eFaktura B2C through the banks' network operated by Mastercard Payment Services,
  which needs an issuer agreement with the bank and qualification testing (secondary).
  It is paid as a bank bill, so it arrives **with a KID in OCR/camt.054**, not through
  ePayment. This is the roadmap's separate "eFaktura and AvtaleGiro for consumers"; phase
  4's KID matching already covers its money.
- **Betalingslenker** is a portal product (2.49% + 1 NOK) with **no API** in the developer
  docs and no stated link lifetime (**UNCERTAIN**). Not usable from Vantigo.
- **QR API merchant-redirect QR** is static, never times out, and points to a URL that can
  be changed. Only relevant if the Vipps-branded frame is wanted; Vantigo can render its
  own QR of its own pay URL.

### 4.2 The 10-minute lifetime, and the pay page it forces

- By default a user has **10 minutes** to accept, after which the payment is `EXPIRED`.
- **Long-living payments** (`expiresAt`, 10 minutes to 60 days) are restricted access ("In
  Norway: Available in special circumstances"), and for Norwegian merchants the timeout
  "can not exceed 24 hours", "The customer must be present at the time the service is
  performed and/or agreed upon", the request goes "only after the delivery of the product
  or service", and `receipt.orderLines` with `productUrl` are mandatory.
- A one-time payment QR "will time out after 10 minutes, so it's not possible to print
  these QR codes".
- **So an ePayment cannot live for an invoice's 14-day term in Norway.**
- Terms: "The customer must actively accept the terms and conditions before a payment is
  initiated. Sending a deeplink directly to a payment does not satisfy this requirement".
  ePayment takes `merchantLegalLinks.termsOfSaleUrl` and `privacyPolicyUrl`. How that rule
  applies to an already-issued invoice is **UNCERTAIN**, and the docs neither list nor
  forbid "paying an invoice" with standard ePayment (**UNCERTAIN**; ask Vipps MobilePay).
  The merchant terms at `https://vippsmobilepay.com/legal/terms-and-conditions` were not
  read.
- **The consequence** (the Vipps study's inference): the link on an invoice is **a stable
  Vantigo URL**, for example `https://<installation>/pay/<opaque invoice token>`, showing
  the amount, due date, seller and the terms and privacy links; "Betal med Vipps" creates
  the ePayment then (`WEB_REDIRECT`, `returnUrl` back to the page) and opens the returned
  `redirectUrl`. Each attempt gets a new `reference` with a suffix. The same URL can be a
  QR on the PDF. The link must not carry a pre-created `redirectUrl`, which dies in 10
  minutes and must be opened "directly, without making any changes".
- **This needs a publicly reachable HTTPS endpoint on the installation** for the pay page
  and the `returnUrl` — a stronger requirement than webhooks alone.

### 4.3 User flows, and the QR on site

| `userFlow` | What happens | Fits Vantigo when |
|---|---|---|
| `WEB_REDIRECT` (normal) | `redirectUrl` opens the app on a phone with it installed, or the landing page elsewhere (the user types their number and approves the push). Needs `returnUrl` (https or a custom scheme, ≤2500 chars) | At home with an e-mailed invoice, through the pay page; also on site on the craftsman's tablet |
| `PUSH_MESSAGE` | Needs `customer` (MSISDN `^\d{9,15}$`, personal QR or token); skips the landing page. **"Special approval required"**: only where the payment starts on a device the user does not own or control, with consent; otherwise error 5080 | On site if the sales unit is approved; never for e-mailed invoices |
| `QR` | Returns a one-time QR in `redirectUrl`; `qrFormat.format` `IMAGE/SVG+XML` (default), `IMAGE/PNG` (`size` 100–2000, default 1024) or `TEXT/TARGETURL`; send `customerInteraction: "CUSTOMER_PRESENT"`; **times out after 10 minutes** | **The quick invoice on site**: the tablet shows the QR and the customer scans it. No phone number; the docs attach no approval to QR |
| `NATIVE_REDIRECT` | App-to-app for a merchant native app; "Not recommended" | Not relevant |

- `customerInteraction` is `CUSTOMER_PRESENT` or `CUSTOMER_NOT_PRESENT` (default);
  CUSTOMER_PRESENT "is required for compliance and reporting reasons" in physical settings.
- A business taking payments online and in person "may be required to create separate
  sales units for each". Whether the on-site quick invoice and e-mailed pay links need two
  MSNs is **UNCERTAIN**; the port should allow more than one credential per provider later.

### 4.4 Create, the lifecycle and the states

**Create** — required `amount`, `paymentMethod`, `reference`, `userFlow`:

- `amount` `{currency, value}`, `value` in øre, minimum 100 (1.00 NOK), maximum 65000000
  (650 000.00 NOK); the currency must match the sales unit's market (NOK for Norway) or
  error 5040.
- `paymentMethod.type = WALLET`. `CARD` needs `WEB_REDIRECT` and is not in the test
  environment.
- `reference` `^[a-zA-Z0-9-]{8,64}$`, case-sensitive, unique per MSN (raised from 50 to 64
  in v1.7.0, January 2025); use an attempt suffix for retries (`-1`, `-2`); a reused
  reference gives 4150.
- Optional: `paymentDescription` (3–100 chars, shown in the app, the portal and the
  settlement files), `metadata` (≤5 pairs), `receipt` (order lines with `taxRate` in
  hundredths, e.g. `2500` = 25%), `receiptUrl`, `merchantLegalLinks`,
  `customerInteraction`, `qrFormat`, `expiresAt` (restricted), `minimumUserAge`,
  `profile`.
- Response 201 `{reference, redirectUrl?}`; `redirectUrl` is absent for PUSH_MESSAGE.
  Errors are RFC 7807 with `extraDetails[{name: "ErrorCode", reason: "6080"}]` and
  `traceId`.

**States:** `CREATED`, `AUTHORIZED` (approved, amount reserved; final), `ABORTED` (the user
cancelled), `EXPIRED` (no action in 10 minutes), `TERMINATED` (the merchant cancelled
before authorization). **There is no CAPTURED or REFUNDED state**: "Even if the merchant
(partially) captures, (partially) refunds, or cancels the payment, the state will remain
AUTHORIZED". The money is in `aggregate`: `authorizedAmount`, `capturedAmount`,
`refundedAmount`, `cancelledAmount`. Event names: `CREATED`, `AUTHORIZED`, `CAPTURED`,
`CANCELLED`, `REFUNDED`, `ABORTED`, `EXPIRED`, `TERMINATED`. Wording: *aborted* is the
customer stopping before authorization, *terminated* the merchant, *cancelled* the
merchant releasing an authorized, uncaptured amount.

| Operation | Endpoint | Idempotency-Key | Notes |
|---|---|---|---|
| Get | `GET /epayment/v1/payments/{reference}` | — | `aggregate`, `amount`, `state`, `pspReference`, `metadata?`, `captureGuaranteedUntil?` (added August 2026) |
| Event log | `GET …/{reference}/events` | — | "the authoritative data for all details and operations for a payment" |
| Capture | `POST …/{reference}/capture` | **required** | Full or partial; check `capturedAmount` — "the HTTP status code alone is not sufficient" |
| Cancel | `POST …/{reference}/cancel` | not required by the spec | Before authorization → `TERMINATED`; after → releases all of the uncaptured remainder; a fully captured payment cannot be cancelled (6040) |
| Refund | `POST …/{reference}/refund` | **required** | Full or partial, repeatable, **within 365 days** (6220); deducted from settlement; "Currently, refunds always have zero fees" |
| Force approve (test) | `POST /epayment/v1/test/payments/{reference}/approve` | — | §4.8 |

- Errors to map: 409 on create/capture/cancel/refund; `423` (retry later); 6160 order
  processing; 6250 cannot cancel a processing payment; 6260 funds unavailable and 6280
  capture failed (both new August 2026).
- **ePayment is always reserve-capture** ("Only the eCom API and Recurring API support
  direct capture"); uncaptured money is never paid out. "By regulation, you must not
  capture a payment until the product or service is ready to be delivered". An issued
  invoice describes a delivered service, so capturing at once on `AUTHORIZED` is within
  the rule. Capture deadline: Norway 180 days by the API, but bank reservations lapse
  sooner (Visa 5–7 days, Mastercard 30, **BankAxept 7 hard**); use
  `captureGuaranteedUntil`; stop retrying a failed capture after 30 days. Partial capture
  is allowed in Norway (6140 "Must capture full amount" exists).
- **Register the payment on a successful capture (`aggregate.capturedAmount` ≥ the
  intended amount), not on `AUTHORIZED`.** A capture can fail for lack of funds (6260).
  Release unused reservations promptly.

### 4.5 Idempotency, rate limits, polling, the two `pspReference`s

- **Idempotency.** `Idempotency-Key` (≤50 chars) is required on create, capture and
  refund. A retry with the same key and a different body gives 4010 (capture) or 4150
  (refund); 4020 is "already exists". **How long a key is honoured is not documented**
  (**UNCERTAIN**): persist the key with the operation row and reuse it on every retry, as
  phase 2 did. Create is also idempotent on `reference` (4150). Repeating a cancel after
  success gives 6050 or 6190; map "already cancelled" to success.
- **Rate limits:** create, capture, cancel and refund **5 per minute per reference + MSN**;
  GET payment and GET events **120 per minute per reference + subscription key**.
- **Outcome.** "Track status with both Webhooks and polling"; "you should not rely on
  webhooks alone"; the checklist: "Implement both webhooks and polling … you must also
  implement the Webhooks API … The merchant must also always poll". Poll cadence: "Start
  after 5 seconds; Check every 2 seconds", and "Wait for the full timeout period" (10
  minutes) before treating an unanswered payment as failed. Polling is bounded to roughly
  10–11 minutes per attempt while `CREATED`, plus one confirmation after capture.
- **Two kinds of `pspReference`.** A webhook's `pspReference` is **the event's own** and
  matches the event log; the one in API responses (GET, capture, cancel, refund) is the
  **CREATED event's**, so they do not match. A capture's event `pspReference` equals the
  Report API's `capture.pspReference` and its fees'.
- **Exactly once.** Register the payment once per capture `pspReference`: the capture
  response, the captured webhook and the poller may all report the same capture.

### 4.6 Keys, tokens and system headers

- **Access token:** `POST https://api.vipps.no/accesstoken/get` (test
  `https://apitest.vipps.no/accesstoken/get`) with `client_id`, `client_secret`,
  `Ocp-Apim-Subscription-Key`, `Merchant-Serial-Number` and the system headers; empty
  body. Valid **1 hour in test, 24 hours in production**, reusable for its whole life,
  several valid at once. Every call then sends `Authorization: Bearer`,
  `Ocp-Apim-Subscription-Key` and `Merchant-Serial-Number`. The OAuth endpoint
  `POST /miami/v1/token` (15 minutes) is for accounting keys, not ePayment.
- **Sales unit keys:** `client_id` (GUID), `client_secret` (Base64), two interchangeable
  subscription keys (for rotation); the MSN is needed but is not a key. Per sales unit and
  per environment; test and production are fully separate. Whether `client_id` is secret
  is **UNCERTAIN** (S22 calls it the "username"); sealing all three is the cautious choice.
- **Partner keys are ruled out for a self-hosted ERP:** "You *must not* use partner keys
  if merchants can see or access them in any way", including when "The keys and secrets
  are stored on the merchant's system"; they also do not work in test. Each installation
  stores **its own merchant's sales-unit keys**, sealed — phase 2's Storecove model. "Partners
  may occasionally use a merchant's own sales unit keys on behalf of that single merchant".
  Becoming a registered partner is optional (a partner manager, help with PUSH_MESSAGE and
  long-living approval, a checklist review with a video); whether a vendor whose merchants
  use their own keys must pass that review is **UNCERTAIN**. The Report API accepts the
  merchant's own sales-unit keys for ePayment merchants.
- **System headers:** `Vipps-System-Name`, `Vipps-System-Version`,
  `Vipps-System-Plugin-Name`, `Vipps-System-Plugin-Version`, each ≤30 chars, "required for
  plugins and partners" and strongly recommended for direct integrations. The Vipps
  study's suggestion: `vantigo` / release version / `vantigo-payments` (or
  `vantigo-invoices`, later `vantigo-pos`) / release version, plus `Merchant-Serial-Number`
  always. The spec lists them on create, cancel, capture, refund and the token call, not
  on GET; sending them everywhere is harmless.

### 4.7 Webhooks

- **Register** `POST /webhooks/v1/webhooks` `{url, events[]}` → `{id, secret}`; list and
  delete. A registration cannot be updated: register anew, verify, delete the old one. The
  URL must be world-reachable HTTPS with no redirect, on a common port (80, 443, 8080),
  TLS 1.2; up to 25 registrations per event type per MSN. ePayment events:
  `epayments.payment.{created,authorized,aborted,expired,cancelled,captured,refunded,terminated}.v1`.
  Webhooks fire for actions in the business portal too, e.g. a manual refund.
- **Signature** (request-authentication page, exact). Headers `x-ms-date` (RFC 1123),
  `x-ms-content-sha256`, `Authorization`, `Host`; `Webhook-Id` names the registration.
  1. `x-ms-content-sha256` must equal `base64(SHA256(raw body bytes))`.
  2. The string-to-sign is exactly

     ```
     POST\n<pathAndQuery>\n<x-ms-date>;<host>;<x-ms-content-sha256>
     ```

     with `\n`, **not `\r\n`**; `<pathAndQuery>` the registered URL's path and query;
     `<host>` the target URL's host.
  3. `signature = base64(HMAC-SHA256(key = UTF-8 bytes of the secret string, msg =
     string-to-sign))` — the C# sample uses `Encoding.UTF8.GetBytes(secret)` and Node
     `createHmac('sha256', secret)`.
  4. Expected: `Authorization: HMAC-SHA256
     SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=<signature>`.
- **The published test vector fails.** Secret `AAAA…AA==` (86 `A` + `==`), date `Thu, 30
  Mar 2023 08:38:32 GMT`, host `webhook.site`, path
  `/e2cee29b-012e-4f1d-8ef4-e95fd74a7a63`, body
  `{"some-unique-content":"ee6e441b-cc4a-46f8-895d-a5af79bcc233/hello-world"}`, content
  hash `lNlsp1XA03N34HrQsVzPgJKtC+r7l/RBF4V3JQUWMj4=`, signature
  `agAiSyogQbDHpeucoNwYz+yAr5nJ+v+zasdkSbqzv+U=`. On 2026-10-06 the **content hash
  reproduced exactly; the signature did not** under the documented string-to-sign, with
  the secret as UTF-8 and Base64-decoded, `\n` and `\r\n`, path and full URL, host with
  and without `:443`, and the registration example's secret. The HMAC key interpretation
  is **UNCERTAIN**; pin the hash step to the published vector and the signature step to a
  fixture captured from a real test webhook (or a self-generated vector flagged as such).
- **Also UNCERTAIN:** whether `<host>` includes a non-default port; how a reverse proxy
  that rewrites `Host` or the path affects verification (verify against the configured
  public URL, not the request line); any clock-skew or replay window for `x-ms-date` (none
  documented).
- **Retries:** on a 4xx/5xx or no answer in **10 seconds**; attempts 1–4 every 2 seconds,
  5 after 60 s, 6 after 120 s, 7–29 hourly, then daily, **ending after 7 days** ("This retry
  scheme may change"). **Ordering is guaranteed per registered webhook**: "your server needs
  to accept all preceding requests for a given payment, before any new notifications can be
  received for the same payment" — head-of-line blocking per payment, so answer 200 once
  the event is verified and stored, and process it asynchronously. Registrations for
  servers unresponsive for 2 weeks are deleted; a deletion inside the retry window ends the
  retries; check and re-register on a schedule and store the new secret. Do not let an
  obsolete endpoint return 404; Vipps cannot resend by hand.
- **Payload:** required `msn`, `reference`, `pspReference`, `name`, `amount`, `timestamp`,
  `success`; optional `idempotencyKey` (some events such as CANCELLED may not supply it),
  `captureGuaranteedUntil`, `userDetails`, `sub`, `shippingDetails`. `amount` is the
  event's own (a capture's or refund's), not the aggregate: re-read with GET after an
  event. `success: false` is a failed operation, not a state change. Dedupe on event
  `pspReference` + `name`.
- **Polling-only.** Vipps' docs never describe it as acceptable, and the checklist says
  "must" implement webhooks; whether Vipps accepts a polling-only direct integration is
  **UNCERTAIN** (the merchant go-live flow has no review step). Functionally polling is
  enough (the event log is authoritative, GET carries every amount, a request lives 10
  minutes, 120 GETs a minute are allowed): webhooks can be an optional accelerator. But
  the pay page already needs public HTTPS, so polling-only matters mainly for the on-site
  QR and for installations that send no pay links.

### 4.8 The test environment

- Test ("MT") base URL `https://apitest.vipps.no`, production `https://api.vipps.no`; no IP
  restrictions.
- **No anonymous sandbox.** Test keys need an active customer relationship: a registered
  business with an organisation number in NO, DK or FI, a business bank account in the same
  country, and eID to sign. Then *For developers → API keys → Test → Add test sales unit*
  (options: skip landing page, Recurring, direct capture). Partners get test keys by
  e-mail. The tagged test needs the Vantigo team's own test sales unit, as phase 2 needed
  a Storecove sandbox key.
- Test users are generated in the portal with a card, cannot be modified, and must match
  the market. The MT app comes via TestFlight / a Google Group. Special amounts (øre): 151
  insufficient funds, 182 refused by issuer, 183 suspected fraud, 184 withdrawal limit,
  186 expired card, 187 invalid card, 197 3DS denied, 201 unknown result for 1 hour, 202
  SCA required; refund triggers 123 and 124.
- Not in test: `CARD`, the Report, Sales and Management APIs, partner keys, and
  **settlements** ("There are no settlements in the test environment"; the payout flow can
  only be tested in production with 2 NOK payments). The Webhooks API is in test.
- **Force approve:** `POST /epayment/v1/test/payments/{reference}/approve` with
  `{"customer": {"phoneNumber": …}}` or `{"token": …}` (from `redirectUrl`); "Attempted use
  in production is not allowed". **"All test users must manually approve at least one
  payment in the Vipps or MobilePay app before this endpoint can be used for that user"**:
  one manual approval per test user, once. Test errors 10010, 10020, 10030, 10050, 10070.
- The Vipps study's sketch of the tagged test (mirroring `//go:build storecove`):
  `//go:build vipps`, skipped unless `VIPPS_TEST_CLIENT_ID`, `VIPPS_TEST_CLIENT_SECRET`,
  `VIPPS_TEST_SUBSCRIPTION_KEY`, `VIPPS_TEST_MSN`, `VIPPS_TEST_PHONE` are set; token,
  create, force approve, poll to `AUTHORIZED`, capture with a key and assert
  `capturedAmount`, repeat the capture with the same key, partial refund, check the event
  log, a second create cancelled to `TERMINATED`, optionally amount 151. One reference per
  scenario (rate limits are per reference); refresh the 1-hour token.

### 4.9 Commercial terms, payouts and reconciliation

- **Agreement:** a merchant agreement signed with eID; order "Payment Integration" /
  "Integrert betaling"; KYC, PEP and AML checks; then production keys. An unverified bank
  account gives 5060.
- **List prices** (commercial pages): Integrert betaling **2.99% + 1 NOK** (seen on
  /betalingslosninger, not on /pricing — **UNCERTAIN**), Betalingslenker 2.49% + 1 NOK,
  Faste betalinger 2.99% + 1 NOK, Vippsnummer and Vippskassa 1.75%.
- **Settlement (Norway):** daily, weekly (Monday) or monthly (the 1st). Daily: capture on
  day 1, report day 2, payout day 3 (a Monday capture paid Wednesday, a Friday capture
  Tuesday). **One lump sum per sales unit per period.**
- **Payouts are not KID payments.** The bank text is `Utb. {Recipient} Vippsnr
  {PayoutNumber}`, e.g. `Utb. 2000810 Vippsnr 117703`; "This format is not configurable".
  A camt import sees one credit per payout that matches no invoice, and it **must not be
  invoice-matched**: the invoice was already settled on capture. Route it to a "Vipps
  clearing" position (exception case n in §3.5).
- Net settlement retains fees from the payout; gross invoices them; refunds are always
  deducted from the next payout; a negative balance can lead to a top-up invoice. The
  default for a new merchant is **UNCERTAIN**.
- **Report API** (production only, merchant's own keys): `GET /settlement/v1/ledgers`
  (`?settlesForRecipientHandles=NO:<msn>`) for the `ledgerId`; `GET
  /report/v2/ledgers/{ledgerId}/funds/dates/{YYYY-MM-DD}` or `/funds/feed`; topic `fees`.
  Entry types `capture`, `refund`, `fees-retained`, `payout-scheduled`, `payout-aborted`,
  `retained-disputed-capture`, `returned-disputed-capture`, `correction`, `top-up`,
  `credit-note` ("Be prepared to receive entry types not listed here"). A `capture`
  carries `reference` and `pspReference`; reconcile on those, "Do not pair on
  ledgerDate". `payout-scheduled` has `pspReference = <ledgerId>-<payoutNumber>`.
- **The two Vipps pages disagree.** The settlements page says a `payout-scheduled`
  entry's `reference` "matches the reference text on your bank statement"; the Report API
  entry-types page says for Norway "Currently this field is missing in Norway and will not
  correspond to the reference on the payout". **UNCERTAIN.** Match a Norwegian payout by
  amount, date and `Vippsnr {PayoutNumber}` against the payout number in `pspReference`.
- The Sales API is for accounting partners and VM-number/mPOS payments; not needed for
  ePayment.

### 4.10 MobilePay, and what other providers share

- **MobilePay** uses the same ePayment API, endpoints and base URLs; a Danish sales unit
  uses DKK and a Finnish one EUR. A Norwegian merchant is paid in NOK by a MobilePay user.
  Differences: capture deadline 14 days unless approved, partial capture on request,
  long-living payments on request in Denmark (and in Finland for former MobilePay Invoice
  merchants) with app reminders 72 and 24 hours before expiry, commercial-card blocking. A
  MobilePay adapter is the same adapter with other currency and market rules.
- **Others** (secondary): Vipps on Stripe is a private preview (`vipps_preview=v1`, NOK);
  Stripe Payment Links and Checkout Sessions give a hosted link plus `Stripe-Signature`
  webhooks (their lifetimes are **UNCERTAIN**, not read in primary docs). Nexi Checkout
  offers Vipps as a method with reserve/charge, refund and a
  `payment.checkout.completed` webhook. Zettle and SumUp start payments on their own
  devices; they are sales readers for Point of sale, outside this port's shape.
- **Common operations:** create a payment request (amount in minor units and currency,
  merchant reference, description, return URL, optional customer hint → provider id, a
  link or QR, an expiry); get status (a normalised state and the four amounts); cancel or
  release; refund with an idempotency key; verify and parse a webhook; capture where the
  provider separates it. **What differs:** reserve-capture vs automatic capture; the
  request lifetime; push-to-phone; who renders the QR; the webhook signature; whether the
  provider can hold a long-lived link (Stripe) or Vantigo must host it (Vipps); the
  currency/market per credential; the settlement report.

**Identifiers to persist per attempt:** `reference` (one per attempt; invoice id plus an
attempt suffix), `msn`, the CREATED `pspReference`, the event `pspReference`s, `redirectUrl`
(short-lived; treat as sensitive — whoever holds it can pay or see the request), `userFlow`,
`customerInteraction`, `paymentDescription`, `state`, the four `aggregate` amounts,
`captureGuaranteedUntil`, each operation's Idempotency-Key, the webhook `id` and sealed
`secret`, and (production) `ledgerId` and the payout `pspReference`.

## 5. The codebase's seams

### 5.1 Payments

- **Table** `invoices.payments` (`mig/00035_invoices_payments_delivery.sql:13-33`): id
  identity, `invoice_id` FK RESTRICT, `paid_on`, `amount numeric(14,2) > 0`, `currency`,
  `reference varchar(100)` (free text, not unique, `:19`), `note varchar(500)`,
  **`registered_by_user_id uuid NOT NULL` (`:21`)**, `registered_at`, and `removed_at`,
  `removed_by_user_id`, `removal_reason` all or none (`ck_payments_removal`, `:27-29`).
  Indexes `ix_payments_invoice` and the partial `ix_payments_invoice_live … WHERE removed_at
  IS NULL` (`:31,33`). **No source column, no external reference, no idempotency key.**
- **Triggers.** `refuse_payment_change` / `tr_payments_immutable` (`:67-89`) refuses DELETE
  and allows only the removal and the note blanked; it compares `to_jsonb(OLD)` minus the
  removal columns and the note (`:73-74`), so **any column added later is frozen without
  touching the function**. `ALTER … DROP NOT NULL` is DDL and does not fire it.
  `refuse_payment_on_unissued` / `tr_payments_parent` (`:100-122`) reads the parent FOR
  SHARE, requires an issued invoice, and blanks the note for an erased customer.
- **The nullable-user precedent:** `transmissions.resolved_by_user_id` is nullable, a
  worker's resolution carrying a note and no user (`mig/00036_invoices_ehf_kid.sql:92,104`).
- **Handlers** (`srv/invoices/payments.go`): `parsePayment` (`:54`), `openOf` (`:93`,
  uncredited total minus live payments), `PostInvoicesByIdPayments` (`:111`) — refuses a
  credit note and a draft, then `withLockedTx` (`:143`; READ COMMITTED,
  `srv/invoices/server.go:115-117`), `LockInvoice` FOR UPDATE (`:147`), `openOf`
  (`:157`), and the refusals `invoice_settled` and `payment_exceeds_open` ("An
  overpayment is not registered.", `:161-171`). The removal is at `:197`. `today` is
  `businessDay(s.deps.Clock())` (`:130`; `srv/invoices/values.go:32`). Credit notes lock
  the same original row, so payments and credits serialise on it.
- **Response:** `paidAmount`, `openAmount`, and `refundDue` when open < 0
  (`srv/invoices/responses.go:207-209`) — a figure only, with no flow behind it.
- **Permission:** `invoices:payments`, sensitive (`srv/invoices/module.go:61-63`); the
  payment operations are `permission:invoices:access+invoices:payments`
  (`openapi/invoices.yaml:3306,3366`), not `invoices:issue`.
- **State:** `invoices.document_state(kind, status, gross, credited, paid, due_date, today)`
  (`mig/00035_invoices_payments_delivery.sql:190-200`), IMMUTABLE: draft, a credit note's
  issued, credited, paid (open ≤ 0), **overdue (`due_date < today`)**, partially_paid,
  open. Its Go mirror `documentState` (`srv/invoices/state.go:39`) is held to it by a test;
  the filter values are `invoiceStates` (`:29`). A reminder's own new deadline does not
  change `overdue`, which derives from the frozen `due_date`.
- **Not in the CSV export** (`srv/invoices/queries/export.sql`). The customer slots carry
  payments in the personal-data export and blank their notes on erase
  (`srv/invoices/customer_slots.go:518`); payments have no `customer_id`, so a merge
  re-points nothing.

### 5.2 KID

- **Package** `srv/invoices/kid/kid.go` is a leaf: `CheckMod10` (`:29`), `CheckMod11`
  (`:47`, returns **`'-'` when the remainder is 1**, `:44-56`), `Compute` (`:76`; body =
  invoice number zero-padded to length − 1, then the check digit), `Verify`, `Fits`.
  **There is no `Parse`** and no query by KID. A bank file may carry the `'-'` check digit,
  and a parser must accept it.
- **Reverse lookup needs no new index:** parse the body to a number and look up by
  `number`, which `ux_invoices_number` indexes (`mig/00034_invoices_baseline.sql:195`),
  then confirm the stored `kid`. Numbers are unique across invoices and credit notes in
  the one series, and a credit note's `kid` is NULL (`mig/00036_invoices_ehf_kid.sql:31-36`).
- **Settings:** `kid_length smallint` (4–25) and `kid_algorithm` (mod10/mod11), both or
  neither (`mig/00036_invoices_ehf_kid.sql:17-22`). One length only; phase 2 left out
  several lengths on one agreement (`invoices.md:2117-2118`), while a bank agreement may
  register up to three (§3.1). No OCR Avtale-ID and no account field beyond the seller's
  `bank_account varchar(11)` (`mig/00034_invoices_baseline.sql:30`) and IBAN/BIC; a file's
  account can be checked against `bank_account`.

### 5.3 Documents, numbering, mail

- **Kind:** `ck_invoices_kind CHECK (kind IN ('invoice', 'credit_note'))`
  (`mig/00034_invoices_baseline.sql:170`). **Issued documents are frozen:**
  `refuse_issued_document_change` (`:256-281`) refuses every UPDATE except to
  `customer_id` and the PDF set once. Reminder state (level, fee, new deadline, hand-off)
  must live in new tables keyed by `invoice_id`.
- **Counter:** one row `'documents'` (`mig/00034_invoices_baseline.sql:52`;
  `AllocateNumber`, `srv/invoices/queries/counters.sql:15-22`); the journal checks the
  series for gaps (`srv/invoices/journal.go`). A reminder must not take a number from it.
  A reminder series, if wanted, would be a second `counter_name` row.
- **PDF:** `storeOnce` keys `documents/%d/%d-%s.pdf` (`srv/invoices/pdfstore.go:268,278`);
  a reminder PDF needs its own model and key prefix.
- **Deliveries:** `invoices.deliveries.invoice_id` references the invoice
  (`mig/00035_invoices_payments_delivery.sql:40`); a reminder delivery could reference the
  invoice but not the reminder.
- **Mail:** `PostInvoicesByIdSend` (`srv/invoices/send.go:226`) sends synchronously through
  `s.smtpSend` (`:303`) and records the delivery (`:321`); it is rate-limited at **60 per
  client per 10 minutes** (`srv/invoices/module.go:75`). A reminder run reusing it would
  hit that limit; 1B left out "sending through an outbox or a worker"
  (`invoices.md:2126-2127`). The mail platform is `mail.Outbound` / `SendOutbound`
  (`srv/mail/outbound.go:44,61`).
- **EHF:** reminders cannot travel as EHF or eFaktura by the customers module's own rule
  (`srv/customers/billing_values.go:215-216`).

### 5.4 Due dates, overdue, stats

- `DueDate = issueDate + payment_terms_days` at issue (`srv/invoices/issue.go:521`); terms
  from the request, the profile, then settings (`srv/invoices/drafts.go:484,487`);
  `default_payment_terms_days` defaults to 14 (`mig/00034_invoices_baseline.sql:34`).
- Overdue drives the list's `overdue` filter, the summary's `overdueCount`/`overdueAmount`
  (`srv/invoices/stats.go:61,87-88`) and the customer 360 tab.
- **Invoices has no `/stats/attention`.** Only `/invoices/stats/summary` exists
  (`openapi/invoices.yaml:3787`); customers and other modules have one (e.g.
  `openapi/customers.yaml:4502`). 1B left out "timeseries and attention stats"
  (`invoices.md:2128`). An "overdue / reminder due" attention item would be new.
- Queries take `@today`/`@now`, never `CURRENT_DATE` or `now()`.

### 5.5 The customer side

- `customers.customers.reminder_email` and `reminder_delivery`
  (`mig/00019_customers_billing_profile.sql:10,15`); `reminder_delivery` is email or
  paper only (`srv/customers/billing_values.go:215-216`).
- Both arrive resolved on `contracts.CustomerBillingProfile` (`srv/contracts/directory.go:74`):
  `ReminderEmail` (`:105`, falling back to the invoice e-mail) and `ReminderDelivery`
  (`:114`); resolution at `srv/customers/directory.go:178-238`. **No invoices code reads
  them yet.**
- **Consumer or business:** `CustomerBillingProfile.Type` is `"business" | "person"`
  (`srv/contracts/directory.go:78`), snapshotted as `invoices.invoices.buyer_type`
  (`mig/00034_invoices_baseline.sql:130`; `buyerSnapshot`, `srv/invoices/issue.go:114,122`).
  The snapshot, not a fresh directory read, chooses the B2C or B2B regime. Nothing tells
  a sole proprietor apart from other businesses.
- **Reminder policy** (no reminders, no fee, no interest, excluded, hand to inkasso) has no
  column anywhere. Two homes: the customers billing profile (a customers migration, a
  contract field, the customers UI; customers owns the profile), or an invoices table keyed
  by opaque `customer_id`, which `RepointCustomer` must re-point on merge
  (`srv/invoices/customer_slots.go:54`) and the export and erase must cover. Group defaults
  would be another customers migration.

### 5.6 The access-point pattern, credentials, workers

- **Port:** `AccessPoint` (`srv/invoices/accesspoint/accesspoint.go:20`) with typed errors;
  adapter `NewStorecove(baseURL, apiKey, legalEntityID, transport, now)`
  (`srv/invoices/accesspoint/storecove.go:58`).
- **The fake is an HTTP fake:** `srv/invoices/accesspoint/storecovetest` is an ordinary
  package serving an httptest TLS server whose `Transport` tests pass as
  `Deps.HTTPTransport` (`srv/module/module.go:131`), so the real adapter runs over HTTP. A
  `vippstest` would copy it.
- **Tagged sandbox test:** `srv/invoices/accesspoint/storecove_sandbox_test.go:1`
  (`//go:build storecove`).
- **Credentials:** `invoices.access_point_credentials` (`mig/00036_invoices_ehf_kid.sql:44`):
  a single row, `provider` CHECK, `settings_json` (non-secret), `secret_ciphertext`,
  `rejected_at`. Handlers in `srv/invoices/credentials.go` (purpose
  `invoices/access-point-credential`, `:29`; the 409 `transmissionsActive`, `:62`; a 503
  when the key is unreadable, `:67-72`). Sealing is `secrets.Box`, AES-256-GCM per
  purpose from APP_SECRET (`srv/secrets/secrets.go:99,141,168,193,203`), available as
  `Deps.Secrets` (`srv/module/module.go:35`).
- **Config:** `INVOICES_EHF_ENABLED` (`srv/config/config.go:632`) and
  `INVOICES_STORECOVE_BASE_URL` through `httpBaseURL` (`:516`), so a holder of
  `invoices:manage` cannot point the client elsewhere. The pattern for a Vipps base URL and
  an enable switch.
- **Workers** (`srv/worker/worker.go:26-40`: Name, Interval, Run) are registered only when
  `InvoicesEhfEnabled` (`srv/invoices/module.go:102`). Two shapes:
  - **row lease** — `EhfWorker` (`srv/invoices/ehf_worker.go:149`, 60 s lease at `:53`)
    claims with `ClaimTransmission`, a conditional UPDATE over a `FOR UPDATE SKIP LOCKED`
    pick (`srv/invoices/queries/transmissions.sql:54-73`). A Vipps poller fits it.
  - **advisory lease** — `EhfEventsWorker` (`srv/invoices/ehf_events_worker.go:38`, key
    `0x494E5645484631`; `underLease` with `pg_try_advisory_lock` on its own connection,
    `:221-229`; at most 500 per cycle, `:42`). A reminder run or nightly overdue scan fits
    it.

### 5.7 Inbound HTTP: anonymous routes, CSRF, the raw body, no public page

- **No inbound webhook exists.** The Mailgun inbound webhook was dropped in the Go port
  (`srv/openapi/cmd/contract/split.go:19,25`); Storecove events are pulled; phase 2 left
  push webhooks out (`invoices.md:2116`). A Vipps receiver would be the first
  unauthenticated, non-identity endpoint.
- **The router supports it.** `x-vantigo-access: anonymous` is used today only by identity
  (`openapi/identity.yaml`), and any module's contract may declare it
  (`srv/module/router_test.go:31`).
- **CSRF:** `http.NewCrossOriginProtection` wraps the API (`srv/server/server.go:41-53`);
  "Requests with neither header are non-browser clients and pass", so a Vipps server POST
  gets through. Session cookies are SameSite=Strict (`srv/identity/cookies.go:101`).
- **The raw body.** The router caps bodies with `MaxBytesReader`, 1 MiB by default
  (`srv/module/router.go:23,318`), and the strict handlers decode JSON into typed bodies.
  **The HMAC check needs the raw bytes.** The code study's options: declare the body
  `application/octet-stream` or `text/plain` so the strict handler gets an `io.Reader`, or
  tee the body in a middleware. Multipart operations already get the raw reader, the
  nearest precedent.
- **No public page.** Invoices has no public, token or share route. The host's
  unauthenticated pages are `publicPaths` (`host/lib/public-paths.ts:2-9`: sign-in, setup,
  password and invitation pages, session-expired). A pay page or `returnUrl` page is a new
  public path, plus, if it shows status, a new anonymous API behind an unguessable token.
  Return URLs would be built from `Config.AppOrigin` (APP_URL), which the server already
  trusts for CSRF (`srv/server/server.go:47`). The server's mux serves `/health/`, the docs
  and the SPA (`srv/server/server.go:62-67`).

### 5.8 Uploads, quick invoice, migrations

- **Upload pattern:** `POST /customers/import` (`srv/customers/import.go`: one multipart
  part named `file`, `importBodyLimits` at `:92`, `importFilePart` at `:125`), wired
  through `module.RouterOptions.BodyLimits` (`srv/module/router.go:35-37`). The raw file
  can be kept in the object store (`Deps.ObjectStore`). The CSV writer for an inkasso
  export is `srv/invoices/csvfile.go` / `csvexport.go`.
- **Quick invoice = two transactions today.** `PostInvoices` (`srv/invoices/drafts.go:453`)
  then `PostInvoicesByIdIssue` (`srv/invoices/issue.go:254`), which reads the profile and
  checks the store before its transaction and takes document → settings → counter locks.
  One step is either two transactions or a refactor into one.
- **Migrations:** the last is `00040_invoices_work.sql`, so phase 4 starts at **00041**;
  names are `NNNNN_<module>_<name>.sql` and the owner is parsed from the name
  (`srv/db/schema_test.go:39`); house style is one migration per phase. A new dated rates
  table can copy `vat_code_rates`' exclusion constraint (`mig/00034_invoices_baseline.sql:80,88`,
  btree_gist at `:14`). A new child table of a document should copy the `guard_*_insert`
  FOR SHARE + `erased_customers` pattern (`mig/00035_invoices_payments_delivery.sql:156-177`).

### 5.9 The module-boundaries rules on a shared package and its schema

- The roadmap wants the `PaymentProvider` port in a shared server package so Point of sale
  can use it. **Code can be shared:** rule 1 lets a module import platform packages,
  `internal/peppol` (shared by Customers and Invoices) being the precedent
  (`docs/src/content/docs/en/contributing/module-boundaries.md:22-37`). A new
  `internal/payments` (or similar) must be added to the depguard `platform` list
  (`apps/server/.golangci.yml:361-400`, which lists `internal/peppol` and
  `internal/secrets`, so it may import no business module) and named in rule 1. Rule 2:
  the platform imports no module; rule 3: contracts stay pure.
- **Tables cannot be shared:** rule 4, one schema per module (`module-boundaries.md:48`).
  `TestNoModuleReferencesAnotherModulesSchema` (`srv/db/schema_test.go:133`) fails when one
  module qualifies another's schema; `moduleSchemas` (`:28`) excludes `platform`, which
  `internal/ratelimit` uses (`:25-26`). A package holding SQL would need `platform` or a
  new schema. So the credentials and attempt tables either belong to Invoices (Point of
  sale gets its own later) or a platform-owned table is introduced.
- A new shared package's path must appear in some docs page's `sources`, or `docs:check`
  will not see it.

## 6. What the research settles, and what it leaves to the spec

**Settled by the facts:** fees are computed from a dated inkassosats with 1/20 and 3/20,
rounded to the krone; late interest uses one dated rate per half-year, split at the
boundary, with no statutory compounding; the § 3a compensation is its own dated EUR-40
figure, B2B only, offset against fees; a reminder cannot be a document kind or take a
number from the salgsdokument series (whether it is a salgsdokument is itself
**UNCERTAIN**, but nothing found requires it to be one); camt.054 or OCR, not camt.053, is
what KID matching reads, through `…/CdtrRefInf/Ref` with `SCOR`; parsing is hand-written;
ePayment, not eCom or an invoice API, is the Vipps product; a Vipps link on an invoice must
be Vantigo's own pay page; the payment is registered on a confirmed capture, as Vipps' own
capture rule implies; each installation uses its merchant's own sales-unit keys; a Vipps
payout is never an invoice payment; the credential and worker patterns of phase 2 apply.

**Left to the spec** (the facts that bear on each in brackets):

1. **Where reminder policy lives** — customers' billing profile or an invoices table with
   customer slots (§5.5); per customer only or also per group; which policy fields (no
   reminders, no fee, no interest, B2B agreed rate R4, § 3a on or off).
2. **Overpayment, credit balances and refunds: in phase 4 or not.** The reference page's
   "What comes next" says phase 4 includes "overpayment, customer credit balances and
   refunds as a flow" (`invoices.md:2083-2085`); `ROADMAP.md:851-870` does not. Today
   overpayment is refused and `refundDue` is a figure (§5.1). If out, exception cases c, f
   and g of §3.5 end in the queue with a manual resolution.
3. **The dated rules engine** — which rules are data (R1–R20), the shape of the rates
   tables (one table for rate + compensation, one for the sats, or one generic dated-value
   table), who maintains values after release (a migration per half-year, an admin
   screen, or both), and how the new inkassolov's regime switches on an unknown date
   (§2.1, §2.11).
4. **The inkassovarsel's end** — whether phase 4 sends a creditor's inkassovarsel at all,
   given that the right ends with the new law (signalled 2027-01-01) and the
   betalingsoppfordring's fee with it; or purring only, plus "the claim will be sent to an
   inkassoforetak" wording (Prop. 3 L 12.5.5) (§2.1, §2.6).
5. **Interest details** — first interest day (due date or the day after), day count,
   whether interest is charged at all by default, on which events it is computed (on a
   reminder, at hand-off, on demand), and which compensation figure applies (due date or
   claim date) (§2.2, §2.3).
6. **Where a reminder fee and interest are recorded** — a reminder table with fee and
   interest columns, a separate dated-claim record, or an interest/fee note; how they show
   on the invoice view and in `openAmount`; whether payments can be allocated to them;
   income recognition on issue or on payment (§2.9, §5.3).
7. **Partial-payment allocation** — principal first, costs → interest → principal, or the
   debtor's designation; recorded so the dekningsrekkefølge can be explained (§2.7).
8. **Due dates on weekends and the grace period** before a deadline counts as missed;
   whether the OCR `Oppdragsdato` or camt dates feed the timeliness test (§2.7, §3.1).
9. **Reminder delivery** — a worker or batch path through `SendOutbound` instead of the
   rate-limited send (§5.3); paper reminders (PDF only); the reminder PDF's content per
   letter type (§2.6 table); its own key prefix and delivery log.
10. **Import sources** — OCR, camt.054 or both; one active source per account; versions
    .02 and .08; card information transactions; DBIT entries; the transaction
    fingerprint; runtime XSD validation or a test-time oracle (§3).
11. **The exception queue** — its reason codes and resolutions (§3.5), auto-post of
    partial payments, suggestion-only for no-KID lines, the split resolution (one payment
    across several invoices, left out of 1B).
12. **The payments migration** — a `source` column (manual / ocr / camt054 / vipps), a
    nullable `registered_by_user_id` with a CHECK tying it to the source, and a unique
    external reference (bank fingerprint, Vipps capture `pspReference`) (§5.1).
13. **The hand-off format** — no standard exists (§2.10): a CSV in the house format with
    the implied minimum set, principal apart from fees and interest; what happens to the
    invoice after hand-off (a status, the run stopped, payments still matched).
14. **The pay page and public HTTPS** — a new public host path and an anonymous API behind
    an unguessable token; what it shows; how it behaves for a paid, credited or handed-off
    invoice; how an installation without public HTTPS degrades (no pay links, QR only)
    (§4.2, §5.7).
15. **Webhook vs poll** — polling as the guaranteed path and webhooks as an accelerator, or
    both required; the raw-body route; verification against the configured public URL;
    re-registration on a schedule (§4.5, §4.7).
16. **Where the credentials and attempts live** — `invoices.payment_provider_credentials`
    on the access-point shape (Point of sale gets its own later) or a platform-owned table;
    one credential or several per provider (online and in-person sales units) (§4.3,
    §5.9).
17. **The shared package's shape** — the port's operations (§4.10), its name, depguard and
    rule-1 entries, its docs page and `sources`.
18. **The Vipps payout clearing** — how a camt import recognises `Utb. … Vippsnr …` and
    where it routes it; whether Report API reconciliation is in phase 4 or later (§4.9).
19. **The quick invoice** — two transactions or one refactored issue path; single line;
    person or business; optional QR at once (§4.3, §5.8). The line to kontantsalg is
    studied in the addendum (§9).
20. **Attention and navigation** — an Invoices `/stats/attention` for overdue or reminder
    due, and a receivables entry gated on `invoices:payments` (§5.4).

## 7. UNCERTAIN items

Merged from the four studies; the section that raises each is in brackets.

1. The new inkassolov's in-force date (signalled 2027-01-01) and its forskrift: egeninkasso
   fee levels, timing and letter content; whether a hearing has been published (§2.1).
2. Whether interest starts on the due date or the day after (§2.2).
3. The day-count convention and interest on interest (§2.2).
4. Whether late interest runs on the purregebyr or the § 3a compensation (§2.2).
5. The implied Norges Bank policy rates (derived, not read) (§2.2).
6. Whether the § 3a compensation is per invoice or per claim bundle (§2.3).
7. Which half-year's NOK figure applies to the § 3a compensation: due date or claim date
   (§2.3).
8. Whether one letter may count as both purring and inkassovarsel under § 1-3 (§2.5).
9. Whether plain e-mail is always "betryggende" for a varsel; no forskrift under INKL
   § 3 a (§2.5).
10. Weekend or holiday due dates: no statutory roll-forward; domstolloven § 149 by analogy
    (§2.7).
11. Whether camt.054 gives the payer's order date (OCR has `Oppdragsdato`) (§2.7, §3.2).
12. Whether an ordinary invoice term is a "kredittavtale" under FAL § 2-9 (3); the
    customary allocation order (§2.7).
13. Fees on letters sent under 14 days after due date as case law (clear as text) (§2.8).
14. The § 3a compensation's VAT treatment; a fee above the statutory amount or a
    fakturagebyr (§2.9).
15. Whether a reminder is a salgsdokument (inference only); income-recognition timing;
    bokføringsloven § 10 as amended from 2027-01-01, not read (§2.9).
16. Hand-off formats: no standard; Intrum, Sergel, Lowell and PRA not confirmed; whether
    direct payments after hand-off must be reported to the byrå (§2.10).
17. The OCR giro end date (none published) and the claim that camt will replace OCR (§3.1).
18. The Direkte remittering end date: DNB 31 Oct 2026 vs secondary 31 Dec 2025 (§3.1).
19. The sign of OCR reversal types 18/20; how banks report a recalled ordinary giro (§3.1).
20. The OCR file's character encoding; which payer channels map to types 10/13/16 (§3.1).
21. Whether a check-digit-invalid KID can appear in camt `CdtrRefInf/Ref`, and the sumpost
    for a brevgiro with a wrong KID (§3.2).
22. How a reversed incoming KID payment links to its original in Norwegian camt.054 (§3.2).
23. The Danske example file's redistribution licence (§3.2).
24. Bits MIG 2.0 (camt.054.001.08) content; portal needs login (§3.2).
25. Whether DNB's camt.053 breaks KID lump sums into `TxDtls` (§3.3).
26. Whether Nordea and SpareBank 1 online banks offer camt.054 as a manual download;
    Handelsbanken channels; SpareBank 1 prices (§3.4).
27. Visma eAccounting's queue (pages unreachable) (§3.5).
28. Whether a re-ordered OCR copy keeps its forsendelsesnummer and a camt COPY its MsgId
    (§3.6).
29. Whether the OCR archive reference equals camt's `TxDtls/Refs/AcctSvcrRef` (§3.6).
30. The mbanq/iso20022-go licence; the XSD files' own header text (§3.7).
31. Whether Vipps accepts a payer-started ePayment from an e-mailed invoice's pay page
    under standard Integrert betaling, and how the "actively accept the terms" rule applies
    to an issued invoice; the merchant terms not read (§4.2).
32. Whether the on-site QR and remote pay links need separate sales units (§4.3).
33. The Betalingslenker link lifetime, and that it has no API (§4.1).
34. eCom: "Legacy" with no discontinuation date vs "deprecated" (§4.1).
35. How long Vipps honours an `Idempotency-Key` (§4.5).
36. Whether `client_id` is secret; whether a vendor using merchants' own keys must pass the
    partner checklist (§4.6).
37. The webhook HMAC key interpretation (the published signature vector does not
    reproduce); the `host` value behind proxies or non-default ports; any replay or
    clock-skew window (§4.7).
38. Whether Vipps accepts a polling-only direct integration (§4.7).
39. The default settlement type (net or gross); whether the Norwegian payout `reference`
    matches the bank text (two Vipps pages disagree); whether a Norwegian bank delivers the
    payout credit in camt.054 or only camt.053; the 2.99% + 1 NOK list price on /pricing
    (§4.9).
40. Stripe Payment Link and Checkout Session lifetimes (secondary) (§4.10).
41. The line between an on-site quick invoice paid by Vipps and a kontantsalg: the Vipps
    study defers it to the law study, which does not cover it (§6 item 19). Answered by a
    fifth study in the addendum (§9); its own UNCERTAIN items are §9.6.

**Where the studies disagree or leave a gap:**

- **A reminder as a salgsdokument.** The code study states "a reminder is not a
  salgsdokument" as a fact; the law study finds no primary statement and marks it an
  inference (§2.9). The design conclusion (no number from the series) holds either way,
  because the bookkeeping law does not require a reminder to be a sales document.
- **The Vipps payout in camt.054.** The bank study lists a "Vipps settlement" credit as
  exception case n, a camt-only non-KID line, as if it will appear in camt.054; the Vipps
  study marks camt.054 versus camt.053 delivery of that credit **UNCERTAIN** and defers to
  the bank study, which gives no source for it. DNB's Total payment and eGiro agreements
  carry payments without KID (§3.2), which suggests camt.054, but no bank page was read
  that says so.
- **Kontantsalg.** The Vipps study says the legal line between an on-site Vipps payment
  and kontantsalg is the law study's; the law study does not treat it. The addendum's
  study (§9) closes the gap.
- **The payer's order date.** The law study marks its availability **UNCERTAIN** for
  camt.054; the bank study shows OCR carries it (`Oppdragsdato`, item 2 pos 42-47) and camt
  only optional settlement/acceptance dates.
- **Overpayment in phase 4.** The roadmap and the reference page disagree (§6 item 2).

## 8. Sources

### 8.1 The law study (all read 2026-10-06 unless stated)

Primary:

- [FRL] Forsinkelsesrenteloven LOV-1976-12-17-100 — https://lovdata.no/dokument/NL/lov/1976-12-17-100
- [INKL] Inkassoloven LOV-1988-05-13-26 — https://lovdata.no/dokument/NL/lov/1988-05-13-26
- [INKF] Inkassoforskriften FOR-1989-07-14-562 (last amended FOR-2025-12-19-2709) — https://lovdata.no/dokument/SF/forskrift/1989-07-14-562
- [NYINKL] New inkassolov LOV-2026-05-22-19 — https://lovdata.no/dokument/LTI/lov/2026-05-22-19
- [PROP3L] Prop. 3 L (2025–2026), ch. 12, 13, 18 and 34 — https://www.regjeringen.no/no/dokumenter/prop.-3-l-20252026/id3121729/
- [INNST] Innst. 192 L (2025–2026) — https://www.stortinget.no/no/Saker-og-publikasjoner/Publikasjoner/Innstillinger/Stortinget/2025-2026/inns-202526-192l/?all=true
- [R2026H2] FOR-2026-06-25-1372 — https://lovdata.no/dokument/LTI/forskrift/2026-06-25-1372
- [R2026H1] FOR-2025-12-18-2658 — https://lovdata.no/dokument/LTI/forskrift/2025-12-18-2658
- [R2025H2] FOR-2025-06-23-1321 — https://lovdata.no/dokument/LTI/forskrift/2025-06-23-1321
- [R2025H1] FOR-2024-12-19-3279 — https://lovdata.no/dokument/LTI/forskrift/2024-12-19-3279
- [R2024H2] FOR-2024-06-26-1320 — https://lovdata.no/dokument/LTI/forskrift/2024-06-26-1320
- [R2024H1] FOR-2023-12-14-2043 — https://lovdata.no/dokument/LTI/forskrift/2023-12-14-2043
- [R2023H2] FOR-2023-06-22-1075 — https://lovdata.no/dokument/LTI/forskrift/2023-06-22-1075
- [DELEG] FOR-2025-06-18-1069 — https://lovdata.no/dokument/LTI/forskrift/2025-06-18-1069
- [RETN] Finansdepartementet's guidelines, FOR-2013-06-26-756 — https://lovdata.no/dokument/INS/forskrift/2013-06-26-756
- [P150] Prop. 150 L (2011–2012), ch. 7 and 12 — https://www.regjeringen.no/no/dokumenter/prop-150-l-20112012/id700167/?ch=7
- [IS2026] FOR-2025-12-19-2709 — https://lovdata.no/dokument/LTI/forskrift/2025-12-19-2709
- [IS2019] FOR-2018-12-20-2050 — https://lovdata.no/dokument/LTI/forskrift/2018-12-20-2050
- [IF2020] FOR-2020-06-19-1248 — https://lovdata.no/dokument/LTI/forskrift/2020-06-19-1248
- [IF2025] FOR-2024-10-11-2452 — https://lovdata.no/dokument/LTI/forskrift/2024-10-11-2452
- [FT-IS] Finanstilsynet, "Inkassosatsen i 2026 blir 750 kroner" (published 2026-01-06 per page metadata) — https://www.finanstilsynet.no/nyhetsarkiv/nyheter/2026/inkassosatsen-i-2026-blir-750-kroner/
- [FT-OV] Finanstilsynet, "Oversikt over utenrettslige inndrivingskostnader" — https://www.finanstilsynet.no/forbrukerinformasjon/inkassovirksomhet/oversikt-over-utenrettslige-inndrivingskostnader/
- [FAL] Finansavtaleloven LOV-2020-12-18-146 — https://lovdata.no/dokument/NL/lov/2020-12-18-146
- [DL] Domstolloven §§ 148–149 — https://lovdata.no/dokument/NL/lov/1915-08-13-5/KAPITTEL_8
- [MVAL] Merverdiavgiftsloven § 4-1 (2) — https://lovdata.no/lov/2009-06-19-58/§4-1
- [MVAH] Skatteetaten, Merverdiavgiftshåndboken 2025, ch. 3 and 4 — https://oppslag.rettskilder.skatteetaten.no/rettskilder2/type/handboker/merverdiavgiftshandboken/gjeldende/MVA2025_M-4
- [BOKL/BOKF] Bokføringsloven § 10; bokføringsforskriften §§ 5-1-1, 5-12 (read 2026-10-05) — https://lovdata.no/dokument/NL/lov/2004-11-19-73 , https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558
- [FINKN-2025-240] FinKN Inkasso 2025-240 (4.3.2025) — https://publisering.finkn.no/api/statements/openPDF/2025-240
- [FINKN-2023-845] FinKN Inkasso 2023-845 (27.11.2023) — https://publisering.finkn.no/api/statements/openPDF/2023-845
- [FINKN-SMS] FinKN, "Er faktura på SMS tilstrekkelig?" (FinKN 2017-492) — https://www.finkn.no/nyheter/faktura-pa-sms
- [SOM] Sivilombudet, eget tiltak 2019/3663 (10.12.2019) — https://www.sivilombudet.no/uttalelser/eget-tiltak-kemneren-i-grenlands-rutiner-ved-innkreving-for-kommunen/

Secondary:

- Finans Norge, "Hva betyr den nye inkassoloven for inkassoforetak?" — https://www.finansnorge.no/bransjer/inkasso/inkassoloven/hva-betyr-den-nye-inkassoloven-for-inkassoforetak/
- PowerOffice, Kredinor extension — https://www.poweroffice.no/utvidelser/Kredinor
- Kredinor, integration with Dynamics 365 — https://www.kredinor.no/kredinors-integrasjon-med-dynamics-365/

### 8.2 The bank-file study (all read 2026-10-06)

Primary:

- MPS, Systemspesifikasjon OCR giro v4.0 (2018) — https://www.mastercardpaymentservices.com/media/ruqn3ort/ocr-systemspesifikasjon_no_mps.pdf
- MPS, Brukerhåndbok innbetalingstjenestene (Aug 2021) — https://www.mastercardpaymentservices.com/media/un5kqjll/brukerhaandbok-innbetalingstjenestene_no_mps.pdf
- MPS, Innbetaling Total Implementeringsguide (March 2021) — https://www.mastercardpaymentservices.com/media/psohuf0f/innbetaling-total-implementeringsguide_eng_mps.pdf
- Nets/MPS, Forklaring til innholdet i eksempelfilen CAMT 54 K — https://www.nets.eu/no-nb/SiteCollectionDocuments/Innbetaling%20Total/Forklaring%20til%20Innholdet%20i%20eksempelfilen%20CAMT%2054%20K.pdf
- DNB ERP newsletter (2025) — https://content.dnb.no/docs/9485434/dnb-nyhetsbrev-erp.pdf
- DNB ERP and integration page — https://www.dnb.no/en/business/help-and-guidance/erp-and-integration
- DNB MIG zip (camt.054.001.02 v2.0, camt.053.001.02 v2.0, files dated 2026-03-10) — https://bc.dnb.no/original/gallery/10314/files/original/ae33a032-c812-4330-ba8c-ef631b4384bf.zip
- DNB payments price list — https://www.dnb.no/en/business/daily-banking/payments/payments-price-list
- Nordea CAAR MIG camt.054.001.02 v1.9 (2022) — https://www.nordea.com/en/doc/nordea-caar-camt.054.001.02-credit-notification.pdf
- Nordea Corporate eGateway MIG camt.054.001.02 v1.5 (2020-11-30) — https://nordea.com/en/doc/mig-camt-054-001-02-credit-v-1-5-2020-11-30-v-02.pdf
- Nordea OCR page — https://www.nordea.no/bedrift/vare-produkter/betalinger/inn-og-utbetalingstjenester/ocr.html ; price list — https://www.nordea.no/bedrift/priser/prisliste-innbetalingstjenester.html
- SpareBank 1 file-transfer help — https://www.sparebank1.no/nb/bank/bedrift/kundeservice/bm-kort-betaling/hjelpesider-filoverforing.html
- Danske Bank camt.054 examples and the Norway credits example — https://danskeci.com/ci/transaction-banking/instructions/iso-20022-xml/examples-debit-credit-notification ; https://danskeci.com/-/media/pdf/danskeci-com/iso-20022-xml/camt054_example_creditsonly_no.xml?rev=669b58804ccb49ef8ea77306f72e625e&hash=F0D61F6AEDE6CF0D38A1460190BF9C06
- Bits rundskriv 12/22 (01.12.2022) — https://bits.no/document/bits-rundskriv-2022-nr-12-ny-versjon-av-bits-iso-20022-kunde-bank-message-implementation-guidelines-3/ ; Bits ISO 20022 page — https://www.bits.no/en/bank/iso-20022/ ; Bits standards portal (login, not read) — https://www.bits-standards.org/
- ISO 20022 message archive, terms of use, IPR policy — https://www.iso20022.org/catalogue-messages/iso-20022-messages-archive , https://www.iso20022.org/terms-use , https://www.iso20022.org/intellectual-property-rights
- ISO 20022 XSDs — https://www.iso20022.org/message/12746/download , https://www.iso20022.org/message/12776/download , https://www.iso20022.org/message/12706/download , https://www.iso20022.org/message/12736/download

Secondary:

- moov-io/fednow20022 camt.054.001.08 models — https://github.com/moov-io/fednow20022/blob/master/gen/camt_054_001_08/models.go
- moov-io/iso20022 — https://pkg.go.dev/github.com/moov-io/iso20022 ; anyfin/ocrline — https://pkg.go.dev/github.com/anyfin/ocrline ; otovo/python-netsgiro — https://github.com/otovo/python-netsgiro
- Tripletex help (ukjent KID) — https://hjelp.tripletex.no/hc/no/articles/4819025452433-Hvorfor-f%C3%A5r-jeg-innbetalinger-med-ukjent-KID-og-hvordan-l%C3%B8ser-jeg-det
- Fiken help — https://hjelp.fiken.no/hvordan-registrere-betaling-som-ikke-stemmer-med-faktura
- DNB Regnskap help (22/08/2024) — https://hjelp.dnbregnskap.dnb.no/no/article/manuell-handtering-av-innbetalinger-1vxfujo/
- PowerOffice help (OCR import feiler) — https://hjelpesenter.poweroffice.no/ocr-import-feiler
- Visma eAccounting help (not reachable; search snippet only) — https://help.visma.net/no_no/eaccounting/content/online-help/cashbank-match-transactions-incoming-payments.htm
- Aritma on Direkte remittering — https://www.aritma.com/direct-remittance-is-ending

### 8.3 The Vipps study (all read 2026-10-06)

Primary, developer.vippsmobilepay.com (pages show "Last updated" dates from 2026):

- S1 ePayment API overview — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/
- S2 core concepts — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/concepts.md
- S3 create — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/operations/create.md
- S4–S9 capture, cancel, refund, get, event log, force approve — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/operations/ (capture.md, cancel.md, refund.md, get_info.md, get_event_log.md, force-approve.md)
- S10 long-living payments — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/features/long-living-payments.md
- S11 errors and rate limits — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/errors.md
- S12 ePayment webhooks — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/webhooks.md
- S13 terms — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/terms.md
- S14 checklist — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/checklist.md
- S15 changelog — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/CHANGELOG.md
- S16 in person — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/features/customer-present-payments.md
- S17 QR — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/features/qr-payments.md
- S18 ePayment OpenAPI v1.8.5 — https://developer.vippsmobilepay.com/redocusaurus/epayment-swagger-id.yaml
- S19 Access Token API — https://developer.vippsmobilepay.com/docs/APIs/access-token-api/README.md (and standard-authentication.md, access-token_faq.md)
- S20 Access Token OpenAPI — https://developer.vippsmobilepay.com/redocusaurus/access-token-swagger-id.yaml
- S21 HTTP headers — https://developer.vippsmobilepay.com/docs/knowledge-base/http-headers.md
- S22 API keys — https://developer.vippsmobilepay.com/docs/knowledge-base/api-keys.md
- S23 partner keys — https://developer.vippsmobilepay.com/docs/partner/partner-keys.md
- S24 Webhooks API guide — https://developer.vippsmobilepay.com/docs/APIs/webhooks-api/api-guide.md
- S25 webhook request authentication — https://developer.vippsmobilepay.com/docs/APIs/webhooks-api/request-authentication.md
- S26 webhooks FAQ — https://developer.vippsmobilepay.com/docs/APIs/webhooks-api/faq.md
- S27 webhook events — https://developer.vippsmobilepay.com/docs/APIs/webhooks-api/events.md
- S28 Webhooks OpenAPI — https://developer.vippsmobilepay.com/redocusaurus/webhooks-swagger-id.yaml
- S29 polling guidelines — https://developer.vippsmobilepay.com/docs/knowledge-base/polling-guidelines.md
- S30 timeouts — https://developer.vippsmobilepay.com/docs/knowledge-base/timeouts.md
- S31 reserve and capture — https://developer.vippsmobilepay.com/docs/knowledge-base/reserve-and-capture.md
- S32 orderId / reference — https://developer.vippsmobilepay.com/docs/knowledge-base/orderid.md
- S33 payment rules — https://developer.vippsmobilepay.com/docs/knowledge-base/payment-rules.md
- S34 payment description — https://developer.vippsmobilepay.com/docs/knowledge-base/transactiontext.md
- S35 test environment — https://developer.vippsmobilepay.com/docs/knowledge-base/test-environment.md
- S36 business portal — https://developer.vippsmobilepay.com/docs/knowledge-base/portal.md
- S37 applying for services — https://developer.vippsmobilepay.com/docs/knowledge-base/applying-for-services.md
- S38 settlements — https://developer.vippsmobilepay.com/docs/knowledge-base/settlements.md
- S39 Report API — https://developer.vippsmobilepay.com/docs/APIs/report-api/README.md (and api-guide/overview.md, fetching-report-data.md, entry-types.md, settlement-process.md, report-api-faq.md)
- S40 Sales API — https://developer.vippsmobilepay.com/docs/APIs/sales-api/api-guide.md
- S41 eCom API intro and changelog — https://developer.vippsmobilepay.com/docs/APIs/ecom-api/README.md , https://developer.vippsmobilepay.com/docs/APIs/ecom-api/CHANGELOG.md
- S42 QR API and knowledge-base QR — https://developer.vippsmobilepay.com/docs/APIs/qr-api/api-guide/one-time-payment.md , https://developer.vippsmobilepay.com/docs/APIs/qr-api/api-guide/merchant-redirect.md , https://developer.vippsmobilepay.com/docs/knowledge-base/qr-code.md
- S43 landing page — https://developer.vippsmobilepay.com/docs/knowledge-base/landing-page.md
- S44 across borders — https://developer.vippsmobilepay.com/docs/knowledge-base/across-borders.md
- S45 ePayment FAQ — https://developer.vippsmobilepay.com/docs/APIs/epayment-api/faq.md
- S46 site index — https://developer.vippsmobilepay.com/llms.txt

Commercial pages, read through a fetch summarizer:

- S47 pricing — https://vippsmobilepay.com/nb-NO/pricing , https://vippsmobilepay.com/nb-NO/betalingslosninger
- S48 Betalingslenker — https://vippsmobilepay.com/nb-NO/betalingslenker
- S49 help centre, eFaktura in Vipps — https://help.vippsmobilepay.com/nb-NO/articles/why-am-i-receiving-efaktura

Secondary:

- X1 Mastercard Payment Services, eFaktura B2C (search result, not opened) — https://www.mastercardpaymentservices.com/norway/kundeservice/efaktura-b2c/
- X2 Conta, "eFaktura – slik kan kundene betale med Vipps" (dated 2026-08-20) — https://conta.no/fakturering/slik-kan-kundene-betale-med-vipps/
- X3 Norkred, "Vipps eFaktura" (search result) — https://www.norkred.no/fagblogg/vipps-efaktura
- X4 Stripe Vipps docs (search result) — https://docs.stripe.com/payments/vipps
- X5 Nexi Checkout, Vipps (search result) — https://developer.nexigroup.com/nexi-checkout/en-EU/docs/vipps/

### 8.4 The code study

No external sources. The repository at `0ba2840e`, read-only; every `file:line` in §5 was
checked against that commit when this document was assembled.

## 9. Addendum — the quick invoice and kontantsalg

A fifth study, made after §§1–8 were assembled, answers §7 item 41: where an on-site
quick invoice paid by Vipps stops being a credit sale. Every source was read on
**2026-10-06**; tags as in §2 (**LAW**, **ADMIN** for Skatteetaten's and
Skattedirektoratet's statements). It is not legal advice.

### 9.1 Verdict

- **The roadmap's premise is wrong.** `ROADMAP.md:866-868` says the quick invoice "is a
  credit sale — the invoice is the document and the payment follows it". Kontantsalg is
  defined by **when and how the payment is settled**, not by the document: issuing an
  invoice does not turn a sale paid at delivery into a credit sale.
- **(a) Paid later — a credit sale.** A quick invoice paid by bank with its KID, or
  through the pay link after the tradesperson has left, is not settled "ved levering":
  an ordinary credit sale on the customer ledger (bokføringsforskriften § 3-1 første
  ledd nr. 3). No kassasystem.
- **(b) Paid on site by Vipps QR — kontantsalg.** Vipps at delivery is "kontanter" in
  the regulation's sense, so the sale is kontantsalg. It is lawful **without** a
  kassasystem only under the **kontantfaktura** exemption, § 5-4-1 tredje ledd, which
  Skattedirektoratet reads narrowly (§9.3).
- **(c) The safe middle ground.** Offer on-site payment only through a flow that
  enforces the kontantfaktura conditions — the buyer named with an address or an
  organisation number before any payment starts, every payment tied to one full
  delkapittel 5-1 invoice in the ordinary series, no anonymous or receipt-only mode, a
  notice to the user — because Skatteetaten makes that a condition for **the supplier**
  of the software to stay outside kassasystemlova (§9.4).

### 9.2 Kontantsalg, and why Vipps counts

- **Bokføringsforskriften § 5-3-1** (FOR-2004-12-01-1558, last amended by
  FOR-2026-09-29-1933): "a. kontantsalg: salg av varer og tjenester der kjøpers
  betalingsforpliktelse overfor selger gjøres opp ved levering, ved bruk av betalingskort
  eller kontanter som betalingsmiddel. Salg over internett eller ved oppkrav anses ikke
  som kontantsalg, b. betalingskort: debetkort, kredittkort og faktureringskort, c.
  kontanter: andre betalingsmidler enn betalingskort". **LAW** [BOKF-5]
- **Kassasystemlova § 2 a–c** (LOV-2015-06-19-58) has the same definitions in nynorsk.
  **LAW** [KASL]
- **Bokføringsloven § 10 a**: kontantsalg is registered and documented in a kassasystem
  with a produkterklæring; the ministry may set exemptions by regulation. **LAW** [BOKL]
- **Vipps is "kontanter".** Skattedirektoratet's prinsipputtalelse on internet sales
  (29.06.2017), point 2, names app payments such as Vipps and MobilePay among the
  "andre betalingsmidler" of § 5-3-1 c; its point 3.3 treats an order paid by Vipps and
  then handed over as ordinary kontantsalg needing a kassasystem. **ADMIN** [SKD-2017]
- Only kontantsalg must be registered in a kassasystem (prinsipputtalelse on credit
  sales, 15.09.2016). **ADMIN** [SKD-2016]

### 9.3 The duty, and the one exemption that fits

- **§ 5-3-2 første ledd**: kontantsalg is registered continuously in a kassasystem with a
  produkterklæring "med mindre annet er bestemt i denne forskrift"; § 5-3-3 allows only
  declared systems. **LAW**
- **Delkapittel 5-4** lists the exemptions. § 5-4-1 (1): ambulant or sporadic kontantsalg
  up to 3G a year, and kontantsalg up to NOK 50 000 excl. VAT a year (the latter "fra fast
  forretningssted", prinsipputtalelse 11.11.2020 [SKD-2020]) — both depend on the user's
  own turnover, so a product cannot rely on them. § 5-4-1 (2): pre-numbered tickets.
  **§ 5-4-1 (3): "Kontantsalg kreves ikke registrert i et kassasystem dersom den
  bokføringspliktige utsteder salgsdokument (kontantfaktura) i samsvar med delkapittel
  5-1."** §§ 5-4-2 to 5-4-4 (events, vending machines, unstaffed points) do not fit;
  § 5-4-5 requires a daily count of the till against the sales documentation "ved salg
  som nevnt i § 5-4-1 og § 5-4-2"; § 5-4-7 is the ministry's dispensation. There is **no**
  exemption for "sale against invoice" or for business buyers as such. **LAW** [BOKF-5]
- FOR-2026-09-29-1933 (in force 2027/2028/2030) does not touch delkapittel 5-3 or 5-4.
  **LAW** [FOR-1933]
- **Skattedirektoratet's narrow reading** (prinsipputtalelse "Bruk av kontantfaktura som
  alternativ til kassasystem", 07.01.2019) [SKD-2019], paraphrased:
  - the ministry proposed abolishing § 5-4-1 (3) in 2018 (save for health services); the
    proposal was not adopted, so the exemption stands;
  - the wording has no limit, but the heading, its place and the preparatory works call
    for a narrow reading;
  - it is for businesses whose ordinary invoicing identifies the customer — "virksomheter
    som regelmessig har kredittsalg hvor kunden faktureres i ettertid" — with health
    practitioners, car dealers and workshops, hotels and airlines as examples, and
    occasional sales of other goods by such a business covered too;
  - it does not cover regular retail or service businesses that set up a solution
    registering personal data at each cash sale; a business that also runs a counter,
    restaurant or bar uses a kassasystem for that part;
  - the kontantfaktura must hold everything a credit invoice holds, including the buyer's
    name and address or organisation number (§ 5-1-2), and must be used for **every**
    kontantsalg, whatever the amount;
  - wrongful use, or a kontantfaktura lacking required content, breaches bokføringsloven
    § 10 a, sanctioned by an overtredelsesgebyr (skattebetalingsloven § 14-7 (1) d).
  **ADMIN**

### 9.4 The supplier's side

- Skatteetaten's "Spørsmål og svar om nye kassasystemer": "For ikke å komme inn under
  kassasystemlova må leverandør sørge for at systemet er oppbygd slik at det ikke kan
  registreres kontantsalg på systemet uten at kjøpers navn og adresse blir registrert."
  **ADMIN** [SKE-QA]
- Kassasystemlova §§ 1 and 2 e cover suppliers ("leverandørar"); § 5 requires the
  produkterklæring; § 8 sets a breach fee of 30 rettsgebyr for an undeclared or
  non-compliant system. **LAW** [KASL]
- So a Vantigo that let a user take an on-site payment **without** the buyer's name and
  address would expose the user (§ 10 a) and Vantigo itself (an undeclared kassasystem).
  The buyer-identity rule is a product requirement, not advice.

### 9.5 Timing and channel

| Scenario | Classification | Basis |
|---|---|---|
| The customer scans the Vipps QR on site, at or right after completion | kontantsalg | § 5-3-1 a, c; [SKD-2017] 3.3 |
| A card payment on site (terminal or SoftPOS) | kontantsalg | § 5-3-1 a, b |
| The customer pays later by bank with the KID | credit sale | not settled "ved levering"; [SKD-2016] |
| The customer pays later through the pay link, after the tradesperson has left | credit sale | the same; **UNCERTAIN** at the edges |
| The customer pays while the tradesperson waits, or as a condition of handover | probably kontantsalg in substance | **UNCERTAIN**: no source defines the window of "ved levering" |

- **The internet-sale carve-out** is limited to sales ordered and paid through the
  seller's own online solution without visiting the seller's place of business, on the
  customer's own equipment ([SKD-2017] 3.1–3.2). A face-to-face QR at the job site is
  closest to its point 3.3 example, kontantsalg. Do not rely on the carve-out for a pay
  link opened on site. Whether working at the customer's home rather than the seller's
  premises changes this is unaddressed. **ADMIN / UNCERTAIN**
- "Oppkrav" (cash on delivery) means collection by a carrier; not relevant.
- Restricting on-site payment to business buyers is **not** required: the exemption
  applies to identified private buyers too, and the restriction would add no safety.

**The requirements the study derives for an on-site payment path** (the first three
from §§9.3–9.4, the last recommended): (1) the buyer's name with an address or an
organisation number is mandatory before a payment can start, with no "kontantkunde" and
no exception; (2) every on-site payment is tied 1:1 to a full § 5-1-1 invoice numbered in
the ordinary series; (3) no receipt-only or anonymous sale mode exists anywhere in the
product until a counter-sale module ships with a declared kassasystem; (4) a notice or an
acknowledgement that on-site payment is kontantsalg, allowed without a kassasystem only
for a business that mainly sells on credit to identified customers, and that a shop or
counter sale needs a kassasystem; (5) the time of each payment recorded and a daily list
of on-site payments for reconciliation against the Vipps settlement — the preparatory
works quoted in [SKD-2019] mention the time of day and a daily report, and § 5-4-5's
daily count refers to "salg som nevnt i § 5-4-1", which arguably includes kontantfaktura
sales (**UNCERTAIN** whether required; build it in).

### 9.6 UNCERTAIN items

1. No primary source names håndverkere, or "a Vipps payment of an invoice at delivery";
   the verdict applies the 2017 and 2019 statements to these facts.
2. The time window of "ved levering": a payment later the same day, or a pay link used
   while the tradesperson is still there. Treat any payment awaited at, or required for,
   handover as kontantsalg.
3. Whether the internet-sale carve-out could ever cover a QR or pay link opened on site —
   probably not ([SKD-2017] 3.3); not relied on.
4. Whether § 5-4-5's daily reconciliation, and the preparatory works' time of day and
   daily report, bind kontantfaktura users — plausible.
5. The user's other kontantsalg is outside Vantigo's control: a user who also sells over
   a counter needs a kassasystem for that part (delt virksomhet, [SKD-2019]).
6. NRS bokføringsstandard GBS 16 (internet sales; advance, cash and credit), which
   Skatteetaten cites, was not read.
7. The 2026 value of 3G was not checked; it does not bear on the design.
8. [SKD-2019] dates the regulation amendment it discusses "14. desember 2019"; the
   Lovdata amendment notes show it is FOR-2018-12-14-1983 — a typo in the statement.

### 9.7 Sources (all read 2026-10-06)

Primary:

- [BOKF-5] Bokføringsforskriften FOR-2004-12-01-1558, kapittel 5 — https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558/KAPITTEL_5
- [BOKL] Bokføringsloven LOV-2004-11-19-73 § 10 a — https://lovdata.no/dokument/NL/lov/2004-11-19-73
- [KASL] Kassasystemlova LOV-2015-06-19-58 — https://lovdata.no/dokument/NL/lov/2015-06-19-58
- [FOR-1933] FOR-2026-09-29-1933 — https://lovdata.no/dokument/LTI/forskrift/2026-09-29-1933
- [SKD-2016] Skattedirektoratet, prinsipputtalelse "Nye krav til kassasystemer – registrering av kredittsalg" (15.09.2016) — https://www.skatteetaten.no/rettskilder/type/uttalelser/prinsipputtalelser/nye-krav-til-kassasystemer--registrering-av-kredittsalg/
- [SKD-2017] Skattedirektoratet, prinsipputtalelse "Internettsalg etter kassasystemlova og bokføringsforskriften" (29.06.2017) — https://www.skatteetaten.no/rettskilder/type/uttalelser/prinsipputtalelser/internettsalg-etter-kassasystemlova-og-bokforingsforskriften/
- [SKD-2019] Skattedirektoratet, prinsipputtalelse "Bruk av kontantfaktura som alternativ til kassasystem" (07.01.2019) — https://www.skatteetaten.no/rettskilder/type/uttalelser/prinsipputtalelser/bruk-av-kontantfaktura-som-alternativ-til-kassasystem/
- [SKD-2020] Skattedirektoratet, prinsipputtalelse "Unntak for krav til kassasystem ved lavt kontantsalg" (11.11.2020) — https://www.skatteetaten.no/rettskilder/type/uttalelser/prinsipputtalelser/unntak-for-krav-til-kassasystem-ved-lavt-kontantsalg/
- [SKE-QA] Skatteetaten, "Spørsmål og svar om nye kassasystemer" — https://www.skatteetaten.no/bedrift-og-organisasjon/starte-og-drive/rutiner-regnskap-og-kassasystem/kassasystem/sporsmal-og-svar-om-nye-kassasystemer/
