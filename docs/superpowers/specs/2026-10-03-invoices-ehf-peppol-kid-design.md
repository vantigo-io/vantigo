# Invoices — EHF over Peppol, and KID — design (Invoices phase 2)

The third delivery of the Invoices module, on a branch cut from `main` after phase 1B
(PR #129). Research: `docs/superpowers/research/2026-10-03-invoices-ehf-peppol-kid.md`
(§1 the mandate; §2 the document — §2.2 identifiers, §2.3 the mapping, §2.4 the gaps,
§2.5 the edge cases; §3 validation; §4 attachments, receipts and status; §5 the access
point — §5.3 providers, §5.5 sender identity; §6 KID; §7 the codebase; §8 patterns; §10
the decisions it asked for); the 1A research `2026-09-26-invoices-module.md` §4–§5 is its
baseline. Module doc: `docs/src/content/docs/en/reference/invoices.md`. This revision
follows the design's critical review (one blocker — two sends under a shared lock — and
thirty-one findings, each taken or answered in its place).

Phases 1A and 1B issue a lawful PDF and hand it over by e-mail; from **2027-01-01**
(Lov 19. juni 2026 nr. 39) a Norwegian business must receive an e-invoice, and the public
sector has required EHF since 2019. This phase is what makes an invoice to such a buyer
lawful. The delivery:

- **the EHF document**: a Peppol BIS Billing 3.0 UBL 2.1 invoice or credit note, built
  deterministically from the issued document's snapshot the way the PDF is, with the PDF
  embedded, stored once per bytes as the sales document when it is sent as EHF;
- **validation**: a Go pre-check of what the module itself can get wrong, and the
  official XSD and Schematron artefacts run as the test oracle over committed goldens;
- **the seller's e-invoicing identity** on the settings: a Peppol participant id;
- **KID** under the seller's bank agreement — a per-invoice reference computed at issue
  with its algorithm recorded, printed on the PDF, carried in the e-mail and in the EHF's
  payment id;
