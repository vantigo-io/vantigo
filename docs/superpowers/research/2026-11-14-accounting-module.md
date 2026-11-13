# Accounting module — research

The owner asked on 2026-11-13 for **the next app: Accounting** — *an app that can have the
entire accounting and books for a company. Think like Fiken and Visma. It should serve the
same purpose, but still be integrated with the rest of the application portfolio*, with
*integration with Altinn and other public sector systems that can automate much of the
mandatory boring stuff*. This note sets what the law and the public APIs require against
what Vantigo already has, and proposes phases. A design spec starts from §8 and §9 once the
owner has answered §10, which he did on 2026-11-14.

The module is **Accounting** (`accounting`; Norwegian **Regnskap**; package
`@vantigo/accounting-ui`; permissions `accounting:*`). Code references are against `main`
at `0ebc37d21`, whose latest migration is `00177`. `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`. External facts were fetched on
2026-11-13 and 2026-11-14; *(u)* marks one this note could not verify at a primary source.

## 1. What is decided, and what this note adds

### 1.1 On the record

- **Invoices foundation design** (`specs/2026-09-26-invoices-foundation-design.md:25`):
  *Vantigo stays a sub-ledger: no general ledger, no posting.* The reference repeats it:
  *a payment here is a registration, not a posting*. Accounting reverses that decision; it
  does not change what Invoices is.
- **ROADMAP.md** lists as not done *several legal entities per tenant, each with its own
  series* and *a posting export in SAF-T-friendly form to Tripletex, Fiken or PowerOffice*.
  A ledger of our own replaces the second; the first becomes a question for it (§9.4).
- **Supplier invoices design** (`specs/2026-09-26-supplier-invoices-design.md`, out of
  scope): accounts payable, a supplier register, inbound e-invoices, VAT codes per line.
  All four are Accounting's to pick up.
- **2026-11-13, the owner:** the purpose is Fiken's and Visma's; integrated with the other
  apps; public-sector filing automated where it can be.

### 1.2 What this note settles

- what the law requires of an accounting system, and what changed in 2026 (§2);
- which filings can be automated, with which credentials, hosted and self-hosted (§3);
- bank, year-end and payroll: what is in reach and what is another product (§4);
- what the competitors sell, as the table stakes (§5);
- the domain model (§6) and the seams in the code (§7);
- the phases (§8), the readings with the defaults proposed (§9) and the owner's answers (§10).

## 2. What the law requires of the system

### 2.1 Three things changed in 2026

1. **E-invoicing and digital bookkeeping are enacted** (lov 19 June 2026 nr. 39).
   - From **1 January 2027**, businesses with a bookkeeping duty invoice each other by
     e-invoice: EHF Fakturering or Peppol BIS Billing 3.0 or later. A PDF does not qualify.
   - From **1 January 2030**, they must receive e-invoices and keep their books *in an
     electronic accounting system*. The same paragraph lets the ministry set requirements
     for such systems by regulation; none are set yet.
   - From 1 January 2028 the buyer's organisation number is mandatory on the document when
     the buyer has a bookkeeping duty *(u: from the amending regulation, not re-read)*.
2. **SAF-T Financial 1.40** is published and mandatory for periods starting 1 January 2027.
   Version 1.30, mandatory since 2025, already removed the mapping to a standard chart of
   accounts: **every account maps to a næringsspesifikasjon grouping code**, and a
   standard tax code on every tax code is mandatory.
