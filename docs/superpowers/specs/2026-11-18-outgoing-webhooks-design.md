# Outgoing webhooks for integrations — design

Revision 2, 2026-11-18 (revision 1 followed by an adversarial review against the code; its
findings are folded in as **§13, which wins where it differs from the text above**). Builds on API access phase 1
(`specs/2026-11-17-api-access-design.md`, K1–K61; its §14 amendments win over its earlier
text) and on the research `research/2026-11-18-outgoing-webhooks.md` ("R"). The owner's
questions in R §7 take R's recommended defaults here; §12 says what overturning each would
change. Decisions are numbered W*n*. Code references are against `529727b15`, whose latest
migration is `00200`. `srv/` is `apps/server/internal/`; `host/` is
`apps/host/frontend/src/`.

## 1. Purpose

An integration subscribes to domain events. Vantigo POSTs each one, as a small signed JSON
message, to an HTTPS URL the Owner gave. The receiver verifies the signature, then fetches the
resource with its API key, so the permission check stays in one place. Failures are retried
with backoff; a delivery log shows every attempt and can resend.

This is a feature of the identity module (as API access is), shown as a **Webhooks** tab on an
integration's page in *Workspace admin › API access*. Norwegian: **Webhooks** (the word is
used as is), the tab *Webhooks*.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **A** | Schema (all tables), the event catalogue, endpoint management (create, edit, enable, disable, delete, secret and rolling it), the signer, the guarded HTTP client, audit, the screen's endpoints tab, reference + admin pages | — |
| **B** | The fan-out handler, the sender worker, retries, auto-disable and its notice, the test event | A |
| **C** | The delivery log (API + screen), resend, retention, the landing page | B |

**Not in this phase:** webhooks that carry the resource (snapshot payloads); events about
people (§3); an `Idempotency-Key` on writes; per-endpoint custom headers; batching several
events in one POST; a webhook API for keys (endpoints are managed by Owners in the screen);
asymmetric signing (`v1a`/`v2a`); a replay-missing/backfill tool; receivers other than HTTPS.

## 2. Decisions — the model

- **W1 — An endpoint belongs to an integration.** Table `identity.webhook_endpoints`:

  | Column | Meaning |
  | --- | --- |
  | `id` | uuid PK |
  | `integration_id` | FK `identity.integrations(user_id)` ON DELETE RESTRICT |
  | `url` | `https://…`, at most 2,048 characters (W11) |
  | `description` | optional, up to 500 characters |
  | `events` | `text[]` of catalogue names (W6), 1–50 entries, no duplicates |
  | `secret_ciphertext`, `previous_secret_ciphertext`, `previous_secret_expires_at` | W8, W9 |
  | `status` | `enabled` \| `disabled` |
  | `disabled_reason` | `manual` \| `failing` \| `gone` \| `integration_archived`; null when enabled |
  | `disabled_at`, `failing_since`, `last_success_at`, `disabled_notice_sent_at` | W17, W18 |
  | `created_by`, `created_at`, `updated_at` | `created_by` FK ON DELETE SET NULL |
  | `version` | uuid, optimistic concurrency (`If-Match`), as identity's other rows |

  At most **5 endpoints per integration** and **50 per workspace** (409
  `webhook_limit_reached`). Deleting an endpoint is a hard delete that cascades its deliveries
  (the audit row keeps the fact, W24).
