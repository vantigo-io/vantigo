# Invoices phase 6 — delivery D, eFaktura and AvtaleGiro: implementation plan

This plan details **delivery D** of Invoices phase 6. It is kept in its own file so it
does not conflict with the shared phase-6 plan
(`docs/superpowers/plans/2026-11-17-invoices-phase6-plan.md`), whose *Deliveries B to E*
section points here for D. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-invoices-phase6-design.md`, revision 4:
  - §5 (I29–I35);
  - §14 Q1, decided: Mastercard Payment Services (MPS), direct;
  - §14 Q2, the default: no fødselsnummer;
  - §15.
- **The research:** `docs/superpowers/research/2026-11-17-invoices-competitive-gaps.md`, §4.2
  and §7.
- **MPS's public developer documentation**, read on 2026-11-18. Its findings are in *MPS:
  what the public documentation says* below, and what is unsure there is marked
  **UNSURE**.

Code references are against `main` at `6cf1dcf20`, where the latest migration is `00193`.

**Abbreviations**, as in the shared plan:

- `srv/` is `apps/server/internal/`;
- `inv/` is `srv/invoices/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/invoices/frontend/src/`;
- `cfe/` is `apps/customers/frontend/src/`.

## How D lands

- **Branch:** `claude/invoices-efaktura`, one PR against `main`. D depends on neither B
  nor C.
- **Migrations:**
  - D takes the next free numbers when it opens its PR, and checks them against `main`
    again right before merging (see the memory *migration number collisions*).
  - Already claimed:
    - `00194`–`00196` by the api-access stack;
    - `00197` by B-1 and `00198` by B-2;
    - most likely `00199` by C.
  - Below, D's two files are named **`NNNNN_invoices_efaktura.sql`** and
    **`NNNNN+1_customers_date_of_birth.sql`**.
- **Order** (spec §14 Q8): D is last. Its code can be built and merged before Vantigo has
  its submitter agreement with MPS:
  - every test, the seed and local development run against the fake (`mpstest`, D2);
  - sending through MPS for real is configuration that an operator turns on (D3). It
    ships off.

## MPS: what the public documentation says

Sources (all read 2026-11-18):

- **[COMBO]** <https://mastercardpaymentservices.com/norway/utvikler/combo-api>
- **[KSA]** <https://www.efaktura.no/developer/documentation/invoice-solutions-ws-efaktura-atg-submitter-v1/>,
  the Kombo Submitter API, a SOAP service (WSDL and XSD published)
- **[KMS]** <https://www.efaktura.no/developer/documentation/kombo-mastercard-submitter-api/>,
  the Kombo variant in which MPS hosts the invoice
- **[MANDATE]** <https://www.mastercardpaymentservices.com/norway/utvikler/creditor-mandate-api/>.
  The OpenAPI file is <https://dokumentasjon.bits.no/api/spec/bits/avtalegiro/AvtaleGiro-Creditor-APIs.yaml>.
- **[BP]** <https://www.efaktura.no/media/nzcemrz0/best-practice-efaktura-apis.pdf>
- **[ONB]** <https://www.efaktura.no/media/2wkncm10/einvoice-webservice-onboarding-guide.pdf>
- **[DL]** <https://www.mastercardpaymentservices.com/norway/utvikler/deadlines/>
- **[KID]** <https://www.mastercardpaymentservices.com/norway/utvikler/kid/>

**The recipient lookup** is `getEfakturaIdentifiers` [KSA]:

- **Batch:** up to 1,000 keys in one call.
- **The identifiers a key may carry:**
  - `efakturaIdentifier` (9 digits);
  - **either** `socialSecurityNumber` (11 digits) **or** `organizationNumber`;
  - `telephoneAlias` (with the country code) and `emailAlias`;
  - `name` (first and last) and `address` (lines, postal code, city, country);
  - `dateOfBirth`;
  - `efakturaReferenceAndContentProvider`;
  - optionally `efakturaIssuer`, the issuer's organisation number.
- **The matching rule:**
  - one unique key (the identifier, or the fødselsnummer) is enough;
  - if two or more of phone, e-mail and date of birth are given, at least two must match;
  - if only one of them is given, the name, the address or the postal code must match as
    well;
  - if none of them is given, the name, the address and the postal code must all match.
- **The answer:**
  - `responseResult`: `UNIQUE_RECEIVER_FOUND`, `RECEIVER_NOT_FOUND`,
    `LOOKUP_KEY_MISSING_DATA` or `MULTIPLE_RECEIVERS`;
  - `efakturaIdentifier`;
  - `autoAcceptEfaktura`, the consumer's "I want everything" choice;
  - with the issuer given, `issuerResponse` with `blockedByReceiver`,
    `hasEfakturaAgreement` and `efakturaReferences`.
- **Advice in [BP]:**
  - always pass the issuer, so a block is seen;
  - store the identifier rather than looking it up before every invoice;
  - a lookup by aliases alone is slow;
  - calls may run in parallel.
- **Rate limits:** not published (**UNSURE**).
- **So the spec's default holds without a fødselsnummer.** Phone, e-mail and date of
  birth, with the name and address, satisfy the matching rule. Q2 stays closed. The hit
  rate is still measured in the test environment once access exists (D13).

**`addShipment`** [KSA], [COMBO]:

- **What one shipment carries:**
  - eFaktura, AvtaleGiro or both, at most 1,000 transactions;
  - per transaction, `paymentType` is `EFAKTURA`, `EFAKTURA_AND_AVTALEGIRO`,
    `EFAKTURA_AND_EXTERNAL_AVTALEGIRO` or `AVTALEGIRO`.
- **Fields:**
  - on the shipment: `messageIdentifier` (5–50 characters), `shipmentId` (a UUID we choose)
    and **`issuer`** (the 9-digit organisation number);
  - on each transaction:
    - `transactionId` (a UUID we choose) and `documentType`, which accepts only `INVOICE`
      today (error 3100);
    - `kid` (2–25 characters, required for AvtaleGiro);
    - `accountNumber` (11 digits), `amount` (2 decimals) and `dueDate`;
  - `efakturaInfo`: one of `efakturaIdentifier`, `socialSecurityNumber` or
    `eFakturaReference`, plus `brandName` (up to 40), `eFakturaUrl` (up to 255) and
    optional `minimumAmount` and `legalOrgNumber`;
  - `atgInfo`: `atgBankNotification`, a notification text (42 lines of 80),
    `atgAbbreviatedName` (up to 10) and `atgExternalReference` (up to 25). [COMBO] says
    a combined transaction is notified by the creditor, not by the bank.
- **Where the invoice is:**
  - In [KSA] the submitter hosts it behind `eFakturaUrl`. The bank's link carries a
    signed token, which the submitter checks with `validateSecurityToken`.
  - [KMS] instead takes the PDF (base64) or an `invoiceSpecification`, and MPS hosts it.
  - No size limit is published (**UNSURE**).
- **Due dates** [COMBO]:
  - eFaktura: from today + 3 days;
  - AvtaleGiro: from today + 4 days;
  - both: at most 12 months ahead.
- **Status, by polling only.** No callback is documented.
  - The calls: `getShipmentStatus`, `getTransactionStatus`, `getShipments` (by a time
    interval) and `getTransaction`.
  - [BP] backs off 1, 5, 15 and 30 minutes, then 1 hour, and asks the submitter to call
    support after 24 hours.
  - eFaktura transaction states: `IN_PROGRESS`, `ACCEPTED`, `PROCESSED`, `REJECTED`,
    `STOPPED`, `DUPLICATE`, `NOT_FOUND`, `CANCELLED` and `NOT_AN_EFAKTURA_TRANSACTION`.
  - AvtaleGiro transaction states: `IN_PROGRESS`, `PENDING`, `PROCESSED`, `REJECTED`,
    the `CANCELLATION_*` states, `CANCELLED`, `NOT_AN_ATG_TRANSACTION` and
    `EXTERNAL_ATG`.
  - A transaction also carries a rejection code and text, and an `attention` flag set
    when the amount exceeds the mandate's limit.
  - When the AvtaleGiro half of a combined transaction is rejected, the eFaktura is held
    back [COMBO].
- **Cancelling:**
  - `cancelShipment` and `cancelTransaction` answer `CANCELLATION_REQUESTED`,
    `NOT_ALLOWED` or `PROCESSING_IN_PROGRESS-TRY_LATER`.
  - The deadline [DL] is **14:00 on the day before the due date**.
  - A claim notified by eFaktura must be received by **14:00 on a working day, 4
    calendar days before the due date**.
- **Errors:** a SOAP fault always comes back as HTTP 500, so the adapter reads the body.

**The Mandate API** [MANDATE] is REST under OpenAPI 3.0:

- **Base URLs:** `https://payments.mastercard.no/creditor-mandate/`, and
  `https://mtf.payments.mastercard.no` for test.
