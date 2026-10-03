# Invoices phase 2 — EHF over Peppol, and KID — research

Research for Invoices phase 2 (`ROADMAP.md:787-797`): three external studies — the EHF
document, the access point, KID — and an inventory of the codebase's seams, building on
the 1A research (`docs/superpowers/research/2026-09-26-invoices-module.md`, "1A" below)
and saying where it corrects it. Every external source was read on **2026-10-03** unless another
date is given. Vendor and advisory pages are secondary sources only; anything not
confirmed from a primary source is marked **UNCERTAIN**. Code references are `file:line`
against `feat/invoices-ehf-peppol-kid` at `00daa9f5` (identical to `main`); `srv/`
abbreviates `apps/server/internal/`, and `invoices.md` the reference page
`docs/src/content/docs/en/reference/invoices.md`.

## 1. Why now

### 1.1 The duties and their dates

- **B2B sending: 2027-01-01.** Lov 19. juni 2026 nr. 39
  (https://lovdata.no/dokument/LTI/lov/2026-06-19-39) puts the sending duty — part I
  §§ 3, 10, 11, 13 — in force on 1 Jan 2027, per the Stortinget case page
  (https://www.stortinget.no/no/Saker-og-publikasjoner/Saker/Sak/?p=200136). Re-confirmed
  this pass; 1A §1.2 has the legislative trail. The formal in-force
  resolution text is still unseen — **UNCERTAIN**, as 1A left it.
- **Receiving and digital bookkeeping: 2030-01-01** (part I § 7 (4), same page).
  Receiving EHF is Expenses' concern, not this phase's, and needs the tenant's own ELMA
  registration (1A §4.3).
- **B2B only.** The duty binds bokføringspliktige invoicing each other; B2C is not covered
  (1A §1.2; re-confirmed by vatcalc.com, sovos.com and comarch.com, 2026, secondary). No primary source makes a **PDF by e-mail to a consumer or a foreign buyer**
  unlawful after 2027-01-01 — the text conditions on a Norwegian bookkeeping-duty
  counterparty — **UNCERTAIN** in the negative. VATupdate's "conditional on the buyer
  being registered in ELMA" stays **UNCERTAIN** (1A §1.2).
- **The format regulation is pending.** Skattedirektoratet reports by **15 Dec 2026** — a
  report-back, not the regulation's date; EHF is the expected format by every secondary
  source and the 2019 precedent — **UNCERTAIN**, nothing published as of 2026-10-03.
- **B2G since 2019.** FOR-2019-04-01-444 § 4 approves EHF Billing 3.0 / Peppol BIS Billing
  3.0 or newer (https://lovdata.no/dokument/SF/forskrift/2019-04-01-444/%C2%A74); a
  tenant invoicing a municipality cannot meet it today.

### 1.2 What 1A and 1B deliver, and what they cannot

1A issues a numbered, immutable invoice or credit note with buyer and seller snapshots,
VAT per (category, rate) and a PDF stored once (`invoices.md:9-19`, `:316-342`). 1B adds
payments, derived states, e-mail delivery of the stored PDF, the CSV export and stats
(`invoices.md:14-19`). The reference opens with the limit in a callout: "a PDF by e-mail
is not an e-invoice; EHF over Peppol is phase 2" (`invoices.md:21-25`).

The send already says so to the user, as four warnings that never refuse
(`srv/invoices/send.go:41-44`, `invoices.md:499-509`): `delivery_preference_ehf` (the
profile says `ehf`), `delivery_preference_other`, `buyer_norwegian_business` (the snapshot
has an organisation number, before 2027) and `buyer_norwegian_business_required` (the same
from `b2bDutyFrom`, 2027-01-01, `send.go:47-50`). The public-body warning was dropped
because the directory has no such fact (`invoices.md:511-518`). Phase 2 is what those
warnings point at: without it, every 1B send to a Norwegian business from 2027-01-01 is
flagged non-compliant and nothing in the app can do better.

## 2. The EHF document

### 2.1 EHF Billing 3.0 is Peppol BIS Billing 3.0

**(Correction to how 1A §4.1 framed it.)** There is no Norwegian CIUS and no Norwegian
CustomizationID: EHF Fakturering 3.0 "is based on the Peppol BIS billing 3.0
specification", implemented "without extensions or extra rules"
(https://github.com/anskaffelser/ehf-postaward-g3 , `docs/billing-3.0/norway/main.adoc`;
https://anskaffelser.dev/postaward/g3/spec/current/billing-3.0/norway/) — beyond two
country Schematron checks, **NO-R-001** and **NO-R-002**, registered under OpenPeppol's
country-rule policy (PDF not read in full — **UNCERTAIN** on its conditions). One
document, one artefact release schedule (§3).

### 2.2 The identifiers

Sources: https://docs.peppol.eu/poacc/billing/3.0/bis/ ,
https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/tree/ ,
https://docs.peppol.eu/poacc/billing/3.0/codelist/eas/ ,
https://docs.peppol.eu/poacc/billing/3.0/codelist/UNCL1001-inv/ .

| Element | Value |
| --- | --- |
| `cbc:UBLVersionID` | `2.1` (the document type id's `::2.1` suffix) |
| `cbc:CustomizationID` | `urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0` |
| `cbc:ProfileID` | `urn:fdc:peppol.eu:2017:poacc:billing:01:1.0` (Profile 01) |
| Process id (SBDH/AS4, not in the UBL) | `cenbii-procid-ubl::urn:fdc:peppol.eu:2017:poacc:billing:01:1.0` — the scheme prefix quoted from the BIS page, the tail mirroring the ProfileID; **UNCERTAIN** only in that no page printed the literal end to end |
| Invoice document type | `busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1` |
| CreditNote document type | the same with `CreditNote-2::CreditNote` — **UNCERTAIN**: pattern-substituted, not read verbatim on a spec page |
| Participant scheme | **0192** = Organisasjonsnummer (Enhetsregisteret) on the EAS list; **0208 is Belgium's** enterprise number. `0192:<9 digits>` for seller and buyer |
| `cbc:InvoiceTypeCode` | **380** (commercial invoice). 384 corrected and 389 self-billed are allowed and not ours |
| `cbc:CreditNoteTypeCode` | **381**. 396 factored is not ours |

Both document type strings already ship as unexported constants the SMP lookup compares
as whole strings (`srv/peppol/smp.go:36-42`, commented with participants observed live on
2026-09-21, `:14-35`). The credit-note string is thus a shipped assumption with no
primary-page quote behind it; confirm it against a live ServiceGroup before the send path
relies on it too. Profile 02, "Billing with Response" (`urn:peppol:bis:billing_with_response`,
3.0.21) is **UNCERTAIN** (search summary) and matters only for Invoice Response (§4.2).
**380/381, never a negative 380**: BIS 3.0 allows a negative Invoice as a credit
(peppolvalidator.com, e-invoice.be, secondary), but the module has a first-class numbered
credit note (§ 5-2-7, `invoices.md:48-49`).

### 2.3 The mandatory content, mapped to what the module already holds

The module was designed for this mapping (`invoices.md:96-98`, "gross less allowance, as
EHF expresses a discount"; `srv/invoices/money.go:33-39` keeps gross and allowance
separately "so the PDF and a later EHF agree (PEPPOL-EN16931-R120)"). Columns are in
`srv/db/migrations/00034_invoices_baseline.sql` (invoices `:99-198`, lines `:204-233`,
VAT summaries `:237-248`).

| EN 16931 term | UBL | Source in the module |
| --- | --- | --- |
| BT-1 number, BT-2 issue date | `cbc:ID`, `cbc:IssueDate` | `number`, `issue_date` |
| BT-3 type code | `InvoiceTypeCode` / `CreditNoteTypeCode` | `kind` → 380 / 381 |
| BT-5 currency | `DocumentCurrencyCode` | `currency` (NOK only, `invoices.md:69-71`) |
| BT-9 due date | `cbc:DueDate` | `due_date`; absent on a credit note by CHECK (`invoices.md:44-47`) |
| BT-10 buyer reference / BT-13 order reference | `cbc:BuyerReference` / `cac:OrderReference/cbc:ID` | `your_reference` (`00034:120`, prefilled from the profile's buyer reference, `invoices.md:105-106`), `order_reference` (`:122`) |
| BT-25/26 preceding invoice | `cac:BillingReference/cac:InvoiceDocumentReference` | `credits_invoice_id` → the original's number and issue date |
| BT-27/30/31 seller name, legal id, VAT id | `PartyLegalEntity`, `PartyTaxScheme[VAT]` | seller snapshot `legal_name`, `organisation_number`, `vat_registered` → `NO<orgnr>MVA` |
| BT-35–40 seller address | `cac:PostalAddress` | seller snapshot address, `country` |
| BT-44/47 buyer name, legal id | `PartyLegalEntity` | buyer snapshot `name`, `organisation_number` |
| BT-49 buyer electronic address | `cbc:EndpointID@schemeID` | buyer snapshot `peppol_id` (`00034:140`), set from the profile's resolved `PeppolID` (`srv/invoices/issue.go:101`) |
| BT-50–55 buyer address | `cac:PostalAddress` | buyer snapshot address, `country` |
| BT-72 / BG-14 delivery | `cac:Delivery/cbc:ActualDeliveryDate` / `cac:InvoicePeriod` | `delivery_date` / `delivery_from`–`delivery_to` |
| BT-81/84/86 payment means, account, BIC | `cac:PaymentMeans` | seller snapshot `bank_account`, `iban`, `bic` |
| BG-23 VAT breakdown | `cac:TaxTotal/cac:TaxSubtotal` | `invoices.vat_summaries`: `taxable_amount`, `vat_amount`, `vat_category`, `rate_percent`, `exemption_reason` |
| BG-22 totals | `cac:LegalMonetaryTotal` | `net_total`, `vat_total`, `gross_total` |
| BG-25 line | `cac:InvoiceLine` / `cac:CreditNoteLine` | `position`, `description`, `quantity` (3 dec.), `unit_price` (4), `line_gross`/`allowance`/`net`, VAT snapshot |

Already satisfied by design: per-rate VAT on summed nets (BR-CO-17, `money.go:79`); line
nets summing to the document net (BR-CO-10, `invoices.md:199-200`); the NO-R-001
organisation-number MOD-11 (the module's own check on `PUT /settings`,
`srv/invoices/settings.go:53-66`, `invoices.md:775`; the rule's severity is inferred fatal
from its "MUST" — **UNCERTAIN** at label level); a free-text reason on every non-`S` VAT
code, held by a CHECK (`00034:73`).

### 2.4 The gaps

1. **`EndpointID@schemeID="0192"` for both parties.** The buyer's is in the snapshot
   when the profile resolved one; the **seller has no Peppol id** anywhere —
   `invoices.settings` (`00034:19-43`) holds none. It is derivable as
   `0192:<organisation_number>`, which is what a sender without its own registration
   would put there (§5.5); whether to store it is a decision (§10).
2. **The buyer's electronic address can be missing.** The snapshot's `buyer_peppol_id`
   is NULL for a person or a foreign buyer, and an issued snapshot is frozen. An EHF for
   such a document cannot be built; the send must refuse, not fall back silently.
3. **R003, buyer or order reference.** PEPPOL-EN16931-R003 (fatal) needs `BuyerReference`
   or an order reference (1A §4.1). Both columns default to `''` (`00034:120-122`) and
   nothing requires either at issue. An EHF-bound document needs one; today's data can
   lack it.
4. **Payment means for KID.** `PaymentMeansCode` **30** (BBAN) or **58** (IBAN),
   `PayeeFinancialAccount/cbc:ID`, `cbc:PaymentID` for the KID (§6.5); BR-50 requires the
   account whenever credit-transfer means is given, and the BIC goes in
   `FinancialInstitutionBranch/cbc:ID` **only with an IBAN**
   (https://github.com/anskaffelser/ehf-postaward-g3/blob/main/docs/billing-3.0/norway/description/payment-information.adoc).
   No KID or payment-means concept exists in the schema.
5. **The Foretaksregisteret element.** NO-R-002 tests
   `normalize-space(PartyTaxScheme[TaxScheme/ID='TAX']/CompanyID) = 'Foretaksregisteret'`
   (https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/NO-R-002/); the PDF prints
   the word (`invoices.md:66-67`), the UBL needs the second `PartyTaxScheme` when
   `seller_in_foretaksregisteret` (`00034:147`) is true. The test is **unconditional** —
   the AS/ASA/NUF condition is prose only — so a sole proprietor gets it on every
   document; severity **warning** per two secondary sources — **UNCERTAIN** at label level.
6. **The line discount as `cac:AllowanceCharge`** (`ChargeIndicator=false`, `Amount`,
   `BaseAmount` with `MultiplierFactorNumeric`, tied by PEPPOL-EN16931-R040–R042,
   https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/). `discount_percent` and
   `line_gross` are stored, so it can be built exactly.
7. **VATEX codes** (https://docs.peppol.eu/poacc/billing/3.0/codelist/vatex/):
   `VATEX-EU-AE`, `-G`, `-O` for the seeded `51`, `52`, `7` (`invoices.md:91-93`).
   **(Correction to 1A §3.4.)** No VATEX code fits a domestic exemption (`E`, code `6`) or
   zero rate (`Z`, code `5`); the free-text BT-120 the module holds is the carrier. The
   `BR-E-*`/`BR-AE-*`/`BR-G-*` conditionality was not fetched — **UNCERTAIN**.
8. **The credit note's `BillingReference`** (BT-25/26) from `credits_invoice_id`, and the
   type code from `kind`: 380 or 381, nothing else (§2.2).
9. **The unit of measure.** `InvoicedQuantity@unitCode` takes a UN/ECE Rec. 20/21 code
   (`HUR`, `C62`); `invoices.lines.unit` is free text, ≤ 20 characters, default `''`
   (`00034:210`, `srv/invoices/drafts.go:236`). Not in the three studies — the rule (BR-23)
   is **UNCERTAIN** on id and wording here, but no UBL line exists without a `unitCode`.
10. **Decimals (BR-DEC).** The module's 2/3/4 decimals (`invoices.md:82`) match the EN
    16931 convention; the `BR-DEC-*` rules were not fetched — **UNCERTAIN** rule by rule.
11. **Seller contact (BT-41–43)** is optional; the settings' e-mail could fill it.

Not a gap: foreign currency. When a later phase allows it, PEPPOL-EN16931-R053/R054/R005
and BR-53 require a second, subtotal-less `TaxTotal` in NOK; the rate columns are already
there, fixed at 1 (`invoices.md:69-71`).

### 2.5 Edge cases proven safe

**The final credit note's squaring row.** 1A's credit note can carry "a row at that rate
with a taxable amount of 0.00 and the small negative VAT remainder" (`invoices.md:191-200`),
and the reference says "the EHF phase (2) must be able to carry a negative VAT row too"
(`:196-197`). BR-CO-17's test, verbatim from
https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-tc434/BR-CO-17/ :

```text
(round(Percent) = 0 and round(TaxAmount) = 0)
or (round(Percent) != 0 and
    (abs(TaxAmount) - 1 < round(abs(TaxableAmount) * (Percent div 100) * 10 * 10) div 100)
    and
    (abs(TaxAmount) + 1 > round(abs(TaxableAmount) * (Percent div 100) * 10 * 10) div 100))
or (not(exists(Percent)) and round(TaxAmount) = 0)
```

BR-S-09 (`…/BR-S-09/`) has the same shape. **The tolerance is a full krone, and both
sides take `abs()`.** For `TaxableAmount=0.00`, `TaxAmount=-0.03` at 25 %: expected 0.00;
`0.03 − 1 < 0` and `0.03 + 1 > 0` — both pass. No `PayableRoundingAmount` (BT-114) or
document-level `AllowanceCharge` is needed; only more than a krone on one row would fail.
BR-CO-10 is exact two-decimal rounding with no slack (`…/BR-CO-10/`), which the
per-line-then-sum discipline already meets.

**Positive totals on a credit note.** A 381 carries its totals **positive**; the type code
signals the credit (peppolvalidator.com, e-invoice.be, secondary). That is how the module
stores one (`invoices.md:84`) — the serializer must not reuse the journal's or the CSV's
display sign (`:561-563`, `:590-591`). Peppol's own example has negative lines
(`CreditedQuantity=-3`); no rule found forces either sign, so positive lines, matching
storage, are lawful — **UNCERTAIN** only in that no rule was found either way.

## 3. Validation

**The artefacts.** The UBL 2.1 XSD, then Schematron: the EN 16931 core rules
(`ConnectingEurope/eInvoicing-EN16931`, tagged releases, pre-compiled to XSLT) and
Peppol's BIS and country rules (`OpenPEPPOL/peppol-bis-invoice-3`, where NO-R-002 is
`rules/unit-UBL-NO/NO-R-002.xml`). Two scheduled releases a year, spring (~May) and
autumn (~November), each mandatory about three months later, plus hotfixes
(https://docs.peppol.eu/poacc/billing/3.0/release-notes/):

| Version | Published | Mandatory from |
| --- | --- | --- |
| 3.0.21 (spring) | May 2026 | 2026-08-17 |
| 3.0.20-hotfix | 2026-01-27 | 2026-02-23 |
| 3.0.20 (autumn) | 2025-11-24 | 2026-02-23 |
| 3.0.19 (spring) | 2025-05-21 | 2025-08-25 |

3.0.21 bundles EN 16931 artefacts 1.3.16 (2026-04-10). A pinned artefact must be upgraded
twice a year, or the installation validates against superseded rules.

**Why `xmllint` cannot run them.** The artefacts are XSLT 2.0; libxml2 is XSLT 1.0 — every
source agrees. The reference processor is **Saxon**; Saxon-HE suffices. Size is no concern
at the 500-line cap (`invoices.md:100`; the one slow case was a 6.9 MB invoice). **No
pure-Go Schematron exists** as far as found: `github.com/knroy/go-xml` (pure-Go XSLT 2.0
claimed, single maintainer, untested against custom functions such as NO-R-001's
`u:mod11`) and `github.com/chrisdutz/gosaxon` (a GraalVM-native Saxon-HE behind Go) are
both **UNCERTAIN** — spikes at most. The runtime image is distroless static (1A §8): no
JVM, no shell. **Hosted validators**: anskaffelser.dev's
(https://anskaffelser.dev/service/validator/ , not fetched) is a UI on the Java library
vefa-validator (https://github.com/anskaffelser/vefa-validator); no documented public REST
contract was found — **UNCERTAIN**; third-party wrappers would see every invoice.

**The options.**

| Option | For | Against |
| --- | --- | --- |
| (a) Saxon running the official artefacts | the only way to run the published rules with no re-implementation; catches fine print like BR-CO-17's ±1 and NO-R-002's unconditional test | a JVM (or a native Saxon) next to a static Go binary; per-installation footprint |
| (b) a hosted validator | nothing to run | uptime and contract unknown; sees every invoice; not an official endpoint |
| (c) Go-side checks | fast, in-process, typed errors in the module's own codes | ~150+ rules re-implemented; drifts every spring and autumn |

**Recommendation.** Saxon running the official artefacts as **the test oracle in CI**:
golden UBL files from the module's fixtures (each VAT category, a partial and a final
credit note with the squaring row, a discount, reverse charge) validated against the
pinned release, so every serializer change and every artefact upgrade meets the real
rules. At runtime **a Go fast-fail pre-check** of the module's own preconditions — buyer
endpoint, R003 reference, bank account, unit code, a KID valid under the agreement —
refusing with the module's own 409 codes. Hosted validators as a manual cross-check only.
Saxon at send time too (a sidecar, `gosaxon`) is a decision (§10); whether the chosen
access point validates on receipt was not established — **UNCERTAIN**.

## 4. Attachments, receipts and status

### 4.1 The embedded PDF

`cac:AdditionalDocumentReference/cac:Attachment/cbc:EmbeddedDocumentBinaryObject`,
Base64, with **mandatory** `@mimeCode` (`application/pdf`) and `@filename`
(https://docs.peppol.eu/poacc/billing/3.0/syntax/ubl-invoice/cac-AdditionalDocumentReference/cac-Attachment/cbc-EmbeddedDocumentBinaryObject/),
unchanged in EHF. A visual copy is **recommended, not mandatory** (1A §4.1). No normative
size limit: AP practice says under 3 MB per attachment, EHF Ordering 5 MB, ~10 MB per
message at "many access points". The stored PDF is far below, `loadStoredPDF` caps a
read at 20 MiB and verifies the hash (`srv/invoices/pdfstore.go:343`), and the download's
file name (`:282`) is the `@filename`. A hard MimeCode list — **UNCERTAIN**, immaterial.

### 4.2 What comes back

- **AS4 receipt** — every message. The receiving access point (corner 3) accepted the
  bytes; nothing about the business.
- **MLR → MLS.** MLR needed the buyer's own system to answer and mostly did not; **MLS
  (Message Level Status)** replaces it — finalised May 2025, 1.1.0 on 7 Jul 2026
  (https://docs.peppol.eu/poacc/upgrade-3/2025-Q4/syntax/MLR/tree/ ; vatupdate.com and a
  SAP community post agree, secondary). **APs must send MLS by 2027-03-31** (T3, OpenPeppol
  Service Provider Operational Guideline, 2 Jul 2026) — the AP's duty, not Vantigo's.
  Technical status, **never a business decision**. The receiving-side date and a given
  AP's support are **UNCERTAIN**.
- **Invoice Response** — a buyer-initiated business document with statuses `AB`, `IP`,
  `UQ`, `CA`, `RE`, `AP`, `PD`
  (https://docs.peppol.eu/poacc/upgrade-3/profiles/63-invoiceresponse/). Needs Profile 02
  and an SMP registration on **both** sides — the sender would have to register to
  receive responses. Optional, and not in the roadmap's phase 2 line.

### 4.3 What "sent" may honestly mean

In rising strength: (1) the provider accepted the submission; (2) the receiving AP
returned the AS4 receipt; (3) MLS, still technical; (4) an Invoice Response, the only
business acknowledgement. 1B's standard for e-mail is already explicit — "a delivery row
means the mail server accepted the mail, not that it arrived" (`invoices.md:417-420`).
Phase 2 should hold the same line: **"delivered" means (2), the AS4 receipt**, worded as
"delivered to the receiver's access point"; (1) is "queued with the access point"; nothing
in the UI says "received", "approved" or "seen" until (4) exists. For Storecove,
`succeeded` is (2) and realistically the last state a sender not advertising Invoice
Response will see (§5.3).

## 5. The access point

### 5.1 The three styles

| Style | What it takes | Per installation |
| --- | --- | --- |
| (a) become a certified AP | OpenPeppol membership and AP certification under the TIA, ISO 27001, an OpenPeppol PKI certificate, a 24/7 AS4 endpoint; DFØ (the Norwegian Peppol Authority) adds 99.5 % availability, 2 GB payloads and ≥ 3 months of logs (https://anskaffelser.dev/payment/g2/docs/draft/requirements-ap/ , summary only — **UNCERTAIN**) | yes — every installation an AP |
| (b) a commercial AP's REST API | an account and an API key | no — certification sits with the provider |
| (c) self-hosted AS4 behind an internal shim | (a)'s obligations, with Oxalis-NG (https://github.com/OxalisCommunity/oxalis-ng) or phoss-ap (https://github.com/phax/phoss-ap, Apache-2.0, Java 21+, Postgres, MLS support) as the engine | yes |

OpenPeppol's fees (https://peppol.org/join/fees/ , from 1 Jul 2025): AP certification
**€1,500** first and yearly; sign-up **€1,050–2,950**; membership **€1,850–5,500/yr** by
employee band. DFØ is the Norwegian Authority
(https://www.anskaffelser.no/english/e-procurement/norwegian-agency-public-and-financial-management-dfo-peppol-authority).
Whether one certified AP may send for several legal entities under one certificate — how
(b) providers evidently work — was not checked against the membership terms —
**UNCERTAIN**, and the question that would decide a Vantigo-operated shared AP.

### 5.2 The recommendation: a commercial AP behind a port

**Style (b)**, behind an `AccessPoint` port (send, status, receipt), as 1A §4.2 already
proposed. For a self-hostable product the arithmetic is decisive: "every infrastructure
piece multiplies per dedicated customer deployment" (`ROADMAP.md:8-21`), and (a) or (c)
puts a certification, a PKI certificate, an uptime obligation and up to €7,000 a year on
an installation that sends twenty invoices a month. Under (b) the installation brings an
API key, the server makes an HTTPS call it already knows how to make (the Brreg and SMP
clients, §7.3), and Vantigo still builds and validates the UBL itself — the provider
transports, it does not author. (b) is also the only style with public, self-serve,
testable documentation today, which an honest adapter test needs (§5.6).

### 5.3 The providers

| Provider | Public API docs | Auth | Payload | Idempotency | Status | Sandbox |
| --- | --- | --- | --- | --- | --- | --- |
| **Storecove** | https://www.storecove.com/docs (OpenAPI) | Bearer API key | UBL, or its own JSON | `idempotencyGuid` body field; a repeat is **422** | webhooks (push, retried ≤ 5 days) or a pull queue; evidence endpoint | yes, with test participant ids on OpenPeppol and other networks |
| **Qvalia** | https://qvalia.com/help/how-to-access-peppol-api-step-by-step-guide/ | API key per environment | UBL 2.1 XML or UBL-shaped JSON | not found | not detailed | yes, `api-qa.qvalia.com`, same endpoints as production |
| **SendRegning** | https://sendregning.github.io/ (REST, HATEOAS) | API key (guide not fetched) | its own draft fields, then issue-and-send; `ehf` shipment method with an optional org number | not found | a history endpoint; no webhook found — **UNCERTAIN** | **none**: "there's no test environment… every request… will have consequences on production" |
| **e-invoice.be** | https://e-invoice.be/peppol-api | API key | flat JSON (also UBL) | not found | not detailed | yes — serialises as production, then e-mails the UBL instead of sending |
| Unimaze | https://apidocs.unimaze.com | HTTP Basic (search summary) | **UNCERTAIN** | — | — | **UNCERTAIN** |
| Pagero (Thomson Reuters) | no self-serve portal found | — | — | — | — | — |
| Tietoevry, Logiq, Compello | none found (Logiq confirms 1A §4.2's UNCERTAIN) | — | — | — | — | — |

Pricing is published only by SendRegning (https://www.sendregning.no/priser/) and
e-invoice.be (from €0.25 per invoice); Storecove and Qvalia are by contact.

**Storecove** in detail (https://www.storecove.com/docs ,
https://help.storecove.com/en/articles/4640597-submit-invoices-via-the-api): base
`https://api.storecove.com/api/v2/`, `Authorization: Bearer <key>`; `POST
/document_submissions` with a `legalEntityId` (the sender, set up once per legal entity),
`routing` by `eIdentifiers` (`0192:…`) or an e-mail fallback, and the `document`.
Webhook states: `no_action_taken` (no routable recipient), `failed` (terminal),
`cleared`, `succeeded` (corner 3, the AS4 receipt), and `acknowledged`, `in_process`,
`under_query` (corner 4, only with Invoice Response advertised).
`GET /document_submissions/{guid}/evidence` returns the proof. Storecove does its own
SML/SMP walk; Vantigo's re-check still decides first (§5.5). **UNCERTAIN**: the exact
field and encoding for a raw UBL body; which PDF reaches the receiver when both the
embedded one and a Storecove PDF field are present; the dedupe window; rate limits (a
"100 per 60 s" figure could not be traced to Storecove); how the sandbox routes.

**Storecove first, Qvalia second, SendRegning not first.** Storecove has a real sandbox,
a documented lifecycle, an idempotency key and UBL-in; Qvalia the most faithful sandbox
and UBL-in, with a wider EDI surface. SendRegning is the strongest Norway-specific option,
but every test call is a billable production action and its draft model looks like "the
provider assembles the EHF from fields" — **UNCERTAIN** until its schema is read.

### 5.4 The self-hosted installation and the status channel

A webhook needs an endpoint the provider can reach, which many self-hosted installations
are not. Storecove's **pull queue** (polled by `GET`) needs none, and a status poll by
submission id works with any provider. Webhook signing was not researched —
**UNCERTAIN**. Polling from a worker works everywhere (§8).

### 5.5 Sender identity, and the SMP re-check at send

**No ELMA registration is needed to send.** ELMA (Digdir's SMP,
https://samarbeid.digdir.no/elma/dette-er-elma/108) registers a participant to be **found
as a receiver**. The sender's id appears as the UBL supplier endpoint (`0192:<orgnr>`)
and on the AS4 envelope, neither needing a registration ("Only receiving requires formal
registration in Peppol" — a provider FAQ, secondary, consistent with every provider's
onboarding).

**The re-check uses the existing client.** `peppol.Client.Lookup(ctx, participant)
(Result, error)` (`srv/peppol/lookup.go:164`) already answers per document type:
`Result{Registered, SMPHost, CanReceiveInvoice, CanReceiveCreditNote}` (`lookup.go:86-91`).
Its package comment names Invoices as a future caller (`srv/peppol/doc.go:47-49`). At send:
call it again, gate an invoice on `CanReceiveInvoice` and a credit note on
`CanReceiveCreditNote`, and treat an error as "could not find out", never as "not
registered" (`lookup.go:159-163`). Customers' stored answer
(`customers.customer_peppol_lookups`, up to `CUSTOMERS_PEPPOL_RECHECK_AGE`, 720 h, old —
`docs/src/content/docs/en/reference/customers.md:2345-2353`) may decide whether to
*offer* EHF; it must not gate a transmission.

**Which participant.** Customers looks up "the billing profile's explicit `peppolId` when
set, else `derivedPeppolID`" (`customers.md:2249-2254`); the contract's resolved
`PeppolID` is the same value (`srv/contracts/directory.go:118`), and the issue snapshots
it (`issue.go:101`). The snapshot is what the UBL's buyer `EndpointID` must say; if the
profile's id has changed since issue, the send would address one participant and print
another — a decision (§10).

### 5.6 Test infrastructure

**(Correction.)** The Peppol test SML is no longer the Commission's
`acc.edelivery.tech.ec.europa.eu`; after OpenPeppol's SML insourcing it is
**`participant.sml.test.tech.peppol.org`** (production `participant.sml.prod.tech.peppol.org`;
https://www.arratech.com/blog/openpeppol-sml-insourcing ; `customers.md:2350`), selected by
`PEPPOL_SML_ZONE` with no code change (`srv/config/config.go:185`). OpenPeppol's AP test
bed (https://peppol.org/wp-content/uploads/2024/03/Peppol_TestbedAndOnboarding_v1.4.pdf)
needs no SML registration and concerns styles (a)/(c) only; its example participant
`0192:810418052` is a search paraphrase — **UNCERTAIN**, not a fixture.

A Go test can fake **honestly** what `internal/peppol`'s tests fake — a stub DNS server
and a stub SMP over real TLS (`lookup_test.go:20-30`, `smp_test.go:21-28`, `:131-150`), a
fake resolver only for the guard tests (`lookup_test.go:284-301`): an `httptest.Server`
for the provider asserts the request (auth, idempotency field, UBL body) and returns every
documented status, error and the 422 on a repeat — the adapter's contract. It **cannot**
show that the provider routes, delivers over AS4, returns real evidence or enforces its
limits: that is an opt-in integration test against a sandbox (Storecove or Qvalia),
skipped without a test key, never default in CI. e-invoice.be's sandbox suits a dry-run
mode but proves no transmission.

## 6. KID

### 6.1 The specification

Two Mastercard Payment Services (MPS, formerly Nets) PDFs, text extracted from the files
themselves: *Systemspesifikasjon OCR giro* v4.0, 2018
(https://www.mastercardpaymentservices.com/media/ruqn3ort/ocr-systemspesifikasjon_no_mps.pdf),
and *Brukerhåndbok innbetalingstjenestene*
(https://www.mastercardpaymentservices.com/media/un5kqjll/brukerhaandbok-innbetalingstjenestene_no_mps.pdf,
filename dated Aug 2021; cover date **UNCERTAIN**), "the manual" below.

- **Length.** The OCR file field is 25 positions, right-justified, blank-padded, and
  "Bokstaver kan ikke benyttes i KID" (§2.3.1, Felt 13). The logical rule is the
  manual's §6.5: "minimum 3 + kontrollsiffer og maksimum 25 siffer inkludert
  kontrollsiffer". **4 to 25 digits, check digit included.** Conta's "2–25"
  (https://conta.no/artikler/kid-nummer, secondary) has no primary backing. Whether the
  minimum is bank-enforced — **UNCERTAIN**.
- **MOD10** (§4, p. 17): weights 2, 1 alternating from the right, the *digits* of the
  products summed, check = 10 − last digit of the sum (0 when that is 0). The worked
  example, verbatim: "Felt uten kontrollsiffer 1 2 3 4 5 6 7 8 … Siffersum:
  1+4+3+8+5+1+2+7+1+6=38 Kontrollsiffer 10-8=2" — **12345678 → 123456782**.
- **MOD11**: weights 2–7 repeating from the right, check = 11 − (sum mod 11), 0 when the
  remainder is 0. Example: "Produkter: 3+4+21+24+25+24+21+16=138 … rest på 6 …
  Kontrollsiffer: 11-6=5" — **12345678 → 123456785**. And: "Dersom kontrollsiffer blir 10
  (rest=1) må kontrollsifferet erstattes med - (minus-tegn)."
- **(Correction to 1A §5.1,** which gave "1234567 → MOD10 check 2".) The MOD10 example's
  field is 12345678, the same as MOD11's; 1234567 gives 4 under MOD10. A test fixture
  copied from 1A would be wrong.

### 6.2 The bank agreement

The payee states **KID length and modulus** on the "Registreringsskjema bankkunde", which
MPS distributes to every bank so the payer's bank can check a typed KID (manual §2).
"Det kan opprettes kun en avtale pr konto. Det er mulig å benytte inntil tre ulike
KID-lengder på hver konto." — **one agreement per account, up to three
(length, algorithm) pairs, each of a different length** (also AvtaleGiro brukerhåndbok
§7.1, https://www.avtalegiro.no/media/y24ch0vl/avtalegiro-brukerhaandbok_no_-mps.pdf).
Without an agreement no bank validates the KID, and a printed KID arrives as free text.
The settings therefore model an agreement, not a free choice: per bank account, the
registered length(s) and algorithm, and "no agreement" as the default.

### 6.3 An invalid KID

Manual §6.5: a KID that does not match the registered modulus and length "vil
betalingen bli avvist av banken"; **tvungen KID** is an opt-in level that rejects every
payment missing or carrying an invalid KID; **brevgiro** goes through regardless. Egiro
reporting (§1.2) lists "innbetalinger med gyldig KID" and "innbetalinger med ugyldig KID"
as categories. **Two-source caveat:** how "rejected by the bank" and "reported as
invalid" fit together is this research's synthesis of two passages, not one MPS sentence
— **UNCERTAIN**. For phase 4's import it means an exception queue for missing and wrong
KIDs is a documented bank-side category, not a corner case.

### 6.4 Composition

No law or MPS rule fixes the composition; only digits and length are constrained. The
AvtaleGiro manual constrains it by use (§7.1–7.2): "KID i OCR giro benyttes for å
identifisere innbetalingen"; in AvtaleGiro it "må inneholde kundenummer" and
"Fakturanummer alene kan ikke benyttes"; eFaktura's must contain the eFaktura reference.

**Per-invoice KID, recommended.** Phase 4 matches a payment to one invoice; a KID unique
per invoice resolves it alone. The number is gap-free and never reused
(`invoices.md:33-37`), so `<number zero-padded><check digit>` is unique by construction;
`<customer number><number><check>` (the common vendor convention, secondary) adds nothing
to matching but prepares AvtaleGiro. **Not "fast KID"** (one per customer) for OCR giro:
variable invoices would need amount-and-date heuristics; AvtaleGiro's customer anchor is
that later phase's fork. **MOD10 by default** — MOD11's "-" trips payers (1A §5.1).
Credit notes get **no KID**; they have no payment block (`invoices.md:178`). Partial
payments under one KID are fine (whether a bank flags a further one — **UNCERTAIN**). A
length *n* holds numbers of up to *n − 1* digits; `seriesStart` and volume decide when a
second registered length is needed.

### 6.5 Carriage

- **The PDF's payment block**: "Kontonummer / KID / Beløp / Forfall", the conventional
  giro labelling — 1A §8's planned block. New documents only: stored PDFs never change
  (`invoices.md:329-342`).
- **The UBL**: `cac:PaymentMeans` with `cbc:PaymentMeansCode` **30** (BBAN) or **58**
  (IBAN), `cbc:PaymentID` = the KID ("remittance information about the payment, for
  example KID-number", payment-information.adoc above), and
  `cac:PayeeFinancialAccount/cbc:ID`. 59 is SEPA direct debit and not ours. A secondary
  snippet claiming "54" is wrong.
- **The e-mail**: the payment paragraph names `{account}` and asks the payer to quote the
  invoice number (`invoices.md:484-497`); there is **no `{kid}` placeholder**. With a KID
  the paragraph should ask for the KID instead.

### 6.6 No Billing 3.0 rule checks the KID

**(Negative finding.)** No NO-R rule concerns `PaymentID` — NO-R-001 and NO-R-002 are the
only two (https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/ , checked with the
Norway page). The rule the premise remembered is EHF 2.0's **`NOGOV-T10-R012`**, a
**warning**: "Payment Identifier (KID number) SHOULD be used according to EHF"
(https://anskaffelser.dev/postaward/g2/spec/current/rules/t10-ehf/) — a retired profile,
and it never checked the checksum. **No Schematron catches a malformed KID.** The check
is the module's own, at issue, as the IBAN's mod-97 is today (`settings.go:91`); there is
no MOD10 in the module yet — only `validGLN` in customers.

### 6.7 What the law requires about payment

Bokføringsforskriften § 5-1-1 (https://lovdata.no/forskrift/2004-12-01-1558/§5-1-1) item
5: **"vederlag og betalingsforfall"** — consideration and due date. **No account number,
IBAN or KID** in § 5-1-1 or elsewhere in kap. 5 found. The account is commercial practice
(and the module's own issue gate, `seller.go:5-9`); a KID is a convenience under a bank
agreement. KID is therefore optional per tenant, never a condition of issue.

**"Faktura uten KID" fees — UNCERTAIN.** Consumer sites say some banks charge the payer
more without a KID; no bank's price list was found saying so. DNB's
(https://www.dnb.no/en/business/daily-banking/payments/payments-price-list, secondary)
charges the *payee* for an OCR agreement — not the claim. Keep it out of the user guide.

## 7. Where the codebase stands

### 7.1 The Peppol client, its configuration, and what Customers exposes

`srv/peppol` is "pure network plumbing: no database, no HTTP contract, no module"
(`doc.go:1-50`), a platform package Invoices may import. `Options{Zone, DNSServers,
Timeout, HTTPTransport}` (`lookup.go:57`); `NewClient` never fails, a nil transport
becomes `guardedTransport` through netguard, redirects are never followed (`:96`);
`Lookup` makes at most one NAPTR query and one ServiceGroup GET (`:164`). `Result` is
two booleans and a host (`:86-91`): **no endpoint URL, certificate or document-type
list** — a capability gate, not a sender (nor needed under §5.2). The document types are
unexported (`smp.go:36-42`); no Accept header, because ELMA answers 406 to
`application/xml` (`smp.go:150-156`).

Config: `PeppolLookupEnabled` (`srv/config/config.go:178`), `PeppolSMLZone` (`:185`),
`PeppolDNSServer` (`:189`), `PeppolTimeout` (`:192`), loaded by `peppolLookup()`
(`:550`); the re-check worker's `CustomersPeppolRecheck*` (`:211-221`); "Add new settings
here, never by reading os.Getenv at a call site" (`:7`). The admin table is "Transport,
storage and modules" (`docs/src/content/docs/en/admin/authentication.md:357`, PEPPOL rows
`:369-372`), and `PEPPOL_LOOKUP_ENABLED` is documented as the **Customers** lookup's
switch (`customers.md:2349`).

Customers builds its own client (`srv/customers/server.go:42-60`; `Deps.PeppolLookup` is
a test seam, `srv/module/module.go:161`); `lookupAndStorePeppol`
(`srv/customers/peppol_lookup.go:202`) calls the network outside any transaction and
logs failures by kind, never `err.Error()`, which may carry an org number. The answer is
one row per customer in `customers.customer_peppol_lookups` (migration `00020`), shown
only on Customers' own endpoint. The contract's `CustomerBillingProfile`
(`srv/contracts/directory.go:74`) carries `InvoiceDelivery` (`:113`) and the resolved
`PeppolID` (`:118`) — **no lookup answer**.

### 7.2 Secrets and the credential pattern

`secrets.New` (`srv/secrets/secrets.go:99`), `Seal(purpose, plaintext)` (`:141`), `Open`
(`:168`): AES-256-GCM, a per-purpose HKDF key, the purpose as AAD; on `Deps.Secrets` in
every mode, worker mode included. No rotation: a new `APP_SECRET` invalidates every
sealed value (`docs/src/content/docs/en/admin/sso-scim.md:349`). The precedent is
Communications' SMTP channel (purpose `communications/channel-smtp-credential`,
`srv/communications/channels.go:48`; table `communications.channel_credentials`,
`00006:50-60`): non-secret settings as JSON beside the sealed secret; the response gives
`hasCredentials`, never the secret (`channels.go:128-150`); an omitted password on update
keeps the sealed one (`:580-628`).

### 7.3 Workers, outbound HTTP

`worker.Worker{Name, Interval, Run}` (`srv/worker/worker.go:26`). `module.Workers`
(`srv/module/workers.go:20`) adds only `CustomerPersonalData` — **it does not set
`Deps.Directory`**, which only `Compose` does (`srv/module/compose.go:182`). Invoices has
no workers. Two patterns: Customers' **advisory lease** (`pg_try_advisory_lock`, batches,
no backoff, `srv/customers/peppol_recheck_worker.go:240-265`) for cursor work with "no
per-row claim to fall back on"; and Communications' **outbox**
(`srv/communications/outbox.go`): per-row claim by conditional UPDATE, poll 5 s, lease
60 s, at most 8 attempts (`:47-70`), backoff `min(3600, 2^min(attempts,10))` s, a crash
marker stamped before the external call (`:374`), a redacted `last_error`, multi-replica
safe.

Brreg's client (`srv/customers/brreg.go:226`) is the outbound-HTTP model: base URL and
timeout from config, three attempts with full-jitter backoff on ≥ 500 and 408, no
redirects, `Deps.HTTPTransport`/`HTTPBackoff` as test seams. **netguard** guards only
destinations that come from third-party data (the SMP, mail); operator-configured hosts —
Brreg, the AI client with its redacted key (`srv/config/redact.go:45`) — are not. A
provider base URL is operator-configured.

### 7.4 Invoices

- **Tables.** Settings `00034:19-43` (no Peppol id, no KID); invoices `:99-198` with the
  snapshots, `your_reference`/`order_reference` (`:120-122`), `buyer_peppol_id` (`:140`),
  `pdf_object_key`/`pdf_sha256`; lines `:204-233` (`unit` free text, `:210`); VAT
  summaries `:237-248`; `vat_codes.ehf_category` ∈ S, Z, E, AE, G, O, K (`:71`).
- **Triggers.** `refuse_issued_document_change` (`00034:256-281`) compares `to_jsonb` of
  the row minus `customer_id`, `pdf_object_key`, `pdf_sha256`: **any new column on an
  issued row is frozen automatically**, so a set-once UBL key or a status there means
  changing the trigger — or a separate table. `refuse_issued_child_change` (`:298-329`)
  refuses child writes under an issued document.
- **Issue.** `PostInvoicesByIdIssue` (`srv/invoices/issue.go:215`): directory read before
  the transaction, `withLockedTx` (`:244-417`) — lock, settings, `AllocateNumber`, checks,
  line snapshots, VAT summaries, seller snapshot, `IssueDocument` (`:412`) — then
  `storeAfterIssue` (`:429`). `buyerSnapshot` (`:92`) copies `PeppolID` and `GLN` (`:101`).
  A KID derived from the number belongs inside that transaction, after `AllocateNumber`.
- **The PDF.** `storeOnce` (`pdfstore.go:221`): render from the rows only (`renderIssued`,
  `:157`), hash, key `documents/<id>/<number>-<sha>.pdf`, set once; `loadStoredPDF`
  (`:343`) stores if never stored, else reads, caps 20 MiB, verifies; the module never
  deletes an object (`:27`).
- **Validation helpers.** `mod11` (`settings.go:66`), `validOrganisationNumber` (`:53`),
  `validBankAccount` (`:60`), `validIBAN` (`:91`), `bicPattern` (`:37`). No MOD10.
- **The send.** `PostInvoicesByIdSend` (`send.go:183`): synchronous, the send on an
  uncancellable 30 s, the row on 5 s (`invoices.md:395-402`); rate limit 60/10 min
  (`module.go:70`); `withSendDefaults` (`send.go:124`); contract
  `openapi/invoices.yaml:2168-2249`, `InvoicesSendRequest` (`:971`), `InvoicesMetaResponse`
  (`:28`, `mailAvailable`, `storageAvailable`).
- **Deliveries.** `invoices.deliveries` (`00035:38-48`); `refuse_delivery_change`
  (`:127-144`) allows only the recipient's blanking; `guard_delivery_insert` (`:156-177`)
  reads the document `FOR SHARE` and the erased marker.
- **Slots.** `RepointCustomer` (`customer_slots.go:52`); child rows follow by
  `invoice_id`. `exportCustomerData` (`:268`) and `EraseCustomerData` (`:399`) name each
  child table: a transmission table follows a merge for free; export and erase must name it.
- **The object store.** `ObjectStore{Put, Get, Exists, Delete}`
  (`srv/storage/storage.go:61-73`); content type opaque, so `application/xml` is fine; key
  rules (`srv/storage/fs.go:401`: ≤ 1024 bytes, no `\ % : ? #`, no `.`/`..` segments)
  admit `documents/<id>/<number>-<sha>.xml`; `fs` only, no WORM
  (`docs/src/content/docs/en/admin/object-storage.md:78`).
- **XML.** `encoding/xml` only in `peppol/smp.go`; no XSD, Schematron, XSLT or XML
  dependency in `apps/server/go.mod`. Writing UBL with `encoding/xml` is easy; validating
  it is the hard part (§3).

### 7.5 The frontend and the documentation pages

Invoices: `SendDialog` (`apps/invoices/frontend/src/pages/-send-dialog.tsx:45`),
`DeliveriesCard` (`components/deliveries-card.tsx:12`), `IssuedDocument`
(`pages/invoice.tsx:967`, actions `:994-1009`), the seller form (`pages/settings.tsx:165`,
completeness list `:268-299`), i18n `sendWarning.delivery_preference_ehf` (`i18n.ts:321`,
nb `:718`). Customers: `CustomerPeppolStatus`, `CustomerEhfOffer`
(`apps/customers/frontend/src/pages/-customer-peppol-status.tsx:80`, `:234`). Host: the
`invoices:*` catalog (`apps/host/frontend/src/catalogs/admin.ts:348-377`).

Docs: `en/reference/invoices.md` — The law (`:27`), The model (`:73`), Issuing (`:127`),
The PDF (`:316`), Sending a document (`:352`, "synchronous: no outbox and no worker"),
Retention (`:673`), Permissions (`:736`), Endpoints (`:763`), What comes next (`:797`).
`en|nb/user/invoices.md` — Before the first invoice (`:20`), Sending by e-mail (`:237`).
`admin/` has no e-invoicing page; `installation.md:261` covers background workers.

## 8. Patterns to take

1. **The outbox shape for a transmission.** A row per send, claimed by conditional
   UPDATE, a crash marker before the provider call, bounded attempts with backoff, a
   redacted `last_error`, terminal states — Communications' outbox (§7.3), not the lease.
   The request writes the row and answers "queued"; a worker submits, then polls. A 5xx
   or timeout is "could not find out" and retried; the provider's `failed` is terminal —
   the peppol client's distinction (`lookup.go:159-163`). **The idempotency key is per
   logical send**, reused on every retry, so a lost response is recovered by polling,
   never by a second submission.
2. **Credentials never returned.** The API key sealed under its own purpose, non-secret
   settings (provider, base URL, legal-entity id) beside it, `hasCredentials` on the wire,
   an omitted key kept on update — the SMTP channel (§7.2), under `invoices:manage`.
3. **The SMP re-check through the existing client**, per document type, just before the
   submit, not on Customers' stored answer (§5.5); failures logged by kind (§7.1).
4. **A deterministic UBL from the snapshot, like the PDF** — the rows and snapshots only,
   never the settings, directory or VAT tables (`invoices.md:329-334`) — so a document's
   PDF and UBL cannot disagree.
5. **Store once.** Sent as EHF, the XML is the salgsdokument (§ 5-2-9,
   https://lovdata.no/forskrift/2004-12-01-1558/%C2%A75-2-9): render, hash, put
   `documents/<id>/<number>-<sha>.xml`, record key and hash once, resend the stored bytes
   — `storeOnce`/`loadStoredPDF` (§7.4) as the template.
6. **The deliveries table's shape for the transmission log.** Immutable but for named
   transitions, a parent trigger reading the document `FOR SHARE` and the erased marker,
   `ON DELETE RESTRICT`; merge follows `invoice_id`, export lists it, erase blanks what
   is personal (a sole proprietor's participant id is an org number, which Customers
   clears on anonymisation, `customers.md:1638-1644`).
7. **Best effort on reads, refusals on writes**, as `sendDefaults` (`invoices.md:520-533`):
   a capability shown on a document never fails a read; the send refuses on a definitive
   "cannot receive".

## 9. Recommended phase 2 scope and sequencing

**2A — the EHF document, KID and one access point** (the B2B date is three months away):

1. **KID.** Settings: a KID agreement per the seller's account (none by default; length,
   MOD10/MOD11), validated at `PUT /settings`; a MOD10 next to `mod11`. At issue, inside
   the transaction after `AllocateNumber`, a KID for an invoice when an agreement exists,
   stored in the snapshot. Printed in the PDF's payment block; a `{kid}` form of the
   e-mail's payment paragraph.
2. **The UBL serializer.** Invoice and CreditNote per §2, from the snapshot only, the PDF
   embedded; the seller's endpoint, Foretaksregisteret, AllowanceCharge, VATEX, unit
   codes, BillingReference. Golden files validated by Saxon against the pinned artefacts
   in CI (§3); the Go pre-check at send.
3. **Settings for e-invoicing.** The seller Peppol id (or its derivation), the provider,
   its non-secret settings and the sealed key; `GET /meta` answering whether EHF is
   available, as it answers `mailAvailable`.
4. **The `AccessPoint` port, one adapter (Storecove), a dry-run adapter**, and the
   transmission log with an outbox-style worker (submit, poll), status shown on the
   document. The send chooses EHF when the buyer's snapshot has an endpoint and the
   live re-check says the document type is accepted; the 1B e-mail path stays for every
   other buyer.
5. **Store-once of the UBL** at the first EHF transmission; resends reuse it.

**Defer:** Invoice Response and Profile 02 (needs a receiving registration); receiving
EHF (Expenses, needs ELMA); MLS beyond what the provider reports; a second adapter;
eFaktura and AvtaleGiro (`ROADMAP.md` "Later"; AvtaleGiro needs a customer-anchored KID,
§6.4); foreign currency's second `TaxTotal`; a runtime Saxon, unless the design wants it.
**2B**, if the split is wanted: a second provider, webhooks for installations that are
reachable, Invoice Response.

**The documentation pages phase 2 changes** (AGENTS.md: the plan lists them, both
languages where bilingual):

- `en/reference/invoices.md` — the opening callout (`:21-25`), The law, The model, Issuing
  (the KID), The PDF (the payment block), Sending (the EHF path, the log, what "delivered"
  means), Retention (the stored UBL), Permissions, Endpoints, What comes next.
- `en/user/invoices.md` and `nb/user/invoices.md` — Before the first invoice (the KID
  agreement, e-invoicing settings) and Sending (EHF or e-mail, the statuses).
- A new admin page **"E-invoicing (EHF over Peppol)"** in `en/admin/` and `nb/admin/` —
  choosing a provider, the API key, the test network, verifying a send, what goes wrong —
  listed in both `admin/index.md`.
- `en|nb/admin/authentication.md`, "Transport, storage and modules" (`:357`) — rows for
  any new variable, and the PEPPOL rows if the switch's meaning widens;
  `en|nb/admin/installation.md`, "Background workers and scaling" (`:261`) — the worker.

## 10. Decisions the design must take

1. **The identifiers.** Confirm the CreditNote document type string against a live
   ServiceGroup (`smp.go:40` already relies on it) and the process id literal, if the
   adapter ever builds the envelope — both **UNCERTAIN**.
2. **Which provider first, and whether to support two.** Storecove is recommended;
   Qvalia second; SendRegning has no sandbox. One adapter in 2A, or two to prove the
   port? Who holds the provider account — the installation, or a Vantigo-operated account
   routed by legal entity (§5.1's **UNCERTAIN** on shared certification)?
3. **Synchronous or outbox send.** 1B's e-mail send is synchronous by design
   (`invoices.md:354-356`); the provider's lifecycle is asynchronous (minutes to days).
   Recommended: the request writes a row and answers "queued", a worker submits and
   polls. Then: does the e-mail path move to the worker too, or stay synchronous?
4. **Webhook or poll.** A self-hosted installation may be unreachable; polling works
   everywhere. Is a webhook route built at all, and how is it authenticated (signing
   not researched — **UNCERTAIN**)?
5. **Where the UBL is stored, and when.** At issue (every invoice has one, B2C too) or at
   the first EHF send; on `invoices.invoices` (the trigger's exclusion list grows, as for
   the PDF) or in the transmission table. Is the stored PDF embedded always, or by a
   setting?
6. **KID layout and the agreement fields.** Per-invoice `<number><check>` vs
   `<customer number><number><check>`; MOD10 default, MOD11 allowed with its "-"; one
   length or up to three; the seller's account it belongs to (one agreement per account,
   §6.2); what happens when the number outgrows the length; documents issued before the
   agreement get none.
7. **The buyer whose SMP answer says no.** For an invoice: refuse EHF and offer e-mail
   with the warning, or fall back automatically? For a credit note to a receiver that
   takes invoices but not credit notes (`CanReceiveCreditNote=false`): e-mail it, with
   what warning? A lookup *error*: refuse and retry, never fall back silently.
8. **How the 1B e-mail path and the EHF path coexist** under `invoiceDelivery`: the
   roadmap's precedence is "EHF when the receiver accepts the document type, otherwise
   the billing profile's preference" (`ROADMAP.md:793-795`). Is EHF automatic for a
   capable receiver whose profile says `email`? Does the 1B warning set change once EHF
   is available? Can a user send both (EHF plus a courtesy e-mail)?
9. **The seller's Peppol id source.** Derived `0192:<organisation_number>` always, or a
   stored, editable id (a tenant registered under another scheme or a sub-unit)?
10. **The buyer endpoint: snapshot or current profile.** The UBL prints the issue-time
    snapshot; the profile's id may have changed since. Send to the snapshot's, refuse on
    a mismatch, or re-issue?
11. **R003 and unit codes at issue.** Require a buyer or order reference, and a mapped
    unit code, at issue for a buyer with an endpoint — or only at EHF send, when the
    document is already immutable and cannot be fixed but by credit and re-issue?
12. **Retention of the UBL and the log.** Five years after year-end under § 13
    (https://lovdata.no/lov/2004-11-19-73/%C2%A713), as the PDF; the 2027 wording of § 13
    is still unread — **UNCERTAIN**. Which provider evidence is kept locally, and is the
    provider's own retention relied on for anything?
13. **Runtime validation.** Saxon in CI only, or also at send (a sidecar or `gosaxon`,
    **UNCERTAIN** maturity) — and how a pinned artefact is upgraded every spring and
    autumn.
14. **The `PEPPOL_LOOKUP_ENABLED` switch.** It is Customers'; does it also disable the
    EHF re-check (and so EHF), or does Invoices get its own switch?
15. **Worker mode.** `module.Workers` leaves `Deps.Directory` nil (`workers.go:20`); a
    transmission worker must work from the snapshot and the row alone, or the platform
    must resolve the directory in worker mode.
16. **Documents issued before phase 2.** They have no KID and possibly no references or
    unit codes; can they be sent as EHF (their UBL is reproducible from the snapshot), or
    only documents issued after?
17. **Permissions and status words.** An EHF send under `invoices:issue`, the AP
    credentials under `invoices:manage`? The states shown — queued, delivered to the
    access point, failed — with nothing saying "received" before an Invoice Response.

Carried-forward **UNCERTAIN** items: the in-force resolution text; PDF-by-mail's
lawfulness for B2C and foreign buyers after 2027 (not found either way); the ELMA
condition on the duty; the format regulation's date; Profile 02's URN; the country-rule
policy's conditions; NO-R-001's and NO-R-002's severity labels; BR-E/AE/G conditionality;
BR-DEC and BR-23 rule by rule; negative credit-note lines; the hosted validators' REST
contracts; `go-xml`'s and `gosaxon`'s maturity; MimeCode list; MLS's receiving-side date
and provider support; DFØ's AP requirements PDF; shared AP certification; Storecove's raw
UBL field, PDF precedence, dedupe window, rate limits and sandbox routing; SendRegning's
draft schema and status mechanism; Unimaze's API; the test participant `0192:810418052`;
webhook signing; the KID minimum's enforcement; the invalid-KID reconciliation; the
"faktura uten KID" fee; the Brukerhåndbok's cover date; and § 13's 2027 wording.
