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
- **2.1** (2026-11-19): delivery A as built, and where it departs from the text:
  - *The store's interface* (I12) names its methods `ClaimIdempotencyKey`, `LookupIdempotencyKey`,
    `CompleteIdempotencyKey`, `MarkIdempotencyKeyUnknown` and `ReleaseIdempotencyKey` (the 409 release of
    I23), so they read on `Access` beside its other methods, and adds `IdempotencyEnabled()`: the kill
    switch (I9) reaches the router through the store it already finds, with no `RouterOptions` change.
  - *The scope* (I28) is a fourth key column, `via` (`session`, `api_key`, `station:<id>`,
    `assistant:<grant>`, `agent:<grant>`), in the primary key `(principal_id, via, operation_id, key)`;
    the quota (I21) counts across a principal's ways in.
  - *Anonymising a customer* (I20) deletes the keys of agent runs scoped to that customer, kept in a
    `customer_id` column from `Via.CustomerID`; identity registers a `CustomerPersonalData` for it
    (kind `identity.idempotencyKeys`). Another principal's stored answer that mentions the customer is
    sealed and expires within 24 hours; it is not searched.
  - *Sealing* (I20): `secrets.Box` gained `SealBound`/`OpenBound`, which add a caller's value (the row's
    scope) to the AEAD's additional data under one purpose, `identity/idempotency`; a purpose per row
    would derive and cache a key per row.
  - *The shared predicate* (I19) is `module.Idempotent` (exported, for delivery B's `Build` option and
    MCP) over the router's pure `idempotentOperation(method, op, rule, never)`, with
    `module.IdempotencyNever`; it lives in `module`, not `integrator`, because depguard keeps
    `internal/operations` (`JSONRequest`/`JSONResponse`) out of `integrator`. B passes the predicate to
    `Build` rather than `Build` calling it. Over the composed contract: 498 `POST`s, 321 in scope, 177
    excluded, each listed with its reason in `integration/idempotency_scope_test.go`.
  - *The slow operations* (I26) are the five named, listed with reasons; they were not measured, and no
    other operation was found over 60 seconds in the test run (none is timed there). (2.2: one remains.)
  - *A refusal that is not kept* (amends I20, I23): a `400`/`404`/`410`/`422` that carries `no-store` or
    `Set-Cookie`, or a non-JSON body, **releases** the claim instead of leaving it `unknown`: a refusal
    applied nothing, and every `400` written by `httpx.WriteProblem` (the decode errors) carries
    `Cache-Control: no-store`. A success with them stays `unknown`, as written. A handler's own `401`/`403`
    is `unknown`, as I23 says of every status it does not list.
  - *The reason holder* (I22): `keyGate`'s `next` takes the request, so the holder travels on its context.
  - *The prune* (I21) is a step of `identity.api-keys` under its own advisory lease; the worker now runs
    when either `IDENTITY_API_KEYS_ENABLED` or `IDEMPOTENCY_ENABLED` is on, and does only the parts whose
    switch is on.
  - *`00199`'s description* (I22): its comment line, and a `COMMENT ON COLUMN` in the new migration.