- **`GET /creditor-account`** is a delta queue, not a "since" query:
  - it answers the mandates created, changed or deleted since the last delivery, up to
    1,000 a page;
  - the caller keeps calling while `page.total_pages` lasts, and 204 means nothing new;
  - the same `X-Request-ID` replays the same page;
  - MPS asks for a call at least daily.
- **The headers:** `Creditor-Account-Number` (11 digits) and `X-Request-ID` (a UUID).
- **A mandate's fields:**
  - `mandate_reference`, which is the KID;
  - `mandate_status.code`: `ACTV`, `INAC` or `PEND`;
  - `debtor.contact_details.prefered_method`: `NONE` or `MAIL`.
- **No amount limit is returned.** The limit is the payer's, and the API only answers
  `GET /creditor-account/kid?valid-amount=…` with a `mandate_coverage_indicator`.
- **Retries:** time out after 20 s, then wait (n−1)³ + 30 s.

**Test environment and authentication:**

- **The eFaktura SOAP services:**
  - The test environment's host name is not published (**UNSURE**; the WSDLs name only
    `efaktura-services.nets.no`). The submitter user guide PDF may name it.
  - Access is asked of Support.Norway@mastercard.com. Test data is shared by every user,
    and a formal test comes before production [ONB].
  - Authentication is mTLS with a client certificate MPS issues from our CSR, plus a
    WS-Security UsernameToken for a user MPS creates.
  - Every source IP is allow-listed per environment: no private, very large or shared
    cloud ranges. There is no OAuth and no API key [ONB].
- **The Mandate API:**
  - mTLS with an organisation-validated eIDAS certificate (QWAC or QSealC), TLS 1.2 or
    later;
  - responses are signed (`rsa-pss-sha512`), and checking the signature is optional.
- **Who is who:**
  - the submitter is the certificate plus the WS-Security user;
  - the issuer is `issuer` on `addShipment` and `efakturaIssuer` on the lookup, both
    the organisation number;
  - the Mandate API names the creditor by `Creditor-Account-Number`.
  - Whether one submitter certificate may read many creditors' mandates is not
    published (**UNSURE**).

**The KID** [KID]:

- 4–25 digits, of the length agreed in the service agreement;
- at most 3 KID structures per agreement, each of a different length;
- the check digit is MOD10 or MOD11, where `-` stands for remainder 1;
- in a combined transaction, the customer id inside the KID should equal the eFaktura
  reference.

## D: readings against the code

