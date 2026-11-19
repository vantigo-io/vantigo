# An MCP server at /api/mcp — design

Revision 2, 2026-11-19 (revision 1 followed by an adversarial review against the code and the
MCP specification; its findings are folded in as **§11, which wins where it differs from the
text above**). Builds on API access phase 1 (`specs/2026-11-17-api-access-design.md`,
K1–K61; its §14 wins over its earlier text) and webhooks (`specs/2026-11-18-outgoing-webhooks-design.md`),
and on the research `research/2026-11-19-mcp-server.md` ("R"). The owner's questions in R §8
take R's recommended defaults here; §12 says what overturning each would change. Decisions
are numbered M*n*. Code references are against `988079aaf`. `srv/` is `apps/server/internal/`;
`host/` is `apps/host/frontend/src/`.

## 1. Purpose

An AI client (Claude Code, Cursor, VS Code, n8n, an in-house agent) connects to
`https://<workspace>/api/mcp` with an integration's API key and works with Vantigo through the
Model Context Protocol, under the integration's roles, narrowing and rate limit. It is the
REST API in the form agents expect; it grants nothing the key could not already do.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **A** | The integration's MCP setting (`off`/`read`/`read_write`), its operation, audit and screen; `MCP_ENABLED`; docs of the setting | — |
| **B** | The endpoint: protocol (modern and legacy), authentication, the scoped catalogue, `search_operations`, `describe_operation`, `read`, the request log; docs | A |
| **C** | The `write` tool, the connection snippet in the key dialog, the reference page's completion, the landing page | B |

**Not in this phase:** OAuth and ChatGPT's connectors (API access §12 "OAuth apps"); resources
and prompts; per-operation tools; SSE and server-initiated messages; elicitation; browser
clients; an MCP tool-call dashboard.

## 2. Decisions — the setting

- **M1 — `mcp_access` on the integration.** Column `identity.integrations.mcp_access`
  `text NOT NULL DEFAULT 'off' CHECK (mcp_access IN ('off','read','read_write'))`. Every
  existing and new integration is `off`: its keys were issued for REST, and an agent holding a
  key can be steered by the data it reads (R §7).
- **M2 — Changing it.** `PUT /api/v1/identity/integrations/{id}/mcp` with `{"mcpAccess": …}`
  and `If-Match` (the integration's `version`, which the change bumps). Owner-only
  (`policy:Owner+OwnerManagement`), `x-vantigo-api: never`, `x-vantigo-assistant: never`;
  an archived integration answers 409 `integration_archived`; audit
  `integration.mcp_changed` with before and after. The integration's read schema gains
  `mcpAccess`.
- **M3 — The effective mode** of a request is the narrower of the integration's `mcp_access` and
  the key: a **read-only key** (K11) is `read` even when the integration is `read_write`.
  `off` refuses everything at the endpoint (M9).
- **M4 — The kill switch.** `MCP_ENABLED` (default true) in `srv/config`, as the other feature
  switches. False: `/api/mcp` is not registered and answers the 404 problem every
  unregistered `/api/` path does. The Owner's screen then says MCP is switched off for the
  installation.

## 3. Decisions — the endpoint

- **M5 — Where it lives.** `POST /api/mcp`, registered in `srv/module/compose.go` beside
  `GET /api/openapi.json` and before the `/api/` catch-alls, implemented in `package module`
  (`apimcp.go`) so it can use `keyGate`, `apiDocuments` and the composed API handler. `GET`
  and `DELETE` answer `405` with `Allow: POST`. A request body is capped at 1 MiB.
- **M6 — Authentication.** The handler requires a **valid key** and nothing else: it calls
  `Access.Check` with the same anonymous-with-`APIRead` rule the document uses, then requires
  the principal to be a key principal (`Via.Kind == "api_key"`); an anonymous or session
  request is `401` with `WWW-Authenticate: Bearer realm="vantigo"` and a problem body saying a
  key is needed (a client configured with a header does not start OAuth on a 401, so the
  text must be plain). A refused key is recorded by `keys.refuse` as for any request. The
  integration's mode is then read through a new optional contract
  `contracts.MCPAccess` — `Mode(ctx, Principal) (mode string, err error)` — implemented by
  identity's `Access` and found by type assertion (K35's pattern). `off` is `403
  mcp_not_enabled`.