- **the access point**: a port with one adapter (Storecove's REST API), credentials in
  the secrets box in a table of their own, never returned;
- **the transmission**: an outbox-shaped table and a worker that submits, polls the
  provider and records the receipt; a send that re-checks the receiver against the
  Peppol network and queues under the document's lock; an `unconfirmed` outcome that a
  person resolves rather than a timeout that resends;
- **channel precedence** by the billing profile's preference; the two customer slots
  extended; the OpenAPI contract; the frontend; and the documentation — the reference,
  the user guide in both languages, a new administration page, and the contributing
  guide's validation procedure.

Vantigo stays a sub-ledger; receiving e-invoices (the 2030 duty) is another module's
concern, and the Peppol Invoice Response is a later phase (D13).

`srv/` is `apps/server/internal/`. Money stays as 1A has it: exact decimal inside, JSON
numbers on the wire, absent when it does not apply.

## Decisions

### D1 — Scope, permissions and the switches

No new permission. **Sending as EHF is under `invoices:issue`**, as e-mail is (1B D1).
**The seller's Peppol id, the KID agreement and the access-point credentials are under
`invoices:manage`.**

Two operator settings, in `config.go` by its conventions and in the configuration
reference: `INVOICES_EHF_ENABLED` (default **on**; `0` turns the feature off: the send
answers 503 `ehf_unavailable`, the worker does not start, the settings card says why) and
`INVOICES_STORECOVE_BASE_URL` (default `https://api.storecove.com/api/v2/`, validated
`https` only like `BRREG_BASE_URL`; an operator points it at the sandbox or a mock host
— it is the operator's, never the tenant's, so a `invoices:manage` holder cannot aim the
client anywhere). The Peppol lookup's own switch `PEPPOL_LOOKUP_ENABLED` is the customers
module's; **off, EHF is off too** — a send that cannot re-check does not send.

`GET /invoices/meta` grows `ehfAvailable` (the switch on, the lookup enabled,
credentials stored, the seller's Peppol id set), `accessPointCredentialsRejected` (D9:
the provider refused the key; shown on the settings page and the send dialog) and
`capabilities.canSendEhf` (`invoices:issue` and `ehfAvailable`).

### D2 — The seller's e-invoicing identity, on the settings

`invoices.settings` gains `peppol_id varchar(60)`: the seller's own Peppol participant
identifier, `0192:<organisation number>` for a Norwegian business. **The migration
backfills it** from a valid `organisation_number`, and the seller form defaults it the
same way when empty, so an existing installation is ready without re-saving. Validated as
the customers module validates a buyer's (`^([0-9]{4}):([A-Za-z0-9-]{1,50})$`; a `0192`
value must be the seller's own organisation number). It is **not part of the seller
snapshot**: it is the sender's address on the network at the time of sending; the UBL
reads it at render and the transmission records what was used (D9). A sender needs **no
ELMA registration** to send (research §5.5); it needs an account with an access point
(D7).

### D3 — KID: the agreement, the number, where it goes

**The agreement.** `invoices.settings` gains `kid_length smallint` (4–25: "minimum 3 +
check digit, maximum 25 including the check digit", the OCR giro's rule) and
`kid_algorithm varchar(5)` (`mod10` | `mod11`), both NULL until the seller enters the
pair the bank agreed — **one pair per installation** (the bank's "up to three lengths"
is Out of scope). Saving is refused (400 on `kidLength`) only when the next number does
not fit: `digits(next) > kid_length − 1`, where `next` is the counter's next value, or
`series_start` before the first issue; the response **warns** `kid_headroom_low` when
fewer than two digits of headroom remain (a 100-fold growth), and the card shows it.
Clearing or changing the pair is allowed, with the card's warning that the KIDs of every
open invoice were computed under the old agreement — the bank keeps the old length
valid when asked (that is what its "up to three lengths" is for).

**The number.** At issue, when the agreement is set, an invoice gets
`kid = pad(number, kid_length − 1) ‖ check(number)` — the invoice number zero-padded to
the agreed length less one, then the check digit by the agreement's algorithm: MOD10
(Luhn: weights 2/1 from the right, the digits of each product summed, `(10 − sum mod
10) mod 10`) or MOD11 (weights 2–7 repeating from the right, `11 − (sum mod 11)`; 0 when
the remainder is 0; **`-` when the remainder is 1**, as the specification prescribes).
Both pinned against the specification's own worked examples (`12345678` → `123456782`
under MOD10 and `123456785` under MOD11 — the 1A research's example was wrong, research
§6.1). Per-invoice, from the gap-free number, so unique on the account by construction
and matchable by phase 4's payment imports; a credit note never gets a KID. A number
that does not fit — only possible after the agreement was shortened — refuses the issue
with 409 `kid_length_exceeded`, judged after the number is allocated under the counter
lock so the refusal rolls the number back like every other.

Two columns on `invoices.invoices`, set in the draft→issued update and frozen by the 1A
trigger with the rest: `kid varchar(25)` and `kid_algorithm varchar(5)`. **The stored
KID is re-verified against the stored algorithm** — never the current agreement — when
the PDF or the EHF renders; a mismatch is a 500 and an error log, never a silent
reprint. The UI recommends MOD10 in the agreement's help text and explains the MOD11
`-`; the module implements what the bank agreed.

**Where it goes.** The PDF's payment block gains a "KID" line when set; the e-mail
cover text's payment paragraph says "Merk betalingen med KID {kid}" / "quoting KID
{kid}" instead of the invoice number when a KID exists; the EHF carries it as
`cac:PaymentMeans/cbc:PaymentID` (D4) — and **without a KID the EHF carries no
`PaymentID` at all**: Norwegian receiving systems read that element as a KID, and an
invoice number that happens to pass a check would be paid as one against an account with
no agreement. The CSV export gains a `KID` column **appended last** (the layout is
documented as fixed in order; a new column goes at the end), empty when none; the
reference notes that spreadsheets strip leading zeros.

**Validation is the module's own**: no Billing 3.0 rule checks a KID (research §6.6;
`NOGOV-T10-R012` was EHF 2.0's and a SHOULD).

### D4 — The EHF document: one deterministic UBL per issued document

**Identifiers** (research §2.2): `CustomizationID`
`urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0`,
`ProfileID` `urn:fdc:peppol.eu:2017:poacc:billing:01:1.0`, no `UBLVersionID` (a Peppol
warning), `InvoiceTypeCode` **380** / `CreditNoteTypeCode` **381** — never a negative
invoice. EHF Billing 3.0 Norway is Peppol BIS Billing 3.0 plus NO-R-001 and NO-R-002;
there is no Norwegian customization id. The document type identifiers are the two
strings `srv/peppol/smp.go` already uses, exported (D6); the credit-note one is verified
against a live SMP answer in the spike (research §10, UNCERTAIN).

**The XML** is written by a small hand writer (`srv/invoices/ehf/writer.go`) over
`encoding/xml`'s encoder with prefixed literal element names (`cac:`, `cbc:`) and the
three namespace declarations on the root — `encoding/xml` cannot emit prefixes from
struct tags, and a hand writer makes the element order explicit, which the XSD layer of
the oracle (D11) enforces. Decimals: amounts with exactly two decimals, quantities and
unit prices with their stored scale (3 and 4), never exponent form, through
`big.Rat.FloatString`.

**The mapping**, from the snapshot and nothing else (the settings are read only for the
seller's Peppol id; the directory is never read — the buyer is the snapshot's, as the PDF
prints it):

| UBL | From |
| --- | --- |
| `cbc:ID`, `cbc:IssueDate`, `cbc:DueDate` (invoice only) | `number`, `issue_date`, `due_date` |
| `cbc:DocumentCurrencyCode` | `currency` (NOK) |
| `cbc:BuyerReference` (BT-10) | `your_reference`; **required by Peppol unless an order reference is given** (PEPPOL-EN16931-R003): a document with neither cannot be sent as EHF (D8, 409 `buyer_reference_missing`); drafts warn (D8) |
| `cac:OrderReference/cbc:ID` | `order_reference` when set |
| `cac:BillingReference/cac:InvoiceDocumentReference` (credit note) | the original's `number` and `issue_date` |
| `cac:InvoicePeriod` or `cac:Delivery/cbc:ActualDeliveryDate` | `delivery_from/to` or `delivery_date`; `cac:Delivery/cac:DeliveryLocation/cac:Address` when a place of delivery is set **and has a country** (BR-57); a place without a country is left out of the EHF (the PDF still prints it) |
| `cac:AccountingSupplierParty/cac:Party` | `cbc:EndpointID@schemeID` the seller's Peppol id's scheme and value (D2); `cac:PartyName`; `cac:PostalAddress` the seller snapshot's; `cac:PartyTaxScheme[VAT]/cbc:CompanyID` `NO<orgnr>MVA` **only when `seller_vat_registered`** (NO-R-001, fatal); `cac:PartyTaxScheme[TAX]/cbc:CompanyID` `Foretaksregisteret` **only when `seller_in_foretaksregisteret`** (NO-R-002, a warning when absent — a sole proprietorship rightly gets it); `cac:PartyLegalEntity/cbc:RegistrationName` the legal name and `cbc:CompanyID@schemeID="0192"` the organisation number; `cac:Contact/cbc:ElectronicMail` the seller e-mail when set |
| `cac:AccountingCustomerParty/cac:Party` | `cbc:EndpointID` from `buyer_peppol_id` (scheme = its prefix, value = the rest); `cac:PostalAddress` the buyer snapshot's; **`cac:PartyLegalEntity/cbc:RegistrationName` always** (BT-44 is mandatory — BR-07), with `cbc:CompanyID@schemeID="0192"` for a Norwegian business's organisation number, `cbc:CompanyID` without a scheme for a foreign id, and none for a person |
| `cac:PaymentMeans` (invoice only) | **one**, `cbc:PaymentMeansCode` **30** (credit transfer; 58 is SEPA's EUR shape and wrong on a NOK invoice); `cac:PayeeFinancialAccount/cbc:ID` the domestic account when the buyer is Norwegian, the IBAN with `cac:FinancialInstitutionBranch/cbc:ID` the BIC when the buyer is foreign and the seller has them (else the domestic account); `cbc:PaymentID` the KID when set, **absent otherwise** (D3) |
| `cac:PaymentTerms/cbc:Note` | "Forfall {due}" / "Due {due}" on an invoice; **"Kreditnota – beløpet godskrives" / "Credit note – the amount is credited" on a credit note** (BR-CO-25: a positive payable amount needs terms or a due date, and a credit note has no due date) |
| `cac:TaxTotal/cbc:TaxAmount` and one `cac:TaxSubtotal` per VAT summary row | `vat_total`; per row `taxable_amount`, `vat_amount`, `cac:TaxCategory` with `cbc:ID` the category, **`cbc:Percent` for S, Z, E, AE, G and K — never for O** (BR-O-05); `cbc:TaxExemptionReasonCode` **VATEX-EU-AE / VATEX-EU-G / VATEX-EU-O** for AE, G, O; **`cbc:TaxExemptionReason` the free-text reason for E only** — never on Z (BR-Z-10 forbids any reason on Z) and never on S |
| `cac:LegalMonetaryTotal` | `LineExtensionAmount` and `TaxExclusiveAmount` = `net_total`, `TaxInclusiveAmount` and `PayableAmount` = `gross_total`; no `PayableRoundingAmount` |
| one `cac:InvoiceLine` / `cac:CreditNoteLine` per line | `cbc:ID` the position; **`cbc:InvoicedQuantity` / `cbc:CreditedQuantity`** with `@unitCode` (D5); `cbc:LineExtensionAmount` `line_net`; `cac:AllowanceCharge` with `ChargeIndicator` false, `Amount` `line_allowance`, `BaseAmount` `line_gross`, `MultiplierFactorNumeric` `discount_percent` only when the discount is not zero (PEPPOL-EN16931-R040–R042); `cac:Item/cbc:Name` the description; `cac:Item/cac:ClassifiedTaxCategory` the line's snapshot category, with `cbc:Percent` except for O; `cac:Price/cbc:PriceAmount` `unit_price` |
| `cac:AdditionalDocumentReference` | the stored PDF as `cac:Attachment/cbc:EmbeddedDocumentBinaryObject` Base64 with `@mimeCode="application/pdf"` and `@filename` the download's name; `cbc:ID` the number; `cbc:DocumentDescription` "Faktura (PDF)" / "Invoice (PDF)" |

**Category K** (intra-EEA, a user-created code) needs the buyer's VAT identifier, which
the module does not hold: a document with a K line is refused at send with 409
`ehf_invalid` naming `vat_category_k_unsupported`; the PDF path is unaffected.

**Amounts are positive on a credit note** — the type code signals the credit (research
§2.5); the module stores them positive already. The credit note's **squaring row** (a
taxable amount of 0.00 with a small negative VAT) is carried as stored: BR-CO-17 and
BR-S-09 test `abs(…) ± 1`, a one-krone tolerance, and the walked example passes
(research §2.5). **But BR-S-08 (fatal) requires a line at the row's rate to exist**
before its tolerance applies, and a final note's squaring row usually has none (amended
after the Task 4 review, from the 1.3.16 Schematron source). So the writer adds, for
every VAT row with no line at its (category, rate) — generic over the category — a
**synthetic zero line** after the real ones: `cbc:ID` the highest position plus one
(then two, …), a quantity of `0.000` `C62`, a line amount of `0.00`, the name
"Avrunding merverdiavgift <rate> %" / "VAT rounding <rate> %" by the document's
language, the row's category and rate under the line rules, and a price of `0.0000`.
It is a rendering concern: the stored document is unchanged.