Each reading says what the code or MPS says, against what the spec assumed, and the
resolution this plan builds.

1. **The delivery preference already has `efaktura`.**
   - *Finding:* I30 says "Delivery preference gains `efaktura`", but
     `srv/customers/billing_values.go:203-212` already accepts it, and
     `billing_profile.go:161` already warns `efaktura_for_business`.
   - *Resolution:* nothing is added. Invoices starts *reading* the value (D6).
2. **Transmissions have no `channel` column, and their columns are Peppol's.**
   - *Finding:* I31 says the row is kept with `channel = 'efaktura'`, but
     `invoices.transmissions` (`mig/00036:61-115`) has no channel. Eight of its columns
     are `NOT NULL` and Peppol-only: `sender_participant`, `receiver_participant`,
     `document_type`, `process_id`, `ubl_object_key`, `ubl_sha256` and the
     `lookup_*` pair.
   - *Resolution*, the spec's model kept:
     - D1 adds `channel varchar(10) NOT NULL DEFAULT 'ehf'` and drops `NOT NULL` from
       those columns, under a per-channel `CHECK`;
     - every EHF query filters `channel = 'ehf'` (D6 lists them);
     - `ux_transmissions_active` becomes `(invoice_id, channel)`.
   - The immutability trigger compares `to_jsonb(OLD) - state_columns`
     (`mig/00036:155-175`), so `channel` and the new identity columns are frozen
     without change. Only the new *state* columns need the function replaced.
3. **A delivered row without evidence is claimed forever.**
   - *Finding:* `ix_transmissions_due` and `ClaimTransmission`
     (`inv/queries/transmissions.sql:54-75`) claim any delivered row that has a
     `provider_ref` but no evidence.
   - *Resolution:* the eFaktura worker stores MPS's final `getTransactionStatus` answer
     as the evidence object, so the same rule ends the row. EHF's claim is filtered to
     `channel = 'ehf'` anyway.
4. **MPS has no event queue.**
   - *Finding:* Storecove pushes into a queue that `ehf_events_worker.go` drains. MPS
     can only be polled.
   - *Resolution:* the port has `Status` (I29), and `invoices-efaktura` polls on [BP]'s
     back-off. There is no events worker for eFaktura.
5. **The base URLs are the operator's, not the tenant's.**
   - *Finding:* §10 says the provider's base URL goes in its settings "as the access
     point's does". But the access point's URL is operator configuration
     (`INVOICES_STORECOVE_BASE_URL`, `srv/config/config.go:366-371,1189-1196`),
     precisely so that an `invoices:manage` holder "cannot aim the client anywhere".
   - *Resolution:* the MPS URLs are configuration (D3).
6. **The submitter's secrets are Vantigo's, not the tenant's.**
   - *Finding:* I29 makes Vantigo the submitter. Its client certificate, its key and
     its WS-Security user are therefore Vantigo's, issued once by MPS and IP-bound to
     the installation's egress.
   - *Resolution:*
     - They are operator secrets, read from files (D3), as
       `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` is (`config.go:1923-1939`).
     - The tenant row that I29 seals under `invoices:manage` holds what is the
       tenant's: the agreement states, the AvtaleGiro creditor account and, if MPS
       requires a per-creditor eIDAS certificate for the Mandate API (**UNSURE**), that
       certificate and key, sealed.
     - The test connection checks both halves.
7. **The issuer id is the organisation number.**
   - *Finding:* I29 says the settings record "the tenant's issuer id". MPS names the
     issuer by its organisation number [KSA].
   - *Resolution:* the issuer is `invoices.settings.organisation_number`, and no MPS id
     is stored.
8. **The mandate carries no limit.**
   - *Finding:* I32 stores `avtalegiro_mandates(…, limit, …)`, and I33 compares the
     amount with it. The Mandate API returns no limit, only a coverage check.
   - *Resolution:*
     - The table has no `limit` column.
     - The port gains **`Covers(ctx, kid, amount) (bool, error)`**, which is
       `GET /creditor-account/kid?valid-amount=`. This is a sixth method, filling the
       gap I29's list leaves.
     - The claim's "within the limit" is `Covers` at send. "Not covered" sends eFaktura
       alone, with the reason `avtalegiro_limit`.
     - A transaction that comes back with `attention` set is recorded on the claim.
9. **Who hosts the invoice.**
   - *Finding:* [KSA] has no PDF field. The submitter hosts the invoice behind
     `eFakturaUrl` and validates MPS's token. That would be a new public route.
   - *Resolution:* the adapter uses **[KMS]**, which takes the stored PDF, so nothing
     new is exposed.
   - **UNSURE:** whether [KMS] carries `EFAKTURA_AND_AVTALEGIRO`. If it does not, the
     fallback is [KSA] behind the existing public pay-link host (`PUBLIC_BASE_URL`,
     `fe/pages/pay.tsx`): a `GET /p/efaktura/{token}` that calls
     `validateSecurityToken`, then streams the stored PDF. D2 keeps the PDF behind the
     port either way, so only the adapter changes.
10. **The customer KID needs a customer number, and the directory reads by id only.**
    - *Finding:* `contracts.CustomerDirectory` (`srv/contracts/directory.go`) reads by
      id only, and a mandate arrives with only its KID.
    - *Resolution:* the sync parses the number out of the `9…` KID. It resolves the
      customer through Invoices' own snapshots, the newest issued document whose
      `buyer_customer_number` is that number (`mig/00034:129`), and follows
      `MergedInto`. No match leaves the mandate unresolved (I32).
    - The matching rule then reads the mandate row's `customer_id`, never the number.
      So a merge, which re-points the row, keeps working although the KID still names
      the old number.
