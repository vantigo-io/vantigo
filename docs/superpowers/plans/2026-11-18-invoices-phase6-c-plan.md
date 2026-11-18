# Invoices phase 6 — delivery C, foreign currency: implementation plan

This plan builds delivery C of Invoices phase 6. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-invoices-phase6-design.md`, revision
  5: §4 I17–I28, §7, §8, §9's C row, §10, §11, §12, and §15. The amended I21 and I24
  rules hold: the NOK receivable is defined, and the closing movement takes the NOK
  rest.
- **The plan for A and B:** `docs/superpowers/plans/2026-11-17-invoices-phase6-plan.md`.
  Its *How it lands* and its gates apply here unchanged.

Code references are against `claude/invoices-credit` at `fbbbc1b22`, which is B-1
(PR #374) about to merge. B-1 supplies what C builds on:

- `amount_nok` on payments, write-offs, credit rows and refunds, which every insert
  fills through `amountNOKOf` and `nokOfInvoice` (`inv/credit.go:199-224`);
- `guard_payment_amount_nok`, which refuses a non-NOK row without `amount_nok`
  (`mig/00197:330-345`);
- the bank line's arithmetic in NOK (`LiveAppliedTo`, `unappliedOf`);
- the customer's credit, kept per currency.

*C: readings against the code* resolves every place where the spec and the code
disagree, or where the spec leaves a gap. **The readings win.**

**Abbreviations** are those of the A/B plan:

- `srv/` is `apps/server/internal/`;
- `inv/` is `srv/invoices/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/invoices/frontend/src/`.

## How C lands

- **Branch and PR:** `claude/invoices-currency`, one PR against `main`, cut once B-1 has
  merged. C does not need B-2.
- **Migration:** the next free number at the time the PR opens. The numbers taken so
  far are 00194–00196 (api-access), 00197–00198 (B-1), 00199 (B-2) and 00200–00201
  (D). C is therefore `00202_invoices_currency.sql` if those land first. The number is
  checked against `main` again right before merging (see the memory *migration number
  collisions*).
- **One migration file.** It holds the Invoices half and the Accounting half, as
  `00197` did.
- **Commits**, in this order, each one passing the gates:
  1. `feat(invoices): exchange rates from Norges Bank` (C1–C3)
  2. `feat(invoices): documents in currency` (C4–C7)
  3. `feat(invoices): payments in currency and the exchange difference` (C8)
  4. `feat(accounting): posting sales in currency` (C9)
  5. `feat(invoices-ui): …` (C11)
  6. `docs: …` (C13)

## The legal source for VAT in NOK (spec §14 Q6, closed)

**Bokføringsforskriften § 5-1-1 first ledd nr. 6** (FOR-2004-12-01-1558, chapter 5,
<https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558/KAPITTEL_5>) says:

> "Merverdiavgift skal angis i norske kroner."

The other provisions:

- **Nr. 5** (vederlag og betalingsforfall) does not require NOK. The price and the
  amount to pay may stay in the currency.
- **Bokføringsforskriften § 4-2** (bookkeeping in a foreign currency) locks the amounts
  for the VAT return in NOK at the rate of the invoice date. That is why the rate is
  fixed at issue (I19).
- **Merverdiavgiftsloven § 4-11** concerns imports (the customs value), not currency.
  The act's currency rule is § 4-12 (2), which covers services bought from abroad. The
  spec's alternative is therefore not the source.
- **Skatteetaten's guidance:** the principle statement *Salgsdokumentets innhold –
  merverdiavgift* (24 September 2006,
  <https://www.skatteetaten.no/nn/rettskjelder/type/uttalelser/prinsipputtalelser/salgsdokumentets-innhold--merverdiavgift/>)
  says the rate alone cannot replace the VAT stated in NOK. It also recommends the base
  in NOK as well.

What the PDF therefore prints (I26):

- the VAT in NOK, per rate and in total — required;
- the base in NOK per rate — recommended;
- the rate and its date.

The code already cites nr. 6 for the currency label (`inv/pdf.go:452-455`). The
reference's "NOK only (§ 5-1-1 nr. 6)" bullet (`en/reference/invoices.md:98-100`) is
rewritten from it.

## Norges Bank's API, as called on 2026-11-18

**The request:**

- `GET {INVOICES_RATES_URL}/EXR/B.{CUR1+CUR2+…}.NOK.SP?format=sdmx-json&startPeriod=YYYY-MM-DD&endPeriod=YYYY-MM-DD&locale=en`.
- The default base is `https://data.norges-bank.no/api/data`.
- The key's order is FREQ.BASE_CUR.QUOTE_CUR.TENOR. `B` is business days and `SP` is
  spot.
- Several currencies may be joined with `+` in one request.

**The response:**

- It is `{meta, data}`.
- The observations are at `data.dataSets[0].series["s:0:0:0"].observations["i"][0]`,
  and each value is a **string** (`"10.5755"`).
- The dates are at `data.structure.dimensions.observation[0].values[i].id`
  (`"2026-11-17"`). Only business days appear.
- **Trap 1:** the series key's first index points into
  `data.structure.dimensions.series[1].values` (BASE_CUR). That list is **not** in
  request order: `EUR+SEK+JPY` came back as JPY, EUR, SEK. The currency is always
  resolved through that list.
- The series attributes are an array of value indices, in the order of
  `data.structure.attributes.series`: DECIMALS, CALCULATED, UNIT_MULT, COLLECTION.
- **Trap 2:** UNIT_MULT's `values` list was `[{"id":"2"},{"id":"0"}]`. The index is
  resolved to its `id`, which is the power of ten, and the index itself is never used.
  - JPY, SEK and DKK are quoted per 100.
  - EUR and USD are quoted per 1.
  - `rate = value / 10^UNIT_MULT`.
- `detail=dataonly` strips the attributes, so UNIT_MULT is lost. **Never send it.**
- **No observations** (a weekend-only range, or an unknown currency) answers **HTTP 404**
  with `{"errors":[{"code":404,"message":"No data for data query …"}]}`. This is not an
  empty 200, and it is read as "no rate", not as a failure.

