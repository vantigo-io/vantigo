# Invoices module — research

Research for a new Invoices module. Two inputs: a domain study (Norwegian law, VAT,
EHF/Peppol, the 2026 B2B e-invoicing law, KID, dunning, Nordic and PSA systems, PDF
options) and an inventory of what the codebase already has for invoicing and what waits
for it. Every source was read on **2026-09-26** unless another date is given. Advisory-firm
and vendor pages are secondary sources only. Anything not confirmed from a primary source
is marked **UNCERTAIN**. Code references are `file:line` against `feat/invoices-foundation`
at `e5601fb0` (identical to `main`); `srv/` abbreviates `apps/server/internal/`.

## 1. Why now

### 1.1 Four roadmaps end at the same place

- **Customers.** The billing profile is "ready for Invoices to read once that module
  exists" (`docs/src/content/docs/en/reference/customers.md:2632-2633`), and "whether a customer *can actually be
  invoiced* a given way is Invoices' question to ask at send time" (`docs/src/content/docs/en/reference/customers.md:490-496`).
  `disabled` waits for Invoices to give it meaning, with Business Central's "blocked for
  invoicing" as the model (`docs/src/content/docs/en/reference/customers.md:94-102`; `ROADMAP.md:49-52`). Customer 360
  still lacks "invoiced revenue and outstanding once Invoices exists"
  (`ROADMAP.md:241-242,270-272`). Per-customer dunning, a portal and credit limits are
  parked behind Invoices (`ROADMAP.md:325-329`).
- **Projects and Time.** A billing milestone "is what Invoices turns into an invoice line;
  a-konto is one kind" (`docs/superpowers/specs/2026-09-18-project-management-plan.md:132-135`);
  "A later Invoices module sets the same status" (`ROADMAP.md:533-537`). Time has an
  `invoiced` status and an `invoiced_at` column that "exist for the future Invoices module
  to use" (`docs/src/content/docs/en/reference/time.md:192-196`) and a whole section "What invoicing will read"
  (`docs/src/content/docs/en/reference/time.md:363-387`).
- **Expenses.** "The module that turns that list into an invoice does not exist yet; when
  it does, it owns the stamp" (`docs/src/content/docs/en/reference/expenses.md:1260-1265`; `ROADMAP.md:727-732`).
- **Energy.** Phase 4 is "Invoicing groundwork (align with Invoices)": a
  `contracts.ConsumptionProvider` and a price dimension (spot prices per price area NO1–NO5,
  grid tariffs) "decided together with the Invoices module" (`ROADMAP.md:379-387`).

### 1.2 The B2B e-invoicing mandate is enacted