11. **The payer has to learn the customer KID.**
    - *Finding:* the spec does not say how the customer KID reaches the payer, who needs
      it to create a mandate.
    - *Resolution:*
      - With AvtaleGiro on, a person's invoice PDF prints one line under the payment
        details: *AvtaleGiro: opprett avtale i nettbanken med KID {customer KID}*, in
        the document's language.
      - The eFaktura is sent with `eFakturaReference` set to the customer number, as
        [KID] asks.
      - A document without it renders byte for byte as before (`pinnedRenderSHA256`).
12. **The KID cap has to hold for invoices already issued.**
    - *Finding:* `kid.Compute` (`inv/kid/kid.go:76-89`) allows length − 1 digits.
    - *Resolution:*
      - Turning AvtaleGiro on is refused 409 `avtalegiro_kid_space_taken` when the next
        number has more than length − 2 digits (`kid.Fits` with a reserve,
        `inv/settings.go:475`).
      - It is refused the same way when any issued KID already begins with `9`
        (a query).
      - While AvtaleGiro is on, the issue uses `ComputeReserved`, and at the cap it
        refuses `kid_length_exceeded` as today (`inv/issue.go:532`).
      - This answers I32's "the plan checks every installation's series": the check is
        made at the switch, on that installation's own numbers.
13. **The reminder engine's first letter already waits at least a day.**
    - *Finding:* the first letter is due from `EffectiveDue(due) + first_reminder_days`
      (`inv/reminderrules/engine.go:259-264`). `EffectiveDue` is already the next
      business day (`months.go:38`), and `first_reminder_days ≥ 1`
      (`mig/00041:379`).
    - That can still land on a Saturday, before the debit's bank file arrives.
    - *Resolution:* with an accepted claim, the earliest day is
      `max(E + first_reminder_days, NextBusinessDay(E + 1 day))`. Before it, the
      outcome is `waiting`, with the new reason `avtalegiro_claim`.
14. **eFaktura counts as a delivery.**
    - *Finding:* `RuleDeliveries` (`inv/queries/ruleinput.sql:47-68`) counts delivered
      transmissions as kind `'ehf'`.
    - *Resolution:* it reads `t.channel` as the kind, so a delivered eFaktura is
      delivery fact D8 with no other change.
15. **Credit cannot call the provider inside the issue.**
    - *Finding:* I34 cancels the claim when a credit note is issued, but the issue runs
      in one transaction (`inv/issue.go:366`), and a provider call never runs in one.
    - *Resolution:*
      - The credit note's issue moves a live claim to `cancel_requested` in the same
        transaction.
      - `invoices-efaktura` calls `Cancel`.
      - Past the deadline, the claim stays `accepted` and its state says so. The payment
        then comes, and its surplus follows B's credit when B is in, or the exception
        queue when it is not.
16. **The lookup needs a phone and a date of birth that the profile does not carry.**
    - *Finding:* `CustomerBillingProfile` (`directory.go:89-152`) carries
      `InvoiceEmail` but no phone and no date of birth. The phone lives on
      `customers.customers` (`mig/00017`).
    - *Resolution:* the profile gains `Phone` and `DateOfBirth`, and D4 adds the date
      of birth to Customers.

## D1. Migrations

**`NNNNN_invoices_efaktura.sql`:**

- **`invoices.consumer_provider_credentials`**, one row, as
  `access_point_credentials` (`mig/00036:44-53`):
  - `provider` is `'mps'` (CHECK);
  - `settings_json` holds:
    - `efakturaAgreement` and `avtalegiroAgreement` (`none`, `pending` or `active`);
    - `creditorAccount` (11 digits);
    - `brandName` (up to 40 characters);
    - `abbreviatedName` (up to 10 characters);
  - `secret_ciphertext` is the optional mandate certificate and key, sealed, and may be
    `''`;
  - `rejected_at` and `updated_at`.
- **`invoices.consumer_lookups`**, keyed by `customer_id int PRIMARY KEY`:
  - `status`: `found`, `not_found`, `blocked` or `ambiguous` (CHECK);
  - `efaktura_identifier char(9)`, which is NULL unless `found`;
  - `auto_accept` and `has_agreement`, both boolean;
  - `checked_at` and `next_check_at`.
  - Sendable means `found AND (auto_accept OR has_agreement)`.
  - No name, phone or e-mail is copied: the lookup's input is read from the profile
    each time.
- **`invoices.avtalegiro_mandates`:**
  - `id`, `kid varchar(25) UNIQUE`;
  - `customer_id int` (NULL while unresolved);
  - `status`: `active`, `inactive` or `pending`, from `ACTV`, `INAC` and `PEND`;
  - `notify_by_mail bool`;
  - `provider_seen_at`, `resolved_by_user_id` and `updated_at`.
  - It has no `limit` (reading 8).
- **`invoices.avtalegiro_claims`:**
  - `id`, `invoice_id` (one live claim per invoice, by a partial unique index), the
    `transmission_id` FK and the `mandate_id` FK;
  - `amount numeric(14,2)`, `due_date` and `kid`;
  - `status`: `submitted`, `accepted`, `rejected`, `cancel_requested`, `cancelled` or
    `paid` (CHECK);
  - `attention bool`, `rejection_code` and `rejection_text` (redacted, 500 characters);
  - stamps for each state.
  - A trigger lets only the state columns change and refuses every DELETE, the
    transmissions' shape.
