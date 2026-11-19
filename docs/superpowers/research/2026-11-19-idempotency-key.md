# A general `Idempotency-Key` for creating operations — research

Research, 2026-11-19, following the webhooks and MCP phases (`specs/2026-11-18-outgoing-webhooks-design.md`
W21 and §10, `specs/2026-11-19-mcp-server-design.md` M13, and API access §12 "Idempotency-Key … waits for
the webhooks phase"). Code references are against `92b6c0e4d`; `srv/` is `apps/server/internal/`.
"(unverified)" means the source did not confirm it.

## 1. Why now

Three new callers retry: **API-key integrations** (timeouts, 5xx), **MCP agents** (the `write` tool's
"may or may not have been made" answer, MCP B security finding S1) and **webhook receivers**
(they dedupe on `webhook-id`, the receiving half of the same story). A retried `POST` that creates an
invoice, sends mail or books a shipment does it twice. Only **4 operations of about 350 keyable
`POST`s** accept the header today.

## 2. What exists in our code

- **Per-module mechanisms.** `Idempotency-Key` is declared by `postCommunicationsConversations`,
  `…Reply` and `…Attachments` (communications.yaml:2184, 2508, 2730) and `postInvoicesByIdApplyCredit`
  (invoices.yaml:10299). Communications keeps `communications.idempotency_records` with a **global**
  unique key and a body fingerprint, inserts it in the business transaction (atomic with the effect),
  replays a slim `200` rebuilt from ids, and answers `409 idempotency_key_reused` for another body;
  attachments and apply-credit are per user. There is **no TTL**. Other modules use body ids
  instead: `requestId` (quick invoice), `clientRequestId` (inventory, manufacturing, orders,
  purchasing proposals).
- **No generic mechanism and no `x-vantigo-idempotent` mark.** The router (`srv/module/router.go`)
  has the natural hook: inside `wrap`'s `serve` closure, after `MaxBytesReader` and before `h(...)`,
  `op.OperationID`, the principal and the request are all known (`router.go:393-403`). Every module
  builds its own `Router` (23 call sites), so a per-module option would touch them all; the existing
  precedent for reaching identity from the router without touching them is a **type assertion on
  `Access`** (`contracts.APIRequestRecorder`, K35).
- **Response capture.** `httpx.StatusRecorder` records only status and size; a buffering recorder
  exists in MCP (`callRecorder`, `apimcp_tools.go`) and the dispatcher. Generated handlers write status,
  headers and a JSON body; some operations stream or answer binary.
- **Who sends it today.** The Assistant's bulk run and the agents module send keys only when the
  operation declares the header; MCP's allow-list lets `Idempotency-Key` through only when declared
  (`apimcp_tools.go:58`). A mechanism that adds the parameter to the *served* contract makes all
  three pick it up.
- **Storage precedents.** `identity.api_requests` is partitioned by day (no unique index possible
  across partitions); the identity worker `identity.api-keys` already prunes on a schedule under an
  advisory lock; row leases (`lease_id`/`lease_until`) and `INSERT … ON CONFLICT` are common. The
  next free migration is **00212** (00211 is on `main`).
- **Counts.** 358 keyable `POST`s (351 creates/actions, 7 marked `x-vantigo-api: read`); `PUT`, `PATCH` and
  `DELETE` (about 210) are state-replacing or already-gone and carry `If-Match` where it matters.

## 3. What others do

- **IETF `draft-ietf-httpapi-idempotency-key-header`** (-08, Oct 2026, active, Standards Track):
  the header is an RFC 9651 Item Structured Field String (`Idempotency-Key: "8e03978e-…"`); `400` when
  required and missing; `409` when a retry arrives while the original is in flight; `422` when the key is
  reused with a different payload; a completed request's result, "success or an error", is returned
  on a repeat; no retention number is given but the policy must be published; the fingerprint method
  is the server's choice; keys must be unguessable and the lookup key composite with server-known
  attributes (§5). (Read at -07; -08 may differ.)
- **Stripe** (<https://docs.stripe.com/api/idempotent_requests>): keys up to 255 characters, pruned after
  24 hours or more; saves status and body of the first request **including 500s**; compares the
  parameters and errors on a mismatch; a request that fails validation, authentication, rate limit or
  conflicts with an in-flight one is **not saved**; a replay carries `Idempotent-Replayed: true`. v2 keeps
  keys 30 days and re-executes a failed first request. Adyen: `idempotency-key`, 64 characters, kept
  7–14 days, scoped per company account. PayPal: `PayPal-Request-Id`, replay returns the latest status
  of the earlier request. AWS EC2: client token, `IdempotentParameterMismatch` on a changed request.
  Shopify: `@idempotent(key:)` on GraphQL mutations. Tripletex, Fiken, GitHub: none found (unverified).
- **MCP:** no idempotency key in the protocol; `idempotentHint` is an annotation only. Guidance in the
  field: the caller or runtime mints the key once and **reuses it on every retry**; the model must not
  generate a fresh one for a retry. No evidence that MCP clients mint keys (unverified).

## 4. Pitfalls that shape the design

- **Atomicity.** A generic middleware cannot join the handler's transaction: the handler commits its
  effect, *then* the response is stored. A crash or error between the two leaves "effect happened,
  outcome unknown". Re-running the handler for such a key is unsafe for sends and money. The honest
  behaviour is **"outcome unknown — read the record"**, never silent re-execution. Modules that need
  strict atomicity keep or add a native mechanism inside their own transaction (communications today).
- **5xx.** Stripe stores 500s; the draft leaves it open. A 5xx may or may not have committed.
- **Scope.** The existing keys are global (communications) or per user. A key must be scoped to the
  principal (the integration for a key, the person for a session) so one principal cannot read
  another's stored response.
- **Fingerprint.** Method, route and path parameters, the query and the body. Hash the raw bytes (a
  canonical JSON would hide real differences and cost parsing).
- **Size and kind.** Cap stored bodies; skip streaming, multipart and binary operations.
- **Replays and limits.** A replay still costs a request: count it against the integration's
  limit (otherwise replays bypass it) and log it distinctly.

## 5. Recommended shape (R)

One **router-level middleware** for `POST` operations, opt-out rather than opt-in (an opt-in mark would
mean editing ~350 operations and would leave the rest unprotected): the header is accepted on every
in-scope `POST`; operations that declare the header themselves keep their native mechanism; the
parameter is added to the *served* integrator document so the reference, MCP and the Assistant see it.
The store is a table in `identity`, reached by type assertion on `Access` (K35's pattern); pruning
joins the identity worker. States: `in_progress` (leased), `done` (stored response), `unknown`
(outcome not known). A key is scoped to `(principal, operation, key)`.

## 6. Questions for the owner, with recommended defaults

1. **Opt-in or opt-out per operation?** *Default:* opt-out. Every keyable creating `POST` accepts the
   header; excluded: reads marked `x-vantigo-api: read`, non-JSON (multipart, binary, streaming)
   operations, anonymous operations, and operations that already declare the header.
2. **Should the header ever be required?** *Default:* no. Optional everywhere, so nothing breaks; the
   three communications operations keep theirs required.
3. **Scope and conflict codes.** *Default:* `(principal, operation, key)`; `422 idempotency_key_reused`
   for a different request, `409 idempotency_in_progress` for a concurrent duplicate, `409
   idempotency_outcome_unknown` after a crash or a 5xx. (The native operations keep their `409`.)
4. **What is stored.** *Default:* 2xx and deterministic 4xx (400, 404, 409, 410, 422) are replayed
   exactly (status, `Content-Type`, `Location`, `ETag`, body) with `Idempotent-Replayed: true`; a 5xx,
   panic or timeout makes the key `unknown` and is **not re-run**; a body over 256 KB is `unknown`.
5. **Retention.** *Default:* 24 hours, pruned by the identity worker; published in the docs.
6. **Do replays count against the rate limit and the log?** *Default:* yes, both; the log row's
   `reason` is `idempotent_replay`.
7. **Do the native mechanisms change?** *Default:* no. Converging them (global communications key,
   slim replays) is a later, separate change.
8. **MCP.** *Default:* the `write` tool allows the header on every operation the middleware covers;
   the tool descriptions tell the caller to mint one key per intended action and reuse it on retries.
9. **Kill switch.** *Default:* `IDEMPOTENCY_ENABLED` (default true), as the other features.