- **2.2** (2026-11-19): delivery A after its code review and security review; where it differs from 2.1,
  2.2 wins.
  - *The migration* is `00217_identity_idempotency.sql` (#408 holds 00213).
  - *No answer written* (code review, blocker): a handler that wrote no status — a caller gone, whose
    error `httpx.WriteError` answers with nothing — is `unknown`, never a stored empty `200`.
  - *The quota is part of the claim* (I21; review): `ClaimIdempotencyKey` runs in one transaction under
    `pg_advisory_xact_lock(hashtextextended('identity/idempotency:' || principal, 0))`; it reads the
    key first, so a retry of an existing key is answered whatever the quota, and only a new key counts
    against it. A claim still running reserves 256 KB of the 16 MB. The store's `LookupIdempotencyKey`
    is gone: the claim answers the live key (`IdempotencyClaim.Existing`), so the three-attempt loop of
    2.1 is gone too.
  - *The fingerprint* (I24; review): the middleware's SHA-256 digest is keyed by the store with
    `Box.MAC` under `identity/idempotency-fingerprint` (a MAC purpose, never a Seal's) before it is
    kept or compared. Every name, value and count is its own length-prefixed part; `Content-Type` is
    its media type and charset, lower-cased, `utf-8` dropped; a JSON body is canonicalised only up to
    64 KiB and only in valid UTF-8, otherwise hashed as its bytes (a reformatted large body is another
    request); nesting is bounded by `encoding/json`'s own depth limit.
  - *One sealed answer* (I20; review): `answer` holds status, headers (at most 8 KB, else `unknown`) and
    body as one plaintext, sealed and bound to the row; `status`, `headers` and `body` columns are gone.
    The lookup classifies from `state`, `lease_until` and `expires_at` without opening.
  - *Erasure* (I20; review): the Owner's user delete deletes the person's keys, as the erasure does.
    Anonymising a customer also raises a watermark (`identity.idempotency_erasure`, one row, never
    lowered): an answer claimed before it reads `unknown` and is never replayed, whoever stored it,
    since any of them may name the customer; its sealed row is pruned when it expires, within 24 hours.
  - *A grant's scope* (I28; review): an Assistant's or agent's principal with no grant on the context
    has no scope and the header is not honoured (fail closed); a grant's customer is part of `via`.
  - *The prune* (I21; review) runs every cycle of `identity.api-keys`, whatever `IDEMPOTENCY_ENABLED`
    says, so rows kept before the switch was turned off still go; the worker runs when either switch is
    on. A failed prune is logged once, by `Run`.
  - *One predicate* (I19; review): `module.Idempotent` and the router call the same
    `idempotentOperation(method, op, rule, known, never)`, the known-permission check included.
  - *`IdempotencyNever`* holds only `postStaffRetentionRun`, the one of the five named in I26 that does
    its work in the request; the billing run, the import run, the bank feed sync and the SAF-T export are
    queued for a worker and answer at once, so they are in scope. Over the composed contract: 498
    `POST`s, 325 in scope, 173 excluded.
  - *Accepted* (review): a replay is the first answer as it was; the handler's own row-level checks (a
    record changed since, a permission withdrawn) do not run again for it within the 24 hours. The docs
    say so.
  - *Noted, not now* (security review): AES-GCM with random 96-bit nonces under one derived key per
    purpose stays far inside its bound at this volume (a key per installation, rows living 24 hours);
    should volume grow, revisit with XAES-256-GCM or a per-row subkey.

- **2.3** (2026-11-19): delivery B as built, after its code review and security review, and where
  it departs from the text:
  - *`Build`'s option* (I25) is `integrator.Options{Idempotency func(method, op, catalog) bool}`, a
    fourth parameter of `Build`, rather than a bool: depguard keeps `integrator` from the router's
    package, so the callers pass `module.Idempotent` in, and a nil predicate adds nothing. It is
    asked of every operation `Build` keeps, before `decorate`. The parameter is one component,
    `components/parameters/IdempotencyKey` (string, 1–255, optional; its description links
    *Retrying safely* and says the header is honoured only where the live document declares it);
    where an operation documents no `409` or `422`, it refers to
    `components/responses/IdempotencyConflict` (with `Retry-After`) or `IdempotencyKeyReused`;
    where it does, the operation gets a copy of its own answer — never the shared component it may
    refer to — with "With an Idempotency-Key, …" appended to the description, `Retry-After` added
    to a `409`'s headers unless it has one, and `AuthErrorResponse` beside its `application/json`
    schema as an **`anyOf`** (review: a `oneOf` over the structurally identical
    `CommunicationErrorResponse` could never match uniquely), left out where that schema already
    takes `{"error": {code, message}}`, or as that schema, or as a new `application/json` beside a
    `problem+json` one. The composed-contract test checks that the document's set equals the
    router's (no hard-coded count; at 2026-11-19 about 325 operations, and 12 of the merged
    schemas already took the error body).
  - *The switch* (I25): `apiDocuments` reads it once, from the `contracts.IdempotencyStore` on
    `Access`, as the router does; it is fixed for the life of the documents, so it is no part of
    the LRU keys. The offline document (`vantigo openapi integrator`, `IntegratorDocument`) is the
    default installation's, so it offers the key. With a store switched off, the router answers a
    request that sent a key to an in-scope operation with `Idempotency-Key-Ignored: true` (security
    review S3), from the same predicate and without a `RouterOptions` change.
  - *MCP* (I15): the catalogue records two flags from the document — `routerIdempotent` (the
    parameter is the shared component) and `ownKey` (an operation's own: communications' three,
    applying a credit). `search_operations` answers `idempotencyKey: true` for the first;
    `describe_operation` says which key (`general: …` or `its own: …`, review S4). The general
    key's text is in the instructions once (one random key per intended action, the same key for
    every retry, never a new key for one, never a key from the workspace's data, what each answer
    means), `write` has one line, `delete` none, and with the switch off none of it is there
    (review 7). A replay's result is `{"idempotentReplayed", "status", "response", "note"}` —
    `response: null` for an empty answer, and `note` saying that if the call was not a retry of the
    same action nothing was done, so resend with a new random key (review S1); a refusal replayed
    carries `idempotentReplayed` and the note too (S5). `next` is given only to a call that sent a
    key (review 2): the general key's for `routerIdempotent`, its own for an own key's reuse; so is
    the "retry with the same key" text of an unanswered write.
  - *The Assistant* (I16) is unchanged; a test asserts the dispatcher's description of an in-scope
    operation lists no `Idempotency-Key`.
  - *Accepted, Later* (security review S6): a key is the integration's, across its API keys; a
    retry from a narrower key that may still call the operation gets the stored answer of the
    first, not one cut to its own narrowing. Binding the fingerprint to the key's permission set
    would need the scope read per request in A's middleware; the reference says so instead.
  - *Webhooks* (review 4): a receiver derives one key per message and action, unguessable
    (an HMAC of the `webhook-id` and the step under its own secret), and keeps its own dedupe on
    `webhook-id`, since retries run about 45 hours and resends 30 days, beyond the key's 24.
  - *The landing clause* (I18) is not part of this delivery's pull request.

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