- **M7 — Protocol: hand-rolled, stateless, JSON only.** No dependency (R §4).
  - **Modern (2026-07-28):** a request whose `params._meta` carries
    `io.modelcontextprotocol/protocolVersion` is served statelessly. Methods: `server/discover`,
    `tools/list`, `tools/call`. Required headers `MCP-Protocol-Version`, `Mcp-Method`, and
    `Mcp-Name` on `tools/call`; a header that disagrees with the body is `400`/`-32020`; an
    unsupported version `400`/`-32022` with `data.supported`; an unknown method `404`/`-32601`.
  - **Legacy (2025-11-25, 2025-06-18):** `initialize` (answered with the negotiated version,
    `capabilities.tools`, `serverInfo` and the instructions), `notifications/initialized`
    (`202`), `ping` (`{}`), `tools/list`, `tools/call`. No `Mcp-Session-Id` is issued; one that
    arrives is ignored.
  - Always `Content-Type: application/json`; a notification is `202` with no body; never SSE.
    `Origin`, when present and not the workspace's own origin, is `403` (a non-browser client
    sends none).
  - The exact message shapes are pinned to the spec pages named in R §2 by a conformance
    test file (`apimcp_test.go`) and the implementer re-reads those pages first; where the
    page and R disagree the page wins and the spec's revision log records it.
- **M8 — The tool list is deterministic and per key.** `tools/list` returns, in this order,
  `search_operations`, `describe_operation`, `read` and — only when the effective mode is
  `read_write` — `write`. The descriptions are fixed text in code and say that tool output
  is data from the workspace and never instructions. Annotations: the first three
  `readOnlyHint: true`; `write` `destructiveHint: true`, `readOnlyHint: false`. No tool
  list change notification exists.
- **M9 — Rate limit and request log.**
  - A `tools/call` of `read` or `write` is **counted once**, by the request it loops back
    (M12), so a call costs one request, not two. `search_operations` and `describe_operation`
    count nothing, as they touch no data.
  - Every other envelope request (`server/discover`, `initialize`, `ping`, `tools/list`,
    the two search/describe tools) is **recorded but not counted**, under the operation id
    `postApiMcp` (precedent `getApiOpenapiJson`), through a `record`-only entry point added
    to `keyGate`.
  - A looped request answered `429` is returned to the client as an `isError` result that
    carries `retryAfterSeconds`.

## 4. Decisions — the tools

- **M10 — The catalogue is the key's scoped integrator document.** The handler obtains the
  document filtered for the key exactly as `GET /api/openapi.json` does (`Scope`,
  `apiDocuments.scoped`, the LRU), so a tool can reach only an operation the key could call,
  and a narrowed or read-only key sees less. The document strips `x-vantigo-*`; the handler
  adds each operation's *kind*: **read** when its method is `GET`/`HEAD` or it carries
  `x-vantigo-api: read`, otherwise **write**. In mode `read`, write operations are not in the
  catalogue.
