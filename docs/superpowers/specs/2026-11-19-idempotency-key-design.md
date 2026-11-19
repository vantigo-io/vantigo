# A general `Idempotency-Key` — design

Revision 2, 2026-11-19 (revision 1 followed by an adversarial review against the code; its 19 findings
are folded in as **§11, which wins where it differs from the text above**). Builds on API access (`specs/2026-11-17-api-access-design.md`, K1–K61), webhooks
(`specs/2026-11-18-outgoing-webhooks-design.md`) and MCP (`specs/2026-11-19-mcp-server-design.md`, M1–M36),
and on the research `research/2026-11-19-idempotency-key.md` ("R"). The owner's questions in R §6 take R's
recommended defaults here; §9 says what overturning each would change. Decisions are numbered I*n*. Code
references are against `92b6c0e4d`. `srv/` is `apps/server/internal/`; `host/` is `apps/host/frontend/src/`.

## 1. Purpose

A caller that retries a creating `POST` — an integration after a timeout, an MCP agent after "the change
may or may not have been made", a script — adds `Idempotency-Key: <unique value>` and gets the same
result instead of a second invoice, mail or shipment. It works on every in-scope operation without the
module doing anything.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **A** | The store, the middleware in the router, pruning, `IDEMPOTENCY_ENABLED`, the request-log reason, the behaviour docs | — |
| **B** | The served contract (integrator document, per-operation description) , MCP and the Assistant pick it up, docs for integrators, the landing note | A |

**Not in this phase:** requiring the key anywhere; converging the native mechanisms (communications' global
key and slim replays, apply-credit, `requestId`/`clientRequestId`); `PUT`/`PATCH`/`DELETE`; keys minted by
the server; a per-key idempotency dashboard; same-transaction storage for strict operations (R §4,
"later").

## 2. Decisions — scope and header

- **I1 — In scope: a creating `POST`.** The middleware acts on a request when **all** hold:
  the method is `POST`; the operation's access rule is a permission or anonymous-with-principal rule
  (the principal has a `UserID`); the operation is **not** `x-vantigo-api: read` (searches, previews);
  the operation has **no `Idempotency-Key` parameter of its own** (native mechanism wins); the operation's
  request body is JSON or empty and its success response is JSON or empty (`jsonRequest` in
  `srv/operations/mode.go` is the model; multipart, binary and streaming operations are out);
  the operation is not on a small **deny list** (`IdempotencyNever`, in code with a test) for operations whose
  repeat must run (none at the start; the list is the escape hatch).
  Otherwise the header is ignored (no error), and the documentation says so.
