---
title: Platform packages
description: The packages under the server's internal/ that every module may use — the secrets box that seals and MACs values under keys derived from APP_SECRET, the payments port with its Vipps MobilePay adapter, and the public-holiday calendar.
sidebar:
  order: 5
sources:
  - apps/server/internal/secrets
  - apps/server/internal/payments
  - apps/server/internal/calendar
---

A module may import the platform but never another module (see
[Module boundaries](/en/contributing/module-boundaries/)). This page describes the
platform packages a module reaches for when it holds something it must not store in
clear, when it takes a payment, and when it counts days around the public holidays.

## The secrets box

[`apps/server/internal/secrets`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/secrets)
is the one place the server turns `APP_SECRET` into keys. A module receives the box as
`Deps.Secrets` and never sees a key.

The box derives one 32-byte key per **purpose** with HKDF-SHA256: `APP_SECRET` is the
input key material, the salt is fixed, and the purpose string is the HKDF info. A key
is derived once and cached for the life of the process. Two operations use it:

- **`Seal` and `Open`** encrypt with AES-256-GCM under the purpose's key, with a fresh
  random nonce each time and the purpose bound in as additional authenticated data, so
  a value sealed for one purpose cannot be opened under another. The output starts
  with a key-id byte, `0x01` today, which leaves room for a rotation scheme. `Open`
  never says why it failed. `SealString` and `OpenString` are the same operations for
  text contexts such as cookies.
- **`MAC`** returns the HMAC-SHA256 of the data under the purpose's key: 32 bytes,
  the same every time for the same purpose, data and `APP_SECRET`. Sealing is
  randomised, so two seals of one value never match; a MAC is what lets a sealed
  value be found, or refused as a duplicate by a unique column, without opening
  anything. Compare two MACs with **`MACEqual`**, which takes the same time wherever
  they differ. The only error `MAC` returns is the key derivation's.

### Purposes

A purpose is a constant the calling module owns, never input from a request, written
`module/purpose`:

| Purpose | Used for |
|---|---|
| `identity/totp` | a user's TOTP secret |
| `identity/oidc-state`, `identity/oidc-external` | the OIDC flow's state and external-identity cookies |
| `communications/channel-smtp-credential` | a channel's SMTP password |
| `invoices/access-point-credential` | the e-invoicing access point's API key |
| `staff/national-id` | a person's national identity number (sealed) |
| `staff/national-id-index` | the MAC of a person's national identity number, in a unique column |
| `staff/bank-account` | a person's bank account (sealed) |

**A purpose either seals or MACs, never both**, so no key both encrypts and
authenticates. A MAC purpose carries the suffix `-index`: a module that seals a value
and indexes it uses two purposes, `module/purpose` for the sealed value and
`module/purpose-index` for its MAC — `staff/national-id` and
`staff/national-id-index` — and pins the two constants distinct in a test.
When one MAC purpose covers values of several kinds, the caller separates them in the
input itself — kind, then country, then value, with a separator none of them can
contain — so the same characters under two kinds do not collide. The form of that
input is part of every MAC stored: changing it orphans them all.

### Rotating APP_SECRET

