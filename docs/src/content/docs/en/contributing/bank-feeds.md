---
title: Bank feeds
description: The bank feed port internal/bankfeed — the Feed interface, accounts, transactions with their direction and kind, the two typed errors — Folio's adapter and how it reads a direction Folio does not answer, the foliotest fake and the hand-written fixtures, and how to add a bank.
sidebar:
  order: 7
sources:
  - apps/server/internal/bankfeed
---

[`apps/server/internal/bankfeed`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/bankfeed)
is the port between a business module and a bank that answers an account's transactions
through an API, shaped like
[the payments port](/en/contributing/platform-packages/#the-payments-port): interfaces and
values only, no SQL — each consumer keeps its connections, accounts and lines in its own
schema (module boundaries rule 4) — importing no module, only the standard library and
`internal/buildinfo`. It is in depguard's platform set, and its purity test fails a build
that imports anything else. Invoices is to read incoming payments through it; nothing
imports it yet. The design is
[the bank feed design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-13-bank-feed-folio-design.md).

## The port

`bankfeed.Feed` is one bank credential:

| Method | Does |
| --- | --- |
| `Verify(ctx)` | The cheapest authenticated call, for a settings page |
| `Accounts(ctx)` | The accounts the credential can read: number, name and kind — `operational`, `tax`, `savings`, `earmarks`, `card`, or `other` for a kind the adapter does not know |
| `Transactions(ctx, account, from, to)` | The booked transactions of one account, both days inclusive, each with the bytes the bank sent for it |

A `Transaction` is the bank's id, the booking day at UTC midnight, the amount in minor
units (unsigned) with its currency, a `Direction` (`credit`, `debit`, `unknown`), a `Kind`
(`transfer`, `card`, `internal`), the KID, the remittance text, the debtor's name and
account, and `Raw`. **The adapter decides direction and kind; the module decides what a
line means.** An adapter cuts nothing — lengths are the module's — and answers
`unknown` rather than guess, which the module refuses.

**Errors.** `ErrUnauthorized` (a 401 or 403) and `*ErrRateLimited{RetryAfter}` (a 429).
Anything else is a plain error: the read failed and nothing was learnt. **An adapter never
retries**: a read has no side effect, and the consumer's next cycle reads again.

## Folio's adapter

`bankfeed/folio` speaks [Folio's API](https://api.folio.no/v2/api.yml):
`folio.New(Options{BaseURL, APIKey, Transport, Now})`. The base URL is the operator's
(`https://api.folio.no/v2`), absolute http or https; the API key is a customer's, sent as
`Authorization: Bearer`, with `User-Agent: vantigo/<release>`. A call is bounded by thirty
seconds, an answer by 10 MiB, and a redirect is never followed — it would send the key to
a host no operator named. Nothing is retried or cached.

| Port | Folio |
| --- | --- |
| `Verify` | `GET /accounts`, the answer discarded |
| `Accounts` | `GET /accounts`: `accountNumber`, `name`, and `type` — `Operational`, `Tax`, `Savings`, `Earmarks`, `Card` onto the kinds of the same names, anything else onto `other` |
| `Transactions` | `GET /accounts/{accountNumber}/transactions?startDate=&endDate=`, both `YYYY-MM-DD`; `transactions.booked[]` |
| `ID` | `id` |
| `BookedOn` | `bookingDate` |
| amount and currency | `transactionAmount.amount` and `.currency` — never `currencyAmount`, the original currency's |
| `KID` | `kid`, trimmed |
| `Text` | `description` |
| debtor | `debtor.name` and `debtor.accountNumber`, as written |
| `Kind` | `internal` when `creditorEventId` is present (both sides of a transfer between the customer's own accounts carry it), else `card` when `cardAuthorization` is, else `transfer` |
| `Direction` | read from the parties, below |
| `Raw` | the element of `booked[]` as Folio sent it |

**Direction.** Folio answers no sign and no credit or debit marker. The adapter compares
the account asked for with `creditor.accountNumber` and `debtor.accountNumber`, each
without spaces and a Norwegian IBAN (`NO`, two check digits, eleven digits) read as its
eleven-digit BBAN: `credit` when the creditor's is the account and the debtor's is not,
`debit` the other way, `unknown` when neither or both are — a party or its account may be
absent. One transaction Folio writes in a form the adapter does not read therefore stops
the account's sync, and never registers a wrong payment.

**The amount** must be digits, a point and exactly two decimals, and is read to øre
without a float. Anything else — three decimals, a sign, an amount past `int64` — as well
as a transaction without its id, booking day or currency, fails the whole call: the
module refuses the batch rather than store part of it.

## Testing

`bankfeed/folio/foliotest` is an `httptest` TLS Folio for tests only (depguard keeps it out
of production code). `SetAccounts` sets what `GET /accounts` answers; `AddTransaction`
books a transaction on an account, answered in the order added and filtered by
`startDate` and `endDate` (today by `SetNow`'s clock when absent); an unknown account is a
404 and any key but `foliotest.APIKey` a 401. `Fail(op, Failure)` answers one operation —
`OpAccounts` or `OpTransactions`, and only that one — with a status and `Retry-After`, a
reset connection, a redirect, a body of the test's own, or `Oversized`, one byte past the
adapter's bound, for `Times` requests or until `Clear`. `Requests()` is every request
received. A harness passes its `Transport()` as `Deps.HTTPTransport`.

**The fixtures are hand-written.** `bankfeed/folio/testdata/accounts.json` and
`transactions.json` are written from the schemas of Folio's OpenAPI document, not
recorded, and each says so: an incoming transfer with a KID and one without, an outgoing
payment, a card purchase, both directions of an internal transfer, an incoming payment to
the account written as an IBAN with spaces, and one whose creditor has no account. Folio
has no test environment, and no response has been read from a real account yet. Until one
is, three things are unverified: that `creditor.accountNumber` is the account asked for on
an incoming payment, in which form, and whether `kid` arrives on a payment made with one.
A mismatch shows as the account's sync refused for an unknown direction, never as a wrong
payment. When a real response is read, replace the fixtures with it, the customer's names
and numbers changed, and say so in their comment.

## Adding a bank

1. Add the adapter beside Folio's, `internal/bankfeed/<bank>`, implementing
   `bankfeed.Feed`. Map refused credentials to `ErrUnauthorized` and a rate limit to
   `ErrRateLimited`, and leave everything else a plain error. Decide direction from what
   the bank answers, and answer `unknown` whenever it is not certain. Follow no redirect,
   bound the call and the answer, and never retry.
2. Add its fake, `internal/bankfeed/<bank>/<bank>test`, to depguard's `test-fakes` set in
   `apps/server/.golangci.yml`, and both directories to `TestBankfeed_Purity`'s list.
3. Write its fixtures from a recorded response when there is one, and say in each which
   they are.
4. Where the consumer names the provider — in Invoices, the provider value its schema's
   check allows and the contract's description of it — add the bank's.
5. Document the bank here, and wherever the consumer's user and administration guides
   name Folio (both languages).