**Deterministic and stored once per bytes.** `renderEHF(doc, sellerPeppolID, pdf)` builds
the XML from the document's rows, the seller's current Peppol id and the stored PDF's
bytes, with a fixed element order and no timestamps beyond the document's own; the same
inputs render the same bytes. The UBL is stored under
`documents/<id>/<number>-<sha256>.xml` in the `invoices` scope (`application/xml`) when
a transmission is created, keyed by its hash, and its key and hash go on the
transmission (D9), never on the document row. Storing at send, not at issue: the UBL is
the sales document only once it is transmitted (§ 5-2-9: the bytes that may have reached
the receiver are the record), and a document only ever e-mailed has no UBL to keep.
**A later send reuses the previous transmission's stored UBL only when that
transmission was `unconfirmed` and a person resolved it as failed** (`failed` with
`resolved_by_user_id` set) — the bytes may have reached the receiver, so a second send
must carry the same ones; after any other `failed` (a provider 4xx, a provider-reported
failure before the AS4 receipt, the age cap on a never-attempted row) or a `cancelled`
the send **renders fresh**, so a mapping fix in a later release or a corrected
seller id is not locked out, and the content-addressed key keeps store-once per bytes.
The send finds the previous object through the latest transmission's `ubl_object_key`.

### D5 — Units: the one free-text field that needs a code

A line's `unit` is free text today (≤ 20). EHF needs a UNECE Recommendation 20 code on
every quantity. The module maps the common Norwegian and English words, trimmed,
case-insensitively and with trailing punctuation stripped (`stk.` → `stk`): `stk`,
`pcs`, `piece` → `C62`; `time`, `timer`, `h`, `hour`, `hours` → `HUR`; `min` → `MIN`;
`dag`, `day` → `DAY`; `uke`, `week` → `WEE`; `mnd`, `month` → `MON`; `år`, `year` →
`ANN`; `kg` → `KGM`; `g` → `GRM`; `m` → `MTR`; `m2`, `m²` → `MTK`; `m3`, `m³` → `MTQ`;
`l`, `liter`, `litre` → `LTR`; `km` → `KMT`; `pakke`, `pack`, `pk` → `XPK`; `sett`,
`set` → `SET`; `kWh` → `KWH` — in one table (`srv/invoices/ehf/units.go`) with a test per
entry; `t` is deliberately not mapped (hour or tonne). An empty unit or an unknown word
maps to **`C62` (one)**; the description carries the meaning. No new column, no picker in
this phase.

### D6 — Reusing the Peppol client: the receiver re-check, and three exports

`srv/peppol` already answers "can this participant receive an invoice / a credit note"
(`Result.CanReceiveInvoice`, `CanReceiveCreditNote`) through the DNS-rebinding guard from
the four `PEPPOL_*` settings; its package doc names Invoices as the next caller.
Invoices builds its own `*peppol.Client` in `newServer` when `PeppolLookupEnabled`
(the customers module's shape), behind `Deps.PeppolLookup` for tests, and calls it
through `contractscalls.go` (`peppolLookup`, outside any lock); a lookup failure is
logged by its kind only, never its message (it can carry the organisation number — the
customers precedent). The package exports `InvoiceDocumentType`,
`CreditNoteDocumentType` and the new `BillingProcessID`
(`urn:fdc:peppol.eu:2017:poacc:billing:01:1.0`).

**The send re-checks at request time** (D8), never the customers module's stored
answer, which is advisory and can be thirty days old; **the worker re-checks again before
submitting when the request's answer is older than 24 hours** (a long outage between
queueing and submission). The stored answer stays what the billing card shows.

### D7 — The access-point port and the Storecove adapter

A port in `srv/invoices/accesspoint/accesspoint.go` (a sub-package of the module, with
the adapter beside it):

```go
type AccessPoint interface {
    // Submit hands one document to the network. The same key twice is one submission:
    // the provider refuses the second with the same 422 it uses for a validation
    // refusal, so Submit answers ErrUnprocessable for both and the worker decides (D9).
    Submit(ctx context.Context, s Submission) (SubmissionRef, error)
    // NextEvent reads one event from the provider's queue; ok is false on an empty queue.
    NextEvent(ctx context.Context) (e Event, ok bool, err error)
    // AckEvent removes an event from the queue once it is applied or found irrelevant.
    AckEvent(ctx context.Context, eventID string) error
    // Evidence answers the provider's receipt and the delivered documents for a
    // submission the receiving access point accepted, or ErrNotYetAvailable.
    Evidence(ctx context.Context, ref SubmissionRef) (Evidence, error)
    // Verify makes the cheapest authenticated read, for the settings page.
    Verify(ctx context.Context) error
}
type Submission struct {
    IdempotencyKey   uuid.UUID
    Sender, Receiver string        // "0192:…"
    DocumentType, ProcessID string
    UBL              []byte
}
type Event struct {
    ID             string             // the queue entry, acknowledged by AckEvent
    SubmissionRef  SubmissionRef
    IdempotencyKey uuid.UUID
    State          SubmissionState    // submitted | delivered | failed
    At             time.Time
    Reason         string             // the provider's wording; kept off the wire and redacted in logs
}
type Evidence struct {
    ReceiptJSON   []byte             // the provider's evidence document, stored as the receipt
    Delivered     []byte             // the transmitted UBL, fetched from the evidence's expiring URL
    DeliveredMIME string
    MessageID, ReceivingAP string
}
```