- **`invoices.transmissions`** (reading 2):
  - `channel varchar(10) NOT NULL DEFAULT 'ehf'`, with `CHECK (channel IN ('ehf',
    'efaktura'))`;
  - `ck_transmissions_provider` gains `'mps'`;
  - drop `NOT NULL` on the eight Peppol columns, and add `ck_transmissions_channel`:
    for `ehf` all eight are set, for `efaktura` all eight are NULL;
  - the identity columns `efaktura_identifier char(9)`, `shipment_id uuid`,
    `payment_type varchar(30)` and `pdf_object_key`, all NULL;
  - the state column `provider_status varchar(40)`;
  - `ux_transmissions_active` is re-created on `(invoice_id, channel)`;
  - `refuse_transmission_change` is replaced only to add `provider_status` to
    `state_columns` (`mig/00036:155-175`). The Down restores it as it was.
- **`invoices.settings`:** `avtalegiro_enabled boolean NOT NULL DEFAULT false`.
- Append the file to `inv/sqlc.yaml`. `srv/db/schema_test.go` checks it.

**`NNNNN+1_customers_date_of_birth.sql`:**

- `customers.customers.date_of_birth date`, NULL, with
  `CHECK (date_of_birth IS NULL OR type = 'person')`.

## D2. The port, the MPS adapter and the fake