- **Proposal**, 16 Mar 2026 (regjeringen.no,
  https://www.regjeringen.no/no/aktuelt/foreslar-krav-om-e-faktura-og-digital-bokforing-i-naringslivet/id3152311/):
  "bokføringspliktige virksomheter skal fakturere hverandre med e-faktura innen **1. januar
  2027**, og … innen **1. januar 2030** skal bokføre digitalt." The sending duty was moved
  forward from 2028. This is Prop. 44 L (2025–2026),
  https://lovdata.no/dokument/PROP/forarbeid/prop-44-l-202526
- **Enacted**: Innst. 262 L (7 May 2026),
  https://www.stortinget.no/no/Saker-og-publikasjoner/Publikasjoner/Innstillinger/Stortinget/2025-2026/inns-202526-262l/?m=2 ;
  lovvedtak 52 (2025–2026),
  https://www.stortinget.no/no/Saker-og-publikasjoner/Vedtak/Beslutninger/Lovvedtak/2025-2026/vedtak-202526-052/ ;
  sanctioned as **Lov 19. juni 2026 nr. 39**, https://lovdata.no/dokument/LTI/lov/2026-06-19-39 .
- **In force**: the Stortinget case page (https://www.stortinget.no/no/Saker-og-publikasjoner/Saker/Sak/?p=200136)
  lists **1 Jan 2027** for part I §§ 3, 10, 11, 13 (the **sending** duty) and **1 Jan 2030**
  for part I § 7 (4) (digital bookkeeping and the **receiving** duty). Lovdata's *Statsråd
  19. juni 2026* (https://lovdata.no/artikkel/statsrad_19__juni_2026/5544) records
  "delvis ikraftsetting" without the decision text. **UNCERTAIN:** the formal in-force
  resolution was not seen; the dates come from the Stortinget page.
- **The text** defines e-faktura as "et salgsdokument som kan utstedes, sendes og mottas i
  et strukturert, elektronisk format, som er egnet for automatisert behandling" and says
  "Dokumentasjon for salg av varer og tjenester til andre bokføringspliktige skal utstedes
  i elektronisk fakturaformat". The ministry sets format and exemptions by regulation.
- **Still pending**: Skattedirektoratet's hearing (20 Jun 2025, closed 31 Oct 2025)
  proposed **EHF** as the format and an exemption for sole proprietorships under NOK 50 000
  turnover without accounting or VAT duties; regulations are expected **by December 2026**
  (Regnskap Norge, 27 Mar 2026, https://www.regnskapnorge.no/faget/artikler/bokforing/pa-vei-mot-pliktig-efakturering-og-digital-bokforing/ ;
  Deloitte, https://www.deloitte.com/no/no/services/tax/blogs/skattekilden/elektronisk-fakturering-fra-1-januar-2027.html ;
  Sticos, updated 5 Jul 2026, https://www.sticos.no/fagstoff/e-faktura-blir-obligatorisk-fra-2027).
  **B2C is not covered.** VATupdate (18 Sep 2026,
  https://www.vatupdate.com/2026/09/18/briefing-document-podcast-e-invoicing-and-e-reporting-in-norway/)
  says the duty is "conditional on the buyer being registered in ELMA" and the regulation
  is "expected to designate EHF 3.0 or newer" — **UNCERTAIN**, secondary, not adopted.
- **B2G** has required EHF since 2019: *Forskrift om elektronisk faktura i offentlige
  anskaffelser* (FOR-2019-04-01-444) § 4 approves EHF Billing 3.0 / Peppol BIS Billing 3.0
  or newer, https://lovdata.no/dokument/SF/forskrift/2019-04-01-444/%C2%A74 .

**Consequence.** From 2027-01-01 — about three months from today — tenants that invoice
Norwegian businesses must send e-invoices, presumably EHF over Peppol. A module that only
e-mails PDFs is non-compliant for B2B on its first day of 2027. `ROADMAP.md:333-336`
calls the date "indicative … nothing here is scheduled against it", and the Customers
research marked it UNCERTAIN (`docs/superpowers/research/2026-09-21-customers-module-next.md:93-95`);
both are now out of date.

## 2. What the law requires of a salgsdokument

### 2.1 Content — bokføringsforskriften kap. 5-1

Source: https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558/KAPITTEL_5-1

- **§ 5-1-1**: number and documentation date; the parties; nature and scope of the supply
  (description, quantity, unit price); time and place of delivery; consideration and due
  date; VAT and other duties required by law. **VAT shall be stated in NOK.** Under reverse
  charge: "Omvendt avgiftsplikt – Merverdiavgift ikke beregnet", and the buyer's org number
  is always given.
- **§ 5-1-1a**: language per bokføringsloven § 12 — Norwegian, Swedish, Danish or English.
- **§ 5-1-2**: buyer = name and address *or* org number (org number mandatory under reverse
  charge). Seller = name and **org number**, followed by **"MVA"** if VAT-registered. AS/ASA
  and NUF must show **"Foretaksregisteret"** with head-office address, and liquidation if
  any. Skatteetaten: a buyer may lose its input-VAT deduction if "MVA" or the org number is
  missing, https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/angivelse-av-kjoper-i-salgsdokumentet/
- **§ 5-1-4**: the delivery-date requirement falls away for forwarding agents, mail order,
  and documents travelling with the shipment.
- **§ 5-1-5**: taxable and exempt sales shown and totalled separately; the same for
  reverse charge and for different rates.
- § 5-1-6 shows that a "felles nummerserie" is a recognised concept.

### 2.2 Numbering and immutability

- **§ 5-1-3** (https://lovdata.no/forskrift/2004-12-01-1558/§5-1-3): "Salgsdokument skal
  være forhåndsnummerert … ved maskinelt tildelte nummer med en kontrollerbar sekvens …".
  A document issued in the first fifteen working days of a month may carry the previous
  month's last date if delivery had happened by then.
- Skatteetaten (4 Feb 2007): "en fortløpende maskinelt tildelt nummerserie for den enkelte
  bokføringspliktige enhet", and the journal must show "det ikke er brudd i nummerserien",
  https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/nummerering-av-salgsdokument-bokforing-ved-bruk-av-excel-mv/
- Skatteetaten: a system that lets a user easily override numbering breaches the rule
  "uavhengig av om muligheten rent faktisk benyttes",
  https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/nummerering-av-salgsdokumenter/
- Skatteetaten (Nov 2012): several sets of identical numbers in one year breach the rule;
  short series such as 1–50 are not a "kontrollerbar sekvens",
  https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/nummerering-av-salgsdokument/
- Numbering may be sequential "possibly per document type"
  (https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/nummerering-av-salgsdokument--bruk-av-felles-faktureringssystem/)
  — **UNCERTAIN** wording, taken from a search summary; consistent with practice.
- **§ 5-2-9**: an electronically issued document uses a format that cannot be changed
  "uten at endringen fremgår direkte av salgsdokumentet".

### 2.3 Credit notes

- **§ 5-2-7** (https://lovdata.no/forskrift/2004-12-01-1558/§5-2-7): a replacement document
  requires "en kreditnota som reverserer opprinnelig salgsdokument". An issued invoice is
  credited and re-issued, never edited.
- A kreditnota carries full kap. 5-1 content, says what is returned or discounted, and
  "som hovedregel" references the document it corrects (Kontohjelp on GBS 1,
  https://kontohjelp.no/kontering/kreditnota/innhold-i-kreditnotaen ; GBS 1, Jan 2026,
  https://www.regnskapsstiftelsen.no/wp-content/uploads/2026/02/2026-01-GBS-1-Kreditnota-%E2%80%93-innhold-utstedelse-bokforing-og-oppbevaring-januar-2026-endringsmarkert-mot-HU-august-2025.pdf).
  **UNCERTAIN:** the GBS PDF text was not extracted; whether it prescribes or only allows a
  separate credit-note series is unconfirmed.
- Sticos (https://www.sticos.no/fagstoff/nar-skal-kreditnota-brukes-og-nar-kan-den-ikke-brukes):
  credit only to correct earlier invoiced sales (wrong buyer, delivery, price, VAT, missing
  data) — not for market support, debt forgiveness or "endelig konstaterte tap på
  kundefordringer". Partial corrections may instead be a supplementary invoice or negative
  lines on a later invoice to the same buyer.
- Capping a credit note at the original's remaining uncredited balance is common practice,
  not law. **UNCERTAIN:** no primary source lists what counts as "document" on an issued
  invoice; treat everything printed or sent in the EHF as immutable, and only payments,
  reminder flags and internal notes as mutable.

### 2.4 Timing, advance invoicing and a-konto

- **§ 5-2-2**: issue "snarest", no later than one month after delivery; § 5-2-3 monthly
  collective invoicing within 15 working days; § 5-2-4 ongoing services up to one month
  after the VAT period (https://lovdata.no/forskrift/2004-12-01-1558/§5-2-1).
- **§ 5-2-6**: subscriptions, rentals, fees and similar may be invoiced up to **one year**
  before delivery; otherwise invoicing ahead of delivery is not a salgsdokument, and a pure
  prepayment is a financial advance (payment request). Skatteetaten, 27 Jun 2024,
  https://www.skatteetaten.no/en/rettskilder/type/kunngjoringer/endringer-i-bokforingsforskriften-mv/
- **A-konto** (Skatteetaten,
  https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/utstedelse-av-salgsdokument-i-bygge--og-anleggsvirksomheten--avregning-av-forskudd/):
  "rene forskudd" need no sales document; once work has started, the duty applies to "den
  delen av betalingen som tilsvarer verdien av de arbeidene som er utført". VAT falls due
  in the term the document is issued (mval. § 15-9); the final invoice deducts earlier
  a-konto invoices. **UNCERTAIN:** the presentation of the deduction is practice, not law.

### 2.5 Currency, rounding, retention

- **Currency.** VAT in NOK (§ 5-1-1 nr. 6). SKD-melding Av 10/1990 (updated 1 Feb 2019,
  https://www.skatteetaten.no/en/rettskilder/type/skattedirektoratets-meldinger/omsetning-i-utenlandsk-valuta-og-oreavrunding-i-salgsdokument):
  base and consideration need not be in NOK; VAT-return amounts convert at the **invoice
  date's rate** (bokføringsforskriften § 4-2 (2)). **UNCERTAIN** wording (body PDF not read).
  EHF: a non-NOK invoice carries a second `TaxTotal` in NOK (§4.1).
- **Rounding.** No øreavrunding for electronic payment; øre shown at line and total level
  (Revisorforeningen, https://www.revisorforeningen.no/fag/nyheter/oreavrunding-av-vederlag/).
  VAT must be shown **per rate**, not necessarily per line — per line, grouped per rate, or
  a code per line with a per-code summary (Skatteetaten, 8 Feb 2010,
  https://www.skatteetaten.no/en/rettskilder/type/uttalelser/prinsipputtalelser/krav-til-a-spesifisere-merverdiavgift-i-salgsdokument/).
- **Retention.** Bokføringsloven § 13 (https://lovdata.no/lov/2004-11-19-73/§13): sales
  documents **5 years** after the end of the financial year; contracts, correspondence,
  price lists 3 years 6 months (10 → 5 years from 2015,
  https://www.skatteetaten.no/en/rettskilder/type/kunngjoringer/redusert-oppbevaringstid-for-regnskapsmateriale/).
  Storage in the EEA, UK and Switzerland is allowed without dispensation if accessible from
  Norway and notified (Amesto, https://www.amestoaccounthouse.no/blogg/oppbevaring-av-regnskapsmateriale-i-2026)
  — **UNCERTAIN**, not checked against kap. 7. Lov 2026 nr. 39 also changes § 13 from
  2027-01-01 — **UNCERTAIN**, new text not read.
- **SAF-T Financial v1.40** is the only valid version from 1 Jan 2027 (VATupdate, above) —
  **UNCERTAIN**, secondary; matters only if Vantigo becomes the bookkeeping system.

### 2.6 Implications for software

1. The system assigns the number at **issue**; the user never types it.
2. A number is never reused or deleted. An issued invoice is never deleted, only credited.
3. A **draft is not a salgsdokument** and takes no number — otherwise a deleted draft
   leaves a gap. The Nordic systems number on "opprett"/"fakturer", distinct from sending.
4. Allocation is transactional with issue. A Postgres `SEQUENCE` is not gap-free; a counter
   row locked to commit is.
5. The issued document (data and PDF) is immutable (§ 5-2-9).
6. The start number is set once per series (migration from an old system), then locked.
7. Numbering is per bokføringspliktig enhet: per tenant, and per legal entity if a tenant
   ever holds several.
8. Correction = credit note referencing the original + new invoice. A credit note is its
   own numbered document of the same entity, full or partial, same VAT and currency.
9. Foreign-currency invoices store document currency, exchange rate with date and source,
   and VAT per rate in NOK.
10. Issued invoices, credit notes, PDFs, EHF XML and payments are undeletable for 5 years
    after year-end. A customer referenced by an invoice cannot be erased; the invoice's
    buyer snapshot stays.

## 3. VAT

### 3.1 Rates 2026

Skatteetaten, https://www.skatteetaten.no/en/rates/value-added-tax/

| Rate | Scope |
|---|---|
| 25 % | general |
| 15 % | foodstuffs; water and wastewater |
| 12 % | passenger transport, accommodation, broadcasting, cinema, sport, amusement parks |
| 11.11 % | raw fish (anskaffelser.dev, §4.1) |
| 0 % (fritatt) | zero-rated, inside the act, deductible (export etc.) |
| unntatt | outside the act (health, education, finance), no deduction |

No food-rate change for 2026 (Bondelag,
https://www.bondelaget.no/rjr/skatt-regnskap-og-trygd/fagartikler/skatt-og-avgift/statsbudsjett-for-2026-endringer-pa-skatte-og-avgiftsrettens-omrade).
**UNCERTAIN** for 2027 (budget in October 2026). Rates must be data with validity periods.

### 3.2 SAF-T standard tax codes for sales

Skatteetaten's `Standard_Tax_Codes.csv`,
https://github.com/Skatteetaten/saf-t/blob/master/Standard%20Tax%20Codes/CSV/Standard_Tax_Codes.csv

| Code | Meaning | Rate class |
|---|---|---|
| 3 | Utgående mva | 25 % |
| 31 | Utgående mva | 15 % |
| 32 | Utgående mva | 11.11 % |
| 33 | Utgående mva | 12 % |
| 5 | Innenlands omsetning fritatt | 0 |
| 51 | Innenlands omsetning, omvendt avgiftsplikt | 0 |
| 52 | Utførsel (export) | 0 |
| 6 | Utenfor mva-loven | outside |
| 7 | Ingen mva-behandling (inntekter) | not turnover |

Codes 1, 11–15, 20–22 and 81–92 are purchases and imports — for Expenses and supplier
invoices, not sales. The mva-melding reports per SAF-T code; credit notes reduce the same
codes in the term they are issued
(https://skatteetaten.github.io/mva-meldingen/mvameldingen/forretningsregler/ — **UNCERTAIN**,
not read in depth).

### 3.3 The per-rate computation rule

Peppol **BR-CO-17**: category tax amount = category taxable amount × rate/100, rounded to
2 decimals — VAT is computed **on the per-rate sum of lines**, not per line and summed.
Line net is rounded to 2 decimals (PEPPOL-EN16931-R120: qty × net price / base qty +
charges − allowances). Sources: https://docs.peppol.eu/poacc/billing/3.0/bis/ ,
https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/ . Norwegian law accepts both, so
compute per rate everywhere (PDF, EHF, any export) and the outputs never differ by an øre.

### 3.4 The tenant VAT-code model

Common practice: a per-line VAT code from the tenant's own list, defaulted from the product
or work type, overridable per line. The EHF needs a UNCL5305 category per line — `S`
standard, `Z` zero, `E` exempt, `AE` reverse charge, `G` export, `K` intra-EEA, `O` outside
scope; Norway uses S with 25/15/12/11.11 and E, Z, K, AE, G at 0
(https://anskaffelser.dev/postaward/g3/spec/current/billing-3.0/norway/). So one table:
**tenant VAT code → (SAF-T code, EHF category, rate %, valid from/to, exemption reason
text)**. EN16931 requires `TaxExemptionReason` for E, AE, G and similar — **UNCERTAIN**,
exact BR-E/BR-AE ids not quoted.

Today `products.tax_categories` has `(id, name, kind, rate numeric(5,4))` with no validity
dates and no code (`srv/db/migrations/00004_products_baseline.sql:24-32`), kinds
Standard/Reduced/Zero/Exempt (`srv/products/taxcategories.go:42-55`), and no S/Z/E/AE or
SAF-T codes anywhere in the repo.

## 4. EHF / Peppol

### 4.1 What an EHF must carry beyond EN 16931

Sources: https://docs.peppol.eu/poacc/billing/3.0/bis/ ,
https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/ ,
https://anskaffelser.dev/postaward/g3/spec/current/billing-3.0/norway/

- `cbc:EndpointID@schemeID` **0192** (Norwegian org number) for seller and buyer.
- **PEPPOL-EN16931-R003 (fatal):** a buyer reference or purchase-order reference is
  required. Map to the billing profile's `buyerReference`, overridable per invoice; offer
  an order reference too.
- **NO-R-001 (fatal):** seller VAT number = `NO` + 9 digits + `MVA`.
- **NO-R-002 (warning):** "Foretaksregisteret" as a second `PartyTaxScheme`
  (`CompanyID` = "Foretaksregisteret", `TaxScheme/ID` = "TAX").
- Payment means UNCL4461 **30** (credit transfer), 58 or 59; `PayeeFinancialAccount/ID`
  (BBAN, or IBAN + BIC); **`PaymentID` carries the KID**.
- One `TaxTotal` with a `TaxSubtotal` per category and rate (**R053**); a non-NOK document
  adds a second `TaxTotal` in NOK without subtotals (**R054**; **R005**: tax currency must
  differ from document currency).
- Credit note: UBL `CreditNote`, type code 381, `BillingReference/InvoiceDocumentReference`
  to the original's id and date (a negative 380 is also allowed). **R080:** one project
  reference per document.
- Attachments: `AdditionalDocumentReference/Attachment/EmbeddedDocumentBinaryObject`
  (Base64, `@mimeCode` and `@filename` required),
  https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/cac-AdditionalDocumentReference/cac-Attachment/cbc-EmbeddedDocumentBinaryObject/ .
  Commonly listed types: PDF, PNG, JPEG, CSV, xlsx, ods — **UNCERTAIN**, code list not
  read. A timesheet specification goes here.
- A PDF copy is recommended, not mandatory; EHF 3 dropped EHF 2's visual-copy marking
  (Helse Sør-Øst guide, https://www.helse-sorost.no/4ab037/contentassets/8300d0a520d9443f8a70aa20178e1772/guide-for-ehf-faktura.pdf).
  **UNCERTAIN:** that it is legally required only with an invoice portal.
- CO2 fields (AdditionalItemProperty) are optional Norwegian extensions — possibly for
  energy later.

### 4.2 Access points and integration styles

- Sending needs a certified **Peppol access point**; the Norwegian Peppol Authority is
  **DFØ** (https://www.anskaffelser.no/verktoy/veiledere/aksesspunkt ;
  https://docs.digdir.no/docs/eGovernment/introduksjon). Any certified AP anywhere can
  deliver to Norway (https://peppolvalidator.com/peppol-norway, 10 Apr 2026 — its "no firm
  timetable" line is outdated, §1.2).
- 80+ APs are listed (https://www.anskaffelser.no/verktoy/veileder/aksesspunkter-ehf-og-bis-formater),
  among them Visma, Tietoevry, Pagero, Basware, Logiq, Routty, Strålfors, Azets, B2BRouter,
  EDICOM, Qvalia, the banks and SAP.
- Styles for software that is not an AP:
  - **Logiq** — APIs for status/receipts, API or SFTP transfer
    (https://www.logiqconnect.com/additional-services/peppol-access-point); **UNCERTAIN**,
    public API docs not found.
  - **SendRegning** — REST (HATEOAS), picks EHF when the receiver is in ELMA
    (https://www.sendregning.no/utviklere/ , https://sendregning.github.io/).
  - **Storecove** — REST/JSON with OpenAPI, JSON or UBL in (https://www.storecove.com/docs).
  - **e-invoice.be** — flat JSON in, UBL generated (https://e-invoice.be/peppol-api).
  - Unimicro as an AP API: **UNCERTAIN**, not found. Vipps is not an AP.
- **Recommendation:** Vantigo generates the UBL itself (it owns and must validate the
  data), validates locally with the official Peppol/EHF Schematron, and hands the XML to an
  AP through a small `AccessPoint` port (send, status, receipt callback) with a dry-run
  mode. The first adapter is the user's choice.

### 4.3 ELMA

ELMA is Digdir's SMP for Norwegian participants (https://samarbeid.digdir.no/elma/dette-er-elma/108 ,
https://docs.digdir.no/docs/ELMA/). Lookup goes through the Peppol SML/SMP since the
datahotel closed (https://www.anskaffelser.no/nyhetsarkiv/omlegging-av-sok-pa-mottakere-av-ehf);
new ELMA terms from 5 Jan 2026 — **UNCERTAIN** detail. Invoices must **re-check at send**
that the receiver accepts the *invoice* and the *credit note* document type separately:
an invoice-capable receiver may not accept credit notes. Receiving EHF (for Expenses) needs
the tenant's own ELMA registration and is out of scope.

### 4.4 Mandate status in one line

B2G EHF mandatory since 2019; B2B sending duty law from 2027-01-01, format regulation
(expected EHF) due by December 2026; receiving duty and digital bookkeeping 2030 (§1.2).

## 5. KID, payment files, eFaktura, dunning

### 5.1 KID

Mastercard Payment Services, *Systemspesifikasjon OCR giro* v4.0 (30 Sep 2018),
https://www.mastercardpaymentservices.com/media/ruqn3ort/ocr-systemspesifikasjon_no_mps.pdf

- 25 positions, **digits only**, right-aligned. Check digit **MOD10** (Luhn) or **MOD11**
  (weights 2–7 repeating; remainder 1 gives **"-"**). Spec examples: 1234567 → MOD10 check
  2; 12345678 → MOD11 check 5. Min 3 + check digit, max 25 (Conta says 2–25,
  https://conta.no/artikler/kid-nummer). The **bank agreement** fixes length and algorithm —
  **UNCERTAIN** whether variable length is allowed (brukerhåndbok not read in detail).
- Composition is convention (e.g. customer number + invoice number + check digit). Store
  the KID on the invoice and match by lookup. Prefer MOD10 (MOD11's "-" trips payers). Make
  length, algorithm and composition tenant settings; with no KID agreement, send no KID and
  use the invoice number as message. Without an OCR agreement a KID only arrives as
  remittance text.

### 5.2 Payment-matching files

- **OCR giro**: 80-character records, format `NY`; shipment `NY000010`/`NY000089`,
  assignment `NY090020`/`NY090088`, amount record 1 `NY09tt30` (transaction type,
  settlement date, amount in øre (17), KID (25)), record 2 `NY09tt31` (archive reference,
  debit account). The spec carries a full example file — a ready test fixture.
- **camt.054** (ISO 20022) is the successor; MPS "Innbetaling Total" builds on it.
  **UNCERTAIN** whether and when OCR giro is retired.
- A payment record: date, amount, currency, method (manual/OCR/camt/bank API), bank
  reference, KID/message, payer; allocation to one or many invoices; overpayment → customer
  credit balance; write-off tolerance; reversal. **Open amount = gross − credited − paid;
  paid/partly paid/overdue is derived, not stored.** Order: manual, then file import, then
  bank API (**UNCERTAIN**, no provider research).

### 5.3 eFaktura (B2C)

Bank-network e-invoice to the consumer's netbank or Vipps, through MPS with a bank
agreement and file qualification (https://mastercardpaymentservices.com/norway/efaktura).
**Vipps discontinued "Vipps eFaktura"** as sender but remains a place to pay
(https://www.fair.no/nyheter/vipps-legger-ned-%22vipps-efaktura%22/bc43efcd-5e22-4a78-b7bd-fbad8b816b0c)
— **UNCERTAIN** date. Needs consent ("Ja takk til alle") and a provider (MPS, SendRegning,
Logiq B2C). A late phase.

### 5.4 Dunning — 2026 figures

- **Forsinkelsesrente 12.25 % p.a. from 1 Jul 2026** (12.0 % in H1 2026); set twice a year
  as policy rate + ≥ 8 pp. **B2B standard compensation NOK 430** (forsinkelsesrenteloven
  § 3a). Finanstilsynet,
  https://www.finanstilsynet.no/nyhetsarkiv/nyheter/2026/forsinkelsesrente-og-standardkompensasjon-for-inndrivelseskostnader-fra-1.-juli-2026 ;
  FOR-2026-06-25-1372, https://lovdata.no/dokument/SF/forskrift/2026-06-25-1372 .
- **Inkassosats 2026 = NOK 750**,
  https://www.finanstilsynet.no/nyhetsarkiv/nyheter/2026/inkassosatsen-i-2026-blir-750-kroner/
- **Inkassoforskriften § 1-2** (https://lovdata.no/dokument/SF/forskrift/1989-07-14-562/KAPITTEL_1,
  last amended FOR-2025-12-19-2709): a written purring sent **at the earliest 14 days after
  due date**, stating amount and what it concerns, may carry **1/20 of the inkassosats**,
  rounded to the krone — **NOK 38** in 2026. The same for an inkassovarsel. The creditor's
  own betalingsoppfordring carries 3/20 (NOK 113), only after a ≥ 14-day deadline was
  missed. The sats is taken when the notice is sent.
- **§ 1-3**: per claim, fees for either *two purringer + one betalingsoppfordring* or *one
  purring + one inkassovarsel + one betalingsoppfordring*; a second notice counts only if a
  **≥ 14-day** deadline in the first was missed; new fees after six months.
- **Inkassoloven § 9** (https://lovdata.no/lov/1988-05-13-26/§9): after due date, a written
  inkassovarsel with ≥ 14 days to pay; may be combined with a purring.
- A fee-free reminder can go any time after due date.
- Design: overdue list → user picks → reminder documents (not salgsdokumenter, not in the
  invoice series), level and dates recorded, 14-day gaps and fee caps from a **dated rates
  table**, inkasso hand-off as an export first.

## 6. What comparable systems do

### 6.1 Nordic accounting systems (condensed)

Vendor help pages only; **UNC** = UNCERTAIN. Visma eAccounting Norway is being wound down
into Tripletex (https://hjelp.tripletex.no/hc/no/articles/19918391816721); its help site
needs JavaScript, so its facts are snippets.

| | Fiken | Tripletex | PowerOffice Go | Conta | 24SevenOffice | Uni Economy |
|---|---|---|---|---|---|---|
| Number assigned | on "Opprett" | UNC | series in settings, credit notes share it | on creation | UNC | on "Fakturer" |
| Channels | EHF, e-mail, eFaktura, post, SMS | EHF, e-mail, eFaktura, AvtaleGiro, paper | e-mail, EHF, print, eFaktura, AvtaleGiro | EHF, eFaktura, e-mail, post | e-mail, EHF, eFaktura, AvtaleGiro | per-customer "utsendelsesplan" |
| A-konto | UNC | project invoice plan, final deducts | UNC | manual (2900) | UNC | UNC |
| Time → invoice | draft from "fakturagrunnlag", timesheet attachment | "Godkjenn og fakturer" | "Fakturaforslag" per total/employee/activity | via Timerabbit | "fakturaplan" | wizard, "Splitt på dimensjon" |
| Credit notes | full/partial; asks to unlock hours | full; crediting frees hours and expenses | full, edit down | full only | full or minus order | full or partial |
| Reminders/inkasso | Kravia, needs KID | Amili, Kravia, Kredinor | Kredinor etc., 14 days | Kravia | Oflow | via Markedsplass |

Sources: Fiken https://hjelp.fiken.no/faktura-med-feil-slik-retter-du-den ,
https://hjelp.fiken.no/timefoering ; Tripletex https://hjelp.tripletex.no/hc/no/articles/4406369059985
(a-konto), https://hjelp.tripletex.no/hc/no/articles/22045715985681 (crediting frees hours);
PowerOffice https://hjelpesenter.poweroffice.no/fakturaforslag ,
https://hjelpesenter.poweroffice.no/kreditere-faktura ; Conta
https://hjelp.conta.no/faktura/fakturaer/kreditnotaer/ ; 24SevenOffice
https://support.24sevenoffice.com/no-no/hvordan-opprette-en-kreditnota ; Uni
https://help.unieconomy.no/fakturere-timer , https://help.unieconomy.no/utsendelsesplan-for-faktura .

Shared: nobody edits or deletes an issued invoice; most number on book/create, separate
from sending. Time comes as one total, per employee or per activity, with an optional
timesheet. Crediting releases hours (Tripletex automatically, Fiken asks). Collection goes
to a partner at 14 days past due. "EHF if the receiver can, else the customer's
preference" is the norm.

### 6.2 PSA tools (condensed)

Harvest and Scoro help centres returned 403; their facts are search excerpts **(s)**,
treat as UNCERTAIN-lite.

| | Harvest | Productive | Kantata | Teamwork | Scoro |
|---|---|---|---|---|---|
| Grouping | task/person/project/detailed (s) | budget/service/project/task | person→task/task/detailed | task/list/date/person, text editable | 2-level (s) |
| Marked invoiced when | on invoice (s) | on any invoice, **even a draft** | on invoice | on adding to invoice | on invoice (s) |
| Delete/cancel | removing line unlocks | invoice with credit note locked | drafts deletable, active cancelled, items re-invoiceable | removing → Unbilled | unlink → Unbilled |
| Credit note | none (s) | full/partial, two-way link | cancel + re-invoice (UNC) | none found (UNC) | full/partial, can become prepayment (s) |

Sources: https://support.getharvest.com/hc/en-us/articles/4408204890381-Unlocking-invoiced-time-and-expenses ,
https://help.productive.io/en/articles/6491533-credit-note ,
https://knowledge.kantata.com/hc/en-us/articles/215314918-Cancel-or-Delete-an-Invoice ,
https://support.teamwork.com/projects/finance/adding-an-invoice ,
https://support.scoro.com/hc/en-us/articles/12664935036557-Issuing-credit-notes .

### 6.3 Patterns to take

1. The uninvoiced view lists only *approved, billable, not-yet-invoiced* items — Time's and
   Expenses' approval states and `ActualsTotals.Invoiced` already fit.
2. Grouping chosen per import; line text editable without touching the source.
3. Sources **reserved when placed on a draft** (Productive, Teamwork) so two drafts cannot
   bill the same hour; released when the line or draft is removed.
4. Invoiced sources are locked; after issue the only unlock is a **credit note that
   releases them for rebilling** (Scoro/Kantata), since Norwegian law forbids editing.
5. Fixed price by milestone, % or remaining budget; over-budget time as a zero/negative line.
6. Prepayment is a real invoice + drawdown or a recurring budget — but a pure prepayment is
   not a salgsdokument in Norway (§2.4).
7. Expense markup as % or amount with a tenant default.
8. Approval before send is optional, threshold-based where it exists.
9. Issue (number + immutable) is one step; send is a retryable delivery attempt, possibly
   on several channels.

## 7. Where the codebase stands

### 7.1 The rules that shape every seam

- **A third write direction needs its own design.** Rule 8: a module never writes another's
  data except through `contracts.CustomerReferenceHolder`; "Another write direction needs a
  design of its own" (`docs/src/content/docs/en/contributing/module-boundaries.md:67-79`); rule 9 is `CustomerPersonalData`
  (`:80-96`). Stamping time entries, expense lines or milestones as invoiced from Invoices
  is that third direction. Every other contract is a read (`srv/contracts/references.go:9-13`).
- No contract call inside a transaction holding a lock (`docs/src/content/docs/en/contributing/module-boundaries.md:245-247`;
  `srv/contracts/references.go:31-34`).
- The outbox/event bus is deferred, to be built "together with its first real consumer"
  (`ROADMAP.md:8-21`).
- **Workers see nil providers.** `module.Workers` adds only `CustomerPersonalData` to the
  base Deps (`srv/module/workers.go:20-33`); providers resolve only in `Compose`
  (`srv/module/compose.go:177-226`). A dunning or Peppol-send worker would see
  `Deps.Expenses`/`Deps.Actuals` nil.

### 7.2 Customers

- Billing profile columns: `invoice_email`, `reminder_email`, `payment_terms_days`,
  `currency`, `language`, `invoice_delivery`, `reminder_delivery`, `peppol_id`, `gln`,
  `buyer_reference` (`srv/db/migrations/00019_customers_billing_profile.sql:9-18`) and
  `default_bill_rate` (`00028_customers_default_bill_rate.sql:14`). `language` ∈ `nb`/`en`;
  `invoiceDelivery` ∈ `email`/`ehf`/`efaktura`/`paper`; `reminderDelivery` ∈ `email`/`paper`
  (`docs/src/content/docs/en/reference/customers.md:477-488`). Payment terms fall back to the group default
  (`docs/src/content/docs/en/reference/customers.md:751-759`; `srv/customers/directory.go:207-212`).
- Invoice address = primary `invoice`, else primary `postal`, else none
  (`docs/src/content/docs/en/reference/customers.md:464-468`).
- Read through `CustomerDirectory.BillingProfile` (`srv/contracts/directory.go:169-173`),
  resolved in `resolveBillingProfile` (`srv/customers/directory.go:191-239`). **No batch
  `BillingProfiles(ids)`.** `CustomerBillingProfile` (`srv/contracts/directory.go:70-111`)
  carries **no status, no `MergedInto`, no anonymised flag, no Peppol lookup answer**.
- Peppol: `internal/peppol` is lookup only (`srv/peppol/lookup.go:96,164`), shared "with the
  future Invoices module" (`docs/src/content/docs/en/contributing/module-boundaries.md:18-19`); answers store
  `can_receive_invoice` and `can_receive_credit_note` (`srv/db/migrations/00020_customers_peppol_lookup.sql:5-10`);
  a recheck worker exists (`srv/customers/peppol_recheck_worker.go`). **No access point or
  send capability.**
- Brreg `vat_registered` is stored (`srv/db/migrations/00021_customers_registry_records.sql:15`)
  but "that is Invoices' call, later" (`docs/superpowers/specs/2026-09-22-customers-brreg-full-design.md:159-160`);
  not in any contract.
- Anonymisation keeps the customer number ("the bookkeeping reference") and clears billing
  identifiers (`docs/src/content/docs/en/reference/customers.md:1601-1602,1638-1644`); "linking data to it after its day
  is the linker's responsibility" (`:1650-1653`). `CustomerReferences` and
  `CustomerPersonalData` are called for disabled modules too (`srv/module/module.go:213-233`).

### 7.3 Products

- "an invoice line later gets its product, unit and tax category for free"
  (`docs/src/content/docs/en/reference/projects.md:249-254`) — **not true through the contract**: `VariantEntry` has no
  tax category or rate (`srv/contracts/catalog.go:12-19`). The tax category sits on the
  product (`00004_products_baseline.sql:51`), unversioned, so a rate edit rewrites history
  for anything not snapshotted; "Prices and the resolved VAT rate must be snapshotted at
  transaction time" (`docs/src/content/docs/en/reference/products.md:32-36`).
- `ListPrice` returns `Money{Amount float64}` (`srv/contracts/catalog.go:23-26,43-53`).
  Products is optional (nil when disabled, `srv/module/module.go:59-64`).

### 7.4 Projects

- Project `customer_id`, `billing_type`, one `currency`, `fixed_price_amount`
  (`srv/db/migrations/00008_projects_baseline.sql:11-29`); billing lines with `variant_id`
  and a trackable code `<project>-<line>` (`docs/src/content/docs/en/reference/projects.md:1250-1254`).
- Milestones: `status planned|ready|invoiced|cancelled` with `invoiced_at`,
  `invoiced_by_user_id`, `invoice_reference varchar(100)`, `invoice_date`,
  `invoiced_amount` (`srv/db/migrations/00011_projects_milestones.sql:23-46`); stamped by
  hand through `POST /api/v1/projects/milestones/{milestoneId}/status`
  (`openapi/projects.yaml:2440-2459`). Undo is never refused because "crediting an invoice
  is a real event" (`docs/src/content/docs/en/reference/projects.md:409-423`). **No milestone read in `ProjectDirectory`**
  (`srv/contracts/projects.go:80-135`).
- Financial rights are re-derived by each consumer (e.g. `srv/expenses/authorize.go:363-369`);
  no directory method.

### 7.5 Time

- `time.entries`: `status` includes `invoiced` (`srv/time/values.go:36`), `invoiced_at`
  (`srv/db/migrations/00010_time_baseline.sql:29`) — **no `invoiced_by`, no reference, no
  frozen amount**. Nothing writes `invoiced` today; tests reach it through the database
  (`docs/src/content/docs/en/reference/time.md:192-196`); approval refuses to reopen (`srv/time/approval.go:63-64`); the
  state "has no way out" (`docs/src/content/docs/en/reference/time.md:183`).
- Invoiceable = approved, billable, with a bill rate: `hours × billRate ×
  billMultiplierPercent / 100` in `billCurrency`, "rounded once per invoice line", grouped
  by project and line, then work type (`docs/src/content/docs/en/reference/time.md:368-376`). "Time provides no contract
  yet" for it (`:365`); `ProjectActuals` is aggregates only (`srv/contracts/actuals.go:45-57`).

### 7.6 Expenses

- Stamp `invoiced_at`, `invoiced_by_user_id`, free-text `invoice_reference`
  (`srv/db/migrations/00012_expenses_baseline.sql:95-97`), set one line at a time over HTTP
  with a revision (`srv/expenses/invoiced.go:63-111,130-216`).
- The ready predicate exists in **six textual copies** (`srv/expenses/queries/projectexpenses.sql:66-76`,
  `entries.sql:179-184,206-211`, `reimbursements.sql:257-285`, `srv/expenses/invoiced.go:36-61`);
  the fix has a precedent in the `expenses.owes_employee` SQL function
  (`srv/db/migrations/00033_expenses_supplier_invoices.sql:34`).
- `ProjectExpenses` is aggregate-only (`srv/contracts/expenses.go:42-58`); invoicing needs
  lines (id, revision, date, kind, net, bill, currency, claim, supplier name/number).
- The bill side has no VAT (`00012_expenses_baseline.sql:81-85`); re-billed outlays and
  mileage differ in VAT. `supplier_invoice_number` is not unique, so re-billing twice is
  possible (`.superpowers/sdd/2026-09-26-supplier-invoices/whole-branch-backend-review.md:108-116`,
  scratch).

### 7.7 Energy

Metering points with `price_area`, supply periods, monthly-partitioned consumption
intervals (`srv/db/migrations/00005_energy_baseline.sql:25-170`) and HTTP consumption
aggregates (`openapi/energy.yaml:491-549,821-922`). **No prices, spot prices, tariffs or
`ConsumptionProvider`**; Energy fills no provider slot (`srv/energy/module.go:58-59`).

### 7.8 The missing seller record

No installation-level company record exists: no legal name, org number, address, bank
account, MVA flag, logo or number settings. `identity.system_settings` holds only
maintenance mode (`srv/db/migrations/00002_identity_baseline.sql:248-254`);
`config.Branding` is SPA whitelabeling (`srv/config/config.go:39-48,766-777`). Module-owned
settings rows are the pattern (`expenses.settings`, `00012_expenses_baseline.sql:40-47`).
One Compose project per tenant means per installation = per tenant.

### 7.9 Documents and mail

- **No PDF, templating or UBL library** in `apps/server/go.mod:5-32`, no
  `text/template`/`html/template` in `srv/`; `encoding/xml` only in the SMP client.
- `mail.Sender.Send` is plain text, no attachments (`srv/mail/mail.go:19-32`).
  **`mail.SendOutbound`** takes To/Cc/Bcc, text + HTML and `[]Attachment` with per-channel
  config (`srv/mail/outbound.go:22-75`) — its one producer is communications' outbox
  worker. Communications offers no contract to enqueue mail (`srv/contracts/`; attachments
  "can be staged but never sent", `docs/src/content/docs/en/reference/communications.md:249-252`).
- Storage: `ObjectStore{Put, Get, Exists, Delete}` (`srv/storage/storage.go:61-72`),
  `fs` driver only, no presigned URLs, nothing immutable/WORM (`docs/src/content/docs/en/admin/object-storage.md:9-23,107-122`).

### 7.10 Numbering, money, adding a module

- **Gapless counter precedent**: `customers.counters` upsert — "not a Postgres SEQUENCE,
  matching .NET's gapless-under-rollback semantics"
  (`srv/db/migrations/00003_customers_baseline.sql:114-121`;
  `srv/customers/queries/customers.sql:1-10`); Projects copies it with `PeekCounterValue`
  (`srv/projects/queries/counters.sql:1-24`). No per-year reset or prefix.
- **Money**: `numeric(12,2)` in the DB; `math/big.Rat`, rounded once half away from zero
  (`docs/src/content/docs/en/reference/expenses.md:112-118`; `docs/src/content/docs/en/reference/time.md:69-72`); contracts mixed — decimal text in
  `ActualsBucket`/`ExpenseBucket` "so no float ever rounds money"
  (`srv/contracts/actuals.go:67-70`) vs `float64` in `Money`, `DefaultBillRate`,
  `FixedAmount`. Currency is never converted anywhere (`srv/contracts/actuals.go:27-39`).
- Only Expenses has a time zone setting (`srv/expenses/settings.go:61-70`).
- Adding a module: `docs/src/content/docs/en/contributing/module-boundaries.md:268-318` (openapi, baseline migration,
  depguard, `moduleSchemas`, config dependency check, frontend app, one admin catalog entry
  per permission). A new provider slot is platform code (`srv/module/module.go:28-161`,
  `srv/module/compose.go:177-226`). `contracttest.RequireCoverage` is the operations gate
  (`srv/openapi/contracttest/contracttest.go:261-280`).
- **Next migration: `00034`** (last is `00033_expenses_supplier_invoices.sql`).

### 7.11 What a "ready to invoice" pull needs

| Source | Exists | Missing |
|---|---|---|
| Milestones | HTTP list + status door | contract read; stamp is per-milestone HTTP with revision |
| Time | aggregate `ProjectActuals` incl. `Invoiced` | line read (ids + revision); any stamp writer |
| Expenses | aggregate `Ready*`; HTTP `toInvoice=true` | line contract read; bulk atomic stamp/un-stamp; one predicate |
| Per customer | `ProjectsForCustomer` (≤ 2000) → batch reads | nothing takes a customer id for hours/expenses |
| Energy | HTTP consumption | `ConsumptionProvider`, any price |

## 8. PDF rendering

| Option | Verdict |
|---|---|
| **maroto v2** (https://github.com/johnfercher/maroto, MIT, v2.4.2 2026-09-10) | pure Go, grid layout, tables, QR/barcodes; fonts embedded via `go:embed` |
| go-pdf/fpdf, gofpdf, gopdf | pure Go but low-level; fpdf archived |
| unipdf | commercial metered licence — unsuitable for self-hosted |
| chromedp + headless Chrome | HTML templates, but Chromium ≈ 150 MB compressed; cannot run in `distroless/static` |
| Gotenberg | Chromium + LibreOffice sidecar ≈ 700 MB compressed, another service per tenant stack |
| Typst (https://github.com/typst/typst, Apache-2.0, v0.15.1) | excellent typography; ≈ 17 MB static binary copyable into distroless; exec'd from Go; PDF/A **UNCERTAIN** |
| wkhtmltopdf | archived, do not use |

The runtime image is `gcr.io/distroless/static-debian12:nonroot` (repo `Dockerfile`) — no
shell, no libc, no fonts — which rules out Chromium in-process. **Choice: maroto v2 with an
embedded open-licence font** (Inter or Noto Sans, for æøå and "kr"). It keeps the single
static binary. An invoice is a fixed layout: logo header, parties, meta block, line table,
VAT per rate, totals, payment block (account, KID, due date), footer with org no + MVA +
Foretaksregisteret; branding = logo, accent colour, footer text. **Typst** as a bundled
binary is the upgrade path if richer templates are wanted.

Rendering is **once, at issue**: store the bytes with a hash and never re-render an issued
invoice with a newer template (§ 5-2-9). For e-mail the PDF *is* the salgsdokument; over
Peppol the XML is, and the same stored PDF is embedded. **UNCERTAIN:** whether maroto can
produce PDF/A-3; no Norwegian PDF/A requirement found. **UNCERTAIN:** AP attachment size
limits.

## 9. Recommended roadmap

**Phase 1 — the salgsdokument, with manual lines.**
Seller record (legal name, org no, MVA flag, Foretaksregisteret flag, address, bank
account(s), logo). Numbering series with a locked start and gap-free allocation inside the
issue transaction. Invoice aggregate draft → issued → credited; lines with description,
quantity, unit, unit price, discount, VAT code; VAT summary per rate; buyer snapshot from
`BillingProfile` (address, buyer reference, terms → due date, currency, language);
delivery date or period. Deterministic stored PDF; e-mail delivery through `mail`. Full
and partial credit notes. Manual payment registration with partial payments and derived
status. An invoice journal proving no gaps, and a CSV export for the accountant. Currency
and exchange rate modelled from day one even if the UI allows only NOK.
*Why first:* nothing else is lawful without numbering, immutability and credit notes, and
every later source only produces lines. Currency is cheap now and expensive to retrofit.

**Phase 2 — EHF and KID (moved up).**
UBL 2.1 Invoice/CreditNote per BIS 3.0 + NO rules, local Schematron validation, an
`AccessPoint` port with one adapter, a receiver check against ELMA/SMP per document type at
send, delivery status and receipts, embedded PDF. KID (MOD10/MOD11, tenant-configured) on
the PDF and in `PaymentID`.
*Why here:* the B2B sending duty starts 2027-01-01 and B2G needs EHF already; UBL is data
mapping over a phase-1 model built for it (0192, buyer reference, VAT categories).

**Phase 3 — work from Projects, Time and Expenses.**
An uninvoiced-work view per customer/project (approved time, re-billable expenses and
supplier invoices, ready milestones); "create invoice from…" with grouping (project, work
type, person, date, detailed) and a timesheet attachment; expense markup; milestone lines;
a-konto invoices and a final invoice deducting them. **The write-back contract:** issuing
stamps sources as invoiced with the **invoice id and number**, replacing hand-typed
references; crediting may release sources for rebilling. Prerequisites: the rule-8 design
for a third write direction, line-level reads, the one expenses predicate, and a stamp for
Time, which has no writer.
*Why after EHF:* biggest UX surface, depends on the aggregate; could swap with phase 2 only
if no tenant invoices B2B before 2027 — given the mandate, don't.

**Phase 4 — payment files and dunning.**
OCR giro and camt.054 import with KID matching and an exception queue; reminder runs
(purring, inkassovarsel) with the 14-day rules and fee caps from dated tables; late
interest (12.25 % from 2026-07-01) and NOK 430 B2B compensation; per-customer dunning
opt-out (parked in `ROADMAP.md:325-329`); inkasso export.
*Why here:* matching needs the KID; dunning needs reliable payment state. Needs a worker
that can see its providers (§7.1).

**Phase 5 — energy.**
Periodic invoices per supply point/period, tariff and price lines, batch issue.
*Why late:* needs batch issue, schedules, mature payment matching, and the heaviest volume;
Energy has no prices or `ConsumptionProvider` yet.

**Later:** recurring invoices (§ 5-2-6, up to a year ahead; fits with energy as scheduled
batch issue); eFaktura B2C and AvtaleGiro; a customer portal; a bank API; several legal
entities per tenant; a SAF-T-friendly posting export or integrations to
Tripletex/Fiken/PowerOffice.

## 10. Decisions the phase 1 design must take

1. **Seller record.** Where the tenant's legal identity lives (org no, MVA, AS vs ENK →
   Foretaksregisteret, bank accounts, logo, e-mail sender, Peppol id); one per tenant or
   several legal entities, each with its own series and ELMA registration.
2. **Numbering.** Shared or separate credit-note series; per year or continuous; start
   number and when it locks; reuse the counters upsert (serialises on the row — acceptable
   throughput?) with the number taken at issue, not draft.
3. **Drafts.** PDF preview with "UTKAST" watermark and no number; `invoices:issue` separate
   from editing; any approval step.
4. **Rounding.** Per-rate VAT on line sums (BR-CO-17); decimals for price and quantity; no
   øreavrunding; discount as allowance or net price; `big.Rat` and decimal-text contracts.
5. **VAT codes.** The tenant table (SAF-T, EHF category, rate, validity, exemption text);
   defaults from product, work type, expense category or customer (foreign → 52 or reverse
   charge) and precedence; the missing tax data on `VariantEntry`, unversioned
   `tax_categories`, no VAT on expense bill amounts, none on milestones; foreign VAT numbers
   not stored.
6. **The write direction.** Contract command in a shared transaction, an outbox, or read-only
   Invoices with each module's manual door kept (rule 8, `docs/src/content/docs/en/contributing/module-boundaries.md:78-79`);
   who owns `invoiced_at` for hours.
7. **One stamp protocol for three shapes** (time status + `invoiced_at`; expense stamp + by +
   reference; milestone status + by + reference + date + amount); does the stamp carry an
   opaque invoice id.
8. **Reserve or lock.** Sources reserved on a draft or only at issue; release on draft delete;
   on credit, release or stay invoiced — Time's `invoiced` has no way out today.
9. **Line-level reads** for time entries and expense lines: new methods or contracts, and
   the one-predicate refactor first.
10. **Currency.** NOK only at launch or foreign from day one; rate source (Norges Bank or
    manual) and date (invoice date); NOK VAT storage; customer vs project vs expense-line
    currency can all differ.
11. **Delivery.** Channel precedence (EHF when ELMA accepts the document type, else the
    profile's choice); fallback when EHF fails; whether PDF-by-e-mail to a Norwegian business
    stays lawful after 2027 (pending regulation); `mail.SendOutbound` with process `SMTP_*`
    or a communications channel or a new communications contract.
12. **Access point.** Which provider; does the tenant or the Vantigo host hold the AP
    contract; credentials in `secrets`.
13. **KID.** MOD10 or MOD11, length, composition; only when the tenant has a KID agreement.
14. **A-konto and prepayment.** VAT-bearing invoice type vs non-VAT payment request; how the
    final invoice nets them.
15. **Retention vs GDPR and merges.** Buyer snapshot on every issued document; Customers'
    anonymisation must refuse or skip customers with invoices inside 5 years after year-end;
    whether Invoices implements `CustomerReferences` (re-point issued invoices?) and
    `CustomerPersonalData`; the profile lacks `MergedInto` and an anonymised flag.
16. **`disabled`.** Is it "blocked for invoicing"; it is not in the directory contract.
17. **Payment state.** Derived from allocations; overpayment and customer credit balance;
    write-off tolerance.
18. **Authorization.** Own permission set plus the project financial-rights rule
    (`srv/expenses/authorize.go:363-369`) or a directory method for it.
19. **Module dependencies.** `invoices requires customers` presumably; projects, time,
    expenses, products, energy optional as nil `Deps` slots.
20. **Workers.** Dunning and Peppol send jobs see nil provider slots in worker mode
    (`srv/module/workers.go:20-33`).
21. **Energy scope** for the first delivery — no prices, tariffs or provider exist.
22. **Duplicate supplier invoices** re-billed twice (non-unique number): warn at invoice time?
23. **Posting.** Stay a sub-ledger with exports/integrations, or grow a general ledger — this
    decides whether the 2030 digital-bookkeeping duty concerns Vantigo at all.