- **W2 — Archiving or disabling an integration.** Archiving disables every endpoint
  (`integration_archived`, in the archive's transaction) and dead-letters its pending
  deliveries. Disabling an integration creates no new deliveries for it (fan-out skips it, W14)
  and the sender does not send its pending ones; they are sent when it is enabled again, unless
  the endpoint has been disabled meanwhile.
- **W3 — Who manages endpoints.** Every operation is `x-vantigo-access: policy:OwnerManagement`
  and `x-vantigo-api: never`, exactly as the integrations' operations (K21). **No new
  permission key.** What an endpoint may *receive* follows the integration's roles (W7).

## 3. Decisions — the events

- **W4 — Exposed events are an allow-list, not "all events".** A catalogue in `srv/contracts`
  (`webhook_events.go`) lists, for each exposed event:

  | Field | Meaning |
  | --- | --- |
  | `Name`, `Version` | the outbox event (version 1 only) |
  | `Resource` | the resource type a receiver sees, e.g. `sales_order` |
  | `IDField` | the JSON field of the event's payload holding the resource id, e.g. `orderId` |
  | `Path` | the REST path of the resource, e.g. `/api/v1/orders/sales-orders/{id}` |
  | `Permission` | the key that reads it, e.g. `orders:view` |

  A test holds the catalogue to the code: each name is a published contract event; each
  `IDField` exists in its struct; each `Permission` is in the permission catalogue, is
  `Delegable` and is not in `APINever`; each `Path` is an operation of the composed OpenAPI
  contract with a `GET`. `reference/webhooks.md` must list every name (a test reads the page).
- **W5 — The first release exposes business documents: 25 events** — orders (12), invoices
  (5), purchasing (4), manufacturing (1), accounting (3). **Left out:** `communications.*`
  (user ids, conversations), `time.week.unsubmitted` (a person's hours) and `expenses.*`
  (a person's claims). They concern people; K53 already keeps Staff's equivalents `never`.
  Adding one is a catalogue entry, a test and a docs line, decided event by event.
  `invoices.customer_credit.changed` is exposed only if its resource has a `GET` path
  (W4's test decides; otherwise it is left out and listed in the spec's revision log).
- **W6 — Subscribing.** An endpoint lists names from the catalogue; `*` is not accepted and
  there are no prefixes. The screen groups the events by module and shows only those the
  integration's roles let it receive (W7); the API refuses the rest with 422
  `webhook_event_not_permitted` (and an unknown name with 422 `webhook_event_unknown`).
- **W7 — Filtering by the integration's permissions.** An event is delivered to an endpoint
  only while the integration **holds the event's `Permission`**, evaluated as the integration's
  roles allow (`EffectivePermissionForUser`, the Owner/SystemAdmin shortcuts do not apply to an
  integration, and `APINever` is refused) — checked at **subscribe time** and again at
  **fan-out time** (W14), so a role removed stops the stream at once. A key's narrowing is not
  applied: the stream belongs to the integration, the receiver's fetch is what its key
  narrows. The thin payload (W12) discloses the resource's type, id and that it happened.
  A permission that was lost is not an error on the endpoint: the event is recorded as
  `skipped` (`permission_not_held`) in the log, so the cause is visible.

## 4. Decisions — the message

- **W12 — The body is thin and fixed at fan-out.** One JSON object, stored as text so a resend
  sends the same bytes:

  ```json
  {
    "id": "evt_8421",
    "type": "orders.sales_order.confirmed",
    "version": 1,
    "timestamp": "2026-11-18T10:15:00Z",
    "sequence": 8421,
    "data": { "resource": { "type": "sales_order", "id": "42", "href": "/api/v1/orders/sales-orders/42" } }
  }
  ```

  - `id` is `evt_` + the outbox event id and `sequence` that number; both increase but have
    gaps (events are shared by all endpoints and by the subscribers of other events).
  - `timestamp` is the event's `occurred_at` (UTC, RFC 3339).
  - `href` is the path only; the receiver already knows the workspace's address (a base URL
    that was wrong would be a bug the receiver cannot see). The reference page says so.
  - The body carries **no amount, no name and no other field of the event**.
  - `type: "webhook.ping"` is the test event (W19): `data: {}`, no `sequence`.
- **W13 — Signing is Standard Webhooks `v1`.** Headers on every request:

  | Header | Value |
  | --- | --- |
  | `webhook-id` | `msg_` + the delivery's uuid without dashes; **unchanged across retries and resends** |
  | `webhook-timestamp` | Unix seconds at *this attempt* |
  | `webhook-signature` | `v1,<base64 HMAC-SHA256 of "{id}.{timestamp}.{body}">`, one entry per active secret (W9), space-separated |
  | `Content-Type` | `application/json` |
  | `User-Agent` | `Vantigo-Webhooks/1` |

  The key is the base64-decoded bytes after `whsec_`. A Go test reproduces Svix's published
  vector (R §2) before anything else is built on the signer. The reference page documents the
  verification in prose, a five-minute tolerance and deduping on `webhook-id`.

## 5. Decisions — fan-out and sending

- **W14 — One static outbox handler fans out.** `identity.webhooks` is added to identity's
  `EventHandlers` (`srv/module/workers.go:22-41`); its `Events` are the catalogue. `Handle`
  runs on the delivery's transaction and, in identity's own schema and with no network:
  1. selects the `enabled` endpoints whose integration is active (not archived, not
     disabled) and whose `events` contain the event name;
  2. per distinct integration, checks W7's permission;
  3. inserts one `webhook_deliveries` row per endpoint, `ON CONFLICT (endpoint_id, event_id)
     DO NOTHING` (the outbox is at-least-once), with the body of W12 built from the event's
     `Payload` and `IDField`. State `pending`, or `skipped` when W7 fails.
  An event whose payload lacks its `IDField` is a bug: `Handle` returns an error, the outbox
  retries and then holds it dead for the operator, as for any handler. There is **no
  backfill**: an endpoint created now sees events published after the fan-out that follows.
- **W15 — The sender is a leased worker, `identity.webhooks`.** It runs where identity's
  other worker runs (`srv/identity/workers.go`), switched by `IDENTITY_WEBHOOKS_ENABLED`
  (default true) and polling every 2 s (`IDENTITY_WEBHOOKS_POLL`). Each pass it claims up to
  20 due deliveries — `pending`, `next_attempt_at <= now`, lease free or expired, endpoint
  enabled, integration active — `FOR UPDATE SKIP LOCKED`, setting `lease_id` and a lease of
  2 minutes (longer than one attempt, W16); **at most 4 in flight per endpoint**. It then
  sends up to 8 concurrently. It never holds one pooled connection while it asks for another
  (it runs on a pool of one): the claim, the send and the result are three separate uses.
  Several worker processes are safe.
- **W16 — An attempt.** Decrypt the secrets, sign (W13), POST with the guarded client (W20),
  15 seconds in all. Only **2xx** succeeds. Everything else, including 3xx (never followed)
  and a TLS or DNS failure, fails the attempt. The result is one `webhook_attempts` row (W21)
  and the delivery's update, on a short transaction.
  - **410 Gone** marks the delivery dead and disables the endpoint (`gone`, W18).
  - **429, 502, 503, 504** with `Retry-After` (seconds or a date) delay the next attempt to
    the later of the schedule and the header, capped at 1 hour.
  - A lease that expired mid-attempt is not an attempt that happened; the next claim retries.
- **W17 — Retry schedule.** A run has **8 attempts**: now, then after 30 s, 5 min, 30 min,
  2 h, 6 h, 12 h and 24 h (about 45 hours in all), each delay jittered ±10%. The eighth
  failure makes the delivery `dead`. A resend (W22) starts a fresh run of 8.
- **W18 — When an endpoint is disabled.** On a failed attempt `failing_since` is set if null;
  on a success it is cleared and `last_success_at` set. After each pass, an enabled endpoint
  with `failing_since` older than **5 days** and **at least 5 dead deliveries** since is
  disabled (`failing`), as an endpoint that returned 410 is at once (`gone`). Disabling:
  - dead-letters the endpoint's pending deliveries (`endpoint_disabled`), so nothing stale is
    sent when it is enabled again;
  - sends **one notice** (en/nb, per recipient, as `sendAPIKeyNotice` does) to the integration's
    contact and every Owner, deduplicated by `disabled_notice_sent_at`;
  - writes the audit action `webhook.disabled` with `system:webhooks` as actor.
  Re-enabling is manual (Owner), clears `failing_since`, and sends a ping (W19).
- **W19 — The test event.** The *Send test event* button and a successful create or
  re-enable queue a `webhook.ping` delivery with **a run of 1 attempt** (no retries, never
  counted toward W18). It appears in the log with kind `ping`.
- **W20 — The guarded client.** `srv/identity/webhooks_client.go` builds the transport of
  `srv/calendar/subscriptions_fetch.go:38-131`: `Proxy: nil`, `DisableKeepAlives`,
  `DialContext: netguard.DialContext(resolver, dialer)` (every address checked, the checked
  literal dialled, so DNS cannot change between the check and the connect), dial and TLS 10 s,
  response header 10 s, `MaxResponseHeaderBytes` 64 KiB, `CheckRedirect → ErrUseLastResponse`,
  and a body read capped at 1 KiB and discarded after. It is a process singleton; tests inject
  a transport through `Deps.HTTPTransport`, as the other modules do. The copy is deliberate:
  no module imports another's code (the comment at `subscriptions_fetch.go:80-82`).
- **W21 — Ordering: none is promised.** Messages for one endpoint are sent roughly in
  creation order, up to 4 at a time, and a failing delivery never holds another back.
  Receivers use `sequence`/`timestamp` to discard what is older than they have seen, and
  `webhook-id` to dedupe. Because the body is thin and the receiver fetches current state, an
  out-of-order message is harmless. The reference page says all of this.
- **W22 — Resend.** An Owner may resend a `succeeded` or `dead` delivery within its retention
  (W23) while the endpoint is enabled and the integration still holds the event's permission
  (409 `webhook_endpoint_disabled` / `webhook_event_not_permitted`). It keeps `webhook-id` and
  the body, takes a new timestamp and signature, and starts a fresh run (W17). Three resends
  per delivery per minute (429 on more).

## 6. Decisions — the log, secrets, URLs, audit

- **W23 — The log and its retention.** `identity.webhook_deliveries` (id uuid, endpoint,
  `event_id`, `event_name`, `kind` event|ping, `body`, `state`
  `pending`|`succeeded`|`dead`|`skipped`, `run_attempts`, `total_attempts`, `next_attempt_at`,
  lease columns, `last_status`, `last_error`, `created_at`, `completed_at`) and
  `webhook_attempts` (delivery, `at`, `status_code`, `duration_ms`, `error`, `excerpt`,
  `secret_used` current|previous). `event_id` has no FK to `platform.events`, which prunes on
  its own schedule. The worker deletes deliveries completed more than **30 days** ago, in
  batches of 1,000, the request log's period. `error` is a fixed sentence from a closed set
  (`timeout`, `connection_refused`, `dns`, `tls`, `blocked_address`, `redirect`, `http_status`,
  `too_large`, `other`), never an error string from the network stack. `excerpt` is at most
  512 bytes of the response, for a non-2xx status only, with control characters stripped,
  shown as plain text.
- **W8 — Secrets.** `whsec_` + base64 of 32 random bytes, unique per endpoint, **shown once**
  (in the create and roll responses, `Cache-Control: no-store`, as a key is) and kept sealed
  with `secrets.Box`, purpose `identity/webhook-secret` (AES-256-GCM, the purpose is also the
  AEAD data). No endpoint returns a secret again; the screen says to roll it if it is lost.
  Neither the audit snapshot nor a log line carries a secret or its ciphertext.
- **W9 — Rolling.** *Roll secret* generates a new secret, keeps the previous one as
  `previous_secret_*` and sends **both signatures** until `previous_secret_expires_at`
  (default 24 hours, the Owner chooses 1 hour to 7 days), after which the sender ignores and
  clears it. Rolling twice replaces the previous one at once (the older secret is gone), which
  the screen warns about when an overlap is still running.
- **W10 — Editing.** URL, description and events are editable with `If-Match`. Changing the
  URL does not roll the secret. Deliveries already queued go to the URL as it is *at the
  attempt*; the audit row has before and after.
- **W11 — URL rules.** `https` only; a host name (an IP literal, `localhost` and names
  ending `.localhost`, `.local`, `.internal` are refused); no userinfo; no fragment; port
  empty or 443; at most 2,048 characters. At save time the host is resolved and refused when
  any address is disallowed by `netguard` (422 `webhook_url_blocked`) or does not resolve (422
  `webhook_url_unresolvable`). **That is advice, not the boundary:** the dial in W20 checks
  again on every attempt. A query string is allowed (receivers use tokens in it) and is
  **never logged**, nor placed in an audit snapshot or a notice; the screen shows the URL
  to Owners in full.
- **W24 — Audit.** Actions in `identity.authorization_audit` through `writeAudit`
  (`srv/identity/audit.go`): `webhook.created`, `.updated`, `.deleted`, `.enabled`,
  `.disabled`, `.secret_rolled`, `.test_sent`, `.resent`. A snapshot has the endpoint's id,
  integration, URL without the query, events and status; never a secret.
- **W25 — API.** Under `/api/v1/identity/integrations/{id}/webhooks` and
  `/api/v1/identity/webhook-events`; every operation Owner-only, `x-vantigo-api: never`
  (also added to `S/openapi/apimarks_test.go`'s golden list), with limits in identity's
  `limits` map (30/min writes, as `policyUserManagement`):

  | Operation | Path |
  | --- | --- |
  | list, create | `GET`/`POST …/webhooks` (create returns the secret) |
  | read, update, delete | `GET`/`PATCH`/`DELETE …/webhooks/{webhookId}` |
  | roll secret | `POST …/webhooks/{webhookId}/roll-secret` |
  | enable, disable | `POST …/webhooks/{webhookId}/enable` · `/disable` |
  | test event | `POST …/webhooks/{webhookId}/test` |
  | deliveries (C) | `GET …/webhooks/{webhookId}/deliveries` (cursor, `state`) · `GET …/deliveries/{deliveryId}` (attempts) |
  | resend (C) | `POST …/deliveries/{deliveryId}/resend` |
  | catalogue | `GET /api/v1/identity/webhook-events?integrationId=` (names, resource, whether the integration may receive it) |

## 7. Platform, registries and documentation

- **Migration `00204`** (renumbered from `00201`, which main took; verify against `origin/main` and open PRs right before merging,
  `[[migration-number-collisions]]`) holds all tables, so B and C add none.
- **Config:** `IDENTITY_WEBHOOKS_ENABLED`, `IDENTITY_WEBHOOKS_POLL` in `srv/config`,
  listed in `admin/configuration.md`. A worker switched off sends nothing and delivers
  nothing; rows queue up and are sent when it is back (they expire after 30 days).
- **No new module**, so Adding-a-module's registries do not apply. `promptVersion` and the
  Assistant's link prefixes are untouched; `go generate` runs in `apps/server` after the docs
  edit (`[[docs-edit-needs-go-generate]]`).
- **Documentation** (AGENTS.md; `sources` frontmatter declares the paths):
  - A: `reference/webhooks.md` (en; the message, headers, verification, events,
    retries, ordering, limits), `admin/api-access.md` en+nb (a Webhooks section: add an endpoint,
    roll a secret, what the Owner sees when one is disabled), `reference/identity.md` (tables,
    permissions, errors), `admin/configuration.md` en+nb.
  - B: the same pages gain retries, disabling and the notice.
  - C: the log and resend sections.
  - The generated API reference regenerates from `openapi/identity.yaml`; never by hand.
- **Landing:** the API access / integrations page in `vantigo-landing` says the app offers
  webhooks, once C is on `main`, from the admin guide's words. If the repository is not
  checked out, the hand-over names it (AGENTS.md "The public site").
- **Frontend:** `host/routes/workspace/-api-access-webhooks.tsx` (endpoints),
  `-api-access-webhook-deliveries.tsx` (log), a secret dialog beside `-api-key-dialog.tsx`,
  `host/api/webhooks.ts`, keys `apiAccess.webhook*` in `catalogs/api-access.ts` in en and nb.

## 8. Edge cases

- **The same event reaches an endpoint twice** (outbox at-least-once): the unique
  `(endpoint_id, event_id)` makes the second insert a no-op.
- **Two events for the same resource** in quick succession: two messages; the receiver's
  fetch returns the same current state twice.
- **The receiver is slow, not down:** 15 s, then it is a failed attempt and retried. Four in
  flight per endpoint bounds what a slow receiver costs.
- **An endpoint's host starts resolving to a private address:** every attempt fails with
  `blocked_address`; after W18 it is disabled and the Owner is told.
- **The Owner deletes an endpoint with deliveries in flight:** the delivery's update finds no
  row and the attempt is dropped; nothing is retried.
- **A role is removed from the integration while deliveries are queued:** queued deliveries
  are sent (the thin message discloses nothing new and the fetch is refused); new events are
  `skipped` (W7).
- **Clock skew:** the receiver's tolerance is its business; Vantigo's clock is the installation's.
- **A large burst** (an import confirming thousands of orders) fans out to rows, sent at the
  worker's pace; the log is bounded by W23's retention.
- **Integration deleted:** integrations are archived, never deleted (K8), so `RESTRICT`
  never meets a delete.

## 9. Testing

- Signer: Svix vector; two-secret header; constant-time compare helper for tests.
- Catalogue test (W4), `reference/webhooks.md` names test, apimarks golden list.
- Handler: fan-out per endpoint, permission skip, disabled integration, duplicate event,
  missing `IDField` error, runs on a transaction and on a pool of one.
- Sender: with an `httptest` TLS server injected through `Deps.HTTPTransport`: 2xx, 5xx
  retry schedule (injected clock), 3xx not followed, `Retry-After`, 410, timeout, lease
  expiry, 4-per-endpoint cap, secret roll overlap (two signatures), pruning.
- Client: the guard refuses loopback, metadata, IPv4-mapped and rebinding names; no redirect;
  the excerpt is capped; the URL query never reaches a log line (a test captures `slog`).
- API: Owner-only, a key refused `api_not_available`, `If-Match`, limits, URL rules, the
  secret returned once and never again, the audit snapshot contains no secret.
- Auto-disable and notice: once, to the contact and the Owners, per language.
- Frontend (vitest): the endpoint form, the one-time secret dialog (nothing in storage), the
  log filters, nb/en parity (`catalogs/parity.test.ts`).
- The manual browser check (needs a running instance and a tunnel or a public receiver).

## 10. Later

- Snapshot payloads and a field selector; events about people, one by one; webhooks managed
  with a key; custom headers; `Idempotency-Key` on writes; batching; replay-missing; MCP
  (separate spec).

## 11. Revision log

- **1** (2026-11-18): first draft from R.
- **2** (2026-11-18): the review's 22 findings, §13.
- **3** (2026-11-18, as built in delivery A):
  - **24 events, not 25.** W26's test (`internal/integration/webhook_events_test.go`) passed
    every entry but `accounting.supplier_invoice.deleted`: its draft is hard-deleted with it
    (`accounting/supplier_invoices.go`, `DeleteAccountingSupplierInvoice`), so
    `GET /accounting/supplier-invoices/{id}` can only answer 404. It is left out, and
    `reference/webhooks.md` says so. Every other GET is `permission:<module>:access` (or
    `accounting:read`), delegable, and not `x-vantigo-api: never`.
  - Credit notes: `GET /invoices/{id}` serves them (`kind: credit_note`), so
    `invoices.document.issued` is exposed with resource `invoice`; `customer_credit.changed`
    is exposed (resource `customer_credit`, `IDField: customerId`).
  - The catalogue entry carries `Version` beside the zero-value `Event`; the test asserts
    `Version == Event.EventVersion() == 1`.
  - `webhook_deliveries` gains `state_reason` (`permission_not_held`, `attempts_exhausted`,
    `gone`, `endpoint_disabled`, `integration_archived`, `expired`) and `max_run_attempts` (8,
    or 1 for a ping), which W7, W18, W19 and W30 need stored. `webhook_attempts.secret_used`
    is `current` | `both` (whether the previous secret signed too), not `current` | `previous`.
  - W11 also refuses a single-label host (no dot), which only a search domain could resolve.
  - W28's "short hash" is a keyed digest: `secrets.Box.MAC` under its own purpose
    `identity/webhook-url-digest`, 8 bytes in hex, so a path token cannot be guessed back
    from the audit row.
  - The 50-per-workspace count leaves out the endpoints of archived integrations. On an
    archived integration, disable is answered as it is and delete is allowed; create, PATCH,
    enable and roll are 409 `integration_archived` (W36).
  - `GET /webhook-events` requires `integrationId`. The writes are in identity's `limits`
    map at `policyUserManagement`; the reads are not limited, as the integrations' are not.
  - The test, deliveries and resend operations are declared with B and C, not in A's
    contract: identity's operation-coverage gate fails on an operation no test exercises.
  - The pages say plainly that A sends nothing yet.
- **4** (2026-11-18, delivery A's code and security reviews):
  - The migration is `00204`: main took `00201` (invoices) and `00202` (customers).
  - Checks: no NULL or empty event name; a pending delivery has a `next_attempt_at`;
    `state_reason` is tied to its state (none for pending or succeeded,
    `permission_not_held` for skipped, the dead reasons for dead). A partial index
    `(endpoint_id, created_at, id) WHERE state = 'pending'` serves B's claim, so B adds no
    migration. Every statement locking several endpoints locks them `ORDER BY id` (B's
    fan-out `FOR SHARE` included).
  - W8 binds a secret to its endpoint: the purpose is `identity/webhook-secret/<endpoint id>`,
    which the box also uses as AEAD data, so a ciphertext moved to another row never opens.
  - W11: the host is normalised with IDNA's lookup profile (lowered, trailing dot dropped,
    punycode, full-width dots mapped) before every check, and that ASCII form, without an
    explicit `:443`, is the URL stored. The contract carries the bounds: `url` 2,048,
    `description` 500, `events` 1–50 of at most 100 characters, `overlapHours` 1–168.
  - W20: the webhook client is always built on netguard's dialler. `Deps.HTTPTransport` never
    replaces it; tests swap only the resolver, the dialler and the trusted roots
    (`Access.webhookNet`). An attempt whose context the caller ended is `aborted` and is
    recorded as nothing (W16, W38). `Retry-After` is parsed into a delay of at most an hour,
    for 429/502/503/504 only, never kept as text. The excerpt also blanks format characters
    (`Cf`: bidirectional overrides, zero-width joiners). `too_large` is told by net/http's
    words, which have no sentinel; a test holds them.
  - W24: the audit snapshot leaves out the description (free text may hold what the path held).
  - W9: a previous secret whose overlap has ended is cleared by the endpoint's next write;
    B's sender must sign with it only while `previous_secret_expires_at` is after the attempt.
    The screen warns before a roll that would end a running overlap, and the server still
    ends it at once.
  - W7: the save-time permission check reads the roles unlocked; a role taken while a save
    commits is caught by the fan-out and the claim (W33). Accepted.
  - `contracts.WebhookEvent.Href(id)` fills the resource path's one parameter, whatever its name.
  - The workspace limit is tested under concurrency across integrations (48 seeded, racing
    creates on four integrations stop at exactly 50).
- **5** (2026-11-18, as built in delivery B):
  - **Worker-mode deps carry the catalog.** The fan-out and the claim decide W7 by the
    composed permission catalog (`apiNeverIn`), which a worker-only process never had:
    `module.EventHandlers` and `module.WorkerDeps` now set `Deps.Catalog` from the enabled
    modules' permissions when the caller set none. Without it every event would have been
    `skipped` in worker mode.
  - **The claim is two statements in one transaction**, W29 (a) then (b-d): in READ COMMITTED
    a single statement's snapshot predates its `FOR UPDATE` lock, so it could miss leases a
    rival committed in between and pass the cap of 4. The second statement's fresh snapshot
    sees them. Its round-robin is `ORDER BY rank, created_at, id` limited to the process's
    free slots.
  - **Lock order.** A result's transaction holds the endpoint `FOR UPDATE` first, then updates
    the delivery under its lease (W38), as disable, archive and delete do; the reverse would
    deadlock with a disable settling the same rows.
  - A ping's result touches neither `failing_since` nor `last_success_at`; a 410 to it still
    disables (W37). The endpoint's `version` is not bumped by the sender's writes, so an
    Owner's `If-Match` is not invalidated by traffic.
  - The auto-disable check (W18) runs after every pass; the expiry, the prune and the sweep of
    notices still owed run at most once a minute, so a notice nobody could be mailed is not
    retried every 2 s. A 410 and an auto-disable mail their notice at once.
  - The integration-reenable dead-lettering (W30) uses `state_reason` `expired`.
  - `POST …/test` answers `202 {deliveryId}`, refuses a disabled endpoint with 409
    `webhook_endpoint_disabled`, and audits `webhook.test_sent` with the delivery's id; it is
    in identity's `limits` at `policyUserManagement`.
  - Tests reach a real `httptest` TLS server through `Access.webhookNet` (W20 rev 4), never
    `Deps.HTTPTransport`; a test seam shortens the 15 s budget for the timeout case. No
    migration was needed.
- **6** (2026-11-19, delivery B's code and security reviews):
  - The migration is `00204` (A's rename; invoices took `00203`).
  - **Throughput.** The poll is the idle cadence only: a pass that leased all it could (the
    process's free slots, or a pass's 2 of some endpoint) is followed by the next at once, and a
    finished send wakes the loop, so one endpoint is not held to 2 messages per 2 s.
  - **S1, starvation.** A delivery's turn is its rank plus its endpoint's sends in flight, so
    every endpoint with nothing in flight is served before any gets a second; the last 4 free
    slots of a process (`webhookReserve`) are kept for such first sends, so hanging endpoints
    hold at most 12 of 16.
  - **Expiry and prune** take their rows `FOR UPDATE SKIP LOCKED` and the expiry's outer
    `WHERE` repeats `pending` and the free lease, so a row being written or leased meanwhile is
    left alone.
  - **An undecryptable secret** is a failed attempt with error `other` (the closed set of W23 is
    unchanged; adding a word would need a migration), so the run backs off and W18 applies,
    instead of a claim every lease.
  - **Notices (W32).** The sweep of notices still owed is skipped when there is no mail driver,
    backs off per endpoint in memory (1 minute doubling to 1 hour), and gives up on an endpoint
    disabled more than 7 days ago (`disabled_at`). No `notice_attempts` column. The
    integration's name loses control and format characters in a notice (S6); the mail drivers
    encode the subject header themselves.
  - **W30's re-enable expiry reads `next_attempt_at`, not `created_at`**: a delivery is dropped
    when it has been due for more than 24 hours, overdue because its integration was disabled; one
    queued long ago whose retry fell due recently keeps its run.
  - **S3.** The permission and the endpoint's and integration's status are checked at claim, not
    at the post: in the window between, one message may still go out; the reference says so.
  - **S4.** `webhookPermitted` is false for an entry with no permission, and a contracts test
    holds every entry to at least one.
  - **S5.** The fan-out writes at most one `skipped` row a day per endpoint and event name, so an
    integration that lost a role does not grow the log by a row per event. Skips at claim
    settle rows that already exist.
  - **S7, for delivery C.** The attempt `excerpt` is a receiver's text: the log screen renders it
    as React text only, never as HTML or Markdown.
  - `module.Workers`' comment and `contributing/module-boundaries.md` say that worker-mode deps
    carry the enabled modules' catalog.

## 12. What overturning a default changes

1. *Events* (R Q1): more events are catalogue entries; `communications.*` needs a decision on
   exposing user ids.
2. *Filtering* (Q2): a per-key stream needs the key id on the endpoint and `Scope` at fan-out.
3. *Own permission* (Q3): a new key in the catalogue, the Owner tier unchanged for secrets.
4. *Rotation window* (Q4): W9's default and bounds.
5. *Retries / disable* (Q5, Q6): W17/W18 constants only.
6. *Ordering* (Q7): per-endpoint serial delivery would mean 1 in flight and head-of-line
   blocking by a failing delivery; W15's cap and W21 change.
7. *Retention* (Q8): W23's period and excerpt.
8. *Ports/local* (Q9): W11/W20 gain an allow-list; `netguard` has none today.

## 13. Amendments (revision 2, from the review)

- **W26 (amends W4, W5): the catalogue states the GET's real rule.**
  - An entry is `{Event contracts.Event (a zero value), Resource, IDField, Path, Permissions []string}`.
    `Permissions` equals the `x-vantigo-access` permission keys of the `GET` at `Path`
    exactly (rules are often compound), and the first-row examples in W4 are illustrative
    only: the real orders path is `/api/v1/orders/orders/{id}` and its rule
    `permission:orders:access`. Contracts has no event registry, so the zero value gives the
    name and version; the test asserts `Version == EventVersion()`.
  - The test lives in `internal/integration` (it needs the composed catalogue, which
    `contracts` cannot import). It asserts for each entry: the `GET` exists; its
    permissions equal `Permissions`; every one is `Delegable` and not `APINever`; and the
    `GET`'s `x-vantigo-api` is **not `never`** (K53 marks operations `never` that stay
    delegable, and a receiver whose key can never fetch the resource has been sent
    something useless). An event failing the last test is not exposed.
  - `invoices.payment.received` and `.removed` have no payment `GET`: their resource is the
    **invoice** (`IDField: invoiceId`, `Resource: invoice`). `accounting.supplier_invoice.deleted`
    is exposed only if its resource's `GET` can answer for a deleted invoice; otherwise it
    is left out. `invoices.customer_credit.changed` has `GET
    /invoices/customers/{customerId}/credit` (`invoices:access`); it is exposed, its id being
    a customer id, which can be a private person: **decided: exposed**, the owner may remove it.
    Credit notes (`invoices.document.issued`, `kind: credit_note`) are exposed only if
    `GET /invoices/{id}` serves them (the implementer verifies and records the result).
- **W27 (amends W12, W21): `sequence` orders one resource only.** Outbox ids are assigned
  at insert, not commit; only one aggregate's events commit in id order. A receiver compares
  `sequence` **only between messages with the same `data.resource.type` and `id`**, never
  across resources, and `timestamp` is the publisher's `occurred_at` and not monotonic. The
  reference says "ids rise but are neither contiguous nor ordered across resources".
- **W28 (amends W11, W24): paths are secrets too.** Zapier, Slack and Make carry their token
  in the *path*. Audit snapshots, notices and log lines carry **scheme and host only** plus a
  short hash of the full URL (so a change is visible); never path or query. Only the Owner's
  screen shows the full URL.
- **W29 (amends W15, W16): the claim and fairness.**
  - The claim is: (a) lock candidate endpoints `FOR UPDATE SKIP LOCKED`
    (enabled, integration active); (b) count unexpired leases per locked endpoint; (c) rank
    that endpoint's due deliveries (`row_number() OVER (PARTITION BY endpoint_id ORDER BY
    created_at, id)`) in an outer query holding no lock; (d) lease those with rank <= 4 - in
    flight by `UPDATE … WHERE id = ANY(…) AND (lease_until IS NULL OR lease_until <= now)`.
    Locking the endpoint is what makes the cap hold across worker processes.
  - Every result write is `WHERE lease_id = $claim`, as `failSQL` does
    (`events/dispatch.go:375-378`); a lost lease is not an attempt.
  - **Fairness:** a pass claims at most 2 deliveries per endpoint, round-robin; global
    concurrency is 16 (above twice the per-endpoint cap); a pass does not wait for its slowest
    send before the next claim, so a slow endpoint holds at most its own 4 slots. The sender
    is `identity.webhook-sender`, gated by its own `IDENTITY_WEBHOOKS_ENABLED` inside
    `workers()` (today `workers()` returns nil unless the keys worker is enabled), not the
    outbox handler's name.
- **W30 (amends W23, W2, §7): everything expires.** Pruning deletes any delivery whose
  `created_at` is older than 30 days whatever its state. A delivery still `pending` after
  **3 days** becomes `dead` (`expired`); deliveries of an integration that was disabled are
  dead-lettered on enabling when older than 24 hours. `skipped` rows are inserted with
  `completed_at` set. The text in §7 about queued rows "expiring" is this.
- **W31 (amends W14, W2, W18): fan-out cannot race disable or archive.** `Handle` selects the
  endpoints `FOR SHARE`; disable, archive and delete lock them `FOR UPDATE` before
  dead-lettering, so a delivery is never inserted for an endpoint after its pending rows were
  settled.
- **W32 (amends W18): the notice.** Follow `sendAPIKeyNotice` (`identity/apikey_notices.go:64-94`):
  claim with `UPDATE … SET disabled_notice_sent_at = now WHERE … IS NULL`; mail with no
  transaction open; release the claim when nobody was mailed; **clear the column on
  re-enable**. Notices go for `failing` and `gone` only. Dead deliveries counted toward W18's
  five are those that died by running out of attempts, not `endpoint_disabled`,
  `integration_archived` or `expired`. `failing_since` is set `COALESCE(failing_since,
  attempt_at)` only when `attempt_at` is later than `last_success_at`, so racing results
  cannot clear and reset it. Archiving writes `webhook.disabled` per endpoint.
- **W33 (amends W7): the permission is also checked at claim.** The sender checks W7's
  permission once per integration per pass; a delivery whose permission is no longer held is
  marked `skipped` (`permission_not_held`) instead of being sent. "Stops at once" is then true.
- **W34 (amends W3, W25).** The rule is `policy:Owner+OwnerManagement` with
  `x-vantigo-api: never` and `x-vantigo-assistant: never`, as the integrations'
  (`openapi/identity.yaml:5651-5653`). The golden list is
  `srv/openapi/testdata/api-marks.txt`, regenerated with `-update`.
- **W35 (amends W22): the resend limit** is a database counter on the delivery
  (`resend_count`, `resend_window_start`), since identity's `limits` map is per client address
  and operation. Three per minute per delivery; otherwise 429.
- **W36 (amends W1, W2): fences.** Creating an endpoint takes `pg_advisory_xact_lock` on a
  constant key, so the 50-per-workspace limit holds under concurrency. Create, PATCH, enable,
  test and resend on an archived integration answer 409 `integration_archived`.
- **W37 (amends W19, W12, W16): the ping.** Its `event_id` is NULL (the unique index tolerates
  it), its body `id` is `evt_ping_<deliveryId>`, and a 410 to a ping does disable the endpoint
  (it says "this URL is gone"). A ping that fails once is `dead` with no retry.
- **W38 (amends W16): the result write order.** Update the delivery first under its lease,
  then insert the attempt only if a row was affected, so a deletion mid-attempt loses the
  attempt and nothing else.
- **W39 (amends §7, W14): wiring.**
  - Identity has no `EventHandlers`; add it to `identity.Module` (`identity/module.go:148`),
    collected by `module/workers.go:22-41`. A dead fan-out delivery holds back later webhook
    events of that aggregate (the claim orders per aggregate, `events/dispatch.go:254-262`);
    it is seen in the operator list, not the log.
  - `reference/identity.md` does not exist. Tables and errors go in `reference/webhooks.md`,
    listed in `reference/index.md`. The new Go and TSX files and `contracts/webhook_events.go`
    join the `sources` of `reference/webhooks.md` and of `admin/api-access.md` (en and nb).
  - Frontend: `IntegrationTab` and the route's `validateSearch` accept a `webhooks` tab
    (`-api-access-integration.tsx:69`, `api-access_.$integrationId.tsx:24-25`).
