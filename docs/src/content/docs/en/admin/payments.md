---
title: Payments from the bank
description: The bank agreement that gives you OCR giro or camt.054 files of incoming payments, where to download them, and the format each account's files are imported in.
sidebar:
  order: 43
sources:
  - apps/server/internal/invoices/bankfile
  - apps/server/internal/invoices/bankimport.go
  - apps/server/internal/invoices/bankaccounts.go
  - apps/server/internal/invoices/reminder_worker.go
  - apps/server/internal/invoices/vipps_settings.go
  - apps/server/internal/invoices/payment_settings.go
  - apps/server/internal/invoices/payment_worker.go
  - apps/server/internal/payments
  - apps/server/internal/security/publichost.go
---

Vantigo reads the bank's own record of the money that arrived on the seller's account: a
file of incoming payments, downloaded from the online bank and imported in Invoices by
someone with `invoices:payments`. Each payment in it carries the KID printed on the
invoice, which is how a payment finds its invoice: right after the import, each payment
carrying the KID of an issued invoice, paid to the account that invoice printed, is
registered against it, by the person who imported the file; the rest are kept for a
person to handle. This page is the operator's side: the
agreement to ask the bank for, where the files are, the format each account is imported
in, and what can go wrong. The rules of the import are in
[the reference](/en/reference/invoices/#bank-files-and-the-exception-queue).

Vantigo takes two formats:

- **OCR giro** — the 80-character records of an OCR/KID agreement, produced by Mastercard
  Payment Services for every Norwegian bank. It carries the payments made **with a valid
  KID**, and nothing else.
- **camt.054** — the ISO 20022 notification of incoming payments (versions `.001.02` and
  `.001.08`), under an agreement for all incoming payments. It carries every credit to the
  account, with or without a KID.

camt.053, the account statement, is not enough: with a payment agreement it shows lump
sums, not who paid what.

## The bank agreement

1. **Agree a KID with the bank.** Ask for an OCR/KID agreement — "Fakturere med KID" or an
   "innbetalingsavtale" — on the account in **Invoice settings**. The bank registers a KID
   length and a check digit method (MOD10 or MOD11) with Mastercard Payment Services;
   enter exactly that on the card **KID** in **Invoice settings**
   ([the KID agreement](/en/user/invoices/#agree-a-kid-with-the-bank)), so every invoice
   prints a KID the bank accepts.
2. **Choose the file you will import.** The OCR agreement gives you OCR giro files of the
   KID payments. For every incoming payment — a customer who pays without the KID, or
   with a wrong one, included — ask instead for an eGiro / camt.054 agreement for incoming
   payments ("Innbetaling Total" at some banks), which gives camt.054 files. One of the two
   is enough per account; importing both for the same account would read each KID payment
   twice.
3. **Consider tvungen KID** — a separate option on the OCR agreement under which the bank
   refuses a payment without a KID or with an invalid one (paper giro excepted). Your
   customers then cannot pay without the KID, so the OCR file misses nothing a customer
   paid; without it, a payment without a valid KID reaches the account but **not the OCR
   file**, and only the statement or a camt.054 file shows it.

The bank's prices for these agreements differ; DNB and Nordea charge a set-up fee, a
monthly fee and a fee per KID payment.

## Getting the files

Download the file of the days you want from the online bank, then import it in Invoices.
Vantigo does not fetch files from the bank by itself in this release.

- **DNB**: the corporate online bank's file transfer offers the OCR and the eGiro files,
  and a file can be ordered again under "File transfer – Order files" (at a fee).
- **Nordea**: the online bank's "hente fil" lets you fetch the OCR file.
- **SpareBank 1**: "Meny → Filer → Hent filer".

Whether Nordea's and SpareBank 1's online banks offer camt.054 as a manual download is
**uncertain**; ask the bank. Handelsbanken was not looked into.

The same file can be imported only once, and so can a file of the same identity (OCR's
sender, transmission number and recipient; camt.054's message id and creation time) —
the second is refused naming the first import, who made it and when. A file that
overlaps one already imported — two downloads covering the same days, a copy the bank
sends again — is imported, and the payments it shares with the earlier one are kept as
duplicates of them, never read twice.

## The format of each account

Each receiving account is imported in one format. **The first file imported for an
account sets it**: an OCR giro file makes the account `ocr`, a camt.054 file `camt054`. A
later file of the other format for that account is refused with
`bank_import_format_mismatch`, naming the account and its format, and nothing of it is
imported.

To switch an account — say from OCR to camt.054 when you take the agreement for all
incoming payments — someone with `invoices:manage` changes the account's format: in
**Invoices** → **Payments** (which opens only with `invoices:payments` too), under **Bank
accounts**, **Change the format** beside the account (or `PUT /api/v1/invoices/bank-accounts/{account}/format`). Vantigo then records the **cutover**: the latest booking day of that
account's payments read in the old format. Matching then holds back a payment of the new
format booked on or before the cutover as a possible duplicate, left for a person,
because the old format may already have brought it in. Make the switch once the last old-format file is
imported, and start the new format's files from the day after. A change of bank is a new
account, with its own format.

**Bank accounts** on the same page — or `GET /api/v1/invoices/bank-accounts` — lists every
account with its format, the previous format and cutover, and its latest file.

## Before you import

- **The accounts.** Every account a file names must be the seller's **Bank account** in
  **Invoice settings**, or one an issued invoice printed (so payments to an account the
  seller had before are still read); a Norwegian IBAN is read as its account number.
  Otherwise the file is refused with `bank_account_unknown`, naming the account's last
  four digits — check the settings, or that the file is this company's.
- **An object store** ([Object storage](/en/admin/object-storage/)). Every imported file is
  kept as it was uploaded, under `bank-files/<sha256>.ocr` or `.xml`, because it is the
  documentation of the payments booked from it (bokføringsloven § 10). Without a store an
  import is refused with `storage_unavailable` and nothing is read.

## What can go wrong

- **400 on the file.** It is not an OCR giro or camt.054 file, it is past 10 MiB or holds
  more than 5 000 payments, or it breaks its own rules — a control total that does not add
  up, a booking day after today, an amount not in NOK. The message names the record or
  the element. Nothing is imported: download the file again, and do not edit it by hand.
- **`bank_account_unknown`**, **`bank_file_duplicate`**, **`bank_import_format_mismatch`**:
  above.
- **What OCR leaves out.** An OCR giro file holds only payments with a valid KID; a
  customer who paid without one, or with one the bank rejected, is not in it. Without
  tvungen KID, look for such payments on the account statement, or import camt.054
  instead. Card payments (OCR's card information) are counted and ignored.
- **Payments left pending.** Matching runs right after the import, one payment at a
  time; if it stops early — a database error, or the upload's request ending — the file
  stays imported, the import's answer counts the rest as `pending`, and the stop is
  logged at warn with the file and the payment. Someone with `invoices:payments` matches
  the rest with **Match the rest** on the file in **Payments** (or
  `POST /api/v1/invoices/bank-files/{id}/match`), and is then the one registering them.
- **A negative assignment.** An OCR assignment whose payments net below zero — a reversal
  larger than the payments of the day — cannot be written in the format's end record;
  whether Mastercard Payment Services can send one at all is **uncertain**. A negative
  line within an assignment is read and kept apart for a person.
- **Payments kept for a person.** What matching could not place waits in the exception
  queue, at the foot of **Payments** (or `GET /api/v1/invoices/bank-transactions?status=exception`),
  each with its reason and, where it lacks a KID's invoice,
  suggestions. Someone with `invoices:payments` applies it to invoices, dismisses it with a
  note, confirms a duplicate or keeps it as a payment of its own
  ([the exception queue](/en/reference/invoices/#the-exception-queue)). A **reversal** —
  the bank taking a payment back — is never undone by itself: handle it, naming the
  payment it reverses, which is then removed, or saying why none is. After the switch of
  an account's format, the payments held back by the cutover are confirmed as duplicates
  there, or applied when they were not in the old format's files after all.

## The reminder worker and mail

Reminder letters to customers who take them by e-mail are sent by the
`invoices-reminders` background worker
([the worker](/en/reference/invoices/#the-worker)). It runs on every installation,
inside the API process or the dedicated worker container
([Background workers and scaling](/en/admin/installation/#background-workers-and-scaling)),
one letter at a time and at most one a second per instance; several instances share the
letters under a 60-second lease and never send one twice on purpose.

- **It needs the SMTP settings.** Letters go through the same `MAIL_DRIVER=smtp` and
  `SMTP_*` settings as everything else Vantigo mails, from `SMTP_FROM` under the seller's
  name, with replies to the e-mail in **Invoice settings**. Without mail (`MAIL_DRIVER`
  not `smtp`), a reminder run makes **every letter a paper letter**, with the warning
  `mail_unavailable`, and nothing is e-mailed.
- **Paper letters need no worker and no mail.** A person prints them in a batch for the
  day they will be posted and confirms the posting; the batch stores each letter's PDF
  through the same object store, the same way, and a PDF it could not store then is
  stored when the batch is next posted or downloaded
  ([paper and posting](/en/reference/invoices/#paper-and-posting)).
- **It needs an object store.** Each letter's PDF is stored once, before it is mailed,
  under `reminders/` ([Object storage](/en/admin/object-storage/)); a store that cannot
  be written is a failed attempt.
- **A failed attempt** — the mail server refusing, the store unreachable, a send that
  would not finish inside the lease — is retried after 2, 4, 8 … seconds, at most an hour
  apart, with the reason in the letter's `lastError`. A letter still unsent **48 hours
  after its first attempt** is **`failed`**: check the mail server and the store, then
  someone with `invoices:payments` presses **Send again** on the invoice's **Reminders**
  card or on the run's page, or withdraws it. Each failed letter is an item on the
  dashboard's **Needs attention** list; `GET /api/v1/invoices/reminders?status=failed`
  lists them.
- **A letter that waits.** A letter whose collection rates miss a half-year, or that
  would carry a fee past the regime review, is not attempted: it waits an hour at a time,
  its `heldReason` saying why, without ever failing. Add the rate, or review the regime,
  in the reminder settings, and it goes.
- **At least once.** If the process stops between the mail server taking a letter and
  Vantigo marking it sent, the next claim sends it again, with the same Message-ID, so
  the customer's mail program can tell it is the same letter. If the process instead stalls past the
  letter's 60-second lease in that window, someone may withdraw the letter meanwhile:
  it was mailed but is recorded `withdrawn`, and the worker logs a warning naming it.

## Vipps

Invoices takes Vipps MobilePay payments through the
[payments port](/en/contributing/platform-packages/#the-payments-port): pay links on
issued invoices, and payment on site. This is the operator's side of it; the rules are in
[the reference](/en/reference/invoices/#credentials-and-payment-settings).

**Preconditions:**

- The company has an agreement with Vipps MobilePay for integrated payments
  (*Integrert betaling*, the ePayment API) and a **sales unit** for it in the Vipps
  business portal. One sales unit serves the whole
  installation.
- The sales unit's API keys, from the portal's *For developers → API keys*: the client id, the
  client secret and the subscription key, beside the sales unit's number (MSN).
- For pay links, a public host for the pay page ([The public host](#the-public-host)).
  On-site payment needs none.

**Steps:**

1. Leave `INVOICES_VIPPS_ENABLED` at its default, on. `0` switches Vipps off on this
   installation: no request is ever sent to Vipps, the keys cannot be verified, and
   nothing Vipps offers is available — while the stored keys and settings are kept.
2. Leave `INVOICES_VIPPS_BASE_URL` at its default, `https://api.vipps.no`, for real
   payments. To try the whole flow first, point it at Vipps' test environment,
   `https://apitest.vipps.no`, with a **test** sales unit's keys — never production
   keys against the test environment, or test keys against production; a sales unit's
   keys work in its own environment only. Restart after changing either.
3. A manager (`invoices:manage`) enters the MSN and the three keys in the Invoices
   settings and verifies them. The keys are sealed by the secrets box under
   `invoices/payment-provider-credential`; they depend on `APP_SECRET`, as every sealed
   secret does.
4. A manager sets the payment settings: pay links (with the seller's sales terms and
   privacy notice URLs) and payment on site.

**Verify:** the verify answers *ok*. It fetches an access token with the keys and reads
the sales unit's list of webhooks, which needs the keys and the MSN together; Vantigo
registers no webhook — it polls ([the payments worker](#the-payments-worker)).

**The address Vipps answers.** Vantigo sends a payer, or encodes in an on-site QR
code, only an absolute `https` address that Vipps answers to a new payment. Any other
answer counts as an outcome not yet known: the pay page says Vipps could not start the
payment, and the payments worker reads the payment again.

**Rotating a key.** A new client secret for the same sales unit may be stored at any
time; enter only the secret, and the other two keys are kept. A change of the MSN, the
client id or the subscription key — another sales unit — is refused while a Vipps
payment is in progress, because finishing it needs the keys it was made with: wait until
the payments in progress are finished. Removing the keys is refused for the same reason.

**Storing keys again returns what was on to service.** While Vipps refuses the keys,
or they cannot be opened, nothing Vipps offers is available, but the payment settings
are left as they were. Storing good keys again — or a verify that succeeds — brings pay
links and on-site payment that are still switched on back into service at once, without
asking anyone to turn them on again; on-site payment keeps the kontantsalg
acknowledgement given when it was first turned on. Turn a setting off first if it should
not come back.

**On-site payment is kontantsalg.** A payment taken from a customer standing at the
counter is a cash sale, and the invoice is then a cash invoice (*kontantfaktura*) with the
rules that come with one — the buyer identified on it, and the sale in the cash records
of the day. Turning it on asks the manager to acknowledge that notice, and records who
did and when. Have your accountant or auditor read the notice before turning on-site
payment on in production.

**What can go wrong:**

- *Refused* on verify: Vipps does not accept the keys or the MSN — a key mistyped, a key
  from the other environment, or a sales unit without the ePayment product. Enter the
  keys again. While Vipps refuses them, meta reports it and nothing Vipps offers is
  available.
- *Unreachable* on verify: the network, a timeout, a Vipps outage or a throttle. Check
  that the server can reach `INVOICES_VIPPS_BASE_URL` over https, and try again.
- The keys cannot be opened: `APP_SECRET` changed since they were stored. Invoices logs
  it at error, flags the keys as refused and answers *payments unavailable*; enter all
  three keys again.

## The payments worker

The `invoices-payments` worker carries every Vipps payment from the moment the payer is
sent to Vipps to its end: it reads the payment until the payer approves or Vipps ends
it, captures what is open on the invoice, registers the payment, and releases what was
authorized and not captured. It runs while `INVOICES_VIPPS_ENABLED` is on, in the API
process or in the worker container ([Background workers and
scaling](/en/admin/installation/#background-workers-and-scaling)), polls every second and
handles every payment that is due in each cycle. The rules are in [the
reference](/en/reference/invoices/#capture-and-the-reservation).

**Webhooks are not supported yet.** Vantigo follows every Vipps payment by reading it
(polling); it registers no webhook with Vipps, and nothing needs to reach it from Vipps.
The public host's allowlist names no webhook path: a webhook path is added to it with
the webhook itself, never before.

**While a capture is being completed, its amount is reserved** on the invoice: a manual
payment, a bank file or a reminder cannot take it, and a refusal says a Vipps payment is
being completed. With `INVOICES_VIPPS_ENABLED=0` the worker does not run and no payment
moves on, but the reservations stay, each shown on the dashboard as a stranded
reservation — switch Vipps back on, or have a manager abandon each payment whose capture
was never sent after checking it in the portal. One whose capture was sent can be
abandoned only once Vipps can be read again.

**Abandoning a payment** (*Abandon* on the invoice's Vipps payment, `invoices:manage`)
takes a reason and the confirmation that the portal shows nothing captured. Only a
payment whose capture's outcome is unknown, one the worker cannot complete, or a request
it gave up on can be abandoned; any other is refused, and the refusal says why. Vantigo does
not take the confirmation on trust: wherever it can read Vipps it reads the payment
first. Money Vipps shows captured is registered as a payment of the invoice and the
abandon refused; a payment Vipps still holds capturable is refused until it is released
in the portal; a payment request Vipps knows and has not ended is handed back to the
worker. Only then is the attempt abandoned: its reserved amount released, who, when and
why recorded. When Vipps answers too slowly, or another manager handed the payment back
meanwhile, the worker may have taken it over: the abandon is refused — read the payment
again. A capture discovered later is registered by hand. The rules are in [the
reference](/en/reference/invoices/#abandon).

**Verify:** a test payment of a small invoice, approved in the Vipps app (or the test
environment's), is registered on the invoice within seconds as a Vipps payment.

**What can go wrong:**

- *A capture whose outcome is unknown* (`capture_unknown`). Vipps did not answer the
  capture in a way that says whether the money was taken, for seven days — an outage,
  keys refused, a throttle. The amount stays reserved, and the worker keeps reading the
  payment, hourly, for up to 180 days. Look the payment up in the Vipps business portal
  by its reference (`<prefix>-<invoice number>-<attempt id>`, shown on the invoice):
  captured there, it is registered at the next read once Vipps answers again; not
  captured, release the authorization in the portal and have a manager abandon the
  attempt, which releases the reservation. Check the keys first — a refused key
  (*Refused* on verify) leaves every capture unknown. It is on the dashboard as a capture
  of unknown outcome.
- *A failed capture* (`capture_failed`). Vipps refused the capture for lack of funds
  three times, or failed it and kept failing it until the capture's guarantee ran out or
  30 days after the authorization, or Vipps ended the payment. Nothing was taken; the invoice is open again and the customer pays another
  way. Should the money show as captured at Vipps after all — or a payment be captured
  in the portal — the worker registers it as soon as it reads it.
- *A payment's create that never settles.* When the create of a payment was never
  answered and sending it again keeps conflicting, the worker stops after ten tries and
  the payment's last error says to look the reference up in the Vipps portal. It shows
  on the dashboard as stranded, and on site it holds the invoice's one live payment
  request, so no new one can be made: a manager abandons it. Vantigo reads Vipps first —
  a payment Vipps does not know, or has ended, is abandoned; one it knows and has not
  ended is handed back to the worker, which completes it and registers anything
  captured.
- *A capture that cannot be registered.* The money is captured but the payment could
  not be written; the attempt keeps its reservation, its last error says why, and the
  worker tries again. It shows on the dashboard as a capture of unknown outcome until it
  is registered.
- *A release that never ends.* When Vipps refuses for good to release an amount it
  authorized and did not capture, the worker stops asking: the payment's last error says
  to release it in the Vipps portal. Such a payment still counts as in progress, so the
  sales unit and its keys cannot be changed until it is finished, and it shows on the
  dashboard as stranded. Once the rest is released in the portal, *Abandon* hands it
  back to the worker — the payment itself is registered and is never abandoned — which
  reads Vipps again and records the release.
- *The sales unit was changed.* A payment made under another sales unit than the one
  stored is never sent to the new one; it waits, its amount reserved and shown on the
  dashboard as stranded, until a manager abandons it — only while its capture was never
  sent, since Vantigo cannot read it under the new sales unit.
- *A refund made in the Vipps portal.* For 30 days after a capture the worker reads the
  payment once a day; a refund made in the portal is recorded and shown on the dashboard
  until someone with `invoices:payments` removes the payment (and registers the part
  kept, if any). Vantigo never removes a payment on its own, and a read that fails
  changes nothing.

## The public host

A customer pays an invoice with Vipps MobilePay from a **pay page**: the link printed on
the invoice opens it, and the page creates the payment only when the customer presses
the button, because a Vipps payment request lives ten minutes and an invoice's term is
weeks. The customer is on the internet, not signed in to Vantigo, so the pay page must
be reachable from the internet — while the rest of Vantigo, its sign-in included, need
not be. `PUBLIC_BASE_URL` names where the pay page is reachable; unset (the default),
there are no pay links. On-site QR payments need no public host: the tablet that shows
the QR is signed in on `APP_URL`.

**Preconditions:** a TLS-terminating reverse proxy in front of Vantigo
([Transport security](/en/admin/transport-security/)) and a host name for the pay page
with its certificate.

**The gate keys on `Host`.** Vantigo cannot see which listener or which name a request
arrived on, only its `Host` header. On the public host — a request whose `Host`, port
ignored, is `PUBLIC_BASE_URL`'s host name — it serves only the allowlist below. A
request with any other `Host` that the host filter admits — `APP_URL`'s host name, or
loopback (`localhost`, `127.0.0.1`, `::1`), which stays admitted for the container's own
health check — is served everything. So **the proxy that publishes the public host must
never forward any `Host` other than the public host**: if it passed on the client's own
`Host`, anyone on the internet could send `Host: localhost` or `Host: vantigo.example.com`
to the pay page's address and reach the whole of Vantigo, sign-in included. That proxy
rule is what makes the public host safe; the steps below set it.

1. **Choose the host.** Recommended: a host of its own, such as `pay.example.com`, with
   the **same base path** as `APP_URL` — `PUBLIC_BASE_URL=https://pay.example.com` when
   `APP_BASE_PATH` is unset, `https://pay.example.com/vantigo` when it is `/vantigo`.
   On that host Vantigo serves only what the pay page needs and answers 404 to
   everything else. The alternative is `APP_URL`'s own origin: then there is no second
   host and nothing is filtered, so publishing the pay page means publishing all of
   Vantigo — the sign-in, the API and the documentation — to the internet.
   Configuration refuses, at start-up, a `PUBLIC_BASE_URL` that is not `https`; that
   carries a user, a query or a fragment; whose path is not `APP_BASE_PATH`; whose host
   name ends in a dot or is not ASCII (write an internationalised name in its punycode
   form, `xn--…`); or that names `APP_URL`'s host name on another port (the host filter
   and the gate compare host names without the port, so the two could not be told
   apart).
2. **Publish it with a fixed `Host`.** The proxy forwards the public host's requests to
   the same Vantigo process as `APP_URL` with **`Host` set to the public host name itself**
   — a constant, never the client's header (in nginx, `proxy_set_header Host
   pay.example.com;`, never `$host` or `$http_host`). `X-Forwarded-Host` is never
   honoured, and the pay page's requests are same-origin only because `Host` is the public
   host. Give the proxy a catch-all for every other name, so a request naming neither
   host is dropped there rather than forwarded. The host filter admits the public host
   beside `APP_URL`'s and loopback, and nothing else
   ([Host header filtering](/en/admin/transport-security/#host-header-filtering)).
3. **Trust the proxy.** Set `TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` to the proxy in
   front ([Forwarded headers and HTTPS](/en/admin/authentication/#forwarded-headers-and-https)).
   The pay page's operations are anonymous and rate-limited by the client's address;
   without the two settings every payer arrives from the proxy's address and shares one
   bucket, so a handful of customers paying at once are refused.
4. **Keep the pay link's token out of the proxy's logs.** The pay link is
   `PUBLIC_BASE_URL/pay?token=…`, and the token is what lets anyone holding it pay the
   invoice and see its amount. Vantigo's own request log writes the path and never the
   query, and every response carries `Referrer-Policy: no-referrer`; the pay page then
   reads the invoice with `POST /api/v1/invoices/pay/read`, the token in the body, so it
   never reaches an API URL. What remains is the proxy's own logging of the page request.
   Nothing on the public host needs its query logged, so log the path alone for the whole
   host.

   With nginx:

   ```nginx
   log_format vantigo_pay '$remote_addr [$time_local] "$request_method $uri $server_protocol" '
                          '$status $body_bytes_sent "$http_user_agent"';

   # Every name other than the two published hosts: closed without an answer.
   server {
       listen 443 ssl default_server;
       server_name _;
       ssl_reject_handshake on;
       return 444;
   }
   server {
       listen 80 default_server;
       server_name _;
       return 444;
   }

   server {
       listen 443 ssl;
       server_name pay.example.com;
       # ...ssl_certificate...
       access_log /var/log/nginx/vantigo-pay.log vantigo_pay;
       error_log  /var/log/nginx/vantigo-pay.error.log crit;
       location / {
           proxy_set_header Host pay.example.com;   # a constant, never $host
           proxy_pass http://vantigo:8080;
       }
   }
   ```

   `$uri` is the path alone; `$request` would carry the query. nginx's **error log** also
   prints the request line, query included, when an upstream fails ("connect() failed …
   request: "GET /pay?token=…""), at the `error` level: `crit` above keeps those lines out,
   at the cost of the upstream errors themselves on this host — read them in the
   `APP_URL` host's error log, or keep `error` and treat that log as holding pay tokens
   (restrict who reads it, keep it short). `ssl_reject_handshake` needs nginx 1.19.4 or
   later; on an older one, give the catch-all a certificate of its own instead. The port-80
   catch-all matters when the proxy also listens on 80.

   **`APP_URL`'s own site must not be reachable on the public address.** If its
   `server` block listens on the same address and port in the same nginx, a client can
   open TLS for `pay.example.com` and send `Host: vantigo.example.com`: nginx picks the
   `server` block by `Host`, so the request reaches `APP_URL`'s block and the whole of
   Vantigo through the pay page's address — the catch-all does not help. Make
   `APP_URL`'s block listen only on an internal address (`listen 10.0.0.5:443 ssl;`), or
   restrict it to your own networks with `allow` and `deny`, or publish it from another
   proxy altogether.

   With Caddy, a site block answers only the names it lists — any other name finds no
   site and is refused — and `reverse_proxy` sends the upstream the site's own host, so
   nothing more is needed for `Host`; delete the token from the logged URI:

   ```text
   pay.example.com {
       log {
           format filter {
               request>uri query {
                   delete token
               }
           }
       }
       reverse_proxy vantigo:8080
   }
   ```

   The same holds for `APP_URL`'s site in the same Caddy: Caddy routes by `Host`, so a
   client opening TLS for `pay.example.com` with `Host: vantigo.example.com` reaches it.
   Restrict that site to your own networks with a `remote_ip` matcher, or turn on
   `strict_sni_host` in the `servers` global option, which answers `421 Misdirected
   Request` when `Host` does not match the TLS server name. The `log` directive
   configures only the access log; Caddy's documentation does not say that an upstream
   failure stays out of its runtime log (the `default` logger, on stderr), so treat that
   log as holding pay tokens too: raise its level, or restrict who reads it and keep it
   short.
5. **Restart** Vantigo with `PUBLIC_BASE_URL` set; it is read at start-up.

**The allowlist.** On the public host, a request whose method and path are not on this
list answers 404 — a problem document under `/api/`, an empty body elsewhere — before
any routing (`security.PublicHostGate`); the same request on `APP_URL`'s host is served as
before. Paths are relative to the base path. A path that is not already clean (`..`, `.`,
an empty segment) or that is escaped other than canonically (an encoded `/`, `%2F`) is
on no list.

| Method | Path |
| --- | --- |
| `GET`, `HEAD` | `/pay` (the pay page; any query) |
| `GET`, `HEAD` | `/assets/…` (the page's bundle), `/favicon.png` |
| `POST` | `/api/v1/invoices/pay/read` (the page's read; the token in the body) |
| `POST` | `/api/v1/invoices/pay/attempts` (a payment started) |
| `GET` | `/api/v1/invoices/pay/attempts/{reference}` (its status) |
| `GET`, `HEAD` | `/health/…` (probes) |

**Verify** from outside, against the public host:

```sh
curl -i https://pay.example.com/sign-in            # 404
curl -i https://pay.example.com/api/v1/identity/me # 404, a problem document
curl -i 'https://pay.example.com/pay?token=x'      # 200, the pay page
curl -i https://vantigo.example.com/sign-in        # 200 on APP_URL's host
# The proxy must not pass on another Host: neither of these may answer 200.
curl -i https://pay.example.com/sign-in -H 'Host: localhost'
curl -i https://pay.example.com/sign-in -H 'Host: vantigo.example.com'
```

The last two must answer 404 (the proxy sent the public host regardless) or be refused
by the proxy — never 200 and the sign-in page. The `Host: vantigo.example.com` check
proves something only when `APP_URL`'s site is served by the same proxy; there it is
exactly the request that would reach `APP_URL`'s site through the public address. Then open a pay link and check the proxy's
access log: the line for `/pay` carries no `token`. In the browser, the pay page shows
the invoice without signing in, its address bar no longer shows the `token`, and the
developer tools' network list holds only requests from the table above — no
`/api/v1/identity/session`.

**What can go wrong:**

- **`/sign-in` answers 200 with `Host: localhost` or `APP_URL`'s host** sent to the public
  address: the proxy forwards the client's `Host` (`$host`, `$http_host`). Set `Host` to
  the public host name as a constant. Vantigo itself does not refuse loopback on the
  public listener — it cannot tell listeners apart, and loopback must stay admitted for the
  container's health check, which probes `127.0.0.1` from inside — so this proxy rule is
  the only thing that keeps the rest of Vantigo off the public address.
- **Every request to the public host answers 400** "The request host is not allowed":
  `PUBLIC_BASE_URL` is unset or names another host than the one the proxy sends.
- **The pay page is a blank page or its requests answer 404**: the proxy serves the
  public host under another path than `APP_BASE_PATH`, or strips the base path itself.
- **Payers are refused with 429 while few are paying**: `TRUSTED_PROXY_HOPS` and
  `TRUSTED_PROXY_CIDRS` are not set, so every payer shares the proxy's bucket.
- **`/sign-in` answers 200 on the public host** with no `Host` trick at all:
  `PUBLIC_BASE_URL` names `APP_URL`'s own host, so there is no gate — or the proxy sends
  the public host's requests with `APP_URL`'s `Host`.