- **`srv/consumerinvoice/consumerinvoice.go`.** A platform package like `srv/payments`
  (`payments.go:1-17`): values and interfaces only, no SQL, no module import.
  - The interface `Provider`:
    - `Lookup(ctx, []Consumer) ([]Address, error)`, batched to 1,000;
    - `Send(ctx, Shipment) (Ref, error)`;
    - `Status(ctx, Ref) ([]TransactionStatus, error)`;
    - `Cancel(ctx, Ref, transactionID) (CancelOutcome, error)`;
    - `Mandates(ctx, requestID uuid.UUID) (MandatePage, error)`, the delta queue;
    - `Covers(ctx, kid, Money) (bool, error)` (reading 8);
    - `Verify(ctx) error`.
  - `Consumer` is a person's lookup key: phone, e-mail, date of birth, name, address.
    It has **no fødselsnummer field**, under Q2.
  - `Address` holds the status, the identifier, the auto-accept flag, whether there is
    an agreement, and whether the issuer is blocked.
  - `Shipment` holds the issuer and the transactions. Each transaction has the
    `PaymentType` (`efaktura` or `both`), the KID, the account, the amount (the
    payments package's `Money` pattern), the due date, the PDF bytes and the brand
    name.
  - The states fold MPS's onto `submitted`, `delivered`, `failed` and `cancelled` for
    eFaktura, and onto `submitted`, `accepted`, `rejected` and `cancelled` for
    AvtaleGiro.
  - The errors: `ErrUnauthorized`, `ErrThrottled` and `ErrRefused{Code, Text}`. Any
    other error is retryable, the access point's convention (`accesspoint.go:110-150`).
  - The idempotency keys are the caller's: `shipmentId` and `transactionId` are
    persisted before the call and reused.
- **`srv/consumerinvoice/mps/`:**
  - The SOAP client for [KMS]: `getEfakturaIdentifiers`, `addShipment`,
    `getTransactionStatus` and `cancelTransaction`.
    - A WS-Security UsernameToken.
    - A fault is read from the HTTP 500 body.
    - The transport is mTLS: `Deps.HTTPTransport`, cloned with the client certificate.
  - The REST client for [MANDATE].
  - The status-code tables, and the redaction of rejection texts (`redactReason`'s rule,
    `inv/sendehf.go:95`).
- **`srv/consumerinvoice/mps/mpstest/`.** An httptest fake for tests only, as
  `accesspoint/storecovetest/server.go:1-14` is. It has a TLS listener that *requires* a
  client certificate. It serves:
  - the lookup by the matching rule above;
  - shipments, with states that advance by the test's clock;
  - cancellation with the 14:00 deadline;
  - the mandate queue with paging and replay;
  - coverage.
  - Failures are injected with `FailOn(op, n)`, never `FailNext` (spec §12, the memory
    *Google fake FailNext vs token cache*).
- **`purity_test.go`** for the port and the adapter, after `payments/purity_test.go`.
- **The adapter's tests** run against `mpstest`. An opt-in test against MPS's test
  environment is gated by an environment variable, as `storecove_sandbox_test.go` is.
- **depguard:** add `consumerinvoice` to every set in `apps/server/.golangci.yml` that
  names `payments`.

## D3. Configuration and credentials

- **`srv/config/config.go`**, a new `invoicesEfaktura`, after `invoicesEhf`
  (`:1189`):
  - `INVOICES_EFAKTURA_ENABLED`, **default off**. This is the live switch. Off, the
    send answers 503 `efaktura_unavailable` and `invoices-efaktura` is never handed to
    the runner.
  - `INVOICES_MPS_EFAKTURA_BASE_URL`, default the production service URL from the
    [KMS] WSDL, and `INVOICES_MPS_MANDATE_BASE_URL`, default
    `https://payments.mastercard.no/creditor-mandate`. Both use `httpsBaseURL`.
  - `INVOICES_MPS_CLIENT_CERT_FILE`, `INVOICES_MPS_CLIENT_KEY_FILE`,
    `INVOICES_MPS_USERNAME` and `INVOICES_MPS_PASSWORD_FILE`:
    - absolute, readable paths (`:1923-1939`);
    - required when the switch is on;
    - in `redact_test.go`'s secret patterns.
- **Tenant settings, in `inv/consumer_provider.go`**, mirroring `inv/credentials.go`
  (`:151-322`):
  - `GET`, `PUT` and `DELETE /api/v1/invoices/settings/consumer-provider` and `POST
    …/verify`, all under `invoices:manage`;
  - the secrets-box purpose `invoices/consumer-provider-credential`;
  - the kept-key-under-`FOR UPDATE` rule (`:177-181`);
  - DELETE answers 409 `transmissions_active` while an eFaktura transmission or claim
    is in flight;
  - `verify` calls `Verify`: a lookup of the issuer's own blacklist entry, and, with
    AvtaleGiro on, one `Mandates` page under a fresh request id.
- **`PUT /api/v1/invoices/settings/avtalegiro`** `{enabled}` (`invoices:manage`):
  - it needs a KID agreement (409 `kid_agreement_missing`) and an active AvtaleGiro
    agreement in the credentials;
  - it refuses `avtalegiro_kid_space_taken` (reading 12).
- **`inv/meta.go`** (`ehfAvailable`, `:156`) gains `efakturaAvailable`, answered in
  the meta capabilities.
- **`inv/contractscalls.go`** gains `consumerProvider(ctx)`, beside `accessPoint`
  (`:365-380`).

## D4. Customers: the date of birth

- **`srv/customers/contact_info.go`** (`:82`, `:99`): `dateOfBirth` on a person's
  contact info, validated as a past date after 1900. A business answers 400
  `date_of_birth_person_only`.
- **The directory:**
  - `DirectoryBillingProfile` (`srv/customers/queries/customers.sql:620`) and
    `resolveBillingProfile` (`directory.go:295`) answer it;
  - `contracts.CustomerBillingProfile` gains `Phone string` and
    `DateOfBirth *time.Time` (reading 16).
- **Anonymisation:** `AnonymiseCustomerRow`
  (`srv/customers/queries/personal_data.sql:242-248`) sets `date_of_birth = NULL`.
  The customer's own export includes it.
- **`openapi/customers.yaml`**, and the contact-info card in `cfe/`: a **Date of birth**
  field for a person. Its help text: *Used to find the customer for eFaktura. Never
  printed.*

## D5. Finding the consumer

- **`inv/consumer_lookup.go`:**
  - `refreshLookup(ctx, customerIDs)` reads the profiles and sends the keys that satisfy
    MPS's matching rule. A person without them is `not_found`, and no call is made.
  - It upserts `consumer_lookups` with `next_check_at = checked_at + 7 days`.
  - Only `type = person` is looked up.
- **`POST /api/v1/invoices/customers/{id}/efaktura-lookup`** (`invoices:issue`) runs a
  lookup now. `GET` (`invoices:access`) answers the stored row.
- **The customer slots** (`inv/customer_slots.go`):
  - `RepointCustomer` (`:78-105`):
    - deletes the from-customer's lookup and marks the into-customer due now;
    - re-points mandates;
    - answers the new kinds `invoices.consumerLookups` and
      `invoices.avtalegiroMandates`, added to `mergeKindNouns`
      (`srv/customers/timeline_events.go:658`).
  - `EraseCustomerData` (`:956`):
    - deletes the lookup;
    - cancels claims not yet submitted;
    - keeps mandates and claims, which hold amounts and dates and no personal data;
    - includes them in the export.

## D6. Send as eFaktura, and the claim

**The EHF queries** gain `channel = 'ehf'` (`inv/queries/transmissions.sql`):

- `ClaimTransmission` (`:54`), `ActiveTransmissionExists` (`:46`),
  `ActiveTransmissionsCount` (`:242`) and `LatestPersonResolvedFailedTransmission`
  (`:35`);
- the event appliers (`:171`, `:186`), `AnyAwaitingEvents` (`:196`) and
  `QueuedTransmissionsDue` (`:215`);
- `TransmissionsOfDocuments` (`:258`), for the list's `ehfStatus`. The list gains
  `efakturaStatus` from the same query by channel.
- `CancelCustomerUnattemptedTransmissions` (`:248`) stays channel-blind.

**`POST /api/v1/invoices/{id}/send-efaktura`** (`invoices:issue`), in
`inv/sendefaktura.go`, follows `PostInvoicesByIdSendEhf` (`inv/sendehf.go:340-548`) step
for step:

1. 503 `efaktura_unavailable` when the provider is not configured or the eFaktura
   agreement is not active.
2. The document must be issued and its customer not anonymised.
   - A credit note answers 409 `efaktura_credit_note`. Its title says it goes by e-mail
     or post (I31).
3. 409 `currency_not_nok`, and `kid_missing` when the document has no KID.
4. Due dates are judged on the business day in Europe/Oslo:
   - 409 `efaktura_due_too_soon` when the due date is before today + 3;
   - 409 `efaktura_due_too_far` when it is more than 12 months ahead.
5. The lookup is refreshed if it is older than 7 days. The call is made outside any
   transaction. If the customer cannot be sent to, the answer is 409
   `efaktura_not_found`, with `reason` (`not_found`, `blocked`, `no_agreement` or
   `ambiguous`).
6. 409 `efaktura_already_sent` while a live transmission exists on the channel.
7. **The claim (I33)** goes when AvtaleGiro is on and the customer has an `active`
   mandate. The shipment is `both` if:
   - the due date is at least the 4th calendar day after the next working day that is
     still before 14:00 Oslo time [DL];
   - `Covers(kid, amount)` is true, called outside the transaction.

   Otherwise the shipment is `efaktura`, and the answer's `avtalegiro` block says why:
   `avtalegiro_due_too_soon`, `avtalegiro_limit` or `avtalegiro_no_mandate`.
8. Under the document's `FOR UPDATE` and the credentials' `FOR SHARE`:
   - insert the transmission (`channel = 'efaktura'`, provider `mps`, the
     `shipment_id`, the stored PDF's key and hash);
   - insert the claim row as `submitted` when there is one;
   - publish `invoices.efaktura.transmission_changed`.
9. Answer 202 with the document's `efaktura` block.

**`inv/send.go` `sendWarnings`** (`:113-136`): with the preference `efaktura` and a
sendable lookup, warn `efaktura_preferred` in place of `delivery_preference_other`, as
`ehf_preferred` does.

**The `invoices-efaktura` worker** (`inv/efaktura_worker.go`):

- It is a lease worker shaped as `ehf_worker.go`:
  - the claim is a conditional UPDATE over a `SKIP LOCKED` pick;
  - the crash marker is `submit_attempted_at`;
  - every completion is checked against the lease (`:242-340`);
  - every time comes from `Deps.Clock()`.
- **The phases** of one cycle:
  1. **Submit** a queued row: `Send` with the stored `shipmentId` and `transactionId`.
     A `DUPLICATE` answer on retry is success.
  2. **Poll** a submitted row: `Status`, on [BP]'s back-off. The states:
     - eFaktura `ACCEPTED` or `PROCESSED` → `delivered`, with the answer stored as
       evidence (reading 3);
     - `REJECTED`, `STOPPED` or `NOT_FOUND` → `failed`;
     - the AvtaleGiro half moves the claim: `accepted`, or `rejected` with its code and
       `attention`;
     - after 7 days with no outcome → `unconfirmed`, as EHF's `ehfSubmittedLimit` is.
  3. **Cancel** a `cancel_requested` claim through `Cancel` until 14:00 on the day
     before the due date:
     - `CANCELLATION_REQUESTED` → `cancelled` on the next poll;
     - `NOT_ALLOWED` or past the deadline → back to `accepted`, with
       `cancel_refused_at`;
     - `TRY_LATER` → rescheduled.
  4. **Mandates**, once a day under the module's advisory lock: drain `Mandates`
     (reading 10) and upsert the rows.
  5. **Lookups:** the customers whose `next_check_at` has passed, up to 1,000 a call.
- `inv/module.go` `workers` (`:186-199`) adds it when `InvoicesEfakturaEnabled` is set.
  It gets a pool-of-one test (spec §10).
- **Events** (spec §7), as `contracts/events_invoices.go` structs published with
  `events.Publish` (as `inv/issue.go:652` does):
  - `invoices.efaktura.transmission_changed`;
  - `invoices.avtalegiro.claim_changed`.
  - Their payloads are ids, states, amounts and dates. They carry no identifier and no
    name.

## D7. AvtaleGiro: the KID space, matching, reminders and credit

- **`inv/kid/kid.go`:**
  - `CustomerKID(customerNumber, length, algorithm)` is `"9"`, then the number padded
    to length − 2, then the check digit;
  - `ComputeReserved` caps invoice numbers at length − 2 digits;
  - `IsCustomerKID(s)`.
  - Unit tests: customer 1234 and invoice 1234 never share a KID, in either algorithm
    or any length from 4 to 25.
- **The issue** (`inv/issue.go:520-540`) uses `ComputeReserved` when
  `avtalegiro_enabled` is set. The settings warning `kid_headroom_low`
  (`inv/settings.go:483-490`) counts the reserved digit.
- **The PDF line**, under reading 11:
  - it is in `inv/pdf.go` and in the snapshot as `buyer_avtalegiro_kid` (frozen on
    issue);
  - a person gets it only while AvtaleGiro is on.
  - Pin a second render hash for it. The existing `pinnedRenderSHA256` stays.
- **Matching** (`inv/matching.go`), in `classify` (`:276-320`), before step 5
  (`invoiceOfKid`, `:328-354`):
  - A KID for which `IsCustomerKID` holds goes to the new `mandateClaimOf`. It finds the
    mandate by KID, then that customer's claim in `accepted` with
    `due_date = line.booked_on` (or the business day before, for a Monday debit of a
    weekend due date) and `amount = line.amount`.
  - A fit answers the claim's invoice. `registerMatch` then moves the claim to `paid`
    in the same transaction.
  - No fit queues `kid_unknown`, with the mandate's customer as the suggestion. It never
    reaches `invoiceOfKid`.
  - Tests: the collision case of I32, a claim that fits, an amount that differs, two
    claims due the same day.
- **Reminders:**
  - `reminderrules.Input` gains `AvtaleGiroDue *time.Time`, the accepted claim's due
    date. `Next` (`engine.go:241`) applies reading 13 with the reason
    `avtalegiro_claim`.
  - The rule-input loader (`inv/queries/ruleinput.sql`) reads the claim.
  - `RuleDeliveries` reads `t.channel` (reading 14).
  - Engine tests: a due date on a Friday, and one before a holiday.
- **Credit:**
  - issuing a credit note on an invoice with a live claim sets `cancel_requested`
    inside `issueLocked` (`inv/issue.go:389`);
  - a claim already `paid` is left as it is (reading 15).

## D8. OpenAPI and generation

- **`openapi/invoices.yaml`:**
  - the consumer-provider settings, verify, and the AvtaleGiro switch;
  - `send-efaktura` and its codes;
  - the document's `efaktura` and `avtalegiro` blocks, and the list's
    `efakturaStatus`;
  - the lookup;
  - `GET /api/v1/invoices/avtalegiro/mandates` (`invoices:access`) and `POST
    …/mandates/{id}/resolve` (`invoices:payments`), which assigns an unresolved
    mandate to a customer;
  - the claims on the document.
- **`openapi/customers.yaml`:** `dateOfBirth`.
- Run `go generate ./...` in `apps/server` and `bun run gen:client` in both
  frontends, and commit the `api-schema.d.ts` files.

## D9. Frontend

- **`fe/pages/-send-efaktura-dialog.tsx`**, after `-send-ehf-dialog.tsx`:
  - the lookup's state;
  - whether AvtaleGiro goes too, or why not;
  - the refusals by code through `lib/errors.ts`;
  - for a credit note, the text *Credit notes cannot be sent as eFaktura. Send it by
    e-mail or post.*
- **`fe/pages/invoice.tsx`:** **Send as eFaktura** beside **Send as EHF**. The
  **E-invoice** card (`fe/components/ehf-card.tsx`) shows the eFaktura transmission and
  the claim's state.
- **`fe/pages/settings.tsx`:**
  - an **eFaktura and AvtaleGiro** section with the provider settings, **Verify** and
    the AvtaleGiro switch;
  - the count sent per month (I35).
- **A mandates page** in the payments area: the unresolved ones, with **Assign to
  customer**.
- **The customer panel** (`fe/components/customer-invoices-panel.tsx`) shows the eFaktura
  status and **Check eFaktura**.
- **i18n** in `en` and `nb`. `bun run translations:check` passes.
- **Tests:**
  - the dialog, both ways;
  - the refusals;
  - the settings section;
  - assigning a mandate.

## D10. Seed data

`apps/server/cmd/vantigo/seed.go`, in `seedInvoicesReceivables` (`:1536`):

- provider credentials with both agreements active;
- `avtalegiro_enabled` on;
- a person customer with a phone and a date of birth, whose lookup is `found` and
  auto-accepted;
- an active mandate on that customer's KID;
- one issued invoice sent as `both`: a delivered transmission, an `accepted` claim and
  evidence stored;
- one unresolved mandate.

All of it is written as rows, and no provider is called (spec §12).

## D11. Documentation (spec §10)

- **`docs/src/content/docs/{en,nb}/user/invoices.md`:**
  - a new *Sending as eFaktura* section after *Sending as EHF* (`:1777`): who can get
    it, the due-date window, the lookup, AvtaleGiro and its claim, credit notes, the
    states;
  - *Set up e-invoicing* (`:105`): eFaktura and AvtaleGiro;
  - *Agree a KID with the bank* (`:169`): the AvtaleGiro KID and its reserved digit;
  - *Reminders*: an AvtaleGiro claim waits a business day after the due date.
- **`{en,nb}/admin/e-invoicing.md`**, a new part *eFaktura and AvtaleGiro through
  Mastercard Payment Services*:
  - the submitter agreement and the operator's files;
  - the egress IP to allow-list;
  - the tenant's agreements with its bank;
  - storing and verifying the settings;
  - turning AvtaleGiro on and its KID check;
  - the worker and its phases;
  - testing against MPS's test environment;
  - **the live switch: `INVOICES_EFAKTURA_ENABLED` and the two base URLs**;
  - known limits.
  - `sources` gains `srv/consumerinvoice`.
- **`{en,nb}/admin/configuration.md`:** the six new variables.
- **`en/reference/invoices.md`:**
  - the model: the three tables, the transmission's `channel`, the settings flag;
  - *Sending as eFaktura* beside *Sending as EHF* (`:3023`): the steps and codes, the
    claim, the worker (*Workers*, `:3144`);
  - *Matching* (`:3711`): the mandate rule first;
  - *The delivery fact* (`:4145`) and the reminder worker: readings 13 and 14;
  - *The access point's credentials* (`:371`): the consumer provider beside them;
  - *Endpoints*;
  - *Events* (`:6347`): the two new rows;
  - *What comes next*: phase 6 D done.
  - `sources` gains the migration.
- **`en/reference/contributing` › platform packages:** `consumerinvoice` beside
  `payments`.
- **`{en,nb}/user/customers.md` and `en/reference/customers.md`:** the date of birth,
  its use and its erasure, and the eFaktura status shown.
- **`ROADMAP.md`:** phase 6 D done.
- Run `go generate ./...` after the docs (the memory *docs edit needs go generate*).
- **The public site** (`../vantigo-landing`, `web/src/apps/invoices.ts`), in en and
  nb, written from the user guide:
  - *Send invoices to consumers' online bank as eFaktura, and collect them by
    AvtaleGiro.*
  - Open it as a PR there, linked from this one. If that repository is not checked
    out, say so in the PR.

## D12. Assistant

- `fe/assist-targets.json`: `invoices.send.efaktura`, `invoices.settings.efaktura` and
  `invoices.avtalegiro.mandates`, with `en` and `nb` labels.
- Bump `promptVersion` in `srv/assistant/prompt.go:15`.
- Add the new operations to the integration test's operation registry if they belong
  there. Uploads and downloads do not (the commit `807b01b06`).

## D13. The gates

- `mise run server:check` and `mise run server:test`. If the shared database runs out
  of clients, run invoices, customers and consumerinvoice with `-p 1`.
- `mise run frontend:check`. Read its log, since its wrapper exits 0 anyway.
- `mise run docs:check`.
- When MPS's test access exists (it is not needed to merge):
  - run the gated adapter test;
  - record the lookup's hit rate without a fødselsnummer over the shared test data, in
    the PR and in `open-questions` for Q2.
- Commits follow Conventional Commits (`feat(invoices): …`, `feat(customers): …`) and
  are SSH-signed.

## D14. Review

An independent review of the diff before the PR leaves draft. It checks in particular:

- the transmissions migration: the channel `CHECK`, the replaced trigger, and every
  EHF query filtered;
- that no customer KID can equal an invoice KID, and the switch's check over issued
  KIDs;
- the mandate rule running before `invoiceOfKid`, and the claim moving to `paid` in the
  match's transaction;
- no provider call inside a transaction: the send, the credit note and the worker;
- the secrets: the operator's files never logged, and the tenant's sealed row never
  answered;
- no fødselsnummer anywhere: in the port, in the adapter's request or in logs;
- the reminder wait, and the PDF byte-identical without AvtaleGiro.
