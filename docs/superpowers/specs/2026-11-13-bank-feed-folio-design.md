# Invoices: a bank feed, with Folio as the first bank — design

Date: 2026-11-13. Status: accepted by the owner 2026-11-14. Plan:
[2026-11-14-bank-feed-folio-plan](../plans/2026-11-14-bank-feed-folio-plan.md), whose
*Deviations and findings* amend this design where the code made a decision here wrong —
among them: a queued feed line a person applies is registered by that person (F6 over
F10), the feed's read is open to `invoices:access` (F8), and Folio's transaction id is
kept without hyphens as the archive reference (F5).

## The problem

Invoices learns that an invoice was paid in three ways: a person registers the payment,
a person uploads the bank's file (OCR giro or camt.054), or Vipps tells the payments
worker. The first two wait for a person. The payments design left "bank APIs and direct
file delivery" out on purpose (D20,
[the reference](../../src/content/docs/en/reference/invoices.md)), and the roadmap still
lists "a bank API for payments" under Later.

Folio, a Norwegian business account for small companies, now has a public API. A seller
who banks there could have incoming payments read, matched on their KID and registered
without anyone fetching a file. This design adds that as a **bank feed**: a port any bank
with an API can sit behind, Folio the first adapter, and the existing matching and
exception queue unchanged behind it.

Orders is not part of it. Orders takes no payment and hands off to Invoices, and Folio is
a bank account, not a checkout: it cannot take a buyer's card or Vipps payment.

## Readings

From Folio's OpenAPI document (`https://api.folio.no/v2/api.yml`, read 2026-11-13) and
their announcement (`https://folio.no/guider-og-artikler/Folio-API`).

- **R1 — Authentication.** A bearer token. A customer creates an API key in Folio
  (`app.folio.no/til/api-tilgang`); a company building an integration can instead get an
  OAuth2 client. The document names no scopes: a key reads everything and may create
  payments.
- **R2 — Accounts.** `GET /accounts` answers each account's `accountNumber` (BBAN),
  `name`, `type` (`Operational`, `Tax`, `Savings`, `Earmarks`, `Card`), `balance`, and
  `matchingTransactionsAt`: the last time the balance matched the transactions, with
  nothing pending.
- **R3 — Transactions.** `GET /accounts/{accountNumber}/transactions?startDate&endDate`
  (dates inclusive, `endDate` defaulting to today) answers `transactions.booked[]` only.
  Each has `id` (uuid), `bookingDate`, `description`, `kid` when present,
  `transactionAmount` (`amount` with two decimals, unsigned, and `currency`),
  `currencyAmount` (the original currency), `creditor` and `debtor` (`name`,
  `accountNumber`), `paymentId` when our own payment made it, `creditorEventId` on both
  sides of an internal transfer, and `cardAuthorization` for a card purchase.
- **R4 — No direction field.** An amount carries no sign and a transaction no
  credit/debit marker. Whether money came in has to be read from `creditor.accountNumber`
  being the account asked for. **Not verified against a real response.**
- **R5 — No reversal marker**, no value date, no ordering date, no bank transaction code.
- **R6 — No webhooks, no paging, no stated rate limit, no test environment.** The one
  server listed is production. Everything is read by date range.
