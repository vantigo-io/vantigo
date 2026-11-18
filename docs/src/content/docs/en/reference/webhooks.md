---
title: "Webhooks"
description: "Outgoing webhooks of an integration: the endpoint and its rules, the events an endpoint may subscribe to and the permission each needs, the thin signed message, Standard Webhooks signing and how a receiver verifies it, the fan-out and the sender, retries, ordering, when an endpoint is disabled and who is told, the test event, expiry, the secret and its roll, the endpoints API with its refusals, the limits, the audit and the tables."
sidebar:
  order: 3
sources:
  - apps/server/internal/contracts/webhook_events.go
  - apps/server/internal/identity/webhooks.go
  - apps/server/internal/identity/webhooks_signer.go
  - apps/server/internal/identity/webhooks_secret.go
  - apps/server/internal/identity/webhooks_url.go
  - apps/server/internal/identity/webhooks_client.go
  - apps/server/internal/identity/queries/webhooks.sql
  - apps/server/internal/identity/queries/webhooks_sender.sql
  - apps/server/internal/identity/webhooks_handler.go
  - apps/server/internal/identity/webhooks_worker.go
  - apps/server/internal/db/migrations/00204_identity_webhooks.sql
  - apps/server/internal/integration/webhook_events_test.go
  - apps/host/frontend/src/routes/workspace/-api-access-webhooks.tsx
  - apps/host/frontend/src/routes/workspace/-webhook-secret-dialog.tsx
  - openapi/identity.yaml
---

