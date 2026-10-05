# Invoices phase 3 — Work becomes invoices — research

Research for Invoices phase 3 (`ROADMAP.md:817-829`): the Norwegian rules that bind an
invoice built from hours, expenses and milestones, an inventory of the three source
modules and of the invoices module's own seams, and the write-back the roadmap calls "a
third sanctioned cross-module write direction". It builds on the 1A research
(`docs/superpowers/research/2026-09-26-invoices-module.md`, "1A") and the phase 2 research
(`docs/superpowers/research/2026-10-03-invoices-ehf-peppol-kid.md`, "P2"), and says where
it corrects them. External sources were read on **2026-10-05**: statutes and regulations
on Lovdata, Skatteetaten's handbook and statements (administrative practice), Norsk
RegnskapsStiftelse's GBS standards (authoritative practice, not law), vendor pages
(secondary only). Anything not confirmed from a primary source is marked **UNCERTAIN**.
Code references are `file:line` against `main` at `361cdff6`; `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`, `MB` the contributing page
`docs/src/content/docs/en/contributing/module-boundaries.md`, and `R/` the reference pages
under `docs/src/content/docs/en/reference/`.

## 1. Why now

### 1.1 Three roadmaps end at this phase

- **Time.** "What invoicing will read" (`R/time.md:369-393`): approved, billable entries
  with a bill rate, `hours × billRate × billMultiplierPercent / 100` "rounded once per
  invoice line", grouped by project, trackable code `<project>-<line>` and work type. The
  `invoiced` status and `invoiced_at` exist "for the future Invoices module to use", and
  nothing writes them (`R/time.md:198-201`).
- **Expenses.** "Next: invoicing": the stamp is set by hand, per line; "ready to invoice"
  is the list an Invoices module would build from, and "that module owns the stamp when it
  arrives" (`ROADMAP.md:727-732`; `R/expenses.md:1266-1271`).
- **Projects.** Milestones move `planned → ready → invoiced` by hand, and "a later Invoices
  module will set the same status" (`ROADMAP.md:533-537`; design E5,
  `docs/superpowers/specs/2026-09-19-project-economy-design.md:51-53`); a billing
  milestone "is what Invoices turns into an invoice line; a-konto is one kind"
  (`docs/superpowers/specs/2026-09-18-project-management-plan.md:134`).

The roadmap's phase 3 lists an uninvoiced view per customer and project, a grouping
wizard with an optional timesheet, expense markup, milestone and a-konto invoices with a
final settlement, and the write-back "inside the issue transaction", which "gets its own
contract design under [module-boundaries] before any code" (`ROADMAP.md:817-829`). 1A
named the prerequisites: "the rule-8 design for a third write direction, line-level
reads, the one expenses predicate, and a stamp for Time, which has no writer"
(1A `:683-693`).

### 1.2 The 2027 context, and a new regulation

- **The B2B sending duty starts 2027-01-01** (P2 §1.1). Phase 2 made an invoice for
  consultancy hours sendable as EHF; phase 3 makes it buildable from the work.