Error classes the adapter must tell apart, by status: `ErrUnprocessable` carrying the
body's messages (a 422 — a validation refusal **or** a duplicate key, indistinguishable),
`ErrUnauthorized` (401 or 403, the key), `ErrThrottled` with a retry-after (429),
`ErrUnmappedScheme` (raised before any call), `ErrNotYetAvailable` (the evidence's 404),
and a transport or 5xx error (retryable, outcome unknown). `delivered` means **the receiving access point accepted the message
(the AS4 receipt)** — nothing stronger (research §4); the adapter's mapping of the
provider's states onto the four is tested.

**One adapter, Storecove**, by its OpenAPI document as the spike read it (the plan's
"Spike findings"): `Authorization: Bearer <api key>`; `POST document_submissions` with
`legalEntityId`, `idempotencyGuid` (36 characters), `routing.eIdentifiers` `[{scheme,
id}]` — **Storecove's scheme for a Norwegian organisation number is `NO:ORG`**, so the
adapter maps the Peppol scheme `0192` to it (a small table; an unmapped scheme is
`ErrUnmappedScheme` before any call) — and `document: {documentType: "invoice",
rawDocumentData: {document: <base64 UBL>, parseStrategy: "ubl"}}`; the response is
`{guid}`. **Storecove parses the submitted UBL into its own model and regenerates the
UBL it transmits** (its documentation says so): what reaches the receiver is not
Vantigo's bytes. So the record of what was sent is **two things**: the UBL as submitted
(`ubl_sha256`, D4) and **the delivered copy from the provider's evidence**, which the
worker fetches and stores once a transmission is `delivered` (D9) — `GET
document_submissions/{guid}/evidence/sending` answers JSON with the receiving access
point, the message id, an inline receipt XML and **expiring URLs** to the transmitted
documents; the adapter fetches those documents at once and `Evidence` answers the
receipt JSON plus the delivered UBL bytes, both stored. Whether the PDF embedded in
Vantigo's UBL survives the regeneration is **unstated**; the tagged sandbox test asserts
it, and the administration page says it until then. **There is no per-submission status
read**: status arrives as webhooks — pushed to a public URL, or **pulled from the
account's FIFO queue** (`GET webhook_instances/` → one event or 204; `DELETE
webhook_instances/{guid}` acknowledges) — and every event carries the submission's
`guid` and `idempotencyGuid`. The states a Norwegian sender sees: `no_action_taken` (no
routable receiver → `failed`), `failed` (final → `failed`), `succeeded` (the receiving
access point's AS4 receipt → `delivered`); the others are gated on Invoice Response or
tax clearance and are mapped to `submitted` if they ever appear. **The two 422 bodies —
a duplicate `idempotencyGuid` and a validation refusal — are the same shape**, so the
adapter cannot tell them apart; the worker does, by its crash marker (D9). No rate limit,
no 429 and no 5xx are documented; the adapter still classifies them defensively.
Sandbox access is a **sales-contact form with a thirty-day test account**, the same host
with a sandbox key: the plan asks for it on day one, the implementation runs against an
`httptest` Storecove speaking the OpenAPI schemas, and the tagged test runs when
`STORECOVE_SANDBOX_API_KEY` is set. One API key spans several legal entities
(`legalEntityId` is per call), which is what a multi-entity installation would need
later.

**The HTTP client** is the Brreg client's shape — the base URL is the operator's
(D1), so it dials unguarded like Brreg does (the guard refuses loopback and private hosts
and would make a mock impossible) — with `Deps.HTTPTransport` as the seam, a 30-second
per-request timeout, no redirects, and **no retry loop inside the adapter**: the worker
retries under the idempotency key (D9), one attempt per claim, so a timeout after the
provider accepted cannot become a second document.

**Credentials** live in their own table `invoices.access_point_credentials` (one row,
`id = 1`; the communications channel shape, and off the settings row every issue reads
`FOR SHARE`): `provider varchar(20)`, `settings_json text` (non-secret:
`legalEntityId`), `secret_ciphertext text` sealed under
`invoices/access-point-credential`, `rejected_at timestamptz` (set by the worker on a
401/403 or a failed `Open`, cleared by the next successful call or by a new `PUT`; meta
reads it as `accessPointCredentialsRejected`), `updated_at`. **One Storecove account per
installation**: the events worker acknowledges every event it reads, and a shared
account would lose another system's events; the administration page says so. `PUT /invoices/settings/access-point`
(`invoices:manage`) takes `{provider, legalEntityId, apiKey?}` — an omitted key keeps the
stored one, re-sealed in the transaction; the response answers `hasCredentials`, never
the key. `DELETE` clears it and a provider switch replaces it — **both refused with 409
`transmissions_active`** while any transmission is `queued`, `submitted` or
`unconfirmed`. `POST …/verify` calls `Verify` and answers `ok`, `unauthorized`,
`unreachable`. A failed `Open` at send or in the worker is logged at error, sets
`rejected_at`, and leaves the row queued (D9); an empty key is never sent.

### D8 — `POST /invoices/{id}/send-ehf`: judged, re-checked, queued under the lock

`invoices:access+invoices:issue`, no body, its own rate-limit policy (`invoices-send-ehf`,
60 per client per 10 minutes). In order:

1. 503 `ehf_unavailable` when the switch is off, the lookup is disabled, no credentials
   are stored, or the seller has no Peppol id. Judged first.
2. 404; 409 `invoice_draft`; 409 `customer_anonymised`.
3. 409 `no_peppol_id` when the snapshot has no `buyer_peppol_id` — a document issued
   before the customer got one cannot be sent as EHF (credit and re-issue, or e-mail).
4. 409 `buyer_reference_missing` when neither reference is set (D4).
5. 409 `ehf_already_sent` when a transmission is `queued`, `submitted`, `delivered` or
   `unconfirmed` (read without a lock, for a quick answer; judged again in step 9).
6. The stored PDF (`loadStoredPDF`), then `renderEHF`, then **the pre-check** (D11) on
   the bytes → 409 `ehf_invalid` naming the rule ids. Cheap and local, before the network.
7. **The re-check**: `peppolLookup(buyer_peppol_id)`, outside any lock. Not registered,
   or registered without the document's kind → 409 `peppol_not_receivable` carrying
   `peppolRegistered` and `peppolCanReceive`; could not find out → 502
   `peppol_lookup_failed`.
8. The UBL stored once by its hash, unless the latest transmission's bytes are reused
   (D4) — outside any lock; 503 `storage_unavailable`.
9. One `withLockedTx`: **the document `FOR UPDATE`** (two sends serialise here; `FOR
   SHARE` would not — the 1B payments precedent), `ehf_already_sent` judged under the
   lock, then `INSERT invoices.transmissions` as `queued`. **The floor** is a partial
   unique index on `(invoice_id) WHERE status IN ('queued','submitted','delivered','unconfirmed')`;
   its violation maps to 409 `ehf_already_sent`.
10. The document is answered with its `ehf` block (D10).

**Drafts warn early.** A draft whose profile prefers `ehf` or whose customer has a
Peppol id, with neither reference set, carries the warning `ehf_buyer_reference_missing`
(1A's warnings mechanism) on every read and save, and the editor shows it: the reference
is editable only before issue, and `buyer_reference_missing` after issue has no remedy
but a credit note.

### D9 — The transmission: an outbox row and the `invoices-ehf` worker

**`invoices.transmissions`:** `id bigint`, `invoice_id` (`ON DELETE RESTRICT`),
`provider varchar(20)`, `idempotency_key uuid UNIQUE`, `sender_participant varchar(60)`,
`receiver_participant varchar(100)`, `document_type varchar(300)`, `process_id
varchar(100)`, `ubl_object_key varchar(300)`, `ubl_sha256 char(64)`, `pdf_sha256
char(64)`, `status varchar(20)` (`queued` | `submitted` | `delivered` | `failed` |
`unconfirmed` | `cancelled`), `provider_ref varchar(200)`, `evidence_object_key
varchar(300)`, `evidence_sha256 char(64)`, `submit_attempts int`, `poll_attempts int`, `next_attempt_at`,
`submit_attempted_at` (the outbox's crash marker), `lease_id`, `lease_until`,
`last_error varchar(500)` (the port's `Reason`, with anything matching an e-mail address
or a `0192:` identifier replaced by a placeholder), `lookup_registered bool`,
`lookup_can_receive bool`, `lookup_at`, `queued_at`, `submitted_at`, `delivered_at`,
`failed_at`, `cancelled_at`, `resolved_by_user_id uuid`, `resolution_note varchar(500)`,
`created_by_user_id uuid`. Indexes: the partial unique of D8; `(status, next_attempt_at)
WHERE status IN ('queued','submitted','unconfirmed') OR (status = 'delivered' AND
evidence_object_key IS NULL AND provider_ref IS NOT NULL)` — a delivered row stays
claimable until its evidence is stored. **Every query over this table takes the time
as `@now` from `Deps.Clock()`**, never SQL `now()`: the harness clock sits weeks from
the database's, and a lease or a cap judged by `now()` would be wrong under test.

Triggers: a parent trigger **on INSERT only** (`FOR SHARE` the document, refuse a
draft's, re-check the anonymisation marker after the wait, as the delivery insert does)
— so the worker's claim `UPDATE` takes no document lock; no DELETE ever; an UPDATE may
change only the state columns (`status`, `provider_ref`, `evidence_object_key`, the
attempt counters, `next_attempt_at`, `submit_attempted_at`, the lease, `last_error`, the
`lookup_*` columns, the timestamps, the resolution) — identity and the UBL's key and
hash are frozen; a terminal row (`failed`, `cancelled`) changes nothing; a `delivered`
row keeps its status and `delivered_at` frozen and may change only the lease,
`next_attempt_at`, `poll_attempts`, `last_error`, and `evidence_object_key` with
`evidence_sha256` once, while the evidence is NULL.

**The worker** (`srv/invoices/ehf_worker.go`, `Module.Workers` gated on the switches),
the communications outbox's shape: poll 5 s; claim by conditional `UPDATE` with a
60-second lease; **one provider call per claim**, bounded to the adapter's 30 s, so no
claim outlives half its lease; every completion is a lease-checked `UPDATE` that is a
no-op when the lease changed (the outbox's rule, copied).

- `queued` → when `lookup_at` is older than 24 h the lookup runs again first in the
  same claim (a lookup is not a provider call; the `lookup_*` columns are refreshed;
  a receiver no longer registered or no longer accepting the document type → `failed`
  with the reason `receiver_not_receivable`, the marker untouched, and the claim ends)
  → the key opened from the credentials row (a failed `Open` leaves the row queued,
  reschedules it an hour out, logs at error and sets `rejected_at`) → the UBL fetched
  from the store and checked against `ubl_sha256` → **stamp `submit_attempted_at`
  (auto-committed, immediately before the call)** → `Submit` → `submitted` with
  `provider_ref` and `submitted_at`, and `rejected_at` cleared. **The crash marker is
  never touched by a claim**; a completion restores it to its pre-claim value only
  when the outcome proves the provider did not take the document — `ErrUnauthorized`,
  `ErrThrottled`, `ErrUnmappedScheme`. A transport failure or a 5xx → `submit_attempts
  + 1`, `next_attempt_at = now + backoff(attempts)` (`min(3600, 2^n)` s), still
  `queued`; `ErrThrottled` → the provider's retry-after, no attempt counted;
  `ErrUnauthorized` → stays `queued` an hour out, no attempt counted, `rejected_at`
  set, an error log; `ErrUnmappedScheme` → `failed` with the reason; the 422 rule
  below. **The cap is by age only**: 48 hours since `queued_at` → `unconfirmed` when
  `submit_attempted_at` is set (the provider may have the document), `failed` when it
  never was. Attempts are not capped — the backoff reaches its hourly ceiling — so a
  provider outage of some minutes does not strand every queued document on a person's
  desk; the 48 hours stay under any dedupe window the spike finds.
- **Status comes from the queue.** A second worker, `invoices-ehf-events`, under an
  advisory lease (the customers module's shape — one replica drains at a time), polls
  `GET webhook_instances/` every 30 s while any row is `submitted` or `unconfirmed`:
  each event is matched to a row by `provider_ref` (or by `idempotency_key` when the row
  never learned its reference) and applied **idempotently, without a row lease** — one
  `UPDATE … WHERE status IN ('queued','submitted','unconfirmed')` that sets
  `provider_ref` when missing: `succeeded` → `delivered` with `delivered_at`; `failed`
  or `no_action_taken` → `failed` with the reason; `0 rows` (the row already terminal,
  or no row of ours) is logged by guid — and then **always** acknowledged with
  `DELETE`. A `queued` row with the marker set counts as awaiting an event. The queue
  is drained until 204.
- `submitted` rows also probe on their own cadence by `poll_attempts` (5 min, 15 min,
  then hourly): `Evidence` **is the status proxy** — 404 means not delivered yet, 200
  means delivered even if the event was lost — so a row the queue never mentions still
  completes. `delivered` is committed first; **then** the next claim fetches `Evidence`
  and stores it once (`Exists` before `Put`) under
  `documents/<id>/<number>-<transmission>-receipt.json` with the delivered UBL beside it
  as `…-delivered.xml`, `evidence_sha256` the receipt's — a `delivered` row with a NULL
  `evidence_object_key` and a reference is claimable, and a failed fetch retries on the
  cadence; `submitted` without a reference is never probed, only rescheduled hourly;
  still `submitted` **seven days after `submitted_at`** → `unconfirmed`.
- **The 422 rule.** A 422 on the first attempt (`submit_attempted_at` was NULL before
  this claim) is a validation refusal → `failed` with the body's messages as the reason;
  a 422 on a retry (the marker was already set) may be the duplicate-key refusal of a
  submission that went through → `submitted` without a reference, which the queue drain
  resolves by `idempotency_key`, or `unconfirmed` if nothing arrives in seven days.
- `unconfirmed` is terminal for the machine and open for a person: the transmission
  **blocks a new send** (D8's index includes it) until an `invoices:issue` holder
  resolves it with `POST /invoices/{id}/transmissions/{transmissionId}/resolve
  {outcome: "delivered" | "failed", note}` after checking with the provider — the
  outcome, who and the note are recorded; `failed` then allows a new send (with a fresh
  render, D4), `delivered` closes it. The worker keeps probing an `unconfirmed` row with
  a provider reference once a day for thirty days and resolves it itself if the
  provider finally answers (`resolved_by_user_id` NULL, the note saying so); after
  thirty days, or at once without a reference, `next_attempt_at = 'infinity'` and the
  row waits for a person.

**Cancel.** `POST …/transmissions/{transmissionId}/cancel` (`invoices:issue`): only a
`queued` row that was **never attempted** — `UPDATE … WHERE status = 'queued' AND
submit_attempted_at IS NULL AND (lease_until IS NULL OR lease_until < @now)` — becomes
`cancelled`; anything else is 409 `transmission_not_cancellable`.

Both workers hold a `*server` built by `newServer` from worker mode's `Deps` — the
pool, the secrets box, the clock, the configuration and an object store built from the
configuration (`Deps` carries none) — so every lookup and provider call goes through
`contractscalls.go`; they need no directory. A lookup-disabled installation still
probes and drains but leaves a `queued` row alone (the send refused it already). Every state change is logged with the transmission id and the provider
reference, never the receiver's identifier.

### D10 — The document's EHF state, and channel precedence

An issued document answers `ehf` (absent on a draft): `{status, queuedAt, submittedAt,
deliveredAt, failedAt, providerRef?, reason?, canSend, blockedBy?, preference?,
buyerPeppolId?, transmissions[]}` — `preference` and `buyerPeppolId` for
`invoices:issue` holders and **not gated on mail being available**, since an EHF-only
installation has no SMTP;
`status` the latest transmission's or `not_sent`; `reason` and `providerRef` only for
`invoices:issue` holders; `canSend` what D8 would answer without the network, with
`blockedBy` naming the first refusal code; `transmissions[]` every row's identity, state,
timestamps, the UBL's hash, the resolution, and a `ublUrl` for `GET
/invoices/{id}/transmissions/{transmissionId}/ubl` (`invoices:access`; the stored XML as
`application/xml`, verified against its hash — a missing or altered object is a 500,
never a re-render). The list item gains `ehfStatus` on issued documents.

**Channel precedence** (the roadmap's rule, "EHF when the receiver accepts the document
type, otherwise the billing profile's preference"): the receiver's acceptance is known
only at the send's re-check, so the UI decides by what it has — when `canSendEhf` and
the profile's preference (`sendDefaults.preference`) is `ehf`, or the buyer has a Peppol
id and the preference is unset, **the issued document's primary action is "Send as
EHF"** and e-mail is secondary; the e-mail dialog then warns `ehf_preferred` (emitted
by the server in place of `delivery_preference_ehf` when `canSendEhf`) and, for a
Norwegian business from 2027-01-01, `buyer_norwegian_business_required` as 1B does.
Each dialog warns when the other channel already carried the document (`deliveries[]`
non-empty, or an EHF transmission active or delivered). Neither is refused for the
other.

### D11 — Validation: a Go pre-check, and the official artefacts as the oracle

**The pre-check** (`srv/invoices/ehf/precheck.go`) runs on the rendered bytes and names
rule ids. What a request can refuse in words (409 `ehf_invalid`): the buyer reference
rule (R003); the buyer endpoint scheme in the **full EAS list** (`PEPPOL-EN16931-CL008`'s
source, vendored as a table with its version); a K category; the seller's VAT id shape
when registered (NO-R-001's own expression). What only the module can break — the totals
re-summed from lines and VAT rows (BR-CO-10/13/15), the stored KID against its
algorithm, the attachment present, a unit code outside the table — is a **500 and an
error log**, never a user-facing refusal.

**The oracle** runs in CI and on demand, never in the server:

- Two layers. **XSD**: the UBL 2.1 schemas from OASIS, pinned by checksum, validated
  with the JDK's own `javax.xml.validation` (a single-file `Validate.java`) — Schematron
  checks nothing about element order or names, and that is what a hand writer can get
  wrong. **Schematron**: both artefacts pinned by checksum — the CEN EN 16931 rules
  (`ConnectingEurope/eInvoicing-EN16931` release `validation-1.3.16`, whose
  `en16931-ubl-1.3.16.zip` ships the compiled XSLT) and the Peppol BIS Billing 3.0 rules
  (`OpenPEPPOL/peppol-bis-invoice-3`, `rules/sch/PEPPOL-EN16931-UBL.sch`, which carries
  NO-R-001/002 — the repository ships Schematron sources only, so the oracle compiles
  them once with the ISO Schematron XSLT 2.0 skeleton, also through Saxon; pinned by the
  newest tag, `v3.0.20` today, and bumped to 3.0.21 — published 2026-05-20, mandatory
  from 2026-08-17 — the day it is tagged) — run through Saxon-HE 12.7 from Maven
  Central, pinned; the SVRL output parsed, **failing on `flag="fatal"` only** and
  reporting warnings.
- Toolchain: `java = "temurin-21"` in `mise.toml`'s `[tools]`; `tools/ehf/validate.sh`
  downloads the artefacts into a cache with checksums; `mise run ehf:validate` is the
  task; a CI job with `install_args: "java"` runs it on every pull request (cheap; no
  path filter).
- Fixtures: `srv/invoices/ehf/testdata/golden/*.xml` are the documents `TestEHF_Goldens`
  renders from fixed fixtures (`-update` rewrites them): an invoice with S at every
  seeded rate plus Z, E, AE and G (O is exclusive to a non-registered seller — a second
  fixture); a discount; a foreign buyer; a person; a credit note and a partial credit
  note with the squaring row; a KID under each algorithm; IBAN and BIC to a foreign
  buyer; a delivery period and a place. `testdata/invalid/*.xml` with a manifest of the
  rule ids each must trip (no buyer reference; a malformed endpoint; tampered totals; a
  reason on Z; a percent on O) — the oracle must report exactly those, and the pre-check
  must agree where it has the rule.
- The spring and autumn artefact releases are a pin bump with the goldens re-validated.
  Hosted validators are for a person, never a dependency.

### D12 — The slots, retention and privacy

- **Merge**: transmissions hang off the document by id; nothing to re-point.
- **Export**: each document's section gains `transmissions` (provider, state,
  timestamps, the receiver participant id, the UBL's hash, the resolution) — no bytes.
- **Erase**: a `queued` transmission never attempted is **cancelled** (the cancel rule's
  conditional update, reported as `invoices.transmissions` with the count); every other
  row is kept untouched: the UBL, like the PDF, is the sales document under § 13 and
  carries the buyer snapshot; the receiver's identifier is an organisation's or the
  snapshot's own. The anonymisation table in the customers reference gains the row.
- **Retention**: the UBL and the receipt are kept five years after the end of the
  financial year, as the PDF is; the module never deletes an object.

### D13 — Out of scope, named

Receiving e-invoices (2030; another module); the Peppol Invoice Response and Profile
02; message-level status beyond the port's four states; provider webhooks (a callback
endpoint is a later addition behind the same port); a second provider (the port expects
one — Qvalia next; SendRegning has no sandbox; a Storecove account is sold through a
sales contact, which the administration page says plainly); running as one's own access
point; eFaktura and AvtaleGiro; a bank agreement with several KID lengths; a KID per
customer; a unit-code picker and a `unit_code` column; foreign currency in the EHF; the
intra-EEA category K; Schematron at runtime; resending a `delivered` document; sending
drafts; bulk send; the document-level allowance; the "corrected invoice" type 384; a
unit-code table wider than D5's.

### D14 — The OpenAPI contract

`openapi/invoices.yaml` (the API reference regenerates from it):

- Operations: `postInvoicesByIdSendEhf`, `postInvoicesByIdTransmissionsByTransmissionIdCancel`,
  `…Resolve`, `getInvoicesByIdTransmissionsByTransmissionIdUbl` (`application/xml`),
  `putInvoicesSettingsAccessPoint`, `deleteInvoicesSettingsAccessPoint`,
  `postInvoicesSettingsAccessPointVerify`.
- Schemas: `InvoicesEhfState`, `InvoicesTransmission`, `InvoicesTransmissionResolution`,
  `InvoicesAccessPointRequest`/`Response`, `InvoicesAccessPointVerifyResponse`; the
  settings request/response gain `peppolId`, `kidLength`, `kidAlgorithm` and the
  response the `kid_headroom_low` warning; the document response gains `kid`,
  `kidAlgorithm` and `ehf`, the list item `ehfStatus`, meta `ehfAvailable`,
  `accessPointCredentialsRejected`, `canSendEhf`.
- Codes on `InvoicesConflictProblem`: `ehf_unavailable`, `no_peppol_id`,
  `buyer_reference_missing`, `ehf_already_sent`, `peppol_not_receivable` (+
  `peppolRegistered`, `peppolCanReceive`), `peppol_lookup_failed` (502), `ehf_invalid` (+
  `rules[]`), `kid_length_exceeded`, `transmissions_active`,
  `transmission_not_cancellable`, `transmission_not_resolvable`.
- Warnings: `ehf_buyer_reference_missing`, `ehf_preferred`, `kid_headroom_low`.

### D15 — The frontend

- **Settings** (`invoices:manage`): an **E-invoicing** card — the seller's Peppol id
  (prefilled, editable, in the completeness list); the access point: provider, legal
  entity id, API key (write-only, "stored"), Verify, Remove (refused while transmissions
  are active, in words); the switch's state when off; the credentials-rejected notice. A
  **KID** card — length and algorithm with the help text, the next KID previewed, the
  headroom warning and the change warning.
- **Invoice page, issued document**: the primary action by D10; "Send as EHF" opens a
  dialog that names the receiver id and says the network will be asked whether it
  accepts this kind of document, shows the 1B warnings and the cross-channel note; on 200
  "Queued for sending as EHF". An **E-invoice card** with the state in honest words (Not
  sent / Queued / Submitted / Delivered to the receiver's access point / Failed /
  Unconfirmed – needs a check with the provider / Cancelled), timestamps, the provider
  reference and reason for issuers, Cancel while never attempted, Resolve for an
  unconfirmed one (outcome and note), "Download EHF (XML)", and a new Send after a
  failure or a resolved failure; every refusal of D8 in the reader's language.
- **Draft editor**: the `ehf_buyer_reference_missing` warning beside the references.
- **List**: an EHF column on issued documents.
- **The PDF** gains the KID line; the e-mail text the KID sentence; the e-mail dialog the
  `ehf_preferred` warning.
- en + nb throughout.

### D16 — Documentation, as its own deliverable

Per `AGENTS.md`'s page map:

- `docs/src/content/docs/en/reference/invoices.md`: "E-invoicing: EHF over Peppol" (the
  identifiers, the mapping table incl. the category rules, units, the pre-check and the
  oracle, the port and its four states and what `delivered` means, the transmission
  table, the worker's cadences and caps, `unconfirmed` and resolution, the send's
  refusals in order, channel precedence, retention); "KID" (the agreement and its
  headroom rule, the number and the stored algorithm, where it goes, no `PaymentID`
  without a KID); the model, endpoints and permissions tables; the e-mail section's KID
  sentence; retention and anonymisation; "What comes next" to phase 3.
- `en/user/invoices.md` and `nb/user/invoices.md`: "Sending as EHF" (what the states
  mean, what to do when unconfirmed or failed, the buyer-reference trap), "KID on
  invoices", the settings' two cards, the list's EHF column, the draft warning.
- **A new administration page** `en/admin/e-invoicing.md` and `nb/admin/e-invoicing.md`:
  EHF and Peppol in two paragraphs; the duty dates; choosing an access point, opening a
  Storecove account (through their sales contact; sandbox first); the API key and where
  it is stored; `INVOICES_EHF_ENABLED`, `INVOICES_STORECOVE_BASE_URL` and the lookup's
  switch; the `invoices-ehf` worker and worker mode; the KID bank agreement — what to ask
  the bank for and what changing it means; what to do when a transmission is unconfirmed
  or failed. Listed in the admin overview.
- `en/contributing/`: a short "E-invoice validation" section (or page) for `mise run
  ehf:validate`, the artefact pins and the fixtures — a contributor procedure, English
  only.
- `admin/authentication.md`'s configuration reference: the two settings;
  `admin/installation.md` "Background workers and scaling": the worker;
  `admin/object-storage.md` (en + nb): the `.xml` and receipt keys;
  `reference/customers.md`: the anonymisation list gains `invoices.transmissions`.
- `ROADMAP.md`: phase 2 done, phase 3 next.
- The new admin page's `sources`: `apps/server/internal/invoices/accesspoint`,
  `apps/server/internal/invoices/ehf`, `tools/ehf`.

## Readings on the record

1. `INVOICES_EHF_ENABLED` defaults on; `PEPPOL_LOOKUP_ENABLED=0` disables EHF too.
2. One KID (length, algorithm) per installation; the KID is the zero-padded invoice
   number plus its check digit; the algorithm is stored with the KID; MOD11's remainder-1
   case yields `-`.
3. The headroom rule refuses only what does not fit and warns under two digits.
4. Units map by a word table with `C62` as the fallback; `t` is not mapped.
5. The UBL is stored at the first send and reused only after an `unconfirmed` outcome
   a person resolved as failed; any other failure or a cancel renders fresh.
6. No `PaymentID` without a KID; one `PaymentMeans`, code 30, the IBAN for a foreign
   buyer.
7. The send queues under the document's `FOR UPDATE`; the worker submits once per
   claim; status is drained from the provider's queue and probed by evidence, never
   pushed to us; an unknown outcome is `unconfirmed` and a person resolves it.
8. `delivered` is the AS4 receipt and is worded so.
9. Storecove first, by its OpenAPI document: status is drained from its pull queue by
   a leased second worker, evidence is the proof probe, and the delivered copy from
   the evidence is stored as the transmitted record because Storecove regenerates the
   UBL; a 422 on a retry is read as a possible duplicate, never as a refusal.
10. The access-point client is unguarded like Brreg's; the URL is the operator's.
11. Transmissions are kept through anonymisation; a never-attempted queued one is
    cancelled.
12. Channel precedence is decided by the profile's preference and the buyer's Peppol
    id; the receiver's acceptance is the send's re-check.
13. Category K is refused at send.
14. No new permission.
15. The cap on a queued transmission is by age (48 hours) only; attempts back off to an
    hourly ceiling and are never capped, and a 401, 403 or 429 counts as no attempt.
16. The crash marker is stamped immediately before the provider call and never touched
    by a claim; only an outcome that proves the provider did not take the document
    restores it.
17. Every transmissions query takes its time from `Deps.Clock()` as `@now`.
18. One Storecove account per installation, because the event queue is account-wide
    and the drain acknowledges every event.
19. The Peppol artefact is pinned at `v3.0.20`, the newest tag; 3.0.21 is adopted the
    day it is tagged.
20. A VAT row without a line at its category and rate gets a zero-quantity line in the
    EHF, because BR-S-08 requires one; the stored document is unchanged.

## Testing

Through 1A's harness, plus `Deps.PeppolLookup` (a fake answering registered / not /
cannot-receive-credit-notes / error), `Deps.HTTPTransport` for the adapter (an
`httptest` Storecove speaking the confirmed contract: accept; 422 duplicate with and
without the reference; 422 validation; 401; 429 with retry-after; 5xx; status in each
state; evidence), a fake object store, the fixed clock, and `WithEnv` for the switches.

- **Settings**: the Peppol id backfill, default and validation; the KID pair's bounds,
  the fit rule and the headroom warning against the counter and against `series_start`;
  the access-point PUT never returns the key, keeps it when omitted, `hasCredentials`,
  DELETE and a switch refused with `transmissions_active`, verify's three answers;
  `invoices:manage` on each.
- **KID**: both algorithms against the specification's worked examples and the `-`
  case; the KID and its algorithm on an invoice at issue, never on a credit note; the
  PDF line, the e-mail sentence and the CSV's last column; `kid_length_exceeded` rolls
  the number back; a document issued before the agreement has none; the stored KID
  re-verified against the stored algorithm after the agreement changes (a render still
  works; a tampered KID is a 500).
- **The UBL**: the goldens byte-stable; each mapping row pinned through an XML
  read-back: identifiers and no `UBLVersionID`; both parties incl. the buyer's
  `RegistrationName` for a person and the scheme-less foreign id; VAT id only when
  registered; Foretaksregisteret only when registered; the single `PaymentMeans` with
  the account or the IBAN; `PaymentID` only with a KID; the period or the date and the
  place only with a country; the credit note's reference, positive amounts, terms note
  and `CreditedQuantity`; the allowance only with a discount; the unit table, the
  punctuation strip and the fallback; no `Percent` on O; the exemption code on AE/G/O,
  the text on E only, nothing on Z; the squaring row; the embedded PDF's bytes; a K line
  refused; the render pure, and different when the seller's Peppol id changes.
- **The oracle**: `mise run ehf:validate` green on every golden in CI; every invalid
  fixture trips exactly its manifest's rules; the pre-check agrees where it has the
  rule.
- **The send**: every refusal in D8's order, each shown by removing its guard; the
  pre-check before the lookup; the lookup through the fake, never the directory, its
  failure a 502 logged by kind; **two racing sends → one queued, one `ehf_already_sent`**
  (held on a seam after the lookup; the second waits on the lock and is refused; with
  the index alone the insert fails and maps to the same code); the UBL stored once and
  reused after a resolved `unconfirmed`, rendered fresh after any other `failed` or a
  `cancelled`; the draft warning;
  the rate limit; `invoices:issue`.
- **The worker**: claim and lease (two workers, one submission); one call per claim;
  the crash marker stamped before `Submit` and restored on 401, 429 and an unmapped
  scheme; the 422 rule's two branches (first attempt → `failed` with the messages; a
  retry → `submitted` without a reference); backoff on 5xx; the retry-after on 429
  counting no attempt; `ErrUnauthorized` keeps the row, counts no attempt and flags
  meta; the age cap → `unconfirmed` or `failed` by the marker; the probe cadence;
  `delivered` committed before evidence, evidence stored once (`Exists` before `Put`)
  and re-probed after a fetch failure; the seven days → `unconfirmed`; the daily probe
  of an unconfirmed row resolving it, and `'infinity'` after thirty days; the 24-hour
  lookup refresh in the submit claim, and `receiver_not_receivable`; a failed `Open`
  leaves the row and flags meta; the lease-changed no-op; the switch off → no worker;
  worker-mode `Deps` works. **The events worker**: a match by reference and by key; a
  duplicate event; an event after the probe already delivered; an event for a failed
  row; an unknown event acknowledged; the drain stops at 204; two workers, one drain.
- **Cancel and resolve**: cancel only never-attempted queued (a race with the claim
  loses); resolve only `unconfirmed`, recorded with who and the note; a resolved failure
  allows a new send.
- **The document's `ehf` block**, `blockedBy`, the list's status, the issuer-only
  fields; the UBL download verified, 500 when gone.
- **The slots**: export carries transmissions; erase cancels the never-attempted and
  keeps the rest, reporting the count; run twice reports zero.
- **The integration test** (`srv/integration`): a real customer with a Peppol id and
  `invoiceDelivery: ehf` → issue → send-ehf against the fake lookup and the `httptest`
  provider → the worker run once → `submitted`, again → `delivered` with evidence.
- **Frontend**: the settings cards; the primary action by precedence; the dialogs and
  the E-invoice card through every state incl. resolve; the draft warning; both
  catalogs.
- **Docs**: the pages of D16 checked against the code as 1B's were; `mise run
  docs:check`.