There is no key ring. Every key the box derives depends on `APP_SECRET`, so changing it
makes every sealed value unopenable **and** every stored MAC unmatchable: a lookup by
MAC finds nothing, and a unique MAC column no longer refuses a duplicate of a value
written before the change. Nothing re-derives or re-indexes on its own. A rotation
therefore needs a migration that opens and re-seals every sealed column and
recomputes every MAC column under the new secret, with both secrets at hand; until
that exists, `APP_SECRET` is not rotated in place. The operator's side of this is in
[Authentication → Key material](/en/admin/authentication/#key-material).

A module that adds a purpose names it in the table above, and a module that stores
sealed values or MACs says on its own administration page what a rotation costs its
data.

## The payments port

[`apps/server/internal/payments`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/payments)
is the port between a module and a payment provider, shared by Invoices and a later
Point of sale so both use the same adapters. It holds interfaces and values only:
no SQL — each consumer keeps its attempts and payments in its own schema (module
boundaries rule 4) — and it imports nothing but the standard library and
`internal/buildinfo`, which `TestPayments_Purity` and depguard's `platform` list
enforce.

### The interface

`payments.Provider` is one provider account (for Vipps, one sales unit):
`Create` a payment request under the caller's reference — `FlowRedirect` with a return
URL, or `FlowQR` answering the QR's target URL — which the caller may keep, never
logged, to show the same code again — and its code as SVG — `Get` its state and its four
amounts (authorized, captured, cancelled, refunded), `Capture`, `Cancel` and
`Refund`, `CaptureTime` (the first capture's time from the provider's event log),
`VerifyWebhook` and `Verify`, the cheapest authenticated call for a settings page.

**Amounts cross the port in minor units only**: `payments.Money{Currency, Minor}`,
øre for NOK. A module keeps its exact decimals inside and converts at the call,
refusing anything that is not a whole number of øre.

### Errors and "outcome unknown"

An adapter answers typed errors: `ErrUnauthorized` (the keys), `ErrNotFound`,
`ErrConflict{Code}` (a 409, or a reference or key already in use), `ErrRefused{Code}`
(a 400), `ErrThrottled{RetryAfter}` (a 429 or 423; the Vipps adapter reads `Retry-After`
as seconds or an HTTP date and answers at most a day, so a huge or overflowing header
is never a wait without end), `ErrInsufficientFunds` and
`ErrCaptureFailed` (a capture that took nothing — **not final**: Vipps asks for a retry,
and the caller decides when to capture again and when to give up),
`ErrInvalidSignature`, and `ErrInvalidRequest` — a request the adapter
refused **before sending it** because it breaks a documented rule of the provider's.
Anything else — a transport failure, a timeout, a 5xx, a redirect, an answer that
does not decode, a payment answered without one of its four amounts or in another
currency — is a plain error meaning **the outcome is unknown**: the request may
have taken effect, and the caller learns which by reading the payment again.

What is final depends on the operation, so there is no generic predicate, only one per
operation: `IsDefinitiveCreateRefusal` (refused keys, a request never sent, or a 400
naming a code — no payment exists, the attempt may fail) and `IsCaptureFailure`
(`ErrInsufficientFunds` or `ErrCaptureFailed` and nothing else). A conflict is never a
definitive refusal: "the reference already exists" may be this very request, made by
an earlier try whose answer was lost — the Vipps adapter reads the payment back then
(4150, 4020) and answers it as created only when it is the one asked for (the same
amount and currency) and still `CREATED` or `AUTHORIZED`. A QR is never read back —
Vipps does not promise that a read's redirect URL is the QR's target — so the outcome
is unknown and the caller polls the reference. A 409, a 401 or a
404 on a capture is **not** a failed capture: its money may have moved; and Vipps'
capture codes count only on the 400 it documents them with.

### Idempotency keys and retries

The `Idempotency-Key` on create, capture, cancel and refund is **the caller's**: it
persists the key with the operation's row and reuses it on every retry, so a timeout
after the provider accepted cannot become a second payment or capture. An adapter
never makes one up and refuses a call without one. It **never retries** either —
not even the transport's own replay of an idempotent request on a broken connection,
which the Vipps adapter turns off. The one repeat is a call answered 401, made once
more under a freshly fetched token.

### The Vipps MobilePay adapter

`internal/payments/vipps` speaks the ePayment API: `vipps.New(Options{BaseURL,
ClientID, ClientSecret, SubscriptionKey, MSN, SystemVersion, Transport, Now})`. It
fetches the access token from `/accesstoken/get`, caches it until a minute before it
expires by `Now` (one fetch at a time), and sends on every call the subscription key,
the `Merchant-Serial-Number` and the four `Vipps-System-*` headers (`vantigo`,
`vantigo-invoices`, the release). It refuses before any request an amount outside
1.00–650 000.00 NOK, a reference outside `^[a-zA-Z0-9-]{8,64}$` and a description
outside 3–100 characters, and a return URL or legal link that is not https or is
longer than 2500 characters. A call is bounded by thirty seconds and never follows a
redirect. A QR is asked for as Vipps' target URL (`TEXT/TARGETURL`) and rendered here
by `internal/payments/qrsvg` — the one QR renderer, which a module's own QR codes use
too — so the adapter never fetches a URL an answer names. The SVG holds only `svg`,
`rect` and `path`, never the encoded text; a page shows it as an image,
`<img src="data:image/svg+xml;base64,…">`, never inlined into its markup.
`RegisterWebhook` and `DeleteWebhook` manage a webhook registration for
`vipps.Events`, the eight ePayment events. **Invoices uses none of the webhook calls
yet**: it registers no webhook and receives none, and its worker polls
([Vipps payments](/en/reference/invoices/#vipps-payments)); they and the verification
below are kept for the webhook receiver that follows.

**Webhooks are verified against the registered URL**, never the request line: the
content hash of the raw body against `x-ms-content-sha256`, then HMAC-SHA256 under the
secret over `POST\n<path and query>\n<x-ms-date>;<host>;<content hash>`, where the
path, query and host are those of the URL the webhook was registered at — a reverse
proxy may rewrite the request line, and what Vipps signs is the URL it was given.
The `x-ms-date` must be an RFC 1123 date at most eight days old — Vipps retries a
callback for seven — and at most five minutes ahead, so a captured callback cannot be
replayed for ever.
Vipps' published test vector's signature does not reproduce, so the hash step is
pinned to that vector (`testdata/webhook-vector.json`), the signature step to a
self-generated one (flagged as such) and, once captured, to a real callback in
`testdata/webhook-captured.json`, which does not exist yet — its test skips without it.

### Testing against it

`internal/payments/vipps/vippstest` is an `httptest` TLS server speaking the token,
ePayment and Webhooks APIs, for every suite that touches a payment: states scripted
per reference, the test environment's special amount 151 øre (insufficient funds),
any status or error code on demand, a reset connection, idempotency by key, partial
captures, a refund made in the business portal and a signed webhook posted to a URL
the test gives. A harness passes its `Transport()` as the module's HTTP transport.
Only tests import it: depguard's `test-fakes` rule refuses it in any other file.

The tagged test `vipps_test_env_test.go` (`//go:build vipps`) runs the adapter
against Vipps' own test environment:

```sh
cd apps/server
go test -tags vipps -run TestVippsTestEnv ./internal/payments/vipps/
```

It needs a test sales unit's five variables — `VIPPS_TEST_CLIENT_ID`,
`VIPPS_TEST_CLIENT_SECRET`, `VIPPS_TEST_SUBSCRIPTION_KEY`, `VIPPS_TEST_MSN` and
`VIPPS_TEST_PHONE` (a test user who has approved one payment in the test app by hand)
— and skips without them; `VIPPS_TEST_BASE_URL` overrides
`https://apitest.vipps.no`. It creates, force-approves, captures (and repeats the
capture under the same key), refunds part, reads the event log, cancels a second
request, tries 151 øre (logging the HTTP status Vipps really answers its capture
with) and a QR. With `VIPPS_TEST_WEBHOOK_URL` — a public https URL
forwarded to `VIPPS_TEST_WEBHOOK_LISTEN` (default `127.0.0.1:8089`) — it also registers
a webhook, captures one signed callback, verifies it, writes it as the fixture and
deletes the registration.

## The calendar

[`apps/server/internal/calendar`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/calendar)
is the one place the server knows which days are public holidays; a module that
counts business days counts them with it. Invoices moves a due date and a reminder
letter's deadline off them ([The rules](/en/reference/invoices/#the-rules)); Staff
counts every working day it reckons — the holiday account, the payroll totals, the
sick-leave rate — with `BusinessDay`, and lists the calendar's holidays with
`PublicHolidays` ([Absences](/en/reference/staff/#absences)). It
is a set of pure functions over the standard library's `time` package: it holds no
data, has no table and no migration, and reads no clock — its own purity test fails
on any other import or on a call such as `time.Now`.

**A day is a civil date at UTC midnight.** The caller derives it in its own zone —
the installation's calendar day, made `time.Date(y, m, d, 0, 0, 0, 0, time.UTC)` — so
day arithmetic never meets a daylight-saving shift. A time that is not a UTC midnight
is read as its civil date in its own location — 10:00Z or midnight in Oslo on 17 May
is 17 May.

| Function | Answers |
|---|---|
| `PublicHoliday(d) bool` | whether `d` is a Norwegian public holiday: New Year's Day, Maundy Thursday, Good Friday, Easter Sunday and Monday, 1 May, 17 May, Ascension Day, Whit Sunday and Monday, Christmas Day and Boxing Day — whatever weekday it falls on. Christmas Eve and New Year's Eve are not; an ordinary Sunday, a helligdag in law too, is not listed but left to `BusinessDay`'s weekend test. |
| `EasterSunday(year) time.Time` | the Gregorian Easter Sunday of the year (the anonymous Gregorian algorithm), from which the movable feasts are counted. |
| `BusinessDay(d) bool` | whether `d` is neither a Saturday, a Sunday nor a public holiday. |
| `NextBusinessDay(d) time.Time` | `d` when it is a business day, else the first business day after it. |
| `PublicHolidays(from, to) []time.Time` | the public holidays from `from` to `to`, both counted, in date order and each day once (Whit Monday can fall on 17 May); none when `to` is before `from`. |

The calendar is **Norway's only**. A region beyond it would be a later column on a
module's settings, passed in by the caller; nothing in the package reads a setting.