- **FOR-2026-09-29-1933** (kunngjort 29.09.2026,
  https://lovdata.no/dokument/LTI/forskrift/2026-09-29-1933) amends bokføringsforskriften.
  **(Correction to P2 §1.1,** which recorded the format regulation as unpublished on
  2026-10-03.)
  - **§ 5-2-9 second paragraph, from 2027-01-01**: approved electronic-invoice standards
    are EHF Fakturering, Peppol BIS Billing, EHF Selvfakturering and Peppol BIS
    Self-Billing, "alle i versjon 3.0 eller nyere"; until 2029-12-31 the parties may agree
    another electronic format.
  - **§ 5-1-2 from 2028-01-01**: "Ved salg til bokføringspliktig kjøper skal kjøpers
    organisasjonsnummer alltid angis."
  - **§ 5-2-1 fourth paragraph from 2028-01-01**: "Salgsdokument skal oversendes
    bokføringspliktig kjøper."
  - A new **§ 1-2** exemption for turnover up to NOK 50 000, from 2027.

  None is specific to phase 3, but the 2028 rule tightens the module's `buyer_incomplete`
  check, whose `buyerComplete` accepts an organisation number **or** a complete address
  (`srv/invoices/issue.go:134-146`) — §11, item 13.
- **Bokføringsloven § 10 is amended by lov 19. juni 2026 nr. 39 from 2027-01-01**
  (Lovdata's note on § 10); the new text was not read — **UNCERTAIN (U8)** whether the
  "referanse fra primærdokumentet" sentence §2.1 relies on changes.
- **GBS 1 Kreditnota was re-issued 22 Jan 2026** (§5). **GBS 10 is on hearing** to
  30 Nov 2026, "Det materielle innholdet videreføres i hovedsak"
  (https://www.regnskapnorge.no/faget/artikler/bokforing/horing-om-endringer-i-gbs-9-10-og-13);
  the 2015 version is used below.

### 1.3 What phases 1 and 2 cannot do

A line references nothing outside the module but a VAT code (`srv/invoices/drafts.go:82-89`),
and the document knows only an opaque `customer_id` (`mig/00034_invoices_baseline.sql:104`).
The three source modules each keep a manual stamp with a free-text reference (§7.2–7.4),
so an hour can be billed twice or never, and neither side can say which invoice billed it.

## 2. What the law requires of an invoice for hours and services

### 2.1 Content, and how far hours may be grouped

- **Bokføringsforskriften § 5-1-1** requires "3. ytelsens art og omfang" and "4. tidspunkt
  og sted for levering av ytelsen"
  (https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558). Nothing in kap. 5-1 requires
  a line per hour, person or day.
- Skatteetaten, 31 Oct 2011: a fairly general description may be used "når tjenesten i
  utgangspunktet er definert og avgrenset"; where art and omfang are specified in a
  contract or another document, the invoice must refer to it
  (https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/angivelse-av-art-i-salgsdokumentet-med-videre/).
- **Bokføringsloven § 10**: "Dersom dokumentasjonen består av flere dokumenter, skal det
  være referanse fra primærdokumentet til øvrige dokumenter."
  (https://lovdata.no/dokument/NL/lov/2004-11-19-73)
- So hours **may be grouped** per project, work type, person or period, if the line states
  the nature of the work, the hours and the price and the document states the period; a
  grouped line that relies on a timesheet for its "omfang" references it. PowerOffice Go
  groups "Total", "Per ansatt" or "Per aktivitet"
  (https://hjelpesenter.poweroffice.no/timeinnstillinger-oversikt, secondary); 1A found
  the same shapes everywhere (1A `:409-438`).
- **UNCERTAIN (U2):** no Skatteetaten statement says hours may be one total line with no
  breakdown when no timesheet is sent.

### 2.2 The delivery period

- Services delivered over time state the period (§ 5-1-1 nr. 4); BG-14
  `cac:InvoicePeriod` is "Also called delivery period"
  (https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/cac-InvoicePeriod/).
  **UNCERTAIN (U1)** that a period satisfies "tidspunkt" — practice, no statement read.
- A line may carry its own period (BG-26). **PEPPOL-EN16931-R110/R111** (fatal): it "MUST
  be within invoice period"; **BR-29/30**: end ≥ start; **BR-CO-19/20**: start or end
  filled (https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/ ,
  https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-tc434/).
- Today a draft may hold neither a date nor a period, an issued document must
  (`mig/00034:106-109`, `:175-180`; refusal `delivery_date_missing`,
  `srv/invoices/issue.go:40`), and the EHF writes a header period
  (`srv/invoices/ehf/render.go:98-103`) and no line period. A wizard that prefills the
  period from the first and last source date satisfies R110/R111 by construction.

### 2.3 The timesheet is the seller's record, not the sales document

- **§ 5-14**: "Bokføringspliktige som utfører tjenester hvor vederlaget er basert på
  tidsforbruk, skal for hver eier og ansatt dokumentere utførte timer. Timene skal
  spesifiseres pr. dag fordelt på intern tid og på de enkelte kunder eller oppdrag.
  Dokumentasjonen skal være utarbeidet senest innen utløpet av den etterfølgende måned."
  It applies at fixed price too.
- **§ 7-3**: kept **3 years 6 months** after year end — shorter than a sales document's
  5 years. **Construction**: § 8-1-2 "skal føre timelister etter § 5-14"; § 8-1-5 keeps
  project accounts and "timelister og ordrelister" **10 years**.
- **GBS 10** (2015,
  https://www.regnskapsstiftelsen.no/wp-content/uploads/2015/06/2015-04-GBS-10-Dokumentasjon-av-medg%C3%A5tt-tid-oppdatert-22.04.15.pdf):
  for control of completeness and periodisation; per day "som hovedregel"; "Det er ikke
  stilt formkrav"; "Det er ikke krav om å spesifisere arbeidsoppgavene".
- So the timesheet is an obligation of its own and **need not be sent**; sent as the
  specification an invoice relies on, it is referenced (§ 10). Time already keeps hours
  per person, day and project (`mig/00010_time_baseline.sql:6-33`), with a period lock
  that "is how a month gets closed before it is invoiced" (`R/time.md:390`).
- **UNCERTAIN (U3):** whether an attached timesheet inherits the 5-year retention or keeps
  § 7-3's 3 years 6 months. Conservative: 5 (10 in construction).

### 2.4 When to invoice

| Rule (bokføringsforskriften unless noted) | Text | Applies to |
| --- | --- | --- |
| § 5-2-2 | "snarest mulig og senest en måned etter levering" | a discrete job |
| § 5-2-3 | "innen femten virkedager i måneden etter leveringsmåneden" | monthly collective invoicing |
| § 5-2-4 | "Tjenester som leveres løpende … skal faktureres senest en måned etter utløpet av den alminnelige skattleggingsperioden for merverdiavgift"; tender work may follow payment plans unless they "avviker vesentlig fra den reelle fremdriften" | continuous services |
| skatteforvaltningsforskriften § 8-3-1 | the VAT term is two calendar months (https://lovdata.no/forskrift/2016-11-23-1360/§8-3-1) | § 5-2-4's clock |
| § 5-2-5 | metered consumption up to a year | **not** time: "kan f.eks. ikke anvendes generelt for virksomhet som fakturerer basert på medgått tid" (Skattedirektoratet 2 Apr 2024) |

- MVA-håndboken ch. 15 names the case: "Enkelte tjenester leveres fortløpende og
  faktureres basert på medgått tid, f.eks. advokat- og konsulentbistand"
  (https://oppslag.rettskilder.skatteetaten.no/rettskilder2/type/handboker/merverdiavgiftshandboken/gjeldende/MVA2025_M-15).
  Continuous hourly work in January–February is invoiced by **31 March**. There is no
  "every month" rule. The 18 Feb 2025 amendment (nr. 392) changed terminology only
  (https://lovdata.no/forskrift/2025-02-18-392; preamble read through a search summary).
- **No VAT on undone work.** § 5-2-6 allows invoicing ahead only for "persontransport,
  servering, abonnementer, leier, avgifter og lignende"; mval. **§ 15-10 (3)**:
  "Salgsdokumentasjon for en merverdiavgiftspliktig omsetning kan ikke utstedes før ved
  levering av varen eller tjenesten" (https://lovdata.no/dokument/NL/lov/2009-06-19-58).
  An hour dated after the issue date cannot go on a VAT invoice.
- The module already warns `issued_late` against the delivery end
  (`srv/invoices/responses.go:30`); phase 3 can warn on *uninvoiced* work the same way —
  never block.

## 3. A-konto, delfaktura, forskudd and sluttoppgjør

### 3.1 Three different things

| Term | What it is | Salgsdokument? | VAT |
| --- | --- | --- | --- |
| **Rent forskudd** (betalingsanmodning) | money asked before work is done | **No**: "Rene forskudd vil fortsatt verken gi rett eller plikt til å utstede salgsdokument" (Skatteetaten 29 Aug 2022) | none (§ 15-10 (3)); "be om et finansielt forskudd" (https://www.skatteetaten.no/en/rettskilder/type/kunngjoringer/endringer-i-bokforingsforskriften-mv/) |
| **A-kontofaktura / delfaktura** | the part of the work performed in the period | **Yes** | full, in the term the document is issued |
| **Sluttfaktura** | the settlement on completion, less what was invoiced | **Yes** | on the remainder (§3.3) |

1A §2.4 had the shape and left the deduction's presentation as practice (1A `:144-158`).

### 3.2 When the VAT falls due

- **Mval. § 15-9 (1)**: reported "i skattemeldingen for den terminen dokumentasjonen er
  utstedt" — the document date, not the payment.
- **Mvaf. § 15-9-1 (2)**: "Dersom det mottas delbetaling for utført arbeid, skal
  salgsdokument for dette utstedes når delbetalingen mottas"
  (https://lovdata.no/dokument/SF/forskrift/2009-12-15-1540). **§ 15-9-3**: disputed
  amounts under tilvirkningskontrakter are reported when settled or paid.
- **GBS 1 (2026) §1.5, §6.2**: a credit note may not be used to defer VAT on a disputed
  amount.

### 3.3 The sluttfaktura

- **No statute prescribes its form.**
- **GBS 1 (2026) §1.1**: corrections may be made "ved å innta korreksjonene på en senere
  faktura til den samme kjøperen, for eksempel ved at feilaktige fakturalinjer i et
  tidligere salgsdokument krediteres med negativt fortegn", with a credit note's
  references
  (https://www.regnskapsstiftelsen.no/wp-content/uploads/2026/02/2026-01-GBS-1-Kreditnota-%E2%80%93-innhold-utstedelse-bokforing-og-oppbevaring-januar-2026-endringsmarkert-mot-HU-august-2025.pdf;
  **UNCERTAIN (U10)**: read from the tracked-changes PDF).
- **Peppol BIS 3 §5.6**: "Pre-payment (with or without VAT) is settled through a final
  invoice"; **BR-27** forbids only a negative *price*, so a quantity may be negative
  (https://docs.peppol.eu/poacc/billing/3.0/bis/).
- **BG-3 `cac:BillingReference` is 0..n**: "Repeat the cac:BillingReference to add several
  preceding invoice references"
  (https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/cac-BillingReference/).
- **BT-113 `cbc:PrepaidAmount`**, "The sum of amounts which have been paid in advance";
  **BR-CO-16**: Payable = TaxInclusive − Prepaid + Rounding — it lowers the payable, **not
  the VAT base**
  (https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/cac-LegalMonetaryTotal/cbc-PrepaidAmount/).

What follows (the layout is practice):

- A-konto invoices **with VAT** are deducted by a **negative line at the same VAT category
  and rate** ("Fratrukket a konto, faktura nr. N av dato"), and the sluttfaktura
  references each (BG-3). Tripletex: with one rate the VAT nets to the remainder
  (https://hjelp.tripletex.no/hc/no/articles/4406369059985-Slik-a-kontofakturerer-forh%C3%A5ndsfakturerer-du-med-prosjektmodulen,
  secondary). Invoicing only the remaining work is equally lawful; so is crediting each
  a-konto and issuing a full final invoice (Conta's method), with more documents.
- **Pure financial advances** (no VAT) are deducted as `PrepaidAmount`, the sluttfaktura
  carrying full VAT on the whole delivery. Derived from § 15-10 (3), § 15-9, BR-CO-16.
- `PrepaidAmount` for a-konto that **were** VAT invoices double-counts the VAT.
- **UNCERTAIN (U6):** no Skatteetaten wording for the deduction line was found.

### 3.4 Numbering

A-konto invoices and the sluttfaktura are ordinary salgsdokumenter in the **same series**
(§ 5-1-3). GBS 1 §1.5 keeps documents that are not real credit notes out of the
credit-note series; by analogy a payment request should not take an invoice number —
**UNCERTAIN (U5)**: an analogy, and Tripletex numbers a-konto "uten mva" as invoices on
account 2900.

### 3.5 Building and construction: § 8-1-2a

"Salgsdokument kan utstedes i samsvar med bygge- og anleggsarbeidets fremdrift. Når
arbeidet er fullført, skal salgsdokument utstedes senest innen en måned etter utløpet av
den alminnelige skattleggingsperioden for merverdiavgift …", and a received part-payment
matching progress is invoiced on the same clock. Since **1 Jan 2021** progress invoicing
is a right, not a duty; completion triggers "en ubetinget faktureringsplikt"
(MVA-håndboken ch. 15). The duty on a received advance covers only "den delen av
betalingen som tilsvarer verdien av de arbeidene som er utført i perioden" (29 Aug 2022,
https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/utstedelse-av-salgsdokument-i-bygge--og-anleggsvirksomheten--avregning-av-forskudd/).
BFU 1/2026 counts a total contractor's receipts for sub-contracted work as part-payment;
SKNA6-2026-27 treats a distinct completed part as a part-delivery, invoiced within a
month. GBS 1 footnote 5 lets a voluntary progress invoice that is unpaid be credited,
"Det kreves ikke at beløpet er omtvistet." §10 defers all of it.

## 4. Re-billing expenses

### 4.1 Utlegg: outside the VAT base

- **Mval. § 4-1 (2) a**: not consideration is "godtgjørelse for utlegg pådratt i kjøpers
  navn og for kjøpers regning". MVA-håndboken: "Det er en forutsetning at selger bare
  oppkrever det utlagte beløp av kjøper uten påslag og at utlegget regnskapsføres som
  utlegg, ikke omsetning"
  (https://oppslag.rettskilder.skatteetaten.no/rettskilder2/type/handboker/merverdiavgiftshandboken/gjeldende/MVA2025_M-4);
  any fee or kept discount puts it in the base.
- Skatteetaten 23 Mar 2015: the document is issued "i kjøperens navn", forwarded with a
  clear reference, kept apart from the sale
  (https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/krav-til-dokumentasjon-og-bokforing-ved-fellesanskaffelser-og-utlegg/).
- So an utlegg is not a sale: no output VAT, not revenue, a supplier document addressed to
  the customer, totalled apart (§ 5-1-5). **UNCERTAIN (U4):** whether it may sit on the
  same sales document as taxable lines or must be a separate claim.

### 4.2 Viderefakturering: the seller's own cost, re-sold

- **Mval. § 4-2 (1)**: "I beregningsgrunnlaget inngår alle kostnader ved oppfyllelsen av
  avtalen, enten de inngår i vederlaget eller det kreves særskilt betaling".
  MVA-håndboken: "Det er sikker praksis at en ikke kan unngå avgift på slike omkostninger
  ved at de faktureres særskilt"; the rate is "den sats som gjelder for selve leveransen,
  dvs. normalt 25 %".
- **Markup is irrelevant**: "påslag [er] ikke noe vilkår for omsetning" (4 Nov 2024,
  https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/viderefakturering-av-varer-og-tjenester---omsetningsbegrepet--/).
- So a hotel night or a flight (12 %) re-billed as part of a 25 % consulting job is billed
  at **25 %** — the **main supply's** rate, never the receipt's (UiO's VAT guide, citing
  AV 24/82 and AV 10/83,
  https://www.uio.no/for-ansatte/arbeidsstotte/okonomi/andre-okonomiprosesser/mva/avgiftstemaer/utgiftsrefusjon/,
  secondary). **Diett and kilometergodtgjørelse** likewise
  (https://regnskapsguiden.com/moms-pa-kj%C3%B8regodtgj%C3%B8relse/, secondary) —
  **UNCERTAIN (U11):** no administrative text names them.
- **A separately sold good keeps its own rate**: "det enkelte omsetningsobjektet behandles
  hver for seg" (MVA-håndboken). Cost of performance versus separate supply is a judgement
  per contract — **UNCERTAIN** at the edges.
- **Supplier invoices** addressed to the seller, input VAT deducted, are re-sold at the
  main supply's rate, markup or not; only a cost incurred **in the customer's name** is an
  utlegg.

### 4.3 What this means for the data

Expenses holds a `vat_amount` and no VAT code; the bill amount is the net plus markup,
in the entry's own currency (§7.3). The expense's input rate must **never** become the
sales line's: a re-billed cost takes the main supply's code, a separate good its own, an
utlegg none. The expense row holds none of the three facts.

## 5. Credit notes and releasing work

- **§ 5-2-7**: a replacement requires "en kreditnota som reverserer opprinnelig
  salgsdokument". Nothing forbids invoicing the work again after the credit.
- **GBS 1 (2026)**: §1.2 — beyond an over-charge, "utstedes det i tillegg til kreditnotaen
  også et nytt og korrekt salgsdokument til erstatning"; §1.1 — "tilstrekkelig å kreditere
  de aktuelle fakturalinjene og eventuelt fakturere disse på nytt"; §2.2 — a credit that
  partly corrects names **which lines**, and the original need not reference the credit
  note (it is not "retting" under bokføringsloven § 9, so § 5-12 does not apply); §2.3 —
  credit notes in the invoice series "eller … i en egen nummerserie" **(answers 1A's open
  item: a separate series is allowed; the shared one stays lawful)**; §2.4 — same
  currency and amounts; §4.1/4.2 — one month after the **event** causing the credit.
- A reference from the new invoice to the credit note is required by neither; recommended
  for the audit trail — **UNCERTAIN** as a rule.
- **An hour is never on two live invoice lines at once** — no specific rule; it follows
  from bokføringsloven § 4's completeness and accuracy. Crediting releases the work.

Practice (1A `:441-457`): Tripletex frees hours and expenses on a credit automatically,
Fiken asks; Productive and Teamwork reserve a source as soon as it is on a draft.

## 6. EHF fields for projects and periods, and personal data in timesheets

### 6.1 The EHF elements phase 3 touches

From Peppol BIS Billing 3.0, May 2026 (https://docs.peppol.eu/poacc/billing/3.0/bis/,
syntax under https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/):

| Term | UBL | Meaning | Written today |
| --- | --- | --- | --- |
| BT-11 | `cac:ProjectReference/cbc:ID` | the project, 0..1 on an **Invoice** (**R080**: one per document; **UBL-SR-39**). A CreditNote has none: an ADR with `DocumentTypeCode` **50** (BIS §11.3.7) | no |
| BT-12 | `cac:ContractDocumentReference/cbc:ID` | the contract | no |
| BT-19 / BT-133 | `cbc:AccountingCost` | where to book in the **buyer's** accounts — not the seller's project | no |
| BG-14 / BG-26 | `cac:InvoicePeriod` | the delivery period, header and line (R110/R111) | header only (`srv/invoices/ehf/render.go:98-103`) |
| BG-3 | `cac:BillingReference` | 0..n preceding invoices — the a-konto | the credited original only |
| BT-18 / BT-128 | ADR code 130 / line `DocumentReference` | an invoiced object (R100, R101) | no |
| BT-113 | `cbc:PrepaidAmount` | paid in advance | no (`srv/invoices/ehf/render.go:249-254`) |

Under R080 **an invoice spanning two projects can name neither**. 1A recorded R080
(1A `:289-291`); the credit-note variant is new here.

### 6.2 Attachments

- `cac:AdditionalDocumentReference` (BG-24) is **0..n**, described as "an embedded
  document, Base64 encoded (such as a time report)", example file "Hours-spent.csv"
  (https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/cac-AdditionalDocumentReference/).
  `@mimeCode` and `@filename` are mandatory (**UBL-DT-06/07**, fatal); **BR-52**: an ID.
- **PEPPOL-EN16931-CL001** (fatal) allows exactly `application/pdf`, `image/png`,
  `image/jpeg`, `text/csv`, xlsx and ods
  (https://docs.peppol.eu/poacc/billing/3.0/codelist/MimeCode/). **(Answers the
  UNCERTAIN of 1A `:295-297` and P2 §4.1.)** BIS §7.2.10: attachments travel with the
  invoice, and a receiver "shall accept and process" those on the list.
- **No count or size limit in BIS 3**; quoted figures (5, 10, 50, 100 MB) are vendor or
  access-point limits — **UNCERTAIN (U7)** for Storecove.
- Today one ADR carries the stored PDF with `cbc:ID` = the number
  (`srv/invoices/ehf/render.go:117-130`), and Storecove's regeneration of it is still
  unproven (`ROADMAP.md:809-811`).

### 6.3 Personal data in a timesheet

- Naming employees discloses their data to the customer. The basis is normally **GDPR
  art. 6(1)(f)** (documenting the claim) or **6(1)(b)** where the contract names
  specialists, with Datatilsynet's three-part test documented
  (https://www.datatilsynet.no/rettigheter-og-plikter/virksomhetenes-plikter/om-behandlingsgrunnlag/nodvendig-for-a-ivareta-legitime-interesser---interesseavveining/).
  **Art. 5(1)(c)** and **25(2)**: minimise, by default; **art. 13**: tell the employees
  (https://eur-lex.europa.eu/eli/reg/2016/679/oj).
- No rule puts the employee's name on the sales document; § 5-14 is internal.
  PowerOffice Go shows the **employee number by default**, the name optionally (above,
  secondary).
- A time entry's `note varchar(2000)` (`mig/00010_time_baseline.sql:17`) may hold
  special-category data ("sick, left early", art. 9): a timesheet carries a billing
  description, never the note.
- Attached to an issued invoice, names stay for the retention and are not erasable
  (art. 17(3)(b)) — **UNCERTAIN** in the period, as U3.

## 7. Where the codebase stands

### 7.1 The module-boundary rules, and the two sanctioned write directions

- **Rule 3**: "The one non-DTO type is `pgx.Tx`, in `contracts.CustomerReferenceHolder`
  and `contracts.CustomerPersonalData` … the reason is rules 8 and 9's" (`MB:41-46`).
  **Rule 4**: one schema per module, no cross-schema keys or joins (`MB:47-52`),
  enforced by `TestNoModuleReferencesAnotherModulesSchema` (`srv/db/schema_test.go:120`).
- **Rule 8**: customers' merge calls every holder "inside its own transaction, which
  already holds both customer rows locked"; the holder runs its own SQL on its own schema,
  "never begins or ends a transaction, never reads a directory (no in-process lookup
  happens under a lock anywhere in this codebase)", and is idempotent (`MB:81-91`).
  **`MB:92-93`**: "Another write direction needs a design of its own, not a second
  holder-shaped interface — rule 9 is that design for the second."
- **Rule 9**: export outside any customers transaction, erase **inside** the
  anonymisation transaction, "the holder's rules exactly", run "whether or not its module
  is enabled" (`MB:94-113`). Both are enforced "by shape and by test": the caller's
  `pgx.Tx`, a rollback test per holder, SQL in the module's `queries/`, and the caller's
  test that a holder error rolls everything back (`MB:144-155`).
- In code: the merge locks both customer rows (`srv/customers/merge.go:345-357`) and calls
  `holder.RepointCustomer(ctx, tx, from, into)` for each (`:387-393`); Invoices' holder
  locks the customer's documents newest first, "the order a credit note's issue takes them
  in … so the two never deadlock" (`srv/invoices/customer_slots.go:50-69`). Both
  directions run **customers → holders**: customer rows first, then each holder's.
  Nothing else shares a transaction across modules; there is no reserve/confirm, saga or
  compensation, and no cross-module event — the outbox bus is "deferred until Orders",
  built "together with its first real consumer" (`ROADMAP.md:8-21`).
- **The no-outside-call rule.** `withLockedTx` marks the context; "nothing inside fn calls
  another module or the object store" (`srv/invoices/server.go:102-115`). Why: the
  directories read "through the same pool, so a transaction that holds locks and then
  waits for a second connection can starve the pool" (`srv/expenses/server.go:104-112`),
  and "a slow store under a row lock is the same hazard by another route"
  (`srv/invoices/contractscalls.go:27-31`). MB keeps it as "**no contract call inside a
  transaction that holds a lock**" (`MB:261-264`).
- **How it is checked.** Every outbound call goes through an accessor calling
  `noteContractCall` (`srv/invoices/contractscalls.go:18-44`); the test hook records calls
  made while `InLockedTx(ctx)`, and `newInvoicesHarness` fails the test at cleanup if
  there were any (`srv/invoices/harness_test.go:72-78`, recorder `:82-118`). Expenses has
  the same (`srv/expenses/contractscalls.go`).
- **The tension.** The rule exists for a second pool connection or slow I/O. Rules 8 and
  9 already run other modules' SQL under a lock, on the caller's `pgx.Tx` — no connection
  taken, nothing leaving the process. A write-back on the issue's tx is that category, but
  the hook flags **any** accessor call, and `withLockedTx` gives `fn` only
  `*store.Queries`, never the `pgx.Tx` (`srv/invoices/server.go:110-115`).
- **Workers never compose.** `module.Workers` adds only `CustomerPersonalData`
  (`srv/module/workers.go:20-34`); providers resolve only in `Compose` (projects
  `srv/module/compose.go:201-206`, actuals `:213-218`, expenses `:226-231`, holders "resolved
  last" `:235-258`). An asynchronous write-back needs its handlers collected in both.
- Drift noticed in passing: `MB:11` and rule 4's schema list (`MB:48-49`) omit
  `invoices`, which `srv/db/schema_test.go:28` includes — worth fixing if MB is edited.

### 7.2 Time

- `time.entries` (`mig/00010_time_baseline.sql:6-33`, work types `mig/00032`): `user_id`,
  **`project_id integer NOT NULL`**, `billing_line_id`, `task_id`/`task_title`,
  `entry_date`, `hours numeric(5,2)`, `note`, `billable`, `bill_rate`/`bill_currency`,
  cost rate, `rate_source`, the work-type snapshot, `status`, `invoiced_at`, `revision`.
- **Rates are frozen on the hour** from submit; the chain is billing line → project →
  customer → person card → none, with no conversion (`srv/time/rates.go:1-15`;
  `R/time.md:62-110`). An invoice next month "bills what was promised, not what the rate
  card says today" (`R/time.md:384-385`).
- **`invoiced_at` is unwritten.** The status (`srv/time/values.go:36`) and the column
  (`mig/00010:29`) exist; approval refuses "Entry %d is invoiced"
  (`srv/time/approval.go:63-64`); unapprove skips it (`srv/time/queries/approvals.sql:30`);
  the actuals read it (`srv/time/queries/actuals.sql:47`) — but **no query in
  `srv/time/queries/` sets `invoiced_at` or the `invoiced` status**; tests reach it through
  the database (`R/time.md:198-201`). No `invoiced_by`, reference or frozen amount.
- **"`invoiced` has no way out"** (`R/time.md:189`); "Nothing moves an invoiced entry,
  unapprove included" (`:198`). Releasing hours on a credit needs a new `invoiced →
  approved` move and a change to the documented state machine.
- `billable` defaults true on fixed-price projects (`srv/time/values.go:260-271`), so their
  hours look like uninvoiced time-and-materials work beside the milestones.
- No row leaves the module: `ProjectActuals` is aggregates per project, line and work type,
  with `Invoiced` "the part of Approved that has already been billed"
  (`srv/contracts/actuals.go:95-112`); `GET /time/entries` filters by week, not range, and
  not by billable (`srv/time/queries/entries.sql:111-127`). There is no timesheet export.

### 7.3 Expenses

- **A manual stamp with an undo**: `POST /expenses/entries/{id}/invoiced {reference?,
  revision}` and `.../invoiced/undo` (`srv/expenses/invoiced.go:65`, `:90`), one line at
  a time, financial rights on the project, the period lock ignored
  (`R/expenses.md:811-821`). It locks the claim, then the line
  (`srv/expenses/invoiced.go:169-170`; module lock order `srv/expenses/claims.go:28-33`).
  Columns `invoiced_at`, `invoiced_by_user_id`, `invoice_reference varchar(100)`
  (`mig/00012_expenses_baseline.sql:95-97`); `UnmarkEntryInvoiced` clears them
  (`srv/expenses/queries/reimbursements.sql:287-297`).
- **The ready predicate** — unit approved, billable, not per diem, priced, not invoiced —
  is still in **several textual copies**: the list and its count
  (`srv/expenses/queries/entries.sql:179-184`, `:206-211`), the project figures twice
  (`srv/expenses/queries/projectexpenses.sql:67-76`), the stamp
  (`srv/expenses/queries/reimbursements.sql:273-284`) and `invoicedRefusal`
  (`srv/expenses/invoiced.go:36-61`); "The two must stay the same sentence"
  (`entries.sql:151`). 1A asked for one predicate first (1A `:565-569`). Index
  `ix_entries_to_invoice (project_id, entry_date) WHERE billable AND invoiced_at IS NULL`
  (`mig/00014:48-49`).
- **Markup**: bill amount = round(net × (1 + markup/100)) for outlays and supplier
  invoices, km × customer rate for mileage (`srv/expenses/money.go:49-60`).
- **Any currency** for outlays and supplier invoices, mileage only the installation's
  (`srv/expenses/entries_validation.go:330-343`); `bill_amount` is in the entry's currency.
- **VAT is an amount, never a code** (`srv/expenses/money.go:23-30`).
- **Per diem is never billable** (`srv/expenses/invoiced.go:44-48`).
- **Supplier invoices** (`mig/00033`): always on a project, company-paid, PDF required,
  re-billable like an outlay through the same stamp (`R/expenses.md:225-316`);
  `supplier_invoice_number` is not unique, so one can be re-billed twice (1A item 22,
  `:766`).
- No project, no invoice: "no customer to invoice it to" (`srv/expenses/invoiced.go:146-149`).
  Receipts live under Expenses' own storage scope (`srv/expenses/attachments.go:240-247`).
- `ProjectExpenses` gives per-currency totals with `Ready*` and `Invoiced*`, and
  "invoicing is a stamp here, not a status" (`srv/contracts/expenses.go:88`); no rows.

### 7.4 Projects

- **Milestones** (`mig/00011_projects_milestones.sql:23-45`): a flat `amount` +
  `amount_currency` or a `percent` of the fixed price; the stamp `invoiced_at`,
  `invoiced_by_user_id`, `invoice_reference varchar(100)`, `invoice_date`,
  `invoiced_amount` (frozen).
- **planned → ready → invoiced, with a way back**: `ready ↔ invoiced` under financial
  rights, the rest under the manager (`srv/projects/milestones_validation.go:74-96`);
  `planned` cannot go straight to `invoiced`. The undo is never refused for a lost fixed
  price, "because crediting an invoice is a real event that must not be blocked", converts
  a percent milestone to an amount (`R/projects.md:409-424`) and clears all five stamp
  columns (`:429`). An invoiced milestone is read-only
  (`srv/projects/milestones_validation.go:203-211`).
- **Lock order: project, then milestone** — "The milestone's own row lock comes second,
  always in that order" (`srv/projects/milestones.go:37-47`), because invoicing freezes
  fixed price × percent (`:604-607`).
- **Billing lines are not milestones**: a line is "variant + pricing rule" that prices
  hours and gives a line its product and unit (`R/projects.md:253-260`); `ProductCatalog`
  carries no tax category (`srv/contracts/catalog.go:7-15`).
- **No a-konto concept** in code; the economy design puts "a-konto accounts, retention,
  WIP" out of scope (`docs/superpowers/specs/2026-09-19-project-economy-design.md:22-26`).
- **Totals only across the boundary**: `ProjectDirectory` has no milestone read
  (`srv/contracts/projects.go`); actuals and expenses are aggregates. There is **no
  row-level read of hours** for any consumer.

### 7.5 The customer link

- Every source reaches a customer only through its project (`ProjectEntry.CustomerID
  *int32`, `srv/contracts/projects.go:16-25`; `ProjectsForCustomer`, `:100`); no source
  row stores a customer id.
- A project with no customer must be non-billable (`R/projects.md:66-69`), so billable
  work has one customer at any instant. But a project's customer can change (the
  `customer-changed` timeline event, `R/projects.md:53`), uninvoiced work follows it, and
  a merge re-points projects as a rule-8 holder (`MB:91`). The view resolves the customer
  at read time; the draft must not assume it is unchanged at issue.
- An anonymised, archived or disabled customer gets no new invoice (`customerGate`,
  `srv/invoices/drafts.go:62-79`); projects keep `customer_id` after anonymisation
  (`R/projects.md:1243-1248`).

### 7.6 Invoices

- **Lines are recreated on every save**: `writeLines` deletes every line and inserts them
  with new ids (`srv/invoices/drafts.go:351-378`; `srv/invoices/queries/lines.sql:8`).
  Anything keyed on `lines.id` with `ON DELETE CASCADE` is wiped by every `PUT`.
  `maxLines = 500` (`drafts.go:48`) — an itemised month can approach it.
- **`quantity > 0`**: `ck_lines_quantity` and `ck_lines_unit_price CHECK (unit_price >= 0)`
  (`mig/00034:223-224`) make a negative line — an a-konto deduction — impossible on either
  kind. The `big.Rat` arithmetic (`srv/invoices/money.go`) would tolerate one.
- **No project and no source column** on the document or line (`mig/00034:99-198`,
  `:204-233`); no `projectId` list filter.
- **Immutability.** `refuse_issued_document_change` compares `to_jsonb(row)` minus
  `customer_id`, `pdf_object_key`, `pdf_sha256` (`mig/00034:256-281`): **any column added
  later is frozen at issue** — "released" cannot live on the row.
  `refuse_issued_child_change` refuses child writes under an issued parent (`:298-329`);
  a new frozen child table takes it. The foundation design foresaw "a phase-3 write-back"
  (`docs/superpowers/specs/2026-09-26-invoices-foundation-design.md:656`).
- **The issue** (`srv/invoices/issue.go:215`). Before the transaction: the draft, the
  storage check, an invoice's billing profile (`:218-243`). In `withLockedTx`
  (`:247-438`): 1 `LockInvoice` (`:249`); 2 the merge re-check (`:261-264`); 3
  `ShareSettings` (`:266`); 4 `AllocateNumber` (`:271`), hook `issueAfterAllocation`; 5 the
  checks, all after the counter (`:288-354`), a credit note's `creditIssueChecks` locking
  the original (`srv/invoices/credits.go:603-609`); the KID (`:355-371`); 6 line snapshots,
  VAT summaries, `IssueDocument` last (`:373-433`). After the commit, the PDF (`:450`).
  "The lock order is always the document, then the settings row, then the counter, then —
  for a credit note — the original" (`:29-32`). Refusals are `cannotIssue(code, detail)`
  (`:59-61`), optionally with `LinePosition` (`:180-184`), and roll the number back.
- **Where a stamp fits.** Id and number are known after step 4. A stamp after the checks
  and the KID, beside `IssueDocument`, commits or rolls back with the number; its locks
  come last — document → settings → counter → original → **sources** — and its refusals
  (`source_already_invoiced`, `source_changed`) are `cannotIssue` codes with a
  `LinePosition`. After the commit, like the PDF, it is no longer "inside the issue
  transaction".
- **A credit's release lives on the credit side.** `CopyLinesToCredit` copies lines
  `WHERE src.quantity > 0` with `credits_line_id` = the original
  (`srv/invoices/queries/credits.sql:39`); credit drafts save through `writeLines` too, so
  **`credits_line_id` is the one stable key**. Under the original's lock the credit book
  knows a return at the line's price (`isReturn`, `srv/invoices/credits.go:233`), the
  line's last return (`lastReturn`, `:250`) and the final reversal (`finalReversal`,
  `:274`); caps refuse `credit_exceeds_line`/`_invoice` (`:29-30`, `:534`). An issued
  original's children are frozen, so "invoiced" reads "an issued link and no issued
  release", the release being a row of the credit note, written in its issue after the
  original's lock. A grouped line credited in part cannot say which hours it releases.
- **The PDF can carry a timesheet.** maroto v2.4.2 (`srv/invoices/pdf.go:10`) paginates
  `AddRows` and has `AddPages`. An issued PDF is rendered "from its own rows and
  snapshots, never the settings, the directory or the VAT tables"
  (`srv/invoices/pdfstore.go:37-38`), possibly later than the issue — so a timesheet is a
  **snapshot in the invoices schema**, frozen at issue. One PDF per document under a
  set-once key; `pdfcpu` is only indirect (`apps/server/go.mod:66`), so nothing appends
  to a stored PDF.
- **One EHF attachment** (§6.2) and one e-mail attachment (`srv/invoices/send.go:389`).
- **Permissions** (`srv/invoices/module.go:39-71`): `access`, `create`, `issue`,
  `manage`, `payments`; a draft is `access+create`, an issue `access+issue`
  (`x-vantigo-access` in `openapi/invoices.yaml`). None reads per-person hours.
- **CSV**: one row per document × VAT row, "A new column goes at the end"
  (`srv/invoices/csvexport.go:27-31`) — a project fits only at document level.
- **Response blocks**: warnings never refuse (`srv/invoices/responses.go:26-34`); the
  `ehf` block comes from own rows with `blockedBy` (`srv/invoices/transmissions.go:51-56`,
  `:96-129`), read by the send (`srv/invoices/send.go:187`); `invoiceResponse` "reads no
  directory itself" (`srv/invoices/responses.go:225-226`); `GET /meta` answers
  `mailAvailable`, `ehfAvailable` (`srv/invoices/meta.go:50-65`).
- **Units**: "timer", "time", "h", "hour" → HUR, fallback C62
  (`srv/invoices/ehf/units.go:13-47`).
- **A final below zero**: `document_state` counts `gross − credited − paid ≤ 0` as `paid`
  (`mig/00035:190-196`); a sluttfaktura whose deductions exceed the remainder is a credit
  in substance.

### 7.7 The frontend and the tests

- **Project tabs are host-owned**, gated by module, permission and capability
  (`visibleProjectDetailTabs`,
  `apps/host/frontend/src/routes/projects/-project-detail-layout.tsx:144`), cross-module
  ones through `-module-tab-gate.tsx`. The customer's Invoices tab mounts
  `CustomerInvoicesPanel` from `@vantigo/invoices-ui`
  (`apps/host/frontend/src/routes/customers/-customer-invoices-tab.tsx`).
- **Links are passed in.** Packages never import each other (rule 7); the host computes the
  href and hands it in — `expensesHref` into `ProjectEconomy`
  (`apps/host/frontend/src/routes/projects/-project-economy-route.tsx:16`, `:44`). An
  uninvoiced panel from invoices-ui fits a customer tab and a project tab the same way.
- Time's badge already knows `invoiced` (`apps/time/frontend/src/lib/status.ts:2`, `:13`);
  the invoice editor is one 1160-line page (`apps/invoices/frontend/src/pages/invoice.tsx`).
- **modtest** has the read fakes: `WithProjects` (`srv/modtest/modtest.go:206`),
  `WithActuals` (`:221`), `WithExpenses` (`:239`), beside the holder seams (`:251`,
  `:263`); a new slot needs its own `WithX`.
- **The integration harness composes all five**: one recorder over customers, projects,
  time, expenses, invoices (`srv/integration/harness_test.go:156`), `newInstallation`
  (`:100`); `invoicesInstallation` (`srv/integration/invoices_test.go:63`) and
  `figures_test.go:119` (projects + time + expenses, `buildFixture` `:258`) are the two
  halves of phase 3's end-to-end test. That package has no contract-call hook.

## 8. Patterns to take

1. **The holder contracts' shape** (`MB:81-113`, `:144-155`): the caller's `pgx.Tx`; SQL
   in the module's own `queries/`; never begin, commit or read a directory; idempotent; a
   constructor needing only the clock, so it runs enabled or not
   (`srv/invoices/customer_slots.go:44-48`); a rollback test per implementation and a
   caller test that one error rolls everything back.
2. **A block answered from own rows, with `blockedBy`** — the `ehf` block
   (`srv/invoices/transmissions.go:51-129`). A `sources` block (kinds, counts, can issue,
   first blocker) from the invoices schema's link rows, never a live read.
3. **Warning on the draft, refusal at the issue** — `vat_code_not_valid`
   (`srv/invoices/responses.go:34`, `srv/invoices/issue.go:186-190`); a changed source fits
   the same pair.
4. **Read before the lock, re-check under it** — the profile and the merge re-check
   (`srv/invoices/issue.go:238-264`); the wizard reads on the pool, keeps the revisions it
   read, and the write detects a change.
5. **Snapshots for anything printed** — the store-once rule
   (`srv/invoices/pdfstore.go:37-38`); a timesheet is a snapshot table.
6. **The slot tests** — `modtest` fakes, the locked-call recorder
   (`srv/invoices/harness_test.go:72-78`), the integration package, the deadlock race test
   (`srv/invoices/customer_slots_test.go:110`).
7. **The docs rule, per task**: behaviour lands with its pages, a plan lists them as its
   own step, and `Docs-Impact: none — …` says why when nothing changes
   (`AGENTS.md:14-38`, `:70`). Phase 3 touches three modules' pages besides Invoices'.

## 9. The write-back: four options

The roadmap fixes the outcome — sources marked invoiced with the invoice's id and number,
released by a credit note — and the precondition: a design under MB first
(`ROADMAP.md:823-828`). It does not fix the mechanism. This section decides nothing.

Common to all: each source schema needs columns for the invoice id and number (all hold
free text today); Time needs its first `invoiced` writer and a way back; and the manual
doors — expenses' `invoiced`/`undo`, the milestone's `ready ↔ invoiced` — must refuse a
module-stamped row or be retired, though design E5 keeps the manual milestone step "for
installations that invoice elsewhere" (`project-economy-design.md:51-53`).

### 9.1 (a) A holder-style command on the issue's `pgx.Tx`

A many-provider slot — say `Deps.InvoicedWork []contracts.InvoicedWorkHolder` with
`MarkInvoiced(ctx, tx, InvoiceRef{ID, Number, IssueDate}, sources)` and
`ReleaseInvoiced(ctx, tx, …)` — provided by Time, Expenses and Projects, called after the
checks and before `IssueDocument`, and in a credit note's issue after the original's lock.

- **Meets:** "inside the issue transaction" literally; no crash leaves unstamped work; an
  uninvoiceable source is a `cannotIssue` with `LinePosition` and the number rolls back;
  release is atomic with the credit. It mirrors rules 8 and 9.
- **Breaks or amends:**
  - `MB:92-93`: this is the "design of its own" — **rule 10**.
  - The lock rule: `withLockedTx` must expose the `pgx.Tx`, and the hook must tell a
    tx-bound command from a pool-bound call — an un-noted accessor documented as the
    exception, or a distinct prefix the harness allows under a lock while every pool-bound
    call still fails. `MB:261-264` restated, e.g. "no call that takes its own connection or
    leaves the process while a transaction holds locks".
  - **The direction inverts.** Customers, owning the lock, calls out today; here Invoices
    calls three modules whose orders must compose with its own — Projects project then
    milestone (`srv/projects/milestones.go:37-47`), Expenses claim then lines in id order
    (`srv/expenses/claims.go:28-33`) — after document → settings → counter → original, and
    **no source writer may ever lock an invoice row** (true today). A merge takes customer
    rows, then each holder's rows in holder order, Projects' and Invoices' among them
    (`srv/customers/merge.go:387-393`); that must be shown not to cycle with an issue's
    invoice document → milestone rows. Race tests per pair are the proof.
  - The counter row is held while many source rows are locked: the serialisation window
    grows with the invoice.
  - Validation (still approved? re-priced since the draft?) runs in the source module under
    Invoices' lock and needs a rule for a stale draft line.
  - A disabled source module still has rows; the holder runs enabled or not.
- **Rule 10 would say:** the slot, its methods and callers; the call's place in both
  transactions; the cross-module lock order, with no source module locking an invoices
  row; rule 8's constraints for implementations; the restated lock rule; the refusal
  shape; the enforcement bullets.
- **Tests:** per-module holder tests on a rolled-back real tx; invoices tests with a fake
  holder recording the tx; integration; races (issue against the expense stamp, a
  milestone move, a time unapprove).

### 9.2 (b) Reserve, confirm, reconcile

Before the lock, `Reserve(ctx, draftID, sources)` in each source module's own transaction;
the issue commits the invoice and a pending row; after the commit, `Confirm(invoiceID,
number)`; on refusal, `Release(draftID)`; a worker confirms and expires stragglers.

- **Meets:** no call under any lock — the hook stands; each module writes in its own tx.
- **Breaks:** not "inside the issue transaction" (the roadmap changes); reserved and
  confirmed states in three schemas and their effect on unapprove, the expense undo and
  the milestone undo; a crash between commit and confirm leaves sources reserved until the
  worker runs, and that worker must be collected in `module.Workers`
  (`srv/module/workers.go:20-34`), idempotent per invoice; a credit release has the same
  window. The most code and states; only the EHF queue's lease and idempotency resemble
  it (`ROADMAP.md:799-806`).
- **Rule 10 would say:** the three calls, their idempotency, the worker's guarantee and
  deadline, and what a source's state means while reserved.
- **Tests:** crash windows with the clock seam, expiry, integration.

### 9.3 (c) `invoices.line_sources`, pull only

Invoices stores the link in its own schema — `line_sources(line_id, source_kind,
source_id, project_id, …)` — written at draft and issue, frozen with the document,
released by credit-side rows; a new **read** contract (`InvoicedFor(kind, ids) →
map[id]InvoiceRef`) lets the sources show "invoiced" and filter their ready lists.

- **Meets:** no cross-module write, no third write direction, no lock question; atomic
  with the issue; rule 4 clean.
- **Breaks:** the sources' own state stops being authoritative, and their rules branch on
  it — time's refusal to reopen (`srv/time/approval.go:63-64`), expenses' refusals and
  index (`mig/00014:48-49`), the milestone's frozen amount and read-only rule, the
  `Invoiced` buckets of both aggregate contracts (`srv/contracts/actuals.go:95-112`,
  `srv/contracts/expenses.go`). Those checks would read Invoices under their own locks
  (forbidden) or before them (racy); `ActualsForProjects` and `ExpensesForProjects` would
  call Invoices while serving, the cycle MB avoids (`MB:201-227`). Three new optional
  dependencies; the manual stamps retired or coexisting.
- **MB:** no rule 10, a read contract and "providers do not call it while serving".
- **Tests:** read side, but every source aggregate changes.

### 9.4 (d) An outbox event

The issue inserts `InvoiceIssued(invoice_id, number, sources)` (and `CreditNoteIssued`)
in an invoices outbox; a dispatcher hands each to handlers registered by the three
modules, each stamping in its own tx, idempotent on the invoice id.

- **Meets:** the decided asynchronous pattern (`ROADMAP.md:8-21`, `MB:14-17`); no call
  under a lock; the enqueue is atomic and survives a crash.
- **Breaks:** the bus is deferred until Orders — Invoices would be its first consumer, a
  platform decision. Between commit and dispatch the source looks uninvoiced, so another
  draft could pull it: Invoices guards double billing itself, so (d) implies (c)'s table.
  A refusing handler (the entry was reopened) cannot undo an issued invoice — an exception
  queue like EHF's `unconfirmed` (`ROADMAP.md:803-804`). Handlers are a new slot collected
  in `Compose` and `Workers`.
- **Rule 10 would say:** the event contracts, the dispatcher's lease, retries and
  idempotency, and what a handler may do.
- **Tests:** dispatcher tests in the platform, per-module handlers, integration draining
  the queue.

### 9.5 Side by side

| | (a) holder on the tx | (b) reserve/confirm | (c) line_sources | (d) outbox |
| --- | --- | --- | --- | --- |
| "Inside the issue transaction" | yes | no | the link only | the enqueue only |
| Writes into other schemas | under Invoices' lock | in their own tx | none | in their own tx |
| Lock rule amended | yes | no | no | no |
| `MB:92-93` | rule 10 | rule 10 | a read contract | rule 10 and the bus |
| Source state authoritative | yes | eventually | no | eventually |
| Crash window | none | commit → confirm | none | commit → dispatch |
| Platform work | `withLockedTx`, the hook | a worker | none | the event bus |

Every option still needs Invoices to know, from the draft on, which sources each line
came from — to keep two drafts off the same hour and to know what to stamp — so every
option implies an invoices-owned link (§10).

### 9.6 Questions every option answers

- **Partial credits**: release on a line's full return (`lastReturn`) only, never a price
  reduction; an explicit subset on the credit note (exact when itemised); and a milestone's
  frozen amount against a partial credit.
- **A disabled source module**: stamp it anyway, as rules 8 and 9 run?
- **The manual doors** and the free-text references: kept, refused, retired.
- **Authority**: the stamp runs under `invoices:issue`, not the source module's own
  permission; the contract says so.

## 10. Recommended scope and sequencing

The roadmap lists the view, the wizard (project, work type, person, date, itemised), the
optional timesheet, markup, milestone and a-konto invoices with a final settlement, and
the write-back (`ROADMAP.md:817-829`). In dependency order:

**3A — the contract and the reads.**

1. **The MB design first** — rule 10, or the read contract under (c) — with the restated
   lock rule, the cross-module lock order and enforcement, reviewed as a spec before code.
2. **Line-level reads**: hours (id, revision, user, date, hours, rate, currency,
   multiplier, work type, line, note), expense lines (id, revision, kind, date, net, bill
   amount, markup, currency, claim, supplier name and number), milestones (id, revision,
   name, effective amount, currency, status) — none exists (§7.2–7.4). **The one expenses
   predicate** first, as an SQL function on the `expenses.owes_employee` precedent
   (1A `:565-570`).
3. **Time's writer**: `invoiced` with id and number, and `invoiced → approved`, with the
   state machine's page changed.

**3B — the wizard and the write-back.**

4. **`invoices.line_sources`** (`source_kind`, `source_id`, `source_revision`,
   `project_id`, snapshot quantity and amount) under `refuse_issued_child_change`, carried
   through `writeLines` — the `PUT` body echoes each line's sources, or `writeLines`
   diffs. Uniqueness among live drafts and unreleased issued lines cannot be an index over
   the parent's status: enforce it under lock at draft and issue, or keep a state column.
5. **A release table on the credit side**, keyed on `credits_line_id`, written in the
   credit note's issue after the original's lock.
6. **`srv/invoices/work.go`**: the uninvoiced view (source contracts read on the pool,
   before any transaction, less what live drafts hold) and "create a draft from work"
   (gates and profile before the transaction, as `PostInvoices` does), new accessors in
   `contractscalls.go`, `workAvailable` on `GET /meta`.
7. **The write-back** by §9's chosen mechanism, with refusal codes, the `sources` block and
   the draft warning.
8. **Settings**: default markup, default VAT code per source kind, the hours unit "timer";
   on the single row, PUT with its revision under `invoices:manage`.
9. **The uninvoiced panel** in invoices-ui, mounted by the host on the customer's Invoices
   tab and a project tab, linked from Projects' Economy tab by a host-supplied href.

**3C — the timesheet and a-konto.**

10. **The timesheet snapshot**: `invoices.timesheet_rows` written with the draft, frozen at
    issue, rendered as a new `pdfModel` block on its own page **inside the same PDF** —
    store-once key and EHF writer unchanged — with the person by number or initials by
    default, and in the module's personal-data export and erase.
11. **A-konto and the sluttfaktura**: milestone lines (quantity 1, unit "", the milestone's
    amount); an a-konto as an ordinary invoice; **deduction lines** with negative
    quantity, the a-konto's VAT code and a frozen `deducts_invoice_id`, capped by the
    a-konto's uncredited net per VAT row under its lock; the CHECK relaxed **on invoices
    only**; `BillingReference` 0..n in the EHF. `CopyLinesToCredit` and the credit caps
    then learn about negative lines.

**Defer:** construction's special rules (§3.5: § 8-1-2a progress invoicing, 10-year
timelists, retention money); `PrepaidAmount` and the VAT-free payment request (not a
salgsdokument; U5); several attachments — a separate timesheet file, forwarded receipts —
until Storecove's limits (U7) and its PDF regeneration (`ROADMAP.md:809-811`) are known;
utlegg as its own section (U4); line-level `InvoicePeriod`, BT-12, BT-18/BT-128,
`AccountingCost`; mixed currencies on one invoice.

**Pages phase 3 changes** (both languages where bilingual): `R/invoices.md` (The model,
Drafts, Issuing, Credit notes, The PDF, The EHF document, The CSV export if a project
column comes, Retention and personal data, Permissions, Endpoints, What comes next);
`R/time.md` (the state machine, "What invoicing will read"), `R/expenses.md` (the
invoiced track), `R/projects.md` (milestones); `en|nb/user/invoices.md`, and
`en|nb/user/time.md`, `expenses.md`, `projects.md` where the manual doors change; `MB`.

## 11. Decisions the design must take

1. **The write-back mechanism** (§9): (a), (b), (c), (d), or (c) with one of the others.
   Hangs on it: whether "inside the issue transaction" stands, the lock rule's wording,
   whether the sources' own state stays authoritative, whether the event bus is built now.
2. **Reserve at draft or only at issue** (1A `:464-466`); how a reservation survives
   `writeLines`; what releases it (line removed, draft deleted).
3. **Grouping and what the line says.** Which groupings, which default, the generated text
   ("Konsulenttimer, prosjekt X, september 2026"). Hangs on it: § 5-1-1's art and omfang
   (§2.1), whether a timesheet must be referenced, partial-credit precision, the 500-line
   cap.
4. **The timesheet.** Attached by default or not, per customer or per invoice; inside the
   PDF or a separate CSV or PDF (CL001 allows both); which columns; the person by
   **employee number or initials by default**, names only when the tenant enables it,
   never the note (§6.3); retention 5 years or 3 y 6 m (U3); whether the customer's
   personal-data export includes it.
5. **Markup and VAT code per source kind.** Hours: from the billing line's product, the
   work type, or a setting? Re-billed costs: the main supply's code (§4.2) — the main
   line's, or chosen per invoice? A separate good: its own. Milestones: a setting. Markup:
   the expense's own `markup_percent` from Expenses, or an invoice-side default (1A
   pattern 7, `:471`)?
6. **Utlegg**: unsupported in 3 (re-billing only), a flag on the expense, or a section
   outside the VAT base (U4).
7. **A-konto and the deduction.** An a-konto milestone kind or an invoice-side flag;
   negative lines at the same code (GBS 1 §1.1, BIS §5.6), remainder only, or crediting
   each a-konto; the cap and its lock order; the CHECK relaxed on invoices only; BG-3.
8. **Partial-credit release**: on `lastReturn` only, an explicit subset, or nothing until
   the final reversal; and whether the new invoice references the credit note (§5).
9. **The project dimension**: per-line `project_id` on the link, a nullable document
   project for single-project invoices (BT-11 under R080, a list filter, a CSV column at
   the end), or both; whether the wizard keeps one project per invoice so the EHF can name
   it.
10. **Who may see rates per person**: reuse `invoices:create`, add a sixth permission (a
    catalog entry), or apply Projects' financial-rights rule (1A item 18, `:759-760`);
    per-person detail in the `sources` block gated like `sendDefaults`.
11. **Mixed currencies.** One invoice, one currency (`mig/00034:117-119`), and the module
    issues "in NOK and nothing else" (`R/invoices.md:74-75`); hours carry the project's
    currency, expenses any. Split, refuse, or convert.
12. **Deadline warnings** (§2.4): one month after the VAT term for continuous services,
    one month after delivery for a discrete job; on the view, the draft, or both; and how
    "continuous" is known (per project, per customer, never guessed).
13. **The 2028 buyer org-number rule** (§1.2): `buyerComplete` must require the number for
    a bokføringspliktig buyer from 2028-01-01; the directory has no such fact. A dated rule
    like `b2bDutyFrom` (`srv/invoices/send.go:47-50`), a warning before the date, and how a
    person buyer is told apart — possibly the invoices backlog rather than phase 3.
14. **Fixed-price projects' hours** (§7.2): excluded, zero-priced lines, or information.
15. **Duplicate supplier invoices** (1A item 22): warn when a supplier number is re-billed.

**UNCERTAIN legal items carried** (sources in §§1–6):

- U1. A period as § 5-1-1's "tidspunkt … for levering" for services — practice says yes.
- U2. Hours as one total line with no breakdown when no timesheet is sent.
- U3. An attached timesheet's retention: 5 years or 3 y 6 m (10 in construction).
- U4. An utlegg on the same sales document as taxable lines, or a separate claim.
- U5. A VAT-free payment request in the invoice number series.
- U6. Skatteetaten's wording for the sluttfaktura deduction line.
- U7. Storecove's attachment size and count limits.
- U8. Bokføringsloven § 10 as amended from 2027-01-01 — not read.
- U9. The GBS 10 revision (hearing to 30 Nov 2026).
- U10. GBS 1 read from the tracked-changes PDF.
- U11. Diett and kilometergodtgjørelse at the main supply's rate — § 4-2's general rule and
  secondary sources only.
- Also: the 2025 amendment's preamble (search summary only, §2.4); the "separate supply"
  edge for re-billed goods (§4.2); a reference from a new invoice to the credit note as a
  rule rather than a choice (§5).
