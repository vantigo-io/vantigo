# Outgoing webhooks for integrations — research

Research, 2026-11-18, for the follow-up to API access phase 1
(`docs/superpowers/specs/2026-11-17-api-access-design.md` §12; `research/2026-11-17-api-keys-and-api-reference.md`
§4.1). Code references are against `529727b15`, whose latest migration is `00200`.
`srv/` is `apps/server/internal/`. Claims about other products come from their public
documentation, fetched 2026-11-18; "not found" means the documentation did not say.

## 1. What an integrator wants

An integration (`identity.integrations`, user kind `integration`) holds a key and polls. A
webhook turns that into "tell me when an order is confirmed": Vantigo POSTs a small signed
message to an HTTPS URL, the receiver verifies it, then fetches the resource with its key.
That is what makes Zapier, Make and n8n usable, and it is what Tripletex, Stripe and Shopify
offer.

## 2. What comparable products do

| | Stripe | GitHub | Shopify | Tripletex | Fiken |
| --- | --- | --- | --- | --- | --- |
| Payload | Snapshot, or *thin* (type + related object; thin is recommended for new work) | Full event, 25 MB cap | Full object | `{subscriptionId, event, id, value}`; `value` optional, trimmed with `fields` | no public webhooks found |
| Signature | `Stripe-Signature: t=…,v1=…` HMAC-SHA256 over `t.body`, hex; 5 min tolerance in libraries | `X-Hub-Signature-256` HMAC-SHA256 of body; no timestamp | `X-Shopify-Hmac-SHA256` base64; no timestamp | none (a static header you choose) | — |
| Dedupe key | event id | `X-GitHub-Delivery` | `X-Shopify-Webhook-Id` | `id` | — |
| Secret rotation | "Roll secret", old one valid up to 7 days, one `v1` per active secret | none found | app secret; up to an hour to switch | n/a | — |
| Retries | exponential, up to 3 days | **none automatic** | 8 retries, about 4 h | increasing, 30 h | — |
| Auto-disable | not found | not found | 8 consecutive failures deletes the subscription, e-mail to the developer | after a week of failure: `DISABLED_TOO_MANY_ERRORS`, re-enable by PUT | — |
| Ordering | not guaranteed | not documented | not documented | not guaranteed; a `version` field | — |
| Log / resend | 30-day event list, resend within 15 days (dashboard) | 3-day delivery history, redelivery keeps the delivery id | not found | status only | — |
| Timeout / success | 2xx; 3xx is a failure | 10 s, 2xx | 5 s total, only 200 | not found | — |
| Test | CLI trigger, sandbox events | `ping` on create | — | — | — |

**Standard Webhooks** (<https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md>),
which Svix also uses:

- headers `webhook-id` (unchanged across retries), `webhook-timestamp` (Unix seconds) and
  `webhook-signature`, a space-separated list of `v1,<base64>`;
- signed content `{id}.{timestamp}.{raw body}`; `v1` is HMAC-SHA256 keyed by the
  base64-decoded bytes after `whsec_`, output base64;