An integration can have **webhook endpoints**: HTTPS URLs to which Vantigo posts a short,
signed message when an event the endpoint lists happens — a sales order confirmed, an invoice
overdue, a purchase order sent. The message says which resource it is about and nothing
else; the receiver fetches the resource with one of the integration's
[API keys](/en/reference/api-introduction/), so what it may read is decided where every other
request is decided. Endpoints belong to the identity module, as integrations do, and are
managed by an Owner under *Workspace admin › API access* (see
[API access](/en/admin/api-access/#webhooks)).

An event reaches an endpoint in two steps: the **fan-out** queues one message per endpoint
when the event is published, and the **sender** posts what was queued, retrying a failure for
about two days. The delivery log, with resend, arrives in the next release.

## The endpoint

| Field | Rule |
| --- | --- |
| `url` | `https` only; a host name, never an IP address, `localhost` or a name under `.localhost`, `.local` or `.internal`, and with at least one dot; no user name or password; no fragment; the port empty or `443`; at most 2,048 characters. A query string is allowed. The host is checked and kept in its ASCII form — lowered, a trailing dot dropped, an internationalised name as punycode and a full-width dot as `.` — so `foo．internal` is refused as `foo.internal`; an explicit `:443` is dropped. |
| `description` | Optional, up to 500 characters. |
| `events` | 1 to 50 names from [the catalogue](#the-events), each one the integration may receive. `*` and prefixes are not accepted. |
| `status` | `enabled` or `disabled`, with `disabledReason` `manual` (an Owner), `failing` (the sender, after 5 days of failure), `gone` (the receiver answered `410 Gone`) or `integration_archived`. |
| `previousSecretExpiresAt` | While a roll's overlap runs, when the secret it replaced stops signing. |
| `version` | The endpoint's version, for `If-Match`. |

At save time the host is resolved once and refused when any address it resolves to is
private, loopback, link-local (the `169.254.169.254` metadata address), carrier-grade NAT or
otherwise reserved (`422 webhook_url_blocked`), or when it does not resolve
(`422 webhook_url_unresolvable`). That check is advice for a friendly answer, not the
boundary: the sender dials through the same guard on every attempt, checking every address
the name resolves to then and connecting to the very address it checked, so a name that
starts resolving to a private address is never reached.

An integration has at most **5 endpoints** and the workspace **50**, counting the endpoints
of integrations that are not archived (`409 webhook_limit_reached`). Creating an endpoint
takes a transaction lock, so two creations at once never pass a limit together.

Archiving an integration disables every enabled endpoint of it (`integration_archived`) and
settles the deliveries waiting for them, in the archive's transaction, each audited
`webhook.disabled`. An archived integration's endpoints cannot be created, changed, enabled
or have their secret rolled (`409 integration_archived`); they can be deleted.

## The events

An endpoint subscribes to events from a catalogue, an allow-list in
[`contracts/webhook_events.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/contracts/webhook_events.go):
each event is exposed deliberately, with the resource a receiver fetches and the permissions
that fetch needs. An integration receives an event only while its roles hold **every** one of
the event's permissions, none of them one no API key ever holds. The check is made when an
endpoint is saved (`422 webhook_event_not_permitted`), again when the event is
[fanned out](#the-fan-out) and once more when its message is [claimed to be sent](#sending):
a role taken from the integration stops the stream at once, and what was not sent is recorded
`skipped` with the reason `permission_not_held`, so the cause can be seen. A key's narrowing does not apply to the stream: the stream belongs to
the integration, and the receiver's fetch is what its key narrows.

| Event | Resource | Fetch | Permission |
| --- | --- | --- | --- |
| `orders.quote.sent` | `quote` | `GET /api/v1/orders/quotes/{id}` | `orders:access` |
| `orders.quote.accepted` | `quote` | `GET /api/v1/orders/quotes/{id}` | `orders:access` |
| `orders.sales_order.confirmed` | `sales_order` | `GET /api/v1/orders/orders/{id}` | `orders:access` |
| `orders.sales_order.changed` | `sales_order` | `GET /api/v1/orders/orders/{id}` | `orders:access` |
| `orders.sales_order.cancelled` | `sales_order` | `GET /api/v1/orders/orders/{id}` | `orders:access` |
| `orders.sales_order.invoiced` | `sales_order` | `GET /api/v1/orders/orders/{id}` | `orders:access` |
| `orders.subscription.started` | `subscription` | `GET /api/v1/orders/subscriptions/{id}` | `orders:access` |
| `orders.subscription.changed` | `subscription` | `GET /api/v1/orders/subscriptions/{id}` | `orders:access` |
| `orders.subscription.ended` | `subscription` | `GET /api/v1/orders/subscriptions/{id}` | `orders:access` |
| `orders.delivery.posted` | `delivery` | `GET /api/v1/orders/deliveries/{id}` | `orders:access` |
| `orders.shipment.booked` | `shipment` | `GET /api/v1/orders/shipments/{id}` | `orders:access` |
| `orders.shipment.delivered` | `shipment` | `GET /api/v1/orders/shipments/{id}` | `orders:access` |
| `invoices.document.issued` | `invoice` | `GET /api/v1/invoices/{id}` | `invoices:access` |
| `invoices.invoice.overdue` | `invoice` | `GET /api/v1/invoices/{id}` | `invoices:access` |
| `invoices.payment.received` | `invoice` | `GET /api/v1/invoices/{id}` | `invoices:access` |
| `invoices.payment.removed` | `invoice` | `GET /api/v1/invoices/{id}` | `invoices:access` |
| `invoices.customer_credit.changed` | `customer_credit` | `GET /api/v1/invoices/customers/{customerId}/credit` | `invoices:access` |
| `purchasing.order.sent` | `purchase_order` | `GET /api/v1/purchasing/orders/{orderId}` | `purchasing:access` |
| `purchasing.order.closed` | `purchase_order` | `GET /api/v1/purchasing/orders/{orderId}` | `purchasing:access` |
| `purchasing.receipt.posted` | `purchase_receipt` | `GET /api/v1/purchasing/receipts/{receiptId}` | `purchasing:access` |
| `purchasing.receipt.undone` | `purchase_receipt` | `GET /api/v1/purchasing/receipts/{receiptId}` | `purchasing:access` |
| `manufacturing.order.done` | `manufacturing_order` | `GET /api/v1/manufacturing/orders/{id}` | `manufacturing:access` |
| `accounting.supplier_invoice.posted` | `supplier_invoice` | `GET /api/v1/accounting/supplier-invoices/{id}` | `accounting:read` |
| `accounting.supplier_invoice.reversed` | `supplier_invoice` | `GET /api/v1/accounting/supplier-invoices/{id}` | `accounting:read` |

- `invoices.document.issued` covers credit notes too: `GET /api/v1/invoices/{id}` serves a
  credit note, its `kind` `credit_note`.
- A payment has no resource of its own, so `invoices.payment.received` and
  `invoices.payment.removed` name the **invoice** it was registered against.
- `invoices.customer_credit.changed` names a customer by its id, and a customer can be a
  private person.

**Left out**, on purpose: `communications.*` (user ids and conversations),
`time.week.unsubmitted` (a person's hours) and `expenses.*` (a person's claims), which concern
people rather than business documents; and `accounting.supplier_invoice.deleted`, since a
deleted draft is gone and its `GET` can only answer `404`. Adding an event is a catalogue entry,
a test and a line here.

A test in `internal/integration` holds the catalogue to the code: each entry is a version 1
contract event whose payload has the id field; its path is a `GET` of the composed contract
whose access rule names exactly the entry's permissions; each permission is delegable and
not one no key holds; that `GET` is not `x-vantigo-api: never`; and this page lists every
event.

## The message

The body is one small JSON object, fixed when the event is fanned out, so a resend sends the
same bytes:

```json
{
  "id": "evt_8421",
  "type": "orders.sales_order.confirmed",
  "version": 1,
  "timestamp": "2026-11-18T10:15:00Z",
  "sequence": 8421,
  "data": { "resource": { "type": "sales_order", "id": "42", "href": "/api/v1/orders/orders/42" } }
}
```

- `id` is `evt_` and the outbox event's id, `sequence` that number. Ids rise but are neither
  contiguous nor ordered across resources: compare `sequence` only between messages about the
  same `data.resource.type` and `id`, to discard one older than what you have seen.
- `timestamp` is when the event happened (UTC, RFC 3339); it is not monotonic either.
- `href` is the path only. The receiver already knows the installation's address, the one its
  API key is used against.
- The body carries **no amount, no name and no other field of the event**. Fetch the resource
  to learn its state; because the fetch returns the current state, a message that arrives late
  or twice is harmless.
- `webhook.ping` is the test message, with `data: {}` and no `sequence`.

No order is promised between messages ([Ordering](#ordering)); dedupe on `webhook-id`.

## The fan-out

The outbox handler `identity.webhooks` takes every event of [the catalogue](#the-events). On
the outbox delivery's own transaction, with no network, it:

1. holds, `FOR SHARE` and in id order, the **enabled** endpoints that list the event and whose
   integration is neither archived nor disabled — disabling, archiving and deleting hold them
   `FOR UPDATE` before they settle what waits, so no message is queued for an endpoint after
   its waiting messages were dropped;
2. reads each integration's permissions once and decides whether it may receive the event;
3. queues one delivery per endpoint, its body fixed there: `pending`, or `skipped`
   (`permission_not_held`) when the integration may not — at most one skipped delivery a
   day per endpoint and event, which shows the cause without a row for every event missed.
   The outbox delivers at least once; a second fan-out of the same event to the same
   endpoint is a no-op.

An event whose payload lacks the catalogue's id field is a bug, not an endpoint's fault: the
handler fails, the outbox retries it and then holds it dead for the operator, and later
webhook events of the same resource wait behind it in the outbox. There is **no backfill**:
an endpoint created now receives the events published after it.

## Sending

The sender is the worker `identity.webhook-sender`, which runs where the workers run, unless
it is switched off ([Configuration](/en/admin/configuration/)). It passes every 2 seconds
when idle; a pass that leased all it could is followed by the next at once, and a send that
finishes starts one, so a fast receiver gets messages as fast as it answers. Each pass it:

1. locks the enabled endpoints of active integrations that have a message due, `FOR UPDATE
   SKIP LOCKED`, so two worker processes never count one endpoint at once;
2. counts each one's messages in flight, ranks its due messages by when they were queued, and
   leases, for 2 minutes, at most **2 per endpoint per pass**, never more than **4 in flight
   per endpoint** across every process, and no more than its own free slots (**16 sends** at
   once per process). An endpoint with nothing in flight is served before any gets a second
   send, and the last 4 free slots are kept for such first sends, so endpoints that never
   answer hold at most 12 slots between them and the rest are still served;
3. checks each integration's permission once and records `skipped` what it may no longer
   receive;
4. sends each leased message on its own, without waiting for the slowest before the next pass,
   so a slow receiver holds only its own four slots.

The permission, the endpoint's status and the integration's are checked when a message is
claimed, not again when it is posted: a role taken, or an endpoint or integration disabled or
archived, in the moment between the claim and the post can still let that one message go
out. It names a resource the receiver's fetch is then refused or no longer sees; nothing
after it is sent.

An **attempt** opens the secrets, signs ([Signing](#signing)) with the current secret — and
with the previous one too while a roll's overlap ends after the attempt's time — and posts
once through the guarded client: the address checked on every attempt, 15 seconds in all, no
redirect followed. Only a **`2xx`** answer succeeds; a `3xx`, any other status, a timeout, a
TLS, DNS or connection failure and a blocked address all fail it. Its result is written on a
short transaction of its own, the endpoint held first, and only while the claim's lease still
holds the message: a message whose lease ran out and was claimed by another sender, settled
meanwhile or deleted with its endpoint records nothing. An attempt the sender abandons
because it is stopping is not one either; the message is sent again once its lease runs out.
A secret that cannot be opened — the installation's `APP_SECRET` changed, say — sends nothing
and is recorded as a failed attempt with the error `other`, so the message is retried on the
schedule and the endpoint can be disabled as failing; rolling the secret mends it.

### Retries

A message has a run of **8 attempts**: at once, then after 30 seconds, 5 minutes, 30 minutes,
2 hours, 6 hours, 12 hours and 24 hours — about 45 hours in all — each wait moved by up to 10 %
either way, so that many retries do not arrive together. The eighth failure makes it `dead`
(`attempts_exhausted`). A receiver that answers `429`, `502`, `503` or `504` with
`Retry-After`, in seconds or as a date, delays the next attempt to the later of the schedule
and the header, up to an hour; nothing else of the header is kept. A message to an endpoint
that answers `410 Gone` is `dead` (`gone`) at once, and the endpoint is
[disabled](#when-an-endpoint-is-disabled).

### Ordering

**No order is promised.** Messages for one endpoint are sent roughly in the order they were
queued, up to 4 at a time, and a failing message never holds another back, so a retried
message can arrive after a later one. Compare `sequence` only between messages about the same
resource, fetch the resource for its state, and dedupe on `webhook-id`.

### When an endpoint is disabled

The sender keeps, for each endpoint, `failing_since` — the first failed attempt since the
last success — and `last_success_at`; a result that raced a later one cannot clear or restart
the count. An enabled endpoint is disabled:

- as **`failing`** when it has been failing for **5 days** and **at least 5** of its messages
  have died by running out of attempts since — not those dropped by a disabling, an archive
  or the expiry, and never a test event;
- as **`gone`** at once, when it answers `410 Gone` to any message, a test event included.

Disabling drops the messages waiting for the endpoint (`dead`, `endpoint_disabled`), so
nothing stale is sent once it is enabled again, and is audited `webhook.disabled` with the
actor `system:webhooks`. Then **one notice** is mailed, in each recipient's language, to the
integration's contact and to every Owner: which endpoint, by its scheme and host only, why,
and where to enable it; the integration's name in it loses any control character. The notice
is claimed first, so two senders never both mail it; it is mailed with no transaction open.
When no one could be mailed it is tried again, at most once a minute and backing off to once
an hour, for a week after the disabling, and not at all on an installation with no mail. It
goes once per disabling: enabling the endpoint again clears it.

**Enabling** is an Owner's, by hand: the endpoint starts afresh with no failure counted, and a
test event is queued to it.

### The test event

**Send test event**, creating an endpoint and enabling one queue a `webhook.ping` message:
the body's `id` is `evt_ping_` and the delivery's id, `data` is `{}` and there is no
`sequence`. It is signed as every message is, has a run of **one** attempt — a failure makes
it `dead` with no retry — and never counts toward disabling the endpoint, except that a
`410 Gone` to it disables the endpoint as `gone`.

### Expiry and retention

Once a minute the sender settles as `dead` (`expired`) every message still `pending` 3 days
after it was queued, and deletes every message queued more than **30 days** ago, with its
attempts, whatever its state, a thousand at a time; a row another transaction holds is left
for the next sweep. While an integration is disabled its endpoints get nothing new and
nothing waiting is sent; when it is enabled again, what has been due for more than 24 hours
— overdue because it was disabled — is settled `expired`, and the rest is sent. With the sender
switched off, messages queue and expire the same way once it runs again.

## Signing

Every request is signed as [Standard Webhooks](https://www.standardwebhooks.com/) `v1`
specifies, which Svix also uses:

| Header | Value |
| --- | --- |
| `webhook-id` | `msg_` and the delivery's id; the same on every retry and resend |
| `webhook-timestamp` | The Unix seconds of this attempt |
| `webhook-signature` | `v1,` and the base64 HMAC-SHA256 of `{webhook-id}.{webhook-timestamp}.{body}`, one per active secret, separated by spaces |
| `Content-Type` | `application/json` |
| `User-Agent` | `Vantigo-Webhooks/1` |

The HMAC key is the base64-decoded bytes after `whsec_` in the endpoint's secret.

### Verifying a message

A receiver:

1. reads the raw body as it arrived, before any JSON parsing;
2. refuses a `webhook-timestamp` more than five minutes from its own clock, so an old message
   cannot be replayed;
3. computes `v1,` and the base64 HMAC-SHA256, keyed by the decoded secret, of the
   `webhook-id`, a dot, the `webhook-timestamp`, a dot and the raw body;
4. accepts the message when that value equals **any** of the space-separated entries of
   `webhook-signature`, compared in constant time — while a roll's overlap runs there are two;
5. drops a `webhook-id` it has handled already, and answers `2xx` quickly, doing the work
   afterwards.

The Standard Webhooks libraries do steps 2 to 4. An example, with the secret
`whsec_YW4gZXhhbXBsZSBzZWNyZXQgZm9yIHRoZSBkb2NzISE=` and the body above written on one line
without spaces:

```text
webhook-id: msg_2f1c0c5e8a7b4d6e9f0a1b2c3d4e5f60
webhook-timestamp: 1794996903
webhook-signature: v1,PC7oLLVZMjENZBAqlHjINCU8CI1QCvSh4RHm2eDlY84=
```

Vantigo's signer reproduces Svix's published test vector, and this example, in its tests.

## The secret

A secret is `whsec_` and the base64 of 32 random bytes, one per endpoint. It is answered
**once**, in the response to creating the endpoint or rolling its secret, with
`Cache-Control: no-store`; no read returns it again. It is kept sealed with the installation's
`APP_SECRET` (AES-256-GCM under the purpose `identity/webhook-secret/` and the endpoint's id),
because the sender needs it to sign. The purpose is also the cipher's additional data, so a
ciphertext copied into another endpoint's row opens nowhere; neither the audit trail nor a log line carries it or its ciphertext.

**Rolling** makes a new secret and keeps the one it replaces signing beside it until the
overlap ends — 1 to 168 hours, 24 by default — so a receiver can switch without missing a
message. Rolling again while an overlap runs replaces the previous secret at once: the older
secret stops signing. Changing an endpoint's URL does not roll its secret.

## What is recorded

The audit trail (`identity.authorization_audit_events`) records, with the Owner who did it:
`webhook.created`, `webhook.updated`, `webhook.deleted`, `webhook.enabled`, `webhook.disabled`,
`webhook.secret_rolled` and `webhook.test_sent`, with the queued delivery's id. When the sender
disables an endpoint it records `webhook.disabled` with the actor `system:webhooks`. A snapshot holds the endpoint's id, its integration, its events
and its status — not its description, which is free text — and of the URL **only the scheme and host** with a short keyed
digest of the whole, so a change shows: services such as Zapier, Slack and Make carry their
token in the path, and others in the query. The full URL is shown only on the Owner's screen.
A log line about an attempt carries the scheme and host too.

## The endpoints API

Every operation is an Owner's (`policy:Owner+OwnerManagement`), never available to an API key
(`403 api_not_available`) or the Assistant. The writes share identity's user-management limit
of 30 requests a minute from one address (`429 rate_limited`).

| Operation | Path | Answers |
| --- | --- | --- |
| List | `GET /api/v1/identity/integrations/{id}/webhooks` | The endpoints, oldest first |
| Create | `POST /api/v1/identity/integrations/{id}/webhooks` | `201` the endpoint and its secret; a test event is queued |
| Read | `GET …/webhooks/{webhookId}` | The endpoint |
| Change | `PATCH …/webhooks/{webhookId}` with `If-Match` | The endpoint; the description is cleared when absent |
| Delete | `DELETE …/webhooks/{webhookId}` | `204`; its deliveries go with it |
| Disable | `POST …/webhooks/{webhookId}/disable` | The endpoint; its pending deliveries are settled |
| Enable | `POST …/webhooks/{webhookId}/enable` | The endpoint, with no failure counted; a test event is queued |
| Roll the secret | `POST …/webhooks/{webhookId}/roll-secret` with `overlapHours` | `201` the endpoint and its new secret |
| Send a test event | `POST …/webhooks/{webhookId}/test` | `202` the queued delivery's `deliveryId` |
| The catalogue | `GET /api/v1/identity/webhook-events?integrationId=` | Every event with its resource, path, permissions and whether the integration may receive it |

### Refusals

| Status | Code | When |
| --- | --- | --- |
| `400` | `invalid_request` | A URL outside its rules, a description over 500 characters, not 1 to 50 events, an overlap outside 1 to 168 hours, or an `If-Match` that is not a version |
| `404` | `not_found` | No such integration or endpoint |
| `409` | `webhook_limit_reached` | The integration has 5 endpoints or the workspace 50 |
| `409` | `integration_archived` | Creating, changing, enabling, rolling or sending a test event on an archived integration |
| `409` | `webhook_endpoint_disabled` | Sending a test event to a disabled endpoint |
| `412` | `version_mismatch` | The endpoint changed since the `If-Match` version |
| `422` | `webhook_event_unknown` | A name the catalogue does not have |
| `422` | `webhook_event_not_permitted` | An event the integration's roles do not let it receive |
| `422` | `webhook_url_blocked` | The host resolves to an address Vantigo never sends to |
| `422` | `webhook_url_unresolvable` | The host does not resolve |

## Tables

Migration `00204` holds every table of the feature in the `identity` schema.

- **`webhook_endpoints`** — an endpoint: its integration (`ON DELETE RESTRICT`; integrations are
  archived, never deleted), URL, description, events (1 to 50), the sealed secret and the
  previous one with its expiry, the status and why it is disabled, `failing_since`,
  `last_success_at` and `disabled_notice_sent_at` for the sender, who made it and when, and its
  version.
- **`webhook_deliveries`** — one message to one endpoint: the outbox event's id (no foreign key,
  since the outbox prunes on its own; none for a ping), its name, `kind` `event` or `ping`, the
  body, `state` `pending`, `succeeded`, `dead` or `skipped` with `state_reason`, the attempts of
  this run and in all, the next attempt, the sender's lease, the last status and error, the
  resend counter, and when it was made and completed. `(endpoint_id, event_id)` is unique, so an
  event fanned out twice makes one delivery. Deleting an endpoint deletes its deliveries.
- **`webhook_attempts`** — one attempt: when, the status code, the duration, an error from a
  closed set (`timeout`, `connection_refused`, `dns`, `tls`, `blocked_address`, `redirect`,
  `http_status`, `too_large`, `other`), at most 512 bytes of a non-`2xx` answer, and whether the
  current secret signed alone or with the previous one.