3. **The NOK 5 million exemption** from electronic availability and SAF-T is repealed from
   1 January 2027 (Skatteetaten's statement of 30 September 2026). The new floor is a
   turnover under NOK 50,000. In practice every customer of ours must be able to produce
   SAF-T.

So the timing is good: from 2030 every Norwegian company needs what this module is, and
Invoices already sends EHF.

### 2.2 Bokføringsloven and the regulation

There is **no approval scheme** for accounting systems (unlike cash registers), and the
duties rest on the company keeping the books. Prop. 44 L (2025–2026) considered regulating
vendors and declined for now. What the system must make possible:

- **The specifications** (law § 5, regulation § 3-1), per period, each dated when produced:
  - *bokføringsspesifikasjon*: every entry in order, with document date and reference;
  - *kontospesifikasjon*: per account, entries with opening and closing balance;
  - *kunde-* and *leverandørspesifikasjon*: per party, entries and balance;
  - *merverdiavgiftsspesifikasjon*: output and input VAT per rate, exempt and
    outside-scope sales, reverse charge, import basis;
  - withdrawals by and sales to owners, sales to senior employees, payroll-reportable
    benefits.
- **Audit trail** (§ 6): from document through specification to filing and back.
  System-generated entries need their rule documented.
- **No change after the deadline** (§ 9): a posted entry is corrected by a new entry that
  reverses it in full. Documentation is never changed after issue (§ 10).
- **Period locking** (regulation § 7-6): periods are closed against change within the
  deadlines; the description of the lock is kept five years.
- **Ajourhold** (§ 7): at least every four months, and within each filing deadline.
- **Retention** (§ 13): five years for books, specifications and vouchers; 3.5 years for
  secondary documentation; ten years for capital goods under the VAT adjustment rules.
  E-invoices are kept **in their original format** from 2027.
- **Storage** (§ 13, regulation § 7-5): Norway, or another EEA state, the UK or
  Switzerland when the tax office is notified. **Backup** (§ 7-2): kept apart, and tested
  once a year. Both are operator procedures for the administration guide.
- **Currency** (§ 8): NOK, with VAT fixed in NOK at the invoice date. Invoices already does
  that (`vat_amount_nok`).

Giving an **external accountant** access is the normal case. Regnskapsførerloven § 5-6
requires the firm to keep a register of the named people using each power of attorney and
to revoke access when it ends: per-person, revocable access, which identity already gives.

### 2.3 VAT

- Rates for 2026: 25 %, 15 % (food; water), 12 % (transport, accommodation, cinema).
- Terms: two months by default, due one month and ten days after (the third term on
  31 August); yearly on approval under NOK 1 million.
- A ledger needs the **whole standard tax code list**, not the sales half: input VAT
  (1, 11, 13), import of goods (81–85, 14, 15), services from abroad (86–89), reverse
  charge (51, 91, 92). Invoices seeds only the outgoing codes (§7.1).
- Bad debts correct the basis (mval. § 4-7); capital goods are adjusted over five or ten
  years (§§ 9-1 to 9-5). The first is phase 1 with write-offs; the second is later.

### 2.4 The chart of accounts

No law requires NS 4102; the regulation asks only for an account code and a name.
NS 4102:2023 is sold by Standard Norge, and its terms for embedding in software are not
published *(u)*. What is public is Skatteetaten's per-year code list for the
næringsspesifikasjon, which uses four-digit codes in the same number space. **We ship our
own default chart, each account carrying its grouping code** (§9.2), and ask Standard
Norge before using the standard's own names, if we ever want them (§10.5).

## 3. The filings, and who holds the keys

### 3.1 The platform

**Altinn II closed on 19 June 2026.** Everything is Altinn 3, Maskinporten and
Skatteetaten's own APIs, with *tilgangspakker* replacing the old roles by year end.

- **Maskinporten** is OAuth2 with a signed JWT grant; free; needs a Norwegian organisation
  number, Digdir's terms and scopes granted by each API owner.
- A **system user** is a non-personal user a customer creates for one registered system.
  The vendor registers the system (API only), sends the customer an approval link, and
  then asks Maskinporten for a token naming that customer.
- **An accountant's client system user** covers the firm's clients through Enhetsregisteret
  roles or delegated access packages.

### 3.2 Hosted and self-hosted

Vantigo is one deployment per tenant, run by us or by the customer
(`docs/src/content/docs/en/admin/installation.md:12-13`, `README.md:36-37`). Altinn's
guidance covers both, and is explicit:

- **Hosted:** the vendor is the Maskinporten client and the registered system; it must
  itself control and log which person triggers each call.
- **Self-hosted** (Altinn's scenario 9): the customer creates its own Maskinporten client,
  registers the system itself and keeps the key on its server. *I slike scenarioer kan
  ikke systemleverandør dele eget sertifikat/nøkkelpar med systemkunde.*

This fits the two configuration tiers already built: the Maskinporten client and its key
are **environment settings**, ours on the hosted service and the operator's on their own
server. The cost falls on the self-hoster, who signs Digdir's terms and asks Skatteetaten
for scopes under their own organisation number. Whether Skatteetaten grants scopes to many
single companies is not documented *(u)*. A relay through our hosted service is not
described anywhere and would make us the shared service for self-hosters too; §10.2.

### 3.3 Filing by filing

| Filing | Channel today | Sign-in | Size | Phase |
| --- | --- | --- | --- | --- |
| SAF-T Financial | file on request in an audit; upload form or API | none for the file | medium | 1 |
| MVA-melding | validation at Skatteetaten, then an Altinn 3 instance | **personal ID-porten** | medium | 2 |
| VAT deadlines (Frister, beta) | Skatteetaten API | system user | small | 2 |
| Claims and payments (Krav og betalinger) | Skatteetaten API | system user | small | 2 |
| Aksjonærregisteroppgaven | Skatteetaten API; **system only since June 2026** | system user | small | 3 |
| Årsregnskap | Altinn 3, Brønnøysund's API | system user fills, a person signs with ID-porten | medium | 3 |
| Skattemelding with næringsspesifikasjon | Skatteetaten and Altinn 3; **system only since income year 2023** | system user; a person confirms | very large | 3 or never |
| A-melding | Skatteetaten API | system user | a payroll product | separate app |

- **MVA-melding is the one that matters first, and it still needs a person.** Skatteetaten,
  31 March 2026: *Det er ikke bestemt på hvilken måte MVA skal ta i bruk systembruker.* The
  flow is: sign in with ID-porten, validate the XML at Skatteetaten, create the instance,
  upload, complete, submit, then read the receipt and the payment information with its KID.
  The return's `mvaKode` **is** the SAF-T standard tax code, so the ledger's tax codes carry
  straight through.
- **ID-porten needs one redirect address per domain**, so each hosted tenant
  (`<tenant>.vantigo.cloud`) is its own integration; the legacy guide lets a vendor create
  them dynamically or lets customers own them *(u: that guide now redirects)*. Digdir's
  onboarding is described as slow.
- **Frister and Krav og betalinger** are open only to a vendor that already files through
  one reporting API and to customers who have filed once through the system. They follow
  MVA-melding; they cannot come first.
- **The test environment** uses synthetic companies from Tenor, and vendors are responsible
  for their own testing.

### 3.4 What we must do as a company

Independent of any code, and with lead times we do not control: accept Digdir's terms for
Maskinporten and for end-user system vendors; ask Digdir for an ID-porten integration and
the system-register scopes; accept Skatteetaten's terms and ask for the VAT validation and
submission scopes; get Tenor test users. §10.1.

## 4. Bank, year-end and payroll

### 4.1 Bank

There is no free, universal bank feed.

- **Direct**: each bank sells an integration agreement (DNB: camt.053 to a system for
  NOK 60 a month per organisation number) over SFTP or its own API, and onboards each
  accounting system itself. ISO 20022 throughout: pain.001 out, camt.053 statements,
  camt.054 incoming with KID.
- **Middleware**: Aritma (formerly ZData) sells one API over the Norwegian banks and is
  what Fiken uses; price on request. PSD2 aggregators (Neonomics, Enable Banking) give
  balances and transactions with a consent renewed about every 180 days, no bulk payments
  and no KID detail.
- **Files**: every net bank exports camt.053 or CSV by hand.

**Folio** has a public API, and a bank feed with Folio as the first bank is designed for
Invoices (`specs/2026-11-13-bank-feed-folio-design.md`, merged 2026-11-13, not built): a
port `internal/bankfeed` any bank can sit behind. It keeps incoming payments only; the
ledger needs the same port for every movement.

Invoices already reads OCR giro and camt.054 and matches on KID
(`invoices.bank_transactions`, mig/00041). What is missing is the **statement**: camt.053,
and reconciling every movement, not only customer payments.

### 4.2 Year-end

- **Aksjonærregisteroppgaven** is small (a main form and one sub-form per shareholder),
  system-only since June 2026, and works with a system user. Every AS needs it.
- **Årsregnskap** for a small company is structured form data with notes; a person signs.
  Brønnøysund's API documentation is handed out on request *(u)*.
- **Skattemelding with næringsspesifikasjon** is a product of its own: a new major schema
  every income year, two schemas of about 220 KB each, and tax rules (temporary
  differences, declining-balance depreciation) on top of the books. Tripletex and Fiken
  sell it as a yearly add-on; PowerOffice launches its own in January 2027 and Conta's is
  still *coming soon*. Vendors ship it years after the ledger.
- The grouping code SAF-T already requires per account gives the profit-and-loss and
  balance half of the næringsspesifikasjon for free.

### 4.3 Payroll

A-melding is monthly and needs employments, pay types, tax cards, holiday pay and employer
tax zones. Every competitor sells it apart. Staff has the people and an export port
(`PayrollExporter`); Accounting takes the payroll journal as an imported voucher.

## 5. What the products sell

Prices ex VAT, from the vendors' price pages in November 2026.

| | Base | Bank feed and payments | Year-end (AS) | Payroll |
| --- | --- | --- | --- | --- |
| Fiken | 219 kr/month | 69 kr/month | 1 590 kr/year, with årsregnskap and aksjonærregisteroppgave | 69 kr per user per month |
| Tripletex | 199–479 kr/month | 49 kr/month | 990 kr/year | 65 kr per user per month |
| PowerOffice Go | 425 kr/month plus per voucher | not priced | from January 2027, 695–1 395 kr/year | 195 kr/month plus 35 kr per payee |
| Conta | 209–339 kr/month | not priced | coming soon | coming soon |

**Table stakes:** EHF in and out, the VAT return filed from the system, a bank feed with
payments as a cheap add-on, year-end as a yearly add-on, payroll per user. None of them has
projects, orders, stock and production feeding the books the way Vantigo can.

## 6. The domain model

- **Legal entity**: whose books these are; one in phase 1 (§9.4).
- **Fiscal year** and **periods** (months), each *open*, *closed* or *locked*; **VAT terms**
  over them.
- **Account**: number, name, kind, default tax code, the **grouping code** with its history
  per year, active.
- **Tax code**: the full standard list, with dated rates; sales codes read from Invoices.
- **Voucher** (*bilag*): series, gap-free number, voucher date, document date, source
  (module, kind, id), attachments, the rule that made it. Never edited; reversed by a
  voucher that names it.
- **Posting**: voucher, account, debit or credit in NOK, optional currency amount and
  rate, tax code with base and VAT, party (customer or supplier), and **dimensions**:
  project and department.
- **Party ledgers**: open items per customer and supplier, matched by payments and credit
  notes.
- **Posting rule**: which account a source line lands on, by line kind, product tax
  category, expense category or VAT code.
- **Supplier** and **supplier invoice**, with lines, tax codes, approval, due date and
  payment.
- **Bank account**, **statement**, **statement line**, **reconciliation**.
- **VAT return**: a term, its figures per code, the XML, the validation result, the
  receipt, the KID.
- **Export**: a SAF-T file, with the mapping used.

## 7. Seams in the code

### 7.1 Invoices

- **VAT is already a code with a SAF-T tag.** `invoices.vat_codes.saf_t_code`, dated rates,
  and a snapshot on every issued line (mig/00034:60-92, 218-221). `invoices.vat_summaries`
  holds base and VAT **in NOK** per (category, rate) with its SAF-T code (00034:237-248).
  Posting an invoice needs only the revenue account per line.
- **Issued documents are immutable by trigger** (00034:256-329), numbered from one gap-free
  counter, with a journal that checks for gaps (`srv/invoices/journal.go:150-190`). That is
  the law's § 9 and § 10 already.
- **Only outgoing codes exist**: 3, 31, 32, 33, 5, 51, 52, 6, 7 (00034:339-362).
- **There is no `invoice.issued` event.** Only `invoices.invoice.overdue` and
  `invoices.payment.received` (`contracts/events_invoices.go`). A removed payment
  publishes nothing (*A removal is not an event*, reference `invoices.md:5683`).
- **Missing for a ledger**: write-offs and losses, overpayments and customer credit
  balances, rounding differences, refunds, Vipps fees and settlement, foreign currency
  (only NOK is issued).
- **The seller** is one row, `invoices.settings`: name, organisation number,
  `vat_registered`, one bank account (00034:19-43). No fiscal year, no VAT term.

### 7.2 Expenses

- Supplier invoices are an expense kind (mig/00033): free-text supplier, gross and **one
  VAT amount**, no code, no account, must be on a project. Categories have a name only.
- Reimbursements carry a date and reference; `expenses.settings.locked_before` is a period
  lock of its own (mig/00012:40-46). Expenses publishes **no events**.

### 7.3 Orders, Inventory, Manufacturing, Energy, Time, Staff

- **Orders** lines carry Invoices' VAT codes; nothing in Orders is a posting until it is
  invoiced.
- **Inventory has quantities and no value**: no cost on a movement, valuation listed as
  left out (reference `inventory.md:2579`). Phase 1 books purchases as cost and adjusts
  stock at period end by hand (§9.6).
- **Manufacturing** holds standard and actual cost per order (mig/00159:32-41) but
  `manufacturing.order.done` carries none.
- **Energy** settlement lines reach the books through the invoice they become.
- **Time** has cost rates per entry; **Staff** has `cost_centre`, departments and
  `staff.legal_entities` with organisation numbers (mig/00042:27-55), unlinked to the
  seller in Invoices.

### 7.4 The platform

- **The outbox is transactional and exactly-once for a handler's own schema**
  (`srv/events`, mig/00117): `events.Publish` on the publisher's transaction, deliveries
  ordered per aggregate, retried with backoff, dead after eight failures.
- Two properties shape the ledger: **a handler added later gets no backfill**
  (`srv/events/dispatch.go:56`), and **done events are pruned after 30 days**. Events
  cannot be the ledger's only source; it needs a read contract to catch up from (§9.3).
- The rule (platform-packages): *the outbox carries what may lag, never what must refuse*.
- **Secrets** (`srv/secrets`) seals keys per purpose; **the access point port**
  (`srv/invoices/accesspoint`, one adapter: Storecove) sends EHF and is where receiving
  would go; **netguard** guards outbound calls; **Brønnøysund** lookup lives in Customers
  and would serve a supplier register too.
- **Adding a module** is the fourteen-step checklist in module-boundaries, ending with the
  page on the public site.

## 8. The phases

**Phase 1 — the books.** Useful alone: a company keeps its books here and hands its
accountant a SAF-T file.

- **A. The ledger.** Legal entity, fiscal years, periods and locking, the default chart
  with grouping codes, tax codes, vouchers and postings, manual journals with attachments,
  reversal, opening balances, the specifications of §2.2 as screens and exports. The
  accountant role.
- **B. Sales post themselves.** `invoice.issued`, credit notes, payments and their
  removal, reminder charges; posting rules; the customer ledger; write-offs with the VAT
  correction.
- **C. Purchases.** A supplier register, supplier invoices with lines and tax codes,
  approval, the supplier ledger, payment registration; expenses and reimbursements post.
- **D. VAT.** The VAT specification, the term's return as figures and as valid XML, term
  closing, the settlement voucher.
- **E. SAF-T 1.40.** Export validated against the XSD in CI; **import** of another
  system's SAF-T file as opening balances and history, which is the way in from Fiken or
  Tripletex.
- **F. Bank.** camt.053 and CSV import, reconciliation of every movement, taking over from
  Invoices' KID matching where the two meet.

**Phase 2 — filing.** ID-porten and MVA-melding validation and submission; Frister and
Krav og betalinger; inbound EHF through the access point into C; a bank feed and pain.001
payments.

**Phase 3 — the year.** Closing the year; aksjonærregisteroppgaven; årsregnskap for small
companies; then §10.4.

**Not this module:** payroll and A-melding; fixed-asset VAT adjustment and consolidation
until asked.

## 9. Readings, with the defaults proposed

1. **Name.** *Accounting* / **Regnskap**.
2. **The chart is ours**, in the familiar four-digit space, every account with its grouping
   code; customers edit it freely. No NS 4102 text is copied until §10.5 is answered.
3. **Posting is by event, with a contract to catch up.** Books may lag by seconds, and no
   source module is ever refused by the ledger. Each source also serves a read (*documents
   since*), used at activation and to prove completeness. (Overturn: post synchronously in
   the issuing transaction; the ledger could then stop an invoice, which the platform rule
   forbids.)
4. **One legal entity in phase 1**: the seller in Invoices. Its record moves to a shared
   place both modules read; several entities wait for the roadmap item.
5. **A document dated in a locked period posts in the first open one**, keeping its own
   date, and its VAT falls in the term it is posted in. The source is not refused.
6. **Stock is periodic.** Purchases are cost when invoiced; the stock value is a period-end
   voucher. Perpetual valuation waits for Inventory to carry cost.
7. **Tax codes: Accounting owns the list; Invoices keeps its own.** They meet on the
   standard code already snapshotted on every line. (Overturn: move the codes to one owner,
   a migration across three modules.)
8. **Purchases live in Accounting.** Expenses keeps the employee's outlay and the project's
   cost; a supplier invoice entered in Accounting can carry a project. The Expenses kind
   stays for those without the module.
9. **Accounting requires Invoices**, and works without the other business modules.
10. **Posted means posted.** No draft period in which vouchers can still be edited; a
    mistake is reversed the same minute. Simpler than the law allows, and one rule.
11. **Dimensions are project and department**, both existing records.
12. **Build SAF-T 1.40 only**; no customer of ours has books older than 2027.

## 10. The owner's answers (2026-11-14)

1. **System vendor.** Yes: the registrations in §3.4 start *not so far in the future*.
   They block phase 2, not phase 1.
2. **Self-hosted filing.** The self-hoster configures their own Maskinporten client, as
   Altinn documents, and the administration guide says so. No relay. The owner: *this is
   also why our hosted alternative becomes great since we will already have this in
   place*.
3. **Bank feed.** Files in phase 1; a quote from Aritma before phase 2. The owner adds:
   *we will also have the bank integration with Folio soon*; the ledger's statement reads
   that feed too.
4. **Skattemelding.** Left to this note: **not built in this design.** Phase 3 stops at
   aksjonærregisteroppgaven and årsregnskap; the accountant or a year-end product finishes
   from SAF-T. Reopened when customers ask.
5. **NS 4102.** Open: the owner asked why Standard Norge should be asked at all. Until
   answered, §9.2 stands: our own chart and names, nothing copied from the standard.
6. **Price.** No add-ons: bank and year-end are part of Accounting on the hosted service.

## Sources

Fetched 2026-11-13 and 2026-11-14. Four were re-read for this note: the in-force notice,
the SAF-T 1.3 change list, Skatteetaten's VAT and Altinn 3 notice, and Altinn's user
scenarios. The rest were read by the research passes behind this note.

**Law and standards**
- [Bokføringsloven](https://lovdata.no/dokument/NL/lov/2004-11-19-73) ·
  [Bokføringsforskriften](https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558) ·
  [Lov 19 June 2026 nr. 39](https://lovdata.no/dokument/LTI/lov/2026-06-19-39) ·
  [FOR-2026-09-29-1933](https://lovdata.no/dokument/LTI/forskrift/2026-09-29-1933)
- [In-force notice, 19 June 2026](https://www.regjeringen.no/no/aktuelt/nye-lovregler-om-e-fakturering-i-naringslivet-og-enkelte-andre-lovendringer-pa-finansmarkedsomradet-settes-i-kraft/id3166726/) ·
  [Prop. 44 L (2025–2026)](https://www.regjeringen.no/contentassets/c0e0271bc56c43dc82575e4df33d0f39/no/pdfs/prp202520260044000dddpdfs.pdf)
- [Skatteetaten's statement on the regulations, 30 September 2026](https://www.skatteetaten.no/rettskilder/type/uttalelser/uttalelser/forskrifter-til-lovendringer-om-digital-bokforing-og-e-fakturering/)
- [Bokføringsstandarder (NBS)](https://www.regnskapsstiftelsen.no/bokforing/bokforingsstandarder/) ·
  [Regnskapsførerloven](https://lovdata.no/dokument/NL/lov/2022-12-16-90) ·
  [Merverdiavgiftsloven](https://lovdata.no/dokument/NL/lov/2009-06-19-58) ·
  [VAT rates](https://www.skatteetaten.no/satser/merverdiavgift/) ·
  [NS 4102:2023](https://online.standard.no/nb/ns-4102-2023)

**SAF-T**
- [Skatteetaten/saf-t](https://github.com/Skatteetaten/saf-t) ·
  [About 1.3](https://github.com/Skatteetaten/saf-t/blob/master/SAF-T_Financial_1.3/About_SAF-T_Financial_v.1.3.txt) ·
  [Documentation](https://www.skatteetaten.no/bedrift-og-organisasjon/starte-og-drive/rutiner-regnskap-og-kassasystem/saf-t-regnskap/dokumentasjon/) ·
  [Test submission](https://www.skatteetaten.no/bedrift-og-organisasjon/starte-og-drive/rutiner-regnskap-og-kassasystem/saf-t-regnskap/testinnsending/)

**Altinn, Maskinporten, system users**
- [Closure of Altinn II](https://samarbeid.digdir.no/felleslosninger/viktig-informasjon-om-avvikling-av-altinn-ii/3560) ·
  [Maskinporten for consumers](https://samarbeid.digdir.no/maskinporten/konsument/119) ·
  [Terms for end-user system vendors](https://samarbeid.digdir.no/altinn/bruksvilkar-sluttbrukersystemleverandorer-i-altinn/3002)
- [System user](https://docs.altinn.studio/nb/authorization/guides/system-vendor/system-user/) ·
  [User scenarios](https://docs.altinn.studio/nb/authorization/guides/system-vendor/system-user/userscenarios/) ·
  [Access control](https://docs.altinn.studio/nb/authorization/guides/system-vendor/system-user/access-control/)

**Skatteetaten and Brønnøysund APIs**
- [VAT and Altinn 3, 31 March 2026](https://www.skatteetaten.no/samarbeidspartnere/sluttbrukersystemer/sbs-nyheter/merverdiavgiftog-altinn-3-/) ·
  [MVA-melding submission](https://skatteetaten.github.io/api-dokumentasjon/api/mvameldinginnsending) ·
  [mva-meldingen schemas and code lists](https://github.com/Skatteetaten/mva-meldingen) ·
  [System user at Skatteetaten](https://skatteetaten.github.io/api-dokumentasjon/om/systembruker) ·
  [Vendor onboarding](https://www.skatteetaten.no/samarbeidspartnere/reetablering-altinn/systemleverandor/oppkobling/)
- [Frister](https://skatteetaten.github.io/api-dokumentasjon/api/frister) ·
  [Krav og betalinger](https://skatteetaten.github.io/api-dokumentasjon/api/kravogbetalinger) ·
  [Aksjonærregisteroppgaven](https://skatteetaten.github.io/api-dokumentasjon/api/innrapportering-aksjonaerregisteroppgave) ·
  [Skattemeldingen](https://github.com/Skatteetaten/skattemeldingen) ·
  [A-melding](https://skatteetaten.github.io/api-dokumentasjon/api/innrapportering-amelding)
- [Årsregnskap, machine submission](https://brreg.github.io/docs/apidokumentasjon/regnskapsregisteret/maskinell-innrapportering/hvordan-sende-inn/) ·
  [Submission of annual accounts](https://www.brreg.no/en/submission-of-annual-accounts/) ·
  [Enhetsregisteret](https://data.brreg.no/enhetsregisteret/api/dokumentasjon/no/index.html)

**Bank**
- [DNB bank integration](https://www.dnb.no/bedrift/a-aa/bankintegrasjon) ·
  [DNB ERP and integration](https://www.dnb.no/en/business/help-and-guidance/erp-and-integration) ·
  [Nordea Corporate Access](https://www.nordea.com/en/our-services/corporate-access) ·
  [Aritma](https://www.aritma.com/products/api-platform) ·
  [Neonomics](https://www.neonomics.io/) ·
  [Enable Banking](https://enablebanking.com/docs/faq/)

**Products**
- [Fiken](https://fiken.no/priser) · [Tripletex](https://www.tripletex.no/priser/) ·
  [PowerOffice](https://poweroffice.no/priser) · [Conta](https://conta.no/priser/)