- secrets are 24–64 random bytes, base64, `whsec_`-prefixed, unique per endpoint;
- rotation: sign with old and new, send both signatures, the receiver accepts any;
- the spec gives no replay tolerance (5 minutes is Stripe's convention); a payload under
  about 20 KB; the body is `type`, `timestamp` (ISO 8601) and `data`;
- delivery: retry over days with backoff and jitter, 15–30 s timeout, only 2xx is success,
  **3xx is a failure and redirects are not followed**, `410 Gone` disables the endpoint,
  `429`/`502`/`504` mean throttle and honour `Retry-After`;
- the published symmetric test vector, from Svix (<https://docs.svix.com/receiving/verifying-payloads/how-manual>):
  secret `whsec_plJ3nmyCDGBKInavdOK15jsl`, id `msg_loFOjxBNrRLzqYUf`, timestamp `1731705121`,
  body `{"event_type":"ping","data":{"success":true}}`, signature
  `v1,rAvfW3dJ/X/qxhsaXPOyyCGmRKsaKWcsNccKXlIktD0=`. A Go test must reproduce it before the
  signer is trusted; it was not recomputed here.

Svix's schedule is immediately, 5 s, 5 min, 30 min, 2 h, 5 h, 10 h, 10 h (about 27.5 h),
disabling an endpoint after 5 days of failure.

**What we take from them**

1. Thin events, signed by Standard Webhooks, no redirects, 2xx only: this is the consensus,
   and the one design Stripe now recommends.
2. Nobody guarantees order; everybody tells receivers to dedupe on an id. We promise the
   same and give a monotonic event sequence.
3. Auto-disable plus a notice to a human is universal; the thresholds vary from 8 failures to
   a week. A time-based rule is kinder to a receiver that is down for a weekend.
4. Resend is a log feature with a window, and it keeps the message id.

## 3. What our outbox can and cannot do

`srv/events` (migration `00117`): `platform.events` and `platform.event_deliveries`.

- **Handlers are static.** `Module.EventHandlers` returns handlers composed at start. The
  dispatcher builds its subscription triples once (`dispatch.go:149`); fan-out writes one
  `event_deliveries` row per composed handler (`fanOutSQL`, `:180`). There is **no dynamic
  subscriber**, and no backfill for a handler added later.
- **A handler must not leave the process.** `Handle` runs on the delivery transaction under a
  1-minute timeout (`events.go:36-41`); it writes its own module's schema only. A webhook POST
  in `Handle` would hold a pooled connection while a stranger's server answers.
- **Ordering** is per handler, aggregate and aggregate id, and a failed or dead delivery
  blocks later ones of that aggregate (`claimSQL`). That is right for in-process effects and
  wrong for a customer's endpoint, where one dead endpoint must not stall anything else.
- **Retries** are 1 m, 5 m, 30 m, 2 h, 12 h, 8 attempts, then `dead` for an operator.
- **Retention**: events are pruned 30 days after creation once every delivery is `done`; a dead
  delivery pins its event forever.
- **Payload**: `Publish` stores `json.Marshal(event)`; the envelope (name, version, module,
  aggregate id, occurred_at) lives in columns. There is no actor and no tenant. Contract
  rule O3: events carry ids, numbers, amounts and states, never names, addresses, e-mails or
  phones (`contracts/events.go:9-12`).
- **Events**: about 37, all version 1, in `srv/contracts/events_*.go` (accounting 3,
  communications 4, expenses 8, invoices 5, manufacturing 1, orders 12, purchasing 4, time 1).
  The resource id has a different field name per event (`orderId`, `invoiceId`, `receiptId`…)
  and a few aggregate ids are not the resource (`purchasing.receipt.*` aggregates on the
  order; `time.week.unsubmitted` on `userId|weekStart`).

**The shape that follows.** One static handler, `identity.webhooks`, takes every
webhook-able event. Its `Handle` runs on the delivery's transaction, reads identity's own
subscriptions and inserts **one row per matching endpoint** into `identity.webhook_deliveries`
(in identity's schema, no network). A separate leased worker claims those rows and does the
HTTP POST with its own retry state, so a slow receiver blocks only itself. The outbox's
fan-out then protects "an event is never lost between a state change and the queue", which is
all it has to.

## 4. Security of a customer-supplied URL

`srv/netguard` already has what matters: `DialContext` resolves once, refuses the whole host
if any address is private, loopback, link-local (the 169.254.169.254 metadata address),
CGNAT, multicast, Teredo, NAT64 and the IPv4-in-IPv6 forms, then dials the checked IP
literal, so a DNS answer cannot change between check and connect. SNI and certificate
verification still use the host name.

It does **not** check scheme, port, userinfo, redirects, timeouts or body size; each caller's
transport does. The reference is `srv/calendar/subscriptions_fetch.go:38-131`: `Proxy: nil`,
`DisableKeepAlives`, `netguard.DialContext`, dial and TLS 10 s, response header 15 s,
`CheckRedirect → http.ErrUseLastResponse`, a limited body read. Seven modules copy that
transport "because no module imports another's code" (`:80-82`); identity needs its own
copy, or a shared constructor in `netguard` (see decision candidate W20 in the spec).

What the webhook client adds: `https` only; port 443 by default, an allow-list of ports is
an owner question; no userinfo; no redirects; response body **not stored** beyond a short
excerpt and never shown as markup; a save-time resolve for friendly errors (not a boundary,
DNS changes); and the same guarded dial on every retry. A development install needs a way
to deliver to `localhost`; netguard has no allow-list, so tests inject a transport as
`deps.HTTPTransport` does elsewhere, and the manual check uses a tunnel.

## 5. Permissions: what an event may tell a receiver

There is **no mapping from event to permission**, and no module contract of the form "may
user X read resource Y". Authorization is a permission key per route, and row-level scope is
each module's own. An integration's reach is `Access.Scope` (held permissions minus
`APINever`, cut by the key's narrowing). Consequences:

- A subscription must be limited to events whose *view* permission the integration holds.
  That needs a catalogue, in `contracts`, giving each exposed event its resource type, the
  payload field holding the resource id, its REST path, and the permission that reads it.
- The check runs at **subscribe time** (so the screen offers only what can work) and again at
  **fan-out time** (roles change; a revoked role stops the stream at once). A skipped event
  is logged as such, not silently dropped.
- Keys are narrowable and webhooks belong to the integration, not a key. The webhook
  therefore follows the integration's *roles* (the widest key), and the receiver's fetch is
  what applies the key's narrowing. The thin event leaks only "an order with id 42 was
  confirmed".
- Events with user ids (`communications.*`, `time.week.unsubmitted`) or a person's money
  (`expenses.*`) concern people, not business documents. API access K53 already marks
  Staff's personal data and payroll `never`. They are left out of the first release.

## 6. Where the pieces go

- Tables in `identity`: `webhook_endpoints`, `webhook_deliveries`, `webhook_attempts`
  (and a notice dedupe row, as `api_key_notices`).
- Signing secrets must be recoverable for HMAC, so they use `secrets.Box`
  (AES-256-GCM, HKDF per purpose string; `srv/secrets/secrets.go`) with a new purpose, as
  TOTP and OIDC client secrets do. API keys are hashed only; that does not apply here.
- A leased worker following identity's `RunCycle` shape, but claiming rows with
  `FOR UPDATE SKIP LOCKED` and `lease_until` as the dispatcher does, not one global advisory
  lock (a queue needs concurrency). It never holds a pooled connection while asking for
  another.
- Owner-tier operations (`policy:OwnerManagement`, `x-vantigo-api: never`) as for
  integrations; audit actions `webhook.*` in `identity.authorization_audit`, with no secret in
  a snapshot.
- Notices to the contact and the Owners reuse `sendAPIKeyNotice`'s recipient set.
- The admin screen is a tab on the integration's page
  (`apps/host/frontend/src/routes/workspace/-api-access-integration.tsx`), modelled on the
  request log (`-api-access-requests.tsx`).

## 7. Questions for the owner, with recommended defaults

1. **Which events?** *Default:* business documents only: orders (12), invoices (5),
   purchasing (4), manufacturing (1), accounting (3) — 25 events. Left out: communications,
   time, expenses (they concern people). Each is added deliberately, with its permission
   mapping, never by "all events".
2. **Filtering by permission?** *Default:* the integration must hold the event's view
   permission, checked on subscribe and again at fan-out. A key's narrowing is applied by the
   receiver's fetch, not by the stream.
3. **Own permission or the roles?** *Default:* no new permission key. Managing endpoints is
   the Owner tier like keys; what an endpoint may receive follows the integration's roles.
4. **Secret rotation.** *Default:* `whsec_` secret per endpoint, shown once; "Roll secret"
   keeps the previous one valid for 24 hours (owner may choose up to 7 days), both signatures
   sent meanwhile.
5. **Retries.** *Default:* 8 attempts at 0, 30 s, 5 min, 30 min, 2 h, 6 h, 12 h, 24 h (about
   45 hours), jittered ±10%, `Retry-After` honoured up to 1 hour; a per-attempt timeout of
   15 s; only 2xx succeeds; no redirects.
6. **Auto-disable.** *Default:* an endpoint is disabled when it has had no success for
   5 days and at least 5 deliveries have died in that time, or at once on `410 Gone`. A
   notice goes to the contact and the Owners. Re-enable is manual and sends a test event.
7. **Ordering.** *Default:* none promised across events. Each message carries a monotonic
   `sequence` (the outbox event id) and `occurred_at`; the receiver fetches current state. A
   dead delivery never blocks another.
8. **Log and retention.** *Default:* one row per delivery with an attempts child (time,
   status code, duration, error sentence, 1 KiB response excerpt as plain text), 30 days like
   the request log, resend within that window keeping `webhook-id`.
9. **Ports and local targets.** *Default:* HTTPS on port 443 only; no private targets, no
   exceptions. A development install uses a tunnel.
10. **Limits.** *Default:* 5 endpoints per integration, 50 per workspace, 3 resends per
    delivery per minute.
11. **Test event.** *Default:* a "Send test event" button and an automatic `webhook.ping` on
    create and on re-enable, outside the retry machinery.
12. **Idempotency keys for creating operations** (deferred in API access §12 "until webhooks"):
    *Default:* not in this phase; `webhook-id` gives receivers their dedupe, and an
    `Idempotency-Key` on writes is its own change.
