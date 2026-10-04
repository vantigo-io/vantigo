---
title: E-invoicing
description: Sending invoices as EHF over the Peppol network through a Storecove access point, the two workers that carry them, and the KID agreement with the bank.
sidebar:
  order: 42
sources:
  - apps/server/internal/invoices/accesspoint
  - apps/server/internal/invoices/ehf
---

EHF is the Norwegian e-invoice: EHF Billing 3.0, which is Peppol BIS Billing 3.0 with two
Norwegian rules on top — an invoice or credit note as UBL 2.1 XML that the buyer's
accounting system reads without retyping, with Vantigo's PDF of the document embedded.
It travels over **Peppol**, a network of access points: the sender hands the document to
its access point, which looks the receiver up in the network's registry and delivers it
to the receiver's access point. Vantigo is not an access point itself; it hands each
document to one, **Storecove**, through Storecove's API.

The duties: public bodies have required EHF from their suppliers since 2019, and from
**1 January 2027** a Norwegian business must send its invoices to other Norwegian
businesses as e-invoices — an e-mailed PDF no longer meets the duty. Receiving
e-invoices becomes a duty in 2030 and is not part of Vantigo yet. A sender needs no
registration of its own in the Peppol registry (ELMA) to send; it needs an account with
an access point. This page is the operator's side: the account, the settings, the
workers, and what to do when something goes wrong. The person sending invoices has
[the user guide](/en/user/invoices/#sending-as-ehf); the rules are in
[the reference](/en/reference/invoices/#sending-as-ehf).

## Before you start

- The Invoices module is enabled (`MODULES`, with `customers`), and the seller record in
  **Invoice settings** has the organisation number.
- **`PEPPOL_LOOKUP_ENABLED=1`**, the default. Every send asks the Peppol network again
  whether the receiver accepts the document, so with the lookup off EHF is off too. The
  lookup needs outbound DNS and HTTPS to the registry's SMP servers
  ([the Peppol settings](/en/admin/authentication/#transport-storage-and-modules)).
- **`INVOICES_EHF_ENABLED=1`**, the default. `0` turns sending as EHF off: a send answers
  503 `ehf_unavailable`, the workers below do not start, and the settings page says EHF
  is not available.
- **`INVOICES_STORECOVE_BASE_URL`**, by default Storecove's production API
  `https://api.storecove.com/api/v2/`. It is the operator's setting, never a user's:
  nobody in the app can point the adapter elsewhere. Outbound HTTPS to that host is
  needed, and to the download links Storecove's evidence names.
- **An object store** ([Object storage](/en/admin/object-storage/)). Issuing already
  needs one; EHF files and receipts are stored beside the PDFs.
- **Background workers running** — in `api` mode with `WORKERS_IN_PROCESS=1` (the
  default) or in a `worker` container ([Background workers and
  scaling](/en/admin/installation/#background-workers-and-scaling)). Without them a sent
  document stays queued.
- **The seller's Peppol id.** It defaults to `0192:` and the organisation number, which
  is what a Norwegian business uses; check it on the card **E-invoicing**.
- Someone with `invoices:manage` to enter the credentials.

## Choosing an access point

Vantigo speaks to one provider, **Storecove**, chosen because its API covers what an
unattended sender needs: a submission keyed so that a retry is never a second invoice,
a queue of delivery events, and a receipt with the copy that was delivered. Others may
follow behind the same port; none is supported today. Storecove sells accounts through a
sales contact, not a sign-up form, and prices them by agreement.

## 1. Open a Storecove account

1. Ask Storecove for an account through the contact form on
   [storecove.com](https://www.storecove.com/). You get a **test account for thirty
   days** first: the same API host, with a sandbox key. Use it to try the whole chain
   before you ask for production.
2. In Storecove, create your company as a **legal entity** — name, address, country NO —
   and give it its **Peppol identifier**: the scheme `NO:ORG` (superscheme
   `iso6523-actorid-upis`) with your organisation number.
3. **Keep the two identifiers equal.** Storecove's `NO:ORG` with your organisation
   number is the same participant as Vantigo's Peppol id `0192:<organisation number>`.
   Storecove rebuilds the EHF it transmits from the one Vantigo submits, and the sender
   on the network may be its legal entity's identifier; if the two differ, the document
   Vantigo records and the one the customer receives name different senders. Change one,
   change both.
4. Note the legal entity's numeric **id**, and create an **API key** for the account.

**One Storecove account per installation.** A delivery's outcome comes from the account's
event queue, which Vantigo empties and acknowledges event by event — every event it
reads, its own or not, since it cannot leave one at the head of the queue. Another
system reading the same account, or a second Vantigo installation, would lose its events
to this one and this one to it. Give production, staging and every test installation an
account of its own.

## 2. Store the credentials

In the app, with `invoices:manage`: **Invoice settings** → **E-invoicing** → **Access
point**, enter the **Legal entity id** and the **API key**, and click **Save access
point**. Or through the API:

```http
PUT /api/v1/invoices/settings/access-point
Content-Type: application/json

{"provider": "storecove", "legalEntityId": 12345, "apiKey": "…"}
```

The key is sealed with a key derived from `APP_SECRET` and stored in its own row,
`invoices.access_point_credentials`; it is never shown, logged or answered again — the
answer says only `hasCredentials`. Leaving `apiKey` out keeps the stored one, so a new
legal entity id needs no key. **Changing `APP_SECRET` makes the stored key unreadable**:
the next use logs an error, the settings page shows *The access point refused the key*,
and the key has to be entered again
([the credentials](/en/reference/invoices/#the-access-points-credentials)).

Removing the credentials (**Remove the credentials**, or `DELETE` on the same path) and
switching to another provider are refused with 409 `transmissions_active` while any
transmission is queued, submitted or unconfirmed: the provider still holds what those
need. Replacing the key is never refused, so a refused key can be fixed while documents
wait.

## 3. Verify

Click **Verify** on the card (or `POST /api/v1/invoices/settings/access-point/verify`).
Vantigo reads the legal entity from Storecove with the stored key and answers:

| Answer | Meaning |
| --- | --- |
| *The access point accepted the key.* (`ok`) | The key works for that legal entity; a rejected-key flag is cleared. |
| *The access point refused the key.* (`unauthorized`) | Storecove answered 401 or 403: a wrong, revoked or expired key. The flag is set. |
| *The access point could not be reached, or the key does not reach this legal entity.* (`unreachable`) | The network, a timeout, a server error at Storecove, or a legal entity id the key does not cover. |
| *No key is stored, or the stored key can no longer be read here.* (503 `ehf_unavailable`) | Vantigo cannot read the stored key — `APP_SECRET` changed, or the row was altered. The flag is set and an error logged; enter the key again. Without any stored credentials, Verify answers 409 `ehf_unavailable`. |

Then check that the card's **What e-invoicing needs** says *Sending as EHF is
available*. The first real send is the final proof: send one invoice to a customer who
expects EHF and watch its card reach **Delivered to the receiver's access point**.

## What the workers do

Two background workers carry a sent document; they run wherever this deployment runs
workers, and only while `INVOICES_EHF_ENABLED` is on. With the Peppol lookup off they
still follow what was already handed over, but hand over nothing new.

- **`invoices-ehf`**, every 5 seconds, takes one due transmission at a time under a
  60-second lease, so replicas never handle the same one, and makes **one call to
  Storecove per turn**, bounded to 30 seconds.
  - A **queued** document: if the receiver was last looked up more than 24 hours ago, the
    lookup runs again (a receiver that has left the network fails the transmission as
    `receiver_not_receivable`); then the document is submitted under the transmission's
    own idempotency key, so a retry is never a second invoice. A network error or a
    server error is retried, waiting twice as long each time up to an hour; a 429 waits
    as long as Storecove asks; a refused key waits an hour, raises the rejected-key flag
    and logs an error. A document still queued **48 hours** after it was sent is given up:
    as **unconfirmed** when Storecove may have it, as **failed** when it never got that
    far.
  - A **submitted** document: Storecove's receipt is asked for after 5 minutes, again
    after 15, then hourly, in case the event queue never mentions it; still submitted
    **seven days** after submission, it becomes **unconfirmed**.
  - A **delivered** document: the receipt and the delivered copy are fetched once and
    stored (below).
  - An **unconfirmed** document with a Storecove reference: asked about once a day for
    thirty days, and marked delivered if Storecove finally has a receipt; after that it
    waits for a person. Without a reference it waits for a person at once.
- **`invoices-ehf-events`**, every 30 seconds, under a PostgreSQL advisory lock
  (`pg_try_advisory_lock`) so one replica drains at a time: while a transmission is
  submitted, unconfirmed and still being asked about or recently queued without a
  reference, or queued after a hand-over was tried, it reads Storecove's
  event queue until it is empty — at most 500 events a cycle — marks each document
  delivered (Storecove's `succeeded`: the receiving access point's receipt) or failed
  (`failed`, `no_action_taken`), an unconfirmed one included, and acknowledges every
  event. An event the database refuses outright is logged at error and acknowledged
  anyway, so it cannot hold up the queue behind it.

**Delivered** means the receiving access point acknowledged the message — nothing
stronger: not that the customer's system accepted the invoice or that anyone read it.

## Unconfirmed and failed transmissions

**Unconfirmed** means Vantigo cannot know whether the document arrived: Storecove may
have it and never confirmed. The machine does not guess, because a guess would either
send a second invoice or drop one. It blocks a new send of that document until someone
with `invoices:issue` resolves it on the document's card **E-invoice (EHF)** with
**Resolve**, after checking with Storecove — the card shows issuers the **Provider
reference**, Storecove's id of the submission, to look it up by. As the holder of the
Storecove account, expect to be asked. Resolved as failed, the next send carries the very
same EHF, so a document that did arrive is at worst received twice, never as two
different documents. If Storecove's receipt or event arrives first, Vantigo resolves the
transmission itself, noting that the provider did.

**Failed** means the document was not delivered: Storecove refused it (the reason, in
Storecove's words, is shown to issuers), the receiver left the network, or it never
reached Storecove within 48 hours. The document can be sent again, or e-mailed.

## When sending fails

- **The rejected-key flag.** The settings card shows *The access point refused the key*,
  `GET /api/v1/invoices/meta` answers `accessPointCredentialsRejected: true`, and the log
  has an error. The key was revoked or mistyped, or `APP_SECRET` changed. Save a valid
  key and click **Verify**. Saving does not hurry the queue: each document the refused
  key held back goes out when it is next due, within the hour.
- **The lookup.** A send refused with 502 `peppol_lookup_failed` means the Peppol
  registry could not be asked: check outbound DNS and HTTPS, and `PEPPOL_DNS_SERVER`.
  `peppol_not_receivable` is not a fault: the receiver is not registered for that
  document type, and the document goes by e-mail.
- **The credentials.** **Verify** answering *could not be reached* points at the network
  to `INVOICES_STORECOVE_BASE_URL`, or at a legal entity id the key does not cover.
- **503 `ehf_unavailable`.** One of the preconditions is missing: the switch, the lookup,
  the credentials or the seller's Peppol id. The card **E-invoicing** says which line is
  not ready.
- **Documents stay queued.** The workers are not running: check `WORKERS_IN_PROCESS`, or
  that the `worker` container is up.
- **Failed with Storecove's validation messages.** Storecove refused the document. The
  reason names the rule; e-mail the document meanwhile, and report it — Vantigo's own
  checks should have caught it ([E-invoice validation](/en/contributing/e-invoice-validation/)).

## The objects written

Beside the PDF, in the `invoices` scope of the object store
([Object storage](/en/admin/object-storage/#module-scopes-and-the-physical-key)):

| Key | What |
| --- | --- |
| `invoices/documents/<id>/<number>-<sha256>.xml` | The EHF as Vantigo submitted it, stored once by its hash when the document is sent — **Download EHF (XML)** serves it. |
| `invoices/documents/<id>/<number>-<transmission>-receipt.json` | Storecove's evidence of delivery: the receiving access point, the message id and the receipt. |
| `invoices/documents/<id>/<number>-<transmission>-delivered.xml` | The EHF Storecove actually delivered, which it rebuilt from Vantigo's. |

Nothing here is ever deleted or overwritten: like the PDF, they are bookkeeping material
kept five years after the end of the financial year, so back them up with the rest of the
store.

## The KID agreement with the bank

A KID is the reference on a payment that lets the bank, and later Vantigo, match it to
one invoice. Ask your bank for a **KID agreement (OCR giro)** on the account in the
seller record; the bank registers a **length** and a **check digit method** for it, and
rejects or flags a payment whose KID does not fit them.

- **The length** counts the check digit, 4 to 25. Vantigo's KID is the invoice number
  zero-padded to the length less one, then the check digit, so choose a length with room
  to grow: the digits of your invoice numbers, one for the check digit, and two to
  spare. The settings refuse a length the next number does not fit and warn when fewer
  than two digits are left.
- **The method**, MOD10 or MOD11. Ask for MOD10: under MOD11 some numbers get `-` as
  their check digit, which payers stumble over.
- One agreement per account; the bank can keep up to three lengths valid on it.

Enter the pair on the card **KID** in **Invoice settings**
([the user guide](/en/user/invoices/#agree-a-kid-with-the-bank)). Every invoice issued
from then on carries a KID on its PDF, in its e-mail and in its EHF; earlier ones carry
none. **Changing the agreement** later applies to new invoices only: open invoices keep
the KIDs they were issued with, so ask the bank to keep the old length valid until they
are paid — that is what its extra lengths are for. A length the next number does not fit
is refused when it is saved (400 on `kidLength`); issuing stops (409
`kid_length_exceeded`) only when the numbers outgrow a length that fitted when it was
saved, until the agreement is lengthened.

## Testing against the Storecove sandbox

With a sandbox key, the adapter can be run against Storecove itself. From `apps/server`:

```bash
STORECOVE_SANDBOX_API_KEY=… STORECOVE_SANDBOX_LEGAL_ENTITY_ID=… \
  mise exec -- go test -tags storecove ./internal/invoices/accesspoint/
```

It submits one invoice and one credit note to Storecove's Norwegian test receiver
(`NO:ORG` `010101018`), drains the account's event queue until their outcomes arrive —
acknowledging every event, so run it on an account nothing else reads — and checks that
the PDF embedded in the submitted EHF is in the copy Storecove delivered.
`INVOICES_STORECOVE_BASE_URL` overrides the host, and `STORECOVE_SANDBOX_SELLER_ORG` the
seller's organisation number in the test documents (default `974760673`; keep it the
legal entity's own). Without the two variables the tests skip
([the test](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/invoices/accesspoint/storecove_sandbox_test.go)).
An installation can likewise be pointed at a mock Storecove with
`INVOICES_STORECOVE_BASE_URL`.

## Known limits

- **The embedded PDF.** Storecove rebuilds the EHF it transmits, and its documentation
  does not say whether the PDF Vantigo embeds survives. The sandbox test above checks it;
  until it has passed against Storecove, open the delivered copy (`…-delivered.xml`) of a
  first real send and look for the attachment.
- **Peppol BIS Billing 3.0.21.** Vantigo's EHF is validated against the Peppol rules as
  tagged `v3.0.20`; the 3.0.21 rules are adopted the day OpenPEPPOL tags them. Until then
  a rule new in 3.0.21 can refuse a document at Storecove, which shows as **Failed** with
  Storecove's reason.
- **Storecove's memory of a submission key** is not documented; Vantigo gives up on a
  queued document after 48 hours to stay well inside any such window.
- **Not supported:** receiving e-invoices, Peppol Invoice Response, a second provider,
  Storecove's push webhooks, running Vantigo as its own access point, eFaktura and
  AvtaleGiro, and VAT category K (intra-EEA) lines, which go by e-mail.
