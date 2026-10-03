# Invoices — EHF over Peppol, and KID — design (Invoices phase 2)

The third delivery of the Invoices module, on a branch cut from `main` after phase 1B
(PR #129). Research: `docs/superpowers/research/2026-10-03-invoices-ehf-peppol-kid.md`
(§1 the mandate, §2 the document, §3 validation, §4 attachments and status, §5 the access
point, §6 KID, §7 the codebase, §8 patterns, §10 the decisions it asked for); the 1A
research `2026-09-26-invoices-module.md` §4–§5 is its baseline. Module doc:
`docs/src/content/docs/en/reference/invoices.md`. Phases 1A and 1B issue a lawful PDF and
hand it over by e-mail; from **2027-01-01** (Lov 19. juni 2026 nr. 39) a Norwegian
business must receive an e-invoice, and the public sector has required EHF since 2019.
This phase is what makes an invoice to such a buyer lawful.

The delivery:

- **the EHF document**: a Peppol BIS Billing 3.0 UBL 2.1 invoice or credit note, built
  deterministically from the issued document's snapshot the way the PDF is, with the PDF
  embedded, stored once as the sales document when it is sent as EHF;
- **validation**: a Go pre-check of what the module itself can get wrong, and the
  official Schematron artefacts run as the test oracle over committed golden documents;
- **the seller's e-invoicing identity** on the settings: a Peppol participant id;
- **KID** under the seller's bank agreement — a per-invoice reference computed at issue,
  printed on the PDF, carried in the e-mail and in the EHF's payment id;
- **the access point**: a port with one adapter (Storecove's REST API), credentials in
  the secrets box, never returned;
- **the transmission**: an outbox-shaped table and a worker that submits, polls the
  provider's status and records the receipt; a send endpoint that re-checks the receiver
  against the Peppol network and queues; the document shows its EHF state;
- the two customer slots extended, the frontend, and the documentation — the reference,
  the user guide in both languages, and a new administration page.

Vantigo stays a sub-ledger; receiving e-invoices (the 2030 duty) is another module's
concern, and the Peppol Invoice Response is a later phase (D13).

`srv/` is `apps/server/internal/`. Money stays as 1A has it: exact decimal inside,
JSON numbers on the wire, absent when it does not apply.

## Decisions

### D1 — Scope, permissions and the switch

No new permission. **Sending as EHF is under `invoices:issue`**, as e-mail is (1B D1):
whoever may hand a document over may hand it over this way. **The seller's Peppol id,
the KID agreement and the access-point credentials are under `invoices:manage`**, the
settings' own key.

One operator switch, `INVOICES_EHF_ENABLED` (default **on** — the duty is the norm, and
without credentials nothing is sent anyway; `0` turns the feature off: the send answers
503 `ehf_unavailable`, the worker does not start, the settings card says why). One
operator setting, `INVOICES_STORECOVE_BASE_URL` (default `https://api.storecove.com/api/v2/`;
an operator points it at the sandbox or a mock — it is the operator's, never the
tenant's, so a `invoices:manage` holder cannot aim the client at an internal host). Both
in `config.go` by its conventions, both in the configuration reference.

`GET /invoices/meta` grows `ehfAvailable` (the switch on **and** credentials stored
**and** the seller's Peppol id set) and `capabilities.canSendEhf` (`invoices:issue` and
`ehfAvailable`).

### D2 — The seller's e-invoicing identity, on the settings

`invoices.settings` gains `peppol_id varchar(60)`: the seller's own Peppol participant
identifier, `0192:<organisation number>` for a Norwegian business. It **defaults from the
organisation number** when the seller record is saved and the field is empty — the UI
offers it, the server fills it — and is validated like the customers module validates a
buyer's (`^([0-9]{4}):([A-Za-z0-9-]{1,50})$`, and a `0192` value must be the seller's own
organisation number: a seller does not send under someone else's identity). It is **not
part of the seller snapshot**: it is the sender's address on the network at the time of
sending, not a fact about the document; the UBL reads it at generation (D4) and the
transmission records what was used.

A sender needs **no ELMA registration** to send (research §5); it needs an account with
an access point (D7).

### D3 — KID: the agreement, the number, where it goes

**The agreement.** `invoices.settings` gains `kid_length smallint` (4–25, the OCR
giro's "minimum 3 + check digit, maximum 25 including the check digit") and
`kid_algorithm varchar(5)` (`mod10` | `mod11`), both NULL until the seller has a bank
agreement and enters it — **one (length, algorithm) pair per installation**, the bank
account being one; the agreement's "up to three lengths" is Out of scope. Saving the pair
is refused (400 on `kidLength`) when `kid_length − 1` is smaller than the digits of the
counter's next number **plus two** — a KID must have room for a hundred times more
documents than exist, so an agreement is not outgrown next year. Clearing the pair is
allowed; documents issued under it keep their KID.

**The number.** At issue, when the agreement is set, an invoice gets
`kid = pad(number, kid_length − 1) ‖ check(number)` — the invoice number zero-padded to
the agreed length less one, then the check digit by the agreement's algorithm:
MOD10 (Luhn, weights 2/1 from the right, digit sums) or MOD11 (weights 2–7 from the
right, `11 − (sum mod 11)`, 0 when the remainder is 0, and **`-` when the remainder is
1**, as the OCR specification prescribes). Per-invoice, derived from the gap-free number,
so unique on the account by construction and matchable by phase 4's payment imports
(research §6: a "fast KID" per customer is AvtaleGiro's need, not OCR giro's). A credit
note never gets a KID (it has no payment block). A number that does not fit the agreed
length — impossible by the headroom rule unless the agreement was shortened in the bank
and re-entered — refuses the issue with 409 `kid_length_exceeded` before the number is
allocated (the check runs on the counter's next value under the counter lock; a refusal
rolls back as every issue refusal does).

The column `invoices.invoices.kid varchar(25)`, set at issue, is frozen by the 1A
trigger like every other column. The UI shows the MOD11 `-` case for what it is and
recommends MOD10 in the agreement's help text; the module implements what the bank
agreed.

**Where it goes.** The PDF's payment block gains a KID line ("KID" / "KID") when set;
the e-mail cover text's payment paragraph says "Merk betalingen med KID {kid}" /
"quoting KID {kid}" instead of the invoice number when a KID exists (1B's texts, one
substitution); the EHF carries it as `cac:PaymentMeans/cbc:PaymentID` (D4). Without an
agreement nothing changes from 1B: the number is the remittance text. The CSV export
gains a `KID` column after `Number` (empty when none) — the accountant matches on it.

**Validation is the module's own**: no Billing 3.0 rule checks a KID (research §6; the
rule people remember, `NOGOV-T10-R012`, was EHF 2.0's and only a SHOULD). The module
computes it, pins both algorithms against the specification's worked examples, and
re-verifies the stored KID against the stored number when it renders (a mismatch is a
500, never a silent reprint).

### D4 — The EHF document: one deterministic UBL per issued document

**Identifiers** (research §2.1): `CustomizationID`
`urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0`,
`ProfileID` `urn:fdc:peppol.eu:2017:poacc:billing:01:1.0`, UBL 2.1, `InvoiceTypeCode`
**380** for an invoice and `CreditNoteTypeCode` **381** for a credit note — never a
negative invoice: the module has a numbered credit note already. EHF Billing 3.0 Norway
is Peppol BIS Billing 3.0 plus two Norwegian checks (NO-R-001, NO-R-002); there is no
Norwegian customization id. The document type identifiers are the two strings
`srv/peppol/smp.go` already holds (exported now, D6), the credit-note one verified against
a live SMP answer in the first task — it is pattern-substituted in the research (§2.1,
UNCERTAIN).

**The mapping**, from the snapshot and nothing else (the settings are read only for the
seller's Peppol id, D2; the directory is never read — the buyer's address is the
snapshot's, as the PDF prints it):

| UBL | From |
| --- | --- |
| `cbc:ID`, `cbc:IssueDate`, `cbc:DueDate` (invoice only) | `number`, `issue_date`, `due_date` |
| `cbc:DocumentCurrencyCode` | `currency` (NOK) |
| `cbc:BuyerReference` (BT-10) | `your_reference`; **required by Peppol unless an order reference is given** (PEPPOL-EN16931-R003): a document with neither cannot be sent as EHF (D8, 409 `buyer_reference_missing`) |
| `cac:OrderReference/cbc:ID` | `order_reference` when set |
| `cac:BillingReference/cac:InvoiceDocumentReference` (credit note) | the original's `number` and `issue_date` |
| `cac:InvoicePeriod` or `cac:Delivery/cbc:ActualDeliveryDate` | `delivery_from/to` or `delivery_date`; `cac:Delivery/cac:DeliveryLocation` when a place of delivery is set |
| `cac:AccountingSupplierParty/cac:Party` | `cbc:EndpointID@schemeID="0192"` the seller's Peppol id value; `cac:PartyName` the legal name; `cac:PostalAddress` the seller snapshot's; `cac:PartyTaxScheme[VAT]/cbc:CompanyID` `NO<orgnr>MVA` **when `seller_vat_registered`** (NO-R-001, fatal; absent otherwise); `cac:PartyTaxScheme[TAX]/cbc:CompanyID` `Foretaksregisteret` **when `seller_in_foretaksregisteret`** (NO-R-002, a warning when absent, which a sole proprietorship rightly gets); `cac:PartyLegalEntity` registration name and `cbc:CompanyID@schemeID="0192"` the organisation number; `cac:Contact/cbc:ElectronicMail` the seller e-mail when set |
| `cac:AccountingCustomerParty/cac:Party` | `cbc:EndpointID` from `buyer_peppol_id` (scheme = its prefix, value = the rest); `cac:PartyName`/`cac:PostalAddress` the buyer snapshot's; `cac:PartyLegalEntity/cbc:CompanyID@schemeID="0192"` for a Norwegian business, the foreign id for a foreign one; a person gets name and address only |
| `cac:PaymentMeans` (invoice only) | code **30** with `cac:PayeeFinancialAccount/cbc:ID` the domestic account; a second `cac:PaymentMeans` code **58** with the IBAN and `cac:FinancialInstitutionBranch/cbc:ID` the BIC when the seller has both; `cbc:PaymentID` the KID when set, else the invoice number as the remittance text |
| `cac:PaymentTerms/cbc:Note` | "Forfall {due}" / "Due {due}" |
| `cac:AllowanceCharge` (document level) | none — the module has no document-level allowance |
| `cac:TaxTotal/cbc:TaxAmount` and one `cac:TaxSubtotal` per VAT summary row | `vat_total`; per row `taxable_amount`, `vat_amount`, `cac:TaxCategory` with `cbc:ID` the category, `cbc:Percent`, `cbc:TaxExemptionReasonCode` **VATEX-EU-AE / VATEX-EU-G / VATEX-EU-O** for AE, G, O and `cbc:TaxExemptionReason` the free-text reason for E and Z (research §2.3) |
| `cac:LegalMonetaryTotal` | `LineExtensionAmount` and `TaxExclusiveAmount` = `net_total`, `TaxInclusiveAmount` and `PayableAmount` = `gross_total`; no `PayableRoundingAmount` |
| one `cac:InvoiceLine` / `cac:CreditNoteLine` per line | `cbc:ID` the position; `cbc:InvoicedQuantity@unitCode` (D5); `cbc:LineExtensionAmount` `line_net`; `cac:AllowanceCharge` with `ChargeIndicator` false, `Amount` `line_allowance`, `BaseAmount` `line_gross`, `MultiplierFactorNumeric` `discount_percent` when the discount is not zero (PEPPOL-EN16931-R040–R042); `cac:Item/cbc:Name` the description; `cac:Item/cac:ClassifiedTaxCategory` the line's snapshot category and rate; `cac:Price/cbc:PriceAmount` `unit_price`; on a credit note `cac:BillingReference` on the line is not used — the document-level reference suffices |
| `cac:AdditionalDocumentReference` | the stored PDF as `cac:Attachment/cbc:EmbeddedDocumentBinaryObject` Base64 with `@mimeCode="application/pdf"` and `@filename` the download's name; `cbc:ID` the number; `cbc:DocumentDescription` "Faktura (PDF)" / "Invoice (PDF)" |

**Amounts are positive on a credit note** — the type code signals the credit (research
§2.3); the module stores them positive already. The credit note's **squaring row** (a
taxable amount of 0.00 with a small negative VAT) is **lawful as it is**: BR-CO-17 and
BR-S-09 test `abs(…) ± 1`, a one-krone tolerance, and the walked example passes
(research §2.3); no rounding amount and no document allowance is added for it.

**Deterministic and stored once.** `renderEHF(doc)` builds the XML from the document's
rows and the stored PDF's bytes, with a fixed element order and no timestamps beyond the
document's own; the same document renders the same bytes. The UBL is stored once at the
first EHF send under `documents/<id>/<number>-<sha256>.xml` in the `invoices` scope
(`application/xml`), and its key and hash go on the transmission (D6), never on the
document row — storing at send, not at issue, because the UBL is the sales document only
once it is transmitted (§ 5-2-9: the bytes sent are the record), and a document that
is only ever e-mailed has no UBL to keep. A second transmission of the same document
(after a failure) reuses the stored object — the bytes are the same by construction, and
the record must not fork.

### D5 — Units: the one free-text field that needs a code

A line's `unit` is free text today (≤ 20). EHF needs a UNECE Recommendation 20 code on
every quantity. The module maps the common Norwegian and English words — `stk`, `pcs`,
`piece` → `C62`; `time`, `timer`, `h`, `hour`, `hours` → `HUR`; `dag`, `day` → `DAY`;
`mnd`, `month` → `MON`; `kg` → `KGM`; `g` → `GRM`; `m` → `MTR`; `m2`, `m²` → `MTK`;
`m3` → `MTQ`; `l`, `liter`, `litre` → `LTR`; `km` → `KMT`; `pakke`, `pack` → `XPK`;
`sett`, `set` → `SET`; `kWh` → `KWH` — case-insensitively, trimmed, in one table
(`units.go`) with a test per entry; an empty unit or an unknown word maps to **`C62`
(one)** and the word itself goes nowhere (the description carries the meaning). No new
column, no picker in this phase: the words people type are the ones the table knows, and
the fallback is lawful.

### D6 — Reusing the Peppol client: the receiver re-check, and two exports

`srv/peppol` already answers "can this participant receive an invoice / a credit note"
(`Result.CanReceiveInvoice`, `CanReceiveCreditNote`) by the SML→SMP walk, through the
DNS-rebinding guard, from the four `PEPPOL_*` settings; its package doc names Invoices as
the next caller. Invoices builds its own `*peppol.Client` from the same configuration in
`newServer` when `PeppolLookupEnabled` (the customers module's shape), behind
`Deps.PeppolLookup` for tests, and calls it through `contractscalls.go` (`peppolLookup`,
outside any lock). The two document type identifier constants are exported
(`peppol.InvoiceDocumentType`, `peppol.CreditNoteDocumentType`) and the process
identifier `urn:fdc:peppol.eu:2017:poacc:billing:01:1.0` is added beside them — the
adapter declares them (D7).

**The send re-checks at send time** (D8), never the customers module's stored answer:
that answer is advisory and can be thirty days old (research §5). The stored answer stays
what the billing card shows and what offers "Use EHF".

### D7 — The access-point port and the Storecove adapter

A port in `srv/invoices/accesspoint.go`:

```go
type AccessPoint interface {
    // Submit hands one document to the network. The same key submitted twice is one
    // submission: the provider refuses the second, and Submit answers ErrAlreadySubmitted
    // with the first's reference when it can learn it.
    Submit(ctx context.Context, s Submission) (SubmissionRef, error)
    // Status answers where a submission is, in the port's own vocabulary.
    Status(ctx context.Context, ref SubmissionRef) (SubmissionStatus, error)
    // Evidence answers the provider's receipt for a delivered submission, as bytes to keep.
    Evidence(ctx context.Context, ref SubmissionRef) ([]byte, string, error)
}
type Submission struct {
    IdempotencyKey uuid.UUID
    Sender, Receiver string            // participant ids, "0192:…"
    DocumentType, ProcessID string     // the Peppol identifiers
    UBL []byte                          // the stored document, as is
}
type SubmissionStatus struct {
    State       SubmissionState   // submitted | delivered | failed | unknown
    At          time.Time
    ProviderRef string
    Reason      string            // the provider's wording of a failure, kept off the wire
}
```

`SubmissionState` is the port's vocabulary, and **`delivered` means the receiving access
point accepted the message (the AS4 receipt)** — nothing stronger (research §4): not that
the buyer saw it, not that it was approved. The provider's richer states map onto these
four; the mapping is the adapter's and is tested.

**One adapter, Storecove** (`srv/invoices/accesspoint/storecove.go`, a sub-package of the
module): `Authorization: Bearer <api key>`, `POST document_submissions` with the UBL as
the document body, the sender's `legalEntityId`, the receiver's `eIdentifiers`
(`0192`, value) and the `idempotencyGuid`; status by `GET document_submissions/{guid}`
(the pull shape; webhooks need a public callback a self-hosted installation may not
have, D13); evidence by `GET document_submissions/{guid}/evidence`. The exact field
names of the UBL body and the status pull are **UNCERTAIN** in the research (§5) and are
settled by the plan's first task, a spike against Storecove's OpenAPI document and
sandbox, before the adapter is written; the adapter's own tests run against an
`httptest` server speaking the confirmed contract, and one tagged test (`storecove`)
runs against the sandbox when `STORECOVE_SANDBOX_API_KEY` is set and is never part of
`go test ./...`.

**The HTTP client** dials through the DNS-rebinding guard (`netguard`, the Peppol
client's shape) — the base URL is the operator's, but the client carries a secret and
there is no reason to be less careful than the lookup is — with `Deps.HTTPTransport` as
the test seam, a 30-second per-request timeout, no redirects, and the Brreg client's
retry on 5xx/408 (three attempts, jittered backoff) for `Status` and `Evidence` only:
**`Submit` is never retried by the HTTP client** — the worker retries it under the
idempotency key (D9), so a timeout after the provider accepted cannot become a second
document.

**Credentials.** `invoices.settings` gains `access_point_provider varchar(20)`
(`storecove` or NULL), `access_point_settings_json text` (non-secret: `legalEntityId`)
and `access_point_secret_ciphertext text` — the API key sealed under the purpose
`invoices/access-point-credential`, the communications channel shape: `PUT
/invoices/settings/access-point` (`invoices:manage`) takes `{provider, legalEntityId,
apiKey?}`, an omitted key keeps the stored one, the response answers `hasCredentials`
and never the key; `DELETE` clears all three. A failed `Open` at send time is a 503
`ehf_unavailable` and an error log — never an empty key sent to the provider. `POST
/invoices/settings/access-point/verify` (`invoices:manage`) calls the provider's
cheapest authenticated read and answers ok or the failure's class; the settings page
has the button.

### D8 — `POST /invoices/{id}/send-ehf`: judged, re-checked, queued

`invoices:access+invoices:issue`, no body. In order:

1. 503 `ehf_unavailable` when the switch is off, no credentials are stored, the seller
   has no Peppol id, or the Peppol lookup is disabled (`PEPPOL_LOOKUP_ENABLED=0` — a send
   that cannot re-check does not send). Judged first.
2. 404; 409 `invoice_draft`; 409 `customer_anonymised` (the 1B marker).
3. 409 `no_peppol_id` when the snapshot has no `buyer_peppol_id` — the buyer's address is
   the snapshot's, and a document issued before the customer got one cannot be sent as
   EHF (credit and re-issue, or e-mail it).
4. 409 `buyer_reference_missing` when neither `your_reference` nor `order_reference` is
   set (D4).
5. 409 `ehf_already_sent` when a transmission for this document is `queued`,
   `submitted` or `delivered`; a `failed` or `cancelled` one allows a new send.
6. **The re-check**: `peppolLookup(buyer_peppol_id)` through the guard, outside any
   lock. Not registered, or registered without the document's kind → 409
   `peppol_not_receivable` carrying `peppolRegistered` and `peppolCanReceive`; a lookup
   that could not find out → 502 `peppol_lookup_failed` (retry, never "not registered").
7. **The pre-check** (D11) on the rendered UBL → 409 `ehf_invalid` listing the rule ids.
8. The stored PDF (1B's `loadStoredPDF`), then `renderEHF`, then the UBL stored once
   (D4) — all outside any lock; 503 `storage_unavailable` as the download answers.
9. One `withLockedTx`: the document `FOR SHARE` (nothing of it changes; the trigger
   shape); `ehf_already_sent` judged again under the lock (two sends race here);
   `INSERT invoices.transmissions` as `queued` with the idempotency key, the sender and
   receiver ids, the document type and process id, the UBL's key and hash, the Peppol
   lookup's answer and when.
10. The document is answered with its `ehf` block (D10). The worker takes it from here.

A rate limit of 60 per client per 10 minutes, the e-mail send's.

### D9 — The transmission: an outbox row and the `invoices-ehf` worker

**`invoices.transmissions`:** `id bigint`, `invoice_id` (`ON DELETE RESTRICT`),
`provider varchar(20)`, `idempotency_key uuid UNIQUE`, `sender_participant varchar(60)`,
`receiver_participant varchar(60)`, `document_type varchar(300)`, `process_id
varchar(100)`, `ubl_object_key varchar(300)`, `ubl_sha256 char(64)`, `pdf_sha256
char(64)`, `status varchar(20)` (`queued` | `submitted` | `delivered` | `failed` |
`cancelled`), `provider_ref varchar(200)`, `evidence_object_key varchar(300)`,
`attempts int`, `next_attempt_at timestamptz`, `lease_id`, `lease_until`, `last_error
varchar(500)` (the port's `Reason`, redacted of anything that looks like an address),
`lookup_registered bool`, `lookup_can_receive bool`, `lookup_at timestamptz`,
`queued_at`, `submitted_at`, `delivered_at`, `failed_at`, `cancelled_at`,
`created_by_user_id uuid`. Triggers: a parent trigger (the 1B shape: `FOR SHARE` the
document, refuse a draft's); no DELETE ever; an UPDATE may change only the state
columns (`status`, `provider_ref`, `evidence_object_key`, `attempts`,
`next_attempt_at`, the lease, `last_error`, the four timestamps) — the identity columns
and the UBL's key and hash are frozen. A partial index on `(status, next_attempt_at)
WHERE status IN ('queued','submitted')`.

**The worker** (`srv/invoices/ehf_worker.go`, `Module.Workers` gated on the switch),
the communications outbox's shape: poll 5 s; claim by conditional `UPDATE` with a
60-second lease (the row is the lock, replica-safe); `queued` → `Submit` (the API key
opened from the settings row, the UBL fetched from the store, the PDF's hash checked)
→ `submitted` with `provider_ref` and `submitted_at`; `ErrAlreadySubmitted` is treated
as success with the first reference; a transport failure or a provider 5xx → `attempts +
1`, `next_attempt_at = now + backoff(attempts)` (the outbox's `min(3600, 2^n)` s), still
`queued`; a provider 4xx that is not "already submitted" → `failed` at once with
`last_error`. `submitted` → `Status` on a slower cadence (`next_attempt_at` one minute,
then five, then fifteen, then hourly, for up to **seven days**): `delivered` →
`delivered_at` and `Evidence` fetched and stored once under
`documents/<id>/<number>-<transmission>-receipt.<ext>`; `failed` → `failed_at` and the
reason; `unknown` after seven days → `failed` with "no answer from the provider", for a
person to look at. Every state change is logged with the transmission id and the
provider reference, never the receiver's identifiers beyond the scheme.

The worker needs the pool, the secrets box, the clock, the configuration and an object
store it builds itself from the configuration (worker mode's `Deps` carries none; the
1B precedent is the module's own `storage.New` in `newServer`); it needs **no
directory** — the re-check happened in the request. A worker-mode installation that
runs the API with `WORKERS_IN_PROCESS=0` runs it in the worker process, as every worker.

**Cancel.** `POST /invoices/{id}/transmissions/{transmissionId}/cancel`
(`invoices:issue`): a `queued` transmission not yet claimed becomes `cancelled`; any
other state is 409 `transmission_not_cancellable` — once submitted it is on the network.

### D10 — The document's EHF state, on the wire

An issued document answers `ehf` (absent on a draft): `{status, queuedAt,
submittedAt, deliveredAt, failedAt, providerRef?, reason?, canSend, transmissions[]}` —
`status` the latest transmission's, or `not_sent`; `reason` only for `invoices:issue`
holders (it is the provider's wording); `canSend` what D8 would answer without sending
(the switch, credentials, the seller id, the buyer id, the reference, and no active
transmission — the lookup is not run for a read); `transmissions[]` every row's
identity, state and timestamps, the UBL's hash, and a `ublUrl` for `GET
/invoices/{id}/transmissions/{transmissionId}/ubl` (`invoices:access`; the stored XML as
`application/xml`, verified against its hash like the PDF). The list item gains
`ehfStatus` on issued invoices and credit notes. The 1B send defaults keep their
warnings; `delivery_preference_ehf` now reads as a pointer to the EHF action in the UI.

### D11 — Validation: a Go pre-check, and the official artefacts as the oracle

**The pre-check** (`srv/invoices/ehf/precheck.go`) is what the module itself can get
wrong and what the request can refuse in words: the buyer reference rule (R003), the
buyer endpoint id well-formed with a known EAS scheme (`0192` and the EAS list's
Norwegian and Nordic schemes, a table with its source), the seller's VAT id shape when
registered (NO-R-001's own regular expression and MOD-11), the totals re-summed from
the lines and the VAT rows (BR-CO-10, BR-CO-13/15), the KID's check digit when set,
the attachment present, the unit codes in the table. Each failure names a rule id; the
send answers them (D8); the CI oracle proves the pre-check never contradicts the
artefacts on the goldens.

**The oracle.** The official Schematron artefacts (Peppol BIS Billing 3.0, pinned
release 3.0.21 — the one mandatory from 2026-08-17 — plus EHF's two Norwegian rules)
need an XSLT 2.0 processor; no Go engine runs them (research §3). So they run in **CI
and on demand**, not in the server: `tools/ehf/validate.sh` downloads the pinned
artefacts with a checksum, runs Saxon-HE under mise's Java over every file in
`srv/invoices/ehf/testdata/golden/*.xml`, and fails on any fatal; the goldens are the
documents `TestEHF_Goldens` renders from fixed fixtures — an invoice with every VAT
category the module seeds, one with a discount, a foreign buyer, a person, a credit
note, a partial credit note with the squaring row, one with a KID, one with IBAN and
BIC — and the test fails when a golden drifts from the render (`-update` rewrites
them). `mise run ehf:validate` is the task; the CI job runs it on every pull request
that touches the module. The spring and autumn artefact releases are a pin bump with
the goldens re-validated. Hosted validators are for a person, never a dependency.

### D12 — The slots, retention and privacy

- **Merge**: transmissions hang off the document by id; nothing to re-point.
- **Export**: each document's section gains `transmissions` (provider, state,
  timestamps, the receiver participant id, the UBL's hash) — no bytes.
- **Erase**: transmissions are **kept untouched**, reported as `invoices.transmissions`
  at 0: the UBL, like the PDF, is the sales document and carries the buyer snapshot
  under § 13; the receiver's participant id is an organisation's identifier or the
  snapshot's own. A person's documents are rarely EHF — a person has no Peppol id — but
  the rule is stated. The anonymisation table in the customers reference gains the row.
- **Retention**: the UBL and the receipt are kept five years after the end of the
  financial year, as the PDF is; the module never deletes an object (1A); a document
  sent as EHF whose UBL object is missing or fails its hash is a 500 on the UBL download
  and an error log, never a re-render (the bytes sent are the record).

### D13 — Out of scope, named

Receiving e-invoices (2030; another module); the Peppol Invoice Response and Profile
02; message-level status beyond what the provider reports as the port's four states;
provider webhooks (poll only; a callback endpoint is a later addition behind the same
port); a second provider (the port is designed for one more adapter; SendRegning has
no sandbox, Qvalia is the next candidate); running as one's own access point; eFaktura
and AvtaleGiro; a bank agreement with several KID lengths; a KID per customer; a
unit-code picker and a `unit_code` column; foreign currency in the EHF (the exchange
rate columns stay at 1); Schematron at runtime; resending a `delivered` document;
sending drafts; bulk send; the document-level allowance; the "corrected invoice" type
384.

### D14 — The frontend

- **Settings** (`invoices:manage`): an **E-invoicing** card — the seller's Peppol id
  (prefilled, editable, with the checklist line "Peppol id set"); the access point:
  provider (Storecove), legal entity id, API key (write-only, "stored" shown), Verify,
  Remove; the switch's state when off ("E-invoicing is switched off on this
  installation"). A **KID** card — length and algorithm with the help text, the next
  KID previewed from the counter, the headroom refusal in words.
- **Invoice page, issued document**: "Send as EHF" (`canSendEhf` and `ehf.canSend`)
  opening a dialog that shows what the send will check (the receiver id, "the network
  will be asked whether this receiver accepts an invoice / a credit note") and the 1B
  warnings; on 200 the notification "Queued for sending as EHF"; an **E-invoice card**
  with the state line (Not sent / Queued / Submitted / Delivered to the receiver's
  access point / Failed / Cancelled — the honest wording from D7), the timestamps, the
  provider reference and reason for `invoices:issue` holders, Cancel while queued,
  "Download EHF (XML)", and a new Send after a failure; every refusal of D8 in the
  reader's language.
- **List**: an EHF column on issued documents (the status in one word).
- **The PDF** gains the KID line; the e-mail text the KID sentence.
- The customers billing card is unchanged: it already shows the stored lookup and
  offers "Use EHF".
- en + nb throughout.

### D15 — Documentation, as its own deliverable

Per `AGENTS.md`:

- `docs/src/content/docs/en/reference/invoices.md`: new sections "E-invoicing: EHF over
  Peppol" (the identifiers, the mapping table, units, the pre-check and the oracle, the
  port and its states and what `delivered` means, the transmission table and the
  worker, the send's refusals, retention), "KID" (the agreement, the number, where it
  goes); the model table (settings columns, `kid`, `transmissions`); the endpoints and
  permissions tables; the e-mail section's KID sentence; the retention and
  anonymisation paragraphs; "What comes next" to phase 3.
- `docs/src/content/docs/en/user/invoices.md` and `nb/user/invoices.md`: "Sending as
  EHF", "KID on invoices", the settings' two cards, the list's EHF column.
- **A new administration page** `docs/src/content/docs/en/admin/e-invoicing.md` and
  `nb/admin/e-invoicing.md`: what EHF and Peppol are in two paragraphs, the duty dates,
  choosing an access point and opening a Storecove account (sandbox first), the API key
  and where it is stored, `INVOICES_EHF_ENABLED` and `INVOICES_STORECOVE_BASE_URL`, the
  worker and worker mode, the KID bank agreement (what to ask the bank for), the
  validation task for contributors, what to do when a transmission fails.
- `admin/authentication.md`'s configuration reference: the two settings;
  `admin/installation.md` "Background workers and scaling": the `invoices-ehf` worker;
  the admin and user overviews list the new page and sections.
- `ROADMAP.md`: phase 2 done, phase 3 next.
- The spec's `sources` are already on the invoices pages; the new admin page lists
  `apps/server/internal/invoices/accesspoint`, `apps/server/internal/invoices/ehf` and
  `tools/ehf`.

## Readings on the record

1. `INVOICES_EHF_ENABLED` defaults on.
2. One KID (length, algorithm) per installation; the KID is the zero-padded invoice
   number plus its check digit; MOD11's remainder-1 case yields `-` as the OCR
   specification says, and the UI recommends MOD10.
3. Units map by a word table with `C62` as the fallback; no column, no picker.
4. The UBL is stored at the first EHF send, not at issue.
5. `PaymentID` is the KID, else the invoice number.
6. The send queues; the worker submits; status is polled, not pushed.
7. `delivered` is the AS4 receipt and is worded so.
8. Storecove first; its exact request fields are settled by a spike before the adapter.
9. The access-point client dials through the DNS-rebinding guard although the base URL
   is the operator's.
10. Transmissions are kept through anonymisation, reported at 0.
11. The seller's Peppol id is not part of the snapshot.
12. Sending EHF needs no new permission.

## Testing

Through 1A's harness, plus `Deps.PeppolLookup` (a fake answering registered / not /
cannot-receive-credit-notes / error), `Deps.HTTPTransport` for the adapter (an
`httptest` Storecove speaking the confirmed contract: accept, reject the duplicate key
with 422, status in each state, evidence), a fake object store, the fixed clock, and
`WithEnv` for the switch.

- **Settings**: the Peppol id default and validation (a `0192` id must be the seller's
  own number); the KID pair's bounds and the headroom rule against the counter; the
  access-point PUT never returns the key, keeps it when omitted, `hasCredentials`,
  DELETE clears, verify ok / unauthorised / unreachable; `invoices:manage` on each.
- **KID**: both algorithms against the specification's worked examples and the `-`
  case; the KID on an invoice at issue and never on a credit note; the PDF line, the
  e-mail sentence and the CSV column; `kid_length_exceeded` under the counter lock; a
  document issued before the agreement has none; the stored KID re-verified on render.
- **The UBL**: the goldens (every fixture of D11) byte-stable; each mapping row pinned
  through an XML read-back (not string matching): identifiers, both parties, the VAT
  id only when registered, Foretaksregisteret only when registered, the endpoint
  schemes, the payment means (30 alone; 30 and 58 with IBAN; the KID or the number as
  PaymentID; none on a credit note), the period or the date, the line allowance only
  with a discount, the unit table and the fallback, the exemption code for AE/G/O and
  the text for E/Z, the credit note's reference and positive amounts, the squaring row
  as a subtotal with 0.00 and a negative VAT, the embedded PDF's bytes equal to the
  stored object; the render is pure (a mutation of the settings after issue changes
  nothing but the seller's Peppol id).
- **The oracle**: `mise run ehf:validate` green on every golden in CI; the pre-check
  flags exactly the fixtures the artefacts flag (a fixture without a buyer reference,
  one with a malformed endpoint, one whose totals were tampered).
- **The send**: every refusal in D8's order, each shown by removing its guard; the
  re-check uses the fake lookup and never the directory; the lookup error is 502; two
  racing sends → one queued, one `ehf_already_sent`; the UBL stored once and reused by
  a resend after a failure (one object, two rows); the rate limit; `invoices:issue`.
- **The worker**: claim and lease (two workers, one submission); `Submit` once per key;
  the duplicate-key answer treated as success; retry with backoff on a 5xx; `failed` on
  a 4xx; the status cadence and the seven-day cut-off under the fixed clock; evidence
  stored once; no directory, no store call under the claim transaction beyond the
  row; the switch off → no worker; worker-mode `Deps` (no object store) still works.
- **Cancel**: queued only.
- **The document's `ehf` block** and the list's status; `reason` only for issuers; the
  UBL download verified against its hash, 500 when the object is gone.
- **The slots**: export carries transmissions; erase reports `invoices.transmissions`
  at 0 and touches nothing.
- **The integration test** (`srv/integration`): a real customer with a Peppol id and
  `invoiceDelivery: ehf` → issue → send-ehf against the fake lookup and the `httptest`
  provider → the worker run once → `delivered`.
- **Frontend**: the settings cards; the send dialog and the E-invoice card through
  every state; the KID preview; both catalogs.
- **Docs**: the new admin page and the reference sections checked against the code as
  1B's docs were; `mise run docs:check`.