**Publication** is "approximately 16:00 CET", as middle rates, indicative only
(<https://www.norges-bank.no/en/topics/Statistics/exchange_rates/>). COLLECTION `C` is
the ECB concertation at 14:15 CET.

---

## C1. Migration `00202_invoices_currency.sql` (number: next free)

- **`invoices.exchange_rates`** (I18):
  - Columns: `currency char(3)`, `rate_date date`, `rate numeric(14,6) NOT NULL CHECK
    (rate > 0)`, `source varchar(12) NOT NULL CHECK (source IN ('norges_bank',
    'manual'))`, `fetched_at timestamptz NOT NULL`, `entered_by_user_id uuid` (`ck_exchange_rates_actor`: NOT NULL
    exactly when the source is `manual`), and `PRIMARY KEY (currency, rate_date)`.
  - `ck_exchange_rates_currency CHECK (currency ~ '^[A-Z]{3}$' AND currency <> 'NOK')`.
  - `refuse_exchange_rate_overwrite`, BEFORE UPDATE: a `manual` row is never changed by a
    `norges_bank` write (I18). The fetch's upsert is `ON CONFLICT DO UPDATE … WHERE
    exchange_rates.source = 'norges_bank'`, and the trigger backs it up in SQL.
  - Rows are deleted only when they are `manual` (C3's DELETE). A trigger refuses the
    rest.
- **`invoices.rate_fetches`**, one row per enabled currency. It carries the worker's
  lease and the settings page's *last fetch*:
  - `currency char(3) PRIMARY KEY`, `lease_id varchar(32)`, `lease_until timestamptz`,
    `last_attempt_at`, `last_success_at`, `last_error varchar(200)`;
  - `ck_rate_fetches_lease`: both lease columns or neither, the bank-feed account's
    shape.
- **`invoices.settings.currencies char(3)[] NOT NULL DEFAULT '{}'`**, with
  `ck_settings_currencies`: no NOK, and every element `^[A-Z]{3}$`.
  `ck_settings_default_currency` stays NOK (I17).
- **`invoices.invoices.exchange_rate_source varchar(12) NOT NULL DEFAULT 'nok'`**:
  - `ck_invoices_exchange_rate_source`:
    - `(currency = 'NOK') = (exchange_rate_source = 'nok')`;
    - `currency <> 'NOK' OR exchange_rate = 1`;
    - `exchange_rate_source IN ('nok', 'norges_bank', 'manual')`.
  - `ADD COLUMN … DEFAULT` rewrites no row through a trigger, so
    `refuse_issued_document_change` (`mig/00034:254-281`) needs no edit. Its jsonb
    comparison covers the column from now on.
- **NOK snapshots on the document's children** (C: readings 2). They are written at
  issue while the document is still a draft, so `refuse_issued_child_change`
  (`mig/00034:298-320`) needs no change:
  - `invoices.vat_summaries.taxable_amount_nok numeric(14,2)`, nullable;
  - `invoices.lines.line_net_nok numeric(14,2)`, nullable.
  - NULL means "a document issued before C". Every such document is NOK, so the reader
    takes `taxable_amount` and `line_net`. The `00180` precedent for pre-existing
    snapshots applies.
- **`invoices.payments.exchange_difference_nok numeric(14,2) NOT NULL DEFAULT 0`** (I24):
  - It is `amount_nok` minus the NOK receivable the payment cleared.
  - `ck_payments_exchange_difference`: `currency <> 'NOK' OR exchange_difference_nok =
    0`.
  - The default rewrites nothing, and `refuse_payment_change`'s jsonb comparison covers
    the column.
- **`invoices.credit_refunds`** (C: readings 9):
  - B-1 has no currency constraint here: the NOK-only rule is Go's
    (`inv/credit.go:635`).
  - `exchange_difference_nok numeric(14,2) NOT NULL DEFAULT 0` is added, under
    `ck_credit_refunds_exchange_difference` (`currency <> 'NOK' OR … = 0`).
- **`invoices.document_nok(bigint)`**, STABLE. It returns `(base_nok, vat_nok,
  gross_nok)`:
  - `base_nok` is the sum of `COALESCE(taxable_amount_nok, taxable_amount)`;
  - `vat_nok` is the sum of `vat_amount_nok`;
  - `gross_nok` is their sum, the I21 receivable.
  - Stats (C10) and the journal's `JournalOpenAt` read it.
- **`invoices.open_nok(bigint)`**, STABLE. It is the NOK receivable still open:
  - the document's `gross_nok`;
  - minus its credit notes' `gross_nok`;
  - minus the live payments' `amount_nok − exchange_difference_nok`;
  - minus the standing write-offs' `amount_nok`;
  - plus `moved`'s `amount_nok`.

  For NOK it equals `settled_amount()`'s open amount, and a test asserts that on every
  seed invoice.
- **The queue:** `ck_bank_transactions_reason` (latest in `mig/00182:229-233`) and
  `ck_bank_transaction_events_reason` (`mig/00041:227-229`, and `00182`'s replacement
  if it has one) both gain `currency_differs`. Each constraint is dropped and re-added
  with the full list.
- **The event** `invoices.exchange_rate.fetched` (§7): `publish_exchange_rate_fetched()`,
  AFTER INSERT OR UPDATE on `exchange_rates`, with `currency`, `day` and `source`. It is
  added to the module's event table and schema.
- **The Accounting half** writes no table. `accounting.postings` already has
  `currency`, `currency_amount` and `exchange_rate` (`mig/00181:244-246`), with
  `ck_postings_currency` (`mig/00185:66-67`).
- **The Down** refuses while a document, payment or credit row is not in NOK, or while
  `exchange_rates` holds a row, in `mig/00197`'s Down shape. It then drops everything in
  reverse.
- Append the file to `inv/sqlc.yaml`. `srv/db/schema_test.go` checks the list.

## C2. Server: the rates, the fetcher and the worker

- **New `inv/exchange_rates.go`:**
  - `nbCurrencies`, the currencies Norges Bank publishes, as a sorted constant list
    checked by a test against a recorded response. Settings refuse any other code.
  - `resolveRate(ctx, q, currency, day) (rate, rateDay, source, ok)`: the latest row with
    `rate_date <= day AND rate_date >= day − 7`. A `manual` row wins over a `norges_bank`
    row of the same day. The primary key holds one row per day; see C: readings 4.
  - Queries (`inv/queries/exchange_rates.sql`; **not** `rates.sql`, which holds the
    reminder rates):
    - `RateOn`;
    - `RatesBetween` (a page for the settings);
    - `UpsertFetchedRate`, whose conflict clause skips `manual` rows;
    - `PutManualRate`;
    - `DeleteManualRate`;
    - `ClaimRateFetch` and `ReleaseRateFetch`, with the lease;
    - `EnsureRateFetch`;
    - `RateFetchStatus`.
- **New `inv/norgesbank.go`, the client:**
  - It dials through **netguard**, exactly as Energy's spot-price feed does. The
    pattern to copy is `srv/energy/spot_prices_feed.go:108-142`: a transport built from
    `netguard.DialContext`, plus `spotFeedClient`, which follows no redirects and sets
    a timeout.
  - The body is read with a cap of 1 MiB.
  - `fetchRates(ctx, client, base, currencies, from, to) (map[cur][]dayRate, error)`
    parses the response by the API shape above:
    - the currency comes through `dimensions.series[1].values` (trap 1);
    - `UNIT_MULT` comes through the `values[idx].id` of the attribute named UNIT_MULT,
      found by name in `attributes.series` (trap 2);
    - the value is a string, parsed exactly with `big.Rat.SetString`, then divided by
      `10^mult` and rounded to six decimals (`numeric(14,6)`).
  - A 404 whose body carries `errors[0].code == 404` reads as no rows. Any other status
    is a failure that names the status and never quotes the body.
  - The outcomes, in `spotFeedTransportFailure`'s shape: `disallowed_address`,
    `unreachable`, `answered_<status>`, `unreadable`, `too_large`.
- **Config** (`srv/config/config.go`):
  - `InvoicesRatesURL`, read from `INVOICES_RATES_URL` with
    `httpsBaseURL(p, env, "INVOICES_RATES_URL", "https://data.norges-bank.no/api/data")`,
    beside `INVOICES_FOLIO_BASE_URL` (`:1220`). It must be an absolute https URL with no
    trailing slash.
  - `InvoicesRatesEnabled`, read from `INVOICES_RATES_ENABLED` (default on, as the bank
    feed is, `:1219`).
  - Both fields are documented in the struct (`:386-400`).
  - The config test gets a case for a bad URL.
- **The worker `invoices-rates`** (new `inv/rates_worker.go`). It is a lease worker in
  `bank_feed_worker.go`'s shape (`:22-52`, `:130-182`), with no hold-and-wait:
  - **Every 15 minutes** it computes the Oslo business day through `businessZone` and
    `holidays`. Before 16:30 Oslo time on a business day it does nothing for that day.
  - **The claim:** for each enabled currency, it claims that currency's `rate_fetches`
    row with a 5-minute lease (`ClaimRateFetch … FOR UPDATE SKIP LOCKED`) in a short
    transaction, then commits.
  - **The fetch:** with no transaction open, it fetches from the last `rate_date` (else
    today − 30) to today. All claimed currencies go in one request.
  - **The write:** a second short transaction upserts the rows and releases the lease
    with the outcome (`last_success_at` or `last_error`).
  - **It holds no connection across the HTTP call.**
  - It is registered in `workers()` (`inv/module.go:190-203`) only when
    `InvoicesRatesEnabled` is set. The comment block above it (`:170-189`) gains its
    sentence.
  - `rateFetchLease` is a `var`, so a test can shorten it, as `bankFeedWorkerLease` is
    (`:50-52`).
- **`contracts.ExchangeRates`** (new `srv/contracts/exchange_rates.go`):
  - `Rate(ctx, currency string, day time.Time) (ExchangeRate, bool, error)`, where
    `ExchangeRate` holds `Currency`, `Day`, `Rate` (a decimal string) and `Source`. It
    applies the same seven-day rule as `resolveRate`.
  - It has a single provider, wired as `SalesJournal` is: `module.Module.ExchangeRates
    func(Deps) contracts.ExchangeRates` (`srv/module/module.go:637-642`) and
    `Deps.ExchangeRates` (beside `Deps.SalesJournal`, `:148-153`).
  - `compose_test.go` gets a two-providers case, beside `:3344-3368`.
  - It has **no reader in C** (C: readings 10).

## C3. Server: settings and manual rates

- **`inv/settings.go`:**
  - `parseSettings` (`:245`) keeps `defaultCurrency` NOK-only, with `onlyNOK`'s message
    reworded to *NOK is the functional currency*.
  - `currencies` is parsed as follows:
    - upper-cased and de-duplicated;
    - at most 20 entries;
    - each in `nbCurrencies`, else a field error;
    - NOK refused as a field error.
  - **Turning a currency on** (I17, I18), in `PutInvoicesSettings`:
    1. Before the settings transaction, run `fetchRates` for the new currencies for 30
       days.
    2. Upsert what came back.
    3. Then, under `LockSettings`, refuse:
       - 409 `iban_required` when `iban` or `bic` is empty (I22), naming both fields;
       - 409 `exchange_rate_missing` when a new currency has no rate within 7 days of
         today. The problem's `currency` field names it, and its detail says to enter a
         manual rate first.
    4. `EnsureRateFetch` per currency.
  - A failed fetch is not itself a refusal: a manual rate entered earlier satisfies the
    check.
  - **Turning a currency off** is allowed (§11). Open documents keep their currency.
  - `settingsResponse` answers `currencies` and `rateFetches: [{currency,
    lastSuccessAt, lastError}]`.
- **New endpoints**, in `inv/exchange_rates.go`:
  - `GET /api/v1/invoices/exchange-rates?currency=&from=&to=` (`invoices:access`): the
    rows, at most 400 per page.
  - `PUT /api/v1/invoices/exchange-rates/{currency}/{day}` (`invoices:manage`), body
    `{rate}`:
    - the rate must be above 0 with at most six decimals (else 400);
    - 400 when the currency is not enabled;
    - 400 when the day is in the future;
    - it writes or replaces a `manual` row, which may replace a fetched one (C:
      readings 4).
  - `DELETE /api/v1/invoices/exchange-rates/{currency}/{day}` (`invoices:manage`):
    - a manual row only, else 409 `exchange_rate_fetched`;
    - 404 when there is no row;
    - 204 on success.
  - `POST /api/v1/invoices/exchange-rates/fetch` (`invoices:manage`) runs the worker's
    cycle once, now. It has a rate-limit entry in `inv/module.go:122-135`
    (`invoices-rates-fetch`, 10 per 10 minutes). It answers the status rows.

## C4. Server: the draft in currency (I19, I20, I28)

- **`parseDraft`** (`inv/drafts.go:226-249`) takes `enabled []string` in place of the
  one `currency`:
  - `currency` must be NOK or in `enabled`, else the field error *This currency is not
    turned on in the settings*.
  - `exchangeRate` (new, optional) is a positive decimal with at most six decimals. It
    is refused on NOK.
  - Its callers change:
    - `drafts.go:515` and `:642`;
    - `quick.go:113`, which passes `[]` and stays NOK (I20);
    - work and consumption (`work.go:959,1104`, `consumption.go:640,740`), which stay
      NOK and keep `currency_not_nok` and `mixed_currency` as they are (D11,
      `consumption.go:373`).
- **The draft's currency**, at create (`drafts.go:583`):
  1. the request's;
  2. else the billing profile's `Currency`, when it is enabled;
  3. else NOK.

  `customer_currency_differs` (`responses.go:30,499`) stands whenever the profile's
  currency differs from the draft's (I28).
- **Changing the currency** of a draft that holds sources, or deduction lines, is
  refused with 400 on `currency`: *A draft with order lines, work or a-konto keeps the
  currency it was made in.* The sources' prices are in their own currency.
- **The draft's rate:**
  - At each save, a foreign draft without `exchangeRate` stores `resolveRate(issue date
    or today)` with source `norges_bank`.
  - With `exchangeRate`, it stores that rate, source `manual`, and the date the person
    chose (`exchangeRateDate`, default today).
  - With no rate found, it keeps `exchange_rate` at 1, and the response warns
    `exchange_rate_missing`. The issue refuses.
  - The draft response gains `exchangeRate`, `exchangeRateDate` and
    `exchangeRateSource`.
- **Deductions** (`deductions.go:277`) already take the currency. They are passed the
  draft's, so an a-konto and its final settlement share it (I20).
- **From orders** (`inv/from_orders.go`):
  - `:122`: `currency_not_supported` gives way to `currency_not_enabled` for an order
    in a currency neither NOK nor enabled. The `Blocked` code changes, and so does the
    view.
  - `:434-437`: the per-order refusal becomes 409 `currency_not_enabled` with the same
    test. An enabled currency passes.
  - `:590`: `InsertInvoiceDraft` takes `Currency: d.group.key.currency`. The grouping
    already keys on currency (`order_lines.go:192-198`), so one draft is made per
    currency.
  - Each foreign draft resolves its rate as above.
- **The billing worker** (`inv/billing_worker.go:405-447`):
  - `foreign` collects only the orders whose currency is neither NOK nor enabled, with
    the code `currency_not_enabled`.
  - The item makes one draft per (customer, currency).
  - Energy items stay NOK.
- **Subscription periods** carry their order's currency already
  (`mig/00124:77`). No change is needed beyond the group.

## C5. Server: the arithmetic and the issue (I19, I21, I22)

- **`docmath`** (`srv/docmath/docmath.go`):
  - `VATSummary` gains `BaseNOK`, computed as `Round2(Taxable × rate)` beside `VATNOK`
    (`:103-110`).
  - `Totals` gains `BaseNOK` and `GrossNOK = Σ BaseNOK + Σ VATNOK`. It is **never**
    `Round2(Gross × rate)`.
  - New `NetsNOK(lines []TaxedLine, rows []VATSummary, rate) []*big.Rat`:
    - each line is `Round2(net × rate)`;
    - per (category, rate) row, the remainder against `BaseNOK` goes on the row's line
      with the largest absolute net, the lowest position on a tie;
    - so each row's lines sum to its `BaseNOK`.
  - For a rate of 1, every figure equals its currency figure. A property test checks
    this for NOK.
- **Credit notes** (`inv/credits.go:480-520`, the squaring of VAT): `BaseNOK` squares
  as `VATNOK` does (C: readings 3):
  - a credit note that brings a row's credited taxable to the original's takes
    `BaseNOK` = the original's row `BaseNOK` minus the earlier credit notes';
  - otherwise it takes `Round2(taxable × rate)`.

  `numericPair` reads the stored pair, and the new column joins `charged`'s query.
- **The issue** (`inv/issue.go`):
  - `invoiceIssueChecks` (`:190-257`) gains two refusals, each through `cannotIssue`:
    - `iban_required` for a foreign invoice when the settings snapshot lacks `iban` or
      `bic` (I22);
    - `exchange_rate_missing` when the source is `norges_bank` and
      `resolveRate(issueDate)` finds nothing within 7 days.
    - `currency_not_enabled` is **not** checked at issue (§11: turning a currency off
      leaves open drafts issuable).
  - Before `summarize` (`:512-519`):
    - an automatic rate is resolved again for the actual issue date, and `locked`'s
      rate is replaced in memory;
    - a manual rate stands.
  - The issue then writes:
    - `InsertVatSummary` (`:629-640`): `taxable_amount_nok`;
    - `SnapshotLine` (`:618-624`): `line_net_nok`, from `NetsNOK`;
    - `IssueDocument` (`queries/invoices.sql:128-168`): `exchange_rate = @exchange_rate`,
      `exchange_rate_date = @exchange_rate_date` and `exchange_rate_source`.
  - The COALESCE at `:140` goes. An invoice passes its resolved pair. A credit note
    passes its stored pair, which `InsertCreditDraft` copied
    (`queries/credits.sql:11,20`), so a credit note keeps its original's rate (I19).
  - **Pay links on a foreign invoice** (I22):
    - `payLinkAtIssue` (`inv/paylinks.go:106`) makes none;
    - `payLinkRefusal` (`:293`) refuses 409 `currency_not_nok` first;
    - `withPayLink` (`:226`) hides the action;
    - the attempt's create (`inv/attempts.go:429`, `payments.NOK(minor)`) gets a guard
      before the provider is called: 409 `currency_not_nok`, never a non-NOK amount
      sent.

## C6. Server: the PDF and the EHF (I22, I23, I26)

- **The PDF** (`inv/pdf.go:445-530`):
  - `pdfDocument` gains `exchangeRate`, `rateDate`, `rateSource` and the summaries'
    `taxableNOK`.
  - A NOK document renders byte for byte as before. `pinnedRenderSHA256`
    (`pdf_internal_test.go:231`) is unchanged.
  - **Off NOK:**
    - Every amount column keeps `inCurrency`'s header, and the total to pay keeps its
      code (`:498-500`).
    - A block below the VAT rows prints:
      - the rate: *Kurs EUR/NOK 11,7234, Norges Bank 16.11.2026*, or *Kurs EUR/NOK
        11,7234, avtalt kurs*;
      - a table with the category, *Grunnlag (NOK)* and *MVA (NOK)*, one row per rate,
        with a total row.

      The labels come in `nb` and `en` (`:76-118`).
    - The payment block (`:504-527`) puts IBAN and BIC first, then the account number,
      then the KID (I22). There is no Vipps line.
  - A new pinned hash, `pinnedRenderSHA256EUR`, for a EUR fixture with two VAT rates.
- **Reminder letters** (`inv/reminderpdf.go:103`) print the currency code on every
  amount. They take no rate block: a letter claims the currency amount.
- **The EHF** (`inv/ehf/`):
  - `ehf.Document` (`doc.go:25-56`) gains `TaxCurrencyCode string` and `VATTotalNOK
    *big.Rat`. Both are left empty for NOK.
  - `render.go`:
    - `:100`: when `TaxCurrencyCode != ""` and differs from `cur`, `cbc:TaxCurrencyCode`
      is written right after `cbc:DocumentCurrencyCode`, in UBL order.
    - `:271-280`: after the existing `cac:TaxTotal`, a second `cac:TaxTotal` holds only
      `cbc:TaxAmount currencyID="NOK"` with `VATTotalNOK`.
    - A NOK document writes neither, and its goldens do not move.
  - PaymentMeans stays code 30. Off NOK the IBAN and BIC are written as
    `PayeeFinancialAccount` whatever the buyer's country. The condition at `:235`
    widens to `foreign buyer || d.Currency != "NOK"`.
  - `precheck.go:175-196`:
    - `First("cac:TaxTotal")` stays the document-currency total, and the code says why.
    - New rule `RuleTaxCurrency` (`PEPPOL-EN16931-R005`): when `TaxCurrencyCode` is
      present it differs from `DocumentCurrencyCode`.
    - New rule `RuleTaxTotalNOK` (`"tax_total_nok"`): with `TaxCurrencyCode`, exactly two
      `cac:TaxTotal` exist. The second has no subtotal, its `TaxAmount`'s `currencyID`
      is `TaxCurrencyCode`, and it equals the document's `VATTotalNOK`, which the
      precheck is passed. Without a `TaxCurrencyCode`, there is exactly one.
    - The subtotal loop (`r.All("cac:TaxTotal", "cac:TaxSubtotal")`) is unchanged,
      since the second total has none.
  - `inv/ehf_*.go`, where the `ehf.Document` is built from the snapshot, passes the two
    fields from `vat_total_nok`.
  - **Goldens** (`ehf/testdata/golden/`):
    - new `invoice-eur.xml` and `credit-note-eur.xml`;
    - an invalid fixture, `tax-currency-equals-document.xml`, in `testdata/invalid/`
      with its `manifest.json` entry.
  - CI's `validate_ehf` job (`.github/workflows/ci.yml:533`) runs the Peppol BIS 3
    Schematron over them.

## C7. Server: reminders, the CSV export and stats (I25, I27)

- **`reminderrules`**:
  - The engine's input gains `Foreign bool`. Under it, `judgeCharge`'s outcome
    (`engine.go:319-333`) is forced to neither fee nor compensation, with a new note
    `NoteForeignCurrency = "foreign_currency"` (beside `:87-91`) so the letter's log
    says why.
  - Interest is computed as today, on the currency principal at the Norwegian rate
    (I25).
  - The engine stays pure. `purity_test.go` passes.
- **`inv/reminder_worker.go`** and the run's preview pass `inv.Currency != "NOK"`.
  Hand-off to collection is unchanged (I25).
- **Charge payments on a foreign invoice** are refused with 409 `currency_not_nok`, both
  by hand and as an allocation's `chargesAmount` (C: readings 6).
- **The CSV export** (`inv/csvexport.go:26-31,119-170`, `queries/export.sql:11`):
  - Three columns go at the end, by the header's own rule: `Exchange rate`, `Rate date`
    and `Rate source`.
  - `Base NOK` (`:137-141`) reads `COALESCE(taxable_amount_nok, …)` instead of
    computing it.
  - Every other column is unchanged. The header test is updated.
- **Stats** (`queries/stats.sql:2-40`) sum in NOK:
  - `gross_total` becomes `(invoices.document_nok(i.id)).gross_nok`;
  - `open_amount` becomes `invoices.open_nok(i.id)`.
  - The response's amounts are documented as NOK. A EUR seed invoice moves the stats
    test's expected figures.

## C8. Server: payments in currency and the exchange difference (I24)

- **New `inv/exchange_difference.go`**, pure and unit-tested. It holds the three rules
  that keep the NOK receivable matching the currency open amount:
  - `clearedNOK(inv, amount, openCurrencyAfter, openNOKBefore)`:
    - when `openCurrencyAfter > 0`, `Round2(amount × inv.rate)`;
    - when it is `== 0` (the closing movement), `openNOKBefore`.
  - `differenceNOK = amountNOK − clearedNOK`. It is positive for a gain.
  - For NOK, `cleared == amount == amountNOK`, and the difference is 0.
  - `openNOKBefore` is read **under the invoice's lock** through `open_nok()` (C1).
- **By hand** (`inv/payments.go:109-237`):
  - `InvoicesPaymentRequest` gains `amountNok`:
    - required off NOK (400 `amountNok`, *What arrived in NOK*);
    - refused on NOK unless it equals `amount`.
  - The form proposes it at the day's rate (`resolveRate(paidOn)`, falling back to the
    document's rate). The proposal is a field of the invoice response, `proposedRate`.
  - `:203`, `:234`: `payment.AmountNok` is the request's off NOK, and `amountNOKOf`
    stays for NOK. `exchange_difference_nok` comes from `clearedNOK`.
  - `keepRestAsCredit` off NOK (`:194-222`):
    - the payment takes what is open in the currency;
    - its `amount_nok` is `amountNok × open / amount`, rounded;
    - the closing rule gives its difference;
    - the rest is an `overpayment` credit row in the **invoice's currency**, worth
      `amountNok −` the payment's `amount_nok`.
- **Write-offs** (`inv/writeoffs.go:131`): `amount_nok` is `clearedNOK` (the document
  rate, or the NOK rest when it closes). No difference is posted: a write-off moves no
  money.
- **Credit rows** (`inv/credit.go`):
  - `nokOfInvoice` (`:199-210`) is used where a row's NOK is taken at the document's
    rate. It gains the closing rule: `credit_note` moves (`:366`) and `applied` take
    the NOK rest when they close.
  - The `applied` row's `amount_nok` (`:501`) is the credit's **carrying** NOK, not the
    document's: `balance_nok × use / balance`, rounded, and the whole `balance_nok` when
    the use empties the balance. The paired payment of source `credit` carries that
    `amount_nok` and its `exchange_difference_nok` (C: readings 7).
- **The queue, applied by hand** (`inv/bankqueue.go`):
  - `InvoicesBankTransactionApplyRequest.allocations[]` gains `amountNok`
    (`parseApply`, `:652-700`):
    - required when the invoice is not NOK;
    - refused when it is NOK.
  - In `applyLocked` (`:733-830`):
    - the line total (`:791`) sums `amountNok` for a foreign allocation and `amount` for
      NOK;
    - `allocation_exceeds_transaction` (`:807`) compares that sum;
    - the fail-closed error at `:816-820` becomes the foreign branch: `InsertImportedPayment`
      with `Amount: a.amount`, `AmountNok: a.amountNok` and the difference;
    - `chargesAmount` on a foreign invoice is refused 409 `currency_not_nok`.
- **Keep the rest as credit** (`inv/bankqueue_credit.go:85-130`): the fail-closed error
  at `:85` becomes the foreign branch.
  - The body gains `amount` (in the currency, default what is open) and `amountNok`
    (the line's share, required off NOK).
  - The payment is as above. The line's NOK rest, `line − applied − amountNok`, is an
    `overpayment` row in **NOK** naming the line. The customer's credit is per
    currency, and NOK is what the line holds (C: readings 8).
- **Automatic matching** (`inv/matching.go:540-550`): the fail-closed error in
  `registerMatch` moves earlier. `judgeMatch` (`:471-522`) queues a line whose KID names
  a foreign invoice with the reason `currency_differs`, and the invoice as its
  suggestion. Neither the split (B-2's `trySplit`) nor a match ever takes a foreign
  invoice: `trySplit`'s candidates stay NOK only.
- **The queue's actions** (`fe/components/bank-transaction-table.tsx` `actionsOf`, `:48`):
  `currency_differs` offers **Apply** and **Dismiss**. The API's `queueAction` judges
  the reason as `exceeds_open` is judged.
- **Bank transactions stay NOK:**
  - `ck_bank_transactions_currency` (`mig/00041:151`) is unchanged;
  - so are the bank feed's refusal (`bank_feed_sync.go:461`), camt.054's
    (`bankfile/camt054.go:196,480`) and OCR's (`ocr.go:408`).
  - Their comments name C: a currency account is later (§13).
- **The payment fact** (`queries/sales_journal.sql:35-71`, `inv/sales_journal.go:240-300`):
  - `JournalFacts` reads `p.amount`, `p.currency` and `p.exchange_difference_nok`.
  - `contracts.JournalFact` gains `Currency`, `Amount` (in the currency) and
    `ExchangeDifferenceNOK`. For NOK the difference is `"0.00"`, and `Amount ==
    AmountNOK`.
  - A removal carries its original's figures.
- **The events:** `invoices.payment.received` already carries `amountNok` (B-1). No
  change is needed.
- **Refunds in currency** (`inv/credit.go:635`):
  - the NOK-only check gives way to "NOK or enabled";
  - the body gains `amountNok`, the NOK that left, required off NOK;
  - `exchange_difference_nok = carrying − amountNok`, and the closing rule applies on
    the balance.

## C9. Accounting: posting in currency (I27)

- **`srv/contracts/sales_journal.go`:**
  - `JournalDocument` (`:100-115`) gains `ExchangeRate string` (`"1.000000"` for NOK)
    and `GrossNOK`.
  - `JournalLine` (`:134-153`) gains `Net`, in the document's currency, beside `NetNOK`.
  - `JournalVATSummary` (`:156-159`) gains `Base` and `VAT` in the currency.
  - `JournalCredit` gains `ExchangeDifferenceNOK`.
  - The file comment (`:37-40`) loses "Invoices issues NOK only".
- **`inv/sales_journal.go`:**
  - `journalLine` (`:204`) reads `COALESCE(line_net_nok, line_net)` in place of
    `nok(l.LineNet, rate)`.
  - `journalSummary` (`:221`) reads `COALESCE(taxable_amount_nok, taxable_amount)`.
  - `nok()` (`:75`) stays only for the charge payments' fallback.
  - `JournalOpenAt` (`queries/sales_journal.sql:90-127`) answers `OpenNOK` through the
    same terms as `open_nok()`, as of the day.
- **`srv/accounting/sales.go`:**
  - `documentVoucher` (`:229-322`): the receivables line carries `Currency`,
    `CurrencyAmount` (the signed gross) and `ExchangeRate` when the document is not
    NOK. Its amount stays `Σ BaseNOK + Σ VATNOK`.
  - Revenue and VAT post in NOK, with no currency columns (I27).
  - The remainder fix at `:305-307` stays as a guard. It is 0 now that lines sum to
    `BaseNOK` per row, and a test asserts it.
  - `factVoucher`, for `FactPayment` and `FactPaymentRemoved` (`:416-428`):
    - the bank line is `AmountNOK`;
    - the receivables line is `AmountNOK − ExchangeDifferenceNOK`, with the currency
      columns (`Amount`, the document's rate);
    - a non-zero difference posts to `roleCurrencyGain` (credit) or `roleCurrencyLoss`
      (debit). These are the roles `supplier_payments.go:26-29` defines (8060 and 8160,
      `chart.go:234-235`).
    - A removal turns the whole voucher, as today.
  - `creditVoucher` (`:538`):
    - `credit_applied` and `credit_refunded` carry the difference to the same roles;
    - the credit item's line carries the credit's currency columns.
  - Write-off vouchers take `AmountNOK` as they do now. The rule versions bump
    (`salesRulePaymentVersion`, the credit rules' versions).
- **Posting** (`srv/accounting/posting.go:117-122,340-342`) already writes the currency
  triple. `ck_postings_currency` holds the three together.
- **Reports and open items** (`srv/accounting/reports.go`, the customers report): the
  open item shows the currency amount beside the NOK, read from the postings' sums.
  There is no unrealised revaluation (§13: Accounting's year-end).
- **SAF-T** (`saft_export.go:1351-1352`) already exports the currency triple per line.
  A test checks a EUR invoice's receivable line.
- `openapi/accounting.yaml`: the open item's `currency` and `currencyAmount`, if the
  report answers them.

## C10. OpenAPI and generation

- **`openapi/invoices.yaml`:**
  - the settings' `currencies` and `rateFetches`;
  - the four exchange-rate operations, each with `x-vantigo-access` per §8 (manage for
    writes, access for reads);
  - `InvoicesInvoiceRequest` (`:3444`): `exchangeRate` and `exchangeRateDate`;
  - the invoice response: `exchangeRate`, `exchangeRateDate`, `exchangeRateSource`,
    `proposedRate` and the VAT rows' `taxableAmountNok`;
  - `amountNok` on the payment request, the apply allocation, keep-as-credit and the
    refund;
  - the queue reason `currency_differs`;
  - the codes `exchange_rate_missing`, `iban_required`, `currency_not_enabled`,
    `exchange_rate_fetched` and `currency_not_nok` (pay links, charges);
  - the warning `exchange_rate_missing`;
  - stats' amounts described as NOK;
  - the permission catalog's `invoices:manage` and `invoices:payments` descriptions
    (`:22`) name currencies, manual rates and payments in currency.
- **Events:** the `invoices.exchange_rate.fetched` payload goes in the events schema.
- **Generation:** run `go generate ./...` in `apps/server` and `bun run gen:client` in
  `apps/invoices` and `apps/accounting`. Commit both `api-schema.d.ts`.

## C11. Frontend

- **`fe/pages/settings.tsx`:**
  - A **Currencies** block under the bank account fields:
    - a multi-select of Norges Bank's currencies;
    - per enabled currency: the last fetch, or its error, **Fetch now**, and the last
      seven rates with their source;
    - **Enter a rate** (day and rate) and **Remove**, on manual rows only.
  - Its help text: *Invoices in another currency need the IBAN and BIC. Rates come from
    Norges Bank each business day after 16:30; a rate you enter is never overwritten.*
- **`fe/api/exchange-rates.ts`.**
- **`fe/pages/invoice.tsx`** (the draft, `:885-1064`):
  - a **Currency** select (NOK plus the enabled ones), disabled when the draft holds
    sources;
  - beside it the rate, *EUR/NOK 11,7234 · Norges Bank 16.11.2026*, with **Use another
    rate**, which opens the rate and its date (source `manual`) and offers **Use Norges
    Bank's rate** to revert;
  - the totals show *VAT in NOK* and *Total in NOK*;
  - the warnings `exchange_rate_missing` and `customer_currency_differs` are shown;
  - an issued foreign invoice shows the rate line, and has no pay-link action.
- **`fe/pages/-register-payment-modal.tsx`** (`:86-124`): off NOK, a second field,
  **Received in NOK**, prefilled at `proposedRate` × amount. Under it, the exchange
  difference it will book (*Currency gain NOK 12,40*).
- **`fe/pages/-apply-dialog.tsx`** and **`-keep-as-credit-dialog.tsx`**: a foreign
  invoice's row asks for the amount in its currency and its share in NOK. The sum
  check uses the NOK shares.
- **`fe/components/bank-transaction-table.tsx`:** the reason `currency_differs`, with
  its label *The invoice is in another currency*.
- **`fe/components/customer-credit-card.tsx`:** balances per currency; a refund off
  NOK asks for the NOK that left.
- **`fe/pages/from-orders.tsx`:** `currency_not_enabled` replaces
  `currency_not_supported` in the blocked labels.
- **i18n:** every string in `en` and `nb` (`fe/i18n.ts`). `bun run translations:check`
  passes.
- **Tests:**
  - the currency select and the rate override;
  - the payment modal's NOK field and its difference;
  - the apply dialog's sum in NOK;
  - the settings' currency block with a refused `iban_required`.
- **`apps/accounting/frontend`:** the open items page shows the currency amount beside
  the NOK.

## C12. Tests (spec §12)

- **Unit:**
  - `docmath`:
    - `NetsNOK`'s remainder within each row (two rates, three lines, a negative
      deduction line);
    - `BaseNOK + VATNOK ≠ round(gross × rate)` on a chosen case;
    - NOK's identity.
  - `exchange_difference_test.go`:
    - a partial payment;
    - two NOK transfers at different rates (§11);
    - the closing payment clearing the NOK rest exactly, with no phantom 0.01;
    - a closing write-off;
    - a closing credit row;
    - applied credit at a carrying value.
  - `norgesbank_test.go`, against recorded responses in
    `inv/testdata/norgesbank/`:
    - EUR+SEK+JPY in shuffled order (trap 1);
    - UNIT_MULT `2` at index 0 (trap 2);
    - the 404 no-data body;
    - a 500;
    - an oversized body;
    - a redirect refused.
  - `reminderrules`: a foreign invoice gets no fee or compensation but does get
    interest.
- **A fake Norges Bank** (`inv/norgesbank_fake_test.go`): an `httptest` server serving
  SDMX-JSON per currency and day, with `FailOn(currency)` rather than `FailNext` (see
  the memory *Google fake FailNext vs token cache*).
- **Integration**, against Postgres:
  - **Every new refusal:**
    - `iban_required` at settings and at issue;
    - `exchange_rate_missing` at settings and at issue, including the 7-day edge after
      a Norwegian holiday (§11);
    - `currency_not_enabled` from orders;
    - `currency_not_nok` on pay links, attempts and charges;
    - `exchange_rate_fetched`.
  - **Every trigger and check:**
    - the manual row surviving a fetch's upsert;
    - `ck_invoices_exchange_rate_source`;
    - `ck_payments_exchange_difference`;
    - the queue reason constraints.
  - **The rate:** the automatic rate re-resolved at issue; the manual one kept; a
    credit note keeping its original's rate.
  - **Orders:** a EUR order invoiced from orders and by the billing worker, giving one
    draft per currency.
  - **The bank queue:** a NOK line applied to a EUR invoice, and its NOK rest kept as
    credit in NOK; a KID naming a EUR invoice queued `currency_differs`.
  - `open_nok()` equal to the open amount for every NOK document of the seed.
- **The worker:**
  - `TestRatesWorker_FetchesOnAPoolOfOne`, after `bank_feed_worker_test.go:234` and
    `credit_test.go:614-623`'s `poolOfOneDeps`, proving no connection is held across
    the fetch;
  - a lease test of two workers on one currency;
  - the 16:30 gate on a business day and on a holiday.
- **Races:** a payment by hand against an apply from the queue on one EUR invoice. Both
  take the invoice's lock, and the second sees the first's NOK rest.
- **Golden PDFs:** the NOK hash unchanged, and `pinnedRenderSHA256EUR`.
- **EHF:**
  - goldens `invoice-eur.xml` and `credit-note-eur.xml`;
  - the precheck's two new rules;
  - every NOK golden byte-identical;
  - CI's Schematron job.
- **Accounting:**
  - a EUR invoice's voucher, with the receivable's currency triple;
  - a payment with a gain and one with a loss;
  - the closing payment leaving 1500's item at exactly 0;
  - applied credit with a difference;
  - `OpenAt` plus `CreditsAt` equal to 1500's balance with a EUR invoice open.
- **Sales journal:** the payment fact's `Amount`, `Currency` and
  `ExchangeDifferenceNOK`; `JournalLine.Net`; `ExchangeRate`.
- **Compose:** the second `ExchangeRates` provider is refused.

## C13. Seed, documentation, assistant, site, gates, review

- **Seed** (`apps/server/cmd/vantigo/seed.go`, `seedInvoicesReceivables` `:1536`):
  - the settings' IBAN and BIC (if empty), with `currencies = {EUR}`;
  - 30 days of EUR rates with source `norges_bank`, and one `manual` row;
  - a customer whose billing profile currency is EUR;
  - an Orders order in EUR (through Orders' seed, if it seeds orders), invoiced;
  - an issued EUR invoice with two VAT rates, partly paid in NOK with a gain, then
    closed by a second payment;
  - one EUR line in the queue with `currency_differs`.
- **Docs** (AGENTS.md; `nb` translated page for page):
  - **`{en,nb}/user/invoices.md`:**
    - *The seller record and the number series* (`:42`): IBAN and BIC for foreign
      invoices;
    - a new *Currencies and exchange rates*, after *Agree a KID with the bank*
      (`:169`): turning a currency on, the fetch, entering a rate;
    - *Creating a draft* (`:326`): the currency and the rate, and the override;
    - *Invoicing orders* (`:653`): foreign orders invoiced, and *When the orders wizard
      refuses* (`:774`) gets `currency_not_enabled`;
    - *Issuing* (`:888`): the two refusals;
    - *The quick invoice* (`:939`): NOK only;
    - *Registering payments* (`:1109`): received in NOK, and the exchange difference;
    - *The exception queue* (`:1393`): `currency_differs` and the apply in NOK;
    - *Reminders* (`:1486`): no fees on a foreign invoice.
  - **`en/reference/invoices.md`:**
    - the legal bullet at `:98-100`, rewritten from § 5-1-1 nr. 6's wording;
    - the model table (`:140-161`): `exchange_rates`, `rate_fetches`,
      `settings.currencies`, `exchange_rate_source`, the two `_nok` snapshots,
      `exchange_difference_nok`, `document_nok()` and `open_nok()`;
    - *Issuing* (`:1889`): I19 and I21;
    - *Payments and the state of an invoice* (`:2269`): I24's three rules;
    - *The PDF* (`:2691`);
    - *The EHF document* (`:2770`): `TaxCurrencyCode` and the second `TaxTotal`;
    - *Bank files and the exception queue* (`:3401`);
    - *Reminders* (`:4111`);
    - *Vipps payments* (`:5069`): NOK only;
    - *The journal* (`:5585`), *The CSV export* (`:5611`) and *Stats* (`:5669`);
    - *What Invoices reports to other modules* (`:5958`): `ExchangeRates`, and the
      journal's new fields;
    - the endpoints (`:6281`), the events (`:6527`), the workers, and *What comes
      next* (`:6415`);
    - `sources` gains the migration and the new files.
  - **`en/reference/accounting.md` and `{en,nb}/user/accounting.md`:** receivables in
    currency, and the payment's difference to 8060 and 8160.
  - **`{en,nb}/user/orders.md` and `en/reference/orders.md`:** orders in an enabled
    currency are invoiced.
  - **`{en,nb}/user/customers.md`:** the billing profile's currency now picks the
    draft's.
  - **`{en,nb}/admin/configuration.md`:** `INVOICES_RATES_URL` and
    `INVOICES_RATES_ENABLED`, the outbound host `data.norges-bank.no`, and what fails
    closed.
  - **`en/contributing/module-boundaries.md`:** `contracts.ExchangeRates` among the
    single-provider contracts.
  - **`ROADMAP.md`**, Invoices phase 6: C done.
  - Run `go generate ./...` after the docs (see the memory *docs edit needs go
    generate*).
- **Assistant:**
  - `fe/assist-targets.json` gains `invoices.settings.currencies`,
    `invoices.invoice.rate` and `invoices.payment.amountNok`, in `en` and `nb`;
  - bump `promptVersion` (`srv/assistant/prompt.go:15`);
  - there are no file endpoints, so `assistant_test.go` is untouched.
- **The public site** (`../vantigo-landing`, checked out beside this repository,
  `web/src/apps/invoices.ts`, en and nb): add one line, written from the guide pages:
  *Invoice in euro and other currencies, at Norges Bank's daily rate, with the VAT
  shown in NOK as the law requires, and the currency difference booked when the
  customer pays.* Open it as a PR there, linked from this one.
  `mise run apps:check` must pass in that repository.
- **Gates:**
  - `mise run server:check`;
  - `mise run server:test`, or the packages invoices, accounting, docmath, config and
    module with `-p 1` if the shared database runs out of clients;
  - `mise run frontend:check`, reading its log;
  - `mise run docs:check`;
  - signed Conventional Commits.
- **Review:** an independent review before the PR leaves draft. It checks in
  particular:
  - the closing rule, that `open_nok()` reaches exactly 0 and Accounting's 1500 item
    with it;
  - the parser's two traps, and that `detail=dataonly` is never sent;
  - the worker holding no connection across the fetch;
  - every NOK golden (PDF and EHF) byte-identical;
  - the fail-closed paths: no foreign amount ever reaches Vipps, a match or a split;
  - the manual row never overwritten.

## C: readings against the code

These become spec amendments (revision 6).

1. **The legal source is § 5-1-1 first ledd nr. 6**, not merverdiavgiftsloven § 4-11.
   § 4-11 is about imports, and the act's currency rule is § 4-12 (2). § 4-2 of the
   regulation fixes the rate at the invoice date. Q6 is closed.
2. **The spec's §9 has no column for `BaseNOK` or a line's NOK net.**
   - Today `vat_summaries` holds only `vat_amount_nok` (`mig/00034:237-247`).
   - `sales_journal.go:204-226` and `csvexport.go:137-141` compute `round(x × rate)`
     at read time, which cannot carry I21's per-row remainder.
   - **Resolution:** `taxable_amount_nok` and `line_net_nok`, nullable for documents
     issued before C (all NOK). They are written at issue while the document is still
     a draft, so `refuse_issued_child_change` needs no change.
3. **A credit note's `BaseNOK` must square** as its `VATNOK` does
   (`inv/credits.go:480-520`). Otherwise two partial credits that sum to the original
   leave øre on the NOK receivable, and no payment arrives to take the rest.
   **Resolution:** the squaring row takes the original's `BaseNOK` rest.
4. **"Manual is never overwritten"** sits against a primary key of (currency,
   rate_date), which allows one row per day. **Resolution:**
   - a manual entry replaces a fetched row of that day;
   - a fetch never replaces a manual row, through its `WHERE` clause and the trigger;
   - deleting a manual row lets the next fetch fill the day.
5. **The closing rule needs the NOK already cleared.** `amount_nok` alone is what
   arrived, not what the receivable lost. **Resolution:**
   `payments.exchange_difference_nok`, stored at insert under the invoice's lock, and
   `invoices.open_nok()`, read under that lock. Write-offs and credit rows carry the
   cleared figure as their `amount_nok`, since they move no money.
6. **Charges on a foreign invoice.** Interest is in the currency (I25), but
   `charge_payments` has no `amount_nok`, and B left them NOK. **Resolution:** a charge
   payment on a foreign invoice is refused `currency_not_nok`, by hand and as
   `chargesAmount`. Interest is printed and goes to collection. A charge payment in
   currency is later.
7. **Applied credit has two NOK values:** what the credit was worth when it arose, and
   what the receivable it clears is worth. **Resolution:** the `applied` row carries
   the credit's carrying NOK, the average of the balance and its rest on the last use.
   Its payment carries that `amount_nok` with the difference. Accounting posts the
   difference on the credit fact, since the payment of source `credit` posts nothing
   (B: readings 7).
8. **Keep-as-credit from a NOK line on a foreign invoice.** The line's rest is NOK.
   **Resolution:** the rest is kept as **NOK** credit, which the per-currency ledger
   allows. It is not converted to the invoice's currency.
9. **Refunds were NOK-only in B** (`inv/credit.go:635`). Credit in EUR arises in C
   (overpayments by hand, credit-note surpluses) and could never leave. **Resolution:**
   a refund in an enabled currency asks for `amountNok`, and posts the difference
   against the carrying value.
10. **`contracts.ExchangeRates` readers** (I18, I28 "the plan decides"):
    - Accounting needs no rate in C, because the journal carries each document's.
    - Orders' NOK equivalent in its analysis is not in C. It goes on the roadmap's
      *Later* list.
    - The contract ships with its provider and a compose test, so either can read it
      without a change to Invoices.
11. **The worker's name.** The spec names it `invoices-rates`, and the module uses both
    `invoices-bank-feed` and `invoices.credit_moves`. **Resolution:** `invoices-rates`,
    as the spec says, the hyphenated form of the polling workers.
12. **What the precheck reads.** The precheck's `First("cac:TaxTotal")` reads the
    first total, which stays the document-currency one with its subtotals. Its subtotal
    loop already skips the second, so only the new rules are added.
    `render.go:235` already writes the IBAN for a foreign *buyer*. C widens it to a
    foreign *document*.
13. **The grouping already keys on currency** (`order_lines.go:192-198`). Only the
    currency passed to `InsertInvoiceDraft` (`from_orders.go:590`) and the two checks
    change. The billing worker's `foreign` skip stays for currencies that are not
    enabled.
14. **The remainder rule's precedent.** I21 cites `srv/accounting/purchases.go:145` for
    the remainder "within the VAT row". That code puts it on the largest base line of
    the whole document. Invoices follows I21 (per row). Purchases is unchanged.
    Accounting's own fix (`sales.go:305-307`) becomes a zero guard.
15. **Stats in NOK** read through `document_nok()` and `open_nok()`. The response's
    figures are NOK for every document. The field names are unchanged, and their
    descriptions say NOK.
16. **Later, not C** (set at delivery): Orders' NOK equivalent in its analysis through
    `contracts.ExchangeRates`, and Accounting's open items showing the currency amount
    beside the NOK (the postings carry the currency triple), go on the roadmap's
    *Later* lists of Invoices and Accounting.
17. **Turning a currency on fetches synchronously**, before the settings transaction,
    so `exchange_rate_missing` can be judged in the same request. The fetch holds no
    lock.