- **R7 — Writing.** Payments are created as drafts and execute only after a person signs
  with BankID; attachments can be uploaded to an event; a salary run can be created. None
  of it is used by this design ([Later](#later-named-not-designed)).
- **R8 — KID on incoming payments is not confirmed.** The schema has `kid`, but the
  document does not say whether a payer can pay a Folio account with a KID, or whether
  that needs an agreement as an OCR giro agreement does elsewhere.

From Vantigo as built:

- **R9 —** The import stores a file, then its lines, and matches each line on its KID
  ([Bank files and the exception queue](../../src/content/docs/en/reference/invoices.md)).
  Matching needs, per line: the receiving account, the booking day, the amount, the KID,
  and for the suggestions the remittance text and the debtor's account. R3 gives all of
  them.
- **R10 —** Each account has one import format; changing it sets `cutover_through`, and a
  line of the new format booked on or before it is held back as `possible_duplicate`.
- **R11 —** A payment's `source` is `manual`, `ocr`, `camt054` or `vipps`
  (`ck_payments_source`, migration 00046). A bank-line payment must name a registering
  user; a Vipps payment names none.
- **R12 —** The payments port (`internal/payments`, adapter `payments/vipps`, fake
  `payments/vipps/vippstest`) is the shape for a provider behind an interface; the secrets
  box (`internal/secrets`) seals a workspace's credential under a purpose key; the
  payments worker is the lease-worker shape.

## Decisions

### F1 — A port, `internal/bankfeed`, and an adapter beside it

A platform package like `payments`: interfaces and values, no SQL, importing no module.

```go
type Feed interface {
    // Verify makes the cheapest authenticated call, for the settings page.
    Verify(ctx context.Context) error
    // Accounts lists the accounts the credential can read.
    Accounts(ctx context.Context) ([]Account, error)
    // Transactions answers the booked transactions of account from..to, both
    // inclusive, each with the bytes the bank sent for it.
    Transactions(ctx context.Context, account string, from, to time.Time) ([]Transaction, error)
}
```

`Transaction` carries the bank's id, the booking day, the amount in minor units with its
currency, a `Direction` (`credit`, `debit`, `unknown`), a `Kind` (`transfer`, `card`,
`internal`), the KID, the text, the debtor's name and account, and `Raw json.RawMessage`.
The adapter decides direction and kind; the module decides what a line means. An adapter
never retries: a read is repeated by the next cycle.

`bankfeed/folio` is the adapter, `bankfeed/folio/foliotest` its fake. The adapter takes
its transport from `Deps.HTTPTransport`, follows no redirect, bounds a call at 30 seconds
and a response at 10 MiB, and maps 401 and 403 to `bankfeed.ErrUnauthorized`, 429 to
`ErrRateLimited` with the `Retry-After` it was given, anything else to a plain error.

**Folio's mapping.** `credit` when `creditor.accountNumber` is the account asked for,
`debit` when `debtor.accountNumber` is, `unknown` when neither or both (R4). `internal`
when `creditorEventId` is present, `card` when `cardAuthorization` is, else `transfer`.
The amount is `transactionAmount`, never `currencyAmount`.

### F2 — The feed belongs to Invoices; it is not a module

The connection, its accounts and the worker live in Invoices, as Vipps does. Invoices is
the one consumer today. When Expenses reads card purchases ([Later](#later-named-not-designed))
it uses the same port with tables of its own, and the question of one shared credential
is taken then, not now.

### F3 — The credential: an API key first, at the workspace tier

- **Instance tier.** `INVOICES_BANK_FEED_ENABLED` (default on; `0` sends no request to any
  bank and keeps what is stored) and `INVOICES_FOLIO_BASE_URL` (default
  `https://api.folio.no/v2`). The base URL is the operator's, never the workspace's, so
  the adapter cannot be pointed at another host from the interface.
- **Workspace tier.** A person with `invoices:manage` chooses the bank (Folio, the only
  choice) and pastes the API key. It is sealed by the secrets box under the purpose
  `invoices/bank-feed-credential`, never answered again, and shown as its last four
  characters. Saving verifies it (`Verify`); a key that fails is not stored.
- **OAuth is not in this design.** It needs an agreement with Folio and a pre-approved
  redirect, and buys nothing a read needs. It becomes necessary with outgoing payments
  (BankID signing from inside Vantigo), which is Later.

A leaked key reads the company's whole bank account. It cannot move money: a payment
created with it is a draft until a person signs with BankID (R7). The admin page says
both.

### F4 — Which accounts, and from which day

After the key is saved the screen lists Folio's accounts. One can be switched on when it
is the seller's **Bank account** or an account an issued invoice printed — the rule the
import already applies (`bank_account_unknown`). An account of type `Card` is never
offered.

Switching an account on asks for a **start day**, defaulting to today: nothing booked
before it is read, so payments already registered by hand are not registered again. It
then sets the account's format to `feed` by the existing format change (R10): an account
that had `ocr` or `camt054` keeps that as `previous_format` with its `cutover_through`,
and feed lines booked on or before the cutover are held back as `possible_duplicate`.
While the format is `feed`, a file uploaded for that account is refused as today
(`bank_import_format_mismatch`). Switching the feed off sets no format; the person
changes it back by the same control when they return to files.

### F5 — A sync is an import without an upload

One lease worker, the payments worker's shape. Each enabled account is due every
**30 minutes** by default (`INVOICES_BANK_FEED_INTERVAL`, 5 minutes to 24 hours), and
`POST /invoices/bank-feed/sync` (`invoices:payments`) makes an account due now through
the same claim, so a manual sync and the worker never run together.

A cycle for one account, the bank called outside any transaction:

1. **Read** `from` = the later of the start day and the account's latest feed booking
   day less **10 days**; `to` = today, the business day. The overlap is re-read every
   time because a bank may book a transaction on an earlier day than it first appeared.
2. **Refuse the whole batch**, storing nothing, when a transaction has a booking day
   after today, an amount that is not `NOK` with two decimals, or a direction `unknown`.
   The files' rule: all or nothing, and the reason kept on the account for the screen.
3. **Keep** the transfers that are credits. Everything else is counted and not stored:
   `debit`, `card_information`, `zero_amount` (the existing kinds) and a new
   `internal_transfer`.
4. **Drop what is already stored**: a kept transaction whose fingerprint an earlier batch
   stored is the same record read again, not a duplicate, and gets no row. With nothing
   left the cycle ends with the account's `last_synced_at` set and no batch.
5. **Store the document.** The kept transactions' `Raw` bytes, in the order received, as
   one JSON array under `bank-files/<sha256>.json`, by the files' `Exists`-then-`Put`.
   It is the documentation of the payments booked from it, kept as the files are. Without
   an object store the cycle fails and is tried again.
6. **The import's transaction**, as it is today: the account's row, a `bank_files` row
   with format `feed`, provider `folio`, no uploader and the identity
   `folio|<account>|<claim time>`, then the lines.
7. **Matching**, per line, after the commit — unchanged.

**What a line holds.** `line_ref` Folio's transaction id; the account; the booking day;
no value or ordering day; the amount; the KID as written; the remittance text
(`description`, at most 1 000 characters); the debtor's name and account; the archive
reference Folio's transaction id again; no bank code. The fingerprint is computed as for
a file, and since it covers the archive reference it is unique per Folio transaction: the
ordinal is always 1 and two genuine payments of one day, amount and KID stay two lines.

**What is never stored.** Outgoing transactions — salaries and supplier payments among
them — card purchases and internal transfers leave only a count. Merchant, card and agent
details are never asked for.

### F6 — Matching is unchanged, but for who registers

Classification, the lock order, the allocation and the exception queue are as built. Two
things change, both in the schema's checks:

- A payment or charge payment from a feed line has `source` **`bank_feed`**, its
  `bank_transaction_id`, and **no registering user**, as a Vipps payment has none. The
  line's `matched` and `queued` events and a `deadline_met` waiver are likewise by no
  user; the screens show *Bank feed* where they show a name.
- The soft key keeps comparing lines of different batches. A second payment of the same
  day, amount and KID in a later batch is held back as `possible_duplicate`; it would
  have been queued `invoice_settled` or `exceeds_open` anyway.

A line a person then applies from the queue is registered by that person, as today.

**Without a KID** (R8) nothing is registered by itself: the line is queued `no_kid` with
the existing suggestions — the invoice number in the text, an open amount that equals the
line's, the debtor's account seen before. The feed is still worth having then: the queue
fills itself and each line is one confirmation.

**Reversals.** The feed cannot see one (R5): money sent back is an ordinary debit and is
counted, not stored. A payment that was returned stays registered until a person removes
it. The user guide says so.

### F7 — When the bank says no

| What happens | The account | Next try |
| --- | --- | --- |
| `ErrUnauthorized` | status `unauthorized`; every account of the connection stops | none until the key is saved again |
| `ErrRateLimited` | status `ok`, the wait noted | after `Retry-After`, at least the interval |
| any other error, a timeout | `failing` after three in a row | the interval doubled, up to 6 hours |
| a batch refused (F5 step 2) | `refused`, with the reason | the interval; the same batch is refused until Folio's data or the adapter changes |

`unauthorized`, `failing` and `refused` appear on `/stats/attention` for people with
`invoices:manage`. No mail is sent. A cycle that fails writes nothing but the account's
status.

### F8 — The API

All under `invoices`, in `openapi/invoices.yaml`.

| Operation | Permission | What it does |
| --- | --- | --- |
| `GET /invoices/bank-feed` | `invoices:payments` | the connection: bank, the key's last four, status, and each account with its status, start day, last sync and last batch |
| `PUT /invoices/bank-feed` | `invoices:manage` | `{provider, apiKey}`; verified, then sealed. 409 `bank_feed_disabled` when the instance switch is off; 422 `bank_feed_credential_rejected` |
| `DELETE /invoices/bank-feed` | `invoices:manage` | removes the key and switches every account off; files, lines and payments stay |
| `GET /invoices/bank-feed/accounts` | `invoices:manage` | the bank's accounts, read now, each with whether it can be switched on and why not |
| `PUT /invoices/bank-feed/accounts/{account}` | `invoices:manage` | `{enabled, startDay}`; 409 `bank_account_unknown` as the import |
| `POST /invoices/bank-feed/sync` | `invoices:payments` | `{account?}`; makes the account, or every enabled one, due now. 202 |

`GET /meta` gains `capabilities.canManageBankFeed`. `BankFile` gains `provider` and a
nullable `uploadedBy`; `format` gains `feed`; a payment's `source` gains `bank_feed`.
These are plain strings in the contract, not enums.

### F9 — The screens

- **Workspace admin → Integrations → Bank** (nb *Bank*): connect, the accounts with their
  switches and start days, each account's status and last sync, disconnect.
- **Invoices → Payments → Bank files**: a feed batch is listed among the files as
  *Bank feed · Folio* with its counts, and a *Fetch now* button beside *Import* for
  people with `invoices:payments`. The exception queue needs no change beyond showing
  *Bank feed* as who registered.

### F10 — The schema

One migration, numbered against `main` right before merging:

- `invoices.bank_feed_connections`: one row, the provider, the sealed credential, its
  last four, status, who connected and when.
- `invoices.bank_feed_accounts`: the account, enabled, the start day, status and reason,
  the failure count, `last_synced_at`, `next_sync_at`, and the lease's columns.
- `bank_files`: `provider text NULL`; `uploaded_by_user_id` nullable exactly when
  `format = 'feed'`; `ck_bank_files_format` gains `feed`, and so do
  `ck_bank_transactions_format` and `bank_import_accounts`' two format checks.
- `ck_payments_source` and `ck_charge_payments_source` gain `bank_feed`: a
  `bank_transaction_id`, no payment attempt, no registering user.
- `bank_transaction_events.by_user_id` becomes nullable for the events matching writes.

## Out of scope, named

Outgoing payments and BankID signing · OAuth · salary runs · receipts and attachments ·
balances and statements · a second bank's adapter · camt.053 · reversals detected by the
feed · a customer credit balance for overpayments · webhooks (Folio has none).

## Later, named not designed

- **The second adapter: an aggregator, not a bank.** No other Norwegian bank found on
  2026-11-14 lets a business customer switch on a documented API by themselves:
  SpareBank 1 sends corporate customers to a partner or to files, and the banks' PSD2
  APIs need a licence. Aritma's banking API covers the Norwegian business banks behind
  one contract, with webhooks, and is the likely second adapter behind this port. It
  needs a commercial agreement; no price was found.

- **Expenses: company cards.** Folio's events carry card purchases with merchant,
  cardholder and receipt. They could become outlays in Expenses with the receipt attached,
  and a receipt uploaded in Vantigo could be sent to Folio. Second in value after this
  design; it needs Folio's people mapped to Vantigo's users.
- **Expenses: reimbursements paid from the bank.** A draft payment per approved claim
  instead of the payroll file, signed in Folio. Needs OAuth for signing inside Vantigo.
- **Supplier invoices paid with KID.** Belongs with the Purchasing module the roadmap
  names, since Expenses' supplier invoice is explicitly not accounts payable.

## Deliveries

Stacked, each with its documentation in the same series.

- **A — The port and Folio's adapter.** `internal/bankfeed`, `bankfeed/folio`, the fake,
  purity tests, depguard sets. First task: a recorded response from a real Folio account
  with an incoming payment, to settle R4 and R8 before anything is built on them.
  Docs: `contributing/platform-packages.md` (the bank feed port), a new
  `contributing/bank-feeds.md` (*Adding a bank*), listed in the contributing overview.
- **B — The connection.** The migration, the two instance settings, `PUT`/`GET`/`DELETE
  /invoices/bank-feed`, the accounts and their switches with the format change, the
  Integrations page. Docs: `admin/payments.md` and `admin/configuration.md` in `en/` and
  `nb/`; `reference/invoices.md`.
- **C — The sync.** The worker, the batch, matching by no user, the status table, the
  attention entries, *Fetch now*, the Bank files list. Docs: `user/invoices.md` and
  `admin/payments.md` in `en/` and `nb/`; `reference/invoices.md`; the roadmap's Later
  line for "a bank API for payments" moved to done.
- **D — The public site.** The Invoices page in `vantigo-landing` says payments from a
  Folio account register themselves, in English and Norwegian, written from the user
  guide pages of C. Its own pull request, linked to C's.

## Testing

- The adapter against the fake: the mapping of R3, direction and kind, each error class,
  a response past the size bound, a redirect not followed.
- The batch: refused whole on each rule of F5 step 2; a re-read overlap stores nothing;
  two identical payments on one day are two lines; outgoing, card and internal
  transactions leave only counts and are absent from the stored document.
- The cutover: an account moved from `camt054` to `feed` holds back lines on or before
  the cutover; a file uploaded for a feed account is refused.
- Matching by no user: the payment, the events and the `deadline_met` waiver; the checks
  refuse a `bank_feed` payment with a user and a file payment without one.
- The worker on a pool of one, and the boot test on a pool of four, as the other lease
  workers; a manual sync and the worker claiming the same account.
- Races: a sync against an apply from the queue on the same invoice.

## Open questions

For the owner:

1. **Folio first, or the port with files delivered by SFTP first?** Folio serves small
   companies; larger sellers get camt.054 from their bank. This design assumes Folio is
   worth being first because its API exists today and needs no agreement.
2. **The default interval**, 30 minutes: slower is kinder to an API with no stated limit,
   faster shows a payment sooner.
3. **A returned payment** stays registered until a person removes it (F6). Acceptable for
   a first version?
4. **Do we have a Folio account to record delivery A's response from?**

For Folio (`hei@folio.no`), before delivery A ends:

5. Can a payer pay a Folio account with a KID, and does it need an agreement (R8)?
6. How is an incoming transaction told from an outgoing one (R4)? Is a returned payment
   marked?
7. Is there a rate limit, and a test environment?
8. Can a booked transaction change or disappear, and how far back can a new one be booked?
9. The terms for an OAuth client, for the Later list.