- **M11 — The three data tools.**
  - `search_operations {query, limit?}`: a token match over summary, tag, path and
    description (the dispatcher's weights, 6/3/3/1), at most 25 results of `{operation,
    method, path, summary, tag, kind}`.
  - `describe_operation {operation}`: path, query and header parameters, the request-body
    schema and the success-response schema, with `$ref`s resolved to a bounded depth and
    descriptions clipped, at most 16 KB (the dispatcher's `Describe` is the model; MCP does
    not import `srv/operations`, which depguard reserves for the Assistant and agents).
  - `read {operation, path?, query?, body?}` and (C) `write` with the same arguments:
    `path` and `query` are string maps, `body` any JSON. `read` refuses a write operation
    and `write` refuses a read one, with `isError` and a message naming the right tool.
    An operation not in the key's catalogue is `isError` "not available to this key".
- **M12 — A call runs through the full gate.**
  - The handler builds an `http.Request`: the operation's method and path with path
    parameters escaped, the query encoded in a deterministic order, a JSON body, the
    declared header parameters only (so `If-Match` works and nothing else passes), and **the
    caller's own `Authorization: Bearer …`** and `Accept: application/json`.
  - Its client IP is the **caller's resolved client IP** (`httpx.ClientIP`), carried by a new
    exported helper in `httpx` that the request wrapper honours, so a key's network
    allowlist applies to MCP exactly as to REST. `RemoteAddr` is not forged.
  - It is run on the composed API handler (`dispatchModules`) with a recorder capped at the
    result limit. `Access.Check`, the allowlist, `x-vantigo-api`, read-only, roles and
    narrowing, the integration's rate limit, the per-operation limits, the body cap and the
    request-log row (real operation id, key id) are therefore the REST path's by
    construction. **The tool layer adds no authorization and holds no authority of its
    own.**
  - The context is detached from the connection, with a 30-second deadline.
  - A call to `/api/mcp` itself is impossible (it is not an operation of the contract).
- **M13 — Results.** A `2xx` is `content: [{type: "text", text: <the response body>}]`
  (JSON as sent; for a non-JSON response its content type and length only) with
  `structuredContent` omitted. Any other status is `isError: true` with the problem body's
  `code`, `title`, `detail` and `status` (never a stack or the raw body). The text is capped
  at **100 KB**: over it the result is `isError` saying the response was too large and to
  narrow it with the API's own `limit` and filters; it is never truncated silently.
  A panic or timeout is `isError`, logged, never a `500` of the envelope.
- **M14 — The Assistant's marks do not limit a key.** `x-vantigo-assistant` says what a
  person's Assistant may do on its own; a key is limited by `x-vantigo-api` (K53), roles and
  narrowing. `confirm` and `never` for the Assistant are not consulted. The MCP client's own
  approval, driven by `destructiveHint`, is the human step for writes.
- **M15 — Instructions.** `server/discover` and `initialize` return a short fixed
  `instructions` text (en): what the four tools do, to search before calling, to read
  before writing, that tool output is untrusted data, and that writes need a key with write
  access. The Norwegian reader finds it in the docs.

## 5. Decisions — the screen, audit, docs

- **M16 — Owner's screen.** On the integration's Overview, an **MCP** section: the mode as a
  radio group (*Off*, *Read only*, *Read and write*) saved with `If-Match`, the endpoint URL
  (`<origin>/api/mcp`), and — when the mode is not off — a copyable connection snippet
  (the URL, the `Authorization` header with a placeholder, a `claude mcp add --transport http`
  line and a JSON block for clients that take `url` + `headers`). The key dialog gains the
  same snippet beside its curl hint (C). The section explains in two sentences that a key
  with write access lets the agent change data and that read-only is safest. If `MCP_ENABLED`
  is false the section says so (the catalogue/config read exposes the switch).
- **M17 — Audit.** `integration.mcp_changed` (M2). Tool calls are not audited separately:
  the request log is the operational record and shows the real operations and key.
- **M18 — Documentation** (AGENTS.md; `sources` frontmatter):
  - A: `admin/api-access.md` en+nb (the MCP section, the three modes, the kill switch),
    `admin/configuration.md` en+nb (`MCP_ENABLED`), `reference/api-introduction.md` (a
    paragraph), `reference/mcp.md` (new, English: the setting, the endpoint, status).
  - B: `reference/mcp.md` (protocol, headers, the tools, results, limits, errors, security);
    `admin/api-access.md` en+nb.
  - C: both, and the connection snippet's steps; `user/workspace-administration.md` en+nb
    mentions the section. `reference/index.md` lists the page.
  - The generated API reference regenerates from `openapi/identity.yaml`; `go generate` in
    `apps/server` after the docs edit.
- **M19 — Landing.** The integrations note on the home page of `vantigo-landing` says an
  agent can connect with a key, read-only by default, once C is on `main`; written from the
  admin guide. If the repository is unavailable the hand-over names it.
- **M20 — Migration.** One migration (A) adds the column. Its number is the next free after
  `origin/main` **and every open PR** at the moment of enqueueing (`[[migration-number-collisions]]`).

## 6. Edge cases

- **A key revoked, disabled, expired or narrowed during a session:** nothing is cached
  (the protocol is stateless); the next call is judged afresh.
- **The mode changes between `tools/list` and `tools/call`:** the call is judged by the
  current mode; a `write` call under `read` is `isError` "not available".
- **Two keys of one integration:** share the integration's limit and mode; a read-only key
  is narrower.
- **A huge `tools/call` body:** the 1 MiB envelope cap answers `413` as any request would.
- **A path parameter containing `/` or `..`:** escaped as one segment; the router matches
  the operation's template, so it cannot reach another route.
- **Prompt injection in a record:** cannot be prevented; bounded by the mode, `read`/`write`
  split, roles, narrowing, rate limit and the log (R §7).
- **Clients that need OAuth (ChatGPT):** cannot connect in this phase; the `401` is a plain
  bearer challenge.
- **Legacy client sends `Mcp-Session-Id`:** ignored. `GET` for a stream: `405`.

## 7. Testing

- Conformance tests per message of M7 (modern and legacy), header mismatches, versions,
  unknown methods, notifications, `405`s, `Origin`.
- Authorization: no key, session cookie, bad key (`401`, recorded), revoked/expired/disabled,
  `off` → `403 mcp_not_enabled`, `read` hides and refuses `write`, read-only key under
  `read_write`, narrowed key sees and reaches less, a network-restricted key refused from
  another address through MCP (M12), an operation outside the catalogue refused.
- Dispatch: a `read` returns the REST result; a `write` through a key that lacks the
  permission is `isError` with the 403 problem; `If-Match`; path escaping; the request log
  row has the real operation id and key id; the call counts once against the limit,
  `tools/list` zero; a `429` becomes `isError` with `retryAfterSeconds`; result cap; timeout.
- Tools: search ranking and cap, describe size cap and `$ref` resolution, deterministic
  ordering of `tools/list`.
- The setting: `PUT` happy path, `If-Match` 412, Owner-only, key refused, archived 409, audit
  snapshot; `MCP_ENABLED=false` → 404.
- Frontend (vitest): the section, mode change, snippet content, nb/en parity.
- The manual check with a real client (`claude mcp add`) against a running instance.

## 8. Later

OAuth and ChatGPT; resources (`knowledge://article/{id}`) and prompts; an Origin allow-list for
trusted browser clients; tool-call counts in the request log's UI; per-key MCP mode; swapping
the hand-rolled core for the official SDK if the specification outgrows it.