- **I2 — The header.** `Idempotency-Key`, at most one. Its value is trimmed, a surrounding pair of double
  quotes (the draft's String form) is removed, and it must then be 1–255 printable ASCII characters
  without control characters. Anything else is `400 idempotency_key_invalid` (before any work). Absent
  means the request runs as today. Never required (the three communications operations keep theirs).
- **I3 — The scope of a key** is `(principal, operation, key)`: the integration's user id for a key
  principal, the person's id for a session or a grant, the operation id, and the key. The same key on
  another operation, or from another principal, is a different key. A key is never readable by another
  principal.
- **I4 — The fingerprint** is SHA-256 over the method, the matched route's path parameters in order,
  the query with its pairs sorted, and the raw body bytes. The body is read through the router's
  `MaxBytesReader` (a refusal is the operation's usual 400/413) and restored for the handler.

## 3. Decisions — behaviour

- **I5 — The flow**, inside `wrap`'s `serve` closure between the body cap and the handler, after
  authentication, the integration's rate limit and the request log's start (so a replay is counted and
  logged, I10):
  1. **Claim.** `INSERT … ON CONFLICT DO NOTHING` a row `(principal, operation, key, fingerprint,
     state='in_progress', lease_until = now + 2 min, expires_at = now + 24 h)` in its own short
     transaction. If the insert wins, run the handler (step 3).
  2. **The key exists.** Read it (`FOR UPDATE` is not needed):
     - fingerprint differs → `422 idempotency_key_reused`;
     - `done` → **replay** (I6);
     - `in_progress` with a live lease → `409 idempotency_in_progress` with `Retry-After: 1`;
     - `in_progress` with an expired lease, or `unknown` → `409 idempotency_outcome_unknown`
       ("the first request may or may not have been applied: read the record, then use a new key").
       The handler is **never re-run** for a key that reached the handler.
  3. **Run** the handler on a response recorder that buffers status, the headers of I6 and the body (up to
     256 KB), passing writes through only after the outcome is classified (the response is written to the
     client from the recorder).
  4. **Record.** In a second short transaction, by outcome:
     - 2xx, or a 4xx of 400, 404, 409, 410, 422 → `done` with the stored response;
     - any other status (5xx, 408, 429), a panic, a response over 256 KB, a non-JSON content type → `unknown`
       (the client still receives the real response);
     - a refusal before the handler (authentication, authorization, rate limit, body cap) never reaches the
       claim (the claim is after them) and stores nothing.
  A failure to write the second transaction is logged and leaves `in_progress`, which expires into
  `outcome_unknown` (safe).
- **I6 — The replay** answers the stored status with the stored headers — `Content-Type`, `Location`,
  `ETag`, `Content-Language` — and the stored body, plus `Idempotent-Replayed: true`. Other headers
  (cookies, rate-limit headers, `Retry-After`) are not stored; the replay carries the *current*
  `RateLimit-*` headers.
- **I7 — Concurrency** is the unique key: exactly one of two simultaneous requests wins the claim; the
  other gets I5.2. There is no waiting.
- **I8 — Retention.** `expires_at` is 24 hours from the claim; an expired row is treated as absent (a
  new claim replaces it with `ON CONFLICT … DO UPDATE WHERE expires_at < now()`), and the identity worker
  `identity.api-keys` deletes expired rows in batches of 1,000 each cycle, under its existing lock.
- **I9 — The kill switch.** `IDEMPOTENCY_ENABLED` (default true) in `srv/config`. False: the middleware
  is not installed and the header is ignored everywhere (the native mechanisms are unaffected).
- **I10 — Limit, log, audit.** A replay is a request: it is counted against the integration's rate limit
  and logged. Its request-log `reason` is `idempotent_replay` (17 characters, within `varchar(20)`),
  set through a new `contracts.APIReason` constant. A replay never reaches the handler, so there is no
  second domain audit entry or event: the audit shows the first execution.

## 4. Decisions — the store and the wiring

- **I11 — The table** `identity.idempotency_keys`: `principal_id uuid`, `operation_id varchar(200)`,
  `key varchar(255)`, `fingerprint bytea` (32), `state` `in_progress|done|unknown`, `lease_until`,
  `status smallint`, `headers jsonb`, `body bytea` (≤ 256 KB, check), `created_at`, `expires_at`;
  primary key `(principal_id, operation_id, key)`; index on `expires_at`. No FK (the principal may be
  deleted; rows expire). Migration `00212_identity_idempotency.sql` (the next free number after `main`
  **and every open PR** at enqueueing).
- **I12 — Reaching it.** The router finds the store by type assertion on `Access`
  (`contracts.IdempotencyStore`, implemented by identity's `Access`), as K35 does for the recorder, so
  the 23 `RouterOptions` call sites do not change. The interface: `Claim`, `Lookup`, `Complete`,
  `MarkUnknown`, each on its own short transaction. A composition without `Access` or without the
  assertion has no middleware.
- **I13 — Code placement.** The middleware is `srv/module/idempotency.go` (package `module`); the
  recorder is its own small type (the MCP and dispatcher recorders are for other purposes); the
  decision of I1 is a pure function `idempotentOperation(op, rule)` with a test that lists the
  operations it excludes.

## 5. Decisions — the surface (delivery B)

- **I14 — The served contract.** `integrator.Build` adds, to every in-scope operation (I1), an optional
  `Idempotency-Key` header parameter and the `409`/`422` responses, with one shared description
  linking the reference page; the source contracts (`openapi/*.yaml`) are **not** edited, so the
  generated code, the golden marks and `x-vantigo-*` lint are unaffected. A test asserts the integrator
  document and the router's decision agree for every operation.
- **I15 — MCP.** `mcpSettableHeaders` already allows `Idempotency-Key`; the check "only when the
  operation declares it" is satisfied by consulting the same predicate as I14 (the index records
  it). The `write` and `delete` descriptions and the instructions tell the caller to mint **one key per
  intended action and reuse it for every retry of that action**, never a new key for a retry. The
  result of a replay is the stored one; `Idempotent-Replayed` is in the tool result's text.
- **I16 — The Assistant** is unchanged: it sends keys only for operations that declare them natively.
- **I17 — Documentation** (AGENTS.md; `sources`): `reference/api-introduction.md` (a "Retrying safely"
  section between *The rate limit* and *What changes and how you hear of it*, the new refusal codes in the
  Refusals table), `reference/mcp.md` (the write tools), `reference/webhooks.md` (one sentence: receivers
  dedupe on `webhook-id`; senders may use `Idempotency-Key`), `admin/api-access.md` en+nb (a short
  section; the config variable), `admin/configuration.md` en+nb, `reference/api-compatibility.md` (an
  optional header on existing operations is additive); `go generate` in `apps/server`.
- **I18 — Landing.** One clause in the integrations note on the home page ("and can retry safely"), after B.

## 6. Edge cases

- **Same key, different body** → 422 (even after the first finished). **Same key after 24 hours** → a new
  claim.
- **The first request is still running when its client gives up** → the retry gets 409 `in_progress` until
  the lease (2 minutes) ends, then `outcome_unknown`.
- **A handler that takes longer than the lease** is still running when a retry arrives after 2 minutes: the
  retry gets `outcome_unknown` (it must not run beside the first).
- **The server restarts mid-request** → the row stays `in_progress`, becomes `outcome_unknown`.
- **A 4xx that is not deterministic** (401/403 come before the claim; 429 is not stored) is never stored.
- **A session request** from the host UI with a key (none sends one today) behaves the same.
- **Operations with a native key** keep their behaviour exactly, including the global communications scope.
- **A very large number of distinct keys** is bounded by retention and the integration's rate limit.

## 7. Testing

- Unit: `idempotentOperation` over the whole composed contract (the exclusions listed and counted);
  header parsing (quotes, length, control characters); fingerprint (query order, body bytes, path params).
- Middleware with a real Postgres: first request stored and replayed byte-for-byte (status, headers,
  body, `Idempotent-Replayed`); different body 422; concurrent duplicates (one wins, one 409); stale
  lease and `unknown` → `outcome_unknown` without re-running the handler (counter in the fake handler);
  5xx/panic/oversize → `unknown`; 400/404/409/410/422 replayed; 401/403/429 not stored; two principals
  and two operations with one key are independent; expiry reclaims; pruning deletes; the kill switch.
- Rate limit and log: a replay counts once and logs `idempotent_replay`.
- B: the integrator document adds the parameter exactly where the router acts; MCP `write` with a key
  replays; the whole-contract walks (`TheWholeContract` tests) still pass.
- The manual check: retry a `POST` with `curl` and with an MCP `write`.

## 8. Later

Requiring the key on selected operations; same-transaction storage for money and sends; converging the
native mechanisms; `PATCH`/`DELETE`; server-minted keys for MCP; a per-integration view of replays.

## 9. What overturning a default changes

1. *Opt-out* (R Q1): an opt-in mark means editing ~350 operations and a lint rule; the engine is unchanged.
2. *Never required* (Q2): a `required` mark would need the contract mark after all.
3. *Codes/scope* (Q3): constants and the primary key.
4. *Storing 5xx* (Q4): storing 5xx as `done` makes a transient failure permanent for that key and removes
   `outcome_unknown` for it; re-running is rejected as unsafe.
5. *Retention* (Q5): one constant and the prune.

## 10. Revision log

- **1** (2026-11-19): first draft from R.
- **2** (2026-11-19): the review's findings, §11.

## 11. Amendments (revision 2, from the review)

- **I19 (replaces I1): the scope is `Build`'s keep-set.** One shared predicate decides, for the router, the
  integrator document (I14) and MCP. An operation is in scope when **all** hold: method `POST`; the access
  rule is `permission:` (not anonymous, not `policy:`, not `session`, not `scim`); it is **not**
  `x-vantigo-api: never` and names no `APINever` permission (exactly `integrator.Build`'s keep-set, minus
  reads); not `x-vantigo-api: read`; no `Idempotency-Key` parameter of its own; JSON-or-empty request and
  success response (`jsonRequest`/`jsonResponse` in `srv/operations/mode.go`, to be exported); not in
  `IdempotencyNever`; and its documented 2xx does not set `Cache-Control: no-store`. This excludes the
  one-time-secret operations that sessions could otherwise reach (the staff account pass, national-id and
  bank-account reveals, station pairing), anonymous operations that carry a cookie's session principal
  (login, passkeys, password recovery, invitations, pay attempts, RSVP) and every `policy:` rule (API-key and
  webhook-secret mint/roll, the SCIM token). The predicate's test lists the excluded operations and their count
  (the composed contract has 498 `POST`s; about 330 are in scope).
- **I20 (amends I11, I6): no secret at rest.** At runtime a response carrying `Cache-Control: no-store`, or
  `Set-Cookie`, is recorded `unknown` with **no body**. Stored bodies are sealed with `internal/secrets`
  (`secrets.Box`, purpose `identity/idempotency`, bound to the row's principal, operation and key as AAD).
  Erasing a person or anonymising a customer deletes the idempotency rows of the affected principal.
  The backup/retention note in the docs says rows live 24 hours.
- **I21 (replaces I8, amends §6): pruning, quota, and who prunes.** Pruning loops until a batch comes back short,
  is its own worker step tied to `IDEMPOTENCY_ENABLED`, **not** to `IDENTITY_API_KEYS_ENABLED`, and runs every
  cycle of the identity worker (and the worker also prunes when the API-keys expiry is off, as a separate
  lease). A **per-principal cap** of 2,000 live keys and 16 MB of stored bytes answers
  `429 idempotency_quota` (Retry-After 60) at the claim; sessions, which have no integration rate limit, are
  bounded by it. §6's line "bounded by the integration's rate limit" is withdrawn.
- **I22 (replaces I10's mechanism): the log reason.** `keyGate.run` gives `next` a `*string` reason holder on the
  request context; the middleware sets `idempotent_replay` on a replay. `reason` is redefined as "a refusal or
  a replay" in `00199`'s description, `identity.yaml`'s enum and filter (8152-8162), the host's
  `refusalReasons` (`-api-access-requests.tsx:15`), the `apiAccess.reason_*` labels (en and nb) and the
  Requests text in `admin/api-access.md` and the user guide. All in **delivery A**.
- **I23 (amends I5.4): what is replayed.** Only **2xx and 400, 404, 410, 422** are `done`. A `409` is
  state-dependent (`payment_request_active`, `invoice_changed`, …): it **releases the claim** (the row is
  deleted), so an identical retry runs again, as Stripe does. 5xx, 408, 429, a panic, a response over 256 KB,
  a `no-store`/`Set-Cookie` response, or a non-JSON, non-empty body → `unknown` (`isJSON`
  of `operations/mode.go:104` or an empty body counts as JSON, so `application/problem+json` is fine).
- **I24 (replaces I4): the fingerprint** is SHA-256 over the method, the route's path parameters in order, the
  query sorted by name (values kept in order within a name), `Content-Type`, `If-Match`, `If-None-Match`, and
  the body: for a JSON body, **canonical JSON** (decoded with `UseNumber`, re-marshalled with sorted keys), so a
  model regenerating the same arguments with another key order or spacing is a genuine retry; raw bytes for an
  empty or non-JSON body. On a read error (`MaxBytesError`), the middleware restores a reader that replays the
  bytes read and returns the same error, skips the claim, and lets the handler answer with the operation's
  usual 400 (the router has no 413).
- **I25 (replaces I14's mechanism): the served document merges.** `integrator.Build` gains
  `Options{Idempotency bool}`, carried in the callers' cache keys (`apidocument.go`, `apimcp_index.go`);
  false when `IDEMPOTENCY_ENABLED` is false, so the document and MCP never advertise a header nothing honours.
  It adds the parameter and **merges** the `409`/`422` responses: appended to an existing response's
  description (and `oneOf` schema), created only where none exists; it never replaces a documented one.
  A test asserts the document's set of operations with the parameter equals the router's predicate (I19).
- **I26 (amends I5.4, I12): completion survives cancellation.** `Claim`, `Complete` and `MarkUnknown` run on
  `context.WithoutCancel` with their own 5-second timeout (as `keyGate.record` does), so a disconnect or the
  MCP loopback's 30-second deadline does not leave the row `in_progress`. `Complete` is conditioned on a
  `lease_id` held by the claimer. A handler that outlives the 2-minute lease and finishes later still writes
  `done` if its `lease_id` still matches; a key answered `outcome_unknown` may thus later replay, which is
  defensible and documented. The slow operations (`postInvoicesBillingRuns`, `postCustomersImportsByImportIdRun`,
  `postStaffRetentionRun`, `postInvoicesBankFeedSync`, `createAccountingSaftExport`, and any over 60 seconds
  in the test run) are listed in `IdempotencyNever` with a reason, unless the test proves otherwise.
- **I27 (amends I5.1/I8): one claim statement.** `INSERT … ON CONFLICT (principal_id, operation_id, key) DO
  UPDATE SET fingerprint=…, state='in_progress', lease_id=…, lease_until=…, expires_at=…, status=NULL,
  headers=NULL, body=NULL WHERE idempotency_keys.expires_at < now() RETURNING lease_id`; no row returned means
  the key is live and is read (I5.2).
- **I28 (amends I3): the scope includes the way in.** For a key principal the scope is the integration (across
  its keys). For a person it also includes `Via.Kind` and, for a grant, the grant or agent id, so a session's key
  is not replayed to the Assistant or an agent run (whose handler-side authorization differs).
- **I29 (amends I11): the migration number** is the next free after `origin/main` and every open PR at
  enqueueing (00213 on 2026-11-19: #405 holds 00212).
- **I30 (amends §1, the split):** delivery **A** is the engine, pruning, quota, the kill switch, the reason
  surface (I22), the shared predicate (I19) and the behaviour docs; delivery **B** is `Build`'s option and merge
  (I25), MCP, the integrator docs, the landing clause. A's default stays on: the header is simply not yet
  advertised.
- **I31 (amends I17): docs.** The heading is "What changes, and how you hear of it". `contributing/module-boundaries.md`
  (sources `module/`) and `admin/authentication.md` (sources `identity/`) are updated where the coverage check asks.
- **I32:** a recorder replaces `w` **before** `contracts.WithRequest`, because handlers write headers through
  `ResponseWriterFrom` (`identity/server.go:108`, `google/personal.go:271`, `entra/outlook.go:308`); it needs
  `Unwrap`; `Flush`/`Hijack` are not needed for the JSON-only scope (a test asserts no in-scope operation flushes).
