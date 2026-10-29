---
title: Platform packages
description: The packages under the server's internal/ that every module may use — the secrets box that seals and MACs values under keys derived from APP_SECRET, the mail package's outbound message, its calendar part and its rendering as MIME, its reading of a received message and the sanitising of its HTML, the payments port with its Vipps MobilePay adapter, and the public-holiday calendar.
sidebar:
  order: 5
sources:
  - apps/server/internal/secrets
  - apps/server/internal/mail
  - apps/server/internal/payments
  - apps/server/internal/holidays
  - apps/server/internal/imagemeta
---

A module may import the platform but never another module (see
[Module boundaries](/en/contributing/module-boundaries/)). This page describes the
platform packages a module reaches for when it holds something it must not store in
clear, when it sends a mail of its own, when it takes a payment, when it counts days
around the public holidays, and when it stores a picture people will see.

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
| `calendar/rsvp-link` | an outside attendee's answer link, sealed so every later mail carries the same link ([the answer by link](/en/reference/calendar/#the-token-and-the-link)) |
| `calendar/feed-address` | a person's calendar address, sealed so the settings screen can show it again; the request is matched by the token's plain hash ([the feed](/en/reference/calendar/#the-address-and-its-two-variants)) |
| `calendar/feed-busy-uid` | the MAC of a person's id and a projected item's kind and id, cut to 32 hex digits, as that item's `UID` in the busy feed — opaque, stable per person and item, and never stored, so a rotation changes the `UID`s rather than orphaning anything ([the feed](/en/reference/calendar/#the-icalendar-object)) |
| `staff/national-id` | a person's national identity number (sealed) |
| `staff/national-id-index` | the MAC of a person's national identity number, in a unique column |
| `staff/bank-account` | a person's bank account (sealed) |

**A purpose either seals or MACs, never both**, so no key both encrypts and
authenticates. A MAC purpose that indexes a value carries the suffix `-index`: a module
that seals a value and indexes it uses two purposes, `module/purpose` for the sealed value and
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

## The mail package

[`apps/server/internal/mail`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/mail)
sends the installation's own mail through the SMTP configuration of
[Email delivery](/en/admin/authentication/#email-delivery), behind its guard against
private destinations. `mail.Message` is identity's three-field application mail;
`mail.Outbound` is a full message — recipients, both bodies, a display name, a
Reply-To, threading headers and attachments — which Communications sends through each
channel's own SMTP configuration, and Invoices, Staff and Calendar through the
installation's, by the seam `Deps.SMTPSend` (or `mail.SendOutbound` when it is unset).

`mail.RenderMIME(from, out, opts...)` renders an `Outbound` as the RFC 5322 message
`SendOutbound` would send from `from` — the same headers, the `Message-ID`, `In-Reply-To`
and `References` the caller set, the same parts — and answers its bytes instead of
dialling, for a provider that takes a message whole rather than speaking SMTP:
Communications' [Microsoft 365 channels](/en/reference/communications/#delivery-semantics),
whose bytes Entra posts to Graph. It refuses an `Outbound` with no recipient, as
`SendOutbound` does. **The bytes carry no `Bcc:` header** unless the caller passes
`mail.WithBcc()`. go-mail's writer (v0.8.1) never writes one: over SMTP the blind copies
travel in the envelope's `RCPT TO`, never in the message. A provider that reads its
recipients from the headers has no envelope, so it would drop them; `WithBcc` adds the
header once, the addresses comma-joined as go-mail renders them, for a provider that
reads it — Graph's MIME draft — while the caller also hands the blind copies over beside
the bytes. Without blind copies the rendering has the headers and parts of the message SMTP
sends.

`Outbound.Calendar`, a `*mail.CalendarPart{Method, Body}`, carries an iCalendar object
the iMIP way: as the **third alternative** after the text and the HTML,
`text/calendar; method=<Method>` (go-mail completes it with `charset=UTF-8`), and
attached again as `application/ics` — `invite.ics` for a `REQUEST`, `cancel.ics` for a
`CANCEL`, the only two methods it admits. `Method` must be the `METHOD` the body
declares; the body is the whole `VCALENDAR`, CRLF line endings and all, carried as
given. Calendar's invitations are the one user
([invitations](/en/reference/calendar/#the-message)).

**Reading received mail.** `mail.ParseInbound(raw, limits)` reads an RFC 5322 message
into a `mail.Inbound`: `MessageID` and the first `In-Reply-To` bracketed, `References`
bracketed, distinct and oldest first (at most 100, the first and the newest kept; each id
header read for at most 400 ids, and an id with a control character counts as missing),
the `From`, `To` and `Cc` addresses in lower case (one over 320 characters or with a
control character dropped, at most 100 each in `To` and `Cc`), the subject RFC 2047-decoded (at most 998
bytes), `AutoSubmitted` (`Auto-Submitted` other than `no`, `X-Auto-Response-Suppress`,
`Precedence` `bulk`/`auto_reply`/`list`, `X-Autoreply`), the text and HTML bodies in UTF-8,
**unsanitised**, and the attachments with their file names, types, `Content-ID`s and
whether they are inline. It is built on `net/mail` and `mime/multipart`, with
`quoted-printable`, tolerant `base64`, RFC 2231 file names in any charset — which
`mime.ParseMediaType` drops for a charset other than UTF-8 or US-ASCII, so the package
reads those parameters itself — and every charset of `golang.org/x/text`'s `htmlindex`.
`mail.InboundLimits` bounds it — `DefaultInboundLimits` is 200 parts, 10 levels, 100
attachments and 1 MiB for each body (a longer text cut with `…`, a longer HTML dropped and
`HTMLDropped` set); a damaged part is skipped and counted; no header block, or neither a
`From` nor a body, is `mail.ErrUnparseable`, whose text holds nothing of the message.
A forwarded `message/rfc822` and a `winmail.dat` are kept as attachments, not opened; a
part with no `Content-Type` is `text/plain`; no string of the result holds a NUL.
`mail.NormalizeMessageID` is the bracketed, trimmed form both the parser and a caller
compare ids in.

`mail.SanitizeHTML(html, inlineURL)` answers a received message's HTML safe to show, on
[bluemonday](https://github.com/microcosm-cc/bluemonday)'s UGC policy after a pre-pass over
`golang.org/x/net/html`'s tree: `cid:` images rewritten through `inlineURL` (removed when
it answers none), every other image but a base64 `data:` PNG, GIF, JPEG or WebP removed,
`background` and `srcset` dropped, links and image map areas only `http`, `https` and
`mailto` — no relative URL kept but the inline image's download path, carried through
the policy as a `vantigo-inline:` placeholder —, links with
`rel="nofollow noreferrer noopener"` and `target="_blank"`, and a short allow-list of
typographic and box styles — never `url(…)`, `position` or `expression`. `<script>`,
`<style>`, `<iframe>`, `<object>`, `<embed>`, `<form>`, `<input>`, `<meta>`, `<base>`,
`<link>`, `<svg>`, `<math>` and every `on*` attribute go. `mail.TextFromHTML(sanitized)`
is a plain text of it — blocks and `<br>` as line breaks, list items as `- `, white space
collapsed — for a message that has only HTML. Communications'
[receiving](/en/reference/communications/#receiving) is the one user. The package's corpus
of hand-built messages is `testdata/mime/`, each with its expected reading beside it.

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

## The holidays

[`apps/server/internal/holidays`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/holidays),
the public-holiday calendar, is the one place the server knows which days are public holidays; a module that
counts business days counts them with it. Invoices moves a due date and a reminder
letter's deadline off them ([The rules](/en/reference/invoices/#the-rules)); Staff
counts every working day it reckons — the holiday account, the payroll totals, the
sick-leave rate — with `BusinessDay`, and lists the calendar's holidays with
`PublicHolidays` ([Absences](/en/reference/staff/#absences)). It
is a set of pure functions over the standard library's `time` package: it holds no
data, has no table and no migration, and reads no clock — its own purity test fails
on any other import or on a call such as `time.Now`.

**A day is a civil date at UTC midnight.** `DayIn(t, loc)` is the one derivation of
such a day from an instant — the calendar day `t` falls on in `loc`, made
`time.Date(y, m, d, 0, 0, 0, 0, time.UTC)` — so day arithmetic never meets a
daylight-saving shift and two callers agree about what a day is. The business
calendar (`contracts.BusinessCalendar`, identity's) answers `Today` and `Day` through
it in the workspace's business time zone, and Staff and Expenses, which each wrote
the same function for themselves, now mean this one. A time that is not a UTC
midnight is read by the holiday functions as its civil date in its own location —
10:00Z or midnight in Oslo on 17 May is 17 May.

| Function | Answers |
|---|---|
| `DayIn(t, loc) time.Time` | the calendar day `t` falls on in `loc`, at midnight UTC: 22:30Z on 30 June is 1 July in Oslo. |
| `PublicHoliday(d) bool` | whether `d` is a Norwegian public holiday: New Year's Day, Maundy Thursday, Good Friday, Easter Sunday and Monday, 1 May, 17 May, Ascension Day, Whit Sunday and Monday, Christmas Day and Boxing Day — whatever weekday it falls on. Christmas Eve and New Year's Eve are not; an ordinary Sunday, a helligdag in law too, is not listed but left to `BusinessDay`'s weekend test. |
| `EasterSunday(year) time.Time` | the Gregorian Easter Sunday of the year (the anonymous Gregorian algorithm), from which the movable feasts are counted. |
| `BusinessDay(d) bool` | whether `d` is neither a Saturday, a Sunday nor a public holiday. |
| `NextBusinessDay(d) time.Time` | `d` when it is a business day, else the first business day after it. |
| `PublicHolidays(from, to) []time.Time` | the public holidays from `from` to `to`, both counted, in date order and each day once (Whit Monday can fall on 17 May); none when `to` is before `from`. |

The calendar is **Norway's only**. A region beyond it would be a later column on a
module's settings, passed in by the caller; nothing in the package reads a setting.

## The image metadata stripper

`internal/imagemeta` strips a picture's metadata before a module stores or serves it:
a phone's photo carries where and when it was taken, the device and often a thumbnail
of the uncropped picture, and none of it is the person's face. Staff strips HR's photo
of a person before it reaches the object store, and identity strips a user's own avatar
and the managed one Staff sets, since Staff serves either to the person's colleagues.

| Function | What it does |
| --- | --- |
| `Strip(contentType string, data []byte) ([]byte, error)` | for `image/jpeg`, walks the segments from the start-of-image marker and drops APP1 (Exif, XMP) and APP13 (IPTC), copying everything from the start-of-scan marker on as it is; for `image/png`, walks the chunks and drops `eXIf`, `tEXt`, `zTXt` and `iTXt`, stopping after `IEND`; a file without metadata comes back byte for byte. |
| `ErrStructure` | the segments or chunks do not hold together — a truncated or foreign file — which a caller refuses as it refuses a bad image. |

The pixels are never decoded, so a decompression bomb costs the caller nothing here;
judging the type and the dimensions is the caller's (`image.DecodeConfig`, which reads
the header alone). The package imports the standard library only, and any module may
import it.