## 9. Revision log

- **1** (2026-11-19): first draft from R.
- **2** (2026-11-19): the review's 19 findings, §11; the deliveries are now two.
- **3** (2026-11-19, delivery A built): the specification pages re-read
  (2026-07-28 `basic/transports/streamable-http`, `basic/versioning`, `basic/index`,
  `server/discover`, `server/tools`, `server/utilities/caching`; 2025-11-25 `basic/lifecycle`,
  `basic/transports`). Where they say more than M7/M28, the pages win:
  - `supportedVersions` (and `-32022`'s `data.supported`) lists `2026-07-28`, `2025-11-25` and
    `2025-06-18`: the endpoint is dual-era, as the versioning page's example server is. A
    request whose `_meta` names a legacy version is served with the legacy method set (the
    header must equal it), a row M28's table lacked.
  - A modern request whose `_meta` lacks `io.modelcontextprotocol/clientCapabilities` is
    `400`/`-32602` (`basic/index`, per-request fields).
  - A modern request (it has `_meta`) without `MCP-Protocol-Version` is `-32020`: the transport
    page lists a missing required header as a header-validation failure. M28's "header missing
    → 400" rule applies to a request with no `_meta`: `-32022` for `server/discover`, `-32600`
    otherwise.
  - A notification is `202` after the version checks, with no `Mcp-Method` check: the
    revision defines no header rule for notification POSTs.
  - `-32601` is `404` for a modern request, `200` for a legacy one: under 2025-11-25 a `404`
    tells the client its session is gone. `-32602` for an unknown tool is `200` (a JSON-RPC
    error answer; no page gives it a status).
  - Every modern result carries `serverInfo` in its `_meta` (`basic/index`, SHOULD), not only
    `server/discover`'s. `server/discover`'s `ttlMs` is an hour.
  Choices the spec left open: the per-IP policy of M22 is 1,200 a minute; `read` takes a
  `headers` argument for M31's three headers; the read tools' annotations also say
  `destructiveHint: false`, `idempotentHint: true`, `openWorldHint: false`; an empty `2xx` is
  `{"status": N}`; the envelope of a looped-back `read` is recorded (`postApiMcp`, record-only)
  beside the counted row of the operation it called. The kind is read with the router's own
  rule, factored out of `Router.HandleFunc` as `setAPIRule`; the dispatcher's tokenizer,
  `Describe` compactor and JSON checks are exported from `internal/operations` (`shared.go`).
  `IntegrationDetail` carries both `mcpAccess` and `mcpAvailable`; `Integration` (the list)
  does not.

## 10. What overturning a default changes

1. *Opt-in per integration* (R Q1): without `mcp_access` the delivery A shrinks to the kill
   switch and a key could use MCP at once; the safest default is lost.
2. *Tool surface* (Q2): per-operation tools need a ~1,000-tool list or a tag filter and break
   the tool-count limits of clients.
3. *Assistant marks* (Q3): honouring them hides 186 operations from MCP only (28 of them
   Knowledge) and needs the unstripped contract in the catalogue.
4. *Protocol* (Q4): dropping legacy loses Claude Code/Cursor versions older than the
   2026-07-28 revision; adding SSE adds a stream and a keep-alive.
5. *Auth* (Q5): OAuth adds protected-resource metadata, a token endpoint's trust and audience
   binding.
6. *Result size* (Q7), *rate limit* (Q8): constants and the `record`-only gate.

## 11. Amendments (revision 2, from the review)

- **M21 (replaces §1's table, amends M18, M20): two deliveries.**
  - **A** — the whole read-only MCP: the column, `PUT …/mcp`, audit, `MCP_ENABLED` (exposed to the
    screen as `IntegrationDetail.mcpAvailable`), the Overview section, the endpoint with both
    protocol eras, authentication, the scoped catalogue, `search_operations`,
    `describe_operation`, `read`, the request log, `reference/mcp.md`, `admin/api-access.md`,
    `admin/configuration.md`, `reference/api-introduction.md`. Until B the mode `read_write`
    behaves as `read`, and the pages say so.
  - **B** — the `write` tool, the connection snippet in the key dialog, the docs' completion,
    `user/workspace-administration.md`, the landing page.
  - The next free migration is `00210` today (#398 holds `00209`); check again before
    enqueueing. `module/apimcp.go` and every new file join the `sources` of `reference/mcp.md`
    and `admin/api-access.md`; `apikeygate.go` is already in `api-introduction.md`'s sources.
- **M22 (amends M9): every authenticated envelope request is counted.** `keyGate.serve` counts it
  once under `postApiMcp`, **except** a `tools/call` of `read`/`write` that reaches the
  loopback, which the loopback counts. A call refused before the loopback (unknown operation,
  wrong tool, bad arguments) is counted and recorded as an envelope request. The claim that a
  flood is bounded by "key-failure handling" is withdrawn: an unknown key is refused with
  nothing counted today, so `/api/mcp` gets a per-IP `Limiter.Hit` policy (as the other
  per-operation limits) ahead of `Check`. `Mode`, `Scope` and the key read are one query
  (`GetAPIKeyScope` joined with the integration's `mcp_access`), returning mode, permissions and
  read-only together.
- **M23 (amends M10, M11): a parsed index, not bytes.** `apiDocuments.scoped` caches marshalled
  bytes. The handler keeps, per scope key, a **parsed index** in its own LRU (operation id →
  method, path template, parameters, body and response schemas, kind, search tokens),
  built once per scope; no request parses the document. Operations with a non-JSON request body
  (the 26 multipart ones) are not in the catalogue.
- **M24 (amends M10): the kind comes from the contract.** `integrator.Build` strips every
  `x-vantigo-*`, so the kind is taken from the unstripped contract by operation id with the
  router's own rule (`router.go:279-299`): **read when `Rule.API == APIRead`**. A test asserts
  this for every keyable operation.
- **M25 (amends M12): what enforces the mode.** For a key that REST lets write, under
  `mcp_access = read` it is the tool layer's kind check alone that stops a write; the
  statement "the tool layer adds no authorization" holds for permissions, narrowing and
  limits, and the mode is stated as the one thing MCP adds, tested against the router's rule. The
  loopback refuses a non-read kind before it is dispatched, and `Mode` returns the effective
  read-only flag for the case of a read-only key.
- **M26 (replaces M12's client-IP helper): no `httpx` API is added.** The loopback's context is
  `context.WithoutCancel` of the envelope request's context with a 30-second deadline: the
  forwarded client address and the trace id survive, so the allowlist applies and the log
  rows link to the envelope. The MCP handler puts no principal, grant or request on its own
  context before the loopback (test). An exported setter for the client address would be a
  forgery footgun for every module and is rejected.
- **M27 (amends M6): the 401 is the handler's.** A request with no key, or whose principal is not
  `ViaAPIKey` (a session cookie is admitted by the document rule), is answered by the MCP
  handler itself: `401`, `WWW-Authenticate: Bearer realm="vantigo"`, problem code
  `api_key_required`. It never goes through `Access.Reject`, which would clear a signed-in
  browser's session cookie. A refused *key* is recorded by `keys.refuse` as for any request.
- **M28 (amends M7): the protocol details.**
  - Modern results carry `resultType: "complete"`. `server/discover` returns
    `supportedVersions`, `capabilities` and `instructions`, with `serverInfo` under
    `_meta["io.modelcontextprotocol/serverInfo"]`. `server/discover` and `tools/list` carry
    `ttlMs` and `cacheScope`; `tools/list` varies by key, so it is **`cacheScope: "private"`,
    `ttlMs: 0`** and `server/discover` `public` with a long `ttlMs` only because it carries
    nothing key-specific. `-32022` carries `data.supported` and `data.requested`.
  - Classification of each (stateless) request: `initialize` → legacy, negotiating down to
    2025-11-25 or 2025-06-18; `_meta` version present → modern, the header must equal it
    (`-32020`); no `_meta` and header 2025-11-25 or 2025-06-18 → the legacy method set; no
    `_meta` and header 2026-07-28 → `-32020`; header missing or another value → `400`
    (`-32022` with `supported` for a modern-looking request, a plain `400` otherwise). A missing
    `Mcp-Method`/`Mcp-Name` is `-32020`; `Mcp-Name` in the `=?base64?…?=` form is decoded.
  - **Origin:** `http.CrossOriginProtection` around `api` (`server.go:56-63`) already refuses a
    cross-origin browser request with `403` before any JSON-RPC answer; MCP adds no second
    check of its own, and the docs say browser clients cannot connect. A request with no `Origin`
    (every non-browser client) passes.
  - The conformance tests pin each shape to the spec pages named in R; where they disagree the
    pages win and §9 records it.
- **M29 (amends M5): wiring.** The handler is registered as the method-less pattern `/api/mcp` on
  `outer`, **bound to `api` after `dispatchModules`** (as `ops.Bind` is), because `api` is built after
  `outer` is filled (`compose.go:682-692`); `GET`/`DELETE` answer `405` with `Allow: POST`
  (a `"POST /api/mcp"` pattern would let GET reach the 404 catch-all). A composition with no
  `Access` (`IntegratorDocument`) omits it.
- **M30 (corrects M11): why the dispatcher is not imported.** `internal/module` may import
  `internal/operations` (it does already); it is not reused because it is bound to a person's
  grant and is not filtered by a key's scope. Its tokenizer weights and the `Describe` limits
  are the models, and the shared pieces are used where exported.
- **M31 (amends M11): arguments.** `path` and `query` values are a string or a list of strings
  (the ten array parameters). Header parameters are limited to `If-Match`, `If-None-Match` and
  `Idempotency-Key`, refused when a value contains CR or LF; `Authorization`, `Cookie` and `Host`
  are never settable. An unknown tool name (and `write` while it is unlisted) is the protocol
  error `-32602`, not `isError`.
- **M32 (amends M13): the cap is on the escaped text.** The 100 KB limit counts the bytes of
  the JSON-escaped `text` field, not of the response body.
- **M33 (amends M16): the URL.** `origin + appUrl("/api/mcp")`, as the curl hint builds it, so a
  base path is honoured.
- **M34 (amends API access §12): the Assistant's marks.** M14 amends API access §12, which sketched
  MCP as the operations dispatcher "with the `x-vantigo-assistant` flags": keys follow
  `x-vantigo-api` instead.
